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
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/kinesisvideo"
	kvstypes "github.com/aws/aws-sdk-go-v2/service/kinesisvideo/types"
	"github.com/icearp/disco-cli/store"
)

// kvsTestTotal is every row of the fixture: two of each of the two types,
// one per page.
const kvsTestTotal = 2 * 2

var (
	kvsTestThrottle = apiErr("ClientLimitExceededException", "exceeded the limit of allowed client calls")
	kvsTestBad      = apiErr("InvalidArgumentException", "bad")
	kvsTestGone     = apiErr("ResourceNotFoundException", "gone")
	kvsTestCreated  = time.Date(2025, 7, 28, 0, 0, 0, 0, time.UTC)
	kvsTestScope    = testAccountID + "/" + testRegion
)

// kvsTestKind is one listing: its op, the type it stores, its ARN's resource
// kind, the op that reads its tags, the label that op's warnings and errors
// carry, and the denial that op models.
type kvsTestKind struct {
	op, typ, kind, tagOp, tagLabel string
	denied                         error
}

func kvsTestKinds() []kvsTestKind {
	return []kvsTestKind{
		{
			"ListStreams", TypeKinesisVideoStream, "stream", "ListTagsForStream", "kinesis-video:ListTagsForStream",
			apiErr("NotAuthorizedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: kinesisvideo:ListTagsForStream"),
		},
		{
			"ListSignalingChannels", TypeKinesisVideoSignalingChannel, "channel", "ListTagsForResource", "kinesis-video:ListTagsForResource",
			apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: kinesisvideo:ListTagsForResource"),
		},
	}
}

func kvsTestKindOf(t *testing.T, op string) kvsTestKind {
	t.Helper()
	for _, k := range kvsTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Kinesis Video listing", op)
	return kvsTestKind{}
}

// kvsTestName is the name of k's element with suffix, which the row is named
// by.
func (k kvsTestKind) kvsTestName(suffix string) string { return k.kind + "-" + suffix }

// kvsTestARNIn is the ARN of k's element with suffix in partition and region,
// in the Service Reference's ARNFormat:
// arn:{p}:kinesisvideo:{region}:{acct}:{kind}/{name}/{creationTime}.
func (k kvsTestKind) kvsTestARNIn(partition, region, suffix string) string {
	return "arn:" + partition + ":kinesisvideo:" + region + ":" + testAccountID + ":" + k.kind + "/" + k.kvsTestName(suffix) + "/1753660800000"
}

func (k kvsTestKind) kvsTestARN(suffix string) string {
	return k.kvsTestARNIn("aws", testRegion, suffix)
}

// kvsTestStatus is the status of k's element with suffix: the "bare"
// element is DELETING, so a row builder storing a constant Status fails the
// tests and a resource being deleted is shown stored and read.
func kvsTestStatus(suffix string) kvstypes.Status {
	if suffix == "bare" {
		return kvstypes.StatusDeleting
	}
	return kvstypes.StatusActive
}

// kvsTestElement is k's listed element with suffix, carrying arn (nil for an
// element listed without one).
func (k kvsTestKind) kvsTestElement(arn *string, suffix string) any {
	name := sdkaws.String(k.kvsTestName(suffix))
	if k.op == "ListStreams" {
		return kvstypes.StreamInfo{
			StreamARN: arn, StreamName: name, Status: kvsTestStatus(suffix),
			CreationTime: &kvsTestCreated, DataRetentionInHours: sdkaws.Int32(24),
			DeviceName: sdkaws.String("camera-" + suffix), MediaType: sdkaws.String("video/h264"),
			KmsKeyId: sdkaws.String("alias/aws/kinesisvideo"), Version: sdkaws.String("v-" + suffix),
		}
	}
	return kvstypes.ChannelInfo{
		ChannelARN: arn, ChannelName: name, ChannelStatus: kvsTestStatus(suffix),
		ChannelType: kvstypes.ChannelTypeSingleMaster, CreationTime: &kvsTestCreated,
		SingleMasterConfiguration: &kvstypes.SingleMasterConfiguration{MessageTtlSeconds: sdkaws.Int32(60)},
		Version:                   sdkaws.String("v-" + suffix),
	}
}

