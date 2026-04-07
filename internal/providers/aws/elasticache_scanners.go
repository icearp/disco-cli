package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
)

func init() { registerService(serviceEntry{name: "aws:elasticache", fn: scanElastiCache}) }

// scanElastiCache discovers ElastiCache replication groups (Redis) and cache
// clusters (Memcached, and the individual node clusters within Redis groups).
func scanElastiCache(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := elasticache.NewFromConfig(acct.cfg, func(o *elasticache.Options) { o.Region = region })
	if err := scanElastiCacheReplicationGroups(ctx, client, acct, region, st, scanID); err != nil {
		return err
	}
	return scanElastiCacheClusters(ctx, client, acct, region, st, scanID)
}

// scanElastiCacheReplicationGroups pages through DescribeReplicationGroups and
// upserts each group. Tags are included in the describe response via the ARN.
func scanElastiCacheReplicationGroups(ctx context.Context, client *elasticache.Client, acct *account, region string, st *store.Store, scanID string) error {
	pager := elasticache.NewDescribeReplicationGroupsPaginator(client, &elasticache.DescribeReplicationGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("elasticache:DescribeReplicationGroups", acct.ID, region, err)
			}
			return fmt.Errorf("elasticache:DescribeReplicationGroups: %w", err)
		}
		var batch []*store.Resource
		for _, rg := range page.ReplicationGroups {
			// The ARN is available on the replication group directly.
			arn := sv(rg.ARN)
			// Fetch tags for this replication group.
			tagsOut, err := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{ResourceName: &arn})
			var tagsJSON *string
			if err == nil {
				tagsJSON = awsTagsJSON(tagsOut.TagList)
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeElastiCacheReplicationGroup,
				NativeID:       arn,
				Name:           rg.ReplicationGroupId,
				Region:         &region,
				Status:         rg.Status,
				AttributesJSON: mustJSON(rg),
				TagsJSON:       tagsJSON,
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert ElastiCache replication groups: %w", err)
			}
		}
	}
	return nil
}

// scanElastiCacheClusters pages through DescribeCacheClusters. This covers
// Memcached clusters and the individual shard clusters within Redis replication groups.
func scanElastiCacheClusters(ctx context.Context, client *elasticache.Client, acct *account, region string, st *store.Store, scanID string) error {
	pager := elasticache.NewDescribeCacheClustersPaginator(client, &elasticache.DescribeCacheClustersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("elasticache:DescribeCacheClusters", acct.ID, region, err)
			}
			return fmt.Errorf("elasticache:DescribeCacheClusters: %w", err)
		}
		var batch []*store.Resource
		for _, cc := range page.CacheClusters {
			arn := sv(cc.ARN)
			tagsOut, err := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{ResourceName: &arn})
			var tagsJSON *string
			if err == nil {
				tagsJSON = awsTagsJSON(tagsOut.TagList)
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeElastiCacheCluster,
				NativeID:       arn,
				Name:           cc.CacheClusterId,
				Region:         &region,
				Zone:           cc.PreferredAvailabilityZone,
				CreatedAt:      tp(cc.CacheClusterCreateTime),
				Status:         cc.CacheClusterStatus,
				AttributesJSON: mustJSON(cc),
				TagsJSON:       tagsJSON,
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert ElastiCache clusters: %w", err)
			}
		}
	}
	return nil
}
