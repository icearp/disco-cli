package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/datazone"
	dztypes "github.com/aws/aws-sdk-go-v2/service/datazone/types"
	smithymw "github.com/aws/smithy-go/middleware"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// stubDZGov serves the six governance list ops from per-parent page sets.
// Keys: domain id; "<domain>/<project>" for notebooks; "<domain>/<unit>" for
// rules; "<domain>/managed=<bool>" for blueprints.
type stubDZGov struct {
	dataZoneAPI
	err        error            // returned by every call when set
	errFor     map[string]error // per-parent error
	calls      []string
	pools      map[string][][]dztypes.AccountPoolSummary
	blueprints map[string][][]dztypes.EnvironmentBlueprintSummary
	notebooks  map[string][][]dztypes.NotebookSummary
	rules      map[string][][]dztypes.RuleSummary
	subs       map[string][][]dztypes.SubscriptionSummary
	grants     map[string][][]dztypes.SubscriptionGrantSummary
	ruleTypes  []dztypes.RuleTargetType
	cascaded   []*bool
}

func (s *stubDZGov) call(key string) error {
	s.calls = append(s.calls, key)
	if s.err != nil {
		return s.err
	}
	return s.errFor[key]
}

// dzPage returns the page a NextToken points at; tokens are page indexes.
func dzPage[T any](pages [][]T, tok *string) ([]T, *string) {
	i := 0
	if tok != nil {
		i, _ = strconv.Atoi(*tok)
	}
	if i >= len(pages) {
		return nil, nil
	}
	var next *string
	if i+1 < len(pages) {
		n := strconv.Itoa(i + 1)
		next = &n
	}
	return pages[i], next
}

func (s *stubDZGov) ListAccountPools(_ context.Context, in *datazone.ListAccountPoolsInput, _ ...func(*datazone.Options)) (*datazone.ListAccountPoolsOutput, error) {
	key := *in.DomainIdentifier
	if err := s.call(key); err != nil {
		return nil, err
	}
	items, next := dzPage(s.pools[key], in.NextToken)
	return &datazone.ListAccountPoolsOutput{Items: items, NextToken: next}, nil
}

func (s *stubDZGov) ListEnvironmentBlueprints(_ context.Context, in *datazone.ListEnvironmentBlueprintsInput, _ ...func(*datazone.Options)) (*datazone.ListEnvironmentBlueprintsOutput, error) {
	key := fmt.Sprintf("%s/managed=%t", *in.DomainIdentifier, *in.Managed)
	if err := s.call(key); err != nil {
		return nil, err
	}
	items, next := dzPage(s.blueprints[key], in.NextToken)
	return &datazone.ListEnvironmentBlueprintsOutput{Items: items, NextToken: next}, nil
}

func (s *stubDZGov) ListNotebooks(_ context.Context, in *datazone.ListNotebooksInput, _ ...func(*datazone.Options)) (*datazone.ListNotebooksOutput, error) {
	key := *in.DomainIdentifier + "/" + *in.OwningProjectIdentifier
	if err := s.call(key); err != nil {
		return nil, err
	}
	items, next := dzPage(s.notebooks[key], in.NextToken)
	return &datazone.ListNotebooksOutput{Items: items, NextToken: next}, nil
}

func (s *stubDZGov) ListRules(_ context.Context, in *datazone.ListRulesInput, _ ...func(*datazone.Options)) (*datazone.ListRulesOutput, error) {
	key := *in.DomainIdentifier + "/" + *in.TargetIdentifier
	s.ruleTypes = append(s.ruleTypes, in.TargetType)
	s.cascaded = append(s.cascaded, in.IncludeCascaded)
	if err := s.call(key); err != nil {
		return nil, err
	}
	items, next := dzPage(s.rules[key], in.NextToken)
	return &datazone.ListRulesOutput{Items: items, NextToken: next}, nil
}

func (s *stubDZGov) ListSubscriptions(_ context.Context, in *datazone.ListSubscriptionsInput, _ ...func(*datazone.Options)) (*datazone.ListSubscriptionsOutput, error) {
	key := *in.DomainIdentifier
	if err := s.call(key); err != nil {
		return nil, err
	}
	items, next := dzPage(s.subs[key], in.NextToken)
	return &datazone.ListSubscriptionsOutput{Items: items, NextToken: next}, nil
}

