package aws

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/amp"
	amptypes "github.com/aws/aws-sdk-go-v2/service/amp/types"
	"github.com/icearp/disco-cli/store"
)

// TestScanAPSExtended_ReadsTags pins that anomaly-detector and
// rule-groups-namespace rows carry the Tags of their list elements, and that
// an untagged element stores none.
func TestScanAPSExtended_ReadsTags(t *testing.T) {
	const (
		wsARN      = "arn:aws:aps:us-east-1:123456789012:workspace/ws-1"
		adTagged   = wsARN + "/anomalydetector/ad-1"
		adUntagged = wsARN + "/anomalydetector/ad-2"
		nsTagged   = "arn:aws:aps:us-east-1:123456789012:rulegroupsnamespace/ws-1/ns1"
		nsUntagged = "arn:aws:aps:us-east-1:123456789012:rulegroupsnamespace/ws-1/ns2"
	)
	tags := map[string]string{"team": "observability"}
	handlers := map[string]func(in any) (any, error){
		"ListAnomalyDetectors": func(any) (any, error) {
			return &amp.ListAnomalyDetectorsOutput{AnomalyDetectors: []amptypes.AnomalyDetectorSummary{
				{Arn: ptrStr(adTagged), Alias: ptrStr("ad1"), Tags: tags},
				{Arn: ptrStr(adUntagged), Alias: ptrStr("ad2")},
			}}, nil
		},
		"ListRuleGroupsNamespaces": func(any) (any, error) {
			return &amp.ListRuleGroupsNamespacesOutput{RuleGroupsNamespaces: []amptypes.RuleGroupsNamespaceSummary{
				{Arn: ptrStr(nsTagged), Name: ptrStr("ns1"), Tags: tags},
				{Arn: ptrStr(nsUntagged), Name: ptrStr("ns2")},
			}}, nil
		},
	}
	st := newTestStore(t)
	client := amp.NewFromConfig(opStubConfig(handlers, errors.New("unexpected op")))
	acct := newTestAccount(testAccountID)
	workspaces := []apsWorkspaceRef{{ID: "ws-1", ARN: wsARN}}

	for _, c := range []struct {
		typ              string
		scan             func() (int, int, error)
		tagged, untagged string
	}{
		{TypeAPSAnomalyDetector, func() (int, int, error) {
			return scanAPSAnomalyDetectors(context.Background(), client, workspaces, acct, testRegion, st, testScanID)
		}, adTagged, adUntagged},
		{TypeAPSRuleGroupsNamespace, func() (int, int, error) {
			return scanAPSRuleGroupsNamespaces(context.Background(), client, workspaces, acct, testRegion, st, testScanID)
		}, nsTagged, nsUntagged},
	} {
		t.Run(c.typ, func(t *testing.T) {
			total, inserted, err := c.scan()
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 2 || inserted != 2 {
				t.Errorf("total=%d inserted=%d, want 2/2", total, inserted)
			}
			rows := rowsByNativeID(t, st, c.typ)
			for _, arn := range []string{c.tagged, c.untagged} {
				if _, ok := rows[arn]; !ok {
					t.Fatalf("%s not stored", arn)
				}
			}
			assertTagsMap(t, rows[c.tagged].TagsJSON, tags)
			if got := rows[c.untagged].TagsJSON; got != nil {
				t.Errorf("untagged %s TagsJSON = %q, want nil", c.untagged, *got)
			}
		})
	}
}

