package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/waf"
	waftypes "github.com/aws/aws-sdk-go-v2/service/waf/types"
	"github.com/icearp/disco-cli/store"
)

// wafTestTotal is every row of the fixture: two (one per page) for each of
// the twelve listings.
const wafTestTotal = 12 * 2

// wafTestTagsOp is the tag op's label, written out so that a wrong wafTagsOp
// fails the tests.
const wafTestTagsOp = "waf:ListTagsForResource"

var (
	wafTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: waf:List")
	wafTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	wafTestBad      = apiErr("WAFInvalidParameterException", "bad")
	wafTestGone     = apiErr("WAFNonexistentItemException", "gone")
)

// wafTestKind is one WAF Classic listing: its op, the type it stores, the
// ARN kind of its rows, the element field holding the id, whether its rows
// take tags, and page, which builds the op's output holding one element and
// next.
type wafTestKind struct {
	op, typ, kind, idField string
	tagged                 bool
	page                   func(id, name, next *string) any
}

func wafTestKinds() []wafTestKind {
	return []wafTestKind{
		{"ListWebACLs", TypeWAFWebACL, "webacl", "WebACLId", true, func(id, name, next *string) any {
			return &waf.ListWebACLsOutput{NextMarker: next, WebACLs: []waftypes.WebACLSummary{{WebACLId: id, Name: name}}}
		}},
		{"ListRules", TypeWAFRule, "rule", "RuleId", true, func(id, name, next *string) any {
			return &waf.ListRulesOutput{NextMarker: next, Rules: []waftypes.RuleSummary{{RuleId: id, Name: name}}}
		}},
		{"ListRuleGroups", TypeWAFRuleGroup, "rulegroup", "RuleGroupId", true, func(id, name, next *string) any {
			return &waf.ListRuleGroupsOutput{NextMarker: next, RuleGroups: []waftypes.RuleGroupSummary{{RuleGroupId: id, Name: name}}}
		}},
		{"ListRateBasedRules", TypeWAFRateBasedRule, "ratebasedrule", "RuleId", true, func(id, name, next *string) any {
			return &waf.ListRateBasedRulesOutput{NextMarker: next, Rules: []waftypes.RuleSummary{{RuleId: id, Name: name}}}
		}},
		{"ListIPSets", TypeWAFIPSet, "ipset", "IPSetId", false, func(id, name, next *string) any {
			return &waf.ListIPSetsOutput{NextMarker: next, IPSets: []waftypes.IPSetSummary{{IPSetId: id, Name: name}}}
		}},
		{"ListByteMatchSets", TypeWAFByteMatchSet, "bytematchset", "ByteMatchSetId", false, func(id, name, next *string) any {
			return &waf.ListByteMatchSetsOutput{NextMarker: next, ByteMatchSets: []waftypes.ByteMatchSetSummary{{ByteMatchSetId: id, Name: name}}}
		}},
		{"ListGeoMatchSets", TypeWAFGeoMatchSet, "geomatchset", "GeoMatchSetId", false, func(id, name, next *string) any {
			return &waf.ListGeoMatchSetsOutput{NextMarker: next, GeoMatchSets: []waftypes.GeoMatchSetSummary{{GeoMatchSetId: id, Name: name}}}
		}},
		{"ListRegexMatchSets", TypeWAFRegexMatchSet, "regexmatchset", "RegexMatchSetId", false, func(id, name, next *string) any {
			return &waf.ListRegexMatchSetsOutput{NextMarker: next, RegexMatchSets: []waftypes.RegexMatchSetSummary{{RegexMatchSetId: id, Name: name}}}
		}},
		{"ListRegexPatternSets", TypeWAFRegexPatternSet, "regexpatternset", "RegexPatternSetId", false, func(id, name, next *string) any {
			return &waf.ListRegexPatternSetsOutput{NextMarker: next, RegexPatternSets: []waftypes.RegexPatternSetSummary{{RegexPatternSetId: id, Name: name}}}
		}},
		{"ListSizeConstraintSets", TypeWAFSizeConstraintSet, "sizeconstraintset", "SizeConstraintSetId", false, func(id, name, next *string) any {
			return &waf.ListSizeConstraintSetsOutput{NextMarker: next, SizeConstraintSets: []waftypes.SizeConstraintSetSummary{{SizeConstraintSetId: id, Name: name}}}
		}},
		{"ListSqlInjectionMatchSets", TypeWAFSQLInjectionMatchSet, "sqlinjectionmatchset", "SqlInjectionMatchSetId", false, func(id, name, next *string) any {
			return &waf.ListSqlInjectionMatchSetsOutput{NextMarker: next, SqlInjectionMatchSets: []waftypes.SqlInjectionMatchSetSummary{{SqlInjectionMatchSetId: id, Name: name}}}
		}},
		{"ListXssMatchSets", TypeWAFXSSMatchSet, "xssmatchset", "XssMatchSetId", false, func(id, name, next *string) any {
			return &waf.ListXssMatchSetsOutput{NextMarker: next, XssMatchSets: []waftypes.XssMatchSetSummary{{XssMatchSetId: id, Name: name}}}
		}},
	}
}

