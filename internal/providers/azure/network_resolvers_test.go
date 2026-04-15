package azure

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// TestResolveSubnetVNetRelationships verifies that a subnet's VNet parent is
// correctly derived from the subnet NativeID path (no JSON parsing needed —
// the relationship is structural, not attribute-based).
func TestResolveSubnetVNetRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	vnetNativeID := "/subscriptions/sub-123/resourceGroups/NetRG/providers/Microsoft.Network/virtualNetworks/my-vnet"
	subnetNativeID := vnetNativeID + "/subnets/my-subnet"

	subnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkSubnet, subnetNativeID, "", "{}")
	vnetResourceID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "", "{}")

	if err := resolveSubnetVNetRelationships(sub, st); err != nil {
		t.Fatalf("resolveSubnetVNetRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(subnetID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vnetResourceID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected subnet -[attached-to]-> vnet, got %+v", rels[0])
	}
}

// TestResolveSubnetVNetRelationships_NoSubnets verifies no error when the
// subscription has no subnets.
func TestResolveSubnetVNetRelationships_NoSubnets(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	if err := resolveSubnetVNetRelationships(sub, st); err != nil {
		t.Fatalf("resolveSubnetVNetRelationships (empty): %v", err)
	}
}
