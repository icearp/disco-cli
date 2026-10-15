package aws

import (
	"context"
	"fmt"
	"reflect"
	"strings"
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
		if sdkaws.ToInt32(req.MaxResults) != m2DataSetsPageLimit {
			return nil, fmt.Errorf("MaxResults = %v; want %d", req.MaxResults, m2DataSetsPageLimit)
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

// m2AllHandlers answers every m2 op scanM2All calls with one application,
// one environment, one deployment and one data set; override replaces ops.
func m2AllHandlers(calls *parentCalls, override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListApplications": func(any) (any, error) {
			return &m2.ListApplicationsOutput{Applications: []m2types.ApplicationSummary{{
				ApplicationArn: sdkaws.String(m2AppARN("app-1")), ApplicationId: sdkaws.String("app-1"), Name: sdkaws.String("app-1"),
			}}}, nil
		},
		"ListEnvironments": func(any) (any, error) {
			return &m2.ListEnvironmentsOutput{Environments: []m2types.EnvironmentSummary{{
				EnvironmentArn: sdkaws.String(fmt.Sprintf("arn:aws:m2:%s:%s:env/env-1", testRegion, testAccountID)), Name: sdkaws.String("env-1"),
			}}}, nil
		},
		"ListDeployments": func(any) (any, error) {
			return &m2.ListDeploymentsOutput{Deployments: []m2types.DeploymentSummary{{DeploymentId: sdkaws.String("d-1")}}}, nil
		},
		"ListDataSets": m2DataSetsHandler(calls, map[string][][]m2types.DataSetSummary{"app-1": {{m2DataSet("DS1")}}}, nil),
	}
	for op, f := range override {
		h[op] = f
	}
	return h
}

// The data set phase runs over the applications the older phases stored,
// after every older phase, and a hard error in an older phase after the
// application phase does not stop it.
func TestScanM2All_DatasetPhase(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failedOp  string
		wantTotal int
	}{
		{name: "all succeed", wantTotal: 4},
		{name: "environments fail", failedOp: "ListEnvironments", wantTotal: 2},
		{name: "deployments fail", failedOp: "ListDeployments", wantTotal: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			override := map[string]func(any) (any, error){}
			if tc.failedOp != "" {
				override[tc.failedOp] = func(any) (any, error) { return nil, m2QuotaErr }
			}
			client := newM2TestClient(m2AllHandlers(&calls, override))
			total, _, err := scanM2All(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if (tc.failedOp != "") != isAPIErrorCode(err, "ServiceQuotaExceededException") {
				t.Errorf("err = %v; failed op %q", err, tc.failedOp)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d; want %d", total, tc.wantTotal)
			}
			dataSetID := m2AppARN("app-1") + "/dataset/DS1"
			if _, ok := rowsByNativeID(t, st, TypeM2Dataset)[dataSetID]; !ok {
				t.Fatal("data set row missing")
			}
			assertContainsNative(t, st, m2AppARN("app-1"), dataSetID)
		})
	}
}

// Data sets are listed only after every older phase: each older op fails if
// the data set phase already ran.
func TestScanM2All_DatasetPhaseRunsLast(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := m2AllHandlers(&calls, nil)
	for _, op := range []string{"ListApplications", "ListEnvironments", "ListDeployments"} {
		inner := h[op]
		h[op] = func(in any) (any, error) {
			if len(calls.distinct()) > 0 {
				return nil, fmt.Errorf("%s called after ListDataSets", op)
			}
			return inner(in)
		}
	}
	if _, _, err := scanM2All(context.Background(), newM2TestClient(h), newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scanM2All: %v", err)
	}
}

// A failed application phase leaves the data set phase no applications.
func TestScanM2All_ApplicationsFail(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newM2TestClient(m2AllHandlers(&calls, map[string]func(any) (any, error){
		"ListApplications": func(any) (any, error) { return nil, m2QuotaErr },
	}))
	_, _, err := scanM2All(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ServiceQuotaExceededException") {
		t.Errorf("err = %v; want ServiceQuotaExceededException", err)
	}
	if got := calls.distinct(); len(got) != 0 {
		t.Errorf("ListDataSets called for %v; want none", got)
	}
}

// A transient older-phase error is demoted to one warning so that the data set
// phase's hard error still reaches the dispatcher as a hard error.
func TestScanM2All_TransientDemoted(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "m2")
	var calls parentCalls
	client := newM2TestClient(m2AllHandlers(&calls, map[string]func(any) (any, error){
		"ListEnvironments": func(any) (any, error) { return nil, m2Throttled },
		"ListDataSets":     func(any) (any, error) { return nil, m2QuotaErr },
	}))
	_, _, err := scanM2All(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "ServiceQuotaExceededException") {
		t.Errorf("err = %v (outcome %v); want only the hard ServiceQuotaExceededException (outcomeError)", err, got)
	}
	if *warns != 1 {
		t.Errorf("m2 warnings = %d; want 1", *warns)
	}
}
