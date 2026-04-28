package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveLoadBalancingRelationships covers the full chain:
// forwardingRule → targetHttpsProxy → urlMap → backendService.
func TestResolveLoadBalancingRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	const base = "https://www.googleapis.com/compute/v1/projects/my-project/global"
	bsURL := base + "/backendServices/bs-1"
	umURL := base + "/urlMaps/um-1"
	tpURL := base + "/targetHttpsProxies/tp-1"
	frURL := base + "/forwardingRules/fr-1"

	bsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeBackendService, bsURL, "", "{}")
	umID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeURLMap, umURL, "",
		`{"defaultService": "`+bsURL+`"}`)
	tpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeTargetHTTPSProxy, tpURL, "",
		`{"urlMap": "`+umURL+`"}`)
	frID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeForwardingRule, frURL, "",
		`{"target": "`+tpURL+`"}`)

	if err := resolveLoadBalancingRelationships(p, st); err != nil {
		t.Fatalf("resolveLoadBalancingRelationships: %v", err)
	}

	// fr → tp
	rels, _ := st.RelationshipsFrom(frID)
	if len(rels) != 1 || rels[0].ToID != tpID || rels[0].Kind != store.RelRoutesTo {
		t.Errorf("fr edge: got %+v", rels)
	}
	// tp → um
	rels, _ = st.RelationshipsFrom(tpID)
	if len(rels) != 1 || rels[0].ToID != umID {
		t.Errorf("tp edge: got %+v", rels)
	}
	// um → bs
	rels, _ = st.RelationshipsFrom(umID)
	if len(rels) != 1 || rels[0].ToID != bsID {
		t.Errorf("um edge: got %+v", rels)
	}
}

// TestResolveLoadBalancing_FRtoBackendService covers internal LBs that point
// a forwarding rule directly at a regional backend service (no proxy hop).
func TestResolveLoadBalancing_FRtoBackendService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	bsURL := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/backendServices/bs-int"
	frURL := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/forwardingRules/fr-int"
	bsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeBackendService, bsURL, "us-central1", "{}")
	frID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeForwardingRule, frURL, "us-central1",
		`{"backendService": "`+bsURL+`"}`)

	if err := resolveLoadBalancingRelationships(p, st); err != nil {
		t.Fatalf("resolveLoadBalancingRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(frID)
	if len(rels) != 1 || rels[0].ToID != bsID {
		t.Errorf("got %+v, want fr→bs", rels)
	}
}

// TestResolveLoadBalancing_URLMapToBackendBucket verifies CDN-fronted buckets.
func TestResolveLoadBalancing_URLMapToBackendBucket(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	bbURL := "https://www.googleapis.com/compute/v1/projects/my-project/global/backendBuckets/bb-1"
	umURL := "https://www.googleapis.com/compute/v1/projects/my-project/global/urlMaps/um-cdn"
	bbID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeBackendBucket, bbURL, "", "{}")
	umID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeURLMap, umURL, "",
		`{"defaultService": "`+bbURL+`"}`)

	if err := resolveLoadBalancingRelationships(p, st); err != nil {
		t.Fatalf("resolveLoadBalancingRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(umID)
	if len(rels) != 1 || rels[0].ToID != bbID {
		t.Errorf("got %+v, want um→bb", rels)
	}
}
