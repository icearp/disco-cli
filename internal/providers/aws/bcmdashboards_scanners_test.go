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
	"github.com/aws/aws-sdk-go-v2/service/bcmdashboards"
	bcmdtypes "github.com/aws/aws-sdk-go-v2/service/bcmdashboards/types"
	"github.com/icearp/disco-cli/store"
)

// bcmdTestTotal is every row of the fixture: two of each of the two types,
// one per page.
const bcmdTestTotal = 2 * 2

// bcmdTestOtherAccount owns the dashboard shared in by
// TestScanBCMDashboards_SharedDashboardKeptUntagged.
const bcmdTestOtherAccount = "444455556666"

var (
	bcmdTestDenied   = apiErr("AccessDeniedException", "not authorized")
	bcmdTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	bcmdTestBad      = apiErr("ValidationException", "bad")
	bcmdTestGone     = apiErr("ResourceNotFoundException", "gone")
	bcmdTestCreated  = time.Date(2025, 7, 28, 0, 0, 0, 0, time.UTC)
	bcmdTestUpdated  = time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
	bcmdTestScope    = testAccountID + "/us-east-1"
)

// bcmdTestKind is one listing: its op, the type it stores, its ARN's
// resource kind and the noun its warnings name its elements by.
type bcmdTestKind struct {
	op, typ, kind, what string
}

func bcmdTestKinds() []bcmdTestKind {
	return []bcmdTestKind{
		{"ListDashboards", TypeBCMDashboardsDashboard, "dashboard", "dashboards"},
		{"ListScheduledReports", TypeBCMDashboardsScheduledReport, "scheduled-report", "scheduled reports"},
	}
}

func bcmdTestKindOf(t *testing.T, op string) bcmdTestKind {
	t.Helper()
	for _, k := range bcmdTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no BCM Dashboards listing", op)
	return bcmdTestKind{}
}

func (k bcmdTestKind) name(suffix string) string { return "name-" + k.kind + "-" + suffix }

// arnIn is the ARN of k's element with suffix owned by account, in the
// Service Reference's ARNFormat: arn:{p}:bcm-dashboards::{acct}:{kind}/{id}.
func (k bcmdTestKind) arnIn(partition, account, suffix string) string {
	return "arn:" + partition + ":bcm-dashboards::" + account + ":" + k.kind + "/id-" + suffix
}

func (k bcmdTestKind) arn(suffix string) string { return k.arnIn("aws", testAccountID, suffix) }

// element is k's listed element with suffix, carrying arn (nil for an
// element listed without one).
func (k bcmdTestKind) element(arn *string, suffix string) any {
	name := sdkaws.String(k.name(suffix))
	if k.op == "ListDashboards" {
		return bcmdtypes.DashboardReference{
			Arn: arn, Name: name, Type: bcmdtypes.DashboardTypeCustom,
			CreatedAt: &bcmdTestCreated, UpdatedAt: &bcmdTestUpdated, Description: sdkaws.String("dashboard " + suffix),
		}
	}
	return bcmdtypes.ScheduledReportSummary{
		Arn: arn, Name: name, State: bcmdtypes.ScheduleStateEnabled,
		DashboardArn:       sdkaws.String(bcmdTestKinds()[0].arn(suffix)),
		ScheduleExpression: sdkaws.String("cron(0 8 1 * ? *)"), WidgetIds: []string{"w-" + suffix},
	}
}

// page is k's output holding element e, and next.
func (k bcmdTestKind) page(e any, next *string) any {
	if d, ok := e.(bcmdtypes.DashboardReference); ok {
		return &bcmdashboards.ListDashboardsOutput{NextToken: next, Dashboards: []bcmdtypes.DashboardReference{d}}
	}
	return &bcmdashboards.ListScheduledReportsOutput{NextToken: next, ScheduledReports: []bcmdtypes.ScheduledReportSummary{e.(bcmdtypes.ScheduledReportSummary)}}
}

// bcmdTestCalls records the calls of a scan and answers the tag reads, by
// ARN, with tags or tagErrs. partition and account are those of the ARNs the
// listings return.
type bcmdTestCalls struct {
	mu                 sync.Mutex
	partition, account string
	calls              map[string]int
	maxResults         map[string][]*int32
	tagARNs            []string
	tags               map[string][]bcmdtypes.ResourceTag
	tagErrs            map[string]error
}

// newBCMDTestCalls tags every "-tagged" row with its type.
func newBCMDTestCalls() *bcmdTestCalls {
	return newBCMDTestCallsIn("aws", testAccountID)
}

