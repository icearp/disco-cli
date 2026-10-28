package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/guardduty"
	gdtypes "github.com/aws/aws-sdk-go-v2/service/guardduty/types"
	"github.com/icearp/disco-cli/store"
)

var gdTestCreated = time.Date(2026, 9, 2, 3, 4, 5, 0, time.UTC)

func gdTestAssocARN(ruleID, assocID string) string {
	return "arn:aws:guardduty:" + testRegion + ":" + testAccountID + ":detection-rule/custom/" + ruleID + "/association/" + assocID
}

func gdTestAssoc(ruleID, assocID string) gdtypes.AssociationSummary {
	return gdtypes.AssociationSummary{
		Arn: sdkaws.String(gdTestAssocARN(ruleID, assocID)), AssociationId: sdkaws.String(assocID),
		RuleId: sdkaws.String(ruleID), Mode: gdtypes.AssociationModeLive, CreatedAt: &gdTestCreated,
	}
}

func gdTestOrgConfig(ruleID string, mode gdtypes.AssociationMode) gdtypes.DetectionRuleOrgConfigurationSummary {
	return gdtypes.DetectionRuleOrgConfigurationSummary{
		RuleId: sdkaws.String(ruleID), Mode: mode, Status: gdtypes.DetectionRuleConfigurationStatusActive, CreatedAt: &gdTestCreated,
	}
}

// gdRulePhase is one custom-detection-rule list op, described so one table
// drives both phases: page builds the op's output from its NextToken input,
// with one listable item named after the page and NextToken "p2" on page 1.
type gdRulePhase struct {
	op     string // op name as the stub sees it
	label  string // the op label warnings name
	rtype  string
	scan   func(context.Context, guardDutyExtAPI, *account, string, *store.Store, string) (int, int, error)
	page   func(token string) any
	empty  func() any
	native func(item string) string
	token  func(in any) string
	limit  func(in any) int32
}

var (
	gdAssocPhase = gdRulePhase{
		op: "ListCustomDetectionRuleAssociations", label: gdListCustomRuleAssocsOp, rtype: TypeGuardDutyCustomDetectionRuleAssociation, scan: gdScanAssocs,
		page: func(next string) any {
			return &guardduty.ListCustomDetectionRuleAssociationsOutput{RuleAssociations: []gdtypes.AssociationSummary{gdTestAssoc("r-1", "a-"+pageName(next))}, NextToken: nextOf(next)}
		},
		empty:  func() any { return &guardduty.ListCustomDetectionRuleAssociationsOutput{} },
		native: func(item string) string { return gdTestAssocARN("r-1", "a-"+item) },
		token: func(in any) string {
			return sdkaws.ToString(in.(*guardduty.ListCustomDetectionRuleAssociationsInput).NextToken)
		},
		limit: func(in any) int32 {
			return sdkaws.ToInt32(in.(*guardduty.ListCustomDetectionRuleAssociationsInput).MaxResults)
		},
	}
	gdOrgPhase = gdRulePhase{
		op: "ListCustomDetectionRuleOrgConfigurations", label: gdListCustomRuleOrgOp, rtype: TypeGuardDutyCustomDetectionRuleOrgConfiguration, scan: scanGDCustomRuleOrgConfigurations,
		page: func(next string) any {
			return &guardduty.ListCustomDetectionRuleOrgConfigurationsOutput{
				Configurations: []gdtypes.DetectionRuleOrgConfigurationSummary{gdTestOrgConfig("r-"+pageName(next), gdtypes.AssociationModeDryRun)},
				NextToken:      nextOf(next),
			}
		},
		empty: func() any { return &guardduty.ListCustomDetectionRuleOrgConfigurationsOutput{} },
		native: func(item string) string {
			return gdRuleOrgConfigNativeID("aws", testRegion, testAccountID, "r-"+item, gdtypes.AssociationModeDryRun)
		},
		token: func(in any) string {
			return sdkaws.ToString(in.(*guardduty.ListCustomDetectionRuleOrgConfigurationsInput).NextToken)
		},
		limit: func(in any) int32 {
			return sdkaws.ToInt32(in.(*guardduty.ListCustomDetectionRuleOrgConfigurationsInput).MaxResults)
		},
	}
	gdRulePhases = []gdRulePhase{gdAssocPhase, gdOrgPhase}
)

