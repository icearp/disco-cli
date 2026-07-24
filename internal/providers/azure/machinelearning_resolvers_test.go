package azure

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v4"
)

// TestResolveMachineLearningRelationships verifies a workspace derives -[uses]->
// edges to its storage account, key vault, and container registry (full ARM
// IDs, matched case-insensitively), and emits none for an unscanned/absent ref.
func TestResolveMachineLearningRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	saNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/mlsa"
	saID := upsertTestResource(t, st, "azure", sub.ID, TypeStorageStorageAccount, saNativeID, "eastus", "{}")
	kvNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/mlkv"
	kvID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, kvNativeID, "eastus", "{}")
	acrNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.ContainerRegistry/registries/mlacr"
	acrID := upsertTestResource(t, st, "azure", sub.ID, TypeContainerRegistryRegistry, acrNativeID, "eastus", "{}")

	ws := armmachinelearning.Workspace{
		Properties: &armmachinelearning.WorkspaceProperties{
			StorageAccount:    to.Ptr(upper(saNativeID)),
			KeyVault:          to.Ptr(upper(kvNativeID)),
			ContainerRegistry: to.Ptr(upper(acrNativeID)),
			// applicationInsights points at an unscanned type → no edge.
			ApplicationInsights: to.Ptr("/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Insights/components/ai"),
		},
	}
	wsID := upsertTestResource(t, st, "azure", sub.ID, TypeMachineLearningWorkspace,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/mlw", "eastus", marshalAttrs(t, ws))

	if err := resolveMachineLearningRelationships(sub, st); err != nil {
		t.Fatalf("resolveMachineLearningRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(wsID)
	got := map[string]bool{}
	for _, r := range rels {
		if r.Kind != store.RelUses {
			t.Errorf("unexpected edge kind %q", r.Kind)
		}
		got[r.ToID] = true
	}
	if !got[saID] || !got[kvID] || !got[acrID] || len(rels) != 3 {
		t.Errorf("expected workspace -[uses]-> {storage, keyvault, acr}, got %+v", rels)
	}
}

// TestResolveMachineLearningRelationships_NoRefs verifies a workspace with no
// dependency refs produces no edges and does not panic.
func TestResolveMachineLearningRelationships_NoRefs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	wsID := upsertTestResource(t, st, "azure", sub.ID, TypeMachineLearningWorkspace,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.MachineLearningServices/workspaces/mlw", "eastus", "{}")
	if err := resolveMachineLearningRelationships(sub, st); err != nil {
		t.Fatalf("resolveMachineLearningRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(wsID); len(rels) != 0 {
		t.Errorf("expected no edges, got %+v", rels)
	}
}
