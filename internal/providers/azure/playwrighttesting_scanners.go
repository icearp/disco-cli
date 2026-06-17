package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/playwrighttesting/armplaywrighttesting"
)

func init() {
	registerService(serviceEntry{
		name: "azure:playwright",
		fn:   scanPlaywrightTesting,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.azureplaywrightservice", DiscoType: TypePlaywrightAccount, Leaf: true},
		},
	})
}

// scanPlaywrightTesting discovers Azure Playwright Testing (workspaces) accounts.
func scanPlaywrightTesting(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armplaywrighttesting.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armplaywrighttesting:NewAccountsClient: %w", err)
	}
	return azSimpleScan(ctx, "armplaywrighttesting:Accounts.ListBySubscription", TypePlaywrightAccount, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armplaywrighttesting.AccountsClientListBySubscriptionResponse) []*armplaywrighttesting.Account {
			return p.Value
		},
		func(a *armplaywrighttesting.Account) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
