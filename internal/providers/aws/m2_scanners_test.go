package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/m2"
	m2types "github.com/aws/aws-sdk-go-v2/service/m2/types"
	"github.com/icearp/disco-cli/store"
)

var (
	m2Denied    = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: m2:List")
	m2Throttled = apiErr("ThrottlingException", "Rate exceeded")
	m2QuotaErr  = apiErr("ServiceQuotaExceededException", "quota")
)

func newM2TestClient(handlers map[string]func(in any) (any, error)) *m2.Client {
	return m2.NewFromConfig(opStubConfig(handlers, m2Denied))
}

func m2AppARN(id string) string {
	return fmt.Sprintf("arn:aws:m2:%s:%s:app/%s", testRegion, testAccountID, id)
}

// m2Apps stores an application row for each id and returns them as parents.
func m2Apps(t *testing.T, st *store.Store, ids ...string) []childParent {
	t.Helper()
	apps := make([]childParent, len(ids))
	for i, id := range ids {
		apps[i] = childParent{id: id, arn: m2AppARN(id)}
		upsertTestResource(t, st, "aws", testAccountID, TypeM2Application, apps[i].arn, testRegion, "{}")
	}
	return apps
}

func m2DataSet(name string) m2types.DataSetSummary {
	return m2types.DataSetSummary{DataSetName: sdkaws.String(name), DataSetOrg: sdkaws.String("VSAM")}
}

// m2DataSetsHandler answers ListDataSets from pages keyed by application id:
// the first page of an application carries NextToken "p2" when it has a
// second. errs fails an application's call with the given error; a "/p2" key
// fails only its second page. A call whose MaxResults is not the model cap
// fails, so the page size is pinned.
func m2DataSetsHandler(calls *parentCalls, pages map[string][][]m2types.DataSetSummary, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*m2.ListDataSetsInput)
		if sdkaws.ToInt32(req.MaxResults) != 2000 {
			return nil, fmt.Errorf("MaxResults = %v; want 2000", req.MaxResults)
		}
		id := sdkaws.ToString(req.ApplicationId)
		calls.add(id)
		page := 0
		if sdkaws.ToString(req.NextToken) == "p2" {
			page = 1
			if err := errs[id+"/p2"]; err != nil {
				return nil, err
			}
		} else if err := errs[id]; err != nil {
			return nil, err
		}
		out := &m2.ListDataSetsOutput{}
		if page < len(pages[id]) {
			out.DataSets = pages[id][page]
		}
		if page == 0 && len(pages[id]) > 1 {
			out.NextToken = sdkaws.String("p2")
		}
		return out, nil
	}
}

func TestScanM2Datasets_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	first := m2DataSet("AWS.M2.CARDDEMO.ACCTDATA.VSAM.KSDS")
	first.CreationTime = &created
	var calls parentCalls
	client := newM2TestClient(map[string]func(any) (any, error){
		"ListDataSets": m2DataSetsHandler(&calls, map[string][][]m2types.DataSetSummary{
			"app-1": {{first, {}}, {m2DataSet("a/b")}},
			"app-2": {{m2DataSet("DS2")}},
		}, nil),
	})

	total, _, err := scanM2Datasets(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, m2Apps(t, st, "app-1", "app-2"))
	if err != nil {
		t.Fatalf("scanM2Datasets: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"app-1", "app-2"}) {
		t.Errorf("applications queried = %v; want [app-1 app-2]", got)
	}
	rows := rowsByNativeID(t, st, TypeM2Dataset)
	if len(rows) != 3 {
		t.Fatalf("rows = %d; want 3 (the nameless element skipped): %v", len(rows), rows)
	}
	firstID := m2AppARN("app-1") + "/dataset/AWS.M2.CARDDEMO.ACCTDATA.VSAM.KSDS"
	for _, want := range []struct{ app, id string }{
		{m2AppARN("app-1"), firstID},
		{m2AppARN("app-1"), m2AppARN("app-1") + "/dataset/a/b"},
		{m2AppARN("app-2"), m2AppARN("app-2") + "/dataset/DS2"},
	} {
		r, ok := rows[want.id]
		if !ok {
			t.Errorf("missing data set %s", want.id)
			continue
		}
		if r.Type != TypeM2Dataset || sv(r.Region) != testRegion {
			t.Errorf("%s: Type %q Region %q", want.id, r.Type, sv(r.Region))
		}
		assertContainsNative(t, st, want.app, want.id)
	}
	r := rows[firstID]
	if sv(r.Name) != "AWS.M2.CARDDEMO.ACCTDATA.VSAM.KSDS" {
		t.Errorf("Name = %q", sv(r.Name))
	}
	if sv(r.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("CreatedAt = %q; want 2026-01-02T03:04:05Z", sv(r.CreatedAt))
	}
	if !strings.Contains(r.AttributesJSON, `"DataSetOrg":"VSAM"`) {
		t.Errorf("attrs = %s; want DataSetOrg VSAM", r.AttributesJSON)
	}
}

