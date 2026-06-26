package cmd

import (
	"testing"
	"time"

	"codeberg.org/icearp/disco/store"
)

func TestServiceStatusSuffix(t *testing.T) {
	cases := []struct {
		name     string
		status   store.ServiceStatus
		errCount int
		want     string
	}{
		{"ok", store.ServiceOK, 0, ""},
		{"unavailable", store.ServiceUnavailable, 0, "  (service unavailable)"},
		{"disabled", store.ServiceDisabled, 0, "  (service disabled)"},
		{"errors", store.ServiceOK, 3, "  (with errors)"},
		{"unavailable beats errors", store.ServiceUnavailable, 3, "  (service unavailable)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := serviceStatusSuffix(c.status, c.errCount); got != c.want {
				t.Errorf("serviceStatusSuffix(%v, %d) = %q; want %q", c.status, c.errCount, got, c.want)
			}
		})
	}
}

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
