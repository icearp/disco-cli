package aws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/xray"
	xraytypes "github.com/aws/aws-sdk-go-v2/service/xray/types"
	"github.com/icearp/disco-cli/store"
)

const xrayTestGroupARN = "arn:aws:xray:us-east-1:123456789012:group/team/ABC"

var (
	xrayTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: xray:Get")
	errXRayTest    = errors.New("xray test failure")
	xrayTestRuleA  = xrayIndexingRuleNativeID(testRegion, testAccountID, "Default")
	xrayTestRuleB  = xrayIndexingRuleNativeID(testRegion, testAccountID, "rule-b")
)

func xrayTestRule(name *string, desired float64) xraytypes.IndexingRule {
	return xraytypes.IndexingRule{
		Name:       name,
		ModifiedAt: sdkaws.Time(time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)),
		Rule: &xraytypes.IndexingRuleValueMemberProbabilistic{Value: xraytypes.ProbabilisticRuleValue{
			DesiredSamplingPercentage: sdkaws.Float64(desired),
		}},
	}
}

// xrayIndexingRulePages serves GetIndexingRules over two pages: "Default";
// then "rule-b" and an element with no name.
func xrayIndexingRulePages(in any) (any, error) {
	if sv(in.(*xray.GetIndexingRulesInput).NextToken) == "" {
		return &xray.GetIndexingRulesOutput{
			IndexingRules: []xraytypes.IndexingRule{xrayTestRule(sdkaws.String("Default"), 1)},
			NextToken:     sdkaws.String("p2"),
		}, nil
	}
	return &xray.GetIndexingRulesOutput{IndexingRules: []xraytypes.IndexingRule{
		xrayTestRule(sdkaws.String("rule-b"), 25),
		xrayTestRule(nil, 50),
	}}, nil
}

// xrayHandlers answers every X-Ray op the scan makes: one group, and the two
// indexing-rule pages.
func xrayHandlers() map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"GetGroups": func(any) (any, error) {
			return &xray.GetGroupsOutput{Groups: []xraytypes.GroupSummary{
				{GroupARN: sdkaws.String(xrayTestGroupARN), GroupName: sdkaws.String("team")},
			}}, nil
		},
		"ListResourcePolicies": func(any) (any, error) { return &xray.ListResourcePoliciesOutput{}, nil },
		"GetSamplingRules":     func(any) (any, error) { return &xray.GetSamplingRulesOutput{}, nil },
		"GetTraceSegmentDestination": func(any) (any, error) {
			return &xray.GetTraceSegmentDestinationOutput{Status: xraytypes.TraceSegmentDestinationStatusActive}, nil
		},
		"GetIndexingRules": xrayIndexingRulePages,
	}
}

func scanXRayIndexingRulesWith(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	client := xray.NewFromConfig(opStubConfig(handlers, xrayTestDenied))
	total, _, err := scanXRayIndexingRules(context.Background(), client, acct, testRegion, st, testScanID)
	return total, err
}

func scanXRayTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, xrayTestDenied)
	total, _, err := scanXRay(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanXRayIndexingRules_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	total, err := scanXRayIndexingRulesWith(t, st, map[string]func(in any) (any, error){"GetIndexingRules": xrayIndexingRulePages})
	if err != nil {
		t.Fatalf("scanXRayIndexingRules: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	rows := rowsByNativeID(t, st, TypeXRayIndexingRule)
	if got, want := nativeIDs(rows), []string{xrayTestRuleA, xrayTestRuleB}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	if xrayTestRuleA != "arn:aws:xray:us-east-1:123456789012:indexing-rule/Default" {
		t.Errorf("NativeID = %q", xrayTestRuleA)
	}
	r := rows[xrayTestRuleA]
	if r.Type != TypeXRayIndexingRule || sv(r.Name) != "Default" || sv(r.Region) != testRegion || r.ManagedByProvider {
		t.Errorf("row = type %q name %q region %q managed %v", r.Type, sv(r.Name), sv(r.Region), r.ManagedByProvider)
	}
	attrs := xrayRuleAttrs(t, rows[xrayTestRuleB])
	if attrs.Rule == nil || attrs.Rule.Probabilistic == nil || attrs.Rule.Probabilistic.DesiredSamplingPercentage != 25 || attrs.Rule.Value != nil ||
		!attrs.ModifiedAt.Equal(time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("attrs = %s, want the rule under Probabilistic and no unlabelled Value", rows[xrayTestRuleB].AttributesJSON)
	}
}

type xrayTestRuleAttrs struct {
	ModifiedAt time.Time
	Rule       *struct {
		Probabilistic *struct{ DesiredSamplingPercentage float64 }
		UnknownMember *struct{ Tag string }
		Value         json.RawMessage
	}
}

func xrayRuleAttrs(t *testing.T, r store.Resource) xrayTestRuleAttrs {
	t.Helper()
	var attrs xrayTestRuleAttrs
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	return attrs
}

// A rule kind newer than the pinned SDK is stored under UnknownMember with its tag.
func TestScanXRayIndexingRules_UnknownRuleKind(t *testing.T) {
	st := newTestStore(t)
	_, err := scanXRayIndexingRulesWith(t, st, map[string]func(in any) (any, error){
		"GetIndexingRules": func(any) (any, error) {
			return &xray.GetIndexingRulesOutput{IndexingRules: []xraytypes.IndexingRule{{
				Name: sdkaws.String("Default"),
				Rule: &xraytypes.UnknownUnionMember{Tag: "Future", Value: []byte(`{}`)},
			}}}, nil
		},
	})
	if err != nil {
		t.Fatalf("scanXRayIndexingRules: %v", err)
	}
	r := rowsByNativeID(t, st, TypeXRayIndexingRule)[xrayTestRuleA]
	if attrs := xrayRuleAttrs(t, r); attrs.Rule == nil || attrs.Rule.UnknownMember == nil || attrs.Rule.UnknownMember.Tag != "Future" || attrs.Rule.Probabilistic != nil {
		t.Errorf("attrs = %s, want UnknownMember tagged Future", r.AttributesJSON)
	}
}

// A rule the API returns without a Rule value is stored with Rule null, not {}.
func TestScanXRayIndexingRules_NoRuleValue(t *testing.T) {
	st := newTestStore(t)
	_, err := scanXRayIndexingRulesWith(t, st, map[string]func(in any) (any, error){
		"GetIndexingRules": func(any) (any, error) {
			return &xray.GetIndexingRulesOutput{IndexingRules: []xraytypes.IndexingRule{{Name: sdkaws.String("Default")}}}, nil
		},
	})
	if err != nil {
		t.Fatalf("scanXRayIndexingRules: %v", err)
	}
	r := rowsByNativeID(t, st, TypeXRayIndexingRule)[xrayTestRuleA]
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	if string(m["Rule"]) != "null" {
		t.Errorf("Rule = %s, want null", m["Rule"])
	}
}

func TestScanXRayIndexingRules_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanXRayIndexingRulesWith(t, st, map[string]func(in any) (any, error){
		"GetIndexingRules": func(any) (any, error) { return &xray.GetIndexingRulesOutput{}, nil },
	})
	if err != nil || total != 0 {
		t.Errorf("total = %d, err = %v; want 0, nil", total, err)
	}
}

// A repeated token fails the listing; both pages' rules are stored.
func TestScanXRayIndexingRules_RepeatedTokenFails(t *testing.T) {
	st := newTestStore(t)
	calls := 0
	total, err := scanXRayIndexingRulesWith(t, st, map[string]func(in any) (any, error){
		"GetIndexingRules": func(any) (any, error) {
			calls++
			if calls > 3 {
				return nil, errXRayTest
			}
			name := map[int]string{1: "Default", 2: "rule-b", 3: "rule-c"}[calls]
			return &xray.GetIndexingRulesOutput{
				IndexingRules: []xraytypes.IndexingRule{xrayTestRule(sdkaws.String(name), 1)},
				NextToken:     sdkaws.String("same"),
			}, nil
		},
	})
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if calls != 2 || total != 2 {
		t.Errorf("calls = %d, total = %d; want 2, 2", calls, total)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeXRayIndexingRule)); !slices.Equal(got, []string{xrayTestRuleA, xrayTestRuleB}) {
		t.Errorf("rows = %v, want %v", got, []string{xrayTestRuleA, xrayTestRuleB})
	}
}

// A denial warns once; one after a page keeps the rules already listed.
func TestScanXRayIndexingRules_AccessDenied(t *testing.T) {
	cases := []struct {
		name string
		deny func(in *xray.GetIndexingRulesInput) bool
		want []string
	}{
		{"first page", func(*xray.GetIndexingRulesInput) bool { return true }, nil},
		{"second page", func(in *xray.GetIndexingRulesInput) bool { return in.NextToken != nil }, []string{xrayTestRuleA}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, xrayGetIndexingRulesOp)
			total, err := scanXRayIndexingRulesWith(t, st, map[string]func(in any) (any, error){
				"GetIndexingRules": func(in any) (any, error) {
					if c.deny(in.(*xray.GetIndexingRulesInput)) {
						return nil, xrayTestDenied
					}
					return xrayIndexingRulePages(in)
				},
			})
			if err != nil {
				t.Fatalf("scanXRayIndexingRules: %v", err)
			}
			if *warnings != 1 {
				t.Errorf("warnings = %d, want 1", *warnings)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeXRayIndexingRule)); !slices.Equal(got, c.want) || total != len(c.want) {
				t.Errorf("rows = %v, total = %d; want %v", got, total, c.want)
			}
		})
	}
}

