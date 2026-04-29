package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

func init() {
	registerService(serviceEntry{
		name: "azure:aks",
		fn:   scanAKS,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.containerservice", DiscoType: TypeContainerServiceManagedCluster},
		},
	})
}

// scanAKS discovers Azure Kubernetes Service managed clusters.
func scanAKS(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcontainerservice.NewManagedClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcontainerservice:NewManagedClustersClient: %w", err)
	}
	return azPageScan(ctx, "armcontainerservice:ManagedClusters.List", sub, st,
		client.NewListPager(nil),
		func(page armcontainerservice.ManagedClustersClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, cluster := range page.Value {
				if cluster.ID == nil {
					continue
				}
				name, loc := sv(cluster.Name), sv(cluster.Location)
				var status string
				if cluster.Properties != nil && cluster.Properties.ProvisioningState != nil {
					status = *cluster.Properties.ProvisioningState
				}
				r := &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeContainerServiceManagedCluster, NativeID: sv(cluster.ID),
					Name: &name, Region: &loc, Status: &status,
					TagsJSON: azTagsJSON(cluster.Tags), AttributesJSON: mustJSON(cluster),
					DiscoveredBy: scanID,
				}
				if cluster.SystemData != nil {
					r.CreatedAt = tp(cluster.SystemData.CreatedAt)
				}
				batch = append(batch, r)
			}
			return batch, nil
		})
}
