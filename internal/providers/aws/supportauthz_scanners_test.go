package aws

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/supportauthz"
	satypes "github.com/aws/aws-sdk-go-v2/service/supportauthz/types"
	"github.com/icearp/disco-cli/store"
)

const (
	saRegion  = "eu-west-1"
	saPermitA = "arn:aws:supportauthz:" + saRegion + ":" + testAccountID + ":supportpermit/permit-a"
	saPermitB = "arn:aws:supportauthz:" + saRegion + ":" + testAccountID + ":supportpermit/permit-b"
	saKeyARN  = "arn:aws:kms:" + saRegion + ":" + testAccountID + ":key/1111aaaa-0000-0000-0000-000000000000"
)

var (
	saAfter  = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	saBefore = time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)
)

// saPermitOutput is the GetSupportPermit body for arn. permit-a authorizes
// all actions on two resources inside a time window; permit-b authorizes one
// action on every resource in the region, with no window.
func saPermitOutput(arn string) *supportauthz.GetSupportPermitOutput {
	out := &supportauthz.GetSupportPermitOutput{
		Arn:            &arn,
		Status:         satypes.SupportPermitStatusActive,
		CreatedAt:      sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		SigningKeyInfo: &satypes.SigningKeyInfoMemberKmsKey{Value: saKeyARN},
	}
	if arn == saPermitA {
		out.Name = ptrStr("permitA")
		out.Description = ptrStr("case 123 debugging")
		out.Tags = map[string]string{"env": "prod"}
		out.Permit = &satypes.Permit{
			Actions:   &satypes.ActionSetMemberAllActions{},
			Resources: &satypes.ResourceSetMemberResources{Value: []string{"res-1", "res-2"}},
			Conditions: []satypes.Condition{
				&satypes.ConditionMemberAllowAfter{Value: saAfter},
				&satypes.ConditionMemberAllowBefore{Value: saBefore},
			},
		}
		return out
	}
	out.Name = ptrStr("permitB")
	out.Status = satypes.SupportPermitStatusInactive
	out.Permit = &satypes.Permit{
		Actions:   &satypes.ActionSetMemberActions{Value: []string{"ec2:RebootInstance"}},
		Resources: &satypes.ResourceSetMemberAllResourcesInRegion{},
	}
	return out
}

// saHandlers answers ListSupportPermits with permit-a on page one and permit-b
// plus an ARN-less summary on page two, and GetSupportPermit with
// saPermitOutput. It records every GetSupportPermit identifier.
func saHandlers(gets *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListSupportPermits": func(in any) (any, error) {
			if in.(*supportauthz.ListSupportPermitsInput).NextToken == nil {
				return &supportauthz.ListSupportPermitsOutput{
					SupportPermits: []satypes.SupportPermitSummary{{Arn: ptrStr(saPermitA)}},
					NextToken:      ptrStr("p2"),
				}, nil
			}
			return &supportauthz.ListSupportPermitsOutput{
				SupportPermits: []satypes.SupportPermitSummary{{Arn: ptrStr(saPermitB)}, {Name: ptrStr("noarn")}},
			}, nil
		},
		"GetSupportPermit": func(in any) (any, error) {
			id := sv(in.(*supportauthz.GetSupportPermitInput).SupportPermitIdentifier)
			gets.add(id)
			return saPermitOutput(id), nil
		},
	}
}

// runSupportAuthZAcct runs the registered aws:supportauthz scanner in
// saRegion, so each test also exercises the client wiring.
func runSupportAuthZAcct(t *testing.T, st *store.Store, acct *account) (int, error) {
	t.Helper()
	idx := slices.IndexFunc(registeredServices, func(e serviceEntry) bool { return e.name == "aws:supportauthz" })
	if idx < 0 {
		t.Fatal("aws:supportauthz is not registered")
	}
	total, _, err := registeredServices[idx].fn(context.Background(), acct, saRegion, st, testScanID)
	return total, err
}

// runSupportAuthZ runs the scanner against handlers; an unstubbed op is denied.
func runSupportAuthZ(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, apiErr("AccessDeniedException", "unstubbed op"))
	return runSupportAuthZAcct(t, st, acct)
}

