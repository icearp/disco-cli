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
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/invoicing"
	invtypes "github.com/aws/aws-sdk-go-v2/service/invoicing/types"
	"github.com/icearp/disco-cli/store"
)

// invTestTotal is every row of the fixture: two of each of the two types,
// one per page.
const invTestTotal = 2 * 2

var (
	invTestDenied    = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: invoicing:List")
	invTestThrottle  = apiErr("ThrottlingException", "Rate exceeded")
	invTestBad       = apiErr("ValidationException", "bad")
	invTestGone      = apiErr("ResourceNotFoundException", "gone")
	invTestPayerOnly = apiErr("ValidationException", "Operation not permitted for member accounts. This API is only allowed for regular or payer accounts.")
	invTestCreated   = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	invTestUpdated   = time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	invTestScope     = testAccountID + "/us-east-1"
)

// invTestKind is one listing: its op, the type it stores, its ARN's resource
// kind and the noun its warnings name its elements by.
type invTestKind struct {
	op, typ, kind, what string
}

func invTestKinds() []invTestKind {
	return []invTestKind{
		{"ListInvoiceUnits", TypeInvoicingInvoiceUnit, "invoice-unit", "invoice units"},
		{"ListProcurementPortalPreferences", TypeInvoicingProcurementPortalPreference, "procurement-portal-preference", "procurement portal preferences"},
	}
}

func invTestKindOf(t *testing.T, op string) invTestKind {
	t.Helper()
	for _, k := range invTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Invoicing listing", op)
	return invTestKind{}
}

// key is what names k's element with suffix in a warning: an invoice unit's
// name, a preference's buyer identifier.
func (k invTestKind) key(suffix string) string { return k.kind + "-" + suffix }

// arnIn is the ARN of k's element with suffix owned by account, in the
// Service Reference's ARNFormat: arn:{p}:invoicing::{acct}:{kind}/{id}.
func (k invTestKind) arnIn(partition, account, suffix string) string {
	return "arn:" + partition + ":invoicing::" + account + ":" + k.kind + "/id-" + suffix
}

func (k invTestKind) arn(suffix string) string { return k.arnIn("aws", testAccountID, suffix) }

// element is k's listed element with suffix, carrying arn (nil for an
// element listed without one).
func (k invTestKind) element(arn *string, suffix string) any {
	if k.op == "ListInvoiceUnits" {
		return invtypes.InvoiceUnit{
			InvoiceUnitArn: arn, Name: sdkaws.String(k.key(suffix)), Description: sdkaws.String("unit " + suffix),
			InvoiceReceiver: sdkaws.String("111122223333"), LastModified: &invTestUpdated,
			Rule:                   &invtypes.InvoiceUnitRule{LinkedAccounts: []string{"222233334444"}},
			TaxInheritanceDisabled: sdkaws.Bool(true),
		}
	}
	return invtypes.ProcurementPortalPreferenceSummary{
		ProcurementPortalPreferenceArn: arn, AwsAccountId: sdkaws.String(testAccountID),
		ProcurementPortalName: invtypes.ProcurementPortalNameCoupa, BuyerIdentifier: sdkaws.String(k.key(suffix)),
		CreateDate: &invTestCreated, LastUpdateDate: &invTestUpdated,
		EinvoiceDeliveryEnabled:          sdkaws.Bool(true),
		EinvoiceDeliveryPreferenceStatus: invtypes.ProcurementPortalPreferenceStatusActive,
	}
}

// page is k's output holding element e, and next.
func (k invTestKind) page(e any, next *string) any {
	if u, ok := e.(invtypes.InvoiceUnit); ok {
		return &invoicing.ListInvoiceUnitsOutput{NextToken: next, InvoiceUnits: []invtypes.InvoiceUnit{u}}
	}
	return &invoicing.ListProcurementPortalPreferencesOutput{NextToken: next, ProcurementPortalPreferences: []invtypes.ProcurementPortalPreferenceSummary{e.(invtypes.ProcurementPortalPreferenceSummary)}}
}

// invTestCalls records the calls of a scan and answers the tag reads, by
// ARN, with tags or tagErrs. partition is that of the ARNs the listings
// return.
type invTestCalls struct {
	mu         sync.Mutex
	partition  string
	calls      map[string]int
	maxResults map[string][]*int32
	tagARNs    []string
	tags       map[string][]invtypes.ResourceTag
	tagErrs    map[string]error
}

