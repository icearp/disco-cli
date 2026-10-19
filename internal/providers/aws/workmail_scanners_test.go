package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/workmail"
	workmailtypes "github.com/aws/aws-sdk-go-v2/service/workmail/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	wmDenied     = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: workmail:List"}
	wmBadInput   = &smithy.GenericAPIError{Code: "InvalidParameterException", Message: "bad"}
	wmOrgGone    = &smithy.GenericAPIError{Code: "OrganizationNotFoundException", Message: "gone"}
	wmOrgState   = &smithy.GenericAPIError{Code: "OrganizationStateException", Message: "organization is being deleted"}
	wmCreated    = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	wmCreatedStr = "2026-09-01T08:00:00Z"
)

const (
	wmOrgA = "arn:aws:workmail:us-east-1:123456789012:organization/m-a"
	wmOrgB = "arn:aws:workmail:us-east-1:123456789012:organization/m-b"
)

var wmOrgs = []childParent{{id: "m-a", arn: wmOrgA}, {id: "m-b", arn: wmOrgB}}

func wmClient(handlers map[string]func(in any) (any, error)) *workmail.Client {
	return workmail.NewFromConfig(opStubConfig(handlers, wmDenied))
}

// wmInputField reads a *string or *int32 field of a WorkMail list input; ok is
// false when the input has no such field.
func wmInputField(in any, field string) (v reflect.Value, ok bool) {
	f := reflect.ValueOf(in).Elem().FieldByName(field)
	if !f.IsValid() {
		return reflect.Value{}, false
	}
	return f, true
}

func wmOrgOf(in any) string {
	f, _ := wmInputField(in, "OrganizationId")
	return sv(f.Interface().(*string))
}

// wmPhase describes one organization child phase for the shared table tests.
type wmPhase struct {
	op        string
	typ       string
	run       func(*workMailScan, context.Context) (int, int, error)
	paginated bool
	// out builds the op's output holding one element per id; an empty id
	// leaves the element's identifier unset.
	out func(ids []string, next *string) any
	// suffix is the NativeID part after the organization ARN for id.
	suffix     func(id string) string
	name       func(id string) string
	status     string
	hasCreated bool
	attrKey    string
}

