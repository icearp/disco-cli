package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/recoveryservices/armrecoveryservices"
)

// TestResolveRecoveryServicesRelationships verifies a CMK-encrypted vault
// derives a -[uses]-> Key Vault edge from keyUri.
func TestResolveRecoveryServicesRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	// Mixed-case vault name segment vs lowercase key URI exercises the
	// resolver's index-side normalization (see cognitiveservices test).
	kvNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/RsvKv"
	kvID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, kvNativeID, "eastus", "{}")

	vault := armrecoveryservices.Vault{
		Properties: &armrecoveryservices.VaultProperties{
			Encryption: &armrecoveryservices.VaultPropertiesEncryption{
				KeyVaultProperties: &armrecoveryservices.CmkKeyVaultProperties{
					KeyURI: to.Ptr("https://rsvkv.vault.azure.net/keys/k/v1"),
				},
			},
		},
	}
	vaultNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.RecoveryServices/vaults/rsv"
	vaultID := upsertTestResource(t, st, "azure", sub.ID, TypeRecoveryServicesVault, vaultNativeID, "eastus", marshalAttrs(t, vault))

	// A vault referencing a Key Vault not in scope must yield no edge.
	orphan := armrecoveryservices.Vault{
		Properties: &armrecoveryservices.VaultProperties{
			Encryption: &armrecoveryservices.VaultPropertiesEncryption{
				KeyVaultProperties: &armrecoveryservices.CmkKeyVaultProperties{
					KeyURI: to.Ptr("https://missing.vault.azure.net/keys/k/v1"),
				},
			},
		},
	}
	orphanNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.RecoveryServices/vaults/rsv2"
	orphanID := upsertTestResource(t, st, "azure", sub.ID, TypeRecoveryServicesVault, orphanNativeID, "eastus", marshalAttrs(t, orphan))

	if err := resolveRecoveryServicesRelationships(sub, st); err != nil {
		t.Fatalf("resolveRecoveryServicesRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vaultID)
	if len(rels) != 1 || rels[0].ToID != kvID || rels[0].Kind != store.RelUses {
		t.Errorf("expected vault -[uses]-> keyvault, got %+v", rels)
	}
	if orphanRels, _ := st.RelationshipsFrom(orphanID); len(orphanRels) != 0 {
		t.Errorf("expected no edge for vault with absent CMK key vault, got %+v", orphanRels)
	}
}

// TestResolveRecoveryServicesRelationships_NoCMK verifies a vault without
// encryption properties produces no edge and doesn't panic on missing JSON
// (guards the nil-pointer path through properties.encryption).
func TestResolveRecoveryServicesRelationships_NoCMK(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv", "eastus", "{}")
	vaultID := upsertTestResource(t, st, "azure", sub.ID, TypeRecoveryServicesVault,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.RecoveryServices/vaults/rsv",
		"eastus", "{}")

	if err := resolveRecoveryServicesRelationships(sub, st); err != nil {
		t.Fatalf("resolveRecoveryServicesRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(vaultID); len(rels) != 0 {
		t.Errorf("expected no edge for vault without CMK, got %+v", rels)
	}
}
