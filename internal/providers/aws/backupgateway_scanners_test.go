package aws

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backupgateway"
	bgwtypes "github.com/aws/aws-sdk-go-v2/service/backupgateway/types"
	"github.com/icearp/disco-cli/store"
)

type stubBackupGateway struct {
	backupGatewayAPI
	hyps []bgwtypes.Hypervisor
}

func (s *stubBackupGateway) ListHypervisors(_ context.Context, _ *backupgateway.ListHypervisorsInput, _ ...func(*backupgateway.Options)) (*backupgateway.ListHypervisorsOutput, error) {
	return &backupgateway.ListHypervisorsOutput{Hypervisors: s.hyps}, nil
}

func TestScanBackupGatewayHypervisors(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	arn := fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hyp-1", testRegion, acct.ID)
	name := "vc1"
	stub := &stubBackupGateway{
		hyps: []bgwtypes.Hypervisor{
			{HypervisorArn: &arn, Name: &name, State: bgwtypes.HypervisorStateOnline},
		},
	}

	_, total, _, err := scanBackupGatewayHypervisors(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d want 1", total)
	}
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, arn)); err != nil {
		t.Errorf("hypervisor missing: %v", err)
	}
}

var (
	bgwTestHypA = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hype-aaaa", testRegion, testAccountID)
	bgwTestHypB = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hype-bbbb", testRegion, testAccountID)
	bgwTestGwA  = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:gateway/bgw-aaaa", testRegion, testAccountID)
	bgwTestHypC = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hype-cccc", testRegion, testAccountID)
	bgwTestGwB  = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:gateway/bgw-bbbb", testRegion, testAccountID)
	bgwTestGwC  = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:gateway/bgw-cccc", testRegion, testAccountID)
	bgwTestVM   = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:vm/vm-1", testRegion, testAccountID)
	bgwTestRole = fmt.Sprintf("arn:aws:iam::%s:role/bgw-tags", testAccountID)

	bgwTestSchedule  = bgwTestGwA + "/bandwidth-rate-limit-schedule"
	bgwTestScheduleB = bgwTestGwB + "/bandwidth-rate-limit-schedule"
	bgwTestMapping   = bgwTestHypA + "/property-mappings"
	bgwTestMappingB  = bgwTestHypB + "/property-mappings"

	bgwDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: backup-gateway:Get")
	bgwThrottle = apiErr("ThrottlingException", "slow down")
	bgwBadInput = apiErr("InvalidRequestException", "bad input")
)

// bgwCalls records the parents each configuration op was called for.
type bgwCalls struct{ schedules, mappings parentCalls }

// bgwHandlers answers a region with hypervisor A on page 1 and B and C on
// page 2, gateways A, B and C, one virtual machine, a schedule on gateways A
// and B and property mappings on hypervisors A and B. Gateway C and
// hypervisor C have no configuration.
func bgwHandlers(calls *bgwCalls) map[string]func(in any) (any, error) {
	page2 := "page-2"
	return map[string]func(in any) (any, error){
		"ListHypervisors": func(in any) (any, error) {
			if sv(in.(*backupgateway.ListHypervisorsInput).NextToken) == "" {
				return &backupgateway.ListHypervisorsOutput{
					Hypervisors: []bgwtypes.Hypervisor{{HypervisorArn: &bgwTestHypA, Name: sdkaws.String("a"), State: bgwtypes.HypervisorStateOnline}},
					NextToken:   &page2,
				}, nil
			}
			return &backupgateway.ListHypervisorsOutput{
				Hypervisors: []bgwtypes.Hypervisor{
					{HypervisorArn: &bgwTestHypB, Name: sdkaws.String("b"), State: bgwtypes.HypervisorStatePending},
					{HypervisorArn: &bgwTestHypC, Name: sdkaws.String("c"), State: bgwtypes.HypervisorStateOnline},
				},
			}, nil
		},
		"ListGateways": func(any) (any, error) {
			return &backupgateway.ListGatewaysOutput{Gateways: []bgwtypes.Gateway{{GatewayArn: &bgwTestGwA}, {GatewayArn: &bgwTestGwB}, {GatewayArn: &bgwTestGwC}}}, nil
		},
		"ListVirtualMachines": func(any) (any, error) {
			return &backupgateway.ListVirtualMachinesOutput{VirtualMachines: []bgwtypes.VirtualMachine{{ResourceArn: &bgwTestVM}}}, nil
		},
		"GetBandwidthRateLimitSchedule": func(in any) (any, error) {
			gw := sv(in.(*backupgateway.GetBandwidthRateLimitScheduleInput).GatewayArn)
			calls.schedules.add(gw)
			out := &backupgateway.GetBandwidthRateLimitScheduleOutput{GatewayArn: &gw}
			if gw != bgwTestGwC {
				out.BandwidthRateLimitIntervals = []bgwtypes.BandwidthRateLimitInterval{{
					DaysOfWeek: []int32{1, 2}, StartHourOfDay: sdkaws.Int32(8), StartMinuteOfHour: sdkaws.Int32(0),
					EndHourOfDay: sdkaws.Int32(17), EndMinuteOfHour: sdkaws.Int32(59), AverageUploadRateLimitInBitsPerSec: sdkaws.Int64(102400),
				}}
			}
			return out, nil
		},
		"GetHypervisorPropertyMappings": func(in any) (any, error) {
			h := sv(in.(*backupgateway.GetHypervisorPropertyMappingsInput).HypervisorArn)
			calls.mappings.add(h)
			out := &backupgateway.GetHypervisorPropertyMappingsOutput{HypervisorArn: &h}
			if h != bgwTestHypC {
				out.IamRoleArn = &bgwTestRole
				out.VmwareToAwsTagMappings = []bgwtypes.VmwareToAwsTagMapping{{
					VmwareCategory: sdkaws.String("env"), VmwareTagName: sdkaws.String("prod"),
					AwsTagKey: sdkaws.String("Env"), AwsTagValue: sdkaws.String("prod"),
				}}
			}
			return out, nil
		},
	}
}

func scanBackupGatewayTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, bgwDenied)
	total, _, err := scanBackupGateway(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

// bgwConfigIDs returns the NativeIDs of every stored configuration row, sorted.
func bgwConfigIDs(t *testing.T, st *store.Store) []string {
	t.Helper()
	var ids []string
	for _, typ := range []string{TypeBackupGatewayBandwidthRateLimitSchedule, TypeBackupGatewayHypervisorPropertyMapping} {
		for id := range rowsByNativeID(t, st, typ) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func bgwSorted(ids ...string) []string {
	sort.Strings(ids)
	return ids
}

// bgwAllConfigs is every configuration row bgwHandlers yields, sorted.
func bgwAllConfigs() []string {
	return bgwSorted(bgwTestSchedule, bgwTestScheduleB, bgwTestMapping, bgwTestMappingB)
}

// bgwConfigsWithout is bgwAllConfigs less the rows in drop.
func bgwConfigsWithout(drop ...string) []string {
	return slices.DeleteFunc(bgwAllConfigs(), func(id string) bool { return slices.Contains(drop, id) })
}

func TestScanBackupGateway_ConfigRows(t *testing.T) {
	st := newTestStore(t)
	var calls bgwCalls
	total, err := scanBackupGatewayTopLevel(t, st, bgwHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := calls.schedules.distinct(), bgwSorted(bgwTestGwA, bgwTestGwB, bgwTestGwC); !slices.Equal(got, want) {
		t.Errorf("GetBandwidthRateLimitSchedule gateways = %v, want %v", got, want)
	}
	if got, want := calls.mappings.distinct(), bgwSorted(bgwTestHypA, bgwTestHypB, bgwTestHypC); !slices.Equal(got, want) {
		t.Errorf("GetHypervisorPropertyMappings hypervisors = %v, want %v", got, want)
	}
	// 3 hypervisors + 3 gateways + 1 VM + 2 schedules + 2 mappings
	if total != 11 {
		t.Errorf("total = %d, want 11", total)
	}

	sched, ok := rowsByNativeID(t, st, TypeBackupGatewayBandwidthRateLimitSchedule)[bgwTestSchedule]
	if !ok {
		t.Fatalf("schedule %s not stored", bgwTestSchedule)
	}
	if sv(sched.Region) != testRegion {
		t.Errorf("schedule region = %q, want %q", sv(sched.Region), testRegion)
	}
	if !strings.Contains(sched.AttributesJSON, `"AverageUploadRateLimitInBitsPerSec":102400`) {
		t.Errorf("schedule attrs = %s, want the interval", sched.AttributesJSON)
	}
	mapping, ok := rowsByNativeID(t, st, TypeBackupGatewayHypervisorPropertyMapping)[bgwTestMapping]
	if !ok {
		t.Fatalf("mapping %s not stored", bgwTestMapping)
	}
	if sv(mapping.Region) != testRegion {
		t.Errorf("mapping region = %q, want %q", sv(mapping.Region), testRegion)
	}
	if !strings.Contains(mapping.AttributesJSON, `"IamRoleArn":"`+bgwTestRole+`"`) || !strings.Contains(mapping.AttributesJSON, `"VmwareTagName":"prod"`) {
		t.Errorf("mapping attrs = %s, want the role and the tag mapping", mapping.AttributesJSON)
	}
	if got, want := bgwConfigIDs(t, st), bgwAllConfigs(); !slices.Equal(got, want) {
		t.Errorf("config rows = %v, want %v: an unconfigured parent stores none", got, want)
	}
	assertContainsNative(t, st, bgwTestGwA, bgwTestSchedule)
	assertContainsNative(t, st, bgwTestHypA, bgwTestMapping)
}

// A hypervisor whose mappings name only an IAM role, or only tag mappings,
// still has a configuration.
func TestScanBackupGateway_PartialPropertyMappingsStored(t *testing.T) {
	for name, out := range map[string]*backupgateway.GetHypervisorPropertyMappingsOutput{
		"role only": {IamRoleArn: &bgwTestRole},
		"tags only": {VmwareToAwsTagMappings: []bgwtypes.VmwareToAwsTagMapping{{VmwareTagName: sdkaws.String("prod")}}},
	} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			var calls bgwCalls
			h := bgwHandlers(&calls)
			h["GetHypervisorPropertyMappings"] = func(in any) (any, error) {
				if sv(in.(*backupgateway.GetHypervisorPropertyMappingsInput).HypervisorArn) != bgwTestHypA {
					return &backupgateway.GetHypervisorPropertyMappingsOutput{}, nil
				}
				return out, nil
			}
			if _, err := scanBackupGatewayTopLevel(t, st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if _, ok := rowsByNativeID(t, st, TypeBackupGatewayHypervisorPropertyMapping)[bgwTestMapping]; !ok {
				t.Errorf("mapping %s not stored", bgwTestMapping)
			}
		})
	}
}

// Per parent: a vanished parent is skipped silently, a denial warns once for
// the op, a ValidationException or a transient error skips with one warning,
// and the sibling's row is kept in each case; any other error fails the op's
// phase, storing none of its rows, after the resource phases stored theirs.
func TestScanBackupGateway_ConfigParentErrors(t *testing.T) {
	notFound := apiErr("ResourceNotFoundException", "gone")
	validation := apiErr("ValidationException", "not ready")
	for _, tc := range []struct {
		name      string
		op        string
		parent    string // the parent that fails
		err       error
		wantErr   string
		wantRows  []string
		wantWarns int
	}{
		{"schedule gateway gone", backupGatewayBandwidthScheduleOp, bgwTestGwA, notFound, "", bgwConfigsWithout(bgwTestSchedule), 0},
		{"schedule denied", backupGatewayBandwidthScheduleOp, bgwTestGwA, bgwDenied, "", bgwConfigsWithout(bgwTestSchedule), 1},
		{"schedule not ready", backupGatewayBandwidthScheduleOp, bgwTestGwA, validation, "", bgwConfigsWithout(bgwTestSchedule), 1},
		{"schedule throttled", backupGatewayBandwidthScheduleOp, bgwTestGwA, bgwThrottle, "", bgwConfigsWithout(bgwTestSchedule), 1},
		{"schedule other error", backupGatewayBandwidthScheduleOp, bgwTestGwA, bgwBadInput, "InvalidRequestException", bgwConfigsWithout(bgwTestSchedule, bgwTestScheduleB), 0},
		{"schedule sibling gone", backupGatewayBandwidthScheduleOp, bgwTestGwB, notFound, "", bgwConfigsWithout(bgwTestScheduleB), 0},
		{"mapping hypervisor gone", backupGatewayPropertyMappingsOp, bgwTestHypA, notFound, "", bgwConfigsWithout(bgwTestMapping), 0},
		{"mapping denied", backupGatewayPropertyMappingsOp, bgwTestHypA, bgwDenied, "", bgwConfigsWithout(bgwTestMapping), 1},
		{"mapping not ready", backupGatewayPropertyMappingsOp, bgwTestHypA, validation, "", bgwConfigsWithout(bgwTestMapping), 1},
		{"mapping throttled", backupGatewayPropertyMappingsOp, bgwTestHypA, bgwThrottle, "", bgwConfigsWithout(bgwTestMapping), 1},
		{"mapping other error", backupGatewayPropertyMappingsOp, bgwTestHypA, bgwBadInput, "InvalidRequestException", bgwConfigsWithout(bgwTestMapping, bgwTestMappingB), 0},
		{"mapping sibling gone", backupGatewayPropertyMappingsOp, bgwTestHypB, notFound, "", bgwConfigsWithout(bgwTestMappingB), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.op)
			var calls bgwCalls
			h := bgwHandlers(&calls)
			sdkOp := strings.TrimPrefix(tc.op, "backupgateway:")
			ok := h[sdkOp]
			h[sdkOp] = func(in any) (any, error) {
				var parent string
				switch v := in.(type) {
				case *backupgateway.GetBandwidthRateLimitScheduleInput:
					parent = sv(v.GatewayArn)
				case *backupgateway.GetHypervisorPropertyMappingsInput:
					parent = sv(v.HypervisorArn)
				}
				if parent == tc.parent {
					return nil, tc.err
				}
				return ok(in)
			}
			_, err := scanBackupGatewayTopLevel(t, st, h)
			if tc.wantErr != "" {
				assertAPICode(t, err, tc.wantErr)
			} else if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := bgwConfigIDs(t, st); !slices.Equal(got, bgwSorted(tc.wantRows...)) {
				t.Errorf("config rows = %v, want %v", got, bgwSorted(tc.wantRows...))
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings for %s = %d, want %d", tc.op, *warns, tc.wantWarns)
			}
			if _, ok := rowsByNativeID(t, st, TypeBackupGatewayVirtualMachine)[bgwTestVM]; !ok {
				t.Errorf("virtual machine %s not stored: the configuration phases must run after every resource phase", bgwTestVM)
			}
		})
	}
}

