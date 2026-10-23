package aws

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/synthetics"
	syntheticstypes "github.com/aws/aws-sdk-go-v2/service/synthetics/types"

	"github.com/icearp/disco-cli/store"
)

var (
	synTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: synthetics:Describe")
	errSynTest    = errors.New("synthetics test failure")
	// errSynUnexpectedInput answers a request a stub does not expect.
	errSynUnexpectedInput = errors.New("synthetics stub: unexpected request input")
)

const (
	synWebARN    = "arn:aws:synthetics:us-east-1:123456789012:canary:web"
	synAPIARN    = "arn:aws:synthetics:us-east-1:123456789012:canary:api"
	synGroupARN  = "arn:aws:synthetics:us-east-1:123456789012:group:g-1"
	synGroup2ARN = "arn:aws:synthetics:us-east-1:123456789012:group:g-2"
)

// synTaggedCanary is a running, tagged canary; synUntaggedCanary has no tags
// and a status without a state, so its row has no Status.
var (
	synTaggedCanary = syntheticstypes.Canary{
		Id: ptrStr("c-web"), Name: ptrStr("web"), RuntimeVersion: ptrStr("syn-nodejs-puppeteer-9.1"),
		Code:   &syntheticstypes.CanaryCodeOutput{Handler: ptrStr("index.handler"), SourceLocationArn: ptrStr("arn:aws:lambda:us-east-1:123456789012:layer:cwsyn-web:1")},
		Status: &syntheticstypes.CanaryStatus{State: syntheticstypes.CanaryStateRunning},
		Tags:   map[string]string{"env": "prod", "team": ""},
	}
	synUntaggedCanary = syntheticstypes.Canary{Id: ptrStr("c-api"), Name: ptrStr("api"), Status: &syntheticstypes.CanaryStatus{}}
)

// synCanaryPages answers DescribeCanaries with two pages: the tagged canary
// and one without a name, then the untagged canary. A request with any input
// other than the token and the page size cap is refused.
func synCanaryPages(in any) (any, error) {
	req := in.(*synthetics.DescribeCanariesInput)
	if sv32(req.MaxResults) != 20 || req.Names != nil {
		return nil, errSynUnexpectedInput
	}
	switch sv(req.NextToken) {
	case "":
		return &synthetics.DescribeCanariesOutput{
			Canaries:  []syntheticstypes.Canary{synTaggedCanary, {Id: ptrStr("c-noname")}},
			NextToken: ptrStr("p2"),
		}, nil
	case "p2":
		return &synthetics.DescribeCanariesOutput{Canaries: []syntheticstypes.Canary{synUntaggedCanary}}, nil
	}
	return nil, errSynUnexpectedInput
}

// synHandlers answers DescribeCanaries with synCanaryPages and ListGroups with
// one group; every other op is denied.
func synHandlers() map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"DescribeCanaries": synCanaryPages,
		"ListGroups": func(in any) (any, error) {
			req := in.(*synthetics.ListGroupsInput)
			if sv32(req.MaxResults) != 20 || req.NextToken != nil {
				return nil, errSynUnexpectedInput
			}
			return &synthetics.ListGroupsOutput{Groups: []syntheticstypes.GroupSummary{synGroup}}, nil
		},
	}
}

// synGroup is the group synHandlers lists; synGroupPages lists it with an
// element without an Arn, then synGroup2 on a second page.
var (
	synGroup  = syntheticstypes.GroupSummary{Arn: ptrStr(synGroupARN), Id: ptrStr("g-1"), Name: ptrStr("g")}
	synGroup2 = syntheticstypes.GroupSummary{Arn: ptrStr(synGroup2ARN), Id: ptrStr("g-2")}
)

// synGroupPages answers ListGroups with two pages. A request with any input
// other than the token and the page size cap is refused.
func synGroupPages(in any) (any, error) {
	req := in.(*synthetics.ListGroupsInput)
	if sv32(req.MaxResults) != 20 {
		return nil, errSynUnexpectedInput
	}
	switch sv(req.NextToken) {
	case "":
		return &synthetics.ListGroupsOutput{
			Groups:    []syntheticstypes.GroupSummary{synGroup, {Id: ptrStr("g-noarn"), Name: ptrStr("lost")}},
			NextToken: ptrStr("g2"),
		}, nil
	case "g2":
		return &synthetics.ListGroupsOutput{Groups: []syntheticstypes.GroupSummary{synGroup2}}, nil
	}
	return nil, errSynUnexpectedInput
}

