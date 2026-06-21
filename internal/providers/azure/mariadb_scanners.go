package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mariadb/armmariadb"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.dbformariadb",
		fn:   scanMariaDB,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.dbformariadb", DiscoType: TypeMariaDBServer, Leaf: true},
		},
	})
}

// scanMariaDB discovers Azure Database for MariaDB servers. The RP is
// deprecated (retiring) but the SDK still lists servers; the graceful-skip
// error classifier tolerates dead-RP responses at scan time.
func scanMariaDB(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmariadb.NewServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmariadb:NewServersClient: %w", err)
	}
	return azSimpleScan(ctx, "armmariadb:Servers.List", TypeMariaDBServer, sub, st, scanID,
		client.NewListPager(nil),
		func(p armmariadb.ServersClientListResponse) []*armmariadb.Server { return p.Value },
		func(r *armmariadb.Server) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
