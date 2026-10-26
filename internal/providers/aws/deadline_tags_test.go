package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/deadline"
	dltypes "github.com/aws/aws-sdk-go-v2/service/deadline/types"
	"github.com/icearp/disco-cli/store"
)

// dlRow is one fixture row: its type and NativeID.
type dlRow struct{ typ, arn string }

// dlNames is the two resources each listing of the fixture serves, "tagged"
// on its first page and "bare" on its second; ListTagsForResource answers
// with tags for every resource whose own id is "tagged".
var dlNames = []string{"tagged", "bare"}

func dlFarm(name string) string { return deadlineFarmARN("aws", testRegion, testAccountID, name) }

func dlLicenseEndpoint(name string) string {
	return deadlineLicenseEndpointARN("aws", testRegion, testAccountID, name)
}

// dlTaggedRows is every row of a type whose tags are read: two farms, each
// with two fleets, queues and budgets, two volumes per fleet, two license
// endpoints and two monitors.
func dlTaggedRows() []dlRow {
	var rows []dlRow
	for _, f := range dlNames {
		farm := dlFarm(f)
		rows = append(rows, dlRow{TypeDeadlineFarm, farm})
		for _, c := range dlNames {
			fleet := farm + "/fleet/" + c
			rows = append(rows,
				dlRow{TypeDeadlineFleet, fleet},
				dlRow{TypeDeadlineQueue, farm + "/queue/" + c},
				dlRow{TypeDeadlineBudget, farm + "/budget/" + c})
			for _, v := range dlNames {
				rows = append(rows, dlRow{TypeDeadlineVolume, fleet + "/volume/" + v})
			}
		}
		rows = append(rows,
			dlRow{TypeDeadlineLicenseEndpoint, dlLicenseEndpoint(f)},
			dlRow{TypeDeadlineMonitor, "arn:aws:deadline:" + testRegion + ":" + testAccountID + ":monitor/" + f})
	}
	return rows
}

// dlUntaggedRows is every fixture row of a type whose tags are not read.
func dlUntaggedRows() []dlRow {
	var rows []dlRow
	for _, f := range dlNames {
		farm := dlFarm(f)
		rows = append(rows,
			dlRow{TypeDeadlineLimit, farm + "/limit/limit-1"},
			dlRow{TypeDeadlineStorageProfile, farm + "/storage-profile/sp-1"},
			dlRow{TypeDeadlineQueueFleetAssociation, farm + "/queue/tagged/fleet/tagged/association"},
			dlRow{TypeDeadlineQueueLimitAssociation, farm + "/queue/tagged/limit/limit-1/association"},
			dlRow{TypeDeadlineMeteredProduct, dlLicenseEndpoint(f) + "/metered-product/prod-1"})
		for _, q := range dlNames {
			rows = append(rows, dlRow{TypeDeadlineQueueEnvironment, farm + "/queue/" + q + "/queue-environment/env-1"})
		}
	}
	return rows
}

func dlIsTagged(arn string) bool { return strings.HasSuffix(arn, "/tagged") }

// dlCalls records the stubbed Deadline calls (bacCalls) and the farm and
// child ids the per-fleet and per-queue listings are sent.
type dlCalls struct {
	*bacCalls
	volumesOf, envsOf, fleetMembersOf parentCalls
}

