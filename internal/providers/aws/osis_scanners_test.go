package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/osis"
	osistypes "github.com/aws/aws-sdk-go-v2/service/osis/types"

	"github.com/icearp/disco-cli/store"
)

var (
	osisTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: osis:List")
	errOSISTest    = errors.New("osis test failure")
	// errOSISUnexpectedInput answers a request a stub does not expect.
	errOSISUnexpectedInput = errors.New("osis stub: unexpected request input")
)

const (
	osisPipeARN      = "arn:aws:osis:us-east-1:123456789012:pipeline/logs"
	osisOtherPipeARN = "arn:aws:osis:us-east-1:123456789012:pipeline/unlisted"
	osisConn1Native  = osisPipeARN + "/endpoint-connection/ep-1"
	osisConn2Native  = osisOtherPipeARN + "/endpoint-connection/ep-2"
	osisOlderRows    = 3
)

// osisRecorder captures the MaxResults each ListPipelineEndpointConnections
// call was made with.
type osisRecorder struct {
	mu         sync.Mutex
	maxResults []int32
}

func (r *osisRecorder) record(v *int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxResults = append(r.maxResults, sdkaws.ToInt32(v))
}

func osisConn(pipeARN, endpointID string, status osistypes.PipelineEndpointStatus) osistypes.PipelineEndpointConnection {
	return osistypes.PipelineEndpointConnection{
		PipelineArn: ptrStr(pipeARN), EndpointId: ptrStr(endpointID), Status: status, VpcEndpointOwner: ptrStr("210987654321"),
	}
}

// osisHandlers answers every osis op: one row for each older type, and two
// connection pages, the first holding an element without its EndpointId. The
// second page's connection names a pipeline ListPipelines did not return.
func osisHandlers(rec *osisRecorder) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListPipelines": func(in any) (any, error) {
			if req := in.(*osis.ListPipelinesInput); sdkaws.ToInt32(req.MaxResults) != 100 || req.NextToken != nil {
				return nil, errOSISUnexpectedInput
			}
			return &osis.ListPipelinesOutput{Pipelines: []osistypes.PipelineSummary{
				{PipelineArn: ptrStr(osisPipeARN), PipelineName: ptrStr("logs"), Status: osistypes.PipelineStatusActive},
			}}, nil
		},
		"ListPipelineBlueprints": func(any) (any, error) {
			return &osis.ListPipelineBlueprintsOutput{Blueprints: []osistypes.PipelineBlueprintSummary{{BlueprintName: ptrStr("AWS-ApacheLogPipeline")}}}, nil
		},
		"ListPipelineEndpoints": func(any) (any, error) {
			return &osis.ListPipelineEndpointsOutput{PipelineEndpoints: []osistypes.PipelineEndpoint{{EndpointId: ptrStr("ep-own"), PipelineArn: ptrStr(osisPipeARN)}}}, nil
		},
		"ListPipelineEndpointConnections": func(in any) (any, error) {
			req := in.(*osis.ListPipelineEndpointConnectionsInput)
			rec.record(req.MaxResults)
			if req.NextToken == nil {
				return &osis.ListPipelineEndpointConnectionsOutput{
					PipelineEndpointConnections: []osistypes.PipelineEndpointConnection{
						osisConn(osisPipeARN, "ep-1", osistypes.PipelineEndpointStatusActive),
						{PipelineArn: ptrStr(osisPipeARN)},
					},
					NextToken: ptrStr("p2"),
				}, nil
			}
			return &osis.ListPipelineEndpointConnectionsOutput{PipelineEndpointConnections: []osistypes.PipelineEndpointConnection{
				osisConn(osisOtherPipeARN, "ep-2", osistypes.PipelineEndpointStatusRevoked),
			}}, nil
		},
	}
}

// scanOSISTopLevel runs scanOSIS against handlers on a fresh store, where
// every stored row is new, so it fails the test unless inserted equals total.
func scanOSISTopLevel(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	return scanOSISCtx(context.Background(), t, st, ignoreCtx(handlers))
}

