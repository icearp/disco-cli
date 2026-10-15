package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator"
	bcmpctypes "github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

type stubBcmPricingCalculator struct {
	scenarios []bcmpctypes.BillScenarioSummary
	estimates []bcmpctypes.BillEstimateSummary
	workloads []bcmpctypes.WorkloadEstimateSummary
	bcmPricingCalculatorAPI
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
	parents, total, _, err := scanBcmPricingCalculatorBillScenarios(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d want 1", total)
	}
	expectedARN := bcmPricingCalculatorNativeID(acct.ID, "bill-scenario", id)
	if want := []childParent{{id: id, arn: expectedARN}}; !slices.Equal(parents, want) {
		t.Errorf("parents = %v; want %v", parents, want)
	}
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
	weParents, weTotal, _, err := scanBcmPricingCalculatorWorkloadEstimates(context.Background(), stub, acct, testRegion, st, testScanID)
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
	if want := []childParent{{id: weID, arn: weARN}}; !slices.Equal(weParents, want) {
		t.Errorf("workload parents = %v; want %v", weParents, want)
	}
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
			_, total, inserted, err := scanBcmPricingCalculatorWorkloadEstimates(context.Background(), stub, acct, testRegion, st, testScanID)
			return total, inserted, err
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

const bcmPCTestUsageAccount = "111122223333"

// bcmPCFake answers the pricing-calculator ops through opStubConfig, so the
// real client and paginators run. Child pages are keyed by parent id; errs,
// keyed "<op> <parent id>", fail that parent's call. Every child call is
// recorded as "<op> <parent id> <MaxResults>".
type bcmPCFake struct {
	scenarios     []bcmpctypes.BillScenarioSummary
	workloads     []bcmpctypes.WorkloadEstimateSummary
	scenariosErr  error
	estimatesErr  error
	commitments   map[string][][]bcmpctypes.BillScenarioCommitmentModificationItem
	usages        map[string][][]bcmpctypes.BillScenarioUsageModificationItem
	workloadUsage map[string][][]bcmpctypes.WorkloadEstimateUsageItem
	errs          map[string]error
	calls         parentCalls
}

func newBcmPCFake() *bcmPCFake {
	return &bcmPCFake{
		commitments:   map[string][][]bcmpctypes.BillScenarioCommitmentModificationItem{},
		usages:        map[string][][]bcmpctypes.BillScenarioUsageModificationItem{},
		workloadUsage: map[string][][]bcmpctypes.WorkloadEstimateUsageItem{},
		errs:          map[string]error{},
	}
}

func bcmPCFakePage[T any](f *bcmPCFake, op, parent string, maxResults *int32, token *string, pages map[string][][]T) ([]T, *string, error) {
	f.calls.add(fmt.Sprintf("%s %s %d", op, parent, sdkaws.ToInt32(maxResults)))
	if err := f.errs[op+" "+parent]; err != nil {
		return nil, nil, err
	}
	i := 0
	if token != nil {
		n, err := strconv.Atoi(*token)
		if err != nil {
			return nil, nil, fmt.Errorf("fake %s: bad token %q: %w", op, *token, err)
		}
		i = n
	}
	ps := pages[parent]
	if i >= len(ps) {
		return nil, nil, nil
	}
	var next *string
	if i+1 < len(ps) {
		next = sdkaws.String(strconv.Itoa(i + 1))
	}
	return ps[i], next, nil
}

func (f *bcmPCFake) client() *bcmpricingcalculator.Client {
	return bcmpricingcalculator.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		"ListBillScenarios": func(any) (any, error) {
			if f.scenariosErr != nil {
				return nil, f.scenariosErr
			}
			return &bcmpricingcalculator.ListBillScenariosOutput{Items: f.scenarios}, nil
		},
		"ListBillEstimates": func(any) (any, error) {
			if f.estimatesErr != nil {
				return nil, f.estimatesErr
			}
			return &bcmpricingcalculator.ListBillEstimatesOutput{}, nil
		},
		"ListWorkloadEstimates": func(any) (any, error) {
			return &bcmpricingcalculator.ListWorkloadEstimatesOutput{Items: f.workloads}, nil
		},
		"ListBillScenarioCommitmentModifications": func(in any) (any, error) {
			p := in.(*bcmpricingcalculator.ListBillScenarioCommitmentModificationsInput)
			items, next, err := bcmPCFakePage(f, "ListBillScenarioCommitmentModifications", sdkaws.ToString(p.BillScenarioId), p.MaxResults, p.NextToken, f.commitments)
			if err != nil {
				return nil, err
			}
			return &bcmpricingcalculator.ListBillScenarioCommitmentModificationsOutput{Items: items, NextToken: next}, nil
		},
		"ListBillScenarioUsageModifications": func(in any) (any, error) {
			p := in.(*bcmpricingcalculator.ListBillScenarioUsageModificationsInput)
			items, next, err := bcmPCFakePage(f, "ListBillScenarioUsageModifications", sdkaws.ToString(p.BillScenarioId), p.MaxResults, p.NextToken, f.usages)
			if err != nil {
				return nil, err
			}
			return &bcmpricingcalculator.ListBillScenarioUsageModificationsOutput{Items: items, NextToken: next}, nil
		},
		"ListWorkloadEstimateUsage": func(in any) (any, error) {
			p := in.(*bcmpricingcalculator.ListWorkloadEstimateUsageInput)
			items, next, err := bcmPCFakePage(f, "ListWorkloadEstimateUsage", sdkaws.ToString(p.WorkloadEstimateId), p.MaxResults, p.NextToken, f.workloadUsage)
			if err != nil {
				return nil, err
			}
			return &bcmpricingcalculator.ListWorkloadEstimateUsageOutput{Items: items, NextToken: next}, nil
		},
	}, errors.New("bcmPCFake: unexpected operation")))
}

