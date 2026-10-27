package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagent"
	agenttypes "github.com/aws/aws-sdk-go-v2/service/bedrockagent/types"
	bda "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation"
	bdatypes "github.com/aws/aws-sdk-go-v2/service/bedrockdataautomation/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/store"
)

func brARN(kind, name string) string {
	return fmt.Sprintf("arn:aws:bedrock:%s:%s:%s/%s", testRegion, testAccountID, kind, name)
}

// The rows the bedrock fixtures list without a tag read: AWS-owned or not a
// resource of any tag op.
var (
	brDefaultRouterARN  = brARN("default-prompt-router", "anthropic.claude:1")
	brSystemProfileARN  = brARN("inference-profile", "us.anthropic.claude")
	brFoundationARN     = "arn:aws:bedrock:" + testRegion + "::foundation-model/anthropic.claude-v2"
	brEndpointARN       = "arn:aws:sagemaker:" + testRegion + ":" + testAccountID + ":endpoint/ep"
	brSampleBlueprintAR = "arn:aws:bedrock:" + testRegion + ":aws:blueprint/sample"
)

// brUntaggedRows is how many rows brHandlers lists besides the tagged
// fixtures: the five above.
const brUntaggedRows = 5

// brTagged is one type the tag reads cover, the tag op its rows are sent to,
// and the NativeIDs of its two fixture resources: "tagged" (first page, or
// under the "tagged" parent) and "bare".
type brTagged struct {
	typ, tagOp         string
	taggedARN, bareARN string
}

func brTaggedTypes() []brTagged {
	two := func(typ, tagOp string, arn func(n string) string) brTagged {
		return brTagged{typ: typ, tagOp: tagOp, taggedARN: arn("tagged"), bareARN: arn("bare")}
	}
	kind := func(k string) func(string) string { return func(n string) string { return brARN(k, n) } }
	return []brTagged{
		two(TypeBedrockGuardrail, bedrockListTagsOp, kind("guardrail")),
		two(TypeBedrockAutomatedReasoningPolicy, bedrockListTagsOp, kind("automated-reasoning-policy")),
		two(TypeBedrockIntelligentPromptRouter, bedrockListTagsOp, kind("prompt-router")),
		two(TypeBedrockApplicationInferenceProfile, bedrockListTagsOp, kind("application-inference-profile")),
		two(TypeBedrockCustomModel, bedrockListTagsOp, kind("custom-model")),
		two(TypeBedrockImportedModel, bedrockListTagsOp, kind("imported-model")),
		two(TypeBedrockProvisionedModel, bedrockListTagsOp, kind("provisioned-model")),
		two(TypeBedrockCustomModelDeployment, bedrockListTagsOp, kind("custom-model-deployment")),
		two(TypeBedrockAgent, bedrockAgentListTagsOp, func(n string) string { return bedrockAgentARN("aws", testRegion, testAccountID, n) }),
		two(TypeBedrockAgentAlias, bedrockAgentListTagsOp, func(n string) string { return bedrockAgentAliasARN("aws", testRegion, testAccountID, n, n) }),
		two(TypeBedrockKnowledgeBase, bedrockAgentListTagsOp, func(n string) string { return bedrockKBARN("aws", testRegion, testAccountID, n) }),
		two(TypeBedrockFlow, bedrockAgentListTagsOp, kind("flow")),
		two(TypeBedrockFlowAlias, bedrockAgentListTagsOp, func(n string) string { return brARN("flow", n+"/alias/"+n) }),
		two(TypeBedrockPrompt, bedrockAgentListTagsOp, kind("prompt")),
		two(TypeBedrockBlueprint, bedrockDAListTagsOp, kind("blueprint")),
		two(TypeBedrockDataAutomationProject, bedrockDAListTagsOp, kind("data-automation-project")),
		two(TypeBedrockDataAutomationLibrary, bedrockDAListTagsOp, kind("data-automation-library")),
	}
}

// brTagFixtures gives every tagged fixture the tags {"type": <its type>} and
// returns the "<tag op> <ARN>" pairs the scan must send, sorted.
func brTagFixtures(c *bacCalls) []string {
	var sent []string
	for _, f := range brTaggedTypes() {
		c.tags[f.taggedARN] = map[string]string{"type": f.typ}
		sent = append(sent, f.tagOp+" "+f.taggedARN, f.tagOp+" "+f.bareARN)
	}
	slices.Sort(sent)
	return sent
}

// brListTags answers the three packages' ListTagsForResource from c.tags or
// c.errs, keyed by the ARN sent, and records "<tag op> <ARN>" in c.tagARNs.
func brListTags(c *bacCalls) func(any) (any, error) {
	return func(in any) (any, error) {
		var op, arn string
		switch req := in.(type) {
		case *bedrock.ListTagsForResourceInput:
			op, arn = bedrockListTagsOp, sv(req.ResourceARN)
		case *bedrockagent.ListTagsForResourceInput:
			op, arn = bedrockAgentListTagsOp, sv(req.ResourceArn)
		case *bda.ListTagsForResourceInput:
			op, arn = bedrockDAListTagsOp, sv(req.ResourceARN)
		default:
			return nil, fmt.Errorf("unexpected ListTagsForResource input %T", in)
		}
		c.mu.Lock()
		c.tagARNs = append(c.tagARNs, op+" "+arn)
		c.mu.Unlock()
		if err := c.errs[arn]; err != nil {
			return nil, err
		}
		tags := c.tags[arn]
		keys := slices.Sorted(maps.Keys(tags))
		switch in.(type) {
		case *bedrock.ListTagsForResourceInput:
			out := &bedrock.ListTagsForResourceOutput{}
			for _, k := range keys {
				out.Tags = append(out.Tags, bedrocktypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(tags[k])})
			}
			return out, nil
		case *bda.ListTagsForResourceInput:
			out := &bda.ListTagsForResourceOutput{}
			for _, k := range keys {
				out.Tags = append(out.Tags, bdatypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(tags[k])})
			}
			return out, nil
		}
		return &bedrockagent.ListTagsForResourceOutput{Tags: tags}, nil
	}
}

// brPage serves "tagged" on a listing's first page and "bare" on its second.
func brPage(c *bacCalls, op string, out func(name string, next *string) any) func(any) (any, error) {
	return bacTwoPages(c, op, out)
}

// brChild serves, for a parent named in field, one child named after it.
func brChild(c *bacCalls, op, field string, out func(parent string) any) func(any) (any, error) {
	return c.answer(op, func(in any) any { return out(sv(bacField[*string](in, field))) })
}

// brEmpty answers op with an empty out.
func brEmpty(c *bacCalls, op string, out any) func(any) (any, error) {
	return c.answer(op, func(any) any { return out })
}

