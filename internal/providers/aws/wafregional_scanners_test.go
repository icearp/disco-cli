package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/wafregional"
	wafregionaltypes "github.com/aws/aws-sdk-go-v2/service/wafregional/types"
	"github.com/icearp/disco-cli/store"
)

// wafRegionalTestListed is every listed row of the fixture: two (one per
// page) for each of the twelve listings.
const wafRegionalTestListed = 12 * 2

// wafRegionalTestTotal adds the fixture's association rows: one load
// balancer and one API Gateway stage for each of the two web ACLs.
const wafRegionalTestTotal = wafRegionalTestListed + 2*2

// The op labels, written out so that a wrong constant fails the tests.
const (
	wafRegionalTestTagsOp  = "wafregional:ListTagsForResource"
	wafRegionalTestAssocOp = "wafregional:ListResourcesForWebACL"
)

// wafRegionalTestKind is one regional WAF Classic listing: its op, the type
// it stores, the ARN kind of its rows, the element field holding the id,
// whether its rows take tags, and page, which builds the op's output holding
// one element and next.
type wafRegionalTestKind struct {
	op, typ, kind, idField string
	tagged                 bool
	page                   func(id, name, next *string) any
}

func wafRegionalTestKinds() []wafRegionalTestKind {
	return []wafRegionalTestKind{
		{"ListWebACLs", TypeWAFRegionalWebACL, "webacl", "WebACLId", true, func(id, name, next *string) any {
			return &wafregional.ListWebACLsOutput{NextMarker: next, WebACLs: []wafregionaltypes.WebACLSummary{{WebACLId: id, Name: name}}}
		}},
		{"ListRules", TypeWAFRegionalRule, "rule", "RuleId", true, func(id, name, next *string) any {
			return &wafregional.ListRulesOutput{NextMarker: next, Rules: []wafregionaltypes.RuleSummary{{RuleId: id, Name: name}}}
		}},
		{"ListRuleGroups", TypeWAFRegionalRuleGroup, "rulegroup", "RuleGroupId", true, func(id, name, next *string) any {
			return &wafregional.ListRuleGroupsOutput{NextMarker: next, RuleGroups: []wafregionaltypes.RuleGroupSummary{{RuleGroupId: id, Name: name}}}
		}},
		{"ListRateBasedRules", TypeWAFRegionalRateBasedRule, "ratebasedrule", "RuleId", true, func(id, name, next *string) any {
			return &wafregional.ListRateBasedRulesOutput{NextMarker: next, Rules: []wafregionaltypes.RuleSummary{{RuleId: id, Name: name}}}
		}},
		{"ListIPSets", TypeWAFRegionalIPSet, "ipset", "IPSetId", false, func(id, name, next *string) any {
			return &wafregional.ListIPSetsOutput{NextMarker: next, IPSets: []wafregionaltypes.IPSetSummary{{IPSetId: id, Name: name}}}
		}},
		{"ListByteMatchSets", TypeWAFRegionalByteMatchSet, "bytematchset", "ByteMatchSetId", false, func(id, name, next *string) any {
			return &wafregional.ListByteMatchSetsOutput{NextMarker: next, ByteMatchSets: []wafregionaltypes.ByteMatchSetSummary{{ByteMatchSetId: id, Name: name}}}
		}},
		{"ListGeoMatchSets", TypeWAFRegionalGeoMatchSet, "geomatchset", "GeoMatchSetId", false, func(id, name, next *string) any {
			return &wafregional.ListGeoMatchSetsOutput{NextMarker: next, GeoMatchSets: []wafregionaltypes.GeoMatchSetSummary{{GeoMatchSetId: id, Name: name}}}
		}},
		{"ListRegexMatchSets", TypeWAFRegionalRegexMatchSet, "regexmatchset", "RegexMatchSetId", false, func(id, name, next *string) any {
			return &wafregional.ListRegexMatchSetsOutput{NextMarker: next, RegexMatchSets: []wafregionaltypes.RegexMatchSetSummary{{RegexMatchSetId: id, Name: name}}}
		}},
		{"ListRegexPatternSets", TypeWAFRegionalRegexPatternSet, "regexpatternset", "RegexPatternSetId", false, func(id, name, next *string) any {
			return &wafregional.ListRegexPatternSetsOutput{NextMarker: next, RegexPatternSets: []wafregionaltypes.RegexPatternSetSummary{{RegexPatternSetId: id, Name: name}}}
		}},
		{"ListSizeConstraintSets", TypeWAFRegionalSizeConstraintSet, "sizeconstraintset", "SizeConstraintSetId", false, func(id, name, next *string) any {
			return &wafregional.ListSizeConstraintSetsOutput{NextMarker: next, SizeConstraintSets: []wafregionaltypes.SizeConstraintSetSummary{{SizeConstraintSetId: id, Name: name}}}
		}},
		{"ListSqlInjectionMatchSets", TypeWAFRegionalSQLInjectionMatchSet, "sqlinjectionmatchset", "SqlInjectionMatchSetId", false, func(id, name, next *string) any {
			return &wafregional.ListSqlInjectionMatchSetsOutput{NextMarker: next, SqlInjectionMatchSets: []wafregionaltypes.SqlInjectionMatchSetSummary{{SqlInjectionMatchSetId: id, Name: name}}}
		}},
		{"ListXssMatchSets", TypeWAFRegionalXSSMatchSet, "xssmatchset", "XssMatchSetId", false, func(id, name, next *string) any {
			return &wafregional.ListXssMatchSetsOutput{NextMarker: next, XssMatchSets: []wafregionaltypes.XssMatchSetSummary{{XssMatchSetId: id, Name: name}}}
		}},
	}
}

