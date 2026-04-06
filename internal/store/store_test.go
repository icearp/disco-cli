package store

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"testing"
)

// testScanID is the fixed scan ID inserted into every test database.
// All test resources use this as their DiscoveredBy value.
const testScanID = "00000000000000000000000000000000"

// openTestStore opens a temporary SQLite database for testing and inserts a
// scan record so resources can satisfy the discovered_by FK constraint.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("openTestStore: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.db.Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("openTestStore: insert test scan: %v", err)
	}
	return st
}

// sp returns a pointer to the given string.
func sp(s string) *string { return &s }

// --- ResourceID tests ---

// TestResourceID_Algorithm verifies the exact hashing formula used by ResourceID.
// This test documents the algorithm so any change (separator, byte length, encoding)
// causes an immediate failure — a breaking change that would orphan all stored
// relationships in production databases.
func TestResourceID_Algorithm(t *testing.T) {
	provider, accountID, rtype, nativeID := "aws", "123456789012", "aws:ec2:instance", "i-abc123"

	// Re-implement the algorithm independently to lock it in.
	input := provider + "|" + accountID + "|" + rtype + "|" + nativeID
	h := sha256.Sum256([]byte(input))
	want := fmt.Sprintf("%x", h[:16])

	got := ResourceID(provider, accountID, rtype, nativeID)
	if got != want {
		t.Errorf("ResourceID algorithm changed:\n  got:  %s\n  want: %s\nWARNING: this breaks all existing databases", got, want)
	}
	if len(got) != 32 {
		t.Errorf("ResourceID length = %d, want 32", len(got))
	}
}

// TestResourceID_Uniqueness verifies that different inputs produce different IDs.
func TestResourceID_Uniqueness(t *testing.T) {
	base := ResourceID("aws", "acct", "aws:ec2:instance", "i-1")

	cases := []struct {
		name                         string
		provider, account, typ, native string
	}{
		{"different provider", "gcp", "acct", "aws:ec2:instance", "i-1"},
		{"different account", "aws", "other", "aws:ec2:instance", "i-1"},
		{"different type", "aws", "acct", "aws:ec2:vpc", "i-1"},
		{"different native", "aws", "acct", "aws:ec2:instance", "i-2"},
	}
	for _, tc := range cases {
		got := ResourceID(tc.provider, tc.account, tc.typ, tc.native)
		if got == base {
			t.Errorf("%s: ResourceID collision — different inputs produced the same ID %s", tc.name, got)
		}
	}
}

// TestResourceID_Deterministic verifies the same inputs always produce the same ID.
func TestResourceID_Deterministic(t *testing.T) {
	for range 10 {
		got := ResourceID("aws", "acct", "aws:ec2:instance", "i-abc")
		want := ResourceID("aws", "acct", "aws:ec2:instance", "i-abc")
		if got != want {
			t.Fatalf("ResourceID is non-deterministic: %s != %s", got, want)
		}
	}
}

// --- UpsertResources / GetResource tests ---

// TestUpsertResources_RoundTrip verifies that all fields survive a write-then-read cycle.
func TestUpsertResources_RoundTrip(t *testing.T) {
	st := openTestStore(t)

	r := &Resource{
		Provider:       "aws",
		AccountID:      "123456789012",
		AccountName:    sp("My Account"),
		Type:           "aws:ec2:instance",
		NativeID:       "arn:aws:ec2:us-east-1:123456789012:instance/i-abc",
		Name:           sp("web-server"),
		Region:         sp("us-east-1"),
		Zone:           sp("us-east-1a"),
		Status:         sp("running"),
		TagsJSON:       sp(`{"env":"prod","team":"platform"}`),
		AttributesJSON: `{"InstanceId":"i-abc","VpcId":"vpc-123"}`,
		DiscoveredBy:   testScanID,
	}

	if err := st.UpsertResource(r); err != nil {
		t.Fatalf("UpsertResource: %v", err)
	}

	got, err := st.GetResource(r.ID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}

	if got.Provider != r.Provider {
		t.Errorf("Provider: got %q, want %q", got.Provider, r.Provider)
	}
	if got.AccountID != r.AccountID {
		t.Errorf("AccountID: got %q, want %q", got.AccountID, r.AccountID)
	}
	if got.Type != r.Type {
		t.Errorf("Type: got %q, want %q", got.Type, r.Type)
	}
	if got.NativeID != r.NativeID {
		t.Errorf("NativeID: got %q, want %q", got.NativeID, r.NativeID)
	}
	if *got.Name != *r.Name {
		t.Errorf("Name: got %q, want %q", *got.Name, *r.Name)
	}
	if *got.Region != *r.Region {
		t.Errorf("Region: got %q, want %q", *got.Region, *r.Region)
	}
	if *got.Status != *r.Status {
		t.Errorf("Status: got %q, want %q", *got.Status, *r.Status)
	}
	if got.AttributesJSON != r.AttributesJSON {
		t.Errorf("AttributesJSON: got %q, want %q", got.AttributesJSON, r.AttributesJSON)
	}
	if got.TagsJSON == nil || *got.TagsJSON != *r.TagsJSON {
		t.Errorf("TagsJSON: got %v, want %v", got.TagsJSON, r.TagsJSON)
	}
	if got.VerifiedAt == nil {
		t.Error("VerifiedAt should be set by UpsertResources, got nil")
	}
}

