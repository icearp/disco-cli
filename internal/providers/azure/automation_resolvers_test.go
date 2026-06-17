package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/automation/armautomation"
)

// TestResolveAutomationRelationships verifies a CMK-encrypted automation
// account derives a -[uses]-> Key Vault edge from the keyvaultUri vault root,
// matched case-insensitively.
func TestResolveAutomationRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	kvNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/AutoKv"
	kvID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, kvNativeID, "eastus", "{}")

	acct := armautomation.Account{
		Properties: &armautomation.AccountProperties{
			Encryption: &armautomation.EncryptionProperties{
				KeyVaultProperties: &armautomation.KeyVaultProperties{
					KeyvaultURI: to.Ptr("https://autokv.vault.azure.net/"),
				},
			},
		},
	}
	acctID := upsertTestResource(t, st, "azure", sub.ID, TypeAutomationAccount,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.Automation/automationAccounts/aa", "eastus", marshalAttrs(t, acct))

	if err := resolveAutomationRelationships(sub, st); err != nil {
		t.Fatalf("resolveAutomationRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(acctID)
	if len(rels) != 1 || rels[0].ToID != kvID || rels[0].Kind != store.RelUses {
		t.Errorf("expected account -[uses]-> keyvault, got %+v", rels)
	}
}

// TestResolveAutomationRelationships_NoCMK verifies an account without
// encryption produces no edge and does not panic.
func TestResolveAutomationRelationships_NoCMK(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv", "eastus", "{}")
	acctID := upsertTestResource(t, st, "azure", sub.ID, TypeAutomationAccount,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.Automation/automationAccounts/aa", "eastus", "{}")
	if err := resolveAutomationRelationships(sub, st); err != nil {
		t.Fatalf("resolveAutomationRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(acctID); len(rels) != 0 {
		t.Errorf("expected no edge, got %+v", rels)
	}
}