func (s *stubDZGov) ListSubscriptionGrants(_ context.Context, in *datazone.ListSubscriptionGrantsInput, _ ...func(*datazone.Options)) (*datazone.ListSubscriptionGrantsOutput, error) {
	key := *in.DomainIdentifier
	if err := s.call(key); err != nil {
		return nil, err
	}
	items, next := dzPage(s.grants[key], in.NextToken)
	return &datazone.ListSubscriptionGrantsOutput{Items: items, NextToken: next}, nil
}

type dzScanFn func(context.Context, dataZoneAPI, *account, string, *store.Store, string, []*dzDomain) (int, int, error)

var dzCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// dzGovCase describes one governance scanner. fill seeds two pages for
// parent `first` (page 1 leads with an id-less element that must be skipped)
// and one item for parent `second`; ids are the child ids, in order.
type dzGovCase struct {
	name            string
	fn              dzScanFn
	typ             string
	kind            string
	first           string
	second          string
	parents         int // calls made for the fixture domains when nothing errors
	notFoundModeled bool
	wantStatus      string
	hasCreated      bool
	attrKey         string
	fill            func(s *stubDZGov, first, second string)
}

func dzFixtureDomains() []*dzDomain {
	return []*dzDomain{
		{id: "dom-1", projectIDs: []string{"prj-1", "prj-2"}, unitIDs: []string{"root-1", "du-2"}},
		{id: "dom-2", projectIDs: []string{"prj-3"}, unitIDs: []string{"root-2"}},
	}
}

func dzGovCases() []dzGovCase {
	return []dzGovCase{
		{
			name: "account-pools", fn: scanDataZoneAccountPools, typ: TypeDataZoneAccountPool, kind: "account-pool",
			first: "dom-1", second: "dom-2", parents: 2, attrKey: "DomainUnitId",
			fill: func(s *stubDZGov, first, second string) {
				el := func(id string) dztypes.AccountPoolSummary {
					return dztypes.AccountPoolSummary{Id: ptrStr(id), Name: ptrStr("n-" + id), DomainUnitId: ptrStr("du-x")}
				}
				s.pools = map[string][][]dztypes.AccountPoolSummary{
					first:  {{{Name: ptrStr("no-id")}, el("a")}, {el("b")}},
					second: {{el("c")}},
				}
			},
		},
		{
			name: "environment-blueprints", fn: scanDataZoneEnvironmentBlueprints, typ: TypeDataZoneEnvironmentBlueprint, kind: "environment-blueprint",
			first: "dom-1/managed=false", second: "dom-2/managed=true", parents: 4, notFoundModeled: true, hasCreated: true, attrKey: "Provider",
			fill: func(s *stubDZGov, first, second string) {
				el := func(id string) dztypes.EnvironmentBlueprintSummary {
					return dztypes.EnvironmentBlueprintSummary{Id: ptrStr(id), Name: ptrStr("n-" + id), Provider: ptrStr("Amazon DataZone"), CreatedAt: &dzCreated}
				}
				s.blueprints = map[string][][]dztypes.EnvironmentBlueprintSummary{
					first:  {{{Name: ptrStr("no-id")}, el("a")}, {el("b")}},
					second: {{el("c")}},
				}
			},
		},
		{
			name: "notebooks", fn: scanDataZoneNotebooks, typ: TypeDataZoneNotebook, kind: "notebook",
			first: "dom-1/prj-2", second: "dom-2/prj-3", parents: 3, wantStatus: "ACTIVE", hasCreated: true, attrKey: "OwningProjectId",
			fill: func(s *stubDZGov, first, second string) {
				el := func(id string) dztypes.NotebookSummary {
					return dztypes.NotebookSummary{Id: ptrStr(id), Name: ptrStr("n-" + id), OwningProjectId: ptrStr("prj-2"), Status: dztypes.NotebookStatusActive, CreatedAt: &dzCreated}
				}
				s.notebooks = map[string][][]dztypes.NotebookSummary{
					first:  {{{Name: ptrStr("no-id")}, el("a")}, {el("b")}},
					second: {{el("c")}},
				}
			},
		},
		{
			name: "rules", fn: scanDataZoneRules, typ: TypeDataZoneRule, kind: "rule",
			first: "dom-1/du-2", second: "dom-2/root-2", parents: 3, notFoundModeled: true, attrKey: "Revision",
			fill: func(s *stubDZGov, first, second string) {
				el := func(id string) dztypes.RuleSummary {
					return dztypes.RuleSummary{Identifier: ptrStr(id), Name: ptrStr("n-" + id), Revision: ptrStr("1")}
				}
				s.rules = map[string][][]dztypes.RuleSummary{
					first:  {{{Name: ptrStr("no-id")}, el("a")}, {el("b")}},
					second: {{el("c")}},
				}
			},
		},
		{
			name: "subscriptions", fn: scanDataZoneSubscriptions, typ: TypeDataZoneSubscription, kind: "subscription",
			first: "dom-1", second: "dom-2", parents: 2, notFoundModeled: true, wantStatus: "APPROVED", hasCreated: true, attrKey: "SubscriptionRequestId",
			fill: func(s *stubDZGov, first, second string) {
				el := func(id string) dztypes.SubscriptionSummary {
					return dztypes.SubscriptionSummary{Id: ptrStr(id), Status: dztypes.SubscriptionStatusApproved, SubscriptionRequestId: ptrStr("sr-1"), CreatedAt: &dzCreated}
				}
				s.subs = map[string][][]dztypes.SubscriptionSummary{
					first:  {{{SubscriptionRequestId: ptrStr("no-id")}, el("a")}, {el("b")}},
					second: {{el("c")}},
				}
			},
		},
		{
			name: "subscription-grants", fn: scanDataZoneSubscriptionGrants, typ: TypeDataZoneSubscriptionGrant, kind: "subscription-grant",
			first: "dom-1", second: "dom-2", parents: 2, notFoundModeled: true, wantStatus: "COMPLETED", hasCreated: true, attrKey: "SubscriptionTargetId",
			fill: func(s *stubDZGov, first, second string) {
				el := func(id string) dztypes.SubscriptionGrantSummary {
					return dztypes.SubscriptionGrantSummary{Id: ptrStr(id), Status: dztypes.SubscriptionGrantOverallStatusCompleted, SubscriptionTargetId: ptrStr("st-1"), CreatedAt: &dzCreated}
				}
				s.grants = map[string][][]dztypes.SubscriptionGrantSummary{
					first:  {{{SubscriptionTargetId: ptrStr("no-id")}, el("a")}, {el("b")}},
					second: {{el("c")}},
				}
			},
		},
	}
}

