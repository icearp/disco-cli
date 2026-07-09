package aws

import (
	"context"
	"reflect"
	"regexp"
	"runtime"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
)

// serviceEntry describes a scannable AWS service.
//
// emits enumerates every disco type the scanner upserts. Coverage truth
// source — `disco coverage` reads aggregated emits via CollectEmits(),
// NOT KnownTypes().
type serviceEntry struct {
	name   string
	global bool // global = once per account (region ignored); regional = once per region
	optIn  bool // optIn = excluded from the default scan; runs only when explicitly selected (--services <name> or a dedicated --include flag)
	fn     func(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error)
	emits  []coverage.TypeDecl
}

// registeredDescriptors holds every type declared via the unified registerType
// path. Source of truth for descriptor-derived aliases and the
// TestNoDoubleDeclaredTypes guard.
var registeredDescriptors []restype.Descriptor

// descriptorEmits accumulates the coverage decls produced by registerType.
var descriptorEmits []coverage.TypeDecl

// registerType is the unified per-type registration: it records the descriptor
// and forwards its field rules into the shared redact/volatile/managed engines
// via restype.Emit, whose coverage decl joins descriptorEmits so CollectEmits
// surfaces it. Call from the init() of the file owning the type's upsert.
func registerType(d restype.Descriptor) {
	registeredDescriptors = append(registeredDescriptors, d)
	descriptorEmits = append(descriptorEmits, restype.Emit(d))
}

// descriptorAliases returns the disco-type -> upstream-key overrides declared
// via registerType (empty Upstream falls through to AlgorithmicKey).
func descriptorAliases() map[string]string {
	out := make(map[string]string, len(registeredDescriptors))
	for _, d := range registeredDescriptors {
		if d.Upstream != "" {
			out[d.Type] = d.Upstream
		}
	}
	return out
}

// CollectEmits returns the deduped union of every emits decl registered
// across the AWS package. Consumed by the coverage.Provider impl.
func CollectEmits() []coverage.TypeDecl {
	out := make([]coverage.TypeDecl, 0, 256)
	out = append(out, descriptorEmits...)
	for _, s := range registeredServices {
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

// registeredServices is populated by each *_scanners.go file's init().
// Adding a service only requires a new file calling registerService from its
// init() — no aws.go changes needed.
var registeredServices []serviceEntry

// registerService adds a service to the package-level registry.
// Panics if a service with the same name has already been registered, which
// catches copy-paste errors that would otherwise silently scan a service twice.
func registerService(e serviceEntry) {
	for _, s := range registeredServices {
		if s.name == e.name {
			panic("disco: duplicate AWS service registration: " + e.name)
		}
	}
	registeredServices = append(registeredServices, e)
}

// EdgeDecl declares one (source disco type, target disco type, edge kind)
// triple a resolver emits. Optional, but resolvers that declare their edges
// let `disco coverage` and the aws-resolver-audit tool reason about resolver
// coverage without observing actual DB edges (which require both endpoints
// to be scanned in the current account).
type EdgeDecl struct {
	Source string // disco type emitting the edge (the resolver's source iteration type)
	Target string // disco type the edge points at
	Kind   string // store.Rel* constant — "attached-to", "uses", "routes-to", etc.
}

// resolverEntry describes a phase-2 relationship resolver. Name is derived
// from the function's reflected name so error reports can identify the
// failing resolver without each call site spelling its own name. Emits is
// optional — resolvers that declare their edge shapes power resolver
// coverage tooling.
type resolverEntry struct {
	name  string
	fn    func(acct *account, st *store.Store) error
	emits []EdgeDecl
}

// registeredResolvers is populated by each *_resolvers.go file's init().
var registeredResolvers []resolverEntry

// registerResolver adds a resolver to the package-level registry. The
// variadic `emits` argument is optional metadata — list every distinct
// (source, target, kind) triple the resolver upserts. Resolvers without an
// emits list still register, but their edge coverage is invisible to the
// audit tooling.
func registerResolver(fn func(acct *account, st *store.Store) error, emits ...EdgeDecl) {
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
// disco service segments touched by those edges (Source+Target combined).
// EdgeCount==0 marks an unannotated resolver — either a deliberate no-op
// (sidecar populator, audit-stub) or a sweep target not yet annotated.
// cmd-side tooling consumes Services for the `disco coverage resolvers
// --services` filter.
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

// serviceSegment returns the middle segment of a disco type ("aws:ec2:instance"
// -> "ec2"). Returns "" for malformed inputs.
func serviceSegment(discoType string) string {
	parts := strings.SplitN(discoType, ":", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[1]
}

// resolverName returns the unqualified function name from runtime reflection,
// e.g. "resolveBackupVaults" for codeberg.org/.../aws.resolveBackupVaults.
//
// Anonymous closures registered via `registerResolver(func(...) {...})`
// reflect as `pkg.init.funcN` — the trimmed suffix would be the meaningless
// `funcN` and would surface in `disco coverage resolvers` and
// `ScanError.Service`. Panic at init time so the foot-gun is loud.
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

// anonResolverNameRE matches the `funcN` suffix that Go's runtime gives an
// anonymous closure (`pkg.init.func1`, `pkg.someFn.func2`, etc.).
var anonResolverNameRE = regexp.MustCompile(`^func\d+$`)
