package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/connectcases"
	cctypes "github.com/aws/aws-sdk-go-v2/service/connectcases/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// casesTestTotal is every row of the fixture: two domains (one per
	// page); in each, two case rules, fields, layouts and templates (one per
	// page); and two options (one per page) under each of the four fields,
	// all custom single-select.
	casesTestTotal = 2 + 2*4*2 + 4*2
	// casesTestDomainRows is what one domain holds: itself, its eight
	// per-domain children and its two fields' four options.
	casesTestDomainRows = 1 + 4*2 + 2*2
)

var (
	casesTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: cases:List")
	casesTestThrottle = apiErr("ThrottlingException", "slow down")
	casesTestBad      = apiErr("ValidationException", "bad")
	casesTestGone     = apiErr("ResourceNotFoundException", "gone")
	casesTestDomains  = []string{"domain-bare", "domain-tagged"}
)

func casesTestDomainARN(partition, region, id string) string {
	return "arn:" + partition + ":cases:" + region + ":" + testAccountID + ":domain/" + id
}

// casesKind is one listing whose rows have their tags read: the List op, the
// type it stores, the ARN segment of its rows ("" for a domain), the short
// name its rows are named by, the element field holding the ARN, the Status
// its rows carry ("" for none), and page, which builds the op's output
// holding one element and next.
type casesKind struct {
	op, typ, seg, short, arnField, status string
	page                                  func(arn, name, next *string) any
}

func casesKinds() []casesKind {
	return []casesKind{
		{"ListDomains", TypeCasesDomain, "", "domain", "DomainArn", "", func(arn, name, next *string) any {
			return &connectcases.ListDomainsOutput{NextToken: next, Domains: []cctypes.DomainSummary{{DomainArn: arn, DomainId: name, Name: name}}}
		}},
		{"ListCaseRules", TypeCasesCaseRule, "case-rule", "rule", "CaseRuleArn", "", func(arn, name, next *string) any {
			return &connectcases.ListCaseRulesOutput{NextToken: next, CaseRules: []cctypes.CaseRuleSummary{{CaseRuleArn: arn, CaseRuleId: name, Name: name, RuleType: cctypes.RuleTypeRequired}}}
		}},
		{"ListFields", TypeCasesField, "field", "field", "FieldArn", "", func(arn, name, next *string) any {
			return &connectcases.ListFieldsOutput{NextToken: next, Fields: []cctypes.FieldSummary{{
				FieldArn: arn, FieldId: name, Name: name, Namespace: cctypes.FieldNamespaceCustom, Type: cctypes.FieldTypeSingleSelect,
			}}}
		}},
		{"ListLayouts", TypeCasesLayout, "layout", "layout", "LayoutArn", "", func(arn, name, next *string) any {
			return &connectcases.ListLayoutsOutput{NextToken: next, Layouts: []cctypes.LayoutSummary{{LayoutArn: arn, LayoutId: name, Name: name}}}
		}},
		{"ListTemplates", TypeCasesTemplate, "template", "template", "TemplateArn", "Active", func(arn, name, next *string) any {
			return &connectcases.ListTemplatesOutput{NextToken: next, Templates: []cctypes.TemplateSummary{{TemplateArn: arn, TemplateId: name, Name: name, Status: cctypes.TemplateStatusActive}}}
		}},
	}
}

// casesKindOf is op's casesKind; it panics on an op that is none, a typo in
// a test.
func casesKindOf(op string) casesKind {
	for _, k := range casesKinds() {
		if k.op == op {
			return k
		}
	}
	panic(op + " is no Connect Cases listing")
}

// arn is the ARN of k's row on page suffix under the domain domainID.
func (k casesKind) arn(domainID, suffix string) string {
	name := k.short + "-" + suffix
	if k.seg == "" {
		return casesTestDomainARN("aws", testRegion, name)
	}
	return casesTestDomainARN("aws", testRegion, domainID) + "/" + k.seg + "/" + name
}

// arns is the ARN of every row k's listing returns in the fixture.
func (k casesKind) arns() []string {
	if k.seg == "" {
		return []string{k.arn("", "tagged"), k.arn("", "bare")}
	}
	var arns []string
	for _, d := range casesTestDomains {
		arns = append(arns, k.arn(d, "tagged"), k.arn(d, "bare"))
	}
	return arns
}

