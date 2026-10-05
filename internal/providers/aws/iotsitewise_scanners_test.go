package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotsitewise"
	iswtypes "github.com/aws/aws-sdk-go-v2/service/iotsitewise/types"
	"github.com/icearp/disco-cli/store"
)

// fakeIoTSW serves the workspace-family list ops from pages keyed by NextToken
// ("" is the first page). Account-level ops fail with pageErrs[token]; the
// per-workspace ops fail with errs[workspaceName].
// iswCallLog records the workspaces queried; childFanOut calls the fake from
// concurrent goroutines.
type iswCallLog struct {
	mu    sync.Mutex
	names []string
}

func (l *iswCallLog) add(name string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.names = append(l.names, name)
}

type fakeIoTSW struct {
	iotSWAPI
	workspaces   map[string]*iotsitewise.ListWorkspacesOutput
	applications map[string]*iotsitewise.ListApplicationsOutput
	pipelines    map[string]map[string]*iotsitewise.ListPipelinesOutput
	tasks        map[string]map[string]*iotsitewise.ListTasksOutput
	pageErrs     map[string]error
	errs         map[string]error
	queried      *iswCallLog
}

func (f fakeIoTSW) ListWorkspaces(_ context.Context, in *iotsitewise.ListWorkspacesInput, _ ...func(*iotsitewise.Options)) (*iotsitewise.ListWorkspacesOutput, error) {
	if err := f.pageErrs[sv(in.NextToken)]; err != nil {
		return nil, err
	}
	if out := f.workspaces[sv(in.NextToken)]; out != nil {
		return out, nil
	}
	return &iotsitewise.ListWorkspacesOutput{}, nil
}

func (f fakeIoTSW) ListApplications(_ context.Context, in *iotsitewise.ListApplicationsInput, _ ...func(*iotsitewise.Options)) (*iotsitewise.ListApplicationsOutput, error) {
	if err := f.pageErrs[sv(in.NextToken)]; err != nil {
		return nil, err
	}
	if out := f.applications[sv(in.NextToken)]; out != nil {
		return out, nil
	}
	return &iotsitewise.ListApplicationsOutput{}, nil
}

func (f fakeIoTSW) ListPipelines(_ context.Context, in *iotsitewise.ListPipelinesInput, _ ...func(*iotsitewise.Options)) (*iotsitewise.ListPipelinesOutput, error) {
	f.queried.add(*in.WorkspaceName)
	if err := f.errs[*in.WorkspaceName]; err != nil {
		return nil, err
	}
	if out := f.pipelines[*in.WorkspaceName][sv(in.NextToken)]; out != nil {
		return out, nil
	}
	return &iotsitewise.ListPipelinesOutput{}, nil
}

func (f fakeIoTSW) ListTasks(_ context.Context, in *iotsitewise.ListTasksInput, _ ...func(*iotsitewise.Options)) (*iotsitewise.ListTasksOutput, error) {
	f.queried.add(*in.WorkspaceName)
	if err := f.errs[*in.WorkspaceName]; err != nil {
		return nil, err
	}
	if out := f.tasks[*in.WorkspaceName][sv(in.NextToken)]; out != nil {
		return out, nil
	}
	return &iotsitewise.ListTasksOutput{}, nil
}

var iswCreated = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func iswWorkspaceARN(name string) string {
	return fmt.Sprintf("arn:aws:iotsitewise:%s:%s:workspace/%s", testRegion, testAccountID, name)
}

func iswWorkspace(name string) iswtypes.WorkspaceSummary {
	return iswtypes.WorkspaceSummary{
		Arn: sdkaws.String(iswWorkspaceARN(name)), Name: sdkaws.String(name), CreatedAt: &iswCreated,
		Status: &iswtypes.WorkspaceStatus{State: iswtypes.WorkspaceStateActive},
	}
}

func iswApplication(workspace, id string) iswtypes.ApplicationSummary {
	return iswtypes.ApplicationSummary{
		Arn: sdkaws.String(iswWorkspaceARN(workspace) + "/application/" + id),
		Id:  sdkaws.String(id), Name: sdkaws.String("app-" + id), WorkspaceName: sdkaws.String(workspace),
		Status: iswtypes.ApplicationStatus("ACTIVE"), CreatedAt: &iswCreated,
	}
}

func iswPipeline(workspace, name string) iswtypes.PipelineSummary {
	return iswtypes.PipelineSummary{
		PipelineArn: sdkaws.String(iswWorkspaceARN(workspace) + "/pipeline/" + name), PipelineName: sdkaws.String(name),
		Status: &iswtypes.ResourceStatus{State: iswtypes.ResourceStateActive}, CreatedAt: &iswCreated, Version: sdkaws.String("3"),
	}
}

