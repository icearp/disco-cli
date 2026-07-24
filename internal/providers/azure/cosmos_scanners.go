package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCosmosDatabaseAccount, Service: "microsoft.documentdb"})
	registerType(restype.Descriptor{Type: TypeCosmosCassandraCluster, Service: "microsoft.documentdb", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCosmosRestorableDatabaseAccount, Service: "microsoft.documentdb", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.documentdb",
		fn:   scanDocumentDBNamespace,
	})
}

// scanCosmos discovers Azure Cosmos DB database accounts, managed Cassandra
// clusters, and restorable database accounts. Per-API child resources
// (SQL/Mongo/Cassandra/Gremlin/Table databases + containers/graphs) are
// deferred — they explode in volume on multi-tenant accounts, and the account
// row alone already carries the security-relevant edges (CMEK, identity,
// network ACLs, private endpoints).
func scanCosmos(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcosmos.NewDatabaseAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcosmos:NewDatabaseAccountsClient: %w", err)
	}
	total, inserted, err = azSimpleScan(ctx, "armcosmos:DatabaseAccounts.List", TypeCosmosDatabaseAccount, sub, st, scanID,
		client.NewListPager(nil),
		func(p armcosmos.DatabaseAccountsClientListResponse) []*armcosmos.DatabaseAccountGetResults {
			return p.Value
		},
		func(a *armcosmos.DatabaseAccountGetResults) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
	if err != nil {
		return total, inserted, err
	}

	cassClient, err := armcosmos.NewCassandraClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armcosmos:NewCassandraClustersClient: %w", err)
	}
	ct, ci, err := azSimpleScan(ctx, "armcosmos:CassandraClusters.ListBySubscription", TypeCosmosCassandraCluster, sub, st, scanID,
		cassClient.NewListBySubscriptionPager(nil),
		func(p armcosmos.CassandraClustersClientListBySubscriptionResponse) []*armcosmos.ClusterResource {
			return p.Value
		},
		func(c *armcosmos.ClusterResource) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
	total += ct
	inserted += ci
	if err != nil {
		return total, inserted, err
	}

	// Restorable database accounts are a provider-managed backup view: Azure
	// auto-materialises one per Cosmos account and the user cannot delete it.
	restClient, err := armcosmos.NewRestorableDatabaseAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armcosmos:NewRestorableDatabaseAccountsClient: %w", err)
	}
	rt, ri, err := azSimpleScan(ctx, "armcosmos:RestorableDatabaseAccounts.List", TypeCosmosRestorableDatabaseAccount, sub, st, scanID,
		restClient.NewListPager(nil),
		func(p armcosmos.RestorableDatabaseAccountsClientListResponse) []*armcosmos.RestorableDatabaseAccountGetResult {
			return p.Value
		},
		func(r *armcosmos.RestorableDatabaseAccountGetResult) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), managed: true, full: r}
		})
	total += rt
	inserted += ri
	return total, inserted, err
}

// scanDocumentDBNamespace runs every Microsoft.documentdb scanner phase
// concurrently — the namespace spans several disco scanners merged under one
// serviceEntry so the service name aligns to the ARM namespace.
func scanDocumentDBNamespace(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanCosmos(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanMongoCluster(ctx, sub, cred, st, scanID) },
	)
}