// A denial for every parent of an op is one warning, not one per parent.
func TestScanBackupGateway_ConfigDeniedWarnsOnce(t *testing.T) {
	for _, op := range []string{backupGatewayBandwidthScheduleOp, backupGatewayPropertyMappingsOp} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, op)
			var calls bgwCalls
			h := bgwHandlers(&calls)
			delete(h, strings.TrimPrefix(op, "backupgateway:"))
			if _, err := scanBackupGatewayTopLevel(t, st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d, want 1", *warns)
			}
		})
	}
}

// A hard error in a resource phase is returned, the resource phases after it
// do not run, and the configuration phases still run over the parents stored
// before it.
func TestScanBackupGateway_ResourcePhaseErrorStillRunsConfigs(t *testing.T) {
	for _, tc := range []struct {
		failOp      string
		fail        func(in any) bool // whether the call fails
		wantRows    []string
		wantAbsent  []string // resource types the phases after failOp would store
		wantPresent string   // a resource stored before failOp
	}{
		{
			"ListHypervisors",
			func(in any) bool { return sv(in.(*backupgateway.ListHypervisorsInput).NextToken) != "" },
			[]string{bgwTestMapping},
			[]string{TypeBackupGatewayGateway, TypeBackupGatewayVirtualMachine},
			bgwTestHypA,
		},
		{"ListGateways", func(any) bool { return true }, []string{bgwTestMapping, bgwTestMappingB}, []string{TypeBackupGatewayVirtualMachine}, bgwTestHypA},
		{"ListVirtualMachines", func(any) bool { return true }, bgwAllConfigs(), nil, bgwTestGwA},
	} {
		t.Run(tc.failOp, func(t *testing.T) {
			st := newTestStore(t)
			var calls bgwCalls
			h := bgwHandlers(&calls)
			ok := h[tc.failOp]
			h[tc.failOp] = func(in any) (any, error) {
				if tc.fail(in) {
					return nil, bgwBadInput
				}
				return ok(in)
			}
			_, err := scanBackupGatewayTopLevel(t, st, h)
			assertAPICode(t, err, "InvalidRequestException")
			if got := bgwConfigIDs(t, st); !slices.Equal(got, bgwSorted(tc.wantRows...)) {
				t.Errorf("config rows = %v, want %v", got, bgwSorted(tc.wantRows...))
			}
			for _, typ := range tc.wantAbsent {
				if rows := rowsByNativeID(t, st, typ); len(rows) != 0 {
					t.Errorf("%s rows = %d, want none: the phases after %s must not run", typ, len(rows), tc.failOp)
				}
			}
			if _, err := st.GetResource(store.ResourceID("aws", testAccountID, tc.wantPresent)); err != nil {
				t.Errorf("%s not stored before %s failed: %v", tc.wantPresent, tc.failOp, err)
			}
		})
	}
}

// A transient error in a resource phase is demoted to a warning so that a
// configuration phase's hard error still reaches the dispatcher as a hard error.
func TestScanBackupGateway_ResourceTransientDoesNotMaskConfigError(t *testing.T) {
	for _, op := range []string{"GetBandwidthRateLimitSchedule", "GetHypervisorPropertyMappings"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "backupgateway")
			var calls bgwCalls
			h := bgwHandlers(&calls)
			h["ListVirtualMachines"] = func(any) (any, error) { return nil, bgwThrottle }
			h[op] = func(any) (any, error) { return nil, bgwBadInput }
			_, err := scanBackupGatewayTopLevel(t, st, h)
			assertAPICode(t, err, "InvalidRequestException")
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v, want outcomeError", got)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
			}
		})
	}
}
