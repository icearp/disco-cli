package store

import (
	"testing"
)

// ensureTestScan inserts a stub scan row so DiscoveredBy/VerifiedBy
// FK constraints are satisfied. Idempotent — OR IGNORE swallows the
// duplicate on repeat calls.
func ensureTestScan(t *testing.T, st *Store, id string) {
	t.Helper()
	if _, err := st.db.Exec(
		`INSERT OR IGNORE INTO scans (id, started_at, status, providers, scope)
		 VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, id); err != nil {
		t.Fatalf("ensureTestScan: %v", err)
	}
}

// upsertOne drives UpsertResource with the natural-key fields filled
// in. Returns the deterministic ResourceID hash for downstream lookups
// (equals r.ID under both builds).
func upsertOne(t *testing.T, st *Store, attrs, tags string, scanID string) string {
	t.Helper()
	ensureTestScan(t, st, scanID)
	r := &Resource{
		Provider:       "aws",
		AccountID:      "acct",
		Type:           "aws:ec2:instance",
		NativeID:       "i-vers-1",
		AttributesJSON: attrs,
		TagsJSON:       sp(tags),
		DiscoveredBy:   scanID,
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	return r.ID
}

func TestVersioning_FirstDiscovery_SingleRootRow(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{"env":"prod"}`, testScanID)

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("want 1 row in chain, got %d", len(versions))
	}
	v := versions[0]
	if v.RootID != rootID {
		t.Errorf("RootID: got %q want %q", v.RootID, rootID)
	}
	if v.PreviousVersionID != nil {
		t.Errorf("PreviousVersionID: want nil, got %v", *v.PreviousVersionID)
	}
	if v.SupersededBy != nil {
		t.Errorf("SupersededBy: want nil (current row), got %v", *v.SupersededBy)
	}
	if v.VerifiedAt == nil || *v.VerifiedAt == "" {
		t.Error("VerifiedAt should be set on first discovery")
	}
}

func TestVersioning_UnchangedRescan_VerifyOnly(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{"env":"prod"}`, "scan-A")

	// Re-upsert with identical attributes + tags but a different scan id.
	ensureTestScan(t, st, "scan-B")
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-vers-1",
		AttributesJSON: `{"a":1}`,
		TagsJSON:       sp(`{"env":"prod"}`),
		DiscoveredBy:   "scan-B",
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("unchanged rescan must NOT split: got %d rows", len(versions))
	}
	v := versions[0]
	if v.VerifiedBy == nil || *v.VerifiedBy != "scan-B" {
		t.Errorf("VerifiedBy should advance to scan-B, got %v", v.VerifiedBy)
	}
	if v.DiscoveredBy != "scan-A" {
		t.Errorf("DiscoveredBy should remain scan-A, got %q", v.DiscoveredBy)
	}
}

func TestVersioning_ChangedAttributes_VersionSplit(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{"env":"prod"}`, "scan-A")

	ensureTestScan(t, st, "scan-B")
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-vers-1",
		AttributesJSON: `{"a":2}`,
		TagsJSON:       sp(`{"env":"prod"}`),
		DiscoveredBy:   "scan-B",
	}); err != nil {
		t.Fatalf("split upsert: %v", err)
	}

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("attribute change must split: got %d rows", len(versions))
	}
	old, cur := versions[0], versions[1]
	if old.AttributesJSON != `{"a":1}` {
		t.Errorf("old row attributes frozen: got %q", old.AttributesJSON)
	}
	if cur.AttributesJSON != `{"a":2}` {
		t.Errorf("current row attributes: got %q", cur.AttributesJSON)
	}
	if old.SupersededBy == nil || *old.SupersededBy != cur.VersionRowID {
		t.Errorf("old.SupersededBy must point at current row, got %v", old.SupersededBy)
	}
	if cur.PreviousVersionID == nil || *cur.PreviousVersionID != old.VersionRowID {
		t.Errorf("current.PreviousVersionID must point at old row, got %v", cur.PreviousVersionID)
	}
	if cur.RootID != old.RootID {
		t.Errorf("RootID must be shared, got old=%q new=%q", old.RootID, cur.RootID)
	}
	if cur.DiscoveredAt != old.DiscoveredAt {
		t.Errorf("DiscoveredAt must inherit from root, got old=%q new=%q",
			old.DiscoveredAt, cur.DiscoveredAt)
	}
}

func TestVersioning_ChangedTags_VersionSplit(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{"env":"prod"}`, "scan-A")

	ensureTestScan(t, st, "scan-B")
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-vers-1",
		AttributesJSON: `{"a":1}`,
		TagsJSON:       sp(`{"env":"staging"}`),
		DiscoveredBy:   "scan-B",
	}); err != nil {
		t.Fatalf("tag-change upsert: %v", err)
	}

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("tag change must split: got %d rows", len(versions))
	}
}

// TestVersioning_ListReturnsCurrentRowOnly guards the partial-unique
// invariant: after a version split, ListResources returns one row per
// natural key.
func TestVersioning_ListReturnsCurrentRowOnly(t *testing.T) {
	st := openTestStore(t)
	_ = upsertOne(t, st, `{"a":1}`, `{}`, "scan-A")
	_ = upsertOne(t, st, `{"a":2}`, `{}`, "scan-B")
	_ = upsertOne(t, st, `{"a":3}`, `{}`, "scan-C")

	results, err := st.ListResources(ResourceFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 current row, got %d", len(results))
	}
	if results[0].AttributesJSON != `{"a":3}` {
		t.Errorf("want latest attributes, got %q", results[0].AttributesJSON)
	}
}
