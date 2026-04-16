package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

func init() { registerService(serviceEntry{name: "azure:aks", fn: scanAKS}) }

// scanAKS discovers Azure Kubernetes Service managed clusters.
func scanAKS(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcontainerservice.NewManagedClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcontainerservice:NewManagedClustersClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcontainerservice:ManagedClusters.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcontainerservice:ManagedClusters.List: %w", err)
		}
		var batch []*store.Resource
		for _, cluster := range page.Value {
			if cluster.ID == nil {
				continue
			}
			name := sv(cluster.Name)
			location := sv(cluster.Location)
			var status string
			if cluster.Properties != nil && cluster.Properties.ProvisioningState != nil {
				status = *cluster.Properties.ProvisioningState
			}
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeContainerServiceManagedCluster,
				NativeID:       sv(cluster.ID),
				Name:           &name,
				Region:         &location,
				Status:         &status,
				AttributesJSON: mustJSON(cluster),
				DiscoveredBy:   scanID,
			}
			if cluster.SystemData != nil {
				r.CreatedAt = tp(cluster.SystemData.CreatedAt)
			}
			if cluster.Tags != nil {
				s := mustJSON(cluster.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert AKS clusters: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
