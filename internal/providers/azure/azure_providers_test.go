package azure

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	azfake "github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	armresourcesfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/fake"
)

// TestProviderDisabled pins the gate decision: a service is only skipped when
// its ARM namespace is present in the registration map AND not registered.
// Unknown namespaces and a nil map (probe failed) always scan.
func TestProviderDisabled(t *testing.T) {
	reg := map[string]bool{
		"microsoft.compute": true,
		"microsoft.orbital": false,
	}
	cases := []struct {
		name    string
		reg     map[string]bool
		svc     string
		want    bool
		comment string
	}{
		{"registered → scan", reg, "azure:microsoft.compute", false, ""},
		{"not registered → disabled", reg, "azure:microsoft.orbital", true, ""},
		{"unknown namespace → scan", reg, "azure:microsoft.unknown", false, ""},
		{"nil map (probe failed) → scan", nil, "azure:microsoft.orbital", false, ""},
		{"case-insensitive match", map[string]bool{"microsoft.compute": false}, "azure:Microsoft.Compute", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerDisabled(tc.reg, tc.svc); got != tc.want {
				t.Errorf("providerDisabled(%q) = %v; want %v", tc.svc, got, tc.want)
			}
		})
	}
}

// TestRegisteredProvidersFromPager drives the pager drain against an
// armresources fake transport with a mix of registration states (including
// nil-field rows that must be skipped) and asserts the resulting map. Only
// "Registered"/"Registering" map to true; "NotRegistered"/"Unregistering" map
// to false.
func TestRegisteredProvidersFromPager(t *testing.T) {
	server := armresourcesfake.ProvidersServer{
		NewListPager: func(_ *armresources.ProvidersClientListOptions) azfake.PagerResponder[armresources.ProvidersClientListResponse] {
			r := azfake.PagerResponder[armresources.ProvidersClientListResponse]{}
			r.AddPage(http.StatusOK, armresources.ProvidersClientListResponse{
				ProviderListResult: armresources.ProviderListResult{Value: []*armresources.Provider{
					{Namespace: to.Ptr("Microsoft.Compute"), RegistrationState: to.Ptr("Registered")},
					{Namespace: to.Ptr("Microsoft.Orbital"), RegistrationState: to.Ptr("NotRegistered")},
					{Namespace: to.Ptr("Microsoft.Web"), RegistrationState: to.Ptr("Registering")},
					{Namespace: to.Ptr("Microsoft.Sql"), RegistrationState: to.Ptr("Unregistering")},
					{Namespace: to.Ptr("Microsoft.NoState"), RegistrationState: nil},
					{Namespace: nil, RegistrationState: to.Ptr("Registered")},
				}},
			}, nil)
			return r
		},
	}

	client, err := armresources.NewProvidersClient(testSubID, fakeCred(),
		fakeClientOptions(t, armresourcesfake.NewProvidersServerTransport(&server)))
	if err != nil {
		t.Fatalf("NewProvidersClient: %v", err)
	}

	got, err := registeredProvidersFromPager(t.Context(), client.NewListPager(nil))
	if err != nil {
		t.Fatalf("registeredProvidersFromPager: %v", err)
	}

	want := map[string]bool{
		"microsoft.compute": true,  // Registered
		"microsoft.orbital": false, // NotRegistered
		"microsoft.web":     true,  // Registering → treated as scan
		"microsoft.sql":     false, // Unregistering → treated as disabled
	}
	if len(got) != len(want) {
		t.Fatalf("map size: got %d (%v); want %d (%v)", len(got), got, len(want), want)
	}
	for ns, w := range want {
		if got[ns] != w {
			t.Errorf("registered[%q] = %v; want %v", ns, got[ns], w)
		}
	}
}

