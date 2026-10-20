package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/drs"
	drstypes "github.com/aws/aws-sdk-go-v2/service/drs/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	drsDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: drs:List"}
	drsBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	drsGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
)

func drsTestARN(kind, id string) string { return drsARN(testRegion, testAccountID, kind, id) }

func drsActionNativeID(parentARN, actionID string) string {
	return parentARN + "/launch-action/" + actionID
}

func nativeIDs(rows map[string]store.Resource) []string {
	out := make([]string, 0, len(rows))
	for id := range rows {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// drsPlanHandlers serves ListRecoveryPlans over two pages (plan1; then plan2
// and an element with no ARN, or fail) and GetRecoveryPlan for any plan, with
// the plan in getFail returning that error.
func drsPlanHandlers(listFail error, getFail map[string]error) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListRecoveryPlans": func(in any) (any, error) {
			if in.(*drs.ListRecoveryPlansInput).NextToken == nil {
				return &drs.ListRecoveryPlansOutput{RecoveryPlans: []drstypes.RecoveryPlanSummary{{RecoveryPlanArn: ptrStr(drsTestARN("recovery-plan", "plan1"))}}, NextToken: ptrStr("p2")}, nil
			}
			if listFail != nil {
				return nil, listFail
			}
			return &drs.ListRecoveryPlansOutput{RecoveryPlans: []drstypes.RecoveryPlanSummary{
				{RecoveryPlanArn: ptrStr(drsTestARN("recovery-plan", "plan2"))},
				{Name: ptrStr("no-arn")},
			}}, nil
		},
		"GetRecoveryPlan": func(in any) (any, error) {
			arn := sv(in.(*drs.GetRecoveryPlanInput).RecoveryPlanArn)
			if err := getFail[arn]; err != nil {
				return nil, err
			}
			plan := &drstypes.RecoveryPlan{RecoveryPlanArn: &arn, Name: ptrStr("name-" + strings.TrimPrefix(arn, drsTestARN("recovery-plan", ""))), Description: ptrStr("desc"), Tags: map[string]string{"env": "dr"}}
			if arn == drsTestARN("recovery-plan", "plan1") {
				plan.Status = drstypes.RecoveryPlanStatusActive
			}
			return &drs.GetRecoveryPlanOutput{RecoveryPlan: plan}, nil
		},
	}
}

func scanDRSPlans(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, []childParent, error) {
	t.Helper()
	client := drs.NewFromConfig(opStubConfig(handlers, drsDenied))
	total, _, parents, err := scanDRSRecoveryPlans(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, parents, err
}

func parentIDs(ps []childParent) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.id)
	}
	sort.Strings(out)
	return out
}

func TestScanDRSRecoveryPlans_StoresEveryPageSkipsARNless(t *testing.T) {
	st := newTestStore(t)
	total, parents, err := scanDRSPlans(t, st, drsPlanHandlers(nil, nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	plan1, plan2 := drsTestARN("recovery-plan", "plan1"), drsTestARN("recovery-plan", "plan2")
	rows := rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)
	if want := []string{plan1, plan2}; total != 2 || !slices.Equal(nativeIDs(rows), want) || !slices.Equal(parentIDs(parents), want) {
		t.Fatalf("total = %d, rows = %v, parents = %v; want %v", total, nativeIDs(rows), parentIDs(parents), want)
	}
	row := rows[plan1]
	if sv(row.Name) != "name-plan1" || sv(row.Status) != "ACTIVE" || sv(row.Region) != testRegion ||
		!jsonHasString(t, row.AttributesJSON, "Description", "desc") || sv(row.TagsJSON) == "" || !jsonHasString(t, sv(row.TagsJSON), "env", "dr") {
		t.Errorf("row name/status/region = %q/%q/%q, tags = %s, attrs = %s", sv(row.Name), sv(row.Status), sv(row.Region), sv(row.TagsJSON), row.AttributesJSON)
	}
	if rows[plan2].Status != nil {
		t.Errorf("plan2 status = %q, want unset for an absent Status", sv(rows[plan2].Status))
	}
}

func TestScanDRSRecoveryPlans_Empty(t *testing.T) {
	st := newTestStore(t)
	var gets atomic.Int32
	total, parents, err := scanDRSPlans(t, st, map[string]func(in any) (any, error){
		"ListRecoveryPlans": func(any) (any, error) { return &drs.ListRecoveryPlansOutput{}, nil },
		"GetRecoveryPlan": func(any) (any, error) {
			gets.Add(1)
			return &drs.GetRecoveryPlanOutput{}, nil
		},
	})
	if err != nil || total != 0 || len(parents) != 0 || gets.Load() != 0 || len(rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)) != 0 {
		t.Errorf("total = %d, parents = %v, gets = %d, err = %v; want nothing, nil", total, parents, gets.Load(), err)
	}
}

