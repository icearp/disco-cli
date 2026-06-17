package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/netapp/armnetapp/v7"
)

func init() {
	registerService(serviceEntry{
		name: "azure:netapp",
		fn:   scanNetApp,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.netapp", DiscoType: TypeNetAppAccount},
		},
	})
}

// scanNetApp discovers Azure NetApp Files accounts.
func scanNetApp(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetapp.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetapp:NewAccountsClient: %w", err)
	}
	return azSimpleScan(ctx, "armnetapp:Accounts.ListBySubscription", TypeNetAppAccount, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armnetapp.AccountsClientListBySubscriptionResponse) []*armnetapp.Account { return p.Value },
		func(a *armnetapp.Account) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
