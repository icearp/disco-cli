package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name: "aws:network-firewall",
		fn:   scanNetworkFirewall,
		emits: []coverage.TypeDecl{
			{Service: "networkfirewall", DiscoType: TypeNetworkFirewallFirewall},
			{Service: "networkfirewall", DiscoType: TypeNetworkFirewallFirewallPolicy},
			{Service: "networkfirewall", DiscoType: TypeNetworkFirewallRuleGroup},
			{Service: "networkfirewall", DiscoType: TypeNetworkFirewallLoggingConfiguration},
			{Service: "networkfirewall", DiscoType: TypeNetworkFirewallTLSInspectionConfiguration},
			{Service: "networkfirewall", DiscoType: TypeNetworkFirewallVpcEndpointAssociation},
			{Service: "networkfirewall", DiscoType: TypeNetworkFirewallProxyConfiguration, Leaf: true},
			{Service: "networkfirewall", DiscoType: TypeNetworkFirewallProxyRuleGroup, Leaf: true},
		},
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
}

// scanNetworkFirewall discovers Network Firewall firewalls, firewall policies,
// and rule groups in one region. Each of the three phases List→Describe fans
// out describe calls concurrently via errgroup. Phase-level AccessDenied is
// tolerated via skipIfAccessDenied + break, preserving totals from earlier
// phases (per the multi-phase scanner pattern).
func scanNetworkFirewall(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := networkfirewall.NewFromConfig(acct.cfg, func(o *networkfirewall.Options) { o.Region = region })

	// Phase 1: firewalls
	{
		t, i, ferr := scanNetworkFirewalls(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return 0, 0, ferr
		}
		total += t
		inserted += i
	}

	// Phase 2: firewall policies
	{
		t, i, ferr := scanNetworkFirewallPolicies(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	// Phase 3: rule groups
	{
		t, i, ferr := scanNetworkFirewallRuleGroups(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	// Phase 4: extended types
	{
		t, i, ferr := scanNetworkFirewallExtended(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func scanNetworkFirewalls(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := networkfirewall.NewListFirewallsPaginator(client, &networkfirewall.ListFirewallsInput{})
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "network-firewall:ListFirewalls", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("network-firewall:ListFirewalls: %w", perr)
		}
		for _, m := range out.Firewalls {
			if m.FirewallArn != nil {
				arns = append(arns, *m.FirewallArn)
			}
		}
	}
	if len(arns) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutHigh)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, arn := range arns {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeFirewall(gctx, &networkfirewall.DescribeFirewallInput{FirewallArn: &arn})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("network-firewall:DescribeFirewall %s: %w", arn, derr)
			}
			if out.Firewall == nil {
				return nil
			}
			fw := out.Firewall
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeNetworkFirewallFirewall,
				NativeID:       sv(fw.FirewallArn),
				Name:           fw.FirewallName,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) > 0 {
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			return 0, 0, fmt.Errorf("upsert network-firewall firewalls: %w", uerr)
		}
		total = len(batch)
		inserted = n
	}
	return total, inserted, nil
}

func scanNetworkFirewallPolicies(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := networkfirewall.NewListFirewallPoliciesPaginator(client, &networkfirewall.ListFirewallPoliciesInput{})
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "network-firewall:ListFirewallPolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("network-firewall:ListFirewallPolicies: %w", perr)
		}
		for _, m := range out.FirewallPolicies {
			if m.Arn != nil {
				arns = append(arns, *m.Arn)
			}
		}
	}
	if len(arns) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutHigh)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, arn := range arns {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeFirewallPolicy(gctx, &networkfirewall.DescribeFirewallPolicyInput{FirewallPolicyArn: &arn})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("network-firewall:DescribeFirewallPolicy %s: %w", arn, derr)
			}
			if out.FirewallPolicyResponse == nil {
				return nil
			}
			resp := out.FirewallPolicyResponse
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeNetworkFirewallFirewallPolicy,
				NativeID:       sv(resp.FirewallPolicyArn),
				Name:           resp.FirewallPolicyName,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) > 0 {
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			return 0, 0, fmt.Errorf("upsert network-firewall firewall policies: %w", uerr)
		}
		total = len(batch)
		inserted = n
	}
	return total, inserted, nil
}

func scanNetworkFirewallRuleGroups(ctx context.Context, client networkfirewallAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// Default Scope=ACCOUNT (omitted) returns customer-owned rule groups (both
	// stateless and stateful). MANAGED scope is excluded — not customer-owned.
	pager := networkfirewall.NewListRuleGroupsPaginator(client, &networkfirewall.ListRuleGroupsInput{})
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "network-firewall:ListRuleGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("network-firewall:ListRuleGroups: %w", perr)
		}
		for _, m := range out.RuleGroups {
			if m.Arn != nil {
				arns = append(arns, *m.Arn)
			}
		}
	}
	if len(arns) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutHigh)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, arn := range arns {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			// Type omitted: required only if ARN missing, which it isn't.
			out, derr := client.DescribeRuleGroup(gctx, &networkfirewall.DescribeRuleGroupInput{RuleGroupArn: &arn})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("network-firewall:DescribeRuleGroup %s: %w", arn, derr)
			}
			if out.RuleGroupResponse == nil {
				return nil
			}
			resp := out.RuleGroupResponse
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeNetworkFirewallRuleGroup,
				NativeID:       sv(resp.RuleGroupArn),
				Name:           resp.RuleGroupName,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) > 0 {
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			return 0, 0, fmt.Errorf("upsert network-firewall rule groups: %w", uerr)
		}
		total = len(batch)
		inserted = n
	}
	return total, inserted, nil
}
