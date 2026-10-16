package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appintegrations"
	appintegrationstypes "github.com/aws/aws-sdk-go-v2/service/appintegrations/types"
	"github.com/icearp/disco-cli/store"
)

const (
	appintTestPrefix   = "arn:aws:app-integrations:us-east-1:123456789012:"
	appintTestAppA     = appintTestPrefix + "application/aaaaaaaa-0000-0000-0000-000000000001"
	appintTestAppB     = appintTestPrefix + "application/aaaaaaaa-0000-0000-0000-000000000002"
	appintTestDIID     = "dddddddd-0000-0000-0000-000000000001"
	appintTestDI       = appintTestPrefix + "data-integration/" + appintTestDIID
	appintTestEI       = appintTestPrefix + "event-integration/orders"
	appintTestEINoName = appintTestPrefix + "event-integration/nameless"

	appintTestAppAssocA1 = appintTestPrefix + "application-association/aaaaaaaa-0000-0000-0000-000000000001/a1"
	appintTestAppAssocA2 = appintTestPrefix + "application-association/aaaaaaaa-0000-0000-0000-000000000001/a2"
	appintTestAppAssocB1 = appintTestPrefix + "application-association/aaaaaaaa-0000-0000-0000-000000000002/b1"
	appintTestDIAssoc    = appintTestPrefix + "data-integration-association/dddddddd-0000-0000-0000-000000000001/d1"
	appintTestDIAssoc2   = appintTestPrefix + "data-integration-association/dddddddd-0000-0000-0000-000000000001/d2"
	appintTestEIAssoc    = appintTestPrefix + "event-integration-association/orders/eeeeeeee-0000-0000-0000-000000000001"
	appintTestEIAssoc2   = appintTestPrefix + "event-integration-association/orders/eeeeeeee-0000-0000-0000-000000000002"
)

var (
	appintDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: app-integrations:ListApplicationAssociations")
	appintBadInput = apiErr("InvalidRequestException", "bad")
	appintThrottle = apiErr("ThrottlingException", "slow down")

	// The association rows each parent kind yields with appintHandlers.
	appintAppAssocs = []string{appintTestAppAssocA1, appintTestAppAssocA2, appintTestAppAssocB1}
	appintDIAssocs  = []string{appintTestDIAssoc, appintTestDIAssoc2}
	appintEIAssocs  = []string{appintTestEIAssoc, appintTestEIAssoc2}
)

// appintCalls records, per association op, the parent identifier each call took.
type appintCalls struct{ apps, dataIntegrations, eventIntegrations parentCalls }

// appintPageLimitOK reports whether a list call asked for the model's page cap;
// the handlers reject any other size.
func appintPageLimitOK(maxResults *int32) bool {
	return sdkaws.ToInt32(maxResults) == appIntegrationsPageLimit
}

var appintBadPageSize = apiErr("ValidationException", "maxResults must be at most 50")

