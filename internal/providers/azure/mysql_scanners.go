package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers"
)

func init() {
	registerService(serviceEntry{
		name: "azure:mysql",
		fn:   scanMySQL,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.dbformysql", DiscoType: TypeMySQLFlexibleServer},
		},
	})
}

// scanMySQL discovers Azure Database for MySQL flexible servers. Single
// Server (deprecated tier) deferred — see postgresql_scanners.go rationale.
func scanMySQL(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmysqlflexibleservers.NewServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmysqlflexibleservers:NewServersClient: %w", err)
	}
	return azSimpleScan(ctx, "armmysqlflexibleservers:Servers.List", TypeMySQLFlexibleServer, sub, st, scanID,
		client.NewListPager(nil),
		func(p armmysqlflexibleservers.ServersClientListResponse) []*armmysqlflexibleservers.Server {
			return p.Value
		},
		func(r *armmysqlflexibleservers.Server) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