// A denial on page two warns once and keeps page one's plan; any other error
// fails the phase.
func TestScanDRSRecoveryPlans_FailedListPage(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "drs:ListRecoveryPlans")
		total, parents, err := scanDRSPlans(t, st, drsPlanHandlers(drsDenied, nil))
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if *warns != 1 {
			t.Errorf("warnings = %d, want 1", *warns)
		}
		want := []string{drsTestARN("recovery-plan", "plan1")}
		if total != 1 || !slices.Equal(nativeIDs(rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)), want) || !slices.Equal(parentIDs(parents), want) {
			t.Errorf("total = %d, parents = %v; want page one's plan1 stored", total, parentIDs(parents))
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		_, _, err := scanDRSPlans(t, st, drsPlanHandlers(drsBadInput, nil))
		assertAPICode(t, err, "ValidationException")
	})
}

// A plan that fails GetRecoveryPlan drops only itself, from the rows and from
// the parents handed to the step phase: deleted since listing is silent, a
// denial warns once, and any other error fails the phase.
func TestScanDRSRecoveryPlans_FailedGet(t *testing.T) {
	plan1, plan2 := drsTestARN("recovery-plan", "plan1"), drsTestARN("recovery-plan", "plan2")
	for _, tc := range []struct {
		name     string
		err      error
		wantWarn int
	}{
		{"plan gone", drsGone, 0},
		{"denied", drsDenied, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "drs:GetRecoveryPlan")
			total, parents, err := scanDRSPlans(t, st, drsPlanHandlers(nil, map[string]error{plan1: tc.err}))
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			want := []string{plan2}
			if total != 1 || !slices.Equal(nativeIDs(rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)), want) || !slices.Equal(parentIDs(parents), want) {
				t.Errorf("total = %d, parents = %v; want only plan2", total, parentIDs(parents))
			}
		})
	}
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		_, _, err := scanDRSPlans(t, st, drsPlanHandlers(nil, map[string]error{plan2: drsBadInput}))
		assertAPICode(t, err, "ValidationException")
	})
}

