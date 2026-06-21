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
	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	// maxConcurrentSubscriptions caps parallel subscription scans.
	maxConcurrentSubscriptions = 10
	// maxConcurrentServices caps parallel service scanners per subscription.
	maxConcurrentServices = 10
	// serviceTimeout is the per-service hard deadline. azure:microsoft.compute now covers VMSS,
	// galleries, and hosting fan-outs in addition to core compute types, so this must
	// be generous enough for large subscriptions.
	serviceTimeout = 30 * time.Minute
)

// azHTTPClient pools connections to the ARM control plane. Every arm* client
// targets the single host management.azure.com, but Go's default transport
// keeps only MaxIdleConnsPerHost=2 idle connections — under the scan's
// service + fanout concurrency that forces most requests to pay a fresh
// TCP+TLS handshake (or block waiting for a connection to free up), which
// dominates wall-clock even for empty services. Raise the per-host idle pool;
// the scan's own semaphores already bound in-flight concurrency, so
// MaxConnsPerHost stays unbounded.
var azHTTPClient = newAzHTTPClient()

func newAzHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 200
	tr.MaxIdleConnsPerHost = 100
	tr.MaxConnsPerHost = 0
	tr.IdleConnTimeout = 90 * time.Second
	return &http.Client{Transport: tr}
}