func wafRegionalTestKindOf(t *testing.T, op string) wafRegionalTestKind {
	t.Helper()
	for _, k := range wafRegionalTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no regional WAF Classic listing", op)
	return wafRegionalTestKind{}
}

// id is the id of the row on page n (1 or 2) of k's listing.
func (k wafRegionalTestKind) id(n string) string { return k.kind + "-" + n }

// arn is the ARN of the row on page n of k's listing in partition and
// region: for the four kinds that take tags, the Service Reference's
// arn:${Partition}:waf-regional:${Region}:${Account}:<kind>/${Id}.
func (k wafRegionalTestKind) arn(partition, region, n string) string {
	return "arn:" + partition + ":waf-regional:" + region + ":" + testAccountID + ":" + k.kind + "/" + k.id(n)
}

// wafRegionalTestTaggedARNs is the ARN of every row whose tags are read, sorted.
func wafRegionalTestTaggedARNs(partition, region string) []string {
	var arns []string
	for _, k := range wafRegionalTestKinds() {
		if k.tagged {
			arns = append(arns, k.arn(partition, region, "1"), k.arn(partition, region, "2"))
		}
	}
	slices.Sort(arns)
	return arns
}

// wafRegionalTestLB and wafRegionalTestStage are the load balancer and API
// Gateway stage the fixture's web ACL id protects.
func wafRegionalTestLB(id string) string {
	return "arn:aws:elasticloadbalancing:us-east-1:" + testAccountID + ":loadbalancer/app/lb-" + id + "/lbid-" + id
}

func wafRegionalTestStage(id string) string {
	return "arn:aws:apigateway:us-east-1::/restapis/api-" + id + "/stages/stage-" + id
}

// wafRegionalCalls records the calls of a scan and answers the tag reads, by
// ARN, with tags or tagErrs, and ListResourcesForWebACL, by "<web ACL id>
// <resource type>", with the fixture's resources or assocErrs.
type wafRegionalCalls struct {
	mu        sync.Mutex
	limits    map[string][]int32
	tagARNs   []string
	assocs    []string
	tags      map[string]map[string]string
	tagErrs   map[string]error
	assocErrs map[string]error
}

// newWAFRegionalCalls tags the page-1 row of every tagged listing with its kind.
func newWAFRegionalCalls(partition, region string) *wafRegionalCalls {
	c := &wafRegionalCalls{limits: map[string][]int32{}, tags: map[string]map[string]string{}, tagErrs: map[string]error{}, assocErrs: map[string]error{}}
	for _, k := range wafRegionalTestKinds() {
		if k.tagged {
			c.tags[k.arn(partition, region, "1")] = map[string]string{"kind": k.kind}
		}
	}
	return c
}

func (c *wafRegionalCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limits[op] = append(c.limits[op], bacField[int32](in, "Limit"))
}

func (c *wafRegionalCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.limits[op])
}

func (c *wafRegionalCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *wafRegionalCalls) sortedAssocs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.assocs))
}

