package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/networkcloud/armnetworkcloud"
)

func init() {
	registerService(serviceEntry{
		name: "azure:networkcloud",
		fn:   scanNetworkCloud,
		emits: []coverage.TypeDecl{
			// Custom-location edge wired centrally; the Operator Nexus cluster
			// ships scanner-only.
			{Service: "microsoft.networkcloud", DiscoType: TypeNetworkCloudCluster, Leaf: true},
		},
	})
}

// scanNetworkCloud discovers Azure Operator Nexus (Network Cloud) clusters.
func scanNetworkCloud(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetworkcloud.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkcloud:NewClustersClient: %w", err)
	}
	return azSimpleScan(ctx, "armnetworkcloud:Clusters.ListBySubscription", TypeNetworkCloudCluster, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armnetworkcloud.ClustersClientListBySubscriptionResponse) []*armnetworkcloud.Cluster {
			return p.Value
		},
		func(c *armnetworkcloud.Cluster) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
