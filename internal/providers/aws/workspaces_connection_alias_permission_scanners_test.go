package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/workspaces"
	wstypes "github.com/aws/aws-sdk-go-v2/service/workspaces/types"
)

const (
	wsTestAliasID1      = "wsca-11111111"
	wsTestAliasID2      = "wsca-22222222"
	wsTestSharedAliasID = "wsca-33333333"
	wsTestAlias1        = "arn:aws:workspaces:us-east-1:123456789012:connectionalias/" + wsTestAliasID1
	wsTestAlias2        = "arn:aws:workspaces:us-east-1:123456789012:connectionalias/" + wsTestAliasID2
	wsTestAliasPerm1    = wsTestAlias1 + "/permission"
	wsTestAliasPerm2    = wsTestAlias2 + "/permission"
)

// wsAliasHandlers is wsTopLevelHandlers plus connection aliases over two
// pages (owned alias 1; then owned alias 2, an alias another account owns and
// an element with no id). Alias 1 is shared over two pages, the later-sorting
// account first; alias 2 is shared with no one. Every permission call is
// recorded in permCalls.
func wsAliasHandlers(addInCalls, permCalls *parentCalls) map[string]func(in any) (any, error) {
	h := wsTopLevelHandlers(addInCalls)
	h["DescribeConnectionAliases"] = func(in any) (any, error) {
		if in.(*workspaces.DescribeConnectionAliasesInput).NextToken == nil {
			return &workspaces.DescribeConnectionAliasesOutput{NextToken: ptrStr("p2"), ConnectionAliases: []wstypes.ConnectionAlias{
				{AliasId: ptrStr(wsTestAliasID1), OwnerAccountId: ptrStr(testAccountID), ConnectionString: ptrStr("www.example.com")},
			}}, nil
		}
		return &workspaces.DescribeConnectionAliasesOutput{ConnectionAliases: []wstypes.ConnectionAlias{
			{AliasId: ptrStr(wsTestAliasID2), OwnerAccountId: ptrStr(testAccountID)},
			{AliasId: ptrStr(wsTestSharedAliasID), OwnerAccountId: ptrStr("210987654321")},
			{OwnerAccountId: ptrStr(testAccountID)},
		}}, nil
	}
	h["DescribeConnectionAliasPermissions"] = func(in any) (any, error) {
		p := in.(*workspaces.DescribeConnectionAliasPermissionsInput)
		permCalls.add(sv(p.AliasId))
		if err := wsCheckLimit(p.MaxResults); err != nil {
			return nil, err
		}
		switch {
		case sv(p.AliasId) == wsTestAliasID2:
			return &workspaces.DescribeConnectionAliasPermissionsOutput{AliasId: p.AliasId}, nil
		case p.NextToken == nil:
			return &workspaces.DescribeConnectionAliasPermissionsOutput{
				AliasId: p.AliasId, NextToken: ptrStr("p2"),
				ConnectionAliasPermissions: []wstypes.ConnectionAliasPermission{
					{SharedAccountId: ptrStr("444455556666"), AllowAssociation: ptrBool(true)},
				},
			}, nil
		default:
			return &workspaces.DescribeConnectionAliasPermissionsOutput{
				AliasId: p.AliasId,
				ConnectionAliasPermissions: []wstypes.ConnectionAliasPermission{
					{SharedAccountId: ptrStr("111122223333"), AllowAssociation: ptrBool(false)},
				},
			}, nil
		}
	}
	return h
}

// wsAliasPermissionAttrs decodes a permission row's attributes.
func wsAliasPermissionAttrs(t *testing.T, attrs string) wsConnectionAliasPermission {
	t.Helper()
	var p wsConnectionAliasPermission
	if err := json.Unmarshal([]byte(attrs), &p); err != nil {
		t.Fatalf("attrs %s: %v", attrs, err)
	}
	return p
}

type wsGrant struct {
	account string
	allow   bool
}

func wsGrants(p wsConnectionAliasPermission) []wsGrant {
	out := []wsGrant{}
	for _, g := range p.ConnectionAliasPermissions {
		out = append(out, wsGrant{sv(g.SharedAccountId), *g.AllowAssociation})
	}
	return out
}

