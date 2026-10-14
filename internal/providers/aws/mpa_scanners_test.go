package aws

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/mpa"
	mpatypes "github.com/aws/aws-sdk-go-v2/service/mpa/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	mpaDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: mpa:List"}
	mpaBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	mpaConflict = &smithy.GenericAPIError{Code: "ConflictException", Message: "conflict"}
	mpaGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
)

const (
	mpaTeam1ARN     = "arn:aws:mpa:us-east-1:123456789012:approval-team/t1"
	mpaTeam2ARN     = "arn:aws:mpa:us-east-1:123456789012:approval-team/t2"
	mpaTeam3ARN     = "arn:aws:mpa:us-east-1:123456789012:approval-team/t3"
	mpaSourceARN    = "arn:aws:mpa:us-east-1:123456789012:identity-source/s1"
	mpaPermARN      = "arn:aws:ram::aws:permission/AWSRAMDefaultPermissionMPA"
	mpaCatalogueARN = "arn:aws:mpa:::aws:policy/backup.amazonaws.com/CreateRestoreAccessVault"
)

// mpaPolicyHandler serves ListResourcePolicies: t1 gets two pages (an AWS_RAM
// policy; then an AWS_MANAGED one of the same name and two elements missing
// the name or the type), every other team one page holding an AWS_RAM policy
// with t1's name and PolicyArn. fail maps a team ARN to the error its first
// call returns. Every call's team is recorded.
func mpaPolicyHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*mpa.ListResourcePoliciesInput)
		team := sv(req.ResourceArn)
		calls.add(team)
		if err := fail[team]; err != nil {
			return nil, err
		}
		shared := mpatypes.ListResourcePoliciesResponseResourcePolicy{
			PolicyArn: ptrStr(mpaPermARN), PolicyName: ptrStr("share"), PolicyType: mpatypes.PolicyTypeAwsRam,
		}
		if team != mpaTeam1ARN {
			return &mpa.ListResourcePoliciesOutput{ResourcePolicies: []mpatypes.ListResourcePoliciesResponseResourcePolicy{shared}}, nil
		}
		if req.NextToken == nil {
			return &mpa.ListResourcePoliciesOutput{ResourcePolicies: []mpatypes.ListResourcePoliciesResponseResourcePolicy{shared}, NextToken: ptrStr("p2")}, nil
		}
		return &mpa.ListResourcePoliciesOutput{ResourcePolicies: []mpatypes.ListResourcePoliciesResponseResourcePolicy{
			{PolicyArn: ptrStr(mpaCatalogueARN), PolicyName: ptrStr("share"), PolicyType: mpatypes.PolicyTypeAwsManaged},
			{PolicyArn: ptrStr(mpaPermARN), PolicyType: mpatypes.PolicyTypeAwsRam},
			{PolicyArn: ptrStr(mpaPermARN), PolicyName: ptrStr("no-type")},
		}}, nil
	}
}

// scanMPAPolicies runs the resource policy phase over the given team ARNs,
// seeding each team row first so its contains edge has a parent.
func scanMPAPolicies(t *testing.T, st *store.Store, handler func(in any) (any, error), teamARNs ...string) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	var teams []childParent
	var seed []*store.Resource
	for _, arn := range teamARNs {
		teams = append(teams, childParent{id: arn, arn: arn})
		seed = append(seed, &store.Resource{Provider: "aws", AccountID: testAccountID, Type: TypeMPAApprovalTeam, NativeID: arn, DiscoveredBy: testScanID})
	}
	if _, err := st.UpsertResources(seed); err != nil {
		t.Fatalf("seed teams: %v", err)
	}
	client := mpa.NewFromConfig(opStubConfig(map[string]func(any) (any, error){"ListResourcePolicies": handler}, mpaDenied))
	total, _, err := scanMPAResourcePolicies(context.Background(), client, acct, testRegion, st, testScanID, teams)
	return total, err
}

