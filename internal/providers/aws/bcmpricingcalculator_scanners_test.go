package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator"
	bcmpctypes "github.com/aws/aws-sdk-go-v2/service/bcmpricingcalculator/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

const bcmPCTestUsageAccount = "111122223333"

// bcmPCFake answers the pricing-calculator ops through opStubConfig, so the
// real client and paginators run. The bill-scenario, bill-estimate and
// workload-estimate listings serve one element a page; errs keyed
// "<op> #<page>" (0 for the first) fail that page, and an op named in repeat
// answers every page with the same NextToken. Child pages are keyed by parent
// id; errs keyed "<op> <parent id>" fail that parent's call. Every child call
// is recorded as "<op> <parent id> <MaxResults>", and each parent listing's
// MaxResults by op. ListTagsForResource answers tags by ARN, or fails with
// errs[ARN], and records every ARN sent.
type bcmPCFake struct {
	scenarios     []bcmpctypes.BillScenarioSummary
	estimates     []bcmpctypes.BillEstimateSummary
	workloads     []bcmpctypes.WorkloadEstimateSummary
	commitments   map[string][][]bcmpctypes.BillScenarioCommitmentModificationItem
	usages        map[string][][]bcmpctypes.BillScenarioUsageModificationItem
	workloadUsage map[string][][]bcmpctypes.WorkloadEstimateUsageItem
	tags          map[string]map[string]string
	errs          map[string]error
	repeat        string
	calls         parentCalls
	// before, when set, runs before a parent listing's page is answered.
	before func(op string, token *string)

	mu         sync.Mutex // guards maxResults and tagARNs
	maxResults map[string][]int32
	tagARNs    []string
}

func newBcmPCFake() *bcmPCFake {
	return &bcmPCFake{
		commitments:   map[string][][]bcmpctypes.BillScenarioCommitmentModificationItem{},
		usages:        map[string][][]bcmpctypes.BillScenarioUsageModificationItem{},
		workloadUsage: map[string][][]bcmpctypes.WorkloadEstimateUsageItem{},
		tags:          map[string]map[string]string{},
		errs:          map[string]error{},
		maxResults:    map[string][]int32{},
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

// bcmPCFakeListPage answers page token of a parent listing of items, one
// element a page.
func bcmPCFakeListPage[T any](f *bcmPCFake, op string, maxResults *int32, token *string, items []T) ([]T, *string, error) {
	if f.before != nil {
		f.before(op, token)
	}
	f.mu.Lock()
	f.maxResults[op] = append(f.maxResults[op], sdkaws.ToInt32(maxResults))
	f.mu.Unlock()
	i := 0
	if token != nil {
		n, err := strconv.Atoi(*token)
		if err != nil {
			return nil, nil, fmt.Errorf("fake %s: bad token %q: %w", op, *token, err)
		}
		i = n
	}
	if err := f.errs[fmt.Sprintf("%s #%d", op, i)]; err != nil {
		return nil, nil, err
	}
	if i >= len(items) {
		return nil, nil, nil
	}
	var next *string
	switch {
	case f.repeat == op:
		next = sdkaws.String("1")
	case i+1 < len(items):
		next = sdkaws.String(strconv.Itoa(i + 1))
	}
	return items[i : i+1], next, nil
}

func (f *bcmPCFake) pageSizes(op string) []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.maxResults[op])
}

func (f *bcmPCFake) sortedTagARNs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.tagARNs))
}

