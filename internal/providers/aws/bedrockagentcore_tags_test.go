package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	bacdata "github.com/aws/aws-sdk-go-v2/service/bedrockagentcore"
	bacdatatypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcore/types"
	bac "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
	bactypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/types"
	"github.com/icearp/disco-cli/store"
)

// bacCalls records what the stubbed AgentCore calls were sent and answers
// ListTagsForResource from tags or errs, both keyed by ResourceArn; calls
// run concurrently.
type bacCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	tagARNs    []string
	ops        map[string]int
	tags       map[string]map[string]string
	errs       map[string]error
}

func newBACCalls() *bacCalls {
	return &bacCalls{maxResults: map[string][]int32{}, ops: map[string]int{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
}

// bacField is in's field name, the zero T when in has none.
func bacField[T any](in any, name string) T {
	var zero T
	v := reflect.ValueOf(in).Elem().FieldByName(name)
	if !v.IsValid() {
		return zero
	}
	got, _ := v.Interface().(T)
	return got
}

func (c *bacCalls) called(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.maxResults[op] = append(c.maxResults[op], *n)
	}
}

// answer records each call to op and answers it with out(in).
func (c *bacCalls) answer(op string, out func(in any) any) func(any) (any, error) {
	return func(in any) (any, error) {
		c.called(op, in)
		return out(in), nil
	}
}

func (c *bacCalls) listTags(in any) (any, error) {
	arn := sv(in.(*bac.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	c.mu.Unlock()
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &bac.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

func (c *bacCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func bacTestARN(kind, name string) string {
	return bacARN("aws", testRegion, testAccountID, kind, name)
}

// bacTagged is one type the tag read covers: its listing op and the ARN kind
// of its two fixture resources, "tagged" and "bare".
type bacTagged struct{ op, typ, kind string }

func bacTaggedTypes() []bacTagged {
	return []bacTagged{
		{"ListApiKeyCredentialProviders", TypeBedrockAgentCoreAPIKeyCredentialProvider, "token-vault/default/apikeycredentialprovider"},
		{"ListOauth2CredentialProviders", TypeBedrockAgentCoreOAuth2CredentialProvider, "token-vault/default/oauth2credentialprovider"},
		{"ListBrowsers", TypeBedrockAgentCoreBrowserCustom, "browser-custom"},
		{"ListBrowserProfiles", TypeBedrockAgentCoreBrowserProfile, "browser-profile"},
		{"ListCodeInterpreters", TypeBedrockAgentCoreCodeInterpreterCustom, "code-interpreter-custom"},
		{"ListEvaluators", TypeBedrockAgentCoreEvaluator, "evaluator"},
		{"ListMemories", TypeBedrockAgentCoreMemory, "memory"},
		{"ListOnlineEvaluationConfigs", TypeBedrockAgentCoreOnlineEvaluationConfig, "online-evaluation-config"},
		{"ListPolicyEngines", TypeBedrockAgentCorePolicyEngine, "policy-engine"},
		{"ListAgentRuntimes", TypeBedrockAgentCoreRuntime, "runtime"},
		{"ListAgentRuntimeEndpoints", TypeBedrockAgentCoreRuntimeEndpoint, "runtime/tagged/runtime-endpoint"},
		{"ListAgentRuntimeEndpoints", TypeBedrockAgentCoreRuntimeEndpoint, "runtime/bare/runtime-endpoint"},
		{"ListWorkloadIdentities", TypeBedrockAgentCoreWorkloadIdentity, "workload-identity-directory/default/workload-identity"},
		{"ListHarnesses", TypeBedrockAgentCoreHarness, "harness"},
		{"ListHarnessEndpoints", TypeBedrockAgentCoreHarnessEndpoint, "harness/tagged/harness-endpoint"},
		{"ListHarnessEndpoints", TypeBedrockAgentCoreHarnessEndpoint, "harness/bare/harness-endpoint"},
		{"ListPaymentManagers", TypeBedrockAgentCorePaymentManager, "payment-manager"},
		{"ListPaymentCredentialProviders", TypeBedrockAgentCorePaymentCredentialProvider, "token-vault/default/paymentcredentialprovider"},
		{"ListCapacityProviders", TypeBedrockAgentCoreCapacityProvider, "capacity-provider"},
		{"ListGateways", TypeBedrockAgentCoreGateway, "gateway"},
		{"ListConsentPortals", TypeBedrockAgentCoreConsentPortal, "consent-portal"},
		{"ListConfigurationBundles", TypeBedrockAgentCoreConfigurationBundle, "configuration-bundle"},
		{"ListDatasets", TypeBedrockAgentCoreDataset, "dataset"},
	}
}

// bacChild is a child listing: the input field naming its parent, the type
// of its rows, the NativeID of the child the parent "bare" answers with,
// and whether its model lists a not-found code.
type bacChild struct {
	op, parentField, typ, siblingARN string
	notFound                         bool
}

func bacChildren() []bacChild {
	return []bacChild{
		{"ListGatewayTargets", "GatewayIdentifier", TypeBedrockAgentCoreGatewayTarget, bacTestARN("gateway-target", "bare/t"), true},
		{"ListGatewayRules", "GatewayIdentifier", TypeBedrockAgentCoreGatewayRule, bacTestARN("gateway", "bare") + "/rule/rule", true},
		{"ListGatewayRateLimits", "GatewayIdentifier", TypeBedrockAgentCoreGatewayRateLimit, bacTestARN("gateway", "bare") + "/rate-limit/limit", true},
		{"ListPolicies", "PolicyEngineId", TypeBedrockAgentCorePolicy, bacTestARN("policy-engine/bare/policy", "pol"), true},
		{"ListPolicyGenerations", "PolicyEngineId", TypeBedrockAgentCorePolicyGeneration, bacTestARN("policy-engine/bare/policy-generation", "gen"), true},
		{"ListRegistryRecords", "RegistryId", TypeBedrockAgentCoreRegistryRecord, bacTestARN("registry/bare/record", "rec"), true},
		{"ListHarnessEndpoints", "HarnessId", TypeBedrockAgentCoreHarnessEndpoint, bacTestARN("harness/bare/harness-endpoint", "tagged"), true},
		{"ListPaymentConnectors", "PaymentManagerId", TypeBedrockAgentCorePaymentConnector, bacTestARN("payment-connector", "bare/con"), false},
		{"ListAgentRuntimeEndpoints", "AgentRuntimeId", TypeBedrockAgentCoreRuntimeEndpoint, bacTestARN("runtime/bare/runtime-endpoint", "tagged"), false},
	}
}

// The AWS built-in rows, stored without a tag read.
var (
	bacSystemBrowserARN = "arn:aws:bedrock-agentcore:" + testRegion + ":aws:browser/aws.browser.v1"
	bacSystemCIARN      = "arn:aws:bedrock-agentcore:" + testRegion + ":aws:code-interpreter/aws.codeinterpreter.v1"
	bacBuiltinEvalARN   = "arn:aws:bedrock-agentcore:::evaluator/Builtin.Helpfulness"
)

// bacFixtureRows is how many rows bacHandlers lists besides the tagged
// fixtures: three built-in rows, two registries, two A/B tests and one child
// per parent for the gateway targets, rules and rate limits, policies,
// policy generations, registry records and payment connectors.
const bacFixtureRows = 3 + 2 + 2 + 7*2

// bacTwoPages serves "tagged" on the first page and "bare" on the second.
func bacTwoPages(c *bacCalls, op string, out func(name string, next *string) any) func(any) (any, error) {
	return c.answer(op, func(in any) any {
		if bacField[*string](in, "NextToken") == nil {
			return out("tagged", sdkaws.String("p2"))
		}
		return out("bare", nil)
	})
}

// bacHandlers answers every AgentCore op the scan calls: two resources for
// each tagged type (bacTaggedTypes) and for the registries and A/B tests,
// the built-in browser, code interpreter and evaluator, and one child per
// parent for the other child listings.
func bacHandlers(c *bacCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListApiKeyCredentialProviders": bacTwoPages(c, "ListApiKeyCredentialProviders", func(n string, next *string) any {
			return &bac.ListApiKeyCredentialProvidersOutput{NextToken: next, CredentialProviders: []bactypes.ApiKeyCredentialProviderItem{
				{CredentialProviderArn: sdkaws.String(bacTestARN("token-vault/default/apikeycredentialprovider", n)), Name: sdkaws.String(n)},
			}}
		}),
		"ListOauth2CredentialProviders": bacTwoPages(c, "ListOauth2CredentialProviders", func(n string, next *string) any {
			return &bac.ListOauth2CredentialProvidersOutput{NextToken: next, CredentialProviders: []bactypes.Oauth2CredentialProviderItem{
				{CredentialProviderArn: sdkaws.String(bacTestARN("token-vault/default/oauth2credentialprovider", n)), Name: sdkaws.String(n)},
			}}
		}),
		"ListBrowsers": func(in any) (any, error) {
			if in.(*bac.ListBrowsersInput).Type == bactypes.ResourceTypeSystem {
				c.called("ListBrowsers", in)
				return &bac.ListBrowsersOutput{BrowserSummaries: []bactypes.BrowserSummary{{BrowserArn: sdkaws.String(bacSystemBrowserARN), BrowserId: sdkaws.String("aws.browser.v1")}}}, nil
			}
			return bacTwoPages(c, "ListBrowsers", func(n string, next *string) any {
				return &bac.ListBrowsersOutput{NextToken: next, BrowserSummaries: []bactypes.BrowserSummary{
					{BrowserArn: sdkaws.String(bacTestARN("browser-custom", n)), BrowserId: sdkaws.String(n)},
				}}
			})(in)
		},
		"ListBrowserProfiles": bacTwoPages(c, "ListBrowserProfiles", func(n string, next *string) any {
			return &bac.ListBrowserProfilesOutput{NextToken: next, ProfileSummaries: []bactypes.BrowserProfileSummary{
				{ProfileArn: sdkaws.String(bacTestARN("browser-profile", n)), ProfileId: sdkaws.String(n)},
			}}
		}),
		"ListCodeInterpreters": func(in any) (any, error) {
			if in.(*bac.ListCodeInterpretersInput).Type == bactypes.ResourceTypeSystem {
				c.called("ListCodeInterpreters", in)
				return &bac.ListCodeInterpretersOutput{CodeInterpreterSummaries: []bactypes.CodeInterpreterSummary{{CodeInterpreterArn: sdkaws.String(bacSystemCIARN), CodeInterpreterId: sdkaws.String("aws.codeinterpreter.v1")}}}, nil
			}
			return bacTwoPages(c, "ListCodeInterpreters", func(n string, next *string) any {
				return &bac.ListCodeInterpretersOutput{NextToken: next, CodeInterpreterSummaries: []bactypes.CodeInterpreterSummary{
					{CodeInterpreterArn: sdkaws.String(bacTestARN("code-interpreter-custom", n)), CodeInterpreterId: sdkaws.String(n)},
				}}
			})(in)
		},
		"ListEvaluators": bacTwoPages(c, "ListEvaluators", func(n string, next *string) any {
			out := &bac.ListEvaluatorsOutput{NextToken: next, Evaluators: []bactypes.EvaluatorSummary{
				{EvaluatorArn: sdkaws.String(bacTestARN("evaluator", n)), EvaluatorId: sdkaws.String(n), EvaluatorType: bactypes.EvaluatorTypeCustom},
			}}
			if n == "tagged" {
				out.Evaluators = append(out.Evaluators, bactypes.EvaluatorSummary{
					EvaluatorArn: sdkaws.String(bacBuiltinEvalARN), EvaluatorId: sdkaws.String("Builtin.Helpfulness"), EvaluatorType: bactypes.EvaluatorTypeBuiltin,
				})
			}
			return out
		}),
		"ListMemories": bacTwoPages(c, "ListMemories", func(n string, next *string) any {
			return &bac.ListMemoriesOutput{NextToken: next, Memories: []bactypes.MemorySummary{{Arn: sdkaws.String(bacTestARN("memory", n)), Id: sdkaws.String(n)}}}
		}),
		"ListOnlineEvaluationConfigs": bacTwoPages(c, "ListOnlineEvaluationConfigs", func(n string, next *string) any {
			return &bac.ListOnlineEvaluationConfigsOutput{NextToken: next, OnlineEvaluationConfigs: []bactypes.OnlineEvaluationConfigSummary{
				{OnlineEvaluationConfigArn: sdkaws.String(bacTestARN("online-evaluation-config", n)), OnlineEvaluationConfigId: sdkaws.String(n)},
			}}
		}),
		"ListPolicyEngines": bacTwoPages(c, "ListPolicyEngines", func(n string, next *string) any {
			return &bac.ListPolicyEnginesOutput{NextToken: next, PolicyEngines: []bactypes.PolicyEngine{
				{PolicyEngineArn: sdkaws.String(bacTestARN("policy-engine", n)), PolicyEngineId: sdkaws.String(n)},
			}}
		}),
		"ListAgentRuntimes": bacTwoPages(c, "ListAgentRuntimes", func(n string, next *string) any {
			return &bac.ListAgentRuntimesOutput{NextToken: next, AgentRuntimes: []bactypes.AgentRuntime{
				{AgentRuntimeArn: sdkaws.String(bacTestARN("runtime", n)), AgentRuntimeId: sdkaws.String(n)},
			}}
		}),
		"ListAgentRuntimeEndpoints": func(in any) (any, error) {
			rt := sv(in.(*bac.ListAgentRuntimeEndpointsInput).AgentRuntimeId)
			return bacTwoPages(c, "ListAgentRuntimeEndpoints", func(n string, next *string) any {
				return &bac.ListAgentRuntimeEndpointsOutput{NextToken: next, RuntimeEndpoints: []bactypes.AgentRuntimeEndpoint{
					{AgentRuntimeEndpointArn: sdkaws.String(bacTestARN("runtime/"+rt+"/runtime-endpoint", n)), Id: sdkaws.String(n)},
				}}
			})(in)
		},
		"ListWorkloadIdentities": bacTwoPages(c, "ListWorkloadIdentities", func(n string, next *string) any {
			return &bac.ListWorkloadIdentitiesOutput{NextToken: next, WorkloadIdentities: []bactypes.WorkloadIdentityType{
				{WorkloadIdentityArn: sdkaws.String(bacTestARN("workload-identity-directory/default/workload-identity", n)), Name: sdkaws.String(n)},
			}}
		}),
		"ListHarnesses": bacTwoPages(c, "ListHarnesses", func(n string, next *string) any {
			return &bac.ListHarnessesOutput{NextToken: next, Harnesses: []bactypes.HarnessSummary{{Arn: sdkaws.String(bacTestARN("harness", n)), HarnessId: sdkaws.String(n)}}}
		}),
		"ListHarnessEndpoints": func(in any) (any, error) {
			h := sv(in.(*bac.ListHarnessEndpointsInput).HarnessId)
			return bacTwoPages(c, "ListHarnessEndpoints", func(n string, next *string) any {
				return &bac.ListHarnessEndpointsOutput{NextToken: next, Endpoints: []bactypes.HarnessEndpoint{
					{Arn: sdkaws.String(bacTestARN("harness/"+h+"/harness-endpoint", n)), EndpointName: sdkaws.String(n), HarnessId: sdkaws.String(h)},
				}}
			})(in)
		},
		"ListPaymentManagers": bacTwoPages(c, "ListPaymentManagers", func(n string, next *string) any {
			return &bac.ListPaymentManagersOutput{NextToken: next, PaymentManagers: []bactypes.PaymentManagerSummary{
				{PaymentManagerArn: sdkaws.String(bacTestARN("payment-manager", n)), PaymentManagerId: sdkaws.String(n), Status: bactypes.PaymentManagerStatusReady},
			}}
		}),
		"ListPaymentCredentialProviders": bacTwoPages(c, "ListPaymentCredentialProviders", func(n string, next *string) any {
			return &bac.ListPaymentCredentialProvidersOutput{NextToken: next, CredentialProviders: []bactypes.PaymentCredentialProviderItem{
				{CredentialProviderArn: sdkaws.String(bacTestARN("token-vault/default/paymentcredentialprovider", n)), Name: sdkaws.String(n)},
			}}
		}),
		"ListCapacityProviders": bacTwoPages(c, "ListCapacityProviders", func(n string, next *string) any {
			return &bac.ListCapacityProvidersOutput{NextToken: next, CapacityProviders: []bactypes.CapacityProviderSummary{
				{CapacityProviderArn: sdkaws.String(bacTestARN("capacity-provider", n)), CapacityProviderId: sdkaws.String(n)},
			}}
		}),
		"ListGateways": bacTwoPages(c, "ListGateways", func(n string, next *string) any {
			return &bac.ListGatewaysOutput{NextToken: next, Items: []bactypes.GatewaySummary{{GatewayId: sdkaws.String(n), Name: sdkaws.String(n)}}}
		}),
		"ListConsentPortals": bacTwoPages(c, "ListConsentPortals", func(n string, next *string) any {
			return &bac.ListConsentPortalsOutput{NextToken: next, ConsentPortals: []bactypes.ConsentPortalSummary{
				{ConsentPortalArn: sdkaws.String(bacTestARN("consent-portal", n)), ConsentPortalId: sdkaws.String(n)},
			}}
		}),
		"ListConfigurationBundles": bacTwoPages(c, "ListConfigurationBundles", func(n string, next *string) any {
			return &bac.ListConfigurationBundlesOutput{NextToken: next, Bundles: []bactypes.ConfigurationBundleSummary{
				{BundleArn: sdkaws.String(bacTestARN("configuration-bundle", n)), BundleId: sdkaws.String(n)},
			}}
		}),
		"ListDatasets": bacTwoPages(c, "ListDatasets", func(n string, next *string) any {
			return &bac.ListDatasetsOutput{NextToken: next, Datasets: []bactypes.DatasetSummary{
				{DatasetArn: sdkaws.String(bacTestARN("dataset", n)), DatasetId: sdkaws.String(n)},
			}}
		}),
		"ListRegistries": bacTwoPages(c, "ListRegistries", func(n string, next *string) any {
			return &bac.ListRegistriesOutput{NextToken: next, Registries: []bactypes.RegistrySummary{
				{RegistryArn: sdkaws.String(bacTestARN("registry", n)), RegistryId: sdkaws.String(n)},
			}}
		}),
		"ListABTests": bacTwoPages(c, "ListABTests", func(n string, next *string) any {
			return &bacdata.ListABTestsOutput{NextToken: next, AbTests: []bacdatatypes.ABTestSummary{
				{AbTestArn: sdkaws.String(bacTestARN("ab-test", n)), AbTestId: sdkaws.String(n)},
			}}
		}),
		"ListGatewayTargets": c.answer("ListGatewayTargets", func(in any) any {
			return &bac.ListGatewayTargetsOutput{Items: []bactypes.TargetSummary{{TargetId: sdkaws.String("t")}}}
		}),
		"ListGatewayRules": c.answer("ListGatewayRules", func(in any) any {
			return &bac.ListGatewayRulesOutput{GatewayRules: []bactypes.GatewayRuleDetail{{RuleId: sdkaws.String("rule")}}}
		}),
		"ListGatewayRateLimits": c.answer("ListGatewayRateLimits", func(in any) any {
			return &bac.ListGatewayRateLimitsOutput{RateLimits: []bactypes.GatewayRateLimitDetail{{RateLimitId: sdkaws.String("limit")}}}
		}),
		"ListPolicies": c.answer("ListPolicies", func(in any) any {
			e := sv(in.(*bac.ListPoliciesInput).PolicyEngineId)
			return &bac.ListPoliciesOutput{Policies: []bactypes.Policy{{PolicyArn: sdkaws.String(bacTestARN("policy-engine/"+e+"/policy", "pol")), PolicyId: sdkaws.String("pol")}}}
		}),
		"ListPolicyGenerations": c.answer("ListPolicyGenerations", func(in any) any {
			e := sv(in.(*bac.ListPolicyGenerationsInput).PolicyEngineId)
			return &bac.ListPolicyGenerationsOutput{PolicyGenerations: []bactypes.PolicyGeneration{
				{PolicyGenerationArn: sdkaws.String(bacTestARN("policy-engine/"+e+"/policy-generation", "gen")), PolicyGenerationId: sdkaws.String("gen")},
			}}
		}),
		"ListRegistryRecords": c.answer("ListRegistryRecords", func(in any) any {
			r := sv(in.(*bac.ListRegistryRecordsInput).RegistryId)
			return &bac.ListRegistryRecordsOutput{RegistryRecords: []bactypes.RegistryRecordSummary{
				{RecordArn: sdkaws.String(bacTestARN("registry/"+r+"/record", "rec")), RecordId: sdkaws.String("rec")},
			}}
		}),
		"ListPaymentConnectors": c.answer("ListPaymentConnectors", func(in any) any {
			return &bac.ListPaymentConnectorsOutput{PaymentConnectors: []bactypes.PaymentConnectorSummary{{PaymentConnectorId: sdkaws.String("con")}}}
		}),
	}
}

// bacTaggedARNs is every fixture resource of a tagged type, tagging each
// "tagged" one with its type.
func bacTaggedARNs(c *bacCalls) []string {
	var arns []string
	for _, f := range bacTaggedTypes() {
		c.tags[bacTestARN(f.kind, "tagged")] = map[string]string{"type": f.typ}
		arns = append(arns, bacTestARN(f.kind, "tagged"), bacTestARN(f.kind, "bare"))
	}
	slices.Sort(arns)
	return arns
}

// bacScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func bacScan(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	return bacScanCtx(context.Background(), t, st, handlers)
}

func bacScanCtx(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock-agentcore:Op"))
	return scanBedrockAgentCore(ctx, acct, testRegion, st, testScanID)
}

func bacRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// bacWarnings collects the warnings sent under op.
func bacWarnings(st *store.Store, op string) *[]store.ScanWarning {
	var (
		mu   sync.Mutex
		got  []store.ScanWarning
		prev = st.OnWarn
	)
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service == op {
			mu.Lock()
			got = append(got, w)
			mu.Unlock()
		}
		if prev != nil {
			prev(w)
		}
	}
	return &got
}