// scanOSISCtx is scanOSISTopLevel under ctx, for handlers that read the
// call's context.
func scanOSISCtx(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(context.Context, any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(handlers, osisTestDenied)
	total, inserted, err := scanOSIS(ctx, acct, testRegion, st, testScanID)
	if inserted != total {
		t.Errorf("inserted = %d, want total %d", inserted, total)
	}
	return total, err
}

func TestScanOSIS_StoresEndpointConnections(t *testing.T) {
	st := newTestStore(t)
	rec := &osisRecorder{}
	total, err := scanOSISTopLevel(t, st, osisHandlers(rec))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != osisOlderRows+2 {
		t.Errorf("total = %d, want %d", total, osisOlderRows+2)
	}
	if !slices.Equal(rec.maxResults, []int32{100, 100}) {
		t.Errorf("MaxResults = %v, want [100 100]", rec.maxResults)
	}
	rows := rowsByNativeID(t, st, TypeOSISPipelineEndpointConnection)
	if len(rows) != 2 {
		t.Fatalf("connection rows = %v, want 2", rows)
	}
	r, ok := rows[osisConn1Native]
	if !ok {
		t.Fatalf("no row %s in %v", osisConn1Native, rows)
	}
	if r.Type != TypeOSISPipelineEndpointConnection || sv(r.Region) != testRegion || sv(r.Name) != "ep-1" || sv(r.Status) != "ACTIVE" {
		t.Errorf("row = type %q region %q name %q status %q", r.Type, sv(r.Region), sv(r.Name), sv(r.Status))
	}
	var attrs struct{ VpcEndpointOwner string }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.VpcEndpointOwner != "210987654321" {
		t.Errorf("attrs = %s (err %v), want VpcEndpointOwner 210987654321", r.AttributesJSON, err)
	}
	if sv(rows[osisConn2Native].Status) != "REVOKED" {
		t.Errorf("%s status = %q, want REVOKED", osisConn2Native, sv(rows[osisConn2Native].Status))
	}
	assertContainsNative(t, st, osisPipeARN, osisConn1Native)
}

// A connection to a pipeline ListPipelines did not return is stored unlinked,
// with no pair recorded for the store to warn about.
func TestScanOSIS_ConnectionToUnlistedPipelineUnlinked(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	if _, err := scanOSISTopLevel(t, st, osisHandlers(&osisRecorder{})); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, osisConn2Native), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("contains edges to %s = %+v, want none", osisConn2Native, rels)
	}
}

func TestScanOSIS_EmptyConnections(t *testing.T) {
	st := newTestStore(t)
	h := osisHandlers(&osisRecorder{})
	h["ListPipelineEndpointConnections"] = func(any) (any, error) { return &osis.ListPipelineEndpointConnectionsOutput{}, nil }
	total, err := scanOSISTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != osisOlderRows {
		t.Errorf("total = %d, want %d (older rows only)", total, osisOlderRows)
	}
	if n := len(rowsByNativeID(t, st, TypeOSISPipelineEndpointConnection)); n != 0 {
		t.Errorf("connection rows = %d, want 0", n)
	}
}

// A denial warns once, keeps the rows of the pages before it, and costs the
// older phases nothing.
func TestScanOSIS_ConnectionsDenied(t *testing.T) {
	cases := []struct {
		name     string
		deniedAt int
		wantRows []string
	}{
		{"first page", 1, nil},
		{"second page", 2, []string{osisConn1Native}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := osisHandlers(&osisRecorder{})
			first := h["ListPipelineEndpointConnections"]
			calls := 0
			h["ListPipelineEndpointConnections"] = func(in any) (any, error) {
				calls++
				if calls == c.deniedAt {
					return nil, osisTestDenied
				}
				return first(in)
			}
			warnings := countOpWarnings(st, osisEndpointConnectionsOp)
			total, err := scanOSISTopLevel(t, st, h)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warnings != 1 {
				t.Errorf("warnings = %d, want 1", *warnings)
			}
			if total != osisOlderRows+len(c.wantRows) {
				t.Errorf("total = %d, want %d", total, osisOlderRows+len(c.wantRows))
			}
			rows := rowsByNativeID(t, st, TypeOSISPipelineEndpointConnection)
			if len(rows) != len(c.wantRows) {
				t.Errorf("connection rows = %v, want %v", rows, c.wantRows)
			}
			for _, n := range c.wantRows {
				if _, ok := rows[n]; !ok {
					t.Errorf("missing row %s", n)
				}
			}
		})
	}
}