func (f *bcmPCFake) client() *bcmpricingcalculator.Client {
	return bcmpricingcalculator.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		"ListBillScenarios": func(in any) (any, error) {
			p := in.(*bcmpricingcalculator.ListBillScenariosInput)
			items, next, err := bcmPCFakeListPage(f, "ListBillScenarios", p.MaxResults, p.NextToken, f.scenarios)
			if err != nil {
				return nil, err
			}
			return &bcmpricingcalculator.ListBillScenariosOutput{Items: items, NextToken: next}, nil
		},
		"ListBillEstimates": func(in any) (any, error) {
			p := in.(*bcmpricingcalculator.ListBillEstimatesInput)
			items, next, err := bcmPCFakeListPage(f, "ListBillEstimates", p.MaxResults, p.NextToken, f.estimates)
			if err != nil {
				return nil, err
			}
			return &bcmpricingcalculator.ListBillEstimatesOutput{Items: items, NextToken: next}, nil
		},
		"ListWorkloadEstimates": func(in any) (any, error) {
			p := in.(*bcmpricingcalculator.ListWorkloadEstimatesInput)
			items, next, err := bcmPCFakeListPage(f, "ListWorkloadEstimates", p.MaxResults, p.NextToken, f.workloads)
			if err != nil {
				return nil, err
			}
			return &bcmpricingcalculator.ListWorkloadEstimatesOutput{Items: items, NextToken: next}, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			arn := sv(in.(*bcmpricingcalculator.ListTagsForResourceInput).Arn)
			f.mu.Lock()
			f.tagARNs = append(f.tagARNs, arn)
			f.mu.Unlock()
			if err := f.errs[arn]; err != nil {
				return nil, err
			}
			return &bcmpricingcalculator.ListTagsForResourceOutput{Tags: f.tags[arn]}, nil
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
		arn := bcmPricingCalculatorNativeID("aws", testAccountID, ph.parentKind, id)
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

var (
	bcmPCDenied    = bcmPCAPIErr("AccessDeniedException", "User: x is not authorized to perform: bcm-pricing-calculator:ListTagsForResource")
	bcmPCNotFound  = bcmPCAPIErr("ResourceNotFoundException", "gone")
	bcmPCThrottled = bcmPCAPIErr("ThrottlingException", "slow down")
	bcmPCBadInput  = bcmPCAPIErr("ValidationException", "bad")
	bcmPCCreated   = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

// bcmPCTestTotal is the rows bcmPCTopLevelFake stores: six parents and one
// child of each child kind.
const bcmPCTestTotal = 9

func bcmPCARN(kind, id string) string {
	return bcmPricingCalculatorNativeID("aws", testAccountID, kind, id)
}

// bcmPCTopLevelFake has two bill scenarios, two bill estimates and two
// workload estimates, each listing two pages of one element. The first of
// each (s1, e1, w1) has tags; s1 and w1 hold one child of every kind they can
// hold.
func bcmPCTopLevelFake() *bcmPCFake {
	f := newBcmPCFake()
	for _, id := range []string{"s1", "s2"} {
		f.scenarios = append(f.scenarios, bcmpctypes.BillScenarioSummary{
			Id: sdkaws.String(id), Name: sdkaws.String("scenario " + id), Status: bcmpctypes.BillScenarioStatusReady,
			CreatedAt: &bcmPCCreated, ExpiresAt: &bcmPCCreated, GroupSharingPreference: bcmpctypes.GroupSharingPreferenceEnumOpen,
		})
	}
	for _, id := range []string{"e1", "e2"} {
		f.estimates = append(f.estimates, bcmpctypes.BillEstimateSummary{
			Id: sdkaws.String(id), Name: sdkaws.String("estimate " + id), Status: bcmpctypes.BillEstimateStatusComplete,
			CreatedAt: &bcmPCCreated, BillInterval: &bcmpctypes.BillInterval{Start: &bcmPCCreated},
		})
	}
	for _, id := range []string{"w1", "w2"} {
		f.workloads = append(f.workloads, bcmpctypes.WorkloadEstimateSummary{
			Id: sdkaws.String(id), Name: sdkaws.String("workload " + id), Status: bcmpctypes.WorkloadEstimateStatusValid,
			CreatedAt: &bcmPCCreated, TotalCost: sdkaws.Float64(12.5), RateType: bcmpctypes.WorkloadEstimateRateTypeBeforeDiscounts,
		})
	}
	f.tags[bcmPCARN("bill-scenario", "s1")] = map[string]string{"env": "prod"}
	f.tags[bcmPCARN("bill-estimate", "e1")] = map[string]string{"team": "finance"}
	f.tags[bcmPCARN("workload-estimate", "w1")] = map[string]string{"cost-center": "q3"}
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
	return bcmPCARN(ph.parentKind, bcmPCTopLevelParent(ph))
}

func bcmPCChildNativeID(ph bcmPCChildPhase) string {
	return bcmPCTopLevelParentARN(ph) + "/" + ph.kind + "/x"
}

// bcmPCParentType is one of the tag-read types bcmPCTopLevelFake lists.
type bcmPCParentType struct {
	op, kind, typ, status, name string
	ids                         []string
}

func bcmPCParentTypes() []bcmPCParentType {
	return []bcmPCParentType{
		{"ListBillScenarios", "bill-scenario", TypeBcmPricingCalculatorBillScenario, "READY", "scenario", []string{"s1", "s2"}},
		{"ListBillEstimates", "bill-estimate", TypeBcmPricingCalculatorBillEstimate, "COMPLETE", "estimate", []string{"e1", "e2"}},
		{"ListWorkloadEstimates", "workload-estimate", TypeBcmPricingCalculatorWorkloadEstimate, "VALID", "workload", []string{"w1", "w2"}},
	}
}

func bcmPCScan(ctx context.Context, st *store.Store, f *bcmPCFake) (int, int, error) {
	return scanBcmPricingCalculator(ctx, f.client(), newTestAccount(testAccountID), st, testScanID)
}

// Bill scenarios, bill estimates and workload estimates are stored under
// their built ARNs with the tags ListTagsForResource returns for that ARN
// (nil for none) and the listed element as attributes; the children are
// stored under their parents without a tag read. Each listing is sent
// MaxResults 25. No error, no warning.
func TestScanBcmPricingCalculator_StoresTagsOfParentTypes(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := bcmPCTopLevelFake()

	total, inserted, err := bcmPCScan(context.Background(), st, f)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != bcmPCTestTotal || inserted != bcmPCTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, bcmPCTestTotal, bcmPCTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	var wantTagARNs []string
	for _, pt := range bcmPCParentTypes() {
		rows := rowsByNativeID(t, st, pt.typ)
		var want []string
		for _, id := range pt.ids {
			want = append(want, bcmPCARN(pt.kind, id))
		}
		if got := slices.Sorted(maps.Keys(rows)); !slices.Equal(got, want) {
			t.Fatalf("%s rows = %v; want %v", pt.typ, got, want)
		}
		wantTagARNs = append(wantTagARNs, want...)
		for i, arn := range want {
			r := rows[arn]
			assertTagsMapOrNil(t, r.TagsJSON, f.tags[arn])
			if want := pt.name + " " + pt.ids[i]; sv(r.Name) != want {
				t.Errorf("%s Name = %q; want %q", arn, sv(r.Name), want)
			}
			if sv(r.Status) != pt.status || sv(r.CreatedAt) != "2026-09-01T08:00:00Z" || sv(r.Region) != "global" {
				t.Errorf("%s Status, CreatedAt, Region = %q, %q, %q; want %q, 2026-09-01T08:00:00Z, global", arn, sv(r.Status), sv(r.CreatedAt), sv(r.Region), pt.status)
			}
		}
		if got := f.pageSizes(pt.op); !slices.Equal(got, []int32{25, 25}) {
			t.Errorf("%s MaxResults = %v; want [25 25]", pt.op, got)
		}
	}
	slices.Sort(wantTagARNs)
	if got := f.sortedTagARNs(); !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantTagARNs)
	}

	var scenario bcmpctypes.BillScenarioSummary
	r := rowsByNativeID(t, st, TypeBcmPricingCalculatorBillScenario)[bcmPCARN("bill-scenario", "s2")]
	if err := json.Unmarshal([]byte(r.AttributesJSON), &scenario); err != nil || sv(scenario.Id) != "s2" ||
		scenario.GroupSharingPreference != bcmpctypes.GroupSharingPreferenceEnumOpen || scenario.ExpiresAt == nil || sv(r.Name) != "scenario s2" {
		t.Errorf("bill scenario Name %q, attrs %s (%v); want the listed element", sv(r.Name), r.AttributesJSON, err)
	}
	var estimate bcmpctypes.BillEstimateSummary
	r = rowsByNativeID(t, st, TypeBcmPricingCalculatorBillEstimate)[bcmPCARN("bill-estimate", "e2")]
	if err := json.Unmarshal([]byte(r.AttributesJSON), &estimate); err != nil || sv(estimate.Id) != "e2" ||
		estimate.BillInterval == nil || estimate.BillInterval.Start == nil || sv(r.Name) != "estimate e2" {
		t.Errorf("bill estimate Name %q, attrs %s (%v); want the listed element", sv(r.Name), r.AttributesJSON, err)
	}
	var workload bcmpctypes.WorkloadEstimateSummary
	r = rowsByNativeID(t, st, TypeBcmPricingCalculatorWorkloadEstimate)[bcmPCARN("workload-estimate", "w2")]
	if err := json.Unmarshal([]byte(r.AttributesJSON), &workload); err != nil || sv(workload.Id) != "w2" ||
		sdkaws.ToFloat64(workload.TotalCost) != 12.5 || workload.RateType != bcmpctypes.WorkloadEstimateRateTypeBeforeDiscounts || sv(r.Name) != "workload w2" {
		t.Errorf("workload estimate Name %q, attrs %s (%v); want the listed element", sv(r.Name), r.AttributesJSON, err)
	}
	for _, ph := range bcmPCChildPhases() {
		native := bcmPCChildNativeID(ph)
		c, ok := rowsByNativeID(t, st, ph.typ)[native]
		if !ok {
			t.Errorf("%s row %s missing", ph.typ, native)
			continue
		}
		if c.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", native, *c.TagsJSON)
		}
		assertContainsNative(t, st, bcmPCTopLevelParentARN(ph), native)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanBcmPricingCalculator_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := bcmPCScan(context.Background(), st, bcmPCTopLevelFake()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := bcmPCScan(context.Background(), st, bcmPCTopLevelFake())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != bcmPCTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, bcmPCTestTotal)
	}
}