// casesTestOptionARNs is the NativeID of every option of the fixture.
func casesTestOptionARNs() []string {
	var ids []string
	for _, f := range casesKindOf("ListFields").arns() {
		ids = append(ids, f+"/option/high", f+"/option/tier/1")
	}
	slices.Sort(ids)
	return ids
}

// casesTaggedARNs is the ARN of every row with tags read, sorted.
func casesTaggedARNs() []string {
	var arns []string
	for _, k := range casesKinds() {
		arns = append(arns, k.arns()...)
	}
	slices.Sort(arns)
	return arns
}

// casesCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type casesCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	parents    map[string][]string
	tagARNs    []string
	tags       map[string]map[string]*string
	errs       map[string]error
}

// newCasesCalls tags every "-tagged" row with its type and a key whose value
// is null.
func newCasesCalls() *casesCalls {
	c := &casesCalls{maxResults: map[string][]int32{}, parents: map[string][]string{}, tags: map[string]map[string]*string{}, errs: map[string]error{}}
	for _, k := range casesKinds() {
		for _, arn := range k.arns() {
			if strings.HasSuffix(arn, "-tagged") {
				c.tags[arn] = map[string]*string{"type": sdkaws.String(k.typ), "empty": nil}
			}
		}
	}
	return c
}

// casesWantTags is the tags newCasesCalls gives a row of typ.
func casesWantTags(typ string) map[string]string { return map[string]string{"type": typ, "empty": ""} }

// record notes op's MaxResults and, on a first page, the parent it lists:
// the domain, or domain/field for options.
func (c *casesCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxResults[op] = append(c.maxResults[op], sdkaws.ToInt32(bacField[*int32](in, "MaxResults")))
	if bacField[*string](in, "NextToken") != nil {
		return
	}
	parent := sv(bacField[*string](in, "DomainId"))
	if f := bacField[*string](in, "FieldId"); f != nil {
		parent += "/" + *f
	}
	c.parents[op] = append(c.parents[op], parent)
}

func (c *casesCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op])
}

func (c *casesCalls) listedParents(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.parents[op]))
}

func (c *casesCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *casesCalls) listTags(in any) (any, error) {
	arn := sv(in.(*connectcases.ListTagsForResourceInput).Arn)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &connectcases.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second, under the domain its input names.
func (c *casesCalls) twoPages(k casesKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		arn, name := k.arn(sv(bacField[*string](in, "DomainId")), suffix), k.short+"-"+suffix
		return k.page(&arn, &name, next), nil
	}
}

// options answers ListFieldOptions with "high" on the first page and "tier/1"
// on the second.
func (c *casesCalls) options(in any) (any, error) {
	c.record("ListFieldOptions", in)
	if bacField[*string](in, "NextToken") == nil {
		return &connectcases.ListFieldOptionsOutput{NextToken: sdkaws.String("p2"), Options: []cctypes.FieldOption{
			{Value: sdkaws.String("high"), Name: sdkaws.String("High"), Active: sdkaws.Bool(true)},
		}}, nil
	}
	return &connectcases.ListFieldOptionsOutput{Options: []cctypes.FieldOption{
		{Value: sdkaws.String("tier/1"), Name: sdkaws.String("Tier 1"), Active: sdkaws.Bool(false)},
	}}, nil
}

// casesHandlers answers every op the scan calls: each listing in two pages and
// the tag reads from c.
func casesHandlers(c *casesCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags, "ListFieldOptions": c.options}
	for _, k := range casesKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// casesScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func casesScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, casesTestDenied)
	return scanCases(ctx, acct, testRegion, st, testScanID)
}

func casesWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, for every domain, each row under its
// ARN, named by its name, its attributes the listed element, a template with
// its Status and no other row with one, and with the tags its tag read
// returns (nil for none; a null value stored as ""). ListTagsForResource is
// sent exactly the ARNs of every row but the options. ListDomains is sent its
// maxResults cap of 10, every other listing its cap of 100. Every per-domain
// listing is called for both domains and the options for each field. No
// error, no warning.
func TestScanCases_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCasesCalls()

	total, inserted, err := casesScan(context.Background(), st, casesHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != casesTestTotal || inserted != casesTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, casesTestTotal, casesTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range casesKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != len(k.arns()) {
			t.Errorf("%s rows = %d; want %d", k.typ, len(rows), len(k.arns()))
		}
		for _, arn := range k.arns() {
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			wantName := arn[strings.LastIndex(arn, "/")+1:]
			if sv(r.Name) != wantName || sv(r.Region) != testRegion || sv(r.Status) != k.status || (k.status == "") != (r.Status == nil) || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %v, %v; want %q, %q, %q, nil", arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, wantName, testRegion, k.status)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[k.arnField] != arn || attrs["Name"] != wantName {
				t.Errorf("%s attrs = %s (%v); want the listed element", arn, r.AttributesJSON, err)
			}
			if strings.HasSuffix(arn, "-tagged") {
				assertTagsMap(t, r.TagsJSON, casesWantTags(k.typ))
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, casesTaggedARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, casesTaggedARNs())
	}

	options := rowsByNativeID(t, st, TypeCasesFieldOption)
	if got := nativeIDs(options); !slices.Equal(got, casesTestOptionARNs()) {
		t.Errorf("options = %v\nwant %v", got, casesTestOptionARNs())
	}
	field := casesKindOf("ListFields").arn("domain-tagged", "bare")
	opt := options[field+"/option/tier/1"]
	var attrs struct {
		Value  string
		Active *bool
	}
	if err := json.Unmarshal([]byte(opt.AttributesJSON), &attrs); err != nil || attrs.Value != "tier/1" || attrs.Active == nil || *attrs.Active {
		t.Errorf("option attrs = %s (%v); want the listed option (Value tier/1, Active false)", opt.AttributesJSON, err)
	}
	if sv(opt.Name) != "Tier 1" || sv(opt.Region) != testRegion || opt.Status != nil || opt.TagsJSON != nil {
		t.Errorf("option Name, Region, Status, TagsJSON = %q, %q, %v, %v; want Tier 1, %s, nil, nil", sv(opt.Name), sv(opt.Region), opt.Status, opt.TagsJSON, testRegion)
	}
	for _, id := range casesTestOptionARNs() {
		fieldARN, _, _ := strings.Cut(id, "/option/")
		assertContainsNative(t, st, fieldARN, id)
	}

	for _, op := range []string{"ListCaseRules", "ListFields", "ListLayouts", "ListTemplates"} {
		if got := calls.listedParents(op); !slices.Equal(got, casesTestDomains) {
			t.Errorf("%s domains = %v; want %v", op, got, casesTestDomains)
		}
	}
	wantFields := []string{"domain-bare/field-bare", "domain-bare/field-tagged", "domain-tagged/field-bare", "domain-tagged/field-tagged"}
	if got := calls.listedParents("ListFieldOptions"); !slices.Equal(got, wantFields) {
		t.Errorf("ListFieldOptions parents = %v; want %v", got, wantFields)
	}
	for op, want := range map[string]int32{
		"ListDomains": 10, "ListCaseRules": 100, "ListFields": 100, "ListLayouts": 100, "ListTemplates": 100, "ListFieldOptions": 100,
	} {
		if calls.count(op) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, n := range calls.maxResults[op] {
			if n != want {
				t.Errorf("%s MaxResults = %d; want %d", op, n, want)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanCases_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := casesScan(context.Background(), st, casesHandlers(newCasesCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := casesScan(context.Background(), st, casesHandlers(newCasesCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != casesTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, casesTestTotal)
	}
}

// No domain: nothing stored, no child listing or tag read, no error, no
// warning.
func TestScanCases_NoDomains(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCasesCalls()
	handlers := casesHandlers(calls)
	handlers["ListDomains"] = func(in any) (any, error) {
		calls.record("ListDomains", in)
		return &connectcases.ListDomainsOutput{}, nil
	}

	total, inserted, err := casesScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListCaseRules", "ListFields", "ListLayouts", "ListTemplates", "ListFieldOptions"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if got := calls.sortedTagARNs(); len(got) != 0 {
		t.Errorf("ListTagsForResource ARNs = %v; want none", got)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently (a domain with its
// children, a field with its options, none of which are listed). A denial
// keeps the row untagged and warns once under the tag op with the scan's
// scope; a throttle keeps it and is transient; any other error keeps it and
// is an error.
func TestScanCases_TagReadFailures(t *testing.T) {
	layout := casesKindOf("ListLayouts").arns()[0]
	domain := casesKindOf("ListDomains").arns()[1]
	field := casesKindOf("ListFields").arns()[2]
	for _, c := range []struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"layout/not found", layout, TypeCasesLayout, casesTestGone, -1, "", casesTestTotal - 1, false, false},
		{"layout/denied", layout, TypeCasesLayout, casesTestDenied, -1, "", casesTestTotal, true, true},
		{"layout/transient", layout, TypeCasesLayout, casesTestThrottle, outcomeTransient, "ThrottlingException", casesTestTotal, true, false},
		{"layout/other error", layout, TypeCasesLayout, casesTestBad, outcomeError, "ValidationException", casesTestTotal, true, false},
		{"domain/not found", domain, TypeCasesDomain, casesTestGone, -1, "", casesTestTotal - casesTestDomainRows, false, false},
		{"domain/denied", domain, TypeCasesDomain, casesTestDenied, -1, "", casesTestTotal, true, true},
		{"field/not found", field, TypeCasesField, casesTestGone, -1, "", casesTestTotal - 1 - 2, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newCasesCalls()
			calls.errs[c.arn] = c.err

			total, _, err := casesScan(context.Background(), st, casesHandlers(calls))
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
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{casesTagsOp}
			}
			if got := casesWarnOps(w); !slices.Equal(got, want) {
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

// Denied tag reads in two phases send one warning for the whole scan; the
// rows are kept.
func TestScanCases_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCasesCalls()
	for _, op := range []string{"ListDomains", "ListTemplates"} {
		for _, arn := range casesKindOf(op).arns() {
			calls.errs[arn] = casesTestDenied
		}
	}

	total, _, err := casesScan(context.Background(), st, casesHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != casesTestTotal {
		t.Errorf("total = %d; want %d", total, casesTestTotal)
	}
	if got := casesWarnOps(warns()); !slices.Equal(got, []string{casesTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, casesTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanCases_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, casesTagsOp)
	calls := newCasesCalls()
	calls.errs[casesKindOf("ListCaseRules").arns()[0]] = casesTestThrottle
	calls.errs[casesKindOf("ListTemplates").arns()[3]] = casesTestBad

	_, _, err := casesScan(context.Background(), st, casesHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", casesTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored. A failed domain write lists no domain's children.
func TestScanCases_FailedWriteStopsScan(t *testing.T) {
	t.Run("fields", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		refuseStoreWrites(t, st, TypeCasesField)
		calls := newCasesCalls()
		calls.errs[casesKindOf("ListFields").arns()[0]] = casesTestDenied

		total, _, err := casesScan(context.Background(), st, casesHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		for _, op := range []string{"ListLayouts", "ListTemplates", "ListFieldOptions"} {
			if n := calls.count(op); n != 0 {
				t.Errorf("%s called %d times; want 0 after the failed write", op, n)
			}
		}
		// The domains and each domain's two case rules.
		if want := 2 + 2*2; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
	})
	t.Run("domains", func(t *testing.T) {
		st := newTestStore(t)
		refuseStoreWrites(t, st, TypeCasesDomain)
		calls := newCasesCalls()

		_, _, err := casesScan(context.Background(), st, casesHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		for _, op := range []string{"ListCaseRules", "ListFields", "ListLayouts", "ListTemplates", "ListFieldOptions"} {
			if n := calls.count(op); n != 0 {
				t.Errorf("%s called %d times after the failed domain write", op, n)
			}
		}
	})
}

// A listing's failed later page keeps the pages read before it, and its
// error does not stop the later phases. A domain not stored has no children
// read.
func TestScanCases_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op, domain               string
		kept, lost               string
		wantTotal, wantLastPhase int
	}{
		// domain-tagged's second page of case rules.
		{
			"ListCaseRules", "domain-tagged",
			casesKindOf("ListCaseRules").arn("domain-tagged", "tagged"),
			casesKindOf("ListCaseRules").arn("domain-tagged", "bare"),
			casesTestTotal - 1, 8,
		},
		// The domains' second page: domain-bare and its children are lost.
		{
			"ListDomains", "",
			casesKindOf("ListDomains").arn("", "tagged"), casesKindOf("ListDomains").arn("", "bare"),
			casesTestTotal - casesTestDomainRows, 4,
		},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newCasesCalls()
			handlers := casesHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil && sv(bacField[*string](in, "DomainId")) == c.domain {
					calls.record(c.op, in)
					return nil, casesTestBad
				}
				return list(in)
			}

			total, _, err := casesScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, casesKindOf(c.op).typ)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s, read before the failure, missing", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; its page failed", c.lost)
			}
			if got := len(rowsByNativeID(t, st, TypeCasesFieldOption)); got != c.wantLastPhase {
				t.Errorf("options (the last phase) = %d; want %d", got, c.wantLastPhase)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanCases_RepeatedTokenKeepsPages(t *testing.T) {
	for _, c := range []struct {
		op, typ string
		rows    int
		repeat  func(out any)
	}{
		{"ListLayouts", TypeCasesLayout, 4, func(out any) { out.(*connectcases.ListLayoutsOutput).NextToken = sdkaws.String("same") }},
		{"ListFieldOptions", TypeCasesFieldOption, 8, func(out any) { out.(*connectcases.ListFieldOptionsOutput).NextToken = sdkaws.String("same") }},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newCasesCalls()
			handlers := casesHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				out, err := list(in)
				c.repeat(out)
				return out, err
			}

			total, _, err := casesScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != casesTestTotal {
				t.Errorf("total = %d; want %d", total, casesTestTotal)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != c.rows {
				t.Errorf("%s rows = %d; want %d", c.typ, n, c.rows)
			}
		})
	}
}

// One parent's failed child listing costs the other parents none of their
// rows: a parent deleted since it was listed is skipped silently, a denial
// warns once under the op, and any other error, a transient one after the
// SDK's retries included, is returned after the rows are stored.
func TestScanCases_PerParentListingFailures(t *testing.T) {
	for _, c := range []struct {
		op, typ string
		// parent fails, by its DomainId, or its FieldId for options.
		parentField, parent string
		lost                []string
		// lostOptions is the options lost with the parent's rows.
		lostOptions int
	}{
		{"ListCaseRules", TypeCasesCaseRule, "DomainId", "domain-tagged", []string{
			casesKindOf("ListCaseRules").arn("domain-tagged", "tagged"), casesKindOf("ListCaseRules").arn("domain-tagged", "bare"),
		}, 0},
		{"ListFields", TypeCasesField, "DomainId", "domain-bare", []string{
			casesKindOf("ListFields").arn("domain-bare", "tagged"), casesKindOf("ListFields").arn("domain-bare", "bare"),
		}, 4},
		{"ListLayouts", TypeCasesLayout, "DomainId", "domain-tagged", []string{
			casesKindOf("ListLayouts").arn("domain-tagged", "tagged"), casesKindOf("ListLayouts").arn("domain-tagged", "bare"),
		}, 0},
		{"ListTemplates", TypeCasesTemplate, "DomainId", "domain-tagged", []string{
			casesKindOf("ListTemplates").arn("domain-tagged", "tagged"), casesKindOf("ListTemplates").arn("domain-tagged", "bare"),
		}, 0},
		{"ListFieldOptions", TypeCasesFieldOption, "FieldId", "field-tagged", []string{
			casesKindOf("ListFields").arn("domain-bare", "tagged") + "/option/high", casesKindOf("ListFields").arn("domain-bare", "tagged") + "/option/tier/1",
			casesKindOf("ListFields").arn("domain-tagged", "tagged") + "/option/high", casesKindOf("ListFields").arn("domain-tagged", "tagged") + "/option/tier/1",
		}, 0},
	} {
		for _, e := range []struct {
			name      string
			err       error
			outcome   serviceOutcome // -1: no error
			code      string
			wantWarns int
		}{
			{"parent deleted", casesTestGone, -1, "", 0},
			{"denied", casesTestDenied, -1, "", 1},
			{"transient", casesTestThrottle, outcomeTransient, "ThrottlingException", 0},
			{"other error", casesTestBad, outcomeError, "ValidationException", 0},
		} {
			t.Run(c.op+"/"+e.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "connectcases:"+c.op)
				calls := newCasesCalls()
				handlers := casesHandlers(calls)
				list := handlers[c.op]
				handlers[c.op] = func(in any) (any, error) {
					if sv(bacField[*string](in, c.parentField)) == c.parent {
						calls.record(c.op, in)
						return nil, e.err
					}
					return list(in)
				}

				total, _, err := casesScan(context.Background(), st, handlers)
				if e.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, e.outcome)
					assertAPICode(t, err, e.code)
				}
				rows := rowsByNativeID(t, st, c.typ)
				wantRows := 8 - len(c.lost)
				if c.typ != TypeCasesFieldOption {
					wantRows = 4 - len(c.lost)
				}
				if len(rows) != wantRows {
					t.Errorf("%s rows = %d; want %d", c.typ, len(rows), wantRows)
				}
				for _, id := range c.lost {
					if _, ok := rows[id]; ok {
						t.Errorf("%s stored; its parent failed", id)
					}
				}
				if want := casesTestTotal - len(c.lost) - c.lostOptions; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				if *warns != e.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, e.wantWarns)
				}
			})
		}
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, keyed by its name or by the fallback
// text when it has none. An option without a value is not stored and warned
// about alike, keyed by its name or the fallback text and its field; the
// fields are read in ARN order, so the warning names domain-bare's. A domain without a DomainId, or a single-select field without
// a FieldId, is stored and tagged without its children read, with one
// warning naming the empty field.
func TestScanCases_ElementsWithoutIdentifiers(t *testing.T) {
	noIDDomain := casesTestDomainARN("aws", testRegion, "x")
	noIDField := casesKindOf("ListFields").arn("domain-tagged", "x")
	bareField := casesKindOf("ListFields").arn("domain-bare", "bare")
	for _, c := range []struct {
		name, op, key string
		answer        func(in any) (any, bool)
		lost          int
		// stored is a row kept without its children, of storedType; field is
		// the empty field its warning names.
		stored, storedType, field string
	}{
		{
			"case rule without an ARN", "ListCaseRules", "rule-x",
			func(in any) (any, bool) {
				return &connectcases.ListCaseRulesOutput{CaseRules: []cctypes.CaseRuleSummary{{Name: sdkaws.String("rule-x")}}}, sv(bacField[*string](in, "DomainId")) == "domain-tagged"
			}, 2, "", "", "",
		},
		{
			"template without an ARN or a name", "ListTemplates", noIdentifier,
			func(in any) (any, bool) {
				return &connectcases.ListTemplatesOutput{Templates: []cctypes.TemplateSummary{{}}}, sv(bacField[*string](in, "DomainId")) == "domain-tagged"
			}, 2, "", "", "",
		},
		{
			"option without a value", "ListFieldOptions", "Nameless in field " + bareField,
			func(in any) (any, bool) {
				return &connectcases.ListFieldOptionsOutput{Options: []cctypes.FieldOption{{Name: sdkaws.String("Nameless")}}}, sv(bacField[*string](in, "FieldId")) == "field-bare"
			}, 4, "", "", "",
		},
		{
			"option without a value or a name", "ListFieldOptions", noIdentifier + " in field " + bareField,
			func(in any) (any, bool) {
				return &connectcases.ListFieldOptionsOutput{Options: []cctypes.FieldOption{{}}}, sv(bacField[*string](in, "FieldId")) == "field-bare"
			}, 4, "", "", "",
		},
		{
			"domain without a DomainId", "ListDomains", noIDDomain,
			func(any) (any, bool) {
				return &connectcases.ListDomainsOutput{Domains: []cctypes.DomainSummary{{DomainArn: sdkaws.String(noIDDomain), Name: sdkaws.String("x")}}}, true
			},
			// Replaces both domains: one stored, no children read.
			casesTestTotal - 1, noIDDomain, TypeCasesDomain, "DomainId",
		},
		{
			"field without a FieldId", "ListFields", noIDField,
			func(in any) (any, bool) {
				return &connectcases.ListFieldsOutput{Fields: []cctypes.FieldSummary{{
					FieldArn: sdkaws.String(noIDField), Name: sdkaws.String("field-x"), Namespace: cctypes.FieldNamespaceCustom, Type: cctypes.FieldTypeSingleSelect,
				}}}, sv(bacField[*string](in, "DomainId")) == "domain-tagged"
			},
			// Replaces domain-tagged's two fields and their four options: one stored.
			2 + 4 - 1, noIDField, TypeCasesField, "FieldId",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newCasesCalls()
			handlers := casesHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if out, ok := c.answer(in); ok {
					calls.record(c.op, in)
					return out, nil
				}
				return list(in)
			}

			total, _, err := casesScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := casesTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "connectcases:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under connectcases:%s naming %s", w, c.op, c.key)
			}
			if c.field != "" && !strings.Contains(w[0].Message, c.field) {
				t.Errorf("warning %q; want it to name the empty %s", w[0].Message, c.field)
			}
			if slices.Contains(calls.sortedTagARNs(), "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
			if c.stored == "" {
				return
			}
			if _, ok := rowsByNativeID(t, st, c.storedType)[c.stored]; !ok {
				t.Errorf("%s not stored", c.stored)
			}
			if !slices.Contains(calls.sortedTagARNs(), c.stored) {
				t.Errorf("tag ARNs lack %s", c.stored)
			}
		})
	}
}

