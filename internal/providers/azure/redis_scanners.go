package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRedisCache, Service: "microsoft.cache"})
	registerService(serviceEntry{
		name: "azure:microsoft.cache",
		fn:   scanCacheNamespace,
	})
}

// scanRedis discovers Azure Cache for Redis instances. Firewall rules, patch
// schedules, linked servers, private endpoint connections, and access keys
// deferred — sub-resources with narrow cross-service edge value.
func scanRedis(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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

// scanCacheNamespace runs every Microsoft.cache scanner phase concurrently. The
// cache ARM namespace spans several disco scanners merged under one
// serviceEntry so the service name matches the namespace.
func scanCacheNamespace(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanRedis(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanRedisEnterprise(ctx, sub, cred, st, scanID) },
	)
}
