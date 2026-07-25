package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2IPAM, Service: "ec2", Upstream: "AWS::EC2::IPAM", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2IPAMScope, Service: "ec2", Upstream: "AWS::EC2::IPAMScope"})
	registerType(restype.Descriptor{Type: TypeEC2IPAMPool, Service: "ec2", Upstream: "AWS::EC2::IPAMPool"})
	registerType(restype.Descriptor{Type: TypeEC2IPAMPoolCIDR, Service: "ec2", Upstream: "AWS::EC2::IPAMPoolCidr"})
	registerType(restype.Descriptor{Type: TypeEC2IPAMAllocation, Service: "ec2", Upstream: "AWS::EC2::IPAMAllocation"})
	registerType(restype.Descriptor{Type: TypeEC2IPAMResourceDiscovery, Service: "ec2", Upstream: "AWS::EC2::IPAMResourceDiscovery", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2IPAMResourceDiscoveryAssociation, Service: "ec2", Upstream: "AWS::EC2::IPAMResourceDiscoveryAssociation"})
}

// scanEC2IPAM discovers all IPAM-related EC2 resources in parallel.
func scanEC2IPAM(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) { return scanIPAMs(ctx, client, acct, region, st, scanID) },
		func(ctx context.Context) (int, int, error) {
			return scanIPAMScopes(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIPAMPools(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIPAMPoolCIDRs(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIPAMAllocations(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIPAMResourceDiscoveries(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIPAMResourceDiscoveryAssociations(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanIPAMs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeIpams", acct, region, st,
		ec2.NewDescribeIpamsPaginator(client, &ec2.DescribeIpamsInput{}),
		func(page *ec2.DescribeIpamsOutput) []*store.Resource {
			var out []*store.Resource
			for _, ipam := range page.Ipams {
				status := string(ipam.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2IPAM,
					NativeID:       sv(ipam.IpamArn),
					Name:           ipam.Description,
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(ipam.Tags),
					AttributesJSON: mustJSON(ipam),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanIPAMScopes(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeIpamScopes", acct, region, st,
		ec2.NewDescribeIpamScopesPaginator(client, &ec2.DescribeIpamScopesInput{}),
		func(page *ec2.DescribeIpamScopesOutput) []*store.Resource {
			var out []*store.Resource
			for _, scope := range page.IpamScopes {
				status := string(scope.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2IPAMScope,
					NativeID:       sv(scope.IpamScopeArn),
					Name:           scope.Description,
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(scope.Tags),
					AttributesJSON: mustJSON(scope),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanIPAMPools(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeIpamPools", acct, region, st,
		ec2.NewDescribeIpamPoolsPaginator(client, &ec2.DescribeIpamPoolsInput{}),
		func(page *ec2.DescribeIpamPoolsOutput) []*store.Resource {
			var out []*store.Resource
			for _, pool := range page.IpamPools {
				status := string(pool.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2IPAMPool,
					NativeID:       sv(pool.IpamPoolArn),
					Name:           pool.Description,
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(pool.Tags),
					AttributesJSON: mustJSON(pool),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanIPAMPoolCIDRs fetches all IPAM pool IDs, then fans out concurrently to
// retrieve CIDRs for each pool.
func scanIPAMPoolCIDRs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	poolIDs, err := listIPAMPoolIDs(ctx, client, acct, region, st)
	if err != nil {
		return
	}
	if len(poolIDs) == 0 {
		return
	}
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, ctx := errgroup.WithContext(ctx)
	for _, poolID := range poolIDs {
		g.Go(func() error {
			tt, nn, e := ec2PageScan(
				ctx, "ec2:GetIpamPoolCidrs", acct, region, st,
				ec2.NewGetIpamPoolCidrsPaginator(client, &ec2.GetIpamPoolCidrsInput{IpamPoolId: &poolID}),
				func(page *ec2.GetIpamPoolCidrsOutput) []*store.Resource {
					var out []*store.Resource
					for _, cidr := range page.IpamPoolCidrs {
						nativeID := ec2ARN(region, acct.ID, "ipam-pool-cidr", poolID+"/"+sv(cidr.Cidr))
						status := string(cidr.State)
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeEC2IPAMPoolCIDR,
							NativeID:       nativeID,
							Region:         &region,
							Status:         &status,
							AttributesJSON: mustJSON(cidr),
							DiscoveredBy:   scanID,
						})
					}
					return out
				},
			)
			add(tt, nn)
			return e
		})
	}
	err = g.Wait()
	return int(t.Load()), int(n.Load()), err
}

// scanIPAMAllocations fetches all IPAM pool IDs, then fans out concurrently to
// retrieve allocations for each pool.
func scanIPAMAllocations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	poolIDs, err := listIPAMPoolIDs(ctx, client, acct, region, st)
	if err != nil {
		return
	}
	if len(poolIDs) == 0 {
		return
	}
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, ctx := errgroup.WithContext(ctx)
	for _, poolID := range poolIDs {
		g.Go(func() error {
			tt, nn, e := ec2PageScan(
				ctx, "ec2:GetIpamPoolAllocations", acct, region, st,
				ec2.NewGetIpamPoolAllocationsPaginator(client, &ec2.GetIpamPoolAllocationsInput{IpamPoolId: &poolID}),
				func(page *ec2.GetIpamPoolAllocationsOutput) []*store.Resource {
					var out []*store.Resource
					for _, alloc := range page.IpamPoolAllocations {
						nativeID := ec2ARN(region, acct.ID, "ipam-allocation", poolID+"/"+sv(alloc.IpamPoolAllocationId))
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeEC2IPAMAllocation,
							NativeID:       nativeID,
							Name:           alloc.Description,
							Region:         &region,
							AttributesJSON: mustJSON(alloc),
							DiscoveredBy:   scanID,
						})
					}
					return out
				},
			)
			add(tt, nn)
			return e
		})
	}
	err = g.Wait()
	return int(t.Load()), int(n.Load()), err
}

func scanIPAMResourceDiscoveries(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeIpamResourceDiscoveries", acct, region, st,
		ec2.NewDescribeIpamResourceDiscoveriesPaginator(client, &ec2.DescribeIpamResourceDiscoveriesInput{}),
		func(page *ec2.DescribeIpamResourceDiscoveriesOutput) []*store.Resource {
			var out []*store.Resource
			for _, rd := range page.IpamResourceDiscoveries {
				status := string(rd.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2IPAMResourceDiscovery,
					NativeID:       sv(rd.IpamResourceDiscoveryArn),
					Name:           rd.Description,
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(rd.Tags),
					AttributesJSON: mustJSON(rd),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanIPAMResourceDiscoveryAssociations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeIpamResourceDiscoveryAssociations", acct, region, st,
		ec2.NewDescribeIpamResourceDiscoveryAssociationsPaginator(client, &ec2.DescribeIpamResourceDiscoveryAssociationsInput{}),
		func(page *ec2.DescribeIpamResourceDiscoveryAssociationsOutput) []*store.Resource {
			var out []*store.Resource
			for _, assoc := range page.IpamResourceDiscoveryAssociations {
				status := string(assoc.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2IPAMResourceDiscoveryAssociation,
					NativeID:       sv(assoc.IpamResourceDiscoveryAssociationArn),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(assoc.Tags),
					AttributesJSON: mustJSON(assoc),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// listIPAMPoolIDs returns all IPAM pool IDs in this region; used by nested scanners.
func listIPAMPoolIDs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store) ([]string, error) {
	var poolIDs []string
	pager := ec2.NewDescribeIpamPoolsPaginator(client, &ec2.DescribeIpamPoolsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "ec2:DescribeIpamPools", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("ec2:DescribeIpamPools (list pool IDs): %w", err)
		}
		for _, pool := range page.IpamPools {
			if pool.IpamPoolId != nil {
				poolIDs = append(poolIDs, *pool.IpamPoolId)
			}
		}
	}
	return poolIDs, nil
}
