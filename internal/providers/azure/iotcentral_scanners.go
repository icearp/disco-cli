package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/iotcentral/armiotcentral"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.iotcentral",
		fn:   scanIoTCentral,
		emits: []coverage.TypeDecl{
			// Identity → MSI and private-endpoint edges resolved centrally; the
			// app ships scanner-only.
			{Service: "microsoft.iotcentral", DiscoType: TypeIoTCentralApp, Leaf: true},
		},
	})
}

// scanIoTCentral discovers Azure IoT Central applications.
func scanIoTCentral(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
