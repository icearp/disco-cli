package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

const (
	ec2CapacityManagerDataExportsOp       = "ec2:DescribeCapacityManagerDataExports"
	ec2CapacityManagerDataExportsPageSize = 1000 // DescribeCapacityManagerDataExportsRequestMaxResults maximum
	ec2TGWMeteringPoliciesOp              = "ec2:DescribeTransitGatewayMeteringPolicies"
	ec2TGWMeteringPoliciesPageSize        = 1000 // TransitGatewayMaxResults maximum
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2CapacityManagerDataExport, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2NetworkPerformanceMetricSubscription, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2TransitGatewayMeteringPolicy, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2VPCEncryptionControl, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2VPNConcentrator, Service: "ec2"})
}

// scanEC2MiscExtra discovers small EC2 families not worth their own scanner
// file: CapacityManager data exports, AWS Network Performance metric
// subscriptions, TransitGateway metering policies, VPC encryption controls,
// VPN concentrators. None carry native ARN fields on the SDK summary —
// synthesize via ec2ARN.
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

// scanCapacityManagerDataExports stores the Capacity Manager data export
// configurations, those read before a failed page included (storeTokenList).
// A region feature gap on the first page is an empty listing, as ec2PageScan
// treats it; on a later page it fails the listing. The listing is hand-rolled,
// not the SDK paginator, because that one repeats a repeated NextToken forever.
func scanCapacityManagerDataExports(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ec2CapacityManagerDataExportsOp,
		func(ctx context.Context, token *string) ([]ec2types.CapacityManagerDataExportResponse, *string, error) {
			out, err := client.DescribeCapacityManagerDataExports(ctx, &ec2.DescribeCapacityManagerDataExportsInput{
				MaxResults: sdkaws.Int32(ec2CapacityManagerDataExportsPageSize),
				NextToken:  token,
			})
			if token == nil && isEC2RegionFeatureGap(err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.CapacityManagerDataExports, out.NextToken, nil
		},
		func(e ec2types.CapacityManagerDataExportResponse) (*store.Resource, string, string) {
			id := sv(e.CapacityManagerDataExportId)
			if id == "" {
				return nil, "data exports have no CapacityManagerDataExportId", "one without a CapacityManagerDataExportId"
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2CapacityManagerDataExport,
				NativeID:       ec2ARN(region, acct.ID, "capacity-manager-data-export", id),
				Region:         &region,
				TagsJSON:       awsTagsJSON(e.Tags),
				AttributesJSON: mustJSON(e),
				DiscoveredBy:   scanID,
			}, "", ""
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

// scanTransitGatewayMeteringPolicies stores the transit gateway metering
// policies, those read before a failed page included (storeTokenList). The SDK
// has no paginator for this op.
func scanTransitGatewayMeteringPolicies(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ec2TGWMeteringPoliciesOp,
		func(ctx context.Context, token *string) ([]ec2types.TransitGatewayMeteringPolicy, *string, error) {
			out, err := client.DescribeTransitGatewayMeteringPolicies(ctx, &ec2.DescribeTransitGatewayMeteringPoliciesInput{
				MaxResults: sdkaws.Int32(ec2TGWMeteringPoliciesPageSize),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.TransitGatewayMeteringPolicies, out.NextToken, nil
		},
		func(p ec2types.TransitGatewayMeteringPolicy) (*store.Resource, string, string) {
			id := sv(p.TransitGatewayMeteringPolicyId)
			if id == "" {
				return nil, "metering policies have no TransitGatewayMeteringPolicyId", cmp.Or(sv(p.TransitGatewayId), "one without a TransitGatewayId")
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2TransitGatewayMeteringPolicy,
				NativeID:       ec2ARN(region, acct.ID, "transit-gateway-metering-policy", id),
				Region:         &region,
				TagsJSON:       awsTagsJSON(p.Tags),
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			}, "", ""
		},
	)
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
			// VPC encryption controls aren't deployed in every region
			// (UnsupportedOperation) — silent-skip.
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