// TestFormatAzureError_RedactsARMTokenRejectionBody pins the leak boundary for
// EVERY caller, not just the unreachable-subscription gate.
//
// The gate refuses the subscription only after the resource-group list has
// already reported its own 401 through skipIfAccessDenied, which formats with
// this function and stored the ARM body verbatim. That body quotes the issuer
// that was presented -- under Lighthouse federation, disco's own directory,
// identical for every customer -- onto the CUSTOMER's scan record.
//
// The response carries a real body on purpose: the SDK fakes build a
// ResponseError with no body at all, so a "must not contain" assertion against
// one of those passes whether the code redacts or not.
func TestFormatAzureError_RedactsARMTokenRejectionBody(t *testing.T) {
	const issuer = "https://sts.windows.net/581de929-1111-2222-3333-444455556666/"
	body := `{"error":{"code":"InvalidAuthenticationTokenTenant","message":` +
		`"The access token is from the wrong issuer '` + issuer + `'. It must match ` +
		`the tenant 'https://sts.windows.net/99998888-7777-6666-5555-444433332222/' ` +
		`associated with this subscription."}}`
	respErr := &azcore.ResponseError{
		ErrorCode:  "InvalidAuthenticationTokenTenant",
		StatusCode: http.StatusUnauthorized,
		RawResponse: &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader(body)),
		},
	}

	got := formatAzureError(respErr)

	const want = "azure token rejected for this scope (InvalidAuthenticationTokenTenant); see scanner logs"
	if got != want {
		t.Errorf("formatAzureError() = %q; want %q", got, want)
	}
	// Exact equality already settles it; these name WHAT must not be there, so
	// a future rewording that reintroduces the body fails with the reason.
	for _, leak := range []string{"sts.windows.net", "issuer", "581de929"} {
		if strings.Contains(got, leak) {
			t.Errorf("formatAzureError() = %q; leaks %q onto the customer's scan record", got, leak)
		}
	}
}

// TestFormatAzureError_RedactsEverySiblingAuthCode is the reason the branch
// tests the STATUS and not a code prefix.
//
// Keying it on `InvalidAuthenticationToken` was the first version, and it left
// every sibling 401 storing its ARM body verbatim on the customer's scan
// record. A code list is a hand-maintained allow-list on a disclosure boundary
// and Microsoft adds codes; 401 means ARM refused OUR token, whatever it calls
// the refusal.
func TestFormatAzureError_RedactsEverySiblingAuthCode(t *testing.T) {
	for _, code := range []string{
		"ExpiredAuthenticationToken",
		"AuthenticationFailed",
		"InvalidAuthenticationInfo",
	} {
		t.Run(code, func(t *testing.T) {
			const issuer = "https://sts.windows.net/581de929-1111-2222-3333-444455556666/"
			body := `{"error":{"code":"` + code + `","message":"rejected for issuer '` + issuer + `'"}}`
			respErr := &azcore.ResponseError{
				ErrorCode:  code,
				StatusCode: http.StatusUnauthorized,
				RawResponse: &http.Response{
					StatusCode: http.StatusUnauthorized,
					Body:       io.NopCloser(strings.NewReader(body)),
				},
			}
			got := formatAzureError(respErr)
			want := "azure token rejected for this scope (" + code + "); see scanner logs"
			if got != want {
				t.Errorf("formatAzureError() = %q; want %q", got, want)
			}
			if strings.Contains(got, "sts.windows.net") {
				t.Errorf("formatAzureError() = %q; leaks the issuer for a sibling 401 code", got)
			}
		})
	}
}

