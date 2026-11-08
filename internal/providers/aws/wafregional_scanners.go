package aws

import (
	"context"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/wafregional"
	wafregionaltypes "github.com/aws/aws-sdk-go-v2/service/wafregional/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWAFRegionalWebACL, Service: "wafregional"})
	registerType(restype.Descriptor{Type: TypeWAFRegionalRule, Service: "wafregional"})
	registerType(restype.Descriptor{Type: TypeWAFRegionalRuleGroup, Service: "wafregional"})
	registerType(restype.Descriptor{Type: TypeWAFRegionalRateBasedRule, Service: "wafregional"})
	registerType(restype.Descriptor{Type: TypeWAFRegionalIPSet, Service: "wafregional", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalByteMatchSet, Service: "wafregional", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalGeoMatchSet, Service: "wafregional", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalRegexMatchSet, Service: "wafregional", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalRegexPatternSet, Service: "wafregional", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalSizeConstraintSet, Service: "wafregional", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalSQLInjectionMatchSet, Service: "wafregional", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFRegionalXSSMatchSet, Service: "wafregional", Untaggable: true})
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
	ListTagsForResource(context.Context, *wafregional.ListTagsForResourceInput, ...func(*wafregional.Options)) (*wafregional.ListTagsForResourceOutput, error)
}

const (
	wafRegionalTagsOp  = "wafregional:ListTagsForResource"
	wafRegionalAssocOp = "wafregional:ListResourcesForWebACL"
)

// wafRegionalARN synthesizes a stable regional ARN for a WAF Classic resource,
// in the Service Reference's ARN format for the kinds that take tags.
// Regional List ops return only {Id, Name} with no ARN.
func wafRegionalARN(partition, region, account, kind, id string) string {
	return arnFor(partition, "waf-regional", region, account, kind+"/"+id)
}

// scanWAFRegional discovers WAF Classic (v1) regional resources per region,
// then the resources each web ACL protects (scanWAFRegionalAll).
func scanWAFRegional(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := wafregional.NewFromConfig(acct.cfg, func(o *wafregional.Options) { o.Region = region })
	return scanWAFRegionalAll(ctx, client, acct, region, st, scanID)
}

// scanWAFRegionalAll stores every regional WAF Classic listing of the
// account in region, as scanWAFClassic does for the global flavour (each
// listing its own phase; the rows of the types that take tags tagged through
// wafRegionalTags, their denials one warning for the scan), then, as a last
// phase, one association row per resource each stored web ACL protects
// (storeWAFRegionalAssociations).
func scanWAFRegionalAll(ctx context.Context, client wafRegionalAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	tags := wafRegionalTags(client).sharingDenials()
	flavour := wafFlavour{svc: "wafregional", region: &region, arn: func(kind, id string) string {
		return wafRegionalARN(acct.partition(), region, acct.ID, kind, id)
	}}
	var webACLs []childParent
	phases := wafListingPhases(ctx, st, acct, region, scanID, flavour, wafRegionalPhases(client), tags, &webACLs)
	phases = append(phases, countedPhase(0, wafRegionalAssocOp, func() (int, error) {
		return storeWAFRegionalAssociations(ctx, client, acct, region, st, scanID, webACLs)
	}))
	total, err := runHostPhases(ctx, st, acct, region, "wafregional", 0, phases)
	tags.warnDenials(st, acct, region)
	return total, err
}

// wafRegionalTags reads a row's tags with the regional flavour's
// ListTagsForResource (wafTagsVia).
func wafRegionalTags(client wafRegionalAPI) rowTagReader {
	return wafTagsVia(wafRegionalTagsOp, func(ctx context.Context, arn string, marker *string) ([]wafregionaltypes.Tag, *string, error) {
		out, err := client.ListTagsForResource(ctx, &wafregional.ListTagsForResourceInput{ResourceARN: &arn, Limit: wafPageCap, NextMarker: marker})
		if err != nil {
			return nil, nil, err
		}
		// A page without TagInfoForResource is read as holding no tags;
		// whether the service sends one is unverified live.
		var page []wafregionaltypes.Tag
		if info := out.TagInfoForResource; info != nil {
			page = info.TagList
		}
		return page, out.NextMarker, nil
	}, func(t wafregionaltypes.Tag) (*string, *string) { return t.Key, t.Value })
}

