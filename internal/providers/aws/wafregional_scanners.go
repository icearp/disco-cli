package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/wafregional"
	wafregionaltypes "github.com/aws/aws-sdk-go-v2/service/wafregional/types"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWAFRegionalWebACL, Service: "wafregional", Upstream: "AWS::waf-regional::webacl", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalRule, Service: "wafregional", Upstream: "AWS::waf-regional::rule", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalRuleGroup, Service: "wafregional", Upstream: "AWS::waf-regional::rulegroup", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalRateBasedRule, Service: "wafregional", Upstream: "AWS::waf-regional::ratebasedrule", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalIPSet, Service: "wafregional", Upstream: "AWS::waf-regional::ipset", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalByteMatchSet, Service: "wafregional", Upstream: "AWS::waf-regional::bytematchset", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalGeoMatchSet, Service: "wafregional", Upstream: "AWS::waf-regional::geomatchset", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalRegexMatchSet, Service: "wafregional", Upstream: "AWS::waf-regional::regexmatchset", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalRegexPatternSet, Service: "wafregional", Upstream: "AWS::waf-regional::regexpatternset", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalSizeConstraintSet, Service: "wafregional", Upstream: "AWS::waf-regional::sizeconstraintset", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalSQLInjectionMatchSet, Service: "wafregional", Upstream: "AWS::waf-regional::sqlinjectionmatchset", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalXSSMatchSet, Service: "wafregional", Upstream: "AWS::waf-regional::xssmatchset", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalWebACLAssociation, Service: "wafregional"})
	registerService(serviceEntry{
		name: "aws:wafregional",
		fn:   scanWAFRegional,
	})
}

// wafRegionalAPI is the narrow set of WAF Classic (regional scope) list ops the
// scanner calls. *wafregional.Client satisfies it; tests inject stubs.
type wafRegionalAPI interface {
	ListWebACLs(context.Context, *wafregional.ListWebACLsInput, ...func(*wafregional.Options)) (*wafregional.ListWebACLsOutput, error)
	ListRules(context.Context, *wafregional.ListRulesInput, ...func(*wafregional.Options)) (*wafregional.ListRulesOutput, error)
	ListRuleGroups(context.Context, *wafregional.ListRuleGroupsInput, ...func(*wafregional.Options)) (*wafregional.ListRuleGroupsOutput, error)
	ListRateBasedRules(context.Context, *wafregional.ListRateBasedRulesInput, ...func(*wafregional.Options)) (*wafregional.ListRateBasedRulesOutput, error)
	ListIPSets(context.Context, *wafregional.ListIPSetsInput, ...func(*wafregional.Options)) (*wafregional.ListIPSetsOutput, error)
	ListByteMatchSets(context.Context, *wafregional.ListByteMatchSetsInput, ...func(*wafregional.Options)) (*wafregional.ListByteMatchSetsOutput, error)
	ListGeoMatchSets(context.Context, *wafregional.ListGeoMatchSetsInput, ...func(*wafregional.Options)) (*wafregional.ListGeoMatchSetsOutput, error)
	ListRegexMatchSets(context.Context, *wafregional.ListRegexMatchSetsInput, ...func(*wafregional.Options)) (*wafregional.ListRegexMatchSetsOutput, error)
	ListRegexPatternSets(context.Context, *wafregional.ListRegexPatternSetsInput, ...func(*wafregional.Options)) (*wafregional.ListRegexPatternSetsOutput, error)
	ListSizeConstraintSets(context.Context, *wafregional.ListSizeConstraintSetsInput, ...func(*wafregional.Options)) (*wafregional.ListSizeConstraintSetsOutput, error)
	ListSqlInjectionMatchSets(context.Context, *wafregional.ListSqlInjectionMatchSetsInput, ...func(*wafregional.Options)) (*wafregional.ListSqlInjectionMatchSetsOutput, error)
	ListXssMatchSets(context.Context, *wafregional.ListXssMatchSetsInput, ...func(*wafregional.Options)) (*wafregional.ListXssMatchSetsOutput, error)
	ListResourcesForWebACL(context.Context, *wafregional.ListResourcesForWebACLInput, ...func(*wafregional.Options)) (*wafregional.ListResourcesForWebACLOutput, error)
}

// wafRegionalARN synthesizes a stable regional ARN for a WAF Classic resource.
// Regional List ops return only {Id, Name} with no ARN.
func wafRegionalARN(region, account, kind, id string) string {
	return fmt.Sprintf("arn:aws:waf-regional:%s:%s:%s/%s", region, account, kind, id)
}