// A hard error in any phase is returned, and every other phase's rows are
// stored: each phase runs whatever the others returned (the connection rows
// are stored unlinked when ListPipelines fails).
func TestScanOSIS_PhaseErrorPropagates(t *testing.T) {
	cases := []struct {
		op                                  string
		wantTotal, wantEndpoints, wantConns int
	}{
		{"ListPipelines", 2 + 2, 1, 2},
		{"ListPipelineBlueprints", 2 + 2, 1, 2},
		{"ListPipelineEndpoints", 2 + 2, 0, 2},
		{"ListPipelineEndpointConnections", osisOlderRows, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := osisHandlers(&osisRecorder{})
			h[c.op] = func(any) (any, error) { return nil, errOSISTest }
			total, err := scanOSISTopLevel(t, st, h)
			if !errors.Is(err, errOSISTest) {
				t.Fatalf("err = %v, want the %s error", err, c.op)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d, want %d", total, c.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, TypeOSISPipelineEndpoint)); n != c.wantEndpoints {
				t.Errorf("endpoint rows = %d, want %d", n, c.wantEndpoints)
			}
			if n := len(rowsByNativeID(t, st, TypeOSISPipelineEndpointConnection)); n != c.wantConns {
				t.Errorf("connection rows = %d, want %d", n, c.wantConns)
			}
		})
	}
}

// A hard error on a later page is returned, and the rows of the page before
// it are stored and linked.
func TestScanOSIS_ConnectionErrorOnSecondPageKeepsFirst(t *testing.T) {
	st := newTestStore(t)
	h := osisHandlers(&osisRecorder{})
	first := h["ListPipelineEndpointConnections"]
	h["ListPipelineEndpointConnections"] = func(in any) (any, error) {
		if in.(*osis.ListPipelineEndpointConnectionsInput).NextToken != nil {
			return nil, errOSISTest
		}
		return first(in)
	}
	total, err := scanOSISTopLevel(t, st, h)
	if !errors.Is(err, errOSISTest) {
		t.Fatalf("err = %v, want the connections error", err)
	}
	if total != osisOlderRows+1 {
		t.Errorf("total = %d, want %d", total, osisOlderRows+1)
	}
	rows := rowsByNativeID(t, st, TypeOSISPipelineEndpointConnection)
	if _, ok := rows[osisConn1Native]; !ok || len(rows) != 1 {
		t.Errorf("connection rows = %v, want only %s", rows, osisConn1Native)
	}
	assertContainsNative(t, st, osisPipeARN, osisConn1Native)
}

// A page that repeats the token it was asked with ends the listing.
func TestScanOSIS_ConnectionsRepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	h := osisHandlers(&osisRecorder{})
	calls := 0
	h["ListPipelineEndpointConnections"] = func(any) (any, error) {
		calls++
		if calls > 3 {
			return nil, errOSISTest
		}
		id := []string{"", "ep-a", "ep-b", "ep-c"}[calls]
		return &osis.ListPipelineEndpointConnectionsOutput{
			PipelineEndpointConnections: []osistypes.PipelineEndpointConnection{osisConn(osisPipeARN, id, osistypes.PipelineEndpointStatusActive)},
			NextToken:                   ptrStr("same"),
		}, nil
	}
	if _, err := scanOSISTopLevel(t, st, h); err != nil {
		t.Fatalf("err = %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if n := len(rowsByNativeID(t, st, TypeOSISPipelineEndpointConnection)); n != 2 {
		t.Errorf("connection rows = %d, want 2", n)
	}
}

// A side's transient error is demoted to a warning under its op instead of
// buried in the other side's hard error.
func TestScanOSIS_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	cases := []struct {
		name, transientOp, hardOp, warnOp string
	}{
		{"throttled pipeline phase", "ListPipelines", "ListPipelineEndpointConnections", osisListPipelinesOp},
		{"throttled blueprint phase", "ListPipelineBlueprints", "ListPipelines", osisListPipelineBlueprintsOp},
		{"throttled endpoint phase", "ListPipelineEndpoints", "ListPipelines", osisListPipelineEndpointsOp},
		{"throttled connection phase", "ListPipelineEndpointConnections", "ListPipelines", osisEndpointConnectionsOp},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := osisHandlers(&osisRecorder{})
			h[c.transientOp] = func(any) (any, error) { return nil, throttle }
			h[c.hardOp] = func(any) (any, error) { return nil, errOSISTest }
			warnings := countOpWarnings(st, c.warnOp)
			_, err := scanOSISTopLevel(t, st, h)
			if !errors.Is(err, errOSISTest) {
				t.Fatalf("err = %v, want the %s error", err, c.hardOp)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.warnOp, *warnings)
			}
		})
	}
}