// Empty listings: nothing stored, no child listing or tag read, no error, no
// warning.
func TestScanBcmPricingCalculator_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newBcmPCFake()

	total, inserted, err := bcmPCScan(context.Background(), st, f)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if calls := f.calls.distinct(); len(calls) != 0 {
		t.Errorf("child calls = %v; want none", calls)
	}
	if got := f.sortedTagARNs(); len(got) != 0 {
		t.Errorf("tag reads = %v; want none", got)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently (a bill scenario or
// workload estimate with its children, which are then not listed). A denial
// keeps the row untagged and warns once under the tag op with the scan's
// scope; a throttle keeps it and is transient; any other error keeps it and
// is an error.
func TestScanBcmPricingCalculator_TagReadFailures(t *testing.T) {
	scenario, estimate, workload := bcmPCARN("bill-scenario", "s1"), bcmPCARN("bill-estimate", "e1"), bcmPCARN("workload-estimate", "w1")
	for _, c := range []struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
		// unlisted is the dropped parent whose children must not be listed.
		unlisted string
	}{
		{"bill scenario/not found", scenario, TypeBcmPricingCalculatorBillScenario, bcmPCNotFound, -1, "", bcmPCTestTotal - 3, false, false, "s1"},
		{"bill scenario/denied", scenario, TypeBcmPricingCalculatorBillScenario, bcmPCDenied, -1, "", bcmPCTestTotal, true, true, ""},
		{"bill scenario/other error", scenario, TypeBcmPricingCalculatorBillScenario, bcmPCBadInput, outcomeError, "ValidationException", bcmPCTestTotal, true, false, ""},
		{"bill estimate/not found", estimate, TypeBcmPricingCalculatorBillEstimate, bcmPCNotFound, -1, "", bcmPCTestTotal - 1, false, false, ""},
		{"bill estimate/transient", estimate, TypeBcmPricingCalculatorBillEstimate, bcmPCThrottled, outcomeTransient, "ThrottlingException", bcmPCTestTotal, true, false, ""},
		{"bill estimate/denied", estimate, TypeBcmPricingCalculatorBillEstimate, bcmPCDenied, -1, "", bcmPCTestTotal, true, true, ""},
		{"workload estimate/not found", workload, TypeBcmPricingCalculatorWorkloadEstimate, bcmPCNotFound, -1, "", bcmPCTestTotal - 2, false, false, "w1"},
		{"workload estimate/transient", workload, TypeBcmPricingCalculatorWorkloadEstimate, bcmPCThrottled, outcomeTransient, "ThrottlingException", bcmPCTestTotal, true, false, ""},
		{"workload estimate/other error", workload, TypeBcmPricingCalculatorWorkloadEstimate, bcmPCBadInput, outcomeError, "ValidationException", bcmPCTestTotal, true, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := bcmPCTopLevelFake()
			f.errs[c.arn] = c.err

			total, _, err := bcmPCScan(context.Background(), st, f)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{bcmPCTagsOp}
			}
			if got := scnWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/us-east-1" {
					t.Errorf("warning Scope %q; want %s/us-east-1", x.Scope, testAccountID)
				}
			}
			for _, call := range f.calls.distinct() {
				if strings.Fields(call)[1] == c.unlisted {
					t.Errorf("child call %q for the dropped parent", call)
				}
			}
		})
	}
}

