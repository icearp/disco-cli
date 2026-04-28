package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveCloudArmorRelationships verifies that backendService.securityPolicy
// + .edgeSecurityPolicy fields produce uses edges, and that pointing both to
// the same policy collapses to one edge.
func TestResolveCloudArmorRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	pol1 := "https://www.googleapis.com/compute/v1/projects/my-project/global/securityPolicies/waf-1"
	pol2 := "https://www.googleapis.com/compute/v1/projects/my-project/global/securityPolicies/edge-1"
	pol1ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSecurityPolicy, pol1, "", "{}")
	pol2ID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSecurityPolicy, pol2, "", "{}")

	bsBoth := upsertTestResource(t, st, "gcp", p.ID, TypeComputeBackendService,
		"https://www.googleapis.com/compute/v1/projects/my-project/global/backendServices/bs-1", "",
		`{"securityPolicy": "`+pol1+`", "edgeSecurityPolicy": "`+pol2+`"}`)
	bsDup := upsertTestResource(t, st, "gcp", p.ID, TypeComputeBackendService,
		"https://www.googleapis.com/compute/v1/projects/my-project/global/backendServices/bs-2", "",
		`{"securityPolicy": "`+pol1+`", "edgeSecurityPolicy": "`+pol1+`"}`)

	if err := resolveCloudArmorRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudArmorRelationships: %v", err)
	}

	// bsBoth → pol1 + pol2 (2 edges)
	rels, _ := st.RelationshipsFrom(bsBoth)
	if len(rels) != 2 {
		t.Errorf("bsBoth: expected 2 edges, got %d: %+v", len(rels), rels)
	}
	for _, r := range rels {
		if r.Kind != store.RelUses {
			t.Errorf("expected uses, got %s", r.Kind)
		}
		if r.ToID != pol1ID && r.ToID != pol2ID {
			t.Errorf("unexpected ToID %s", r.ToID)
		}
	}

	// bsDup → pol1 only (1 edge after dedup)
	relsDup, _ := st.RelationshipsFrom(bsDup)
	if len(relsDup) != 1 || relsDup[0].ToID != pol1ID {
		t.Errorf("bsDup: expected 1 edge to pol1, got %+v", relsDup)
	}
}