func wmStr(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

var wmPhases = []wmPhase{
	{
		op: "ListUsers", typ: TypeWorkMailUser, run: (*workMailScan).users, paginated: true,
		out: func(ids []string, next *string) any {
			var us []workmailtypes.User
			for _, id := range ids {
				us = append(us, workmailtypes.User{Id: wmStr(id), Name: ptrStr("n-" + id), Email: ptrStr(id + "@example.com"), State: workmailtypes.EntityStateEnabled, UserRole: workmailtypes.UserRoleUser})
			}
			return &workmail.ListUsersOutput{Users: us, NextToken: next}
		},
		suffix: func(id string) string { return "/user/" + id }, name: func(id string) string { return "n-" + id },
		status: "ENABLED", attrKey: "Email",
	},
	{
		op: "ListGroups", typ: TypeWorkMailGroup, run: (*workMailScan).groups, paginated: true,
		out: func(ids []string, next *string) any {
			var gs []workmailtypes.Group
			for _, id := range ids {
				gs = append(gs, workmailtypes.Group{Id: wmStr(id), Name: ptrStr("n-" + id), Email: ptrStr(id + "@example.com"), State: workmailtypes.EntityStateEnabled})
			}
			return &workmail.ListGroupsOutput{Groups: gs, NextToken: next}
		},
		suffix: func(id string) string { return "/group/" + id }, name: func(id string) string { return "n-" + id },
		status: "ENABLED", attrKey: "Email",
	},
	{
		op: "ListResources", typ: TypeWorkMailResource, run: (*workMailScan).resources, paginated: true,
		out: func(ids []string, next *string) any {
			var rs []workmailtypes.Resource
			for _, id := range ids {
				rs = append(rs, workmailtypes.Resource{Id: wmStr(id), Name: ptrStr("n-" + id), Type: workmailtypes.ResourceTypeRoom, State: workmailtypes.EntityStateEnabled})
			}
			return &workmail.ListResourcesOutput{Resources: rs, NextToken: next}
		},
		suffix: func(id string) string { return "/resource/" + id }, name: func(id string) string { return "n-" + id },
		status: "ENABLED", attrKey: "Type",
	},
	{
		op: "ListMailDomains", typ: TypeWorkMailMailDomain, run: (*workMailScan).mailDomains, paginated: true,
		out: func(ids []string, next *string) any {
			var ds []workmailtypes.MailDomainSummary
			for _, id := range ids {
				ds = append(ds, workmailtypes.MailDomainSummary{DomainName: wmStr(id), DefaultDomain: true})
			}
			return &workmail.ListMailDomainsOutput{MailDomains: ds, NextToken: next}
		},
		suffix: func(id string) string { return "/maildomain/" + id }, name: func(id string) string { return id },
		attrKey: "DefaultDomain",
	},
	{
		op: "ListAccessControlRules", typ: TypeWorkMailAccessControlRule, run: (*workMailScan).accessControlRules,
		out: func(ids []string, _ *string) any {
			var rs []workmailtypes.AccessControlRule
			for _, id := range ids {
				rs = append(rs, workmailtypes.AccessControlRule{Name: wmStr(id), Effect: workmailtypes.AccessControlRuleEffectDeny, IpRanges: []string{"10.0.0.0/8"}, DateCreated: &wmCreated})
			}
			return &workmail.ListAccessControlRulesOutput{Rules: rs}
		},
		suffix: func(id string) string { return "/accesscontrolrule/" + id }, name: func(id string) string { return id },
		hasCreated: true, attrKey: "IpRanges",
	},
	{
		op: "ListAvailabilityConfigurations", typ: TypeWorkMailAvailabilityConfiguration, run: (*workMailScan).availabilityConfigurations, paginated: true,
		out: func(ids []string, next *string) any {
			var cs []workmailtypes.AvailabilityConfiguration
			for _, id := range ids {
				cs = append(cs, workmailtypes.AvailabilityConfiguration{
					DomainName: wmStr(id), ProviderType: workmailtypes.AvailabilityProviderTypeEws, DateCreated: &wmCreated,
					EwsProvider: &workmailtypes.RedactedEwsAvailabilityProvider{EwsEndpoint: ptrStr("https://ews.example.com"), EwsUsername: ptrStr("svc")},
				})
			}
			return &workmail.ListAvailabilityConfigurationsOutput{AvailabilityConfigurations: cs, NextToken: next}
		},
		suffix: func(id string) string { return "/availabilityconfiguration/" + id }, name: func(id string) string { return id },
		hasCreated: true, attrKey: "EwsProvider",
	},
	{
		op: "ListImpersonationRoles", typ: TypeWorkMailImpersonationRole, run: (*workMailScan).impersonationRoles, paginated: true,
		out: func(ids []string, next *string) any {
			var rs []workmailtypes.ImpersonationRole
			for _, id := range ids {
				rs = append(rs, workmailtypes.ImpersonationRole{ImpersonationRoleId: wmStr(id), Name: ptrStr("n-" + id), Type: workmailtypes.ImpersonationRoleTypeReadOnly, DateCreated: &wmCreated})
			}
			return &workmail.ListImpersonationRolesOutput{Roles: rs, NextToken: next}
		},
		suffix: func(id string) string { return "/impersonationrole/" + id }, name: func(id string) string { return "n-" + id },
		hasCreated: true, attrKey: "Type",
	},
	{
		op: "ListMobileDeviceAccessRules", typ: TypeWorkMailMobileDeviceAccessRule, run: (*workMailScan).mobileDeviceAccessRules,
		out: func(ids []string, _ *string) any {
			var rs []workmailtypes.MobileDeviceAccessRule
			for _, id := range ids {
				rs = append(rs, workmailtypes.MobileDeviceAccessRule{MobileDeviceAccessRuleId: wmStr(id), Name: ptrStr("n-" + id), Effect: workmailtypes.MobileDeviceAccessRuleEffectAllow, DeviceTypes: []string{"iPhone"}, DateCreated: &wmCreated})
			}
			return &workmail.ListMobileDeviceAccessRulesOutput{Rules: rs}
		},
		suffix: func(id string) string { return "/mobiledeviceaccessrule/" + id }, name: func(id string) string { return "n-" + id },
		hasCreated: true, attrKey: "DeviceTypes",
	},
	{
		op: "ListMobileDeviceAccessOverrides", typ: TypeWorkMailMobileDeviceAccessOverride, run: (*workMailScan).mobileDeviceAccessOverrides, paginated: true,
		out: func(ids []string, next *string) any {
			var os []workmailtypes.MobileDeviceAccessOverride
			for _, id := range ids {
				o := workmailtypes.MobileDeviceAccessOverride{Effect: workmailtypes.MobileDeviceAccessRuleEffectDeny, DateCreated: &wmCreated}
				if id != "" {
					o.UserId, o.DeviceId = ptrStr("u-"+id), ptrStr("d-"+id)
				}
				os = append(os, o)
			}
			return &workmail.ListMobileDeviceAccessOverridesOutput{Overrides: os, NextToken: next}
		},
		suffix:     func(id string) string { return "/mobiledeviceaccessoverride/u-" + id + "/d-" + id },
		hasCreated: true, attrKey: "Effect",
	},
	{
		op: "ListPersonalAccessTokens", typ: TypeWorkMailPersonalAccessToken, run: (*workMailScan).personalAccessTokens, paginated: true,
		out: func(ids []string, next *string) any {
			var ts []workmailtypes.PersonalAccessTokenSummary
			for _, id := range ids {
				ts = append(ts, workmailtypes.PersonalAccessTokenSummary{PersonalAccessTokenId: wmStr(id), Name: ptrStr("n-" + id), UserId: ptrStr("u-1"), Scopes: []string{"read"}, DateCreated: &wmCreated})
			}
			return &workmail.ListPersonalAccessTokensOutput{PersonalAccessTokenSummaries: ts, NextToken: next}
		},
		suffix: func(id string) string { return "/personalaccesstoken/" + id }, name: func(id string) string { return "n-" + id },
		hasCreated: true, attrKey: "Scopes",
	},
}

// wmHappyHandler serves org m-a elements a1 and a2 (on two pages when the op
// is paginated) and org m-b element b1 plus one without an identifier. It
// fails the call when a paginated op is not sent the 100-item page size.
func wmHappyHandler(p wmPhase, calls *parentCalls) func(any) (any, error) {
	return func(in any) (any, error) {
		org := wmOrgOf(in)
		calls.add(org)
		if p.paginated {
			if mr, _ := wmInputField(in, "MaxResults"); mr.Interface().(*int32) == nil || *mr.Interface().(*int32) != 100 {
				return nil, wmBadInput
			}
		}
		if org == "m-b" {
			return p.out([]string{"b1", ""}, nil), nil
		}
		if !p.paginated {
			return p.out([]string{"a1", "a2"}, nil), nil
		}
		next, _ := wmInputField(in, "NextToken")
		if next.Interface().(*string) == nil {
			return p.out([]string{"a1"}, ptrStr("p2")), nil
		}
		return p.out([]string{"a2"}, nil), nil
	}
}

func wmSeedOrgs(t *testing.T, st *store.Store) {
	t.Helper()
	for _, o := range wmOrgs {
		upsertTestResource(t, st, "aws", testAccountID, TypeWorkMailOrganization, o.arn, testRegion, "{}")
	}
}

func wmScan(client workMailAPI, st *store.Store, orgs []childParent) *workMailScan {
	return &workMailScan{client: client, acct: newTestAccount(testAccountID), region: testRegion, st: st, scanID: testScanID, orgs: orgs, rejected: map[string]bool{}}
}

// Every organization is queried, every page stored under its organization,
// and an element without an identifier dropped.
func TestWorkMailChildPhases_StoresEveryOrgsChildren(t *testing.T) {
	for _, p := range wmPhases {
		t.Run(p.op, func(t *testing.T) {
			st := newTestStore(t)
			wmSeedOrgs(t, st)
			var calls parentCalls
			client := wmClient(map[string]func(any) (any, error){p.op: wmHappyHandler(p, &calls)})
			total, _, err := p.run(wmScan(client, st, wmOrgs), context.Background())
			if err != nil {
				t.Fatalf("%s: %v", p.op, err)
			}
			if got := calls.distinct(); !slices.Equal(got, []string{"m-a", "m-b"}) {
				t.Errorf("orgs queried = %v, want [m-a m-b]", got)
			}
			rows := rowsByNativeID(t, st, p.typ)
			want := map[string]string{wmOrgA + p.suffix("a1"): "a1", wmOrgA + p.suffix("a2"): "a2", wmOrgB + p.suffix("b1"): "b1"}
			if total != 3 || len(rows) != 3 {
				t.Errorf("total = %d, rows = %v; want 3 rows %v", total, slices.Sorted(maps.Keys(rows)), slices.Sorted(maps.Keys(want)))
			}
			for nid, id := range want {
				r, ok := rows[nid]
				if !ok {
					t.Errorf("row %s missing", nid)
					continue
				}
				if sv(r.Region) != testRegion {
					t.Errorf("%s Region = %q, want %q", nid, sv(r.Region), testRegion)
				}
				wantName := ""
				if p.name != nil {
					wantName = p.name(id)
				}
				if sv(r.Name) != wantName {
					t.Errorf("%s Name = %q, want %q", nid, sv(r.Name), wantName)
				}
				if sv(r.Status) != p.status {
					t.Errorf("%s Status = %q, want %q", nid, sv(r.Status), p.status)
				}
				wantCreated := ""
				if p.hasCreated {
					wantCreated = wmCreatedStr
				}
				if sv(r.CreatedAt) != wantCreated {
					t.Errorf("%s CreatedAt = %q, want %q", nid, sv(r.CreatedAt), wantCreated)
				}
				var attrs map[string]any
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
					t.Fatalf("decode attrs %s: %v", nid, err)
				}
				if _, ok := attrs[p.attrKey]; !ok {
					t.Errorf("%s attrs lack %s: %s", nid, p.attrKey, r.AttributesJSON)
				}
			}
			assertContainsNative(t, st, wmOrgA, wmOrgA+p.suffix("a2"))
			assertContainsNative(t, st, wmOrgB, wmOrgB+p.suffix("b1"))
			assertContainsEdgesDeclared(t, st)
		})
	}
}

