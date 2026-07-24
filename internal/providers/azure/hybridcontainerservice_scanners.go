package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridcontainerservice/armhybridcontainerservice"
)

func init() {
	registerType(restype.Descriptor{Type: TypeHybridContainerVirtualNetwork, Service: "microsoft.hybridcontainerservice", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.hybridcontainerservice",
		fn:   scanHybridContainerService,
	})
}

// scanHybridContainerService discovers AKS-on-Arc (hybrid) logical networks.
func scanHybridContainerService(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armhybridcontainerservice.NewVirtualNetworksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhybridcontainerservice:NewVirtualNetworksClient: %w", err)
	}
	return azSimpleScan(ctx, "armhybridcontainerservice:VirtualNetworks.ListBySubscription", TypeHybridContainerVirtualNetwork, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armhybridcontainerservice.VirtualNetworksClientListBySubscriptionResponse) []*armhybridcontainerservice.VirtualNetwork {
			return p.Value
		},
		func(v *armhybridcontainerservice.VirtualNetwork) azTrackedBase {
			return azTrackedBase{id: sv(v.ID), name: sv(v.Name), location: sv(v.Location), tags: v.Tags, full: v}
		})
}
