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
	"github.com/aws/aws-sdk-go-v2/service/gameliftstreams"
	glstypes "github.com/aws/aws-sdk-go-v2/service/gameliftstreams/types"
	"github.com/icearp/disco-cli/store"
)

// glsTestTotal is every row of the fixture: two of each of the two types,
// one per page.
const glsTestTotal = 2 * 2

var (
	glsTestDenied   = apiErr("AccessDeniedException", "not authorized")
	glsTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	glsTestBad      = apiErr("ValidationException", "bad")
	glsTestGone     = apiErr("ResourceNotFoundException", "gone")
	glsTestCreated  = time.Date(2025, 7, 28, 0, 0, 0, 0, time.UTC)
	glsTestUpdated  = time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
	glsTestScope    = testAccountID + "/" + testRegion
)

// glsTestKind is one listing: its op, the type it stores and its ARN's
// resource kind.
type glsTestKind struct {
	op, typ, kind string
}

func glsTestKinds() []glsTestKind {
	return []glsTestKind{
		{"ListApplications", TypeGameLiftStreamsApplication, "application"},
		{"ListStreamGroups", TypeGameLiftStreamsStreamGroup, "streamgroup"},
	}
}

func glsTestKindOf(t *testing.T, op string) glsTestKind {
	t.Helper()
	for _, k := range glsTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no GameLift Streams listing", op)
	return glsTestKind{}
}

// id is the Id of k's element with suffix, which the row is named by.
func (k glsTestKind) id(suffix string) string { return k.kind + "-" + suffix }

// arnIn is the ARN of k's element with suffix in partition and region, in
// the Service Reference's ARNFormat:
// arn:{p}:gameliftstreams:{region}:{acct}:{kind}/{id}.
func (k glsTestKind) arnIn(partition, region, suffix string) string {
	return "arn:" + partition + ":gameliftstreams:" + region + ":" + testAccountID + ":" + k.kind + "/" + k.id(suffix)
}

func (k glsTestKind) arn(suffix string) string { return k.arnIn("aws", testRegion, suffix) }

// element is k's listed element with suffix, carrying arn (nil for an
// element listed without one). The "bare" element's Status differs from
// the others' so a row builder storing a constant Status fails the tests.
func (k glsTestKind) element(arn *string, suffix string) any {
	id := sdkaws.String(k.id(suffix))
	if k.op == "ListApplications" {
		status := glstypes.ApplicationStatusReady
		if suffix == "bare" {
			status = glstypes.ApplicationStatusProcessing
		}
		return glstypes.ApplicationSummary{
			Arn: arn, Id: id, Status: status,
			CreatedAt: &glsTestCreated, LastUpdatedAt: &glsTestUpdated, Description: sdkaws.String("app " + suffix),
			RuntimeEnvironment: &glstypes.RuntimeEnvironment{Type: glstypes.RuntimeEnvironmentTypeProton, Version: sdkaws.String("20241007")},
		}
	}
	status := glstypes.StreamGroupStatusActive
	if suffix == "bare" {
		status = glstypes.StreamGroupStatusActivating
	}
	return glstypes.StreamGroupSummary{
		Arn: arn, Id: id, Status: status, StreamClass: glstypes.StreamClassGen5nHigh,
		CreatedAt: &glsTestCreated, LastUpdatedAt: &glsTestUpdated, Description: sdkaws.String("group " + suffix),
		DefaultApplication: &glstypes.DefaultApplication{Id: sdkaws.String("application-" + suffix)},
	}
}

// page is k's output holding element e, and next.
func (k glsTestKind) page(e any, next *string) any {
	if a, ok := e.(glstypes.ApplicationSummary); ok {
		return &gameliftstreams.ListApplicationsOutput{NextToken: next, Items: []glstypes.ApplicationSummary{a}}
	}
	return &gameliftstreams.ListStreamGroupsOutput{NextToken: next, Items: []glstypes.StreamGroupSummary{e.(glstypes.StreamGroupSummary)}}
}

