package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/icearp/disco-cli/internal/providers"
	"github.com/icearp/disco-cli/store"
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

// quotaStubScanner reports one quota service that wrote quota limits and one
// resource service that wrote none, in the order a dispatcher reports them.
type quotaStubScanner struct{}

func (quotaStubScanner) Name() string { return "aws" }
func (quotaStubScanner) Scan(_ context.Context, st *store.Store, _ string) error {
	st.ReportQuotas("aws:servicequotas", "us-east-1", 2, 3)
	st.ReportService("aws:servicequotas", "us-east-1", 5, 0, 0, 0, store.ServiceOK)
	st.ReportQuotas("aws:ec2", "us-east-1", 0, 0)
	st.ReportService("aws:ec2", "us-east-1", 1, 1, 0, 0, store.ServiceOK)
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

// Quota limits count on their own service's line, as its total does, but the
// summary totals them apart from resources, whose count covers only the
// resources table. A scan that wrote no quota limits prints no quota clause.
func TestRunScan_CountsQuotaLimitsApart(t *testing.T) {
	seedTestDB(t)
	var out, errOut bytes.Buffer
	if err := runScan(newScanTestCmd(&out, &errOut), []providers.Scanner{quotaStubScanner{}}); err != nil {
		t.Fatalf("runScan: %v", err)
	}
	lines := map[string]string{}
	for line := range strings.Lines(errOut.String()) {
		if f := strings.Fields(line); len(f) > 2 {
			lines[f[1]] = line
		}
	}
	for service, want := range map[string]string{"aws:servicequotas": "(5 total, 2 new, 3 changed)", "aws:ec2": "(1 total, 1 new, 0 changed)"} {
		if !strings.Contains(lines[service], want) {
			t.Errorf("%s progress line = %q, want it to contain %q", service, lines[service], want)
		}
	}
	if want := "Scan complete: 0 resources (1 new, 0 changed), quota limits (2 new, 3 changed) in "; !strings.Contains(out.String(), want) {
		t.Errorf("summary lacks %q:\n%s", want, out.String())
	}

	out.Reset()
	if err := runScan(newScanTestCmd(&out, &errOut), []providers.Scanner{stubScanner{name: "aws"}}); err != nil {
		t.Fatalf("runScan without quotas: %v", err)
	}
	if want := "Scan complete: 0 resources (1 new, 0 changed) in "; !strings.Contains(out.String(), want) {
		t.Errorf("summary without quotas lacks %q:\n%s", want, out.String())
	}
}

// Two scopes sharing a label share one pending entry: a second report before
// the first line adds to it, so no scope's quota counts drop off the lines.
func TestQuotaTally_SharedLabelKeepsCounts(t *testing.T) {
	t.Parallel()
	q := newQuotaTally()
	q.report("azure:quotas", "Pay-As-You-Go", 2, 1)
	q.report("azure:quotas", "Pay-As-You-Go", 3, 0)
	if n, c := q.take("azure:quotas", "Pay-As-You-Go"); n != 5 || c != 1 {
		t.Errorf("first line take = %d, %d; want 5, 1", n, c)
	}
	if n, c := q.take("azure:quotas", "Pay-As-You-Go"); n != 0 || c != 0 {
		t.Errorf("second line take = %d, %d; want 0, 0", n, c)
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
	if err := st.CompleteScan(scanID); err != nil {
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