// A denied blueprint or endpoint listing is warned about once, under its op
// and the account and region, and is not returned.
func TestScanOSIS_PhaseDenialWarnsUnderOp(t *testing.T) {
	cases := []struct{ apiOp, warnOp string }{
		{"ListPipelineBlueprints", osisListPipelineBlueprintsOp},
		{"ListPipelineEndpoints", osisListPipelineEndpointsOp},
	}
	for _, c := range cases {
		t.Run(c.apiOp, func(t *testing.T) {
			st := newTestStore(t)
			var warns []store.ScanWarning
			st.OnWarn = func(w store.ScanWarning) { warns = append(warns, w) }
			h := osisHandlers(&osisRecorder{})
			h[c.apiOp] = func(any) (any, error) { return nil, osisTestDenied }
			total, err := scanOSISTopLevel(t, st, h)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if total != osisOlderRows-1+2 {
				t.Errorf("total = %d, want %d", total, osisOlderRows-1+2)
			}
			if len(warns) != 1 || warns[0].Service != c.warnOp || warns[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warnings = %+v, want one under %s", warns, c.warnOp)
			}
		})
	}
}

const osisMetricsPipeARN = "arn:aws:osis:us-east-1:123456789012:pipeline/metrics"

// osisTaggedPipeline is a tagged ACTIVE pipeline; osisUntaggedPipeline has
// neither tags nor a status.
var (
	osisTaggedPipeline = osistypes.PipelineSummary{
		PipelineArn: ptrStr(osisPipeARN), PipelineName: ptrStr("logs"), Status: osistypes.PipelineStatusActive,
		Tags: []osistypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}, {Key: ptrStr("team"), Value: ptrStr("")}},
	}
	osisUntaggedPipeline = osistypes.PipelineSummary{PipelineArn: ptrStr(osisMetricsPipeARN), PipelineName: ptrStr("metrics")}
)

// osisPipelinePages answers ListPipelines with two pages: the tagged pipeline
// and an element without a PipelineArn, then the untagged pipeline. A request
// with another MaxResults or token is refused.
func osisPipelinePages(in any) (any, error) {
	req := in.(*osis.ListPipelinesInput)
	if sdkaws.ToInt32(req.MaxResults) != 100 {
		return nil, errOSISUnexpectedInput
	}
	switch sv(req.NextToken) {
	case "":
		return &osis.ListPipelinesOutput{
			Pipelines: []osistypes.PipelineSummary{osisTaggedPipeline, {PipelineName: ptrStr("noarn")}},
			NextToken: ptrStr("p2"),
		}, nil
	case "p2":
		return &osis.ListPipelinesOutput{Pipelines: []osistypes.PipelineSummary{osisUntaggedPipeline}}, nil
	}
	return nil, errOSISUnexpectedInput
}

// Each listed pipeline is stored from its ListPipelines element: NativeID the
// ARN, Name, Status when it has one, the element verbatim as attributes, and
// its tags. An element without an ARN is not stored and is warned about once
// under its name.
func TestScanOSIS_PipelineRows(t *testing.T) {
	st := newTestStore(t)
	var warns []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warns = append(warns, w) }
	h := osisHandlers(&osisRecorder{})
	h["ListPipelines"] = osisPipelinePages
	total, err := scanOSISTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != osisOlderRows+1+2 {
		t.Errorf("total = %d, want %d", total, osisOlderRows+1+2)
	}
	rows := rowsByNativeID(t, st, TypeOSISPipeline)
	if len(rows) != 2 {
		t.Fatalf("pipeline rows = %v, want 2", rows)
	}
	logs, metrics := rows[osisPipeARN], rows[osisMetricsPipeARN]
	if sv(logs.Name) != "logs" || sv(logs.Status) != "ACTIVE" || sv(logs.Region) != testRegion {
		t.Errorf("logs row = name %q status %q region %q", sv(logs.Name), sv(logs.Status), sv(logs.Region))
	}
	assertJSONEqual(t, "logs attributes", logs.AttributesJSON, mustJSON(osisTaggedPipeline))
	assertTagsMap(t, logs.TagsJSON, map[string]string{"env": "prod", "team": ""})
	if sv(metrics.Name) != "metrics" || metrics.Status != nil || metrics.TagsJSON != nil {
		t.Errorf("metrics row = name %q status %v tags %v; want metrics, nil, nil", sv(metrics.Name), metrics.Status, metrics.TagsJSON)
	}
	assertJSONEqual(t, "metrics attributes", metrics.AttributesJSON, mustJSON(osisUntaggedPipeline))
	if len(warns) != 1 || warns[0].Service != osisListPipelinesOp || warns[0].Scope != testAccountID+"/"+testRegion ||
		!strings.Contains(warns[0].Message, "noarn") {
		t.Errorf("warnings = %+v, want one %s warning naming noarn", warns, osisListPipelinesOp)
	}
}