// appintHandlers answers every AppIntegrations op the scan calls: applications
// A and B plus one without an ARN, one data integration, an event integration
// named "orders" and one with no name. Every association listing spans two
// pages: application A's (plus an element without an ARN), the data
// integration's and the event integration's; B has one association.
func appintHandlers(calls *appintCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListApplications": func(in any) (any, error) {
			if !appintPageLimitOK(in.(*appintegrations.ListApplicationsInput).MaxResults) {
				return nil, appintBadPageSize
			}
			return &appintegrations.ListApplicationsOutput{Applications: []appintegrationstypes.ApplicationSummary{
				{Arn: sdkaws.String(appintTestAppA), Name: sdkaws.String("a")},
				{Name: sdkaws.String("arnless")},
				{Arn: sdkaws.String(appintTestAppB), Name: sdkaws.String("b")},
			}}, nil
		},
		"ListDataIntegrations": func(in any) (any, error) {
			if !appintPageLimitOK(in.(*appintegrations.ListDataIntegrationsInput).MaxResults) {
				return nil, appintBadPageSize
			}
			return &appintegrations.ListDataIntegrationsOutput{DataIntegrations: []appintegrationstypes.DataIntegrationSummary{
				{Arn: sdkaws.String(appintTestDI), Name: sdkaws.String("crm")},
			}}, nil
		},
		"ListEventIntegrations": func(in any) (any, error) {
			if !appintPageLimitOK(in.(*appintegrations.ListEventIntegrationsInput).MaxResults) {
				return nil, appintBadPageSize
			}
			return &appintegrations.ListEventIntegrationsOutput{EventIntegrations: []appintegrationstypes.EventIntegration{
				{EventIntegrationArn: sdkaws.String(appintTestEI), Name: sdkaws.String("orders"), EventBridgeBus: sdkaws.String("default")},
				{EventIntegrationArn: sdkaws.String(appintTestEINoName)},
			}}, nil
		},
		"ListApplicationAssociations": func(in any) (any, error) {
			p := in.(*appintegrations.ListApplicationAssociationsInput)
			calls.apps.add(sdkaws.ToString(p.ApplicationId))
			if !appintPageLimitOK(p.MaxResults) {
				return nil, appintBadPageSize
			}
			switch sdkaws.ToString(p.ApplicationId) {
			case appintTestAppA:
				if p.NextToken == nil {
					return &appintegrations.ListApplicationAssociationsOutput{
						ApplicationAssociations: []appintegrationstypes.ApplicationAssociationSummary{
							{ApplicationAssociationArn: sdkaws.String(appintTestAppAssocA1), ApplicationArn: sdkaws.String(appintTestAppA), ClientId: sdkaws.String("client-1")},
							{ApplicationArn: sdkaws.String(appintTestAppA)},
						},
						NextToken: sdkaws.String("page2"),
					}, nil
				}
				return &appintegrations.ListApplicationAssociationsOutput{ApplicationAssociations: []appintegrationstypes.ApplicationAssociationSummary{
					{ApplicationAssociationArn: sdkaws.String(appintTestAppAssocA2), ApplicationArn: sdkaws.String(appintTestAppA)},
				}}, nil
			case appintTestAppB:
				return &appintegrations.ListApplicationAssociationsOutput{ApplicationAssociations: []appintegrationstypes.ApplicationAssociationSummary{
					{ApplicationAssociationArn: sdkaws.String(appintTestAppAssocB1), ApplicationArn: sdkaws.String(appintTestAppB)},
				}}, nil
			}
			return nil, apiErr("ResourceNotFoundException", "no such application")
		},
		"ListDataIntegrationAssociations": func(in any) (any, error) {
			p := in.(*appintegrations.ListDataIntegrationAssociationsInput)
			calls.dataIntegrations.add(sdkaws.ToString(p.DataIntegrationIdentifier))
			if !appintPageLimitOK(p.MaxResults) {
				return nil, appintBadPageSize
			}
			if p.NextToken == nil {
				return &appintegrations.ListDataIntegrationAssociationsOutput{
					DataIntegrationAssociations: []appintegrationstypes.DataIntegrationAssociationSummary{{
						DataIntegrationAssociationArn: sdkaws.String(appintTestDIAssoc), DataIntegrationArn: sdkaws.String(appintTestDI),
						DestinationURI: sdkaws.String("arn://wisdom"), ClientId: sdkaws.String("wisdom"),
					}},
					NextToken: sdkaws.String("page2"),
				}, nil
			}
			return &appintegrations.ListDataIntegrationAssociationsOutput{DataIntegrationAssociations: []appintegrationstypes.DataIntegrationAssociationSummary{
				{DataIntegrationAssociationArn: sdkaws.String(appintTestDIAssoc2), DataIntegrationArn: sdkaws.String(appintTestDI)},
			}}, nil
		},
		"ListEventIntegrationAssociations": func(in any) (any, error) {
			p := in.(*appintegrations.ListEventIntegrationAssociationsInput)
			calls.eventIntegrations.add(sdkaws.ToString(p.EventIntegrationName))
			if !appintPageLimitOK(p.MaxResults) {
				return nil, appintBadPageSize
			}
			if p.NextToken == nil {
				return &appintegrations.ListEventIntegrationAssociationsOutput{
					EventIntegrationAssociations: []appintegrationstypes.EventIntegrationAssociation{{
						EventIntegrationAssociationArn: sdkaws.String(appintTestEIAssoc), EventIntegrationName: sdkaws.String("orders"),
						EventBridgeRuleName: sdkaws.String("orders-rule"), ClientId: sdkaws.String("connect"),
					}},
					NextToken: sdkaws.String("page2"),
				}, nil
			}
			return &appintegrations.ListEventIntegrationAssociationsOutput{EventIntegrationAssociations: []appintegrationstypes.EventIntegrationAssociation{
				{EventIntegrationAssociationArn: sdkaws.String(appintTestEIAssoc2), EventIntegrationName: sdkaws.String("orders")},
			}}, nil
		},
	}
}

func scanAppIntegrationsTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, appintDenied)
	total, _, err := scanAppIntegrations(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

// appintAssocIDs returns the NativeIDs of every stored association, sorted.
func appintAssocIDs(t *testing.T, st *store.Store) []string {
	t.Helper()
	var ids []string
	for _, typ := range []string{
		TypeAppIntegrationsApplicationAssociation,
		TypeAppIntegrationsDataIntegrationAssociation,
		TypeAppIntegrationsEventIntegrationAssociation,
	} {
		ids = append(ids, nativeIDs(rowsByNativeID(t, st, typ))...)
	}
	slices.Sort(ids)
	return ids
}

func appintSorted(lists ...[]string) []string {
	out := slices.Concat(lists...)
	slices.Sort(out)
	return out
}

func TestScanAppIntegrations_StoresAssociations(t *testing.T) {
	st := newTestStore(t)
	var calls appintCalls
	total, err := scanAppIntegrationsTopLevel(t, st, appintHandlers(&calls))
	if err != nil {
		t.Fatalf("scanAppIntegrations: %v", err)
	}
	// 2 applications + 1 data integration + 2 event integrations + 7 associations
	if total != 12 {
		t.Errorf("total = %d, want 12", total)
	}
	if got := calls.apps.distinct(); !slices.Equal(got, []string{appintTestAppA, appintTestAppB}) {
		t.Errorf("ListApplicationAssociations parents = %v, want both application ARNs", got)
	}
	if got := calls.dataIntegrations.distinct(); !slices.Equal(got, []string{appintTestDIID}) {
		t.Errorf("ListDataIntegrationAssociations parents = %v, want the id from the data integration ARN", got)
	}
	if got := calls.eventIntegrations.distinct(); !slices.Equal(got, []string{"orders"}) {
		t.Errorf("ListEventIntegrationAssociations parents = %v, want only the named event integration", got)
	}
	for _, tc := range []struct {
		typ  string
		want []string
	}{
		{TypeAppIntegrationsApplicationAssociation, appintAppAssocs},
		{TypeAppIntegrationsDataIntegrationAssociation, appintDIAssocs},
		{TypeAppIntegrationsEventIntegrationAssociation, appintEIAssocs},
	} {
		rows := rowsByNativeID(t, st, tc.typ)
		if got := nativeIDs(rows); !slices.Equal(got, tc.want) {
			t.Errorf("%s rows = %v, want %v", tc.typ, got, tc.want)
		}
		for id, r := range rows {
			if r.Type != tc.typ || sv(r.Region) != testRegion {
				t.Errorf("%s: type %q region %q", id, r.Type, sv(r.Region))
			}
		}
	}
	if _, ok := rowsByNativeID(t, st, TypeAppIntegrationsEventIntegration)[appintTestEINoName]; !ok {
		t.Error("nameless event integration missing; only its association listing is skipped")
	}

	var attrs struct {
		EventIntegrationName string `json:"EventIntegrationName"`
		EventBridgeRuleName  string `json:"EventBridgeRuleName"`
	}
	if err := json.Unmarshal([]byte(rowsByNativeID(t, st, TypeAppIntegrationsEventIntegrationAssociation)[appintTestEIAssoc].AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs.EventIntegrationName != "orders" || attrs.EventBridgeRuleName != "orders-rule" {
		t.Errorf("attrs = %+v, want the SDK element verbatim", attrs)
	}

	assertContainsNative(t, st, appintTestAppA, appintTestAppAssocA1)
	assertContainsNative(t, st, appintTestAppA, appintTestAppAssocA2)
	assertContainsNative(t, st, appintTestAppB, appintTestAppAssocB1)
	for _, id := range appintDIAssocs {
		assertContainsNative(t, st, appintTestDI, id)
	}
	for _, id := range appintEIAssocs {
		assertContainsNative(t, st, appintTestEI, id)
	}
}

func TestScanAppIntegrations_NoAssociations(t *testing.T) {
	st := newTestStore(t)
	var calls appintCalls
	h := appintHandlers(&calls)
	h["ListApplicationAssociations"] = func(in any) (any, error) {
		calls.apps.add(sdkaws.ToString(in.(*appintegrations.ListApplicationAssociationsInput).ApplicationId))
		return &appintegrations.ListApplicationAssociationsOutput{}, nil
	}
	h["ListDataIntegrationAssociations"] = func(any) (any, error) { return &appintegrations.ListDataIntegrationAssociationsOutput{}, nil }
	h["ListEventIntegrationAssociations"] = func(any) (any, error) { return &appintegrations.ListEventIntegrationAssociationsOutput{}, nil }
	total, err := scanAppIntegrationsTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanAppIntegrations: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want the 5 parents", total)
	}
	if got := appintAssocIDs(t, st); len(got) != 0 {
		t.Errorf("association rows = %v, want none", got)
	}
	if got := calls.apps.distinct(); !slices.Equal(got, []string{appintTestAppA, appintTestAppB}) {
		t.Errorf("ListApplicationAssociations parents = %v, want both applications", got)
	}
}

// appintParentKey returns the parent identifier an association list input
// carries, or "" for a parent list input.
func appintParentKey(in any) string {
	switch p := in.(type) {
	case *appintegrations.ListApplicationAssociationsInput:
		return sdkaws.ToString(p.ApplicationId)
	case *appintegrations.ListDataIntegrationAssociationsInput:
		return sdkaws.ToString(p.DataIntegrationIdentifier)
	case *appintegrations.ListEventIntegrationAssociationsInput:
		return sdkaws.ToString(p.EventIntegrationName)
	}
	return ""
}

// A parent deleted since listing, denied, or failing transiently after retries
// drops only its own associations: deleted is silent, a denial warns once per
// op however many parents hit it, and a transient error warns once. Any other
// error fails that phase (storing none of its rows) and is returned, while the
// other association phases and the parents keep their rows.
func TestScanAppIntegrations_FailedAssociationParent(t *testing.T) {
	gone := apiErr("ResourceNotFoundException", "gone")
	const (
		appOp = "ListApplicationAssociations"
		diOp  = "ListDataIntegrationAssociations"
		eiOp  = "ListEventIntegrationAssociations"
	)
	label := map[string]string{
		appOp: appIntegrationsListApplicationAssociationsOp,
		diOp:  appIntegrationsListDataIntegrationAssociationsOp,
		eiOp:  appIntegrationsListEventIntegrationAssociationsOp,
	}
	onlyB := []string{appintTestAppAssocB1}
	for _, tc := range []struct {
		name      string
		op        string
		fail      map[string]error // by parent identifier
		wantCode  string           // "" means the scan returns nil
		wantRows  []string
		wantWarns int
	}{
		{"application gone", appOp, map[string]error{appintTestAppA: gone}, "", appintSorted(onlyB, appintDIAssocs, appintEIAssocs), 0},
		{"applications denied", appOp, map[string]error{appintTestAppA: appintDenied, appintTestAppB: appintDenied}, "", appintSorted(appintDIAssocs, appintEIAssocs), 1},
		{"application throttled", appOp, map[string]error{appintTestAppA: appintThrottle}, "", appintSorted(onlyB, appintDIAssocs, appintEIAssocs), 1},
		{"application other error", appOp, map[string]error{appintTestAppA: appintBadInput}, "InvalidRequestException", appintSorted(appintDIAssocs, appintEIAssocs), 0},
		{"data integration gone", diOp, map[string]error{appintTestDIID: gone}, "", appintSorted(appintAppAssocs, appintEIAssocs), 0},
		{"data integration denied", diOp, map[string]error{appintTestDIID: appintDenied}, "", appintSorted(appintAppAssocs, appintEIAssocs), 1},
		{"data integration throttled", diOp, map[string]error{appintTestDIID: appintThrottle}, "", appintSorted(appintAppAssocs, appintEIAssocs), 1},
		{"data integration other error", diOp, map[string]error{appintTestDIID: appintBadInput}, "InvalidRequestException", appintSorted(appintAppAssocs, appintEIAssocs), 0},
		{"event integration gone", eiOp, map[string]error{"orders": gone}, "", appintSorted(appintAppAssocs, appintDIAssocs), 0},
		{"event integration denied", eiOp, map[string]error{"orders": appintDenied}, "", appintSorted(appintAppAssocs, appintDIAssocs), 1},
		{"event integration throttled", eiOp, map[string]error{"orders": appintThrottle}, "", appintSorted(appintAppAssocs, appintDIAssocs), 1},
		{"event integration other error", eiOp, map[string]error{"orders": appintBadInput}, "InvalidRequestException", appintSorted(appintAppAssocs, appintDIAssocs), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, label[tc.op])
			var calls appintCalls
			h := appintHandlers(&calls)
			next := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if err, ok := tc.fail[appintParentKey(in)]; ok {
					return nil, err
				}
				return next(in)
			}
			_, err := scanAppIntegrationsTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
				if isTransientNetworkError(err) {
					t.Errorf("err = %v reads as transient, want a hard error", err)
				}
			}
			if got := appintAssocIDs(t, st); !slices.Equal(got, tc.wantRows) {
				t.Errorf("association rows = %v, want %v", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if n := len(rowsByNativeID(t, st, TypeAppIntegrationsEventIntegration)); n != 2 {
				t.Errorf("event integration rows = %d, want 2; an association phase must not cost the parents rows", n)
			}
		})
	}
}

