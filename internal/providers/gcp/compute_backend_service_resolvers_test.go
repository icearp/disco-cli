package gcp

import (
	"testing"

	compute "google.golang.org/api/compute/v1"
)

func TestResolveBackendServiceRelationships_GlobalHappyPath(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	netID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetwork, "projects/proj-1/global/networks/net-1", "",
		marshalAttrs(t, &compute.Network{SelfLink: "projects/proj-1/global/networks/net-1", Name: "net-1"}))
	hcID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeHealthCheck, "projects/proj-1/global/healthChecks/hc-1", "",
		marshalAttrs(t, &compute.HealthCheck{SelfLink: "projects/proj-1/global/healthChecks/hc-1", Name: "hc-1"}))
	igID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstanceGroup, "projects/proj-1/zones/us-central1-a/instanceGroups/ig-1", "us-central1",
		marshalAttrs(t, &compute.InstanceGroup{SelfLink: "projects/proj-1/zones/us-central1-a/instanceGroups/ig-1", Name: "ig-1"}))
	negID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetworkEndpointGroup, "projects/proj-1/zones/us-central1-a/networkEndpointGroups/neg-1", "us-central1",
		marshalAttrs(t, &compute.NetworkEndpointGroup{SelfLink: "projects/proj-1/zones/us-central1-a/networkEndpointGroups/neg-1", Name: "neg-1"}))

	bsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeBackendService, "projects/proj-1/global/backendServices/bs-1", "",
		marshalAttrs(t, &compute.BackendService{
			SelfLink:     "projects/proj-1/global/backendServices/bs-1",
			Name:         "bs-1",
			Network:      "projects/proj-1/global/networks/net-1",
			HealthChecks: []string{"projects/proj-1/global/healthChecks/hc-1"},
			Backends: []*compute.Backend{
				{Group: "projects/proj-1/zones/us-central1-a/instanceGroups/ig-1"},
				{Group: "projects/proj-1/zones/us-central1-a/networkEndpointGroups/neg-1"},
			},
		}))

	if err := resolveBackendServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveBackendServiceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(bsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[netID] != "attached-to" || got[hcID] != "uses" || got[igID] != "uses" || got[negID] != "uses" {
		t.Errorf("want backendService->network/healthCheck/instanceGroup/NEG edges, got %+v", rels)
	}
	if len(rels) != 4 {
		t.Errorf("want exactly 4 edges, got %d: %+v", len(rels), rels)
	}
}

func TestResolveBackendServiceRelationships_RegionalHealthCheckScopedToRegional(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	// Modern global HealthCheck present in-store under the same NativeID
	// shape as a hypothetical region health check would collide on if the
	// resolver failed to scope candidates by source type.
	regionHCID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionHealthCheck, "projects/proj-1/regions/us-central1/healthChecks/rhc-1", "us-central1",
		marshalAttrs(t, &compute.HealthCheck{SelfLink: "projects/proj-1/regions/us-central1/healthChecks/rhc-1", Name: "rhc-1", Region: "us-central1"}))

	rbsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionBackendService, "projects/proj-1/regions/us-central1/backendServices/rbs-1", "us-central1",
		marshalAttrs(t, &compute.BackendService{
			SelfLink:     "projects/proj-1/regions/us-central1/backendServices/rbs-1",
			Name:         "rbs-1",
			Region:       "us-central1",
			HealthChecks: []string{"projects/proj-1/regions/us-central1/healthChecks/rhc-1"},
		}))

	if err := resolveBackendServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveBackendServiceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(rbsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != regionHCID || rels[0].Kind != "uses" {
		t.Errorf("want regionBackendService->regionHealthCheck edge, got %+v", rels)
	}
}

func TestResolveBackendServiceRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	bsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeBackendService, "projects/proj-1/global/backendServices/bs-1", "",
		marshalAttrs(t, &compute.BackendService{
			SelfLink:     "projects/proj-1/global/backendServices/bs-1",
			Name:         "bs-1",
			Network:      "projects/proj-1/global/networks/not-scanned",
			HealthChecks: []string{"projects/proj-1/global/healthChecks/not-scanned"},
			Backends: []*compute.Backend{
				{Group: "projects/proj-1/zones/us-central1-a/instanceGroups/not-scanned"},
			},
		}))

	if err := resolveBackendServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveBackendServiceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(bsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned references, got %+v", rels)
	}
}