func newBCMDTestCallsIn(partition, account string) *bcmdTestCalls {
	c := &bcmdTestCalls{
		partition: partition, account: account,
		calls: map[string]int{}, maxResults: map[string][]*int32{},
		tags: map[string][]bcmdtypes.ResourceTag{}, tagErrs: map[string]error{},
	}
	for _, k := range bcmdTestKinds() {
		c.tags[k.arnIn(partition, account, "tagged")] = []bcmdtypes.ResourceTag{{Key: sdkaws.String("type"), Value: sdkaws.String(k.typ)}}
	}
	return c
}

// record notes op's call and the MaxResults a listing was sent.
func (c *bcmdTestCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if op != "ListTagsForResource" {
		c.maxResults[op] = append(c.maxResults[op], bacField[*int32](in, "MaxResults"))
	}
}

func (c *bcmdTestCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *bcmdTestCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *bcmdTestCalls) listTags(in any) (any, error) {
	arn := sv(in.(*bcmdashboards.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	return &bcmdashboards.ListTagsForResourceOutput{ResourceTags: c.tags[arn]}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *bcmdTestCalls) twoPages(k bcmdTestKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(k.element(sdkaws.String(k.arnIn(c.partition, c.account, suffix)), suffix), next), nil
	}
}

// bcmdTestHandlers answers every op the scan calls from c.
func bcmdTestHandlers(c *bcmdTestCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range bcmdTestKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// bcmdTestScanAs runs the scan for acct against handlers through the real
// client; every op they do not answer is denied.
func bcmdTestScanAs(ctx context.Context, acct *account, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	client := bcmdashboards.NewFromConfig(opStubConfig(handlers, bcmdTestDenied))
	return scanBCMDashboards(ctx, client, acct, st, testScanID)
}

func bcmdTestScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	return bcmdTestScanAs(ctx, newTestAccount(testAccountID), st, handlers)
}

// bcmdAllTagARNs is the ARN of every row of the fixture, sorted.
func bcmdAllTagARNs() []string {
	var arns []string
	for _, k := range bcmdTestKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

// bcmdStoredWithTags reports whether the row under arn is stored with tags.
// It does not fail the test, so a listing's handler can call it.
func bcmdStoredWithTags(st *store.Store, arn string) bool {
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
	return err == nil && r != nil && r.TagsJSON != nil
}

// Both listings store both their pages, each row under its listed ARN, in
// the scanned account's name, named by its listed name, in the global
// region, not managed, with its listed element as its attributes and the
// tags its tag read returns (nil for none). A dashboard has its listed
// CreatedAt and no Status; a scheduled report has its State as Status and no
// CreatedAt. ListTagsForResource is sent exactly the ARNs of every row;
// every listing call is sent MaxResults 100. No error, no warning.
func TestScanBCMDashboards_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBCMDTestCalls()

	total, inserted, err := bcmdTestScan(context.Background(), st, bcmdTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != bcmdTestTotal || inserted != bcmdTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, bcmdTestTotal, bcmdTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range bcmdTestKinds() {
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
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != "global" || r.ManagedByProvider || sv(r.AccountName) != newTestAccount(testAccountID).Name {
				t.Errorf("%s Name, Region, Managed, AccountName = %q, %q, %v, %q; want %q, global, false, the account's",
					arn, sv(r.Name), sv(r.Region), r.ManagedByProvider, sv(r.AccountName), k.name(suffix))
			}
			if k.op == "ListDashboards" {
				if want := "2025-07-28T00:00:00Z"; sv(r.CreatedAt) != want || r.Status != nil {
					t.Errorf("%s CreatedAt, Status = %q, %v; want %q, none", arn, sv(r.CreatedAt), r.Status, want)
				}
			} else if sv(r.Status) != "ENABLED" || r.CreatedAt != nil {
				t.Errorf("%s Status, CreatedAt = %q, %q; want ENABLED, none", arn, sv(r.Status), sv(r.CreatedAt))
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
		if got := calls.maxResults[k.op]; len(got) != 2 || got[0] == nil || *got[0] != 100 || got[1] == nil || *got[1] != 100 {
			t.Errorf("%s MaxResults = %v; want two calls sent 100", k.op, got)
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, bcmdAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, bcmdAllTagARNs())
	}
}

// Every listing writes its first page, with its tags, before it reads the
// second.
func TestScanBCMDashboards_PagesWrittenAsRead(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	handlers := bcmdTestHandlers(newBCMDTestCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range bcmdTestKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = bcmdStoredWithTags(st, k.arn("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := bcmdTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range bcmdTestKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.arn("tagged"))
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanBCMDashboards_RescanInsertsNone(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := bcmdTestScan(context.Background(), st, bcmdTestHandlers(newBCMDTestCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := bcmdTestScan(context.Background(), st, bcmdTestHandlers(newBCMDTestCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != bcmdTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, bcmdTestTotal)
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanBCMDashboards_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBCMDTestCalls()
	handlers := bcmdTestHandlers(calls)
	empty := map[string]any{
		"ListDashboards":       &bcmdashboards.ListDashboardsOutput{},
		"ListScheduledReports": &bcmdashboards.ListScheduledReportsOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	total, inserted, err := bcmdTestScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range bcmdTestKinds() {
		if n := calls.count(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource called %d times; want 0", n)
	}
}

// A failed tag read of a row of this account. ResourceNotFoundException is
// modeled, so a resource deleted since it was listed is dropped silently. A
// denial keeps the row untagged and warns once under the tag op with the
// home region's scope; a throttle keeps it and is transient; any other error
// keeps it and is an error.
func TestScanBCMDashboards_TagReadFailures(t *testing.T) {
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
	for _, k := range bcmdTestKinds() {
		cases = append(cases,
			tagFailure{k.kind + "/not found", k.op, bcmdTestGone, -1, "", bcmdTestTotal - 1, false, false},
			tagFailure{k.kind + "/denied", k.op, bcmdTestDenied, -1, "", bcmdTestTotal, true, true},
			tagFailure{k.kind + "/transient", k.op, bcmdTestThrottle, outcomeTransient, "ThrottlingException", bcmdTestTotal, true, false},
			tagFailure{k.kind + "/other error", k.op, bcmdTestBad, outcomeError, "ValidationException", bcmdTestTotal, true, false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBCMDTestCalls()
			k := bcmdTestKindOf(t, c.op)
			arn := k.arn("tagged")
			calls.tagErrs[arn] = c.err

			total, _, err := bcmdTestScan(context.Background(), st, bcmdTestHandlers(calls))
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
				want = []string{bcmdTagsOp}
			}
			if got := afWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != bcmdTestScope {
					t.Errorf("warning Scope %q; want %s", x.Scope, bcmdTestScope)
				}
			}
		})
	}
}

// A dashboard whose ARN names another account (shared in) is kept untagged
// without a warning when its tag read is denied or not found, and stored with
// its tags when the read succeeds; this account's own rows are read as
// usual.
func TestScanBCMDashboards_SharedDashboardKeptUntagged(t *testing.T) {
	t.Parallel()
	dashboards := bcmdTestKindOf(t, "ListDashboards")
	shared := dashboards.arnIn("aws", bcmdTestOtherAccount, "shared")
	for _, c := range []struct {
		name     string
		err      error
		wantTags map[string]string
	}{
		{"denied", bcmdTestDenied, nil},
		{"not found", bcmdTestGone, nil},
		{"read", nil, map[string]string{"owner": "other"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBCMDTestCalls()
			calls.tags[shared] = []bcmdtypes.ResourceTag{{Key: sdkaws.String("owner"), Value: sdkaws.String("other")}}
			if c.err != nil {
				calls.tagErrs[shared] = c.err
			}
			handlers := bcmdTestHandlers(calls)
			twoPages := handlers["ListDashboards"]
			handlers["ListDashboards"] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record("ListDashboards", in)
					return dashboards.page(dashboards.element(sdkaws.String(shared), "shared"), nil), nil
				}
				return twoPages(in)
			}

			total, _, err := bcmdTestScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != bcmdTestTotal {
				t.Errorf("total = %d; want %d", total, bcmdTestTotal)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			r, ok := rowsByNativeID(t, st, TypeBCMDashboardsDashboard)[shared]
			if !ok {
				t.Fatalf("%s missing", shared)
			}
			if c.wantTags == nil {
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
			} else {
				assertTagsMap(t, r.TagsJSON, c.wantTags)
			}
			if !bcmdStoredWithTags(st, dashboards.arn("tagged")) {
				t.Errorf("%s not stored with its tags", dashboards.arn("tagged"))
			}
		})
	}
}

// Denied tag reads in both phases send one warning for the whole scan; the
// rows are kept.
func TestScanBCMDashboards_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBCMDTestCalls()
	calls.tagErrs[bcmdTestKindOf(t, "ListDashboards").arn("bare")] = bcmdTestDenied
	calls.tagErrs[bcmdTestKindOf(t, "ListScheduledReports").arn("tagged")] = bcmdTestDenied

	total, _, err := bcmdTestScan(context.Background(), st, bcmdTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != bcmdTestTotal {
		t.Errorf("total = %d; want %d", total, bcmdTestTotal)
	}
	if got := afWarnOps(warns()); !slices.Equal(got, []string{bcmdTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, bcmdTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanBCMDashboards_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, bcmdTagsOp)
	calls := newBCMDTestCalls()
	calls.tagErrs[bcmdTestKindOf(t, "ListDashboards").arn("bare")] = bcmdTestThrottle
	calls.tagErrs[bcmdTestKindOf(t, "ListScheduledReports").arn("tagged")] = bcmdTestBad

	_, _, err := bcmdTestScan(context.Background(), st, bcmdTestHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", bcmdTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), and the scheduled-report phase does not run.
func TestScanBCMDashboards_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeBCMDashboardsDashboard)
	calls := newBCMDTestCalls()
	calls.tagErrs[bcmdTestKindOf(t, "ListDashboards").arn("tagged")] = bcmdTestDenied

	total, _, err := bcmdTestScan(context.Background(), st, bcmdTestHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListScheduledReports"); n != 0 {
		t.Errorf("ListScheduledReports called %d times; want 0 after the failed write", n)
	}
	if total != 0 {
		t.Errorf("total = %d; want 0", total)
	}
}

// A denied tag read is still warned about when a later phase fails: its row
// was stored, so the scan's error does not hide why it has no tags.
func TestScanBCMDashboards_TagDenialWarnedBesideLaterFailure(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		fail func(t *testing.T, st *store.Store, calls *bcmdTestCalls, handlers map[string]func(any) (any, error))
		want func(t *testing.T, err error)
	}{
		{"later listing page fails", func(t *testing.T, _ *store.Store, calls *bcmdTestCalls, handlers map[string]func(any) (any, error)) {
			list := handlers["ListScheduledReports"]
			handlers["ListScheduledReports"] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record("ListScheduledReports", in)
					return nil, bcmdTestBad
				}
				return list(in)
			}
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
		}},
		{"later tag read fails", func(t *testing.T, _ *store.Store, calls *bcmdTestCalls, _ map[string]func(any) (any, error)) {
			calls.tagErrs[bcmdTestKindOf(t, "ListScheduledReports").arn("tagged")] = bcmdTestBad
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
		}},
		{"later write fails", func(t *testing.T, st *store.Store, _ *bcmdTestCalls, _ map[string]func(any) (any, error)) {
			refuseStoreWrites(t, st, TypeBCMDashboardsScheduledReport)
		}, func(t *testing.T, err error) {
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBCMDTestCalls()
			denied := bcmdTestKindOf(t, "ListDashboards").arn("bare")
			calls.tagErrs[denied] = bcmdTestDenied
			handlers := bcmdTestHandlers(calls)
			c.fail(t, st, calls, handlers)

			_, _, err := bcmdTestScan(context.Background(), st, handlers)
			c.want(t, err)
			w := warns()
			if got := afWarnOps(w); !slices.Equal(got, []string{bcmdTagsOp}) {
				t.Fatalf("warnings = %v; want one under %s", w, bcmdTagsOp)
			}
			if x := w[0]; x.Scope != bcmdTestScope || !strings.Contains(x.Message, "first "+denied) {
				t.Errorf("warning = %+v; want Scope %s, keyed %s", x, bcmdTestScope, denied)
			}
			r, ok := rowsByNativeID(t, st, TypeBCMDashboardsDashboard)[denied]
			if !ok {
				t.Fatalf("%s missing", denied)
			}
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", denied, *r.TagsJSON)
			}
		})
	}
}

// A listing's failed later page keeps the page read before it, and its error
// does not stop the other phase. A listing's first page denied is warned
// about under its op, and the other phase runs too.
func TestScanBCMDashboards_ListFailureKeepsPagesAndOtherPhase(t *testing.T) {
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
	for _, k := range bcmdTestKinds() {
		cases = append(cases,
			listFailure{"later page fails/" + k.op, k.op, bcmdTestBad, true, 1, false},
			listFailure{"denied/" + k.op, k.op, bcmdTestDenied, false, 0, true})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBCMDTestCalls()
			handlers := bcmdTestHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if later := bacField[*string](in, "NextToken") != nil; later == c.laterPage {
					calls.record(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := bcmdTestScan(context.Background(), st, handlers)
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
				wantWarn = []string{"bcmdashboards:" + c.op}
			}
			if got := afWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := bcmdTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			for _, k := range bcmdTestKinds() {
				rows := rowsByNativeID(t, st, k.typ)
				if k.op != c.op {
					if len(rows) != 2 {
						t.Errorf("%s rows = %d; want 2, its phase run", k.typ, len(rows))
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
// the later phase runs.
func TestScanBCMDashboards_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newBCMDTestCalls()
	handlers := bcmdTestHandlers(calls)
	twoPages := handlers["ListDashboards"]
	handlers["ListDashboards"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*bcmdashboards.ListDashboardsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := bcmdTestScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != bcmdTestTotal {
		t.Errorf("total = %d; want %d", total, bcmdTestTotal)
	}
	if n := calls.count("ListDashboards"); n != 2 {
		t.Errorf("ListDashboards called %d times; want 2", n)
	}
	if n := len(rowsByNativeID(t, st, TypeBCMDashboardsScheduledReport)); n != 2 {
		t.Errorf("scheduled reports = %d; want 2", n)
	}
}

// An element listed without an ARN is not stored, not read, and warned about
// once under its listing with the home region's scope, calling the
// listing's elements by their noun and keyed by its name; the rest of the
// listing is stored.
func TestScanBCMDashboards_ElementWithoutARN(t *testing.T) {
	t.Parallel()
	for _, k := range bcmdTestKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBCMDTestCalls()
			handlers := bcmdTestHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record(k.op, in)
					return k.page(k.element(nil, "noarn"), nil), nil
				}
				return twoPages(in)
			}

			total, _, err := bcmdTestScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := bcmdTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an ARN", k.typ, n)
			}
			want := slices.DeleteFunc(bcmdAllTagARNs(), func(a string) bool { return a == k.arn("bare") })
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			w := warns()
			if got := afWarnOps(w); !slices.Equal(got, []string{"bcmdashboards:" + k.op}) {
				t.Fatalf("warnings = %v; want one under bcmdashboards:%s", w, k.op)
			}
			if x := w[0]; x.Scope != bcmdTestScope || !strings.Contains(x.Message, "listed "+k.what+" have no ARN") ||
				!strings.HasSuffix(x.Message, "first "+k.name("noarn")) {
				t.Errorf("warning = %+v; want Scope %s, naming %s, keyed %q", x, bcmdTestScope, k.what, k.name("noarn"))
			}
		})
	}
}

// In GovCloud and China the listed ARN of every row is its NativeID and
// reaches the tag op unchanged.
func TestScanBCMDashboards_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, partition := range []string{"aws-us-gov", "aws-cn"} {
		t.Run(partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newBCMDTestCallsIn(partition, testAccountID)
			if _, _, err := bcmdTestScanAs(context.Background(), partitionAccount(partition), st, bcmdTestHandlers(calls)); err != nil {
				t.Fatalf("scan: %v", err)
			}
			var want []string
			for _, k := range bcmdTestKinds() {
				want = append(want, k.arnIn(partition, testAccountID, "tagged"), k.arnIn(partition, testAccountID, "bare"))
			}
			slices.Sort(want)
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			for _, k := range bcmdTestKinds() {
				arn := k.arnIn(partition, testAccountID, "tagged")
				assertTagsMap(t, rowsByNativeID(t, st, k.typ)[arn].TagsJSON, map[string]string{"type": k.typ})
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing; the later phase is not run, and the rows
// written before the stop stay stored.
func TestScanBCMDashboards_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newBCMDTestCalls()
	handlers := bcmdTestHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListDashboards"]
	handlers["ListDashboards"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := bcmdTestScan(ctx, st, handlers)
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
	if n := calls.count("ListScheduledReports"); n != 0 {
		t.Errorf("ListScheduledReports called %d times after ctx ended", n)
	}
	dashboards := bcmdTestKindOf(t, "ListDashboards")
	if rows := rowsByNativeID(t, st, TypeBCMDashboardsDashboard); len(rows) != 1 || !bcmdStoredWithTags(st, dashboards.arn("tagged")) {
		t.Errorf("dashboards = %v; want only the first page's, with its tags", rows)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and the later phase is not run.
func TestScanBCMDashboards_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "bcm-dashboards.us-east-1.api.aws", IsNotFound: true}
	t.Run("a listing after the other stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := bcmdTestHandlers(newBCMDTestCalls())
		handlers["ListScheduledReports"] = func(any) (any, error) { return nil, nx }
		total, _, err := bcmdTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := bcmdTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d dashboards counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newBCMDTestCalls()
		calls.tagErrs[bcmdTestKindOf(t, "ListDashboards").arn("tagged")] = nx
		_, _, err := bcmdTestScan(context.Background(), st, bcmdTestHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newBCMDTestCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range bcmdTestKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		_, _, err := bcmdTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListScheduledReports"); n != 0 {
			t.Errorf("ListScheduledReports called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
