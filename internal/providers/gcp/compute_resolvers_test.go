package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

// TestResolveComputeInstanceRelationships verifies a GCP instance's network
// and subnetwork are extracted from the networkInterfaces JSON array, locking
// in GCP's lowercase JSON keys ("network", "subnetwork") vs AWS's capitalization.
func TestResolveComputeInstanceRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	networkURL := "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/default"
	subnetURL := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/default"

	inst := compute.Instance{NetworkInterfaces: []*compute.NetworkInterface{{
		Network:    networkURL,
		Subnetwork: subnetURL,
	}}}
	instID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstance,
		"//compute.googleapis.com/projects/my-project/zones/us-central1-a/instances/inst-1", "", marshalAttrs(t, inst))
	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, networkURL, "", "{}")
	subnetID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSubnet, subnetURL, "", "{}")

	if err := resolveComputeInstanceRelationships(p, st); err != nil {
		t.Fatalf("resolveComputeInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships (network + subnetwork), got %d", len(rels))
	}

	assertGCPRelationship(t, rels, instID, netID, store.RelAttachedTo)
	assertGCPRelationship(t, rels, instID, subnetID, store.RelAttachedTo)
}

// TestResolveComputeInstanceRelationships_NetworkOnly verifies an instance
// with only a network (no subnetwork) produces exactly one relationship.
func TestResolveComputeInstanceRelationships_NetworkOnly(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	networkURL := "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/default"
	inst := compute.Instance{NetworkInterfaces: []*compute.NetworkInterface{{Network: networkURL}}}
	instID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstance,
		"//compute.googleapis.com/projects/my-project/zones/us-central1-a/instances/inst-2", "", marshalAttrs(t, inst))
	upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, networkURL, "", "{}")

	if err := resolveComputeInstanceRelationships(p, st); err != nil {
		t.Fatalf("resolveComputeInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
}

// TestResolveComputeInstanceRelationships_EmptyAttrs verifies no error
// when an instance has no network interfaces.
func TestResolveComputeInstanceRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	instID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstance,
		"//compute.googleapis.com/projects/my-project/zones/us-central1-a/instances/bare", "", "{}")

	if err := resolveComputeInstanceRelationships(p, st); err != nil {
		t.Fatalf("resolveComputeInstanceRelationships (empty): %v", err)
	}
	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveSubnetworkRelationships verifies a subnet's parent network is
// derived from its "network" JSON field.
func TestResolveSubnetworkRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	networkURL := "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/default"
	subnetURL := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/default"
	subnet := compute.Subnetwork{Network: networkURL}

	subnetID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSubnet, subnetURL, "", marshalAttrs(t, subnet))
	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, networkURL, "", "{}")

	if err := resolveSubnetworkRelationships(p, st); err != nil {
		t.Fatalf("resolveSubnetworkRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(subnetID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != netID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected subnet -[attached-to]-> network, got %+v", rels[0])
	}
}

// assertGCPRelationship fails the test if no matching relationship exists.
func assertGCPRelationship(t *testing.T, rels []store.Relationship, fromID, toID, kind string) {
	t.Helper()
	for _, r := range rels {
		if r.FromID == fromID && r.ToID == toID && r.Kind == kind {
			return
		}
	}
	t.Errorf("missing relationship: %s -[%s]-> %s", fromID, kind, toID)
}
