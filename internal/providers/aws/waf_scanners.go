package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/waf"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:waf",
		global: true,
		fn:     scanWAF,
		emits: []coverage.TypeDecl{
			{Service: "waf", DiscoType: TypeWAFWebACL, Leaf: true},
			{Service: "waf", DiscoType: TypeWAFRule, Leaf: true},
			{Service: "waf", DiscoType: TypeWAFRuleGroup, Leaf: true},
			{Service: "waf", DiscoType: TypeWAFRateBasedRule, Leaf: true},
			{Service: "waf", DiscoType: TypeWAFIPSet, Leaf: true},
			{Service: "waf", DiscoType: TypeWAFByteMatchSet, Leaf: true},
			{Service: "waf", DiscoType: TypeWAFGeoMatchSet, Leaf: true},
			{Service: "waf", DiscoType: TypeWAFRegexMatchSet, Leaf: true},
			{Service: "waf", DiscoType: TypeWAFRegexPatternSet, Leaf: true},
			{Service: "waf", DiscoType: TypeWAFSizeConstraintSet, Leaf: true},
			{Service: "waf", DiscoType: TypeWAFSQLInjectionMatchSet, Leaf: true},
			{Service: "waf", DiscoType: TypeWAFXSSMatchSet, Leaf: true},
		},
	})
}

// wafAPI is the narrow set of WAF Classic (global / CloudFront scope) list ops
// the scanner calls. *waf.Client satisfies it; tests inject stubs.
type wafAPI interface {
	ListWebACLs(context.Context, *waf.ListWebACLsInput, ...func(*waf.Options)) (*waf.ListWebACLsOutput, error)
	ListRules(context.Context, *waf.ListRulesInput, ...func(*waf.Options)) (*waf.ListRulesOutput, error)
	ListRuleGroups(context.Context, *waf.ListRuleGroupsInput, ...func(*waf.Options)) (*waf.ListRuleGroupsOutput, error)
	ListRateBasedRules(context.Context, *waf.ListRateBasedRulesInput, ...func(*waf.Options)) (*waf.ListRateBasedRulesOutput, error)
	ListIPSets(context.Context, *waf.ListIPSetsInput, ...func(*waf.Options)) (*waf.ListIPSetsOutput, error)
	ListByteMatchSets(context.Context, *waf.ListByteMatchSetsInput, ...func(*waf.Options)) (*waf.ListByteMatchSetsOutput, error)
	ListGeoMatchSets(context.Context, *waf.ListGeoMatchSetsInput, ...func(*waf.Options)) (*waf.ListGeoMatchSetsOutput, error)
	ListRegexMatchSets(context.Context, *waf.ListRegexMatchSetsInput, ...func(*waf.Options)) (*waf.ListRegexMatchSetsOutput, error)
	ListRegexPatternSets(context.Context, *waf.ListRegexPatternSetsInput, ...func(*waf.Options)) (*waf.ListRegexPatternSetsOutput, error)
	ListSizeConstraintSets(context.Context, *waf.ListSizeConstraintSetsInput, ...func(*waf.Options)) (*waf.ListSizeConstraintSetsOutput, error)
	ListSqlInjectionMatchSets(context.Context, *waf.ListSqlInjectionMatchSetsInput, ...func(*waf.Options)) (*waf.ListSqlInjectionMatchSetsOutput, error)
	ListXssMatchSets(context.Context, *waf.ListXssMatchSetsInput, ...func(*waf.Options)) (*waf.ListXssMatchSetsOutput, error)
}

// wafItem is the (id, name, raw-summary) tuple every WAF Classic List op
// reduces to. attrs is the raw SDK summary, stored verbatim as AttributesJSON.
type wafItem struct {
	id    string
	name  string
	attrs any
}

// wafPhase pairs one List op (mapped to wafItems) with the disco type + ARN
// kind segment it produces.
type wafPhase struct {
	op        string
	discoType string
	kind      string
	list      func(ctx context.Context, marker *string) ([]wafItem, *string, error)
}

