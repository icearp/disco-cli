package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresqlhsc/armpostgresqlhsc"
)

func init() {
	registerType(restype.Descriptor{Type: TypePostgreSQLServerGroupV2, Service: "microsoft.dbforpostgresql", Redact: []redact.Rule{{Path: "properties.administratorLoginPassword", Mode: redact.RedactScalar}}})
}

// scanPostgreSQLHSC discovers Azure Cosmos DB for PostgreSQL (Citus) clusters.
func scanPostgreSQLHSC(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armpostgresqlhsc.NewClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpostgresqlhsc:NewClustersClient: %w", err)
	}
	return azSimpleScan(ctx, "armpostgresqlhsc:Clusters.List", TypePostgreSQLServerGroupV2, sub, st, scanID,
		client.NewListPager(nil),
		func(p armpostgresqlhsc.ClustersClientListResponse) []*armpostgresqlhsc.Cluster { return p.Value },
		func(c *armpostgresqlhsc.Cluster) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