// Both pages of every team are stored under the team; a policy name reused
// across teams and across policy types within a team stays one row each.
func TestScanMPAResourcePolicies_StoresEveryPageOfEveryTeam(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanMPAPolicies(t, st, mpaPolicyHandler(&calls, nil), mpaTeam1ARN, mpaTeam2ARN)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{mpaTeam1ARN, mpaTeam2ARN}) {
		t.Errorf("teams listed = %v, want [t1 t2]", got)
	}
	rows := rowsByNativeID(t, st, TypeMPAResourcePolicy)
	want := []string{
		"arn:aws:mpa:us-east-1:123456789012:approval-team/t1/resource-policy/AWS_RAM/share",
		"arn:aws:mpa:us-east-1:123456789012:approval-team/t1/resource-policy/AWS_MANAGED/share",
		"arn:aws:mpa:us-east-1:123456789012:approval-team/t2/resource-policy/AWS_RAM/share",
	}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("total = %d, rows = %v; want %v", total, rows, want)
	}
	for _, id := range want {
		if _, ok := rows[id]; !ok {
			t.Errorf("missing %s in %v", id, rows)
		}
	}
	p := rows[want[0]]
	if sv(p.Name) != "share" || sv(p.Region) != "us-east-1" || !strings.Contains(p.AttributesJSON, `"PolicyArn":"arn:aws:ram::aws:permission/AWSRAMDefaultPermissionMPA"`) {
		t.Errorf("name/region = %q/%q, attrs = %s", sv(p.Name), sv(p.Region), p.AttributesJSON)
	}
	assertContainsNative(t, st, mpaTeam1ARN, want[0])
	assertContainsNative(t, st, mpaTeam1ARN, want[1])
	assertContainsNative(t, st, mpaTeam2ARN, want[2])
	assertContainsEdgesDeclared(t, st)
}

func TestScanMPAResourcePolicies_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanMPAPolicies(t, st, func(any) (any, error) { return &mpa.ListResourcePoliciesOutput{}, nil }, mpaTeam1ARN)
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeMPAResourcePolicy)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// A team deleted after it was listed is skipped silently; a denial warns once
// for the op and keeps the other teams; any other error fails.
func TestScanMPAResourcePolicies_FailedTeam(t *testing.T) {
	const op = "mpa:ListResourcePolicies"
	t.Run("gone", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, op)
		var calls parentCalls
		total, err := scanMPAPolicies(t, st, mpaPolicyHandler(&calls, map[string]error{mpaTeam1ARN: mpaGone}), mpaTeam1ARN, mpaTeam2ARN)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if _, ok := rowsByNativeID(t, st, TypeMPAResourcePolicy)[mpaTeam2ARN+"/resource-policy/AWS_RAM/share"]; total != 1 || !ok || *warns != 0 {
			t.Errorf("total = %d, t2 policy stored = %v, warnings = %d; want 1, true, 0", total, ok, *warns)
		}
	})
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, op)
		var calls parentCalls
		total, err := scanMPAPolicies(t, st, mpaPolicyHandler(&calls, map[string]error{mpaTeam2ARN: mpaDenied, mpaTeam3ARN: mpaDenied}), mpaTeam1ARN, mpaTeam2ARN, mpaTeam3ARN)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if *warns != 1 {
			t.Errorf("warnings = %d, want 1", *warns)
		}
		if n := len(rowsByNativeID(t, st, TypeMPAResourcePolicy)); total != 2 || n != 2 {
			t.Errorf("total = %d, rows = %d; want t1's 2 policies", total, n)
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		_, err := scanMPAPolicies(t, st, mpaPolicyHandler(&calls, map[string]error{mpaTeam2ARN: mpaConflict}), mpaTeam1ARN, mpaTeam2ARN)
		assertAPICode(t, err, "ConflictException")
	})
}

