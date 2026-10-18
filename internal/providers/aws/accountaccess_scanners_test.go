package aws

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/accountaccess"
	aatypes "github.com/aws/aws-sdk-go-v2/service/accountaccess/types"
	"github.com/icearp/disco-cli/store"
)

const (
	aaRegion      = "eu-west-1"
	aaAppA        = "arn:aws:account-access:" + aaRegion + ":" + testAccountID + ":application/app-a"
	aaAppB        = "arn:aws:account-access:" + aaRegion + ":" + testAccountID + ":application/app-b"
	aaInstanceARN = "arn:aws:sso:::instance/ssoins-1111222233334444"
	aaSSOAppARN   = "arn:aws:sso::" + testAccountID + ":application/ssoins-1111222233334444/apl-aaaabbbbccccdddd"
	aaRoleARN     = "arn:aws:iam::" + testAccountID + ":role/Admin"
	aaUserID      = "11111111-2222-3333-4444-555555555555"
	aaGroupID     = "66666666-7777-8888-9999-000000000000"
)

// aaAppOutput is the GetApplication body for arn: app-a is ACTIVE and
// tagged, app-b failed to delete.
func aaAppOutput(arn string) *accountaccess.GetApplicationOutput {
	out := &accountaccess.GetApplicationOutput{
		Status:    aatypes.StatusActive,
		CreatedAt: sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		IdentitySource: &aatypes.IdentitySourceDetailsMemberIdentityCenter{Value: aatypes.IdentityCenterDetails{
			InstanceArn: ptrStr(aaInstanceARN), ApplicationArn: ptrStr(aaSSOAppARN),
		}},
	}
	if arn == aaAppA {
		out.Tags = map[string]string{"env": "prod"}
		return out
	}
	out.Status = aatypes.StatusDeleteFailed
	return out
}

// aaEntitlement is a ListEntitlements element granting principal the Admin
// role.
func aaEntitlement(id string, principal aatypes.IdentityCenterPrincipal) aatypes.EntitlementsListMember {
	return aatypes.EntitlementsListMember{
		EntitlementId: &id,
		CreatedAt:     sdkaws.Time(time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)),
		Entitlement: &aatypes.EntitlementSummaryMemberPrincipalRole{Value: aatypes.PrincipalRoleEntitlementSummary{
			Principal: &aatypes.PrincipalMemberIdentityCenter{Value: principal},
			RoleArn:   ptrStr(aaRoleARN), Account: ptrStr(testAccountID), AccountName: ptrStr("prod"),
		}},
	}
}

func aaEntNativeID(app, id string) string { return app + "/entitlement/" + id }

// aaHandlers answers ListApplications with app-a on page one and app-b plus
// an ARN-less summary on page two; GetApplication with aaAppOutput; and
// ListEntitlements with ent-1 (a user) on app-a's first page, ent-2 (a group)
// and an id-less element on its second, and ent-3 on app-b. It records every
// GetApplication and ListEntitlements application ARN.
func aaHandlers(gets, lists *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListApplications": func(in any) (any, error) {
			if in.(*accountaccess.ListApplicationsInput).NextToken == nil {
				return &accountaccess.ListApplicationsOutput{
					Applications: []aatypes.ApplicationSummary{{ApplicationArn: ptrStr(aaAppA)}},
					NextToken:    ptrStr("p2"),
				}, nil
			}
			return &accountaccess.ListApplicationsOutput{
				Applications: []aatypes.ApplicationSummary{{ApplicationArn: ptrStr(aaAppB)}, {TenantId: ptrStr("noarn")}},
			}, nil
		},
		"GetApplication": func(in any) (any, error) {
			arn := sv(in.(*accountaccess.GetApplicationInput).ApplicationArn)
			gets.add(arn)
			return aaAppOutput(arn), nil
		},
		"ListEntitlements": func(in any) (any, error) {
			lin := in.(*accountaccess.ListEntitlementsInput)
			app := sv(lin.ApplicationArn)
			lists.add(app)
			switch {
			case app == aaAppB:
				return &accountaccess.ListEntitlementsOutput{Entitlements: []aatypes.EntitlementsListMember{
					aaEntitlement("ent-3", &aatypes.IdentityCenterPrincipalMemberUserId{Value: aaUserID}),
				}}, nil
			case lin.NextToken == nil:
				return &accountaccess.ListEntitlementsOutput{
					Entitlements: []aatypes.EntitlementsListMember{
						aaEntitlement("ent-1", &aatypes.IdentityCenterPrincipalMemberUserId{Value: aaUserID}),
					},
					NextToken: ptrStr("e2"),
				}, nil
			default:
				noID := aaEntitlement("", &aatypes.IdentityCenterPrincipalMemberUserId{Value: aaUserID})
				noID.EntitlementId = nil
				return &accountaccess.ListEntitlementsOutput{Entitlements: []aatypes.EntitlementsListMember{
					aaEntitlement("ent-2", &aatypes.IdentityCenterPrincipalMemberGroupId{Value: aaGroupID}), noID,
				}}, nil
			}
		},
	}
}

