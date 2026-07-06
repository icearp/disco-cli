package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveMessagingRelationships verifies CMEK-enabled Event Hubs and
// Service Bus namespaces derive -[uses]-> Key Vault edges via
// properties.encryption.keyVaultProperties[].keyVaultUri (DNS root form).
// Multiple entries pointing at the same vault collapse to one edge per
// namespace.
func TestResolveMessagingRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-msg")

	vaultID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault,
		"/subscriptions/sub-msg/resourceGroups/RG/providers/Microsoft.KeyVault/vaults/myvault", "eastus", "{}")

	ehAttrs := `{"properties":{"encryption":{"keySource":"Microsoft.KeyVault","keyVaultProperties":[{"keyName":"k1","keyVaultUri":"https://MYVAULT.vault.azure.net/","keyVersion":"v1"},{"keyName":"k2","keyVaultUri":"https://myvault.vault.azure.net/","keyVersion":"v2"}]}}}`
	ehID := upsertTestResource(t, st, "azure", sub.ID, TypeEventHubNamespace,
		"/subscriptions/sub-msg/resourceGroups/RG/providers/Microsoft.EventHub/namespaces/ehns", "eastus", ehAttrs)

	sbAttrs := `{"properties":{"encryption":{"keyVaultProperties":[{"keyVaultUri":"https://myvault.vault.azure.net/"}]}}}`
	sbID := upsertTestResource(t, st, "azure", sub.ID, TypeServiceBusNamespace,
		"/subscriptions/sub-msg/resourceGroups/RG/providers/Microsoft.ServiceBus/namespaces/sbns", "eastus", sbAttrs)

	if err := resolveMessagingRelationships(sub, st); err != nil {
		t.Fatalf("resolveMessagingRelationships: %v", err)
	}
	for _, c := range []struct {
		name string
		id   string
	}{{"eventhub", ehID}, {"servicebus", sbID}} {
		rels, _ := st.RelationshipsFrom(c.id)
		if len(rels) != 1 || rels[0].ToID != vaultID || rels[0].Kind != store.RelUses {
			t.Errorf("%s: expected -[uses]-> vault, got %+v", c.name, rels)
		}
	}
}

// TestVaultNameFromVaultURI verifies the DNS-root parser handles the public
// suffix and a non-vault URI (returns "").
func TestVaultNameFromVaultURI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://myvault.vault.azure.net/", "myvault"},
		{"https://gov.vault.usgovcloudapi.net/", "gov"},
		{"https://example.com/", ""},
	}
	for _, tc := range cases {
		if got := vaultNameFromVaultURI(tc.in); got != tc.want {
			t.Errorf("vaultNameFromVaultURI(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
