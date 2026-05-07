package cmd

import (
	"encoding/json"
	"fmt"
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
	checkPacks = nil
	checkSeverity = ""
	checkOutputFmt = "table"
	checkExitZero = false
	checkTagFilters = nil
	checkIncludeManaged = false
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
		cmd.SetArgs([]string{"check", "--rules", dir, "--exit-zero", "-o", "json"})
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

// TestCheckCmd_DefaultGatesOnFindings asserts the default behaviour:
// any reported finding produces a non-nil error (cobra renders exit 1).
// Pre-flip this required --exit-nonzero; post-flip it is the default.
func TestCheckCmd_DefaultGatesOnFindings(t *testing.T) {
	seedCheckDB(t)
	resetCheckFlags()
	dir := writePolicy(t)

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--rules", dir, "-o", "json"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "finding") {
		t.Errorf("want findings error, got %v", err)
	}
}

// TestCheckCmd_ExitZeroOverride asserts --exit-zero suppresses the gate:
// findings still print to stdout, but the command exits 0.
func TestCheckCmd_ExitZeroOverride(t *testing.T) {
	seedCheckDB(t)
	resetCheckFlags()
	dir := writePolicy(t)

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--rules", dir, "--exit-zero", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("--exit-zero on findings: want nil err, got %v", err)
	}
	if !strings.Contains(out, "ebs-unencrypted") {
		t.Errorf("--exit-zero must still emit findings to stdout; got %q", out)
	}
}

// TestCheckCmd_SARIF asserts -o sarif emits a valid v2.1.0 doc with the
// EBS finding mapped to a SARIF result + driver rule descriptor.
func TestCheckCmd_SARIF(t *testing.T) {
	seedCheckDB(t)
	resetCheckFlags()
	dir := writePolicy(t)

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--rules", dir, "--exit-zero", "-o", "sarif"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	var doc sarifLog
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if doc.Version != "2.1.0" {
		t.Errorf("version: want 2.1.0, got %q", doc.Version)
	}
	if len(doc.Runs) != 1 {
		t.Fatalf("runs: want 1, got %d", len(doc.Runs))
	}
	run := doc.Runs[0]
	if run.Tool.Driver.Name != "disco" {
		t.Errorf("driver.name: %q", run.Tool.Driver.Name)
	}
	if len(run.Tool.Driver.Rules) != 1 || run.Tool.Driver.Rules[0].ID != "ebs-unencrypted" {
		t.Errorf("driver.rules: %+v", run.Tool.Driver.Rules)
	}
	if len(run.Results) != 1 {
		t.Fatalf("results: want 1, got %d", len(run.Results))
	}
	r := run.Results[0]
	if r.RuleID != "ebs-unencrypted" {
		t.Errorf("ruleId: %q", r.RuleID)
	}
	if r.Level != "error" {
		t.Errorf("level: want error (high → error), got %q", r.Level)
	}
	if len(r.Locations) != 1 || len(r.Locations[0].LogicalLocations) != 1 {
		t.Fatalf("locations: %+v", r.Locations)
	}
	if r.Locations[0].LogicalLocations[0].FullyQualifiedName == "" {
		t.Errorf("logicalLocation.fullyQualifiedName empty")
	}
	// F8: tool.driver.version must agree with cmd.Version (single source).
	if run.Tool.Driver.Version != Version {
		t.Errorf("driver.version: got %q, want %q (must match cmd.Version)",
			run.Tool.Driver.Version, Version)
	}
}

// TestDiscoVersion_DefaultDev pins the no-ldflag fallback. Tests run without
// the Makefile -X stamp, so the default "dev" must surface.
func TestDiscoVersion_DefaultDev(t *testing.T) {
	if discoVersion() != "dev" {
		t.Errorf("discoVersion() = %q, want dev (no ldflag in test build)", discoVersion())
	}
}

// TestCheckCmd_SARIF_Empty asserts an empty-findings run still produces a
// valid SARIF doc with results: [] — code-scanning ingesters use that to
// clear stale findings on subsequent green runs.
func TestCheckCmd_SARIF_Empty(t *testing.T) {
	seedTestDB(t) // no unencrypted volume — policy won't match
	resetCheckFlags()
	dir := writePolicy(t)

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--rules", dir, "--exit-zero", "-o", "sarif"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	var doc sarifLog
	if jerr := json.Unmarshal([]byte(out), &doc); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if len(doc.Runs) != 1 || len(doc.Runs[0].Results) != 0 {
		t.Errorf("want 1 run with 0 results, got %+v", doc.Runs)
	}
}