// scanSynTopLevel runs scanSynthetics against handlers on a fresh store,
// where every stored row is new, so it fails the test unless inserted equals
// total.
func scanSynTopLevel(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	return scanSynCtx(context.Background(), t, st, ignoreCtx(handlers))
}

func scanSynCtx(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(context.Context, any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(handlers, synTestDenied)
	total, inserted, err := scanSynthetics(ctx, acct, testRegion, st, testScanID)
	if inserted != total {
		t.Errorf("inserted = %d, want total %d", inserted, total)
	}
	return total, err
}

// Each listed canary is stored from its element: NativeID the synthesized
// ARN, Name, Status only when the element has a state, the element verbatim
// as attributes, and its tags. A canary without a name is not stored and is
// warned about once under its id.
func TestScanSynthetics_CanaryRows(t *testing.T) {
	st := newTestStore(t)
	var warns []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service == synDescribeCanariesOp {
			warns = append(warns, w)
		}
	}
	total, err := scanSynTopLevel(t, st, synHandlers())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 2+1 {
		t.Errorf("total = %d, want 3 (2 canaries, 1 group)", total)
	}
	rows := rowsByNativeID(t, st, TypeSyntheticsCanary)
	if len(rows) != 2 {
		t.Fatalf("canary rows = %v, want 2", rows)
	}
	web, api := rows[synWebARN], rows[synAPIARN]
	if sv(web.Name) != "web" || sv(web.Region) != testRegion || sv(web.Status) != "RUNNING" {
		t.Errorf("web row = name %q region %q status %q", sv(web.Name), sv(web.Region), sv(web.Status))
	}
	assertJSONEqual(t, "web attributes", web.AttributesJSON, mustJSON(synTaggedCanary))
	assertTagsMap(t, web.TagsJSON, map[string]string{"env": "prod", "team": ""})
	if sv(api.Name) != "api" || api.Status != nil || api.TagsJSON != nil {
		t.Errorf("api row = name %q status %v tags %v; want api, nil, nil", sv(api.Name), api.Status, api.TagsJSON)
	}
	assertJSONEqual(t, "api attributes", api.AttributesJSON, mustJSON(synUntaggedCanary))
	if len(warns) != 1 || warns[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(warns[0].Message, "c-noname") {
		t.Errorf("warnings = %+v, want one naming c-noname", warns)
	}
	if n := len(rowsByNativeID(t, st, TypeSyntheticsGroup)); n != 1 {
		t.Errorf("group rows = %d, want 1", n)
	}
}

// A failed later page keeps the canaries read before it: a denial warns once
// and is not returned, any other error and a repeated token are returned.
func TestScanSynthetics_CanaryLaterPageFails(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   error
		wantWarns int
	}{
		{"denied", synTestDenied, nil, 1},
		{"other error", errSynTest, errSynTest, 0},
		{"repeated token", nil, errRepeatedNextToken, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, synDescribeCanariesOp)
			h := synHandlers()
			h["DescribeCanaries"] = func(in any) (any, error) {
				if sv(in.(*synthetics.DescribeCanariesInput).NextToken) != "p2" {
					return synCanaryPages(in)
				}
				if c.err != nil {
					return nil, c.err
				}
				return &synthetics.DescribeCanariesOutput{NextToken: ptrStr("p2")}, nil
			}
			total, err := scanSynTopLevel(t, st, h)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != 1+1 {
				t.Errorf("total = %d, want 2 (1 canary, 1 group)", total)
			}
			if *warns != c.wantWarns+1 {
				t.Errorf("warnings = %d, want %d (+1 for c-noname)", *warns, c.wantWarns+1)
			}
			rows := rowsByNativeID(t, st, TypeSyntheticsCanary)
			if _, ok := rows[synWebARN]; !ok || len(rows) != 1 {
				t.Errorf("canary rows = %v, want only %s", rows, synWebARN)
			}
		})
	}
}

