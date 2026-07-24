package azure

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appconfiguration/armappconfiguration"
)

// TestResolveAppConfigurationRelationships verifies a CMK-encrypted store
// derives a -[uses]-> Key Vault edge from the keyIdentifier key URI.
func TestResolveAppConfigurationRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	// Mixed-case vault name segment vs lowercase key URI exercises the
	// resolver's index-side normalization (see cognitiveservices test).
	vaultNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/AppCfgVault"
	vaultID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, vaultNativeID, "eastus", "{}")

	cfg := armappconfiguration.ConfigurationStore{
		Properties: &armappconfiguration.ConfigurationStoreProperties{
			Encryption: &armappconfiguration.EncryptionProperties{
				KeyVaultProperties: &armappconfiguration.KeyVaultProperties{
					KeyIdentifier: to.Ptr("https://appcfgvault.vault.azure.net/keys/k/abc123"),
				},
			},
		},
	}
	storeNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.AppConfiguration/configurationStores/cfg"
	storeID := upsertTestResource(t, st, "azure", sub.ID, TypeAppConfigurationStore, storeNativeID, "eastus", marshalAttrs(t, cfg))

	// A second store referencing a vault not in scope must yield no edge.
	orphan := armappconfiguration.ConfigurationStore{
		Properties: &armappconfiguration.ConfigurationStoreProperties{
			Encryption: &armappconfiguration.EncryptionProperties{
				KeyVaultProperties: &armappconfiguration.KeyVaultProperties{
					KeyIdentifier: to.Ptr("https://missing.vault.azure.net/keys/k/abc123"),
				},
			},
		},
	}
	orphanNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.AppConfiguration/configurationStores/cfg2"
	orphanID := upsertTestResource(t, st, "azure", sub.ID, TypeAppConfigurationStore, orphanNativeID, "eastus", marshalAttrs(t, orphan))

	if err := resolveAppConfigurationRelationships(sub, st); err != nil {
		t.Fatalf("resolveAppConfigurationRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(storeID)
	if len(rels) != 1 || rels[0].ToID != vaultID || rels[0].Kind != store.RelUses {
		t.Errorf("expected store -[uses]-> vault, got %+v", rels)
	}
	if orphanRels, _ := st.RelationshipsFrom(orphanID); len(orphanRels) != 0 {
		t.Errorf("expected no edge for store with absent CMK vault, got %+v", orphanRels)
	}
}

// TestResolveAppConfigurationRelationships_NoCMK verifies a store without
// encryption properties produces no edge (and no panic on missing JSON).
func TestResolveAppConfigurationRelationships_NoCMK(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/v", "eastus", "{}")
	storeID := upsertTestResource(t, st, "azure", sub.ID, TypeAppConfigurationStore,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.AppConfiguration/configurationStores/cfg",
		"eastus", "{}")

	if err := resolveAppConfigurationRelationships(sub, st); err != nil {
		t.Fatalf("resolveAppConfigurationRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(storeID); len(rels) != 0 {
		t.Errorf("expected no edge for store without CMK, got %+v", rels)
	}
}
