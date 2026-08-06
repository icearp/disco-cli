package gcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	cloudquotas "cloud.google.com/go/cloudquotas/apiv1"
	"cloud.google.com/go/cloudquotas/apiv1/cloudquotaspb"
	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/iterator"
	serviceusage "google.golang.org/api/serviceusage/v1"
)

func init() {
	// No registerType. A quota limit is a value, not a resource: nothing
	// provisions it, it has no graph edges, and it is queried by service and by
	// proximity to the limit. It lands in the `quotas` table instead.
	//
	// Not opt-in, matching Azure rather than AWS: every GCP scan records
	// quotas. GCP's serviceEntry carries no optIn field and *gcp.Scanner does
	// not implement providers.ServiceQuotasIncluder, so always-on needs
	// neither.
	registerService(serviceEntry{
		name: quotaServiceName,
		fn:   scanCloudQuotas,
	})
}

const (
	// quotaServiceName is the registry name and the scope-line label.
	quotaServiceName = "gcp:cloudquotas"

	// quotaSubAccountType is FOCUS SubAccountType for every GCP quota: the
	// container an account_id names is a project, always. AWS writes
	// "Account", Azure "Subscription".
	quotaSubAccountType = "Project"

	// maxConcurrentQuotaServices caps the per-enabled-service ListQuotaInfos
	// fan-out within one project. Matches maxConcurrentServices.
	maxConcurrentQuotaServices = 10

	// quotaRegionDimension and quotaZoneDimension are the two dimension keys
	// Cloud Quotas documents by name; everything else is service-specific.
	quotaRegionDimension = "region"
	quotaZoneDimension   = "zone"
)

// scanCloudQuotas records service quota *limits* via Cloud Quotas v1, storing
// them in the `quotas` table.
//
// ListQuotaInfos has no wildcard parent — it names exactly one service, and
// listing across containers is not allowed — so enabled services are
// enumerated from Service Usage first and the quota listing fans out over
// them.
//
// Usage is deliberately absent: Cloud Quotas reports none on QuotaInfo, which
// is what makes the version chain bump only on a real quota change. Any future
// field that looks like a limit and moves like a gauge belongs in the
// attributes remainder, which the store's change comparison does not read.
func scanCloudQuotas(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	su, err := serviceusage.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("serviceusage:NewService: %w", err)
	}
	// REST rather than gRPC: it shares the HTTP transport and the fake-server
	// test idiom every other GCP scanner uses.
	qc, err := cloudquotas.NewRESTClient(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudquotas:NewRESTClient: %w", err)
	}
	defer func() { _ = qc.Close() }()
	return scanCloudQuotasWithClients(ctx, p, st, scanID, su, qc)
}

// scanCloudQuotasWithClients is the scanner body, taking both clients so tests
// can point concrete ones at a fake server.
func scanCloudQuotasWithClients(ctx context.Context, p *project, st *store.Store, scanID string, su *serviceusage.Service, qc *cloudquotas.Client) (total, inserted int, err error) {
	services, err := enabledServiceNames(ctx, su, p)
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, quotaServiceName, p.ID, err)
		}
		return 0, 0, err
	}
	if len(services) == 0 {
		return 0, 0, nil
	}

	var (
		mu      sync.Mutex
		rows    []*store.Quota
		outcome quotaListOutcome
	)
	if err := forEachItem(ctx, maxConcurrentQuotaServices, services, func(gctx context.Context, service string) error {
		batch, err := listServiceQuotas(gctx, qc, p, scanID, service)
		if err != nil {
			// A service the caller cannot read must not cost the project the
			// other thirty-nine, so per-service failures accumulate instead of
			// aborting the fan-out.
			outcome.record(service, err)
			return nil
		}
		if len(batch) == 0 {
			return nil
		}
		mu.Lock()
		rows = append(rows, batch...)
		mu.Unlock()
		return nil
	}); err != nil {
		return 0, 0, err
	}
	if err := outcome.resolve(st, p.ID, len(services)); err != nil {
		return 0, 0, err
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertQuotas(rows)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert quotas: %w", err)
	}
	return len(rows), n, nil
}

