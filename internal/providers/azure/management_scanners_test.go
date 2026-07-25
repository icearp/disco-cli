package azure

import (
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managementgroups/armmanagementgroups"
	armmgmtfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managementgroups/armmanagementgroups/fake"
	"github.com/icearp/disco-cli/store"
)

// TestScanManagementInto_StoresUnderTenant pins the tenant-scope move: management
// groups are upserted under the supplied account (the tenant GUID), not a
// subscription ID, so a multi-subscription scan keeps a single copy of the tree.
func TestScanManagementInto_StoresUnderTenant(t *testing.T) {
	st := newTestStore(t)
	const tenantID = "tenant-guid-xyz"
	mgID := "/providers/Microsoft.Management/managementGroups/mg-root"

	server := armmgmtfake.Server{
		NewListPager: func(_ *armmanagementgroups.ClientListOptions) fake.PagerResponder[armmanagementgroups.ClientListResponse] {
			r := fake.PagerResponder[armmanagementgroups.ClientListResponse]{}
			r.AddPage(http.StatusOK, armmanagementgroups.ClientListResponse{
				ManagementGroupListResult: armmanagementgroups.ManagementGroupListResult{
					Value: []*armmanagementgroups.ManagementGroupInfo{{
						ID:   to.Ptr(mgID),
						Name: to.Ptr("mg-root"),
					}},
				},
			}, nil)
			return r
		},
	}
	client, err := armmanagementgroups.NewClient(fakeCred(), fakeClientOptions(t, armmgmtfake.NewServerTransport(&server)))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	total, inserted, err := scanManagementInto(t.Context(), tenantID, st, testScanID, client)
	if err != nil {
		t.Fatalf("scanManagementInto: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	// Stored under the tenant account, not a subscription.
	if _, err := st.GetResource(store.ResourceID("azure", tenantID, mgID)); err != nil {
		t.Errorf("management group not stored under tenant account: %v", err)
	}
	// And NOT under the subscription account (the old per-sub behavior).
	if _, err := st.GetResource(store.ResourceID("azure", testSubID, mgID)); err == nil {
		t.Error("management group unexpectedly stored under subscription account")
	}
}
