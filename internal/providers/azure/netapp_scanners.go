package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/netapp/armnetapp/v7"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNetAppAccount, Service: "microsoft.netapp", Redact: []redact.Rule{{Path: "properties.activeDirectories[*].password", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "azure:microsoft.netapp",
		fn:   scanNetApp,
	})
}

// scanNetApp discovers Azure NetApp Files accounts.
func scanNetApp(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
