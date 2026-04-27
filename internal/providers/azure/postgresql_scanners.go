package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
)

func init() { registerService(serviceEntry{name: "azure:postgresql", fn: scanPostgreSQL}) }

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
	return azPageScan(ctx, "armpostgresqlflexibleservers:Servers.List", sub, st,
		client.NewListPager(nil),
		func(page armpostgresqlflexibleservers.ServersClientListResponse) ([]*store.Resource, [][2]string) {
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
					Type: TypePostgreSQLFlexibleServer, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(r.Tags), AttributesJSON: mustJSON(r),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypePostgreSQLFlexibleServer, nativeID))
				}
			}
			return batch, pairs
		})
}
