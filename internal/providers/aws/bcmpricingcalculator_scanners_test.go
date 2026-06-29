package aws

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator"
	bcmpctypes "github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator/types"
)

type stubBcmPricingCalculator struct {
	scenarios []bcmpctypes.BillScenarioSummary
	estimates []bcmpctypes.BillEstimateSummary
	workloads []bcmpctypes.WorkloadEstimateSummary
}

func (s *stubBcmPricingCalculator) ListBillScenarios(_ context.Context, _ *bcmpricingcalculator.ListBillScenariosInput, _ ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListBillScenariosOutput, error) {
	return &bcmpricingcalculator.ListBillScenariosOutput{Items: s.scenarios}, nil
}

func (s *stubBcmPricingCalculator) ListBillEstimates(_ context.Context, _ *bcmpricingcalculator.ListBillEstimatesInput, _ ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListBillEstimatesOutput, error) {
	return &bcmpricingcalculator.ListBillEstimatesOutput{Items: s.estimates}, nil
}

func (s *stubBcmPricingCalculator) ListWorkloadEstimates(_ context.Context, _ *bcmpricingcalculator.ListWorkloadEstimatesInput, _ ...func(*bcmpricingcalculator.Options)) (*bcmpricingcalculator.ListWorkloadEstimatesOutput, error) {
	return &bcmpricingcalculator.ListWorkloadEstimatesOutput{Items: s.workloads}, nil
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
	expectedARN := bcmPricingCalculatorNativeID(testRegion, acct.ID, "bill-scenario", id)
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, TypeBcmPricingCalculatorBillScenario, expectedARN)); err != nil {
		t.Errorf("bill-scenario missing: %v", err)
	}
}

func TestScanBcmPricingCalculatorEstimates(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	beID, beName := "be-1", "Bill estimate 1"
	weID, weName := "we-1", "Workload estimate 1"
	stub := &stubBcmPricingCalculator{
		estimates: []bcmpctypes.BillEstimateSummary{{Id: &beID, Name: &beName, Status: bcmpctypes.BillEstimateStatusComplete}},
		workloads: []bcmpctypes.WorkloadEstimateSummary{{Id: &weID, Name: &weName, Status: bcmpctypes.WorkloadEstimateStatusValid}},
	}
	if _, _, err := scanBcmPricingCalculatorBillEstimates(context.Background(), stub, acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("bill-estimates: %v", err)
	}
	if _, _, err := scanBcmPricingCalculatorWorkloadEstimates(context.Background(), stub, acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("workload-estimates: %v", err)
	}
	beARN := bcmPricingCalculatorNativeID(testRegion, acct.ID, "bill-estimate", beID)
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, TypeBcmPricingCalculatorBillEstimate, beARN)); err != nil {
		t.Errorf("bill-estimate missing: %v", err)
	}
	weARN := bcmPricingCalculatorNativeID(testRegion, acct.ID, "workload-estimate", weID)
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, TypeBcmPricingCalculatorWorkloadEstimate, weARN)); err != nil {
		t.Errorf("workload-estimate missing: %v", err)
	}
}
