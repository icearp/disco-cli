package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	bda "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBedrockGuardrail, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockGuardrailVersion, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockAutomatedReasoningPolicy, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockAutomatedReasoningPolicyVersion, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockAutomatedReasoningPolicyTestCase, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockIntelligentPromptRouter, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockApplicationInferenceProfile, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockInferenceProfile, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockFoundationModel, Service: "bedrock", Managed: true})
	registerType(restype.Descriptor{Type: TypeBedrockEnforcedGuardrailConfiguration, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockAgent, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentAlias, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentVersion, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentActionGroup, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentKnowledgeBase, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockAgentCollaborator, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockKnowledgeBase, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockDataSource, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockFlow, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockFlowAlias, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockFlowVersion, Service: "bedrock"})
	registerType(restype.Descriptor{Type: TypeBedrockPrompt, Service: "bedrock"})
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
	ListAgentVersions(context.Context, *bedrockagent.ListAgentVersionsInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentVersionsOutput, error)
	ListAgentActionGroups(context.Context, *bedrockagent.ListAgentActionGroupsInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentActionGroupsOutput, error)
	ListAgentKnowledgeBases(context.Context, *bedrockagent.ListAgentKnowledgeBasesInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentKnowledgeBasesOutput, error)
	ListAgentCollaborators(context.Context, *bedrockagent.ListAgentCollaboratorsInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListAgentCollaboratorsOutput, error)
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
	ListAutomatedReasoningPolicyTestCases(context.Context, *bedrock.ListAutomatedReasoningPolicyTestCasesInput, ...func(*bedrock.Options)) (*bedrock.ListAutomatedReasoningPolicyTestCasesOutput, error)
	ListPromptRouters(context.Context, *bedrock.ListPromptRoutersInput, ...func(*bedrock.Options)) (*bedrock.ListPromptRoutersOutput, error)
	ListInferenceProfiles(context.Context, *bedrock.ListInferenceProfilesInput, ...func(*bedrock.Options)) (*bedrock.ListInferenceProfilesOutput, error)
	ListEnforcedGuardrailsConfiguration(context.Context, *bedrock.ListEnforcedGuardrailsConfigurationInput, ...func(*bedrock.Options)) (*bedrock.ListEnforcedGuardrailsConfigurationOutput, error)
	ListFoundationModels(context.Context, *bedrock.ListFoundationModelsInput, ...func(*bedrock.Options)) (*bedrock.ListFoundationModelsOutput, error)
}

func scanBedrock(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	bclient := bedrock.NewFromConfig(acct.cfg, func(o *bedrock.Options) { o.Region = region })
	aclient := bedrockagent.NewFromConfig(acct.cfg, func(o *bedrockagent.Options) { o.Region = region })
	bdaClient := bda.NewFromConfig(acct.cfg, func(o *bda.Options) { o.Region = region })
	return scanBedrockClients(ctx, bclient, bclient, aclient, bdaClient, acct, region, st, scanID)
}

// scanBedrockClients runs the per-parent child fan-outs after every other
// phase: they make one call per agent or policy, so they are the likeliest to
// fail, and a failure there must not cost the parent-level phases.
func scanBedrockClients(ctx context.Context, fclient bedrockAPI, mclient bedrockModelsAPI, aclient bedrockAgentAPI, dclient bedrockDataAutomationAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var policyArns, agentIDs []string
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			var t, i int
			var ferr error
			policyArns, t, i, ferr = scanBedrockFoundation(ctx, fclient, acct, region, st, scanID)
			return t, i, ferr
		},
		func() (int, int, error) {
			var t, i int
			var ferr error
			agentIDs, t, i, ferr = scanBedrockAgents(ctx, aclient, acct, region, st, scanID)
			return t, i, ferr
		},
		func() (int, int, error) { return scanBedrockModels(ctx, mclient, acct, region, st, scanID) },
		func() (int, int, error) { return scanBedrockDataAutomation(ctx, dclient, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanBedrockAgentVersions(ctx, aclient, acct, region, st, scanID, agentIDs)
		},
		func() (int, int, error) {
			return scanBedrockAgentActionGroups(ctx, aclient, acct, region, st, scanID, agentIDs)
		},
		func() (int, int, error) {
			return scanBedrockAgentKnowledgeBases(ctx, aclient, acct, region, st, scanID, agentIDs)
		},
		func() (int, int, error) {
			return scanBedrockAgentCollaborators(ctx, aclient, acct, region, st, scanID, agentIDs)
		},
		func() (int, int, error) {
			return scanBedrockARPolicyTestCases(ctx, fclient, acct, region, st, scanID, policyArns)
		},
	} {
		t, i, perr := phase()
		total += t
		inserted += i
		if perr != nil {
			return total, inserted, perr
		}
	}
	return total, inserted, nil
}
