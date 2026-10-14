package aws

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/grafana"
	grafanatypes "github.com/aws/aws-sdk-go-v2/service/grafana/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	gfDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: grafana:List"}
	gfBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	gfGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
	gfConflict = &smithy.GenericAPIError{Code: "ConflictException", Message: "workspace is not active"}
	gfCreated  = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	gfExpires  = time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)
)

const (
	gfWsA     = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-a"
	gfWsB     = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-b"
	gfWsC     = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-c"
	gfSaA1    = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-a/serviceaccount/1"
	gfSaA2    = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-a/serviceaccount/2"
	gfSaB1    = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-b/serviceaccount/1"
	gfSaC1    = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-c/serviceaccount/1"
	gfTokA1t1 = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-a/serviceaccount/1/token/1"
	gfTokA1t2 = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-a/serviceaccount/1/token/2"
	gfTokB1t1 = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-b/serviceaccount/1/token/1"
)

func gfClient(handlers map[string]func(in any) (any, error)) *grafana.Client {
	return grafana.NewFromConfig(opStubConfig(handlers, gfDenied))
}

func TestGrafanaHasServiceAccounts(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"8.4", false},
		{"7.5", false},
		{"9.4", true},
		{"10.4", true},
		{"", true},
		{"next", true},
	} {
		if got := grafanaHasServiceAccounts(tc.version); got != tc.want {
			t.Errorf("grafanaHasServiceAccounts(%q) = %v, want %v", tc.version, got, tc.want)
		}
	}
}

// gfWorkspacesHandler serves g-a (10.4) and g-b (9.4) on page one, then g-c
// (no version) and g-old (8.4) on page two.
func gfWorkspacesHandler(in any) (any, error) {
	ws := func(id, version string) grafanatypes.WorkspaceSummary {
		w := grafanatypes.WorkspaceSummary{Id: ptrStr(id), Name: ptrStr("ws-" + id), Status: grafanatypes.WorkspaceStatusActive}
		if version != "" {
			w.GrafanaVersion = ptrStr(version)
		}
		return w
	}
	if in.(*grafana.ListWorkspacesInput).NextToken == nil {
		return &grafana.ListWorkspacesOutput{Workspaces: []grafanatypes.WorkspaceSummary{ws("g-a", "10.4"), ws("g-b", "9.4")}, NextToken: ptrStr("p2")}, nil
	}
	return &grafana.ListWorkspacesOutput{Workspaces: []grafanatypes.WorkspaceSummary{ws("g-c", ""), ws("g-old", "8.4")}}, nil
}

// Every workspace is stored; only those whose version has service accounts
// become fan-out parents.
func TestScanGrafanaWorkspaces_ParentsSkipPreServiceAccountVersions(t *testing.T) {
	st := newTestStore(t)
	client := gfClient(map[string]func(any) (any, error){"ListWorkspaces": gfWorkspacesHandler})
	parents, total, _, err := scanGrafanaWorkspaces(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeGrafanaWorkspace)
	if total != 4 || len(rows) != 4 {
		t.Errorf("total = %d, rows = %d; want 4, 4", total, len(rows))
	}
	if _, ok := rows["arn:aws:grafana:us-east-1:123456789012:/workspaces/g-old"]; !ok {
		t.Errorf("8.4 workspace not stored: %v", slices.Sorted(maps.Keys(rows)))
	}
	slices.SortFunc(parents, func(a, b childParent) int { return strings.Compare(a.id, b.id) })
	want := []childParent{{id: "g-a", arn: gfWsA}, {id: "g-b", arn: gfWsB}, {id: "g-c", arn: gfWsC}}
	if !slices.Equal(parents, want) {
		t.Errorf("parents = %v, want %v", parents, want)
	}
}