func wafTestKindOf(t *testing.T, op string) wafTestKind {
	t.Helper()
	for _, k := range wafTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no WAF Classic listing", op)
	return wafTestKind{}
}

// id is the id of the row on page n (1 or 2) of k's listing.
func (k wafTestKind) id(n string) string { return k.kind + "-" + n }

// arn is the ARN of the row on page n of k's listing in partition.
func (k wafTestKind) arn(partition, n string) string {
	return "arn:" + partition + ":waf::" + testAccountID + ":" + k.kind + "/" + k.id(n)
}

// wafTestTaggedARNs is the ARN of every row whose tags are read, sorted.
func wafTestTaggedARNs(partition string) []string {
	var arns []string
	for _, k := range wafTestKinds() {
		if k.tagged {
			arns = append(arns, k.arn(partition, "1"), k.arn(partition, "2"))
		}
	}
	slices.Sort(arns)
	return arns
}

// wafCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type wafCalls struct {
	mu      sync.Mutex
	limits  map[string][]int32
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// newWAFCalls tags the page-1 row of every tagged listing with its kind.
func newWAFCalls(partition string) *wafCalls {
	c := &wafCalls{limits: map[string][]int32{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range wafTestKinds() {
		if k.tagged {
			c.tags[k.arn(partition, "1")] = map[string]string{"kind": k.kind}
		}
	}
	return c
}

func (c *wafCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limits[op] = append(c.limits[op], bacField[int32](in, "Limit"))
}

func (c *wafCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.limits[op])
}

func (c *wafCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *wafCalls) listTags(in any) (any, error) {
	arn := sv(in.(*waf.ListTagsForResourceInput).ResourceARN)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	info := &waftypes.TagInfoForResource{ResourceARN: &arn, TagList: []waftypes.Tag{}}
	for k, v := range c.tags[arn] {
		info.TagList = append(info.TagList, waftypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &waf.ListTagsForResourceOutput{TagInfoForResource: info}, nil
}

// twoPages answers k's op with row 1 on the first page and row 2 on the
// second.
func (c *wafCalls) twoPages(k wafTestKind) func(any) (any, error) {
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

// wafHandlers answers every op the scan calls: each listing in two pages and
// the tag reads from c.
func wafHandlers(c *wafCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range wafTestKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// wafScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func wafScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, wafTestDenied)
	return scanWAF(ctx, acct, "", st, testScanID)
}

// wafWarnOps is the op of each warning, in order.
func wafWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, each row under the ARN built from its
// id, named by its name, with region "global", no Status or CreatedAt, and
// its attributes the listed element. Only the web ACLs, rules, rule groups
// and rate-based rules have their tags read (nil for none):
// ListTagsForResource is sent exactly their ARNs. Every call is sent the
// model's Limit cap of 100. No error, no warning.
func TestScanWAF_StoresEveryListingWithTags(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newWAFCalls("aws")

	total, inserted, err := wafScan(context.Background(), st, wafHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != wafTestTotal || inserted != wafTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, wafTestTotal, wafTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if _, ok := rowsByNativeID(t, st, TypeWAFWebACL)["arn:aws:waf::123456789012:webacl/webacl-1"]; !ok {
		t.Errorf("web ACL not stored under its Service Reference ARN")
	}
	for _, k := range wafTestKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", k.typ, len(rows))
		}
		for _, n := range []string{"1", "2"} {
			arn := k.arn("aws", n)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != "name-"+k.id(n) || sv(r.Region) != "global" || r.Status != nil || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %v, %v; want %q, global, nil, nil", arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, "name-"+k.id(n))
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
	if got, want := calls.sortedTagARNs(), wafTestTaggedARNs("aws"); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
	for op, limits := range calls.limits {
		for _, n := range limits {
			if n != 100 {
				t.Errorf("%s Limit = %d; want 100", op, n)
			}
		}
	}
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanWAF_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := wafScan(context.Background(), st, wafHandlers(newWAFCalls("aws"))); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := wafScan(context.Background(), st, wafHandlers(newWAFCalls("aws")))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != wafTestTotal {
		t.Errorf("rescan total = %d; want %d", total, wafTestTotal)
	}
}

// Empty listings: nothing stored, no tag read, no error, no warning.
func TestScanWAF_NoResources(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newWAFCalls("aws")
	handlers := wafHandlers(calls)
	for _, k := range wafTestKinds() {
		handlers[k.op] = func(in any) (any, error) {
			calls.record(k.op, in)
			// The op's zero output: no element, no NextMarker.
			return reflect.New(reflect.TypeOf(k.page(nil, nil, nil)).Elem()).Interface(), nil
		}
	}

	total, inserted, err := wafScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource called %d times; want 0", n)
	}
	for _, k := range wafTestKinds() {
		if n := calls.count(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
	}
}

// A failed tag read. WAFNonexistentItemException is modeled, so a resource
// deleted since it was listed is dropped silently. A denial keeps the row
// untagged and warns once under the tag op with the scan's scope (the home
// region); a throttle keeps it and is transient; any other error keeps it
// and is an error.
func TestScanWAF_TagReadFailures(t *testing.T) {
	t.Parallel()
	arn := wafTestKindOf(t, "ListRateBasedRules").arn("aws", "1")
	for _, c := range []struct {
		name      string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"not found", wafTestGone, -1, "", wafTestTotal - 1, false, false},
		{"denied", wafTestDenied, -1, "", wafTestTotal, true, true},
		{"transient", wafTestThrottle, outcomeTransient, "ThrottlingException", wafTestTotal, true, false},
		{"other error", wafTestBad, outcomeError, "WAFInvalidParameterException", wafTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newWAFCalls("aws")
			calls.errs[arn] = c.err

			total, _, err := wafScan(context.Background(), st, wafHandlers(calls))
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
			r, ok := rowsByNativeID(t, st, TypeWAFRateBasedRule)[arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{wafTestTagsOp}
			}
			if got := wafWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/us-east-1" {
					t.Errorf("warning Scope %q; want %s/us-east-1", x.Scope, testAccountID)
				}
			}
		})
	}
}

// ListTagsForResource pages are all read, each sent the Limit cap; a
// repeated NextMarker fails the read, keeping the row untagged.
func TestScanWAF_TagPages(t *testing.T) {
	t.Parallel()
	arn := wafTestKindOf(t, "ListRuleGroups").arn("aws", "2")
	pageOf := func(next *string, k, v string) *waf.ListTagsForResourceOutput {
		return &waf.ListTagsForResourceOutput{NextMarker: next, TagInfoForResource: &waftypes.TagInfoForResource{
			ResourceARN: &arn, TagList: []waftypes.Tag{{Key: sdkaws.String(k), Value: sdkaws.String(v)}},
		}}
	}
	for _, c := range []struct {
		name     string
		second   *string
		wantTags map[string]string
	}{
		{"every page read", nil, map[string]string{"a": "1", "b": "2"}},
		{"repeated marker", sdkaws.String("t2"), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := newWAFCalls("aws")
			handlers := wafHandlers(calls)
			var limits []int32
			handlers["ListTagsForResource"] = func(in any) (any, error) {
				req := in.(*waf.ListTagsForResourceInput)
				if sv(req.ResourceARN) != arn {
					return calls.listTags(in)
				}
				calls.mu.Lock()
				limits = append(limits, req.Limit)
				calls.mu.Unlock()
				if req.NextMarker == nil {
					return pageOf(sdkaws.String("t2"), "a", "1"), nil
				}
				return pageOf(c.second, "b", "2"), nil
			}

			total, _, err := wafScan(context.Background(), st, handlers)
			if total != wafTestTotal {
				t.Errorf("total = %d; want %d", total, wafTestTotal)
			}
			if !slices.Equal(limits, []int32{100, 100}) {
				t.Errorf("Limits = %v; want [100 100]", limits)
			}
			r := rowsByNativeID(t, st, TypeWAFRuleGroup)[arn]
			if c.wantTags == nil {
				if !errors.Is(err, errRepeatedNextToken) {
					t.Fatalf("err = %v; want errRepeatedNextToken", err)
				}
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				return
			}
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			assertTagsMap(t, r.TagsJSON, c.wantTags)
		})
	}
}