// kvsTestPage is k's output holding element e, and next.
func (k kvsTestKind) kvsTestPage(e any, next *string) any {
	if s, ok := e.(kvstypes.StreamInfo); ok {
		return &kinesisvideo.ListStreamsOutput{NextToken: next, StreamInfoList: []kvstypes.StreamInfo{s}}
	}
	return &kinesisvideo.ListSignalingChannelsOutput{NextToken: next, ChannelInfoList: []kvstypes.ChannelInfo{e.(kvstypes.ChannelInfo)}}
}

// kvsTestTags is the merged tags of k's "tagged" row: its first tag page
// holds "type", its second "page".
func (k kvsTestKind) kvsTestTags() map[string]string {
	return map[string]string{"type": k.typ, "page": "2"}
}

// kvsTestCalls records the calls of a scan and answers the tag reads by ARN:
// a "tagged" row's tags come in two pages, any other row has none, and
// tagErrs fails a row's read. partition and region are those of the ARNs the
// listings return.
type kvsTestCalls struct {
	mu                sync.Mutex
	partition, region string
	calls             map[string]int
	maxResults        map[string][]*int32
	tagARNs           map[string][]string
	tagErrs           map[string]error
	repeatTagToken    map[string]bool
}

func newKVSTestCalls() *kvsTestCalls { return newKVSTestCallsIn("aws", testRegion) }

func newKVSTestCallsIn(partition, region string) *kvsTestCalls {
	return &kvsTestCalls{
		partition: partition, region: region,
		calls: map[string]int{}, maxResults: map[string][]*int32{},
		tagARNs: map[string][]string{}, tagErrs: map[string]error{}, repeatTagToken: map[string]bool{},
	}
}

// kvsTestRecord notes op's call and the MaxResults a listing was sent.
func (c *kvsTestCalls) kvsTestRecord(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if op == "ListStreams" || op == "ListSignalingChannels" {
		c.maxResults[op] = append(c.maxResults[op], bacField[*int32](in, "MaxResults"))
	}
}

func (c *kvsTestCalls) kvsTestCount(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

// kvsTestSortedTagARNs is the ARNs tagOp's reads were sent on their first
// page, sorted.
func (c *kvsTestCalls) kvsTestSortedTagARNs(tagOp string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs[tagOp]))
}

// kvsTestListTags answers k's tag op. The first page of a "tagged" row holds
// "type" and NextToken "t2" (repeated on every page when repeatTagToken is
// set); its second page holds "page". tagErrs fails the first page, or, for
// an ARN suffixed "#2", only the second.
func (c *kvsTestCalls) kvsTestListTags(k kvsTestKind) func(any) (any, error) {
	arnField := "ResourceARN"
	if k.tagOp == "ListTagsForStream" {
		arnField = "StreamARN"
	}
	tagged := func() string { return k.kvsTestARNIn(c.partition, c.region, "tagged") }
	return func(in any) (any, error) {
		c.kvsTestRecord(k.tagOp, in)
		arn := sv(bacField[*string](in, arnField))
		token := bacField[*string](in, "NextToken")
		c.mu.Lock()
		defer c.mu.Unlock()
		page := map[string]string{}
		var next *string
		if token == nil {
			c.tagARNs[k.tagOp] = append(c.tagARNs[k.tagOp], arn)
			if err := c.tagErrs[arn]; err != nil {
				return nil, err
			}
			if arn == tagged() {
				page, next = map[string]string{"type": k.typ}, sdkaws.String("t2")
			}
		} else {
			if err := c.tagErrs[arn+"#2"]; err != nil {
				return nil, err
			}
			page["page"] = "2"
			if c.repeatTagToken[arn] {
				next = token
			}
		}
		if k.tagOp == "ListTagsForStream" {
			return &kinesisvideo.ListTagsForStreamOutput{Tags: page, NextToken: next}, nil
		}
		return &kinesisvideo.ListTagsForResourceOutput{Tags: page, NextToken: next}, nil
	}
}

// kvsTestTwoPages answers k's op with "tagged" on the first page and "bare"
// on the second.
func (c *kvsTestCalls) kvsTestTwoPages(k kvsTestKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.kvsTestRecord(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.kvsTestPage(k.kvsTestElement(sdkaws.String(k.kvsTestARNIn(c.partition, c.region, suffix)), suffix), next), nil
	}
}

