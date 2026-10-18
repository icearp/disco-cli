package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/connecthealth"
	chtypes "github.com/aws/aws-sdk-go-v2/service/connecthealth/types"
	"github.com/icearp/disco-cli/store"
)

const (
	haRegion   = "us-west-2"
	haDomainA  = "dom-aaaaaaaaaaaaaaaaaa"
	haDomainB  = "dom-bbbbbbbbbbbbbbbbbb"
	haDomainC  = "dom-cccccccccccccccccc"
	haDomARNA  = "arn:aws:health-agent:" + haRegion + ":" + testAccountID + ":domain/" + haDomainA
	haDomARNB  = "arn:aws:health-agent:" + haRegion + ":" + testAccountID + ":domain/" + haDomainB
	haSub1     = "sub-111111111111111111111"
	haSub2     = "sub-222222222222222222222"
	haSub3     = "sub-333333333333333333333"
	haSubARN1  = haDomARNA + "/subscription/" + haSub1
	haSubARN2  = haDomARNA + "/subscription/" + haSub2
	haSubARN3  = haDomARNA + "/subscription/" + haSub3
	haKeyARN   = "arn:aws:kms:" + haRegion + ":" + testAccountID + ":key/1111aaaa-0000-0000-0000-000000000000"
	haRoleARN  = "arn:aws:iam::" + testAccountID + ":role/ehr"
	haCreated  = "2026-02-03T04:05:06Z"
	haUnstubbd = "unstubbed op"
)

var haCreatedAt = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

func haDomainARN(id string) string {
	return "arn:aws:health-agent:" + haRegion + ":" + testAccountID + ":domain/" + id
}

// haHandlers answers ListDomains with domain A on page one and domain B, a
// DELETED domain C and an id-less summary on page two; GetDomain with the
// domain's body; and ListSubscriptions with two pages for domain A (one
// subscription, then an INACTIVE one, a DELETED one and an ARN-less one) and
// none for domain B. It records the GetDomain and ListSubscriptions domain ids.
func haHandlers(gets, lists *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListDomains": func(in any) (any, error) {
			if in.(*connecthealth.ListDomainsInput).NextToken == nil {
				return &connecthealth.ListDomainsOutput{
					Domains:   []chtypes.DomainSummary{{DomainId: ptrStr(haDomainA), Status: chtypes.DomainStatusActive}},
					NextToken: ptrStr("p2"),
				}, nil
			}
			return &connecthealth.ListDomainsOutput{Domains: []chtypes.DomainSummary{
				{DomainId: ptrStr(haDomainB), Status: chtypes.DomainStatusDeleting},
				{DomainId: ptrStr(haDomainC), Status: chtypes.DomainStatusDeleted},
				{Name: ptrStr("no-id")},
			}}, nil
		},
		"GetDomain": func(in any) (any, error) {
			id := sv(in.(*connecthealth.GetDomainInput).DomainId)
			gets.add(id)
			out := &connecthealth.GetDomainOutput{
				DomainId: &id, Arn: ptrStr(haDomainARN(id)), Name: ptrStr("name-" + id),
				Status: chtypes.DomainStatusActive, CreatedAt: sdkaws.Time(haCreatedAt),
			}
			if id == haDomainA {
				out.KmsKeyArn = ptrStr(haKeyARN)
				out.Tags = map[string]string{"env": "prod"}
				out.WebAppConfiguration = &chtypes.WebAppConfiguration{
					EhrRole: ptrStr(haRoleARN), IdcApplicationId: ptrStr("app-1"), IdcRegion: ptrStr("us-east-1"),
				}
			} else {
				out.Status = chtypes.DomainStatusDeleting
			}
			return out, nil
		},
		"ListSubscriptions": func(in any) (any, error) {
			lin := in.(*connecthealth.ListSubscriptionsInput)
			id := sv(lin.DomainId)
			lists.add(id)
			if id != haDomainA {
				return &connecthealth.ListSubscriptionsOutput{}, nil
			}
			sub := func(sid, arn string, status chtypes.SubscriptionStatus) chtypes.SubscriptionDescription {
				return chtypes.SubscriptionDescription{
					DomainId: ptrStr(haDomainA), SubscriptionId: ptrStr(sid), Arn: nonEmptyPtr(arn), Status: status,
					CreatedAt: sdkaws.Time(haCreatedAt), LastUpdatedAt: sdkaws.Time(haCreatedAt),
				}
			}
			if lin.NextToken == nil {
				return &connecthealth.ListSubscriptionsOutput{
					Subscriptions: []chtypes.SubscriptionDescription{sub(haSub1, haSubARN1, chtypes.SubscriptionStatusActive)},
					NextToken:     ptrStr("s2"),
				}, nil
			}
			return &connecthealth.ListSubscriptionsOutput{Subscriptions: []chtypes.SubscriptionDescription{
				sub(haSub2, haSubARN2, chtypes.SubscriptionStatusInactive),
				sub(haSub3, haSubARN3, chtypes.SubscriptionStatusDeleted),
				sub("sub-noarn", "", chtypes.SubscriptionStatusActive),
			}}, nil
		},
	}
}

