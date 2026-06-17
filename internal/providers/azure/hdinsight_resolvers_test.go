package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hdinsight/armhdinsight"
)

// TestResolveHDInsightRelationships verifies a VNet-injected cluster derives a
// single -[attached-to]-> VNet edge across multiple roles in the same VNet
// (dedup), matched case-insensitively.
func TestResolveHDInsightRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	vnetNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/HdiVNet"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")
	base := "/subscriptions/" + testSubID + "/resourcegroups/rg/providers/microsoft.network/virtualnetworks/hdivnet/subnets/"

	cluster := armhdinsight.Cluster{
		Properties: &armhdinsight.ClusterGetProperties{
			ComputeProfile: &armhdinsight.ComputeProfile{
				Roles: []*armhdinsight.Role{
					{VirtualNetworkProfile: &armhdinsight.VirtualNetworkProfile{Subnet: to.Ptr(base + "head")}},
					{VirtualNetworkProfile: &armhdinsight.VirtualNetworkProfile{Subnet: to.Ptr(base + "worker")}},
				},
			},
		},
	}
	cID := upsertTestResource(t, st, "azure", sub.ID, TypeHDInsightCluster,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.HDInsight/clusters/hdi", "eastus", marshalAttrs(t, cluster))

	if err := resolveHDInsightRelationships(sub, st); err != nil {
		t.Fatalf("resolveHDInsightRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	if len(rels) != 1 || rels[0].ToID != vnetID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected single cluster -[attached-to]-> vnet, got %+v", rels)
	}
}

// TestResolveHDInsightRelationships_NoVNet verifies a cluster with no compute
// profile produces no edge and does not panic.
func TestResolveHDInsightRelationships_NoVNet(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	cID := upsertTestResource(t, st, "azure", sub.ID, TypeHDInsightCluster,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.HDInsight/clusters/hdi", "eastus", "{}")
	if err := resolveHDInsightRelationships(sub, st); err != nil {
		t.Fatalf("resolveHDInsightRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(cID); len(rels) != 0 {
		t.Errorf("expected no edges, got %+v", rels)
	}
}