// A tag response without TagInfoForResource reads as no tags.
func TestScanWAF_TagResponseWithoutInfo(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newWAFCalls("aws")
	handlers := wafHandlers(calls)
	arn := wafTestKindOf(t, "ListWebACLs").arn("aws", "1")
	handlers["ListTagsForResource"] = func(in any) (any, error) {
		if sv(in.(*waf.ListTagsForResourceInput).ResourceARN) == arn {
			return &waf.ListTagsForResourceOutput{}, nil
		}
		return calls.listTags(in)
	}

	if _, _, err := wafScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeWAFWebACL)[arn]
	if !ok {
		t.Fatalf("%s not stored", arn)
	}
	if r.TagsJSON != nil {
		t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
	}
}

// Denied tag reads in two phases send one warning for the whole scan; the
// rows are kept.
func TestScanWAF_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newWAFCalls("aws")
	calls.errs[wafTestKindOf(t, "ListWebACLs").arn("aws", "1")] = wafTestDenied
	calls.errs[wafTestKindOf(t, "ListRateBasedRules").arn("aws", "2")] = wafTestDenied

	total, _, err := wafScan(context.Background(), st, wafHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != wafTestTotal {
		t.Errorf("total = %d; want %d", total, wafTestTotal)
	}
	if got := wafWarnOps(warns()); !slices.Equal(got, []string{wafTestTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, wafTestTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanWAF_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, wafTestTagsOp)
	calls := newWAFCalls("aws")
	calls.errs[wafTestKindOf(t, "ListWebACLs").arn("aws", "2")] = wafTestThrottle
	calls.errs[wafTestKindOf(t, "ListRules").arn("aws", "1")] = wafTestBad

	_, _, err := wafScan(context.Background(), st, wafHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "WAFInvalidParameterException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", wafTestTagsOp, *warns)
	}
}

