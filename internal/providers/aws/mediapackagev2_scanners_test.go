package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mediapackagev2"
	mp2types "github.com/aws/aws-sdk-go-v2/service/mediapackagev2/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// mp2TestTotal is every row of the fixture: two channel groups; two
	// channels in each group, each with a policy; two origin endpoints in
	// each of the four channels, each with a policy. Every listing returns
	// one element per page.
	mp2TestTotal = 2 + 2*4 + 2*8
	mp2TestDoc   = `{"Version":"2012-10-17"}`
)

var (
	mp2TestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: mediapackagev2:List")
	mp2TestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	mp2TestBad      = apiErr("ValidationException", "bad")
	mp2TestGone     = apiErr("ResourceNotFoundException", "gone")
	// mp2TestTime is every listed element's CreatedAt and ModifiedAt, which
	// the rows do not carry.
	mp2TestTime = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

func mp2TestGroupARN(group string) string {
	return "arn:aws:mediapackagev2:" + testRegion + ":" + testAccountID + ":channelGroup/" + group
}

// mp2TestPathARN is the ARN of the resource a call's input names by its
// group, channel and origin endpoint names.
func mp2TestPathARN(in any) string {
	arn := mp2TestGroupARN(sv(bacField[*string](in, "ChannelGroupName")))
	if c := bacField[*string](in, "ChannelName"); c != nil {
		arn += "/channel/" + *c
	}
	if e := bacField[*string](in, "OriginEndpointName"); e != nil {
		arn += "/originEndpoint/" + *e
	}
	return arn
}

// mp2TestPath is the names a call's input sends, joined by "/".
func mp2TestPath(in any) string {
	var parts []string
	for _, f := range []string{"ChannelGroupName", "ChannelName", "OriginEndpointName"} {
		if n := bacField[*string](in, f); n != nil {
			parts = append(parts, *n)
		}
	}
	return strings.Join(parts, "/")
}

// mp2Kind is one listing whose rows have their tags read: the List op, the
// type it stores, the ARN segment of its rows, the short name its rows are
// named by, the listing its parent comes from ("" for none), and page, which
// builds the op's output holding the one element named name and next.
type mp2Kind struct {
	op, typ, seg, short, parentOp string
	page                          func(arn, name string, next *string) any
}

func mp2Kinds() []mp2Kind {
	return []mp2Kind{
		{"ListChannelGroups", TypeMediaPackageV2ChannelGroup, "channelGroup", "g", "", func(arn, name string, next *string) any {
			return &mediapackagev2.ListChannelGroupsOutput{NextToken: next, Items: []mp2types.ChannelGroupListConfiguration{{Arn: &arn, ChannelGroupName: &name, Description: &name, CreatedAt: sdkaws.Time(mp2TestTime), ModifiedAt: sdkaws.Time(mp2TestTime)}}}
		}},
		{"ListChannels", TypeMediaPackageV2Channel, "channel", "ch", "ListChannelGroups", func(arn, name string, next *string) any {
			return &mediapackagev2.ListChannelsOutput{NextToken: next, Items: []mp2types.ChannelListConfiguration{{Arn: &arn, ChannelName: &name, Description: &name, CreatedAt: sdkaws.Time(mp2TestTime), ModifiedAt: sdkaws.Time(mp2TestTime)}}}
		}},
		{"ListOriginEndpoints", TypeMediaPackageV2OriginEndpoint, "originEndpoint", "oe", "ListChannels", func(arn, name string, next *string) any {
			return &mediapackagev2.ListOriginEndpointsOutput{NextToken: next, Items: []mp2types.OriginEndpointListConfiguration{{Arn: &arn, OriginEndpointName: &name, Description: &name, CreatedAt: sdkaws.Time(mp2TestTime), ModifiedAt: sdkaws.Time(mp2TestTime)}}}
		}},
	}
}

func mp2KindOf(t *testing.T, op string) mp2Kind {
	t.Helper()
	for _, k := range mp2Kinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no MediaPackage v2 listing", op)
	return mp2Kind{}
}

// arnUnder is the ARN of the row named k.short-suffix listed under parent
// (an ARN; ignored for a channel group).
func (k mp2Kind) arnUnder(parent, suffix string) string {
	if k.parentOp == "" {
		return mp2TestGroupARN(k.short + "-" + suffix)
	}
	return parent + "/" + k.seg + "/" + k.short + "-" + suffix
}

// arnsOf is the ARN of every row k's listing returns in the fixture.
func (k mp2Kind) arnsOf(t *testing.T) []string {
	parents := []string{""}
	if k.parentOp != "" {
		parents = mp2KindOf(t, k.parentOp).arnsOf(t)
	}
	var arns []string
	for _, p := range parents {
		arns = append(arns, k.arnUnder(p, "tagged"), k.arnUnder(p, "bare"))
	}
	return arns
}

// mp2Calls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type mp2Calls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	paths      map[string][]string
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newMP2Calls tags every "-tagged" row with its type.
func newMP2Calls(t *testing.T) *mp2Calls {
	c := &mp2Calls{maxResults: map[string][]int32{}, paths: map[string][]string{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range mp2Kinds() {
		for _, arn := range k.arnsOf(t) {
			if strings.HasSuffix(arn, "-tagged") {
				c.tags[arn] = map[string]string{"type": k.typ}
			}
		}
	}
	return c
}

// recordMP2 notes op's MaxResults (0 when unset) and, on a first page, the
// path its input names.
func (c *mp2Calls) recordMP2(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := bacField[*int32](in, "MaxResults")
	if n == nil {
		n = sdkaws.Int32(0)
	}
	c.maxResults[op] = append(c.maxResults[op], *n)
	if bacField[*string](in, "NextToken") == nil {
		c.paths[op] = append(c.paths[op], mp2TestPath(in))
	}
}

// calledMP2 is the number of op calls.
func (c *mp2Calls) calledMP2(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op])
}

// pathsMP2 is the paths op was called for, sorted.
func (c *mp2Calls) pathsMP2(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.paths[op]))
}

