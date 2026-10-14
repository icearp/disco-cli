package aws

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/securityhub"
	securityhubtypes "github.com/aws/aws-sdk-go-v2/service/securityhub/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	shDenied     = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: securityhub:List"}
	shNotEnabled = &smithy.GenericAPIError{Code: "InvalidAccessException", Message: "Account 123456789012 is not subscribed to AWS Security Hub"}
	shNotFound   = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
	shBadInput   = &smithy.GenericAPIError{Code: "InvalidInputException", Message: "bad"}
	shInvited    = time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
)

func shHubARN(region string) string {
	return fmt.Sprintf("arn:aws:securityhub:%s:%s:hub/default", region, testAccountID)
}

func shActionTargetARN(region, id string) string {
	return fmt.Sprintf("arn:aws:securityhub:%s:%s:action/custom/%s", region, testAccountID, id)
}

func shActionTarget(region, id string) securityhubtypes.ActionTarget {
	return securityhubtypes.ActionTarget{ActionTargetArn: ptrStr(shActionTargetARN(region, id)), Name: ptrStr("name-" + id), Description: ptrStr("send " + id)}
}

func shMember(acct, status string) securityhubtypes.Member {
	return securityhubtypes.Member{AccountId: ptrStr(acct), AdministratorId: ptrStr(testAccountID), Email: ptrStr(acct + "@example.com"), MemberStatus: ptrStr(status), InvitedAt: &shInvited}
}

// shActionTargetHandler serves page one (at1) then, after fail is checked, page
// two (at2 and an element with no ARN).
func shActionTargetHandler(region string, fail error) func(in any) (any, error) {
	return func(in any) (any, error) {
		if in.(*securityhub.DescribeActionTargetsInput).NextToken == nil {
			return &securityhub.DescribeActionTargetsOutput{ActionTargets: []securityhubtypes.ActionTarget{shActionTarget(region, "at1")}, NextToken: ptrStr("p2")}, nil
		}
		if fail != nil {
			return nil, fail
		}
		return &securityhub.DescribeActionTargetsOutput{ActionTargets: []securityhubtypes.ActionTarget{shActionTarget(region, "at2"), {Name: ptrStr("no-arn")}}}, nil
	}
}

// shMemberHandler records each call's OnlyAssociated, then serves page one
// (111111111111) and, after fail is checked, page two (222222222222 with no
// status, and an element with no AccountId).
func shMemberHandler(onlyAssociated *[]*bool, fail error) func(in any) (any, error) {
	var mu sync.Mutex
	return func(in any) (any, error) {
		p := in.(*securityhub.ListMembersInput)
		mu.Lock()
		*onlyAssociated = append(*onlyAssociated, p.OnlyAssociated)
		mu.Unlock()
		if p.NextToken == nil {
			return &securityhub.ListMembersOutput{Members: []securityhubtypes.Member{shMember("111111111111", "Invited")}, NextToken: ptrStr("p2")}, nil
		}
		if fail != nil {
			return nil, fail
		}
		noStatus := shMember("222222222222", "")
		noStatus.MemberStatus = nil
		return &securityhub.ListMembersOutput{Members: []securityhubtypes.Member{noStatus, {Email: ptrStr("x@example.com")}}}, nil
	}
}

func shClient(handlers map[string]func(in any) (any, error)) *securityhub.Client {
	return securityhub.NewFromConfig(opStubConfig(handlers, shDenied))
}

func scanShActionTargets(t *testing.T, st *store.Store, handler func(in any) (any, error)) (int, error) {
	t.Helper()
	client := shClient(map[string]func(any) (any, error){"DescribeActionTargets": handler})
	total, _, err := scanSecurityHubActionTargets(context.Background(), client, newTestAccount(testAccountID), testRegion, shHubARN(testRegion), st, testScanID)
	return total, err
}

func scanShMembers(t *testing.T, st *store.Store, handler func(in any) (any, error)) (int, error) {
	t.Helper()
	client := shClient(map[string]func(any) (any, error){"ListMembers": handler})
	total, _, err := scanSecurityHubMembers(context.Background(), client, newTestAccount(testAccountID), testRegion, shHubARN(testRegion), st, testScanID)
	return total, err
}

func seedShHub(t *testing.T, st *store.Store) {
	t.Helper()
	upsertTestResource(t, st, "aws", testAccountID, TypeSecurityHubHub, shHubARN(testRegion), testRegion, "{}")
}