// bcmPCChildPhase describes one child phase for the table tests: the SDK op it
// calls, the parent and child types, the NativeID segment, the page size it
// must request, and how to seed its pages of child ids under a parent.
type bcmPCChildPhase struct {
	op, kind, parentKind string
	parentType, typ      string
	limit                int32
	wantStatus           string
	// wantUnionMember is the CommitmentAction member key the seeded items'
	// stored attributes must carry; empty for phases without the union.
	wantUnionMember string
	run             func(context.Context, bcmPricingCalculatorAPI, *account, string, *store.Store, string, []childParent) (int, int, error)
	seed            func(f *bcmPCFake, parent string, pages [][]string)
}

func bcmPCChildPhases() []bcmPCChildPhase {
	return []bcmPCChildPhase{
		{
			op: "ListBillScenarioCommitmentModifications", kind: "commitment-modification", parentKind: "bill-scenario",
			parentType: TypeBcmPricingCalculatorBillScenario, typ: TypeBcmPricingCalculatorBillScenarioCommitmentModification,
			limit: 25, wantUnionMember: "AddSavingsPlanAction", run: scanBcmPCCommitmentModifications,
			seed: func(f *bcmPCFake, parent string, pages [][]string) {
				for _, ids := range pages {
					var items []bcmpctypes.BillScenarioCommitmentModificationItem
					for _, id := range ids {
						items = append(items, bcmpctypes.BillScenarioCommitmentModificationItem{
							Id: sdkaws.String(id), UsageAccountId: sdkaws.String(bcmPCTestUsageAccount),
							CommitmentAction: &bcmpctypes.BillScenarioCommitmentModificationActionMemberAddSavingsPlanAction{
								Value: bcmpctypes.AddSavingsPlanAction{SavingsPlanOfferingId: sdkaws.String("sp-offer")},
							},
						})
					}
					f.commitments[parent] = append(f.commitments[parent], items)
				}
			},
		},
		{
			op: "ListBillScenarioUsageModifications", kind: "usage-modification", parentKind: "bill-scenario",
			parentType: TypeBcmPricingCalculatorBillScenario, typ: TypeBcmPricingCalculatorBillScenarioUsageModification,
			limit: 25, run: scanBcmPCUsageModifications,
			seed: func(f *bcmPCFake, parent string, pages [][]string) {
				for _, ids := range pages {
					var items []bcmpctypes.BillScenarioUsageModificationItem
					for _, id := range ids {
						items = append(items, bcmpctypes.BillScenarioUsageModificationItem{Id: sdkaws.String(id), UsageAccountId: sdkaws.String(bcmPCTestUsageAccount)})
					}
					f.usages[parent] = append(f.usages[parent], items)
				}
			},
		},
		{
			op: "ListWorkloadEstimateUsage", kind: "usage", parentKind: "workload-estimate",
			parentType: TypeBcmPricingCalculatorWorkloadEstimate, typ: TypeBcmPricingCalculatorWorkloadEstimateUsage,
			limit: 300, wantStatus: "VALID", run: scanBcmPCWorkloadEstimateUsage,
			seed: func(f *bcmPCFake, parent string, pages [][]string) {
				for _, ids := range pages {
					var items []bcmpctypes.WorkloadEstimateUsageItem
					for _, id := range ids {
						items = append(items, bcmpctypes.WorkloadEstimateUsageItem{Id: sdkaws.String(id), UsageAccountId: sdkaws.String(bcmPCTestUsageAccount), Status: bcmpctypes.WorkloadEstimateCostStatusValid})
					}
					f.workloadUsage[parent] = append(f.workloadUsage[parent], items)
				}
			},
		},
	}
}

