package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestBacListSkip covers the bedrockagentcore choke point: access-denied and the
// two region-gap codes are swallowed to nil, while an unrelated error propagates.
// AuthorizerConfigurationException (a 500 returned where the AgentCore front-end
// resolves but is not provisioned) is now swallowed alongside
// UnknownOperationException: propagating it aborted the sequential
// scanBedrockAgentCore before its later ops (payments, the parallel batch) ran,
// and the retryable 500 also burned the per-service retry budget. The retry burn
// is cut separately by marking the code non-retryable on the client; here we pin
// that it no longer propagates as a scan error.
func TestBacListSkip(t *testing.T) {
	st := newTestStore(t)
	cases := []struct {
		name    string
		err     error
		wantNil bool
	}{
		{"access denied swallowed", apiErr("AccessDeniedException", "denied"), true},
		{"UnknownOperationException region gap", apiErr("UnknownOperationException", "UnknownError"), true},
		{"AuthorizerConfigurationException region gap", apiErr("AuthorizerConfigurationException", "Internal server error"), true},
		{"unrelated code propagates", apiErr("ValidationException", "bad input"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := bacListSkip(st, nil, "bedrockagentcore:ListPaymentCredentialProviders", "payment-credential-providers", testAccountID, testRegion, tc.err)
			if tc.wantNil && err != nil {
				t.Errorf("err = %v; want nil (swallowed)", err)
			}
			if !tc.wantNil && err == nil {
				t.Error("err = nil; want propagated")
			}
		})
	}
}

// TestBacListSkip_PreservesAccumulatedRows pins the "keep rows already
// accumulated by the caller" contract: on a swallowed error the batch must still
// be upserted, not dropped. A regression to `return 0, 0, nil` would zero total.
func TestBacListSkip_PreservesAccumulatedRows(t *testing.T) {
	st := newTestStore(t)
	region := testRegion
	arn := "arn:aws:bedrock-agentcore:" + region + ":" + testAccountID + ":registry/reg-1"
	name := "reg-1"
	batch := []*store.Resource{{
		Provider: "aws", AccountID: testAccountID, Type: TypeBedrockAgentCoreRegistry,
		NativeID: arn, Name: &name, Region: &region, DiscoveredBy: testScanID,
	}}
	total, inserted, err := bacListSkip(st, batch, "bedrockagentcore:ListRegistries", "registries", testAccountID, testRegion, apiErr("UnknownOperationException", "gap"))
	if err != nil {
		t.Fatalf("err = %v; want nil (region gap swallowed)", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("total=%d inserted=%d; want 1/1 — accumulated row must survive the skip", total, inserted)
	}
	if _, err := st.GetResource(store.ResourceID("aws", testAccountID, arn)); err != nil {
		t.Errorf("accumulated row not persisted: %v", err)
	}
}
