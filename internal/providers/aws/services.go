package aws

import (
	"context"

	"codeburg.org/icearp/disco/internal/store"
)

// serviceEntry describes a scannable AWS service.
type serviceEntry struct {
	name   string
	global bool // global = once per account (region ignored); regional = once per region
	fn     func(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error
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

// resolverEntry describes a phase-2 relationship resolver.
type resolverEntry struct {
	fn func(acct *account, st *store.Store) error
}

// registeredResolvers is populated by each *_resolvers.go file's init().
var registeredResolvers []resolverEntry

// registerResolver adds a resolver to the package-level registry.
func registerResolver(fn func(acct *account, st *store.Store) error) {
	registeredResolvers = append(registeredResolvers, resolverEntry{fn: fn})
}
