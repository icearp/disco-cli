package aws

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/mediatailor"
	mttypes "github.com/aws/aws-sdk-go-v2/service/mediatailor/types"
	"github.com/icearp/disco-cli/store"
)

var (
	mtDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: mediatailor:ListFunctions")
	mtValidation = apiErr("BadRequestException", "bad")
)

const (
	mtFn1ARN = "arn:aws:mediatailor:us-east-1:123456789012:function/enrich"
	mtFn2ARN = "arn:aws:mediatailor:us-east-1:123456789012:function/pipeline"
	mtPCARN  = "arn:aws:mediatailor:us-east-1:123456789012:playbackConfiguration/live"
)

// mtFunctionPages serves a two-page ListFunctions: page one answers a call
// without a token, page two the call carrying "p2", unless pageTwoErr replaces
// it. Every call's token and page size are recorded.
type mtFunctionPages struct {
	mu         sync.Mutex
	tokens     []string
	maxResults []int32
}

// handler serves "enrich" (HTTP_REQUEST, tagged) on page one; "pipeline"
// (SEQUENTIAL_EXECUTOR) and two elements without an ARN on page two.
func (p *mtFunctionPages) handler(pageTwoErr error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req, ok := in.(*mediatailor.ListFunctionsInput)
		if !ok {
			return nil, errors.New("unexpected ListFunctions input")
		}
		p.mu.Lock()
		p.tokens = append(p.tokens, sv(req.NextToken))
		var n int32
		if req.MaxResults != nil {
			n = *req.MaxResults
		}
		p.maxResults = append(p.maxResults, n)
		p.mu.Unlock()
		if req.NextToken == nil {
			return &mediatailor.ListFunctionsOutput{Items: []mttypes.Function{{
				Arn: ptrStr(mtFn1ARN), FunctionId: ptrStr("enrich"), FunctionType: mttypes.FunctionTypeHttpRequest,
				HttpRequestConfiguration: &mttypes.HttpRequestConfiguration{
					MethodType: mttypes.MethodTypeGet, Runtime: mttypes.RuntimeTypeJsonata,
					Url: ptrStr("https://ads.example.com/segment"),
				},
				Tags: map[string]string{"team": "ads"},
			}}, NextToken: ptrStr("p2")}, nil
		}
		if pageTwoErr != nil {
			return nil, pageTwoErr
		}
		return &mediatailor.ListFunctionsOutput{Items: []mttypes.Function{
			{
				Arn: ptrStr(mtFn2ARN), FunctionId: ptrStr("pipeline"), FunctionType: mttypes.FunctionTypeSequentialExecutor,
				SequentialExecutorConfiguration: &mttypes.SequentialExecutorConfiguration{
					Runtime: mttypes.RuntimeTypeJsonata, FunctionList: []mttypes.FunctionRef{{FunctionId: ptrStr("enrich")}},
				},
			},
			{FunctionId: ptrStr("no-arn"), FunctionType: mttypes.FunctionTypeCustomOutput},
			{FunctionId: ptrStr("no-arn-2"), FunctionType: mttypes.FunctionTypeCustomOutput},
		}}, nil
	}
}

