package aws

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesisanalyticsv2"
	kav2types "github.com/aws/aws-sdk-go-v2/service/kinesisanalyticsv2/types"
	"github.com/icearp/disco-cli/store"
)

// TestScanKinesisAnalyticsV2_OneVersionPerScan: an unchanged application must
// produce one version row however many times it is scanned. The scanner used
// to store the ListApplications summary and then the DescribeApplication
// detail under the same ARN in one scan; with no ON CONFLICT update in the
// store, each differing upsert is a version split, so every scan added two
// rows and reported the application as changed.
func TestScanKinesisAnalyticsV2_OneVersionPerScan(t *testing.T) {
	st := newTestStore(t)
	arn := "arn:aws:kinesisanalytics:us-east-1:123456789012:application/app1"
	name := "app1"
	summary := func() stubCall {
		return stubCall{Output: &kinesisanalyticsv2.ListApplicationsOutput{ApplicationSummaries: []kav2types.ApplicationSummary{{
			ApplicationARN: &arn, ApplicationName: &name, ApplicationStatus: kav2types.ApplicationStatusRunning,
			ApplicationVersionId: sdkaws.Int64(3), RuntimeEnvironment: kav2types.RuntimeEnvironmentFlink118,
		}}}}
	}
	role := "arn:aws:iam::123456789012:role/kda"
	detail := func() stubCall {
		return stubCall{Output: &kinesisanalyticsv2.DescribeApplicationOutput{ApplicationDetail: &kav2types.ApplicationDetail{
			ApplicationARN: &arn, ApplicationName: &name, ApplicationStatus: kav2types.ApplicationStatusRunning,
			ApplicationVersionId: sdkaws.Int64(3), RuntimeEnvironment: kav2types.RuntimeEnvironmentFlink118,
			ServiceExecutionRole: &role,
		}}}
	}
	stub := stubResponses(t, map[string][]stubCall{
		"ListApplications":         {summary(), summary()},
		"DescribeApplication":      {detail(), detail()},
		"ListApplicationSnapshots": {kav2NoSnapshots(), kav2NoSnapshots()},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stub, testRegion)}

	second, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var newC, changedC atomic.Int64
	for _, scanID := range []string{testScanID, second} {
		if _, _, err := scanKinesisAnalyticsV2(context.Background(), acct, testRegion, st.WithUpsertCounters(&newC, &changedC), scanID); err != nil {
			t.Fatalf("scan %s: %v", scanID, err)
		}
	}

	versions, err := st.GetResourceVersions(store.ResourceID("aws", testAccountID, arn))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Errorf("versions = %d, want 1 (unchanged application scanned twice)", len(versions))
	}
	if newC.Load() != 1 || changedC.Load() != 0 {
		t.Errorf("new=%d changed=%d, want 1/0", newC.Load(), changedC.Load())
	}
	// The stored row is the detail body: resolvers read ServiceExecutionRole.
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.AttributesJSON, `"ServiceExecutionRole"`) {
		t.Errorf("stored attributes lack the detail body: %s", r.AttributesJSON)
	}
}

