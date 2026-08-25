package azure

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/icearp/disco-cli/store"
)

// customerDirectory and discoDirectory stand for the two directories the whole
// Graph gate exists to keep apart: the one a customer consented and the one
// disco's own credential authenticates in.
// Both carry hex LETTERS on purpose. An all-digit GUID makes strings.ToUpper a
// no-op, which silently turns any case-folding assertion into a tautology --
// measured: the accept-either-case test below passed against a plain ==.
const (
	customerDirectory = "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
	discoDirectory    = "9f8e7d6c-5b4a-4b9a-8271-6f5e4d3c2b1a"
)

// federatedNoGraph is the Lighthouse shape before any consent: the WIF
// contract is set, so every tenant-scope service is suppressed.
//
// Derived from federatedCfg rather than spelled out again — two fixtures for
// "the minimum contract that turns federation on" is the keep-these-in-step
// shape, and the one that drifts would silently stop being federated.
func federatedNoGraphCfg() wifConfig {
	c := federatedCfg()
	c.tenantID = discoDirectory
	return c
}

// federatedWithGraphCfg adds a consented customer directory.
func federatedWithGraphCfg() wifConfig {
	c := federatedNoGraphCfg()
	c.graphTenantID = customerDirectory
	return c
}

var (
	federatedNoGraph   = federatedNoGraphCfg()
	federatedWithGraph = federatedWithGraphCfg()
)

// recordingCred captures the TokenRequestOptions of every acquisition so a
// test can assert WHICH directory a token was asked for — the property the
// whole threading exists to establish, and one no response body reveals.
type recordingCred struct {
	mu   sync.Mutex
	opts []policy.TokenRequestOptions
	tok  string
	err  error
	// okCalls caps how many acquisitions succeed before every later one
	// errors. Zero means no cap. This is what keeps a test that exercises the
	// ACCEPT path off the network: scanEntra reads the tid from its first
	// token and then builds a real graphClient hard-wired to
	// https://graph.microsoft.com, so a credential that keeps succeeding sends
	// live requests carrying a forged bearer. One success proves acceptance;
	// the failure after it stops the scan at the next call.
	okCalls int
}

func (c *recordingCred) GetToken(_ context.Context, o policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.mu.Lock()
	c.opts = append(c.opts, o)
	n := len(c.opts)
	c.mu.Unlock()
	if c.err != nil {
		return azcore.AccessToken{}, c.err
	}
	if c.okCalls > 0 && n > c.okCalls {
		return azcore.AccessToken{}, errors.New("no further tokens: this test must not reach the network")
	}
	return azcore.AccessToken{Token: c.tok}, nil
}

func (c *recordingCred) requested() []policy.TokenRequestOptions {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]policy.TokenRequestOptions(nil), c.opts...)
}

// jwtWithTid builds an unsigned JWT carrying one tid claim. tenantIDFromJWT
// reads the payload without verifying, so an unsigned token is enough.
func jwtWithTid(tid string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(`{"tid":"`+tid+`"}`)) + ".s"
}

func TestGraphTenantEnabled(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  wifConfig
		want bool
	}{
		{"unfederated, even with a directory named", wifConfig{graphTenantID: customerDirectory}, false},
		{"federated, no directory", federatedNoGraph, false},
		{"federated, consented directory", federatedWithGraph, true},
		{"federated, uppercase GUID", wifConfig{clientID: "c", tenantID: "t", graphTenantID: strings.ToUpper(customerDirectory)}, true},
		{"federated, not a GUID", wifConfig{clientID: "c", tenantID: "t", graphTenantID: "contoso.onmicrosoft.com"}, false},
		{"federated, multi-tenant alias", wifConfig{clientID: "c", tenantID: "t", graphTenantID: "organizations"}, false},
		{"federated, common alias", wifConfig{clientID: "c", tenantID: "t", graphTenantID: "common"}, false},
		{"federated, GUID with padding", wifConfig{clientID: "c", tenantID: "t", graphTenantID: " " + customerDirectory}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.graphTenantEnabled(); got != tc.want {
				t.Errorf("graphTenantEnabled() = %v; want %v", got, tc.want)
			}
		})
	}
}

// TestCredentialOptions_AllowsOnlyTheConsentedDirectory pins the half of the
// threading that lives on the credential. Without an allow list azidentity
// refuses a request naming any tenant but the credential's own, so this is
// what makes the per-request TenantID work at all — and "*" here would let a
// caller mint a token for every directory that ever deployed the Lighthouse
// offer.
func TestCredentialOptions_AllowsOnlyTheConsentedDirectory(t *testing.T) {
	if got := credentialOptions(federatedNoGraph); got != nil {
		t.Errorf("credentialOptions with no consented directory = %+v; want nil", got)
	}
	got := credentialOptions(federatedWithGraph)
	if got == nil {
		t.Fatal("credentialOptions with a consented directory = nil; want an allow list")
	}
	// Exact set equality, which subsumes the wildcard case: ["*"] has length
	// one and is not the consented directory, so a separate check for it could
	// never fire without this one firing first.
	if len(got.AdditionallyAllowedTenants) != 1 || got.AdditionallyAllowedTenants[0] != customerDirectory {
		t.Errorf("AdditionallyAllowedTenants = %q; want exactly [%q] — never the wildcard, which would reach every directory this application has a service principal in", got.AdditionallyAllowedTenants, customerDirectory)
	}
}

// TestTenantServiceRunnable_GraphConsentUngatesGraphAlone is the disclosure
// guard. A consented customer directory redirects GRAPH and must never
// re-enable a tenant-root ARM phase, whose calls answer about the credential's
// own directory whatever a token names — that is the cross-customer
// disclosure the federation gate was built for, and widening the gate is the
// obvious way to reintroduce it.
func TestTenantServiceRunnable_GraphConsentUngatesGraphAlone(t *testing.T) {
	graph := tenantServiceEntry{name: "azure:microsoft.entra", graphScoped: true}
	arm := tenantServiceEntry{name: "azure:microsoft.management"}
	dedup := tenantServiceEntry{name: "azure:microsoft.authorization", dedupOnly: true}

	for _, tc := range []struct {
		name                          string
		cfg                           wifConfig
		wantGraph, wantARM, wantDedup bool
	}{
		{"unfederated runs everything", wifConfig{}, true, true, true},
		{"federated with no consent runs nothing", federatedNoGraph, false, false, false},
		{"federated with consent runs graph only", federatedWithGraph, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tenantServiceRunnable(graph, tc.cfg); got != tc.wantGraph {
				t.Errorf("graph service runnable = %v; want %v", got, tc.wantGraph)
			}
			if got := tenantServiceRunnable(arm, tc.cfg); got != tc.wantARM {
				t.Errorf("tenant-root ARM service runnable = %v; want %v", got, tc.wantARM)
			}
			if got := tenantServiceRunnable(dedup, tc.cfg); got != tc.wantDedup {
				t.Errorf("dedup-only service runnable = %v; want %v", got, tc.wantDedup)
			}
		})
	}
}

// TestRegisteredTenantServices_OnlyGraphIsRedirectable pins the redirectable
// set to exactly one entry, read from the live registry rather than a
// hand-built one.
//
// It cannot check that a graphScoped service is REALLY reachable over Graph —
// no assertion can — and it fires identically for a correctly added second
// Graph service. That is the point: widening this set is a decision to make
// deliberately, because every member is a tenant-scope phase that a federated
// scan will run.
func TestRegisteredTenantServices_OnlyGraphIsRedirectable(t *testing.T) {
	var scoped []string
	for _, svc := range registeredTenantServices {
		if svc.graphScoped {
			scoped = append(scoped, svc.name)
		}
	}
	if len(scoped) != 1 || scoped[0] != "azure:microsoft.entra" {
		t.Errorf("graphScoped services = %q; want exactly [azure:microsoft.entra] — a tenant-root ARM phase cannot be redirected by a token", scoped)
	}
}

// TestGraphClient_TokenNamesTheConfiguredDirectory pins the per-request half.
// Asserted at the token REQUEST, because nothing in a Graph response says
// which directory the token was for.
func TestGraphClient_TokenNamesTheConfiguredDirectory(t *testing.T) {
	for _, tc := range []struct{ name, tenantID, want string }{
		{"unfederated asks for no particular directory", "", ""},
		{"consented directory is named", customerDirectory, customerDirectory},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cred := &recordingCred{err: errors.New("stop before the call")}
			g := newGraphClient(cred, tc.tenantID)
			_ = g.get(context.Background(), "https://example.invalid/v1.0/users", nil)

			reqs := cred.requested()
			if len(reqs) != 1 {
				t.Fatalf("token acquisitions = %d; want 1", len(reqs))
			}
			if reqs[0].TenantID != tc.want {
				t.Errorf("TokenRequestOptions.TenantID = %q; want %q", reqs[0].TenantID, tc.want)
			}
			if len(reqs[0].Scopes) != 1 || reqs[0].Scopes[0] != graphScope {
				t.Errorf("Scopes = %q; want [%q]", reqs[0].Scopes, graphScope)
			}
		})
	}
}

// TestScanEntra_RefusesADirectoryTheTokenDoesNotName is the positive signal the
// gate never had. The env var says which directory was consented; the tid says
// which one answered. They can only disagree if something upstream is wrong,
// and proceeding would file one directory's identities under another's account
// id in an append-only inventory.
func TestScanEntra_RefusesADirectoryTheTokenDoesNotName(t *testing.T) {
	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	st := recordingStore(&svcs, &notices, &warnings)

	cred := &recordingCred{tok: jwtWithTid(discoDirectory)}
	total, inserted, err := scanEntra(context.Background(), []subscription{{ID: "sub"}}, cred, federatedWithGraph, st, "scan-id")
	if err != nil {
		t.Fatalf("scanEntra returned err = %v; want nil (a refusal is a warning, not a scan failure)", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("scanEntra stored total=%d inserted=%d; want 0/0 — a mismatched directory must write nothing", total, inserted)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "different directory") {
		t.Fatalf("warnings = %+v; want one naming the directory mismatch", warnings)
	}
	// The refusal must come from the tid, not from failing to ask: the request
	// still has to name the consented directory, or this passes for the wrong
	// reason.
	reqs := cred.requested()
	if len(reqs) != 1 || reqs[0].TenantID != customerDirectory {
		t.Errorf("token requests = %+v; want one naming %q", reqs, customerDirectory)
	}
}

// TestScanEntra_UnfederatedAsksForNoParticularDirectory keeps the standalone
// CLI unchanged: an operator's own credential already authenticates in the
// directory they mean to scan, so naming one would be wrong.
func TestScanEntra_UnfederatedAsksForNoParticularDirectory(t *testing.T) {
	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	st := recordingStore(&svcs, &notices, &warnings)

	cred := &recordingCred{err: errors.New("stop before any graph call")}
	if _, _, err := scanEntra(context.Background(), []subscription{{ID: "sub"}}, cred, wifConfig{}, st, "scan-id"); err != nil {
		t.Fatalf("scanEntra returned err = %v; want nil", err)
	}
	reqs := cred.requested()
	if len(reqs) != 1 {
		t.Fatalf("token acquisitions = %d; want 1", len(reqs))
	}
	if reqs[0].TenantID != "" {
		t.Errorf("TokenRequestOptions.TenantID = %q; want empty for an unfederated scan", reqs[0].TenantID)
	}
}

// TestReportTenantScopeSkipped_StillAccountsForTheARMServicesUnderGraphConsent
// covers the half a consent must NOT silence. Once the Graph phase runs, the
// tenant-root ARM phases are still suppressed and an operator must still be
// told so — an if/else that reported skips only when the whole phase was off
// would drop these notices exactly when the scan looks most complete.
func TestReportTenantScopeSkipped_StillAccountsForTheARMServicesUnderGraphConsent(t *testing.T) {
	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	reportTenantScopeSkipped(recordingStore(&svcs, &notices, &warnings), []subscription{{ID: "sub"}}, nil, federatedWithGraph)

	for _, n := range notices {
		if n.Service == "azure:microsoft.entra" {
			t.Errorf("reported the Graph phase as skipped while it was running: %+v", n)
		}
	}
	// Asserted as a SET, not a count: every non-Graph tenant service is
	// accounted for and nothing else is. A count would be a number over a live
	// registry, wrong the day a tenant service is added.
	noticed := map[string]bool{}
	for _, n := range notices {
		noticed[n.Service] = true
	}
	for _, svc := range registeredTenantServices {
		if svc.graphScoped {
			continue
		}
		if !noticed[svc.name] {
			t.Errorf("suppressed service %q got no notice: an operator cannot tell it was skipped from a tenant that genuinely has none", svc.name)
		}
		delete(noticed, svc.name)
	}
	for name := range noticed {
		t.Errorf("service %q was reported skipped while it ran", name)
	}

	// The phase warning survives a consent and must not over-report. Checked at
	// the SERVICE ID, which is what the skipped list is built from. In THIS
	// state the warning mentions Entra ID nowhere at all: skippedGraph is
	// false, so graphTenantAdvice is never called, and the ARM clause names
	// only ARM. An earlier version of this comment described the message as
	// still carrying Entra prose in general terms, which no state renders.
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v; want exactly one for the phase", warnings)
	}
	if strings.Contains(warnings[0].Message, "azure:microsoft.entra") {
		t.Errorf("the phase warning names the Graph service, which ran: %q", warnings[0].Message)
	}
	if !strings.Contains(warnings[0].Message, "azure:microsoft.management") {
		t.Errorf("the phase warning omits the suppressed tenant-root ARM service: %q", warnings[0].Message)
	}
}

