package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysql"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.dbformysql",
		fn:   scanMySQL,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.dbformysql", DiscoType: TypeMySQLFlexibleServer},
			{Service: "microsoft.dbformysql", DiscoType: TypeMySQLSingleServer, Leaf: true},
		},
	})
}

// scanMySQL discovers Azure Database for MySQL flexible servers and the
// deprecated Single Server tier. Single Server's RP is being retired; the
// graceful-skip error classifier tolerates dead-RP responses at scan time.
func scanMySQL(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
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
		},
		func() (int, int, error) {
			singleClient, err := armmysql.NewServersClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armmysql:NewServersClient: %w", err)
			}
			return azSimpleScan(ctx, "armmysql:Servers.List", TypeMySQLSingleServer, sub, st, scanID,
				singleClient.NewListPager(nil),
				func(p armmysql.ServersClientListResponse) []*armmysql.Server { return p.Value },
				func(r *armmysql.Server) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
