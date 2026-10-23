package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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
		// Alone, the transient error is returned, so the scan is partial.
		{name: "transient error alone is returned", page1Err: apiErr("ServiceUnavailable", "try later"), wantCode: "ServiceUnavailable"},
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

func TestScanAutoScaling_OlderTransientDoesNotMaskNotificationHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "autoscaling:DescribePolicies")
	var calls asCallLog
	h := asHandlers(&calls)
	h["DescribePolicies"] = func(any) (any, error) { return nil, apiErr("ServiceUnavailable", "try later") }
	h["DescribeNotificationConfigurations"] = func(any) (any, error) { return nil, apiErr("ValidationException", "bad") }

	_, err := scanAutoScalingWith(t, st, h)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("autoscaling:DescribePolicies warnings = %d, want 1 for the demoted transient", *warns)
	}
}

const asTestGroupARN2 = "arn:aws:autoscaling:us-east-1:123456789012:autoScalingGroup:22222222-3333-4444-5555-666666666666:autoScalingGroupName/batch"

// asTwoPageGroups answers DescribeAutoScalingGroups with two pages: the
// tagged "web" group and one without a name, then the untagged "batch"
// group, or page2Err. It records each call's input.
func asTwoPageGroups(inputs *[]autoscaling.DescribeAutoScalingGroupsInput, page2Err error) func(in any) (any, error) {
	var mu sync.Mutex
	return func(in any) (any, error) {
		req := in.(*autoscaling.DescribeAutoScalingGroupsInput)
		mu.Lock()
		*inputs = append(*inputs, *req)
		mu.Unlock()
		if req.NextToken == nil {
			return &autoscaling.DescribeAutoScalingGroupsOutput{
				AutoScalingGroups: []astypes.AutoScalingGroup{
					{
						AutoScalingGroupName: ptrStr("web"), AutoScalingGroupARN: ptrStr(asTestGroupARN), MinSize: ptrInt32(1),
						Tags: []astypes.TagDescription{
							{Key: ptrStr("env"), Value: ptrStr("prod"), PropagateAtLaunch: sdkaws.Bool(true), ResourceId: ptrStr("web"), ResourceType: ptrStr("auto-scaling-group")},
							{Key: ptrStr("team"), Value: ptrStr("web")},
						},
					},
					{AutoScalingGroupARN: ptrStr("arn:aws:autoscaling:us-east-1:123456789012:autoScalingGroup:x:autoScalingGroupName/")},
				},
				NextToken: ptrStr("g2"),
			}, nil
		}
		if page2Err != nil {
			return nil, page2Err
		}
		return &autoscaling.DescribeAutoScalingGroupsOutput{AutoScalingGroups: []astypes.AutoScalingGroup{
			{AutoScalingGroupName: ptrStr("batch"), AutoScalingGroupARN: ptrStr(asTestGroupARN2), Status: ptrStr("Delete in progress")},
		}}, nil
	}
}