func TestGrafanaHasInstance(t *testing.T) {
	for _, tc := range []struct {
		status grafanatypes.WorkspaceStatus
		want   bool
	}{
		{"CREATING", false},
		{"CREATION_FAILED", false},
		{"DELETING", false},
		{"DELETION_FAILED", false},
		{"FAILED", false},
		{"ACTIVE", true},
		{"UPDATING", true},
		{"UPDATE_FAILED", true},
		{"UPGRADING", true},
		{"UPGRADE_FAILED", true},
		{"VERSION_UPDATING", true},
		{"VERSION_UPDATE_FAILED", true},
		{"LICENSE_REMOVAL_FAILED", true},
		{"DEGRADED", true},
	} {
		if got := grafanaHasInstance(tc.status); got != tc.want {
			t.Errorf("grafanaHasInstance(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

// Workspaces being created or deleted are stored but get no service account
// fan-out; one mid-update keeps its instance and is fanned out.
func TestScanGrafanaWorkspaces_ParentsSkipInstancelessStates(t *testing.T) {
	st := newTestStore(t)
	ws := func(id string, status grafanatypes.WorkspaceStatus) grafanatypes.WorkspaceSummary {
		return grafanatypes.WorkspaceSummary{Id: ptrStr(id), GrafanaVersion: ptrStr("10.4"), Status: status}
	}
	client := gfClient(map[string]func(any) (any, error){"ListWorkspaces": func(any) (any, error) {
		return &grafana.ListWorkspacesOutput{Workspaces: []grafanatypes.WorkspaceSummary{
			ws("g-creating", "CREATING"), ws("g-deleting", "DELETING"), ws("g-updating", "UPDATING"),
		}}, nil
	}})
	parents, total, _, err := scanGrafanaWorkspaces(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n := len(rowsByNativeID(t, st, TypeGrafanaWorkspace)); total != 3 || n != 3 {
		t.Errorf("total = %d, rows = %d; want 3, 3", total, n)
	}
	want := []childParent{{id: "g-updating", arn: "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-updating"}}
	if !slices.Equal(parents, want) {
		t.Errorf("parents = %v, want %v", parents, want)
	}
}

// gfServiceAccountHandler serves, for g-a, page one (sa 1) then page two
// (sa 2 and an element with no Id); any other workspace gets one page with
// its sa 1. A workspace in fail returns that error. Every call's workspace is
// recorded.
func gfServiceAccountHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	sa := func(id string) grafanatypes.ServiceAccountSummary {
		return grafanatypes.ServiceAccountSummary{Id: ptrStr(id), Name: ptrStr("sa-" + id), GrafanaRole: grafanatypes.RoleAdmin, IsDisabled: ptrStr("false")}
	}
	return func(in any) (any, error) {
		req := in.(*grafana.ListWorkspaceServiceAccountsInput)
		ws := sv(req.WorkspaceId)
		calls.add(ws)
		if err := fail[ws]; err != nil {
			return nil, err
		}
		if ws != "g-a" {
			return &grafana.ListWorkspaceServiceAccountsOutput{WorkspaceId: req.WorkspaceId, ServiceAccounts: []grafanatypes.ServiceAccountSummary{sa("1")}}, nil
		}
		if req.NextToken == nil {
			return &grafana.ListWorkspaceServiceAccountsOutput{WorkspaceId: req.WorkspaceId, ServiceAccounts: []grafanatypes.ServiceAccountSummary{sa("1")}, NextToken: ptrStr("p2")}, nil
		}
		return &grafana.ListWorkspaceServiceAccountsOutput{WorkspaceId: req.WorkspaceId, ServiceAccounts: []grafanatypes.ServiceAccountSummary{sa("2"), {Name: ptrStr("no-id")}}}, nil
	}
}

// scanGFServiceAccounts seeds the workspace rows and fans out over them.
func scanGFServiceAccounts(t *testing.T, st *store.Store, handler func(in any) (any, error), ids ...string) ([]grafanaServiceAccount, int, error) {
	t.Helper()
	parents := make([]childParent, 0, len(ids))
	arns := make([]string, 0, len(ids))
	for _, id := range ids {
		arn := "arn:aws:grafana:us-east-1:123456789012:/workspaces/" + id
		parents = append(parents, childParent{id: id, arn: arn})
		arns = append(arns, arn)
	}
	seedParentRows(t, st, TypeGrafanaWorkspace, arns...)
	client := gfClient(map[string]func(any) (any, error){"ListWorkspaceServiceAccounts": handler})
	accounts, total, _, err := scanGrafanaServiceAccounts(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	return accounts, total, err
}

func sortedGFAccounts(accounts []grafanaServiceAccount) []grafanaServiceAccount {
	slices.SortFunc(accounts, func(a, b grafanaServiceAccount) int { return strings.Compare(a.nativeID, b.nativeID) })
	return accounts
}

// The same service account id in two workspaces stays two rows.
func TestScanGrafanaServiceAccounts_StoresEveryPageOfEveryWorkspace(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	accounts, total, err := scanGFServiceAccounts(t, st, gfServiceAccountHandler(&calls, nil), "g-a", "g-b")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"g-a", "g-b"}) {
		t.Errorf("workspaces listed = %v, want [g-a g-b]", got)
	}
	rows := rowsByNativeID(t, st, TypeGrafanaWorkspaceServiceAccount)
	ids := slices.Sorted(maps.Keys(rows))
	if want := []string{gfSaA1, gfSaA2, gfSaB1}; total != 3 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 3, %v", total, ids, want)
	}
	b1 := rows[gfSaB1]
	if sv(b1.Name) != "sa-1" || sv(b1.Region) != "us-east-1" || !strings.Contains(b1.AttributesJSON, `"GrafanaRole":"ADMIN"`) {
		t.Errorf("b1 name/region = %q/%q, attrs = %s", sv(b1.Name), sv(b1.Region), b1.AttributesJSON)
	}
	want := []grafanaServiceAccount{
		{workspaceID: "g-a", id: "1", nativeID: gfSaA1},
		{workspaceID: "g-a", id: "2", nativeID: gfSaA2},
		{workspaceID: "g-b", id: "1", nativeID: gfSaB1},
	}
	if got := sortedGFAccounts(accounts); !slices.Equal(got, want) {
		t.Errorf("accounts = %v, want %v", got, want)
	}
	assertContainsNative(t, st, gfWsA, gfSaA1)
	assertContainsNative(t, st, gfWsA, gfSaA2)
	assertContainsNative(t, st, gfWsB, gfSaB1)
}

func TestScanGrafanaServiceAccounts_Empty(t *testing.T) {
	st := newTestStore(t)
	accounts, total, err := scanGFServiceAccounts(t, st, func(any) (any, error) { return &grafana.ListWorkspaceServiceAccountsOutput{}, nil }, "g-a")
	if err != nil || total != 0 || len(accounts) != 0 || len(rowsByNativeID(t, st, TypeGrafanaWorkspaceServiceAccount)) != 0 {
		t.Errorf("total = %d, accounts = %v, err = %v; want 0 rows, nil", total, accounts, err)
	}
}

// A workspace deleted after it was listed is skipped silently, and one in a
// conflicting state warns for that workspace; either way it yields no token
// parents and the other workspaces are kept. A denial warns once for the op
// and keeps the other workspaces; any other error fails.
func TestScanGrafanaServiceAccounts_FailedWorkspace(t *testing.T) {
	const op = "grafana:ListWorkspaceServiceAccounts"
	for _, tc := range []struct {
		name      string
		err       error
		wantWarns int
	}{
		{name: "gone", err: gfGone, wantWarns: 0},
		{name: "conflict", err: gfConflict, wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, op)
			var calls parentCalls
			accounts, total, err := scanGFServiceAccounts(t, st, gfServiceAccountHandler(&calls, map[string]error{"g-a": tc.err}), "g-a", "g-b")
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			want := []grafanaServiceAccount{{workspaceID: "g-b", id: "1", nativeID: gfSaB1}}
			if total != 1 || *warns != tc.wantWarns || !slices.Equal(accounts, want) {
				t.Errorf("total = %d, warnings = %d, accounts = %v; want 1, %d, %v", total, *warns, accounts, tc.wantWarns, want)
			}
		})
	}
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, op)
		var calls parentCalls
		accounts, total, err := scanGFServiceAccounts(t, st, gfServiceAccountHandler(&calls, map[string]error{"g-b": gfDenied, "g-c": gfDenied}), "g-a", "g-b", "g-c")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		n := len(rowsByNativeID(t, st, TypeGrafanaWorkspaceServiceAccount))
		if *warns != 1 || total != 2 || n != 2 || len(accounts) != 2 {
			t.Errorf("warnings = %d, total = %d, rows = %d, accounts = %d; want 1, 2, 2, 2", *warns, total, n, len(accounts))
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		accounts, _, err := scanGFServiceAccounts(t, st, gfServiceAccountHandler(&calls, map[string]error{"g-b": gfBadInput}), "g-a", "g-b")
		assertAPICode(t, err, "ValidationException")
		if accounts != nil {
			t.Errorf("accounts = %v, want nil on a hard error", accounts)
		}
	})
}

