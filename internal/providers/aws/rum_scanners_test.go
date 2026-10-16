package aws

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rum"
	rumtypes "github.com/aws/aws-sdk-go-v2/service/rum/types"
	"github.com/icearp/disco-cli/store"
)

var rumDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: rum:List")

const rumEvidentlyARN = "arn:aws:evidently:us-east-1:123456789012:project/p1/experiment/e1"

func rumMonitorARN(name string) string { return rumAppMonitorARN(testRegion, testAccountID, name) }

// rumMonitors stores an app monitor row for each name and returns them as parents.
func rumMonitors(t *testing.T, st *store.Store, names ...string) []childParent {
	t.Helper()
	monitors := make([]childParent, len(names))
	for i, name := range names {
		monitors[i] = childParent{id: name, arn: rumMonitorARN(name)}
		upsertTestResource(t, st, "aws", testAccountID, TypeRUMAppMonitor, monitors[i].arn, testRegion, "{}")
	}
	return monitors
}

func rumCloudWatch() rumtypes.MetricDestinationSummary {
	return rumtypes.MetricDestinationSummary{Destination: rumtypes.MetricDestinationCloudWatch}
}

func rumEvidently() rumtypes.MetricDestinationSummary {
	return rumtypes.MetricDestinationSummary{
		Destination:    rumtypes.MetricDestinationEvidently,
		DestinationArn: sdkaws.String(rumEvidentlyARN),
		IamRoleArn:     sdkaws.String("arn:aws:iam::123456789012:role/rum-evidently"),
	}
}

func rumMetricDef(id string) rumtypes.MetricDefinition {
	return rumtypes.MetricDefinition{
		MetricDefinitionId: sdkaws.String(id), Name: sdkaws.String("metric-" + id), Namespace: sdkaws.String("AWS/RUM"),
	}
}

// rumPaged answers one call from pages keyed by parent: the first page carries
// NextToken "p2" when the parent has a second. errs fails a parent's call; a
// "/p2" key fails only its second page.
func rumPaged[T any](key string, token *string, pages map[string][][]T, errs map[string]error) ([]T, *string, error) {
	page := 0
	if sdkaws.ToString(token) == "p2" {
		page = 1
		if err := errs[key+"/p2"]; err != nil {
			return nil, nil, err
		}
	} else if err := errs[key]; err != nil {
		return nil, nil, err
	}
	var items []T
	if page < len(pages[key]) {
		items = pages[key][page]
	}
	if page == 0 && len(pages[key]) > 1 {
		return items, sdkaws.String("p2"), nil
	}
	return items, nil, nil
}

// rumDestsHandler answers ListRumMetricsDestinations keyed by app monitor
// name. A call whose MaxResults is not the model cap fails, so the page size
// is pinned.
func rumDestsHandler(calls *parentCalls, pages map[string][][]rumtypes.MetricDestinationSummary, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*rum.ListRumMetricsDestinationsInput)
		if sdkaws.ToInt32(req.MaxResults) != rumPageLimit {
			return nil, fmt.Errorf("MaxResults = %v; want %d", req.MaxResults, rumPageLimit)
		}
		name := sdkaws.ToString(req.AppMonitorName)
		calls.add(name)
		items, next, err := rumPaged(name, req.NextToken, pages, errs)
		if err != nil {
			return nil, err
		}
		return &rum.ListRumMetricsDestinationsOutput{Destinations: items, NextToken: next}, nil
	}
}

// rumDefsKey names a BatchGetRumMetricDefinitions call by its three inputs.
func rumDefsKey(monitor string, dest rumtypes.MetricDestination, destARN string) string {
	return monitor + "|" + string(dest) + "|" + destARN
}

// rumDefsHandler answers BatchGetRumMetricDefinitions keyed by rumDefsKey,
// with the MaxResults cap pinned as in rumDestsHandler.
func rumDefsHandler(calls *parentCalls, pages map[string][][]rumtypes.MetricDefinition, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*rum.BatchGetRumMetricDefinitionsInput)
		if sdkaws.ToInt32(req.MaxResults) != rumPageLimit {
			return nil, fmt.Errorf("MaxResults = %v; want %d", req.MaxResults, rumPageLimit)
		}
		key := rumDefsKey(sdkaws.ToString(req.AppMonitorName), req.Destination, sdkaws.ToString(req.DestinationArn))
		calls.add(key)
		items, next, err := rumPaged(key, req.NextToken, pages, errs)
		if err != nil {
			return nil, err
		}
		return &rum.BatchGetRumMetricDefinitionsOutput{MetricDefinitions: items, NextToken: next}, nil
	}
}

