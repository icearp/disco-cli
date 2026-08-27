package azure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/icearp/disco-cli/store"
)

// TestSubscriptionResourceBatch_KeepsOnlyTheScannedSubscription pins the
// disclosure boundary: GET /subscriptions answers for every delegation the
// managing tenant holds, so an unfiltered page records other customers'
// subscriptions under this customer's account.
func TestSubscriptionResourceBatch_KeepsOnlyTheScannedSubscription(t *testing.T) {
	sub := &subscription{ID: "11111111-1111-1111-1111-111111111111", Name: "ours"}
	page := []*armsubscription.Subscription{
		{
			ID:             to.Ptr("/subscriptions/11111111-1111-1111-1111-111111111111"),
			SubscriptionID: to.Ptr("11111111-1111-1111-1111-111111111111"),
			DisplayName:    to.Ptr("Ours"),
		},
		{
			ID:             to.Ptr("/subscriptions/22222222-2222-2222-2222-222222222222"),
			SubscriptionID: to.Ptr("22222222-2222-2222-2222-222222222222"),
			DisplayName:    to.Ptr("Another Customer Production"),
		},
		nil,
		{SubscriptionID: to.Ptr("11111111-1111-1111-1111-111111111111")}, // no ID: skipped
	}

	// Same subscription, with only the ARM id populated. It must still match:
	// a dropped entry is later reported as a possibly-revoked delegation, so
	// failing to match here produces a confident, wrong diagnosis.
	idOnly := []*armsubscription.Subscription{{
		ID: to.Ptr("/subscriptions/11111111-1111-1111-1111-111111111111"),
	}}
	if got := subscriptionResourceBatch(idOnly, sub, "scan-1"); len(got) != 1 {
		t.Errorf("recorded %d resources for an entry carrying only an ARM id; want 1", len(got))
	}

	got := subscriptionResourceBatch(page, sub, "scan-1")
	if len(got) != 1 {
		t.Fatalf("recorded %d resources; want exactly the scanned subscription", len(got))
	}
	if got[0].NativeID != "/subscriptions/11111111-1111-1111-1111-111111111111" {
		t.Errorf("recorded native id %q; want the scanned subscription", got[0].NativeID)
	}
	for _, r := range got {
		if strings.Contains(r.AttributesJSON, "2222") {
			t.Errorf("attributes carry another customer's subscription: %s", r.AttributesJSON)
		}
	}
}

// TestSubscriptionResourceBatch_MatchesCaseInsensitively guards the GUID case
// the ARM API is free to vary.
func TestSubscriptionResourceBatch_MatchesCaseInsensitively(t *testing.T) {
	sub := &subscription{ID: "AAAAAAAA-1111-1111-1111-111111111111"}
	page := []*armsubscription.Subscription{{
		ID:             to.Ptr("/subscriptions/aaaaaaaa-1111-1111-1111-111111111111"),
		SubscriptionID: to.Ptr("aaaaaaaa-1111-1111-1111-111111111111"),
	}}
	if got := subscriptionResourceBatch(page, sub, "scan-1"); len(got) != 1 {
		t.Fatalf("recorded %d resources; want 1 (case-insensitive match)", len(got))
	}
}