// Denied tag reads of every tag-read type send one warning for the whole
// scan; the rows are kept.
func TestScanBcmPricingCalculator_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := bcmPCTopLevelFake()
	for _, pt := range bcmPCParentTypes() {
		for _, id := range pt.ids {
			f.errs[bcmPCARN(pt.kind, id)] = bcmPCDenied
		}
	}

	total, _, err := bcmPCScan(context.Background(), st, f)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != bcmPCTestTotal {
		t.Errorf("total = %d; want %d", total, bcmPCTestTotal)
	}
	if got := scnWarnOps(warns()); !slices.Equal(got, []string{bcmPCTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, bcmPCTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanBcmPricingCalculator_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, bcmPCTagsOp)
	f := bcmPCTopLevelFake()
	f.errs[bcmPCARN("bill-scenario", "s2")] = bcmPCThrottled
	f.errs[bcmPCARN("bill-estimate", "e2")] = bcmPCBadInput

	_, _, err := bcmPCScan(context.Background(), st, f)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", bcmPCTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phase's rows
// stay stored.
func TestScanBcmPricingCalculator_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeBcmPricingCalculatorBillEstimate)
	f := bcmPCTopLevelFake()
	f.errs[bcmPCARN("bill-estimate", "e1")] = bcmPCDenied

	total, _, err := bcmPCScan(context.Background(), st, f)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := len(f.pageSizes("ListWorkloadEstimates")); n != 0 {
		t.Errorf("ListWorkloadEstimates called %d times; want 0 after the failed write", n)
	}
	if calls := f.calls.distinct(); len(calls) != 0 {
		t.Errorf("child calls = %v; want none after the failed write", calls)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (the bill scenarios)", total)
	}
	if n := len(rowsByNativeID(t, st, TypeBcmPricingCalculatorBillScenario)); n != 2 {
		t.Errorf("bill scenario rows = %d; want 2", n)
	}
}