func (c *mp2Calls) tagARNsMP2() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *mp2Calls) listTagsMP2(in any) (any, error) {
	arn := sv(in.(*mediapackagev2.ListTagsForResourceInput).ResourceArn)
	c.recordMP2("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &mediapackagev2.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// twoPagesMP2 answers k's op with "tagged" on the first page and "bare" on
// the second, under the parent its input names.
func (c *mp2Calls) twoPagesMP2(k mp2Kind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.recordMP2(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(k.arnUnder(mp2TestPathARN(in), suffix), k.short+"-"+suffix, next), nil
	}
}

// mp2Handlers answers every op the scan calls: each listing in two pages,
// every policy read with mp2TestDoc, and the tag reads from c.
func mp2Handlers(c *mp2Calls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTagsMP2,
		"GetChannelPolicy": func(in any) (any, error) {
			c.recordMP2("GetChannelPolicy", in)
			return &mediapackagev2.GetChannelPolicyOutput{ChannelGroupName: bacField[*string](in, "ChannelGroupName"), ChannelName: bacField[*string](in, "ChannelName"), Policy: sdkaws.String(mp2TestDoc)}, nil
		},
		"GetOriginEndpointPolicy": func(in any) (any, error) {
			c.recordMP2("GetOriginEndpointPolicy", in)
			return &mediapackagev2.GetOriginEndpointPolicyOutput{ChannelGroupName: bacField[*string](in, "ChannelGroupName"), ChannelName: bacField[*string](in, "ChannelName"), OriginEndpointName: bacField[*string](in, "OriginEndpointName"), Policy: sdkaws.String(mp2TestDoc)}, nil
		},
	}
	for _, k := range mp2Kinds() {
		h[k.op] = c.twoPagesMP2(k)
	}
	return h
}

// mp2ScanIn runs the scan's real entry point for acct in region against
// handlers; every op they do not answer is denied.
func mp2ScanIn(ctx context.Context, st *store.Store, acct *account, region string, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct.cfg = opStubConfig(handlers, mp2TestDenied)
	return scanMediaPackageV2(ctx, acct, region, st, testScanID)
}

func mp2Scan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	return mp2ScanIn(ctx, st, newTestAccount(testAccountID), testRegion, handlers)
}