// Groups store the Tags their element carries, every page is read with the
// page cap and no filter, and a group without a name warns once.
func TestScanAutoScaling_GroupsStoreTags(t *testing.T) {
	st := newTestStore(t)
	groupWarns := countOpWarnings(st, autoScalingGroupsOp)
	var calls asCallLog
	h := asHandlers(&calls)
	var inputs []autoscaling.DescribeAutoScalingGroupsInput
	h["DescribeAutoScalingGroups"] = asTwoPageGroups(&inputs, nil)

	total, err := scanAutoScalingWith(t, st, h)
	if err != nil {
		t.Fatalf("scanAutoScaling: %v", err)
	}
	// 2 groups + 1 launch configuration + 1 lifecycle hook + 1 warm pool + 2 notification configurations
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
	if *groupWarns != 1 {
		t.Errorf("%s warnings = %d, want 1 for the group without a name", autoScalingGroupsOp, *groupWarns)
	}
	if len(inputs) != 2 {
		t.Fatalf("DescribeAutoScalingGroups calls = %d, want 2", len(inputs))
	}
	for i, wantToken := range []string{"", "g2"} {
		in := inputs[i]
		if sv(in.NextToken) != wantToken || sdkaws.ToInt32(in.MaxRecords) != autoScalingPageCap ||
			in.AutoScalingGroupNames != nil || in.Filters != nil || in.IncludeInstances != nil {
			t.Errorf("call %d input = %+v; want NextToken %q, MaxRecords %d, no names, filters or IncludeInstances", i, in, wantToken, autoScalingPageCap)
		}
	}

	rows := rowsByNativeID(t, st, TypeAutoScalingGroup)
	if len(rows) != 2 {
		t.Fatalf("group rows = %v, want web and batch", rows)
	}
	web := rows[asTestGroupARN]
	if sv(web.Name) != "web" || web.Status != nil {
		t.Errorf("web name, status = %q, %v; want web, nil", sv(web.Name), web.Status)
	}
	var attrs astypes.AutoScalingGroup
	if err := json.Unmarshal([]byte(web.AttributesJSON), &attrs); err != nil {
		t.Fatalf("web attrs: %v", err)
	}
	if sdkaws.ToInt32(attrs.MinSize) != 1 || len(attrs.Tags) != 2 || !sdkaws.ToBool(attrs.Tags[0].PropagateAtLaunch) {
		t.Errorf("web attrs = %s; want the SDK element verbatim, Tags included", web.AttributesJSON)
	}
	assertTagsMap(t, web.TagsJSON, map[string]string{"env": "prod", "team": "web"})
	batch := rows[asTestGroupARN2]
	if sv(batch.Name) != "batch" || sv(batch.Status) != "Delete in progress" || batch.TagsJSON != nil {
		t.Errorf("batch name, status, tags = %q, %q, %v; want batch, Delete in progress, nil", sv(batch.Name), sv(batch.Status), batch.TagsJSON)
	}
}

// A failed later group page keeps the groups read before it, and the
// per-group phases still run over them: a denial warns, any other error is
// returned.
func TestScanAutoScaling_GroupPageErrorKeepsEarlierGroups(t *testing.T) {
	for _, c := range []struct {
		name      string
		page2Err  error
		wantCode  string // "" means the scan returns nil
		wantWarns int    // the group without a name, plus the denial
	}{
		{"denied", asTestDenied, "", 2},
		{"hard error", apiErr("ValidationException", "bad"), "ValidationException", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			groupWarns := countOpWarnings(st, autoScalingGroupsOp)
			var calls asCallLog
			h := asHandlers(&calls)
			var inputs []autoscaling.DescribeAutoScalingGroupsInput
			h["DescribeAutoScalingGroups"] = asTwoPageGroups(&inputs, c.page2Err)

			total, err := scanAutoScalingWith(t, st, h)
			switch {
			case c.wantCode == "" && err != nil:
				t.Fatalf("scanAutoScaling: %v, want nil", err)
			case c.wantCode != "" && !isAPIErrorCode(err, c.wantCode):
				t.Fatalf("scanAutoScaling err = %v, want %s", err, c.wantCode)
			}
			// web + launch configuration + web's hook and warm pool + 2 notification configurations
			if total != 6 {
				t.Errorf("total = %d, want 6", total)
			}
			if _, ok := rowsByNativeID(t, st, TypeAutoScalingGroup)[asTestGroupARN]; !ok {
				t.Error("web group from the first page missing")
			}
			if _, ok := rowsByNativeID(t, st, TypeAutoScalingLifecycleHook)[asTestHookWebDrain]; !ok {
				t.Error("web's lifecycle hook missing: the hook phase did not get the stored group")
			}
			assertContainsNative(t, st, asTestGroupARN, asTestNotifyWebA)
			if *groupWarns != c.wantWarns {
				t.Errorf("%s warnings = %d, want %d", autoScalingGroupsOp, *groupWarns, c.wantWarns)
			}
		})
	}
}

