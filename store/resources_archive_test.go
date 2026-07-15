package store

import "testing"

// currentVersion returns the current row of a chain (SupersededBy IS NULL).
func currentVersion(t *testing.T, st *Store, rootID string) ResourceVersion {
	t.Helper()
	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	for _, v := range versions {
		if v.SupersededBy == nil {
			return v
		}
	}
	t.Fatalf("no current row in chain %s (%d versions)", rootID, len(versions))
	return ResourceVersion{}
}

// TestArchiveResource_TombstonesCurrentRow: archiving appends a tombstone that
// becomes the current row (deleted_at set), supersedes the prior row, and
// preserves the pre-archive row in the chain.
func TestArchiveResource_TombstonesCurrentRow(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{"env":"prod"}`, "scan-A")

	ok, err := st.ArchiveResource(rootID, "user-42")
	if err != nil {
		t.Fatalf("ArchiveResource: %v", err)
	}
	if !ok {
		t.Fatal("ArchiveResource: want archived=true")
	}

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("archive must append a tombstone version: got %d rows", len(versions))
	}
	old, cur := versions[0], versions[1]
	if old.SupersededBy == nil || *old.SupersededBy != cur.VersionRowID {
		t.Errorf("old row must be superseded by the tombstone, got %v", old.SupersededBy)
	}
	if old.DeletedAt != nil {
		t.Errorf("pre-archive row must stay live, got deleted_at=%v", *old.DeletedAt)
	}
	if cur.SupersededBy != nil {
		t.Errorf("tombstone must be the current row, got superseded_by=%v", *cur.SupersededBy)
	}
	if cur.DeletedAt == nil || *cur.DeletedAt == "" {
		t.Error("tombstone must carry deleted_at")
	}
	if cur.DeletedBy == nil || *cur.DeletedBy != "user-42" {
		t.Errorf("tombstone deleted_by: got %v want user-42", cur.DeletedBy)
	}
	// Payload + first-seen provenance carry forward from the chain.
	if cur.AttributesJSON != `{"a":1}` {
		t.Errorf("tombstone attributes must copy the prior row, got %q", cur.AttributesJSON)
	}
	if cur.DiscoveredBy != "scan-A" {
		t.Errorf("tombstone discovered_by must inherit the root, got %q", cur.DiscoveredBy)
	}
	if cur.VerifiedBy == nil || *cur.VerifiedBy != "scan-A" {
		t.Errorf("tombstone verified_by must carry the last real sighting, got %v", cur.VerifiedBy)
	}
}

// TestArchiveResource_AlreadyArchived_NoOp: a second archive is idempotent —
// it returns false and appends nothing.
func TestArchiveResource_AlreadyArchived_NoOp(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{}`, "scan-A")

	if ok, err := st.ArchiveResource(rootID, "user-1"); err != nil || !ok {
		t.Fatalf("first archive: ok=%v err=%v", ok, err)
	}
	ok, err := st.ArchiveResource(rootID, "user-2")
	if err != nil {
		t.Fatalf("second archive: %v", err)
	}
	if ok {
		t.Error("second archive must be a no-op (already archived)")
	}
	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("double archive must not append a second tombstone: got %d rows", len(versions))
	}
}

// TestArchiveResource_UnknownRoot_False: archiving an unknown root is a no-op.
func TestArchiveResource_UnknownRoot_False(t *testing.T) {
	st := openTestStore(t)
	ok, err := st.ArchiveResource("does-not-exist", "user-1")
	if err != nil {
		t.Fatalf("ArchiveResource: %v", err)
	}
	if ok {
		t.Error("archiving an unknown root must return false")
	}
}

// TestRestoreResource_LiftsTombstone: restore clears the tombstone in place and
// is idempotent.
func TestRestoreResource_LiftsTombstone(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{}`, "scan-A")
	if ok, err := st.ArchiveResource(rootID, "user-1"); err != nil || !ok {
		t.Fatalf("archive: ok=%v err=%v", ok, err)
	}

	ok, err := st.RestoreResource(rootID)
	if err != nil {
		t.Fatalf("RestoreResource: %v", err)
	}
	if !ok {
		t.Fatal("restore must lift the tombstone")
	}
	cur := currentVersion(t, st, rootID)
	if cur.DeletedAt != nil {
		t.Errorf("restored current row must be live, got deleted_at=%v", *cur.DeletedAt)
	}
	// Restore is in place — no extra chain entry.
	versions, _ := st.GetResourceVersions(rootID)
	if len(versions) != 2 {
		t.Errorf("restore must not append a version: got %d rows", len(versions))
	}

	if ok, err := st.RestoreResource(rootID); err != nil {
		t.Fatalf("second restore: %v", err)
	} else if ok {
		t.Error("second restore must be a no-op (already live)")
	}
}

// TestArchiveThenUnchangedRescan_Resurrects: a scan re-seeing an archived
// resource with unchanged attributes lifts the tombstone via the verify-only
// path — the resource still exists, so archival self-corrects.
func TestArchiveThenUnchangedRescan_Resurrects(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{"env":"prod"}`, "scan-A")
	if ok, err := st.ArchiveResource(rootID, "user-1"); err != nil || !ok {
		t.Fatalf("archive: ok=%v err=%v", ok, err)
	}

	// Re-scan sees the same resource unchanged.
	ensureTestScan(t, st, "scan-B")
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-vers-1",
		AttributesJSON: `{"a":1}`,
		TagsJSON:       sp(`{"env":"prod"}`),
		DiscoveredBy:   "scan-B",
	}); err != nil {
		t.Fatalf("rescan upsert: %v", err)
	}

	cur := currentVersion(t, st, rootID)
	if cur.DeletedAt != nil {
		t.Errorf("re-seen resource must resurrect, got deleted_at=%v", *cur.DeletedAt)
	}
	if cur.DeletedBy != nil {
		t.Errorf("resurrection must clear deleted_by, got %v", *cur.DeletedBy)
	}
	if cur.VerifiedBy == nil || *cur.VerifiedBy != "scan-B" {
		t.Errorf("verified_by must advance to scan-B, got %v", cur.VerifiedBy)
	}
	// Verify-only reuses the tombstone row — no new version.
	versions, _ := st.GetResourceVersions(rootID)
	if len(versions) != 2 {
		t.Errorf("unchanged resurrection must not split: got %d rows", len(versions))
	}
}

// TestArchiveThenChangedRescan_Resurrects: a scan re-seeing an archived
// resource with changed attributes version-splits into a fresh live row.
func TestArchiveThenChangedRescan_Resurrects(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{}`, "scan-A")
	if ok, err := st.ArchiveResource(rootID, "user-1"); err != nil || !ok {
		t.Fatalf("archive: ok=%v err=%v", ok, err)
	}

	ensureTestScan(t, st, "scan-B")
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-vers-1",
		AttributesJSON: `{"a":2}`,
		TagsJSON:       sp(`{}`),
		DiscoveredBy:   "scan-B",
	}); err != nil {
		t.Fatalf("changed rescan upsert: %v", err)
	}

	cur := currentVersion(t, st, rootID)
	if cur.DeletedAt != nil {
		t.Errorf("split resurrection must be live, got deleted_at=%v", *cur.DeletedAt)
	}
	if cur.AttributesJSON != `{"a":2}` {
		t.Errorf("current row attributes: got %q want {\"a\":2}", cur.AttributesJSON)
	}
	versions, _ := st.GetResourceVersions(rootID)
	if len(versions) != 3 {
		t.Errorf("archive + changed rescan must yield a 3-row chain: got %d", len(versions))
	}
}
