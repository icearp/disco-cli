package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/horizondb/armhorizondb"
)

func init() {
	registerService(serviceEntry{
		name: "azure:horizondb",
		fn:   scanHorizonDB,
		emits: []coverage.TypeDecl{
			// Private-endpoint → target edges resolved centrally; the cluster
			// carries no other in-scope ARM-ID reference, so it ships
			// scanner-only.
			{Service: "microsoft.horizondb", DiscoType: TypeHorizonDBCluster, Leaf: true},
		},
	})
}

// scanHorizonDB discovers Azure HorizonDB clusters.
func scanHorizonDB(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armhorizondb.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhorizondb:NewClustersClient: %w", err)
	}
	return azSimpleScan(ctx, "armhorizondb:Clusters.ListBySubscription", TypeHorizonDBCluster, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armhorizondb.ClustersClientListBySubscriptionResponse) []*armhorizondb.Cluster { return p.Value },
		func(c *armhorizondb.Cluster) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