func newRUMTestClient(handlers map[string]func(in any) (any, error)) *rum.Client {
	return rum.NewFromConfig(opStubConfig(handlers, rumDenied))
}

func rumCWDestID(monitor string) string {
	return rumMonitorARN(monitor) + "/metrics-destination/CloudWatch"
}

func rumEvDestID(monitor string) string {
	return rumMonitorARN(monitor) + "/metrics-destination/Evidently/" + rumEvidentlyARN
}

func TestRUMMetricsDestinationNativeID_DistinctPerDestination(t *testing.T) {
	m := rumMonitorARN("m1")
	other := rumEvidently()
	other.DestinationArn = sdkaws.String("arn:aws:evidently:us-east-1:123456789012:project/p2/experiment/e1")
	ids := map[string]bool{}
	for _, d := range []rumtypes.MetricDestinationSummary{rumCloudWatch(), rumEvidently(), other} {
		ids[rumMetricsDestinationNativeID(m, d)] = true
	}
	if len(ids) != 3 {
		t.Errorf("NativeIDs = %v; want 3 distinct", ids)
	}
	if got := rumMetricsDestinationNativeID(m, rumCloudWatch()); got != rumCWDestID("m1") {
		t.Errorf("CloudWatch NativeID = %q; want %q", got, rumCWDestID("m1"))
	}
	if got := rumMetricsDestinationNativeID(m, rumEvidently()); got != rumEvDestID("m1") {
		t.Errorf("Evidently NativeID = %q; want %q", got, rumEvDestID("m1"))
	}
}

func TestScanRUMAppMonitors_PagesAndLimit(t *testing.T) {
	st := newTestStore(t)
	var n atomic.Int64
	client := newRUMTestClient(map[string]func(any) (any, error){
		"ListAppMonitors": func(in any) (any, error) {
			req := in.(*rum.ListAppMonitorsInput)
			if sdkaws.ToInt32(req.MaxResults) != rumPageLimit {
				return nil, fmt.Errorf("MaxResults = %v; want %d", req.MaxResults, rumPageLimit)
			}
			if n.Add(1) == 1 {
				return &rum.ListAppMonitorsOutput{
					AppMonitorSummaries: []rumtypes.AppMonitorSummary{{Name: sdkaws.String("m1"), State: rumtypes.StateEnumActive}, {}},
					NextToken:           sdkaws.String("p2"),
				}, nil
			}
			return &rum.ListAppMonitorsOutput{AppMonitorSummaries: []rumtypes.AppMonitorSummary{{Name: sdkaws.String("m2")}}}, nil
		},
	})
	total, _, monitors, err := scanRUMAppMonitors(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("total, err = %d, %v; want 2, nil", total, err)
	}
	want := []childParent{{id: "m1", arn: rumMonitorARN("m1")}, {id: "m2", arn: rumMonitorARN("m2")}}
	if !reflect.DeepEqual(monitors, want) {
		t.Errorf("monitors = %v; want %v", monitors, want)
	}
	rows := rowsByNativeID(t, st, TypeRUMAppMonitor)
	if r, ok := rows[rumMonitorARN("m1")]; !ok || sv(r.Status) != "ACTIVE" || len(rows) != 2 {
		t.Errorf("rows = %v; want m1 (ACTIVE) and m2", rows)
	}
}

// A denial or a hard error on a later page keeps the app monitors stored from
// the earlier pages and returns them as parents.
func TestScanRUMAppMonitors_LaterPageErrorKeepsEarlierPages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		wantErr bool
	}{
		{name: "denied", err: rumDenied},
		{name: "other code", err: apiErr("ValidationException", "bad"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, rumListAppMonitorsOp)
			client := newRUMTestClient(map[string]func(any) (any, error){
				"ListAppMonitors": func(in any) (any, error) {
					if in.(*rum.ListAppMonitorsInput).NextToken != nil {
						return nil, tc.err
					}
					return &rum.ListAppMonitorsOutput{
						AppMonitorSummaries: []rumtypes.AppMonitorSummary{{Name: sdkaws.String("m1")}},
						NextToken:           sdkaws.String("p2"),
					}, nil
				},
			})
			total, _, monitors, err := scanRUMAppMonitors(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantErr != isAPIErrorCode(err, "ValidationException") {
				t.Errorf("err = %v; want ValidationException: %v", err, tc.wantErr)
			}
			if !tc.wantErr && (err != nil || *warns != 1) {
				t.Errorf("err, warnings = %v, %d; want nil, 1", err, *warns)
			}
			if total != 1 || len(monitors) != 1 || len(rowsByNativeID(t, st, TypeRUMAppMonitor)) != 1 {
				t.Errorf("total, monitors = %d, %v; want 1 stored and returned", total, monitors)
			}
		})
	}
}

