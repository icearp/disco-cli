package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/horizondb/armhorizondb"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.horizondb",
		fn:   scanHorizonDB,
		emits: []coverage.TypeDecl{
			// Private-endpoint → target edges resolved centrally; carries no
			// other in-scope ARM-ID reference, so both ship scanner-only.
			{Service: "microsoft.horizondb", DiscoType: TypeHorizonDBCluster, Leaf: true},
			{Service: "microsoft.horizondb", DiscoType: TypeHorizonDBParameterGroup, Leaf: true},
		},
	})
}

// scanHorizonDB discovers Azure HorizonDB clusters and parameter groups.
func scanHorizonDB(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	clusters, err := armhorizondb.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhorizondb:NewClustersClient: %w", err)
	}
	pgroups, err := armhorizondb.NewParameterGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhorizondb:NewParameterGroupsClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armhorizondb:Clusters.ListBySubscription", TypeHorizonDBCluster, sub, st, scanID,
				clusters.NewListBySubscriptionPager(nil),
				func(p armhorizondb.ClustersClientListBySubscriptionResponse) []*armhorizondb.Cluster { return p.Value },
				func(c *armhorizondb.Cluster) azTrackedBase {
					return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armhorizondb:ParameterGroups.ListBySubscription", TypeHorizonDBParameterGroup, sub, st, scanID,
				pgroups.NewListBySubscriptionPager(nil),
				func(p armhorizondb.ParameterGroupsClientListBySubscriptionResponse) []*armhorizondb.ParameterGroup {
					return p.Value
				},
				func(g *armhorizondb.ParameterGroup) azTrackedBase {
					return azTrackedBase{id: sv(g.ID), name: sv(g.Name), location: sv(g.Location), tags: g.Tags, full: g}
				})
		},
	)
}
