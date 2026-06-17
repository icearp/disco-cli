package azure

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datamigration/armdatamigration"
)

// TestResolveDataMigrationRelationships verifies a DMS instance derives an
// -[attached-to]-> VNet edge from properties.virtualSubnetId, matched
// case-insensitively.
func TestResolveDataMigrationRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	vnetPrefix := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetPrefix+"dmsvnet", "eastus", "{}")

	svc := armdatamigration.Service{
		Properties: &armdatamigration.ServiceProperties{
			VirtualSubnetID: to.Ptr(vnetPrefix + "DmsVNet/subnets/s"),
		},
	}
	sID := upsertTestResource(t, st, "azure", sub.ID, TypeDataMigrationService,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.DataMigration/services/dms", "eastus", marshalAttrs(t, svc))

	if err := resolveDataMigrationRelationships(sub, st); err != nil {
		t.Fatalf("resolveDataMigrationRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	if len(rels) != 1 || rels[0].ToID != vnetID {
		t.Errorf("expected one -[attached-to]-> vnet edge, got %+v", rels)
	}
}

// TestResolveDataMigrationRelationships_NoRefs verifies a service with no
// subnet produces no edges and does not panic.
func TestResolveDataMigrationRelationships_NoRefs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	sID := upsertTestResource(t, st, "azure", sub.ID, TypeDataMigrationService,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.DataMigration/services/dms", "eastus", "{}")
	if err := resolveDataMigrationRelationships(sub, st); err != nil {
		t.Fatalf("resolveDataMigrationRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(sID); len(rels) != 0 {
		t.Errorf("expected no edges, got %+v", rels)
	}
}