// glsTestCalls records the calls of a scan and answers the tag reads, by
// ARN, with tags or tagErrs. partition and region are those of the ARNs the
// listings return.
type glsTestCalls struct {
	mu                sync.Mutex
	partition, region string
	calls             map[string]int
	maxResults        map[string][]*int32
	tagARNs           []string
	tags              map[string]map[string]string
	tagErrs           map[string]error
}

// newGLSTestCalls tags every "-tagged" row with its type.
func newGLSTestCalls() *glsTestCalls { return newGLSTestCallsIn("aws", testRegion) }

func newGLSTestCallsIn(partition, region string) *glsTestCalls {
	c := &glsTestCalls{
		partition: partition, region: region,
		calls: map[string]int{}, maxResults: map[string][]*int32{},
		tags: map[string]map[string]string{}, tagErrs: map[string]error{},
	}
	for _, k := range glsTestKinds() {
		c.tags[k.arnIn(partition, region, "tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

// record notes op's call and the MaxResults a listing was sent.
func (c *glsTestCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if op != "ListTagsForResource" {
		c.maxResults[op] = append(c.maxResults[op], bacField[*int32](in, "MaxResults"))
	}
}

func (c *glsTestCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *glsTestCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *glsTestCalls) listTags(in any) (any, error) {
	arn := sv(in.(*gameliftstreams.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	return &gameliftstreams.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *glsTestCalls) twoPages(k glsTestKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(k.element(sdkaws.String(k.arnIn(c.partition, c.region, suffix)), suffix), next), nil
	}
}

// glsTestHandlers answers every op the scan calls from c.
func glsTestHandlers(c *glsTestCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range glsTestKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// glsTestScanAs runs the scan for acct in region against handlers through
// the real client; every op they do not answer is denied.
func glsTestScanAs(ctx context.Context, acct *account, region string, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	client := gameliftstreams.NewFromConfig(opStubConfig(handlers, glsTestDenied), func(o *gameliftstreams.Options) { o.Region = region })
	return scanGameLiftStreams(ctx, client, acct, region, st, testScanID)
}

func glsTestScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	return glsTestScanAs(ctx, newTestAccount(testAccountID), testRegion, st, handlers)
}

// glsAllTagARNs is the ARN of every row of the fixture, sorted.
func glsAllTagARNs() []string {
	var arns []string
	for _, k := range glsTestKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

// glsStoredWithTags reports whether the row under arn is stored with tags.
// It does not fail the test, so a listing's handler can call it.
func glsStoredWithTags(st *store.Store, arn string) bool {
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
	return err == nil && r != nil && r.TagsJSON != nil
}

// Both listings store both their pages, each row under its listed ARN, in
// the scanned account's name, named by its listed Id, in the scanned region,
// with its listed Status, no CreatedAt, not managed, its listed element as
// its attributes and the tags its tag read returns (nil for none).
// ListTagsForResource is sent exactly the ARNs of every row; every listing
// call is sent MaxResults 100. No error, no warning.
func TestScanGameLiftStreams_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGLSTestCalls()

	total, inserted, err := glsTestScan(context.Background(), st, glsTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != glsTestTotal || inserted != glsTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, glsTestTotal, glsTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	type opSuffix struct{ op, suffix string }
	wantStatus := map[opSuffix]string{
		{"ListApplications", "tagged"}: "READY", {"ListApplications", "bare"}: "PROCESSING",
		{"ListStreamGroups", "tagged"}: "ACTIVE", {"ListStreamGroups", "bare"}: "ACTIVATING",
	}
	for _, k := range glsTestKinds() {
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
			if sv(r.Name) != k.id(suffix) || sv(r.Region) != testRegion || r.ManagedByProvider || sv(r.AccountName) != newTestAccount(testAccountID).Name {
				t.Errorf("%s Name, Region, Managed, AccountName = %q, %q, %v, %q; want %q, %s, false, the account's",
					arn, sv(r.Name), sv(r.Region), r.ManagedByProvider, sv(r.AccountName), k.id(suffix), testRegion)
			}
			if want := wantStatus[opSuffix{k.op, suffix}]; sv(r.Status) != want || r.CreatedAt != nil {
				t.Errorf("%s Status, CreatedAt = %q, %q; want %s, none", arn, sv(r.Status), sv(r.CreatedAt), want)
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
	if got := calls.sortedTagARNs(); !slices.Equal(got, glsAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, glsAllTagARNs())
	}
}

// Every listing writes its first page, with its tags, before it reads the
// second.
func TestScanGameLiftStreams_PagesWrittenAsRead(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	handlers := glsTestHandlers(newGLSTestCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range glsTestKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = glsStoredWithTags(st, k.arn("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := glsTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range glsTestKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.arn("tagged"))
		}
	}
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanGameLiftStreams_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := glsTestScan(context.Background(), st, glsTestHandlers(newGLSTestCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := glsTestScan(context.Background(), st, glsTestHandlers(newGLSTestCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != glsTestTotal {
		t.Errorf("rescan total = %d; want %d", total, glsTestTotal)
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanGameLiftStreams_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGLSTestCalls()
	handlers := glsTestHandlers(calls)
	empty := map[string]any{
		"ListApplications": &gameliftstreams.ListApplicationsOutput{},
		"ListStreamGroups": &gameliftstreams.ListStreamGroupsOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	total, inserted, err := glsTestScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range glsTestKinds() {
		if n := calls.count(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource called %d times; want 0", n)
	}
}

// A failed tag read keeps the row untagged and its siblings stored. A denial
// warns once under the tag op with the region's scope; a throttle is
// transient; ResourceNotFoundException, which the op does not model, and any
// other error are errors.
func TestScanGameLiftStreams_TagReadFailures(t *testing.T) {
	t.Parallel()
	type tagFailure struct {
		name    string
		op      string
		err     error
		outcome serviceOutcome // -1: no error
		code    string
		warn    bool
	}
	var cases []tagFailure
	for _, k := range glsTestKinds() {
		cases = append(cases,
			tagFailure{k.kind + "/denied", k.op, glsTestDenied, -1, "", true},
			tagFailure{k.kind + "/transient", k.op, glsTestThrottle, outcomeTransient, "ThrottlingException", false},
			tagFailure{k.kind + "/unmodeled not found", k.op, glsTestGone, outcomeError, "ResourceNotFoundException", false},
			tagFailure{k.kind + "/other error", k.op, glsTestBad, outcomeError, "ValidationException", false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newGLSTestCalls()
			k := glsTestKindOf(t, c.op)
			arn := k.arn("tagged")
			calls.tagErrs[arn] = c.err

			total, _, err := glsTestScan(context.Background(), st, glsTestHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != glsTestTotal {
				t.Errorf("total = %d; want %d", total, glsTestTotal)
			}
			rows := rowsByNativeID(t, st, k.typ)
			r, ok := rows[arn]
			if !ok {
				t.Fatalf("%s missing", arn)
			}
			if r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if _, ok := rows[k.arn("bare")]; !ok {
				t.Errorf("sibling %s missing", k.arn("bare"))
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{glsTagsOp}
			}
			if got := afWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != glsTestScope {
					t.Errorf("warning Scope %q; want %s", x.Scope, glsTestScope)
				}
			}
		})
	}
}

// Denied tag reads in both phases send one warning for the whole scan; the
// rows are kept.
func TestScanGameLiftStreams_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newGLSTestCalls()
	calls.tagErrs[glsTestKindOf(t, "ListApplications").arn("bare")] = glsTestDenied
	calls.tagErrs[glsTestKindOf(t, "ListStreamGroups").arn("tagged")] = glsTestDenied

	total, _, err := glsTestScan(context.Background(), st, glsTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != glsTestTotal {
		t.Errorf("total = %d; want %d", total, glsTestTotal)
	}
	if got := afWarnOps(warns()); !slices.Equal(got, []string{glsTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, glsTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanGameLiftStreams_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, glsTagsOp)
	calls := newGLSTestCalls()
	calls.tagErrs[glsTestKindOf(t, "ListApplications").arn("bare")] = glsTestThrottle
	calls.tagErrs[glsTestKindOf(t, "ListStreamGroups").arn("tagged")] = glsTestBad

	_, _, err := glsTestScan(context.Background(), st, glsTestHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", glsTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), and the stream-group phase does not run.
func TestScanGameLiftStreams_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeGameLiftStreamsApplication)
	calls := newGLSTestCalls()
	calls.tagErrs[glsTestKindOf(t, "ListApplications").arn("tagged")] = glsTestDenied

	total, _, err := glsTestScan(context.Background(), st, glsTestHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListStreamGroups"); n != 0 {
		t.Errorf("ListStreamGroups called %d times; want 0 after the failed write", n)
	}
	if total != 0 {
		t.Errorf("total = %d; want 0", total)
	}
}

// A denied tag read is still warned about when a later phase fails: its row
// was stored, so the scan's error does not hide why it has no tags.
func TestScanGameLiftStreams_TagDenialWarnedBesideLaterFailure(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		fail func(t *testing.T, st *store.Store, calls *glsTestCalls, handlers map[string]func(any) (any, error))
		want func(t *testing.T, err error)
	}{
		{"later listing page fails", func(t *testing.T, _ *store.Store, calls *glsTestCalls, handlers map[string]func(any) (any, error)) {
			list := handlers["ListStreamGroups"]
			handlers["ListStreamGroups"] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record("ListStreamGroups", in)
					return nil, glsTestBad
				}
				return list(in)
			}
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
		}},
		{"later tag read fails", func(t *testing.T, _ *store.Store, calls *glsTestCalls, _ map[string]func(any) (any, error)) {
			calls.tagErrs[glsTestKindOf(t, "ListStreamGroups").arn("tagged")] = glsTestBad
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
		}},
		{"later write fails", func(t *testing.T, st *store.Store, _ *glsTestCalls, _ map[string]func(any) (any, error)) {
			refuseStoreWrites(t, st, TypeGameLiftStreamsStreamGroup)
		}, func(t *testing.T, err error) {
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newGLSTestCalls()
			denied := glsTestKindOf(t, "ListApplications").arn("bare")
			calls.tagErrs[denied] = glsTestDenied
			handlers := glsTestHandlers(calls)
			c.fail(t, st, calls, handlers)

			_, _, err := glsTestScan(context.Background(), st, handlers)
			c.want(t, err)
			w := warns()
			if got := afWarnOps(w); !slices.Equal(got, []string{glsTagsOp}) {
				t.Fatalf("warnings = %v; want one under %s", w, glsTagsOp)
			}
			if x := w[0]; x.Scope != glsTestScope || !strings.Contains(x.Message, "first "+denied) {
				t.Errorf("warning = %+v; want Scope %s, keyed %s", x, glsTestScope, denied)
			}
			r, ok := rowsByNativeID(t, st, TypeGameLiftStreamsApplication)[denied]
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
func TestScanGameLiftStreams_ListFailureKeepsPagesAndOtherPhase(t *testing.T) {
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
	for _, k := range glsTestKinds() {
		cases = append(cases,
			listFailure{"later page fails/" + k.op, k.op, glsTestBad, true, 1, false},
			listFailure{"denied/" + k.op, k.op, glsTestDenied, false, 0, true})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newGLSTestCalls()
			handlers := glsTestHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if later := bacField[*string](in, "NextToken") != nil; later == c.laterPage {
					calls.record(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := glsTestScan(context.Background(), st, handlers)
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
				wantWarn = []string{"gameliftstreams:" + c.op}
			}
			if got := afWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := glsTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			for _, k := range glsTestKinds() {
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
func TestScanGameLiftStreams_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newGLSTestCalls()
	handlers := glsTestHandlers(calls)
	twoPages := handlers["ListApplications"]
	handlers["ListApplications"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*gameliftstreams.ListApplicationsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := glsTestScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != glsTestTotal {
		t.Errorf("total = %d; want %d", total, glsTestTotal)
	}
	if n := calls.count("ListApplications"); n != 2 {
		t.Errorf("ListApplications called %d times; want 2", n)
	}
	if n := len(rowsByNativeID(t, st, TypeGameLiftStreamsStreamGroup)); n != 2 {
		t.Errorf("stream groups = %d; want 2", n)
	}
}

// An element listed without an ARN is not stored, not read, and warned about
// once under its listing with the region's scope, keyed by its Id; the rest
// of the listing is stored.
func TestScanGameLiftStreams_ElementWithoutARN(t *testing.T) {
	t.Parallel()
	for _, k := range glsTestKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newGLSTestCalls()
			handlers := glsTestHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record(k.op, in)
					return k.page(k.element(nil, "noarn"), nil), nil
				}
				return twoPages(in)
			}

			total, _, err := glsTestScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := glsTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an ARN", k.typ, n)
			}
			want := slices.DeleteFunc(glsAllTagARNs(), func(a string) bool { return a == k.arn("bare") })
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			w := warns()
			if got := afWarnOps(w); !slices.Equal(got, []string{"gameliftstreams:" + k.op}) {
				t.Fatalf("warnings = %v; want one under gameliftstreams:%s", w, k.op)
			}
			if x := w[0]; x.Scope != glsTestScope || !strings.HasSuffix(x.Message, "first "+k.id("noarn")) {
				t.Errorf("warning = %+v; want Scope %s, keyed %q", x, glsTestScope, k.id("noarn"))
			}
		})
	}
}

// In GovCloud and China the listed ARN of every row is its NativeID and
// reaches the tag op unchanged.
func TestScanGameLiftStreams_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, partition := range []string{"aws-us-gov", "aws-cn"} {
		t.Run(partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(partition)
			region := acct.Regions[0]
			calls := newGLSTestCallsIn(partition, region)
			if _, _, err := glsTestScanAs(context.Background(), acct, region, st, glsTestHandlers(calls)); err != nil {
				t.Fatalf("scan: %v", err)
			}
			var want []string
			for _, k := range glsTestKinds() {
				want = append(want, k.arnIn(partition, region, "tagged"), k.arnIn(partition, region, "bare"))
			}
			slices.Sort(want)
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			for _, k := range glsTestKinds() {
				arn := k.arnIn(partition, region, "tagged")
				assertTagsMap(t, rowsByNativeID(t, st, k.typ)[arn].TagsJSON, map[string]string{"type": k.typ})
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing; the later phase is not run, and the rows
// written before the stop stay stored.
func TestScanGameLiftStreams_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newGLSTestCalls()
	handlers := glsTestHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListApplications"]
	handlers["ListApplications"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := glsTestScan(ctx, st, handlers)
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
	if n := calls.count("ListStreamGroups"); n != 0 {
		t.Errorf("ListStreamGroups called %d times after ctx ended", n)
	}
	apps := glsTestKindOf(t, "ListApplications")
	if rows := rowsByNativeID(t, st, TypeGameLiftStreamsApplication); len(rows) != 1 || !glsStoredWithTags(st, apps.arn("tagged")) {
		t.Errorf("applications = %v; want only the first page's, with its tags", rows)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and the later phase is not run.
func TestScanGameLiftStreams_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "gameliftstreams.us-east-1.api.aws", IsNotFound: true}
	t.Run("a listing after the other stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := glsTestHandlers(newGLSTestCalls())
		handlers["ListStreamGroups"] = func(any) (any, error) { return nil, nx }
		total, _, err := glsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := glsTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d applications counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newGLSTestCalls()
		calls.tagErrs[glsTestKindOf(t, "ListApplications").arn("tagged")] = nx
		_, _, err := glsTestScan(context.Background(), st, glsTestHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newGLSTestCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range glsTestKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		_, _, err := glsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListStreamGroups"); n != 0 {
			t.Errorf("ListStreamGroups called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
