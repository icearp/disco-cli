package aws

import (
	"context"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2IPAMPrefixListResolver, Service: "ec2", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2IPAMPrefixListResolverTarget, Service: "ec2"})
}

// scanEC2IPAMResolver discovers IPAM prefix list resolver resources.
// Resolvers and targets each carry a native ARN on the SDK summary, so no
// NativeID synthesis is needed.
func scanEC2IPAMResolver(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanIPAMPrefixListResolvers(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIPAMPrefixListResolverTargets(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanIPAMPrefixListResolvers(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeIpamPrefixListResolvers", acct, region, st,
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
	return ec2PageScan(
		ctx, "ec2:DescribeIpamPrefixListResolverTargets", acct, region, st,
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