// wmErrScenario runs p with m-a returning one element and m-b answered by mb.
func wmErrScenario(t *testing.T, p wmPhase, mb func() (any, error)) (rows map[string]store.Resource, warnings int, err error) {
	t.Helper()
	st := newTestStore(t)
	wmSeedOrgs(t, st)
	warned := countOpWarnings(st, "workmail:"+p.op)
	client := wmClient(map[string]func(any) (any, error){p.op: func(in any) (any, error) {
		if wmOrgOf(in) == "m-b" {
			return mb()
		}
		return p.out([]string{"a1"}, nil), nil
	}})
	_, _, err = p.run(wmScan(client, st, wmOrgs), context.Background())
	return rowsByNativeID(t, st, p.typ), *warned, err
}

func TestWorkMailChildPhases_PerOrgErrors(t *testing.T) {
	for _, p := range wmPhases {
		t.Run(p.op+"/OrgGoneSkippedSilently", func(t *testing.T) {
			rows, warnings, err := wmErrScenario(t, p, func() (any, error) { return nil, wmOrgGone })
			if err != nil || warnings != 0 || len(rows) != 1 {
				t.Errorf("err = %v, warnings = %d, rows = %d; want nil, 0, 1", err, warnings, len(rows))
			}
		})
		t.Run(p.op+"/OrgStateWarnsAndRejects", func(t *testing.T) {
			rows, warnings, err := wmErrScenario(t, p, func() (any, error) { return nil, wmOrgState })
			if err != nil || warnings != 1 || len(rows) != 1 {
				t.Errorf("err = %v, warnings = %d, rows = %d; want nil, 1, 1", err, warnings, len(rows))
			}
		})
		t.Run(p.op+"/OtherErrorPropagates", func(t *testing.T) {
			_, warnings, err := wmErrScenario(t, p, func() (any, error) { return nil, wmBadInput })
			var ae smithy.APIError
			if !errors.As(err, &ae) || ae.ErrorCode() != "InvalidParameterException" {
				t.Errorf("err = %v, want InvalidParameterException", err)
			}
			if warnings != 0 {
				t.Errorf("warnings = %d, want 0", warnings)
			}
		})
	}
}