// runHealthAgent runs the registered aws:health-agent scanner in haRegion
// against handlers, so each test also exercises the client wiring and the
// phase order; an unstubbed op is denied.
func runHealthAgent(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	idx := slices.IndexFunc(registeredServices, func(e serviceEntry) bool { return e.name == "aws:health-agent" })
	if idx < 0 {
		t.Fatal("aws:health-agent is not registered")
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, apiErr("AccessDeniedException", haUnstubbd))
	total, _, err := registeredServices[idx].fn(context.Background(), acct, haRegion, st, testScanID)
	return total, err
}

func TestScanHealthAgent_StoresDomainsAndSubscriptions(t *testing.T) {
	st := newTestStore(t)
	var gets, lists parentCalls
	h := haHandlers(&gets, &lists)
	listDomains, listSubs := h["ListDomains"], h["ListSubscriptions"]
	h["ListDomains"] = func(in any) (any, error) {
		lin := in.(*connecthealth.ListDomainsInput)
		if got := lin.MaxResults; got == nil || *got != 100 {
			t.Errorf("ListDomains MaxResults = %v, want 100", got)
		}
		if lin.Status != "" {
			t.Errorf("ListDomains Status = %q, want no status filter", lin.Status)
		}
		return listDomains(in)
	}
	h["ListSubscriptions"] = func(in any) (any, error) {
		if got := in.(*connecthealth.ListSubscriptionsInput).MaxResults; got == nil || *got != 100 {
			t.Errorf("ListSubscriptions MaxResults = %v, want 100", got)
		}
		return listSubs(in)
	}

	total, err := runHealthAgent(t, st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4 (2 domains, 2 subscriptions)", total)
	}
	if got := gets.distinct(); !slices.Equal(got, []string{haDomainA, haDomainB}) {
		t.Errorf("GetDomain ids = %v, want [%s %s] (DELETED and id-less skipped)", got, haDomainA, haDomainB)
	}
	if got := lists.distinct(); !slices.Equal(got, []string{haDomainA, haDomainB}) {
		t.Errorf("ListSubscriptions domain ids = %v, want [%s %s]", got, haDomainA, haDomainB)
	}

	domains := rowsByNativeID(t, st, TypeHealthAgentDomain)
	if len(domains) != 2 {
		t.Fatalf("domains = %d, want 2: %v", len(domains), domains)
	}
	a, ok := domains[haDomARNA]
	if !ok {
		t.Fatalf("domain %s not stored", haDomARNA)
	}
	if sv(a.Region) != haRegion || sv(a.Name) != "name-"+haDomainA || sv(a.Status) != "ACTIVE" || sv(a.CreatedAt) != haCreated {
		t.Errorf("domain A region, name, status, created = %q, %q, %q, %q", sv(a.Region), sv(a.Name), sv(a.Status), sv(a.CreatedAt))
	}
	if sv(a.TagsJSON) != `{"env":"prod"}` {
		t.Errorf("domain A TagsJSON = %q, want {\"env\":\"prod\"}", sv(a.TagsJSON))
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode domain A attributes: %v", err)
	}
	if attrs["KmsKeyArn"] != haKeyARN {
		t.Errorf("domain A attrs KmsKeyArn = %v, want the GetDomain key", attrs["KmsKeyArn"])
	}
	if b := domains[haDomARNB]; sv(b.Status) != "DELETING" {
		t.Errorf("domain B status = %q, want DELETING", sv(b.Status))
	}

	subs := rowsByNativeID(t, st, TypeHealthAgentSubscription)
	if len(subs) != 2 {
		t.Fatalf("subscriptions = %d, want 2 (DELETED and ARN-less skipped): %v", len(subs), subs)
	}
	s1, ok := subs[haSubARN1]
	if !ok {
		t.Fatalf("subscription %s not stored", haSubARN1)
	}
	if sv(s1.Region) != haRegion || sv(s1.Name) != haSub1 || sv(s1.Status) != "ACTIVE" || sv(s1.CreatedAt) != haCreated {
		t.Errorf("subscription 1 region, name, status, created = %q, %q, %q, %q", sv(s1.Region), sv(s1.Name), sv(s1.Status), sv(s1.CreatedAt))
	}
	if err := json.Unmarshal([]byte(s1.AttributesJSON), &attrs); err != nil {
		t.Fatalf("decode subscription attributes: %v", err)
	}
	if attrs["DomainId"] != haDomainA {
		t.Errorf("subscription attrs DomainId = %v, want %s", attrs["DomainId"], haDomainA)
	}
	if s2 := subs[haSubARN2]; sv(s2.Status) != "INACTIVE" {
		t.Errorf("subscription 2 status = %q, want INACTIVE", sv(s2.Status))
	}
	assertContainsNative(t, st, haDomARNA, haSubARN1)
	assertContainsNative(t, st, haDomARNA, haSubARN2)
}