// apsRepeatingClient serves one workspace whose anomaly-detector and
// rule-groups-namespace listings return the token they were sent on page two,
// each page holding one row, and a stored resource policy.
func apsRepeatingClient() *amp.Client {
	page := func(tok *string) (string, *string) {
		if tok == nil {
			return "1", ptrStr("t")
		}
		return "2", ptrStr("t")
	}
	handlers := map[string]func(in any) (any, error){
		"ListWorkspaces": func(any) (any, error) {
			return &amp.ListWorkspacesOutput{Workspaces: []amptypes.WorkspaceSummary{
				{WorkspaceId: ptrStr("ws-1"), Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:workspace/ws-1")},
			}}, nil
		},
		"ListAnomalyDetectors": func(in any) (any, error) {
			n, next := page(in.(*amp.ListAnomalyDetectorsInput).NextToken)
			return &amp.ListAnomalyDetectorsOutput{NextToken: next, AnomalyDetectors: []amptypes.AnomalyDetectorSummary{
				{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:anomalydetector/ws-1/ad-" + n)},
			}}, nil
		},
		"ListRuleGroupsNamespaces": func(in any) (any, error) {
			n, next := page(in.(*amp.ListRuleGroupsNamespacesInput).NextToken)
			return &amp.ListRuleGroupsNamespacesOutput{NextToken: next, RuleGroupsNamespaces: []amptypes.RuleGroupsNamespaceSummary{
				{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:rulegroupsnamespace/ws-1/ns-" + n)},
			}}, nil
		},
		"DescribeResourcePolicy": func(any) (any, error) {
			return &amp.DescribeResourcePolicyOutput{PolicyDocument: ptrStr(`{"Version":"2012-10-17"}`)}, nil
		},
		"ListScrapers": func(any) (any, error) { return &amp.ListScrapersOutput{}, nil },
	}
	return amp.NewFromConfig(opStubConfig(handlers, errors.New("unexpected op")))
}

// TestScanAPSExtended_RepeatedTokenKeepsRowsAndPhases pins that a listing
// whose service repeats its NextToken returns errRepeatedNextToken with the
// rows read so far stored, and that it does not stop the later phases.
func TestScanAPSExtended_RepeatedTokenKeepsRowsAndPhases(t *testing.T) {
	workspaces := []apsWorkspaceRef{{ID: "ws-1", ARN: "arn:aws:aps:us-east-1:123456789012:workspace/ws-1"}}
	acct := newTestAccount(testAccountID)
	for _, c := range []struct {
		typ  string
		scan func(context.Context, apsAPI, []apsWorkspaceRef, *account, string, *store.Store, string) (int, int, error)
	}{
		{TypeAPSAnomalyDetector, scanAPSAnomalyDetectors},
		{TypeAPSRuleGroupsNamespace, scanAPSRuleGroupsNamespaces},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := c.scan(context.Background(), apsRepeatingClient(), workspaces, acct, testRegion, st, testScanID)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v, want errRepeatedNextToken", err)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); total != 2 || n != 2 {
				t.Errorf("total=%d stored=%d, want 2 each (both pages kept)", total, n)
			}
		})
	}

	t.Run("later phases run", func(t *testing.T) {
		st := newTestStore(t)
		total, _, err := scanAPSAll(context.Background(), apsRepeatingClient(), acct, testRegion, st, testScanID)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v, want errRepeatedNextToken", err)
		}
		if total != 6 {
			t.Errorf("total = %d, want 6 (1 workspace, 2 detectors, 2 namespaces, 1 policy)", total)
		}
		if n := len(rowsByNativeID(t, st, TypeAPSResourcePolicy)); n != 1 {
			t.Errorf("resource policies stored = %d, want 1", n)
		}
	})
}

// TestScanAPSExtended_HardErrorKeepsRows pins that a hard error on one
// workspace's listing or policy read keeps the rows already read for it and
// every other workspace's rows, and is returned.
func TestScanAPSExtended_HardErrorKeepsRows(t *testing.T) {
	bad := apiErr("InternalServerException", "boom")
	isBad := func(err error) bool { return isAPIErrorCode(err, "InternalServerException") }
	page := func(wid string, tok *string) (string, *string, error) {
		switch {
		case tok == nil:
			return wid + "-1", ptrStr("t"), nil
		case wid == "ws-bad":
			return "", nil, bad
		default:
			return wid + "-2", nil, nil
		}
	}
	handlers := map[string]func(in any) (any, error){
		"ListAnomalyDetectors": func(in any) (any, error) {
			i := in.(*amp.ListAnomalyDetectorsInput)
			n, next, err := page(sv(i.WorkspaceId), i.NextToken)
			if err != nil {
				return nil, err
			}
			return &amp.ListAnomalyDetectorsOutput{NextToken: next, AnomalyDetectors: []amptypes.AnomalyDetectorSummary{
				{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:anomalydetector/" + n)},
			}}, nil
		},
		"ListRuleGroupsNamespaces": func(in any) (any, error) {
			i := in.(*amp.ListRuleGroupsNamespacesInput)
			n, next, err := page(sv(i.WorkspaceId), i.NextToken)
			if err != nil {
				return nil, err
			}
			return &amp.ListRuleGroupsNamespacesOutput{NextToken: next, RuleGroupsNamespaces: []amptypes.RuleGroupsNamespaceSummary{
				{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:rulegroupsnamespace/" + n)},
			}}, nil
		},
		"DescribeResourcePolicy": func(in any) (any, error) {
			if sv(in.(*amp.DescribeResourcePolicyInput).WorkspaceId) == "ws-bad" {
				return nil, bad
			}
			return &amp.DescribeResourcePolicyOutput{PolicyDocument: ptrStr(`{"Version":"2012-10-17"}`)}, nil
		},
	}
	client := amp.NewFromConfig(opStubConfig(handlers, errors.New("unexpected op")))
	acct := newTestAccount(testAccountID)
	var workspaces []apsWorkspaceRef
	for _, id := range []string{"ws-bad", "ws-ok"} {
		workspaces = append(workspaces, apsWorkspaceRef{ID: id, ARN: "arn:aws:aps:us-east-1:123456789012:workspace/" + id})
	}
	for _, c := range []struct {
		typ  string
		scan func(context.Context, apsAPI, []apsWorkspaceRef, *account, string, *store.Store, string) (int, int, error)
		want int // ws-bad page 1 plus ws-ok's two pages, or ws-ok's policy
	}{
		{TypeAPSAnomalyDetector, scanAPSAnomalyDetectors, 3},
		{TypeAPSRuleGroupsNamespace, scanAPSRuleGroupsNamespaces, 3},
		{TypeAPSResourcePolicy, scanAPSResourcePolicies, 1},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := c.scan(context.Background(), client, workspaces, acct, testRegion, st, testScanID)
			if !isBad(err) {
				t.Fatalf("err = %v, want one carrying InternalServerException", err)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); total != c.want || n != c.want {
				t.Errorf("total=%d stored=%d, want %d each", total, n, c.want)
			}
		})
	}
}