func TestScanWorkSpaces_StoresConnectionAliasPermissions(t *testing.T) {
	st := newTestStore(t)
	var addInCalls, permCalls parentCalls
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(wsAliasHandlers(&addInCalls, &permCalls), wsDenied)
	total, inserted, err := scanWorkSpaces(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanWorkSpaces: %v", err)
	}
	// 3 aliases + 1 application + 2 directories + 2 links + 3 add-ins + 2 permissions
	if total != 13 || inserted != 13 {
		t.Errorf("total/inserted = %d/%d, want 13/13", total, inserted)
	}
	if got := permCalls.distinct(); !slices.Equal(got, []string{wsTestAliasID1, wsTestAliasID2}) {
		t.Errorf("permissions read for %v, want only the owned aliases [%s %s]", got, wsTestAliasID1, wsTestAliasID2)
	}

	perms := rowsByNativeID(t, st, TypeWorkSpacesConnectionAliasPermission)
	if got := nativeIDs(perms); !slices.Equal(got, []string{wsTestAliasPerm1, wsTestAliasPerm2}) {
		t.Fatalf("permission rows = %v, want [%s %s]", got, wsTestAliasPerm1, wsTestAliasPerm2)
	}
	p1 := perms[wsTestAliasPerm1]
	if sv(p1.Name) != wsTestAliasID1 || sv(p1.Region) != testRegion || p1.Type != TypeWorkSpacesConnectionAliasPermission {
		t.Errorf("permission name/region/type = %q/%q/%q, want %s/%s/%s",
			sv(p1.Name), sv(p1.Region), p1.Type, wsTestAliasID1, testRegion, TypeWorkSpacesConnectionAliasPermission)
	}
	attrs1 := wsAliasPermissionAttrs(t, p1.AttributesJSON)
	if attrs1.AliasID != wsTestAliasID1 {
		t.Errorf("AliasId = %q, want %s", attrs1.AliasID, wsTestAliasID1)
	}
	want := []wsGrant{{"111122223333", false}, {"444455556666", true}}
	if got := wsGrants(attrs1); !slices.Equal(got, want) {
		t.Errorf("alias 1 grants = %v, want both pages sorted %v", got, want)
	}
	if got := wsGrants(wsAliasPermissionAttrs(t, perms[wsTestAliasPerm2].AttributesJSON)); len(got) != 0 {
		t.Errorf("alias 2 grants = %v, want none", got)
	}
	assertContainsNative(t, st, wsTestAlias1, wsTestAliasPerm1)
	assertContainsNative(t, st, wsTestAlias2, wsTestAliasPerm2)
	assertContainsEdgesDeclared(t, st)
}

// An alias shared with no one stores the same body on every scan, so its
// grants key marshals as [] rather than null.
func TestScanWorkSpaces_UnsharedAliasStoresEmptyList(t *testing.T) {
	st := newTestStore(t)
	var addInCalls, permCalls parentCalls
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(wsAliasHandlers(&addInCalls, &permCalls), wsDenied)
	if _, _, err := scanWorkSpaces(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanWorkSpaces: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rowsByNativeID(t, st, TypeWorkSpacesConnectionAliasPermission)[wsTestAliasPerm2].AttributesJSON), &raw); err != nil {
		t.Fatal(err)
	}
	if got := string(raw["ConnectionAliasPermissions"]); got != "[]" {
		t.Errorf("ConnectionAliasPermissions = %s, want []", got)
	}
}