// bcmPCSeedParents stores parent rows of ph's parent type for ids and returns
// them as the parent phase would.
func bcmPCSeedParents(t *testing.T, st *store.Store, ph bcmPCChildPhase, ids ...string) []childParent {
	t.Helper()
	var parents []childParent
	for _, id := range ids {
		arn := bcmPricingCalculatorNativeID(testAccountID, ph.parentKind, id)
		upsertTestResource(t, st, "aws", testAccountID, ph.parentType, arn, "global", "{}")
		parents = append(parents, childParent{id: id, arn: arn})
	}
	return parents
}

func bcmPCAPIErr(code, msg string) error { return &smithy.GenericAPIError{Code: code, Message: msg} }

func TestScanBcmPCChildren_PagesStoredUnderParent(t *testing.T) {
	for _, ph := range bcmPCChildPhases() {
		t.Run(ph.op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			parents := bcmPCSeedParents(t, st, ph, "p1", "p2-gone")
			f := newBcmPCFake()
			ph.seed(f, "p1", [][]string{{"a"}, {"b", ""}})
			f.errs[ph.op+" p2-gone"] = bcmPCAPIErr("ResourceNotFoundException", "not found")

			total, _, err := ph.run(context.Background(), f.client(), acct, bcmPricingCalculatorRegion, st, testScanID, parents)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 2 {
				t.Errorf("total = %d; want 2", total)
			}
			rows := rowsByNativeID(t, st, ph.typ)
			if len(rows) != 2 {
				t.Errorf("stored %d rows; want 2: %v", len(rows), rows)
			}
			for _, id := range []string{"a", "b"} {
				native := parents[0].arn + "/" + ph.kind + "/" + id
				r, ok := rows[native]
				if !ok {
					t.Errorf("row %s missing", native)
					continue
				}
				if sv(r.Region) != "global" {
					t.Errorf("%s Region = %q; want global", native, sv(r.Region))
				}
				if sv(r.Status) != ph.wantStatus {
					t.Errorf("%s Status = %q; want %q", native, sv(r.Status), ph.wantStatus)
				}
				var attrs struct {
					ID             string `json:"Id"`
					UsageAccountID string `json:"UsageAccountId"`
				}
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
					t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
				}
				if attrs.ID != id || attrs.UsageAccountID != bcmPCTestUsageAccount {
					t.Errorf("%s attrs = %+v; want Id %s, UsageAccountId %s", native, attrs, id, bcmPCTestUsageAccount)
				}
				if ph.wantUnionMember != "" {
					if _, ok := unionMembers(t, r.AttributesJSON, "CommitmentAction")[ph.wantUnionMember]; !ok {
						t.Errorf("%s attrs %s; want CommitmentAction.%s", native, r.AttributesJSON, ph.wantUnionMember)
					}
				}
				assertContainsNative(t, st, parents[0].arn, native)
			}
			wantCalls := []string{
				fmt.Sprintf("%s p1 %d", ph.op, ph.limit),
				fmt.Sprintf("%s p2-gone %d", ph.op, ph.limit),
			}
			if got := f.calls.distinct(); !slices.Equal(got, wantCalls) {
				t.Errorf("calls = %v; want %v", got, wantCalls)
			}
		})
	}
}

func TestScanBcmPCChildren_Empty(t *testing.T) {
	for _, ph := range bcmPCChildPhases() {
		t.Run(ph.op, func(t *testing.T) {
			st := newTestStore(t)
			parents := bcmPCSeedParents(t, st, ph, "p1")
			f := newBcmPCFake()
			total, _, err := ph.run(context.Background(), f.client(), newTestAccount(testAccountID), bcmPricingCalculatorRegion, st, testScanID, parents)
			if err != nil || total != 0 {
				t.Errorf("scan = (%d, %v); want (0, nil)", total, err)
			}
			if rows := rowsByNativeID(t, st, ph.typ); len(rows) != 0 {
				t.Errorf("stored %v; want none", rows)
			}
			if got, want := f.calls.distinct(), []string{fmt.Sprintf("%s p1 %d", ph.op, ph.limit)}; !slices.Equal(got, want) {
				t.Errorf("calls = %v; want %v", got, want)
			}
		})
	}
}

