package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers"
)

func init() { registerService(serviceEntry{name: "azure:mysql", fn: scanMySQL}) }

// scanMySQL discovers Azure Database for MySQL flexible servers. Single
// Server (deprecated tier) deferred — see postgresql_scanners.go rationale.
func scanMySQL(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmysqlflexibleservers.NewServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmysqlflexibleservers:NewServersClient: %w", err)
	}
	return azPageScan(ctx, "armmysqlflexibleservers:Servers.List", sub, st,
		client.NewListPager(nil),
		func(page armmysqlflexibleservers.ServersClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, r := range page.Value {
				if r == nil || r.ID == nil {
					continue
				}
				name, loc := sv(r.Name), sv(r.Location)
				nativeID := sv(r.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeMySQLFlexibleServer, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(r.Tags), AttributesJSON: mustJSON(r),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeMySQLFlexibleServer, nativeID))
				}
			}
			return batch, pairs
		})
}