// Every phase runs whatever another phase returns: one phase's hard error is
// returned and the other phases' rows are stored.
func TestScanAutoScaling_PhaseErrorDoesNotStopOtherPhases(t *testing.T) {
	for _, c := range []struct {
		op        string
		wantTotal int
		lost      string // the type whose rows the failed phase would store
	}{
		{"DescribeAutoScalingGroups", 3, TypeAutoScalingGroup}, // LC + 2 notification configurations; no group feeds the fan-outs
		{"DescribeLaunchConfigurations", 5, TypeAutoScalingLaunchConfiguration},
		{"DescribePolicies", 6, TypeAutoScalingScalingPolicy},
		{"DescribeScheduledActions", 6, TypeAutoScalingScheduledAction},
		{"DescribeLifecycleHooks", 5, TypeAutoScalingLifecycleHook},
		{"DescribeWarmPool", 5, TypeAutoScalingWarmPool},
		{"DescribeNotificationConfigurations", 4, TypeAutoScalingNotificationConfiguration},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			var calls asCallLog
			h := asHandlers(&calls)
			h[c.op] = func(any) (any, error) { return nil, apiErr("ValidationException", "boom") }
			total, err := scanAutoScalingWith(t, st, h)
			if !isAPIErrorCode(err, "ValidationException") {
				t.Fatalf("scanAutoScaling err = %v, want the %s phase's ValidationException", err, c.op)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d, want %d", total, c.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, c.lost)); n != 0 {
				t.Errorf("%s rows = %d, want 0", c.lost, n)
			}
			if c.op != "DescribeLaunchConfigurations" {
				if _, ok := rowsByNativeID(t, st, TypeAutoScalingLaunchConfiguration)[asTestLCARN]; !ok {
					t.Error("launch configuration row missing")
				}
			}
			if c.op != "DescribeNotificationConfigurations" {
				if n := len(rowsByNativeID(t, st, TypeAutoScalingNotificationConfiguration)); n != 2 {
					t.Errorf("notification configuration rows = %d, want 2", n)
				}
			}
		})
	}
}

// A store write failure stops the later phases: every later write would fail
// too.
func TestScanAutoScaling_StoreWriteStopsLaterPhases(t *testing.T) {
	for _, failing := range []string{"DescribeAutoScalingGroups", "DescribeLaunchConfigurations"} {
		t.Run(failing, func(t *testing.T) {
			st, closeStore := openClosingStore(t)
			var calls asCallLog
			h := asHandlers(&calls)
			next := h[failing]
			h[failing] = func(in any) (any, error) {
				closeStore()
				return next(in)
			}
			_, err := scanAutoScalingWith(t, st, h)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			if last := calls.ops[len(calls.ops)-1]; last != failing {
				t.Errorf("ops called = %v; want none after %s", calls.ops, failing)
			}
		})
	}
}

// asTwoGroups lists groups web and api; every group has a hook named drain
// and a warm pool.
func asTwoGroups(h map[string]func(in any) (any, error)) {
	h["DescribeAutoScalingGroups"] = func(any) (any, error) {
		return &autoscaling.DescribeAutoScalingGroupsOutput{AutoScalingGroups: []astypes.AutoScalingGroup{
			{AutoScalingGroupName: ptrStr("web"), AutoScalingGroupARN: ptrStr(asTestGroupARN)},
			{AutoScalingGroupName: ptrStr("api"), AutoScalingGroupARN: ptrStr(asTestGroupARN + "-api")},
		}}, nil
	}
	h["DescribeLifecycleHooks"] = func(in any) (any, error) {
		group := in.(*autoscaling.DescribeLifecycleHooksInput).AutoScalingGroupName
		return &autoscaling.DescribeLifecycleHooksOutput{LifecycleHooks: []astypes.LifecycleHook{{AutoScalingGroupName: group, LifecycleHookName: ptrStr("drain")}}}, nil
	}
	h["DescribeWarmPool"] = func(any) (any, error) {
		return &autoscaling.DescribeWarmPoolOutput{WarmPoolConfiguration: &astypes.WarmPoolConfiguration{MinSize: ptrInt32(1)}}, nil
	}
}