// TestScanKinesisAnalyticsV2_DeniedDetailKeepsSummary: without
// DescribeApplication the application is still stored, once, from its
// summary — the detail read is an enrichment, not a precondition.
func TestScanKinesisAnalyticsV2_DeniedDetailKeepsSummary(t *testing.T) {
	st := newTestStore(t)
	arn := "arn:aws:kinesisanalytics:us-east-1:123456789012:application/app1"
	name := "app1"
	stub := stubResponses(t, map[string][]stubCall{
		"ListApplications": {{Output: &kinesisanalyticsv2.ListApplicationsOutput{ApplicationSummaries: []kav2types.ApplicationSummary{{
			ApplicationARN: &arn, ApplicationName: &name, ApplicationStatus: kav2types.ApplicationStatusReady,
		}}}}},
		"DescribeApplication":      {{Err: apiErr("AccessDeniedException", "not authorized to perform: kinesisanalytics:DescribeApplication")}},
		"ListApplicationSnapshots": {kav2NoSnapshots()},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stub, testRegion)}

	total, inserted, err := scanKinesisAnalyticsV2(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	versions, err := st.GetResourceVersions(store.ResourceID("aws", testAccountID, arn))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || strings.Contains(versions[0].AttributesJSON, `"ServiceExecutionRole"`) {
		t.Errorf("versions = %+v, want one summary row", versions)
	}
}

// TestScanKinesisAnalyticsV2_FailedDetailStillStoresEveryApplication: a
// detail-read failure on one application must not cost the ones after it
// their rows; each falls back to its summary and the failure is returned.
func TestScanKinesisAnalyticsV2_FailedDetailStillStoresEveryApplication(t *testing.T) {
	st := newTestStore(t)
	arns := []string{
		"arn:aws:kinesisanalytics:us-east-1:123456789012:application/app1",
		"arn:aws:kinesisanalytics:us-east-1:123456789012:application/app2",
	}
	names := []string{"app1", "app2"}
	var sums []kav2types.ApplicationSummary
	for i := range arns {
		sums = append(sums, kav2types.ApplicationSummary{ApplicationARN: &arns[i], ApplicationName: &names[i]})
	}
	stub := stubResponses(t, map[string][]stubCall{
		"ListApplications": {{Output: &kinesisanalyticsv2.ListApplicationsOutput{ApplicationSummaries: sums}}},
		"DescribeApplication": {
			{Err: apiErr("InvalidRequestException", "bad")},
			{Output: &kinesisanalyticsv2.DescribeApplicationOutput{ApplicationDetail: &kav2types.ApplicationDetail{ApplicationARN: &arns[1], ApplicationName: &names[1]}}},
		},
		"ListApplicationSnapshots": {kav2NoSnapshots(), kav2NoSnapshots()},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stub, testRegion)}

	total, _, err := scanKinesisAnalyticsV2(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InvalidRequestException") {
		t.Errorf("err = %v, want the DescribeApplication InvalidRequestException", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	for _, arn := range arns {
		if v, verr := st.GetResourceVersions(store.ResourceID("aws", testAccountID, arn)); verr != nil || len(v) != 1 {
			t.Errorf("%s: versions = %d (%v), want 1", arn, len(v), verr)
		}
	}
}

func kav2NoSnapshots() stubCall {
	return stubCall{Output: &kinesisanalyticsv2.ListApplicationSnapshotsOutput{}}
}

var kav2Denied = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: kinesisanalytics:List")

func kav2AppARN(name string) string {
	return fmt.Sprintf("arn:aws:kinesisanalytics:%s:%s:application/%s", testRegion, testAccountID, name)
}

// kav2Apps stores an application row for each name and returns them as parents.
func kav2Apps(t *testing.T, st *store.Store, names ...string) []childParent {
	t.Helper()
	apps := make([]childParent, len(names))
	for i, name := range names {
		apps[i] = childParent{id: name, arn: kav2AppARN(name)}
		upsertTestResource(t, st, "aws", testAccountID, TypeKAV2Application, apps[i].arn, testRegion, "{}")
	}
	return apps
}

func kav2Snapshot(name string) kav2types.SnapshotDetails {
	return kav2types.SnapshotDetails{
		SnapshotName: sdkaws.String(name), SnapshotStatus: kav2types.SnapshotStatusReady, ApplicationVersionId: sdkaws.Int64(7),
	}
}

// kav2SnapshotsHandler answers ListApplicationSnapshots from pages keyed by
// application name: the first page of an application carries NextToken "p2"
// when it has a second. errs fails an application's call with the given
// error; a "/p2" key fails only its second page. A call whose Limit is not the
// model cap fails, so the page size is pinned.
func kav2SnapshotsHandler(calls *parentCalls, pages map[string][][]kav2types.SnapshotDetails, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*kinesisanalyticsv2.ListApplicationSnapshotsInput)
		if sdkaws.ToInt32(req.Limit) != kav2SnapshotPageLimit {
			return nil, fmt.Errorf("Limit = %v; want %d", req.Limit, kav2SnapshotPageLimit)
		}
		name := sdkaws.ToString(req.ApplicationName)
		calls.add(name)
		page := 0
		if sdkaws.ToString(req.NextToken) == "p2" {
			page = 1
			if err := errs[name+"/p2"]; err != nil {
				return nil, err
			}
		} else if err := errs[name]; err != nil {
			return nil, err
		}
		out := &kinesisanalyticsv2.ListApplicationSnapshotsOutput{}
		if page < len(pages[name]) {
			out.SnapshotSummaries = pages[name][page]
		}
		if page == 0 && len(pages[name]) > 1 {
			out.NextToken = sdkaws.String("p2")
		}
		return out, nil
	}
}

func newKAV2TestClient(handlers map[string]func(in any) (any, error)) *kinesisanalyticsv2.Client {
	return kinesisanalyticsv2.NewFromConfig(opStubConfig(handlers, kav2Denied))
}

func TestScanKAV2ApplicationSnapshots_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	first := kav2Snapshot("snap-1")
	first.SnapshotCreationTimestamp = &created
	var calls parentCalls
	client := newKAV2TestClient(map[string]func(any) (any, error){
		"ListApplicationSnapshots": kav2SnapshotsHandler(&calls, map[string][][]kav2types.SnapshotDetails{
			"app1": {{first, {}}, {kav2Snapshot("snap-2")}},
			"app2": {{kav2Snapshot("snap-3")}},
		}, nil),
	})

	total, _, err := scanKAV2ApplicationSnapshots(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, kav2Apps(t, st, "app1", "app2", "app3"))
	if err != nil {
		t.Fatalf("scanKAV2ApplicationSnapshots: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"app1", "app2", "app3"}) {
		t.Errorf("applications queried = %v; want [app1 app2 app3]", got)
	}
	rows := rowsByNativeID(t, st, TypeKAV2ApplicationSnapshot)
	if len(rows) != 3 {
		t.Fatalf("rows = %d; want 3 (the nameless element skipped): %v", len(rows), rows)
	}
	firstID := kav2AppARN("app1") + "/snapshot/snap-1"
	for _, want := range []struct{ app, id string }{
		{kav2AppARN("app1"), firstID},
		{kav2AppARN("app1"), kav2AppARN("app1") + "/snapshot/snap-2"},
		{kav2AppARN("app2"), kav2AppARN("app2") + "/snapshot/snap-3"},
	} {
		r, ok := rows[want.id]
		if !ok {
			t.Errorf("missing snapshot %s", want.id)
			continue
		}
		if r.Type != TypeKAV2ApplicationSnapshot || sv(r.Region) != testRegion {
			t.Errorf("%s: Type %q Region %q", want.id, r.Type, sv(r.Region))
		}
		assertContainsNative(t, st, want.app, want.id)
	}
	r := rows[firstID]
	if sv(r.Name) != "snap-1" || sv(r.Status) != "READY" {
		t.Errorf("Name, Status = %q, %q; want snap-1, READY", sv(r.Name), sv(r.Status))
	}
	if sv(r.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("CreatedAt = %q; want 2026-01-02T03:04:05Z", sv(r.CreatedAt))
	}
	if !strings.Contains(r.AttributesJSON, `"ApplicationVersionId":7`) {
		t.Errorf("attrs = %s; want ApplicationVersionId 7", r.AttributesJSON)
	}
}

func TestScanKAV2ApplicationSnapshots_Empty(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newKAV2TestClient(map[string]func(any) (any, error){
		"ListApplicationSnapshots": kav2SnapshotsHandler(&calls, nil, nil),
	})
	total, _, err := scanKAV2ApplicationSnapshots(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, kav2Apps(t, st, "app1"))
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeKAV2ApplicationSnapshot)); n != 0 {
		t.Errorf("rows = %d; want 0", n)
	}
}

