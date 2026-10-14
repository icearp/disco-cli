package aws

import (
	"context"
	"errors"
	"path/filepath"
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
