package gcp

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
)

// serviceEntry describes a scannable GCP service (scoped to one project).
//
// emits enumerates every disco type the scanner upserts — coverage truth
// source for the `disco coverage` matrix, aggregated across
// registeredServices + registeredOrgServices + extraEmits (hierarchy_scanners
// + resolver-side synthetic stubs).
type serviceEntry struct {
	name  string
	fn    func(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error)
	emits []coverage.TypeDecl
}

// registeredServices is populated by each *_scanners.go file's init().
// New service: new file + registerService call from its init() — no gcp.go
// changes needed.
var registeredServices []serviceEntry

// registerService adds a service to the package-level registry.
// Panics on duplicate name — catches copy-paste errors that would otherwise
// silently scan a service twice.
func registerService(e serviceEntry) {
	for _, s := range registeredServices {
		if s.name == e.name {
			panic("disco: duplicate GCP service registration: " + e.name)
		}
	}
	registeredServices = append(registeredServices, e)
}

// orgScope identifies an organization- or folder-scope target for org-services.
// Populated by scanHierarchy from the project parents discovered during the scan.
type orgScope struct {
	Kind     string // "organization" or "folder"
	Name     string // canonical GCP name, e.g. "organizations/123" / "folders/456"
	Resource string // disco resource ID (store.ResourceID(...) result)
}

// orgServiceEntry describes a scannable org/folder-scope GCP service. The fn
// runs ONCE per scan (not once per project) and receives every org+folder
// scope discovered by scanHierarchy. Targets: VPC Service Controls, folder/org
// IAM policies, org-scope Logging sinks. Empty scopes => fn is skipped.
type orgServiceEntry struct {
	name  string
	fn    func(ctx context.Context, scopes []orgScope, st *store.Store, scanID string) (total, inserted int, err error)
	emits []coverage.TypeDecl
}

// extraEmits accumulates disco-type decls for code paths that do NOT
// flow through registerService / registerOrgService — namely:
//   - hierarchy_scanners.go (called direct from gcp.go's scanHierarchy)
//
// CollectEmits unions registeredServices + registeredOrgServices +
// extraEmits and dedupes by DiscoType.
var extraEmits []coverage.TypeDecl

// registerExtraEmits is for non-serviceEntry sources of disco types.
// Call from init() in the file that owns the upsert site.
func registerExtraEmits(decls ...coverage.TypeDecl) {
	extraEmits = append(extraEmits, decls...)
}

