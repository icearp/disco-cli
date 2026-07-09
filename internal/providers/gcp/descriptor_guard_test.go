package gcp

import "testing"

// TestNoDoubleDeclaredTypes ensures a disco type is declared EITHER the legacy
// way (serviceEntry.emits / registerExtraEmits) OR via registerType, never
// both. A type in both sets would double-forward redact/volatile/managed rules
// and make the migration's intent ambiguous; CollectEmits dedups the coverage
// decl so the overlap would otherwise be silent.
func TestNoDoubleDeclaredTypes(t *testing.T) {
	legacy := make(map[string]bool)
	for _, d := range extraEmits {
		legacy[d.DiscoType] = true
	}
	for _, s := range registeredServices {
		for _, d := range s.emits {
			legacy[d.DiscoType] = true
		}
	}
	for _, s := range registeredOrgServices {
		for _, d := range s.emits {
			legacy[d.DiscoType] = true
		}
	}

	for _, d := range registeredDescriptors {
		if legacy[d.Type] {
			t.Errorf("type %q declared via BOTH registerType and a legacy emit — remove the legacy site", d.Type)
		}
	}
}
