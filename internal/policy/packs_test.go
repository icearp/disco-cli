package policy

import (
	"context"
	"strings"
	"testing"
)

func TestLoadPacks_AWSWAF(t *testing.T) {
	mods, err := LoadPacks([]string{"aws-waf"})
	if err != nil {
		t.Fatalf("LoadPacks: %v", err)
	}
	if len(mods) != 5 {
		t.Errorf("aws-waf: got %d modules, want 5", len(mods))
	}
	for path := range mods {
		if !strings.HasPrefix(path, "aws-waf/") {
			t.Errorf("module key %q missing aws-waf/ prefix", path)
		}
	}
}

func TestLoadPacks_Unknown(t *testing.T) {
	_, err := LoadPacks([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "available: aws-waf") {
		t.Errorf("want unknown-pack error mentioning aws-waf, got %v", err)
	}
}

func TestLoadPacks_Empty(t *testing.T) {
	mods, err := LoadPacks(nil)
	if err != nil {
		t.Fatalf("LoadPacks nil: %v", err)
	}
	if len(mods) != 0 {
		t.Errorf("empty input: got %d modules, want 0", len(mods))
	}
}

// TestAWSWAF_Compiles asserts the bundled pack compiles cleanly through
// the full engine path — catches any rego.v1 violations / sprintf shape
// drift in the embedded modules at build time.
func TestAWSWAF_Compiles(t *testing.T) {
	mods, err := LoadPacks([]string{"aws-waf"})
	if err != nil {
		t.Fatalf("LoadPacks: %v", err)
	}
	if _, err := NewEngine(context.Background(), nil, mods); err != nil {
		t.Fatalf("aws-waf compile: %v", err)
	}
}
