package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mongocluster/armmongocluster"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMongoCluster, Service: "microsoft.documentdb", Redact: []redact.Rule{{Path: "properties.connectionString", Mode: redact.RedactScalar}, {Path: "properties.administrator.password", Mode: redact.RedactScalar}}})
}

// scanMongoCluster discovers Azure Cosmos DB for MongoDB (vCore) clusters.
func scanMongoCluster(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmongocluster.NewMongoClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmongocluster:NewMongoClustersClient: %w", err)
	}
	return azSimpleScan(ctx, "armmongocluster:MongoClusters.List", TypeMongoCluster, sub, st, scanID,
		client.NewListPager(nil),
		func(p armmongocluster.MongoClustersClientListResponse) []*armmongocluster.MongoCluster {
			return p.Value
		},
		func(c *armmongocluster.MongoCluster) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
