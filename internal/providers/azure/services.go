package azure

import (
	"context"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// serviceEntry describes a scannable Azure service (scoped to one subscription).
type serviceEntry struct {
	name string
	fn   func(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error
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