// TestProbeError_DiscriminatesAuthenticationFromAuthorization drives the RP
// probe against a fake transport that fails, and asserts the discriminator
// scanSubscription gates the whole subscription on.
//
// The error is taken from a pager drain rather than hand-built, because the
// risk here is not the status-code comparison but whether the SDK's wrapping
// still lets errors.As reach the *azcore.ResponseError. Read that narrowly:
// azcore's fake constructs the *exported.ResponseError itself rather than
// going through runtime.NewResponseError, so this exercises the pager's
// wrapping and NOT the production construction step. What settles the
// production step is the live incident of 2026-08-26, whose recorded message
// is formatAzureError's ResponseError branch verbatim.
//
// 401 means the token has no standing for the subscription at all, so every
// service scanner that follows would issue a list call that cannot succeed.
// 403 means the principal is recognised and only this one role is too narrow,
// so the subscription is still worth scanning.
func TestProbeError_DiscriminatesAuthenticationFromAuthorization(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		code    string
		want    bool
		comment string
	}{
		{
			"401 → subscription unreachable", http.StatusUnauthorized, "InvalidAuthenticationTokenTenant", true,
			"no delegation to this principal",
		},
		{
			"403 → narrow role, keep scanning", http.StatusForbidden, "AuthorizationFailed", false,
			"recognised principal, one role too narrow",
		},
		{"404 → not an auth outcome", http.StatusNotFound, "SubscriptionNotFound", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := armresourcesfake.ProvidersServer{
				NewListPager: func(_ *armresources.ProvidersClientListOptions) azfake.PagerResponder[armresources.ProvidersClientListResponse] {
					r := azfake.PagerResponder[armresources.ProvidersClientListResponse]{}
					r.AddResponseError(tc.status, tc.code)
					return r
				},
			}
			client, err := armresources.NewProvidersClient(testSubID, fakeCred(),
				fakeClientOptions(t, armresourcesfake.NewProvidersServerTransport(&server)))
			if err != nil {
				t.Fatalf("NewProvidersClient: %v", err)
			}

			got, perr := registeredProvidersFromPager(t.Context(), client.NewListPager(nil))
			if perr == nil {
				t.Fatalf("expected the probe to fail with %d", tc.status)
			}
			if got != nil {
				t.Errorf("map on failure = %v; want nil so the caller does not gate on a partial probe", got)
			}
			if got := isAuthenticationFailure(perr); got != tc.want {
				t.Errorf("isAuthenticationFailure(%d %s) = %v; want %v (%s)",
					tc.status, tc.code, got, tc.want, tc.comment)
			}
			// A 403 must stay skippable so the existing per-call warning path is
			// unchanged. A 401 stays skippable too, deliberately: the gate in
			// scanSubscription is the ONLY place a 401 is more than a per-call
			// skip, and widening isSkippableScanError would change every other
			// call site with it.
			if !isSkippableScanError(perr) && tc.status != http.StatusNotFound {
				t.Errorf("%d stopped being skippable; the per-service warning path depends on it", tc.status)
			}
			if tc.want {
				// The message is a cross-repo contract twice over: disco-saas
				// matches this text to classify the account's health, and the
				// text is stored on the CUSTOMER's scan record, where the ARM
				// body must not appear -- it names the issuer that was
				// presented, which under federation is disco's own directory.
				//
				// Asserted as EQUALITY, not by hunting for the body's giveaway
				// strings. This fake's ResponseError carries no RawResponse
				// body at all, so a "must not contain sts.windows.net" check
				// passes just as well against the unredacted err.Error() --
				// measured: that mutant survived it. Equality is what fails the
				// moment anything else is interpolated.
				want := "subscription unreachable: ARM refused this token for the subscription " +
					"(401 " + tc.code + ") -- the delegation to this identity may never have been " +
					"made, may have been revoked, or the subscription may not exist"
				// formatAzureError is what scanSubscription's caller applies, and
				// it is where the sentence was being eaten. Assert the composition.
				if got := formatAzureError(unreachableSubscriptionError(perr)); got != want {
					t.Errorf("unreachable message =\n  %q\nwant\n  %q", got, want)
				}
			}
		})
	}
}

