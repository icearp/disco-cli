package aws

import (
	"cmp"
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

const (
	ec2VPCBPAExclusionsOp              = "ec2:DescribeVpcBlockPublicAccessExclusions"
	ec2VPCBPAExclusionsPageSize        = 1000 // DescribeVpcBlockPublicAccessExclusionsMaxResults maximum
	ec2VPCEndpointServicesOp           = "ec2:DescribeVpcEndpointServices"
	ec2VPCEndpointServicePermissionsOp = "ec2:DescribeVpcEndpointServicePermissions"
	// The documented MaxResults maximum of the two ops below; the model sets
	// no range.
	ec2VPCEndpointServicesPageSize           = 1000
	ec2VPCEndpointServicePermissionsPageSize = 1000
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2VPC, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2Subnet, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2InternetGateway, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2EgressOnlyIGW, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2NatGateway, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2RouteTable, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2NetworkInterface, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2NetworkInterfacePermission, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2NetworkACL, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2EIP, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2DHCPOptions, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2CarrierGateway, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2VPCEndpoint, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2VPCEndpointService, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2VPCEndpointServicePermissions, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2VPCEndpointConnectionNotification, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2VPCPeeringConnection, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2VPCBlockPublicAccessOptions, Service: "ec2", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeEC2VPCBlockPublicAccessExclusion, Service: "ec2"})
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

func scanSubnets(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeSubnets", acct, region, st,
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

// scanVPCBlockPublicAccessOptions retrieves the VPC block public access
// setting, which is configured PER REGION — DescribeVpcBlockPublicAccessOptions
// is a regional call and its payload carries AwsRegion.
//
// The NativeID therefore carries the region. It used to omit it, on the premise
// that the setting was one-per-account, and that cost more than a wrong count:
// every region upserted the same natural key, so each region's scanner
// version-split the previous one and the surviving current row was whichever
// region committed last. An account blocking public access in one region and
// allowing it in another reported only one of the two, nondeterministically.
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
	nativeID := ec2ARN(region, acct.ID, "vpc-block-public-access-options", acct.ID)
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

// scanVPCBlockPublicAccessExclusions stores the VPC Block Public Access
// exclusions, those read before a failed page included (storeTokenList). The
// SDK has no paginator for this op.
func scanVPCBlockPublicAccessExclusions(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ec2VPCBPAExclusionsOp,
		func(ctx context.Context, token *string) ([]ec2types.VpcBlockPublicAccessExclusion, *string, error) {
			out, err := client.DescribeVpcBlockPublicAccessExclusions(ctx, &ec2.DescribeVpcBlockPublicAccessExclusionsInput{
				MaxResults: aws.Int32(ec2VPCBPAExclusionsPageSize),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.VpcBlockPublicAccessExclusions, out.NextToken, nil
		},
		func(excl ec2types.VpcBlockPublicAccessExclusion) (*store.Resource, string, string) {
			id := sv(excl.ExclusionId)
			if id == "" {
				return nil, "exclusions have no ExclusionId", cmp.Or(sv(excl.ResourceArn), "one without a ResourceArn")
			}
			status := string(excl.State)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2VPCBlockPublicAccessExclusion,
				NativeID:       ec2ARN(region, acct.ID, "vpc-block-public-access-exclusion", id),
				Region:         &region,
				Status:         &status,
				TagsJSON:       awsTagsJSON(excl.Tags),
				AttributesJSON: mustJSON(excl),
				DiscoveredBy:   scanID,
			}, "", ""
		},
	)
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

// scanVPCEndpointServices stores the VPC endpoint services this account owns,
// those read before a failed page included (storeTokenList). The owner filter
// keeps out the AWS-managed services the API otherwise returns. The SDK has no
// paginator for this op.
func scanVPCEndpointServices(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	ownerFilter := ec2types.Filter{Name: aws.String("owner"), Values: []string{acct.ID}}
	return storeTokenList(ctx, st, acct, region, ec2VPCEndpointServicesOp,
		func(ctx context.Context, token *string) ([]ec2types.ServiceDetail, *string, error) {
			out, err := client.DescribeVpcEndpointServices(ctx, &ec2.DescribeVpcEndpointServicesInput{
				Filters:    []ec2types.Filter{ownerFilter},
				MaxResults: aws.Int32(ec2VPCEndpointServicesPageSize),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ServiceDetails, out.NextToken, nil
		},
		func(svc ec2types.ServiceDetail) (*store.Resource, string, string) {
			id := sv(svc.ServiceId)
			if id == "" {
				return nil, "services have no ServiceId", cmp.Or(sv(svc.ServiceName), "one without a ServiceName")
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2VPCEndpointService,
				NativeID:       ec2ARN(region, acct.ID, "vpc-endpoint-service", id),
				Name:           svc.ServiceName,
				Region:         &region,
				TagsJSON:       awsTagsJSON(svc.Tags),
				AttributesJSON: mustJSON(svc),
				DiscoveredBy:   scanID,
			}, "", ""
		},
	)
}

// scanVPCEndpointServicePermissions stores the principals allowed to connect
// to each VPC endpoint service this scan stored in region (only services this
// account owns are stored). One service's failure stops no other
// (storeTokenEachParent). A service deleted since it was listed fails the
// phase: an EC2 parent may be skipped only with a warning, and
// storeTokenEachParent's notFound skips one silently when nothing was read
// from it.
func scanVPCEndpointServicePermissions(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	services, err := ec2StoredParents(st, acct, region, scanID, TypeEC2VPCEndpointService, "vpc-endpoint-service")
	if err != nil {
		return 0, 0, err
	}
	svcIDs := make([]string, len(services))
	for i, p := range services {
		svcIDs[i] = p.id
	}
	return storeTokenEachParent(ctx, st, acct, region, ec2VPCEndpointServicePermissionsOp, nil, svcIDs,
		func(ctx context.Context, svcID string, token *string) ([]ec2types.AllowedPrincipal, *string, error) {
			out, err := client.DescribeVpcEndpointServicePermissions(ctx, &ec2.DescribeVpcEndpointServicePermissionsInput{
				ServiceId:  &svcID,
				MaxResults: aws.Int32(ec2VPCEndpointServicePermissionsPageSize),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.AllowedPrincipals, out.NextToken, nil
		},
		func(svcID string, perm ec2types.AllowedPrincipal) (*store.Resource, string, string) {
			principal := sv(perm.Principal)
			if principal == "" {
				return nil, "permissions have no Principal", cmp.Or(sv(perm.ServicePermissionId), "one of service "+svcID)
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2VPCEndpointServicePermissions,
				NativeID:       ec2ARN(region, acct.ID, "vpc-endpoint-service-permission", svcID+"/"+principal),
				Region:         &region,
				TagsJSON:       awsTagsJSON(perm.Tags),
				AttributesJSON: mustJSON(perm),
				DiscoveredBy:   scanID,
			}, "", ""
		},
	)
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
