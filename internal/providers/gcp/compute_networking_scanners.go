package gcp

import (
	"context"
	"sync"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

// Wave 4 of the GCP type-coverage buildout (docs/gcp-type-coverage.md): the
// Compute Engine networking-core domain. New phases of the existing
// "gcp:compute" service. No resolvers this wave — these types reference
// networks/subnetworks/backends via bare self-link strings scattered across
// many different field shapes per type; wiring all of them is a larger,
// separate pass rather than something to guess at per-type here.
func init() {
	registerType(restype.Descriptor{Type: TypeComputeRoute, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeRouter, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeVpnGateway, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeExternalVpnGateway, Service: "compute", Leaf: true})
	registerType(restype.Descriptor{Type: TypeComputeTargetVpnGateway, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeVpnTunnel, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeNetworkAttachment, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeNetworkEndpointGroup, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeRegionNetworkEndpointGroup, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeGlobalNetworkEndpointGroup, Service: "compute", Leaf: true})
	registerType(restype.Descriptor{Type: TypeComputeNetworkFirewallPolicy, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeRegionNetworkFirewallPolicy, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeNetworkProfile, Service: "compute", Leaf: true})
	registerType(restype.Descriptor{Type: TypeComputeNodeGroup, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeNodeTemplate, Service: "compute", Leaf: true})
	registerType(restype.Descriptor{Type: TypeComputePacketMirroring, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeServiceAttachment, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeNetworkEdgeSecurityService, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeCrossSiteNetwork, Service: "compute", Leaf: true})
	registerType(restype.Descriptor{Type: TypeComputeWireGroup, Service: "compute"})
}

func scanComputeRoutes(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:routes.list",
		svc.Routes.List(p.ID),
		func(page *compute.RouteList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, r := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeRoute, NativeID: r.SelfLink, Name: &r.Name,
					CreatedAt: strp(r.CreationTimestamp), AttributesJSON: mustJSON(r),
					DiscoveredBy: scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeRouters(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:routers.aggregatedList",
		svc.Routers.AggregatedList(p.ID),
		func(page *compute.RouterAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, r := range items.Routers {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeRouter, NativeID: r.SelfLink, Name: &r.Name,
						Region:         &region,
						CreatedAt:      strp(r.CreationTimestamp),
						AttributesJSON: mustJSON(r),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeVpnGateways(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:vpnGateways.aggregatedList",
		svc.VpnGateways.AggregatedList(p.ID),
		func(page *compute.VpnGatewayAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, g := range items.VpnGateways {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeVpnGateway, NativeID: g.SelfLink, Name: &g.Name,
						Region:         &region,
						CreatedAt:      strp(g.CreationTimestamp),
						AttributesJSON: mustJSON(g),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeExternalVpnGateways(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:externalVpnGateways.list",
		svc.ExternalVpnGateways.List(p.ID),
		func(page *compute.ExternalVpnGatewayList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, g := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeExternalVpnGateway, NativeID: g.SelfLink, Name: &g.Name,
					CreatedAt: strp(g.CreationTimestamp), AttributesJSON: mustJSON(g),
					DiscoveredBy: scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeTargetVpnGateways(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:targetVpnGateways.aggregatedList",
		svc.TargetVpnGateways.AggregatedList(p.ID),
		func(page *compute.TargetVpnGatewayAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, g := range items.TargetVpnGateways {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeTargetVpnGateway, NativeID: g.SelfLink, Name: &g.Name,
						Region:         &region,
						CreatedAt:      strp(g.CreationTimestamp),
						AttributesJSON: mustJSON(g),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeVpnTunnels(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:vpnTunnels.aggregatedList",
		svc.VpnTunnels.AggregatedList(p.ID),
		func(page *compute.VpnTunnelAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, t := range items.VpnTunnels {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeVpnTunnel, NativeID: t.SelfLink, Name: &t.Name,
						Region:         &region,
						CreatedAt:      strp(t.CreationTimestamp),
						AttributesJSON: mustJSON(t),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeNetworkAttachments's AggregatedList documents itself as
// returning both regional and global scopes, but the ledger only tracks one
// disco type (no NetworkAttachments are global in practice) — Region is set
// when the scope key resolves to one, left nil otherwise.
func scanComputeNetworkAttachments(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:networkAttachments.aggregatedList",
		svc.NetworkAttachments.AggregatedList(p.ID),
		func(page *compute.NetworkAttachmentAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					region = &region0
				}
				for _, na := range items.NetworkAttachments {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeNetworkAttachment, NativeID: na.SelfLink, Name: &na.Name,
						Region:         region,
						CreatedAt:      strp(na.CreationTimestamp),
						AttributesJSON: mustJSON(na),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeNetworkEndpointGroups(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:networkEndpointGroups.aggregatedList",
		svc.NetworkEndpointGroups.AggregatedList(p.ID),
		func(page *compute.NetworkEndpointGroupAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, neg := range items.NetworkEndpointGroups {
					batch = append(batch, negToResource(p, scanID, TypeComputeNetworkEndpointGroup, neg))
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeRegionNetworkEndpointGroups(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return gcpRegionFanoutScan(ctx, p, st, fanoutMed, "compute:regionNetworkEndpointGroups.list",
		func(region string) pager[compute.NetworkEndpointGroupList] {
			return svc.RegionNetworkEndpointGroups.List(p.ID, region)
		},
		func(page *compute.NetworkEndpointGroupList) []*compute.NetworkEndpointGroup { return page.Items },
		func(neg *compute.NetworkEndpointGroup, region string) *store.Resource {
			r := negToResource(p, scanID, TypeComputeRegionNetworkEndpointGroup, neg)
			r.Region = &region
			r.Zone = nil
			return r
		})
}

func scanComputeGlobalNetworkEndpointGroups(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:globalNetworkEndpointGroups.list",
		svc.GlobalNetworkEndpointGroups.List(p.ID),
		func(page *compute.NetworkEndpointGroupList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, neg := range page.Items {
				batch = append(batch, negToResource(p, scanID, TypeComputeGlobalNetworkEndpointGroup, neg))
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func negToResource(p *project, scanID, discoType string, neg *compute.NetworkEndpointGroup) *store.Resource {
	r := &store.Resource{
		Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
		Type: discoType, NativeID: neg.SelfLink, Name: &neg.Name,
		CreatedAt: strp(neg.CreationTimestamp), AttributesJSON: mustJSON(neg),
		DiscoveredBy: scanID,
	}
	if neg.Zone != "" {
		zone := lastSegment(neg.Zone)
		r.Zone = &zone
		region := zoneToRegion(zone)
		r.Region = &region
	}
	return r
}

// scanComputeNetworkFirewallPolicies covers both NetworkFirewallPolicy
// (global) and RegionNetworkFirewallPolicy (regional) via a single
// AggregatedList call — same combined-scope shape as Wave 2/3's
// InstanceTemplate/PublicDelegatedPrefix. Item type is compute.FirewallPolicy
// (the SDK drops the "Network" prefix on the item struct even though the
// service/scoped-list names keep it).
func scanComputeNetworkFirewallPolicies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:networkFirewallPolicies.aggregatedList",
		svc.NetworkFirewallPolicies.AggregatedList(p.ID),
		func(page *compute.NetworkFirewallPolicyAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				discoType := TypeComputeNetworkFirewallPolicy
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					discoType = TypeComputeRegionNetworkFirewallPolicy
					region = &region0
				}
				for _, fp := range items.FirewallPolicies {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: discoType, NativeID: fp.SelfLink, Name: &fp.Name,
						Region:         region,
						CreatedAt:      strp(fp.CreationTimestamp),
						AttributesJSON: mustJSON(fp),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeNetworkProfiles(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:networkProfiles.list",
		svc.NetworkProfiles.List(p.ID),
		func(page *compute.NetworkProfilesListResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, np := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeNetworkProfile, NativeID: np.SelfLink, Name: &np.Name,
					CreatedAt: strp(np.CreationTimestamp), AttributesJSON: mustJSON(np),
					DiscoveredBy: scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeNodeGroups(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:nodeGroups.aggregatedList",
		svc.NodeGroups.AggregatedList(p.ID),
		func(page *compute.NodeGroupAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, ng := range items.NodeGroups {
					r := &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeNodeGroup, NativeID: ng.SelfLink, Name: &ng.Name,
						CreatedAt: strp(ng.CreationTimestamp), AttributesJSON: mustJSON(ng),
						DiscoveredBy: scanID,
					}
					if ng.Zone != "" {
						zone := lastSegment(ng.Zone)
						r.Zone = &zone
						region := zoneToRegion(zone)
						r.Region = &region
					}
					batch = append(batch, r)
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeNodeTemplates(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:nodeTemplates.aggregatedList",
		svc.NodeTemplates.AggregatedList(p.ID),
		func(page *compute.NodeTemplateAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, nt := range items.NodeTemplates {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeNodeTemplate, NativeID: nt.SelfLink, Name: &nt.Name,
						Region:         &region,
						CreatedAt:      strp(nt.CreationTimestamp),
						AttributesJSON: mustJSON(nt),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputePacketMirrorings(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:packetMirrorings.aggregatedList",
		svc.PacketMirrorings.AggregatedList(p.ID),
		func(page *compute.PacketMirroringAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, pm := range items.PacketMirrorings {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputePacketMirroring, NativeID: pm.SelfLink, Name: &pm.Name,
						Region:         &region,
						CreatedAt:      strp(pm.CreationTimestamp),
						AttributesJSON: mustJSON(pm),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeServiceAttachments — same combined-scope caveat as
// scanComputeNetworkAttachments: AggregatedList spans regional+global but the
// ledger tracks one disco type.
func scanComputeServiceAttachments(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:serviceAttachments.aggregatedList",
		svc.ServiceAttachments.AggregatedList(p.ID),
		func(page *compute.ServiceAttachmentAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					region = &region0
				}
				for _, sa := range items.ServiceAttachments {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeServiceAttachment, NativeID: sa.SelfLink, Name: &sa.Name,
						Region:         region,
						CreatedAt:      strp(sa.CreationTimestamp),
						AttributesJSON: mustJSON(sa),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeNetworkEdgeSecurityServices(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:networkEdgeSecurityServices.aggregatedList",
		svc.NetworkEdgeSecurityServices.AggregatedList(p.ID),
		func(page *compute.NetworkEdgeSecurityServiceAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, nes := range items.NetworkEdgeSecurityServices {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeNetworkEdgeSecurityService, NativeID: nes.SelfLink, Name: &nes.Name,
						Region:         &region,
						CreatedAt:      strp(nes.CreationTimestamp),
						AttributesJSON: mustJSON(nes),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeCrossSiteNetworks collects the discovered network names during
// its own List phase so scanComputeWireGroups can fan out the nested
// wireGroups.list call per network — same two-phase list-then-fan-out shape
// as Wave 2's IGM ResizeRequests.
func scanComputeCrossSiteNetworks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	var names []string
	t, n, err := runPaginated(ctx, st, p, "compute:crossSiteNetworks.list",
		svc.CrossSiteNetworks.List(p.ID),
		func(page *compute.CrossSiteNetworkList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, xsn := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeCrossSiteNetwork, NativeID: xsn.SelfLink, Name: &xsn.Name,
					CreatedAt: strp(xsn.CreationTimestamp), AttributesJSON: mustJSON(xsn),
					DiscoveredBy: scanID,
				})
				names = append(names, xsn.Name)
			}
			return upsertWithProjClosure(p, st, batch)
		})
	if err != nil {
		return t, n, err
	}
	t2, n2, err := scanComputeWireGroups(ctx, svc, p, st, scanID, names)
	return t + t2, n + n2, err
}

func scanComputeWireGroups(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string, crossSiteNetworkNames []string) (total, inserted int, err error) {
	if len(crossSiteNetworkNames) == 0 {
		return 0, 0, nil
	}
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	if err := forEachItem(ctx, fanoutMed, crossSiteNetworkNames, func(gctx context.Context, xsnName string) error {
		perr := svc.WireGroups.List(p.ID, xsnName).Pages(gctx, func(page *compute.WireGroupList) error {
			local := make([]*store.Resource, 0, len(page.Items))
			for _, wg := range page.Items {
				local = append(local, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeWireGroup, NativeID: wg.SelfLink, Name: &wg.Name,
					CreatedAt: strp(wg.CreationTimestamp), AttributesJSON: mustJSON(wg),
					DiscoveredBy: scanID,
				})
			}
			if len(local) > 0 {
				mu.Lock()
				batch = append(batch, local...)
				mu.Unlock()
			}
			return nil
		})
		if perr != nil {
			if isPermissionDenied(perr) {
				return skipIfDenied(st, "compute:wireGroups.list", p.ID+"/"+xsnName, perr)
			}
			return perr
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	return upsertWithProjClosure(p, st, batch)
}
