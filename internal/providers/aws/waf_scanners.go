package aws

import (
	"cmp"
	"context"

	"github.com/aws/aws-sdk-go-v2/service/waf"
	waftypes "github.com/aws/aws-sdk-go-v2/service/waf/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWAFWebACL, Service: "waf"})
	registerType(restype.Descriptor{Type: TypeWAFRule, Service: "waf"})
	registerType(restype.Descriptor{Type: TypeWAFRuleGroup, Service: "waf"})
	registerType(restype.Descriptor{Type: TypeWAFRateBasedRule, Service: "waf"})
	registerType(restype.Descriptor{Type: TypeWAFIPSet, Service: "waf", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFByteMatchSet, Service: "waf", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFGeoMatchSet, Service: "waf", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFRegexMatchSet, Service: "waf", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFRegexPatternSet, Service: "waf", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFSizeConstraintSet, Service: "waf", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFSQLInjectionMatchSet, Service: "waf", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeWAFXSSMatchSet, Service: "waf", Untaggable: true})
	registerService(serviceEntry{
		name:   "aws:waf",
		global: true,
		fn:     scanWAF,
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
	ListTagsForResource(context.Context, *waf.ListTagsForResourceInput, ...func(*waf.Options)) (*waf.ListTagsForResourceOutput, error)
}

const (
	wafTagsOp = "waf:ListTagsForResource"
	// wafPageCap is the PaginationLimit maximum (the model's range is 0-100)
	// of every WAF Classic listing and of ListTagsForResource.
	wafPageCap = 100
)

// wafGone is the not-found code both WAF Classic flavours' ListTagsForResource
// and wafregional's ListResourcesForWebACL model.
var wafGone = []string{"WAFNonexistentItemException"}

// wafTaggedKinds are the ARN kinds of the WAF Classic resources that take
// tags. The Service Reference lists exactly these under ListTagsForResource
// and TagResource, and only their Create* inputs carry Tags; the condition
// sets (IP, byte match, geo match, ...) take none.
var wafTaggedKinds = map[string]bool{"webacl": true, "rule": true, "rulegroup": true, "ratebasedrule": true}

// wafNoIDReasons says, per ARN kind, why a listed element of that kind
// without its id is not stored. Every kind wafPhases and wafRegionalPhases
// list has one.
var wafNoIDReasons = map[string]string{
	"webacl":               "web ACLs have no WebACLId",
	"rule":                 "rules have no RuleId",
	"rulegroup":            "rule groups have no RuleGroupId",
	"ratebasedrule":        "rate-based rules have no RuleId",
	"ipset":                "IP sets have no IPSetId",
	"bytematchset":         "byte match sets have no ByteMatchSetId",
	"geomatchset":          "geo match sets have no GeoMatchSetId",
	"regexmatchset":        "regex match sets have no RegexMatchSetId",
	"regexpatternset":      "regex pattern sets have no RegexPatternSetId",
	"sizeconstraintset":    "size constraint sets have no SizeConstraintSetId",
	"sqlinjectionmatchset": "SQL injection match sets have no SqlInjectionMatchSetId",
	"xssmatchset":          "XSS match sets have no XssMatchSetId",
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
func wafARN(partition, account, kind, id string) string {
	return arnFor(partition, "waf", "", account, kind+"/"+id)
}

// scanWAF discovers WAF Classic (v1) resources. The service is global — API
// hosted in one home region (homeRegion; us-east-1 for aws) — so it registers
// global=true, pins the client to that region, and stores resources with
// region "global".
func scanWAF(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, error) {
	region, err := acct.homeRegion("waf")
	if err != nil {
		return 0, err
	}
	client := waf.NewFromConfig(acct.cfg, func(o *waf.Options) { o.Region = region })
	return scanWAFClassic(ctx, client, acct, region, st, scanID)
}

// scanWAFClassic stores every WAF Classic listing of the account, region
// being the home region client calls, which scopes warnings and errors. Each
// listing runs as its own phase, one at a time: a phase's error does not stop
// the later ones; a failed write, ctx's end and an NXDOMAIN of the first
// listing with nothing stored do (runHostPhases). A listing
// keeps the pages read before a failed one and ends with an error on a
// repeated NextMarker. The rows of the types that take tags (wafTaggedKinds)
// have their tags read per row (wafTags), a page written before the next is
// read, and their denials are one warning for the whole scan.
func scanWAFClassic(ctx context.Context, client wafAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	tags := wafTags(client).sharingDenials()
	flavour := wafFlavour{svc: "waf", region: regionGlobal, arn: func(kind, id string) string { return wafARN(acct.partition(), acct.ID, kind, id) }}
	phases := wafListingPhases(ctx, st, acct, region, scanID, flavour, wafPhases(client), tags, nil)
	total, err := runHostPhases(ctx, st, acct, region, "waf", 0, phases)
	tags.warnDenials(st, acct, region)
	return total, err
}

// wafFlavour is what sets the rows of one WAF Classic flavour apart: svc,
// the SDK package its ops are reported under; region, the Region its rows
// are stored with; and arn, which builds a row's ARN from its kind and id.
type wafFlavour struct {
	svc    string
	region *string
	arn    func(kind, id string) string
}

// wafListingPhases is one hostPhase per listing of flavour, each reported
// under flavour.svc and the listing's op. The rows of the types that take
// tags (wafTaggedKinds) have their tags read through tags, a page written
// before the next is read; the others are written once their listing ends.
// webACLs, when set, is given the web ACLs stored, as the parents of a later
// phase.
func wafListingPhases(ctx context.Context, st *store.Store, acct *account, region, scanID string, flavour wafFlavour, listings []wafPhase, tags rowTagReader, webACLs *[]childParent) []hostPhase {
	phases := make([]hostPhase, len(listings))
	for i, ph := range listings {
		op := flavour.svc + ":" + ph.op
		row := func(it wafItem) (*store.Resource, string, string) { return wafRow(acct, scanID, flavour, ph, it) }
		phases[i] = hostPhase{op: op, run: func(p *phaseTotals) bool {
			switch {
			case ph.kind == "webacl" && webACLs != nil:
				var failed bool
				*webACLs, failed = storeParentPagesTags(ctx, st, acct, region, op, "web ACLs", "WebACLId", ph.list, row, func(it wafItem) string { return it.id }, tags, p)
				return failed
			case wafTaggedKinds[ph.kind]:
				return storeTokenPagesTags(ctx, st, acct, region, op, ph.list, row, tags, p)
			}
			total, err := storeTokenList(ctx, st, acct, region, op, ph.list, row)
			return p.addPhase(op, total, err)
		}}
	}
	return phases
}

// wafRow is the row of an element ph listed, stored under the ARN
// flavour.arn builds from its id, since the listings return none. An element
// without an id is not stored: its ARN would name no resource, and every such
// element would merge into one row.
func wafRow(acct *account, scanID string, flavour wafFlavour, ph wafPhase, it wafItem) (*store.Resource, string, string) {
	if it.id == "" {
		return nil, wafNoIDReasons[ph.kind], cmp.Or(it.name, noIdentifier)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: ph.discoType, NativeID: flavour.arn(ph.kind, it.id),
		Name: nonEmptyPtr(it.name), Region: flavour.region, AttributesJSON: mustJSON(it.attrs), DiscoveredBy: scanID,
	}, "", ""
}

// wafTags reads a row's tags with the global flavour's ListTagsForResource
// (wafTagsVia).
func wafTags(client wafAPI) rowTagReader {
	return wafTagsVia(wafTagsOp, func(ctx context.Context, arn string, marker *string) ([]waftypes.Tag, *string, error) {
		out, err := client.ListTagsForResource(ctx, &waf.ListTagsForResourceInput{ResourceARN: &arn, Limit: wafPageCap, NextMarker: marker})
		if err != nil {
			return nil, nil, err
		}
		// A page without TagInfoForResource is read as holding no tags;
		// whether the service sends one is unverified live.
		var page []waftypes.Tag
		if info := out.TagInfoForResource; info != nil {
			page = info.TagList
		}
		return page, out.NextMarker, nil
	}, func(t waftypes.Tag) (*string, *string) { return t.Key, t.Value })
}

// wafTagsVia reads a row's tags with op, a WAF Classic flavour's
// ListTagsForResource, whose page reads one page of them, sent the row's
// NativeID: the ARN built from the listed id, which is in the Service
// Reference's ARN format for every type read here. Every page is read, and a
// repeated NextMarker fails the read. WAFNonexistentItemException, the op's
// modeled not-found, drops a resource deleted after it was listed.
func wafTagsVia[Tag any](op string, page func(ctx context.Context, arn string, marker *string) ([]Tag, *string, error), kv func(Tag) (key, value *string)) rowTagReader {
	return rowTagReader{
		op:       op,
		notFound: wafGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			tags, err := readTokenPages(ctx, func(ctx context.Context, marker *string) ([]Tag, *string, error) {
				return page(ctx, r.NativeID, marker)
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, kv), nil
		},
	}
}

// wafPhases maps each WAF Classic List op into a uniform wafPhase. Uses
// NextMarker/Limit pagination (no New*Paginator); Limit=wafPageCap dodges the
// zero-limit footgun.
func wafPhases(c wafAPI) []wafPhase {
	return []wafPhase{
		{"ListWebACLs", TypeWAFWebACL, "webacl", func(ctx context.Context, marker *string) ([]wafItem, *string, error) {
			out, err := c.ListWebACLs(ctx, &waf.ListWebACLsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListRules(ctx, &waf.ListRulesInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListRuleGroups(ctx, &waf.ListRuleGroupsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListRateBasedRules(ctx, &waf.ListRateBasedRulesInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListIPSets(ctx, &waf.ListIPSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListByteMatchSets(ctx, &waf.ListByteMatchSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListGeoMatchSets(ctx, &waf.ListGeoMatchSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListRegexMatchSets(ctx, &waf.ListRegexMatchSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListRegexPatternSets(ctx, &waf.ListRegexPatternSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListSizeConstraintSets(ctx, &waf.ListSizeConstraintSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListSqlInjectionMatchSets(ctx, &waf.ListSqlInjectionMatchSetsInput{Limit: wafPageCap, NextMarker: marker})
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
			out, err := c.ListXssMatchSets(ctx, &waf.ListXssMatchSetsInput{Limit: wafPageCap, NextMarker: marker})
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