// TestRunTenantServices_RunsOnlyWhatItAdmits is the disclosure guard on the
// half that PREVENTS, as opposed to the half that reports.
//
// The skip loop in reportTenantScopeSkipped is pinned above, and a passing
// suite there says nothing about this one: deleting the tenantServiceRunnable
// check from runTenantServices lets a Lighthouse scan call the tenant-root ARM
// services against disco's own directory, with every other test still green.
//
// Observed at the TOKEN, following refusingCredential's reasoning: a call that
// never happens leaves nothing in the store, but every Azure client asks for a
// token before its first request, and the two halves ask for DIFFERENT
// audiences. So "an ARM token was requested" is exactly "a tenant-root ARM
// service ran", which no assertion over rows could say.
func TestRunTenantServices_RunsOnlyWhatItAdmits(t *testing.T) {
	subs := []subscription{{ID: "sub"}}

	t.Run("federated with no consented directory runs nothing", func(t *testing.T) {
		var svcs []serviceReport
		var notices []store.ScanNotice
		var warnings []store.ScanWarning
		st := recordingStore(&svcs, &notices, &warnings)
		runTenantServices(t.Context(), subs, refusingCredential{t: t}, federatedNoGraph, nil, st, "scan-id")
	})

	t.Run("federated with a consented directory runs graph and only graph", func(t *testing.T) {
		var svcs []serviceReport
		var notices []store.ScanNotice
		var warnings []store.ScanWarning
		st := recordingStore(&svcs, &notices, &warnings)

		cred := &recordingCred{err: errors.New("stop before any call")}
		runTenantServices(t.Context(), subs, cred, federatedWithGraph, nil, st, "scan-id")

		var graphAsks, armAsks int
		for _, o := range cred.requested() {
			for _, sc := range o.Scopes {
				switch sc {
				case graphScope:
					graphAsks++
					if o.TenantID != customerDirectory {
						t.Errorf("a Graph token was requested for %q; want the consented directory %q", o.TenantID, customerDirectory)
					}
				case armScope:
					armAsks++
				}
			}
		}
		if armAsks != 0 {
			t.Errorf("%d Azure Resource Manager tokens were requested: a tenant-root ARM service ran under a credential whose directory is disco's, which is the cross-customer disclosure the gate exists to prevent", armAsks)
		}
		if graphAsks == 0 {
			t.Error("no Graph token was requested: the consented directory did not actually re-enable the Entra services, so this test would pass against a gate that admits nothing")
		}
	})
}

// TestScanEntra_PinsOnlyWhatTheGateAdmitted covers the case where the variable
// is set and the gate says no — a non-GUID, or an unfederated run. scanEntra
// re-derives the pin from wifConfig rather than being handed it, so reading
// graphTenantID directly would aim a Lighthouse-shaped scan at a directory the
// credential's allow list does not carry, and aim a LOCAL scan at a directory
// the operator never consented to.
//
// DEFENCE IN DEPTH for the first case, not a reachable state: a wifConfig with
// graphTenantID set and no clientID/tenantID is partiallyConfigured, which the
// startup path REFUSES with ErrIncompleteWIFConfig, so no scan reaches
// scanEntra holding it. It is pinned anyway because the gate is what keeps the
// refusal from being the only thing standing between a local run and disco's
// own directory — but do not read this subtest as evidence the shape occurs.
func TestScanEntra_PinsOnlyWhatTheGateAdmitted(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  wifConfig
	}{
		{"unfederated, with a directory named anyway", wifConfig{graphTenantID: customerDirectory}},
		{"federated, value is not a GUID", wifConfig{clientID: "c", tenantID: discoDirectory, graphTenantID: "contoso.onmicrosoft.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var svcs []serviceReport
			var notices []store.ScanNotice
			var warnings []store.ScanWarning
			st := recordingStore(&svcs, &notices, &warnings)

			cred := &recordingCred{err: errors.New("stop before any call")}
			if _, _, err := scanEntra(t.Context(), []subscription{{ID: "sub"}}, cred, tc.cfg, st, "scan-id"); err != nil {
				t.Fatalf("scanEntra returned err = %v; want nil", err)
			}
			reqs := cred.requested()
			if len(reqs) != 1 {
				t.Fatalf("token acquisitions = %d; want 1", len(reqs))
			}
			if reqs[0].TenantID != "" {
				t.Errorf("TokenRequestOptions.TenantID = %q; want empty — the gate refused this configuration, so no directory may be named", reqs[0].TenantID)
			}
		})
	}
}

// TestScanEntra_AcceptsTheConsentedDirectoryInEitherCase pins the ACCEPT
// direction of the tid comparison, which a mismatch test cannot see.
//
// graphTenantGUID admits an uppercase GUID and Entra returns tid lowercase, so
// folding is load-bearing: under a plain == the two would differ and the Entra
// phase would refuse itself entirely, on a correct configuration, reporting a
// directory mismatch that did not happen.
func TestScanEntra_AcceptsTheConsentedDirectoryInEitherCase(t *testing.T) {
	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	st := recordingStore(&svcs, &notices, &warnings)

	cfg := federatedWithGraph
	cfg.graphTenantID = strings.ToUpper(customerDirectory)
	// okCalls: 1 — scanEntra reads the tid from the first token, and every
	// acquisition after that fails, so the Graph client built on acceptance
	// stops at its first call instead of issuing live requests to
	// graph.microsoft.com with a forged bearer.
	cred := &recordingCred{tok: jwtWithTid(customerDirectory), okCalls: 1}

	_, _, _ = scanEntra(t.Context(), []subscription{{ID: "sub"}}, cred, cfg, st, "scan-id")

	for _, w := range warnings {
		if strings.Contains(w.Message, "different directory") {
			t.Fatalf("refused a token for the configured directory over letter case alone: %q", w.Message)
		}
	}
	// Asserted POSITIVELY as well: the absence of a refusal is also what a
	// scanEntra that never compared anything would produce, so the negative
	// alone would stay green against a deleted comparison. Reaching a SECOND
	// acquisition is what proves the first was accepted rather than bailed on,
	// and both must still name the consented directory.
	reqs := cred.requested()
	if len(reqs) < 2 {
		t.Fatalf("token acquisitions = %d; want at least 2 — the second is the Graph client built after acceptance, and without it this passes against a scanEntra that refused for some other reason", len(reqs))
	}
	for i, o := range reqs {
		if !strings.EqualFold(o.TenantID, customerDirectory) {
			t.Errorf("token %d was requested for %q; want the consented directory %q", i, o.TenantID, customerDirectory)
		}
	}
}

func TestSameGraphHost(t *testing.T) {
	const base = "https://graph.microsoft.com/v1.0"
	for _, tc := range []struct {
		name, next string
		want       bool
	}{
		{"the same host", base + "/users?$skiptoken=x", true},
		{"a lookalike suffix", "https://graph.microsoft.com.evil.example/v1.0/users", false},
		{"a different host entirely", "https://evil.example/v1.0/users", false},
		{"downgraded to http on the right host", "http://graph.microsoft.com/v1.0/users", false},
		{"a relative link", "/v1.0/users?$skiptoken=x", false},
		{"host case differs", "https://GRAPH.microsoft.com/v1.0/users", true},
		{"credentials in the userinfo", "https://graph.microsoft.com@evil.example/v1.0/users", false},
		// The origin includes the PORT, so an explicitly :443-qualified link
		// against a bare-host base is refused. Graph does not emit one; if it
		// ever did, a paginated Entra scan would abort rather than leak, which
		// is the direction to fail in.
		{"explicit default port against a bare-host base", "https://graph.microsoft.com:443/v1.0/users", false},
		// url.Parse passes bytes >= 0x80 into Host unvalidated, and
		// strings.EqualFold applies UNICODE folding where U+017F folds to 's'.
		// A plain EqualFold answers TRUE here.
		{"a non-ASCII homoglyph host", "https://graph.microſoft.com/v1.0/users", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameGraphHost(base, tc.next); got != tc.want {
				t.Errorf("sameGraphHost(%q, %q) = %v; want %v", base, tc.next, got, tc.want)
			}
		})
	}
}