// gdScanAssocs runs the association phase alone and returns its counts and
// errors as the service folds them.
func gdScanAssocs(ctx context.Context, client guardDutyExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var p phaseTotals
	scanGDCustomRuleAssociations(ctx, client, acct, region, st, scanID, &p)
	return p.result(st, acct, region)
}

// gdNoAssocTags answers GetCustomDetectionRuleAssociation without tags.
func gdNoAssocTags(any) (any, error) {
	return &guardduty.GetCustomDetectionRuleAssociationOutput{}, nil
}

// pageName names the item a page carries: "1" on the first page, "2" after.
func pageName(token string) string {
	if token == "" {
		return "1"
	}
	return "2"
}

func nextOf(token string) *string {
	if token == "" {
		return sdkaws.String("p2")
	}
	return nil
}

// gdPhaseHandler serves p's two pages, failing the page whose token is failToken
// with failErr (no failure when failErr is nil); repeat makes every page answer
// NextToken "same", the second page's item still named "2". It records each
// call's token and MaxResults, and fails the call past maxCalls so a paginator
// that loops ends instead of hanging.
type gdPhaseHandler struct {
	p         gdRulePhase
	failToken string
	failErr   error
	repeat    bool
	maxCalls  int
	mu        sync.Mutex
	tokens    []string
	limits    []int32
}

func (h *gdPhaseHandler) serve(in any) (any, error) {
	token := h.p.token(in)
	h.mu.Lock()
	h.tokens = append(h.tokens, token)
	h.limits = append(h.limits, h.p.limit(in))
	n := len(h.tokens)
	h.mu.Unlock()
	if n > h.maxCalls {
		return nil, errors.New("stub call cap exceeded: the paginator did not stop")
	}
	if h.failErr != nil && token == h.failToken {
		return nil, h.failErr
	}
	out := h.p.page(token)
	if !h.repeat {
		return out, nil
	}
	switch o := out.(type) {
	case *guardduty.ListCustomDetectionRuleAssociationsOutput:
		o.NextToken = sdkaws.String("same")
	case *guardduty.ListCustomDetectionRuleOrgConfigurationsOutput:
		o.NextToken = sdkaws.String("same")
	}
	return out, nil
}

func runGDPhase(t *testing.T, st *store.Store, h *gdPhaseHandler) (int, error) {
	t.Helper()
	client := guardduty.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
		h.p.op: h.serve, "GetCustomDetectionRuleAssociation": gdNoAssocTags,
	}, apiErr("AccessDeniedException", "unstubbed op")))
	total, _, err := h.p.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

func TestGDCustomRulePhases_TwoPagesStored(t *testing.T) {
	for _, p := range gdRulePhases {
		t.Run(p.op, func(t *testing.T) {
			st := newTestStore(t)
			h := &gdPhaseHandler{p: p, maxCalls: 2}
			total, err := runGDPhase(t, st, h)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 2 {
				t.Errorf("total = %d; want 2", total)
			}
			if want := []string{"", "p2"}; !slices.Equal(h.tokens, want) {
				t.Errorf("tokens = %v; want %v", h.tokens, want)
			}
			if want := []int32{100, 100}; !slices.Equal(h.limits, want) {
				t.Errorf("MaxResults = %v; want %v (the model cap)", h.limits, want)
			}
			rows := rowsByNativeID(t, st, p.rtype)
			if len(rows) != 2 {
				t.Fatalf("stored %d %s rows; want 2: %v", len(rows), p.rtype, rows)
			}
			for _, item := range []string{"1", "2"} {
				r, ok := rows[p.native(item)]
				if !ok {
					t.Fatalf("no row %s in %v", p.native(item), rows)
				}
				if r.Type != p.rtype || sv(r.Region) != testRegion || r.ManagedByProvider || sv(r.CreatedAt) != "2026-09-02T03:04:05Z" {
					t.Errorf("%s: type %q, region %q, managed %v, createdAt %q; want %q, %q, false, 2026-09-02T03:04:05Z",
						r.NativeID, r.Type, sv(r.Region), r.ManagedByProvider, sv(r.CreatedAt), p.rtype, testRegion)
				}
			}
		})
	}
}