// Each per-application error is skipped or returned by its code. Two of three
// applications fail, so a skip that warns must warn once for both.
func TestScanKAV2ApplicationSnapshots_PerApplicationErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   string
		wantWarns int
	}{
		{name: "denied warns once", err: kav2Denied, wantWarns: 1},
		{name: "throttled warns once", err: apiErr("ThrottlingException", "Rate exceeded"), wantWarns: 1},
		{name: "unsupported operation warns once", err: apiErr("UnsupportedOperationException", "not a Flink application"), wantWarns: 1},
		{name: "invalid argument warns once", err: apiErr("InvalidArgumentException", "invalid"), wantWarns: 1},
		{name: "application deleted since listing skipped silently", err: apiErr("ResourceNotFoundException", "gone")},
		{name: "other code fails the phase", err: apiErr("InvalidRequestException", "bad"), wantErr: "InvalidRequestException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, kav2ListApplicationSnapshotsOp)
			var calls parentCalls
			client := newKAV2TestClient(map[string]func(any) (any, error){
				"ListApplicationSnapshots": kav2SnapshotsHandler(&calls, map[string][][]kav2types.SnapshotDetails{
					"app3": {{kav2Snapshot("snap-3")}},
				}, map[string]error{"app1": tc.err, "app2": tc.err}),
			})
			total, _, err := scanKAV2ApplicationSnapshots(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, kav2Apps(t, st, "app1", "app2", "app3"))
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
			if _, ok := rowsByNativeID(t, st, TypeKAV2ApplicationSnapshot)[kav2AppARN("app3")+"/snapshot/snap-3"]; !ok || total != 1 {
				t.Errorf("total = %d, sibling row stored = %v; want 1, true", total, ok)
			}
		})
	}
}

