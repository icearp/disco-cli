package store

import (
	"strings"
	"testing"
)

// Two rows started in the same second tie on started_at (1s resolution on
// both dialects). newestFirst breaks the tie on rowid (insertion order) on
// SQLite and on id on Postgres, which has no rowid — these readers used to
// fail there outright. The high id is inserted first, so insertion order and
// id order disagree and each dialect's expectation pins its own tiebreak.
const (
	tieStartedAt = "2099-01-01T00:00:00Z"
	tieHighID    = "ffffffffffffffffffffffffffffffff"
	tieLowID     = "00000000000000000000000000000001"
)

// tieWant returns the expected newest-first order of the two tied rows.
func tieWant(st *Store) (first, second string) {
	if st.driver == driverPostgres {
		return tieHighID, tieLowID
	}
	return tieLowID, tieHighID // inserted last, so newest by rowid
}

func TestScanOrdering_TiebreakBothDialects(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		for _, id := range []string{tieHighID, tieLowID} {
			if _, err := st.CreateScanWithID(id, []string{"aws"}, map[string]any{}); err != nil {
				t.Fatalf("CreateScanWithID(%s): %v", id, err)
			}
			if _, err := st.exec(`UPDATE scans SET started_at = ?, status = 'partial' WHERE id = ?`, tieStartedAt, id); err != nil {
				t.Fatalf("pin scan %s: %v", id, err)
			}
		}
		first, second := tieWant(st)

		scans, err := st.ListScans()
		if err != nil {
			t.Fatalf("ListScans: %v", err)
		}
		if len(scans) < 2 || scans[0].ID != first || scans[1].ID != second {
			t.Errorf("ListScans order = %v, want %s then %s", scanIDs(scans), first, second)
		}

		latest, err := st.LatestCompleteScan("aws")
		if err != nil {
			t.Fatalf("LatestCompleteScan: %v", err)
		}
		if latest.ID != first {
			t.Errorf("LatestCompleteScan = %s, want %s", latest.ID, first)
		}

		incomplete, err := st.LatestIncompleteScan()
		if err != nil {
			t.Fatalf("LatestIncompleteScan: %v", err)
		}
		if incomplete == nil || incomplete.ID != first {
			t.Errorf("LatestIncompleteScan = %v, want %s", incomplete, first)
		}
	})
}

func TestListCheckRuns_TiebreakBothDialects(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		for _, id := range []string{tieHighID, tieLowID} {
			runID := seedRun(t, st, "high", 0) // no findings: an FK would pin the run id
			if _, err := st.exec(`UPDATE check_runs SET id = ?, started_at = ? WHERE id = ?`, id, tieStartedAt, runID); err != nil {
				t.Fatalf("pin run %s: %v", id, err)
			}
		}
		first, second := tieWant(st)

		runs, err := st.ListCheckRuns()
		if err != nil {
			t.Fatalf("ListCheckRuns: %v", err)
		}
		var got []string
		for _, r := range runs {
			got = append(got, r.ID)
		}
		if len(got) < 2 || got[0] != first || got[1] != second {
			t.Errorf("ListCheckRuns order = %s, want %s then %s", strings.Join(got, ","), first, second)
		}
	})
}

func scanIDs(scans []Scan) []string {
	out := make([]string, 0, len(scans))
	for _, s := range scans {
		out = append(out, s.ID)
	}
	return out
}
