package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/icearp/disco-cli/store"
)

const (
	asTestGroupARN = "arn:aws:autoscaling:us-east-1:123456789012:autoScalingGroup:11111111-2222-3333-4444-555555555555:autoScalingGroupName/web"
	asTestLCARN    = "arn:aws:autoscaling:us-east-1:123456789012:launchConfiguration:66666666-7777-8888-9999-000000000000:launchConfigurationName/web-lc"
	asTestTopicA   = "arn:aws:sns:us-east-1:123456789012:asg-events"
	asTestTopicB   = "arn:aws:sns:us-east-1:123456789012:other-events"

	asTestLaunch    = "autoscaling:EC2_INSTANCE_LAUNCH"
	asTestTerminate = "autoscaling:EC2_INSTANCE_TERMINATE"

	asTestNotifyWebA    = "arn:aws:autoscaling:us-east-1:123456789012:notification-configuration/web/" + asTestTopicA
	asTestNotifyOrphanB = "arn:aws:autoscaling:us-east-1:123456789012:notification-configuration/gone/" + asTestTopicB

	asTestHookWebDrain = "arn:aws:autoscaling:us-east-1:123456789012:lifecycle-hook/web/drain"
	asTestWarmPoolWeb  = "arn:aws:autoscaling:us-east-1:123456789012:warm-pool/web"
)