// Only custom single-select fields have their options listed: a text field
// and a system single-select field are stored and tagged, and no options are
// listed for them.
func TestScanCases_OnlyCustomSingleSelectFieldsListOptions(t *testing.T) {
	st := newTestStore(t)
	calls := newCasesCalls()
	handlers := casesHandlers(calls)
	text := casesKindOf("ListFields").arn("domain-tagged", "text")
	system := casesKindOf("ListFields").arn("domain-tagged", "status")
	calls.tags[system] = map[string]*string{"k": sdkaws.String("v")}
	list := handlers["ListFields"]
	handlers["ListFields"] = func(in any) (any, error) {
		if sv(bacField[*string](in, "DomainId")) != "domain-tagged" {
			return list(in)
		}
		calls.record("ListFields", in)
		return &connectcases.ListFieldsOutput{Fields: []cctypes.FieldSummary{
			{FieldArn: sdkaws.String(text), FieldId: sdkaws.String("field-text"), Name: sdkaws.String("field-text"), Namespace: cctypes.FieldNamespaceCustom, Type: cctypes.FieldTypeText},
			{FieldArn: sdkaws.String(system), FieldId: sdkaws.String("status"), Name: sdkaws.String("status"), Namespace: cctypes.FieldNamespaceSystem, Type: cctypes.FieldTypeSingleSelect},
		}}, nil
	}

	if _, _, err := casesScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := calls.listedParents("ListFieldOptions"), []string{"domain-bare/field-bare", "domain-bare/field-tagged"}; !slices.Equal(got, want) {
		t.Errorf("ListFieldOptions parents = %v; want %v", got, want)
	}
	fields := rowsByNativeID(t, st, TypeCasesField)
	if _, ok := fields[text]; !ok {
		t.Errorf("text field %s not stored", text)
	}
	assertTagsMap(t, fields[system].TagsJSON, map[string]string{"k": "v"})
	if fields[system].ManagedByProvider {
		t.Error("system field ManagedByProvider = true; want false")
	}
}

