package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/managed"
)

// TestNoDoubleDeclaredTypes ensures a disco type is declared EITHER the legacy
// way (serviceEntry.emits / registerExtraEmits) OR via registerType, never
// both — a type in both sets would double-forward redact/volatile/managed
// rules while CollectEmits silently dedups the coverage decl. Post-migration
// the legacy sets are empty; the test guards against a future half-migration.
func TestNoDoubleDeclaredTypes(t *testing.T) {
	legacy := make(map[string]bool)
	for _, s := range registeredServices {
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

// descriptorEmitted reports whether ty was declared via registerType — the
// post-migration source of coverage emits. Test helper for the per-service
// registration-covered tests that used to iterate serviceEntry.emits.
func descriptorEmitted(ty string) bool {
	for _, d := range registeredDescriptors {
		if d.Type == ty {
			return true
		}
	}
	return false
}

// serviceRegistered reports whether a registerService call named `name` exists.
func serviceRegistered(name string) bool {
	for _, s := range registeredServices {
		if s.name == name {
			return true
		}
	}
	return false
}

// TestUnconditionalManagedStampedByType proves each unconditionally-managed
// type's Descriptor.Managed:true reached the shared managed engine (which the
// store consults to stamp ManagedByProvider), replacing the deleted scanner
// literals. Dual-natured types (IAMPolicy, LambdaLayerVersion, OrganizationsOU)
// are deliberately absent — their managed flag stays per-row scanner-set.
func TestUnconditionalManagedStampedByType(t *testing.T) {
	wantManaged := []string{
		TypeServiceQuota, TypeQuickSightAccount, TypeQuickSightCustomization,
		TypeOrganizationsRoot, TypeBedrockFoundationModel, TypeAPIGatewayAccount,
		TypeSecurityHubStandard, TypeRoute53ResolverResolverConfig,
	}
	for _, ty := range wantManaged {
		if !managed.Is(ty) {
			t.Errorf("%s not registered as managed — Descriptor.Managed:true not forwarded", ty)
		}
	}
	// Dual-natured types must NOT be type-stamped (would wrongly hide customer rows).
	for _, ty := range []string{TypeIAMPolicy, TypeLambdaLayerVersion, TypeOrganizationsOU} {
		if managed.Is(ty) {
			t.Errorf("%s type-stamped managed — dual-natured type must stay scanner-set", ty)
		}
	}
}
