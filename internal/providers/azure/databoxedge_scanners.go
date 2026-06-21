package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databoxedge/armdataboxedge"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.databoxedge",
		fn:   scanDataBoxEdge,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; the device carries no
			// other in-scope reference, so this ships scanner-only.
			{Service: "microsoft.databoxedge", DiscoType: TypeDataBoxEdgeDevice, Leaf: true},
		},
	})
}

// scanDataBoxEdge discovers Azure Data Box Edge / Stack Edge devices.
func scanDataBoxEdge(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdataboxedge.NewDevicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdataboxedge:NewDevicesClient: %w", err)
	}
	return azSimpleScan(ctx, "armdataboxedge:Devices.ListBySubscription", TypeDataBoxEdgeDevice, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdataboxedge.DevicesClientListBySubscriptionResponse) []*armdataboxedge.Device {
			return p.Value
		},
		func(d *armdataboxedge.Device) azTrackedBase {
			return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
		})
}