// CollectEmits returns the deduped union of every emits decl registered
// across the GCP package. Consumed by the coverage.Provider impl.
func CollectEmits() []coverage.TypeDecl {
	out := make([]coverage.TypeDecl, 0, 64)
	out = append(out, extraEmits...)
	for _, s := range registeredServices {
		out = append(out, s.emits...)
	}
	for _, s := range registeredOrgServices {
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

// registeredOrgServices is populated by org-scope *_scanners.go files' init().
var registeredOrgServices []orgServiceEntry

// registerOrgService adds an org/folder-scope service to the registry.
// Panics on duplicate name to catch copy-paste errors at init time.
func registerOrgService(e orgServiceEntry) {
	for _, s := range registeredOrgServices {
		if s.name == e.name {
			panic("disco: duplicate GCP org service registration: " + e.name)
		}
	}
	registeredOrgServices = append(registeredOrgServices, e)
}

// runOrgServices fires every registered org/folder-scope service exactly once
// per scan, after scanHierarchy and before per-project fan-out. Errors per
// service are reported via st.ReportError + st.ReportService, never
// propagated (matches scanProject's per-service convention). Skipped when no
// org/folder scopes were resolved (e.g. user only has project-level creds).
func runOrgServices(ctx context.Context, scopes []orgScope, filter []string, st *store.Store, scanID string) {
	if len(scopes) == 0 || len(registeredOrgServices) == 0 {
		return
	}
	allowed := serviceFilterSet(filter)
	for _, svc := range registeredOrgServices {
		if allowed != nil && !allowed[svc.name] {
			continue
		}
		var newC, changedC atomic.Int64
		total, _, err := svc.fn(ctx, scopes, st.WithUpsertCounters(&newC, &changedC), scanID)
		if err != nil {
			// API-not-enabled at org scope (accesscontextmanager, org-policy,
			// etc.) returns errServiceDisabled — mirrors scanProject, surfaces
			// "(project: disabled)" instead of an error.
			if errors.Is(err, errServiceDisabled) {
				st.ReportService(svc.name, "org", 0, 0, 0, 0, store.ServiceDisabled)
				continue
			}
			st.ReportError(store.ScanError{
				Provider: "gcp", Service: svc.name, Scope: "org",
				Message: err.Error(),
			})
			st.ReportService(svc.name, "org", total, int(newC.Load()), int(changedC.Load()), 1, store.ServiceOK)
			continue
		}
		st.ReportService(svc.name, "org", total, int(newC.Load()), int(changedC.Load()), 0, store.ServiceOK)
	}
}

// serviceFilterSet returns nil for an empty filter (i.e. allow all), or a set
// of allowed names. Shared by the project-scope and org-scope dispatch loops.
func serviceFilterSet(filter []string) map[string]bool {
	if len(filter) == 0 {
		return nil
	}
	out := make(map[string]bool, len(filter))
	for _, name := range filter {
		out[name] = true
	}
	return out
}

// EdgeDecl declares one (source disco type, target disco type, edge kind)
// triple a resolver emits. Optional, but resolvers that declare their edges
// let `disco coverage resolvers` reason about resolver coverage without
// observing actual DB edges (which require both endpoints scanned in the
// current project). Mirrors aws.EdgeDecl.
type EdgeDecl struct {
	Source string // disco type emitting the edge (the resolver's source iteration type)
	Target string // disco type the edge points at
	Kind   string // store.Rel* constant — "attached-to", "uses", "routes-to", etc.
}

// resolverEntry describes a phase-2 relationship resolver. Name is derived
// from the function's reflected name (see resolverName) so call sites don't
// spell their own. emits is optional metadata powering resolver coverage
// tooling.
type resolverEntry struct {
	name  string
	fn    func(p *project, st *store.Store) error
	emits []EdgeDecl
}

// registeredResolvers is populated by each *_resolvers.go file's init().
var registeredResolvers []resolverEntry

// registerResolver adds a resolver to the package-level registry. The
// variadic `emits` argument is optional — list every distinct
// (source, target, kind) triple the resolver upserts. Resolvers without an
// emits list still register, but their edge coverage is invisible to
// `disco coverage resolvers`.
func registerResolver(fn func(p *project, st *store.Store) error, emits ...EdgeDecl) {
	registeredResolvers = append(registeredResolvers, resolverEntry{
		name: resolverFnName(fn), fn: fn, emits: emits,
	})
}

// orgResolverEntry describes a phase-2 relationship resolver over org/
// customer-scoped resource types (accesscontextmanager, cloudidentity) —
// types whose AccountID is an org/folder/customer identifier, not a project
// ID. Runs ONCE per scan (mirrors orgServiceEntry's once-per-scan scanner
// lane), after every per-project resolver has finished, so it can freely
// query across every project/org/customer scope in the store without a
// per-project AccountID filter.
type orgResolverEntry struct {
	name  string
	fn    func(st *store.Store) error
	emits []EdgeDecl
}

// registeredOrgResolvers is populated by each org-scoped *_resolvers.go
// file's init().
var registeredOrgResolvers []orgResolverEntry

// registerOrgResolver adds an org-scoped resolver to the package-level
// registry. Mirrors registerResolver — see its doc for the emits contract.
func registerOrgResolver(fn func(st *store.Store) error, emits ...EdgeDecl) {
	registeredOrgResolvers = append(registeredOrgResolvers, orgResolverEntry{
		name: resolverFnName(fn), fn: fn, emits: emits,
	})
}

// resolverFnName returns the unqualified function name from runtime
// reflection, e.g. "resolveFirewallRelationships". Anonymous closures reflect
// as "pkg.init.funcN" — panic at init time so the foot-gun (an unnamed
// resolver polluting coverage-tool / ScanError output) is loud, mirroring
// aws.resolverName. Takes `any` so both registerResolver's and
// registerOrgResolver's distinct fn signatures share one implementation.
func resolverFnName(fn any) string {
	full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	short := full
	if i := strings.LastIndex(full, "."); i >= 0 {
		short = full[i+1:]
	}
	if anonResolverNameRE.MatchString(short) {
		panic("disco: registerResolver requires a named function, got anonymous closure: " + full)
	}
	return short
}

var anonResolverNameRE = regexp.MustCompile(`^func\d+$`)

// serviceSegment returns the middle segment of a disco type
// ("gcp:compute:instance" -> "compute"). Returns "" for malformed inputs.
func serviceSegment(discoType string) string {
	parts := strings.SplitN(discoType, ":", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[1]
}

// ListResolvers returns one coverage.ResolverInfo per registered resolver
// (per-project + org-scoped, in that registration order) — the
// coverage.ResolverAuditor implementation backing `disco coverage resolvers
// --providers gcp`.
func ListResolvers() []coverage.ResolverInfo {
	out := make([]coverage.ResolverInfo, 0, len(registeredResolvers)+len(registeredOrgResolvers))
	for _, r := range registeredResolvers {
		out = append(out, resolverInfo(r.name, r.emits))
	}
	for _, r := range registeredOrgResolvers {
		out = append(out, resolverInfo(r.name, r.emits))
	}
	return out
}

func resolverInfo(name string, emits []EdgeDecl) coverage.ResolverInfo {
	seen := map[string]struct{}{}
	var svcs []string
	for _, e := range emits {
		for _, t := range []string{e.Source, e.Target} {
			if s := serviceSegment(t); s != "" {
				if _, dup := seen[s]; !dup {
					seen[s] = struct{}{}
					svcs = append(svcs, s)
				}
			}
		}
	}
	return coverage.ResolverInfo{Name: name, EdgeCount: len(emits), Services: svcs}
}

// ResolverEdgeSources returns the distinct EdgeDecl.Source disco-types across
// every registered resolver (per-project + org-scoped) — backs `disco
// coverage resolvers --missing`, which reports emitted disco types never
// appearing as a resolver source.
func ResolverEdgeSources() []string {
	seen := map[string]struct{}{}
	var out []string
	collect := func(emits []EdgeDecl) {
		for _, e := range emits {
			if _, dup := seen[e.Source]; !dup {
				seen[e.Source] = struct{}{}
				out = append(out, e.Source)
			}
		}
	}
	for _, r := range registeredResolvers {
		collect(r.emits)
	}
	for _, r := range registeredOrgResolvers {
		collect(r.emits)
	}
	return out
}