func TestScanHealthAgent_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	total, err := runHealthAgent(t, st, map[string]func(in any) (any, error){
		"ListDomains": func(any) (any, error) { return &connecthealth.ListDomainsOutput{}, nil },
	})
	if err != nil || total != 0 || *warns != 0 {
		t.Errorf("total, warnings, err = %d, %d, %v; want 0, 0, nil", total, *warns, err)
	}
}

// A ListDomains denial warns once and keeps the domains (and their
// subscriptions) from the pages before it.
func TestScanHealthAgent_ListDomainsDeniedKeepsListedDomains(t *testing.T) {
	for _, tc := range []struct {
		name       string
		secondPage bool
		wantTotal  int
	}{
		{name: "first page", wantTotal: 0},
		{name: "second page", secondPage: true, wantTotal: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "health-agent:ListDomains")
			var gets, lists parentCalls
			h := haHandlers(&gets, &lists)
			list := h["ListDomains"]
			h["ListDomains"] = func(in any) (any, error) {
				if (in.(*connecthealth.ListDomainsInput).NextToken != nil) == tc.secondPage {
					return nil, apiErr("AccessDeniedException", "not authorized to perform: health-agent:ListDomains")
				}
				return list(in)
			}
			total, err := runHealthAgent(t, st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != tc.wantTotal || *warns != 1 {
				t.Errorf("total, warnings = %d, %d; want %d, 1", total, *warns, tc.wantTotal)
			}
		})
	}
}

// A ListDomains error other than a denial fails the scan before any
// subscription listing.
func TestScanHealthAgent_ListDomainsErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	var gets, lists parentCalls
	h := haHandlers(&gets, &lists)
	h["ListDomains"] = func(any) (any, error) { return nil, apiErr("ValidationException", "bad status") }
	_, err := runHealthAgent(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := lists.distinct(); len(got) != 0 {
		t.Errorf("ListSubscriptions called for %v after a failed domain listing", got)
	}
}

// haGetFails makes GetDomain fail for domain A with err, when err is not nil,
// and answer domain B.
func haGetFails(gets, lists *parentCalls, err error) map[string]func(in any) (any, error) {
	h := haHandlers(gets, lists)
	get := h["GetDomain"]
	h["GetDomain"] = func(in any) (any, error) {
		if err != nil && sv(in.(*connecthealth.GetDomainInput).DomainId) == haDomainA {
			return nil, err
		}
		return get(in)
	}
	return h
}

func TestScanHealthAgent_GetDomainErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string // "" when the scan succeeds
		wantWarns int
		wantRows  int
	}{
		{name: "not found skips silently", err: apiErr("ResourceNotFoundException", "domain gone"), wantRows: 1},
		{name: "denied warns once", err: apiErr("AccessDeniedException", "not authorized to perform: health-agent:GetDomain"), wantWarns: 1, wantRows: 1},
		{name: "other error propagates", err: apiErr("ValidationException", "bad id"), wantCode: "ValidationException"},
		{name: "no arn skips", wantRows: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "health-agent:GetDomain")
			var gets, lists parentCalls
			h := haGetFails(&gets, &lists, tc.err)
			if tc.err == nil {
				get := h["GetDomain"]
				h["GetDomain"] = func(in any) (any, error) {
					out, err := get(in)
					if sv(in.(*connecthealth.GetDomainInput).DomainId) == haDomainA {
						out.(*connecthealth.GetDomainOutput).Arn = nil
					}
					return out, err
				}
			}
			_, err := runHealthAgent(t, st, h)
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := rowsByNativeID(t, st, TypeHealthAgentDomain)
			if _, ok := rows[haDomARNB]; len(rows) != tc.wantRows || (tc.wantRows == 1 && !ok) || *warns != tc.wantWarns {
				t.Errorf("domains, warnings = %v, %d; want %d (domain B), %d", rows, *warns, tc.wantRows, tc.wantWarns)
			}
			if tc.wantCode == "" && !slices.Equal(lists.distinct(), []string{haDomainB}) {
				t.Errorf("ListSubscriptions domain ids = %v, want only the stored domain %s", lists.distinct(), haDomainB)
			}
		})
	}
}

