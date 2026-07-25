package azure

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mongocluster/armmongocluster"
	"github.com/icearp/disco-cli/store"
)

// TestResolveMongoClusterRelationships verifies a mongo (vCore) cluster derives
// a -[uses]-> Key Vault edge from the CMK keyEncryptionKeyUrl.
func TestResolveMongoClusterRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	kvNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/mongokv"
	kvID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, kvNativeID, "eastus", "{}")

	c := armmongocluster.MongoCluster{
		Properties: &armmongocluster.Properties{
			Encryption: &armmongocluster.EncryptionProperties{
				CustomerManagedKeyEncryption: &armmongocluster.CustomerManagedKeyEncryptionProperties{
					KeyEncryptionKeyURL: to.Ptr("https://MongoKv.vault.azure.net/keys/k/v1"),
				},
			},
		},
	}
	cID := upsertTestResource(t, st, "azure", sub.ID, TypeMongoCluster,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.DocumentDB/mongoClusters/mc", "eastus", marshalAttrs(t, c))

	if err := resolveMongoClusterRelationships(sub, st); err != nil {
		t.Fatalf("resolveMongoClusterRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	if len(rels) != 1 || rels[0].ToID != kvID || rels[0].Kind != store.RelUses {
		t.Errorf("expected one -[uses]-> keyvault edge, got %+v", rels)
	}
}

// TestResolveMongoClusterRelationships_NoRefs verifies a cluster with no CMK
// produces no edges and does not panic.
func TestResolveMongoClusterRelationships_NoRefs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	cID := upsertTestResource(t, st, "azure", sub.ID, TypeMongoCluster,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.DocumentDB/mongoClusters/mc", "eastus", "{}")
	if err := resolveMongoClusterRelationships(sub, st); err != nil {
		t.Fatalf("resolveMongoClusterRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(cID); len(rels) != 0 {
		t.Errorf("expected no edges, got %+v", rels)
	}
}