// A listing's failed later page keeps the page read before it, and its error
// does not stop the later phases, the child phases included. A bill scenario
// not stored has no children read.
func TestScanBcmPricingCalculator_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, pt := range bcmPCParentTypes() {
		t.Run(pt.op, func(t *testing.T) {
			st := newTestStore(t)
			f := bcmPCTopLevelFake()
			f.errs[pt.op+" #1"] = bcmPCBadInput

			total, _, err := bcmPCScan(context.Background(), st, f)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != bcmPCTestTotal-1 {
				t.Errorf("total = %d; want %d", total, bcmPCTestTotal-1)
			}
			rows := rowsByNativeID(t, st, pt.typ)
			if _, ok := rows[bcmPCARN(pt.kind, pt.ids[0])]; !ok || len(rows) != 1 {
				t.Errorf("%s rows = %v; want only %s, read before the failure", pt.typ, slices.Collect(maps.Keys(rows)), pt.ids[0])
			}
			for _, other := range bcmPCParentTypes() {
				if n := len(rowsByNativeID(t, st, other.typ)); other.op != pt.op && n != 2 {
					t.Errorf("%s rows = %d; want 2", other.typ, n)
				}
			}
			for _, ph := range bcmPCChildPhases() {
				if _, ok := rowsByNativeID(t, st, ph.typ)[bcmPCChildNativeID(ph)]; !ok {
					t.Errorf("%s child of the first page's parent missing", ph.typ)
				}
			}
		})
	}
}

// A repeated nextToken ends the listing as an error, keeping its pages.
func TestScanBcmPricingCalculator_RepeatedTokenKeepsPages(t *testing.T) {
	for _, pt := range bcmPCParentTypes() {
		t.Run(pt.op, func(t *testing.T) {
			st := newTestStore(t)
			f := bcmPCTopLevelFake()
			f.repeat = pt.op

			total, _, err := bcmPCScan(context.Background(), st, f)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != bcmPCTestTotal {
				t.Errorf("total = %d; want %d", total, bcmPCTestTotal)
			}
			if n := len(rowsByNativeID(t, st, pt.typ)); n != 2 {
				t.Errorf("%s rows = %d; want 2", pt.typ, n)
			}
		})
	}
}

// Each listing writes its first page, with its tags, before the second page
// is read.
func TestScanBcmPricingCalculator_PagesWrittenAsRead(t *testing.T) {
	st := newTestStore(t)
	f := bcmPCTopLevelFake()
	storedFirst := map[string]bool{}
	f.before = func(op string, token *string) {
		if token != nil {
			pt := bcmPCParentTypes()[slices.IndexFunc(bcmPCParentTypes(), func(pt bcmPCParentType) bool { return pt.op == op })]
			storedFirst[op] = b2biStoredWithTags(st, bcmPCARN(pt.kind, pt.ids[0]))
		}
	}

	if _, _, err := bcmPCScan(context.Background(), st, f); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, pt := range bcmPCParentTypes() {
		if !storedFirst[pt.op] {
			t.Errorf("%s: the first page's row was not stored with its tags when the second page was read", pt.op)
		}
	}
}

