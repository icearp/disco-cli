package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
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

// TestResolveLoadBalancing_ForwardingRuleToTargetSslProxy covers SSL Proxy LB
// forwarding rules, whose target points at a targetSslProxy rather than an
// HTTP(S) proxy — a distinct code path the initial index build missed.
func TestResolveLoadBalancing_ForwardingRuleToTargetSslProxy(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	const base = "https://www.googleapis.com/compute/v1/projects/my-project/global"
	tpURL := base + "/targetSslProxies/tp-1"
	frURL := base + "/globalForwardingRules/fr-1"

	tpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeTargetSslProxy, tpURL, "", "{}")
	frID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeGlobalForwardingRule, frURL, "",
		`{"target": "`+tpURL+`"}`)

	if err := resolveLoadBalancingRelationships(p, st); err != nil {
		t.Fatalf("resolveLoadBalancingRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(frID)
	if len(rels) != 1 || rels[0].ToID != tpID {
		t.Errorf("got %+v, want globalForwardingRule→targetSslProxy", rels)
	}
}

// TestResolveLoadBalancing_GlobalForwardingRule covers the global forwarding
// rule variant, which reuses the same ForwardingRule struct/fields as the
// regional type.
func TestResolveLoadBalancing_GlobalForwardingRule(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	const base = "https://www.googleapis.com/compute/v1/projects/my-project/global"
	tpURL := base + "/targetHttpProxies/tp-1"
	frURL := base + "/globalForwardingRules/fr-1"

	tpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeTargetHTTPProxy, tpURL, "", "{}")
	frID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeGlobalForwardingRule, frURL, "",
		`{"target": "`+tpURL+`"}`)

	if err := resolveLoadBalancingRelationships(p, st); err != nil {
		t.Fatalf("resolveLoadBalancingRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(frID)
	if len(rels) != 1 || rels[0].ToID != tpID {
		t.Errorf("got %+v, want globalForwardingRule→targetHttpProxy", rels)
	}
}

// TestResolveLoadBalancing_TargetTcpProxyToBackendService covers TCP/SSL
// proxies, which route directly to a backendService with no urlMap hop.
func TestResolveLoadBalancing_TargetTcpProxyToBackendService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	const base = "https://www.googleapis.com/compute/v1/projects/my-project/global"
	bsURL := base + "/backendServices/bs-1"
	tpURL := base + "/targetTcpProxies/tp-1"

	bsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeBackendService, bsURL, "", "{}")
	tpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeTargetTCPProxy, tpURL, "",
		`{"service": "`+bsURL+`"}`)

	if err := resolveLoadBalancingRelationships(p, st); err != nil {
		t.Fatalf("resolveLoadBalancingRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tpID)
	if len(rels) != 1 || rels[0].ToID != bsID {
		t.Errorf("got %+v, want targetTcpProxy→backendService", rels)
	}
}

// TestResolveLoadBalancing_TargetGrpcProxyToURLMap covers gRPC proxies, which
// route through a urlMap like HTTP(S) proxies rather than directly like
// SSL/TCP proxies.
func TestResolveLoadBalancing_TargetGrpcProxyToURLMap(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	const base = "https://www.googleapis.com/compute/v1/projects/my-project/global"
	umURL := base + "/urlMaps/um-1"
	tpURL := base + "/targetGrpcProxies/tp-1"

	umID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeURLMap, umURL, "", "{}")
	tpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeTargetGrpcProxy, tpURL, "",
		`{"urlMap": "`+umURL+`"}`)

	if err := resolveLoadBalancingRelationships(p, st); err != nil {
		t.Fatalf("resolveLoadBalancingRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tpID)
	if len(rels) != 1 || rels[0].ToID != umID {
		t.Errorf("got %+v, want targetGrpcProxy→urlMap", rels)
	}
}

// TestResolveLoadBalancing_RegionalChain covers the full regional variant of
// the chain: regional forwarding rule → regional target HTTPS proxy →
// regional url map → regional backend service.
func TestResolveLoadBalancing_RegionalChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	const base = "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1"
	bsURL := base + "/backendServices/bs-1"
	umURL := base + "/urlMaps/um-1"
	tpURL := base + "/targetHttpsProxies/tp-1"
	frURL := base + "/forwardingRules/fr-1"

	bsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionBackendService, bsURL, "us-central1", "{}")
	umID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionURLMap, umURL, "us-central1",
		`{"defaultService": "`+bsURL+`"}`)
	tpID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionTargetHTTPSProxy, tpURL, "us-central1",
		`{"urlMap": "`+umURL+`"}`)
	frID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeForwardingRule, frURL, "us-central1",
		`{"target": "`+tpURL+`"}`)

	if err := resolveLoadBalancingRelationships(p, st); err != nil {
		t.Fatalf("resolveLoadBalancingRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(frID)
	if len(rels) != 1 || rels[0].ToID != tpID {
		t.Errorf("fr edge: got %+v", rels)
	}
	rels, _ = st.RelationshipsFrom(tpID)
	if len(rels) != 1 || rels[0].ToID != umID {
		t.Errorf("tp edge: got %+v", rels)
	}
	rels, _ = st.RelationshipsFrom(umID)
	if len(rels) != 1 || rels[0].ToID != bsID {
		t.Errorf("um edge: got %+v", rels)
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
