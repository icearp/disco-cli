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
	"github.com/aws/aws-sdk-go-v2/service/tnb"
	tnbtypes "github.com/aws/aws-sdk-go-v2/service/tnb/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// tnbTestTotal is every row of the fixture: two of each of the five types,
// one per page.
const tnbTestTotal = 2 * 5

var (
	tnbTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: tnb:List")
	tnbTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	tnbTestBad      = apiErr("ValidationException", "bad")
	tnbTestGone     = apiErr("ResourceNotFoundException", "gone")
)

// tnbKind is one listing: its op, the type it stores, its ARN's resource
// kind, the prefix of its elements' ids, the prefix of the name a row takes
// when that is not its id ("" when the row is named by its id), its
// elements' state, and the attribute naming the parent a resolver reads ("" for
// none).
type tnbKind struct {
	op, typ, kind, idPrefix, namePrefix, status, refField string
}

func tnbKinds() []tnbKind {
	return []tnbKind{
		{"ListSolFunctionPackages", TypeTnbFunctionPackage, "function-package", "fp", "", "ONBOARDED", ""},
		{"ListSolFunctionInstances", TypeTnbFunctionInstance, "function-instance", "fi", "", "INSTANTIATED", "VnfPkgId"},
		{"ListSolNetworkPackages", TypeTnbNetworkPackage, "network-package", "np", "nsd", "ONBOARDED", ""},
		{"ListSolNetworkInstances", TypeTnbNetworkInstance, "network-instance", "ni", "nsi", "INSTANTIATED", "NsdInfoId"},
		{"ListSolNetworkOperations", TypeTnbNetworkOperation, "network-operation", "no", "", "COMPLETED", "NsInstanceId"},
	}
}

func tnbKindOf(t *testing.T, op string) tnbKind {
	t.Helper()
	for _, k := range tnbKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no TNB listing", op)
	return tnbKind{}
}

func (k tnbKind) id(suffix string) string { return k.idPrefix + "-" + suffix }

func (k tnbKind) arn(suffix string) string {
	return "arn:aws:tnb:" + testRegion + ":" + testAccountID + ":" + k.kind + "/" + k.id(suffix)
}

// name is the Name of k's row for the element with suffix.
func (k tnbKind) name(suffix string) string {
	if k.namePrefix == "" {
		return k.id(suffix)
	}
	return k.namePrefix + "-" + suffix
}

// ref is the parent id k's element with suffix names in k.refField.
func (k tnbKind) ref(suffix string) string { return "parent-" + suffix }

// page is k's output holding the element with suffix under arn (nil for an
// element listed without one), and next.
func (k tnbKind) page(arn *string, suffix string, next *string) any {
	id, name, ref := sdkaws.String(k.id(suffix)), sdkaws.String(k.name(suffix)), sdkaws.String(k.ref(suffix))
	switch k.op {
	case "ListSolFunctionPackages":
		return &tnb.ListSolFunctionPackagesOutput{NextToken: next, FunctionPackages: []tnbtypes.ListSolFunctionPackageInfo{
			{Arn: arn, Id: id, OnboardingState: tnbtypes.OnboardingState(k.status), OperationalState: tnbtypes.OperationalStateEnabled},
		}}
	case "ListSolFunctionInstances":
		return &tnb.ListSolFunctionInstancesOutput{NextToken: next, FunctionInstances: []tnbtypes.ListSolFunctionInstanceInfo{
			{Arn: arn, Id: id, InstantiationState: tnbtypes.VnfInstantiationState(k.status), VnfPkgId: ref, NsInstanceId: sdkaws.String("ni-x")},
		}}
	case "ListSolNetworkPackages":
		return &tnb.ListSolNetworkPackagesOutput{NextToken: next, NetworkPackages: []tnbtypes.ListSolNetworkPackageInfo{
			{Arn: arn, Id: id, NsdName: name, NsdOnboardingState: tnbtypes.NsdOnboardingState(k.status)},
		}}
	case "ListSolNetworkInstances":
		return &tnb.ListSolNetworkInstancesOutput{NextToken: next, NetworkInstances: []tnbtypes.ListSolNetworkInstanceInfo{
			{Arn: arn, Id: id, NsInstanceName: name, NsState: tnbtypes.NsState(k.status), NsdInfoId: ref},
		}}
	default:
		return &tnb.ListSolNetworkOperationsOutput{NextToken: next, NetworkOperations: []tnbtypes.ListSolNetworkOperationsInfo{
			{Arn: arn, Id: id, OperationState: tnbtypes.NsLcmOperationState(k.status), NsInstanceId: ref, LcmOperationType: tnbtypes.LcmOperationTypeInstantiate},
		}}
	}
}

// tnbCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type tnbCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	calls      map[string]int
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newTNBCalls tags every "-tagged" row with its type.
func newTNBCalls() *tnbCalls {
	c := &tnbCalls{maxResults: map[string][]int32{}, calls: map[string]int{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range tnbKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

// record notes op's call and its MaxResults.
func (c *tnbCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.maxResults[op] = append(c.maxResults[op], *n)
	}
}

func (c *tnbCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *tnbCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *tnbCalls) listTags(in any) (any, error) {
	arn := sv(in.(*tnb.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &tnb.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *tnbCalls) twoPages(k tnbKind) func(any) (any, error) {
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

// tnbHandlers answers every op the scan calls from c.
func tnbHandlers(c *tnbCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range tnbKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// tnbScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func tnbScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, tnbTestDenied)
	return scanTnb(ctx, acct, testRegion, st, testScanID)
}

// tnbAllTagARNs is the ARN of every row of the fixture, sorted.
func tnbAllTagARNs() []string {
	var arns []string
	for _, k := range tnbKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

func tnbWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, each row under its listed ARN, named
// by its id (a network package by its NsdName, a network instance by its
// NsInstanceName), with its state as Status, no CreatedAt, its listed
// element as attributes (the parent id its resolver reads included), and the
// tags its tag read returns (nil for none). ListTagsForResource is sent
// exactly the ARNs of every row; every listing is sent MaxResults 100. No
// error, no warning.
func TestScanTnb_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newTNBCalls()

	total, inserted, err := tnbScan(context.Background(), st, tnbHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != tnbTestTotal || inserted != tnbTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, tnbTestTotal, tnbTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range tnbKinds() {
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
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion || sv(r.Status) != k.status || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %q, %v; want %q, %q, %q, nil", arn, sv(r.Name), sv(r.Region), sv(r.Status), r.CreatedAt, k.name(suffix), testRegion, k.status)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			if attrs["Arn"] != arn || attrs["Id"] != k.id(suffix) {
				t.Errorf("%s attrs = %s; want its element", arn, r.AttributesJSON)
			}
			if k.refField != "" && attrs[k.refField] != k.ref(suffix) {
				t.Errorf("%s attrs %s = %v; want %s", arn, k.refField, attrs[k.refField], k.ref(suffix))
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
	if got := calls.sortedTagARNs(); !slices.Equal(got, tnbAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, tnbAllTagARNs())
	}
}

// tnbStoredWithTags reports whether the row of typ under arn is stored with
// tags. It does not fail the test, so a listing's handler can call it.
func tnbStoredWithTags(st *store.Store, typ, arn string) bool {
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
func TestScanTnb_PagesWrittenAsRead(t *testing.T) {
	st := newTestStore(t)
	handlers := tnbHandlers(newTNBCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range tnbKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = tnbStoredWithTags(st, k.typ, k.arn("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := tnbScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range tnbKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.arn("tagged"))
		}
	}
}

// An element listed with an empty state is stored with Status "", not nil.
func TestScanTnb_EmptyStateKeptAsStatus(t *testing.T) {
	st := newTestStore(t)
	calls := newTNBCalls()
	handlers := tnbHandlers(calls)
	for _, k := range tnbKinds() {
		k.status = ""
		handlers[k.op] = func(in any) (any, error) {
			calls.record(k.op, in)
			arn := k.arn("nostate")
			return k.page(&arn, "nostate", nil), nil
		}
	}

	if _, _, err := tnbScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range tnbKinds() {
		r, ok := rowsByNativeID(t, st, k.typ)[k.arn("nostate")]
		if !ok {
			t.Errorf("%s missing", k.arn("nostate"))
			continue
		}
		if r.Status == nil || *r.Status != "" {
			t.Errorf("%s Status = %v; want a pointer to \"\"", k.arn("nostate"), r.Status)
		}
	}
}

// AWS refusing every TNB op as deprecated is the service blocked: no
// warning, nothing stored, and an error dispatch classifies as blocked
// rather than as a failure of the scan.
func TestScanTnb_ServiceBlocked(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(nil, respErrMsg(403, "AWS Telco Network Builder is deprecated. All API operations are blocked."))

	total, inserted, err := scanTnb(context.Background(), acct, testRegion, st, testScanID)
	assertGlueOutcome(t, err, outcomeBlocked)
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanTnb_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := tnbScan(context.Background(), st, tnbHandlers(newTNBCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := tnbScan(context.Background(), st, tnbHandlers(newTNBCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != tnbTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, tnbTestTotal)
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanTnb_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newTNBCalls()
	handlers := tnbHandlers(calls)
	empty := map[string]any{
		"ListSolFunctionPackages":  &tnb.ListSolFunctionPackagesOutput{},
		"ListSolFunctionInstances": &tnb.ListSolFunctionInstancesOutput{},
		"ListSolNetworkPackages":   &tnb.ListSolNetworkPackagesOutput{},
		"ListSolNetworkInstances":  &tnb.ListSolNetworkInstancesOutput{},
		"ListSolNetworkOperations": &tnb.ListSolNetworkOperationsOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	total, inserted, err := tnbScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range tnbKinds() {
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
func TestScanTnb_TagReadFailures(t *testing.T) {
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
		{"network operation/not found", "ListSolNetworkOperations", tnbTestGone, -1, "", tnbTestTotal - 1, false, false},
		{"function instance/denied", "ListSolFunctionInstances", tnbTestDenied, -1, "", tnbTestTotal, true, true},
		{"network package/transient", "ListSolNetworkPackages", tnbTestThrottle, outcomeTransient, "ThrottlingException", tnbTestTotal, true, false},
		{"function package/other error", "ListSolFunctionPackages", tnbTestBad, outcomeError, "ValidationException", tnbTestTotal, true, false},
		{"network instance/not found", "ListSolNetworkInstances", tnbTestGone, -1, "", tnbTestTotal - 1, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newTNBCalls()
			k := tnbKindOf(t, c.op)
			arn := k.arn("tagged")
			calls.errs[arn] = c.err

			total, _, err := tnbScan(context.Background(), st, tnbHandlers(calls))
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
				want = []string{tnbTagsOp}
			}
			if got := tnbWarnOps(w); !slices.Equal(got, want) {
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
func TestScanTnb_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newTNBCalls()
	calls.errs[tnbKindOf(t, "ListSolFunctionPackages").arn("bare")] = tnbTestDenied
	calls.errs[tnbKindOf(t, "ListSolNetworkInstances").arn("tagged")] = tnbTestDenied
	calls.errs[tnbKindOf(t, "ListSolNetworkOperations").arn("bare")] = tnbTestDenied

	total, _, err := tnbScan(context.Background(), st, tnbHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != tnbTestTotal {
		t.Errorf("total = %d; want %d", total, tnbTestTotal)
	}
	if got := tnbWarnOps(warns()); !slices.Equal(got, []string{tnbTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, tnbTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanTnb_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, tnbTagsOp)
	calls := newTNBCalls()
	calls.errs[tnbKindOf(t, "ListSolFunctionInstances").arn("bare")] = tnbTestThrottle
	calls.errs[tnbKindOf(t, "ListSolNetworkOperations").arn("tagged")] = tnbTestBad

	_, _, err := tnbScan(context.Background(), st, tnbHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", tnbTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanTnb_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeTnbNetworkPackage)
	calls := newTNBCalls()
	calls.errs[tnbKindOf(t, "ListSolNetworkPackages").arn("tagged")] = tnbTestDenied

	total, _, err := tnbScan(context.Background(), st, tnbHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListSolNetworkInstances", "ListSolNetworkOperations"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if total != 4 {
		t.Errorf("total = %d; want the 4 function packages and instances", total)
	}
	for _, typ := range []string{TypeTnbFunctionPackage, TypeTnbFunctionInstance} {
		if n := len(rowsByNativeID(t, st, typ)); n != 2 {
			t.Errorf("%s rows = %d; want 2", typ, n)
		}
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the later phases. A denied listing is warned about
// under its op instead, and the later phases run too.
func TestScanTnb_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		name      string
		op        string
		err       error
		laterPage bool
		wantKept  int
	}{
		{"later page fails", "ListSolFunctionPackages", tnbTestBad, true, 1},
		{"later page fails mid-scan", "ListSolNetworkInstances", tnbTestBad, true, 1},
		{"denied", "ListSolFunctionInstances", tnbTestDenied, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newTNBCalls()
			handlers := tnbHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") != nil) == c.laterPage {
					calls.record(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := tnbScan(context.Background(), st, handlers)
			var wantWarn []string
			if isAccessDenied(c.err) {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				wantWarn = []string{"tnb:" + c.op}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ValidationException")
			}
			if got := tnbWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := tnbTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			k := tnbKindOf(t, c.op)
			rows := rowsByNativeID(t, st, k.typ)
			if _, ok := rows[k.arn("tagged")]; ok != (c.wantKept == 1) {
				t.Errorf("%s stored = %v; want %v", k.arn("tagged"), ok, c.wantKept == 1)
			}
			if _, ok := rows[k.arn("bare")]; ok {
				t.Errorf("%s stored; its page failed", k.arn("bare"))
			}
			if n := len(rowsByNativeID(t, st, TypeTnbNetworkOperation)); n != 2 {
				t.Errorf("network operations (the last phase) = %d; want 2", n)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanTnb_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newTNBCalls()
	handlers := tnbHandlers(calls)
	twoPages := handlers["ListSolNetworkPackages"]
	handlers["ListSolNetworkPackages"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*tnb.ListSolNetworkPackagesOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := tnbScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != tnbTestTotal {
		t.Errorf("total = %d; want %d", total, tnbTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeTnbNetworkPackage)); n != 2 {
		t.Errorf("network packages = %d; want 2", n)
	}
}

// An element listed without an ARN is not stored, not sent to the tag op,
// and warned about once under its listing; the rest of the listing is
// stored.
func TestScanTnb_ElementWithoutARN(t *testing.T) {
	for _, k := range tnbKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newTNBCalls()
			handlers := tnbHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record(k.op, in)
					return k.page(nil, "noarn", nil), nil
				}
				return twoPages(in)
			}

			total, _, err := tnbScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := tnbTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an ARN", k.typ, n)
			}
			want := slices.DeleteFunc(tnbAllTagARNs(), func(a string) bool { return a == k.arn("bare") })
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			if got := tnbWarnOps(warns()); !slices.Equal(got, []string{"tnb:" + k.op}) {
				t.Errorf("warnings = %v; want one under tnb:%s", got, k.op)
			}
		})
	}
}

// A GovCloud or China ARN reaches the tag op unchanged and is the row's
// NativeID.
func TestScanTnb_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newTNBCalls()
			handlers := tnbHandlers(calls)
			prefix := "arn:" + c.partition + ":tnb:" + c.region + ":" + testAccountID + ":"
			pkgARN := prefix + "function-package/fp-p"
			opARN := prefix + "network-operation/no-p"
			calls.tags[pkgARN] = map[string]string{"k": "package"}
			calls.tags[opARN] = map[string]string{"k": "operation"}
			pkgs, ops := tnbKindOf(t, "ListSolFunctionPackages"), tnbKindOf(t, "ListSolNetworkOperations")
			handlers[pkgs.op] = func(in any) (any, error) {
				calls.record(pkgs.op, in)
				return pkgs.page(&pkgARN, "p", nil), nil
			}
			handlers[ops.op] = func(in any) (any, error) {
				calls.record(ops.op, in)
				return ops.page(&opARN, "p", nil), nil
			}
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(handlers, tnbTestDenied)
			if _, _, err := scanTnb(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, want := range []string{pkgARN, opARN} {
				if !slices.Contains(calls.sortedTagARNs(), want) {
					t.Errorf("ListTagsForResource ARNs %v lack %s", calls.sortedTagARNs(), want)
				}
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeTnbFunctionPackage)[pkgARN].TagsJSON, map[string]string{"k": "package"})
			assertTagsMap(t, rowsByNativeID(t, st, TypeTnbNetworkOperation)[opARN].TagsJSON, map[string]string{"k": "operation"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanTnb_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newTNBCalls()
	handlers := tnbHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListSolNetworkPackages"]
	handlers["ListSolNetworkPackages"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := tnbScan(ctx, st, handlers)
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
	for _, op := range []string{"ListSolNetworkInstances", "ListSolNetworkOperations"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanTnb_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "tnb.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := tnbHandlers(newTNBCalls())
		handlers["ListSolNetworkOperations"] = func(any) (any, error) { return nil, nx }
		total, _, err := tnbScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := tnbTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newTNBCalls()
		calls.errs[tnbKindOf(t, "ListSolFunctionPackages").arn("tagged")] = nx
		_, _, err := tnbScan(context.Background(), st, tnbHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newTNBCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range tnbKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(handlers, nx)
		_, _, err := scanTnb(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListSolFunctionInstances"); n != 0 {
			t.Errorf("ListSolFunctionInstances called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
