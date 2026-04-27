package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis"
)

func init() { registerService(serviceEntry{name: "azure:redis", fn: scanRedis}) }

// scanRedis discovers Azure Cache for Redis instances. Firewall rules, patch
// schedules, linked servers, private endpoint connections, and access keys
// deferred — sub-resources with narrow cross-service edge value.
func scanRedis(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armredis.NewClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armredis:NewClient: %w", err)
	}
	return azPageScan(ctx, "armredis:Caches.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armredis.ClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, r := range page.Value {
				if r == nil || r.ID == nil {
					continue
				}
				name, loc := sv(r.Name), sv(r.Location)
				nativeID := sv(r.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeRedisCache, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(r.Tags), AttributesJSON: mustJSON(r),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeRedisCache, nativeID))
				}
			}
			return batch, pairs
		})
}
