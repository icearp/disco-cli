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
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/elementalinference"
	eitypes "github.com/aws/aws-sdk-go-v2/service/elementalinference/types"
	"github.com/icearp/disco-cli/store"
)

// eiTestTotal is every row the fixture stores: two of each of the two types,
// one per page (the DELETED element of each first page is not stored).
const eiTestTotal = 2 * 2

var (
	eiTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: elemental-inference:List")
	eiTestThrottle = apiErr("TooManyRequestException", "slow down")
	eiTestBad      = apiErr("ValidationException", "bad")
	eiTestGone     = apiErr("ResourceNotFoundException", "gone")
	eiTestScope    = testAccountID + "/" + testRegion
)

// eiTestKind is one listing: its op, the type it stores, its ARN's resource
// kind, the noun its warnings name its elements by, and the status of its
// first page's element.
type eiTestKind struct {
	op, typ, kind, what, status string
}

func eiTestKinds() []eiTestKind {
	return []eiTestKind{
		{"ListFeeds", TypeElementalInferenceFeed, "feed", "feeds", "ACTIVE"},
		{"ListDictionaries", TypeElementalInferenceDictionary, "dictionary", "dictionaries", "AVAILABLE"},
	}
}

func eiTestKindOf(t *testing.T, op string) eiTestKind {
	t.Helper()
	for _, k := range eiTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Elemental Inference listing", op)
	return eiTestKind{}
}

func (k eiTestKind) eiTestName(id string) string { return "name-" + k.kind + "-" + id }

// eiTestARNIn is the ARN of k's element id in region, in the Service
// Reference's ARNFormat: arn:{p}:elemental-inference:{r}:{acct}:{kind}/{id}.
func (k eiTestKind) eiTestARNIn(partition, region, id string) string {
	return "arn:" + partition + ":elemental-inference:" + region + ":" + testAccountID + ":" + k.kind + "/" + id
}

func (k eiTestKind) eiTestARN(id string) string { return k.eiTestARNIn("aws", testRegion, id) }

// eiTestElement is k's listed element id with status, carrying arn (nil for
// an element listed without one).
func (k eiTestKind) eiTestElement(arn *string, id, status string) any {
	name := sdkaws.String(k.eiTestName(id))
	if k.op == "ListFeeds" {
		return eitypes.FeedSummary{
			Arn: arn, Id: sdkaws.String(id), Name: name, Status: eitypes.FeedStatus(status),
			Association: &eitypes.FeedAssociation{AssociatedResourceName: sdkaws.String("channel-" + id)},
		}
	}
	return eitypes.DictionarySummary{
		Arn: arn, Id: sdkaws.String(id), Name: name, Status: eitypes.DictionaryStatus(status),
		Language: eitypes.DictionaryLanguageEng,
	}
}

// eiTestPage is k's output holding elements es, and next.
func (k eiTestKind) eiTestPage(next *string, es ...any) any {
	if k.op == "ListFeeds" {
		out := &elementalinference.ListFeedsOutput{NextToken: next}
		for _, e := range es {
			out.Feeds = append(out.Feeds, e.(eitypes.FeedSummary))
		}
		return out
	}
	out := &elementalinference.ListDictionariesOutput{NextToken: next}
	for _, e := range es {
		out.Dictionaries = append(out.Dictionaries, e.(eitypes.DictionarySummary))
	}
	return out
}

// eiTestCalls records the calls of a scan and answers the tag reads, by
// ARN, with tags or tagErrs. partition and region are those of the ARNs the
// listings return.
type eiTestCalls struct {
	mu                sync.Mutex
	partition, region string
	calls             map[string]int
	maxResults        map[string][]*int32
	tagARNs           []string
	tags              map[string]map[string]string
	tagErrs           map[string]error
}

// newEITestCalls tags every "tagged" row with its type.
func newEITestCalls() *eiTestCalls { return newEITestCallsIn("aws", testRegion) }

