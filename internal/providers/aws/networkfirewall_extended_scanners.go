package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	nfwtypes "github.com/aws/aws-sdk-go-v2/service/networkfirewall/types"
	"github.com/icearp/disco-cli/store"
)

// isProxyPreviewRegionGap matches the InvalidRequestException ("The API being
// called does not exist") Network Firewall proxy ops return outside their
// public-preview region (us-east-2 only, as of 2026-07). Silent per-region
// skip: the rest of NetworkFirewall still scans, and these ops self-heal
// (start returning data, no code change) as the preview expands, vs a
// hardcoded region gate.
func isProxyPreviewRegionGap(err error) bool {
	return isAPIErrorWithMessage(err, "InvalidRequestException", "does not exist")
}

// isFirstPageProxyGap reports whether err, answering the page sent token, is
// the preview region gap. Only a first page can be: once a page succeeded,
// the op exists in the region, so a later gap is an error.
func isFirstPageProxyGap(token *string, err error) bool {
	return token == nil && isProxyPreviewRegionGap(err)
}

// nfListedRow is the row of a resource stored as its list element, or nil
// with the reason when the element has no ARN to key it by.
func nfListedRow(acct *account, region, scanID, rtype, what string, arn, name *string, elem any) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have an empty ARN", sv(name)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: *arn, Name: name, Region: &region,
		AttributesJSON: mustJSON(elem), DiscoveredBy: scanID,
	}, "", ""
}

// scanNFProxyConfigurations stores the proxy configurations as listed; a
// first-page preview region gap stores none, silently.
func scanNFProxyConfigurations(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, nfProxyConfigListOp,
		func(ctx context.Context, token *string) ([]nfwtypes.ProxyConfigurationMetadata, *string, error) {
			out, err := client.ListProxyConfigurations(ctx, &networkfirewall.ListProxyConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(nfListMax)})
			if isFirstPageProxyGap(token, err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.ProxyConfigurations, out.NextToken, nil
		},
		func(c nfwtypes.ProxyConfigurationMetadata) (*store.Resource, string, string) {
			return nfListedRow(acct, region, scanID, TypeNetworkFirewallProxyConfiguration, "proxy configurations", c.Arn, c.Name, c)
		})
}

// scanNFProxyRuleGroups stores the proxy rule groups as listed; a first-page
// preview region gap stores none, silently.
func scanNFProxyRuleGroups(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, nfProxyRuleGroupListOp,
		func(ctx context.Context, token *string) ([]nfwtypes.ProxyRuleGroupMetadata, *string, error) {
			out, err := client.ListProxyRuleGroups(ctx, &networkfirewall.ListProxyRuleGroupsInput{NextToken: token, MaxResults: sdkaws.Int32(nfListMax)})
			if isFirstPageProxyGap(token, err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.ProxyRuleGroups, out.NextToken, nil
		},
		func(g nfwtypes.ProxyRuleGroupMetadata) (*store.Resource, string, string) {
			return nfListedRow(acct, region, scanID, TypeNetworkFirewallProxyRuleGroup, "proxy rule groups", g.Arn, g.Name, g)
		})
}

// scanNFLoggingConfigurations calls DescribeLoggingConfiguration for each
// firewall the firewalls phase listed, and stores each configured one as
// {firewallArn}/logging-configuration. A firewall the Describe no longer
// finds (ResourceNotFoundException, in the op's model) was deleted since it
// was listed; denied firewalls are warned about once; any other Describe
// error is returned after the rest are stored.
func scanNFLoggingConfigurations(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string, firewallARNs []string) (int, int, error) {
	var (
		batch  []*store.Resource
		errs   []error
		denied skipTally
	)
	for _, fa := range firewallARNs {
		lc, err := client.DescribeLoggingConfiguration(ctx, &networkfirewall.DescribeLoggingConfigurationInput{FirewallArn: &fa})
		switch {
		case err == nil:
		case isNFDeletedAfterList(err):
			continue
		case isAccessDenied(err):
			denied.skipped(fa, err)
			continue
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", nfLoggingOp, fa, err))
			continue
		}
		if lc.LoggingConfiguration == nil || len(lc.LoggingConfiguration.LogDestinationConfigs) == 0 {
			continue
		}
		label := "logging-configuration"
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeNetworkFirewallLoggingConfiguration, NativeID: fa + "/logging-configuration",
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(lc), DiscoveredBy: scanID,
		})
	}
	return storeReadRows(st, acct, region, nfLoggingOp, batch, nil, func() {
		if denied.count > 0 {
			_ = skipIfAccessDenied(st, nfLoggingOp, acct.ID, region, fmt.Errorf("%d firewall(s) denied, so their logging configuration was not stored, first %s: %w", denied.count, denied.first, denied.err))
		}
	}, errs...)
}