// drsTopLevelHandlers answers source servers s-1 and s-2, launch template
// lct-1 (the last pre-existing phase), the plan handlers, and:
//   - ListRecoveryPlanSteps: plan1 over two pages (a server step on s-1; a
//     wait step, a step with a union member newer than the SDK, and an element
//     with no ARN), plan2 none.
//   - ListLaunchActions: lct-1 over two pages (a1; a2 and an element with no
//     id), s-1 the same id a1, s-2 none.
//
// Each list records the parent it was called for. Every other op is denied and
// warn-skipped by its phase.
func drsTopLevelHandlers(stepCalls, actionCalls *parentCalls) map[string]func(in any) (any, error) {
	h := drsPlanHandlers(nil, nil)
	h["DescribeSourceServers"] = func(any) (any, error) {
		return &drs.DescribeSourceServersOutput{Items: []drstypes.SourceServer{
			{Arn: ptrStr(drsTestARN("source-server", "s-1")), SourceServerID: ptrStr("s-1")},
			{Arn: ptrStr(drsTestARN("source-server", "s-2")), SourceServerID: ptrStr("s-2")},
		}}, nil
	}
	h["DescribeLaunchConfigurationTemplates"] = func(any) (any, error) {
		return &drs.DescribeLaunchConfigurationTemplatesOutput{Items: []drstypes.LaunchConfigurationTemplate{
			{Arn: ptrStr(drsTestARN("launch-configuration-template", "lct-1")), LaunchConfigurationTemplateID: ptrStr("lct-1")},
		}}, nil
	}
	h["ListRecoveryPlanSteps"] = func(in any) (any, error) {
		p := in.(*drs.ListRecoveryPlanStepsInput)
		plan := sv(p.RecoveryPlanArn)
		stepCalls.add(plan)
		if plan != drsTestARN("recovery-plan", "plan1") {
			return &drs.ListRecoveryPlanStepsOutput{}, nil
		}
		if p.NextToken == nil {
			return &drs.ListRecoveryPlanStepsOutput{NextToken: ptrStr("p2"), RecoveryPlanSteps: []drstypes.RecoveryPlanStep{{
				RecoveryPlanStepArn: ptrStr(drsTestARN("recovery-plan-step", "step1")), StepName: ptrStr("servers"),
				Configuration: &drstypes.RecoveryPlanStepConfigurationMemberServerStepConfiguration{Value: drstypes.ServerStepConfiguration{
					Servers: []drstypes.RecoveryPlanServer{{ServerArn: ptrStr(drsTestARN("source-server", "s-1"))}},
				}},
			}}}, nil
		}
		return &drs.ListRecoveryPlanStepsOutput{RecoveryPlanSteps: []drstypes.RecoveryPlanStep{
			{
				RecoveryPlanStepArn: ptrStr(drsTestARN("recovery-plan-step", "step2")), StepName: ptrStr("wait"),
				Configuration: &drstypes.RecoveryPlanStepConfigurationMemberWaitStepConfiguration{Value: drstypes.WaitStepConfiguration{WaitDurationMinutes: ptrInt32(5)}},
			},
			{
				RecoveryPlanStepArn: ptrStr(drsTestARN("recovery-plan-step", "step3")), StepName: ptrStr("future"),
				Configuration: &drstypes.UnknownUnionMember{Tag: "futureStepConfiguration", Value: []byte(`{"x":1}`)},
			},
			{StepName: ptrStr("no-arn")},
		}}, nil
	}
	h["ListLaunchActions"] = func(in any) (any, error) {
		p := in.(*drs.ListLaunchActionsInput)
		id := sv(p.ResourceId)
		actionCalls.add(id)
		switch {
		case id == "s-1":
			return &drs.ListLaunchActionsOutput{Items: []drstypes.LaunchAction{{ActionId: ptrStr("a1"), Name: ptrStr("server-a1")}}}, nil
		case id != "lct-1":
			return &drs.ListLaunchActionsOutput{}, nil
		case p.NextToken == nil:
			return &drs.ListLaunchActionsOutput{NextToken: ptrStr("p2"), Items: []drstypes.LaunchAction{{ActionId: ptrStr("a1"), Name: ptrStr("template-a1"), Category: drstypes.LaunchActionCategorySecurity}}}, nil
		default:
			return &drs.ListLaunchActionsOutput{Items: []drstypes.LaunchAction{{ActionId: ptrStr("a2"), Name: ptrStr("template-a2")}, {Name: ptrStr("no-id")}}}, nil
		}
	}
	return h
}

func ptrInt32(v int32) *int32 { return &v }

func scanDRSTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) error {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, drsDenied)
	_, _, err := scanDRS(context.Background(), acct, testRegion, st, testScanID)
	return err
}

