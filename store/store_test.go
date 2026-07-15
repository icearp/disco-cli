package store

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.db.Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("openTestStore: insert test scan: %v", err)
	}
	return st
}

// sp returns a pointer to the given string.
func sp(s string) *string { return &s }

// TestOpenReadOnly_Reads guards F20: read-only mode opens a populated DB
// and surfaces existing rows via the normal query path.
func TestOpenReadOnly_Reads(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ro.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("insert scan: %v", err)
	}
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "111", Type: "aws:s3:bucket",
		NativeID: "b-1", AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	_ = st.Close()

	ro, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	rows, err := ro.ListResources(ResourceFilter{IncludeManaged: true, Limit: 10})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("want 1 row from RO store, got %d", len(rows))
	}
}

// TestOpenReadOnly_RejectsWrite proves the structural guarantee — any write
// through a read-only store hits SQLite's "readonly database" error.
func TestOpenReadOnly_RejectsWrite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ro.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("insert scan: %v", err)
	}
	_ = st.Close()

	ro, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close() })

	_, err = ro.UpsertResource(&Resource{
		Provider: "aws", AccountID: "111", Type: "aws:s3:bucket",
		NativeID: "b-1", AttributesJSON: "{}", DiscoveredBy: testScanID,
	})
	if err == nil {
		t.Fatalf("write succeeded against RO store; want failure")
	}
	if !strings.Contains(err.Error(), "readonly") {
		t.Errorf("want readonly error, got: %v", err)
	}
}

// --- Scan lifecycle tests ---

