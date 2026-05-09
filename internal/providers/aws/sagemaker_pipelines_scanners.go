package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerPipeline},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerProject},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerPartnerApp},
	)
}

// sagemakerPipelinesAPI is the narrow surface used by the Pipelines family.
type sagemakerPipelinesAPI interface {
	ListPipelines(context.Context, *sagemaker.ListPipelinesInput, ...func(*sagemaker.Options)) (*sagemaker.ListPipelinesOutput, error)
	DescribePipeline(context.Context, *sagemaker.DescribePipelineInput, ...func(*sagemaker.Options)) (*sagemaker.DescribePipelineOutput, error)
	ListProjects(context.Context, *sagemaker.ListProjectsInput, ...func(*sagemaker.Options)) (*sagemaker.ListProjectsOutput, error)
	DescribeProject(context.Context, *sagemaker.DescribeProjectInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeProjectOutput, error)
	ListPartnerApps(context.Context, *sagemaker.ListPartnerAppsInput, ...func(*sagemaker.Options)) (*sagemaker.ListPartnerAppsOutput, error)
	DescribePartnerApp(context.Context, *sagemaker.DescribePartnerAppInput, ...func(*sagemaker.Options)) (*sagemaker.DescribePartnerAppOutput, error)
}

// scanSageMakerPipelines runs all Pipelines family phases for one region:
// pipelines, projects, partner apps.
func scanSageMakerPipelines(ctx context.Context, client sagemakerPipelinesAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func(context.Context, sagemakerPipelinesAPI, *account, string, *store.Store, string) (int, int, error){
		scanSageMakerPipelinesList,
		scanSageMakerProjects,
		scanSageMakerPartnerApps,
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

func scanSageMakerPipelinesList(ctx context.Context, client sagemakerPipelinesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListPipelinesPaginator(client, &sagemaker.ListPipelinesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListPipelines", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListPipelines: %w", perr)
		}
		for _, p := range out.PipelineSummaries {
			if p.PipelineName != nil {
				names = append(names, *p.PipelineName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribePipeline(gctx, &sagemaker.DescribePipelineInput{PipelineName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribePipeline %s: %w", name, derr)
		}
		arn := sv(out.PipelineArn)
		if arn == "" {
			return nil, nil
		}
		pname := sv(out.PipelineName)
		status := string(out.PipelineStatus)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerPipeline,
			NativeID:       arn,
			Name:           &pname,
			Region:         &region,
			Status:         &status,
			CreatedAt:      tp(out.CreationTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker pipelines")
}

func scanSageMakerProjects(ctx context.Context, client sagemakerPipelinesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListProjectsPaginator(client, &sagemaker.ListProjectsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListProjects", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListProjects: %w", perr)
		}
		for _, p := range out.ProjectSummaryList {
			if p.ProjectName != nil {
				names = append(names, *p.ProjectName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeProject(gctx, &sagemaker.DescribeProjectInput{ProjectName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeProject %s: %w", name, derr)
		}
		arn := sv(out.ProjectArn)
		if arn == "" {
			return nil, nil
		}
		pname := sv(out.ProjectName)
		status := string(out.ProjectStatus)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerProject,
			NativeID:       arn,
			Name:           &pname,
			Region:         &region,
			Status:         &status,
			CreatedAt:      tp(out.CreationTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker projects")
}

// scanSageMakerPartnerApps fans out by ARN — DescribePartnerApp's input
// field is Arn, not Name (unlike most other SageMaker resources).
func scanSageMakerPartnerApps(ctx context.Context, client sagemakerPipelinesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListPartnerAppsPaginator(client, &sagemaker.ListPartnerAppsInput{})
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListPartnerApps", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListPartnerApps: %w", perr)
		}
		for _, p := range out.Summaries {
			if p.Arn != nil {
				arns = append(arns, *p.Arn)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, arns, fanoutMed, func(gctx context.Context, arn string) (*store.Resource, error) {
		out, derr := client.DescribePartnerApp(gctx, &sagemaker.DescribePartnerAppInput{Arn: &arn})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribePartnerApp %s: %w", arn, derr)
		}
		outARN := sv(out.Arn)
		if outARN == "" {
			return nil, nil
		}
		pname := sv(out.Name)
		status := string(out.Status)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerPartnerApp,
			NativeID:       outARN,
			Name:           &pname,
			Region:         &region,
			Status:         &status,
			CreatedAt:      tp(out.CreationTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker partner apps")
}