func TestScanRUMMetricsDestinations_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newRUMTestClient(map[string]func(any) (any, error){
		"ListRumMetricsDestinations": rumDestsHandler(&calls, map[string][][]rumtypes.MetricDestinationSummary{
			"m1": {{rumCloudWatch()}, {rumEvidently(), {}}},
			"m2": {{rumCloudWatch()}},
		}, nil),
	})

	total, _, dests, err := scanRUMMetricsDestinations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rumMonitors(t, st, "m1", "m2", "m3"))
	if err != nil {
		t.Fatalf("scanRUMMetricsDestinations: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"m1", "m2", "m3"}) {
		t.Errorf("app monitors queried = %v; want [m1 m2 m3]", got)
	}
	rows := rowsByNativeID(t, st, TypeRUMMetricsDestination)
	if len(rows) != 3 {
		t.Fatalf("rows = %d; want 3 (the destination-less element skipped): %v", len(rows), rows)
	}
	for _, want := range []struct{ monitor, id, name string }{
		{"m1", rumCWDestID("m1"), "CloudWatch"},
		{"m1", rumEvDestID("m1"), "Evidently"},
		{"m2", rumCWDestID("m2"), "CloudWatch"},
	} {
		r, ok := rows[want.id]
		if !ok {
			t.Errorf("missing destination %s", want.id)
			continue
		}
		if r.Type != TypeRUMMetricsDestination || sv(r.Region) != testRegion || sv(r.Name) != want.name {
			t.Errorf("%s: Type %q Region %q Name %q", want.id, r.Type, sv(r.Region), sv(r.Name))
		}
		assertContainsNative(t, st, rumMonitorARN(want.monitor), want.id)
	}
	if !strings.Contains(rows[rumEvDestID("m1")].AttributesJSON, `"IamRoleArn":"arn:aws:iam::123456789012:role/rum-evidently"`) {
		t.Errorf("attrs = %s; want IamRoleArn", rows[rumEvDestID("m1")].AttributesJSON)
	}
	got := make([]string, len(dests))
	for i, d := range dests {
		got[i] = d.monitorName + " " + d.nativeID
	}
	sort.Strings(got)
	want := []string{"m1 " + rumCWDestID("m1"), "m1 " + rumEvDestID("m1"), "m2 " + rumCWDestID("m2")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("returned destinations = %v; want %v", got, want)
	}
}

func TestScanRUMMetricsDestinations_Empty(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newRUMTestClient(map[string]func(any) (any, error){
		"ListRumMetricsDestinations": rumDestsHandler(&calls, nil, nil),
	})
	total, _, dests, err := scanRUMMetricsDestinations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rumMonitors(t, st, "m1"))
	if err != nil || total != 0 || len(dests) != 0 {
		t.Errorf("total, dests, err = %d, %v, %v; want 0, [], nil", total, dests, err)
	}
	if n := len(rowsByNativeID(t, st, TypeRUMMetricsDestination)); n != 0 {
		t.Errorf("rows = %d; want 0", n)
	}
}