func TestScanGDCustomRuleAssociations_StoresRow(t *testing.T) {
	st := newTestStore(t)
	if _, err := runGDPhase(t, st, &gdPhaseHandler{p: gdAssocPhase, maxCalls: 2}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	r := rowsByNativeID(t, st, TypeGuardDutyCustomDetectionRuleAssociation)[gdTestAssocARN("r-1", "a-1")]
	if sv(r.Name) != "a-1" {
		t.Errorf("name %q; want a-1", sv(r.Name))
	}
	var got gdtypes.AssociationSummary
	if err := json.Unmarshal([]byte(r.AttributesJSON), &got); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if got.Mode != gdtypes.AssociationModeLive || sdkaws.ToString(got.RuleId) != "r-1" {
		t.Errorf("attrs Mode %q, RuleId %q; want LIVE, r-1", got.Mode, sdkaws.ToString(got.RuleId))
	}
}

// Both modes of one rule are separate configurations (Get/Update/Delete key
// on RuleId and Mode), so each gets its own row.
func TestScanGDCustomRuleOrgConfigurations_KeysOnRuleAndMode(t *testing.T) {
	st := newTestStore(t)
	h := map[string]func(any) (any, error){
		"ListCustomDetectionRuleOrgConfigurations": func(any) (any, error) {
			return &guardduty.ListCustomDetectionRuleOrgConfigurationsOutput{Configurations: []gdtypes.DetectionRuleOrgConfigurationSummary{
				gdTestOrgConfig("r-1", gdtypes.AssociationModeLive), gdTestOrgConfig("r-1", gdtypes.AssociationModeDryRun),
			}}, nil
		},
	}
	client := guardduty.NewFromConfig(opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op")))
	if _, _, err := scanGDCustomRuleOrgConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeGuardDutyCustomDetectionRuleOrgConfiguration)
	want := map[string]bool{
		"arn:aws:guardduty:" + testRegion + ":" + testAccountID + ":detection-rule/custom/r-1/org-configuration/LIVE":    true,
		"arn:aws:guardduty:" + testRegion + ":" + testAccountID + ":detection-rule/custom/r-1/org-configuration/DRY_RUN": true,
	}
	if len(rows) != len(want) {
		t.Fatalf("stored %d rows; want %d: %v", len(rows), len(want), rows)
	}
	for id, r := range rows {
		if !want[id] {
			t.Errorf("unexpected row %s", id)
		}
		if sv(r.Status) != "ACTIVE" || sv(r.Name) != "r-1" {
			t.Errorf("%s: status %q, name %q; want ACTIVE, r-1", id, sv(r.Status), sv(r.Name))
		}
	}
}

// An empty listing stores nothing and warns nothing; items without the id
// their row needs are not stored and are warned about once per listing.
func TestGDCustomRulePhases_EmptyAndIDlessItems(t *testing.T) {
	idless := map[string]any{
		gdAssocPhase.op: &guardduty.ListCustomDetectionRuleAssociationsOutput{
			RuleAssociations: []gdtypes.AssociationSummary{{AssociationId: sdkaws.String("no-arn")}},
		},
		gdOrgPhase.op: &guardduty.ListCustomDetectionRuleOrgConfigurationsOutput{
			Configurations: []gdtypes.DetectionRuleOrgConfigurationSummary{{Mode: gdtypes.AssociationModeLive}, {RuleId: sdkaws.String("no-mode")}},
		},
	}
	for _, p := range gdRulePhases {
		for _, tc := range []struct {
			name  string
			out   any
			warns int
		}{{"empty", p.empty(), 0}, {"id-less", idless[p.op], 1}} {
			t.Run(p.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, p.label)
				client := guardduty.NewFromConfig(opStubConfig(map[string]func(any) (any, error){p.op: func(any) (any, error) { return tc.out, nil }}, apiErr("AccessDeniedException", "unstubbed op")))
				total, _, err := p.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
				if err != nil || total != 0 {
					t.Errorf("got (%d, %v); want (0, nil)", total, err)
				}
				if n := len(rowsByNativeID(t, st, p.rtype)); n != 0 {
					t.Errorf("stored %d rows; want 0", n)
				}
				if *warns != tc.warns {
					t.Errorf("%s warnings = %d; want %d", p.label, *warns, tc.warns)
				}
			})
		}
	}
}