// TestIterateGraph_RefusesAPaginationLinkToAnotherHost pins the call site of
// sameGraphHost, which its own unit test cannot: iterateGraph could stop
// consulting it and every other test would stay green.
//
// The link is followed by ISSUING A REQUEST, not by a redirect, so net/http's
// rule about dropping Authorization across hosts never applies — the bearer
// would be presented to whatever host the response body named. Under a
// consented directory that bearer carries Directory.Read.All on a CUSTOMER's
// tenant.
func TestIterateGraph_RefusesAPaginationLinkToAnotherHost(t *testing.T) {
	// atomic because the write is on the handler goroutine and the read is on
	// the test's. It closes no measured race — on the failing path the handler's
	// Add happens-before the client finishes reading the response, which
	// happens-before iterateGraph returns — and it kills no mutant. Kept because
	// the alternative is a plain int whose safety depends on that chain holding
	// for whatever a future fixture does.
	var elsewhereHits atomic.Int64
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhereHits.Add(1)
		_, _ = w.Write([]byte(`{"value":[]}`))
	}))
	defer elsewhere.Close()

	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"value":[{"id":"u1"}],"@odata.nextLink":"` + elsewhere.URL + `/users-page2"}`))
	}))
	defer home.Close()

	g := &graphClient{cred: stubTokenIssuer{token: "x"}, http: home.Client(), baseURL: home.URL}
	var seen int
	err := iterateGraph(t.Context(), g, home.URL+"/users", func(userAttrs) bool { seen++; return true })

	if err == nil {
		t.Fatal("iterateGraph followed a pagination link off the configured origin and returned no error")
	}
	// By TYPE, never by message: the message is what a hostile link gets to
	// influence, and reportEntraErr classifies by substring over it.
	var fle *foreignLinkError
	if !errors.As(err, &fle) {
		t.Fatalf("err = %v (%T); want a *foreignLinkError so callers can classify it without reading the text", err, err)
	}
	// Note what this fixture actually differs by: both servers are 127.0.0.1,
	// so it is the PORT that makes the origins differ. That is the property
	// worth having — url.URL.Host carries the port, so an origin check
	// includes it — and the genuinely-different-host cases are covered by
	// TestSameGraphHost. It also means Graph emitting a :443-qualified link
	// against a bare-host base would be refused; Graph does not, and refusing
	// is the safe direction.
	if n := elsewhereHits.Load(); n != 0 {
		t.Errorf("the other origin was called %d times; the bearer must never leave the configured Graph origin", n)
	}
	// The refusal must come after the first page was consumed, not instead of
	// it: a guard that discarded rows already fetched would be a data-loss bug
	// wearing a security fix.
	if seen != 1 {
		t.Errorf("consumed %d entities from the first page; want 1", seen)
	}
}

// TestForeignLinkError_CannotSteerTheEntraClassifier is the half that makes the
// sentinel worth having.
//
// reportEntraErr classifies by SUBSTRING over err.Error(), and a hostile
// nextLink chooses the URL. Echoing it verbatim let an attacker write
// "Authorization_RequestDenied" into the path and demote the refusal to the
// routine missing-consent WARNING — the strongest evidence of a tampered Graph
// response, filed as a permission the customer simply has not granted.
func TestForeignLinkError_CannotSteerTheEntraClassifier(t *testing.T) {
	// The first three put the token in the PATH or QUERY, which hostOnly
	// strips. The rest put it in the HOST, which it cannot — measured:
	// url.Parse admits "Authorization_RequestDenied.evil.example" as a host
	// (shouldEscape permits alphanumerics, '-', '_', '.', '~'), and an IPv6
	// zone admits spaces (encodeZone exempts ' '), so "[fe80::1% 401]"
	// contains " 401". Restricting the message to the host is therefore not
	// what makes this safe. Classifying by type is necessary and, on its own,
	// still not sufficient: the last fixture steers formatAzureError instead,
	// whose scanBodyForAADSTS matches "AADSTS" in ANY error type, so a type
	// branch that reported formatAzureError's string filed the refusal as a
	// credential failure and dropped the host. That is why the assertions
	// below read the reported MESSAGE and not only the report's kind.
	for _, hostile := range []string{
		"https://evil.example/v1.0/Authorization_RequestDenied",
		"https://evil.example/v1.0/users?x=Insufficient%20privileges",
		"https://evil.example/graph:%20401",
		"https://Authorization_RequestDenied.evil.example/v1.0/users",
		"https://[fe80::1%25%20401]/v1.0/users",
		"https://AADSTS700016.evil.example/v1.0/users",
	} {
		t.Run(hostile, func(t *testing.T) {
			var svcs []serviceReport
			var notices []store.ScanNotice
			var warnings []store.ScanWarning
			st := recordingStore(&svcs, &notices, &warnings)
			var errs []store.ScanError
			st.OnError = func(e store.ScanError) { errs = append(errs, e) }
			keysBefore := credentialErrorKeys()

			reportEntraErr(st, "users", &foreignLinkError{host: hostOnly(hostile)})

			if len(warnings) != 0 {
				t.Errorf("a refused pagination link was reported as a WARNING: %+v — the attacker chose text the classifier matched, so this is now indistinguishable from a missing consent grant", warnings)
			}
			// Positive too, or a reportEntraErr that dropped the error
			// entirely would satisfy the assertion above.
			if len(errs) != 1 {
				t.Fatalf("errors reported = %d; want exactly 1 — the refusal must reach the scan record as an ERROR", len(errs))
			}
			// The KIND alone is not the whole contract. The host is the only
			// actionable fact in the report, and the message is what carries
			// it; a classifier that files the refusal correctly and then
			// substitutes someone else's text has taken it away.
			if host := hostOnly(hostile); !strings.Contains(errs[0].Message, host) {
				t.Errorf("the reported message does not name the host that was refused (%q): %q", host, errs[0].Message)
			}
			if strings.Contains(errs[0].Message, "authentication failed") {
				t.Errorf("a refused pagination link was reported as a CREDENTIAL failure: %q — the attacker put an AADSTS code in the host and formatAzureError rewrote the message", errs[0].Message)
			}
			// The ORDERING, which the message alone does not pin: computing
			// msg := formatAzureError(err) above the type test and still
			// reporting err.Error() passes every assertion above while
			// writing the attacker's host to stderr and leaving their chosen
			// key in this never-cleared map. Measured — that mutant survived
			// the message assertions.
			if k := credentialErrorKeys(); k != keysBefore {
				t.Errorf("loggedCredentialErrors grew from %d to %d keys: formatAzureError ran on a refused link, so an attacker chose a permanent map key and a line on stderr", keysBefore, k)
			}
		})
	}
}

// TestHostOnly_KeepsThePathOutOfTheMessage pins the other half: even the host
// is reported through a fixed prefix, and nothing from the path or query
// survives into the text a classifier reads.
func TestHostOnly_KeepsThePathOutOfTheMessage(t *testing.T) {
	msg := (&foreignLinkError{host: hostOnly("https://evil.example/v1.0/Authorization_RequestDenied?q=Insufficient+privileges")}).Error()
	for _, forbidden := range []string{"Authorization_RequestDenied", "Insufficient privileges", "Insufficient+privileges", "/v1.0/"} {
		if strings.Contains(msg, forbidden) {
			t.Errorf("the refusal message carries attacker-chosen text %q: %q", forbidden, msg)
		}
	}
	if !strings.Contains(msg, "evil.example") {
		t.Errorf("the refusal message names no host, so an operator cannot act on it: %q", msg)
	}
}

// TestHostOnly_SaysWhyThereIsNoHost pins the two placeholders.
//
// They are the whole report for a link that has no host to name, and they are
// different DIAGNOSES: a relative link is a Graph that answered oddly, an
// unparseable one is a link that is not a URL at all. Swapping them, or
// falling back to the raw string for either, is invisible to every other test
// here — the refusal is filed correctly in both cases, and only the text an
// operator reads changes.
func TestHostOnly_SaysWhyThereIsNoHost(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
	}{
		{"relative", "/v1.0/users?$skiptoken=x", "the link was relative"},
		{"unparseable", "https://exa mple.com/v1.0/users", "(unparseable)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := hostOnly(tc.raw)
			if !strings.Contains(got, tc.want) {
				t.Errorf("hostOnly(%q) = %q; want it to say %q", tc.raw, got, tc.want)
			}
			// And it must not fall back to the input, which is the whole
			// reason hostOnly exists.
			if strings.Contains(got, "v1.0") {
				t.Errorf("hostOnly(%q) = %q; the path survived into the message", tc.raw, got)
			}
		})
	}
}

// TestIterateGraph_RefusesARepeatedLink pins the exact-repeat diagnosis, NOT
// the loop's bound — that is runTenantServices' deadline, pinned by
// TestRunTenantServices_BoundsEachService.
//
// The distinction is the whole point, and this comment asserted the opposite
// for one round: a server incrementing $skiptoken yields a NEW URL every time
// and walks straight past this guard. What it buys is a named refusal for the
// shape that IS detectable, instead of a scan that looks healthy for thirty
// minutes and then times out. The fixture would hang without it, so it runs
// under a deadline that fails loudly instead.
func TestIterateGraph_RefusesARepeatedLink(t *testing.T) {
	var served atomic.Int64
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served.Add(1)
		fmt.Fprintf(w, `{"value":[{"id":"u"}],"@odata.nextLink":%q}`, srv.URL+"/v1.0/users")
	}))
	defer srv.Close()

	g := &graphClient{cred: &recordingCred{tok: jwtWithTid(customerDirectory)}, http: graphHTTPClient, baseURL: srv.URL}

	done := make(chan error, 1)
	go func() {
		done <- iterateGraph(context.Background(), g, srv.URL+"/v1.0/users", func(map[string]any) bool { return true })
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cycle of pagination links terminated without a refusal")
		}
		var repeated *repeatedLinkError
		if !errors.As(err, &repeated) {
			t.Errorf("err = %v (%T); want a *repeatedLinkError", err, err)
		}
		// The refusal is never a missing grant, and never a classifier's to
		// read: the link is the server's to choose.
		var svcs []serviceReport
		var notices []store.ScanNotice
		var warnings []store.ScanWarning
		st := recordingStore(&svcs, &notices, &warnings)
		var errs []store.ScanError
		st.OnError = func(e store.ScanError) { errs = append(errs, e) }
		reportEntraErr(st, "users", err)
		if len(warnings) != 0 {
			t.Errorf("a refused cycle was reported as a missing-consent WARNING: %+v", warnings)
		}
		if len(errs) != 1 {
			t.Errorf("errors reported = %d; want exactly 1", len(errs))
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("iterateGraph did not return within 10s; the server served %d pages and the loop is unbounded", served.Load())
	}
}

// TestGraphClient_RefusesARedirect is the hazard sameGraphHost does NOT cover.
//
// sameGraphHost reads the @odata.nextLink in a response BODY. A 3xx is the
// other way a Graph response sends the request elsewhere, and it was the worse
// one: measured on the unfixed client, a 302 to another origin had the foreign
// response DECODED and stored as the customer's directory objects, with the
// bearer forwarded to that origin — net/http compares only url.Hostname()
// when deciding whether to keep Authorization, so scheme and port are ignored.
// Both servers here are 127.0.0.1, which is exactly that case.
func TestGraphClient_RefusesARedirect(t *testing.T) {
	var elsewhereHits atomic.Int64
	var gotAuth atomic.Value
	gotAuth.Store("")
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhereHits.Add(1)
		gotAuth.Store(r.Header.Get("Authorization"))
		fmt.Fprint(w, `{"value":[{"id":"attacker-planted-user"}]}`)
	}))
	defer elsewhere.Close()

	// The redirect's own BODY is valid JSON carrying a planted row. Without
	// that, dropping the 3xx refusal merely degrades to a decode error on
	// http.Redirect's HTML and the test still passes — measured, that mutant
	// survived. A 3xx body is decodable if the server says so, and Do hands it
	// back under ErrUseLastResponse, so this is the shape that must be refused.
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", elsewhere.URL+"/v1.0/users")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusFound)
		fmt.Fprint(w, `{"value":[{"id":"planted-in-the-redirect-body"}]}`)
	}))
	defer graph.Close()

	g := &graphClient{cred: &recordingCred{tok: jwtWithTid(customerDirectory)}, http: graphHTTPClient, baseURL: graph.URL}
	var page graphPage[map[string]any]
	err := g.get(context.Background(), graph.URL+"/v1.0/users", &page)

	if err == nil {
		t.Fatalf("the redirect was followed; decoded %d rows from the other origin", len(page.Value))
	}
	if n := elsewhereHits.Load(); n != 0 {
		t.Errorf("the other origin was fetched %d times", n)
	}
	if a := gotAuth.Load().(string); a != "" {
		t.Errorf("the bearer was forwarded to the other origin: %q — under a consented directory that token carries Directory.Read.All on a CUSTOMER's tenant", a)
	}
	// Rows too, not just the fetch: a redirect served from a cache or a
	// racing fixture would still be a foreign body decoded as Graph's.
	if len(page.Value) != 0 {
		t.Errorf("decoded %d rows from a refused redirect: %+v", len(page.Value), page.Value)
	}

	// And it is an ERROR, never the missing-consent warning: no permission a
	// customer can grant makes a redirect go away.
	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	st := recordingStore(&svcs, &notices, &warnings)
	var errs []store.ScanError
	st.OnError = func(e store.ScanError) { errs = append(errs, e) }
	reportEntraErr(st, "users", err)
	if len(warnings) != 0 {
		t.Errorf("a refused redirect was reported as a missing-consent WARNING: %+v", warnings)
	}
	if len(errs) != 1 {
		t.Errorf("errors reported = %d; want exactly 1", len(errs))
	}
}

// TestRunTenantServices_BoundsEachService pins the deadline the tenant phase
// did not have.
//
// Scan blocks on wg.Wait(), so a tenant service that never returns hung the
// whole scan rather than losing its own results — and a Graph server naming a
// fresh @odata.nextLink forever is enough to do it. The repeated-link refusal
// in iterateGraph does NOT cover that: a varying $skiptoken is a new URL every
// time, so it names the exact repeat and nothing else.
//
// Asserted as "the callee was given a deadline", not by waiting one out:
// serviceTimeout is 30 minutes. That is the honest limit of this test — it
// cannot show the deadline is ENFORCED, only that the context carries one,
// which is what context.WithTimeout then guarantees.
//
// Must not run in parallel: it swaps registeredTenantServices, a package-level
// var every tenant-phase test reads.
func TestRunTenantServices_BoundsEachService(t *testing.T) {
	var gotDeadline bool
	var hadOne bool
	var left time.Duration
	// Checked from INSIDE the service, because after runTenantServices returns
	// the service context is cancelled by its own deferred cancel either way —
	// so cancelling the parent afterwards proves nothing about derivation.
	var inheritedCancel bool
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	restore := registeredTenantServices
	registeredTenantServices = []tenantServiceEntry{{
		name:        "azure:test.deadline",
		graphScoped: true,
		fn: func(ctx context.Context, _ []subscription, _ azcore.TokenCredential, _ wifConfig, _ *store.Store, _ string) (int, int, error) {
			var dl time.Time
			dl, hadOne = ctx.Deadline()
			if hadOne {
				left = time.Until(dl)
			}
			// context.WithTimeout(context.Background(), serviceTimeout) passes
			// every deadline assertion below while silently unhooking tenant
			// services from scan cancellation: a cancelled scan would then run
			// them to completion instead of stopping.
			cancelParent()
			inheritedCancel = ctx.Err() != nil
			gotDeadline = true
			return 0, 0, nil
		},
	}}
	defer func() { registeredTenantServices = restore }()

	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	runTenantServices(parent, []subscription{{ID: "sub"}},
		&recordingCred{tok: jwtWithTid(customerDirectory)}, federatedWithGraph,
		nil, recordingStore(&svcs, &notices, &warnings), "scan-1")

	if !gotDeadline {
		t.Fatal("the service never ran")
	}
	if !hadOne {
		t.Fatal("the tenant service was handed a context with no deadline, so a service that never returns hangs the whole scan")
	}
	// The VALUE too. "has a deadline" passes identically for
	// context.WithTimeout(ctx, time.Nanosecond), which would abort every real
	// Entra scan — a mutant this test could not see when it asserted existence
	// alone.
	// A BAND, not a floor. `left < serviceTimeout-time.Minute` alone passes for
	// context.WithTimeout(ctx, 24*time.Hour), which is the hang this wrapper
	// exists to prevent wearing a deadline's clothes — the assertion has to
	// bound the budget from ABOVE as well. No slack is needed on that side and
	// none is given: left is time.Until(deadline) where the deadline was
	// computed from a strictly earlier monotonic reading, so left is always
	// under serviceTimeout and the upper bound cannot flake.
	if left < serviceTimeout-time.Minute {
		t.Errorf("the tenant service was given %s of budget; serviceTimeout is %s — a deadline this short aborts a real directory enumeration", left, serviceTimeout)
	}
	if left > serviceTimeout {
		t.Errorf("the tenant service was given %s of budget; serviceTimeout is %s — a deadline longer than the phase's own bound leaves a service that never returns hanging the scan, which is what this wrapper exists to stop", left, serviceTimeout)
	}
	if !inheritedCancel {
		t.Error("cancelling the scan context did not cancel the tenant service's: the deadline was derived from a fresh root, so a cancelled scan runs its tenant services to completion")
	}
}

// TestReportEntra_BoundsAndDeFangsRemoteText pins the ONE chokepoint every
// Graph-derived message passes through on its way to the scan record.
//
// Bounding per error TYPE was the wrong shape: graphErr splices the response
// BODY — the largest remote-chosen string of the lot — and went through
// formatAzureError's pass-through arm untouched while a narrower type was
// bounded. So the fixtures here are the paths, not the types.
//
// Deliberately NOT all-ASCII: for ASCII, string([]rune(s)[:200]) and s[:200]
// are byte-identical, so an ASCII fixture cannot tell a rune-safe cut from a
// byte cut — the same trap this file's customerDirectory comment names for
// ToUpper.
func TestReportEntra_BoundsAndDeFangsRemoteText(t *testing.T) {
	report := func(t *testing.T, err error) (errs []store.ScanError, warns []store.ScanWarning) {
		t.Helper()
		var svcs []serviceReport
		var notices []store.ScanNotice
		st := recordingStore(&svcs, &notices, &warns)
		st.OnError = func(e store.ScanError) { errs = append(errs, e) }
		reportEntraErr(st, "users", err)
		return errs, warns
	}

	t.Run("a Graph error BODY is bounded", func(t *testing.T) {
		// A 4xx body is the remote side's to choose and reaches the record
		// through formatAzureError's pass-through arm.
		//
		// THREE-byte runes, deliberately: with "é" the byte cut landed exactly
		// on a boundary (the "graph: 500: " prefix is 12 bytes and 188 is
		// even), so the mutant that slices bytes instead of runes survived
		// this very assertion. 188 is not divisible by 3.
		body := strings.Repeat("日", 5000)
		errs, warns := report(t, &graphErr{status: 500, body: body})
		if len(warns) != 0 {
			t.Fatalf("a 500 was reported as a missing-consent warning: %+v", warns)
		}
		if len(errs) != 1 {
			t.Fatalf("errors = %d; want 1", len(errs))
		}
		if n := len([]rune(errs[0].Message)); n > 250 {
			t.Errorf("the scan record took %d runes of a body the server chose", n)
		}
		if !strings.Contains(errs[0].Message, "truncated") {
			t.Errorf("the message does not say it was cut: %.80q", errs[0].Message)
		}
		if !utf8.ValidString(errs[0].Message) {
			t.Errorf("the cut split a rune: %q", errs[0].Message)
		}
	})

	t.Run("the error body is capped at the READ, not only at the record", func(t *testing.T) {
		// A separate bound from the one above, and invisible to it: the
		// chokepoint truncates the MESSAGE, so an uncapped io.ReadAll would
		// produce an identical scan record while holding the whole body in
		// memory first. Nothing but this assertion can tell them apart.
		huge := strings.Repeat("x", 4<<20)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, huge)
		}))
		defer srv.Close()

		g := &graphClient{cred: &recordingCred{tok: jwtWithTid(customerDirectory)}, http: graphHTTPClient, baseURL: srv.URL}
		err := g.get(context.Background(), srv.URL+"/v1.0/users", new(struct{}))
		var ge *graphErr
		if !errors.As(err, &ge) {
			t.Fatalf("err = %v (%T); want a *graphErr", err, err)
		}
		if len(ge.body) > maxGraphErrorBody {
			t.Errorf("read %d bytes of a body the server chose; cap is %d", len(ge.body), maxGraphErrorBody)
		}
	})

	t.Run("a hostile HOST is bounded", func(t *testing.T) {
		// url.Parse imposes no host length limit, so hostOnly's output is not
		// a bound — which its doc claimed for one round.
		long := "https://" + strings.Repeat("a", 20000) + ".evil.example/v1.0/users"
		errs, _ := report(t, &foreignLinkError{host: hostOnly(long)})
		if len(errs) != 1 {
			t.Fatalf("errors = %d; want 1", len(errs))
		}
		if n := len([]rune(errs[0].Message)); n > 250 {
			t.Errorf("the scan record took %d runes of a host the server chose", n)
		}
	})

	t.Run("a bidi control cannot reorder the sentence", func(t *testing.T) {
		// Measured: bidi controls survive url.Parse, unlike ASCII control
		// characters, which it refuses. A host carrying one visually reorders
		// the refusal it sits inside.
		errs, _ := report(t, &foreignLinkError{host: hostOnly("https://\u202eevil.example/x")})
		if len(errs) != 1 {
			t.Fatalf("errors = %d; want 1", len(errs))
		}
		if strings.ContainsRune(errs[0].Message, '\u202e') {
			t.Errorf("a RIGHT-TO-LEFT OVERRIDE reached the scan record: %q", errs[0].Message)
		}
		if !strings.Contains(errs[0].Message, "evil.example") {
			t.Errorf("the host an operator must act on was lost: %q", errs[0].Message)
		}
	})

	t.Run("a short message survives intact", func(t *testing.T) {
		errs, _ := report(t, &graphTransportError{err: errors.New("connection refused")})
		if len(errs) != 1 {
			t.Fatalf("errors = %d; want 1", len(errs))
		}
		if !strings.Contains(errs[0].Message, "connection refused") {
			t.Errorf("the bound ate the diagnostic it exists to bound: %q", errs[0].Message)
		}
		if strings.Contains(errs[0].Message, "truncated") {
			t.Errorf("a short message was marked truncated: %q", errs[0].Message)
		}
	})
}

// TestGraphTransportError_KeepsTheRequestURLOutOfTheText is the other half of
// the classifier defence, and the half sameGraphHost cannot cover.
//
// sameGraphHost constrains a nextLink's scheme and host and nothing else, so
// from page two onward the PATH and QUERY of the URL g.get requests come from
// the response body. http.Client.Do reports a transport failure as a
// *url.Error whose Error() embeds that whole URL — so a nextLink of
// ".../v1.0/Authorization_RequestDenied" that the attacker then refuses to
// answer demoted a transport failure to the missing-consent WARNING.
func TestGraphTransportError_KeepsTheRequestURLOutOfTheText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	client := srv.Client()
	srv.Close() // nothing is listening, so the next request fails at the transport

	g := &graphClient{cred: &recordingCred{tok: jwtWithTid(customerDirectory)}, http: client, baseURL: base}
	err := g.get(context.Background(), base+"/v1.0/Authorization_RequestDenied?x=Insufficient%20privileges", new(struct{}))
	if err == nil {
		t.Fatal("the request reached a live server; this test needs the transport to fail")
	}
	for _, forbidden := range []string{"Authorization_RequestDenied", "Insufficient privileges", "Insufficient%20privileges", "/v1.0/"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("the transport error carries attacker-chosen text %q: %q", forbidden, err.Error())
		}
	}

	// A cause the SERVER chose, which connection-refused cannot exhibit: Go
	// parses the Location header before it consults CheckRedirect, so a
	// Location it cannot parse is echoed into the transport cause. That is a
	// classifier token inside a graphTransportError, and it is what pins the
	// type branch — without it the substring block reaches the same verdict by
	// luck and the mutant survives. Measured.
	t.Run("a server-chosen cause is still not a consent failure", func(t *testing.T) {
		hostile := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "/v1.0/Authorization_RequestDenied%zz")
			w.WriteHeader(http.StatusFound)
		}))
		defer hostile.Close()

		hg := &graphClient{cred: &recordingCred{tok: jwtWithTid(customerDirectory)}, http: graphHTTPClient, baseURL: hostile.URL}
		herr := hg.get(context.Background(), hostile.URL+"/v1.0/users", new(struct{}))
		if herr == nil {
			t.Fatal("the unparseable Location was accepted")
		}
		if !strings.Contains(herr.Error(), "Authorization_RequestDenied") {
			t.Skipf("this Go version does not echo the Location into the cause (%q); the fixture no longer exhibits the property", herr.Error())
		}
		var s2 []serviceReport
		var n2 []store.ScanNotice
		var w2 []store.ScanWarning
		st2 := recordingStore(&s2, &n2, &w2)
		var e2 []store.ScanError
		st2.OnError = func(e store.ScanError) { e2 = append(e2, e) }
		reportEntraErr(st2, "users", herr)
		if len(w2) != 0 {
			t.Errorf("a transport failure was reported as a missing-consent WARNING: %+v — the server chose the text and the substring block read it", w2)
		}
		if len(e2) != 1 {
			t.Errorf("errors reported = %d; want exactly 1", len(e2))
		}
	})

	// And the classifier must file it as an error, not as a missing grant.
	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	st := recordingStore(&svcs, &notices, &warnings)
	var errs []store.ScanError
	st.OnError = func(e store.ScanError) { errs = append(errs, e) }
	reportEntraErr(st, "users", err)
	if len(warnings) != 0 {
		t.Errorf("a transport failure was reported as a missing-consent WARNING: %+v", warnings)
	}
	if len(errs) != 1 {
		t.Errorf("errors reported = %d; want exactly 1", len(errs))
	}
}

// TestGraphTenantAdvice pins the trailing clause of the phase warning, which
// is persisted onto the scan record and is the only place an operator is told
// what they can still do.
//
// The two SUPPRESSED states — unset, and set but malformed — fail closed
// identically, so nothing about the outcome tells them apart and only the
// message can. (The third state is not a fail-closed one at all: the Entra
// services run. It is here because the advice must be silent there.) Collapsing them was the defect: a scan that
// already has a consented directory was still being told to set the variable
// it was configured with, and a malformed value read exactly like an unset
// one, which is the case where naming the shape is the whole diagnosis.
func TestGraphTenantAdvice(t *testing.T) {
	t.Run("a consented directory is already in effect: no advice", func(t *testing.T) {
		if got := graphTenantAdvice(federatedWithGraph); got != "" {
			t.Errorf("advice = %q; want none — the Entra services ran, so they are not in the skipped list and have no remedy to offer", got)
		}
	})

	t.Run("set but not a GUID: name the shape", func(t *testing.T) {
		cfg := federatedNoGraph
		cfg.graphTenantID = "contoso.onmicrosoft.com"
		got := graphTenantAdvice(cfg)
		if !strings.Contains(got, "not a directory GUID") {
			t.Errorf("advice = %q; want one saying the value is set but malformed — otherwise this reads exactly like unset and the operator re-sets what they already set", got)
		}
	})

	t.Run("unset: name the variable", func(t *testing.T) {
		got := graphTenantAdvice(federatedNoGraph)
		if !strings.Contains(got, envGraphTenantID) {
			t.Errorf("advice = %q; want one naming %s", got, envGraphTenantID)
		}
		if strings.Contains(got, "not a directory GUID") {
			t.Errorf("advice = %q; want no claim that a value is set, because none is", got)
		}
	})
}

// TestReportTenantScopeSkipped_DoesNotPrescribeTheRemedyAlreadyApplied is the
// same claim at the surface an operator actually reads. graphTenantAdvice's
// own test cannot see whether the warning still calls it.
//
// It pins NEITHER guard on its own, which is worth stating rather than
// discovering: with a consented directory the Entra service runs, so
// skippedGraph is false AND graphTenantAdvice's first arm returns "". Delete
// either one and this message is byte-identical — both mutants survive here.
// The gate is pinned by the ARM-only subtest of
// TestReportTenantScopeSkipped_ExplainsOnlyTheKindsItSkipped, where the advice
// would return its variable-naming arm; the first arm is pinned by the direct
// call in TestGraphTenantAdvice. What this test adds is the RENDERING an
// operator of a consented scan actually reads, which neither of those sees.
func TestReportTenantScopeSkipped_DoesNotPrescribeTheRemedyAlreadyApplied(t *testing.T) {
	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	reportTenantScopeSkipped(recordingStore(&svcs, &notices, &warnings), []subscription{{ID: "sub"}}, nil, federatedWithGraph)

	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v; want exactly one for the phase", warnings)
	}
	if strings.Contains(warnings[0].Message, "by setting "+envGraphTenantID) {
		t.Errorf("the phase warning tells an operator to set %s, which this scan is already configured with: %q", envGraphTenantID, warnings[0].Message)
	}
}

// TestReportTenantScopeSkipped_ExplainsOnlyTheKindsItSkipped pins the two
// clauses of the phase warning against the list they are offered as reasons
// for.
//
// --services can exclude either kind, so neither clause follows from the
// wifConfig alone. Attached unconditionally, the ARM justification explains a
// Graph-only list with a fact that is false of Graph — and the Graph advice
// tells an operator how to re-enable a service this run never asked for.
func TestReportTenantScopeSkipped_ExplainsOnlyTheKindsItSkipped(t *testing.T) {
	t.Run("a Graph-only list carries no ARM justification", func(t *testing.T) {
		var svcs []serviceReport
		var notices []store.ScanNotice
		var warnings []store.ScanWarning
		reportTenantScopeSkipped(recordingStore(&svcs, &notices, &warnings),
			[]subscription{{ID: "sub"}}, []string{"azure:microsoft.entra"}, federatedNoGraph)

		if len(warnings) != 1 {
			t.Fatalf("warnings = %+v; want exactly one", warnings)
		}
		if strings.Contains(warnings[0].Message, armReason) {
			t.Errorf("a list holding only the Graph service is explained by a fact about tenant-root ARM, which the very next clause contradicts: %q", warnings[0].Message)
		}
		if !strings.Contains(warnings[0].Message, envGraphTenantID) {
			t.Errorf("the Graph service was skipped and its remedy was withheld: %q", warnings[0].Message)
		}
	})

	// The default federated scan skips BOTH kinds, which is the rendering every
	// such scan actually emits and the one neither single-kind subtest sees.
	t.Run("a mixed list scopes the ARM reason to the ARM members", func(t *testing.T) {
		var svcs []serviceReport
		var notices []store.ScanNotice
		var warnings []store.ScanWarning
		reportTenantScopeSkipped(recordingStore(&svcs, &notices, &warnings),
			[]subscription{{ID: "sub"}}, nil, federatedNoGraph)

		if len(warnings) != 1 {
			t.Fatalf("warnings = %+v; want exactly one", warnings)
		}
		msg := warnings[0].Message
		// Both clauses belong here, but the ARM reason must say WHICH members
		// it explains: unqualified it follows a list whose first member is the
		// Graph service, and the next clause contradicts it for that member.
		//
		// Checked inside the CLAUSE that carries the reason, not over the whole
		// message: every service name also appears in the skipped list at the
		// front, so a whole-message Contains is satisfied by text from a
		// different sentence and passes against the unscoped rendering. That
		// mutant survived exactly this assertion written the loose way.
		clause := armReasonClause(t, msg)
		if !strings.Contains(clause, "azure:microsoft.management") {
			t.Errorf("the ARM reason names none of the members it applies to, so it reads as a statement about the whole list — whose first member is the Graph service, contradicted by the very next clause: %q", clause)
		}
		if strings.Contains(clause, "azure:microsoft.entra") {
			t.Errorf("the ARM reason names the Graph service, which is not read through a tenant-root ARM call: %q", clause)
		}
		if !strings.Contains(msg, envGraphTenantID) {
			t.Errorf("the Graph service was skipped and its remedy was withheld: %q", msg)
		}
	})

	t.Run("an ARM-only list carries no Graph advice", func(t *testing.T) {
		var svcs []serviceReport
		var notices []store.ScanNotice
		var warnings []store.ScanWarning
		reportTenantScopeSkipped(recordingStore(&svcs, &notices, &warnings),
			[]subscription{{ID: "sub"}}, []string{"azure:microsoft.management"}, federatedNoGraph)

		if len(warnings) != 1 {
			t.Fatalf("warnings = %+v; want exactly one", warnings)
		}
		if strings.Contains(warnings[0].Message, envGraphTenantID) {
			t.Errorf("a run that asked only for management groups is told how to re-enable a service it never requested: %q", warnings[0].Message)
		}
		if !strings.Contains(warnings[0].Message, armReason) {
			t.Errorf("the ARM service was skipped and its reason was withheld: %q", warnings[0].Message)
		}
		// "Of those" names a subset. Here the ARM members ARE the list, so it
		// would restate the list as a subset of itself — and the scoping the
		// mixed-list case needs is exactly what makes this rendering wrong.
		if strings.Contains(warnings[0].Message, "Of those") {
			t.Errorf("the reason is introduced as applying to a subset of a list it covers entirely: %q", warnings[0].Message)
		}
	})
}

// credentialErrorKeys counts the entries in loggedCredentialErrors.
//
// The map is package-level, never cleared, and keyed by a fragment of the
// error text — so it is both the side effect worth asserting and one that
// only a DELTA can be asserted about, since other tests in this package add
// entries of their own.
func credentialErrorKeys() int {
	n := 0
	loggedCredentialErrors.Range(func(any, any) bool {
		n++
		return true
	})
	return n
}

// armReason identifies the clause that explains an ARM skip. It IS the
// production constant, not a fragment of it: every use is a strings.Contains,
// so slicing bought nothing and cost a package-var initializer that could
// panic — which aborts the entire test binary before a single test runs, on
// an ordinary copy edit (dropping the relative pronoun in "which names no
// directory" is the obvious reword).
//
// Being the constant makes two of its three uses a BRANCH pin and nothing
// more: both sides of the comparison move together under a reword, so those
// assertions cannot see one. That is the right shape for asking WHICH clause
// the warning carries, and it is why the wording needs its own positive
// assertion — TestARMWarningReason_SaysWhyNothingCanBeDone below.
//
// The third use, armReasonClause, is a SELECTOR rather than a pin, and it does
// fail on one class of reword: it requires a single ". "-delimited clause to
// contain the whole constant, so a reword introducing a sentence break inside
// armWarningReason t.Fatals there. Latent today (that constant carries commas
// only). Two earlier versions of this comment were wrong in opposite
// directions — one claimed a reword surfaces here, the next that none can.
const armReason = armWarningReason

// TestARMWarningReason_SaysWhyNothingCanBeDone is the positive assertion every
// other ARM check needs and none supplies. armReason IS armWarningReason, so
// the branch pins move with a reword and stay green; this is the only place a
// reword goes red, which is the point — the reword is when the claim needs
// re-reading.
//
// Asserted on the CLAIM's load-bearing parts, not the whole sentence: that it
// names Azure Resource Manager as what is read, and that it says no directory
// is named, which is the entire reason no setting can lift this suppression.
// A reword keeping both survives; one dropping either has changed what the
// operator is told.
func TestARMWarningReason_SaysWhyNothingCanBeDone(t *testing.T) {
	// "nothing could confirm" is the CONCLUSION and is asserted for the same
	// reason the two nouns are: a reword keeping both nouns and replacing the
	// tail ("so the results were stored under the credential's own directory
	// instead") states the opposite outcome and passed without it.
	for _, want := range []string{"Azure Resource Manager", "names no directory", "nothing could confirm"} {
		if !strings.Contains(armWarningReason, want) {
			t.Errorf("armWarningReason no longer says %q, so the warning states a loss without the reason no setting can lift it: %q", want, armWarningReason)
		}
	}
	// The notice and the warning are two deliberate spellings of one reason
	// (see armWarningReason's doc). Deliberate is not the same as free: they
	// have already drifted once, on the verb.
	if !strings.Contains(armSkipNotice, "names no directory") {
		t.Errorf("armSkipNotice and armWarningReason no longer give the same reason; the per-service notice says %q", armSkipNotice)
	}
}

// assertDirectoryPairing holds s to the two-way mapping the directory guidance
// exists to state: the operator federating into their OWN tenant is PERMITTED
// the value a Lighthouse MSP is PROHIBITED, and each clause carries its own
// verb and not the other's.
//
// ORDER-AGNOSTIC, deliberately. An earlier version required the OWN-tenant
// clause to come first and reported anything else as a swap — but leading with
// the risky case is ordinary security writing, so that failed a correct rewrite
// with a diagnosis naming something that had not happened, which is how a guard
// gets suppressed instead of read. What must not change is which audience each
// verb binds; where they sit is the author's.
func assertDirectoryPairing(t *testing.T, where, s string) {
	t.Helper()
	own, lh := strings.Index(s, "OWN tenant"), strings.Index(s, "Lighthouse")
	if own < 0 || lh < 0 {
		t.Errorf("%s does not name both federation modes, so no pairing can be checked: %q", where, s)
		return
	}
	// BOTH clauses bounded. Bounding only the earlier one left the later one
	// running to end-of-string, which made the two orders disagree about
	// identical semantics: with the labels in today's order a trailing
	// "never" was outside the checked span, and with them reversed the same
	// sentence landed inside the OWN clause and fired the prohibition error —
	// the wrong-diagnosis failure this helper was made order-agnostic to
	// avoid, reintroduced by the fix for it. The later clause ends at its own
	// sentence boundary; the pairing is one sentence and what follows it is
	// the sufficiency bound, which belongs to neither audience.
	first, second := own, lh
	if lh < own {
		first, second = lh, own
	}
	firstClause, secondClause := s[first:second], s[second:]
	if i := strings.Index(secondClause, ". "); i >= 0 {
		secondClause = secondClause[:i]
	}
	ownClause, lhClause := firstClause, secondClause
	if lh < own {
		ownClause, lhClause = secondClause, firstClause
	}
	if !strings.Contains(ownClause, "same value as "+envWIFTenantID) {
		t.Errorf("%s: the OWN-tenant clause no longer PERMITS %s, which is the correct answer for an operator federating into their own tenant: %q", where, envWIFTenantID, ownClause)
	}
	if strings.Contains(ownClause, "never") {
		t.Errorf("%s: the OWN-tenant clause carries the prohibition; it belongs to the Lighthouse clause, and here it dead-ends the operator whose correct answer it forbids: %q", where, ownClause)
	}
	if !strings.Contains(lhClause, "never "+envWIFTenantID) {
		t.Errorf("%s: the Lighthouse clause no longer PROHIBITS %s: %q", where, envWIFTenantID, lhClause)
	}
}

// TestAssertDirectoryPairing_IsSymmetricInTheTwoOrders executes the branch no
// production string reaches.
//
// Both call sites feed text naming the OWN tenant first, so the reversed arm
// was dead code — and dead code in a GUARD is where a wrong verdict hides: it
// was asymmetric, passing a trailing "never" in one order and failing it in
// the other. These four cases are synthetic on purpose. They are the only
// place the helper is exercised against a string production does not produce,
// which is what the helper promises to accept.
func TestAssertDirectoryPairing_IsSymmetricInTheTwoOrders(t *testing.T) {
	const (
		ownFirst = "into your OWN tenant it is the same value as " + envWIFTenantID +
			"; under Azure Lighthouse it is the CUSTOMER's directory and never " + envWIFTenantID + ", which discloses it"
		lhFirst = "under Azure Lighthouse it is the CUSTOMER's directory and never " + envWIFTenantID +
			"; into your OWN tenant it is the same value as " + envWIFTenantID
		tail = ". The tenant-root services are never re-enabled whatever it names"
	)
	for _, tc := range []struct {
		name string
		s    string
	}{
		{"OWN tenant first", ownFirst},
		{"Lighthouse first", lhFirst},
		// The trailing sentence is not part of the pairing and must not be
		// read into either clause, in either order. Wording it with "never"
		// is an ordinary edit — graphWhichDirectory's own tail already says
		// "stay suppressed whatever it names".
		{"OWN tenant first, trailing sentence says never", ownFirst + tail},
		{"Lighthouse first, trailing sentence says never", lhFirst + tail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertDirectoryPairing(t, "synthetic", tc.s)
		})
	}
}

// TestReportTenantScopeSkipped_WarningCarriesTheDirectoryProhibition asserts
// the security payload on the string an OPERATOR receives, which nothing did.
//
// Twelve rounds refined WHAT was asserted about graphTenantAdvice's return
// value — a grep, then the predicate, then the pairing — and every one of them
// was made against the helper. None was made against the warning. So deleting
// the call at the single site that renders it,
//
//	msg += graphTenantAdvice(wif)  ->  msg += ". Set " + envGraphTenantID + " to re-enable the Entra ID services"
//
// stripped the whole which-directory guidance, prohibition included, from the
// only surface a customer sees — scanrun persists warnings and discards
// notices — and was green package-wide. A test on a helper's return value
// cannot see the helper's call being deleted. Assert on the rendering the
// audience receives.
//
// Both suppressed states, because they reach the advice through different arms
// and a single-state test would leave the other's arm free.
func TestReportTenantScopeSkipped_WarningCarriesTheDirectoryProhibition(t *testing.T) {
	malformed := federatedNoGraphCfg()
	malformed.graphTenantID = "not-a-guid"

	for _, tc := range []struct {
		name string
		wif  wifConfig
	}{
		{"no directory named", federatedNoGraph},
		{"a directory named and refused", malformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var svcs []serviceReport
			var notices []store.ScanNotice
			var warnings []store.ScanWarning
			reportTenantScopeSkipped(recordingStore(&svcs, &notices, &warnings), []subscription{{ID: "sub"}}, nil, tc.wif)

			if len(warnings) != 1 {
				t.Fatalf("warnings = %d; want exactly 1 for the phase", len(warnings))
			}
			msg := warnings[0].Message
			assertDirectoryPairing(t, "the phase warning", msg)
			// The consequence and the sufficiency bound reach the operator
			// too, for the same reason: each is asserted on the helper
			// elsewhere, and the helper is not what anyone reads.
			for _, want := range []string{"inventory", "stay suppressed"} {
				if !strings.Contains(msg, want) {
					t.Errorf("the phase warning does not carry %q; it is stated on graphTenantAdvice and never reaches the scan record: %q", want, msg)
				}
			}
		})
	}
}

// armReasonClause returns the sentence of msg that carries the ARM reason.
//
// Sentence-scoped on purpose: assertions over the whole warning are satisfied
// by the skipped-service LIST at the front, which names every suppressed
// service whatever the reason clause says.
func armReasonClause(t *testing.T, msg string) string {
	t.Helper()
	for _, c := range strings.Split(msg, ". ") {
		if strings.Contains(c, armReason) {
			return c
		}
	}
	t.Fatalf("no clause in the warning carries the ARM reason: %q", msg)
	return ""
}

// TestGraphClient_RefusesAnOversizePage pins the SUCCESS-body cap.
//
// The error body was capped and the success body was not, which is the wrong
// way round: the success path is the one that decodes into memory and then
// goes back for another page. runTenantServices' deadline bounds TIME, not
// bytes, so it is not a substitute — a server can stream at full speed.
//
// Refused BY NAME rather than by a bare io.LimitReader, whose truncation
// surfaces as "unexpected EOF" and reads as a Graph fault. That distinction is
// the assertion: a mutant swapping LimitedReader for LimitReader still fails
// the request, and only the type test tells the two apart.
func TestGraphClient_RefusesAnOversizePage(t *testing.T) {
	chunk := []byte(strings.Repeat("A", 1<<20))
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"value":[{"id":"`); err != nil {
			return
		}
		// One MiB past the cap, streamed: the client stops reading at the
		// limit and closes, so every write after that fails and the loop has
		// to stop rather than panic.
		for written := int64(0); written <= maxGraphPageBody; written += int64(len(chunk)) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		_, _ = io.WriteString(w, `"}]}`)
	}))
	defer graph.Close()

	g := &graphClient{cred: &recordingCred{tok: jwtWithTid(customerDirectory)}, http: graphHTTPClient, baseURL: graph.URL}
	var page graphPage[map[string]any]
	err := g.get(context.Background(), graph.URL+"/v1.0/users", &page)

	var oversize *oversizePageError
	if !errors.As(err, &oversize) {
		t.Fatalf("an oversize page was reported as %T (%v); want *oversizePageError — a bare byte limit reports \"unexpected EOF\", which reads as a Graph fault", err, err)
	}
	if oversize.limit != maxGraphPageBody {
		t.Errorf("refusal names limit %d; want maxGraphPageBody (%d)", oversize.limit, maxGraphPageBody)
	}
	if len(page.Value) != 0 {
		t.Errorf("decoded %d rows from a refused page", len(page.Value))
	}
	// And it is an ERROR, not the missing-consent warning: no permission a
	// customer can grant shrinks a response.
	if m, ok := neverAConsentFailure(err); !ok {
		t.Error("an oversize page is not classified as never-a-consent-failure, so a body containing \" 403\" could file it as the routine consent warning")
	} else if strings.Contains(m, "A") {
		t.Errorf("the refusal message carries remote bytes: %q", m)
	}
}

