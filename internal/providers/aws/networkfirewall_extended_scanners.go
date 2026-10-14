package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	"github.com/icearp/disco-cli/store"
)

// scanNetworkFirewallExtended discovers the types beyond firewalls, policies
// and rule groups. Proxies and container associations run last: they are the
// newest ops, so an error in either must not cost the older phases' rows.
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

	t, i, ferr = scanNFProxyConfigurations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanNFProxyRuleGroups(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanNFProxies(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanNFContainerAssociations(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// isProxyPreviewRegionGap matches the InvalidRequestException ("The API being
// called does not exist") Network Firewall proxy ops return outside their
// public-preview region (us-east-2 only, as of 2026-07). Silent per-region
// skip: the rest of NetworkFirewall still scans, and these ops self-heal
// (start returning data, no code change) as the preview expands, vs a
// hardcoded region gate.
func isProxyPreviewRegionGap(err error) bool {
	return isAPIErrorWithMessage(err, "InvalidRequestException", "does not exist")
}

func scanNFProxyConfigurations(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := networkfirewall.NewListProxyConfigurationsPaginator(client, &networkfirewall.ListProxyConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isProxyPreviewRegionGap(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "networkfirewall:ListProxyConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("networkfirewall:ListProxyConfigurations: %w", err)
		}
		for _, c := range out.ProxyConfigurations {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkFirewallProxyConfiguration, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "networkfirewall proxy-configurations")
}

func scanNFProxyRuleGroups(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := networkfirewall.NewListProxyRuleGroupsPaginator(client, &networkfirewall.ListProxyRuleGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isProxyPreviewRegionGap(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "networkfirewall:ListProxyRuleGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("networkfirewall:ListProxyRuleGroups: %w", err)
		}
		for _, g := range out.ProxyRuleGroups {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkFirewallProxyRuleGroup, NativeID: arn,
				Name: g.Name, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "networkfirewall proxy-rule-groups")
}

// scanNFLoggingConfigurations walks ListFirewalls, calls
// DescribeLoggingConfiguration per firewall, and skips firewalls with no
// logging configured. Synth ARN: {firewallArn}/logging-configuration.
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
			attrsJSON := mustJSON(c)
			arnLocal := arn
			if dout, derr := client.DescribeTLSInspectionConfiguration(ctx, &networkfirewall.DescribeTLSInspectionConfigurationInput{TLSInspectionConfigurationArn: &arnLocal}); derr == nil {
				attrsJSON = mustJSON(dout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkFirewallTLSInspectionConfiguration, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: attrsJSON, DiscoveredBy: scanID,
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

// scanNFProxies lists proxies and describes each one: the list summary
// carries only the ARN and name, while the state, NAT gateway and proxy
// configuration live on DescribeProxy. Proxies share the proxy-configuration
// ops' public preview, so the same region gap applies.
func scanNFProxies(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := networkfirewall.NewListProxiesPaginator(client, &networkfirewall.ListProxiesInput{})
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isProxyPreviewRegionGap(err) {
				break
			}
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "network-firewall:ListProxies", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("network-firewall:ListProxies: %w", err)
		}
		for _, p := range out.Proxies {
			if arn := sv(p.Arn); arn != "" {
				arns = append(arns, arn)
			}
		}
	}
	batch, err := describeFanOut(ctx, st, acct, region, "network-firewall:DescribeProxy", arns, isNFDeletedAfterList,
		func(ctx context.Context, arn string) (*store.Resource, error) {
			out, err := client.DescribeProxy(ctx, &networkfirewall.DescribeProxyInput{ProxyArn: &arn})
			if err != nil {
				return nil, err
			}
			if out.Proxy == nil {
				return nil, nil
			}
			p := out.Proxy
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkFirewallProxy, NativeID: arn,
				Name: p.ProxyName, Region: &region, Status: nonEmptyPtr(string(p.ProxyState)),
				CreatedAt: tp(p.CreateTime), AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			}, nil
		})
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, batch, "network-firewall proxies")
}

// scanNFContainerAssociations lists container associations and describes
// each one: the summary carries only the ARN and name, while the monitored
// ECS/EKS cluster ARNs and the status live on DescribeContainerAssociation.
func scanNFContainerAssociations(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := networkfirewall.NewListContainerAssociationsPaginator(client, &networkfirewall.ListContainerAssociationsInput{})
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "network-firewall:ListContainerAssociations", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("network-firewall:ListContainerAssociations: %w", err)
		}
		for _, c := range out.ContainerAssociations {
			if arn := sv(c.Arn); arn != "" {
				arns = append(arns, arn)
			}
		}
	}
	batch, err := describeFanOut(ctx, st, acct, region, "network-firewall:DescribeContainerAssociation", arns, isNFDeletedAfterList,
		func(ctx context.Context, arn string) (*store.Resource, error) {
			out, err := client.DescribeContainerAssociation(ctx, &networkfirewall.DescribeContainerAssociationInput{ContainerAssociationArn: &arn})
			if err != nil {
				return nil, err
			}
			// The output is flat (no payload sub-struct), so the row stores it
			// whole, UpdateToken included, as DescribeFirewall rows do.
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkFirewallContainerAssociation, NativeID: arn,
				Name: out.ContainerAssociationName, Region: &region, Status: nonEmptyPtr(string(out.Status)),
				AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
			}, nil
		})
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, batch, "network-firewall container-associations")
}

// isNFDeletedAfterList matches a resource deleted between its list and its
// describe call.
func isNFDeletedAfterList(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}