// gfTokenHandler serves, for g-a/1, page one (token 1) then page two (token 2
// and an element with no Id); any other service account gets one page with
// its token 1. A "workspace/serviceaccount" key in fail returns that error.
// Every call's key is recorded.
func gfTokenHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	tok := func(id string) grafanatypes.ServiceAccountTokenSummary {
		return grafanatypes.ServiceAccountTokenSummary{Id: ptrStr(id), Name: ptrStr("tok-" + id), CreatedAt: &gfCreated, ExpiresAt: &gfExpires}
	}
	return func(in any) (any, error) {
		req := in.(*grafana.ListWorkspaceServiceAccountTokensInput)
		key := sv(req.WorkspaceId) + "/" + sv(req.ServiceAccountId)
		calls.add(key)
		if err := fail[key]; err != nil {
			return nil, err
		}
		if key != "g-a/1" {
			return &grafana.ListWorkspaceServiceAccountTokensOutput{ServiceAccountTokens: []grafanatypes.ServiceAccountTokenSummary{tok("1")}}, nil
		}
		if req.NextToken == nil {
			return &grafana.ListWorkspaceServiceAccountTokensOutput{ServiceAccountTokens: []grafanatypes.ServiceAccountTokenSummary{tok("1")}, NextToken: ptrStr("p2")}, nil
		}
		return &grafana.ListWorkspaceServiceAccountTokensOutput{ServiceAccountTokens: []grafanatypes.ServiceAccountTokenSummary{tok("2"), {Name: ptrStr("no-id")}}}, nil
	}
}

