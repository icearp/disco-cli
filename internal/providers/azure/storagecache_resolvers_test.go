package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storagecache/armstoragecache"
)

// TestResolveStorageCacheRelationships verifies an HPC Cache derives an
// -[attached-to]-> VNet edge (properties.subnet) and a -[uses]-> Key Vault
// edge (CMK encryptionSettings.keyEncryptionKey.sourceVault.id), each matched
// case-insensitively.
func TestResolveStorageCacheRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	kvNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/CacheKv"
	kvID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault, kvNativeID, "eastus", "{}")
	vnetPrefix := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/"
	vnetID := upsertTestResource(t, st, "azure", sub.ID, TypeNetworkVirtualNetwork, vnetPrefix+"cachevnet", "eastus", "{}")
	// Mixed-case refs vs lowercase stored resources make probe-side ToLower load-bearing.
	subnetRef := vnetPrefix + "CacheVNet/subnets/s"

	cache := armstoragecache.Cache{
		Properties: &armstoragecache.CacheProperties{
			Subnet: to.Ptr(subnetRef),
			EncryptionSettings: &armstoragecache.CacheEncryptionSettings{
				KeyEncryptionKey: &armstoragecache.KeyVaultKeyReference{
					SourceVault: &armstoragecache.KeyVaultKeyReferenceSourceVault{
						ID: to.Ptr("/subscriptions/" + testSubID + "/resourceGroups/RG/providers/Microsoft.KeyVault/vaults/CacheKv"),
					},
				},
			},
		},
	}
	cNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.StorageCache/caches/hpc"
	cID := upsertTestResource(t, st, "azure", sub.ID, TypeStorageCacheCache, cNativeID, "eastus", marshalAttrs(t, cache))

	if err := resolveStorageCacheRelationships(sub, st); err != nil {
		t.Fatalf("resolveStorageCacheRelationships: %v", err)
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
		t.Errorf("expected cache -[uses]-> kv and -[attached-to]-> vnet, got %+v", rels)
	}
}

// TestResolveStorageCacheRelationships_NoRefs verifies a cache with no
// subnet/CMK config produces no edges and doesn't panic on missing JSON.
func TestResolveStorageCacheRelationships_NoRefs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	cID := upsertTestResource(t, st, "azure", sub.ID, TypeStorageCacheCache,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.StorageCache/caches/hpc", "eastus", "{}")
	if err := resolveStorageCacheRelationships(sub, st); err != nil {
		t.Fatalf("resolveStorageCacheRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(cID); len(rels) != 0 {
		t.Errorf("expected no edges, got %+v", rels)
	}
}
