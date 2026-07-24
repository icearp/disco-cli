package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/policy"
	"github.com/icearp/disco-cli/store"
)

func resetFindingsFlags() {
	findingsOutputFmt = ""
	findingsCheckRunID = ""
	findingsRunSince.reset()
	findingsSeverity = ""
	findingsCategory = ""
	findingsType = ""
	findingsProviders = nil
	findingsFindingID = ""
}

// TestRenderCheckRuns_EmptyJSON pins the wire contract: a zero-row check-run
// list renders as `[]`, not `null` (F6 parity with list/scans).
func TestRenderCheckRuns_EmptyJSON(t *testing.T) {
	out, err := captureStdout(t, func() error { return renderCheckRuns(nil, "json") })
	if err != nil {
		t.Fatalf("renderCheckRuns: %v", err)
	}
	if got := strings.TrimSpace(out); got != "[]" {
		t.Errorf("empty findings runs -o json: want [], got %q", got)
	}
}

// seedFindingRun upserts one check_run carrying a single high-severity
// EBS finding. Returns the run id for assertions.
func seedFindingRun(t *testing.T, st *store.Store) string {
	t.Helper()
	rows := []store.StoredFinding{findingToStored(policy.Finding{
		ID:         "waf-sec-ebs-encryption-at-rest",
		Severity:   "high",
		Message:    "EBS volume \"\" (vol-bad) is not encrypted at rest",
		ResourceID: "res-x",
		Category:   "aws-waf",
		Type:       "aws:ec2:volume",
	})}
	id, err := st.PersistCheckRun(nil, []string{"aws-waf"}, "", 1, rows)
	if err != nil {
		t.Fatalf("PersistCheckRun: %v", err)
	}
	return id
}

func TestFindings_List(t *testing.T) {
	st := seedTestDB(t)
	seedFindingRun(t, st)
	resetFindingsFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"findings", "list", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("findings list: %v", err)
	}
	var fs []policy.Finding
	if jerr := json.Unmarshal([]byte(out), &fs); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if len(fs) != 1 || fs[0].ID != "waf-sec-ebs-encryption-at-rest" {
		t.Errorf("findings: %+v", fs)
	}
	if fs[0].Category != "aws-waf" {
		t.Errorf("category: %q", fs[0].Category)
	}
}

func TestFindings_LatestShorthand(t *testing.T) {
	st := seedTestDB(t)
	id := seedFindingRun(t, st)
	resetFindingsFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"findings", "list", "--check-run-id", "latest", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("findings latest: %v", err)
	}
	var fs []policy.Finding
	if jerr := json.Unmarshal([]byte(out), &fs); jerr != nil {
		t.Fatalf("not JSON: %v", jerr)
	}
	if len(fs) != 1 {
		t.Errorf("latest: got %d, want 1", len(fs))
	}
	_ = id
}

func TestFindings_UnknownCheckRun(t *testing.T) {
	seedTestDB(t)
	resetFindingsFlags()

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"findings", "list", "--check-run-id", "deadbeef"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("want 'not found' error, got %v", err)
	}
}

func TestFindings_Runs(t *testing.T) {
	st := seedTestDB(t)
	seedFindingRun(t, st)
	seedFindingRun(t, st)
	resetFindingsFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"findings", "runs"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("findings runs: %v", err)
	}
	if !strings.Contains(out, "STARTED") || !strings.Contains(out, "PACKS") {
		t.Errorf("missing header: %s", out)
	}
	if !strings.Contains(out, "aws-waf") {
		t.Errorf("packs missing: %s", out)
	}
}

func TestFindings_RunsJSON(t *testing.T) {
	st := seedTestDB(t)
	seedFindingRun(t, st)
	resetFindingsFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"findings", "runs", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("findings runs json: %v", err)
	}
	var runs []store.CheckRun
	if jerr := json.Unmarshal([]byte(out), &runs); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if len(runs) != 1 {
		t.Errorf("runs: got %d, want 1", len(runs))
	}
}

// TestFindings_BySeverity pins the cutoff (minimum) semantics of
// `findings list --severity`, matching `disco check --severity`: the seed
// finding is "high", so a "medium" cutoff includes it and a "critical" cutoff
// excludes it.
func TestFindings_BySeverity(t *testing.T) {
	st := seedTestDB(t)
	seedFindingRun(t, st)

	listSeverity := func(t *testing.T, sev string) []policy.Finding {
		t.Helper()
		resetFindingsFlags()
		out, err := captureStdout(t, func() error {
			cmd := rootCmd
			cmd.SetArgs([]string{"findings", "list", "--severity", sev, "-o", "json"})
			return cmd.Execute()
		})
		if err != nil {
			t.Fatalf("findings list --severity %s: %v", sev, err)
		}
		var fs []policy.Finding
		if jerr := json.Unmarshal([]byte(out), &fs); jerr != nil {
			t.Fatalf("not JSON: %v\n%s", jerr, out)
		}
		return fs
	}

	if got := len(listSeverity(t, "medium")); got != 1 {
		t.Errorf("severity>=medium: got %d, want 1 (cutoff includes the high seed)", got)
	}
	if got := len(listSeverity(t, "critical")); got != 0 {
		t.Errorf("severity>=critical: got %d, want 0 (cutoff excludes the high seed)", got)
	}
}
