//go:build paid

package store

import (
	"path/filepath"
	"testing"
)

// TestDiffScans_AddedAndStale verifies the two main diff buckets:
// resources first seen in the newer scan (added) and resources last
// verified by the older scan (stale).
func TestDiffScans_AddedAndStale(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Two scan IDs with valid scan rows so FK constraints are satisfied.
	scanA, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan A: %v", err)
	}
	scanB, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan B: %v", err)
	}

	// Resource 1: discovered in A, refreshed by B → neither added nor stale.
	r1 := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-common",
		AttributesJSON: "{}", DiscoveredBy: scanA,
	}
	if _, err := st.UpsertResource(r1); err != nil {
		t.Fatalf("upsert r1 via A: %v", err)
	}
	// Re-upsert via scan B to update verified_by to B.
	r1.DiscoveredBy = scanB
	if _, err := st.UpsertResource(r1); err != nil {
		t.Fatalf("re-upsert r1 via B: %v", err)
	}

	// Resource 2: discovered only in A (not re-verified by B) → stale.
	r2 := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-gone",
		AttributesJSON: "{}", DiscoveredBy: scanA,
	}
	if _, err := st.UpsertResource(r2); err != nil {
		t.Fatalf("upsert r2 via A: %v", err)
	}

	// Resource 3: first seen in B → added.
	r3 := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-new",
		AttributesJSON: "{}", DiscoveredBy: scanB,
	}
	if _, err := st.UpsertResource(r3); err != nil {
		t.Fatalf("upsert r3 via B: %v", err)
	}

	d, err := st.DiffScans(scanA, scanB)
	if err != nil {
		t.Fatalf("DiffScans: %v", err)
	}

	if len(d.Added) != 1 || d.Added[0].NativeID != "i-new" {
		t.Errorf("Added: want [i-new], got %v", nativeIDs(d.Added))
	}
	if len(d.Stale) != 1 || d.Stale[0].NativeID != "i-gone" {
		t.Errorf("Stale: want [i-gone], got %v", nativeIDs(d.Stale))
	}
}

// TestDiffScans_Errors verifies the two input-validation failure modes.
func TestDiffScans_Errors(t *testing.T) {
	st := openTestStore(t)

	if _, err := st.DiffScans(testScanID, testScanID); err == nil {
		t.Error("DiffScans with identical IDs should fail")
	}
	if _, err := st.DiffScans(testScanID, "nonexistent"); err == nil {
		t.Error("DiffScans with nonexistent to-scan should fail")
	}
}

func nativeIDs(rs []Resource) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.NativeID
	}
	return out
}
