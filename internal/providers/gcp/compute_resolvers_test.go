package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveComputeInstanceRelationships verifies that a GCP instance's
// network and subnetwork are extracted from the networkInterfaces JSON array.
// GCP uses lowercase JSON keys ("network", "subnetwork") — different from AWS
// capitalization. This test locks in the casing.
func TestResolveComputeInstanceRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	networkURL := "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/default"
	subnetURL := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/default"

	attrsJSON := `{
		"networkInterfaces": [
			{
				"network":    "` + networkURL + `",
				"subnetwork": "` + subnetURL + `"
			}
		]
	}`
	instID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstance,
		"//compute.googleapis.com/projects/my-project/zones/us-central1-a/instances/inst-1", "", attrsJSON)
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

// TestResolveComputeInstanceRelationships_NetworkOnly verifies that an instance
// with only a network (no subnetwork) produces exactly one relationship.
func TestResolveComputeInstanceRelationships_NetworkOnly(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	networkURL := "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/default"
	attrsJSON := `{"networkInterfaces": [{"network": "` + networkURL + `"}]}`
	instID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstance,
		"//compute.googleapis.com/projects/my-project/zones/us-central1-a/instances/inst-2", "", attrsJSON)
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

// TestResolveSubnetworkRelationships verifies that a subnet's parent network
// is derived from its "network" JSON field.
func TestResolveSubnetworkRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	networkURL := "https://www.googleapis.com/compute/v1/projects/my-project/global/networks/default"
	subnetURL := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/subnetworks/default"
	attrsJSON := `{"network": "` + networkURL + `"}`

	subnetID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSubnet, subnetURL, "", attrsJSON)
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
