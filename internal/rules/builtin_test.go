package rules

import "testing"

// TestBuiltins_Parse asserts every embedded YAML loads cleanly — guards against
// typos in the shipped rule set.
func TestBuiltins_Parse(t *testing.T) {
	rs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	if len(rs) == 0 {
		t.Fatal("no builtin rules embedded")
	}
	seen := map[string]bool{}
	for _, r := range rs {
		if seen[r.ID] {
			t.Errorf("duplicate builtin id %q", r.ID)
		}
		seen[r.ID] = true
		if r.ID == "" {
			t.Errorf("builtin rule missing id (source=%s)", r.Source)
		}
		if _, err := ParseSeverity(string(r.Severity)); err != nil {
			t.Errorf("rule %s: %v", r.ID, err)
		}
	}
}
