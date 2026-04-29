package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
)

func init() {
	registerService(serviceEntry{
		name: "azure:postgresql",
		fn:   scanPostgreSQL,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.dbforpostgresql", DiscoType: TypePostgreSQLFlexibleServer},
		},
	})
}

// scanPostgreSQL discovers Azure Database for PostgreSQL flexible servers.
// Single Server (deprecated tier) deferred — Microsoft recommends migration
// to Flexible Server, and Single Server uses a separate `armpostgresql` SDK.
// Databases, configurations, firewall rules, backup policies deferred — narrow
// cross-service edge value relative to volume.
func scanPostgreSQL(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armpostgresqlflexibleservers.NewServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpostgresqlflexibleservers:NewServersClient: %w", err)
	}
	return azSimpleScan(ctx, "armpostgresqlflexibleservers:Servers.List", TypePostgreSQLFlexibleServer, sub, st, scanID,
		client.NewListPager(nil),
		func(p armpostgresqlflexibleservers.ServersClientListResponse) []*armpostgresqlflexibleservers.Server {
			return p.Value
		},
		func(r *armpostgresqlflexibleservers.Server) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