func TestScanSupportPermits_StoresEveryPageInTheScannedRegion(t *testing.T) {
	st := newTestStore(t)
	var gets, regions parentCalls
	h := saHandlers(&gets)
	list := h["ListSupportPermits"]
	h["ListSupportPermits"] = func(in any) (any, error) {
		lin := in.(*supportauthz.ListSupportPermitsInput)
		if got := lin.MaxResults; got == nil || *got != 100 {
			t.Errorf("ListSupportPermits MaxResults = %v, want 100", got)
		}
		if len(lin.SupportPermitStatuses) != 0 {
			t.Errorf("ListSupportPermits SupportPermitStatuses = %v, want no status filter", lin.SupportPermitStatuses)
		}
		return list(in)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = recordCallRegions(opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op")), &regions)

	total, err := runSupportAuthZAcct(t, st, acct)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{saRegion}) {
		t.Errorf("calls went to regions %v, want [%s]", got, saRegion)
	}
	if got := gets.distinct(); !slices.Equal(got, []string{saPermitA, saPermitB}) {
		t.Errorf("GetSupportPermit identifiers = %v, want [%s %s]", got, saPermitA, saPermitB)
	}
	rows := rowsByNativeID(t, st, TypeSupportAuthZSupportPermit)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2: %v", len(rows), rows)
	}
	a, ok := rows[saPermitA]
	if !ok {
		t.Fatalf("permit %s not stored", saPermitA)
	}
	if sv(a.Region) != saRegion || sv(a.Name) != "permitA" || sv(a.Status) != "ACTIVE" {
		t.Errorf("region, name, status = %q, %q, %q; want %s, permitA, ACTIVE", sv(a.Region), sv(a.Name), sv(a.Status), saRegion)
	}
	if sv(a.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("CreatedAt = %q, want 2026-01-02T03:04:05Z", sv(a.CreatedAt))
	}
	if sv(a.TagsJSON) != `{"env":"prod"}` {
		t.Errorf("TagsJSON = %q, want {\"env\":\"prod\"}", sv(a.TagsJSON))
	}
	if b := rows[saPermitB]; sv(b.Status) != "INACTIVE" {
		t.Errorf("permit-b status = %q, want INACTIVE", sv(b.Status))
	}
}

// saDecodeJSON decodes s into generic JSON values, so comparisons ignore key
// order.
func saDecodeJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return v
}

// The stored attributes name the union member each permit field sets, which
// the SDK's {"Value": ...} union encoding loses. A permit with no conditions
// stores them as null.
func TestScanSupportPermits_AttributesNameUnionMembers(t *testing.T) {
	st := newTestStore(t)
	var gets parentCalls
	if _, err := runSupportAuthZ(t, st, saHandlers(&gets)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeSupportAuthZSupportPermit)
	for _, tc := range []struct {
		arn  string
		want string
	}{
		{saPermitA, `{"Actions":{"AllActions":{}},"Resources":{"Resources":["res-1","res-2"]},
			"Conditions":[{"AllowAfter":"2026-03-01T00:00:00Z"},{"AllowBefore":"2026-03-08T00:00:00Z"}]}`},
		{saPermitB, `{"Actions":{"Actions":["ec2:RebootInstance"]},"Resources":{"AllResourcesInRegion":{}},"Conditions":null}`},
	} {
		attrs, ok := saDecodeJSON(t, rows[tc.arn].AttributesJSON).(map[string]any)
		if !ok {
			t.Fatalf("%s attributes are not an object: %s", tc.arn, rows[tc.arn].AttributesJSON)
		}
		if want := saDecodeJSON(t, tc.want); !reflect.DeepEqual(attrs["Permit"], want) {
			t.Errorf("%s Permit = %v, want %v", tc.arn, attrs["Permit"], want)
		}
		if want := map[string]any{"KmsKey": saKeyARN}; !reflect.DeepEqual(attrs["SigningKeyInfo"], want) {
			t.Errorf("%s SigningKeyInfo = %v, want %v", tc.arn, attrs["SigningKeyInfo"], want)
		}
		if tc.arn == saPermitA && attrs["Description"] != "case 123 debugging" {
			t.Errorf("%s Description = %v, want the GetSupportPermit description", tc.arn, attrs["Description"])
		}
	}
}

// A member this SDK version does not model is kept under UnknownMember with
// its tag; an absent Permit stores null and an absent SigningKeyInfo {}.
func TestFlattenSupportPermit_UnknownAndAbsentMembers(t *testing.T) {
	unknown := &satypes.UnknownUnionMember{Tag: "newMember"}
	wantUnknown := map[string]any{"UnknownMember": map[string]any{"Tag": "newMember", "Value": nil}}
	for _, tc := range []struct {
		name       string
		out        *supportauthz.GetSupportPermitOutput
		wantPermit any
		wantKey    any
	}{
		{
			name: "unknown members",
			out: &supportauthz.GetSupportPermitOutput{
				SigningKeyInfo: unknown,
				Permit:         &satypes.Permit{Actions: unknown, Resources: unknown, Conditions: []satypes.Condition{unknown}},
			},
			wantPermit: map[string]any{"Actions": wantUnknown, "Resources": wantUnknown, "Conditions": []any{wantUnknown}},
			wantKey:    wantUnknown,
		},
		{
			name:       "absent",
			out:        &supportauthz.GetSupportPermitOutput{},
			wantPermit: nil,
			wantKey:    map[string]any{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := saDecodeJSON(t, mustJSON(flattenSupportPermit(tc.out))).(map[string]any)
			if !ok {
				t.Fatal("attributes are not an object")
			}
			if !reflect.DeepEqual(got["Permit"], tc.wantPermit) {
				t.Errorf("Permit = %v, want %v", got["Permit"], tc.wantPermit)
			}
			if !reflect.DeepEqual(got["SigningKeyInfo"], tc.wantKey) {
				t.Errorf("SigningKeyInfo = %v, want %v", got["SigningKeyInfo"], tc.wantKey)
			}
		})
	}
}