// TestCheckCmd_EvaluatesPastDefaultLimit guards against the silent
// 500-row truncation that ResourceFilter{} applies. Seeds 600 volumes,
// the last of which (sorted provider/type/name) is unencrypted; the
// policy must still fire on it.
func TestCheckCmd_EvaluatesPastDefaultLimit(t *testing.T) {
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	rows := make([]*store.Resource, 0, 600)
	for i := 0; i < 599; i++ {
		rows = append(rows, &store.Resource{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:volume",
			NativeID:       fmt.Sprintf("vol-good-%04d", i),
			AttributesJSON: `{"Encrypted": true}`, DiscoveredBy: scanID,
		})
	}
	rows = append(rows, &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:volume",
		NativeID: "vol-zzz-bad", AttributesJSON: `{"Encrypted": false}`, DiscoveredBy: scanID,
	})
	if _, err := st.UpsertResources(rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resetCheckFlags()
	dir := writePolicy(t)
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--rules", dir, "--exit-zero", "-o", "json"})
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
		t.Errorf("want 1 ebs-unencrypted finding past default 500-row cap, got %+v", fs)
	}
}

// TestCheckCmd_DefaultsCustomerOnly confirms the F24 fix: `disco check` with
// no flags evaluates customer-managed rows only. Provider-managed rows are
// out of scope until --include-managed is passed.
func TestCheckCmd_DefaultsCustomerOnly(t *testing.T) {
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	rows := []*store.Resource{
		{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:volume", NativeID: "vol-customer",
			AttributesJSON: `{"Encrypted": false}`, DiscoveredBy: scanID,
		},
		{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:volume", NativeID: "vol-managed",
			AttributesJSON: `{"Encrypted": false}`, DiscoveredBy: scanID, ManagedByProvider: true,
		},
	}
	if _, err := st.UpsertResources(rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resetCheckFlags()
	dir := writePolicy(t)
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--rules", dir, "--exit-zero", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	var fs []policy.Finding
	if jerr := json.Unmarshal([]byte(out), &fs); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if len(fs) != 1 {
		t.Fatalf("want 1 finding (customer only) by default, got %d: %+v", len(fs), fs)
	}

	resetCheckFlags()
	out, err = captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--rules", dir, "--include-managed", "--exit-zero", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("check --include-managed: %v", err)
	}
	if jerr := json.Unmarshal([]byte(out), &fs); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if len(fs) != 2 {
		t.Errorf("want 2 findings (customer + managed) with --include-managed, got %d", len(fs))
	}
}

// TestCheckCmd_RulesRequired confirms the command rejects an invocation
// with neither --rules nor --packs.
func TestCheckCmd_RulesRequired(t *testing.T) {
	seedTestDB(t)
	resetCheckFlags()

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "--rules or --packs") {
		t.Errorf("want --rules-or-packs error, got %v", err)
	}
}

// TestCheckCmd_PacksAWSWAF runs the bundled aws-waf pack against a seeded
// DB carrying one unencrypted EBS volume and asserts the
// waf-sec-ebs-encryption-at-rest finding fires end-to-end.
func TestCheckCmd_PacksAWSWAF(t *testing.T) {
	seedCheckDB(t) // plants vol-bad with Encrypted: false
	resetCheckFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--packs", "aws-waf", "--exit-zero", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	var fs []policy.Finding
	if jerr := json.Unmarshal([]byte(out), &fs); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	found := false
	for _, f := range fs {
		if f.ID == "waf-sec-ebs-encryption-at-rest" {
			found = true
			if f.Category != "aws-waf" {
				t.Errorf("category: got %q, want aws-waf", f.Category)
			}
		}
	}
	if !found {
		t.Errorf("waf-sec-ebs-encryption-at-rest not in findings: %+v", fs)
	}
}

// TestCheckCmd_PacksUnknown confirms an unknown pack name surfaces a
// user-readable error mentioning the available packs.
func TestCheckCmd_PacksUnknown(t *testing.T) {
	seedTestDB(t)
	resetCheckFlags()

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--packs", "bogus"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "available: aws-waf") {
		t.Errorf("want unknown-pack error mentioning aws-waf, got %v", err)
	}
}

// TestCheckCmd_PacksPlusRules asserts --packs and --rules compose: both
// sources evaluate against the same population in one query.
func TestCheckCmd_PacksPlusRules(t *testing.T) {
	seedCheckDB(t)
	resetCheckFlags()
	dir := writePolicy(t)

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--packs", "aws-waf", "--rules", dir, "--exit-zero", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	var fs []policy.Finding
	if jerr := json.Unmarshal([]byte(out), &fs); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	// Inline test policy fires ebs-unencrypted; WAF pack fires
	// waf-sec-ebs-encryption-at-rest. Same row, two findings.
	ids := map[string]bool{}
	for _, f := range fs {
		ids[f.ID] = true
	}
	if !ids["ebs-unencrypted"] || !ids["waf-sec-ebs-encryption-at-rest"] {
		t.Errorf("compose: want both ids, got %+v", ids)
	}
}
