package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/route53resolver"
	r53rtypes "github.com/aws/aws-sdk-go-v2/service/route53resolver/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRoute53ResolverFirewallDomainList, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverFirewallRuleGroup, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverFirewallRuleGroupAssociation, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverOutpostResolver, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverResolverConfig, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverResolverDNSSECConfig, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverResolverEndpoint, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverResolverQueryLoggingConfig, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverResolverQueryLoggingConfigAssociation, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverResolverRule, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverResolverRuleAssociation, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverFirewallConfig, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverFirewallRule, Service: "route53resolver"})
	registerType(restype.Descriptor{Type: TypeRoute53ResolverResolverEndpointIPAddress, Service: "route53resolver"})
	registerService(serviceEntry{
		name: "aws:route53resolver",
		fn:   scanRoute53Resolver,
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
	ListFirewallConfigs(context.Context, *route53resolver.ListFirewallConfigsInput, ...func(*route53resolver.Options)) (*route53resolver.ListFirewallConfigsOutput, error)
	ListFirewallRules(context.Context, *route53resolver.ListFirewallRulesInput, ...func(*route53resolver.Options)) (*route53resolver.ListFirewallRulesOutput, error)
	ListResolverEndpointIpAddresses(context.Context, *route53resolver.ListResolverEndpointIpAddressesInput, ...func(*route53resolver.Options)) (*route53resolver.ListResolverEndpointIpAddressesOutput, error)
}

const (
	r53rListFirewallRulesOp       = "route53resolver:ListFirewallRules"
	r53rListEndpointIPAddressesOp = "route53resolver:ListResolverEndpointIpAddresses"
	r53rPageLimit                 = 100 // MaxResults' model maximum
)

// r53rParents are the rule groups and endpoints the region-wide phases
// stored, as the parents of the rule and endpoint IP address phases.
type r53rParents struct{ ruleGroups, endpoints []childParent }

// scanRoute53Resolver runs the region-wide phases, which stop at their first
// error other than a denial, then the phases that list the rules and IP
// addresses of the rule groups and endpoints stored before that error, so a
// failure on either side costs the other no rows.
// Each result has its transient error demoted to a warning unless it carries
// a store write failure, since one transient member would make the dispatcher
// read the whole join as transient; the rest are joined and the counts summed.
func scanRoute53Resolver(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := route53resolver.NewFromConfig(acct.cfg, func(o *route53resolver.Options) { o.Region = region })
	total, inserted, parents, err := scanR53RRegionPhases(ctx, client, acct, region, st, scanID)
	errs := []error{demoteTransient(st, "route53resolver", acct, region, err)}
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{r53rListFirewallRulesOp, func() (int, int, error) {
			return scanR53RFirewallRules(ctx, client, acct, region, st, scanID, parents.ruleGroups)
		}},
		{r53rListEndpointIPAddressesOp, func() (int, int, error) {
			return scanR53RResolverEndpointIPAddresses(ctx, client, acct, region, st, scanID, parents.endpoints)
		}},
	} {
		t, i, perr := phase.run()
		total += t
		inserted += i
		errs = append(errs, demoteTransient(st, phase.op, acct, region, perr))
	}
	return total, inserted, errors.Join(errs...)
}

