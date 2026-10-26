package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationautoscaling"
	aastypes "github.com/aws/aws-sdk-go-v2/service/applicationautoscaling/types"
	"github.com/icearp/disco-cli/store"
)

// stubApplicationAutoScaling is a deterministic in-memory replacement for
// applicationAutoScalingAPI used by the scanner tests. Both methods key
// canned responses by ServiceNamespace so per-namespace fan-out is
// exercised.
type stubApplicationAutoScaling struct {
	targetsByNS  map[aastypes.ServiceNamespace]*applicationautoscaling.DescribeScalableTargetsOutput
	policiesByNS map[aastypes.ServiceNamespace]*applicationautoscaling.DescribeScalingPoliciesOutput
}

func (s *stubApplicationAutoScaling) DescribeScalableTargets(_ context.Context, in *applicationautoscaling.DescribeScalableTargetsInput, _ ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalableTargetsOutput, error) {
	if out, ok := s.targetsByNS[in.ServiceNamespace]; ok {
		return out, nil
	}
	return &applicationautoscaling.DescribeScalableTargetsOutput{}, nil
}

func (s *stubApplicationAutoScaling) DescribeScalingPolicies(_ context.Context, in *applicationautoscaling.DescribeScalingPoliciesInput, _ ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScalingPoliciesOutput, error) {
	if out, ok := s.policiesByNS[in.ServiceNamespace]; ok {
		return out, nil
	}
	return &applicationautoscaling.DescribeScalingPoliciesOutput{}, nil
}

func (s *stubApplicationAutoScaling) DescribeScheduledActions(context.Context, *applicationautoscaling.DescribeScheduledActionsInput, ...func(*applicationautoscaling.Options)) (*applicationautoscaling.DescribeScheduledActionsOutput, error) {
	return &applicationautoscaling.DescribeScheduledActionsOutput{}, nil
}

func TestApplicationAutoScalingScalableTargetNativeID(t *testing.T) {
	got := applicationAutoScalingScalableTargetNativeID("aws", "us-east-1", "111111111111", "ecs", "service/cluster/svc", "ecs:service:DesiredCount")
	want := "arn:aws:application-autoscaling:us-east-1:111111111111:scalable-target/ecs/service/cluster/svc/ecs:service:DesiredCount"
	if got != want {
		t.Errorf("native id = %q, want %q", got, want)
	}
}

func TestScanApplicationAutoScalingScalableTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	rid := "service/cluster/svc"
	dim := aastypes.ScalableDimension("ecs:service:DesiredCount")
	stub := &stubApplicationAutoScaling{
		targetsByNS: map[aastypes.ServiceNamespace]*applicationautoscaling.DescribeScalableTargetsOutput{
			aastypes.ServiceNamespaceEcs: {ScalableTargets: []aastypes.ScalableTarget{{
				ResourceId:        &rid,
				ScalableDimension: dim,
			}}},
		},
	}
	total, inserted, _, err := scanApplicationAutoScalingScalableTargets(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanApplicationAutoScalingScalableTargets: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	want := applicationAutoScalingScalableTargetNativeID("aws", region, acct.ID, "ecs", rid, string(dim))
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationAutoScalingScalableTarget}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != want {
		t.Errorf("rows=%+v, want one row with NativeID %s", rows, want)
	}
}

