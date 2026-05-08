package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerNotebookInstance},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerNotebookInstanceLifecycleConfig, Leaf: true},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerCodeRepository, Leaf: true},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerProcessingJob},
	)
}

// sagemakerTrainingAPI is the narrow surface used by the Training/Notebook
// family. Each phase List+fan-out Describe so attrs carry the full Describe
// body (NetworkInterfaceId, KmsKeyId, RoleArn, GitConfig, ProcessingResources).
type sagemakerTrainingAPI interface {
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
func scanSageMakerTraining(ctx context.Context, client sagemakerTrainingAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func(context.Context, sagemakerTrainingAPI, *account, string, *store.Store, string) (int, int, error){
		scanSageMakerNotebookInstances,
		scanSageMakerNotebookInstanceLifecycleConfigs,
		scanSageMakerCodeRepositories,
		scanSageMakerProcessingJobs,
	} {
		t, i, ferr := phase(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanSageMakerNotebookInstances lists notebook instances then fans out
// DescribeNotebookInstance for full body (RoleArn, KmsKeyId, NetworkInterfaceId,
// SubnetId, SecurityGroups, DefaultCodeRepository).
func scanSageMakerNotebookInstances(ctx context.Context, client sagemakerTrainingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListNotebookInstancesPaginator(client, &sagemaker.ListNotebookInstancesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListNotebookInstances", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListNotebookInstances: %w", perr)
		}
		for _, n := range out.NotebookInstances {
			if n.NotebookInstanceName != nil {
				names = append(names, *n.NotebookInstanceName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeNotebookInstance(gctx, &sagemaker.DescribeNotebookInstanceInput{NotebookInstanceName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeNotebookInstance %s: %w", name, derr)
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
	}, st, "sagemaker notebook instances")
}

// scanSageMakerNotebookInstanceLifecycleConfigs lists lifecycle configs then
// fans out DescribeNotebookInstanceLifecycleConfig for full body (OnCreate,
// OnStart script content).
func scanSageMakerNotebookInstanceLifecycleConfigs(ctx context.Context, client sagemakerTrainingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListNotebookInstanceLifecycleConfigsPaginator(client, &sagemaker.ListNotebookInstanceLifecycleConfigsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListNotebookInstanceLifecycleConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListNotebookInstanceLifecycleConfigs: %w", perr)
		}
		for _, c := range out.NotebookInstanceLifecycleConfigs {
			if c.NotebookInstanceLifecycleConfigName != nil {
				names = append(names, *c.NotebookInstanceLifecycleConfigName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeNotebookInstanceLifecycleConfig(gctx, &sagemaker.DescribeNotebookInstanceLifecycleConfigInput{NotebookInstanceLifecycleConfigName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeNotebookInstanceLifecycleConfig %s: %w", name, derr)
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
	}, st, "sagemaker notebook lifecycle configs")
}

// scanSageMakerCodeRepositories lists git code repositories then fans out
// DescribeCodeRepository for full body (GitConfig.RepositoryUrl, SecretArn).
func scanSageMakerCodeRepositories(ctx context.Context, client sagemakerTrainingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListCodeRepositoriesPaginator(client, &sagemaker.ListCodeRepositoriesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListCodeRepositories", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListCodeRepositories: %w", perr)
		}
		for _, c := range out.CodeRepositorySummaryList {
			if c.CodeRepositoryName != nil {
				names = append(names, *c.CodeRepositoryName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeCodeRepository(gctx, &sagemaker.DescribeCodeRepositoryInput{CodeRepositoryName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeCodeRepository %s: %w", name, derr)
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
	}, st, "sagemaker code repositories")
}

// scanSageMakerProcessingJobs lists processing jobs then fans out
// DescribeProcessingJob for full body (ProcessingResources, NetworkConfig,
// AppSpecification, RoleArn, ProcessingInputs/Outputs).
func scanSageMakerProcessingJobs(ctx context.Context, client sagemakerTrainingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListProcessingJobsPaginator(client, &sagemaker.ListProcessingJobsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListProcessingJobs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListProcessingJobs: %w", perr)
		}
		for _, j := range out.ProcessingJobSummaries {
			if j.ProcessingJobName != nil {
				names = append(names, *j.ProcessingJobName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeProcessingJob(gctx, &sagemaker.DescribeProcessingJobInput{ProcessingJobName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeProcessingJob %s: %w", name, derr)
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
	}, st, "sagemaker processing jobs")
}

// sagemakerDescribeFanout runs build per name concurrently with the given
// semaphore weight, collects non-nil results, and upserts as a single batch.
// Skips empty input. Wraps the upsert error with the supplied label.
func sagemakerDescribeFanout(
	ctx context.Context,
	names []string,
	weight int64,
	build func(context.Context, string) (*store.Resource, error),
	st *store.Store,
	label string,
) (int, int, error) {
	if len(names) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(weight)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			r, err := build(gctx, n)
			if err != nil {
				return err
			}
			if r == nil {
				return nil
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert %s: %w", label, uerr)
	}
	return len(batch), n, nil
}