// brHandlers answers every op the bedrock scan calls: two resources for each
// tagged type (brTaggedTypes), the untagged rows above, and nothing for the
// other listings. A knowledge base's GetKnowledgeBase fails with
// c.errs["get "+id] when set.
func brHandlers(c *bacCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListTagsForResource": brListTags(c),
		"ListGuardrails": func(in any) (any, error) {
			if in.(*bedrock.ListGuardrailsInput).GuardrailIdentifier != nil {
				c.called("ListGuardrails", in)
				return &bedrock.ListGuardrailsOutput{}, nil
			}
			return brPage(c, "ListGuardrails", func(n string, next *string) any {
				return &bedrock.ListGuardrailsOutput{NextToken: next, Guardrails: []bedrocktypes.GuardrailSummary{
					{Arn: sdkaws.String(brARN("guardrail", n)), Id: sdkaws.String(n), Name: sdkaws.String(n)},
				}}
			})(in)
		},
		"ListAutomatedReasoningPolicies": func(in any) (any, error) {
			if in.(*bedrock.ListAutomatedReasoningPoliciesInput).PolicyArn != nil {
				c.called("ListAutomatedReasoningPolicies", in)
				return &bedrock.ListAutomatedReasoningPoliciesOutput{}, nil
			}
			return brPage(c, "ListAutomatedReasoningPolicies", func(n string, next *string) any {
				return &bedrock.ListAutomatedReasoningPoliciesOutput{NextToken: next, AutomatedReasoningPolicySummaries: []bedrocktypes.AutomatedReasoningPolicySummary{
					{PolicyArn: sdkaws.String(brARN("automated-reasoning-policy", n)), PolicyId: sdkaws.String(n), Name: sdkaws.String(n)},
				}}
			})(in)
		},
		"ListPromptRouters": brPage(c, "ListPromptRouters", func(n string, next *string) any {
			out := &bedrock.ListPromptRoutersOutput{NextToken: next, PromptRouterSummaries: []bedrocktypes.PromptRouterSummary{
				{PromptRouterArn: sdkaws.String(brARN("prompt-router", n)), PromptRouterName: sdkaws.String(n), Type: bedrocktypes.PromptRouterTypeCustom},
			}}
			if n == "bare" {
				out.PromptRouterSummaries = append(out.PromptRouterSummaries, bedrocktypes.PromptRouterSummary{
					PromptRouterArn: sdkaws.String(brDefaultRouterARN), PromptRouterName: sdkaws.String("default"), Type: bedrocktypes.PromptRouterTypeDefault,
				})
			}
			return out
		}),
		"ListInferenceProfiles": brPage(c, "ListInferenceProfiles", func(n string, next *string) any {
			out := &bedrock.ListInferenceProfilesOutput{NextToken: next, InferenceProfileSummaries: []bedrocktypes.InferenceProfileSummary{
				{InferenceProfileArn: sdkaws.String(brARN("application-inference-profile", n)), InferenceProfileId: sdkaws.String(n), Type: bedrocktypes.InferenceProfileTypeApplication},
			}}
			if n == "bare" {
				out.InferenceProfileSummaries = append(out.InferenceProfileSummaries, bedrocktypes.InferenceProfileSummary{
					InferenceProfileArn: sdkaws.String(brSystemProfileARN), InferenceProfileId: sdkaws.String("us.anthropic.claude"), Type: bedrocktypes.InferenceProfileTypeSystemDefined,
				})
			}
			return out
		}),
		"ListEnforcedGuardrailsConfiguration": brEmpty(c, "ListEnforcedGuardrailsConfiguration", &bedrock.ListEnforcedGuardrailsConfigurationOutput{}),
		"ListFoundationModels": brEmpty(c, "ListFoundationModels", &bedrock.ListFoundationModelsOutput{ModelSummaries: []bedrocktypes.FoundationModelSummary{
			{ModelArn: sdkaws.String(brFoundationARN), ModelId: sdkaws.String("anthropic.claude-v2")},
		}}),
		"ListCustomModels": brPage(c, "ListCustomModels", func(n string, next *string) any {
			return &bedrock.ListCustomModelsOutput{NextToken: next, ModelSummaries: []bedrocktypes.CustomModelSummary{
				{ModelArn: sdkaws.String(brARN("custom-model", n)), ModelName: sdkaws.String(n), ModelStatus: bedrocktypes.ModelStatusActive},
			}}
		}),
		"ListImportedModels": brPage(c, "ListImportedModels", func(n string, next *string) any {
			return &bedrock.ListImportedModelsOutput{NextToken: next, ModelSummaries: []bedrocktypes.ImportedModelSummary{
				{ModelArn: sdkaws.String(brARN("imported-model", n)), ModelName: sdkaws.String(n)},
			}}
		}),
		"ListProvisionedModelThroughputs": brPage(c, "ListProvisionedModelThroughputs", func(n string, next *string) any {
			return &bedrock.ListProvisionedModelThroughputsOutput{NextToken: next, ProvisionedModelSummaries: []bedrocktypes.ProvisionedModelSummary{
				{ProvisionedModelArn: sdkaws.String(brARN("provisioned-model", n)), ProvisionedModelName: sdkaws.String(n)},
			}}
		}),
		"ListCustomModelDeployments": brPage(c, "ListCustomModelDeployments", func(n string, next *string) any {
			return &bedrock.ListCustomModelDeploymentsOutput{NextToken: next, ModelDeploymentSummaries: []bedrocktypes.CustomModelDeploymentSummary{
				{CustomModelDeploymentArn: sdkaws.String(brARN("custom-model-deployment", n)), CustomModelDeploymentName: sdkaws.String(n)},
			}}
		}),
		"ListMarketplaceModelEndpoints": brEmpty(c, "ListMarketplaceModelEndpoints", &bedrock.ListMarketplaceModelEndpointsOutput{MarketplaceModelEndpoints: []bedrocktypes.MarketplaceModelEndpointSummary{
			{EndpointArn: sdkaws.String(brEndpointARN)},
		}}),
		"ListAutomatedReasoningPolicyTestCases": brEmpty(c, "ListAutomatedReasoningPolicyTestCases", &bedrock.ListAutomatedReasoningPolicyTestCasesOutput{}),
		"ListAgents": brPage(c, "ListAgents", func(n string, next *string) any {
			return &bedrockagent.ListAgentsOutput{NextToken: next, AgentSummaries: []agenttypes.AgentSummary{{AgentId: sdkaws.String(n), AgentName: sdkaws.String(n)}}}
		}),
		"ListAgentAliases": brChild(c, "ListAgentAliases", "AgentId", func(agent string) any {
			return &bedrockagent.ListAgentAliasesOutput{AgentAliasSummaries: []agenttypes.AgentAliasSummary{{AgentAliasId: sdkaws.String(agent), AgentAliasName: sdkaws.String(agent)}}}
		}),
		"ListKnowledgeBases": brPage(c, "ListKnowledgeBases", func(n string, next *string) any {
			return &bedrockagent.ListKnowledgeBasesOutput{NextToken: next, KnowledgeBaseSummaries: []agenttypes.KnowledgeBaseSummary{{KnowledgeBaseId: sdkaws.String(n), Name: sdkaws.String(n)}}}
		}),
		"GetKnowledgeBase": func(in any) (any, error) {
			id := sv(in.(*bedrockagent.GetKnowledgeBaseInput).KnowledgeBaseId)
			c.called("GetKnowledgeBase", in)
			if err := c.errs["get "+id]; err != nil {
				return nil, err
			}
			return &bedrockagent.GetKnowledgeBaseOutput{KnowledgeBase: &agenttypes.KnowledgeBase{
				KnowledgeBaseArn: sdkaws.String(bedrockKBARN("aws", testRegion, testAccountID, id)), KnowledgeBaseId: sdkaws.String(id),
				Name: sdkaws.String(id), RoleArn: sdkaws.String("arn:aws:iam::" + testAccountID + ":role/kb-" + id),
			}}, nil
		},
		"ListDataSources": brEmpty(c, "ListDataSources", &bedrockagent.ListDataSourcesOutput{}),
		"ListFlows": brPage(c, "ListFlows", func(n string, next *string) any {
			return &bedrockagent.ListFlowsOutput{NextToken: next, FlowSummaries: []agenttypes.FlowSummary{{Arn: sdkaws.String(brARN("flow", n)), Id: sdkaws.String(n), Name: sdkaws.String(n)}}}
		}),
		"ListFlowAliases": brChild(c, "ListFlowAliases", "FlowIdentifier", func(flow string) any {
			return &bedrockagent.ListFlowAliasesOutput{FlowAliasSummaries: []agenttypes.FlowAliasSummary{
				{Arn: sdkaws.String(brARN("flow", flow+"/alias/"+flow)), Id: sdkaws.String(flow), Name: sdkaws.String(flow), FlowId: sdkaws.String(flow)},
			}}
		}),
		"ListFlowVersions": brEmpty(c, "ListFlowVersions", &bedrockagent.ListFlowVersionsOutput{}),
		"ListPrompts": func(in any) (any, error) {
			if in.(*bedrockagent.ListPromptsInput).PromptIdentifier != nil {
				c.called("ListPrompts", in)
				return &bedrockagent.ListPromptsOutput{}, nil
			}
			return brPage(c, "ListPrompts", func(n string, next *string) any {
				return &bedrockagent.ListPromptsOutput{NextToken: next, PromptSummaries: []agenttypes.PromptSummary{{Arn: sdkaws.String(brARN("prompt", n)), Id: sdkaws.String(n), Name: sdkaws.String(n)}}}
			})(in)
		},
		"ListAgentVersions":       brEmpty(c, "ListAgentVersions", &bedrockagent.ListAgentVersionsOutput{}),
		"ListAgentActionGroups":   brEmpty(c, "ListAgentActionGroups", &bedrockagent.ListAgentActionGroupsOutput{}),
		"ListAgentKnowledgeBases": brEmpty(c, "ListAgentKnowledgeBases", &bedrockagent.ListAgentKnowledgeBasesOutput{}),
		"ListAgentCollaborators":  brEmpty(c, "ListAgentCollaborators", &bedrockagent.ListAgentCollaboratorsOutput{}),
		"ListBlueprints": func(in any) (any, error) {
			if in.(*bda.ListBlueprintsInput).ResourceOwner == bdatypes.ResourceOwnerService {
				c.called("ListBlueprints", in)
				return &bda.ListBlueprintsOutput{Blueprints: []bdatypes.BlueprintSummary{{BlueprintArn: sdkaws.String(brSampleBlueprintAR), BlueprintName: sdkaws.String("sample")}}}, nil
			}
			return brPage(c, "ListBlueprints", func(n string, next *string) any {
				return &bda.ListBlueprintsOutput{NextToken: next, Blueprints: []bdatypes.BlueprintSummary{{BlueprintArn: sdkaws.String(brARN("blueprint", n)), BlueprintName: sdkaws.String(n)}}}
			})(in)
		},
		"ListDataAutomationProjects": brPage(c, "ListDataAutomationProjects", func(n string, next *string) any {
			return &bda.ListDataAutomationProjectsOutput{NextToken: next, Projects: []bdatypes.DataAutomationProjectSummary{{ProjectArn: sdkaws.String(brARN("data-automation-project", n)), ProjectName: sdkaws.String(n)}}}
		}),
		"ListDataAutomationLibraries": brPage(c, "ListDataAutomationLibraries", func(n string, next *string) any {
			return &bda.ListDataAutomationLibrariesOutput{NextToken: next, Libraries: []bdatypes.DataAutomationLibrarySummary{{LibraryArn: sdkaws.String(brARN("data-automation-library", n)), LibraryName: sdkaws.String(n)}}}
		}),
	}
}

// brScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func brScan(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	return brScanCtx(context.Background(), t, st, handlers, apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:Op"))
}

func brScanCtx(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error), unanswered error) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, unanswered)
	return scanBedrock(ctx, acct, testRegion, st, testScanID)
}

// brRecordParents wraps each child listing op in handlers to record the
// value of the parent field fields names for it, skipping calls without one
// (the parent listing that shares the op). The returned func reads the record, sorted.
func brRecordParents(handlers map[string]func(any) (any, error), fields map[string]string) func() map[string][]string {
	var mu sync.Mutex
	sent := map[string][]string{}
	for op, field := range fields {
		next := handlers[op]
		handlers[op] = func(in any) (any, error) {
			if parent := bacField[*string](in, field); parent != nil {
				mu.Lock()
				sent[op] = append(sent[op], *parent)
				mu.Unlock()
			}
			return next(in)
		}
	}
	return func() map[string][]string {
		mu.Lock()
		defer mu.Unlock()
		for _, v := range sent {
			slices.Sort(v)
		}
		return sent
	}
}

