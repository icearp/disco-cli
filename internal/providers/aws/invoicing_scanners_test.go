package aws

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/invoicing"
	invtypes "github.com/aws/aws-sdk-go-v2/service/invoicing/types"
	"github.com/icearp/disco-cli/store"
)

var (
	invDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: invoicing:List")
	invValidation = apiErr("ValidationException", "bad")
	invPayerOnly  = apiErr("ValidationException", "Operation not permitted for member accounts. This API is only allowed for regular or payer accounts.")
)

const (
	invUnit1ARN = "arn:aws:invoicing::123456789012:invoice-unit/u1"
	invUnit2ARN = "arn:aws:invoicing::123456789012:invoice-unit/u2"
	invPref1ARN = "arn:aws:invoicing::123456789012:procurement-portal-preference/p1"
	invPref2ARN = "arn:aws:invoicing::123456789012:procurement-portal-preference/p2"
)

// invPages serves two-page lists: page one answers a call without a token,
// page two the call carrying "p2", unless pageTwoErr replaces it. Every
// call's token is recorded.
type invPages struct {
	mu     sync.Mutex
	tokens []string
}

func (p *invPages) record(token *string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tokens = append(p.tokens, sv(token))
}

// preferences serves p1 on page one; p2 and an element without an ARN on page
// two.
func (p *invPages) preferences(pageTwoErr error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req, ok := in.(*invoicing.ListProcurementPortalPreferencesInput)
		if !ok {
			return nil, errors.New("unexpected ListProcurementPortalPreferences input")
		}
		p.record(req.NextToken)
		if req.NextToken == nil {
			created := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
			return &invoicing.ListProcurementPortalPreferencesOutput{ProcurementPortalPreferences: []invtypes.ProcurementPortalPreferenceSummary{{
				ProcurementPortalPreferenceArn:   ptrStr(invPref1ARN),
				ProcurementPortalName:            invtypes.ProcurementPortalNameCoupa,
				BuyerIdentifier:                  ptrStr("buyer-1"),
				CreateDate:                       &created,
				EinvoiceDeliveryPreferenceStatus: invtypes.ProcurementPortalPreferenceStatusActive,
			}}, NextToken: ptrStr("p2")}, nil
		}
		if pageTwoErr != nil {
			return nil, pageTwoErr
		}
		return &invoicing.ListProcurementPortalPreferencesOutput{ProcurementPortalPreferences: []invtypes.ProcurementPortalPreferenceSummary{
			{ProcurementPortalPreferenceArn: ptrStr(invPref2ARN)},
			{BuyerIdentifier: ptrStr("no-arn")},
		}}, nil
	}
}

// invoiceUnits serves u1 on page one and u2 on page two.
func (p *invPages) invoiceUnits(in any) (any, error) {
	req, ok := in.(*invoicing.ListInvoiceUnitsInput)
	if !ok {
		return nil, errors.New("unexpected ListInvoiceUnits input")
	}
	p.record(req.NextToken)
	if req.NextToken == nil {
		return &invoicing.ListInvoiceUnitsOutput{InvoiceUnits: []invtypes.InvoiceUnit{{InvoiceUnitArn: ptrStr(invUnit1ARN)}}, NextToken: ptrStr("p2")}, nil
	}
	return &invoicing.ListInvoiceUnitsOutput{InvoiceUnits: []invtypes.InvoiceUnit{{InvoiceUnitArn: ptrStr(invUnit2ARN)}}}, nil
}

func scanInvPrefsWith(st *store.Store, handler func(in any) (any, error)) (int, error) {
	client := invoicing.NewFromConfig(opStubConfig(map[string]func(any) (any, error){"ListProcurementPortalPreferences": handler}, invDenied))
	total, _, err := scanInvoicingProcurementPortalPreferences(context.Background(), client, newTestAccount(testAccountID), "us-east-1", st, testScanID)
	return total, err
}

