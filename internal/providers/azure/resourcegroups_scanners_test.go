package azure

import (
	"net/http"
	"path/filepath"
	"testing"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	armresourcesfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/fake"

	"github.com/icearp/disco-cli/store"
)

// closedStore returns a store whose database is already closed, so any write
// through it fails. Deliberately not newTestStore: that one registers cleanups
// which themselves query the database.
func closedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("closedStore: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("closedStore: close: %v", err)
	}
	return st
}

// TestResourceGroupsFromPager_ListedTracksTheCallNotTheError pins `listed`,
// which is half of the conjunction scanSubscription refuses a whole
// subscription on. It is NOT `err == nil` in either direction, and both
// directions are asserted here because each has already been written the wrong
// way: a refusal is absorbed into a ScanWarning and returns a NIL error, and a
// page that came back and then failed to STORE returns an error with the token
// already proved good.
//
// Getting the second one wrong is the expensive direction: a database fault
// would let a concurrent 401 on the providers endpoint refuse the subscription
// and report the account's access as denied.
func TestResourceGroupsFromPager_ListedTracksTheCallNotTheError(t *testing.T) {
	rgPage := func(names ...string) armresources.ResourceGroupsClientListResponse {
		var vals []*armresources.ResourceGroup
		for _, n := range names {
			vals = append(vals, &armresources.ResourceGroup{
				ID:       to.Ptr("/subscriptions/" + testSubID + "/resourceGroups/" + n),
				Name:     to.Ptr(n),
				Location: to.Ptr("eastus"),
			})
		}
		return armresources.ResourceGroupsClientListResponse{
			ResourceGroupListResult: armresources.ResourceGroupListResult{Value: vals},
		}
	}

	cases := []struct {
		name       string
		respond    func(*azfake.PagerResponder[armresources.ResourceGroupsClientListResponse])
		wantListed bool
		wantErr    bool
		// brokenStore drives the page-then-WRITE-failure direction, which is
		// the one the split exists for and which no pager outcome can produce.
		brokenStore bool
	}{
		{
			name: "a page came back and the store refused it",
			respond: func(r *azfake.PagerResponder[armresources.ResourceGroupsClientListResponse]) {
				r.AddPage(http.StatusOK, rgPage("rg-one"), nil)
			},
			brokenStore: true,
			wantListed:  true,
			wantErr:     true,
		},
		{
			name: "a page came back",
			respond: func(r *azfake.PagerResponder[armresources.ResourceGroupsClientListResponse]) {
				r.AddPage(http.StatusOK, rgPage("rg-one"), nil)
			},
			wantListed: true,
		},
		{
			// The token was accepted; there is simply nothing there. This is
			// the case a "did we store anything" test would get backwards.
			name: "an EMPTY page came back",
			respond: func(r *azfake.PagerResponder[armresources.ResourceGroupsClientListResponse]) {
				r.AddPage(http.StatusOK, rgPage(), nil)
			},
			wantListed: true,
		},
		{
			// Absorbed into a ScanWarning, so err is nil -- which is exactly
			// why the caller cannot use the error to answer this question.
			name: "403 refused, reported as a warning",
			respond: func(r *azfake.PagerResponder[armresources.ResourceGroupsClientListResponse]) {
				r.AddResponseError(http.StatusForbidden, "AuthorizationFailed")
			},
			wantListed: false,
		},
		{
			name: "401 refused, reported as a warning",
			respond: func(r *azfake.PagerResponder[armresources.ResourceGroupsClientListResponse]) {
				r.AddResponseError(http.StatusUnauthorized, "InvalidAuthenticationTokenTenant")
			},
			wantListed: false,
		},
		{
			// A page, then a refusal on the next one. The token was accepted.
			name: "a page came back before the refusal",
			respond: func(r *azfake.PagerResponder[armresources.ResourceGroupsClientListResponse]) {
				r.AddPage(http.StatusOK, rgPage("rg-one"), nil)
				r.AddResponseError(http.StatusUnauthorized, "InvalidAuthenticationTokenTenant")
			},
			wantListed: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			if tc.brokenStore {
				// A store that cannot accept the batch. The page already came
				// back, so `listed` must stay true: this is the direction that
				// would otherwise report the account's access as denied
				// because OUR write failed.
				st = closedStore(t)
			}
			server := armresourcesfake.ResourceGroupsServer{
				NewListPager: func(_ *armresources.ResourceGroupsClientListOptions) azfake.PagerResponder[armresources.ResourceGroupsClientListResponse] {
					r := azfake.PagerResponder[armresources.ResourceGroupsClientListResponse]{}
					tc.respond(&r)
					return r
				},
			}
			client, err := armresources.NewResourceGroupsClient(testSubID, fakeCred(),
				fakeClientOptions(t, armresourcesfake.NewResourceGroupsServerTransport(&server)))
			if err != nil {
				t.Fatalf("NewResourceGroupsClient: %v", err)
			}

			listed, err := resourceGroupsFromPager(t.Context(), newTestSubscription(testSubID),
				st, testScanID, client.NewListPager(nil))
			if listed != tc.wantListed {
				t.Errorf("listed = %v; want %v", listed, tc.wantListed)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v; want error: %v", err, tc.wantErr)
			}
		})
	}
}
