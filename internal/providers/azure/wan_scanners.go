package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

func init() { registerService(serviceEntry{name: "azure:wan", fn: scanWAN}) }

// scanWAN discovers Azure enterprise networking resources that have
// subscription-wide list APIs: ExpressRoute circuits, Virtual WANs, Virtual
// Hubs, VPN Sites, and vWAN VPN Gateways.
//
// Classic VirtualNetworkGateways (Microsoft.Network/virtualNetworkGateways —
// covers both ER gateways and classic VPN gateways) and ExpressRoute Gateways
// (Microsoft.Network/expressRouteGateways) are RG-scoped only — deferred
// until a per-RG fan-out scanner pattern lands.
func scanWAN(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	circuitsClient, err := armnetwork.NewExpressRouteCircuitsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewExpressRouteCircuitsClient: %w", err)
	}
	wansClient, err := armnetwork.NewVirtualWansClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewVirtualWansClient: %w", err)
	}
	hubsClient, err := armnetwork.NewVirtualHubsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewVirtualHubsClient: %w", err)
	}
	sitesClient, err := armnetwork.NewVPNSitesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewVPNSitesClient: %w", err)
	}
	gwClient, err := armnetwork.NewVPNGatewaysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewVPNGatewaysClient: %w", err)
	}

	phases := []func() (int, int, error){
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:ExpressRouteCircuits.ListAll", TypeNetworkExpressRouteCircuit, sub, st, scanID,
				circuitsClient.NewListAllPager(nil),
				func(p armnetwork.ExpressRouteCircuitsClientListAllResponse) []*armnetwork.ExpressRouteCircuit {
					return p.Value
				},
				ercToBase)
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:VirtualWans.List", TypeNetworkVirtualWAN, sub, st, scanID,
				wansClient.NewListPager(nil),
				func(p armnetwork.VirtualWansClientListResponse) []*armnetwork.VirtualWAN { return p.Value },
				vwanToBase)
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:VirtualHubs.List", TypeNetworkVirtualHub, sub, st, scanID,
				hubsClient.NewListPager(nil),
				func(p armnetwork.VirtualHubsClientListResponse) []*armnetwork.VirtualHub { return p.Value },
				vhubToBase)
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:VPNSites.List", TypeNetworkVPNSite, sub, st, scanID,
				sitesClient.NewListPager(nil),
				func(p armnetwork.VPNSitesClientListResponse) []*armnetwork.VPNSite { return p.Value },
				vpnSiteToBase)
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:VPNGateways.List", TypeNetworkVPNGateway, sub, st, scanID,
				gwClient.NewListPager(nil),
				func(p armnetwork.VPNGatewaysClientListResponse) []*armnetwork.VPNGateway { return p.Value },
				vpnGatewayToBase)
		},
	}

	for _, fn := range phases {
		t, i, err := fn()
		total += t
		inserted += i
		if err != nil {
			return total, inserted, err
		}
	}
	return total, inserted, nil
}

func ercToBase(e *armnetwork.ExpressRouteCircuit) azTrackedBase {
	return azTrackedBase{id: sv(e.ID), name: sv(e.Name), location: sv(e.Location), tags: e.Tags, full: e}
}

func vwanToBase(v *armnetwork.VirtualWAN) azTrackedBase {
	return azTrackedBase{id: sv(v.ID), name: sv(v.Name), location: sv(v.Location), tags: v.Tags, full: v}
}

func vhubToBase(h *armnetwork.VirtualHub) azTrackedBase {
	return azTrackedBase{id: sv(h.ID), name: sv(h.Name), location: sv(h.Location), tags: h.Tags, full: h}
}

func vpnSiteToBase(s *armnetwork.VPNSite) azTrackedBase {
	return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
}

func vpnGatewayToBase(g *armnetwork.VPNGateway) azTrackedBase {
	return azTrackedBase{id: sv(g.ID), name: sv(g.Name), location: sv(g.Location), tags: g.Tags, full: g}
}
