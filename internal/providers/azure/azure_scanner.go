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

	// Resolve the tenant GUID once and stamp it onto every subscription so
	// tenant-scope scanners/resolvers can store tenant-identical resources
	// (management groups, built-in role/policy definitions) under a single
	// account rather than duplicating them per subscription. Resolution is
	// best-effort: on failure the empty tenantID disables that deduplication and
	// each subscription falls back to storing its own copy (current behavior).
	if tenantID, terr := tenantIDFromCredScope(ctx, cred, armScope); terr != nil {
		st.ReportWarning(store.ScanWarning{
			Provider: "azure", Service: "scan", Scope: "tenant",
			Message: "resolve tenant id: " + formatAzureError(terr),
		})
	} else {
		// Friendly tenant name is a separate best-effort Graph call; degrade to
		// the GUID (left as empty tenantName) when directory read is unavailable.
		tenantName, _ := tenantDisplayName(ctx, cred)
		for i := range subs {
			subs[i].tenantID = tenantID
			subs[i].tenantName = tenantName
		}
	}

	// Plain WaitGroup (not errgroup): a per-subscription failure is reported
	// and skipped rather than cancelling the other subscriptions' scans. Fatal
	// conditions (load-subscriptions) already returned early above.
	sem := semaphore.NewWeighted(maxConcurrentSubscriptions)
	var wg sync.WaitGroup

	// Tenant-scope services (e.g. Entra ID via Microsoft Graph) populate the
	// principal resources that per-sub phase-2 resolvers (RBAC role assignments)
	// FK-match against. The dependency is narrow — only the phase-2 authorization
	// resolver consumes Entra rows — so rather than run the tenant phase serially
	// before the fan-out, we run it concurrently and gate only each subscription's
	// resolver phase on entraDone (see scanSubscription). Entra's latency is thus
	// hidden behind phase-1 scanning instead of added onto total scan time.
	//
	// close(entraDone) is deferred FIRST so it runs LAST — after reportPanic
	// recovers — guaranteeing waiters are released even if the tenant phase panics
	// (no deadlock). The channel close happens-after every principal upsert, so
	// phase-2 readers always observe a fully-written principal set.
	entraDone := make(chan struct{})
	wg.Go(func() {
		defer close(entraDone)
		defer reportPanic(st, "entra", tenantScopeLabel(subs))
		runTenantServices(ctx, subs, cred, s.serviceFilter, st, scanID)
	})

	for i := range subs {
		sub := &subs[i]
		wg.Go(func() {
			defer reportPanic(st, "scan", sub.scopeLabel())
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			if err := scanSubscription(ctx, sub, cred, s.serviceFilter, st, scanID, entraDone); err != nil {
				st.ReportError(store.ScanError{
					Provider: "azure", Service: "scan", Scope: sub.scopeLabel(),
					Message: formatAzureError(err),
				})
			}
		})
	}
	wg.Wait()

	// Stitch the top three hierarchy tiers (management-group → subscription →
	// resource-group) once every subscription's phase-1 and the tenant phase have
	// written their rows — the only point at which both endpoints of each
	// cross-phase closure pair exist in the store.
	stitchTopHierarchy(ctx, subs, cred, st)
	return nil
}