// wafARN synthesizes a stable global ARN for a WAF Classic resource. List ops
// return only {Id, Name} with no ARN, so we build the documented global-scope
// shape (empty region segment).
func wafARN(account, kind, id string) string {
	return fmt.Sprintf("arn:aws:waf::%s:%s/%s", account, kind, id)
}

// scanWAF discovers WAF Classic (v1) resources. The service is global — API
// hosted only in us-east-1 — so it registers global=true, pins the client to
// us-east-1, and stores resources with region "global".
func scanWAF(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := waf.NewFromConfig(acct.cfg, func(o *waf.Options) { o.Region = region })
	arnFn := func(kind, id string) string { return wafARN(acct.ID, kind, id) }
	t, i, _, ferr := scanWAFPhases(ctx, wafPhases(client), acct, region, regionGlobal, st, scanID, "waf", arnFn)
	return t, i, ferr
}

// wafPhases maps each WAF Classic List op into a uniform wafPhase. Uses
// NextMarker/Limit pagination (no New*Paginator); Limit=100 dodges the
// zero-limit footgun, wafCollect walks NextMarker.
func wafPhases(c wafAPI) []wafPhase {
	const limit = 100
	return []wafPhase{
		{"ListWebACLs", TypeWAFWebACL, "webacl", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListWebACLs(ctx, &waf.ListWebACLsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.WebACLs))
			for _, w := range out.WebACLs {
				items = append(items, wafItem{sv(w.WebACLId), sv(w.Name), w})
			}
			return items, out.NextMarker, nil
		}},
		{"ListRules", TypeWAFRule, "rule", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListRules(ctx, &waf.ListRulesInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.Rules))
			for _, r := range out.Rules {
				items = append(items, wafItem{sv(r.RuleId), sv(r.Name), r})
			}
			return items, out.NextMarker, nil
		}},
		{"ListRuleGroups", TypeWAFRuleGroup, "rulegroup", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListRuleGroups(ctx, &waf.ListRuleGroupsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.RuleGroups))
			for _, g := range out.RuleGroups {
				items = append(items, wafItem{sv(g.RuleGroupId), sv(g.Name), g})
			}
			return items, out.NextMarker, nil
		}},
		{"ListRateBasedRules", TypeWAFRateBasedRule, "ratebasedrule", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListRateBasedRules(ctx, &waf.ListRateBasedRulesInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.Rules))
			for _, r := range out.Rules {
				items = append(items, wafItem{sv(r.RuleId), sv(r.Name), r})
			}
			return items, out.NextMarker, nil
		}},
		{"ListIPSets", TypeWAFIPSet, "ipset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListIPSets(ctx, &waf.ListIPSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.IPSets))
			for _, s := range out.IPSets {
				items = append(items, wafItem{sv(s.IPSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListByteMatchSets", TypeWAFByteMatchSet, "bytematchset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListByteMatchSets(ctx, &waf.ListByteMatchSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.ByteMatchSets))
			for _, s := range out.ByteMatchSets {
				items = append(items, wafItem{sv(s.ByteMatchSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListGeoMatchSets", TypeWAFGeoMatchSet, "geomatchset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListGeoMatchSets(ctx, &waf.ListGeoMatchSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.GeoMatchSets))
			for _, s := range out.GeoMatchSets {
				items = append(items, wafItem{sv(s.GeoMatchSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListRegexMatchSets", TypeWAFRegexMatchSet, "regexmatchset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListRegexMatchSets(ctx, &waf.ListRegexMatchSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.RegexMatchSets))
			for _, s := range out.RegexMatchSets {
				items = append(items, wafItem{sv(s.RegexMatchSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListRegexPatternSets", TypeWAFRegexPatternSet, "regexpatternset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListRegexPatternSets(ctx, &waf.ListRegexPatternSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.RegexPatternSets))
			for _, s := range out.RegexPatternSets {
				items = append(items, wafItem{sv(s.RegexPatternSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListSizeConstraintSets", TypeWAFSizeConstraintSet, "sizeconstraintset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListSizeConstraintSets(ctx, &waf.ListSizeConstraintSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.SizeConstraintSets))
			for _, s := range out.SizeConstraintSets {
				items = append(items, wafItem{sv(s.SizeConstraintSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListSqlInjectionMatchSets", TypeWAFSQLInjectionMatchSet, "sqlinjectionmatchset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListSqlInjectionMatchSets(ctx, &waf.ListSqlInjectionMatchSetsInput{Limit: limit, NextMarker: marker})
			if err != nil {
				return nil, nil, err
			}
			items := make([]wafItem, 0, len(out.SqlInjectionMatchSets))
			for _, s := range out.SqlInjectionMatchSets {
				items = append(items, wafItem{sv(s.SqlInjectionMatchSetId), sv(s.Name), s})
			}
			return items, out.NextMarker, nil
		}},
		{"ListXssMatchSets", TypeWAFXSSMatchSet, "xssmatchset", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListXssMatchSets(ctx, &waf.ListXssMatchSetsInput{Limit: limit, NextMarker: marker})
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

// wafCollect walks the NextMarker pagination loop for one List op, accumulating
// every page's items.
func wafCollect(ctx context.Context, page func(ctx context.Context, marker *string) ([]wafItem, *string, error)) ([]wafItem, error) {
	var out []wafItem
	var marker *string
	for {
		items, next, err := page(ctx, marker)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if next == nil || sv(next) == "" {
			return out, nil
		}
		marker = next
	}
}

// scanWAFPhases runs every phase for one WAF Classic flavour (global or
// regional). regionScope tags the warning/error scope; regionPtr is the
// resource Region column ("global" for the global flavour). Returns the
// scanned web-ACL ids so the regional caller can fan out ListResourcesForWebACL.
func scanWAFPhases(ctx context.Context, phases []wafPhase, acct *account, regionScope string, regionPtr *string, st *store.Store, scanID, svcPrefix string, arnFn func(kind, id string) string) (total, inserted int, webACLIDs []string, err error) {
	for _, ph := range phases {
		items, lerr := wafCollect(ctx, ph.list)
		if lerr != nil {
			if isAccessDenied(lerr) {
				_ = skipIfAccessDenied(st, svcPrefix+":"+ph.op, acct.ID, regionScope, lerr)
				continue
			}
			return total, inserted, webACLIDs, fmt.Errorf("%s:%s: %w", svcPrefix, ph.op, lerr)
		}
		if ph.kind == "webacl" {
			for _, it := range items {
				if it.id != "" {
					webACLIDs = append(webACLIDs, it.id)
				}
			}
		}
		t, i, uerr := upsertWAFItems(st, acct, scanID, items, ph.discoType, ph.kind, regionPtr, arnFn)
		if uerr != nil {
			return total, inserted, webACLIDs, uerr
		}
		total += t
		inserted += i
	}
	return total, inserted, webACLIDs, nil
}

// upsertWAFItems builds + upserts the resource batch for one phase's items.
func upsertWAFItems(st *store.Store, acct *account, scanID string, items []wafItem, discoType, kind string, regionPtr *string, arnFn func(kind, id string) string) (int, int, error) {
	batch := make([]*store.Resource, 0, len(items))
	for _, it := range items {
		if it.id == "" {
			continue
		}
		res := &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: discoType, NativeID: arnFn(kind, it.id),
			Region: regionPtr, AttributesJSON: mustJSON(it.attrs), DiscoveredBy: scanID,
		}
		if it.name != "" {
			nm := it.name
			res.Name = &nm
		}
		batch = append(batch, res)
	}
	return upsertBatch(st, batch, discoType)
}