// The two skipped codes each warn once, so neither hides the other.
func TestScanKAV2ApplicationSnapshots_OneWarningPerSkippedCode(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, kav2ListApplicationSnapshotsOp)
	var calls parentCalls
	client := newKAV2TestClient(map[string]func(any) (any, error){
		"ListApplicationSnapshots": kav2SnapshotsHandler(&calls, nil, map[string]error{
			"app1": apiErr("UnsupportedOperationException", "x"),
			"app2": apiErr("InvalidArgumentException", "y"),
		}),
	})
	_, _, err := scanKAV2ApplicationSnapshots(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, kav2Apps(t, st, "app1", "app2"))
	if err != nil || *warns != 2 {
		t.Errorf("err, warnings = %v, %d; want nil, 2", err, *warns)
	}
}

// A skipped application keeps the rows its earlier pages stored.
func TestScanKAV2ApplicationSnapshots_SkipKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newKAV2TestClient(map[string]func(any) (any, error){
		"ListApplicationSnapshots": kav2SnapshotsHandler(&calls, map[string][][]kav2types.SnapshotDetails{
			"app1": {{kav2Snapshot("snap-1")}, {kav2Snapshot("snap-2")}},
		}, map[string]error{"app1/p2": apiErr("UnsupportedOperationException", "x")}),
	})
	total, _, err := scanKAV2ApplicationSnapshots(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, kav2Apps(t, st, "app1"))
	if err != nil || total != 1 {
		t.Errorf("total, err = %d, %v; want 1, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeKAV2ApplicationSnapshot)[kav2AppARN("app1")+"/snapshot/snap-1"]; !ok {
		t.Error("first-page row not kept")
	}
}

// The listing stops when the service repeats the token it was sent, rather
// than looping on it.
func TestScanKAV2ApplicationSnapshots_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var n atomic.Int64
	client := newKAV2TestClient(map[string]func(any) (any, error){
		"ListApplicationSnapshots": func(any) (any, error) {
			if n.Add(1) > 5 {
				return nil, apiErr("InvalidRequestException", "looped")
			}
			return &kinesisanalyticsv2.ListApplicationSnapshotsOutput{NextToken: sdkaws.String("same")}, nil
		},
	})
	_, _, err := scanKAV2ApplicationSnapshots(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, kav2Apps(t, st, "app1"))
	if err != nil || n.Load() != 2 {
		t.Errorf("err, calls = %v, %d; want nil, 2", err, n.Load())
	}
}

