package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2ReservedInstances, Service: "ec2", Upstream: "AWS::ec2::reserved-instances", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2HostReservation, Service: "ec2", Upstream: "AWS::ec2::host-reservation", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2CapacityBlock, Service: "ec2", Upstream: "AWS::ec2::capacity-block", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2FpgaImage, Service: "ec2", Upstream: "AWS::ec2::fpga-image", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2PublicIpv4Pool, Service: "ec2", Upstream: "AWS::ec2::ipv4pool-ec2", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2Ipv6Pool, Service: "ec2", Upstream: "AWS::ec2::ipv6pool-ec2", Leaf: true})
}

// scanEC2Inventory discovers EC2 purchase/capacity inventory and BYOIP address
// pools: Reserved Instances, dedicated-host reservations, Capacity Blocks,
// self-owned FPGA images, and public IPv4 / IPv6 address pools. These are
// billing or address-allocation records with no outbound resource edges (Leaf).
func scanEC2Inventory(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanReservedInstances(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanHostReservations(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCapacityBlocks(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanFpgaImages(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanPublicIpv4Pools(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIpv6Pools(ctx, client, acct, region, st, scanID)
		},
	)
}

// scanReservedInstances — DescribeReservedInstances returns the full set in one
// call (no paginator). Cancelled/retired RIs remain listable for ~a year.
func scanReservedInstances(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, perr := client.DescribeReservedInstances(ctx, &ec2.DescribeReservedInstancesInput{})
	if perr != nil {
		if isAccessDenied(perr) {
			return 0, 0, skipIfAccessDenied(st, "ec2:DescribeReservedInstances", acct.ID, region, perr)
		}
		return 0, 0, fmt.Errorf("ec2:DescribeReservedInstances: %w", perr)
	}
	var batch []*store.Resource
	for _, ri := range out.ReservedInstances {
		id := sv(ri.ReservedInstancesId)
		if id == "" {
			continue
		}
		status := string(ri.State)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2ReservedInstances,
			NativeID:       ec2ARN(region, acct.ID, "reserved-instances", id),
			Region:         &region,
			Status:         &status,
			TagsJSON:       awsTagsJSON(ri.Tags),
			AttributesJSON: mustJSON(ri),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ec2 reserved-instances: %w", uerr)
	}
	return len(batch), n, nil
}

func scanHostReservations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeHostReservations", acct, region, st,
		ec2.NewDescribeHostReservationsPaginator(client, &ec2.DescribeHostReservationsInput{}),
		func(page *ec2.DescribeHostReservationsOutput) []*store.Resource {
			var out []*store.Resource
			for _, hr := range page.HostReservationSet {
				id := sv(hr.HostReservationId)
				if id == "" {
					continue
				}
				status := string(hr.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2HostReservation,
					NativeID:       ec2ARN(region, acct.ID, "host-reservation", id),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(hr.Tags),
					AttributesJSON: mustJSON(hr),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanCapacityBlocks(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeCapacityBlocks", acct, region, st,
		ec2.NewDescribeCapacityBlocksPaginator(client, &ec2.DescribeCapacityBlocksInput{}),
		func(page *ec2.DescribeCapacityBlocksOutput) []*store.Resource {
			var out []*store.Resource
			for _, cb := range page.CapacityBlocks {
				id := sv(cb.CapacityBlockId)
				if id == "" {
					continue
				}
				status := string(cb.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2CapacityBlock,
					NativeID:       ec2ARN(region, acct.ID, "capacity-block", id),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(cb.Tags),
					AttributesJSON: mustJSON(cb),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanFpgaImages discovers self-owned Amazon FPGA Images. Public/Marketplace
// AFIs are unbounded and not ours to audit (same ownership filter as AMIs).
func scanFpgaImages(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeFpgaImages", acct, region, st,
		ec2.NewDescribeFpgaImagesPaginator(client, &ec2.DescribeFpgaImagesInput{Owners: []string{"self"}}),
		func(page *ec2.DescribeFpgaImagesOutput) []*store.Resource {
			var out []*store.Resource
			for _, fi := range page.FpgaImages {
				id := sv(fi.FpgaImageId)
				if id == "" {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2FpgaImage,
					NativeID:       ec2ARN(region, acct.ID, "fpga-image", id),
					Name:           fi.Name,
					Region:         &region,
					TagsJSON:       awsTagsJSON(fi.Tags),
					AttributesJSON: mustJSON(fi),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanPublicIpv4Pools(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribePublicIpv4Pools", acct, region, st,
		ec2.NewDescribePublicIpv4PoolsPaginator(client, &ec2.DescribePublicIpv4PoolsInput{}),
		func(page *ec2.DescribePublicIpv4PoolsOutput) []*store.Resource {
			var out []*store.Resource
			for _, p := range page.PublicIpv4Pools {
				id := sv(p.PoolId)
				if id == "" {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2PublicIpv4Pool,
					NativeID:       ec2ARN(region, acct.ID, "ipv4pool-ec2", id),
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

func scanIpv6Pools(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeIpv6Pools", acct, region, st,
		ec2.NewDescribeIpv6PoolsPaginator(client, &ec2.DescribeIpv6PoolsInput{}),
		func(page *ec2.DescribeIpv6PoolsOutput) []*store.Resource {
			var out []*store.Resource
			for _, p := range page.Ipv6Pools {
				id := sv(p.PoolId)
				if id == "" {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2Ipv6Pool,
					NativeID:       ec2ARN(region, acct.ID, "ipv6pool-ec2", id),
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