// One group's failed lifecycle-hook or warm-pool read costs only that group:
// a denial warns once and is not returned, any other error is returned after
// the other group's row is stored, and a hook without a name warns once.
func TestScanAutoScaling_PerGroupFanOutOutcomes(t *testing.T) {
	hard := apiErr("ResourceContentionFault", "busy")
	apiHook := "arn:aws:autoscaling:us-east-1:123456789012:lifecycle-hook/api/drain"
	apiPool := "arn:aws:autoscaling:us-east-1:123456789012:warm-pool/api"
	for _, c := range []struct {
		name, op, label, typ string
		webErr               error
		webOut               any   // answered for web instead when set
		wantErr              error // nil: the scan returns nil
		lost, kept           string
		warnFirst            string // the key the one warning names first
	}{
		{name: "hook denied", op: "DescribeLifecycleHooks", label: autoScalingLifecycleHooksOp, typ: TypeAutoScalingLifecycleHook, webErr: asTestDenied, lost: asTestHookWebDrain, kept: apiHook, warnFirst: "web:"},
		{name: "hook error", op: "DescribeLifecycleHooks", label: autoScalingLifecycleHooksOp, typ: TypeAutoScalingLifecycleHook, webErr: hard, wantErr: hard, lost: asTestHookWebDrain, kept: apiHook},
		{
			name: "hook without a name", op: "DescribeLifecycleHooks", label: autoScalingLifecycleHooksOp, typ: TypeAutoScalingLifecycleHook,
			webOut: &autoscaling.DescribeLifecycleHooksOutput{LifecycleHooks: []astypes.LifecycleHook{{AutoScalingGroupName: ptrStr("web")}}},
			lost:   asTestHookWebDrain, kept: apiHook, warnFirst: "one of group web",
		},
		{name: "warm pool denied", op: "DescribeWarmPool", label: autoScalingWarmPoolOp, typ: TypeAutoScalingWarmPool, webErr: asTestDenied, lost: asTestWarmPoolWeb, kept: apiPool, warnFirst: "web:"},
		{name: "warm pool error", op: "DescribeWarmPool", label: autoScalingWarmPoolOp, typ: TypeAutoScalingWarmPool, webErr: hard, wantErr: hard, lost: asTestWarmPoolWeb, kept: apiPool},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var warnings []store.ScanWarning // every warning under c.label is reported on this goroutine
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == c.label {
					warnings = append(warnings, w)
				}
			}
			h := asHandlers(&asCallLog{})
			asTwoGroups(h)
			next := h[c.op]
			h[c.op] = func(in any) (any, error) {
				var group *string
				switch req := in.(type) {
				case *autoscaling.DescribeLifecycleHooksInput:
					group = req.AutoScalingGroupName
				case *autoscaling.DescribeWarmPoolInput:
					group = req.AutoScalingGroupName
				}
				if sv(group) == "web" && c.webErr != nil {
					return nil, c.webErr
				}
				if sv(group) == "web" && c.webOut != nil {
					return c.webOut, nil
				}
				return next(in)
			}
			_, err := scanAutoScalingWith(t, st, h)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[c.kept]; !ok || len(rows) != 1 {
				t.Errorf("%s rows = %v; want only %s", c.typ, rows, c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; want it not read", c.lost)
			}
			if c.wantErr != nil {
				if len(warnings) != 0 {
					t.Errorf("%s warnings = %+v, want none", c.label, warnings)
				}
				return
			}
			if len(warnings) != 1 || warnings[0].Scope != testAccountID+"/"+testRegion {
				t.Fatalf("%s warnings = %+v, want one scoped to %s/%s", c.label, warnings, testAccountID, testRegion)
			}
			// The warning names the group it is about: web, not its sibling api. ScanWarning has
			// no key field, so the key is matched inside the message, without the text around it.
			if msg := warnings[0].Message; !strings.Contains(msg, c.warnFirst) {
				t.Errorf("%s warning = %q, want it to name %q first", c.label, msg, c.warnFirst)
			}
		})
	}
}

