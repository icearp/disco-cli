package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/billingconductor"
	bctypes "github.com/aws/aws-sdk-go-v2/service/billingconductor/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// bcTestTotal is every row of the fixture: two of each of the four types,
// one per page.
const bcTestTotal = 2 * 4

var (
	bcTestDenied    = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: billingconductor:List")
	bcTestPayerOnly = apiErr("AccessDeniedException", "Only payer account is authorized to perform this action")
	bcTestThrottle  = apiErr("ThrottlingException", "Rate exceeded")
	bcTestBad       = apiErr("ValidationException", "bad")
	bcTestGone      = apiErr("ResourceNotFoundException", "gone")
)

// bcKind is one listing: its op, the type it stores, and its ARN's resource
// kind.
type bcKind struct{ op, typ, kind string }

func bcKinds() []bcKind {
	return []bcKind{
		{"ListBillingGroups", TypeBillingConductorBillingGroup, "billinggroup"},
		{"ListCustomLineItems", TypeBillingConductorCustomLineItem, "customlineitem"},
		{"ListPricingPlans", TypeBillingConductorPricingPlan, "pricingplan"},
		{"ListPricingRules", TypeBillingConductorPricingRule, "pricingrule"},
	}
}

func bcKindOf(t *testing.T, op string) bcKind {
	t.Helper()
	for _, k := range bcKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Billing Conductor listing", op)
	return bcKind{}
}

func (k bcKind) arn(suffix string) string {
	id := k.kind + "-" + suffix
	if k.kind == "billinggroup" {
		id = bcGroupIDs[suffix]
	}
	return "arn:aws:billingconductor::" + testAccountID + ":" + k.kind + "/" + id
}

// bcGroupIDs is the id of the billing group with suffix: 12 digits, the only
// billing group id the tag op admits (bcGroupTagReadable).
var bcGroupIDs = map[string]string{"tagged": "111111111111", "bare": "222222222222"}

func (k bcKind) name(suffix string) string { return k.kind + "-" + suffix + "-name" }

// bcCreated is the CreationTime (epoch millis) of the element with suffix,
// and bcCreatedAt the CreatedAt it is stored with.
var (
	bcCreated   = map[string]int64{"tagged": 1700000000000, "bare": 1700000001000}
	bcCreatedAt = map[string]string{"tagged": "2023-11-14T22:13:20Z", "bare": "2023-11-14T22:13:21Z"}
)

// bcBillingGroupARN is the billing group every custom line item of the
// fixture applies to; its resolver reads it from the attributes.
var bcBillingGroupARN = "arn:aws:billingconductor::" + testAccountID + ":billinggroup/" + testAccountID

// page is k's output holding the element with suffix under arn (nil for an
// element listed without one), and next.
func (k bcKind) page(arn *string, suffix string, next *string) any {
	name, created := sdkaws.String(k.name(suffix)), bcCreated[suffix]
	switch k.op {
	case "ListBillingGroups":
		return &billingconductor.ListBillingGroupsOutput{NextToken: next, BillingGroups: []bctypes.BillingGroupListElement{
			{Arn: arn, Name: name, CreationTime: created, PrimaryAccountId: sdkaws.String(testAccountID)},
		}}
	case "ListCustomLineItems":
		return &billingconductor.ListCustomLineItemsOutput{NextToken: next, CustomLineItems: []bctypes.CustomLineItemListElement{
			{Arn: arn, Name: name, CreationTime: created, BillingGroupArn: &bcBillingGroupARN},
		}}
	case "ListPricingPlans":
		return &billingconductor.ListPricingPlansOutput{NextToken: next, PricingPlans: []bctypes.PricingPlanListElement{
			{Arn: arn, Name: name, CreationTime: created, Size: 3},
		}}
	default:
		return &billingconductor.ListPricingRulesOutput{NextToken: next, PricingRules: []bctypes.PricingRuleListElement{
			{Arn: arn, Name: name, CreationTime: created, Type: bctypes.PricingRuleTypeDiscount},
		}}
	}
}

