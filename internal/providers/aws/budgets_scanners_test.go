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
	"github.com/aws/aws-sdk-go-v2/service/budgets"
	budgetstypes "github.com/aws/aws-sdk-go-v2/service/budgets/types"
	"github.com/icearp/disco-cli/store"
)

// budgetsTestTotal is every row of the fixture: two budgets and two budget
// actions, one per page.
const budgetsTestTotal = 2 * 2

var (
	budgetsTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: budgets:ViewBudget")
	budgetsTestLinked   = apiErr("AccessDeniedException", "Your account is a linked account; ask the payer account to enable budgets")
	budgetsTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	budgetsTestBad      = apiErr("InvalidParameterException", "bad")
	budgetsTestGone     = apiErr("NotFoundException", "gone")
)

// The fixture's budget names hold a space, which the ARN keeps as listed.
var (
	budgetsTestNames     = map[string]string{"tagged": "team budget", "bare": "dev budget"}
	budgetsTestActionIDs = map[string]string{"tagged": "11111111-1111-4111-8111-111111111111", "bare": "22222222-2222-4222-8222-222222222222"}
)

// budgetsKind is one listing: its op, the type it stores, and its elements'
// ARNs in partition.
type budgetsKind struct{ op, typ string }

var (
	budgetsKindBudgets = budgetsKind{"DescribeBudgets", TypeBudgetsBudget}
	budgetsKindActions = budgetsKind{"DescribeBudgetActionsForAccount", TypeBudgetsBudgetsAction}
)

func budgetsKinds() []budgetsKind { return []budgetsKind{budgetsKindBudgets, budgetsKindActions} }

// arn is the ARN of k's element with suffix in partition: a budget's ARN is
// its name's, an action's its budget's (the budget with the same suffix)
// with /action/<id>.
func (k budgetsKind) arn(partition, suffix string) string {
	arn := "arn:" + partition + ":budgets::" + testAccountID + ":budget/" + budgetsTestNames[suffix]
	if k == budgetsKindActions {
		arn += "/action/" + budgetsTestActionIDs[suffix]
	}
	return arn
}

// name is the stored Name of k's element with suffix.
func (k budgetsKind) name(suffix string) string {
	if k == budgetsKindActions {
		return budgetsTestActionIDs[suffix]
	}
	return budgetsTestNames[suffix]
}

// page is k's output holding the element with suffix, and next.
func (k budgetsKind) page(suffix string, next *string) any {
	if k == budgetsKindActions {
		return &budgets.DescribeBudgetActionsForAccountOutput{NextToken: next, Actions: []budgetstypes.Action{{
			ActionId: sdkaws.String(budgetsTestActionIDs[suffix]), BudgetName: sdkaws.String(budgetsTestNames[suffix]),
			ActionType: budgetstypes.ActionTypeIam, Status: budgetstypes.ActionStatusStandby,
		}}}
	}
	return &budgets.DescribeBudgetsOutput{NextToken: next, Budgets: []budgetstypes.Budget{{
		BudgetName: sdkaws.String(budgetsTestNames[suffix]), BudgetType: budgetstypes.BudgetTypeCost, TimeUnit: budgetstypes.TimeUnitMonthly,
	}}}
}