func TestScanSecurityHubActionTargets_StoresEveryPageUnderHub(t *testing.T) {
	st := newTestStore(t)
	seedShHub(t, st)
	total, err := scanShActionTargets(t, st, shActionTargetHandler(testRegion, nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeSecurityHubActionTarget)
	at1, at2 := shActionTargetARN(testRegion, "at1"), shActionTargetARN(testRegion, "at2")
	if _, ok := rows[at1]; total != 2 || len(rows) != 2 || !ok {
		t.Fatalf("total = %d, rows = %v; want at1 and at2", total, rows)
	}
	row, ok := rows[at2]
	if !ok {
		t.Fatalf("missing %s in %v", at2, rows)
	}
	if sv(row.Name) != "name-at2" || sv(row.Region) != testRegion || !jsonHasString(t, row.AttributesJSON, "Description", "send at2") {
		t.Errorf("row name/region = %q/%q, attrs = %s", sv(row.Name), sv(row.Region), row.AttributesJSON)
	}
	assertContainsNative(t, st, shHubARN(testRegion), at1)
	assertContainsNative(t, st, shHubARN(testRegion), at2)
}

func TestScanSecurityHubActionTargets_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanShActionTargets(t, st, func(any) (any, error) { return &securityhub.DescribeActionTargetsOutput{}, nil })
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeSecurityHubActionTarget)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// A denial on page two warns once and a disabled hub skips silently, both
// keeping page one's row; any other error fails the phase.
func TestScanSecurityHubActionTargets_FailedPage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fail  error
		warns int
	}{
		{"denied", shDenied, 1},
		{"not enabled", shNotEnabled, 0},
		{"not found", shNotFound, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "securityhub:DescribeActionTargets")
			total, err := scanShActionTargets(t, st, shActionTargetHandler(testRegion, tc.fail))
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.warns {
				t.Errorf("warnings = %d, want %d", *warns, tc.warns)
			}
			if _, ok := rowsByNativeID(t, st, TypeSecurityHubActionTarget)[shActionTargetARN(testRegion, "at1")]; total != 1 || !ok {
				t.Errorf("total = %d, want page one's at1 stored", total)
			}
		})
	}
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		_, err := scanShActionTargets(t, st, shActionTargetHandler(testRegion, shBadInput))
		assertAPICode(t, err, "InvalidInputException")
	})
}

func TestScanSecurityHubMembers_StoresEveryMemberUnderHub(t *testing.T) {
	st := newTestStore(t)
	seedShHub(t, st)
	var onlyAssociated []*bool
	total, err := scanShMembers(t, st, shMemberHandler(&onlyAssociated, nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for i, v := range onlyAssociated {
		if v == nil || *v {
			t.Errorf("call %d OnlyAssociated = %v, want false so invited members are listed", i, v)
		}
	}
	rows := rowsByNativeID(t, st, TypeSecurityHubMember)
	m1 := shHubARN(testRegion) + "/member/111111111111"
	m2 := shHubARN(testRegion) + "/member/222222222222"
	if total != 2 || len(rows) != 2 {
		t.Fatalf("total = %d, rows = %v; want %s and %s", total, rows, m1, m2)
	}
	row, ok := rows[m1]
	if !ok {
		t.Fatalf("missing %s in %v", m1, rows)
	}
	if sv(row.Name) != "111111111111" || sv(row.Status) != "Invited" || sv(row.Region) != testRegion ||
		!jsonHasString(t, row.AttributesJSON, "Email", "111111111111@example.com") {
		t.Errorf("row name/status/region = %q/%q/%q, attrs = %s", sv(row.Name), sv(row.Status), sv(row.Region), row.AttributesJSON)
	}
	if noStatus, ok := rows[m2]; !ok || noStatus.Status != nil {
		t.Errorf("row %s = %+v (found %v), want stored with nil Status", m2, noStatus, ok)
	}
	assertContainsNative(t, st, shHubARN(testRegion), m1)
	assertContainsNative(t, st, shHubARN(testRegion), m2)
}

func TestScanSecurityHubMembers_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanShMembers(t, st, func(any) (any, error) { return &securityhub.ListMembersOutput{}, nil })
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeSecurityHubMember)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// A denial on page two warns once and a hub disabled mid-scan skips silently,
// both keeping page one's row; any other error fails the phase.
func TestScanSecurityHubMembers_FailedPage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fail  error
		warns int
	}{
		{"denied", shDenied, 1},
		{"not enabled", shNotEnabled, 0},
		{"not found", shNotFound, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "securityhub:ListMembers")
			var onlyAssociated []*bool
			total, err := scanShMembers(t, st, shMemberHandler(&onlyAssociated, tc.fail))
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.warns {
				t.Errorf("warnings = %d, want %d", *warns, tc.warns)
			}
			if _, ok := rowsByNativeID(t, st, TypeSecurityHubMember)[shHubARN(testRegion)+"/member/111111111111"]; total != 1 || !ok {
				t.Errorf("total = %d, want page one's member stored", total)
			}
		})
	}
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var onlyAssociated []*bool
		_, err := scanShMembers(t, st, shMemberHandler(&onlyAssociated, shBadInput))
		assertAPICode(t, err, "InvalidInputException")
	})
}

// shTopLevelHandlers answers DescribeHub and DescribeStandards (the last
// pre-existing phase) plus the two new phases, all in region; every other op
// is denied.
func shTopLevelHandlers(region string) map[string]func(in any) (any, error) {
	var onlyAssociated []*bool
	return map[string]func(in any) (any, error){
		"DescribeHub": func(any) (any, error) {
			return &securityhub.DescribeHubOutput{HubArn: ptrStr(shHubARN(region))}, nil
		},
		"DescribeStandards": func(any) (any, error) {
			return &securityhub.DescribeStandardsOutput{Standards: []securityhubtypes.Standard{{StandardsArn: ptrStr(shStandardARN(region)), Name: ptrStr("CIS")}}}, nil
		},
		"DescribeActionTargets": shActionTargetHandler(region, nil),
		"ListMembers":           shMemberHandler(&onlyAssociated, nil),
	}
}

