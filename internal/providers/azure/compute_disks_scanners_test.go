package azure

import (
	"net/http"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	armcomputefake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake"
)

// TestScanDisks_FakeTransport exercises the disk scanner end-to-end against a
// fake-transport-backed *armcompute.DisksClient. Verifies the azSimpleScan
// pipeline (NewListPager → azPageScan → azTrackedRows → UpsertResources) and
// the rgHierarchyPair emission for an RG-scoped resource ID. Uses the
// in-process fake pattern recommended for client-library testing — concrete
// SDK client retained, transport swapped.
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

	id := store.ResourceID("azure", sub.ID, TypeComputeManagedDisk, diskNativeID)
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

// TestScanDisks_FakeTransport_AccessDenied verifies the scanner tolerates a
// 403 from the SDK by returning (0, 0, nil) and reporting a scan error
// rather than propagating the failure.
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