// TestPartialScan verifies that PartialScan sets the 'partial' status, records
// the resource count, and persists the combined error message.
func TestPartialScan(t *testing.T) {
	st := openTestStore(t)

	id, err := st.CreateScan([]string{"aws", "gcp"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	if err := st.PartialScan(id, 42, "gcp: permission denied"); err != nil {
		t.Fatalf("PartialScan: %v", err)
	}

	sc, err := st.GetScan(id)
	if err != nil {
		t.Fatalf("GetScan: %v", err)
	}
	if sc.Status != "partial" {
		t.Errorf("Status: got %q, want partial", sc.Status)
	}
	if sc.ResourceCount == nil || *sc.ResourceCount != 42 {
		t.Errorf("ResourceCount: got %v, want 42", sc.ResourceCount)
	}
	if sc.Error == nil || *sc.Error != "gcp: permission denied" {
		t.Errorf("Error: got %v, want %q", sc.Error, "gcp: permission denied")
	}
	if sc.FinishedAt == nil {
		t.Error("FinishedAt should be set by PartialScan, got nil")
	}
}

// TestListResources_Since asserts the Since clause filters by discovered_at,
// using lexicographic comparison on the RFC3339 string. Seed two rows with
// explicit DiscoveredAt timestamps either side of the cutoff.
func TestListResources_Since(t *testing.T) {
	st := openTestStore(t)
	old := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance",
		NativeID: "i-old", AttributesJSON: "{}",
		DiscoveredAt: "2026-01-01T00:00:00Z", DiscoveredBy: testScanID,
	}
	fresh := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance",
		NativeID: "i-new", AttributesJSON: "{}",
		DiscoveredAt: "2026-05-01T00:00:00Z", DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResources([]*Resource{old, fresh}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	results, err := st.ListResources(ResourceFilter{
		DiscoveredSince: "2026-04-01T00:00:00Z",
		Limit:           100,
	})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(results) != 1 || results[0].NativeID != "i-new" {
		t.Errorf("DiscoveredSince filter: got %+v, want only i-new", results)
	}
}

// TestListResources_CreatedSinceBefore asserts the CreatedSince /
// CreatedBefore clauses filter on the resource's intrinsic created_at
// column (NOT discovered_at). Rows with NULL created_at are excluded
// from both filters — matches the documented contract.
func TestListResources_CreatedSinceBefore(t *testing.T) {
	st := openTestStore(t)
	old := "2024-06-01T00:00:00Z"
	mid := "2025-06-01T00:00:00Z"
	rOld := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:volume",
		NativeID: "vol-old", AttributesJSON: "{}", CreatedAt: &old,
		DiscoveredBy: testScanID,
	}
	rMid := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:volume",
		NativeID: "vol-mid", AttributesJSON: "{}", CreatedAt: &mid,
		DiscoveredBy: testScanID,
	}
	rNoTS := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:volume",
		NativeID: "vol-no-ts", AttributesJSON: "{}",
		DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResources([]*Resource{rOld, rMid, rNoTS}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	before, err := st.ListResources(ResourceFilter{
		CreatedBefore: "2025-01-01T00:00:00Z", Limit: 100,
	})
	if err != nil {
		t.Fatalf("CreatedBefore: %v", err)
	}
	if len(before) != 1 || before[0].NativeID != "vol-old" {
		t.Errorf("CreatedBefore: got %v, want only vol-old", before)
	}

	after, err := st.ListResources(ResourceFilter{
		CreatedSince: "2025-01-01T00:00:00Z", Limit: 100,
	})
	if err != nil {
		t.Fatalf("CreatedSince: %v", err)
	}
	if len(after) != 1 || after[0].NativeID != "vol-mid" {
		t.Errorf("CreatedSince: got %v, want only vol-mid", after)
	}

	// Half-open [since, before) returns both timestamped rows; vol-no-ts
	// stays excluded because NULL < anything is unknown in SQL.
	closed, err := st.ListResources(ResourceFilter{
		CreatedSince: "2024-01-01T00:00:00Z", CreatedBefore: "2026-01-01T00:00:00Z", Limit: 100,
	})
	if err != nil {
		t.Fatalf("closed interval: %v", err)
	}
	if len(closed) != 2 {
		t.Errorf("closed interval: got %d rows, want 2", len(closed))
	}
}

// TestListScans_Providers asserts ListScans unmarshals ProvidersJSON into
// the Providers slice for every returned row — auditor-facing rendering
// reads the slice directly without a follow-up GetScan call.
func TestListScans_Providers(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.CreateScan([]string{"aws", "gcp"}, map[string]any{}); err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	scans, err := st.ListScans()
	if err != nil {
		t.Fatalf("ListScans: %v", err)
	}
	if len(scans) == 0 {
		t.Fatal("want at least 1 scan, got 0")
	}
	found := false
	for _, sc := range scans {
		if len(sc.Providers) == 2 && sc.Providers[0] == "aws" && sc.Providers[1] == "gcp" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no scan with providers [aws gcp] found: %+v", scans)
	}
}

// --- ResourceID tests ---

// TestResourceID_Algorithm verifies the exact hashing formula used by ResourceID.
// This test documents the algorithm so any change (separator, byte length, encoding)
// causes an immediate failure — a breaking change that would orphan all stored
// relationships in production databases.
func TestResourceID_Algorithm(t *testing.T) {
	provider, accountID, nativeID := "aws", "123456789012", "i-abc123"

	// Re-implement the algorithm independently to lock it in. type is deliberately
	// NOT part of identity — see ResourceID.
	input := provider + "|" + accountID + "|" + nativeID
	h := sha256.Sum256([]byte(input))
	want := fmt.Sprintf("%x", h[:16])

	got := ResourceID(provider, accountID, nativeID)
	if got != want {
		t.Errorf("ResourceID algorithm changed:\n  got:  %s\n  want: %s\nWARNING: this breaks all existing databases", got, want)
	}
	if len(got) != 32 {
		t.Errorf("ResourceID length = %d, want 32", len(got))
	}
}

// TestResourceID_Uniqueness verifies that different inputs produce different IDs.
// type is not an input — a type change keeps the same ID and supersedes (see
// TestVersioning_TypeChange_SupersedesNotForks).
func TestResourceID_Uniqueness(t *testing.T) {
	base := ResourceID("aws", "acct", "i-1")

	cases := []struct {
		name                      string
		provider, account, native string
	}{
		{"different provider", "gcp", "acct", "i-1"},
		{"different account", "aws", "other", "i-1"},
		{"different native", "aws", "acct", "i-2"},
	}
	for _, tc := range cases {
		got := ResourceID(tc.provider, tc.account, tc.native)
		if got == base {
			t.Errorf("%s: ResourceID collision — different inputs produced the same ID %s", tc.name, got)
		}
	}
}

// TestResourceID_Deterministic verifies the same inputs always produce the same ID.
func TestResourceID_Deterministic(t *testing.T) {
	for range 10 {
		got := ResourceID("aws", "acct", "i-abc")
		want := ResourceID("aws", "acct", "i-abc")
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

	if _, err := st.UpsertResource(r); err != nil {
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
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("UpsertResource: %v", err)
	}

	want := ResourceID("aws", "acct", "i-123")
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
	if _, err := st.UpsertResource(first); err != nil {
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
	if _, err := st.UpsertResource(second); err != nil {
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

	results, err := st.ListResources(ResourceFilter{Providers: []string{"aws"}, Limit: 100})
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

// TestListResources_ExcludeTypes verifies the ExcludeTypes denylist clause
// drops named types from the result set without affecting Types include.
func TestListResources_ExcludeTypes(t *testing.T) {
	st := openTestStore(t)
	insertResource(t, st, "aws", "acct", "aws:ec2:instance", "i-1")
	insertResource(t, st, "aws", "acct", "aws:ec2:vpc", "vpc-1")
	insertResource(t, st, "aws", "acct", "aws:logs:log-stream", "ls-1")

	results, err := st.ListResources(ResourceFilter{
		ExcludeTypes: []string{"aws:logs:log-stream"},
		Limit:        100,
	})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 rows after exclude, got %d", len(results))
	}
	for _, r := range results {
		if r.Type == "aws:logs:log-stream" {
			t.Errorf("excluded type leaked into results: %+v", r)
		}
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
	if _, err := st.UpsertResources([]*Resource{r1, r2}); err != nil {
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

// TestListResources_HidesManagedByDefault verifies provider-managed resources
// are filtered out unless IncludeManaged is set, and that the flag round-trips.
func TestListResources_HidesManagedByDefault(t *testing.T) {
	st := openTestStore(t)

	user := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:prefix-list",
		NativeID: "pl-user", AttributesJSON: "{}", DiscoveredBy: testScanID,
	}
	managed := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:prefix-list",
		NativeID: "pl-aws", AttributesJSON: "{}", DiscoveredBy: testScanID,
		ManagedByProvider: true,
	}
	if _, err := st.UpsertResources([]*Resource{user, managed}); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}

	got, err := st.ListResources(ResourceFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListResources default: %v", err)
	}
	if len(got) != 1 || got[0].NativeID != "pl-user" {
		t.Errorf("default list: got %+v, want only user resource", got)
	}
	if got[0].ManagedByProvider {
		t.Errorf("user resource should not be flagged managed: %+v", got[0])
	}

	got, err = st.ListResources(ResourceFilter{Limit: 100, IncludeManaged: true})
	if err != nil {
		t.Fatalf("ListResources include: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("IncludeManaged: got %d, want 2", len(got))
	}
	var sawManaged bool
	for _, r := range got {
		if r.NativeID == "pl-aws" && r.ManagedByProvider {
			sawManaged = true
		}
	}
	if !sawManaged {
		t.Errorf("managed flag did not round-trip: %+v", got)
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
	if _, err := st.UpsertResources([]*Resource{tagged, untagged}); err != nil {
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

// TestRecordHierarchyBatch verifies multi-level ancestor derivation.
// Given org → folder → project, DescendantsOf(org) must return both folder
// and project; DescendantsOf(folder) must return only project.
func TestRecordHierarchyBatch(t *testing.T) {
	st := openTestStore(t)

	orgID := insertResource(t, st, "gcp", "p1", "gcp:cloudresourcemanager:organization", "org-1")
	folderID := insertResource(t, st, "gcp", "p1", "gcp:cloudresourcemanager:folder", "folder-1")
	projID := insertResource(t, st, "gcp", "p1", "gcp:cloudresourcemanager:project", "proj-1")

	// Build the hierarchy: org is the root (self-reference), folder under org, project under folder.
	pairs := [][2]string{
		{orgID, orgID},     // root self-entry
		{folderID, orgID},  // folder → org
		{projID, folderID}, // project → folder
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		t.Fatalf("RecordHierarchyBatch: %v", err)
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

// TestRecordHierarchy_TypeFilter verifies that DescendantsOf type filter works.
func TestRecordHierarchy_TypeFilter(t *testing.T) {
	st := openTestStore(t)

	parentID := insertResource(t, st, "gcp", "p1", "gcp:cloudresourcemanager:folder", "f-1")
	childA := insertResource(t, st, "gcp", "p1", "gcp:compute:instance", "inst-1")
	childB := insertResource(t, st, "gcp", "p1", "gcp:storage:bucket", "bucket-1")

	if err := st.RecordHierarchyBatch([][2]string{
		{parentID, parentID},
		{childA, parentID},
		{childB, parentID},
	}); err != nil {
		t.Fatalf("RecordHierarchyBatch: %v", err)
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
	if _, err := st.UpsertResource(r); err != nil {
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

// TestReportWarning verifies OnWarn receives each reported ScanWarning and
// that calling ReportWarning without a handler is a no-op.
func TestReportWarning(t *testing.T) {
	st := openTestStore(t)

	// No handler installed: must not panic.
	st.ReportWarning(ScanWarning{Provider: "aws", Service: "kms", Scope: "123", Message: "denied"})

	var got []ScanWarning
	st.OnWarn = func(w ScanWarning) { got = append(got, w) }

	st.ReportWarning(ScanWarning{Provider: "aws", Service: "kms:ListKeys", Scope: "123/us-east-1", Message: "AccessDenied"})
	st.ReportWarning(ScanWarning{Provider: "gcp", Service: "storage", Scope: "proj-1", Message: "403 forbidden"})

	if len(got) != 2 {
		t.Fatalf("expected 2 warnings, got %d", len(got))
	}
	if got[0].Provider != "aws" || got[0].Service != "kms:ListKeys" {
		t.Errorf("warning[0] mismatch: %+v", got[0])
	}
	if got[1].Provider != "gcp" || got[1].Scope != "proj-1" {
		t.Errorf("warning[1] mismatch: %+v", got[1])
	}
}

// TestOpen_DBFilePermissions verifies that the SQLite database file is
// created with 0600 permissions so stored cloud metadata + scrubbed
// attributes aren't group/world-readable under a default umask.
func TestOpen_DBFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "perms.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("DB file perms: got %o, want 0600", got)
	}
}

// TestReversedContainsEdges_DetectsAndIgnores covers the two cases the
// invariant query must handle: a reversed contains row (child→parent
// matches the closure ancestor pair) is returned; a properly-directed
// row is not. Edges with no closure pairing are ignored.
func TestReversedContainsEdges_DetectsAndIgnores(t *testing.T) {
	st := openTestStore(t)

	parentID := insertResource(t, st, "aws", "acct", "aws:elasticfilesystem:file-system", "fs-1")
	childID := insertResource(t, st, "aws", "acct", "aws:elasticfilesystem:mount-target", "fsmt-1")

	// Seed parent's self-entry first (depth 0) so the child→parent
	// closure walk has something to extend; matches scan-time order
	// where parent's closure entry lands before each child's.
	if err := st.RecordHierarchy(parentID, parentID); err != nil {
		t.Fatalf("seed parent closure: %v", err)
	}
	if err := st.RecordHierarchy(childID, parentID); err != nil {
		t.Fatalf("RecordHierarchy: %v", err)
	}

	// Reversed edge: from=child, to=parent. ReversedContainsEdges must
	// surface it.
	mustUpsertRel(t, st, childID, parentID, RelContains)
	got, err := st.ReversedContainsEdges()
	if err != nil {
		t.Fatalf("ReversedContainsEdges: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("reversed: got %d rows, want 1", len(got))
	}
	if got[0].FromID != childID || got[0].ToID != parentID {
		t.Errorf("row mismatch: %+v", got[0])
	}

	// Add a second pair with the canonical direction; existing reversed
	// row remains the only flagged result.
	parent2 := insertResource(t, st, "aws", "acct", "aws:s3:bucket", "b-1")
	child2 := insertResource(t, st, "aws", "acct", "aws:s3:bucket-policy", "p-1")
	if err := st.RecordHierarchy(parent2, parent2); err != nil {
		t.Fatalf("seed parent2 closure: %v", err)
	}
	if err := st.RecordHierarchy(child2, parent2); err != nil {
		t.Fatalf("RecordHierarchy 2: %v", err)
	}
	mustUpsertRel(t, st, parent2, child2, RelContains) // correct direction
	got, _ = st.ReversedContainsEdges()
	if len(got) != 1 {
		t.Errorf("after canonical add: got %d rows, want still 1", len(got))
	}
}

// TestRecordHierarchy_WritesRelationshipRow asserts the unified
// closure writer also records a parent→child contains row. Without this,
// `disco graph` walks (which read only `relationships`) miss every Azure
// or GCP hierarchy edge.
func TestRecordHierarchy_WritesRelationshipRow(t *testing.T) {
	st := openTestStore(t)

	parentID := insertResource(t, st, "azure", "sub", "azure:microsoft.resources:resource-groups", "rg-1")
	childID := insertResource(t, st, "azure", "sub", "azure:microsoft.compute:virtual-machines", "vm-1")

	if err := st.RecordHierarchy(parentID, parentID); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if err := st.RecordHierarchy(childID, parentID); err != nil {
		t.Fatalf("RecordHierarchy: %v", err)
	}

	rels, err := st.RelationshipsFrom(parentID, RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != childID {
		t.Errorf("want 1 contains row parent→child, got %+v", rels)
	}
}

// TestRecordHierarchy_SkipsMissingResource confirms the EXISTS guard:
// when parent resource isn't upserted, closure entries still write and
// no relationship row is created. A ScanWarning fires so operators see
// the drift instead of it being a silent no-op.
func TestRecordHierarchy_SkipsMissingResource(t *testing.T) {
	st := openTestStore(t)

	var warns []ScanWarning
	st.OnWarn = func(w ScanWarning) { warns = append(warns, w) }

	childID := insertResource(t, st, "azure", "sub", "azure:microsoft.compute:virtual-machines", "vm-orphan")
	missingParentID := "deadbeef00000000000000000000000a"

	if err := st.RecordHierarchy(childID, missingParentID); err != nil {
		t.Fatalf("RecordHierarchy: %v", err)
	}

	rels, _ := st.RelationshipsTo(childID, RelContains)
	if len(rels) != 0 {
		t.Errorf("expected no relationship row when parent absent, got %+v", rels)
	}
	if len(warns) != 1 || warns[0].Service != "hierarchy" {
		t.Errorf("expected one hierarchy warning, got %+v", warns)
	}
}
