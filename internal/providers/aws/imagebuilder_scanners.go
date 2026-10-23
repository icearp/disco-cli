package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/imagebuilder"
	"github.com/aws/aws-sdk-go-v2/service/imagebuilder/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeImageBuilderComponent, Service: "imagebuilder"})
	registerType(restype.Descriptor{Type: TypeImageBuilderContainerRecipe, Service: "imagebuilder"})
	registerType(restype.Descriptor{Type: TypeImageBuilderDistributionConfiguration, Service: "imagebuilder"})
	registerType(restype.Descriptor{Type: TypeImageBuilderImage, Service: "imagebuilder"})
	registerType(restype.Descriptor{Type: TypeImageBuilderImagePipeline, Service: "imagebuilder"})
	registerType(restype.Descriptor{Type: TypeImageBuilderImageRecipe, Service: "imagebuilder"})
	registerType(restype.Descriptor{Type: TypeImageBuilderInfrastructureConfig, Service: "imagebuilder"})
	registerType(restype.Descriptor{Type: TypeImageBuilderLifecyclePolicy, Service: "imagebuilder"})
	registerType(restype.Descriptor{Type: TypeImageBuilderWorkflow, Service: "imagebuilder"})
	registerService(serviceEntry{
		name: "aws:imagebuilder",
		fn:   scanImageBuilder,
	})
}

type imageBuilderAPI interface {
	ListComponents(context.Context, *imagebuilder.ListComponentsInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListComponentsOutput, error)
	ListContainerRecipes(context.Context, *imagebuilder.ListContainerRecipesInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListContainerRecipesOutput, error)
	ListDistributionConfigurations(context.Context, *imagebuilder.ListDistributionConfigurationsInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListDistributionConfigurationsOutput, error)
	ListImages(context.Context, *imagebuilder.ListImagesInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListImagesOutput, error)
	ListImagePipelines(context.Context, *imagebuilder.ListImagePipelinesInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListImagePipelinesOutput, error)
	ListImageRecipes(context.Context, *imagebuilder.ListImageRecipesInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListImageRecipesOutput, error)
	ListInfrastructureConfigurations(context.Context, *imagebuilder.ListInfrastructureConfigurationsInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListInfrastructureConfigurationsOutput, error)
	ListLifecyclePolicies(context.Context, *imagebuilder.ListLifecyclePoliciesInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListLifecyclePoliciesOutput, error)
	ListWorkflows(context.Context, *imagebuilder.ListWorkflowsInput, ...func(*imagebuilder.Options)) (*imagebuilder.ListWorkflowsOutput, error)
}

const (
	ibListComponentsOp                   = "imagebuilder:ListComponents"
	ibListContainerRecipesOp             = "imagebuilder:ListContainerRecipes"
	ibListDistributionConfigurationsOp   = "imagebuilder:ListDistributionConfigurations"
	ibListImagesOp                       = "imagebuilder:ListImages"
	ibListImagePipelinesOp               = "imagebuilder:ListImagePipelines"
	ibListImageRecipesOp                 = "imagebuilder:ListImageRecipes"
	ibListInfrastructureConfigurationsOp = "imagebuilder:ListInfrastructureConfigurations"
	ibListLifecyclePoliciesOp            = "imagebuilder:ListLifecyclePolicies"
	ibListWorkflowsOp                    = "imagebuilder:ListWorkflows"
)

// ibMaxResults is the page cap every Image Builder list op's Smithy model
// gives maxResults (RestrictedInteger, 1-25).
var ibMaxResults = sdkaws.Int32(25)

func scanImageBuilder(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := imagebuilder.NewFromConfig(acct.cfg, func(o *imagebuilder.Options) { o.Region = region })
	return scanImageBuilderAll(ctx, client, acct, region, st, scanID)
}

