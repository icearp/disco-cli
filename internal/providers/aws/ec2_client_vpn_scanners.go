package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2ClientVPNEndpoint},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2ClientVPNAuthorizationRule},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2ClientVPNRoute},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2ClientVPNTargetNetworkAssociation},
	)
}

// scanEC2ClientVPN discovers all Client VPN resources in parallel.
func scanEC2ClientVPN(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanClientVPNEndpoints(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanClientVPNAuthorizationRules(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanClientVPNRoutes(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanClientVPNTargetNetworkAssociations(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanClientVPNEndpoints(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeClientVpnEndpoints", acct, region, st,
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
func scanClientVPNAuthorizationRules(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	endpointIDs, err := listClientVPNEndpointIDs(ctx, client, acct, region, st)
	if err != nil {
		return
	}
	if len(endpointIDs) == 0 {
		return
	}
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, ctx := errgroup.WithContext(ctx)
	for _, epID := range endpointIDs {
		g.Go(func() error {
			tt, nn, e := ec2PageScan(
				ctx, "ec2:DescribeClientVpnAuthorizationRules", acct, region, st,
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
			add(tt, nn)
			return e
		})
	}
	err = g.Wait()
	return int(t.Load()), int(n.Load()), err
}

// scanClientVPNRoutes fans out per Client VPN endpoint.
func scanClientVPNRoutes(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	endpointIDs, err := listClientVPNEndpointIDs(ctx, client, acct, region, st)
	if err != nil {
		return
	}
	if len(endpointIDs) == 0 {
		return
	}
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, ctx := errgroup.WithContext(ctx)
	for _, epID := range endpointIDs {
		g.Go(func() error {
			tt, nn, e := ec2PageScan(
				ctx, "ec2:DescribeClientVpnRoutes", acct, region, st,
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
			add(tt, nn)
			return e
		})
	}
	err = g.Wait()
	return int(t.Load()), int(n.Load()), err
}

// scanClientVPNTargetNetworkAssociations fans out per Client VPN endpoint.
func scanClientVPNTargetNetworkAssociations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	endpointIDs, err := listClientVPNEndpointIDs(ctx, client, acct, region, st)
	if err != nil {
		return
	}
	if len(endpointIDs) == 0 {
		return
	}
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, ctx := errgroup.WithContext(ctx)
	for _, epID := range endpointIDs {
		g.Go(func() error {
			tt, nn, e := ec2PageScan(
				ctx, "ec2:DescribeClientVpnTargetNetworks", acct, region, st,
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
			add(tt, nn)
			return e
		})
	}
	err = g.Wait()
	return int(t.Load()), int(n.Load()), err
}

// listClientVPNEndpointIDs returns all Client VPN endpoint IDs in this region.
func listClientVPNEndpointIDs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store) ([]string, error) {
	var ids []string
	pager := ec2.NewDescribeClientVpnEndpointsPaginator(client, &ec2.DescribeClientVpnEndpointsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "ec2:DescribeClientVpnEndpoints", acct.ID, region, err)
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
