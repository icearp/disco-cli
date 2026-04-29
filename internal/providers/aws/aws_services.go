package aws

import (
	"context"
	"reflect"
	"runtime"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
)

// serviceEntry describes a scannable AWS service.
//
// emits enumerates every disco type the scanner upserts. Coverage truth
// source — `disco coverage` reads aggregated emits via CollectEmits(),
// NOT KnownTypes().
type serviceEntry struct {
	name   string
	global bool // global = once per account (region ignored); regional = once per region
	fn     func(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error)
	emits  []coverage.TypeDecl
}

// extraEmits accumulates disco-type decls for code paths that do NOT flow
// through registerService — namely resolver-side synthetic stubs (e.g.
// cross-account-trust foreign-account, AWS-managed-policy catalogue stubs)
// declared from the file that owns the upsert site.
var extraEmits []coverage.TypeDecl

// registerExtraEmits is for non-serviceEntry sources of disco types. Call
// from init() in the file that owns the upsert site.
func registerExtraEmits(decls ...coverage.TypeDecl) {
	extraEmits = append(extraEmits, decls...)
}

// CollectEmits returns the deduped union of every emits decl registered
// across the AWS package. Consumed by the coverage.Provider impl.
func CollectEmits() []coverage.TypeDecl {
	out := make([]coverage.TypeDecl, 0, 256)
	out = append(out, extraEmits...)
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
// Adding a new service only requires creating a new file and calling registerService
// from its init() — no changes to aws.go are needed.
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

// resolverEntry describes a phase-2 relationship resolver. Name is derived
// from the function's reflected name so error reports can identify the
// failing resolver without each call site spelling its own name.
type resolverEntry struct {
	name string
	fn   func(acct *account, st *store.Store) error
}

// registeredResolvers is populated by each *_resolvers.go file's init().
var registeredResolvers []resolverEntry

// registerResolver adds a resolver to the package-level registry.
func registerResolver(fn func(acct *account, st *store.Store) error) {
	registeredResolvers = append(registeredResolvers, resolverEntry{name: resolverName(fn), fn: fn})
}

// resolverName returns the unqualified function name from runtime reflection,
// e.g. "resolveBackupVaults" for codeberg.org/.../aws.resolveBackupVaults.
func resolverName(fn any) string {
	full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	if i := strings.LastIndex(full, "."); i >= 0 {
		return full[i+1:]
	}
	return full
}
