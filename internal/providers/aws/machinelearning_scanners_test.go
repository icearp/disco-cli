package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/machinelearning"
	mltypes "github.com/aws/aws-sdk-go-v2/service/machinelearning/types"
	"github.com/icearp/disco-cli/regions"
	"github.com/icearp/disco-cli/store"
)

var mlTestCreated = time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)

// mlKind builds one Describe* operation's output from element ids. An empty
// id yields an element without one; every element is COMPLETED and named
// "<id>-name".
type mlKind struct {
	op, label, typ, kind string
	page                 func(ids []string, next *string) any
}

var mlKinds = []mlKind{
	{"DescribeDataSources", mlDescribeDataSourcesOp, TypeMachineLearningDataSource, "datasource", func(ids []string, next *string) any {
		out := &machinelearning.DescribeDataSourcesOutput{NextToken: next}
		for _, id := range ids {
			out.Results = append(out.Results, mltypes.DataSource{DataSourceId: mlOptID(id), Name: sdkaws.String(id + "-name"), Status: mltypes.EntityStatusCompleted, CreatedAt: &mlTestCreated, DataLocationS3: sdkaws.String("s3://bucket/" + id)})
		}
		return out
	}},
	{"DescribeMLModels", mlDescribeMLModelsOp, TypeMachineLearningMLModel, "mlmodel", func(ids []string, next *string) any {
		out := &machinelearning.DescribeMLModelsOutput{NextToken: next}
		for _, id := range ids {
			out.Results = append(out.Results, mltypes.MLModel{MLModelId: mlOptID(id), Name: sdkaws.String(id + "-name"), Status: mltypes.EntityStatusCompleted, CreatedAt: &mlTestCreated, InputDataLocationS3: sdkaws.String("s3://bucket/" + id)})
		}
		return out
	}},
	{"DescribeEvaluations", mlDescribeEvaluationsOp, TypeMachineLearningEvaluation, "evaluation", func(ids []string, next *string) any {
		out := &machinelearning.DescribeEvaluationsOutput{NextToken: next}
		for _, id := range ids {
			out.Results = append(out.Results, mltypes.Evaluation{EvaluationId: mlOptID(id), Name: sdkaws.String(id + "-name"), Status: mltypes.EntityStatusCompleted, CreatedAt: &mlTestCreated, InputDataLocationS3: sdkaws.String("s3://bucket/" + id)})
		}
		return out
	}},
	{"DescribeBatchPredictions", mlDescribeBatchPredictionsOp, TypeMachineLearningBatchPrediction, "batchprediction", func(ids []string, next *string) any {
		out := &machinelearning.DescribeBatchPredictionsOutput{NextToken: next}
		for _, id := range ids {
			out.Results = append(out.Results, mltypes.BatchPrediction{BatchPredictionId: mlOptID(id), Name: sdkaws.String(id + "-name"), Status: mltypes.EntityStatusCompleted, CreatedAt: &mlTestCreated, InputDataLocationS3: sdkaws.String("s3://bucket/" + id)})
		}
		return out
	}},
}

func mlOptID(id string) *string {
	if id == "" {
		return nil
	}
	return sdkaws.String(id)
}

func mlTestNativeID(kind, id string) string {
	return "arn:aws:machinelearning:" + testRegion + ":" + testAccountID + ":" + kind + "/" + id
}

// mlCalls records every Limit and NextToken each operation was sent.
type mlCalls struct {
	mu     sync.Mutex
	limits map[string][]int32
	tokens map[string][]string
}

func (c *mlCalls) add(op string, limit *int32, token *string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limits == nil {
		c.limits, c.tokens = map[string][]int32{}, map[string][]string{}
	}
	c.limits[op] = append(c.limits[op], sdkaws.ToInt32(limit))
	c.tokens[op] = append(c.tokens[op], sdkaws.ToString(token))
	return len(c.tokens[op]) - 1
}

func (c *mlCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.tokens[op])
}

// mlInput reads Limit and NextToken from any of the four Describe inputs.
func mlInput(in any) (limit *int32, token *string) {
	switch v := in.(type) {
	case *machinelearning.DescribeDataSourcesInput:
		return v.Limit, v.NextToken
	case *machinelearning.DescribeMLModelsInput:
		return v.Limit, v.NextToken
	case *machinelearning.DescribeEvaluationsInput:
		return v.Limit, v.NextToken
	case *machinelearning.DescribeBatchPredictionsInput:
		return v.Limit, v.NextToken
	}
	panic("unexpected input type")
}

