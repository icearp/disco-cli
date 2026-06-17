package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridcontainerservice/armhybridcontainerservice"
)

func init() {
	registerService(serviceEntry{
		name: "azure:hybridcontainerservice",
		fn:   scanHybridContainerService,
		emits: []coverage.TypeDecl{
			// Custom-location edge wired centrally; the logical network ships
			// scanner-only.
			{Service: "microsoft.hybridcontainerservice", DiscoType: TypeHybridContainerVirtualNetwork, Leaf: true},
		},
	})
}

// scanHybridContainerService discovers AKS-on-Arc (hybrid) logical networks.
func scanHybridContainerService(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