func TestScanM2Datasets_Empty(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newM2TestClient(map[string]func(any) (any, error){
		"ListDataSets": m2DataSetsHandler(&calls, nil, nil),
	})
	total, _, err := scanM2Datasets(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, m2Apps(t, st, "app-1"))
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeM2Dataset)); n != 0 {
		t.Errorf("rows = %d; want 0", n)
	}
}

// Each per-application error is skipped or returned by its code. Two of three
// applications fail, so a skip that warns must warn once for both.
func TestScanM2Datasets_PerApplicationErrors(t *testing.T) {
	notFound := apiErr("ResourceNotFoundException", "gone")
	validation := apiErr("ValidationException", "not ready")
	conflict := apiErr("ConflictException", "not deployed")
	for _, tc := range []struct {
		name      string
		err       error
		wantWarns int
		wantErr   bool
	}{
		{name: "not found skipped silently", err: notFound, wantWarns: 0},
		{name: "denied warns once", err: m2Denied, wantWarns: 1},
		{name: "throttled warns once", err: m2Throttled, wantWarns: 1},
		{name: "validation warns once", err: validation, wantWarns: 1},
		{name: "conflict warns once", err: conflict, wantWarns: 1},
		{name: "execution timeout warns once as transient", err: &m2types.ExecutionTimeoutException{Message: sdkaws.String("slow")}, wantWarns: 1},
		{name: "validation naming another field warns once", err: m2ValidationOn("applicationId"), wantWarns: 1},
		{name: "other code fails the phase", err: m2QuotaErr, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, m2ListDataSetsOp)
			var calls parentCalls
			client := newM2TestClient(map[string]func(any) (any, error){
				"ListDataSets": m2DataSetsHandler(&calls, map[string][][]m2types.DataSetSummary{
					"app-3": {{m2DataSet("DS3")}},
				}, map[string]error{"app-1": tc.err, "app-2": tc.err}),
			})
			total, _, err := scanM2Datasets(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, m2Apps(t, st, "app-1", "app-2", "app-3"))
			if tc.wantErr {
				if !isAPIErrorCode(err, "ServiceQuotaExceededException") {
					t.Errorf("err = %v; want ServiceQuotaExceededException", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			if _, ok := rowsByNativeID(t, st, TypeM2Dataset)[m2AppARN("app-3")+"/dataset/DS3"]; !ok || total != 1 {
				t.Errorf("total = %d, sibling row stored = %v; want 1, true", total, ok)
			}
		})
	}
}

func m2ValidationOn(field string) error {
	return &m2types.ValidationException{
		Message:   sdkaws.String("invalid"),
		FieldList: []m2types.ValidationExceptionField{{Name: sdkaws.String(field), Message: sdkaws.String("invalid")}},
	}
}

// A ValidationException naming maxResults is disco's own input error, so it
// fails the phase rather than being skipped as a not-ready application.
func TestScanM2Datasets_PageSizeRejectedFails(t *testing.T) {
	for _, field := range []string{"maxResults", "MaxResults"} {
		t.Run(field, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, m2ListDataSetsOp)
			var calls parentCalls
			client := newM2TestClient(map[string]func(any) (any, error){
				"ListDataSets": m2DataSetsHandler(&calls, nil, map[string]error{"app-1": m2ValidationOn(field)}),
			})
			_, _, err := scanM2Datasets(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, m2Apps(t, st, "app-1"))
			if got := classifyServiceError(err); got != outcomeError || *warns != 0 {
				t.Errorf("err = %v (outcome %v), warnings = %d; want outcomeError and no warning", err, got, *warns)
			}
		})
	}
}

// A skipped application keeps the rows its earlier pages stored.
func TestScanM2Datasets_SkipKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newM2TestClient(map[string]func(any) (any, error){
		"ListDataSets": m2DataSetsHandler(&calls, map[string][][]m2types.DataSetSummary{
			"app-1": {{m2DataSet("DS1")}, {m2DataSet("DS2")}},
		}, map[string]error{"app-1/p2": apiErr("ValidationException", "x")}),
	})
	total, _, err := scanM2Datasets(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, m2Apps(t, st, "app-1"))
	if err != nil || total != 1 {
		t.Errorf("total, err = %d, %v; want 1, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeM2Dataset)[m2AppARN("app-1")+"/dataset/DS1"]; !ok {
		t.Error("first-page row not kept")
	}
}