func scanMTFunctionsWith(st *store.Store, handler func(in any) (any, error)) (int, error) {
	client := mediatailor.NewFromConfig(opStubConfig(map[string]func(any) (any, error){"ListFunctions": handler}, mtDenied))
	total, _, err := scanMTFunctions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// Both pages are stored at page size 100, and an element without an ARN is
// skipped.
func TestScanMTFunctions_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	var pages mtFunctionPages
	total, err := scanMTFunctionsWith(st, pages.handler(nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(pages.tokens, []string{"", "p2"}) {
		t.Errorf("tokens sent = %q, want [\"\" \"p2\"]", pages.tokens)
	}
	if !slices.Equal(pages.maxResults, []int32{100, 100}) {
		t.Errorf("page sizes sent = %v, want [100 100]", pages.maxResults)
	}
	rows := rowsByNativeID(t, st, TypeMediaTailorFunction)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("total = %d, rows = %v; want enrich and pipeline", total, rows)
	}
	fn1, ok := rows[mtFn1ARN]
	if !ok {
		t.Fatalf("missing %s in %v", mtFn1ARN, rows)
	}
	if sv(fn1.Name) != "enrich" || sv(fn1.Region) != "us-east-1" || sv(fn1.TagsJSON) != `{"team":"ads"}` || fn1.Status != nil {
		t.Errorf("enrich name/region/tags/status = %q/%q/%q/%v, want enrich/us-east-1/{\"team\":\"ads\"}/nil",
			sv(fn1.Name), sv(fn1.Region), sv(fn1.TagsJSON), fn1.Status)
	}
	if !strings.Contains(fn1.AttributesJSON, `"MethodType":"GET"`) {
		t.Errorf("enrich attrs = %s", fn1.AttributesJSON)
	}
	fn2, ok := rows[mtFn2ARN]
	if !ok {
		t.Fatalf("missing %s in %v", mtFn2ARN, rows)
	}
	if sv(fn2.Name) != "pipeline" || fn2.TagsJSON != nil {
		t.Errorf("pipeline name = %q, tags = %v; want pipeline, nil", sv(fn2.Name), fn2.TagsJSON)
	}
}

// Elements listed without an ARN are not stored, and the region gets exactly
// one warning for them however many there are.
func TestScanMTFunctions_ARNlessElementsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	var scopes []string
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service == "mediatailor:ListFunctions" {
			scopes = append(scopes, w.Scope)
		}
	}
	var pages mtFunctionPages
	total, err := scanMTFunctionsWith(st, pages.handler(nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(scopes, []string{"123456789012/us-east-1"}) {
		t.Errorf("warning scopes = %q, want one for 123456789012/us-east-1", scopes)
	}
	if total != 2 || len(rowsByNativeID(t, st, TypeMediaTailorFunction)) != 2 {
		t.Errorf("total = %d, want 2 (enrich, pipeline)", total)
	}
}

func TestScanMTFunctions_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanMTFunctionsWith(st, func(any) (any, error) { return &mediatailor.ListFunctionsOutput{}, nil })
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeMediaTailorFunction)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// On the first call, the unrouted-operation shapes are a silent feature gap
// and a denial warns once; any other error is returned.
func TestScanMTFunctions_FirstPageError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantWarns int
	}{
		{name: "unknown operation", err: apiErr("UnknownOperationException", "")},
		{name: "unrouted gateway", err: apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")},
		{name: "unrouted gateway denial", err: apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")},
		{name: "http 404", err: respErrCode(404)},
		{name: "denied", err: mtDenied, wantWarns: 1},
		{name: "other", err: mtValidation, wantCode: "BadRequestException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "mediatailor:ListFunctions")
			total, err := scanMTFunctionsWith(st, func(any) (any, error) { return nil, tc.err })
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if total != 0 || *warns != tc.wantWarns {
				t.Errorf("total = %d, warnings = %d; want 0, %d", total, *warns, tc.wantWarns)
			}
		})
	}
}

// After a page has succeeded, a denial warns once and keeps page one's row;
// every other error, the not-deployed shapes included, is returned with page
// one's row stored.
func TestScanMTFunctions_PageTwoError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantHTTP  int
		wantWarns int
	}{
		{name: "denied", err: mtDenied, wantWarns: 1},
		{name: "unknown operation", err: apiErr("UnknownOperationException", ""), wantCode: "UnknownOperationException"},
		{name: "unrouted gateway", err: apiErr("NotFoundException", "Unable to determine service/operation name to be authorized"), wantCode: "NotFoundException"},
		{name: "http 404", err: respErrCode(404), wantHTTP: 404},
		{name: "other", err: mtValidation, wantCode: "BadRequestException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "mediatailor:ListFunctions")
			var pages mtFunctionPages
			total, err := scanMTFunctionsWith(st, pages.handler(tc.err))
			switch {
			case tc.wantCode != "":
				assertAPICode(t, err, tc.wantCode)
			case tc.wantHTTP != 0:
				if c, ok := httpStatusCode(err); !ok || c != tc.wantHTTP {
					t.Fatalf("err = %v, want HTTP %d", err, tc.wantHTTP)
				}
			case err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeMediaTailorFunction)
			if _, ok := rows[mtFn1ARN]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want enrich only", total, rows)
			}
		})
	}
}

// A failed write of the rows listed before a list error reports both errors.
func TestScanMTFunctions_WriteAndListErrorsBothReported(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	var pages mtFunctionPages
	_, err = scanMTFunctionsWith(st, pages.handler(mtValidation))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want store.ErrStoreWrite", err)
	}
	assertAPICode(t, err, "BadRequestException")
}

// mediaTailorScanHandlers serves no channels or source locations, one playback
// configuration "live" with no prefetch schedules, and the function pages;
// overrides replace a handler by op name.
func mediaTailorScanHandlers(fns *mtFunctionPages, overrides map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListChannels":        func(any) (any, error) { return &mediatailor.ListChannelsOutput{}, nil },
		"ListSourceLocations": func(any) (any, error) { return &mediatailor.ListSourceLocationsOutput{}, nil },
		"ListPlaybackConfigurations": func(any) (any, error) {
			return &mediatailor.ListPlaybackConfigurationsOutput{Items: []mttypes.PlaybackConfiguration{{
				PlaybackConfigurationArn: ptrStr(mtPCARN), Name: ptrStr("live"),
			}}}, nil
		},
		"ListPrefetchSchedules": func(any) (any, error) { return &mediatailor.ListPrefetchSchedulesOutput{}, nil },
		"ListFunctions":         fns.handler(nil),
	}
	for op, fn := range overrides {
		h[op] = fn
	}
	return h
}