// Each per-app-monitor error is skipped or returned by its code. Two of three
// app monitors fail, so a skip that warns must warn once for both; a skipped
// app monitor's destinations are not returned, its sibling's are.
func TestScanRUMMetricsDestinations_PerParentErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   string
		wantWarns int
	}{
		{name: "denied warns once", err: rumDenied, wantWarns: 1},
		{name: "throttled warns once", err: apiErr("ThrottlingException", "Rate exceeded"), wantWarns: 1},
		{name: "validation warns once", err: apiErr("ValidationException", "invalid"), wantWarns: 1},
		{name: "app monitor deleted since listing skipped silently", err: apiErr("ResourceNotFoundException", "gone")},
		{name: "other code fails the phase", err: apiErr("InvalidRequestException", "bad"), wantErr: "InvalidRequestException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, rumListMetricsDestinationsOp)
			var calls parentCalls
			client := newRUMTestClient(map[string]func(any) (any, error){
				"ListRumMetricsDestinations": rumDestsHandler(&calls, map[string][][]rumtypes.MetricDestinationSummary{
					"m1": {{rumEvidently()}},
					"m3": {{rumCloudWatch()}},
				}, map[string]error{"m1": tc.err, "m2": tc.err}),
			})
			total, _, dests, err := scanRUMMetricsDestinations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rumMonitors(t, st, "m1", "m2", "m3"))
			if tc.wantErr != "" {
				if !isAPIErrorCode(err, tc.wantErr) || classifyServiceError(err) != outcomeError || dests != nil {
					t.Errorf("err, dests = %v, %v; want a hard %s and no destinations", err, dests, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			if _, ok := rowsByNativeID(t, st, TypeRUMMetricsDestination)[rumCWDestID("m3")]; !ok || total != 1 {
				t.Errorf("total = %d, sibling row stored = %v; want 1, true", total, ok)
			}
			if len(dests) != 1 || dests[0].nativeID != rumCWDestID("m3") {
				t.Errorf("returned destinations = %v; want only m3's", dests)
			}
		})
	}
}

// The listing stops when the service repeats the token it was sent, rather
// than looping on it.
func TestScanRUMMetricsDestinations_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var n atomic.Int64
	client := newRUMTestClient(map[string]func(any) (any, error){
		"ListRumMetricsDestinations": func(any) (any, error) {
			if n.Add(1) > 5 {
				return nil, apiErr("InvalidRequestException", "looped")
			}
			return &rum.ListRumMetricsDestinationsOutput{NextToken: sdkaws.String("same")}, nil
		},
	})
	_, _, _, err := scanRUMMetricsDestinations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rumMonitors(t, st, "m1"))
	if err != nil || n.Load() != 2 {
		t.Errorf("err, calls = %v, %d; want nil, 2", err, n.Load())
	}
}

// rumDests stores a destination row under each app monitor's row and returns
// the destinations as the metric definition phase's parents.
func rumDests(t *testing.T, st *store.Store, pairs ...rumDestPair) []rumDestination {
	t.Helper()
	var out []rumDestination
	for _, p := range pairs {
		rumMonitors(t, st, p.monitor)
		id := rumMetricsDestinationNativeID(rumMonitorARN(p.monitor), p.dest)
		upsertTestResource(t, st, "aws", testAccountID, TypeRUMMetricsDestination, id, testRegion, "{}")
		out = append(out, rumDestination{monitorName: p.monitor, nativeID: id, summary: p.dest})
	}
	return out
}

func TestScanRUMMetricDefinitions_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var n atomic.Int64
	client := newRUMTestClient(map[string]func(any) (any, error){
		"BatchGetRumMetricDefinitions": func(any) (any, error) {
			if n.Add(1) > 5 {
				return nil, apiErr("InvalidRequestException", "looped")
			}
			return &rum.BatchGetRumMetricDefinitionsOutput{NextToken: sdkaws.String("same")}, nil
		},
	})
	_, _, err := scanRUMMetricDefinitions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rumDests(t, st, rumDestPair{"m1", rumCloudWatch()}))
	if err != nil || n.Load() != 2 {
		t.Errorf("err, calls = %v, %d; want nil, 2", err, n.Load())
	}
}

// A denied first page warns, stores nothing, and leaves the child phases no
// parents.
func TestScanRUM_AppMonitorsDenied(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, rumListAppMonitorsOp)
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(nil, rumDenied)}
	total, _, err := scanRUM(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || total != 0 || *warns != 1 {
		t.Errorf("total, warnings, err = %d, %d, %v; want 0, 1, nil", total, *warns, err)
	}
}

type rumDestPair struct {
	monitor string
	dest    rumtypes.MetricDestinationSummary
}