// A hard error in either phase is returned, and the other phase still runs.
func TestScanSynthetics_PhaseErrorPropagates(t *testing.T) {
	cases := []struct {
		op                      string
		wantTotal, wantCanaries int
		wantGroups              int
	}{
		{"DescribeCanaries", 1, 0, 1},
		{"ListGroups", 2, 2, 0},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := synHandlers()
			h[c.op] = func(any) (any, error) { return nil, errSynTest }
			total, err := scanSynTopLevel(t, st, h)
			if !errors.Is(err, errSynTest) {
				t.Fatalf("err = %v, want the %s error", err, c.op)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d, want %d", total, c.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, TypeSyntheticsCanary)); n != c.wantCanaries {
				t.Errorf("canary rows = %d, want %d", n, c.wantCanaries)
			}
			if n := len(rowsByNativeID(t, st, TypeSyntheticsGroup)); n != c.wantGroups {
				t.Errorf("group rows = %d, want %d", n, c.wantGroups)
			}
		})
	}
}

// A throttled phase beside the other phase's hard error is demoted to one
// warning under its op, so the scan reads as an error, not as transient.
func TestScanSynthetics_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	for _, c := range []struct{ throttled, hard string }{
		{"DescribeCanaries", "ListGroups"},
		{"ListGroups", "DescribeCanaries"},
	} {
		t.Run(c.throttled, func(t *testing.T) {
			st := newTestStore(t)
			h := synHandlers()
			h[c.throttled] = func(any) (any, error) { return nil, throttle }
			h[c.hard] = func(any) (any, error) { return nil, errSynTest }
			warnings := countOpWarnings(st, "synthetics:"+c.throttled)
			_, err := scanSynTopLevel(t, st, h)
			if !errors.Is(err, errSynTest) {
				t.Fatalf("err = %v, want the %s error", err, c.hard)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warnings != 1 {
				t.Errorf("synthetics:%s warnings = %d, want 1", c.throttled, *warnings)
			}
		})
	}
}

// A throttled canary phase alone is returned, so the scan reads as partial,
// with the group phase's rows counted.
func TestScanSynthetics_TransientCanaryPhaseReturned(t *testing.T) {
	st := newTestStore(t)
	h := synHandlers()
	h["DescribeCanaries"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "Rate exceeded") }
	total, err := scanSynTopLevel(t, st, h)
	if got := classifyServiceError(err); got != outcomeTransient {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeTransient", err, got)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (the group)", total)
	}
}

// A failed canary write stops the scan before the group phase, and warns
// about nothing: no row was stored.
func TestScanSynthetics_CanaryWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeSyntheticsCanary)
	warns := countOpWarnings(st, synDescribeCanariesOp)
	h := synHandlers()
	var later []string
	h["ListGroups"] = func(any) (any, error) {
		later = append(later, "ListGroups")
		return nil, errSynTest
	}
	total, err := scanSynTopLevel(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	if total != 0 || len(later) != 0 || *warns != 0 {
		t.Errorf("total, later ops, warnings = %d, %v, %d; want 0, none, 0", total, later, *warns)
	}
}

// When ctx ends on the first DescribeCanaries page, the second is not read:
// the first page's canary is stored and the stop is returned. A ctx stop is
// not a store write failure, so the group phase still makes its call.
func TestScanSynthetics_ContextEndsDuringCanaries(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called []string
	h := map[string]func(context.Context, any) (any, error){
		"DescribeCanaries": func(ctx context.Context, in any) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			cancel()
			return synCanaryPages(in)
		},
		"ListGroups": func(ctx context.Context, _ any) (any, error) {
			called = append(called, "ListGroups")
			return nil, ctx.Err()
		},
	}
	total, err := scanSynCtx(ctx, t, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (the first page's canary)", total)
	}
	if !slices.Equal(called, []string{"ListGroups"}) {
		t.Errorf("later calls = %v, want [ListGroups]", called)
	}
	rows := rowsByNativeID(t, st, TypeSyntheticsCanary)
	if _, ok := rows[synWebARN]; !ok || len(rows) != 1 {
		t.Errorf("canary rows = %v, want only %s", rows, synWebARN)
	}
}