// The function phase runs in the scan's own region alongside the playback
// configurations.
func TestScanMediaTailor_StoresFunctions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var fns mtFunctionPages
	var regions parentCalls
	cfg := opStubConfig(mediaTailorScanHandlers(&fns, nil), mtDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, _, err := scanMediaTailor(context.Background(), acct, "eu-west-1", st, testScanID)
	if err != nil {
		t.Fatalf("scanMediaTailor: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (1 playback configuration, 2 functions)", total)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	rows := rowsByNativeID(t, st, TypeMediaTailorFunction)
	for _, id := range []string{mtFn1ARN, mtFn2ARN} {
		if r, ok := rows[id]; !ok || sv(r.Region) != "eu-west-1" {
			t.Errorf("function %s missing or not in eu-west-1: %v", id, rows)
		}
	}
}

// A function-phase hard error reaches the caller with the playback
// configuration and the functions' own page-one row stored and counted.
func TestScanMediaTailor_FunctionErrorAfterMediaPhase(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var fns mtFunctionPages
	acct.cfg = opStubConfig(mediaTailorScanHandlers(&fns, map[string]func(any) (any, error){
		"ListFunctions": fns.handler(mtValidation),
	}), mtDenied)

	total, _, err := scanMediaTailor(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "BadRequestException")
	if total != 2 {
		t.Errorf("total = %d, want 2 (1 playback configuration, 1 function)", total)
	}
	if _, ok := rowsByNativeID(t, st, TypeMediaTailorPlaybackConfiguration)[mtPCARN]; !ok {
		t.Errorf("missing playback configuration %s", mtPCARN)
	}
	if _, ok := rowsByNativeID(t, st, TypeMediaTailorFunction)[mtFn1ARN]; !ok {
		t.Errorf("missing function %s", mtFn1ARN)
	}
}

// A hard error in the media phase does not cost the functions: they are stored
// and counted, and the media error still reaches the caller.
func TestScanMediaTailor_MediaErrorStillScansFunctions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var fns mtFunctionPages
	acct.cfg = opStubConfig(mediaTailorScanHandlers(&fns, map[string]func(any) (any, error){
		"ListPlaybackConfigurations": func(any) (any, error) { return nil, mtValidation },
	}), mtDenied)

	total, _, err := scanMediaTailor(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "BadRequestException")
	if total != 2 || len(rowsByNativeID(t, st, TypeMediaTailorFunction)) != 2 {
		t.Errorf("total = %d, want 2 functions", total)
	}
}

func mtARN(resource string) string {
	return "arn:aws:mediatailor:us-east-1:123456789012:" + resource
}