// TestRedactCredentialError asserts disco's own identifiers never reach the
// customer-visible scan record.
//
// The text names disco's Entra tenant GUID (in the authority URL azidentity
// prints ahead of any body), disco's AWS role ARN (when the assertion callback
// fails, where there is no body at all), and trace ids. None is a secret, and
// none is part of what the customer scanned for.
func TestRedactCredentialError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
		leaks    []string
	}{
		{
			name: "entra rejects the assertion",
			err: errors.New("ClientAssertionCredential authentication failed. POST " +
				"https://login.microsoftonline.com/581de929-8709-4154-a686-84103b1adc23/oauth2/v2.0/token\n" +
				"AADSTS700213: No matching federated identity record found for presented assertion subject " +
				"'arn:aws:sts::228886154857:assumed-role/disco-saas-dev-scanner-azure/disco-azure-federation'. " +
				"Trace ID: abc Correlation ID: def"),
			wantCode: "AADSTS700213",
			leaks:    []string{"581de929", "assumed-role", "Trace ID"},
		},
		{
			// The case the AADSTS-only trigger missed entirely: MSAL made no
			// HTTP call, so there is no body and no AADSTS code — just STS
			// telling us which of DISCO's principals was refused.
			name: "aws refuses to mint the assertion",
			// The STS call happens inside the assertion callback, and
			// azidentity REPLACES whatever the callback returns with an
			// AuthenticationFailedError carrying its text. That type has no
			// Unwrap, so production's chain is the AuthenticationFailedError
			// with no cause, while this fixture nests it the other way because
			// the message field is unexported. errors.As is satisfied either
			// way, and errors.As is what the redaction triggers on —
			// deliberately not the
			// "azure wif:" prefix, which every pre-exchange CONFIG refusal
			// carries too (TestRedactCredentialError_LeavesConfigRefusalsAlone).
			err: fmt.Errorf("ClientAssertionCredential: azure wif: get web identity token: "+
				"operation error STS: GetWebIdentityToken, https response error StatusCode: 403, "+
				"AccessDenied: User: arn:aws:sts::228886154857:assumed-role/disco-saas-dev-scanner/x "+
				"is not authorized to perform: sts:GetWebIdentityToken: %w",
				&azidentity.AuthenticationFailedError{}),
			leaks: []string{"arn:aws:sts", "228886154857", "assumed-role"},
		},
		{
			// The case that settles WHY the trigger is the error type and not
			// the AADSTS substring: a gateway error has an HTML body and no
			// code, while azidentity still prints the authority URL ahead of
			// it — and that URL carries disco's tenant GUID.
			name: "the authority answers 502 with no code",
			err: fmt.Errorf("ClientAssertionCredential authentication failed. POST "+
				"https://login.microsoftonline.com/581de929-8709-4154-a686-84103b1adc23/oauth2/v2.0/token\n"+
				"<html>502 Bad Gateway</html>: %w", &azidentity.AuthenticationFailedError{}),
			leaks: []string{"581de929"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactCredentialError(tt.err)
			if got == "" {
				t.Fatalf("redactCredentialError() returned \"\", so the raw text reaches the scan record: %v", tt.err)
			}
			for _, leak := range tt.leaks {
				if strings.Contains(got, leak) {
					t.Errorf("redacted message still carries %q: %q", leak, got)
				}
			}
			if tt.wantCode != "" && !strings.Contains(got, tt.wantCode) {
				t.Errorf("redacted message dropped %s, leaving nothing to search on: %q", tt.wantCode, got)
			}
		})
	}
}

// TestRedactCredentialError_LeavesOrdinaryErrorsAlone guards the other
// direction: an ARM permission failure is exactly what the customer needs to
// read, and redacting it would hide their own misconfiguration.
func TestRedactCredentialError_LeavesOrdinaryErrorsAlone(t *testing.T) {
	for _, err := range []error{
		errors.New("403 Forbidden: caller lacks Reader on /subscriptions/1111"),
		errors.New("context deadline exceeded"),
	} {
		if got := redactCredentialError(err); got != "" {
			t.Errorf("redactCredentialError(%v) = %q; want \"\" (not a credential failure)", err, got)
		}
	}
}

// TestFormatAzureError_RedactsOnEveryArm pins the call site. formatAzureError
// returns err.Error() verbatim from two separate arms, and a credential
// failure can reach either: the non-ResponseError branch (a GetToken failure
// carries no HTTP response) and the default branch.
func TestFormatAzureError_RedactsOnEveryArm(t *testing.T) {
	err := errors.New("ClientAssertionCredential: AADSTS70021: No matching federated identity record found")
	got := formatAzureError(err)
	if strings.Contains(got, "federated identity record") {
		t.Errorf("formatAzureError leaked Entra diagnostic text: %q", got)
	}
	if !strings.Contains(got, "AADSTS70021") {
		t.Errorf("formatAzureError dropped the diagnostic code: %q", got)
	}

	// A *azcore.ResponseError with no status and no ARM message falls to the
	// default arm, which returned err.Error() unredacted.
	respErr := &azcore.ResponseError{ErrorCode: "AADSTS70021: no matching federated identity record"}
	if got := formatAzureError(respErr); strings.Contains(got, "no matching federated identity record") {
		t.Errorf("formatAzureError's default arm leaked Entra diagnostic text: %q", got)
	}
}