// mlHandlers answers each kind's operation with pages[op][i] on the i-th
// paged call, chaining pages by NextToken "t1", "t2", ...; an operation with
// no entry returns one empty page. The gate's Limit=1 probe is answered empty
// and not counted as a page. errs[op] replaces the reply to that operation's
// paged calls.
func mlHandlers(t *testing.T, calls *mlCalls, pages map[string][][]string, errs map[string]error) map[string]func(in any) (any, error) {
	t.Helper()
	h := map[string]func(in any) (any, error){}
	for _, k := range mlKinds {
		h[k.op] = func(in any) (any, error) {
			limit, token := mlInput(in)
			if k.op == "DescribeDataSources" && sdkaws.ToInt32(limit) == 1 {
				return k.page(nil, nil), nil
			}
			i := calls.add(k.op, limit, token)
			if err := errs[k.op]; err != nil {
				return nil, err
			}
			want := ""
			if i > 0 {
				want = "t" + strconv.Itoa(i)
			}
			if got := sdkaws.ToString(token); got != want {
				t.Errorf("%s call %d NextToken = %q, want %q", k.op, i+1, got, want)
			}
			p := pages[k.op]
			if len(p) == 0 {
				return k.page(nil, nil), nil
			}
			if i >= len(p) {
				t.Fatalf("%s called %d times, want %d", k.op, i+1, len(p))
			}
			var next *string
			if i+1 < len(p) {
				next = sdkaws.String("t" + strconv.Itoa(i+1))
			}
			return k.page(p[i], next), nil
		}
	}
	return h
}

func runML(t *testing.T, st *store.Store, h map[string]func(in any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	return scanMachineLearning(context.Background(), acct, testRegion, st, testScanID)
}

// mlWarnings counts every warning by its Service label.
func mlWarnings(st *store.Store) map[string]int {
	var mu sync.Mutex
	got := map[string]int{}
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		got[w.Service]++
	}
	return got
}