// m2Fixture answers every op of an m2 scan and records its calls. Each
// account-wide listing returns its "-tagged" element on its first page and
// its "-bare" one on its second. app-tagged has two deployment pages and one
// data set; app-bare has one deployment and no data set. Every "-tagged"
// application and environment is tagged with its type. Handlers run on
// fan-out goroutines, so mu guards the state they share.
type m2Fixture struct {
	partition, region string

	mu         sync.Mutex
	calls      map[string]int
	maxResults map[string][]int32
	tagARNs    []string
	deployApps parentCalls
	dataSets   parentCalls
	tags       map[string]map[string]string
	errs       map[string]error // tag read errors by ARN
	// listErrs fails a listing: key op for its first page, op+"/p2" for its
	// second; ListDeployments keys add "/"+appID before that.
	listErrs    map[string]error
	apps        [][]m2types.ApplicationSummary
	envs        [][]m2types.EnvironmentSummary
	deployments map[string][][]m2types.DeploymentSummary
	dataSetPgs  map[string][][]m2types.DataSetSummary
}

// m2TestTotal is every row of the fixture: 2 applications, 2 environments,
// 3 deployments and 1 data set.
const m2TestTotal = 8

var m2TestCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newM2Fixture() *m2Fixture { return newM2FixtureIn("aws", testRegion) }

func newM2FixtureIn(partition, region string) *m2Fixture {
	f := &m2Fixture{
		partition: partition, region: region,
		calls: map[string]int{}, maxResults: map[string][]int32{},
		errs: map[string]error{}, listErrs: map[string]error{},
	}
	f.apps = [][]m2types.ApplicationSummary{{f.app("app-tagged")}, {f.app("app-bare")}}
	f.envs = [][]m2types.EnvironmentSummary{{f.env("env-tagged")}, {f.env("env-bare")}}
	f.deployments = map[string][][]m2types.DeploymentSummary{
		"app-tagged": {{m2Deployment("app-tagged", "d-1")}, {m2Deployment("app-tagged", "d-2")}},
		"app-bare":   {{m2Deployment("app-bare", "d-3")}},
	}
	f.dataSetPgs = map[string][][]m2types.DataSetSummary{"app-tagged": {{m2DataSet("DS1")}}}
	f.tags = map[string]map[string]string{
		f.appARN("app-tagged"): {"type": TypeM2Application},
		f.envARN("env-tagged"): {"type": TypeM2Environment},
	}
	return f
}

func (f *m2Fixture) appARN(id string) string {
	return fmt.Sprintf("arn:%s:m2:%s:%s:app/%s", f.partition, f.region, testAccountID, id)
}

func (f *m2Fixture) envARN(id string) string {
	return fmt.Sprintf("arn:%s:m2:%s:%s:env/%s", f.partition, f.region, testAccountID, id)
}

func (f *m2Fixture) app(id string) m2types.ApplicationSummary {
	return m2types.ApplicationSummary{
		ApplicationArn: sdkaws.String(f.appARN(id)), ApplicationId: sdkaws.String(id), Name: sdkaws.String("name-" + id),
		Status: m2types.ApplicationLifecycleAvailable, EngineType: m2types.EngineTypeMicrofocus, CreationTime: &m2TestCreated,
	}
}

func (f *m2Fixture) env(id string) m2types.EnvironmentSummary {
	return m2types.EnvironmentSummary{
		EnvironmentArn: sdkaws.String(f.envARN(id)), EnvironmentId: sdkaws.String(id), Name: sdkaws.String("name-" + id),
		Status: m2types.EnvironmentLifecycleAvailable, EngineType: m2types.EngineTypeMicrofocus, CreationTime: &m2TestCreated,
	}
}

// m2Deployment is a deployment of appID to env-tagged, whose EnvironmentId
// the resolver reads from the row's attributes.
func m2Deployment(appID, id string) m2types.DeploymentSummary {
	return m2types.DeploymentSummary{
		ApplicationId: sdkaws.String(appID), DeploymentId: sdkaws.String(id), EnvironmentId: sdkaws.String("env-tagged"),
		Status: m2types.DeploymentLifecycleSucceeded, CreationTime: &m2TestCreated,
	}
}

// allTagARNs is the ARN of every application and environment, sorted.
func (f *m2Fixture) allTagARNs() []string {
	return slices.Sorted(slices.Values([]string{f.appARN("app-bare"), f.appARN("app-tagged"), f.envARN("env-bare"), f.envARN("env-tagged")}))
}

