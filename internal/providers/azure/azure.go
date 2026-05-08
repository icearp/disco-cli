// Package azure implements cloud resource discovery for Microsoft Azure.
// It makes per-service API calls using the Azure SDK for Go (arm* packages)
// and follows the two-phase scan pattern.
package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	// maxConcurrentSubscriptions caps parallel subscription scans.
	maxConcurrentSubscriptions = 10
	// maxConcurrentServices caps parallel service scanners per subscription.
	maxConcurrentServices = 10
	// maxConcurrentFanout caps concurrent child API calls within a single service
	// (e.g. VM extension calls per VM, gallery image scans per gallery).
	// 50 keeps us well under Azure ARM rate limits (1200 req/min per subscription)
	// while cutting the number of sequential fanout rounds compared to 20.
	maxConcurrentFanout = 50
	// serviceTimeout is the per-service hard deadline. azure:compute now covers VMSS,
	// galleries, and hosting fan-outs in addition to core compute types, so this must
	// be generous enough for large subscriptions.
	serviceTimeout = 30 * time.Minute
)

// azClientOptions is shared by all arm* SDK client constructors. The retry
// policy reduces the base delay from the SDK default (800ms) to 500ms and
// allows up to 4 attempts — enough headroom for transient ARM errors without
// stalling the fanout critical path.
var azClientOptions = &arm.ClientOptions{
	ClientOptions: azcore.ClientOptions{
		Retry: policy.RetryOptions{
			MaxRetries:    4,
			RetryDelay:    500 * time.Millisecond,
			MaxRetryDelay: 30 * time.Second,
		},
	},
}

func init() { providers.Register(&Scanner{}) }

// Scanner implements providers.Scanner for Azure.
type Scanner struct {
	serviceFilter []string // nil = scan all registered services
}

// Name implements providers.Scanner.
func (s *Scanner) Name() string { return "azure" }

// SetServiceFilter restricts the scan to the named services (e.g. "azure:compute", "azure:network").
// An empty or nil slice scans all registered services.
func (s *Scanner) SetServiceFilter(services []string) { s.serviceFilter = services }

// ServiceNames returns the names of all services this scanner will report.
func (s *Scanner) ServiceNames() []string {
	svcs := filteredServices(s.serviceFilter)
	names := make([]string, len(svcs))
	for i, svc := range svcs {
		names[i] = svc.name
	}
	return names
}

// Scan discovers all Azure resources across all configured subscriptions.
// Subscriptions are scanned in parallel, bounded by maxConcurrentServices.
func (s *Scanner) Scan(ctx context.Context, st *store.Store, scanID string) error {
	subs, cred, err := loadSubscriptions(ctx)
	if err != nil {
		return fmt.Errorf("azure: load subscriptions: %w", err)
	}

	// Tenant-scope services (e.g. Entra ID via Microsoft Graph) run ONCE per
	// scan, before per-subscription fan-out. Sits above the subscription
	// boundary so consumers can populate principal resources that per-sub
	// resolvers (RBAC role assignments) FK-match against.
	runTenantServices(ctx, subs, cred, s.serviceFilter, st, scanID)

	sem := semaphore.NewWeighted(maxConcurrentSubscriptions)
	g, gctx := errgroup.WithContext(ctx)
	for i := range subs {
		sub := &subs[i]
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			if err := scanSubscription(gctx, sub, cred, s.serviceFilter, st, scanID); err != nil {
				return fmt.Errorf("azure subscription %s: %w", sub.ID, err)
			}
			return nil
		})
	}
	return g.Wait()
}