// wafRegionalPhases mirrors wafPhases for the regional SDK package. Same
// NextMarker/Limit pagination shape and page cap.
func wafRegionalPhases(c wafRegionalAPI) []wafPhase {
	return []wafPhase{
		{"ListWebACLs", TypeWAFRegionalWebACL, "webacl", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListWebACLs(ctx, &wafregional.ListWebACLsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListRules(ctx, &wafregional.ListRulesInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListRuleGroups(ctx, &wafregional.ListRuleGroupsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListRateBasedRules(ctx, &wafregional.ListRateBasedRulesInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListIPSets(ctx, &wafregional.ListIPSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListByteMatchSets(ctx, &wafregional.ListByteMatchSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListGeoMatchSets(ctx, &wafregional.ListGeoMatchSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListRegexMatchSets(ctx, &wafregional.ListRegexMatchSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListRegexPatternSets(ctx, &wafregional.ListRegexPatternSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListSizeConstraintSets(ctx, &wafregional.ListSizeConstraintSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListSqlInjectionMatchSets(ctx, &wafregional.ListSqlInjectionMatchSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListXssMatchSets(ctx, &wafregional.ListXssMatchSetsInput{Limit: wafPageCap, NextMarker: marker})
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

// wafRegionalAssocTypes are the resource types ListResourcesForWebACL is
// asked for, in the order they are read.
var wafRegionalAssocTypes = []wafregionaltypes.ResourceType{
	wafregionaltypes.ResourceTypeApplicationLoadBalancer,
	wafregionaltypes.ResourceTypeApiGateway,
}

// wafRegionalAssoc is one resource ListResourcesForWebACL listed for a web
// ACL: its ARN and the resource type it was asked for.
type wafRegionalAssoc struct {
	rtype wafregionaltypes.ResourceType
	arn   string
}

// storeWAFRegionalAssociations stores one association row per ALB / API
// Gateway resource each of webACLs protects. The web ACLs are read
// concurrently and each is classified on its own (storeTokenEachParent): one
// deleted since it was listed (WAFNonexistentItemException) is skipped,
// silently when nothing was read for it and with a warning when a resource
// was already read for it; a denial warns once, and any other error is
// returned after the rows are stored.
//
// ListResourcesForWebACL takes no marker and lists one resource type per
// call, so each type is read as one page of the web ACL's listing, the page's
// token naming the type (nil for the first): a failed type keeps the types
// read before it.
func storeWAFRegionalAssociations(ctx context.Context, client wafRegionalAPI, acct *account, region string, st *store.Store, scanID string, webACLs []childParent) (int, error) {
	ids := make([]string, len(webACLs))
	for i, w := range webACLs {
		ids[i] = w.id
	}
	page := func(ctx context.Context, id string, rtype *string) ([]wafRegionalAssoc, *string, error) {
		i := 0
		if rtype != nil {
			i = slices.Index(wafRegionalAssocTypes, wafregionaltypes.ResourceType(*rtype))
		}
		rt := wafRegionalAssocTypes[i]
		out, err := client.ListResourcesForWebACL(ctx, &wafregional.ListResourcesForWebACLInput{WebACLId: &id, ResourceType: rt})
		if err != nil {
			return nil, nil, err
		}
		got := make([]wafRegionalAssoc, len(out.ResourceArns))
		for j, arn := range out.ResourceArns {
			got[j] = wafRegionalAssoc{rt, arn}
		}
		var next *string
		if i+1 < len(wafRegionalAssocTypes) {
			next = sp(string(wafRegionalAssocTypes[i+1]))
		}
		return got, next, nil
	}
	row := func(id string, a wafRegionalAssoc) (*store.Resource, string, string) {
		return wafRegionalAssocRow(acct, region, scanID, id, a), "", ""
	}
	return storeTokenEachParent(ctx, st, acct, region, wafRegionalAssocOp, wafGone, ids, page, row)
}

// wafRegionalAssocRow is the association row of resource a, which web ACL id
// protects, keyed under the web ACL's ARN by the last segment of a's ARN.
func wafRegionalAssocRow(acct *account, region, scanID, id string, a wafRegionalAssoc) *store.Resource {
	tail := a.arn
	if idx := strings.LastIndex(a.arn, "/"); idx >= 0 {
		tail = a.arn[idx+1:]
	}
	label := a.arn
	attrs := map[string]string{"WebACLId": id, "ResourceArn": a.arn, "ResourceType": string(a.rtype)}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type:     TypeWAFRegionalWebACLAssociation,
		NativeID: wafRegionalARN(acct.partition(), region, acct.ID, "webacl", id) + "/association/" + tail,
		Name:     &label, Region: &region,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	}
}
