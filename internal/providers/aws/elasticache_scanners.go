package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	smithy "github.com/aws/smithy-go"
)

func init() {
	registerService(serviceEntry{
		name: "aws:elasticache",
		fn:   scanElastiCache,
		emits: []coverage.TypeDecl{
			{Service: "elasticache", DiscoType: TypeElastiCacheCacheCluster},
			{Service: "elasticache", DiscoType: TypeElastiCacheReplicationGroup},
			{Service: "elasticache", DiscoType: TypeElastiCacheGlobalReplicationGroup},
			{Service: "elasticache", DiscoType: TypeElastiCacheServerlessCache},
			{Service: "elasticache", DiscoType: TypeElastiCacheParameterGroup, Leaf: true},
			{Service: "elasticache", DiscoType: TypeElastiCacheSecurityGroup, Leaf: true},
			{Service: "elasticache", DiscoType: TypeElastiCacheSubnetGroup},
			{Service: "elasticache", DiscoType: TypeElastiCacheUser, Leaf: true},
			{Service: "elasticache", DiscoType: TypeElastiCacheUserGroup},
		},
	})
}

// elasticacheAPI is the narrow set of ElastiCache operations called by the
// scanElastiCache sub-phases.
type elasticacheAPI interface {
	DescribeCacheClusters(context.Context, *elasticache.DescribeCacheClustersInput, ...func(*elasticache.Options)) (*elasticache.DescribeCacheClustersOutput, error)
	DescribeReplicationGroups(context.Context, *elasticache.DescribeReplicationGroupsInput, ...func(*elasticache.Options)) (*elasticache.DescribeReplicationGroupsOutput, error)
	DescribeGlobalReplicationGroups(context.Context, *elasticache.DescribeGlobalReplicationGroupsInput, ...func(*elasticache.Options)) (*elasticache.DescribeGlobalReplicationGroupsOutput, error)
	DescribeCacheParameterGroups(context.Context, *elasticache.DescribeCacheParameterGroupsInput, ...func(*elasticache.Options)) (*elasticache.DescribeCacheParameterGroupsOutput, error)
	DescribeCacheSecurityGroups(context.Context, *elasticache.DescribeCacheSecurityGroupsInput, ...func(*elasticache.Options)) (*elasticache.DescribeCacheSecurityGroupsOutput, error)
	DescribeServerlessCaches(context.Context, *elasticache.DescribeServerlessCachesInput, ...func(*elasticache.Options)) (*elasticache.DescribeServerlessCachesOutput, error)
	DescribeCacheSubnetGroups(context.Context, *elasticache.DescribeCacheSubnetGroupsInput, ...func(*elasticache.Options)) (*elasticache.DescribeCacheSubnetGroupsOutput, error)
	DescribeUsers(context.Context, *elasticache.DescribeUsersInput, ...func(*elasticache.Options)) (*elasticache.DescribeUsersOutput, error)
	DescribeUserGroups(context.Context, *elasticache.DescribeUserGroupsInput, ...func(*elasticache.Options)) (*elasticache.DescribeUserGroupsOutput, error)
	ListTagsForResource(context.Context, *elasticache.ListTagsForResourceInput, ...func(*elasticache.Options)) (*elasticache.ListTagsForResourceOutput, error)
}

// scanElastiCache discovers all ElastiCache resource types in the given region.
func scanElastiCache(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := elasticache.NewFromConfig(acct.cfg, func(o *elasticache.Options) { o.Region = region })
	for _, scan := range []func(context.Context, elasticacheAPI, *account, string, *store.Store, string) (int, int, error){
		scanElastiCacheReplicationGroups,
		scanElastiCacheClusters,
		scanElastiCacheGlobalReplicationGroups,
		scanElastiCacheParameterGroups,
		scanElastiCacheSecurityGroups,
		scanElastiCacheServerlessCaches,
		scanElastiCacheSubnetGroups,
		scanElastiCacheUsers,
		scanElastiCacheUserGroups,
	} {
		tt, nn, err := scan(ctx, client, acct, region, st, scanID)
		if err != nil {
			return total, inserted, err
		}
		total += tt
		inserted += nn
	}
	return
}