func TestScanSupportPermits_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "supportauthz:ListSupportPermits")
	total, err := runSupportAuthZ(t, st, map[string]func(in any) (any, error){
		"ListSupportPermits": func(any) (any, error) { return &supportauthz.ListSupportPermitsOutput{}, nil },
	})
	if err != nil || total != 0 || *warns != 0 {
		t.Errorf("total, warnings, err = %d, %d, %v; want 0, 0, nil", total, *warns, err)
	}
}

// A list denial warns once and keeps the permits from the pages before it.
func TestScanSupportPermits_ListDeniedKeepsListedPermits(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failPage bool // fail page two instead of page one
		want     int
	}{
		{name: "first page", want: 0},
		{name: "second page", failPage: true, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "supportauthz:ListSupportPermits")
			var gets parentCalls
			h := saHandlers(&gets)
			list := h["ListSupportPermits"]
			h["ListSupportPermits"] = func(in any) (any, error) {
				if (in.(*supportauthz.ListSupportPermitsInput).NextToken != nil) == tc.failPage {
					return nil, apiErr("AccessDeniedException", "not authorized to perform: supportauthz:ListSupportPermits")
				}
				return list(in)
			}
			total, err := runSupportAuthZ(t, st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := len(rowsByNativeID(t, st, TypeSupportAuthZSupportPermit))
			if total != tc.want || rows != tc.want || *warns != 1 {
				t.Errorf("total, rows, warnings = %d, %d, %d; want %d, %d, 1", total, rows, *warns, tc.want, tc.want)
			}
		})
	}
}

func TestScanSupportPermits_ListErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	_, err := runSupportAuthZ(t, st, map[string]func(in any) (any, error){
		"ListSupportPermits": func(any) (any, error) {
			return nil, apiErr("ValidationException", "bad status filter")
		},
	})
	assertAPICode(t, err, "ValidationException")
}

// saGetFails makes GetSupportPermit fail for permit-a with err and answer
// permit-b.
func saGetFails(gets *parentCalls, err error) map[string]func(in any) (any, error) {
	h := saHandlers(gets)
	get := h["GetSupportPermit"]
	h["GetSupportPermit"] = func(in any) (any, error) {
		if sv(in.(*supportauthz.GetSupportPermitInput).SupportPermitIdentifier) == saPermitA {
			return nil, err
		}
		return get(in)
	}
	return h
}

// A permit deleted between the list and its Get is dropped silently; its
// sibling is stored.
func TestScanSupportPermits_GetNotFoundSkipsPermit(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "supportauthz:GetSupportPermit")
	var gets parentCalls
	total, err := runSupportAuthZ(t, st, saGetFails(&gets, apiErr("ResourceNotFoundException", "permit not found")))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeSupportAuthZSupportPermit)
	if _, ok := rows[saPermitB]; total != 1 || len(rows) != 1 || !ok || *warns != 0 {
		t.Errorf("total, rows, warnings = %d, %v, %d; want 1, only %s, 0", total, rows, *warns, saPermitB)
	}
}

// A denied Get warns once for the op, however many permits it hits.
func TestScanSupportPermits_GetDeniedWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "supportauthz:GetSupportPermit")
	var gets parentCalls
	h := saHandlers(&gets)
	h["GetSupportPermit"] = func(any) (any, error) {
		return nil, apiErr("AccessDeniedException", "not authorized to perform: supportauthz:GetSupportPermit")
	}
	total, err := runSupportAuthZ(t, st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || *warns != 1 {
		t.Errorf("total, warnings = %d, %d; want 0, 1", total, *warns)
	}
}

func TestScanSupportPermits_GetErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	var gets parentCalls
	_, err := runSupportAuthZ(t, st, saGetFails(&gets, apiErr("ValidationException", "bad identifier")))
	assertAPICode(t, err, "ValidationException")
	if n := len(rowsByNativeID(t, st, TypeSupportAuthZSupportPermit)); n != 0 {
		t.Errorf("rows = %d, want 0 after a failed Get", n)
	}
}

// A service that answers with the token it was sent ends the listing after
// that page instead of looping.
func TestScanSupportPermits_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var gets parentCalls
	h := saHandlers(&gets)
	calls := 0
	h["ListSupportPermits"] = func(any) (any, error) {
		calls++
		if calls > 2 {
			return nil, apiErr("ValidationException", "listing did not stop")
		}
		arn := saPermitA
		if calls == 2 {
			arn = saPermitB
		}
		return &supportauthz.ListSupportPermitsOutput{
			SupportPermits: []satypes.SupportPermitSummary{{Arn: &arn}},
			NextToken:      ptrStr("same"),
		}, nil
	}
	total, err := runSupportAuthZ(t, st, h)
	if err != nil || total != 2 {
		t.Errorf("total, err = %d, %v; want 2, nil", total, err)
	}
}
