package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/store"
)

type stubSageMakerTraining struct {
	notebooks     []smtypes.NotebookInstanceSummary
	notebookOut   map[string]*sagemaker.DescribeNotebookInstanceOutput
	lifecycles    []smtypes.NotebookInstanceLifecycleConfigSummary
	lifecyclesOut map[string]*sagemaker.DescribeNotebookInstanceLifecycleConfigOutput
	repos         []smtypes.CodeRepositorySummary
	repoOut       map[string]*sagemaker.DescribeCodeRepositoryOutput
	procJobs      []smtypes.ProcessingJobSummary
	procJobOut    map[string]*sagemaker.DescribeProcessingJobOutput
}

func (s *stubSageMakerTraining) ListNotebookInstances(_ context.Context, _ *sagemaker.ListNotebookInstancesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListNotebookInstancesOutput, error) {
	return &sagemaker.ListNotebookInstancesOutput{NotebookInstances: s.notebooks}, nil
}

func (s *stubSageMakerTraining) DescribeNotebookInstance(_ context.Context, in *sagemaker.DescribeNotebookInstanceInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeNotebookInstanceOutput, error) {
	return s.notebookOut[*in.NotebookInstanceName], nil
}

func (s *stubSageMakerTraining) ListNotebookInstanceLifecycleConfigs(_ context.Context, _ *sagemaker.ListNotebookInstanceLifecycleConfigsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListNotebookInstanceLifecycleConfigsOutput, error) {
	return &sagemaker.ListNotebookInstanceLifecycleConfigsOutput{NotebookInstanceLifecycleConfigs: s.lifecycles}, nil
}

func (s *stubSageMakerTraining) DescribeNotebookInstanceLifecycleConfig(_ context.Context, in *sagemaker.DescribeNotebookInstanceLifecycleConfigInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeNotebookInstanceLifecycleConfigOutput, error) {
	return s.lifecyclesOut[*in.NotebookInstanceLifecycleConfigName], nil
}

func (s *stubSageMakerTraining) ListCodeRepositories(_ context.Context, _ *sagemaker.ListCodeRepositoriesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListCodeRepositoriesOutput, error) {
	return &sagemaker.ListCodeRepositoriesOutput{CodeRepositorySummaryList: s.repos}, nil
}

func (s *stubSageMakerTraining) DescribeCodeRepository(_ context.Context, in *sagemaker.DescribeCodeRepositoryInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeCodeRepositoryOutput, error) {
	return s.repoOut[*in.CodeRepositoryName], nil
}

func (s *stubSageMakerTraining) ListProcessingJobs(_ context.Context, _ *sagemaker.ListProcessingJobsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListProcessingJobsOutput, error) {
	return &sagemaker.ListProcessingJobsOutput{ProcessingJobSummaries: s.procJobs}, nil
}

func (s *stubSageMakerTraining) DescribeProcessingJob(_ context.Context, in *sagemaker.DescribeProcessingJobInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeProcessingJobOutput, error) {
	return s.procJobOut[*in.ProcessingJobName], nil
}

func TestScanSageMakerTraining(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	nbName := "nb-1"
	nbARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:notebook-instance/%s", testRegion, acct.ID, nbName)
	lcName := "lc-1"
	lcARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:notebook-instance-lifecycle-config/%s", testRegion, acct.ID, lcName)
	repoName := "repo-1"
	repoARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:code-repository/%s", testRegion, acct.ID, repoName)
	pjName := "job-1"
	pjARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:processing-job/%s", testRegion, acct.ID, pjName)

	stub := &stubSageMakerTraining{
		notebooks: []smtypes.NotebookInstanceSummary{{NotebookInstanceArn: &nbARN, NotebookInstanceName: &nbName, CreationTime: &now}},
		notebookOut: map[string]*sagemaker.DescribeNotebookInstanceOutput{
			nbName: {NotebookInstanceArn: &nbARN, NotebookInstanceName: &nbName, NotebookInstanceStatus: smtypes.NotebookInstanceStatusInService, CreationTime: &now},
		},
		lifecycles: []smtypes.NotebookInstanceLifecycleConfigSummary{{NotebookInstanceLifecycleConfigArn: &lcARN, NotebookInstanceLifecycleConfigName: &lcName, CreationTime: &now}},
		lifecyclesOut: map[string]*sagemaker.DescribeNotebookInstanceLifecycleConfigOutput{
			lcName: {NotebookInstanceLifecycleConfigArn: &lcARN, NotebookInstanceLifecycleConfigName: &lcName, CreationTime: &now},
		},
		repos: []smtypes.CodeRepositorySummary{{CodeRepositoryArn: &repoARN, CodeRepositoryName: &repoName, CreationTime: &now}},
		repoOut: map[string]*sagemaker.DescribeCodeRepositoryOutput{
			repoName: {CodeRepositoryArn: &repoARN, CodeRepositoryName: &repoName, CreationTime: &now},
		},
		procJobs: []smtypes.ProcessingJobSummary{{ProcessingJobArn: &pjARN, ProcessingJobName: &pjName, ProcessingJobStatus: smtypes.ProcessingJobStatusCompleted, CreationTime: &now}},
		procJobOut: map[string]*sagemaker.DescribeProcessingJobOutput{
			pjName: {ProcessingJobArn: &pjARN, ProcessingJobName: &pjName, ProcessingJobStatus: smtypes.ProcessingJobStatusCompleted, CreationTime: &now},
		},
	}

	total, inserted, err := scanSageMakerTraining(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Fatalf("total=%d inserted=%d want 4/4", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeSageMakerNotebookInstance, nbARN},
		{TypeSageMakerNotebookInstanceLifecycleConfig, lcARN},
		{TypeSageMakerCodeRepository, repoARN},
		{TypeSageMakerProcessingJob, pjARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanSageMakerTrainingEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubSageMakerTraining{}
	total, inserted, err := scanSageMakerTraining(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
