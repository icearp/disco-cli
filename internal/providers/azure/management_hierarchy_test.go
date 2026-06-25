package azure

import (
	"net/http"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managementgroups/armmanagementgroups"
	armmgmtfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managementgroups/armmanagementgroups/fake"
)

const (
	hierTenant = "tenant-guid"
	hierSub    = "00000000-0000-0000-0000-0000000000aa"
	rootMGID   = "/providers/Microsoft.Management/managementGroups/root"
	childMGID  = "/providers/Microsoft.Management/managementGroups/child"
	hierSubID  = "/subscriptions/" + hierSub
	hierRGID   = hierSubID + "/resourceGroups/rg1"
)

// entitiesServer returns a fake Entities transport yielding the tenant tree:
// child MG parented to root, the subscription parented to child, plus a root MG
// with no parent and an out-of-scope subscription parented to child (neither in
// the store) to prove both are skipped.
func entitiesServer() armmgmtfake.EntitiesServer {
	return armmgmtfake.EntitiesServer{
		NewListPager: func(_ *armmanagementgroups.EntitiesClientListOptions) fake.PagerResponder[armmanagementgroups.EntitiesClientListResponse] {
			r := fake.PagerResponder[armmanagementgroups.EntitiesClientListResponse]{}
			r.AddPage(http.StatusOK, armmanagementgroups.EntitiesClientListResponse{
				EntityListResult: armmanagementgroups.EntityListResult{
					Value: []*armmanagementgroups.EntityInfo{
						{ID: to.Ptr(rootMGID)}, // root: no parent → skipped
						{ID: to.Ptr(childMGID), Properties: &armmanagementgroups.EntityInfoProperties{
							Parent:          &armmanagementgroups.EntityParentGroupInfo{ID: to.Ptr(rootMGID)},
							ParentNameChain: []*string{to.Ptr("root")},
						}},
						{ID: to.Ptr(hierSubID), Properties: &armmanagementgroups.EntityInfoProperties{
							Parent:          &armmanagementgroups.EntityParentGroupInfo{ID: to.Ptr(childMGID)},
							ParentNameChain: []*string{to.Ptr("root"), to.Ptr("child")},
						}},
						{ID: to.Ptr("/subscriptions/out-of-scope"), Properties: &armmanagementgroups.EntityInfoProperties{
							Parent:          &armmanagementgroups.EntityParentGroupInfo{ID: to.Ptr(childMGID)},
							ParentNameChain: []*string{to.Ptr("root"), to.Ptr("child")},
						}},
					},
				},
			}, nil)
			return r
		},
	}
}

func entitiesClient(t *testing.T, srv armmgmtfake.EntitiesServer) *armmanagementgroups.EntitiesClient {
	t.Helper()
	c, err := armmanagementgroups.NewEntitiesClient(fakeCred(), fakeClientOptions(t, armmgmtfake.NewEntitiesServerTransport(&srv)))
	if err != nil {
		t.Fatalf("NewEntitiesClient: %v", err)
	}
	return c
}

// TestStitchTopHierarchy_LinksAllTiers is the end-to-end contract: after seeding
// the MG/subscription/RG rows and recording every pair the two tier helpers
// return, the closure carries parent -[contains]-> child for all three top tiers
// and the canonical direction holds (newTestStore's cleanup fails on any
// reversed contains edge).
func TestStitchTopHierarchy_LinksAllTiers(t *testing.T) {
	st := newTestStore(t)
	rootID := upsertTestResource(t, st, "azure", hierTenant, TypeManagementGroup, rootMGID, "global", "{}")
	childID := upsertTestResource(t, st, "azure", hierTenant, TypeManagementGroup, childMGID, "global", "{}")
	subID := upsertTestResource(t, st, "azure", hierSub, TypeSubscription, hierSubID, "", "{}")
	rgID := upsertTestResource(t, st, "azure", hierSub, TypeResourcesResourceGroup, hierRGID, "eastus", "{}")

	subIndex, err := storeNativeIDIndex(st, TypeSubscription)
	if err != nil {
		t.Fatalf("storeNativeIDIndex(sub): %v", err)
	}
	mgIndex, err := storeNativeIDIndex(st, TypeManagementGroup)
	if err != nil {
		t.Fatalf("storeNativeIDIndex(mg): %v", err)
	}

	mgPairs, err := managementParentPairsWithClient(t.Context(), entitiesClient(t, entitiesServer()), *newTestSubscription(hierSub), mgIndex, subIndex, st)
	if err != nil {
		t.Fatalf("managementParentPairsWithClient: %v", err)
	}
	// Exercise the real assembly+record path (self-seed + ordering live here).
	recordTopHierarchy(st, mgIndex, subIndex, mgPairs)

	// Topology: root MG → child MG → subscription → resource group.
	assertContains := func(parent, child, label string) {
		t.Helper()
		rels, err := st.RelationshipsFrom(parent, "contains")
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", label, err)
		}
		for _, r := range rels {
			if r.ToID == child {
				return
			}
		}
		t.Errorf("%s: no contains edge %s -> %s (got %d edges)", label, parent, child, len(rels))
	}
	assertContains(rootID, childID, "MG->MG")
	assertContains(childID, subID, "MG->subscription")
	assertContains(subID, rgID, "subscription->RG")

	// Closure depth chains: DescendantsOf walks the transitive closure (depth>0),
	// so the root must reach all three descendants, the child two, the sub one.
	// This is the regression the contains-only assertions above miss — without
	// the root self-seed + ancestor ordering the closure stays depth-0 only.
	assertDescendants := func(ancestor, label string, want int) {
		t.Helper()
		desc, err := st.DescendantsOf(ancestor, store.ResourceFilter{})
		if err != nil {
			t.Fatalf("DescendantsOf(%s): %v", label, err)
		}
		if len(desc) != want {
			t.Errorf("DescendantsOf(%s) = %d, want %d", label, len(desc), want)
		}
	}
	assertDescendants(rootID, "root", 3)   // child MG, subscription, RG
	assertDescendants(childID, "child", 2) // subscription, RG
	assertDescendants(subID, "sub", 1)     // RG
}

