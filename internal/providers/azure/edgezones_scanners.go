package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/edgezones/armedgezones"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEdgeZonesExtendedZone, Service: "microsoft.edgezones", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.edgezones",
		fn:   scanEdgeZones,
	})
}

// scanEdgeZones discovers the Azure Extended Zones registered for the subscription.
func scanEdgeZones(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armedgezones.NewExtendedZonesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armedgezones:NewExtendedZonesClient: %w", err)
	}
	return azSimpleScan(ctx, "armedgezones:ExtendedZones.ListBySubscription", TypeEdgeZonesExtendedZone, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armedgezones.ExtendedZonesClientListBySubscriptionResponse) []*armedgezones.ExtendedZone {
			return p.Value
		},
		func(z *armedgezones.ExtendedZone) azTrackedBase {
			return azTrackedBase{id: sv(z.ID), name: sv(z.Name), full: z}
		})
}
