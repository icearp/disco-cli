package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationsignals"
	applicationsignalstypes "github.com/aws/aws-sdk-go-v2/service/applicationsignals/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeApplicationSignalsSLO, Service: "applicationsignals"})
	registerType(restype.Descriptor{Type: TypeApplicationSignalsGroupingConfiguration, Service: "applicationsignals"})
	registerType(restype.Descriptor{Type: TypeCloudWatchService, Service: "cloudwatch"})
	registerType(restype.Descriptor{Type: TypeApplicationSignalsInstrumentationConfiguration, Service: "applicationsignals"})
	registerService(serviceEntry{
		name: "aws:applicationsignals",
		fn:   scanApplicationSignals,
	})
}

const (
	appSignalsListSLOsOp                                = "applicationsignals:ListServiceLevelObjectives"
	appSignalsListGroupingOp                            = "applicationsignals:ListGroupingAttributeDefinitions"
	appSignalsListServicesOp                            = "applicationsignals:ListServices"
	appSignalsListInstrumentationConfigurationsOp       = "applicationsignals:ListInstrumentationConfigurations"
	appSignalsTagsOp                                    = "applicationsignals:ListTagsForResource"
	appSignalsSLOPageLimit                        int32 = 50  // model cap (MaxResults 1..50)
	appSignalsServicesPageLimit                   int32 = 100 // model cap (MaxResults 1..100)
	appSignalsInstrumentationPageLimit            int32 = 100 // model cap (MaxResults 1..100)
)

// appSignalsNotFound is the not-found code ListTagsForResource models, and
// the one ListInstrumentationConfigurations models for a service gone since
// ListServices reported it.
var appSignalsNotFound = []string{"ResourceNotFoundException"}

type applicationSignalsAPI interface {
	ListServiceLevelObjectives(context.Context, *applicationsignals.ListServiceLevelObjectivesInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListServiceLevelObjectivesOutput, error)
	ListGroupingAttributeDefinitions(context.Context, *applicationsignals.ListGroupingAttributeDefinitionsInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListGroupingAttributeDefinitionsOutput, error)
	ListServices(context.Context, *applicationsignals.ListServicesInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListServicesOutput, error)
	ListInstrumentationConfigurations(context.Context, *applicationsignals.ListInstrumentationConfigurationsInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListInstrumentationConfigurationsOutput, error)
	ListTagsForResource(context.Context, *applicationsignals.ListTagsForResourceInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListTagsForResourceOutput, error)
}

// applicationSignalsService is an instrumented service stored by the services
// phase: the name and environment ListInstrumentationConfigurations keys on,
// and the service row's NativeID.
type applicationSignalsService struct{ name, environment, nativeID string }

// applicationSignalsServiceNativeID synthesizes a stable NativeID for an
// Application Signals service. The SDK exposes no ARN — identity is the
// required KeyAttributes map (Type/Name/Environment/…). Sorting into "k=v"
// pairs keeps the NativeID stable regardless of Go's map-iteration order.
func applicationSignalsServiceNativeID(partition, region, acct string, keyAttrs map[string]string) string {
	keys := make([]string, 0, len(keyAttrs))
	for k := range keyAttrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+keyAttrs[k])
	}
	return arnFor(partition, "application-signals", region, acct, "service/"+strings.Join(pairs, ";"))
}

// applicationSignalsGroupingConfigurationNativeID synthesizes the singleton
// per-(account, region) NativeID for the grouping configuration. The SDK
// exposes no ARN; body enumerated via ListGroupingAttributeDefinitions.
func applicationSignalsGroupingConfigurationNativeID(partition, region, acct string) string {
	return arnFor(partition, "application-signals", region, acct, "grouping-configuration")
}

