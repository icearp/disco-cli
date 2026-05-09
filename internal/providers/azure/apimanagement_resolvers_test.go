package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveAPIManagementRelationships verifies a VNet-injected APIM service
// derives an attached-to edge to the VNet via
// properties.virtualNetworkConfiguration.subnetResourceId.
func TestResolveAPIManagementRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-apim")

	vnetNativeID := "/subscriptions/sub-apim/resourceGroups/Net/providers/Microsoft.Network/virtualNetworks/vnet"
	subnetNativeID := vnetNativeID + "/subnets/apim"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")

	apimID := upsertTestResource(t, st, "azure", sub.ID, TypeAPIManagementService,
		"/subscriptions/sub-apim/resourceGroups/RG/providers/Microsoft.ApiManagement/service/apim1",
		"eastus",
		`{"properties":{"virtualNetworkType":"Internal","virtualNetworkConfiguration":{"subnetResourceId":"`+subnetNativeID+`"}}}`)

	if err := resolveAPIManagementRelationships(sub, st); err != nil {
		t.Fatalf("resolveAPIManagementRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(apimID)
	if len(rels) != 1 || rels[0].ToID != vnetID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected apim -[attached-to]-> vnet, got %+v", rels)
	}
}

// TestResolveAPIManagementRelationships_None verifies a non-VNet (None) APIM
// produces no edge.
func TestResolveAPIManagementRelationships_None(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-apim")

	apimID := upsertTestResource(t, st, "azure", sub.ID, TypeAPIManagementService,
		"/subscriptions/sub-apim/resourceGroups/RG/providers/Microsoft.ApiManagement/service/none", "eastus",
		`{"properties":{"virtualNetworkType":"None"}}`)

	if err := resolveAPIManagementRelationships(sub, st); err != nil {
		t.Fatalf("resolveAPIManagementRelationships (none): %v", err)
	}
	rels, _ := st.RelationshipsFrom(apimID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
