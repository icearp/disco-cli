package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSageMakerNotebookInstance, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerNotebookInstanceLifecycleConfig, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerCodeRepository, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerProcessingJob, Service: "sagemaker"})
}

// sagemakerTrainingAPI is the narrow surface for the Training/Notebook
// family. Each phase List+fan-out Describe so attrs carry the full Describe
// body (NetworkInterfaceId, KmsKeyId, RoleArn, GitConfig, ProcessingResources).
type sagemakerTrainingAPI interface {
	sagemakerTagsAPI
	ListNotebookInstances(context.Context, *sagemaker.ListNotebookInstancesInput, ...func(*sagemaker.Options)) (*sagemaker.ListNotebookInstancesOutput, error)
	DescribeNotebookInstance(context.Context, *sagemaker.DescribeNotebookInstanceInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeNotebookInstanceOutput, error)
	ListNotebookInstanceLifecycleConfigs(context.Context, *sagemaker.ListNotebookInstanceLifecycleConfigsInput, ...func(*sagemaker.Options)) (*sagemaker.ListNotebookInstanceLifecycleConfigsOutput, error)
	DescribeNotebookInstanceLifecycleConfig(context.Context, *sagemaker.DescribeNotebookInstanceLifecycleConfigInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeNotebookInstanceLifecycleConfigOutput, error)
	ListCodeRepositories(context.Context, *sagemaker.ListCodeRepositoriesInput, ...func(*sagemaker.Options)) (*sagemaker.ListCodeRepositoriesOutput, error)
	DescribeCodeRepository(context.Context, *sagemaker.DescribeCodeRepositoryInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeCodeRepositoryOutput, error)
	ListProcessingJobs(context.Context, *sagemaker.ListProcessingJobsInput, ...func(*sagemaker.Options)) (*sagemaker.ListProcessingJobsOutput, error)
	DescribeProcessingJob(context.Context, *sagemaker.DescribeProcessingJobInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeProcessingJobOutput, error)
}

