package aws

import (
	"context"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2SecondaryInterface, Service: "ec2", Upstream: "AWS::ec2::secondary-interface"})
	registerType(restype.Descriptor{Type: TypeEC2SecondaryNetwork, Service: "ec2", Upstream: "AWS::ec2::secondary-network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2SecondarySubnet, Service: "ec2", Upstream: "AWS::ec2::secondary-subnet"})
}

// scanEC2Secondary discovers multi-VPC secondary networking resources:
// secondary networks, their secondary subnets, and the secondary interfaces
// attached across them.
func scanEC2Secondary(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanSecondaryNetworks(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSecondarySubnets(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanSecondaryInterfaces(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanSecondaryNetworks(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeSecondaryNetworks", acct, region, st,
		ec2.NewDescribeSecondaryNetworksPaginator(client, &ec2.DescribeSecondaryNetworksInput{}),
		func(page *ec2.DescribeSecondaryNetworksOutput) []*store.Resource {
			var out []*store.Resource
			for _, sn := range page.SecondaryNetworks {
				arn := sv(sn.SecondaryNetworkArn)
				if arn == "" {
					continue
				}
				status := string(sn.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SecondaryNetwork,
					NativeID:       arn,
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(sn.Tags),
					AttributesJSON: mustJSON(sn),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanSecondarySubnets(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeSecondarySubnets", acct, region, st,
		ec2.NewDescribeSecondarySubnetsPaginator(client, &ec2.DescribeSecondarySubnetsInput{}),
		func(page *ec2.DescribeSecondarySubnetsOutput) []*store.Resource {
			var out []*store.Resource
			for _, ss := range page.SecondarySubnets {
				arn := sv(ss.SecondarySubnetArn)
				if arn == "" {
					continue
				}
				status := string(ss.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SecondarySubnet,
					NativeID:       arn,
					Region:         &region,
					Zone:           ss.AvailabilityZoneId,
					Status:         &status,
					TagsJSON:       awsTagsJSON(ss.Tags),
					AttributesJSON: mustJSON(ss),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanSecondaryInterfaces(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeSecondaryInterfaces", acct, region, st,
		ec2.NewDescribeSecondaryInterfacesPaginator(client, &ec2.DescribeSecondaryInterfacesInput{}),
		func(page *ec2.DescribeSecondaryInterfacesOutput) []*store.Resource {
			var out []*store.Resource
			for _, si := range page.SecondaryInterfaces {
				arn := sv(si.SecondaryInterfaceArn)
				if arn == "" {
					continue
				}
				status := string(si.Status)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SecondaryInterface,
					NativeID:       arn,
					Region:         &region,
					Zone:           si.AvailabilityZoneId,
					Status:         &status,
					TagsJSON:       awsTagsJSON(si.Tags),
					AttributesJSON: mustJSON(si),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
