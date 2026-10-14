package aws

import (
	"testing"

	workmailtypes "github.com/aws/aws-sdk-go-v2/service/workmail/types"
	"github.com/icearp/disco-cli/store"
)

const wmFnARN = "arn:aws:lambda:us-east-1:123456789012:function:availability"

func TestResolveWorkMailAvailabilityLambda(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cfg      workmailtypes.AvailabilityConfiguration
		wantEdge bool
	}{
		{"UnqualifiedARN", workmailtypes.AvailabilityConfiguration{LambdaProvider: &workmailtypes.LambdaAvailabilityProvider{LambdaArn: ptrStr(wmFnARN)}}, true},
		{"QualifiedARN", workmailtypes.AvailabilityConfiguration{LambdaProvider: &workmailtypes.LambdaAvailabilityProvider{LambdaArn: ptrStr(wmFnARN + ":live")}}, true},
		{"UnscannedFunction", workmailtypes.AvailabilityConfiguration{LambdaProvider: &workmailtypes.LambdaAvailabilityProvider{LambdaArn: ptrStr(wmFnARN + "-other")}}, false},
		{"EwsProvider", workmailtypes.AvailabilityConfiguration{EwsProvider: &workmailtypes.RedactedEwsAvailabilityProvider{EwsEndpoint: ptrStr("https://ews.example.com")}}, false},
		{"NoAttrs", workmailtypes.AvailabilityConfiguration{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeWorkMailAvailabilityConfiguration,
				wmOrgA+"/availabilityconfiguration/example.com", testRegion, mustJSON(tc.cfg))
			fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, wmFnARN, testRegion, "{}")

			if err := resolveWorkMailAvailabilityLambda(acct, st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			rels, err := st.RelationshipsFrom(cfgID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if tc.wantEdge {
				assertRelationship(t, rels, cfgID, fnID, store.RelUses)
				if len(rels) != 1 {
					t.Errorf("edges = %v, want only the lambda edge", rels)
				}
			} else if len(rels) != 0 {
				t.Errorf("edges = %v, want none", rels)
			}
		})
	}
}