// TestScanBcmPCChildren_ParentErrors fails two of three parents with each error
// class: skipped ones keep the third parent's row, warned ones warn once per op
// however many parents fail, and any other error is returned.
func TestScanBcmPCChildren_ParentErrors(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantWarnings int
		wantErrCode  string
	}{
		{name: "access-denied", err: bcmPCAPIErr("AccessDeniedException", "User: x is not authorized to perform: bcm-pricing-calculator:List"), wantWarnings: 1},
		{name: "data-unavailable", err: bcmPCAPIErr("DataUnavailableException", "unavailable"), wantWarnings: 1},
		{name: "transient", err: bcmPCAPIErr("ThrottlingException", "slow down"), wantWarnings: 1},
		{name: "migration-required-deny", err: bcmPCAPIErr("AccessDeniedException", "Migrate the policies in your account to use the new IAM actions"), wantWarnings: 0},
		{name: "parent-gone", err: bcmPCAPIErr("ResourceNotFoundException", "not found"), wantWarnings: 0},
		{name: "other", err: bcmPCAPIErr("ValidationException", "bad"), wantErrCode: "ValidationException"},
	}
	for _, ph := range bcmPCChildPhases() {
		for _, tc := range cases {
			t.Run(ph.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warnings := countOpWarnings(st, "bcmpricingcalculator:"+ph.op)
				parents := bcmPCSeedParents(t, st, ph, "p1", "p2", "p3")
				f := newBcmPCFake()
				f.errs[ph.op+" p1"] = tc.err
				f.errs[ph.op+" p2"] = tc.err
				ph.seed(f, "p3", [][]string{{"c"}})

				_, _, err := ph.run(context.Background(), f.client(), newTestAccount(testAccountID), bcmPricingCalculatorRegion, st, testScanID, parents)
				if tc.wantErrCode != "" {
					if !isAPIErrorCode(err, tc.wantErrCode) {
						t.Errorf("err = %v; want %s", err, tc.wantErrCode)
					}
					return
				}
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if *warnings != tc.wantWarnings {
					t.Errorf("warnings = %d; want %d", *warnings, tc.wantWarnings)
				}
				rows := rowsByNativeID(t, st, ph.typ)
				if _, ok := rows[parents[2].arn+"/"+ph.kind+"/c"]; !ok || len(rows) != 1 {
					t.Errorf("rows = %v; want only p3's child", rows)
				}
			})
		}
	}
}

func TestFlattenBcmPCCommitmentMod_NamesTheActionKind(t *testing.T) {
	cases := []struct {
		action       bcmpctypes.BillScenarioCommitmentModificationAction
		key          string
		field, value string
	}{
		{&bcmpctypes.BillScenarioCommitmentModificationActionMemberAddReservedInstanceAction{Value: bcmpctypes.AddReservedInstanceAction{ReservedInstancesOfferingId: sdkaws.String("ri-offer")}}, "AddReservedInstanceAction", "ReservedInstancesOfferingId", "ri-offer"},
		{&bcmpctypes.BillScenarioCommitmentModificationActionMemberAddSavingsPlanAction{Value: bcmpctypes.AddSavingsPlanAction{SavingsPlanOfferingId: sdkaws.String("sp-offer")}}, "AddSavingsPlanAction", "SavingsPlanOfferingId", "sp-offer"},
		{&bcmpctypes.BillScenarioCommitmentModificationActionMemberNegateReservedInstanceAction{Value: bcmpctypes.NegateReservedInstanceAction{ReservedInstancesId: sdkaws.String("ri-1")}}, "NegateReservedInstanceAction", "ReservedInstancesId", "ri-1"},
		{&bcmpctypes.BillScenarioCommitmentModificationActionMemberNegateSavingsPlanAction{Value: bcmpctypes.NegateSavingsPlanAction{SavingsPlanId: sdkaws.String("sp-1")}}, "NegateSavingsPlanAction", "SavingsPlanId", "sp-1"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			attrs := mustJSON(flattenBcmPCCommitmentMod(bcmpctypes.BillScenarioCommitmentModificationItem{Id: sdkaws.String("m1"), CommitmentAction: tc.action}))
			members := unionMembers(t, attrs, "CommitmentAction")
			var payload map[string]string
			if err := json.Unmarshal(members[tc.key], &payload); err != nil || len(members) != 1 || payload[tc.field] != tc.value {
				t.Errorf("CommitmentAction = %v (decode err %v); want only %s with %s=%s", members, err, tc.key, tc.field, tc.value)
			}
		})
	}
	t.Run("UnknownMember", func(t *testing.T) {
		attrs := mustJSON(flattenBcmPCCommitmentMod(bcmpctypes.BillScenarioCommitmentModificationItem{
			Id: sdkaws.String("m1"), CommitmentAction: &bcmpctypes.UnknownUnionMember{Tag: "removeEverything", Value: []byte("{}")},
		}))
		assertUnknownMember(t, unionMembers(t, attrs, "CommitmentAction"), "removeEverything", "{}")
	})
}

