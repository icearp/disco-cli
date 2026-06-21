package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/kusto/armkusto"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.kusto",
		fn:   scanKusto,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.kusto", DiscoType: TypeKustoCluster},
		},
	})
}

// scanKusto discovers Azure Data Explorer (Kusto) clusters.
func scanKusto(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armkusto.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armkusto:NewClustersClient: %w", err)
	}
	return azSimpleScan(ctx, "armkusto:Clusters.List", TypeKustoCluster, sub, st, scanID,
		client.NewListPager(nil),
		func(p armkusto.ClustersClientListResponse) []*armkusto.Cluster { return p.Value },
		func(c *armkusto.Cluster) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