// TestEnumerateScope_RefusesUnderFederation pins the second tenant-wide
// GET /subscriptions call. It runs with a nil credential on purpose: the
// refusal must come before any token is requested, so a nil deref here means
// the guard moved below the call it is meant to prevent.
func TestEnumerateScope_RefusesUnderFederation(t *testing.T) {
	wif := federatedCfg()
	if !wif.configured() {
		t.Fatal("fixture is not federated; the test would assert nothing")
	}
	_, err := enumerateScope(t.Context(), nil, wif)
	if !errors.Is(err, ErrFederatedEnumeration) {
		t.Fatalf("enumerateScope() error = %v; want ErrFederatedEnumeration", err)
	}
}

// TestResolveSubscriptionScope_RefusesAnEmptyConfigList guards the input path
// federation makes load-bearing: with enumeration refused, the config list is
// the only alternative to --subscriptions, and an empty id there builds a
// malformed "/subscriptions/" scope rather than failing.
func TestResolveSubscriptionScope_RefusesAnEmptyConfigList(t *testing.T) {
	cfg := providerCfg{Subscriptions: []subscriptionCfg{{ID: "  "}, {ID: ""}}}
	_, err := resolveSubscriptionScope(nil, cfg, func() ([]subscription, error) {
		t.Fatal("enumerate called; an empty config list must fail closed, not auto-discover")
		return nil, nil
	})
	if err == nil {
		t.Fatal("resolveSubscriptionScope() succeeded on a config list of empty ids; want a refusal")
	}
}

// TestRedactCredentialError_LeavesConfigRefusalsAlone is the regression guard
// for two corrections that cancelled each other.
//
// Redaction was first triggered on the "azure wif:" prefix, which every error
// this package raises carries — including the ones raised BEFORE any exchange:
// a half-set contract and a missing AWS_REGION. Both name the variable an
// operator must fix, and collapsing them to "authentication failed" produces
// exactly the outcome the eager region check was added to prevent. There is
// nothing to redact in either: they contain no tenant GUID, no ARN and no
// trace id.
func TestRedactCredentialError_LeavesConfigRefusalsAlone(t *testing.T) {
	for _, err := range []error{
		ErrIncompleteWIFConfig,
		fmt.Errorf("azure credential: %w", ErrIncompleteWIFConfig),
		errors.New("azure wif: no AWS region resolved; set AWS_REGION"),
		errors.New("azure wif: DISCO_AZURE_WIF_ROLE_ARN and DISCO_AZURE_WIF_SESSION_NAME must be set together"),
	} {
		if got := redactCredentialError(err); got != "" {
			t.Errorf("redactCredentialError(%v) = %q; want \"\" — a config refusal must name the variable to fix", err, got)
		}
	}
}

// TestScan_KeepsAConfigRefusalMatchable is the other half: the redaction path
// used to drop the error chain, so errors.Is could not see a refusal that
// reached the one call site that ships.
func TestScan_KeepsAConfigRefusalMatchable(t *testing.T) {
	t.Setenv(envWIFClientID, "client-guid")
	t.Setenv(envWIFTenantID, "")
	t.Setenv(envWIFAudience, "")
	t.Setenv(envWIFRoleARN, "")
	t.Setenv(envWIFSessionName, "")

	err := (&Scanner{}).Scan(t.Context(), newTestStore(t), testScanID)
	if !errors.Is(err, ErrIncompleteWIFConfig) {
		t.Fatalf("Scan() error = %v; want it to wrap ErrIncompleteWIFConfig", err)
	}
	if strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("a configuration refusal was reported as an authentication failure: %q", err)
	}
	if !strings.Contains(err.Error(), envWIFTenantID) {
		t.Errorf("the refusal does not name the variable to set: %q", err)
	}
}

