package gcp

import (
	"errors"
	"net/http"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

// Region*/InstantSnapshotGroups scanners fan out via gcpRegions/gcpZones,
// which build their own real ADC client internally — not reachable through
// the fakeComputeService passed to the scanner under test (see
// gcp_scan_helpers_test.go's TestGcpRegionFanoutScanIn for the generic
// fan-out coverage, and the "Test-seam pattern" note in CLAUDE.md). Only the
// scanners that use the passed-in *compute.Service directly for their own
// List/AggregatedList call are covered here.

func TestScanComputeDisks_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	diskSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/disks/disk1"
	page := compute.DiskAggregatedList{
		Items: map[string]compute.DisksScopedList{
			"zones/us-central1-a": {
				Disks: []*compute.Disk{{Name: "disk1", SelfLink: diskSelfLink, Zone: "zones/us-central1-a", Status: "READY"}},
			},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/disks": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeDisks(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeDisks: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	id := store.ResourceID("gcp", p.ID, diskSelfLink)
	got, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Zone == nil || *got.Zone != "us-central1-a" {
		t.Errorf("disk zone: got %v, want us-central1-a", got.Zone)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("disk region: got %v, want us-central1", got.Region)
	}
}

func TestScanComputeDisks_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"caller is missing compute.disks.list","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeDisks(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeDisks (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

func TestScanComputeDisks_APINotEnabled(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"Compute Engine API has not been used in project my-project before or it is disabled.","errors":[{"reason":"accessNotConfigured"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeComputeService(t, srv)

	_, _, err := scanComputeDisks(t.Context(), svc, p, st, testScanID)
	if !errors.Is(err, errServiceDisabled) {
		t.Fatalf("scanComputeDisks: expected errServiceDisabled sentinel, got %v", err)
	}
}

func TestScanComputeImages_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	imgSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/images/img1"
	page := compute.ImageList{Items: []*compute.Image{{Name: "img1", SelfLink: imgSelfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/images": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeImages(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeImages: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	id := store.ResourceID("gcp", p.ID, imgSelfLink)
	got, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Zone != nil || got.Region != nil {
		t.Errorf("image should have no zone/region, got zone=%v region=%v", got.Zone, got.Region)
	}
}

func TestScanComputeMachineImages_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	miSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/machineImages/mi1"
	page := compute.MachineImageList{Items: []*compute.MachineImage{{Name: "mi1", SelfLink: miSelfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/machineImages": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeMachineImages(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeMachineImages: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeSnapshots_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	snapSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/global/snapshots/snap1"
	page := compute.SnapshotList{Items: []*compute.Snapshot{{Name: "snap1", SelfLink: snapSelfLink}}}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/global/snapshots": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeSnapshots(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeSnapshots: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComputeInstantSnapshots_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	isSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/instantSnapshots/is1"
	page := compute.InstantSnapshotAggregatedList{
		Items: map[string]compute.InstantSnapshotsScopedList{
			"zones/us-central1-a": {
				InstantSnapshots: []*compute.InstantSnapshot{{Name: "is1", SelfLink: isSelfLink, Zone: "zones/us-central1-a"}},
			},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/instantSnapshots": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeInstantSnapshots(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeInstantSnapshots: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	id := store.ResourceID("gcp", p.ID, isSelfLink)
	got, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Zone == nil || *got.Zone != "us-central1-a" {
		t.Errorf("instant snapshot zone: got %v, want us-central1-a", got.Zone)
	}
}

func TestScanComputeStoragePools_Fake(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	spSelfLink := "https://www.googleapis.com/compute/v1/projects/my-project/zones/us-central1-a/storagePools/sp1"
	page := compute.StoragePoolAggregatedList{
		Items: map[string]compute.StoragePoolsScopedList{
			"zones/us-central1-a": {
				StoragePools: []*compute.StoragePool{{Name: "sp1", SelfLink: spSelfLink, Zone: "zones/us-central1-a"}},
			},
		},
	}
	srv := fakeGCPServer(t, map[string]string{
		"/projects/my-project/aggregated/storagePools": marshalAttrs(t, page),
	})
	svc := fakeComputeService(t, srv)

	total, inserted, err := scanComputeStoragePools(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComputeStoragePools: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	id := store.ResourceID("gcp", p.ID, spSelfLink)
	got, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Region == nil || *got.Region != "us-central1" {
		t.Errorf("storage pool region: got %v, want us-central1", got.Region)
	}
}