// TestIterateGraph_RefusesTooManyPages pins the loop bound, which the
// repeated-link refusal does NOT cover: a server incrementing $skiptoken names
// a fresh URL every time, so no link ever repeats and the cycle map grows with
// keys the server chose.
//
// maxGraphPages is lowered here rather than walked to. Reading the bound back
// out of the production expression instead would agree with itself whether or
// not iterateGraph still consults it.
//
// The mutant HANGS rather than failing an assertion — deleting the bound
// leaves the loop walking a server that never stops paging — so it is killed
// by the package timeout, not by a message. Run it with one.
//
// Must not run in parallel: it writes maxGraphPages, a package-level var.
func TestIterateGraph_RefusesTooManyPages(t *testing.T) {
	restore := maxGraphPages
	// The SHIPPED value, before it is overwritten. Everything below runs
	// against 4, so nothing else in the package can see maxGraphPages = 1 —
	// a bound that truncates every real directory at one page, which the two
	// sibling iterateGraph tests also survive because each returns on its own
	// guard before paging twice. A floor, not the number: the constant's own
	// doc claims "orders of magnitude past any real directory", and that is
	// the claim worth pinning.
	if restore < 10_000 {
		t.Errorf("shipped maxGraphPages = %d, which can truncate a real directory; it is documented as orders of magnitude past one", restore)
	}
	maxGraphPages = 4
	t.Cleanup(func() { maxGraphPages = restore })

	var served atomic.Int64
	var graph *httptest.Server
	graph = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := served.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// A NEW link every page, which is what defeats the repeated-link
		// guard. Using the same one would be diagnosed by that guard instead
		// and this test would pass against a deleted page bound.
		fmt.Fprintf(w, `{"value":[{"id":"u%d"}],"@odata.nextLink":%q}`, n, fmt.Sprintf("%s/v1.0/users?$skiptoken=%d", graph.URL, n))
	}))
	defer graph.Close()

	g := &graphClient{cred: &recordingCred{tok: jwtWithTid(customerDirectory)}, http: graphHTTPClient, baseURL: graph.URL}
	var seen int
	err := iterateGraph(context.Background(), g, graph.URL+"/v1.0/users", func(map[string]any) bool {
		seen++
		return true
	})

	var tooMany *tooManyPagesError
	if !errors.As(err, &tooMany) {
		t.Fatalf("an endless pager was reported as %T (%v); want *tooManyPagesError", err, err)
	}
	if tooMany.limit != maxGraphPages {
		t.Errorf("refusal names limit %d; want maxGraphPages (%d)", tooMany.limit, maxGraphPages)
	}
	// The BOUND, not just the refusal: a guard that fires one page late is
	// still a guard, one that fires after 10x the limit is not.
	if got := served.Load(); got > int64(maxGraphPages) {
		t.Errorf("served %d pages against a limit of %d", got, maxGraphPages)
	}
	if seen > maxGraphPages {
		t.Errorf("yielded %d entities from at most %d pages", seen, maxGraphPages)
	}
	// Never the consent warning: no grant makes a looping server stop.
	if _, ok := neverAConsentFailure(err); !ok {
		t.Error("an endless pager is not classified as never-a-consent-failure")
	}
}

