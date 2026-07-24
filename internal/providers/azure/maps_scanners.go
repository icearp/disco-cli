package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/maps/armmaps"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMapsAccount, Service: "microsoft.maps", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.maps",
		fn:   scanMaps,
	})
}

// scanMaps discovers Azure Maps accounts.
func scanMaps(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmaps.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmaps:NewAccountsClient: %w", err)
	}
	return azSimpleScan(ctx, "armmaps:Accounts.ListBySubscription", TypeMapsAccount, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armmaps.AccountsClientListBySubscriptionResponse) []*armmaps.Account { return p.Value },
		func(a *armmaps.Account) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