// mp2AllTaggedARNs is the ARN of every row whose tags are read, sorted.
func mp2AllTaggedARNs(t *testing.T) []string {
	var arns []string
	for _, k := range mp2Kinds() {
		arns = append(arns, k.arnsOf(t)...)
	}
	slices.Sort(arns)
	return arns
}

// mp2WarnOps is the op of each warning, in order.
func mp2WarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// failMP2 makes op fail with err for the calls whose input names path,
// recording them; the others are answered as before.
func failMP2(calls *mp2Calls, handlers map[string]func(in any) (any, error), op, path string, err error) {
	answer := handlers[op]
	handlers[op] = func(in any) (any, error) {
		if mp2TestPath(in) == path {
			calls.recordMP2(op, in)
			return nil, err
		}
		return answer(in)
	}
}

// Every listing stores both its pages, for every parent, each row under its
// ARN, named by its name, its attributes the listed element, with no Status
// or CreatedAt, and with the tags its tag read returns (nil for none). Each
// channel and origin endpoint has its policy stored under its ARN +
// "/policy", named by its parent's name, untagged. ListTagsForResource is
// sent exactly the ARNs of the channel groups, channels and origin
// endpoints; every listing is sent the model's MaxResults cap of 100. Each
// child call is sent the names of a parent stored this scan. No error, no
// warning.
func TestScanMediaPackageV2_StoresEveryRowWithItsTags(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMP2Calls(t)

	total, inserted, err := mp2Scan(context.Background(), st, mp2Handlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mp2TestTotal || inserted != mp2TestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, mp2TestTotal, mp2TestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range mp2Kinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != len(k.arnsOf(t)) {
			t.Errorf("%s rows = %d; want %d", k.typ, len(rows), len(k.arnsOf(t)))
		}
		for _, arn := range k.arnsOf(t) {
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			wantName := arn[strings.LastIndex(arn, "/")+1:]
			if sv(r.Name) != wantName || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %v, %v; want %q, %q, nil, nil", arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, wantName, testRegion)
			}
			var attrs struct{ Arn, Description string }
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Arn != arn || attrs.Description != wantName {
				t.Errorf("%s attrs = %s (%v); want the listed element", arn, r.AttributesJSON, err)
			}
			if strings.HasSuffix(arn, "-tagged") {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	for typ, parentOp := range map[string]string{
		TypeMediaPackageV2ChannelPolicy:        "ListChannels",
		TypeMediaPackageV2OriginEndpointPolicy: "ListOriginEndpoints",
	} {
		rows := rowsByNativeID(t, st, typ)
		parents := mp2KindOf(t, parentOp).arnsOf(t)
		if len(rows) != len(parents) {
			t.Errorf("%s rows = %d; want %d", typ, len(rows), len(parents))
		}
		for _, parent := range parents {
			r, ok := rows[parent+"/policy"]
			if !ok {
				t.Errorf("%s %s/policy missing", typ, parent)
				continue
			}
			wantName := parent[strings.LastIndex(parent, "/")+1:]
			var attrs struct{ Policy string }
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Policy != mp2TestDoc {
				t.Errorf("%s attrs = %s (%v); want the policy read", parent, r.AttributesJSON, err)
			}
			if sv(r.Name) != wantName || sv(r.Region) != testRegion || r.TagsJSON != nil {
				t.Errorf("%s/policy Name, Region, TagsJSON = %q, %q, %v; want %q, %q, nil", parent, sv(r.Name), sv(r.Region), r.TagsJSON, wantName, testRegion)
			}
		}
	}
	if got := calls.tagARNsMP2(); !slices.Equal(got, mp2AllTaggedARNs(t)) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, mp2AllTaggedARNs(t))
	}
	perChannel := []string{"g-bare/ch-bare", "g-bare/ch-tagged", "g-tagged/ch-bare", "g-tagged/ch-tagged"}
	var perEndpoint []string
	for _, c := range perChannel {
		perEndpoint = append(perEndpoint, c+"/oe-bare", c+"/oe-tagged")
	}
	for op, want := range map[string][]string{
		"ListChannels":            {"g-bare", "g-tagged"},
		"GetChannelPolicy":        perChannel,
		"ListOriginEndpoints":     perChannel,
		"GetOriginEndpointPolicy": perEndpoint,
	} {
		if got := calls.pathsMP2(op); !slices.Equal(got, want) {
			t.Errorf("%s paths = %v; want %v", op, got, want)
		}
	}
	for _, k := range mp2Kinds() {
		if calls.calledMP2(k.op) == 0 {
			t.Errorf("%s not called", k.op)
		}
		for _, n := range calls.maxResults[k.op] {
			if n != 100 {
				t.Errorf("%s MaxResults = %d; want 100", k.op, n)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanMediaPackageV2_RescanInsertsNone(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := mp2Scan(context.Background(), st, mp2Handlers(newMP2Calls(t))); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := mp2Scan(context.Background(), st, mp2Handlers(newMP2Calls(t)))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != mp2TestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, mp2TestTotal)
	}
}

// No channel group listed: nothing stored, no child call or tag read, no
// error. An empty listing warns nothing; a denied one warns once under its
// op.
func TestScanMediaPackageV2_NoChannelGroups(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		err       error
		wantWarns []string
	}{
		{"empty", nil, nil},
		{"denied", mp2TestDenied, []string{"mediapackagev2:ListChannelGroups"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newMP2Calls(t)
			handlers := mp2Handlers(calls)
			handlers["ListChannelGroups"] = func(in any) (any, error) {
				calls.recordMP2("ListChannelGroups", in)
				if c.err != nil {
					return nil, c.err
				}
				return &mediapackagev2.ListChannelGroupsOutput{}, nil
			}

			total, inserted, err := mp2Scan(context.Background(), st, handlers)
			if err != nil || total != 0 || inserted != 0 {
				t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
			}
			if got := mp2WarnOps(warns()); !slices.Equal(got, c.wantWarns) {
				t.Errorf("warnings = %v; want %v", got, c.wantWarns)
			}
			for _, op := range []string{"ListChannels", "GetChannelPolicy", "ListOriginEndpoints", "GetOriginEndpointPolicy", "ListTagsForResource"} {
				if n := calls.calledMP2(op); n != 0 {
					t.Errorf("%s called %d times; want 0", op, n)
				}
			}
		})
	}
}

// A failed tag read keeps the row untagged whatever the error:
// ListTagsForResource models no not-found code, so even
// ResourceNotFoundException is an error, not a dropped row. A denial warns
// once under the tag op with the scan's scope; a throttle is transient; any
// other error is an error. A channel group whose tags were not read still
// has its channels listed.
func TestScanMediaPackageV2_TagReadFailures(t *testing.T) {
	t.Parallel()
	channel := mp2KindOf(t, "ListChannels").arnsOf(t)[0]
	group := mp2TestGroupARN("g-tagged")
	for _, c := range []struct {
		name     string
		arn, typ string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		warn     bool
	}{
		{"channel/denied", channel, TypeMediaPackageV2Channel, mp2TestDenied, -1, "", true},
		{"channel/transient", channel, TypeMediaPackageV2Channel, mp2TestThrottle, outcomeTransient, "ThrottlingException", false},
		{"channel/other error", channel, TypeMediaPackageV2Channel, mp2TestBad, outcomeError, "ValidationException", false},
		{"channel/unmodeled not found", channel, TypeMediaPackageV2Channel, mp2TestGone, outcomeError, "ResourceNotFoundException", false},
		{"group/denied", group, TypeMediaPackageV2ChannelGroup, mp2TestDenied, -1, "", true},
		{"group/unmodeled not found", group, TypeMediaPackageV2ChannelGroup, mp2TestGone, outcomeError, "ResourceNotFoundException", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newMP2Calls(t)
			calls.errs[c.arn] = c.err

			total, _, err := mp2Scan(context.Background(), st, mp2Handlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != mp2TestTotal {
				t.Errorf("total = %d; want %d", total, mp2TestTotal)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if !ok {
				t.Fatal("row not stored; want it kept")
			}
			if r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{mp2TagsOp}
			}
			if got := mp2WarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, c.arn) {
					t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", x.Scope, x.Message, testAccountID, testRegion, c.arn)
				}
			}
		})
	}
}

