package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/forecast"
	fctypes "github.com/aws/aws-sdk-go-v2/service/forecast/types"
	"github.com/icearp/disco-cli/store"
)

var (
	fcDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: forecast:List")
	fcClosed   = apiErr("AccessDeniedException", "")
	fcInvalid  = apiErr("InvalidInputException", "bad")
	fcThrottle = apiErr("ThrottlingException", "Rate exceeded")
	fcNotFound = apiErr("ResourceNotFoundException", "gone")
)

// fcListing is one listing of the fixture: its op, the type and ARN kind of
// its rows, and its output for one page holding the element of arn and name.
type fcListing struct {
	op, typ, kind string
	out           func(next, arn, name *string) any
}

var fcListings = []fcListing{
	{"ListDatasets", TypeForecastDataset, "dataset", func(next, arn, name *string) any {
		return &forecast.ListDatasetsOutput{NextToken: next, Datasets: []fctypes.DatasetSummary{{DatasetArn: arn, DatasetName: name}}}
	}},
	{"ListDatasetGroups", TypeForecastDatasetGroup, "dataset-group", func(next, arn, name *string) any {
		return &forecast.ListDatasetGroupsOutput{NextToken: next, DatasetGroups: []fctypes.DatasetGroupSummary{{DatasetGroupArn: arn, DatasetGroupName: name}}}
	}},
	{"ListPredictors", TypeForecastPredictor, "predictor", func(next, arn, name *string) any {
		return &forecast.ListPredictorsOutput{NextToken: next, Predictors: []fctypes.PredictorSummary{{PredictorArn: arn, PredictorName: name, Status: sdkaws.String("ACTIVE")}}}
	}},
	{"ListForecasts", TypeForecastForecast, "forecast", func(next, arn, name *string) any {
		return &forecast.ListForecastsOutput{NextToken: next, Forecasts: []fctypes.ForecastSummary{{ForecastArn: arn, ForecastName: name, Status: sdkaws.String("ACTIVE")}}}
	}},
	{"ListExplainabilities", TypeForecastExplainability, "explainability", func(next, arn, name *string) any {
		return &forecast.ListExplainabilitiesOutput{NextToken: next, Explainabilities: []fctypes.ExplainabilitySummary{{ExplainabilityArn: arn, ExplainabilityName: name, Status: sdkaws.String("ACTIVE")}}}
	}},
	{"ListMonitors", TypeForecastMonitor, "monitor", func(next, arn, name *string) any {
		return &forecast.ListMonitorsOutput{NextToken: next, Monitors: []fctypes.MonitorSummary{{MonitorArn: arn, MonitorName: name, Status: sdkaws.String("ACTIVE")}}}
	}},
	{"ListWhatIfAnalyses", TypeForecastWhatIfAnalysis, "what-if-analysis", func(next, arn, name *string) any {
		return &forecast.ListWhatIfAnalysesOutput{NextToken: next, WhatIfAnalyses: []fctypes.WhatIfAnalysisSummary{{WhatIfAnalysisArn: arn, WhatIfAnalysisName: name, Status: sdkaws.String("ACTIVE")}}}
	}},
	{"ListWhatIfForecasts", TypeForecastWhatIfForecast, "what-if-forecast", func(next, arn, name *string) any {
		return &forecast.ListWhatIfForecastsOutput{NextToken: next, WhatIfForecasts: []fctypes.WhatIfForecastSummary{{WhatIfForecastArn: arn, WhatIfForecastName: name, Status: sdkaws.String("ACTIVE")}}}
	}},
}

// fcTestTotal is every listing's two pages of one element each.
const fcTestTotal = 8 * 2

func fcARN(kind, id string) string {
	return "arn:aws:forecast:" + testRegion + ":" + testAccountID + ":" + kind + "/" + id
}

// fcListingOf is the fixture listing of op.
func fcListingOf(t *testing.T, op string) fcListing {
	t.Helper()
	for _, l := range fcListings {
		if l.op == op {
			return l
		}
	}
	t.Fatalf("%s is not a fixture listing", op)
	return fcListing{}
}

// fcCalls records a scan's calls and answers its tag reads and Describes,
// by ARN, with tags or errors.
type fcCalls struct {
	mu           sync.Mutex
	partition    string
	region       string
	maxResults   map[string][]int32
	tagARNs      []string
	tags         map[string]map[string]string
	errs         map[string]error
	describeErrs map[string]error
}

