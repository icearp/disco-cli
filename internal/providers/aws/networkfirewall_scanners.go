package aws

import (
	"context"
	"fmt"
	"slices"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	nfwtypes "github.com/aws/aws-sdk-go-v2/service/networkfirewall/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNetworkFirewallFirewall, Service: "networkfirewall"})
	registerType(restype.Descriptor{Type: TypeNetworkFirewallFirewallPolicy, Service: "networkfirewall"})
	registerType(restype.Descriptor{Type: TypeNetworkFirewallRuleGroup, Service: "networkfirewall"})
	registerType(restype.Descriptor{Type: TypeNetworkFirewallLoggingConfiguration, Service: "networkfirewall"})
	registerType(restype.Descriptor{Type: TypeNetworkFirewallTLSInspectionConfiguration, Service: "networkfirewall"})
	registerType(restype.Descriptor{Type: TypeNetworkFirewallVpcEndpointAssociation, Service: "networkfirewall"})
	registerType(restype.Descriptor{Type: TypeNetworkFirewallProxyConfiguration, Service: "networkfirewall"})
	registerType(restype.Descriptor{Type: TypeNetworkFirewallProxyRuleGroup, Service: "networkfirewall"})
	registerType(restype.Descriptor{Type: TypeNetworkFirewallProxy, Service: "networkfirewall"})
	registerType(restype.Descriptor{Type: TypeNetworkFirewallContainerAssociation, Service: "networkfirewall"})
	registerService(serviceEntry{
		name: "aws:network-firewall",
		fn:   scanNetworkFirewall,
	})
}

// networkfirewallAPI is the narrow set of Network Firewall operations
// called by the scanNetworkFirewall sub-phases.
type networkfirewallAPI interface {
	ListFirewalls(context.Context, *networkfirewall.ListFirewallsInput, ...func(*networkfirewall.Options)) (*networkfirewall.ListFirewallsOutput, error)
	DescribeFirewall(context.Context, *networkfirewall.DescribeFirewallInput, ...func(*networkfirewall.Options)) (*networkfirewall.DescribeFirewallOutput, error)
	ListFirewallPolicies(context.Context, *networkfirewall.ListFirewallPoliciesInput, ...func(*networkfirewall.Options)) (*networkfirewall.ListFirewallPoliciesOutput, error)
	DescribeFirewallPolicy(context.Context, *networkfirewall.DescribeFirewallPolicyInput, ...func(*networkfirewall.Options)) (*networkfirewall.DescribeFirewallPolicyOutput, error)
	ListRuleGroups(context.Context, *networkfirewall.ListRuleGroupsInput, ...func(*networkfirewall.Options)) (*networkfirewall.ListRuleGroupsOutput, error)
	DescribeRuleGroup(context.Context, *networkfirewall.DescribeRuleGroupInput, ...func(*networkfirewall.Options)) (*networkfirewall.DescribeRuleGroupOutput, error)
	DescribeLoggingConfiguration(context.Context, *networkfirewall.DescribeLoggingConfigurationInput, ...func(*networkfirewall.Options)) (*networkfirewall.DescribeLoggingConfigurationOutput, error)
	ListTLSInspectionConfigurations(context.Context, *networkfirewall.ListTLSInspectionConfigurationsInput, ...func(*networkfirewall.Options)) (*networkfirewall.ListTLSInspectionConfigurationsOutput, error)
	DescribeTLSInspectionConfiguration(context.Context, *networkfirewall.DescribeTLSInspectionConfigurationInput, ...func(*networkfirewall.Options)) (*networkfirewall.DescribeTLSInspectionConfigurationOutput, error)
	ListVpcEndpointAssociations(context.Context, *networkfirewall.ListVpcEndpointAssociationsInput, ...func(*networkfirewall.Options)) (*networkfirewall.ListVpcEndpointAssociationsOutput, error)
	ListProxyConfigurations(context.Context, *networkfirewall.ListProxyConfigurationsInput, ...func(*networkfirewall.Options)) (*networkfirewall.ListProxyConfigurationsOutput, error)
	ListProxyRuleGroups(context.Context, *networkfirewall.ListProxyRuleGroupsInput, ...func(*networkfirewall.Options)) (*networkfirewall.ListProxyRuleGroupsOutput, error)
	ListProxies(context.Context, *networkfirewall.ListProxiesInput, ...func(*networkfirewall.Options)) (*networkfirewall.ListProxiesOutput, error)
	DescribeProxy(context.Context, *networkfirewall.DescribeProxyInput, ...func(*networkfirewall.Options)) (*networkfirewall.DescribeProxyOutput, error)
	ListContainerAssociations(context.Context, *networkfirewall.ListContainerAssociationsInput, ...func(*networkfirewall.Options)) (*networkfirewall.ListContainerAssociationsOutput, error)
	DescribeContainerAssociation(context.Context, *networkfirewall.DescribeContainerAssociationInput, ...func(*networkfirewall.Options)) (*networkfirewall.DescribeContainerAssociationOutput, error)
}

