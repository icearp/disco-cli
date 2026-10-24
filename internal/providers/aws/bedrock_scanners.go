package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	bda "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation"
	bdatypes "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation/types"
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
	registerType(restype.Descriptor{Type: TypeBedrockFoundationModel, Service: "bedrock", Managed: true, Untaggable: true})
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

// bedrockAgentAPI is the narrow bedrockagent SDK surface (agents, knowledge
// bases, flows, prompts) the bedrock scan uses.
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
	ListTagsForResource(context.Context, *bedrockagent.ListTagsForResourceInput, ...func(*bedrockagent.Options)) (*bedrockagent.ListTagsForResourceOutput, error)
}

// bedrockAPI is the narrow bedrock SDK surface (guardrails, automated
// reasoning, routers, inference profiles, models) the bedrock scan uses.
type bedrockAPI interface {
	ListGuardrails(context.Context, *bedrock.ListGuardrailsInput, ...func(*bedrock.Options)) (*bedrock.ListGuardrailsOutput, error)
	ListAutomatedReasoningPolicies(context.Context, *bedrock.ListAutomatedReasoningPoliciesInput, ...func(*bedrock.Options)) (*bedrock.ListAutomatedReasoningPoliciesOutput, error)
	ListAutomatedReasoningPolicyTestCases(context.Context, *bedrock.ListAutomatedReasoningPolicyTestCasesInput, ...func(*bedrock.Options)) (*bedrock.ListAutomatedReasoningPolicyTestCasesOutput, error)
	ListPromptRouters(context.Context, *bedrock.ListPromptRoutersInput, ...func(*bedrock.Options)) (*bedrock.ListPromptRoutersOutput, error)
	ListInferenceProfiles(context.Context, *bedrock.ListInferenceProfilesInput, ...func(*bedrock.Options)) (*bedrock.ListInferenceProfilesOutput, error)
	ListEnforcedGuardrailsConfiguration(context.Context, *bedrock.ListEnforcedGuardrailsConfigurationInput, ...func(*bedrock.Options)) (*bedrock.ListEnforcedGuardrailsConfigurationOutput, error)
	ListFoundationModels(context.Context, *bedrock.ListFoundationModelsInput, ...func(*bedrock.Options)) (*bedrock.ListFoundationModelsOutput, error)
	ListCustomModels(context.Context, *bedrock.ListCustomModelsInput, ...func(*bedrock.Options)) (*bedrock.ListCustomModelsOutput, error)
	ListImportedModels(context.Context, *bedrock.ListImportedModelsInput, ...func(*bedrock.Options)) (*bedrock.ListImportedModelsOutput, error)
	ListProvisionedModelThroughputs(context.Context, *bedrock.ListProvisionedModelThroughputsInput, ...func(*bedrock.Options)) (*bedrock.ListProvisionedModelThroughputsOutput, error)
	ListCustomModelDeployments(context.Context, *bedrock.ListCustomModelDeploymentsInput, ...func(*bedrock.Options)) (*bedrock.ListCustomModelDeploymentsOutput, error)
	ListMarketplaceModelEndpoints(context.Context, *bedrock.ListMarketplaceModelEndpointsInput, ...func(*bedrock.Options)) (*bedrock.ListMarketplaceModelEndpointsOutput, error)
	ListTagsForResource(context.Context, *bedrock.ListTagsForResourceInput, ...func(*bedrock.Options)) (*bedrock.ListTagsForResourceOutput, error)
}

// The tag ops of the three SDK packages the bedrock scan calls.
const (
	bedrockListTagsOp      = "bedrock:ListTagsForResource"
	bedrockAgentListTagsOp = "bedrockagent:ListTagsForResource"
	bedrockDAListTagsOp    = "bedrockdataautomation:ListTagsForResource"
)

// bedrockPageCap is the maxResults cap (1-1000) the bedrock, bedrockagent and
// bedrockdataautomation models document for every listing sent one here.
const bedrockPageCap int32 = 1000

func scanBedrock(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	bclient := bedrock.NewFromConfig(acct.cfg, func(o *bedrock.Options) { o.Region = region })
	aclient := bedrockagent.NewFromConfig(acct.cfg, func(o *bedrockagent.Options) { o.Region = region })
	bdaClient := bda.NewFromConfig(acct.cfg, func(o *bda.Options) { o.Region = region })
	return scanBedrockClients(ctx, bclient, aclient, bdaClient, acct, region, st, scanID)
}

