package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:ec2", fn: scanEC2}) }

// scanEC2 discovers all EC2 resource types in one region, running all
// sub-scanners in parallel via an errgroup.
func scanEC2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := ec2.NewFromConfig(acct.cfg, func(o *ec2.Options) { o.Region = region })

	g, ctx := errgroup.WithContext(ctx)
	// Originally covered types.
	g.Go(func() error { return scanInstances(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCs(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSubnets(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSecurityGroups(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVolumes(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanInternetGateways(ctx, client, acct, region, st, scanID) })
	// Networking extensions.
	g.Go(func() error { return scanNatGateways(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanRouteTables(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEIPs(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanNetworkInterfaces(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanNetworkACLs(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCEndpoints(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCPeeringConnections(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanDHCPOptions(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEgressOnlyIGWs(ctx, client, acct, region, st, scanID) })
	// VPN / transit.
	g.Go(func() error { return scanCustomerGateways(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPNGateways(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPNConnections(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTransitGateways(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTransitGatewayAttachments(ctx, client, acct, region, st, scanID) })
	// Compute management.
	g.Go(func() error { return scanLaunchTemplates(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanKeyPairs(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanPlacementGroups(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSpotFleets(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanHosts(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanCapacityReservations(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanInstanceConnectEndpoints(ctx, client, acct, region, st, scanID) })
	// Observability / policy.
	g.Go(func() error { return scanFlowLogs(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanPrefixLists(ctx, client, acct, region, st, scanID) })
	return g.Wait()
}

// ec2Pager is satisfied by every AWS SDK v2 EC2 paginator.
type ec2Pager[P any] interface {
	HasMorePages() bool
	NextPage(context.Context, ...func(*ec2.Options)) (P, error)
}

// ec2PageScan runs a paginated EC2 Describe call, converts each full page to
// a batch of resources via toResources, and upserts the batch. Access-denied
// errors are handled via skipIfAccessDenied.
func ec2PageScan[P any](
	ctx context.Context,
	iamAction string,
	acct *account,
	region string,
	st *store.Store,
	pager ec2Pager[P],
	toResources func(P) []*store.Resource,
) error {
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied(iamAction, acct.ID, region, err)
			}
			return fmt.Errorf("%s: %w", iamAction, err)
		}
		if batch := toResources(page); len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert %s: %w", iamAction, err)
			}
		}
	}
	return nil
}

func scanInstances(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeInstances", acct, region, st,
		ec2.NewDescribeInstancesPaginator(client, &ec2.DescribeInstancesInput{}),
		func(page *ec2.DescribeInstancesOutput) []*store.Resource {
			var out []*store.Resource
			for _, res := range page.Reservations {
				for _, inst := range res.Instances {
					status := string(inst.State.Name)
					out = append(out, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeEC2Instance,
						NativeID:       ec2ARN(region, acct.ID, "instance", sv(inst.InstanceId)),
						Name:           ec2TagName(inst.Tags),
						Region:         &region,
						Zone:           inst.Placement.AvailabilityZoneId,
						CreatedAt:      tp(inst.LaunchTime),
						Status:         &status,
						TagsJSON:       awsTagsJSON(inst.Tags),
						AttributesJSON: mustJSON(inst),
						DiscoveredBy:   scanID,
					})
				}
			}
			return out
		},
	)
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

func scanSecurityGroups(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeSecurityGroups", acct, region, st,
		ec2.NewDescribeSecurityGroupsPaginator(client, &ec2.DescribeSecurityGroupsInput{}),
		func(page *ec2.DescribeSecurityGroupsOutput) []*store.Resource {
			var out []*store.Resource
			for _, sg := range page.SecurityGroups {
				name := sv(sg.GroupName)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SecurityGroup,
					NativeID:       ec2ARN(region, acct.ID, "security-group", sv(sg.GroupId)),
					Name:           &name,
					Region:         &region,
					TagsJSON:       awsTagsJSON(sg.Tags),
					AttributesJSON: mustJSON(sg),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanVolumes(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeVolumes", acct, region, st,
		ec2.NewDescribeVolumesPaginator(client, &ec2.DescribeVolumesInput{}),
		func(page *ec2.DescribeVolumesOutput) []*store.Resource {
			var out []*store.Resource
			for _, vol := range page.Volumes {
				status := string(vol.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2Volume,
					NativeID:       ec2ARN(region, acct.ID, "volume", sv(vol.VolumeId)),
					Name:           ec2TagName(vol.Tags),
					Region:         &region,
					Zone:           vol.AvailabilityZoneId,
					Status:         &status,
					TagsJSON:       awsTagsJSON(vol.Tags),
					AttributesJSON: mustJSON(vol),
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

// ec2TagName extracts the "Name" tag value, returning nil if absent.
func ec2TagName(tags []ec2types.Tag) *string {
	for _, t := range tags {
		if sv(t.Key) == "Name" && t.Value != nil {
			return t.Value
		}
	}
	return nil
}

// — Networking extensions —

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
	// DescribeAddresses does not have a paginator; all results are returned in one call.
	out, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribeAddresses", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribeAddresses: %w", err)
	}
	var batch []*store.Resource
	for _, addr := range out.Addresses {
		r := &store.Resource{
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
		}
		batch = append(batch, r)
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

// — VPN / transit —

func scanCustomerGateways(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	// DescribeCustomerGateways has no paginator; all results returned in one call.
	out, err := client.DescribeCustomerGateways(ctx, &ec2.DescribeCustomerGatewaysInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribeCustomerGateways", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribeCustomerGateways: %w", err)
	}
	var batch []*store.Resource
	for _, cgw := range out.CustomerGateways {
		status := sv(cgw.State)
		r := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2CustomerGateway,
			NativeID:       ec2ARN(region, acct.ID, "customer-gateway", sv(cgw.CustomerGatewayId)),
			Name:           ec2TagName(cgw.Tags),
			Region:         &region,
			Status:         &status,
			TagsJSON:       awsTagsJSON(cgw.Tags),
			AttributesJSON: mustJSON(cgw),
			DiscoveredBy:   scanID,
		}
		batch = append(batch, r)
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert customer gateways: %w", err)
		}
	}
	return nil
}

func scanVPNGateways(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	// DescribeVpnGateways has no paginator; all results returned in one call.
	out, err := client.DescribeVpnGateways(ctx, &ec2.DescribeVpnGatewaysInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribeVpnGateways", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribeVpnGateways: %w", err)
	}
	var batch []*store.Resource
	for _, vgw := range out.VpnGateways {
		status := string(vgw.State)
		r := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2VPNGateway,
			NativeID:       ec2ARN(region, acct.ID, "vpn-gateway", sv(vgw.VpnGatewayId)),
			Name:           ec2TagName(vgw.Tags),
			Region:         &region,
			Status:         &status,
			TagsJSON:       awsTagsJSON(vgw.Tags),
			AttributesJSON: mustJSON(vgw),
			DiscoveredBy:   scanID,
		}
		batch = append(batch, r)
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert VPN gateways: %w", err)
		}
	}
	return nil
}

func scanVPNConnections(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	// DescribeVpnConnections has no paginator; all results returned in one call.
	out, err := client.DescribeVpnConnections(ctx, &ec2.DescribeVpnConnectionsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribeVpnConnections", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribeVpnConnections: %w", err)
	}
	var batch []*store.Resource
	for _, vpn := range out.VpnConnections {
		status := string(vpn.State)
		r := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2VPNConnection,
			NativeID:       ec2ARN(region, acct.ID, "vpn-connection", sv(vpn.VpnConnectionId)),
			Name:           ec2TagName(vpn.Tags),
			Region:         &region,
			Status:         &status,
			TagsJSON:       awsTagsJSON(vpn.Tags),
			AttributesJSON: mustJSON(vpn),
			DiscoveredBy:   scanID,
		}
		batch = append(batch, r)
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert VPN connections: %w", err)
		}
	}
	return nil
}

func scanTransitGateways(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeTransitGateways", acct, region, st,
		ec2.NewDescribeTransitGatewaysPaginator(client, &ec2.DescribeTransitGatewaysInput{}),
		func(page *ec2.DescribeTransitGatewaysOutput) []*store.Resource {
			var out []*store.Resource
			for _, tgw := range page.TransitGateways {
				status := string(tgw.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TransitGateway,
					NativeID:       sv(tgw.TransitGatewayArn),
					Name:           ec2TagName(tgw.Tags),
					Region:         &region,
					CreatedAt:      tp(tgw.CreationTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(tgw.Tags),
					AttributesJSON: mustJSON(tgw),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanTransitGatewayAttachments(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeTransitGatewayAttachments", acct, region, st,
		ec2.NewDescribeTransitGatewayAttachmentsPaginator(client, &ec2.DescribeTransitGatewayAttachmentsInput{}),
		func(page *ec2.DescribeTransitGatewayAttachmentsOutput) []*store.Resource {
			var out []*store.Resource
			for _, att := range page.TransitGatewayAttachments {
				status := string(att.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TransitGatewayAttachment,
					NativeID:       ec2ARN(region, acct.ID, "transit-gateway-attachment", sv(att.TransitGatewayAttachmentId)),
					Name:           ec2TagName(att.Tags),
					Region:         &region,
					CreatedAt:      tp(att.CreationTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(att.Tags),
					AttributesJSON: mustJSON(att),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// — Compute management —

func scanLaunchTemplates(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeLaunchTemplates", acct, region, st,
		ec2.NewDescribeLaunchTemplatesPaginator(client, &ec2.DescribeLaunchTemplatesInput{}),
		func(page *ec2.DescribeLaunchTemplatesOutput) []*store.Resource {
			var out []*store.Resource
			for _, lt := range page.LaunchTemplates {
				name := sv(lt.LaunchTemplateName)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2LaunchTemplate,
					NativeID:       ec2ARN(region, acct.ID, "launch-template", sv(lt.LaunchTemplateId)),
					Name:           &name,
					Region:         &region,
					CreatedAt:      tp(lt.CreateTime),
					TagsJSON:       awsTagsJSON(lt.Tags),
					AttributesJSON: mustJSON(lt),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanKeyPairs(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	// DescribeKeyPairs has no paginator; all results returned in one call.
	out, err := client.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribeKeyPairs", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribeKeyPairs: %w", err)
	}
	var batch []*store.Resource
	for _, kp := range out.KeyPairs {
		name := sv(kp.KeyName)
		r := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2KeyPair,
			NativeID:       ec2ARN(region, acct.ID, "key-pair", sv(kp.KeyPairId)),
			Name:           &name,
			Region:         &region,
			CreatedAt:      tp(kp.CreateTime),
			TagsJSON:       awsTagsJSON(kp.Tags),
			AttributesJSON: mustJSON(kp),
			DiscoveredBy:   scanID,
		}
		batch = append(batch, r)
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert key pairs: %w", err)
		}
	}
	return nil
}

func scanPlacementGroups(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	// DescribePlacementGroups has no paginator; all results returned in one call.
	out, err := client.DescribePlacementGroups(ctx, &ec2.DescribePlacementGroupsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribePlacementGroups", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribePlacementGroups: %w", err)
	}
	var batch []*store.Resource
	for _, pg := range out.PlacementGroups {
		status := string(pg.State)
		name := sv(pg.GroupName)
		r := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2PlacementGroup,
			NativeID:       ec2ARN(region, acct.ID, "placement-group", sv(pg.GroupId)),
			Name:           &name,
			Region:         &region,
			Status:         &status,
			TagsJSON:       awsTagsJSON(pg.Tags),
			AttributesJSON: mustJSON(pg),
			DiscoveredBy:   scanID,
		}
		batch = append(batch, r)
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert placement groups: %w", err)
		}
	}
	return nil
}

func scanSpotFleets(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeSpotFleetRequests", acct, region, st,
		ec2.NewDescribeSpotFleetRequestsPaginator(client, &ec2.DescribeSpotFleetRequestsInput{}),
		func(page *ec2.DescribeSpotFleetRequestsOutput) []*store.Resource {
			var out []*store.Resource
			for _, sf := range page.SpotFleetRequestConfigs {
				status := string(sf.SpotFleetRequestState)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SpotFleet,
					NativeID:       ec2ARN(region, acct.ID, "spot-fleet-request", sv(sf.SpotFleetRequestId)),
					Region:         &region,
					CreatedAt:      tp(sf.CreateTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(sf.Tags),
					AttributesJSON: mustJSON(sf),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanHosts(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeHosts", acct, region, st,
		ec2.NewDescribeHostsPaginator(client, &ec2.DescribeHostsInput{}),
		func(page *ec2.DescribeHostsOutput) []*store.Resource {
			var out []*store.Resource
			for _, h := range page.Hosts {
				status := string(h.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2Host,
					NativeID:       ec2ARN(region, acct.ID, "dedicated-host", sv(h.HostId)),
					Name:           ec2TagName(h.Tags),
					Region:         &region,
					Zone:           h.AvailabilityZoneId,
					Status:         &status,
					TagsJSON:       awsTagsJSON(h.Tags),
					AttributesJSON: mustJSON(h),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanCapacityReservations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeCapacityReservations", acct, region, st,
		ec2.NewDescribeCapacityReservationsPaginator(client, &ec2.DescribeCapacityReservationsInput{}),
		func(page *ec2.DescribeCapacityReservationsOutput) []*store.Resource {
			var out []*store.Resource
			for _, cr := range page.CapacityReservations {
				status := string(cr.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2CapacityReservation,
					NativeID:       sv(cr.CapacityReservationArn),
					Name:           ec2TagName(cr.Tags),
					Region:         &region,
					Zone:           cr.AvailabilityZoneId,
					CreatedAt:      tp(cr.CreateDate),
					Status:         &status,
					TagsJSON:       awsTagsJSON(cr.Tags),
					AttributesJSON: mustJSON(cr),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanInstanceConnectEndpoints(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeInstanceConnectEndpoints", acct, region, st,
		ec2.NewDescribeInstanceConnectEndpointsPaginator(client, &ec2.DescribeInstanceConnectEndpointsInput{}),
		func(page *ec2.DescribeInstanceConnectEndpointsOutput) []*store.Resource {
			var out []*store.Resource
			for _, ice := range page.InstanceConnectEndpoints {
				status := string(ice.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2InstanceConnectEndpoint,
					NativeID:       sv(ice.InstanceConnectEndpointArn),
					Name:           ec2TagName(ice.Tags),
					Region:         &region,
					CreatedAt:      tp(ice.CreatedAt),
					Status:         &status,
					TagsJSON:       awsTagsJSON(ice.Tags),
					AttributesJSON: mustJSON(ice),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// — Observability / policy —

func scanFlowLogs(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeFlowLogs", acct, region, st,
		ec2.NewDescribeFlowLogsPaginator(client, &ec2.DescribeFlowLogsInput{}),
		func(page *ec2.DescribeFlowLogsOutput) []*store.Resource {
			var out []*store.Resource
			for _, fl := range page.FlowLogs {
				status := sv(fl.FlowLogStatus)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2FlowLog,
					NativeID:       ec2ARN(region, acct.ID, "vpc-flow-log", sv(fl.FlowLogId)),
					Region:         &region,
					CreatedAt:      tp(fl.CreationTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(fl.Tags),
					AttributesJSON: mustJSON(fl),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanPrefixLists(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeManagedPrefixLists", acct, region, st,
		ec2.NewDescribeManagedPrefixListsPaginator(client, &ec2.DescribeManagedPrefixListsInput{}),
		func(page *ec2.DescribeManagedPrefixListsOutput) []*store.Resource {
			var out []*store.Resource
			for _, pl := range page.PrefixLists {
				status := string(pl.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2PrefixList,
					NativeID:       sv(pl.PrefixListArn),
					Name:           pl.PrefixListName,
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(pl.Tags),
					AttributesJSON: mustJSON(pl),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