func TestWorkMailChildPhases_AccessDeniedWarnsOnce(t *testing.T) {
	for _, p := range wmPhases {
		t.Run(p.op, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "workmail:"+p.op)
			total, _, err := p.run(wmScan(wmClient(nil), st, wmOrgs), context.Background())
			if err != nil || total != 0 || *warned != 1 {
				t.Errorf("err = %v, total = %d, warnings = %d; want nil, 0, 1", err, total, *warned)
			}
		})
	}
}

func TestWorkMailChildPhases_Empty(t *testing.T) {
	for _, p := range wmPhases {
		t.Run(p.op, func(t *testing.T) {
			st := newTestStore(t)
			client := wmClient(map[string]func(any) (any, error){p.op: func(any) (any, error) { return p.out(nil, nil), nil }})
			total, _, err := p.run(wmScan(client, st, wmOrgs), context.Background())
			if err != nil || total != 0 || len(rowsByNativeID(t, st, p.typ)) != 0 {
				t.Errorf("err = %v, total = %d; want nil, 0 and no rows", err, total)
			}
		})
	}
}

// WorkMail keeps listing deleted entities; they are skipped while disabled
// ones are kept.
func TestWorkMailEntities_SkipDeletedKeepDisabled(t *testing.T) {
	for _, tc := range []struct {
		op, typ, kind string
		run           func(*workMailScan, context.Context) (int, int, error)
		out           any
	}{
		{"ListUsers", TypeWorkMailUser, "user", (*workMailScan).users, &workmail.ListUsersOutput{Users: []workmailtypes.User{
			{Id: ptrStr("del"), State: workmailtypes.EntityStateDeleted}, {Id: ptrStr("off"), State: workmailtypes.EntityStateDisabled},
		}}},
		{"ListGroups", TypeWorkMailGroup, "group", (*workMailScan).groups, &workmail.ListGroupsOutput{Groups: []workmailtypes.Group{
			{Id: ptrStr("del"), State: workmailtypes.EntityStateDeleted}, {Id: ptrStr("off"), State: workmailtypes.EntityStateDisabled},
		}}},
		{"ListResources", TypeWorkMailResource, "resource", (*workMailScan).resources, &workmail.ListResourcesOutput{Resources: []workmailtypes.Resource{
			{Id: ptrStr("del"), State: workmailtypes.EntityStateDeleted}, {Id: ptrStr("off"), State: workmailtypes.EntityStateDisabled},
		}}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			client := wmClient(map[string]func(any) (any, error){tc.op: func(any) (any, error) { return tc.out, nil }})
			if _, _, err := tc.run(wmScan(client, st, wmOrgs[:1]), context.Background()); err != nil {
				t.Fatalf("%s: %v", tc.op, err)
			}
			rows := rowsByNativeID(t, st, tc.typ)
			off := wmOrgA + "/" + tc.kind + "/off"
			if len(rows) != 1 || sv(rows[off].Status) != "DISABLED" {
				t.Errorf("rows = %v; want only %s with Status DISABLED", slices.Sorted(maps.Keys(rows)), off)
			}
		})
	}
}

