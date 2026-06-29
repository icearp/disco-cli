package aws

import "testing"

// TestResolveBcmPricingCalculatorRelationships verifies the no-op resolver
// runs without error and emits no relationships. Replace with real
// assertions once Cost Explorer cost-category modeling lands.
func TestResolveBcmPricingCalculatorRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	arn := bcmPricingCalculatorNativeID(testRegion, acct.ID, "bill-scenario", "scenario-x")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeBcmPricingCalculatorBillScenario, arn, testRegion, "{}")

	if err := resolveBcmPricingCalculatorRelationships(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(bID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}
