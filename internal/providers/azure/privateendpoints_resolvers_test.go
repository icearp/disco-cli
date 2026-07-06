package azure

import (
	"testing"
)

// TestResolvePrivateEndpointRelationships verifies a PE produces:
// (a) attached-to → VNet via subnet path stripping,
// (b) attached-to → target storage account via privateLinkServiceConnections.
// Match on target ID is case-insensitive.
func TestResolvePrivateEndpointRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-pe")

	vnetNativeID := "/subscriptions/sub-pe/resourceGroups/Net/providers/Microsoft.Network/virtualNetworks/vnet"
	subnetNativeID := vnetNativeID + "/subnets/pe"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")

	storageNativeID := "/subscriptions/sub-pe/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/myacct"
	storageID := upsertTestResource(t, st, "azure", sub.ID, TypeStorageStorageAccount, storageNativeID, "eastus", "{}")

	peNativeID := "/subscriptions/sub-pe/resourceGroups/RG/providers/Microsoft.Network/privateEndpoints/pe1"
	peAttrs := `{"properties":{"subnet":{"id":"` + subnetNativeID + `"},"privateLinkServiceConnections":[{"properties":{"privateLinkServiceId":"/SUBSCRIPTIONS/SUB-PE/RESOURCEGROUPS/RG/PROVIDERS/MICROSOFT.STORAGE/STORAGEACCOUNTS/MYACCT","groupIds":["blob"]}}]}}`
	peID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkPrivateEndpoint, peNativeID, "eastus", peAttrs)

	if err := resolvePrivateEndpointRelationships(sub, st); err != nil {
		t.Fatalf("resolvePrivateEndpointRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(peID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d (%+v)", len(rels), rels)
	}
	got := map[string]bool{}
	for _, r := range rels {
		got[r.ToID] = true
	}
	if !got[vnetID] || !got[storageID] {
		t.Errorf("expected edges to vnet (%s) and storage (%s), got %+v", vnetID, storageID, rels)
	}
}

// TestResolvePrivateEndpointRelationships_SubResourceTargetID verifies a PE
// whose privateLinkServiceId points to a sub-resource path (e.g.
// /storageAccounts/foo/blobServices/default) trims back to the parent
// resource disco actually has stored.
func TestResolvePrivateEndpointRelationships_SubResourceTargetID(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-pe")

	storageNativeID := "/subscriptions/sub-pe/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/myacct"
	storageID := upsertTestResource(t, st, "azure", sub.ID, TypeStorageStorageAccount, storageNativeID, "eastus", "{}")

	peID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkPrivateEndpoint,
		"/subscriptions/sub-pe/resourceGroups/RG/providers/Microsoft.Network/privateEndpoints/pe2", "eastus",
		`{"properties":{"privateLinkServiceConnections":[{"properties":{"privateLinkServiceId":"`+storageNativeID+`/blobServices/default"}}]}}`)

	if err := resolvePrivateEndpointRelationships(sub, st); err != nil {
		t.Fatalf("resolvePrivateEndpointRelationships (subresource): %v", err)
	}
	rels, _ := st.RelationshipsFrom(peID)
	if len(rels) != 1 || rels[0].ToID != storageID {
		t.Errorf("expected pe -[attached-to]-> storage account (parent), got %+v", rels)
	}
}

// TestResolvePrivateEndpointRelationships_UnknownTarget verifies a PE whose
// target resource is not in the local store produces no edge for it (but
// still emits the VNet edge if the subnet's parent VNet is known).
func TestResolvePrivateEndpointRelationships_UnknownTarget(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-pe")

	peID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkPrivateEndpoint,
		"/subscriptions/sub-pe/resourceGroups/RG/providers/Microsoft.Network/privateEndpoints/orphan", "eastus",
		`{"properties":{"privateLinkServiceConnections":[{"properties":{"privateLinkServiceId":"/subscriptions/sub-pe/resourceGroups/Other/providers/Microsoft.Storage/storageAccounts/missing"}}]}}`)

	if err := resolvePrivateEndpointRelationships(sub, st); err != nil {
		t.Fatalf("resolvePrivateEndpointRelationships (orphan): %v", err)
	}
	rels, _ := st.RelationshipsFrom(peID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
