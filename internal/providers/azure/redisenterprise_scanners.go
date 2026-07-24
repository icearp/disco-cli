package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redisenterprise/armredisenterprise"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRedisEnterpriseCluster, Service: "microsoft.cache", Leaf: true})
}

// scanRedisEnterprise discovers Azure Cache for Redis Enterprise clusters.
func scanRedisEnterprise(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armredisenterprise.NewClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armredisenterprise:NewClient: %w", err)
	}
	return azSimpleScan(ctx, "armredisenterprise:Client.List", TypeRedisEnterpriseCluster, sub, st, scanID,
		client.NewListPager(nil),
		func(p armredisenterprise.ClientListResponse) []*armredisenterprise.Cluster { return p.Value },
		func(c *armredisenterprise.Cluster) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