// TestResolveBackendServiceRelationships_AmbiguousGroupPicksCorrectCandidate
// guards upsertIfScannedAny's candidate-search order: with InstanceGroup and
// RegionInstanceGroup decoys scanned under unrelated self-links, and the
// real backend group a GlobalNetworkEndpointGroup (last in the candidate
// list), the resolver must still find it rather than stopping early or
// cross-matching a decoy.
func TestResolveBackendServiceRelationships_AmbiguousGroupPicksCorrectCandidate(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstanceGroup, "projects/proj-1/zones/us-central1-a/instanceGroups/decoy-ig", "us-central1",
		marshalAttrs(t, &compute.InstanceGroup{SelfLink: "projects/proj-1/zones/us-central1-a/instanceGroups/decoy-ig", Name: "decoy-ig"}))
	upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionInstanceGroup, "projects/proj-1/regions/us-central1/instanceGroups/decoy-rig", "us-central1",
		marshalAttrs(t, &compute.InstanceGroup{SelfLink: "projects/proj-1/regions/us-central1/instanceGroups/decoy-rig", Name: "decoy-rig"}))

	negID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeGlobalNetworkEndpointGroup, "projects/proj-1/global/networkEndpointGroups/global-neg-1", "",
		marshalAttrs(t, &compute.NetworkEndpointGroup{SelfLink: "projects/proj-1/global/networkEndpointGroups/global-neg-1", Name: "global-neg-1"}))

	bsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeBackendService, "projects/proj-1/global/backendServices/bs-1", "",
		marshalAttrs(t, &compute.BackendService{
			SelfLink: "projects/proj-1/global/backendServices/bs-1",
			Name:     "bs-1",
			Backends: []*compute.Backend{
				{Group: "projects/proj-1/global/networkEndpointGroups/global-neg-1"},
			},
		}))

	if err := resolveBackendServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveBackendServiceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(bsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != negID || rels[0].Kind != "uses" {
		t.Errorf("want backendService->globalNetworkEndpointGroup edge (not a decoy match), got %+v", rels)
	}
}

func TestResolveBackendServiceRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveBackendServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveBackendServiceRelationships on empty project: %v", err)
	}
}

func TestResolveAutoscalerRelationships_ZonalAndRegional(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	igmID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeInstanceGroupManager, "projects/proj-1/zones/us-central1-a/instanceGroupManagers/igm-1", "us-central1",
		marshalAttrs(t, &compute.InstanceGroupManager{SelfLink: "projects/proj-1/zones/us-central1-a/instanceGroupManagers/igm-1", Name: "igm-1"}))
	rigmID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionInstanceGroupManager, "projects/proj-1/regions/us-central1/instanceGroupManagers/rigm-1", "us-central1",
		marshalAttrs(t, &compute.InstanceGroupManager{SelfLink: "projects/proj-1/regions/us-central1/instanceGroupManagers/rigm-1", Name: "rigm-1"}))

	asID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeAutoscaler, "projects/proj-1/zones/us-central1-a/autoscalers/as-1", "us-central1",
		marshalAttrs(t, &compute.Autoscaler{
			SelfLink: "projects/proj-1/zones/us-central1-a/autoscalers/as-1",
			Name:     "as-1",
			Target:   "projects/proj-1/zones/us-central1-a/instanceGroupManagers/igm-1",
		}))
	rasID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionAutoscaler, "projects/proj-1/regions/us-central1/autoscalers/ras-1", "us-central1",
		marshalAttrs(t, &compute.Autoscaler{
			SelfLink: "projects/proj-1/regions/us-central1/autoscalers/ras-1",
			Name:     "ras-1",
			Target:   "projects/proj-1/regions/us-central1/instanceGroupManagers/rigm-1",
		}))

	if err := resolveAutoscalerRelationships(p, st); err != nil {
		t.Fatalf("resolveAutoscalerRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(asID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(asID): %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != igmID || rels[0].Kind != "attached-to" {
		t.Errorf("want autoscaler->instanceGroupManager edge, got %+v", rels)
	}

	rels, err = st.RelationshipsFrom(rasID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(rasID): %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != rigmID || rels[0].Kind != "attached-to" {
		t.Errorf("want regionAutoscaler->regionInstanceGroupManager edge, got %+v", rels)
	}
}

func TestResolveAutoscalerRelationships_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	asID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeAutoscaler, "projects/proj-1/zones/us-central1-a/autoscalers/as-1", "us-central1",
		marshalAttrs(t, &compute.Autoscaler{
			SelfLink: "projects/proj-1/zones/us-central1-a/autoscalers/as-1",
			Name:     "as-1",
			Target:   "projects/proj-1/zones/us-central1-a/instanceGroupManagers/not-scanned",
		}))

	if err := resolveAutoscalerRelationships(p, st); err != nil {
		t.Fatalf("resolveAutoscalerRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(asID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned target, got %+v", rels)
	}
}

func TestResolveAutoscalerRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveAutoscalerRelationships(p, st); err != nil {
		t.Fatalf("resolveAutoscalerRelationships on empty project: %v", err)
	}
}

func TestResolveCloudArmorRelationships_RegionBackendService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	spID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeSecurityPolicy, "projects/proj-1/global/securityPolicies/sp-1", "",
		marshalAttrs(t, &compute.SecurityPolicy{SelfLink: "projects/proj-1/global/securityPolicies/sp-1", Name: "sp-1"}))

	rbsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionBackendService, "projects/proj-1/regions/us-central1/backendServices/rbs-1", "us-central1",
		marshalAttrs(t, &compute.BackendService{
			SelfLink:       "projects/proj-1/regions/us-central1/backendServices/rbs-1",
			Name:           "rbs-1",
			Region:         "us-central1",
			SecurityPolicy: "projects/proj-1/global/securityPolicies/sp-1",
		}))

	if err := resolveCloudArmorRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudArmorRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(rbsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != spID || rels[0].Kind != "uses" {
		t.Errorf("want regionBackendService->securityPolicy edge, got %+v", rels)
	}
}
