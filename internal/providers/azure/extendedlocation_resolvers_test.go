package azure

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurearcdata/armazurearcdata"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/extendedlocation/armextendedlocation"
)

// TestResolveExtendedLocationConsumers verifies any resource carrying the
// top-level extendedLocation envelope derives a -[uses]-> custom-location edge,
// matched case-insensitively.
func TestResolveExtendedLocationConsumers(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	clNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.ExtendedLocation/customLocations/cl"
	clID := upsertTestResource(t, st, "azure", sub.ID, TypeCustomLocation, clNativeID, "eastus", "{}")

	dc := armazurearcdata.DataControllerResource{
		ExtendedLocation: &armazurearcdata.ExtendedLocation{
			Name: to.Ptr("/subscriptions/" + testSubID + "/resourceGroups/RG/providers/Microsoft.ExtendedLocation/customLocations/CL"),
			Type: to.Ptr(armazurearcdata.ExtendedLocationTypesCustomLocation),
		},
	}
	dcID := upsertTestResource(t, st, "azure", sub.ID, TypeAzureArcDataController,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.AzureArcData/dataControllers/dc", "eastus", marshalAttrs(t, dc))

	if err := resolveExtendedLocationConsumers(sub, st); err != nil {
		t.Fatalf("resolveExtendedLocationConsumers: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dcID)
	if len(rels) != 1 || rels[0].ToID != clID || rels[0].Kind != store.RelUses {
		t.Errorf("expected one -[uses]-> custom-location edge, got %+v", rels)
	}
}

// TestResolveCustomLocationRelationships verifies a custom location wires to its
// backing connected-cluster host via properties.hostResourceId.
func TestResolveCustomLocationRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	ccNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Kubernetes/connectedClusters/cc"
	ccID := upsertTestResource(t, st, "azure", sub.ID, TypeKubernetesConnectedCluster, ccNativeID, "eastus", "{}")

	cl := armextendedlocation.CustomLocation{
		Properties: &armextendedlocation.CustomLocationProperties{
			HostResourceID: to.Ptr("/subscriptions/" + testSubID + "/resourceGroups/RG/providers/Microsoft.Kubernetes/connectedClusters/CC"),
		},
	}
	clID := upsertTestResource(t, st, "azure", sub.ID, TypeCustomLocation,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.ExtendedLocation/customLocations/cl", "eastus", marshalAttrs(t, cl))

	if err := resolveCustomLocationRelationships(sub, st); err != nil {
		t.Fatalf("resolveCustomLocationRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(clID)
	if len(rels) != 1 || rels[0].ToID != ccID || rels[0].Kind != store.RelUses {
		t.Errorf("expected one -[uses]-> connected-cluster edge, got %+v", rels)
	}
}

// TestResolveExtendedLocation_NoRefs verifies resources without the envelope /
// host ref produce no edges and do not panic.
func TestResolveExtendedLocation_NoRefs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	clID := upsertTestResource(t, st, "azure", sub.ID, TypeCustomLocation,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.ExtendedLocation/customLocations/cl", "eastus", "{}")
	dcID := upsertTestResource(t, st, "azure", sub.ID, TypeAzureArcDataController,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.AzureArcData/dataControllers/dc", "eastus", "{}")
	if err := resolveExtendedLocationConsumers(sub, st); err != nil {
		t.Fatalf("resolveExtendedLocationConsumers: %v", err)
	}
	if err := resolveCustomLocationRelationships(sub, st); err != nil {
		t.Fatalf("resolveCustomLocationRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(dcID); len(rels) != 0 {
		t.Errorf("expected no edges from dc, got %+v", rels)
	}
	if rels, _ := st.RelationshipsFrom(clID); len(rels) != 0 {
		t.Errorf("expected no edges from cl, got %+v", rels)
	}
}
