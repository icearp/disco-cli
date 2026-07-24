package aws

import (
	"context"
	"testing"

	"github.com/icearp/disco-cli/store"
	bac "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol"
	bactypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/types"
)

// stubBAC implements bedrockAgentCoreAPI; only the fields a test populates
// return rows, every other op returns an empty page. Extend as the interface
// grows.
type stubBAC struct {
	customBrowsers []bactypes.BrowserSummary
	systemBrowsers []bactypes.BrowserSummary
	customCI       []bactypes.CodeInterpreterSummary
	systemCI       []bactypes.CodeInterpreterSummary
	bundles        []bactypes.ConfigurationBundleSummary
	datasets       []bactypes.DatasetSummary
	harnesses      []bactypes.HarnessSummary
	endpoints      map[string][]bactypes.HarnessEndpoint
	registries     []bactypes.RegistrySummary
	records        map[string][]bactypes.RegistryRecordSummary
	managers       []bactypes.PaymentManagerSummary
	connectors     map[string][]bactypes.PaymentConnectorSummary
	credProviders  []bactypes.PaymentCredentialProviderItem
}

//nolint:revive // method name must match the SDK op (ListApiKeyCredentialProviders) to satisfy bedrockAgentCoreAPI.
func (s *stubBAC) ListApiKeyCredentialProviders(_ context.Context, _ *bac.ListApiKeyCredentialProvidersInput, _ ...func(*bac.Options)) (*bac.ListApiKeyCredentialProvidersOutput, error) {
	return &bac.ListApiKeyCredentialProvidersOutput{}, nil
}

func (s *stubBAC) ListBrowsers(_ context.Context, in *bac.ListBrowsersInput, _ ...func(*bac.Options)) (*bac.ListBrowsersOutput, error) {
	if in.Type == bactypes.ResourceTypeSystem {
		return &bac.ListBrowsersOutput{BrowserSummaries: s.systemBrowsers}, nil
	}
	return &bac.ListBrowsersOutput{BrowserSummaries: s.customBrowsers}, nil
}

func (s *stubBAC) ListBrowserProfiles(_ context.Context, _ *bac.ListBrowserProfilesInput, _ ...func(*bac.Options)) (*bac.ListBrowserProfilesOutput, error) {
	return &bac.ListBrowserProfilesOutput{}, nil
}

func (s *stubBAC) ListCodeInterpreters(_ context.Context, in *bac.ListCodeInterpretersInput, _ ...func(*bac.Options)) (*bac.ListCodeInterpretersOutput, error) {
	if in.Type == bactypes.ResourceTypeSystem {
		return &bac.ListCodeInterpretersOutput{CodeInterpreterSummaries: s.systemCI}, nil
	}
	return &bac.ListCodeInterpretersOutput{CodeInterpreterSummaries: s.customCI}, nil
}

func (s *stubBAC) ListEvaluators(_ context.Context, _ *bac.ListEvaluatorsInput, _ ...func(*bac.Options)) (*bac.ListEvaluatorsOutput, error) {
	return &bac.ListEvaluatorsOutput{}, nil
}

func (s *stubBAC) ListGateways(_ context.Context, _ *bac.ListGatewaysInput, _ ...func(*bac.Options)) (*bac.ListGatewaysOutput, error) {
	return &bac.ListGatewaysOutput{}, nil
}

func (s *stubBAC) ListGatewayTargets(_ context.Context, _ *bac.ListGatewayTargetsInput, _ ...func(*bac.Options)) (*bac.ListGatewayTargetsOutput, error) {
	return &bac.ListGatewayTargetsOutput{}, nil
}

func (s *stubBAC) ListMemories(_ context.Context, _ *bac.ListMemoriesInput, _ ...func(*bac.Options)) (*bac.ListMemoriesOutput, error) {
	return &bac.ListMemoriesOutput{}, nil
}

