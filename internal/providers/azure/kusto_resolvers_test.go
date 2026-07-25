package azure

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/kusto/armkusto"
	"github.com/icearp/disco-cli/store"
)

// TestResolveKustoRelationships verifies a cluster derives both a -[uses]->
// Key Vault edge (CMK keyVaultUri) and an -[attached-to]-> VNet edge
// (virtualNetworkConfiguration.subnetId), each matched case-insensitively.
func TestResolveKustoRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	kvNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/KustoKv"
	kvID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, kvNativeID, "eastus", "{}")
	vnetPrefix := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetPrefix+"kustovnet", "eastus", "{}")
	// Mixed-case subnet ref vs lowercase stored VNet makes probe-side ToLower
	// in upsertVNetAttachment load-bearing (vnetIDFromSubnetID preserves
	// input case).
	subnetRef := vnetPrefix + "KustoVNet/subnets/s"

	cluster := armkusto.Cluster{
		Properties: &armkusto.ClusterProperties{
			KeyVaultProperties:          &armkusto.KeyVaultProperties{KeyVaultURI: to.Ptr("https://kustokv.vault.azure.net/")},
			VirtualNetworkConfiguration: &armkusto.VirtualNetworkConfiguration{SubnetID: to.Ptr(subnetRef)},
		},
	}
	cNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Kusto/clusters/adx"
	cID := upsertTestResource(t, st, "azure", sub.ID, TypeKustoCluster, cNativeID, "eastus", marshalAttrs(t, cluster))

	if err := resolveKustoRelationships(sub, st); err != nil {
		t.Fatalf("resolveKustoRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	var sawKV, sawVNet bool
	for _, r := range rels {
		if r.ToID == kvID && r.Kind == store.RelUses {
			sawKV = true
		}
		if r.ToID == vnetID && r.Kind == store.RelAttachedTo {
			sawVNet = true
		}
	}
	if !sawKV || !sawVNet || len(rels) != 2 {
		t.Errorf("expected cluster -[uses]-> kv and -[attached-to]-> vnet, got %+v", rels)
	}
}

// TestResolveKustoRelationships_NoRefs verifies a cluster with no CMK / VNet
// config produces no edges and does not panic on missing JSON.
func TestResolveKustoRelationships_NoRefs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	cID := upsertTestResource(t, st, "azure", sub.ID, TypeKustoCluster,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.Kusto/clusters/adx", "eastus", "{}")
	if err := resolveKustoRelationships(sub, st); err != nil {
		t.Fatalf("resolveKustoRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(cID); len(rels) != 0 {
		t.Errorf("expected no edges, got %+v", rels)
	}
}
