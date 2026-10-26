package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/deadline"
	dltypes "github.com/aws/aws-sdk-go-v2/service/deadline/types"
	"github.com/icearp/disco-cli/store"
)

const dlTestStore = "d-1234567890"

var (
	dlTestFarmA   = deadlineFarmARN("aws", testRegion, testAccountID, "farm-a")
	dlTestFarmB   = deadlineFarmARN("aws", testRegion, testAccountID, "farm-b")
	dlTestFleetA  = dlTestFarmA + "/fleet/fleet-a"
	dlTestFleetB  = dlTestFarmB + "/fleet/fleet-b"
	dlTestQueueA  = dlTestFarmA + "/queue/queue-a"
	dlTestQueueB  = dlTestFarmB + "/queue/queue-b"
	dlTestMonitor = "arn:aws:deadline:us-east-1:123456789012:monitor/monitor-1"

	dlTestFarmMembers = []string{
		dlTestFarmA + "/member/GROUP/g-1",
		dlTestFarmA + "/member/USER/u-1",
		dlTestFarmB + "/member/USER/u-2",
	}
	dlTestFleetMembers = []string{
		dlTestFleetA + "/member/GROUP/g-1",
		dlTestFleetA + "/member/USER/u-1",
		dlTestFleetB + "/member/USER/u-2",
	}
	dlTestQueueMembers = []string{
		dlTestQueueA + "/member/GROUP/g-1",
		dlTestQueueA + "/member/USER/u-1",
		dlTestQueueB + "/member/USER/u-2",
	}

	dlDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: deadline:List")
	dlBadInput = apiErr("ValidationException", "bad")
	dlThrottle = apiErr("ThrottlingException", "slow down")
	dlGone     = apiErr("ResourceNotFoundException", "gone")
)

// dlMemberCalls records each member list call as op farmId/childId (farmId
// alone for ListFarmMembers).
type dlMemberCalls struct{ farm, fleet, queue parentCalls }

// dlMemberPages answers one member list call for key: the first parent of
// each op ("farm-a", "farm-a/fleet-a", "farm-a/queue-a") lists two pages, the
// second one page; any other key is an error, so a member call made with the
// wrong farm id fails the scan.
func dlMemberPages[M any](key string, next *string, first, second []M, other []M) ([]M, *string, error) {
	switch key {
	case "farm-a", "farm-a/fleet-a", "farm-a/queue-a":
		if next == nil {
			return first, sdkaws.String("page2"), nil
		}
		return second, nil, nil
	case "farm-b", "farm-b/fleet-b", "farm-b/queue-b":
		return other, nil, nil
	}
	return nil, nil, apiErr("ValidationException", "unknown parent "+key)
}