// Every tagged type stores the tags ListTagsForResource returns for the
// resource's own ARN, and nil for a resource without tags; the built-in rows
// and the untagged types are never sent to it. Both pages of every listing
// are stored, each listing asks for its model's page cap, and the scan
// reports no error or warning.
func TestScanBedrockAgentCore_StoresTagsOfEveryTaggedType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newBACCalls()
	wantARNs := bacTaggedARNs(calls)

	total, inserted, err := bacScan(t, st, bacHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := len(wantARNs) + bacFixtureRows; total != want || inserted != want {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, want, want)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, f := range bacTaggedTypes() {
		tagged, ok := bacRowOf(t, st, f.typ, bacTestARN(f.kind, "tagged"))
		if !ok {
			t.Fatalf("%s tagged row missing", f.typ)
		}
		assertTagsMap(t, tagged.TagsJSON, map[string]string{"type": f.typ})
		bare, ok := bacRowOf(t, st, f.typ, bacTestARN(f.kind, "bare"))
		if !ok {
			t.Fatalf("%s bare row missing", f.typ)
		}
		if bare.TagsJSON != nil {
			t.Errorf("%s bare TagsJSON = %q; want nil", f.typ, *bare.TagsJSON)
		}
	}
	for _, c := range []struct{ typ, arn string }{
		{TypeBedrockAgentCoreBrowser, bacSystemBrowserARN},
		{TypeBedrockAgentCoreCodeInterpreter, bacSystemCIARN},
		{TypeBedrockAgentCoreEvaluator, bacBuiltinEvalARN},
		{TypeBedrockAgentCoreRegistry, bacTestARN("registry", "bare")},
		{TypeBedrockAgentCoreABTest, bacTestARN("ab-test", "bare")},
	} {
		if _, ok := bacRowOf(t, st, c.typ, c.arn); !ok {
			t.Errorf("%s %s missing", c.typ, c.arn)
		}
	}
	for _, c := range bacChildren() {
		if _, ok := bacRowOf(t, st, c.typ, c.siblingARN); !ok {
			t.Errorf("%s %s missing", c.typ, c.siblingARN)
		}
	}
	if m, _ := bacRowOf(t, st, TypeBedrockAgentCorePaymentManager, bacTestARN("payment-manager", "bare")); sv(m.Status) != "READY" {
		t.Errorf("payment manager Status = %q; want READY", sv(m.Status))
	}
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	wantCaps := map[string]int32{
		"ListApiKeyCredentialProviders": 100, "ListOauth2CredentialProviders": 20, "ListBrowsers": 100,
		"ListBrowserProfiles": 100, "ListCodeInterpreters": 100, "ListEvaluators": 100, "ListMemories": 100,
		"ListOnlineEvaluationConfigs": 100, "ListPolicyEngines": 100, "ListAgentRuntimes": 100,
		"ListAgentRuntimeEndpoints": 100, "ListWorkloadIdentities": 20, "ListHarnesses": 100,
		"ListPaymentManagers": 100, "ListPaymentCredentialProviders": 20, "ListCapacityProviders": 100,
		"ListGateways": 1000, "ListGatewayTargets": 1000, "ListGatewayRules": 100, "ListGatewayRateLimits": 100,
		"ListPolicies": 100, "ListPolicyGenerations": 100, "ListHarnessEndpoints": 100, "ListRegistries": 100,
		"ListRegistryRecords": 100, "ListPaymentConnectors": 100, "ListConsentPortals": 100,
		"ListConfigurationBundles": 100, "ListDatasets": 100, "ListABTests": 100,
	}
	for op, want := range wantCaps {
		got := calls.maxResults[op]
		if len(got) == 0 {
			t.Errorf("%s never called", op)
		}
		for _, n := range got {
			if n != want {
				t.Errorf("%s MaxResults = %d; want %d", op, n, want)
			}
		}
	}
	if extra := slices.Collect(maps.Keys(calls.maxResults)); len(extra) != len(wantCaps) {
		t.Errorf("ops that sent MaxResults = %v; want only %v", extra, slices.Collect(maps.Keys(wantCaps)))
	}
}

