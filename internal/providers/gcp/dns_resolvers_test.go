package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveDNSRelationships verifies A-record → forwarding-rule via IP
// match, AAAA support, and that non-A/AAAA records (TXT) emit no edges.
func TestResolveDNSRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	frV4ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeForwardingRule,
		"https://www.googleapis.com/compute/v1/projects/my-project/global/forwardingRules/fr-v4", "",
		`{"IPAddress": "203.0.113.10"}`)
	frV6ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeForwardingRule,
		"https://www.googleapis.com/compute/v1/projects/my-project/global/forwardingRules/fr-v6", "",
		`{"IPAddress": "2001:db8::1"}`)

	zone := "projects/my-project/managedZones/example-com"
	rrA := upsertTestResource(t, st, "gcp", p.ID, TypeDNSRecordSet, zone+"/rrsets/A/www.example.com.", "",
		`{"type": "A", "rrdatas": ["203.0.113.10"]}`)
	rrAAAA := upsertTestResource(t, st, "gcp", p.ID, TypeDNSRecordSet, zone+"/rrsets/AAAA/www.example.com.", "",
		`{"type": "AAAA", "rrdatas": ["2001:db8::1"]}`)
	rrTXT := upsertTestResource(t, st, "gcp", p.ID, TypeDNSRecordSet, zone+"/rrsets/TXT/www.example.com.", "",
		`{"type": "TXT", "rrdatas": ["v=spf1 -all"]}`)

	if err := resolveDNSRelationships(p, st); err != nil {
		t.Fatalf("resolveDNSRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(rrA); len(rels) != 1 || rels[0].ToID != frV4ID || rels[0].Kind != store.RelRoutesTo {
		t.Errorf("A: got %+v, want →fr-v4", rels)
	}
	if rels, _ := st.RelationshipsFrom(rrAAAA); len(rels) != 1 || rels[0].ToID != frV6ID {
		t.Errorf("AAAA: got %+v, want →fr-v6", rels)
	}
	if rels, _ := st.RelationshipsFrom(rrTXT); len(rels) != 0 {
		t.Errorf("TXT: expected no edges, got %+v", rels)
	}
}

const (
	testNetworkSelfLink     = "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/net-1"
	testPeerNetworkSelfLink = "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/net-2"
)

func TestResolveDNSManagedZoneRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, testNetworkSelfLink, "", "{}")
	peerNetID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, testPeerNetworkSelfLink, "", "{}")

	zoneID := upsertTestResource(t, st, "gcp", p.ID, TypeDNSManagedZone,
		"projects/my-project/managedZones/private-zone", "",
		`{"privateVisibilityConfig": {"networks": [{"networkUrl": "`+testNetworkSelfLink+`"}]},
		  "peeringConfig": {"targetNetwork": {"networkUrl": "`+testPeerNetworkSelfLink+`"}}}`)

	if err := resolveDNSManagedZoneRelationships(p, st); err != nil {
		t.Fatalf("resolveDNSManagedZoneRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(zoneID)
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[netID] != store.RelAttachedTo || got[peerNetID] != store.RelUses || len(rels) != 2 {
		t.Errorf("managed zone: got %+v, want →net-1 attached-to + →net-2 uses", rels)
	}
}

func TestResolveDNSManagedZoneRelationships_NoBindingsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, testNetworkSelfLink, "", "{}")
	zoneID := upsertTestResource(t, st, "gcp", p.ID, TypeDNSManagedZone,
		"projects/my-project/managedZones/public-zone", "", "{}")

	if err := resolveDNSManagedZoneRelationships(p, st); err != nil {
		t.Fatalf("resolveDNSManagedZoneRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(zoneID)
	if len(rels) != 0 {
		t.Errorf("public zone: want no edges, got %+v", rels)
	}
}

func TestResolveDNSPolicyRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, testNetworkSelfLink, "", "{}")
	policyID := upsertTestResource(t, st, "gcp", p.ID, TypeDNSPolicy,
		"projects/my-project/policies/my-policy", "",
		`{"networks": [{"networkUrl": "`+testNetworkSelfLink+`"}]}`)

	if err := resolveDNSPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveDNSPolicyRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(policyID)
	if len(rels) != 1 || rels[0].ToID != netID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("policy: got %+v, want →network attached-to", rels)
	}
}

func TestResolveDNSResponsePolicyRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, testNetworkSelfLink, "", "{}")
	rpID := upsertTestResource(t, st, "gcp", p.ID, TypeDNSResponsePolicy,
		"projects/my-project/responsePolicies/my-rp", "",
		`{"networks": [{"networkUrl": "`+testNetworkSelfLink+`"}]}`)

	if err := resolveDNSResponsePolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveDNSResponsePolicyRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rpID)
	if len(rels) != 1 || rels[0].ToID != netID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("response policy: got %+v, want →network attached-to", rels)
	}
}

func TestResolveDNSNetworkBindingResolvers_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveDNSManagedZoneRelationships(p, st); err != nil {
		t.Fatalf("resolveDNSManagedZoneRelationships on empty project: %v", err)
	}
	if err := resolveDNSPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveDNSPolicyRelationships on empty project: %v", err)
	}
	if err := resolveDNSResponsePolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveDNSResponsePolicyRelationships on empty project: %v", err)
	}
}
