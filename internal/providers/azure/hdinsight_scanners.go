package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hdinsight/armhdinsight"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.hdinsight",
		fn:   scanHDInsight,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.hdinsight", DiscoType: TypeHDInsightCluster},
		},
	})
}

// scanHDInsight discovers Azure HDInsight clusters.
func scanHDInsight(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armhdinsight.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhdinsight:NewClustersClient: %w", err)
	}
	return azSimpleScan(ctx, "armhdinsight:Clusters.List", TypeHDInsightCluster, sub, st, scanID,
		client.NewListPager(nil),
		func(p armhdinsight.ClustersClientListResponse) []*armhdinsight.Cluster { return p.Value },
		func(c *armhdinsight.Cluster) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
