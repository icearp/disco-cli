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
	client, err := armstorage.NewAccountsClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armstorage:NewAccountsClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armstorage:Accounts.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armstorage:Accounts.List: %w", err)
		}
		var batch []*store.Resource
		for _, acct := range page.Value {
			if acct.ID == nil {
				continue
			}
			name := sv(acct.Name)
			location := sv(acct.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeStorageStorageAccount,
				NativeID:       sv(acct.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(acct),
				DiscoveredBy:   scanID,
			}
			if acct.Tags != nil {
				s := mustJSON(acct.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert storage accounts: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