// scanWAFRegional discovers WAF Classic (v1) regional resources per region,
// then fans out ListResourcesForWebACL per web-ACL to materialise association
// rows linking ALB / API Gateway stages to the protecting web-ACL.
func scanWAFRegional(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := wafregional.NewFromConfig(acct.cfg, func(o *wafregional.Options) { o.Region = region })
	rg := region
	arnFn := func(kind, id string) string { return wafRegionalARN(region, acct.ID, kind, id) }

	total, inserted, webACLIDs, err := scanWAFPhases(ctx, wafRegionalPhases(client), acct, region, &rg, st, scanID, "wafregional", arnFn)
	if err != nil {
		return total, inserted, err
	}

	t, i, aerr := scanWAFRegionalAssociations(ctx, client, acct, region, st, scanID, webACLIDs)
	if aerr != nil {
		return total, inserted, aerr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// wafRegionalPhases mirrors wafPhases for the regional SDK package. Same
// NextMarker/Limit pagination shape.
func wafRegionalPhases(c wafRegionalAPI) []wafPhase {
	const limit = 100
	return []wafPhase{
		{"ListWebACLs", TypeWAFRegionalWebACL, "webacl", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListWebACLs(ctx, &wafregional.ListWebACLsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.WebACLs))
			for _, w := range out.WebACLs {
				items = append(items, wafItem{sv(w.WebACLId), sv(w.Name), w})
			}
			return items, out.NextMarker, nil
		}},
		{"ListRules", TypeWAFRegionalRule, "rule", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListRules(ctx, &wafregional.ListRulesInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.Rules))
			for _, r := range out.Rules {
				items = append(items, wafItem{sv(r.RuleId), sv(r.Name), r})
			}
			return items, out.NextMarker, nil
		}},
		{"ListRuleGroups", TypeWAFRegionalRuleGroup, "rulegroup", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListRuleGroups(ctx, &wafregional.ListRuleGroupsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.RuleGroups))
			for _, g := range out.RuleGroups {
				items = append(items, wafItem{sv(g.RuleGroupId), sv(g.Name), g})
			}
			return items, out.NextMarker, nil
		}},
		{"ListRateBasedRules", TypeWAFRegionalRateBasedRule, "ratebasedrule", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListRateBasedRules(ctx, &wafregional.ListRateBasedRulesInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.Rules))
			for _, r := range out.Rules {
				items = append(items, wafItem{sv(r.RuleId), sv(r.Name), r})
			}
			return items, out.NextMarker, nil
		}},
		{"ListIPSets", TypeWAFRegionalIPSet, "ipset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListIPSets(ctx, &wafregional.ListIPSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.IPSets))
			for _, s := range out.IPSets {
				items = append(items, wafItem{sv(s.IPSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListByteMatchSets", TypeWAFRegionalByteMatchSet, "bytematchset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListByteMatchSets(ctx, &wafregional.ListByteMatchSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.ByteMatchSets))
			for _, s := range out.ByteMatchSets {
				items = append(items, wafItem{sv(s.ByteMatchSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListGeoMatchSets", TypeWAFRegionalGeoMatchSet, "geomatchset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListGeoMatchSets(ctx, &wafregional.ListGeoMatchSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.GeoMatchSets))
			for _, s := range out.GeoMatchSets {
				items = append(items, wafItem{sv(s.GeoMatchSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListRegexMatchSets", TypeWAFRegionalRegexMatchSet, "regexmatchset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListRegexMatchSets(ctx, &wafregional.ListRegexMatchSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.RegexMatchSets))
			for _, s := range out.RegexMatchSets {
				items = append(items, wafItem{sv(s.RegexMatchSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListRegexPatternSets", TypeWAFRegionalRegexPatternSet, "regexpatternset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListRegexPatternSets(ctx, &wafregional.ListRegexPatternSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.RegexPatternSets))
			for _, s := range out.RegexPatternSets {
				items = append(items, wafItem{sv(s.RegexPatternSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListSizeConstraintSets", TypeWAFRegionalSizeConstraintSet, "sizeconstraintset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListSizeConstraintSets(ctx, &wafregional.ListSizeConstraintSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.SizeConstraintSets))
			for _, s := range out.SizeConstraintSets {
				items = append(items, wafItem{sv(s.SizeConstraintSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListSqlInjectionMatchSets", TypeWAFRegionalSQLInjectionMatchSet, "sqlinjectionmatchset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListSqlInjectionMatchSets(ctx, &wafregional.ListSqlInjectionMatchSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.SqlInjectionMatchSets))
			for _, s := range out.SqlInjectionMatchSets {
				items = append(items, wafItem{sv(s.SqlInjectionMatchSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListXssMatchSets", TypeWAFRegionalXSSMatchSet, "xssmatchset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListXssMatchSets(ctx, &wafregional.ListXssMatchSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.XssMatchSets))
			for _, s := range out.XssMatchSets {
				items = append(items, wafItem{sv(s.XssMatchSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
	}
}

// scanWAFRegionalAssociations enumerates the ALB / API Gateway resources each
// web-ACL protects (ListResourcesForWebACL takes no marker — single call per
// resource type) and synthesizes one association row per protected resource.
func scanWAFRegionalAssociations(ctx context.Context, client wafRegionalAPI, acct *account, region string, st *store.Store, scanID string, webACLIDs []string) (int, int, error) {
	rtypes := []wafregionaltypes.ResourceType{
		wafregionaltypes.ResourceTypeApplicationLoadBalancer,
		wafregionaltypes.ResourceTypeApiGateway,
	}
	var batch []*store.Resource
	for _, id := range webACLIDs {
		webaclARN := wafRegionalARN(region, acct.ID, "webacl", id)
		for _, rt := range rtypes {
			wid := id
			out, err := client.ListResourcesForWebACL(ctx, &wafregional.ListResourcesForWebACLInput{
				WebACLId: &wid, ResourceType: rt,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "wafregional:ListResourcesForWebACL", acct.ID, region, err)
					continue
				}
				return 0, 0, fmt.Errorf("wafregional:ListResourcesForWebACL: %w", err)
			}
			for _, resArn := range out.ResourceArns {
				tail := resArn
				if idx := strings.LastIndex(resArn, "/"); idx >= 0 {
					tail = resArn[idx+1:]
				}
				native := webaclARN + "/association/" + tail
				rg := region
				label := resArn
				attrs := map[string]string{"WebACLId": id, "ResourceArn": resArn, "ResourceType": string(rt)}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeWAFRegionalWebACLAssociation, NativeID: native,
					Name: &label, Region: &rg,
					AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "wafregional web-acl-associations")
}