// nfResult is what one phase of scanNetworkFirewall returns. transient holds
// the Describe calls that still failed transiently after the SDK's retries,
// apart from err, so that beside a hard error it is demoted under the
// Describe op that failed. listed is the ARNs the phase listed.
type nfResult struct {
	total, inserted int
	err, transient  error
	listed          []string
}

func nfPlain(total, inserted int, err error) nfResult {
	return nfResult{total: total, inserted: inserted, err: err}
}

// nfScanPhase is one independent phase of scanNetworkFirewall: op is the op
// its err is warned under if demoted, describeOp that of its transient error.
type nfScanPhase struct {
	op, describeOp string
	run            func() nfResult
}

// nfLoggingOp labels the logging-configuration phase, which describes the
// firewalls the firewalls phase listed.
const nfLoggingOp = "networkfirewall:DescribeLoggingConfiguration"

// The ops of the phases stored as listed, shared by each phase's scanner and
// its entry in scanNetworkFirewall, which warns under it if demoted.
const (
	nfVEAListOp            = "networkfirewall:ListVpcEndpointAssociations"
	nfProxyConfigListOp    = "networkfirewall:ListProxyConfigurations"
	nfProxyRuleGroupListOp = "networkfirewall:ListProxyRuleGroups"
)

// scanNetworkFirewall runs every Network Firewall phase in one region, in
// order; proxies and container associations come last, being the newest ops.
// A phase's error does not stop the later phases; only a failed store write
// does, since every later write would fail too.
func scanNetworkFirewall(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := networkfirewall.NewFromConfig(acct.cfg, func(o *networkfirewall.Options) { o.Region = region })
	described := func(scan func(context.Context, networkfirewallAPI, *account, string, *store.Store, string) nfResult) func() nfResult {
		return func() nfResult { return scan(ctx, client, acct, region, st, scanID) }
	}
	listed := func(scan func(context.Context, networkfirewallAPI, *account, string, *store.Store, string) (int, int, error)) func() nfResult {
		return func() nfResult { return nfPlain(scan(ctx, client, acct, region, st, scanID)) }
	}
	var firewalls nfResult
	phases := []nfScanPhase{
		{nfFirewallOps.list, nfFirewallOps.describe, func() nfResult {
			firewalls = scanNetworkFirewalls(ctx, client, acct, region, st, scanID)
			return firewalls
		}},
		{nfPolicyOps.list, nfPolicyOps.describe, described(scanNetworkFirewallPolicies)},
		{nfRuleGroupOps.list, nfRuleGroupOps.describe, described(scanNetworkFirewallRuleGroups)},
		{nfLoggingOp, "", func() nfResult {
			return nfPlain(scanNFLoggingConfigurations(ctx, client, acct, region, st, scanID, firewalls.listed))
		}},
		{nfTLSOps.list, nfTLSOps.describe, described(scanNFTLSInspectionConfigurations)},
		{nfVEAListOp, "", listed(scanNFVpcEndpointAssociations)},
		{nfProxyConfigListOp, "", listed(scanNFProxyConfigurations)},
		{nfProxyRuleGroupListOp, "", listed(scanNFProxyRuleGroups)},
		{nfProxyOps.list, nfProxyOps.describe, described(scanNFProxies)},
		{nfContainerOps.list, nfContainerOps.describe, described(scanNFContainerAssociations)},
	}
	var p phaseTotals
	for _, ph := range phases {
		r := ph.run()
		if p.addPhase(ph.op, r.total, r.inserted, r.err) {
			break
		}
		p.addPhase(ph.describeOp, 0, 0, r.transient)
	}
	return p.result(st, acct, region)
}

