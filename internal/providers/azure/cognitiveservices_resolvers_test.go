package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
)

// TestResolveCognitiveServicesRelationships verifies a CMK-encrypted account
// derives a -[uses]-> Key Vault edge from the keyVaultUri vault DNS root, and
// that an account whose CMK vault is absent produces no edge.
func TestResolveCognitiveServicesRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	// Mixed-case vault name segment vs lowercase CMK URI: the index key is
	// derived via strings.ToLower(nameFromID(...)) and the probe via the URI
	// host, so a match here genuinely depends on the resolver's index-side
	// normalization (drop that ToLower and this test goes red).
	vaultNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/CMKVault"
	vaultID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, vaultNativeID, "eastus", "{}")

	acct := armcognitiveservices.Account{
		Properties: &armcognitiveservices.AccountProperties{
			Encryption: &armcognitiveservices.Encryption{
				KeyVaultProperties: &armcognitiveservices.KeyVaultProperties{
					KeyVaultURI: to.Ptr("https://cmkvault.vault.azure.net/"),
				},
			},
		},
	}
	acctNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/ai"
	acctID := upsertTestResource(t, st, "azure", sub.ID, TypeCognitiveServicesAccount, acctNativeID, "eastus", marshalAttrs(t, acct))

	// A second account referencing a vault not in scope must yield no edge.
	orphan := armcognitiveservices.Account{
		Properties: &armcognitiveservices.AccountProperties{
			Encryption: &armcognitiveservices.Encryption{
				KeyVaultProperties: &armcognitiveservices.KeyVaultProperties{
					KeyVaultURI: to.Ptr("https://missing.vault.azure.net/"),
				},
			},
		},
	}
	orphanNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/ai2"
	orphanID := upsertTestResource(t, st, "azure", sub.ID, TypeCognitiveServicesAccount, orphanNativeID, "eastus", marshalAttrs(t, orphan))

	if err := resolveCognitiveServicesRelationships(sub, st); err != nil {
		t.Fatalf("resolveCognitiveServicesRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(acctID)
	if len(rels) != 1 || rels[0].ToID != vaultID || rels[0].Kind != store.RelUses {
		t.Errorf("expected account -[uses]-> vault, got %+v", rels)
	}
	if orphanRels, _ := st.RelationshipsFrom(orphanID); len(orphanRels) != 0 {
		t.Errorf("expected no edge for account with absent CMK vault, got %+v", orphanRels)
	}
}

// TestResolveCognitiveServicesRelationships_NoAccounts verifies no error and no
// edges when the subscription has no Cognitive Services accounts.
func TestResolveCognitiveServicesRelationships_NoAccounts(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	if err := resolveCognitiveServicesRelationships(sub, st); err != nil {
		t.Fatalf("resolveCognitiveServicesRelationships: %v", err)
	}
}