func iswTask(workspace, name string) iswtypes.TaskSummary {
	return iswtypes.TaskSummary{
		TaskArn: sdkaws.String(iswWorkspaceARN(workspace) + "/task/" + name), TaskName: sdkaws.String(name),
		Status: &iswtypes.ResourceStatus{State: iswtypes.ResourceStateActive}, CreatedAt: &iswCreated, Version: sdkaws.String("2"),
	}
}

// assertISWRow checks the fields every workspace-family row carries.
func assertISWRow(t *testing.T, r store.Resource, wantType, wantName, wantStatus, attrKey string) {
	t.Helper()
	if r.Type != wantType || sv(r.Region) != testRegion || sv(r.Name) != wantName || sv(r.Status) != wantStatus {
		t.Errorf("row %s: type=%q region=%q name=%q status=%q; want %q %q %q %q",
			r.NativeID, r.Type, sv(r.Region), sv(r.Name), sv(r.Status), wantType, testRegion, wantName, wantStatus)
	}
	if got, err := time.Parse(time.RFC3339, sv(r.CreatedAt)); err != nil || !got.Equal(iswCreated) {
		t.Errorf("row %s: CreatedAt = %q; want %s", r.NativeID, sv(r.CreatedAt), iswCreated)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[attrKey] == nil {
		t.Errorf("row %s: attributes %s lack %q (err %v)", r.NativeID, r.AttributesJSON, attrKey, err)
	}
}

func TestScanIoTSWWorkspaces_PaginatesAndReturnsParents(t *testing.T) {
	st := newTestStore(t)
	noStatus := iswWorkspace("ws3")
	noStatus.Status = nil
	fake := fakeIoTSW{workspaces: map[string]*iotsitewise.ListWorkspacesOutput{
		"":   {WorkspaceSummaries: []iswtypes.WorkspaceSummary{iswWorkspace("ws1"), {Name: sdkaws.String("no-arn")}}, NextToken: sdkaws.String("p2")},
		"p2": {WorkspaceSummaries: []iswtypes.WorkspaceSummary{iswWorkspace("ws2"), noStatus}},
	}}

	parents, total, _, err := scanIoTSWWorkspaces(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanIoTSWWorkspaces: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (the ARN-less workspace is skipped)", total)
	}
	want := []childParent{{"ws1", iswWorkspaceARN("ws1")}, {"ws2", iswWorkspaceARN("ws2")}, {"ws3", iswWorkspaceARN("ws3")}}
	if fmt.Sprint(parents) != fmt.Sprint(want) {
		t.Errorf("parents = %v; want %v", parents, want)
	}
	rows := qsRows(t, st, TypeIoTSWWorkspace)
	if len(rows) != 3 {
		t.Fatalf("stored %d workspace rows; want 3", len(rows))
	}
	assertISWRow(t, rows[iswWorkspaceARN("ws1")], TypeIoTSWWorkspace, "ws1", "ACTIVE", "Status")
	assertISWRow(t, rows[iswWorkspaceARN("ws2")], TypeIoTSWWorkspace, "ws2", "ACTIVE", "Arn")
	if s := rows[iswWorkspaceARN("ws3")].Status; s != nil {
		t.Errorf("status-less workspace stored Status %q; want unset", *s)
	}
}

func TestScanIoTSWApplications_PaginatesAndLinksListedWorkspaces(t *testing.T) {
	st := newTestStore(t)
	upsertTestResource(t, st, "aws", testAccountID, TypeIoTSWWorkspace, iswWorkspaceARN("ws1"), testRegion, "{}")
	fake := fakeIoTSW{applications: map[string]*iotsitewise.ListApplicationsOutput{
		"":   {Applications: []iswtypes.ApplicationSummary{iswApplication("ws1", "a1"), {Id: sdkaws.String("no-arn")}}, NextToken: sdkaws.String("p2")},
		"p2": {Applications: []iswtypes.ApplicationSummary{iswApplication("ws1", "a2"), iswApplication("unlisted", "a3")}},
	}}
	parents := []childParent{{"ws1", iswWorkspaceARN("ws1")}}

	total, _, err := scanIoTSWApplications(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	if err != nil {
		t.Fatalf("scanIoTSWApplications: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	rows := qsRows(t, st, TypeIoTSWApplication)
	if len(rows) != 3 {
		t.Fatalf("stored %d application rows; want 3", len(rows))
	}
	a1 := iswWorkspaceARN("ws1") + "/application/a1"
	assertISWRow(t, rows[a1], TypeIoTSWApplication, "app-a1", "ACTIVE", "WorkspaceName")
	assertQSContains(t, st, iswWorkspaceARN("ws1"), a1, iswWorkspaceARN("ws1")+"/application/a2")
	a3 := store.ResourceID("aws", testAccountID, iswWorkspaceARN("unlisted")+"/application/a3")
	if rels, err := st.RelationshipsTo(a3, store.RelContains); err != nil || len(rels) != 0 {
		t.Errorf("application in an unlisted workspace has contains parents %v (err %v); want none", rels, err)
	}
}

// TestScanIoTSWAccountOps_ErrorShapes runs the account-level list ops through
// their error ladder: a gap or deny on page 2 keeps page 1's rows.
func TestScanIoTSWAccountOps_ErrorShapes(t *testing.T) {
	ops := []struct {
		name, rowType, op string
		run               func(st *store.Store, page2Err error) error
	}{
		{"workspaces", TypeIoTSWWorkspace, "iotsitewise:ListWorkspaces", func(st *store.Store, page2Err error) error {
			fake := fakeIoTSW{
				workspaces: map[string]*iotsitewise.ListWorkspacesOutput{"": {WorkspaceSummaries: []iswtypes.WorkspaceSummary{iswWorkspace("ws1")}, NextToken: sdkaws.String("p2")}},
				pageErrs:   map[string]error{"p2": page2Err},
			}
			_, _, _, err := scanIoTSWWorkspaces(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID)
			return err
		}},
		{"applications", TypeIoTSWApplication, "iotsitewise:ListApplications", func(st *store.Store, page2Err error) error {
			fake := fakeIoTSW{
				applications: map[string]*iotsitewise.ListApplicationsOutput{"": {Applications: []iswtypes.ApplicationSummary{iswApplication("ws1", "a1")}, NextToken: sdkaws.String("p2")}},
				pageErrs:     map[string]error{"p2": page2Err},
			}
			_, _, err := scanIoTSWApplications(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
			return err
		}},
	}
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantRows  int
		wantWarns int
	}{
		{"no error stores page 1", nil, false, 1, 0},
		{"feature gap is skipped silently", apiErr("InvalidRequestException", "Feature not supported yet"), false, 1, 0},
		{"access denied warns", apiErr("AccessDeniedException", "User: x is not authorized to perform: iotsitewise:List"), false, 1, 1},
		{"other invalid request propagates", apiErr("InvalidRequestException", "bad token"), true, 0, 0},
	}
	for _, op := range ops {
		for _, tc := range cases {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				var warned []string
				st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
				err := op.run(st, tc.err)
				if tc.wantErr {
					if !isAPIErrorCode(err, "InvalidRequestException") {
						t.Fatalf("err = %v; want the InvalidRequestException propagated", err)
					}
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if got := len(qsRows(t, st, op.rowType)); got != tc.wantRows {
					t.Errorf("stored %d rows; want %d", got, tc.wantRows)
				}
				if len(warned) != tc.wantWarns {
					t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
				}
				for _, svc := range warned {
					if svc != op.op {
						t.Errorf("warning names %q; want %q", svc, op.op)
					}
				}
			})
		}
	}
}

func TestScanIoTSWAccountOps_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	parents, total, _, err := scanIoTSWWorkspaces(context.Background(), fakeIoTSW{}, acct, testRegion, st, testScanID)
	if err != nil || total != 0 || len(parents) != 0 {
		t.Errorf("workspaces: parents=%v total=%d err=%v; want none, 0, nil", parents, total, err)
	}
	total, _, err = scanIoTSWApplications(context.Background(), fakeIoTSW{}, acct, testRegion, st, testScanID, nil)
	if err != nil || total != 0 {
		t.Errorf("applications: total=%d err=%v; want 0, nil", total, err)
	}
}

// iswChildOps drives each per-workspace phase; every workspace not in errs
// lists one child on page 1 and one on page 2.
var iswChildOps = []struct {
	name, childType, kind, op, attrKey string
	run                                func(st *store.Store, parents []childParent, errs map[string]error, queried *iswCallLog) error
}{
	{
		"pipelines", TypeIoTSWPipeline, "pipeline", "iotsitewise:ListPipelines", "PipelineArn",
		func(st *store.Store, parents []childParent, errs map[string]error, queried *iswCallLog) error {
			fake := fakeIoTSW{pipelines: map[string]map[string]*iotsitewise.ListPipelinesOutput{}, errs: errs, queried: queried}
			for _, p := range parents {
				fake.pipelines[p.id] = map[string]*iotsitewise.ListPipelinesOutput{
					"":   {PipelineSummaries: []iswtypes.PipelineSummary{iswPipeline(p.id, "c1"), {PipelineName: sdkaws.String("no-arn")}}, NextToken: sdkaws.String("p2")},
					"p2": {PipelineSummaries: []iswtypes.PipelineSummary{iswPipeline(p.id, "c2")}},
				}
			}
			_, _, err := scanIoTSWPipelines(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
			return err
		},
	},
	{
		"tasks", TypeIoTSWTask, "task", "iotsitewise:ListTasks", "TaskArn",
		func(st *store.Store, parents []childParent, errs map[string]error, queried *iswCallLog) error {
			fake := fakeIoTSW{tasks: map[string]map[string]*iotsitewise.ListTasksOutput{}, errs: errs, queried: queried}
			for _, p := range parents {
				fake.tasks[p.id] = map[string]*iotsitewise.ListTasksOutput{
					"":   {TaskSummaries: []iswtypes.TaskSummary{iswTask(p.id, "c1"), {TaskName: sdkaws.String("no-arn")}}, NextToken: sdkaws.String("p2")},
					"p2": {TaskSummaries: []iswtypes.TaskSummary{iswTask(p.id, "c2")}},
				}
			}
			_, _, err := scanIoTSWTasks(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
			return err
		},
	},
}

func seedISWWorkspaces(t *testing.T, st *store.Store, names ...string) []childParent {
	t.Helper()
	parents := make([]childParent, len(names))
	for i, n := range names {
		upsertTestResource(t, st, "aws", testAccountID, TypeIoTSWWorkspace, iswWorkspaceARN(n), testRegion, "{}")
		parents[i] = childParent{id: n, arn: iswWorkspaceARN(n)}
	}
	return parents
}

func TestScanIoTSWWorkspaceChildren_PaginatesEveryWorkspaceAndWiresHierarchy(t *testing.T) {
	for _, op := range iswChildOps {
		t.Run(op.name, func(t *testing.T) {
			st := newTestStore(t)
			parents := seedISWWorkspaces(t, st, "ws1", "ws2")
			var queried iswCallLog
			if err := op.run(st, parents, nil, &queried); err != nil {
				t.Fatalf("run: %v", err)
			}
			rows := qsRows(t, st, op.childType)
			if len(rows) != 4 {
				t.Fatalf("stored %d rows; want 4 (2 pages x 2 workspaces, ARN-less skipped)", len(rows))
			}
			for _, ws := range []string{"ws1", "ws2"} {
				c1 := iswWorkspaceARN(ws) + "/" + op.kind + "/c1"
				assertISWRow(t, rows[c1], op.childType, "c1", "ACTIVE", op.attrKey)
				assertQSContains(t, st, iswWorkspaceARN(ws), c1, iswWorkspaceARN(ws)+"/"+op.kind+"/c2")
			}
			if len(queried.names) != 4 {
				t.Errorf("queried %v; want 2 pages for each of 2 workspaces", queried.names)
			}
		})
	}
}

func TestScanIoTSWWorkspaceChildren_ErrorHandling(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantRows  int
		wantWarns int
	}{
		{"workspace deleted since listing is skipped", apiErr("ResourceNotFoundException", "gone"), false, 4, 0},
		{"feature gap is skipped silently", apiErr("InvalidRequestException", "Feature not supported yet"), false, 4, 0},
		{"access denied warns once and keeps siblings", apiErr("AccessDeniedException", "User: x is not authorized to perform: iotsitewise:List"), false, 4, 1},
		{"other error propagates", apiErr("InternalFailureException", "boom"), true, 0, 0},
	}
	for _, op := range iswChildOps {
		for _, tc := range cases {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				var warned []string
				st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
				parents := seedISWWorkspaces(t, st, "bad1", "bad2", "ok1", "ok2")
				err := op.run(st, parents, map[string]error{"bad1": tc.err, "bad2": tc.err}, nil)
				if tc.wantErr {
					if !isAPIErrorCode(err, "InternalFailureException") {
						t.Fatalf("err = %v; want the InternalFailureException propagated", err)
					}
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if got := len(qsRows(t, st, op.childType)); got != tc.wantRows {
					t.Errorf("stored %d rows; want %d", got, tc.wantRows)
				}
				if len(warned) != tc.wantWarns {
					t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
				}
				for _, svc := range warned {
					if svc != op.op {
						t.Errorf("warning names %q; want %q", svc, op.op)
					}
				}
			})
		}
	}
}

func TestScanIoTSWWorkspaceChildren_NoWorkspacesMakesNoCalls(t *testing.T) {
	for _, op := range iswChildOps {
		t.Run(op.name, func(t *testing.T) {
			st := newTestStore(t)
			var queried iswCallLog
			if err := op.run(st, nil, nil, &queried); err != nil {
				t.Fatalf("run: %v", err)
			}
			if len(queried.names) != 0 || len(qsRows(t, st, op.childType)) != 0 {
				t.Errorf("queried %v and stored rows with no workspaces; want neither", queried.names)
			}
		})
	}
}

// iswServiceStub serves every op scanIoTSiteWise calls: one gateway from the
// pre-existing phases, one workspace with one child of each kind, and
// override replacing an op's queue.
func iswServiceStub(t *testing.T, override map[string][]stubCall) sdkaws.Config {
	t.Helper()
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	calls := map[string][]stubCall{
		"ListAssetModels":       one(&iotsitewise.ListAssetModelsOutput{}),
		"ListComputationModels": one(&iotsitewise.ListComputationModelsOutput{}),
		"ListGateways": one(&iotsitewise.ListGatewaysOutput{GatewaySummaries: []iswtypes.GatewaySummary{
			{GatewayId: sdkaws.String("gw"), GatewayName: sdkaws.String("gateway")},
		}}),
		"ListDatasets":     one(&iotsitewise.ListDatasetsOutput{}),
		"ListPortals":      one(&iotsitewise.ListPortalsOutput{}),
		"ListWorkspaces":   one(&iotsitewise.ListWorkspacesOutput{WorkspaceSummaries: []iswtypes.WorkspaceSummary{iswWorkspace("ws")}}),
		"ListPipelines":    one(&iotsitewise.ListPipelinesOutput{PipelineSummaries: []iswtypes.PipelineSummary{iswPipeline("ws", "pl")}}),
		"ListTasks":        one(&iotsitewise.ListTasksOutput{TaskSummaries: []iswtypes.TaskSummary{iswTask("ws", "tk")}}),
		"ListApplications": one(&iotsitewise.ListApplicationsOutput{Applications: []iswtypes.ApplicationSummary{iswApplication("ws", "ap")}}),
	}
	for op, c := range override {
		calls[op] = c
	}
	return cloud9CfgWithStub(stubResponses(t, calls), testRegion)
}

// TestScanIoTSiteWise_WorkspaceFamilyAfterExistingPhases runs the whole service
// against the real SDK client (its input validators included): the pre-existing
// phases still store their rows, and the workspace family's children are
// contained by the workspace.
func TestScanIoTSiteWise_WorkspaceFamilyAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = iswServiceStub(t, nil)
	total, _, err := scanIoTSiteWise(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanIoTSiteWise: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d; want 5 (gateway, workspace, application, pipeline, task)", total)
	}
	gw := iotSWARN(testRegion, testAccountID, "gateway", "gw")
	if _, ok := qsRows(t, st, TypeIoTSWGateway)[gw]; !ok {
		t.Errorf("pre-existing gateway phase did not store %s", gw)
	}
	ws := iswWorkspaceARN("ws")
	assertQSContains(t, st, ws, ws+"/pipeline/pl", ws+"/task/tk", ws+"/application/ap")
}

// TestScanIoTSiteWise_WorkspaceChildErrorPropagates pins that a per-workspace
// op's hard error reaches the service result, stops the later task phase, and
// cannot cost the account-level applications listed before it.
func TestScanIoTSiteWise_WorkspaceChildErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = iswServiceStub(t, map[string][]stubCall{
		"ListPipelines": {{Err: apiErr("InternalFailureException", "boom")}},
	})
	_, _, err := scanIoTSiteWise(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InternalFailureException") {
		t.Fatalf("err = %v; want the ListPipelines InternalFailureException", err)
	}
	if got := len(qsRows(t, st, TypeIoTSWApplication)); got != 1 {
		t.Errorf("stored %d applications; want 1 (listed before the failing pipelines phase)", got)
	}
	if got := len(qsRows(t, st, TypeIoTSWTask)); got != 0 {
		t.Errorf("stored %d tasks; want 0 (the task phase follows the failing one)", got)
	}
}