func TestScanWorkMailOrganizations_PagesAndAccessDenied(t *testing.T) {
	t.Run("TwoPages", func(t *testing.T) {
		st := newTestStore(t)
		client := wmClient(map[string]func(any) (any, error){"ListOrganizations": func(in any) (any, error) {
			lin := in.(*workmail.ListOrganizationsInput)
			if lin.MaxResults == nil || *lin.MaxResults != 100 {
				return nil, wmBadInput
			}
			if lin.NextToken == nil {
				return &workmail.ListOrganizationsOutput{OrganizationSummaries: []workmailtypes.OrganizationSummary{
					{OrganizationId: ptrStr("m-a"), Alias: ptrStr("corp"), State: ptrStr("Active")}, {Alias: ptrStr("no-id")},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &workmail.ListOrganizationsOutput{OrganizationSummaries: []workmailtypes.OrganizationSummary{{OrganizationId: ptrStr("m-b")}}}, nil
		}})
		parents, total, _, err := scanWorkMailOrganizations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !slices.Equal(parents, wmOrgs) {
			t.Errorf("parents = %v, want %v", parents, wmOrgs)
		}
		rows := rowsByNativeID(t, st, TypeWorkMailOrganization)
		if total != 2 || len(rows) != 2 {
			t.Fatalf("total = %d, rows = %d; want 2, 2", total, len(rows))
		}
		if a := rows[wmOrgA]; sv(a.Name) != "corp" || sv(a.Status) != "Active" {
			t.Errorf("m-a Name, Status = %q, %q; want corp, Active", sv(a.Name), sv(a.Status))
		}
		if b := rows[wmOrgB]; sv(b.Name) != "m-b" || b.Status != nil {
			t.Errorf("m-b Name, Status = %q, %v; want the id m-b and no status", sv(b.Name), b.Status)
		}
	})
	t.Run("AccessDenied", func(t *testing.T) {
		st := newTestStore(t)
		warned := countOpWarnings(st, "workmail:ListOrganizations")
		parents, total, _, err := scanWorkMailOrganizations(context.Background(), wmClient(nil), newTestAccount(testAccountID), testRegion, st, testScanID)
		if err != nil || total != 0 || len(parents) != 0 || *warned != 1 {
			t.Errorf("err = %v, total = %d, parents = %v, warnings = %d; want nil, 0, none, 1", err, total, parents, *warned)
		}
	})
}

// The top-level scan lists the organizations first and then runs every child
// phase over them; an organization whose state the first phase sees rejected
// is not queried again, and one phase's error neither stops the later phases
// nor loses their rows.
func TestScanWorkMailIn_RunsEveryPhaseOverListedOrgs(t *testing.T) {
	st := newTestStore(t)
	var (
		mu     sync.Mutex
		called = map[string][]string{}
	)
	handlers := map[string]func(any) (any, error){
		"ListOrganizations": func(any) (any, error) {
			return &workmail.ListOrganizationsOutput{OrganizationSummaries: []workmailtypes.OrganizationSummary{
				{OrganizationId: ptrStr("m-a")}, {OrganizationId: ptrStr("m-b")},
			}}, nil
		},
	}
	for _, p := range wmPhases {
		handlers[p.op] = func(in any) (any, error) {
			org := wmOrgOf(in)
			mu.Lock()
			called[p.op] = append(called[p.op], org)
			mu.Unlock()
			switch {
			case org == "m-b" && p.op == "ListUsers":
				return nil, wmOrgState
			case org == "m-a" && p.op == "ListGroups":
				return nil, wmBadInput
			}
			return p.out([]string{org + "-1"}, nil), nil
		}
	}
	var mailboxes parentCalls
	handlers["ListMailboxPermissions"] = func(in any) (any, error) {
		mailboxes.add(wmMailboxOf(in))
		return wmPerms(nil, "g-1"), nil
	}
	warned := countOpWarnings(st, "workmail:ListUsers")
	total, _, err := scanWorkMailIn(context.Background(), wmClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)

	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "InvalidParameterException" {
		t.Errorf("err = %v, want the ListGroups InvalidParameterException", err)
	}
	if *warned != 1 {
		t.Errorf("ListUsers state warnings = %d, want 1", *warned)
	}
	// 2 organizations + 1 user (m-a) + 0 groups + 1 child of m-a for each of
	// the 8 later list phases + 1 grant on each of m-a's user and resource.
	if total != 13 {
		t.Errorf("total = %d, want 13", total)
	}
	// m-a's user and resource share the id m-a-1.
	if got := mailboxes.distinct(); !slices.Equal(got, []string{"m-a/m-a-1"}) {
		t.Errorf("mailboxes queried = %v, want [m-a/m-a-1]", got)
	}
	for _, mailbox := range []string{wmOrgA + "/user/m-a-1", wmOrgA + "/resource/m-a-1"} {
		grant := mailbox + "/mailboxpermission/g-1"
		if _, ok := rowsByNativeID(t, st, TypeWorkMailMailboxPermission)[grant]; !ok {
			t.Errorf("row %s missing", grant)
			continue
		}
		assertContainsNative(t, st, mailbox, grant)
	}
	for _, p := range wmPhases {
		want := []string{"m-a"}
		if p.op == "ListUsers" {
			want = []string{"m-a", "m-b"}
		}
		got := slices.Sorted(slices.Values(called[p.op]))
		if !slices.Equal(got, want) {
			t.Errorf("%s queried orgs %v, want %v", p.op, got, want)
		}
		if p.op == "ListGroups" {
			continue
		}
		child := wmOrgA + p.suffix("m-a-1")
		if _, ok := rowsByNativeID(t, st, p.typ)[child]; !ok {
			t.Errorf("%s: row %s missing", p.op, child)
			continue
		}
		assertContainsNative(t, st, wmOrgA, child)
	}
	assertContainsEdgesDeclared(t, st)
}

// wmTopLevelHandlers serves two organizations and one child per organization
// for every child op, with the ops in override answering instead.
func wmTopLevelHandlers(override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	handlers := map[string]func(any) (any, error){
		"ListOrganizations": func(any) (any, error) {
			return &workmail.ListOrganizationsOutput{OrganizationSummaries: []workmailtypes.OrganizationSummary{
				{OrganizationId: ptrStr("m-a")}, {OrganizationId: ptrStr("m-b")},
			}}, nil
		},
	}
	for _, p := range wmPhases {
		handlers[p.op] = func(in any) (any, error) { return p.out([]string{wmOrgOf(in) + "-1"}, nil), nil }
	}
	maps.Copy(handlers, override)
	return handlers
}

var wmThrottled = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}

// A throttled phase is warned about on its own, so it cannot make the
// dispatcher classify the whole service transient: the other phases' rows
// are counted and another phase's hard error stays a hard error.
func TestScanWorkMailIn_TransientPhaseDoesNotMaskOthers(t *testing.T) {
	st := newTestStore(t)
	throttled := countOpWarnings(st, "workmail:ListUsers")
	handlers := wmTopLevelHandlers(map[string]func(any) (any, error){
		"ListUsers":  func(any) (any, error) { return nil, wmThrottled },
		"ListGroups": func(any) (any, error) { return nil, wmBadInput },
	})
	total, _, err := scanWorkMailIn(context.Background(), wmClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)

	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "InvalidParameterException" {
		t.Errorf("err = %v, want the ListGroups InvalidParameterException", err)
	}
	if *throttled != 1 {
		t.Errorf("ListUsers transient warnings = %d, want 1", *throttled)
	}
	// 2 organizations + 2 children (one per organization) for each of the 8 phases that succeed.
	if total != 18 {
		t.Errorf("total = %d, want 18", total)
	}
}

// A failed store write stays a hard error when another phase is transient.
func TestScanWorkMailIn_StoreWriteBesideTransientStaysHardError(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closing.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	insertTestScan(t, st, testScanID, "running")
	var closeOnce sync.Once
	handlers := wmTopLevelHandlers(map[string]func(any) (any, error){
		// The organizations are stored by now; every child upsert after this fails.
		"ListUsers": func(in any) (any, error) {
			closeOnce.Do(func() { _ = st.Close() })
			return wmPhases[0].out([]string{wmOrgOf(in) + "-1"}, nil), nil
		},
		"ListGroups": func(any) (any, error) { return nil, wmThrottled },
	})
	_, _, err = scanWorkMailIn(context.Background(), wmClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) || classifyServiceError(err) != outcomeStoreWrite {
		t.Errorf("err = %v classified %v; want store.ErrStoreWrite, outcomeStoreWrite", err, classifyServiceError(err))
	}
}

const (
	wmUserA1     = wmOrgA + "/user/u1"
	wmResourceA1 = wmOrgA + "/resource/r1"
	wmUserB1     = wmOrgB + "/user/b1"
	wmMailboxOp  = "workmail:ListMailboxPermissions"
)

var wmMailboxes = []workMailMailbox{
	{org: wmOrgs[0], entity: childParent{id: "u1", arn: wmUserA1}},
	{org: wmOrgs[0], entity: childParent{id: "r1", arn: wmResourceA1}},
	{org: wmOrgs[1], entity: childParent{id: "b1", arn: wmUserB1}},
}

// wmMailboxScan stores the two organizations and three mailboxes (user u1 and
// resource r1 of m-a, user b1 of m-b) and returns a scan holding them as the
// users and resources phases leave it.
func wmMailboxScan(t *testing.T, st *store.Store, client workMailAPI) *workMailScan {
	t.Helper()
	wmSeedOrgs(t, st)
	for _, m := range wmMailboxes {
		typ := TypeWorkMailUser
		if m.entity.arn == wmResourceA1 {
			typ = TypeWorkMailResource
		}
		upsertTestResource(t, st, "aws", testAccountID, typ, m.entity.arn, testRegion, "{}")
	}
	s := wmScan(client, st, wmOrgs)
	s.mailboxes = slices.Clone(wmMailboxes)
	return s
}

func wmMailboxOf(in any) string {
	mp := in.(*workmail.ListMailboxPermissionsInput)
	return sv(mp.OrganizationId) + "/" + sv(mp.EntityId)
}

func wmPerms(next *string, grantees ...string) *workmail.ListMailboxPermissionsOutput {
	out := &workmail.ListMailboxPermissionsOutput{NextToken: next}
	for _, g := range grantees {
		out.Permissions = append(out.Permissions, workmailtypes.Permission{
			GranteeId: wmStr(g), GranteeType: workmailtypes.MemberTypeUser,
			PermissionValues: []workmailtypes.PermissionType{workmailtypes.PermissionTypeFullAccess},
		})
	}
	return out
}

// Every mailbox is asked, u1's two pages are both stored, and a grant without
// a grantee id is dropped.
func TestWorkMailMailboxPermissions_StoresEveryMailboxsGrants(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := wmClient(map[string]func(any) (any, error){"ListMailboxPermissions": func(in any) (any, error) {
		mp := in.(*workmail.ListMailboxPermissionsInput)
		calls.add(wmMailboxOf(in))
		if mp.MaxResults == nil || *mp.MaxResults != 100 {
			return nil, wmBadInput
		}
		switch wmMailboxOf(in) {
		case "m-a/u1":
			if mp.NextToken == nil {
				return wmPerms(ptrStr("p2"), "g-1"), nil
			}
			return wmPerms(nil, "u-9"), nil
		case "m-a/r1":
			return wmPerms(nil, "u-1", ""), nil
		}
		return wmPerms(nil, "u-2"), nil
	}})
	total, inserted, err := wmMailboxScan(t, st, client).mailboxPermissions(context.Background())
	if err != nil {
		t.Fatalf("mailboxPermissions: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"m-a/r1", "m-a/u1", "m-b/b1"}) {
		t.Errorf("mailboxes queried = %v, want [m-a/r1 m-a/u1 m-b/b1]", got)
	}
	rows := rowsByNativeID(t, st, TypeWorkMailMailboxPermission)
	want := map[string]string{
		wmUserA1 + "/mailboxpermission/g-1":     "g-1",
		wmUserA1 + "/mailboxpermission/u-9":     "u-9",
		wmResourceA1 + "/mailboxpermission/u-1": "u-1",
		wmUserB1 + "/mailboxpermission/u-2":     "u-2",
	}
	if total != 4 || inserted != 4 || len(rows) != 4 {
		t.Errorf("total = %d, inserted = %d, rows = %v; want 4, 4, %v", total, inserted, slices.Sorted(maps.Keys(rows)), slices.Sorted(maps.Keys(want)))
	}
	for nid, grantee := range want {
		r, ok := rows[nid]
		if !ok {
			t.Errorf("row %s missing", nid)
			continue
		}
		if sv(r.Name) != grantee || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil {
			t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %v, %v; want %q, %q, nil, nil", nid, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, grantee, testRegion)
		}
		var attrs workmailtypes.Permission
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("decode attrs %s: %v", nid, err)
		}
		if !slices.Equal(attrs.PermissionValues, []workmailtypes.PermissionType{workmailtypes.PermissionTypeFullAccess}) {
			t.Errorf("%s PermissionValues = %v, want [FULL_ACCESS]", nid, attrs.PermissionValues)
		}
	}
	assertContainsNative(t, st, wmUserA1, wmUserA1+"/mailboxpermission/u-9")
	assertContainsNative(t, st, wmResourceA1, wmResourceA1+"/mailboxpermission/u-1")
	assertContainsNative(t, st, wmUserB1, wmUserB1+"/mailboxpermission/u-2")
	assertContainsEdgesDeclared(t, st)
}

func TestWorkMailMailboxPermissions_EmptyAndDenied(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		st := newTestStore(t)
		client := wmClient(map[string]func(any) (any, error){"ListMailboxPermissions": func(any) (any, error) { return wmPerms(nil), nil }})
		total, _, err := wmMailboxScan(t, st, client).mailboxPermissions(context.Background())
		if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeWorkMailMailboxPermission)) != 0 {
			t.Errorf("err = %v, total = %d; want nil, 0 and no rows", err, total)
		}
	})
	t.Run("AccessDeniedWarnsOnce", func(t *testing.T) {
		st := newTestStore(t)
		warned := countOpWarnings(st, wmMailboxOp)
		total, _, err := wmMailboxScan(t, st, wmClient(nil)).mailboxPermissions(context.Background())
		if err != nil || total != 0 || *warned != 1 {
			t.Errorf("err = %v, total = %d, warnings = %d; want nil, 0, 1", err, total, *warned)
		}
	})
}

