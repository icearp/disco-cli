package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridnetwork/armhybridnetwork"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.hybridnetwork",
		fn:   scanHybridNetwork,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.hybridnetwork", DiscoType: TypeHybridNetworkFunction, Leaf: true},
			{Service: "microsoft.hybridnetwork", DiscoType: TypeHybridNetworkDevice, Leaf: true},
		},
	})
}

// scanHybridNetwork discovers Azure Operator Service Manager network functions
// and devices, both sub-wide via armhybridnetwork.
func scanHybridNetwork(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	nf, err := armhybridnetwork.NewNetworkFunctionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhybridnetwork:NewNetworkFunctionsClient: %w", err)
	}
	dev, err := armhybridnetwork.NewDevicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhybridnetwork:NewDevicesClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armhybridnetwork:NetworkFunctions.ListBySubscription", TypeHybridNetworkFunction, sub, st, scanID,
				nf.NewListBySubscriptionPager(nil),
				func(p armhybridnetwork.NetworkFunctionsClientListBySubscriptionResponse) []*armhybridnetwork.NetworkFunction {
					return p.Value
				},
				func(f *armhybridnetwork.NetworkFunction) azTrackedBase {
					return azTrackedBase{id: sv(f.ID), name: sv(f.Name), location: sv(f.Location), tags: f.Tags, full: f}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armhybridnetwork:Devices.ListBySubscription", TypeHybridNetworkDevice, sub, st, scanID,
				dev.NewListBySubscriptionPager(nil),
				func(p armhybridnetwork.DevicesClientListBySubscriptionResponse) []*armhybridnetwork.Device {
					return p.Value
				},
				func(d *armhybridnetwork.Device) azTrackedBase {
					return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
				})
		},
	)
}
