package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/sync/errgroup"
)

// scanEC2ClientVPN discovers all Client VPN resources in parallel.
func scanEC2ClientVPN(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return scanClientVPNEndpoints(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanClientVPNAuthorizationRules(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanClientVPNRoutes(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanClientVPNTargetNetworkAssociations(ctx, client, acct, region, st, scanID) })
	return g.Wait()
}

func scanClientVPNEndpoints(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	return ec2PageScan(ctx, "ec2:DescribeClientVpnEndpoints", acct, region, st,
		ec2.NewDescribeClientVpnEndpointsPaginator(client, &ec2.DescribeClientVpnEndpointsInput{}),
		func(page *ec2.DescribeClientVpnEndpointsOutput) []*store.Resource {
			var out []*store.Resource
			for _, ep := range page.ClientVpnEndpoints {
				var status string
				if ep.Status != nil {
					status = string(ep.Status.Code)
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2ClientVPNEndpoint,
					NativeID:       ec2ARN(region, acct.ID, "client-vpn-endpoint", sv(ep.ClientVpnEndpointId)),
					Region:         &region,
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

// scanClientVPNAuthorizationRules fans out per Client VPN endpoint.
func scanClientVPNAuthorizationRules(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	endpointIDs, err := listClientVPNEndpointIDs(ctx, client, acct, region)
	if err != nil {
		return err
	}
	if len(endpointIDs) == 0 {
		return nil
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, epID := range endpointIDs {
		epID := epID
		g.Go(func() error {
			return ec2PageScan(ctx, "ec2:DescribeClientVpnAuthorizationRules", acct, region, st,
				ec2.NewDescribeClientVpnAuthorizationRulesPaginator(client, &ec2.DescribeClientVpnAuthorizationRulesInput{
					ClientVpnEndpointId: &epID,
				}),
				func(page *ec2.DescribeClientVpnAuthorizationRulesOutput) []*store.Resource {
					var out []*store.Resource
					for _, rule := range page.AuthorizationRules {
						nativeID := ec2ARN(region, acct.ID, "client-vpn-auth-rule",
							epID+"/"+sv(rule.DestinationCidr)+"/"+sv(rule.GroupId))
						var status string
						if rule.Status != nil {
							status = string(rule.Status.Code)
						}
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeEC2ClientVPNAuthorizationRule,
							NativeID:       nativeID,
							Region:         &region,
							Status:         &status,
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

// scanClientVPNRoutes fans out per Client VPN endpoint.
func scanClientVPNRoutes(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	endpointIDs, err := listClientVPNEndpointIDs(ctx, client, acct, region)
	if err != nil {
		return err
	}
	if len(endpointIDs) == 0 {
		return nil
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, epID := range endpointIDs {
		epID := epID
		g.Go(func() error {
			return ec2PageScan(ctx, "ec2:DescribeClientVpnRoutes", acct, region, st,
				ec2.NewDescribeClientVpnRoutesPaginator(client, &ec2.DescribeClientVpnRoutesInput{
					ClientVpnEndpointId: &epID,
				}),
				func(page *ec2.DescribeClientVpnRoutesOutput) []*store.Resource {
					var out []*store.Resource
					for _, route := range page.Routes {
						nativeID := ec2ARN(region, acct.ID, "client-vpn-route",
							epID+"/"+sv(route.DestinationCidr)+"/"+sv(route.TargetSubnet))
						var status string
						if route.Status != nil {
							status = string(route.Status.Code)
						}
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeEC2ClientVPNRoute,
							NativeID:       nativeID,
							Region:         &region,
							Status:         &status,
							AttributesJSON: mustJSON(route),
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

// scanClientVPNTargetNetworkAssociations fans out per Client VPN endpoint.
func scanClientVPNTargetNetworkAssociations(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	endpointIDs, err := listClientVPNEndpointIDs(ctx, client, acct, region)
	if err != nil {
		return err
	}
	if len(endpointIDs) == 0 {
		return nil
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, epID := range endpointIDs {
		epID := epID
		g.Go(func() error {
			return ec2PageScan(ctx, "ec2:DescribeClientVpnTargetNetworks", acct, region, st,
				ec2.NewDescribeClientVpnTargetNetworksPaginator(client, &ec2.DescribeClientVpnTargetNetworksInput{
					ClientVpnEndpointId: &epID,
				}),
				func(page *ec2.DescribeClientVpnTargetNetworksOutput) []*store.Resource {
					var out []*store.Resource
					for _, assoc := range page.ClientVpnTargetNetworks {
						var status string
						if assoc.Status != nil {
							status = string(assoc.Status.Code)
						}
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeEC2ClientVPNTargetNetworkAssociation,
							NativeID:       ec2ARN(region, acct.ID, "client-vpn-target-network", sv(assoc.AssociationId)),
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

// listClientVPNEndpointIDs returns all Client VPN endpoint IDs in this region.
func listClientVPNEndpointIDs(ctx context.Context, client *ec2.Client, acct *account, region string) ([]string, error) {
	var ids []string
	pager := ec2.NewDescribeClientVpnEndpointsPaginator(client, &ec2.DescribeClientVpnEndpointsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied("ec2:DescribeClientVpnEndpoints", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("ec2:DescribeClientVpnEndpoints (list IDs): %w", err)
		}
		for _, ep := range page.ClientVpnEndpoints {
			if ep.ClientVpnEndpointId != nil {
				ids = append(ids, *ep.ClientVpnEndpointId)
			}
		}
	}
	return ids, nil
}