func (c *wafRegionalCalls) listTags(in any) (any, error) {
	arn := sv(in.(*wafregional.ListTagsForResourceInput).ResourceARN)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	info := &wafregionaltypes.TagInfoForResource{ResourceARN: &arn, TagList: []wafregionaltypes.Tag{}}
	for k, v := range c.tags[arn] {
		info.TagList = append(info.TagList, wafregionaltypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &wafregional.ListTagsForResourceOutput{TagInfoForResource: info}, nil
}

func (c *wafRegionalCalls) listResources(in any) (any, error) {
	req := in.(*wafregional.ListResourcesForWebACLInput)
	id := sv(req.WebACLId)
	key := id + " " + string(req.ResourceType)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.assocs = append(c.assocs, key)
	if err := c.assocErrs[key]; err != nil {
		return nil, err
	}
	arn := wafRegionalTestLB(id)
	if req.ResourceType == wafregionaltypes.ResourceTypeApiGateway {
		arn = wafRegionalTestStage(id)
	}
	return &wafregional.ListResourcesForWebACLOutput{ResourceArns: []string{arn}}, nil
}

// twoPages answers k's op with row 1 on the first page and row 2 on the
// second.
func (c *wafRegionalCalls) twoPages(k wafRegionalTestKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		n, next := "1", sdkaws.String("p2")
		if bacField[*string](in, "NextMarker") != nil {
			n, next = "2", nil
		}
		id, name := k.id(n), "name-"+k.id(n)
		return k.page(&id, &name, next), nil
	}
}

// wafRegionalHandlers answers every op the scan calls: each listing in two
// pages, and the tag reads and association listings from c.
func wafRegionalHandlers(c *wafRegionalCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource":    c.listTags,
		"ListResourcesForWebACL": c.listResources,
	}
	for _, k := range wafRegionalTestKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// wafRegionalScan runs the scan's real entry point in us-east-1 against
// handlers; every op they do not answer is denied.
func wafRegionalScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, wafTestDenied)
	return scanWAFRegional(ctx, acct, testRegion, st, testScanID)
}

// wafRegionalAssocARN is the NativeID of the association of the resource
// whose ARN ends in tail with web ACL id.
func wafRegionalAssocARN(id, tail string) string {
	return "arn:aws:waf-regional:us-east-1:" + testAccountID + ":webacl/" + id + "/association/" + tail
}

// Every listing stores both its pages, each row under the ARN built from its
// id, named by its name, with the scan's region, no Status or CreatedAt, and
// its attributes the listed element. Only the web ACLs, rules, rule groups
// and rate-based rules have their tags read (nil for none):
// ListTagsForResource is sent exactly their ARNs. Every listing and tag read
// is sent the model's Limit cap of 100. Each stored web ACL is asked for its
// load balancers and API Gateway stages, one association row each. No
// error, no warning.
func TestScanWAFRegional_StoresEveryListingWithTags(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newWAFRegionalCalls("aws", testRegion)

	total, inserted, err := wafRegionalScan(context.Background(), st, wafRegionalHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != wafRegionalTestTotal || inserted != wafRegionalTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, wafRegionalTestTotal, wafRegionalTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if _, ok := rowsByNativeID(t, st, TypeWAFRegionalWebACL)["arn:aws:waf-regional:us-east-1:123456789012:webacl/webacl-1"]; !ok {
		t.Errorf("web ACL not stored under its Service Reference ARN")
	}
	for _, k := range wafRegionalTestKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", k.typ, len(rows))
		}
		for _, n := range []string{"1", "2"} {
			arn := k.arn("aws", testRegion, n)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != "name-"+k.id(n) || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %v, %v; want %q, %s, nil, nil", arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, "name-"+k.id(n), testRegion)
			}
			if r.AccountID != testAccountID || sv(r.AccountName) != "Test Account" || r.ManagedByProvider {
				t.Errorf("%s AccountID, AccountName, ManagedByProvider = %q, %q, %v; want %s, Test Account, false", arn, r.AccountID, sv(r.AccountName), r.ManagedByProvider, testAccountID)
			}
			var attrs map[string]string
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[k.idField] != k.id(n) || attrs["Name"] != "name-"+k.id(n) {
				t.Errorf("%s attrs = %s (%v); want the listed element", arn, r.AttributesJSON, err)
			}
			if k.tagged && n == "1" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"kind": k.kind})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if n := calls.count(k.op); n != 2 {
			t.Errorf("%s called %d times; want 2", k.op, n)
		}
	}
	if got, want := calls.sortedTagARNs(), wafRegionalTestTaggedARNs("aws", testRegion); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
	for op, limits := range calls.limits {
		for _, n := range limits {
			if n != 100 {
				t.Errorf("%s Limit = %d; want 100", op, n)
			}
		}
	}
	wantAssocCalls := []string{"webacl-1 API_GATEWAY", "webacl-1 APPLICATION_LOAD_BALANCER", "webacl-2 API_GATEWAY", "webacl-2 APPLICATION_LOAD_BALANCER"}
	if got := calls.sortedAssocs(); !slices.Equal(got, wantAssocCalls) {
		t.Errorf("ListResourcesForWebACL calls = %v; want %v", got, wantAssocCalls)
	}
	assocs := rowsByNativeID(t, st, TypeWAFRegionalWebACLAssociation)
	if len(assocs) != 4 {
		t.Errorf("association rows = %d; want 4", len(assocs))
	}
	for _, id := range []string{"webacl-1", "webacl-2"} {
		for _, c := range []struct{ resARN, tail, rtype string }{
			{wafRegionalTestLB(id), "lbid-" + id, "APPLICATION_LOAD_BALANCER"},
			{wafRegionalTestStage(id), "stage-" + id, "API_GATEWAY"},
		} {
			native := wafRegionalAssocARN(id, c.tail)
			r, ok := assocs[native]
			if !ok {
				t.Errorf("association %s missing", native)
				continue
			}
			if sv(r.Name) != c.resARN || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil || r.TagsJSON != nil || sv(r.AccountName) != "Test Account" || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, TagsJSON, AccountName, Managed = %q, %q, %v, %v, %v, %q, %v", native, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.TagsJSON, sv(r.AccountName), r.ManagedByProvider)
			}
			var attrs map[string]string
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs: %v", native, err)
			}
			if want := map[string]string{"WebACLId": id, "ResourceArn": c.resARN, "ResourceType": c.rtype}; !reflect.DeepEqual(attrs, want) {
				t.Errorf("%s attrs = %v; want %v", native, attrs, want)
			}
		}
	}
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanWAFRegional_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := wafRegionalScan(context.Background(), st, wafRegionalHandlers(newWAFRegionalCalls("aws", testRegion))); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := wafRegionalScan(context.Background(), st, wafRegionalHandlers(newWAFRegionalCalls("aws", testRegion)))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != wafRegionalTestTotal {
		t.Errorf("rescan total = %d; want %d", total, wafRegionalTestTotal)
	}
}

