package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveRedisRelationships verifies that a VNet-injected Redis cache
// derives an attached-to edge to the parent VNet via subnetId path stripping.
func TestResolveRedisRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-redis")

	vnetNativeID := "/subscriptions/sub-redis/resourceGroups/NetRG/providers/Microsoft.Network/virtualNetworks/my-vnet"
	subnetNativeID := vnetNativeID + "/subnets/redis-subnet"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")

	cacheNativeID := "/subscriptions/sub-redis/resourceGroups/RG/providers/Microsoft.Cache/Redis/my-cache"
	cacheAttrs := `{"properties":{"subnetId":"` + subnetNativeID + `"}}`
	cacheID := upsertTestResource(t, st, "azure", sub.ID, TypeRedisCache, cacheNativeID, "eastus", cacheAttrs)

	if err := resolveRedisRelationships(sub, st); err != nil {
		t.Fatalf("resolveRedisRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(cacheID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != vnetID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected redis -[attached-to]-> vnet, got %+v", rels)
	}
}

// TestResolveRedisRelationships_NoVNet verifies a Basic/Standard cache (no
// subnetId, public ingress) produces no edge.
func TestResolveRedisRelationships_NoVNet(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-redis")

	cacheID := upsertTestResource(t, st, "azure", sub.ID, TypeRedisCache,
		"/subscriptions/sub-redis/resourceGroups/RG/providers/Microsoft.Cache/Redis/public", "eastus", "{}")

	if err := resolveRedisRelationships(sub, st); err != nil {
		t.Fatalf("resolveRedisRelationships (no vnet): %v", err)
	}
	rels, err := st.RelationshipsFrom(cacheID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