// attrKey is the element field, besides Arn and Name, that a test pins in
// k's stored attributes, and its value.
func (k bcKind) attrKey() (string, any) {
	switch k.op {
	case "ListBillingGroups":
		return "PrimaryAccountId", testAccountID
	case "ListCustomLineItems":
		return "BillingGroupArn", bcBillingGroupARN
	case "ListPricingPlans":
		return "Size", float64(3)
	default:
		return "Type", "DISCOUNT"
	}
}

// bcCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type bcCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	calls      map[string]int
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newBCCalls tags every "-tagged" row with its type.
func newBCCalls() *bcCalls {
	c := &bcCalls{maxResults: map[string][]int32{}, calls: map[string]int{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range bcKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

// record notes op's call and its MaxResults.
func (c *bcCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.maxResults[op] = append(c.maxResults[op], *n)
	}
}

func (c *bcCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *bcCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *bcCalls) listTags(in any) (any, error) {
	arn := sv(in.(*billingconductor.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &billingconductor.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *bcCalls) twoPages(k bcKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		arn := k.arn(suffix)
		return k.page(&arn, suffix, next), nil
	}
}

// bcHandlers answers every op the scan calls from c.
func bcHandlers(c *bcCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range bcKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// bcScanIn runs the scan for acct in region against handlers; every op they
// do not answer fails with deny.
func bcScanIn(ctx context.Context, st *store.Store, acct *account, region string, handlers map[string]func(in any) (any, error), deny error) (int, int, error) {
	acct.cfg = opStubConfig(handlers, deny)
	client := billingconductor.NewFromConfig(acct.cfg, func(o *billingconductor.Options) { o.Region = region })
	return scanBillingConductor(ctx, client, acct, region, st, testScanID)
}

// bcScan runs the scan in the commercial home against handlers; every op
// they do not answer is denied.
func bcScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	return bcScanIn(ctx, st, newTestAccount(testAccountID), testRegion, handlers, bcTestDenied)
}

// bcAllTagARNs is the ARN of every row of the fixture, sorted.
func bcAllTagARNs() []string {
	var arns []string
	for _, k := range bcKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

func bcWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, each row under its listed ARN, named
// by its Name, Region "global", no Status, CreatedAt from its CreationTime,
// not managed, its listed element as attributes, and the tags its tag read
// returns (nil for none). ListTagsForResource is sent exactly the ARNs of
// every row; every listing is sent MaxResults 100. No error, no warning.
func TestScanBillingConductor_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBCCalls()

	total, inserted, err := bcScan(context.Background(), st, bcHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != bcTestTotal || inserted != bcTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, bcTestTotal, bcTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range bcKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", k.typ, len(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != "global" || r.Status != nil || sv(r.CreatedAt) != bcCreatedAt[suffix] || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, managed = %q, %q, %v, %q, %v; want %q, global, nil, %q, false",
					arn, sv(r.Name), sv(r.Region), r.Status, sv(r.CreatedAt), r.ManagedByProvider, k.name(suffix), bcCreatedAt[suffix])
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			key, want := k.attrKey()
			if attrs["Arn"] != arn || attrs["Name"] != k.name(suffix) || attrs[key] != want {
				t.Errorf("%s attrs = %s; want its element", arn, r.AttributesJSON)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if got := calls.maxResults[k.op]; !slices.Equal(got, []int32{100, 100}) {
			t.Errorf("%s MaxResults = %v; want 100 on both pages", k.op, got)
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, bcAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, bcAllTagARNs())
	}
}

// bcStoredWithTags reports whether the row of typ under arn is stored with
// tags. It does not fail the test, so a listing's handler can call it.
func bcStoredWithTags(st *store.Store, typ, arn string) bool {
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, Types: []string{typ}, Limit: util.AllResources})
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.NativeID == arn {
			return r.TagsJSON != nil
		}
	}
	return false
}

