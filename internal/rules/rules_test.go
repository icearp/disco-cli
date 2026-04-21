package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestLoad_Valid exercises the happy path: file loads, fields populated,
// Source set.
func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	p := writeYAML(t, dir, "r.yaml", `
version: 1
rules:
  - id: x
    description: test
    severity: medium
    match:
      type: aws:ec2:volume
      where:
        - path: Encrypted
          op: eq
          value: false
`)
	rs, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(rs) != 1 || rs[0].ID != "x" || rs[0].Source != p {
		t.Errorf("bad rule: %+v", rs)
	}
}

// TestLoad_BadVersion asserts unknown schema version is rejected — guards
// against silently evaluating a file whose schema we don't understand.
func TestLoad_BadVersion(t *testing.T) {
	dir := t.TempDir()
	p := writeYAML(t, dir, "r.yaml", "version: 99\nrules: []\n")
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Errorf("want schema-version error, got %v", err)
	}
}

// TestLoad_Directory asserts directory inputs walk recursively and pick up
// every *.yaml/*.yml.
func TestLoad_Directory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeYAML(t, dir, "a.yaml", "version: 1\nrules:\n  - {id: a, severity: low, match: {}}\n")
	writeYAML(t, sub, "b.yml", "version: 1\nrules:\n  - {id: b, severity: low, match: {}}\n")
	rs, err := Load(dir)
	if err != nil {
		t.Fatalf("Load dir: %v", err)
	}
	if len(rs) != 2 {
		t.Errorf("want 2 rules, got %d", len(rs))
	}
}

// TestLoad_DuplicateID asserts duplicate ids across sources fail loudly —
// silent override would be a footgun for rule authors.
func TestLoad_DuplicateID(t *testing.T) {
	dir := t.TempDir()
	p1 := writeYAML(t, dir, "a.yaml", "version: 1\nrules:\n  - {id: dup, severity: low, match: {}}\n")
	p2 := writeYAML(t, dir, "b.yaml", "version: 1\nrules:\n  - {id: dup, severity: low, match: {}}\n")
	if _, err := Load(p1, p2); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("want duplicate error, got %v", err)
	}
}

// TestLoad_BadOp asserts unknown predicate operator fails validation.
func TestLoad_BadOp(t *testing.T) {
	dir := t.TempDir()
	p := writeYAML(t, dir, "r.yaml", `
version: 1
rules:
  - id: x
    severity: low
    match:
      where:
        - {path: foo, op: bogus, value: 1}
`)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Errorf("want unknown-op error, got %v", err)
	}
}

// TestLoad_BadSeverity asserts bad severity fails validation.
func TestLoad_BadSeverity(t *testing.T) {
	dir := t.TempDir()
	p := writeYAML(t, dir, "r.yaml", "version: 1\nrules:\n  - {id: x, severity: urgent, match: {}}\n")
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "invalid severity") {
		t.Errorf("want severity error, got %v", err)
	}
}

// TestSeverity_AtLeast verifies the ordering used by --severity.
func TestSeverity_AtLeast(t *testing.T) {
	if !SevCritical.AtLeast(SevLow) {
		t.Error("critical should be >= low")
	}
	if SevLow.AtLeast(SevHigh) {
		t.Error("low should not be >= high")
	}
}
