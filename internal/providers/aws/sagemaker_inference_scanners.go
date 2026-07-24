package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
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
func scanSageMakerInference(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func(context.Context, sagemakerInferenceAPI, *account, string, *store.Store, string) (int, int, error){
		scanSageMakerEndpoints,
		scanSageMakerEndpointConfigs,
		scanSageMakerModels,
		scanSageMakerInferenceComponents,
		scanSageMakerInferenceExperiments,
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

func scanSageMakerEndpoints(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListEndpointsPaginator(client, &sagemaker.ListEndpointsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListEndpoints", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListEndpoints: %w", perr)
		}
		for _, e := range out.Endpoints {
			if e.EndpointName != nil {
				names = append(names, *e.EndpointName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeEndpoint(gctx, &sagemaker.DescribeEndpointInput{EndpointName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeEndpoint %s: %w", name, derr)
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
	}, st, "sagemaker endpoints")
}

func scanSageMakerEndpointConfigs(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListEndpointConfigsPaginator(client, &sagemaker.ListEndpointConfigsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListEndpointConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListEndpointConfigs: %w", perr)
		}
		for _, c := range out.EndpointConfigs {
			if c.EndpointConfigName != nil {
				names = append(names, *c.EndpointConfigName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeEndpointConfig(gctx, &sagemaker.DescribeEndpointConfigInput{EndpointConfigName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeEndpointConfig %s: %w", name, derr)
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
	}, st, "sagemaker endpoint configs")
}

func scanSageMakerModels(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListModelsPaginator(client, &sagemaker.ListModelsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListModels", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListModels: %w", perr)
		}
		for _, m := range out.Models {
			if m.ModelName != nil {
				names = append(names, *m.ModelName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeModel(gctx, &sagemaker.DescribeModelInput{ModelName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeModel %s: %w", name, derr)
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
	}, st, "sagemaker models")
}

func scanSageMakerInferenceComponents(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListInferenceComponentsPaginator(client, &sagemaker.ListInferenceComponentsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListInferenceComponents", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListInferenceComponents: %w", perr)
		}
		for _, c := range out.InferenceComponents {
			if c.InferenceComponentName != nil {
				names = append(names, *c.InferenceComponentName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeInferenceComponent(gctx, &sagemaker.DescribeInferenceComponentInput{InferenceComponentName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeInferenceComponent %s: %w", name, derr)
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
	}, st, "sagemaker inference components")
}

func scanSageMakerInferenceExperiments(ctx context.Context, client sagemakerInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListInferenceExperimentsPaginator(client, &sagemaker.ListInferenceExperimentsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListInferenceExperiments", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListInferenceExperiments: %w", perr)
		}
		for _, e := range out.InferenceExperiments {
			if e.Name != nil {
				names = append(names, *e.Name)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeInferenceExperiment(gctx, &sagemaker.DescribeInferenceExperimentInput{Name: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeInferenceExperiment %s: %w", name, derr)
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
	}, st, "sagemaker inference experiments")
}
