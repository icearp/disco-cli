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
	registerType(restype.Descriptor{Type: TypeSageMakerEndpoint, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerEndpointConfig, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerModel, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerInferenceComponent, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerInferenceExperiment, Service: "sagemaker"})
}

// sagemakerInferenceAPI is the narrow surface used by the Inference family.
// Each phase List+fan-out Describe so attrs carry the full Describe body
// (ProductionVariants, KmsKeyId, NetworkConfig, ExecutionRoleArn, ImageUri).
type sagemakerInferenceAPI interface {
	sagemakerTagsAPI
	ListEndpoints(context.Context, *sagemaker.ListEndpointsInput, ...func(*sagemaker.Options)) (*sagemaker.ListEndpointsOutput, error)
	DescribeEndpoint(context.Context, *sagemaker.DescribeEndpointInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeEndpointOutput, error)
	ListEndpointConfigs(context.Context, *sagemaker.ListEndpointConfigsInput, ...func(*sagemaker.Options)) (*sagemaker.ListEndpointConfigsOutput, error)
	DescribeEndpointConfig(context.Context, *sagemaker.DescribeEndpointConfigInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeEndpointConfigOutput, error)
	ListModels(context.Context, *sagemaker.ListModelsInput, ...func(*sagemaker.Options)) (*sagemaker.ListModelsOutput, error)
	DescribeModel(context.Context, *sagemaker.DescribeModelInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeModelOutput, error)
	ListInferenceComponents(context.Context, *sagemaker.ListInferenceComponentsInput, ...func(*sagemaker.Options)) (*sagemaker.ListInferenceComponentsOutput, error)
	DescribeInferenceComponent(context.Context, *sagemaker.DescribeInferenceComponentInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeInferenceComponentOutput, error)
	ListInferenceExperiments(context.Context, *sagemaker.ListInferenceExperimentsInput, ...func(*sagemaker.Options)) (*sagemaker.ListInferenceExperimentsOutput, error)
	DescribeInferenceExperiment(context.Context, *sagemaker.DescribeInferenceExperimentInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeInferenceExperimentOutput, error)
}