func TestScanDRS_StoresRecoveryPlanStepsUnderTheirPlan(t *testing.T) {
	st := newTestStore(t)
	var stepCalls, actionCalls parentCalls
	if err := scanDRSTopLevel(t, st, drsTopLevelHandlers(&stepCalls, &actionCalls)); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	plan1, plan2 := drsTestARN("recovery-plan", "plan1"), drsTestARN("recovery-plan", "plan2")
	if got := stepCalls.distinct(); !slices.Equal(got, []string{plan1, plan2}) {
		t.Errorf("steps listed for %v, want [%s %s]", got, plan1, plan2)
	}
	step1, step2, step3 := drsTestARN("recovery-plan-step", "step1"), drsTestARN("recovery-plan-step", "step2"), drsTestARN("recovery-plan-step", "step3")
	rows := rowsByNativeID(t, st, TypeDRSRecoveryPlanStep)
	if got := nativeIDs(rows); !slices.Equal(got, []string{step1, step2, step3}) {
		t.Fatalf("step rows = %v, want [%s %s %s]", got, step1, step2, step3)
	}
	assertContainsNative(t, st, plan1, step1)
	assertContainsNative(t, st, plan1, step2)
	assertContainsNative(t, st, plan1, step3)

	var server, wait, unknown struct {
		Configuration map[string]json.RawMessage
	}
	if err := json.Unmarshal([]byte(rows[step1].AttributesJSON), &server); err != nil {
		t.Fatalf("step1 attrs: %v", err)
	}
	if err := json.Unmarshal([]byte(rows[step2].AttributesJSON), &wait); err != nil {
		t.Fatalf("step2 attrs: %v", err)
	}
	if err := json.Unmarshal([]byte(rows[step3].AttributesJSON), &unknown); err != nil {
		t.Fatalf("step3 attrs: %v", err)
	}
	var member drstypes.UnknownUnionMember
	if err := json.Unmarshal(unknown.Configuration["UnknownMember"], &member); err != nil || len(unknown.Configuration) != 1 ||
		member.Tag != "futureStepConfiguration" || string(member.Value) != `{"x":1}` {
		t.Errorf("unknown-member step Configuration = %s (decode err %v)", rows[step3].AttributesJSON, err)
	}
	if got := string(server.Configuration["ServerStepConfiguration"]); got != `{"Servers":[{"ServerArn":"`+drsTestARN("source-server", "s-1")+`","ImpactLevel":""}]}` || len(server.Configuration) != 1 {
		t.Errorf("server step Configuration = %s", rows[step1].AttributesJSON)
	}
	if got := string(wait.Configuration["WaitStepConfiguration"]); got != `{"WaitDurationMinutes":5}` || len(wait.Configuration) != 1 {
		t.Errorf("wait step Configuration = %s", rows[step2].AttributesJSON)
	}
	if sv(rows[step1].Name) != "servers" || sv(rows[step1].Region) != testRegion {
		t.Errorf("step1 name/region = %q/%q", sv(rows[step1].Name), sv(rows[step1].Region))
	}
}

func TestScanDRS_StoresLaunchActionsUnderTemplatesAndServers(t *testing.T) {
	st := newTestStore(t)
	var stepCalls, actionCalls parentCalls
	if err := scanDRSTopLevel(t, st, drsTopLevelHandlers(&stepCalls, &actionCalls)); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	if got := actionCalls.distinct(); !slices.Equal(got, []string{"lct-1", "s-1", "s-2"}) {
		t.Errorf("launch actions listed for %v, want [lct-1 s-1 s-2]", got)
	}
	lct, s1 := drsTestARN("launch-configuration-template", "lct-1"), drsTestARN("source-server", "s-1")
	tA1, tA2, sA1 := drsActionNativeID(lct, "a1"), drsActionNativeID(lct, "a2"), drsActionNativeID(s1, "a1")
	rows := rowsByNativeID(t, st, TypeDRSLaunchAction)
	want := []string{tA1, tA2, sA1}
	sort.Strings(want)
	if got := nativeIDs(rows); !slices.Equal(got, want) {
		t.Fatalf("launch action rows = %v, want %v", got, want)
	}
	assertContainsNative(t, st, lct, tA1)
	assertContainsNative(t, st, lct, tA2)
	assertContainsNative(t, st, s1, sA1)
	row := rows[tA1]
	if sv(row.Name) != "template-a1" || sv(row.Region) != testRegion || !jsonHasString(t, row.AttributesJSON, "Category", "SECURITY") {
		t.Errorf("row name/region = %q/%q, attrs = %s", sv(row.Name), sv(row.Region), row.AttributesJSON)
	}
}

