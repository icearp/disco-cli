package azure

import (
	"context"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// tenantServiceEntry describes a tenant-scope Azure service (i.e. one whose
// API surface sits ABOVE the subscription boundary). The fn runs ONCE per
// scan, after subscription discovery and concurrently with the per-subscription
// fan-out; each subscription's phase-2 resolvers block on its completion (see
// Scan / waitForTenant) so the principals it writes are present before any
// resolver consumes them.
//
// Targets: Entra ID (Microsoft Graph users / groups / service principals /
// app registrations / directory roles) and any other Microsoft Graph or
// tenant-scope ARM API. Distinct from registeredServices, which fan out
// per-subscription.
//
// The signature receives the discovered subscription set so a tenant scanner
// can correlate tenant principals with the per-sub RBAC scopes already
// collected (e.g. resolve Graph object IDs against subscription role
// assignments). Subscriptions are read-only here — never mutate.
type tenantServiceEntry struct {
	name  string
	fn    func(ctx context.Context, subs []subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error)
	emits []coverage.TypeDecl
}

// registeredTenantServices is populated by each tenant-scope *_scanners.go
// file's init(). Phase 1 (Entra ID) wires the first consumers; the registry
// is intentionally empty in the foundation drop.
var registeredTenantServices []tenantServiceEntry

// registerTenantService adds a tenant-scope service to the registry.
// Panics on duplicate name to catch copy-paste errors at init time.
func registerTenantService(e tenantServiceEntry) {
	for _, s := range registeredTenantServices {
		if s.name == e.name {
			panic("disco: duplicate Azure tenant service registration: " + e.name)
		}
	}
	registeredTenantServices = append(registeredTenantServices, e)
}

// runTenantServices fires every registered tenant-scope service exactly once
// per scan. Scan runs it concurrently with the per-subscription fan-out and
// gates each subscription's phase-2 resolvers on its completion. Errors per
// service are reported
// via st.ReportError + st.ReportService (errCount=1) — never propagated.
// Skipped when no tenant services are registered.
func runTenantServices(ctx context.Context, subs []subscription, cred azcore.TokenCredential, filter []string, st *store.Store, scanID string) {
	if len(registeredTenantServices) == 0 {
		return
	}
	allowed := tenantServiceFilterSet(filter)
	scope := tenantScopeLabel(subs)
	for _, svc := range registeredTenantServices {
		if allowed != nil && !allowed[svc.name] {
			continue
		}
		var newC, changedC atomic.Int64
		total, _, err := svc.fn(ctx, subs, cred, st.WithUpsertCounters(&newC, &changedC), scanID)
		if err != nil {
			st.ReportError(store.ScanError{
				Provider: "azure", Service: svc.name, Scope: scope,
				Message: formatAzureError(err),
			})
			st.ReportService(svc.name, scope, total, int(newC.Load()), int(changedC.Load()), 1, store.ServiceOK)
			continue
		}
		st.ReportService(svc.name, scope, total, int(newC.Load()), int(changedC.Load()), 0, store.ServiceOK)
	}
}

// tenantServiceFilterSet returns nil when filter is empty (allow all), else
// a set of allowed service names. Shared with the per-sub dispatch shape so
// a single --services flag can target both tenant and subscription services.
func tenantServiceFilterSet(filter []string) map[string]bool {
	if len(filter) == 0 {
		return nil
	}
	out := make(map[string]bool, len(filter))
	for _, name := range filter {
		out[name] = true
	}
	return out
}
