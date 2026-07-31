package store

import (
	"regexp"
	"testing"
	"time"
)

// rfc3339Stored is the exact shape nowExpr must write. Anchored and
// second-precision on purpose: RFC3339Nano would also satisfy time.Parse, but
// these columns are compared as TEXT, and two rows agreeing on the instant
// while disagreeing on the digit count do not compare equal.
var rfc3339Stored = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

// TestNowExpr_WritesRFC3339OnBothDialects pins the format of every column
// nowExpr feeds, and it runs under withDialects because that is the only
// construction that can see the defect it guards.
//
// nowExpr is two independently written SQL expressions — a Postgres to_char
// and a SQLite strftime — that must render the same bytes. Nothing in the Go
// type system relates them, so a typo in either one is caught only by
// executing both. SQLite is also the permissive dialect: openTestStore alone
// would happily certify a to_char format string that Postgres rejects
// outright, which is exactly how `managed_by_provider = 1` shipped.
//
// Asserting the shape rather than round-tripping through ParseTimestamp is
// deliberate. ParseTimestamp accepts the legacy zoneless form too, so a
// parse-based assertion would pass against a nowExpr that had never been
// changed at all — it would test the reader and call it a test of the writer.
func TestNowExpr_WritesRFC3339OnBothDialects(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		id, err := st.CreateScan([]string{"aws"}, map[string]any{})
		if err != nil {
			t.Fatalf("create scan: %v", err)
		}
		if err := st.CompleteScan(id); err != nil {
			t.Fatalf("complete scan: %v", err)
		}

		sc, err := st.GetScan(id)
		if err != nil {
			t.Fatalf("get scan: %v", err)
		}
		// GetScan projects through toRFC3339, which would mask a bad stored
		// value by reformatting it on the way out. Read the columns raw.
		var startedAt, finishedAt string
		if err := st.get(&startedAt,
			`SELECT started_at FROM scans WHERE id = ?`, id); err != nil {
			t.Fatalf("read started_at: %v", err)
		}
		if err := st.get(&finishedAt,
			`SELECT finished_at FROM scans WHERE id = ?`, id); err != nil {
			t.Fatalf("read finished_at: %v", err)
		}
		for _, c := range []struct{ name, got string }{
			{"started_at", startedAt},
			{"finished_at", finishedAt},
		} {
			if !rfc3339Stored.MatchString(c.got) {
				t.Errorf("scans.%s = %q, want RFC3339 (%s)", c.name, c.got, rfc3339Stored)
			}
		}
		if sc.ID != id {
			t.Errorf("GetScan returned id %q, want %q", sc.ID, id)
		}

		if err := st.SaveCheckpoint(id, "aws", "ec2", "us-east-1", "tok"); err != nil {
			t.Fatalf("save checkpoint: %v", err)
		}
		var updatedAt string
		if err := st.get(&updatedAt,
			`SELECT updated_at FROM scan_checkpoints WHERE scan_id = ?`, id); err != nil {
			t.Fatalf("read updated_at: %v", err)
		}
		if !rfc3339Stored.MatchString(updatedAt) {
			t.Errorf("scan_checkpoints.updated_at = %q, want RFC3339 (%s)", updatedAt, rfc3339Stored)
		}

		// ListCheckpoints parses that column into a time.Time. A zero value
		// here is the silent-failure mode: the field is populated only when
		// the parse succeeds, so a format the reader cannot read looks like a
		// checkpoint that was never stamped.
		cps, err := st.ListCheckpoints(id)
		if err != nil {
			t.Fatalf("list checkpoints: %v", err)
		}
		if len(cps) != 1 {
			t.Fatalf("ListCheckpoints returned %d rows, want 1", len(cps))
		}
		if cps[0].UpdatedAt.IsZero() {
			t.Errorf("Checkpoint.UpdatedAt is zero — %q did not parse", updatedAt)
		}
	})
}

// TestParseTimestamp_AcceptsBothStoredShapes pins the reader's tolerance as a
// deliberate choice rather than an accident. Nothing rewrites rows written
// before v0.31.0 — migrations/pg/016 is deliberately empty — so any store that
// predates it still holds the legacy shape, and dropping the second layout
// would strand those rows silently, since every caller treats a parse failure
// as "no timestamp" rather than an error.
func TestParseTimestamp_AcceptsBothStoredShapes(t *testing.T) {
	want := time.Date(2026, 7, 28, 20, 47, 8, 0, time.UTC)
	for _, tc := range []struct{ name, in string }{
		{"rfc3339", "2026-07-28T20:47:08Z"},
		{"legacy zoneless", "2026-07-28 20:47:08"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseTimestamp(tc.in)
			if !ok {
				t.Fatalf("ParseTimestamp(%q) reported failure", tc.in)
			}
			if !got.Equal(want) {
				t.Errorf("ParseTimestamp(%q) = %v, want %v", tc.in, got, want)
			}
		})
	}

	// An offset that is not UTC must be normalized, not carried: callers
	// compare these against time.Now().UTC().
	got, ok := ParseTimestamp("2026-07-28T15:47:08-05:00")
	if !ok {
		t.Fatal("ParseTimestamp rejected an offset-bearing RFC3339 value")
	}
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("ParseTimestamp(offset) = %v (%v), want %v (UTC)", got, got.Location(), want)
	}

	if _, ok := ParseTimestamp("garbage"); ok {
		t.Error("ParseTimestamp accepted garbage")
	}
}