// A page that repeats the token it was asked with ends the listing as an
// error instead of looping, keeping the pages read.
func TestGDCustomRulePhases_RepeatedTokenStops(t *testing.T) {
	for _, p := range gdRulePhases {
		t.Run(p.op, func(t *testing.T) {
			st := newTestStore(t)
			h := &gdPhaseHandler{p: p, repeat: true, maxCalls: 3}
			total, err := runGDPhase(t, st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if want := []string{"", "same"}; !slices.Equal(h.tokens, want) {
				t.Errorf("tokens = %v; want %v", h.tokens, want)
			}
			if n := len(rowsByNativeID(t, st, p.rtype)); total != 2 || n != 2 {
				t.Errorf("total = %d, stored = %d; want 2, 2", total, n)
			}
		})
	}
}

func TestGDCustomRulePhases_ErrorLadder(t *testing.T) {
	cases := []struct {
		name      string
		token     string // page that fails: "" first, "p2" second
		err       error
		wantCode  string
		wantRows  int
		wantWarns int
	}{
		{name: "first-page denial warns", err: apiErr("AccessDeniedException", "synthetic-test-deny"), wantWarns: 1},
		{name: "first-page op not deployed skips silently", err: apiErr("UnknownOperationException", "synthetic-test-unknown-op")},
		{name: "first-page bad request propagates", err: apiErr("BadRequestException", "synthetic-test-400"), wantCode: "BadRequestException"},
		{name: "first-page other error propagates", err: apiErr("InternalServerErrorException", "synthetic-test-500"), wantCode: "InternalServerErrorException"},
		{name: "later-page denial warns and keeps rows", token: "p2", err: apiErr("AccessDeniedException", "synthetic-test-deny"), wantRows: 1, wantWarns: 1},
		{name: "later-page op not deployed propagates and keeps rows", token: "p2", err: apiErr("UnknownOperationException", "synthetic-test-unknown-op"), wantCode: "UnknownOperationException", wantRows: 1},
		{name: "later-page bad request propagates and keeps rows", token: "p2", err: apiErr("BadRequestException", "synthetic-test-400"), wantCode: "BadRequestException", wantRows: 1},
	}
	for _, p := range gdRulePhases {
		for _, tc := range cases {
			t.Run(p.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, p.label)
				_, err := runGDPhase(t, st, &gdPhaseHandler{p: p, failToken: tc.token, failErr: tc.err, maxCalls: 2})
				if tc.wantCode != "" {
					if !isAPIErrorCode(err, tc.wantCode) {
						t.Fatalf("err = %v; want the %s propagated", err, tc.wantCode)
					}
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if n := len(rowsByNativeID(t, st, p.rtype)); n != tc.wantRows {
					t.Errorf("stored %d rows; want %d", n, tc.wantRows)
				}
				if *warns != tc.wantWarns {
					t.Errorf("recorded %d %s warnings; want %d", *warns, p.label, tc.wantWarns)
				}
			})
		}
	}
}

// gdTopLevelHandlers serves every op scanGuardDuty calls for one detector "d"
// with one publishing destination and one row per custom-detection-rule op.
func gdTopLevelHandlers() map[string]func(any) (any, error) {
	one := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	h := map[string]func(any) (any, error){
		"ListDetectors":              one(&guardduty.ListDetectorsOutput{DetectorIds: []string{"d"}}),
		"GetDetector":                one(&guardduty.GetDetectorOutput{Status: gdtypes.DetectorStatusEnabled}),
		"ListFilters":                one(&guardduty.ListFiltersOutput{}),
		"ListMembers":                one(&guardduty.ListMembersOutput{}),
		"ListIPSets":                 one(&guardduty.ListIPSetsOutput{}),
		"ListMalwareProtectionPlans": one(&guardduty.ListMalwareProtectionPlansOutput{}),
		"ListPublishingDestinations": one(&guardduty.ListPublishingDestinationsOutput{Destinations: []gdtypes.Destination{
			{DestinationId: sdkaws.String("pd"), DestinationType: gdtypes.DestinationTypeS3},
		}}),
		"ListThreatEntitySets":  one(&guardduty.ListThreatEntitySetsOutput{}),
		"ListThreatIntelSets":   one(&guardduty.ListThreatIntelSetsOutput{}),
		"ListTrustedEntitySets": one(&guardduty.ListTrustedEntitySetsOutput{}),
		"DescribeOrganizationConfiguration": one(&guardduty.DescribeOrganizationConfigurationOutput{
			AutoEnableOrganizationMembers: gdtypes.AutoEnableMembersAll,
		}),
		"ListTagsForResource":               one(&guardduty.ListTagsForResourceOutput{}),
		"GetCustomDetectionRuleAssociation": gdNoAssocTags,
	}
	for _, p := range gdRulePhases {
		h[p.op] = func(any) (any, error) { return p.page("p2"), nil }
	}
	return h
}