// One action id under a template and under a source server is two actions;
// the parent ARN in the NativeID keeps their identities apart.
func TestScanDRS_SameActionIDUnderTwoParentsKeepsTwoIdentities(t *testing.T) {
	st := newTestStore(t)
	var stepCalls, actionCalls parentCalls
	if err := scanDRSTopLevel(t, st, drsTopLevelHandlers(&stepCalls, &actionCalls)); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeDRSLaunchAction)
	tpl := rows[drsActionNativeID(drsTestARN("launch-configuration-template", "lct-1"), "a1")]
	srv := rows[drsActionNativeID(drsTestARN("source-server", "s-1"), "a1")]
	if tpl.ID == "" || srv.ID == "" || tpl.ID == srv.ID || sv(tpl.Name) != "template-a1" || sv(srv.Name) != "server-a1" {
		t.Errorf("template a1 = %s/%q, server a1 = %s/%q; want two rows with their own names", tpl.ID, sv(tpl.Name), srv.ID, sv(srv.Name))
	}
}

func TestScanDRS_NoParentsNoChildCalls(t *testing.T) {
	st := newTestStore(t)
	var stepCalls, actionCalls parentCalls
	h := drsTopLevelHandlers(&stepCalls, &actionCalls)
	delete(h, "DescribeSourceServers")
	delete(h, "DescribeLaunchConfigurationTemplates")
	h["ListRecoveryPlans"] = func(any) (any, error) { return &drs.ListRecoveryPlansOutput{}, nil }
	if err := scanDRSTopLevel(t, st, h); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	if len(stepCalls.distinct()) != 0 || len(actionCalls.distinct()) != 0 {
		t.Errorf("step calls = %v, action calls = %v; want none", stepCalls.distinct(), actionCalls.distinct())
	}
	if n := len(rowsByNativeID(t, st, TypeDRSRecoveryPlanStep)) + len(rowsByNativeID(t, st, TypeDRSLaunchAction)); n != 0 {
		t.Errorf("child rows = %d, want 0", n)
	}
}

// A failed parent drops only itself: deleted since listing is silent, a
// denial warns once however many parents hit it, and any other error fails
// the scan. Both plans fail in the step case; the launch-action case keeps the
// healthy s-1's action.
func TestScanDRS_FailedChildParent(t *testing.T) {
	for _, tc := range []struct {
		op, bad1, bad2, childType string
		want                      []string
	}{
		{
			op: "ListRecoveryPlanSteps", bad1: drsTestARN("recovery-plan", "plan1"), bad2: drsTestARN("recovery-plan", "plan2"),
			childType: TypeDRSRecoveryPlanStep, want: []string{},
		},
		{
			op: "ListLaunchActions", bad1: "lct-1", bad2: "s-2",
			childType: TypeDRSLaunchAction, want: []string{drsActionNativeID(drsTestARN("source-server", "s-1"), "a1")},
		},
	} {
		for _, ec := range []struct {
			name     string
			err      error
			wantWarn int
		}{
			{"parent gone", drsGone, 0},
			{"denied", drsDenied, 1},
		} {
			t.Run(tc.op+"/"+ec.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "drs:"+tc.op)
				var stepCalls, actionCalls parentCalls
				h := drsTopLevelHandlers(&stepCalls, &actionCalls)
				h[tc.op] = failParents(h[tc.op], ec.err, tc.bad1, tc.bad2)
				if err := scanDRSTopLevel(t, st, h); err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if *warns != ec.wantWarn {
					t.Errorf("warnings = %d, want %d", *warns, ec.wantWarn)
				}
				if got := nativeIDs(rowsByNativeID(t, st, tc.childType)); !slices.Equal(got, tc.want) {
					t.Errorf("rows = %v, want %v", got, tc.want)
				}
			})
		}
		t.Run(tc.op+"/other", func(t *testing.T) {
			st := newTestStore(t)
			var stepCalls, actionCalls parentCalls
			h := drsTopLevelHandlers(&stepCalls, &actionCalls)
			h[tc.op] = failParents(h[tc.op], drsBadInput, tc.bad1, tc.bad1)
			assertAPICode(t, scanDRSTopLevel(t, st, h), "ValidationException")
		})
	}
}