// An organization an earlier phase saw rejected is not asked about.
func TestWorkMailMailboxPermissions_SkipsRejectedOrgs(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := wmClient(map[string]func(any) (any, error){"ListMailboxPermissions": func(in any) (any, error) {
		calls.add(wmMailboxOf(in))
		return wmPerms(nil), nil
	}})
	s := wmMailboxScan(t, st, client)
	s.rejected["m-b"] = true
	if _, _, err := s.mailboxPermissions(context.Background()); err != nil {
		t.Fatalf("mailboxPermissions: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"m-a/r1", "m-a/u1"}) {
		t.Errorf("mailboxes queried = %v, want [m-a/r1 m-a/u1]", got)
	}
}

// m-a/u1 answers with one grant; m-a/r1 and m-b/b1 answer with fail.
func wmMailboxErrScenario(t *testing.T, fail error) (rows map[string]store.Resource, total, warnings int, err error) {
	t.Helper()
	st := newTestStore(t)
	warned := countOpWarnings(st, wmMailboxOp)
	client := wmClient(map[string]func(any) (any, error){"ListMailboxPermissions": func(in any) (any, error) {
		if wmMailboxOf(in) == "m-a/u1" {
			return wmPerms(nil, "u-9"), nil
		}
		return nil, fail
	}})
	total, _, err = wmMailboxScan(t, st, client).mailboxPermissions(context.Background())
	return rowsByNativeID(t, st, TypeWorkMailMailboxPermission), total, *warned, err
}

func TestWorkMailMailboxPermissions_PerMailboxErrors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fail         error
		wantWarnings int
	}{
		{"EntityGoneSkippedSilently", &smithy.GenericAPIError{Code: "EntityNotFoundException", Message: "gone"}, 0},
		{"OrgGoneSkippedSilently", wmOrgGone, 0},
		{"OrgStateWarnsOnce", wmOrgState, 1},
		{"TransientWarnsOnce", wmThrottled, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, total, warnings, err := wmMailboxErrScenario(t, tc.fail)
			if err != nil || warnings != tc.wantWarnings || total != 1 || len(rows) != 1 {
				t.Errorf("err = %v, warnings = %d, total = %d, rows = %d; want nil, %d, 1, 1", err, warnings, total, len(rows), tc.wantWarnings)
			}
		})
	}
	for _, code := range []string{"InvalidParameterException", "EntityStateException"} {
		t.Run(code+"Propagates", func(t *testing.T) {
			_, _, warnings, err := wmMailboxErrScenario(t, &smithy.GenericAPIError{Code: code, Message: "no"})
			var ae smithy.APIError
			if !errors.As(err, &ae) || ae.ErrorCode() != code {
				t.Errorf("err = %v, want %s", err, code)
			}
			if warnings != 0 {
				t.Errorf("warnings = %d, want 0", warnings)
			}
		})
	}
}