// A hard error in a parent phase is returned, and the association phases still
// run over the parents stored before it; the phases after the failing one do
// not run, as before associations existed.
func TestScanAppIntegrations_ParentPhaseErrorStillRunsAssociations(t *testing.T) {
	st := newTestStore(t)
	var calls appintCalls
	h := appintHandlers(&calls)
	h["ListDataIntegrations"] = func(any) (any, error) { return nil, appintBadInput }
	total, err := scanAppIntegrationsTopLevel(t, st, h)
	assertAPICode(t, err, "InvalidRequestException")
	if got := appintAssocIDs(t, st); !slices.Equal(got, appintAppAssocs) {
		t.Errorf("association rows = %v, want the applications' %v", got, appintAppAssocs)
	}
	if got := calls.eventIntegrations.distinct(); len(got) != 0 {
		t.Errorf("ListEventIntegrationAssociations parents = %v, want none: the event-integration phase did not run", got)
	}
	// 2 applications + 3 application associations
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
}

// A transient error in a parent phase is demoted to a warning so that an
// association phase's hard error still reaches the dispatcher as a hard error.
func TestScanAppIntegrations_ParentTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "appintegrations")
	var calls appintCalls
	h := appintHandlers(&calls)
	h["ListEventIntegrations"] = func(any) (any, error) { return nil, appintThrottle }
	h["ListApplicationAssociations"] = func(any) (any, error) { return nil, appintBadInput }
	_, err := scanAppIntegrationsTopLevel(t, st, h)
	assertAPICode(t, err, "InvalidRequestException")
	if isTransientNetworkError(err) {
		t.Errorf("err = %v reads as transient; the association phase's hard error must not be masked", err)
	}
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
	if got := appintAssocIDs(t, st); !slices.Equal(got, appintDIAssocs) {
		t.Errorf("association rows = %v, want the data integration's %v", got, appintDIAssocs)
	}
}