// enabledServiceNames returns the bare service names enabled in the project,
// e.g. "compute.googleapis.com". Sorted, so the fan-out order does not depend
// on API response order.
//
// The name comes from Config.Name rather than from splitting the resource name
// ("projects/123/services/compute.googleapis.com").
func enabledServiceNames(ctx context.Context, su *serviceusage.Service, p *project) ([]string, error) {
	var out []string
	err := su.Services.List("projects/"+p.ID).
		Filter("state:ENABLED").
		PageSize(200).
		Pages(ctx, func(page *serviceusage.ListServicesResponse) error {
			for _, s := range page.Services {
				if s == nil || s.Config == nil || s.Config.Name == "" {
					continue
				}
				out = append(out, s.Config.Name)
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// listServiceQuotas pages ListQuotaInfos for one service. The location
// component must be "global" — regional information arrives inside
// DimensionsInfos, not in the parent.
func listServiceQuotas(ctx context.Context, qc *cloudquotas.Client, p *project, scanID, service string) ([]*store.Quota, error) {
	parent := fmt.Sprintf("projects/%s/locations/global/services/%s", p.ID, service)
	it := qc.ListQuotaInfos(ctx, &cloudquotaspb.ListQuotaInfosRequest{Parent: parent})
	var out []*store.Quota
	for {
		qi, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("cloudquotas:ListQuotaInfos %s: %w", service, err)
		}
		out = append(out, quotaRows(p, scanID, qi)...)
	}
}

// quotaRows maps one QuotaInfo onto one store.Quota per DimensionsInfo entry.
//
// The value lives at DimensionsInfo.Details.Value, not on QuotaInfo, so a
// quota code carries a different limit per dimension set and N dimension sets
// produce N rows. A QuotaInfo with no DimensionsInfos emits nothing — there is
// no value to record.
func quotaRows(p *project, scanID string, qi *cloudquotaspb.QuotaInfo) []*store.Quota {
	if qi == nil || qi.GetQuotaId() == "" {
		return nil
	}
	out := make([]*store.Quota, 0, len(qi.GetDimensionsInfos()))
	for _, di := range qi.GetDimensionsInfos() {
		if di == nil {
			continue
		}
		out = append(out, quotaRow(p, scanID, qi, di))
	}
	return out
}

// quotaRow maps one (QuotaInfo, DimensionsInfo) pair onto store.Quota.
//
// DefaultValue stays nil: Cloud Quotas reports no default. A constant NULL can
// never split a version chain, but it does mean the "raised above default"
// filter can never match a GCP quota.
//
// Adjustable is !IsFixed — GCP's own statement of whether a higher value can
// be requested.
func quotaRow(p *project, scanID string, qi *cloudquotaspb.QuotaInfo, di *cloudquotaspb.DimensionsInfo) *store.Quota {
	region := *regionGlobal
	if r := di.GetDimensions()[quotaRegionDimension]; r != "" {
		region = r
	}
	name := qi.GetQuotaDisplayName()
	if name == "" {
		name = qi.GetQuotaId()
	}
	subAccountType := quotaSubAccountType

	q := &store.Quota{
		Provider:     "gcp",
		AccountID:    p.ID,
		AccountName:  &p.Name,
		Region:       region,
		ServiceCode:  qi.GetService(),
		QuotaCode:    qi.GetQuotaId(),
		DimensionKey: quotaDimensionKey(di.GetDimensions()),
		Name:         name,
		Unit:         strp(qi.GetMetricUnit()),
		Adjustable:   !qi.GetIsFixed(),
		// GCP quotas are metric-scoped, so ResourceType stays nil — which is
		// how FOCUS spells "not resource-scoped".
		AvailabilityZone: strp(di.GetDimensions()[quotaZoneDimension]),
		SubAccountType:   &subAccountType,
		AttributesJSON:   mustJSON(quotaAttributes(qi, di)),
		DiscoveredBy:     scanID,
	}
	if d := di.GetDetails(); d != nil {
		v := float64(d.GetValue())
		q.Value = &v
	}
	if unit, value, ok := parseGCPRefreshInterval(qi.GetRefreshInterval()); ok {
		q.PeriodUnit, q.PeriodValue = &unit, &value
	}
	return q
}

// quotaDimensionKey encodes a dimension map into the row's stable natural-key
// component, or "" when the limit is undimensioned.
//
// Sorted by key: Dimensions is a Go map, so an insertion-order encoding would
// produce a different key on the next scan, re-key every row and strand each
// predecessor as a current row no later scan can reach.
//
// "region" is omitted — it is already its own key column.
//
// Separators are percent-escaped on both halves of every pair. store.QuotaID
// joins its components with "|" and this encoding joins pairs with "," and
// halves with "=", so an unescaped separator arriving in a provider-supplied
// value could make two distinct quotas hash to one root.
func quotaDimensionKey(dims map[string]string) string {
	keys := make([]string, 0, len(dims))
	for k := range dims {
		if k == quotaRegionDimension {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, escapeDimensionToken(k)+"="+escapeDimensionToken(dims[k]))
	}
	return strings.Join(parts, ",")
}

// dimensionTokenEscaper percent-escapes the three separators plus the escape
// character itself. strings.Replacer never rescans its own output, so "%"
// expanding to "%25" cannot be re-escaped by a later rule.
var dimensionTokenEscaper = strings.NewReplacer("%", "%25", "|", "%7C", ",", "%2C", "=", "%3D")

func escapeDimensionToken(s string) string { return dimensionTokenEscaper.Replace(s) }

// parseGCPRefreshInterval reads the rate window Cloud Quotas reports as free
// text into the (unit, value) pair the columns store, so a GCP "minute" and an
// AWS MINUTE/1 land on the same two values. Observed forms: "minute", "day",
// "10 seconds", and empty for allocation (non-rate) quotas.
//
// Anything not resolving to one of the seven units the PG CHECK accepts
// returns false, leaving both columns NULL while the raw string survives in
// the attributes remainder. The failure that matters is accepting something
// the constraint will reject: SQLite carries no such CHECK, so disco's own
// store tests cannot see it.
func parseGCPRefreshInterval(s string) (unit string, value int, ok bool) {
	fields := strings.Fields(strings.ToLower(s))
	switch len(fields) {
	case 1:
		u, ok := quotaPeriodUnit(fields[0])
		if !ok {
			return "", 0, false
		}
		return u, 1, true
	case 2:
		n, err := strconv.Atoi(fields[0])
		if err != nil || n <= 0 {
			return "", 0, false
		}
		u, ok := quotaPeriodUnit(fields[1])
		if !ok {
			return "", 0, false
		}
		return u, n, true
	default:
		return "", 0, false
	}
}

// quotaPeriodUnit normalizes a unit word to the lowercase singular the column
// stores, accepting the plural Cloud Quotas uses with a count ("10 seconds").
func quotaPeriodUnit(word string) (string, bool) {
	switch unit := strings.TrimSuffix(word, "s"); unit {
	case "microsecond", "millisecond", "second", "minute", "hour", "day", "week":
		return unit, true
	default:
		return "", false
	}
}

// quotaListOutcome accumulates per-service ListQuotaInfos failures so the scan
// record carries ONE entry per project rather than one per service.
//
// The aggregation is the point, not tidiness. A systematic failure — a missing
// grant, the Cloud Quotas API off — fails every enabled service, and a project
// enables tens of them. Tens of near-identical rows on one scan record is not
// a louder signal than one row but a quieter one: it buries every other
// finding and inflates the stored payload.
//
// record is safe for concurrent use; resolve is called after the fan-out has
// joined.
type quotaListOutcome struct {
	mu           sync.Mutex
	failures     int
	first        error  // first failure to arrive, i.e. an example, not a stable choice
	firstService string // its service
}

func (o *quotaListOutcome) record(service string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failures++
	if o.first == nil {
		o.first, o.firstService = err, service
	}
}

// resolve turns the accumulated failures into either a scanner-level sentinel
// or one aggregated warning, and reports the latter.
//
// Every service failing on the same project-wide condition is not tens of
// service problems but one project fact — the Cloud Quotas API not enabled, or
// billing off — so it escalates to the sentinel the dispatcher renders as
// "(project: disabled)" instead of warning.
func (o *quotaListOutcome) resolve(st *store.Store, projectID string, attempted int) error {
	if o.failures == 0 {
		return nil
	}
	if o.failures == attempted {
		if isBillingDisabled(o.first) {
			return markBillingDisabled(o.first)
		}
		if isAPINotEnabled(o.first) {
			return markServiceDisabled(o.first)
		}
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "gcp",
		Service:  quotaServiceName,
		Scope:    projectID,
		Message: fmt.Sprintf("%d of %d service(s) failed to list quotas; example %s: %s",
			o.failures, attempted, o.firstService, o.first),
	})
	return nil
}

// quotaAttributesRow is the stored attributes shape: everything Cloud Quotas
// reports on a QuotaInfo, with DimensionsInfos narrowed to the single entry
// this row was built from. Storing all N in each of N rows would grow the
// widest column quadratically.
//
// Hand-mirrored rather than marshalling the generated message: its JSON tags
// are snake_case, its enums marshal as bare integers, and narrowing the slice
// would copy a struct carrying protoimpl's DoNotCopy.
type quotaAttributesRow struct {
	Name                     string                    `json:"name,omitempty"`
	QuotaID                  string                    `json:"quotaId,omitempty"`
	Metric                   string                    `json:"metric,omitempty"`
	Service                  string                    `json:"service,omitempty"`
	IsPrecise                bool                      `json:"isPrecise"`
	IsFixed                  bool                      `json:"isFixed"`
	IsConcurrent             bool                      `json:"isConcurrent"`
	RefreshInterval          string                    `json:"refreshInterval,omitempty"`
	ContainerType            string                    `json:"containerType,omitempty"`
	Dimensions               []string                  `json:"dimensions,omitempty"`
	MetricDisplayName        string                    `json:"metricDisplayName,omitempty"`
	QuotaDisplayName         string                    `json:"quotaDisplayName,omitempty"`
	MetricUnit               string                    `json:"metricUnit,omitempty"`
	ServiceRequestQuotaURI   string                    `json:"serviceRequestQuotaUri,omitempty"`
	QuotaIncreaseEligibility *quotaEligibilityAttrs    `json:"quotaIncreaseEligibility,omitempty"`
	DimensionsInfo           *quotaDimensionsInfoAttrs `json:"dimensionsInfo,omitempty"`
}

type quotaEligibilityAttrs struct {
	IsEligible          bool   `json:"isEligible"`
	IneligibilityReason string `json:"ineligibilityReason,omitempty"`
}

type quotaDimensionsInfoAttrs struct {
	Dimensions          map[string]string  `json:"dimensions,omitempty"`
	Details             *quotaDetailsAttrs `json:"details,omitempty"`
	ApplicableLocations []string           `json:"applicableLocations,omitempty"`
}

type quotaDetailsAttrs struct {
	Value       int64              `json:"value"`
	RolloutInfo *quotaRolloutAttrs `json:"rolloutInfo,omitempty"`
}

// quotaRolloutAttrs is kept nested rather than flattened to a bool: the field
// is present only while a rollout will change the effective limit, so its
// presence carries information a false would not.
type quotaRolloutAttrs struct {
	OngoingRollout bool `json:"ongoingRollout"`
}

func quotaAttributes(qi *cloudquotaspb.QuotaInfo, di *cloudquotaspb.DimensionsInfo) quotaAttributesRow {
	out := quotaAttributesRow{
		Name:                   qi.GetName(),
		QuotaID:                qi.GetQuotaId(),
		Metric:                 qi.GetMetric(),
		Service:                qi.GetService(),
		IsPrecise:              qi.GetIsPrecise(),
		IsFixed:                qi.GetIsFixed(),
		IsConcurrent:           qi.GetIsConcurrent(),
		RefreshInterval:        qi.GetRefreshInterval(),
		ContainerType:          qi.GetContainerType().String(),
		Dimensions:             qi.GetDimensions(),
		MetricDisplayName:      qi.GetMetricDisplayName(),
		QuotaDisplayName:       qi.GetQuotaDisplayName(),
		MetricUnit:             qi.GetMetricUnit(),
		ServiceRequestQuotaURI: qi.GetServiceRequestQuotaUri(),
	}
	if e := qi.GetQuotaIncreaseEligibility(); e != nil {
		out.QuotaIncreaseEligibility = &quotaEligibilityAttrs{
			IsEligible:          e.GetIsEligible(),
			IneligibilityReason: e.GetIneligibilityReason().String(),
		}
	}
	if di != nil {
		info := &quotaDimensionsInfoAttrs{
			Dimensions:          di.GetDimensions(),
			ApplicableLocations: di.GetApplicableLocations(),
		}
		if d := di.GetDetails(); d != nil {
			info.Details = &quotaDetailsAttrs{Value: d.GetValue()}
			if r := d.GetRolloutInfo(); r != nil {
				info.Details.RolloutInfo = &quotaRolloutAttrs{OngoingRollout: r.GetOngoingRollout()}
			}
		}
		out.DimensionsInfo = info
	}
	return out
}
