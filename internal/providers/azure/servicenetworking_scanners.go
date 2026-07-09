package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicenetworking/armservicenetworking"
)

func init() {
	registerType(restype.Descriptor{Type: TypeServiceNetworkingTrafficController, Service: "microsoft.servicenetworking", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.servicenetworking",
		fn:   scanServiceNetworking,
	})
}

// scanServiceNetworking discovers Application Gateway for Containers traffic controllers.
func scanServiceNetworking(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armservicenetworking.NewTrafficControllerInterfaceClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armservicenetworking:NewTrafficControllerInterfaceClient: %w", err)
	}
	return azSimpleScan(ctx, "armservicenetworking:TrafficControllers.ListBySubscription", TypeServiceNetworkingTrafficController, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armservicenetworking.TrafficControllerInterfaceClientListBySubscriptionResponse) []*armservicenetworking.TrafficController {
			return p.Value
		},
		func(t *armservicenetworking.TrafficController) azTrackedBase {
			return azTrackedBase{id: sv(t.ID), name: sv(t.Name), location: sv(t.Location), tags: t.Tags, full: t}
		})
}
