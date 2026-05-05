package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/route53resolver"
)

func init() {
	registerService(serviceEntry{
		name: "aws:route53resolver",
		fn:   scanRoute53Resolver,
		emits: []coverage.TypeDecl{
			{Service: "route53resolver", DiscoType: TypeRoute53ResolverFirewallDomainList},
			{Service: "route53resolver", DiscoType: TypeRoute53ResolverFirewallRuleGroup},
			{Service: "route53resolver", DiscoType: TypeRoute53ResolverFirewallRuleGroupAssociation},
			{Service: "route53resolver", DiscoType: TypeRoute53ResolverOutpostResolver},
			{Service: "route53resolver", DiscoType: TypeRoute53ResolverResolverConfig},
			{Service: "route53resolver", DiscoType: TypeRoute53ResolverResolverDNSSECConfig},
			{Service: "route53resolver", DiscoType: TypeRoute53ResolverResolverEndpoint},
			{Service: "route53resolver", DiscoType: TypeRoute53ResolverResolverQueryLoggingConfig},
			{Service: "route53resolver", DiscoType: TypeRoute53ResolverResolverQueryLoggingConfigAssociation},
			{Service: "route53resolver", DiscoType: TypeRoute53ResolverResolverRule},
			{Service: "route53resolver", DiscoType: TypeRoute53ResolverResolverRuleAssociation},
		},
	})
}

type route53ResolverAPI interface {
	ListFirewallDomainLists(context.Context, *route53resolver.ListFirewallDomainListsInput, ...func(*route53resolver.Options)) (*route53resolver.ListFirewallDomainListsOutput, error)
	ListFirewallRuleGroups(context.Context, *route53resolver.ListFirewallRuleGroupsInput, ...func(*route53resolver.Options)) (*route53resolver.ListFirewallRuleGroupsOutput, error)
	ListFirewallRuleGroupAssociations(context.Context, *route53resolver.ListFirewallRuleGroupAssociationsInput, ...func(*route53resolver.Options)) (*route53resolver.ListFirewallRuleGroupAssociationsOutput, error)
	ListOutpostResolvers(context.Context, *route53resolver.ListOutpostResolversInput, ...func(*route53resolver.Options)) (*route53resolver.ListOutpostResolversOutput, error)
	ListResolverConfigs(context.Context, *route53resolver.ListResolverConfigsInput, ...func(*route53resolver.Options)) (*route53resolver.ListResolverConfigsOutput, error)
	ListResolverDnssecConfigs(context.Context, *route53resolver.ListResolverDnssecConfigsInput, ...func(*route53resolver.Options)) (*route53resolver.ListResolverDnssecConfigsOutput, error)
	ListResolverEndpoints(context.Context, *route53resolver.ListResolverEndpointsInput, ...func(*route53resolver.Options)) (*route53resolver.ListResolverEndpointsOutput, error)
	ListResolverQueryLogConfigs(context.Context, *route53resolver.ListResolverQueryLogConfigsInput, ...func(*route53resolver.Options)) (*route53resolver.ListResolverQueryLogConfigsOutput, error)
	ListResolverQueryLogConfigAssociations(context.Context, *route53resolver.ListResolverQueryLogConfigAssociationsInput, ...func(*route53resolver.Options)) (*route53resolver.ListResolverQueryLogConfigAssociationsOutput, error)
	ListResolverRules(context.Context, *route53resolver.ListResolverRulesInput, ...func(*route53resolver.Options)) (*route53resolver.ListResolverRulesOutput, error)
	ListResolverRuleAssociations(context.Context, *route53resolver.ListResolverRuleAssociationsInput, ...func(*route53resolver.Options)) (*route53resolver.ListResolverRuleAssociationsOutput, error)
}

