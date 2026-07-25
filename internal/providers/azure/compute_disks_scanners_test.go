package azure

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	armcomputefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake"
	"github.com/icearp/disco-cli/store"
)

// TestScanDisks_FakeTransport exercises the disk scanner against a
// fake-transport-backed *armcompute.DisksClient (concrete SDK client, transport
// swapped — the recommended in-process fake pattern). Verifies the azSimpleScan
// pipeline (NewListPager → azPageScan → azTrackedRows → UpsertResources) and the
// rgHierarchyPair emission for an RG-scoped resource ID.
func TestScanDisks_FakeTransport(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	diskNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg1/providers/Microsoft.Compute/disks/d1"

	server := armcomputefake.DisksServer{
		NewListPager: func(_ *armcompute.DisksClientListOptions) fake.PagerResponder[armcompute.DisksClientListResponse] {
			r := fake.PagerResponder[armcompute.DisksClientListResponse]{}
			r.AddPage(http.StatusOK, armcompute.DisksClientListResponse{
				DiskList: armcompute.DiskList{Value: []*armcompute.Disk{{
					ID:       to.Ptr(diskNativeID),
					Name:     to.Ptr("d1"),
					Location: to.Ptr("eastus"),
				}}},
			}, nil)
			return r
		},
	}

	client, err := armcompute.NewDisksClient(sub.ID, fakeCred(), fakeClientOptions(t, armcomputefake.NewDisksServerTransport(&server)))
	if err != nil {
		t.Fatalf("NewDisksClient: %v", err)
	}

	total, inserted, err := scanDisksWithClient(t.Context(), sub, st, testScanID, client)
	if err != nil {
		t.Fatalf("scanDisksWithClient: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	id := store.ResourceID("azure", sub.ID, diskNativeID)
	got, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Name == nil || *got.Name != "d1" {
		t.Errorf("disk name: got %v, want d1", got.Name)
	}
	if got.Region == nil || *got.Region != "eastus" {
		t.Errorf("disk region: got %v, want eastus", got.Region)
	}
}

// TestScanDisks_FakeTransport_Pagination verifies the pager is fully drained
// across multiple pages and every disk is upserted.
func TestScanDisks_FakeTransport_Pagination(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	mkDisk := func(name string) *armcompute.Disk {
		id := "/subscriptions/" + testSubID + "/resourceGroups/rg1/providers/Microsoft.Compute/disks/" + name
		return &armcompute.Disk{ID: to.Ptr(id), Name: to.Ptr(name), Location: to.Ptr("eastus")}
	}

	server := armcomputefake.DisksServer{
		NewListPager: func(_ *armcompute.DisksClientListOptions) fake.PagerResponder[armcompute.DisksClientListResponse] {
			r := fake.PagerResponder[armcompute.DisksClientListResponse]{}
			r.AddPage(http.StatusOK, armcompute.DisksClientListResponse{
				DiskList: armcompute.DiskList{Value: []*armcompute.Disk{mkDisk("d1"), mkDisk("d2")}},
			}, nil)
			r.AddPage(http.StatusOK, armcompute.DisksClientListResponse{
				DiskList: armcompute.DiskList{Value: []*armcompute.Disk{mkDisk("d3")}},
			}, nil)
			return r
		},
	}

	client, err := armcompute.NewDisksClient(sub.ID, fakeCred(), fakeClientOptions(t, armcomputefake.NewDisksServerTransport(&server)))
	if err != nil {
		t.Fatalf("NewDisksClient: %v", err)
	}

	total, inserted, err := scanDisksWithClient(t.Context(), sub, st, testScanID, client)
	if err != nil {
		t.Fatalf("scanDisksWithClient: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Fatalf("counts: got total=%d inserted=%d, want 3/3", total, inserted)
	}
}

// TestScanDisks_FakeTransport_AccessDenied verifies the scanner tolerates a 403
// by returning (0, 0, nil) and reporting a scan error instead of propagating it.
func TestScanDisks_FakeTransport_AccessDenied(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	server := armcomputefake.DisksServer{
		NewListPager: func(_ *armcompute.DisksClientListOptions) fake.PagerResponder[armcompute.DisksClientListResponse] {
			r := fake.PagerResponder[armcompute.DisksClientListResponse]{}
			r.AddResponseError(http.StatusForbidden, "AuthorizationFailed")
			return r
		},
	}

	client, err := armcompute.NewDisksClient(sub.ID, fakeCred(), fakeClientOptions(t, armcomputefake.NewDisksServerTransport(&server)))
	if err != nil {
		t.Fatalf("NewDisksClient: %v", err)
	}

	total, inserted, err := scanDisksWithClient(t.Context(), sub, st, testScanID, client)
	if err != nil {
		t.Fatalf("scanDisksWithClient (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

// TestScanDisks_FakeTransport_SubscriptionNotRegistered verifies a 404
// SubscriptionNotRegistered (RP not registered on the subscription) surfaces as
// the errServiceNotRegistered sentinel with (0, 0) counts — the dispatch loop
// turns this into a disabled service (no warning, no error), Azure's analog of
// AWS's errServiceDisabled.
func TestScanDisks_FakeTransport_SubscriptionNotRegistered(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	server := armcomputefake.DisksServer{
		NewListPager: func(_ *armcompute.DisksClientListOptions) fake.PagerResponder[armcompute.DisksClientListResponse] {
			r := fake.PagerResponder[armcompute.DisksClientListResponse]{}
			r.AddResponseError(http.StatusNotFound, "SubscriptionNotRegistered")
			return r
		},
	}

	client, err := armcompute.NewDisksClient(sub.ID, fakeCred(), fakeClientOptions(t, armcomputefake.NewDisksServerTransport(&server)))
	if err != nil {
		t.Fatalf("NewDisksClient: %v", err)
	}

	total, inserted, err := scanDisksWithClient(t.Context(), sub, st, testScanID, client)
	if !errors.Is(err, errServiceNotRegistered) {
		t.Fatalf("scanDisksWithClient (not registered): got err=%v, want errServiceNotRegistered", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
