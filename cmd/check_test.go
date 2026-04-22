package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/rules"
	"codeberg.org/icearp/disco/internal/store"
)

// seedCheckDB overlays seedTestDB with an unencrypted EBS volume so the
// aws-ebs-unencrypted builtin fires.
func seedCheckDB(t *testing.T) *store.Store {
	t.Helper()
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	vol := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:volume",
		NativeID: "vol-bad", AttributesJSON: `{"Encrypted": false}`, DiscoveredBy: scanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{vol}); err != nil {
		t.Fatalf("upsert vol: %v", err)
	}
	return st
}

// resetCheckFlags puts the package-level flag vars back to defaults between
// tests — cobra leaks state otherwise.
func resetCheckFlags() {
	checkRulePaths = nil
	checkIncBuiltins = true
	checkSeverity = ""
	checkRuleIDs = nil
	checkOutputFmt = "table"
	checkExitNonZero = false
}

// TestCheckCmd_JSON runs builtins against a seeded DB and asserts the JSON
// output decodes to at least the expected EBS finding.
func TestCheckCmd_JSON(t *testing.T) {
	seedCheckDB(t)
	resetCheckFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--rule", "aws-ebs-unencrypted", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	var fs []rules.Finding
	if jerr := json.Unmarshal([]byte(out), &fs); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if len(fs) != 1 || fs[0].RuleID != "aws-ebs-unencrypted" {
		t.Errorf("findings: %+v", fs)
	}
}

// TestCheckCmd_ExitNonZero asserts --exit-nonzero returns a non-nil error
// (which cobra surfaces as exit status 1) when findings exist.
func TestCheckCmd_ExitNonZero(t *testing.T) {
	seedCheckDB(t)
	resetCheckFlags()

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--rule", "aws-ebs-unencrypted", "--exit-nonzero", "-o", "json"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "finding") {
		t.Errorf("want findings error, got %v", err)
	}
}

// TestCheckCmd_UnknownFormat verifies the unknown --output format error path.
func TestCheckCmd_UnknownFormat(t *testing.T) {
	seedTestDB(t)
	resetCheckFlags()

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "-o", "xml"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "unknown --output format") {
		t.Errorf("want format error, got %v", err)
	}
}