// nfListMax is the MaxResults cap the Smithy model sets on every Network
// Firewall list op this package calls.
const nfListMax = 100

// nfOps names a described phase's list and Describe ops.
type nfOps struct{ list, describe string }

var (
	nfFirewallOps  = nfOps{"network-firewall:ListFirewalls", "network-firewall:DescribeFirewall"}
	nfPolicyOps    = nfOps{"network-firewall:ListFirewallPolicies", "network-firewall:DescribeFirewallPolicy"}
	nfRuleGroupOps = nfOps{"network-firewall:ListRuleGroups", "network-firewall:DescribeRuleGroup"}
	nfTLSOps       = nfOps{"networkfirewall:ListTLSInspectionConfigurations", "networkfirewall:DescribeTLSInspectionConfiguration"}
	nfProxyOps     = nfOps{"network-firewall:ListProxies", "network-firewall:DescribeProxy"}
	nfContainerOps = nfOps{"network-firewall:ListContainerAssociations", "network-firewall:DescribeContainerAssociation"}
)

// nfTagKV reads a Network Firewall tag for kvTagsJSON.
func nfTagKV(t nfwtypes.Tag) (key, value *string) { return t.Key, t.Value }

// nfBody is what a Describe returned for one resource: the value stored as
// the row's attributes and the row's name, tags, status and creation time.
type nfBody struct {
	out               any
	name              *string
	tags              []nfwtypes.Tag
	status, createdAt *string
}

// nfDescribed is a phase that lists one type and stores each listed resource
// from its Describe body. That body is the scanner's only source of the
// resource's tags: ListTagsForResource also returns them but is not called.
// listed reads the ARN and name off a list element; describe returns nil
// when the output carries no payload.
type nfDescribed[Item any] struct {
	rtype, what string
	ops         nfOps
	page        func(ctx context.Context, token *string) ([]Item, *string, error)
	listed      func(Item) (arn, name *string)
	describe    func(ctx context.Context, arn string) (*nfBody, error)
}

// nfReads tallies the listed resources not stored from their Describe body,
// for one warning or error per cause after the rows are stored.
type nfReads struct {
	denied    skipTally
	transient transientTally
	unkeyed   skipTally
}

// nfKept describes the resources of one cause stored as their list element.
const nfKept = "%d %s stored as their list element, without tags or references, first %s: %w"

