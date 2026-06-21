package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/purview/armpurview"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.purview",
		fn:   scanPurview,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.purview", DiscoType: TypePurviewAccount, Leaf: true},
		},
	})
}

// scanPurview discovers Microsoft Purview accounts.
func scanPurview(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armpurview.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpurview:NewAccountsClient: %w", err)
	}
	return azSimpleScan(ctx, "armpurview:Accounts.ListBySubscription", TypePurviewAccount, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armpurview.AccountsClientListBySubscriptionResponse) []*armpurview.Account { return p.Value },
		func(a *armpurview.Account) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
