package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/iotfirmwaredefense/armiotfirmwaredefense"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTFirmwareDefenseWorkspace, Service: "microsoft.iotfirmwaredefense", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.iotfirmwaredefense",
		fn:   scanIoTFirmwareDefense,
	})
}

// scanIoTFirmwareDefense discovers IoT Firmware Defense (Defender for IoT firmware analysis) workspaces.
func scanIoTFirmwareDefense(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armiotfirmwaredefense.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armiotfirmwaredefense:NewWorkspacesClient: %w", err)
	}
	return azSimpleScan(ctx, "armiotfirmwaredefense:Workspaces.ListBySubscription", TypeIoTFirmwareDefenseWorkspace, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armiotfirmwaredefense.WorkspacesClientListBySubscriptionResponse) []*armiotfirmwaredefense.Workspace {
			return p.Value
		},
		func(w *armiotfirmwaredefense.Workspace) azTrackedBase {
			return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
		})
}