// The endpoints the bedrock scan calls: each SDK package has its own host,
// and a region may serve bedrock without the agent or data-automation host.
const (
	bedrockHost = iota
	bedrockAgentHost
	bedrockDAHost
)

// bedrockPhase is one listing of the bedrock scan: the op it is reported
// under, the host it calls, and run, which adds its counts and errors to p
// and reports whether a store write failed.
type bedrockPhase struct {
	op   string
	host int
	run  func(p *phaseTotals) (storeWriteFailed bool)
}

// bedrockPhases is the bedrock scan's phases in the order they run. A child
// phase gets the parents its parent phase stored, so the phases must run one
// at a time, in order.
func bedrockPhases(ctx context.Context, bclient bedrockAPI, aclient bedrockAgentAPI, dclient bedrockDataAutomationAPI, acct *account, region string, st *store.Store, scanID string) []bedrockPhase {
	var (
		btags, atags, dtags                               = bedrockTags(bclient), bedrockAgentTags(aclient), bedrockDATags(dclient)
		guardrails, policies, agents, kbs, flows, prompts []childParent
	)
	counted := func(host int, op string, run func() (int, int, error)) bedrockPhase {
		return bedrockPhase{op, host, func(p *phaseTotals) bool {
			t, i, err := run()
			return p.addPhase(op, t, i, err)
		}}
	}
	return []bedrockPhase{
		{"bedrock:ListGuardrails", bedrockHost, func(p *phaseTotals) (stop bool) {
			guardrails, stop = scanBedrockGuardrails(ctx, bclient, acct, region, st, scanID, btags, p)
			return stop
		}},
		counted(bedrockHost, "bedrock:ListGuardrails(versions)", func() (int, int, error) {
			return scanBedrockGuardrailVersions(ctx, bclient, acct, region, st, scanID, childParentIDs(guardrails))
		}),
		{"bedrock:ListAutomatedReasoningPolicies", bedrockHost, func(p *phaseTotals) (stop bool) {
			policies, stop = scanBedrockARPolicies(ctx, bclient, acct, region, st, scanID, btags, p)
			return stop
		}},
		counted(bedrockHost, "bedrock:ListAutomatedReasoningPolicies(versions)", func() (int, int, error) {
			return scanBedrockARPolicyVersions(ctx, bclient, acct, region, st, scanID, childParentIDs(policies))
		}),
		{"bedrock:ListPromptRouters", bedrockHost, func(p *phaseTotals) bool {
			return scanBedrockPromptRouters(ctx, bclient, acct, region, st, scanID, btags, p)
		}},
		{"bedrock:ListInferenceProfiles", bedrockHost, func(p *phaseTotals) bool {
			return scanBedrockInferenceProfiles(ctx, bclient, acct, region, st, scanID, btags, p)
		}},
		counted(bedrockHost, "bedrock:ListEnforcedGuardrailsConfiguration", func() (int, int, error) {
			return scanBedrockEnforcedGuardrails(ctx, bclient, acct, region, st, scanID)
		}),
		counted(bedrockHost, "bedrock:ListFoundationModels", func() (int, int, error) {
			return scanBedrockFoundationModels(ctx, bclient, acct, region, st, scanID)
		}),
		{"bedrockagent:ListAgents", bedrockAgentHost, func(p *phaseTotals) (stop bool) {
			agents, stop = scanBedrockAgentList(ctx, aclient, acct, region, st, scanID, atags, p)
			return stop
		}},
		{"bedrockagent:ListAgentAliases", bedrockAgentHost, func(p *phaseTotals) bool {
			return scanBedrockAgentAliases(ctx, aclient, acct, region, st, scanID, agents, atags, p)
		}},
		{"bedrockagent:ListKnowledgeBases", bedrockAgentHost, func(p *phaseTotals) (stop bool) {
			kbs, stop = scanBedrockKnowledgeBases(ctx, aclient, acct, region, st, scanID, p)
			return stop
		}},
		{"bedrockagent:ListDataSources", bedrockAgentHost, func(p *phaseTotals) bool {
			return scanBedrockDataSources(ctx, aclient, acct, region, st, scanID, childParentIDs(kbs), p)
		}},
		{"bedrockagent:ListFlows", bedrockAgentHost, func(p *phaseTotals) (stop bool) {
			flows, stop = scanBedrockFlows(ctx, aclient, acct, region, st, scanID, atags, p)
			return stop
		}},
		{"bedrockagent:ListFlowAliases", bedrockAgentHost, func(p *phaseTotals) bool {
			return scanBedrockFlowAliases(ctx, aclient, acct, region, st, scanID, flows, atags, p)
		}},
		counted(bedrockAgentHost, "bedrockagent:ListFlowVersions", func() (int, int, error) {
			return scanBedrockFlowVersions(ctx, aclient, acct, region, st, scanID, childParentIDs(flows))
		}),
		{"bedrockagent:ListPrompts", bedrockAgentHost, func(p *phaseTotals) (stop bool) {
			prompts, stop = scanBedrockPrompts(ctx, aclient, acct, region, st, scanID, atags, p)
			return stop
		}},
		{"bedrockagent:ListPrompts(versions)", bedrockAgentHost, func(p *phaseTotals) bool {
			return scanBedrockPromptVersions(ctx, aclient, acct, region, st, scanID, prompts, atags, p)
		}},
		{"bedrock:ListCustomModels", bedrockHost, func(p *phaseTotals) bool {
			return scanBedrockCustomModels(ctx, bclient, acct, region, st, scanID, btags, p)
		}},
		{"bedrock:ListImportedModels", bedrockHost, func(p *phaseTotals) bool {
			return scanBedrockImportedModels(ctx, bclient, acct, region, st, scanID, btags, p)
		}},
		{"bedrock:ListProvisionedModelThroughputs", bedrockHost, func(p *phaseTotals) bool {
			return scanBedrockProvisionedModels(ctx, bclient, acct, region, st, scanID, btags, p)
		}},
		{"bedrock:ListCustomModelDeployments", bedrockHost, func(p *phaseTotals) bool {
			return scanBedrockCustomModelDeployments(ctx, bclient, acct, region, st, scanID, btags, p)
		}},
		counted(bedrockHost, "bedrock:ListMarketplaceModelEndpoints", func() (int, int, error) {
			return scanBedrockMarketplaceModelEndpoints(ctx, bclient, acct, region, st, scanID)
		}),
		{"bedrockdataautomation:ListBlueprints", bedrockDAHost, func(p *phaseTotals) bool {
			return scanBedrockBlueprints(ctx, dclient, acct, region, st, scanID, dtags, p)
		}},
		{"bedrockdataautomation:ListDataAutomationProjects", bedrockDAHost, func(p *phaseTotals) bool {
			return scanBedrockDataAutomationProjects(ctx, dclient, acct, region, st, scanID, dtags, p)
		}},
		{"bedrockdataautomation:ListDataAutomationLibraries", bedrockDAHost, func(p *phaseTotals) bool {
			return scanBedrockDataAutomationLibraries(ctx, dclient, acct, region, st, scanID, dtags, p)
		}},
		// The per-parent child fan-outs run last: they make one call per agent
		// or policy, so they are the likeliest to fail.
		counted(bedrockAgentHost, "bedrockagent:ListAgentVersions", func() (int, int, error) {
			return scanBedrockAgentVersions(ctx, aclient, acct, region, st, scanID, childParentIDs(agents))
		}),
		counted(bedrockAgentHost, "bedrockagent:ListAgentActionGroups", func() (int, int, error) {
			return scanBedrockAgentActionGroups(ctx, aclient, acct, region, st, scanID, childParentIDs(agents))
		}),
		counted(bedrockAgentHost, "bedrockagent:ListAgentKnowledgeBases", func() (int, int, error) {
			return scanBedrockAgentKnowledgeBases(ctx, aclient, acct, region, st, scanID, childParentIDs(agents))
		}),
		counted(bedrockAgentHost, "bedrockagent:ListAgentCollaborators", func() (int, int, error) {
			return scanBedrockAgentCollaborators(ctx, aclient, acct, region, st, scanID, childParentIDs(agents))
		}),
		counted(bedrockHost, "bedrock:ListAutomatedReasoningPolicyTestCases", func() (int, int, error) {
			return scanBedrockARPolicyTestCases(ctx, bclient, acct, region, st, scanID, childParentIDs(policies))
		}),
	}
}

