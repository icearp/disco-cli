package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos"
)

func init() { registerService(serviceEntry{name: "azure:cosmos", fn: scanCosmos}) }

// scanCosmos discovers Azure Cosmos DB database accounts. Per-API child
// resources (SQL/Mongo/Cassandra/Gremlin/Table databases + containers/graphs)
// are deferred — they explode in volume on multi-tenant accounts and the
// account row alone carries the security-relevant edges (CMEK, identity,
// network ACLs, private endpoints).
func scanCosmos(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcosmos.NewDatabaseAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcosmos:NewDatabaseAccountsClient: %w", err)
	}
	return azPageScan(ctx, "armcosmos:DatabaseAccounts.List", sub, st,
		client.NewListPager(nil),
		func(page armcosmos.DatabaseAccountsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, a := range page.Value {
				if a == nil || a.ID == nil {
					continue
				}
				name, loc := sv(a.Name), sv(a.Location)
				nativeID := sv(a.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeCosmosDatabaseAccount, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(a.Tags), AttributesJSON: mustJSON(a),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeCosmosDatabaseAccount, nativeID))
				}
			}
			return batch, pairs
		})
}
