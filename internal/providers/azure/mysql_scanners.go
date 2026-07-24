package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMySQLFlexibleServer, Service: "microsoft.dbformysql", Redact: []redact.Rule{{Path: "properties.administratorLoginPassword", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "azure:microsoft.dbformysql",
		fn:   scanMySQL,
	})
}

// scanMySQL discovers Azure Database for MySQL flexible servers.
func scanMySQL(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
