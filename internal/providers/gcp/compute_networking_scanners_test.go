package gcp

import (
	"errors"
	"net/http"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

// scanComputeRegionNetworkEndpointGroups fans out via gcpRegions, which
// builds its own real ADC client internally — not reachable through the
// fakeComputeService passed to the scanner under test (same caveat as
// earlier waves' Region* scanners). Not covered here.

func TestScanComputeRoutes_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/routes/route1"
	page := compute.RouteList{Items: []*compute.Route{{Name: "route1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/routes": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeRoutes(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeRoutes: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	if _, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeRoute, selfLink)); err != nil {
		t.Errorf("GetResource: %v", err)
	}
}

func TestScanComputeRoutes_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"caller is missing compute.routes.list","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeRoutes(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeRoutes (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

func TestScanComputeRoutes_APINotEnabled(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"Compute Engine API has not been used in project my-project before or it is disabled.","errors":[{"reason":"accessNotConfigured"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	_, _, err := scanComputeRoutes(t.Context(), svc, p, st, testScanID)
	if !errors.Is(err, errServiceDisabled) {
		t.Fatalf("scanComputeRoutes: expected errServiceDisabled sentinel, got %v", err)
	}
}

func TestScanComputeRouters_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/routers/r1"
	page := compute.RouterAggregatedList{
		Items: map[string]compute.RoutersScopedList{
			"regions/us-central1": {Routers: []*compute.Router{{Name: "r1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/routers": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeRouters(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeRouters: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeRouter, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("router region: got %v, want us-central1", got.Region)
	}
}

func TestScanComputeVpnGateways_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/vpnGateways/vg1"
	page := compute.VpnGatewayAggregatedList{
		Items: map[string]compute.VpnGatewaysScopedList{
			"regions/us-central1": {VpnGateways: []*compute.VpnGateway{{Name: "vg1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/vpnGateways": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeVpnGateways(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeVpnGateways: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeExternalVpnGateways_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/externalVpnGateways/evg1"
	page := compute.ExternalVpnGatewayList{Items: []*compute.ExternalVpnGateway{{Name: "evg1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/externalVpnGateways": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeExternalVpnGateways(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeExternalVpnGateways: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeTargetVpnGateways_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/targetVpnGateways/tvg1"
	page := compute.TargetVpnGatewayAggregatedList{
		Items: map[string]compute.TargetVpnGatewaysScopedList{
			"regions/us-central1": {TargetVpnGateways: []*compute.TargetVpnGateway{{Name: "tvg1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/targetVpnGateways": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeTargetVpnGateways(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeTargetVpnGateways: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeVpnTunnels_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/vpnTunnels/vt1"
	page := compute.VpnTunnelAggregatedList{
		Items: map[string]compute.VpnTunnelsScopedList{
			"regions/us-central1": {VpnTunnels: []*compute.VpnTunnel{{Name: "vt1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/vpnTunnels": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeVpnTunnels(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeVpnTunnels: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeNetworkAttachments_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	regionalSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/networkAttachments/na1"
	globalSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/networkAttachments/na-global"
	page := compute.NetworkAttachmentAggregatedList{
		Items: map[string]compute.NetworkAttachmentsScopedList{
			"regions/us-central1": {NetworkAttachments: []*compute.NetworkAttachment{{Name: "na1", SelfLink: regionalSelfLink}}},
			"global":              {NetworkAttachments: []*compute.NetworkAttachment{{Name: "na-global", SelfLink: globalSelfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/networkAttachments": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeNetworkAttachments(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeNetworkAttachments: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	regional, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeNetworkAttachment, regionalSelfLink))
	if err != nil {
		t.Fatalf("GetResource(regional): %v", err)
	}
	if regional.Region == nil || *regional.Region != "us-central1" {
		t.Errorf("regional network attachment region: got %v, want us-central1", regional.Region)
	}
	global, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeNetworkAttachment, globalSelfLink))
	if err != nil {
		t.Fatalf("GetResource(global): %v", err)
	}
	if global.Region != nil {
		t.Errorf("global-scope network attachment should have nil region, got %v", global.Region)
	}
}

func TestScanComputeNetworkEndpointGroups_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/networkEndpointGroups/neg1"
	page := compute.NetworkEndpointGroupAggregatedList{
		Items: map[string]compute.NetworkEndpointGroupsScopedList{
			"zones/us-central1-a": {NetworkEndpointGroups: []*compute.NetworkEndpointGroup{{Name: "neg1", SelfLink: selfLink, Zone: "zones/us-central1-a"}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/networkEndpointGroups": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeNetworkEndpointGroups(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeNetworkEndpointGroups: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeNetworkEndpointGroup, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Zone == nil || *got.Zone != "us-central1-a" {
		t.Errorf("NEG zone: got %v, want us-central1-a", got.Zone)
	}
}

func TestScanComputeGlobalNetworkEndpointGroups_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/networkEndpointGroups/gneg1"
	page := compute.NetworkEndpointGroupList{Items: []*compute.NetworkEndpointGroup{{Name: "gneg1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/networkEndpointGroups": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeGlobalNetworkEndpointGroups(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeGlobalNetworkEndpointGroups: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeNetworkFirewallPolicies_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	globalSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/firewallPolicies/fp-global"
	regionalSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/firewallPolicies/fp-regional"
	page := compute.NetworkFirewallPolicyAggregatedList{
		Items: map[string]compute.FirewallPoliciesScopedList{
			"global":              {FirewallPolicies: []*compute.FirewallPolicy{{Name: "fp-global", SelfLink: globalSelfLink}}},
			"regions/us-central1": {FirewallPolicies: []*compute.FirewallPolicy{{Name: "fp-regional", SelfLink: regionalSelfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/firewallPolicies": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeNetworkFirewallPolicies(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeNetworkFirewallPolicies: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	global, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeNetworkFirewallPolicy, globalSelfLink))
	if err != nil {
		t.Fatalf("GetResource(global): %v", err)
	}
	if global.Region != nil {
		t.Errorf("global firewall policy should have nil region, got %v", global.Region)
	}
	regional, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeRegionNetworkFirewallPolicy, regionalSelfLink))
	if err != nil {
		t.Fatalf("GetResource(regional): %v", err)
	}
	if regional.Region == nil || *regional.Region != "us-central1" {
		t.Errorf("regional firewall policy region: got %v, want us-central1", regional.Region)
	}
}

func TestScanComputeNetworkProfiles_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/networkProfiles/np1"
	page := compute.NetworkProfilesListResponse{Items: []*compute.NetworkProfile{{Name: "np1", SelfLink: selfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/networkProfiles": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeNetworkProfiles(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeNetworkProfiles: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeNodeGroups_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/nodeGroups/ng1"
	page := compute.NodeGroupAggregatedList{
		Items: map[string]compute.NodeGroupsScopedList{
			"zones/us-central1-a": {NodeGroups: []*compute.NodeGroup{{Name: "ng1", SelfLink: selfLink, Zone: "zones/us-central1-a"}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/nodeGroups": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeNodeGroups(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeNodeGroups: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeNodeGroup, selfLink))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Zone == nil || *got.Zone != "us-central1-a" {
		t.Errorf("node group zone: got %v, want us-central1-a", got.Zone)
	}
}

func TestScanComputeNodeTemplates_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/nodeTemplates/nt1"
	page := compute.NodeTemplateAggregatedList{
		Items: map[string]compute.NodeTemplatesScopedList{
			"regions/us-central1": {NodeTemplates: []*compute.NodeTemplate{{Name: "nt1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/nodeTemplates": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeNodeTemplates(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeNodeTemplates: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputePacketMirrorings_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/packetMirrorings/pm1"
	page := compute.PacketMirroringAggregatedList{
		Items: map[string]compute.PacketMirroringsScopedList{
			"regions/us-central1": {PacketMirrorings: []*compute.PacketMirroring{{Name: "pm1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/packetMirrorings": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputePacketMirrorings(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputePacketMirrorings: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeServiceAttachments_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/serviceAttachments/sa1"
	page := compute.ServiceAttachmentAggregatedList{
		Items: map[string]compute.ServiceAttachmentsScopedList{
			"regions/us-central1": {ServiceAttachments: []*compute.ServiceAttachment{{Name: "sa1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/serviceAttachments": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeServiceAttachments(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeServiceAttachments: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeNetworkEdgeSecurityServices_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	selfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/networkEdgeSecurityServices/nes1"
	page := compute.NetworkEdgeSecurityServiceAggregatedList{
		Items: map[string]compute.NetworkEdgeSecurityServicesScopedList{
			"regions/us-central1": {NetworkEdgeSecurityServices: []*compute.NetworkEdgeSecurityService{{Name: "nes1", SelfLink: selfLink}}},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/networkEdgeSecurityServices": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeNetworkEdgeSecurityServices(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeNetworkEdgeSecurityServices: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeCrossSiteNetworks_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	xsnSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/crossSiteNetworks/xsn1"
	wgSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/crossSiteNetworks/xsn1/wireGroups/wg1"
	xsnPage := compute.CrossSiteNetworkList{Items: []*compute.CrossSiteNetwork{{Name: "xsn1", SelfLink: xsnSelfLink}}}
	wgPage := compute.WireGroupList{Items: []*compute.WireGroup{{Name: "wg1", SelfLink: wgSelfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/crossSiteNetworks":                 marshalAttrs(t, xsnPage),
		"/projects/my-project/global/crossSiteNetworks/xsn1/wireGroups": marshalAttrs(t, wgPage),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeCrossSiteNetworks(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeCrossSiteNetworks: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2 (network + wire group)", total, inserted)
	}

	if _, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeCrossSiteNetwork, xsnSelfLink)); err != nil {
		t.Errorf("GetResource(network): %v", err)
	}
	if _, err := st.GetResource(store.ResourceID("gcp", p.ID, TypeComputeWireGroup, wgSelfLink)); err != nil {
		t.Errorf("GetResource(wire group): %v", err)
	}
}

func TestScanComputeWireGroups_NoNetworks(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	svc := fakeComputeService(t, fakeGCPServer(t, map[string]string{}))

	total, inserted, err := scanComputeWireGroups(t.Context(), svc, p, st, testScanID, nil)
	if err != nil {
		t.Fatalf("scanComputeWireGroups (no networks): %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
