package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/sync/errgroup"
)

// scanEC2Networking discovers core VPC types and networking extension types: VPCs,
// subnets, internet gateways, NAT gateways, route tables, EIPs, ENIs, NACLs,
// VPC endpoints, VPC peering, DHCP options, and egress-only IGWs.
func scanEC2Networking(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return scanVPCs(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSubnets(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanInternetGateways(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanNatGateways(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanRouteTables(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEIPs(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanNetworkInterfaces(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanNetworkACLs(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCEndpoints(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCPeeringConnections(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanDHCPOptions(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEgressOnlyIGWs(ctx, client, acct, region, st, scanID) })
	return g.Wait()
}

func scanVPCs(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeVpcs", acct, region, st,
		ec2.NewDescribeVpcsPaginator(client, &ec2.DescribeVpcsInput{}),
		func(page *ec2.DescribeVpcsOutput) []*store.Resource {
			var out []*store.Resource
			for _, vpc := range page.Vpcs {
				status := string(vpc.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2VPC,
					NativeID:       ec2ARN(region, acct.ID, "vpc", sv(vpc.VpcId)),
					Name:           ec2TagName(vpc.Tags),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(vpc.Tags),
					AttributesJSON: mustJSON(vpc),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanSubnets(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeSubnets", acct, region, st,
		ec2.NewDescribeSubnetsPaginator(client, &ec2.DescribeSubnetsInput{}),
		func(page *ec2.DescribeSubnetsOutput) []*store.Resource {
			var out []*store.Resource
			for _, sn := range page.Subnets {
				status := string(sn.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2Subnet,
					NativeID:       ec2ARN(region, acct.ID, "subnet", sv(sn.SubnetId)),
					Name:           ec2TagName(sn.Tags),
					Region:         &region,
					Zone:           sn.AvailabilityZoneId,
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

func scanInternetGateways(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeInternetGateways", acct, region, st,
		ec2.NewDescribeInternetGatewaysPaginator(client, &ec2.DescribeInternetGatewaysInput{}),
		func(page *ec2.DescribeInternetGatewaysOutput) []*store.Resource {
			var out []*store.Resource
			for _, igw := range page.InternetGateways {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2InternetGateway,
					NativeID:       ec2ARN(region, acct.ID, "internet-gateway", sv(igw.InternetGatewayId)),
					Name:           ec2TagName(igw.Tags),
					Region:         &region,
					TagsJSON:       awsTagsJSON(igw.Tags),
					AttributesJSON: mustJSON(igw),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanNatGateways(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeNatGateways", acct, region, st,
		ec2.NewDescribeNatGatewaysPaginator(client, &ec2.DescribeNatGatewaysInput{}),
		func(page *ec2.DescribeNatGatewaysOutput) []*store.Resource {
			var out []*store.Resource
			for _, ngw := range page.NatGateways {
				status := string(ngw.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2NatGateway,
					NativeID:       ec2ARN(region, acct.ID, "natgateway", sv(ngw.NatGatewayId)),
					Name:           ec2TagName(ngw.Tags),
					Region:         &region,
					CreatedAt:      tp(ngw.CreateTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(ngw.Tags),
					AttributesJSON: mustJSON(ngw),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanRouteTables(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeRouteTables", acct, region, st,
		ec2.NewDescribeRouteTablesPaginator(client, &ec2.DescribeRouteTablesInput{}),
		func(page *ec2.DescribeRouteTablesOutput) []*store.Resource {
			var out []*store.Resource
			for _, rt := range page.RouteTables {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2RouteTable,
					NativeID:       ec2ARN(region, acct.ID, "route-table", sv(rt.RouteTableId)),
					Name:           ec2TagName(rt.Tags),
					Region:         &region,
					TagsJSON:       awsTagsJSON(rt.Tags),
					AttributesJSON: mustJSON(rt),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanEIPs(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	// DescribeAddresses has no paginator; all results returned in one call.
	out, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribeAddresses", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribeAddresses: %w", err)
	}
	var batch []*store.Resource
	for _, addr := range out.Addresses {
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2EIP,
			NativeID:       ec2ARN(region, acct.ID, "elastic-ip", sv(addr.AllocationId)),
			Name:           ec2TagName(addr.Tags),
			Region:         &region,
			TagsJSON:       awsTagsJSON(addr.Tags),
			AttributesJSON: mustJSON(addr),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert EIPs: %w", err)
		}
	}
	return nil
}

func scanNetworkInterfaces(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeNetworkInterfaces", acct, region, st,
		ec2.NewDescribeNetworkInterfacesPaginator(client, &ec2.DescribeNetworkInterfacesInput{}),
		func(page *ec2.DescribeNetworkInterfacesOutput) []*store.Resource {
			var out []*store.Resource
			for _, eni := range page.NetworkInterfaces {
				status := string(eni.Status)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2NetworkInterface,
					NativeID:       ec2ARN(region, acct.ID, "network-interface", sv(eni.NetworkInterfaceId)),
					Name:           ec2TagName(eni.TagSet),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(eni.TagSet),
					AttributesJSON: mustJSON(eni),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanNetworkACLs(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeNetworkAcls", acct, region, st,
		ec2.NewDescribeNetworkAclsPaginator(client, &ec2.DescribeNetworkAclsInput{}),
		func(page *ec2.DescribeNetworkAclsOutput) []*store.Resource {
			var out []*store.Resource
			for _, nacl := range page.NetworkAcls {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2NetworkACL,
					NativeID:       ec2ARN(region, acct.ID, "network-acl", sv(nacl.NetworkAclId)),
					Name:           ec2TagName(nacl.Tags),
					Region:         &region,
					TagsJSON:       awsTagsJSON(nacl.Tags),
					AttributesJSON: mustJSON(nacl),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanVPCEndpoints(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeVpcEndpoints", acct, region, st,
		ec2.NewDescribeVpcEndpointsPaginator(client, &ec2.DescribeVpcEndpointsInput{}),
		func(page *ec2.DescribeVpcEndpointsOutput) []*store.Resource {
			var out []*store.Resource
			for _, ep := range page.VpcEndpoints {
				status := string(ep.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2VPCEndpoint,
					NativeID:       ec2ARN(region, acct.ID, "vpc-endpoint", sv(ep.VpcEndpointId)),
					Name:           ec2TagName(ep.Tags),
					Region:         &region,
					CreatedAt:      tp(ep.CreationTimestamp),
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

func scanVPCPeeringConnections(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeVpcPeeringConnections", acct, region, st,
		ec2.NewDescribeVpcPeeringConnectionsPaginator(client, &ec2.DescribeVpcPeeringConnectionsInput{}),
		func(page *ec2.DescribeVpcPeeringConnectionsOutput) []*store.Resource {
			var out []*store.Resource
			for _, pc := range page.VpcPeeringConnections {
				status := string(pc.Status.Code)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2VPCPeeringConnection,
					NativeID:       ec2ARN(region, acct.ID, "vpc-peering-connection", sv(pc.VpcPeeringConnectionId)),
					Name:           ec2TagName(pc.Tags),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(pc.Tags),
					AttributesJSON: mustJSON(pc),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanDHCPOptions(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeDhcpOptions", acct, region, st,
		ec2.NewDescribeDhcpOptionsPaginator(client, &ec2.DescribeDhcpOptionsInput{}),
		func(page *ec2.DescribeDhcpOptionsOutput) []*store.Resource {
			var out []*store.Resource
			for _, dhcp := range page.DhcpOptions {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2DHCPOptions,
					NativeID:       ec2ARN(region, acct.ID, "dhcp-options", sv(dhcp.DhcpOptionsId)),
					Name:           ec2TagName(dhcp.Tags),
					Region:         &region,
					TagsJSON:       awsTagsJSON(dhcp.Tags),
					AttributesJSON: mustJSON(dhcp),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanEgressOnlyIGWs(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeEgressOnlyInternetGateways", acct, region, st,
		ec2.NewDescribeEgressOnlyInternetGatewaysPaginator(client, &ec2.DescribeEgressOnlyInternetGatewaysInput{}),
		func(page *ec2.DescribeEgressOnlyInternetGatewaysOutput) []*store.Resource {
			var out []*store.Resource
			for _, eigw := range page.EgressOnlyInternetGateways {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2EgressOnlyIGW,
					NativeID:       ec2ARN(region, acct.ID, "egress-only-internet-gateway", sv(eigw.EgressOnlyInternetGatewayId)),
					Name:           ec2TagName(eigw.Tags),
					Region:         &region,
					TagsJSON:       awsTagsJSON(eigw.Tags),
					AttributesJSON: mustJSON(eigw),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