func shStandardARN(region string) string {
	return fmt.Sprintf("arn:aws:securityhub:%s::standards/cis/v/1", region)
}

// The client is pinned to the scanned region, and the new rows sit under the
// hub.
func TestScanSecurityHub_StoresNewChildrenInScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var regions parentCalls
	cfg := opStubConfig(shTopLevelHandlers("eu-west-1"), shDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanSecurityHub(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanSecurityHub: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	hub := shHubARN("eu-west-1")
	member := hub + "/member/111111111111"
	target := shActionTargetARN("eu-west-1", "at1")
	memberRow, ok := rowsByNativeID(t, st, TypeSecurityHubMember)[member]
	if !ok || sv(memberRow.Region) != "eu-west-1" {
		t.Errorf("member row = %+v (found %v), want region eu-west-1", memberRow, ok)
	}
	if row, ok := rowsByNativeID(t, st, TypeSecurityHubActionTarget)[target]; !ok || sv(row.Region) != "eu-west-1" {
		t.Errorf("action target row = %+v (found %v), want region eu-west-1", row, ok)
	}
	hubRow, ok := rowsByNativeID(t, st, TypeSecurityHubHub)[hub]
	if !ok || hubRow.ID == memberRow.ID {
		t.Errorf("hub row id = %q (found %v), member row id = %q; want two distinct rows", hubRow.ID, ok, memberRow.ID)
	}
	assertContainsNative(t, st, hub, member)
	assertContainsNative(t, st, hub, target)
}

// The insight, standards-subscription and product-subscription phases store
// their row under the hub; newTestStore's cleanup fails the test if any of
// those contains pairs is undeclared.
func TestScanSecurityHubChildren_StoredUnderHub(t *testing.T) {
	st := newTestStore(t)
	seedShHub(t, st)
	hub := shHubARN(testRegion)
	insight := fmt.Sprintf("arn:aws:securityhub:%s:%s:insight/%s/custom/i1", testRegion, testAccountID, testAccountID)
	subscription := fmt.Sprintf("arn:aws:securityhub:%s:%s:subscription/cis/v/1", testRegion, testAccountID)
	product := fmt.Sprintf("arn:aws:securityhub:%s:%s:product-subscription/aws/guardduty", testRegion, testAccountID)
	client := shClient(map[string]func(any) (any, error){
		"GetInsights": func(any) (any, error) {
			return &securityhub.GetInsightsOutput{Insights: []securityhubtypes.Insight{{InsightArn: ptrStr(insight), Name: ptrStr("i1")}}}, nil
		},
		"GetEnabledStandards": func(any) (any, error) {
			return &securityhub.GetEnabledStandardsOutput{StandardsSubscriptions: []securityhubtypes.StandardsSubscription{{StandardsSubscriptionArn: ptrStr(subscription), StandardsStatus: securityhubtypes.StandardsStatusReady}}}, nil
		},
		"ListEnabledProductsForImport": func(any) (any, error) {
			return &securityhub.ListEnabledProductsForImportOutput{ProductSubscriptions: []string{product}}, nil
		},
	})
	acct := newTestAccount(testAccountID)
	ctx := context.Background()
	for name, scan := range map[string]func() (int, int, error){
		"insights": func() (int, int, error) {
			return scanSecurityHubInsights(ctx, client, acct, testRegion, hub, st, testScanID)
		},
		"standards": func() (int, int, error) {
			return scanSecurityHubStandards(ctx, client, acct, testRegion, hub, st, testScanID)
		},
		"products": func() (int, int, error) {
			return scanSecurityHubProducts(ctx, client, acct, testRegion, hub, st, testScanID)
		},
	} {
		if total, _, err := scan(); err != nil || total != 1 {
			t.Fatalf("%s: total = %d, err = %v; want 1 row", name, total, err)
		}
	}
	assertContainsNative(t, st, hub, insight)
	assertContainsNative(t, st, hub, subscription)
	assertContainsNative(t, st, hub, product)
	assertContainsEdgesDeclared(t, st)
}

// Each new phase runs after every pre-existing phase, and its hard error
// reaches the caller.
func TestScanSecurityHub_NewPhaseErrorAfterExistingPhases(t *testing.T) {
	for _, op := range []string{"DescribeActionTargets", "ListMembers"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			handlers := shTopLevelHandlers(testRegion)
			handlers[op] = func(any) (any, error) { return nil, shBadInput }
			acct.cfg = opStubConfig(handlers, shDenied)

			_, _, err := scanSecurityHub(context.Background(), acct, testRegion, st, testScanID)
			assertAPICode(t, err, "InvalidInputException")
			if n := len(rowsByNativeID(t, st, TypeSecurityHubStandard)); n != 1 {
				t.Errorf("standard rows = %d, want 1: %s ran before the last pre-existing phase", n, op)
			}
		})
	}
}