// Per domain, a vanished domain is skipped silently, a denial warns once, and
// a transient error skips the domain with one warning. ValidationException
// skips a domain that is not ACTIVE (B, DELETING) with one warning but fails
// the phase for an ACTIVE one (A). The sibling's subscription and both domains
// are kept; a failed phase keeps the stored domains and their count.
func TestScanHealthAgent_ListSubscriptionsErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fail      string // the domain whose listing fails
		err       error
		wantCode  string // "" when the scan succeeds
		wantWarns int
	}{
		{name: "not found", fail: haDomainA, err: apiErr("ResourceNotFoundException", "domain gone")},
		{name: "denied", fail: haDomainA, err: apiErr("AccessDeniedException", "not authorized to perform: health-agent:ListSubscriptions"), wantWarns: 1},
		{name: "throttled", fail: haDomainA, err: apiErr("ThrottlingException", "slow down"), wantWarns: 1},
		{name: "validation on deleting domain", fail: haDomainB, err: apiErr("ValidationException", "domain is being deleted"), wantWarns: 1},
		{name: "validation on active domain", fail: haDomainA, err: apiErr("ValidationException", "bad input"), wantCode: "ValidationException"},
		{name: "other", fail: haDomainA, err: apiErr("ConflictException", "conflict"), wantCode: "ConflictException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "health-agent:ListSubscriptions")
			var gets, lists parentCalls
			h := haHandlers(&gets, &lists)
			h["ListSubscriptions"] = func(in any) (any, error) {
				id := sv(in.(*connecthealth.ListSubscriptionsInput).DomainId)
				lists.add(id)
				if id == tc.fail {
					return nil, tc.err
				}
				return &connecthealth.ListSubscriptionsOutput{Subscriptions: []chtypes.SubscriptionDescription{{
					DomainId: &id, SubscriptionId: ptrStr(haSub1),
					Arn: ptrStr(haDomainARN(id) + "/subscription/" + haSub1), Status: chtypes.SubscriptionStatusActive,
				}}}, nil
			}
			total, err := runHealthAgent(t, st, h)
			if n := len(rowsByNativeID(t, st, TypeHealthAgentDomain)); n != 2 {
				t.Errorf("domains = %d, want 2", n)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
				if total != 2 {
					t.Errorf("total = %d, want the 2 stored domains", total)
				}
				return
			}
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			sibling := haDomainA
			if tc.fail == haDomainA {
				sibling = haDomainB
			}
			want := haDomainARN(sibling) + "/subscription/" + haSub1
			subs := rowsByNativeID(t, st, TypeHealthAgentSubscription)
			if _, ok := subs[want]; total != 3 || len(subs) != 1 || !ok {
				t.Errorf("total, subscriptions = %d, %v; want 3, only %s", total, subs, want)
			}
		})
	}
}

// A service that answers with the token it was sent ends the subscription
// listing after that page instead of looping.
func TestScanHealthAgent_ListSubscriptionsRepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var gets, lists parentCalls
	h := haHandlers(&gets, &lists)
	var (
		mu    sync.Mutex
		calls = map[string]int{}
	)
	h["ListSubscriptions"] = func(in any) (any, error) {
		id := sv(in.(*connecthealth.ListSubscriptionsInput).DomainId)
		mu.Lock()
		calls[id]++
		n := calls[id]
		mu.Unlock()
		if n > 2 {
			return nil, apiErr("ConflictException", "listing did not stop")
		}
		sid := fmt.Sprintf("sub-%021d", n)
		return &connecthealth.ListSubscriptionsOutput{
			Subscriptions: []chtypes.SubscriptionDescription{{
				DomainId: &id, SubscriptionId: &sid,
				Arn: ptrStr(haDomainARN(id) + "/subscription/" + sid), Status: chtypes.SubscriptionStatusActive,
			}},
			NextToken: ptrStr("same"),
		}, nil
	}
	total, err := runHealthAgent(t, st, h)
	if err != nil || total != 6 {
		t.Errorf("total, err = %d, %v; want 6 (2 domains, 2 subscriptions each), nil", total, err)
	}
}