// TestScanAPSExtended_ListDeniedWarnsOnce pins that a denied anomaly-detector
// or rule-groups-namespace listing warns once for all denied workspaces and
// keeps the other workspace's rows, with no error.
func TestScanAPSExtended_ListDeniedWarnsOnce(t *testing.T) {
	deny := apiErr("AccessDeniedException", "not authorized to perform: aps:List")
	handlers := map[string]func(in any) (any, error){
		"ListAnomalyDetectors": func(in any) (any, error) {
			wid := sv(in.(*amp.ListAnomalyDetectorsInput).WorkspaceId)
			if wid != "ws-ok" {
				return nil, deny
			}
			return &amp.ListAnomalyDetectorsOutput{AnomalyDetectors: []amptypes.AnomalyDetectorSummary{
				{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:anomalydetector/ws-ok/ad-1")},
			}}, nil
		},
		"ListRuleGroupsNamespaces": func(in any) (any, error) {
			wid := sv(in.(*amp.ListRuleGroupsNamespacesInput).WorkspaceId)
			if wid != "ws-ok" {
				return nil, deny
			}
			return &amp.ListRuleGroupsNamespacesOutput{RuleGroupsNamespaces: []amptypes.RuleGroupsNamespaceSummary{
				{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:rulegroupsnamespace/ws-ok/ns-1")},
			}}, nil
		},
	}
	client := amp.NewFromConfig(opStubConfig(handlers, errors.New("unexpected op")))
	acct := newTestAccount(testAccountID)
	for _, denied := range [][]string{{"ws-deny1"}, {"ws-deny1", "ws-deny2"}} {
		var workspaces []apsWorkspaceRef
		for _, id := range append([]string{"ws-ok"}, denied...) {
			workspaces = append(workspaces, apsWorkspaceRef{ID: id, ARN: "arn:aws:aps:us-east-1:123456789012:workspace/" + id})
		}
		for _, c := range []struct {
			op, typ string
			scan    func(context.Context, apsAPI, []apsWorkspaceRef, *account, string, *store.Store, string) (int, int, error)
		}{
			{"amp:ListAnomalyDetectors", TypeAPSAnomalyDetector, scanAPSAnomalyDetectors},
			{"amp:ListRuleGroupsNamespaces", TypeAPSRuleGroupsNamespace, scanAPSRuleGroupsNamespaces},
		} {
			t.Run(fmt.Sprintf("%s/%d denied", c.typ, len(denied)), func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, c.op)
				total, _, err := c.scan(context.Background(), client, workspaces, acct, testRegion, st, testScanID)
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if *warns != 1 {
					t.Errorf("warnings = %d, want 1", *warns)
				}
				if n := len(rowsByNativeID(t, st, c.typ)); total != 1 || n != 1 {
					t.Errorf("total=%d stored=%d, want 1 each", total, n)
				}
			})
		}
	}
}

