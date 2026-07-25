package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/kusto/armkusto"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeKustoCluster, Service: "microsoft.kusto"})
	registerService(serviceEntry{
		name: "azure:microsoft.kusto",
		fn:   scanKusto,
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
