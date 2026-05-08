package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/imagebuilder"
	"github.com/aws/aws-sdk-go-v2/service/imagebuilder/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:imagebuilder",
		fn:   scanImageBuilder,
		emits: []coverage.TypeDecl{
			{Service: "imagebuilder", DiscoType: TypeImageBuilderComponent, Leaf: true},
			{Service: "imagebuilder", DiscoType: TypeImageBuilderContainerRecipe, Leaf: true},
			{Service: "imagebuilder", DiscoType: TypeImageBuilderDistributionConfiguration, Leaf: true},
			{Service: "imagebuilder", DiscoType: TypeImageBuilderImagePipeline},
			{Service: "imagebuilder", DiscoType: TypeImageBuilderImageRecipe, Leaf: true},
			{Service: "imagebuilder", DiscoType: TypeImageBuilderInfrastructureConfig},
			{Service: "imagebuilder", DiscoType: TypeImageBuilderLifecyclePolicy},
			{Service: "imagebuilder", DiscoType: TypeImageBuilderWorkflow, Leaf: true},
		},
	})
}

type imageBuilderAPI interface {
	ListComponents(context.Context, *imagebuilder.ListComponentsInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListComponentsOutput, error)
	ListContainerRecipes(context.Context, *imagebuilder.ListContainerRecipesInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListContainerRecipesOutput, error)
	ListDistributionConfigurations(context.Context, *imagebuilder.ListDistributionConfigurationsInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListDistributionConfigurationsOutput, error)
	ListImagePipelines(context.Context, *imagebuilder.ListImagePipelinesInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListImagePipelinesOutput, error)
	ListImageRecipes(context.Context, *imagebuilder.ListImageRecipesInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListImageRecipesOutput, error)
	ListInfrastructureConfigurations(context.Context, *imagebuilder.ListInfrastructureConfigurationsInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListInfrastructureConfigurationsOutput, error)
	ListLifecyclePolicies(context.Context, *imagebuilder.ListLifecyclePoliciesInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListLifecyclePoliciesOutput, error)
	ListWorkflows(context.Context, *imagebuilder.ListWorkflowsInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListWorkflowsOutput, error)
}

// scanImageBuilder runs all eight ImageBuilder phases sequentially. Components
// and Workflows filter to Owner=Self to skip the Amazon-managed catalogue;
// ContainerRecipes / ImageRecipes default to Self.
func scanImageBuilder(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := imagebuilder.NewFromConfig(acct.cfg, func(o *imagebuilder.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIBComponents(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIBContainerRecipes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIBDistributionConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIBImagePipelines(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIBImageRecipes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIBInfrastructureConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIBLifecyclePolicies(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIBWorkflows(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanIBComponents(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := imagebuilder.NewListComponentsPaginator(client, &imagebuilder.ListComponentsInput{Owner: types.OwnershipSelf})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "imagebuilder:ListComponents", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("imagebuilder:ListComponents: %w", perr)
		}
		for _, c := range out.ComponentVersionList {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeImageBuilderComponent, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "imagebuilder components")
}

func scanIBContainerRecipes(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := imagebuilder.NewListContainerRecipesPaginator(client, &imagebuilder.ListContainerRecipesInput{Owner: types.OwnershipSelf})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "imagebuilder:ListContainerRecipes", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("imagebuilder:ListContainerRecipes: %w", perr)
		}
		for _, r := range out.ContainerRecipeSummaryList {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			label := sv(r.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeImageBuilderContainerRecipe, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "imagebuilder container-recipes")
}

func scanIBDistributionConfigs(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := imagebuilder.NewListDistributionConfigurationsPaginator(client, &imagebuilder.ListDistributionConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "imagebuilder:ListDistributionConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("imagebuilder:ListDistributionConfigurations: %w", perr)
		}
		for _, d := range out.DistributionConfigurationSummaryList {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeImageBuilderDistributionConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "imagebuilder distribution-configurations")
}

func scanIBImagePipelines(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := imagebuilder.NewListImagePipelinesPaginator(client, &imagebuilder.ListImagePipelinesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "imagebuilder:ListImagePipelines", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("imagebuilder:ListImagePipelines: %w", perr)
		}
		for _, p := range out.ImagePipelineList {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeImageBuilderImagePipeline, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "imagebuilder image-pipelines")
}

func scanIBImageRecipes(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := imagebuilder.NewListImageRecipesPaginator(client, &imagebuilder.ListImageRecipesInput{Owner: types.OwnershipSelf})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "imagebuilder:ListImageRecipes", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("imagebuilder:ListImageRecipes: %w", perr)
		}
		for _, r := range out.ImageRecipeSummaryList {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			label := sv(r.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeImageBuilderImageRecipe, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "imagebuilder image-recipes")
}

func scanIBInfrastructureConfigs(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := imagebuilder.NewListInfrastructureConfigurationsPaginator(client, &imagebuilder.ListInfrastructureConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "imagebuilder:ListInfrastructureConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("imagebuilder:ListInfrastructureConfigurations: %w", perr)
		}
		for _, ic := range out.InfrastructureConfigurationSummaryList {
			arn := sv(ic.Arn)
			if arn == "" {
				continue
			}
			label := sv(ic.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeImageBuilderInfrastructureConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(ic), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "imagebuilder infrastructure-configurations")
}

func scanIBLifecyclePolicies(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := imagebuilder.NewListLifecyclePoliciesPaginator(client, &imagebuilder.ListLifecyclePoliciesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "imagebuilder:ListLifecyclePolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("imagebuilder:ListLifecyclePolicies: %w", perr)
		}
		for _, p := range out.LifecyclePolicySummaryList {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			label := sv(p.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeImageBuilderLifecyclePolicy, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "imagebuilder lifecycle-policies")
}

func scanIBWorkflows(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := imagebuilder.NewListWorkflowsPaginator(client, &imagebuilder.ListWorkflowsInput{Owner: types.OwnershipSelf})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "imagebuilder:ListWorkflows", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("imagebuilder:ListWorkflows: %w", perr)
		}
		for _, w := range out.WorkflowVersionList {
			arn := sv(w.Arn)
			if arn == "" {
				continue
			}
			label := sv(w.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeImageBuilderWorkflow, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "imagebuilder workflows")
}
