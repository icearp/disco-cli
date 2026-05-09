package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveMySQLRelationships verifies a MySQL flexible server derives
// both a VNet attached-to edge (delegatedSubnetResourceId) and a Key Vault
// uses edge (dataEncryption.primaryKeyUri) when both are configured.
func TestResolveMySQLRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-mysql")

	vnetNativeID := "/subscriptions/sub-mysql/resourceGroups/Net/providers/Microsoft.Network/virtualNetworks/vnet"
	subnetNativeID := vnetNativeID + "/subnets/mysql"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")

	vaultNativeID := "/subscriptions/sub-mysql/resourceGroups/RG/providers/Microsoft.KeyVault/vaults/myvault"
	vaultID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, vaultNativeID, "eastus", "{}")

	srvAttrs := `{"properties":{"network":{"delegatedSubnetResourceId":"` + subnetNativeID + `"},"dataEncryption":{"type":"AzureKeyVault","primaryKeyUri":"https://myvault.vault.azure.net/keys/mysql-cmk/v1"}}}`
	srvID := upsertTestResource(t, st, "azure", sub.ID, TypeMySQLFlexibleServer,
		"/subscriptions/sub-mysql/resourceGroups/RG/providers/Microsoft.DBforMySQL/flexibleServers/mysql1", "eastus", srvAttrs)

	if err := resolveMySQLRelationships(sub, st); err != nil {
		t.Fatalf("resolveMySQLRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(srvID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d (%+v)", len(rels), rels)
	}
	gotKinds := map[string]string{}
	for _, r := range rels {
		gotKinds[r.Kind] = r.ToID
	}
	if gotKinds[store.RelAttachedTo] != vnetID {
		t.Errorf("attached-to edge: got %q, want %q", gotKinds[store.RelAttachedTo], vnetID)
	}
	if gotKinds[store.RelUses] != vaultID {
		t.Errorf("uses edge: got %q, want %q", gotKinds[store.RelUses], vaultID)
	}
}
