package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

// scanNetworkSweep discovers the remaining sub/RG-listable Microsoft.Network
// resource types not covered by the core scanNetwork phases. It runs every
// type concurrently (sync.WaitGroup + mutex aggregation, matching scanNetwork)
// so a single AccessDenied on one type never blocks the others. Most types
// expose a subscription-wide NewListAllPager / NewListPager; the handful that
// only offer per-RG endpoints (connections, localnetworkgateways) fan out via
// azRGFanoutScan.
//
// Catalogue types Azure auto-materialises and the user cannot delete
// (azurefirewallfqdntags, azurewebcategories, bgpservicecommunities,
// expressrouteportslocations, expressrouteserviceproviders,
// networkvirtualapplianceskus) are upserted with managed=true so they hide
// from default `disco list` / `disco graph`.
func scanNetworkSweep(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	phases, err := networkSweepPhases(ctx, sub, cred, st, scanID)
	if err != nil {
		return 0, 0, err
	}
	return azRunPhases(phases...)
}

// networkSweepPhases constructs every armnetwork client once and returns the
// per-type scan closures. Client construction is the only error-returning step;
// a failure there aborts the whole sweep (credential/options are shared, so one
// failing means all would).
//
//nolint:gocognit // linear client-construct + one closure per type; splitting hides the per-type list.
func networkSweepPhases(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) ([]func() (int, int, error), error) {
	var (
		waf       *armnetwork.WebApplicationFirewallPoliciesClient
		asg       *armnetwork.ApplicationSecurityGroupsClient
		fqdnTags  *armnetwork.AzureFirewallFqdnTagsClient
		firewalls *armnetwork.AzureFirewallsClient
		webCat    *armnetwork.WebCategoriesClient
		bastion   *armnetwork.BastionHostsClient
		bgpComm   *armnetwork.BgpServiceCommunitiesClient
		conns     *armnetwork.VirtualNetworkGatewayConnectionsClient
		customIP  *armnetwork.CustomIPPrefixesClient
		ddos      *armnetwork.DdosProtectionPlansClient
		dscp      *armnetwork.DscpConfigurationClient
		erPorts   *armnetwork.ExpressRoutePortsClient
		erPortLoc *armnetwork.ExpressRoutePortsLocationsClient
		erSP      *armnetwork.ExpressRouteServiceProvidersClient
		fwPolicy  *armnetwork.FirewallPoliciesClient
		ipAlloc   *armnetwork.IPAllocationsClient
		ipGroups  *armnetwork.IPGroupsClient
		lbs       *armnetwork.LoadBalancersClient
		localGW   *armnetwork.LocalNetworkGatewaysClient
		natGW     *armnetwork.NatGatewaysClient
		nics      *armnetwork.InterfacesClient
		mgrConn   *armnetwork.SubscriptionNetworkManagerConnectionsClient
		mgrs      *armnetwork.ManagersClient
		netProf   *armnetwork.ProfilesClient
		nva       *armnetwork.VirtualAppliancesClient
		nvaSKU    *armnetwork.VirtualApplianceSKUsClient
		watchers  *armnetwork.WatchersClient
		p2sGW     *armnetwork.P2SVPNGatewaysClient
		pls       *armnetwork.PrivateLinkServicesClient
		pipPrefix *armnetwork.PublicIPPrefixesClient
		routeFltr *armnetwork.RouteFiltersClient
		routeTbl  *armnetwork.RouteTablesClient
		secPP     *armnetwork.SecurityPartnerProvidersClient
		sepPolicy *armnetwork.ServiceEndpointPoliciesClient
		vnetTaps  *armnetwork.VirtualNetworkTapsClient
		vrouters  *armnetwork.VirtualRoutersClient
		vpnSrvCfg *armnetwork.VPNServerConfigurationsClient
	)
	// Each entry constructs one armnetwork client; the first construction
	// failure aborts the sweep (shared cred/options means all would fail).
	ctors := map[string]func() error{
		"WebApplicationFirewallPoliciesClient": func() (e error) {
			waf, e = armnetwork.NewWebApplicationFirewallPoliciesClient(sub.ID, cred, azClientOptions)
			return
		},
		"ApplicationSecurityGroupsClient": func() (e error) {
			asg, e = armnetwork.NewApplicationSecurityGroupsClient(sub.ID, cred, azClientOptions)
			return
		},
		"AzureFirewallFqdnTagsClient": func() (e error) {
			fqdnTags, e = armnetwork.NewAzureFirewallFqdnTagsClient(sub.ID, cred, azClientOptions)
			return
		},
		"AzureFirewallsClient": func() (e error) {
			firewalls, e = armnetwork.NewAzureFirewallsClient(sub.ID, cred, azClientOptions)
			return
		},
		"WebCategoriesClient": func() (e error) { webCat, e = armnetwork.NewWebCategoriesClient(sub.ID, cred, azClientOptions); return },
		"BastionHostsClient":  func() (e error) { bastion, e = armnetwork.NewBastionHostsClient(sub.ID, cred, azClientOptions); return },
		"BgpServiceCommunitiesClient": func() (e error) {
			bgpComm, e = armnetwork.NewBgpServiceCommunitiesClient(sub.ID, cred, azClientOptions)
			return
		},
		"VirtualNetworkGatewayConnectionsClient": func() (e error) {
			conns, e = armnetwork.NewVirtualNetworkGatewayConnectionsClient(sub.ID, cred, azClientOptions)
			return
		},
		"CustomIPPrefixesClient": func() (e error) {
			customIP, e = armnetwork.NewCustomIPPrefixesClient(sub.ID, cred, azClientOptions)
			return
		},
		"DdosProtectionPlansClient": func() (e error) {
			ddos, e = armnetwork.NewDdosProtectionPlansClient(sub.ID, cred, azClientOptions)
			return
		},
		"DscpConfigurationClient": func() (e error) {
			dscp, e = armnetwork.NewDscpConfigurationClient(sub.ID, cred, azClientOptions)
			return
		},
		"ExpressRoutePortsClient": func() (e error) {
			erPorts, e = armnetwork.NewExpressRoutePortsClient(sub.ID, cred, azClientOptions)
			return
		},
		"ExpressRoutePortsLocationsClient": func() (e error) {
			erPortLoc, e = armnetwork.NewExpressRoutePortsLocationsClient(sub.ID, cred, azClientOptions)
			return
		},
		"ExpressRouteServiceProvidersClient": func() (e error) {
			erSP, e = armnetwork.NewExpressRouteServiceProvidersClient(sub.ID, cred, azClientOptions)
			return
		},
		"FirewallPoliciesClient": func() (e error) {
			fwPolicy, e = armnetwork.NewFirewallPoliciesClient(sub.ID, cred, azClientOptions)
			return
		},
		"IPAllocationsClient": func() (e error) {
			ipAlloc, e = armnetwork.NewIPAllocationsClient(sub.ID, cred, azClientOptions)
			return
		},
		"IPGroupsClient":      func() (e error) { ipGroups, e = armnetwork.NewIPGroupsClient(sub.ID, cred, azClientOptions); return },
		"LoadBalancersClient": func() (e error) { lbs, e = armnetwork.NewLoadBalancersClient(sub.ID, cred, azClientOptions); return },
		"LocalNetworkGatewaysClient": func() (e error) {
			localGW, e = armnetwork.NewLocalNetworkGatewaysClient(sub.ID, cred, azClientOptions)
			return
		},
		"NatGatewaysClient": func() (e error) { natGW, e = armnetwork.NewNatGatewaysClient(sub.ID, cred, azClientOptions); return },
		"InterfacesClient":  func() (e error) { nics, e = armnetwork.NewInterfacesClient(sub.ID, cred, azClientOptions); return },
		"SubscriptionNetworkManagerConnectionsClient": func() (e error) {
			mgrConn, e = armnetwork.NewSubscriptionNetworkManagerConnectionsClient(sub.ID, cred, azClientOptions)
			return
		},
		"ManagersClient": func() (e error) { mgrs, e = armnetwork.NewManagersClient(sub.ID, cred, azClientOptions); return },
		"ProfilesClient": func() (e error) { netProf, e = armnetwork.NewProfilesClient(sub.ID, cred, azClientOptions); return },
		"VirtualAppliancesClient": func() (e error) {
			nva, e = armnetwork.NewVirtualAppliancesClient(sub.ID, cred, azClientOptions)
			return
		},
		"VirtualApplianceSKUsClient": func() (e error) {
			nvaSKU, e = armnetwork.NewVirtualApplianceSKUsClient(sub.ID, cred, azClientOptions)
			return
		},
		"WatchersClient":       func() (e error) { watchers, e = armnetwork.NewWatchersClient(sub.ID, cred, azClientOptions); return },
		"P2SVPNGatewaysClient": func() (e error) { p2sGW, e = armnetwork.NewP2SVPNGatewaysClient(sub.ID, cred, azClientOptions); return },
		"PrivateLinkServicesClient": func() (e error) {
			pls, e = armnetwork.NewPrivateLinkServicesClient(sub.ID, cred, azClientOptions)
			return
		},
		"PublicIPPrefixesClient": func() (e error) {
			pipPrefix, e = armnetwork.NewPublicIPPrefixesClient(sub.ID, cred, azClientOptions)
			return
		},
		"RouteFiltersClient": func() (e error) {
			routeFltr, e = armnetwork.NewRouteFiltersClient(sub.ID, cred, azClientOptions)
			return
		},
		"RouteTablesClient": func() (e error) { routeTbl, e = armnetwork.NewRouteTablesClient(sub.ID, cred, azClientOptions); return },
		"SecurityPartnerProvidersClient": func() (e error) {
			secPP, e = armnetwork.NewSecurityPartnerProvidersClient(sub.ID, cred, azClientOptions)
			return
		},
		"ServiceEndpointPoliciesClient": func() (e error) {
			sepPolicy, e = armnetwork.NewServiceEndpointPoliciesClient(sub.ID, cred, azClientOptions)
			return
		},
		"VirtualNetworkTapsClient": func() (e error) {
			vnetTaps, e = armnetwork.NewVirtualNetworkTapsClient(sub.ID, cred, azClientOptions)
			return
		},
		"VirtualRoutersClient": func() (e error) {
			vrouters, e = armnetwork.NewVirtualRoutersClient(sub.ID, cred, azClientOptions)
			return
		},
		"VPNServerConfigurationsClient": func() (e error) {
			vpnSrvCfg, e = armnetwork.NewVPNServerConfigurationsClient(sub.ID, cred, azClientOptions)
			return
		},
	}
	for name, c := range ctors {
		if e := c(); e != nil {
			return nil, fmt.Errorf("armnetwork:New%s: %w", name, e)
		}
	}

	return []func() (int, int, error){
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:WebApplicationFirewallPolicies.ListAll", TypeNetworkWAFPolicy, sub, st, scanID,
				waf.NewListAllPager(nil),
				func(p armnetwork.WebApplicationFirewallPoliciesClientListAllResponse) []*armnetwork.WebApplicationFirewallPolicy {
					return p.Value
				},
				func(r *armnetwork.WebApplicationFirewallPolicy) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:ApplicationSecurityGroups.ListAll", TypeNetworkApplicationSecurityGroup, sub, st, scanID,
				asg.NewListAllPager(nil),
				func(p armnetwork.ApplicationSecurityGroupsClientListAllResponse) []*armnetwork.ApplicationSecurityGroup {
					return p.Value
				},
				func(r *armnetwork.ApplicationSecurityGroup) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:AzureFirewallFqdnTags.ListAll", TypeNetworkAzureFirewallFqdnTag, sub, st, scanID,
				fqdnTags.NewListAllPager(nil),
				func(p armnetwork.AzureFirewallFqdnTagsClientListAllResponse) []*armnetwork.AzureFirewallFqdnTag {
					return p.Value
				},
				func(r *armnetwork.AzureFirewallFqdnTag) azTrackedBase {
					return netManagedBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:AzureFirewalls.ListAll", TypeNetworkAzureFirewall, sub, st, scanID,
				firewalls.NewListAllPager(nil),
				func(p armnetwork.AzureFirewallsClientListAllResponse) []*armnetwork.AzureFirewall { return p.Value },
				func(r *armnetwork.AzureFirewall) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:WebCategories.ListBySubscription", TypeNetworkAzureWebCategory, sub, st, scanID,
				webCat.NewListBySubscriptionPager(nil),
				func(p armnetwork.WebCategoriesClientListBySubscriptionResponse) []*armnetwork.AzureWebCategory {
					return p.Value
				},
				func(r *armnetwork.AzureWebCategory) azTrackedBase { return netManagedProxyBase(r.ID, r.Name, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:BastionHosts.List", TypeNetworkBastionHost, sub, st, scanID,
				bastion.NewListPager(nil),
				func(p armnetwork.BastionHostsClientListResponse) []*armnetwork.BastionHost { return p.Value },
				func(r *armnetwork.BastionHost) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:BgpServiceCommunities.List", TypeNetworkBgpServiceCommunity, sub, st, scanID,
				bgpComm.NewListPager(nil),
				func(p armnetwork.BgpServiceCommunitiesClientListResponse) []*armnetwork.BgpServiceCommunity {
					return p.Value
				},
				func(r *armnetwork.BgpServiceCommunity) azTrackedBase {
					return netManagedBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		// connections — RG-scoped only.
		func() (int, int, error) {
			return azRGFanoutScan(ctx, "armnetwork:VirtualNetworkGatewayConnections.List", TypeNetworkConnection, sub, cred, st, scanID,
				func(rg string) azPager[armnetwork.VirtualNetworkGatewayConnectionsClientListResponse] {
					return conns.NewListPager(rg, nil)
				},
				func(p armnetwork.VirtualNetworkGatewayConnectionsClientListResponse) []*armnetwork.VirtualNetworkGatewayConnection {
					return p.Value
				},
				func(r *armnetwork.VirtualNetworkGatewayConnection) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:CustomIPPrefixes.ListAll", TypeNetworkCustomIPPrefix, sub, st, scanID,
				customIP.NewListAllPager(nil),
				func(p armnetwork.CustomIPPrefixesClientListAllResponse) []*armnetwork.CustomIPPrefix { return p.Value },
				func(r *armnetwork.CustomIPPrefix) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:DdosProtectionPlans.List", TypeNetworkDdosProtectionPlan, sub, st, scanID,
				ddos.NewListPager(nil),
				func(p armnetwork.DdosProtectionPlansClientListResponse) []*armnetwork.DdosProtectionPlan {
					return p.Value
				},
				func(r *armnetwork.DdosProtectionPlan) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:DscpConfiguration.ListAll", TypeNetworkDscpConfiguration, sub, st, scanID,
				dscp.NewListAllPager(nil),
				func(p armnetwork.DscpConfigurationClientListAllResponse) []*armnetwork.DscpConfiguration {
					return p.Value
				},
				func(r *armnetwork.DscpConfiguration) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:ExpressRoutePorts.List", TypeNetworkExpressRoutePort, sub, st, scanID,
				erPorts.NewListPager(nil),
				func(p armnetwork.ExpressRoutePortsClientListResponse) []*armnetwork.ExpressRoutePort { return p.Value },
				func(r *armnetwork.ExpressRoutePort) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:ExpressRoutePortsLocations.List", TypeNetworkExpressRoutePortsLocation, sub, st, scanID,
				erPortLoc.NewListPager(nil),
				func(p armnetwork.ExpressRoutePortsLocationsClientListResponse) []*armnetwork.ExpressRoutePortsLocation {
					return p.Value
				},
				func(r *armnetwork.ExpressRoutePortsLocation) azTrackedBase {
					return netManagedBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:ExpressRouteServiceProviders.List", TypeNetworkExpressRouteServiceProv, sub, st, scanID,
				erSP.NewListPager(nil),
				func(p armnetwork.ExpressRouteServiceProvidersClientListResponse) []*armnetwork.ExpressRouteServiceProvider {
					return p.Value
				},
				func(r *armnetwork.ExpressRouteServiceProvider) azTrackedBase {
					return netManagedBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:FirewallPolicies.ListAll", TypeNetworkFirewallPolicy, sub, st, scanID,
				fwPolicy.NewListAllPager(nil),
				func(p armnetwork.FirewallPoliciesClientListAllResponse) []*armnetwork.FirewallPolicy { return p.Value },
				func(r *armnetwork.FirewallPolicy) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:IPAllocations.List", TypeNetworkIPAllocation, sub, st, scanID,
				ipAlloc.NewListPager(nil),
				func(p armnetwork.IPAllocationsClientListResponse) []*armnetwork.IPAllocation { return p.Value },
				func(r *armnetwork.IPAllocation) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:IPGroups.List", TypeNetworkIPGroup, sub, st, scanID,
				ipGroups.NewListPager(nil),
				func(p armnetwork.IPGroupsClientListResponse) []*armnetwork.IPGroup { return p.Value },
				func(r *armnetwork.IPGroup) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:LoadBalancers.ListAll", TypeNetworkLoadBalancer, sub, st, scanID,
				lbs.NewListAllPager(nil),
				func(p armnetwork.LoadBalancersClientListAllResponse) []*armnetwork.LoadBalancer { return p.Value },
				func(r *armnetwork.LoadBalancer) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		// localnetworkgateways — RG-scoped only.
		func() (int, int, error) {
			return azRGFanoutScan(ctx, "armnetwork:LocalNetworkGateways.List", TypeNetworkLocalNetworkGateway, sub, cred, st, scanID,
				func(rg string) azPager[armnetwork.LocalNetworkGatewaysClientListResponse] {
					return localGW.NewListPager(rg, nil)
				},
				func(p armnetwork.LocalNetworkGatewaysClientListResponse) []*armnetwork.LocalNetworkGateway {
					return p.Value
				},
				func(r *armnetwork.LocalNetworkGateway) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:NatGateways.ListAll", TypeNetworkNatGateway, sub, st, scanID,
				natGW.NewListAllPager(nil),
				func(p armnetwork.NatGatewaysClientListAllResponse) []*armnetwork.NatGateway { return p.Value },
				func(r *armnetwork.NatGateway) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:Interfaces.ListAll", TypeNetworkInterface, sub, st, scanID,
				nics.NewListAllPager(nil),
				func(p armnetwork.InterfacesClientListAllResponse) []*armnetwork.Interface { return p.Value },
				func(r *armnetwork.Interface) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:SubscriptionNetworkManagerConnections.List", TypeNetworkManagerConnection, sub, st, scanID,
				mgrConn.NewListPager(nil),
				func(p armnetwork.SubscriptionNetworkManagerConnectionsClientListResponse) []*armnetwork.ManagerConnection {
					return p.Value
				},
				func(r *armnetwork.ManagerConnection) azTrackedBase { return netProxyBase(r.ID, r.Name, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:Managers.ListBySubscription", TypeNetworkManager, sub, st, scanID,
				mgrs.NewListBySubscriptionPager(nil),
				func(p armnetwork.ManagersClientListBySubscriptionResponse) []*armnetwork.Manager { return p.Value },
				func(r *armnetwork.Manager) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:Profiles.ListAll", TypeNetworkProfile, sub, st, scanID,
				netProf.NewListAllPager(nil),
				func(p armnetwork.ProfilesClientListAllResponse) []*armnetwork.Profile { return p.Value },
				func(r *armnetwork.Profile) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:VirtualAppliances.List", TypeNetworkVirtualAppliance, sub, st, scanID,
				nva.NewListPager(nil),
				func(p armnetwork.VirtualAppliancesClientListResponse) []*armnetwork.VirtualAppliance { return p.Value },
				func(r *armnetwork.VirtualAppliance) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:VirtualApplianceSKUs.List", TypeNetworkVirtualApplianceSKU, sub, st, scanID,
				nvaSKU.NewListPager(nil),
				func(p armnetwork.VirtualApplianceSKUsClientListResponse) []*armnetwork.VirtualApplianceSKU {
					return p.Value
				},
				func(r *armnetwork.VirtualApplianceSKU) azTrackedBase {
					return netManagedBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:Watchers.ListAll", TypeNetworkWatcher, sub, st, scanID,
				watchers.NewListAllPager(nil),
				func(p armnetwork.WatchersClientListAllResponse) []*armnetwork.Watcher { return p.Value },
				func(r *armnetwork.Watcher) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:P2SVPNGateways.List", TypeNetworkP2SVPNGateway, sub, st, scanID,
				p2sGW.NewListPager(nil),
				func(p armnetwork.P2SVPNGatewaysClientListResponse) []*armnetwork.P2SVPNGateway { return p.Value },
				func(r *armnetwork.P2SVPNGateway) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:PrivateLinkServices.ListBySubscription", TypeNetworkPrivateLinkService, sub, st, scanID,
				pls.NewListBySubscriptionPager(nil),
				func(p armnetwork.PrivateLinkServicesClientListBySubscriptionResponse) []*armnetwork.PrivateLinkService {
					return p.Value
				},
				func(r *armnetwork.PrivateLinkService) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:PublicIPPrefixes.ListAll", TypeNetworkPublicIPPrefix, sub, st, scanID,
				pipPrefix.NewListAllPager(nil),
				func(p armnetwork.PublicIPPrefixesClientListAllResponse) []*armnetwork.PublicIPPrefix { return p.Value },
				func(r *armnetwork.PublicIPPrefix) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:RouteFilters.List", TypeNetworkRouteFilter, sub, st, scanID,
				routeFltr.NewListPager(nil),
				func(p armnetwork.RouteFiltersClientListResponse) []*armnetwork.RouteFilter { return p.Value },
				func(r *armnetwork.RouteFilter) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:RouteTables.ListAll", TypeNetworkRouteTable, sub, st, scanID,
				routeTbl.NewListAllPager(nil),
				func(p armnetwork.RouteTablesClientListAllResponse) []*armnetwork.RouteTable { return p.Value },
				func(r *armnetwork.RouteTable) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:SecurityPartnerProviders.List", TypeNetworkSecurityPartnerProvider, sub, st, scanID,
				secPP.NewListPager(nil),
				func(p armnetwork.SecurityPartnerProvidersClientListResponse) []*armnetwork.SecurityPartnerProvider {
					return p.Value
				},
				func(r *armnetwork.SecurityPartnerProvider) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:ServiceEndpointPolicies.List", TypeNetworkServiceEndpointPolicy, sub, st, scanID,
				sepPolicy.NewListPager(nil),
				func(p armnetwork.ServiceEndpointPoliciesClientListResponse) []*armnetwork.ServiceEndpointPolicy {
					return p.Value
				},
				func(r *armnetwork.ServiceEndpointPolicy) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:VirtualNetworkTaps.ListAll", TypeNetworkVirtualNetworkTap, sub, st, scanID,
				vnetTaps.NewListAllPager(nil),
				func(p armnetwork.VirtualNetworkTapsClientListAllResponse) []*armnetwork.VirtualNetworkTap {
					return p.Value
				},
				func(r *armnetwork.VirtualNetworkTap) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:VirtualRouters.List", TypeNetworkVirtualRouter, sub, st, scanID,
				vrouters.NewListPager(nil),
				func(p armnetwork.VirtualRoutersClientListResponse) []*armnetwork.VirtualRouter { return p.Value },
				func(r *armnetwork.VirtualRouter) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armnetwork:VPNServerConfigurations.List", TypeNetworkVPNServerConfiguration, sub, st, scanID,
				vpnSrvCfg.NewListPager(nil),
				func(p armnetwork.VPNServerConfigurationsClientListResponse) []*armnetwork.VPNServerConfiguration {
					return p.Value
				},
				func(r *armnetwork.VPNServerConfiguration) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
	}, nil
}

// netBase / netManagedBase / netProxyBase / netManagedProxyBase build the
// azTrackedBase shape from the shared (ID, Name, Location, Tags) SDK fields.
// Proxy variants omit location/tags for types whose SDK struct lacks them.
func netBase(id, name, location *string, tags map[string]*string, full any) azTrackedBase {
	return azTrackedBase{id: sv(id), name: sv(name), location: sv(location), tags: tags, full: full}
}

func netManagedBase(id, name, location *string, tags map[string]*string, full any) azTrackedBase {
	return azTrackedBase{id: sv(id), name: sv(name), location: sv(location), tags: tags, managed: true, full: full}
}

func netProxyBase(id, name *string, full any) azTrackedBase {
	return azTrackedBase{id: sv(id), name: sv(name), full: full}
}

func netManagedProxyBase(id, name *string, full any) azTrackedBase {
	return azTrackedBase{id: sv(id), name: sv(name), managed: true, full: full}
}