func newEITestCallsIn(partition, region string) *eiTestCalls {
	c := &eiTestCalls{
		partition: partition, region: region,
		calls: map[string]int{}, maxResults: map[string][]*int32{},
		tags: map[string]map[string]string{}, tagErrs: map[string]error{},
	}
	for _, k := range eiTestKinds() {
		c.tags[k.eiTestARNIn(partition, region, "tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *eiTestCalls) eiTestRecord(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if op != "ListTagsForResource" {
		c.maxResults[op] = append(c.maxResults[op], bacField[*int32](in, "MaxResults"))
	}
}

func (c *eiTestCalls) eiTestCount(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *eiTestCalls) eiTestSortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *eiTestCalls) eiTestListTags(in any) (any, error) {
	arn := sv(in.(*elementalinference.ListTagsForResourceInput).ResourceArn)
	c.eiTestRecord("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	return &elementalinference.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// eiTestTwoPages answers k's op with "tagged" (k.status) and "deleted"
// (DELETED) on the first page and "bare" (no status) on the second.
func (c *eiTestCalls) eiTestTwoPages(k eiTestKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.eiTestRecord(k.op, in)
		arn := func(id string) *string { return sdkaws.String(k.eiTestARNIn(c.partition, c.region, id)) }
		if bacField[*string](in, "NextToken") != nil {
			return k.eiTestPage(nil, k.eiTestElement(arn("bare"), "bare", "")), nil
		}
		return k.eiTestPage(sdkaws.String("p2"),
			k.eiTestElement(arn("tagged"), "tagged", k.status),
			k.eiTestElement(arn("deleted"), "deleted", "DELETED")), nil
	}
}

// eiTestHandlers answers every op the scan calls from c.
func eiTestHandlers(c *eiTestCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.eiTestListTags}
	for _, k := range eiTestKinds() {
		h[k.op] = c.eiTestTwoPages(k)
	}
	return h
}

// eiTestScanAs runs the scan for acct in region against handlers through
// the real client; every op they do not answer is denied.
func eiTestScanAs(ctx context.Context, acct *account, region string, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	acct.cfg = opStubConfig(handlers, eiTestDenied)
	return scanElementalInference(ctx, acct, region, st, testScanID)
}

func eiTestScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	return eiTestScanAs(ctx, newTestAccount(testAccountID), testRegion, st, handlers)
}

// eiAllTagARNs is the ARN of every stored row of the fixture, sorted.
func eiAllTagARNs() []string {
	var arns []string
	for _, k := range eiTestKinds() {
		arns = append(arns, k.eiTestARN("tagged"), k.eiTestARN("bare"))
	}
	slices.Sort(arns)
	return arns
}

// eiStoredWithTags reports whether the row under arn is stored with tags. It
// does not fail the test, so a listing's handler can call it.
func eiStoredWithTags(st *store.Store, arn string) bool {
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
	return err == nil && r != nil && r.TagsJSON != nil
}

// Both listings store both their pages, each row under its listed ARN, in
// the scanned account's name, named by its listed name, in the scanned
// region, not managed, with its listed element as its attributes, its
// status as Status (none when the element has none) and the tags its tag
// read returns (nil for none). A DELETED element is not stored and its tags
// are not read. ListTagsForResource is sent exactly the ARNs of every row;
// every listing call is sent MaxResults 100. No error, no warning.
func TestScanElementalInference_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newEITestCalls()

	counted, upserts := countUpserts(st)
	total, err := eiTestScan(context.Background(), counted, eiTestHandlers(calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != eiTestTotal || inserted != eiTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, eiTestTotal, eiTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range eiTestKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %v; want tagged and bare", k.typ, rows)
		}
		for _, c := range []struct{ id, status string }{{"tagged", k.status}, {"bare", ""}} {
			arn := k.eiTestARN(c.id)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != k.eiTestName(c.id) || sv(r.Region) != testRegion || r.ManagedByProvider || sv(r.AccountName) != newTestAccount(testAccountID).Name || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Managed, AccountName, CreatedAt = %q, %q, %v, %q, %q; want %q, %s, false, the account's, none",
					arn, sv(r.Name), sv(r.Region), r.ManagedByProvider, sv(r.AccountName), sv(r.CreatedAt), k.eiTestName(c.id), testRegion)
			}
			if (c.status == "") != (r.Status == nil) || sv(r.Status) != c.status {
				t.Errorf("%s Status = %v; want %q (nil when empty)", arn, r.Status, c.status)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			if listed := kcJSONValue(t, k.eiTestElement(sdkaws.String(arn), c.id, c.status)); !reflect.DeepEqual(attrs, listed) {
				t.Errorf("%s attrs = %s; want its listed element %v", arn, r.AttributesJSON, listed)
			}
			if c.id == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if _, ok := rows[k.eiTestARN("deleted")]; ok {
			t.Errorf("DELETED %s stored", k.eiTestARN("deleted"))
		}
		if got := calls.maxResults[k.op]; len(got) != 2 || got[0] == nil || *got[0] != 100 || got[1] == nil || *got[1] != 100 {
			t.Errorf("%s MaxResults = %v; want two calls sent 100", k.op, got)
		}
	}
	if got := calls.eiTestSortedTagARNs(); !slices.Equal(got, eiAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, eiAllTagARNs())
	}
}

