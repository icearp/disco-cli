package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos"
)

func init() {
	registerService(serviceEntry{
		name: "azure:cosmos",
		fn:   scanCosmos,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.documentdb", DiscoType: TypeCosmosDatabaseAccount},
		},
	})
}

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
	return azSimpleScan(ctx, "armcosmos:DatabaseAccounts.List", TypeCosmosDatabaseAccount, sub, st, scanID,
		client.NewListPager(nil),
		func(p armcosmos.DatabaseAccountsClientListResponse) []*armcosmos.DatabaseAccountGetResults {
			return p.Value
		},
		func(a *armcosmos.DatabaseAccountGetResults) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
