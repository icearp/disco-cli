package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2TransitGatewayPolicyTable, Service: "ec2", Upstream: "AWS::ec2::transit-gateway-policy-table"})
	registerType(restype.Descriptor{Type: TypeEC2TransitGatewayRouteTableAnnouncement, Service: "ec2", Upstream: "AWS::ec2::transit-gateway-route-table-announcement"})
}

// scanEC2TGWExtra discovers Transit Gateway policy tables and route-table
// announcements (the cross-region peering route advertisements).
func scanEC2TGWExtra(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanTGWPolicyTables(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanTGWRouteTableAnnouncements(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanTGWPolicyTables(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeTransitGatewayPolicyTables", acct, region, st,
		ec2.NewDescribeTransitGatewayPolicyTablesPaginator(client, &ec2.DescribeTransitGatewayPolicyTablesInput{}),
		func(page *ec2.DescribeTransitGatewayPolicyTablesOutput) []*store.Resource {
			var out []*store.Resource
			for _, pt := range page.TransitGatewayPolicyTables {
				id := sv(pt.TransitGatewayPolicyTableId)
				if id == "" {
					continue
				}
				status := string(pt.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TransitGatewayPolicyTable,
					NativeID:       ec2ARN(region, acct.ID, "transit-gateway-policy-table", id),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(pt.Tags),
					AttributesJSON: mustJSON(pt),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanTGWRouteTableAnnouncements(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeTransitGatewayRouteTableAnnouncements", acct, region, st,
		ec2.NewDescribeTransitGatewayRouteTableAnnouncementsPaginator(client, &ec2.DescribeTransitGatewayRouteTableAnnouncementsInput{}),
		func(page *ec2.DescribeTransitGatewayRouteTableAnnouncementsOutput) []*store.Resource {
			var out []*store.Resource
			for _, an := range page.TransitGatewayRouteTableAnnouncements {
				id := sv(an.TransitGatewayRouteTableAnnouncementId)
				if id == "" {
					continue
				}
				status := string(an.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TransitGatewayRouteTableAnnouncement,
					NativeID:       ec2ARN(region, acct.ID, "transit-gateway-route-table-announcement", id),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(an.Tags),
					AttributesJSON: mustJSON(an),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
