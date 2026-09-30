package store

import (
	"testing"
)

func seedRun(t *testing.T, st *Store, severity string, count int) string {
	t.Helper()
	rows := make([]StoredFinding, 0, count)
	sev := severity
	if sev == "" {
		sev = "high"
	}
	for i := 0; i < count; i++ {
		rows = append(rows, StoredFinding{
			FindingID:  "test-rule",
			ResourceID: "res-x",
			Severity:   sev,
			Message:    "seed",
		})
	}
	id, err := st.PersistCheckRun([]string{"./policies"}, []string{"aws-waf"}, "", 100, rows)
	if err != nil {
		t.Fatalf("PersistCheckRun: %v", err)
	}
	return id
}

func TestPersistCheckRun_RoundTrip(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		runID := seedRun(t, st, "high", 3)

		got, err := st.GetCheckRun(runID)
		if err != nil {
			t.Fatalf("GetCheckRun: %v", err)
		}
		if got.FindingCount == nil || *got.FindingCount != 3 {
			t.Errorf("finding_count: got %v, want 3", got.FindingCount)
		}
		if len(got.Packs) != 1 || got.Packs[0] != "aws-waf" {
			t.Errorf("packs: got %v, want [aws-waf]", got.Packs)
		}
		if len(got.RulesPaths) != 1 || got.RulesPaths[0] != "./policies" {
			t.Errorf("rules_paths: got %v", got.RulesPaths)
		}

		rows, err := st.ListFindings(FindingFilter{CheckRunID: runID})
		if err != nil {
			t.Fatalf("ListFindings: %v", err)
		}
		if len(rows) != 3 {
			t.Errorf("findings: got %d, want 3", len(rows))
		}
	})
}

func TestListFindings_ByCheckRunID(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		idA := seedRun(t, st, "high", 2)
		_ = seedRun(t, st, "medium", 5)

		rows, err := st.ListFindings(FindingFilter{CheckRunID: idA})
		if err != nil {
			t.Fatalf("ListFindings: %v", err)
		}
		if len(rows) != 2 {
			t.Errorf("isolation: got %d, want 2", len(rows))
		}
		for _, r := range rows {
			if r.CheckRunID != idA {
				t.Errorf("leaked row: %+v", r)
			}
		}
	})
}

func TestPersistCheckRun_FKCascade(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		runID := seedRun(t, st, "high", 4)

		if err := st.DeleteCheckRun(runID); err != nil {
			t.Fatalf("DeleteCheckRun: %v", err)
		}
		rows, err := st.ListFindings(FindingFilter{CheckRunID: runID})
		if err != nil {
			t.Fatalf("ListFindings: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("CASCADE: got %d rows, want 0", len(rows))
		}
	})
}