// Empty listings: nothing stored, no tag read, no association listing, no
// error, no warning.
func TestScanWAFRegional_NoResources(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newWAFRegionalCalls("aws", testRegion)
	handlers := wafRegionalHandlers(calls)
	for _, k := range wafRegionalTestKinds() {
		handlers[k.op] = func(in any) (any, error) {
			calls.record(k.op, in)
			// The op's zero output: no element, no NextMarker.
			return reflect.New(reflect.TypeOf(k.page(nil, nil, nil)).Elem()).Interface(), nil
		}
	}

	total, inserted, err := wafRegionalScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource called %d times; want 0", n)
	}
	if got := calls.sortedAssocs(); len(got) != 0 {
		t.Errorf("ListResourcesForWebACL calls = %v; want none", got)
	}
	for _, k := range wafRegionalTestKinds() {
		if n := calls.count(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
	}
}

// A failed tag read. WAFNonexistentItemException is modeled, so a resource
// deleted since it was listed is dropped silently; a dropped web ACL has no
// associations read. A denial keeps the row untagged and warns once under
// the tag op with the scan's scope; a throttle keeps it and is transient;
// any other error keeps it and is an error.
func TestScanWAFRegional_TagReadFailures(t *testing.T) {
	t.Parallel()
	arn := wafRegionalTestKindOf(t, "ListWebACLs").arn("aws", testRegion, "1")
	for _, c := range []struct {
		name       string
		err        error
		outcome    serviceOutcome // -1: no error
		code       string
		wantTotal  int
		wantKept   bool
		warn       bool
		wantAssocs int // ListResourcesForWebACL calls
	}{
		{"not found", wafTestGone, -1, "", wafRegionalTestTotal - 3, false, false, 2},
		{"denied", wafTestDenied, -1, "", wafRegionalTestTotal, true, true, 4},
		{"transient", wafTestThrottle, outcomeTransient, "ThrottlingException", wafRegionalTestTotal, true, false, 4},
		{"other error", wafTestBad, outcomeError, "WAFInvalidParameterException", wafRegionalTestTotal, true, false, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newWAFRegionalCalls("aws", testRegion)
			calls.tagErrs[arn] = c.err

			total, _, err := wafRegionalScan(context.Background(), st, wafRegionalHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, ok := rowsByNativeID(t, st, TypeWAFRegionalWebACL)[arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if got := len(calls.sortedAssocs()); got != c.wantAssocs {
				t.Errorf("ListResourcesForWebACL calls = %d; want %d", got, c.wantAssocs)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{wafRegionalTestTagsOp}
			}
			if got := wafWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// ListTagsForResource pages are all read, each sent the Limit cap.
func TestScanWAFRegional_TagPages(t *testing.T) {
	t.Parallel()
	arn := wafRegionalTestKindOf(t, "ListRuleGroups").arn("aws", testRegion, "2")
	st := newTestStore(t)
	calls := newWAFRegionalCalls("aws", testRegion)
	handlers := wafRegionalHandlers(calls)
	var markers []string
	var limits []int32
	handlers["ListTagsForResource"] = func(in any) (any, error) {
		req := in.(*wafregional.ListTagsForResourceInput)
		if sv(req.ResourceARN) != arn {
			return calls.listTags(in)
		}
		calls.mu.Lock()
		markers = append(markers, sv(req.NextMarker))
		limits = append(limits, req.Limit)
		calls.mu.Unlock()
		next, k, v := sdkaws.String("t2"), "a", "1"
		if req.NextMarker != nil {
			next, k, v = nil, "b", "2"
		}
		return &wafregional.ListTagsForResourceOutput{NextMarker: next, TagInfoForResource: &wafregionaltypes.TagInfoForResource{
			ResourceARN: &arn, TagList: []wafregionaltypes.Tag{{Key: sdkaws.String(k), Value: sdkaws.String(v)}},
		}}, nil
	}

	if _, _, err := wafRegionalScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(markers, []string{"", "t2"}) || !slices.Equal(limits, []int32{100, 100}) {
		t.Errorf("NextMarkers, Limits = %q, %v; want [\"\" t2], [100 100]", markers, limits)
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeWAFRegionalRuleGroup)[arn].TagsJSON, map[string]string{"a": "1", "b": "2"})
}

// A tag response without TagInfoForResource reads as no tags.
func TestScanWAFRegional_TagResponseWithoutInfo(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newWAFRegionalCalls("aws", testRegion)
	handlers := wafRegionalHandlers(calls)
	arn := wafRegionalTestKindOf(t, "ListRules").arn("aws", testRegion, "1")
	handlers["ListTagsForResource"] = func(in any) (any, error) {
		if sv(in.(*wafregional.ListTagsForResourceInput).ResourceARN) == arn {
			return &wafregional.ListTagsForResourceOutput{}, nil
		}
		return calls.listTags(in)
	}

	if _, _, err := wafRegionalScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeWAFRegionalRule)[arn]
	if !ok {
		t.Fatalf("%s not stored", arn)
	}
	if r.TagsJSON != nil {
		t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
	}
}

// Denied tag reads in two phases send one warning for the whole scan; the
// rows are kept.
func TestScanWAFRegional_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newWAFRegionalCalls("aws", testRegion)
	calls.tagErrs[wafRegionalTestKindOf(t, "ListWebACLs").arn("aws", testRegion, "1")] = wafTestDenied
	calls.tagErrs[wafRegionalTestKindOf(t, "ListRateBasedRules").arn("aws", testRegion, "2")] = wafTestDenied

	total, _, err := wafRegionalScan(context.Background(), st, wafRegionalHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != wafRegionalTestTotal {
		t.Errorf("total = %d; want %d", total, wafRegionalTestTotal)
	}
	if got := wafWarnOps(warns()); !slices.Equal(got, []string{wafRegionalTestTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, wafRegionalTestTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanWAFRegional_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, wafRegionalTestTagsOp)
	calls := newWAFRegionalCalls("aws", testRegion)
	calls.tagErrs[wafRegionalTestKindOf(t, "ListWebACLs").arn("aws", testRegion, "2")] = wafTestThrottle
	calls.tagErrs[wafRegionalTestKindOf(t, "ListRules").arn("aws", testRegion, "1")] = wafTestBad

	_, _, err := wafRegionalScan(context.Background(), st, wafRegionalHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "WAFInvalidParameterException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", wafRegionalTestTagsOp, *warns)
	}
}

// A failed write stops the scan: no later phase runs, the associations
// included, and the earlier phases' rows stay stored. A tagged listing's
// refused first page ends it before page 2, the web ACL listing's (which
// also collects the parents of the associations) included; an untagged one
// writes once, after its last page.
func TestScanWAFRegional_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, op  string
		listCalls int
		later     string
		wantTotal int
	}{
		{"web ACL listing", "ListWebACLs", 1, "ListRules", 0},
		{"tagged listing", "ListRules", 1, "ListRuleGroups", 2},
		{"untagged listing", "ListIPSets", 2, "ListByteMatchSets", 8},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			refuseStoreWrites(t, st, wafRegionalTestKindOf(t, c.op).typ)
			calls := newWAFRegionalCalls("aws", testRegion)

			total, _, err := wafRegionalScan(context.Background(), st, wafRegionalHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if n := calls.count(c.op); n != c.listCalls {
				t.Errorf("%s called %d times; want %d", c.op, n, c.listCalls)
			}
			for _, op := range []string{c.later, "ListXssMatchSets"} {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times; want 0 after the failed write", op, n)
				}
			}
			if got := calls.sortedAssocs(); len(got) != 0 {
				t.Errorf("ListResourcesForWebACL calls = %v; want none after the failed write", got)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d (the earlier phases' rows)", total, c.wantTotal)
			}
		})
	}
}

