package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2IPAMPrefixListResolver},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2IPAMPrefixListResolverTarget},
	)
}

// scanEC2IPAMResolver discovers IPAM prefix list resolver resources.
// Resolvers and resolver targets each carry a native ARN field on the
// SDK summary so no synthesis is required.
func scanEC2IPAMResolver(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(ctx,
		func(ctx context.Context) (int, int, error) {
			return scanIPAMPrefixListResolvers(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIPAMPrefixListResolverTargets(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanIPAMPrefixListResolvers(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeIpamPrefixListResolvers", acct, region, st,
		ec2.NewDescribeIpamPrefixListResolversPaginator(client, &ec2.DescribeIpamPrefixListResolversInput{}),
		func(page *ec2.DescribeIpamPrefixListResolversOutput) []*store.Resource {
			var out []*store.Resource
			for _, r := range page.IpamPrefixListResolvers {
				arn := sv(r.IpamPrefixListResolverArn)
				if arn == "" {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2IPAMPrefixListResolver,
					NativeID:       arn,
					Region:         &region,
					TagsJSON:       awsTagsJSON(r.Tags),
					AttributesJSON: mustJSON(r),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanIPAMPrefixListResolverTargets(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeIpamPrefixListResolverTargets", acct, region, st,
		ec2.NewDescribeIpamPrefixListResolverTargetsPaginator(client, &ec2.DescribeIpamPrefixListResolverTargetsInput{}),
		func(page *ec2.DescribeIpamPrefixListResolverTargetsOutput) []*store.Resource {
			var out []*store.Resource
			for _, t := range page.IpamPrefixListResolverTargets {
				arn := sv(t.IpamPrefixListResolverTargetArn)
				if arn == "" {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2IPAMPrefixListResolverTarget,
					NativeID:       arn,
					Region:         &region,
					TagsJSON:       awsTagsJSON(t.Tags),
					AttributesJSON: mustJSON(t),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
