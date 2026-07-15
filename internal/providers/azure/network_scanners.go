package azure

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNetworkVirtualNetwork, Service: "microsoft.network"})
	registerType(restype.Descriptor{Type: TypeNetworkSubnet, Service: "microsoft.network", Uncatalogued: true})
	registerType(restype.Descriptor{Type: TypeNetworkSecurityGroup, Service: "microsoft.network"})
	registerType(restype.Descriptor{Type: TypeNetworkPublicIPAddress, Service: "microsoft.network"})
	registerType(restype.Descriptor{Type: TypeNetworkApplicationGateway, Service: "microsoft.network"})
	registerType(restype.Descriptor{Type: TypeNetworkExpressRouteCircuit, Service: "microsoft.network"})
	registerType(restype.Descriptor{Type: TypeNetworkVirtualWAN, Service: "microsoft.network"})
	registerType(restype.Descriptor{Type: TypeNetworkVirtualHub, Service: "microsoft.network"})
	registerType(restype.Descriptor{Type: TypeNetworkVPNGateway, Service: "microsoft.network"})
	registerType(restype.Descriptor{Type: TypeNetworkVPNSite, Service: "microsoft.network"})
	registerType(restype.Descriptor{Type: TypeNetworkExpressRouteGateway, Service: "microsoft.network"})
	registerType(restype.Descriptor{Type: TypeNetworkVirtualNetworkGW, Service: "microsoft.network"})
	registerType(restype.Descriptor{Type: TypeNetworkWAFPolicy, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkApplicationSecurityGroup, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkAzureFirewallFqdnTag, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkAzureFirewall, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkAzureWebCategory, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkBastionHost, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkBgpServiceCommunity, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkConnection, Service: "microsoft.network", Leaf: true, Redact: []redact.Rule{{Path: "properties.sharedKey", Mode: redact.RedactScalar}, {Path: "properties.authorizationKey", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeNetworkCustomIPPrefix, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkDdosProtectionPlan, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkDscpConfiguration, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkExpressRoutePort, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkExpressRoutePortsLocation, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkExpressRouteServiceProv, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkFirewallPolicy, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkIPAllocation, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkIPGroup, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkLoadBalancer, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkLocalNetworkGateway, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkNatGateway, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkInterface, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkManagerConnection, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkManager, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkProfile, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkVirtualAppliance, Service: "microsoft.network", Leaf: true, Redact: []redact.Rule{{Path: "properties.cloudInitConfiguration", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeNetworkWatcher, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkP2SVPNGateway, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkPrivateLinkService, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkPublicIPPrefix, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkRouteFilter, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkRouteTable, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkSecurityPartnerProvider, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkServiceEndpointPolicy, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkVirtualNetworkTap, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkVirtualRouter, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkVPNServerConfiguration, Service: "microsoft.network", Leaf: true, Redact: []redact.Rule{{Path: "properties.radiusServerSecret", Mode: redact.RedactScalar}, {Path: "properties.radiusServers[*].radiusServerSecret", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "azure:microsoft.network",
		fn:   scanNetworkNamespace,
	})
}

// scanNetwork is the single entry point for every Microsoft.Network resource
// type disco scans. Phases run concurrently via sync.WaitGroup (NOT errgroup
// — per "Errors never abort scan", every phase attempts regardless of
// sibling failures). Per-phase AccessDenied is tolerated via
// skipIfAccessDenied inside each phase; the orchestrator surfaces only the
// first non-tolerated error so the dispatcher can report-and-continue.
//
// Phase split:
//   - VNets / NSGs / PublicIPs: subscription-wide List, embedded subnet
//     children for VNets.
//   - ER circuits / Virtual WANs / Virtual Hubs / VPN Sites / vWAN VPN
//     Gateways: subscription-wide List via azSimpleScan.
//   - VirtualNetworkGateways: RG-scoped only (no sub-wide list API), fan
//     out via azRGFanoutScan.
//   - ExpressRouteGateways: SDK exposes only ListBySubscription (single
//     call, no pager); wrap inline.
func scanNetwork(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
	agwClient, err := armnetwork.NewApplicationGatewaysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewApplicationGatewaysClient: %w", err)
	}

	var (
		mu       sync.Mutex
		firstErr error
	)
	addTotals := func(t, n int, e error) {
		mu.Lock()
		defer mu.Unlock()
		total += t
		inserted += n
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}

	phases := []func() (int, int, error){
		func() (int, int, error) { return scanVNets(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanNSGs(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanPublicIPs(ctx, sub, cred, st, scanID) },
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
		// azRGFanoutScan.
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
		// ExpressRouteGateways — SDK only exposes ListBySubscription (single
		// call, no pager). Inline.
		func() (int, int, error) {
			return scanExpressRouteGateways(ctx, sub, ergClient, st, scanID)
		},
		// Application Gateways — sub-wide ListAll.
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:ApplicationGateways.ListAll", TypeNetworkApplicationGateway, sub, st, scanID,
				agwClient.NewListAllPager(nil),
				func(p armnetwork.ApplicationGatewaysClientListAllResponse) []*armnetwork.ApplicationGateway {
					return p.Value
				},
				agwToBase)
		},
		// Coverage sweep — remaining Microsoft.Network types, fanned out as one
		// nested phase to keep scanNetwork's complexity bounded.
		func() (int, int, error) { return scanNetworkSweep(ctx, sub, cred, st, scanID) },
	}

	var wg sync.WaitGroup
	for _, fn := range phases {
		wg.Go(func() {
			t, n, e := fn()
			addTotals(t, n, e)
		})
	}
	wg.Wait()
	return total, inserted, firstErr
}

func scanVNets(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetwork.NewVirtualNetworksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewVirtualNetworksClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armnetwork:VirtualNetworks.ListAll", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armnetwork:VirtualNetworks.ListAll: %w", err)
		}
		var batch []*store.Resource
		var subnetBatch []*store.Resource
		var subnetPairs [][2]string
		for _, vnet := range page.Value {
			if vnet.ID == nil {
				continue
			}
			name := sv(vnet.Name)
			location := sv(vnet.Location)
			vnetID := sv(vnet.ID)
			vnetResourceID := store.ResourceID("azure", sub.ID, vnetID)

			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeNetworkVirtualNetwork,
				NativeID:       vnetID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(vnet),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(vnet.Tags)
			batch = append(batch, r)

			// Subnets are embedded in the VNet response.
			if vnet.Properties != nil {
				for _, sn := range vnet.Properties.Subnets {
					if sn.ID == nil {
						continue
					}
					snName := sv(sn.Name)
					snID := sv(sn.ID)
					snResource := &store.Resource{
						Provider:       "azure",
						AccountID:      sub.ID,
						AccountName:    &sub.Name,
						Type:           TypeNetworkSubnet,
						NativeID:       snID,
						Name:           &snName,
						Region:         &location,
						AttributesJSON: mustJSON(sn),
						DiscoveredBy:   scanID,
					}
					subnetBatch = append(subnetBatch, snResource)
					snResourceID := store.ResourceID("azure", sub.ID, snID)
					subnetPairs = append(subnetPairs, [2]string{snResourceID, vnetResourceID})
				}
			}
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert VNets: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		if len(subnetBatch) > 0 {
			n, err := st.UpsertResources(subnetBatch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert subnets: %w", err)
			}
			total += len(subnetBatch)
			inserted += n
			if err := st.RecordHierarchyBatch(subnetPairs); err != nil {
				return 0, 0, fmt.Errorf("closure subnets: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanNSGs(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetwork.NewSecurityGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewSecurityGroupsClient: %w", err)
	}
	return azPageScan(ctx, "armnetwork:SecurityGroups.ListAll", sub, st,
		client.NewListAllPager(nil),
		func(page armnetwork.SecurityGroupsClientListAllResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, nsg := range page.Value {
				if nsg.ID == nil {
					continue
				}
				name, loc := sv(nsg.Name), sv(nsg.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeNetworkSecurityGroup, NativeID: sv(nsg.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(nsg.Tags), AttributesJSON: mustJSON(nsg),
					DiscoveredBy: scanID,
				})
			}
			return batch, nil
		})
}

func scanPublicIPs(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetwork.NewPublicIPAddressesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewPublicIPAddressesClient: %w", err)
	}
	return azPageScan(ctx, "armnetwork:PublicIPAddresses.ListAll", sub, st,
		client.NewListAllPager(nil),
		func(page armnetwork.PublicIPAddressesClientListAllResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, ip := range page.Value {
				if ip.ID == nil {
					continue
				}
				name, loc := sv(ip.Name), sv(ip.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeNetworkPublicIPAddress, NativeID: sv(ip.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(ip.Tags), AttributesJSON: mustJSON(ip),
					DiscoveredBy: scanID,
				})
			}
			return batch, nil
		})
}

// scanExpressRouteGateways adapts the single-call ListBySubscription API
// into the standard scanner-phase shape: enumerate, build batch + RG pairs
// via azTrackedRows, upsert.
func scanExpressRouteGateways(ctx context.Context, sub *subscription, client *armnetwork.ExpressRouteGatewaysClient, st *store.Store, scanID string) (total, inserted int, err error) {
	resp, err := client.ListBySubscription(ctx, nil)
	if err != nil {
		if isSkippableScanError(err) {
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
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return len(batch), n, fmt.Errorf("closure ExpressRouteGateways: %w", err)
		}
	}
	return len(batch), n, nil
}

// azTrackedBase extractors for the subscription-wide and RG-fanout phases.
// Each returns id / name / location / tags / full-payload tuples consumed
// by azSimpleScan + azRGFanoutScan + azTrackedRows.

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

func agwToBase(g *armnetwork.ApplicationGateway) azTrackedBase {
	return azTrackedBase{id: sv(g.ID), name: sv(g.Name), location: sv(g.Location), tags: g.Tags, full: g}
}

// scanNetworkNamespace runs every Microsoft.network scanner phase concurrently
// — the namespace spans several disco scanners merged under one serviceEntry
// so the service name aligns to the namespace.
func scanNetworkNamespace(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanNetwork(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanDNS(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanDNSResolver(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanFrontDoor(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanPrivateEndpoints(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanTrafficManager(ctx, sub, cred, st, scanID) },
	)
}