// A failed association write is returned as the write failure; the listed
// rows stay stored.
func TestScanWAFRegional_FailedAssociationWrite(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeWAFRegionalWebACLAssociation)
	_, _, err := wafRegionalScan(context.Background(), st, wafRegionalHandlers(newWAFRegionalCalls("aws", testRegion)))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if n := len(rowsByNativeID(t, st, TypeWAFRegionalWebACL)); n != 2 {
		t.Errorf("web ACLs stored = %d; want 2", n)
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the later phases; so for a failed first page. A web
// ACL listing that fails on page 2 still has the associations of its page-1
// web ACL read (base skipped every association). A repeated NextMarker ends
// the listing as an error, keeping its pages.
func TestScanWAFRegional_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, op  string
		failPage  string // "1", "2", or "repeat" for a repeated NextMarker
		wantTotal int
		wantRows  []string // the page numbers of op's rows stored
		wantErr   error
	}{
		{"web ACLs/second page", "ListWebACLs", "2", wafRegionalTestTotal - 3, []string{"1"}, nil},
		{"web ACLs/first page", "ListWebACLs", "1", wafRegionalTestTotal - 6, nil, nil},
		{"tagged/second page", "ListRules", "2", wafRegionalTestTotal - 1, []string{"1"}, nil},
		{"untagged/second page", "ListIPSets", "2", wafRegionalTestTotal - 1, []string{"1"}, nil},
		{"tagged/repeated marker", "ListRateBasedRules", "repeat", wafRegionalTestTotal, []string{"1", "2"}, errRepeatedNextToken},
		{"untagged/repeated marker", "ListGeoMatchSets", "repeat", wafRegionalTestTotal, []string{"1", "2"}, errRepeatedNextToken},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := newWAFRegionalCalls("aws", testRegion)
			handlers := wafRegionalHandlers(calls)
			k := wafRegionalTestKindOf(t, c.op)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				second := bacField[*string](in, "NextMarker") != nil
				switch {
				case c.failPage == "repeat":
					n := "1"
					if second {
						n = "2"
					}
					id, name := k.id(n), "name-"+k.id(n)
					calls.record(c.op, in)
					return k.page(&id, &name, sdkaws.String("same")), nil
				case (c.failPage == "2") == second:
					calls.record(c.op, in)
					return nil, wafTestBad
				}
				return list(in)
			}

			total, _, err := wafRegionalScan(context.Background(), st, handlers)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v; want %v", err, c.wantErr)
				}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "WAFInvalidParameterException")
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, k.typ)
			if len(rows) != len(c.wantRows) {
				t.Errorf("%s rows = %d; want %d", k.typ, len(rows), len(c.wantRows))
			}
			for _, n := range c.wantRows {
				if _, ok := rows[k.arn("aws", testRegion, n)]; !ok {
					t.Errorf("%s, read before the failure, missing", k.arn("aws", testRegion, n))
				}
			}
			if n := len(rowsByNativeID(t, st, TypeWAFRegionalXSSMatchSet)); n != 2 {
				t.Errorf("XSS match sets (the last listing) = %d; want 2", n)
			}
		})
	}
}

