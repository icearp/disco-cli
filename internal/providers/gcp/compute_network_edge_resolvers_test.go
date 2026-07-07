package gcp

import (
	"testing"

	compute "google.golang.org/api/compute/v1"
)

func TestResolveNetworkRelationships_FirewallPolicyAndPeering(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	fpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetworkFirewallPolicy, "projects/proj-1/global/firewallPolicies/fp-1", "",
		marshalAttrs(t, &compute.FirewallPolicy{SelfLink: "projects/proj-1/global/firewallPolicies/fp-1", Name: "fp-1"}))

	net2ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-2", "",
		marshalAttrs(t, &compute.Network{SelfLink: "projects/proj-1/global/networks/net-2", Name: "net-2"}))

	net1ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{
			SelfLink:       "projects/proj-1/global/networks/net-1",
			Name:           "net-1",
			FirewallPolicy: "projects/proj-1/global/firewallPolicies/fp-1",
			Peerings: []*compute.NetworkPeering{
				{Network: "projects/proj-1/global/networks/net-2"},
			},
		}))

	if err := resolveNetworkRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(net1ID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[fpID] != "attached-to" || got[net2ID] != "attached-to" {
		t.Errorf("want network->firewallPolicy+peer edges, got %+v", rels)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveNetworkRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{
			SelfLink:       "projects/proj-1/global/networks/net-1",
			Name:           "net-1",
			FirewallPolicy: "projects/proj-1/global/firewallPolicies/not-scanned",
			Peerings: []*compute.NetworkPeering{
				{Network: "projects/proj-1/global/networks/not-scanned"},
			},
		}))

	if err := resolveNetworkRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(netID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned references, got %+v", rels)
	}
}

func TestResolveNetworkRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveNetworkRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkRelationships on empty project: %v", err)
	}
}

func TestResolveNetworkFirewallPolicyRelationships_ToNetwork(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{SelfLink: "projects/proj-1/global/networks/net-1", Name: "net-1"}))

	fpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetworkFirewallPolicy, "projects/proj-1/global/firewallPolicies/fp-1", "",
		marshalAttrs(t, &compute.FirewallPolicy{
			SelfLink: "projects/proj-1/global/firewallPolicies/fp-1",
			Name:     "fp-1",
			Associations: []*compute.FirewallPolicyAssociation{
				{AttachmentTarget: "projects/proj-1/global/networks/net-1"},
			},
		}))

	regionFpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionNetworkFirewallPolicy, "projects/proj-1/regions/us-central1/firewallPolicies/rfp-1", "us-central1",
		marshalAttrs(t, &compute.FirewallPolicy{
			SelfLink: "projects/proj-1/regions/us-central1/firewallPolicies/rfp-1",
			Name:     "rfp-1",
			Region:   "us-central1",
			Associations: []*compute.FirewallPolicyAssociation{
				{AttachmentTarget: "projects/proj-1/global/networks/net-1"},
			},
		}))

	if err := resolveNetworkFirewallPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkFirewallPolicyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(fpID): %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != netID || rels[0].Kind != "attached-to" {
		t.Errorf("want firewallPolicy->network edge, got %+v", rels)
	}

	rels, err = st.RelationshipsFrom(regionFpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(regionFpID): %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != netID || rels[0].Kind != "attached-to" {
		t.Errorf("want regionFirewallPolicy->network edge, got %+v", rels)
	}
}

func TestResolveNetworkFirewallPolicyRelationships_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	fpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetworkFirewallPolicy, "projects/proj-1/global/firewallPolicies/fp-1", "",
		marshalAttrs(t, &compute.FirewallPolicy{
			SelfLink: "projects/proj-1/global/firewallPolicies/fp-1",
			Name:     "fp-1",
			Associations: []*compute.FirewallPolicyAssociation{
				{AttachmentTarget: "projects/proj-1/global/networks/not-scanned"},
			},
		}))

	if err := resolveNetworkFirewallPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkFirewallPolicyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned network, got %+v", rels)
	}
}

