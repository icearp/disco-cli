package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backupgateway"
	bgwtypes "github.com/aws/aws-sdk-go-v2/service/backupgateway/types"
	"github.com/icearp/disco-cli/store"
)

var (
	bgwTestHypA = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hype-aaaa", testRegion, testAccountID)
	bgwTestHypB = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hype-bbbb", testRegion, testAccountID)
	bgwTestGwA  = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:gateway/bgw-aaaa", testRegion, testAccountID)
	bgwTestHypC = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hype-cccc", testRegion, testAccountID)
	bgwTestGwB  = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:gateway/bgw-bbbb", testRegion, testAccountID)
	bgwTestGwC  = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:gateway/bgw-cccc", testRegion, testAccountID)
	bgwTestVM   = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:vm/vm-1", testRegion, testAccountID)
	bgwTestVM2  = fmt.Sprintf("arn:aws:backup-gateway:%s:%s:vm/vm-2", testRegion, testAccountID)
	bgwTestRole = fmt.Sprintf("arn:aws:iam::%s:role/bgw-tags", testAccountID)

	bgwTestSchedule  = bgwTestGwA + "/bandwidth-rate-limit-schedule"
	bgwTestScheduleB = bgwTestGwB + "/bandwidth-rate-limit-schedule"
	bgwTestMapping   = bgwTestHypA + "/property-mappings"
	bgwTestMappingB  = bgwTestHypB + "/property-mappings"

	bgwDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: backup-gateway:Get")
	bgwThrottle = apiErr("ThrottlingException", "slow down")
	bgwBadInput = apiErr("InvalidRequestException", "bad input")
	bgwGoneErr  = apiErr("ResourceNotFoundException", "gone")

	// bgwTestTags are the tags ListTagsForResource returns; every other
	// resource has none.
	bgwTestTags = map[string]map[string]string{
		bgwTestHypA: {"env": "prod"},
		bgwTestGwA:  {"team": "ops", "cost": "42"},
		bgwTestVM:   {"app": "db"},
	}
)

// bgwTestTotal is every row bgwHandlers yields: 3 hypervisors, 3 gateways,
// 2 virtual machines, 2 schedules and 2 property mappings.
const bgwTestTotal = 12

// bgwCalls records the calls bgwHandlers answers, and holds the tags and the
// errors its ListTagsForResource returns per ARN on top of bgwTestTags.
type bgwCalls struct {
	schedules, mappings parentCalls
	// tagARNs is every ARN ListTagsForResource was sent; lists every listing
	// op called.
	tagARNs, lists parentCalls
	tags           map[string]map[string]string
	tagErrs        map[string]error
}

