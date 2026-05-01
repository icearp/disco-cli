package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/route53globalresolver"
)

func init() {
	registerService(serviceEntry{
		name: "aws:route53globalresolver",
		fn:   scanRoute53GlobalResolver,
		emits: []coverage.TypeDecl{
			{Service: "route53globalresolver", DiscoType: TypeR53GRAccessSource},
			{Service: "route53globalresolver", DiscoType: TypeR53GRAccessToken},
			{Service: "route53globalresolver", DiscoType: TypeR53GRDNSView},
			{Service: "route53globalresolver", DiscoType: TypeR53GRFirewallDomainList},
			{Service: "route53globalresolver", DiscoType: TypeR53GRFirewallRule},
			{Service: "route53globalresolver", DiscoType: TypeR53GRGlobalResolver},
			{Service: "route53globalresolver", DiscoType: TypeR53GRHostedZoneAssociation},
		},
	})
}

type r53grAPI interface {
	ListGlobalResolvers(context.Context, *route53globalresolver.ListGlobalResolversInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListGlobalResolversOutput, error)
	ListAccessSources(context.Context, *route53globalresolver.ListAccessSourcesInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListAccessSourcesOutput, error)
	ListAccessTokens(context.Context, *route53globalresolver.ListAccessTokensInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListAccessTokensOutput, error)
	ListDNSViews(context.Context, *route53globalresolver.ListDNSViewsInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListDNSViewsOutput, error)
	ListFirewallDomainLists(context.Context, *route53globalresolver.ListFirewallDomainListsInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListFirewallDomainListsOutput, error)
	ListFirewallRules(context.Context, *route53globalresolver.ListFirewallRulesInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListFirewallRulesOutput, error)
	ListHostedZoneAssociations(context.Context, *route53globalresolver.ListHostedZoneAssociationsInput, ...func(*route53globalresolver.Options)) (*route53globalresolver.ListHostedZoneAssociationsOutput, error)
}

type r53grRef struct{ id, arn string }

func scanRoute53GlobalResolver(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := route53globalresolver.NewFromConfig(acct.cfg, func(o *route53globalresolver.Options) { o.Region = region })

	resolvers, t, i, ferr := scanR53GRResolvers(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanR53GRAccessSources(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53GRFirewallDomainLists(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	var views []r53grRef
	for _, r := range resolvers {
		vs, t, i, perr := scanR53GRDNSViews(ctx, client, acct, region, st, scanID, r.id)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
		views = append(views, vs...)
	}

	for _, v := range views {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanR53GRFirewallRules(ctx, client, acct, region, st, scanID, v.id) },
			func() (int, int, error) { return scanR53GRAccessTokens(ctx, client, acct, region, st, scanID, v.id) },
			func() (int, int, error) {
				return scanR53GRHostedZoneAssociations(ctx, client, acct, region, st, scanID, v.arn)
			},
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

func scanR53GRResolvers(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID string) ([]r53grRef, int, int, error) {
	pager := route53globalresolver.NewListGlobalResolversPaginator(client, &route53globalresolver.ListGlobalResolversInput{})
	var batch []*store.Resource
	var refs []r53grRef
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53globalresolver:ListGlobalResolvers", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("route53globalresolver:ListGlobalResolvers: %w", perr)
		}
		for _, r := range out.GlobalResolvers {
			arn := sv(r.Arn)
			id := sv(r.Id)
			if arn == "" || id == "" {
				continue
			}
			label := sv(r.Name)
			if label == "" {
				label = id
			}
			refs = append(refs, r53grRef{id: id, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRGlobalResolver, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "route53globalresolver global-resolvers")
	return refs, t, i, err
}

func scanR53GRAccessSources(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53globalresolver.NewListAccessSourcesPaginator(client, &route53globalresolver.ListAccessSourcesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53globalresolver:ListAccessSources", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53globalresolver:ListAccessSources: %w", perr)
		}
		for _, a := range out.AccessSources {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRAccessSource, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53globalresolver access-sources")
}

func scanR53GRFirewallDomainLists(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53globalresolver.NewListFirewallDomainListsPaginator(client, &route53globalresolver.ListFirewallDomainListsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53globalresolver:ListFirewallDomainLists", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53globalresolver:ListFirewallDomainLists: %w", perr)
		}
		for _, f := range out.FirewallDomainLists {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			label := sv(f.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRFirewallDomainList, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53globalresolver firewall-domain-lists")
}

func scanR53GRDNSViews(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID, resolverID string) ([]r53grRef, int, int, error) {
	rid := resolverID
	pager := route53globalresolver.NewListDNSViewsPaginator(client, &route53globalresolver.ListDNSViewsInput{GlobalResolverId: &rid})
	var batch []*store.Resource
	var refs []r53grRef
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53globalresolver:ListDNSViews", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("route53globalresolver:ListDNSViews: %w", perr)
		}
		for _, v := range out.DnsViews {
			arn := sv(v.Arn)
			id := sv(v.Id)
			if arn == "" || id == "" {
				continue
			}
			label := sv(v.Name)
			if label == "" {
				label = id
			}
			refs = append(refs, r53grRef{id: id, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRDNSView, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "route53globalresolver dns-views")
	return refs, t, i, err
}

func scanR53GRFirewallRules(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID, viewID string) (int, int, error) {
	vid := viewID
	pager := route53globalresolver.NewListFirewallRulesPaginator(client, &route53globalresolver.ListFirewallRulesInput{DnsViewId: &vid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53globalresolver:ListFirewallRules", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53globalresolver:ListFirewallRules: %w", perr)
		}
		for _, r := range out.FirewallRules {
			id := sv(r.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:route53globalresolver:%s:%s:dns-view/%s/firewall-rule/%s", region, acct.ID, viewID, id)
			label := sv(r.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRFirewallRule, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53globalresolver firewall-rules")
}

func scanR53GRAccessTokens(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID, viewID string) (int, int, error) {
	vid := viewID
	pager := route53globalresolver.NewListAccessTokensPaginator(client, &route53globalresolver.ListAccessTokensInput{DnsViewId: &vid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53globalresolver:ListAccessTokens", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53globalresolver:ListAccessTokens: %w", perr)
		}
		for _, a := range out.AccessTokens {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRAccessToken, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53globalresolver access-tokens")
}

func scanR53GRHostedZoneAssociations(ctx context.Context, client r53grAPI, acct *account, region string, st *store.Store, scanID, viewARN string) (int, int, error) {
	rarn := viewARN
	pager := route53globalresolver.NewListHostedZoneAssociationsPaginator(client, &route53globalresolver.ListHostedZoneAssociationsInput{ResourceArn: &rarn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53globalresolver:ListHostedZoneAssociations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53globalresolver:ListHostedZoneAssociations: %w", perr)
		}
		for _, h := range out.HostedZoneAssociations {
			id := sv(h.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/hosted-zone-association/%s", viewARN, id)
			label := sv(h.HostedZoneName)
			if label == "" {
				label = sv(h.HostedZoneId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeR53GRHostedZoneAssociation, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(h), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53globalresolver hosted-zone-associations")
}