func TestResolveNetworkAttachmentRelationships_ToNetworkAndSubnets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{SelfLink: "projects/proj-1/global/networks/net-1", Name: "net-1"}))
	sub1ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSubnet, "projects/proj-1/regions/us-central1/subnetworks/sub-1", "us-central1",
		marshalAttrs(t, &compute.Subnetwork{SelfLink: "projects/proj-1/regions/us-central1/subnetworks/sub-1", Name: "sub-1"}))

	naID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetworkAttachment, "projects/proj-1/regions/us-central1/networkAttachments/na-1", "us-central1",
		marshalAttrs(t, &compute.NetworkAttachment{
			SelfLink:    "projects/proj-1/regions/us-central1/networkAttachments/na-1",
			Name:        "na-1",
			Network:     "projects/proj-1/global/networks/net-1",
			Subnetworks: []string{"projects/proj-1/regions/us-central1/subnetworks/sub-1"},
		}))

	if err := resolveNetworkAttachmentRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkAttachmentRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(naID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[netID] != "attached-to" || got[sub1ID] != "attached-to" {
		t.Errorf("want networkAttachment->network+subnet edges, got %+v", rels)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveNetworkAttachmentRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	naID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetworkAttachment, "projects/proj-1/regions/us-central1/networkAttachments/na-1", "us-central1",
		marshalAttrs(t, &compute.NetworkAttachment{
			SelfLink:    "projects/proj-1/regions/us-central1/networkAttachments/na-1",
			Name:        "na-1",
			Network:     "projects/proj-1/global/networks/not-scanned",
			Subnetworks: []string{"projects/proj-1/regions/us-central1/subnetworks/not-scanned"},
		}))

	if err := resolveNetworkAttachmentRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkAttachmentRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(naID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned references, got %+v", rels)
	}
}

func TestResolveServiceAttachmentRelationships_ToForwardingRule(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	frID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeForwardingRule, "projects/proj-1/regions/us-central1/forwardingRules/fr-1", "us-central1",
		marshalAttrs(t, &compute.ForwardingRule{SelfLink: "projects/proj-1/regions/us-central1/forwardingRules/fr-1", Name: "fr-1"}))

	saID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeServiceAttachment, "projects/proj-1/regions/us-central1/serviceAttachments/sa-1", "us-central1",
		marshalAttrs(t, &compute.ServiceAttachment{
			SelfLink:               "projects/proj-1/regions/us-central1/serviceAttachments/sa-1",
			Name:                   "sa-1",
			ProducerForwardingRule: "projects/proj-1/regions/us-central1/forwardingRules/fr-1",
		}))

	if err := resolveServiceAttachmentRelationships(p, st); err != nil {
		t.Fatalf("resolveServiceAttachmentRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(saID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != frID || rels[0].Kind != "attached-to" {
		t.Errorf("want serviceAttachment->forwardingRule edge, got %+v", rels)
	}
}

func TestResolveServiceAttachmentRelationships_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	saID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeServiceAttachment, "projects/proj-1/regions/us-central1/serviceAttachments/sa-1", "us-central1",
		marshalAttrs(t, &compute.ServiceAttachment{
			SelfLink:               "projects/proj-1/regions/us-central1/serviceAttachments/sa-1",
			Name:                   "sa-1",
			ProducerForwardingRule: "projects/proj-1/regions/us-central1/forwardingRules/not-scanned",
		}))

	if err := resolveServiceAttachmentRelationships(p, st); err != nil {
		t.Fatalf("resolveServiceAttachmentRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(saID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned forwarding rule, got %+v", rels)
	}
}