// listTags answers ListTagsForResource from c.tagErrs, c.tags and
// bgwTestTags, in that order.
func (c *bgwCalls) listTags(in any) (any, error) {
	arn := sv(in.(*backupgateway.ListTagsForResourceInput).ResourceArn)
	c.tagARNs.add(arn)
	if err, ok := c.tagErrs[arn]; ok {
		return nil, err
	}
	tags, ok := c.tags[arn]
	if !ok {
		tags = bgwTestTags[arn]
	}
	out := &backupgateway.ListTagsForResourceOutput{ResourceArn: &arn}
	for k, v := range tags {
		out.Tags = append(out.Tags, bgwtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return out, nil
}

// bgwHandlers answers a region with hypervisor A on page 1 and B and C on
// page 2, gateway A on page 1 and B and C on page 2, virtual machine 1 on
// page 1 and 2 on page 2, a schedule on gateways A and B and property
// mappings on hypervisors A and B. Gateway C and hypervisor C have no
// configuration. Hypervisor A, gateway A and virtual machine 1 are tagged
// (bgwTestTags).
func bgwHandlers(calls *bgwCalls) map[string]func(in any) (any, error) {
	page2 := "page-2"
	firstPage := func(op string, in any) bool {
		calls.lists.add(op)
		return bacField[*string](in, "NextToken") == nil
	}
	return map[string]func(in any) (any, error){
		"ListHypervisors": func(in any) (any, error) {
			if firstPage("ListHypervisors", in) {
				return &backupgateway.ListHypervisorsOutput{
					Hypervisors: []bgwtypes.Hypervisor{{HypervisorArn: &bgwTestHypA, Name: sdkaws.String("a"), Host: sdkaws.String("vc-a.example"), State: bgwtypes.HypervisorStateOnline}},
					NextToken:   &page2,
				}, nil
			}
			return &backupgateway.ListHypervisorsOutput{
				Hypervisors: []bgwtypes.Hypervisor{
					{HypervisorArn: &bgwTestHypB, Name: sdkaws.String("b"), State: bgwtypes.HypervisorStatePending},
					{HypervisorArn: &bgwTestHypC},
				},
			}, nil
		},
		"ListGateways": func(in any) (any, error) {
			if firstPage("ListGateways", in) {
				return &backupgateway.ListGatewaysOutput{
					Gateways:  []bgwtypes.Gateway{{GatewayArn: &bgwTestGwA, GatewayDisplayName: sdkaws.String("gw-a"), GatewayType: bgwtypes.GatewayTypeBackupVm}},
					NextToken: &page2,
				}, nil
			}
			return &backupgateway.ListGatewaysOutput{Gateways: []bgwtypes.Gateway{{GatewayArn: &bgwTestGwB}, {GatewayArn: &bgwTestGwC}}}, nil
		},
		"ListVirtualMachines": func(in any) (any, error) {
			if firstPage("ListVirtualMachines", in) {
				return &backupgateway.ListVirtualMachinesOutput{
					VirtualMachines: []bgwtypes.VirtualMachine{{ResourceArn: &bgwTestVM, Name: sdkaws.String("vm1"), Path: sdkaws.String("/dc/vm1")}},
					NextToken:       &page2,
				}, nil
			}
			return &backupgateway.ListVirtualMachinesOutput{VirtualMachines: []bgwtypes.VirtualMachine{{ResourceArn: &bgwTestVM2}}}, nil
		},
		"ListTagsForResource": calls.listTags,
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
	total, err := bgwScan(context.Background(), st, handlers)
	return total, err
}

// bgwScan runs the service scan entry point against handlers; an op without
// a handler is denied.
func bgwScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, bgwDenied)
	return scanBackupGateway(ctx, acct, testRegion, st, testScanID)
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

// bgwTaggedIDs is the NativeID of every hypervisor, gateway and virtual
// machine bgwHandlers yields, sorted: the ARNs sent to ListTagsForResource.
func bgwTaggedIDs() []string {
	return bgwSorted(bgwTestHypA, bgwTestHypB, bgwTestHypC, bgwTestGwA, bgwTestGwB, bgwTestGwC, bgwTestVM, bgwTestVM2)
}

// bgwStoredIDs is the NativeID of every stored hypervisor, gateway and
// virtual machine, sorted.
func bgwStoredIDs(t *testing.T, st *store.Store) []string {
	t.Helper()
	var ids []string
	for _, typ := range []string{TypeBackupGatewayHypervisor, TypeBackupGatewayGateway, TypeBackupGatewayVirtualMachine} {
		for id := range rowsByNativeID(t, st, typ) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func TestScanBackupGateway_ConfigRows(t *testing.T) {
	t.Parallel()
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
	if total != bgwTestTotal {
		t.Errorf("total = %d, want %d", total, bgwTestTotal)
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// A hard error in a resource phase's later page is returned; the pages read
// before it are kept, and every later phase still runs over the parents
// stored.
func TestScanBackupGateway_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op         string
		typ        string
		kept, lost string
		wantTotal  int
		wantConfig []string
	}{
		{"ListHypervisors", TypeBackupGatewayHypervisor, bgwTestHypA, bgwTestHypB, bgwTestTotal - 2 - 1, bgwConfigsWithout(bgwTestMappingB)},
		{"ListGateways", TypeBackupGatewayGateway, bgwTestGwA, bgwTestGwB, bgwTestTotal - 2 - 1, bgwConfigsWithout(bgwTestScheduleB)},
		{"ListVirtualMachines", TypeBackupGatewayVirtualMachine, bgwTestVM, bgwTestVM2, bgwTestTotal - 1, bgwAllConfigs()},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			var calls bgwCalls
			h := bgwHandlers(&calls)
			list := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					return nil, bgwBadInput
				}
				return list(in)
			}
			total, err := scanBackupGatewayTopLevel(t, st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidRequestException")
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			rows := rowsByNativeID(t, st, tc.typ)
			if _, ok := rows[tc.kept]; !ok {
				t.Errorf("%s, read before the failure, missing", tc.kept)
			}
			if _, ok := rows[tc.lost]; ok {
				t.Errorf("%s stored; its page failed", tc.lost)
			}
			if got := bgwConfigIDs(t, st); !slices.Equal(got, bgwSorted(tc.wantConfig...)) {
				t.Errorf("config rows = %v, want %v", got, bgwSorted(tc.wantConfig...))
			}
			if _, ok := rowsByNativeID(t, st, TypeBackupGatewayVirtualMachine)[bgwTestVM]; !ok {
				t.Errorf("virtual machine %s not stored: a phase's error must not stop the later ones", bgwTestVM)
			}
		})
	}
}

// A transient error in a resource phase is demoted to a warning under its op
// so that a configuration phase's hard error still reaches the dispatcher as
// a hard error.
func TestScanBackupGateway_ResourceTransientDoesNotMaskConfigError(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"GetBandwidthRateLimitSchedule", "GetHypervisorPropertyMappings"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, backupGatewayListVirtualMachinesOp)
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

// Every hypervisor, gateway and virtual machine is stored from both its
// pages under its listed ARN, its attributes the listed element, with the
// tags ListTagsForResource returns (nil for none). The tag op is sent exactly
// the ARN of every such row, and no configuration row. A hypervisor is named
// by its Name and has its State as Status, both stored as listed even when
// empty; a gateway is named by its display name and a virtual machine by its
// Name, each by its ARN when that is empty. No error, no warning.
func TestScanBackupGateway_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls bgwCalls
	counted, upserts := countUpserts(st)
	total, err := bgwScan(context.Background(), counted, bgwHandlers(&calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != bgwTestTotal || inserted != bgwTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, bgwTestTotal, bgwTestTotal)
	}
	if got := calls.tagARNs.distinct(); !slices.Equal(got, bgwTaggedIDs()) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, bgwTaggedIDs())
	}
	if n := len(calls.tagARNs.ids); n != len(bgwTaggedIDs()) {
		t.Errorf("ListTagsForResource calls = %d; want one per row, %d", n, len(bgwTaggedIDs()))
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}

	for _, c := range []struct {
		typ, arn     string
		name, status string // status "-": the row has no Status
		tags         map[string]string
		attr         func(string) bool // whether the attributes are the listed element
	}{
		{TypeBackupGatewayHypervisor, bgwTestHypA, "a", "ONLINE", bgwTestTags[bgwTestHypA], func(a string) bool {
			var h bgwtypes.Hypervisor
			return json.Unmarshal([]byte(a), &h) == nil && sv(h.Host) == "vc-a.example" && sv(h.HypervisorArn) == bgwTestHypA
		}},
		{TypeBackupGatewayHypervisor, bgwTestHypB, "b", "PENDING", nil, nil},
		{TypeBackupGatewayHypervisor, bgwTestHypC, "", "", nil, nil},
		{TypeBackupGatewayGateway, bgwTestGwA, "gw-a", "-", bgwTestTags[bgwTestGwA], func(a string) bool {
			var g bgwtypes.Gateway
			return json.Unmarshal([]byte(a), &g) == nil && g.GatewayType == bgwtypes.GatewayTypeBackupVm && sv(g.GatewayArn) == bgwTestGwA
		}},
		{TypeBackupGatewayGateway, bgwTestGwB, bgwTestGwB, "-", nil, nil},
		{TypeBackupGatewayVirtualMachine, bgwTestVM, "vm1", "-", bgwTestTags[bgwTestVM], func(a string) bool {
			var v bgwtypes.VirtualMachine
			return json.Unmarshal([]byte(a), &v) == nil && sv(v.Path) == "/dc/vm1" && sv(v.ResourceArn) == bgwTestVM
		}},
		{TypeBackupGatewayVirtualMachine, bgwTestVM2, bgwTestVM2, "-", nil, nil},
	} {
		t.Run(c.arn, func(t *testing.T) {
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if !ok {
				t.Fatalf("%s %s not stored", c.typ, c.arn)
			}
			if r.Name == nil || *r.Name != c.name {
				t.Errorf("Name = %v; want %q", r.Name, c.name)
			}
			switch {
			case c.status == "-" && r.Status != nil:
				t.Errorf("Status = %q; want none", *r.Status)
			case c.status != "-" && (r.Status == nil || *r.Status != c.status):
				t.Errorf("Status = %v; want %q", r.Status, c.status)
			}
			if sv(r.Region) != testRegion {
				t.Errorf("Region = %q; want %q", sv(r.Region), testRegion)
			}
			if r.Provider != "aws" || sv(r.AccountName) != newTestAccount(testAccountID).Name {
				t.Errorf("Provider, AccountName = %q, %q; want aws, %q", r.Provider, sv(r.AccountName), newTestAccount(testAccountID).Name)
			}
			if c.attr != nil && !c.attr(r.AttributesJSON) {
				t.Errorf("attributes = %s; want the listed element", r.AttributesJSON)
			}
			if c.tags == nil {
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				return
			}
			assertTagsMap(t, r.TagsJSON, c.tags)
		})
	}
}