func TestScanWorkSpaces_NoOwnedAliases(t *testing.T) {
	for _, tc := range []struct {
		name      string
		aliases   []wstypes.ConnectionAlias
		wantWarns int
	}{
		{"no aliases", nil, 0},
		{"only an alias another account owns", []wstypes.ConnectionAlias{{AliasId: ptrStr(wsTestSharedAliasID), OwnerAccountId: ptrStr("210987654321")}}, 0},
		// One warning covers every alias with no owner.
		{"aliases with no owner", []wstypes.ConnectionAlias{{AliasId: ptrStr(wsTestAliasID1)}, {AliasId: ptrStr(wsTestAliasID2)}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, wsDescribeAliasPermissionsOp)
			var addInCalls, permCalls parentCalls
			h := wsAliasHandlers(&addInCalls, &permCalls)
			h["DescribeConnectionAliases"] = func(any) (any, error) {
				return &workspaces.DescribeConnectionAliasesOutput{ConnectionAliases: tc.aliases}, nil
			}
			if _, err := scanWorkSpacesTopLevel(t, st, h); err != nil {
				t.Fatalf("scanWorkSpaces: %v", err)
			}
			if got := permCalls.distinct(); len(got) != 0 {
				t.Errorf("permissions read for %v, want none", got)
			}
			if n := len(rowsByNativeID(t, st, TypeWorkSpacesConnectionAliasPermission)); n != 0 {
				t.Errorf("permission rows = %d, want 0", n)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

// A failed alias drops only its own permission row: deleted since listing is
// silent; a denial, OperationNotSupportedException,
// InvalidParameterValuesException or a transient error after retries warns once however many aliases hit it; any other error fails the
// scan after every other phase has stored its rows.
func TestScanWorkSpaces_FailedAliasPermission(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failIDs   []string
		err       error
		wantCode  string // "" means the scan returns nil
		wantRows  []string
		wantWarns int
	}{
		{"alias gone", []string{wsTestAliasID2}, wsGone, "", []string{wsTestAliasPerm1}, 0},
		{"operation not supported on both", []string{wsTestAliasID1, wsTestAliasID2}, apiErr("OperationNotSupportedException", "no"), "", nil, 1},
		{"denied on both", []string{wsTestAliasID1, wsTestAliasID2}, wsDenied, "", nil, 1},
		{"throttled", []string{wsTestAliasID2}, apiErr("ThrottlingException", "rate exceeded"), "", []string{wsTestAliasPerm1}, 1},
		{"alias not ready on both", []string{wsTestAliasID1, wsTestAliasID2}, wsBadAddInID, "", nil, 1},
		{"alias not ready", []string{wsTestAliasID2}, wsBadAddInID, "", []string{wsTestAliasPerm1}, 1},
		// Both fail: the first error cancels the fan-out, so a sibling's row
		// would depend on scheduling.
		{"other error", []string{wsTestAliasID1, wsTestAliasID2}, wsBadInput, "ValidationException", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, wsDescribeAliasPermissionsOp)
			var addInCalls, permCalls parentCalls
			h := wsAliasHandlers(&addInCalls, &permCalls)
			next := h["DescribeConnectionAliasPermissions"]
			h["DescribeConnectionAliasPermissions"] = func(in any) (any, error) {
				if slices.Contains(tc.failIDs, sv(in.(*workspaces.DescribeConnectionAliasPermissionsInput).AliasId)) {
					return nil, tc.err
				}
				return next(in)
			}
			_, err := scanWorkSpacesTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeWorkSpacesConnectionAliasPermission)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("permission rows = %v, want %v", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			for typ, id := range map[string]string{
				TypeWorkSpacesConnectionAlias:    wsTestAlias1,
				TypeWorkSpacesAccountLink:        wsTestLink1,
				TypeWorkSpacesConnectClientAddIn: wsTestAddIn3,
			} {
				if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
					t.Errorf("row %s missing; a failed permission phase must not cost another phase its rows", id)
				}
			}
		})
	}
}

// A hard error in an older phase after the alias phase still lets the
// permission phase run over the aliases stored before it, and is returned.
func TestScanWorkSpaces_OlderPhaseErrorStillRunsAliasPermissions(t *testing.T) {
	st := newTestStore(t)
	var addInCalls, permCalls parentCalls
	h := wsAliasHandlers(&addInCalls, &permCalls)
	h["DescribeApplications"] = func(any) (any, error) { return nil, wsBadInput }
	_, err := scanWorkSpacesTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeWorkSpacesConnectionAliasPermission)); !slices.Equal(got, []string{wsTestAliasPerm1, wsTestAliasPerm2}) {
		t.Errorf("permission rows = %v, want [%s %s]", got, wsTestAliasPerm1, wsTestAliasPerm2)
	}
}

// A failed alias listing hands the permission phase no aliases.
func TestScanWorkSpaces_FailedAliasListReadsNoPermissions(t *testing.T) {
	st := newTestStore(t)
	var addInCalls, permCalls parentCalls
	h := wsAliasHandlers(&addInCalls, &permCalls)
	h["DescribeConnectionAliases"] = func(in any) (any, error) {
		if in.(*workspaces.DescribeConnectionAliasesInput).NextToken != nil {
			return nil, wsBadInput
		}
		return wsAliasHandlers(&addInCalls, &permCalls)["DescribeConnectionAliases"](in)
	}
	_, err := scanWorkSpacesTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := permCalls.distinct(); len(got) != 0 {
		t.Errorf("permissions read for %v, want none: no alias was stored", got)
	}
}

// A transient error in an older phase is demoted to a warning so that the
// permission phase's hard error still reaches the dispatcher as a hard error.
func TestScanWorkSpaces_OlderPhaseTransientDoesNotMaskAliasPermissionError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "workspaces")
	var addInCalls, permCalls parentCalls
	h := wsAliasHandlers(&addInCalls, &permCalls)
	h["DescribeApplications"] = func(any) (any, error) { return nil, apiErr("ServiceUnavailableException", "try later") }
	h["DescribeConnectionAliasPermissions"] = func(any) (any, error) { return nil, wsBadInput }
	_, err := scanWorkSpacesTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

