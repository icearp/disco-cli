package aws

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator"
	bcmpctypes "github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator/types"
)

type stubBcmPricingCalculator struct {
	scenarios []bcmpctypes.BillScenarioSummary
}

func (s *stubBcmPricingCalculator) ListBillScenarios(_ context.Context, _ *bcmpricingcalculator.ListBillScenariosInput, _ ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListBillScenariosOutput, error) {
	return &bcmpricingcalculator.ListBillScenariosOutput{Items: s.scenarios}, nil
}

func TestScanBcmPricingCalculatorBillScenarios(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	id := "scenario-abc"
	name := "EC2 Q3 forecast"
	stub := &stubBcmPricingCalculator{
		scenarios: []bcmpctypes.BillScenarioSummary{
			{Id: &id, Name: &name, Status: bcmpctypes.BillScenarioStatusReady},
		},
	}
	total, _, err := scanBcmPricingCalculatorBillScenarios(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d want 1", total)
	}
	expectedARN := bcmPricingCalculatorBillScenarioNativeID(testRegion, acct.ID, id)
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, TypeBcmPricingCalculatorBillScenario, expectedARN)); err != nil {
		t.Errorf("bill-scenario missing: %v", err)
	}
}
