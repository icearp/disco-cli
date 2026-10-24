package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBedrockCustomModel, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockImportedModel, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockMarketplaceModelEndpoint, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockProvisionedModel, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockCustomModelDeployment, Service: "bedrock"})
}

// isBedrockModelsGap reports the first-page refusals of a model listing that
// mean the op is not offered to this account or region, so the listing ends
// silently:
//   - an SCP's explicit deny;
//   - newer sub-features (custom-model deployments, Marketplace endpoints)
//     are not deployed in every Bedrock region, and AWS rejects the op with a
//     ValidationException reading "operation is not recognized" or "don't
//     have the permissions to perform the requested operation";
//   - some regions reject a not-yet-deployed op outright, with a 400/404
//     UnknownOperationException or a ValidationException reading "Unknown
//     operation" (casing varies by region), or with an HTML 404 page the SDK
//     fails to decode. UnknownOperationException and the HTML 404 are not
//     codes these ops model: they are gateway answers observed in those
//     regions, exceptions to the rule that a skip names only modeled codes
//     (#14);
//   - ListImportedModels and ListMarketplaceModelEndpoints are account
//     opt-in sub-features: an account not enrolled is refused with "not
//     authorized to invoke this API operation" while the rest of Bedrock
//     works.
//
// A real IAM denial names the action and is warned about.
func isBedrockModelsGap(err error) bool {
	return isSCPExplicitDeny(err) ||
		isAPIErrorWithMessage(err, "ValidationException", "operation is not recognized") ||
		isAPIErrorWithMessage(err, "ValidationException", "don't have the permissions to perform the requested operation") ||
		isAPIErrorCode(err, "UnknownOperationException") ||
		isAPIErrorWithMessage(err, "ValidationException", "Unknown operation") ||
		isAPIErrorWithMessage(err, "ValidationException", "Unknown Operation") ||
		isHTTP404(err) ||
		isAPIErrorWithMessage(err, "AccessDeniedException", "not authorized to invoke this API operation")
}

func scanBedrockCustomModels(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrock:ListCustomModels",
		bacPages(isBedrockModelsGap, func(ctx context.Context, token *string) ([]bedrocktypes.CustomModelSummary, *string, error) {
			out, err := client.ListCustomModels(ctx, &bedrock.ListCustomModelsInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ModelSummaries, out.NextToken, nil
		}),
		func(m bedrocktypes.CustomModelSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockCustomModel, "custom models", m.ModelArn, sv(m.ModelName), sv(m.ModelName), m)
			if r != nil {
				r.Status = nonEmptyPtr(string(m.ModelStatus))
				r.CreatedAt = tp(m.CreationTime)
			}
			return r, why, key
		}, tags, p)
}

func scanBedrockImportedModels(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrock:ListImportedModels",
		bacPages(isBedrockModelsGap, func(ctx context.Context, token *string) ([]bedrocktypes.ImportedModelSummary, *string, error) {
			out, err := client.ListImportedModels(ctx, &bedrock.ListImportedModelsInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ModelSummaries, out.NextToken, nil
		}),
		func(m bedrocktypes.ImportedModelSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockImportedModel, "imported models", m.ModelArn, sv(m.ModelName), sv(m.ModelName), m)
			if r != nil {
				r.CreatedAt = tp(m.CreationTime)
			}
			return r, why, key
		}, tags, p)
}

func scanBedrockProvisionedModels(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrock:ListProvisionedModelThroughputs",
		bacPages(isBedrockModelsGap, func(ctx context.Context, token *string) ([]bedrocktypes.ProvisionedModelSummary, *string, error) {
			out, err := client.ListProvisionedModelThroughputs(ctx, &bedrock.ListProvisionedModelThroughputsInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ProvisionedModelSummaries, out.NextToken, nil
		}),
		func(m bedrocktypes.ProvisionedModelSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockProvisionedModel, "provisioned models",
				m.ProvisionedModelArn, sv(m.ProvisionedModelName), sv(m.ProvisionedModelName), m)
			if r != nil {
				r.Status = nonEmptyPtr(string(m.Status))
				r.CreatedAt = tp(m.CreationTime)
			}
			return r, why, key
		}, tags, p)
}

func scanBedrockCustomModelDeployments(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "bedrock:ListCustomModelDeployments",
		bacPages(isBedrockModelsGap, func(ctx context.Context, token *string) ([]bedrocktypes.CustomModelDeploymentSummary, *string, error) {
			out, err := client.ListCustomModelDeployments(ctx, &bedrock.ListCustomModelDeploymentsInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ModelDeploymentSummaries, out.NextToken, nil
		}),
		func(d bedrocktypes.CustomModelDeploymentSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockCustomModelDeployment, "custom model deployments",
				d.CustomModelDeploymentArn, sv(d.CustomModelDeploymentName), sv(d.CustomModelDeploymentName), d)
			if r != nil {
				r.Status = nonEmptyPtr(string(d.Status))
				r.CreatedAt = tp(d.CreatedAt)
			}
			return r, why, key
		}, tags, p)
}

// scanBedrockMarketplaceModelEndpoints stores the region's Marketplace model
// endpoints. Their tags are not read: the Service Reference lists no
// endpoint resource of bedrock's ListTagsForResource, and its
// TaggableResourcesArn pattern does not accept an endpoint ARN.
func scanBedrockMarketplaceModelEndpoints(ctx context.Context, client bedrockAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "bedrock:ListMarketplaceModelEndpoints",
		bacPages(isBedrockModelsGap, func(ctx context.Context, token *string) ([]bedrocktypes.MarketplaceModelEndpointSummary, *string, error) {
			out, err := client.ListMarketplaceModelEndpoints(ctx, &bedrock.ListMarketplaceModelEndpointsInput{NextToken: token, MaxResults: sdkaws.Int32(bedrockPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.MarketplaceModelEndpoints, out.NextToken, nil
		}),
		func(e bedrocktypes.MarketplaceModelEndpointSummary) (*store.Resource, string, string) {
			r, why, key := bacRow(acct, region, scanID, TypeBedrockMarketplaceModelEndpoint, "Marketplace model endpoints", e.EndpointArn, sv(e.EndpointArn), sv(e.ModelSourceIdentifier), e)
			if r != nil {
				r.Status = nonEmptyPtr(string(e.Status))
				r.CreatedAt = tp(e.CreatedAt)
			}
			return r, why, key
		})
}