// TestManagementParentPairs_SkipsUnknownEndpoints proves the entity matcher emits
// pairs only when BOTH endpoints are in store: the out-of-scope subscription
// (parent present, child absent) and the parentless root are skipped.
func TestManagementParentPairs_SkipsUnknownEndpoints(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "azure", hierTenant, TypeManagementGroup, rootMGID, "global", "{}")
	upsertTestResource(t, st, "azure", hierTenant, TypeManagementGroup, childMGID, "global", "{}")
	upsertTestResource(t, st, "azure", hierSub, TypeSubscription, hierSubID, "", "{}")

	subIndex, _ := storeNativeIDIndex(st, TypeSubscription)
	mgIndex, _ := storeNativeIDIndex(st, TypeManagementGroup)

	pairs, err := managementParentPairsWithClient(t.Context(), entitiesClient(t, entitiesServer()), *newTestSubscription(hierSub), mgIndex, subIndex, st)
	if err != nil {
		t.Fatalf("managementParentPairsWithClient: %v", err)
	}
	// child->root and sub->child only; root (no parent) and out-of-scope sub dropped.
	if len(pairs) != 2 {
		t.Fatalf("got %d pairs, want 2 (child->root, sub->child): %v", len(pairs), pairs)
	}
}

// TestManagementParentPairs_AccessDeniedTolerated proves a 403 on the Entities
// list degrades to an empty result with no error, so the RG tier (collected
// without the API) still records when tenant management read is missing.
func TestManagementParentPairs_AccessDeniedTolerated(t *testing.T) {
	st := newTestStore(t)
	srv := armmgmtfake.EntitiesServer{
		NewListPager: func(_ *armmanagementgroups.EntitiesClientListOptions) fake.PagerResponder[armmanagementgroups.EntitiesClientListResponse] {
			r := fake.PagerResponder[armmanagementgroups.EntitiesClientListResponse]{}
			r.AddResponseError(http.StatusForbidden, "AuthorizationFailed")
			return r
		},
	}
	pairs, err := managementParentPairsWithClient(t.Context(), entitiesClient(t, srv), *newTestSubscription(hierSub), map[string]string{}, map[string]string{}, st)
	if err != nil {
		t.Fatalf("AccessDenied should be tolerated, got: %v", err)
	}
	if len(pairs) != 0 {
		t.Fatalf("got %d pairs on 403, want 0", len(pairs))
	}
}

// TestResourceGroupParentPairs_SkipsOrphan covers the no-subscription case: an RG
// whose subscription resource was never stored produces no pair (no dangling
// parent), while an RG with a stored subscription links.
func TestResourceGroupParentPairs_SkipsOrphan(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "azure", hierSub, TypeSubscription, hierSubID, "", "{}")
	linkedRG := upsertTestResource(t, st, "azure", hierSub, TypeResourcesResourceGroup, hierRGID, "eastus", "{}")
	// RG under a subscription that was not scanned as a resource.
	orphanSub := "11111111-1111-1111-1111-111111111111"
	upsertTestResource(t, st, "azure", orphanSub, TypeResourcesResourceGroup,
		"/subscriptions/"+orphanSub+"/resourceGroups/orphan", "eastus", "{}")

	subIndex, _ := storeNativeIDIndex(st, TypeSubscription)
	pairs, err := resourceGroupParentPairs(st, subIndex)
	if err != nil {
		t.Fatalf("resourceGroupParentPairs: %v", err)
	}
	if len(pairs) != 1 || pairs[0][0] != linkedRG {
		t.Fatalf("got %v, want exactly the linked RG %s paired", pairs, linkedRG)
	}
}