func (f *m2Fixture) record(op string, in any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[op]++
	f.maxResults[op] = append(f.maxResults[op], sdkaws.ToInt32(bacField[*int32](in, "MaxResults")))
}

func (f *m2Fixture) count(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[op]
}

func (f *m2Fixture) sortedTagARNs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.tagARNs))
}

// m2FixturePage answers one page of pages: the second when token is "p2",
// else the first with NextToken "p2" when a second exists, or the error
// listErrs holds for key (key+"/p2" for the second page).
func m2FixturePage[T any](f *m2Fixture, key string, token *string, pages [][]T) ([]T, *string, error) {
	page, errKey := 0, key
	if sdkaws.ToString(token) == "p2" {
		page, errKey = 1, key+"/p2"
	}
	f.mu.Lock()
	err := f.listErrs[errKey]
	f.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	var items []T
	if page < len(pages) {
		items = pages[page]
	}
	var next *string
	if page == 0 && len(pages) > 1 {
		next = sdkaws.String("p2")
	}
	return items, next, nil
}

func (f *m2Fixture) handlers() map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListApplications": func(in any) (any, error) {
			f.record("ListApplications", in)
			items, next, err := m2FixturePage(f, "ListApplications", in.(*m2.ListApplicationsInput).NextToken, f.apps)
			if err != nil {
				return nil, err
			}
			return &m2.ListApplicationsOutput{Applications: items, NextToken: next}, nil
		},
		"ListEnvironments": func(in any) (any, error) {
			f.record("ListEnvironments", in)
			items, next, err := m2FixturePage(f, "ListEnvironments", in.(*m2.ListEnvironmentsInput).NextToken, f.envs)
			if err != nil {
				return nil, err
			}
			return &m2.ListEnvironmentsOutput{Environments: items, NextToken: next}, nil
		},
		"ListDeployments": func(in any) (any, error) {
			f.record("ListDeployments", in)
			req := in.(*m2.ListDeploymentsInput)
			app := sdkaws.ToString(req.ApplicationId)
			f.deployApps.add(app)
			items, next, err := m2FixturePage(f, "ListDeployments/"+app, req.NextToken, f.deployments[app])
			if err != nil {
				return nil, err
			}
			return &m2.ListDeploymentsOutput{Deployments: items, NextToken: next}, nil
		},
		"ListDataSets": func(in any) (any, error) {
			f.record("ListDataSets", in)
			return m2DataSetsHandler(&f.dataSets, f.dataSetPgs, nil)(in)
		},
		"ListTagsForResource": func(in any) (any, error) {
			arn := sdkaws.ToString(in.(*m2.ListTagsForResourceInput).ResourceArn)
			f.mu.Lock()
			defer f.mu.Unlock()
			f.tagARNs = append(f.tagARNs, arn)
			if err := f.errs[arn]; err != nil {
				return nil, err
			}
			return &m2.ListTagsForResourceOutput{Tags: f.tags[arn]}, nil
		},
	}
}

// m2Scan runs the scan's real entry point against handlers in the fixture's
// partition and region; every op they do not answer is denied.
func m2Scan(ctx context.Context, st *store.Store, f *m2Fixture, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.Partition = f.partition
	acct.cfg = opStubConfig(handlers, m2Denied)
	return scanM2(ctx, acct, f.region, st, testScanID)
}