// scanBedrockClients runs every bedrock phase (bedrockPhases) in order. A
// phase's error does not stop the later ones; a failed write does, since
// every later write would fail too, and so does ctx's end, reported as one
// error. The phases run one at a time, so readRowTags's own bound is the
// bound on the tag reads in flight.
//
// A host whose first phase stores nothing and fails only with NXDOMAIN (the
// blueprint phase makes one listing per owner, each failing alike) is not
// served in this region, and its later phases are not run. For the agent and
// data-automation hosts that NXDOMAIN is dropped: the region serves bedrock
// without that part of it. The bedrock host's NXDOMAIN is kept, so a region
// without bedrock is reported unavailable; once any host stored rows, every
// NXDOMAIN left is a failure of this scan instead (resultLateNXDOMAIN), so
// those rows keep their counts.
func scanBedrockClients(ctx context.Context, bclient bedrockAPI, aclient bedrockAgentAPI, dclient bedrockDataAutomationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var p phaseTotals
	phases := bedrockPhases(ctx, bclient, aclient, dclient, acct, region, st, scanID)
	var ran, gone [3]bool
	notRun := 0
	for i, ph := range phases {
		if gone[ph.host] {
			continue
		}
		if ctx.Err() != nil {
			for _, rest := range phases[i:] {
				if !gone[rest.host] {
					notRun++
				}
			}
			break
		}
		total, errs := p.total, len(p.errs)
		if ph.run(&p) {
			break
		}
		if !ran[ph.host] && p.total == total && onlyDNSNotFound(p.errs[errs:]) {
			gone[ph.host] = true
			if ph.host != bedrockHost {
				p.errs = p.errs[:errs]
			}
		}
		ran[ph.host] = true
	}
	p.foldStop(ctx, "bedrock", notRun)
	return p.resultLateNXDOMAIN(st, acct, region)
}