// newFCCalls tags each listing's first-page element with its type.
func newFCCalls() *fcCalls {
	c := &fcCalls{
		partition: "aws", region: testRegion,
		maxResults: map[string][]int32{}, tags: map[string]map[string]string{}, errs: map[string]error{}, describeErrs: map[string]error{},
	}
	for _, l := range fcListings {
		c.tags[fcARN(l.kind, "x1")] = map[string]string{"type": l.typ}
	}
	return c
}

func (c *fcCalls) arn(kind, id string) string {
	return "arn:" + c.partition + ":forecast:" + c.region + ":" + testAccountID + ":" + kind + "/" + id
}

func (c *fcCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var n int32
	if p := bacField[*int32](in, "MaxResults"); p != nil {
		n = *p
	}
	c.maxResults[op] = append(c.maxResults[op], n)
}

func (c *fcCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op])
}

func (c *fcCalls) readARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

// pages answers l's listing: the first page holds element x1 and names a
// next page, which holds x2 and ends the listing.
func (c *fcCalls) pages(l fcListing) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(l.op, in)
		first := bacField[*string](in, "NextToken") == nil
		var next *string
		if first {
			next = sdkaws.String("p2")
		}
		id := pick(first, "x1", "x2")
		return l.out(next, sdkaws.String(c.arn(l.kind, id)), sdkaws.String("name-"+id)), nil
	}
}

func (c *fcCalls) listTags(in any) (any, error) {
	arn := sv(in.(*forecast.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	var tags []fctypes.Tag
	for k, v := range c.tags[arn] {
		tags = append(tags, fctypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &forecast.ListTagsForResourceOutput{Tags: tags}, nil
}

func (c *fcCalls) describeErr(arn string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.describeErrs[arn]
}

// fcKMSKey is the KMS key every described dataset is encrypted with, a field
// only the Describe body carries.
const fcKMSKey = "arn:aws:kms:" + testRegion + ":" + testAccountID + ":key/k1"

func fcHandlers(c *fcCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"DescribeDataset": func(in any) (any, error) {
			arn := in.(*forecast.DescribeDatasetInput).DatasetArn
			c.record("DescribeDataset", in)
			if err := c.describeErr(sv(arn)); err != nil {
				return nil, err
			}
			return &forecast.DescribeDatasetOutput{DatasetArn: arn, EncryptionConfig: &fctypes.EncryptionConfig{KMSKeyArn: sdkaws.String(fcKMSKey)}}, nil
		},
		"DescribeDatasetGroup": func(in any) (any, error) {
			arn := in.(*forecast.DescribeDatasetGroupInput).DatasetGroupArn
			c.record("DescribeDatasetGroup", in)
			if err := c.describeErr(sv(arn)); err != nil {
				return nil, err
			}
			return &forecast.DescribeDatasetGroupOutput{DatasetGroupArn: arn, DatasetArns: []string{c.arn("dataset", "x1")}}, nil
		},
	}
	for _, l := range fcListings {
		h[l.op] = c.pages(l)
	}
	return h
}

// fcScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func fcScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, fcDenied)
	return scanForecast(ctx, acct, testRegion, st, testScanID)
}

func fcWarningOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// fcAttrKeys is the top-level keys of a row's attributes.
func fcAttrKeys(t *testing.T, r store.Resource) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("%s attributes: %v", r.NativeID, err)
	}
	return m
}