// A failed write stops the scan: no later phase runs, and the earlier
// phases' rows stay stored. A listing whose tags are read writes each page
// before the next is read, so its refused first page ends it before page 2;
// one whose tags are not read writes once, after its last page. A rule's tag
// read is denied: when the refused write lost that rule there is no warning
// about it; when the rule was stored before, the denial is warned about.
func TestScanWAF_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, op  string
		listCalls int      // calls of op's listing
		later     string   // the next listing, never called
		wantTotal int      // the rows of the phases before op
		wantWarns []string // the ops warned under
	}{
		{"tagged listing", "ListRules", 1, "ListRuleGroups", 2, nil},
		{"untagged listing", "ListIPSets", 2, "ListByteMatchSets", 8, []string{wafTestTagsOp}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			k := wafTestKindOf(t, c.op)
			refuseStoreWrites(t, st, k.typ)
			calls := newWAFCalls("aws")
			calls.errs[wafTestKindOf(t, "ListRules").arn("aws", "1")] = wafTestDenied

			total, _, err := wafScan(context.Background(), st, wafHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if got := wafWarnOps(warns()); !slices.Equal(got, c.wantWarns) {
				t.Errorf("warnings = %v; want %v", got, c.wantWarns)
			}
			if n := calls.count(c.op); n != c.listCalls {
				t.Errorf("%s called %d times; want %d", c.op, n, c.listCalls)
			}
			for _, op := range []string{c.later, "ListXssMatchSets"} {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times; want 0 after the failed write", op, n)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d (the earlier phases' rows)", total, c.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, TypeWAFWebACL)); n != 2 {
				t.Errorf("web ACLs stored = %d; want 2", n)
			}
		})
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the later phases; so for a failed first page. A
// repeated NextMarker ends the listing as an error, keeping its pages. Both
// hold for a listing whose tags are read and for one whose are not.
func TestScanWAF_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, op  string
		failPage  string // "1", "2", or "repeat" for a repeated NextMarker
		wantTotal int
		wantRows  []string // the page numbers of op's rows stored
		wantErr   error
	}{
		{"tagged/second page", "ListRules", "2", wafTestTotal - 1, []string{"1"}, nil},
		{"tagged/first page", "ListWebACLs", "1", wafTestTotal - 2, nil, nil},
		{"untagged/second page", "ListIPSets", "2", wafTestTotal - 1, []string{"1"}, nil},
		{"tagged/repeated marker", "ListRateBasedRules", "repeat", wafTestTotal, []string{"1", "2"}, errRepeatedNextToken},
		{"untagged/repeated marker", "ListGeoMatchSets", "repeat", wafTestTotal, []string{"1", "2"}, errRepeatedNextToken},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := newWAFCalls("aws")
			handlers := wafHandlers(calls)
			k := wafTestKindOf(t, c.op)
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

			total, _, err := wafScan(context.Background(), st, handlers)
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
				if _, ok := rows[k.arn("aws", n)]; !ok {
					t.Errorf("%s, read before the failure, missing", k.arn("aws", n))
				}
			}
			if n := len(rowsByNativeID(t, st, TypeWAFXSSMatchSet)); n != 2 {
				t.Errorf("XSS match sets (the last phase) = %d; want 2", n)
			}
		})
	}
}