// onlyDNSNotFound reports whether errs is not empty and every one is an
// NXDOMAIN.
func onlyDNSNotFound(errs phaseErrs) bool {
	for _, f := range errs {
		if !isDNSNotFound(f.err) {
			return false
		}
	}
	return len(errs) > 0
}

// bedrockTags reads a row's tags with bedrock's ListTagsForResource, sent
// the row's NativeID: every type read through it is stored under the ARN its
// list element carries, and the op's TaggableResourcesArn pattern accepts the
// guardrail, automated-reasoning-policy, prompt-router,
// application-inference-profile, custom-model, imported-model,
// provisioned-model and custom-model-deployment ARNs. The op answers with
// every tag at once (no page token); ResourceNotFoundException is the only
// not-found code its model lists. A ManagedByProvider row (an AWS default
// prompt router, a system-defined inference profile) is not read: the Service
// Reference lists neither as a resource of the op, a default router's
// default-prompt-router ARN does not fit the pattern, and a rejection would
// fail every scan.
func bedrockTags(client bedrockAPI) rowTagReader {
	return rowTagReader{
		op:       bedrockListTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return r.TagsJSON, nil
			}
			out, err := client.ListTagsForResource(ctx, &bedrock.ListTagsForResourceInput{ResourceARN: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t bedrocktypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// bedrockAgentTags reads a row's tags with bedrockagent's
// ListTagsForResource, sent the row's NativeID. Agents, agent aliases and
// knowledge bases are stored under an ARN built in the Service Reference's
// format from their ids (their list elements carry none; GetKnowledgeBase's
// KnowledgeBaseArn has the same form), flows, flow aliases and prompts under
// the ARN their element carries; the op's TaggableResourcesArn pattern
// accepts all six. The op answers with every tag at once;
// ResourceNotFoundException is the only not-found code its model lists.
func bedrockAgentTags(client bedrockAgentAPI) rowTagReader {
	return bedrockAgentTagsAt(client, func(r *store.Resource) string { return r.NativeID })
}

// bedrockAgentTagsAt is bedrockAgentTags sent arnOf(row) instead of the
// row's NativeID.
func bedrockAgentTagsAt(client bedrockAgentAPI, arnOf func(*store.Resource) string) rowTagReader {
	return rowTagReader{
		op:       bedrockAgentListTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &bedrockagent.ListTagsForResourceInput{ResourceArn: sdkaws.String(arnOf(r))})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// bedrockDATags reads a row's tags with bedrockdataautomation's
// ListTagsForResource, sent the row's NativeID, the ARN the blueprint,
// project or library list element carries, which the op's
// TaggableResourceArn pattern accepts. The op answers with every tag at once;
// ResourceNotFoundException is the only not-found code its model lists. A
// ManagedByProvider row (a SERVICE-owned sample blueprint) is not read: its
// ARN names the "aws" account, which the pattern rejects.
func bedrockDATags(client bedrockDataAutomationAPI) rowTagReader {
	return rowTagReader{
		op:       bedrockDAListTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return r.TagsJSON, nil
			}
			out, err := client.ListTagsForResource(ctx, &bda.ListTagsForResourceInput{ResourceARN: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t bdatypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}