// bcmPCTopLevelFake has one bill scenario and one workload estimate, each with
// one child of every kind it can hold.
func bcmPCTopLevelFake() *bcmPCFake {
	f := newBcmPCFake()
	f.scenarios = []bcmpctypes.BillScenarioSummary{{Id: sdkaws.String("s1"), Name: sdkaws.String("scenario")}}
	f.workloads = []bcmpctypes.WorkloadEstimateSummary{{Id: sdkaws.String("w1"), Name: sdkaws.String("workload")}}
	for _, ph := range bcmPCChildPhases() {
		ph.seed(f, bcmPCTopLevelParent(ph), [][]string{{"x"}})
	}
	return f
}

// bcmPCTopLevelParent is the id of the bcmPCTopLevelFake parent of ph's children.
func bcmPCTopLevelParent(ph bcmPCChildPhase) string {
	if ph.parentKind == "workload-estimate" {
		return "w1"
	}
	return "s1"
}

func bcmPCTopLevelParentARN(ph bcmPCChildPhase) string {
	return bcmPricingCalculatorNativeID(testAccountID, ph.parentKind, bcmPCTopLevelParent(ph))
}

func bcmPCChildNativeID(ph bcmPCChildPhase) string {
	return bcmPCTopLevelParentARN(ph) + "/" + ph.kind + "/x"
}

func TestScanBcmPricingCalculator_StoresChildrenUnderListedParents(t *testing.T) {
	st := newTestStore(t)
	f := bcmPCTopLevelFake()
	total, _, err := scanBcmPricingCalculator(context.Background(), f.client(), newTestAccount(testAccountID), st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d; want 5 (2 parents, 3 children)", total)
	}
	for _, ph := range bcmPCChildPhases() {
		native := bcmPCChildNativeID(ph)
		if _, ok := rowsByNativeID(t, st, ph.typ)[native]; !ok {
			t.Errorf("%s row %s missing", ph.typ, native)
			continue
		}
		assertContainsNative(t, st, bcmPCTopLevelParentARN(ph), native)
	}
}

// TestScanBcmPricingCalculator_ChildPhasesRunAfterOlderPhaseFailure pins that
// a hard error in an older phase neither skips the child phases of the parents
// already listed nor is lost.
func TestScanBcmPricingCalculator_ChildPhasesRunAfterOlderPhaseFailure(t *testing.T) {
	st := newTestStore(t)
	f := bcmPCTopLevelFake()
	f.estimatesErr = bcmPCAPIErr("ValidationException", "bad")
	total, _, err := scanBcmPricingCalculator(context.Background(), f.client(), newTestAccount(testAccountID), st, testScanID)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Errorf("err = %v; want the ListBillEstimates ValidationException", err)
	}
	// The workload-estimate phase runs after bill estimates, so it and its
	// child phase stored nothing.
	if total != 3 {
		t.Errorf("total = %d; want 3 (scenario and its 2 children)", total)
	}
	for _, ph := range bcmPCChildPhases() {
		_, ok := rowsByNativeID(t, st, ph.typ)[bcmPCChildNativeID(ph)]
		if want := ph.parentKind == "bill-scenario"; ok != want {
			t.Errorf("%s stored = %v; want %v", ph.typ, ok, want)
		}
	}
}

