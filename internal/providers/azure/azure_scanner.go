// Package azure implements cloud resource discovery for Microsoft Azure.
// It makes per-service API calls using the Azure SDK for Go (arm* packages)
// and follows the two-phase scan pattern.
package azure

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	// maxConcurrentSubscriptions caps parallel subscription scans.
	maxConcurrentSubscriptions = 10
	// maxConcurrentServices caps parallel service scanners per subscription.
	maxConcurrentServices = 10
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
	serviceFilter        []string // nil = scan all registered services
	subscriptionOverride []string // nil = config-then-enumerate; non-nil = pinned, no auto-enumeration
}

// Name implements providers.Scanner.
func (s *Scanner) Name() string { return "azure" }

// SetServiceFilter restricts the scan to the named services (e.g. "azure:compute", "azure:network").
// An empty or nil slice scans all registered services.
func (s *Scanner) SetServiceFilter(services []string) { s.serviceFilter = services }

// SetSubscriptionOverride pins the scan to exactly the given subscription IDs,
// disabling auto-enumeration (fail-closed). Implements providers.SubscriptionOverrider.
// A nil slice leaves the default config-then-enumerate behavior intact.
func (s *Scanner) SetSubscriptionOverride(subscriptionIDs []string) {
	s.subscriptionOverride = subscriptionIDs
}

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
	subs, cred, err := loadSubscriptions(ctx, s.subscriptionOverride)
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
		g.Go(func() error {
			// Each resolver gets its own buffered store (independent buffer) so
			// concurrent resolvers stay isolated; flush collapses the per-edge
			// autocommit serialisation into one tx per resolver.
			bs := st.BeginRelBuffer()
			err := fn(sub, bs)
			if ferr := bs.FlushRelBuffer(); ferr != nil && err == nil {
				err = ferr
			}
			return err
		})
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
// regionGlobal; see store/CLAUDE.md "region = \"global\" sentinel".
var regionGlobal = func() *string { s := "global"; return &s }()

func sv(p *string) string     { return util.Sv(p) }
func tp(t *time.Time) *string { return util.TimeRFC3339(t) }