func (s *stubBAC) ListOauth2CredentialProviders(_ context.Context, _ *bac.ListOauth2CredentialProvidersInput, _ ...func(*bac.Options)) (*bac.ListOauth2CredentialProvidersOutput, error) {
	return &bac.ListOauth2CredentialProvidersOutput{}, nil
}

func (s *stubBAC) ListOnlineEvaluationConfigs(_ context.Context, _ *bac.ListOnlineEvaluationConfigsInput, _ ...func(*bac.Options)) (*bac.ListOnlineEvaluationConfigsOutput, error) {
	return &bac.ListOnlineEvaluationConfigsOutput{}, nil
}

func (s *stubBAC) ListPolicies(_ context.Context, _ *bac.ListPoliciesInput, _ ...func(*bac.Options)) (*bac.ListPoliciesOutput, error) {
	return &bac.ListPoliciesOutput{}, nil
}

func (s *stubBAC) ListPolicyEngines(_ context.Context, _ *bac.ListPolicyEnginesInput, _ ...func(*bac.Options)) (*bac.ListPolicyEnginesOutput, error) {
	return &bac.ListPolicyEnginesOutput{}, nil
}

func (s *stubBAC) ListAgentRuntimes(_ context.Context, _ *bac.ListAgentRuntimesInput, _ ...func(*bac.Options)) (*bac.ListAgentRuntimesOutput, error) {
	return &bac.ListAgentRuntimesOutput{}, nil
}

func (s *stubBAC) ListAgentRuntimeEndpoints(_ context.Context, _ *bac.ListAgentRuntimeEndpointsInput, _ ...func(*bac.Options)) (*bac.ListAgentRuntimeEndpointsOutput, error) {
	return &bac.ListAgentRuntimeEndpointsOutput{}, nil
}

func (s *stubBAC) ListWorkloadIdentities(_ context.Context, _ *bac.ListWorkloadIdentitiesInput, _ ...func(*bac.Options)) (*bac.ListWorkloadIdentitiesOutput, error) {
	return &bac.ListWorkloadIdentitiesOutput{}, nil
}

func (s *stubBAC) ListConfigurationBundles(_ context.Context, _ *bac.ListConfigurationBundlesInput, _ ...func(*bac.Options)) (*bac.ListConfigurationBundlesOutput, error) {
	return &bac.ListConfigurationBundlesOutput{Bundles: s.bundles}, nil
}

func (s *stubBAC) ListDatasets(_ context.Context, _ *bac.ListDatasetsInput, _ ...func(*bac.Options)) (*bac.ListDatasetsOutput, error) {
	return &bac.ListDatasetsOutput{Datasets: s.datasets}, nil
}

func (s *stubBAC) ListHarnesses(_ context.Context, _ *bac.ListHarnessesInput, _ ...func(*bac.Options)) (*bac.ListHarnessesOutput, error) {
	return &bac.ListHarnessesOutput{Harnesses: s.harnesses}, nil
}

func (s *stubBAC) ListHarnessEndpoints(_ context.Context, in *bac.ListHarnessEndpointsInput, _ ...func(*bac.Options)) (*bac.ListHarnessEndpointsOutput, error) {
	return &bac.ListHarnessEndpointsOutput{Endpoints: s.endpoints[sv(in.HarnessId)]}, nil
}

func (s *stubBAC) ListRegistries(_ context.Context, _ *bac.ListRegistriesInput, _ ...func(*bac.Options)) (*bac.ListRegistriesOutput, error) {
	return &bac.ListRegistriesOutput{Registries: s.registries}, nil
}

func (s *stubBAC) ListRegistryRecords(_ context.Context, in *bac.ListRegistryRecordsInput, _ ...func(*bac.Options)) (*bac.ListRegistryRecordsOutput, error) {
	return &bac.ListRegistryRecordsOutput{RegistryRecords: s.records[sv(in.RegistryId)]}, nil
}

