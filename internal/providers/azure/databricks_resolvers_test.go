package azure

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

// TestResolveDatabricksRelationships verifies a VNet-injected Databricks
// workspace derives an attached-to edge to the custom VNet via
// properties.parameters.customVirtualNetworkId.value (case-insensitive).
func TestResolveDatabricksRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-dbr")

	vnetNativeID := "/subscriptions/sub-dbr/resourceGroups/Net/providers/Microsoft.Network/virtualNetworks/dbr-vnet"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")

	wsAttrs := `{"properties":{"parameters":{"customVirtualNetworkId":{"value":"/SUBSCRIPTIONS/SUB-DBR/RESOURCEGROUPS/NET/PROVIDERS/MICROSOFT.NETWORK/VIRTUALNETWORKS/DBR-VNET","type":"String"}}}}`
	wsID := upsertTestResource(t, st, "azure", sub.ID, TypeDatabricksWorkspace,
		"/subscriptions/sub-dbr/resourceGroups/RG/providers/Microsoft.Databricks/workspaces/ws", "eastus", wsAttrs)

	if err := resolveDatabricksRelationships(sub, st); err != nil {
		t.Fatalf("resolveDatabricksRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(wsID)
	if len(rels) != 1 || rels[0].ToID != vnetID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected workspace -[attached-to]-> vnet, got %+v", rels)
	}
}

// TestResolveDatabricksRelationships_NoCustomVNet verifies a default-VNet
// workspace (no customVirtualNetworkId parameter) produces no edge.
func TestResolveDatabricksRelationships_NoCustomVNet(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-dbr")

	wsID := upsertTestResource(t, st, "azure", sub.ID, TypeDatabricksWorkspace,
		"/subscriptions/sub-dbr/resourceGroups/RG/providers/Microsoft.Databricks/workspaces/default", "eastus", "{}")

	if err := resolveDatabricksRelationships(sub, st); err != nil {
		t.Fatalf("resolveDatabricksRelationships (default vnet): %v", err)
	}
	rels, _ := st.RelationshipsFrom(wsID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
