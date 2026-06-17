package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appplatform/armappplatform"
)

// TestResolveAppPlatformRelationships verifies a VNet-injected Spring Apps
// service derives a single -[attached-to]-> VNet edge even when both the
// runtime and app subnets point at the same VNet (dedup), matched
// case-insensitively.
func TestResolveAppPlatformRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	vnetNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/SpringVNet"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetNativeID, "eastus", "{}")
	base := "/subscriptions/" + testSubID + "/resourcegroups/rg/providers/microsoft.network/virtualnetworks/springvnet/subnets/"

	svc := armappplatform.ServiceResource{
		Properties: &armappplatform.ClusterResourceProperties{
			NetworkProfile: &armappplatform.NetworkProfile{
				ServiceRuntimeSubnetID: to.Ptr(base + "runtime"),
				AppSubnetID:            to.Ptr(base + "apps"),
			},
		},
	}
	svcID := upsertTestResource(t, st, "azure", sub.ID, TypeAppPlatformService,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.AppPlatform/Spring/asa", "eastus", marshalAttrs(t, svc))

	if err := resolveAppPlatformRelationships(sub, st); err != nil {
		t.Fatalf("resolveAppPlatformRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(svcID)
	if len(rels) != 1 || rels[0].ToID != vnetID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected single service -[attached-to]-> vnet, got %+v", rels)
	}
}

// TestResolveAppPlatformRelationships_NoVNet verifies a service without a
// network profile produces no edge and does not panic.
func TestResolveAppPlatformRelationships_NoVNet(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	svcID := upsertTestResource(t, st, "azure", sub.ID, TypeAppPlatformService,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.AppPlatform/Spring/asa", "eastus", "{}")
	if err := resolveAppPlatformRelationships(sub, st); err != nil {
		t.Fatalf("resolveAppPlatformRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(svcID); len(rels) != 0 {
		t.Errorf("expected no edges, got %+v", rels)
	}
}
