package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
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
	return azPageScan(ctx, "armstorage:Accounts.List", sub, st,
		client.NewListPager(nil),
		func(page armstorage.AccountsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, acct := range page.Value {
				if acct.ID == nil {
					continue
				}
				name, loc := sv(acct.Name), sv(acct.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeStorageStorageAccount, NativeID: sv(acct.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(acct.Tags), AttributesJSON: mustJSON(acct),
					DiscoveredBy: scanID,
				})
			}
			return batch, nil
		})
}
