package gcp

import (
	"errors"
	"net/http"
	"testing"

	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/compute/v1"
)

// Region* scanners (RegionInstanceGroups, RegionInstanceGroupManagers) fan
// out via gcpRegions, which builds its own real ADC client internally — not
// reachable through the fakeComputeService passed to the scanner under test
// (same caveat as compute_storage_scanners_test.go's Wave 1 tests). Only the
// scanners using the passed-in *compute.Service directly are covered here.

func TestScanComputeInstanceGroups_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	igSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instanceGroups/ig1"
	page := compute.InstanceGroupAggregatedList{
		Items: map[string]compute.InstanceGroupsScopedList{
			"zones/us-central1-a": {
				InstanceGroups: []*compute.InstanceGroup{{Name: "ig1", SelfLink: igSelfLink, Zone: "zones/us-central1-a"}},
			},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/instanceGroups": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeInstanceGroups(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeInstanceGroups: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	id := store.ResourceID("gcp", p.ID, igSelfLink)
	got, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Zone == nil || *got.Zone != "us-central1-a" {
		t.Errorf("instance group zone: got %v, want us-central1-a", got.Zone)
	}
}

func TestScanComputeInstanceGroupManagers_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	igmSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instanceGroupManagers/igm1"
	igmPage := compute.InstanceGroupManagerAggregatedList{
		Items: map[string]compute.InstanceGroupManagersScopedList{
			"zones/us-central1-a": {
				InstanceGroupManagers: []*compute.InstanceGroupManager{{Name: "igm1", SelfLink: igmSelfLink, Zone: "zones/us-central1-a"}},
			},
		},
	}
	rrSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instanceGroupManagers/igm1/resizeRequests/rr1"
	rrPage := compute.InstanceGroupManagerResizeRequestsListResponse{
		Items: []*compute.InstanceGroupManagerResizeRequest{{Name: "rr1", SelfLink: rrSelfLink}},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/instanceGroupManagers":                              marshalAttrs(t, igmPage),
		"/projects/my-project/zones/us-central1-a/instanceGroupManagers/igm1/resizeRequests": marshalAttrs(t, rrPage),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeInstanceGroupManagers(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeInstanceGroupManagers: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2 (igm + resize request)", total, inserted)
	}

	igmID := store.ResourceID("gcp", p.ID, igmSelfLink)
	if _, err := st.GetResource(igmID); err != nil {
		t.Errorf("GetResource(igm): %v", err)
	}
	rrID := store.ResourceID("gcp", p.ID, rrSelfLink)
	got, err := st.GetResource(rrID)
	if err != nil {
		t.Fatalf("GetResource(resize request): %v", err)
	}
	if got.Zone == nil || *got.Zone != "us-central1-a" {
		t.Errorf("resize request zone: got %v, want us-central1-a", got.Zone)
	}
}

func TestScanComputeInstanceGroupManagers_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"caller is missing compute.instanceGroupManagers.list","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeInstanceGroupManagers(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeInstanceGroupManagers (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

func TestScanComputeInstanceGroupManagers_APINotEnabled(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"Compute Engine API has not been used in project my-project before or it is disabled.","errors":[{"reason":"accessNotConfigured"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	_, _, err := scanComputeInstanceGroupManagers(t.Context(), svc, p, st, testScanID)
	if !errors.Is(err, errServiceDisabled) {
		t.Fatalf("scanComputeInstanceGroupManagers: expected errServiceDisabled sentinel, got %v", err)
	}
}

func TestScanComputeInstanceTemplates_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	globalSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/instanceTemplates/it-global"
	regionalSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/regions/us-central1/instanceTemplates/it-regional"
	page := compute.InstanceTemplateAggregatedList{
		Items: map[string]compute.InstanceTemplatesScopedList{
			"global": {
				InstanceTemplates: []*compute.InstanceTemplate{{Name: "it-global", SelfLink: globalSelfLink}},
			},
			"regions/us-central1": {
				InstanceTemplates: []*compute.InstanceTemplate{{Name: "it-regional", SelfLink: regionalSelfLink}},
			},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/instanceTemplates": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeInstanceTemplates(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeInstanceTemplates: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	globalID := store.ResourceID("gcp", p.ID, globalSelfLink)
	got, err := st.GetResource(globalID)
	if err != nil {
		t.Fatalf("GetResource(global): %v", err)
	}
	if got.Region != nil {
		t.Errorf("global instance template should have no region, got %v", got.Region)
	}

	regionalID := store.ResourceID("gcp", p.ID, regionalSelfLink)
	got2, err := st.GetResource(regionalID)
	if err != nil {
		t.Fatalf("GetResource(regional): %v", err)
	}
	if got2.Region == nil || *got2.Region != "us-central1" {
		t.Errorf("regional instance template region: got %v, want us-central1", got2.Region)
	}
}
