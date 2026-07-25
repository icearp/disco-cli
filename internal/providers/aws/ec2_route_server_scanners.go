package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2RouteServer, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2RouteServerEndpoint, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2RouteServerPeer, Service: "ec2"})
}

// scanEC2RouteServer discovers route server resources in one region: route
// servers (VPC-level BGP control plane), per-route-server endpoints
// (subnet-bound BGP listeners), and per-endpoint peers (BGP-speaking
// neighbours). RouteServerAssociation + RouteServerPropagation are skipped —
// they're association/propagation fields on already-scanned resources (route
// table, route server), not resources with their own SDK list op (see
// docs/aws-missing-services.md).
func scanEC2RouteServer(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanRouteServers(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanRouteServerEndpoints(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanRouteServerPeers(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanRouteServers(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeRouteServers", acct, region, st,
		ec2.NewDescribeRouteServersPaginator(client, &ec2.DescribeRouteServersInput{}),
		func(page *ec2.DescribeRouteServersOutput) []*store.Resource {
			var out []*store.Resource
			for _, rs := range page.RouteServers {
				id := sv(rs.RouteServerId)
				if id == "" {
					continue
				}
				status := string(rs.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2RouteServer,
					NativeID:       ec2ARN(region, acct.ID, "route-server", id),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(rs.Tags),
					AttributesJSON: mustJSON(rs),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanRouteServerEndpoints(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeRouteServerEndpoints", acct, region, st,
		ec2.NewDescribeRouteServerEndpointsPaginator(client, &ec2.DescribeRouteServerEndpointsInput{}),
		func(page *ec2.DescribeRouteServerEndpointsOutput) []*store.Resource {
			var out []*store.Resource
			for _, e := range page.RouteServerEndpoints {
				id := sv(e.RouteServerEndpointId)
				if id == "" {
					continue
				}
				status := string(e.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2RouteServerEndpoint,
					NativeID:       ec2ARN(region, acct.ID, "route-server-endpoint", id),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(e.Tags),
					AttributesJSON: mustJSON(e),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanRouteServerPeers(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeRouteServerPeers", acct, region, st,
		ec2.NewDescribeRouteServerPeersPaginator(client, &ec2.DescribeRouteServerPeersInput{}),
		func(page *ec2.DescribeRouteServerPeersOutput) []*store.Resource {
			var out []*store.Resource
			for _, p := range page.RouteServerPeers {
				id := sv(p.RouteServerPeerId)
				if id == "" {
					continue
				}
				status := string(p.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2RouteServerPeer,
					NativeID:       ec2ARN(region, acct.ID, "route-server-peer", id),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(p.Tags),
					AttributesJSON: mustJSON(p),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
