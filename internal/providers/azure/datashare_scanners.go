package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datashare/armdatashare"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDataShareAccount, Service: "microsoft.datashare", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.datashare",
		fn:   scanDataShare,
	})
}

// scanDataShare discovers Azure Data Share accounts.
func scanDataShare(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdatashare.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdatashare:NewAccountsClient: %w", err)
	}
	return azSimpleScan(ctx, "armdatashare:Accounts.ListBySubscription", TypeDataShareAccount, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdatashare.AccountsClientListBySubscriptionResponse) []*armdatashare.Account { return p.Value },
		func(a *armdatashare.Account) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