// scanImageBuilderAll runs every phase and joins their errors; only a store
// write failure stops it. The ops that take an Owner are asked for Owner=Self,
// so Amazon-managed, Marketplace, third-party and shared resources are not
// listed.
func scanImageBuilderAll(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var phases phaseTotals
	for _, phase := range []struct {
		op string
		fn func(context.Context, imageBuilderAPI, *account, string, *store.Store, string) (int, int, error)
	}{
		{ibListComponentsOp, scanIBComponents},
		{ibListContainerRecipesOp, scanIBContainerRecipes},
		{ibListDistributionConfigurationsOp, scanIBDistributionConfigs},
		{ibListImagesOp, scanIBImages},
		{ibListImagePipelinesOp, scanIBImagePipelines},
		{ibListImageRecipesOp, scanIBImageRecipes},
		{ibListInfrastructureConfigurationsOp, scanIBInfrastructureConfigs},
		{ibListLifecyclePoliciesOp, scanIBLifecyclePolicies},
		{ibListWorkflowsOp, scanIBWorkflows},
	} {
		t, i, perr := phase.fn(ctx, client, acct, region, st, scanID)
		if phases.addPhase(phase.op, t, i, perr) {
			break
		}
	}
	return phases.result(st, acct, region)
}

// ibRow builds the row of a listed Image Builder element, named by name or,
// when that is empty, by its ARN. The caller has checked arn is non-empty.
func ibRow(acct *account, region, scanID, rtype, arn, name string, element any, tags *string) *store.Resource {
	if name == "" {
		name = arn
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: arn, Name: &name, Region: &region,
		AttributesJSON: mustJSON(element), TagsJSON: tags, DiscoveredBy: scanID,
	}
}

