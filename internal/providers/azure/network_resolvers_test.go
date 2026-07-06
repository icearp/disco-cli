package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveSubnetVNetRelationships verifies a subnet's VNet parent is
// derived from the subnet NativeID path (no JSON parsing needed — the
// relationship is structural, not attribute-based).
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

// TestResolveApplicationGatewayRelationships verifies an AGW emits three
// edges from a configured attrs blob: VNet (subnet path in gateway IP
// config), Public IP (frontend IP config), Key Vault (sslCertificates[].
// keyVaultSecretId reference URI — preserved by the sanitizer's
// reference-URI allowlist).
func TestResolveApplicationGatewayRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-agw")

	vnetNativeID := "/subscriptions/sub-agw/resourceGroups/Net/providers/Microsoft.Network/virtualNetworks/vnet"
	subnetNativeID := vnetNativeID + "/subnets/agw"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")

	pipNativeID := "/subscriptions/sub-agw/resourceGroups/Net/providers/Microsoft.Network/publicIPAddresses/pip1"
	pipID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkPublicIPAddress, pipNativeID, "eastus", "{}")

	vaultID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault,
		"/subscriptions/sub-agw/resourceGroups/RG/providers/Microsoft.KeyVault/vaults/myvault", "eastus", "{}")

	agwAttrs := `{"properties":{"gatewayIPConfigurations":[{"properties":{"subnet":{"id":"` + subnetNativeID + `"}}}],"frontendIPConfigurations":[{"properties":{"publicIPAddress":{"id":"` + pipNativeID + `"}}}],"sslCertificates":[{"properties":{"keyVaultSecretId":"https://myvault.vault.azure.net/secrets/cert1/abc123"}}]}}`
	agwID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkApplicationGateway,
		"/subscriptions/sub-agw/resourceGroups/RG/providers/Microsoft.Network/applicationGateways/agw", "eastus", agwAttrs)

	if err := resolveApplicationGatewayRelationships(sub, st); err != nil {
		t.Fatalf("resolveApplicationGatewayRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(agwID)
	if len(rels) != 3 {
		t.Fatalf("expected 3 relationships, got %d (%+v)", len(rels), rels)
	}
	got := map[string]bool{}
	for _, r := range rels {
		got[r.ToID] = true
	}
	if !got[vnetID] || !got[pipID] || !got[vaultID] {
		t.Errorf("expected edges to vnet (%s) / pip (%s) / vault (%s), got %+v", vnetID, pipID, vaultID, rels)
	}
}

// TestResolveApplicationGatewayRelationships_BareAttrs verifies a gateway
// with no frontend/cert config produces no edges and no error.
func TestResolveApplicationGatewayRelationships_BareAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-agw")

	agwID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkApplicationGateway,
		"/subscriptions/sub-agw/resourceGroups/RG/providers/Microsoft.Network/applicationGateways/bare", "eastus", "{}")

	if err := resolveApplicationGatewayRelationships(sub, st); err != nil {
		t.Fatalf("resolveApplicationGatewayRelationships (bare): %v", err)
	}
	rels, err := st.RelationshipsFrom(agwID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