// The fields' pages are written in no fixed order, so the option phase reads
// them sorted by ARN and its warnings name the least field first. The second
// domain's fields are held until the first domain's first page is written
// (its second page is asked for only then), so without the sort the first
// domain's field would be named.
func TestScanCases_FieldOptionWarningNamesLeastField(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCasesCalls()
	handlers := casesHandlers(calls)
	firstWritten := make(chan struct{})
	list := handlers["ListFields"]
	handlers["ListFields"] = func(in any) (any, error) {
		switch domain, next := sv(bacField[*string](in, "DomainId")), bacField[*string](in, "NextToken"); {
		case domain == "domain-tagged" && next != nil:
			close(firstWritten)
		case domain == "domain-bare" && next == nil:
			<-firstWritten
		}
		return list(in)
	}
	handlers["ListFieldOptions"] = func(in any) (any, error) {
		calls.record("ListFieldOptions", in)
		return nil, casesTestDenied
	}

	if _, _, err := casesScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	w := warns()
	if got := casesWarnOps(w); !slices.Equal(got, []string{casesListFieldOptionsOp}) {
		t.Fatalf("warnings = %v; want one under %s", w, casesListFieldOptionsOp)
	}
	if least := casesKindOf("ListFields").arn("domain-bare", "bare"); !strings.Contains(w[0].Message, least) {
		t.Errorf("warning %q; want it to name %s", w[0].Message, least)
	}
}

