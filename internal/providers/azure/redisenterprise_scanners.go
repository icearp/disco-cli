package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redisenterprise/armredisenterprise"
)

func init() {
	registerExtraEmits([]coverage.TypeDecl{
		{Service: "microsoft.cache", DiscoType: TypeRedisEnterpriseCluster, Leaf: true},
	}...)
}

// scanRedisEnterprise discovers Azure Cache for Redis Enterprise clusters.
func scanRedisEnterprise(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
