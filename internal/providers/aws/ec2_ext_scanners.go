package aws

import (
	"context"
	"fmt"
	"strconv"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"golang.org/x/sync/errgroup"
)

// scanEC2Ext discovers miscellaneous extended EC2 resources and sub-resources
// in parallel. Sub-resources call the same underlying APIs as their parents
// but emit one store.Resource per child entry.
func scanEC2Ext(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	g, ctx := errgroup.WithContext(ctx)
	// Capacity / fleet.
	g.Go(func() error { return scanCapacityReservationFleets(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEC2Fleets(ctx, client, acct, region, st, scanID) })
	// Carrier gateway.
	g.Go(func() error { return scanCarrierGateways(ctx, client, acct, region, st, scanID) })
	// VPC features.
	g.Go(func() error { return scanVPCBlockPublicAccessOptions(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCBlockPublicAccessExclusions(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCEndpointConnectionNotifications(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCEndpointServices(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCEndpointServicePermissions(ctx, client, acct, region, st, scanID) })
	// Security group extensions.
	g.Go(func() error { return scanSecurityGroupRules(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSecurityGroupVPCAssociations(ctx, client, acct, region, st, scanID) })
	// Network interface / misc extensions.
	g.Go(func() error { return scanNetworkInterfacePermissions(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSnapshotBlockPublicAccess(ctx, client, acct, region, st, scanID) })
	// Sub-resources: expand from parent API responses.
	g.Go(func() error { return scanRoutes(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanRouteTableAssociations(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanNetworkACLEntries(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSubnetNetworkACLAssociations(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCCIDRBlocks(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCDHCPOptionsAssociations(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPCGatewayAttachments(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanSubnetCIDRBlocks(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEIPAssociations(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanNetworkInterfaceAttachments(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVolumeAttachments(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPNConnectionRoutes(ctx, client, acct, region, st, scanID) })
	return g.Wait()
}

// — Capacity / fleet —

func scanCapacityReservationFleets(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeCapacityReservationFleets", acct, region, st,
		ec2.NewDescribeCapacityReservationFleetsPaginator(client, &ec2.DescribeCapacityReservationFleetsInput{}),
		func(page *ec2.DescribeCapacityReservationFleetsOutput) []*store.Resource {
			var out []*store.Resource
			for _, fleet := range page.CapacityReservationFleets {
				status := string(fleet.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2CapacityReservationFleet,
					NativeID:       sv(fleet.CapacityReservationFleetArn),
					Region:         &region,
					CreatedAt:      tp(fleet.CreateTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(fleet.Tags),
					AttributesJSON: mustJSON(fleet),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanEC2Fleets(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeFleets", acct, region, st,
		ec2.NewDescribeFleetsPaginator(client, &ec2.DescribeFleetsInput{}),
		func(page *ec2.DescribeFleetsOutput) []*store.Resource {
			var out []*store.Resource
			for _, fleet := range page.Fleets {
				status := string(fleet.FleetState)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2Fleet,
					NativeID:       ec2ARN(region, acct.ID, "fleet", sv(fleet.FleetId)),
					Region:         &region,
					CreatedAt:      tp(fleet.CreateTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(fleet.Tags),
					AttributesJSON: mustJSON(fleet),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// — Carrier gateway —

func scanCarrierGateways(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeCarrierGateways", acct, region, st,
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
}

// — VPC features —

// scanVPCBlockPublicAccessOptions retrieves the account-level VPC block public access
// setting. There is one per account; NativeID omits region for stability across scans.
func scanVPCBlockPublicAccessOptions(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	out, err := client.DescribeVpcBlockPublicAccessOptions(ctx, &ec2.DescribeVpcBlockPublicAccessOptionsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribeVpcBlockPublicAccessOptions", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribeVpcBlockPublicAccessOptions: %w", err)
	}
	if out.VpcBlockPublicAccessOptions == nil {
		return nil
	}
	opt := out.VpcBlockPublicAccessOptions
	// Use account-level NativeID (no region) so the same resource is upserted each region.
	nativeID := ec2ARN("", acct.ID, "vpc-block-public-access-options", acct.ID)
	if err := st.UpsertResource(&store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeEC2VPCBlockPublicAccessOptions,
		NativeID:       nativeID,
		Region:         &region,
		AttributesJSON: mustJSON(opt),
		DiscoveredBy:   scanID,
	}); err != nil {
		return fmt.Errorf("upsert vpc-block-public-access-options: %w", err)
	}
	return nil
}

// scanVPCBlockPublicAccessExclusions has no paginator; uses manual NextToken pagination.
func scanVPCBlockPublicAccessExclusions(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	var nextToken *string
	for {
		maxResults := int32(1000)
		out, err := client.DescribeVpcBlockPublicAccessExclusions(ctx, &ec2.DescribeVpcBlockPublicAccessExclusionsInput{
			MaxResults: &maxResults,
			NextToken:  nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("ec2:DescribeVpcBlockPublicAccessExclusions", acct.ID, region, err)
			}
			return fmt.Errorf("ec2:DescribeVpcBlockPublicAccessExclusions: %w", err)
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
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert vpc-block-public-access-exclusions: %w", err)
			}
		}
		if out.NextToken == nil {
			return nil
		}
		nextToken = out.NextToken
	}
}

func scanVPCEndpointConnectionNotifications(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeVpcEndpointConnectionNotifications", acct, region, st,
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
// DescribeVpcEndpointServices has no paginator; uses manual NextToken pagination.
// The owner filter restricts results to account-owned services; without it the
// API returns AWS-managed services as well.
func scanVPCEndpointServices(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	ownerFilter := ec2types.Filter{Name: aws.String("owner"), Values: []string{acct.ID}}
	var nextToken *string
	for {
		out, err := client.DescribeVpcEndpointServices(ctx, &ec2.DescribeVpcEndpointServicesInput{
			Filters:   []ec2types.Filter{ownerFilter},
			NextToken: nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("ec2:DescribeVpcEndpointServices", acct.ID, region, err)
			}
			return fmt.Errorf("ec2:DescribeVpcEndpointServices: %w", err)
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
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert vpc-endpoint-services: %w", err)
			}
		}
		if out.NextToken == nil {
			return nil
		}
		nextToken = out.NextToken
	}
}

// scanVPCEndpointServicePermissions fans out per VPC endpoint service.
func scanVPCEndpointServicePermissions(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	svcIDs, err := listVPCEndpointServiceIDs(ctx, client, acct, region)
	if err != nil {
		return err
	}
	if len(svcIDs) == 0 {
		return nil
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, svcID := range svcIDs {
		svcID := svcID
		g.Go(func() error {
			return ec2PageScan(ctx, "ec2:DescribeVpcEndpointServicePermissions", acct, region, st,
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
		})
	}
	return g.Wait()
}

// listVPCEndpointServiceIDs returns all VPC endpoint service IDs owned by this account.
// Uses manual NextToken pagination (no paginator available in SDK).
// The owner filter is required; without it the API returns AWS-managed services
// which cannot be queried for permissions.
func listVPCEndpointServiceIDs(ctx context.Context, client *ec2.Client, acct *account, region string) ([]string, error) {
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
				_ = skipIfAccessDenied("ec2:DescribeVpcEndpointServices", acct.ID, region, err)
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

// — Security group extensions —

// scanSecurityGroupRules scans all security group rules and emits one resource
// per rule, typed as ingress or egress based on the IsEgress field.
func scanSecurityGroupRules(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	// Collect all SG IDs to query their rules.
	sgIDs, err := listSecurityGroupIDs(ctx, client, acct, region)
	if err != nil {
		return err
	}
	if len(sgIDs) == 0 {
		return nil
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, sgID := range sgIDs {
		sgID := sgID
		g.Go(func() error {
			filterName := "group-id"
			return ec2PageScan(ctx, "ec2:DescribeSecurityGroupRules", acct, region, st,
				ec2.NewDescribeSecurityGroupRulesPaginator(client, &ec2.DescribeSecurityGroupRulesInput{
					Filters: []ec2types.Filter{{Name: &filterName, Values: []string{sgID}}},
				}),
				func(page *ec2.DescribeSecurityGroupRulesOutput) []*store.Resource {
					var out []*store.Resource
					for _, rule := range page.SecurityGroupRules {
						ruleType := TypeEC2SecurityGroupIngress
						if rule.IsEgress != nil && *rule.IsEgress {
							ruleType = TypeEC2SecurityGroupEgress
						}
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           ruleType,
							NativeID:       ec2ARN(region, acct.ID, "security-group-rule", sv(rule.SecurityGroupRuleId)),
							Region:         &region,
							TagsJSON:       awsTagsJSON(rule.Tags),
							AttributesJSON: mustJSON(rule),
							DiscoveredBy:   scanID,
						})
					}
					return out
				},
			)
		})
	}
	return g.Wait()
}

func scanSecurityGroupVPCAssociations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeSecurityGroupVpcAssociations", acct, region, st,
		ec2.NewDescribeSecurityGroupVpcAssociationsPaginator(client, &ec2.DescribeSecurityGroupVpcAssociationsInput{}),
		func(page *ec2.DescribeSecurityGroupVpcAssociationsOutput) []*store.Resource {
			var out []*store.Resource
			for _, assoc := range page.SecurityGroupVpcAssociations {
				status := string(assoc.State)
				nativeID := ec2ARN(region, acct.ID, "security-group-vpc-assoc",
					sv(assoc.GroupId)+"/"+sv(assoc.VpcId))
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2SecurityGroupVPCAssociation,
					NativeID:       nativeID,
					Region:         &region,
					Status:         &status,
					AttributesJSON: mustJSON(assoc),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// listSecurityGroupIDs returns all security group IDs in this region.
func listSecurityGroupIDs(ctx context.Context, client *ec2.Client, acct *account, region string) ([]string, error) {
	var ids []string
	pager := ec2.NewDescribeSecurityGroupsPaginator(client, &ec2.DescribeSecurityGroupsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied("ec2:DescribeSecurityGroups", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("ec2:DescribeSecurityGroups (list IDs): %w", err)
		}
		for _, sg := range page.SecurityGroups {
			if sg.GroupId != nil {
				ids = append(ids, *sg.GroupId)
			}
		}
	}
	return ids, nil
}

// — Network interface / misc extensions —

func scanNetworkInterfacePermissions(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeNetworkInterfacePermissions", acct, region, st,
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

// scanNetworkPerformanceMetricSubscriptions is intentionally omitted:
// DescribeNetworkPerformanceMetricSubscriptions is not available in
// aws-sdk-go-v2/service/ec2 v1.296.2.

// scanSnapshotBlockPublicAccess retrieves the account-level snapshot block public access
// setting. There is one per account; NativeID omits region for stability.
func scanSnapshotBlockPublicAccess(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	out, err := client.GetSnapshotBlockPublicAccessState(ctx, &ec2.GetSnapshotBlockPublicAccessStateInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:GetSnapshotBlockPublicAccessState", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:GetSnapshotBlockPublicAccessState: %w", err)
	}
	state := string(out.State)
	nativeID := ec2ARN("", acct.ID, "snapshot-block-public-access", acct.ID)
	if err := st.UpsertResource(&store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeEC2SnapshotBlockPublicAccess,
		NativeID:       nativeID,
		Region:         &region,
		Status:         &state,
		AttributesJSON: mustJSON(map[string]string{"state": state}),
		DiscoveredBy:   scanID,
	}); err != nil {
		return fmt.Errorf("upsert snapshot-block-public-access: %w", err)
	}
	return nil
}

// — Sub-resources (expand from parent APIs) —

// scanRoutes calls DescribeRouteTables and emits one resource per route entry.
func scanRoutes(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeRouteTables (routes)", acct, region, st,
		ec2.NewDescribeRouteTablesPaginator(client, &ec2.DescribeRouteTablesInput{}),
		func(page *ec2.DescribeRouteTablesOutput) []*store.Resource {
			var out []*store.Resource
			for _, rt := range page.RouteTables {
				rtID := sv(rt.RouteTableId)
				for _, route := range rt.Routes {
					dest := sv(route.DestinationCidrBlock)
					if dest == "" {
						dest = sv(route.DestinationIpv6CidrBlock)
					}
					if dest == "" {
						dest = sv(route.DestinationPrefixListId)
					}
					if dest == "" {
						continue
					}
					nativeID := ec2ARN(region, acct.ID, "route", rtID+"/"+dest)
					status := string(route.State)
					out = append(out, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeEC2Route,
						NativeID:       nativeID,
						Region:         &region,
						Status:         &status,
						AttributesJSON: mustJSON(route),
						DiscoveredBy:   scanID,
					})
				}
			}
			return out
		},
	)
}

// scanRouteTableAssociations emits subnet and gateway route table associations as
// separate resources. Subnet associations use TypeEC2SubnetRouteTableAssociation;
// gateway associations use TypeEC2GatewayRouteTableAssociation.
func scanRouteTableAssociations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeRouteTables (associations)", acct, region, st,
		ec2.NewDescribeRouteTablesPaginator(client, &ec2.DescribeRouteTablesInput{}),
		func(page *ec2.DescribeRouteTablesOutput) []*store.Resource {
			var out []*store.Resource
			for _, rt := range page.RouteTables {
				for _, assoc := range rt.Associations {
					if assoc.RouteTableAssociationId == nil {
						continue
					}
					rtype := TypeEC2SubnetRouteTableAssociation
					if assoc.GatewayId != nil {
						rtype = TypeEC2GatewayRouteTableAssociation
					}
					nativeID := sv(assoc.RouteTableAssociationId)
					out = append(out, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           rtype,
						NativeID:       nativeID,
						Region:         &region,
						AttributesJSON: mustJSON(assoc),
						DiscoveredBy:   scanID,
					})
				}
			}
			return out
		},
	)
}

// scanNetworkACLEntries emits one resource per NACL entry (rule).
func scanNetworkACLEntries(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeNetworkAcls (entries)", acct, region, st,
		ec2.NewDescribeNetworkAclsPaginator(client, &ec2.DescribeNetworkAclsInput{}),
		func(page *ec2.DescribeNetworkAclsOutput) []*store.Resource {
			var out []*store.Resource
			for _, nacl := range page.NetworkAcls {
				naclID := sv(nacl.NetworkAclId)
				for _, entry := range nacl.Entries {
					direction := "ingress"
					if aws.ToBool(entry.Egress) {
						direction = "egress"
					}
					ruleNum := 0
					if entry.RuleNumber != nil {
						ruleNum = int(*entry.RuleNumber)
					}
					nativeID := ec2ARN(region, acct.ID, "network-acl-entry",
						naclID+"/"+strconv.Itoa(ruleNum)+"/"+direction)
					out = append(out, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeEC2NetworkACLEntry,
						NativeID:       nativeID,
						Region:         &region,
						AttributesJSON: mustJSON(entry),
						DiscoveredBy:   scanID,
					})
				}
			}
			return out
		},
	)
}

// scanSubnetNetworkACLAssociations emits one resource per NACL subnet association.
func scanSubnetNetworkACLAssociations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeNetworkAcls (associations)", acct, region, st,
		ec2.NewDescribeNetworkAclsPaginator(client, &ec2.DescribeNetworkAclsInput{}),
		func(page *ec2.DescribeNetworkAclsOutput) []*store.Resource {
			var out []*store.Resource
			for _, nacl := range page.NetworkAcls {
				for _, assoc := range nacl.Associations {
					if assoc.NetworkAclAssociationId == nil {
						continue
					}
					out = append(out, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeEC2SubnetNetworkACLAssociation,
						NativeID:       sv(assoc.NetworkAclAssociationId),
						Region:         &region,
						AttributesJSON: mustJSON(assoc),
						DiscoveredBy:   scanID,
					})
				}
			}
			return out
		},
	)
}

// scanVPCCIDRBlocks emits one resource per VPC CIDR block association.
func scanVPCCIDRBlocks(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeVpcs (cidr-blocks)", acct, region, st,
		ec2.NewDescribeVpcsPaginator(client, &ec2.DescribeVpcsInput{}),
		func(page *ec2.DescribeVpcsOutput) []*store.Resource {
			var out []*store.Resource
			for _, vpc := range page.Vpcs {
				for _, assoc := range vpc.CidrBlockAssociationSet {
					if assoc.AssociationId == nil {
						continue
					}
					var status string
					if assoc.CidrBlockState != nil {
						status = string(assoc.CidrBlockState.State)
					}
					out = append(out, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeEC2VPCCIDRBlock,
						NativeID:       sv(assoc.AssociationId),
						Region:         &region,
						Status:         &status,
						AttributesJSON: mustJSON(assoc),
						DiscoveredBy:   scanID,
					})
				}
			}
			return out
		},
	)
}

// scanVPCDHCPOptionsAssociations emits one resource per VPC-to-DHCP-options pairing.
func scanVPCDHCPOptionsAssociations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeVpcs (dhcp-options-assoc)", acct, region, st,
		ec2.NewDescribeVpcsPaginator(client, &ec2.DescribeVpcsInput{}),
		func(page *ec2.DescribeVpcsOutput) []*store.Resource {
			var out []*store.Resource
			for _, vpc := range page.Vpcs {
				if vpc.VpcId == nil || vpc.DhcpOptionsId == nil {
					continue
				}
				nativeID := ec2ARN(region, acct.ID, "vpc-dhcp-options-association",
					*vpc.VpcId+"/"+*vpc.DhcpOptionsId)
				out = append(out, &store.Resource{
					Provider:  "aws",
					AccountID: acct.ID,
					AccountName: &acct.Name,
					Type:           TypeEC2VPCDHCPOptionsAssociation,
					NativeID:       nativeID,
					Region:         &region,
					AttributesJSON: mustJSON(map[string]string{"VpcId": *vpc.VpcId, "DhcpOptionsId": *vpc.DhcpOptionsId}),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanVPCGatewayAttachments emits one resource per IGW-to-VPC attachment.
func scanVPCGatewayAttachments(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeInternetGateways (gateway-attachments)", acct, region, st,
		ec2.NewDescribeInternetGatewaysPaginator(client, &ec2.DescribeInternetGatewaysInput{}),
		func(page *ec2.DescribeInternetGatewaysOutput) []*store.Resource {
			var out []*store.Resource
			for _, igw := range page.InternetGateways {
				igwID := sv(igw.InternetGatewayId)
				for _, att := range igw.Attachments {
					if att.VpcId == nil {
						continue
					}
					nativeID := ec2ARN(region, acct.ID, "vpc-gateway-attachment", igwID+"/"+*att.VpcId)
					status := string(att.State)
					out = append(out, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeEC2VPCGatewayAttachment,
						NativeID:       nativeID,
						Region:         &region,
						Status:         &status,
						AttributesJSON: mustJSON(att),
						DiscoveredBy:   scanID,
					})
				}
			}
			return out
		},
	)
}

// scanSubnetCIDRBlocks emits one resource per IPv6 CIDR block association on a subnet.
func scanSubnetCIDRBlocks(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeSubnets (cidr-blocks)", acct, region, st,
		ec2.NewDescribeSubnetsPaginator(client, &ec2.DescribeSubnetsInput{}),
		func(page *ec2.DescribeSubnetsOutput) []*store.Resource {
			var out []*store.Resource
			for _, sn := range page.Subnets {
				for _, assoc := range sn.Ipv6CidrBlockAssociationSet {
					if assoc.AssociationId == nil {
						continue
					}
					var status string
					if assoc.Ipv6CidrBlockState != nil {
						status = string(assoc.Ipv6CidrBlockState.State)
					}
					out = append(out, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeEC2SubnetCIDRBlock,
						NativeID:       sv(assoc.AssociationId),
						Region:         &region,
						Status:         &status,
						AttributesJSON: mustJSON(assoc),
						DiscoveredBy:   scanID,
					})
				}
			}
			return out
		},
	)
}

// scanEIPAssociations emits one resource per EIP that is currently associated.
func scanEIPAssociations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	// DescribeAddresses has no paginator.
	out, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribeAddresses (eip-associations)", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribeAddresses (eip-associations): %w", err)
	}
	var batch []*store.Resource
	for _, addr := range out.Addresses {
		if addr.AssociationId == nil {
			continue // only emit associated EIPs
		}
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2EIPAssociation,
			NativeID:       sv(addr.AssociationId),
			Region:         &region,
			AttributesJSON: mustJSON(addr),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert eip-associations: %w", err)
		}
	}
	return nil
}

// scanNetworkInterfaceAttachments emits one resource per ENI that has an attachment.
func scanNetworkInterfaceAttachments(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeNetworkInterfaces (attachments)", acct, region, st,
		ec2.NewDescribeNetworkInterfacesPaginator(client, &ec2.DescribeNetworkInterfacesInput{}),
		func(page *ec2.DescribeNetworkInterfacesOutput) []*store.Resource {
			var out []*store.Resource
			for _, eni := range page.NetworkInterfaces {
				if eni.Attachment == nil || eni.Attachment.AttachmentId == nil {
					continue
				}
				att := eni.Attachment
				status := string(att.Status)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2NetworkInterfaceAttachment,
					NativeID:       sv(att.AttachmentId),
					Region:         &region,
					Status:         &status,
					AttributesJSON: mustJSON(att),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanVolumeAttachments emits one resource per volume attachment.
func scanVolumeAttachments(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeVolumes (attachments)", acct, region, st,
		ec2.NewDescribeVolumesPaginator(client, &ec2.DescribeVolumesInput{}),
		func(page *ec2.DescribeVolumesOutput) []*store.Resource {
			var out []*store.Resource
			for _, vol := range page.Volumes {
				volID := sv(vol.VolumeId)
				for _, att := range vol.Attachments {
					if att.InstanceId == nil {
						continue
					}
					nativeID := ec2ARN(region, acct.ID, "volume-attachment", volID+"/"+*att.InstanceId)
					status := string(att.State)
					out = append(out, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeEC2VolumeAttachment,
						NativeID:       nativeID,
						Region:         &region,
						Status:         &status,
						AttributesJSON: mustJSON(att),
						DiscoveredBy:   scanID,
					})
				}
			}
			return out
		},
	)
}

// scanVPNConnectionRoutes emits one resource per static route in a VPN connection.
func scanVPNConnectionRoutes(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	// DescribeVpnConnections has no paginator.
	out, err := client.DescribeVpnConnections(ctx, &ec2.DescribeVpnConnectionsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return skipIfAccessDenied("ec2:DescribeVpnConnections (routes)", acct.ID, region, err)
		}
		return fmt.Errorf("ec2:DescribeVpnConnections (routes): %w", err)
	}
	var batch []*store.Resource
	for _, vpn := range out.VpnConnections {
		vpnID := sv(vpn.VpnConnectionId)
		for _, route := range vpn.Routes {
			if route.DestinationCidrBlock == nil {
				continue
			}
			nativeID := ec2ARN(region, acct.ID, "vpn-connection-route", vpnID+"/"+*route.DestinationCidrBlock)
			status := string(route.State)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2VPNConnectionRoute,
				NativeID:       nativeID,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(route),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert vpn-connection-routes: %w", err)
		}
	}
	return nil
}
