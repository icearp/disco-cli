package gcp

import (
	"testing"

	compute "google.golang.org/api/compute/v1"
)

func TestResolveComputeAddressRelationships_NetworkSubnet(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{SelfLink: "projects/proj-1/global/networks/net-1", Name: "net-1"}))
	subnetID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSubnet, "projects/proj-1/regions/us-central1/subnetworks/sub-1", "us-central1",
		marshalAttrs(t, &compute.Subnetwork{SelfLink: "projects/proj-1/regions/us-central1/subnetworks/sub-1", Name: "sub-1"}))

	addrID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeAddress, "projects/proj-1/regions/us-central1/addresses/addr-1", "us-central1",
		marshalAttrs(t, &compute.Address{
			SelfLink:   "projects/proj-1/regions/us-central1/addresses/addr-1",
			Name:       "addr-1",
			Network:    "projects/proj-1/global/networks/net-1",
			Subnetwork: "projects/proj-1/regions/us-central1/subnetworks/sub-1",
		}))

	if err := resolveComputeAddressRelationships(p, st); err != nil {
		t.Fatalf("resolveComputeAddressRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(addrID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[netID] != "attached-to" || got[subnetID] != "attached-to" {
		t.Errorf("want attached-to edges to network+subnet, got %+v", rels)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveComputeAddressRelationships_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	addrID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeGlobalAddress, "projects/proj-1/global/addresses/addr-1", "",
		marshalAttrs(t, &compute.Address{
			SelfLink: "projects/proj-1/global/addresses/addr-1",
			Name:     "addr-1",
			Network:  "projects/other-proj/global/networks/not-scanned",
		}))

	if err := resolveComputeAddressRelationships(p, st); err != nil {
		t.Fatalf("resolveComputeAddressRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(addrID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned network reference, got %+v", rels)
	}
}

func TestResolveComputeAddressRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveComputeAddressRelationships(p, st); err != nil {
		t.Fatalf("resolveComputeAddressRelationships on empty project: %v", err)
	}
}

func TestResolveRouterRelationships_Network(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{SelfLink: "projects/proj-1/global/networks/net-1", Name: "net-1"}))
	routerID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRouter, "projects/proj-1/regions/us-central1/routers/rtr-1", "us-central1",
		marshalAttrs(t, &compute.Router{
			SelfLink: "projects/proj-1/regions/us-central1/routers/rtr-1",
			Name:     "rtr-1",
			Network:  "projects/proj-1/global/networks/net-1",
		}))

	if err := resolveRouterRelationships(p, st); err != nil {
		t.Fatalf("resolveRouterRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(routerID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != netID || rels[0].Kind != "attached-to" {
		t.Errorf("want single attached-to edge to network, got %+v", rels)
	}
}

func TestResolveRouterRelationships_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	routerID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRouter, "projects/proj-1/regions/us-central1/routers/rtr-1", "us-central1",
		marshalAttrs(t, &compute.Router{
			SelfLink: "projects/proj-1/regions/us-central1/routers/rtr-1",
			Name:     "rtr-1",
			Network:  "projects/other-proj/global/networks/not-scanned",
		}))

	if err := resolveRouterRelationships(p, st); err != nil {
		t.Fatalf("resolveRouterRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(routerID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned network reference, got %+v", rels)
	}
}

func TestResolveRouterRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveRouterRelationships(p, st); err != nil {
		t.Fatalf("resolveRouterRelationships on empty project: %v", err)
	}
}

func TestResolveRouteRelationships_FullChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{SelfLink: "projects/proj-1/global/networks/net-1", Name: "net-1"}))
	instID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstance, "projects/proj-1/zones/us-central1-a/instances/inst-1", "us-central1-a",
		marshalAttrs(t, &compute.Instance{SelfLink: "projects/proj-1/zones/us-central1-a/instances/inst-1", Name: "inst-1"}))

	routeID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRoute, "projects/proj-1/global/routes/route-1", "",
		marshalAttrs(t, &compute.Route{
			SelfLink:        "projects/proj-1/global/routes/route-1",
			Name:            "route-1",
			Network:         "projects/proj-1/global/networks/net-1",
			NextHopInstance: "projects/proj-1/zones/us-central1-a/instances/inst-1",
		}))

	if err := resolveRouteRelationships(p, st); err != nil {
		t.Fatalf("resolveRouteRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(routeID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[netID] != "attached-to" || got[instID] != "attached-to" {
		t.Errorf("want attached-to edges to network+instance, got %+v", rels)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveRouteRelationships_NextHopIlb(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	frID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeForwardingRule, "projects/proj-1/regions/us-central1/forwardingRules/fr-1", "us-central1",
		marshalAttrs(t, &compute.ForwardingRule{SelfLink: "projects/proj-1/regions/us-central1/forwardingRules/fr-1", Name: "fr-1"}))

	routeID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRoute, "projects/proj-1/global/routes/route-1", "",
		marshalAttrs(t, &compute.Route{
			SelfLink:   "projects/proj-1/global/routes/route-1",
			Name:       "route-1",
			NextHopIlb: "projects/proj-1/regions/us-central1/forwardingRules/fr-1",
		}))

	if err := resolveRouteRelationships(p, st); err != nil {
		t.Fatalf("resolveRouteRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(routeID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != frID || rels[0].Kind != "attached-to" {
		t.Errorf("want single attached-to edge to forwarding rule, got %+v", rels)
	}
}

func TestResolveRouteRelationships_NextHopGatewayIgnored(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	routeID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRoute, "projects/proj-1/global/routes/route-1", "",
		marshalAttrs(t, &compute.Route{
			SelfLink:       "projects/proj-1/global/routes/route-1",
			Name:           "route-1",
			NextHopGateway: "projects/proj-1/global/gateways/default-internet-gateway",
		}))

	if err := resolveRouteRelationships(p, st); err != nil {
		t.Fatalf("resolveRouteRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(routeID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for a bare NextHopGateway-only route, got %+v", rels)
	}
}

func TestResolveRouteRelationships_UnscannedNextHopSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	routeID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRoute, "projects/proj-1/global/routes/route-1", "",
		marshalAttrs(t, &compute.Route{
			SelfLink:        "projects/proj-1/global/routes/route-1",
			Name:            "route-1",
			Network:         "projects/other-proj/global/networks/not-scanned",
			NextHopInstance: "projects/other-proj/zones/us-central1-a/instances/not-scanned",
		}))

	if err := resolveRouteRelationships(p, st); err != nil {
		t.Fatalf("resolveRouteRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(routeID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when every referenced target is unscanned, got %+v", rels)
	}
}

func TestResolveRouteRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveRouteRelationships(p, st); err != nil {
		t.Fatalf("resolveRouteRelationships on empty project: %v", err)
	}
}