// dzParentDomain is the domain id of a stub key ("dom-1/prj-2" → "dom-1").
func dzParentDomain(key string) string {
	domain, _, _ := strings.Cut(key, "/")
	return domain
}

func dzRows(t *testing.T, st *store.Store, typ string) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: testAccountID, Types: []string{typ}, Limit: util.AllResources, IncludeManaged: true})
	if err != nil {
		t.Fatalf("ListResources %s: %v", typ, err)
	}
	out := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		out[r.NativeID] = r
	}
	return out
}

func TestScanDataZoneGovernance_PagesFieldsAndFanout(t *testing.T) {
	for _, tc := range dzGovCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			stub := &stubDZGov{}
			tc.fill(stub, tc.first, tc.second)

			total, _, err := tc.fn(context.Background(), stub, acct, testRegion, st, testScanID, dzFixtureDomains())
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 3 {
				t.Errorf("total=%d, want 3 (two pages of %s plus %s, id-less element skipped)", total, tc.first, tc.second)
			}
			if len(stub.calls) != tc.parents+1 {
				t.Errorf("calls=%v, want every parent queried once plus one second page (%d)", stub.calls, tc.parents+1)
			}
			rows := dzRows(t, st, tc.typ)
			if len(rows) != 3 {
				t.Fatalf("stored %d rows, want 3: %v", len(rows), rows)
			}
			for _, w := range []struct{ parent, id string }{{tc.first, "a"}, {tc.first, "b"}, {tc.second, "c"}} {
				native := dzARN(testRegion, acct.ID, dzParentDomain(w.parent), tc.kind, w.id)
				r, ok := rows[native]
				if !ok {
					t.Errorf("missing row %s", native)
					continue
				}
				if r.Type != tc.typ || sv(r.Region) != testRegion {
					t.Errorf("%s: type=%s region=%s", native, r.Type, sv(r.Region))
				}
			}
			a := rows[dzARN(testRegion, acct.ID, dzParentDomain(tc.first), tc.kind, "a")]
			wantName := "n-a"
			if tc.typ == TypeDataZoneSubscription || tc.typ == TypeDataZoneSubscriptionGrant {
				wantName = "a"
			}
			if sv(a.Name) != wantName {
				t.Errorf("name=%q, want %q", sv(a.Name), wantName)
			}
			if sv(a.Status) != tc.wantStatus {
				t.Errorf("status=%q, want %q", sv(a.Status), tc.wantStatus)
			}
			if tc.hasCreated && sv(a.CreatedAt) != dzCreated.Format(time.RFC3339) {
				t.Errorf("createdAt=%q, want %q", sv(a.CreatedAt), dzCreated.Format(time.RFC3339))
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
				t.Fatalf("attrs: %v", err)
			}
			if _, ok := attrs[tc.attrKey]; !ok {
				t.Errorf("attrs missing %s: %s", tc.attrKey, a.AttributesJSON)
			}
		})
	}
}