// Each child listing is sent every parent its parent phase stored, both
// pages of them, in the form the child op keys on.
func TestScanBedrock_ChildPhasesGetEveryStoredParent(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	brTagFixtures(calls)
	handlers := brHandlers(calls)
	ids := []string{"bare", "tagged"}
	policyARNs := []string{brARN("automated-reasoning-policy", "bare"), brARN("automated-reasoning-policy", "tagged")}
	fields := map[string]string{
		"ListGuardrails": "GuardrailIdentifier", "ListAutomatedReasoningPolicies": "PolicyArn",
		"ListAutomatedReasoningPolicyTestCases": "PolicyArn", "ListAgentAliases": "AgentId", "ListAgentVersions": "AgentId",
		"ListAgentActionGroups": "AgentId", "ListAgentKnowledgeBases": "AgentId", "ListAgentCollaborators": "AgentId",
		"ListDataSources": "KnowledgeBaseId", "ListFlowAliases": "FlowIdentifier", "ListFlowVersions": "FlowIdentifier",
		"ListPrompts": "PromptIdentifier",
	}
	sent := brRecordParents(handlers, fields)

	if _, _, err := brScan(t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	got := sent()
	for op := range fields {
		want := ids
		if op == "ListAutomatedReasoningPolicies" || op == "ListAutomatedReasoningPolicyTestCases" {
			want = policyARNs
		}
		if !slices.Equal(got[op], want) {
			t.Errorf("%s parents = %v; want %v", op, got[op], want)
		}
	}
}

// With every listing empty the scan stores nothing and reports no error or
// warning, and no tag read or child listing is sent.
func TestScanBedrock_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newBACCalls()
	handlers := map[string]func(any) (any, error){"ListTagsForResource": brListTags(calls)}
	for op, out := range map[string]any{
		"ListGuardrails": &bedrock.ListGuardrailsOutput{}, "ListAutomatedReasoningPolicies": &bedrock.ListAutomatedReasoningPoliciesOutput{},
		"ListPromptRouters": &bedrock.ListPromptRoutersOutput{}, "ListInferenceProfiles": &bedrock.ListInferenceProfilesOutput{},
		"ListEnforcedGuardrailsConfiguration": &bedrock.ListEnforcedGuardrailsConfigurationOutput{}, "ListFoundationModels": &bedrock.ListFoundationModelsOutput{},
		"ListCustomModels": &bedrock.ListCustomModelsOutput{}, "ListImportedModels": &bedrock.ListImportedModelsOutput{},
		"ListProvisionedModelThroughputs": &bedrock.ListProvisionedModelThroughputsOutput{}, "ListCustomModelDeployments": &bedrock.ListCustomModelDeploymentsOutput{},
		"ListMarketplaceModelEndpoints": &bedrock.ListMarketplaceModelEndpointsOutput{}, "ListAgents": &bedrockagent.ListAgentsOutput{},
		"ListKnowledgeBases": &bedrockagent.ListKnowledgeBasesOutput{}, "ListFlows": &bedrockagent.ListFlowsOutput{},
		"ListPrompts": &bedrockagent.ListPromptsOutput{}, "ListBlueprints": &bda.ListBlueprintsOutput{},
		"ListDataAutomationProjects": &bda.ListDataAutomationProjectsOutput{}, "ListDataAutomationLibraries": &bda.ListDataAutomationLibrariesOutput{},
	} {
		handlers[op] = brEmpty(calls, op, out)
	}

	total, inserted, err := brScan(t, st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 || *warns != 0 || len(calls.tagARNs) != 0 {
		t.Errorf("total, inserted, warnings, tag reads = %d, %d, %d, %d; want all 0", total, inserted, *warns, len(calls.tagARNs))
	}
}

// Every tagged type stores the tags its tag op returns for the resource's
// own ARN, and nil for a resource without tags; the AWS-owned rows and the
// untagged types are never sent to a tag op. Both pages of every listing are
// stored, each listing asks for its model's page cap, and the scan reports
// no error or warning.
func TestScanBedrock_StoresTagsOfEveryTaggedType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newBACCalls()
	wantSent := brTagFixtures(calls)

	total, inserted, err := brScan(t, st, brHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := len(wantSent) + brUntaggedRows; total != want || inserted != want {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, want, want)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, f := range brTaggedTypes() {
		rows := rowsByNativeID(t, st, f.typ)
		tagged, ok := rows[f.taggedARN]
		if !ok {
			t.Fatalf("%s tagged row %s missing; have %v", f.typ, f.taggedARN, slices.Collect(maps.Keys(rows)))
		}
		assertTagsMap(t, tagged.TagsJSON, map[string]string{"type": f.typ})
		bare, ok := rows[f.bareARN]
		if !ok {
			t.Fatalf("%s bare row %s missing", f.typ, f.bareARN)
		}
		if bare.TagsJSON != nil {
			t.Errorf("%s bare TagsJSON = %q; want nil", f.typ, *bare.TagsJSON)
		}
		if tagged.ManagedByProvider || bare.ManagedByProvider {
			t.Errorf("%s ManagedByProvider = %v, %v; want false", f.typ, tagged.ManagedByProvider, bare.ManagedByProvider)
		}
	}
	for _, c := range []struct{ typ, arn string }{
		{TypeBedrockIntelligentPromptRouter, brDefaultRouterARN},
		{TypeBedrockInferenceProfile, brSystemProfileARN},
		{TypeBedrockFoundationModel, brFoundationARN},
		{TypeBedrockMarketplaceModelEndpoint, brEndpointARN},
		{TypeBedrockBlueprint, brSampleBlueprintAR},
	} {
		r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
		if !ok {
			t.Errorf("%s %s missing", c.typ, c.arn)
			continue
		}
		if wantManaged := c.typ != TypeBedrockMarketplaceModelEndpoint; r.ManagedByProvider != wantManaged {
			t.Errorf("%s %s ManagedByProvider = %v; want %v", c.typ, c.arn, r.ManagedByProvider, wantManaged)
		}
	}
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantSent) {
		t.Errorf("tag reads = %v\nwant %v", calls.tagARNs, wantSent)
	}
	wantCaps := []string{
		"ListGuardrails", "ListAutomatedReasoningPolicies", "ListPromptRouters", "ListInferenceProfiles",
		"ListCustomModels", "ListImportedModels", "ListProvisionedModelThroughputs", "ListCustomModelDeployments",
		"ListMarketplaceModelEndpoints", "ListAgents", "ListAgentAliases", "ListKnowledgeBases", "ListFlows",
		"ListFlowAliases", "ListPrompts", "ListBlueprints", "ListDataAutomationProjects", "ListDataAutomationLibraries",
		"ListDataSources", "ListFlowVersions", "ListAgentVersions", "ListAgentActionGroups", "ListAgentKnowledgeBases", "ListAgentCollaborators",
		"ListAutomatedReasoningPolicyTestCases",
	}
	for _, op := range wantCaps {
		got := calls.maxResults[op]
		if len(got) == 0 {
			t.Errorf("%s never sent MaxResults", op)
		}
		for _, n := range got {
			if n != 1000 {
				t.Errorf("%s MaxResults = %d; want 1000", op, n)
			}
		}
	}
	if sent := slices.Sorted(maps.Keys(calls.maxResults)); !slices.Equal(sent, slices.Sorted(slices.Values(wantCaps))) {
		t.Errorf("ops that sent MaxResults = %v; want %v", sent, slices.Sorted(slices.Values(wantCaps)))
	}
}

