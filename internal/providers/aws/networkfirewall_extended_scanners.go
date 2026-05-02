package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
)

// scanNetworkFirewallExtended discovers per-firewall logging configurations,
// account-wide TLS inspection configurations, and VPC endpoint associations.
func scanNetworkFirewallExtended(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t, i, ferr := scanNFLoggingConfigurations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanNFTLSInspectionConfigurations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanNFVpcEndpointAssociations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanNFLoggingConfigurations walks ListFirewalls and calls
// DescribeLoggingConfiguration per firewall. Skips firewalls whose logging
// is not configured. Synth ARN: {firewallArn}/logging-configuration.
func scanNFLoggingConfigurations(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := networkfirewall.NewListFirewallsPaginator(client, &networkfirewall.ListFirewallsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "networkfirewall:ListFirewalls(logging)", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("networkfirewall:ListFirewalls(logging): %w", err)
		}
		for _, f := range out.Firewalls {
			fa := sv(f.FirewallArn)
			if fa == "" {
				continue
			}
			lc, derr := client.DescribeLoggingConfiguration(ctx, &networkfirewall.DescribeLoggingConfigurationInput{FirewallArn: &fa})
			if derr != nil {
				if isAccessDenied(derr) || isAPIErrorCode(derr, "ResourceNotFoundException") {
					continue
				}
				return 0, 0, fmt.Errorf("networkfirewall:DescribeLoggingConfiguration: %w", derr)
			}
			if lc.LoggingConfiguration == nil || len(lc.LoggingConfiguration.LogDestinationConfigs) == 0 {
				continue
			}
			arn := fa + "/logging-configuration"
			label := "logging-configuration"
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkFirewallLoggingConfiguration, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(lc), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "networkfirewall logging-configurations")
}

func scanNFTLSInspectionConfigurations(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListTLSInspectionConfigurations(ctx, &networkfirewall.ListTLSInspectionConfigurationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "networkfirewall:ListTLSInspectionConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("networkfirewall:ListTLSInspectionConfigurations: %w", err)
		}
		for _, c := range out.TLSInspectionConfigurations {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkFirewallTLSInspectionConfiguration, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "networkfirewall tls-inspection-configurations")
}

func scanNFVpcEndpointAssociations(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListVpcEndpointAssociations(ctx, &networkfirewall.ListVpcEndpointAssociationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "networkfirewall:ListVpcEndpointAssociations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("networkfirewall:ListVpcEndpointAssociations: %w", err)
		}
		for _, v := range out.VpcEndpointAssociations {
			arn := sv(v.VpcEndpointAssociationArn)
			if arn == "" {
				continue
			}
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkFirewallVpcEndpointAssociation, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "networkfirewall vpc-endpoint-associations")
}