// Every listing writes its first page, with its tags, before it reads the
// second.
func TestScanBillingConductor_PagesWrittenAsRead(t *testing.T) {
	st := newTestStore(t)
	handlers := bcHandlers(newBCCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range bcKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = bcStoredWithTags(st, k.typ, k.arn("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := bcScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range bcKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.arn("tagged"))
		}
	}
}

// An AWS-managed pricing plan is stored managed and without tags, and is
// not sent to the tag op; a customer plan listed beside it is read. A zero
// CreationTime stores no CreatedAt.
func TestScanBillingConductor_ManagedPricingPlanNotRead(t *testing.T) {
	st := newTestStore(t)
	calls := newBCCalls()
	handlers := bcHandlers(calls)
	plans := bcKindOf(t, "ListPricingPlans")
	managedARN := "arn:aws:billingconductor::aws:pricingplan/BasicPricingPlan"
	customARN := plans.arn("tagged")
	handlers[plans.op] = func(in any) (any, error) {
		calls.record(plans.op, in)
		return &billingconductor.ListPricingPlansOutput{PricingPlans: []bctypes.PricingPlanListElement{
			{Arn: &managedARN, Name: sdkaws.String("BasicPricingPlan")},
			{Arn: &customARN, Name: sdkaws.String("custom")},
		}}, nil
	}

	if _, _, err := bcScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeBillingConductorPricingPlan)
	m, ok := rows[managedARN]
	if !ok {
		t.Fatalf("managed plan %s missing", managedARN)
	}
	if !m.ManagedByProvider || sv(m.Name) != "BasicPricingPlan" {
		t.Errorf("managed plan managed, Name = %v, %q; want true, BasicPricingPlan", m.ManagedByProvider, sv(m.Name))
	}
	if m.TagsJSON != nil || m.CreatedAt != nil {
		t.Errorf("managed plan TagsJSON, CreatedAt = %v, %v; want both nil", m.TagsJSON, m.CreatedAt)
	}
	if c := rows[customARN]; c.ManagedByProvider {
		t.Errorf("customer plan %s stored as managed", customARN)
	}
	assertTagsMap(t, rows[customARN].TagsJSON, map[string]string{"type": TypeBillingConductorPricingPlan})
	if slices.Contains(calls.sortedTagARNs(), managedARN) {
		t.Errorf("ListTagsForResource sent the managed plan %s", managedARN)
	}
	if !slices.Contains(calls.sortedTagARNs(), customARN) {
		t.Errorf("ListTagsForResource not sent the customer plan %s", customARN)
	}
}

// The payer-only refusal of the first call (a member account) reports the
// account not entitled: nothing stored, no warning, no other call.
func TestScanBillingConductor_PayerOnlyNotEntitled(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBCCalls()
	handlers := bcHandlers(calls)
	handlers["ListBillingGroups"] = func(in any) (any, error) {
		calls.record("ListBillingGroups", in)
		return nil, bcTestPayerOnly
	}

	total, inserted, err := bcScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeNotEntitled)
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range bcKinds() {
		want := 0
		if k.op == "ListBillingGroups" {
			want = 1
		}
		if n := calls.count(k.op); n != want {
			t.Errorf("%s called %d times; want %d", k.op, n, want)
		}
	}
}