// scanElastiCacheReplicationGroups pages through DescribeReplicationGroups and
// upserts each group. Tags are included via ListTagsForResource.
func scanElastiCacheReplicationGroups(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticache.NewDescribeReplicationGroupsPaginator(client, &elasticache.DescribeReplicationGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "elasticache:DescribeReplicationGroups", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("elasticache:DescribeReplicationGroups: %w", err)
		}
		var batch []*store.Resource
		for _, rg := range page.ReplicationGroups {
			arn := sv(rg.ARN)
			tagsOut, _ := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{ResourceName: &arn})
			var tagsJSON *string
			if tagsOut != nil {
				tagsJSON = awsTagsJSON(tagsOut.TagList)
			}
			batch = append(batch, &store.Resource{
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
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert ElastiCache replication groups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanElastiCacheClusters pages through DescribeCacheClusters. This covers
// Memcached clusters and the individual shard clusters within Redis replication groups.
func scanElastiCacheClusters(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticache.NewDescribeCacheClustersPaginator(client, &elasticache.DescribeCacheClustersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "elasticache:DescribeCacheClusters", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("elasticache:DescribeCacheClusters: %w", err)
		}
		var batch []*store.Resource
		for _, cc := range page.CacheClusters {
			arn := sv(cc.ARN)
			tagsOut, _ := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{ResourceName: &arn})
			var tagsJSON *string
			if tagsOut != nil {
				tagsJSON = awsTagsJSON(tagsOut.TagList)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeElastiCacheCacheCluster,
				NativeID:       arn,
				Name:           cc.CacheClusterId,
				Region:         &region,
				Zone:           cc.PreferredAvailabilityZone,
				CreatedAt:      tp(cc.CacheClusterCreateTime),
				Status:         cc.CacheClusterStatus,
				AttributesJSON: mustJSON(cc),
				TagsJSON:       tagsJSON,
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert ElastiCache clusters: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanElastiCacheGlobalReplicationGroups pages through DescribeGlobalReplicationGroups.
// These are global resources (not region-scoped); UpsertResources deduplicates by
// stable NativeID-derived primary key, so calling per-region is safe.
func scanElastiCacheGlobalReplicationGroups(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	showMembers := true
	pager := elasticache.NewDescribeGlobalReplicationGroupsPaginator(client, &elasticache.DescribeGlobalReplicationGroupsInput{
		ShowMemberInfo: &showMembers,
	})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "elasticache:DescribeGlobalReplicationGroups", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("elasticache:DescribeGlobalReplicationGroups: %w", err)
		}
		var batch []*store.Resource
		for _, grg := range page.GlobalReplicationGroups {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeElastiCacheGlobalReplicationGroup,
				NativeID:       sv(grg.ARN),
				Name:           grg.GlobalReplicationGroupId,
				Region:         &region,
				Status:         grg.Status,
				AttributesJSON: mustJSON(grg),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert ElastiCache global replication groups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanElastiCacheParameterGroups pages through DescribeCacheParameterGroups.
func scanElastiCacheParameterGroups(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticache.NewDescribeCacheParameterGroupsPaginator(client, &elasticache.DescribeCacheParameterGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "elasticache:DescribeCacheParameterGroups", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("elasticache:DescribeCacheParameterGroups: %w", err)
		}
		var batch []*store.Resource
		for _, pg := range page.CacheParameterGroups {
			arn := sv(pg.ARN)
			tagsOut, _ := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{ResourceName: &arn})
			var tagsJSON *string
			if tagsOut != nil {
				tagsJSON = awsTagsJSON(tagsOut.TagList)
			}
			batch = append(batch, &store.Resource{
				Provider:          "aws",
				AccountID:         acct.ID,
				AccountName:       &acct.Name,
				Type:              TypeElastiCacheParameterGroup,
				NativeID:          arn,
				Name:              pg.CacheParameterGroupName,
				Region:            &region,
				AttributesJSON:    mustJSON(pg),
				TagsJSON:          tagsJSON,
				DiscoveredBy:      scanID,
				ManagedByProvider: isDefaultElastiCachePG(sv(pg.CacheParameterGroupName)),
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert ElastiCache parameter groups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// isCacheSecurityGroupsNotPermitted reports whether err is the ElastiCache
// error for VPC-only accounts that have never used EC2-Classic.
func isCacheSecurityGroupsNotPermitted(err error) bool {
	var ae smithy.APIError
	return errors.As(err, &ae) && ae.ErrorCode() == "InvalidParameterValue" &&
		ae.ErrorMessage() == "Use of cache security groups is not permitted in this API version for your account."
}

// scanElastiCacheSecurityGroups pages through DescribeCacheSecurityGroups.
// These are legacy EC2-Classic security groups; VPC-only accounts return
// InvalidParameterValue, which is treated as an empty result.
func scanElastiCacheSecurityGroups(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticache.NewDescribeCacheSecurityGroupsPaginator(client, &elasticache.DescribeCacheSecurityGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isCacheSecurityGroupsNotPermitted(err) {
				return 0, 0, nil // VPC-only account — no EC2-Classic cache security groups
			}
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "elasticache:DescribeCacheSecurityGroups", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("elasticache:DescribeCacheSecurityGroups: %w", err)
		}
		var batch []*store.Resource
		for _, sg := range page.CacheSecurityGroups {
			arn := sv(sg.ARN)
			tagsOut, _ := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{ResourceName: &arn})
			var tagsJSON *string
			if tagsOut != nil {
				tagsJSON = awsTagsJSON(tagsOut.TagList)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeElastiCacheSecurityGroup,
				NativeID:       arn,
				Name:           sg.CacheSecurityGroupName,
				Region:         &region,
				AttributesJSON: mustJSON(sg),
				TagsJSON:       tagsJSON,
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert ElastiCache security groups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanElastiCacheServerlessCaches pages through DescribeServerlessCaches.
func scanElastiCacheServerlessCaches(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticache.NewDescribeServerlessCachesPaginator(client, &elasticache.DescribeServerlessCachesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "elasticache:DescribeServerlessCaches", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("elasticache:DescribeServerlessCaches: %w", err)
		}
		var batch []*store.Resource
		for _, sc := range page.ServerlessCaches {
			arn := sv(sc.ARN)
			tagsOut, _ := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{ResourceName: &arn})
			var tagsJSON *string
			if tagsOut != nil {
				tagsJSON = awsTagsJSON(tagsOut.TagList)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeElastiCacheServerlessCache,
				NativeID:       arn,
				Name:           sc.ServerlessCacheName,
				Region:         &region,
				Status:         sc.Status,
				AttributesJSON: mustJSON(sc),
				TagsJSON:       tagsJSON,
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert ElastiCache serverless caches: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanElastiCacheSubnetGroups pages through DescribeCacheSubnetGroups.
func scanElastiCacheSubnetGroups(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticache.NewDescribeCacheSubnetGroupsPaginator(client, &elasticache.DescribeCacheSubnetGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "elasticache:DescribeCacheSubnetGroups", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("elasticache:DescribeCacheSubnetGroups: %w", err)
		}
		var batch []*store.Resource
		for _, sg := range page.CacheSubnetGroups {
			arn := sv(sg.ARN)
			tagsOut, _ := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{ResourceName: &arn})
			var tagsJSON *string
			if tagsOut != nil {
				tagsJSON = awsTagsJSON(tagsOut.TagList)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeElastiCacheSubnetGroup,
				NativeID:       arn,
				Name:           sg.CacheSubnetGroupName,
				Region:         &region,
				AttributesJSON: mustJSON(sg),
				TagsJSON:       tagsJSON,
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert ElastiCache subnet groups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanElastiCacheUsers pages through DescribeUsers.
func scanElastiCacheUsers(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticache.NewDescribeUsersPaginator(client, &elasticache.DescribeUsersInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "elasticache:DescribeUsers", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("elasticache:DescribeUsers: %w", err)
		}
		var batch []*store.Resource
		for _, u := range page.Users {
			arn := sv(u.ARN)
			tagsOut, _ := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{ResourceName: &arn})
			var tagsJSON *string
			if tagsOut != nil {
				tagsJSON = awsTagsJSON(tagsOut.TagList)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeElastiCacheUser,
				NativeID:       arn,
				Name:           u.UserId,
				Region:         &region,
				Status:         u.Status,
				AttributesJSON: mustJSON(u),
				TagsJSON:       tagsJSON,
				DiscoveredBy:   scanID,
				// UserId "default" is the AWS-managed default user present in
				// every account.
				ManagedByProvider: sv(u.UserId) == "default",
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert ElastiCache users: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanElastiCacheUserGroups pages through DescribeUserGroups.
func scanElastiCacheUserGroups(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticache.NewDescribeUserGroupsPaginator(client, &elasticache.DescribeUserGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "elasticache:DescribeUserGroups", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("elasticache:DescribeUserGroups: %w", err)
		}
		var batch []*store.Resource
		for _, ug := range page.UserGroups {
			arn := sv(ug.ARN)
			tagsOut, _ := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{ResourceName: &arn})
			var tagsJSON *string
			if tagsOut != nil {
				tagsJSON = awsTagsJSON(tagsOut.TagList)
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeElastiCacheUserGroup,
				NativeID:       arn,
				Name:           ug.UserGroupId,
				Region:         &region,
				Status:         ug.Status,
				AttributesJSON: mustJSON(ug),
				TagsJSON:       tagsJSON,
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert ElastiCache user groups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// isDefaultElastiCachePG reports whether a CacheParameterGroupName matches
// AWS's pre-created defaults — names like "default.redis6.x",
// "default.memcached1.6", "default.valkey7". AWS creates one per supported
// engine/version, immutable by the customer; treating them as managed hides
// them from `disco list` / `disco graph` defaults. Customer PGs may also
// start with "default" but never embed a "." (forbidden in user-supplied
// names alongside the "default" prefix), so the two-part check is reliable.
func isDefaultElastiCachePG(name string) bool {
	return strings.HasPrefix(name, "default") && strings.Contains(name, ".")
}
