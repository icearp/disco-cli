package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/datapipeline"
	dptypes "github.com/aws/aws-sdk-go-v2/service/datapipeline/types"
	"github.com/icearp/disco-cli/store"
)

var (
	dpTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: datapipeline:DescribePipelines")
	errDPTest    = errors.New("datapipeline test failure")
)

// stubDataPipeline answers ListPipelines from pages keyed by the Marker sent
// ("" for the first) and DescribePipelines with one description per id,
// from desc when present, else with the id as its Name. listErr fails the
// page of its marker; describeErr fails the batch starting at its id. Calls
// are recorded.
type stubDataPipeline struct {
	pages       map[string]*datapipeline.ListPipelinesOutput
	desc        map[string]dptypes.PipelineDescription
	listErr     map[string]error
	describeErr map[string]error
	gone        map[string]error // fails every batch holding the id
	onDescribe  func()           // called at the start of every DescribePipelines call when set

	markers []string
	batches [][]string
}

func (s *stubDataPipeline) ListPipelines(_ context.Context, in *datapipeline.ListPipelinesInput, _ ...func(*datapipeline.Options)) (*datapipeline.ListPipelinesOutput, error) {
	marker := sv(in.Marker)
	s.markers = append(s.markers, marker)
	if err := s.listErr[marker]; err != nil {
		return nil, err
	}
	if out, ok := s.pages[marker]; ok {
		return out, nil
	}
	return &datapipeline.ListPipelinesOutput{}, nil
}

func (s *stubDataPipeline) DescribePipelines(_ context.Context, in *datapipeline.DescribePipelinesInput, _ ...func(*datapipeline.Options)) (*datapipeline.DescribePipelinesOutput, error) {
	if s.onDescribe != nil {
		s.onDescribe()
	}
	s.batches = append(s.batches, slices.Clone(in.PipelineIds))
	if err := s.describeErr[in.PipelineIds[0]]; err != nil {
		return nil, err
	}
	for _, id := range in.PipelineIds {
		if err := s.gone[id]; err != nil {
			return nil, err
		}
	}
	out := &datapipeline.DescribePipelinesOutput{}
	for _, id := range in.PipelineIds {
		d, ok := s.desc[id]
		if !ok {
			d = dptypes.PipelineDescription{PipelineId: ptrStr(id), Name: ptrStr(id)}
		}
		out.PipelineDescriptionList = append(out.PipelineDescriptionList, d)
	}
	return out, nil
}

func dpNativeID(id string) string {
	return "arn:aws:datapipeline:" + testRegion + ":" + testAccountID + ":pipeline/" + id
}

// dpTwoPageStub lists df-1 (tagged) and an element without an Id on the
// first page, then df-2 … df-26 on the second: 26 ids, two describe batches
// starting at df-1 and df-26.
func dpTwoPageStub() *stubDataPipeline {
	var second []dptypes.PipelineIdName
	for i := 2; i <= 26; i++ {
		id := fmt.Sprintf("df-%d", i)
		second = append(second, dptypes.PipelineIdName{Id: ptrStr(id), Name: ptrStr(id)})
	}
	return &stubDataPipeline{
		pages: map[string]*datapipeline.ListPipelinesOutput{
			"": {
				PipelineIdList: []dptypes.PipelineIdName{{Id: ptrStr("df-1"), Name: ptrStr("nightly")}, {Name: ptrStr("no-id")}},
				Marker:         ptrStr("m2"), HasMoreResults: true,
			},
			"m2": {PipelineIdList: second},
		},
		desc: map[string]dptypes.PipelineDescription{
			"df-1": {
				PipelineId: ptrStr("df-1"), Name: ptrStr("nightly"), Description: ptrStr("etl"),
				Fields: []dptypes.Field{{Key: ptrStr("@pipelineState"), StringValue: ptrStr("SCHEDULED")}},
				Tags:   []dptypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}, {Key: ptrStr("team"), Value: ptrStr("data")}},
			},
		},
	}
}