// Denied tag reads in two phases send one warning for the whole scan; the
// rows are kept.
func TestScanMediaPackageV2_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMP2Calls(t)
	for _, op := range []string{"ListChannels", "ListOriginEndpoints"} {
		for _, arn := range mp2KindOf(t, op).arnsOf(t) {
			calls.errs[arn] = mp2TestDenied
		}
	}

	total, _, err := mp2Scan(context.Background(), st, mp2Handlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mp2TestTotal {
		t.Errorf("total = %d; want %d", total, mp2TestTotal)
	}
	if got := mp2WarnOps(warns()); !slices.Equal(got, []string{mp2TagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, mp2TagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanMediaPackageV2_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, mp2TagsOp)
	calls := newMP2Calls(t)
	calls.errs[mp2TestGroupARN("g-bare")] = mp2TestThrottle
	calls.errs[mp2KindOf(t, "ListOriginEndpoints").arnsOf(t)[5]] = mp2TestBad

	_, _, err := mp2Scan(context.Background(), st, mp2Handlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", mp2TagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phase's rows
// stay stored.
func TestScanMediaPackageV2_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeMediaPackageV2Channel)
	calls := newMP2Calls(t)
	calls.errs[mp2KindOf(t, "ListChannels").arnsOf(t)[0]] = mp2TestDenied

	total, _, err := mp2Scan(context.Background(), st, mp2Handlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"GetChannelPolicy", "ListOriginEndpoints", "GetOriginEndpointPolicy"} {
		if n := calls.calledMP2(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if total != 2 || len(rowsByNativeID(t, st, TypeMediaPackageV2ChannelGroup)) != 2 {
		t.Errorf("total = %d, channel groups stored = %d; want 2, 2", total, len(rowsByNativeID(t, st, TypeMediaPackageV2ChannelGroup)))
	}
}

// A listing's failed later page keeps the pages read before it, and its
// error does not stop the later phases. A parent not stored has no children
// read.
func TestScanMediaPackageV2_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	channel := mp2TestGroupARN("g-tagged") + "/channel/ch-tagged"
	for _, c := range []struct {
		op, path             string
		kept, lost           string
		wantTotal, wantFinal int
	}{
		// The channel groups' second page: g-bare and its 12 descendants
		// are lost.
		{"ListChannelGroups", "", mp2TestGroupARN("g-tagged"), mp2TestGroupARN("g-bare"), mp2TestTotal - 13, 4},
		// One channel's second page of origin endpoints: oe-bare and its
		// policy are lost.
		{"ListOriginEndpoints", "g-tagged/ch-tagged", channel + "/originEndpoint/oe-tagged", channel + "/originEndpoint/oe-bare", mp2TestTotal - 2, 7},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newMP2Calls(t)
			handlers := mp2Handlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil && mp2TestPath(in) == c.path {
					calls.recordMP2(c.op, in)
					return nil, mp2TestBad
				}
				return list(in)
			}

			total, _, err := mp2Scan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, mp2KindOf(t, c.op).typ)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s, read before the failure, missing", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; its page failed", c.lost)
			}
			if got := len(rowsByNativeID(t, st, TypeMediaPackageV2OriginEndpointPolicy)); got != c.wantFinal {
				t.Errorf("origin endpoint policies (the last phase) = %d; want %d", got, c.wantFinal)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanMediaPackageV2_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newMP2Calls(t)
	handlers := mp2Handlers(calls)
	twoPages := handlers["ListChannels"]
	handlers["ListChannels"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*mediapackagev2.ListChannelsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := mp2Scan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != mp2TestTotal {
		t.Errorf("total = %d; want %d", total, mp2TestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeMediaPackageV2Channel)); n != 4 {
		t.Errorf("channels = %d; want 4", n)
	}
}

// One parent's failed child call costs the other parents none of their
// rows: a parent deleted since it was listed (the modeled
// ResourceNotFoundException) is skipped silently, a denial warns once under
// the op, and any other error is returned. For a policy read,
// ResourceNotFoundException or an empty policy means no policy: no row and
// no warning.
func TestScanMediaPackageV2_PerParentFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op, path string
		// lost is the rows the failed parent's call would have stored, with
		// their descendants.
		lost int
	}{
		{"ListChannels", "g-tagged", 2 + 2 + 4 + 4},
		{"GetChannelPolicy", "g-tagged/ch-tagged", 1},
		{"ListOriginEndpoints", "g-bare/ch-tagged", 2 + 2},
		{"GetOriginEndpointPolicy", "g-bare/ch-bare/oe-tagged", 1},
	} {
		for _, e := range []struct {
			name      string
			err       error
			code      string
			wantWarns int
		}{
			{"parent gone", mp2TestGone, "", 0},
			{"denied", mp2TestDenied, "", 1},
			{"other error", mp2TestBad, "ValidationException", 0},
		} {
			t.Run(c.op+"/"+e.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newMP2Calls(t)
				handlers := mp2Handlers(calls)
				failMP2(calls, handlers, c.op, c.path, e.err)

				total, _, err := mp2Scan(context.Background(), st, handlers)
				if e.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, e.code)
				}
				if want := mp2TestTotal - c.lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				w := warns()
				if len(w) != e.wantWarns || (len(w) == 1 && w[0].Service != "mediapackagev2:"+c.op) {
					t.Errorf("warnings = %v; want %d under mediapackagev2:%s", w, e.wantWarns, c.op)
				}
			})
		}
	}
	t.Run("empty policy", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newMP2Calls(t)
		handlers := mp2Handlers(calls)
		read := handlers["GetChannelPolicy"]
		handlers["GetChannelPolicy"] = func(in any) (any, error) {
			out, err := read(in)
			if mp2TestPath(in) == "g-bare/ch-bare" {
				out.(*mediapackagev2.GetChannelPolicyOutput).Policy = nil
			}
			return out, err
		}

		total, _, err := mp2Scan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if want := mp2TestTotal - 1; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
		if _, ok := rowsByNativeID(t, st, TypeMediaPackageV2ChannelPolicy)[mp2TestGroupARN("g-bare")+"/channel/ch-bare/policy"]; ok {
			t.Error("empty policy stored")
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// An element without an ARN is not stored, is not sent to the tag op, and
// is warned about once under its listing, naming it. A channel group,
// channel or origin endpoint without a name is stored under its ARN and
// tag-read, with one warning naming it. Neither has its children read.
func TestScanMediaPackageV2_ElementWithoutARNOrName(t *testing.T) {
	t.Parallel()
	noNameGroup := mp2TestGroupARN("x")
	noNameChannel := mp2TestGroupARN("g-tagged") + "/channel/x"
	noNameEndpoint := mp2TestGroupARN("g-tagged") + "/channel/ch-tagged/originEndpoint/x"
	for _, c := range []struct {
		name, op, key string
		answer        func(in any) any
		wantTotal     int
		// stored is the ARN of the row stored for the element; "" for none
		// of typ stored.
		stored, typ string
		// childOps are the ops the element's children are read by.
		childOps []string
	}{
		{
			"channel without an ARN", "ListChannels", "ch-x",
			func(in any) any {
				if mp2TestPath(in) != "g-tagged" {
					return &mediapackagev2.ListChannelsOutput{}
				}
				return &mediapackagev2.ListChannelsOutput{Items: []mp2types.ChannelListConfiguration{{ChannelName: sdkaws.String("ch-x")}}}
			},
			// Only the channel groups are stored.
			2, "", TypeMediaPackageV2Channel,
			[]string{"GetChannelPolicy", "ListOriginEndpoints"},
		},
		{
			"channel without a name", "ListChannels", noNameChannel,
			func(in any) any {
				if mp2TestPath(in) != "g-tagged" {
					return &mediapackagev2.ListChannelsOutput{}
				}
				return &mediapackagev2.ListChannelsOutput{Items: []mp2types.ChannelListConfiguration{{Arn: sdkaws.String(noNameChannel)}}}
			},
			// The channel groups and that channel.
			2 + 1, noNameChannel, TypeMediaPackageV2Channel,
			[]string{"GetChannelPolicy", "ListOriginEndpoints"},
		},
		{
			"channel group without a name", "ListChannelGroups", noNameGroup,
			func(any) any {
				return &mediapackagev2.ListChannelGroupsOutput{Items: []mp2types.ChannelGroupListConfiguration{{Arn: sdkaws.String(noNameGroup)}}}
			},
			1, noNameGroup, TypeMediaPackageV2ChannelGroup,
			[]string{"ListChannels"},
		},
		{
			"origin endpoint without an ARN", "ListOriginEndpoints", "oe-x",
			func(in any) any {
				if mp2TestPath(in) != "g-tagged/ch-tagged" {
					return &mediapackagev2.ListOriginEndpointsOutput{}
				}
				return &mediapackagev2.ListOriginEndpointsOutput{Items: []mp2types.OriginEndpointListConfiguration{{OriginEndpointName: sdkaws.String("oe-x")}}}
			},
			// The groups, channels and channel policies.
			2 + 4 + 4, "", TypeMediaPackageV2OriginEndpoint,
			[]string{"GetOriginEndpointPolicy"},
		},
		{
			"origin endpoint without a name", "ListOriginEndpoints", noNameEndpoint,
			func(in any) any {
				if mp2TestPath(in) != "g-tagged/ch-tagged" {
					return &mediapackagev2.ListOriginEndpointsOutput{}
				}
				return &mediapackagev2.ListOriginEndpointsOutput{Items: []mp2types.OriginEndpointListConfiguration{{Arn: sdkaws.String(noNameEndpoint)}}}
			},
			// The groups, channels and channel policies, and that endpoint.
			2 + 4 + 4 + 1, noNameEndpoint, TypeMediaPackageV2OriginEndpoint,
			[]string{"GetOriginEndpointPolicy"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newMP2Calls(t)
			handlers := mp2Handlers(calls)
			handlers[c.op] = func(in any) (any, error) {
				calls.recordMP2(c.op, in)
				return c.answer(in), nil
			}

			total, _, err := mp2Scan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "mediapackagev2:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under mediapackagev2:%s naming %s", w, c.op, c.key)
			}
			if slices.Contains(calls.tagARNsMP2(), "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
			for _, op := range c.childOps {
				if n := calls.calledMP2(op); n != 0 {
					t.Errorf("%s calls = %d, paths %v; want none", op, n, calls.pathsMP2(op))
				}
			}
			rows := rowsByNativeID(t, st, c.typ)
			if c.stored == "" {
				if len(rows) != 0 {
					t.Errorf("%s rows = %d; want 0", c.typ, len(rows))
				}
				return
			}
			if _, ok := rows[c.stored]; !ok || len(rows) != 1 {
				t.Errorf("%s rows = %v; want only %s", c.typ, slices.Sorted(maps.Keys(rows)), c.stored)
			}
			if !slices.Contains(calls.tagARNsMP2(), c.stored) {
				t.Errorf("tag ARNs lack %s", c.stored)
			}
		})
	}
}

// In GovCloud and China the listed ARNs reach the tag op unchanged, and a
// channel's or origin endpoint's policy is stored under its listed ARN +
// "/policy".
func TestScanMediaPackageV2_PartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			groupARN := partitionARN(pf.partition, "mediapackagev2", pf.region, "channelGroup/g")
			channelARN := groupARN + "/channel/c"
			endpointARN := channelARN + "/originEndpoint/e"
			st := newTestStore(t)
			calls := newMP2Calls(t)
			calls.tags[groupARN] = map[string]string{"env": "prod"}
			handlers := mp2Handlers(calls)
			handlers["ListChannelGroups"] = func(in any) (any, error) {
				calls.recordMP2("ListChannelGroups", in)
				return &mediapackagev2.ListChannelGroupsOutput{Items: []mp2types.ChannelGroupListConfiguration{{Arn: sdkaws.String(groupARN), ChannelGroupName: sdkaws.String("g")}}}, nil
			}
			handlers["ListChannels"] = func(in any) (any, error) {
				calls.recordMP2("ListChannels", in)
				return &mediapackagev2.ListChannelsOutput{Items: []mp2types.ChannelListConfiguration{{Arn: sdkaws.String(channelARN), ChannelName: sdkaws.String("c")}}}, nil
			}
			handlers["ListOriginEndpoints"] = func(in any) (any, error) {
				calls.recordMP2("ListOriginEndpoints", in)
				return &mediapackagev2.ListOriginEndpointsOutput{Items: []mp2types.OriginEndpointListConfiguration{{Arn: sdkaws.String(endpointARN), OriginEndpointName: sdkaws.String("e")}}}, nil
			}

			if _, _, err := mp2ScanIn(context.Background(), st, partitionAccount(pf.partition), pf.region, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			got := calls.tagARNsMP2()
			if want := []string{groupARN, channelARN, endpointARN}; !slices.Equal(got, want) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeMediaPackageV2ChannelGroup)[groupARN].TagsJSON, map[string]string{"env": "prod"})
			if _, ok := rowsByNativeID(t, st, TypeMediaPackageV2ChannelPolicy)[channelARN+"/policy"]; !ok {
				t.Errorf("channel policy %s/policy not stored", channelARN)
			}
			if _, ok := rowsByNativeID(t, st, TypeMediaPackageV2OriginEndpointPolicy)[endpointARN+"/policy"]; !ok {
				t.Errorf("origin endpoint policy %s/policy not stored", endpointARN)
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanMediaPackageV2_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newMP2Calls(t)
	handlers := mp2Handlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListChannels"]
	handlers["ListChannels"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := mp2Scan(ctx, st, handlers)
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
	for _, op := range []string{"GetChannelPolicy", "ListOriginEndpoints", "GetOriginEndpointPolicy"} {
		if n := calls.calledMP2(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanMediaPackageV2_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "mediapackagev2.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("last phase after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := mp2Handlers(newMP2Calls(t))
		handlers["GetOriginEndpointPolicy"] = func(any) (any, error) { return nil, nx }
		total, _, err := mp2Scan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := mp2TestTotal - 8; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newMP2Calls(t)
		calls.errs[mp2TestGroupARN("g-bare")] = nx
		_, _, err := mp2Scan(context.Background(), st, mp2Handlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanMediaPackageV2(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
