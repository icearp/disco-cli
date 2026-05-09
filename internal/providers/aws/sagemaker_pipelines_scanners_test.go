package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
)

type stubSageMakerPipelines struct {
	pipelines   []smtypes.PipelineSummary
	pipelineOut map[string]*sagemaker.DescribePipelineOutput
	projects    []smtypes.ProjectSummary
	projectOut  map[string]*sagemaker.DescribeProjectOutput
	partnerApps []smtypes.PartnerAppSummary
	partnerOut  map[string]*sagemaker.DescribePartnerAppOutput
}

func (s *stubSageMakerPipelines) ListPipelines(_ context.Context, _ *sagemaker.ListPipelinesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListPipelinesOutput, error) {
	return &sagemaker.ListPipelinesOutput{PipelineSummaries: s.pipelines}, nil
}

func (s *stubSageMakerPipelines) DescribePipeline(_ context.Context, in *sagemaker.DescribePipelineInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribePipelineOutput, error) {
	return s.pipelineOut[*in.PipelineName], nil
}

func (s *stubSageMakerPipelines) ListProjects(_ context.Context, _ *sagemaker.ListProjectsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListProjectsOutput, error) {
	return &sagemaker.ListProjectsOutput{ProjectSummaryList: s.projects}, nil
}

func (s *stubSageMakerPipelines) DescribeProject(_ context.Context, in *sagemaker.DescribeProjectInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeProjectOutput, error) {
	return s.projectOut[*in.ProjectName], nil
}

func (s *stubSageMakerPipelines) ListPartnerApps(_ context.Context, _ *sagemaker.ListPartnerAppsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListPartnerAppsOutput, error) {
	return &sagemaker.ListPartnerAppsOutput{Summaries: s.partnerApps}, nil
}

func (s *stubSageMakerPipelines) DescribePartnerApp(_ context.Context, in *sagemaker.DescribePartnerAppInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribePartnerAppOutput, error) {
	return s.partnerOut[*in.Arn], nil
}

func TestScanSageMakerPipelines(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	plName := "pl-1"
	plARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:pipeline/%s", testRegion, acct.ID, plName)
	prjName := "prj-1"
	prjARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:project/%s", testRegion, acct.ID, prjName)
	paName := "pa-1"
	paARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:partner-app/%s", testRegion, acct.ID, paName)

	stub := &stubSageMakerPipelines{
		pipelines: []smtypes.PipelineSummary{{PipelineArn: &plARN, PipelineName: &plName, CreationTime: &now}},
		pipelineOut: map[string]*sagemaker.DescribePipelineOutput{
			plName: {PipelineArn: &plARN, PipelineName: &plName, PipelineStatus: smtypes.PipelineStatusActive, CreationTime: &now},
		},
		projects: []smtypes.ProjectSummary{{ProjectArn: &prjARN, ProjectName: &prjName, CreationTime: &now}},
		projectOut: map[string]*sagemaker.DescribeProjectOutput{
			prjName: {ProjectArn: &prjARN, ProjectName: &prjName, ProjectStatus: smtypes.ProjectStatusCreateCompleted, CreationTime: &now},
		},
		partnerApps: []smtypes.PartnerAppSummary{{Arn: &paARN, Name: &paName, CreationTime: &now}},
		partnerOut: map[string]*sagemaker.DescribePartnerAppOutput{
			paARN: {Arn: &paARN, Name: &paName, Status: smtypes.PartnerAppStatusAvailable, CreationTime: &now},
		},
	}

	total, inserted, err := scanSageMakerPipelines(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Fatalf("total=%d inserted=%d want 3/3", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeSageMakerPipeline, plARN},
		{TypeSageMakerProject, prjARN},
		{TypeSageMakerPartnerApp, paARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.typ, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanSageMakerPipelinesEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubSageMakerPipelines{}
	total, inserted, err := scanSageMakerPipelines(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
