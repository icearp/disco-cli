package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
)

func init() { registerService(serviceEntry{name: "azure:containerregistry", fn: scanContainerRegistry}) }

// scanContainerRegistry discovers Azure Container Registry (ACR) registries.
// Replications, webhooks, tasks, scope-maps, tokens, cache rules, and private
// link resources deferred — narrow cross-service edge value relative to volume.
func scanContainerRegistry(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcontainerregistry.NewRegistriesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcontainerregistry:NewRegistriesClient: %w", err)
	}
	return azPageScan(ctx, "armcontainerregistry:Registries.List", sub, st,
		client.NewListPager(nil),
		func(page armcontainerregistry.RegistriesClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, r := range page.Value {
				if r == nil || r.ID == nil {
					continue
				}
				name, loc := sv(r.Name), sv(r.Location)
				nativeID := sv(r.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeContainerRegistryRegistry, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(r.Tags), AttributesJSON: mustJSON(r),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeContainerRegistryRegistry, nativeID))
				}
			}
			return batch, pairs
		})
}
