package store

import "testing"

// seedVerifiedResource upserts one resource attributed to scanID. Passing a
// changed attribute payload for an already-seen natural key produces a version
// split, which is what a scan emitting the same identity twice does.
func seedVerifiedResource(t *testing.T, st *Store, scanID, nativeID, attrs string) {
	t.Helper()
	if _, err := st.UpsertResources([]*Resource{{
		Provider: "aws", AccountID: "123456789012", Type: "aws:ec2:instance",
		NativeID: nativeID, AttributesJSON: attrs, DiscoveredBy: scanID,
	}}); err != nil {
		t.Fatalf("upsert %s: %v", nativeID, err)
	}
}

// TestTerminalScanCountsCurrentResources pins that a finished scan reports the
// resources it saw, counted from the rows themselves.
//
// The count used to be supplied by the caller as the sum of each service's
// self-reported total. Scanners run concurrently over independent scopes and
// nothing dedupes across them, so an identity emitted by more than one scope
// was counted once per emission — measured at 558 duplicate visits in a single
// real scan. Counting rows makes the number self-correcting: a duplicate
// emitter writes the same row twice and inflates nothing.
//
// The superseded row is the negative space. An intra-scan version split leaves
// history behind, and counting every row the scan stamped would report more
// resources than exist.
func TestTerminalScanCountsCurrentResources(t *testing.T) {
	for _, tc := range []struct {
		name     string
		finalize func(st *Store, scanID string) error
		want     string
	}{
		{"complete", func(st *Store, id string) error { return st.CompleteScan(id) }, "completed"},
		{"partial", func(st *Store, id string) error { return st.PartialScan(id, "svc failed") }, "partial"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t)
			scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
			if err != nil {
				t.Fatalf("CreateScan: %v", err)
			}

			seedVerifiedResource(t, st, scanID, "arn:one", `{"a":1}`)
			seedVerifiedResource(t, st, scanID, "arn:two", `{"a":1}`)
			// Same identity again with different attributes: the first row is
			// superseded, so the scan stamped 3 rows but saw 2 resources.
			seedVerifiedResource(t, st, scanID, "arn:two", `{"a":2}`)

			if err := tc.finalize(st, scanID); err != nil {
				t.Fatalf("finalize: %v", err)
			}

			var status string
			var count *int
			if err := st.DB().QueryRow(
				`SELECT status, resource_count FROM scans WHERE id = ?`, scanID,
			).Scan(&status, &count); err != nil {
				t.Fatalf("read scan: %v", err)
			}
			if status != tc.want {
				t.Errorf("status = %q; want %q", status, tc.want)
			}
			if count == nil {
				t.Fatalf("resource_count is NULL; a terminal scan must report a count")
			}
			if *count != 2 {
				t.Errorf("resource_count = %d; want 2 (3 rows stamped, 1 superseded by an intra-scan split)", *count)
			}
		})
	}
}
