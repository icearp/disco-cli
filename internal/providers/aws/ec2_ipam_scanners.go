package aws

import (
	"cmp"
	"context"
	"fmt"
	"sync/atomic"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

const (
	ec2IPAMPoolsOp                 = "ec2:DescribeIpamPools"
	ec2IPAMPoolsPageSize           = 1000 // IpamMaxResults maximum
	ec2IPAMPoolAllocationsOp       = "ec2:GetIpamPoolAllocations"
	ec2IPAMPoolAllocationsPageSize = 100000 // GetIpamPoolAllocationsMaxResults maximum
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2IPAM, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2IPAMScope, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2IPAMPool, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2IPAMPoolCIDR, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2IPAMAllocation, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2IPAMResourceDiscovery, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2IPAMResourceDiscoveryAssociation, Service: "ec2"})
}

// scanEC2IPAM discovers IPAM-related EC2 resources in parallel; the pool
// allocations are read later, by scanEC2Late. It also reports whether
// scanIPAMs stored an IPAM whose home Region is region.
func scanEC2IPAM(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, homeIPAM bool, err error) {
	total, inserted, err = runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			t, n, home, err := scanIPAMs(ctx, client, acct, region, st, scanID)
			homeIPAM = home
			return t, n, err
		},
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
			return scanIPAMResourceDiscoveries(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIPAMResourceDiscoveryAssociations(ctx, client, acct, region, st, scanID)
		},
	)
	return total, inserted, homeIPAM, err
}

// scanIPAMs stores the IPAMs and reports whether one whose home Region
// (IpamRegion) is region was stored. A listing that fails reports none, even
// when earlier pages were stored.
func scanIPAMs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, homeIPAM bool, err error) {
	total, inserted, err = ec2PageScan(
		ctx, "ec2:DescribeIpams", acct, region, st,
		ec2.NewDescribeIpamsPaginator(client, &ec2.DescribeIpamsInput{}),
		func(page *ec2.DescribeIpamsOutput) []*store.Resource {
			var out []*store.Resource
			for _, ipam := range page.Ipams {
				if sv(ipam.IpamRegion) == region {
					homeIPAM = true
				}
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
	return total, inserted, homeIPAM && err == nil, err
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

// scanIPAMPools stores the IPAM pools, those read before a failed page
// included (storeTokenList). A region feature gap on the first page is an
// empty listing, as ec2PageScan treats it; on a later page it fails the
// listing. The listing is hand-rolled, not the SDK paginator, because that one
// repeats a repeated NextToken forever.
func scanIPAMPools(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ec2IPAMPoolsOp,
		func(ctx context.Context, token *string) ([]ec2types.IpamPool, *string, error) {
			out, err := client.DescribeIpamPools(ctx, &ec2.DescribeIpamPoolsInput{
				MaxResults: sdkaws.Int32(ec2IPAMPoolsPageSize),
				NextToken:  token,
			})
			if token == nil && isEC2RegionFeatureGap(err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.IpamPools, out.NextToken, nil
		},
		func(pool ec2types.IpamPool) (*store.Resource, string, string) {
			arn := sv(pool.IpamPoolArn)
			if arn == "" {
				return nil, "pools have no IpamPoolArn", cmp.Or(sv(pool.IpamPoolId), "one without an IpamPoolId")
			}
			status := string(pool.State)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2IPAMPool,
				NativeID:       arn,
				Name:           pool.Description,
				Region:         &region,
				Status:         &status,
				TagsJSON:       awsTagsJSON(pool.Tags),
				AttributesJSON: mustJSON(pool),
				DiscoveredBy:   scanID,
			}, "", ""
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

// scanIPAMAllocations stores the allocations of each IPAM pool this scan
// stored in region, except deleted pools. One pool's failure stops no other
// (storeTokenEachParent). A pool deleted since it was listed fails the phase:
// an EC2 parent may be skipped only with a warning, and storeTokenEachParent's
// notFound skips one silently when nothing was read from it.
func scanIPAMAllocations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pools, err := ec2StoredParents(st, acct, region, scanID, TypeEC2IPAMPool, "ipam-pool", string(ec2types.IpamPoolStateDeleteComplete))
	if err != nil {
		return 0, 0, err
	}
	poolIDs := make([]string, len(pools))
	for i, p := range pools {
		poolIDs[i] = p.id
	}
	return storeTokenEachParent(ctx, st, acct, region, ec2IPAMPoolAllocationsOp, nil, poolIDs,
		func(ctx context.Context, poolID string, token *string) ([]ec2types.IpamPoolAllocation, *string, error) {
			out, err := client.GetIpamPoolAllocations(ctx, &ec2.GetIpamPoolAllocationsInput{
				IpamPoolId: &poolID,
				MaxResults: sdkaws.Int32(ec2IPAMPoolAllocationsPageSize),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.IpamPoolAllocations, out.NextToken, nil
		},
		func(poolID string, alloc ec2types.IpamPoolAllocation) (*store.Resource, string, string) {
			id := sv(alloc.IpamPoolAllocationId)
			if id == "" {
				return nil, "allocations have no IpamPoolAllocationId", "one in pool " + poolID
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2IPAMAllocation,
				NativeID:       ec2ARN(region, acct.ID, "ipam-allocation", poolID+"/"+id),
				Name:           alloc.Description,
				Region:         &region,
				TagsJSON:       awsTagsJSON(alloc.Tags),
				AttributesJSON: mustJSON(alloc),
				DiscoveredBy:   scanID,
			}, "", ""
		},
	)
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
