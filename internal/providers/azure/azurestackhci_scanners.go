package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurestackhci/armazurestackhci"
)

func init() {
	registerService(serviceEntry{
		name: "azure:azurestackhci",
		fn:   scanAzureStackHCI,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.azurestackhci", DiscoType: TypeAzureStackHCICluster, Leaf: true},
		},
	})
}

// scanAzureStackHCI discovers Azure Stack HCI clusters.
func scanAzureStackHCI(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armazurestackhci.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurestackhci:NewClustersClient: %w", err)
	}
	return azSimpleScan(ctx, "armazurestackhci:Clusters.ListBySubscription", TypeAzureStackHCICluster, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armazurestackhci.ClustersClientListBySubscriptionResponse) []*armazurestackhci.Cluster {
			return p.Value
		},
		func(c *armazurestackhci.Cluster) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
