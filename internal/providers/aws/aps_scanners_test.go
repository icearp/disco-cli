package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/amp"
	amptypes "github.com/aws/aws-sdk-go-v2/service/amp/types"
	"github.com/icearp/disco-cli/store"
)

const apsTestWorkspaceARN = "arn:aws:aps:us-east-1:123456789012:workspace/ws-1"

// apsAllHandlers serves one workspace, one scraper, and one anomaly detector,
// rule-groups namespace and resource policy for it. Each handler counts its
// calls in calls.
func apsAllHandlers(calls map[string]int) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListWorkspaces": func(any) (any, error) {
			return &amp.ListWorkspacesOutput{Workspaces: []amptypes.WorkspaceSummary{
				{WorkspaceId: ptrStr("ws-1"), Arn: ptrStr(apsTestWorkspaceARN)},
			}}, nil
		},
		"ListScrapers": func(any) (any, error) {
			return &amp.ListScrapersOutput{Scrapers: []amptypes.ScraperSummary{
				{ScraperId: ptrStr("s-1"), Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:scraper/s-1")},
			}}, nil
		},
		"ListAnomalyDetectors": func(any) (any, error) {
			return &amp.ListAnomalyDetectorsOutput{AnomalyDetectors: []amptypes.AnomalyDetectorSummary{
				{Arn: ptrStr(apsTestWorkspaceARN + "/anomalydetector/ad-1")},
			}}, nil
		},
		"ListRuleGroupsNamespaces": func(any) (any, error) {
			return &amp.ListRuleGroupsNamespacesOutput{RuleGroupsNamespaces: []amptypes.RuleGroupsNamespaceSummary{
				{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:rulegroupsnamespace/ws-1/ns-1")},
			}}, nil
		},
		"DescribeResourcePolicy": func(any) (any, error) {
			return &amp.DescribeResourcePolicyOutput{PolicyDocument: ptrStr(`{"Version":"2012-10-17"}`)}, nil
		},
	}
	for op, f := range h {
		h[op] = func(in any) (any, error) {
			calls[op]++
			return f(in)
		}
	}
	return h
}

// TestScanAPSAll_PhaseErrorRunsLaterPhases pins that a failed workspace or
// scraper listing does not stop the phases after it: the error is returned and
// every other phase's rows are stored.
func TestScanAPSAll_PhaseErrorRunsLaterPhases(t *testing.T) {
	bad := apiErr("InternalServerException", "boom")
	for _, c := range []struct {
		failOp  string
		wantTyp []string // types still stored
	}{
		{"ListWorkspaces", []string{TypeAPSScraper}},
		{"ListScrapers", []string{TypeAPSWorkspace, TypeAPSAnomalyDetector, TypeAPSRuleGroupsNamespace, TypeAPSResourcePolicy}},
	} {
		t.Run(c.failOp, func(t *testing.T) {
			h := apsAllHandlers(map[string]int{})
			h[c.failOp] = func(any) (any, error) { return nil, bad }
			st := newTestStore(t)
			client := amp.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
			total, _, err := scanAPSAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if !errors.Is(err, bad) {
				t.Fatalf("err = %v, want the %s error", err, c.failOp)
			}
			if total != len(c.wantTyp) {
				t.Errorf("total = %d, want %d", total, len(c.wantTyp))
			}
			for _, typ := range c.wantTyp {
				if n := len(rowsByNativeID(t, st, typ)); n != 1 {
					t.Errorf("%s stored = %d, want 1", typ, n)
				}
			}
		})
	}
}

// TestScanAPSAll_StoreWriteStopsPhases pins that a failed store write stops the
// phases after it, both after the workspace phase and between the
// per-workspace phases, and is returned as ErrStoreWrite.
func TestScanAPSAll_StoreWriteStopsPhases(t *testing.T) {
	for _, c := range []struct {
		closeIn  string // the op whose call closes the store
		notAfter string // a later op that must not be called
	}{
		{"ListWorkspaces", "ListScrapers"},
		{"ListScrapers", "ListAnomalyDetectors"},
		{"ListAnomalyDetectors", "ListRuleGroupsNamespaces"},
		{"ListRuleGroupsNamespaces", "DescribeResourcePolicy"},
	} {
		t.Run(c.closeIn, func(t *testing.T) {
			calls := map[string]int{}
			h := apsAllHandlers(calls)
			st, closeStore := openClosingStore(t)
			serve := h[c.closeIn]
			h[c.closeIn] = func(in any) (any, error) {
				closeStore()
				return serve(in)
			}
			client := amp.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
			_, _, err := scanAPSAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			if calls[c.notAfter] != 0 {
				t.Errorf("%s called %d times after the failed write, want 0", c.notAfter, calls[c.notAfter])
			}
		})
	}
}

