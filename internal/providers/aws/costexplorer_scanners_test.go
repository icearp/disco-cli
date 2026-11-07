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
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

const (
	// ceTestTotal is every row of the fixture: two of each of the three
	// types, one per page.
	ceTestTotal = 2 * 3
	// ceTestTagsOp is the label the tag reads' warnings and errors carry.
	ceTestTagsOp = "ce:ListTagsForResource"
)

var (
	ceTestDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: ce:Get")
	ceTestNotEnabled = apiErr("AccessDeniedException", "User not enabled for cost explorer access")
	ceTestNoCategory = apiErr("AccessDeniedException", "Linked account doesn't have access to cost category.")
	// Cost Explorer's throttle, the only one ListTagsForResource models.
	ceTestThrottle = apiErr("LimitExceededException", "You made too many calls in a short period of time. Try again later.")
	ceTestBad      = apiErr("ValidationException", "bad")
	ceTestGone     = apiErr("ResourceNotFoundException", "gone")
)

// ceKind is one listing: its op, the type it stores, its ARN's resource
// kind, and the input field that carries its page token.
type ceKind struct{ op, typ, kind, tokenField string }

func ceKinds() []ceKind {
	return []ceKind{
		{"GetAnomalyMonitors", TypeCEAnomalyMonitor, "anomalymonitor", "NextPageToken"},
		{"GetAnomalySubscriptions", TypeCEAnomalySubscription, "anomalysubscription", "NextPageToken"},
		{"ListCostCategoryDefinitions", TypeCECostCategory, "costcategory", "NextToken"},
	}
}

func ceKindOf(t *testing.T, op string) ceKind {
	t.Helper()
	for _, k := range ceKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Cost Explorer listing", op)
	return ceKind{}
}

func (k ceKind) ceARN(suffix string) string {
	return "arn:aws:ce::" + testAccountID + ":" + k.kind + "/" + k.kind + "-" + suffix
}

func (k ceKind) ceName(suffix string) string { return k.kind + "-" + suffix + "-name" }

// laterPage reports whether in, an input of k's op, asks for a page after
// the first.
func (k ceKind) laterPage(in any) bool { return bacField[*string](in, k.tokenField) != nil }

// cePage is k's output holding one element under arn (nil for an element
// listed without one), named name, and next.
func (k ceKind) cePage(arn, name *string, next *string) any {
	switch k.op {
	case "GetAnomalyMonitors":
		return &costexplorer.GetAnomalyMonitorsOutput{NextPageToken: next, AnomalyMonitors: []cetypes.AnomalyMonitor{
			{MonitorArn: arn, MonitorName: name, MonitorType: cetypes.MonitorTypeDimensional, MonitorDimension: cetypes.MonitorDimensionService},
		}}
	case "GetAnomalySubscriptions":
		return &costexplorer.GetAnomalySubscriptionsOutput{NextPageToken: next, AnomalySubscriptions: []cetypes.AnomalySubscription{
			{SubscriptionArn: arn, SubscriptionName: name, Frequency: cetypes.AnomalySubscriptionFrequencyDaily, AccountId: sdkaws.String(testAccountID)},
		}}
	default:
		return &costexplorer.ListCostCategoryDefinitionsOutput{NextToken: next, CostCategoryReferences: []cetypes.CostCategoryReference{
			{CostCategoryArn: arn, Name: name, NumberOfRules: 3},
		}}
	}
}

// attrs is the ARN and name fields of k's element and a third field a test
// pins in its stored attributes, with that field's value.
func (k ceKind) attrs() (arnKey, nameKey, key string, want any) {
	switch k.op {
	case "GetAnomalyMonitors":
		return "MonitorArn", "MonitorName", "MonitorDimension", "SERVICE"
	case "GetAnomalySubscriptions":
		return "SubscriptionArn", "SubscriptionName", "AccountId", testAccountID
	default:
		return "CostCategoryArn", "Name", "NumberOfRules", float64(3)
	}
}

// ceCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type ceCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	calls      map[string]int
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newCECalls tags every "-tagged" row with its type.
func newCECalls() *ceCalls {
	c := &ceCalls{maxResults: map[string][]int32{}, calls: map[string]int{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range ceKinds() {
		c.tags[k.ceARN("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

// record notes op's call and its MaxResults.
func (c *ceCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.maxResults[op] = append(c.maxResults[op], *n)
	}
}

func (c *ceCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *ceCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *ceCalls) listTags(in any) (any, error) {
	arn := sv(in.(*costexplorer.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	var tags []cetypes.ResourceTag
	for key, v := range c.tags[arn] {
		tags = append(tags, cetypes.ResourceTag{Key: sdkaws.String(key), Value: sdkaws.String(v)})
	}
	return &costexplorer.ListTagsForResourceOutput{ResourceTags: tags}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *ceCalls) twoPages(k ceKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if k.laterPage(in) {
			suffix, next = "bare", nil
		}
		arn, name := k.ceARN(suffix), k.ceName(suffix)
		return k.cePage(&arn, &name, next), nil
	}
}

// failing answers k's op with err on the first page when laterPage is false,
// else on the second, and as twoPages otherwise.
func (c *ceCalls) failing(k ceKind, laterPage bool, err error) func(any) (any, error) {
	pages := c.twoPages(k)
	return func(in any) (any, error) {
		if k.laterPage(in) == laterPage {
			c.record(k.op, in)
			return nil, err
		}
		return pages(in)
	}
}

// ceHandlers answers every op the scan calls from c.
func ceHandlers(c *ceCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range ceKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// ceScanIn runs the scan for acct in region against handlers; every op they
// do not answer fails with deny.
func ceScanIn(ctx context.Context, st *store.Store, acct *account, region string, handlers map[string]func(in any) (any, error), deny error) (int, int, error) {
	acct.cfg = opStubConfig(handlers, deny)
	client := costexplorer.NewFromConfig(acct.cfg, func(o *costexplorer.Options) { o.Region = region })
	return scanCostExplorerAll(ctx, client, acct, region, st, testScanID)
}

// ceScan runs the scan in the commercial home against handlers; every op
// they do not answer is denied.
func ceScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	return ceScanIn(ctx, st, newTestAccount(testAccountID), testRegion, handlers, ceTestDenied)
}

// ceAllTagARNs is the ARN of every row of the fixture, sorted.
func ceAllTagARNs() []string {
	var arns []string
	for _, k := range ceKinds() {
		arns = append(arns, k.ceARN("tagged"), k.ceARN("bare"))
	}
	slices.Sort(arns)
	return arns
}

func ceWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, each row under its listed ARN in the
// scanned account, named by its listed name, Region "global", no Status, no CreatedAt, not managed,
// its listed element as attributes, and the tags its tag read returns (nil
// for none). ListTagsForResource is sent exactly the ARNs of every row. Only
// ListCostCategoryDefinitions is sent MaxResults, 100 on each page. No error,
// no warning.
func TestScanCostExplorer_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCECalls()

	total, inserted, err := ceScan(context.Background(), st, ceHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != ceTestTotal || inserted != ceTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, ceTestTotal, ceTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range ceKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", k.typ, len(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.ceARN(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if r.AccountID != testAccountID || sv(r.AccountName) != "Test Account" {
				t.Errorf("%s AccountID, AccountName = %q, %q; want %q, Test Account", arn, r.AccountID, sv(r.AccountName), testAccountID)
			}
			if sv(r.Name) != k.ceName(suffix) || sv(r.Region) != "global" || r.Status != nil || r.CreatedAt != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, managed = %q, %q, %v, %v, %v; want %q, global, no Status or CreatedAt, false",
					arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, k.ceName(suffix))
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			arnKey, nameKey, key, want := k.attrs()
			if attrs[arnKey] != arn || attrs[nameKey] != k.ceName(suffix) || attrs[key] != want {
				t.Errorf("%s attrs = %s; want its element", arn, r.AttributesJSON)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	for op, want := range map[string][]int32{
		"GetAnomalyMonitors":          nil,
		"GetAnomalySubscriptions":     nil,
		"ListCostCategoryDefinitions": {100, 100},
	} {
		if got := calls.maxResults[op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", op, got, want)
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, ceAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, ceAllTagARNs())
	}
}

// ceStoredWithTags reports whether the row of typ under arn is stored with
// tags. It does not fail the test, so a listing's handler can call it.
func ceStoredWithTags(st *store.Store, typ, arn string) bool {
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
func TestScanCostExplorer_PagesWrittenAsRead(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	handlers := ceHandlers(newCECalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range ceKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if k.laterPage(in) {
				mu.Lock()
				storedFirst[k.op] = ceStoredWithTags(st, k.typ, k.ceARN("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := ceScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range ceKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.ceARN("tagged"))
		}
	}
}

// Cost Explorer's not-enabled refusal of a listing's first page reads that
// listing as empty, without a warning, and every later listing still runs.
// The scan reports the account disabled only when nothing was stored and no
// phase failed: rows stored by another listing keep their counts, and
// another listing's error is returned instead of the sentinel. A linked
// account refused only cost categories has Cost Explorer enabled, so its
// empty scan is not reported disabled.
func TestScanCostExplorer_NotEnabled(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		refused   []string // listings whose first page is refused
		others    map[string]error
		empty     bool // the listings not refused answer empty
		want      serviceOutcome
		wantTotal int
	}{
		{"every listing refused", []string{"GetAnomalyMonitors", "GetAnomalySubscriptions", "ListCostCategoryDefinitions"}, nil, false, outcomeDisabled, 0},
		{"cost categories refused, the others empty", []string{"ListCostCategoryDefinitions"}, nil, true, -1, 0},
		{"cost categories refused after rows were stored", []string{"ListCostCategoryDefinitions"}, nil, false, -1, 4},
		{"monitors refused before rows were stored", []string{"GetAnomalyMonitors"}, nil, false, -1, 4},
		{"refused beside another listing's error", []string{"GetAnomalyMonitors", "ListCostCategoryDefinitions"}, map[string]error{"GetAnomalySubscriptions": ceTestBad}, false, outcomeError, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newCECalls()
			handlers := ceHandlers(calls)
			emptyPages := map[string]any{
				"GetAnomalyMonitors":          &costexplorer.GetAnomalyMonitorsOutput{},
				"GetAnomalySubscriptions":     &costexplorer.GetAnomalySubscriptionsOutput{},
				"ListCostCategoryDefinitions": &costexplorer.ListCostCategoryDefinitionsOutput{},
			}
			wantCalls := map[string]int{}
			for _, k := range ceKinds() {
				wantCalls[k.op] = 1
				switch {
				case slices.Contains(c.refused, k.op):
					refusal := ceTestNotEnabled
					if k.op == "ListCostCategoryDefinitions" {
						refusal = ceTestNoCategory
					}
					handlers[k.op] = calls.failing(k, false, refusal)
				case c.others[k.op] != nil:
					handlers[k.op] = calls.failing(k, false, c.others[k.op])
				case c.empty:
					handlers[k.op] = func(in any) (any, error) {
						calls.record(k.op, in)
						return emptyPages[k.op], nil
					}
				default:
					wantCalls[k.op] = 2
				}
			}

			total, _, err := ceScan(context.Background(), st, handlers)
			if c.want < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.want)
			}
			if got, want := errors.Is(err, errServiceDisabled), c.want == outcomeDisabled; got != want {
				t.Errorf("err = %v carries errServiceDisabled: %v; want %v", err, got, want)
			}
			if c.want == outcomeError {
				assertAPICode(t, err, "ValidationException")
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for op, want := range wantCalls {
				if n := calls.count(op); n != want {
					t.Errorf("%s called %d times; want %d", op, n, want)
				}
			}
		})
	}
}

// The not-enabled refusal of a later page follows a page the service
// answered: it is warned about as a denial under its listing, the first
// page is kept, and the scan is not reported disabled.
func TestScanCostExplorer_NotEnabledLaterPageIsDenial(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCECalls()
	handlers := ceHandlers(calls)
	k := ceKindOf(t, "GetAnomalyMonitors")
	handlers[k.op] = calls.failing(k, true, ceTestNotEnabled)

	total, _, err := ceScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if want := ceTestTotal - 1; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	if got, want := ceWarnOps(warns()), []string{"ce:GetAnomalyMonitors"}; !slices.Equal(got, want) {
		t.Errorf("warnings = %v; want %v", got, want)
	}
	if _, ok := rowsByNativeID(t, st, k.typ)[k.ceARN("tagged")]; !ok {
		t.Errorf("%s missing; its page was answered", k.ceARN("tagged"))
	}
}

// A rescan whose anomaly monitors Cost Explorer refuses as not enabled keeps
// the counts of the rows the other listings stored again, though none of
// them is new: the scan is not reported disabled.
func TestScanCostExplorer_NotEnabledRescanKeepsCounts(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := ceScan(context.Background(), st, ceHandlers(newCECalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	calls := newCECalls()
	handlers := ceHandlers(calls)
	k := ceKindOf(t, "GetAnomalyMonitors")
	handlers[k.op] = calls.failing(k, false, ceTestNotEnabled)

	total, inserted, err := ceScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("rescan err = %v; want nil", err)
	}
	if want := ceTestTotal - 2; total != want || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, want)
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanCostExplorer_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCECalls()
	handlers := ceHandlers(calls)
	empty := map[string]any{
		"GetAnomalyMonitors":          &costexplorer.GetAnomalyMonitorsOutput{},
		"GetAnomalySubscriptions":     &costexplorer.GetAnomalySubscriptionsOutput{},
		"ListCostCategoryDefinitions": &costexplorer.ListCostCategoryDefinitionsOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	total, inserted, err := ceScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range ceKinds() {
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
// untagged and warns once under the tag op with the scan's scope; Cost
// Explorer's throttle, LimitExceededException, keeps it and is transient;
// any other error keeps it and is an error.
func TestScanCostExplorer_TagReadFailures(t *testing.T) {
	t.Parallel()
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
		{"anomaly monitor/not found", "GetAnomalyMonitors", ceTestGone, -1, "", ceTestTotal - 1, false, false},
		{"anomaly subscription/denied", "GetAnomalySubscriptions", ceTestDenied, -1, "", ceTestTotal, true, true},
		{"cost category/transient", "ListCostCategoryDefinitions", ceTestThrottle, outcomeTransient, "LimitExceededException", ceTestTotal, true, false},
		{"anomaly monitor/other error", "GetAnomalyMonitors", ceTestBad, outcomeError, "ValidationException", ceTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newCECalls()
			k := ceKindOf(t, c.op)
			arn := k.ceARN("tagged")
			calls.errs[arn] = c.err

			total, _, err := ceScan(context.Background(), st, ceHandlers(calls))
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
			if _, ok := rows[k.ceARN("bare")]; !ok {
				t.Errorf("sibling %s missing", k.ceARN("bare"))
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{ceTestTagsOp}
			}
			if got := ceWarnOps(w); !slices.Equal(got, want) {
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

// Denied tag reads in every phase send one warning for the whole scan; the
// rows are kept.
func TestScanCostExplorer_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCECalls()
	calls.errs[ceKindOf(t, "GetAnomalyMonitors").ceARN("bare")] = ceTestDenied
	calls.errs[ceKindOf(t, "GetAnomalySubscriptions").ceARN("tagged")] = ceTestDenied
	calls.errs[ceKindOf(t, "ListCostCategoryDefinitions").ceARN("bare")] = ceTestDenied

	total, _, err := ceScan(context.Background(), st, ceHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != ceTestTotal {
		t.Errorf("total = %d; want %d", total, ceTestTotal)
	}
	if got := ceWarnOps(warns()); !slices.Equal(got, []string{ceTestTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, ceTestTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanCostExplorer_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, ceTestTagsOp)
	calls := newCECalls()
	calls.errs[ceKindOf(t, "GetAnomalyMonitors").ceARN("bare")] = ceTestThrottle
	calls.errs[ceKindOf(t, "ListCostCategoryDefinitions").ceARN("tagged")] = ceTestBad

	_, _, err := ceScan(context.Background(), st, ceHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", ceTestTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanCostExplorer_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeCEAnomalySubscription)
	calls := newCECalls()
	calls.errs[ceKindOf(t, "GetAnomalySubscriptions").ceARN("tagged")] = ceTestDenied

	total, _, err := ceScan(context.Background(), st, ceHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListCostCategoryDefinitions"); n != 0 {
		t.Errorf("ListCostCategoryDefinitions called %d times; want 0 after the failed write", n)
	}
	if total != 2 {
		t.Errorf("total = %d; want the 2 anomaly monitors", total)
	}
	if n := len(rowsByNativeID(t, st, TypeCEAnomalyMonitor)); n != 2 {
		t.Errorf("anomaly monitors = %d; want 2", n)
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the later phases. Cost Explorer's throttle,
// LimitExceededException, on any listing's page is transient; any other
// error is an error. A denied listing is warned about under its op instead,
// and the later phases run too.
func TestScanCostExplorer_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		op        string
		err       error
		laterPage bool
		wantKept  int
		outcome   serviceOutcome // -1: no error
		code      string
	}{
		{"first phase, first page fails", "GetAnomalyMonitors", ceTestBad, false, 0, outcomeError, "ValidationException"},
		{"first phase, later page fails", "GetAnomalyMonitors", ceTestBad, true, 1, outcomeError, "ValidationException"},
		{"later page fails mid-scan", "GetAnomalySubscriptions", ceTestBad, true, 1, outcomeError, "ValidationException"},
		{"anomaly monitors, first page throttled", "GetAnomalyMonitors", ceTestThrottle, false, 0, outcomeTransient, "LimitExceededException"},
		{"anomaly subscriptions, later page throttled", "GetAnomalySubscriptions", ceTestThrottle, true, 1, outcomeTransient, "LimitExceededException"},
		{"cost categories, first page throttled", "ListCostCategoryDefinitions", ceTestThrottle, false, 0, outcomeTransient, "LimitExceededException"},
		{"cost categories, later page throttled", "ListCostCategoryDefinitions", ceTestThrottle, true, 1, outcomeTransient, "LimitExceededException"},
		{"denied", "GetAnomalySubscriptions", ceTestDenied, false, 0, -1, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newCECalls()
			handlers := ceHandlers(calls)
			k := ceKindOf(t, c.op)
			handlers[c.op] = calls.failing(k, c.laterPage, c.err)

			total, _, err := ceScan(context.Background(), st, handlers)
			var wantWarn []string
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				wantWarn = []string{"ce:" + c.op}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if got := ceWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := ceTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			rows := rowsByNativeID(t, st, k.typ)
			if _, ok := rows[k.ceARN("tagged")]; ok != (c.wantKept == 1) {
				t.Errorf("%s stored = %v; want %v", k.ceARN("tagged"), ok, c.wantKept == 1)
			}
			if _, ok := rows[k.ceARN("bare")]; ok {
				t.Errorf("%s stored; its page failed", k.ceARN("bare"))
			}
			for _, other := range ceKinds() {
				if other.op == c.op {
					continue
				}
				if n := len(rowsByNativeID(t, st, other.typ)); n != 2 {
					t.Errorf("%s rows = %d; want 2", other.typ, n)
				}
			}
		})
	}
}

// A repeated page token ends each listing as an error, keeping its pages.
func TestScanCostExplorer_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, k := range ceKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newCECalls()
			handlers := ceHandlers(calls)
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				arn, name := k.ceARN("tagged"), k.ceName("tagged")
				if k.laterPage(in) {
					arn, name = k.ceARN("bare"), k.ceName("bare")
				}
				return k.cePage(&arn, &name, sdkaws.String("same")), nil
			}

			total, _, err := ceScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != ceTestTotal {
				t.Errorf("total = %d; want %d", total, ceTestTotal)
			}
			if n := calls.count(k.op); n != 2 {
				t.Errorf("%s called %d times; want 2", k.op, n)
			}
		})
	}
}

// An element listed without an ARN is not stored, not sent to the tag op,
// and warned about once under its listing, by its listed name; the rest of the listing is
// stored. An element without a name is stored with its tags read.
func TestScanCostExplorer_ElementWithoutARN(t *testing.T) {
	t.Parallel()
	for _, k := range ceKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newCECalls()
			handlers := ceHandlers(calls)
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				if k.laterPage(in) {
					return k.cePage(nil, sdkaws.String("noarn"), nil), nil
				}
				arn := k.ceARN("tagged")
				return k.cePage(&arn, nil, sdkaws.String("p2")), nil
			}

			total, _, err := ceScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := ceTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			rows := rowsByNativeID(t, st, k.typ)
			if len(rows) != 1 {
				t.Errorf("%s rows = %d; want the 1 with an ARN", k.typ, len(rows))
			}
			r := rows[k.ceARN("tagged")]
			if r.Name != nil {
				t.Errorf("%s Name = %q; want nil, as listed", k.ceARN("tagged"), *r.Name)
			}
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			want := slices.DeleteFunc(ceAllTagARNs(), func(a string) bool { return a == k.ceARN("bare") })
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			w := warns()
			if got := ceWarnOps(w); !slices.Equal(got, []string{"ce:" + k.op}) {
				t.Fatalf("warnings = %v; want one under ce:%s", got, k.op)
			}
			if !strings.Contains(w[0].Message, "noarn") {
				t.Errorf("warning %q does not name the element's listed name noarn", w[0].Message)
			}
		})
	}
}

// A China ARN (the op's ResourceArn pattern admits every aws partition)
// reaches the tag op unchanged and is the row's NativeID.
func TestScanCostExplorer_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newCECalls()
	handlers := ceHandlers(calls)
	var want []string
	for _, k := range ceKinds() {
		arn := "arn:aws-cn:ce::" + testAccountID + ":" + k.kind + "/cn-1"
		want = append(want, arn)
		calls.tags[arn] = map[string]string{"k": k.kind}
		handlers[k.op] = func(in any) (any, error) {
			calls.record(k.op, in)
			return k.cePage(&arn, sdkaws.String("cn"), nil), nil
		}
	}
	slices.Sort(want)
	acct := newTestAccount(testAccountID)
	acct.Partition = "aws-cn"

	if _, _, err := ceScanIn(context.Background(), st, acct, "cn-northwest-1", handlers, ceTestDenied); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
	for _, k := range ceKinds() {
		arn := "arn:aws-cn:ce::" + testAccountID + ":" + k.kind + "/cn-1"
		assertTagsMap(t, rowsByNativeID(t, st, k.typ)[arn].TagsJSON, map[string]string{"k": k.kind})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanCostExplorer_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newCECalls()
	handlers := ceHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := ceKindOf(t, "GetAnomalySubscriptions")
	list := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if k.laterPage(in) {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := ceScan(ctx, st, handlers)
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
	if n := calls.count("ListCostCategoryDefinitions"); n != 0 {
		t.Errorf("ListCostCategoryDefinitions called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanCostExplorer_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "ce.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := ceHandlers(newCECalls())
		handlers["ListCostCategoryDefinitions"] = func(any) (any, error) { return nil, nx }
		total, _, err := ceScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := ceTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCECalls()
		calls.errs[ceKindOf(t, "GetAnomalyMonitors").ceARN("tagged")] = nx
		_, _, err := ceScan(context.Background(), st, ceHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newCECalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range ceKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		_, _, err := ceScanIn(context.Background(), st, newTestAccount(testAccountID), testRegion, handlers, nx)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("GetAnomalySubscriptions"); n != 0 {
			t.Errorf("GetAnomalySubscriptions called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