// TestReportEntraErr_ClassifiesAGraphErrByItsStatus pins the typed-status
// classification.
//
// The status used to be read out of the response BODY with
// strings.Contains(raw, " 403"), which matches anywhere in 8 KiB the remote
// side wrote — so a 500 whose body happened to say "Error 403" was filed as
// the routine missing-consent WARNING and the real fault was never surfaced as
// one. graphErr carries the status as a field; the code STRINGS still come
// from the body, because those are Graph's own diagnosis.
func TestReportEntraErr_ClassifiesAGraphErrByItsStatus(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		body         string
		wantWarn     bool
		wantRedacted bool
	}{
		{"401 is a consent failure", http.StatusUnauthorized, `{"error":{"code":"InvalidAuthenticationToken"}}`, true, false},
		{"403 is a consent failure", http.StatusForbidden, `{"error":{"code":"Authorization_RequestDenied"}}`, true, false},
		{"a 500 whose body merely mentions 403", http.StatusInternalServerError, `{"error":{"message":"upstream said Error 403 while retrying"}}`, false, false},
		{"a 500 whose body merely mentions 401", http.StatusInternalServerError, `{"error":{"message":"gateway timeout after 401 seconds"}}`, false, false},
		{"a 404 is a hard error", http.StatusNotFound, `{"error":{"code":"ResourceNotFound"}}`, false, false},
		// Graph's own code still decides on a status that does not: this is
		// the input the substring classifier exists for, and narrowing to the
		// status alone would drop it.
		{"any status carrying Graph's own denial code", http.StatusInternalServerError, `{"error":{"code":"Authorization_RequestDenied"}}`, true, false},
		{"any status carrying the privileges message", http.StatusBadGateway, `{"error":{"message":"Insufficient privileges to complete the operation."}}`, true, false},
		// A 401 carrying an AADSTS code is the credential failure the
		// formatter collapses. Still a WARNING — a 401 is a consent failure by
		// status — but its TEXT must not survive.
		{"a 401 whose body is a credential failure", http.StatusUnauthorized, `{"error":{"code":"AADSTS700016: application not found in directory 9f8e7d6c-5b4a-4b9a-8271-6f5e4d3c2b1a"}}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var svcs []serviceReport
			var notices []store.ScanNotice
			var warnings []store.ScanWarning
			st := recordingStore(&svcs, &notices, &warnings)
			var errs []store.ScanError
			st.OnError = func(e store.ScanError) { errs = append(errs, e) }

			reportEntraErr(st, "users", &graphErr{status: tc.status, body: tc.body})

			// The MESSAGE, not only the verdict, for the ONE case where the
			// formatter changes it. formatAzureError passes an ordinary
			// graphErr body straight through (it is neither a credential
			// failure nor an *azcore.ResponseError), so asserting redaction
			// generally would be false. What the chokepoint buys is exactly
			// one thing per its own doc: a credential failure redacted here
			// like everywhere else, kept narrow by scanBodyForAADSTS's graphErr
			// arm requiring a 401. Without this, msg := formatAzureError(err)
			// is replaceable by err.Error() and every subtest still passes.
			if tc.wantRedacted {
				for _, m := range reportedMessages(warnings, errs) {
					if strings.Contains(m, "AADSTS") && strings.Contains(m, tc.body) {
						t.Errorf("a credential failure reached the scan record with its body intact: %q — that text names disco's own tenant and the assertion subject, not anything the customer scanned", m)
					}
				}
			}
			gotWarn := len(warnings) == 1 && len(errs) == 0
			gotErr := len(errs) == 1 && len(warnings) == 0
			switch {
			case tc.wantWarn && !gotWarn:
				t.Errorf("status %d body %q: want ONE warning, got %d warnings / %d errors — a missing consent the customer can grant must not be filed as a hard error", tc.status, tc.body, len(warnings), len(errs))
			case !tc.wantWarn && !gotErr:
				t.Errorf("status %d body %q: want ONE error, got %d warnings / %d errors — a fault filed as the routine consent warning is never surfaced as one", tc.status, tc.body, len(warnings), len(errs))
			}
		})
	}
}

// TestRunTenantServices_ReleasesTheDeadlineWhenAServicePanics pins the
// DEFERRED cancel.
//
// runTenantServices does not recover — the only recovery is on the goroutine
// in Scan — and azure_scanner_test.go registers a tenant service that panics
// on purpose. So a bare cancel() after the call is skipped exactly when the
// timer most needs releasing, and go vet's lostcancel does not see it: the
// call IS present, on a path the panic jumps over.
//
// The observable is the service context itself, captured before the panic:
// with the cancel deferred it is Canceled by the time the panic reaches this
// frame, and without it, still live.
//
// Must not run in parallel: it swaps registeredTenantServices.
func TestRunTenantServices_ReleasesTheDeadlineWhenAServicePanics(t *testing.T) {
	var captured context.Context
	restore := registeredTenantServices
	registeredTenantServices = []tenantServiceEntry{{
		name:        "azure:test.panic",
		graphScoped: true,
		fn: func(ctx context.Context, _ []subscription, _ azcore.TokenCredential, _ wifConfig, _ *store.Store, _ string) (int, int, error) {
			captured = ctx
			panic("a tenant service panicked")
		},
	}}
	defer func() { registeredTenantServices = restore }()

	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	var panicked bool
	func() {
		defer func() { panicked = recover() != nil }()
		runTenantServices(context.Background(), []subscription{{ID: "sub"}},
			&recordingCred{tok: jwtWithTid(customerDirectory)}, federatedWithGraph,
			nil, recordingStore(&svcs, &notices, &warnings), "scan-1")
	}()

	if !panicked {
		t.Fatal("the service did not panic, so this test proves nothing about the panic path")
	}
	if captured == nil {
		t.Fatal("the service never ran")
	}
	if captured.Err() == nil {
		t.Error("the per-service deadline was still live after the service panicked: cancel() was skipped, so the timer and its parent-context registration outlive the phase")
	}
	// A DEADLINE, not just a cancel: context.WithCancel in place of
	// WithTimeout leaves Err() == context.Canceled here and passes every
	// assertion above while the bound this whole wrapper exists for is gone.
	if _, ok := captured.Deadline(); !ok {
		t.Error("the service context carried no deadline, so what was released was a plain cancel and a service that never returns still hangs the scan")
	}
}

// reportedMessages flattens the messages a store recorded, whichever severity
// they landed under. A message's severity and its TEXT are separate claims and
// a test asserting the text should not have to know which branch produced it.
func reportedMessages(warnings []store.ScanWarning, errs []store.ScanError) []string {
	out := make([]string, 0, len(warnings)+len(errs))
	for _, w := range warnings {
		out = append(out, w.Message)
	}
	for _, e := range errs {
		out = append(out, e.Message)
	}
	return out
}

// TestGraphClient_MalformedPageIsNotAnOversizeRefusal is the other side of the
// cap, and the side that decides whether the refusal MEANS anything.
//
// Truncation and hitting the cap both reach json.Decoder as
// io.ErrUnexpectedEOF, so the only thing separating them is the
// LimitedReader's remaining N. Widening that test to `true` makes every decode
// failure an oversizePageError, and a customer whose page was tampered with in
// transit is told the response was over 64 MiB. Nothing else in the package
// puts a malformed body through graphClient.get.
//
// WHAT THIS DOES NOT SEE, because the fixture is 29 bytes and leaves N just
// under the cap: any off-by-one threshold in [1, maxGraphPageBody-1]. Together
// with TestGraphClient_RefusesAnOversizePage, which drives N to 0, the pair
// kills `true` and thresholds at the very top of the range and nothing
// between. Closing that band needs a MALFORMED body of exactly the cap — 64
// MiB per run for an off-by-one in a backstop — and is deliberately not paid.
// An earlier version of this comment named `lr.N <= 1` as a mutant it catches;
// it does not.
func TestGraphClient_MalformedPageIsNotAnOversizeRefusal(t *testing.T) {
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Truncated mid-value, kilobytes below the cap.
		fmt.Fprint(w, `{"value":[{"id":"u1"},{"id":`)
	}))
	defer graph.Close()

	g := &graphClient{cred: &recordingCred{tok: jwtWithTid(customerDirectory)}, http: graphHTTPClient, baseURL: graph.URL}
	var page graphPage[map[string]any]
	err := g.get(context.Background(), graph.URL+"/v1.0/users", &page)

	if err == nil {
		t.Fatal("a truncated page decoded without error")
	}
	var oversize *oversizePageError
	if errors.As(err, &oversize) {
		t.Errorf("a %d-byte malformed page was refused as oversize (limit %d): a decode failure below the cap is a Graph fault or tampering, and reporting it as a size limit sends the operator after the wrong thing", 29, oversize.limit)
	}
}

// TestReportTenantScopeSkipped_SaysWhichGraphStateSuppressedIt pins the split
// the per-service notice was missing.
//
// The Graph phase is suppressed in TWO states — no directory named, and one
// named that this package refused for not being a GUID — and they fail closed
// identically, so nothing but the message can tell them apart. That argument
// already produced graphTenantAdvice's split in the phase WARNING; the notice,
// which is what renders beside the service line, kept the unnamed wording for
// both. It told an operator who HAD set the variable that nothing was named,
// and pointed them at a warning saying the opposite.
func TestReportTenantScopeSkipped_SaysWhichGraphStateSuppressedIt(t *testing.T) {
	graphServices := func() []string {
		var names []string
		for _, svc := range registeredTenantServices {
			if svc.graphScoped {
				names = append(names, svc.name)
			}
		}
		return names
	}()
	if len(graphServices) == 0 {
		t.Skip("no graphScoped tenant services registered")
	}

	malformed := federatedNoGraphCfg()
	malformed.graphTenantID = "not-a-guid"
	// The precondition the whole test rests on: a malformed value must reach
	// the suppressed path rather than being refused earlier. If this ever
	// stops holding, the assertions below pass vacuously.
	if malformed.graphTenantEnabled() {
		t.Fatal("a malformed graph tenant id was accepted, so this test can no longer reach the state it exists for")
	}

	for _, tc := range []struct {
		name string
		wif  wifConfig
		want string
	}{
		{"no directory named", federatedNoGraph, graphSkipNoticeUnnamed},
		{"a directory named and refused", malformed, graphSkipNoticeMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var svcs []serviceReport
			var notices []store.ScanNotice
			var warnings []store.ScanWarning
			reportTenantScopeSkipped(recordingStore(&svcs, &notices, &warnings), []subscription{{ID: "sub"}}, nil, tc.wif)

			byService := map[string]string{}
			for _, n := range notices {
				byService[n.Service] = n.Message
			}
			for _, name := range graphServices {
				if got := byService[name]; got != tc.want {
					t.Errorf("%s notice = %q; want %q", name, got, tc.want)
				}
			}
		})
	}

	// The two must actually differ, or selecting between them is decoration
	// and the assertions above hold for a single shared string.
	if graphSkipNoticeUnnamed == graphSkipNoticeMalformed {
		t.Error("the two Graph suppression notices are identical, so the operator still cannot tell which state they are in")
	}
	// CONTENT, positive, and per notice. Every `want` above is a production
	// constant, so rewriting BOTH in the same direction — dropping the pointer
	// to the warning, say — satisfies every assertion so far while the notices
	// stop saying the thing that makes them useful.
	if !strings.Contains(graphSkipNoticeMalformed, "GUID") {
		t.Errorf("the malformed-value notice does not name the shape that was rejected, which is the whole diagnosis nothing else in the scan record carries: %q", graphSkipNoticeMalformed)
	}
	for _, n := range []struct{ label, msg string }{
		{"graphSkipNoticeUnnamed", graphSkipNoticeUnnamed},
		{"graphSkipNoticeMalformed", graphSkipNoticeMalformed},
	} {
		if !strings.Contains(n.msg, "scan warning") {
			t.Errorf("%s does not point at the warning that carries the remedy, so the notice states a loss and withholds the fix: %q", n.label, n.msg)
		}
	}
}

// TestRunTenantPhase_AccountsForSkipsEvenWhenAServicePanics pins the ORDER of
// the two halves, which is the only reason runTenantPhase exists as a function
// rather than as two statements in Scan's goroutine.
//
// runTenantServices does not recover. Reported second, the skip accounting
// unwinds away with the panic — and the shape that reaches it is the ORDINARY
// consented one, where the Graph service runs and the ARM services are
// skipped, so a customer would see a panic and an inventory with no account of
// the suppressed half.
//
// Must not run in parallel: it swaps registeredTenantServices.
func TestRunTenantPhase_AccountsForSkipsEvenWhenAServicePanics(t *testing.T) {
	restore := registeredTenantServices
	registeredTenantServices = []tenantServiceEntry{
		// Runnable under a consented directory, and panics: the half that
		// aborts the goroutine.
		{
			name:        "azure:test.graph",
			graphScoped: true,
			fn: func(context.Context, []subscription, azcore.TokenCredential, wifConfig, *store.Store, string) (int, int, error) {
				panic("a tenant service panicked")
			},
		},
		// Never runnable under federation: the half whose accounting must
		// survive.
		{name: "azure:test.arm"},
	}
	defer func() { registeredTenantServices = restore }()

	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	var panicked bool
	func() {
		defer func() { panicked = recover() != nil }()
		runTenantPhase(context.Background(), []subscription{{ID: "sub"}},
			&recordingCred{tok: jwtWithTid(customerDirectory)}, federatedWithGraph,
			nil, recordingStore(&svcs, &notices, &warnings), "scan-1")
	}()

	if !panicked {
		t.Fatal("the service did not panic, so this test proves nothing about the panic path")
	}
	var armNotice bool
	for _, n := range notices {
		if n.Service == "azure:test.arm" {
			armNotice = true
		}
	}
	if !armNotice {
		t.Error("no notice for the skipped ARM service: the accounting ran after the phase and a panic took it, leaving the scan record silent about a service that was suppressed")
	}
	if len(warnings) == 0 {
		t.Error("no phase warning: the severity of a suppressed directory read is carried by the warning, and it was lost to the panic")
	}
	var armService bool
	for _, s := range svcs {
		if s.name == "azure:test.arm" {
			armService = true
		}
	}
	if !armService {
		t.Error("no zero-count service row for the skipped ARM service, so the progress line does not account for it")
	}
}

// TestGraphTenantAdvice_NamesBothAudiencesAndTheWrongValue pins the advice's
// content, which nothing did: every assertion on it elsewhere checks only that
// DISCO_AZURE_GRAPH_TENANT_ID is mentioned, so reverting the whole clause to a
// one-line "set it to that directory's GUID" passed the package.
//
// The withheld half is the security-relevant one. A Lighthouse operator's
// environment already holds a GUID — DISCO_AZURE_WIF_TENANT_ID — that passes
// every check in this package (azidentity short-circuits when the requested
// tenant IS the credential's default, so the tid comes back equal and
// scanEntra accepts) and writes the MANAGING directory's users, groups and
// service principals into the customer's inventory. This message is the only
// place an operator is told that.
func TestGraphTenantAdvice_NamesBothAudiencesAndTheWrongValue(t *testing.T) {
	malformed := federatedNoGraphCfg()
	malformed.graphTenantID = "not-a-guid"

	for _, tc := range []struct {
		name string
		wif  wifConfig
	}{
		{"no directory named", federatedNoGraph},
		// The malformed state reaches this too, and is the state whose
		// operator is about to retype a value. It carried only the GUID shape.
		{"a directory named and refused", malformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := graphTenantAdvice(tc.wif)
			if got == "" {
				t.Fatal("no advice offered for a suppressed Graph phase")
			}
			// BOTH audiences. The doc's own success criterion is that one
			// sentence serves a Lighthouse MSP and an operator federating into
			// their own tenant, for whom the prohibited value is the correct
			// answer — so naming only the prohibition dead-ends them.
			for _, want := range []string{envWIFTenantID, "OWN tenant", "Lighthouse", "CUSTOMER"} {
				if !strings.Contains(got, want) {
					t.Errorf("advice does not mention %q, so it does not distinguish the two operators who read it: %q", want, got)
				}
			}
			// The PROHIBITION itself, positively. Everything above is a
			// vocabulary check that a reword cannot fail: with "never"
			// weakened to "usually", or dropped so the clause merely names
			// the value, every token above is still present and the one
			// security instruction in the message has been inverted.
			if !strings.Contains(got, "never "+envWIFTenantID) {
				t.Errorf("advice no longer PROHIBITS %s under Lighthouse; that value passes every check in this package and discloses disco's own directory: %q", envWIFTenantID, got)
			}
			// And WHICH audience it binds. Asserting the predicate is not
			// enough: the predicate has an ARGUMENT — the federation mode —
			// and swapping the two branch labels moves it while leaving every
			// token above present. That mutant tells a Lighthouse MSP to point
			// Graph at disco's own directory, which is the disclosure this
			// whole change exists to prevent, and it was green package-wide.
			// So pin the PAIRING: prohibition after Lighthouse, permission
			// after OWN tenant, and neither clause carrying the other's verb.
			assertDirectoryPairing(t, "advice", got)
			// And what going wrong COSTS. "never use X" without the
			// consequence reads as a style rule.
			if !strings.Contains(got, "inventory") {
				t.Errorf("advice names the wrong value without saying what pointing there does — it is accepted, and writes disco's own directory into the customer's inventory: %q", got)
			}
			// The sufficiency BOUND, positively. The negative below greps a
			// literal an earlier round retired, so it is satisfied by
			// deleting the replacement outright or by any differently-worded
			// sufficiency claim.
			if !strings.Contains(got, "stay suppressed") {
				t.Errorf("advice does not bound what naming a directory buys; the tenant-root phases stay off whatever it names: %q", got)
			}
			// No sufficiency claim: naming a directory restores the Entra ID
			// services alone, and an earlier version closed with "setting it
			// is the correct answer", which read as done.
			if strings.Contains(got, "correct answer") {
				t.Errorf("advice claims the setting is the whole answer; the tenant-root phases stay suppressed regardless: %q", got)
			}
		})
	}

	// The malformed state must still carry its own diagnosis, which is the
	// only thing separating it from unset.
	if !strings.Contains(graphTenantAdvice(malformed), "GUID") {
		t.Error("the malformed-value advice no longer names the shape that was rejected")
	}
	if strings.Contains(graphTenantAdvice(federatedNoGraph), "is set but") {
		t.Error("the unset state is told its value was rejected; nothing was set")
	}
	if graphTenantAdvice(federatedWithGraph) != "" {
		t.Error("advice was offered for a Graph phase that ran, repeating a remedy already applied")
	}
}

// TestReportTenantScopeSkipped_WarningLeadSaysWhyThereIsAWarning pins the lead
// clause. Deleting its entire prose and leaving `(a, b): ` was green: the only
// assertion reaching the lead required the skipped service NAMES, which
// strings.Join supplies on its own.
func TestReportTenantScopeSkipped_WarningLeadSaysWhyThereIsAWarning(t *testing.T) {
	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	reportTenantScopeSkipped(recordingStore(&svcs, &notices, &warnings), []subscription{{ID: "sub"}}, nil, federatedNoGraph)

	if len(warnings) != 1 {
		t.Fatalf("warnings = %d; want exactly 1 for the phase", len(warnings))
	}
	msg := warnings[0].Message
	// Scoped to the LEAD, which is what this test is about. Read against the
	// whole message these three pass with the lead reduced to a bare service
	// list and the prose moved to the end — the same mechanic armReasonClause
	// exists for, not applied to the test that most needs it.
	lead, _, _ := strings.Cut(msg, ". ")
	// The one fact true in EVERY state, which is why it is what survives in
	// the lead: this scan is federated, and the variable that made it so.
	for _, want := range []string{"federated credential", envWIFClientID, "absent from this scan"} {
		if !strings.Contains(lead, want) {
			t.Errorf("the warning lead does not say %q, so it names suppressed services and never says the scan is the reason: %q", want, lead)
		}
	}
	// And NOT the claim that was false in the consented and malformed states.
	// Asserted STRUCTURALLY over the lead, not as a grep for the retired
	// literal, which re-adding the same claim reworded would satisfy.
	//
	// The property is a CONFIRMATION claim, not the word "directory". An
	// earlier version forbade "directory" and was wrong twice over: this
	// codebase says "tenant" and "directory" interchangeably, so
	// "whose tenant cannot be confirmed to belong to the scanned
	// subscription's" walked through it; and the docs call the service
	// "Entra ID directory objects", so rendering the skipped services by
	// their operator-facing names — the obvious next edit — would have gone
	// red with a diagnosis naming something that had not happened.
	//
	// Two checks, because the verb list is a VOCABULARY check however it is
	// framed: "cannot be shown to be", "may not be the scanned tenant's" and
	// "no guarantee" carry none of the three. An absence has no predicate to
	// assert, so nothing here can be structural the way the pairing check is.
	for _, verb := range []string{"confirm", "verif", "prove"} {
		if strings.Contains(lead, verb) {
			t.Errorf("the lead carries a %q claim again, leaving two competing causal clauses; the kind-specific reasons follow and know their state, the lead does not: %q", verb, lead)
		}
	}
	// The SIZE bound is the half that survives paraphrase: a second causal
	// clause has to be spelled somehow, and every spelling is long. Measured —
	// the prose is 110 bytes, and the retired claim re-added in the shortest
	// wording that dodges the verb list took it to 181. The bound sits at 140
	// so an ordinary reword has room and a re-added clause does not. Taken
	// after the service list, whose length belongs to the registry.
	_, prose, cut := strings.Cut(lead, "): ")
	if !cut {
		t.Fatalf("the lead no longer separates the service list from its prose: %q", lead)
	}
	if len(prose) > 140 {
		t.Errorf("the lead prose is %d bytes, over the 140 bound; the reasons that know their state belong in the clauses after it, not here: %q", len(prose), prose)
	}
	if strings.Contains(msg, "cannot be confirmed to be the scanned tenant's") {
		t.Errorf("the lead reasserts a directory-confirmation claim that is false once a directory is consented — the Entra phase confirms its own by tid: %q", msg)
	}
}