// TestScanAPSAll_DeniedWorkspacesListedOnce pins that a denied ListWorkspaces
// is called and warned once, with the scrapers still stored.
func TestScanAPSAll_DeniedWorkspacesListedOnce(t *testing.T) {
	calls := map[string]int{}
	h := apsAllHandlers(calls)
	h["ListWorkspaces"] = func(any) (any, error) {
		calls["ListWorkspaces"]++
		return nil, apiErr("AccessDeniedException", "not authorized to perform: aps:ListWorkspaces")
	}
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	client := amp.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	total, _, err := scanAPSAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if calls["ListWorkspaces"] != 1 {
		t.Errorf("ListWorkspaces calls = %d, want 1", calls["ListWorkspaces"])
	}
	if len(warnings) != 1 || warnings[0].Service != "amp:ListWorkspaces" {
		t.Errorf("warnings = %+v, want one under amp:ListWorkspaces", warnings)
	}
	if total != 1 || len(rowsByNativeID(t, st, TypeAPSScraper)) != 1 {
		t.Errorf("total = %d, want the 1 scraper stored", total)
	}
}

// TestScanAPSAll_EmptyARNWarnsOncePerOp pins that listed items with no ARN
// are not stored and warn once per op, beside the stored items.
func TestScanAPSAll_EmptyARNWarnsOncePerOp(t *testing.T) {
	h := apsAllHandlers(map[string]int{})
	h["ListWorkspaces"] = func(any) (any, error) {
		return &amp.ListWorkspacesOutput{Workspaces: []amptypes.WorkspaceSummary{
			{WorkspaceId: ptrStr("ws-1"), Arn: ptrStr(apsTestWorkspaceARN)},
			{WorkspaceId: ptrStr("ws-2")},
			{WorkspaceId: ptrStr("ws-3")},
		}}, nil
	}
	h["ListScrapers"] = func(any) (any, error) {
		return &amp.ListScrapersOutput{Scrapers: []amptypes.ScraperSummary{{ScraperId: ptrStr("s-1")}, {ScraperId: ptrStr("s-2")}}}, nil
	}
	h["ListAnomalyDetectors"] = func(any) (any, error) {
		return &amp.ListAnomalyDetectorsOutput{AnomalyDetectors: []amptypes.AnomalyDetectorSummary{
			{Arn: ptrStr(apsTestWorkspaceARN + "/anomalydetector/ad-1")}, {Alias: ptrStr("a")}, {Alias: ptrStr("b")},
		}}, nil
	}
	h["ListRuleGroupsNamespaces"] = func(any) (any, error) {
		return &amp.ListRuleGroupsNamespacesOutput{RuleGroupsNamespaces: []amptypes.RuleGroupsNamespaceSummary{
			{Name: ptrStr("a")}, {Name: ptrStr("b")},
		}}, nil
	}
	st := newTestStore(t)
	warns := map[string]*int{}
	ops := []string{"amp:ListWorkspaces", "amp:ListScrapers", "amp:ListAnomalyDetectors", "amp:ListRuleGroupsNamespaces"}
	for _, op := range ops {
		warns[op] = countOpWarnings(st, op)
	}
	client := amp.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	if _, _, err := scanAPSAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	for _, op := range ops {
		if *warns[op] != 1 {
			t.Errorf("%s warnings = %d, want 1", op, *warns[op])
		}
	}
	for typ, want := range map[string]int{
		TypeAPSWorkspace: 1, TypeAPSScraper: 0, TypeAPSAnomalyDetector: 1, TypeAPSRuleGroupsNamespace: 0,
	} {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s stored = %d, want %d", typ, n, want)
		}
	}
}

