package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2LocalGatewayRouteTable, Service: "ec2", Upstream: "AWS::EC2::LocalGatewayRouteTable", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2LocalGatewayRoute, Service: "ec2", Upstream: "AWS::EC2::LocalGatewayRoute"})
	registerType(restype.Descriptor{Type: TypeEC2LocalGatewayVirtualInterface, Service: "ec2", Upstream: "AWS::EC2::LocalGatewayVirtualInterface"})
	registerType(restype.Descriptor{Type: TypeEC2LocalGatewayVirtualInterfaceGroup, Service: "ec2", Upstream: "AWS::EC2::LocalGatewayVirtualInterfaceGroup", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEC2LocalGatewayRouteTableVPCAssociation, Service: "ec2", Upstream: "AWS::EC2::LocalGatewayRouteTableVPCAssociation"})
	registerType(restype.Descriptor{Type: TypeEC2LocalGatewayRouteTableVIGAssociation, Service: "ec2", Upstream: "AWS::EC2::LocalGatewayRouteTableVirtualInterfaceGroupAssociation"})
}

// scanEC2LocalGateway discovers all Local Gateway resources in parallel.
func scanEC2LocalGateway(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanLocalGatewayRouteTables(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanLocalGatewayRoutes(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanLocalGatewayVirtualInterfaces(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanLocalGatewayVirtualInterfaceGroups(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanLocalGatewayRouteTableVPCAssociations(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanLocalGatewayRouteTableVIGAssociations(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanLocalGatewayRouteTables(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeLocalGatewayRouteTables", acct, region, st,
		ec2.NewDescribeLocalGatewayRouteTablesPaginator(client, &ec2.DescribeLocalGatewayRouteTablesInput{}),
		func(page *ec2.DescribeLocalGatewayRouteTablesOutput) []*store.Resource {
			var out []*store.Resource
			for _, rt := range page.LocalGatewayRouteTables {
				status := sv(rt.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2LocalGatewayRouteTable,
					NativeID:       sv(rt.LocalGatewayRouteTableArn),
					Region:         &region,
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

// scanLocalGatewayRoutes fans out per route table via SearchLocalGatewayRoutes
// (no standalone DescribeLocalGatewayRoutes).
func scanLocalGatewayRoutes(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	rtIDs, err := listLocalGatewayRouteTableIDs(ctx, client, acct, region, st)
	if err != nil {
		return
	}
	if len(rtIDs) == 0 {
		return
	}
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, ctx := errgroup.WithContext(ctx)
	for _, rtID := range rtIDs {
		g.Go(func() error {
			tt, nn, e := ec2PageScan(
				ctx, "ec2:SearchLocalGatewayRoutes", acct, region, st,
				ec2.NewSearchLocalGatewayRoutesPaginator(client, &ec2.SearchLocalGatewayRoutesInput{
					LocalGatewayRouteTableId: &rtID,
				}),
				func(page *ec2.SearchLocalGatewayRoutesOutput) []*store.Resource {
					var out []*store.Resource
					for _, r := range page.Routes {
						dest := sv(r.DestinationCidrBlock)
						if dest == "" {
							dest = sv(r.DestinationPrefixListId)
						}
						if dest == "" {
							continue
						}
						nativeID := ec2ARN(region, acct.ID, "local-gateway-route", rtID+"/"+dest)
						status := string(r.State)
						out = append(out, &store.Resource{
							Provider:       "aws",
							AccountID:      acct.ID,
							AccountName:    &acct.Name,
							Type:           TypeEC2LocalGatewayRoute,
							NativeID:       nativeID,
							Region:         &region,
							Status:         &status,
							AttributesJSON: mustJSON(r),
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

// listLocalGatewayRouteTableIDs returns all local gateway route table IDs in this region.
func listLocalGatewayRouteTableIDs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store) ([]string, error) {
	var ids []string
	pager := ec2.NewDescribeLocalGatewayRouteTablesPaginator(client, &ec2.DescribeLocalGatewayRouteTablesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "ec2:DescribeLocalGatewayRouteTables", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("ec2:DescribeLocalGatewayRouteTables (list IDs): %w", err)
		}
		for _, rt := range page.LocalGatewayRouteTables {
			if rt.LocalGatewayRouteTableId != nil {
				ids = append(ids, *rt.LocalGatewayRouteTableId)
			}
		}
	}
	return ids, nil
}

func scanLocalGatewayVirtualInterfaces(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeLocalGatewayVirtualInterfaces", acct, region, st,
		ec2.NewDescribeLocalGatewayVirtualInterfacesPaginator(client, &ec2.DescribeLocalGatewayVirtualInterfacesInput{}),
		func(page *ec2.DescribeLocalGatewayVirtualInterfacesOutput) []*store.Resource {
			var out []*store.Resource
			for _, vi := range page.LocalGatewayVirtualInterfaces {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2LocalGatewayVirtualInterface,
					NativeID:       ec2ARN(region, acct.ID, "local-gateway-virtual-interface", sv(vi.LocalGatewayVirtualInterfaceId)),
					Region:         &region,
					TagsJSON:       awsTagsJSON(vi.Tags),
					AttributesJSON: mustJSON(vi),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanLocalGatewayVirtualInterfaceGroups(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeLocalGatewayVirtualInterfaceGroups", acct, region, st,
		ec2.NewDescribeLocalGatewayVirtualInterfaceGroupsPaginator(client, &ec2.DescribeLocalGatewayVirtualInterfaceGroupsInput{}),
		func(page *ec2.DescribeLocalGatewayVirtualInterfaceGroupsOutput) []*store.Resource {
			var out []*store.Resource
			for _, vig := range page.LocalGatewayVirtualInterfaceGroups {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2LocalGatewayVirtualInterfaceGroup,
					NativeID:       ec2ARN(region, acct.ID, "local-gateway-virtual-interface-group", sv(vig.LocalGatewayVirtualInterfaceGroupId)),
					Region:         &region,
					TagsJSON:       awsTagsJSON(vig.Tags),
					AttributesJSON: mustJSON(vig),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanLocalGatewayRouteTableVPCAssociations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeLocalGatewayRouteTableVpcAssociations", acct, region, st,
		ec2.NewDescribeLocalGatewayRouteTableVpcAssociationsPaginator(client, &ec2.DescribeLocalGatewayRouteTableVpcAssociationsInput{}),
		func(page *ec2.DescribeLocalGatewayRouteTableVpcAssociationsOutput) []*store.Resource {
			var out []*store.Resource
			for _, assoc := range page.LocalGatewayRouteTableVpcAssociations {
				status := sv(assoc.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2LocalGatewayRouteTableVPCAssociation,
					NativeID:       ec2ARN(region, acct.ID, "local-gateway-route-table-vpc-assoc", sv(assoc.LocalGatewayRouteTableVpcAssociationId)),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(assoc.Tags),
					AttributesJSON: mustJSON(assoc),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanLocalGatewayRouteTableVIGAssociations(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeLocalGatewayRouteTableVirtualInterfaceGroupAssociations", acct, region, st,
		ec2.NewDescribeLocalGatewayRouteTableVirtualInterfaceGroupAssociationsPaginator(client, &ec2.DescribeLocalGatewayRouteTableVirtualInterfaceGroupAssociationsInput{}),
		func(page *ec2.DescribeLocalGatewayRouteTableVirtualInterfaceGroupAssociationsOutput) []*store.Resource {
			var out []*store.Resource
			for _, assoc := range page.LocalGatewayRouteTableVirtualInterfaceGroupAssociations {
				status := sv(assoc.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2LocalGatewayRouteTableVIGAssociation,
					NativeID:       ec2ARN(region, acct.ID, "local-gateway-route-table-vig-assoc", sv(assoc.LocalGatewayRouteTableVirtualInterfaceGroupAssociationId)),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(assoc.Tags),
					AttributesJSON: mustJSON(assoc),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