// A DELETING feed or dictionary still exists and an ARCHIVED feed is not
// documented as gone, so each is stored with its status and its tags read.
func TestScanElementalInference_StoresDeletingAndArchived(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newEITestCalls()
	handlers := eiTestHandlers(calls)
	kept := map[string][]string{"ListFeeds": {"DELETING", "ARCHIVED"}, "ListDictionaries": {"DELETING"}}
	var wantARNs []string
	for _, k := range eiTestKinds() {
		var es []any
		for _, status := range kept[k.op] {
			id := strings.ToLower(status)
			es = append(es, k.eiTestElement(sdkaws.String(k.eiTestARN(id)), id, status))
			wantARNs = append(wantARNs, k.eiTestARN(id))
		}
		handlers[k.op] = func(in any) (any, error) {
			calls.eiTestRecord(k.op, in)
			return k.eiTestPage(nil, es...), nil
		}
	}

	total, err := eiTestScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	for _, k := range eiTestKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		for _, status := range kept[k.op] {
			if r, ok := rows[k.eiTestARN(strings.ToLower(status))]; !ok || sv(r.Status) != status {
				t.Errorf("%s %s missing or Status != %s: %v", k.typ, status, status, rows)
			}
		}
	}
	slices.Sort(wantARNs)
	if got := calls.eiTestSortedTagARNs(); !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantARNs)
	}
}