// scanSubscription runs phase 1 (resources + hierarchy) then phase 2
// (relationships) for one subscription.
func scanSubscription(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, services []string, st *store.Store, scanID string) error {
	// Scan resource groups first (they are parents of all resources).
	if err := scanResourceGroups(ctx, sub, cred, st, scanID); err != nil {
		return err
	}

	// Scan all registered service types in parallel, bounded by maxConcurrentServices.
	sem := semaphore.NewWeighted(maxConcurrentServices)
	g, gctx := errgroup.WithContext(ctx)
	for _, svc := range filteredServices(services) {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(gctx, serviceTimeout)
			defer cancel()
			total, inserted, err := svc.fn(svcCtx, sub, cred, st, scanID)
			if err != nil {
				return err
			}
			st.ReportService(svc.name, sub.ID, total, inserted, 0, false)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	// Phase 1c: API-driven cross-cutting resolvers (e.g. diagnostic-settings)
	// run AFTER phase-1 services so st.ListResources returns the full set.
	// Errors degrade to ReportError; the resolve phase still proceeds.
	for _, ar := range registeredAPIResolvers {
		edges, aerr := ar.fn(ctx, sub, cred, st)
		if aerr != nil {
			st.ReportError(store.ScanError{
				Provider: "azure", Service: ar.name, Scope: sub.ID,
				Message: formatAzureError(aerr),
			})
			st.ReportService(ar.name, sub.ID, 0, edges, 1, false)
			continue
		}
		st.ReportService(ar.name, sub.ID, 0, edges, 0, false)
	}

	st.ReportResolveStart("azure")
	var counter atomic.Int64
	err := resolveRelationships(ctx, sub, st.WithRelCounter(&counter))
	st.ReportResolveComplete("azure", int(counter.Load()))
	return err
}

// resolveRelationships is phase 2 for Azure: derive edges between resources
// that have already been written to the DB. Resolvers run in parallel since
// they operate on disjoint resource types.
func resolveRelationships(ctx context.Context, sub *subscription, st *store.Store) error {
	g, _ := errgroup.WithContext(ctx)
	for _, r := range registeredResolvers {
		fn := r.fn
		g.Go(func() error { return fn(sub, st) })
	}
	return g.Wait()
}

// filteredServices returns the services to run. When filter is non-empty, only
// services whose name appears in filter are returned.
func filteredServices(filter []string) []serviceEntry {
	if len(filter) == 0 {
		return registeredServices
	}
	allowed := make(map[string]bool, len(filter))
	for _, name := range filter {
		allowed[name] = true
	}
	var out []serviceEntry
	for _, svc := range registeredServices {
		if allowed[svc.name] {
			out = append(out, svc)
		}
	}
	return out
}

// — shared helpers —

// subscription holds a resolved Azure subscription.
type subscription struct {
	ID   string
	Name string
}

// functionAppSettingsByDiscoID is a per-subscription sidecar populated by the
// AppService scanner during scan and consumed by the Functions resolver.
// Outer key = subscription ID, inner key = function-app disco ID, innermost
// = setting name → value. App settings are NOT a first-class ARM resource
// (they live as sub-resource config of `Microsoft.Web/sites`), so per the
// providers/CLAUDE.md "non-resource config fetches" rule they sidecar
// rather than wrap into the parent site's AttributesJSON. Package-level
// rather than `subscription` field because subscription is passed by value
// in some call paths and adding a mutex would break those copy semantics.
var (
	functionAppSettingsMu sync.Mutex
	functionAppSettings   = map[string]map[string]map[string]string{}
)

// recordFunctionAppSettings stores app settings for one function-app site
// under a given subscription, concurrent-safe across per-site fan-out.
func recordFunctionAppSettings(subID, siteDiscoID string, settings map[string]string) {
	if len(settings) == 0 {
		return
	}
	functionAppSettingsMu.Lock()
	defer functionAppSettingsMu.Unlock()
	subMap, ok := functionAppSettings[subID]
	if !ok {
		subMap = map[string]map[string]string{}
		functionAppSettings[subID] = subMap
	}
	subMap[siteDiscoID] = settings
}

// loadFunctionAppSettings returns the app settings recorded for the given
// subscription. Concurrent-safe; returns nil if scanner did not run.
func loadFunctionAppSettings(subID string) map[string]map[string]string {
	functionAppSettingsMu.Lock()
	defer functionAppSettingsMu.Unlock()
	return functionAppSettings[subID]
}

func mustJSON(v any) string { return util.MustJSON(v) }

// regionGlobal is the canonical Region pointer for non-regional Azure
// resources (tenant-scope Entra ID users / groups / SPs / app regs / roles)
// and any other rows whose location is "everywhere". Mirrors AWS
// regionGlobal; see internal/store/CLAUDE.md "region = \"global\" sentinel".
var regionGlobal = func() *string { s := "global"; return &s }()

func sv(p *string) string     { return util.Sv(p) }
func tp(t *time.Time) *string { return util.TimeRFC3339(t) }

// rgFromID extracts the resource group name from an Azure resource ID,
// lowercased for use in computing stable hierarchy IDs.
// e.g. /subscriptions/xxx/resourceGroups/myRG/... → "myrg"
func rgFromID(id string) string {
	parts := strings.Split(strings.ToLower(id), "/")
	for i, p := range parts {
		if p == "resourcegroups" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// rgNameFromID extracts the resource group name from an Azure resource ID,
// preserving original casing for use in API calls.
// e.g. /subscriptions/xxx/resourceGroups/MyRG/... → "MyRG"
func rgNameFromID(id string) string {
	lower := strings.ToLower(id)
	const sep = "/resourcegroups/"
	start := strings.Index(lower, sep)
	if start < 0 {
		return ""
	}
	rest := id[start+len(sep):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// nameFromID returns the last path segment of an Azure resource ID,
// preserving original casing. Used to extract resource names for API calls.
// e.g. /subscriptions/xxx/.../virtualMachines/myVM → "myVM"
func nameFromID(id string) string {
	idx := strings.LastIndex(id, "/")
	if idx < 0 || idx == len(id)-1 {
		return id
	}
	return id[idx+1:]
}

// truncateAtSegment returns the portion of id before the first occurrence of
// the case-insensitive separator. Used by resolvers to derive parent resource IDs
// from child NativeIDs (e.g. strip "/extensions/" suffix to get VM ID).
func truncateAtSegment(id, separator string) string {
	idx := strings.Index(strings.ToLower(id), strings.ToLower(separator))
	if idx < 0 {
		return ""
	}
	return id[:idx]
}

// azTagsJSON converts an Azure SDK tag map to a JSON-encoded {key:value} string pointer.
// Returns nil when tags is nil or empty.
func azTagsJSON(tags map[string]*string) *string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for k, v := range tags {
		if v != nil {
			m[k] = *v
		}
	}
	s := mustJSON(m)
	return &s
}

// azPager is satisfied by every Azure SDK paginator (More/NextPage pair).
type azPager[P any] interface {
	More() bool
	NextPage(context.Context) (P, error)
}

// azPageScan runs a paginated Azure list call, converting each page to resources
// and hierarchy pairs via toResources, then upserts both. Handles access-denied
// by logging and returning nil.
func azPageScan[P any](
	ctx context.Context,
	action string,
	sub *subscription,
	st *store.Store,
	pager azPager[P],
	toResources func(P) ([]*store.Resource, [][2]string),
) (total, inserted int, err error) {
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, action, sub.ID, err)
			}
			return total, inserted, fmt.Errorf("%s: %w", action, err)
		}
		batch, pairs := toResources(page)
		if len(batch) == 0 {
			continue
		}
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert %s: %w", action, err)
		}
		total += len(batch)
		inserted += n
		if len(pairs) > 0 {
			if err := st.RecordHierarchyBatch(pairs); err != nil {
				return total, inserted, fmt.Errorf("closure %s: %w", action, err)
			}
		}
	}
	return
}