var gfTokenParents = []grafanaServiceAccount{
	{workspaceID: "g-a", id: "1", nativeID: gfSaA1},
	{workspaceID: "g-b", id: "1", nativeID: gfSaB1},
	{workspaceID: "g-c", id: "1", nativeID: gfSaC1},
}

// scanGFTokens seeds the service account rows and fans out over them.
func scanGFTokens(t *testing.T, st *store.Store, handler func(in any) (any, error), accounts []grafanaServiceAccount) (int, error) {
	t.Helper()
	ids := make([]string, len(accounts))
	for i, a := range accounts {
		ids[i] = a.nativeID
	}
	seedParentRows(t, st, TypeGrafanaWorkspaceServiceAccount, ids...)
	client := gfClient(map[string]func(any) (any, error){"ListWorkspaceServiceAccountTokens": handler})
	total, _, err := scanGrafanaServiceAccountTokens(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, accounts)
	return total, err
}

// Each call names the service account's own workspace, and the same token id
// under two service accounts stays two rows.
func TestScanGrafanaServiceAccountTokens_StoresEveryPageOfEveryServiceAccount(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanGFTokens(t, st, gfTokenHandler(&calls, nil), gfTokenParents[:2])
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"g-a/1", "g-b/1"}) {
		t.Errorf("service accounts listed = %v, want [g-a/1 g-b/1]", got)
	}
	rows := rowsByNativeID(t, st, TypeGrafanaWorkspaceServiceAccountToken)
	ids := slices.Sorted(maps.Keys(rows))
	if want := []string{gfTokA1t1, gfTokA1t2, gfTokB1t1}; total != 3 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 3, %v", total, ids, want)
	}
	t2 := rows[gfTokA1t2]
	if sv(t2.Name) != "tok-2" || sv(t2.Region) != "us-east-1" || sv(t2.CreatedAt) != "2026-09-01T08:00:00Z" ||
		!strings.Contains(t2.AttributesJSON, `"ExpiresAt":"2027-03-01T08:00:00Z"`) {
		t.Errorf("t2 name/region/created = %q/%q/%q, attrs = %s", sv(t2.Name), sv(t2.Region), sv(t2.CreatedAt), t2.AttributesJSON)
	}
	assertContainsNative(t, st, gfSaA1, gfTokA1t1)
	assertContainsNative(t, st, gfSaA1, gfTokA1t2)
	assertContainsNative(t, st, gfSaB1, gfTokB1t1)
}