// The account-wide listings read every page at the documented MaxRecords cap
// with no filter, warn once about elements without the id their row needs,
// and keep the pages read before a failed page.
func TestScanAutoScaling_ListPhasesKeepPagesAndWarnEmptyIDs(t *testing.T) {
	lcARN := func(n string) *string {
		return ptrStr("arn:aws:autoscaling:us-east-1:123456789012:launchConfiguration:1:launchConfigurationName/" + n)
	}
	policyARN := func(n string) *string {
		return ptrStr("arn:aws:autoscaling:us-east-1:123456789012:scalingPolicy:1:autoScalingGroupName/web:policyName/" + n)
	}
	actionARN := func(n string) *string {
		return ptrStr("arn:aws:autoscaling:us-east-1:123456789012:scheduledUpdateGroupAction:1:autoScalingGroupName/web:scheduledActionName/" + n)
	}
	for _, c := range []struct {
		op, label, typ string
		// answer returns the page for token: "" holds a stored element and one
		// without its id, "p2" one stored element.
		answer func(in any) (token *string, maxRecords *int32, out any)
		first  string // NativeID of the first page's stored element
	}{
		{"DescribeLaunchConfigurations", autoScalingLaunchConfigurationsOp, TypeAutoScalingLaunchConfiguration, func(in any) (*string, *int32, any) {
			req := in.(*autoscaling.DescribeLaunchConfigurationsInput)
			if req.LaunchConfigurationNames != nil {
				t.Errorf("DescribeLaunchConfigurations names = %v, want none", req.LaunchConfigurationNames)
			}
			if req.NextToken == nil {
				return req.NextToken, req.MaxRecords, &autoscaling.DescribeLaunchConfigurationsOutput{NextToken: ptrStr("p2"), LaunchConfigurations: []astypes.LaunchConfiguration{
					{LaunchConfigurationName: ptrStr("a"), LaunchConfigurationARN: lcARN("a")}, {LaunchConfigurationARN: lcARN("nameless")},
				}}
			}
			return req.NextToken, req.MaxRecords, &autoscaling.DescribeLaunchConfigurationsOutput{LaunchConfigurations: []astypes.LaunchConfiguration{
				{LaunchConfigurationName: ptrStr("b"), LaunchConfigurationARN: lcARN("b")},
			}}
		}, sv(lcARN("a"))},
		{"DescribePolicies", autoScalingPoliciesOp, TypeAutoScalingScalingPolicy, func(in any) (*string, *int32, any) {
			req := in.(*autoscaling.DescribePoliciesInput)
			if req.AutoScalingGroupName != nil || req.PolicyNames != nil || req.PolicyTypes != nil {
				t.Errorf("DescribePolicies input = %+v, want no filter", req)
			}
			if req.NextToken == nil {
				return req.NextToken, req.MaxRecords, &autoscaling.DescribePoliciesOutput{NextToken: ptrStr("p2"), ScalingPolicies: []astypes.ScalingPolicy{
					{PolicyName: ptrStr("a"), PolicyARN: policyARN("a")}, {PolicyName: ptrStr("arnless")},
				}}
			}
			return req.NextToken, req.MaxRecords, &autoscaling.DescribePoliciesOutput{ScalingPolicies: []astypes.ScalingPolicy{
				{PolicyName: ptrStr("b"), PolicyARN: policyARN("b")},
			}}
		}, sv(policyARN("a"))},
		{"DescribeScheduledActions", autoScalingScheduledActionsOp, TypeAutoScalingScheduledAction, func(in any) (*string, *int32, any) {
			req := in.(*autoscaling.DescribeScheduledActionsInput)
			if req.AutoScalingGroupName != nil || req.ScheduledActionNames != nil || req.StartTime != nil || req.EndTime != nil {
				t.Errorf("DescribeScheduledActions input = %+v, want no filter", req)
			}
			if req.NextToken == nil {
				return req.NextToken, req.MaxRecords, &autoscaling.DescribeScheduledActionsOutput{NextToken: ptrStr("p2"), ScheduledUpdateGroupActions: []astypes.ScheduledUpdateGroupAction{
					{ScheduledActionName: ptrStr("a"), ScheduledActionARN: actionARN("a")}, {ScheduledActionARN: actionARN("nameless")},
				}}
			}
			return req.NextToken, req.MaxRecords, &autoscaling.DescribeScheduledActionsOutput{ScheduledUpdateGroupActions: []astypes.ScheduledUpdateGroupAction{
				{ScheduledActionName: ptrStr("b"), ScheduledActionARN: actionARN("b")},
			}}
		}, sv(actionARN("a"))},
	} {
		for _, failPage2 := range []bool{false, true} {
			t.Run(c.op+map[bool]string{false: "/all pages", true: "/second page fails"}[failPage2], func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, c.label)
				h := asHandlers(&asCallLog{})
				var (
					mu     sync.Mutex
					tokens []string
				)
				h[c.op] = func(in any) (any, error) {
					token, maxRecords, out := c.answer(in)
					mu.Lock()
					tokens = append(tokens, sv(token))
					mu.Unlock()
					if sdkaws.ToInt32(maxRecords) != autoScalingPageCap {
						t.Errorf("%s MaxRecords = %d, want %d", c.op, sdkaws.ToInt32(maxRecords), autoScalingPageCap)
					}
					if failPage2 && token != nil {
						return nil, apiErr("ResourceContentionFault", "busy")
					}
					return out, nil
				}
				_, err := scanAutoScalingWith(t, st, h)
				if failPage2 != isAPIErrorCode(err, "ResourceContentionFault") || (!failPage2 && err != nil) {
					t.Fatalf("err = %v with the second page failing %v", err, failPage2)
				}
				if !slices.Equal(tokens, []string{"", "p2"}) {
					t.Errorf("tokens sent = %q, want [\"\" \"p2\"]", tokens)
				}
				rows := rowsByNativeID(t, st, c.typ)
				wantRows := map[bool]int{false: 2, true: 1}[failPage2]
				if _, ok := rows[c.first]; !ok || len(rows) != wantRows {
					t.Errorf("%s rows = %d (first page's stored: %v), want %d", c.typ, len(rows), ok, wantRows)
				}
				if *warns != 1 {
					t.Errorf("%s warnings = %d, want 1 for the element without its id", c.label, *warns)
				}
			})
		}
	}
}

// A store write failure in a per-group phase stops the scan before that
// phase warns about a group it denied.
func TestScanAutoScaling_GroupPhaseStoreWriteFailureWarnsNothing(t *testing.T) {
	st, closeStore := openClosingStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	h := asHandlers(&asCallLog{})
	asTwoGroups(h)
	next := h["DescribeLifecycleHooks"]
	h["DescribeLifecycleHooks"] = func(in any) (any, error) {
		closeStore()
		if sv(in.(*autoscaling.DescribeLifecycleHooksInput).AutoScalingGroupName) == "web" {
			return nil, asTestDenied
		}
		return next(in)
	}
	_, err := scanAutoScalingWith(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	for _, w := range warnings {
		if w.Service != "store" {
			t.Errorf("warning %+v after the failed write, want none", w)
		}
	}
}

// Once ctx is done a per-group phase reads no group and says so rather than
// returning as if no group had hooks.
func TestScanAutoScalingLifecycleHooks_StopsWhenContextDone(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := autoscaling.NewFromConfig(opStubConfig(asHandlers(&asCallLog{}), asTestDenied))
	_, _, err := scanAutoScalingLifecycleHooks(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, []string{"web"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