// TestUpsertResources_IDAutoComputed verifies that ID is computed when left empty.
func TestUpsertResources_IDAutoComputed(t *testing.T) {
	st := openTestStore(t)

	r := &Resource{
		Provider:       "aws",
		AccountID:      "acct",
		Type:           "aws:ec2:instance",
		NativeID:       "i-123",
		AttributesJSON: "{}",
		DiscoveredBy:   testScanID,
	}
	// Do not set r.ID.
	if err := st.UpsertResource(r); err != nil {
		t.Fatalf("UpsertResource: %v", err)
	}

	want := ResourceID("aws", "acct", "aws:ec2:instance", "i-123")
	if r.ID != want {
		t.Errorf("ID: got %q, want %q", r.ID, want)
	}
}

// TestUpsertResources_OnConflict verifies the upsert merge strategy:
// mutable fields (name, status, tags, attributes, verified_*) are updated;
// discovered_at is NOT overwritten (it records first-seen time).
func TestUpsertResources_OnConflict(t *testing.T) {
	st := openTestStore(t)

	first := &Resource{
		Provider:       "aws",
		AccountID:      "acct",
		Type:           "aws:ec2:instance",
		NativeID:       "i-abc",
		Name:           sp("old-name"),
		Status:         sp("running"),
		AttributesJSON: `{"old":true}`,
		DiscoveredAt:   "2024-01-01T00:00:00Z", // set explicitly to control the value
		DiscoveredBy:   testScanID,
	}
	if err := st.UpsertResource(first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	second := &Resource{
		Provider:       "aws",
		AccountID:      "acct",
		Type:           "aws:ec2:instance",
		NativeID:       "i-abc",
		Name:           sp("new-name"),
		Status:         sp("stopped"),
		AttributesJSON: `{"new":true}`,
		DiscoveredAt:   "2025-06-01T00:00:00Z", // different — should NOT be stored
		DiscoveredBy:   testScanID,
	}
	if err := st.UpsertResource(second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	got, err := st.GetResource(first.ID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}

	if *got.Name != "new-name" {
		t.Errorf("Name should be updated: got %q", *got.Name)
	}
	if *got.Status != "stopped" {
		t.Errorf("Status should be updated: got %q", *got.Status)
	}
	if got.AttributesJSON != `{"new":true}` {
		t.Errorf("AttributesJSON should be updated: got %q", got.AttributesJSON)
	}
	// discovered_at must not be overwritten — it records when the resource was first seen.
	if got.DiscoveredAt != "2024-01-01T00:00:00Z" {
		t.Errorf("DiscoveredAt should be immutable: got %q, want 2024-01-01T00:00:00Z", got.DiscoveredAt)
	}
}

// --- ListResources filter tests ---

// TestListResources_ByProvider verifies provider filter.
func TestListResources_ByProvider(t *testing.T) {
	st := openTestStore(t)
	insertResource(t, st, "aws", "acct", "aws:ec2:instance", "i-1")
	insertResource(t, st, "gcp", "proj", "gcp:compute:instance", "inst-1")

	results, err := st.ListResources(ResourceFilter{Provider: "aws", Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(results) != 1 || results[0].Provider != "aws" {
		t.Errorf("expected 1 aws resource, got %d", len(results))
	}
}

// TestListResources_ByType verifies type filter.
func TestListResources_ByType(t *testing.T) {
	st := openTestStore(t)
	insertResource(t, st, "aws", "acct", "aws:ec2:instance", "i-1")
	insertResource(t, st, "aws", "acct", "aws:ec2:vpc", "vpc-1")

	results, err := st.ListResources(ResourceFilter{Types: []string{"aws:ec2:vpc"}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(results) != 1 || results[0].Type != "aws:ec2:vpc" {
		t.Errorf("expected 1 vpc resource, got %d", len(results))
	}
}

// TestListResources_ByRegion verifies region filter.
func TestListResources_ByRegion(t *testing.T) {
	st := openTestStore(t)
	r1 := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance",
		NativeID: "i-1", Region: sp("us-east-1"), AttributesJSON: "{}", DiscoveredBy: testScanID,
	}
	r2 := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance",
		NativeID: "i-2", Region: sp("eu-west-1"), AttributesJSON: "{}", DiscoveredBy: testScanID,
	}
	if err := st.UpsertResources([]*Resource{r1, r2}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	results, err := st.ListResources(ResourceFilter{Regions: []string{"us-east-1"}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(results) != 1 || *results[0].Region != "us-east-1" {
		t.Errorf("expected 1 us-east-1 resource, got %d", len(results))
	}
}

// TestListResources_ByTagKeyValue verifies json_extract-based tag filtering.
func TestListResources_ByTagKeyValue(t *testing.T) {
	st := openTestStore(t)

	tagged := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:s3:bucket",
		NativeID: "bucket-a", TagsJSON: sp(`{"env":"prod","team":"platform"}`),
		AttributesJSON: "{}", DiscoveredBy: testScanID,
	}
	untagged := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:s3:bucket",
		NativeID: "bucket-b", AttributesJSON: "{}", DiscoveredBy: testScanID,
	}
	if err := st.UpsertResources([]*Resource{tagged, untagged}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	// Filter by tag key + value.
	results, err := st.ListResources(ResourceFilter{TagKey: "env", TagValue: "prod", Limit: 100})
	if err != nil {
		t.Fatalf("ListResources by tag key+value: %v", err)
	}
	if len(results) != 1 || results[0].NativeID != "bucket-a" {
		t.Errorf("expected 1 prod-tagged resource, got %d", len(results))
	}

	// Filter by tag key only (any value).
	results, err = st.ListResources(ResourceFilter{TagKey: "team", Limit: 100})
	if err != nil {
		t.Fatalf("ListResources by tag key only: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 team-tagged resource, got %d", len(results))
	}
}

// TestListResources_Limit verifies the Limit parameter is respected.
func TestListResources_Limit(t *testing.T) {
	st := openTestStore(t)
	for i := 0; i < 5; i++ {
		insertResource(t, st, "aws", "acct", "aws:ec2:instance", fmt.Sprintf("i-%d", i))
	}

	results, err := st.ListResources(ResourceFilter{Limit: 3})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("Limit=3: got %d results", len(results))
	}
}

// --- Relationship tests ---

// TestUpsertRelationship_Idempotent verifies that upserting the same relationship
// twice results in exactly one row (no duplicate).
func TestUpsertRelationship_Idempotent(t *testing.T) {
	st := openTestStore(t)

	fromID := insertResource(t, st, "aws", "acct", "aws:ec2:instance", "i-1")
	toID := insertResource(t, st, "aws", "acct", "aws:ec2:vpc", "vpc-1")

	for range 3 {
		if err := st.UpsertRelationship(fromID, toID, RelAttachedTo, "directed", nil); err != nil {
			t.Fatalf("UpsertRelationship: %v", err)
		}
	}

	rels, err := st.RelationshipsFrom(fromID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected exactly 1 relationship, got %d", len(rels))
	}
}

// TestRelationshipsFrom verifies direction and kind filtering.
func TestRelationshipsFrom(t *testing.T) {
	st := openTestStore(t)

	fromID := insertResource(t, st, "aws", "acct", "aws:ec2:instance", "i-1")
	vpcID := insertResource(t, st, "aws", "acct", "aws:ec2:vpc", "vpc-1")
	sgID := insertResource(t, st, "aws", "acct", "aws:ec2:security-group", "sg-1")

	mustUpsertRel(t, st, fromID, vpcID, RelAttachedTo)
	mustUpsertRel(t, st, fromID, sgID, RelUses)

	// All relationships from fromID.
	all, err := st.RelationshipsFrom(fromID)
	if err != nil {
		t.Fatalf("RelationshipsFrom (all): %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(all))
	}

	// Filter by kind.
	attached, err := st.RelationshipsFrom(fromID, RelAttachedTo)
	if err != nil {
		t.Fatalf("RelationshipsFrom (attached-to): %v", err)
	}
	if len(attached) != 1 || attached[0].ToID != vpcID {
		t.Errorf("RelationshipsFrom filtered by kind: unexpected results %v", attached)
	}
}

// TestRelationshipsTo verifies reverse-direction lookup.
func TestRelationshipsTo(t *testing.T) {
	st := openTestStore(t)

	inst1 := insertResource(t, st, "aws", "acct", "aws:ec2:instance", "i-1")
	inst2 := insertResource(t, st, "aws", "acct", "aws:ec2:instance", "i-2")
	vpcID := insertResource(t, st, "aws", "acct", "aws:ec2:vpc", "vpc-1")

	mustUpsertRel(t, st, inst1, vpcID, RelAttachedTo)
	mustUpsertRel(t, st, inst2, vpcID, RelAttachedTo)

	rels, err := st.RelationshipsTo(vpcID)
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relationships pointing to vpc, got %d", len(rels))
	}
}

// --- Hierarchy closure tests ---

// TestBatchAddToHierarchyClosure verifies multi-level ancestor derivation.
// Given org → folder → project, DescendantsOf(org) must return both folder
// and project; DescendantsOf(folder) must return only project.
func TestBatchAddToHierarchyClosure(t *testing.T) {
	st := openTestStore(t)

	orgID := insertResource(t, st, "gcp", "p1", "gcp:cloudresourcemanager:organization", "org-1")
	folderID := insertResource(t, st, "gcp", "p1", "gcp:cloudresourcemanager:folder", "folder-1")
	projID := insertResource(t, st, "gcp", "p1", "gcp:cloudresourcemanager:project", "proj-1")

	// Build the hierarchy: org is the root (self-reference), folder under org, project under folder.
	pairs := [][2]string{
		{orgID, orgID},      // root self-entry
		{folderID, orgID},   // folder → org
		{projID, folderID},  // project → folder
	}
	if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
		t.Fatalf("BatchAddToHierarchyClosure: %v", err)
	}

	// Descendants of org should include folder and project.
	orgDesc, err := st.DescendantsOf(orgID, ResourceFilter{})
	if err != nil {
		t.Fatalf("DescendantsOf(org): %v", err)
	}
	if len(orgDesc) != 2 {
		t.Errorf("DescendantsOf(org): expected 2, got %d", len(orgDesc))
	}

	// Descendants of folder should include only project.
	folderDesc, err := st.DescendantsOf(folderID, ResourceFilter{})
	if err != nil {
		t.Fatalf("DescendantsOf(folder): %v", err)
	}
	if len(folderDesc) != 1 || folderDesc[0].ID != projID {
		t.Errorf("DescendantsOf(folder): expected [project], got %v", folderDesc)
	}

	// Descendants of leaf project should be empty.
	projDesc, err := st.DescendantsOf(projID, ResourceFilter{})
	if err != nil {
		t.Fatalf("DescendantsOf(project): %v", err)
	}
	if len(projDesc) != 0 {
		t.Errorf("DescendantsOf(project): expected 0, got %d", len(projDesc))
	}
}

// TestAddToHierarchyClosure_TypeFilter verifies that DescendantsOf type filter works.
func TestAddToHierarchyClosure_TypeFilter(t *testing.T) {
	st := openTestStore(t)

	parentID := insertResource(t, st, "gcp", "p1", "gcp:cloudresourcemanager:folder", "f-1")
	childA := insertResource(t, st, "gcp", "p1", "gcp:compute:instance", "inst-1")
	childB := insertResource(t, st, "gcp", "p1", "gcp:storage:bucket", "bucket-1")

	if err := st.BatchAddToHierarchyClosure([][2]string{
		{parentID, parentID},
		{childA, parentID},
		{childB, parentID},
	}); err != nil {
		t.Fatalf("BatchAddToHierarchyClosure: %v", err)
	}

	results, err := st.DescendantsOf(parentID, ResourceFilter{Types: []string{"gcp:compute:instance"}})
	if err != nil {
		t.Fatalf("DescendantsOf with type filter: %v", err)
	}
	if len(results) != 1 || results[0].Type != "gcp:compute:instance" {
		t.Errorf("expected 1 compute instance descendant, got %d", len(results))
	}
}

// --- helpers used in tests above ---

// insertResource upserts a minimal resource and returns its computed ID.
func insertResource(t *testing.T, st *Store, provider, accountID, rtype, nativeID string) string {
	t.Helper()
	r := &Resource{
		Provider:       provider,
		AccountID:      accountID,
		Type:           rtype,
		NativeID:       nativeID,
		AttributesJSON: "{}",
		DiscoveredBy:   testScanID,
	}
	if err := st.UpsertResource(r); err != nil {
		t.Fatalf("insertResource %s/%s: %v", rtype, nativeID, err)
	}
	return r.ID
}

// mustUpsertRel upserts a directed relationship and fails the test on error.
func mustUpsertRel(t *testing.T, st *Store, fromID, toID, kind string) {
	t.Helper()
	if err := st.UpsertRelationship(fromID, toID, kind, "directed", nil); err != nil {
		t.Fatalf("UpsertRelationship %s -[%s]-> %s: %v", fromID, kind, toID, err)
	}
}