// The payer-only refusal of a later listing's first page while nothing is
// stored reports the account not entitled, and the listings after it are not
// run. ListBillingGroups denied: its one warning is kept. Answered empty: no
// warning. Failed otherwise: its error is returned without the sentinel,
// the refusal is warned about under ListCustomLineItems, and the listings
// after the refusal are still not run.
func TestScanBillingConductor_PayerOnlyBeforeAnyRow(t *testing.T) {
	for _, c := range []struct {
		name      string
		groups    func() (any, error)
		want      serviceOutcome
		wantWarns []string
	}{
		{"groups denied", func() (any, error) { return nil, bcTestDenied }, outcomeNotEntitled, []string{"billingconductor:ListBillingGroups"}},
		{"groups empty", func() (any, error) { return &billingconductor.ListBillingGroupsOutput{}, nil }, outcomeNotEntitled, nil},
		{"groups failed", func() (any, error) { return nil, bcTestBad }, outcomeError, []string{"billingconductor:ListCustomLineItems"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBCCalls()
			handlers := bcHandlers(calls)
			handlers["ListBillingGroups"] = func(in any) (any, error) {
				calls.record("ListBillingGroups", in)
				return c.groups()
			}
			handlers["ListCustomLineItems"] = func(in any) (any, error) {
				calls.record("ListCustomLineItems", in)
				return nil, bcTestPayerOnly
			}

			total, inserted, err := bcScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, c.want)
			if got, want := errors.Is(err, errServiceNotEntitled), c.want == outcomeNotEntitled; got != want {
				t.Errorf("err = %v carries errServiceNotEntitled: %v; want %v", err, got, want)
			}
			if c.want == outcomeError && !isAPIErrorCode(err, "ValidationException") {
				t.Errorf("err = %v; want ListBillingGroups' ValidationException kept", err)
			}
			if total != 0 || inserted != 0 {
				t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
			}
			if got := bcWarnOps(warns()); !slices.Equal(got, c.wantWarns) {
				t.Errorf("warnings = %v; want %v", got, c.wantWarns)
			}
			for _, k := range bcKinds() {
				want := 0
				if k.op == "ListBillingGroups" || k.op == "ListCustomLineItems" {
					want = 1
				}
				if n := calls.count(k.op); n != want {
					t.Errorf("%s called %d times; want %d", k.op, n, want)
				}
			}
		})
	}
}

// The payer-only refusal after a call the service answered is a denial:
// warned about under its listing, the rows already listed kept, and the
// later listings run.
func TestScanBillingConductor_PayerOnlyAfterAnswersIsDenial(t *testing.T) {
	for _, c := range []struct {
		name      string
		op        string
		laterPage bool
		wantKept  int
	}{
		{"billing groups, later page", "ListBillingGroups", true, 1},
		{"pricing plans, first page", "ListPricingPlans", false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBCCalls()
			handlers := bcHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") != nil) == c.laterPage {
					calls.record(c.op, in)
					return nil, bcTestPayerOnly
				}
				return list(in)
			}

			total, _, err := bcScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if want := bcTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if got, want := bcWarnOps(warns()), []string{"billingconductor:" + c.op}; !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
			if n := len(rowsByNativeID(t, st, TypeBillingConductorPricingRule)); n != 2 {
				t.Errorf("pricing rules (the last phase) = %d; want 2", n)
			}
		})
	}
}

