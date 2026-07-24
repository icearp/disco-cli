package azure

import (
	"testing"

	"github.com/icearp/disco-cli/internal/managed"
)

// TestRackSKUStampedManaged proves the descriptor's Managed:true flag reached
// the shared managed engine (which the store consults to stamp
// ManagedByProvider), replacing the deleted scanner-side literal. RackSKU is
// Azure's only unconditionally-managed type.
func TestRackSKUStampedManaged(t *testing.T) {
	if !managed.Is(TypeNetworkCloudRackSKU) {
		t.Errorf("%s not registered as managed — Managed:true not forwarded", TypeNetworkCloudRackSKU)
	}
}

// TestNoDoubleDeclaredTypes ensures a disco type is declared EITHER the legacy
// way (serviceEntry.emits / registerExtraEmits) OR via registerType, never
// both — a type in both sets would double-forward redact/volatile/managed
// rules while CollectEmits silently dedups the coverage decl.
func TestNoDoubleDeclaredTypes(t *testing.T) {
	legacy := make(map[string]bool)
	for _, s := range registeredServices {
		for _, d := range s.emits {
			legacy[d.DiscoType] = true
		}
	}
	for _, s := range registeredTenantServices {
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