func TestScanGrafanaServiceAccountTokens_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanGFTokens(t, st, func(any) (any, error) { return &grafana.ListWorkspaceServiceAccountTokensOutput{}, nil }, gfTokenParents[:1])
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeGrafanaWorkspaceServiceAccountToken)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// A service account deleted after it was listed is skipped silently, and one
// whose workspace is in a conflicting state warns for that service account;
// either way the others are kept. A denial warns once for the op and keeps the
// others; any other error fails.
func TestScanGrafanaServiceAccountTokens_FailedServiceAccount(t *testing.T) {
	const op = "grafana:ListWorkspaceServiceAccountTokens"
	for _, tc := range []struct {
		name      string
		err       error
		wantWarns int
	}{
		{name: "gone", err: gfGone, wantWarns: 0},
		{name: "conflict", err: gfConflict, wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, op)
			var calls parentCalls
			total, err := scanGFTokens(t, st, gfTokenHandler(&calls, map[string]error{"g-a/1": tc.err}), gfTokenParents[:2])
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if _, ok := rowsByNativeID(t, st, TypeGrafanaWorkspaceServiceAccountToken)[gfTokB1t1]; total != 1 || !ok || *warns != tc.wantWarns {
				t.Errorf("total = %d, b1 token stored = %v, warnings = %d; want 1, true, %d", total, ok, *warns, tc.wantWarns)
			}
		})
	}
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, op)
		var calls parentCalls
		total, err := scanGFTokens(t, st, gfTokenHandler(&calls, map[string]error{"g-b/1": gfDenied, "g-c/1": gfDenied}), gfTokenParents)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		n := len(rowsByNativeID(t, st, TypeGrafanaWorkspaceServiceAccountToken))
		if *warns != 1 || total != 2 || n != 2 {
			t.Errorf("warnings = %d, total = %d, rows = %d; want 1, 2 (g-a/1's tokens), 2", *warns, total, n)
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		_, err := scanGFTokens(t, st, gfTokenHandler(&calls, map[string]error{"g-b/1": gfBadInput}), gfTokenParents[:2])
		assertAPICode(t, err, "ValidationException")
		// The service account id alone ("1") is ambiguous across workspaces.
		if err == nil || !strings.Contains(err.Error(), "g-b") {
			t.Errorf("err = %v, want it to name workspace g-b", err)
		}
	})
}

// The token list never returns a key, so the token type has no Redact rule.
// Any new SDK field must be reviewed for credential material (and a Redact
// rule added to the descriptor if it carries any) before this list grows.
func TestGrafanaTokenSummary_FieldsReviewedForSecrets(t *testing.T) {
	typ := reflect.TypeFor[grafanatypes.ServiceAccountTokenSummary]()
	var got []string
	for i := range typ.NumField() {
		if f := typ.Field(i); f.IsExported() {
			got = append(got, f.Name)
		}
	}
	slices.Sort(got)
	if want := []string{"CreatedAt", "ExpiresAt", "Id", "LastUsedAt", "Name"}; !slices.Equal(got, want) {
		t.Errorf("ServiceAccountTokenSummary fields = %v, want %v: review the new fields for secrets before updating", got, want)
	}
}