// TestRedactCredentialError_LeavesAnARMPermissionErrorAlone uses the shape
// production actually produces for a customer-side permission failure. An
// *azcore.ResponseError parses the body's code into ErrorCode, which is what
// the AADSTS check tests — so this is the guard against the redaction
// swallowing the one error the customer most needs to read.
func TestRedactCredentialError_LeavesAnARMPermissionErrorAlone(t *testing.T) {
	respErr := armResponseError(http.StatusForbidden, "AuthorizationFailed",
		"The client does not have authorization to perform action 'Microsoft.Resources/subscriptions/read' over scope '/subscriptions/1111'.")

	if got := redactCredentialError(respErr); got != "" {
		t.Fatalf("redactCredentialError() = %q; want \"\" — an ARM permission failure is the customer's own to fix", got)
	}
	if got := formatAzureError(respErr); !strings.Contains(got, "AuthorizationFailed") {
		t.Errorf("formatAzureError() = %q; want it to keep the ARM error code", got)
	}
}

// armResponseError builds the *azcore.ResponseError shape ARM produces, with
// the code parsed out of the body exactly as azcore does it.
func armResponseError(status int, code, message string) *azcore.ResponseError {
	body := `{"error":{"code":"` + code + `","message":"` + message + `"}}`
	return &azcore.ResponseError{
		StatusCode: status,
		ErrorCode:  code,
		RawResponse: &http.Response{
			StatusCode: status,
			Request:    &http.Request{Method: http.MethodGet, URL: &url.URL{Scheme: "https", Host: "management.azure.com", Path: "/subscriptions/1111"}},
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(body)),
		},
	}
}

// TestRedactCredentialError_RedactsARMRejectingTheTokenItself covers the shape
// that escaped the ErrorCode-only check: ARM answers 401 with its OWN code,
// InvalidAuthenticationToken, and puts the AADSTS text — which names disco's
// tenant and the assertion subject — in the message. Testing only the code
// would read "not AADSTS" and store the whole body on the customer's scan.
func TestRedactCredentialError_RedactsARMRejectingTheTokenItself(t *testing.T) {
	err := armResponseError(http.StatusUnauthorized, "InvalidAuthenticationToken",
		"AADSTS700213: No matching federated identity record found for presented assertion subject.")

	got := redactCredentialError(err)
	if got == "" {
		t.Fatalf("redactCredentialError() = \"\"; want a redaction — ARM rejected our token and the message names disco's federation")
	}
	if !strings.Contains(got, "AADSTS700213") {
		t.Errorf("redactCredentialError() = %q; want it to keep the diagnostic code", got)
	}
	if strings.Contains(got, "federated identity record") {
		t.Errorf("redactCredentialError() = %q; want the message body dropped", got)
	}
}

// TestRedactCredentialError_RedactsAResponseWithNoParsedCode covers the other
// escape: a response azcore could not parse a code from at all (an HTML error
// page from a proxy). "No code" must not be read as "not a credential
// failure", because there is nothing cheaper than the body left to test.
func TestRedactCredentialError_RedactsAResponseWithNoParsedCode(t *testing.T) {
	err := &azcore.ResponseError{
		StatusCode: http.StatusBadGateway,
		RawResponse: &http.Response{
			StatusCode: http.StatusBadGateway,
			Request:    &http.Request{Method: http.MethodGet, URL: &url.URL{Scheme: "https", Host: "login.microsoftonline.com", Path: "/oauth2/v2.0/token"}},
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("<html><body>AADSTS90033: A transient error has occurred.</body></html>")),
		},
	}

	if got := redactCredentialError(err); !strings.Contains(got, "AADSTS90033") {
		t.Fatalf("redactCredentialError() = %q; want the code lifted from an unparsed body", got)
	}
}

