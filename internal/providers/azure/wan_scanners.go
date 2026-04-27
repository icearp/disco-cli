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
// subscription-wide list APIs (ExpressRoute circuits, Virtual WANs, Virtual
// Hubs, VPN Sites, vWAN VPN Gateways) plus the two classic gateway types
// that require per-RG fan-out (VirtualNetworkGateways — covers both classic
// ER gateways and classic VPN gateways — and ExpressRouteGateways).
// VirtualNetworkGateways landed via the new `azRGFanoutScan` helper;
// ExpressRouteGateways uses the SDK's single `ListBySubscription` call
// despite the type only being settable per-RG.
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
	vngClient, err := armnetwork.NewVirtualNetworkGatewaysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewVirtualNetworkGatewaysClient: %w", err)
	}
	ergClient, err := armnetwork.NewExpressRouteGatewaysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewExpressRouteGatewaysClient: %w", err)
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
		// Classic VirtualNetworkGateways — RG-scoped only, fan out via
		// azRGFanoutScan (R3.23 helper).
		func() (int, int, error) {
			return azRGFanoutScan(ctx, "armnetwork:VirtualNetworkGateways.List", TypeNetworkVirtualNetworkGW, sub, cred, st, scanID,
				func(rg string) azPager[armnetwork.VirtualNetworkGatewaysClientListResponse] {
					return vngClient.NewListPager(rg, nil)
				},
				func(p armnetwork.VirtualNetworkGatewaysClientListResponse) []*armnetwork.VirtualNetworkGateway {
					return p.Value
				},
				vngToBase)
		},
		// ExpressRouteGateways — SDK exposes ListBySubscription (single call,
		// no pager). Wrap in a one-shot adapter to keep phase shape uniform.
		func() (int, int, error) {
			return scanExpressRouteGateways(ctx, sub, ergClient, st, scanID)
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

func vngToBase(g *armnetwork.VirtualNetworkGateway) azTrackedBase {
	return azTrackedBase{id: sv(g.ID), name: sv(g.Name), location: sv(g.Location), tags: g.Tags, full: g}
}

func ergToBase(g *armnetwork.ExpressRouteGateway) azTrackedBase {
	return azTrackedBase{id: sv(g.ID), name: sv(g.Name), location: sv(g.Location), tags: g.Tags, full: g}
}

// scanExpressRouteGateways adapts the single-call ListBySubscription API
// into the standard scanner-phase shape: enumerate, build batch + RG pairs
// via azTrackedRows, upsert.
func scanExpressRouteGateways(ctx context.Context, sub *subscription, client *armnetwork.ExpressRouteGatewaysClient, st *store.Store, scanID string) (total, inserted int, err error) {
	resp, err := client.ListBySubscription(ctx, nil)
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "armnetwork:ExpressRouteGateways.ListBySubscription", sub.ID, err)
		}
		return 0, 0, fmt.Errorf("armnetwork:ExpressRouteGateways.ListBySubscription: %w", err)
	}
	batch, pairs := azTrackedRows(sub, scanID, TypeNetworkExpressRouteGateway, resp.Value, ergToBase)
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert ExpressRouteGateways: %w", err)
	}
	if len(pairs) > 0 {
		if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
			return len(batch), n, fmt.Errorf("closure ExpressRouteGateways: %w", err)
		}
	}
	return len(batch), n, nil
}
