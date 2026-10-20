package aws

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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

// shTopLevelHandlers answers DescribeHub, GetEnabledStandards (a CIS
// subscription), ListSecurityControlDefinitions (shControlDefsHandler),
// DescribeStandards (the last pre-existing phase) plus the three new phases,
// all in region; every other op is denied.
func shTopLevelHandlers(region string) map[string]func(in any) (any, error) {
	var onlyAssociated []*bool
	return map[string]func(in any) (any, error){
		"GetEnabledStandards": func(any) (any, error) {
			sub := fmt.Sprintf("arn:aws:securityhub:%s:%s:subscription/cis-aws-foundations-benchmark/v/1.2.0", region, testAccountID)
			return &securityhub.GetEnabledStandardsOutput{StandardsSubscriptions: []securityhubtypes.StandardsSubscription{{StandardsSubscriptionArn: ptrStr(sub), StandardsArn: ptrStr(shStdCIS), StandardsStatus: securityhubtypes.StandardsStatusReady}}}, nil
		},
		"ListSecurityControlDefinitions":   shControlDefsHandler(region, &parentCalls{}, nil, nil),
		"ListStandardsControlAssociations": shControlAssocHandler(region, &parentCalls{}, nil, nil),
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
	control := shControlNativeID("eu-west-1", shCtlS3)
	assoc := shAssocNativeID("eu-west-1", shCtlS3, shStdCIS)
	if row, ok := rowsByNativeID(t, st, TypeSecurityHubStandardsControlAssociation)[assoc]; !ok || sv(row.Region) != "eu-west-1" {
		t.Errorf("association row = %+v (found %v), want region eu-west-1", row, ok)
	}
	assertContainsNative(t, st, control, assoc)
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
			n, ins, standards, err := scanSecurityHubStandards(ctx, client, acct, testRegion, hub, st, testScanID)
			if len(standards) != 0 {
				t.Errorf("standards = %v, want none: the subscription carries no StandardsArn", standards)
			}
			return n, ins, err
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
	for _, op := range []string{"DescribeActionTargets", "ListMembers", "ListStandardsControlAssociations"} {
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

const (
	shCtlS3  = "S3.1"
	shCtlIAM = "IAM.1"
	shStdCIS = "arn:aws:securityhub:::ruleset/cis-aws-foundations-benchmark/v/1.2.0"
)

func shStdFSBP(region string) string {
	return fmt.Sprintf("arn:aws:securityhub:%s::standards/aws-foundational-security-best-practices/v/1.0.0", region)
}

func shControlNativeID(region, id string) string {
	return fmt.Sprintf("arn:aws:securityhub:%s::security-control/%s", region, id)
}

func shAssocNativeID(region, control, standard string) string {
	return shControlNativeID(region, control) + "/standard/" + standard
}

func shAssoc(control, standard string, status securityhubtypes.AssociationStatus, reason string) securityhubtypes.StandardsControlAssociationSummary {
	a := securityhubtypes.StandardsControlAssociationSummary{
		SecurityControlId: ptrStr(control), StandardsArn: ptrStr(standard), AssociationStatus: status,
		SecurityControlArn: ptrStr(fmt.Sprintf("arn:aws:securityhub:us-east-1:%s:security-control/%s", testAccountID, control)),
	}
	if reason != "" {
		a.UpdatedReason = ptrStr(reason)
	}
	return a
}

// shControlAssocHandler records every call's control id and MaxResults, fails
// a control listed in fail, and otherwise serves S3.1 in two pages (FSBP
// enabled; then CIS disabled and an element with no StandardsArn) and IAM.1 in
// one page (FSBP with no status).
func shControlAssocHandler(region string, calls *parentCalls, maxResults *[]int32, fail map[string]error) func(in any) (any, error) {
	var mu sync.Mutex
	return func(in any) (any, error) {
		p := in.(*securityhub.ListStandardsControlAssociationsInput)
		id := sv(p.SecurityControlId)
		calls.add(id)
		if maxResults != nil {
			mu.Lock()
			*maxResults = append(*maxResults, sdkaws.ToInt32(p.MaxResults))
			mu.Unlock()
		}
		if err := fail[id]; err != nil {
			return nil, err
		}
		var out []securityhubtypes.StandardsControlAssociationSummary
		var next *string
		switch {
		case id == shCtlS3 && p.NextToken == nil:
			out, next = []securityhubtypes.StandardsControlAssociationSummary{shAssoc(id, shStdFSBP(region), securityhubtypes.AssociationStatusEnabled, "")}, ptrStr("p2")
		case id == shCtlS3:
			out = []securityhubtypes.StandardsControlAssociationSummary{shAssoc(id, shStdCIS, securityhubtypes.AssociationStatusDisabled, "not used"), {SecurityControlId: ptrStr(id)}}
		case id == shCtlIAM:
			out = []securityhubtypes.StandardsControlAssociationSummary{shAssoc(id, shStdFSBP(region), "", "")}
		}
		return &securityhub.ListStandardsControlAssociationsOutput{StandardsControlAssociationSummaries: out, NextToken: next}, nil
	}
}

// shSeedControls stores the S3.1 and IAM.1 control rows and returns them as
// the parents scanSHSecurityControls hands on.
func shSeedControls(t *testing.T, st *store.Store) []childParent {
	t.Helper()
	var controls []childParent
	for _, id := range []string{shCtlS3, shCtlIAM} {
		arn := shControlNativeID(testRegion, id)
		upsertTestResource(t, st, "aws", testAccountID, TypeSecurityHubSecurityControl, arn, testRegion, "{}")
		controls = append(controls, childParent{id: id, arn: arn})
	}
	return controls
}

const (
	shCtlEC2 = "EC2.9"
	shStdPCI = "arn:aws:securityhub:us-east-1::standards/pci-dss/v/3.2.1"
)

func shControlDefs(ids ...string) []securityhubtypes.SecurityControlDefinition {
	defs := make([]securityhubtypes.SecurityControlDefinition, len(ids))
	for i, id := range ids {
		if id != "" {
			defs[i].SecurityControlId = ptrStr(id)
		}
	}
	return defs
}

// shControlDefsHandler records each call's StandardsArn ("" for the unfiltered
// catalogue listing) and MaxResults. It fails a standard's first page with
// fail[standard] and FSBP's second page with fail[FSBP+"/p2"], and otherwise
// serves: unfiltered S3.1 and an element with no id; FSBP S3.1, then IAM.1
// and an element with no id; CIS S3.1; PCI EC2.9.
func shControlDefsHandler(region string, calls *parentCalls, maxResults *[]int32, fail map[string]error) func(in any) (any, error) {
	var mu sync.Mutex
	return func(in any) (any, error) {
		p := in.(*securityhub.ListSecurityControlDefinitionsInput)
		standard := sv(p.StandardsArn)
		calls.add(standard)
		if maxResults != nil {
			mu.Lock()
			*maxResults = append(*maxResults, sdkaws.ToInt32(p.MaxResults))
			mu.Unlock()
		}
		if err := fail[standard]; err != nil && p.NextToken == nil {
			return nil, err
		}
		out := &securityhub.ListSecurityControlDefinitionsOutput{}
		switch {
		case standard == "":
			out.SecurityControlDefinitions = shControlDefs(shCtlS3, "")
		case standard == shStdFSBP(region) && p.NextToken == nil:
			out.SecurityControlDefinitions, out.NextToken = shControlDefs(shCtlS3), ptrStr("p2")
		case standard == shStdFSBP(region):
			if err := fail[standard+"/p2"]; err != nil {
				return nil, err
			}
			out.SecurityControlDefinitions = shControlDefs(shCtlIAM, "")
		case standard == shStdCIS:
			out.SecurityControlDefinitions = shControlDefs(shCtlS3)
		case standard == shStdPCI:
			out.SecurityControlDefinitions = shControlDefs(shCtlEC2)
		}
		return out, nil
	}
}

type shAssocCalls struct {
	defs, assocs         parentCalls
	defsMax, assocsMax   []int32
	defsFail, assocsFail map[string]error
}

func scanShControlAssocs(t *testing.T, st *store.Store, standards []string, controls []childParent, c *shAssocCalls) (int, int, error) {
	t.Helper()
	client := shClient(map[string]func(any) (any, error){
		"ListSecurityControlDefinitions":   shControlDefsHandler(testRegion, &c.defs, &c.defsMax, c.defsFail),
		"ListStandardsControlAssociations": shControlAssocHandler(testRegion, &c.assocs, &c.assocsMax, c.assocsFail),
	})
	return scanSecurityHubControlAssociations(context.Background(), client, newTestAccount(testAccountID), testRegion, standards, controls, st, testScanID)
}

func TestScanSecurityHubControlAssociations_StoresEveryPageUnderControl(t *testing.T) {
	st := newTestStore(t)
	controls := shSeedControls(t, st)
	var c shAssocCalls
	total, inserted, err := scanShControlAssocs(t, st, []string{shStdFSBP(testRegion), shStdCIS}, controls, &c)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := c.defs.distinct(); !slices.Equal(got, []string{shStdCIS, shStdFSBP(testRegion)}) {
		t.Errorf("standards listed = %v, want both subscribed standards", got)
	}
	// S3.1 is in both standards but asked once: two pages for S3.1, one for IAM.1.
	if got := c.assocs.distinct(); !slices.Equal(got, []string{shCtlIAM, shCtlS3}) || len(c.assocs.ids) != 3 {
		t.Errorf("association calls = %v, want S3.1 (two pages) and IAM.1 once each", c.assocs.ids)
	}
	for i, m := range append(c.defsMax, c.assocsMax...) {
		if m != 100 {
			t.Errorf("call %d MaxResults = %d, want the model cap 100", i, m)
		}
	}
	s3FSBP := shAssocNativeID(testRegion, shCtlS3, shStdFSBP(testRegion))
	s3CIS := shAssocNativeID(testRegion, shCtlS3, shStdCIS)
	iamFSBP := shAssocNativeID(testRegion, shCtlIAM, shStdFSBP(testRegion))
	rows := rowsByNativeID(t, st, TypeSecurityHubStandardsControlAssociation)
	if got, want := nativeIDs(rows), []string{iamFSBP, s3CIS, s3FSBP}; total != 3 || inserted != 3 || !slices.Equal(got, want) {
		t.Fatalf("total/inserted = %d/%d, rows = %v; want 3/3 and %v", total, inserted, got, want)
	}
	cis := rows[s3CIS]
	if sv(cis.Name) != shCtlS3 || sv(cis.Status) != "DISABLED" || sv(cis.Region) != testRegion || cis.ManagedByProvider ||
		!jsonHasString(t, cis.AttributesJSON, "UpdatedReason", "not used") || !jsonHasString(t, cis.AttributesJSON, "StandardsArn", shStdCIS) {
		t.Errorf("row %s name/status/region/managed = %q/%q/%q/%v, attrs = %s", s3CIS, sv(cis.Name), sv(cis.Status), sv(cis.Region), cis.ManagedByProvider, cis.AttributesJSON)
	}
	if sv(rows[s3FSBP].Status) != "ENABLED" {
		t.Errorf("row %s status = %q, want ENABLED", s3FSBP, sv(rows[s3FSBP].Status))
	}
	if rows[iamFSBP].Status != nil {
		t.Errorf("row %s status = %q, want nil for an absent AssociationStatus", iamFSBP, sv(rows[iamFSBP].Status))
	}
	assertContainsNative(t, st, shControlNativeID(testRegion, shCtlS3), s3FSBP)
	assertContainsNative(t, st, shControlNativeID(testRegion, shCtlS3), s3CIS)
	assertContainsNative(t, st, shControlNativeID(testRegion, shCtlIAM), iamFSBP)
	assertContainsEdgesDeclared(t, st)
}

// With no stored subscription the phase makes no call at all.
func TestScanSecurityHubControlAssociations_NoSubscriptionNoCalls(t *testing.T) {
	st := newTestStore(t)
	controls := shSeedControls(t, st)
	var c shAssocCalls
	total, _, err := scanShControlAssocs(t, st, nil, controls, &c)
	if err != nil || total != 0 || len(c.defs.ids) != 0 || len(c.assocs.ids) != 0 {
		t.Errorf("total = %d, err = %v, calls = %v / %v; want 0 rows, nil, no calls", total, err, c.defs.ids, c.assocs.ids)
	}
}

// Only the controls of subscribed standards are asked: IAM.1 is stored but in
// no subscribed standard.
func TestScanSecurityHubControlAssociations_OnlySubscribedStandardsControlsAsked(t *testing.T) {
	st := newTestStore(t)
	controls := shSeedControls(t, st)
	var c shAssocCalls
	if _, _, err := scanShControlAssocs(t, st, []string{shStdCIS}, controls, &c); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := c.defs.distinct(); !slices.Equal(got, []string{shStdCIS}) {
		t.Errorf("standards listed = %v, want only CIS", got)
	}
	if got := c.assocs.distinct(); !slices.Equal(got, []string{shCtlS3}) {
		t.Errorf("controls asked = %v, want only S3.1", got)
	}
}

// A subscribed standard's control with no stored security-control row is not
// asked, and one warning covers every such control.
func TestScanSecurityHubControlAssociations_UnstoredControlWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	controls := shSeedControls(t, st)
	warns := countOpWarnings(st, shListControlAssociationsOp)
	var c shAssocCalls
	if _, _, err := scanShControlAssocs(t, st, []string{shStdPCI, shStdCIS}, controls, &c); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := c.assocs.distinct(); !slices.Equal(got, []string{shCtlS3}) {
		t.Errorf("controls asked = %v, want S3.1 only (EC2.9 has no stored row)", got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
}

// A standard whose control listing is denied, throttled (transient or
// LimitExceededException) or refused with InvalidAccessException contributes
// the ids read before the error, the other standards still count, and one
// warning names the op; any other error is returned before any association
// call.
func TestScanSecurityHubControlAssociations_FailedStandard(t *testing.T) {
	fsbp := shStdFSBP(testRegion)
	for _, tc := range []struct {
		name      string
		standards []string
		fail      map[string]error
		wantCode  string // "" means the scan returns nil
		wantAsked []string
		wantWarns int
	}{
		{"denied", []string{fsbp, shStdPCI}, map[string]error{fsbp: shDenied}, "", nil, 1},
		{"limit exceeded", []string{fsbp, shStdCIS}, map[string]error{fsbp: apiErr("LimitExceededException", "rate")}, "", []string{shCtlS3}, 1},
		{"throttled", []string{fsbp, shStdCIS}, map[string]error{fsbp: apiErr("ThrottlingException", "slow down")}, "", []string{shCtlS3}, 1},
		{"invalid access", []string{fsbp, shStdCIS}, map[string]error{fsbp: shNotEnabled}, "", []string{shCtlS3}, 1},
		{"denied on page two keeps page one", []string{fsbp}, map[string]error{fsbp + "/p2": shDenied}, "", []string{shCtlS3}, 1},
		{"limit exceeded on page two keeps page one", []string{fsbp}, map[string]error{fsbp + "/p2": apiErr("LimitExceededException", "rate")}, "", []string{shCtlS3}, 1},
		{"other error", []string{fsbp, shStdCIS}, map[string]error{fsbp: shBadInput}, "InvalidInputException", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			controls := shSeedControls(t, st)
			warns := countOpWarnings(st, shListSubscribedControlsOp)
			c := shAssocCalls{defsFail: tc.fail}
			_, _, err := scanShControlAssocs(t, st, tc.standards, controls, &c)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			if got := c.assocs.distinct(); !slices.Equal(got, tc.wantAsked) {
				t.Errorf("controls asked = %v, want %v", got, tc.wantAsked)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

// The CIS v1.2.0 StandardsArn carries no region, so the same control and
// standard in two regions must still be two rows.
func TestScanSecurityHubControlAssociations_RegionlessStandardStaysPerRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		arn := shControlNativeID(region, shCtlS3)
		upsertTestResource(t, st, "aws", testAccountID, TypeSecurityHubSecurityControl, arn, region, "{}")
		client := shClient(map[string]func(any) (any, error){
			"ListSecurityControlDefinitions":   shControlDefsHandler(region, &parentCalls{}, nil, nil),
			"ListStandardsControlAssociations": shControlAssocHandler(region, &parentCalls{}, nil, nil),
		})
		if _, _, err := scanSecurityHubControlAssociations(context.Background(), client, acct, region, []string{shStdCIS}, []childParent{{id: shCtlS3, arn: arn}}, st, testScanID); err != nil {
			t.Fatalf("%s: %v", region, err)
		}
	}
	rows := rowsByNativeID(t, st, TypeSecurityHubStandardsControlAssociation)
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		if row, ok := rows[shAssocNativeID(region, shCtlS3, shStdCIS)]; !ok || sv(row.Region) != region {
			t.Errorf("CIS association in %s = %+v (found %v), want its own row", region, row, ok)
		}
	}
}

func TestScanSecurityHubControlAssociations_NoAssociations(t *testing.T) {
	st := newTestStore(t)
	controls := shSeedControls(t, st)
	client := shClient(map[string]func(any) (any, error){
		"ListSecurityControlDefinitions": shControlDefsHandler(testRegion, &parentCalls{}, nil, nil),
		"ListStandardsControlAssociations": func(any) (any, error) {
			return &securityhub.ListStandardsControlAssociationsOutput{}, nil
		},
	})
	total, _, err := scanSecurityHubControlAssociations(context.Background(), client, newTestAccount(testAccountID), testRegion, []string{shStdFSBP(testRegion)}, controls, st, testScanID)
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeSecurityHubStandardsControlAssociation)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// A denial, an InvalidAccessException, a LimitExceededException or a
// transient error after retries drops only the failing control's rows and
// warns once however many controls hit it; any other error fails the phase
// and is returned.
func TestScanSecurityHubControlAssociations_FailedControl(t *testing.T) {
	throttled := apiErr("ThrottlingException", "slow down")
	limited := apiErr("LimitExceededException", "rate")
	iamRow := shAssocNativeID(testRegion, shCtlIAM, shStdFSBP(testRegion))
	for _, tc := range []struct {
		name      string
		fail      map[string]error
		wantCode  string // "" means the scan returns nil
		wantRows  []string
		wantWarns int
	}{
		{"denied everywhere", map[string]error{shCtlS3: shDenied, shCtlIAM: shDenied}, "", nil, 1},
		{"invalid access everywhere", map[string]error{shCtlS3: shNotEnabled, shCtlIAM: shNotEnabled}, "", nil, 1},
		{"invalid access once", map[string]error{shCtlS3: shNotEnabled}, "", []string{iamRow}, 1},
		{"throttled once", map[string]error{shCtlS3: throttled}, "", []string{iamRow}, 1},
		{"limit exceeded everywhere", map[string]error{shCtlS3: limited, shCtlIAM: limited}, "", nil, 1},
		{"limit exceeded once", map[string]error{shCtlS3: limited}, "", []string{iamRow}, 1},
		{"other error", map[string]error{shCtlS3: shBadInput}, "InvalidInputException", nil, 0},
		{"unmodeled not found", map[string]error{shCtlS3: shNotFound}, "ResourceNotFoundException", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			controls := shSeedControls(t, st)
			warns := countOpWarnings(st, shListControlAssociationsOp)
			c := shAssocCalls{assocsFail: tc.fail}
			_, _, err := scanShControlAssocs(t, st, []string{shStdFSBP(testRegion)}, controls, &c)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeSecurityHubStandardsControlAssociation)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("association rows = %v, want %v", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

// The phase asks only the stored controls of the standards subscribed this
// scan: an element with no SecurityControlId is neither stored nor asked.
func TestScanSecurityHub_ControlAssociationsOverStoredControls(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var defs, assocs parentCalls
	handlers := shTopLevelHandlers(testRegion)
	handlers["ListSecurityControlDefinitions"] = shControlDefsHandler(testRegion, &defs, nil, nil)
	handlers["ListStandardsControlAssociations"] = shControlAssocHandler(testRegion, &assocs, nil, nil)
	acct.cfg = opStubConfig(handlers, shDenied)

	if _, _, err := scanSecurityHub(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanSecurityHub: %v", err)
	}
	if got := defs.distinct(); !slices.Equal(got, []string{"", shStdCIS}) {
		t.Errorf("definition listings = %v, want the catalogue and the subscribed CIS standard", got)
	}
	if got := assocs.distinct(); !slices.Equal(got, []string{shCtlS3}) {
		t.Errorf("controls queried = %v, want [%s]", got, shCtlS3)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeSecurityHubSecurityControl)); !slices.Equal(got, []string{shControlNativeID(testRegion, shCtlS3)}) {
		t.Errorf("control rows = %v, want S3.1 only", got)
	}
	want := []string{shAssocNativeID(testRegion, shCtlS3, shStdCIS), shAssocNativeID(testRegion, shCtlS3, shStdFSBP(testRegion))}
	if got := nativeIDs(rowsByNativeID(t, st, TypeSecurityHubStandardsControlAssociation)); !slices.Equal(got, want) {
		t.Errorf("association rows = %v, want %v", got, want)
	}
}