// TestRedactCredentialError_DoesNotRedactAnOrdinaryARMError pins the direction
// nothing else in this file can see: a 4xx that is neither an AADSTS code nor
// a 401 is answered from what ARM classified it as, and its body is not
// searched. Widening the gate to always scan err.Error() passes every other
// test here, and costs the customer the one diagnostic that is genuinely
// theirs — redaction throws the message away, so an ARM failure that merely
// MENTIONS AADSTS would lose the action and scope they lack permission on.
// The AADSTS text in this message exists only so a widened gate produces a
// visible redaction.
func TestRedactCredentialError_DoesNotRedactAnOrdinaryARMError(t *testing.T) {
	err := armResponseError(http.StatusForbidden, "AuthorizationFailed",
		"The client does not have authorization. AADSTS99999: decoy in the body.")

	if got := redactCredentialError(err); got != "" {
		t.Fatalf("redactCredentialError() = %q; want \"\" — the code alone decides, so the body is never rendered", got)
	}
}

// fakeSubscriptionPager serves canned pages, then a canned error.
type fakeSubscriptionPager struct {
	pages  []armsubscription.SubscriptionsClientListResponse
	err    error
	i      int
	failed bool
}

// More mirrors runtime.Pager, whose cursor advances only on a successful
// fetch — so a failure leaves More() true and only a clean drain turns it
// false. That is the property scanSubscriptionResourceWithPager relies on to
// tell a missing pin from a denied listing, so a fake that got it wrong would
// make the distinction untestable.
func (p *fakeSubscriptionPager) More() bool {
	if p.failed {
		return true
	}
	return p.i < len(p.pages) || (p.err != nil && p.i == len(p.pages))
}

func (p *fakeSubscriptionPager) NextPage(context.Context) (armsubscription.SubscriptionsClientListResponse, error) {
	if p.i < len(p.pages) {
		page := p.pages[p.i]
		p.i++
		return page, nil
	}
	p.i++
	p.failed = true
	return armsubscription.SubscriptionsClientListResponse{}, p.err
}

// TestScanSubscriptionResource_WarnsWhenThePinNeverAppears covers the miss
// case: a subscription the credential cannot see filters to nothing on every
// page, and the scan would otherwise report an empty success.
func TestScanSubscriptionResource_WarnsWhenThePinNeverAppears(t *testing.T) {
	var warnings []store.ScanWarning
	var mu sync.Mutex
	st := newTestStore(t)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, w)
	}

	sub := &subscription{ID: "11111111-1111-1111-1111-111111111111"}
	pager := &fakeSubscriptionPager{pages: []armsubscription.SubscriptionsClientListResponse{{
		ListResult: armsubscription.ListResult{Value: []*armsubscription.Subscription{{
			ID:             to.Ptr("/subscriptions/22222222-2222-2222-2222-222222222222"),
			SubscriptionID: to.Ptr("22222222-2222-2222-2222-222222222222"),
		}}},
	}}}

	if _, _, err := scanSubscriptionResourceWithPager(t.Context(), sub, st, testScanID, pager); err != nil {
		t.Fatalf("scanSubscriptionResourceWithPager() error = %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("emitted %d warnings (%+v); want one naming the missing subscription", len(warnings), warnings)
	}
}

// TestScanSubscriptionResource_DoesNotBlameRevocationForAccessDenied is the
// other direction, and the reason the miss check is not just err == nil:
// azPageScan answers nil for an access-denied list AFTER reporting its own
// warning, so a naive miss check adds a second warning blaming a revoked
// delegation for a permission failure already reported with a different cause.
func TestScanSubscriptionResource_DoesNotBlameRevocationForAccessDenied(t *testing.T) {
	var warnings []store.ScanWarning
	var mu sync.Mutex
	st := newTestStore(t)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, w)
	}

	sub := &subscription{ID: "11111111-1111-1111-1111-111111111111"}
	pager := &fakeSubscriptionPager{err: &azcore.ResponseError{StatusCode: 403, ErrorCode: "AuthorizationFailed"}}

	if _, _, err := scanSubscriptionResourceWithPager(t.Context(), sub, st, testScanID, pager); err != nil {
		t.Fatalf("scanSubscriptionResourceWithPager() error = %v; access denied is skipped, not raised", err)
	}
	for _, w := range warnings {
		if strings.Contains(w.Message, "delegation") {
			t.Errorf("access denied was reported as a possible revoked delegation: %q", w.Message)
		}
	}
}

