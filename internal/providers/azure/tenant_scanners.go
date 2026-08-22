package azure

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/store"
)

// tenantServiceEntry describes a tenant-scope Azure service (API surface
// above the subscription boundary). fn runs ONCE per scan, after subscription
// discovery and concurrently with the per-subscription fan-out; each
// subscription's phase-2 resolvers block on its completion (see Scan /
// waitForTenant) so its written principals are present before any resolver
// consumes them.
//
// Targets: Entra ID (Microsoft Graph users / groups / service principals /
// app registrations / directory roles) and other Graph or tenant-scope ARM
// APIs. Distinct from registeredServices, which fans out per-subscription.
//
// The signature receives the discovered subscription set so a tenant scanner
// can correlate tenant principals with already-collected per-sub RBAC scopes
// (e.g. Graph object IDs against subscription role assignments).
// Subscriptions are read-only here — never mutate.
type tenantServiceEntry struct {
	name string
	fn   func(ctx context.Context, subs []subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error)
	// dedupOnly marks a tenant phase that READS NO DIRECTORY: it fetches data
	// identical in every directory (Microsoft-shipped built-ins) once instead
	// of once per subscription, and the per-sub scanners store their own copy
	// whenever it does not run. Such a service loses nothing when the
	// federation gate suppresses it, which is the difference
	// reportTenantScopeSkipped has to tell an operator.
	dedupOnly bool
	emits     []coverage.TypeDecl
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
// per scan, concurrently with the per-subscription fan-out, and gates each
// subscription's phase-2 resolvers on its completion. Per-service errors go
// through st.ReportError + st.ReportService (errCount=1) — never propagated.
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

// reportTenantScopeSkipped records a notice per tenant-scope service the
// federation gate suppressed, plus one warning for the phase when any of them
// read a directory, for a scan whose credential's directory cannot be
// confirmed to be the scanned tenant's (see wifConfig.tenantScopeEnabled —
// the gate keys on the WIF contract being set, not on the directory actually
// differing, so this fires for self-federation too).
//
// Reported rather than silently skipped: a tenant service that writes nothing
// and says nothing is indistinguishable from a tenant that genuinely has no
// management groups or no directory objects, and the difference decides
// whether an empty result is a finding.
//
// Reach differs by kind, which is why the directory case does not rely on the
// notice: scanrun persists warnings and only RETURNS notices, so a notice
// reaches the CLI renderer and leaves no trace on a SaaS scan record. A
// dedupOnly-only suppression therefore reports to the CLI alone — acceptable
// because it loses no rows, unlike the directory case.
//
// Never ServiceDisabled, which means the customer has not enabled something
// they could enable and renders through tenantNoun as "(subscription:
// disabled)" — two claims that are both false here: nothing on their side is
// off, and the scope is the tenant.
//
// The two kinds of suppression differ in SEVERITY as well as wording. A
// suppressed dedupOnly phase reached the right answer by another route — the
// per-subscription scanners each keep their own copy of the same
// Microsoft-shipped rows, and that service reports real counts under this same
// name later in the run — so it is a notice and nothing more. A suppressed
// DIRECTORY read changed coverage, which store.ScanNotice's contract reserves
// for a warning; the phase raises exactly one, for the reason at the emission
// site.
//
// Honours the --services filter so a run that never asked for these services
// does not report them.
func reportTenantScopeSkipped(st *store.Store, subs []subscription, filter []string) {
	allowed := tenantServiceFilterSet(filter)
	scope := tenantScopeLabel(subs)

	var skipped []string
	for _, svc := range registeredTenantServices {
		if allowed != nil && !allowed[svc.name] {
			continue
		}
		if svc.dedupOnly {
			st.ReportNotice(store.ScanNotice{
				Provider: "azure", Service: svc.name, Scope: scope,
				Message: "tenant-wide deduplication skipped under a federated credential: each subscription stores its own copy of the Microsoft-shipped definitions instead",
			})
			continue
		}
		skipped = append(skipped, svc.name)
		st.ReportNotice(store.ScanNotice{
			Provider: "azure", Service: svc.name, Scope: scope,
			Message: "skipped: this scan uses a federated credential whose directory cannot be confirmed to be the scanned tenant's, so tenant-scope results could describe a different directory",
		})
		// Zero counts and zero errors: the progress line accounts for the
		// service, and the warning below carries the severity. An errCount
		// here would render "(with errors)" and claim a failure that did not
		// happen. Not emitted for a dedupOnly phase: the per-sub service
		// reports real counts under the same name, and a 0-count row beside
		// it reads as a contradiction.
		st.ReportService(svc.name, scope, 0, 0, 0, 0, store.ServiceOK)
	}

	// ONE warning for the phase, not one per service. Both halves of
	// store.ScanNotice's contract have to hold at once: coverage genuinely
	// changed, so a notice alone would leave it outside the "N warnings" count
	// and outside scanrun's persistence — which drops notices entirely, so on
	// the SaaS the notice half is write-only. But this fires on EVERY federated
	// scan, permanently, and the same doc warns that a warning firing on every
	// healthy scan trains people to ignore the block. Per-service fan-out would
	// also grow the count as tenant services are added, which is precisely what
	// the phase-wide gate is designed to absorb.
	if len(skipped) > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "azure", Service: "azure:tenant-scope", Scope: scope,
			Message: "tenant-scope services skipped (" + strings.Join(skipped, ", ") +
				"): this scan uses a federated credential (DISCO_AZURE_WIF_CLIENT_ID) whose directory cannot be confirmed to be the scanned tenant's, so directory and management-group coverage is absent from this scan. No setting re-enables these services",
		})
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