func m2WarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Applications and environments are stored under their listed ARNs with
// their tags (nil for none), their name and status, and no CreatedAt;
// ListTagsForResource is sent exactly their ARNs, never a deployment's or a
// data set's. Deployments and data sets are stored untagged. Every listing
// sends MaxResults 2000. No error, no warning.
func TestScanM2_StoresTagsOfApplicationsAndEnvironments(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newM2Fixture()

	total, inserted, err := m2Scan(context.Background(), st, f, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != m2TestTotal || inserted != m2TestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, m2TestTotal, m2TestTotal)
	}
	if got := f.sortedTagARNs(); !slices.Equal(got, f.allTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, f.allTagARNs())
	}
	for op, want := range map[string][]int32{
		"ListApplications": {2000, 2000}, "ListEnvironments": {2000, 2000},
		"ListDeployments": {2000, 2000, 2000}, "ListDataSets": {2000, 2000},
	} {
		if got := f.maxResults[op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", op, got, want)
		}
	}
	for _, c := range []struct {
		typ, arn, name string
		tags           map[string]string
	}{
		{TypeM2Application, f.appARN("app-tagged"), "name-app-tagged", map[string]string{"type": TypeM2Application}},
		{TypeM2Application, f.appARN("app-bare"), "name-app-bare", nil},
		{TypeM2Environment, f.envARN("env-tagged"), "name-env-tagged", map[string]string{"type": TypeM2Environment}},
		{TypeM2Environment, f.envARN("env-bare"), "name-env-bare", nil},
	} {
		rows := rowsByNativeID(t, st, c.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", c.typ, len(rows))
		}
		r, ok := rows[c.arn]
		if !ok {
			t.Errorf("%s %s not stored", c.typ, c.arn)
			continue
		}
		if sv(r.Name) != c.name || sv(r.Status) != "Available" || sv(r.Region) != testRegion || r.CreatedAt != nil || r.ManagedByProvider {
			t.Errorf("%s: Name %q Status %q Region %q CreatedAt %v ManagedByProvider %v; want %q Available %s nil false",
				c.arn, sv(r.Name), sv(r.Status), sv(r.Region), r.CreatedAt, r.ManagedByProvider, c.name, testRegion)
		}
		if !strings.Contains(r.AttributesJSON, `"EngineType":"microfocus"`) {
			t.Errorf("%s attrs = %s; want the listed element", c.arn, r.AttributesJSON)
		}
		if c.tags == nil {
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", c.arn, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, c.tags)
	}
	deps := rowsByNativeID(t, st, TypeM2Deployment)
	for _, c := range []struct{ app, id string }{{"app-tagged", "d-1"}, {"app-tagged", "d-2"}, {"app-bare", "d-3"}} {
		nativeID := f.appARN(c.app) + "/deployment/" + c.id
		r, ok := deps[nativeID]
		if !ok {
			t.Errorf("deployment %s not stored", nativeID)
			continue
		}
		if r.TagsJSON != nil || sv(r.Status) != "Succeeded" || sv(r.Region) != testRegion || r.ManagedByProvider {
			t.Errorf("%s: TagsJSON %v Status %q Region %q ManagedByProvider %v; want nil Succeeded %s false",
				nativeID, r.TagsJSON, sv(r.Status), sv(r.Region), r.ManagedByProvider, testRegion)
		}
		// resolveM2DeploymentRefs links a deployment to its environment
		// through the EnvironmentId these attributes carry.
		if want := mustJSON(m2Deployment(c.app, c.id)); r.AttributesJSON != want {
			t.Errorf("%s attrs = %s; want the listed element %s", nativeID, r.AttributesJSON, want)
		}
	}
	if len(deps) != 3 {
		t.Errorf("deployment rows = %d; want 3", len(deps))
	}
	if r := deps[f.appARN("app-tagged")+"/deployment/d-1"]; sv(r.Name) != "d-1" || r.CreatedAt != nil {
		t.Errorf("deployment Name %q CreatedAt %v; want d-1 nil", sv(r.Name), r.CreatedAt)
	}
	ds, ok := rowsByNativeID(t, st, TypeM2Dataset)[f.appARN("app-tagged")+"/dataset/DS1"]
	if !ok || ds.TagsJSON != nil {
		t.Errorf("data set stored %v, TagsJSON %v; want stored untagged", ok, ds.TagsJSON)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanM2_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	f := newM2Fixture()
	if _, _, err := m2Scan(context.Background(), st, f, f.handlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := m2Scan(context.Background(), st, f, f.handlers())
	if err != nil || total != m2TestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted, err = %d, %d, %v; want %d, 0, nil", total, inserted, err, m2TestTotal)
	}
}

// Empty listings: each account-wide listing called once, no per-application
// listing, no tag read, nothing stored, no error, no warning.
func TestScanM2_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newM2Fixture()
	f.apps, f.envs = nil, nil

	total, _, err := m2Scan(context.Background(), st, f, f.handlers())
	if err != nil || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
	}
	for op, want := range map[string]int{"ListApplications": 1, "ListEnvironments": 1, "ListDeployments": 0, "ListDataSets": 0} {
		if n := f.count(op); n != want {
			t.Errorf("%s called %d times; want %d", op, n, want)
		}
	}
	if got := f.sortedTagARNs(); len(got) != 0 {
		t.Errorf("tag reads = %v; want none", got)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

var (
	m2TestGone = apiErr("ResourceNotFoundException", "not found")
	m2TestBad  = apiErr("ValidationException", "bad")
)

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently, and an application so
// dropped has no deployments or data sets listed. A denial keeps the row
// untagged and warns once under the tag op with the scan's scope; a throttle
// keeps it and is transient; any other error keeps it and is an error.
func TestScanM2_TagReadFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		typ       string
		id        string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"application/not found", TypeM2Application, "app-tagged", m2TestGone, -1, "", m2TestTotal - 4, false, false},
		{"environment/not found", TypeM2Environment, "env-tagged", m2TestGone, -1, "", m2TestTotal - 1, false, false},
		{"application/denied", TypeM2Application, "app-tagged", m2Denied, -1, "", m2TestTotal, true, true},
		{"environment/denied", TypeM2Environment, "env-tagged", m2Denied, -1, "", m2TestTotal, true, true},
		{"application/transient", TypeM2Application, "app-tagged", m2Throttled, outcomeTransient, "ThrottlingException", m2TestTotal, true, false},
		{"environment/other error", TypeM2Environment, "env-tagged", m2TestBad, outcomeError, "ValidationException", m2TestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newM2Fixture()
			arn, bare := f.appARN(c.id), f.appARN("app-bare")
			if c.typ == TypeM2Environment {
				arn, bare = f.envARN(c.id), f.envARN("env-bare")
			}
			f.errs[arn] = c.err

			total, _, err := m2Scan(context.Background(), st, f, f.handlers())
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, c.typ)
			r, ok := rows[arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if _, ok := rows[bare]; !ok {
				t.Errorf("sibling %s missing", bare)
			}
			wantApps := []string{"app-bare", "app-tagged"}
			if c.typ == TypeM2Application && !c.wantKept {
				wantApps = []string{"app-bare"}
			}
			if got := f.deployApps.distinct(); !slices.Equal(got, wantApps) {
				t.Errorf("ListDeployments applications = %v; want %v", got, wantApps)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{m2TagsOp}
			}
			if got := m2WarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads in both tagged phases send one warning for the whole
// scan; the rows are kept.
func TestScanM2_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newM2Fixture()
	f.errs[f.appARN("app-bare")] = m2Denied
	f.errs[f.envARN("env-tagged")] = m2Denied

	total, _, err := m2Scan(context.Background(), st, f, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != m2TestTotal {
		t.Errorf("total = %d; want %d", total, m2TestTotal)
	}
	if got := m2WarnOps(warns()); !slices.Equal(got, []string{m2TagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, m2TagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanM2_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, m2TagsOp)
	f := newM2Fixture()
	f.errs[f.appARN("app-bare")] = m2Throttled
	f.errs[f.envARN("env-tagged")] = m2TestBad

	_, _, err := m2Scan(context.Background(), st, f, f.handlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", m2TagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phase's rows
// stay stored.
func TestScanM2_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeM2Environment)
	f := newM2Fixture()
	f.errs[f.envARN("env-tagged")] = m2Denied

	total, _, err := m2Scan(context.Background(), st, f, f.handlers())
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListDeployments", "ListDataSets"} {
		if n := f.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeM2Application)); total != 2 || n != 2 {
		t.Errorf("total = %d, application rows = %d; want 2, 2", total, n)
	}
}