func TestScanMachineLearning_TwoPagesStored(t *testing.T) {
	for _, k := range mlKinds {
		t.Run(k.kind, func(t *testing.T) {
			var calls mlCalls
			st := newTestStore(t)
			warns := mlWarnings(st)
			h := mlHandlers(t, &calls, map[string][][]string{k.op: {{"a1", ""}, {"b2"}}}, nil)
			total, inserted, err := runML(t, st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 2 || inserted != 2 {
				t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
			}
			if !slices.Equal(calls.limits[k.op], []int32{100, 100}) {
				t.Errorf("%s Limit per call = %v, want [100 100]", k.op, calls.limits[k.op])
			}
			if len(warns) != 1 || warns[k.label] != 1 {
				t.Errorf("warnings = %v, want one on %s for the element without an id", warns, k.label)
			}
			rows := rowsByNativeID(t, st, k.typ)
			if len(rows) != 2 {
				t.Fatalf("stored %d rows, want 2: %v", len(rows), rows)
			}
			a, ok := rows[mlTestNativeID(k.kind, "a1")]
			if !ok {
				t.Fatalf("row %s missing; have %v", mlTestNativeID(k.kind, "a1"), rows)
			}
			if a.Type != k.typ || sdkaws.ToString(a.Region) != testRegion {
				t.Errorf("Type, Region = %q, %q", a.Type, sdkaws.ToString(a.Region))
			}
			if sdkaws.ToString(a.Name) != "a1-name" || sdkaws.ToString(a.Status) != "COMPLETED" || sdkaws.ToString(a.CreatedAt) != "2025-02-03T04:05:06Z" {
				t.Errorf("Name, Status, CreatedAt = %q, %q, %q", sdkaws.ToString(a.Name), sdkaws.ToString(a.Status), sdkaws.ToString(a.CreatedAt))
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
				t.Fatalf("attrs: %v", err)
			}
			loc := attrs["DataLocationS3"]
			if loc == nil {
				loc = attrs["InputDataLocationS3"]
			}
			if loc != "s3://bucket/a1" {
				t.Errorf("attrs = %s, want the S3 location kept", a.AttributesJSON)
			}
			if _, ok := rows[mlTestNativeID(k.kind, "b2")]; !ok {
				t.Error("second-page row missing")
			}
		})
	}
}

func TestScanMachineLearning_EmptyStoresNothing(t *testing.T) {
	var calls mlCalls
	st := newTestStore(t)
	warns := mlWarnings(st)
	total, inserted, err := runML(t, st, mlHandlers(t, &calls, nil, nil))
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("got (%d, %d, %v), want (0, 0, nil)", total, inserted, err)
	}
	for _, k := range mlKinds {
		if n := calls.count(k.op); n != 1 {
			t.Errorf("%s calls = %d, want 1", k.op, n)
		}
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

// mlClosedAnswers are the denials isMachineLearningClosed treats as an account
// Amazon ML does not serve: the message reported in rusoto issue #1306 (2019),
// verbatim, and the repo-wide empty-message shape.
var mlClosedAnswers = []struct{ name, msg string }{
	{"rusoto message", "Thank you for your interest in Amazon Machine Learning. AmazonML is no longer available to new customers. Please consider using Amazon SageMaker."},
	{"empty message", ""},
}

// A closed answer to the gate ends the scan as not entitled, silently, before
// any phase runs.
func TestScanMachineLearning_ClosedAccountNotEntitled(t *testing.T) {
	for _, c := range mlClosedAnswers {
		t.Run(c.name, func(t *testing.T) {
			var calls mlCalls
			st := newTestStore(t)
			warns := mlWarnings(st)
			h := mlHandlers(t, &calls, nil, nil)
			h["DescribeDataSources"] = func(any) (any, error) { return nil, apiErr("AccessDeniedException", c.msg) }
			total, inserted, err := runML(t, st, h)
			if !errors.Is(err, errServiceNotEntitled) {
				t.Fatalf("err = %v, want errServiceNotEntitled", err)
			}
			if o := classifyServiceError(err); o != outcomeNotEntitled {
				t.Errorf("classifyServiceError = %v, want outcomeNotEntitled", o)
			}
			if total != 0 || inserted != 0 {
				t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
			}
			if len(warns) != 0 {
				t.Errorf("warnings = %v, want none", warns)
			}
			for _, k := range mlKinds[1:] {
				if n := calls.count(k.op); n != 0 {
					t.Errorf("%s called %d times after the gate closed the scan", k.op, n)
				}
			}
		})
	}
}

// A closed answer on one phase after the gate passed skips that phase silently
// and keeps the others.
func TestScanMachineLearning_ClosedPhaseSkippedSilently(t *testing.T) {
	for _, c := range mlClosedAnswers {
		t.Run(c.name, func(t *testing.T) {
			var calls mlCalls
			st := newTestStore(t)
			warns := mlWarnings(st)
			h := mlHandlers(t, &calls,
				map[string][][]string{"DescribeDataSources": {{"d1"}}, "DescribeEvaluations": {{"e1"}}},
				map[string]error{"DescribeMLModels": apiErr("AccessDeniedException", c.msg)})
			total, _, err := runML(t, st, h)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if total != 2 {
				t.Errorf("total = %d, want 2", total)
			}
			if len(warns) != 0 {
				t.Errorf("warnings = %v, want none", warns)
			}
			if len(rowsByNativeID(t, st, TypeMachineLearningEvaluation)) != 1 {
				t.Error("evaluation after the closed phase not stored")
			}
		})
	}
}

// A gate denial that matches neither closed shape is an IAM denial: the scan
// goes on, and the phase that hits it warns once.
func TestScanMachineLearning_GateIAMDenialNotClosed(t *testing.T) {
	var calls mlCalls
	st := newTestStore(t)
	warns := mlWarnings(st)
	h := mlHandlers(t, &calls, map[string][][]string{"DescribeMLModels": {{"m1"}}}, nil)
	h["DescribeDataSources"] = func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: machinelearning:DescribeDataSources")
	}
	total, _, err := runML(t, st, h)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (the ML model)", total)
	}
	if len(warns) != 1 || warns[mlDescribeDataSourcesOp] != 1 {
		t.Errorf("warnings = %v, want one on %s", warns, mlDescribeDataSourcesOp)
	}
}

func TestScanMachineLearning_DeniedPhaseWarnsAndOthersRun(t *testing.T) {
	for _, k := range mlKinds {
		t.Run(k.kind, func(t *testing.T) {
			var calls mlCalls
			st := newTestStore(t)
			warns := mlWarnings(st)
			pages := map[string][][]string{}
			for _, o := range mlKinds {
				pages[o.op] = [][]string{{"x" + o.kind}}
			}
			h := mlHandlers(t, &calls, pages, map[string]error{
				k.op: apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: "+k.label),
			})
			total, _, err := runML(t, st, h)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if total != 3 {
				t.Errorf("total = %d, want 3 (the other phases)", total)
			}
			if len(warns) != 1 || warns[k.label] != 1 {
				t.Errorf("warnings = %v, want one on %s", warns, k.label)
			}
		})
	}
}

