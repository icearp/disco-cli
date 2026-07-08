package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	compute "google.golang.org/api/compute/v1"
)

func TestResolveRegionCompositeHealthCheckRelationships_DestinationAndSources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	fwdSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/forwardingRules/fr-1"
	hsSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/healthSources/hs-1"
	fwdID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeForwardingRule, fwdSelfLink, "us-central1", "{}")
	hsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionHealthSource, hsSelfLink, "us-central1", "{}")

	chcSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/regionCompositeHealthChecks/chc-1"
	chcID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionCompositeHealthCheck, chcSelfLink, "us-central1",
		marshalAttrs(t, &compute.CompositeHealthCheck{
			Name:              "chc-1",
			SelfLink:          chcSelfLink,
			HealthDestination: fwdSelfLink,
			HealthSources:     []string{hsSelfLink},
		}))

	if err := resolveRegionCompositeHealthCheckRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionCompositeHealthCheckRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(chcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	want := map[string]bool{fwdID: false, hsID: false}
	for _, rel := range rels {
		if _, ok := want[rel.ToID]; !ok {
			t.Fatalf("unexpected edge target %s", rel.ToID)
		}
		if rel.Kind != store.RelUses {
			t.Errorf("got kind %s, want %s", rel.Kind, store.RelUses)
		}
		want[rel.ToID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing edge to %s", id)
		}
	}
}

func TestResolveRegionCompositeHealthCheckRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	chcSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/regionCompositeHealthChecks/chc-1"
	chcID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionCompositeHealthCheck, chcSelfLink, "us-central1",
		marshalAttrs(t, &compute.CompositeHealthCheck{
			Name:              "chc-1",
			SelfLink:          chcSelfLink,
			HealthDestination: "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/forwardingRules/not-scanned",
			HealthSources: []string{
				"https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/healthSources/not-scanned",
			},
		}))

	if err := resolveRegionCompositeHealthCheckRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionCompositeHealthCheckRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(chcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

func TestResolveRegionCompositeHealthCheckRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveRegionCompositeHealthCheckRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionCompositeHealthCheckRelationships on empty project: %v", err)
	}
}

func TestResolveRegionHealthCheckServiceRelationships_HealthChecks(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	hcSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/healthChecks/hc-1"
	hcID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionHealthCheck, hcSelfLink, "us-central1", "{}")

	hcsSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/regionHealthCheckServices/hcs-1"
	hcsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionHealthCheckService, hcsSelfLink, "us-central1",
		marshalAttrs(t, &compute.HealthCheckService{
			Name:         "hcs-1",
			SelfLink:     hcsSelfLink,
			HealthChecks: []string{hcSelfLink},
		}))

	if err := resolveRegionHealthCheckServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionHealthCheckServiceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(hcsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != hcID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →healthCheck uses", rels)
	}
}

func TestResolveRegionHealthCheckServiceRelationships_UnscannedHealthCheckSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	hcsSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/regionHealthCheckServices/hcs-1"
	hcsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionHealthCheckService, hcsSelfLink, "us-central1",
		marshalAttrs(t, &compute.HealthCheckService{
			Name:     "hcs-1",
			SelfLink: hcsSelfLink,
			HealthChecks: []string{
				"https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/healthChecks/not-scanned",
			},
		}))

	if err := resolveRegionHealthCheckServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionHealthCheckServiceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(hcsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned health check, got %+v", rels)
	}
}

func TestResolveRegionHealthCheckServiceRelationships_GlobalScopeMatchesGlobalHealthCheck(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	globalHCSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/healthChecks/global-hc-1"
	globalHCID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeHealthCheck, globalHCSelfLink, "", "{}")

	// Global-scope HealthCheckService rows are scanned under the same disco
	// type as regional ones (region left unset) — see the scanner's own
	// "opportunistic-Region shape" header. Its HealthChecks[] must resolve
	// against the global HealthCheck type, not the regional one.
	hcsSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/healthCheckServices/hcs-global"
	hcsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionHealthCheckService, hcsSelfLink, "",
		marshalAttrs(t, &compute.HealthCheckService{
			Name:         "hcs-global",
			SelfLink:     hcsSelfLink,
			HealthChecks: []string{globalHCSelfLink},
		}))

	if err := resolveRegionHealthCheckServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionHealthCheckServiceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(hcsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != globalHCID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →global healthCheck uses", rels)
	}
}

func TestResolveRegionHealthCheckServiceRelationships_NetworkEndpointGroupsAndNotificationEndpoints(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	negSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/networkEndpointGroups/neg-1"
	negID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeNetworkEndpointGroup, negSelfLink, "us-central1-a", "{}")

	notifSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/notificationEndpoints/notif-1"
	notifID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionNotificationEndpoint, notifSelfLink, "us-central1", "{}")

	hcsSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/regionHealthCheckServices/hcs-1"
	hcsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionHealthCheckService, hcsSelfLink, "us-central1",
		marshalAttrs(t, &compute.HealthCheckService{
			Name:                  "hcs-1",
			SelfLink:              hcsSelfLink,
			NetworkEndpointGroups: []string{negSelfLink},
			NotificationEndpoints: []string{notifSelfLink},
		}))

	if err := resolveRegionHealthCheckServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionHealthCheckServiceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(hcsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	want := map[string]bool{negID: false, notifID: false}
	for _, rel := range rels {
		if _, ok := want[rel.ToID]; !ok {
			t.Fatalf("unexpected edge target %s", rel.ToID)
		}
		want[rel.ToID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing edge to %s", id)
		}
	}
}

func TestResolveRegionHealthCheckServiceRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveRegionHealthCheckServiceRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionHealthCheckServiceRelationships on empty project: %v", err)
	}
}

func TestResolveRegionHealthSourceRelationships_AggregationPolicy(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	policySelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/regionHealthAggregationPolicies/pol-1"
	policyID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionHealthAggregationPolicy, policySelfLink, "us-central1", "{}")

	hsSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/healthSources/hs-1"
	hsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionHealthSource, hsSelfLink, "us-central1",
		marshalAttrs(t, &compute.HealthSource{
			Name:                    "hs-1",
			SelfLink:                hsSelfLink,
			HealthAggregationPolicy: policySelfLink,
		}))

	if err := resolveRegionHealthSourceRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionHealthSourceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(hsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != policyID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →healthAggregationPolicy uses", rels)
	}
}

func TestResolveRegionHealthSourceRelationships_UnscannedPolicySkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	hsSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/healthSources/hs-1"
	hsID := upsertTestResource(t, st, "gcp", p.ID, TypeComputeRegionHealthSource, hsSelfLink, "us-central1",
		marshalAttrs(t, &compute.HealthSource{
			Name:                    "hs-1",
			SelfLink:                hsSelfLink,
			HealthAggregationPolicy: "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/regionHealthAggregationPolicies/not-scanned",
		}))

	if err := resolveRegionHealthSourceRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionHealthSourceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(hsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned aggregation policy, got %+v", rels)
	}
}

func TestResolveRegionHealthSourceRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveRegionHealthSourceRelationships(p, st); err != nil {
		t.Fatalf("resolveRegionHealthSourceRelationships on empty project: %v", err)
	}
}