// runAccountAccessAcct runs the registered aws:account-access scanner in
// aaRegion, so each test also exercises the client wiring.
func runAccountAccessAcct(t *testing.T, st *store.Store, acct *account) (int, error) {
	t.Helper()
	idx := slices.IndexFunc(registeredServices, func(e serviceEntry) bool { return e.name == "aws:account-access" })
	if idx < 0 {
		t.Fatal("aws:account-access is not registered")
	}
	total, _, err := registeredServices[idx].fn(context.Background(), acct, aaRegion, st, testScanID)
	return total, err
}

// runAccountAccess runs the scanner against handlers; an unstubbed op is denied.
func runAccountAccess(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, apiErr("AccessDeniedException", "unstubbed op"))
	return runAccountAccessAcct(t, st, acct)
}

func TestScanAccountAccess_StoresEveryPageInTheScannedRegion(t *testing.T) {
	st := newTestStore(t)
	var gets, lists, regions parentCalls
	h := aaHandlers(&gets, &lists)
	var mu sync.Mutex
	var badInputs []string
	for op, field := range map[string]func(in any) (*int32, bool){
		"ListApplications": func(in any) (*int32, bool) {
			return in.(*accountaccess.ListApplicationsInput).MaxResults, true
		},
		"ListEntitlements": func(in any) (*int32, bool) {
			lin := in.(*accountaccess.ListEntitlementsInput)
			return lin.MaxResults, lin.Filter != nil && lin.Filter.PrincipalRole == nil
		},
	} {
		inner := h[op]
		h[op] = func(in any) (any, error) {
			if limit, ok := field(in); limit == nil || *limit != 100 || !ok {
				mu.Lock()
				badInputs = append(badInputs, op)
				mu.Unlock()
			}
			return inner(in)
		}
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = recordCallRegions(opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op")), &regions)

	total, err := runAccountAccessAcct(t, st, acct)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(badInputs) != 0 {
		t.Errorf("calls without MaxResults 100 or an empty filter: %v", badInputs)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5 (2 applications, 3 entitlements)", total)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{aaRegion}) {
		t.Errorf("calls went to regions %v, want [%s]", got, aaRegion)
	}
	if got := gets.distinct(); !slices.Equal(got, []string{aaAppA, aaAppB}) {
		t.Errorf("GetApplication ARNs = %v, want [%s %s]", got, aaAppA, aaAppB)
	}
	if got := lists.distinct(); !slices.Equal(got, []string{aaAppA, aaAppB}) {
		t.Errorf("ListEntitlements ARNs = %v, want [%s %s]", got, aaAppA, aaAppB)
	}

	apps := rowsByNativeID(t, st, TypeAccountAccessApplication)
	if len(apps) != 2 {
		t.Fatalf("applications = %d, want 2: %v", len(apps), apps)
	}
	a := apps[aaAppA]
	if sv(a.Region) != aaRegion || sv(a.Status) != "ACTIVE" || sv(a.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("app-a region, status, created = %q, %q, %q; want %s, ACTIVE, 2026-01-02T03:04:05Z",
			sv(a.Region), sv(a.Status), sv(a.CreatedAt), aaRegion)
	}
	if sv(a.TagsJSON) != `{"env":"prod"}` {
		t.Errorf("app-a TagsJSON = %q, want {\"env\":\"prod\"}", sv(a.TagsJSON))
	}
	if b := apps[aaAppB]; sv(b.Status) != "DELETE_FAILED" {
		t.Errorf("app-b status = %q, want DELETE_FAILED", sv(b.Status))
	}

	ents := rowsByNativeID(t, st, TypeAccountAccessEntitlement)
	want := map[string]string{
		aaEntNativeID(aaAppA, "ent-1"): aaAppA,
		aaEntNativeID(aaAppA, "ent-2"): aaAppA,
		aaEntNativeID(aaAppB, "ent-3"): aaAppB,
	}
	if len(ents) != len(want) {
		t.Fatalf("entitlements = %d, want %d: %v", len(ents), len(want), ents)
	}
	for id, app := range want {
		e, ok := ents[id]
		if !ok {
			t.Errorf("entitlement %s not stored", id)
			continue
		}
		if sv(e.Region) != aaRegion || sv(e.CreatedAt) != "2026-02-03T04:05:06Z" {
			t.Errorf("%s region, created = %q, %q; want %s, 2026-02-03T04:05:06Z", id, sv(e.Region), sv(e.CreatedAt), aaRegion)
		}
		assertContainsNative(t, st, app, id)
	}
}

// The stored attributes name the union member each field sets, which the
// SDK's {"Value": ...} encoding loses: a user and a group principal would
// store alike.
func TestScanAccountAccess_AttributesNameUnionMembers(t *testing.T) {
	st := newTestStore(t)
	var gets, lists parentCalls
	if _, err := runAccountAccess(t, st, aaHandlers(&gets, &lists)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	app, ok := saDecodeJSON(t, rowsByNativeID(t, st, TypeAccountAccessApplication)[aaAppA].AttributesJSON).(map[string]any)
	if !ok {
		t.Fatal("app-a attributes are not an object")
	}
	wantSource := map[string]any{"IdentityCenter": map[string]any{"InstanceArn": aaInstanceARN, "ApplicationArn": aaSSOAppARN}}
	if !reflect.DeepEqual(app["IdentitySource"], wantSource) {
		t.Errorf("app-a IdentitySource = %v, want %v", app["IdentitySource"], wantSource)
	}
	if app["Status"] != "ACTIVE" {
		t.Errorf("app-a Status attribute = %v, want ACTIVE", app["Status"])
	}

	ents := rowsByNativeID(t, st, TypeAccountAccessEntitlement)
	for _, tc := range []struct {
		id        string
		principal map[string]any
	}{
		{aaEntNativeID(aaAppA, "ent-1"), map[string]any{"UserId": aaUserID}},
		{aaEntNativeID(aaAppA, "ent-2"), map[string]any{"GroupId": aaGroupID}},
	} {
		attrs, ok := saDecodeJSON(t, ents[tc.id].AttributesJSON).(map[string]any)
		if !ok {
			t.Fatalf("%s attributes are not an object", tc.id)
		}
		want := map[string]any{"PrincipalRole": map[string]any{
			"Principal": map[string]any{"IdentityCenter": tc.principal},
			"RoleArn":   aaRoleARN, "Account": testAccountID, "AccountName": "prod",
		}}
		if !reflect.DeepEqual(attrs["Entitlement"], want) {
			t.Errorf("%s Entitlement = %v, want %v", tc.id, attrs["Entitlement"], want)
		}
	}
}

// A member this SDK version does not model is kept under UnknownMember with
// its tag, at every union level.
func TestFlattenAccountAccess_UnknownMembers(t *testing.T) {
	unknown := &aatypes.UnknownUnionMember{Tag: "newMember"}
	wantUnknown := map[string]any{"UnknownMember": map[string]any{"Tag": "newMember", "Value": nil}}

	app, _ := saDecodeJSON(t, mustJSON(flattenAccountAccessApplication(&accountaccess.GetApplicationOutput{IdentitySource: unknown}))).(map[string]any)
	if !reflect.DeepEqual(app["IdentitySource"], wantUnknown) {
		t.Errorf("IdentitySource = %v, want %v", app["IdentitySource"], wantUnknown)
	}

	for _, tc := range []struct {
		name        string
		entitlement aatypes.EntitlementSummary
		want        any
	}{
		{name: "entitlement", entitlement: unknown, want: wantUnknown},
		{
			name:        "principal",
			entitlement: &aatypes.EntitlementSummaryMemberPrincipalRole{Value: aatypes.PrincipalRoleEntitlementSummary{Principal: unknown}},
			want:        map[string]any{"PrincipalRole": map[string]any{"Principal": wantUnknown, "RoleArn": nil, "Account": nil, "AccountName": nil}},
		},
		{
			name: "identity center principal",
			entitlement: &aatypes.EntitlementSummaryMemberPrincipalRole{Value: aatypes.PrincipalRoleEntitlementSummary{
				Principal: &aatypes.PrincipalMemberIdentityCenter{Value: unknown},
			}},
			want: map[string]any{"PrincipalRole": map[string]any{
				"Principal": map[string]any{"IdentityCenter": wantUnknown}, "RoleArn": nil, "Account": nil, "AccountName": nil,
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := saDecodeJSON(t, mustJSON(flattenAccountAccessEntitlement(aatypes.EntitlementsListMember{Entitlement: tc.entitlement}))).(map[string]any)
			if !reflect.DeepEqual(got["Entitlement"], tc.want) {
				t.Errorf("Entitlement = %v, want %v", got["Entitlement"], tc.want)
			}
		})
	}
}

func TestScanAccountAccess_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	total, err := runAccountAccess(t, st, map[string]func(in any) (any, error){
		"ListApplications": func(any) (any, error) { return &accountaccess.ListApplicationsOutput{}, nil },
	})
	if err != nil || total != 0 || *warns != 0 {
		t.Errorf("total, warnings, err = %d, %d, %v; want 0, 0, nil", total, *warns, err)
	}
}

// A list denial warns once and keeps the applications, and their
// entitlements, from the pages before it.
func TestScanAccountAccess_ListApplicationsDeniedKeepsListed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		secondPage bool
		wantApps   int
		wantTotal  int
	}{
		{name: "first page", wantApps: 0, wantTotal: 0},
		{name: "second page", secondPage: true, wantApps: 1, wantTotal: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "account-access:ListApplications")
			var gets, lists parentCalls
			h := aaHandlers(&gets, &lists)
			list := h["ListApplications"]
			h["ListApplications"] = func(in any) (any, error) {
				if (in.(*accountaccess.ListApplicationsInput).NextToken != nil) == tc.secondPage {
					return nil, apiErr("AccessDeniedException", "not authorized to perform: account-access:ListApplications")
				}
				return list(in)
			}
			total, err := runAccountAccess(t, st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			apps := len(rowsByNativeID(t, st, TypeAccountAccessApplication))
			if total != tc.wantTotal || apps != tc.wantApps || *warns != 1 {
				t.Errorf("total, applications, warnings = %d, %d, %d; want %d, %d, 1", total, apps, *warns, tc.wantTotal, tc.wantApps)
			}
		})
	}
}

