package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveCosmosRelationships verifies that a Cosmos DB account with a
// keyVaultKeyUri CMEK reference resolves to the corresponding Key Vault.
func TestResolveCosmosRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-cosmos")

	vaultNativeID := "/subscriptions/sub-cosmos/resourceGroups/RG/providers/Microsoft.KeyVault/vaults/cosmosvault"
	vaultID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, vaultNativeID, "eastus", "{}")

	acctNativeID := "/subscriptions/sub-cosmos/resourceGroups/RG/providers/Microsoft.DocumentDB/databaseAccounts/myacct"
	acctAttrs := `{"properties":{"keyVaultKeyUri":"https://cosmosvault.vault.azure.net/keys/cosmos-cmk"}}`
	acctID := upsertTestResource(t, st, "azure", sub.ID, TypeCosmosDatabaseAccount, acctNativeID, "eastus", acctAttrs)

	if err := resolveCosmosRelationships(sub, st); err != nil {
		t.Fatalf("resolveCosmosRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(acctID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != vaultID || rels[0].Kind != store.RelUses {
		t.Errorf("expected cosmos -[uses]-> vault, got %+v", rels)
	}
}

// TestResolveCosmosRelationships_NoCMEK verifies that an account without
// keyVaultKeyUri produces no edge.
func TestResolveCosmosRelationships_NoCMEK(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-cosmos")

	acctID := upsertTestResource(t, st, "azure", sub.ID, TypeCosmosDatabaseAccount,
		"/subscriptions/sub-cosmos/resourceGroups/RG/providers/Microsoft.DocumentDB/databaseAccounts/plain", "eastus", "{}")

	if err := resolveCosmosRelationships(sub, st); err != nil {
		t.Fatalf("resolveCosmosRelationships (no cmek): %v", err)
	}
	rels, err := st.RelationshipsFrom(acctID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
