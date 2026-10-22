package aws

import (
	"context"
	"encoding/json"
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
	gfWsOld   = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-old"
	gfWsSAML  = "arn:aws:grafana:us-east-1:123456789012:/workspaces/g-saml"
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

// gfWorkspacesHandler serves g-a (10.4, Identity Center) and g-b (9.4, SAML
// and Identity Center) on page one, then g-c (no version, no authentication
// summary), g-old (8.4, Identity Center) and g-saml (10.4, SAML only) on
// page two, so the service account and permission parent sets differ both ways.
func gfWorkspacesHandler(in any) (any, error) {
	ws := func(id, version string, providers ...grafanatypes.AuthenticationProviderTypes) grafanatypes.WorkspaceSummary {
		w := grafanatypes.WorkspaceSummary{Id: ptrStr(id), Name: ptrStr("ws-" + id), Status: grafanatypes.WorkspaceStatusActive}
		if version != "" {
			w.GrafanaVersion = ptrStr(version)
		}
		if providers != nil {
			w.Authentication = &grafanatypes.AuthenticationSummary{Providers: providers}
		}
		return w
	}
	const sso, saml = grafanatypes.AuthenticationProviderTypesAwsSso, grafanatypes.AuthenticationProviderTypesSaml
	if in.(*grafana.ListWorkspacesInput).NextToken == nil {
		return &grafana.ListWorkspacesOutput{Workspaces: []grafanatypes.WorkspaceSummary{ws("g-a", "10.4", sso), ws("g-b", "9.4", saml, sso)}, NextToken: ptrStr("p2")}, nil
	}
	return &grafana.ListWorkspacesOutput{Workspaces: []grafanatypes.WorkspaceSummary{ws("g-c", ""), ws("g-old", "8.4", sso), ws("g-saml", "10.4", saml)}}, nil
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
	if total != 5 || len(rows) != 5 {
		t.Errorf("total = %d, rows = %d; want 5, 5", total, len(rows))
	}
	if _, ok := rows[gfWsOld]; !ok {
		t.Errorf("8.4 workspace not stored: %v", slices.Sorted(maps.Keys(rows)))
	}
	slices.SortFunc(parents.serviceAccounts, func(a, b childParent) int { return strings.Compare(a.id, b.id) })
	want := []childParent{{id: "g-a", arn: gfWsA}, {id: "g-b", arn: gfWsB}, {id: "g-c", arn: gfWsC}, {id: "g-saml", arn: gfWsSAML}}
	if !slices.Equal(parents.serviceAccounts, want) {
		t.Errorf("service account parents = %v, want %v", parents.serviceAccounts, want)
	}
}

// The SAML-only workspace is no permission parent; one without an
// authentication summary is, and so is a pre-9 one, which has no service
// accounts.
func TestScanGrafanaWorkspaces_PermissionParentsSkipSAMLOnly(t *testing.T) {
	st := newTestStore(t)
	client := gfClient(map[string]func(any) (any, error){"ListWorkspaces": gfWorkspacesHandler})
	parents, _, _, err := scanGrafanaWorkspaces(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	slices.SortFunc(parents.permissions, func(a, b childParent) int { return strings.Compare(a.id, b.id) })
	want := []childParent{{id: "g-a", arn: gfWsA}, {id: "g-b", arn: gfWsB}, {id: "g-c", arn: gfWsC}, {id: "g-old", arn: gfWsOld}}
	if !slices.Equal(parents.permissions, want) {
		t.Errorf("permission parents = %v, want %v", parents.permissions, want)
	}
}

func TestGrafanaMayUseIdentityCenter(t *testing.T) {
	const sso, saml = grafanatypes.AuthenticationProviderTypesAwsSso, grafanatypes.AuthenticationProviderTypesSaml
	for _, tc := range []struct {
		name string
		auth *grafanatypes.AuthenticationSummary
		want bool
	}{
		{"identity center", &grafanatypes.AuthenticationSummary{Providers: []grafanatypes.AuthenticationProviderTypes{sso}}, true},
		{"both", &grafanatypes.AuthenticationSummary{Providers: []grafanatypes.AuthenticationProviderTypes{saml, sso}}, true},
		{"saml only", &grafanatypes.AuthenticationSummary{Providers: []grafanatypes.AuthenticationProviderTypes{saml}}, false},
		{"no providers", &grafanatypes.AuthenticationSummary{}, true},
		{"no summary", nil, true},
	} {
		if got := grafanaMayUseIdentityCenter(tc.auth); got != tc.want {
			t.Errorf("%s: grafanaMayUseIdentityCenter = %v, want %v", tc.name, got, tc.want)
		}
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
	if !slices.Equal(parents.serviceAccounts, want) {
		t.Errorf("service account parents = %v, want %v", parents.serviceAccounts, want)
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
// their service accounts, the service accounts' tokens and the workspaces'
// permissions.
func gfTopLevelHandlers(saCalls, tokenCalls, permCalls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListWorkspaces":                    gfWorkspacesHandler,
		"ListWorkspaceServiceAccounts":      gfServiceAccountHandler(saCalls, nil),
		"ListWorkspaceServiceAccountTokens": gfTokenHandler(tokenCalls, nil),
		"ListPermissions":                   gfPermissionHandler(permCalls, nil),
	}
}

// Service accounts are listed for every workspace the workspace phase kept as
// a parent, tokens for every service account stored, permissions for every
// workspace that may use Identity Center, each under its parent.
func TestScanGrafana_StoresServiceAccountsTokensAndPermissions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var saCalls, tokenCalls, permCalls parentCalls
	acct.cfg = opStubConfig(gfTopLevelHandlers(&saCalls, &tokenCalls, &permCalls), gfDenied)

	total, inserted, err := scanGrafana(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanGrafana: %v", err)
	}
	if got := saCalls.distinct(); !slices.Equal(got, []string{"g-a", "g-b", "g-c", "g-saml"}) {
		t.Errorf("service accounts listed for %v, want [g-a g-b g-c g-saml]", got)
	}
	if got := tokenCalls.distinct(); !slices.Equal(got, []string{"g-a/1", "g-a/2", "g-b/1", "g-c/1", "g-saml/1"}) {
		t.Errorf("tokens listed for %v, want [g-a/1 g-a/2 g-b/1 g-c/1 g-saml/1]", got)
	}
	if got := permCalls.distinct(); !slices.Equal(got, []string{"g-a", "g-b", "g-c", "g-old"}) {
		t.Errorf("permissions listed for %v, want [g-a g-b g-c g-old]", got)
	}
	// 5 workspaces; 5 service accounts (g-a: 1 2, g-b: 1, g-c: 1, g-saml: 1);
	// 6 tokens (g-a/1: 1 2, g-a/2: 1, g-b/1: 1, g-c/1: 1, g-saml/1: 1);
	// 5 permissions (g-a: user, group; g-b, g-c, g-old: user).
	if total != 21 || inserted != 21 {
		t.Errorf("total, inserted = %d, %d; want 21, 21", total, inserted)
	}
	if n := len(rowsByNativeID(t, st, TypeGrafanaPermission)); n != 5 {
		t.Errorf("permission rows = %d, want 5", n)
	}
	assertContainsNative(t, st, gfWsC, gfSaC1)
	assertContainsNative(t, st, gfSaC1, gfSaC1+"/token/1")
	assertContainsNative(t, st, gfWsC, gfPermC)
}

func TestScanGrafana_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var saCalls, tokenCalls, permCalls, regions parentCalls
	cfg := opStubConfig(gfTopLevelHandlers(&saCalls, &tokenCalls, &permCalls), gfDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanGrafana(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanGrafana: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	if n := len(tokenCalls.distinct()); n != 5 {
		t.Errorf("token list calls = %d service accounts, want 5", n)
	}
}

// Each child phase runs after the workspace phase and tokens after service
// accounts; a phase's hard error reaches the caller with the counts stored
// before it, and the permission phase runs whichever older phase fails.
func TestScanGrafana_PhaseErrorKeepsOtherPhases(t *testing.T) {
	for _, tc := range []struct {
		op                                    string
		err                                   *smithy.GenericAPIError
		wantTotal, wantSAs, wantTokens, perms int
	}{
		// 5 workspaces + 5 permissions; the service account phase stores nothing.
		{op: "ListWorkspaceServiceAccounts", err: gfBadInput, wantTotal: 10, perms: 5},
		// + 5 service accounts; every token list fails.
		{op: "ListWorkspaceServiceAccountTokens", err: gfBadInput, wantTotal: 15, wantSAs: 5, perms: 5},
		// Every older phase stores its rows; every permission list fails.
		{op: "ListPermissions", err: gfConflict, wantTotal: 16, wantSAs: 5, wantTokens: 6},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			var saCalls, tokenCalls, permCalls parentCalls
			handlers := gfTopLevelHandlers(&saCalls, &tokenCalls, &permCalls)
			var failed atomic.Bool
			handlers[tc.op] = func(any) (any, error) {
				failed.Store(true)
				return nil, tc.err
			}
			acct.cfg = opStubConfig(handlers, gfDenied)

			total, _, err := scanGrafana(context.Background(), acct, testRegion, st, testScanID)
			if !failed.Load() {
				t.Fatalf("%s never called", tc.op)
			}
			assertAPICode(t, err, tc.err.Code)
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			for typ, want := range map[string]int{
				TypeGrafanaWorkspace:                    5,
				TypeGrafanaWorkspaceServiceAccount:      tc.wantSAs,
				TypeGrafanaWorkspaceServiceAccountToken: tc.wantTokens,
				TypeGrafanaPermission:                   tc.perms,
			} {
				if n := len(rowsByNativeID(t, st, typ)); n != want {
					t.Errorf("%s rows = %d, want %d", typ, n, want)
				}
			}
		})
	}
}

// A failed workspace phase leaves the permission phase no parents.
func TestScanGrafana_WorkspaceFailureListsNoPermissions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var saCalls, tokenCalls, permCalls parentCalls
	handlers := gfTopLevelHandlers(&saCalls, &tokenCalls, &permCalls)
	handlers["ListWorkspaces"] = func(any) (any, error) { return nil, gfBadInput }
	acct.cfg = opStubConfig(handlers, gfDenied)

	_, _, err := scanGrafana(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "ValidationException")
	if got := permCalls.distinct(); len(got) != 0 {
		t.Errorf("permissions listed for %v, want none", got)
	}
}

// An older phase's transient error is demoted to one warning so that it does
// not make the dispatcher read the permission phase's hard error as transient.
func TestScanGrafana_OlderPhaseTransientDoesNotMaskPermissionError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "grafana")
	acct := newTestAccount(testAccountID)
	var saCalls, tokenCalls, permCalls parentCalls
	handlers := gfTopLevelHandlers(&saCalls, &tokenCalls, &permCalls)
	handlers["ListWorkspaceServiceAccountTokens"] = func(any) (any, error) { return nil, apiErr("ServiceUnavailableException", "try later") }
	handlers["ListPermissions"] = func(any) (any, error) { return nil, gfConflict }
	acct.cfg = opStubConfig(handlers, gfDenied)

	_, _, err := scanGrafana(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "ConflictException")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