func TestScanXRayIndexingRules_OtherErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	_, err := scanXRayIndexingRulesWith(t, st, map[string]func(in any) (any, error){
		"GetIndexingRules": func(any) (any, error) { return nil, apiErr("SomeOtherException", "bad") },
	})
	if !isAPIErrorCode(err, "SomeOtherException") {
		t.Errorf("err = %v, want the SomeOtherException", err)
	}
	if n := len(rowsByNativeID(t, st, TypeXRayIndexingRule)); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

// InvalidRequestException on the first page warns once and stores nothing.
func TestScanXRayIndexingRules_FirstPageInvalidRequestWarns(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, xrayGetIndexingRulesOp)
	total, err := scanXRayIndexingRulesWith(t, st, map[string]func(in any) (any, error){
		"GetIndexingRules": func(any) (any, error) { return nil, apiErr("InvalidRequestException", "bad") },
	})
	if err != nil || total != 0 {
		t.Fatalf("total = %d, err = %v; want 0, nil", total, err)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d, want 1", *warnings)
	}
}

// InvalidRequestException on a later page answers disco's echoed NextToken,
// so it fails the phase like any other mid-pagination error.
func TestScanXRayIndexingRules_LaterPageInvalidRequestFails(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, xrayGetIndexingRulesOp)
	_, err := scanXRayIndexingRulesWith(t, st, map[string]func(in any) (any, error){
		"GetIndexingRules": func(in any) (any, error) {
			if in.(*xray.GetIndexingRulesInput).NextToken != nil {
				return nil, apiErr("InvalidRequestException", "bad")
			}
			return xrayIndexingRulePages(in)
		},
	})
	if !isAPIErrorCode(err, "InvalidRequestException") {
		t.Errorf("err = %v, want the InvalidRequestException", err)
	}
	if *warnings != 0 {
		t.Errorf("warnings = %d, want 0", *warnings)
	}
	if n := len(rowsByNativeID(t, st, TypeXRayIndexingRule)); n != 0 {
		t.Errorf("rows = %d, want 0 (a failed phase stores nothing)", n)
	}
}

func TestScanXRay_StoresIndexingRules(t *testing.T) {
	st := newTestStore(t)
	total, err := scanXRayTopLevel(t, st, xrayHandlers())
	if err != nil {
		t.Fatalf("scanXRay: %v", err)
	}
	// 1 group + 1 transaction-search config + 2 indexing rules
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeXRayIndexingRule)); !slices.Equal(got, []string{xrayTestRuleA, xrayTestRuleB}) {
		t.Errorf("indexing rules = %v", got)
	}
}

// An older phase's failure does not skip the indexing-rule phase, and the
// indexing-rule phase's failure keeps the older phases' rows.
func TestScanXRay_PhasesIndependent(t *testing.T) {
	cases := []struct {
		failOp    string
		wantTotal int
		typ       string
		wantRows  int
	}{
		{"GetGroups", 2, TypeXRayIndexingRule, 2},
		{"GetIndexingRules", 2, TypeXRayGroup, 1},
	}
	for _, c := range cases {
		t.Run(c.failOp, func(t *testing.T) {
			st := newTestStore(t)
			h := xrayHandlers()
			h[c.failOp] = func(any) (any, error) { return nil, errXRayTest }
			total, err := scanXRayTopLevel(t, st, h)
			if !errors.Is(err, errXRayTest) {
				t.Fatalf("err = %v, want the %s error", err, c.failOp)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d, want %d", total, c.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != c.wantRows {
				t.Errorf("%s rows = %d, want %d", c.typ, n, c.wantRows)
			}
		})
	}
}

// A phase's transient error is demoted to one warning, so it cannot make the
// dispatcher read the other phase's hard error as transient.
func TestScanXRay_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	cases := []struct {
		name, transientOp, hardOp, warnOp string
	}{
		{"transient older phase", "GetGroups", "GetIndexingRules", "xray"},
		{"transient indexing-rule phase", "GetIndexingRules", "GetGroups", xrayGetIndexingRulesOp},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := xrayHandlers()
			h[c.transientOp] = func(any) (any, error) { return nil, io.EOF }
			h[c.hardOp] = func(any) (any, error) { return nil, errXRayTest }
			warnings := countOpWarnings(st, c.warnOp)
			_, err := scanXRayTopLevel(t, st, h)
			if !errors.Is(err, errXRayTest) {
				t.Fatalf("err = %v, want the %s error", err, c.hardOp)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.warnOp, *warnings)
			}
		})
	}
}