// A ListApplications error other than a denial fails the scan before any
// entitlement is listed.
func TestScanAccountAccess_ListApplicationsErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	var gets, lists parentCalls
	h := aaHandlers(&gets, &lists)
	h["ListApplications"] = func(any) (any, error) { return nil, apiErr("ValidationException", "bad page size") }
	_, err := runAccountAccess(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := lists.distinct(); len(got) != 0 {
		t.Errorf("ListEntitlements called for %v after a failed application listing", got)
	}
}

// A service that answers with the token it was sent ends the listing after
// that page instead of looping.
func TestScanAccountAccess_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var gets, lists parentCalls
	h := aaHandlers(&gets, &lists)
	calls := 0
	h["ListApplications"] = func(any) (any, error) {
		calls++
		if calls > 2 {
			return nil, apiErr("ValidationException", "listing did not stop")
		}
		arn := aaAppA
		if calls == 2 {
			arn = aaAppB
		}
		return &accountaccess.ListApplicationsOutput{
			Applications: []aatypes.ApplicationSummary{{ApplicationArn: &arn}},
			NextToken:    ptrStr("same"),
		}, nil
	}
	h["ListEntitlements"] = func(any) (any, error) { return &accountaccess.ListEntitlementsOutput{}, nil }
	if total, err := runAccountAccess(t, st, h); err != nil || total != 2 {
		t.Errorf("total, err = %d, %v; want 2, nil", total, err)
	}
}

