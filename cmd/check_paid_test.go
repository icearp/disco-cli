//go:build paid

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/policy"
	"codeberg.org/icearp/disco/internal/store"
)

// seedCheckDB overlays seedTestDB with an unencrypted EBS volume so the
// inline test policy fires.
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

// resetCheckFlags returns package-level flag vars to defaults — cobra leaks
// them across tests because rootCmd is shared.
func resetCheckFlags() {
	checkRulePaths = nil
	checkSeverity = ""
	checkOutputFmt = "table"
	checkExitNonZero = false
	checkTagFilters = nil
}

// writePolicy drops a v1 Rego module that flags unencrypted EBS volumes
// into a temp dir and returns the directory path for `--rules`.
func writePolicy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `package disco
import rego.v1

deny contains f if {
	input.type == "aws:ec2:volume"
	input.attributes.Encrypted == false
	f := {
		"id": "ebs-unencrypted",
		"severity": "high",
		"message": "EBS volume is unencrypted",
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "ebs.rego"), []byte(src), 0o600); err != nil {
		t.Fatalf("write rego: %v", err)
	}
	return dir
}

// TestCheckCmd_JSON runs the inline policy against a seeded DB and asserts
// the JSON output decodes to the expected EBS finding.
func TestCheckCmd_JSON(t *testing.T) {
	seedCheckDB(t)
	resetCheckFlags()
	dir := writePolicy(t)

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--rules", dir, "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	var fs []policy.Finding
	if jerr := json.Unmarshal([]byte(out), &fs); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if len(fs) != 1 || fs[0].ID != "ebs-unencrypted" {
		t.Errorf("findings: %+v", fs)
	}
}

// TestCheckCmd_ExitNonZero asserts --exit-nonzero surfaces a non-nil error
// when findings exist (cobra renders that as exit status 1).
func TestCheckCmd_ExitNonZero(t *testing.T) {
	seedCheckDB(t)
	resetCheckFlags()
	dir := writePolicy(t)

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--rules", dir, "--exit-nonzero", "-o", "json"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "finding") {
		t.Errorf("want findings error, got %v", err)
	}
}

// TestCheckCmd_RulesRequired confirms the command rejects an invocation
// without --rules — there are no embedded policies anymore.
func TestCheckCmd_RulesRequired(t *testing.T) {
	seedTestDB(t)
	resetCheckFlags()

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "--rules is required") {
		t.Errorf("want --rules required error, got %v", err)
	}
}