// newInvTestCalls tags every "-tagged" row with its type.
func newInvTestCalls() *invTestCalls { return newInvTestCallsIn("aws") }

func newInvTestCallsIn(partition string) *invTestCalls {
	c := &invTestCalls{
		partition: partition, calls: map[string]int{}, maxResults: map[string][]*int32{},
		tags: map[string][]invtypes.ResourceTag{}, tagErrs: map[string]error{},
	}
	for _, k := range invTestKinds() {
		c.tags[k.arnIn(partition, testAccountID, "tagged")] = []invtypes.ResourceTag{{Key: sdkaws.String("type"), Value: sdkaws.String(k.typ)}}
	}
	return c
}

// record notes op's call and the MaxResults a listing was sent.
func (c *invTestCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if op != "ListTagsForResource" {
		c.maxResults[op] = append(c.maxResults[op], bacField[*int32](in, "MaxResults"))
	}
}

func (c *invTestCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *invTestCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *invTestCalls) listTags(in any) (any, error) {
	arn := sv(in.(*invoicing.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	return &invoicing.ListTagsForResourceOutput{ResourceTags: c.tags[arn]}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *invTestCalls) twoPages(k invTestKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(k.element(sdkaws.String(k.arnIn(c.partition, testAccountID, suffix)), suffix), next), nil
	}
}

// failingFirst answers k's first page with err and records the call.
func (c *invTestCalls) failingFirst(k invTestKind, err error) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		return nil, err
	}
}

// failingLater answers k's first page as twoPages does and its second with
// err.
func (c *invTestCalls) failingLater(k invTestKind, err error) func(any) (any, error) {
	first := c.twoPages(k)
	return func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			c.record(k.op, in)
			return nil, err
		}
		return first(in)
	}
}

// invTestHandlers answers every op the scan calls from c.
func invTestHandlers(c *invTestCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range invTestKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// invTestScanAs runs the scan for acct against handlers through the real
// client, calling us-east-1; every op they do not answer is denied.
func invTestScanAs(ctx context.Context, acct *account, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	client := invoicing.NewFromConfig(opStubConfig(handlers, invTestDenied))
	return scanInvoicingIn(ctx, client, acct, "us-east-1", st, testScanID)
}

func invTestScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	return invTestScanAs(ctx, newTestAccount(testAccountID), st, handlers)
}

