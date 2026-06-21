package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/deviceupdate/armdeviceupdate"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.deviceupdate",
		fn:   scanDeviceUpdate,
		emits: []coverage.TypeDecl{
			// Identity → MSI and private-endpoint edges resolved centrally; the
			// account ships scanner-only.
			{Service: "microsoft.deviceupdate", DiscoType: TypeDeviceUpdateAccount, Leaf: true},
		},
	})
}

// scanDeviceUpdate discovers Device Update for IoT Hub accounts.
func scanDeviceUpdate(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