// The fields' pages are written in no fixed order, so the warning about
// single-select fields without a FieldId names the least such field, not the
// first written. domain-bare's field is held until domain-tagged's first page
// is written (its second page is asked for only then).
func TestScanCases_NoFieldIDWarningNamesLeastField(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCasesCalls()
	handlers := casesHandlers(calls)
	taggedWritten := make(chan struct{})
	handlers["ListFields"] = func(in any) (any, error) {
		calls.record("ListFields", in)
		domain, next := sv(bacField[*string](in, "DomainId")), bacField[*string](in, "NextToken")
		var token *string
		switch {
		case domain == "domain-tagged" && next != nil:
			close(taggedWritten)
			return &connectcases.ListFieldsOutput{}, nil
		case domain == "domain-tagged":
			token = sdkaws.String("p2")
		default:
			<-taggedWritten
		}
		return &connectcases.ListFieldsOutput{NextToken: token, Fields: []cctypes.FieldSummary{{
			FieldArn: sdkaws.String(casesKindOf("ListFields").arn(domain, "x")), Name: sdkaws.String("field-x"),
			Namespace: cctypes.FieldNamespaceCustom, Type: cctypes.FieldTypeSingleSelect,
		}}}, nil
	}

	if _, _, err := casesScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	w := warns()
	if got := casesWarnOps(w); !slices.Equal(got, []string{"connectcases:ListFields"}) {
		t.Fatalf("warnings = %v; want one under connectcases:ListFields", w)
	}
	if least := casesKindOf("ListFields").arn("domain-bare", "x"); !strings.Contains(w[0].Message, least) {
		t.Errorf("warning %q; want it to name %s", w[0].Message, least)
	}
	if n := calls.count("ListFieldOptions"); n != 0 {
		t.Errorf("ListFieldOptions called %d times; no field has a FieldId", n)
	}
}

