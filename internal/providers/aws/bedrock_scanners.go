package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
)

func init() {
	registerService(serviceEntry{
		name: "aws:bedrock",
		fn:   scanBedrock,
		emits: []coverage.TypeDecl{
			{Service: "bedrock", DiscoType: TypeBedrockGuardrail},
			{Service: "bedrock", DiscoType: TypeBedrockAutomatedReasoningPolicy},
			{Service: "bedrock", DiscoType: TypeBedrockIntelligentPromptRouter},
			{Service: "bedrock", DiscoType: TypeBedrockApplicationInferenceProfile},
			{Service: "bedrock", DiscoType: TypeBedrockEnforcedGuardrailConfiguration},
		},
	})
}

// bedrockAPI is the narrow set of Bedrock SDK ops invoked by scanBedrock
// sub-phases. Foundation-only — agent-side scanners use a separate client.
type bedrockAPI interface {
	ListGuardrails(context.Context, *bedrock.ListGuardrailsInput, ...func(*bedrock.Options)) (*bedrock.ListGuardrailsOutput, error)
	ListAutomatedReasoningPolicies(context.Context, *bedrock.ListAutomatedReasoningPoliciesInput, ...func(*bedrock.Options)) (*bedrock.ListAutomatedReasoningPoliciesOutput, error)
	ListPromptRouters(context.Context, *bedrock.ListPromptRoutersInput, ...func(*bedrock.Options)) (*bedrock.ListPromptRoutersOutput, error)
	ListInferenceProfiles(context.Context, *bedrock.ListInferenceProfilesInput, ...func(*bedrock.Options)) (*bedrock.ListInferenceProfilesOutput, error)
	ListEnforcedGuardrailsConfiguration(context.Context, *bedrock.ListEnforcedGuardrailsConfigurationInput, ...func(*bedrock.Options)) (*bedrock.ListEnforcedGuardrailsConfigurationOutput, error)
}

func scanBedrock(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := bedrock.NewFromConfig(acct.cfg, func(o *bedrock.Options) { o.Region = region })
	return scanBedrockFoundation(ctx, client, acct, region, st, scanID)
}
