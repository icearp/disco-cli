package gcp

import (
	"testing"

	compute "google.golang.org/api/compute/v1"
)

func TestResolveVpnRelationships_FullChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{SelfLink: "projects/proj-1/global/networks/net-1", Name: "net-1"}))
	routerID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRouter, "projects/proj-1/regions/us-central1/routers/rtr-1", "us-central1",
		marshalAttrs(t, &compute.Router{SelfLink: "projects/proj-1/regions/us-central1/routers/rtr-1", Name: "rtr-1"}))

	vgID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeVpnGateway, "projects/proj-1/regions/us-central1/vpnGateways/vg-1", "us-central1",
		marshalAttrs(t, &compute.VpnGateway{SelfLink: "projects/proj-1/regions/us-central1/vpnGateways/vg-1", Name: "vg-1", Network: "projects/proj-1/global/networks/net-1"}))

	vtID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeVpnTunnel, "projects/proj-1/regions/us-central1/vpnTunnels/vt-1", "us-central1",
		marshalAttrs(t, &compute.VpnTunnel{
			SelfLink:   "projects/proj-1/regions/us-central1/vpnTunnels/vt-1",
			Name:       "vt-1",
			VpnGateway: "projects/proj-1/regions/us-central1/vpnGateways/vg-1",
			Router:     "projects/proj-1/regions/us-central1/routers/rtr-1",
		}))

	if err := resolveVpnRelationships(p, st); err != nil {
		t.Fatalf("resolveVpnRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(vgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(vgID): %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != netID || rels[0].Kind != "attached-to" {
		t.Errorf("want vpnGateway→network edge, got %+v", rels)
	}

	rels, err = st.RelationshipsFrom(vtID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(vtID): %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[vgID] != "attached-to" || got[routerID] != "attached-to" {
		t.Errorf("want vpnTunnel→vpnGateway+router edges, got %+v", rels)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveVpnRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	vgID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeVpnGateway, "projects/proj-1/regions/us-central1/vpnGateways/vg-1", "us-central1",
		marshalAttrs(t, &compute.VpnGateway{SelfLink: "projects/proj-1/regions/us-central1/vpnGateways/vg-1", Name: "vg-1", Network: "projects/other-proj/global/networks/not-scanned"}))

	if err := resolveVpnRelationships(p, st); err != nil {
		t.Fatalf("resolveVpnRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(vgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned network reference, got %+v", rels)
	}
}

func TestResolveVpnRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveVpnRelationships(p, st); err != nil {
		t.Fatalf("resolveVpnRelationships on empty project: %v", err)
	}
}