func TestScanRUMMetricDefinitions_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	cwKey := rumDefsKey("m1", rumtypes.MetricDestinationCloudWatch, "")
	evKey := rumDefsKey("m1", rumtypes.MetricDestinationEvidently, rumEvidentlyARN)
	client := newRUMTestClient(map[string]func(any) (any, error){
		"BatchGetRumMetricDefinitions": rumDefsHandler(&calls, map[string][][]rumtypes.MetricDefinition{
			cwKey: {{rumMetricDef("d1"), {}}},
			evKey: {{rumMetricDef("d2")}, {rumMetricDef("d3")}},
		}, nil),
	})
	dests := rumDests(t, st, rumDestPair{"m1", rumCloudWatch()}, rumDestPair{"m1", rumEvidently()}, rumDestPair{"m2", rumCloudWatch()})

	total, _, err := scanRUMMetricDefinitions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, dests)
	if err != nil {
		t.Fatalf("scanRUMMetricDefinitions: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	// The CloudWatch calls carry no DestinationArn: the API rejects one there.
	wantCalls := []string{cwKey, evKey, rumDefsKey("m2", rumtypes.MetricDestinationCloudWatch, "")}
	sort.Strings(wantCalls)
	if got := calls.distinct(); !reflect.DeepEqual(got, wantCalls) {
		t.Errorf("destinations queried = %v; want %v", got, wantCalls)
	}
	rows := rowsByNativeID(t, st, TypeRUMMetricDefinition)
	if len(rows) != 3 {
		t.Fatalf("rows = %d; want 3 (the id-less element skipped): %v", len(rows), rows)
	}
	for _, want := range []struct{ dest, id string }{
		{rumCWDestID("m1"), rumCWDestID("m1") + "/metric-definition/d1"},
		{rumEvDestID("m1"), rumEvDestID("m1") + "/metric-definition/d2"},
		{rumEvDestID("m1"), rumEvDestID("m1") + "/metric-definition/d3"},
	} {
		r, ok := rows[want.id]
		if !ok {
			t.Errorf("missing metric definition %s", want.id)
			continue
		}
		if r.Type != TypeRUMMetricDefinition || sv(r.Region) != testRegion {
			t.Errorf("%s: Type %q Region %q", want.id, r.Type, sv(r.Region))
		}
		assertContainsNative(t, st, want.dest, want.id)
	}
	r := rows[rumCWDestID("m1")+"/metric-definition/d1"]
	if sv(r.Name) != "metric-d1" || !strings.Contains(r.AttributesJSON, `"Namespace":"AWS/RUM"`) {
		t.Errorf("Name, attrs = %q, %s; want metric-d1 with Namespace AWS/RUM", sv(r.Name), r.AttributesJSON)
	}
}

// A CloudWatch destination is queried without a DestinationArn even if its
// summary carries one, since the API documents it must not be sent there.
func TestScanRUMMetricDefinitions_CloudWatchOmitsDestinationArn(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	cw := rumCloudWatch()
	cw.DestinationArn = sdkaws.String("arn:aws:cloudwatch:us-east-1:123456789012:unexpected")
	client := newRUMTestClient(map[string]func(any) (any, error){
		"BatchGetRumMetricDefinitions": rumDefsHandler(&calls, nil, nil),
	})
	_, _, err := scanRUMMetricDefinitions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rumDests(t, st, rumDestPair{"m1", cw}))
	want := []string{rumDefsKey("m1", rumtypes.MetricDestinationCloudWatch, "")}
	if got := calls.distinct(); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("calls, err = %v, %v; want %v, nil", got, err, want)
	}
}

func TestScanRUMMetricDefinitions_Empty(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newRUMTestClient(map[string]func(any) (any, error){
		"BatchGetRumMetricDefinitions": rumDefsHandler(&calls, nil, nil),
	})
	total, _, err := scanRUMMetricDefinitions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, rumDests(t, st, rumDestPair{"m1", rumCloudWatch()}))
	if err != nil || total != 0 || len(calls.distinct()) != 1 {
		t.Errorf("total, calls, err = %d, %v, %v; want 0, one call, nil", total, calls.distinct(), err)
	}
	if n := len(rowsByNativeID(t, st, TypeRUMMetricDefinition)); n != 0 {
		t.Errorf("rows = %d; want 0", n)
	}
}