// A knowledge base's row is its GetKnowledgeBase body, which carries the
// RoleArn the list element lacks; the tag read is sent the NativeID, which
// is the body's KnowledgeBaseArn.
func TestScanBedrock_KnowledgeBaseRowIsTheGetBody(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	brTagFixtures(calls)
	if _, _, err := brScan(t, st, brHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r := rowsByNativeID(t, st, TypeBedrockKnowledgeBase)[bedrockKBARN("aws", testRegion, testAccountID, "tagged")]
	want := `"RoleArn":"arn:aws:iam::` + testAccountID + `:role/kb-tagged"`
	if !strings.Contains(r.AttributesJSON, want) || !strings.Contains(r.AttributesJSON, `"KnowledgeBaseArn":"`+r.NativeID+`"`) {
		t.Errorf("attributes = %s; want the GetKnowledgeBase body, with %s and KnowledgeBaseArn = NativeID", r.AttributesJSON, want)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanBedrock_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	brTagFixtures(calls)
	if _, _, err := brScan(t, st, brHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := brScan(t, st, brHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 2*len(brTaggedTypes())+brUntaggedRows || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, 2*len(brTaggedTypes())+brUntaggedRows)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under the tag op, a throttle after retries is a
// transient error, any other code a hard one. Each of the three tag ops is
// checked, on a parent type: a parent the tag read no longer finds is
// dropped, and its children are never listed.
func TestScanBedrock_TagReadFailures(t *testing.T) {
	for _, target := range []struct {
		typ, tagOp, arn, childOp, childField string
	}{
		{TypeBedrockAutomatedReasoningPolicy, bedrockListTagsOp, brARN("automated-reasoning-policy", "tagged"), "ListAutomatedReasoningPolicies", "PolicyArn"},
		{TypeBedrockAgent, bedrockAgentListTagsOp, bedrockAgentARN("aws", testRegion, testAccountID, "tagged"), "ListAgentAliases", "AgentId"},
		{TypeBedrockDataAutomationProject, bedrockDAListTagsOp, brARN("data-automation-project", "tagged"), "", ""},
	} {
		t.Run(target.typ, func(t *testing.T) {
			cases := []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:ListTagsForResource"), true, outcomeOK, 1},
				{"not found", apiErr("ResourceNotFoundException", "gone"), false, outcomeOK, 0},
				{"other error", apiErr("ValidationException", "bad"), true, outcomeError, 0},
				{"transient", apiErr("ThrottlingException", "Rate exceeded"), true, outcomeTransient, 0},
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, target.tagOp)
					calls := newBACCalls()
					brTagFixtures(calls)
					calls.errs[target.arn] = c.err
					handlers := brHandlers(calls)
					var childParents []string
					if target.childOp != "" {
						next := handlers[target.childOp]
						handlers[target.childOp] = func(in any) (any, error) {
							if parent := bacField[*string](in, target.childField); parent != nil {
								calls.mu.Lock()
								childParents = append(childParents, *parent)
								calls.mu.Unlock()
							}
							return next(in)
						}
					}

					_, _, err := brScan(t, st, handlers)
					if c.outcome == outcomeOK && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != outcomeOK {
						if got := classifyServiceError(err); got != c.outcome {
							t.Errorf("classifyServiceError = %v; want %v (err %v)", got, c.outcome, err)
						}
						assertAPICode(t, err, apiCode(c.err))
					}
					r, ok := rowsByNativeID(t, st, target.typ)[target.arn]
					if ok != c.kept {
						t.Fatalf("%s row stored = %v; want %v", target.typ, ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if target.childOp != "" {
						parent := "tagged"
						if target.typ == TypeBedrockAutomatedReasoningPolicy {
							parent = target.arn
						}
						if listed := slices.Contains(childParents, parent); listed != c.kept {
							t.Errorf("%s of the parent listed = %v; want %v (sent %v)", target.childOp, listed, c.kept, childParents)
						}
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", target.tagOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// outcomeOK names "no error" in the tables above; no dispatch outcome uses
// it.
const outcomeOK serviceOutcome = -1

func apiCode(err error) string {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return ""
	}
	return ae.ErrorCode()
}

// A throttled tag read beside a hard tag-read failure is demoted to one
// warning, so the hard error decides the outcome.
func TestScanBedrock_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, bedrockAgentListTagsOp)
	calls := newBACCalls()
	brTagFixtures(calls)
	calls.errs[brARN("flow", "tagged")] = apiErr("ThrottlingException", "Rate exceeded")
	calls.errs[brARN("guardrail", "tagged")] = apiErr("ValidationException", "bad")

	_, _, err := brScan(t, st, brHandlers(calls))
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
	}
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", bedrockAgentListTagsOp, *warns)
	}
}

// A failed GetKnowledgeBase keeps the list element as the row, tags still
// read, unless the knowledge base is gone: a denial warns once, a throttle
// after retries is a transient error, any other code a hard one. A
// knowledge base the Get no longer finds is dropped, and its tags are not
// read.
func TestScanBedrock_KnowledgeBaseGetFailures(t *testing.T) {
	arn := bedrockKBARN("aws", testRegion, testAccountID, "tagged")
	for _, c := range []struct {
		name      string
		err       error
		kept      bool
		outcome   serviceOutcome
		wantWarns int
	}{
		{"denied", apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:GetKnowledgeBase"), true, outcomeOK, 1},
		{"not found", apiErr("ResourceNotFoundException", "gone"), false, outcomeOK, 0},
		{"other error", apiErr("ValidationException", "bad"), true, outcomeError, 0},
		{"transient", apiErr("ThrottlingException", "Rate exceeded"), true, outcomeTransient, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, bedrockGetKBOp)
			calls := newBACCalls()
			brTagFixtures(calls)
			calls.errs["get tagged"] = c.err

			_, _, err := brScan(t, st, brHandlers(calls))
			if c.outcome == outcomeOK && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != outcomeOK {
				if got := classifyServiceError(err); got != c.outcome {
					t.Errorf("classifyServiceError = %v; want %v (err %v)", got, c.outcome, err)
				}
				assertAPICode(t, err, apiCode(c.err))
			}
			r, ok := rowsByNativeID(t, st, TypeBedrockKnowledgeBase)[arn]
			if ok != c.kept {
				t.Fatalf("knowledge base stored = %v; want %v", ok, c.kept)
			}
			if ok {
				if strings.Contains(r.AttributesJSON, "RoleArn") || !strings.Contains(r.AttributesJSON, `"KnowledgeBaseId":"tagged"`) {
					t.Errorf("attributes = %s; want the list element", r.AttributesJSON)
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeBedrockKnowledgeBase})
			}
			if sent := slices.Contains(calls.tagARNs, bedrockAgentListTagsOp+" "+arn); sent != c.kept {
				t.Errorf("tag read sent = %v; want %v", sent, c.kept)
			}
			if _, ok := rowsByNativeID(t, st, TypeBedrockKnowledgeBase)[bedrockKBARN("aws", testRegion, testAccountID, "bare")]; !ok {
				t.Error("the other knowledge base is missing")
			}
			if len(*warnings) != c.wantWarns {
				t.Fatalf("%s warnings = %v; want %d", bedrockGetKBOp, *warnings, c.wantWarns)
			}
		})
	}
}

// A throttled GetKnowledgeBase beside a hard error elsewhere is demoted to
// one warning.
func TestScanBedrock_KnowledgeBaseGetThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, bedrockGetKBOp)
	calls := newBACCalls()
	brTagFixtures(calls)
	calls.errs["get tagged"] = apiErr("ThrottlingException", "Rate exceeded")
	calls.errs[brARN("guardrail", "tagged")] = apiErr("ValidationException", "bad")

	_, _, err := brScan(t, st, brHandlers(calls))
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", bedrockGetKBOp, *warns)
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost: not the tag denial, not the Get denial.
func TestScanBedrock_FailedWriteStopsWithoutWarning(t *testing.T) {
	tagDenied := apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:ListTagsForResource")
	for _, tc := range []struct {
		typ, deniedARN, getDenied string
		calls                     map[string]int
	}{
		{TypeBedrockGuardrail, brARN("guardrail", "tagged"), "", map[string]int{"ListAutomatedReasoningPolicies": 0}},
		{TypeBedrockAgentAlias, bedrockAgentAliasARN("aws", testRegion, testAccountID, "tagged", "tagged"), "", map[string]int{"ListKnowledgeBases": 0}},
		{TypeBedrockKnowledgeBase, bedrockKBARN("aws", testRegion, testAccountID, "tagged"), "tagged", map[string]int{"ListFlows": 0}},
		// The ACCOUNT blueprints' two pages, and the SERVICE listing never called.
		{TypeBedrockBlueprint, brARN("blueprint", "tagged"), "", map[string]int{"ListBlueprints": 2, "ListDataAutomationProjects": 0}},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			refuseStoreWrites(t, st, tc.typ)
			calls := newBACCalls()
			brTagFixtures(calls)
			calls.errs[tc.deniedARN] = tagDenied
			if tc.getDenied != "" {
				calls.errs["get "+tc.getDenied] = apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:GetKnowledgeBase")
			}

			_, _, err := brScan(t, st, brHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			for op, want := range tc.calls {
				if n := calls.count(op); n != want {
					t.Errorf("%s called %d times; want %d", op, n, want)
				}
			}
		})
	}
}

// A listing's failed page keeps the pages read before it, and no list
// failure stops the phases after it, on any host.
func TestScanBedrock_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	brTagFixtures(calls)
	handlers := brHandlers(calls)
	handlers["ListGuardrails"] = func(any) (any, error) { return nil, apiErr("ValidationException", "guardrails") }
	handlers["ListAgents"] = failOnCall(2, apiErr("InvalidParameterException", "page 2"), handlers["ListAgents"])
	handlers["ListBlueprints"] = func(any) (any, error) { return nil, apiErr("ConflictException", "blueprints") }
	handlers["ListAgentVersions"] = func(any) (any, error) { return nil, apiErr("ConflictException", "versions") }

	_, _, err := brScan(t, st, handlers)
	if calls.count("ListAutomatedReasoningPolicyTestCases") == 0 {
		t.Error("the test-case phase, after the failed agent-version phase, never ran")
	}
	for _, code := range []string{"ValidationException", "InvalidParameterException", "ConflictException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	r, ok := rowsByNativeID(t, st, TypeBedrockAgent)[bedrockAgentARN("aws", testRegion, testAccountID, "tagged")]
	if !ok {
		t.Fatal("agent from the page before the failure missing")
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeBedrockAgent})
	for _, c := range []struct{ typ, arn string }{
		{TypeBedrockAgentAlias, bedrockAgentAliasARN("aws", testRegion, testAccountID, "tagged", "tagged")},
		{TypeBedrockAutomatedReasoningPolicy, brARN("automated-reasoning-policy", "bare")},
		{TypeBedrockPrompt, brARN("prompt", "bare")},
		{TypeBedrockCustomModelDeployment, brARN("custom-model-deployment", "bare")},
		{TypeBedrockDataAutomationLibrary, brARN("data-automation-library", "bare")},
	} {
		if _, ok := rowsByNativeID(t, st, c.typ)[c.arn]; !ok {
			t.Errorf("%s %s missing after the earlier failures", c.typ, c.arn)
		}
	}
}

// A region-gap code on a listing's first page means the op is not offered
// here: no row, no error, no warning. After a page was read it is that
// page's failure, with the rows read before it stored: a denial code warns
// once, any other is returned.
func TestScanBedrock_RegionGapOnlyOnFirstPage(t *testing.T) {
	notFound404 := &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}}, Err: apiErr("UnknownError", "invalid character '<'")}
	for _, tc := range []struct {
		name, op, typ, arn string
		gap                error
		// firstWarns: the gap is also a real outage, so it warns.
		firstWarns bool
	}{
		{"models unknown operation", "ListCustomModels", TypeBedrockCustomModel, brARN("custom-model", "tagged"), apiErr("UnknownOperationException", "Unknown Operation"), false},
		{"models validation unknown operation", "ListImportedModels", TypeBedrockImportedModel, brARN("imported-model", "tagged"), apiErr("ValidationException", "Unknown operation"), false},
		{"models not enrolled", "ListProvisionedModelThroughputs", TypeBedrockProvisionedModel, brARN("provisioned-model", "tagged"), apiErr("AccessDeniedException", "Your account is not authorized to invoke this API operation."), false},
		{"models html 404", "ListCustomModelDeployments", TypeBedrockCustomModelDeployment, brARN("custom-model-deployment", "tagged"), notFound404, false},
		{"router not recognized", "ListPromptRouters", TypeBedrockIntelligentPromptRouter, brARN("prompt-router", "tagged"), apiErr("ValidationException", "This operation is not recognized"), false},
		{"AR feature gate", "ListAutomatedReasoningPolicies", TypeBedrockAutomatedReasoningPolicy, brARN("automated-reasoning-policy", "tagged"), apiErr("AccessDeniedException", "Your account is not authorized to invoke this API operation."), false},
		{"AR closed", "ListAutomatedReasoningPolicies", TypeBedrockAutomatedReasoningPolicy, brARN("automated-reasoning-policy", "tagged"), apiErr("AccessDeniedException", ""), false},
		{"flows 5xx", "ListFlows", TypeBedrockFlow, brARN("flow", "tagged"), apiErr("InternalServerException", "boom"), true},
		{"prompts unmodeled 5xx", "ListPrompts", TypeBedrockPrompt, brARN("prompt", "tagged"), apiErr("InternalServerErrorException", "boom"), true},
		{"prompts SCP", "ListPrompts", TypeBedrockPrompt, brARN("prompt", "tagged"), apiErr("AccessDeniedException", "User: x is not authorized to perform: y with an explicit deny in a service control policy"), false},
		{"data automation closed", "ListDataAutomationProjects", TypeBedrockDataAutomationProject, brARN("data-automation-project", "tagged"), apiErr("AccessDeniedException", ""), false},
		{"data automation not in region", "ListDataAutomationLibraries", TypeBedrockDataAutomationLibrary, brARN("data-automation-library", "tagged"), apiErr("AccessDeniedException", "Blueprint library is not supported in this region"), false},
	} {
		for _, call := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s call %d", tc.name, call), func(t *testing.T) {
				st := newTestStore(t)
				warns := countWarnings(st)
				calls := newBACCalls()
				brTagFixtures(calls)
				handlers := brHandlers(calls)
				handlers[tc.op] = failOnCall(call, tc.gap, handlers[tc.op])

				_, _, err := brScan(t, st, handlers)
				denial := isAccessDenied(tc.gap)
				switch {
				case call == 1 || denial:
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				case err == nil || classifyServiceError(err) == outcomeUnavailable:
					t.Fatalf("err = %v; want the later page's gap code returned", err)
				}
				wantWarns := 0
				if (call == 2 && denial) || (call == 1 && tc.firstWarns) {
					wantWarns = 1
				}
				if *warns != wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, wantWarns)
				}
				if _, ok := rowsByNativeID(t, st, tc.typ)[tc.arn]; ok != (call == 2) {
					t.Errorf("first-page %s stored = %v; want %v", tc.typ, ok, call == 2)
				}
			})
		}
	}
}

// A real IAM denial of a listing warns once and stores nothing of it; the
// other listings are stored.
func TestScanBedrock_ListDenialWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "bedrock:ListAutomatedReasoningPolicies")
	calls := newBACCalls()
	brTagFixtures(calls)
	handlers := brHandlers(calls)
	delete(handlers, "ListAutomatedReasoningPolicies")

	if _, _, err := brScan(t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1", *warns)
	}
	if n := len(rowsByNativeID(t, st, TypeBedrockAutomatedReasoningPolicy)); n != 0 {
		t.Errorf("policies stored = %d; want 0", n)
	}
	if n := len(rowsByNativeID(t, st, TypeBedrockGuardrail)); n != 2 {
		t.Errorf("guardrails stored = %d; want 2", n)
	}
}

