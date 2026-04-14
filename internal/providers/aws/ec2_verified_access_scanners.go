package aws

import (
	"context"
	"sync/atomic"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/sync/errgroup"
)

// scanEC2VerifiedAccess discovers all Verified Access resources in parallel.
func scanEC2VerifiedAccess(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		tt, nn, e := scanVerifiedAccessInstances(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanVerifiedAccessTrustProviders(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanVerifiedAccessGroups(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanVerifiedAccessEndpoints(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	err = g.Wait()
	return int(t.Load()), int(n.Load()), err
}

func scanVerifiedAccessInstances(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeVerifiedAccessInstances", acct, region, st,
		ec2.NewDescribeVerifiedAccessInstancesPaginator(client, &ec2.DescribeVerifiedAccessInstancesInput{}),
		func(page *ec2.DescribeVerifiedAccessInstancesOutput) []*store.Resource {
			var out []*store.Resource
			for _, inst := range page.VerifiedAccessInstances {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2VerifiedAccessInstance,
					NativeID:       ec2ARN(region, acct.ID, "verified-access-instance", sv(inst.VerifiedAccessInstanceId)),
					Region:         &region,
					TagsJSON:       awsTagsJSON(inst.Tags),
					AttributesJSON: mustJSON(inst),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanVerifiedAccessTrustProviders(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeVerifiedAccessTrustProviders", acct, region, st,
		ec2.NewDescribeVerifiedAccessTrustProvidersPaginator(client, &ec2.DescribeVerifiedAccessTrustProvidersInput{}),
		func(page *ec2.DescribeVerifiedAccessTrustProvidersOutput) []*store.Resource {
			var out []*store.Resource
			for _, tp2 := range page.VerifiedAccessTrustProviders {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2VerifiedAccessTrustProvider,
					NativeID:       ec2ARN(region, acct.ID, "verified-access-trust-provider", sv(tp2.VerifiedAccessTrustProviderId)),
					Region:         &region,
					TagsJSON:       awsTagsJSON(tp2.Tags),
					AttributesJSON: mustJSON(tp2),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanVerifiedAccessGroups(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeVerifiedAccessGroups", acct, region, st,
		ec2.NewDescribeVerifiedAccessGroupsPaginator(client, &ec2.DescribeVerifiedAccessGroupsInput{}),
		func(page *ec2.DescribeVerifiedAccessGroupsOutput) []*store.Resource {
			var out []*store.Resource
			for _, g := range page.VerifiedAccessGroups {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2VerifiedAccessGroup,
					NativeID:       ec2ARN(region, acct.ID, "verified-access-group", sv(g.VerifiedAccessGroupId)),
					Region:         &region,
					TagsJSON:       awsTagsJSON(g.Tags),
					AttributesJSON: mustJSON(g),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanVerifiedAccessEndpoints(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeVerifiedAccessEndpoints", acct, region, st,
		ec2.NewDescribeVerifiedAccessEndpointsPaginator(client, &ec2.DescribeVerifiedAccessEndpointsInput{}),
		func(page *ec2.DescribeVerifiedAccessEndpointsOutput) []*store.Resource {
			var out []*store.Resource
			for _, ep := range page.VerifiedAccessEndpoints {
				var status string
				if ep.Status != nil {
					status = string(ep.Status.Code)
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2VerifiedAccessEndpoint,
					NativeID:       ec2ARN(region, acct.ID, "verified-access-endpoint", sv(ep.VerifiedAccessEndpointId)),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(ep.Tags),
					AttributesJSON: mustJSON(ep),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
