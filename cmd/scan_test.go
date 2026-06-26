package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/store"
	"github.com/spf13/cobra"
)

// stubScanner reports exactly one completed (service, scope) unit so runScan's
// progress closure fires, then returns cleanly.
type stubScanner struct{ name string }

func (s stubScanner) Name() string { return s.name }
func (s stubScanner) Scan(_ context.Context, st *store.Store, _ string) error {
	st.ReportService("ec2", "global", 1, 1, 0, 0, store.ServiceOK)
	return nil
}

// newScanTestCmd builds a cobra.Command carrying just the flags runScan reads,
// with stdout/stderr captured into the provided buffers.
func newScanTestCmd(out, errOut *bytes.Buffer) *cobra.Command {
	c := &cobra.Command{}
	c.Flags().Bool("dry-run", false, "")
	c.Flags().Duration("if-older-than", 0, "")
	c.Flags().String("resume", "", "")
	c.Flags().Bool("quiet", false, "")
	c.Flags().Bool("fail-on-error", false, "")
	c.SetOut(out)
	c.SetErr(errOut)
	c.SetContext(context.Background())
	return c
}

// TestRunScan_QuietGatesBanners pins the --quiet contract and the #N progress
// counter: without --quiet the start banner + "#1" momentum marker hit stderr
// and the summary hits stdout; with --quiet stderr carries neither banner nor
// progress line, but the final summary still lands on stdout.
func TestRunScan_QuietGatesBanners(t *testing.T) {
	seedTestDB(t) // sets viper "db" so openWriteDB targets the temp store
	fake := stubScanner{name: "aws"}

	var out, errOut bytes.Buffer
	c := newScanTestCmd(&out, &errOut)
	if err := runScan(c, []providers.Scanner{fake}); err != nil {
		t.Fatalf("runScan: %v", err)
	}
	if !strings.Contains(errOut.String(), "started") {
		t.Errorf("non-quiet: want start banner on stderr, got:\n%s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "#1") {
		t.Errorf("non-quiet: want '#1' progress counter on stderr, got:\n%s", errOut.String())
	}
	if !strings.Contains(out.String(), "Scan complete") {
		t.Errorf("non-quiet: want summary on stdout, got:\n%s", out.String())
	}

	out.Reset()
	errOut.Reset()
	c = newScanTestCmd(&out, &errOut)
	_ = c.Flags().Set("quiet", "true")
	if err := runScan(c, []providers.Scanner{fake}); err != nil {
		t.Fatalf("runScan --quiet: %v", err)
	}
	if strings.Contains(errOut.String(), "started") || strings.Contains(errOut.String(), "#") {
		t.Errorf("--quiet should suppress banner + progress on stderr, got:\n%s", errOut.String())
	}
	if !strings.Contains(out.String(), "Scan complete") {
		t.Errorf("--quiet must still print the final summary on stdout, got:\n%s", out.String())
	}
}

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