// A denied listing is warned about once under its op, with the scan's
// scope, and the other phases run; a denied web ACL listing leaves no web
// ACL to read associations for.
func TestScanWAFRegional_DeniedListingWarns(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op        string
		wantTotal int
	}{
		{"ListWebACLs", wafRegionalTestTotal - 6},
		{"ListIPSets", wafRegionalTestTotal - 2},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			handlers := wafRegionalHandlers(newWAFRegionalCalls("aws", testRegion))
			delete(handlers, c.op)

			total, _, err := wafRegionalScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			w := warns()
			if got := wafWarnOps(w); !slices.Equal(got, []string{"wafregional:" + c.op}) {
				t.Fatalf("warnings = %v; want one under wafregional:%s", got, c.op)
			}
			if w[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning Scope %q; want %s/%s", w[0].Scope, testAccountID, testRegion)
			}
		})
	}
}

// An element without an id is not stored and no tag read or association
// listing is sent for it; one warning under the listing's op names it by
// its name, or by noIdentifier when it has none (base skipped it silently).
func TestScanWAFRegional_ElementWithoutID(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, op  string
		elem      *string
		key       string
		tagReads  int
		assocRead int
	}{
		{"web ACL", "ListWebACLs", sdkaws.String("no-id"), "no-id", 7, 2},
		{"untagged/unnamed", "ListByteMatchSets", nil, noIdentifier, 8, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newWAFRegionalCalls("aws", testRegion)
			handlers := wafRegionalHandlers(calls)
			k := wafRegionalTestKindOf(t, c.op)
			list := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextMarker") == nil {
					return list(in)
				}
				calls.record(k.op, in)
				return k.page(nil, c.elem, nil), nil
			}

			if _, _, err := wafRegionalScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := rowsByNativeID(t, st, k.typ)
			if _, ok := rows[k.arn("aws", testRegion, "1")]; len(rows) != 1 || !ok {
				t.Errorf("%s rows = %v; want only %s", k.typ, rows, k.arn("aws", testRegion, "1"))
			}
			w := warns()
			if got := wafWarnOps(w); !slices.Equal(got, []string{"wafregional:" + k.op}) {
				t.Fatalf("warnings = %v; want one under wafregional:%s", got, k.op)
			}
			if w[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(w[0].Message, c.key) {
				t.Errorf("warning %+v; want scope %s/%s, naming %q", w[0], testAccountID, testRegion, c.key)
			}
			if got := len(calls.sortedTagARNs()); got != c.tagReads {
				t.Errorf("tag reads = %d; want %d", got, c.tagReads)
			}
			if got := len(calls.sortedAssocs()); got != c.assocRead {
				t.Errorf("ListResourcesForWebACL calls = %d; want %d", got, c.assocRead)
			}
		})
	}
	for _, ph := range wafRegionalPhases(nil) {
		if wafNoIDReasons[ph.kind] == "" {
			t.Errorf("%s has no reason for an element without an id", ph.op)
		}
	}
}