const (
	gfPermAUser  = gfWsA + "/permission/SSO_USER/u-1"
	gfPermAGroup = gfWsA + "/permission/SSO_GROUP/g-1"
	gfPermB      = gfWsB + "/permission/SSO_USER/u-1"
	gfPermC      = gfWsC + "/permission/SSO_USER/u-1"
)

// gfPermissionHandler serves, for g-a, page one (user u-1, Admin) then page
// two (group g-1, Editor, and elements with no user, no id and no type); any
// other workspace gets one page with user u-1 as Viewer. A workspace in fail
// returns that error. Every call's workspace is recorded; a call asking for
// other than the model's maximum page size fails the test's assertion on rows.
func gfPermissionHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	perm := func(role grafanatypes.Role, userType grafanatypes.UserType, id string) grafanatypes.PermissionEntry {
		return grafanatypes.PermissionEntry{Role: role, User: &grafanatypes.User{Id: ptrStr(id), Type: userType}}
	}
	return func(in any) (any, error) {
		req := in.(*grafana.ListPermissionsInput)
		ws := sv(req.WorkspaceId)
		calls.add(ws)
		if err := fail[ws]; err != nil {
			return nil, err
		}
		if req.MaxResults == nil || *req.MaxResults != 100 {
			return &grafana.ListPermissionsOutput{}, nil
		}
		if ws != "g-a" {
			return &grafana.ListPermissionsOutput{Permissions: []grafanatypes.PermissionEntry{perm(grafanatypes.RoleViewer, grafanatypes.UserTypeSsoUser, "u-1")}}, nil
		}
		if req.NextToken == nil {
			return &grafana.ListPermissionsOutput{Permissions: []grafanatypes.PermissionEntry{perm(grafanatypes.RoleAdmin, grafanatypes.UserTypeSsoUser, "u-1")}, NextToken: ptrStr("p2")}, nil
		}
		return &grafana.ListPermissionsOutput{Permissions: []grafanatypes.PermissionEntry{
			perm(grafanatypes.RoleEditor, grafanatypes.UserTypeSsoGroup, "g-1"),
			{Role: grafanatypes.RoleAdmin},
			{Role: grafanatypes.RoleAdmin, User: &grafanatypes.User{Type: grafanatypes.UserTypeSsoUser}},
			{Role: grafanatypes.RoleAdmin, User: &grafanatypes.User{Id: ptrStr("u-9")}},
		}}, nil
	}
}

