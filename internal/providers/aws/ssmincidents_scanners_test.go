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

// A denied GetReplicationSet warns once however many sets hit it, and leaves no
// RegionMap to elect from, so the lexically first scanned region stores every
// listed set with empty attributes, the plans and their policies.
func TestScanSSMIncidents_ReplicationSetDeniedFallsBackToFirstScanned(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "ssm-incidents:GetReplicationSet")
	var policyCalls parentCalls
	h := ssmiRegionMapHandlers(&policyCalls, "us-west-2")
	h["ListReplicationSets"] = func(any) (any, error) {
		return &ssmincidents.ListReplicationSetsOutput{ReplicationSetArns: []string{ssmiRSetARN, ssmiRSetARN + "-2"}}, nil
	}
	h["GetReplicationSet"] = func(any) (any, error) { return nil, ssmiDenied }
	totals := scanSSMIEachRegion(t, st, h, []string{"us-west-2", "eu-west-1"}, []string{"us-west-2", "eu-west-1"})
	if *warns != 2 {
		t.Errorf("warnings = %d, want 2 (one per region)", *warns)
	}
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
}

// The elected region warns once that it stored blind when no RegionMap could
// be read, and stays quiet when one could, or when no set is listed.
func TestScanSSMIncidents_BlindElectionWarns(t *testing.T) {
	for _, tc := range []struct {
		name     string
		list     func(any) (any, error)
		get      func(any) (any, error)
		wantWarn int
	}{
		{"set body denied", nil, func(any) (any, error) { return nil, ssmiDenied }, 1},
		{"list denied", func(any) (any, error) { return nil, ssmiDenied }, nil, 1},
		{"no set listed", func(any) (any, error) { return &ssmincidents.ListReplicationSetsOutput{}, nil }, nil, 0},
		{"set readable", nil, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, ssmIncidentsService)
			var policyCalls parentCalls
			h := ssmiRegionMapHandlers(&policyCalls, "us-west-2")
			if tc.list != nil {
				h["ListReplicationSets"] = tc.list
			}
			if tc.get != nil {
				h["GetReplicationSet"] = tc.get
			}
			scanSSMIEachRegion(t, st, h, []string{"us-west-2", "eu-west-1"}, []string{"us-west-2", "eu-west-1"})
			if *warns != tc.wantWarn {
				t.Errorf("blind-election warnings = %d, want %d", *warns, tc.wantWarn)
			}
		})
	}
}

// A region whose every ssm-incidents call is denied elects itself blind but
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
		t.Errorf("blind-election warnings = %d, want 0", *warns)
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

func TestSSMIncidentsStoreRegion(t *testing.T) {
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
		want    string
	}{
		{"first scanned member", set(map[string]ssmitypes.RegionStatus{"us-west-2": active, "eu-west-1": active}), []string{"us-west-2", "eu-west-1", "ap-south-1"}, "eu-west-1"},
		{"failed member skipped", set(map[string]ssmitypes.RegionStatus{"eu-west-1": failed, "us-west-2": active}), []string{"eu-west-1", "us-west-2"}, "us-west-2"},
		{"deleting member skipped", set(map[string]ssmitypes.RegionStatus{"eu-west-1": deleting, "us-west-2": active}), []string{"us-west-2", "eu-west-1"}, "us-west-2"},
		{"no active member", set(map[string]ssmitypes.RegionStatus{"eu-west-1": failed, "us-west-2": deleting}), []string{"ap-south-1", "us-west-2", "eu-west-1"}, "eu-west-1"},
		{"active in any set wins", append(set(map[string]ssmitypes.RegionStatus{"eu-west-1": active, "us-west-2": active}), set(map[string]ssmitypes.RegionStatus{"eu-west-1": failed})...), []string{"us-west-2", "eu-west-1"}, "eu-west-1"},
		{"no member scanned", set(map[string]ssmitypes.RegionStatus{"eu-west-1": active}), []string{"us-east-1", "ap-south-1"}, "ap-south-1"},
		{"no set", nil, []string{"us-east-1", "ap-south-1"}, "ap-south-1"},
		{"nil body", []ssmIReplicationSet{{arn: ssmiRSetARN}}, []string{"us-east-1", "ap-south-1"}, "ap-south-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ssmIncidentsStoreRegion(tc.sets, tc.scanned); got != tc.want {
				t.Errorf("ssmIncidentsStoreRegion(%v, %v) = %q, want %q", tc.sets, tc.scanned, got, tc.want)
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