// A gateway's list element carries no ARN; the NativeID built from its id is
// the GatewayArn GetGateway returns, and the tag read is sent it.
func TestScanBedrockAgentCore_GatewayARNMatchesGetGateway(t *testing.T) {
	got := bac.GetGatewayOutput{GatewayArn: sdkaws.String("arn:aws:bedrock-agentcore:us-east-1:123456789012:gateway/gw-abc123")}
	st := newTestStore(t)
	calls := newBACCalls()
	handlers := map[string]func(any) (any, error){
		"ListTagsForResource": calls.listTags,
		"ListGateways": calls.answer("ListGateways", func(any) any {
			return &bac.ListGatewaysOutput{Items: []bactypes.GatewaySummary{{GatewayId: sdkaws.String("gw-abc123")}}}
		}),
	}
	if _, _, err := bacScan(t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, ok := bacRowOf(t, st, TypeBedrockAgentCoreGateway, *got.GatewayArn); !ok {
		t.Errorf("gateway rows = %v; want one under %s", slices.Collect(maps.Keys(rowsByNativeID(t, st, TypeBedrockAgentCoreGateway))), *got.GatewayArn)
	}
	if !slices.Equal(calls.tagARNs, []string{*got.GatewayArn}) {
		t.Errorf("ListTagsForResource ARNs = %v; want [%s]", calls.tagARNs, *got.GatewayArn)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanBedrockAgentCore_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	bacTaggedARNs(calls)
	if _, _, err := bacScan(t, st, bacHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := bacScan(t, st, bacHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total == 0 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want >0, 0", total, inserted)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under the tag op, a throttle after retries is a
// transient error, any other code a hard one. A runtime the tag read no
// longer finds is dropped with its endpoints never listed; the same holds
// for an endpoint, read in the child phase.
func TestScanBedrockAgentCore_TagReadFailures(t *testing.T) {
	for _, target := range []struct{ typ, arn string }{
		{TypeBedrockAgentCoreRuntime, bacTestARN("runtime", "tagged")},
		{TypeBedrockAgentCoreRuntimeEndpoint, bacTestARN("runtime/bare/runtime-endpoint", "tagged")},
	} {
		t.Run(target.typ, func(t *testing.T) { bacTagReadFailures(t, target.typ, target.arn) })
	}
}

func bacTagReadFailures(t *testing.T, typ, arn string) {
	cases := []struct {
		name      string
		err       error
		kept      bool
		fails     bool
		outcome   serviceOutcome
		wantWarns int
	}{
		{"denied", apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock-agentcore:ListTagsForResource"), true, false, 0, 1},
		{"not found", apiErr("ResourceNotFoundException", "gone"), false, false, 0, 0},
		{"other error", apiErr("ValidationException", "bad"), true, true, outcomeError, 0},
		{"transient", apiErr("ThrottlingException", "Rate exceeded"), true, true, outcomeTransient, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, bacListTagsOp)
			calls := newBACCalls()
			bacTaggedARNs(calls)
			calls.errs[arn] = c.err

			_, _, err := bacScan(t, st, bacHandlers(calls))
			if !c.fails && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.fails {
				if got := classifyServiceError(err); got != c.outcome {
					t.Errorf("classifyServiceError = %v; want %v (err %v)", got, c.outcome, err)
				}
			}
			if c.outcome == outcomeError {
				assertAPICode(t, err, "ValidationException")
			}
			r, ok := bacRowOf(t, st, typ, arn)
			if ok != c.kept {
				t.Fatalf("%s row stored = %v; want %v", typ, ok, c.kept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if _, ok := bacRowOf(t, st, TypeBedrockAgentCoreRuntimeEndpoint, bacTestARN("runtime/bare/runtime-endpoint", "bare")); !ok {
				t.Error("an endpoint whose tag read succeeded is missing")
			}
			if typ == TypeBedrockAgentCoreRuntime {
				_, listed := bacRowOf(t, st, TypeBedrockAgentCoreRuntimeEndpoint, bacTestARN("runtime/tagged/runtime-endpoint", "tagged"))
				if listed != c.kept {
					t.Errorf("endpoints of the runtime listed = %v; want %v", listed, c.kept)
				}
			}
			if len(*warnings) != c.wantWarns {
				t.Fatalf("%s warnings = %v; want %d", bacListTagsOp, *warnings, c.wantWarns)
			}
			for _, w := range *warnings {
				if w.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A throttled tag read beside a hard tag-read failure is demoted to one
// warning, so the hard error decides the outcome.
func TestScanBedrockAgentCore_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, bacListTagsOp)
	calls := newBACCalls()
	bacTaggedARNs(calls)
	calls.errs[bacTestARN("runtime", "tagged")] = apiErr("ThrottlingException", "Rate exceeded")
	calls.errs[bacTestARN("memory", "tagged")] = apiErr("ValidationException", "bad")

	_, _, err := bacScan(t, st, bacHandlers(calls))
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
	}
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", bacListTagsOp, *warns)
	}
}

// A failed write stops the scan before any later listing, the other kind of
// the same op included, and sends no warning about the rows it lost: not
// the tag denial, and not a child listing's denial.
func TestScanBedrockAgentCore_FailedWriteStopsWithoutWarning(t *testing.T) {
	tagDenied := apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock-agentcore:ListTagsForResource")
	for _, tc := range []struct {
		typ, deniedARN, childOp, parentField string
		calls                                map[string]int
	}{
		{TypeBedrockAgentCoreRuntime, bacTestARN("runtime", "tagged"), "", "", map[string]int{"ListPolicyEngines": 0}},
		{TypeBedrockAgentCoreRuntimeEndpoint, bacTestARN("runtime/tagged/runtime-endpoint", "tagged"), "ListAgentRuntimeEndpoints", "AgentRuntimeId", map[string]int{"ListApiKeyCredentialProviders": 0}},
		{TypeBedrockAgentCoreHarnessEndpoint, bacTestARN("harness/tagged/harness-endpoint", "tagged"), "ListHarnessEndpoints", "HarnessId", map[string]int{"ListRegistryRecords": 0}},
		// Two pages of CUSTOM browsers, and the SYSTEM listing never called.
		{TypeBedrockAgentCoreBrowserCustom, bacTestARN("browser-custom", "tagged"), "", "", map[string]int{"ListBrowsers": 2, "ListBrowserProfiles": 0}},
		// The A/B tests are untagged, so no tag read is denied.
		{TypeBedrockAgentCoreABTest, "", "", "", map[string]int{"ListGatewayRules": 0}},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			refuseStoreWrites(t, st, tc.typ)
			calls := newBACCalls()
			bacTaggedARNs(calls)
			calls.errs[tc.deniedARN] = tagDenied
			handlers := bacHandlers(calls)
			if tc.childOp != "" {
				next := handlers[tc.childOp]
				handlers[tc.childOp] = func(in any) (any, error) {
					if sv(bacField[*string](in, tc.parentField)) == "bare" {
						return nil, apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock-agentcore:"+tc.childOp)
					}
					return next(in)
				}
			}

			_, _, err := bacScan(t, st, handlers)
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

// An UnauthorizedException after a page is not warned about when the write
// of that page fails.
func TestScanBedrockAgentCore_FailedWriteSendsNoUnauthorizedWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeBedrockAgentCoreWorkloadIdentity)
	calls := newBACCalls()
	bacTaggedARNs(calls)
	handlers := bacHandlers(calls)
	handlers["ListWorkloadIdentities"] = failOnCall(2, apiErr("UnauthorizedException", "JWT bearer token invalid or not found"), handlers["ListWorkloadIdentities"])

	_, _, err := bacScan(t, st, handlers)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
}

// A listing's failed page keeps the pages read before it, and neither a list
// failure nor a gateway failure stops the phases after it.
func TestScanBedrockAgentCore_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	bacTaggedARNs(calls)
	handlers := bacHandlers(calls)
	handlers["ListGateways"] = func(any) (any, error) { return nil, apiErr("ValidationException", "gateways") }
	memories := handlers["ListMemories"]
	handlers["ListMemories"] = func(in any) (any, error) {
		if in.(*bac.ListMemoriesInput).NextToken != nil {
			return nil, apiErr("InvalidParameterException", "page 2")
		}
		return memories(in)
	}
	handlers["ListPolicies"] = func(any) (any, error) { return nil, apiErr("ConflictException", "policies") }

	_, _, err := bacScan(t, st, handlers)
	for _, code := range []string{"ValidationException", "InvalidParameterException", "ConflictException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	r, ok := bacRowOf(t, st, TypeBedrockAgentCoreMemory, bacTestARN("memory", "tagged"))
	if !ok {
		t.Fatal("memory from the page before the failure missing")
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeBedrockAgentCoreMemory})
	for _, c := range []struct{ typ, arn string }{
		{TypeBedrockAgentCoreRuntime, bacTestARN("runtime", "bare")},
		{TypeBedrockAgentCoreWorkloadIdentity, bacTestARN("workload-identity-directory/default/workload-identity", "bare")},
		{TypeBedrockAgentCoreCapacityProvider, bacTestARN("capacity-provider", "bare")},
		{TypeBedrockAgentCoreConsentPortal, bacTestARN("consent-portal", "bare")},
		{TypeBedrockAgentCoreABTest, bacTestARN("ab-test", "bare")},
	} {
		if _, ok := bacRowOf(t, st, c.typ, c.arn); !ok {
			t.Errorf("%s %s missing after the earlier failures", c.typ, c.arn)
		}
	}
}

// failOnCall answers call n (from 1) of a listing with err, the others as
// next does.
func failOnCall(n int, err error, next func(any) (any, error)) func(any) (any, error) {
	var mu sync.Mutex
	calls := 0
	return func(in any) (any, error) {
		mu.Lock()
		calls++
		this := calls
		mu.Unlock()
		if this == n {
			return nil, err
		}
		return next(in)
	}
}

// A region-gap code on a listing's first page means the op is not deployed
// in the region: no row, no error, no warning. Any other first-page error is
// returned, and so is a region-gap code of an op that has none. After a page
// was read a region-gap code is an error too, returned with that page's rows
// stored; the gateway's "Unable to determine" denial included, which a
// denial path would otherwise drop.
func TestScanBedrockAgentCore_RegionGapOnlyOnFirstPage(t *testing.T) {
	unknownOp := apiErr("UnknownOperationException", "gap")
	authorizer := apiErr("AuthorizerConfigurationException", "Internal server error")
	deniedGap := apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")
	notFoundGap := apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")
	other := apiErr("ValidationException", "bad")
	type listing struct{ op, typ, kind string }
	codeGap := []listing{
		{"ListHarnesses", TypeBedrockAgentCoreHarness, "harness"},
		{"ListPaymentManagers", TypeBedrockAgentCorePaymentManager, "payment-manager"},
		{"ListPaymentCredentialProviders", TypeBedrockAgentCorePaymentCredentialProvider, "token-vault/default/paymentcredentialprovider"},
		{"ListCapacityProviders", TypeBedrockAgentCoreCapacityProvider, "capacity-provider"},
		{"ListConsentPortals", TypeBedrockAgentCoreConsentPortal, "consent-portal"},
		{"ListConfigurationBundles", TypeBedrockAgentCoreConfigurationBundle, "configuration-bundle"},
		{"ListDatasets", TypeBedrockAgentCoreDataset, "dataset"},
		{"ListRegistries", TypeBedrockAgentCoreRegistry, "registry"},
		{"ListABTests", TypeBedrockAgentCoreABTest, "ab-test"},
	}
	denialGap := []listing{
		{"ListEvaluators", TypeBedrockAgentCoreEvaluator, "evaluator"},
		{"ListOnlineEvaluationConfigs", TypeBedrockAgentCoreOnlineEvaluationConfig, "online-evaluation-config"},
	}
	type tcase struct {
		listing
		call    int
		err     error
		wantErr bool
	}
	var cases []tcase
	for _, l := range codeGap {
		cases = append(cases, tcase{l, 1, unknownOp, false}, tcase{l, 1, authorizer, false}, tcase{l, 1, other, true}, tcase{l, 2, unknownOp, true})
	}
	for _, l := range denialGap {
		cases = append(cases, tcase{l, 1, deniedGap, false}, tcase{l, 1, notFoundGap, false}, tcase{l, 1, other, true}, tcase{l, 2, deniedGap, true}, tcase{l, 2, notFoundGap, true})
	}
	cases = append(cases, tcase{listing{"ListMemories", TypeBedrockAgentCoreMemory, "memory"}, 1, unknownOp, true})
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s/call %d/%v", c.op, c.call, c.err), func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			calls := newBACCalls()
			bacTaggedARNs(calls)
			handlers := bacHandlers(calls)
			handlers[c.op] = failOnCall(c.call, c.err, handlers[c.op])

			_, _, err := bacScan(t, st, handlers)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v; want an error: %v", err, c.wantErr)
			}
			if c.wantErr && classifyServiceError(err) != outcomeError {
				t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", classifyServiceError(err), err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			_, ok := bacRowOf(t, st, c.typ, bacTestARN(c.kind, "tagged"))
			if want := c.call == 2; ok != want {
				t.Errorf("first-page %s stored = %v; want %v", c.typ, ok, want)
			}
		})
	}
}

// The ops that model UnauthorizedException (a bearer token missing or
// invalid) treat it as a denial: the listing ends with the pages read before
// it stored, it is warned about under the op, and it is not an error.
func TestScanBedrockAgentCore_UnauthorizedIsDenial(t *testing.T) {
	unauthorized := apiErr("UnauthorizedException", "JWT bearer token invalid or not found")
	for _, l := range []struct{ op, typ, kind string }{
		{"ListApiKeyCredentialProviders", TypeBedrockAgentCoreAPIKeyCredentialProvider, "token-vault/default/apikeycredentialprovider"},
		{"ListOauth2CredentialProviders", TypeBedrockAgentCoreOAuth2CredentialProvider, "token-vault/default/oauth2credentialprovider"},
		{"ListWorkloadIdentities", TypeBedrockAgentCoreWorkloadIdentity, "workload-identity-directory/default/workload-identity"},
		{"ListPaymentCredentialProviders", TypeBedrockAgentCorePaymentCredentialProvider, "token-vault/default/paymentcredentialprovider"},
		{"ListConsentPortals", TypeBedrockAgentCoreConsentPortal, "consent-portal"},
		{"ListABTests", TypeBedrockAgentCoreABTest, "ab-test"},
	} {
		for _, call := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/call %d", l.op, call), func(t *testing.T) {
				st := newTestStore(t)
				warnings := bacWarnings(st, "bedrockagentcore:"+l.op)
				calls := newBACCalls()
				bacTaggedARNs(calls)
				handlers := bacHandlers(calls)
				handlers[l.op] = failOnCall(call, unauthorized, handlers[l.op])

				if _, _, err := bacScan(t, st, handlers); err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if len(*warnings) != 1 {
					t.Fatalf("warnings = %v; want 1", *warnings)
				}
				if (*warnings)[0].Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope = %q; want %s/%s", (*warnings)[0].Scope, testAccountID, testRegion)
				}
				rows := rowsByNativeID(t, st, l.typ)
				if _, ok := rows[bacTestARN(l.kind, "tagged")]; ok != (call == 2) {
					t.Errorf("first-page row stored = %v; want %v", ok, call == 2)
				}
				if _, ok := rows[bacTestARN(l.kind, "bare")]; ok {
					t.Error("a page after the refused one was stored")
				}
			})
		}
	}
}