var asTestDenied = apiErr("AccessDenied", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: autoscaling:DescribeNotificationConfigurations")

func asNotify(group, topic, kind string) astypes.NotificationConfiguration {
	return astypes.NotificationConfiguration{AutoScalingGroupName: ptrStr(group), TopicARN: ptrStr(topic), NotificationType: ptrStr(kind)}
}

// asCallLog records the order of SDK ops answered. Handlers run on fan-out
// goroutines, so it is locked.
type asCallLog struct {
	mu  sync.Mutex
	ops []string
}

func (l *asCallLog) add(op string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops = append(l.ops, op)
}

// asHandlers answers every op the service scan makes: one group "web" with a
// lifecycle hook and a warm pool, one launch configuration, and two pages of
// notification configurations. The web group's topic-A pair is split across
// the pages with its types out of order and one type repeated; group "gone"
// was not listed by the group phase; one element has no topic.
func asHandlers(calls *asCallLog) map[string]func(in any) (any, error) {
	logged := func(op string, h func(in any) (any, error)) func(in any) (any, error) {
		return func(in any) (any, error) {
			calls.add(op)
			return h(in)
		}
	}
	return map[string]func(in any) (any, error){
		"DescribeAutoScalingGroups": logged("DescribeAutoScalingGroups", func(any) (any, error) {
			return &autoscaling.DescribeAutoScalingGroupsOutput{AutoScalingGroups: []astypes.AutoScalingGroup{{
				AutoScalingGroupName: ptrStr("web"), AutoScalingGroupARN: ptrStr(asTestGroupARN),
			}}}, nil
		}),
		"DescribeLaunchConfigurations": logged("DescribeLaunchConfigurations", func(any) (any, error) {
			return &autoscaling.DescribeLaunchConfigurationsOutput{LaunchConfigurations: []astypes.LaunchConfiguration{{
				LaunchConfigurationName: ptrStr("web-lc"), LaunchConfigurationARN: ptrStr(asTestLCARN),
			}}}, nil
		}),
		"DescribePolicies": logged("DescribePolicies", func(any) (any, error) {
			return &autoscaling.DescribePoliciesOutput{}, nil
		}),
		"DescribeScheduledActions": logged("DescribeScheduledActions", func(any) (any, error) {
			return &autoscaling.DescribeScheduledActionsOutput{}, nil
		}),
		"DescribeLifecycleHooks": logged("DescribeLifecycleHooks", func(in any) (any, error) {
			if sv(in.(*autoscaling.DescribeLifecycleHooksInput).AutoScalingGroupName) != "web" {
				return &autoscaling.DescribeLifecycleHooksOutput{}, nil
			}
			return &autoscaling.DescribeLifecycleHooksOutput{LifecycleHooks: []astypes.LifecycleHook{{
				AutoScalingGroupName: ptrStr("web"), LifecycleHookName: ptrStr("drain"),
			}}}, nil
		}),
		"DescribeWarmPool": logged("DescribeWarmPool", func(in any) (any, error) {
			if sv(in.(*autoscaling.DescribeWarmPoolInput).AutoScalingGroupName) != "web" {
				return &autoscaling.DescribeWarmPoolOutput{}, nil
			}
			return &autoscaling.DescribeWarmPoolOutput{WarmPoolConfiguration: &astypes.WarmPoolConfiguration{MinSize: ptrInt32(1)}}, nil
		}),
		"DescribeNotificationConfigurations": logged("DescribeNotificationConfigurations", func(in any) (any, error) {
			if in.(*autoscaling.DescribeNotificationConfigurationsInput).NextToken == nil {
				return &autoscaling.DescribeNotificationConfigurationsOutput{
					NextToken: ptrStr("p2"),
					NotificationConfigurations: []astypes.NotificationConfiguration{
						asNotify("web", asTestTopicA, asTestTerminate),
					},
				}, nil
			}
			return &autoscaling.DescribeNotificationConfigurationsOutput{NotificationConfigurations: []astypes.NotificationConfiguration{
				asNotify("web", asTestTopicA, asTestLaunch),
				asNotify("web", asTestTopicA, asTestTerminate),
				asNotify("gone", asTestTopicB, asTestLaunch),
				{AutoScalingGroupName: ptrStr("web"), NotificationType: ptrStr(asTestLaunch)},
			}}, nil
		}),
	}
}

func scanAutoScalingWith(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, asTestDenied)
	total, _, err := scanAutoScaling(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func asNotifyAttrs(t *testing.T, r store.Resource) autoScalingNotificationConfiguration {
	t.Helper()
	var a autoScalingNotificationConfiguration
	if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
		t.Fatalf("decode %s attrs: %v", r.NativeID, err)
	}
	return a
}

func TestScanAutoScaling_StoresNotificationConfigurationPerGroupTopic(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	var calls asCallLog
	h := asHandlers(&calls)
	var maxRecords []int32
	next := h["DescribeNotificationConfigurations"]
	h["DescribeNotificationConfigurations"] = func(in any) (any, error) {
		maxRecords = append(maxRecords, *in.(*autoscaling.DescribeNotificationConfigurationsInput).MaxRecords)
		return next(in)
	}

	total, err := scanAutoScalingWith(t, st, h)
	if err != nil {
		t.Fatalf("scanAutoScaling: %v", err)
	}
	// 1 group + 1 launch configuration + 1 lifecycle hook + 1 warm pool + 2 notification configurations
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	// The per-group fan-outs are driven by the groups the group phase listed.
	if _, ok := rowsByNativeID(t, st, TypeAutoScalingLifecycleHook)[asTestHookWebDrain]; !ok {
		t.Errorf("no lifecycle hook row %s", asTestHookWebDrain)
	}
	if _, ok := rowsByNativeID(t, st, TypeAutoScalingWarmPool)[asTestWarmPoolWeb]; !ok {
		t.Errorf("no warm pool row %s", asTestWarmPoolWeb)
	}
	if !slices.Equal(maxRecords, []int32{100, 100}) {
		t.Errorf("MaxRecords per page = %v, want [100 100]", maxRecords)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}

	rows := rowsByNativeID(t, st, TypeAutoScalingNotificationConfiguration)
	if len(rows) != 2 {
		t.Fatalf("notification configuration rows = %d, want 2: %v", len(rows), rows)
	}
	web, ok := rows[asTestNotifyWebA]
	if !ok {
		t.Fatalf("no row %s; got %v", asTestNotifyWebA, rows)
	}
	if web.Type != TypeAutoScalingNotificationConfiguration {
		t.Errorf("Type = %q, want %q", web.Type, TypeAutoScalingNotificationConfiguration)
	}
	if sv(web.Region) != testRegion {
		t.Errorf("Region = %q, want %q", sv(web.Region), testRegion)
	}
	want := autoScalingNotificationConfiguration{AutoScalingGroupName: "web", TopicARN: asTestTopicA, NotificationTypes: []string{asTestLaunch, asTestTerminate}}
	if got := asNotifyAttrs(t, web); got.AutoScalingGroupName != want.AutoScalingGroupName || got.TopicARN != want.TopicARN || !slices.Equal(got.NotificationTypes, want.NotificationTypes) {
		t.Errorf("attrs = %+v, want %+v", got, want)
	}
	if _, ok := rows[asTestNotifyOrphanB]; !ok {
		t.Errorf("no row %s for a group the group phase did not list", asTestNotifyOrphanB)
	}
	assertContainsNative(t, st, asTestGroupARN, asTestNotifyWebA)

	if last := calls.ops[len(calls.ops)-1]; last != "DescribeNotificationConfigurations" {
		t.Errorf("last op = %s, want DescribeNotificationConfigurations after every older phase; order %v", last, calls.ops)
	}
}

func TestScanAutoScaling_NotificationConfigurationOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		page1Err  error
		page2Err  error
		empty     bool
		wantCode  string // "" means the scan returns nil
		wantRows  int
		wantWarns int
	}{
		{name: "empty", empty: true},
		{name: "denied", page1Err: asTestDenied, wantWarns: 1},
		{name: "denied on a later page stores nothing", page2Err: asTestDenied, wantWarns: 1},
		{name: "other error on a later page", page2Err: apiErr("ValidationException", "bad token"), wantCode: "ValidationException"},
		{name: "transient error is a warning", page1Err: apiErr("ServiceUnavailable", "try later"), wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "autoscaling:DescribeNotificationConfigurations")
			var calls asCallLog
			h := asHandlers(&calls)
			next := h["DescribeNotificationConfigurations"]
			h["DescribeNotificationConfigurations"] = func(in any) (any, error) {
				onPage2 := in.(*autoscaling.DescribeNotificationConfigurationsInput).NextToken != nil
				switch {
				case tc.empty:
					return &autoscaling.DescribeNotificationConfigurationsOutput{}, nil
				case !onPage2 && tc.page1Err != nil:
					return nil, tc.page1Err
				case onPage2 && tc.page2Err != nil:
					return nil, tc.page2Err
				}
				return next(in)
			}

			total, err := scanAutoScalingWith(t, st, h)
			switch {
			case tc.wantCode == "" && err != nil:
				t.Fatalf("scanAutoScaling: %v, want nil", err)
			case tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode):
				t.Fatalf("scanAutoScaling err = %v, want %s", err, tc.wantCode)
			}
			if got := len(rowsByNativeID(t, st, TypeAutoScalingNotificationConfiguration)); got != tc.wantRows {
				t.Errorf("notification configuration rows = %d, want %d", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			// The older phases' group, launch configuration, hook and warm pool are kept.
			if total != 4 {
				t.Errorf("total = %d, want 4", total)
			}
			if _, ok := rowsByNativeID(t, st, TypeAutoScalingLaunchConfiguration)[asTestLCARN]; !ok {
				t.Error("launch configuration row missing")
			}
		})
	}
}