func TestResolveInterconnectRelationships_AttachmentToInterconnectAndRouter(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	icID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInterconnect, "projects/proj-1/global/interconnects/ic-1", "",
		marshalAttrs(t, &compute.Interconnect{SelfLink: "projects/proj-1/global/interconnects/ic-1", Name: "ic-1"}))
	routerID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRouter, "projects/proj-1/regions/us-central1/routers/rtr-1", "us-central1",
		marshalAttrs(t, &compute.Router{SelfLink: "projects/proj-1/regions/us-central1/routers/rtr-1", Name: "rtr-1"}))

	attID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInterconnectAttachment, "projects/proj-1/regions/us-central1/interconnectAttachments/att-1", "us-central1",
		marshalAttrs(t, &compute.InterconnectAttachment{
			SelfLink:     "projects/proj-1/regions/us-central1/interconnectAttachments/att-1",
			Name:         "att-1",
			Interconnect: "projects/proj-1/global/interconnects/ic-1",
			Router:       "projects/proj-1/regions/us-central1/routers/rtr-1",
		}))

	if err := resolveInterconnectRelationships(p, st); err != nil {
		t.Fatalf("resolveInterconnectRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(attID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[icID] != "attached-to" || got[routerID] != "attached-to" {
		t.Errorf("want interconnectAttachment→interconnect+router edges, got %+v", rels)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveInterconnectRelationships_AttachmentGroupToMembers(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	att1ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInterconnectAttachment, "projects/proj-1/regions/us-central1/interconnectAttachments/att-1", "us-central1",
		marshalAttrs(t, &compute.InterconnectAttachment{SelfLink: "projects/proj-1/regions/us-central1/interconnectAttachments/att-1", Name: "att-1"}))
	att2ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInterconnectAttachment, "projects/proj-1/regions/us-east1/interconnectAttachments/att-2", "us-east1",
		marshalAttrs(t, &compute.InterconnectAttachment{SelfLink: "projects/proj-1/regions/us-east1/interconnectAttachments/att-2", Name: "att-2"}))

	agID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInterconnectAttachmentGroup, "projects/proj-1/global/interconnectAttachmentGroups/ag-1", "",
		marshalAttrs(t, &compute.InterconnectAttachmentGroup{
			SelfLink: "projects/proj-1/global/interconnectAttachmentGroups/ag-1",
			Name:     "ag-1",
			Attachments: map[string]compute.InterconnectAttachmentGroupAttachment{
				"att-1": {Attachment: "projects/proj-1/regions/us-central1/interconnectAttachments/att-1"},
				"att-2": {Attachment: "projects/proj-1/regions/us-east1/interconnectAttachments/att-2"},
			},
		}))

	if err := resolveInterconnectRelationships(p, st); err != nil {
		t.Fatalf("resolveInterconnectRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(agID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[att1ID] != "attached-to" || got[att2ID] != "attached-to" {
		t.Errorf("want attachmentGroup→both member edges, got %+v", rels)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveInterconnectRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	attID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInterconnectAttachment, "projects/proj-1/regions/us-central1/interconnectAttachments/att-1", "us-central1",
		marshalAttrs(t, &compute.InterconnectAttachment{
			SelfLink:     "projects/proj-1/regions/us-central1/interconnectAttachments/att-1",
			Name:         "att-1",
			Interconnect: "projects/proj-1/global/interconnects/not-scanned",
			Router:       "projects/proj-1/regions/us-central1/routers/not-scanned",
		}))

	agID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInterconnectAttachmentGroup, "projects/proj-1/global/interconnectAttachmentGroups/ag-1", "",
		marshalAttrs(t, &compute.InterconnectAttachmentGroup{
			SelfLink: "projects/proj-1/global/interconnectAttachmentGroups/ag-1",
			Name:     "ag-1",
			Attachments: map[string]compute.InterconnectAttachmentGroupAttachment{
				"missing": {Attachment: "projects/proj-1/regions/us-central1/interconnectAttachments/not-scanned"},
			},
		}))

	igID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInterconnectGroup, "projects/proj-1/global/interconnectGroups/ig-1", "",
		marshalAttrs(t, &compute.InterconnectGroup{
			SelfLink: "projects/proj-1/global/interconnectGroups/ig-1",
			Name:     "ig-1",
			Interconnects: map[string]compute.InterconnectGroupInterconnect{
				"missing": {Interconnect: "projects/proj-1/global/interconnects/not-scanned"},
			},
		}))

	wgID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeWireGroup, "projects/proj-1/regions/us-central1/wireGroups/wg-1", "us-central1",
		marshalAttrs(t, &compute.WireGroup{
			SelfLink: "projects/proj-1/regions/us-central1/wireGroups/wg-1",
			Name:     "wg-1",
			Wires: []*compute.Wire{
				{Endpoints: []*compute.WireEndpoint{{Interconnect: "projects/proj-1/global/interconnects/not-scanned"}}},
			},
		}))

	nessID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetworkEdgeSecurityService, "projects/proj-1/regions/us-central1/networkEdgeSecurityServices/ness-1", "us-central1",
		marshalAttrs(t, &compute.NetworkEdgeSecurityService{
			SelfLink:       "projects/proj-1/regions/us-central1/networkEdgeSecurityServices/ness-1",
			Name:           "ness-1",
			SecurityPolicy: "projects/proj-1/global/securityPolicies/not-scanned",
		}))

	if err := resolveInterconnectRelationships(p, st); err != nil {
		t.Fatalf("resolveInterconnectRelationships: %v", err)
	}

	for name, id := range map[string]string{
		"attachment": attID, "attachmentGroup": agID, "interconnectGroup": igID,
		"wireGroup": wgID, "networkEdgeSecurityService": nessID,
	} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", name, err)
		}
		if len(rels) != 0 {
			t.Errorf("%s: want no edges for unscanned references, got %+v", name, rels)
		}
	}
}