// scanApplicationSignals runs the SLO, grouping-configuration and services
// phases, then lists the instrumentation configurations of every instrumented
// service the services phase stored. Every phase runs, one at a time: a
// phase's error does not stop the later ones; a failed write and ctx's end do
// (runHostPhases). The SLOs and the instrumentation configurations have their
// tags read per row (appSignalsTags), and the tag denials of the whole scan
// are one warning.
func scanApplicationSignals(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := applicationsignals.NewFromConfig(acct.cfg, func(o *applicationsignals.Options) { o.Region = region })
	tags := appSignalsTags(client).sharingDenials()
	var services []applicationSignalsService
	total, err := runHostPhases(ctx, st, acct, region, "applicationsignals", 0, []hostPhase{
		{op: appSignalsListSLOsOp, run: func(p *phaseTotals) bool {
			return scanApplicationSignalsSLOs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, appSignalsListGroupingOp, func() (int, error) {
			return scanApplicationSignalsGroupingConfiguration(ctx, client, acct, region, st, scanID)
		}),
		{op: appSignalsListServicesOp, run: func(p *phaseTotals) bool {
			t, stored, err := scanApplicationSignalsServices(ctx, client, acct, region, st, scanID, time.Now())
			services = stored
			return p.addPhase(appSignalsListServicesOp, t, err)
		}},
		{op: appSignalsListInstrumentationConfigurationsOp, run: func(p *phaseTotals) bool {
			return scanApplicationSignalsInstrumentationConfigurations(ctx, client, acct, region, st, scanID, services, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// appSignalsTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its list element carries. The Service Reference lists
// the slo and instrumentationConfig resources under ListTagsForResource and
// TagResource with aws:ResourceTag, and both Create* inputs take Tags; no
// list element carries them. The op does not page. ResourceNotFoundException,
// the op's only modeled error besides ThrottlingException, drops a resource
// deleted after it was listed.
func appSignalsTags(client applicationSignalsAPI) rowTagReader {
	return rowTagReader{
		op:       appSignalsTagsOp,
		notFound: appSignalsNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &applicationsignals.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t applicationsignalstypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanApplicationSignalsServices lists Application Signals services seen in
// the trailing 24h, writing each page as it is read. ListServices requires a
// time window and returns only services with telemetry in it — a service
// quiet longer than the window won't appear (inherent API limit). Services
// carry no ARN; NativeID is synthesized from KeyAttributes, so a service
// listed without them is not stored and is warned about. A failed page or a
// repeated NextToken keeps the pages written before it; a denial is warned
// about. It returns the instrumented services (KeyAttributes Type "Service")
// stored before any failure.
func scanApplicationSignalsServices(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string, now time.Time) (total int, services []applicationSignalsService, err error) {
	start := now.Add(-24 * time.Hour)
	var skips notStoredSkips
	listErr := eachTokenPage(ctx, func(ctx context.Context, token *string) ([]applicationsignalstypes.ServiceSummary, *string, error) {
		out, err := client.ListServices(ctx, &applicationsignals.ListServicesInput{
			StartTime: &start, EndTime: &now,
			MaxResults: sdkaws.Int32(appSignalsServicesPageLimit), NextToken: token,
		})
		if err != nil {
			return nil, nil, err
		}
		return out.ServiceSummaries, out.NextToken, nil
	}, func(page []applicationsignalstypes.ServiceSummary) error {
		batch := make([]*store.Resource, 0, len(page))
		var instrumented []applicationSignalsService
		for _, s := range page {
			if len(s.KeyAttributes) == 0 {
				skips.skipped("services have no KeyAttributes", noIdentifier)
				continue
			}
			name := s.KeyAttributes["Name"]
			nativeID := applicationSignalsServiceNativeID(acct.partition(), region, acct.ID, s.KeyAttributes)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudWatchService,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
			// Only Type "Service" names an application reporting its own
			// service.name, which is what an instrumentation configuration
			// targets; RemoteService and AWS::Service are callees seen from it.
			env := s.KeyAttributes["Environment"]
			if s.KeyAttributes["Type"] == "Service" && name != "" && env != "" {
				instrumented = append(instrumented, applicationSignalsService{name: name, environment: env, nativeID: nativeID})
			}
		}
		t, err := upsertBatch(st, batch, "applicationsignals services")
		total += t
		if err == nil {
			services = append(services, instrumented...)
		}
		return err
	})
	// A failed write is listErr, which storeReadRows returns before warning.
	_, err = storeReadRows(st, acct, region, appSignalsListServicesOp, nil, listErr, func() {
		skips.warn(st, appSignalsListServicesOp, acct, region)
	})
	return total, services, err
}

// scanApplicationSignalsInstrumentationConfigurations stores the probe
// instrumentation configurations of each service, with their tags, and links
// each under its service (storeEachParentTagsUnder). The op has no list-all
// form: it needs a service name and environment, and services holds only the
// services stored this scan, i.e. those ListServices reported with telemetry
// in the trailing 24h window. A probe of a service that sent no telemetry in
// that window is not listed and no warning says so. Services sharing a name
// and environment (their other KeyAttributes differ) are queried once and
// their configurations linked under the first. The services are listed
// concurrently, and one service's failure costs the others none of their
// rows. Per service, the rows read before a failure are kept: a not-found
// (the service is gone since ListServices) skips it silently when nothing was
// read from it, and with one warning for all such services otherwise; an op
// this region's endpoint does not serve on the first page skips it silently;
// a denial warns once; a ValidationException on the first page skips it with
// one warning for all such services; a transient error after the SDK's retries is returned as
// one error for all such services, apart from the hard ones so that it is
// demoted beside them (phaseErrs.join); any other error is returned after the
// rows are stored.
// Breakpoints are not listed: they expire within 24 hours
// (InstrumentationTypeBreakpoint), so they are not durable. It reports
// whether a write failed, after which the scan must stop.
func scanApplicationSignalsInstrumentationConfigurations(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string, services []applicationSignalsService, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	type nameEnv struct{ name, environment string }
	seen := make(map[nameEnv]bool, len(services))
	byNativeID := make(map[string]applicationSignalsService, len(services))
	parents := make([]childParent, 0, len(services))
	for _, s := range services {
		key := nameEnv{s.name, s.environment}
		if seen[key] {
			continue
		}
		seen[key] = true
		byNativeID[s.nativeID] = s
		parents = append(parents, childParent{id: s.nativeID, arn: s.nativeID})
	}
	// The services are listed concurrently, so each tally keeps its smallest
	// service.
	var invalid, throttled skipTally
	failed := storeEachParentTagsUnder(ctx, st, acct, region, appSignalsListInstrumentationConfigurationsOp, appSignalsNotFound, parents,
		func(ctx context.Context, id string, token *string) ([]applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv, *string, error) {
			svc := byNativeID[id]
			out, err := client.ListInstrumentationConfigurations(ctx, &applicationsignals.ListInstrumentationConfigurationsInput{
				Service: &svc.name, Environment: &svc.environment,
				InstrumentationType: applicationsignalstypes.InstrumentationTypeProbe,
				MaxResults:          sdkaws.Int32(appSignalsInstrumentationPageLimit), NextToken: token,
			})
			switch {
			case err == nil:
				return out.LatestConfigurations, out.NextToken, nil
			case token == nil && isOpNotDeployed(err):
			// A later page's ValidationException is not the name or environment,
			// which the first page accepted, so it is returned like any other.
			case token == nil && isAPIErrorCode(err, "ValidationException"):
				invalid.skippedLeast(id, err)
			case isDemotableTransient(err):
				throttled.skippedLeast(id, err)
			default:
				return nil, nil, err
			}
			return nil, nil, nil
		},
		func(id string, c applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv) (*store.Resource, string, string) {
			key := byNativeID[id].name + "/" + cmp.Or(sv(c.LocationHash), noIdentifier)
			return arnRow("instrumentation configurations", c.ARN, &key, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeApplicationSignalsInstrumentationConfiguration, NativeID: sv(c.ARN),
				Name: nonEmptyPtr(sv(c.Description)), Region: &region, CreatedAt: tp(c.CreatedAt),
				AttributesJSON: mustJSON(flattenAppSignalsInstrumentationConfiguration(c)), DiscoveredBy: scanID,
			})
		}, tags, p)
	var transient error
	if throttled.count > 0 {
		transient = fmt.Errorf("%s: %d service(s) not fully listed, so some or all of their instrumentation configurations were not stored, first %s: %w",
			appSignalsListInstrumentationConfigurationsOp, throttled.count, throttled.first, throttled.err)
	}
	if failed {
		// The scan stops after a failed write, and a demoted transient would
		// be a warning about configurations the failed write may have lost,
		// so it joins that write's error, the last one added to p.
		if transient != nil {
			last := &p.errs[len(p.errs)-1]
			last.err = errors.Join(last.err, transient)
		}
		return true
	}
	p.errs.add(appSignalsListInstrumentationConfigurationsOp, transient)
	invalid.warnCode(st, appSignalsListInstrumentationConfigurationsOp, acct, region, "ValidationException", "service(s)",
		"a service name or environment ListServices reports may not match the instrumentation API's input pattern")
	return false
}

// appSignalsInstrumentationConfigurationAttrs shadows the Location and
// CaptureConfiguration unions, which marshal as an unlabelled {"Value": ...},
// with one key per known member so the stored row names the member kind. A
// member newer than the pinned SDK is kept under UnknownMember with its tag and
// its value, which is []byte and so stored base64-encoded.
type appSignalsInstrumentationConfigurationAttrs struct {
	applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv
	Location             appSignalsLocationAttrs
	CaptureConfiguration appSignalsCaptureConfigurationAttrs
}

type appSignalsLocationAttrs struct {
	CodeLocation  *applicationsignalstypes.CodeLocation       `json:",omitempty"`
	UnknownMember *applicationsignalstypes.UnknownUnionMember `json:",omitempty"`
}

type appSignalsCaptureConfigurationAttrs struct {
	CodeCapture   *applicationsignalstypes.CodeCaptureConfiguration `json:",omitempty"`
	UnknownMember *applicationsignalstypes.UnknownUnionMember       `json:",omitempty"`
}

func flattenAppSignalsInstrumentationConfiguration(c applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv) appSignalsInstrumentationConfigurationAttrs {
	out := appSignalsInstrumentationConfigurationAttrs{InstrumentationConfigurationWithoutServiceEnv: c}
	switch l := c.Location.(type) {
	case *applicationsignalstypes.LocationMemberCodeLocation:
		out.Location.CodeLocation = &l.Value
	case *applicationsignalstypes.UnknownUnionMember:
		out.Location.UnknownMember = l
	}
	switch cc := c.CaptureConfiguration.(type) {
	case *applicationsignalstypes.CaptureConfigurationMemberCodeCapture:
		out.CaptureConfiguration.CodeCapture = &cc.Value
	case *applicationsignalstypes.UnknownUnionMember:
		out.CaptureConfiguration.UnknownMember = cc
	}
	return out
}

// scanApplicationSignalsGroupingConfiguration upserts the singleton
// grouping-configuration resource by aggregating ListGroupingAttributeDefinitions
// pages into one attrs body. Emitted only when the API returns at least one
// definition or a non-zero UpdatedAt; empty means no configuration exists in
// this region. A failed page or a repeated NextToken stores nothing: a
// partial body would read as the whole configuration.
func scanApplicationSignalsGroupingConfiguration(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string) (total int, err error) {
	type aggregate struct {
		GroupingAttributeDefinitions []applicationsignalsListGroupingAttributeDefinitionsItem `json:"GroupingAttributeDefinitions"`
		UpdatedAt                    *time.Time                                               `json:"UpdatedAt,omitempty"`
	}
	var agg aggregate
	var nextToken *string
	for {
		out, lerr := client.ListGroupingAttributeDefinitions(ctx, &applicationsignals.ListGroupingAttributeDefinitionsInput{NextToken: nextToken})
		if lerr != nil {
			if isAccessDenied(lerr) {
				return total, skipIfAccessDenied(st, appSignalsListGroupingOp, acct.ID, region, lerr)
			}
			return total, fmt.Errorf("%s: %w", appSignalsListGroupingOp, lerr)
		}
		for _, d := range out.GroupingAttributeDefinitions {
			agg.GroupingAttributeDefinitions = append(agg.GroupingAttributeDefinitions, applicationsignalsListGroupingAttributeDefinitionsItem{
				GroupingName:         sv(d.GroupingName),
				DefaultGroupingValue: sv(d.DefaultGroupingValue),
				GroupingSourceKeys:   d.GroupingSourceKeys,
			})
		}
		if out.UpdatedAt != nil {
			agg.UpdatedAt = out.UpdatedAt
		}
		done, terr := lastPage(nextToken, out.NextToken)
		if terr != nil {
			return total, fmt.Errorf("%s: %w", appSignalsListGroupingOp, terr)
		}
		if done {
			break
		}
		nextToken = out.NextToken
	}
	// No configuration created in this region — skip the singleton row.
	if len(agg.GroupingAttributeDefinitions) == 0 && agg.UpdatedAt == nil {
		return 0, nil
	}
	nativeID := applicationSignalsGroupingConfigurationNativeID(acct.partition(), region, acct.ID)
	name := "grouping-configuration"
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeApplicationSignalsGroupingConfiguration,
		NativeID:       nativeID,
		Name:           &name,
		Region:         &region,
		CreatedAt:      tp(agg.UpdatedAt),
		AttributesJSON: mustJSON(agg),
		DiscoveredBy:   scanID,
	}
	err = st.UpsertResources([]*store.Resource{r})
	if err != nil {
		return 0, fmt.Errorf("upsert applicationsignals grouping-configuration: %w", err)
	}
	return 1, nil
}

// applicationsignalsListGroupingAttributeDefinitionsItem is a JSON-friendly
// projection of `applicationsignalstypes.GroupingAttributeDefinition` — the
// SDK type's unexported smithy serde marker breaks `json.Marshal` when
// nested in the aggregate. Captures only the fields graph analysis needs.
type applicationsignalsListGroupingAttributeDefinitionsItem struct {
	GroupingName         string   `json:"GroupingName"`
	DefaultGroupingValue string   `json:"DefaultGroupingValue,omitempty"`
	GroupingSourceKeys   []string `json:"GroupingSourceKeys,omitempty"`
}

// scanApplicationSignalsSLOs stores the region's service level objectives
// with their tags, each page written as soon as its tags are read
// (storeTokenPagesTags), one tag read per SLO. An SLO listed without an ARN
// is not stored and is warned about. It reports whether a write failed,
// after which the scan must stop.
func scanApplicationSignalsSLOs(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, appSignalsListSLOsOp,
		func(ctx context.Context, token *string) ([]applicationsignalstypes.ServiceLevelObjectiveSummary, *string, error) {
			out, err := client.ListServiceLevelObjectives(ctx, &applicationsignals.ListServiceLevelObjectivesInput{
				MaxResults: sdkaws.Int32(appSignalsSLOPageLimit), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.SloSummaries, out.NextToken, nil
		},
		func(s applicationsignalstypes.ServiceLevelObjectiveSummary) (*store.Resource, string, string) {
			name := sv(s.Name)
			return arnRow("service level objectives", s.Arn, s.Name, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeApplicationSignalsSLO,
				NativeID:       sv(s.Arn),
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(s.CreatedTime),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}, tags, p)
}