// A failed later ListPipelines page keeps the pipelines read before it, which
// still parent their connections, and every other phase still runs: a denial
// warns once and is not returned, any other error is returned.
func TestScanOSIS_PipelineLaterPageFails(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   error
		wantWarns int
	}{
		{"denied", osisTestDenied, nil, 1},
		{"other error", errOSISTest, errOSISTest, 0},
		{"repeated token", nil, errRepeatedNextToken, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, osisListPipelinesOp)
			h := osisHandlers(&osisRecorder{})
			h["ListPipelines"] = func(in any) (any, error) {
				if sv(in.(*osis.ListPipelinesInput).NextToken) != "p2" {
					return osisPipelinePages(in)
				}
				if c.err != nil {
					return nil, c.err
				}
				return &osis.ListPipelinesOutput{NextToken: ptrStr("p2")}, nil
			}
			total, err := scanOSISTopLevel(t, st, h)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != osisOlderRows+2 {
				t.Errorf("total = %d, want %d", total, osisOlderRows+2)
			}
			if *warns != c.wantWarns+1 {
				t.Errorf("%s warnings = %d, want %d (+1 for noarn)", osisListPipelinesOp, *warns, c.wantWarns+1)
			}
			rows := rowsByNativeID(t, st, TypeOSISPipeline)
			if _, ok := rows[osisPipeARN]; !ok || len(rows) != 1 {
				t.Errorf("pipeline rows = %v, want only %s", rows, osisPipeARN)
			}
			assertContainsNative(t, st, osisPipeARN, osisConn1Native)
		})
	}
}

// A failed pipeline write stops the scan before any later phase, and warns
// about nothing: no row was stored.
func TestScanOSIS_PipelineWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeOSISPipeline)
	warns := countOpWarnings(st, osisListPipelinesOp)
	h := osisHandlers(&osisRecorder{})
	h["ListPipelines"] = osisPipelinePages
	var later []string
	for _, op := range []string{"ListPipelineBlueprints", "ListPipelineEndpoints", "ListPipelineEndpointConnections"} {
		h[op] = func(any) (any, error) {
			later = append(later, op)
			return nil, errOSISTest
		}
	}
	total, err := scanOSISTopLevel(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	if total != 0 || len(later) != 0 || *warns != 0 {
		t.Errorf("total, later ops, warnings = %d, %v, %d; want 0, none, 0", total, later, *warns)
	}
}

// A failed write in a later phase stops the scan before the next phase, with
// the earlier phases' rows counted.
func TestScanOSIS_LaterPhaseWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeOSISPipelineBlueprint)
	h := osisHandlers(&osisRecorder{})
	var later []string
	for _, op := range []string{"ListPipelineEndpoints", "ListPipelineEndpointConnections"} {
		h[op] = func(any) (any, error) {
			later = append(later, op)
			return nil, errOSISTest
		}
	}
	total, err := scanOSISTopLevel(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	if total != 1 || len(later) != 0 {
		t.Errorf("total, later ops = %d, %v; want 1 (the pipeline), none", total, later)
	}
}

// When ctx ends on the first ListPipelines page, the second is not read: the
// first page's pipeline is stored and the stop is returned. A ctx stop is not
// a store write failure, so each later phase still makes its first call, which
// fails as the SDK's transport fails a call under a done ctx.
func TestScanOSIS_ContextEndsDuringPipelines(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called []string
	h := map[string]func(context.Context, any) (any, error){
		"ListPipelines": func(ctx context.Context, in any) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			cancel()
			return osisPipelinePages(in)
		},
	}
	for _, op := range []string{"ListPipelineBlueprints", "ListPipelineEndpoints", "ListPipelineEndpointConnections"} {
		h[op] = func(ctx context.Context, _ any) (any, error) {
			called = append(called, op)
			return nil, ctx.Err()
		}
	}
	total, err := scanOSISCtx(ctx, t, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (the first page's pipeline; every later call fails)", total)
	}
	if want := []string{"ListPipelineBlueprints", "ListPipelineEndpoints", "ListPipelineEndpointConnections"}; !slices.Equal(called, want) {
		t.Errorf("later calls = %v, want %v", called, want)
	}
	rows := rowsByNativeID(t, st, TypeOSISPipeline)
	if _, ok := rows[osisPipeARN]; !ok || len(rows) != 1 {
		t.Errorf("pipeline rows = %v, want only %s", rows, osisPipeARN)
	}
}
