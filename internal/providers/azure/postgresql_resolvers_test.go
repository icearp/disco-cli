package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolvePostgreSQLRelationships verifies a VNet-injected PG flexible
// server derives an attached-to edge to the parent VNet via
// network.delegatedSubnetResourceId path stripping.
func TestResolvePostgreSQLRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-pg")

	vnetNativeID := "/subscriptions/sub-pg/resourceGroups/Net/providers/Microsoft.Network/virtualNetworks/vnet"
	subnetNativeID := vnetNativeID + "/subnets/pg"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")

	srvID := upsertTestResource(t, st, "azure", sub.ID, TypePostgreSQLFlexibleServer,
		"/subscriptions/sub-pg/resourceGroups/RG/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg1",
		"eastus",
		`{"properties":{"network":{"delegatedSubnetResourceId":"`+subnetNativeID+`"}}}`)

	if err := resolvePostgreSQLRelationships(sub, st); err != nil {
		t.Fatalf("resolvePostgreSQLRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(srvID)
	if len(rels) != 1 || rels[0].ToID != vnetID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected pg -[attached-to]-> vnet, got %+v", rels)
	}
}

// TestResolvePostgreSQLRelationships_PublicAccess verifies a public-access
// server (no delegatedSubnetResourceId) produces no edge.
func TestResolvePostgreSQLRelationships_PublicAccess(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-pg")

	srvID := upsertTestResource(t, st, "azure", sub.ID, TypePostgreSQLFlexibleServer,
		"/subscriptions/sub-pg/resourceGroups/RG/providers/Microsoft.DBforPostgreSQL/flexibleServers/public", "eastus", "{}")

	if err := resolvePostgreSQLRelationships(sub, st); err != nil {
		t.Fatalf("resolvePostgreSQLRelationships (public): %v", err)
	}
	rels, _ := st.RelationshipsFrom(srvID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