// azTrackedBase is the four-field shape every Azure SDK tracked-resource type
// satisfies: ID, Name, Location, Tags. Per-type extractors return this base
// (plus the original SDK pointer in `full` for AttributesJSON serialization)
// so generic helpers can build store.Resource batches without knowing the
// concrete SDK type.
type azTrackedBase struct {
	id, name, location string
	tags               map[string]*string
	full               any
}

// azTrackedRows builds a store.Resource batch + RG hierarchy pairs from a
// slice of SDK tracked-resource pointers. Generalizes the wanRows precedent.
// Items with nil pointers or empty IDs are skipped. RG hierarchy pairs are
// emitted only when the ID contains a /resourceGroups/ segment.
func azTrackedRows[T any](sub *subscription, scanID, rtype string, items []*T, extract func(*T) azTrackedBase) ([]*store.Resource, [][2]string) {
	var batch []*store.Resource
	var pairs [][2]string
	for _, item := range items {
		if item == nil {
			continue
		}
		b := extract(item)
		if b.id == "" {
			continue
		}
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
			Type: rtype, NativeID: b.id,
			Name: &b.name, Region: &b.location,
			TagsJSON: azTagsJSON(b.tags), AttributesJSON: mustJSON(b.full),
			DiscoveredBy: scanID,
		})
		if rgFromID(b.id) != "" {
			pairs = append(pairs, rgHierarchyPair(sub, rtype, b.id))
		}
	}
	return batch, pairs
}