// The users and resources phases hand on their ENABLED entities as
// mailboxes, not DISABLED ones and none from an organization whose listing
// was refused, and only once the phase stored its rows: a hard or demoted
// transient error, after which childFanOut stores nothing, hands on none.
func TestWorkMailEntities_RecordStoredEnabledMailboxes(t *testing.T) {
	for _, tc := range []struct {
		op, kind string
		run      func(*workMailScan, context.Context) (int, int, error)
		out      any
	}{
		{"ListUsers", "user", (*workMailScan).users, &workmail.ListUsersOutput{Users: []workmailtypes.User{
			{Id: ptrStr("on"), State: workmailtypes.EntityStateEnabled},
			{Id: ptrStr("off"), State: workmailtypes.EntityStateDisabled},
			{Id: ptrStr("del"), State: workmailtypes.EntityStateDeleted},
		}}},
		{"ListResources", "resource", (*workMailScan).resources, &workmail.ListResourcesOutput{Resources: []workmailtypes.Resource{
			{Id: ptrStr("on"), State: workmailtypes.EntityStateEnabled},
			{Id: ptrStr("off"), State: workmailtypes.EntityStateDisabled},
			{Id: ptrStr("del"), State: workmailtypes.EntityStateDeleted},
		}}},
	} {
		for _, mb := range []struct {
			name    string
			fail    error
			wantErr bool
			want    []workMailMailbox
		}{
			{"OrgRejected", wmOrgState, false, []workMailMailbox{{org: wmOrgs[0], entity: childParent{id: "on", arn: wmOrgA + "/" + tc.kind + "/on"}}}},
			{"HardError", wmBadInput, true, nil},
			{"Transient", wmThrottled, false, nil},
		} {
			t.Run(tc.op+"/"+mb.name, func(t *testing.T) {
				st := newTestStore(t)
				wmSeedOrgs(t, st)
				client := wmClient(map[string]func(any) (any, error){tc.op: func(in any) (any, error) {
					if wmOrgOf(in) == "m-b" {
						return nil, mb.fail
					}
					return tc.out, nil
				}})
				s := wmScan(client, st, wmOrgs)
				if _, _, err := tc.run(s, context.Background()); (err != nil) != mb.wantErr {
					t.Errorf("err = %v, want error: %v", err, mb.wantErr)
				}
				if !slices.Equal(s.mailboxes, mb.want) {
					t.Errorf("mailboxes = %v, want %v", s.mailboxes, mb.want)
				}
			})
		}
	}
}

