package azure

import (
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/batch/armbatch"
	"github.com/icearp/disco-cli/store"
)

// TestResolveBatchRelationships verifies a Batch account derives -[uses]->
// edges to its auto-storage account and key vault, both matched
// case-insensitively against full ARM resource IDs.
func TestResolveBatchRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	saNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/batchsa"
	saID := upsertTestResource(t, st, "azure", sub.ID, TypeStorageStorageAccount, saNativeID, "eastus", "{}")
	kvNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/batchkv"
	kvID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, kvNativeID, "eastus", "{}")

	acct := armbatch.Account{
		Properties: &armbatch.AccountProperties{
			// Upper-cased references exercise the lowercased NativeID index.
			AutoStorage:       &armbatch.AutoStorageProperties{StorageAccountID: to.Ptr(upper(saNativeID))},
			KeyVaultReference: &armbatch.KeyVaultReference{ID: to.Ptr(upper(kvNativeID)), URL: to.Ptr("https://batchkv.vault.azure.net/")},
		},
	}
	acctNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Batch/batchAccounts/ba"
	acctID := upsertTestResource(t, st, "azure", sub.ID, TypeBatchAccount, acctNativeID, "eastus", marshalAttrs(t, acct))

	if err := resolveBatchRelationships(sub, st); err != nil {
		t.Fatalf("resolveBatchRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(acctID)
	got := map[string]bool{}
	for _, r := range rels {
		if r.Kind != store.RelUses {
			t.Errorf("unexpected edge kind %q", r.Kind)
		}
		got[r.ToID] = true
	}
	if !got[saID] || !got[kvID] || len(rels) != 2 {
		t.Errorf("expected account -[uses]-> {storage, keyvault}, got %+v", rels)
	}
}

// TestResolveBatchRelationships_NoRefs verifies a Batch account with no
// auto-storage / key-vault references (and missing targets) produces no edge.
func TestResolveBatchRelationships_NoRefs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	acctID := upsertTestResource(t, st, "azure", sub.ID, TypeBatchAccount,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.Batch/batchAccounts/ba",
		"eastus", "{}")

	if err := resolveBatchRelationships(sub, st); err != nil {
		t.Fatalf("resolveBatchRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(acctID); len(rels) != 0 {
		t.Errorf("expected no edges, got %+v", rels)
	}
}

// upper upper-cases the Microsoft.* provider segment of an ARM ID to simulate
// the case drift Azure preserves from create-time input.
func upper(armID string) string {
	return strings.ToUpper(armID)
}
