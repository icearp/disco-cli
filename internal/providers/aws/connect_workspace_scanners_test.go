package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
)

type stubConnectWorkspace struct {
	tasks          []cttypes.TaskTemplateMetadata
	forms          []cttypes.EvaluationFormSummary
	formOut        map[string]*connect.DescribeEvaluationFormOutput
	views          []cttypes.ViewSummary
	viewOut        map[string]*connect.DescribeViewOutput
	versionsByView map[string][]cttypes.ViewVersionSummary
	workspaces     []cttypes.WorkspaceSummary
	workspaceOut   map[string]*connect.DescribeWorkspaceOutput
	prompts        []cttypes.PromptSummary
	promptOut      map[string]*connect.DescribePromptOutput
}

func (s *stubConnectWorkspace) ListTaskTemplates(_ context.Context, _ *connect.ListTaskTemplatesInput, _ ...func(*connect.Options)) (*connect.ListTaskTemplatesOutput, error) {
	return &connect.ListTaskTemplatesOutput{TaskTemplates: s.tasks}, nil
}

func (s *stubConnectWorkspace) ListEvaluationForms(_ context.Context, _ *connect.ListEvaluationFormsInput, _ ...func(*connect.Options)) (*connect.ListEvaluationFormsOutput, error) {
	return &connect.ListEvaluationFormsOutput{EvaluationFormSummaryList: s.forms}, nil
}

func (s *stubConnectWorkspace) DescribeEvaluationForm(_ context.Context, in *connect.DescribeEvaluationFormInput, _ ...func(*connect.Options)) (*connect.DescribeEvaluationFormOutput, error) {
	return s.formOut[*in.EvaluationFormId], nil
}

func (s *stubConnectWorkspace) ListViews(_ context.Context, _ *connect.ListViewsInput, _ ...func(*connect.Options)) (*connect.ListViewsOutput, error) {
	return &connect.ListViewsOutput{ViewsSummaryList: s.views}, nil
}

func (s *stubConnectWorkspace) DescribeView(_ context.Context, in *connect.DescribeViewInput, _ ...func(*connect.Options)) (*connect.DescribeViewOutput, error) {
	return s.viewOut[*in.ViewId], nil
}

func (s *stubConnectWorkspace) ListViewVersions(_ context.Context, in *connect.ListViewVersionsInput, _ ...func(*connect.Options)) (*connect.ListViewVersionsOutput, error) {
	return &connect.ListViewVersionsOutput{ViewVersionSummaryList: s.versionsByView[*in.ViewId]}, nil
}

func (s *stubConnectWorkspace) ListWorkspaces(_ context.Context, _ *connect.ListWorkspacesInput, _ ...func(*connect.Options)) (*connect.ListWorkspacesOutput, error) {
	return &connect.ListWorkspacesOutput{WorkspaceSummaryList: s.workspaces}, nil
}

func (s *stubConnectWorkspace) DescribeWorkspace(_ context.Context, in *connect.DescribeWorkspaceInput, _ ...func(*connect.Options)) (*connect.DescribeWorkspaceOutput, error) {
	return s.workspaceOut[*in.WorkspaceId], nil
}

func (s *stubConnectWorkspace) ListPrompts(_ context.Context, _ *connect.ListPromptsInput, _ ...func(*connect.Options)) (*connect.ListPromptsOutput, error) {
	return &connect.ListPromptsOutput{PromptSummaryList: s.prompts}, nil
}

func (s *stubConnectWorkspace) DescribePrompt(_ context.Context, in *connect.DescribePromptInput, _ ...func(*connect.Options)) (*connect.DescribePromptOutput, error) {
	return s.promptOut[*in.PromptId], nil
}

func TestScanConnectWorkspace(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	instID := "11111111-1111-1111-1111-111111111111"
	instARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s", testRegion, acct.ID, instID)
	instances := []cttypes.InstanceSummary{{Id: &instID, Arn: &instARN}}

	tName := "task"
	tARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/task-template/t-1", testRegion, acct.ID, instID)
	fID := "f-1"
	fARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/evaluation-form/%s", testRegion, acct.ID, instID, fID)
	fTitle := "form"
	vID := "v-1"
	vARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/view/%s", testRegion, acct.ID, instID, vID)
	vName := "view"
	vvARN := vARN + ":1"
	wID := "w-1"
	wARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/workspace/%s", testRegion, acct.ID, instID, wID)
	wName := "ws"
	pID := "p-1"
	pARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/prompt/%s", testRegion, acct.ID, instID, pID)
	pName := "p"

	stub := &stubConnectWorkspace{
		tasks: []cttypes.TaskTemplateMetadata{{Arn: &tARN, Name: &tName, CreatedTime: &now}},
		forms: []cttypes.EvaluationFormSummary{{EvaluationFormArn: &fARN, EvaluationFormId: &fID, Title: &fTitle}},
		formOut: map[string]*connect.DescribeEvaluationFormOutput{
			fID: {EvaluationForm: &cttypes.EvaluationForm{EvaluationFormArn: &fARN, EvaluationFormId: &fID, Title: &fTitle}},
		},
		views: []cttypes.ViewSummary{{Arn: &vARN, Id: &vID, Name: &vName}},
		viewOut: map[string]*connect.DescribeViewOutput{
			vID: {View: &cttypes.View{Arn: &vARN, Id: &vID, Name: &vName, Status: cttypes.ViewStatusPublished}},
		},
		versionsByView: map[string][]cttypes.ViewVersionSummary{
			vID: {{Arn: &vvARN, Id: &vID, Name: &vName}},
		},
		workspaces: []cttypes.WorkspaceSummary{{Arn: &wARN, Id: &wID}},
		workspaceOut: map[string]*connect.DescribeWorkspaceOutput{
			wID: {Workspace: &cttypes.Workspace{Arn: &wARN, Id: &wID, Name: &wName}},
		},
		prompts: []cttypes.PromptSummary{{Arn: &pARN, Id: &pID}},
		promptOut: map[string]*connect.DescribePromptOutput{
			pID: {Prompt: &cttypes.Prompt{PromptARN: &pARN, PromptId: &pID, Name: &pName}},
		},
	}

	total, inserted, err := scanConnectWorkspace(context.Background(), stub, instances, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 6 || inserted != 6 {
		t.Fatalf("total=%d inserted=%d want 6/6", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeConnectTaskTemplate, tARN},
		{TypeConnectEvaluationForm, fARN},
		{TypeConnectView, vARN},
		{TypeConnectViewVersion, vvARN},
		{TypeConnectWorkspace, wARN},
		{TypeConnectPrompt, pARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanConnectWorkspaceEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubConnectWorkspace{}
	total, inserted, err := scanConnectWorkspace(context.Background(), stub, nil, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