// Both pages are stored under the returned ARN in the global region; an
// element without an ARN is skipped, and neither Name nor Status is set.
func TestScanInvoicingProcurementPortalPreferences_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	var pages invPages
	total, err := scanInvPrefsWith(st, pages.preferences(nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(pages.tokens, []string{"", "p2"}) {
		t.Errorf("tokens sent = %q, want [\"\" \"p2\"]", pages.tokens)
	}
	rows := rowsByNativeID(t, st, TypeInvoicingProcurementPortalPreference)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("total = %d, rows = %v; want p1 and p2", total, rows)
	}
	p1, ok := rows[invPref1ARN]
	if !ok {
		t.Fatalf("missing %s in %v", invPref1ARN, rows)
	}
	if p1.Type != "aws:invoicing:procurement-portal-preference" || sv(p1.Region) != "global" || sv(p1.CreatedAt) != "2026-03-04T05:06:07Z" {
		t.Errorf("p1 type/region/created = %q/%q/%q, want aws:invoicing:procurement-portal-preference/global/2026-03-04T05:06:07Z", p1.Type, sv(p1.Region), sv(p1.CreatedAt))
	}
	if p1.Name != nil || p1.Status != nil {
		t.Errorf("p1 name/status = %v/%v, want nil/nil", p1.Name, p1.Status)
	}
	for _, want := range []string{`"ProcurementPortalName":"COUPA"`, `"BuyerIdentifier":"buyer-1"`, `"EinvoiceDeliveryPreferenceStatus":"ACTIVE"`} {
		if !strings.Contains(p1.AttributesJSON, want) {
			t.Errorf("p1 attrs = %s, want %s", p1.AttributesJSON, want)
		}
	}
	if _, ok := rows[invPref2ARN]; !ok {
		t.Errorf("missing %s in %v", invPref2ARN, rows)
	}
}

func TestScanInvoicingProcurementPortalPreferences_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanInvPrefsWith(st, func(any) (any, error) { return &invoicing.ListProcurementPortalPreferencesOutput{}, nil })
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeInvoicingProcurementPortalPreference)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// On the first call, every shape an unrouted operation takes is a silent
// feature gap and a denial warns; any other error is returned.
func TestScanInvoicingProcurementPortalPreferences_FirstPageError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantWarns int
	}{
		{name: "unknown operation", err: apiErr("UnknownOperationException", "")},
		{name: "unrouted gateway", err: apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")},
		{name: "http 404", err: respErrCode(404)},
		{name: "denied", err: invDenied, wantWarns: 1},
		{name: "other", err: invValidation, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "invoicing:ListProcurementPortalPreferences")
			total, err := scanInvPrefsWith(st, func(any) (any, error) { return nil, tc.err })
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if total != 0 || *warns != tc.wantWarns {
				t.Errorf("total = %d, warnings = %d; want 0, %d", total, *warns, tc.wantWarns)
			}
		})
	}
}

// After a page has succeeded, a denial warns once and keeps page one's row;
// every other error, the not-deployed shapes included, is returned with page
// one's row stored.
func TestScanInvoicingProcurementPortalPreferences_PageTwoError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantHTTP  int
		wantWarns int
	}{
		{name: "denied", err: invDenied, wantWarns: 1},
		{name: "unknown operation", err: apiErr("UnknownOperationException", ""), wantCode: "UnknownOperationException"},
		{name: "http 404", err: respErrCode(404), wantHTTP: 404},
		{name: "payer only", err: invPayerOnly, wantCode: "ValidationException"},
		{name: "other", err: invValidation, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "invoicing:ListProcurementPortalPreferences")
			var pages invPages
			total, err := scanInvPrefsWith(st, pages.preferences(tc.err))
			switch {
			case tc.wantCode != "":
				assertAPICode(t, err, tc.wantCode)
			case tc.wantHTTP != 0:
				if c, ok := httpStatusCode(err); !ok || c != tc.wantHTTP {
					t.Fatalf("err = %v, want HTTP %d", err, tc.wantHTTP)
				}
			case err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeInvoicingProcurementPortalPreference)
			if _, ok := rows[invPref1ARN]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want p1 only", total, rows)
			}
		})
	}
}

// A failed write of the rows listed before a list error reports both errors.
func TestScanInvoicingProcurementPortalPreferences_WriteAndListErrorsBothReported(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	var pages invPages
	_, err = scanInvPrefsWith(st, pages.preferences(invValidation))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want store.ErrStoreWrite", err)
	}
	assertAPICode(t, err, "ValidationException")
}