func scanDPWith(t *testing.T, st *store.Store, stub *stubDataPipeline) (int, int, error) {
	t.Helper()
	return scanDataPipelinePipelines(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
}

func TestScanDataPipelinePipelines_StoresDescriptionsWithTags(t *testing.T) {
	st := newTestStore(t)
	listWarns := countOpWarnings(st, dataPipelineListOp)
	describeWarns := countOpWarnings(st, dataPipelineDescribeOp)
	stub := dpTwoPageStub()
	stub.desc["df-2"] = dptypes.PipelineDescription{Name: ptrStr("df-2")}
	total, inserted, err := scanDPWith(t, st, stub)
	if err != nil {
		t.Fatalf("scanDataPipelinePipelines: %v", err)
	}
	if total != 25 || inserted != 25 {
		t.Errorf("total, inserted = %d, %d; want 25, 25 (26 described, one without a PipelineId)", total, inserted)
	}
	if *listWarns != 1 || *describeWarns != 1 {
		t.Errorf("warnings: %s %d, %s %d; want 1 each for the element without an id", dataPipelineListOp, *listWarns, dataPipelineDescribeOp, *describeWarns)
	}
	if !slices.Equal(stub.markers, []string{"", "m2"}) {
		t.Errorf("markers sent = %q, want [\"\" \"m2\"]", stub.markers)
	}
	if len(stub.batches) != 2 || len(stub.batches[0]) != 25 || !slices.Equal(stub.batches[1], []string{"df-26"}) {
		t.Errorf("describe batches = %v, want 25 ids then [df-26]", stub.batches)
	}

	rows := rowsByNativeID(t, st, TypeDataPipelinePipeline)
	if len(rows) != 25 {
		t.Fatalf("rows = %d, want 25", len(rows))
	}
	tagged, ok := rows[dpNativeID("df-1")]
	if !ok {
		t.Fatalf("no row %s", dpNativeID("df-1"))
	}
	if sv(tagged.Name) != "nightly" || sv(tagged.Region) != testRegion {
		t.Errorf("df-1 name, region = %q, %q; want nightly, %s", sv(tagged.Name), sv(tagged.Region), testRegion)
	}
	var attrs dptypes.PipelineDescription
	if err := json.Unmarshal([]byte(tagged.AttributesJSON), &attrs); err != nil {
		t.Fatalf("df-1 attrs: %v", err)
	}
	if sv(attrs.Description) != "etl" || len(attrs.Fields) != 1 || sv(attrs.Fields[0].StringValue) != "SCHEDULED" || len(attrs.Tags) != 2 {
		t.Errorf("df-1 attrs = %s; want the description verbatim, Tags included", tagged.AttributesJSON)
	}
	assertTagsMap(t, tagged.TagsJSON, map[string]string{"env": "prod", "team": "data"})
	if plain := rows[dpNativeID("df-26")]; sv(plain.Name) != "df-26" || plain.TagsJSON != nil {
		t.Errorf("df-26 name, tags = %q, %v; want df-26, nil", sv(plain.Name), plain.TagsJSON)
	}
}

// A failed later list page keeps the pipelines of the pages read before it: a
// denial warns, any other error is returned.
func TestScanDataPipelinePipelines_ListErrorKeepsEarlierPages(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		wantErr   error
		wantWarns int // the element without an Id, plus the denial
	}{
		{"denied", dpTestDenied, nil, 2},
		{"hard error", errDPTest, errDPTest, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			listWarns := countOpWarnings(st, dataPipelineListOp)
			stub := dpTwoPageStub()
			stub.listErr = map[string]error{"m2": c.err}
			total, _, err := scanDPWith(t, st, stub)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != 1 {
				t.Errorf("total = %d, want 1", total)
			}
			if _, ok := rowsByNativeID(t, st, TypeDataPipelinePipeline)[dpNativeID("df-1")]; !ok {
				t.Error("df-1 from the first page missing")
			}
			if *listWarns != c.wantWarns {
				t.Errorf("%s warnings = %d, want %d", dataPipelineListOp, *listWarns, c.wantWarns)
			}
		})
	}
}

// A failed describe batch costs only its own pipelines: a denial warns once,
// any other error, a modeled not-found included, is returned.
// A batch failing with a code that does not name its pipeline is described
// again one id at a time: only the ids gone alone are skipped, silently, and
// an id failing otherwise alone is returned while its batch-mates are kept.
func TestScanDataPipelinePipelines_GoneBatchDescribedPerID(t *testing.T) {
	gone := map[string]error{
		"df-3": apiErr("PipelineNotFoundException", "gone"),
		"df-7": apiErr("PipelineDeletedException", "deleted"),
	}
	for _, c := range []struct {
		name        string
		describeErr map[string]error
		wantErr     error // nil: the scan returns nil
		wantMissing []string
	}{
		{"only gone ids", nil, nil, []string{"df-3", "df-7"}},
		{"another id fails alone", map[string]error{"df-4": errDPTest}, errDPTest, []string{"df-3", "df-4", "df-7"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			describeWarns := countOpWarnings(st, dataPipelineDescribeOp)
			stub := dpTwoPageStub()
			stub.gone, stub.describeErr = gone, c.describeErr
			total, _, err := scanDPWith(t, st, stub)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			rows := rowsByNativeID(t, st, TypeDataPipelinePipeline)
			if want := 26 - len(c.wantMissing); total != want || len(rows) != want {
				t.Errorf("total = %d, rows = %d; want %d", total, len(rows), want)
			}
			for _, id := range c.wantMissing {
				if _, ok := rows[dpNativeID(id)]; ok {
					t.Errorf("%s stored; want it skipped", id)
				}
			}
			for _, id := range []string{"df-1", "df-2", "df-25", "df-26"} {
				if _, ok := rows[dpNativeID(id)]; !ok {
					t.Errorf("%s missing; want it kept", id)
				}
			}
			if *describeWarns != 0 {
				t.Errorf("%s warnings = %d, want 0", dataPipelineDescribeOp, *describeWarns)
			}
		})
	}
}