func scanGDTopLevel(t *testing.T, st *store.Store, h map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, apiErr("AccessDeniedException", "unstubbed op"))
	total, _, err := scanGuardDuty(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

// recordGDWarnings collects every warning's op label.
func recordGDWarnings(st *store.Store) func() []string {
	var mu sync.Mutex
	var warned []string
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		warned = append(warned, w.Service)
		mu.Unlock()
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(warned)
	}
}

// countGDCalls wraps h[op] to count its calls.
func countGDCalls(h map[string]func(any) (any, error), op string) *int {
	var mu sync.Mutex
	n := 0
	inner := h[op]
	h[op] = func(in any) (any, error) {
		mu.Lock()
		n++
		mu.Unlock()
		return inner(in)
	}
	return &n
}

// The whole service stores both custom-detection-rule types when the region
// has a detector; total counts them with the older rows.
func TestScanGuardDuty_StoresCustomDetectionRuleRows(t *testing.T) {
	st := newTestStore(t)
	total, err := scanGDTopLevel(t, st, gdTopLevelHandlers())
	if err != nil {
		t.Fatalf("scanGuardDuty: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d; want 5 (detector, publishing destination, organization configuration, association, rule org configuration)", total)
	}
	for _, p := range gdRulePhases {
		if _, ok := rowsByNativeID(t, st, p.rtype)[p.native("2")]; !ok {
			t.Errorf("no %s row %s", p.rtype, p.native("2"))
		}
	}
}

func TestScanGuardDuty_NoDetectorSkipsCustomDetectionRuleOps(t *testing.T) {
	st := newTestStore(t)
	h := gdTopLevelHandlers()
	h["ListDetectors"] = func(any) (any, error) { return &guardduty.ListDetectorsOutput{}, nil }
	calls := map[string]*int{}
	for _, p := range gdRulePhases {
		calls[p.op] = countGDCalls(h, p.op)
	}
	if _, err := scanGDTopLevel(t, st, h); err != nil {
		t.Fatalf("scanGuardDuty: %v", err)
	}
	for op, n := range calls {
		if *n != 0 {
			t.Errorf("%s called %d times with no detector; want 0", op, *n)
		}
	}
}

// A region whose organization configuration was refused is not the delegated
// administrator's, so the organization-level rule list is not called there and
// adds no warning of its own; associations are still listed.
func TestScanGuardDuty_OrgConfigRefusedSkipsRuleOrgConfigurations(t *testing.T) {
	st := newTestStore(t)
	warned := recordGDWarnings(st)
	h := gdTopLevelHandlers()
	h["DescribeOrganizationConfiguration"] = func(any) (any, error) {
		return nil, apiErr("BadRequestException", "synthetic-test-400")
	}
	orgCalls := countGDCalls(h, gdOrgPhase.op)
	total, err := scanGDTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanGuardDuty: %v; want nil", err)
	}
	if *orgCalls != 0 {
		t.Errorf("%s called %d times; want 0 in a refused region", gdOrgPhase.op, *orgCalls)
	}
	if want := []string{"guardduty:DescribeOrganizationConfiguration"}; !slices.Equal(warned(), want) {
		t.Errorf("warnings %v; want %v", warned(), want)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (detector, publishing destination, association)", total)
	}
	if _, ok := rowsByNativeID(t, st, gdAssocPhase.rtype)[gdAssocPhase.native("2")]; !ok {
		t.Errorf("association not stored")
	}
}

// In a region that served the organization configuration, a BadRequestException
// from the organization-level rule list is an ordinary hard error.
func TestScanGuardDuty_RuleOrgConfigurationsBadRequestPropagates(t *testing.T) {
	st := newTestStore(t)
	warned := recordGDWarnings(st)
	h := gdTopLevelHandlers()
	h[gdOrgPhase.op] = func(any) (any, error) { return nil, apiErr("BadRequestException", "synthetic-test-400") }
	_, err := scanGDTopLevel(t, st, h)
	if !isAPIErrorCode(err, "BadRequestException") || classifyServiceError(err) != outcomeError {
		t.Fatalf("err = %v (outcome %v); want the BadRequestException as outcomeError", err, classifyServiceError(err))
	}
	if got := warned(); len(got) != 0 {
		t.Errorf("warnings %v; want none", got)
	}
}

