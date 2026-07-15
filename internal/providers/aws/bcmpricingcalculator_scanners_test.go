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
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, expectedARN)); err != nil {
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
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, beARN)); err != nil {
		t.Errorf("bill-estimate missing: %v", err)
	}
	weARN := bcmPricingCalculatorNativeID(acct.ID, "workload-estimate", weID)
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, weARN)); err != nil {
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
		name            string
		err             error
		wantDisable     bool // errors.Is(got, errServiceDisabled)
		wantNotEntitled bool // errors.Is(got, errServiceNotEntitled)
		wantNil         bool
	}{
		// Cost Explorer not enabled is self-enableable → disabled. Payer-only is
		// account topology a member can't change → not-entitled. The split guards
		// that these two never collapse back to one sentinel.
		{name: "cost-explorer-not-enabled", err: apiErr("AccessDeniedException", "User not enabled for cost explorer access"), wantDisable: true},
		{name: "payer-only", err: apiErr("ValidationException", "Operation not permitted for member accounts. This API is only allowed for regular or payer accounts."), wantNotEntitled: true},
		{name: "migration-required", err: apiErr("AccessDeniedException", "Migrate the policies in your account to use the new IAM actions"), wantNil: true},
		{name: "generic-access-denied", err: apiErr("AccessDeniedException", "is not authorized to perform: bcmpricingcalculator:ListBillEstimates"), wantNil: true},
		{name: "other-fatal", err: apiErr("InternalServerException", "boom")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bcmPCListErr(st, "bcmpricingcalculator:ListBillEstimates", testAccountID, testRegion, tc.err)
			switch {
			case tc.wantDisable:
				if !errors.Is(got, errServiceDisabled) {
					t.Errorf("got %v; want errServiceDisabled", got)
				}
				if errors.Is(got, errServiceNotEntitled) {
					t.Errorf("got %v; must not also be errServiceNotEntitled", got)
				}
			case tc.wantNotEntitled:
				if !errors.Is(got, errServiceNotEntitled) {
					t.Errorf("got %v; want errServiceNotEntitled", got)
				}
				if errors.Is(got, errServiceDisabled) {
					t.Errorf("got %v; must not also be errServiceDisabled", got)
				}
			case tc.wantNil:
				if got != nil {
					t.Errorf("got %v; want nil", got)
				}
			default: // fatal, non-sentinel
				if got == nil || errors.Is(got, errServiceDisabled) || errors.Is(got, errServiceNotEntitled) {
					t.Errorf("got %v; want a non-sentinel fatal error", got)
				}
			}
		})
	}
}