func TestScanDataPipelinePipelines_DescribeErrorKeepsOtherBatches(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		wantErr   error // nil: the scan returns nil
		wantWarns int
	}{
		{"denied", dpTestDenied, nil, 1},
		{"hard error", errDPTest, errDPTest, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			describeWarns := countOpWarnings(st, dataPipelineDescribeOp)
			stub := dpTwoPageStub()
			stub.describeErr = map[string]error{"df-1": c.err}
			total, _, err := scanDPWith(t, st, stub)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			rows := rowsByNativeID(t, st, TypeDataPipelinePipeline)
			if _, ok := rows[dpNativeID("df-26")]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want only df-26 from the second batch", total, rows)
			}
			if *describeWarns != c.wantWarns {
				t.Errorf("%s warnings = %d, want %d", dataPipelineDescribeOp, *describeWarns, c.wantWarns)
			}
		})
	}
}

// An account the closed service never onboarded is not entitled; the same
// empty-message denial after a page was read only warns.
func TestScanDataPipelinePipelines_ClosedToNewCustomers(t *testing.T) {
	closed := apiErr("AccessDeniedException", "")
	st := newTestStore(t)
	_, _, err := scanDPWith(t, st, &stubDataPipeline{listErr: map[string]error{"": closed}})
	if !errors.Is(err, errServiceNotEntitled) {
		t.Errorf("first page closed: err = %v, want errServiceNotEntitled", err)
	}

	st = newTestStore(t)
	stub := dpTwoPageStub()
	stub.listErr = map[string]error{"m2": closed}
	total, _, err := scanDPWith(t, st, stub)
	if err != nil || total != 1 {
		t.Errorf("later page closed: total, err = %d, %v; want 1, nil", total, err)
	}
}

func TestScanDataPipelinePipelines_RepeatedMarker(t *testing.T) {
	st := newTestStore(t)
	stub := dpTwoPageStub()
	stub.pages["m2"].Marker = ptrStr("m2")
	total, _, err := scanDPWith(t, st, stub)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if total != 26 {
		t.Errorf("total = %d, want 26: the pages read are kept", total)
	}
}

func TestScanDataPipelinePipelines_None(t *testing.T) {
	st := newTestStore(t)
	stub := &stubDataPipeline{}
	total, inserted, err := scanDPWith(t, st, stub)
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("got (%d,%d,%v), want (0,0,nil)", total, inserted, err)
	}
	if len(stub.batches) != 0 {
		t.Errorf("describe batches = %v, want none", stub.batches)
	}
}

// No warning is reported for pipelines a failed write lost.
func TestScanDataPipelinePipelines_StoreWriteFailureWarnsNothing(t *testing.T) {
	st, closeStore := openClosingStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	stub := dpTwoPageStub()
	stub.describeErr = map[string]error{"df-26": dpTestDenied}
	stub.onDescribe = closeStore
	_, _, err := scanDPWith(t, st, stub)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	for _, w := range warnings {
		if w.Service != "store" {
			t.Errorf("warning %+v after the failed write, want none", w)
		}
	}
}

// Once ctx is done no further batch, or id of a gone batch, is described:
// the pipelines described before are stored and the error says the rest were
// not described.
func TestScanDataPipelinePipelines_StopsDescribingWhenContextDone(t *testing.T) {
	for _, c := range []struct {
		name      string
		gone      map[string]error
		cancelAt  int // the DescribePipelines call, counted from 0, during which ctx is cancelled
		wantRows  int
		wantCalls int
	}{
		{"between batches", nil, 0, 25, 1},
		{"between ids of a gone batch", map[string]error{"df-3": apiErr("PipelineNotFoundException", "gone")}, 1, 1, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stub := dpTwoPageStub()
			stub.gone = c.gone
			// Cancel during the call answering the first batch, or the first
			// lone id; the stub still answers it.
			stub.onDescribe = func() {
				if len(stub.batches) == c.cancelAt {
					cancel()
				}
			}
			total, _, err := scanDataPipelinePipelines(ctx, stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			if total != c.wantRows || len(rowsByNativeID(t, st, TypeDataPipelinePipeline)) != c.wantRows {
				t.Errorf("total = %d, want %d stored", total, c.wantRows)
			}
			if len(stub.batches) != c.wantCalls {
				t.Errorf("DescribePipelines calls = %v, want %d", stub.batches, c.wantCalls)
			}
		})
	}
}