// Every listing stops when the service repeats the token it was sent, rather
// than looping on it.
func TestScanAppIntegrations_RepeatedTokenStops(t *testing.T) {
	token := sdkaws.String("same")
	for op, out := range map[string]any{
		"ListApplications":                 &appintegrations.ListApplicationsOutput{NextToken: token},
		"ListDataIntegrations":             &appintegrations.ListDataIntegrationsOutput{NextToken: token},
		"ListEventIntegrations":            &appintegrations.ListEventIntegrationsOutput{NextToken: token},
		"ListApplicationAssociations":      &appintegrations.ListApplicationAssociationsOutput{NextToken: token},
		"ListDataIntegrationAssociations":  &appintegrations.ListDataIntegrationAssociationsOutput{NextToken: token},
		"ListEventIntegrationAssociations": &appintegrations.ListEventIntegrationAssociationsOutput{NextToken: token},
	} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var calls appintCalls
			h := appintHandlers(&calls)
			var parents parentCalls
			var count atomic.Int32
			h[op] = func(in any) (any, error) {
				parents.add(appintParentKey(in))
				if count.Add(1) > 10 {
					return nil, apiErr("LoopGuard", "the repeated token was not detected")
				}
				return out, nil
			}
			if _, err := scanAppIntegrationsTopLevel(t, st, h); err != nil {
				t.Fatalf("scanAppIntegrations: %v", err)
			}
			// Per parent (one for a parent list op): the first call, then one
			// repeat of "same".
			if want := int32(2 * len(parents.distinct())); count.Load() != want {
				t.Errorf("%s calls = %d, want %d", op, count.Load(), want)
			}
		})
	}
}