// One parent's failed child listing does not stop its siblings: a parent the
// op's model says was deleted is skipped silently, a denied one is warned
// about once under the op, any other error is returned. An op whose model
// lists no not-found code returns ResourceNotFoundException as an error. A
// region-gap code is silent only for the ops that are not deployed
// everywhere.
func TestScanBedrockAgentCore_ChildListingFailures(t *testing.T) {
	for _, c := range bacChildren() {
		gapSilent := c.op == "ListGatewayRules" || c.op == "ListGatewayRateLimits"
		for _, tc := range []struct {
			name      string
			err       error
			wantCode  string
			wantWarns int
		}{
			{"not found", apiErr("ResourceNotFoundException", "gone"), map[bool]string{true: "", false: "ResourceNotFoundException"}[c.notFound], 0},
			{"denied", apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock-agentcore:"+c.op), "", 1},
			{"other", apiErr("ValidationException", "bad"), "ValidationException", 0},
			{"region gap", apiErr("UnknownOperationException", "gap"), map[bool]string{true: "", false: "UnknownOperationException"}[gapSilent], 0},
		} {
			t.Run(c.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "bedrockagentcore:"+c.op)
				calls := newBACCalls()
				bacTaggedARNs(calls)
				handlers := bacHandlers(calls)
				next := handlers[c.op]
				handlers[c.op] = func(in any) (any, error) {
					if sv(bacField[*string](in, c.parentField)) == "tagged" {
						return nil, tc.err
					}
					return next(in)
				}

				_, _, err := bacScan(t, st, handlers)
				if tc.wantCode == "" && err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if tc.wantCode != "" && !joinCarriesAPIErrorCode(err, tc.wantCode) {
					t.Fatalf("err = %v; want it to carry %s", err, tc.wantCode)
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
				}
				if _, ok := bacRowOf(t, st, c.typ, c.siblingARN); !ok {
					t.Errorf("sibling's %s %s missing", c.typ, c.siblingARN)
				}
			})
		}
	}
}