// TestSubscriptionUnreachable_RequiresBothSignals pins the conjunction that
// decides whether a whole subscription is refused. It is a conjunction because
// each half alone has a false-positive shape: a 401 confined to the providers
// endpoint says nothing about the rest of the subscription, and a
// resource-group list that did not succeed is routine on a subscription with a
// narrow role. It does NOT discriminate a shared, transient 401: the two calls
// share one token for the whole scan (newCachingCredential memoises per scope
// with no invalidation, under an azcore policy whose Expire() reaches only its
// own copy), so that case refuses both and a retry cannot help.
//
// The errors are drained from a pager rather than hand-built for the same
// reason as the test above.
//
// Coverage boundary, stated because an enumeration of what IS covered reads as
// complete: this pins the PREDICATE,
// TestProbeError_DiscriminatesAuthenticationFromAuthorization pins the message
// it produces, and TestResourceGroupsFromPager_ListedTracksTheCallNotTheError
// pins how rgListed is set. What no test reaches is the WIRING in scanSubscription -- that the gate
// runs after preWG.Wait, that it returns rather than continuing, and that rgErr
// is still reported when it fires. Covering it needs an ARM fake and a token
// stub behind azClientOptions; until then that arm is read, not tested.
func TestSubscriptionUnreachable_RequiresBothSignals(t *testing.T) {
	probeErr := func(t *testing.T, status int, code string) error {
		t.Helper()
		server := armresourcesfake.ProvidersServer{
			NewListPager: func(_ *armresources.ProvidersClientListOptions) azfake.PagerResponder[armresources.ProvidersClientListResponse] {
				r := azfake.PagerResponder[armresources.ProvidersClientListResponse]{}
				r.AddResponseError(status, code)
				return r
			},
		}
		client, err := armresources.NewProvidersClient(testSubID, fakeCred(),
			fakeClientOptions(t, armresourcesfake.NewProvidersServerTransport(&server)))
		if err != nil {
			t.Fatalf("NewProvidersClient: %v", err)
		}
		_, perr := registeredProvidersFromPager(t.Context(), client.NewListPager(nil))
		if perr == nil {
			t.Fatalf("expected the probe to fail with %d", status)
		}
		return perr
	}

	cases := []struct {
		name     string
		probe    func(*testing.T) error
		rgListed bool
		want     bool
	}{
		{
			name: "401 and the resource-group list also refused",
			probe: func(t *testing.T) error {
				return probeErr(t, http.StatusUnauthorized, "InvalidAuthenticationTokenTenant")
			},
			rgListed: false,
			want:     true,
		},
		{
			name: "401 but the resource-group list succeeded",
			probe: func(t *testing.T) error {
				return probeErr(t, http.StatusUnauthorized, "InvalidAuthenticationTokenTenant")
			},
			rgListed: true,
			want:     false,
		},
		{
			name:     "403 with nothing listed is a narrow role, not an unreachable subscription",
			probe:    func(t *testing.T) error { return probeErr(t, http.StatusForbidden, "AuthorizationFailed") },
			rgListed: false,
			want:     false,
		},
		{
			// Not a branch of the conjunction -- it is what lets the
			// conjunction have only two arms. errors.As over a nil error is
			// false, so isAuthenticationFailure answers false and no separate
			// probeErr != nil guard is needed. That guard was written first and
			// removed once this leg proved it dead; without the leg, restoring
			// it looks like prudence.
			name:     "no probe failure at all",
			probe:    func(*testing.T) error { return nil },
			rgListed: false,
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subscriptionUnreachable(tc.probe(t), tc.rgListed); got != tc.want {
				t.Errorf("subscriptionUnreachable(rgListed=%v) = %v; want %v", tc.rgListed, got, tc.want)
			}
		})
	}
}

// TestAuthCodeSelection_IsSharedByBothPaths pins that the shared formatter and
// the subscription gate pick the SAME diagnostic code for the same error.
//
// They did not. redactCredentialError preferred an AADSTS code found in the
// body over ARM's own code -- InvalidAuthenticationToken is generic and
// AADSTS700213 names the actual fault -- and unreachableSubscriptionError took
// respErr.ErrorCode alone. So an ARM 401 with an empty ErrorCode and an AADSTS
// token in the body rendered the specific code through the formatter and the
// bare "Unauthorized" literal through the gate, which is the ONE message
// written about a refused subscription. A comment two lines from the divergence
// asserted the two paths agreed.
//
// It also costs the cross-repo classification: disco-saas matches "AADSTS" as
// a deny signal and matches nothing in "Unauthorized", so the same incident
// labelled the account principal_denied through one path and failed through the
// other.
func TestAuthCodeSelection_IsSharedByBothPaths(t *testing.T) {
	const issuer = "https://sts.windows.net/581de929-1111-2222-3333-444455556666/"
	cases := []struct {
		name     string
		code     string
		body     string
		wantCode string
	}{
		{
			name:     "AADSTS in the body outranks ARM's generic code",
			code:     "InvalidAuthenticationToken",
			body:     `{"error":{"message":"AADSTS700213: No matching federated identity record found. Issuer ` + issuer + `"}}`,
			wantCode: "AADSTS700213",
		},
		{
			name:     "AADSTS in the body when ARM named no code at all",
			code:     "",
			body:     `{"error":{"message":"AADSTS700213: No matching federated identity record found. Issuer ` + issuer + `"}}`,
			wantCode: "AADSTS700213",
		},
		{
			name:     "no AADSTS and no ARM code falls back to the status literal",
			code:     "",
			body:     `{"error":{"message":"The access token is from the wrong issuer ` + issuer + `"}}`,
			wantCode: "Unauthorized",
		},
		{
			name:     "ARM's code stands when the body carries no AADSTS",
			code:     "InvalidAuthenticationTokenTenant",
			body:     `{"error":{"message":"The access token is from the wrong issuer ` + issuer + `"}}`,
			wantCode: "InvalidAuthenticationTokenTenant",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mk := func() *azcore.ResponseError {
				return &azcore.ResponseError{
					ErrorCode:  tc.code,
					StatusCode: http.StatusUnauthorized,
					RawResponse: &http.Response{
						StatusCode: http.StatusUnauthorized,
						Body:       io.NopCloser(strings.NewReader(tc.body)),
					},
				}
			}
			formatted := formatAzureError(mk())
			// scanSubscription returns this error and azure_scanner.go renders
			// it with formatAzureError, so THAT composition is the production
			// expression -- calling unreachableSubscriptionError alone cannot
			// see the shared formatter eating its sentence.
			gate := formatAzureError(unreachableSubscriptionError(mk()))
			if !strings.Contains(gate, "subscription unreachable") {
				t.Errorf("the gate sentence did not survive formatAzureError:\n  %q", gate)
			}

			if want := "(" + tc.wantCode + ")"; !strings.Contains(formatted, want) {
				t.Errorf("formatAzureError() = %q; want the code %q", formatted, want)
			}
			if want := "(401 " + tc.wantCode + ")"; !strings.Contains(gate, want) {
				t.Errorf("unreachableSubscriptionError() = %q; want the code %q", gate, want)
			}
			// The point of sharing: neither may carry the body's identifiers.
			for _, s := range []string{formatted, gate} {
				for _, leak := range []string{"sts.windows.net", "581de929", "issuer"} {
					if strings.Contains(s, leak) {
						t.Errorf("%q leaks %q onto the customer's scan record", s, leak)
					}
				}
			}
		})
	}
}

