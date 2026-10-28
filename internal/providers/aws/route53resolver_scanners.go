package aws

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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
	registerType(restype.Descriptor{Type: TypeRoute53ResolverResolverConfig, Service: "route53resolver", Untaggable: true})
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
	ListTagsForResource(context.Context, *route53resolver.ListTagsForResourceInput, ...func(*route53resolver.Options)) (*route53resolver.ListTagsForResourceOutput, error)
}

const (
	r53rListFirewallRulesOp       = "route53resolver:ListFirewallRules"
	r53rListEndpointIPAddressesOp = "route53resolver:ListResolverEndpointIpAddresses"
	r53rTagsOp                    = "route53resolver:ListTagsForResource"
	// r53rPageLimit is the MaxResults range maximum of every op the scan
	// calls but ListFirewallConfigs.
	r53rPageLimit = 100
	// r53rFirewallConfigPageLimit is ListFirewallConfigs' MaxResults range
	// maximum (its range is 5-10).
	r53rFirewallConfigPageLimit = 10
)

// r53rTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which is the Arn its listing returned, reading every page. The
// Service Reference lists the ARN resource types of the listings it reads
// under ListTagsForResource: firewall-domain-list, firewall-rule-group,
// firewall-rule-group-association, outpost-resolver, resolver-endpoint,
// resolver-query-log-config and resolver-rule. ResourceNotFoundException is
// the op's modeled not-found, for a resource deleted after it was listed.
//
// An AWS-default row (ManagedByProvider: an AWS-managed domain list, an
// autodefined Resolver rule) is not read: whether the op accepts its ARN is
// unverified, and a rejection would fail every scan. For autodefined rules
// the Service Reference points the same way: it has a separate
// autodefined-rule ARN type, which ListTagsForResource does not list. Such a
// row keeps the TagsJSON its builder set.
func r53rTags(client route53ResolverAPI) rowTagReader {
	return rowTagReader{
		op:       r53rTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return r.TagsJSON, nil
			}
			arn := r.NativeID
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]r53rtypes.Tag, *string, error) {
				out, err := client.ListTagsForResource(ctx, &route53resolver.ListTagsForResourceInput{
					ResourceArn: &arn, MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token,
				})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t r53rtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanRoute53Resolver runs every listing as its own phase, one at a time
// (runHostPhases), in the order below: a phase's error does not stop the
// later ones; a failed write and ctx's end do. The rule and IP address
// phases run last, over the rule groups and endpoints the earlier phases
// stored. The tags of the types ListTagsForResource takes are read with
// their listing (r53rTags), and every tag denial of the scan is one warning.
// Rule groups, query logging configs and Resolver rules can be shared with
// this account through RAM (ShareStatus SHARED_WITH_ME), so their rows are
// read under otherAccountGap (rowTagReader.untaggedIfOtherAccount).
func scanRoute53Resolver(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := route53resolver.NewFromConfig(acct.cfg, func(o *route53resolver.Options) { o.Region = region })
	tags := r53rTags(client).sharingDenials()
	shareable := tags.untaggedIfOtherAccount(acct.ID)
	var ruleGroups, endpoints []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "route53resolver", 0, []hostPhase{
		{op: "route53resolver:ListFirewallDomainLists", run: func(p *phaseTotals) bool {
			return scanR53RFirewallDomainLists(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "route53resolver:ListFirewallRuleGroups", run: func(p *phaseTotals) (failed bool) {
			ruleGroups, failed = scanR53RFirewallRuleGroups(ctx, client, acct, region, st, scanID, shareable, p)
			return failed
		}},
		{op: "route53resolver:ListFirewallRuleGroupAssociations", run: func(p *phaseTotals) bool {
			return scanR53RFirewallRuleGroupAssocs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "route53resolver:ListOutpostResolvers", run: func(p *phaseTotals) bool {
			return scanR53ROutpostResolvers(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "route53resolver:ListResolverConfigs", func() (int, int, error) {
			return scanR53RResolverConfigs(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "route53resolver:ListResolverDnssecConfigs", func() (int, int, error) {
			return scanR53RResolverDnssecConfigs(ctx, client, acct, region, st, scanID)
		}),
		{op: "route53resolver:ListResolverEndpoints", run: func(p *phaseTotals) (failed bool) {
			endpoints, failed = scanR53RResolverEndpoints(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "route53resolver:ListResolverQueryLogConfigs", run: func(p *phaseTotals) bool {
			return scanR53RResolverQueryLogConfigs(ctx, client, acct, region, st, scanID, shareable, p)
		}},
		countedPhase(0, "route53resolver:ListResolverQueryLogConfigAssociations", func() (int, int, error) {
			return scanR53RResolverQueryLogConfigAssocs(ctx, client, acct, region, st, scanID)
		}),
		{op: "route53resolver:ListResolverRules", run: func(p *phaseTotals) bool {
			return scanR53RResolverRules(ctx, client, acct, region, st, scanID, shareable, p)
		}},
		countedPhase(0, "route53resolver:ListResolverRuleAssociations", func() (int, int, error) {
			return scanR53RResolverRuleAssocs(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "route53resolver:ListFirewallConfigs", func() (int, int, error) {
			return scanR53RFirewallConfigs(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, r53rListFirewallRulesOp, func() (int, int, error) {
			return scanR53RFirewallRules(ctx, client, acct, region, st, scanID, ruleGroups)
		}),
		countedPhase(0, r53rListEndpointIPAddressesOp, func() (int, int, error) {
			return scanR53RResolverEndpointIPAddresses(ctx, client, acct, region, st, scanID, endpoints)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

func r53rARN(partition, region, acct, kind, id string) string {
	return arnFor(partition, "route53resolver", region, acct, kind+"/"+id)
}

// r53rRow is the row of a listed element of typ, stored under nativeID and
// named label.
func r53rRow(acct *account, region, scanID, typ, nativeID, label string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID,
		Name: &label, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// r53rNoKey is a listing's answer for an element without the field its row
// is keyed by: it is not stored, why says so in the listing's one warning,
// and the element is named by the first of names that is set.
func r53rNoKey(why string, names ...*string) (*store.Resource, string, string) {
	for _, n := range names {
		if sv(n) != "" {
			return nil, why, sv(n)
		}
	}
	return nil, why, "(an element with no name or id)"
}

func scanR53RFirewallDomainLists(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "route53resolver:ListFirewallDomainLists",
		func(ctx context.Context, token *string) ([]r53rtypes.FirewallDomainListMetadata, *string, error) {
			out, err := client.ListFirewallDomainLists(ctx, &route53resolver.ListFirewallDomainListsInput{MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FirewallDomainLists, out.NextToken, nil
		},
		func(d r53rtypes.FirewallDomainListMetadata) (*store.Resource, string, string) {
			if sv(d.Arn) == "" {
				return r53rNoKey("firewall domain lists have no Arn", d.Name, d.Id)
			}
			r := r53rRow(acct, region, scanID, TypeRoute53ResolverFirewallDomainList, sv(d.Arn), cmp.Or(sv(d.Name), sv(d.Id)), d)
			// ManagedOwnerName set marks the AWS-default firewall
			// domain lists present in every account.
			r.ManagedByProvider = d.ManagedOwnerName != nil
			return r, "", ""
		}, tags, p)
}

// isR53RNotFound reports a parent deleted after it was listed.
func isR53RNotFound(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// scanR53RFirewallRuleGroups stores every rule group with its tags and
// returns the stored ones this account owns, as the parents of the rule
// phase. A group shared with this account (ShareStatus SHARED_WITH_ME) is not
// returned: its rules belong to the owner's account, whose own scan lists
// them.
func scanR53RFirewallRuleGroups(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (parents []childParent, storeWriteFailed bool) {
	sharedIn := map[string]bool{}
	parents, storeWriteFailed = storeParentListTags(ctx, st, acct, region, "route53resolver:ListFirewallRuleGroups", "firewall rule groups", "Id",
		func(ctx context.Context, token *string) ([]r53rtypes.FirewallRuleGroupMetadata, *string, error) {
			out, err := client.ListFirewallRuleGroups(ctx, &route53resolver.ListFirewallRuleGroupsInput{MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FirewallRuleGroups, out.NextToken, nil
		},
		func(g r53rtypes.FirewallRuleGroupMetadata) (*store.Resource, string, string) {
			if sv(g.Arn) == "" {
				return r53rNoKey("firewall rule groups have no Arn", g.Name, g.Id)
			}
			if g.ShareStatus == r53rtypes.ShareStatusSharedWithMe {
				sharedIn[sv(g.Arn)] = true
			}
			return r53rRow(acct, region, scanID, TypeRoute53ResolverFirewallRuleGroup, sv(g.Arn), cmp.Or(sv(g.Name), sv(g.Id)), g), "", ""
		},
		func(g r53rtypes.FirewallRuleGroupMetadata) string { return sv(g.Id) },
		tags, p)
	return slices.DeleteFunc(parents, func(g childParent) bool { return sharedIn[g.arn] }), storeWriteFailed
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

// scanR53RFirewallRuleGroupAssocs stores the associations a page at a time
// (storeTokenPagesTags): there is one per VPC a rule group is associated
// with, and each costs a tag read.
func scanR53RFirewallRuleGroupAssocs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "route53resolver:ListFirewallRuleGroupAssociations",
		func(ctx context.Context, token *string) ([]r53rtypes.FirewallRuleGroupAssociation, *string, error) {
			out, err := client.ListFirewallRuleGroupAssociations(ctx, &route53resolver.ListFirewallRuleGroupAssociationsInput{MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FirewallRuleGroupAssociations, out.NextToken, nil
		},
		func(a r53rtypes.FirewallRuleGroupAssociation) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return r53rNoKey("firewall rule group associations have no Arn", a.Name, a.Id)
			}
			return r53rRow(acct, region, scanID, TypeRoute53ResolverFirewallRuleGroupAssociation, sv(a.Arn), sv(a.Id), a), "", ""
		}, tags, p)
}

func scanR53ROutpostResolvers(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "route53resolver:ListOutpostResolvers",
		func(ctx context.Context, token *string) ([]r53rtypes.OutpostResolver, *string, error) {
			out, err := client.ListOutpostResolvers(ctx, &route53resolver.ListOutpostResolversInput{MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.OutpostResolvers, out.NextToken, nil
		},
		func(o r53rtypes.OutpostResolver) (*store.Resource, string, string) {
			if sv(o.Arn) == "" {
				return r53rNoKey("Outpost Resolvers have no Arn", o.Name, o.Id)
			}
			return r53rRow(acct, region, scanID, TypeRoute53ResolverOutpostResolver, sv(o.Arn), cmp.Or(sv(o.Name), sv(o.Id)), o), "", ""
		}, tags, p)
}

// scanR53RResolverConfigs — ResolverConfig has no native ARN; synth from
// (region, acct, vpcId).
func scanR53RResolverConfigs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "route53resolver:ListResolverConfigs",
		func(ctx context.Context, token *string) ([]r53rtypes.ResolverConfig, *string, error) {
			out, err := client.ListResolverConfigs(ctx, &route53resolver.ListResolverConfigsInput{MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ResolverConfigs, out.NextToken, nil
		},
		func(c r53rtypes.ResolverConfig) (*store.Resource, string, string) {
			rid := sv(c.ResourceId)
			if rid == "" {
				return r53rNoKey("Resolver configs have no ResourceId", c.Id)
			}
			return r53rRow(acct, region, scanID, TypeRoute53ResolverResolverConfig, r53rARN(acct.partition(), region, acct.ID, "resolver-config", rid), rid, c), "", ""
		})
}

func scanR53RResolverDnssecConfigs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "route53resolver:ListResolverDnssecConfigs",
		func(ctx context.Context, token *string) ([]r53rtypes.ResolverDnssecConfig, *string, error) {
			out, err := client.ListResolverDnssecConfigs(ctx, &route53resolver.ListResolverDnssecConfigsInput{MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ResolverDnssecConfigs, out.NextToken, nil
		},
		func(c r53rtypes.ResolverDnssecConfig) (*store.Resource, string, string) {
			rid := sv(c.ResourceId)
			if rid == "" {
				return r53rNoKey("DNSSEC configs have no ResourceId", c.Id)
			}
			return r53rRow(acct, region, scanID, TypeRoute53ResolverResolverDNSSECConfig, r53rARN(acct.partition(), region, acct.ID, "resolver-dnssec-config", rid), rid, c), "", ""
		})
}

// scanR53RResolverEndpoints stores every endpoint with its tags and returns
// the stored ones as the parents of the IP address phase.
func scanR53RResolverEndpoints(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "route53resolver:ListResolverEndpoints", "Resolver endpoints", "Id",
		func(ctx context.Context, token *string) ([]r53rtypes.ResolverEndpoint, *string, error) {
			out, err := client.ListResolverEndpoints(ctx, &route53resolver.ListResolverEndpointsInput{MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ResolverEndpoints, out.NextToken, nil
		},
		func(e r53rtypes.ResolverEndpoint) (*store.Resource, string, string) {
			if sv(e.Arn) == "" {
				return r53rNoKey("Resolver endpoints have no Arn", e.Name, e.Id)
			}
			return r53rRow(acct, region, scanID, TypeRoute53ResolverResolverEndpoint, sv(e.Arn), cmp.Or(sv(e.Name), sv(e.Id)), e), "", ""
		},
		func(e r53rtypes.ResolverEndpoint) string { return sv(e.Id) },
		tags, p)
}

func scanR53RResolverQueryLogConfigs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "route53resolver:ListResolverQueryLogConfigs",
		func(ctx context.Context, token *string) ([]r53rtypes.ResolverQueryLogConfig, *string, error) {
			out, err := client.ListResolverQueryLogConfigs(ctx, &route53resolver.ListResolverQueryLogConfigsInput{MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ResolverQueryLogConfigs, out.NextToken, nil
		},
		func(c r53rtypes.ResolverQueryLogConfig) (*store.Resource, string, string) {
			if sv(c.Arn) == "" {
				return r53rNoKey("query logging configs have no Arn", c.Name, c.Id)
			}
			return r53rRow(acct, region, scanID, TypeRoute53ResolverResolverQueryLoggingConfig, sv(c.Arn), cmp.Or(sv(c.Name), sv(c.Id)), c), "", ""
		}, tags, p)
}

func scanR53RResolverQueryLogConfigAssocs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "route53resolver:ListResolverQueryLogConfigAssociations",
		func(ctx context.Context, token *string) ([]r53rtypes.ResolverQueryLogConfigAssociation, *string, error) {
			out, err := client.ListResolverQueryLogConfigAssociations(ctx, &route53resolver.ListResolverQueryLogConfigAssociationsInput{MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ResolverQueryLogConfigAssociations, out.NextToken, nil
		},
		func(a r53rtypes.ResolverQueryLogConfigAssociation) (*store.Resource, string, string) {
			id := sv(a.Id)
			if id == "" {
				return r53rNoKey("query logging config associations have no Id", a.ResourceId)
			}
			return r53rRow(acct, region, scanID, TypeRoute53ResolverResolverQueryLoggingConfigAssociation, r53rARN(acct.partition(), region, acct.ID, "resolver-query-log-config-association", id), id, a), "", ""
		})
}

func scanR53RResolverRules(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "route53resolver:ListResolverRules",
		func(ctx context.Context, token *string) ([]r53rtypes.ResolverRule, *string, error) {
			out, err := client.ListResolverRules(ctx, &route53resolver.ListResolverRulesInput{MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ResolverRules, out.NextToken, nil
		},
		func(rule r53rtypes.ResolverRule) (*store.Resource, string, string) {
			if sv(rule.Arn) == "" {
				return r53rNoKey("Resolver rules have no Arn", rule.Name, rule.Id)
			}
			r := r53rRow(acct, region, scanID, TypeRoute53ResolverResolverRule, sv(rule.Arn), cmp.Or(sv(rule.Name), sv(rule.Id)), rule)
			// Id prefix "rslvr-autodefined-rr-" = AWS-default Internet
			// Resolver rule present in every account.
			r.ManagedByProvider = strings.HasPrefix(sv(rule.Id), "rslvr-autodefined-rr-")
			return r, "", ""
		}, tags, p)
}

func scanR53RResolverRuleAssocs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "route53resolver:ListResolverRuleAssociations",
		func(ctx context.Context, token *string) ([]r53rtypes.ResolverRuleAssociation, *string, error) {
			out, err := client.ListResolverRuleAssociations(ctx, &route53resolver.ListResolverRuleAssociationsInput{MaxResults: sdkaws.Int32(r53rPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ResolverRuleAssociations, out.NextToken, nil
		},
		func(a r53rtypes.ResolverRuleAssociation) (*store.Resource, string, string) {
			id := sv(a.Id)
			if id == "" {
				return r53rNoKey("Resolver rule associations have no Id", a.Name, a.ResolverRuleId)
			}
			r := r53rRow(acct, region, scanID, TypeRoute53ResolverResolverRuleAssociation, r53rARN(acct.partition(), region, acct.ID, "resolver-rule-association", id), cmp.Or(sv(a.Name), id), a)
			// Id prefix "rslvr-autodefined-assoc-" = AWS-default
			// Internet Resolver association attached to every VPC.
			r.ManagedByProvider = strings.HasPrefix(id, "rslvr-autodefined-assoc-")
			return r, "", ""
		})
}

// scanR53RFirewallConfigs — per-VPC DNS Firewall config state. No native ARN;
// synth from (region, acct, Id).
func scanR53RFirewallConfigs(ctx context.Context, client route53ResolverAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "route53resolver:ListFirewallConfigs",
		func(ctx context.Context, token *string) ([]r53rtypes.FirewallConfig, *string, error) {
			out, err := client.ListFirewallConfigs(ctx, &route53resolver.ListFirewallConfigsInput{MaxResults: sdkaws.Int32(r53rFirewallConfigPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FirewallConfigs, out.NextToken, nil
		},
		func(c r53rtypes.FirewallConfig) (*store.Resource, string, string) {
			id := sv(c.Id)
			if id == "" {
				return r53rNoKey("firewall configs have no Id", c.ResourceId)
			}
			return r53rRow(acct, region, scanID, TypeRoute53ResolverFirewallConfig, r53rARN(acct.partition(), region, acct.ID, "firewall-config", id), id, c), "", ""
		})
}