// scanSageMakerInference runs all Inference-family phases for one region:
// endpoints, endpoint configs, models, inference components, experiments.
func scanSageMakerInference(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return runSageMakerPhases(
		sagemakerPhase{"sagemaker:ListEndpoints", func() (int, int, error) { return scanSageMakerEndpoints(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListEndpointConfigs", func() (int, int, error) { return scanSageMakerEndpointConfigs(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListModels", func() (int, int, error) { return scanSageMakerModels(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListInferenceComponents", func() (int, int, error) {
			return scanSageMakerInferenceComponents(ctx, client, acct, region, st, scanID)
		}},
		sagemakerPhase{"sagemaker:ListInferenceExperiments", func() (int, int, error) {
			return scanSageMakerInferenceExperiments(ctx, client, acct, region, st, scanID)
		}},
	)
}

func scanSageMakerEndpoints(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.EndpointSummary]{
		listOp: "sagemaker:ListEndpoints", describeOp: "sagemaker:DescribeEndpoint",
		list: func(ctx context.Context, token *string) ([]smtypes.EndpointSummary, *string, error) {
			out, err := client.ListEndpoints(ctx, &sagemaker.ListEndpointsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Endpoints, out.NextToken, nil
		},
		name: func(s smtypes.EndpointSummary) string { return sv(s.EndpointName) },
		listed: func(s smtypes.EndpointSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerEndpoint, s.EndpointArn, s.EndpointName, s.CreationTime, string(s.EndpointStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.EndpointSummary) (*store.Resource, error) {
			name := sv(s.EndpointName)
			out, err := client.DescribeEndpoint(ctx, &sagemaker.DescribeEndpointInput{EndpointName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.EndpointArn)
			if arn == "" {
				return nil, nil
			}
			epName := sv(out.EndpointName)
			status := string(out.EndpointStatus)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerEndpoint,
				NativeID:       arn,
				Name:           &epName,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListEndpoints"))
}

func scanSageMakerEndpointConfigs(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.EndpointConfigSummary]{
		listOp: "sagemaker:ListEndpointConfigs", describeOp: "sagemaker:DescribeEndpointConfig",
		list: func(ctx context.Context, token *string) ([]smtypes.EndpointConfigSummary, *string, error) {
			out, err := client.ListEndpointConfigs(ctx, &sagemaker.ListEndpointConfigsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EndpointConfigs, out.NextToken, nil
		},
		name: func(s smtypes.EndpointConfigSummary) string { return sv(s.EndpointConfigName) },
		listed: func(s smtypes.EndpointConfigSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerEndpointConfig, s.EndpointConfigArn, s.EndpointConfigName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.EndpointConfigSummary) (*store.Resource, error) {
			name := sv(s.EndpointConfigName)
			out, err := client.DescribeEndpointConfig(ctx, &sagemaker.DescribeEndpointConfigInput{EndpointConfigName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.EndpointConfigArn)
			if arn == "" {
				return nil, nil
			}
			cfgName := sv(out.EndpointConfigName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerEndpointConfig,
				NativeID:       arn,
				Name:           &cfgName,
				Region:         &region,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListEndpointConfigs"))
}

func scanSageMakerModels(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.ModelSummary]{
		listOp: "sagemaker:ListModels", describeOp: "sagemaker:DescribeModel",
		list: func(ctx context.Context, token *string) ([]smtypes.ModelSummary, *string, error) {
			out, err := client.ListModels(ctx, &sagemaker.ListModelsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Models, out.NextToken, nil
		},
		name: func(s smtypes.ModelSummary) string { return sv(s.ModelName) },
		listed: func(s smtypes.ModelSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerModel, s.ModelArn, s.ModelName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.ModelSummary) (*store.Resource, error) {
			name := sv(s.ModelName)
			out, err := client.DescribeModel(ctx, &sagemaker.DescribeModelInput{ModelName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.ModelArn)
			if arn == "" {
				return nil, nil
			}
			mname := sv(out.ModelName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerModel,
				NativeID:       arn,
				Name:           &mname,
				Region:         &region,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListModels"))
}

func scanSageMakerInferenceComponents(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.InferenceComponentSummary]{
		listOp: "sagemaker:ListInferenceComponents", describeOp: "sagemaker:DescribeInferenceComponent",
		list: func(ctx context.Context, token *string) ([]smtypes.InferenceComponentSummary, *string, error) {
			out, err := client.ListInferenceComponents(ctx, &sagemaker.ListInferenceComponentsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.InferenceComponents, out.NextToken, nil
		},
		name: func(s smtypes.InferenceComponentSummary) string { return sv(s.InferenceComponentName) },
		listed: func(s smtypes.InferenceComponentSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerInferenceComponent, s.InferenceComponentArn, s.InferenceComponentName, s.CreationTime, string(s.InferenceComponentStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.InferenceComponentSummary) (*store.Resource, error) {
			name := sv(s.InferenceComponentName)
			out, err := client.DescribeInferenceComponent(ctx, &sagemaker.DescribeInferenceComponentInput{InferenceComponentName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.InferenceComponentArn)
			if arn == "" {
				return nil, nil
			}
			icName := sv(out.InferenceComponentName)
			status := string(out.InferenceComponentStatus)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerInferenceComponent,
				NativeID:       arn,
				Name:           &icName,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListInferenceComponents"))
}

func scanSageMakerInferenceExperiments(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[string]{
		listOp: "sagemaker:ListInferenceExperiments", describeOp: "sagemaker:DescribeInferenceExperiment", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListInferenceExperiments(ctx, &sagemaker.ListInferenceExperimentsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			names := make([]string, len(out.InferenceExperiments))
			for i, e := range out.InferenceExperiments {
				names[i] = sv(e.Name)
			}
			return names, out.NextToken, nil
		},
		name: sagemakerKeyName,
		// No listed: the summary carries no ARN of its own, so a key whose
		// describe fails is not stored.
		describe: func(ctx context.Context, name string) (*store.Resource, error) {
			out, err := client.DescribeInferenceExperiment(ctx, &sagemaker.DescribeInferenceExperimentInput{Name: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.Arn)
			if arn == "" {
				return nil, nil
			}
			expName := sv(out.Name)
			status := string(out.Status)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerInferenceExperiment,
				NativeID:       arn,
				Name:           &expName,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListInferenceExperiments"))
}
