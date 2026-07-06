package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.containerregistry",
		fn:   scanContainerRegistry,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.containerregistry", DiscoType: TypeContainerRegistryRegistry},
		},
	})
}

// scanContainerRegistry discovers Azure Container Registry (ACR) registries.
// Replications, webhooks, tasks, scope-maps, tokens, cache rules, and private
// link resources deferred — low cross-service edge value relative to volume.
func scanContainerRegistry(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcontainerregistry.NewRegistriesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcontainerregistry:NewRegistriesClient: %w", err)
	}
	return azSimpleScan(ctx, "armcontainerregistry:Registries.List", TypeContainerRegistryRegistry, sub, st, scanID,
		client.NewListPager(nil),
		func(p armcontainerregistry.RegistriesClientListResponse) []*armcontainerregistry.Registry {
			return p.Value
		},
		func(r *armcontainerregistry.Registry) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