// The mailbox permission phase runs after the entity phases, over the users
// and resources they stored, and its hard error neither costs the earlier
// phases their rows nor turns the service's error into a transient one.
func TestScanWorkMailIn_MailboxPermissionsRunLast(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	handlers := wmTopLevelHandlers(map[string]func(any) (any, error){
		"ListGroups": func(any) (any, error) { return nil, wmThrottled },
		"ListMailboxPermissions": func(in any) (any, error) {
			calls.add(wmMailboxOf(in))
			if sv(in.(*workmail.ListMailboxPermissionsInput).OrganizationId) == "m-b" {
				return nil, wmBadInput
			}
			return wmPerms(nil, "m-a-1"), nil
		},
	})
	total, _, err := scanWorkMailIn(context.Background(), wmClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)

	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	var ae smithy.APIError
	if !errors.As(err, &ae) || ae.ErrorCode() != "InvalidParameterException" {
		t.Errorf("err = %v, want the ListMailboxPermissions InvalidParameterException", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"m-a/m-a-1", "m-b/m-b-1"}) {
		t.Errorf("mailboxes queried = %v, want the user and resource of each organization", got)
	}
	// 2 organizations + 2 children (one per organization) for each of the 9
	// earlier phases that succeed; the m-b failure costs m-a its grants too,
	// as childFanOut stores nothing after a hard error.
	if total != 20 {
		t.Errorf("total = %d, want 20", total)
	}
	if len(rowsByNativeID(t, st, TypeWorkMailUser)) != 2 {
		t.Errorf("users = %d, want 2", len(rowsByNativeID(t, st, TypeWorkMailUser)))
	}
}