// Each listed group is stored from its element across pages: NativeID its
// ARN, Name, the element verbatim as attributes, and no tags. A group without
// an ARN is not stored and is warned about once under its id.
func TestScanSynthetics_GroupRows(t *testing.T) {
	st := newTestStore(t)
	var warns []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service == synListGroupsOp {
			warns = append(warns, w)
		}
	}
	h := synHandlers()
	h["ListGroups"] = synGroupPages
	total, err := scanSynTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 2+2 {
		t.Errorf("total = %d, want 4 (2 canaries, 2 groups)", total)
	}
	rows := rowsByNativeID(t, st, TypeSyntheticsGroup)
	if len(rows) != 2 {
		t.Fatalf("group rows = %v, want 2", rows)
	}
	g1, g2 := rows[synGroupARN], rows[synGroup2ARN]
	if sv(g1.Name) != "g" || sv(g1.Region) != testRegion || g1.TagsJSON != nil {
		t.Errorf("g-1 row = name %q region %q tags %v", sv(g1.Name), sv(g1.Region), g1.TagsJSON)
	}
	assertJSONEqual(t, "g-1 attributes", g1.AttributesJSON, mustJSON(synGroup))
	if g2.Name != nil {
		t.Errorf("g-2 name = %q, want nil", sv(g2.Name))
	}
	assertJSONEqual(t, "g-2 attributes", g2.AttributesJSON, mustJSON(synGroup2))
	if len(warns) != 1 || warns[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(warns[0].Message, "g-noarn") {
		t.Errorf("warnings = %+v, want one naming g-noarn", warns)
	}
}

// A failed later ListGroups page keeps the groups read before it: a denial
// warns once and is not returned, any other error and a repeated token are
// returned.
func TestScanSynthetics_GroupLaterPageFails(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   error
		wantWarns int
	}{
		{"denied", synTestDenied, nil, 1},
		{"other error", errSynTest, errSynTest, 0},
		{"repeated token", nil, errRepeatedNextToken, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, synListGroupsOp)
			h := synHandlers()
			h["ListGroups"] = func(in any) (any, error) {
				if sv(in.(*synthetics.ListGroupsInput).NextToken) != "g2" {
					return synGroupPages(in)
				}
				if c.err != nil {
					return nil, c.err
				}
				return &synthetics.ListGroupsOutput{NextToken: ptrStr("g2")}, nil
			}
			total, err := scanSynTopLevel(t, st, h)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != 2+1 {
				t.Errorf("total = %d, want 3 (2 canaries, 1 group)", total)
			}
			if *warns != c.wantWarns+1 {
				t.Errorf("warnings = %d, want %d (+1 for g-noarn)", *warns, c.wantWarns+1)
			}
			rows := rowsByNativeID(t, st, TypeSyntheticsGroup)
			if _, ok := rows[synGroupARN]; !ok || len(rows) != 1 {
				t.Errorf("group rows = %v, want only %s", rows, synGroupARN)
			}
		})
	}
}

// A denied first ListGroups page stores no group and warns once, and the
// canaries are still counted.
func TestScanSynthetics_GroupsDenied(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, synListGroupsOp)
	h := synHandlers()
	delete(h, "ListGroups")
	total, err := scanSynTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 2 || *warns != 1 {
		t.Errorf("total, warnings = %d, %d; want 2, 1", total, *warns)
	}
	if n := len(rowsByNativeID(t, st, TypeSyntheticsGroup)); n != 0 {
		t.Errorf("group rows = %d, want 0", n)
	}
}

// A rescan of unchanged canaries and groups on the same store reports the
// real total and nothing inserted.
func TestScanSynthetics_RescanInsertsNothing(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	h := synHandlers()
	h["ListGroups"] = synGroupPages
	acct.cfg = opStubConfig(h, synTestDenied)
	for run, wantInserted := range []int{4, 0} {
		total, inserted, err := scanSynthetics(context.Background(), acct, testRegion, st, testScanID)
		if err != nil {
			t.Fatalf("run %d: err = %v", run, err)
		}
		if total != 4 || inserted != wantInserted {
			t.Errorf("run %d: total, inserted = %d, %d; want 4, %d", run, total, inserted, wantInserted)
		}
	}
}