// deadlineHandlers answers every op scanDeadline calls: farms farm-a and
// farm-b with one fleet, one queue each, one monitor, and members on two pages
// for each op's farm-a parent (the first page also carries an element without
// a principal id, to skip) and one page for the farm-b parent.
func deadlineHandlers(calls *dlMemberCalls) map[string]func(in any) (any, error) {
	user := dltypes.DeadlinePrincipalTypeUser
	group := dltypes.DeadlinePrincipalTypeGroup
	checkLimit := func(maxResults *int32) error {
		if sdkaws.ToInt32(maxResults) != 100 {
			return apiErr("ValidationException", "maxResults must be between 1 and 100")
		}
		return nil
	}
	return map[string]func(in any) (any, error){
		"ListTagsForResource": func(any) (any, error) { return &deadline.ListTagsForResourceOutput{}, nil },
		"ListFarms": func(any) (any, error) {
			return &deadline.ListFarmsOutput{Farms: []dltypes.FarmSummary{
				{FarmId: sdkaws.String("farm-a"), DisplayName: sdkaws.String("A")},
				{FarmId: sdkaws.String("farm-b"), DisplayName: sdkaws.String("B")},
			}}, nil
		},
		"ListFleets": func(in any) (any, error) {
			farm := sdkaws.ToString(in.(*deadline.ListFleetsInput).FarmId)
			return &deadline.ListFleetsOutput{Fleets: []dltypes.FleetSummary{{FleetId: sdkaws.String("fleet-" + farm[len("farm-"):])}}}, nil
		},
		"ListQueues": func(in any) (any, error) {
			farm := sdkaws.ToString(in.(*deadline.ListQueuesInput).FarmId)
			return &deadline.ListQueuesOutput{Queues: []dltypes.QueueSummary{{QueueId: sdkaws.String("queue-" + farm[len("farm-"):])}}}, nil
		},
		"ListLimits":          func(any) (any, error) { return &deadline.ListLimitsOutput{}, nil },
		"ListStorageProfiles": func(any) (any, error) { return &deadline.ListStorageProfilesOutput{}, nil },
		"ListBudgets":         func(any) (any, error) { return &deadline.ListBudgetsOutput{}, nil },
		"ListQueueFleetAssociations": func(any) (any, error) {
			return &deadline.ListQueueFleetAssociationsOutput{}, nil
		},
		"ListQueueLimitAssociations": func(any) (any, error) {
			return &deadline.ListQueueLimitAssociationsOutput{}, nil
		},
		"ListQueueEnvironments": func(any) (any, error) { return &deadline.ListQueueEnvironmentsOutput{}, nil },
		"ListVolumes":           func(any) (any, error) { return &deadline.ListVolumesOutput{}, nil },
		"ListLicenseEndpoints":  func(any) (any, error) { return &deadline.ListLicenseEndpointsOutput{}, nil },
		"ListMonitors": func(any) (any, error) {
			return &deadline.ListMonitorsOutput{Monitors: []dltypes.MonitorSummary{{MonitorId: sdkaws.String("monitor-1")}}}, nil
		},
		"ListFarmMembers": func(in any) (any, error) {
			p := in.(*deadline.ListFarmMembersInput)
			farm := sdkaws.ToString(p.FarmId)
			calls.farm.add(farm)
			if err := checkLimit(p.MaxResults); err != nil {
				return nil, err
			}
			m := func(pid string, pt dltypes.DeadlinePrincipalType, lvl dltypes.MembershipLevel) dltypes.FarmMember {
				return dltypes.FarmMember{FarmId: &farm, IdentityStoreId: sdkaws.String(dlTestStore), PrincipalId: sdkaws.String(pid), PrincipalType: pt, MembershipLevel: lvl}
			}
			members, next, err := dlMemberPages(farm, p.NextToken,
				[]dltypes.FarmMember{m("u-1", user, dltypes.MembershipLevelOwner), {FarmId: &farm, PrincipalType: user}},
				[]dltypes.FarmMember{m("g-1", group, dltypes.MembershipLevelViewer)},
				[]dltypes.FarmMember{m("u-2", user, dltypes.MembershipLevelManager)})
			if err != nil {
				return nil, err
			}
			return &deadline.ListFarmMembersOutput{Members: members, NextToken: next}, nil
		},
		"ListFleetMembers": func(in any) (any, error) {
			p := in.(*deadline.ListFleetMembersInput)
			farm, fleet := sdkaws.ToString(p.FarmId), sdkaws.ToString(p.FleetId)
			calls.fleet.add(farm + "/" + fleet)
			if err := checkLimit(p.MaxResults); err != nil {
				return nil, err
			}
			m := func(pid string, pt dltypes.DeadlinePrincipalType, lvl dltypes.MembershipLevel) dltypes.FleetMember {
				return dltypes.FleetMember{FarmId: &farm, FleetId: &fleet, IdentityStoreId: sdkaws.String(dlTestStore), PrincipalId: sdkaws.String(pid), PrincipalType: pt, MembershipLevel: lvl}
			}
			members, next, err := dlMemberPages(farm+"/"+fleet, p.NextToken,
				[]dltypes.FleetMember{m("u-1", user, dltypes.MembershipLevelContributor), {FarmId: &farm, PrincipalType: user}},
				[]dltypes.FleetMember{m("g-1", group, dltypes.MembershipLevelViewer)},
				[]dltypes.FleetMember{m("u-2", user, dltypes.MembershipLevelOwner)})
			if err != nil {
				return nil, err
			}
			return &deadline.ListFleetMembersOutput{Members: members, NextToken: next}, nil
		},
		"ListQueueMembers": func(in any) (any, error) {
			p := in.(*deadline.ListQueueMembersInput)
			farm, queue := sdkaws.ToString(p.FarmId), sdkaws.ToString(p.QueueId)
			calls.queue.add(farm + "/" + queue)
			if err := checkLimit(p.MaxResults); err != nil {
				return nil, err
			}
			m := func(pid string, pt dltypes.DeadlinePrincipalType, lvl dltypes.MembershipLevel) dltypes.QueueMember {
				return dltypes.QueueMember{FarmId: &farm, QueueId: &queue, IdentityStoreId: sdkaws.String(dlTestStore), PrincipalId: sdkaws.String(pid), PrincipalType: pt, MembershipLevel: lvl}
			}
			members, next, err := dlMemberPages(farm+"/"+queue, p.NextToken,
				[]dltypes.QueueMember{m("g-1", group, dltypes.MembershipLevelManager), {FarmId: &farm, PrincipalType: group}},
				[]dltypes.QueueMember{m("u-1", user, dltypes.MembershipLevelViewer)},
				[]dltypes.QueueMember{m("u-2", user, dltypes.MembershipLevelContributor)})
			if err != nil {
				return nil, err
			}
			return &deadline.ListQueueMembersOutput{Members: members, NextToken: next}, nil
		},
	}
}

func scanDeadlineTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, dlDenied)
	return scanDeadline(context.Background(), acct, testRegion, st, testScanID)
}

func TestScanDeadline_StoresMembers(t *testing.T) {
	st := newTestStore(t)
	warnings := map[string]*[]store.ScanWarning{}
	for _, op := range []string{deadlineListFarmMembersOp, deadlineListFleetMembersOp, deadlineListQueueMembersOp} {
		warnings[op] = bacWarnings(st, op)
	}
	var calls dlMemberCalls
	total, inserted, err := scanDeadlineTopLevel(t, st, deadlineHandlers(&calls))
	if err != nil {
		t.Fatalf("scanDeadline: %v", err)
	}
	// 2 farms + 2 fleets + 2 queues + 1 monitor + 3 members of each kind
	if total != 16 || inserted != 16 {
		t.Errorf("total, inserted = %d, %d, want 16, 16", total, inserted)
	}
	// Each op's farm-a parent lists one element without a principal id: it
	// is not stored, and one warning names it under its parent.
	for op, parent := range map[string]string{
		deadlineListFarmMembersOp: dlTestFarmA, deadlineListFleetMembersOp: dlTestFleetA, deadlineListQueueMembersOp: dlTestQueueA,
	} {
		ws := *warnings[op]
		if len(ws) != 1 || !strings.HasSuffix(ws[0].Message, "first (a member with no principal id) of "+parent) {
			t.Errorf("%s warnings = %v; want one naming the member without a principal id of %s", op, ws, parent)
		}
	}
	for _, tc := range []struct {
		name  string
		calls *parentCalls
		want  []string
	}{
		{"ListFarmMembers", &calls.farm, []string{"farm-a", "farm-b"}},
		{"ListFleetMembers", &calls.fleet, []string{"farm-a/fleet-a", "farm-b/fleet-b"}},
		{"ListQueueMembers", &calls.queue, []string{"farm-a/queue-a", "farm-b/queue-b"}},
	} {
		if got := tc.calls.distinct(); !slices.Equal(got, tc.want) {
			t.Errorf("%s parents = %v, want %v", tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct {
		typ     string
		parents []string
		want    []string
		level   string
	}{
		{TypeDeadlineFarmMember, []string{dlTestFarmA, dlTestFarmA, dlTestFarmB}, dlTestFarmMembers, "OWNER"},
		{TypeDeadlineFleetMember, []string{dlTestFleetA, dlTestFleetA, dlTestFleetB}, dlTestFleetMembers, "CONTRIBUTOR"},
		{TypeDeadlineQueueMember, []string{dlTestQueueA, dlTestQueueA, dlTestQueueB}, dlTestQueueMembers, "VIEWER"},
	} {
		rows := rowsByNativeID(t, st, tc.typ)
		if got := nativeIDs(rows); !slices.Equal(got, tc.want) {
			t.Errorf("%s rows = %v, want %v", tc.typ, got, tc.want)
			continue
		}
		for i, id := range tc.want {
			assertContainsNative(t, st, tc.parents[i], id)
		}
		r := rows[tc.want[1]] // the USER/u-1 member
		if r.Type != tc.typ || sv(r.Region) != testRegion || sv(r.Name) != "u-1" {
			t.Errorf("%s row = type %q region %q name %q", tc.typ, r.Type, sv(r.Region), sv(r.Name))
		}
		var attrs struct {
			IdentityStoreID string `json:"IdentityStoreId"`
			MembershipLevel string `json:"MembershipLevel"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("%s attrs: %v", tc.typ, err)
		}
		if attrs.IdentityStoreID != dlTestStore || attrs.MembershipLevel != tc.level {
			t.Errorf("%s attrs = %s, want the SDK member verbatim (store %s, level %s)", tc.typ, r.AttributesJSON, dlTestStore, tc.level)
		}
	}
}

func TestScanDeadline_NoMembers(t *testing.T) {
	st := newTestStore(t)
	var calls dlMemberCalls
	h := deadlineHandlers(&calls)
	h["ListFarmMembers"] = func(any) (any, error) { return &deadline.ListFarmMembersOutput{}, nil }
	h["ListFleetMembers"] = func(any) (any, error) { return &deadline.ListFleetMembersOutput{}, nil }
	h["ListQueueMembers"] = func(any) (any, error) { return &deadline.ListQueueMembersOutput{}, nil }
	total, _, err := scanDeadlineTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanDeadline: %v", err)
	}
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
	for _, typ := range []string{TypeDeadlineFarmMember, TypeDeadlineFleetMember, TypeDeadlineQueueMember} {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
}

// A parent deleted since listing, denied, or failing transiently (throttle or 5xx) after
// retries drops only its own members: deleted is silent, a denial warns once
// however many parents hit it, and a transient error warns once. A fleet the
// service refuses with ValidationException is skipped with one warning; for a
// farm or queue that code, like any other error, is returned after the
// sibling's members are stored. Every older phase's rows stay, the last
// one's (monitors) included.
func TestScanDeadline_FailedMemberParent(t *testing.T) {
	type failCase struct {
		name      string
		fail      map[string]error // keyed as dlMemberPages keys
		wantCode  string           // "" means the scan returns nil
		wantRows  []string         // NativeIDs of the op type stored
		wantWarns int
	}
	for _, op := range []struct {
		op, typ, key, otherKey string
		rows                   []string
		badInputSkipped        bool
	}{
		{"ListFarmMembers", TypeDeadlineFarmMember, "farm-a", "farm-b", dlTestFarmMembers, false},
		{"ListFleetMembers", TypeDeadlineFleetMember, "farm-a/fleet-a", "farm-b/fleet-b", dlTestFleetMembers, true},
		{"ListQueueMembers", TypeDeadlineQueueMember, "farm-a/queue-a", "farm-b/queue-b", dlTestQueueMembers, false},
	} {
		onlyB := []string{op.rows[2]}
		cases := []failCase{
			{"parent gone", map[string]error{op.key: dlGone}, "", onlyB, 0},
			{"denied everywhere", map[string]error{op.key: dlDenied, op.otherKey: dlDenied}, "", nil, 1},
			{"throttled everywhere", map[string]error{op.key: dlThrottle, op.otherKey: dlThrottle}, "", nil, 1},
			{"throttled once", map[string]error{op.key: dlThrottle}, "", onlyB, 1},
			{"server error once", map[string]error{op.key: apiErr("InternalServerErrorException", "x")}, "", onlyB, 1},
			{"unmodeled code", map[string]error{op.key: apiErr("ConflictException", "x")}, "ConflictException", onlyB, 0},
		}
		if op.badInputSkipped {
			cases = append(cases, failCase{"validation everywhere", map[string]error{op.key: dlBadInput, op.otherKey: dlBadInput}, "", nil, 1})
		} else {
			cases = append(cases, failCase{"validation", map[string]error{op.key: dlBadInput}, "ValidationException", onlyB, 0})
		}
		for _, tc := range cases {
			t.Run(op.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "deadline:"+op.op)
				var calls dlMemberCalls
				h := deadlineHandlers(&calls)
				next := h[op.op]
				h[op.op] = func(in any) (any, error) {
					if err, ok := tc.fail[dlMemberKey(in)]; ok {
						return nil, err
					}
					return next(in)
				}
				_, _, err := scanDeadlineTopLevel(t, st, h)
				if tc.wantCode == "" {
					if err != nil {
						t.Fatalf("err = %v, want nil", err)
					}
				} else {
					assertAPICode(t, err, tc.wantCode)
					if got := classifyServiceError(err); got != outcomeError {
						t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
					}
				}
				if got := nativeIDs(rowsByNativeID(t, st, op.typ)); !slices.Equal(got, tc.wantRows) {
					t.Errorf("%s rows = %v, want %v", op.typ, got, tc.wantRows)
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
				}
				for _, older := range []struct{ typ, id string }{
					{TypeDeadlineFarm, dlTestFarmB},
					{TypeDeadlineFleet, dlTestFleetB},
					{TypeDeadlineQueue, dlTestQueueB},
					{TypeDeadlineMonitor, dlTestMonitor},
				} {
					if _, ok := rowsByNativeID(t, st, older.typ)[older.id]; !ok {
						t.Errorf("%s %s missing; a member phase must not stop the older phases", older.typ, older.id)
					}
				}
			})
		}
	}
}

// A hard error in the last older phase still lets the member phases run over
// the farms, fleets and queues already stored, and is returned.
func TestScanDeadline_OlderPhaseErrorStillRunsMembers(t *testing.T) {
	st := newTestStore(t)
	var calls dlMemberCalls
	h := deadlineHandlers(&calls)
	h["ListMonitors"] = func(any) (any, error) { return nil, dlBadInput }
	total, _, err := scanDeadlineTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	for typ, want := range map[string][]string{
		TypeDeadlineFarmMember:  dlTestFarmMembers,
		TypeDeadlineFleetMember: dlTestFleetMembers,
		TypeDeadlineQueueMember: dlTestQueueMembers,
	} {
		if got := nativeIDs(rowsByNativeID(t, st, typ)); !slices.Equal(got, want) {
			t.Errorf("%s rows = %v, want %v", typ, got, want)
		}
	}
	// 2 farms + 2 fleets + 2 queues + 9 members
	if total != 15 {
		t.Errorf("total = %d, want 15", total)
	}
}

// A transient error in an older phase is demoted to a warning under its op
// so that a member phase's hard error still reaches the dispatcher as a hard
// error.
func TestScanDeadline_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "deadline:ListMonitors")
	var calls dlMemberCalls
	h := deadlineHandlers(&calls)
	h["ListMonitors"] = func(any) (any, error) { return nil, dlThrottle }
	h["ListQueueMembers"] = func(any) (any, error) { return nil, dlBadInput }
	_, _, err := scanDeadlineTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError; the hard member error must not be masked", err, got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

// recordChildListings makes h record each ListQueueEnvironments and
// ListVolumes call as farmId/queueId or farmId/fleetId.
func recordChildListings(h map[string]func(in any) (any, error), envs, volumes *parentCalls) {
	nextEnv, nextVol := h["ListQueueEnvironments"], h["ListVolumes"]
	h["ListQueueEnvironments"] = func(in any) (any, error) {
		p := in.(*deadline.ListQueueEnvironmentsInput)
		envs.add(sdkaws.ToString(p.FarmId) + "/" + sdkaws.ToString(p.QueueId))
		return nextEnv(in)
	}
	h["ListVolumes"] = func(in any) (any, error) {
		p := in.(*deadline.ListVolumesInput)
		volumes.add(sdkaws.ToString(p.FarmId) + "/" + sdkaws.ToString(p.FleetId))
		return nextVol(in)
	}
}

// A queue or fleet listing denied part-way keeps the children read before
// the denial, with one warning, so their members, environments and volumes
// are listed with those of the other farm's children.
func TestScanDeadline_MembersOfPartlyListedParents(t *testing.T) {
	for _, tc := range []struct {
		listOp, memberOp, childOp string
		page2                     func(in any) (any, bool)
		calls                     func(*dlMemberCalls) *parentCalls
		children                  func(envs, volumes *parentCalls) *parentCalls
		want                      []string
	}{
		{
			"ListQueues", "ListQueueMembers", "ListQueueEnvironments",
			func(in any) (any, bool) {
				return &deadline.ListQueuesOutput{Queues: []dltypes.QueueSummary{{QueueId: sdkaws.String("queue-a")}}, NextToken: sdkaws.String("p2")}, in.(*deadline.ListQueuesInput).NextToken == nil
			},
			func(c *dlMemberCalls) *parentCalls { return &c.queue },
			func(envs, _ *parentCalls) *parentCalls { return envs },
			[]string{"farm-a/queue-a", "farm-b/queue-b"},
		},
		{
			"ListFleets", "ListFleetMembers", "ListVolumes",
			func(in any) (any, bool) {
				return &deadline.ListFleetsOutput{Fleets: []dltypes.FleetSummary{{FleetId: sdkaws.String("fleet-a")}}, NextToken: sdkaws.String("p2")}, in.(*deadline.ListFleetsInput).NextToken == nil
			},
			func(c *dlMemberCalls) *parentCalls { return &c.fleet },
			func(_, volumes *parentCalls) *parentCalls { return volumes },
			[]string{"farm-a/fleet-a", "farm-b/fleet-b"},
		},
	} {
		t.Run(tc.listOp, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "deadline:"+tc.listOp)
			var calls dlMemberCalls
			var envs, volumes parentCalls
			h := deadlineHandlers(&calls)
			recordChildListings(h, &envs, &volumes)
			next := h[tc.listOp]
			h[tc.listOp] = func(in any) (any, error) {
				// farm-a lists one child, then is denied on page 2.
				var farm string
				switch p := in.(type) {
				case *deadline.ListQueuesInput:
					farm = sdkaws.ToString(p.FarmId)
				case *deadline.ListFleetsInput:
					farm = sdkaws.ToString(p.FarmId)
				}
				if farm != "farm-a" {
					return next(in)
				}
				if out, first := tc.page2(in); first {
					return out, nil
				}
				return nil, dlDenied
			}
			if _, _, err := scanDeadlineTopLevel(t, st, h); err != nil {
				t.Fatalf("scanDeadline: %v", err)
			}
			if got := tc.calls(&calls).distinct(); !slices.Equal(got, tc.want) {
				t.Errorf("%s parents = %v, want the stored %v", tc.memberOp, got, tc.want)
			}
			if got := tc.children(&envs, &volumes).distinct(); !slices.Equal(got, tc.want) {
				t.Errorf("%s parents = %v, want the stored %v", tc.childOp, got, tc.want)
			}
			if *warns != 1 {
				t.Errorf("%s warnings = %d, want 1 for the denied page", tc.listOp, *warns)
			}
		})
	}
}

// Fleets or queues whose write failed are not stored, so their members are
// not listed. The store is closed while farm-a's children are listed, which
// fails that write and every later one; the write failure is returned.
func TestScanDeadline_NoMembersOfUnwrittenParents(t *testing.T) {
	for _, tc := range []struct {
		listOp string
		calls  func(*dlMemberCalls) *parentCalls
	}{
		{"ListFleets", func(c *dlMemberCalls) *parentCalls { return &c.fleet }},
		{"ListQueues", func(c *dlMemberCalls) *parentCalls { return &c.queue }},
	} {
		t.Run(tc.listOp, func(t *testing.T) {
			// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
			st, err := store.Open(filepath.Join(t.TempDir(), "closing.db"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })
			insertTestScan(t, st, testScanID, "running")
			var calls dlMemberCalls
			h := deadlineHandlers(&calls)
			next := h[tc.listOp]
			h[tc.listOp] = func(in any) (any, error) {
				if err := st.Close(); err != nil {
					t.Errorf("close: %v", err)
				}
				return next(in)
			}
			_, _, err = scanDeadlineTopLevel(t, st, h)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Errorf("err = %v, want store.ErrStoreWrite", err)
			}
			if got := tc.calls(&calls).distinct(); len(got) != 0 {
				t.Errorf("%s member parents = %v, want none: no write of them succeeded", tc.listOp, got)
			}
		})
	}
}

// dlMemberKey is the dlMemberPages key of a member list call's input.
func dlMemberKey(in any) string {
	switch p := in.(type) {
	case *deadline.ListFarmMembersInput:
		return sdkaws.ToString(p.FarmId)
	case *deadline.ListFleetMembersInput:
		return sdkaws.ToString(p.FarmId) + "/" + sdkaws.ToString(p.FleetId)
	case *deadline.ListQueueMembersInput:
		return sdkaws.ToString(p.FarmId) + "/" + sdkaws.ToString(p.QueueId)
	}
	return ""
}

// dlFirstPageInput is a copy of a member list call's input with no NextToken.
func dlFirstPageInput(in any) any {
	switch p := in.(type) {
	case *deadline.ListFarmMembersInput:
		c := *p
		c.NextToken = nil
		return &c
	case *deadline.ListFleetMembersInput:
		c := *p
		c.NextToken = nil
		return &c
	case *deadline.ListQueueMembersInput:
		c := *p
		c.NextToken = nil
		return &c
	}
	return in
}

// dlMemberOp is a member op with its row type, the keys of its two parents,
// its NativeIDs (dlTest*Members) and the one the key parent lists on its
// first page.
type dlMemberOp struct {
	op, typ, key, otherKey string
	rows                   []string
	firstPage              string
}

var dlMemberOps = []dlMemberOp{
	{"ListFarmMembers", TypeDeadlineFarmMember, "farm-a", "farm-b", dlTestFarmMembers, dlTestFarmMembers[1]},
	{"ListFleetMembers", TypeDeadlineFleetMember, "farm-a/fleet-a", "farm-b/fleet-b", dlTestFleetMembers, dlTestFleetMembers[1]},
	{"ListQueueMembers", TypeDeadlineQueueMember, "farm-a/queue-a", "farm-b/queue-b", dlTestQueueMembers, dlTestQueueMembers[0]},
}

// A member listing that ends on its second page keeps the members of the
// first and the sibling parent's, and returns why it ended: a failed page as
// that error, a page that repeats its NextToken as errRepeatedNextToken.
func TestScanDeadline_MemberListingEndedEarlyKeepsPages(t *testing.T) {
	for _, op := range dlMemberOps {
		for _, tc := range []struct {
			name string
			page func(in any, next func(any) (any, error)) (any, error)
			want func(t *testing.T, err error)
		}{
			{
				"failed page",
				func(in any, next func(any) (any, error)) (any, error) {
					if bacField[*string](in, "NextToken") != nil {
						return nil, apiErr("ConflictException", "page 2")
					}
					return next(in)
				},
				func(t *testing.T, err error) { t.Helper(); assertAPICode(t, err, "ConflictException") },
			},
			{
				"repeated token",
				func(in any, next func(any) (any, error)) (any, error) { return next(dlFirstPageInput(in)) },
				func(t *testing.T, err error) {
					t.Helper()
					if !errors.Is(err, errRepeatedNextToken) {
						t.Errorf("err = %v, want errRepeatedNextToken", err)
					}
				},
			},
		} {
			t.Run(op.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				var calls dlMemberCalls
				h := deadlineHandlers(&calls)
				next := h[op.op]
				h[op.op] = func(in any) (any, error) {
					if dlMemberKey(in) == op.key {
						return tc.page(in, next)
					}
					return next(in)
				}
				_, _, err := scanDeadlineTopLevel(t, st, h)
				tc.want(t, err)
				want := []string{op.firstPage, op.rows[2]}
				slices.Sort(want)
				if got := nativeIDs(rowsByNativeID(t, st, op.typ)); !slices.Equal(got, want) {
					t.Errorf("%s rows = %v, want %v", op.typ, got, want)
				}
			})
		}
	}
}

// A failed member write sends none of the warnings about the parents the
// phase skipped: not the denial, the transient skip, the fleet not-ready skip
// or the not-stored warning about the member with no principal id.
func TestScanDeadline_MemberWriteFailureSendsNoWarning(t *testing.T) {
	for _, op := range dlMemberOps {
		// The other parent lists its member, so the phase has a row to write.
		fails := map[string]error{"denied": dlDenied, "throttled": dlThrottle}
		if op.op == "ListFleetMembers" {
			fails["not ready"] = dlBadInput
		}
		for name, fail := range fails {
			t.Run(op.op+"/"+name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "deadline:"+op.op)
				refuseStoreWrites(t, st, op.typ)
				var calls dlMemberCalls
				h := deadlineHandlers(&calls)
				next := h[op.op]
				h[op.op] = func(in any) (any, error) {
					if dlMemberKey(in) == op.key {
						return nil, fail
					}
					return next(in)
				}
				_, _, err := scanDeadlineTopLevel(t, st, h)
				if !errors.Is(err, store.ErrStoreWrite) {
					t.Fatalf("err = %v, want store.ErrStoreWrite", err)
				}
				if *warns != 0 {
					t.Errorf("warnings = %d, want 0 after the failed write", *warns)
				}
			})
		}
	}
}

// A member listing's NXDOMAIN is returned, never skipped as transient: after
// the older phases stored rows the scan fails (resultLateNXDOMAIN); a member
// phase that stored nothing returns the NXDOMAIN as is, the region gap.
func TestScanDeadline_MemberNXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "management.deadline.us-east-1.amazonaws.com", IsNotFound: true}
	for _, op := range dlMemberOps {
		t.Run(op.op+"/rows stored", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "deadline:"+op.op)
			var calls dlMemberCalls
			h := deadlineHandlers(&calls)
			next := h[op.op]
			h[op.op] = func(in any) (any, error) {
				if dlMemberKey(in) == op.key {
					return nil, nx
				}
				return next(in)
			}
			total, _, err := scanDeadlineTopLevel(t, st, h)
			assertGlueOutcome(t, err, outcomeError)
			if total == 0 {
				t.Error("total = 0; want the stored rows counted")
			}
			if *warns != 0 {
				t.Errorf("warnings = %d, want 0: the NXDOMAIN is returned, not skipped", *warns)
			}
		})
	}
	farms := []dlFarmRef{{
		id: "farm-a", arn: dlTestFarmA,
		fleets: []childParent{{id: "fleet-a", arn: dlTestFleetA}},
		queues: []childParent{{id: "queue-a", arn: dlTestQueueA}},
	}}
	for _, phase := range []struct {
		op  string
		run func(context.Context, deadlineAPI, *account, string, *store.Store, string, []dlFarmRef) (int, int, error)
	}{
		{"ListFarmMembers", scanDeadlineFarmMembers},
		{"ListFleetMembers", scanDeadlineFleetMembers},
		{"ListQueueMembers", scanDeadlineQueueMembers},
	} {
		t.Run(phase.op+"/nothing stored", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "deadline:"+phase.op)
			client := deadline.NewFromConfig(opStubConfig(nil, nx))
			total, _, err := phase.run(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, farms)
			assertGlueOutcome(t, err, outcomeUnavailable)
			if total != 0 {
				t.Errorf("total = %d, want 0", total)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d, want 0", *warns)
			}
		})
	}
}