// scanGFPermissions seeds the workspace rows and fans out over them.
func scanGFPermissions(t *testing.T, st *store.Store, handler func(in any) (any, error), ids ...string) (int, int, error) {
	t.Helper()
	parents := make([]childParent, 0, len(ids))
	arns := make([]string, 0, len(ids))
	for _, id := range ids {
		arn := "arn:aws:grafana:us-east-1:123456789012:/workspaces/" + id
		parents = append(parents, childParent{id: id, arn: arn})
		arns = append(arns, arn)
	}
	seedParentRows(t, st, TypeGrafanaWorkspace, arns...)
	client := gfClient(map[string]func(any) (any, error){"ListPermissions": handler})
	return scanGrafanaPermissions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
}

// Every page of every workspace is stored, one row per grantee keyed by its
// type and id, under its workspace; elements missing the user, its id or its
// type are dropped.
func TestScanGrafanaPermissions_StoresEveryPageOfEveryWorkspace(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, inserted, err := scanGFPermissions(t, st, gfPermissionHandler(&calls, nil), "g-a", "g-b")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Errorf("total, inserted = %d, %d; want 3, 3", total, inserted)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"g-a", "g-b"}) {
		t.Errorf("listed for %v, want [g-a g-b]", got)
	}
	rows := rowsByNativeID(t, st, TypeGrafanaPermission)
	if got, want := slices.Sorted(maps.Keys(rows)), []string{gfPermAGroup, gfPermAUser, gfPermB}; !slices.Equal(got, want) {
		t.Fatalf("permission rows = %v, want %v", got, want)
	}
	r := rows[gfPermAGroup]
	if r.Type != TypeGrafanaPermission || sv(r.Region) != testRegion || sv(r.Name) != "g-1" {
		t.Errorf("row = type %q region %q name %q; want %q %q %q", r.Type, sv(r.Region), sv(r.Name), TypeGrafanaPermission, testRegion, "g-1")
	}
	var attrs grafanatypes.PermissionEntry
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode attrs: %v", err)
	}
	if attrs.Role != grafanatypes.RoleEditor || attrs.User == nil || attrs.User.Type != grafanatypes.UserTypeSsoGroup {
		t.Errorf("attrs = %s, want the Editor grant to group g-1", r.AttributesJSON)
	}
	assertContainsNative(t, st, gfWsA, gfPermAUser)
	assertContainsNative(t, st, gfWsA, gfPermAGroup)
	assertContainsNative(t, st, gfWsB, gfPermB)
}

