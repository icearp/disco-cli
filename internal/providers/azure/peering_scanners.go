package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/peering/armpeering"
)

func init() {
	registerService(serviceEntry{
		name: "azure:peering",
		fn:   scanPeering,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.peering", DiscoType: TypePeeringPeering, Leaf: true},
		},
	})
}

// scanPeering discovers Azure Peerings (direct / exchange).
func scanPeering(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armpeering.NewPeeringsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpeering:NewPeeringsClient: %w", err)
	}
	return azSimpleScan(ctx, "armpeering:Peerings.ListBySubscription", TypePeeringPeering, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armpeering.PeeringsClientListBySubscriptionResponse) []*armpeering.Peering { return p.Value },
		func(p *armpeering.Peering) azTrackedBase {
			return azTrackedBase{id: sv(p.ID), name: sv(p.Name), location: sv(p.Location), tags: p.Tags, full: p}
		})
}
