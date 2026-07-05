package aws

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
)

// stubBedrockCatalog implements bedrockAPI; only the catalog ops under test
// return rows, the rest are empty.
type stubBedrockCatalog struct {
	profiles []bedrocktypes.InferenceProfileSummary
	models   []bedrocktypes.FoundationModelSummary
	arpErr   error // returned by ListAutomatedReasoningPolicies when set
}

func (s *stubBedrockCatalog) ListGuardrails(_ context.Context, _ *bedrock.ListGuardrailsInput, _ ...func(*bedrock.Options)) (*bedrock.ListGuardrailsOutput, error) {
	return &bedrock.ListGuardrailsOutput{}, nil
}

func (s *stubBedrockCatalog) ListAutomatedReasoningPolicies(_ context.Context, _ *bedrock.ListAutomatedReasoningPoliciesInput, _ ...func(*bedrock.Options)) (*bedrock.ListAutomatedReasoningPoliciesOutput, error) {
	if s.arpErr != nil {
		return nil, s.arpErr
	}
	return &bedrock.ListAutomatedReasoningPoliciesOutput{}, nil
}

func (s *stubBedrockCatalog) ListPromptRouters(_ context.Context, _ *bedrock.ListPromptRoutersInput, _ ...func(*bedrock.Options)) (*bedrock.ListPromptRoutersOutput, error) {
	return &bedrock.ListPromptRoutersOutput{}, nil
}

func (s *stubBedrockCatalog) ListInferenceProfiles(_ context.Context, _ *bedrock.ListInferenceProfilesInput, _ ...func(*bedrock.Options)) (*bedrock.ListInferenceProfilesOutput, error) {
	return &bedrock.ListInferenceProfilesOutput{InferenceProfileSummaries: s.profiles}, nil
}

func (s *stubBedrockCatalog) ListEnforcedGuardrailsConfiguration(_ context.Context, _ *bedrock.ListEnforcedGuardrailsConfigurationInput, _ ...func(*bedrock.Options)) (*bedrock.ListEnforcedGuardrailsConfigurationOutput, error) {
	return &bedrock.ListEnforcedGuardrailsConfigurationOutput{}, nil
}

func (s *stubBedrockCatalog) ListFoundationModels(_ context.Context, _ *bedrock.ListFoundationModelsInput, _ ...func(*bedrock.Options)) (*bedrock.ListFoundationModelsOutput, error) {
	return &bedrock.ListFoundationModelsOutput{ModelSummaries: s.models}, nil
}

// SYSTEM_DEFINED inference profiles bucket as the AWS-managed catalog type with
// ManagedByProvider set; APPLICATION profiles stay the customer-facing type.
func TestScanBedrockInferenceProfiles_SystemDefinedManaged(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	appArn := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":application-inference-profile/app-1"
	sysArn := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":inference-profile/us.anthropic.claude"
	appName, sysName := "app", "sys"
	stub := &stubBedrockCatalog{profiles: []bedrocktypes.InferenceProfileSummary{
		{InferenceProfileArn: &appArn, InferenceProfileName: &appName, Type: bedrocktypes.InferenceProfileTypeApplication},
		{InferenceProfileArn: &sysArn, InferenceProfileName: &sysName, Type: bedrocktypes.InferenceProfileTypeSystemDefined},
	}}
	if _, _, err := scanBedrockInferenceProfiles(context.Background(), stub, acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}

	app, err := st.GetResource(store.ResourceID("aws", acct.ID, TypeBedrockApplicationInferenceProfile, appArn))
	if err != nil {
		t.Fatalf("application profile missing: %v", err)
	}
	if app.ManagedByProvider {
		t.Error("application inference-profile should not be ManagedByProvider")
	}
	sys, err := st.GetResource(store.ResourceID("aws", acct.ID, TypeBedrockInferenceProfile, sysArn))
	if err != nil {
		t.Fatalf("system-defined profile missing: %v", err)
	}
	if !sys.ManagedByProvider {
		t.Error("system-defined inference-profile should be ManagedByProvider")
	}
}

func TestScanBedrockFoundationModels(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fmArn := "arn:aws:bedrock:" + testRegion + "::foundation-model/anthropic.claude-v2"
	fmID, fmName := "anthropic.claude-v2", "Claude V2"
	stub := &stubBedrockCatalog{models: []bedrocktypes.FoundationModelSummary{
		{ModelArn: &fmArn, ModelId: &fmID, ModelName: &fmName},
	}}
	total, _, err := scanBedrockFoundationModels(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d want 1", total)
	}
	r, err := st.GetResource(store.ResourceID("aws", acct.ID, TypeBedrockFoundationModel, fmArn))
	if err != nil {
		t.Fatalf("foundation model missing: %v", err)
	}
	if !r.ManagedByProvider {
		t.Error("foundation-model should be ManagedByProvider")
	}
}

func TestScanBedrockFoundationModels_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	total, _, err := scanBedrockFoundationModels(context.Background(), &stubBedrockCatalog{}, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 {
		t.Errorf("total=%d want 0", total)
	}
}

// AR Policies is region/account-gated; an empty-body AccessDeniedException is the
// region-gap signal and must silent-skip (no rows, no propagated error, no
// warning) rather than surface as a spurious scan warning.
func TestScanBedrockARPolicies_RegionGapSilent(t *testing.T) {
	st := newTestStore(t)
	warned := false
	st.OnWarn = func(store.ScanWarning) { warned = true }
	acct := newTestAccount(testAccountID)

	stub := &stubBedrockCatalog{arpErr: apiErr("AccessDeniedException", "")}
	arns, total, inserted, err := scanBedrockARPolicies(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("region gap: returned %v, want nil", err)
	}
	if warned {
		t.Error("empty-body region gap must not record a warning")
	}
	if len(arns) != 0 || total != 0 || inserted != 0 {
		t.Errorf("arns=%d total=%d inserted=%d; want all 0", len(arns), total, inserted)
	}
}

// A message-bearing denial (a real per-action IAM deny) must still warn — the
// silent-skip is scoped to the empty-body region-gap variant, not every 403.
func TestScanBedrockARPolicies_RealDenialWarns(t *testing.T) {
	st := newTestStore(t)
	warned := false
	st.OnWarn = func(store.ScanWarning) { warned = true }
	acct := newTestAccount(testAccountID)

	stub := &stubBedrockCatalog{arpErr: apiErr("AccessDenied",
		"User: arn:aws:iam::1:user/u is not authorized to perform: bedrock:ListAutomatedReasoningPolicies")}
	if _, _, _, err := scanBedrockARPolicies(context.Background(), stub, acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("real denial: returned %v, want nil (skipIfAccessDenied swallows)", err)
	}
	if !warned {
		t.Error("a real message-bearing denial must record a warning")
	}
}