func scanNFVpcEndpointAssociations(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, nfVEAListOp,
		func(ctx context.Context, token *string) ([]nfwtypes.VpcEndpointAssociationMetadata, *string, error) {
			out, err := client.ListVpcEndpointAssociations(ctx, &networkfirewall.ListVpcEndpointAssociationsInput{NextToken: token, MaxResults: sdkaws.Int32(nfListMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.VpcEndpointAssociations, out.NextToken, nil
		},
		func(v nfwtypes.VpcEndpointAssociationMetadata) (*store.Resource, string, string) {
			// The element carries no name, so the ARN names the row.
			return nfListedRow(acct, region, scanID, TypeNetworkFirewallVpcEndpointAssociation, "VPC endpoint associations",
				v.VpcEndpointAssociationArn, v.VpcEndpointAssociationArn, v)
		})
}

// scanNFProxies stores each listed proxy from its DescribeProxy body: the
// list summary carries only the ARN and name, while the state, NAT gateway,
// proxy configuration and tags live on DescribeProxy. Proxies share the
// proxy-configuration ops' public preview, so a first-page region gap stores
// none, silently.
func scanNFProxies(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) nfResult {
	return storeNFDescribed(ctx, st, acct, region, scanID, nfDescribed[nfwtypes.ProxyMetadata]{
		rtype: TypeNetworkFirewallProxy, what: "proxies", ops: nfProxyOps,
		page: func(ctx context.Context, token *string) ([]nfwtypes.ProxyMetadata, *string, error) {
			out, err := client.ListProxies(ctx, &networkfirewall.ListProxiesInput{NextToken: token, MaxResults: sdkaws.Int32(nfListMax)})
			if isFirstPageProxyGap(token, err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.Proxies, out.NextToken, nil
		},
		listed: func(m nfwtypes.ProxyMetadata) (*string, *string) { return m.Arn, m.Name },
		describe: func(ctx context.Context, arn string) (*nfBody, error) {
			out, err := client.DescribeProxy(ctx, &networkfirewall.DescribeProxyInput{ProxyArn: &arn})
			if err != nil {
				return nil, err
			}
			p := out.Proxy
			if p == nil {
				return nil, nil
			}
			return &nfBody{
				out: p, name: p.ProxyName, tags: p.Tags,
				status: nonEmptyPtr(string(p.ProxyState)), createdAt: tp(p.CreateTime),
			}, nil
		},
	})
}

// scanNFContainerAssociations stores each listed container association from
// its DescribeContainerAssociation body: the summary carries only the ARN and
// name, while the monitored ECS/EKS cluster ARNs, status and tags live on
// the Describe.
func scanNFContainerAssociations(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) nfResult {
	return storeNFDescribed(ctx, st, acct, region, scanID, nfDescribed[nfwtypes.ContainerAssociationSummary]{
		rtype: TypeNetworkFirewallContainerAssociation, what: "container associations", ops: nfContainerOps,
		page: func(ctx context.Context, token *string) ([]nfwtypes.ContainerAssociationSummary, *string, error) {
			out, err := client.ListContainerAssociations(ctx, &networkfirewall.ListContainerAssociationsInput{NextToken: token, MaxResults: sdkaws.Int32(nfListMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.ContainerAssociations, out.NextToken, nil
		},
		listed: func(m nfwtypes.ContainerAssociationSummary) (*string, *string) { return m.Arn, m.Name },
		describe: func(ctx context.Context, arn string) (*nfBody, error) {
			out, err := client.DescribeContainerAssociation(ctx, &networkfirewall.DescribeContainerAssociationInput{ContainerAssociationArn: &arn})
			if err != nil {
				return nil, err
			}
			// The output is flat (no payload sub-struct), so the row stores it
			// whole, UpdateToken included, as DescribeFirewall rows do.
			return &nfBody{
				out: out, name: out.ContainerAssociationName, tags: out.Tags,
				status: nonEmptyPtr(string(out.Status)),
			}, nil
		},
	})
}

// isNFDeletedAfterList matches a resource deleted between its list and its
// describe call.
func isNFDeletedAfterList(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}