// TestScanAPSAll_WorkspacesPageFailsKeepsChildren pins that when a later
// ListWorkspaces page fails, the workspaces of the pages before still feed
// the anomaly-detector, rule-groups-namespace and resource-policy phases.
func TestScanAPSAll_WorkspacesPageFailsKeepsChildren(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		wantErr bool
	}{
		{"hard error", apiErr("InternalServerException", "boom"), true},
		{"denied", apiErr("AccessDeniedException", "not authorized to perform: aps:ListWorkspaces"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := apsAllHandlers(map[string]int{})
			firstPage := h["ListWorkspaces"]
			h["ListWorkspaces"] = func(in any) (any, error) {
				if in.(*amp.ListWorkspacesInput).NextToken != nil {
					return nil, c.err
				}
				out, err := firstPage(in)
				out.(*amp.ListWorkspacesOutput).NextToken = ptrStr("t")
				return out, err
			}
			st := newTestStore(t)
			client := amp.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
			_, _, err := scanAPSAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if got := errors.Is(err, c.err); got != c.wantErr || !c.wantErr && err != nil {
				t.Fatalf("err = %v, want the page-2 error returned: %v", err, c.wantErr)
			}
			for _, typ := range []string{TypeAPSWorkspace, TypeAPSAnomalyDetector, TypeAPSRuleGroupsNamespace, TypeAPSResourcePolicy} {
				if n := len(rowsByNativeID(t, st, typ)); n != 1 {
					t.Errorf("%s stored = %d, want 1", typ, n)
				}
			}
		})
	}
}