// scanSubscription runs phase 1 (resources + hierarchy) then phase 2
// (relationships) for one subscription.
func scanSubscription(ctx context.Context, sub *subscription, cred azcore.TokenCredential, services []string, st *store.Store, scanID string, entraDone <-chan struct{}) error {
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
	preWG.Go(func() {
		defer reportPanic(st, "resourcegroups", sub.scopeLabel())
		rgErr = scanResourceGroups(ctx, sub, cred, st, scanID)
	})
	preWG.Go(func() {
		defer reportPanic(st, "providers", sub.scopeLabel())
		regProviders, perr = loadRegisteredProviders(ctx, sub.ID, cred)
	})
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
			st.ReportService(svc.name, sub.scopeLabel(), 0, 0, 0, 0, store.ServiceDisabled)
			continue
		}
		wg.Go(func() {
			defer reportPanic(st, svc.name, sub.scopeLabel())
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(ctx, serviceTimeout)
			defer cancel()
			var newC, changedC atomic.Int64
			total, _, err := svc.fn(svcCtx, sub, cred, st.WithUpsertCounters(&newC, &changedC), scanID)
			switch {
			case err == nil:
				st.ReportService(svc.name, sub.scopeLabel(), total, int(newC.Load()), int(changedC.Load()), 0, store.ServiceOK)
			case errors.Is(err, errServiceNotRegistered) && total == 0:
				// Service not available in this subscription (RP unregistered or
				// resource type absent) and nothing was scanned — mark the
				// service disabled (no error, no warning), the Azure analog of
				// AWS's errServiceDisabled. The total==0 guard ensures a merged
				// service that already scanned data in another phase is never
				// blanked.
				st.ReportService(svc.name, sub.scopeLabel(), 0, 0, 0, 0, store.ServiceDisabled)
			default:
				st.ReportError(store.ScanError{
					Provider: "azure", Service: svc.name, Scope: sub.scopeLabel(),
					Message: formatAzureError(err),
				})
				st.ReportService(svc.name, sub.scopeLabel(), total, int(newC.Load()), int(changedC.Load()), 1, store.ServiceOK)
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
			st.ReportService(ar.name, sub.scopeLabel(), 0, edges, 0, 1, store.ServiceOK)
			continue
		}
		st.ReportService(ar.name, sub.scopeLabel(), 0, edges, 0, 0, store.ServiceOK)
	}

	// Phase 2 resolvers consume Entra principals written by the tenant phase, which
	// runs concurrently with phase-1 scanning. Block until it completes so
	// authorization edges (assignment -uses-> principal) are complete. ctx
	// cancellation releases the wait so a cancelled scan never hangs here.
	waitForTenant(ctx, entraDone)

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
		resolverLabel := r.name
		if resolverLabel == "" {
			resolverLabel = "resolve"
		}
		g.Go(func() error {
			defer reportPanic(st, resolverLabel, sub.scopeLabel())
			// Each resolver gets its own buffered store (independent buffer) so
			// concurrent resolvers stay isolated; flush collapses the per-edge
			// autocommit serialisation into one tx per resolver.
			bs := st.BeginRelBuffer()
			if err := r.fn(sub, bs); err != nil {
				st.ReportError(store.ScanError{
					Provider: "azure", Service: resolverLabel, Scope: sub.scopeLabel(), Message: formatAzureError(err),
				})
			}
			if ferr := bs.FlushRelBuffer(); ferr != nil {
				st.ReportError(store.ScanError{
					Provider: "azure", Service: resolverLabel, Scope: sub.scopeLabel(), Message: formatAzureError(ferr),
				})
			}
			return nil // resolver errors are reported, never abort siblings
		})
	}
	_ = g.Wait()
}

// waitForTenant blocks until the tenant phase (Entra) has completed (done is
// closed) or the context is cancelled. It is the entire synchronization surface
// between the concurrent tenant goroutine and each subscription's phase-2
// resolver — split out so the ordering invariant is unit-testable without a
// live scan. On ctx cancellation it returns so a cancelled scan never hangs;
// the downstream resolvers honour the cancelled ctx themselves.
func waitForTenant(ctx context.Context, done <-chan struct{}) {
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// reportPanic recovers a panicking scan goroutine and reports it as a scan
// error instead of letting the panic crash the whole process. Scanner and
// resolver extract closures dereference deeply-nested SDK pointer fields; a nil
// deref in any one of them must degrade to a reported error for that
// service/scope, never abort the scan — the panic-case extension of the
// "errors never abort scan" contract (providers/CLAUDE.md). Call deferred.
func reportPanic(st *store.Store, service, scope string) {
	if r := recover(); r != nil {
		st.ReportError(store.ScanError{
			Provider: "azure", Service: service, Scope: scope,
			Message: fmt.Sprintf("panic: %v", r),
		})
	}
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
	// tenantID is the AAD tenant GUID, resolved once in Scan and stamped onto
	// every subscription. Tenant-scope scanners/resolvers use it as the
	// AccountID for tenant-identical resources (management groups, built-in
	// role/policy definitions) so they are stored once per tenant rather than
	// once per subscription. Empty when tenant resolution failed — callers must
	// fall back to per-subscription behavior.
	tenantID string
	// tenantName is the tenant's friendly display name, resolved once in Scan
	// via Microsoft Graph's /organization endpoint and stamped onto every
	// subscription alongside tenantID. Best-effort: empty when Graph is
	// unreachable / unauthorized, in which case tenant-scope rows label with
	// the GUID instead. Display-only — never used as an account key.
	tenantName string
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

// tenantScopeLabel renders the scope column for tenant-scope service rows
// (Entra ID, management groups, built-in role/policy definitions). Mirrors
// the per-subscription scopeLabel "friendly name, else GUID" shape: prefer the
// tenant display name, fall back to the tenant GUID, and only as a last resort
// the literal "tenant" placeholder when neither resolved (degraded mode). The
// tenant identity is uniform across subs, so the first one is representative.
func tenantScopeLabel(subs []subscription) string {
	if len(subs) > 0 {
		if subs[0].tenantName != "" {
			return subs[0].tenantName
		}
		if subs[0].tenantID != "" {
			return subs[0].tenantID
		}
	}
	return "tenant"
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