// kvsTestHandlers answers every op the scan calls from c.
func kvsTestHandlers(c *kvsTestCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){}
	for _, k := range kvsTestKinds() {
		h[k.op] = c.kvsTestTwoPages(k)
		h[k.tagOp] = c.kvsTestListTags(k)
	}
	return h
}

// kvsTestScanAs runs the scan's real entry point for acct in region against
// handlers; every op they do not answer is denied.
func kvsTestScanAs(ctx context.Context, acct *account, region string, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct.cfg = opStubConfig(handlers, kvsTestKinds()[0].denied)
	return scanKinesisVideo(ctx, acct, region, st, testScanID)
}

func kvsTestScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	return kvsTestScanAs(ctx, newTestAccount(testAccountID), testRegion, st, handlers)
}

// kvsStoredWithTags reports whether the row under arn is stored with tags. It
// does not fail the test, so a listing's handler can call it.
func kvsStoredWithTags(st *store.Store, arn string) bool {
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
	return err == nil && r != nil && r.TagsJSON != nil
}

// Both listings store both their pages, each row under its listed ARN, in
// the scanned account's name, named by its listed name, in the scanned
// region, with its listed status (DELETING included), no CreatedAt, not
// managed, its listed element as its attributes and the tags of every page
// its tag read returns (nil for none). Streams' tags are read with
// ListTagsForStream and channels' with ListTagsForResource, each sent
// exactly the ARNs of its type's rows; every listing call is sent MaxResults
// 10000. No error, no warning.
func TestScanKinesisVideo_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newKVSTestCalls()

	total, inserted, err := kvsTestScan(context.Background(), st, kvsTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != kvsTestTotal || inserted != kvsTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, kvsTestTotal, kvsTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	wantStatus := map[string]string{"tagged": "ACTIVE", "bare": "DELETING"}
	for _, k := range kvsTestKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", k.typ, len(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.kvsTestARN(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != k.kvsTestName(suffix) || sv(r.Region) != testRegion || r.ManagedByProvider || sv(r.AccountName) != newTestAccount(testAccountID).Name {
				t.Errorf("%s Name, Region, Managed, AccountName = %q, %q, %v, %q; want %q, %s, false, the account's",
					arn, sv(r.Name), sv(r.Region), r.ManagedByProvider, sv(r.AccountName), k.kvsTestName(suffix), testRegion)
			}
			if sv(r.Status) != wantStatus[suffix] || r.CreatedAt != nil {
				t.Errorf("%s Status, CreatedAt = %q, %q; want %s, none", arn, sv(r.Status), sv(r.CreatedAt), wantStatus[suffix])
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			if listed := kcJSONValue(t, k.kvsTestElement(sdkaws.String(arn), suffix)); !reflect.DeepEqual(attrs, listed) {
				t.Errorf("%s attrs = %s; want its listed element %v", arn, r.AttributesJSON, listed)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, k.kvsTestTags())
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if got := calls.maxResults[k.op]; len(got) != 2 || got[0] == nil || *got[0] != 10000 || got[1] == nil || *got[1] != 10000 {
			t.Errorf("%s MaxResults = %v; want two calls sent 10000", k.op, got)
		}
		want := []string{k.kvsTestARN("bare"), k.kvsTestARN("tagged")}
		if got := calls.kvsTestSortedTagARNs(k.tagOp); !slices.Equal(got, want) {
			t.Errorf("%s ARNs = %v\nwant %v", k.tagOp, got, want)
		}
		// One call per untagged row, two for the tagged row's two pages.
		if n := calls.kvsTestCount(k.tagOp); n != 3 {
			t.Errorf("%s called %d times; want 3", k.tagOp, n)
		}
	}
}

