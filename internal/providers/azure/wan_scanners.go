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
	type phase struct {
		name string
		fn   func() (int, int, error)
	}

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

	phases := []phase{
		{name: "armnetwork:ExpressRouteCircuits.ListAll", fn: func() (int, int, error) {
			return azPageScan(ctx, "armnetwork:ExpressRouteCircuits.ListAll", sub, st,
				circuitsClient.NewListAllPager(nil),
				func(page armnetwork.ExpressRouteCircuitsClientListAllResponse) ([]*store.Resource, [][2]string) {
					return wanRows(sub, scanID, page.Value, TypeNetworkExpressRouteCircuit, ercToBase)
				})
		}},
		{name: "armnetwork:VirtualWans.List", fn: func() (int, int, error) {
			return azPageScan(ctx, "armnetwork:VirtualWans.List", sub, st,
				wansClient.NewListPager(nil),
				func(page armnetwork.VirtualWansClientListResponse) ([]*store.Resource, [][2]string) {
					return wanRows(sub, scanID, page.Value, TypeNetworkVirtualWAN, vwanToBase)
				})
		}},
		{name: "armnetwork:VirtualHubs.List", fn: func() (int, int, error) {
			return azPageScan(ctx, "armnetwork:VirtualHubs.List", sub, st,
				hubsClient.NewListPager(nil),
				func(page armnetwork.VirtualHubsClientListResponse) ([]*store.Resource, [][2]string) {
					return wanRows(sub, scanID, page.Value, TypeNetworkVirtualHub, vhubToBase)
				})
		}},
		{name: "armnetwork:VPNSites.List", fn: func() (int, int, error) {
			return azPageScan(ctx, "armnetwork:VPNSites.List", sub, st,
				sitesClient.NewListPager(nil),
				func(page armnetwork.VPNSitesClientListResponse) ([]*store.Resource, [][2]string) {
					return wanRows(sub, scanID, page.Value, TypeNetworkVPNSite, vpnSiteToBase)
				})
		}},
		{name: "armnetwork:VPNGateways.List", fn: func() (int, int, error) {
			return azPageScan(ctx, "armnetwork:VPNGateways.List", sub, st,
				gwClient.NewListPager(nil),
				func(page armnetwork.VPNGatewaysClientListResponse) ([]*store.Resource, [][2]string) {
					return wanRows(sub, scanID, page.Value, TypeNetworkVPNGateway, vpnGatewayToBase)
				})
		}},
	}

	for _, p := range phases {
		t, i, err := p.fn()
		total += t
		inserted += i
		if err != nil {
			return total, inserted, err
		}
	}
	return total, inserted, nil
}

// wanResourceBase is the shared shape every armnetwork WAN type satisfies:
// ID, Name, Location, Tags. Each type-specific extractor below returns these
// four fields so wanRows can build a generic store.Resource batch.
type wanResourceBase struct {
	id, name, location string
	tags               map[string]*string
	full               any
}

func wanRows[T any](sub *subscription, scanID string, items []*T, rtype string, extract func(*T) wanResourceBase) ([]*store.Resource, [][2]string) {
	var batch []*store.Resource
	var pairs [][2]string
	for _, item := range items {
		if item == nil {
			continue
		}
		b := extract(item)
		if b.id == "" {
			continue
		}
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
			Type: rtype, NativeID: b.id,
			Name: &b.name, Region: &b.location,
			TagsJSON: azTagsJSON(b.tags), AttributesJSON: mustJSON(b.full),
			DiscoveredBy: scanID,
		})
		if rgFromID(b.id) != "" {
			pairs = append(pairs, rgHierarchyPair(sub, rtype, b.id))
		}
	}
	return batch, pairs
}

func ercToBase(e *armnetwork.ExpressRouteCircuit) wanResourceBase {
	return wanResourceBase{id: sv(e.ID), name: sv(e.Name), location: sv(e.Location), tags: e.Tags, full: e}
}

func vwanToBase(v *armnetwork.VirtualWAN) wanResourceBase {
	return wanResourceBase{id: sv(v.ID), name: sv(v.Name), location: sv(v.Location), tags: v.Tags, full: v}
}

func vhubToBase(h *armnetwork.VirtualHub) wanResourceBase {
	return wanResourceBase{id: sv(h.ID), name: sv(h.Name), location: sv(h.Location), tags: h.Tags, full: h}
}

func vpnSiteToBase(s *armnetwork.VPNSite) wanResourceBase {
	return wanResourceBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
}

func vpnGatewayToBase(g *armnetwork.VPNGateway) wanResourceBase {
	return wanResourceBase{id: sv(g.ID), name: sv(g.Name), location: sv(g.Location), tags: g.Tags, full: g}
}