// storeNFDescribed lists p's type and describes every listed resource
// concurrently. A failed list page keeps the resources read before it: a
// denial is warned about, any other error returned after they are stored. A
// resource Describe no longer finds (ResourceNotFoundException, in each
// Describe's model) was deleted since it was listed and is not stored. One
// whose Describe fails any other way is stored from its list element, a
// different shape without tags or the references its resolver reads: a
// denial is warned about once, transient failures come back as one
// transient error (the scan is partial, since the rows lost data a rescan
// recovers), and every other failure is returned after storing.
func storeNFDescribed[Item any](ctx context.Context, st *store.Store, acct *account, region, scanID string, p nfDescribed[Item]) nfResult {
	items, listErr := readTokenPages(ctx, p.page)
	var (
		reads  nfReads
		wg     sync.WaitGroup
		sem    = make(chan struct{}, fanoutHigh)
		rows   = make([]*store.Resource, len(items))
		errs   = make([]error, len(items))
		listed []string
	)
	for i, it := range items {
		arn, name := p.listed(it)
		if sv(arn) == "" {
			reads.unkeyed.skipped(sv(name), nil)
			continue
		}
		listed = append(listed, *arn)
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			rows[i], errs[i] = p.row(ctx, acct, region, scanID, *arn, name, it, &reads)
		})
	}
	wg.Wait()
	rows = slices.DeleteFunc(rows, func(r *store.Resource) bool { return r == nil })
	r := nfResult{listed: listed}
	if t := &reads.transient; t.count > 0 {
		r.transient = fmt.Errorf("%s: "+nfKept, p.ops.describe, t.count, p.what, t.first, t.err)
	}
	r.total, r.inserted, r.err = storeReadRows(st, acct, region, p.ops.list, rows, listErr, func() {
		if d := &reads.denied; d.count > 0 {
			_ = skipIfAccessDenied(st, p.ops.describe, acct.ID, region, fmt.Errorf(nfKept, d.count, p.what, d.first, d.err))
		}
		warnNotStored(st, p.ops.list, acct, region, &reads.unkeyed, p.what+" have an empty ARN")
	}, errs...)
	return r
}

// row builds the row of the listed resource arn, as storeNFDescribed says.
func (p nfDescribed[Item]) row(ctx context.Context, acct *account, region, scanID, arn string, name *string, it Item, reads *nfReads) (*store.Resource, error) {
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: p.rtype, NativeID: arn, Name: name, Region: &region,
		AttributesJSON: mustJSON(it), DiscoveredBy: scanID,
	}
	body, err := p.describe(ctx, arn)
	switch {
	case err == nil && body != nil:
		r.Name, r.AttributesJSON, r.TagsJSON = body.name, mustJSON(body.out), kvTagsJSON(body.tags, nfTagKV)
		r.Status, r.CreatedAt = body.status, body.createdAt
	case err == nil:
		return r, fmt.Errorf("%s %s: the response carries no %s", p.ops.describe, arn, p.what)
	case isNFDeletedAfterList(err):
		return nil, nil
	case isAccessDenied(err):
		reads.denied.skipped(arn, err)
	case reads.transient.drop(arn, err):
	default:
		return r, fmt.Errorf("%s %s: %w", p.ops.describe, arn, err)
	}
	return r, nil
}