// Every listing writes its first page, with its tags, before it reads the
// second.
func TestScanKinesisVideo_PagesWrittenAsRead(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	handlers := kvsTestHandlers(newKVSTestCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range kvsTestKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = kvsStoredWithTags(st, k.kvsTestARN("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := kvsTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range kvsTestKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.kvsTestARN("tagged"))
		}
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanKinesisVideo_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newKVSTestCalls()
	handlers := kvsTestHandlers(calls)
	empty := map[string]any{
		"ListStreams":           &kinesisvideo.ListStreamsOutput{},
		"ListSignalingChannels": &kinesisvideo.ListSignalingChannelsOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.kvsTestRecord(op, in)
			return out, nil
		}
	}

	total, inserted, err := kvsTestScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range kvsTestKinds() {
		if n := calls.kvsTestCount(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
		if n := calls.kvsTestCount(k.tagOp); n != 0 {
			t.Errorf("%s called %d times; want 0", k.tagOp, n)
		}
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently. A denial (each op's
// modeled one) keeps the row untagged and warns once under the tag op with
// the region's scope; a throttle still failing after the retries keeps it
// and is transient; any other error keeps it and is an error, a failed
// second tag page and a repeated tag NextToken included.
func TestScanKinesisVideo_TagReadFailures(t *testing.T) {
	t.Parallel()
	type tagFailure struct {
		name      string
		op        string
		page2     bool
		repeat    bool
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}
	var cases []tagFailure
	for _, k := range kvsTestKinds() {
		cases = append(cases,
			tagFailure{k.kind + "/not found", k.op, false, false, kvsTestGone, -1, "", kvsTestTotal - 1, false, false},
			tagFailure{k.kind + "/denied", k.op, false, false, k.denied, -1, "", kvsTestTotal, true, true},
			tagFailure{k.kind + "/transient", k.op, false, false, kvsTestThrottle, outcomeTransient, "ClientLimitExceededException", kvsTestTotal, true, false},
			tagFailure{k.kind + "/other error", k.op, false, false, kvsTestBad, outcomeError, "InvalidArgumentException", kvsTestTotal, true, false},
			tagFailure{k.kind + "/second tag page fails", k.op, true, false, kvsTestBad, outcomeError, "InvalidArgumentException", kvsTestTotal, true, false},
			tagFailure{k.kind + "/repeated tag token", k.op, false, true, nil, outcomeError, "", kvsTestTotal, true, false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newKVSTestCalls()
			k := kvsTestKindOf(t, c.op)
			arn := k.kvsTestARN("tagged")
			switch {
			case c.repeat:
				calls.repeatTagToken[arn] = true
			case c.page2:
				calls.tagErrs[arn+"#2"] = c.err
			default:
				calls.tagErrs[arn] = c.err
			}

			total, _, err := kvsTestScan(context.Background(), st, kvsTestHandlers(calls))
			switch {
			case c.outcome < 0:
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			case c.repeat:
				assertGlueOutcome(t, err, c.outcome)
				if !errors.Is(err, errRepeatedNextToken) {
					t.Errorf("err = %v; want errRepeatedNextToken", err)
				}
			default:
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
			if _, ok := rows[k.kvsTestARN("bare")]; !ok {
				t.Errorf("sibling %s missing", k.kvsTestARN("bare"))
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{k.tagLabel}
			}
			if got := afWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != kvsTestScope {
					t.Errorf("warning Scope %q; want %s", x.Scope, kvsTestScope)
				}
			}
		})
	}
}

// A throttled tag read beside a hard tag-read failure, of the same op or of
// the other one, is demoted to one warning under its op, so the hard error
// decides the outcome. Both stream rows are kept untagged.
func TestScanKinesisVideo_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	streams := kvsTestKindOf(t, "ListStreams")
	for _, c := range []struct {
		name string
		hard kvsTestKind
	}{
		{"same op", streams},
		{"other op", kvsTestKindOf(t, "ListSignalingChannels")},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newKVSTestCalls()
			calls.tagErrs[streams.kvsTestARN("bare")] = kvsTestThrottle
			calls.tagErrs[c.hard.kvsTestARN("tagged")] = kvsTestBad

			_, _, err := kvsTestScan(context.Background(), st, kvsTestHandlers(calls))
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidArgumentException")
			if got, want := afWarnOps(warns()), []string{"kinesis-video:ListTagsForStream"}; !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
			rows := rowsByNativeID(t, st, TypeKinesisVideoStream)
			for _, suffix := range []string{"bare", "tagged"} {
				arn := streams.kvsTestARN(suffix)
				r, ok := rows[arn]
				switch {
				case !ok:
					t.Errorf("%s missing", arn)
				case suffix == "bare" || c.hard.op == streams.op:
					if r.TagsJSON != nil {
						t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
					}
				}
			}
		})
	}
}

// The SDK retryer does not retry ClientLimitExceededException; the client
// the scanner builds does, and only that code, while keeping the retryer and
// attempt budget the account config sets.
func TestKVSClientOptions_RetriesThrottle(t *testing.T) {
	t.Parallel()
	throttled := &kvstypes.ClientLimitExceededException{Message: sdkaws.String("slow down")}
	if retry.NewStandard().IsErrorRetryable(throttled) {
		t.Fatal("the SDK's standard retryer retries ClientLimitExceededException; kvsClientOptions' retryer wrap is redundant")
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
			o := kinesisvideo.NewFromConfig(tc.cfg, kvsClientOptions(testRegion)).Options()
			if got := o.Retryer.MaxAttempts(); got != tc.wantAttempts {
				t.Errorf("MaxAttempts = %d, want %d", got, tc.wantAttempts)
			}
			if !o.Retryer.IsErrorRetryable(throttled) {
				t.Error("kinesis-video client does not retry ClientLimitExceededException")
			}
			if o.Retryer.IsErrorRetryable(&kvstypes.InvalidArgumentException{Message: sdkaws.String("bad")}) {
				t.Error("kinesis-video client retries InvalidArgumentException")
			}
			if o.Region != testRegion {
				t.Errorf("region = %q, want %q", o.Region, testRegion)
			}
		})
	}
}