func TestResolveRegionCommitmentRelationships_ToReservations(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	res1ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeReservation, "projects/proj-1/zones/us-central1-a/reservations/res-1", "us-central1",
		marshalAttrs(t, &compute.Reservation{SelfLink: "projects/proj-1/zones/us-central1-a/reservations/res-1", Name: "res-1"}))
	res2ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeReservation, "projects/proj-1/zones/us-central1-a/reservations/res-2", "us-central1",
		marshalAttrs(t, &compute.Reservation{SelfLink: "projects/proj-1/zones/us-central1-a/reservations/res-2", Name: "res-2"}))

	rcID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionCommitment, "projects/proj-1/regions/us-central1/commitments/rc-1", "us-central1",
		marshalAttrs(t, &compute.Commitment{
			SelfLink: "projects/proj-1/regions/us-central1/commitments/rc-1",
			Name:     "rc-1",
			ExistingReservations: []string{
				"projects/proj-1/zones/us-central1-a/reservations/res-1",
				"projects/proj-1/zones/us-central1-a/reservations/res-2",
			},
		}))

	if err := resolveRegionCommitmentRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionCommitmentRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(rcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[res1ID] != "attached-to" || got[res2ID] != "attached-to" {
		t.Errorf("want regionCommitment->both reservation edges, got %+v", rels)
	}
	if len(rels) != 2 {
		t.Errorf("want exactly 2 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveRegionCommitmentRelationships_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	rcID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionCommitment, "projects/proj-1/regions/us-central1/commitments/rc-1", "us-central1",
		marshalAttrs(t, &compute.Commitment{
			SelfLink:             "projects/proj-1/regions/us-central1/commitments/rc-1",
			Name:                 "rc-1",
			ExistingReservations: []string{"projects/proj-1/zones/us-central1-a/reservations/not-scanned"},
		}))

	if err := resolveRegionCommitmentRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionCommitmentRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(rcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned reservation, got %+v", rels)
	}
}

func TestResolveNodeGroupRelationships_ToNodeTemplate(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	ntID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNodeTemplate, "projects/proj-1/regions/us-central1/nodeTemplates/nt-1", "us-central1",
		marshalAttrs(t, &compute.NodeTemplate{SelfLink: "projects/proj-1/regions/us-central1/nodeTemplates/nt-1", Name: "nt-1"}))

	ngID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNodeGroup, "projects/proj-1/zones/us-central1-a/nodeGroups/ng-1", "us-central1",
		marshalAttrs(t, &compute.NodeGroup{
			SelfLink:     "projects/proj-1/zones/us-central1-a/nodeGroups/ng-1",
			Name:         "ng-1",
			NodeTemplate: "projects/proj-1/regions/us-central1/nodeTemplates/nt-1",
		}))

	if err := resolveNodeGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveNodeGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ngID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != ntID || rels[0].Kind != "uses" {
		t.Errorf("want nodeGroup->nodeTemplate edge, got %+v", rels)
	}
}

func TestResolveNodeGroupRelationships_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	ngID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNodeGroup, "projects/proj-1/zones/us-central1-a/nodeGroups/ng-1", "us-central1",
		marshalAttrs(t, &compute.NodeGroup{
			SelfLink:     "projects/proj-1/zones/us-central1-a/nodeGroups/ng-1",
			Name:         "ng-1",
			NodeTemplate: "projects/proj-1/regions/us-central1/nodeTemplates/not-scanned",
		}))

	if err := resolveNodeGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveNodeGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ngID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned node template, got %+v", rels)
	}
}

func TestResolveNetworkEdgeResolvers_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveNetworkFirewallPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkFirewallPolicyRelationships on empty project: %v", err)
	}
	if err := resolveNetworkAttachmentRelationships(p, st); err != nil {
		t.Fatalf("resolveNetworkAttachmentRelationships on empty project: %v", err)
	}
	if err := resolveServiceAttachmentRelationships(p, st); err != nil {
		t.Fatalf("resolveServiceAttachmentRelationships on empty project: %v", err)
	}
	if err := resolveRegionCommitmentRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionCommitmentRelationships on empty project: %v", err)
	}
	if err := resolveNodeGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveNodeGroupRelationships on empty project: %v", err)
	}
}