func scanNetworkFirewalls(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) nfResult {
	return storeNFDescribed(ctx, st, acct, region, scanID, nfDescribed[nfwtypes.FirewallMetadata]{
		rtype: TypeNetworkFirewallFirewall, what: "firewall(s)", ops: nfFirewallOps,
		page: func(ctx context.Context, token *string) ([]nfwtypes.FirewallMetadata, *string, error) {
			out, err := client.ListFirewalls(ctx, &networkfirewall.ListFirewallsInput{NextToken: token, MaxResults: sdkaws.Int32(nfListMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.Firewalls, out.NextToken, nil
		},
		listed: func(m nfwtypes.FirewallMetadata) (*string, *string) { return m.FirewallArn, m.FirewallName },
		describe: func(ctx context.Context, arn string) (*nfBody, error) {
			out, err := client.DescribeFirewall(ctx, &networkfirewall.DescribeFirewallInput{FirewallArn: &arn})
			if err != nil {
				return nil, err
			}
			if out.Firewall == nil {
				return nil, nil
			}
			return &nfBody{out: out, name: out.Firewall.FirewallName, tags: out.Firewall.Tags}, nil
		},
	})
}

func scanNetworkFirewallPolicies(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) nfResult {
	return storeNFDescribed(ctx, st, acct, region, scanID, nfDescribed[nfwtypes.FirewallPolicyMetadata]{
		rtype: TypeNetworkFirewallFirewallPolicy, what: "firewall policies", ops: nfPolicyOps,
		page: func(ctx context.Context, token *string) ([]nfwtypes.FirewallPolicyMetadata, *string, error) {
			out, err := client.ListFirewallPolicies(ctx, &networkfirewall.ListFirewallPoliciesInput{NextToken: token, MaxResults: sdkaws.Int32(nfListMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.FirewallPolicies, out.NextToken, nil
		},
		listed: func(m nfwtypes.FirewallPolicyMetadata) (*string, *string) { return m.Arn, m.Name },
		describe: func(ctx context.Context, arn string) (*nfBody, error) {
			out, err := client.DescribeFirewallPolicy(ctx, &networkfirewall.DescribeFirewallPolicyInput{FirewallPolicyArn: &arn})
			if err != nil {
				return nil, err
			}
			if out.FirewallPolicyResponse == nil {
				return nil, nil
			}
			return &nfBody{out: out, name: out.FirewallPolicyResponse.FirewallPolicyName, tags: out.FirewallPolicyResponse.Tags}, nil
		},
	})
}

// scanNetworkFirewallRuleGroups stores the account's own rule groups: the
// default Scope (ACCOUNT, left unset) lists stateless and stateful ones and
// leaves out the MANAGED ones AWS publishes.
func scanNetworkFirewallRuleGroups(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) nfResult {
	return storeNFDescribed(ctx, st, acct, region, scanID, nfDescribed[nfwtypes.RuleGroupMetadata]{
		rtype: TypeNetworkFirewallRuleGroup, what: "rule groups", ops: nfRuleGroupOps,
		page: func(ctx context.Context, token *string) ([]nfwtypes.RuleGroupMetadata, *string, error) {
			out, err := client.ListRuleGroups(ctx, &networkfirewall.ListRuleGroupsInput{NextToken: token, MaxResults: sdkaws.Int32(nfListMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.RuleGroups, out.NextToken, nil
		},
		listed: func(m nfwtypes.RuleGroupMetadata) (*string, *string) { return m.Arn, m.Name },
		describe: func(ctx context.Context, arn string) (*nfBody, error) {
			// Type is needed only when the rule group is named instead of
			// given by ARN.
			out, err := client.DescribeRuleGroup(ctx, &networkfirewall.DescribeRuleGroupInput{RuleGroupArn: &arn})
			if err != nil {
				return nil, err
			}
			if out.RuleGroupResponse == nil {
				return nil, nil
			}
			return &nfBody{out: out, name: out.RuleGroupResponse.RuleGroupName, tags: out.RuleGroupResponse.Tags}, nil
		},
	})
}

func scanNFTLSInspectionConfigurations(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) nfResult {
	return storeNFDescribed(ctx, st, acct, region, scanID, nfDescribed[nfwtypes.TLSInspectionConfigurationMetadata]{
		rtype: TypeNetworkFirewallTLSInspectionConfiguration, what: "TLS inspection configurations", ops: nfTLSOps,
		page: func(ctx context.Context, token *string) ([]nfwtypes.TLSInspectionConfigurationMetadata, *string, error) {
			out, err := client.ListTLSInspectionConfigurations(ctx, &networkfirewall.ListTLSInspectionConfigurationsInput{NextToken: token, MaxResults: sdkaws.Int32(nfListMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.TLSInspectionConfigurations, out.NextToken, nil
		},
		listed: func(m nfwtypes.TLSInspectionConfigurationMetadata) (*string, *string) { return m.Arn, m.Name },
		describe: func(ctx context.Context, arn string) (*nfBody, error) {
			out, err := client.DescribeTLSInspectionConfiguration(ctx, &networkfirewall.DescribeTLSInspectionConfigurationInput{TLSInspectionConfigurationArn: &arn})
			if err != nil {
				return nil, err
			}
			if out.TLSInspectionConfigurationResponse == nil {
				return nil, nil
			}
			resp := out.TLSInspectionConfigurationResponse
			return &nfBody{out: out, name: resp.TLSInspectionConfigurationName, tags: resp.Tags}, nil
		},
	})
}