// Each per-destination error is skipped or returned by its code, as for the
// destination phase.
func TestScanRUMMetricDefinitions_PerParentErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   string
		wantWarns int
	}{
		{name: "denied warns once", err: rumDenied, wantWarns: 1},
		{name: "throttled warns once", err: apiErr("ThrottlingException", "Rate exceeded"), wantWarns: 1},
		{name: "validation warns once", err: apiErr("ValidationException", "invalid"), wantWarns: 1},
		{name: "destination deleted since listing skipped silently", err: apiErr("ResourceNotFoundException", "gone")},
		{name: "other code fails the phase", err: apiErr("InvalidRequestException", "bad"), wantErr: "InvalidRequestException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, rumBatchGetMetricDefinitionsOp)
			var calls parentCalls
			m3 := rumDefsKey("m3", rumtypes.MetricDestinationCloudWatch, "")
			client := newRUMTestClient(map[string]func(any) (any, error){
				"BatchGetRumMetricDefinitions": rumDefsHandler(&calls, map[string][][]rumtypes.MetricDefinition{
					m3: {{rumMetricDef("d3")}},
				}, map[string]error{
					rumDefsKey("m1", rumtypes.MetricDestinationCloudWatch, ""): tc.err,
					rumDefsKey("m2", rumtypes.MetricDestinationCloudWatch, ""): tc.err,
				}),
			})
			dests := rumDests(t, st, rumDestPair{"m1", rumCloudWatch()}, rumDestPair{"m2", rumCloudWatch()}, rumDestPair{"m3", rumCloudWatch()})
			total, _, err := scanRUMMetricDefinitions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, dests)
			if tc.wantErr != "" {
				if !isAPIErrorCode(err, tc.wantErr) || classifyServiceError(err) != outcomeError {
					t.Errorf("err = %v; want a hard %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			if _, ok := rowsByNativeID(t, st, TypeRUMMetricDefinition)[rumCWDestID("m3")+"/metric-definition/d3"]; !ok || total != 1 {
				t.Errorf("total = %d, sibling row stored = %v; want 1, true", total, ok)
			}
		})
	}
}

// rumTopLevelHandlers answers the ops scanRUM calls: app monitors m1 and m2,
// each with a CloudWatch destination, m1 also with an Evidently one, and each
// destination with one metric definition. A child call whose parent row is not
// already stored fails, so phase order is pinned.
func rumTopLevelHandlers(st *store.Store, destCalls, defCalls *parentCalls) map[string]func(any) (any, error) {
	stored := func(nativeID string) error {
		_, err := st.GetResource(store.ResourceID("aws", testAccountID, nativeID))
		return err
	}
	return map[string]func(any) (any, error){
		"ListAppMonitors": func(any) (any, error) {
			return &rum.ListAppMonitorsOutput{AppMonitorSummaries: []rumtypes.AppMonitorSummary{
				{Name: sdkaws.String("m1")}, {Name: sdkaws.String("m2")},
			}}, nil
		},
		"ListRumMetricsDestinations": func(in any) (any, error) {
			name := sdkaws.ToString(in.(*rum.ListRumMetricsDestinationsInput).AppMonitorName)
			if err := stored(rumMonitorARN(name)); err != nil {
				return nil, fmt.Errorf("app monitor %s not stored before its destinations: %w", name, err)
			}
			destCalls.add(name)
			dests := []rumtypes.MetricDestinationSummary{rumCloudWatch()}
			if name == "m1" {
				dests = append(dests, rumEvidently())
			}
			return &rum.ListRumMetricsDestinationsOutput{Destinations: dests}, nil
		},
		"BatchGetRumMetricDefinitions": func(in any) (any, error) {
			req := in.(*rum.BatchGetRumMetricDefinitionsInput)
			name := sdkaws.ToString(req.AppMonitorName)
			destID := rumMetricsDestinationNativeID(rumMonitorARN(name), rumtypes.MetricDestinationSummary{
				Destination: req.Destination, DestinationArn: req.DestinationArn,
			})
			// Recorded before the stored check, so a call for an unstored
			// destination still counts.
			defCalls.add(destID)
			if err := stored(destID); err != nil {
				return nil, fmt.Errorf("destination %s not stored before its definitions: %w", destID, err)
			}
			return &rum.BatchGetRumMetricDefinitionsOutput{MetricDefinitions: []rumtypes.MetricDefinition{rumMetricDef("d")}}, nil
		},
	}
}

