package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/playwrighttesting/armplaywrighttesting"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePlaywrightAccount, Service: "microsoft.azureplaywrightservice", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.azureplaywrightservice",
		fn:   scanPlaywrightTesting,
	})
}

// scanPlaywrightTesting discovers Azure Playwright Testing (workspaces) accounts.
func scanPlaywrightTesting(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
