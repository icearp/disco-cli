package aws

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync/atomic"
	"testing"

	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssmincidents"
	ssmitypes "github.com/aws/aws-sdk-go-v2/service/ssmincidents/types"
	"github.com/icearp/disco-cli/store"
)

const (
	ssmiRSetARN = "arn:aws:ssm-incidents::123456789012:replication-set/rs-1"
	ssmiPlanP1  = "arn:aws:ssm-incidents::123456789012:response-plan/p1"
	ssmiPlanP2  = "arn:aws:ssm-incidents::123456789012:response-plan/p2"

	ssmiPolP1A = "arn:aws:ssm-incidents::123456789012:response-plan/p1/resource-policy/pol-a"
	ssmiPolP1B = "arn:aws:ssm-incidents::123456789012:response-plan/p1/resource-policy/pol-b"
	ssmiPolP2A = "arn:aws:ssm-incidents::123456789012:response-plan/p2/resource-policy/pol-a"

	ssmiPolicyDoc = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"111122223333"},"Action":"ssm-incidents:GetResponsePlan","Resource":"*"}]}`
)

var (
	ssmiDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: ssm-incidents:Get")
	ssmiBadInput = apiErr("ValidationException", "bad")
	ssmiGone     = apiErr("ResourceNotFoundException", "not found")
)

// ssmiHandlers answers one replication set and response plans p1 and p2 (over
// two pages, the second also holding a summary with no ARN), and:
//   - GetResourcePolicies: p1 over two pages (pol-a; pol-b and an element with
//     no PolicyId), p2 one page with a pol-a of its own.
//
// policyCalls records the plan ARN of each GetResourcePolicies call. Every
// other op is denied.
func ssmiHandlers(policyCalls *parentCalls) map[string]func(in any) (any, error) {
	policy := func(id string) ssmitypes.ResourcePolicy {
		return ssmitypes.ResourcePolicy{PolicyId: ptrStr(id), PolicyDocument: ptrStr(ssmiPolicyDoc), RamResourceShareRegion: ptrStr("us-west-2")}
	}
	return map[string]func(in any) (any, error){
		"ListReplicationSets": func(any) (any, error) {
			return &ssmincidents.ListReplicationSetsOutput{ReplicationSetArns: []string{ssmiRSetARN}}, nil
		},
		"GetReplicationSet": func(any) (any, error) {
			return &ssmincidents.GetReplicationSetOutput{ReplicationSet: &ssmitypes.ReplicationSet{
				Arn: ptrStr(ssmiRSetARN), Status: ssmitypes.ReplicationSetStatusActive,
			}}, nil
		},
		"ListResponsePlans": func(in any) (any, error) {
			if in.(*ssmincidents.ListResponsePlansInput).NextToken == nil {
				return &ssmincidents.ListResponsePlansOutput{
					ResponsePlanSummaries: []ssmitypes.ResponsePlanSummary{{Arn: ptrStr(ssmiPlanP1), Name: ptrStr("p1")}},
					NextToken:             ptrStr("p2"),
				}, nil
			}
			return &ssmincidents.ListResponsePlansOutput{ResponsePlanSummaries: []ssmitypes.ResponsePlanSummary{
				{Arn: ptrStr(ssmiPlanP2), Name: ptrStr("p2")}, {Name: ptrStr("no-arn")},
			}}, nil
		},
		"GetResponsePlan": func(in any) (any, error) {
			arn := in.(*ssmincidents.GetResponsePlanInput).Arn
			return &ssmincidents.GetResponsePlanOutput{Arn: arn}, nil
		},
		"GetResourcePolicies": func(in any) (any, error) {
			p := in.(*ssmincidents.GetResourcePoliciesInput)
			plan := sv(p.ResourceArn)
			policyCalls.add(plan)
			switch {
			case plan == ssmiPlanP2:
				return &ssmincidents.GetResourcePoliciesOutput{ResourcePolicies: []ssmitypes.ResourcePolicy{policy("pol-a")}}, nil
			case p.NextToken == nil:
				return &ssmincidents.GetResourcePoliciesOutput{ResourcePolicies: []ssmitypes.ResourcePolicy{policy("pol-a")}, NextToken: ptrStr("p2")}, nil
			default:
				return &ssmincidents.GetResourcePoliciesOutput{ResourcePolicies: []ssmitypes.ResourcePolicy{
					policy("pol-b"), {PolicyDocument: ptrStr(ssmiPolicyDoc), RamResourceShareRegion: ptrStr("us-west-2")},
				}}, nil
			}
		},
	}
}

func scanSSMITopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ssmiDenied)
	return scanSSMIncidents(context.Background(), acct, testRegion, st, testScanID)
}

func TestScanSSMIncidents_StoresResourcePoliciesUnderTheirPlan(t *testing.T) {
	st := newTestStore(t)
	var policyCalls parentCalls
	if _, _, err := scanSSMITopLevel(t, st, ssmiHandlers(&policyCalls)); err != nil {
		t.Fatalf("scanSSMIncidents: %v", err)
	}
	if got, want := policyCalls.distinct(), []string{ssmiPlanP1, ssmiPlanP2}; !slices.Equal(got, want) {
		t.Errorf("policies listed for %v, want %v", got, want)
	}
	rows := rowsByNativeID(t, st, TypeSSMIncidentsResourcePolicy)
	if got, want := nativeIDs(rows), []string{ssmiPolP1A, ssmiPolP1B, ssmiPolP2A}; !slices.Equal(got, want) {
		t.Fatalf("resource policy rows = %v, want %v", got, want)
	}
	assertContainsNative(t, st, ssmiPlanP1, ssmiPolP1A)
	assertContainsNative(t, st, ssmiPlanP1, ssmiPolP1B)
	assertContainsNative(t, st, ssmiPlanP2, ssmiPolP2A)
	assertContainsEdgesDeclared(t, st)

	pol := rows[ssmiPolP1A]
	if sv(pol.Name) != "pol-a" || sv(pol.Region) != testRegion {
		t.Errorf("pol-a name/region = %q/%q, want pol-a/%s", sv(pol.Name), sv(pol.Region), testRegion)
	}
	var attrs struct {
		PolicyDocument         string
		RAMResourceShareRegion string `json:"RamResourceShareRegion"`
	}
	if err := json.Unmarshal([]byte(pol.AttributesJSON), &attrs); err != nil {
		t.Fatalf("pol-a attrs: %v", err)
	}
	if attrs.PolicyDocument != ssmiPolicyDoc || attrs.RAMResourceShareRegion != "us-west-2" {
		t.Errorf("pol-a attrs = %s", pol.AttributesJSON)
	}
}

// Two plans each holding a pol-a keep two identities, and neither shares its
// plan's identity.
func TestScanSSMIncidents_ResourcePolicyIdentityIsScopedToItsPlan(t *testing.T) {
	st := newTestStore(t)
	var policyCalls parentCalls
	if _, _, err := scanSSMITopLevel(t, st, ssmiHandlers(&policyCalls)); err != nil {
		t.Fatalf("scanSSMIncidents: %v", err)
	}
	policies := rowsByNativeID(t, st, TypeSSMIncidentsResourcePolicy)
	plans := rowsByNativeID(t, st, TypeSSMIncidentsResponsePlan)
	ids := []string{policies[ssmiPolP1A].ID, policies[ssmiPolP2A].ID, plans[ssmiPlanP1].ID, plans[ssmiPlanP2].ID}
	slices.Sort(ids)
	if len(slices.Compact(ids)) != 4 || ids[0] == "" {
		t.Errorf("row ids = %v, want 4 distinct", ids)
	}
}

func TestScanSSMIncidents_NoPlansNoPolicyCalls(t *testing.T) {
	st := newTestStore(t)
	var policyCalls parentCalls
	h := ssmiHandlers(&policyCalls)
	h["ListResponsePlans"] = func(any) (any, error) { return &ssmincidents.ListResponsePlansOutput{}, nil }
	if _, _, err := scanSSMITopLevel(t, st, h); err != nil {
		t.Fatalf("scanSSMIncidents: %v", err)
	}
	if got := policyCalls.distinct(); len(got) != 0 {
		t.Errorf("policy calls = %v, want none", got)
	}
	if n := len(rowsByNativeID(t, st, TypeSSMIncidentsResourcePolicy)); n != 0 {
		t.Errorf("resource policy rows = %d, want 0", n)
	}
}

// A failed plan drops only its own policies: deleted since listing is silent,
// a denial warns once however many plans hit it, and any other error fails
// the scan.
func TestScanSSMIncidents_FailedResourcePolicyPlan(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		bad      []string
		wantWarn int
		want     []string
	}{
		{"plan gone", ssmiGone, []string{ssmiPlanP1}, 0, []string{ssmiPolP2A}},
		{"denied", ssmiDenied, []string{ssmiPlanP1, ssmiPlanP2}, 1, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "ssm-incidents:GetResourcePolicies")
			var policyCalls parentCalls
			h := ssmiHandlers(&policyCalls)
			next := h["GetResourcePolicies"]
			h["GetResourcePolicies"] = func(in any) (any, error) {
				if slices.Contains(tc.bad, sv(in.(*ssmincidents.GetResourcePoliciesInput).ResourceArn)) {
					return nil, tc.err
				}
				return next(in)
			}
			if _, _, err := scanSSMITopLevel(t, st, h); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeSSMIncidentsResourcePolicy)); !slices.Equal(got, tc.want) {
				t.Errorf("rows = %v, want %v", got, tc.want)
			}
		})
	}
}

// The resource-policy phase runs after every pre-existing phase, and its hard
// error reaches the caller.
func TestScanSSMIncidents_ResourcePolicyErrorAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	var policyCalls parentCalls
	h := ssmiHandlers(&policyCalls)
	var failed atomic.Bool
	h["GetResourcePolicies"] = func(any) (any, error) {
		failed.Store(true)
		return nil, ssmiBadInput
	}
	_, _, err := scanSSMITopLevel(t, st, h)
	if !failed.Load() {
		t.Fatal("GetResourcePolicies never called")
	}
	assertAPICode(t, err, "ValidationException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeSSMIncidentsResponsePlan)); !slices.Equal(got, []string{ssmiPlanP1, ssmiPlanP2}) {
		t.Errorf("response plan rows = %v, want [%s %s]", got, ssmiPlanP1, ssmiPlanP2)
	}
	if n := len(rowsByNativeID(t, st, TypeSSMIncidentsReplicationSet)); n != 1 {
		t.Errorf("replication set rows = %d, want 1", n)
	}
}

// Every stored row counts once in the totals: 1 replication set, 2 response
// plans and 3 resource policies. On a fresh store every row is new.
func TestScanSSMIncidents_TotalsCountEveryPhase(t *testing.T) {
	st := newTestStore(t)
	var policyCalls parentCalls
	total, inserted, err := scanSSMITopLevel(t, st, ssmiHandlers(&policyCalls))
	if err != nil {
		t.Fatalf("scanSSMIncidents: %v", err)
	}
	if total != 6 || inserted != 6 {
		t.Errorf("totals = (%d, %d), want (6, 6)", total, inserted)
	}
}

// The client is pinned to the scanned region, not the config's default.
func TestScanSSMIncidents_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var policyCalls, regions parentCalls
	cfg := opStubConfig(ssmiHandlers(&policyCalls), ssmiDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)
	acct.scanRegions = []string{"eu-west-1"}

	if _, _, err := scanSSMIncidents(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanSSMIncidents: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	if row, ok := rowsByNativeID(t, st, TypeSSMIncidentsResourcePolicy)[ssmiPolP1A]; !ok || sv(row.Region) != "eu-west-1" {
		t.Errorf("pol-a row = %+v (found %v), want region eu-west-1", row, ok)
	}
}

// ssmiRegionMapHandlers is ssmiHandlers with a replication set replicated to
// setRegions.
func ssmiRegionMapHandlers(policyCalls *parentCalls, setRegions ...string) map[string]func(in any) (any, error) {
	h := ssmiHandlers(policyCalls)
	regionMap := map[string]ssmitypes.RegionInfo{}
	for _, r := range setRegions {
		regionMap[r] = ssmitypes.RegionInfo{Status: ssmitypes.RegionStatusActive}
	}
	h["GetReplicationSet"] = func(any) (any, error) {
		return &ssmincidents.GetReplicationSetOutput{ReplicationSet: &ssmitypes.ReplicationSet{
			Arn: ptrStr(ssmiRSetARN), Status: ssmitypes.ReplicationSetStatusActive, RegionMap: regionMap,
		}}, nil
	}
	return h
}

// scanSSMIEachRegion runs the service scan once per region in order, as one
// scan of the regions in scanned, and returns each region's total.
func scanSSMIEachRegion(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error), scanned, order []string) map[string]int {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ssmiDenied)
	acct.scanRegions = scanned
	totals := map[string]int{}
	for _, region := range order {
		total, _, err := scanSSMIncidents(context.Background(), acct, region, st, testScanID)
		if err != nil {
			t.Fatalf("scanSSMIncidents(%s): %v", region, err)
		}
		totals[region] = total
	}
	return totals
}

// assertSSMIRowsFrom checks that every region-less row is present, with Region
// set to want.
func assertSSMIRowsFrom(t *testing.T, st *store.Store, want string) {
	t.Helper()
	for typ, ids := range map[string][]string{
		TypeSSMIncidentsReplicationSet: {ssmiRSetARN},
		TypeSSMIncidentsResponsePlan:   {ssmiPlanP1, ssmiPlanP2},
		TypeSSMIncidentsResourcePolicy: {ssmiPolP1A, ssmiPolP1B, ssmiPolP2A},
	} {
		rows := rowsByNativeID(t, st, typ)
		if got := nativeIDs(rows); !slices.Equal(got, ids) {
			t.Errorf("%s rows = %v, want %v", typ, got, ids)
		}
		for id, r := range rows {
			if sv(r.Region) != want {
				t.Errorf("%s %s region = %q, want %q", typ, id, sv(r.Region), want)
			}
		}
	}
}

// With the set replicated to eu-west-1 and us-west-2, only eu-west-1 stores:
// it is the lexically first scanned member, ahead of ap-south-1, which sorts
// first but is not in the set. Either scan order gives the same result.
func TestScanSSMIncidents_StoresRegionlessRowsFromOneRegion(t *testing.T) {
	scanned := []string{"us-west-2", "eu-west-1", "ap-south-1"}
	for _, order := range [][]string{scanned, {"ap-south-1", "eu-west-1", "us-west-2"}} {
		t.Run(order[0]+" first", func(t *testing.T) {
			st := newTestStore(t)
			var policyCalls parentCalls
			totals := scanSSMIEachRegion(t, st, ssmiRegionMapHandlers(&policyCalls, "us-west-2", "eu-west-1"), scanned, order)
			if want := map[string]int{"us-west-2": 0, "eu-west-1": 6, "ap-south-1": 0}; !maps.Equal(totals, want) {
				t.Errorf("totals = %v, want %v", totals, want)
			}
			assertSSMIRowsFrom(t, st, "eu-west-1")
			set := rowsByNativeID(t, st, TypeSSMIncidentsReplicationSet)[ssmiRSetARN]
			var attrs struct{ RegionMap map[string]json.RawMessage }
			if err := json.Unmarshal([]byte(set.AttributesJSON), &attrs); err != nil {
				t.Fatalf("set attrs: %v", err)
			}
			if got := slices.Sorted(maps.Keys(attrs.RegionMap)); sv(set.Status) != "ACTIVE" || !slices.Equal(got, []string{"eu-west-1", "us-west-2"}) {
				t.Errorf("set status/RegionMap = %q/%v, want ACTIVE/[eu-west-1 us-west-2]", sv(set.Status), got)
			}
		})
	}
}

// When the scan leaves out the first member, the lexically first scanned
// member stores; when it covers no member, the lexically first scanned region.
func TestScanSSMIncidents_StoreRegionFallback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		scanned []string
		want    string
	}{
		{"first member not scanned", []string{"us-west-2", "us-east-1"}, "us-west-2"},
		{"no member scanned", []string{"us-east-1", "ap-south-1"}, "ap-south-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var policyCalls parentCalls
			order := slices.Clone(tc.scanned)
			slices.Reverse(order)
			totals := scanSSMIEachRegion(t, st, ssmiRegionMapHandlers(&policyCalls, "us-west-2", "eu-west-1"), tc.scanned, order)
			for _, r := range tc.scanned {
				want := 0
				if r == tc.want {
					want = 6
				}
				if totals[r] != want {
					t.Errorf("%s total = %d, want %d", r, totals[r], want)
				}
			}
			assertSSMIRowsFrom(t, st, tc.want)
		})
	}
}

// ssmiWarningsByService counts st's warnings per ScanWarning.Service. The map
// is not locked: use it only with region scans run one after another.
func ssmiWarningsByService(st *store.Store) map[string]int {
	n := map[string]int{}
	st.OnWarn = func(w store.ScanWarning) { n[w.Service]++ }
	return n
}

// ssmiGetCalls counts GetReplicationSet calls by "from>to": the region whose
// scan made the call, and the region the call went to. A region's own reads
// count as from>from. It is not locked: use it only with region scans run one
// after another, setting from before each.
type ssmiGetCalls struct {
	from string
	n    map[string]int
}

// ssmiGetByRegion is h with GetReplicationSet failing with errs[region] in the
// regions errs names, recording every call in calls.
func ssmiGetByRegion(h map[string]func(in any) (any, error), errs map[string]error) (withCtx map[string]func(context.Context, any) (any, error), calls *ssmiGetCalls) {
	return ssmiGetBy(h, func(region, _ string) error { return errs[region] })
}

// ssmiGetBy is h with GetReplicationSet failing with fail(region, arn) where
// that is not nil, recording every call in calls.
func ssmiGetBy(h map[string]func(in any) (any, error), fail func(region, arn string) error) (withCtx map[string]func(context.Context, any) (any, error), calls *ssmiGetCalls) {
	withCtx = ignoreCtx(h)
	calls = &ssmiGetCalls{n: map[string]int{}}
	get := withCtx["GetReplicationSet"]
	withCtx["GetReplicationSet"] = func(ctx context.Context, in any) (any, error) {
		region := awsmw.GetRegion(ctx)
		calls.n[calls.from+">"+region]++
		if err := fail(region, sv(in.(*ssmincidents.GetReplicationSetInput).Arn)); err != nil {
			return nil, err
		}
		return get(ctx, in)
	}
	return withCtx, calls
}

// ssmiAccountCtx is a test account scanning the regions in scanned, answered by
// handlers that need the call's context.
func ssmiAccountCtx(handlers map[string]func(context.Context, any) (any, error), scanned []string) *account {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(handlers, ssmiDenied)
	acct.scanRegions = scanned
	return acct
}

// scanSSMIEachRegionCtx is scanSSMIEachRegion for handlers that need the
// call's context, keeping calls.from on the region being scanned.
func scanSSMIEachRegionCtx(t *testing.T, st *store.Store, handlers map[string]func(context.Context, any) (any, error), calls *ssmiGetCalls, scanned, order []string) map[string]int {
	t.Helper()
	acct := ssmiAccountCtx(handlers, scanned)
	totals := map[string]int{}
	for _, region := range order {
		calls.from = region
		total, _, err := scanSSMIncidents(context.Background(), acct, region, st, testScanID)
		if err != nil {
			t.Fatalf("scanSSMIncidents(%s): %v", region, err)
		}
		totals[region] = total
	}
	return totals
}

// assertNoSSMIRows checks that no Incident Manager row of any type is stored.
func assertNoSSMIRows(t *testing.T, st *store.Store) {
	t.Helper()
	for _, typ := range []string{TypeSSMIncidentsReplicationSet, TypeSSMIncidentsResponsePlan, TypeSSMIncidentsResourcePolicy} {
		if got := nativeIDs(rowsByNativeID(t, st, typ)); len(got) != 0 {
			t.Errorf("%s rows = %v, want none", typ, got)
		}
	}
}

// reversed returns a reversed copy of s.
func reversed(s []string) []string {
	r := slices.Clone(s)
	slices.Reverse(r)
	return r
}

// When every scanned region lists the sets but is denied GetReplicationSet,
// us-west-2 finds blind eu-west-1 ahead of it and stays out, and eu-west-1 finds
// no reader after it and stores, so the rows are stored once, in either scan
// order: every listed set with empty attributes, the plans and their policies.
// Each region warns once, however many sets it listed, beside its one
// GetReplicationSet denial warning, and probes the other region once.
func TestScanSSMIncidents_BlindEverywhereStoresOnce(t *testing.T) {
	scanned := []string{"us-west-2", "eu-west-1"}
	for _, order := range [][]string{scanned, reversed(scanned)} {
		t.Run(order[0]+" first", func(t *testing.T) {
			st := newTestStore(t)
			warns := ssmiWarningsByService(st)
			var policyCalls parentCalls
			h := ssmiRegionMapHandlers(&policyCalls, "us-west-2")
			h["ListReplicationSets"] = func(any) (any, error) {
				return &ssmincidents.ListReplicationSetsOutput{ReplicationSetArns: []string{ssmiRSetARN, ssmiRSetARN + "-2"}}, nil
			}
			withCtx, calls := ssmiGetByRegion(h, map[string]error{"us-west-2": ssmiDenied, "eu-west-1": ssmiDenied})
			totals := scanSSMIEachRegionCtx(t, st, withCtx, calls, scanned, order)
			if want := map[string]int{"us-west-2": 0, "eu-west-1": 7}; !maps.Equal(totals, want) {
				t.Errorf("totals = %v, want %v (2 sets, 2 plans, 3 policies)", totals, want)
			}
			sets := rowsByNativeID(t, st, TypeSSMIncidentsReplicationSet)
			if got, want := nativeIDs(sets), []string{ssmiRSetARN, ssmiRSetARN + "-2"}; !slices.Equal(got, want) {
				t.Fatalf("replication set rows = %v, want %v", got, want)
			}
			if r := sets[ssmiRSetARN]; r.AttributesJSON != "{}" || r.Status != nil || sv(r.Region) != "eu-west-1" {
				t.Errorf("unread set row attrs/status/region = %s/%v/%q, want {}/nil/eu-west-1", r.AttributesJSON, r.Status, sv(r.Region))
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeSSMIncidentsResourcePolicy)); !slices.Equal(got, []string{ssmiPolP1A, ssmiPolP1B, ssmiPolP2A}) {
				t.Errorf("resource policy rows = %v, want [%s %s %s]", got, ssmiPolP1A, ssmiPolP1B, ssmiPolP2A)
			}
			if want := map[string]int{ssmIncidentsService: 2, "ssm-incidents:GetReplicationSet": 2}; !maps.Equal(warns, want) {
				t.Errorf("warnings by service = %v, want %v (one of each per region)", warns, want)
			}
			want := map[string]int{"us-west-2>us-west-2": 2, "us-west-2>eu-west-1": 1, "eu-west-1>eu-west-1": 2, "eu-west-1>us-west-2": 1}
			if !maps.Equal(calls.n, want) {
				t.Errorf("GetReplicationSet calls from>to = %v, want %v", calls.n, want)
			}
		})
	}
}

// A blind region stays out once a probe finds the reader or a blind region
// ahead of it; the reader probes each candidate ahead of itself, skips a blind
// one and stores the rows itself, once, in either scan order. No region probes
// another twice or probes itself, and eu-west-1 stops at blind ap-south-1. A
// denied probe adds no warning; only each blind region's own scan warns.
func TestScanSSMIncidents_ReaderElectsPastBlindRegions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		scanned    []string
		blind      []string
		setRegions []string
		wantCalls  map[string]int
	}{
		{
			name: "blind region ahead of the reader", scanned: []string{"eu-west-1", "us-west-2"},
			blind: []string{"eu-west-1"}, setRegions: []string{"us-west-2", "eu-west-1"},
			wantCalls: map[string]int{
				"eu-west-1>eu-west-1": 1, "eu-west-1>us-west-2": 1,
				"us-west-2>us-west-2": 1, "us-west-2>eu-west-1": 1,
			},
		},
		{
			name: "every candidate but the reader blind", scanned: []string{"ap-south-1", "eu-west-1", "us-west-2"},
			blind: []string{"ap-south-1", "eu-west-1"}, setRegions: []string{"us-west-2", "eu-west-1", "ap-south-1"},
			wantCalls: map[string]int{
				"ap-south-1>ap-south-1": 1, "ap-south-1>eu-west-1": 1, "ap-south-1>us-west-2": 1,
				"eu-west-1>eu-west-1": 1, "eu-west-1>ap-south-1": 1,
				"us-west-2>us-west-2": 1, "us-west-2>ap-south-1": 1, "us-west-2>eu-west-1": 1,
			},
		},
	} {
		for _, order := range [][]string{tc.scanned, reversed(tc.scanned)} {
			t.Run(tc.name+"/"+order[0]+" first", func(t *testing.T) {
				st := newTestStore(t)
				warns := ssmiWarningsByService(st)
				var policyCalls parentCalls
				errs := map[string]error{}
				for _, r := range tc.blind {
					errs[r] = ssmiDenied
				}
				h, calls := ssmiGetByRegion(ssmiRegionMapHandlers(&policyCalls, tc.setRegions...), errs)
				totals := scanSSMIEachRegionCtx(t, st, h, calls, tc.scanned, order)
				for _, r := range tc.scanned {
					want := 0
					if r == "us-west-2" {
						want = 6
					}
					if totals[r] != want {
						t.Errorf("%s total = %d, want %d", r, totals[r], want)
					}
				}
				assertSSMIRowsFrom(t, st, "us-west-2")
				if !maps.Equal(calls.n, tc.wantCalls) {
					t.Errorf("GetReplicationSet calls from>to = %v, want %v", calls.n, tc.wantCalls)
				}
				n := len(tc.blind)
				if want := map[string]int{ssmIncidentsService: n, "ssm-incidents:GetReplicationSet": n}; !maps.Equal(warns, want) {
					t.Errorf("warnings by service = %v, want %v (the blind regions' own only)", warns, want)
				}
			})
		}
	}
}

// A reader that is the first candidate elects itself without probing any
// region; a reader behind a readable candidate probes that candidate once,
// elects it, and probes no further.
func TestScanSSMIncidents_ProbesStopAtElection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		region    string
		wantTotal int
		wantCalls map[string]int
	}{
		{"reader first elects itself", "eu-west-1", 6, map[string]int{"eu-west-1>eu-west-1": 1}},
		{"reader behind a readable candidate", "us-west-2", 0, map[string]int{"us-west-2>us-west-2": 1, "us-west-2>eu-west-1": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var policyCalls parentCalls
			h, calls := ssmiGetByRegion(ssmiRegionMapHandlers(&policyCalls, "eu-west-1", "us-west-2", "us-east-1"), nil)
			totals := scanSSMIEachRegionCtx(t, st, h, calls, []string{"eu-west-1", "us-west-2", "us-east-1"}, []string{tc.region})
			if totals[tc.region] != tc.wantTotal {
				t.Errorf("%s total = %d, want %d", tc.region, totals[tc.region], tc.wantTotal)
			}
			if !maps.Equal(calls.n, tc.wantCalls) {
				t.Errorf("GetReplicationSet calls from>to = %v, want %v", calls.n, tc.wantCalls)
			}
		})
	}
}

// A probe that finds the set missing (modelled ResourceNotFoundException)
// skips that candidate: the reader behind it elects itself and stores.
func TestScanSSMIncidents_ProbeNotFoundSkipsCandidate(t *testing.T) {
	st := newTestStore(t)
	var policyCalls parentCalls
	h, calls := ssmiGetByRegion(ssmiRegionMapHandlers(&policyCalls, "eu-west-1", "us-west-2"), map[string]error{"eu-west-1": ssmiGone})
	totals := scanSSMIEachRegionCtx(t, st, h, calls, []string{"eu-west-1", "us-west-2"}, []string{"us-west-2"})
	if totals["us-west-2"] != 6 {
		t.Errorf("us-west-2 total = %d, want 6", totals["us-west-2"])
	}
	assertSSMIRowsFrom(t, st, "us-west-2")
}

// A probe that fails with anything but a denial or a not-found fails the
// probing region's scan with that error, storing nothing: the error does not
// say whether the candidate can store the rows. The same holds for a reader
// and for a blind region.
func TestScanSSMIncidents_ProbeErrorFailsScan(t *testing.T) {
	for _, tc := range []struct {
		name string
		errs map[string]error
	}{
		{"reader", map[string]error{"eu-west-1": ssmiBadInput}},
		{"blind region", map[string]error{"eu-west-1": ssmiBadInput, "us-west-2": ssmiDenied}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var policyCalls parentCalls
			h, _ := ssmiGetByRegion(ssmiRegionMapHandlers(&policyCalls, "eu-west-1", "us-west-2"), tc.errs)
			acct := ssmiAccountCtx(h, []string{"eu-west-1", "us-west-2"})
			total, _, err := scanSSMIncidents(context.Background(), acct, "us-west-2", st, testScanID)
			if !isAPIErrorCode(err, "ValidationException") {
				t.Fatalf("scanSSMIncidents(us-west-2) err = %v, want the probe's ValidationException", err)
			}
			if total != 0 {
				t.Errorf("total = %d, want 0", total)
			}
			assertNoSSMIRows(t, st)
		})
	}
}

// A region that can read at least one listed set elects as usual and stores
// every listed set, an unreadable one with empty attributes, without the blind
// warning.
func TestScanSSMIncidents_PartlyReadableRegionElects(t *testing.T) {
	st := newTestStore(t)
	warns := ssmiWarningsByService(st)
	var policyCalls parentCalls
	h := ssmiRegionMapHandlers(&policyCalls, "us-west-2")
	h["ListReplicationSets"] = func(any) (any, error) {
		return &ssmincidents.ListReplicationSetsOutput{ReplicationSetArns: []string{ssmiRSetARN, ssmiRSetARN + "-2"}}, nil
	}
	get := h["GetReplicationSet"]
	h["GetReplicationSet"] = func(in any) (any, error) {
		if sv(in.(*ssmincidents.GetReplicationSetInput).Arn) != ssmiRSetARN {
			return nil, ssmiDenied
		}
		return get(in)
	}
	totals := scanSSMIEachRegion(t, st, h, []string{"us-west-2", "eu-west-1"}, []string{"eu-west-1", "us-west-2"})
	if want := map[string]int{"us-west-2": 7, "eu-west-1": 0}; !maps.Equal(totals, want) {
		t.Errorf("totals = %v, want %v (2 sets, 2 plans, 3 policies)", totals, want)
	}
	if want := map[string]int{"ssm-incidents:GetReplicationSet": 2}; !maps.Equal(warns, want) {
		t.Errorf("warnings by service = %v, want %v (one denial per region, no blind warning)", warns, want)
	}
	sets := rowsByNativeID(t, st, TypeSSMIncidentsReplicationSet)
	if got, want := nativeIDs(sets), []string{ssmiRSetARN, ssmiRSetARN + "-2"}; !slices.Equal(got, want) {
		t.Fatalf("replication set rows = %v, want %v", got, want)
	}
	if r := sets[ssmiRSetARN+"-2"]; r.AttributesJSON != "{}" || r.Status != nil || sv(r.Region) != "us-west-2" {
		t.Errorf("unread set row attrs/status/region = %s/%v/%q, want {}/nil/us-west-2", r.AttributesJSON, r.Status, sv(r.Region))
	}
}

// A region that lists no replication set elects by the scanned-region fallback
// as before, so the lexically first scanned region stores the response plans
// and their policies, without a warning.
func TestScanSSMIncidents_NoSetListedElectsFirstScanned(t *testing.T) {
	st := newTestStore(t)
	warns := ssmiWarningsByService(st)
	var policyCalls parentCalls
	h := ssmiHandlers(&policyCalls)
	h["ListReplicationSets"] = func(any) (any, error) { return &ssmincidents.ListReplicationSetsOutput{}, nil }
	totals := scanSSMIEachRegion(t, st, h, []string{"us-west-2", "eu-west-1"}, []string{"us-west-2", "eu-west-1"})
	if want := map[string]int{"us-west-2": 0, "eu-west-1": 5}; !maps.Equal(totals, want) {
		t.Errorf("totals = %v, want %v (2 plans, 3 policies)", totals, want)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeSSMIncidentsResponsePlan)); !slices.Equal(got, []string{ssmiPlanP1, ssmiPlanP2}) {
		t.Errorf("response plan rows = %v, want [%s %s]", got, ssmiPlanP1, ssmiPlanP2)
	}
	if len(warns) != 0 {
		t.Errorf("warnings by service = %v, want none", warns)
	}
}

// A region denied ListReplicationSets elects by the scanned-region fallback
// and, once it has stored rows, warns once that it elected without a
// RegionMap. A region that reads the set stays quiet.
func TestScanSSMIncidents_ListDeniedElectionWarns(t *testing.T) {
	for _, tc := range []struct {
		name     string
		list     func(any) (any, error)
		wantWarn int
	}{
		{"list denied", func(any) (any, error) { return nil, ssmiDenied }, 1},
		{"set readable", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, ssmIncidentsService)
			var policyCalls parentCalls
			h := ssmiRegionMapHandlers(&policyCalls, "us-west-2")
			if tc.list != nil {
				h["ListReplicationSets"] = tc.list
			}
			scanSSMIEachRegion(t, st, h, []string{"us-west-2", "eu-west-1"}, []string{"us-west-2", "eu-west-1"})
			if *warns != tc.wantWarn {
				t.Errorf("list-denied election warnings = %d, want %d", *warns, tc.wantWarn)
			}
		})
	}
}

// A region whose every ssm-incidents call is denied (ListReplicationSets
// included) elects the lexically first scanned region without a RegionMap, but
// stores nothing, so it does not warn that it stored the rows; the region that
// reads the set stores them.
func TestScanSSMIncidents_RegionDeniedDoesNotWarn(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, ssmIncidentsService)
	var policyCalls parentCalls
	h := ignoreCtx(ssmiRegionMapHandlers(&policyCalls, "us-west-2"))
	for op, next := range h {
		h[op] = func(ctx context.Context, in any) (any, error) {
			if awsmw.GetRegion(ctx) == "eu-west-1" {
				return nil, ssmiDenied
			}
			return next(ctx, in)
		}
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(h, ssmiDenied)
	acct.scanRegions = []string{"us-west-2", "eu-west-1"}
	totals := map[string]int{}
	for _, r := range acct.scanRegions {
		total, _, err := scanSSMIncidents(context.Background(), acct, r, st, testScanID)
		if err != nil {
			t.Fatalf("scanSSMIncidents(%s): %v", r, err)
		}
		totals[r] = total
	}
	if *warns != 0 {
		t.Errorf("list-denied election warnings = %d, want 0", *warns)
	}
	if want := map[string]int{"us-west-2": 6, "eu-west-1": 0}; !maps.Equal(totals, want) {
		t.Errorf("totals = %v, want %v", totals, want)
	}
}

// A replication set deleted between ListReplicationSets and GetReplicationSet
// (modelled ResourceNotFoundException) is skipped; its sibling is stored.
func TestScanSSMIncidents_ReplicationSetDeletedSinceListing(t *testing.T) {
	st := newTestStore(t)
	var policyCalls parentCalls
	h := ssmiRegionMapHandlers(&policyCalls, testRegion)
	h["ListReplicationSets"] = func(any) (any, error) {
		return &ssmincidents.ListReplicationSetsOutput{ReplicationSetArns: []string{ssmiRSetARN, ssmiRSetARN + "-gone"}}, nil
	}
	get := h["GetReplicationSet"]
	h["GetReplicationSet"] = func(in any) (any, error) {
		if sv(in.(*ssmincidents.GetReplicationSetInput).Arn) != ssmiRSetARN {
			return nil, ssmiGone
		}
		return get(in)
	}
	if _, _, err := scanSSMITopLevel(t, st, h); err != nil {
		t.Fatalf("scanSSMIncidents: %v", err)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeSSMIncidentsReplicationSet)); !slices.Equal(got, []string{ssmiRSetARN}) {
		t.Errorf("replication set rows = %v, want [%s]", got, ssmiRSetARN)
	}
}

// scanSSMIAccount runs the account scan for ssm-incidents alone over the
// configured regions, with optedIn as the regions DescribeRegions reports
// enabled.
// planRegions records the region of every ListResponsePlans call.
func scanSSMIAccount(t *testing.T, h map[string]func(in any) (any, error), configured, optedIn []string, planRegions *parentCalls) *store.Store {
	t.Helper()
	st := newTestStore(t)
	h["DescribeRegions"] = func(any) (any, error) {
		out := &ec2.DescribeRegionsOutput{}
		for _, r := range optedIn {
			out.Regions = append(out.Regions, ec2types.Region{RegionName: ptrStr(r)})
		}
		return out, nil
	}
	withCtx := ignoreCtx(h)
	listPlans := withCtx["ListResponsePlans"]
	withCtx["ListResponsePlans"] = func(ctx context.Context, in any) (any, error) {
		planRegions.add(awsmw.GetRegion(ctx))
		return listPlans(ctx, in)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(withCtx, ssmiDenied)
	acct.Regions = configured
	scanAccount(context.Background(), acct, []string{ssmIncidentsService}, true, st, testScanID)
	return st
}

// A configured region the account has not opted into is never elected, even
// as the lexically first member: the scan never runs there.
func TestScanSSMIncidents_NotOptedInRegionNotElected(t *testing.T) {
	var policyCalls parentCalls
	h := ssmiRegionMapHandlers(&policyCalls, "ap-northeast-1", "us-west-2")
	var planRegions parentCalls
	st := scanSSMIAccount(t, h, []string{"ap-northeast-1", "us-west-2", "eu-west-1"}, []string{"us-west-2", "eu-west-1"}, &planRegions)
	if got := planRegions.distinct(); !slices.Equal(got, []string{"us-west-2"}) {
		t.Errorf("response plans listed in %v, want [us-west-2]", got)
	}
	assertSSMIRowsFrom(t, st, "us-west-2")
}

// The fallback skips a region the dispatcher does not run ssm-incidents in:
// af-south-1 sorts first but the SDK endpoint table does not list the service
// there.
// Depends on the shipped SDK table (awsregions) having no ssm-incidents endpoint in af-south-1.
func TestScanSSMIncidents_FallbackSkipsRegionWithoutService(t *testing.T) {
	var policyCalls parentCalls
	h := ssmiRegionMapHandlers(&policyCalls, "ap-northeast-1")
	regions := []string{"af-south-1", "us-west-2", "eu-west-1"}
	var planRegions parentCalls
	st := scanSSMIAccount(t, h, regions, regions, &planRegions)
	if got := planRegions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("response plans listed in %v, want [eu-west-1]", got)
	}
	assertSSMIRowsFrom(t, st, "eu-west-1")
}

func TestSSMIncidentsCandidates(t *testing.T) {
	set := func(status map[string]ssmitypes.RegionStatus) []ssmIReplicationSet {
		m := map[string]ssmitypes.RegionInfo{}
		for r, st := range status {
			m[r] = ssmitypes.RegionInfo{Status: st}
		}
		return []ssmIReplicationSet{{arn: ssmiRSetARN, set: &ssmitypes.ReplicationSet{RegionMap: m}}}
	}
	const (
		active   = ssmitypes.RegionStatusActive
		failed   = ssmitypes.RegionStatusFailed
		deleting = ssmitypes.RegionStatusDeleting
	)
	for _, tc := range []struct {
		name    string
		sets    []ssmIReplicationSet
		scanned []string
		want    []string
	}{
		{"first scanned member", set(map[string]ssmitypes.RegionStatus{"us-west-2": active, "eu-west-1": active}), []string{"us-west-2", "eu-west-1", "ap-south-1"}, []string{"eu-west-1", "us-west-2", "ap-south-1"}},
		{"failed member skipped", set(map[string]ssmitypes.RegionStatus{"eu-west-1": failed, "us-west-2": active}), []string{"eu-west-1", "us-west-2"}, []string{"us-west-2", "eu-west-1"}},
		{"deleting member skipped", set(map[string]ssmitypes.RegionStatus{"eu-west-1": deleting, "us-west-2": active}), []string{"us-west-2", "eu-west-1"}, []string{"us-west-2", "eu-west-1"}},
		{"no active member", set(map[string]ssmitypes.RegionStatus{"eu-west-1": failed, "us-west-2": deleting}), []string{"ap-south-1", "us-west-2", "eu-west-1"}, []string{"eu-west-1", "us-west-2", "ap-south-1"}},
		{"active in any set wins", append(set(map[string]ssmitypes.RegionStatus{"eu-west-1": active, "us-west-2": active}), set(map[string]ssmitypes.RegionStatus{"eu-west-1": failed})...), []string{"us-west-2", "eu-west-1"}, []string{"eu-west-1", "us-west-2"}},
		{"no member scanned", set(map[string]ssmitypes.RegionStatus{"eu-west-1": active}), []string{"us-east-1", "ap-south-1"}, []string{"ap-south-1", "us-east-1"}},
		{"no set", nil, []string{"us-east-1", "ap-south-1"}, []string{"ap-south-1", "us-east-1"}},
		{"nil body", []ssmIReplicationSet{{arn: ssmiRSetARN}}, []string{"us-east-1", "ap-south-1"}, []string{"ap-south-1", "us-east-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ssmIncidentsCandidates(tc.sets, tc.scanned); !slices.Equal(got, tc.want) {
				t.Errorf("ssmIncidentsCandidates(%v, %v) = %v, want %v", tc.sets, tc.scanned, got, tc.want)
			}
		})
	}
}

// GetResponsePlan per plan: a denial keeps the summary row, a plan deleted
// since listing is skipped with its policies, and any other error fails the
// scan.
func TestScanSSMIncidents_GetResponsePlanErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantPlans []string
		wantErr   string
	}{
		{"denied keeps summary", ssmiDenied, []string{ssmiPlanP1, ssmiPlanP2}, ""},
		{"deleted since listing", ssmiGone, []string{ssmiPlanP2}, ""},
		{"other error fails", ssmiBadInput, nil, "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var policyCalls parentCalls
			h := ssmiHandlers(&policyCalls)
			next := h["GetResponsePlan"]
			h["GetResponsePlan"] = func(in any) (any, error) {
				if sv(in.(*ssmincidents.GetResponsePlanInput).Arn) == ssmiPlanP1 {
					return nil, tc.err
				}
				return next(in)
			}
			_, _, err := scanSSMITopLevel(t, st, h)
			if tc.wantErr != "" {
				assertAPICode(t, err, tc.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			plans := rowsByNativeID(t, st, TypeSSMIncidentsResponsePlan)
			if got := nativeIDs(plans); !slices.Equal(got, tc.wantPlans) {
				t.Errorf("plan rows = %v, want %v", got, tc.wantPlans)
			}
			if got := policyCalls.distinct(); !slices.Equal(got, tc.wantPlans) {
				t.Errorf("policies listed for %v, want %v", got, tc.wantPlans)
			}
			if p1, ok := plans[ssmiPlanP1]; ok {
				var attrs struct{ Name string }
				if err := json.Unmarshal([]byte(p1.AttributesJSON), &attrs); err != nil || attrs.Name != "p1" {
					t.Errorf("p1 attrs = %s, want the summary (Name p1)", p1.AttributesJSON)
				}
			}
		})
	}
}

// ssmiDenyAllIn is withCtx with every op denied in region.
func ssmiDenyAllIn(withCtx map[string]func(context.Context, any) (any, error), region string) map[string]func(context.Context, any) (any, error) {
	for op, next := range withCtx {
		withCtx[op] = func(ctx context.Context, in any) (any, error) {
			if awsmw.GetRegion(ctx) == region {
				return nil, ssmiDenied
			}
			return next(ctx, in)
		}
	}
	return withCtx
}

// A region denied every call (an SCP region deny) lists no set, so a blind
// region's probe of it finds it cannot store and goes on: blind us-west-2 is
// then the first blind region and stores, once, in either scan order.
func TestScanSSMIncidents_AllDeniedRegionIsNotBlind(t *testing.T) {
	scanned := []string{"eu-west-1", "us-west-2"}
	for _, order := range [][]string{scanned, reversed(scanned)} {
		t.Run(order[0]+" first", func(t *testing.T) {
			st := newTestStore(t)
			var policyCalls parentCalls
			h, calls := ssmiGetByRegion(ssmiHandlers(&policyCalls), map[string]error{"us-west-2": ssmiDenied})
			h = ssmiDenyAllIn(h, "eu-west-1")
			totals := scanSSMIEachRegionCtx(t, st, h, calls, scanned, order)
			if want := map[string]int{"eu-west-1": 0, "us-west-2": 6}; !maps.Equal(totals, want) {
				t.Errorf("totals = %v, want %v (1 set, 2 plans, 3 policies)", totals, want)
			}
			assertSSMIRowsFrom(t, st, "us-west-2")
		})
	}
}

// ssmiThreeSetHandlers is ssmiRegionMapHandlers listing three sets, the
// smallest ARN (rs-1) neither first nor last, with GetReplicationSet allowed
// only where readable(region, arn).
func ssmiThreeSetHandlers(policyCalls *parentCalls, readable func(region, arn string) bool, setRegions ...string) (map[string]func(context.Context, any) (any, error), *ssmiGetCalls) {
	h := ssmiRegionMapHandlers(policyCalls, setRegions...)
	h["ListReplicationSets"] = func(any) (any, error) {
		return &ssmincidents.ListReplicationSetsOutput{ReplicationSetArns: []string{ssmiRSetARN + "-2", ssmiRSetARN, ssmiRSetARN + "-3"}}, nil
	}
	return ssmiGetBy(h, func(region, arn string) error {
		if readable(region, arn) {
			return nil
		}
		return ssmiDenied
	})
}

// Every region probes with the lexically smallest listed ARN. With a
// resource-scoped denial that lets one region read only rs-1, a probe with
// rs-1 finds it readable where a probe with the first or last listed ARN would
// find it blind, and the rows would be stored twice. Both a reader's probe and a blind region's
// probe are covered, each in either scan order.
func TestScanSSMIncidents_ProbeUsesSmallestARN(t *testing.T) {
	for _, tc := range []struct {
		name     string
		readable func(region, arn string) bool
		want     string
		total    int
	}{
		// eu-west-1 reads only rs-1 and elects itself; us-west-2 reads both and
		// must find eu-west-1 readable.
		{"reader probe", func(region, arn string) bool { return region == "us-west-2" || arn == ssmiRSetARN }, "eu-west-1", 8},
		// eu-west-1 is blind; us-west-2 reads only rs-1 and elects itself, so
		// eu-west-1 must find it readable.
		{"blind probe", func(region, arn string) bool { return region == "us-west-2" && arn == ssmiRSetARN }, "us-west-2", 8},
	} {
		scanned := []string{"eu-west-1", "us-west-2"}
		for _, order := range [][]string{scanned, reversed(scanned)} {
			t.Run(tc.name+"/"+order[0]+" first", func(t *testing.T) {
				st := newTestStore(t)
				var policyCalls parentCalls
				h, calls := ssmiThreeSetHandlers(&policyCalls, tc.readable, "eu-west-1", "us-west-2")
				totals := scanSSMIEachRegionCtx(t, st, h, calls, scanned, order)
				for _, r := range scanned {
					want := 0
					if r == tc.want {
						want = tc.total
					}
					if totals[r] != want {
						t.Errorf("%s total = %d, want %d", r, totals[r], want)
					}
				}
			})
		}
	}
}

// A candidate whose probe finds the set missing cannot store, on the blind
// path too: eu-west-1 skips ap-south-1, finds blind us-west-2 after it, and
// stores; us-west-2 skips ap-south-1, finds blind eu-west-1 ahead, and stays
// out. ap-south-1's own scan is not run: it lists no set and would elect by
// the scanned-region fallback (a known gap on ssmIncidentsStoreRegion).
func TestScanSSMIncidents_BlindProbeSkipsNotFound(t *testing.T) {
	scanned := []string{"ap-south-1", "eu-west-1", "us-west-2"}
	for _, order := range [][]string{{"eu-west-1", "us-west-2"}, {"us-west-2", "eu-west-1"}} {
		t.Run(order[0]+" first", func(t *testing.T) {
			st := newTestStore(t)
			var policyCalls parentCalls
			h, calls := ssmiGetByRegion(ssmiHandlers(&policyCalls), map[string]error{"ap-south-1": ssmiGone, "eu-west-1": ssmiDenied, "us-west-2": ssmiDenied})
			totals := scanSSMIEachRegionCtx(t, st, h, calls, scanned, order)
			if want := map[string]int{"eu-west-1": 6, "us-west-2": 0}; !maps.Equal(totals, want) {
				t.Errorf("totals = %v, want %v (1 set, 2 plans, 3 policies)", totals, want)
			}
			assertSSMIRowsFrom(t, st, "eu-west-1")
		})
	}
}

// ssmiClientIn builds per-region clients answered by handlers, as
// scanSSMIncidents does from acct.cfg.
func ssmiClientIn(handlers map[string]func(context.Context, any) (any, error)) func(region string) ssmIncidentsAPI {
	cfg := opStubConfigCtx(handlers, ssmiDenied)
	return func(r string) ssmIncidentsAPI {
		return ssmincidents.NewFromConfig(cfg, func(o *ssmincidents.Options) { o.Region = r })
	}
}

// The blind election reports which way it went: a reader found, a blind
// region ahead, or region itself elected, naming the deciding region.
func TestSSMIncidentsBlindElection_Outcome(t *testing.T) {
	blindSets := []ssmIReplicationSet{{arn: ssmiRSetARN}}
	scanned := []string{"ap-south-1", "eu-west-1", "us-west-2"}
	for _, tc := range []struct {
		name      string
		region    string
		errs      map[string]error
		wantOut   ssmIBlindOutcome
		wantOther string
	}{
		{"reader after", "eu-west-1", map[string]error{"ap-south-1": ssmiGone, "eu-west-1": ssmiDenied}, ssmIBlindReader, "us-west-2"},
		{"reader ahead", "us-west-2", map[string]error{"ap-south-1": ssmiGone, "us-west-2": ssmiDenied}, ssmIBlindReader, "eu-west-1"},
		{"blind ahead", "eu-west-1", map[string]error{"ap-south-1": ssmiDenied, "eu-west-1": ssmiDenied, "us-west-2": ssmiDenied}, ssmIBlindAhead, "ap-south-1"},
		{"self", "eu-west-1", map[string]error{"ap-south-1": ssmiGone, "eu-west-1": ssmiDenied, "us-west-2": ssmiDenied}, ssmIBlindSelf, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var policyCalls parentCalls
			h, _ := ssmiGetByRegion(ssmiHandlers(&policyCalls), tc.errs)
			out, other, err := ssmIncidentsBlindElection(context.Background(), ssmiClientIn(h), blindSets, scanned, tc.region)
			if err != nil {
				t.Fatalf("ssmIncidentsBlindElection: %v", err)
			}
			if out != tc.wantOut || other != tc.wantOther {
				t.Errorf("ssmIncidentsBlindElection = (%d, %q), want (%d, %q)", out, other, tc.wantOut, tc.wantOther)
			}
		})
	}
}

// A region missing from the scanned list cannot take part in either election;
// both return an error rather than electing nobody.
func TestSSMIncidentsElection_RegionNotScanned(t *testing.T) {
	var policyCalls parentCalls
	h, _ := ssmiGetByRegion(ssmiHandlers(&policyCalls), nil)
	clientIn := ssmiClientIn(h)
	scanned := []string{"us-west-2"}
	readSets := []ssmIReplicationSet{{arn: ssmiRSetARN, set: &ssmitypes.ReplicationSet{}}}
	if _, err := ssmIncidentsStoreRegion(context.Background(), clientIn, readSets, scanned, "eu-west-1"); err == nil {
		t.Error("ssmIncidentsStoreRegion: err = nil, want an error for a region not scanned")
	}
	if _, _, err := ssmIncidentsBlindElection(context.Background(), clientIn, []ssmIReplicationSet{{arn: ssmiRSetARN}}, scanned, "eu-west-1"); err == nil {
		t.Error("ssmIncidentsBlindElection: err = nil, want an error for a region not scanned")
	}
}

// A probe maps the candidate's GetReplicationSet and, after a denial, its
// ListReplicationSets answer to what the candidate can do in the election.
func TestSSMIncidentsProbeReplicationSet(t *testing.T) {
	listed := func(any) (any, error) {
		return &ssmincidents.ListReplicationSetsOutput{ReplicationSetArns: []string{ssmiRSetARN}}, nil
	}
	empty := func(any) (any, error) { return &ssmincidents.ListReplicationSetsOutput{}, nil }
	emptyMore := func(any) (any, error) { return &ssmincidents.ListReplicationSetsOutput{NextToken: ptrStr("p2")}, nil }
	fail := func(err error) func(any) (any, error) { return func(any) (any, error) { return nil, err } }
	read := func(any) (any, error) { return &ssmincidents.GetReplicationSetOutput{}, nil }
	for _, tc := range []struct {
		name      string
		get, list func(any) (any, error)
		want      ssmIProbe
		wantErr   bool
	}{
		{"readable", read, listed, ssmIProbeReadable, false},
		{"set not found", fail(ssmiGone), listed, ssmIProbeCannotStore, false},
		{"get denied, sets listed", fail(ssmiDenied), listed, ssmIProbeBlind, false},
		{"get denied, no set listed", fail(ssmiDenied), empty, ssmIProbeCannotStore, false},
		{"get denied, empty page with more to come", fail(ssmiDenied), emptyMore, ssmIProbeBlind, false},
		{"get and list denied", fail(ssmiDenied), fail(ssmiDenied), ssmIProbeCannotStore, false},
		{"get denied, list fails", fail(ssmiDenied), fail(ssmiBadInput), 0, true},
		{"get fails", fail(ssmiBadInput), listed, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := ignoreCtx(map[string]func(in any) (any, error){"GetReplicationSet": tc.get, "ListReplicationSets": tc.list})
			got, err := probeSSMIReplicationSet(context.Background(), ssmiClientIn(h), "eu-west-1", "us-west-2", ssmiRSetARN)
			if tc.wantErr {
				if !isAPIErrorCode(err, "ValidationException") {
					t.Fatalf("probe err = %v, want the ValidationException", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("probe = (%d, %v), want (%d, nil)", got, err, tc.want)
			}
		})
	}
}

// A blind region stays out when a probe finds a reader ahead of it: eu-west-1
// reads the set and stores, us-west-2 is blind and stores nothing, in either
// scan order.
func TestScanSSMIncidents_BlindStaysOutForReaderAhead(t *testing.T) {
	scanned := []string{"eu-west-1", "us-west-2"}
	for _, order := range [][]string{scanned, reversed(scanned)} {
		t.Run(order[0]+" first", func(t *testing.T) {
			st := newTestStore(t)
			var policyCalls parentCalls
			h, calls := ssmiGetByRegion(ssmiRegionMapHandlers(&policyCalls, "eu-west-1", "us-west-2"), map[string]error{"us-west-2": ssmiDenied})
			totals := scanSSMIEachRegionCtx(t, st, h, calls, scanned, order)
			if want := map[string]int{"eu-west-1": 6, "us-west-2": 0}; !maps.Equal(totals, want) {
				t.Errorf("totals = %v, want %v (1 set, 2 plans, 3 policies)", totals, want)
			}
			assertSSMIRowsFrom(t, st, "eu-west-1")
		})
	}
}