// failParents wraps a child-list handler so the parents bad1 and bad2 fail
// with err.
func failParents(next func(in any) (any, error), err error, bad1, bad2 string) func(in any) (any, error) {
	return func(in any) (any, error) {
		var id string
		switch p := in.(type) {
		case *drs.ListRecoveryPlanStepsInput:
			id = sv(p.RecoveryPlanArn)
		case *drs.ListLaunchActionsInput:
			id = sv(p.ResourceId)
		}
		if id == bad1 || id == bad2 {
			return nil, err
		}
		return next(in)
	}
}

func TestScanDRS_UninitializedAccountDisabled(t *testing.T) {
	st := newTestStore(t)
	var stepCalls, actionCalls parentCalls
	h := drsTopLevelHandlers(&stepCalls, &actionCalls)
	h["DescribeSourceServers"] = func(any) (any, error) {
		return nil, apiErr("UninitializedAccountException", "Account not initialized")
	}
	if err := scanDRSTopLevel(t, st, h); !errors.Is(err, errServiceDisabled) {
		t.Fatalf("err = %v; want service-disabled sentinel", err)
	}
}

// A recovery-plan phase runs after the launch actions, so its hard error
// leaves every launch action stored.
func TestScanDRS_PlanPhaseErrorKeepsLaunchActions(t *testing.T) {
	for _, op := range []string{"ListRecoveryPlans", "GetRecoveryPlan", "ListRecoveryPlanSteps"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var stepCalls, actionCalls parentCalls
			h := drsTopLevelHandlers(&stepCalls, &actionCalls)
			h[op] = func(any) (any, error) { return nil, drsBadInput }
			assertAPICode(t, scanDRSTopLevel(t, st, h), "ValidationException")
			if n := len(rowsByNativeID(t, st, TypeDRSLaunchAction)); n != 3 {
				t.Errorf("launch action rows = %d, want 3: %s ran before the launch actions", n, op)
			}
		})
	}
}

// Each new op runs after every pre-existing phase, and its hard error reaches
// the caller.
func TestScanDRS_NewPhaseErrorAfterExistingPhases(t *testing.T) {
	for _, op := range []string{"ListRecoveryPlans", "GetRecoveryPlan", "ListRecoveryPlanSteps", "ListLaunchActions"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var stepCalls, actionCalls parentCalls
			h := drsTopLevelHandlers(&stepCalls, &actionCalls)
			var failed atomic.Bool
			h[op] = func(any) (any, error) {
				failed.Store(true)
				return nil, drsBadInput
			}
			err := scanDRSTopLevel(t, st, h)
			if !failed.Load() {
				t.Fatalf("%s never called", op)
			}
			assertAPICode(t, err, "ValidationException")
			if n := len(rowsByNativeID(t, st, TypeDRSLaunchConfigurationTemplateResource)); n != 1 {
				t.Errorf("launch template rows = %d, want 1: %s ran before the last pre-existing phase", n, op)
			}
		})
	}
}

// The client is pinned to the scanned region, not the config's default.
func TestScanDRS_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var stepCalls, actionCalls, regions parentCalls
	cfg := opStubConfig(drsTopLevelHandlers(&stepCalls, &actionCalls), drsDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanDRS(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	if row, ok := rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)[drsTestARN("recovery-plan", "plan1")]; !ok || sv(row.Region) != "eu-west-1" {
		t.Errorf("plan row = %+v (found %v), want region eu-west-1", row, ok)
	}
}

func drsReplicationConfigNativeID(serverID string) string {
	return drsTestARN("source-server", serverID) + "/replication-configuration"
}

var drsTestKeyARN = "arn:aws:kms:" + testRegion + ":" + testAccountID + ":key/k-drs"

