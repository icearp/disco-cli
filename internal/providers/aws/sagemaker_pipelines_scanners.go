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
	registerType(restype.Descriptor{Type: TypeSageMakerPipeline, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerProject, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerPartnerApp, Service: "sagemaker"})
}

// sagemakerPipelinesAPI is the narrow surface used by the Pipelines family.
type sagemakerPipelinesAPI interface {
	sagemakerTagsAPI
	ListPipelines(context.Context, *sagemaker.ListPipelinesInput, ...func(*sagemaker.Options)) (*sagemaker.ListPipelinesOutput, error)
	DescribePipeline(context.Context, *sagemaker.DescribePipelineInput, ...func(*sagemaker.Options)) (*sagemaker.DescribePipelineOutput, error)
	ListProjects(context.Context, *sagemaker.ListProjectsInput, ...func(*sagemaker.Options)) (*sagemaker.ListProjectsOutput, error)
	DescribeProject(context.Context, *sagemaker.DescribeProjectInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeProjectOutput, error)
	ListPartnerApps(context.Context, *sagemaker.ListPartnerAppsInput, ...func(*sagemaker.Options)) (*sagemaker.ListPartnerAppsOutput, error)
	DescribePartnerApp(context.Context, *sagemaker.DescribePartnerAppInput, ...func(*sagemaker.Options)) (*sagemaker.DescribePartnerAppOutput, error)
}

// scanSageMakerPipelines runs all Pipelines family phases for one region:
// pipelines, projects, partner apps.
func scanSageMakerPipelines(ctx context.Context, client sagemakerPipelinesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return runSageMakerPhases(
		sagemakerPhase{"sagemaker:ListPipelines", func() (int, int, error) { return scanSageMakerPipelinesList(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListProjects", func() (int, int, error) { return scanSageMakerProjects(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListPartnerApps", func() (int, int, error) { return scanSageMakerPartnerApps(ctx, client, acct, region, st, scanID) }},
	)
}

func scanSageMakerPipelinesList(ctx context.Context, client sagemakerPipelinesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.PipelineSummary]{
		listOp: "sagemaker:ListPipelines", describeOp: "sagemaker:DescribePipeline", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.PipelineSummary, *string, error) {
			out, err := client.ListPipelines(ctx, &sagemaker.ListPipelinesInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.PipelineSummaries, out.NextToken, nil
		},
		name: func(s smtypes.PipelineSummary) string { return sv(s.PipelineName) },
		listed: func(s smtypes.PipelineSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerPipeline, s.PipelineArn, s.PipelineName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.PipelineSummary) (*store.Resource, error) {
			name := sv(s.PipelineName)
			out, err := client.DescribePipeline(ctx, &sagemaker.DescribePipelineInput{PipelineName: &name})
			if err != nil {
				return nil, err
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
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListPipelines"))
}

func scanSageMakerProjects(ctx context.Context, client sagemakerPipelinesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.ProjectSummary]{
		listOp: "sagemaker:ListProjects", describeOp: "sagemaker:DescribeProject",
		list: func(ctx context.Context, token *string) ([]smtypes.ProjectSummary, *string, error) {
			out, err := client.ListProjects(ctx, &sagemaker.ListProjectsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ProjectSummaryList, out.NextToken, nil
		},
		name: func(s smtypes.ProjectSummary) string { return sv(s.ProjectName) },
		listed: func(s smtypes.ProjectSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerProject, s.ProjectArn, s.ProjectName, s.CreationTime, string(s.ProjectStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.ProjectSummary) (*store.Resource, error) {
			name := sv(s.ProjectName)
			out, err := client.DescribeProject(ctx, &sagemaker.DescribeProjectInput{ProjectName: &name})
			if err != nil {
				return nil, err
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
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListProjects"))
}

// scanSageMakerPartnerApps fans out by ARN — DescribePartnerApp's input
// field is Arn, not Name (unlike most other SageMaker resources).
func scanSageMakerPartnerApps(ctx context.Context, client sagemakerPipelinesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.PartnerAppSummary]{
		listOp: "sagemaker:ListPartnerApps", describeOp: "sagemaker:DescribePartnerApp", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.PartnerAppSummary, *string, error) {
			out, err := client.ListPartnerApps(ctx, &sagemaker.ListPartnerAppsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Summaries, out.NextToken, nil
		},
		name: func(s smtypes.PartnerAppSummary) string { return sv(s.Arn) },
		listed: func(s smtypes.PartnerAppSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerPartnerApp, s.Arn, s.Name, s.CreationTime, string(s.Status), s)
		},
		describe: func(ctx context.Context, s smtypes.PartnerAppSummary) (*store.Resource, error) {
			arn := sv(s.Arn)
			out, err := client.DescribePartnerApp(ctx, &sagemaker.DescribePartnerAppInput{Arn: &arn})
			if err != nil {
				return nil, err
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
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListPartnerApps"))
}