// An element without an Id is not stored, is not sent to the tag op, and is
// warned about once under its listing, keyed by its name or the fallback
// text.
func TestScanBcmPricingCalculator_ElementWithoutID(t *testing.T) {
	for _, pt := range bcmPCParentTypes() {
		for _, name := range []string{"orphan", ""} {
			t.Run(pt.op+"/"+cmp.Or(name, "no name"), func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				f := bcmPCTopLevelFake()
				n := nonEmptyPtr(name)
				switch pt.op {
				case "ListBillScenarios":
					f.scenarios = append(f.scenarios, bcmpctypes.BillScenarioSummary{Name: n})
				case "ListBillEstimates":
					f.estimates = append(f.estimates, bcmpctypes.BillEstimateSummary{Name: n})
				case "ListWorkloadEstimates":
					f.workloads = append(f.workloads, bcmpctypes.WorkloadEstimateSummary{Name: n})
				}

				total, _, err := bcmPCScan(context.Background(), st, f)
				if err != nil {
					t.Fatalf("scan: %v", err)
				}
				if total != bcmPCTestTotal {
					t.Errorf("total = %d; want %d", total, bcmPCTestTotal)
				}
				w := warns()
				want := cmp.Or(name, noIdentifier)
				if len(w) != 1 || w[0].Service != "bcmpricingcalculator:"+pt.op || !strings.Contains(w[0].Message, want) {
					t.Fatalf("warnings = %v; want one under bcmpricingcalculator:%s naming %s", w, pt.op, want)
				}
				if got := len(f.sortedTagARNs()); got != 6 {
					t.Errorf("tag reads = %d; want 6", got)
				}
			})
		}
	}
}

// In GovCloud and China the built ARNs carry the account's partition, and
// reach the tag op unchanged.
func TestScanBcmPricingCalculator_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			f := bcmPCTopLevelFake()
			want := []string{
				"arn:" + pf.partition + ":bcm-pricing-calculator::123456789012:bill-estimate/e1",
				"arn:" + pf.partition + ":bcm-pricing-calculator::123456789012:bill-estimate/e2",
				"arn:" + pf.partition + ":bcm-pricing-calculator::123456789012:bill-scenario/s1",
				"arn:" + pf.partition + ":bcm-pricing-calculator::123456789012:bill-scenario/s2",
				"arn:" + pf.partition + ":bcm-pricing-calculator::123456789012:workload-estimate/w1",
				"arn:" + pf.partition + ":bcm-pricing-calculator::123456789012:workload-estimate/w2",
			}
			f.tags[want[2]] = map[string]string{"env": "gov-or-cn"}

			if _, _, err := scanBcmPricingCalculator(context.Background(), f.client(), partitionAccount(pf.partition), st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := f.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeBcmPricingCalculatorBillScenario)[want[2]].TagsJSON, map[string]string{"env": "gov-or-cn"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it, and the later phases are not run.
func TestScanBcmPricingCalculator_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	f := bcmPCTopLevelFake()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.errs["ListBillEstimates #1"] = context.Canceled
	f.before = func(op string, token *string) {
		if op == "ListBillEstimates" && token != nil {
			cancel()
		}
	}

	_, _, err := bcmPCScan(ctx, st, f)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := len(f.pageSizes("ListWorkloadEstimates")); n != 0 {
		t.Errorf("ListWorkloadEstimates called %d times after ctx ended", n)
	}
}

