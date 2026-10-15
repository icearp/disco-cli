package aws

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/connectcases"
	connectcasestypes "github.com/aws/aws-sdk-go-v2/service/connectcases/types"
	"github.com/icearp/disco-cli/store"
)

const (
	casesTestDomain   = "arn:aws:cases:us-east-1:123456789012:domain/d-1"
	casesTestFieldA   = casesTestDomain + "/field/fA"
	casesTestFieldB   = casesTestDomain + "/field/fB"
	casesTestFieldTxt = casesTestDomain + "/field/fT"
	casesTestOptA1    = casesTestFieldA + "/option/high"
	casesTestOptA2    = casesTestFieldA + "/option/tier/1"
	casesTestOptB1    = casesTestFieldB + "/option/low"
)

var (
	casesDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: cases:ListFieldOptions")
	casesBadInput = apiErr("ValidationException", "bad")
)

func casesField(id string, typ connectcasestypes.FieldType, ns connectcasestypes.FieldNamespace) connectcasestypes.FieldSummary {
	return connectcasestypes.FieldSummary{
		FieldArn:  sdkaws.String(casesTestDomain + "/field/" + id),
		FieldId:   sdkaws.String(id),
		Name:      sdkaws.String("field " + id),
		Type:      typ,
		Namespace: ns,
	}
}

func casesOption(value, name string, active bool) connectcasestypes.FieldOption {
	return connectcasestypes.FieldOption{Value: sdkaws.String(value), Name: sdkaws.String(name), Active: sdkaws.Bool(active)}
}

// casesHandlers answers every Connect Cases op the scan calls: one domain, no
// case rules, layouts or templates, two custom single-select fields, a custom text field and a system
// single-select field (the last two are never queried: their ListFieldOptions
// stub answers ValidationException),
// and options listed over two pages for field A (plus a valueless element to
// skip) and one page for field B. Each ListFieldOptions call is recorded in
// optionCalls as domainId/fieldId.
func casesHandlers(optionCalls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListDomains": func(any) (any, error) {
			return &connectcases.ListDomainsOutput{Domains: []connectcasestypes.DomainSummary{
				{DomainArn: sdkaws.String(casesTestDomain), DomainId: sdkaws.String("d-1"), Name: sdkaws.String("support")},
			}}, nil
		},
		"ListCaseRules": func(any) (any, error) { return &connectcases.ListCaseRulesOutput{}, nil },
		"ListFields": func(any) (any, error) {
			return &connectcases.ListFieldsOutput{Fields: []connectcasestypes.FieldSummary{
				casesField("fA", connectcasestypes.FieldTypeSingleSelect, connectcasestypes.FieldNamespaceCustom),
				casesField("fT", connectcasestypes.FieldTypeText, connectcasestypes.FieldNamespaceCustom),
				casesField("status", connectcasestypes.FieldTypeSingleSelect, connectcasestypes.FieldNamespaceSystem),
				casesField("fB", connectcasestypes.FieldTypeSingleSelect, connectcasestypes.FieldNamespaceCustom),
			}}, nil
		},
		"ListLayouts":   func(any) (any, error) { return &connectcases.ListLayoutsOutput{}, nil },
		"ListTemplates": func(any) (any, error) { return &connectcases.ListTemplatesOutput{}, nil },
		"ListFieldOptions": func(in any) (any, error) {
			p := in.(*connectcases.ListFieldOptionsInput)
			optionCalls.add(sdkaws.ToString(p.DomainId) + "/" + sdkaws.ToString(p.FieldId))
			if sdkaws.ToInt32(p.MaxResults) != casesFieldOptionPageLimit {
				return nil, apiErr("ValidationException", "maxResults must be at most 100")
			}
			switch sdkaws.ToString(p.FieldId) {
			case "fA":
				if p.NextToken == nil {
					return &connectcases.ListFieldOptionsOutput{
						Options:   []connectcasestypes.FieldOption{casesOption("high", "High", true), {Name: sdkaws.String("valueless")}},
						NextToken: sdkaws.String("page2"),
					}, nil
				}
				return &connectcases.ListFieldOptionsOutput{Options: []connectcasestypes.FieldOption{casesOption("tier/1", "Tier 1", false)}}, nil
			case "fB":
				return &connectcases.ListFieldOptionsOutput{Options: []connectcasestypes.FieldOption{casesOption("low", "Low", true)}}, nil
			}
			return nil, apiErr("ValidationException", "field has no options")
		},
	}
}

func scanCasesTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, casesDenied)
	total, _, err := scanCases(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanCases_StoresFieldOptions(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanCasesTopLevel(t, st, casesHandlers(&calls))
	if err != nil {
		t.Fatalf("scanCases: %v", err)
	}
	// 1 domain + 4 fields + 3 options
	if total != 8 {
		t.Errorf("total = %d, want 8", total)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"d-1/fA", "d-1/fB"}) {
		t.Errorf("ListFieldOptions parents = %v, want only the custom single-select fields", got)
	}
	rows := rowsByNativeID(t, st, TypeCasesFieldOption)
	if got := nativeIDs(rows); !slices.Equal(got, []string{casesTestOptA1, casesTestOptA2, casesTestOptB1}) {
		t.Fatalf("option rows = %v, want both pages of A and B's option", got)
	}
	r := rows[casesTestOptA2]
	if r.Type != TypeCasesFieldOption || sv(r.Region) != testRegion || sv(r.Name) != "Tier 1" {
		t.Errorf("row = type %q region %q name %q", r.Type, sv(r.Region), sv(r.Name))
	}
	var attrs struct {
		Value  string `json:"Value"`
		Active *bool  `json:"Active"`
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs.Value != "tier/1" || attrs.Active == nil || *attrs.Active {
		t.Errorf("attrs = %s, want the SDK option verbatim (Value tier/1, Active false)", r.AttributesJSON)
	}
	assertContainsNative(t, st, casesTestFieldA, casesTestOptA1)
	assertContainsNative(t, st, casesTestFieldA, casesTestOptA2)
	assertContainsNative(t, st, casesTestFieldB, casesTestOptB1)
	if _, ok := rowsByNativeID(t, st, TypeCasesField)[casesTestFieldTxt]; !ok {
		t.Error("text field row missing; only its option listing is skipped")
	}
}

func TestScanCases_FieldsWithoutOptions(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := casesHandlers(&calls)
	h["ListFieldOptions"] = func(in any) (any, error) {
		p := in.(*connectcases.ListFieldOptionsInput)
		calls.add(sdkaws.ToString(p.DomainId) + "/" + sdkaws.ToString(p.FieldId))
		return &connectcases.ListFieldOptionsOutput{}, nil
	}
	if _, err := scanCasesTopLevel(t, st, h); err != nil {
		t.Fatalf("scanCases: %v", err)
	}
	if n := len(rowsByNativeID(t, st, TypeCasesFieldOption)); n != 0 {
		t.Errorf("option rows = %d, want 0", n)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"d-1/fA", "d-1/fB"}) {
		t.Errorf("ListFieldOptions parents = %v, want both single-select fields", got)
	}
}

// A field deleted since listing, denied, or failing transiently after retries
// drops only its own options: deleted is silent, a denial warns once however
// many fields hit it, and a transient error warns once. Any other error fails
// the phase (childFanOut stores none of its rows) and is returned; the older
// phases' rows stay.
func TestScanCases_FailedFieldOptionParent(t *testing.T) {
	throttled := apiErr("ThrottlingException", "slow down")
	for _, tc := range []struct {
		name      string
		fail      map[string]error
		wantCode  string // "" means the scan returns nil
		wantRows  []string
		wantWarns int
	}{
		{"field gone", map[string]error{"fA": apiErr("ResourceNotFoundException", "gone")}, "", []string{casesTestOptB1}, 0},
		{"denied everywhere", map[string]error{"fA": casesDenied, "fB": casesDenied}, "", nil, 1},
		{"throttled everywhere", map[string]error{"fA": throttled, "fB": throttled}, "", nil, 1},
		{"throttled once", map[string]error{"fA": throttled}, "", []string{casesTestOptB1}, 1},
		{"other error", map[string]error{"fA": casesBadInput}, "ValidationException", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, casesListFieldOptionsOp)
			var calls parentCalls
			h := casesHandlers(&calls)
			next := h["ListFieldOptions"]
			h["ListFieldOptions"] = func(in any) (any, error) {
				if err, ok := tc.fail[sdkaws.ToString(in.(*connectcases.ListFieldOptionsInput).FieldId)]; ok {
					return nil, err
				}
				return next(in)
			}
			_, err := scanCasesTopLevel(t, st, h)
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
			if got := nativeIDs(rowsByNativeID(t, st, TypeCasesFieldOption)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("option rows = %v, want %v", got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if _, ok := rowsByNativeID(t, st, TypeCasesDomain)[casesTestDomain]; !ok {
				t.Error("domain missing; the option phase must not stop the older phases")
			}
		})
	}
}

// A hard error in an older phase after the fields were stored still lets the
// option phase run over them, and is returned.
func TestScanCases_OlderPhaseErrorStillRunsFieldOptions(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := casesHandlers(&calls)
	h["ListTemplates"] = func(any) (any, error) { return nil, casesBadInput }
	total, err := scanCasesTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeCasesFieldOption)); !slices.Equal(got, []string{casesTestOptA1, casesTestOptA2, casesTestOptB1}) {
		t.Errorf("option rows = %v, want all three", got)
	}
	if total != 8 {
		t.Errorf("total = %d, want 8", total)
	}
}

// A transient error in an older phase is demoted to a warning so that the
// option phase's hard error still reaches the dispatcher as a hard error.
func TestScanCases_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "cases")
	var calls parentCalls
	h := casesHandlers(&calls)
	h["ListTemplates"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["ListFieldOptions"] = func(any) (any, error) { return nil, casesBadInput }
	_, err := scanCasesTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if isTransientNetworkError(err) {
		t.Errorf("err = %v reads as transient; the hard option error must not be masked", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}