func TestScanApplicationAutoScalingScalingPolicies(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	arn := "arn:aws:autoscaling:us-east-1:123456789012:scalingPolicy:abc:resource/ecs/service/cluster/svc:policyName/foo"
	name := "foo"
	stub := &stubApplicationAutoScaling{
		policiesByNS: map[aastypes.ServiceNamespace]*applicationautoscaling.DescribeScalingPoliciesOutput{
			aastypes.ServiceNamespaceEcs: {ScalingPolicies: []aastypes.ScalingPolicy{{
				PolicyARN:  &arn,
				PolicyName: &name,
			}}},
		},
	}
	total, inserted, err := scanApplicationAutoScalingScalingPolicies(context.Background(), stub, acct, region, st, testScanID, nil)
	if err != nil {
		t.Fatalf("scanApplicationAutoScalingScalingPolicies: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationAutoScalingScalingPolicy}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != arn {
		t.Errorf("rows=%+v, want one row with NativeID %s", rows, arn)
	}
}

const (
	aasTestActionWeb   = "arn:aws:autoscaling:us-east-1:123456789012:scheduledAction:11111111-2222-3333-4444-555555555555:resource/ecs/service/cluster/web:scheduledActionName/web-night"
	aasTestActionAPI   = "arn:aws:autoscaling:us-east-1:123456789012:scheduledAction:66666666-7777-8888-9999-000000000000:resource/ecs/service/cluster/api:scheduledActionName/api-night"
	aasTestActionTable = "arn:aws:autoscaling:us-east-1:123456789012:scheduledAction:aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee:resource/dynamodb/table/orders:scheduledActionName/orders-peak"
)

var aasTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: application-autoscaling:DescribeScheduledActions")

// aasOpNS keys aasCallLog.perNS: one SDK op in one namespace.
type aasOpNS struct {
	op string
	ns aastypes.ServiceNamespace
}

// aasCallLog records each SDK op answered, in order, the number of calls per
// op and namespace, and the namespace and MaxResults of each
// DescribeScheduledActions call.
type aasCallLog struct {
	ops         []string
	perNS       map[aasOpNS]int
	scheduledNS []aastypes.ServiceNamespace
	maxResults  []int32
}

func (l *aasCallLog) record(op string, ns aastypes.ServiceNamespace) {
	l.ops = append(l.ops, op)
	if l.perNS == nil {
		l.perNS = map[aasOpNS]int{}
	}
	l.perNS[aasOpNS{op, ns}]++
}

func (l *aasCallLog) scheduled(req *applicationautoscaling.DescribeScheduledActionsInput) {
	l.record("DescribeScheduledActions", req.ServiceNamespace)
	l.scheduledNS = append(l.scheduledNS, req.ServiceNamespace)
	l.maxResults = append(l.maxResults, sdkaws.ToInt32(req.MaxResults))
}

// aasHandlers answers every op the service scan makes: one ECS and one
// DynamoDB scalable target, one ECS scaling policy, two pages of ECS
// scheduled actions (the second also carrying an element without an ARN) and
// one page of DynamoDB scheduled actions. Every other namespace has none.
func aasHandlers(calls *aasCallLog) map[string]func(in any) (any, error) {
	rid := "service/cluster/web"
	tableRID := "table/orders"
	policyARN := "arn:aws:autoscaling:us-east-1:123456789012:scalingPolicy:abc:resource/ecs/service/cluster/web:policyName/cpu"
	created := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	return map[string]func(in any) (any, error){
		"DescribeScalableTargets": func(in any) (any, error) {
			ns := in.(*applicationautoscaling.DescribeScalableTargetsInput).ServiceNamespace
			calls.record("DescribeScalableTargets", ns)
			switch ns {
			case aastypes.ServiceNamespaceEcs:
				return &applicationautoscaling.DescribeScalableTargetsOutput{ScalableTargets: []aastypes.ScalableTarget{{
					ResourceId: &rid, ScalableDimension: aastypes.ScalableDimensionECSServiceDesiredCount,
				}}}, nil
			case aastypes.ServiceNamespaceDynamodb:
				return &applicationautoscaling.DescribeScalableTargetsOutput{ScalableTargets: []aastypes.ScalableTarget{{
					ResourceId: &tableRID, ScalableDimension: aastypes.ScalableDimensionDynamoDBTableWriteCapacityUnits,
				}}}, nil
			}
			return &applicationautoscaling.DescribeScalableTargetsOutput{}, nil
		},
		"DescribeScalingPolicies": func(in any) (any, error) {
			ns := in.(*applicationautoscaling.DescribeScalingPoliciesInput).ServiceNamespace
			calls.record("DescribeScalingPolicies", ns)
			if ns != aastypes.ServiceNamespaceEcs {
				return &applicationautoscaling.DescribeScalingPoliciesOutput{}, nil
			}
			return &applicationautoscaling.DescribeScalingPoliciesOutput{ScalingPolicies: []aastypes.ScalingPolicy{{PolicyARN: &policyARN, PolicyName: ptrStr("cpu")}}}, nil
		},
		"DescribeScheduledActions": func(in any) (any, error) {
			req := in.(*applicationautoscaling.DescribeScheduledActionsInput)
			calls.scheduled(req)
			switch {
			case req.ServiceNamespace == aastypes.ServiceNamespaceEcs && req.NextToken == nil:
				return &applicationautoscaling.DescribeScheduledActionsOutput{
					ScheduledActions: []aastypes.ScheduledAction{{
						ScheduledActionARN:   ptrStr(aasTestActionWeb),
						ScheduledActionName:  ptrStr("web-night"),
						ServiceNamespace:     aastypes.ServiceNamespaceEcs,
						ResourceId:           &rid,
						ScalableDimension:    aastypes.ScalableDimensionECSServiceDesiredCount,
						Schedule:             ptrStr("cron(0 22 * * ? *)"),
						CreationTime:         &created,
						ScalableTargetAction: &aastypes.ScalableTargetAction{MinCapacity: sdkaws.Int32(1)},
					}},
					NextToken: ptrStr("page2"),
				}, nil
			case req.ServiceNamespace == aastypes.ServiceNamespaceEcs:
				return &applicationautoscaling.DescribeScheduledActionsOutput{ScheduledActions: []aastypes.ScheduledAction{
					{ScheduledActionARN: ptrStr(aasTestActionAPI), ScheduledActionName: ptrStr("api-night")},
					{ScheduledActionName: ptrStr("no-arn")},
				}}, nil
			case req.ServiceNamespace == aastypes.ServiceNamespaceDynamodb:
				return &applicationautoscaling.DescribeScheduledActionsOutput{ScheduledActions: []aastypes.ScheduledAction{
					{ScheduledActionARN: ptrStr(aasTestActionTable), ScheduledActionName: ptrStr("orders-peak")},
				}}, nil
			}
			return &applicationautoscaling.DescribeScheduledActionsOutput{}, nil
		},
	}
}

func scanApplicationAutoScalingWith(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, aasTestDenied)
	total, _, err := scanApplicationAutoScaling(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanApplicationAutoScaling_ScheduledActions(t *testing.T) {
	st := newTestStore(t)
	var calls aasCallLog
	total, err := scanApplicationAutoScalingWith(t, st, aasHandlers(&calls))
	if err != nil {
		t.Fatalf("scanApplicationAutoScaling: %v", err)
	}
	// 2 targets + 1 policy + 3 scheduled actions
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	rows := rowsByNativeID(t, st, TypeApplicationAutoScalingScheduledAction)
	if len(rows) != 3 {
		t.Fatalf("scheduled action rows = %d, want 3: %v", len(rows), rows)
	}
	for _, arn := range []string{aasTestActionWeb, aasTestActionAPI, aasTestActionTable} {
		if _, ok := rows[arn]; !ok {
			t.Errorf("scheduled action %s missing", arn)
		}
	}
	web := rows[aasTestActionWeb]
	if web.Type != TypeApplicationAutoScalingScheduledAction || sv(web.Region) != testRegion || sv(web.Name) != "web-night" {
		t.Errorf("web row type=%q region=%q name=%q", web.Type, sv(web.Region), sv(web.Name))
	}
	if got := sv(web.CreatedAt); got != "2026-03-01T12:00:00Z" {
		t.Errorf("web CreatedAt = %q, want 2026-03-01T12:00:00Z", got)
	}
	var attrs struct {
		Schedule             string
		ScalableTargetAction struct{ MinCapacity int32 }
	}
	if err := json.Unmarshal([]byte(web.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode web attrs: %v", err)
	}
	if attrs.Schedule != "cron(0 22 * * ? *)" || attrs.ScalableTargetAction.MinCapacity != 1 {
		t.Errorf("web attrs = %+v", attrs)
	}
	for i, m := range calls.maxResults {
		if m != 50 {
			t.Errorf("call %d MaxResults = %d, want 50", i, m)
		}
	}
}

func TestScanApplicationAutoScaling_ScheduledActionsRunAfterOlderPhases(t *testing.T) {
	st := newTestStore(t)
	var calls aasCallLog
	if _, err := scanApplicationAutoScalingWith(t, st, aasHandlers(&calls)); err != nil {
		t.Fatalf("scanApplicationAutoScaling: %v", err)
	}
	first := slices.Index(calls.ops, "DescribeScheduledActions")
	if first < 0 {
		t.Fatal("DescribeScheduledActions never called")
	}
	for _, op := range calls.ops[first:] {
		if op != "DescribeScheduledActions" {
			t.Errorf("%s called after the scheduled action phase started: %v", op, calls.ops)
		}
	}
}

func TestScanApplicationAutoScaling_ScheduledActionErrors(t *testing.T) {
	ecsOnly := func(err error) func(*applicationautoscaling.DescribeScheduledActionsInput) error {
		return func(req *applicationautoscaling.DescribeScheduledActionsInput) error {
			if req.ServiceNamespace == aastypes.ServiceNamespaceEcs {
				return err
			}
			return nil
		}
	}
	every := func(err error) func(*applicationautoscaling.DescribeScheduledActionsInput) error {
		return func(*applicationautoscaling.DescribeScheduledActionsInput) error { return err }
	}
	for _, tc := range []struct {
		name      string
		empty     bool
		fail      func(*applicationautoscaling.DescribeScheduledActionsInput) error
		wantCode  string
		wantRows  []string
		wantWarns int
		wantCalls int
	}{
		{
			name:      "empty in every namespace",
			empty:     true,
			wantCalls: 2,
		},
		{
			name:      "denied ends the phase with one warning",
			fail:      every(aasTestDenied),
			wantWarns: 1, wantCalls: 1,
		},
		{
			name:     "namespace outside the region's enum set is skipped silently",
			fail:     ecsOnly(apiErr("ValidationException", "1 validation error detected: Value 'ecs' at 'serviceNamespace' failed to satisfy constraint: Member must satisfy enum value set: [dynamodb]")),
			wantRows: []string{aasTestActionTable}, wantCalls: 2,
		},
		{
			name:     "other ValidationException is returned",
			fail:     ecsOnly(apiErr("ValidationException", "bad request")),
			wantCode: "ValidationException", wantCalls: 1,
		},
		{
			name: "error on a later page keeps the earlier page",
			fail: func(req *applicationautoscaling.DescribeScheduledActionsInput) error {
				if req.NextToken != nil {
					return apiErr("InternalServiceException", "boom")
				}
				return nil
			},
			wantCode: "InternalServiceException", wantRows: []string{aasTestActionWeb}, wantCalls: 2,
		},
		{
			// Alone, the transient error is returned, so the scan is partial.
			name:     "transient error alone is returned",
			fail:     every(apiErr("ServiceUnavailable", "try later")),
			wantCode: "ServiceUnavailable", wantCalls: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "application-autoscaling:DescribeScheduledActions")
			var calls aasCallLog
			h := aasHandlers(&calls)
			next := h["DescribeScheduledActions"]
			h["DescribeScheduledActions"] = func(in any) (any, error) {
				req := in.(*applicationautoscaling.DescribeScheduledActionsInput)
				if tc.fail != nil {
					if err := tc.fail(req); err != nil {
						calls.scheduled(req)
						return nil, err
					}
				}
				if tc.empty {
					calls.scheduled(req)
					return &applicationautoscaling.DescribeScheduledActionsOutput{}, nil
				}
				return next(in)
			}

			total, err := scanApplicationAutoScalingWith(t, st, h)
			switch {
			case tc.wantCode == "" && err != nil:
				t.Fatalf("scanApplicationAutoScaling: %v, want nil", err)
			case tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode):
				t.Fatalf("scanApplicationAutoScaling err = %v, want %s", err, tc.wantCode)
			}
			rows := rowsByNativeID(t, st, TypeApplicationAutoScalingScheduledAction)
			if len(rows) != len(tc.wantRows) {
				t.Errorf("scheduled action rows = %d, want %d: %v", len(rows), len(tc.wantRows), rows)
			}
			for _, arn := range tc.wantRows {
				if _, ok := rows[arn]; !ok {
					t.Errorf("scheduled action %s missing", arn)
				}
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if got := len(calls.scheduledNS); got != tc.wantCalls {
				t.Errorf("DescribeScheduledActions calls = %d, want %d", got, tc.wantCalls)
			}
			// The older phases' targets and policy are kept.
			if want := 3 + len(tc.wantRows); total != want {
				t.Errorf("total = %d, want %d", total, want)
			}
			if len(rowsByNativeID(t, st, TypeApplicationAutoScalingScalingPolicy)) != 1 {
				t.Error("scaling policy row missing")
			}
		})
	}
}

func TestScanApplicationAutoScaling_OlderPhaseErrorStillScansScheduledActions(t *testing.T) {
	st := newTestStore(t)
	var calls aasCallLog
	h := aasHandlers(&calls)
	h["DescribeScalingPolicies"] = func(any) (any, error) { return nil, apiErr("ValidationException", "boom") }

	total, err := scanApplicationAutoScalingWith(t, st, h)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("scanApplicationAutoScaling err = %v, want the policies phase's ValidationException", err)
	}
	// 2 targets + 3 scheduled actions
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if got := len(rowsByNativeID(t, st, TypeApplicationAutoScalingScheduledAction)); got != 3 {
		t.Errorf("scheduled action rows = %d, want 3", got)
	}
}

