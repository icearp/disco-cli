package azure

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/netapp/armnetapp/v7"
)

// TestResolveNetAppRelationships verifies a CMK-encrypted NetApp account derives
// a -[uses]-> Key Vault edge from the full vault ARM ID (keyVaultResourceId),
// matched case-insensitively; an account whose vault is absent gets no edge.
func TestResolveNetAppRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	kvNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/naKv"
	kvID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, kvNativeID, "eastus", "{}")

	acct := armnetapp.Account{
		Properties: &armnetapp.AccountProperties{
			Encryption: &armnetapp.AccountEncryption{
				KeyVaultProperties: &armnetapp.KeyVaultProperties{
					// Upper-cased ARM ID reference exercises the lowercased index.
					KeyVaultResourceID: to.Ptr(upper(kvNativeID)),
				},
			},
		},
	}
	acctID := upsertTestResource(t, st, "azure", sub.ID, TypeNetAppAccount,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.NetApp/netAppAccounts/na", "eastus", marshalAttrs(t, acct))

	orphan := armnetapp.Account{
		Properties: &armnetapp.AccountProperties{
			Encryption: &armnetapp.AccountEncryption{
				KeyVaultProperties: &armnetapp.KeyVaultProperties{
					KeyVaultResourceID: to.Ptr("/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/missing"),
				},
			},
		},
	}
	orphanID := upsertTestResource(t, st, "azure", sub.ID, TypeNetAppAccount,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.NetApp/netAppAccounts/na2", "eastus", marshalAttrs(t, orphan))

	if err := resolveNetAppRelationships(sub, st); err != nil {
		t.Fatalf("resolveNetAppRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(acctID)
	if len(rels) != 1 || rels[0].ToID != kvID || rels[0].Kind != store.RelUses {
		t.Errorf("expected account -[uses]-> keyvault, got %+v", rels)
	}
	if orphanRels, _ := st.RelationshipsFrom(orphanID); len(orphanRels) != 0 {
		t.Errorf("expected no edge for account with absent CMK vault, got %+v", orphanRels)
	}
}

// TestResolveNetAppRelationships_NoCMK verifies an account without encryption
// produces no edge and does not panic on the missing JSON.
func TestResolveNetAppRelationships_NoCMK(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv", "eastus", "{}")
	acctID := upsertTestResource(t, st, "azure", sub.ID, TypeNetAppAccount,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.NetApp/netAppAccounts/na", "eastus", "{}")
	if err := resolveNetAppRelationships(sub, st); err != nil {
		t.Fatalf("resolveNetAppRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(acctID); len(rels) != 0 {
		t.Errorf("expected no edge, got %+v", rels)
	}
}