// kav2TopLevelHandlers answers the ops scanKinesisAnalyticsV2 calls with two
// applications: app1 has a detail body, app2's detail read is denied so it is
// stored from its summary. Each application has one snapshot.
func kav2TopLevelHandlers(st *store.Store, calls *parentCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListApplications": func(any) (any, error) {
			return &kinesisanalyticsv2.ListApplicationsOutput{ApplicationSummaries: []kav2types.ApplicationSummary{
				{ApplicationARN: sdkaws.String(kav2AppARN("app1")), ApplicationName: sdkaws.String("app1")},
				{ApplicationARN: sdkaws.String(kav2AppARN("app2")), ApplicationName: sdkaws.String("app2")},
			}}, nil
		},
		"DescribeApplication": func(in any) (any, error) {
			name := sdkaws.ToString(in.(*kinesisanalyticsv2.DescribeApplicationInput).ApplicationName)
			if name != "app1" {
				return nil, kav2Denied
			}
			return &kinesisanalyticsv2.DescribeApplicationOutput{ApplicationDetail: &kav2types.ApplicationDetail{
				ApplicationARN: sdkaws.String(kav2AppARN("app1")), ApplicationName: sdkaws.String("app1"),
			}}, nil
		},
		"ListApplicationSnapshots": func(in any) (any, error) {
			name := sdkaws.ToString(in.(*kinesisanalyticsv2.ListApplicationSnapshotsInput).ApplicationName)
			// The application row must already be stored: the snapshot phase
			// fans out over stored applications only.
			if _, err := st.GetResource(store.ResourceID("aws", testAccountID, kav2AppARN(name))); err != nil {
				return nil, fmt.Errorf("application %s not stored before its snapshots: %w", name, err)
			}
			calls.add(name)
			return &kinesisanalyticsv2.ListApplicationSnapshotsOutput{SnapshotSummaries: []kav2types.SnapshotDetails{kav2Snapshot("snap-" + name)}}, nil
		},
	}
}

// The top-level scan lists every stored application's snapshots after the
// application phase, whether the application row came from its detail body
// or its summary.
func TestScanKinesisAnalyticsV2_SnapshotsOfStoredApplications(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(kav2TopLevelHandlers(st, &calls), kav2Denied)}

	total, _, err := scanKinesisAnalyticsV2(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4 (2 applications + 2 snapshots)", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"app1", "app2"}) {
		t.Errorf("applications queried = %v; want [app1 app2]", got)
	}
	for _, name := range []string{"app1", "app2"} {
		assertContainsNative(t, st, kav2AppARN(name), kav2AppARN(name)+"/snapshot/snap-"+name)
	}
}

// An application deleted between ListApplications and DescribeApplication is
// dropped silently: no error, no warning, no row, and no snapshot listing,
// while its sibling is stored with its snapshots.
func TestScanKinesisAnalyticsV2_ApplicationGoneBeforeDetailSkipped(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "kinesisanalyticsv2")
	var calls parentCalls
	h := kav2TopLevelHandlers(st, &calls)
	describe := h["DescribeApplication"]
	h["DescribeApplication"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*kinesisanalyticsv2.DescribeApplicationInput).ApplicationName) == "app2" {
			return nil, apiErr("ResourceNotFoundException", "gone")
		}
		return describe(in)
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, kav2Denied)}

	total, _, err := scanKinesisAnalyticsV2(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || *warns != 0 {
		t.Fatalf("err, warnings = %v, %d; want nil, 0", err, *warns)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (app1 and its snapshot)", total)
	}
	apps := rowsByNativeID(t, st, TypeKAV2Application)
	if _, ok := apps[kav2AppARN("app2")]; ok || len(apps) != 1 {
		t.Errorf("application rows = %v; want app1 only", apps)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"app1"}) {
		t.Errorf("applications queried for snapshots = %v; want [app1]", got)
	}
}

// A transient application-phase error is demoted to a warning, so the snapshot
// phase's hard error still reaches the dispatcher as a hard error, and the
// application rows stay.
func TestScanKinesisAnalyticsV2_ApplicationTransientDoesNotMaskSnapshotError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "kinesisanalyticsv2")
	var calls parentCalls
	h := kav2TopLevelHandlers(st, &calls)
	h["DescribeApplication"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["ListApplicationSnapshots"] = func(any) (any, error) { return nil, apiErr("InvalidRequestException", "bad") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, kav2Denied)}

	_, _, err := scanKinesisAnalyticsV2(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InvalidRequestException") {
		t.Errorf("err = %v; want the snapshot phase's InvalidRequestException", err)
	}
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the demoted transient", *warns)
	}
	if n := len(rowsByNativeID(t, st, TypeKAV2Application)); n != 2 {
		t.Errorf("application rows = %d; want 2", n)
	}
}
