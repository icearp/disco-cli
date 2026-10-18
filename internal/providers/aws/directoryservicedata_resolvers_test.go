package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

// Each user and group is contained by the stored directory its NativeID
// names; a child of an unstored directory gets no edge, and no pair is
// offered for it (the store would warn about its missing parent).
func TestResolveDSDataHierarchy_LinksStoredDirectories(t *testing.T) {
	st := newTestStore(t)
	missingParent := countOpWarnings(st, "hierarchy")
	ms, gone := dsDirARN("d-ms"), dsDirARN("d-gone")
	upsertTestResource(t, st, "aws", testAccountID, TypeDSMicrosoftAD, ms, testRegion, "{}")
	user, group, orphan := ms+"/user/"+dsSID("1104"), ms+"/group/S-1-5-32-544", gone+"/user/"+dsSID("1104")
	upsertTestResource(t, st, "aws", testAccountID, TypeDSDataUser, user, testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeDSDataUser, orphan, testRegion, "{}")
	// Well-known groups are provider-managed; the resolver must still see them.
	if _, err := st.UpsertResources([]*store.Resource{{
		Provider: "aws", AccountID: testAccountID, Type: TypeDSDataGroup, NativeID: group,
		ManagedByProvider: true, AttributesJSON: "{}", DiscoveredBy: testScanID,
	}}); err != nil {
		t.Fatalf("upsert group: %v", err)
	}
	if err := resolveDSDataHierarchy(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	assertContainsNative(t, st, ms, user)
	assertContainsNative(t, st, ms, group)
	if rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, gone), store.RelContains); err != nil || len(rels) != 0 || *missingParent != 0 {
		t.Errorf("edges from the unstored directory = %v, %v, missing-parent warnings %d; want none, 0", rels, err, *missingParent)
	}
	rels, err := st.ListRelationships()
	if err != nil || len(rels) != 2 {
		t.Errorf("relationships = %d, %v; want the 2 contains edges", len(rels), err)
	}
	assertContainsEdgesDeclared(t, st)
}

// With no users or groups stored the resolver records nothing.
func TestResolveDSDataHierarchy_NoRows(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeDSMicrosoftAD, dsDirARN("d-ms"), testRegion, "{}")
	if err := resolveDSDataHierarchy(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rels, err := st.ListRelationships(); err != nil || len(rels) != 0 {
		t.Errorf("relationships = %d, %v; want none", len(rels), err)
	}
}

// A NativeID that names no directory is returned as an error, and the rows
// after it are still linked. The bad row is unnamed so it lists first.
func TestResolveDSDataHierarchy_BadNativeIDReportedOthersLinked(t *testing.T) {
	st := newTestStore(t)
	ms := dsDirARN("d-ms")
	good := ms + "/user/" + dsSID("1104")
	upsertTestResource(t, st, "aws", testAccountID, TypeDSMicrosoftAD, ms, testRegion, "{}")
	upsertTestResource(t, st, "aws", testAccountID, TypeDSDataUser, "not-a-user-id", testRegion, "{}")
	name := "zz-jdoe"
	if _, err := st.UpsertResources([]*store.Resource{{
		Provider: "aws", AccountID: testAccountID, Type: TypeDSDataUser, NativeID: good, Name: &name,
		AttributesJSON: "{}", DiscoveredBy: testScanID,
	}}); err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	if err := resolveDSDataHierarchy(newTestAccount(testAccountID), st); err == nil {
		t.Error("resolve = nil, want the bad NativeID reported")
	}
	assertContainsNative(t, st, ms, good)
}

// More children than one page are all linked, a page at a time.
func TestResolveDSDataHierarchy_LinksEveryPage(t *testing.T) {
	old := dsDataResolvePageSize
	dsDataResolvePageSize = 2
	t.Cleanup(func() { dsDataResolvePageSize = old })
	st := newTestStore(t)
	ms := dsDirARN("d-ms")
	upsertTestResource(t, st, "aws", testAccountID, TypeDSMicrosoftAD, ms, testRegion, "{}")
	var users []string
	for _, rid := range []string{"1101", "1102", "1103", "1104", "1105"} {
		users = append(users, ms+"/user/"+dsSID(rid))
		upsertTestResource(t, st, "aws", testAccountID, TypeDSDataUser, users[len(users)-1], testRegion, "{}")
	}
	if err := resolveDSDataHierarchy(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, ms), store.RelContains)
	if err != nil || len(rels) != len(users) {
		t.Errorf("contains edges = %d, %v; want %d", len(rels), err, len(users))
	}
	for _, u := range users {
		assertContainsNative(t, st, ms, u)
	}
}
