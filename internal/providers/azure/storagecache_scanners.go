package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storagecache/armstoragecache"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.storagecache",
		fn:   scanStorageCache,
		emits: []coverage.TypeDecl{
			// resolveStorageCacheRelationships wires the subnet (VNet) and CMK (Key Vault) edges below.
			{Service: "microsoft.storagecache", DiscoType: TypeStorageCacheCache},
		},
	})
}

// scanStorageCache discovers Azure HPC Cache resources.
func scanStorageCache(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armstoragecache.NewCachesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armstoragecache:NewCachesClient: %w", err)
	}
	return azSimpleScan(ctx, "armstoragecache:Caches.List", TypeStorageCacheCache, sub, st, scanID,
		client.NewListPager(nil),
		func(p armstoragecache.CachesClientListResponse) []*armstoragecache.Cache { return p.Value },
		func(c *armstoragecache.Cache) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