// budgetsCalls records the calls of a scan and answers the tag reads, by
// ARN, with tags or errs.
type budgetsCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	accountIDs map[string][]string
	calls      map[string]int
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newBudgetsCalls tags every "-tagged" row in partition with its type.
func newBudgetsCalls(partition string) *budgetsCalls {
	c := &budgetsCalls{maxResults: map[string][]int32{}, accountIDs: map[string][]string{}, calls: map[string]int{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range budgetsKinds() {
		c.tags[k.arn(partition, "tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

// record notes op's call, its MaxResults and its AccountId.
func (c *budgetsCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.maxResults[op] = append(c.maxResults[op], *n)
	}
	if id := bacField[*string](in, "AccountId"); id != nil {
		c.accountIDs[op] = append(c.accountIDs[op], *id)
	}
}

func (c *budgetsCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *budgetsCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *budgetsCalls) listTags(in any) (any, error) {
	arn := sv(in.(*budgets.ListTagsForResourceInput).ResourceARN)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	var tags []budgetstypes.ResourceTag
	for key, value := range c.tags[arn] {
		tags = append(tags, budgetstypes.ResourceTag{Key: sdkaws.String(key), Value: sdkaws.String(value)})
	}
	return &budgets.ListTagsForResourceOutput{ResourceTags: tags}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *budgetsCalls) twoPages(k budgetsKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		if bacField[*string](in, "NextToken") != nil {
			return k.page("bare", nil), nil
		}
		return k.page("tagged", sdkaws.String("p2")), nil
	}
}

// budgetsHandlers answers every op the scan calls from c.
func budgetsHandlers(c *budgetsCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range budgetsKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// budgetsScanIn runs the registered scanner for acct against handlers; every
// op they do not answer fails with deny.
func budgetsScanIn(ctx context.Context, st *store.Store, acct *account, handlers map[string]func(in any) (any, error), deny error) (int, error) {
	acct.cfg = opStubConfig(handlers, deny)
	return scanBudgets(ctx, acct, "", st, testScanID)
}

// budgetsScan runs the scan for a commercial account against handlers;
// every op they do not answer is denied.
func budgetsScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	return budgetsScanIn(ctx, st, newTestAccount(testAccountID), handlers, budgetsTestDenied)
}

// budgetsAllTagARNs is the commercial ARN of every row of the fixture, sorted.
func budgetsAllTagARNs() []string {
	var arns []string
	for _, k := range budgetsKinds() {
		arns = append(arns, k.arn("aws", "tagged"), k.arn("aws", "bare"))
	}
	slices.Sort(arns)
	return arns
}

func budgetsWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Both listings store both their pages, each row under its built ARN (the
// budget name kept as listed, space included), named by the budget name or
// the action id, Region "global", Status the action's status (none for a
// budget), no CreatedAt, not managed, its listed element as attributes, and
// the tags its tag read returns (nil for none). ListTagsForResource is sent
// exactly the ARNs of every row; the listings are sent the account and
// their MaxResults caps. No error, no warning.
func TestScanBudgets_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBudgetsCalls("aws")

	counted, upserts := countUpserts(st)
	total, err := budgetsScan(context.Background(), counted, budgetsHandlers(calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != budgetsTestTotal || inserted != budgetsTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, budgetsTestTotal, budgetsTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, c := range []struct {
		k        budgetsKind
		status   string
		attrKey  string
		attrWant string
		pageCap  int32
	}{
		{budgetsKindBudgets, "", "BudgetType", "COST", 1000},
		{budgetsKindActions, "STANDBY", "ActionType", "APPLY_IAM_POLICY", 100},
	} {
		rows := rowsByNativeID(t, st, c.k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", c.k.typ, len(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := c.k.arn("aws", suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", c.k.typ, arn)
				continue
			}
			if sv(r.Name) != c.k.name(suffix) || sv(r.Region) != "global" || sv(r.Status) != c.status || (c.status == "") != (r.Status == nil) || r.CreatedAt != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, managed = %q, %q, %v, %v, %v; want %q, global, %q, nil, false",
					arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, c.k.name(suffix), c.status)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			if attrs["BudgetName"] != budgetsTestNames[suffix] || attrs[c.attrKey] != c.attrWant {
				t.Errorf("%s attrs = %s; want its element", arn, r.AttributesJSON)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": c.k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if got := calls.maxResults[c.k.op]; !slices.Equal(got, []int32{c.pageCap, c.pageCap}) {
			t.Errorf("%s MaxResults = %v; want %d on both pages", c.k.op, got, c.pageCap)
		}
		if got := calls.accountIDs[c.k.op]; !slices.Equal(got, []string{testAccountID, testAccountID}) {
			t.Errorf("%s AccountId = %v; want %s on both pages", c.k.op, got, testAccountID)
		}
	}
	want := []string{
		"arn:aws:budgets::123456789012:budget/dev budget",
		"arn:aws:budgets::123456789012:budget/dev budget/action/22222222-2222-4222-8222-222222222222",
		"arn:aws:budgets::123456789012:budget/team budget",
		"arn:aws:budgets::123456789012:budget/team budget/action/11111111-1111-4111-8111-111111111111",
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
}

// Every listing writes its first page, with its tags, before it reads the
// second.
func TestScanBudgets_PagesWrittenAsRead(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	handlers := budgetsHandlers(newBudgetsCalls("aws"))
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range budgetsKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = bcStoredWithTags(st, k.typ, k.arn("aws", "tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, err := budgetsScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range budgetsKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.arn("aws", "tagged"))
		}
	}
}

// The linked-account refusal of the first call reports the account not
// entitled: nothing stored, no warning, no other call.
func TestScanBudgets_LinkedAccountNotEntitled(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBudgetsCalls("aws")
	handlers := budgetsHandlers(calls)
	handlers["DescribeBudgets"] = func(in any) (any, error) {
		calls.record("DescribeBudgets", in)
		return nil, budgetsTestLinked
	}

	counted, upserts := countUpserts(st)
	total, err := budgetsScan(context.Background(), counted, handlers)
	inserted := upserts()
	assertGlueOutcome(t, err, outcomeNotEntitled)
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for op, want := range map[string]int{"DescribeBudgets": 1, "DescribeBudgetActionsForAccount": 0, "ListTagsForResource": 0} {
		if n := calls.count(op); n != want {
			t.Errorf("%s called %d times; want %d", op, n, want)
		}
	}
}

// The linked-account refusal of the actions listing's first page while
// nothing is stored reports the account not entitled. Budgets denied: its
// one warning is kept. Answered empty: no warning. Failed otherwise: its
// error is returned without the sentinel, and the refusal is warned about
// under DescribeBudgetActionsForAccount.
func TestScanBudgets_LinkedAccountBeforeAnyRow(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		budgets   func() (any, error)
		want      serviceOutcome
		wantWarns []string
	}{
		{"budgets denied", func() (any, error) { return nil, budgetsTestDenied }, outcomeNotEntitled, []string{budgetsDescribeBudgetsOp}},
		{"budgets empty", func() (any, error) { return &budgets.DescribeBudgetsOutput{}, nil }, outcomeNotEntitled, nil},
		{"budgets failed", func() (any, error) { return nil, budgetsTestBad }, outcomeError, []string{budgetsDescribeActionsOp}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBudgetsCalls("aws")
			handlers := budgetsHandlers(calls)
			handlers["DescribeBudgets"] = func(in any) (any, error) {
				calls.record("DescribeBudgets", in)
				return c.budgets()
			}
			handlers["DescribeBudgetActionsForAccount"] = func(in any) (any, error) {
				calls.record("DescribeBudgetActionsForAccount", in)
				return nil, budgetsTestLinked
			}

			counted, upserts := countUpserts(st)
			total, err := budgetsScan(context.Background(), counted, handlers)
			inserted := upserts()
			assertGlueOutcome(t, err, c.want)
			if got, want := errors.Is(err, errServiceNotEntitled), c.want == outcomeNotEntitled; got != want {
				t.Errorf("err = %v carries errServiceNotEntitled: %v; want %v", err, got, want)
			}
			if c.want == outcomeError {
				assertAPICode(t, err, "InvalidParameterException")
			}
			if total != 0 || inserted != 0 {
				t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
			}
			if got := budgetsWarnOps(warns()); !slices.Equal(got, c.wantWarns) {
				t.Errorf("warnings = %v; want %v", got, c.wantWarns)
			}
		})
	}
}

// The linked-account refusal after a call the service answered with rows is
// a denial: warned about under its listing, the rows already listed kept,
// and the actions still listed after a budgets refusal. On a rescan the
// budgets already stored count as rows though none is inserted.
func TestScanBudgets_LinkedAccountAfterRowsIsDenial(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		k         budgetsKind
		laterPage bool
		wantKept  int
		rescan    bool
	}{
		{"budgets, later page", budgetsKindBudgets, true, 1, false},
		{"actions, first page after budgets stored", budgetsKindActions, false, 0, false},
		{"actions, first page on a rescan after budgets stored", budgetsKindActions, false, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			if c.rescan {
				if _, err := budgetsScan(context.Background(), st, budgetsHandlers(newBudgetsCalls("aws"))); err != nil {
					t.Fatalf("first scan: %v", err)
				}
			}
			warns := rhWarnRecorder(st)
			calls := newBudgetsCalls("aws")
			handlers := budgetsHandlers(calls)
			list := handlers[c.k.op]
			handlers[c.k.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") != nil) == c.laterPage {
					calls.record(c.k.op, in)
					return nil, budgetsTestLinked
				}
				return list(in)
			}

			total, err := budgetsScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if want := budgetsTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if got, want := budgetsWarnOps(warns()), []string{"budgets:" + c.k.op}; !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
			if n := len(rowsByNativeID(t, st, TypeBudgetsBudgetsAction)); c.k == budgetsKindBudgets && n != 2 {
				t.Errorf("budget actions (the later phase) = %d; want 2", n)
			}
		})
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanBudgets_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBudgetsCalls("aws")
	handlers := budgetsHandlers(calls)
	for op, out := range map[string]any{
		"DescribeBudgets":                 &budgets.DescribeBudgetsOutput{},
		"DescribeBudgetActionsForAccount": &budgets.DescribeBudgetActionsForAccountOutput{},
	} {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	counted, upserts := countUpserts(st)
	total, err := budgetsScan(context.Background(), counted, handlers)
	inserted := upserts()
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for op, want := range map[string]int{"DescribeBudgets": 1, "DescribeBudgetActionsForAccount": 1, "ListTagsForResource": 0} {
		if n := calls.count(op); n != want {
			t.Errorf("%s called %d times; want %d", op, n, want)
		}
	}
}

// A failed tag read. NotFoundException is modeled, so a resource deleted
// since it was listed is dropped silently. A denial keeps the row untagged
// and warns once under the tag op with the scan's scope; a throttle keeps it
// and is transient; any other error keeps it and is an error.
func TestScanBudgets_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		k         budgetsKind
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"budget/not found", budgetsKindBudgets, budgetsTestGone, -1, "", budgetsTestTotal - 1, false, false},
		{"action/not found", budgetsKindActions, budgetsTestGone, -1, "", budgetsTestTotal - 1, false, false},
		{"budget/denied", budgetsKindBudgets, budgetsTestDenied, -1, "", budgetsTestTotal, true, true},
		{"action/transient", budgetsKindActions, budgetsTestThrottle, outcomeTransient, "ThrottlingException", budgetsTestTotal, true, false},
		{"budget/other error", budgetsKindBudgets, budgetsTestBad, outcomeError, "InvalidParameterException", budgetsTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBudgetsCalls("aws")
			arn := c.k.arn("aws", "tagged")
			calls.errs[arn] = c.err

			total, err := budgetsScan(context.Background(), st, budgetsHandlers(calls))
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
			rows := rowsByNativeID(t, st, c.k.typ)
			r, ok := rows[arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if _, ok := rows[c.k.arn("aws", "bare")]; !ok {
				t.Errorf("sibling %s missing", c.k.arn("aws", "bare"))
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{budgetsTagsOp}
			}
			if got := budgetsWarnOps(w); !slices.Equal(got, want) {
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

// Denied tag reads in both phases send one warning for the whole scan; the
// rows are kept.
func TestScanBudgets_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newBudgetsCalls("aws")
	calls.errs[budgetsKindBudgets.arn("aws", "bare")] = budgetsTestDenied
	calls.errs[budgetsKindActions.arn("aws", "tagged")] = budgetsTestDenied

	total, err := budgetsScan(context.Background(), st, budgetsHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != budgetsTestTotal {
		t.Errorf("total = %d; want %d", total, budgetsTestTotal)
	}
	if got := budgetsWarnOps(warns()); !slices.Equal(got, []string{budgetsTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, budgetsTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanBudgets_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, budgetsTagsOp)
	calls := newBudgetsCalls("aws")
	calls.errs[budgetsKindBudgets.arn("aws", "bare")] = budgetsTestThrottle
	calls.errs[budgetsKindActions.arn("aws", "tagged")] = budgetsTestBad

	_, err := budgetsScan(context.Background(), st, budgetsHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", budgetsTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a
// denied tag read among them), and the actions are not listed.
func TestScanBudgets_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeBudgetsBudget)
	calls := newBudgetsCalls("aws")
	calls.errs[budgetsKindBudgets.arn("aws", "tagged")] = budgetsTestDenied

	total, err := budgetsScan(context.Background(), st, budgetsHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("DescribeBudgetActionsForAccount"); n != 0 {
		t.Errorf("DescribeBudgetActionsForAccount called %d times; want 0 after the failed write", n)
	}
	if total != 0 {
		t.Errorf("total = %d; want 0", total)
	}
}

// A listing's failed later page keeps the page read before it, and the
// budgets listing's error does not stop the actions listing. A denied
// listing is warned about under its op instead, and the other listing is
// stored too.
func TestScanBudgets_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		k         budgetsKind
		err       error
		laterPage bool
		wantKept  int
	}{
		{"budgets, later page fails", budgetsKindBudgets, budgetsTestBad, true, 1},
		{"actions, later page fails", budgetsKindActions, budgetsTestBad, true, 1},
		{"budgets denied", budgetsKindBudgets, budgetsTestDenied, false, 0},
		{"actions denied", budgetsKindActions, budgetsTestDenied, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBudgetsCalls("aws")
			handlers := budgetsHandlers(calls)
			list := handlers[c.k.op]
			handlers[c.k.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") != nil) == c.laterPage {
					calls.record(c.k.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, err := budgetsScan(context.Background(), st, handlers)
			var wantWarn []string
			if isAccessDenied(c.err) {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				wantWarn = []string{"budgets:" + c.k.op}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "InvalidParameterException")
			}
			if got := budgetsWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := budgetsTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			rows := rowsByNativeID(t, st, c.k.typ)
			if _, ok := rows[c.k.arn("aws", "tagged")]; ok != (c.wantKept == 1) {
				t.Errorf("%s stored = %v; want %v", c.k.arn("aws", "tagged"), ok, c.wantKept == 1)
			}
			if _, ok := rows[c.k.arn("aws", "bare")]; ok {
				t.Errorf("%s stored; its page failed", c.k.arn("aws", "bare"))
			}
			other := budgetsKindActions
			if c.k == budgetsKindActions {
				other = budgetsKindBudgets
			}
			if n := len(rowsByNativeID(t, st, other.typ)); n != 2 {
				t.Errorf("%s rows = %d; want 2", other.typ, n)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanBudgets_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, k := range budgetsKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := budgetsHandlers(newBudgetsCalls("aws"))
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				out, err := twoPages(in)
				switch o := out.(type) {
				case *budgets.DescribeBudgetsOutput:
					o.NextToken = sdkaws.String("same")
				case *budgets.DescribeBudgetActionsForAccountOutput:
					o.NextToken = sdkaws.String("same")
				}
				return out, err
			}

			total, err := budgetsScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != budgetsTestTotal {
				t.Errorf("total = %d; want %d", total, budgetsTestTotal)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 2 {
				t.Errorf("%s rows = %d; want 2", k.typ, n)
			}
		})
	}
}

// An element whose ARN cannot be built (a budget without a name, an action
// without an id or budget name) is not stored, not sent to the tag op, and
// warned about once under its listing; the rest of the listing is stored.
func TestScanBudgets_ElementWithoutIdentifier(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		k       budgetsKind
		wantKey string
		drop    func(out any)
	}{
		{"budget without name", budgetsKindBudgets, noIdentifier, func(out any) { out.(*budgets.DescribeBudgetsOutput).Budgets[0].BudgetName = nil }},
		{"action without id", budgetsKindActions, budgetsTestNames["bare"], func(out any) {
			out.(*budgets.DescribeBudgetActionsForAccountOutput).Actions[0].ActionId = nil
		}},
		{"action without budget name", budgetsKindActions, budgetsTestActionIDs["bare"], func(out any) {
			out.(*budgets.DescribeBudgetActionsForAccountOutput).Actions[0].BudgetName = nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newBudgetsCalls("aws")
			handlers := budgetsHandlers(calls)
			twoPages := handlers[c.k.op]
			handlers[c.k.op] = func(in any) (any, error) {
				out, err := twoPages(in)
				if bacField[*string](in, "NextToken") != nil {
					c.drop(out)
				}
				return out, err
			}

			total, err := budgetsScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := budgetsTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, c.k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an identifier", c.k.typ, n)
			}
			want := slices.DeleteFunc(budgetsAllTagARNs(), func(a string) bool { return a == c.k.arn("aws", "bare") })
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			w := warns()
			if got := budgetsWarnOps(w); !slices.Equal(got, []string{"budgets:" + c.k.op}) {
				t.Fatalf("warnings = %v; want one under budgets:%s", got, c.k.op)
			}
			if w[0].Scope != testAccountID+"/"+testRegion || !strings.HasSuffix(w[0].Message, "first "+c.wantKey) {
				t.Errorf("warning scope, message = %q, %q; want %s/%s, naming first %q", w[0].Scope, w[0].Message, testAccountID, testRegion, c.wantKey)
			}
		})
	}
}

// A China account's rows are stored under aws-cn ARNs, which reach the tag
// op unchanged.
func TestScanBudgets_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newBudgetsCalls("aws-cn")
	acct := newTestAccount(testAccountID)
	acct.Partition = "aws-cn"

	if _, err := budgetsScanIn(context.Background(), st, acct, budgetsHandlers(calls), budgetsTestDenied); err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []string{
		"arn:aws-cn:budgets::123456789012:budget/dev budget",
		"arn:aws-cn:budgets::123456789012:budget/dev budget/action/22222222-2222-4222-8222-222222222222",
		"arn:aws-cn:budgets::123456789012:budget/team budget",
		"arn:aws-cn:budgets::123456789012:budget/team budget/action/11111111-1111-4111-8111-111111111111",
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
	for _, k := range budgetsKinds() {
		assertTagsMap(t, rowsByNativeID(t, st, k.typ)[k.arn("aws-cn", "tagged")].TagsJSON, map[string]string{"type": k.typ})
	}
}

// Once ctx ends, the stop is one member of the error, although the budgets
// phase saw it in its listing, and the actions are not listed.
func TestScanBudgets_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newBudgetsCalls("aws")
	handlers := budgetsHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["DescribeBudgets"]
	handlers["DescribeBudgets"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, err := budgetsScan(ctx, st, handlers)
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
	if n := calls.count("DescribeBudgetActionsForAccount"); n != 0 {
		t.Errorf("DescribeBudgetActionsForAccount called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and the actions are not listed.
func TestScanBudgets_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "budgets.amazonaws.com", IsNotFound: true}
	t.Run("actions after budgets stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := budgetsHandlers(newBudgetsCalls("aws"))
		handlers["DescribeBudgetActionsForAccount"] = func(any) (any, error) { return nil, nx }
		total, err := budgetsScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != 2 {
			t.Errorf("total = %d; want the 2 budgets counted", total)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newBudgetsCalls("aws")
		calls.errs[budgetsKindBudgets.arn("aws", "tagged")] = nx
		_, err := budgetsScan(context.Background(), st, budgetsHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newBudgetsCalls("aws")
		handlers := map[string]func(in any) (any, error){}
		for _, k := range budgetsKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		_, err := budgetsScanIn(context.Background(), st, newTestAccount(testAccountID), handlers, nx)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("DescribeBudgetActionsForAccount"); n != 0 {
			t.Errorf("DescribeBudgetActionsForAccount called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