// Items that cannot be stored are warned about in one warning per op that
// names the first, or says it has no id; their siblings are stored.
func TestScanBedrockAgentCore_UnstorableItemsWarn(t *testing.T) {
	gatewayOnly := func(out any) func(in any) (any, error) {
		return func(in any) (any, error) {
			if sv(bacField[*string](in, "GatewayIdentifier")) != "tagged" {
				return reflect.New(reflect.TypeOf(out).Elem()).Interface(), nil
			}
			return out, nil
		}
	}
	for _, tc := range []struct {
		name, op, key, typ string
		stored             int
		answer             func(in any) (any, error)
	}{
		{"memory without an ARN first", "ListMemories", "no-arn", TypeBedrockAgentCoreMemory, 1, func(any) (any, error) {
			return &bac.ListMemoriesOutput{Memories: []bactypes.MemorySummary{
				{Arn: sdkaws.String(bacTestARN("memory", "tagged")), Id: sdkaws.String("tagged")}, {Id: sdkaws.String("no-arn")}, {},
			}}, nil
		}},
		{"memory without an ARN or id first", "ListMemories", "(an item with no id)", TypeBedrockAgentCoreMemory, 1, func(any) (any, error) {
			return &bac.ListMemoriesOutput{Memories: []bactypes.MemorySummary{
				{Arn: sdkaws.String(bacTestARN("memory", "tagged")), Id: sdkaws.String("tagged")}, {}, {Id: sdkaws.String("no-arn")},
			}}, nil
		}},
		{"gateway without an id", "ListGateways", "nameless", TypeBedrockAgentCoreGateway, 1, func(any) (any, error) {
			return &bac.ListGatewaysOutput{Items: []bactypes.GatewaySummary{{GatewayId: sdkaws.String("tagged")}, {Name: sdkaws.String("nameless")}}}, nil
		}},
		{"gateway target without an id", "ListGatewayTargets", "tn", TypeBedrockAgentCoreGatewayTarget, 1, gatewayOnly(&bac.ListGatewayTargetsOutput{Items: []bactypes.TargetSummary{
			{TargetId: sdkaws.String("t")}, {Name: sdkaws.String("tn")},
		}})},
		{"gateway rule without an id", "ListGatewayRules", "(a rule with no id) of gateway tagged", TypeBedrockAgentCoreGatewayRule, 0, gatewayOnly(&bac.ListGatewayRulesOutput{GatewayRules: []bactypes.GatewayRuleDetail{{}}})},
		{"gateway rate limit without an id", "ListGatewayRateLimits", "(a rate limit with no id) of gateway tagged", TypeBedrockAgentCoreGatewayRateLimit, 0, gatewayOnly(&bac.ListGatewayRateLimitsOutput{RateLimits: []bactypes.GatewayRateLimitDetail{{}}})},
		{"payment connector without an id", "ListPaymentConnectors", "cn", TypeBedrockAgentCorePaymentConnector, 0, func(any) (any, error) {
			return &bac.ListPaymentConnectorsOutput{PaymentConnectors: []bactypes.PaymentConnectorSummary{{Name: sdkaws.String("cn")}}}, nil
		}},
		{"harness endpoint without an ARN", "ListHarnessEndpoints", "en", TypeBedrockAgentCoreHarnessEndpoint, 0, func(any) (any, error) {
			return &bac.ListHarnessEndpointsOutput{Endpoints: []bactypes.HarnessEndpoint{{EndpointName: sdkaws.String("en")}}}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "bedrockagentcore:"+tc.op)
			calls := newBACCalls()
			bacTaggedARNs(calls)
			handlers := bacHandlers(calls)
			handlers[tc.op] = tc.answer

			if _, _, err := bacScan(t, st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := len(rowsByNativeID(t, st, tc.typ)); got != tc.stored {
				t.Errorf("%s stored = %d; want %d", tc.typ, got, tc.stored)
			}
			if len(*warnings) != 1 {
				t.Fatalf("warnings = %v; want 1", *warnings)
			}
			w := (*warnings)[0]
			if !strings.HasSuffix(w.Message, "first "+tc.key) {
				t.Errorf("warning = %q; want it to name %q first", w.Message, tc.key)
			}
			if w.Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanBedrockAgentCore_RepeatedTokenKeepsPages(t *testing.T) {
	for _, tc := range []struct {
		op, typ, arn string
		out          any
	}{
		{
			"ListWorkloadIdentities", TypeBedrockAgentCoreWorkloadIdentity, bacTestARN("workload-identity-directory/default/workload-identity", "tagged"),
			&bac.ListWorkloadIdentitiesOutput{NextToken: sdkaws.String("same"), WorkloadIdentities: []bactypes.WorkloadIdentityType{
				{WorkloadIdentityArn: sdkaws.String(bacTestARN("workload-identity-directory/default/workload-identity", "tagged")), Name: sdkaws.String("tagged")},
			}},
		},
		{
			"ListRegistries", TypeBedrockAgentCoreRegistry, bacTestARN("registry", "tagged"),
			&bac.ListRegistriesOutput{NextToken: sdkaws.String("same"), Registries: []bactypes.RegistrySummary{
				{RegistryArn: sdkaws.String(bacTestARN("registry", "tagged")), RegistryId: sdkaws.String("tagged")},
			}},
		},
		{
			"ListABTests", TypeBedrockAgentCoreABTest, bacTestARN("ab-test", "tagged"),
			&bacdata.ListABTestsOutput{NextToken: sdkaws.String("same"), AbTests: []bacdatatypes.ABTestSummary{
				{AbTestArn: sdkaws.String(bacTestARN("ab-test", "tagged")), AbTestId: sdkaws.String("tagged")},
			}},
		},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newBACCalls()
			bacTaggedARNs(calls)
			handlers := bacHandlers(calls)
			handlers[tc.op] = func(any) (any, error) { return tc.out, nil }

			_, _, err := bacScan(t, st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := bacRowOf(t, st, tc.typ, tc.arn); !ok {
				t.Errorf("%s from the pages read missing", tc.typ)
			}
		})
	}
}

// bacTopMembers is the members of err's top-level join, or err alone.
func bacTopMembers(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run, although the phase in flight saw it in its
// listing and its tag reads; a hard error beside the stop is kept.
func TestScanBedrockAgentCore_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newBACCalls()
	bacTaggedARNs(calls)
	handlers := bacHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The runtime "bare" ends on its second page, as an SDK call does when
	// ctx ends; its first page's endpoint is stored, so the tag reads after
	// it see the stop too.
	endpoints := handlers["ListAgentRuntimeEndpoints"]
	handlers["ListAgentRuntimeEndpoints"] = func(in any) (any, error) {
		req := in.(*bac.ListAgentRuntimeEndpointsInput)
		switch {
		case sv(req.AgentRuntimeId) == "tagged":
			return nil, apiErr("ValidationException", "bad")
		case req.NextToken != nil:
			cancel()
			return nil, context.Canceled
		}
		return endpoints(in)
	}

	_, _, err := bacScanCtx(ctx, t, st, handlers)
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
	if n := calls.count("ListApiKeyCredentialProviders"); n != 0 {
		t.Errorf("ListApiKeyCredentialProviders called %d times after ctx ended", n)
	}
	if !joinCarriesAPIErrorCode(err, "ValidationException") {
		t.Errorf("err = %v; want the ValidationException beside the stop kept", err)
	}
	// Endpoints is phase 8 of 30, so 22 never started.
	if !strings.Contains(err.Error(), "22 phases not run") {
		t.Errorf("err = %v; want it to say 22 phases not run", err)
	}
}

// A runtime whose endpoint listing is denied is warned about once; the other
// runtime's endpoints are stored with their tags.
func TestScanBedrockAgentCore_RuntimeEndpointDenialKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "bedrockagentcore:ListAgentRuntimeEndpoints")
	calls := newBACCalls()
	bacTaggedARNs(calls)
	handlers := bacHandlers(calls)
	endpoints := handlers["ListAgentRuntimeEndpoints"]
	handlers["ListAgentRuntimeEndpoints"] = func(in any) (any, error) {
		if sv(in.(*bac.ListAgentRuntimeEndpointsInput).AgentRuntimeId) == "bare" {
			return nil, apiErr("AccessDeniedException", "User: x is not authorized to perform: bedrock-agentcore:ListAgentRuntimeEndpoints")
		}
		return endpoints(in)
	}

	if _, _, err := bacScan(t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1", *warns)
	}
	rows := rowsByNativeID(t, st, TypeBedrockAgentCoreRuntimeEndpoint)
	if len(rows) != 2 {
		t.Errorf("endpoints stored = %d; want the 2 of the runtime not denied", len(rows))
	}
	r := rows[bacTestARN("runtime/tagged/runtime-endpoint", "tagged")]
	assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeBedrockAgentCoreRuntimeEndpoint})
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
// The A/B tests are on the data-plane host: its NXDOMAIN on the first page
// is a silent region gap, after a page it is an error with that page
// stored, and its rows keep a control-plane NXDOMAIN from reporting the
// region unavailable.
func TestScanBedrockAgentCore_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "bedrock-agentcore-control.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		calls := newBACCalls()
		bacTaggedARNs(calls)
		handlers := bacHandlers(calls)
		handlers["ListCapacityProviders"] = func(any) (any, error) { return nil, nx }

		total, _, err := bacScan(t, st, handlers)
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
		}
		if total == 0 {
			t.Error("total = 0; want the stored rows counted")
		}
	})
	// Every call but those handlers answers fails with nx.
	nxScan := func(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
		t.Helper()
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(handlers, nx)
		return scanBedrockAgentCore(context.Background(), acct, testRegion, st, testScanID)
	}
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		_, _, err := nxScan(t, st, nil)
		if got := classifyServiceError(err); got != outcomeUnavailable {
			t.Errorf("classifyServiceError = %v; want outcomeUnavailable (err %v)", got, err)
		}
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
	t.Run("A/B rows only", func(t *testing.T) {
		st := newTestStore(t)
		handlers := bacHandlers(newBACCalls())
		total, _, err := nxScan(t, st, map[string]func(any) (any, error){"ListABTests": handlers["ListABTests"]})
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError = %v; want outcomeError, so the stored A/B rows keep their counts (err %v)", got, err)
		}
		if total != 2 {
			t.Errorf("total = %d; want the 2 A/B tests", total)
		}
	})
	t.Run("A/B later page with nothing else stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := bacHandlers(newBACCalls())
		dataNX := &net.DNSError{Err: "no such host", Name: "bedrock-agentcore.us-east-1.amazonaws.com", IsNotFound: true}
		total, _, err := nxScan(t, st, map[string]func(any) (any, error){"ListABTests": failOnCall(2, dataNX, handlers["ListABTests"])})
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", got, err)
		}
		if total != 1 {
			t.Errorf("total = %d; want the 1 A/B test of the first page", total)
		}
	})
	dataNX := &net.DNSError{Err: "no such host", Name: "bedrock-agentcore.us-east-1.amazonaws.com", IsNotFound: true}
	for _, call := range []int{1, 2} {
		t.Run(fmt.Sprintf("A/B tests call %d", call), func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "bedrockagentcore:ListABTests")
			calls := newBACCalls()
			bacTaggedARNs(calls)
			handlers := bacHandlers(calls)
			handlers["ListABTests"] = failOnCall(call, dataNX, handlers["ListABTests"])

			total, _, err := bacScan(t, st, handlers)
			if call == 1 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if call == 2 && classifyServiceError(err) != outcomeError {
				t.Errorf("classifyServiceError = %v; want outcomeError (err %v)", classifyServiceError(err), err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			if total == 0 {
				t.Error("total = 0; want the stored rows counted")
			}
			if _, ok := bacRowOf(t, st, TypeBedrockAgentCoreABTest, bacTestARN("ab-test", "tagged")); ok != (call == 2) {
				t.Errorf("first-page A/B test stored = %v; want %v", ok, call == 2)
			}
		})
	}
}