func TestScanAutoScaling_OlderPhaseErrorStillScansNotificationConfigurations(t *testing.T) {
	st := newTestStore(t)
	var calls asCallLog
	h := asHandlers(&calls)
	h["DescribePolicies"] = func(any) (any, error) { return nil, apiErr("ValidationException", "boom") }

	total, err := scanAutoScalingWith(t, st, h)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("scanAutoScaling err = %v, want the policies phase's ValidationException", err)
	}
	// 1 group + 1 launch configuration + 2 notification configurations
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
	if got := len(rowsByNativeID(t, st, TypeAutoScalingNotificationConfiguration)); got != 2 {
		t.Errorf("notification configuration rows = %d, want 2", got)
	}
	assertContainsNative(t, st, asTestGroupARN, asTestNotifyWebA)
}

func TestScanAutoScaling_OlderTransientDoesNotMaskNotificationHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "autoscaling")
	var calls asCallLog
	h := asHandlers(&calls)
	h["DescribePolicies"] = func(any) (any, error) { return nil, apiErr("ServiceUnavailable", "try later") }
	h["DescribeNotificationConfigurations"] = func(any) (any, error) { return nil, apiErr("ValidationException", "bad") }

	_, err := scanAutoScalingWith(t, st, h)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("autoscaling warnings = %d, want 1 for the demoted transient", *warns)
	}
}