// The media fixture: every list element but the last of each type is tagged.
var (
	mtCh1 = mttypes.Channel{
		Arn: ptrStr(mtARN("channel/ch1")), ChannelName: ptrStr("ch1"), ChannelState: ptrStr("RUNNING"),
		Tags: map[string]string{"env": "prod"},
	}
	mtCh2      = mttypes.Channel{Arn: ptrStr(mtARN("channel/ch2")), ChannelName: ptrStr("ch2"), ChannelState: ptrStr("STOPPED")}
	mtPolicy   = &mediatailor.GetChannelPolicyOutput{Policy: ptrStr(`{"Version":"2012-10-17"}`)}
	mtProgram  = mttypes.ScheduleEntry{Arn: ptrStr(mtARN("program/ch1/p1")), ProgramName: ptrStr("p1"), ChannelName: ptrStr("ch1")}
	mtLoc1     = mttypes.SourceLocation{Arn: ptrStr(mtARN("sourceLocation/sl1")), SourceLocationName: ptrStr("sl1"), Tags: map[string]string{"tier": "gold"}}
	mtLoc2     = mttypes.SourceLocation{Arn: ptrStr(mtARN("sourceLocation/sl2")), SourceLocationName: ptrStr("sl2")}
	mtLive1    = mttypes.LiveSource{Arn: ptrStr(mtARN("liveSource/sl1/cam")), LiveSourceName: ptrStr("cam"), SourceLocationName: ptrStr("sl1"), Tags: map[string]string{"src": "cam"}}
	mtLive2    = mttypes.LiveSource{Arn: ptrStr(mtARN("liveSource/sl1/backup")), LiveSourceName: ptrStr("backup"), SourceLocationName: ptrStr("sl1")}
	mtVod1     = mttypes.VodSource{Arn: ptrStr(mtARN("vodSource/sl1/film")), VodSourceName: ptrStr("film"), SourceLocationName: ptrStr("sl1"), Tags: map[string]string{"kind": "film"}}
	mtVod2     = mttypes.VodSource{Arn: ptrStr(mtARN("vodSource/sl2/clip")), VodSourceName: ptrStr("clip"), SourceLocationName: ptrStr("sl2")}
	mtPC1      = mttypes.PlaybackConfiguration{PlaybackConfigurationArn: ptrStr(mtARN("playbackConfiguration/pc1")), Name: ptrStr("pc1"), Tags: map[string]string{"pc": "main"}}
	mtPC2      = mttypes.PlaybackConfiguration{PlaybackConfigurationArn: ptrStr(mtARN("playbackConfiguration/pc2")), Name: ptrStr("pc2")}
	mtSched1   = mttypes.PrefetchSchedule{Arn: ptrStr(mtARN("prefetchSchedule/pc1/s1")), Name: ptrStr("s1"), PlaybackConfigurationName: ptrStr("pc1"), Tags: map[string]string{"ad": "pre"}}
	mtSched2   = mttypes.PrefetchSchedule{Arn: ptrStr(mtARN("prefetchSchedule/pc2/s2")), Name: ptrStr("s2"), PlaybackConfigurationName: ptrStr("pc2")}
	mtNotFound = apiErr("NotFoundException", "not found")
)

// mtCalls records each call's input under its op name.
type mtCalls struct {
	mu sync.Mutex
	in map[string][]any
}

func (c *mtCalls) record(op string, h func(any) (any, error)) func(any) (any, error) {
	return func(in any) (any, error) {
		c.mu.Lock()
		if c.in == nil {
			c.in = map[string][]any{}
		}
		c.in[op] = append(c.in[op], in)
		c.mu.Unlock()
		return h(in)
	}
}

func (c *mtCalls) of(op string) []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.in[op]
}

// mtMediaHandlers serves the media fixture: two pages of channels (ch2 behind
// token "c2"), a policy for ch1 and empty policy text for ch2, program p1 on
// ch1, two pages of sl1 live sources (backup behind token "l2"), one VOD
// source per location, one prefetch schedule per playback configuration, and
// the function pages. overrides replace a handler by op name. Every call is
// recorded in calls.
func mtMediaHandlers(calls *mtCalls, fns *mtFunctionPages, overrides map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListChannels": func(in any) (any, error) {
			if in.(*mediatailor.ListChannelsInput).NextToken == nil {
				return &mediatailor.ListChannelsOutput{Items: []mttypes.Channel{mtCh1}, NextToken: ptrStr("c2")}, nil
			}
			return &mediatailor.ListChannelsOutput{Items: []mttypes.Channel{mtCh2}}, nil
		},
		"GetChannelPolicy": func(in any) (any, error) {
			if sv(in.(*mediatailor.GetChannelPolicyInput).ChannelName) == "ch1" {
				return mtPolicy, nil
			}
			return &mediatailor.GetChannelPolicyOutput{}, nil
		},
		"GetChannelSchedule": func(in any) (any, error) {
			if sv(in.(*mediatailor.GetChannelScheduleInput).ChannelName) == "ch1" {
				return &mediatailor.GetChannelScheduleOutput{Items: []mttypes.ScheduleEntry{mtProgram}}, nil
			}
			return &mediatailor.GetChannelScheduleOutput{}, nil
		},
		"ListSourceLocations": func(any) (any, error) {
			return &mediatailor.ListSourceLocationsOutput{Items: []mttypes.SourceLocation{mtLoc1, mtLoc2}}, nil
		},
		"ListLiveSources": func(in any) (any, error) {
			req := in.(*mediatailor.ListLiveSourcesInput)
			switch {
			case sv(req.SourceLocationName) != "sl1":
				return &mediatailor.ListLiveSourcesOutput{}, nil
			case req.NextToken == nil:
				return &mediatailor.ListLiveSourcesOutput{Items: []mttypes.LiveSource{mtLive1}, NextToken: ptrStr("l2")}, nil
			default:
				return &mediatailor.ListLiveSourcesOutput{Items: []mttypes.LiveSource{mtLive2}}, nil
			}
		},
		"ListVodSources": func(in any) (any, error) {
			if sv(in.(*mediatailor.ListVodSourcesInput).SourceLocationName) == "sl1" {
				return &mediatailor.ListVodSourcesOutput{Items: []mttypes.VodSource{mtVod1}}, nil
			}
			return &mediatailor.ListVodSourcesOutput{Items: []mttypes.VodSource{mtVod2}}, nil
		},
		"ListPlaybackConfigurations": func(any) (any, error) {
			return &mediatailor.ListPlaybackConfigurationsOutput{Items: []mttypes.PlaybackConfiguration{mtPC1, mtPC2}}, nil
		},
		"ListPrefetchSchedules": func(in any) (any, error) {
			if sv(in.(*mediatailor.ListPrefetchSchedulesInput).PlaybackConfigurationName) == "pc1" {
				return &mediatailor.ListPrefetchSchedulesOutput{Items: []mttypes.PrefetchSchedule{mtSched1}}, nil
			}
			return &mediatailor.ListPrefetchSchedulesOutput{Items: []mttypes.PrefetchSchedule{mtSched2}}, nil
		},
		"ListFunctions": fns.handler(nil),
	}
	for op, fn := range overrides {
		h[op] = fn
	}
	for op, fn := range h {
		h[op] = calls.record(op, fn)
	}
	return h
}

