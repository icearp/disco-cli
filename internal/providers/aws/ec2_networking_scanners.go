package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2VPC, Service: "ec2", Upstream: "AWS::EC2::VPC", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2Subnet, Service: "ec2", Upstream: "AWS::EC2::Subnet"})
	registerType(restype.Descriptor{Type: TypeEC2InternetGateway, Service: "ec2", Upstream: "AWS::EC2::InternetGateway"})
	registerType(restype.Descriptor{Type: TypeEC2EgressOnlyIGW, Service: "ec2", Upstream: "AWS::EC2::EgressOnlyInternetGateway", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2NatGateway, Service: "ec2", Upstream: "AWS::EC2::NatGateway"})
	registerType(restype.Descriptor{Type: TypeEC2RouteTable, Service: "ec2", Upstream: "AWS::EC2::RouteTable"})
	registerType(restype.Descriptor{Type: TypeEC2NetworkInterface, Service: "ec2", Upstream: "AWS::EC2::NetworkInterface"})
	registerType(restype.Descriptor{Type: TypeEC2NetworkInterfacePermission, Service: "ec2", Upstream: "AWS::EC2::NetworkInterfacePermission"})
	registerType(restype.Descriptor{Type: TypeEC2NetworkACL, Service: "ec2", Upstream: "AWS::EC2::NetworkAcl"})
	registerType(restype.Descriptor{Type: TypeEC2EIP, Service: "ec2", Upstream: "AWS::EC2::EIP"})
	registerType(restype.Descriptor{Type: TypeEC2DHCPOptions, Service: "ec2", Upstream: "AWS::EC2::DHCPOptions", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2CarrierGateway, Service: "ec2", Upstream: "AWS::EC2::CarrierGateway"})
	registerType(restype.Descriptor{Type: TypeEC2VPCEndpoint, Service: "ec2", Upstream: "AWS::EC2::VPCEndpoint"})
	registerType(restype.Descriptor{Type: TypeEC2VPCEndpointService, Service: "ec2", Upstream: "AWS::EC2::VPCEndpointService", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2VPCEndpointServicePermissions, Service: "ec2", Upstream: "AWS::EC2::VPCEndpointServicePermissions"})
	registerType(restype.Descriptor{Type: TypeEC2VPCEndpointConnectionNotification, Service: "ec2", Upstream: "AWS::EC2::VPCEndpointConnectionNotification"})
	registerType(restype.Descriptor{Type: TypeEC2VPCPeeringConnection, Service: "ec2", Upstream: "AWS::EC2::VPCPeeringConnection"})
	registerType(restype.Descriptor{Type: TypeEC2VPCBlockPublicAccessOptions, Service: "ec2", Upstream: "AWS::EC2::VPCBlockPublicAccessOptions", Leaf: true, Managed: true})
	registerType(restype.Descriptor{Type: TypeEC2VPCBlockPublicAccessExclusion, Service: "ec2", Upstream: "AWS::EC2::VPCBlockPublicAccessExclusion", Leaf: true})
}

