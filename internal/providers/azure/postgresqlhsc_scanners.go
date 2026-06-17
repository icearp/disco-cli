package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresqlhsc/armpostgresqlhsc"
)

func init() {
	registerService(serviceEntry{
		name: "azure:postgresqlhsc",
		fn:   scanPostgreSQLHSC,
		emits: []coverage.TypeDecl{
			// resolvePostgreSQLHSCRelationships wires the CMK (Key Vault) edge
			// below.
			{Service: "microsoft.dbforpostgresql", DiscoType: TypePostgreSQLServerGroupV2},
		},
	})
}

// scanPostgreSQLHSC discovers Azure Cosmos DB for PostgreSQL (Citus) clusters.
func scanPostgreSQLHSC(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