func scanMTMediaWith(st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	client := mediatailor.NewFromConfig(opStubConfig(handlers, mtDenied))
	total, _, err := scanMediaTailorAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// assertMTRow checks the stored row of arn: its name, attributes equal to the
// listed element, and its tags (nil: none stored).
func assertMTRow(t *testing.T, st *store.Store, typ, arn, name string, element any, tags map[string]string) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	if !ok {
		t.Errorf("%s: no row %s", typ, arn)
		return
	}
	if sv(r.Name) != name {
		t.Errorf("%s name = %q, want %q", arn, sv(r.Name), name)
	}
	var got, want any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &got); err != nil {
		t.Fatalf("%s attrs %q: %v", arn, r.AttributesJSON, err)
	}
	if err := json.Unmarshal([]byte(mustJSON(element)), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s attrs = %s, want %s", arn, r.AttributesJSON, mustJSON(element))
	}
	if tags == nil {
		if r.TagsJSON != nil {
			t.Errorf("%s tags = %s, want none", arn, *r.TagsJSON)
		}
		return
	}
	assertTagsMap(t, r.TagsJSON, tags)
}

// Every media type is stored with its listed element as attributes and the
// element's tags; an untagged element stores no tags.
func TestScanMediaTailorAll_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	var calls mtCalls
	var fns mtFunctionPages
	warns := countWarnings(st)
	total, err := scanMTMediaWith(st, mtMediaHandlers(&calls, &fns, nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// The functions fixture's ARN-less elements give the only warning.
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
	// 2 channels, 1 policy, 1 program, 2 locations, 2 live, 2 VOD, 2 configs, 2 schedules, 2 functions.
	if total != 16 {
		t.Errorf("total = %d, want 16", total)
	}
	assertMTRow(t, st, TypeMediaTailorChannel, mtARN("channel/ch1"), "ch1", mtCh1, map[string]string{"env": "prod"})
	assertMTRow(t, st, TypeMediaTailorChannel, mtARN("channel/ch2"), "ch2", mtCh2, nil)
	if r := rowsByNativeID(t, st, TypeMediaTailorChannel)[mtARN("channel/ch1")]; sv(r.Status) != "RUNNING" {
		t.Errorf("ch1 status = %q, want RUNNING", sv(r.Status))
	}
	assertMTRow(t, st, TypeMediaTailorChannelPolicy, mtARN("channel/ch1/policy"), "ch1", mtPolicy, nil)
	if n := len(rowsByNativeID(t, st, TypeMediaTailorChannelPolicy)); n != 1 {
		t.Errorf("channel policies = %d, want 1 (ch2's policy text is empty)", n)
	}
	assertMTRow(t, st, TypeMediaTailorProgram, mtARN("program/ch1/p1"), "p1", mtProgram, nil)
	assertMTRow(t, st, TypeMediaTailorSourceLocation, mtARN("sourceLocation/sl1"), "sl1", mtLoc1, map[string]string{"tier": "gold"})
	assertMTRow(t, st, TypeMediaTailorSourceLocation, mtARN("sourceLocation/sl2"), "sl2", mtLoc2, nil)
	assertMTRow(t, st, TypeMediaTailorLiveSource, mtARN("liveSource/sl1/cam"), "cam", mtLive1, map[string]string{"src": "cam"})
	assertMTRow(t, st, TypeMediaTailorLiveSource, mtARN("liveSource/sl1/backup"), "backup", mtLive2, nil)
	assertMTRow(t, st, TypeMediaTailorVodSource, mtARN("vodSource/sl1/film"), "film", mtVod1, map[string]string{"kind": "film"})
	assertMTRow(t, st, TypeMediaTailorVodSource, mtARN("vodSource/sl2/clip"), "clip", mtVod2, nil)
	assertMTRow(t, st, TypeMediaTailorPlaybackConfiguration, mtARN("playbackConfiguration/pc1"), "pc1", mtPC1, map[string]string{"pc": "main"})
	assertMTRow(t, st, TypeMediaTailorPlaybackConfiguration, mtARN("playbackConfiguration/pc2"), "pc2", mtPC2, nil)
	assertMTRow(t, st, TypeMediaTailorPrefetchSchedule, mtARN("prefetchSchedule/pc1/s1"), "s1", mtSched1, map[string]string{"ad": "pre"})
	assertMTRow(t, st, TypeMediaTailorPrefetchSchedule, mtARN("prefetchSchedule/pc2/s2"), "s2", mtSched2, nil)
}

// Each call carries the parent it was made for, the token of the page before,
// the model's page cap of 100, and no other filter.
func TestScanMediaTailorAll_RequestInputs(t *testing.T) {
	st := newTestStore(t)
	var calls mtCalls
	var fns mtFunctionPages
	if _, err := scanMTMediaWith(st, mtMediaHandlers(&calls, &fns, nil)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	type req struct {
		parent, token string
		max           int32
	}
	size := func(p *int32) int32 {
		if p == nil {
			return 0
		}
		return *p
	}
	got := map[string][]req{}
	for _, in := range calls.of("ListChannels") {
		r := in.(*mediatailor.ListChannelsInput)
		got["ListChannels"] = append(got["ListChannels"], req{"", sv(r.NextToken), size(r.MaxResults)})
	}
	for _, in := range calls.of("GetChannelPolicy") {
		got["GetChannelPolicy"] = append(got["GetChannelPolicy"], req{sv(in.(*mediatailor.GetChannelPolicyInput).ChannelName), "", 0})
	}
	for _, in := range calls.of("GetChannelSchedule") {
		r := in.(*mediatailor.GetChannelScheduleInput)
		if r.DurationMinutes != nil || r.Audience != nil {
			t.Errorf("GetChannelSchedule DurationMinutes/Audience = %v/%v, want unset", r.DurationMinutes, r.Audience)
		}
		got["GetChannelSchedule"] = append(got["GetChannelSchedule"], req{sv(r.ChannelName), sv(r.NextToken), size(r.MaxResults)})
	}
	for _, in := range calls.of("ListSourceLocations") {
		r := in.(*mediatailor.ListSourceLocationsInput)
		got["ListSourceLocations"] = append(got["ListSourceLocations"], req{"", sv(r.NextToken), size(r.MaxResults)})
	}
	for _, in := range calls.of("ListLiveSources") {
		r := in.(*mediatailor.ListLiveSourcesInput)
		got["ListLiveSources"] = append(got["ListLiveSources"], req{sv(r.SourceLocationName), sv(r.NextToken), size(r.MaxResults)})
	}
	for _, in := range calls.of("ListVodSources") {
		r := in.(*mediatailor.ListVodSourcesInput)
		got["ListVodSources"] = append(got["ListVodSources"], req{sv(r.SourceLocationName), sv(r.NextToken), size(r.MaxResults)})
	}
	for _, in := range calls.of("ListPlaybackConfigurations") {
		r := in.(*mediatailor.ListPlaybackConfigurationsInput)
		got["ListPlaybackConfigurations"] = append(got["ListPlaybackConfigurations"], req{"", sv(r.NextToken), size(r.MaxResults)})
	}
	for _, in := range calls.of("ListPrefetchSchedules") {
		r := in.(*mediatailor.ListPrefetchSchedulesInput)
		if r.ScheduleType != mttypes.ListPrefetchScheduleTypeAll || r.StreamId != nil {
			t.Errorf("ListPrefetchSchedules ScheduleType/StreamId = %q/%v, want ALL/unset", r.ScheduleType, r.StreamId)
		}
		got["ListPrefetchSchedules"] = append(got["ListPrefetchSchedules"], req{sv(r.PlaybackConfigurationName), sv(r.NextToken), size(r.MaxResults)})
	}
	want := map[string][]req{
		"ListChannels":               {{"", "", 100}, {"", "c2", 100}},
		"GetChannelPolicy":           {{"ch1", "", 0}, {"ch2", "", 0}},
		"GetChannelSchedule":         {{"ch1", "", 100}, {"ch2", "", 100}},
		"ListSourceLocations":        {{"", "", 100}},
		"ListLiveSources":            {{"sl1", "", 100}, {"sl1", "l2", 100}, {"sl2", "", 100}},
		"ListVodSources":             {{"sl1", "", 100}, {"sl2", "", 100}},
		"ListPlaybackConfigurations": {{"", "", 100}},
		"ListPrefetchSchedules":      {{"pc1", "", 100}, {"pc2", "", 100}},
	}
	for op, w := range want {
		if !slices.Equal(got[op], w) {
			t.Errorf("%s calls = %+v, want %+v", op, got[op], w)
		}
	}
}

// A failed second channel page keeps ch1, whose policy and schedule are still
// read; every later phase runs; the error is returned.
func TestScanMediaTailorAll_ChannelPageTwoErrorKeepsPageOne(t *testing.T) {
	st := newTestStore(t)
	var calls mtCalls
	var fns mtFunctionPages
	total, err := scanMTMediaWith(st, mtMediaHandlers(&calls, &fns, map[string]func(any) (any, error){
		"ListChannels": func(in any) (any, error) {
			if in.(*mediatailor.ListChannelsInput).NextToken == nil {
				return &mediatailor.ListChannelsOutput{Items: []mttypes.Channel{mtCh1}, NextToken: ptrStr("c2")}, nil
			}
			return nil, mtValidation
		},
	}))
	assertAPICode(t, err, "BadRequestException")
	if total != 15 {
		t.Errorf("total = %d, want 15 (the 16-row fixture less ch2)", total)
	}
	assertMTRow(t, st, TypeMediaTailorChannel, mtARN("channel/ch1"), "ch1", mtCh1, map[string]string{"env": "prod"})
	assertMTRow(t, st, TypeMediaTailorChannelPolicy, mtARN("channel/ch1/policy"), "ch1", mtPolicy, nil)
	assertMTRow(t, st, TypeMediaTailorProgram, mtARN("program/ch1/p1"), "p1", mtProgram, nil)
	if _, ok := rowsByNativeID(t, st, TypeMediaTailorFunction)[mtFn1ARN]; !ok {
		t.Errorf("missing function %s", mtFn1ARN)
	}
}

// A hard error in every account-wide listing but functions still lets each
// later phase run, and every error reaches the caller.
func TestScanMediaTailorAll_PhaseErrorsDoNotStopLaterPhases(t *testing.T) {
	st := newTestStore(t)
	var calls mtCalls
	var fns mtFunctionPages
	fail := func(any) (any, error) { return nil, mtValidation }
	_, err := scanMTMediaWith(st, mtMediaHandlers(&calls, &fns, map[string]func(any) (any, error){
		"ListChannels":               fail,
		"ListSourceLocations":        fail,
		"ListPlaybackConfigurations": fail,
	}))
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 3 {
		t.Fatalf("err = %v, want three joined phase errors", err)
	}
	for _, op := range []string{"ListChannels", "ListSourceLocations", "ListPlaybackConfigurations", "ListFunctions"} {
		if len(calls.of(op)) == 0 {
			t.Errorf("%s was not called", op)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeMediaTailorFunction)); n != 2 {
		t.Errorf("functions stored = %d, want 2", n)
	}
}

// A denied per-channel read warns once for its op, naming the account and
// region, and costs no other row.
func TestScanMediaTailorAll_ChildDenialWarnsOncePerOp(t *testing.T) {
	for _, tc := range []struct {
		op        string
		wantTotal int
	}{
		{"GetChannelPolicy", 15},
		{"GetChannelSchedule", 15},
		{"ListLiveSources", 14},
		{"ListVodSources", 14},
		{"ListPrefetchSchedules", 14},
	} {
		op := tc.op
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var scopes []string
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == "mediatailor:"+op {
					scopes = append(scopes, w.Scope)
				}
			}
			var calls mtCalls
			var fns mtFunctionPages
			total, err := scanMTMediaWith(st, mtMediaHandlers(&calls, &fns, map[string]func(any) (any, error){
				op: func(any) (any, error) { return nil, mtDenied },
			}))
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if !slices.Equal(scopes, []string{"123456789012/us-east-1"}) {
				t.Errorf("warning scopes = %q, want one for 123456789012/us-east-1", scopes)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d (the 16-row fixture less %s's rows)", total, tc.wantTotal, op)
			}
			if len(calls.of("ListFunctions")) == 0 {
				t.Error("ListFunctions was not called")
			}
		})
	}
}