// TestScanBcmPricingCalculator_ChildPhaseErrorKeepsOtherPhases fails each child
// op in turn: its error is returned and every other phase still stores its rows.
func TestScanBcmPricingCalculator_ChildPhaseErrorKeepsOtherPhases(t *testing.T) {
	for _, failing := range bcmPCChildPhases() {
		t.Run(failing.op, func(t *testing.T) {
			st := newTestStore(t)
			f := bcmPCTopLevelFake()
			f.errs[failing.op+" "+bcmPCTopLevelParent(failing)] = bcmPCAPIErr("ValidationException", "bad")
			total, _, err := scanBcmPricingCalculator(context.Background(), f.client(), newTestAccount(testAccountID), st, testScanID)
			if !isAPIErrorCode(err, "ValidationException") {
				t.Errorf("err = %v; want the %s ValidationException", err, failing.op)
			}
			if total != 4 {
				t.Errorf("total = %d; want 4 (2 parents, 2 children)", total)
			}
			for _, ph := range bcmPCChildPhases() {
				_, ok := rowsByNativeID(t, st, ph.typ)[bcmPCChildNativeID(ph)]
				if want := ph.op != failing.op; ok != want {
					t.Errorf("%s stored = %v; want %v", ph.typ, ok, want)
				}
			}
		})
	}
}

// bcmPCPayerOnly is the payer-only rejection bcmPCListErr maps to
// markServiceNotEntitled.
func bcmPCPayerOnly() error {
	return bcmPCAPIErr("ValidationException", "Operation not permitted for member accounts. This API is only allowed for regular or payer accounts.")
}

func TestScanBcmPricingCalculator_FirstPhaseSentinelReturnedAlone(t *testing.T) {
	st := newTestStore(t)
	f := bcmPCTopLevelFake()
	f.scenariosErr = bcmPCAPIErr("AccessDeniedException", "User not enabled for cost explorer access")
	total, _, err := scanBcmPricingCalculator(context.Background(), f.client(), newTestAccount(testAccountID), st, testScanID)
	if !errors.Is(err, errServiceDisabled) || total != 0 {
		t.Errorf("scan = (%d, %v); want (0, errServiceDisabled)", total, err)
	}
	if calls := f.calls.distinct(); len(calls) != 0 {
		t.Errorf("child calls = %v; want none", calls)
	}
}

// TestScanBcmPricingCalculator_LaterSentinelAfterRowsWarns pins that a
// not-entitled refusal after bill scenarios were stored is a warning, not a
// sentinel that would make the dispatcher discard the stored rows' counts.
func TestScanBcmPricingCalculator_LaterSentinelAfterRowsWarns(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "bcmpricingcalculator")
	f := bcmPCTopLevelFake()
	f.estimatesErr = bcmPCPayerOnly()
	total, _, err := scanBcmPricingCalculator(context.Background(), f.client(), newTestAccount(testAccountID), st, testScanID)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (scenario and its 2 children)", total)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
	for _, ph := range bcmPCChildPhases() {
		_, ok := rowsByNativeID(t, st, ph.typ)[bcmPCChildNativeID(ph)]
		if want := ph.parentKind == "bill-scenario"; ok != want {
			t.Errorf("%s stored = %v; want %v", ph.typ, ok, want)
		}
	}
}

// TestScanBcmPricingCalculator_OlderPhaseErrorDoesNotHideChildHardError fails
// an older phase after bill scenarios were stored, with a sentinel and with a
// transient error, while a child phase fails hard: the dispatcher must see the
// hard error.
func TestScanBcmPricingCalculator_OlderPhaseErrorDoesNotHideChildHardError(t *testing.T) {
	cases := []struct {
		name         string
		estimatesErr error
		wantWarnings int
	}{
		{name: "not-entitled", estimatesErr: bcmPCPayerOnly(), wantWarnings: 1},
		{name: "transient", estimatesErr: bcmPCAPIErr("ThrottlingException", "slow down"), wantWarnings: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, "bcmpricingcalculator")
			f := bcmPCTopLevelFake()
			f.estimatesErr = tc.estimatesErr
			f.errs["ListBillScenarioCommitmentModifications s1"] = bcmPCAPIErr("ValidationException", "bad")
			_, _, err := scanBcmPricingCalculator(context.Background(), f.client(), newTestAccount(testAccountID), st, testScanID)
			if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "ValidationException") {
				t.Errorf("outcome = %d, err = %v; want outcomeError (%d) with the child ValidationException", got, err, outcomeError)
			}
			if *warnings != tc.wantWarnings {
				t.Errorf("warnings = %d; want %d", *warnings, tc.wantWarnings)
			}
		})
	}
}