// azClientOptions is shared by all arm* SDK client constructors. The retry
// policy reduces the base delay from the SDK default (800ms) to 500ms and
// allows up to 4 attempts — enough headroom for transient ARM errors without
// stalling the fanout critical path. Transport is the pooled azHTTPClient.
var azClientOptions = &arm.ClientOptions{
	ClientOptions: azcore.ClientOptions{
		Transport: azHTTPClient,
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

// LongDescription is the help text for `disco scan azure --help`.
func (s *Scanner) LongDescription() string {
	return `Scan Azure resources across reachable subscriptions.

Subscription scope comes from DefaultAzureCredential (az login, managed
identity, env vars) or the explicit 'subscriptions:' list in config.yaml.
--subscriptions pins the scan to exactly the listed subscription IDs and
disables auto-enumeration (fail-closed) — use it to constrain a shared
credential delegated across multiple tenants to one tenant's subscriptions.
There is no --regions / --profile flag — Azure scopes per
subscription/resource group, configured statically. --services narrows
the scanner set when iterating on one provider.

Examples:
  disco scan azure
  disco scan azure --subscriptions 00000000-0000-0000-0000-000000000000
  disco scan azure --services azure:microsoft.compute,azure:microsoft.network`
}

// ServiceFilterExample is the --services example shown in azure scan help.
func (s *Scanner) ServiceFilterExample() string {
	return "azure:microsoft.compute,azure:microsoft.network"
}

// ScopeColumnWidth pins the scan progress scope column to fit a subscription UUID.
func (s *Scanner) ScopeColumnWidth() int { return 36 }

// SetServiceFilter restricts the scan to the named services (e.g. "azure:microsoft.compute", "azure:microsoft.network").
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

	// Plain WaitGroup (not errgroup): a per-subscription failure is reported
	// and skipped rather than cancelling the other subscriptions' scans. Fatal
	// conditions (load-subscriptions) already returned early above.
	sem := semaphore.NewWeighted(maxConcurrentSubscriptions)
	var wg sync.WaitGroup
	for i := range subs {
		sub := &subs[i]
		wg.Go(func() {
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			if err := scanSubscription(ctx, sub, cred, s.serviceFilter, st, scanID); err != nil {
				st.ReportError(store.ScanError{
					Provider: "azure", Service: "scan", Scope: sub.scopeLabel(),
					Message: formatAzureError(err),
				})
			}
		})
	}
	wg.Wait()
	return nil
}

// scanSubscription runs phase 1 (resources + hierarchy) then phase 2
// (relationships) for one subscription.
func scanSubscription(ctx context.Context, sub *subscription, cred azcore.TokenCredential, services []string, st *store.Store, scanID string) error {
	// scanResourceGroups (RG parents of all resources) and the RP-registration
	// probe are independent ARM list calls, both on the critical path before any
	// service scanner can start. Run them concurrently so the service loop is
	// gated on the slower of the two, not their sum.
	//
	// scanResourceGroups failure is reported and the scan continues — one
	// service's (even the RG list's) error must never abort the subscription's
	// other scanners.
	//
	// The probe reads RP registration state once per subscription. ARM allows
	// LIST on unregistered providers (empty 200, no error), so the per-call error
	// path can't see a NotRegistered RP — this proactive gate is the only signal,
	// the Azure analog of AWS's phase-0 "service enabled?" gate. A probe failure
	// is non-fatal: regProviders stays nil and every service is scanned with the
	// reactive error classifier as fallback.
	var (
		regProviders map[string]bool
		rgErr, perr  error
		preWG        sync.WaitGroup
	)
	preWG.Go(func() { rgErr = scanResourceGroups(ctx, sub, cred, st, scanID) })
	preWG.Go(func() { regProviders, perr = loadRegisteredProviders(ctx, sub.ID, cred) })
	preWG.Wait()
	if rgErr != nil {
		st.ReportError(store.ScanError{
			Provider: "azure", Service: "resourcegroups", Scope: sub.scopeLabel(),
			Message: formatAzureError(rgErr),
		})
	}
	if perr != nil {
		st.ReportWarning(store.ScanWarning{
			Provider: "azure", Service: "providers", Scope: sub.scopeLabel(),
			Message: formatAzureError(perr),
		})
	}

	// Scan all registered service types in parallel, bounded by
	// maxConcurrentServices. A plain WaitGroup (not errgroup) so one service's
	// failure is reported and skipped, never cancelling its siblings — see
	// providers/CLAUDE.md "Errors never abort scan".
	sem := semaphore.NewWeighted(maxConcurrentServices)
	var wg sync.WaitGroup
	for _, svc := range filteredServices(services) {
		// RP not registered in this subscription → mark disabled and skip the
		// scanner entirely (no goroutine, no list call), mirroring AWS.
		if providerDisabled(regProviders, svc.name) {
			st.ReportService(svc.name, sub.scopeLabel(), 0, 0, 0, true)
			continue
		}
		wg.Go(func() {
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(ctx, serviceTimeout)
			defer cancel()
			total, inserted, err := svc.fn(svcCtx, sub, cred, st, scanID)
			switch {
			case err == nil:
				st.ReportService(svc.name, sub.scopeLabel(), total, inserted, 0, false)
			case errors.Is(err, errServiceNotRegistered) && total == 0:
				// Service not available in this subscription (RP unregistered or
				// resource type absent) and nothing was scanned — mark the
				// service disabled (no error, no warning), the Azure analog of
				// AWS's errServiceDisabled. The total==0 guard ensures a merged
				// service that already scanned data in another phase is never
				// blanked.
				st.ReportService(svc.name, sub.scopeLabel(), 0, 0, 0, true)
			default:
				st.ReportError(store.ScanError{
					Provider: "azure", Service: svc.name, Scope: sub.scopeLabel(),
					Message: formatAzureError(err),
				})
				st.ReportService(svc.name, sub.scopeLabel(), total, inserted, 1, false)
			}
		})
	}
	wg.Wait()

	// Phase 1c: API-driven cross-cutting resolvers (e.g. diagnostic-settings)
	// run AFTER phase-1 services so st.ListResources returns the full set.
	// Errors degrade to ReportError; the resolve phase still proceeds.
	for _, ar := range registeredAPIResolvers {
		edges, aerr := ar.fn(ctx, sub, cred, st)
		if aerr != nil {
			st.ReportError(store.ScanError{
				Provider: "azure", Service: ar.name, Scope: sub.scopeLabel(),
				Message: formatAzureError(aerr),
			})
			st.ReportService(ar.name, sub.scopeLabel(), 0, edges, 1, false)
			continue
		}
		st.ReportService(ar.name, sub.scopeLabel(), 0, edges, 0, false)
	}

	st.ReportResolveStart("azure")
	var counter atomic.Int64
	resolveRelationships(ctx, sub, st.WithRelCounter(&counter))
	st.ReportResolveComplete("azure", int(counter.Load()))
	return nil
}

// resolveRelationships is phase 2 for Azure: derive edges between resources
// that have already been written to the DB. Resolvers run concurrently
// (bounded by maxConcurrentResolvers) since they operate on disjoint resource
// types; a failure in one is reported and does not stop the others — partial
// graph beats no graph.
func resolveRelationships(ctx context.Context, sub *subscription, st *store.Store) {
	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentResolvers)
	for _, r := range registeredResolvers {
		g.Go(func() error {
			// Each resolver gets its own buffered store (independent buffer) so
			// concurrent resolvers stay isolated; flush collapses the per-edge
			// autocommit serialisation into one tx per resolver.
			bs := st.BeginRelBuffer()
			if err := r.fn(sub, bs); err != nil {
				st.ReportError(store.ScanError{
					Provider: "azure", Service: "resolve", Scope: sub.scopeLabel(), Message: formatAzureError(err),
				})
			}
			if ferr := bs.FlushRelBuffer(); ferr != nil {
				st.ReportError(store.ScanError{
					Provider: "azure", Service: "resolve", Scope: sub.scopeLabel(), Message: formatAzureError(ferr),
				})
			}
			return nil // resolver errors are reported, never abort siblings
		})
	}
	_ = g.Wait()
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

// loadRegisteredProviders returns a lowercased ARM-namespace → isRegistered map
// for the subscription. ARM allows LIST on unregistered RPs (they return an
// empty 200, not an error), so this is the only signal that a service is
// genuinely not available in the sub — the Azure analog of AWS's phase-0
// "service enabled?" gate. A nil map (probe failed) means "don't gate": fall
// back to scanning every service and classifying per-call errors reactively.
func loadRegisteredProviders(ctx context.Context, subID string, cred azcore.TokenCredential) (map[string]bool, error) {
	client, err := armresources.NewProvidersClient(subID, cred, azClientOptions)
	if err != nil {
		return nil, err
	}
	return registeredProvidersFromPager(ctx, client.NewListPager(nil))
}

// registeredProvidersFromPager drains a Providers/List pager into the
// namespace→registered map. Split from loadRegisteredProviders so tests can
// drive it with an armresources fake transport.
func registeredProvidersFromPager(ctx context.Context, pager *runtime.Pager[armresources.ProvidersClientListResponse]) (map[string]bool, error) {
	out := map[string]bool{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range page.Value {
			if p.Namespace == nil || p.RegistrationState == nil {
				continue
			}
			// "Registered" / "Registering" → scan it; "NotRegistered" /
			// "Unregistering" → nothing to enumerate, mark disabled.
			state := *p.RegistrationState
			out[strings.ToLower(*p.Namespace)] = strings.EqualFold(state, "Registered") ||
				strings.EqualFold(state, "Registering")
		}
	}
	return out, nil
}

// providerDisabled reports whether svc's ARM namespace is known-not-registered
// in this subscription. Unknown namespace or nil map ⇒ false (scan it).
func providerDisabled(reg map[string]bool, svcName string) bool {
	if reg == nil {
		return false
	}
	ns := strings.ToLower(strings.TrimPrefix(svcName, "azure:"))
	registered, known := reg[ns]
	return known && !registered
}

// — shared helpers —

// subscription holds a resolved Azure subscription.
type subscription struct {
	ID   string
	Name string
}

// scopeLabel is the human-readable per-subscription scope shown in scan
// progress + error output. Azure lists are subscription-wide (one call spans
// all regions) so the scope is the subscription, not a region — prefer the
// DisplayName, falling back to the GUID when the config path supplied only an
// ID.
func (s *subscription) scopeLabel() string {
	if s.Name != "" {
		return s.Name
	}
	return s.ID
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