// drsReplicationConfigHandler answers GetReplicationConfiguration for any
// server, recording the server id; s-1 carries the staging subnet, security
// group, EBS key, bandwidth, PIT policy, instance type and staging-area tags,
// and the server in fail returns err.
func drsReplicationConfigHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		id := sv(in.(*drs.GetReplicationConfigurationInput).SourceServerID)
		calls.add(id)
		if err := fail[id]; err != nil {
			return nil, err
		}
		out := &drs.GetReplicationConfigurationOutput{SourceServerID: &id, Name: ptrStr("cfg-" + id)}
		if id == "s-1" {
			out.StagingAreaSubnetId = ptrStr("subnet-1")
			out.ReplicationServersSecurityGroupsIDs = []string{"sg-1"}
			out.EbsEncryptionKeyArn = &drsTestKeyARN
			out.BandwidthThrottling = 100
			out.ReplicationServerInstanceType = ptrStr("t3.small")
			out.PitPolicy = []drstypes.PITPolicyRule{{Interval: ptrInt32(10), RetentionDuration: ptrInt32(60), Units: drstypes.PITPolicyRuleUnitsMinute}}
			out.StagingAreaTags = map[string]string{"team": "dr"}
		}
		return out, nil
	}
}

// scanDRSReplicationConfigs seeds source servers s-1 and s-2 and runs the
// replication-configuration phase over them.
func scanDRSReplicationConfigs(t *testing.T, st *store.Store, fail map[string]error) (total, inserted int, calls *parentCalls, err error) {
	t.Helper()
	calls = &parentCalls{}
	var servers []childParent
	for _, id := range []string{"s-1", "s-2"} {
		arn := drsTestARN("source-server", id)
		upsertTestResource(t, st, "aws", testAccountID, TypeDRSSourceServerResource, arn, testRegion, "{}")
		servers = append(servers, childParent{id: id, arn: arn})
	}
	client := drs.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"GetReplicationConfiguration": drsReplicationConfigHandler(calls, fail),
	}, drsDenied))
	total, inserted, err = scanDRSReplicationConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, servers)
	return total, inserted, calls, err
}

func TestScanDRSReplicationConfigurations_OneRowPerServer(t *testing.T) {
	st := newTestStore(t)
	total, inserted, calls, err := scanDRSReplicationConfigs(t, st, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"s-1", "s-2"}) {
		t.Errorf("configurations read for %v, want [s-1 s-2]", got)
	}
	c1, c2 := drsReplicationConfigNativeID("s-1"), drsReplicationConfigNativeID("s-2")
	rows := rowsByNativeID(t, st, TypeDRSReplicationConfiguration)
	if total != 2 || inserted != 2 || !slices.Equal(nativeIDs(rows), []string{c1, c2}) {
		t.Fatalf("total/inserted = %d/%d, rows = %v; want 2/2 [%s %s]", total, inserted, nativeIDs(rows), c1, c2)
	}
	assertContainsNative(t, st, drsTestARN("source-server", "s-1"), c1)
	assertContainsNative(t, st, drsTestARN("source-server", "s-2"), c2)
	row := rows[c1]
	if sv(row.Name) != "cfg-s-1" || sv(row.Region) != testRegion || row.ManagedByProvider {
		t.Errorf("row name/region/managed = %q/%q/%v", sv(row.Name), sv(row.Region), row.ManagedByProvider)
	}
	var attrs drs.GetReplicationConfigurationOutput
	if err := json.Unmarshal([]byte(row.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs %s: %v", row.AttributesJSON, err)
	}
	if sv(attrs.StagingAreaSubnetId) != "subnet-1" || !slices.Equal(attrs.ReplicationServersSecurityGroupsIDs, []string{"sg-1"}) ||
		sv(attrs.EbsEncryptionKeyArn) != drsTestKeyARN || attrs.BandwidthThrottling != 100 || sv(attrs.ReplicationServerInstanceType) != "t3.small" ||
		len(attrs.PitPolicy) != 1 || attrs.PitPolicy[0].Units != drstypes.PITPolicyRuleUnitsMinute || attrs.StagingAreaTags["team"] != "dr" {
		t.Errorf("attrs = %s", row.AttributesJSON)
	}
	if strings.Contains(row.AttributesJSON, "ResultMetadata") {
		t.Errorf("attrs carry the SDK's ResultMetadata envelope: %s", row.AttributesJSON)
	}
}

