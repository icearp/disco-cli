package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2LocalGateway, Leaf: true},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2CoipPool},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2OutpostLag},
	)
}

// scanEC2LocalGatewayExtra discovers Outpost local gateways, customer-owned IP
// (CoIP) pools, and Outpost link-aggregation groups.
func scanEC2LocalGatewayExtra(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanLocalGateways(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCoipPools(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanOutpostLags(ctx, client, acct, region, st, scanID)
		},
	)
}

// scanLocalGateways — the local gateway itself is Leaf: its only outbound ref is
// OutpostArn, and disco does not scan the Outposts service.
func scanLocalGateways(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeLocalGateways", acct, region, st,
		ec2.NewDescribeLocalGatewaysPaginator(client, &ec2.DescribeLocalGatewaysInput{}),
		func(page *ec2.DescribeLocalGatewaysOutput) []*store.Resource {
			var out []*store.Resource
			for _, lg := range page.LocalGateways {
				id := sv(lg.LocalGatewayId)
				if id == "" {
					continue
				}
				status := sv(lg.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2LocalGateway,
					NativeID:       ec2ARN(region, acct.ID, "local-gateway", id),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(lg.Tags),
					AttributesJSON: mustJSON(lg),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanCoipPools(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeCoipPools", acct, region, st,
		ec2.NewDescribeCoipPoolsPaginator(client, &ec2.DescribeCoipPoolsInput{}),
		func(page *ec2.DescribeCoipPoolsOutput) []*store.Resource {
			var out []*store.Resource
			for _, p := range page.CoipPools {
				arn := sv(p.PoolArn)
				if arn == "" {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2CoipPool,
					NativeID:       arn,
					Region:         &region,
					TagsJSON:       awsTagsJSON(p.Tags),
					AttributesJSON: mustJSON(p),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanOutpostLags — DescribeOutpostLags has no SDK paginator. Manual loop.
func scanOutpostLags(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.DescribeOutpostLags(ctx, &ec2.DescribeOutpostLagsInput{NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "ec2:DescribeOutpostLags", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("ec2:DescribeOutpostLags: %w", perr)
		}
		for _, ol := range out.OutpostLags {
			id := sv(ol.OutpostLagId)
			if id == "" {
				continue
			}
			status := sv(ol.State)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2OutpostLag,
				NativeID:       ec2ARN(region, acct.ID, "outpost-lag", id),
				Region:         &region,
				Status:         &status,
				TagsJSON:       awsTagsJSON(ol.Tags),
				AttributesJSON: mustJSON(ol),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ec2 outpost-lags: %w", uerr)
	}
	return len(batch), n, nil
}