// TestScanSubscriptionResource_DoesNotBlameRevocationForAMidPaginationDenial
// is the case a page tally could not see. Access denied part-way through
// pagination leaves pages already read, so counting them reports the pin as
// missing on top of the access-denied warning the pager already emitted.
func TestScanSubscriptionResource_DoesNotBlameRevocationForAMidPaginationDenial(t *testing.T) {
	var warnings []store.ScanWarning
	var mu sync.Mutex
	st := newTestStore(t)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, w)
	}

	sub := &subscription{ID: "11111111-1111-1111-1111-111111111111"}
	pager := &fakeSubscriptionPager{
		pages: []armsubscription.SubscriptionsClientListResponse{{
			ListResult: armsubscription.ListResult{Value: []*armsubscription.Subscription{{
				ID:             to.Ptr("/subscriptions/22222222-2222-2222-2222-222222222222"),
				SubscriptionID: to.Ptr("22222222-2222-2222-2222-222222222222"),
			}}},
		}},
		err: &azcore.ResponseError{StatusCode: 403, ErrorCode: "AuthorizationFailed"},
	}

	if _, _, err := scanSubscriptionResourceWithPager(t.Context(), sub, st, testScanID, pager); err != nil {
		t.Fatalf("scanSubscriptionResourceWithPager() error = %v; access denied is skipped, not raised", err)
	}
	for _, w := range warnings {
		if strings.Contains(w.Message, "delegation") {
			t.Errorf("a denial part-way through pagination was reported as a possible revoked delegation: %q", w.Message)
		}
	}
}

// TestReportEntraErr_RedactsACredentialFailure pins the one thing routing this
// function through formatAzureError buys. For a *graphErr the formatted and
// raw strings are identical, so only the credential case can see the
// difference — and that is the case where the text names disco's own tenant
// rather than anything the customer scanned. The Graph phase is gated under
// federation today; this is the guard for when it reopens.
func TestReportEntraErr_RedactsACredentialFailure(t *testing.T) {
	st := newTestStore(t)
	var mu sync.Mutex
	var msgs []string
	st.OnWarn = func(w store.ScanWarning) { mu.Lock(); defer mu.Unlock(); msgs = append(msgs, "warn:"+w.Message) }
	st.OnError = func(e store.ScanError) { mu.Lock(); defer mu.Unlock(); msgs = append(msgs, "error:"+e.Message) }

	err := fmt.Errorf("graph: 401: %w", &azidentity.AuthenticationFailedError{})
	reportEntraErr(st, "tenant", err)

	// Tagged, so this cannot pass on the branch its sibling covers.
	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "warn:") {
		t.Fatalf("reported %q; want exactly one ScanWarning", msgs)
	}
	if !strings.Contains(msgs[0], "azure authentication failed") {
		t.Errorf("reported %q; want the redaction — the raw text names disco's tenant", msgs[0])
	}
}

// TestReportEntraErr_RedactsOnTheHardErrorBranchToo covers the second report
// site. The classifier sends anything without its five permission substrings
// to ScanError, and a mutant that reverts only that branch to the raw text
// survives a test that exercises the warning branch alone.
func TestReportEntraErr_RedactsOnTheHardErrorBranchToo(t *testing.T) {
	st := newTestStore(t)
	var mu sync.Mutex
	var msgs []string
	st.OnWarn = func(w store.ScanWarning) { mu.Lock(); defer mu.Unlock(); msgs = append(msgs, "warn:"+w.Message) }
	st.OnError = func(e store.ScanError) { mu.Lock(); defer mu.Unlock(); msgs = append(msgs, "error:"+e.Message) }

	// No " 401"/" 403"/Authorization_RequestDenied/Insufficient privileges, so
	// this lands on the hard-error branch.
	err := fmt.Errorf("graph request failed: %w", &azidentity.AuthenticationFailedError{})
	reportEntraErr(st, "tenant", err)

	if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "error:") {
		t.Fatalf("reported %q; want exactly one ScanError", msgs)
	}
	if !strings.Contains(msgs[0], "azure authentication failed") {
		t.Errorf("reported %q; want the redaction on this branch too", msgs[0])
	}
}