func TestScanDRSReplicationConfigurations_NoServersNoCalls(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := drs.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"GetReplicationConfiguration": drsReplicationConfigHandler(&calls, nil),
	}, drsDenied))
	total, inserted, err := scanDRSReplicationConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
	if err != nil || total != 0 || inserted != 0 || len(calls.distinct()) != 0 || len(rowsByNativeID(t, st, TypeDRSReplicationConfiguration)) != 0 {
		t.Errorf("total/inserted = %d/%d, calls = %v, err = %v; want nothing, nil", total, inserted, calls.distinct(), err)
	}
}

// A failed server drops only itself: deleted since listing is silent, a
// denial or a transient error warns once however many servers hit it, and any
// other error fails the phase.
func TestScanDRSReplicationConfigurations_FailedServer(t *testing.T) {
	throttled := apiErr("ThrottlingException", "Rate exceeded")
	for _, tc := range []struct {
		name     string
		fail     map[string]error
		wantWarn int
		want     []string
	}{
		{"server gone", map[string]error{"s-1": drsGone}, 0, []string{drsReplicationConfigNativeID("s-2")}},
		{"denied", map[string]error{"s-1": drsDenied, "s-2": drsDenied}, 1, []string{}},
		{"transient", map[string]error{"s-1": throttled, "s-2": throttled}, 1, []string{}},
		{"one transient", map[string]error{"s-1": throttled}, 1, []string{drsReplicationConfigNativeID("s-2")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "drs:GetReplicationConfiguration")
			total, _, _, err := scanDRSReplicationConfigs(t, st, tc.fail)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeDRSReplicationConfiguration)); total != len(tc.want) || !slices.Equal(got, tc.want) {
				t.Errorf("total = %d, rows = %v; want %v", total, got, tc.want)
			}
		})
	}
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		_, _, _, err := scanDRSReplicationConfigs(t, st, map[string]error{"s-2": drsBadInput})
		assertAPICode(t, err, "ValidationException")
	})
}

func TestScanDRS_StoresReplicationConfigurationsUnderTheirServers(t *testing.T) {
	st := newTestStore(t)
	var stepCalls, actionCalls, configCalls parentCalls
	h := drsTopLevelHandlers(&stepCalls, &actionCalls)
	h["GetReplicationConfiguration"] = drsReplicationConfigHandler(&configCalls, nil)
	if err := scanDRSTopLevel(t, st, h); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	if got := configCalls.distinct(); !slices.Equal(got, []string{"s-1", "s-2"}) {
		t.Errorf("configurations read for %v, want [s-1 s-2]", got)
	}
	c1, c2 := drsReplicationConfigNativeID("s-1"), drsReplicationConfigNativeID("s-2")
	if got := nativeIDs(rowsByNativeID(t, st, TypeDRSReplicationConfiguration)); !slices.Equal(got, []string{c1, c2}) {
		t.Fatalf("configuration rows = %v, want [%s %s]", got, c1, c2)
	}
	assertContainsNative(t, st, drsTestARN("source-server", "s-1"), c1)
	assertContainsNative(t, st, drsTestARN("source-server", "s-2"), c2)
}

// The replication-configuration phase runs after every other phase, so its
// hard error reaches the caller with the recovery-plan steps and launch
// actions already stored.
func TestScanDRS_ReplicationConfigurationErrorAfterEveryPhase(t *testing.T) {
	st := newTestStore(t)
	var stepCalls, actionCalls, configCalls parentCalls
	h := drsTopLevelHandlers(&stepCalls, &actionCalls)
	h["GetReplicationConfiguration"] = drsReplicationConfigHandler(&configCalls, map[string]error{"s-1": drsBadInput})
	assertAPICode(t, scanDRSTopLevel(t, st, h), "ValidationException")
	steps, actions := len(rowsByNativeID(t, st, TypeDRSRecoveryPlanStep)), len(rowsByNativeID(t, st, TypeDRSLaunchAction))
	if steps != 3 || actions != 3 {
		t.Errorf("step/launch action rows = %d/%d, want 3/3: the configuration phase ran before them", steps, actions)
	}
}