func TestScanDataZoneGovernance_Empty(t *testing.T) {
	for _, tc := range dzGovCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := tc.fn(context.Background(), &stubDZGov{}, newTestAccount(testAccountID), testRegion, st, testScanID, dzFixtureDomains())
			if err != nil || total != 0 {
				t.Fatalf("total=%d err=%v, want 0/nil", total, err)
			}
			if rows := dzRows(t, st, tc.typ); len(rows) != 0 {
				t.Errorf("stored %d rows, want 0", len(rows))
			}
		})
	}
}

// Denied on every parent: one warning per op, and the fan-out still visits
// every sibling.
func TestScanDataZoneGovernance_AccessDeniedWarnsOnce(t *testing.T) {
	for _, tc := range dzGovCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := 0
			st.OnWarn = func(store.ScanWarning) { warnings++ }
			stub := &stubDZGov{err: apiErr("AccessDeniedException", "User: arn:aws:iam::123:role/x is not authorized to perform: datazone:List")}
			total, _, err := tc.fn(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, dzFixtureDomains())
			if err != nil || total != 0 {
				t.Fatalf("total=%d err=%v, want 0/nil", total, err)
			}
			if warnings != 1 {
				t.Errorf("warnings=%d, want 1", warnings)
			}
			if len(stub.calls) != tc.parents {
				t.Errorf("calls=%v, want all %d parents tried", stub.calls, tc.parents)
			}
		})
	}
}

func TestScanDataZoneGovernance_OtherErrorPropagates(t *testing.T) {
	for _, tc := range dzGovCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			stub := &stubDZGov{err: apiErr("ValidationException", "bad input")}
			_, _, err := tc.fn(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, dzFixtureDomains())
			if !isAPIErrorCode(err, "ValidationException") {
				t.Fatalf("err=%v, want ValidationException propagated", err)
			}
		})
	}
}

// A parent deleted between listing and the child call is skipped only for ops
// whose SDK error set models ResourceNotFoundException; elsewhere it is a real
// error.
func TestScanDataZoneGovernance_ParentNotFound(t *testing.T) {
	for _, tc := range dzGovCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := 0
			st.OnWarn = func(store.ScanWarning) { warnings++ }
			stub := &stubDZGov{errFor: map[string]error{tc.first: apiErr("ResourceNotFoundException", "gone")}}
			tc.fill(stub, tc.first, tc.second)
			total, _, err := tc.fn(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, dzFixtureDomains())
			if !tc.notFoundModeled {
				if !isAPIErrorCode(err, "ResourceNotFoundException") {
					t.Fatalf("err=%v, want unmodeled ResourceNotFoundException propagated", err)
				}
				return
			}
			if err != nil || total != 1 {
				t.Fatalf("total=%d err=%v, want the sibling's 1 row", total, err)
			}
			if warnings != 0 {
				t.Errorf("warnings=%d, want 0 for a vanished parent", warnings)
			}
		})
	}
}

// Denied mid-pagination keeps the rows already read for that parent.
func TestDzCollect_DeniedMidPagesKeepsItems(t *testing.T) {
	st := newTestStore(t)
	f := &dzFanout{st: st, acct: newTestAccount(testAccountID), region: testRegion, op: "datazone:ListRules"}
	calls := 0
	items, err := dzCollect(context.Background(), f, "dom-1", func() bool { return calls < 2 }, func(context.Context) ([]int, error) {
		calls++
		if calls == 2 {
			return nil, apiErr("AccessDeniedException", "denied")
		}
		return []int{1}, nil
	})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v, want [1]/nil", items, err)
	}
}

