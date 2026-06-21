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
		name: "azure:microsoft.peering",
		fn:   scanPeering,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.peering", DiscoType: TypePeeringPeering, Leaf: true},
			{Service: "microsoft.peering", DiscoType: TypePeeringPeerAsn, Leaf: true},
			{Service: "microsoft.peering", DiscoType: TypePeeringPeeringService, Leaf: true},
		},
	})
}

// scanPeering discovers Azure Peerings (direct/exchange), peer-ASN
// registrations, and peering services. PeerAsns are tenant-level proxy
// resources (no location/tags); peerings and services are tracked.
func scanPeering(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	peerings, err := armpeering.NewPeeringsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpeering:NewPeeringsClient: %w", err)
	}
	peerAsns, err := armpeering.NewPeerAsnsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpeering:NewPeerAsnsClient: %w", err)
	}
	svcs, err := armpeering.NewServicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpeering:NewServicesClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armpeering:Peerings.ListBySubscription", TypePeeringPeering, sub, st, scanID,
				peerings.NewListBySubscriptionPager(nil),
				func(p armpeering.PeeringsClientListBySubscriptionResponse) []*armpeering.Peering { return p.Value },
				func(r *armpeering.Peering) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armpeering:PeerAsns.ListBySubscription", TypePeeringPeerAsn, sub, st, scanID,
				peerAsns.NewListBySubscriptionPager(nil),
				func(p armpeering.PeerAsnsClientListBySubscriptionResponse) []*armpeering.PeerAsn { return p.Value },
				func(r *armpeering.PeerAsn) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armpeering:Services.ListBySubscription", TypePeeringPeeringService, sub, st, scanID,
				svcs.NewListBySubscriptionPager(nil),
				func(p armpeering.ServicesClientListBySubscriptionResponse) []*armpeering.Service { return p.Value },
				func(r *armpeering.Service) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
