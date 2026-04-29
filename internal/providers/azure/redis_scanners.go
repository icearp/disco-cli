package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis"
)

func init() {
	registerService(serviceEntry{
		name: "azure:redis",
		fn:   scanRedis,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.cache", DiscoType: TypeRedisCache},
		},
	})
}

// scanRedis discovers Azure Cache for Redis instances. Firewall rules, patch
// schedules, linked servers, private endpoint connections, and access keys
// deferred — sub-resources with narrow cross-service edge value.
func scanRedis(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armredis.NewClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armredis:NewClient: %w", err)
	}
	return azSimpleScan(ctx, "armredis:Caches.ListBySubscription", TypeRedisCache, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armredis.ClientListBySubscriptionResponse) []*armredis.ResourceInfo { return p.Value },
		func(r *armredis.ResourceInfo) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