// A rescan stores the same rows without inserting any.
func TestScanBillingConductor_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := bcScan(context.Background(), st, bcHandlers(newBCCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := bcScan(context.Background(), st, bcHandlers(newBCCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != bcTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, bcTestTotal)
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanBillingConductor_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBCCalls()
	handlers := bcHandlers(calls)
	empty := map[string]any{
		"ListBillingGroups":   &billingconductor.ListBillingGroupsOutput{},
		"ListCustomLineItems": &billingconductor.ListCustomLineItemsOutput{},
		"ListPricingPlans":    &billingconductor.ListPricingPlansOutput{},
		"ListPricingRules":    &billingconductor.ListPricingRulesOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	total, inserted, err := bcScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range bcKinds() {
		if n := calls.count(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource called %d times; want 0", n)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently. A denial keeps the row
// untagged and warns once under the tag op with the scan's scope; a throttle
// keeps it and is transient; any other error keeps it and is an error.
func TestScanBillingConductor_TagReadFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		op        string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"custom line item/not found", "ListCustomLineItems", bcTestGone, -1, "", bcTestTotal - 1, false, false},
		{"billing group/denied", "ListBillingGroups", bcTestDenied, -1, "", bcTestTotal, true, true},
		{"pricing plan/transient", "ListPricingPlans", bcTestThrottle, outcomeTransient, "ThrottlingException", bcTestTotal, true, false},
		{"pricing rule/other error", "ListPricingRules", bcTestBad, outcomeError, "ValidationException", bcTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBCCalls()
			k := bcKindOf(t, c.op)
			arn := k.arn("tagged")
			calls.errs[arn] = c.err

			total, _, err := bcScan(context.Background(), st, bcHandlers(calls))
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
			rows := rowsByNativeID(t, st, k.typ)
			r, ok := rows[arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if _, ok := rows[k.arn("bare")]; !ok {
				t.Errorf("sibling %s missing", k.arn("bare"))
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{bcTagsOp}
			}
			if got := bcWarnOps(w); !slices.Equal(got, want) {
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

// Denied tag reads in three phases send one warning for the whole scan; the
// rows are kept.
func TestScanBillingConductor_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBCCalls()
	calls.errs[bcKindOf(t, "ListBillingGroups").arn("bare")] = bcTestDenied
	calls.errs[bcKindOf(t, "ListPricingPlans").arn("tagged")] = bcTestDenied
	calls.errs[bcKindOf(t, "ListPricingRules").arn("bare")] = bcTestDenied

	total, _, err := bcScan(context.Background(), st, bcHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != bcTestTotal {
		t.Errorf("total = %d; want %d", total, bcTestTotal)
	}
	if got := bcWarnOps(warns()); !slices.Equal(got, []string{bcTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, bcTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanBillingConductor_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, bcTagsOp)
	calls := newBCCalls()
	calls.errs[bcKindOf(t, "ListCustomLineItems").arn("bare")] = bcTestThrottle
	calls.errs[bcKindOf(t, "ListPricingRules").arn("tagged")] = bcTestBad

	_, _, err := bcScan(context.Background(), st, bcHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", bcTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanBillingConductor_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeBillingConductorPricingPlan)
	calls := newBCCalls()
	calls.errs[bcKindOf(t, "ListPricingPlans").arn("tagged")] = bcTestDenied

	total, _, err := bcScan(context.Background(), st, bcHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListPricingRules"); n != 0 {
		t.Errorf("ListPricingRules called %d times; want 0 after the failed write", n)
	}
	if total != 4 {
		t.Errorf("total = %d; want the 4 billing groups and custom line items", total)
	}
	for _, typ := range []string{TypeBillingConductorBillingGroup, TypeBillingConductorCustomLineItem} {
		if n := len(rowsByNativeID(t, st, typ)); n != 2 {
			t.Errorf("%s rows = %d; want 2", typ, n)
		}
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the later phases. A denied listing is warned about
// under its op instead, and the later phases run too.
func TestScanBillingConductor_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		name      string
		op        string
		err       error
		laterPage bool
		wantKept  int
	}{
		{"first phase, later page fails", "ListBillingGroups", bcTestBad, true, 1},
		{"later page fails mid-scan", "ListPricingPlans", bcTestBad, true, 1},
		{"denied", "ListCustomLineItems", bcTestDenied, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBCCalls()
			handlers := bcHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") != nil) == c.laterPage {
					calls.record(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := bcScan(context.Background(), st, handlers)
			var wantWarn []string
			if isAccessDenied(c.err) {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				wantWarn = []string{"billingconductor:" + c.op}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ValidationException")
			}
			if got := bcWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := bcTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			k := bcKindOf(t, c.op)
			rows := rowsByNativeID(t, st, k.typ)
			if _, ok := rows[k.arn("tagged")]; ok != (c.wantKept == 1) {
				t.Errorf("%s stored = %v; want %v", k.arn("tagged"), ok, c.wantKept == 1)
			}
			if _, ok := rows[k.arn("bare")]; ok {
				t.Errorf("%s stored; its page failed", k.arn("bare"))
			}
			if n := len(rowsByNativeID(t, st, TypeBillingConductorPricingRule)); n != 2 {
				t.Errorf("pricing rules (the last phase) = %d; want 2", n)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanBillingConductor_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newBCCalls()
	handlers := bcHandlers(calls)
	twoPages := handlers["ListCustomLineItems"]
	handlers["ListCustomLineItems"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*billingconductor.ListCustomLineItemsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := bcScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != bcTestTotal {
		t.Errorf("total = %d; want %d", total, bcTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeBillingConductorCustomLineItem)); n != 2 {
		t.Errorf("custom line items = %d; want 2", n)
	}
}

// An element listed without an ARN is not stored, not sent to the tag op,
// and warned about once under its listing; the rest of the listing is
// stored.
func TestScanBillingConductor_ElementWithoutARN(t *testing.T) {
	for _, k := range bcKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBCCalls()
			handlers := bcHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record(k.op, in)
					return k.page(nil, "noarn", nil), nil
				}
				return twoPages(in)
			}

			total, _, err := bcScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := bcTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an ARN", k.typ, n)
			}
			want := slices.DeleteFunc(bcAllTagARNs(), func(a string) bool { return a == k.arn("bare") })
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			if got := bcWarnOps(warns()); !slices.Equal(got, []string{"billingconductor:" + k.op}) {
				t.Errorf("warnings = %v; want one under billingconductor:%s", got, k.op)
			}
		})
	}
}

// A billing group whose id is not 12 digits (the listing admits 10 to 12
// alphanumerics; the tag op's ResourceArn pattern only 12 digits) is stored
// without tags, and is not sent to the tag op, without a warning; its
// 12-digit sibling is read.
func TestScanBillingConductor_BillingGroupIDOutsideTagPattern(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBCCalls()
	handlers := bcHandlers(calls)
	groups := bcKindOf(t, "ListBillingGroups")
	prefix := "arn:aws:billingconductor::" + testAccountID + ":billinggroup/"
	shortARN, alnumARN, shortDigitsARN, digitsARN := prefix+"abcde12345", prefix+"abcdef123456", prefix+"1234567890", groups.arn("tagged")
	calls.tags[shortARN] = map[string]string{"k": "short"}
	calls.tags[alnumARN] = map[string]string{"k": "alnum"}
	calls.tags[shortDigitsARN] = map[string]string{"k": "short-digits"}
	handlers[groups.op] = func(in any) (any, error) {
		calls.record(groups.op, in)
		return &billingconductor.ListBillingGroupsOutput{BillingGroups: []bctypes.BillingGroupListElement{
			{Arn: &shortARN, Name: sdkaws.String("short")},
			{Arn: &alnumARN, Name: sdkaws.String("alnum")},
			{Arn: &shortDigitsARN, Name: sdkaws.String("short-digits")},
			{Arn: &digitsARN, Name: sdkaws.String("digits")},
		}}, nil
	}

	if _, _, err := bcScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	rows := rowsByNativeID(t, st, TypeBillingConductorBillingGroup)
	if len(rows) != 4 {
		t.Errorf("billing groups = %d; want 4", len(rows))
	}
	for _, arn := range []string{shortARN, alnumARN, shortDigitsARN} {
		r, ok := rows[arn]
		if !ok {
			t.Errorf("%s missing", arn)
			continue
		}
		if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
		}
	}
	assertTagsMap(t, rows[digitsARN].TagsJSON, map[string]string{"type": TypeBillingConductorBillingGroup})
	want := slices.DeleteFunc(bcAllTagARNs(), func(a string) bool { return a == groups.arn("bare") })
	if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
}

// bcDropName clears the Name of the one element of out, a page of k.page.
func bcDropName(t *testing.T, out any) {
	t.Helper()
	switch o := out.(type) {
	case *billingconductor.ListBillingGroupsOutput:
		o.BillingGroups[0].Name = nil
	case *billingconductor.ListCustomLineItemsOutput:
		o.CustomLineItems[0].Name = nil
	case *billingconductor.ListPricingPlansOutput:
		o.PricingPlans[0].Name = nil
	case *billingconductor.ListPricingRulesOutput:
		o.PricingRules[0].Name = nil
	default:
		t.Fatalf("%T is no Billing Conductor page", out)
	}
}

// An element listed with an ARN but no Name is stored named "" (not NULL),
// with its tags read.
func TestScanBillingConductor_ElementWithoutName(t *testing.T) {
	for _, k := range bcKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newBCCalls()
			handlers := bcHandlers(calls)
			arn := k.arn("tagged")
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				out := k.page(&arn, "tagged", nil)
				bcDropName(t, out)
				return out, nil
			}

			if _, _, err := bcScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			r, ok := rowsByNativeID(t, st, k.typ)[arn]
			if !ok {
				t.Fatalf("%s missing", arn)
			}
			if r.Name == nil || *r.Name != "" {
				t.Errorf("%s Name = %v; want a pointer to \"\"", arn, r.Name)
			}
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
		})
	}
}

// A China ARN (the op's ResourceArn pattern admits aws and aws-cn) reaches
// the tag op unchanged and is the row's NativeID.
func TestScanBillingConductor_TagReadsKeepPartitionARNs(t *testing.T) {
	st := newTestStore(t)
	calls := newBCCalls()
	handlers := bcHandlers(calls)
	prefix := "arn:aws-cn:billingconductor::" + testAccountID + ":"
	groupARN := prefix + "billinggroup/" + testAccountID
	ruleARN := prefix + "pricingrule/rule000001"
	calls.tags[groupARN] = map[string]string{"k": "group"}
	calls.tags[ruleARN] = map[string]string{"k": "rule"}
	groups, rules := bcKindOf(t, "ListBillingGroups"), bcKindOf(t, "ListPricingRules")
	handlers[groups.op] = func(in any) (any, error) {
		calls.record(groups.op, in)
		return groups.page(&groupARN, "tagged", nil), nil
	}
	handlers[rules.op] = func(in any) (any, error) {
		calls.record(rules.op, in)
		return rules.page(&ruleARN, "tagged", nil), nil
	}
	acct := newTestAccount(testAccountID)
	acct.Partition = "aws-cn"

	if _, _, err := bcScanIn(context.Background(), st, acct, "cn-northwest-1", handlers, bcTestDenied); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, want := range []string{groupARN, ruleARN} {
		if !slices.Contains(calls.sortedTagARNs(), want) {
			t.Errorf("ListTagsForResource ARNs %v lack %s", calls.sortedTagARNs(), want)
		}
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeBillingConductorBillingGroup)[groupARN].TagsJSON, map[string]string{"k": "group"})
	assertTagsMap(t, rowsByNativeID(t, st, TypeBillingConductorPricingRule)[ruleARN].TagsJSON, map[string]string{"k": "rule"})
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanBillingConductor_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newBCCalls()
	handlers := bcHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListCustomLineItems"]
	handlers["ListCustomLineItems"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := bcScan(ctx, st, handlers)
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
	for _, op := range []string{"ListPricingPlans", "ListPricingRules"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanBillingConductor_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "billingconductor.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := bcHandlers(newBCCalls())
		handlers["ListPricingRules"] = func(any) (any, error) { return nil, nx }
		total, _, err := bcScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := bcTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newBCCalls()
		calls.errs[bcKindOf(t, "ListBillingGroups").arn("tagged")] = nx
		_, _, err := bcScan(context.Background(), st, bcHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newBCCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range bcKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		_, _, err := bcScanIn(context.Background(), st, newTestAccount(testAccountID), testRegion, handlers, nx)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListCustomLineItems"); n != 0 {
			t.Errorf("ListCustomLineItems called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