// The client scanKinesisVideo builds retries ClientLimitExceededException,
// on a listing and on a tag read of its fan-out: each answered once with the
// throttle and then successfully stores its row, tagged, with no error and
// no warning. lmRetryingConfig answers below the Retry middleware, which
// opStubConfig's answers never reach.
func TestScanKinesisVideo_ThrottleRetriedByScanClient(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	streams := kvsTestKindOf(t, "ListStreams")
	arn := streams.kvsTestARN("tagged")
	var listCalls, tagCalls atomic.Int32
	throttled := &kvstypes.ClientLimitExceededException{Message: sdkaws.String("slow down")}
	acct := newTestAccount(testAccountID)
	acct.cfg = lmRetryingConfig(map[string]func(any) (any, error){
		"ListStreams": func(any) (any, error) {
			if listCalls.Add(1) == 1 {
				return nil, throttled
			}
			return streams.kvsTestPage(streams.kvsTestElement(sdkaws.String(arn), "tagged"), nil), nil
		},
		"ListSignalingChannels": func(any) (any, error) { return &kinesisvideo.ListSignalingChannelsOutput{}, nil },
		"ListTagsForStream": func(any) (any, error) {
			if tagCalls.Add(1) == 1 {
				return nil, throttled
			}
			return &kinesisvideo.ListTagsForStreamOutput{Tags: map[string]string{"k": "v"}}, nil
		},
	})

	total, _, err := scanKinesisVideo(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1", total)
	}
	if l, tg := listCalls.Load(), tagCalls.Load(); l != 2 || tg != 2 {
		t.Errorf("ListStreams, ListTagsForStream attempts = %d, %d; want 2, 2", l, tg)
	}
	r, ok := rowsByNativeID(t, st, streams.typ)[arn]
	if !ok {
		t.Fatalf("%s not stored", arn)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// Every call goes to the scanned region, whatever region the account's
// config names.
func TestScanKinesisVideo_CallsTheScannedRegion(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var regions parentCalls
	cfg := opStubConfig(kvsTestHandlers(newKVSTestCallsIn("aws", "eu-west-1")), kvsTestKinds()[0].denied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, _, err := scanKinesisVideo(context.Background(), acct, "eu-west-1", st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != kvsTestTotal {
		t.Errorf("total = %d; want %d", total, kvsTestTotal)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), and the channel phase does not run.
func TestScanKinesisVideo_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeKinesisVideoStream)
	calls := newKVSTestCalls()
	streams := kvsTestKindOf(t, "ListStreams")
	calls.tagErrs[streams.kvsTestARN("tagged")] = streams.denied

	total, _, err := kvsTestScan(context.Background(), st, kvsTestHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.kvsTestCount("ListSignalingChannels"); n != 0 {
		t.Errorf("ListSignalingChannels called %d times; want 0 after the failed write", n)
	}
	if total != 0 {
		t.Errorf("total = %d; want 0", total)
	}
}

// A denied tag read is still warned about when a later phase fails: its row
// was stored, so the scan's error does not hide why it has no tags.
func TestScanKinesisVideo_TagDenialWarnedBesideLaterFailure(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		fail func(t *testing.T, st *store.Store, calls *kvsTestCalls, handlers map[string]func(any) (any, error))
		want func(t *testing.T, err error)
	}{
		{"later listing page fails", func(t *testing.T, _ *store.Store, calls *kvsTestCalls, handlers map[string]func(any) (any, error)) {
			list := handlers["ListSignalingChannels"]
			handlers["ListSignalingChannels"] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.kvsTestRecord("ListSignalingChannels", in)
					return nil, kvsTestBad
				}
				return list(in)
			}
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidArgumentException")
		}},
		{"later tag read fails", func(t *testing.T, _ *store.Store, calls *kvsTestCalls, _ map[string]func(any) (any, error)) {
			calls.tagErrs[kvsTestKindOf(t, "ListSignalingChannels").kvsTestARN("tagged")] = kvsTestBad
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidArgumentException")
		}},
		{"later write fails", func(t *testing.T, st *store.Store, _ *kvsTestCalls, _ map[string]func(any) (any, error)) {
			refuseStoreWrites(t, st, TypeKinesisVideoSignalingChannel)
		}, func(t *testing.T, err error) {
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newKVSTestCalls()
			streams := kvsTestKindOf(t, "ListStreams")
			denied := streams.kvsTestARN("bare")
			calls.tagErrs[denied] = streams.denied
			handlers := kvsTestHandlers(calls)
			c.fail(t, st, calls, handlers)

			_, _, err := kvsTestScan(context.Background(), st, handlers)
			c.want(t, err)
			w := warns()
			if got := afWarnOps(w); !slices.Equal(got, []string{"kinesis-video:ListTagsForStream"}) {
				t.Fatalf("warnings = %v; want one under kinesis-video:ListTagsForStream", w)
			}
			if x := w[0]; x.Scope != kvsTestScope || !strings.Contains(x.Message, "first "+denied) {
				t.Errorf("warning = %+v; want Scope %s, keyed %s", x, kvsTestScope, denied)
			}
			r, ok := rowsByNativeID(t, st, TypeKinesisVideoStream)[denied]
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
func TestScanKinesisVideo_ListFailureKeepsPagesAndOtherPhase(t *testing.T) {
	t.Parallel()
	listDenied := apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: kinesisvideo:List")
	type listFailure struct {
		name      string
		op        string
		err       error
		laterPage bool
		wantKept  int
		wantWarn  bool
	}
	var cases []listFailure
	for _, k := range kvsTestKinds() {
		cases = append(cases,
			listFailure{"later page fails/" + k.op, k.op, kvsTestBad, true, 1, false},
			listFailure{"first page fails/" + k.op, k.op, kvsTestBad, false, 0, false},
			listFailure{"denied/" + k.op, k.op, listDenied, false, 0, true})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newKVSTestCalls()
			handlers := kvsTestHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if later := bacField[*string](in, "NextToken") != nil; later == c.laterPage {
					calls.kvsTestRecord(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := kvsTestScan(context.Background(), st, handlers)
			if c.wantWarn {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "InvalidArgumentException")
			}
			var wantWarn []string
			if c.wantWarn {
				wantWarn = []string{"kinesis-video:" + c.op}
			}
			if got := afWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := kvsTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			for _, k := range kvsTestKinds() {
				rows := rowsByNativeID(t, st, k.typ)
				if k.op != c.op {
					if len(rows) != 2 {
						t.Errorf("%s rows = %d; want 2, its phase run", k.typ, len(rows))
					}
					continue
				}
				if _, ok := rows[k.kvsTestARN("tagged")]; ok != (c.wantKept == 1) {
					t.Errorf("%s stored = %v; want %v", k.kvsTestARN("tagged"), ok, c.wantKept == 1)
				}
				if _, ok := rows[k.kvsTestARN("bare")]; ok {
					t.Errorf("%s stored; its page failed", k.kvsTestARN("bare"))
				}
			}
		})
	}
}

// A repeated listing NextToken ends the listing as an error, keeping its
// pages, and the later phase runs.
func TestScanKinesisVideo_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newKVSTestCalls()
	handlers := kvsTestHandlers(calls)
	twoPages := handlers["ListStreams"]
	handlers["ListStreams"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*kinesisvideo.ListStreamsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := kvsTestScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != kvsTestTotal {
		t.Errorf("total = %d; want %d", total, kvsTestTotal)
	}
	if n := calls.kvsTestCount("ListStreams"); n != 2 {
		t.Errorf("ListStreams called %d times; want 2", n)
	}
	if n := len(rowsByNativeID(t, st, TypeKinesisVideoSignalingChannel)); n != 2 {
		t.Errorf("signaling channels = %d; want 2", n)
	}
}

// An element listed without an ARN is not stored, not read, and warned about
// once under its listing with the region's scope, keyed by its name; the
// rest of the listing is stored.
func TestScanKinesisVideo_ElementWithoutARN(t *testing.T) {
	t.Parallel()
	for _, k := range kvsTestKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newKVSTestCalls()
			handlers := kvsTestHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.kvsTestRecord(k.op, in)
					return k.kvsTestPage(k.kvsTestElement(nil, "noarn"), nil), nil
				}
				return twoPages(in)
			}

			total, _, err := kvsTestScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := kvsTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an ARN", k.typ, n)
			}
			if got := calls.kvsTestSortedTagARNs(k.tagOp); !slices.Equal(got, []string{k.kvsTestARN("tagged")}) {
				t.Errorf("%s ARNs = %v; want only %s", k.tagOp, got, k.kvsTestARN("tagged"))
			}
			w := warns()
			if got := afWarnOps(w); !slices.Equal(got, []string{"kinesis-video:" + k.op}) {
				t.Fatalf("warnings = %v; want one under kinesis-video:%s", w, k.op)
			}
			if x := w[0]; x.Scope != kvsTestScope || !strings.HasSuffix(x.Message, "first "+k.kvsTestName("noarn")) {
				t.Errorf("warning = %+v; want Scope %s, keyed %q", x, kvsTestScope, k.kvsTestName("noarn"))
			}
		})
	}
}

