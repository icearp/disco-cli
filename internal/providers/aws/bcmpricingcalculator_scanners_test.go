package aws

import (
	"context"
	"errors"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator"
	bcmpctypes "github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator/types"
	"github.com/aws/smithy-go"
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
	expectedARN := bcmPricingCalculatorNativeID(acct.ID, "bill-scenario", id)
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
	beTotal, _, err := scanBcmPricingCalculatorBillEstimates(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("bill-estimates: %v", err)
	}
	if beTotal != 1 {
		t.Errorf("bill-estimate total=%d want 1", beTotal)
	}
	weTotal, _, err := scanBcmPricingCalculatorWorkloadEstimates(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("workload-estimates: %v", err)
	}
	if weTotal != 1 {
		t.Errorf("workload-estimate total=%d want 1", weTotal)
	}
	beARN := bcmPricingCalculatorNativeID(acct.ID, "bill-estimate", beID)
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, TypeBcmPricingCalculatorBillEstimate, beARN)); err != nil {
		t.Errorf("bill-estimate missing: %v", err)
	}
	weARN := bcmPricingCalculatorNativeID(acct.ID, "workload-estimate", weID)
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, TypeBcmPricingCalculatorWorkloadEstimate, weARN)); err != nil {
		t.Errorf("workload-estimate missing: %v", err)
	}
}

// TestScanBcmPricingCalculatorEstimates_Empty guards the id=="" skip and the
// upsertBatch(nil) path: no Items → no rows, no error.
func TestScanBcmPricingCalculatorEstimates_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubBcmPricingCalculator{}
	for _, scan := range []func() (int, int, error){
		func() (int, int, error) {
			return scanBcmPricingCalculatorBillEstimates(context.Background(), stub, acct, testRegion, st, testScanID)
		},
		func() (int, int, error) {
			return scanBcmPricingCalculatorWorkloadEstimates(context.Background(), stub, acct, testRegion, st, testScanID)
		},
	} {
		total, _, err := scan()
		if err != nil {
			t.Fatalf("empty scan: %v", err)
		}
		if total != 0 {
			t.Errorf("empty total=%d want 0", total)
		}
	}
}

// TestBcmPCListErr asserts each error class maps to the right return identity.
func TestBcmPCListErr(t *testing.T) {
	st := newTestStore(t)
	apiErr := func(code, msg string) error {
		return &smithy.GenericAPIError{Code: code, Message: msg}
	}
	tests := []struct {
		name        string
		err         error
		wantDisable bool // errors.Is(got, errServiceDisabled)
		wantNil     bool
	}{
		{"cost-explorer-not-enabled", apiErr("AccessDeniedException", "User not enabled for cost explorer access"), true, false},
		{"payer-only", apiErr("ValidationException", "Operation not permitted for member accounts. This API is only allowed for regular or payer accounts."), true, false},
		{"migration-required", apiErr("AccessDeniedException", "Migrate the policies in your account to use the new IAM actions"), false, true},
		{"generic-access-denied", apiErr("AccessDeniedException", "is not authorized to perform: bcmpricingcalculator:ListBillEstimates"), false, true},
		{"other-fatal", apiErr("InternalServerException", "boom"), false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bcmPCListErr(st, "bcmpricingcalculator:ListBillEstimates", testAccountID, testRegion, tc.err)
			if tc.wantDisable && !errors.Is(got, errServiceDisabled) {
				t.Errorf("got %v; want errServiceDisabled", got)
			}
			if tc.wantNil && got != nil {
				t.Errorf("got %v; want nil", got)
			}
			if !tc.wantDisable && !tc.wantNil && (got == nil || errors.Is(got, errServiceDisabled)) {
				t.Errorf("got %v; want a non-disabled fatal error", got)
			}
		})
	}
}