// Every listing stores both its pages, each row with the tags its read
// returns (nil for a resource without tags) and its listed name. Datasets and
// dataset groups store their Describe body. ListTagsForResource is sent
// exactly the ARN of every row. Every listing page is sent MaxResults 100.
// No error, no warning.
func TestScanForecast_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newFCCalls()

	total, inserted, err := fcScan(context.Background(), st, fcHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != fcTestTotal || inserted != fcTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, fcTestTotal, fcTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	var wantARNs []string
	for _, l := range fcListings {
		rows := rowsByNativeID(t, st, l.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", l.typ, len(rows))
		}
		for _, id := range []string{"x1", "x2"} {
			arn := fcARN(l.kind, id)
			wantARNs = append(wantARNs, arn)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", l.typ, arn)
				continue
			}
			if sv(r.Name) != "name-"+id || sv(r.Region) != testRegion {
				t.Errorf("%s name, region = %q, %q; want %q, %q", arn, sv(r.Name), sv(r.Region), "name-"+id, testRegion)
			}
			if id == "x1" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": l.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
			keys := fcAttrKeys(t, r)
			switch l.typ {
			case TypeForecastDataset:
				if _, ok := keys["EncryptionConfig"]; !ok {
					t.Errorf("%s attributes lack the Describe body's EncryptionConfig: %s", arn, r.AttributesJSON)
				}
			case TypeForecastDatasetGroup:
				if _, ok := keys["DatasetArns"]; !ok {
					t.Errorf("%s attributes lack the Describe body's DatasetArns: %s", arn, r.AttributesJSON)
				}
			default:
				if sv(r.Status) != "ACTIVE" {
					t.Errorf("%s Status = %q; want ACTIVE", arn, sv(r.Status))
				}
			}
		}
	}
	slices.Sort(wantARNs)
	if got := calls.readARNs(); !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantARNs)
	}
	for _, l := range fcListings {
		want := []int32{100, 100}
		if got := calls.maxResults[l.op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", l.op, got, want)
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanForecast_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := fcScan(context.Background(), st, fcHandlers(newFCCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := fcScan(context.Background(), st, fcHandlers(newFCCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != fcTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, fcTestTotal)
	}
}

// A failed tag read. ResourceNotFoundException, which the op models, drops
// the row silently. A denial keeps the row untagged and warns once under
// the tag op; a throttle keeps it and is transient; any other error keeps it
// and is an error.
func TestScanForecast_TagReadFailures(t *testing.T) {
	for _, x := range []struct{ kind, typ string }{
		{"dataset", TypeForecastDataset},
		{"predictor", TypeForecastPredictor},
		{"what-if-forecast", TypeForecastWhatIfForecast},
	} {
		for _, c := range []struct {
			name    string
			err     error
			outcome serviceOutcome // -1: no error
			code    string
			lost    int
			warnOp  string
		}{
			{"not found", fcNotFound, -1, "", 1, ""},
			{"denied", fcDenied, -1, "", 0, forecastTagsOp},
			{"transient", fcThrottle, outcomeTransient, "ThrottlingException", 0, ""},
			{"other error", fcInvalid, outcomeError, "InvalidInputException", 0, ""},
		} {
			t.Run(x.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newFCCalls()
				arn := fcARN(x.kind, "x1")
				calls.errs[arn] = c.err

				total, _, err := fcScan(context.Background(), st, fcHandlers(calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				if want := fcTestTotal - c.lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				r, ok := rowsByNativeID(t, st, x.typ)[arn]
				if ok != (c.lost == 0) {
					t.Fatalf("row stored = %v; want %v", ok, c.lost == 0)
				}
				if ok && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				w := warns()
				var want []string
				if c.warnOp != "" {
					want = []string{c.warnOp}
				}
				if got := fcWarningOps(w); !slices.Equal(got, want) {
					t.Fatalf("warnings = %v; want %v", w, want)
				}
				for _, x := range w {
					if x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, arn) {
						t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", x.Scope, x.Message, testAccountID, testRegion, arn)
					}
				}
			})
		}
	}
}

// Denied tag reads in several phases send one warning for the whole scan;
// the rows are kept.
func TestScanForecast_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newFCCalls()
	for _, kind := range []string{"dataset", "monitor", "what-if-forecast"} {
		calls.errs[fcARN(kind, "x2")] = fcDenied
	}

	total, _, err := fcScan(context.Background(), st, fcHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != fcTestTotal {
		t.Errorf("total = %d; want %d", total, fcTestTotal)
	}
	if got := fcWarningOps(warns()); !slices.Equal(got, []string{forecastTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, forecastTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanForecast_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, forecastTagsOp)
	calls := newFCCalls()
	calls.errs[fcARN("forecast", "x1")] = fcThrottle
	calls.errs[fcARN("monitor", "x2")] = fcInvalid

	_, _, err := fcScan(context.Background(), st, fcHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidInputException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", forecastTagsOp, *warns)
	}
}

// A failed Describe. ResourceNotFoundException, which both Describes model,
// drops the row and its tags are not read. A denial keeps the listed summary
// as the row, tags read, and warns once under the Describe; a throttle keeps
// it and is transient; any other error keeps it and is an error.
func TestScanForecast_DescribeFailures(t *testing.T) {
	for _, x := range []struct{ kind, typ, op string }{
		{"dataset", TypeForecastDataset, "forecast:DescribeDataset"},
		{"dataset-group", TypeForecastDatasetGroup, "forecast:DescribeDatasetGroup"},
	} {
		for _, c := range []struct {
			name    string
			err     error
			outcome serviceOutcome // -1: no error
			code    string
			lost    bool
			warn    bool
		}{
			{"not found", fcNotFound, -1, "", true, false},
			{"denied", fcDenied, -1, "", false, true},
			{"transient", fcThrottle, outcomeTransient, "ThrottlingException", false, false},
			{"other error", fcInvalid, outcomeError, "InvalidInputException", false, false},
		} {
			t.Run(x.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newFCCalls()
				arn := fcARN(x.kind, "x1")
				calls.describeErrs[arn] = c.err

				total, _, err := fcScan(context.Background(), st, fcHandlers(calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				r, ok := rowsByNativeID(t, st, x.typ)[arn]
				if ok == c.lost {
					t.Fatalf("row stored = %v; want %v", ok, !c.lost)
				}
				wantTotal := fcTestTotal
				if c.lost {
					wantTotal--
				}
				if total != wantTotal {
					t.Errorf("total = %d; want %d", total, wantTotal)
				}
				if read := slices.Contains(calls.readARNs(), arn); read == c.lost {
					t.Errorf("tags read = %v; want %v", read, !c.lost)
				}
				if ok {
					// The summary is the row: it carries the listed name and
					// none of the Describe body's fields.
					keys := fcAttrKeys(t, r)
					if _, desc := keys["EncryptionConfig"]; desc {
						t.Errorf("attributes = %s; want the listed summary", r.AttributesJSON)
					}
					if _, desc := keys["DatasetArns"]; desc {
						t.Errorf("attributes = %s; want the listed summary", r.AttributesJSON)
					}
					assertTagsMap(t, r.TagsJSON, map[string]string{"type": x.typ})
				}
				var want []string
				if c.warn {
					want = []string{x.op}
				}
				w := warns()
				if got := fcWarningOps(w); !slices.Equal(got, want) {
					t.Fatalf("warnings = %v; want %v", w, want)
				}
				if c.warn && (w[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(w[0].Message, arn)) {
					t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", w[0].Scope, w[0].Message, testAccountID, testRegion, arn)
				}
			})
		}
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanForecast_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeForecastPredictor)
	calls := newFCCalls()
	calls.errs[fcARN("predictor", "x1")] = fcDenied

	total, _, err := fcScan(context.Background(), st, fcHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListPredictors"); n != 1 {
		t.Errorf("ListPredictors called %d times; want 1, none after the failed write", n)
	}
	if n := calls.count("ListForecasts"); n != 0 {
		t.Errorf("ListForecasts called %d times; want 0 after the failed write", n)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4 (the datasets and dataset groups)", total)
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the later phases.
func TestScanForecast_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, op := range []string{"ListDatasets", "ListExplainabilities"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newFCCalls()
			handlers := fcHandlers(calls)
			handlers[op] = failOnCall(2, fcInvalid, handlers[op])
			l := fcListingOf(t, op)

			total, _, err := fcScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidInputException")
			if total != fcTestTotal-1 {
				t.Errorf("total = %d; want %d", total, fcTestTotal-1)
			}
			if _, ok := rowsByNativeID(t, st, l.typ)[fcARN(l.kind, "x1")]; !ok {
				t.Errorf("%s row from the page before the failure missing", l.typ)
			}
			if got := len(rowsByNativeID(t, st, TypeForecastWhatIfForecast)); got != 2 {
				t.Errorf("what-if forecasts (the last phase) = %d; want 2", got)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanForecast_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newFCCalls()
	handlers := fcHandlers(calls)
	l := fcListingOf(t, "ListMonitors")
	handlers[l.op] = func(in any) (any, error) {
		id := pick(bacField[*string](in, "NextToken") == nil, "x1", "x2")
		return l.out(sdkaws.String("same"), sdkaws.String(fcARN(l.kind, id)), sdkaws.String("name-"+id)), nil
	}

	total, _, err := fcScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != fcTestTotal {
		t.Errorf("total = %d; want %d", total, fcTestTotal)
	}
}

// An element without an ARN is not stored, is not sent to the tag op or a
// Describe, and is warned about once under its listing, keyed by its name or
// by the fallback text when it has none.
func TestScanForecast_ElementWithoutARN(t *testing.T) {
	for _, c := range []struct {
		op, key string
		name    *string
	}{
		{"ListDatasets", "orphan", sdkaws.String("orphan")},
		{"ListWhatIfAnalyses", "(an element with no name)", nil},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newFCCalls()
			handlers := fcHandlers(calls)
			l := fcListingOf(t, c.op)
			handlers[c.op] = func(in any) (any, error) {
				calls.record(c.op, in)
				return l.out(nil, nil, c.name), nil
			}

			total, _, err := fcScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != fcTestTotal-2 {
				t.Errorf("total = %d; want %d", total, fcTestTotal-2)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "forecast:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under forecast:%s naming %s", w, c.op, c.key)
			}
			if n := calls.count("DescribeDataset"); c.op == "ListDatasets" && n != 0 {
				t.Errorf("DescribeDataset called %d times; want 0", n)
			}
			for _, arn := range calls.readARNs() {
				if arn == "" {
					t.Error("an empty ARN was sent to ListTagsForResource")
				}
			}
		})
	}
}

// A GovCloud or China account's listed ARNs reach the tag op unchanged.
func TestScanForecast_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newFCCalls()
			calls.partition, calls.region = pf.partition, pf.region
			var want []string
			for _, l := range fcListings {
				want = append(want, calls.arn(l.kind, "x1"), calls.arn(l.kind, "x2"))
			}
			slices.Sort(want)
			ds := partitionARN(pf.partition, "forecast", pf.region, "dataset/x1")
			calls.tags[ds] = map[string]string{"env": "prod"}
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(fcHandlers(calls), fcDenied)

			if _, _, err := scanForecast(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := calls.readARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeForecastDataset)[ds].TagsJSON, map[string]string{"env": "prod"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanForecast_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newFCCalls()
	handlers := fcHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListForecasts"]
	handlers["ListForecasts"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := fcScan(ctx, st, handlers)
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
	if n := calls.count("ListExplainabilities"); n != 0 {
		t.Errorf("ListExplainabilities called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanForecast_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "forecast.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := fcHandlers(newFCCalls())
		handlers["ListMonitors"] = func(any) (any, error) { return nil, nx }
		total, _, err := fcScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != fcTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, fcTestTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newFCCalls()
		calls.errs[fcARN("monitor", "x2")] = nx
		_, _, err := fcScan(context.Background(), st, fcHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanForecast(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// A listing whose first page is refused as closed to new customers (an
// AccessDeniedException with an empty message) stores nothing and warns
// nothing; every other listing is stored. A real IAM denial on a first page
// (a message naming the action) instead warns once under that listing's op,
// and every other listing is stored.
func TestScanForecast_FirstPageDenials(t *testing.T) {
	for _, l := range fcListings {
		for _, c := range []struct {
			name  string
			err   error
			warns []string
		}{
			{"closed", fcClosed, nil},
			{"denied", fcDenied, []string{"forecast:" + l.op}},
		} {
			t.Run(l.op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				handlers := fcHandlers(newFCCalls())
				handlers[l.op] = func(any) (any, error) { return nil, c.err }

				total, _, err := fcScan(context.Background(), st, handlers)
				if err != nil {
					t.Fatalf("scan: %v", err)
				}
				if total != fcTestTotal-2 {
					t.Errorf("total = %d; want %d", total, fcTestTotal-2)
				}
				for _, o := range fcListings {
					want := 2
					if o.op == l.op {
						want = 0
					}
					if got := len(rowsByNativeID(t, st, o.typ)); got != want {
						t.Errorf("%s rows = %d; want %d", o.typ, got, want)
					}
				}
				w := warns()
				if got := fcWarningOps(w); !slices.Equal(got, c.warns) {
					t.Fatalf("warnings = %v; want %v", w, c.warns)
				}
				if len(w) == 1 && w[0].Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope = %q; want %s/%s", w[0].Scope, testAccountID, testRegion)
				}
			})
		}
	}
}