// A hard error for one parent keeps its siblings' rows and is returned. The
// child listings model no error, so a not-found answer is returned too.
func TestScanMediaTailorAll_ChildErrorKeepsSiblings(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"bad request", mtValidation, "BadRequestException"},
		{"not found", mtNotFound, "NotFoundException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls mtCalls
			var fns mtFunctionPages
			_, err := scanMTMediaWith(st, mtMediaHandlers(&calls, &fns, map[string]func(any) (any, error){
				"ListVodSources": func(in any) (any, error) {
					if sv(in.(*mediatailor.ListVodSourcesInput).SourceLocationName) == "sl1" {
						return nil, tc.err
					}
					return &mediatailor.ListVodSourcesOutput{Items: []mttypes.VodSource{mtVod2}}, nil
				},
			}))
			assertAPICode(t, err, tc.code)
			rows := rowsByNativeID(t, st, TypeMediaTailorVodSource)
			if _, ok := rows[mtARN("vodSource/sl2/clip")]; !ok || len(rows) != 1 {
				t.Errorf("VOD rows = %v, want clip only", rows)
			}
		})
	}
}

// The policy and schedule reads model no error, so a not-found or bad-request
// answer for ch2 is returned, and ch1's policy and program are still stored.
func TestScanMediaTailorAll_ChannelReadErrorKeepsSiblings(t *testing.T) {
	for _, op := range []string{"GetChannelPolicy", "GetChannelSchedule"} {
		for _, tc := range []struct {
			err  error
			code string
		}{
			{mtNotFound, "NotFoundException"},
			{mtValidation, "BadRequestException"},
		} {
			t.Run(op+"/"+tc.code, func(t *testing.T) {
				st := newTestStore(t)
				var calls mtCalls
				var fns mtFunctionPages
				base := mtMediaHandlers(&mtCalls{}, &fns, nil)[op]
				_, err := scanMTMediaWith(st, mtMediaHandlers(&calls, &fns, map[string]func(any) (any, error){
					op: func(in any) (any, error) {
						var channel string
						switch r := in.(type) {
						case *mediatailor.GetChannelPolicyInput:
							channel = sv(r.ChannelName)
						case *mediatailor.GetChannelScheduleInput:
							channel = sv(r.ChannelName)
						}
						if channel == "ch2" {
							return nil, tc.err
						}
						return base(in)
					},
				}))
				assertAPICode(t, err, tc.code)
				assertMTRow(t, st, TypeMediaTailorChannelPolicy, mtARN("channel/ch1/policy"), "ch1", mtPolicy, nil)
				assertMTRow(t, st, TypeMediaTailorProgram, mtARN("program/ch1/p1"), "p1", mtProgram, nil)
			})
		}
	}
}