// A listing's failed later page keeps the page read before it, and its error
// does not stop the later phases; a failed first page does not either. A
// denied listing is warned about under its op instead, and the later phases
// run too.
func TestScanM2_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		name, key string
		err       error
		wantTotal int
		wantApps  []string // applications whose deployments are listed
		warnOp    string   // "" for none
	}{
		{"applications page 2", "ListApplications/p2", m2TestBad, m2TestTotal - 2, []string{"app-tagged"}, ""},
		{"applications page 1", "ListApplications", m2TestBad, 2, nil, ""},
		{"applications denied", "ListApplications", m2Denied, 2, nil, m2ListApplicationsOp},
		{"environments page 2", "ListEnvironments/p2", m2TestBad, m2TestTotal - 1, []string{"app-bare", "app-tagged"}, ""},
		{"environments page 1", "ListEnvironments", m2TestBad, m2TestTotal - 2, []string{"app-bare", "app-tagged"}, ""},
		{"environments denied", "ListEnvironments", m2Denied, m2TestTotal - 2, []string{"app-bare", "app-tagged"}, m2ListEnvironmentsOp},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newM2Fixture()
			f.listErrs[c.key] = c.err

			total, _, err := m2Scan(context.Background(), st, f, f.handlers())
			if c.warnOp == "" {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ValidationException")
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if got := f.deployApps.distinct(); !slices.Equal(got, c.wantApps) {
				t.Errorf("ListDeployments applications = %v; want %v", got, c.wantApps)
			}
			if n := f.count("ListEnvironments"); n == 0 {
				t.Error("ListEnvironments not called")
			}
			var want []string
			if c.warnOp != "" {
				want = []string{c.warnOp}
			}
			if got := m2WarnOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages (the
// one that repeated it included), and the later phases still run.
func TestScanM2_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	f := newM2Fixture()
	handlers := f.handlers()
	handlers["ListApplications"] = func(in any) (any, error) {
		f.record("ListApplications", in)
		if in.(*m2.ListApplicationsInput).NextToken == nil {
			return &m2.ListApplicationsOutput{Applications: []m2types.ApplicationSummary{f.app("app-tagged")}, NextToken: sdkaws.String("p2")}, nil
		}
		return &m2.ListApplicationsOutput{Applications: []m2types.ApplicationSummary{f.app("app-bare")}, NextToken: sdkaws.String("p2")}, nil
	}

	total, _, err := m2Scan(context.Background(), st, f, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if n := f.count("ListApplications"); n != 2 {
		t.Errorf("ListApplications called %d times; want 2", n)
	}
	if total != m2TestTotal {
		t.Errorf("total = %d; want %d", total, m2TestTotal)
	}
	if _, ok := rowsByNativeID(t, st, TypeM2Application)[f.appARN("app-bare")]; !ok {
		t.Error("application of the page that repeated the token not kept")
	}
}

// An element listed without an ARN is not stored, not sent to the tag op,
// and warned about once under its listing, keyed by its name. An application
// without an ApplicationId is stored and tagged, but its deployments and
// data sets cannot be listed: one warning keyed by its ARN says so. A
// deployment without a DeploymentId is warned about under ListDeployments,
// keyed by its application's ARN.
func TestScanM2_ElementsWithoutIdentifier(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newM2Fixture()
	noARNApp, noIDApp := f.app("app-noarn"), f.app("app-noid")
	noARNApp.ApplicationArn, noIDApp.ApplicationId = nil, nil
	noARNEnv := f.env("env-noarn")
	noARNEnv.EnvironmentArn = nil
	f.apps[1] = append(f.apps[1], noARNApp, noIDApp)
	f.envs[1] = append(f.envs[1], noARNEnv)
	f.deployments["app-bare"][0] = append(f.deployments["app-bare"][0], m2types.DeploymentSummary{})

	total, _, err := m2Scan(context.Background(), st, f, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := m2TestTotal + 1; total != want {
		t.Errorf("total = %d; want %d (app-noid added)", total, want)
	}
	want := append(f.allTagARNs(), f.appARN("app-noid"))
	slices.Sort(want)
	if got := f.sortedTagARNs(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
	if got := f.deployApps.distinct(); !slices.Equal(got, []string{"app-bare", "app-tagged"}) {
		t.Errorf("ListDeployments applications = %v; want [app-bare app-tagged]", got)
	}
	w := warns()
	if got := m2WarnOps(w); !slices.Equal(got, []string{m2ListApplicationsOp, m2ListApplicationsOp, m2ListEnvironmentsOp, m2ListDeploymentsOp}) {
		t.Fatalf("warnings = %v; want two under ListApplications, one under ListEnvironments and one under ListDeployments", w)
	}
	for _, c := range []struct {
		op, first string
	}{
		{m2ListApplicationsOp, "name-app-noarn"},
		{m2ListApplicationsOp, f.appARN("app-noid")},
		{m2ListEnvironmentsOp, "name-env-noarn"},
		{m2ListDeploymentsOp, "a deployment of " + f.appARN("app-bare")},
	} {
		if !slices.ContainsFunc(w, func(x store.ScanWarning) bool {
			return x.Service == c.op && strings.HasSuffix(x.Message, "first "+c.first)
		}) {
			t.Errorf("no %s warning keyed %q in %v", c.op, c.first, w)
		}
	}
}

// A GovCloud or China ARN reaches the tag op unchanged and is the row's
// NativeID; deployments and data sets are keyed under it.
func TestScanM2_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			f := newM2FixtureIn(c.partition, c.region)
			if _, _, err := m2Scan(context.Background(), st, f, f.handlers()); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := f.sortedTagARNs(); !slices.Equal(got, f.allTagARNs()) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, f.allTagARNs())
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeM2Application)[f.appARN("app-tagged")].TagsJSON, map[string]string{"type": TypeM2Application})
			assertTagsMap(t, rowsByNativeID(t, st, TypeM2Environment)[f.envARN("env-tagged")].TagsJSON, map[string]string{"type": TypeM2Environment})
			if _, ok := rowsByNativeID(t, st, TypeM2Deployment)[f.appARN("app-bare")+"/deployment/d-3"]; !ok {
				t.Error("deployment not keyed under its application's ARN")
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanM2_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	f := newM2Fixture()
	handlers := f.handlers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListEnvironments"]
	handlers["ListEnvironments"] = func(in any) (any, error) {
		if in.(*m2.ListEnvironmentsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := m2Scan(ctx, st, f, handlers)
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
	for _, op := range []string{"ListDeployments", "ListDataSets"} {
		if n := f.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanM2_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "m2.us-east-1.amazonaws.com", IsNotFound: true}
	for _, c := range []struct {
		name      string
		op        string
		wantTotal int
	}{
		{"environments after applications", "ListEnvironments", m2TestTotal - 2},
		{"deployments", "ListDeployments", m2TestTotal - 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			f := newM2Fixture()
			handlers := f.handlers()
			handlers[c.op] = func(any) (any, error) { return nil, nx }
			total, _, err := m2Scan(context.Background(), st, f, handlers)
			assertGlueOutcome(t, err, outcomeError)
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
	t.Run("a tag read", func(t *testing.T) {
		st := newTestStore(t)
		f := newM2Fixture()
		f.errs[f.appARN("app-tagged")] = nx
		_, _, err := m2Scan(context.Background(), st, f, f.handlers())
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		f := newM2Fixture()
		f.listErrs["ListApplications"] = nx
		_, _, err := m2Scan(context.Background(), st, f, f.handlers())
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := f.count("ListEnvironments"); n != 0 {
			t.Errorf("ListEnvironments called %d times; want 0 once the region proved unavailable", n)
		}
	})
}

// One application's deployment listing failing costs the other application
// none of its deployments, and the failed one keeps its first page. An
// application deleted since it was listed is skipped silently when none of
// its listing was read; a denial warns once under ListDeployments; any other
// error is returned after the deployments are stored.
func TestScanM2_DeploymentFailuresPerApplication(t *testing.T) {
	for _, c := range []struct {
		name      string
		key       string
		err       error
		wantTotal int
		wantErr   bool
		warn      bool
	}{
		{"not found", "ListDeployments/app-tagged", m2TestGone, m2TestTotal - 2, false, false},
		{"denied", "ListDeployments/app-tagged", m2Denied, m2TestTotal - 2, false, true},
		{"other error on page 2", "ListDeployments/app-tagged/p2", m2TestBad, m2TestTotal - 1, true, false},
		{"other error", "ListDeployments/app-tagged", m2TestBad, m2TestTotal - 2, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newM2Fixture()
			f.listErrs[c.key] = c.err

			total, _, err := m2Scan(context.Background(), st, f, f.handlers())
			if c.wantErr {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ValidationException")
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if _, ok := rowsByNativeID(t, st, TypeM2Deployment)[f.appARN("app-bare")+"/deployment/d-3"]; !ok {
				t.Error("sibling application's deployment missing")
			}
			var want []string
			if c.warn {
				want = []string{m2ListDeploymentsOp}
			}
			if got := m2WarnOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// The data set phase runs over the applications stored, after every other
// phase: each other op fails if the data set phase already ran.
func TestScanM2_DatasetPhaseRunsLast(t *testing.T) {
	st := newTestStore(t)
	f := newM2Fixture()
	h := f.handlers()
	for _, op := range []string{"ListApplications", "ListEnvironments", "ListDeployments", "ListTagsForResource"} {
		inner := h[op]
		h[op] = func(in any) (any, error) {
			if len(f.dataSets.distinct()) > 0 {
				return nil, fmt.Errorf("%s called after ListDataSets", op)
			}
			return inner(in)
		}
	}
	if _, _, err := m2Scan(context.Background(), st, f, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := f.dataSets.distinct(); !slices.Equal(got, []string{"app-bare", "app-tagged"}) {
		t.Errorf("ListDataSets applications = %v; want [app-bare app-tagged]", got)
	}
	assertContainsNative(t, st, f.appARN("app-tagged"), f.appARN("app-tagged")+"/dataset/DS1")
}

// A transient listing error beside a hard error of another phase is demoted
// to one warning under its own op, so the hard error decides the outcome.
func TestScanM2_TransientDemotedBesideHardError(t *testing.T) {
	for _, c := range []struct{ listErr, op string }{
		{"ListEnvironments", m2ListEnvironmentsOp},
		{"ListDeployments/app-tagged", m2ListDeploymentsOp},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newM2Fixture()
			f.listErrs[c.listErr] = m2Throttled
			handlers := f.handlers()
			handlers["ListDataSets"] = func(any) (any, error) { return nil, m2QuotaErr }

			_, _, err := m2Scan(context.Background(), st, f, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ServiceQuotaExceededException")
			if got := m2WarnOps(warns()); !slices.Equal(got, []string{c.op}) {
				t.Errorf("warnings = %v; want one under %s", got, c.op)
			}
		})
	}
}