// invAllTagARNs is the ARN of every row of the fixture, sorted.
func invAllTagARNs() []string {
	var arns []string
	for _, k := range invTestKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

// invStoredWithTags reports whether the row under arn is stored with tags.
// It does not fail the test, so a listing's handler can call it.
func invStoredWithTags(st *store.Store, arn string) bool {
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
	return err == nil && r != nil && r.TagsJSON != nil
}

// Both listings store both their pages, each row under its listed ARN, in
// the scanned account's name, in the global region, not managed, with no
// Status, with its listed element as its attributes and the tags its tag
// read returns (nil for none). An invoice unit is named by its name and has
// no CreatedAt; a preference has no name and its CreateDate as CreatedAt.
// ListTagsForResource is sent exactly the ARNs of every row; every
// ListInvoiceUnits call is sent MaxResults 500, every
// ListProcurementPortalPreferences call 100. No error, no warning.
func TestScanInvoicing_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newInvTestCalls()

	total, inserted, err := invTestScan(context.Background(), st, invTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != invTestTotal || inserted != invTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, invTestTotal, invTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	wantMax := map[string]int32{"ListInvoiceUnits": 500, "ListProcurementPortalPreferences": 100}
	for _, k := range invTestKinds() {
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
			if sv(r.Region) != "global" || r.ManagedByProvider || r.Status != nil || sv(r.AccountName) != newTestAccount(testAccountID).Name {
				t.Errorf("%s Region, Managed, Status, AccountName = %q, %v, %v, %q; want global, false, none, the account's",
					arn, sv(r.Region), r.ManagedByProvider, r.Status, sv(r.AccountName))
			}
			if k.op == "ListInvoiceUnits" {
				if sv(r.Name) != k.key(suffix) || r.CreatedAt != nil {
					t.Errorf("%s Name, CreatedAt = %q, %q; want %q, none", arn, sv(r.Name), sv(r.CreatedAt), k.key(suffix))
				}
			} else if want := "2026-03-04T05:06:07Z"; r.Name != nil || sv(r.CreatedAt) != want {
				t.Errorf("%s Name, CreatedAt = %q, %q; want none, %q", arn, sv(r.Name), sv(r.CreatedAt), want)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			if listed := kcJSONValue(t, k.element(sdkaws.String(arn), suffix)); !reflect.DeepEqual(attrs, listed) {
				t.Errorf("%s attrs = %s; want its listed element %v", arn, r.AttributesJSON, listed)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		want := wantMax[k.op]
		if got := calls.maxResults[k.op]; len(got) != 2 || got[0] == nil || *got[0] != want || got[1] == nil || *got[1] != want {
			t.Errorf("%s MaxResults = %v; want two calls sent %d", k.op, got, want)
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, invAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, invAllTagARNs())
	}
}

// The registered scan calls every op, the tag reads included, in the home
// region us-east-1 whatever region the config names.
func TestScanInvoicing_CallsGoToHomeRegion(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	calls := newInvTestCalls()
	var regions parentCalls
	cfg := opStubConfig(invTestHandlers(calls), invTestDenied)
	cfg.Region = "eu-west-1"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, _, err := scanInvoicing(context.Background(), acct, "", st, testScanID)
	if err != nil {
		t.Fatalf("scanInvoicing: %v", err)
	}
	if total != invTestTotal {
		t.Errorf("total = %d; want %d", total, invTestTotal)
	}
	if n := calls.count("ListTagsForResource"); n != invTestTotal {
		t.Errorf("ListTagsForResource called %d times; want %d", n, invTestTotal)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"us-east-1"}) {
		t.Errorf("calls went to regions %v; want [us-east-1]", got)
	}
}

// Every listing writes its first page, with its tags, before it reads the
// second.
func TestScanInvoicing_PagesWrittenAsRead(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	handlers := invTestHandlers(newInvTestCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range invTestKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = invStoredWithTags(st, k.arn("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := invTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range invTestKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.arn("tagged"))
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanInvoicing_RescanInsertsNone(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := invTestScan(context.Background(), st, invTestHandlers(newInvTestCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := invTestScan(context.Background(), st, invTestHandlers(newInvTestCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != invTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, invTestTotal)
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanInvoicing_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newInvTestCalls()
	handlers := invTestHandlers(calls)
	empty := map[string]any{
		"ListInvoiceUnits":                 &invoicing.ListInvoiceUnitsOutput{},
		"ListProcurementPortalPreferences": &invoicing.ListProcurementPortalPreferencesOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	total, inserted, err := invTestScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range invTestKinds() {
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
// untagged and warns once under the tag op with the home region's scope; a
// throttle keeps it and is transient; any other error keeps it and is an
// error.
func TestScanInvoicing_TagReadFailures(t *testing.T) {
	t.Parallel()
	type tagFailure struct {
		name      string
		op        string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}
	var cases []tagFailure
	for _, k := range invTestKinds() {
		cases = append(cases,
			tagFailure{k.kind + "/not found", k.op, invTestGone, -1, "", invTestTotal - 1, false, false},
			tagFailure{k.kind + "/denied", k.op, invTestDenied, -1, "", invTestTotal, true, true},
			tagFailure{k.kind + "/transient", k.op, invTestThrottle, outcomeTransient, "ThrottlingException", invTestTotal, true, false},
			tagFailure{k.kind + "/other error", k.op, invTestBad, outcomeError, "ValidationException", invTestTotal, true, false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newInvTestCalls()
			k := invTestKindOf(t, c.op)
			arn := k.arn("tagged")
			calls.tagErrs[arn] = c.err

			total, _, err := invTestScan(context.Background(), st, invTestHandlers(calls))
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
				want = []string{invoicingTagsOp}
			}
			if got := afWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != invTestScope {
					t.Errorf("warning Scope %q; want %s", x.Scope, invTestScope)
				}
			}
		})
	}
}

// Denied tag reads in both phases send one warning for the whole scan; the
// rows are kept.
func TestScanInvoicing_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newInvTestCalls()
	calls.tagErrs[invTestKindOf(t, "ListInvoiceUnits").arn("bare")] = invTestDenied
	calls.tagErrs[invTestKindOf(t, "ListProcurementPortalPreferences").arn("tagged")] = invTestDenied

	total, _, err := invTestScan(context.Background(), st, invTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != invTestTotal {
		t.Errorf("total = %d; want %d", total, invTestTotal)
	}
	if got := afWarnOps(warns()); !slices.Equal(got, []string{invoicingTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, invoicingTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanInvoicing_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, invoicingTagsOp)
	calls := newInvTestCalls()
	calls.tagErrs[invTestKindOf(t, "ListInvoiceUnits").arn("bare")] = invTestThrottle
	calls.tagErrs[invTestKindOf(t, "ListProcurementPortalPreferences").arn("tagged")] = invTestBad

	_, _, err := invTestScan(context.Background(), st, invTestHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", invoicingTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), and the preference phase does not run.
func TestScanInvoicing_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeInvoicingInvoiceUnit)
	calls := newInvTestCalls()
	calls.tagErrs[invTestKindOf(t, "ListInvoiceUnits").arn("tagged")] = invTestDenied

	total, _, err := invTestScan(context.Background(), st, invTestHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListProcurementPortalPreferences"); n != 0 {
		t.Errorf("ListProcurementPortalPreferences called %d times; want 0 after the failed write", n)
	}
	if total != 0 {
		t.Errorf("total = %d; want 0", total)
	}
}

// A denied tag read is still warned about when a later phase fails: its row
// was stored, so the scan's error does not hide why it has no tags.
func TestScanInvoicing_TagDenialWarnedBesideLaterFailure(t *testing.T) {
	t.Parallel()
	prefs := invTestKinds()[1]
	for _, c := range []struct {
		name string
		fail func(t *testing.T, st *store.Store, calls *invTestCalls, handlers map[string]func(any) (any, error))
		want func(t *testing.T, err error)
	}{
		{"later listing page fails", func(_ *testing.T, _ *store.Store, calls *invTestCalls, handlers map[string]func(any) (any, error)) {
			handlers[prefs.op] = calls.failingLater(prefs, invTestBad)
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
		}},
		{"later tag read fails", func(_ *testing.T, _ *store.Store, calls *invTestCalls, _ map[string]func(any) (any, error)) {
			calls.tagErrs[prefs.arn("tagged")] = invTestBad
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
		}},
		{"later write fails", func(t *testing.T, st *store.Store, _ *invTestCalls, _ map[string]func(any) (any, error)) {
			refuseStoreWrites(t, st, TypeInvoicingProcurementPortalPreference)
		}, func(t *testing.T, err error) {
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newInvTestCalls()
			denied := invTestKindOf(t, "ListInvoiceUnits").arn("bare")
			calls.tagErrs[denied] = invTestDenied
			handlers := invTestHandlers(calls)
			c.fail(t, st, calls, handlers)

			_, _, err := invTestScan(context.Background(), st, handlers)
			c.want(t, err)
			w := warns()
			if got := afWarnOps(w); !slices.Equal(got, []string{invoicingTagsOp}) {
				t.Fatalf("warnings = %v; want one under %s", w, invoicingTagsOp)
			}
			if x := w[0]; x.Scope != invTestScope || !strings.Contains(x.Message, "first "+denied) {
				t.Errorf("warning = %+v; want Scope %s, keyed %s", x, invTestScope, denied)
			}
			r, ok := rowsByNativeID(t, st, TypeInvoicingInvoiceUnit)[denied]
			if !ok {
				t.Fatalf("%s missing", denied)
			}
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", denied, *r.TagsJSON)
			}
		})
	}
}

// A listing's failed first or later page is returned as an error, keeping
// the page read before it, and does not stop the other phase. A listing's
// first or later page denied is
// warned about once under its op, keeps the pages read before it, and the
// other phase runs too.
func TestScanInvoicing_ListFailureKeepsPagesAndOtherPhase(t *testing.T) {
	t.Parallel()
	type listFailure struct {
		name      string
		op        string
		err       error
		laterPage bool
		wantKept  int
		wantWarn  bool
	}
	var cases []listFailure
	for _, k := range invTestKinds() {
		cases = append(cases,
			listFailure{"first page fails/" + k.op, k.op, invTestBad, false, 0, false},
			listFailure{"later page fails/" + k.op, k.op, invTestBad, true, 1, false},
			listFailure{"first page denied/" + k.op, k.op, invTestDenied, false, 0, true},
			listFailure{"later page denied/" + k.op, k.op, invTestDenied, true, 1, true})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newInvTestCalls()
			handlers := invTestHandlers(calls)
			k := invTestKindOf(t, c.op)
			if c.laterPage {
				handlers[c.op] = calls.failingLater(k, c.err)
			} else {
				handlers[c.op] = calls.failingFirst(k, c.err)
			}

			total, _, err := invTestScan(context.Background(), st, handlers)
			if c.wantWarn {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ValidationException")
			}
			var wantWarn []string
			if c.wantWarn {
				wantWarn = []string{"invoicing:" + c.op}
			}
			if got := afWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := invTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			for _, other := range invTestKinds() {
				rows := rowsByNativeID(t, st, other.typ)
				if other.op != c.op {
					if len(rows) != 2 {
						t.Errorf("%s rows = %d; want 2, its phase run", other.typ, len(rows))
					}
					continue
				}
				if _, ok := rows[k.arn("tagged")]; ok != (c.wantKept == 1) {
					t.Errorf("%s stored = %v; want %v", k.arn("tagged"), ok, c.wantKept == 1)
				}
				if _, ok := rows[k.arn("bare")]; ok {
					t.Errorf("%s stored; its page failed", k.arn("bare"))
				}
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages, and
// the other phase runs.
func TestScanInvoicing_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, k := range invTestKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newInvTestCalls()
			handlers := invTestHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				out, err := twoPages(in)
				reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(sdkaws.String("same")))
				return out, err
			}

			total, _, err := invTestScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != invTestTotal {
				t.Errorf("total = %d; want %d", total, invTestTotal)
			}
			if n := calls.count(k.op); n != 2 {
				t.Errorf("%s called %d times; want 2", k.op, n)
			}
			for _, other := range invTestKinds() {
				if n := len(rowsByNativeID(t, st, other.typ)); n != 2 {
					t.Errorf("%s rows = %d; want 2", other.typ, n)
				}
			}
		})
	}
}

// An element listed without an ARN is not stored, not read, and warned about
// once under its listing with the home region's scope, calling the
// listing's elements by their noun and keyed by its name (an invoice unit)
// or buyer identifier (a preference); the rest of the listing is stored.
func TestScanInvoicing_ElementWithoutARN(t *testing.T) {
	t.Parallel()
	for _, k := range invTestKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newInvTestCalls()
			handlers := invTestHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record(k.op, in)
					return k.page(k.element(nil, "noarn"), nil), nil
				}
				return twoPages(in)
			}

			total, _, err := invTestScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := invTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an ARN", k.typ, n)
			}
			want := slices.DeleteFunc(invAllTagARNs(), func(a string) bool { return a == k.arn("bare") })
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			w := warns()
			if got := afWarnOps(w); !slices.Equal(got, []string{"invoicing:" + k.op}) {
				t.Fatalf("warnings = %v; want one under invoicing:%s", w, k.op)
			}
			if x := w[0]; x.Scope != invTestScope || !strings.Contains(x.Message, "listed "+k.what+" have no ARN") ||
				!strings.HasSuffix(x.Message, "first "+k.key("noarn")) {
				t.Errorf("warning = %+v; want Scope %s, naming %s, keyed %q", x, invTestScope, k.what, k.key("noarn"))
			}
		})
	}
}

// Invoicing has no home in aws-us-gov or aws-cn, so scanInvoicing returns a
// *noRegionError there before building a client. scanInvoicingIn, handed a
// listing of non-commercial ARNs, still keys each row by its listed ARN and
// sends that ARN to ListTagsForResource unchanged.
func TestScanInvoicing_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, partition := range []string{"aws-us-gov", "aws-cn"} {
		t.Run(partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newInvTestCallsIn(partition)
			if _, _, err := invTestScanAs(context.Background(), partitionAccount(partition), st, invTestHandlers(calls)); err != nil {
				t.Fatalf("scan: %v", err)
			}
			var want []string
			for _, k := range invTestKinds() {
				want = append(want, k.arnIn(partition, testAccountID, "tagged"), k.arnIn(partition, testAccountID, "bare"))
			}
			slices.Sort(want)
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			for _, k := range invTestKinds() {
				arn := k.arnIn(partition, testAccountID, "tagged")
				assertTagsMap(t, rowsByNativeID(t, st, k.typ)[arn].TagsJSON, map[string]string{"type": k.typ})
			}
		})
	}
	for _, partition := range []string{"aws-us-gov", "aws-cn"} {
		t.Run("no home/"+partition, func(t *testing.T) {
			calls := newInvTestCalls()
			acct := partitionAccount(partition)
			acct.cfg = opStubConfig(invTestHandlers(calls), invTestDenied)
			total, inserted, err := scanInvoicing(context.Background(), acct, "", newTestStore(t), testScanID)
			var nr *noRegionError
			if !errors.As(err, &nr) || nr.Package != "invoicing" || nr.Partition != partition {
				t.Fatalf("err = %#v; want *noRegionError for invoicing in %s", err, partition)
			}
			if total != 0 || inserted != 0 {
				t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
			}
			for _, op := range []string{"ListInvoiceUnits", "ListProcurementPortalPreferences", "ListTagsForResource"} {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times; want 0", op, n)
				}
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing; the later phase is not run, and the rows
// written before the stop stay stored.
func TestScanInvoicing_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newInvTestCalls()
	handlers := invTestHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListInvoiceUnits"]
	handlers["ListInvoiceUnits"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := invTestScan(ctx, st, handlers)
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
	if n := calls.count("ListProcurementPortalPreferences"); n != 0 {
		t.Errorf("ListProcurementPortalPreferences called %d times after ctx ended", n)
	}
	units := invTestKindOf(t, "ListInvoiceUnits")
	if rows := rowsByNativeID(t, st, TypeInvoicingInvoiceUnit); len(rows) != 1 || !invStoredWithTags(st, units.arn("tagged")) {
		t.Errorf("invoice units = %v; want only the first page's, with its tags", rows)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and the later phase is not run.
func TestScanInvoicing_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "invoicing.us-east-1.api.aws", IsNotFound: true}
	t.Run("a listing after the other stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := invTestHandlers(newInvTestCalls())
		handlers["ListProcurementPortalPreferences"] = func(any) (any, error) { return nil, nx }
		total, _, err := invTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := invTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d invoice units counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newInvTestCalls()
		calls.tagErrs[invTestKindOf(t, "ListInvoiceUnits").arn("tagged")] = nx
		_, _, err := invTestScan(context.Background(), st, invTestHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newInvTestCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range invTestKinds() {
			handlers[k.op] = calls.failingFirst(k, nx)
		}
		_, _, err := invTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListProcurementPortalPreferences"); n != 0 {
			t.Errorf("ListProcurementPortalPreferences called %d times; want 0 once the region proved unavailable", n)
		}
	})
}

// On the preference listing's first page, every shape an unrouted operation
// takes is a silent feature gap: no preference row, no warning, and the
// invoice units are stored as usual. The invoice-unit listing has no such
// gap: on its first page the same shapes are returned, and the preferences
// are still stored. After a page has succeeded, the same shapes are returned
// with that page's row stored.
func TestScanInvoicing_PreferencesNotDeployed(t *testing.T) {
	t.Parallel()
	units, prefs := invTestKinds()[0], invTestKinds()[1]
	for _, gap := range []struct {
		name string
		err  error
	}{
		{"unknown operation", apiErr("UnknownOperationException", "")},
		{"unrouted gateway", apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")},
		{"http 404", respErrCode(404)},
	} {
		t.Run("first page/"+gap.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newInvTestCalls()
			handlers := invTestHandlers(calls)
			handlers[prefs.op] = calls.failingFirst(prefs, gap.err)

			total, _, err := invTestScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != invTestTotal-2 {
				t.Errorf("total = %d; want %d", total, invTestTotal-2)
			}
			if n := len(rowsByNativeID(t, st, prefs.typ)); n != 0 {
				t.Errorf("preference rows = %d; want 0", n)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
		})
		t.Run("units first page/"+gap.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := newInvTestCalls()
			handlers := invTestHandlers(calls)
			handlers[units.op] = calls.failingFirst(units, gap.err)

			total, _, err := invTestScan(context.Background(), st, handlers)
			if !errors.Is(err, gap.err) {
				t.Fatalf("err = %v; want %v", err, gap.err)
			}
			if total != invTestTotal-2 {
				t.Errorf("total = %d; want %d", total, invTestTotal-2)
			}
			if n := len(rowsByNativeID(t, st, units.typ)); n != 0 {
				t.Errorf("invoice unit rows = %d; want 0", n)
			}
			if n := len(rowsByNativeID(t, st, prefs.typ)); n != 2 {
				t.Errorf("preference rows = %d; want 2, its phase run", n)
			}
		})
		t.Run("later page/"+gap.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := newInvTestCalls()
			handlers := invTestHandlers(calls)
			handlers[prefs.op] = calls.failingLater(prefs, gap.err)

			total, _, err := invTestScan(context.Background(), st, handlers)
			if !errors.Is(err, gap.err) {
				t.Fatalf("err = %v; want %v", err, gap.err)
			}
			if total != invTestTotal-1 {
				t.Errorf("total = %d; want %d", total, invTestTotal-1)
			}
			if _, ok := rowsByNativeID(t, st, prefs.typ)[prefs.arn("tagged")]; !ok {
				t.Errorf("first page's %s missing", prefs.arn("tagged"))
			}
		})
	}
}

// A member account's payer-only refusal of a listing's first page reads as
// an empty listing for that phase only: the other phase still runs, and the
// scan reports not-entitled only when both phases were refused. When one
// phase lists, its rows are stored and counted and the scan returns nil
// without a warning, since the dispatcher discards a sentinel's counts.
func TestScanInvoicing_PayerOnlyPerPhase(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		refused         []string
		wantNotEntitled bool
		wantTotal       int
	}{
		{"units refused", []string{"ListInvoiceUnits"}, false, 2},
		{"preferences refused", []string{"ListProcurementPortalPreferences"}, false, 2},
		{"both refused", []string{"ListInvoiceUnits", "ListProcurementPortalPreferences"}, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newInvTestCalls()
			handlers := invTestHandlers(calls)
			for _, op := range tc.refused {
				handlers[op] = calls.failingFirst(invTestKindOf(t, op), invTestPayerOnly)
			}

			total, _, err := invTestScan(context.Background(), st, handlers)
			if tc.wantNotEntitled {
				if !errors.Is(err, errServiceNotEntitled) {
					t.Fatalf("err = %v; want errServiceNotEntitled", err)
				}
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d; want %d", total, tc.wantTotal)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, k := range invTestKinds() {
				want := 2
				if slices.Contains(tc.refused, k.op) {
					want = 0
				}
				if n := len(rowsByNativeID(t, st, k.typ)); n != want {
					t.Errorf("%s rows = %d; want %d", k.typ, n, want)
				}
				if n := calls.count(k.op); n == 0 {
					t.Errorf("%s not called", k.op)
				}
			}
		})
	}
}

// A payer-only refusal of a later page follows a page the account was
// served, so it is an error, the page before it stays stored, and the other
// phase runs. Beside a first-page refusal of the other phase the scan is not
// reported not-entitled.
func TestScanInvoicing_PayerOnlyLaterPageIsError(t *testing.T) {
	t.Parallel()
	for _, k := range invTestKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newInvTestCalls()
			handlers := invTestHandlers(calls)
			handlers[k.op] = calls.failingLater(k, invTestPayerOnly)
			for _, other := range invTestKinds() {
				if other.op != k.op {
					handlers[other.op] = calls.failingFirst(other, invTestPayerOnly)
				}
			}

			total, _, err := invTestScan(context.Background(), st, handlers)
			if errors.Is(err, errServiceNotEntitled) {
				t.Fatalf("err = %v; want no errServiceNotEntitled", err)
			}
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != 1 {
				t.Errorf("total = %d; want 1", total)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
				t.Errorf("first page's %s missing", k.arn("tagged"))
			}
		})
	}
}
