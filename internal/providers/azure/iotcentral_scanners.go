package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/iotcentral/armiotcentral"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTCentralApp, Service: "microsoft.iotcentral", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.iotcentral",
		fn:   scanIoTCentral,
	})
}

// scanIoTCentral discovers Azure IoT Central applications.
func scanIoTCentral(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armiotcentral.NewAppsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armiotcentral:NewAppsClient: %w", err)
	}
	return azSimpleScan(ctx, "armiotcentral:Apps.ListBySubscription", TypeIoTCentralApp, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armiotcentral.AppsClientListBySubscriptionResponse) []*armiotcentral.App { return p.Value },
		func(a *armiotcentral.App) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