// InvalidInputException is a real client error, never a closed signal: the
// phase fails, the other phases still store and count their rows.
func TestScanMachineLearning_UnmodelledSkipFailsPhase(t *testing.T) {
	for _, k := range mlKinds {
		t.Run(k.kind, func(t *testing.T) {
			var calls mlCalls
			st := newTestStore(t)
			pages := map[string][][]string{}
			for _, o := range mlKinds {
				pages[o.op] = [][]string{{"x" + o.kind}}
			}
			h := mlHandlers(t, &calls, pages, map[string]error{k.op: apiErr("InvalidInputException", "bad filter")})
			total, _, err := runML(t, st, h)
			if !isAPIErrorCode(err, "InvalidInputException") {
				t.Fatalf("err = %v, want the InvalidInputException", err)
			}
			if o := classifyServiceError(err); o != outcomeError {
				t.Errorf("classifyServiceError = %v, want outcomeError", o)
			}
			if total != 3 {
				t.Errorf("total = %d, want 3 (the other phases)", total)
			}
			for _, o := range mlKinds {
				if o.op != k.op && len(rowsByNativeID(t, st, o.typ)) != 1 {
					t.Errorf("%s row not stored", o.kind)
				}
			}
		})
	}
}

// A throttle on one phase is demoted to a warning, so the joined error still
// classifies as the other phase's hard error.
func TestScanMachineLearning_ThrottleDemotedBesideHardError(t *testing.T) {
	var calls mlCalls
	st := newTestStore(t)
	warns := mlWarnings(st)
	h := mlHandlers(t, &calls, nil, map[string]error{
		"DescribeMLModels":         apiErr("ThrottlingException", "Rate exceeded"),
		"DescribeBatchPredictions": apiErr("InvalidInputException", "bad"),
	})
	_, _, err := runML(t, st, h)
	if o := classifyServiceError(err); o != outcomeError {
		t.Errorf("classifyServiceError = %v (err %v), want outcomeError", o, err)
	}
	if isAPIErrorCode(err, "ThrottlingException") {
		t.Errorf("err = %v, want the throttle demoted out of it", err)
	}
	if len(warns) != 1 || warns[mlDescribeMLModelsOp] != 1 {
		t.Errorf("warnings = %v, want one transient warning on %s", warns, mlDescribeMLModelsOp)
	}
}

func TestScanMachineLearning_LaterPageErrorKeepsEarlierRows(t *testing.T) {
	st := newTestStore(t)
	var calls mlCalls
	h := mlHandlers(t, &calls, nil, nil)
	n := 0
	h["DescribeMLModels"] = func(any) (any, error) {
		n++
		if n == 1 {
			return mlKinds[1].page([]string{"m1"}, sdkaws.String("t1")), nil
		}
		return nil, apiErr("InvalidInputException", "bad token")
	}
	total, _, err := runML(t, st, h)
	if !isAPIErrorCode(err, "InvalidInputException") {
		t.Fatalf("err = %v, want the second page's error", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1", total)
	}
	if _, ok := rowsByNativeID(t, st, TypeMachineLearningMLModel)[mlTestNativeID("mlmodel", "m1")]; !ok {
		t.Error("first-page row not stored")
	}
}

func TestScanMachineLearning_RepeatedNextTokenFails(t *testing.T) {
	st := newTestStore(t)
	var calls mlCalls
	h := mlHandlers(t, &calls, nil, nil)
	n := 0
	h["DescribeEvaluations"] = func(any) (any, error) {
		n++
		if n > 2 {
			return nil, errors.New("scanner kept following the repeated token")
		}
		return mlKinds[2].page([]string{"e" + strconv.Itoa(n)}, sdkaws.String("same")), nil
	}
	total, _, err := runML(t, st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if n != 2 {
		t.Errorf("calls = %d, want 2", n)
	}
	if total != 2 || len(rowsByNativeID(t, st, TypeMachineLearningEvaluation)) != 2 {
		t.Errorf("total = %d, want both pages stored", total)
	}
}

// The SDK endpoint table lists two regions; the generated table must keep
// every other region out of dispatch.
func TestScanMachineLearning_RegionsScoped(t *testing.T) {
	if got := regions.ServiceRegions("aws:machinelearning"); !slices.Equal(got, []string{"eu-west-1", "us-east-1"}) {
		t.Errorf("ServiceRegions = %v, want [eu-west-1 us-east-1]", got)
	}
	if known, avail := regions.ServiceAvailable("aws:machinelearning", "us-west-2"); !known || avail {
		t.Errorf("ServiceAvailable(us-west-2) = %v, %v; want known and unavailable", known, avail)
	}
}
