package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datashare/armdatashare"
)

func init() {
	registerService(serviceEntry{
		name: "azure:datashare",
		fn:   scanDataShare,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; the account carries no
			// other in-scope reference, so this ships scanner-only.
			{Service: "microsoft.datashare", DiscoType: TypeDataShareAccount, Leaf: true},
		},
	})
}

// scanDataShare discovers Azure Data Share accounts.
func scanDataShare(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