func scanRoute53Resolver(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := route53resolver.NewFromConfig(acct.cfg, func(o *route53resolver.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanR53RFirewallDomainLists(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RFirewallRuleGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanR53RFirewallRuleGroupAssocs(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanR53ROutpostResolvers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RResolverConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RResolverDnssecConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RResolverEndpoints(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanR53RResolverQueryLogConfigs(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanR53RResolverQueryLogConfigAssocs(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanR53RResolverRules(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RResolverRuleAssocs(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func r53rARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:route53resolver:%s:%s:%s/%s", region, acct, kind, id)
}

func scanR53RFirewallDomainLists(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListFirewallDomainListsPaginator(client, &route53resolver.ListFirewallDomainListsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListFirewallDomainLists", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListFirewallDomainLists: %w", perr)
		}
		for _, d := range out.FirewallDomainLists {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = sv(d.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverFirewallDomainList, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
				// Lists with ManagedOwnerName are the AWS-default
				// firewall domain lists present in every account.
				ManagedByProvider: d.ManagedOwnerName != nil,
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver firewall-domain-lists")
}

func scanR53RFirewallRuleGroups(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListFirewallRuleGroupsPaginator(client, &route53resolver.ListFirewallRuleGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListFirewallRuleGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListFirewallRuleGroups: %w", perr)
		}
		for _, g := range out.FirewallRuleGroups {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			label := sv(g.Name)
			if label == "" {
				label = sv(g.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverFirewallRuleGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver firewall-rule-groups")
}

func scanR53RFirewallRuleGroupAssocs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListFirewallRuleGroupAssociationsPaginator(client, &route53resolver.ListFirewallRuleGroupAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListFirewallRuleGroupAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListFirewallRuleGroupAssociations: %w", perr)
		}
		for _, a := range out.FirewallRuleGroupAssociations {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverFirewallRuleGroupAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver firewall-rule-group-associations")
}

func scanR53ROutpostResolvers(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListOutpostResolversPaginator(client, &route53resolver.ListOutpostResolversInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListOutpostResolvers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListOutpostResolvers: %w", perr)
		}
		for _, o := range out.OutpostResolvers {
			arn := sv(o.Arn)
			if arn == "" {
				continue
			}
			label := sv(o.Name)
			if label == "" {
				label = sv(o.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverOutpostResolver, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver outpost-resolvers")
}

// scanR53RResolverConfigs — ResolverConfig has no native ARN; synth from
// (region, acct, vpcId).
func scanR53RResolverConfigs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListResolverConfigsPaginator(client, &route53resolver.ListResolverConfigsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListResolverConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListResolverConfigs: %w", perr)
		}
		for _, c := range out.ResolverConfigs {
			rid := sv(c.ResourceId)
			if rid == "" {
				continue
			}
			arn := r53rARN(region, acct.ID, "resolver-config", rid)
			label := rid
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverResolverConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				// Per-VPC AWS-managed config row, one per VPC by default.
				ManagedByProvider: true,
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver resolver-configs")
}

func scanR53RResolverDnssecConfigs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListResolverDnssecConfigsPaginator(client, &route53resolver.ListResolverDnssecConfigsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListResolverDnssecConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListResolverDnssecConfigs: %w", perr)
		}
		for _, c := range out.ResolverDnssecConfigs {
			rid := sv(c.ResourceId)
			if rid == "" {
				continue
			}
			arn := r53rARN(region, acct.ID, "resolver-dnssec-config", rid)
			label := rid
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverResolverDNSSECConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver resolver-dnssec-configs")
}

func scanR53RResolverEndpoints(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListResolverEndpointsPaginator(client, &route53resolver.ListResolverEndpointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListResolverEndpoints", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListResolverEndpoints: %w", perr)
		}
		for _, e := range out.ResolverEndpoints {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			label := sv(e.Name)
			if label == "" {
				label = sv(e.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverResolverEndpoint, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver resolver-endpoints")
}

func scanR53RResolverQueryLogConfigs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListResolverQueryLogConfigsPaginator(client, &route53resolver.ListResolverQueryLogConfigsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListResolverQueryLogConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListResolverQueryLogConfigs: %w", perr)
		}
		for _, c := range out.ResolverQueryLogConfigs {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = sv(c.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverResolverQueryLoggingConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver resolver-query-log-configs")
}

func scanR53RResolverQueryLogConfigAssocs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListResolverQueryLogConfigAssociationsPaginator(client, &route53resolver.ListResolverQueryLogConfigAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListResolverQueryLogConfigAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListResolverQueryLogConfigAssociations: %w", perr)
		}
		for _, a := range out.ResolverQueryLogConfigAssociations {
			id := sv(a.Id)
			if id == "" {
				continue
			}
			arn := r53rARN(region, acct.ID, "resolver-query-log-config-association", id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverResolverQueryLoggingConfigAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver resolver-query-log-config-associations")
}

func scanR53RResolverRules(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListResolverRulesPaginator(client, &route53resolver.ListResolverRulesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListResolverRules", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListResolverRules: %w", perr)
		}
		for _, r := range out.ResolverRules {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			label := sv(r.Name)
			if label == "" {
				label = sv(r.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverResolverRule, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
				// Ids prefixed "rslvr-autodefined-rr-" identify the
				// AWS-default Internet Resolver rules present in every
				// account.
				ManagedByProvider: strings.HasPrefix(sv(r.Id), "rslvr-autodefined-rr-"),
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver resolver-rules")
}

func scanR53RResolverRuleAssocs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListResolverRuleAssociationsPaginator(client, &route53resolver.ListResolverRuleAssociationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListResolverRuleAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListResolverRuleAssociations: %w", perr)
		}
		for _, a := range out.ResolverRuleAssociations {
			id := sv(a.Id)
			if id == "" {
				continue
			}
			arn := r53rARN(region, acct.ID, "resolver-rule-association", id)
			label := sv(a.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverResolverRuleAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				// Ids prefixed "rslvr-autodefined-assoc-" identify the
				// AWS-default Internet Resolver associations attached to
				// every VPC.
				ManagedByProvider: strings.HasPrefix(id, "rslvr-autodefined-assoc-"),
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver resolver-rule-associations")
}
