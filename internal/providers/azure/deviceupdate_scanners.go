package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/deviceupdate/armdeviceupdate"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDeviceUpdateAccount, Service: "microsoft.deviceupdate", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.deviceupdate",
		fn:   scanDeviceUpdate,
	})
}

// scanDeviceUpdate discovers Device Update for IoT Hub accounts.
func scanDeviceUpdate(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdeviceupdate.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdeviceupdate:NewAccountsClient: %w", err)
	}
	return azSimpleScan(ctx, "armdeviceupdate:Accounts.ListBySubscription", TypeDeviceUpdateAccount, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdeviceupdate.AccountsClientListBySubscriptionResponse) []*armdeviceupdate.Account {
			return p.Value
		},
		func(a *armdeviceupdate.Account) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