// scanR53RRegionPhases runs the region-wide list phases in order, stopping at
// the first error other than a denial, and returns the parents stored before it.
func scanR53RRegionPhases(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, parents r53rParents, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanR53RFirewallDomainLists(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			parents.ruleGroups, t, i, err = scanR53RFirewallRuleGroups(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) {
			return scanR53RFirewallRuleGroupAssocs(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanR53ROutpostResolvers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RResolverConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RResolverDnssecConfigs(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			parents.endpoints, t, i, err = scanR53RResolverEndpoints(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) {
			return scanR53RResolverQueryLogConfigs(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanR53RResolverQueryLogConfigAssocs(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanR53RResolverRules(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RResolverRuleAssocs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanR53RFirewallConfigs(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, parents, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, parents, nil
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
				// ManagedOwnerName set marks the AWS-default firewall
				// domain lists present in every account.
				ManagedByProvider: d.ManagedOwnerName != nil,
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver firewall-domain-lists")
}

// isR53RNotFound reports a parent deleted after it was listed.
func isR53RNotFound(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// scanR53RFirewallRuleGroups stores every rule group and returns those this
// account owns, as the parents of the rule phase. A group shared with this
// account (ShareStatus SHARED_WITH_ME) is not returned: its rules belong to
// the owner's account, whose own scan lists them.
func scanR53RFirewallRuleGroups(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := route53resolver.NewListFirewallRuleGroupsPaginator(client, &route53resolver.ListFirewallRuleGroupsInput{},
		func(o *route53resolver.ListFirewallRuleGroupsPaginatorOptions) { o.Limit = r53rPageLimit })
	var (
		batch   []*store.Resource
		parents []childParent
	)
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListFirewallRuleGroups", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("route53resolver:ListFirewallRuleGroups: %w", perr)
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
			if id := sv(g.Id); id != "" && g.ShareStatus != r53rtypes.ShareStatusSharedWithMe {
				parents = append(parents, childParent{id: id, arn: arn})
			}
		}
	}
	total, inserted, err := upsertBatch(st, batch, "route53resolver firewall-rule-groups")
	if err != nil {
		return nil, total, inserted, err
	}
	return parents, total, inserted, nil
}

// r53rFirewallRuleNativeID keys a rule under its rule group by what
// DeleteFirewallRule identifies it by: the domain list or the DNS Firewall
// Advanced threat protection it matches, then its query type when set.
// Priority is unique in a group too, but UpdateFirewallRule changes it. Qtype
// has no pattern in the model, so it goes last. It returns "" for a rule that
// names neither match target.
func r53rFirewallRuleNativeID(groupARN string, r *r53rtypes.FirewallRule) string {
	var id string
	switch {
	case sv(r.FirewallDomainListId) != "":
		id = groupARN + "/firewall-rule/domain-list/" + sv(r.FirewallDomainListId)
	case sv(r.FirewallThreatProtectionId) != "":
		id = groupARN + "/firewall-rule/threat-protection/" + sv(r.FirewallThreatProtectionId)
	default:
		return ""
	}
	if q := sv(r.Qtype); q != "" {
		id += "/qtype/" + q
	}
	return id
}

// scanR53RFirewallRules lists the rules of each given rule group and links
// each under its group. A group deleted after it was listed is skipped; a
// ValidationException skips the group with one warning, since a group being
// updated or deleted may refuse the call (unverified). A rule naming neither
// a domain list nor a threat protection has no key and is not stored; one
// warning per scan counts them and names the first.
func scanR53RFirewallRules(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string, groups []childParent) (int, int, error) {
	var unkeyed skipTally
	total, inserted, err := childFanOutSkipNotReady(ctx, st, acct, region, r53rListFirewallRulesOp, groups, isR53RNotFound, byParentID,
		"rule group(s)", "the service may refuse a group that is being updated or deleted (unverified)",
		func(ctx context.Context, g childParent) ([]*store.Resource, error) {
			pager := route53resolver.NewListFirewallRulesPaginator(client, &route53resolver.ListFirewallRulesInput{FirewallRuleGroupId: &g.id},
				func(o *route53resolver.ListFirewallRulesPaginatorOptions) { o.Limit = r53rPageLimit })
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for i := range out.FirewallRules {
					r := &out.FirewallRules[i]
					nativeID := r53rFirewallRuleNativeID(g.arn, r)
					if nativeID == "" {
						var priority int32
						if r.Priority != nil {
							priority = *r.Priority
						}
						unkeyed.skipped(fmt.Sprintf("group %s rule %q priority %d", g.id, sv(r.Name), priority), nil)
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeRoute53ResolverFirewallRule, NativeID: nativeID,
						Name: nonEmptyPtr(sv(r.Name)), Region: &region, Status: nonEmptyPtr(sv(r.Status)),
						CreatedAt: nonEmptyPtr(sv(r.CreationTime)), AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
	if unkeyed.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: r53rListFirewallRulesOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d firewall rule(s) not stored: no FirewallDomainListId or FirewallThreatProtectionId to key them by; first: %s",
				unkeyed.count, unkeyed.first),
		})
	}
	return total, inserted, err
}

// r53rEndpointIPAddressNativeID keys an IP address under its endpoint.
func r53rEndpointIPAddressNativeID(endpointARN, ipID string) string {
	return endpointARN + "/ip-address/" + ipID
}

// scanR53RResolverEndpointIPAddresses lists the IP addresses of each given
// endpoint and links each under its endpoint. An endpoint deleted after it
// was listed is skipped.
func scanR53RResolverEndpointIPAddresses(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string, endpoints []childParent) (int, int, error) {
	return childFanOutSkipTransient(ctx, st, acct, region, r53rListEndpointIPAddressesOp, endpoints, isR53RNotFound, byParentID,
		func(ctx context.Context, e childParent) ([]*store.Resource, error) {
			pager := route53resolver.NewListResolverEndpointIpAddressesPaginator(client,
				&route53resolver.ListResolverEndpointIpAddressesInput{ResolverEndpointId: &e.id},
				func(o *route53resolver.ListResolverEndpointIpAddressesPaginatorOptions) { o.Limit = r53rPageLimit })
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for i := range out.IpAddresses {
					a := &out.IpAddresses[i]
					ipID := sv(a.IpId)
					if ipID == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeRoute53ResolverResolverEndpointIPAddress, NativeID: r53rEndpointIPAddressNativeID(e.arn, ipID),
						Name: nonEmptyPtr(cmp.Or(sv(a.Ip), sv(a.Ipv6))), Region: &region, Status: nonEmptyPtr(string(a.Status)),
						CreatedAt: nonEmptyPtr(sv(a.CreationTime)), AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
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

// scanR53RResolverEndpoints stores every endpoint and returns them as the
// parents of the IP address phase.
func scanR53RResolverEndpoints(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := route53resolver.NewListResolverEndpointsPaginator(client, &route53resolver.ListResolverEndpointsInput{},
		func(o *route53resolver.ListResolverEndpointsPaginatorOptions) { o.Limit = r53rPageLimit })
	var (
		batch   []*store.Resource
		parents []childParent
	)
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListResolverEndpoints", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("route53resolver:ListResolverEndpoints: %w", perr)
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
			if id := sv(e.Id); id != "" {
				parents = append(parents, childParent{id: id, arn: arn})
			}
		}
	}
	total, inserted, err := upsertBatch(st, batch, "route53resolver resolver-endpoints")
	if err != nil {
		return nil, total, inserted, err
	}
	return parents, total, inserted, nil
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
				// Id prefix "rslvr-autodefined-rr-" = AWS-default Internet
				// Resolver rule present in every account.
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
				// Id prefix "rslvr-autodefined-assoc-" = AWS-default
				// Internet Resolver association attached to every VPC.
				ManagedByProvider: strings.HasPrefix(id, "rslvr-autodefined-assoc-"),
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver resolver-rule-associations")
}

// scanR53RFirewallConfigs — per-VPC DNS Firewall config state. No native ARN;
// synth from (region, acct, Id).
func scanR53RFirewallConfigs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := route53resolver.NewListFirewallConfigsPaginator(client, &route53resolver.ListFirewallConfigsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "route53resolver:ListFirewallConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("route53resolver:ListFirewallConfigs: %w", perr)
		}
		for _, c := range out.FirewallConfigs {
			id := sv(c.Id)
			if id == "" {
				continue
			}
			arn := r53rARN(region, acct.ID, "firewall-config", id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRoute53ResolverFirewallConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "route53resolver firewall-configs")
}
