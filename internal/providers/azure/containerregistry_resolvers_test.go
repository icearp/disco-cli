package azure

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

// TestResolveContainerRegistryRelationships verifies an ACR with a CMEK
// keyIdentifier resolves to its Key Vault by parsing the key URI host and
// matching the leading subdomain against the local vault-name index
// (case-insensitive).
func TestResolveContainerRegistryRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-acr")

	vaultNativeID := "/subscriptions/sub-acr/resourceGroups/RG/providers/Microsoft.KeyVault/vaults/MyVault"
	vaultID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, vaultNativeID, "eastus", "{}")

	regNativeID := "/subscriptions/sub-acr/resourceGroups/RG/providers/Microsoft.ContainerRegistry/registries/myreg"
	regAttrs := `{"properties":{"encryption":{"status":"enabled","keyVaultProperties":{"keyIdentifier":"https://MYVAULT.vault.azure.net/keys/acr-cmk/abc123"}}}}`
	regID := upsertTestResource(t, st, "azure", sub.ID, TypeContainerRegistryRegistry, regNativeID, "eastus", regAttrs)

	if err := resolveContainerRegistryRelationships(sub, st); err != nil {
		t.Fatalf("resolveContainerRegistryRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(regID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != vaultID || rels[0].Kind != store.RelUses {
		t.Errorf("expected acr -[uses]-> vault, got %+v", rels)
	}
}

// TestResolveContainerRegistryRelationships_NoCMEK verifies that an ACR
// without an encryption.keyVaultProperties block produces no edge.
func TestResolveContainerRegistryRelationships_NoCMEK(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-acr")

	regID := upsertTestResource(t, st, "azure", sub.ID, TypeContainerRegistryRegistry,
		"/subscriptions/sub-acr/resourceGroups/RG/providers/Microsoft.ContainerRegistry/registries/plain", "eastus", "{}")

	if err := resolveContainerRegistryRelationships(sub, st); err != nil {
		t.Fatalf("resolveContainerRegistryRelationships (no cmek): %v", err)
	}
	rels, err := st.RelationshipsFrom(regID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestVaultNameFromKeyURI verifies the host-suffix-based parser handles the
// public, US-government, China, and Germany Key Vault DNS suffixes plus a
// non-vault URI (returns "").
func TestVaultNameFromKeyURI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://myvault.vault.azure.net/keys/k/v", "myvault"},
		{"https://gov.vault.usgovcloudapi.net/keys/k", "gov"},
		{"https://cn.vault.azure.cn/keys/k/v", "cn"},
		{"https://de.vault.microsoftazure.de/keys/k", "de"},
		{"https://example.com/keys/k", ""},
		{"not a url", ""},
	}
	for _, tc := range cases {
		if got := vaultNameFromKeyURI(tc.in); got != tc.want {
			t.Errorf("vaultNameFromKeyURI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