// A denied listing is warned about once under its op, with the scan's scope
// (the home region), and the other phases run.
func TestScanWAF_DeniedListingWarns(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"ListRuleGroups", "ListIPSets"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			handlers := wafHandlers(newWAFCalls("aws"))
			delete(handlers, op)

			total, _, err := wafScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != wafTestTotal-2 {
				t.Errorf("total = %d; want %d", total, wafTestTotal-2)
			}
			w := warns()
			if got := wafWarnOps(w); !slices.Equal(got, []string{"waf:" + op}) {
				t.Fatalf("warnings = %v; want one under waf:%s", got, op)
			}
			if w[0].Scope != testAccountID+"/us-east-1" {
				t.Errorf("warning Scope %q; want %s/us-east-1", w[0].Scope, testAccountID)
			}
		})
	}
}

// An element without an id is not stored, and no tag read is sent for it;
// one warning under the listing's op, with the scan's scope, names it by its
// name, or by noIdentifier when it has none. The listing's other row is
// stored. Every listing has a reason for the warning.
func TestScanWAF_ElementWithoutID(t *testing.T) {
	t.Parallel()
	type tc struct {
		name string
		k    wafTestKind
		elem *string // the keyless element's name
		key  string
	}
	var cases []tc
	for _, k := range wafTestKinds() {
		cases = append(cases, tc{k.op, k, sdkaws.String("no-id"), "no-id"})
	}
	for _, op := range []string{"ListRules", "ListByteMatchSets"} {
		cases = append(cases, tc{op + "/unnamed", wafTestKindOf(t, op), nil, noIdentifier})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newWAFCalls("aws")
			handlers := wafHandlers(calls)
			k := c.k
			list := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextMarker") == nil {
					return list(in)
				}
				calls.record(k.op, in)
				return k.page(nil, c.elem, nil), nil
			}

			total, _, err := wafScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != wafTestTotal-1 {
				t.Errorf("total = %d; want %d", total, wafTestTotal-1)
			}
			rows := rowsByNativeID(t, st, k.typ)
			if _, ok := rows[k.arn("aws", "1")]; len(rows) != 1 || !ok {
				t.Errorf("%s rows = %v; want only %s", k.typ, rows, k.arn("aws", "1"))
			}
			w := warns()
			if got := wafWarnOps(w); !slices.Equal(got, []string{"waf:" + k.op}) {
				t.Fatalf("warnings = %v; want one under waf:%s", got, k.op)
			}
			if w[0].Scope != testAccountID+"/us-east-1" || !strings.Contains(w[0].Message, c.key) {
				t.Errorf("warning %+v; want scope %s/us-east-1, naming %q", w[0], testAccountID, c.key)
			}
			wantReads := len(wafTestTaggedARNs("aws"))
			if k.tagged {
				wantReads--
			}
			if got := len(calls.sortedTagARNs()); got != wantReads {
				t.Errorf("tag reads = %d; want %d", got, wantReads)
			}
		})
	}
	for _, ph := range wafPhases(nil) {
		if wafNoIDReasons[ph.kind] == "" {
			t.Errorf("%s has no reason for an element without an id", ph.op)
		}
	}
}

