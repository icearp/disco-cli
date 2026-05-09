package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveSynapseRelationships verifies a Synapse workspace derives a
// -[uses]-> edge to the default ADLS Gen2 storage account via
// properties.defaultDataLakeStorage.resourceId. Match case-insensitive.
func TestResolveSynapseRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-syn")

	saNativeID := "/subscriptions/sub-syn/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/dlaccount"
	saID := upsertTestResource(t, st, "azure", sub.ID, TypeStorageStorageAccount, saNativeID, "eastus", "{}")

	wsAttrs := `{"properties":{"defaultDataLakeStorage":{"resourceId":"/SUBSCRIPTIONS/SUB-SYN/RESOURCEGROUPS/RG/PROVIDERS/MICROSOFT.STORAGE/STORAGEACCOUNTS/DLACCOUNT","filesystem":"users"}}}`
	wsID := upsertTestResource(t, st, "azure", sub.ID, TypeSynapseWorkspace,
		"/subscriptions/sub-syn/resourceGroups/RG/providers/Microsoft.Synapse/workspaces/ws", "eastus", wsAttrs)

	if err := resolveSynapseRelationships(sub, st); err != nil {
		t.Fatalf("resolveSynapseRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(wsID)
	if len(rels) != 1 || rels[0].ToID != saID || rels[0].Kind != store.RelUses {
		t.Errorf("expected synapse -[uses]-> storage, got %+v", rels)
	}
}
