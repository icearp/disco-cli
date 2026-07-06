package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveDNSRelationships verifies a private-DNS-zone vnet-link derives
// an attached-to edge to the linked VNet via properties.virtualNetwork.id,
// case-insensitive.
func TestResolveDNSRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-dns")

	vnetNativeID := "/subscriptions/sub-dns/resourceGroups/Net/providers/Microsoft.Network/virtualNetworks/v1"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")

	linkNativeID := "/subscriptions/sub-dns/resourceGroups/RG/providers/Microsoft.Network/privateDnsZones/contoso.local/virtualNetworkLinks/link1"
	linkAttrs := `{"properties":{"virtualNetwork":{"id":"/SUBSCRIPTIONS/SUB-DNS/RESOURCEGROUPS/NET/PROVIDERS/MICROSOFT.NETWORK/VIRTUALNETWORKS/V1"}}}`
	linkID := upsertTestResource(t, st, "azure", sub.ID, TypeDNSPrivateZoneVNetLink, linkNativeID, "global", linkAttrs)

	if err := resolveDNSRelationships(sub, st); err != nil {
		t.Fatalf("resolveDNSRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(linkID)
	if len(rels) != 1 || rels[0].ToID != vnetID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected vnetlink -[attached-to]-> vnet, got %+v", rels)
	}
}

// TestResolveDNSRelationships_UnknownVNet verifies a link pointing at a VNet
// not in the local store produces no edge.
func TestResolveDNSRelationships_UnknownVNet(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-dns")

	linkID := upsertTestResource(t, st, "azure", sub.ID, TypeDNSPrivateZoneVNetLink,
		"/subscriptions/sub-dns/resourceGroups/RG/providers/Microsoft.Network/privateDnsZones/c.local/virtualNetworkLinks/orphan",
		"global",
		`{"properties":{"virtualNetwork":{"id":"/subscriptions/other/resourceGroups/X/providers/Microsoft.Network/virtualNetworks/missing"}}}`)

	if err := resolveDNSRelationships(sub, st); err != nil {
		t.Fatalf("resolveDNSRelationships (orphan): %v", err)
	}
	rels, _ := st.RelationshipsFrom(linkID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
