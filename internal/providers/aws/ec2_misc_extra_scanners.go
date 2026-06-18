package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2CapacityManagerDataExport, Leaf: true},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2NetworkPerformanceMetricSubscription, Leaf: true},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2TransitGatewayMeteringPolicy},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2VPCEncryptionControl, Leaf: true},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2VPNConcentrator, Leaf: true},
	)
}

// scanEC2MiscExtra discovers small EC2 resource families that do not warrant
// their own scanner file: CapacityManager data exports, AWS Network
// Performance metric subscriptions, TransitGateway metering policies,
// VPC encryption controls, VPN concentrators. None carry native ARN
// fields on the SDK summary — synthesize via ec2ARN.
func scanEC2MiscExtra(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanCapacityManagerDataExports(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanNetworkPerformanceMetricSubscriptions(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanTransitGatewayMeteringPolicies(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPCEncryptionControls(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanVPNConcentrators(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanCapacityManagerDataExports(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeCapacityManagerDataExports", acct, region, st,
		ec2.NewDescribeCapacityManagerDataExportsPaginator(client, &ec2.DescribeCapacityManagerDataExportsInput{}),
		func(page *ec2.DescribeCapacityManagerDataExportsOutput) []*store.Resource {
			var out []*store.Resource
			for _, e := range page.CapacityManagerDataExports {
				id := sv(e.CapacityManagerDataExportId)
				if id == "" {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2CapacityManagerDataExport,
					NativeID:       ec2ARN(region, acct.ID, "capacity-manager-data-export", id),
					Region:         &region,
					AttributesJSON: mustJSON(e),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanNetworkPerformanceMetricSubscriptions — Subscription has no ID field
// on the SDK type; key NativeID by composite (Source, Destination, Metric).
func scanNetworkPerformanceMetricSubscriptions(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeAwsNetworkPerformanceMetricSubscriptions", acct, region, st,
		ec2.NewDescribeAwsNetworkPerformanceMetricSubscriptionsPaginator(client, &ec2.DescribeAwsNetworkPerformanceMetricSubscriptionsInput{}),
		func(page *ec2.DescribeAwsNetworkPerformanceMetricSubscriptionsOutput) []*store.Resource {
			var out []*store.Resource
			for _, s := range page.Subscriptions {
				src := sv(s.Source)
				dst := sv(s.Destination)
				if src == "" || dst == "" {
					continue
				}
				key := fmt.Sprintf("%s/%s/%s", src, dst, s.Metric)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2NetworkPerformanceMetricSubscription,
					NativeID:       ec2ARN(region, acct.ID, "network-performance-metric-subscription", key),
					Region:         &region,
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanTransitGatewayMeteringPolicies — SDK has no paginator for this op.
// Manual NextToken loop.
func scanTransitGatewayMeteringPolicies(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.DescribeTransitGatewayMeteringPolicies(ctx, &ec2.DescribeTransitGatewayMeteringPoliciesInput{NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "ec2:DescribeTransitGatewayMeteringPolicies", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("ec2:DescribeTransitGatewayMeteringPolicies: %w", perr)
		}
		for _, p := range out.TransitGatewayMeteringPolicies {
			id := sv(p.TransitGatewayMeteringPolicyId)
			if id == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2TransitGatewayMeteringPolicy,
				NativeID:       ec2ARN(region, acct.ID, "transit-gateway-metering-policy", id),
				Region:         &region,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ec2 transit-gateway-metering-policies: %w", uerr)
	}
	return len(batch), n, nil
}

// scanVPCEncryptionControls — SDK has no paginator for this op. Manual loop.
func scanVPCEncryptionControls(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.DescribeVpcEncryptionControls(ctx, &ec2.DescribeVpcEncryptionControlsInput{NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "ec2:DescribeVpcEncryptionControls", acct.ID, region, perr)
			}
			// Per-region availability gap: VPC encryption controls aren't deployed
			// in every region (UnsupportedOperation). Silent-skip.
			if isAPIErrorCode(perr, "UnsupportedOperation") {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("ec2:DescribeVpcEncryptionControls: %w", perr)
		}
		for _, c := range out.VpcEncryptionControls {
			id := sv(c.VpcEncryptionControlId)
			if id == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2VPCEncryptionControl,
				NativeID:       ec2ARN(region, acct.ID, "vpc-encryption-control", id),
				Region:         &region,
				TagsJSON:       awsTagsJSON(c.Tags),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ec2 vpc-encryption-controls: %w", uerr)
	}
	return len(batch), n, nil
}

func scanVPNConcentrators(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeVpnConcentrators", acct, region, st,
		ec2.NewDescribeVpnConcentratorsPaginator(client, &ec2.DescribeVpnConcentratorsInput{}),
		func(page *ec2.DescribeVpnConcentratorsOutput) []*store.Resource {
			var out []*store.Resource
			for _, c := range page.VpnConcentrators {
				id := sv(c.VpnConcentratorId)
				if id == "" {
					continue
				}
				status := sv(c.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2VPNConcentrator,
					NativeID:       ec2ARN(region, acct.ID, "vpn-concentrator", id),
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(c.Tags),
					AttributesJSON: mustJSON(c),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