// aaGetFails makes GetApplication fail for app-a with err.
func aaGetFails(gets, lists *parentCalls, err error) map[string]func(in any) (any, error) {
	h := aaHandlers(gets, lists)
	get := h["GetApplication"]
	h["GetApplication"] = func(in any) (any, error) {
		if sv(in.(*accountaccess.GetApplicationInput).ApplicationArn) == aaAppA {
			return nil, err
		}
		return get(in)
	}
	return h
}

// An application whose Get fails with a skipped code is not stored and so
// not a parent; its sibling and the sibling's entitlement are.
func TestScanAccountAccess_GetApplicationSkips(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantWarns int
	}{
		{name: "deleted after listing", err: apiErr("ResourceNotFoundException", "application not found")},
		{name: "denied", err: apiErr("AccessDeniedException", "not authorized to perform: account-access:GetApplication"), wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "account-access:GetApplication")
			var gets, lists parentCalls
			total, err := runAccountAccess(t, st, aaGetFails(&gets, &lists, tc.err))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			apps := rowsByNativeID(t, st, TypeAccountAccessApplication)
			if _, ok := apps[aaAppB]; total != 2 || len(apps) != 1 || !ok || *warns != tc.wantWarns {
				t.Errorf("total, applications, warnings = %d, %v, %d; want 2, only %s, %d", total, apps, *warns, aaAppB, tc.wantWarns)
			}
			if got := lists.distinct(); !slices.Equal(got, []string{aaAppB}) {
				t.Errorf("ListEntitlements ARNs = %v, want only the stored %s", got, aaAppB)
			}
		})
	}
}