func TestScanApplicationAutoScaling_OlderTransientDoesNotMaskScheduledActionHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "application-autoscaling")
	var calls aasCallLog
	h := aasHandlers(&calls)
	h["DescribeScalingPolicies"] = func(any) (any, error) { return nil, apiErr("ServiceUnavailable", "try later") }
	h["DescribeScheduledActions"] = func(any) (any, error) { return nil, apiErr("ValidationException", "bad") }

	_, err := scanApplicationAutoScalingWith(t, st, h)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("application-autoscaling warnings = %d, want 1 for the demoted transient", *warns)
	}
}

// assertAASCallsPerNamespace checks the DescribeScalingPolicies and
// DescribeScheduledActions call count of every namespace the scanner knows,
// so a namespace missing from want must have no call at all.
func assertAASCallsPerNamespace(t *testing.T, calls *aasCallLog, wantPolicies, wantScheduled map[aastypes.ServiceNamespace]int) {
	t.Helper()
	for _, ns := range applicationAutoScalingNamespaces {
		if got := calls.perNS[aasOpNS{"DescribeScalingPolicies", ns}]; got != wantPolicies[ns] {
			t.Errorf("DescribeScalingPolicies calls for %s = %d, want %d", ns, got, wantPolicies[ns])
		}
		if got := calls.perNS[aasOpNS{"DescribeScheduledActions", ns}]; got != wantScheduled[ns] {
			t.Errorf("DescribeScheduledActions calls for %s = %d, want %d", ns, got, wantScheduled[ns])
		}
	}
}