// gfTopLevelHandlers answers every op: the workspaces of gfWorkspacesHandler,
// their service accounts and the service accounts' tokens.
func gfTopLevelHandlers(saCalls, tokenCalls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListWorkspaces":                    gfWorkspacesHandler,
		"ListWorkspaceServiceAccounts":      gfServiceAccountHandler(saCalls, nil),
		"ListWorkspaceServiceAccountTokens": gfTokenHandler(tokenCalls, nil),
	}
}

// Service accounts are listed for every workspace the workspace phase kept as
// a parent, tokens for every service account stored, each under its parent.
func TestScanGrafana_StoresServiceAccountsAndTokens(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var saCalls, tokenCalls parentCalls
	acct.cfg = opStubConfig(gfTopLevelHandlers(&saCalls, &tokenCalls), gfDenied)

	total, _, err := scanGrafana(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanGrafana: %v", err)
	}
	if got := saCalls.distinct(); !slices.Equal(got, []string{"g-a", "g-b", "g-c"}) {
		t.Errorf("service accounts listed for %v, want [g-a g-b g-c]", got)
	}
	if got := tokenCalls.distinct(); !slices.Equal(got, []string{"g-a/1", "g-a/2", "g-b/1", "g-c/1"}) {
		t.Errorf("tokens listed for %v, want [g-a/1 g-a/2 g-b/1 g-c/1]", got)
	}
	// 4 workspaces; 4 service accounts (g-a: 1 2, g-b: 1, g-c: 1);
	// 5 tokens (g-a/1: 1 2, g-a/2: 1, g-b/1: 1, g-c/1: 1).
	if total != 13 {
		t.Errorf("total = %d, want 13", total)
	}
	assertContainsNative(t, st, gfWsC, gfSaC1)
	assertContainsNative(t, st, gfSaC1, gfSaC1+"/token/1")
}

func TestScanGrafana_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var saCalls, tokenCalls, regions parentCalls
	cfg := opStubConfig(gfTopLevelHandlers(&saCalls, &tokenCalls), gfDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanGrafana(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanGrafana: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	if n := len(tokenCalls.distinct()); n != 4 {
		t.Errorf("token list calls = %d service accounts, want 4", n)
	}
}

// Each new phase runs after the workspace phase, tokens after service
// accounts, and a phase's hard error reaches the caller with the counts
// stored before it.
func TestScanGrafana_NewPhaseErrorAfterExistingPhases(t *testing.T) {
	for _, tc := range []struct {
		op          string
		wantTotal   int
		wantSAsRows int
	}{
		// 4 workspaces; the service account phase stores nothing.
		{op: "ListWorkspaceServiceAccounts", wantTotal: 4},
		// + 4 service accounts; every token list fails.
		{op: "ListWorkspaceServiceAccountTokens", wantTotal: 8, wantSAsRows: 4},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			var saCalls, tokenCalls parentCalls
			handlers := gfTopLevelHandlers(&saCalls, &tokenCalls)
			var failed atomic.Bool
			handlers[tc.op] = func(any) (any, error) {
				failed.Store(true)
				return nil, gfBadInput
			}
			acct.cfg = opStubConfig(handlers, gfDenied)

			total, _, err := scanGrafana(context.Background(), acct, testRegion, st, testScanID)
			if !failed.Load() {
				t.Fatalf("%s never called", tc.op)
			}
			assertAPICode(t, err, "ValidationException")
			if n := len(rowsByNativeID(t, st, TypeGrafanaWorkspace)); n != 4 {
				t.Errorf("workspace rows = %d, want 4 before the %s phase", n, tc.op)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, TypeGrafanaWorkspaceServiceAccount)); n != tc.wantSAsRows {
				t.Errorf("service account rows = %d, want %d", n, tc.wantSAsRows)
			}
			if n := len(rowsByNativeID(t, st, TypeGrafanaWorkspaceServiceAccountToken)); n != 0 {
				t.Errorf("token rows = %d, want 0", n)
			}
		})
	}
}