// scanEC2Networking discovers all networking resources: VPCs, subnets, internet
// gateways, NAT gateways, route tables, EIPs, ENIs, NACLs, VPC endpoints, VPC
// peering, DHCP options, egress-only IGWs, carrier gateways, VPC features,
// VPC endpoint services, and network interface permissions.
func scanEC2Networking(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) { return scanVPCs(ctx, client, acct, region, st, scanID) },
		func(ctx context.Context) (int, int, error) { return scanSubnets(ctx, client, acct, region, st, scanID) },
		func(ctx context.Context) (int, int, error) {
			return scanInternetGateways(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanNatGateways(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanRouteTables(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) { return scanEIPs(ctx, client, acct, region, st, scanID) },
		func(ctx context.Context) (int, int, error) {
			return scanNetworkInterfaces(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanNetworkACLs(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPCEndpoints(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPCPeeringConnections(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDHCPOptions(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEgressOnlyIGWs(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCarrierGateways(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPCBlockPublicAccessOptions(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPCBlockPublicAccessExclusions(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPCEndpointConnectionNotifications(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPCEndpointServices(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPCEndpointServicePermissions(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanNetworkInterfacePermissions(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanVPCs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeVpcs", acct, region, st,
		ec2.NewDescribeVpcsPaginator(client, &ec2.DescribeVpcsInput{}),
		func(page *ec2.DescribeVpcsOutput) []*store.Resource {
			var out []*store.Resource
			for _, vpc := range page.Vpcs {
				status := string(vpc.State)
				out = append(out, &store.Resource{
					Provider:          "aws",
					AccountID:         acct.ID,
					AccountName:       &acct.Name,
					Type:              TypeEC2VPC,
					NativeID:          ec2ARN(region, acct.ID, "vpc", sv(vpc.VpcId)),
					Name:              ec2TagName(vpc.Tags),
					Region:            &region,
					Status:            &status,
					ManagedByProvider: vpc.IsDefault != nil && *vpc.IsDefault,
					TagsJSON:          awsTagsJSON(vpc.Tags),
					AttributesJSON:    mustJSON(vpc),
					DiscoveredBy:      scanID,
				})
			}
			return out
		},
	)
}

func scanSubnets(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeSubnets", acct, region, st,
		ec2.NewDescribeSubnetsPaginator(client, &ec2.DescribeSubnetsInput{}),
		func(page *ec2.DescribeSubnetsOutput) []*store.Resource {
			var out []*store.Resource
			for _, sn := range page.Subnets {
				status := string(sn.State)
				out = append(out, &store.Resource{
					Provider:          "aws",
					AccountID:         acct.ID,
					AccountName:       &acct.Name,
					Type:              TypeEC2Subnet,
					NativeID:          ec2ARN(region, acct.ID, "subnet", sv(sn.SubnetId)),
					Name:              ec2TagName(sn.Tags),
					Region:            &region,
					Zone:              sn.AvailabilityZoneId,
					Status:            &status,
					ManagedByProvider: sn.DefaultForAz != nil && *sn.DefaultForAz,
					TagsJSON:          awsTagsJSON(sn.Tags),
					AttributesJSON:    mustJSON(sn),
					DiscoveredBy:      scanID,
				})
			}
			return out
		},
	)
}

func scanInternetGateways(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeInternetGateways", acct, region, st,
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

func scanNatGateways(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeNatGateways", acct, region, st,
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

func scanRouteTables(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeRouteTables", acct, region, st,
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

func scanEIPs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// DescribeAddresses has no paginator; all results returned in one call.
	out, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "ec2:DescribeAddresses", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("ec2:DescribeAddresses: %w", err)
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
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert EIPs: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

func scanNetworkInterfaces(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeNetworkInterfaces", acct, region, st,
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

func scanNetworkACLs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeNetworkAcls", acct, region, st,
		ec2.NewDescribeNetworkAclsPaginator(client, &ec2.DescribeNetworkAclsInput{}),
		func(page *ec2.DescribeNetworkAclsOutput) []*store.Resource {
			var out []*store.Resource
			for _, nacl := range page.NetworkAcls {
				out = append(out, &store.Resource{
					Provider:          "aws",
					AccountID:         acct.ID,
					AccountName:       &acct.Name,
					Type:              TypeEC2NetworkACL,
					NativeID:          ec2ARN(region, acct.ID, "network-acl", sv(nacl.NetworkAclId)),
					Name:              ec2TagName(nacl.Tags),
					Region:            &region,
					ManagedByProvider: nacl.IsDefault != nil && *nacl.IsDefault,
					TagsJSON:          awsTagsJSON(nacl.Tags),
					AttributesJSON:    mustJSON(nacl),
					DiscoveredBy:      scanID,
				})
			}
			return out
		},
	)
}

func scanVPCEndpoints(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeVpcEndpoints", acct, region, st,
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

func scanVPCPeeringConnections(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeVpcPeeringConnections", acct, region, st,
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

func scanDHCPOptions(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeDhcpOptions", acct, region, st,
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

func scanEgressOnlyIGWs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeEgressOnlyInternetGateways", acct, region, st,
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

func scanCarrierGateways(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	total, inserted, err = ec2PageScan(
		ctx, "ec2:DescribeCarrierGateways", acct, region, st,
		ec2.NewDescribeCarrierGatewaysPaginator(client, &ec2.DescribeCarrierGatewaysInput{}),
		func(page *ec2.DescribeCarrierGatewaysOutput) []*store.Resource {
			var out []*store.Resource
			for _, cgw := range page.CarrierGateways {
				status := string(cgw.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2CarrierGateway,
					NativeID:       ec2ARN(region, acct.ID, "carrier-gateway", sv(cgw.CarrierGatewayId)),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(cgw.Tags),
					AttributesJSON: mustJSON(cgw),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
	// Carrier gateways are Wavelength-zone-only; non-Wavelength regions
	// reject with UnsupportedOperation — silent-skip.
	if err != nil && isAPIErrorCode(err, "UnsupportedOperation") {
		return 0, 0, nil
	}
	return total, inserted, err
}

// scanVPCBlockPublicAccessOptions retrieves the account-level VPC block public
// access setting (one per account); NativeID omits region for cross-scan stability.
func scanVPCBlockPublicAccessOptions(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.DescribeVpcBlockPublicAccessOptions(ctx, &ec2.DescribeVpcBlockPublicAccessOptionsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "ec2:DescribeVpcBlockPublicAccessOptions", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("ec2:DescribeVpcBlockPublicAccessOptions: %w", err)
	}
	if out.VpcBlockPublicAccessOptions == nil {
		return
	}
	opt := out.VpcBlockPublicAccessOptions
	// Account-level NativeID (no region) so every region upserts the same row.
	nativeID := ec2ARN("", acct.ID, "vpc-block-public-access-options", acct.ID)
	n, err := st.UpsertResource(&store.Resource{
		Provider:    "aws",
		AccountID:   acct.ID,
		AccountName: &acct.Name,
		Type:        TypeEC2VPCBlockPublicAccessOptions,
		NativeID:    nativeID,
		Region:      &region,
		// Per-account singleton VPC-level public-access config — not user-created.
		AttributesJSON: mustJSON(opt),
		DiscoveredBy:   scanID,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert vpc-block-public-access-options: %w", err)
	}
	return 1, n, nil
}

// scanVPCBlockPublicAccessExclusions has no paginator; uses manual NextToken pagination.
func scanVPCBlockPublicAccessExclusions(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var nextToken *string
	for {
		maxResults := int32(1000)
		out, err := client.DescribeVpcBlockPublicAccessExclusions(ctx, &ec2.DescribeVpcBlockPublicAccessExclusionsInput{
			MaxResults: &maxResults,
			NextToken:  nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "ec2:DescribeVpcBlockPublicAccessExclusions", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("ec2:DescribeVpcBlockPublicAccessExclusions: %w", err)
		}
		var batch []*store.Resource
		for _, excl := range out.VpcBlockPublicAccessExclusions {
			status := string(excl.State)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2VPCBlockPublicAccessExclusion,
				NativeID:       ec2ARN(region, acct.ID, "vpc-block-public-access-exclusion", sv(excl.ExclusionId)),
				Region:         &region,
				Status:         &status,
				TagsJSON:       awsTagsJSON(excl.Tags),
				AttributesJSON: mustJSON(excl),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert vpc-block-public-access-exclusions: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		if out.NextToken == nil {
			return total, inserted, nil
		}
		nextToken = out.NextToken
	}
}

func scanVPCEndpointConnectionNotifications(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeVpcEndpointConnectionNotifications", acct, region, st,
		ec2.NewDescribeVpcEndpointConnectionNotificationsPaginator(client, &ec2.DescribeVpcEndpointConnectionNotificationsInput{}),
		func(page *ec2.DescribeVpcEndpointConnectionNotificationsOutput) []*store.Resource {
			var out []*store.Resource
			for _, n := range page.ConnectionNotificationSet {
				status := string(n.ConnectionNotificationState)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2VPCEndpointConnectionNotification,
					NativeID:       ec2ARN(region, acct.ID, "vpc-endpoint-connection-notification", sv(n.ConnectionNotificationId)),
					Region:         &region,
					Status:         &status,
					AttributesJSON: mustJSON(n),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanVPCEndpointServices scans services owned by this account.
// DescribeVpcEndpointServices has no paginator (manual NextToken). The owner
// filter restricts to account-owned services — omitted, the API also returns
// AWS-managed ones.
func scanVPCEndpointServices(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	ownerFilter := ec2types.Filter{Name: aws.String("owner"), Values: []string{acct.ID}}
	var nextToken *string
	for {
		out, err := client.DescribeVpcEndpointServices(ctx, &ec2.DescribeVpcEndpointServicesInput{
			Filters:   []ec2types.Filter{ownerFilter},
			NextToken: nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "ec2:DescribeVpcEndpointServices", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("ec2:DescribeVpcEndpointServices: %w", err)
		}
		var batch []*store.Resource
		for _, svc := range out.ServiceDetails {
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2VPCEndpointService,
				NativeID:       ec2ARN(region, acct.ID, "vpc-endpoint-service", sv(svc.ServiceId)),
				Name:           svc.ServiceName,
				Region:         &region,
				TagsJSON:       awsTagsJSON(svc.Tags),
				AttributesJSON: mustJSON(svc),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert vpc-endpoint-services: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		if out.NextToken == nil {
			return total, inserted, nil
		}
		nextToken = out.NextToken
	}
}

// scanVPCEndpointServicePermissions fans out per VPC endpoint service.
func scanVPCEndpointServicePermissions(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	svcIDs, err := listVPCEndpointServiceIDs(ctx, client, acct, region, st)
	if err != nil {
		return
	}
	if len(svcIDs) == 0 {
		return
	}
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, ctx := errgroup.WithContext(ctx)
	for _, svcID := range svcIDs {
		g.Go(func() error {
			tt, nn, e := ec2PageScan(
				ctx, "ec2:DescribeVpcEndpointServicePermissions", acct, region, st,
				ec2.NewDescribeVpcEndpointServicePermissionsPaginator(client, &ec2.DescribeVpcEndpointServicePermissionsInput{
					ServiceId: &svcID,
				}),
				func(page *ec2.DescribeVpcEndpointServicePermissionsOutput) []*store.Resource {
					var out []*store.Resource
					for _, perm := range page.AllowedPrincipals {
						nativeID := ec2ARN(region, acct.ID, "vpc-endpoint-service-permission",
							svcID+"/"+sv(perm.Principal))
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeEC2VPCEndpointServicePermissions,
							NativeID:       nativeID,
							Region:         &region,
							AttributesJSON: mustJSON(perm),
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

// listVPCEndpointServiceIDs returns all VPC endpoint service IDs owned by this
// account (manual NextToken pagination — no SDK paginator). The owner filter
// is required: without it, the API also returns AWS-managed services, which
// reject permission queries.
func listVPCEndpointServiceIDs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store) ([]string, error) {
	ownerFilter := ec2types.Filter{Name: aws.String("owner"), Values: []string{acct.ID}}
	var ids []string
	var nextToken *string
	for {
		page, err := client.DescribeVpcEndpointServices(ctx, &ec2.DescribeVpcEndpointServicesInput{
			Filters:   []ec2types.Filter{ownerFilter},
			NextToken: nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "ec2:DescribeVpcEndpointServices", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("ec2:DescribeVpcEndpointServices (list IDs): %w", err)
		}
		for _, svc := range page.ServiceDetails {
			if svc.ServiceId != nil {
				ids = append(ids, *svc.ServiceId)
			}
		}
		if page.NextToken == nil {
			return ids, nil
		}
		nextToken = page.NextToken
	}
}

func scanNetworkInterfacePermissions(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeNetworkInterfacePermissions", acct, region, st,
		ec2.NewDescribeNetworkInterfacePermissionsPaginator(client, &ec2.DescribeNetworkInterfacePermissionsInput{}),
		func(page *ec2.DescribeNetworkInterfacePermissionsOutput) []*store.Resource {
			var out []*store.Resource
			for _, perm := range page.NetworkInterfacePermissions {
				var status string
				if perm.PermissionState != nil {
					status = string(perm.PermissionState.State)
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2NetworkInterfacePermission,
					NativeID:       ec2ARN(region, acct.ID, "network-interface-permission", sv(perm.NetworkInterfacePermissionId)),
					Region:         &region,
					Status:         &status,
					AttributesJSON: mustJSON(perm),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