// Every call goes to the scanned region, whatever region the account's
// config names.
func TestScanElementalInference_CallsTheScannedRegion(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var regions parentCalls
	cfg := opStubConfig(eiTestHandlers(newEITestCallsIn("aws", "eu-west-1")), eiTestDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, err := scanElementalInference(context.Background(), acct, "eu-west-1", st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != eiTestTotal {
		t.Errorf("total = %d; want %d", total, eiTestTotal)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
}

// The SDK retryer does not retry Elemental Inference's 429
// TooManyRequestException; the client the scanner builds does, and only
// that code, while keeping the retryer and attempt budget the account
// config sets.
func TestEIClientOptions_RetriesThrottle(t *testing.T) {
	t.Parallel()
	throttled := &eitypes.TooManyRequestException{Message: sdkaws.String("slow down")}
	if retry.NewStandard().IsErrorRetryable(throttled) {
		t.Fatal("the SDK's standard retryer retries TooManyRequestException; eiClientOptions' retryer wrap is redundant")
	}
	for _, tc := range []struct {
		name         string
		cfg          sdkaws.Config
		wantAttempts int
	}{
		// The shape aws_config.go loads: adaptive mode, 10 attempts.
		{"account config", sdkaws.Config{RetryMode: sdkaws.RetryModeAdaptive, RetryMaxAttempts: 10}, 10},
		// A config-supplied retryer is wrapped, not replaced.
		{"config retryer", sdkaws.Config{Retryer: func() sdkaws.Retryer {
			return retry.AddWithMaxAttempts(retry.NewStandard(), 7)
		}}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := elementalinference.NewFromConfig(tc.cfg, eiClientOptions(testRegion)).Options()
			if got := o.Retryer.MaxAttempts(); got != tc.wantAttempts {
				t.Errorf("MaxAttempts = %d, want %d", got, tc.wantAttempts)
			}
			if !o.Retryer.IsErrorRetryable(throttled) {
				t.Error("elemental-inference client does not retry TooManyRequestException")
			}
			if o.Retryer.IsErrorRetryable(&eitypes.ValidationException{Message: sdkaws.String("bad")}) {
				t.Error("elemental-inference client retries ValidationException")
			}
			if o.Region != testRegion {
				t.Errorf("region = %q, want %q", o.Region, testRegion)
			}
		})
	}
}

// The client scanElementalInference builds retries TooManyRequestException,
// on a listing and on a tag read of its fan-out: each answered once with
// the throttle and then successfully stores its row, tagged, with no error
// and no warning. lmRetryingConfig answers below the Retry middleware, which
// opStubConfig's answers never reach.
func TestScanElementalInference_ThrottleRetriedByScanClient(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	feeds := eiTestKindOf(t, "ListFeeds")
	arn := feeds.eiTestARN("tagged")
	var feedCalls, tagCalls atomic.Int32
	throttled := &eitypes.TooManyRequestException{Message: sdkaws.String("slow down")}
	acct := newTestAccount(testAccountID)
	acct.cfg = lmRetryingConfig(map[string]func(any) (any, error){
		"ListFeeds": func(any) (any, error) {
			if feedCalls.Add(1) == 1 {
				return nil, throttled
			}
			return feeds.eiTestPage(nil, feeds.eiTestElement(sdkaws.String(arn), "tagged", feeds.status)), nil
		},
		"ListDictionaries": func(any) (any, error) { return &elementalinference.ListDictionariesOutput{}, nil },
		"ListTagsForResource": func(any) (any, error) {
			if tagCalls.Add(1) == 1 {
				return nil, throttled
			}
			return &elementalinference.ListTagsForResourceOutput{Tags: map[string]string{"k": "v"}}, nil
		},
	})

	total, err := scanElementalInference(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1", total)
	}
	if f, tg := feedCalls.Load(), tagCalls.Load(); f != 2 || tg != 2 {
		t.Errorf("ListFeeds, ListTagsForResource attempts = %d, %d; want 2, 2", f, tg)
	}
	r, ok := rowsByNativeID(t, st, feeds.typ)[arn]
	if !ok {
		t.Fatalf("%s not stored", arn)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(sv(r.TagsJSON)), &got); err != nil || !reflect.DeepEqual(got, map[string]string{"k": "v"}) {
		t.Errorf("tags = %s (%v); want {\"k\":\"v\"}", sv(r.TagsJSON), err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// Every listing writes its first page, with its tags, before it reads the
// second.
func TestScanElementalInference_PagesWrittenAsRead(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	handlers := eiTestHandlers(newEITestCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range eiTestKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = eiStoredWithTags(st, k.eiTestARN("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, err := eiTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range eiTestKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.eiTestARN("tagged"))
		}
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanElementalInference_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newEITestCalls()
	handlers := eiTestHandlers(calls)
	for _, k := range eiTestKinds() {
		handlers[k.op] = func(in any) (any, error) {
			calls.eiTestRecord(k.op, in)
			return k.eiTestPage(nil), nil
		}
	}

	counted, upserts := countUpserts(st)
	total, err := eiTestScan(context.Background(), counted, handlers)
	inserted := upserts()
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range eiTestKinds() {
		if n := calls.eiTestCount(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
	}
	if n := calls.eiTestCount("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource called %d times; want 0", n)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently. A denial keeps the row
// untagged and warns once under the tag op with the region's scope; a
// throttle still failing after the retries keeps it and is transient; any
// other error keeps it and is an error.
func TestScanElementalInference_TagReadFailures(t *testing.T) {
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
	for _, k := range eiTestKinds() {
		cases = append(cases,
			tagFailure{k.kind + "/not found", k.op, eiTestGone, -1, "", eiTestTotal - 1, false, false},
			tagFailure{k.kind + "/denied", k.op, eiTestDenied, -1, "", eiTestTotal, true, true},
			tagFailure{k.kind + "/transient", k.op, eiTestThrottle, outcomeTransient, "TooManyRequestException", eiTestTotal, true, false},
			tagFailure{k.kind + "/other error", k.op, eiTestBad, outcomeError, "ValidationException", eiTestTotal, true, false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newEITestCalls()
			k := eiTestKindOf(t, c.op)
			arn := k.eiTestARN("tagged")
			calls.tagErrs[arn] = c.err

			total, err := eiTestScan(context.Background(), st, eiTestHandlers(calls))
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
			if _, ok := rows[k.eiTestARN("bare")]; !ok {
				t.Errorf("sibling %s missing", k.eiTestARN("bare"))
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{eiTagsOp}
			}
			if got := afWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != eiTestScope {
					t.Errorf("warning Scope %q; want %s", x.Scope, eiTestScope)
				}
			}
		})
	}
}

// Denied tag reads in both phases send one warning for the whole scan; the
// rows are kept.
func TestScanElementalInference_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newEITestCalls()
	calls.tagErrs[eiTestKindOf(t, "ListFeeds").eiTestARN("bare")] = eiTestDenied
	calls.tagErrs[eiTestKindOf(t, "ListDictionaries").eiTestARN("tagged")] = eiTestDenied

	total, err := eiTestScan(context.Background(), st, eiTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != eiTestTotal {
		t.Errorf("total = %d; want %d", total, eiTestTotal)
	}
	if got := afWarnOps(warns()); !slices.Equal(got, []string{eiTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, eiTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanElementalInference_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, eiTagsOp)
	calls := newEITestCalls()
	calls.tagErrs[eiTestKindOf(t, "ListFeeds").eiTestARN("bare")] = eiTestThrottle
	calls.tagErrs[eiTestKindOf(t, "ListDictionaries").eiTestARN("tagged")] = eiTestBad

	_, err := eiTestScan(context.Background(), st, eiTestHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", eiTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), and the dictionary phase does not run.
func TestScanElementalInference_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeElementalInferenceFeed)
	calls := newEITestCalls()
	calls.tagErrs[eiTestKindOf(t, "ListFeeds").eiTestARN("tagged")] = eiTestDenied

	total, err := eiTestScan(context.Background(), st, eiTestHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.eiTestCount("ListDictionaries"); n != 0 {
		t.Errorf("ListDictionaries called %d times; want 0 after the failed write", n)
	}
	if total != 0 {
		t.Errorf("total = %d; want 0", total)
	}
}

// A denied tag read is still warned about when a later phase fails: its row
// was stored, so the scan's error does not hide why it has no tags.
func TestScanElementalInference_TagDenialWarnedBesideLaterFailure(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		fail func(t *testing.T, st *store.Store, calls *eiTestCalls, handlers map[string]func(any) (any, error))
		want func(t *testing.T, err error)
	}{
		{"later listing page fails", func(t *testing.T, _ *store.Store, calls *eiTestCalls, handlers map[string]func(any) (any, error)) {
			list := handlers["ListDictionaries"]
			handlers["ListDictionaries"] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.eiTestRecord("ListDictionaries", in)
					return nil, eiTestBad
				}
				return list(in)
			}
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
		}},
		{"later tag read fails", func(t *testing.T, _ *store.Store, calls *eiTestCalls, _ map[string]func(any) (any, error)) {
			calls.tagErrs[eiTestKindOf(t, "ListDictionaries").eiTestARN("tagged")] = eiTestBad
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
		}},
		{"later write fails", func(t *testing.T, st *store.Store, _ *eiTestCalls, _ map[string]func(any) (any, error)) {
			refuseStoreWrites(t, st, TypeElementalInferenceDictionary)
		}, func(t *testing.T, err error) {
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newEITestCalls()
			denied := eiTestKindOf(t, "ListFeeds").eiTestARN("bare")
			calls.tagErrs[denied] = eiTestDenied
			handlers := eiTestHandlers(calls)
			c.fail(t, st, calls, handlers)

			_, err := eiTestScan(context.Background(), st, handlers)
			c.want(t, err)
			w := warns()
			if got := afWarnOps(w); !slices.Equal(got, []string{eiTagsOp}) {
				t.Fatalf("warnings = %v; want one under %s", w, eiTagsOp)
			}
			if x := w[0]; x.Scope != eiTestScope || !strings.Contains(x.Message, "first "+denied) {
				t.Errorf("warning = %+v; want Scope %s, keyed %s", x, eiTestScope, denied)
			}
			r, ok := rowsByNativeID(t, st, TypeElementalInferenceFeed)[denied]
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
// about under its op, and the other phase runs too; so does a first page
// failing otherwise.
func TestScanElementalInference_ListFailureKeepsPagesAndOtherPhase(t *testing.T) {
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
	for _, k := range eiTestKinds() {
		cases = append(cases,
			listFailure{"later page fails/" + k.op, k.op, eiTestBad, true, 1, false},
			listFailure{"first page fails/" + k.op, k.op, eiTestBad, false, 0, false},
			listFailure{"denied/" + k.op, k.op, eiTestDenied, false, 0, true})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newEITestCalls()
			handlers := eiTestHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if later := bacField[*string](in, "NextToken") != nil; later == c.laterPage {
					calls.eiTestRecord(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, err := eiTestScan(context.Background(), st, handlers)
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
				wantWarn = []string{"elemental-inference:" + c.op}
			}
			if got := afWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := eiTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			for _, k := range eiTestKinds() {
				rows := rowsByNativeID(t, st, k.typ)
				if k.op != c.op {
					if len(rows) != 2 {
						t.Errorf("%s rows = %d; want 2, its phase run", k.typ, len(rows))
					}
					continue
				}
				if _, ok := rows[k.eiTestARN("tagged")]; ok != (c.wantKept == 1) {
					t.Errorf("%s stored = %v; want %v", k.eiTestARN("tagged"), ok, c.wantKept == 1)
				}
				if _, ok := rows[k.eiTestARN("bare")]; ok {
					t.Errorf("%s stored; its page failed", k.eiTestARN("bare"))
				}
			}
		})
	}
}

// On ListDictionaries' first call, every shape an unrouted operation takes
// is a silent sub-feature gap: no dictionary, no error, no warning, and the
// feeds stored. On a later page the same shapes are returned, with the first
// page's dictionary stored.
func TestScanElementalInference_DictionariesNotDeployed(t *testing.T) {
	t.Parallel()
	dicts := eiTestKindOf(t, "ListDictionaries")
	for _, c := range []struct {
		name string
		err  error
	}{
		{"unknown operation", apiErr("UnknownOperationException", "")},
		{"unrouted gateway", apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")},
		{"http 404", respErrCode(404)},
	} {
		for _, laterPage := range []bool{false, true} {
			name := c.name + "/first page"
			if laterPage {
				name = c.name + "/later page"
			}
			t.Run(name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newEITestCalls()
				handlers := eiTestHandlers(calls)
				list := handlers[dicts.op]
				handlers[dicts.op] = func(in any) (any, error) {
					if later := bacField[*string](in, "NextToken") != nil; later == laterPage {
						calls.eiTestRecord(dicts.op, in)
						return nil, c.err
					}
					return list(in)
				}

				total, err := eiTestScan(context.Background(), st, handlers)
				wantDicts := 0
				if laterPage {
					wantDicts = 1
					if err == nil || !errors.Is(err, c.err) {
						t.Fatalf("err = %v; want %v returned", err, c.err)
					}
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if w := warns(); len(w) != 0 {
					t.Errorf("warnings = %v; want none", w)
				}
				if want := eiTestTotal - 2 + wantDicts; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				if n := len(rowsByNativeID(t, st, TypeElementalInferenceFeed)); n != 2 {
					t.Errorf("feed rows = %d; want 2", n)
				}
				if n := len(rowsByNativeID(t, st, TypeElementalInferenceDictionary)); n != wantDicts {
					t.Errorf("dictionary rows = %d; want %d", n, wantDicts)
				}
			})
		}
	}
}

// The feed API is Elemental Inference itself, not a sub-feature, so on
// ListFeeds' first call the shapes an unrouted operation takes are returned,
// not skipped as a gap: no feed is stored, no warning is recorded, and the
// dictionary phase still stores its rows.
func TestScanElementalInference_FeedsNotDeployedReturned(t *testing.T) {
	t.Parallel()
	feeds := eiTestKindOf(t, "ListFeeds")
	for _, c := range []struct {
		name string
		err  error
	}{
		{"unknown operation", apiErr("UnknownOperationException", "")},
		{"unrouted gateway", apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")},
		{"http 404", respErrCode(404)},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newEITestCalls()
			handlers := eiTestHandlers(calls)
			handlers[feeds.op] = func(in any) (any, error) {
				calls.eiTestRecord(feeds.op, in)
				return nil, c.err
			}

			total, err := eiTestScan(context.Background(), st, handlers)
			if !errors.Is(err, c.err) {
				t.Fatalf("err = %v; want %v returned", err, c.err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			if n := calls.eiTestCount(feeds.op); n != 1 {
				t.Errorf("ListFeeds calls = %d; want 1", n)
			}
			if want := eiTestTotal - 2; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, TypeElementalInferenceFeed)); n != 0 {
				t.Errorf("feed rows = %d; want 0", n)
			}
			if n := len(rowsByNativeID(t, st, TypeElementalInferenceDictionary)); n != 2 {
				t.Errorf("dictionary rows = %d; want 2", n)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages, and
// the later phase runs.
func TestScanElementalInference_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newEITestCalls()
	handlers := eiTestHandlers(calls)
	twoPages := handlers["ListFeeds"]
	handlers["ListFeeds"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*elementalinference.ListFeedsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, err := eiTestScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != eiTestTotal {
		t.Errorf("total = %d; want %d", total, eiTestTotal)
	}
	if n := calls.eiTestCount("ListFeeds"); n != 2 {
		t.Errorf("ListFeeds called %d times; want 2", n)
	}
	if n := len(rowsByNativeID(t, st, TypeElementalInferenceDictionary)); n != 2 {
		t.Errorf("dictionaries = %d; want 2", n)
	}
}

// An element listed without an ARN is not stored, not read, and warned about
// once under its listing with the region's scope, keyed by its name; the
// rest of the listing is stored.
func TestScanElementalInference_ElementWithoutARN(t *testing.T) {
	t.Parallel()
	for _, k := range eiTestKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newEITestCalls()
			handlers := eiTestHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.eiTestRecord(k.op, in)
					return k.eiTestPage(nil, k.eiTestElement(nil, "noarn", "")), nil
				}
				return twoPages(in)
			}

			total, err := eiTestScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := eiTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an ARN", k.typ, n)
			}
			want := slices.DeleteFunc(eiAllTagARNs(), func(a string) bool { return a == k.eiTestARN("bare") })
			if got := calls.eiTestSortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			w := warns()
			if got := afWarnOps(w); !slices.Equal(got, []string{"elemental-inference:" + k.op}) {
				t.Fatalf("warnings = %v; want one under elemental-inference:%s", w, k.op)
			}
			if x := w[0]; x.Scope != eiTestScope || !strings.HasSuffix(x.Message, "first "+k.eiTestName("noarn")) {
				t.Errorf("warning = %+v; want Scope %s, keyed %q", x, eiTestScope, k.eiTestName("noarn"))
			}
		})
	}
}

// In GovCloud and China the listed ARN of every row is its NativeID and
// reaches the tag op unchanged.
func TestScanElementalInference_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newEITestCallsIn(pf.partition, pf.region)
			if _, err := eiTestScanAs(context.Background(), partitionAccount(pf.partition), pf.region, st, eiTestHandlers(calls)); err != nil {
				t.Fatalf("scan: %v", err)
			}
			var want []string
			for _, k := range eiTestKinds() {
				want = append(want, k.eiTestARNIn(pf.partition, pf.region, "tagged"), k.eiTestARNIn(pf.partition, pf.region, "bare"))
			}
			slices.Sort(want)
			if got := calls.eiTestSortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			for _, k := range eiTestKinds() {
				arn := k.eiTestARNIn(pf.partition, pf.region, "tagged")
				assertTagsMap(t, rowsByNativeID(t, st, k.typ)[arn].TagsJSON, map[string]string{"type": k.typ})
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing; the later phase is not run, and the rows
// written before the stop stay stored.
func TestScanElementalInference_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newEITestCalls()
	handlers := eiTestHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListFeeds"]
	handlers["ListFeeds"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, err := eiTestScan(ctx, st, handlers)
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
	if n := calls.eiTestCount("ListDictionaries"); n != 0 {
		t.Errorf("ListDictionaries called %d times after ctx ended", n)
	}
	feeds := eiTestKindOf(t, "ListFeeds")
	if rows := rowsByNativeID(t, st, TypeElementalInferenceFeed); len(rows) != 1 || !eiStoredWithTags(st, feeds.eiTestARN("tagged")) {
		t.Errorf("feeds = %v; want only the first page's, with its tags", rows)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and the later phase is not run.
func TestScanElementalInference_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "elemental-inference.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after the other stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := eiTestHandlers(newEITestCalls())
		handlers["ListDictionaries"] = func(any) (any, error) { return nil, nx }
		total, err := eiTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := eiTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d feeds counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newEITestCalls()
		calls.tagErrs[eiTestKindOf(t, "ListFeeds").eiTestARN("tagged")] = nx
		_, err := eiTestScan(context.Background(), st, eiTestHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newEITestCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range eiTestKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.eiTestRecord(k.op, in)
				return nil, nx
			}
		}
		_, err := eiTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.eiTestCount("ListDictionaries"); n != 0 {
			t.Errorf("ListDictionaries called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
