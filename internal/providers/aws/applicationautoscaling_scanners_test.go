package aws

import (
	"context"
	"encoding/json"
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
	got := applicationAutoScalingScalableTargetNativeID("us-east-1", "111111111111", "ecs", "service/cluster/svc", "ecs:service:DesiredCount")
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
	total, inserted, err := scanApplicationAutoScalingScalableTargets(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanApplicationAutoScalingScalableTargets: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	want := applicationAutoScalingScalableTargetNativeID(region, acct.ID, "ecs", rid, string(dim))
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
	total, inserted, err := scanApplicationAutoScalingScalingPolicies(context.Background(), stub, acct, region, st, testScanID)
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

// aasCallLog records each SDK op answered, in order, with the namespace and
// MaxResults of each DescribeScheduledActions call.
type aasCallLog struct {
	ops         []string
	scheduledNS []aastypes.ServiceNamespace
	maxResults  []int32
}

func (l *aasCallLog) scheduled(req *applicationautoscaling.DescribeScheduledActionsInput) {
	l.ops = append(l.ops, "DescribeScheduledActions")
	l.scheduledNS = append(l.scheduledNS, req.ServiceNamespace)
	l.maxResults = append(l.maxResults, sdkaws.ToInt32(req.MaxResults))
}

// aasHandlers answers every op the service scan makes: one ECS scalable
// target and one ECS scaling policy, two pages of ECS scheduled actions (the
// second also carrying an element without an ARN) and one page of DynamoDB
// scheduled actions. Every other namespace has none.
func aasHandlers(calls *aasCallLog) map[string]func(in any) (any, error) {
	rid := "service/cluster/web"
	policyARN := "arn:aws:autoscaling:us-east-1:123456789012:scalingPolicy:abc:resource/ecs/service/cluster/web:policyName/cpu"
	created := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	return map[string]func(in any) (any, error){
		"DescribeScalableTargets": func(in any) (any, error) {
			calls.ops = append(calls.ops, "DescribeScalableTargets")
			if in.(*applicationautoscaling.DescribeScalableTargetsInput).ServiceNamespace != aastypes.ServiceNamespaceEcs {
				return &applicationautoscaling.DescribeScalableTargetsOutput{}, nil
			}
			return &applicationautoscaling.DescribeScalableTargetsOutput{ScalableTargets: []aastypes.ScalableTarget{{
				ResourceId: &rid, ScalableDimension: aastypes.ScalableDimensionECSServiceDesiredCount,
			}}}, nil
		},
		"DescribeScalingPolicies": func(in any) (any, error) {
			calls.ops = append(calls.ops, "DescribeScalingPolicies")
			if in.(*applicationautoscaling.DescribeScalingPoliciesInput).ServiceNamespace != aastypes.ServiceNamespaceEcs {
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
	// 1 target + 1 policy + 3 scheduled actions
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
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
	if got, want := len(calls.scheduledNS), len(applicationAutoScalingNamespaces)+1; got != want {
		t.Errorf("DescribeScheduledActions calls = %d, want %d (every namespace plus ECS page 2)", got, want)
	}
	for _, ns := range applicationAutoScalingNamespaces {
		if !slices.Contains(calls.scheduledNS, ns) {
			t.Errorf("namespace %s never queried", ns)
		}
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
			wantCalls: 15,
		},
		{
			name:      "denied ends the phase with one warning",
			fail:      every(aasTestDenied),
			wantWarns: 1, wantCalls: 1,
		},
		{
			name:     "namespace outside the region's enum set is skipped silently",
			fail:     ecsOnly(apiErr("ValidationException", "1 validation error detected: Value 'ecs' at 'serviceNamespace' failed to satisfy constraint: Member must satisfy enum value set: [dynamodb]")),
			wantRows: []string{aasTestActionTable}, wantCalls: 15,
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
			name:      "transient error is a warning",
			fail:      every(apiErr("ServiceUnavailable", "try later")),
			wantWarns: 1, wantCalls: 1,
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
			// The older phases' target and policy are kept.
			if want := 2 + len(tc.wantRows); total != want {
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
	// 1 target + 3 scheduled actions
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
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