// TestScanBcmPricingCalculator_OlderPhaseErrorKeepsLaterPhases pins that a
// hard error of one parent listing skips neither the later listings nor the
// child phases, and is returned.
func TestScanBcmPricingCalculator_OlderPhaseErrorKeepsLaterPhases(t *testing.T) {
	st := newTestStore(t)
	f := bcmPCTopLevelFake()
	f.errs["ListBillEstimates #0"] = bcmPCBadInput
	total, _, err := bcmPCScan(context.Background(), st, f)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if total != bcmPCTestTotal-2 {
		t.Errorf("total = %d; want %d (every row but the bill estimates)", total, bcmPCTestTotal-2)
	}
	if n := len(rowsByNativeID(t, st, TypeBcmPricingCalculatorWorkloadEstimate)); n != 2 {
		t.Errorf("workload estimates = %d; want 2", n)
	}
	for _, ph := range bcmPCChildPhases() {
		if _, ok := rowsByNativeID(t, st, ph.typ)[bcmPCChildNativeID(ph)]; !ok {
			t.Errorf("%s child missing", ph.typ)
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
			f.errs[failing.op+" "+bcmPCTopLevelParent(failing)] = bcmPCBadInput
			total, _, err := bcmPCScan(context.Background(), st, f)
			if !isAPIErrorCode(err, "ValidationException") {
				t.Errorf("err = %v; want the %s ValidationException", err, failing.op)
			}
			if total != bcmPCTestTotal-1 {
				t.Errorf("total = %d; want %d", total, bcmPCTestTotal-1)
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

// bcmPCPayerOnly is the payer-only rejection bcmPCGatedPages maps to
// markServiceNotEntitled.
func bcmPCPayerOnly() error {
	return bcmPCAPIErr("ValidationException", "Operation not permitted for member accounts. This API is only allowed for regular or payer accounts.")
}

func bcmPCCostExplorerOff() error {
	return bcmPCAPIErr("AccessDeniedException", "User not enabled for cost explorer access")
}

// An account-level refusal of the first listing, with nothing stored, is the
// dispatch sentinel alone: Cost Explorer not enabled (the account can enable
// it) is disabled, payer-only is not entitled. The later parent listings and
// the child phases make no call, and nothing is warned about.
func TestScanBcmPricingCalculator_FirstPhaseSentinelReturnedAlone(t *testing.T) {
	for _, c := range []struct {
		name     string
		err      error
		sentinel error
		other    error
	}{
		{"cost explorer not enabled", bcmPCCostExplorerOff(), errServiceDisabled, errServiceNotEntitled},
		{"payer only", bcmPCPayerOnly(), errServiceNotEntitled, errServiceDisabled},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := bcmPCTopLevelFake()
			f.errs["ListBillScenarios #0"] = c.err
			total, _, err := bcmPCScan(context.Background(), st, f)
			if !errors.Is(err, c.sentinel) || errors.Is(err, c.other) || total != 0 {
				t.Errorf("scan = (%d, %v); want (0, %v alone)", total, err, c.sentinel)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			if n := len(f.pageSizes("ListBillEstimates")) + len(f.pageSizes("ListWorkloadEstimates")); n != 0 {
				t.Errorf("later listings called %d times; want 0", n)
			}
			if calls := f.calls.distinct(); len(calls) != 0 {
				t.Errorf("child calls = %v; want none", calls)
			}
		})
	}
}

// An account-level refusal after rows were stored, or after another listing
// failed, is a warning under the refused op, not a sentinel that would make
// the dispatcher discard the stored rows' counts or hide the failure. The
// refused listing keeps its earlier pages; the parent listings after it are
// not called, and the child phases run over the parents stored.
func TestScanBcmPricingCalculator_LaterSentinelWarns(t *testing.T) {
	for _, c := range []struct {
		name      string
		errs      map[string]error
		gatedOp   string
		wantTotal int
		wantCode  string
		unlisted  []string
	}{
		{
			name: "bill estimates after scenarios stored", errs: map[string]error{"ListBillEstimates #0": bcmPCPayerOnly()},
			gatedOp: "ListBillEstimates", wantTotal: 2 + 2, unlisted: []string{"ListWorkloadEstimates"},
		},
		{
			name: "later page of bill scenarios", errs: map[string]error{"ListBillScenarios #1": bcmPCCostExplorerOff()},
			gatedOp: "ListBillScenarios", wantTotal: 1 + 2, unlisted: []string{"ListBillEstimates", "ListWorkloadEstimates"},
		},
		{
			name:    "after a failed listing stored nothing",
			errs:    map[string]error{"ListBillScenarios #0": bcmPCBadInput, "ListBillEstimates #0": bcmPCCostExplorerOff()},
			gatedOp: "ListBillEstimates", wantTotal: 0, wantCode: "ValidationException", unlisted: []string{"ListWorkloadEstimates"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := bcmPCTopLevelFake()
			maps.Copy(f.errs, c.errs)
			total, _, err := bcmPCScan(context.Background(), st, f)
			if c.wantCode == "" && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.wantCode != "" {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, c.wantCode)
			}
			if errors.Is(err, errServiceDisabled) || errors.Is(err, errServiceNotEntitled) {
				t.Errorf("err = %v; want no sentinel", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			w := warns()
			if got := scnWarnOps(w); !slices.Equal(got, []string{"bcmpricingcalculator:" + c.gatedOp}) || w[0].Scope != testAccountID+"/us-east-1" {
				t.Errorf("warnings = %v; want one under bcmpricingcalculator:%s", w, c.gatedOp)
			}
			for _, op := range c.unlisted {
				if n := len(f.pageSizes(op)); n != 0 {
					t.Errorf("%s called %d times; want 0 after the refusal", op, n)
				}
			}
		})
	}
}

// A listing denied, or refused by the migration-required IAM deny on its
// first page, stores nothing of its own and keeps the other phases: a denial
// warns under its op, the migration deny is silent. The migration deny after
// a page was read keeps that page and warns as a denial.
func TestScanBcmPricingCalculator_ListingDenials(t *testing.T) {
	migrate := bcmPCAPIErr("AccessDeniedException", "To use this feature, please obtain the required permissions. Migrate the policies in your account to use the new IAM actions.")
	for _, c := range []struct {
		name      string
		key       string
		err       error
		wantTotal int
		wantWarn  bool
	}{
		{"denied", "ListBillEstimates #0", bcmPCAPIErr("AccessDeniedException", "User: x is not authorized to perform: bcm-pricing-calculator:ListBillEstimates"), bcmPCTestTotal - 2, true},
		{"migration deny first page", "ListBillEstimates #0", migrate, bcmPCTestTotal - 2, false},
		{"migration deny later page", "ListBillEstimates #1", migrate, bcmPCTestTotal - 1, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := bcmPCTopLevelFake()
			f.errs[c.key] = c.err
			total, _, err := bcmPCScan(context.Background(), st, f)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			var want []string
			if c.wantWarn {
				want = []string{bcmPCBillEstimatesOp}
			}
			if got := scnWarnOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
			if n := len(rowsByNativeID(t, st, TypeBcmPricingCalculatorWorkloadEstimate)); n != 2 {
				t.Errorf("workload estimates = %d; want 2", n)
			}
		})
	}
}

// TestScanBcmPricingCalculator_OlderPhaseErrorDoesNotHideChildHardError fails
// a parent listing after bill scenarios were stored, with a sentinel and with
// a transient error, while a child phase fails hard: the dispatcher must see
// the hard error, and the parent listing's error is one warning under its op.
func TestScanBcmPricingCalculator_OlderPhaseErrorDoesNotHideChildHardError(t *testing.T) {
	cases := []struct {
		name         string
		estimatesErr error
	}{
		{name: "not-entitled", estimatesErr: bcmPCPayerOnly()},
		{name: "transient", estimatesErr: bcmPCThrottled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, bcmPCBillEstimatesOp)
			f := bcmPCTopLevelFake()
			f.errs["ListBillEstimates #0"] = tc.estimatesErr
			f.errs["ListBillScenarioCommitmentModifications s1"] = bcmPCBadInput
			_, _, err := bcmPCScan(context.Background(), st, f)
			if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "ValidationException") {
				t.Errorf("outcome = %d, err = %v; want outcomeError (%d) with the child ValidationException", got, err, outcomeError)
			}
			if *warnings != 1 {
				t.Errorf("warnings = %d; want 1", *warnings)
			}
		})
	}
}

