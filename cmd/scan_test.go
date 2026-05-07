package cmd

import (
	"testing"
	"time"
)

// TestEvaluateIfOlderThan_SkipsOnRecentCompleteScan pins the regression where
// the LatestCompleteScan query matched 'complete' but CompleteScan writes
// 'completed' — gate never fired and every cron tick re-ran a live scan.
func TestEvaluateIfOlderThan_SkipsOnRecentCompleteScan(t *testing.T) {
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	if err := st.CompleteScan(scanID, 1); err != nil {
		t.Fatalf("CompleteScan: %v", err)
	}

	skip, msg, err := evaluateIfOlderThan(st, []string{"aws"}, 6*time.Hour)
	if err != nil {
		t.Fatalf("evaluateIfOlderThan: %v", err)
	}
	if !skip {
		t.Fatalf("expected skip=true for a fresh completed scan, got skip=false")
	}
	if msg == "" {
		t.Fatalf("expected non-empty skip message")
	}
}

// TestEvaluateIfOlderThan_RunsWhenNoCompleteScan asserts the negative path:
// no qualifying scan → skip=false, scan must run.
func TestEvaluateIfOlderThan_RunsWhenNoCompleteScan(t *testing.T) {
	st := seedTestDB(t)
	// seedTestDB creates a scan but never marks it completed; status stays "running".
	skip, _, err := evaluateIfOlderThan(st, []string{"aws"}, 6*time.Hour)
	if err != nil {
		t.Fatalf("evaluateIfOlderThan: %v", err)
	}
	if skip {
		t.Fatalf("expected skip=false when no completed scan exists, got skip=true")
	}
}
