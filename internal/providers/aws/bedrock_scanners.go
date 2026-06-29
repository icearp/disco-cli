package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
)

func init() {
	registerService(serviceEntry{
		name: "aws:bedrock",
		fn:   scanBedrock,
		emits: []coverage.TypeDecl{
			{Service: "bedrock", DiscoType: TypeBedrockGuardrail, Leaf: true},
			{Service: "bedrock", DiscoType: TypeBedrockGuardrailVersion},
			{Service: "bedrock", DiscoType: TypeBedrockAutomatedReasoningPolicy, Leaf: true},
			{Service: "bedrock", DiscoType: TypeBedrockAutomatedReasoningPolicyVersion},
			{Service: "bedrock", DiscoType: TypeBedrockIntelligentPromptRouter, Leaf: true},
			{Service: "bedrock", DiscoType: TypeBedrockApplicationInferenceProfile, Leaf: true},
			{Service: "bedrock", DiscoType: TypeBedrockEnforcedGuardrailConfiguration, Leaf: true},
			{Service: "bedrock", DiscoType: TypeBedrockAgent},
			{Service: "bedrock", DiscoType: TypeBedrockAgentAlias},
			{Service: "bedrock", DiscoType: TypeBedrockKnowledgeBase},
			{Service: "bedrock", DiscoType: TypeBedrockDataSource},
			{Service: "bedrock", DiscoType: TypeBedrockFlow, Leaf: true},
			{Service: "bedrock", DiscoType: TypeBedrockFlowAlias},
			{Service: "bedrock", DiscoType: TypeBedrockFlowVersion},
			{Service: "bedrock", DiscoType: TypeBedrockPrompt, Leaf: true},
			{Service: "bedrock", DiscoType: TypeBedrockPromptVersion},
		},
	})
}

// bedrockAPI is the narrow set of Bedrock SDK ops invoked by scanBedrock
// sub-phases. Foundation-only — agent-side scanners use a separate client.
// bedrockAgentAPI covers the bedrockagent SDK (Agents, KBs, Flows, Prompts).
type bedrockAgentAPI interface {
	ListAgents(context.Context, *bedrockagent.ListAgentsInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentsOutput, error)
	ListAgentAliases(context.Context, *bedrockagent.ListAgentAliasesInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentAliasesOutput, error)
	ListKnowledgeBases(context.Context, *bedrockagent.ListKnowledgeBasesInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListKnowledgeBasesOutput, error)
	ListDataSources(context.Context, *bedrockagent.ListDataSourcesInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListDataSourcesOutput, error)
	GetKnowledgeBase(context.Context, *bedrockagent.GetKnowledgeBaseInput, ...func(*bedrockagent.Options)) (*bedrockagent.GetKnowledgeBaseOutput, error)
	GetDataSource(context.Context, *bedrockagent.GetDataSourceInput, ...func(*bedrockagent.Options)) (*bedrockagent.GetDataSourceOutput, error)
	ListFlows(context.Context, *bedrockagent.ListFlowsInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListFlowsOutput, error)
	ListFlowAliases(context.Context, *bedrockagent.ListFlowAliasesInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListFlowAliasesOutput, error)
	ListFlowVersions(context.Context, *bedrockagent.ListFlowVersionsInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListFlowVersionsOutput, error)
	ListPrompts(context.Context, *bedrockagent.ListPromptsInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListPromptsOutput, error)
}

type bedrockAPI interface {
	ListGuardrails(context.Context, *bedrock.ListGuardrailsInput, ...func(*bedrock.Options)) (*bedrock.ListGuardrailsOutput, error)
	ListAutomatedReasoningPolicies(context.Context, *bedrock.ListAutomatedReasoningPoliciesInput, ...func(*bedrock.Options)) (*bedrock.ListAutomatedReasoningPoliciesOutput, error)
	ListPromptRouters(context.Context, *bedrock.ListPromptRoutersInput, ...func(*bedrock.Options)) (*bedrock.ListPromptRoutersOutput, error)
	ListInferenceProfiles(context.Context, *bedrock.ListInferenceProfilesInput, ...func(*bedrock.Options)) (*bedrock.ListInferenceProfilesOutput, error)
	ListEnforcedGuardrailsConfiguration(context.Context, *bedrock.ListEnforcedGuardrailsConfigurationInput, ...func(*bedrock.Options)) (*bedrock.ListEnforcedGuardrailsConfigurationOutput, error)
}

func scanBedrock(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	bclient := bedrock.NewFromConfig(acct.cfg, func(o *bedrock.Options) { o.Region = region })
	aclient := bedrockagent.NewFromConfig(acct.cfg, func(o *bedrockagent.Options) { o.Region = region })
	t1, i1, e1 := scanBedrockFoundation(ctx, bclient, acct, region, st, scanID)
	if e1 != nil {
		return 0, 0, e1
	}
	t2, i2, e2 := scanBedrockAgents(ctx, aclient, acct, region, st, scanID)
	if e2 != nil {
		return t1, i1, e2
	}
	t3, i3, e3 := scanBedrockModels(ctx, bclient, acct, region, st, scanID)
	if e3 != nil {
		return t1 + t2, i1 + i2, e3
	}
	return t1 + t2 + t3, i1 + i2 + i3, nil
}