// An element whose name is empty, or missing, is stored with no Name.
func TestScanWAF_ElementWithoutNameStoredUnnamed(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, op string
		elem     *string
	}{
		{"tagged/empty", "ListRules", sdkaws.String("")},
		{"untagged/empty", "ListByteMatchSets", sdkaws.String("")},
		{"untagged/missing", "ListGeoMatchSets", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newWAFCalls("aws")
			handlers := wafHandlers(calls)
			k := wafTestKindOf(t, c.op)
			list := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextMarker") == nil {
					return list(in)
				}
				calls.record(k.op, in)
				id := k.id("2")
				return k.page(&id, c.elem, nil), nil
			}

			total, _, err := wafScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != wafTestTotal {
				t.Errorf("total = %d; want %d", total, wafTestTotal)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			arn := k.arn("aws", "2")
			r, ok := rowsByNativeID(t, st, k.typ)[arn]
			if !ok {
				t.Fatalf("%s not stored", arn)
			}
			if r.Name != nil {
				t.Errorf("%s Name = %q; want nil", arn, *r.Name)
			}
		})
	}
}

// A GovCloud or China account stores its rows under ARNs in its partition,
// and those ARNs reach the tag op unchanged. The scan body is run with a
// client for the partition's region, whether or not the pinned SDK gives
// WAF Classic a home there (homeRegion).
func TestScanWAF_PartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newWAFCalls(pf.partition)
			client := waf.NewFromConfig(opStubConfig(wafHandlers(calls), wafTestDenied), func(o *waf.Options) { o.Region = pf.region })

			total, _, err := scanWAFClassic(context.Background(), client, partitionAccount(pf.partition), pf.region, st, testScanID)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != wafTestTotal {
				t.Errorf("total = %d; want %d", total, wafTestTotal)
			}
			if got, want := calls.sortedTagARNs(), wafTestTaggedARNs(pf.partition); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			k := wafTestKindOf(t, "ListWebACLs")
			r, ok := rowsByNativeID(t, st, k.typ)[k.arn(pf.partition, "1")]
			if !ok {
				t.Fatalf("%s not stored", k.arn(pf.partition, "1"))
			}
			assertTagsMap(t, r.TagsJSON, map[string]string{"kind": "webacl"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it both in a tag read and in its listing, and no later page or
// phase is read. The page in flight is stored, its row untagged.
func TestScanWAF_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newWAFCalls("aws")
	handlers := wafHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := wafTestKindOf(t, "ListRules")
	cut := k.arn("aws", "1")
	handlers["ListTagsForResource"] = func(in any) (any, error) {
		if sv(in.(*waf.ListTagsForResourceInput).ResourceARN) == cut {
			calls.record("ListTagsForResource", in)
			cancel()
			return nil, context.Canceled
		}
		return calls.listTags(in)
	}

	_, _, err := wafScan(ctx, st, handlers)
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
	if n := calls.count("ListRules"); n != 1 {
		t.Errorf("ListRules called %d times; want 1, its second page never read", n)
	}
	if n := calls.count("ListRuleGroups"); n != 0 {
		t.Errorf("ListRuleGroups called %d times after ctx ended", n)
	}
	r, ok := rowsByNativeID(t, st, k.typ)[cut]
	if !ok {
		t.Fatalf("%s, the page in flight, not stored", cut)
	}
	if r.TagsJSON != nil {
		t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later listing is called.
func TestScanWAF_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "waf.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := wafHandlers(newWAFCalls("aws"))
		handlers["ListXssMatchSets"] = func(any) (any, error) { return nil, nx }
		total, _, err := wafScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := wafTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newWAFCalls("aws")
		calls.errs[wafTestKindOf(t, "ListWebACLs").arn("aws", "2")] = nx
		_, _, err := wafScan(context.Background(), st, wafHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newWAFCalls("aws")
		handlers := map[string]func(in any) (any, error){}
		for _, k := range wafTestKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		_, _, err := wafScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		for _, k := range wafTestKinds()[1:] {
			if n := calls.count(k.op); n != 0 {
				t.Errorf("%s called %d times; want 0 once the first listing found no endpoint", k.op, n)
			}
		}
	})
}
