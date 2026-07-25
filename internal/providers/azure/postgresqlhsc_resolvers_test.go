package azure

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresqlhsc/armpostgresqlhsc"
	"github.com/icearp/disco-cli/store"
)

// TestResolvePostgreSQLHSCRelationships verifies a Citus cluster derives a
// -[uses]-> Key Vault edge from the CMK primaryKeyUri.
func TestResolvePostgreSQLHSCRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	kvNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/cituskv"
	kvID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, kvNativeID, "eastus", "{}")

	c := armpostgresqlhsc.Cluster{
		Properties: &armpostgresqlhsc.ClusterProperties{
			DataEncryption: &armpostgresqlhsc.DataEncryption{
				PrimaryKeyURI: to.Ptr("https://CitusKv.vault.azure.net/keys/k/v1"),
			},
		},
	}
	cID := upsertTestResource(t, st, "azure", sub.ID, TypePostgreSQLServerGroupV2,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/serverGroupsv2/c", "eastus", marshalAttrs(t, c))

	if err := resolvePostgreSQLHSCRelationships(sub, st); err != nil {
		t.Fatalf("resolvePostgreSQLHSCRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	if len(rels) != 1 || rels[0].ToID != kvID || rels[0].Kind != store.RelUses {
		t.Errorf("expected one -[uses]-> keyvault edge, got %+v", rels)
	}
}

// TestResolvePostgreSQLHSCRelationships_NoRefs verifies a cluster with no CMK
// produces no edges and does not panic.
func TestResolvePostgreSQLHSCRelationships_NoRefs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	cID := upsertTestResource(t, st, "azure", sub.ID, TypePostgreSQLServerGroupV2,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.DBforPostgreSQL/serverGroupsv2/c", "eastus", "{}")
	if err := resolvePostgreSQLHSCRelationships(sub, st); err != nil {
		t.Fatalf("resolvePostgreSQLHSCRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(cID); len(rels) != 0 {
		t.Errorf("expected no edges, got %+v", rels)
	}
}