func TestScanRUM_ChildrenOfStoredParents(t *testing.T) {
	st := newTestStore(t)
	var destCalls, defCalls parentCalls
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(rumTopLevelHandlers(st, &destCalls, &defCalls), rumDenied)}

	total, _, err := scanRUM(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanRUM: %v", err)
	}
	if total != 8 {
		t.Errorf("total = %d; want 8 (2 app monitors + 3 destinations + 3 definitions)", total)
	}
	if got := destCalls.distinct(); !reflect.DeepEqual(got, []string{"m1", "m2"}) {
		t.Errorf("app monitors queried = %v; want [m1 m2]", got)
	}
	wantDests := []string{rumCWDestID("m1"), rumEvDestID("m1"), rumCWDestID("m2")}
	sort.Strings(wantDests)
	if got := defCalls.distinct(); !reflect.DeepEqual(got, wantDests) {
		t.Errorf("destinations queried = %v; want %v", got, wantDests)
	}
	for _, d := range wantDests {
		assertContainsNative(t, st, d, d+"/metric-definition/d")
	}
	assertContainsNative(t, st, rumMonitorARN("m1"), rumEvDestID("m1"))
}

// A hard destination failure fails the service but keeps the app monitors,
// and no metric definitions are listed for destinations that were not stored.
func TestScanRUM_DestinationFailureKeepsAppMonitors(t *testing.T) {
	st := newTestStore(t)
	var destCalls, defCalls parentCalls
	h := rumTopLevelHandlers(st, &destCalls, &defCalls)
	list := h["ListRumMetricsDestinations"]
	h["ListRumMetricsDestinations"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*rum.ListRumMetricsDestinationsInput).AppMonitorName) == "m2" {
			return nil, apiErr("InvalidRequestException", "bad")
		}
		return list(in)
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, rumDenied)}

	total, _, err := scanRUM(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InvalidRequestException") || classifyServiceError(err) != outcomeError {
		t.Errorf("err = %v; want a hard InvalidRequestException", err)
	}
	if total != 2 || len(rowsByNativeID(t, st, TypeRUMAppMonitor)) != 2 {
		t.Errorf("total = %d; want the 2 app monitors stored and counted", total)
	}
	if got := defCalls.distinct(); len(got) != 0 {
		t.Errorf("destinations queried = %v; want none", got)
	}
}

// A hard metric definition failure fails the service but keeps the app
// monitors and destinations.
func TestScanRUM_DefinitionFailureKeepsParents(t *testing.T) {
	st := newTestStore(t)
	var destCalls, defCalls parentCalls
	h := rumTopLevelHandlers(st, &destCalls, &defCalls)
	h["BatchGetRumMetricDefinitions"] = func(any) (any, error) { return nil, apiErr("InvalidRequestException", "bad") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, rumDenied)}

	total, _, err := scanRUM(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InvalidRequestException") || classifyServiceError(err) != outcomeError {
		t.Errorf("err = %v; want a hard InvalidRequestException", err)
	}
	if total != 5 || len(rowsByNativeID(t, st, TypeRUMMetricsDestination)) != 3 {
		t.Errorf("total = %d; want 5 (2 app monitors + 3 destinations stored)", total)
	}
}

// A throttled app monitor page is demoted to one warning, so a hard
// destination error still classifies the service as failed, and the app
// monitors of the earlier page are kept.
func TestScanRUM_AppMonitorThrottleDemotedBesideHardChildError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, rumListAppMonitorsOp)
	var destCalls, defCalls parentCalls
	h := rumTopLevelHandlers(st, &destCalls, &defCalls)
	h["ListAppMonitors"] = func(in any) (any, error) {
		if in.(*rum.ListAppMonitorsInput).NextToken != nil {
			return nil, apiErr("ThrottlingException", "Rate exceeded")
		}
		return &rum.ListAppMonitorsOutput{
			AppMonitorSummaries: []rumtypes.AppMonitorSummary{{Name: sdkaws.String("m1")}},
			NextToken:           sdkaws.String("p2"),
		}, nil
	}
	h["ListRumMetricsDestinations"] = func(any) (any, error) { return nil, apiErr("InvalidRequestException", "bad") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, rumDenied)}

	_, _, err := scanRUM(context.Background(), acct, testRegion, st, testScanID)
	if classifyServiceError(err) != outcomeError || *warns != 1 {
		t.Errorf("outcome, warnings = %v, %d; want outcomeError, 1 (err %v)", classifyServiceError(err), *warns, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeRUMAppMonitor)[rumMonitorARN("m1")]; !ok {
		t.Error("app monitor from the first page not kept")
	}
}