func TestScanGrafanaPermissions_Empty(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanGFPermissions(t, st, func(any) (any, error) { return &grafana.ListPermissionsOutput{}, nil }, "g-a")
	if err != nil || total != 0 {
		t.Errorf("total = %d, err = %v; want 0, nil", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeGrafanaPermission)); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

// A failed workspace drops only its own grants: deleted since listing is
// silent; a denial, a refusal with ValidationException or a transient error
// after retries warns once however many workspaces hit it; any other error
// fails the phase.
func TestScanGrafanaPermissions_FailedWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantWarns int
	}{
		{name: "gone", err: gfGone, wantWarns: 0},
		{name: "denied", err: gfDenied, wantWarns: 1},
		{name: "not ready", err: gfBadInput, wantWarns: 1},
		{name: "throttled", err: apiErr("ThrottlingException", "rate exceeded"), wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, grafanaListPermissionsOp)
			var calls parentCalls
			fail := map[string]error{"g-a": tc.err, "g-c": tc.err}
			total, _, err := scanGFPermissions(t, st, gfPermissionHandler(&calls, fail), "g-a", "g-b", "g-c")
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			rows := rowsByNativeID(t, st, TypeGrafanaPermission)
			if _, ok := rows[gfPermB]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want 1, [%s]", total, slices.Sorted(maps.Keys(rows)), gfPermB)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		_, _, err := scanGFPermissions(t, st, gfPermissionHandler(&calls, map[string]error{"g-b": gfConflict}), "g-a", "g-b")
		assertAPICode(t, err, "ConflictException")
	})
}

func TestScanGrafanaWorkspaces_ReadsListedTags(t *testing.T) {
	st := newTestStore(t)
	client := gfClient(map[string]func(any) (any, error){"ListWorkspaces": func(any) (any, error) {
		return &grafana.ListWorkspacesOutput{Workspaces: []grafanatypes.WorkspaceSummary{
			{Id: ptrStr("g-a"), Status: grafanatypes.WorkspaceStatusActive, Tags: map[string]string{"env": "prod"}},
			{Id: ptrStr("g-b"), Status: grafanatypes.WorkspaceStatusActive},
		}}, nil
	}})
	_, total, inserted, err := scanGrafanaWorkspaces(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeGrafanaWorkspace)
	assertTagsMap(t, rows[gfWsA].TagsJSON, map[string]string{"env": "prod"})
	if got := rows[gfWsB].TagsJSON; got != nil {
		t.Errorf("untagged workspace TagsJSON = %q, want nil", *got)
	}
}