func (s *stubBAC) ListPolicyGenerations(_ context.Context, _ *bac.ListPolicyGenerationsInput, _ ...func(*bac.Options)) (*bac.ListPolicyGenerationsOutput, error) {
	return &bac.ListPolicyGenerationsOutput{}, nil
}

func (s *stubBAC) ListPaymentManagers(_ context.Context, _ *bac.ListPaymentManagersInput, _ ...func(*bac.Options)) (*bac.ListPaymentManagersOutput, error) {
	return &bac.ListPaymentManagersOutput{PaymentManagers: s.managers}, nil
}

func (s *stubBAC) ListPaymentConnectors(_ context.Context, in *bac.ListPaymentConnectorsInput, _ ...func(*bac.Options)) (*bac.ListPaymentConnectorsOutput, error) {
	return &bac.ListPaymentConnectorsOutput{PaymentConnectors: s.connectors[sv(in.PaymentManagerId)]}, nil
}

func (s *stubBAC) ListPaymentCredentialProviders(_ context.Context, _ *bac.ListPaymentCredentialProvidersInput, _ ...func(*bac.Options)) (*bac.ListPaymentCredentialProvidersOutput, error) {
	return &bac.ListPaymentCredentialProvidersOutput{CredentialProviders: s.credProviders}, nil
}

// SYSTEM browsers/code-interpreters bucket as the managed built-in type; CUSTOM
// ones stay the customer-facing type.
func TestScanBACBrowsersAndCodeInterpreters_SystemVsCustom(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	custBrArn := bacARN(testRegion, testAccountID, "browser", "cust-1")
	sysBrArn := bacARN(testRegion, testAccountID, "browser", "aws.browser.v1")
	custCIArn := bacARN(testRegion, testAccountID, "code-interpreter", "cust-1")
	sysCIArn := bacARN(testRegion, testAccountID, "code-interpreter", "aws.codeinterpreter.v1")
	cb, sb, cc, sc := "cust-1", "aws.browser.v1", "cust-1", "aws.codeinterpreter.v1"
	stub := &stubBAC{
		customBrowsers: []bactypes.BrowserSummary{{BrowserArn: &custBrArn, BrowserId: &cb}},
		systemBrowsers: []bactypes.BrowserSummary{{BrowserArn: &sysBrArn, BrowserId: &sb}},
		customCI:       []bactypes.CodeInterpreterSummary{{CodeInterpreterArn: &custCIArn, CodeInterpreterId: &cc}},
		systemCI:       []bactypes.CodeInterpreterSummary{{CodeInterpreterArn: &sysCIArn, CodeInterpreterId: &sc}},
	}

	if _, _, err := scanBACBrowsers(context.Background(), stub, acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("browsers: %v", err)
	}
	if _, _, err := scanBACCodeInterpreters(context.Background(), stub, acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("code-interpreters: %v", err)
	}

	for _, tc := range []struct {
		rtype, arn string
		managed    bool
	}{
		{TypeBedrockAgentCoreBrowserCustom, custBrArn, false},
		{TypeBedrockAgentCoreBrowser, sysBrArn, true},
		{TypeBedrockAgentCoreCodeInterpreterCustom, custCIArn, false},
		{TypeBedrockAgentCoreCodeInterpreter, sysCIArn, true},
	} {
		r, err := st.GetResource(store.ResourceID("aws", acct.ID, tc.arn))
		if err != nil {
			t.Errorf("%s missing: %v", tc.rtype, err)
			continue
		}
		if r.ManagedByProvider != tc.managed {
			t.Errorf("%s ManagedByProvider=%v want %v", tc.rtype, r.ManagedByProvider, tc.managed)
		}
	}
}

func TestScanBACBrowsers_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	total, _, err := scanBACBrowsers(context.Background(), &stubBAC{}, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 {
		t.Errorf("total=%d want 0", total)
	}
}