// A rescan reports every stored row in its total, not only the rows the store
// newly wrote. On a first scan into an empty store every row is new, so only a
// rescan catches a total taken from the store's written count.
func TestScanBackupGateway_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls bgwCalls
	if _, err := bgwScan(context.Background(), st, bgwHandlers(&calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, err := bgwScan(context.Background(), st, bgwHandlers(&calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != bgwTestTotal {
		t.Errorf("rescan total = %d; want %d", total, bgwTestTotal)
	}
}

// A region with no hypervisor, gateway or virtual machine stores nothing,
// reads no tags and calls no configuration op.
func TestScanBackupGateway_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls bgwCalls
	h := bgwHandlers(&calls)
	h["ListHypervisors"] = func(any) (any, error) { return &backupgateway.ListHypervisorsOutput{}, nil }
	h["ListGateways"] = func(any) (any, error) { return &backupgateway.ListGatewaysOutput{}, nil }
	h["ListVirtualMachines"] = func(any) (any, error) { return &backupgateway.ListVirtualMachinesOutput{}, nil }
	counted, upserts := countUpserts(st)
	total, err := bgwScan(context.Background(), counted, h)
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if got := calls.tagARNs.distinct(); len(got) != 0 {
		t.Errorf("ListTagsForResource ARNs = %v; want none", got)
	}
	if n := len(calls.schedules.distinct()) + len(calls.mappings.distinct()); n != 0 {
		t.Errorf("configuration calls = %d; want 0", n)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently, and a dropped hypervisor
// or gateway has no configuration read. A denial keeps the row untagged and
// warns once under the tag op with the scan's scope; a throttle keeps it and
// is transient; any other error keeps it and is an error.
func TestScanBackupGateway_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		arn, typ   string
		err        error
		outcome    serviceOutcome // -1: no error
		code       string
		wantTotal  int
		wantKept   bool
		warn       bool
		wantConfig []string
	}{
		{"hypervisor/not found", bgwTestHypA, TypeBackupGatewayHypervisor, bgwGoneErr, -1, "", bgwTestTotal - 2, false, false, bgwConfigsWithout(bgwTestMapping)},
		{"gateway/not found", bgwTestGwB, TypeBackupGatewayGateway, bgwGoneErr, -1, "", bgwTestTotal - 2, false, false, bgwConfigsWithout(bgwTestScheduleB)},
		{"virtual machine/not found", bgwTestVM, TypeBackupGatewayVirtualMachine, bgwGoneErr, -1, "", bgwTestTotal - 1, false, false, bgwAllConfigs()},
		{"gateway/denied", bgwTestGwA, TypeBackupGatewayGateway, bgwDenied, -1, "", bgwTestTotal, true, true, bgwAllConfigs()},
		{"virtual machine/transient", bgwTestVM, TypeBackupGatewayVirtualMachine, bgwThrottle, outcomeTransient, "ThrottlingException", bgwTestTotal, true, false, bgwAllConfigs()},
		{"hypervisor/other error", bgwTestHypA, TypeBackupGatewayHypervisor, bgwBadInput, outcomeError, "InvalidRequestException", bgwTestTotal, true, false, bgwAllConfigs()},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := bgwCalls{tagErrs: map[string]error{c.arn: c.err}}
			total, err := bgwScan(context.Background(), st, bgwHandlers(&calls))
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
			if got := bgwConfigIDs(t, st); !slices.Equal(got, c.wantConfig) {
				t.Errorf("config rows = %v; want %v", got, c.wantConfig)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{backupGatewayListTagsForResourceOp}
			}
			if got := amWarnings(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads in every phase send one warning for the whole scan; the
// rows are kept.
func TestScanBackupGateway_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := bgwCalls{tagErrs: map[string]error{bgwTestHypB: bgwDenied, bgwTestGwC: bgwDenied, bgwTestVM2: bgwDenied}}
	total, err := bgwScan(context.Background(), st, bgwHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != bgwTestTotal {
		t.Errorf("total = %d; want %d", total, bgwTestTotal)
	}
	if got := amWarnings(warns()); !slices.Equal(got, []string{backupGatewayListTagsForResourceOp}) {
		t.Errorf("warnings = %v; want one under %s", got, backupGatewayListTagsForResourceOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanBackupGateway_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, backupGatewayListTagsForResourceOp)
	calls := bgwCalls{tagErrs: map[string]error{bgwTestHypC: bgwThrottle, bgwTestVM2: bgwBadInput}}
	_, err := bgwScan(context.Background(), st, bgwHandlers(&calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", backupGatewayListTagsForResourceOp, *warns)
	}
}

// A failed write stops the scan in each resource phase: no warning about the
// rows it lost (a denied tag read among them), no later phase runs, and the
// earlier phases' rows stay counted.
func TestScanBackupGateway_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ, denied string
		laterLists  []string // listings that must not be called
		wantTotal   int
	}{
		{TypeBackupGatewayHypervisor, bgwTestHypA, []string{"ListGateways", "ListVirtualMachines"}, 0},
		{TypeBackupGatewayGateway, bgwTestGwA, []string{"ListVirtualMachines"}, 3},
		{TypeBackupGatewayVirtualMachine, bgwTestVM, nil, 3 + 3},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)
			calls := bgwCalls{tagErrs: map[string]error{c.denied: bgwDenied}}
			total, err := bgwScan(context.Background(), st, bgwHandlers(&calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, op := range c.laterLists {
				if slices.Contains(calls.lists.distinct(), op) {
					t.Errorf("%s called after the failed write", op)
				}
			}
			if n := len(calls.schedules.distinct()) + len(calls.mappings.distinct()); n != 0 {
				t.Errorf("configuration calls = %d; want 0 after the failed write", n)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// Each listing writes a page before it reads the next: the first page's row
// is stored when the second page is requested.
func TestScanBackupGateway_WritesEachPageBeforeTheNext(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls bgwCalls
	h := bgwHandlers(&calls)
	pageOne := map[string]string{"ListHypervisors": bgwTestHypA, "ListGateways": bgwTestGwA, "ListVirtualMachines": bgwTestVM}
	lookups := map[string]error{} // per listing: GetResource of its page-1 row, when page 2 was requested
	for op, arn := range pageOne {
		list := h[op]
		h[op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				_, lookups[op] = st.GetResource(store.ResourceID("aws", testAccountID, arn))
			}
			return list(in)
		}
	}
	if _, err := bgwScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for op, arn := range pageOne {
		err, asked := lookups[op]
		if !asked {
			t.Errorf("%s: page 2 never requested", op)
			continue
		}
		if err != nil {
			t.Errorf("%s: %s not stored before page 2 was read: %v", op, arn, err)
		}
	}
}

// A repeated NextToken ends each listing as an error, keeping its pages; the
// later phases still run.
func TestScanBackupGateway_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"ListHypervisors", "ListGateways", "ListVirtualMachines"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var calls bgwCalls
			h := bgwHandlers(&calls)
			twoPages := h[op]
			h[op] = func(in any) (any, error) {
				out, err := twoPages(in)
				switch o := out.(type) {
				case *backupgateway.ListHypervisorsOutput:
					o.NextToken = sdkaws.String("same")
				case *backupgateway.ListGatewaysOutput:
					o.NextToken = sdkaws.String("same")
				case *backupgateway.ListVirtualMachinesOutput:
					o.NextToken = sdkaws.String("same")
				}
				return out, err
			}
			total, err := scanBackupGatewayTopLevel(t, st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != bgwTestTotal {
				t.Errorf("total = %d; want %d", total, bgwTestTotal)
			}
			if got := bgwStoredIDs(t, st); !slices.Equal(got, bgwTaggedIDs()) {
				t.Errorf("stored = %v; want %v", got, bgwTaggedIDs())
			}
		})
	}
}

// A listed element without an ARN is not stored, and each listing warns once
// under its op; its siblings are stored and have their tags read.
func TestScanBackupGateway_ElementWithoutARN(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls bgwCalls
	h := bgwHandlers(&calls)
	h["ListHypervisors"] = func(any) (any, error) {
		return &backupgateway.ListHypervisorsOutput{Hypervisors: []bgwtypes.Hypervisor{{Name: sdkaws.String("no-arn")}, {HypervisorArn: &bgwTestHypA}}}, nil
	}
	h["ListGateways"] = func(any) (any, error) {
		return &backupgateway.ListGatewaysOutput{Gateways: []bgwtypes.Gateway{{GatewayDisplayName: sdkaws.String("no-arn")}, {}, {GatewayArn: &bgwTestGwA}}}, nil
	}
	h["ListVirtualMachines"] = func(any) (any, error) {
		return &backupgateway.ListVirtualMachinesOutput{VirtualMachines: []bgwtypes.VirtualMachine{{}, {ResourceArn: &bgwTestVM}}}, nil
	}
	if _, err := bgwScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := bgwStoredIDs(t, st), bgwSorted(bgwTestHypA, bgwTestGwA, bgwTestVM); !slices.Equal(got, want) {
		t.Errorf("stored = %v; want %v", got, want)
	}
	if got, want := calls.tagARNs.distinct(), bgwSorted(bgwTestHypA, bgwTestGwA, bgwTestVM); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	w := warns()
	if got, want := amWarnings(w), []string{backupGatewayListHypervisorsOp, backupGatewayListGatewaysOp, backupGatewayListVirtualMachinesOp}; !slices.Equal(got, want) {
		t.Fatalf("warnings = %v; want %v", w, want)
	}
}

// A GovCloud or China ARN reaches the tag op unchanged.
func TestScanBackupGateway_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			arns := map[string]string{
				"ListHypervisors":     partitionARN(pf.partition, "backup-gateway", pf.region, "hypervisor/hype-gov"),
				"ListGateways":        partitionARN(pf.partition, "backup-gateway", pf.region, "gateway/bgw-gov"),
				"ListVirtualMachines": partitionARN(pf.partition, "backup-gateway", pf.region, "vm/vm-gov"),
			}
			calls := bgwCalls{tags: map[string]map[string]string{}}
			for _, arn := range arns {
				calls.tags[arn] = map[string]string{"env": pf.partition}
			}
			h := bgwHandlers(&calls)
			h["ListHypervisors"] = func(any) (any, error) {
				return &backupgateway.ListHypervisorsOutput{Hypervisors: []bgwtypes.Hypervisor{{HypervisorArn: sdkaws.String(arns["ListHypervisors"])}}}, nil
			}
			h["ListGateways"] = func(any) (any, error) {
				return &backupgateway.ListGatewaysOutput{Gateways: []bgwtypes.Gateway{{GatewayArn: sdkaws.String(arns["ListGateways"])}}}, nil
			}
			h["ListVirtualMachines"] = func(any) (any, error) {
				return &backupgateway.ListVirtualMachinesOutput{VirtualMachines: []bgwtypes.VirtualMachine{{ResourceArn: sdkaws.String(arns["ListVirtualMachines"])}}}, nil
			}
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(h, bgwDenied)
			if _, err := scanBackupGateway(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := bgwSorted(arns["ListHypervisors"], arns["ListGateways"], arns["ListVirtualMachines"])
			if got := calls.tagARNs.distinct(); !slices.Equal(got, want) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			for typ, arn := range map[string]string{
				TypeBackupGatewayHypervisor:     arns["ListHypervisors"],
				TypeBackupGatewayGateway:        arns["ListGateways"],
				TypeBackupGatewayVirtualMachine: arns["ListVirtualMachines"],
			} {
				assertTagsMap(t, rowsByNativeID(t, st, typ)[arn].TagsJSON, map[string]string{"env": pf.partition})
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanBackupGateway_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls bgwCalls
	h := bgwHandlers(&calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListGateways"]
	h["ListGateways"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}
	_, err := bgwScan(ctx, st, h)
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
	if slices.Contains(calls.lists.distinct(), "ListVirtualMachines") {
		t.Error("ListVirtualMachines called after ctx ended")
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanBackupGateway_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "backup-gateway.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		var calls bgwCalls
		h := bgwHandlers(&calls)
		h["ListVirtualMachines"] = func(any) (any, error) { return nil, nx }
		total, err := bgwScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if want := bgwTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := bgwCalls{tagErrs: map[string]error{bgwTestGwC: nx}}
		_, err := bgwScan(context.Background(), st, bgwHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, err := scanBackupGateway(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
