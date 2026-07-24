package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
)

func ecTags(ctx context.Context, client elasticacheAPI, arn string) *string {
	out, _ := client.ListTagsForResource(ctx, &elasticache.ListTagsForResourceInput{ResourceName: &arn})
	if out == nil {
		return nil
	}
	return awsTagsJSON(out.TagList)
}

// scanElastiCacheReservedNodes discovers purchased reserved cache nodes (a
// billing reservation, Leaf).
func scanElastiCacheReservedNodes(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticache.NewDescribeReservedCacheNodesPaginator(client, &elasticache.DescribeReservedCacheNodesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "elasticache:DescribeReservedCacheNodes", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("elasticache:DescribeReservedCacheNodes: %w", perr)
		}
		for _, rn := range page.ReservedCacheNodes {
			arn := sv(rn.ReservationARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeElastiCacheReservedInstance, NativeID: arn,
				Name: rn.ReservedCacheNodeId, Region: &region, Status: rn.State,
				AttributesJSON: mustJSON(rn), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "elasticache reserved-cache-nodes")
}

func scanElastiCacheServerlessSnapshots(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticache.NewDescribeServerlessCacheSnapshotsPaginator(client, &elasticache.DescribeServerlessCacheSnapshotsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "elasticache:DescribeServerlessCacheSnapshots", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("elasticache:DescribeServerlessCacheSnapshots: %w", perr)
		}
		for _, s := range page.ServerlessCacheSnapshots {
			arn := sv(s.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeElastiCacheServerlessCacheSnapshot, NativeID: arn,
				Name: s.ServerlessCacheSnapshotName, Region: &region, Status: s.Status,
				TagsJSON: ecTags(ctx, client, arn), AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "elasticache serverless-cache-snapshots")
}

func scanElastiCacheSnapshots(ctx context.Context, client elasticacheAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := elasticache.NewDescribeSnapshotsPaginator(client, &elasticache.DescribeSnapshotsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "elasticache:DescribeSnapshots", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("elasticache:DescribeSnapshots: %w", perr)
		}
		for _, s := range page.Snapshots {
			arn := sv(s.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeElastiCacheSnapshot, NativeID: arn,
				Name: s.SnapshotName, Region: &region, Status: s.SnapshotStatus,
				TagsJSON: ecTags(ctx, client, arn), AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "elasticache snapshots")
}
