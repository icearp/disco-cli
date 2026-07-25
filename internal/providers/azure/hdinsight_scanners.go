package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hdinsight/armhdinsight"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeHDInsightCluster, Service: "microsoft.hdinsight", Redact: []redact.Rule{{Path: "properties.computeProfile.roles[*].osProfile.linuxOperatingSystemProfile.password", Mode: redact.RedactScalar}, {Path: "properties.securityProfile.domainUserPassword", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "azure:microsoft.hdinsight",
		fn:   scanHDInsight,
	})
}

// scanHDInsight discovers Azure HDInsight clusters.
func scanHDInsight(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
