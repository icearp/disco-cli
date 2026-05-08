package azure

import (
	"context"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// extraEmits accumulates disco-type decls for non-serviceEntry sources —
// child scanner files of multi-file services (compute_*, sql_*, dns_*) and
// resolver-side synthetic stubs.
var extraEmits []coverage.TypeDecl

// registerExtraEmits is for non-serviceEntry sources of disco types. Call
// from init() in the file that owns the upsert site.
func registerExtraEmits(decls ...coverage.TypeDecl) {
	extraEmits = append(extraEmits, decls...)
}

// CollectEmits returns the deduped union of every emits decl registered
// across the Azure package. Consumed by the coverage.Provider impl.
func CollectEmits() []coverage.TypeDecl {
	out := make([]coverage.TypeDecl, 0, 128)
	out = append(out, extraEmits...)
	for _, s := range registeredServices {
		out = append(out, s.emits...)
	}
	for _, s := range registeredTenantServices {
		out = append(out, s.emits...)
	}
	seen := make(map[string]bool, len(out))
	deduped := out[:0]
	for _, d := range out {
		if seen[d.DiscoType] {
			continue
		}
		seen[d.DiscoType] = true
		deduped = append(deduped, d)
	}
	return deduped
}

// serviceEntry describes a scannable Azure service (scoped to one subscription).
type serviceEntry struct {
	name  string
	fn    func(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error)
	emits []coverage.TypeDecl
}

// registeredServices is populated by each *_scanners.go file's init().
// Adding a new service only requires creating a new file and calling registerService
// from its init() — no changes to azure.go are needed.
var registeredServices []serviceEntry

// registerService adds a service to the package-level registry.
// Panics if a service with the same name has already been registered, which
// catches copy-paste errors that would otherwise silently scan a service twice.
func registerService(e serviceEntry) {
	for _, s := range registeredServices {
		if s.name == e.name {
			panic("disco: duplicate Azure service registration: " + e.name)
		}
	}
	registeredServices = append(registeredServices, e)
}

// apiResolverEntry is a phase-2 resolver that ALSO needs to make Azure API
// calls (i.e. cross-cutting resolvers like diagnostic-settings, which fetch
// per-resource configuration not captured during phase-1 scanners). It runs
// AFTER all phase-1 services have completed and BEFORE the local-only
// resolvers in registeredResolvers, so st.ListResources returns the full
// resource set. Errors degrade to st.ReportError + ScanService(errCount=1)
// — never propagate.
type apiResolverEntry struct {
	name string
	fn   func(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store) (edges int, err error)
}

// registeredAPIResolvers is populated by *_resolvers.go init() blocks that
// need API access. Distinct from registeredResolvers (DB-only) because the
// signature carries ctx + cred.
var registeredAPIResolvers []apiResolverEntry

// registerAPIResolver adds an API-driven resolver to the registry.
// Panics on duplicate name to catch copy-paste errors at init time.
func registerAPIResolver(e apiResolverEntry) {
	for _, r := range registeredAPIResolvers {
		if r.name == e.name {
			panic("disco: duplicate Azure API resolver registration: " + e.name)
		}
	}
	registeredAPIResolvers = append(registeredAPIResolvers, e)
}

// resolverEntry describes a phase-2 relationship resolver.
type resolverEntry struct {
	fn func(sub *subscription, st *store.Store) error
}

// registeredResolvers is populated by each *_resolvers.go file's init().
var registeredResolvers []resolverEntry

// registerResolver adds a resolver to the package-level registry.
func registerResolver(fn func(sub *subscription, st *store.Store) error) {
	registeredResolvers = append(registeredResolvers, resolverEntry{fn: fn})
}
