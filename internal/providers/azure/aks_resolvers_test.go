package azure

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// TestResolveAKSRelationships verifies that an AKS cluster's VNet is derived
// from the agentPoolProfiles[].vnetSubnetID JSON path (lowercase keys — Azure SDK).
// This test catches case-sensitivity bugs: the JSON keys are lowercase in Azure
// SDK responses, which differs from AWS SDK convention.
func TestResolveAKSRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	vnetNativeID := "/subscriptions/sub-123/resourceGroups/NetRG/providers/Microsoft.Network/virtualNetworks/my-vnet"
	subnetNativeID := vnetNativeID + "/subnets/my-subnet"

	attrsJSON := `{
		"properties": {
			"agentPoolProfiles": [
				{"vnetSubnetID": "` + subnetNativeID + `"}
			]
		}
	}`
	clusterNativeID := "/subscriptions/sub-123/resourceGroups/NetRG/providers/Microsoft.ContainerService/managedClusters/my-cluster"
	clusterID := upsertTestResource(t, st, "azure", sub.ID, TypeAKSManagedCluster, clusterNativeID, "", attrsJSON)
	vnetResourceID := upsertTestResource(t, st, "azure", sub.ID, TypeVirtualNetwork, vnetNativeID, "", "{}")

	if err := resolveAKSRelationships(sub, st); err != nil {
		t.Fatalf("resolveAKSRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vnetResourceID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected cluster -[attached-to]-> vnet, got %+v", rels[0])
	}
}

// TestResolveAKSRelationships_MultiplePoolsSameVNet verifies that duplicate
// VNet relationships are deduplicated (multiple agent pools on the same VNet
// should produce exactly one edge).
func TestResolveAKSRelationships_MultiplePoolsSameVNet(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	vnetNativeID := "/subscriptions/sub-123/resourceGroups/NetRG/providers/Microsoft.Network/virtualNetworks/shared-vnet"
	subnet1 := vnetNativeID + "/subnets/pool1-subnet"
	subnet2 := vnetNativeID + "/subnets/pool2-subnet"

	attrsJSON := `{
		"properties": {
			"agentPoolProfiles": [
				{"vnetSubnetID": "` + subnet1 + `"},
				{"vnetSubnetID": "` + subnet2 + `"}
			]
		}
	}`
	clusterNativeID := "/subscriptions/sub-123/resourceGroups/NetRG/providers/Microsoft.ContainerService/managedClusters/multi-pool"
	clusterID := upsertTestResource(t, st, "azure", sub.ID, TypeAKSManagedCluster, clusterNativeID, "", attrsJSON)
	upsertTestResource(t, st, "azure", sub.ID, TypeVirtualNetwork, vnetNativeID, "", "{}")

	if err := resolveAKSRelationships(sub, st); err != nil {
		t.Fatalf("resolveAKSRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	// Both pools share the same VNet; expect exactly one relationship.
	if len(rels) != 1 {
		t.Errorf("expected 1 deduplicated vnet relationship, got %d", len(rels))
	}
}

// TestResolveAKSRelationships_EmptyAttrs verifies that a cluster with no
// agentPoolProfiles produces no relationships and no error.
func TestResolveAKSRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	clusterNativeID := "/subscriptions/sub-123/resourceGroups/RG/providers/Microsoft.ContainerService/managedClusters/bare"
	clusterID := upsertTestResource(t, st, "azure", sub.ID, TypeAKSManagedCluster, clusterNativeID, "", "{}")

	if err := resolveAKSRelationships(sub, st); err != nil {
		t.Fatalf("resolveAKSRelationships (empty): %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