func newDLCalls() *dlCalls {
	c := &dlCalls{bacCalls: newBACCalls()}
	for _, r := range dlTaggedRows() {
		if dlIsTagged(r.arn) {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *dlCalls) listTags(in any) (any, error) {
	arn := sv(in.(*deadline.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &deadline.ListTagsForResourceOutput{Tags: tags}, nil
}

// twoPages answers op with "tagged" on the first page and "bare" on the
// second.
func (c *dlCalls) twoPages(op string, out func(in any, name string, next *string) any) func(any) (any, error) {
	return c.answer(op, func(in any) any {
		if bacField[*string](in, "NextToken") == nil {
			return out(in, "tagged", sdkaws.String("p2"))
		}
		return out(in, "bare", nil)
	})
}

// dlHandlers answers every op the scan calls with the fixture rows
// (dlTaggedRows, dlUntaggedRows) and no members.
func dlHandlers(c *dlCalls) map[string]func(any) (any, error) {
	farmOf := func(in any) *string { return bacField[*string](in, "FarmId") }
	s := sdkaws.String
	return map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListFarms": c.twoPages("ListFarms", func(_ any, n string, next *string) any {
			return &deadline.ListFarmsOutput{Farms: []dltypes.FarmSummary{{FarmId: s(n), DisplayName: s("farm " + n)}}, NextToken: next}
		}),
		"ListFleets": c.twoPages("ListFleets", func(in any, n string, next *string) any {
			return &deadline.ListFleetsOutput{Fleets: []dltypes.FleetSummary{{FleetId: s(n), FarmId: farmOf(in)}}, NextToken: next}
		}),
		"ListQueues": c.twoPages("ListQueues", func(in any, n string, next *string) any {
			return &deadline.ListQueuesOutput{Queues: []dltypes.QueueSummary{{QueueId: s(n), FarmId: farmOf(in)}}, NextToken: next}
		}),
		"ListBudgets": c.twoPages("ListBudgets", func(_ any, n string, next *string) any {
			return &deadline.ListBudgetsOutput{Budgets: []dltypes.BudgetSummary{{BudgetId: s(n), DisplayName: s("budget " + n), Status: dltypes.BudgetStatusActive}}, NextToken: next}
		}),
		"ListVolumes": c.twoPages("ListVolumes", func(in any, n string, next *string) any {
			p := in.(*deadline.ListVolumesInput)
			c.volumesOf.add(sv(p.FarmId) + "/" + sv(p.FleetId))
			return &deadline.ListVolumesOutput{Volumes: []dltypes.VolumeSummary{{VolumeId: s(n), FarmId: p.FarmId, FleetId: p.FleetId, State: dltypes.VolumeStateInUse}}, NextToken: next}
		}),
		"ListLicenseEndpoints": c.twoPages("ListLicenseEndpoints", func(_ any, n string, next *string) any {
			return &deadline.ListLicenseEndpointsOutput{LicenseEndpoints: []dltypes.LicenseEndpointSummary{{LicenseEndpointId: s(n)}}, NextToken: next}
		}),
		"ListMonitors": c.twoPages("ListMonitors", func(_ any, n string, next *string) any {
			return &deadline.ListMonitorsOutput{Monitors: []dltypes.MonitorSummary{{MonitorId: s(n)}}, NextToken: next}
		}),
		"ListLimits": c.answer("ListLimits", func(any) any {
			return &deadline.ListLimitsOutput{Limits: []dltypes.LimitSummary{{LimitId: s("limit-1")}}}
		}),
		"ListStorageProfiles": c.answer("ListStorageProfiles", func(any) any {
			return &deadline.ListStorageProfilesOutput{StorageProfiles: []dltypes.StorageProfileSummary{{StorageProfileId: s("sp-1")}}}
		}),
		"ListQueueFleetAssociations": c.answer("ListQueueFleetAssociations", func(any) any {
			return &deadline.ListQueueFleetAssociationsOutput{QueueFleetAssociations: []dltypes.QueueFleetAssociationSummary{{QueueId: s("tagged"), FleetId: s("tagged")}}}
		}),
		"ListQueueLimitAssociations": c.answer("ListQueueLimitAssociations", func(any) any {
			return &deadline.ListQueueLimitAssociationsOutput{QueueLimitAssociations: []dltypes.QueueLimitAssociationSummary{{QueueId: s("tagged"), LimitId: s("limit-1")}}}
		}),
		"ListQueueEnvironments": c.answer("ListQueueEnvironments", func(in any) any {
			p := in.(*deadline.ListQueueEnvironmentsInput)
			c.envsOf.add(sv(p.FarmId) + "/" + sv(p.QueueId))
			return &deadline.ListQueueEnvironmentsOutput{Environments: []dltypes.QueueEnvironmentSummary{{QueueEnvironmentId: s("env-1")}}}
		}),
		"ListMeteredProducts": c.answer("ListMeteredProducts", func(any) any {
			return &deadline.ListMeteredProductsOutput{MeteredProducts: []dltypes.MeteredProductSummary{{ProductId: s("prod-1")}}}
		}),
		"ListFarmMembers": c.answer("ListFarmMembers", func(any) any { return &deadline.ListFarmMembersOutput{} }),
		"ListFleetMembers": c.answer("ListFleetMembers", func(in any) any {
			p := in.(*deadline.ListFleetMembersInput)
			c.fleetMembersOf.add(sv(p.FarmId) + "/" + sv(p.FleetId))
			return &deadline.ListFleetMembersOutput{}
		}),
		"ListQueueMembers": c.answer("ListQueueMembers", func(any) any { return &deadline.ListQueueMembersOutput{} }),
	}
}

// dlFixtureRows is how many rows the fixture stores.
var dlFixtureRows = len(dlTaggedRows()) + len(dlUntaggedRows())

// dlScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func dlScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, dlDenied)
	return scanDeadline(ctx, acct, testRegion, st, testScanID)
}

func dlRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type the tag read covers stores the tags ListTagsForResource returns
// for its ARN, and nil for a resource without tags; no other row is sent to
// it, and those rows store no tags. Both pages of every listing are stored,
// every listing asks for the model's page cap, the per-fleet listings are
// sent each fleet's farm and fleet id, and the scan reports no error or
// warning.
func TestScanDeadline_StoresTagsOfEveryTaggedType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newDLCalls()

	total, inserted, err := dlScan(context.Background(), t, st, dlHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 40 || inserted != 40 {
		t.Errorf("total, inserted = %d, %d; want 40, 40", total, inserted)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, w := range dlTaggedRows() {
		wantARNs = append(wantARNs, w.arn)
		r, ok := dlRowOf(t, st, w.typ, w.arn)
		switch {
		case !ok:
			t.Errorf("%s %s missing", w.typ, w.arn)
		case dlIsTagged(w.arn):
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": w.typ})
		case r.TagsJSON != nil:
			t.Errorf("%s %s TagsJSON = %q; want nil", w.typ, w.arn, *r.TagsJSON)
		}
	}
	for _, w := range dlUntaggedRows() {
		r, ok := dlRowOf(t, st, w.typ, w.arn)
		if !ok {
			t.Errorf("%s %s missing", w.typ, w.arn)
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", w.typ, *r.TagsJSON)
		}
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	for _, op := range []string{
		"ListFarms", "ListFleets", "ListQueues", "ListBudgets", "ListVolumes", "ListLicenseEndpoints", "ListMonitors",
		"ListLimits", "ListStorageProfiles", "ListQueueFleetAssociations", "ListQueueLimitAssociations",
		"ListQueueEnvironments", "ListMeteredProducts", "ListFarmMembers", "ListFleetMembers", "ListQueueMembers",
	} {
		got := calls.maxResults[op]
		if len(got) == 0 {
			t.Errorf("%s never sent MaxResults", op)
		}
		for _, n := range got {
			if n != 100 {
				t.Errorf("%s MaxResults = %d; want 100", op, n)
			}
		}
	}
	everyChild := []string{"bare/bare", "bare/tagged", "tagged/bare", "tagged/tagged"}
	if got := calls.volumesOf.distinct(); !slices.Equal(got, everyChild) {
		t.Errorf("ListVolumes farm/fleet = %v; want %v", got, everyChild)
	}
	if got := calls.envsOf.distinct(); !slices.Equal(got, everyChild) {
		t.Errorf("ListQueueEnvironments farm/queue = %v; want %v", got, everyChild)
	}
	budget, _ := dlRowOf(t, st, TypeDeadlineBudget, dlFarm("tagged")+"/budget/tagged")
	volume, _ := dlRowOf(t, st, TypeDeadlineVolume, dlFarm("tagged")+"/fleet/tagged/volume/tagged")
	if sv(budget.Status) != "ACTIVE" || sv(volume.Status) != "IN_USE" {
		t.Errorf("budget, volume Status = %q, %q; want ACTIVE, IN_USE", sv(budget.Status), sv(volume.Status))
	}
	if farm, _ := dlRowOf(t, st, TypeDeadlineFarm, dlFarm("tagged")); sv(farm.Name) != "farm tagged" {
		t.Errorf("farm Name = %q; want its DisplayName", sv(farm.Name))
	}
	if sv(budget.Name) != "budget tagged" {
		t.Errorf("budget Name = %q; want its DisplayName", sv(budget.Name))
	}
}

// A rescan stores the same rows without inserting any.
func TestScanDeadline_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newDLCalls()
	if _, _, err := dlScan(context.Background(), t, st, dlHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := dlScan(context.Background(), t, st, dlHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != dlFixtureRows || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, dlFixtureRows)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under deadline:ListTagsForResource, a throttle after
// retries is a transient error, any other code a hard one. A farm or fleet
// the tag read no longer finds is dropped, and its children are never
// listed; a monitor, from an account-wide listing, likewise.
func TestScanDeadline_TagReadFailures(t *testing.T) {
	for _, target := range []dlRow{
		{TypeDeadlineFarm, dlFarm("tagged")},
		{TypeDeadlineFleet, dlFarm("tagged") + "/fleet/tagged"},
		{TypeDeadlineMonitor, "arn:aws:deadline:" + testRegion + ":" + testAccountID + ":monitor/tagged"},
	} {
		t.Run(target.typ, func(t *testing.T) { dlTagReadFailures(t, target) })
	}
}

func dlTagReadFailures(t *testing.T, target dlRow) {
	for _, c := range []struct {
		name      string
		err       error
		kept      bool
		outcome   serviceOutcome
		wantWarns int
	}{
		{"denied", dlDenied, true, 0, 1},
		{"not found", dlGone, false, 0, 0},
		{"other error", dlBadInput, true, outcomeError, 0},
		{"transient", dlThrottle, true, outcomeTransient, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, deadlineListTagsOp)
			calls := newDLCalls()
			calls.errs[target.arn] = c.err

			_, _, err := dlScan(context.Background(), t, st, dlHandlers(calls))
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
			}
			if c.outcome == outcomeError {
				assertAPICode(t, err, "ValidationException")
			}
			r, ok := dlRowOf(t, st, target.typ, target.arn)
			if ok != c.kept {
				t.Fatalf("%s row stored = %v; want %v", target.typ, ok, c.kept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			switch target.typ {
			case TypeDeadlineFarm:
				if listed := calls.envsOf.distinct(); slices.Contains(listed, "tagged/tagged") != c.kept || !slices.Contains(listed, "bare/bare") {
					t.Errorf("queue environments listed of %v; want the farm's queues only when it is kept, the other farm's always", listed)
				}
			case TypeDeadlineFleet:
				for _, calls := range []*parentCalls{&calls.volumesOf, &calls.fleetMembersOf} {
					if listed := calls.distinct(); slices.Contains(listed, "tagged/tagged") != c.kept || !slices.Contains(listed, "tagged/bare") {
						t.Errorf("fleet children listed of %v; want the fleet only when it is kept, its sibling always", listed)
					}
				}
			}
			if len(*warnings) != c.wantWarns {
				t.Fatalf("%s warnings = %v; want %d", deadlineListTagsOp, *warnings, c.wantWarns)
			}
			for _, w := range *warnings {
				if w.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanDeadline_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, deadlineListTagsOp)
	calls := newDLCalls()
	calls.errs[dlFarm("tagged")+"/queue/tagged"] = dlThrottle
	calls.errs[dlFarm("tagged")+"/queue/bare"] = dlBadInput

	_, _, err := dlScan(context.Background(), t, st, dlHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", deadlineListTagsOp, *warns)
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost: not the tag denial, and not a denied
// listing page.
func TestScanDeadline_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeDeadlineFleet)
	calls := newDLCalls()
	calls.errs[dlFarm("tagged")+"/fleet/tagged"] = dlDenied
	handlers := dlHandlers(calls)
	handlers["ListFleets"] = failOnCall(2, dlDenied, handlers["ListFleets"])

	_, _, err := dlScan(context.Background(), t, st, handlers)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("ListLimits"); n != 0 {
		t.Errorf("ListLimits called %d times after the failed write", n)
	}
}

// A listing's failed page keeps the pages read before it, and neither a
// listing's failure nor a parent listing's failure stops the phases after
// it, a failed counted phase (limits) included. A farm a per-farm listing no
// longer finds is skipped silently.
func TestScanDeadline_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newDLCalls()
	handlers := dlHandlers(calls)
	handlers["ListFarms"] = failOnCall(2, apiErr("ValidationException", "page 2"), handlers["ListFarms"])
	handlers["ListLicenseEndpoints"] = func(any) (any, error) { return nil, apiErr("ConflictException", "endpoints") }
	handlers["ListLimits"] = func(any) (any, error) { return nil, apiErr("ServiceQuotaExceededException", "limits") }
	listBudgets := handlers["ListBudgets"]
	handlers["ListBudgets"] = func(in any) (any, error) {
		if sv(in.(*deadline.ListBudgetsInput).FarmId) == "tagged" {
			return nil, dlGone
		}
		return listBudgets(in)
	}

	_, _, err := dlScan(context.Background(), t, st, handlers)
	for _, code := range []string{"ValidationException", "ServiceQuotaExceededException", "ConflictException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	if joinCarriesAPIErrorCode(err, "ResourceNotFoundException") {
		t.Errorf("err = %v; want the farm ListBudgets no longer finds skipped, not returned", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	farm, ok := dlRowOf(t, st, TypeDeadlineFarm, dlFarm("tagged"))
	if !ok {
		t.Fatal("farm from the page before the failure missing")
	}
	assertTagsMap(t, farm.TagsJSON, map[string]string{"type": TypeDeadlineFarm})
	if _, ok := dlRowOf(t, st, TypeDeadlineFarm, dlFarm("bare")); ok {
		t.Error("farm from the failed page stored")
	}
	if n := len(rowsByNativeID(t, st, TypeDeadlineBudget)); n != 0 {
		t.Errorf("budgets stored = %d; want 0 of the farm ListBudgets no longer finds", n)
	}
	if n := calls.count("ListMeteredProducts"); n != 0 {
		t.Errorf("ListMeteredProducts called %d times with no license endpoint stored", n)
	}
	for _, later := range []dlRow{
		{TypeDeadlineFleet, dlFarm("tagged") + "/fleet/bare"},
		{TypeDeadlineQueueEnvironment, dlFarm("tagged") + "/queue/bare/queue-environment/env-1"},
		{TypeDeadlineVolume, dlFarm("tagged") + "/fleet/bare/volume/bare"},
		{TypeDeadlineMonitor, "arn:aws:deadline:" + testRegion + ":" + testAccountID + ":monitor/bare"},
	} {
		if _, ok := dlRowOf(t, st, later.typ, later.arn); !ok {
			t.Errorf("%s %s missing after the earlier failures", later.typ, later.arn)
		}
	}
}

// Items that cannot be stored are warned about in one warning per op that
// names the first; their siblings are stored.
func TestScanDeadline_UnstorableItemsWarn(t *testing.T) {
	for _, tc := range []struct {
		name, op, key, typ string
		answer             func(any) (any, error)
	}{
		{"farm without an id", "ListFarms", "unnamed", TypeDeadlineFarm, func(any) (any, error) {
			return &deadline.ListFarmsOutput{Farms: []dltypes.FarmSummary{{FarmId: sdkaws.String("tagged")}, {DisplayName: sdkaws.String("unnamed")}}}, nil
		}},
		{"fleet without an id or name", "ListFleets", "(a fleet with no name) of farm tagged", TypeDeadlineFleet, func(in any) (any, error) {
			if sv(in.(*deadline.ListFleetsInput).FarmId) != "tagged" {
				return &deadline.ListFleetsOutput{}, nil
			}
			return &deadline.ListFleetsOutput{Fleets: []dltypes.FleetSummary{{FleetId: sdkaws.String("tagged")}, {}}}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "deadline:"+tc.op)
			handlers := dlHandlers(newDLCalls())
			handlers[tc.op] = tc.answer

			if _, _, err := dlScan(context.Background(), t, st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := len(rowsByNativeID(t, st, tc.typ)); got != 1 {
				t.Errorf("%s stored = %d; want 1", tc.typ, got)
			}
			if len(*warnings) != 1 {
				t.Fatalf("warnings = %v; want 1", *warnings)
			}
			if w := (*warnings)[0]; !strings.HasSuffix(w.Message, "first "+tc.key) || w.Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning = %q (Scope %q); want it to name %q first", w.Message, w.Scope, tc.key)
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanDeadline_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := dlHandlers(newDLCalls())
	handlers["ListMonitors"] = func(any) (any, error) {
		return &deadline.ListMonitorsOutput{Monitors: []dltypes.MonitorSummary{{MonitorId: sdkaws.String("tagged")}}, NextToken: sdkaws.String("same")}, nil
	}

	_, _, err := dlScan(context.Background(), t, st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := dlRowOf(t, st, TypeDeadlineMonitor, "arn:aws:deadline:"+testRegion+":"+testAccountID+":monitor/tagged"); !ok {
		t.Error("monitor from the pages read missing")
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run, although the phase in flight saw it in its
// listing and its tag reads.
func TestScanDeadline_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newDLCalls()
	handlers := dlHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The queue listing of a farm ends on its second page, as an SDK call
	// does when ctx ends; the queues of its first pages are still tag-read.
	queues := handlers["ListQueues"]
	handlers["ListQueues"] = func(in any) (any, error) {
		if in.(*deadline.ListQueuesInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return queues(in)
	}

	_, _, err := dlScan(ctx, t, st, handlers)
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
	if n := calls.count("ListQueueFleetAssociations"); n != 0 {
		t.Errorf("ListQueueFleetAssociations called %d times after ctx ended", n)
	}
	// Queues are phase 6 of 16, so 10 never started.
	if !strings.Contains(err.Error(), "deadline: 10 phases not run") {
		t.Errorf("err = %v; want it to say 10 phases not run", err)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. A tag read's NXDOMAIN is one too. With nothing
// stored it stays region-unavailable.
func TestScanDeadline_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "management.deadline.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := dlHandlers(newDLCalls())
		handlers["ListMonitors"] = func(any) (any, error) { return nil, nx }
		total, _, err := dlScan(context.Background(), t, st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total == 0 {
			t.Error("total = 0; want the stored rows counted")
		}
	})
	t.Run("tag read", func(t *testing.T) {
		st := newTestStore(t)
		calls := newDLCalls()
		calls.errs[dlFarm("tagged")] = nx
		_, _, err := dlScan(context.Background(), t, st, dlHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanDeadline(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}