// listSubscriptionRGNames enumerates all resource groups in the subscription
// via the ARM resource-groups SDK. Used by azRGFanoutScan and any other
// caller that needs RG names without depending on scan order against the
// `azure:resourcegroups` service. AccessDenied tolerated (returns empty
// slice + nil error). Imported as a reverse dep on `armresources` already
// pulled in by `resourcegroups_scanners.go`.
func listSubscriptionRGNames(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential) ([]string, error) {
	client, err := armresources.NewResourceGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return nil, fmt.Errorf("armresources:NewResourceGroupsClient: %w", err)
	}
	var out []string
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("armresources:ResourceGroups.List: %w", err)
		}
		for _, rg := range page.Value {
			if rg == nil || rg.Name == nil {
				continue
			}
			out = append(out, *rg.Name)
		}
	}
	return out, nil
}

// azRGFanoutScan covers ARM resource types that have NO subscription-wide list
// API (only per-RG list endpoints). It enumerates RGs via
// listSubscriptionRGNames once, then fans out per-RG list calls bounded by
// maxConcurrentFanout (errgroup + semaphore). pagerFn returns a fresh pager
// for each RG; pageItems extracts the SDK slice; extract converts each item
// to azTrackedBase. Same shape as azSimpleScan but with a per-RG dimension.
//
// Per-RG AccessDenied + ResourceGroupNotFound errors are tolerated and skip
// that RG silently — partial-cred scans don't fail wholesale. Other errors
// abort. Hierarchy pairs to RG are emitted automatically when the resource
// ID contains a `/resourceGroups/` segment (consistent with azTrackedRows).
//
// Use when adding scanners for RG-scoped types like classic
// VirtualNetworkGateways, ExpressRouteGateways, Front Door endpoints, ADF
// linked services, Logic Apps API connections, etc.
func azRGFanoutScan[T any, P any](
	ctx context.Context,
	action, rtype string,
	sub *subscription,
	cred *azidentity.DefaultAzureCredential,
	st *store.Store,
	scanID string,
	pagerFn func(rg string) azPager[P],
	pageItems func(P) []*T,
	extract func(*T) azTrackedBase,
) (total, inserted int, err error) {
	rgs, err := listSubscriptionRGNames(ctx, sub, cred)
	if err != nil {
		return 0, 0, err
	}
	if len(rgs) == 0 {
		return 0, 0, nil
	}

	var (
		mu       sync.Mutex
		allBatch []*store.Resource
		allPairs [][2]string
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gctx := errgroup.WithContext(ctx)
	for _, rg := range rgs {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			pager := pagerFn(rg)
			for pager.More() {
				page, err := pager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) || isResourceGroupNotFound(err) {
						return nil
					}
					return fmt.Errorf("%s rg=%s: %w", action, rg, err)
				}
				batch, pairs := azTrackedRows(sub, scanID, rtype, pageItems(page), extract)
				if len(batch) == 0 {
					continue
				}
				mu.Lock()
				allBatch = append(allBatch, batch...)
				allPairs = append(allPairs, pairs...)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(allBatch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(allBatch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert %s: %w", action, err)
	}
	if len(allPairs) > 0 {
		if err := st.RecordHierarchyBatch(allPairs); err != nil {
			return len(allBatch), n, fmt.Errorf("closure %s: %w", action, err)
		}
	}
	return len(allBatch), n, nil
}

// isResourceGroupNotFound reports whether err is an Azure 404 RG-vanished
// error (race between RG-list and per-RG list calls). Treated as
// best-effort skip in azRGFanoutScan.
func isResourceGroupNotFound(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusNotFound
	}
	return false
}

// azSimpleScan wires NewListPager → azPageScan → azTrackedRows in a single
// call. Use for simple subscription-scoped list scanners with no sub-resource
// fanout. The scanner shrinks to: build SDK client, call azSimpleScan, supply
// (pageItems, extract) functions.
func azSimpleScan[T any, P any](
	ctx context.Context,
	action, rtype string,
	sub *subscription,
	st *store.Store,
	scanID string,
	pager azPager[P],
	pageItems func(P) []*T,
	extract func(*T) azTrackedBase,
) (total, inserted int, err error) {
	return azPageScan(ctx, action, sub, st, pager, func(page P) ([]*store.Resource, [][2]string) {
		return azTrackedRows(sub, scanID, rtype, pageItems(page), extract)
	})
}
