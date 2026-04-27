package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

func init() { registerService(serviceEntry{name: "azure:storage", fn: scanStorage}) }

// scanStorage discovers Azure storage accounts.
func scanStorage(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armstorage.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armstorage:NewAccountsClient: %w", err)
	}
	return azSimpleScan(ctx, "armstorage:Accounts.List", TypeStorageStorageAccount, sub, st, scanID,
		client.NewListPager(nil),
		func(p armstorage.AccountsClientListResponse) []*armstorage.Account { return p.Value },
		func(a *armstorage.Account) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