// TestScanAPSWorkspaces_NoIDStoredAndWarns pins that a workspace with an ARN
// but no WorkspaceId is stored, is not handed to the per-workspace phases,
// and warns once.
func TestScanAPSWorkspaces_NoIDStoredAndWarns(t *testing.T) {
	h := map[string]func(in any) (any, error){
		"ListWorkspaces": func(any) (any, error) {
			return &amp.ListWorkspacesOutput{Workspaces: []amptypes.WorkspaceSummary{
				{WorkspaceId: ptrStr("ws-1"), Arn: ptrStr(apsTestWorkspaceARN)},
				{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:workspace/ws-2")},
				{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:workspace/ws-3")},
			}}, nil
		},
	}
	st := newTestStore(t)
	warns := countOpWarnings(st, "amp:ListWorkspaces")
	client := amp.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	refs, total, _, err := scanAPSWorkspaces(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if total != 3 || len(rowsByNativeID(t, st, TypeAPSWorkspace)) != 3 {
		t.Errorf("total = %d, want all 3 workspaces stored", total)
	}
	if len(refs) != 1 || refs[0].ID != "ws-1" {
		t.Errorf("refs = %+v, want only ws-1", refs)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
}

// TestScanAPS_WriteFailsWarnsNothing pins that when a phase's write fails, it
// returns ErrStoreWrite and none of its skip warnings, which would claim
// that the rows read were stored.
func TestScanAPS_WriteFailsWarnsNothing(t *testing.T) {
	t.Run("workspaces", func(t *testing.T) {
		st, closeStore := openClosingStore(t)
		warns := countOpWarnings(st, "amp:ListWorkspaces")
		h := map[string]func(in any) (any, error){
			"ListWorkspaces": func(any) (any, error) {
				closeStore()
				return &amp.ListWorkspacesOutput{Workspaces: []amptypes.WorkspaceSummary{
					{WorkspaceId: ptrStr("ws-1"), Arn: ptrStr(apsTestWorkspaceARN)},
					{WorkspaceId: ptrStr("ws-2")},
					{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:workspace/ws-3")},
				}}, nil
			},
		}
		client := amp.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
		_, _, _, err := scanAPSWorkspaces(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v, want ErrStoreWrite", err)
		}
		if *warns != 0 {
			t.Errorf("warnings = %d, want 0", *warns)
		}
	})
	t.Run("scrapers", func(t *testing.T) {
		st, closeStore := openClosingStore(t)
		warns := countOpWarnings(st, "amp:ListScrapers")
		h := map[string]func(in any) (any, error){
			"ListScrapers": func(any) (any, error) {
				closeStore()
				return &amp.ListScrapersOutput{Scrapers: []amptypes.ScraperSummary{
					{ScraperId: ptrStr("s-1"), Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:scraper/s-1")},
					{ScraperId: ptrStr("s-2")},
				}}, nil
			},
		}
		client := amp.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
		_, _, err := scanAPSScrapers(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v, want ErrStoreWrite", err)
		}
		if *warns != 0 {
			t.Errorf("warnings = %d, want 0", *warns)
		}
	})
	t.Run("anomaly detectors", func(t *testing.T) {
		// ws-deny is denied, ws-new is not ready, ws-gone vanishes on page 2,
		// and ws-ok closes the store and lists an id-less detector: each
		// cause would warn if the write succeeded.
		st, closeStore := openClosingStore(t)
		warns := countOpWarnings(st, "amp:ListAnomalyDetectors")
		h := map[string]func(in any) (any, error){
			"ListAnomalyDetectors": func(in any) (any, error) {
				i := in.(*amp.ListAnomalyDetectorsInput)
				switch sv(i.WorkspaceId) {
				case "ws-deny":
					return nil, apiErr("AccessDeniedException", "not authorized to perform: aps:ListAnomalyDetectors")
				case "ws-new":
					return nil, apiErr("ValidationException", "workspace is not active")
				case "ws-gone":
					if i.NextToken != nil {
						return nil, apiErr("ResourceNotFoundException", "gone")
					}
					return &amp.ListAnomalyDetectorsOutput{NextToken: ptrStr("t"), AnomalyDetectors: []amptypes.AnomalyDetectorSummary{
						{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:anomalydetector/ws-gone/ad-1")},
					}}, nil
				default:
					closeStore()
					return &amp.ListAnomalyDetectorsOutput{AnomalyDetectors: []amptypes.AnomalyDetectorSummary{
						{Arn: ptrStr("arn:aws:aps:us-east-1:123456789012:anomalydetector/ws-ok/ad-1")}, {Alias: ptrStr("no-arn")},
					}}, nil
				}
			},
		}
		var workspaces []apsWorkspaceRef
		for _, id := range []string{"ws-deny", "ws-new", "ws-gone", "ws-ok"} {
			workspaces = append(workspaces, apsWorkspaceRef{ID: id, ARN: "arn:aws:aps:us-east-1:123456789012:workspace/" + id})
		}
		client := amp.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
		_, _, err := scanAPSAnomalyDetectors(context.Background(), client, workspaces, newTestAccount(testAccountID), testRegion, st, testScanID)
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v, want ErrStoreWrite", err)
		}
		if *warns != 0 {
			t.Errorf("warnings = %d, want 0", *warns)
		}
	})
}

// TestScanAPSResourcePolicies_NotReadyWarnsOnce pins that workspaces refusing
// DescribeResourcePolicy with ValidationException warn once and are skipped,
// with the readable workspace's policy stored.
func TestScanAPSResourcePolicies_NotReadyWarnsOnce(t *testing.T) {
	h := map[string]func(in any) (any, error){
		"DescribeResourcePolicy": func(in any) (any, error) {
			if sv(in.(*amp.DescribeResourcePolicyInput).WorkspaceId) == "ws-ok" {
				return &amp.DescribeResourcePolicyOutput{PolicyDocument: ptrStr(`{"Version":"2012-10-17"}`)}, nil
			}
			return nil, apiErr("ValidationException", "workspace is not active")
		},
	}
	var workspaces []apsWorkspaceRef
	for _, id := range []string{"ws-new1", "ws-ok", "ws-new2"} {
		workspaces = append(workspaces, apsWorkspaceRef{ID: id, ARN: "arn:aws:aps:us-east-1:123456789012:workspace/" + id})
	}
	st := newTestStore(t)
	warns := countOpWarnings(st, "amp:DescribeResourcePolicy")
	client := amp.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	total, _, err := scanAPSResourcePolicies(context.Background(), client, workspaces, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
	if n := len(rowsByNativeID(t, st, TypeAPSResourcePolicy)); total != 1 || n != 1 {
		t.Errorf("total=%d stored=%d, want 1 each", total, n)
	}
}
