package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestRulesSHA256_EmptyInput(t *testing.T) {
	got, err := RulesSHA256(nil, nil)
	if err != nil {
		t.Fatalf("RulesSHA256: %v", err)
	}
	want := hex.EncodeToString(sha256.New().Sum(nil))
	if got != want {
		t.Errorf("empty input: got %s, want %s", got, want)
	}
}

func TestRulesSHA256_DeterministicAcrossOrdering(t *testing.T) {
	mods1 := map[string]string{"a.rego": "package a", "b.rego": "package b"}
	mods2 := map[string]string{"b.rego": "package b", "a.rego": "package a"}
	h1, err := RulesSHA256(nil, mods1)
	if err != nil {
		t.Fatalf("h1: %v", err)
	}
	h2, err := RulesSHA256(nil, mods2)
	if err != nil {
		t.Fatalf("h2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash not order-independent: %s vs %s", h1, h2)
	}
}

func TestRulesSHA256_PerturbsOnContentChange(t *testing.T) {
	h1, _ := RulesSHA256(nil, map[string]string{"a.rego": "package a"})
	h2, _ := RulesSHA256(nil, map[string]string{"a.rego": "package b"})
	if h1 == h2 {
		t.Error("content change must perturb hash")
	}
}

func TestRulesSHA256_PathsAndModulesMerge(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.rego"), []byte("package f"), 0o600); err != nil {
		t.Fatalf("write rego: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("ignore me"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	got, err := RulesSHA256([]string{dir}, map[string]string{"m.rego": "package m"})
	if err != nil {
		t.Fatalf("RulesSHA256: %v", err)
	}
	if got == "" {
		t.Error("expected non-empty hash")
	}
	// Re-hashing with the .txt removed must yield the same value (paths-walker
	// only picks up .rego).
	if err := os.Remove(filepath.Join(dir, "skip.txt")); err != nil {
		t.Fatalf("remove txt: %v", err)
	}
	got2, _ := RulesSHA256([]string{dir}, map[string]string{"m.rego": "package m"})
	if got != got2 {
		t.Errorf("non-rego file affected hash: %s vs %s", got, got2)
	}
}