// An NXDOMAIN after rows were stored, from a later listing or a tag read, is
// a failure of this scan, not a region gap: the counts are kept. An NXDOMAIN
// of the first listing, with nothing stored, stays region-unavailable: no
// later listing or child phase runs, and nothing is warned about.
func TestScanBcmPricingCalculator_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "bcm-pricing-calculator.us-east-1.api.aws", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		f := bcmPCTopLevelFake()
		f.errs["ListBillEstimates #0"] = nx
		total, _, err := bcmPCScan(context.Background(), st, f)
		assertGlueOutcome(t, err, outcomeError)
		if want := bcmPCTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		f := bcmPCTopLevelFake()
		f.errs[bcmPCARN("bill-estimate", "e1")] = nx
		total, _, err := bcmPCScan(context.Background(), st, f)
		assertGlueOutcome(t, err, outcomeError)
		if total != bcmPCTestTotal {
			t.Errorf("total = %d; want %d", total, bcmPCTestTotal)
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		f := bcmPCTopLevelFake()
		f.errs["ListBillScenarios #0"] = nx
		_, _, err := bcmPCScan(context.Background(), st, f)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := len(f.pageSizes("ListBillEstimates")) + len(f.pageSizes("ListWorkloadEstimates")); n != 0 {
			t.Errorf("later listings called %d times; want 0 once the region proved unavailable", n)
		}
		if calls := f.calls.distinct(); len(calls) != 0 {
			t.Errorf("child calls = %v; want none", calls)
		}
	})
}

// A listed element with no Status is stored with Status "" (not nil), as
// before tags were read, so its identity does not change.
func TestScanBcmPricingCalculator_EmptyStatusKept(t *testing.T) {
	st := newTestStore(t)
	f := newBcmPCFake()
	f.scenarios = []bcmpctypes.BillScenarioSummary{{Id: sdkaws.String("s1")}}
	f.estimates = []bcmpctypes.BillEstimateSummary{{Id: sdkaws.String("e1")}}
	f.workloads = []bcmpctypes.WorkloadEstimateSummary{{Id: sdkaws.String("w1")}}
	if _, _, err := bcmPCScan(context.Background(), st, f); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, c := range []struct{ typ, kind, id string }{
		{TypeBcmPricingCalculatorBillScenario, "bill-scenario", "s1"},
		{TypeBcmPricingCalculatorBillEstimate, "bill-estimate", "e1"},
		{TypeBcmPricingCalculatorWorkloadEstimate, "workload-estimate", "w1"},
	} {
		r, ok := rowsByNativeID(t, st, c.typ)[bcmPCARN(c.kind, c.id)]
		if !ok {
			t.Errorf("%s row %s missing", c.typ, c.id)
			continue
		}
		if r.Status == nil || *r.Status != "" {
			t.Errorf("%s Status = %v; want a pointer to \"\"", c.typ, r.Status)
		}
	}
}