// TestScanAPSResourcePolicies_DeniedWarnsOnce pins that denied
// DescribeResourcePolicy calls warn once for all workspaces, a workspace with
// no policy (ResourceNotFoundException) is skipped silently, and a readable
// policy is stored.
func TestScanAPSResourcePolicies_DeniedWarnsOnce(t *testing.T) {
	handlers := map[string]func(in any) (any, error){
		"DescribeResourcePolicy": func(in any) (any, error) {
			switch sv(in.(*amp.DescribeResourcePolicyInput).WorkspaceId) {
			case "ws-ok":
				return &amp.DescribeResourcePolicyOutput{PolicyDocument: ptrStr(`{"Version":"2012-10-17"}`)}, nil
			case "ws-none":
				return nil, apiErr("ResourceNotFoundException", "no policy")
			default:
				return nil, apiErr("AccessDeniedException", "not authorized to perform: aps:DescribeResourcePolicy")
			}
		},
	}
	for _, denied := range [][]string{{"ws-deny1"}, {"ws-deny1", "ws-deny2"}} {
		t.Run(fmt.Sprintf("%d denied", len(denied)), func(t *testing.T) {
			var workspaces []apsWorkspaceRef
			for _, id := range append([]string{"ws-ok", "ws-none"}, denied...) {
				workspaces = append(workspaces, apsWorkspaceRef{ID: id, ARN: "arn:aws:aps:us-east-1:123456789012:workspace/" + id})
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, "amp:DescribeResourcePolicy")
			client := amp.NewFromConfig(opStubConfig(handlers, errors.New("unexpected op")))
			total, _, err := scanAPSResourcePolicies(context.Background(), client, workspaces, newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil {
				t.Fatalf("scanAPSResourcePolicies: %v", err)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d, want 1", *warns)
			}
			rows := rowsByNativeID(t, st, TypeAPSResourcePolicy)
			if _, ok := rows[workspaces[0].ARN+"/policy"]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total=%d stored=%d ws-ok stored=%v, want 1/1/true", total, len(rows), ok)
			}
		})
	}
}

// TestScanAPSExtended_ListErrorByPage pins how a failed anomaly-detector or
// rule-groups-namespace listing is handled by page. On the first page the
// modeled ResourceNotFoundException (workspace gone) skips silently and
// ValidationException (workspace not ready) skips with one warning. On a later
// page the rows read are kept:
// ResourceNotFoundException (workspace deleted while paged) warns once with no
// error, ValidationException is returned.
func TestScanAPSExtended_ListErrorByPage(t *testing.T) {
	workspaces := []apsWorkspaceRef{{ID: "ws-1", ARN: "arn:aws:aps:us-east-1:123456789012:workspace/ws-1"}}
	acct := newTestAccount(testAccountID)
	for _, c := range []struct {
		name      string
		laterPage bool
		code      string
		wantRows  int
		wantWarns int
		wantErr   bool
	}{
		{"first page not found", false, "ResourceNotFoundException", 0, 0, false},
		{"first page validation", false, "ValidationException", 0, 1, false},
		{"later page not found", true, "ResourceNotFoundException", 1, 1, false},
		{"later page validation", true, "ValidationException", 1, 0, true},
	} {
		failing := func(tok *string) error {
			if (tok != nil) == c.laterPage {
				return apiErr(c.code, "failed")
			}
			return nil
		}
		handlers := map[string]func(in any) (any, error){
			"ListAnomalyDetectors": func(in any) (any, error) {
				if err := failing(in.(*amp.ListAnomalyDetectorsInput).NextToken); err != nil {
					return nil, err
				}
				return &amp.ListAnomalyDetectorsOutput{NextToken: ptrStr("t"), AnomalyDetectors: []amptypes.AnomalyDetectorSummary{
					{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:anomalydetector/ws-1/ad-1")},
				}}, nil
			},
			"ListRuleGroupsNamespaces": func(in any) (any, error) {
				if err := failing(in.(*amp.ListRuleGroupsNamespacesInput).NextToken); err != nil {
					return nil, err
				}
				return &amp.ListRuleGroupsNamespacesOutput{NextToken: ptrStr("t"), RuleGroupsNamespaces: []amptypes.RuleGroupsNamespaceSummary{
					{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:rulegroupsnamespace/ws-1/ns-1")},
				}}, nil
			},
		}
		client := amp.NewFromConfig(opStubConfig(handlers, errors.New("unexpected op")))
		for _, p := range []struct {
			op, typ string
			scan    func(context.Context, apsAPI, []apsWorkspaceRef, *account, string, *store.Store, string) (int, int, error)
		}{
			{"amp:ListAnomalyDetectors", TypeAPSAnomalyDetector, scanAPSAnomalyDetectors},
			{"amp:ListRuleGroupsNamespaces", TypeAPSRuleGroupsNamespace, scanAPSRuleGroupsNamespaces},
		} {
			t.Run(p.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, p.op)
				_, _, err := p.scan(context.Background(), client, workspaces, acct, testRegion, st, testScanID)
				if got := isAPIErrorCode(err, c.code); got != c.wantErr {
					t.Errorf("err = %v, want error %v", err, c.wantErr)
				}
				if err != nil && !c.wantErr {
					t.Errorf("err = %v, want nil", err)
				}
				if n := len(rowsByNativeID(t, st, p.typ)); n != c.wantRows {
					t.Errorf("stored = %d, want %d", n, c.wantRows)
				}
				if *warns != c.wantWarns {
					t.Errorf("warnings = %d, want %d", *warns, c.wantWarns)
				}
			})
		}
	}
}
