package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"golang.org/x/sync/errgroup"
)

// scanEC2TGWExt discovers all Transit Gateway extension resources in parallel.
func scanEC2TGWExt(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return scanTGWConnects(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTGWConnectPeers(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTGWMulticastDomains(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTGWMulticastDomainAssociations(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTGWMulticastGroupMembers(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTGWMulticastGroupSources(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTGWPeeringAttachments(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTGWRouteTables(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTGWRouteTableAssociations(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTGWRouteTablePropagations(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTGWVPCAttachments(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTGWRoutes(ctx, client, acct, region, st, scanID) })
	return g.Wait()
}

func scanTGWConnects(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeTransitGatewayConnects", acct, region, st,
		ec2.NewDescribeTransitGatewayConnectsPaginator(client, &ec2.DescribeTransitGatewayConnectsInput{}),
		func(page *ec2.DescribeTransitGatewayConnectsOutput) []*store.Resource {
			var out []*store.Resource
			for _, conn := range page.TransitGatewayConnects {
				status := string(conn.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TransitGatewayConnect,
					NativeID:       ec2ARN(region, acct.ID, "transit-gateway-connect", sv(conn.TransitGatewayAttachmentId)),
					Region:         &region,
					CreatedAt:      tp(conn.CreationTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(conn.Tags),
					AttributesJSON: mustJSON(conn),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanTGWConnectPeers(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeTransitGatewayConnectPeers", acct, region, st,
		ec2.NewDescribeTransitGatewayConnectPeersPaginator(client, &ec2.DescribeTransitGatewayConnectPeersInput{}),
		func(page *ec2.DescribeTransitGatewayConnectPeersOutput) []*store.Resource {
			var out []*store.Resource
			for _, peer := range page.TransitGatewayConnectPeers {
				status := string(peer.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TransitGatewayConnectPeer,
					NativeID:       ec2ARN(region, acct.ID, "transit-gateway-connect-peer", sv(peer.TransitGatewayConnectPeerId)),
					Region:         &region,
					CreatedAt:      tp(peer.CreationTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(peer.Tags),
					AttributesJSON: mustJSON(peer),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanTGWMulticastDomains(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeTransitGatewayMulticastDomains", acct, region, st,
		ec2.NewDescribeTransitGatewayMulticastDomainsPaginator(client, &ec2.DescribeTransitGatewayMulticastDomainsInput{}),
		func(page *ec2.DescribeTransitGatewayMulticastDomainsOutput) []*store.Resource {
			var out []*store.Resource
			for _, d := range page.TransitGatewayMulticastDomains {
				status := string(d.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TransitGatewayMulticastDomain,
					NativeID:       sv(d.TransitGatewayMulticastDomainArn),
					Name:           ec2TagName(d.Tags),
					Region:         &region,
					CreatedAt:      tp(d.CreationTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(d.Tags),
					AttributesJSON: mustJSON(d),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanTGWMulticastDomainAssociations fans out per multicast domain.
func scanTGWMulticastDomainAssociations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	domainIDs, err := listTGWMulticastDomainIDs(ctx, client, acct, region)
	if err != nil {
		return err
	}
	if len(domainIDs) == 0 {
		return nil
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, domainID := range domainIDs {
		domainID := domainID
		g.Go(func() error {
			return ec2PageScan(ctx, "ec2:GetTransitGatewayMulticastDomainAssociations", acct, region, st,
				ec2.NewGetTransitGatewayMulticastDomainAssociationsPaginator(client, &ec2.GetTransitGatewayMulticastDomainAssociationsInput{
					TransitGatewayMulticastDomainId: &domainID,
				}),
				func(page *ec2.GetTransitGatewayMulticastDomainAssociationsOutput) []*store.Resource {
					var out []*store.Resource
					for _, assoc := range page.MulticastDomainAssociations {
						nativeID := ec2ARN(region, acct.ID, "tgw-mcast-domain-assoc",
							domainID+"/"+sv(assoc.TransitGatewayAttachmentId)+"/"+sv(assoc.Subnet.SubnetId))
						status := string(assoc.Subnet.State)
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeEC2TransitGatewayMulticastDomainAssociation,
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
		})
	}
	return g.Wait()
}

// scanTGWMulticastGroupMembers fans out per multicast domain, filtering for member entries.
func scanTGWMulticastGroupMembers(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return scanTGWMulticastGroups(ctx, client, acct, region, st, scanID, true)
}

// scanTGWMulticastGroupSources fans out per multicast domain, filtering for source entries.
func scanTGWMulticastGroupSources(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return scanTGWMulticastGroups(ctx, client, acct, region, st, scanID, false)
}

func scanTGWMulticastGroups(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string, isGroupMember bool) error {
	domainIDs, err := listTGWMulticastDomainIDs(ctx, client, acct, region)
	if err != nil {
		return err
	}
	if len(domainIDs) == 0 {
		return nil
	}
	resourceType := TypeEC2TransitGatewayMulticastGroupSource
	arnSubtype := "tgw-mcast-group-source"
	if isGroupMember {
		resourceType = TypeEC2TransitGatewayMulticastGroupMember
		arnSubtype = "tgw-mcast-group-member"
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, domainID := range domainIDs {
		domainID := domainID
		g.Go(func() error {
			return ec2PageScan(ctx, "ec2:SearchTransitGatewayMulticastGroups", acct, region, st,
				ec2.NewSearchTransitGatewayMulticastGroupsPaginator(client, &ec2.SearchTransitGatewayMulticastGroupsInput{
					TransitGatewayMulticastDomainId: &domainID,
				}),
				func(page *ec2.SearchTransitGatewayMulticastGroupsOutput) []*store.Resource {
					var out []*store.Resource
					for _, g := range page.MulticastGroups {
						// Filter: members have GroupMember=true, sources have GroupSource=true.
						isMember := g.GroupMember != nil && *g.GroupMember
						if isGroupMember != isMember {
							continue
						}
						nativeID := ec2ARN(region, acct.ID, arnSubtype,
							domainID+"/"+sv(g.GroupIpAddress)+"/"+sv(g.NetworkInterfaceId))
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           resourceType,
							NativeID:       nativeID,
							Region:         &region,
							AttributesJSON: mustJSON(g),
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

func scanTGWPeeringAttachments(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeTransitGatewayPeeringAttachments", acct, region, st,
		ec2.NewDescribeTransitGatewayPeeringAttachmentsPaginator(client, &ec2.DescribeTransitGatewayPeeringAttachmentsInput{}),
		func(page *ec2.DescribeTransitGatewayPeeringAttachmentsOutput) []*store.Resource {
			var out []*store.Resource
			for _, att := range page.TransitGatewayPeeringAttachments {
				status := string(att.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TransitGatewayPeeringAttachment,
					NativeID:       ec2ARN(region, acct.ID, "transit-gateway-peering-attachment", sv(att.TransitGatewayAttachmentId)),
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

func scanTGWRouteTables(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeTransitGatewayRouteTables", acct, region, st,
		ec2.NewDescribeTransitGatewayRouteTablesPaginator(client, &ec2.DescribeTransitGatewayRouteTablesInput{}),
		func(page *ec2.DescribeTransitGatewayRouteTablesOutput) []*store.Resource {
			var out []*store.Resource
			for _, rt := range page.TransitGatewayRouteTables {
				status := string(rt.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TransitGatewayRouteTable,
					NativeID:       ec2ARN(region, acct.ID, "transit-gateway-route-table", sv(rt.TransitGatewayRouteTableId)),
					Name:           ec2TagName(rt.Tags),
					Region:         &region,
					CreatedAt:      tp(rt.CreationTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(rt.Tags),
					AttributesJSON: mustJSON(rt),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanTGWRouteTableAssociations fans out per TGW route table.
func scanTGWRouteTableAssociations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	rtIDs, err := listTGWRouteTableIDs(ctx, client, acct, region)
	if err != nil {
		return err
	}
	if len(rtIDs) == 0 {
		return nil
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, rtID := range rtIDs {
		rtID := rtID
		g.Go(func() error {
			return ec2PageScan(ctx, "ec2:GetTransitGatewayRouteTableAssociations", acct, region, st,
				ec2.NewGetTransitGatewayRouteTableAssociationsPaginator(client, &ec2.GetTransitGatewayRouteTableAssociationsInput{
					TransitGatewayRouteTableId: &rtID,
				}),
				func(page *ec2.GetTransitGatewayRouteTableAssociationsOutput) []*store.Resource {
					var out []*store.Resource
					for _, assoc := range page.Associations {
						nativeID := ec2ARN(region, acct.ID, "tgw-rtb-assoc", rtID+"/"+sv(assoc.TransitGatewayAttachmentId))
						status := string(assoc.State)
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeEC2TransitGatewayRouteTableAssociation,
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
		})
	}
	return g.Wait()
}

// scanTGWRouteTablePropagations fans out per TGW route table.
func scanTGWRouteTablePropagations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	rtIDs, err := listTGWRouteTableIDs(ctx, client, acct, region)
	if err != nil {
		return err
	}
	if len(rtIDs) == 0 {
		return nil
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, rtID := range rtIDs {
		rtID := rtID
		g.Go(func() error {
			return ec2PageScan(ctx, "ec2:GetTransitGatewayRouteTablePropagations", acct, region, st,
				ec2.NewGetTransitGatewayRouteTablePropagationsPaginator(client, &ec2.GetTransitGatewayRouteTablePropagationsInput{
					TransitGatewayRouteTableId: &rtID,
				}),
				func(page *ec2.GetTransitGatewayRouteTablePropagationsOutput) []*store.Resource {
					var out []*store.Resource
					for _, prop := range page.TransitGatewayRouteTablePropagations {
						nativeID := ec2ARN(region, acct.ID, "tgw-rtb-prop", rtID+"/"+sv(prop.TransitGatewayAttachmentId))
						status := string(prop.State)
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeEC2TransitGatewayRouteTablePropagation,
							NativeID:       nativeID,
							Region:         &region,
							Status:         &status,
							AttributesJSON: mustJSON(prop),
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

func scanTGWVPCAttachments(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeTransitGatewayVpcAttachments", acct, region, st,
		ec2.NewDescribeTransitGatewayVpcAttachmentsPaginator(client, &ec2.DescribeTransitGatewayVpcAttachmentsInput{}),
		func(page *ec2.DescribeTransitGatewayVpcAttachmentsOutput) []*store.Resource {
			var out []*store.Resource
			for _, att := range page.TransitGatewayVpcAttachments {
				status := string(att.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TransitGatewayVPCAttachment,
					NativeID:       ec2ARN(region, acct.ID, "transit-gateway-vpc-attachment", sv(att.TransitGatewayAttachmentId)),
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

// scanTGWRoutes fans out per TGW route table and emits each route as a resource.
// SearchTransitGatewayRoutes does not have a paginator; we call it directly per table.
func scanTGWRoutes(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	rtIDs, err := listTGWRouteTableIDs(ctx, client, acct, region)
	if err != nil {
		return err
	}
	if len(rtIDs) == 0 {
		return nil
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, rtID := range rtIDs {
		rtID := rtID
		g.Go(func() error {
			// SearchTransitGatewayRoutes: returns up to 1000 routes; no pagination available.
			// Use a broad filter to match all states.
			stateFilter := "state"
			out, err := client.SearchTransitGatewayRoutes(ctx, &ec2.SearchTransitGatewayRoutesInput{
				TransitGatewayRouteTableId: &rtID,
				Filters: []ec2types.Filter{
					{Name: &stateFilter, Values: []string{"active", "blackhole", "deleted", "deleting", "pending"}},
				},
			})
			if err != nil {
				if isAccessDenied(err) {
					return skipIfAccessDenied("ec2:SearchTransitGatewayRoutes", acct.ID, region, err)
				}
				return fmt.Errorf("ec2:SearchTransitGatewayRoutes: %w", err)
			}
			var batch []*store.Resource
			for _, route := range out.Routes {
				dest := sv(route.DestinationCidrBlock)
				if dest == "" {
					continue // skip routes with no CIDR destination
				}
				nativeID := ec2ARN(region, acct.ID, "transit-gateway-route", rtID+"/"+dest)
				status := string(route.State)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TransitGatewayRoute,
					NativeID:       nativeID,
					Region:         &region,
					Status:         &status,
					AttributesJSON: mustJSON(route),
					DiscoveredBy:   scanID,
				})
			}
			if len(batch) > 0 {
				if err := st.UpsertResources(batch); err != nil {
					return fmt.Errorf("upsert tgw-routes: %w", err)
				}
			}
			return nil
		})
	}
	return g.Wait()
}

// listTGWMulticastDomainIDs returns all TGW multicast domain IDs in this region.
func listTGWMulticastDomainIDs(ctx context.Context, client *ec2.Client, acct *account, region string) ([]string, error) {
	var ids []string
	pager := ec2.NewDescribeTransitGatewayMulticastDomainsPaginator(client, &ec2.DescribeTransitGatewayMulticastDomainsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied("ec2:DescribeTransitGatewayMulticastDomains", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("ec2:DescribeTransitGatewayMulticastDomains (list IDs): %w", err)
		}
		for _, d := range page.TransitGatewayMulticastDomains {
			if d.TransitGatewayMulticastDomainId != nil {
				ids = append(ids, *d.TransitGatewayMulticastDomainId)
			}
		}
	}
	return ids, nil
}

// listTGWRouteTableIDs returns all TGW route table IDs in this region.
func listTGWRouteTableIDs(ctx context.Context, client *ec2.Client, acct *account, region string) ([]string, error) {
	var ids []string
	pager := ec2.NewDescribeTransitGatewayRouteTablesPaginator(client, &ec2.DescribeTransitGatewayRouteTablesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied("ec2:DescribeTransitGatewayRouteTables", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("ec2:DescribeTransitGatewayRouteTables (list IDs): %w", err)
		}
		for _, rt := range page.TransitGatewayRouteTables {
			if rt.TransitGatewayRouteTableId != nil {
				ids = append(ids, *rt.TransitGatewayRouteTableId)
			}
		}
	}
	return ids, nil
}
