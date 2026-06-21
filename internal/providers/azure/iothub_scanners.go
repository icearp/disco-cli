package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/iothub/armiothub"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.devices",
		fn:   scanDevicesNamespace,
		emits: []coverage.TypeDecl{
			// Routing endpoints reference Event Hubs / Service Bus / Storage by
			// connection string, not ARM ID; identity edges resolve centrally.
			{Service: "microsoft.devices", DiscoType: TypeIoTHub, Leaf: true},
		},
	})
}

// scanIoTHub discovers Azure IoT Hubs.
func scanIoTHub(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armiothub.NewResourceClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armiothub:NewResourceClient: %w", err)
	}
	return azSimpleScan(ctx, "armiothub:Resource.ListBySubscription", TypeIoTHub, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armiothub.ResourceClientListBySubscriptionResponse) []*armiothub.Description { return p.Value },
		func(d *armiothub.Description) azTrackedBase {
			return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
		})
}

// scanDevicesNamespace runs every Microsoft.devices scanner phase concurrently. The
// devices ARM namespace spans several disco scanners merged under one
// serviceEntry so the service name aligns to the namespace.
func scanDevicesNamespace(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanIoTHub(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanDeviceProvisioningServices(ctx, sub, cred, st, scanID) },
	)
}
