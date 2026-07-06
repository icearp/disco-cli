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
	c.Flags().Bool("no-progress", false, "")
	c.Flags().Bool("fail-on-error", false, "")
	c.SetOut(out)
	c.SetErr(errOut)
	c.SetContext(context.Background())
	return c
}

// TestRunScan_QuietGatesBanners pins the --quiet contract: stderr is a
// *bytes.Buffer (not a TTY) so the spinner is off and the per-service line
// prints plainly. Without --quiet: banner + per-service line hit stderr,
// summary hits stdout. With --quiet: stderr carries neither, but summary
// still lands on stdout.
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
	// The stub reports service "ec2" → a per-service progress line on stderr.
	if !strings.Contains(errOut.String(), "ec2") || !strings.Contains(errOut.String(), "1 total") {
		t.Errorf("non-quiet: want per-service progress line on stderr, got:\n%s", errOut.String())
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
	if strings.Contains(errOut.String(), "started") || strings.Contains(errOut.String(), "1 total") {
		t.Errorf("--quiet should suppress banner + progress on stderr, got:\n%s", errOut.String())
	}
	if !strings.Contains(out.String(), "Scan complete") {
		t.Errorf("--quiet must still print the final summary on stdout, got:\n%s", out.String())
	}
}

func TestServiceStatusSuffix(t *testing.T) {
	cases := []struct {
		name     string
		service  string
		status   store.ServiceStatus
		errCount int
		want     string
	}{
		{"ok", "aws:ec2", store.ServiceOK, 0, ""},
		{"unavailable", "aws:omics", store.ServiceUnavailable, 0, "  (region: unavailable)"},
		// ServiceDisabled is shared across providers; the scope noun tracks the
		// provider prefix (account / project / subscription).
		{"aws disabled", "aws:macie", store.ServiceDisabled, 0, "  (account: disabled)"},
		{"gcp disabled", "gcp:compute", store.ServiceDisabled, 0, "  (project: disabled)"},
		{"azure disabled", "azure:microsoft.compute", store.ServiceDisabled, 0, "  (subscription: disabled)"},
		{"not entitled", "aws:kendra", store.ServiceNotEntitled, 0, "  (account: not entitled)"},
		{"errors", "aws:s3", store.ServiceOK, 3, "  (with errors)"},
		{"unavailable beats errors", "aws:omics", store.ServiceUnavailable, 3, "  (region: unavailable)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := serviceStatusSuffix(c.service, c.status, c.errCount); got != c.want {
				t.Errorf("serviceStatusSuffix(%q, %v, %d) = %q; want %q", c.service, c.status, c.errCount, got, c.want)
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

// TestElapsedField pins the scan progress timer column: the Duration string
// wrapped in brackets that hug the value, right-padded to 8 chars so the #N
// counter and later columns don't shift mid-scan. Padding sits right of "]",
// never inside the brackets.
// TestScopeRegionsEnabled pins the flag precedence: default on, --scope-regions
// wins when set, and --no-scope-regions forces off even over an explicit
// --scope-regions=true.
func TestScopeRegionsEnabled(t *testing.T) {
	newCmd := func() *cobra.Command {
		c := &cobra.Command{Use: "x", Run: func(*cobra.Command, []string) {}}
		c.Flags().Bool("scope-regions", true, "")
		c.Flags().Bool("no-scope-regions", false, "")
		return c
	}

	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"default on", nil, true},
		{"explicit off", []string{"--scope-regions=false"}, false},
		{"no-scope-regions off", []string{"--no-scope-regions"}, false},
		{"no-scope-regions overrides explicit on", []string{"--scope-regions=true", "--no-scope-regions"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := newCmd()
			cmd.SetArgs(c.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got := scopeRegionsEnabled(cmd, "aws"); got != c.want {
				t.Errorf("scopeRegionsEnabled(%v) = %v; want %v", c.args, got, c.want)
			}
		})
	}
}

func TestElapsedField(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "[0s]    "},
		{45 * time.Second, "[45s]   "},
		{83 * time.Second, "[1m23s] "},
		{623 * time.Second, "[10m23s]"},
		{1500 * time.Millisecond, "[2s]    "}, // rounds to the nearest second
	}
	for _, c := range cases {
		got := elapsedField(c.in)
		if got != c.want {
			t.Errorf("elapsedField(%v) = %q; want %q", c.in, got, c.want)
		}
		// Brackets hug the value (no pad before "]"); any spaces are trailing.
		if !strings.HasPrefix(got, "[") || strings.Contains(got, " ]") {
			t.Errorf("elapsedField(%v) = %q; padding must be outside the brackets", c.in, got)
		}
		// Sub-~1h values keep the column a stable 8 chars.
		if c.in < time.Hour && len(got) != 8 {
			t.Errorf("elapsedField(%v) = %q has len %d; want 8", c.in, got, len(got))
		}
	}
}