// TestBoundedCode_CapsAndFiltersRemoteText pins that an ARM error code is
// treated as remote text rather than as a fixed vocabulary.
//
// azcore takes ResponseError.ErrorCode from the x-ms-error-code RESPONSE HEADER
// when present and from the body's `code` field otherwise, so its length and
// contents are the remote's choice. Both new 401 messages interpolate it into a
// string stored on the customer's scan record, and nothing else bounds it:
// formatAzureError's *ResponseError arms render ErrorCode and the ARM message
// verbatim, and nothing downstream caps them.
func TestBoundedCode_CapsAndFiltersRemoteText(t *testing.T) {
	if got := boundedCode("InvalidAuthenticationTokenTenant"); got != "InvalidAuthenticationTokenTenant" {
		t.Errorf("boundedCode(real code) = %q; want it unchanged", got)
	}
	// Pins the cap EXACTLY, in both directions: a loose upper bound survives a
	// change to any smaller max, which is the opposite of what a cap needs.
	if got, want := boundedCode(strings.Repeat("A", 64)), strings.Repeat("A", 64); got != want {
		t.Errorf("boundedCode(64 chars) = %q; want it untouched at the cap", got)
	}
	if got, want := boundedCode(strings.Repeat("A", 65)), strings.Repeat("A", 64)+"\u2026"; got != want {
		t.Errorf("boundedCode(65 chars) = %q; want %q", got, want)
	}
	if got := boundedCode("Bad\nCode\x00Here"); strings.ContainsAny(got, "\n\x00") {
		t.Errorf("boundedCode() = %q; want control characters replaced", got)
	}
}

