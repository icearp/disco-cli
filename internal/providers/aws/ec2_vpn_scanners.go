package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/sync/errgroup"
)

// scanEC2VPN discovers VPN and Transit Gateway types: customer gateways, VPN
// gateways, VPN connections, transit gateways, and transit gateway attachments.
func scanEC2VPN(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return scanCustomerGateways(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPNGateways(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanVPNConnections(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTransitGateways(ctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanTransitGatewayAttachments(ctx, client, acct, region, st, scanID) })
	return g.Wait()
}

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
		batch = append(batch, &store.Resource{
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
		})
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
		batch = append(batch, &store.Resource{
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
		})
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
		batch = append(batch, &store.Resource{
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
		})
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