// In GovCloud and China the listed ARN of every row is its NativeID and
// reaches its tag op unchanged.
func TestScanKinesisVideo_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newKVSTestCallsIn(pf.partition, pf.region)
			if _, _, err := kvsTestScanAs(context.Background(), partitionAccount(pf.partition), pf.region, st, kvsTestHandlers(calls)); err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, k := range kvsTestKinds() {
				want := []string{k.kvsTestARNIn(pf.partition, pf.region, "bare"), k.kvsTestARNIn(pf.partition, pf.region, "tagged")}
				if got := calls.kvsTestSortedTagARNs(k.tagOp); !slices.Equal(got, want) {
					t.Errorf("%s ARNs = %v\nwant %v", k.tagOp, got, want)
				}
				arn := k.kvsTestARNIn(pf.partition, pf.region, "tagged")
				assertTagsMap(t, rowsByNativeID(t, st, k.typ)[arn].TagsJSON, k.kvsTestTags())
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing; the later phase is not run, and the rows
// written before the stop stay stored.
func TestScanKinesisVideo_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newKVSTestCalls()
	handlers := kvsTestHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListStreams"]
	handlers["ListStreams"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := kvsTestScan(ctx, st, handlers)
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
	if n := calls.kvsTestCount("ListSignalingChannels"); n != 0 {
		t.Errorf("ListSignalingChannels called %d times after ctx ended", n)
	}
	streams := kvsTestKindOf(t, "ListStreams")
	if rows := rowsByNativeID(t, st, TypeKinesisVideoStream); len(rows) != 1 || !kvsStoredWithTags(st, streams.kvsTestARN("tagged")) {
		t.Errorf("streams = %v; want only the first page's, with its tags", rows)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and the later phase is not run.
func TestScanKinesisVideo_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "kinesisvideo.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after the other stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := kvsTestHandlers(newKVSTestCalls())
		handlers["ListSignalingChannels"] = func(any) (any, error) { return nil, nx }
		total, _, err := kvsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := kvsTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d streams counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newKVSTestCalls()
		calls.tagErrs[kvsTestKindOf(t, "ListStreams").kvsTestARN("tagged")] = nx
		_, _, err := kvsTestScan(context.Background(), st, kvsTestHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newKVSTestCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range kvsTestKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.kvsTestRecord(k.op, in)
				return nil, nx
			}
		}
		_, _, err := kvsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.kvsTestCount("ListSignalingChannels"); n != 0 {
			t.Errorf("ListSignalingChannels called %d times; want 0 once the region proved unavailable", n)
		}
	})
}