func TestScanDataZoneEnvironmentBlueprints_ManagedFlag(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubDZGov{blueprints: map[string][][]dztypes.EnvironmentBlueprintSummary{
		"dom-1/managed=false": {{{Id: ptrStr("custom")}}},
		"dom-1/managed=true":  {{{Id: ptrStr("aws")}}},
	}}
	if _, _, err := scanDataZoneEnvironmentBlueprints(context.Background(), stub, acct, testRegion, st, testScanID, []*dzDomain{{id: "dom-1"}}); err != nil {
		t.Fatal(err)
	}
	rows := dzRows(t, st, TypeDataZoneEnvironmentBlueprint)
	for id, want := range map[string]bool{"custom": false, "aws": true} {
		r, ok := rows[dzARN(testRegion, acct.ID, "dom-1", "environment-blueprint", id)]
		if !ok {
			t.Errorf("missing blueprint %s", id)
			continue
		}
		if r.ManagedByProvider != want {
			t.Errorf("%s: ManagedByProvider=%t, want %t", id, r.ManagedByProvider, want)
		}
	}
}

func TestScanDataZoneEnvironmentBlueprints_BothPassesStoredOnceAsManaged(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dup := []dztypes.EnvironmentBlueprintSummary{{Id: ptrStr("dup")}}
	stub := &stubDZGov{blueprints: map[string][][]dztypes.EnvironmentBlueprintSummary{
		"dom-1/managed=false": {dup},
		"dom-1/managed=true":  {dup},
	}}
	total, _, err := scanDataZoneEnvironmentBlueprints(context.Background(), stub, acct, testRegion, st, testScanID, []*dzDomain{{id: "dom-1"}})
	if err != nil || total != 1 {
		t.Fatalf("total=%d err=%v, want 1/nil", total, err)
	}
	r, ok := dzRows(t, st, TypeDataZoneEnvironmentBlueprint)[dzARN(testRegion, acct.ID, "dom-1", "environment-blueprint", "dup")]
	if !ok || !r.ManagedByProvider {
		t.Errorf("row present=%t managed=%t, want present and managed", ok, r.ManagedByProvider)
	}
}

// A rule the server returns under two units (cascaded) is stored once.
func TestScanDataZoneRules_SameRuleUnderTwoUnitsStoredOnce(t *testing.T) {
	st := newTestStore(t)
	rule := []dztypes.RuleSummary{{Identifier: ptrStr("ru-1")}}
	stub := &stubDZGov{rules: map[string][][]dztypes.RuleSummary{
		"dom-1/root-1": {rule},
		"dom-1/du-2":   {rule},
	}}
	domains := []*dzDomain{{id: "dom-1", unitIDs: []string{"root-1", "du-2"}}}
	total, _, err := scanDataZoneRules(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, domains)
	if err != nil || total != 1 {
		t.Fatalf("total=%d err=%v, want 1/nil", total, err)
	}
	if rows := dzRows(t, st, TypeDataZoneRule); len(rows) != 1 {
		t.Errorf("stored %d rule rows, want 1", len(rows))
	}
}

func TestScanDataZoneRules_TargetsDomainUnits(t *testing.T) {
	stub := &stubDZGov{}
	if _, _, err := scanDataZoneRules(context.Background(), stub, newTestAccount(testAccountID), testRegion, newTestStore(t), testScanID, dzFixtureDomains()); err != nil {
		t.Fatal(err)
	}
	want := []string{"dom-1/root-1", "dom-1/du-2", "dom-2/root-2"}
	if fmt.Sprint(stub.calls) != fmt.Sprint(want) {
		t.Errorf("calls=%v, want %v", stub.calls, want)
	}
	for _, tt := range stub.ruleTypes {
		if tt != dztypes.RuleTargetTypeDomainUnit {
			t.Errorf("TargetType=%q, want DOMAIN_UNIT", tt)
		}
	}
	for _, c := range stub.cascaded {
		if c == nil || *c {
			t.Errorf("IncludeCascaded=%v, want explicit false", c)
		}
	}
}

// stubDZUnits serves a two-level domain-unit tree under root-1.
type stubDZUnits struct {
	dataZoneAPI
	children map[string][]string
}