// mpaTopLevelHandlers answers every op: approval teams t1 and t2 on two pages,
// one identity source, and the resource policies of mpaPolicyHandler.
func mpaTopLevelHandlers(policyCalls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListApprovalTeams": func(in any) (any, error) {
			if in.(*mpa.ListApprovalTeamsInput).NextToken == nil {
				return &mpa.ListApprovalTeamsOutput{ApprovalTeams: []mpatypes.ListApprovalTeamsResponseApprovalTeam{
					{Arn: ptrStr(mpaTeam1ARN), Name: ptrStr("team-1"), Status: mpatypes.ApprovalTeamStatusActive},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &mpa.ListApprovalTeamsOutput{ApprovalTeams: []mpatypes.ListApprovalTeamsResponseApprovalTeam{
				{Arn: ptrStr(mpaTeam2ARN), Name: ptrStr("team-2"), Status: mpatypes.ApprovalTeamStatusActive},
				{Name: ptrStr("no-arn")},
			}}, nil
		},
		"ListIdentitySources": func(any) (any, error) {
			return &mpa.ListIdentitySourcesOutput{IdentitySources: []mpatypes.IdentitySourceForList{
				{IdentitySourceArn: ptrStr(mpaSourceARN), Status: mpatypes.IdentitySourceStatusActive},
			}}, nil
		},
		"ListResourcePolicies": mpaPolicyHandler(policyCalls, nil),
	}
}

// Resource policies are listed for every approval team the team phase
// stored, and land under it.
func TestScanMPA_StoresPoliciesOfListedTeams(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var policyCalls parentCalls
	acct.cfg = opStubConfig(mpaTopLevelHandlers(&policyCalls), mpaDenied)

	total, _, err := scanMPA(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanMPA: %v", err)
	}
	if got := policyCalls.distinct(); !slices.Equal(got, []string{mpaTeam1ARN, mpaTeam2ARN}) {
		t.Errorf("policies listed for %v, want [t1 t2]", got)
	}
	// 2 teams, 1 identity source, 3 policies (t1: 2, t2: 1).
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	assertContainsNative(t, st, mpaTeam2ARN, "arn:aws:mpa:us-east-1:123456789012:approval-team/t2/resource-policy/AWS_RAM/share")
	assertContainsEdgesDeclared(t, st)
}

func TestScanMPA_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var policyCalls, regions parentCalls
	cfg := opStubConfig(mpaTopLevelHandlers(&policyCalls), mpaDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanMPA(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanMPA: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	if n := len(policyCalls.distinct()); n != 2 {
		t.Errorf("policy list calls = %d teams, want 2", n)
	}
}

// The resource policy phase runs after both existing phases. A hard error
// reaches the caller with the counts stored before it. A ValidationException
// (a team ARN rejected, so the inferred ResourceArn binding is wrong) warns
// once and returns no error. Either way the earlier rows survive.
func TestScanMPA_PolicyErrorAfterExistingPhases(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantWarns int
	}{
		{name: "hard error", err: mpaConflict, wantCode: "ConflictException"},
		{name: "team ARN rejected", err: mpaBadInput, wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			warns := countOpWarnings(st, "mpa:ListResourcePolicies")
			var policyCalls parentCalls
			handlers := mpaTopLevelHandlers(&policyCalls)
			var failed atomic.Bool
			handlers["ListResourcePolicies"] = func(any) (any, error) {
				failed.Store(true)
				return nil, tc.err
			}
			acct.cfg = opStubConfig(handlers, mpaDenied)

			total, _, err := scanMPA(context.Background(), acct, testRegion, st, testScanID)
			if !failed.Load() {
				t.Fatal("ListResourcePolicies never called")
			}
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
				if total != 3 {
					t.Errorf("total = %d, want 3 (2 teams + 1 identity source)", total)
				}
			} else if err != nil || total != 3 {
				t.Errorf("total = %d, err = %v; want 3 (2 teams + 1 identity source), nil", total, err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if n := len(rowsByNativeID(t, st, TypeMPAApprovalTeam)); n != 2 {
				t.Errorf("team rows = %d, want 2", n)
			}
			if n := len(rowsByNativeID(t, st, TypeMPAIdentitySource)); n != 1 {
				t.Errorf("identity source rows = %d, want 1", n)
			}
			if n := len(rowsByNativeID(t, st, TypeMPAResourcePolicy)); n != 0 {
				t.Errorf("policy rows = %d, want 0", n)
			}
		})
	}
}

// A denied team list warns, lists no policies and keeps the identity sources.
func TestScanMPA_DeniedTeamsKeepIdentitySources(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	warns := countOpWarnings(st, "mpa:ListApprovalTeams")
	var policyCalls parentCalls
	handlers := mpaTopLevelHandlers(&policyCalls)
	delete(handlers, "ListApprovalTeams")
	acct.cfg = opStubConfig(handlers, mpaDenied)

	total, _, err := scanMPA(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || total != 1 || *warns != 1 || len(policyCalls.distinct()) != 0 {
		t.Errorf("total = %d, err = %v, warnings = %d, policy calls = %v; want 1, nil, 1, none", total, err, *warns, policyCalls.distinct())
	}
}