// A playback configuration listed without a name is stored, its prefetch
// schedules are not read, and the region gets one warning for it.
func TestScanMediaTailorAll_NamelessParentWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "mediatailor:ListPlaybackConfigurations")
	var calls mtCalls
	var fns mtFunctionPages
	nameless := mttypes.PlaybackConfiguration{PlaybackConfigurationArn: ptrStr(mtARN("playbackConfiguration/x"))}
	_, err := scanMTMediaWith(st, mtMediaHandlers(&calls, &fns, map[string]func(any) (any, error){
		"ListPlaybackConfigurations": func(any) (any, error) {
			return &mediatailor.ListPlaybackConfigurationsOutput{Items: []mttypes.PlaybackConfiguration{mtPC1, nameless}}, nil
		},
	}))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
	if _, ok := rowsByNativeID(t, st, TypeMediaTailorPlaybackConfiguration)[mtARN("playbackConfiguration/x")]; !ok {
		t.Error("nameless playback configuration not stored")
	}
	if n := len(calls.of("ListPrefetchSchedules")); n != 1 {
		t.Errorf("ListPrefetchSchedules calls = %d, want 1 (pc1 only)", n)
	}
}

// A failed store write stops the scan: no later phase is called, and a
// nameless channel is not warned about, since its row was not stored either.
func TestScanMediaTailorAll_StoreWriteStops(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	warns := countWarnings(st)
	var calls mtCalls
	var fns mtFunctionPages
	_, err = scanMTMediaWith(st, mtMediaHandlers(&calls, &fns, map[string]func(any) (any, error){
		"ListChannels": func(any) (any, error) {
			return &mediatailor.ListChannelsOutput{Items: []mttypes.Channel{mtCh1, {Arn: ptrStr(mtARN("channel/nameless"))}}}, nil
		},
	}))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d, want 0 after the failed write", *warns)
	}
	for _, op := range []string{"GetChannelPolicy", "ListSourceLocations", "ListFunctions"} {
		if n := len(calls.of(op)); n != 0 {
			t.Errorf("%s calls = %d, want 0 after the failed write", op, n)
		}
	}
}