// scanSageMakerTraining runs all Training/Notebook phases for one region:
// notebook instances, their lifecycle configs, code repositories, and
// processing jobs.
func scanSageMakerTraining(ctx context.Context, client sagemakerTrainingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return runSageMakerPhases(
		sagemakerPhase{"sagemaker:ListNotebookInstances", func() (int, int, error) { return scanSageMakerNotebookInstances(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListNotebookInstanceLifecycleConfigs", func() (int, int, error) {
			return scanSageMakerNotebookInstanceLifecycleConfigs(ctx, client, acct, region, st, scanID)
		}},
		sagemakerPhase{"sagemaker:ListCodeRepositories", func() (int, int, error) { return scanSageMakerCodeRepositories(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListProcessingJobs", func() (int, int, error) { return scanSageMakerProcessingJobs(ctx, client, acct, region, st, scanID) }},
	)
}

// scanSageMakerNotebookInstances lists notebook instances then fans out
// DescribeNotebookInstance for full body (RoleArn, KmsKeyId, NetworkInterfaceId,
// SubnetId, SecurityGroups, DefaultCodeRepository).
func scanSageMakerNotebookInstances(ctx context.Context, client sagemakerTrainingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.NotebookInstanceSummary]{
		listOp: "sagemaker:ListNotebookInstances", describeOp: "sagemaker:DescribeNotebookInstance",
		list: func(ctx context.Context, token *string) ([]smtypes.NotebookInstanceSummary, *string, error) {
			out, err := client.ListNotebookInstances(ctx, &sagemaker.ListNotebookInstancesInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.NotebookInstances, out.NextToken, nil
		},
		name: func(s smtypes.NotebookInstanceSummary) string { return sv(s.NotebookInstanceName) },
		listed: func(s smtypes.NotebookInstanceSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerNotebookInstance, s.NotebookInstanceArn, s.NotebookInstanceName, s.CreationTime, string(s.NotebookInstanceStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.NotebookInstanceSummary) (*store.Resource, error) {
			name := sv(s.NotebookInstanceName)
			out, err := client.DescribeNotebookInstance(ctx, &sagemaker.DescribeNotebookInstanceInput{NotebookInstanceName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.NotebookInstanceArn)
			if arn == "" {
				return nil, nil
			}
			instName := sv(out.NotebookInstanceName)
			status := string(out.NotebookInstanceStatus)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerNotebookInstance,
				NativeID:       arn,
				Name:           &instName,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListNotebookInstances"))
}

// scanSageMakerNotebookInstanceLifecycleConfigs lists lifecycle configs then
// fans out DescribeNotebookInstanceLifecycleConfig for full body (OnCreate,
// OnStart script content).
func scanSageMakerNotebookInstanceLifecycleConfigs(ctx context.Context, client sagemakerTrainingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.NotebookInstanceLifecycleConfigSummary]{
		listOp: "sagemaker:ListNotebookInstanceLifecycleConfigs", describeOp: "sagemaker:DescribeNotebookInstanceLifecycleConfig",
		list: func(ctx context.Context, token *string) ([]smtypes.NotebookInstanceLifecycleConfigSummary, *string, error) {
			out, err := client.ListNotebookInstanceLifecycleConfigs(ctx, &sagemaker.ListNotebookInstanceLifecycleConfigsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.NotebookInstanceLifecycleConfigs, out.NextToken, nil
		},
		name: func(s smtypes.NotebookInstanceLifecycleConfigSummary) string {
			return sv(s.NotebookInstanceLifecycleConfigName)
		},
		listed: func(s smtypes.NotebookInstanceLifecycleConfigSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerNotebookInstanceLifecycleConfig, s.NotebookInstanceLifecycleConfigArn, s.NotebookInstanceLifecycleConfigName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.NotebookInstanceLifecycleConfigSummary) (*store.Resource, error) {
			name := sv(s.NotebookInstanceLifecycleConfigName)
			out, err := client.DescribeNotebookInstanceLifecycleConfig(ctx, &sagemaker.DescribeNotebookInstanceLifecycleConfigInput{NotebookInstanceLifecycleConfigName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.NotebookInstanceLifecycleConfigArn)
			if arn == "" {
				return nil, nil
			}
			cfgName := sv(out.NotebookInstanceLifecycleConfigName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerNotebookInstanceLifecycleConfig,
				NativeID:       arn,
				Name:           &cfgName,
				Region:         &region,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListNotebookInstanceLifecycleConfigs"))
}

// scanSageMakerCodeRepositories lists git code repositories then fans out
// DescribeCodeRepository for full body (GitConfig.RepositoryUrl, SecretArn).
func scanSageMakerCodeRepositories(ctx context.Context, client sagemakerTrainingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.CodeRepositorySummary]{
		listOp: "sagemaker:ListCodeRepositories", describeOp: "sagemaker:DescribeCodeRepository",
		list: func(ctx context.Context, token *string) ([]smtypes.CodeRepositorySummary, *string, error) {
			out, err := client.ListCodeRepositories(ctx, &sagemaker.ListCodeRepositoriesInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.CodeRepositorySummaryList, out.NextToken, nil
		},
		name: func(s smtypes.CodeRepositorySummary) string { return sv(s.CodeRepositoryName) },
		listed: func(s smtypes.CodeRepositorySummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerCodeRepository, s.CodeRepositoryArn, s.CodeRepositoryName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.CodeRepositorySummary) (*store.Resource, error) {
			name := sv(s.CodeRepositoryName)
			out, err := client.DescribeCodeRepository(ctx, &sagemaker.DescribeCodeRepositoryInput{CodeRepositoryName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.CodeRepositoryArn)
			if arn == "" {
				return nil, nil
			}
			repoName := sv(out.CodeRepositoryName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerCodeRepository,
				NativeID:       arn,
				Name:           &repoName,
				Region:         &region,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListCodeRepositories"))
}

// scanSageMakerProcessingJobs lists processing jobs then fans out
// DescribeProcessingJob for full body (ProcessingResources, NetworkConfig,
// AppSpecification, RoleArn, ProcessingInputs/Outputs).
func scanSageMakerProcessingJobs(ctx context.Context, client sagemakerTrainingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.ProcessingJobSummary]{
		listOp: "sagemaker:ListProcessingJobs", describeOp: "sagemaker:DescribeProcessingJob", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.ProcessingJobSummary, *string, error) {
			out, err := client.ListProcessingJobs(ctx, &sagemaker.ListProcessingJobsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ProcessingJobSummaries, out.NextToken, nil
		},
		name: func(s smtypes.ProcessingJobSummary) string { return sv(s.ProcessingJobName) },
		listed: func(s smtypes.ProcessingJobSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerProcessingJob, s.ProcessingJobArn, s.ProcessingJobName, s.CreationTime, string(s.ProcessingJobStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.ProcessingJobSummary) (*store.Resource, error) {
			name := sv(s.ProcessingJobName)
			out, err := client.DescribeProcessingJob(ctx, &sagemaker.DescribeProcessingJobInput{ProcessingJobName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.ProcessingJobArn)
			if arn == "" {
				return nil, nil
			}
			jobName := sv(out.ProcessingJobName)
			status := string(out.ProcessingJobStatus)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerProcessingJob,
				NativeID:       arn,
				Name:           &jobName,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListProcessingJobs"))
}
