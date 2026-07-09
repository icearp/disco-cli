package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	bda "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBedrockGuardrail, Service: "bedrock", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBedrockGuardrailVersion, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockAutomatedReasoningPolicy, Service: "bedrock", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBedrockAutomatedReasoningPolicyVersion, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockIntelligentPromptRouter, Service: "bedrock", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBedrockApplicationInferenceProfile, Service: "bedrock", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBedrockInferenceProfile, Service: "bedrock", Upstream: "AWS::bedrock::inference-profile", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBedrockFoundationModel, Service: "bedrock", Upstream: "AWS::bedrock::foundation-model", Leaf: true, Managed: true})
	registerType(restype.Descriptor{Type: TypeBedrockEnforcedGuardrailConfiguration, Service: "bedrock", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBedrockAgent, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentAlias, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockKnowledgeBase, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockDataSource, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockFlow, Service: "bedrock", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBedrockFlowAlias, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockFlowVersion, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockPrompt, Service: "bedrock", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBedrockPromptVersion, Service: "bedrock"})
	registerService(serviceEntry{
		name: "aws:bedrock",
		fn:   scanBedrock,
	})
}

// bedrockAPI is the narrow set of Bedrock SDK ops scanBedrock's foundation
// sub-phases use — agent-side scanners use a separate client (bedrockAgentAPI
// covers the bedrockagent SDK: Agents, KBs, Flows, Prompts).
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
	ListFoundationModels(context.Context, *bedrock.ListFoundationModelsInput, ...func(*bedrock.Options)) (*bedrock.ListFoundationModelsOutput, error)
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
	bdaClient := bda.NewFromConfig(acct.cfg, func(o *bda.Options) { o.Region = region })
	t4, i4, e4 := scanBedrockDataAutomation(ctx, bdaClient, acct, region, st, scanID)
	if e4 != nil {
		return t1 + t2 + t3, i1 + i2 + i3, e4
	}
	return t1 + t2 + t3 + t4, i1 + i2 + i3 + i4, nil
}