// scanIBComponents stores component versions. ComponentVersion carries no
// tags; they need a per-component call, not made here.
func scanIBComponents(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ibListComponentsOp,
		func(ctx context.Context, token *string) ([]types.ComponentVersion, *string, error) {
			out, err := client.ListComponents(ctx, &imagebuilder.ListComponentsInput{Owner: types.OwnershipSelf, NextToken: token, MaxResults: ibMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.ComponentVersionList, out.NextToken, nil
		},
		func(c types.ComponentVersion) (*store.Resource, string, string) {
			arn := sv(c.Arn)
			if arn == "" {
				return nil, "components have an empty Arn", sv(c.Name)
			}
			return ibRow(acct, region, scanID, TypeImageBuilderComponent, arn, sv(c.Name), c, nil), "", ""
		})
}

func scanIBContainerRecipes(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ibListContainerRecipesOp,
		func(ctx context.Context, token *string) ([]types.ContainerRecipeSummary, *string, error) {
			out, err := client.ListContainerRecipes(ctx, &imagebuilder.ListContainerRecipesInput{Owner: types.OwnershipSelf, NextToken: token, MaxResults: ibMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.ContainerRecipeSummaryList, out.NextToken, nil
		},
		func(r types.ContainerRecipeSummary) (*store.Resource, string, string) {
			arn := sv(r.Arn)
			if arn == "" {
				return nil, "container recipes have an empty Arn", sv(r.Name)
			}
			return ibRow(acct, region, scanID, TypeImageBuilderContainerRecipe, arn, sv(r.Name), r, mapTagsJSON(r.Tags)), "", ""
		})
}

func scanIBDistributionConfigs(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ibListDistributionConfigurationsOp,
		func(ctx context.Context, token *string) ([]types.DistributionConfigurationSummary, *string, error) {
			out, err := client.ListDistributionConfigurations(ctx, &imagebuilder.ListDistributionConfigurationsInput{NextToken: token, MaxResults: ibMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.DistributionConfigurationSummaryList, out.NextToken, nil
		},
		func(d types.DistributionConfigurationSummary) (*store.Resource, string, string) {
			arn := sv(d.Arn)
			if arn == "" {
				return nil, "distribution configurations have an empty Arn", sv(d.Name)
			}
			return ibRow(acct, region, scanID, TypeImageBuilderDistributionConfiguration, arn, sv(d.Name), d, mapTagsJSON(d.Tags)), "", ""
		})
}

// scanIBImages stores self-owned image versions. IncludeDeprecated is left
// unset, so deprecated images are not listed. ImageVersion carries no tags
// and no per-build refs (recipe, infrastructure, distribution); both live on
// GetImage, which is not called.
func scanIBImages(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ibListImagesOp,
		func(ctx context.Context, token *string) ([]types.ImageVersion, *string, error) {
			out, err := client.ListImages(ctx, &imagebuilder.ListImagesInput{Owner: types.OwnershipSelf, NextToken: token, MaxResults: ibMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.ImageVersionList, out.NextToken, nil
		},
		func(im types.ImageVersion) (*store.Resource, string, string) {
			arn := sv(im.Arn)
			if arn == "" {
				return nil, "images have an empty Arn", sv(im.Name)
			}
			return ibRow(acct, region, scanID, TypeImageBuilderImage, arn, sv(im.Name), im, nil), "", ""
		})
}

// scanIBImagePipelines stores pipelines with the pipeline's own Tags.
// ImageTags are the tags the pipeline applies to the images it builds, not
// the pipeline's tags.
func scanIBImagePipelines(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ibListImagePipelinesOp,
		func(ctx context.Context, token *string) ([]types.ImagePipeline, *string, error) {
			out, err := client.ListImagePipelines(ctx, &imagebuilder.ListImagePipelinesInput{NextToken: token, MaxResults: ibMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.ImagePipelineList, out.NextToken, nil
		},
		func(p types.ImagePipeline) (*store.Resource, string, string) {
			arn := sv(p.Arn)
			if arn == "" {
				return nil, "image pipelines have an empty Arn", sv(p.Name)
			}
			return ibRow(acct, region, scanID, TypeImageBuilderImagePipeline, arn, sv(p.Name), p, mapTagsJSON(p.Tags)), "", ""
		})
}

func scanIBImageRecipes(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ibListImageRecipesOp,
		func(ctx context.Context, token *string) ([]types.ImageRecipeSummary, *string, error) {
			out, err := client.ListImageRecipes(ctx, &imagebuilder.ListImageRecipesInput{Owner: types.OwnershipSelf, NextToken: token, MaxResults: ibMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.ImageRecipeSummaryList, out.NextToken, nil
		},
		func(r types.ImageRecipeSummary) (*store.Resource, string, string) {
			arn := sv(r.Arn)
			if arn == "" {
				return nil, "image recipes have an empty Arn", sv(r.Name)
			}
			return ibRow(acct, region, scanID, TypeImageBuilderImageRecipe, arn, sv(r.Name), r, mapTagsJSON(r.Tags)), "", ""
		})
}

// scanIBInfrastructureConfigs stores infrastructure configurations with their
// own Tags. ResourceTags are the tags applied to the build instances, not the
// configuration's tags.
func scanIBInfrastructureConfigs(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ibListInfrastructureConfigurationsOp,
		func(ctx context.Context, token *string) ([]types.InfrastructureConfigurationSummary, *string, error) {
			out, err := client.ListInfrastructureConfigurations(ctx, &imagebuilder.ListInfrastructureConfigurationsInput{NextToken: token, MaxResults: ibMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.InfrastructureConfigurationSummaryList, out.NextToken, nil
		},
		func(ic types.InfrastructureConfigurationSummary) (*store.Resource, string, string) {
			arn := sv(ic.Arn)
			if arn == "" {
				return nil, "infrastructure configurations have an empty Arn", sv(ic.Name)
			}
			return ibRow(acct, region, scanID, TypeImageBuilderInfrastructureConfig, arn, sv(ic.Name), ic, mapTagsJSON(ic.Tags)), "", ""
		})
}

func scanIBLifecyclePolicies(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ibListLifecyclePoliciesOp,
		func(ctx context.Context, token *string) ([]types.LifecyclePolicySummary, *string, error) {
			out, err := client.ListLifecyclePolicies(ctx, &imagebuilder.ListLifecyclePoliciesInput{NextToken: token, MaxResults: ibMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.LifecyclePolicySummaryList, out.NextToken, nil
		},
		func(p types.LifecyclePolicySummary) (*store.Resource, string, string) {
			arn := sv(p.Arn)
			if arn == "" {
				return nil, "lifecycle policies have an empty Arn", sv(p.Name)
			}
			return ibRow(acct, region, scanID, TypeImageBuilderLifecyclePolicy, arn, sv(p.Name), p, mapTagsJSON(p.Tags)), "", ""
		})
}

// scanIBWorkflows stores workflow versions. WorkflowVersion carries no tags;
// they need a per-workflow call, not made here.
func scanIBWorkflows(ctx context.Context, client imageBuilderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, ibListWorkflowsOp,
		func(ctx context.Context, token *string) ([]types.WorkflowVersion, *string, error) {
			out, err := client.ListWorkflows(ctx, &imagebuilder.ListWorkflowsInput{Owner: types.OwnershipSelf, NextToken: token, MaxResults: ibMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.WorkflowVersionList, out.NextToken, nil
		},
		func(w types.WorkflowVersion) (*store.Resource, string, string) {
			arn := sv(w.Arn)
			if arn == "" {
				return nil, "workflows have an empty Arn", sv(w.Name)
			}
			return ibRow(acct, region, scanID, TypeImageBuilderWorkflow, arn, sv(w.Name), w, nil), "", ""
		})
}