// One web ACL's failed association listing costs no other web ACL its rows
// (base dropped every association collected): a failure on the second
// resource type keeps the first type's row. A web ACL deleted since it was
// listed is skipped, silently when nothing was read for it and with a
// warning under the op when a resource was; a denial warns once under the
// op, with the scan's scope; any other error is returned after the rows are
// stored.
func TestScanWAFRegional_AssociationFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		failed    map[string]error // "<web ACL id> <resource type>"
		outcome   serviceOutcome   // -1: no error
		wantRows  []string         // association NativeIDs stored
		wantWarns []string
	}{
		{
			"other error on the second type",
			map[string]error{"webacl-1 API_GATEWAY": wafTestBad},
			outcomeError,
			[]string{wafRegionalAssocARN("webacl-1", "lbid-webacl-1"), wafRegionalAssocARN("webacl-2", "lbid-webacl-2"), wafRegionalAssocARN("webacl-2", "stage-webacl-2")},
			nil,
		},
		{
			"other error on the first type",
			map[string]error{"webacl-2 APPLICATION_LOAD_BALANCER": wafTestBad},
			outcomeError,
			[]string{wafRegionalAssocARN("webacl-1", "lbid-webacl-1"), wafRegionalAssocARN("webacl-1", "stage-webacl-1")},
			nil,
		},
		{
			"web ACL deleted",
			map[string]error{"webacl-1 APPLICATION_LOAD_BALANCER": wafTestGone},
			-1,
			[]string{wafRegionalAssocARN("webacl-2", "lbid-webacl-2"), wafRegionalAssocARN("webacl-2", "stage-webacl-2")},
			nil,
		},
		{
			"web ACL deleted after a resource was read",
			map[string]error{"webacl-1 API_GATEWAY": wafTestGone},
			-1,
			[]string{wafRegionalAssocARN("webacl-1", "lbid-webacl-1"), wafRegionalAssocARN("webacl-2", "lbid-webacl-2"), wafRegionalAssocARN("webacl-2", "stage-webacl-2")},
			[]string{wafRegionalTestAssocOp},
		},
		{
			"denied twice",
			map[string]error{"webacl-1 APPLICATION_LOAD_BALANCER": wafTestDenied, "webacl-2 APPLICATION_LOAD_BALANCER": wafTestDenied},
			-1,
			nil,
			[]string{wafRegionalTestAssocOp},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newWAFRegionalCalls("aws", testRegion)
			calls.assocErrs = c.failed

			total, _, err := wafRegionalScan(context.Background(), st, wafRegionalHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, "WAFInvalidParameterException")
			}
			if want := wafRegionalTestListed + len(c.wantRows); total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			got := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeWAFRegionalWebACLAssociation)))
			if want := slices.Sorted(slices.Values(c.wantRows)); !slices.Equal(got, want) {
				t.Errorf("associations = %v\nwant %v", got, want)
			}
			w := warns()
			if got := wafWarnOps(w); !slices.Equal(got, c.wantWarns) {
				t.Fatalf("warnings = %v; want %v", got, c.wantWarns)
			}
			for _, warn := range w {
				if warn.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", warn.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A throttled association read beside a hard failure of another phase is
// demoted to one warning under the association op, so the hard error
// decides the outcome.
func TestScanWAFRegional_AssociationThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, wafRegionalTestAssocOp)
	calls := newWAFRegionalCalls("aws", testRegion)
	calls.assocErrs["webacl-1 API_GATEWAY"] = wafTestThrottle
	calls.tagErrs[wafRegionalTestKindOf(t, "ListRules").arn("aws", testRegion, "1")] = wafTestBad

	_, _, err := wafRegionalScan(context.Background(), st, wafRegionalHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "WAFInvalidParameterException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", wafRegionalTestAssocOp, *warns)
	}
}