// TestReportEntraErr_KeepsAConsentDenialReadable is the other direction, and
// the reason scanBodyForAADSTS has a graphErr arm at all. Routing this
// function through formatAzureError put every Graph error past the redaction
// gate, whose non-ResponseError fallback is a bare AADSTS substring test — so
// a 403 naming the missing consent would have been collapsed to
// "authentication failed", costing the customer the one line that tells them
// what to grant.
func TestReportEntraErr_KeepsAConsentDenialReadable(t *testing.T) {
	st := newTestStore(t)
	var mu sync.Mutex
	var msgs []string
	st.OnWarn = func(w store.ScanWarning) { mu.Lock(); defer mu.Unlock(); msgs = append(msgs, w.Message) }
	st.OnError = func(e store.ScanError) { mu.Lock(); defer mu.Unlock(); msgs = append(msgs, e.Message) }

	// A real consent denial, with an AADSTS decoy in the body.
	err := &graphErr{status: 403, body: `{"error":{"code":"Authorization_RequestDenied",` +
		`"message":"Insufficient privileges to complete the operation. AADSTS99999 seen here."}}`}
	reportEntraErr(st, "tenant", err)

	if len(msgs) != 1 {
		t.Fatalf("reported %d messages (%q); want one", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "Authorization_RequestDenied") {
		t.Errorf("reported %q; want the consent diagnostic kept — it is the customer's to act on", msgs[0])
	}
}

// TestReportEntraErr_RedactsAGraphTokenRejection is the positive direction of
// the same arm, and without it the arm can be mutated to a no-op silently: the
// only other *graphErr in this package's tests is a 403, and the
// credential-failure tests reach redactCredentialError's TYPE arm before
// scanBodyForAADSTS is consulted at all. A Graph 401 is disco's token being
// refused, not the customer's permissions, so its text is not theirs to read.
func TestReportEntraErr_RedactsAGraphTokenRejection(t *testing.T) {
	st := newTestStore(t)
	var mu sync.Mutex
	var msgs []string
	st.OnWarn = func(w store.ScanWarning) { mu.Lock(); defer mu.Unlock(); msgs = append(msgs, w.Message) }
	st.OnError = func(e store.ScanError) { mu.Lock(); defer mu.Unlock(); msgs = append(msgs, e.Message) }

	err := &graphErr{status: 401, body: `{"error":{"code":"InvalidAuthenticationToken",` +
		`"message":"AADSTS50173: The provided grant has expired for tenant 581de929-8709-4154-a686-84103b1adc23."}}`}
	reportEntraErr(st, "tenant", err)

	if len(msgs) != 1 {
		t.Fatalf("reported %d messages (%q); want one", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "AADSTS50173") {
		t.Errorf("reported %q; want the diagnostic code kept", msgs[0])
	}
	if strings.Contains(msgs[0], "581de929") {
		t.Errorf("reported %q; the tenant GUID must not reach the scan record", msgs[0])
	}
}

// TestRedactCredentialError_NamesTheStatusWhenARMSendsNoCode covers a 401 that
// carries no ErrorCode and no AADSTS token in its body. The parenthetical then
// rendered as a bare "()", which reads to an operator as a defect in disco
// rather than as a refusal by Azure — and the redaction is exactly the moment
// the only remaining diagnostic is the one the message must still carry.
func TestRedactCredentialError_NamesTheStatusWhenARMSendsNoCode(t *testing.T) {
	err := armResponseError(http.StatusUnauthorized, "",
		"the access token issued for tenant 11111111-1111-1111-1111-111111111111 cannot be used here")

	got := redactCredentialError(err)
	if got == "" {
		t.Fatalf("redactCredentialError() = \"\"; want a redaction — the body names the issuer that was presented")
	}
	if strings.Contains(got, "()") {
		t.Errorf("redactCredentialError() = %q; want the status named, not an empty parenthetical", got)
	}
	if !strings.Contains(got, "Unauthorized") {
		t.Errorf("redactCredentialError() = %q; want it to name the status when ARM sends no code", got)
	}
	if strings.Contains(got, "11111111-1111-1111-1111-111111111111") {
		t.Errorf("redactCredentialError() = %q; want the body dropped", got)
	}
}