func (s *stubDZUnits) ListDomainUnitsForParent(_ context.Context, in *datazone.ListDomainUnitsForParentInput, _ ...func(*datazone.Options)) (*datazone.ListDomainUnitsForParentOutput, error) {
	var items []dztypes.DomainUnitSummary
	for _, id := range s.children[*in.ParentDomainUnitIdentifier] {
		items = append(items, dztypes.DomainUnitSummary{Id: ptrStr(id)})
	}
	return &datazone.ListDomainUnitsForParentOutput{Items: items}, nil
}

func TestScanDataZoneDomainUnits_RecordsUnitIDsIncludingRoot(t *testing.T) {
	stub := &stubDZUnits{children: map[string][]string{"root-1": {"du-a"}, "du-a": {"du-b"}}}
	d := &dzDomain{id: "dom-1", rootUnitID: "root-1"}
	if _, _, err := scanDataZoneDomainUnits(context.Background(), stub, newTestAccount(testAccountID), testRegion, newTestStore(t), testScanID, []*dzDomain{d}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"root-1", "du-a", "du-b"}; fmt.Sprint(d.unitIDs) != fmt.Sprint(want) {
		t.Errorf("unitIDs=%v, want %v", d.unitIDs, want)
	}
}

// stubDZDomains serves ListDomains and a denied GetDomain.
type stubDZDomains struct {
	dataZoneAPI
	domains []dztypes.DomainSummary
}

func (s *stubDZDomains) ListDomains(context.Context, *datazone.ListDomainsInput, ...func(*datazone.Options)) (*datazone.ListDomainsOutput, error) {
	return &datazone.ListDomainsOutput{Items: s.domains}, nil
}

func (s *stubDZDomains) GetDomain(context.Context, *datazone.GetDomainInput, ...func(*datazone.Options)) (*datazone.GetDomainOutput, error) {
	return nil, apiErr("AccessDeniedException", "User: arn:aws:iam::123:role/x is not authorized to perform: datazone:GetDomain")
}

// GetDomain denied: domains are still stored from the list body, the scan
// continues, and one warning says the root unit (rules) was not walked.
func TestScanDataZoneDomains_GetDomainDeniedWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := 0
	st.OnWarn = func(store.ScanWarning) { warnings++ }
	var items []dztypes.DomainSummary
	for _, id := range []string{"dom-1", "dom-2"} {
		arn := "arn:aws:datazone:" + testRegion + ":" + testAccountID + ":domain/" + id
		items = append(items, dztypes.DomainSummary{Arn: &arn, Id: ptrStr(id)})
	}
	domains, total, _, err := scanDataZoneDomains(context.Background(), &stubDZDomains{domains: items}, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 2 || len(domains) != 2 {
		t.Fatalf("domains=%d total=%d err=%v, want 2/2/nil", len(domains), total, err)
	}
	if warnings != 1 {
		t.Errorf("warnings=%d, want 1", warnings)
	}
	for _, d := range domains {
		if d.rootUnitID != "" {
			t.Errorf("%s rootUnitID=%q, want empty", d.id, d.rootUnitID)
		}
	}
}