// An item without the ARN or id its row is stored under is not stored, and
// one warning names it, or a fixed text when it has no name either.
func TestScanBedrock_UnstorableItemsWarn(t *testing.T) {
	for _, tc := range []struct {
		name, op, warnOp, key, typ string
		stored                     int
		answer                     func(in any) (any, error)
	}{
		{"guardrail without an ARN", "ListGuardrails", "bedrock:ListGuardrails", "g-no-arn", TypeBedrockGuardrail, 1, func(in any) (any, error) {
			if in.(*bedrock.ListGuardrailsInput).GuardrailIdentifier != nil {
				return &bedrock.ListGuardrailsOutput{}, nil
			}
			return &bedrock.ListGuardrailsOutput{Guardrails: []bedrocktypes.GuardrailSummary{
				{Arn: sdkaws.String(brARN("guardrail", "tagged")), Id: sdkaws.String("tagged")}, {Id: sdkaws.String("g-no-arn")},
			}}, nil
		}},
		{"agent without an id", "ListAgents", "bedrockagent:ListAgents", "nameless", TypeBedrockAgent, 1, func(any) (any, error) {
			return &bedrockagent.ListAgentsOutput{AgentSummaries: []agenttypes.AgentSummary{{AgentId: sdkaws.String("tagged")}, {AgentName: sdkaws.String("nameless")}}}, nil
		}},
		{"agent without an id or name", "ListAgents", "bedrockagent:ListAgents", "(an agent with no name)", TypeBedrockAgent, 0, func(any) (any, error) {
			return &bedrockagent.ListAgentsOutput{AgentSummaries: []agenttypes.AgentSummary{{}}}, nil
		}},
		{"knowledge base without an id", "ListKnowledgeBases", "bedrockagent:ListKnowledgeBases", "kb", TypeBedrockKnowledgeBase, 0, func(any) (any, error) {
			return &bedrockagent.ListKnowledgeBasesOutput{KnowledgeBaseSummaries: []agenttypes.KnowledgeBaseSummary{{Name: sdkaws.String("kb")}}}, nil
		}},
		{"alias without an id", "ListAgentAliases", "bedrockagent:ListAgentAliases", "(an alias with no name) of agent tagged", TypeBedrockAgentAlias, 1, func(in any) (any, error) {
			if sv(in.(*bedrockagent.ListAgentAliasesInput).AgentId) == "tagged" {
				return &bedrockagent.ListAgentAliasesOutput{AgentAliasSummaries: []agenttypes.AgentAliasSummary{{}}}, nil
			}
			return &bedrockagent.ListAgentAliasesOutput{AgentAliasSummaries: []agenttypes.AgentAliasSummary{{AgentAliasId: sdkaws.String("bare")}}}, nil
		}},
		{"flow alias without an ARN", "ListFlowAliases", "bedrockagent:ListFlowAliases", "fa of flow tagged", TypeBedrockFlowAlias, 0, func(in any) (any, error) {
			if sv(in.(*bedrockagent.ListFlowAliasesInput).FlowIdentifier) == "tagged" {
				return &bedrockagent.ListFlowAliasesOutput{FlowAliasSummaries: []agenttypes.FlowAliasSummary{{Id: sdkaws.String("fa")}}}, nil
			}
			return &bedrockagent.ListFlowAliasesOutput{}, nil
		}},
		{"blueprint without an ARN", "ListBlueprints", "bedrockdataautomation:ListBlueprints", "(an item with no id)", TypeBedrockBlueprint, 0, func(in any) (any, error) {
			if in.(*bda.ListBlueprintsInput).ResourceOwner == bdatypes.ResourceOwnerService {
				return &bda.ListBlueprintsOutput{}, nil
			}
			return &bda.ListBlueprintsOutput{Blueprints: []bdatypes.BlueprintSummary{{}}}, nil
		}},
		{"guardrail version without an ARN", "ListGuardrails", "bedrock:ListGuardrails", "1 of guardrail tagged", TypeBedrockGuardrailVersion, 1, func(in any) (any, error) {
			switch id := in.(*bedrock.ListGuardrailsInput).GuardrailIdentifier; {
			case id == nil:
				return &bedrock.ListGuardrailsOutput{Guardrails: []bedrocktypes.GuardrailSummary{
					{Arn: sdkaws.String(brARN("guardrail", "tagged")), Id: sdkaws.String("tagged")}, {Arn: sdkaws.String(brARN("guardrail", "bare")), Id: sdkaws.String("bare")},
				}}, nil
			case *id == "tagged":
				return &bedrock.ListGuardrailsOutput{Guardrails: []bedrocktypes.GuardrailSummary{{Version: sdkaws.String("1")}}}, nil
			}
			return &bedrock.ListGuardrailsOutput{Guardrails: []bedrocktypes.GuardrailSummary{{Arn: sdkaws.String(brARN("guardrail", "bare")), Version: sdkaws.String("1")}}}, nil
		}},
		{"flow version without an ARN", "ListFlowVersions", "bedrockagent:ListFlowVersions", "1 of flow tagged", TypeBedrockFlowVersion, 1, func(in any) (any, error) {
			if id := sv(in.(*bedrockagent.ListFlowVersionsInput).FlowIdentifier); id != "tagged" {
				return &bedrockagent.ListFlowVersionsOutput{FlowVersionSummaries: []agenttypes.FlowVersionSummary{{Arn: sdkaws.String(brARN("flow", id)), Version: sdkaws.String("1")}}}, nil
			}
			return &bedrockagent.ListFlowVersionsOutput{FlowVersionSummaries: []agenttypes.FlowVersionSummary{{Version: sdkaws.String("1")}}}, nil
		}},
		{"data source without an id", "ListDataSources", "bedrockagent:ListDataSources", "ds of knowledge base tagged", TypeBedrockDataSource, 0, func(in any) (any, error) {
			if sv(in.(*bedrockagent.ListDataSourcesInput).KnowledgeBaseId) == "tagged" {
				return &bedrockagent.ListDataSourcesOutput{DataSourceSummaries: []agenttypes.DataSourceSummary{{Name: sdkaws.String("ds")}}}, nil
			}
			return &bedrockagent.ListDataSourcesOutput{}, nil
		}},
		{"enforced configuration without an id", "ListEnforcedGuardrailsConfiguration", "bedrock:ListEnforcedGuardrailsConfiguration", "(a configuration with no guardrail)", TypeBedrockEnforcedGuardrailConfiguration, 0, func(any) (any, error) {
			return &bedrock.ListEnforcedGuardrailsConfigurationOutput{GuardrailsConfig: []bedrocktypes.AccountEnforcedGuardrailOutputConfiguration{{}}}, nil
		}},
		{"foundation model without an ARN", "ListFoundationModels", "bedrock:ListFoundationModels", "m", TypeBedrockFoundationModel, 0, func(any) (any, error) {
			return &bedrock.ListFoundationModelsOutput{ModelSummaries: []bedrocktypes.FoundationModelSummary{{ModelId: sdkaws.String("m")}}}, nil
		}},
		{"prompt version without an Arn", "ListPrompts", "bedrockagent:ListPrompts", "1 of prompt tagged", TypeBedrockPromptVersion, 0, func(in any) (any, error) {
			switch id := in.(*bedrockagent.ListPromptsInput).PromptIdentifier; {
			case id == nil:
				return &bedrockagent.ListPromptsOutput{PromptSummaries: []agenttypes.PromptSummary{{Arn: sdkaws.String(brARN("prompt", "tagged")), Id: sdkaws.String("tagged")}}}, nil
			case *id == "tagged":
				return &bedrockagent.ListPromptsOutput{PromptSummaries: []agenttypes.PromptSummary{{Version: sdkaws.String("1")}}}, nil
			}
			return &bedrockagent.ListPromptsOutput{}, nil
		}},
		{"prompt version without a Version", "ListPrompts", "bedrockagent:ListPrompts", "(a version with no number) of prompt tagged", TypeBedrockPromptVersion, 0, func(in any) (any, error) {
			switch id := in.(*bedrockagent.ListPromptsInput).PromptIdentifier; {
			case id == nil:
				return &bedrockagent.ListPromptsOutput{PromptSummaries: []agenttypes.PromptSummary{
					{Arn: sdkaws.String(brARN("prompt", "tagged")), Id: sdkaws.String("tagged")}, {Arn: sdkaws.String(brARN("prompt", "bare")), Id: sdkaws.String("bare")},
				}}, nil
			case *id == "tagged":
				return &bedrockagent.ListPromptsOutput{PromptSummaries: []agenttypes.PromptSummary{{Arn: sdkaws.String(brARN("prompt", "tagged"))}}}, nil
			}
			return &bedrockagent.ListPromptsOutput{}, nil
		}},
		{"action group without an id", "ListAgentActionGroups", "bedrockagent:ListAgentActionGroups", "nameless of tagged", TypeBedrockAgentActionGroup, 1, func(in any) (any, error) {
			if sv(in.(*bedrockagent.ListAgentActionGroupsInput).AgentId) != "tagged" {
				return &bedrockagent.ListAgentActionGroupsOutput{ActionGroupSummaries: []agenttypes.ActionGroupSummary{{ActionGroupId: sdkaws.String("ag")}}}, nil
			}
			return &bedrockagent.ListAgentActionGroupsOutput{ActionGroupSummaries: []agenttypes.ActionGroupSummary{{ActionGroupName: sdkaws.String("nameless")}}}, nil
		}},
		{"test case without an id or name", "ListAutomatedReasoningPolicyTestCases", "bedrock:ListAutomatedReasoningPolicyTestCases", "(one with no name) of " + brARN("automated-reasoning-policy", "tagged"), TypeBedrockAutomatedReasoningPolicyTestCase, 0, func(any) (any, error) {
			return &bedrock.ListAutomatedReasoningPolicyTestCasesOutput{TestCases: []bedrocktypes.AutomatedReasoningPolicyTestCase{{}}}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, tc.warnOp)
			calls := newBACCalls()
			brTagFixtures(calls)
			handlers := brHandlers(calls)
			handlers[tc.op] = tc.answer

			if _, _, err := brScan(t, st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := len(rowsByNativeID(t, st, tc.typ)); got != tc.stored {
				t.Errorf("%s stored = %d; want %d", tc.typ, got, tc.stored)
			}
			if len(*warnings) != 1 {
				t.Fatalf("warnings = %v; want 1", *warnings)
			}
			if w := (*warnings)[0]; !strings.HasSuffix(w.Message, "first "+tc.key) || w.Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning = %+v; want it to name %q first, scoped %s/%s", w, tc.key, testAccountID, testRegion)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanBedrock_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	brTagFixtures(calls)
	handlers := brHandlers(calls)
	handlers["ListFlows"] = func(any) (any, error) {
		return &bedrockagent.ListFlowsOutput{NextToken: sdkaws.String("same"), FlowSummaries: []agenttypes.FlowSummary{
			{Arn: sdkaws.String(brARN("flow", "tagged")), Id: sdkaws.String("tagged")},
		}}, nil
	}

	_, _, err := brScan(t, st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeBedrockFlow)[brARN("flow", "tagged")]; !ok {
		t.Error("flow from the pages read missing")
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run, although the phase in flight saw it in its
// listing and its tag reads; a hard error beside the stop is kept.
func TestScanBedrock_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	brTagFixtures(calls)
	calls.errs[brARN("guardrail", "tagged")] = apiErr("ValidationException", "bad")
	handlers := brHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The agents' second page ends as an SDK call does when ctx ends; the
	// first page's agent is stored, so its tag read sees the stop too.
	agents := handlers["ListAgents"]
	handlers["ListAgents"] = func(in any) (any, error) {
		if in.(*bedrockagent.ListAgentsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return agents(in)
	}

	_, _, err := brScanCtx(ctx, t, st, handlers, apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:Op"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := calls.count("ListAgentAliases"); n != 0 {
		t.Errorf("ListAgentAliases called %d times after ctx ended", n)
	}
	if !joinCarriesAPIErrorCode(err, "ValidationException") {
		t.Errorf("err = %v; want the ValidationException beside the stop kept", err)
	}
	phases := bedrockPhases(ctx, nil, nil, nil, newTestAccount(testAccountID), testRegion, st, testScanID)
	agentsAt := slices.IndexFunc(phases, func(ph hostPhase) bool { return ph.op == "bedrockagent:ListAgents" })
	if notRun := fmt.Sprintf("%d phases not run", len(phases)-agentsAt-1); agentsAt < 0 || !strings.Contains(err.Error(), notRun) {
		t.Errorf("err = %v; want it to say %s", err, notRun)
	}
}

// Each host's NXDOMAIN: the agent and data-automation hosts' on their first
// call is a silent gap (the region serves bedrock without them); bedrock's
// own with nothing stored is the region unavailable; an NXDOMAIN after rows
// were stored is an error that keeps their counts.
func TestScanBedrock_NXDOMAIN(t *testing.T) {
	nx := func(host string) error {
		return &net.DNSError{Err: "no such host", Name: host + "." + testRegion + ".amazonaws.com", IsNotFound: true}
	}
	agentOps := []string{
		"ListAgents", "ListAgentAliases", "ListKnowledgeBases", "GetKnowledgeBase", "ListDataSources", "ListFlows", "ListFlowAliases", "ListFlowVersions", "ListPrompts",
		"ListAgentVersions", "ListAgentActionGroups", "ListAgentKnowledgeBases", "ListAgentCollaborators",
	}
	daOps := []string{"ListBlueprints", "ListDataAutomationProjects", "ListDataAutomationLibraries"}
	without := func(handlers map[string]func(any) (any, error), ops []string, err error) {
		for _, op := range ops {
			handlers[op] = func(any) (any, error) { return nil, err }
		}
	}
	t.Run("agent and data-automation hosts absent", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls := newBACCalls()
		brTagFixtures(calls)
		handlers := brHandlers(calls)
		without(handlers, agentOps, nx("bedrock-agent"))
		without(handlers, daOps, nx("bedrock-data-automation"))

		total, _, err := brScan(t, st, handlers)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
		if want := 2*8 + 4; total != want {
			t.Errorf("total = %d; want the %d bedrock-host rows", total, want)
		}
		if n := calls.count("ListAgentAliases") + calls.count("ListFlows") + calls.count("ListDataAutomationProjects"); n != 0 {
			t.Errorf("later phases of an absent host called %d times; want 0", n)
		}
	})
	t.Run("agent host NXDOMAIN after its rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		calls := newBACCalls()
		brTagFixtures(calls)
		handlers := brHandlers(calls)
		handlers["ListFlows"] = func(any) (any, error) { return nil, nx("bedrock-agent") }

		total, _, err := brScan(t, st, handlers)
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
		}
		if total == 0 {
			t.Error("total = 0; want the stored rows counted")
		}
	})
	t.Run("agent host NXDOMAIN on its first phase's second page", func(t *testing.T) {
		st := newTestStore(t)
		calls := newBACCalls()
		brTagFixtures(calls)
		handlers := brHandlers(calls)
		handlers["ListAgents"] = failOnCall(2, nx("bedrock-agent"), handlers["ListAgents"])

		_, _, err := brScan(t, st, handlers)
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
		}
		if calls.count("ListKnowledgeBases") == 0 {
			t.Error("the agent host's later phases were not run")
		}
	})
	t.Run("a first phase with no rows and no error keeps its host", func(t *testing.T) {
		st := newTestStore(t)
		calls := newBACCalls()
		brTagFixtures(calls)
		handlers := brHandlers(calls)
		handlers["ListGuardrails"] = brEmpty(calls, "ListGuardrails", &bedrock.ListGuardrailsOutput{})
		handlers["ListAgents"] = brEmpty(calls, "ListAgents", &bedrockagent.ListAgentsOutput{})
		handlers["ListBlueprints"] = brEmpty(calls, "ListBlueprints", &bda.ListBlueprintsOutput{})

		if _, _, err := brScan(t, st, handlers); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for _, c := range []struct{ typ, arn string }{
			{TypeBedrockAutomatedReasoningPolicy, brARN("automated-reasoning-policy", "tagged")},
			{TypeBedrockKnowledgeBase, bedrockKBARN("aws", testRegion, testAccountID, "tagged")},
			{TypeBedrockDataAutomationProject, brARN("data-automation-project", "tagged")},
		} {
			if _, ok := rowsByNativeID(t, st, c.typ)[c.arn]; !ok {
				t.Errorf("%s %s missing", c.typ, c.arn)
			}
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		_, _, err := brScanCtx(context.Background(), t, st, nil, nx("bedrock"))
		if got := classifyServiceError(err); got != outcomeUnavailable {
			t.Errorf("classifyServiceError = %v; want outcomeUnavailable (err %v)", got, err)
		}
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
	t.Run("bedrock host absent, agent rows stored", func(t *testing.T) {
		st := newTestStore(t)
		calls := newBACCalls()
		brTagFixtures(calls)
		all := brHandlers(calls)
		handlers := map[string]func(any) (any, error){"ListTagsForResource": all["ListTagsForResource"]}
		for _, op := range agentOps {
			handlers[op] = all[op]
		}

		total, _, err := brScanCtx(context.Background(), t, st, handlers, nx("bedrock"))
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError = %v; want outcomeError, so the agent rows keep their counts (err %v)", got, err)
		}
		if want := 2 * 6; total != want {
			t.Errorf("total = %d; want the %d agent-host rows", total, want)
		}
	})
}

// isBedrockModelsGap, isBedrockDAGap, isBedrockRouterGap and isBedrockARGap accept only their
// gap shapes; an unrelated code of the same family is not a gap.
func TestBedrockGapPredicates(t *testing.T) {
	for _, tc := range []struct {
		name string
		is   func(error) bool
		err  error
		want bool
	}{
		{"models unrelated validation", isBedrockModelsGap, apiErr("ValidationException", "malformed request"), false},
		{"models throttle", isBedrockModelsGap, apiErr("ThrottlingException", "slow down"), false},
		{"models real denial", isBedrockModelsGap, apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:ListCustomModels"), false},
		{"models upper-case unknown", isBedrockModelsGap, apiErr("ValidationException", "Unknown Operation"), true},
		{"data automation real denial", isBedrockDAGap, apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:ListBlueprints"), false},
		{"data automation validation", isBedrockDAGap, apiErr("ValidationException", "You don't have the permissions to perform the requested operation."), true},
		{"data automation closed", isBedrockDAGap, apiErr("AccessDeniedException", ""), true},
		{"data automation throttle", isBedrockDAGap, apiErr("ThrottlingException", "slow down"), false},
		{"router real denial", isBedrockRouterGap, apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:ListPromptRouters"), false},
		{"router canned", isBedrockRouterGap, apiErr("ValidationException", "You don't have the permissions to perform the requested operation."), true},
		{"AR real denial", isBedrockARGap, apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:ListAutomatedReasoningPolicies"), false},
	} {
		if got := tc.is(tc.err); got != tc.want {
			t.Errorf("%s: %v = %v; want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

// brChildRows is how many rows brChildHandlers adds: one child of each of the
// ten child types under each of the two parents.
const brChildRows = 10 * 2

// brPromptVersionARN is the ARN ListPrompts gives a numbered prompt version:
// the prompt's ARN and the version.
func brPromptVersionARN(prompt string) string { return brARN("prompt", prompt) + ":1" }

// brChildHandlers answers every child listing in handlers with one child per
// parent, beside the DRAFT a version listing also returns, and
// GetDataSource with a body that differs from the list element; it fails
// with c.errs["getds "+kb] when set.
func brChildHandlers(c *bacCalls, handlers map[string]func(any) (any, error)) {
	child := func(op string, isChild func(in any) bool, out func(in any) any) {
		next := handlers[op]
		handlers[op] = func(in any) (any, error) {
			if next != nil && !isChild(in) {
				return next(in)
			}
			c.called(op, in)
			return out(in), nil
		}
	}
	always := func(any) bool { return true }
	child("ListGuardrails", func(in any) bool { return in.(*bedrock.ListGuardrailsInput).GuardrailIdentifier != nil }, func(in any) any {
		id := sv(in.(*bedrock.ListGuardrailsInput).GuardrailIdentifier)
		return &bedrock.ListGuardrailsOutput{Guardrails: []bedrocktypes.GuardrailSummary{
			{Arn: sdkaws.String(brARN("guardrail", id)), Id: sdkaws.String(id), Version: sdkaws.String("DRAFT")},
			{Arn: sdkaws.String(brARN("guardrail", id)), Id: sdkaws.String(id), Version: sdkaws.String("1")},
		}}
	})
	child("ListAutomatedReasoningPolicies", func(in any) bool { return in.(*bedrock.ListAutomatedReasoningPoliciesInput).PolicyArn != nil }, func(in any) any {
		arn := in.(*bedrock.ListAutomatedReasoningPoliciesInput).PolicyArn
		return &bedrock.ListAutomatedReasoningPoliciesOutput{AutomatedReasoningPolicySummaries: []bedrocktypes.AutomatedReasoningPolicySummary{
			{PolicyArn: arn, Version: sdkaws.String("DRAFT")}, {PolicyArn: arn, Version: sdkaws.String("1")},
		}}
	})
	child("ListPrompts", func(in any) bool { return in.(*bedrockagent.ListPromptsInput).PromptIdentifier != nil }, func(in any) any {
		id := sv(in.(*bedrockagent.ListPromptsInput).PromptIdentifier)
		return &bedrockagent.ListPromptsOutput{PromptSummaries: []agenttypes.PromptSummary{
			{Arn: sdkaws.String(brARN("prompt", id)), Id: sdkaws.String(id), Version: sdkaws.String("DRAFT")},
			{Arn: sdkaws.String(brPromptVersionARN(id)), Id: sdkaws.String(id), Version: sdkaws.String("1")},
		}}
	})
	child("ListDataSources", always, func(in any) any {
		kb := in.(*bedrockagent.ListDataSourcesInput).KnowledgeBaseId
		return &bedrockagent.ListDataSourcesOutput{DataSourceSummaries: []agenttypes.DataSourceSummary{{DataSourceId: sdkaws.String("ds"), KnowledgeBaseId: kb, Name: sdkaws.String("ds")}}}
	})
	handlers["GetDataSource"] = func(in any) (any, error) {
		req := in.(*bedrockagent.GetDataSourceInput)
		c.called("GetDataSource", in)
		if err := c.errs["getds "+sv(req.KnowledgeBaseId)]; err != nil {
			return nil, err
		}
		return &bedrockagent.GetDataSourceOutput{DataSource: &agenttypes.DataSource{
			DataSourceId: req.DataSourceId, KnowledgeBaseId: req.KnowledgeBaseId, Name: sdkaws.String("ds"), Description: sdkaws.String("the Get body"),
		}}, nil
	}
	child("ListFlowVersions", always, func(in any) any {
		id := sv(in.(*bedrockagent.ListFlowVersionsInput).FlowIdentifier)
		return &bedrockagent.ListFlowVersionsOutput{FlowVersionSummaries: []agenttypes.FlowVersionSummary{{Arn: sdkaws.String(brARN("flow", id)), Id: sdkaws.String(id), Version: sdkaws.String("1")}}}
	})
	child("ListAgentVersions", always, func(any) any {
		return &bedrockagent.ListAgentVersionsOutput{AgentVersionSummaries: []agenttypes.AgentVersionSummary{{AgentVersion: sdkaws.String("DRAFT")}, {AgentVersion: sdkaws.String("1")}}}
	})
	child("ListAgentActionGroups", always, func(any) any {
		return &bedrockagent.ListAgentActionGroupsOutput{ActionGroupSummaries: []agenttypes.ActionGroupSummary{{ActionGroupId: sdkaws.String("ag")}}}
	})
	child("ListAgentKnowledgeBases", always, func(any) any {
		return &bedrockagent.ListAgentKnowledgeBasesOutput{AgentKnowledgeBaseSummaries: []agenttypes.AgentKnowledgeBaseSummary{{KnowledgeBaseId: sdkaws.String("kb")}}}
	})
	child("ListAgentCollaborators", always, func(any) any {
		return &bedrockagent.ListAgentCollaboratorsOutput{AgentCollaboratorSummaries: []agenttypes.AgentCollaboratorSummary{{CollaboratorId: sdkaws.String("c")}}}
	})
	child("ListAutomatedReasoningPolicyTestCases", always, func(any) any {
		return &bedrock.ListAutomatedReasoningPolicyTestCasesOutput{TestCases: []bedrocktypes.AutomatedReasoningPolicyTestCase{{TestCaseId: sdkaws.String("tc")}}}
	})
}

// brChildNativeIDs is, per child type, the NativeID of the child
// brChildHandlers gives the parent named parent.
func brChildNativeIDs(parent string) map[string]string {
	agent := bedrockAgentARN("aws", testRegion, testAccountID, parent)
	return map[string]string{
		TypeBedrockGuardrailVersion:                 brARN("guardrail", parent) + ":1",
		TypeBedrockAutomatedReasoningPolicyVersion:  brARN("automated-reasoning-policy", parent) + ":1",
		TypeBedrockPromptVersion:                    brPromptVersionARN(parent),
		TypeBedrockDataSource:                       bedrockDataSourceARN("aws", testRegion, testAccountID, parent, "ds"),
		TypeBedrockFlowVersion:                      brARN("flow", parent) + ":1",
		TypeBedrockAgentVersion:                     agent + "/version/1",
		TypeBedrockAgentActionGroup:                 agent + "/action-group/ag",
		TypeBedrockAgentKnowledgeBase:               agent + "/knowledge-base/kb",
		TypeBedrockAgentCollaborator:                agent + "/collaborator/c",
		TypeBedrockAutomatedReasoningPolicyTestCase: brARN("automated-reasoning-policy", parent) + "/test-case/tc",
	}
}

// Every child phase stores its parents' children through the scan's entry
// point, and they count in the scan's totals. A version is stored under its
// parent's ARN and its number, DRAFT is not stored, a data source is its
// GetDataSource body, and of the versions only a prompt version's tags are
// read, sent the version's own ARN.
func TestScanBedrock_ChildRowsCountInScanTotals(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	wantSent := brTagFixtures(calls)
	calls.tags[brPromptVersionARN("tagged")] = map[string]string{"type": TypeBedrockPromptVersion}
	handlers := brHandlers(calls)
	brChildHandlers(calls, handlers)

	total, inserted, err := brScan(t, st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := 2*len(brTaggedTypes()) + brUntaggedRows + brChildRows; total != want || inserted != want {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, want, want)
	}
	for _, parent := range []string{"tagged", "bare"} {
		for typ, id := range brChildNativeIDs(parent) {
			rows := rowsByNativeID(t, st, typ)
			if _, ok := rows[id]; !ok || len(rows) != 2 {
				t.Errorf("%s rows = %v; want 2, %s among them", typ, slices.Collect(maps.Keys(rows)), id)
			}
		}
	}
	versions := rowsByNativeID(t, st, TypeBedrockPromptVersion)
	assertTagsMap(t, versions[brPromptVersionARN("tagged")].TagsJSON, map[string]string{"type": TypeBedrockPromptVersion})
	if bare := versions[brPromptVersionARN("bare")]; bare.TagsJSON != nil {
		t.Errorf("bare prompt version TagsJSON = %q; want nil", *bare.TagsJSON)
	}
	ds := rowsByNativeID(t, st, TypeBedrockDataSource)[bedrockDataSourceARN("aws", testRegion, testAccountID, "tagged", "ds")]
	if !strings.Contains(ds.AttributesJSON, `"Description":"the Get body"`) {
		t.Errorf("data source attributes = %s; want the GetDataSource body", ds.AttributesJSON)
	}
	wantSent = append(wantSent, bedrockAgentListTagsOp+" "+brPromptVersionARN("bare"), bedrockAgentListTagsOp+" "+brPromptVersionARN("tagged"))
	slices.Sort(wantSent)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantSent) {
		t.Errorf("tag reads = %v\nwant %v", calls.tagARNs, wantSent)
	}
}

// One parent's failed child listing or Get keeps the other parent's rows and
// the scan's other counts: a denial warns once under its op, a not-found
// parent or data source is skipped, any other error is returned, and a
// failed GetDataSource keeps the list element as the row.
func TestScanBedrock_ChildFailureKeepsTheOtherRows(t *testing.T) {
	full := 2*len(brTaggedTypes()) + brUntaggedRows + brChildRows
	denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:Op")
	for _, tc := range []struct {
		name, op, warnOp, typ string
		fail                  func(c *bacCalls, h map[string]func(any) (any, error))
		total                 int
		code                  string // the error code returned, "" for none
		warns                 int
	}{
		{"agent versions other error", "ListAgentVersions", "", TypeBedrockAgentVersion, func(_ *bacCalls, h map[string]func(any) (any, error)) {
			h["ListAgentVersions"] = brFailFor(h["ListAgentVersions"], "AgentId", apiErr("ValidationException", "bad"))
		}, full - 1, "ValidationException", 0},
		{"flow versions denied", "ListFlowVersions", "bedrockagent:ListFlowVersions", TypeBedrockFlowVersion, func(_ *bacCalls, h map[string]func(any) (any, error)) {
			h["ListFlowVersions"] = brFailFor(h["ListFlowVersions"], "FlowIdentifier", denied)
		}, full - 1, "", 1},
		{"guardrail versions parent gone", "ListGuardrails", "bedrock:ListGuardrails", TypeBedrockGuardrailVersion, func(_ *bacCalls, h map[string]func(any) (any, error)) {
			h["ListGuardrails"] = brFailFor(h["ListGuardrails"], "GuardrailIdentifier", apiErr("ResourceNotFoundException", "gone"))
		}, full - 1, "", 0},
		{"AR versions other error", "ListAutomatedReasoningPolicies", "", TypeBedrockAutomatedReasoningPolicyVersion, func(_ *bacCalls, h map[string]func(any) (any, error)) {
			h["ListAutomatedReasoningPolicies"] = brFailFor(h["ListAutomatedReasoningPolicies"], "PolicyArn", apiErr("ValidationException", "bad"))
		}, full - 1, "ValidationException", 0},
		{"prompt versions denied", "ListPrompts", "bedrockagent:ListPrompts", TypeBedrockPromptVersion, func(_ *bacCalls, h map[string]func(any) (any, error)) {
			h["ListPrompts"] = brFailFor(h["ListPrompts"], "PromptIdentifier", denied)
		}, full - 1, "", 1},
		{"data sources other error", "ListDataSources", "", TypeBedrockDataSource, func(_ *bacCalls, h map[string]func(any) (any, error)) {
			h["ListDataSources"] = brFailFor(h["ListDataSources"], "KnowledgeBaseId", apiErr("ValidationException", "bad"))
		}, full - 1, "ValidationException", 0},
		{"data sources parent gone", "ListDataSources", "", TypeBedrockDataSource, func(_ *bacCalls, h map[string]func(any) (any, error)) {
			h["ListDataSources"] = brFailFor(h["ListDataSources"], "KnowledgeBaseId", apiErr("ResourceNotFoundException", "gone"))
		}, full - 1, "", 0},
		{"GetDataSource other error", "GetDataSource", "", TypeBedrockDataSource, func(c *bacCalls, _ map[string]func(any) (any, error)) {
			c.errs["getds tagged"] = apiErr("ValidationException", "bad")
		}, full, "ValidationException", 0},
		{"GetDataSource denied", "GetDataSource", bedrockGetDSOp, TypeBedrockDataSource, func(c *bacCalls, _ map[string]func(any) (any, error)) {
			c.errs["getds tagged"] = denied
		}, full, "", 1},
		{"GetDataSource not found", "GetDataSource", "", TypeBedrockDataSource, func(c *bacCalls, _ map[string]func(any) (any, error)) {
			c.errs["getds tagged"] = apiErr("ResourceNotFoundException", "gone")
		}, full - 1, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, tc.warnOp)
			calls := newBACCalls()
			brTagFixtures(calls)
			handlers := brHandlers(calls)
			brChildHandlers(calls, handlers)
			tc.fail(calls, handlers)

			total, inserted, err := brScan(t, st, handlers)
			if tc.code == "" && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if tc.code != "" && !joinCarriesAPIErrorCode(err, tc.code) {
				t.Fatalf("err = %v; want it to carry %s", err, tc.code)
			}
			if total != tc.total || inserted != tc.total {
				t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, tc.total, tc.total)
			}
			if _, ok := rowsByNativeID(t, st, tc.typ)[brChildNativeIDs("bare")[tc.typ]]; !ok {
				t.Errorf("the other parent's %s is missing", tc.typ)
			}
			if tc.warnOp != "" && len(*warnings) != tc.warns {
				t.Errorf("%s warnings = %v; want %d", tc.warnOp, *warnings, tc.warns)
			}
			if tc.op == "GetDataSource" && tc.total == full {
				r := rowsByNativeID(t, st, TypeBedrockDataSource)[brChildNativeIDs("tagged")[TypeBedrockDataSource]]
				if strings.Contains(r.AttributesJSON, "the Get body") || !strings.Contains(r.AttributesJSON, `"DataSourceId":"ds"`) {
					t.Errorf("attributes = %s; want the list element", r.AttributesJSON)
				}
			}
		})
	}
}

// brFailFor fails next's calls whose field names the "tagged" parent with
// err, and passes the others on.
func brFailFor(next func(any) (any, error), field string, err error) func(any) (any, error) {
	return func(in any) (any, error) {
		if parent := bacField[*string](in, field); parent != nil && strings.HasSuffix(*parent, "tagged") {
			return nil, err
		}
		return next(in)
	}
}

// A prompt version is stored under its own ARN, prompt/ID:N, whether its
// list element's Arn names the version or its prompt, and attaches to its
// prompt: the resolver recovers the prompt's ARN from that NativeID. The
// DRAFT listed beside it is the prompt itself and is not stored.
func TestScanBedrock_PromptVersionAttachesToItsPrompt(t *testing.T) {
	for _, tc := range []struct{ name, elementARN string }{
		{"Arn names the version", brPromptVersionARN("tagged")},
		{"Arn names the prompt", brARN("prompt", "tagged")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := newBACCalls()
			brTagFixtures(calls)
			handlers := brHandlers(calls)
			brChildHandlers(calls, handlers)
			list := handlers["ListPrompts"]
			handlers["ListPrompts"] = func(in any) (any, error) {
				if sv(in.(*bedrockagent.ListPromptsInput).PromptIdentifier) != "tagged" {
					return list(in)
				}
				return &bedrockagent.ListPromptsOutput{PromptSummaries: []agenttypes.PromptSummary{
					{Arn: sdkaws.String(brARN("prompt", "tagged")), Id: sdkaws.String("tagged"), Version: sdkaws.String("DRAFT")},
					{Arn: sdkaws.String(tc.elementARN), Id: sdkaws.String("tagged"), Version: sdkaws.String("1")},
				}}, nil
			}
			if _, _, err := brScan(t, st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			var ofTagged []string
			for id := range rowsByNativeID(t, st, TypeBedrockPromptVersion) {
				if strings.HasPrefix(id, brARN("prompt", "tagged")) {
					ofTagged = append(ofTagged, id)
				}
			}
			if !slices.Equal(ofTagged, []string{brPromptVersionARN("tagged")}) {
				t.Fatalf("versions of prompt tagged = %v; want only %s", ofTagged, brPromptVersionARN("tagged"))
			}
			if err := resolveBedrockPromptVersion(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolveBedrockPromptVersion: %v", err)
			}
			versionID := store.ResourceID("aws", testAccountID, brPromptVersionARN("tagged"))
			rels, err := st.RelationshipsFrom(versionID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != 1 {
				t.Fatalf("relationships = %v; want 1", rels)
			}
			assertRelationship(t, rels, versionID, store.ResourceID("aws", testAccountID, brARN("prompt", "tagged")), store.RelAttachedTo)
		})
	}
}

// A knowledge base's tag read is sent the KnowledgeBaseArn of its
// GetKnowledgeBase body, which names the partition the NativeID cannot, and
// the NativeID when the Get failed and the row is the list element.
func TestScanBedrock_KnowledgeBaseTagReadUsesTheBodyARN(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	brTagFixtures(calls)
	govARN := "arn:aws-us-gov:bedrock:" + testRegion + ":" + testAccountID + ":knowledge-base/tagged"
	calls.tags[govARN] = map[string]string{"partition": "aws-us-gov"}
	calls.errs["get bare"] = apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:GetKnowledgeBase")
	handlers := brHandlers(calls)
	get := handlers["GetKnowledgeBase"]
	handlers["GetKnowledgeBase"] = func(in any) (any, error) {
		out, err := get(in)
		if body, ok := out.(*bedrockagent.GetKnowledgeBaseOutput); ok && sv(body.KnowledgeBase.KnowledgeBaseId) == "tagged" {
			body.KnowledgeBase.KnowledgeBaseArn = sdkaws.String(govARN)
		}
		return out, err
	}

	if _, _, err := brScan(t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, c := range []struct {
		arn  string
		sent bool
	}{
		{govARN, true},
		{bedrockKBARN("aws", testRegion, testAccountID, "bare"), true},
		{bedrockKBARN("aws", testRegion, testAccountID, "tagged"), false},
	} {
		if sent := slices.Contains(calls.tagARNs, bedrockAgentListTagsOp+" "+c.arn); sent != c.sent {
			t.Errorf("tag read sent %s = %v; want %v", c.arn, sent, c.sent)
		}
	}
	r := rowsByNativeID(t, st, TypeBedrockKnowledgeBase)[bedrockKBARN("aws", testRegion, testAccountID, "tagged")]
	assertTagsMap(t, r.TagsJSON, map[string]string{"partition": "aws-us-gov"})
}

// When ctx ends during the GetKnowledgeBase reads, every knowledge base
// listed is stored as its list element, and the stop is reported once.
func TestScanBedrock_KnowledgeBaseGetCutShortByContext(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	brTagFixtures(calls)
	handlers := brHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handlers["GetKnowledgeBase"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}

	_, _, err := brScanCtx(ctx, t, st, handlers, apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock:Op"))
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	rows := rowsByNativeID(t, st, TypeBedrockKnowledgeBase)
	for _, kb := range []string{"tagged", "bare"} {
		r, ok := rows[bedrockKBARN("aws", testRegion, testAccountID, kb)]
		if !ok {
			t.Errorf("knowledge base %s missing", kb)
			continue
		}
		if !strings.Contains(r.AttributesJSON, `"KnowledgeBaseId":"`+kb+`"`) || strings.Contains(r.AttributesJSON, "RoleArn") {
			t.Errorf("knowledge base %s attributes = %s; want the list element", kb, r.AttributesJSON)
		}
	}
}

// readRowDetails keeps every row whose read ctx's end cut short or kept
// from starting, as the list element, and returns that stop as one error.
func TestReadBedrockDetails_ContextEndIsOneError(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rows := []*store.Resource{
		bacRowAt(acct, testRegion, testScanID, TypeBedrockKnowledgeBase, "kb-1", "kb-1", map[string]string{"Id": "kb-1"}),
		bacRowAt(acct, testRegion, testScanID, TypeBedrockKnowledgeBase, "kb-2", "kb-2", map[string]string{"Id": "kb-2"}),
	}
	kept, _, hard, transient := readRowDetails(ctx, st, acct, testRegion, bedrockGetKBOp, "knowledge base(s)", bedrockGetNotFound, rows,
		func(ctx context.Context, _ *store.Resource) (any, error) {
			cancel()
			return nil, ctx.Err()
		})
	if len(kept) != 2 || kept[0].AttributesJSON != `{"Id":"kb-1"}` || kept[1].AttributesJSON != `{"Id":"kb-2"}` {
		t.Errorf("kept = %v; want both rows as their list element", kept)
	}
	if members := bacTopMembers(hard); len(members) != 1 || !errors.Is(hard, context.Canceled) {
		t.Errorf("hard = %v; want one error carrying context.Canceled", hard)
	}
	if transient != nil {
		t.Errorf("transient = %v; want nil", transient)
	}
}

// A ctx already ended starts no Get: every row is kept as its list element,
// and the stop is one error.
func TestReadBedrockDetails_ContextEndedBeforeAnyRead(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows := []*store.Resource{
		bacRowAt(acct, testRegion, testScanID, TypeBedrockKnowledgeBase, "kb-1", "kb-1", map[string]string{"Id": "kb-1"}),
		bacRowAt(acct, testRegion, testScanID, TypeBedrockKnowledgeBase, "kb-2", "kb-2", map[string]string{"Id": "kb-2"}),
	}
	kept, _, hard, _ := readRowDetails(ctx, st, acct, testRegion, bedrockGetKBOp, "knowledge base(s)", bedrockGetNotFound, rows,
		func(context.Context, *store.Resource) (any, error) {
			t.Error("a Get started after ctx ended")
			return nil, nil
		})
	if len(kept) != 2 {
		t.Errorf("kept = %d rows; want both", len(kept))
	}
	if members := bacTopMembers(hard); len(members) != 1 || !errors.Is(hard, context.Canceled) {
		t.Errorf("hard = %v; want one error carrying context.Canceled", hard)
	}
}

// A failed page of the enforced guardrail configurations keeps the pages
// before it.
func TestScanBedrock_EnforcedGuardrailsKeepEarlierPages(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	brTagFixtures(calls)
	handlers := brHandlers(calls)
	handlers["ListEnforcedGuardrailsConfiguration"] = func(in any) (any, error) {
		if in.(*bedrock.ListEnforcedGuardrailsConfigurationInput).NextToken != nil {
			return nil, apiErr("ValidationException", "page 2")
		}
		return &bedrock.ListEnforcedGuardrailsConfigurationOutput{NextToken: sdkaws.String("p2"), GuardrailsConfig: []bedrocktypes.AccountEnforcedGuardrailOutputConfiguration{{ConfigId: sdkaws.String("c1")}}}, nil
	}

	_, _, err := brScan(t, st, handlers)
	if !joinCarriesAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v; want the ValidationException", err)
	}
	want := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":enforced-guardrail-configuration/c1"
	if _, ok := rowsByNativeID(t, st, TypeBedrockEnforcedGuardrailConfiguration)[want]; !ok {
		t.Errorf("configuration from the first page missing")
	}
}
