package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveFirewallRelationships covers all three behaviors of the
// resolver: firewall→network, firewall→instance via tag intersection, and
// no edge when networks differ.
func TestResolveFirewallRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	netA := "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/vpc-a"
	netB := "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/vpc-b"
	netAID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, netA, "", "{}")

	fwTagged := upsertTestResource(t, st, "gcp", p.ID, TypeComputeFirewall,
		"https://www.googleapis.com/compute/v1/projects/my-project/global/firewalls/fw-tagged", "",
		`{"network": "`+netA+`", "targetTags": ["web", "ssh"]}`)
	fwAll := upsertTestResource(t, st, "gcp", p.ID, TypeComputeFirewall,
		"https://www.googleapis.com/compute/v1/projects/my-project/global/firewalls/fw-all", "",
		`{"network": "`+netA+`"}`)

	// Instance in netA with matching tag — must edge to fwTagged but NOT fwAll.
	matchingInst := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstance,
		"//compute.googleapis.com/projects/my-project/zones/us-central1-a/instances/web-1", "",
		`{"tags": {"items": ["web"]}, "networkInterfaces": [{"network": "`+netA+`"}]}`)
	// Instance in netB — wrong network, no edge from fwTagged.
	upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstance,
		"//compute.googleapis.com/projects/my-project/zones/us-central1-a/instances/web-2", "",
		`{"tags": {"items": ["web"]}, "networkInterfaces": [{"network": "`+netB+`"}]}`)

	if err := resolveFirewallRelationships(p, st); err != nil {
		t.Fatalf("resolveFirewallRelationships: %v", err)
	}

	// fwTagged → netA (attached-to) and fwTagged → matchingInst (uses).
	rels, err := st.RelationshipsFrom(fwTagged)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges from fwTagged, got %d: %+v", len(rels), rels)
	}
	var sawNet, sawInst bool
	for _, r := range rels {
		switch {
		case r.ToID == netAID && r.Kind == store.RelAttachedTo:
			sawNet = true
		case r.ToID == matchingInst && r.Kind == store.RelUses:
			sawInst = true
		}
	}
	if !sawNet || !sawInst {
		t.Errorf("missing expected edge — sawNet=%v sawInst=%v: %+v", sawNet, sawInst, rels)
	}

	// fwAll has no targetTags — only the network edge, no per-instance edges.
	relsAll, _ := st.RelationshipsFrom(fwAll)
	if len(relsAll) != 1 {
		t.Errorf("expected 1 edge from fwAll (network only), got %d", len(relsAll))
	}
}