// TestCredentialLogKey_IsPerSubscriptionNotPerRequestPath pins what bounds the
// stderr copy of a redacted 401.
//
// Two comments in this package claimed the key was "a property of the
// SUBSCRIPTION" while it was a bounded PREFIX of the message. ResponseError's
// first line is `METHOD scheme://host EscapedPath`, so a 120-byte window runs
// past the subscription GUID and into the resource-provider namespace: the key
// was per REQUEST PATH. That was survivable while only an AADSTS-bearing 401
// reached logRawCredentialError; the wider 401 net means every ARM 401 does, so
// a subscription whose resource-group list succeeds and whose every service
// 401s printed one stderr line per distinct ARM path.
func TestCredentialLogKey_IsPerSubscriptionNotPerRequestPath(t *testing.T) {
	const sub = "/subscriptions/fd8a0713-121d-4f03-8845-dd6b971c94a8"
	line := func(rp string) string {
		return "GET https://management.azure.com" + sub + rp +
			"\n--------------------------------------------------------------------------------\n" +
			"RESPONSE 401: 401 Unauthorized\nERROR CODE: InvalidAuthenticationTokenTenant\n"
	}
	a := credentialLogKey("InvalidAuthenticationTokenTenant", line("/providers?api-version=2021-04-01"))
	b := credentialLogKey("InvalidAuthenticationTokenTenant", line("/providers/Microsoft.Compute/virtualMachines?api-version=2024-07-01"))
	if a != b {
		t.Errorf("two request paths under one subscription gave different keys:\n  %q\n  %q", a, b)
	}

	other := credentialLogKey("InvalidAuthenticationTokenTenant",
		"GET https://management.azure.com/subscriptions/11111111-2222-3333-4444-555555555555/providers?api-version=2021-04-01\n")
	if a == other {
		t.Errorf("two subscriptions collapsed onto one key %q; the second one's cause would never be printed", a)
	}

	// A credential failure raised before any request is built names no
	// subscription; it must still dedupe rather than key on nothing.
	noSub := "azure wif: assume role: operation error STS: AssumeRole, https response error StatusCode: 403"
	if got := credentialLogKey("", noSub); got == "|" || got == "" {
		t.Errorf("credentialLogKey(no subscription) = %q; want the bounded-prefix fallback", got)
	}
}

// TestAuthPredicates_SurviveATypedNilResponseError pins the guard that makes
// errors.As safe here.
//
// errors.As MATCHES a typed nil and SETS the target to it, returning true — so
// `if errors.As(err, &respErr)` is not a nil check, and every field read after
// it dereferences nil for a wrapped (*azcore.ResponseError)(nil). The gate
// reaches all three of these from scanSubscription's probe error, and
// reportPanic's recover does not cover them: they run outside it.
func TestAuthPredicates_SurviveATypedNilResponseError(t *testing.T) {
	nilResp := (*azcore.ResponseError)(nil)
	for _, tc := range []struct {
		name string
		err  error
	}{
		// Wrapped only, and NOT because a bare typed nil is unreachable -- it is
		// reachable, from reportPanic's r.(error), and
		// TestReportPanic_SurvivesAPanickedTypedNil panics exactly that value.
		// It is out of scope HERE because the guards below only MOVE that panic:
		// every predicate ends in a bare err.Error(), and ResponseError.Error()
		// dereferences its receiver, so the bare case is caught by reportPanic's
		// inner recover instead. Wrapping is what makes errors.As match while
		// err.Error() stays safe: fmt formats the operand at Errorf time,
		// recovering the panic, so the wrapper holds a plain string.
		{"wrapped typed nil", fmt.Errorf("armresources:Providers.List: %w", nilResp)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each must ANSWER rather than panic. A panic here fails the test by
			// crashing the subtest, which is the whole point.
			if isAuthenticationFailure(tc.err) {
				t.Errorf("isAuthenticationFailure(typed nil) = true; a nil response is not a 401")
			}
			if subscriptionUnreachable(tc.err, false) {
				t.Errorf("subscriptionUnreachable(typed nil) = true; want no refusal on a nil response")
			}
			if got := redactCredentialError(tc.err); strings.Contains(got, "token rejected") {
				t.Errorf("redactCredentialError(typed nil) = %q; want it not to claim a 401", got)
			}
			_ = unreachableSubscriptionError(tc.err)
			_ = formatAzureError(tc.err)
		})
	}
}

// TestRedactCredentialError_BoundsTheAADSTSMatch pins the twin of boundedCode's
// own case, one branch below it.
//
// `AADSTS\d+` has no length limit and the match is spliced straight into a
// scan-record message. boundedCode was added for the ARM code and did not cover
// this, which is the older of the two remote-text splices in the package.
func TestRedactCredentialError_BoundsTheAADSTSMatch(t *testing.T) {
	huge := errors.New("AADSTS" + strings.Repeat("7", 5000) + ": federated credential mismatch")
	got := redactCredentialError(huge)
	if got == "" {
		t.Fatalf("redactCredentialError() = %q; want it to recognise the AADSTS code", got)
	}
	if r := []rune(got); len(r) > 140 {
		t.Errorf("redactCredentialError() kept %d runes; want the AADSTS match bounded like the ARM code", len(r))
	}
}
