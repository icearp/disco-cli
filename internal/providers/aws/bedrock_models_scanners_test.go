package aws

import (
	"context"
	"net/http"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type stubBedrockModels struct {
	custom      []bedrocktypes.CustomModelSummary
	imported    []bedrocktypes.ImportedModelSummary
	provisioned []bedrocktypes.ProvisionedModelSummary
	deployments []bedrocktypes.CustomModelDeploymentSummary
	endpoints   []bedrocktypes.MarketplaceModelEndpointSummary
}

func (s *stubBedrockModels) ListCustomModels(_ context.Context, _ *bedrock.ListCustomModelsInput, _ ...func(*bedrock.Options)) (*bedrock.ListCustomModelsOutput, error) {
	return &bedrock.ListCustomModelsOutput{ModelSummaries: s.custom}, nil
}

func (s *stubBedrockModels) ListImportedModels(_ context.Context, _ *bedrock.ListImportedModelsInput, _ ...func(*bedrock.Options)) (*bedrock.ListImportedModelsOutput, error) {
	return &bedrock.ListImportedModelsOutput{ModelSummaries: s.imported}, nil
}

func (s *stubBedrockModels) ListProvisionedModelThroughputs(_ context.Context, _ *bedrock.ListProvisionedModelThroughputsInput, _ ...func(*bedrock.Options)) (*bedrock.ListProvisionedModelThroughputsOutput, error) {
	return &bedrock.ListProvisionedModelThroughputsOutput{ProvisionedModelSummaries: s.provisioned}, nil
}

func (s *stubBedrockModels) ListCustomModelDeployments(_ context.Context, _ *bedrock.ListCustomModelDeploymentsInput, _ ...func(*bedrock.Options)) (*bedrock.ListCustomModelDeploymentsOutput, error) {
	return &bedrock.ListCustomModelDeploymentsOutput{ModelDeploymentSummaries: s.deployments}, nil
}

func (s *stubBedrockModels) ListMarketplaceModelEndpoints(_ context.Context, _ *bedrock.ListMarketplaceModelEndpointsInput, _ ...func(*bedrock.Options)) (*bedrock.ListMarketplaceModelEndpointsOutput, error) {
	return &bedrock.ListMarketplaceModelEndpointsOutput{MarketplaceModelEndpoints: s.endpoints}, nil
}

func TestScanBedrockModels(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cmArn := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":custom-model/cm-1"
	imArn := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":imported-model/im-1"
	pmArn := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":provisioned-model/pm-1"
	dpArn := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":custom-model-deployment/dp-1"
	epArn := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":marketplace/model-endpoint/ep-1"
	cmName, pmName, dpName := "cm", "pm", "dp"
	stub := &stubBedrockModels{
		custom:      []bedrocktypes.CustomModelSummary{{ModelArn: &cmArn, ModelName: &cmName}},
		imported:    []bedrocktypes.ImportedModelSummary{{ModelArn: &imArn}},
		provisioned: []bedrocktypes.ProvisionedModelSummary{{ProvisionedModelArn: &pmArn, ProvisionedModelName: &pmName, DesiredModelArn: &cmArn}},
		deployments: []bedrocktypes.CustomModelDeploymentSummary{{CustomModelDeploymentArn: &dpArn, CustomModelDeploymentName: &dpName, ModelArn: &cmArn}},
		endpoints:   []bedrocktypes.MarketplaceModelEndpointSummary{{EndpointArn: &epArn}},
	}
	total, _, err := scanBedrockModels(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 5 {
		t.Fatalf("total=%d want 5", total)
	}
	for _, tc := range []struct {
		rtype, arn string
	}{
		{TypeBedrockCustomModel, cmArn},
		{TypeBedrockImportedModel, imArn},
		{TypeBedrockProvisionedModel, pmArn},
		{TypeBedrockCustomModelDeployment, dpArn},
		{TypeBedrockMarketplaceModelEndpoint, epArn},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, tc.rtype, tc.arn)); err != nil {
			t.Errorf("%s missing: %v", tc.rtype, err)
		}
	}
}

// TestBedrockModelsListErr covers the region-gap classification: newer model
// ops (ListCustomModels / ListCustomModelDeployments) that aren't deployed in a
// region reject with UnknownOperationException or a ValidationException whose
// body reads "Unknown operation" (casing varies) — all silent-skipped — while
// an unrelated error still propagates.
func TestBedrockModelsListErr(t *testing.T) {
	st := newTestStore(t)
	cases := []struct {
		name    string
		err     error
		wantNil bool
	}{
		{"UnknownOperationException code", apiErr("UnknownOperationException", "Unknown Operation"), true},
		{"ValidationException lower-case", apiErr("ValidationException", "Unknown operation"), true},
		{"ValidationException upper-case", apiErr("ValidationException", "Unknown Operation"), true},
		{"account not enrolled in model sub-feature", apiErr("AccessDeniedException", "Your account is not authorized to invoke this API operation."), true},
		{"HTML 404 body (deserialize fail)", &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
			Err:      apiErr("UnknownError", "invalid character '<'"),
		}, true},
		{"unrelated ValidationException propagates", apiErr("ValidationException", "malformed request"), false},
		{"unrelated code propagates", apiErr("ThrottlingException", "slow down"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stop, err := bedrockModelsListErr(st, "bedrock:ListCustomModels", testAccountID, testRegion, tc.err)
			if !stop {
				t.Error("bedrockModelsListErr must always signal stop=true")
			}
			if tc.wantNil && err != nil {
				t.Errorf("err = %v; want nil (region gap swallowed)", err)
			}
			if !tc.wantNil && err == nil {
				t.Error("err = nil; want propagated")
			}
		})
	}
}

// Empty lists produce no rows and no error.
func TestScanBedrockModels_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	total, _, err := scanBedrockModels(context.Background(), &stubBedrockModels{}, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 {
		t.Errorf("total=%d want 0", total)
	}
}