// Both phases run against us-east-1 whatever region the config names, and
// their rows are stored and counted.
func TestScanInvoicing_StoresUnitsAndPreferences(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var units, prefs invPages
	var regions parentCalls
	cfg := opStubConfig(map[string]func(any) (any, error){
		"ListInvoiceUnits":                 units.invoiceUnits,
		"ListProcurementPortalPreferences": prefs.preferences(nil),
	}, invDenied)
	cfg.Region = "eu-west-1"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, _, err := scanInvoicing(context.Background(), acct, "", st, testScanID)
	if err != nil {
		t.Fatalf("scanInvoicing: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4 (2 invoice units, 2 preferences)", total)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"us-east-1"}) {
		t.Errorf("calls went to regions %v, want [us-east-1]", got)
	}
	for typ, ids := range map[string][]string{
		TypeInvoicingInvoiceUnit:                 {invUnit1ARN, invUnit2ARN},
		TypeInvoicingProcurementPortalPreference: {invPref1ARN, invPref2ARN},
	} {
		rows := rowsByNativeID(t, st, typ)
		for _, id := range ids {
			if _, ok := rows[id]; !ok {
				t.Errorf("%s row %s missing: %v", typ, id, rows)
			}
		}
	}
}

// The preference phase runs after the invoice units: its hard error reaches
// the caller with the units and its own page-one row stored and counted.
func TestScanInvoicing_PreferenceErrorAfterUnits(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var units, prefs invPages
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListInvoiceUnits":                 units.invoiceUnits,
		"ListProcurementPortalPreferences": prefs.preferences(invValidation),
	}, invDenied)

	total, _, err := scanInvoicing(context.Background(), acct, "", st, testScanID)
	assertAPICode(t, err, "ValidationException")
	if total != 3 {
		t.Errorf("total = %d, want 3 (2 invoice units, 1 preference)", total)
	}
	if n := len(rowsByNativeID(t, st, TypeInvoicingInvoiceUnit)); n != 2 {
		t.Errorf("invoice unit rows = %d, want 2", n)
	}
	if _, ok := rowsByNativeID(t, st, TypeInvoicingProcurementPortalPreference)[invPref1ARN]; !ok {
		t.Errorf("missing preference %s", invPref1ARN)
	}
}

// A member account's payer-only rejection is not-entitled for its phase only:
// the other phase still runs, and the scan reports not-entitled only when both
// phases were. When one phase lists, its rows are stored and counted and the
// scan returns nil, since the dispatcher discards a sentinel's counts.
func TestScanInvoicing_PayerOnlyPerPhase(t *testing.T) {
	payerOnly := func(any) (any, error) { return nil, invPayerOnly }
	for _, tc := range []struct {
		name            string
		units, prefs    func(*invPages) func(any) (any, error)
		wantNotEntitled bool
		wantTotal       int
		wantUnits       int
		wantPrefs       int
	}{
		{
			name:      "units not entitled, preferences listed",
			units:     func(*invPages) func(any) (any, error) { return payerOnly },
			prefs:     func(p *invPages) func(any) (any, error) { return p.preferences(nil) },
			wantTotal: 2, wantPrefs: 2,
		},
		{
			name:      "units listed, preferences not entitled",
			units:     func(p *invPages) func(any) (any, error) { return p.invoiceUnits },
			prefs:     func(*invPages) func(any) (any, error) { return payerOnly },
			wantTotal: 2, wantUnits: 2,
		},
		{
			name:            "both not entitled",
			units:           func(*invPages) func(any) (any, error) { return payerOnly },
			prefs:           func(*invPages) func(any) (any, error) { return payerOnly },
			wantNotEntitled: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			var units, prefs invPages
			acct.cfg = opStubConfig(map[string]func(any) (any, error){
				"ListInvoiceUnits":                 tc.units(&units),
				"ListProcurementPortalPreferences": tc.prefs(&prefs),
			}, invDenied)

			total, _, err := scanInvoicing(context.Background(), acct, "", st, testScanID)
			if tc.wantNotEntitled {
				if !errors.Is(err, errServiceNotEntitled) {
					t.Fatalf("err = %v, want errServiceNotEntitled", err)
				}
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			nUnits := len(rowsByNativeID(t, st, TypeInvoicingInvoiceUnit))
			nPrefs := len(rowsByNativeID(t, st, TypeInvoicingProcurementPortalPreference))
			if total != tc.wantTotal || nUnits != tc.wantUnits || nPrefs != tc.wantPrefs {
				t.Errorf("total = %d, unit rows = %d, preference rows = %d; want %d, %d, %d", total, nUnits, nPrefs, tc.wantTotal, tc.wantUnits, tc.wantPrefs)
			}
		})
	}
}
