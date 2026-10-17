package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
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
		"ListPipelines": func(any) (any, error) {
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

func scanOSISTopLevel(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, osisTestDenied)
	total, _, err := scanOSIS(context.Background(), acct, testRegion, st, testScanID)
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

// A hard error in either side is returned, and the other side's rows are
// stored: the connection phase runs after the older phases, and runs even
// when they fail (its rows are stored unlinked when ListPipelines fails).
func TestScanOSIS_PhaseErrorPropagates(t *testing.T) {
	cases := []struct {
		op        string
		wantTotal int
		wantConns int
	}{
		{"ListPipelines", 2, 2},
		{"ListPipelineEndpoints", 2 + 2, 2},
		{"ListPipelineEndpointConnections", osisOlderRows, 0},
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

// A side's transient error is demoted to a warning, so it cannot make the
// dispatcher read the other side's hard error as transient.
func TestScanOSIS_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	cases := []struct {
		name, transientOp, hardOp, warnOp string
	}{
		{"throttled older phase", "ListPipelines", "ListPipelineEndpointConnections", "osis"},
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
