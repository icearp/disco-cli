package azure

import (
	"context"
	"reflect"
	"regexp"
	"runtime"
	"strings"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// registeredDescriptors holds every type declared via the unified registerType
// path. Source for the TestNoDoubleDeclaredTypes guard. Azure aliases stay in
// azureAPITypeMap (it is both the alias source and the mirror-test truth, and
// carries multiple upstream keys per type), so descriptors set no Upstream.
var registeredDescriptors []restype.Descriptor

// descriptorEmits accumulates the coverage decls produced by registerType,
// kept separate from extraEmits so the migration guard can tell descriptor-
// declared types apart from legacy-declared ones.
var descriptorEmits []coverage.TypeDecl

// registerType is the unified per-type registration: it records the descriptor
// and forwards its field rules into the shared redact/volatile/managed engines
// via restype.Emit, whose coverage decl joins descriptorEmits so CollectEmits
// surfaces it. Call from the init() of the file owning the type's upsert.
func registerType(d restype.Descriptor) {
	registeredDescriptors = append(registeredDescriptors, d)
	descriptorEmits = append(descriptorEmits, restype.Emit(d))
}

// CollectEmits returns the deduped union of every emits decl registered
// across the Azure package. Consumed by the coverage.Provider impl.
func CollectEmits() []coverage.TypeDecl {
	out := make([]coverage.TypeDecl, 0, 128)
	out = append(out, descriptorEmits...)
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
	fn    func(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error)
	emits []coverage.TypeDecl
}

// registeredServices is populated by each *_scanners.go file's init(). New
// service: new file + registerService in its init() — no azure.go changes needed.
var registeredServices []serviceEntry

// registerService adds a service to the package-level registry. Panics on
// duplicate name — catches copy-paste errors that would otherwise silently
// scan a service twice.
func registerService(e serviceEntry) {
	for _, s := range registeredServices {
		if s.name == e.name {
			panic("disco: duplicate Azure service registration: " + e.name)
		}
	}
	registeredServices = append(registeredServices, e)
}

// apiResolverEntry is a phase-2 resolver that also makes Azure API calls (e.g.
// diagnostic-settings, fetching per-resource config not captured in phase-1).
// Runs after all phase-1 services complete and before the local-only
// resolvers in registeredResolvers, so st.ListResources returns the full
// resource set. Errors degrade to st.ReportError + ScanService(errCount=1)
// — never propagate.
type apiResolverEntry struct {
	name string
	fn   func(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store) (edges int, err error)
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

// EdgeDecl declares one relationship edge a resolver upserts. Resolvers list
// every distinct (source, target, kind) triple so audit + coverage tooling can
// reason about resolver coverage without needing both endpoints scanned in
// the current subscription.
type EdgeDecl struct {
	Source string // disco type emitting the edge (the resolver's source iteration type)
	Target string // disco type the edge points at
	Kind   string // store.Rel* constant — "attached-to", "uses", "routes-to", etc.
}

// resolverEntry describes a phase-2 relationship resolver. Name is derived from
// the function's reflected name so error reports identify the failing
// resolver. Emits is optional — declared edge shapes power resolver coverage
// tooling.
type resolverEntry struct {
	name  string
	fn    func(sub *subscription, st *store.Store) error
	emits []EdgeDecl
}

// registeredResolvers is populated by each *_resolvers.go file's init().
var registeredResolvers []resolverEntry

// registerResolver adds a resolver to the package-level registry. Variadic
// `emits` is optional metadata — list every distinct (source, target, kind)
// triple upserted. Resolvers without it still register, but their edge
// coverage stays invisible to audit tooling.
func registerResolver(fn func(sub *subscription, st *store.Store) error, emits ...EdgeDecl) {
	registeredResolvers = append(registeredResolvers, resolverEntry{
		name: resolverName(fn), fn: fn, emits: emits,
	})
}

// CollectResolverEdges returns every EdgeDecl declared by every registered
// resolver, deduplicated on (source, target, kind). Order is stable for
// diff-friendly output.
func CollectResolverEdges() []EdgeDecl {
	seen := map[EdgeDecl]struct{}{}
	out := make([]EdgeDecl, 0)
	for _, r := range registeredResolvers {
		for _, e := range r.emits {
			if _, dup := seen[e]; dup {
				continue
			}
			seen[e] = struct{}{}
			out = append(out, e)
		}
	}
	return out
}

// ResolverInfo summarises one registered resolver for coverage tooling:
// unqualified function name, count of declared EdgeDecls, and the distinct
// disco service segments touched (Source+Target combined). EdgeCount==0
// marks an unannotated resolver — either a deliberate no-op (sidecar
// populator) or a sweep target not yet annotated.
type ResolverInfo struct {
	Name      string
	EdgeCount int
	Services  []string
}

// ListResolvers returns one ResolverInfo per registered resolver in
// registration order. Used by `disco coverage resolvers` to discover
// unannotated registrations.
func ListResolvers() []ResolverInfo {
	out := make([]ResolverInfo, 0, len(registeredResolvers))
	for _, r := range registeredResolvers {
		seen := map[string]struct{}{}
		var svcs []string
		for _, e := range r.emits {
			for _, t := range []string{e.Source, e.Target} {
				if s := serviceSegment(t); s != "" {
					if _, dup := seen[s]; !dup {
						seen[s] = struct{}{}
						svcs = append(svcs, s)
					}
				}
			}
		}
		out = append(out, ResolverInfo{Name: r.name, EdgeCount: len(r.emits), Services: svcs})
	}
	return out
}

// serviceSegment returns the middle segment of a disco type
// ("azure:microsoft.compute:virtual-machines" -> "microsoft.compute"). Returns
// "" for malformed inputs.
func serviceSegment(discoType string) string {
	parts := strings.SplitN(discoType, ":", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[1]
}

// resolverName returns the unqualified function name from runtime reflection,
// e.g. "resolvePolicyRelationships". Anonymous closures reflect as
// `pkg.init.funcN`; panic at init so the foot-gun is loud (the name surfaces in
// `disco coverage resolvers` and `ScanError.Service`).
func resolverName(fn any) string {
	full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	short := full
	if i := strings.LastIndex(full, "."); i >= 0 {
		short = full[i+1:]
	}
	if anonResolverNameRE.MatchString(short) {
		panic("disco: registerResolver requires a named function (got anonymous closure " + full + "); extract to a top-level fn")
	}
	return short
}

// anonResolverNameRE matches the `funcN` suffix Go's runtime gives an anonymous
// closure (`pkg.init.func1`, etc.).
var anonResolverNameRE = regexp.MustCompile(`^func\d+$`)