func TestResolveInterconnectRelationships_GroupAndWireGroupToInterconnect(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	icID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInterconnect, "projects/proj-1/global/interconnects/ic-1", "",
		marshalAttrs(t, &compute.Interconnect{SelfLink: "projects/proj-1/global/interconnects/ic-1", Name: "ic-1"}))

	igID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInterconnectGroup, "projects/proj-1/global/interconnectGroups/ig-1", "",
		marshalAttrs(t, &compute.InterconnectGroup{
			SelfLink: "projects/proj-1/global/interconnectGroups/ig-1",
			Name:     "ig-1",
			Interconnects: map[string]compute.InterconnectGroupInterconnect{
				"ic-1": {Interconnect: "projects/proj-1/global/interconnects/ic-1"},
			},
		}))

	ic2ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInterconnect, "projects/proj-1/global/interconnects/ic-2", "",
		marshalAttrs(t, &compute.Interconnect{SelfLink: "projects/proj-1/global/interconnects/ic-2", Name: "ic-2"}))

	wgID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeWireGroup, "projects/proj-1/regions/us-central1/wireGroups/wg-1", "us-central1",
		marshalAttrs(t, &compute.WireGroup{
			SelfLink: "projects/proj-1/regions/us-central1/wireGroups/wg-1",
			Name:     "wg-1",
			Wires: []*compute.Wire{
				{Endpoints: []*compute.WireEndpoint{{Interconnect: "projects/proj-1/global/interconnects/ic-1"}}},
				{Endpoints: []*compute.WireEndpoint{{Interconnect: "projects/proj-1/global/interconnects/ic-2"}}},
			},
		}))

	if err := resolveInterconnectRelationships(p, st); err != nil {
		t.Fatalf("resolveInterconnectRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(igID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(igID): %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != icID {
		t.Errorf("want interconnectGroup→interconnect edge, got %+v", rels)
	}

	rels, err = st.RelationshipsFrom(wgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(wgID): %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[icID] != "attached-to" || got[ic2ID] != "attached-to" {
		t.Errorf("want wireGroup→both wire-endpoint interconnect edges, got %+v", rels)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges (2 wires × 1 endpoint each), got %d: %+v", len(rels), rels)
	}
}

func TestResolveInterconnectRelationships_NetworkEdgeSecurityServiceToSecurityPolicy(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	spID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSecurityPolicy, "projects/proj-1/global/securityPolicies/sp-1", "",
		marshalAttrs(t, &compute.SecurityPolicy{SelfLink: "projects/proj-1/global/securityPolicies/sp-1", Name: "sp-1"}))

	nessID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetworkEdgeSecurityService, "projects/proj-1/regions/us-central1/networkEdgeSecurityServices/ness-1", "us-central1",
		marshalAttrs(t, &compute.NetworkEdgeSecurityService{
			SelfLink:       "projects/proj-1/regions/us-central1/networkEdgeSecurityServices/ness-1",
			Name:           "ness-1",
			SecurityPolicy: "projects/proj-1/global/securityPolicies/sp-1",
		}))

	if err := resolveInterconnectRelationships(p, st); err != nil {
		t.Fatalf("resolveInterconnectRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(nessID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != spID || rels[0].Kind != "uses" {
		t.Errorf("want networkEdgeSecurityService→securityPolicy edge, got %+v", rels)
	}
}

func TestResolveInterconnectRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveInterconnectRelationships(p, st); err != nil {
		t.Fatalf("resolveInterconnectRelationships on empty project: %v", err)
	}
}