// The grants sort is total: every input order of grants that differ in any
// field gives the same stored order.
func TestDescribeWSConnectionAliasPermission_SortIsTotal(t *testing.T) {
	grants := []wstypes.ConnectionAliasPermission{
		{SharedAccountId: ptrStr("444455556666"), AllowAssociation: ptrBool(false)},
		{SharedAccountId: ptrStr("111122223333"), AllowAssociation: ptrBool(true)},
		{SharedAccountId: ptrStr("111122223333"), AllowAssociation: ptrBool(false)},
		{SharedAccountId: ptrStr("111122223333")},
	}
	want := `[{"AllowAssociation":null,"SharedAccountId":"111122223333"},` +
		`{"AllowAssociation":false,"SharedAccountId":"111122223333"},` +
		`{"AllowAssociation":true,"SharedAccountId":"111122223333"},` +
		`{"AllowAssociation":false,"SharedAccountId":"444455556666"}]`
	for name, in := range map[string][]wstypes.ConnectionAliasPermission{
		"given":    grants,
		"reversed": {grants[3], grants[2], grants[1], grants[0]},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := opStubConfig(map[string]func(in any) (any, error){
				"DescribeConnectionAliasPermissions": func(any) (any, error) {
					return &workspaces.DescribeConnectionAliasPermissionsOutput{ConnectionAliasPermissions: in}, nil
				},
			}, wsDenied)
			perm, err := describeWSConnectionAliasPermission(context.Background(), workspaces.NewFromConfig(cfg), wsTestAliasID1)
			if err != nil {
				t.Fatalf("describeWSConnectionAliasPermission: %v", err)
			}
			if got := mustJSON(perm.ConnectionAliasPermissions); got != want {
				t.Errorf("grants = %s, want %s", got, want)
			}
		})
	}
}

func TestDescribeWSConnectionAliasPermission_FailsOnRepeatedToken(t *testing.T) {
	var calls atomic.Int32
	cfg := opStubConfig(map[string]func(in any) (any, error){
		"DescribeConnectionAliasPermissions": func(any) (any, error) {
			if calls.Add(1) > 2 {
				return nil, errors.New("called again after a repeated token")
			}
			return &workspaces.DescribeConnectionAliasPermissionsOutput{NextToken: ptrStr("same")}, nil
		},
	}, wsDenied)
	perm, err := describeWSConnectionAliasPermission(context.Background(), workspaces.NewFromConfig(cfg), wsTestAliasID1)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
	if perm.AliasID != wsTestAliasID1 || perm.ConnectionAliasPermissions != nil {
		t.Errorf("perm = %+v; want AliasId %s and no grants (not read)", perm, wsTestAliasID1)
	}
}

// An alias whose permission listing repeats its token keeps its row with the
// grants null (not read), and the scan returns the error; the sibling alias
// keeps its row and its read grants.
func TestScanWorkSpaces_RepeatedAliasPermissionTokenKeepsRowWithoutGrants(t *testing.T) {
	st := newTestStore(t)
	var addInCalls, permCalls parentCalls
	handlers := wsAliasHandlers(&addInCalls, &permCalls)
	describe := handlers["DescribeConnectionAliasPermissions"]
	var repeats atomic.Int32
	handlers["DescribeConnectionAliasPermissions"] = func(in any) (any, error) {
		if sv(in.(*workspaces.DescribeConnectionAliasPermissionsInput).AliasId) != wsTestAliasID1 {
			return describe(in)
		}
		if repeats.Add(1) > 3 {
			return nil, errors.New("called again after a repeated token")
		}
		return &workspaces.DescribeConnectionAliasPermissionsOutput{
			AliasId: ptrStr(wsTestAliasID1), NextToken: ptrStr("same"),
			ConnectionAliasPermissions: []wstypes.ConnectionAliasPermission{{SharedAccountId: ptrStr("444455556666"), AllowAssociation: ptrBool(true)}},
		}, nil
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, wsDenied)
	if _, _, err := scanWorkSpaces(context.Background(), acct, testRegion, st, testScanID); !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	perms := rowsByNativeID(t, st, TypeWorkSpacesConnectionAliasPermission)
	if got := nativeIDs(perms); !slices.Equal(got, []string{wsTestAliasPerm1, wsTestAliasPerm2}) {
		t.Fatalf("permission rows = %v, want [%s %s]", got, wsTestAliasPerm1, wsTestAliasPerm2)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(perms[wsTestAliasPerm1].AttributesJSON), &raw); err != nil {
		t.Fatal(err)
	}
	if got := string(raw["ConnectionAliasPermissions"]); got != "null" {
		t.Errorf("repeating alias ConnectionAliasPermissions = %s, want null", got)
	}
	if got := wsAliasPermissionAttrs(t, perms[wsTestAliasPerm2].AttributesJSON).ConnectionAliasPermissions; got == nil {
		t.Errorf("sibling alias grants = null, want [] (read)")
	}
}
