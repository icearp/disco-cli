package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sync/atomic"
	"testing"

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