func TestScanAccountAccess_GetApplicationErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	var gets, lists parentCalls
	_, err := runAccountAccess(t, st, aaGetFails(&gets, &lists, apiErr("ValidationException", "bad arn")))
	assertAPICode(t, err, "ValidationException")
	if n := len(rowsByNativeID(t, st, TypeAccountAccessApplication)); n != 0 {
		t.Errorf("applications = %d, want 0 after a failed Get", n)
	}
	if got := lists.distinct(); len(got) != 0 {
		t.Errorf("ListEntitlements called for %v after a failed application phase", got)
	}
}

// aaListFails makes ListEntitlements fail for app with err.
func aaListFails(gets, lists *parentCalls, app string, err error) map[string]func(in any) (any, error) {
	h := aaHandlers(gets, lists)
	list := h["ListEntitlements"]
	h["ListEntitlements"] = func(in any) (any, error) {
		if sv(in.(*accountaccess.ListEntitlementsInput).ApplicationArn) == app {
			return nil, err
		}
		return list(in)
	}
	return h
}

// A ListEntitlements failure that skips one application keeps the other's
// entitlements and both applications. A ValidationException is skipped only
// for an application that is not ACTIVE (app-b is DELETE_FAILED).
func TestScanAccountAccess_ListEntitlementsSkips(t *testing.T) {
	for _, tc := range []struct {
		name      string
		app       string
		err       error
		wantEnts  []string
		wantWarns int
	}{
		{
			name: "deleted after listing", app: aaAppA, err: apiErr("ResourceNotFoundException", "application not found"),
			wantEnts: []string{aaEntNativeID(aaAppB, "ent-3")},
		},
		{
			name: "denied", app: aaAppA, err: apiErr("AccessDeniedException", "not authorized to perform: account-access:ListEntitlements"),
			wantEnts: []string{aaEntNativeID(aaAppB, "ent-3")}, wantWarns: 1,
		},
		{
			name: "throttled after retries", app: aaAppA, err: apiErr("ThrottlingException", "rate exceeded"),
			wantEnts: []string{aaEntNativeID(aaAppB, "ent-3")}, wantWarns: 1,
		},
		{
			name: "not-active application refuses", app: aaAppB, err: apiErr("ValidationException", "application is not active"),
			wantEnts: []string{aaEntNativeID(aaAppA, "ent-1"), aaEntNativeID(aaAppA, "ent-2")}, wantWarns: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "account-access:ListEntitlements")
			var gets, lists parentCalls
			total, err := runAccountAccess(t, st, aaListFails(&gets, &lists, tc.app, tc.err))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			ents := rowsByNativeID(t, st, TypeAccountAccessEntitlement)
			got := make([]string, 0, len(ents))
			for id := range ents {
				got = append(got, id)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.wantEnts) || *warns != tc.wantWarns {
				t.Errorf("entitlements, warnings = %v, %d; want %v, %d", got, *warns, tc.wantEnts, tc.wantWarns)
			}
			if want := 2 + len(tc.wantEnts); total != want {
				t.Errorf("total = %d, want %d", total, want)
			}
		})
	}
}

// A denial on every application warns once for the op.
func TestScanAccountAccess_ListEntitlementsDeniedWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "account-access:ListEntitlements")
	var gets, lists parentCalls
	h := aaHandlers(&gets, &lists)
	h["ListEntitlements"] = func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "not authorized to perform: account-access:ListEntitlements")
	}
	total, err := runAccountAccess(t, st, h)
	if err != nil || total != 2 || *warns != 1 {
		t.Errorf("total, warnings, err = %d, %d, %v; want 2, 1, nil", total, *warns, err)
	}
}

// A ValidationException from an ACTIVE application, or any other code, fails
// the phase; the applications stay stored.
func TestScanAccountAccess_ListEntitlementsErrorPropagates(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
	}{
		{name: "validation on active application", code: "ValidationException"},
		{name: "other code", code: "ServiceQuotaExceededException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var gets, lists parentCalls
			_, err := runAccountAccess(t, st, aaListFails(&gets, &lists, aaAppA, apiErr(tc.code, "failed")))
			assertAPICode(t, err, tc.code)
			if n := len(rowsByNativeID(t, st, TypeAccountAccessApplication)); n != 2 {
				t.Errorf("applications = %d, want 2 stored before the entitlement phase failed", n)
			}
		})
	}
}