// dzTopLevelResponses stubs a full scanDataZone run: one domain with root unit
// root-1, one project, and one row from each governance op.
func dzTopLevelResponses() map[string][]stubCall {
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	repeat := func(n int, out any) []stubCall {
		calls := make([]stubCall, n)
		for i := range calls {
			calls[i] = stubCall{Output: out}
		}
		return calls
	}
	domARN := "arn:aws:datazone:" + testRegion + ":" + testAccountID + ":domain/dom-1"
	return map[string][]stubCall{
		"ListDomains": one(&datazone.ListDomainsOutput{Items: []dztypes.DomainSummary{{Arn: &domARN, Id: ptrStr("dom-1"), Name: ptrStr("d")}}}),
		"GetDomain":   one(&datazone.GetDomainOutput{Id: ptrStr("dom-1"), RootDomainUnitId: ptrStr("root-1")}),

		"ListDomainUnitsForParent":               one(&datazone.ListDomainUnitsForParentOutput{}),
		"ListProjectProfiles":                    one(&datazone.ListProjectProfilesOutput{}),
		"ListProjects":                           one(&datazone.ListProjectsOutput{Items: []dztypes.ProjectSummary{{Id: ptrStr("prj-1"), Name: ptrStr("p")}}}),
		"ListProjectMemberships":                 one(&datazone.ListProjectMembershipsOutput{}),
		"SearchGroupProfiles":                    repeat(len(dzGroupTypes), &datazone.SearchGroupProfilesOutput{}),
		"SearchUserProfiles":                     repeat(len(dzUserTypes), &datazone.SearchUserProfilesOutput{}),
		"ListEnvironmentProfiles":                one(&datazone.ListEnvironmentProfilesOutput{}),
		"ListEnvironmentBlueprintConfigurations": one(&datazone.ListEnvironmentBlueprintConfigurationsOutput{}),
		"ListEnvironments":                       one(&datazone.ListEnvironmentsOutput{}),
		"ListDataSources":                        one(&datazone.ListDataSourcesOutput{}),
		"ListConnections":                        one(&datazone.ListConnectionsOutput{}),

		"ListEnvironmentBlueprints": {
			{Output: &datazone.ListEnvironmentBlueprintsOutput{Items: []dztypes.EnvironmentBlueprintSummary{{Id: ptrStr("bp-1")}}}},
			{Output: &datazone.ListEnvironmentBlueprintsOutput{}},
		},
		"ListRules":              one(&datazone.ListRulesOutput{Items: []dztypes.RuleSummary{{Identifier: ptrStr("ru-1")}}}),
		"ListSubscriptions":      one(&datazone.ListSubscriptionsOutput{Items: []dztypes.SubscriptionSummary{{Id: ptrStr("sub-1")}}}),
		"ListSubscriptionGrants": one(&datazone.ListSubscriptionGrantsOutput{Items: []dztypes.SubscriptionGrantSummary{{Id: ptrStr("sg-1")}}}),
		"ListAccountPools":       one(&datazone.ListAccountPoolsOutput{Items: []dztypes.AccountPoolSummary{{Id: ptrStr("ap-1")}}}),
		"ListNotebooks":          one(&datazone.ListNotebooksOutput{Items: []dztypes.NotebookSummary{{Id: ptrStr("nb-1")}}}),
	}
}

// TestScanDataZone_GovernancePhasesWiredAndOrdered runs the top-level scan
// against a real client. Account pools and notebooks are V2-only APIs that
// may reject a V1 domain and do not model ResourceNotFound, so they must run
// after rules, subscriptions and grants: an error in either still leaves every
// earlier phase's rows stored.
func TestScanDataZone_GovernancePhasesWiredAndOrdered(t *testing.T) {
	type row struct{ typ, kind, id string }
	rule := row{TypeDataZoneRule, "rule", "ru-1"}
	sub := row{TypeDataZoneSubscription, "subscription", "sub-1"}
	grant := row{TypeDataZoneSubscriptionGrant, "subscription-grant", "sg-1"}
	bp := row{TypeDataZoneEnvironmentBlueprint, "environment-blueprint", "bp-1"}
	pool := row{TypeDataZoneAccountPool, "account-pool", "ap-1"}
	nb := row{TypeDataZoneNotebook, "notebook", "nb-1"}
	for _, tc := range []struct {
		name    string
		failOp  string
		want    []row
		wantErr bool
	}{
		{name: "all succeed", want: []row{bp, rule, sub, grant, pool, nb}},
		{name: "account pools fail", failOp: "ListAccountPools", want: []row{bp, rule, sub, grant}, wantErr: true},
		{name: "notebooks fail", failOp: "ListNotebooks", want: []row{bp, rule, sub, grant, pool}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			responses := dzTopLevelResponses()
			if tc.failOp != "" {
				responses[tc.failOp] = []stubCall{{Err: apiErr("ValidationException", "not a V2 domain")}}
			}
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: sdkaws.Config{
				Region:           testRegion,
				Credentials:      credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
				RetryMaxAttempts: 1,
				APIOptions:       []func(*smithymw.Stack) error{stubResponses(t, responses)},
			}}

			_, _, err := scanDataZone(context.Background(), acct, testRegion, st, testScanID)
			if tc.wantErr != isAPIErrorCode(err, "ValidationException") {
				t.Fatalf("scanDataZone err=%v, wantErr=%t", err, tc.wantErr)
			}
			for _, w := range tc.want {
				if _, ok := dzRows(t, st, w.typ)[dzARN(testRegion, testAccountID, "dom-1", w.kind, w.id)]; !ok {
					t.Errorf("%s row %s not stored", w.typ, w.id)
				}
			}
		})
	}
}
