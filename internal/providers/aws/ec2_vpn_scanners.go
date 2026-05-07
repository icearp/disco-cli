package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2CustomerGateway},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2VPNGateway},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2VPNConnection},
	)
}

// scanEC2VPN discovers VPN types: customer gateways, VPN gateways, and VPN connections.
func scanEC2VPN(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanCustomerGateways(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPNGateways(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPNConnections(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanCustomerGateways(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// DescribeCustomerGateways has no paginator; all results returned in one call.
	out, err := client.DescribeCustomerGateways(ctx, &ec2.DescribeCustomerGatewaysInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "ec2:DescribeCustomerGateways", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("ec2:DescribeCustomerGateways: %w", err)
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
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert customer gateways: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

func scanVPNGateways(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// DescribeVpnGateways has no paginator; all results returned in one call.
	out, err := client.DescribeVpnGateways(ctx, &ec2.DescribeVpnGatewaysInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "ec2:DescribeVpnGateways", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("ec2:DescribeVpnGateways: %w", err)
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
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert VPN gateways: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

func scanVPNConnections(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// DescribeVpnConnections has no paginator; all results returned in one call.
	out, err := client.DescribeVpnConnections(ctx, &ec2.DescribeVpnConnectionsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "ec2:DescribeVpnConnections", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("ec2:DescribeVpnConnections: %w", err)
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
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert VPN connections: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}