// A GovCloud or China account stores its rows under ARNs in its partition
// and region, and those ARNs reach the tag op unchanged.
func TestScanWAFRegional_PartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newWAFRegionalCalls(pf.partition, pf.region)
			client := wafregional.NewFromConfig(opStubConfig(wafRegionalHandlers(calls), wafTestDenied), func(o *wafregional.Options) { o.Region = pf.region })

			total, _, err := scanWAFRegionalAll(context.Background(), client, partitionAccount(pf.partition), pf.region, st, testScanID)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != wafRegionalTestTotal {
				t.Errorf("total = %d; want %d", total, wafRegionalTestTotal)
			}
			if got, want := calls.sortedTagARNs(), wafRegionalTestTaggedARNs(pf.partition, pf.region); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			k := wafRegionalTestKindOf(t, "ListWebACLs")
			r, ok := rowsByNativeID(t, st, k.typ)[k.arn(pf.partition, pf.region, "1")]
			if !ok {
				t.Fatalf("%s not stored", k.arn(pf.partition, pf.region, "1"))
			}
			assertTagsMap(t, r.TagsJSON, map[string]string{"kind": "webacl"})
			native := "arn:" + pf.partition + ":waf-regional:" + pf.region + ":" + testAccountID + ":webacl/webacl-1/association/lbid-webacl-1"
			if _, ok := rowsByNativeID(t, st, TypeWAFRegionalWebACLAssociation)[native]; !ok {
				t.Errorf("association %s not stored", native)
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, and no later page or
// phase is read, the associations included. The page in flight is stored,
// its row untagged.
func TestScanWAFRegional_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newWAFRegionalCalls("aws", testRegion)
	handlers := wafRegionalHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := wafRegionalTestKindOf(t, "ListRules")
	cut := k.arn("aws", testRegion, "1")
	handlers["ListTagsForResource"] = func(in any) (any, error) {
		if sv(in.(*wafregional.ListTagsForResourceInput).ResourceARN) == cut {
			calls.record("ListTagsForResource", in)
			cancel()
			return nil, context.Canceled
		}
		return calls.listTags(in)
	}

	_, _, err := wafRegionalScan(ctx, st, handlers)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := calls.count("ListRuleGroups"); n != 0 {
		t.Errorf("ListRuleGroups called %d times after ctx ended", n)
	}
	if got := calls.sortedAssocs(); len(got) != 0 {
		t.Errorf("ListResourcesForWebACL calls = %v after ctx ended", got)
	}
	r, ok := rowsByNativeID(t, st, k.typ)[cut]
	if !ok {
		t.Fatalf("%s, the page in flight, not stored", cut)
	}
	if r.TagsJSON != nil {
		t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan: the counts
// are kept. With nothing stored it stays region-unavailable and no later
// listing is called.
func TestScanWAFRegional_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "waf-regional.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := wafRegionalHandlers(newWAFRegionalCalls("aws", testRegion))
		handlers["ListXssMatchSets"] = func(any) (any, error) { return nil, nx }
		total, _, err := wafRegionalScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := wafRegionalTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newWAFRegionalCalls("aws", testRegion)
		handlers := map[string]func(in any) (any, error){}
		for _, k := range wafRegionalTestKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		_, _, err := wafRegionalScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		for _, k := range wafRegionalTestKinds()[1:] {
			if n := calls.count(k.op); n != 0 {
				t.Errorf("%s called %d times; want 0 once the first listing found no endpoint", k.op, n)
			}
		}
	})
}