func TestScanApplicationAutoScaling_PoliciesAndActionsOnlyWhereTargets(t *testing.T) {
	st := newTestStore(t)
	var calls aasCallLog
	if _, err := scanApplicationAutoScalingWith(t, st, aasHandlers(&calls)); err != nil {
		t.Fatalf("scanApplicationAutoScaling: %v", err)
	}
	for _, ns := range applicationAutoScalingNamespaces {
		if got := calls.perNS[aasOpNS{"DescribeScalableTargets", ns}]; got != 1 {
			t.Errorf("DescribeScalableTargets calls for %s = %d, want 1", ns, got)
		}
	}
	// ECS and DynamoDB hold targets; ECS scheduled actions span two pages.
	assertAASCallsPerNamespace(t, &calls,
		map[aastypes.ServiceNamespace]int{aastypes.ServiceNamespaceEcs: 1, aastypes.ServiceNamespaceDynamodb: 1},
		map[aastypes.ServiceNamespace]int{aastypes.ServiceNamespaceEcs: 2, aastypes.ServiceNamespaceDynamodb: 1},
	)
}

func TestScanApplicationAutoScaling_FailedTargetListingStillQueriesNamespace(t *testing.T) {
	ecsOnly := func(err error) func(aastypes.ServiceNamespace) error {
		return func(ns aastypes.ServiceNamespace) error {
			if ns == aastypes.ServiceNamespaceEcs {
				return err
			}
			return nil
		}
	}
	allNS := func(n int) map[aastypes.ServiceNamespace]int {
		m := map[aastypes.ServiceNamespace]int{}
		for _, ns := range applicationAutoScalingNamespaces {
			m[ns] = n
		}
		return m
	}
	everyScheduled := allNS(1)
	everyScheduled[aastypes.ServiceNamespaceEcs] = 2
	for _, tc := range []struct {
		name           string
		fail           func(aastypes.ServiceNamespace) error
		wantCode       string
		wantWarns      int
		wantPolicies   map[aastypes.ServiceNamespace]int
		wantScheduled  map[aastypes.ServiceNamespace]int
		wantPolicyRows int
	}{
		{
			name:           "denied",
			fail:           ecsOnly(apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: application-autoscaling:DescribeScalableTargets")),
			wantWarns:      1,
			wantPolicies:   map[aastypes.ServiceNamespace]int{aastypes.ServiceNamespaceEcs: 1, aastypes.ServiceNamespaceDynamodb: 1},
			wantScheduled:  map[aastypes.ServiceNamespace]int{aastypes.ServiceNamespaceEcs: 2, aastypes.ServiceNamespaceDynamodb: 1},
			wantPolicyRows: 1,
		},
		{
			name:           "outside the region's enum set",
			fail:           ecsOnly(apiErr("ValidationException", "1 validation error detected: Value 'ecs' at 'serviceNamespace' failed to satisfy constraint: Member must satisfy enum value set: [dynamodb]")),
			wantPolicies:   map[aastypes.ServiceNamespace]int{aastypes.ServiceNamespaceEcs: 1, aastypes.ServiceNamespaceDynamodb: 1},
			wantScheduled:  map[aastypes.ServiceNamespace]int{aastypes.ServiceNamespaceEcs: 2, aastypes.ServiceNamespaceDynamodb: 1},
			wantPolicyRows: 1,
		},
		{
			// The hard error stops the policy phase as before; no namespace
			// was proven empty, so every one still gets its scheduled actions.
			name:          "hard error",
			fail:          func(aastypes.ServiceNamespace) error { return apiErr("InternalServiceException", "boom") },
			wantCode:      "InternalServiceException",
			wantScheduled: everyScheduled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "application-autoscaling:DescribeScalableTargets")
			var calls aasCallLog
			h := aasHandlers(&calls)
			next := h["DescribeScalableTargets"]
			h["DescribeScalableTargets"] = func(in any) (any, error) {
				ns := in.(*applicationautoscaling.DescribeScalableTargetsInput).ServiceNamespace
				if err := tc.fail(ns); err != nil {
					calls.record("DescribeScalableTargets", ns)
					return nil, err
				}
				return next(in)
			}

			_, err := scanApplicationAutoScalingWith(t, st, h)
			switch {
			case tc.wantCode == "" && err != nil:
				t.Fatalf("scanApplicationAutoScaling: %v, want nil", err)
			case tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode):
				t.Fatalf("scanApplicationAutoScaling err = %v, want %s", err, tc.wantCode)
			}
			assertAASCallsPerNamespace(t, &calls, tc.wantPolicies, tc.wantScheduled)
			if *warns != tc.wantWarns {
				t.Errorf("DescribeScalableTargets warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if got := len(rowsByNativeID(t, st, TypeApplicationAutoScalingScalingPolicy)); got != tc.wantPolicyRows {
				t.Errorf("scaling policy rows = %d, want %d", got, tc.wantPolicyRows)
			}
			if got := len(rowsByNativeID(t, st, TypeApplicationAutoScalingScheduledAction)); got != 3 {
				t.Errorf("scheduled action rows = %d, want 3", got)
			}
		})
	}
}