// A listing phase's transient error beside a later phase's hard error is
// demoted to one warning under its own op, so the hard error decides the
// outcome; every phase's rows are kept.
func TestScanCases_ListThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "connectcases:ListTemplates")
	calls := newCasesCalls()
	handlers := casesHandlers(calls)
	list := handlers["ListTemplates"]
	handlers["ListTemplates"] = func(in any) (any, error) {
		if sv(bacField[*string](in, "DomainId")) == "domain-tagged" {
			calls.record("ListTemplates", in)
			return nil, casesTestThrottle
		}
		return list(in)
	}
	handlers["ListFieldOptions"] = func(in any) (any, error) {
		calls.record("ListFieldOptions", in)
		return nil, casesTestBad
	}

	total, _, err := casesScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("connectcases:ListTemplates warnings = %d; want 1", *warns)
	}
	// Every row but domain-tagged's two templates and the eight options.
	if want := casesTestTotal - 2 - 8; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
}

// A GovCloud or China ARN reaches the tag op unchanged, scanned in an
// account and region of that partition.
func TestScanCases_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			arn := casesTestDomainARN(pf.partition, pf.region, "d-p")
			st := newTestStore(t)
			calls := newCasesCalls()
			calls.tags[arn] = map[string]*string{"env": sdkaws.String("prod")}
			handlers := casesHandlers(calls)
			handlers["ListDomains"] = func(in any) (any, error) {
				calls.record("ListDomains", in)
				return &connectcases.ListDomainsOutput{Domains: []cctypes.DomainSummary{{DomainArn: sdkaws.String(arn), DomainId: sdkaws.String("d-p"), Name: sdkaws.String("d")}}}, nil
			}
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(handlers, casesTestDenied)

			if _, _, err := scanCases(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !slices.Contains(calls.sortedTagARNs(), arn) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %s among them", calls.sortedTagARNs(), arn)
			}
			r := rowsByNativeID(t, st, TypeCasesDomain)[arn]
			assertTagsMap(t, r.TagsJSON, map[string]string{"env": "prod"})
			if sv(r.Region) != pf.region {
				t.Errorf("Region = %q; want %s", sv(r.Region), pf.region)
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanCases_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newCasesCalls()
	handlers := casesHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListFields"]
	handlers["ListFields"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := casesScan(ctx, st, handlers)
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
	if n := calls.count("ListLayouts"); n != 0 {
		t.Errorf("ListLayouts called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanCases_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "cases.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := casesHandlers(newCasesCalls())
		handlers["ListTemplates"] = func(any) (any, error) { return nil, nx }
		total, _, err := casesScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := casesTestTotal - 4; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCasesCalls()
		calls.errs[casesKindOf("ListLayouts").arns()[2]] = nx
		_, _, err := casesScan(context.Background(), st, casesHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanCases(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