// A 5xx on the older organization-configuration phase alone is returned as
// transient, so the scan is partial, and every other phase keeps its rows and
// counts.
func TestScanGuardDuty_OrgConfigTransientKeepsOtherCounts(t *testing.T) {
	st := newTestStore(t)
	warned := recordGDWarnings(st)
	h := gdTopLevelHandlers()
	h["DescribeOrganizationConfiguration"] = func(any) (any, error) {
		return nil, apiErr("InternalServerErrorException", "synthetic-test-500")
	}
	total, err := scanGDTopLevel(t, st, h)
	if got := classifyServiceError(err); got != outcomeTransient {
		t.Fatalf("classifyServiceError(%v) = %v; want outcomeTransient", err, got)
	}
	if got := warned(); len(got) != 0 {
		t.Errorf("warnings %v; want none", got)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4 (detector, publishing destination, association, rule org configuration)", total)
	}
	for _, rtype := range []string{TypeGuardDutyDetector, TypeGuardDutyPublishingDestination, gdAssocPhase.rtype, gdOrgPhase.rtype} {
		if n := len(rowsByNativeID(t, st, rtype)); n != 1 {
			t.Errorf("stored %d %s rows; want 1", n, rtype)
		}
	}
}

// Each new phase runs after every older one and on its own: a hard failure in
// one costs neither the older rows nor the other new phase's rows, and reaches
// the dispatcher as a hard error.
func TestScanGuardDuty_CustomDetectionRulePhaseFailureIsIsolated(t *testing.T) {
	for _, failed := range gdRulePhases {
		t.Run(failed.op, func(t *testing.T) {
			st := newTestStore(t)
			h := gdTopLevelHandlers()
			h[failed.op] = func(any) (any, error) { return nil, apiErr("ValidationException", "synthetic-test-hard") }
			total, err := scanGDTopLevel(t, st, h)
			if !isAPIErrorCode(err, "ValidationException") || classifyServiceError(err) != outcomeError {
				t.Fatalf("err = %v (outcome %v); want the %s ValidationException as outcomeError", err, classifyServiceError(err), failed.op)
			}
			if total != 4 {
				t.Errorf("total = %d; want 4", total)
			}
			for _, rtype := range []string{TypeGuardDutyDetector, TypeGuardDutyPublishingDestination, TypeGuardDutyOrganizationConfiguration} {
				if n := len(rowsByNativeID(t, st, rtype)); n != 1 {
					t.Errorf("stored %d %s rows; want 1", n, rtype)
				}
			}
			for _, p := range gdRulePhases {
				want := 1
				if p.op == failed.op {
					want = 0
				}
				if n := len(rowsByNativeID(t, st, p.rtype)); n != want {
					t.Errorf("stored %d %s rows; want %d", n, p.rtype, want)
				}
			}
		})
	}
}

// A throttle on either side is demoted to one warning, so the other side's
// hard error still reaches the dispatcher as a hard error.
func TestScanGuardDuty_CustomDetectionRuleTransientDoesNotMaskHardError(t *testing.T) {
	type side struct{ op, warnOp string }
	older := side{"DescribeOrganizationConfiguration", "guardduty:DescribeOrganizationConfiguration"}
	var pairs [][2]side
	for _, p := range gdRulePhases {
		newer := side{p.op, p.label}
		pairs = append(pairs, [2]side{older, newer}, [2]side{newer, older})
	}
	for _, pair := range pairs {
		transient, hard := pair[0], pair[1]
		t.Run(transient.op+" throttled, "+hard.op+" fails", func(t *testing.T) {
			st := newTestStore(t)
			warned := recordGDWarnings(st)
			h := gdTopLevelHandlers()
			h[transient.op] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
			h[hard.op] = func(any) (any, error) { return nil, apiErr("ValidationException", "synthetic-test-hard") }
			_, err := scanGDTopLevel(t, st, h)
			if !isAPIErrorCode(err, "ValidationException") || classifyServiceError(err) != outcomeError {
				t.Errorf("err = %v (outcome %v); want the %s error as outcomeError", err, classifyServiceError(err), hard.op)
			}
			if !slices.Equal(warned(), []string{transient.warnOp}) {
				t.Errorf("warnings = %q; want one for %s", warned(), transient.warnOp)
			}
		})
	}
}
