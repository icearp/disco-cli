package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "bedrock", DiscoType: TypeBedrockCustomModel, Leaf: true},
		coverage.TypeDecl{Service: "bedrock", DiscoType: TypeBedrockImportedModel, Leaf: true},
		coverage.TypeDecl{Service: "bedrock", DiscoType: TypeBedrockMarketplaceModelEndpoint, Leaf: true},
		// provisioned-model + custom-model-deployment reference the model they serve.
		coverage.TypeDecl{Service: "bedrock", DiscoType: TypeBedrockProvisionedModel},
		coverage.TypeDecl{Service: "bedrock", DiscoType: TypeBedrockCustomModelDeployment},
	)
}

// bedrockModelsAPI is the narrow Bedrock surface the model scanners use. All
// five List ops are blanket-callable (no required input).
type bedrockModelsAPI interface {
	ListCustomModels(context.Context, *bedrock.ListCustomModelsInput, ...func(*bedrock.Options)) (*bedrock.ListCustomModelsOutput, error)
	ListImportedModels(context.Context, *bedrock.ListImportedModelsInput, ...func(*bedrock.Options)) (*bedrock.ListImportedModelsOutput, error)
	ListProvisionedModelThroughputs(context.Context, *bedrock.ListProvisionedModelThroughputsInput, ...func(*bedrock.Options)) (*bedrock.ListProvisionedModelThroughputsOutput, error)
	ListCustomModelDeployments(context.Context, *bedrock.ListCustomModelDeploymentsInput, ...func(*bedrock.Options)) (*bedrock.ListCustomModelDeploymentsOutput, error)
	ListMarketplaceModelEndpoints(context.Context, *bedrock.ListMarketplaceModelEndpointsInput, ...func(*bedrock.Options)) (*bedrock.ListMarketplaceModelEndpointsOutput, error)
}

// scanBedrockModels discovers custom / imported / provisioned models, custom-
// model deployments, and Marketplace model endpoints. Called from scanBedrock
// (which owns the bedrock client).
func scanBedrockModels(ctx context.Context, client bedrockModelsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanBedrockCustomModels(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBedrockImportedModels(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanBedrockProvisionedModels(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanBedrockCustomModelDeployments(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanBedrockMarketplaceModelEndpoints(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// bedrockModelsListErr soft-skips the SCP / feature-gap / access-denied shapes
// shared by every Bedrock List op (matching the foundation scanners), returning
// (true, ...) when the phase should stop with no rows.
func bedrockModelsListErr(st *store.Store, op, acctID, region string, perr error) (stop bool, err error) {
	switch {
	case isSCPExplicitDeny(perr):
		return true, nil
	// Newer Bedrock sub-features (custom-model deployments, Marketplace
	// endpoints) aren't deployed in every Bedrock region; AWS rejects the op
	// with a ValidationException feature-gap shape rather than AccessDenied.
	// Silent-skip — the warn would fire on every scan in non-supporting regions.
	case isAPIErrorWithMessage(perr, "ValidationException", "operation is not recognized"),
		isAPIErrorWithMessage(perr, "ValidationException", "don't have the permissions to perform the requested operation"):
		return true, nil
	case isAccessDenied(perr):
		return true, skipIfAccessDenied(st, op, acctID, region, perr)
	default:
		return true, fmt.Errorf("%s: %w", op, perr)
	}
}

func scanBedrockCustomModels(ctx context.Context, client bedrockModelsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bedrock.NewListCustomModelsPaginator(client, &bedrock.ListCustomModelsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			_, e := bedrockModelsListErr(st, "bedrock:ListCustomModels", acct.ID, region, perr)
			return 0, 0, e
		}
		for _, m := range out.ModelSummaries {
			arn := sv(m.ModelArn)
			if arn == "" {
				continue
			}
			name := sv(m.ModelName)
			status := string(m.ModelStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockCustomModel, NativeID: arn,
				Name: &name, Region: &region, Status: &status, CreatedAt: tp(m.CreationTime),
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrock custom-models")
}

func scanBedrockImportedModels(ctx context.Context, client bedrockModelsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bedrock.NewListImportedModelsPaginator(client, &bedrock.ListImportedModelsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			_, e := bedrockModelsListErr(st, "bedrock:ListImportedModels", acct.ID, region, perr)
			return 0, 0, e
		}
		for _, m := range out.ModelSummaries {
			arn := sv(m.ModelArn)
			if arn == "" {
				continue
			}
			name := sv(m.ModelName)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockImportedModel, NativeID: arn,
				Name: &name, Region: &region, CreatedAt: tp(m.CreationTime),
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrock imported-models")
}

func scanBedrockProvisionedModels(ctx context.Context, client bedrockModelsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bedrock.NewListProvisionedModelThroughputsPaginator(client, &bedrock.ListProvisionedModelThroughputsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			_, e := bedrockModelsListErr(st, "bedrock:ListProvisionedModelThroughputs", acct.ID, region, perr)
			return 0, 0, e
		}
		for _, m := range out.ProvisionedModelSummaries {
			arn := sv(m.ProvisionedModelArn)
			if arn == "" {
				continue
			}
			name := sv(m.ProvisionedModelName)
			status := string(m.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockProvisionedModel, NativeID: arn,
				Name: &name, Region: &region, Status: &status, CreatedAt: tp(m.CreationTime),
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrock provisioned-models")
}

func scanBedrockCustomModelDeployments(ctx context.Context, client bedrockModelsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bedrock.NewListCustomModelDeploymentsPaginator(client, &bedrock.ListCustomModelDeploymentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			_, e := bedrockModelsListErr(st, "bedrock:ListCustomModelDeployments", acct.ID, region, perr)
			return 0, 0, e
		}
		for _, d := range out.ModelDeploymentSummaries {
			arn := sv(d.CustomModelDeploymentArn)
			if arn == "" {
				continue
			}
			name := sv(d.CustomModelDeploymentName)
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockCustomModelDeployment, NativeID: arn,
				Name: &name, Region: &region, Status: &status, CreatedAt: tp(d.CreatedAt),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrock custom-model-deployments")
}

func scanBedrockMarketplaceModelEndpoints(ctx context.Context, client bedrockModelsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := bedrock.NewListMarketplaceModelEndpointsPaginator(client, &bedrock.ListMarketplaceModelEndpointsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			_, e := bedrockModelsListErr(st, "bedrock:ListMarketplaceModelEndpoints", acct.ID, region, perr)
			return 0, 0, e
		}
		for _, e := range out.MarketplaceModelEndpoints {
			arn := sv(e.EndpointArn)
			if arn == "" {
				continue
			}
			status := string(e.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBedrockMarketplaceModelEndpoint, NativeID: arn,
				Name: &arn, Region: &region, Status: &status, CreatedAt: tp(e.CreatedAt),
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "bedrock marketplace-model-endpoints")
}