// A data integration whose ARN has no data-integration/<id> segment is stored,
// its associations are not listed, and one warning says so; the others are
// listed by id.
func TestScanAppIntegrations_DataIntegrationARNWithoutID(t *testing.T) {
	const odd = appintTestPrefix + "data-integration-legacy"
	st := newTestStore(t)
	warns := countOpWarnings(st, appIntegrationsListDataIntegrationAssociationsOp)
	var calls appintCalls
	h := appintHandlers(&calls)
	h["ListDataIntegrations"] = func(any) (any, error) {
		return &appintegrations.ListDataIntegrationsOutput{DataIntegrations: []appintegrationstypes.DataIntegrationSummary{
			{Arn: sdkaws.String(odd), Name: sdkaws.String("odd")},
			{Arn: sdkaws.String(appintTestDI), Name: sdkaws.String("crm")},
			{Arn: sdkaws.String(appintTestPrefix + "data-integration/"), Name: sdkaws.String("empty id")},
		}}, nil
	}
	if _, err := scanAppIntegrationsTopLevel(t, st, h); err != nil {
		t.Fatalf("scanAppIntegrations: %v", err)
	}
	if got := calls.dataIntegrations.distinct(); !slices.Equal(got, []string{appintTestDIID}) {
		t.Errorf("ListDataIntegrationAssociations parents = %v, want only %s", got, appintTestDIID)
	}
	if n := len(rowsByNativeID(t, st, TypeAppIntegrationsDataIntegration)); n != 3 {
		t.Errorf("data integration rows = %d, want 3", n)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 covering both id-less ARNs", *warns)
	}
}

func TestDataIntegrationIDFromARN(t *testing.T) {
	for _, tc := range []struct {
		arn, wantID string
		wantOK      bool
	}{
		{appintTestDI, appintTestDIID, true},
		{appintTestPrefix + "data-integration/", "", false},
		{appintTestPrefix + "data-integration-legacy", "", false},
		{"", "", false},
	} {
		if id, ok := dataIntegrationIDFromARN(tc.arn); id != tc.wantID || ok != tc.wantOK {
			t.Errorf("dataIntegrationIDFromARN(%q) = %q, %v; want %q, %v", tc.arn, id, ok, tc.wantID, tc.wantOK)
		}
	}
}