func TestScanApplicationAutoScaling_NamespaceWithAnyListedTargetIsQueried(t *testing.T) {
	rid := "service/cluster/web"
	for _, tc := range []struct {
		name  string
		pages []applicationautoscaling.DescribeScalableTargetsOutput
	}{
		{
			name: "target on the first of two pages",
			pages: []applicationautoscaling.DescribeScalableTargetsOutput{
				{ScalableTargets: []aastypes.ScalableTarget{{ResourceId: &rid, ScalableDimension: aastypes.ScalableDimensionECSServiceDesiredCount}}, NextToken: ptrStr("page2")},
				{},
			},
		},
		{
			// A target the scanner cannot store still exists, so its
			// policies and scheduled actions may too.
			name: "target without a resource id",
			pages: []applicationautoscaling.DescribeScalableTargetsOutput{
				{ScalableTargets: []aastypes.ScalableTarget{{ScalableDimension: aastypes.ScalableDimensionECSServiceDesiredCount}}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls aasCallLog
			h := aasHandlers(&calls)
			next := h["DescribeScalableTargets"]
			h["DescribeScalableTargets"] = func(in any) (any, error) {
				req := in.(*applicationautoscaling.DescribeScalableTargetsInput)
				if req.ServiceNamespace != aastypes.ServiceNamespaceEcs {
					return next(in)
				}
				calls.record("DescribeScalableTargets", req.ServiceNamespace)
				page := tc.pages[0]
				if req.NextToken != nil {
					page = tc.pages[1]
				}
				return &page, nil
			}

			if _, err := scanApplicationAutoScalingWith(t, st, h); err != nil {
				t.Fatalf("scanApplicationAutoScaling: %v", err)
			}
			assertAASCallsPerNamespace(t, &calls,
				map[aastypes.ServiceNamespace]int{aastypes.ServiceNamespaceEcs: 1, aastypes.ServiceNamespaceDynamodb: 1},
				map[aastypes.ServiceNamespace]int{aastypes.ServiceNamespaceEcs: 2, aastypes.ServiceNamespaceDynamodb: 1},
			)
		})
	}
}

func TestScanApplicationAutoScaling_RepeatedTargetTokenFailsAndKeepsNamespace(t *testing.T) {
	st := newTestStore(t)
	var calls aasCallLog
	var maxResults []int32
	h := aasHandlers(&calls)
	next := h["DescribeScalableTargets"]
	h["DescribeScalableTargets"] = func(in any) (any, error) {
		req := in.(*applicationautoscaling.DescribeScalableTargetsInput)
		maxResults = append(maxResults, sdkaws.ToInt32(req.MaxResults))
		if req.ServiceNamespace != aastypes.ServiceNamespaceEcs {
			return next(in)
		}
		calls.record("DescribeScalableTargets", req.ServiceNamespace)
		if n := calls.perNS[aasOpNS{"DescribeScalableTargets", req.ServiceNamespace}]; n > 3 {
			t.Fatalf("ECS DescribeScalableTargets called %d times: the repeated token did not end the loop", n)
		}
		// Empty pages: only the repeated token, not a listed target, may keep
		// ECS out of the empty set.
		return &applicationautoscaling.DescribeScalableTargetsOutput{NextToken: ptrStr("same")}, nil
	}

	_, err := scanApplicationAutoScalingWith(t, st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("scanApplicationAutoScaling err = %v, want errRepeatedNextToken", err)
	}
	if got := calls.perNS[aasOpNS{"DescribeScalableTargets", aastypes.ServiceNamespaceEcs}]; got != 2 {
		t.Errorf("ECS DescribeScalableTargets calls = %d, want 2 (the first page, then the repeated token)", got)
	}
	for i, m := range maxResults {
		if m != 50 {
			t.Errorf("DescribeScalableTargets call %d MaxResults = %d, want 50", i, m)
		}
	}
	// The target error stops the policy phase; the scheduled action phase
	// still queries ECS (two pages).
	if got := calls.perNS[aasOpNS{"DescribeScheduledActions", aastypes.ServiceNamespaceEcs}]; got != 2 {
		t.Errorf("ECS DescribeScheduledActions calls = %d, want 2", got)
	}
	if got := calls.perNS[aasOpNS{"DescribeScalingPolicies", aastypes.ServiceNamespaceEcs}]; got != 0 {
		t.Errorf("ECS DescribeScalingPolicies calls = %d, want 0 after the target phase failed", got)
	}
}
