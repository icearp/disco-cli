package azure

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/icearp/disco-cli/store"
)

// federatedCfg is the minimum contract that turns federation on.
func federatedCfg() wifConfig {
	return wifConfig{clientID: "client-guid", tenantID: "our-tenant-guid"}
}

func TestSelectCredentialMode(t *testing.T) {
	tests := []struct {
		name string
		cfg  wifConfig
		want credentialMode
	}{
		{"both halves set", federatedCfg(), credModeFederated},
		{"nothing set", wifConfig{}, credModeDefault},
		// Every half-set case below reports credModeDefault, which is NOT a
		// fall back: newAzureCredential refuses a half-set contract before it
		// consults the mode at all (TestNewAzureCredential_RefusesHalfSet).
		// Reading these rows as "an incomplete config falls back" is the
		// mistake partiallyConfigured exists to prevent.
		{"client id alone", wifConfig{clientID: "client-guid"}, credModeDefault},
		{"tenant id alone", wifConfig{tenantID: "our-tenant-guid"}, credModeDefault},
		{"audience alone", wifConfig{audience: tokenExchangeAudience}, credModeDefault},
		{"session alone", wifConfig{roleARN: "arn:aws:iam::1:role/r", sessionName: "s"}, credModeDefault},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectCredentialMode(tt.cfg); got != tt.want {
				t.Errorf("selectCredentialMode() = %q; want %q", got, tt.want)
			}
		})
	}
}

// TestTenantScopeEnabled pins the cross-tenant guard.
//
// Under Azure Lighthouse the credential authenticates in DISCO's tenant, so
// every tenant-scope API answers about disco's own directory. The deny case
// here is what keeps disco's users, groups, service principals, applications
// and management groups out of a customer's inventory — and it is a deny that
// nothing else would catch, because those APIs SUCCEED rather than 403.
func TestTenantScopeEnabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  wifConfig
		want bool
	}{
		{
			name: "not federated: an operator's own credential scans its own tenant",
			cfg:  wifConfig{},
			want: true,
		},
		{
			name: "federated without a named customer directory: denied",
			cfg:  federatedCfg(),
			want: false,
		},
		{
			name: "federated with only an audience set: still denied",
			cfg: func() wifConfig {
				c := federatedCfg()
				c.audience = "api://custom"
				return c
			}(),
			want: false,
		},
		{
			name: "a role ARN alone does not federate, so tenant scope stays on",
			cfg:  wifConfig{roleARN: "arn:aws:iam::1:role/r"},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.tenantScopeEnabled(); got != tt.want {
				t.Errorf("tenantScopeEnabled() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestEffectiveAudience(t *testing.T) {
	if got := (wifConfig{}).effectiveAudience(); got != tokenExchangeAudience {
		t.Errorf("default audience = %q; want %q", got, tokenExchangeAudience)
	}
	if got := (wifConfig{audience: "api://custom"}).effectiveAudience(); got != "api://custom" {
		t.Errorf("configured audience = %q; want api://custom", got)
	}
}

// TestWifSubjectCredentials_HalfSetSessionFails asserts the half-set session is
// refused rather than silently presenting the ambient identity. Entra matches
// the federated credential's subject against the token's `sub` claim exactly
// and supports no wildcard, so a different principal fails the exchange with
// the same error code as propagation delay — which the retry would then sit
// through. Failing here names the real cause.
func TestWifSubjectCredentials_HalfSetSessionFails(t *testing.T) {
	ambient := aws.Config{Credentials: aws.NewCredentialsCache(aws.AnonymousCredentials{})}

	t.Run("role arn without session name", func(t *testing.T) {
		if _, err := wifSubjectCredentials(ambient, wifConfig{roleARN: "arn:aws:iam::1:role/r"}); err == nil {
			t.Fatal("wifSubjectCredentials() = nil error; want a refusal")
		}
	})
	t.Run("session name without role arn", func(t *testing.T) {
		if _, err := wifSubjectCredentials(ambient, wifConfig{sessionName: "s"}); err == nil {
			t.Fatal("wifSubjectCredentials() = nil error; want a refusal")
		}
	})
	t.Run("neither: the ambient identity itself", func(t *testing.T) {
		got, err := wifSubjectCredentials(ambient, wifConfig{})
		if err != nil {
			t.Fatalf("wifSubjectCredentials() error = %v", err)
		}
		if got != ambient.Credentials {
			t.Errorf("wifSubjectCredentials() = %T; want the ambient provider unchanged", got)
		}
	})
	t.Run("both: a cached assume-role provider", func(t *testing.T) {
		got, err := wifSubjectCredentials(ambient, wifConfig{roleARN: "arn:aws:iam::1:role/r", sessionName: "s"})
		if err != nil {
			t.Fatalf("wifSubjectCredentials() error = %v", err)
		}
		// The cache is the part that matters: an AssumeRole session expires at
		// one hour and a scan can outlive it, so an unwrapped provider would
		// mint one set of credentials and use it past expiry.
		if _, ok := got.(*aws.CredentialsCache); !ok {
			t.Errorf("wifSubjectCredentials() = %T; want it wrapped in *aws.CredentialsCache", got)
		}
	})
}

func TestWifEnv(t *testing.T) {
	// Padded on purpose: a value injected from a file or a secret store
	// arrives with a trailing newline often enough to matter, and untrimmed it
	// reaches the token exchange as part of the client id.
	t.Setenv(envWIFClientID, "  client-guid\n")
	t.Setenv(envWIFTenantID, "our-tenant-guid\t")
	t.Setenv(envWIFAudience, " api://custom ")
	t.Setenv(envWIFRoleARN, "arn:aws:iam::1:role/r\n")
	t.Setenv(envWIFSessionName, "  sess")

	got := wifEnv()
	want := wifConfig{
		clientID:    "client-guid",
		tenantID:    "our-tenant-guid",
		audience:    "api://custom",
		roleARN:     "arn:aws:iam::1:role/r",
		sessionName: "sess",
	}
	if got != want {
		t.Errorf("wifEnv() = %+v; want %+v", got, want)
	}
}

// countingCredential returns errs[i] on call i, then a token.
type countingCredential struct {
	errs  []error
	calls int
}

func (c *countingCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	i := c.calls
	c.calls++
	if i < len(c.errs) && c.errs[i] != nil {
		return azcore.AccessToken{}, c.errs[i]
	}
	return azcore.AccessToken{Token: "tok", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestIsFederationPropagationError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"the propagation code", errors.New("AADSTS70021: No matching federated identity record found for presented assertion"), true},
		// AADSTS700213 contains AADSTS70021 as a prefix but is a permanent
		// failure. Retrying it only delays the report, so the matcher requires
		// the trailing colon.
		{"longer code sharing the prefix", errors.New("AADSTS700213: No matching federated identity record found for presented assertion (permanent: subject mismatch)"), false},
		{"unrelated code", errors.New("AADSTS7000215: invalid client secret"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isFederationPropagationError(tt.err); got != tt.want {
				t.Errorf("isFederationPropagationError() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestRetryCredential(t *testing.T) {
	prop := errors.New("AADSTS70021: No matching federated identity record found for presented assertion")

	t.Run("retries propagation error then succeeds", func(t *testing.T) {
		inner := &countingCredential{errs: []error{prop, prop}}
		r := &retryCredential{inner: inner, delay: time.Millisecond}
		tok, err := r.GetToken(context.Background(), policy.TokenRequestOptions{})
		if err != nil {
			t.Fatalf("GetToken() error = %v; want success after retries", err)
		}
		if tok.Token != "tok" {
			t.Errorf("GetToken() token = %q; want %q", tok.Token, "tok")
		}
		if inner.calls != 3 {
			t.Errorf("inner calls = %d; want 3", inner.calls)
		}
	})

	t.Run("does not retry an unrelated error", func(t *testing.T) {
		inner := &countingCredential{errs: []error{errors.New("AADSTS900023: tenant not found")}}
		r := &retryCredential{inner: inner, delay: time.Millisecond}
		if _, err := r.GetToken(context.Background(), policy.TokenRequestOptions{}); err == nil {
			t.Fatal("GetToken() = nil error; want the inner error")
		}
		if inner.calls != 1 {
			t.Errorf("inner calls = %d; want 1 — an unrelated failure must not be retried", inner.calls)
		}
	})

	t.Run("stops at the attempt cap", func(t *testing.T) {
		errs := make([]error, federationPropagationAttempts)
		for i := range errs {
			errs[i] = prop
		}
		inner := &countingCredential{errs: errs}
		r := &retryCredential{inner: inner, delay: time.Millisecond}
		if _, err := r.GetToken(context.Background(), policy.TokenRequestOptions{}); err == nil {
			t.Fatal("GetToken() = nil error; want the final propagation error")
		}
		if inner.calls != federationPropagationAttempts {
			t.Errorf("inner calls = %d; want %d", inner.calls, federationPropagationAttempts)
		}
	})

	t.Run("honours context cancellation between attempts", func(t *testing.T) {
		inner := &countingCredential{errs: []error{prop, prop, prop, prop}}
		r := &retryCredential{inner: inner, delay: time.Hour}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := r.GetToken(ctx, policy.TokenRequestOptions{}); !errors.Is(err, context.Canceled) {
			t.Errorf("GetToken() error = %v; want context.Canceled", err)
		}
	})
}

// serviceReport is one ReportService call.
type serviceReport struct {
	name   string
	status store.ServiceStatus
}

// recordingStore returns a Store that records reports and touches no database
// — the Report* methods only fire their hook.
//
// Locked because the hooks fire from the tenant goroutine and the per-sub
// goroutines concurrently. Every caller today drives one side only, so -race
// is quiet either way; the mutex is what keeps it that way when a caller
// changes its service filter.
func recordingStore(svcs *[]serviceReport, notices *[]store.ScanNotice, warnings *[]store.ScanWarning) *store.Store {
	var mu sync.Mutex
	st := &store.Store{}
	st.OnServiceComplete = func(service, _ string, _, _, _, _ int, status store.ServiceStatus) {
		mu.Lock()
		defer mu.Unlock()
		*svcs = append(*svcs, serviceReport{name: service, status: status})
	}
	st.OnNotice = func(n store.ScanNotice) {
		mu.Lock()
		defer mu.Unlock()
		*notices = append(*notices, n)
	}
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		*warnings = append(*warnings, w)
	}
	return st
}

// directoryReadTenantServices returns the registered tenant services whose
// suppression actually costs data — the ones a notice must account for with a
// service line. A dedupOnly phase loses nothing, so it is reported differently.
func directoryReadTenantServices() []string {
	var names []string
	for _, svc := range registeredTenantServices {
		if !svc.dedupOnly {
			names = append(names, svc.name)
		}
	}
	return names
}

// TestReportTenantScopeSkipped_AccountsForEveryRegisteredService is the
// structural half of the guard: the gate in Scan suppresses the whole tenant
// phase, so a tenant service added later is covered without being named
// anywhere. This asserts the reporting keeps pace, so a suppressed service
// stays visible in the scan record rather than silently absent — an empty
// result and a skipped scan look identical otherwise.
//
// The notice and the phase warning are the load-bearing halves, not the
// service line: with ServiceOK the progress line renders no suffix at all, so
// dropping them would make a suppressed tenant phase indistinguishable from a
// tenant that has no directory objects. The warning is the half that survives
// into the scan record — scanrun persists warnings and discards notices.
func TestReportTenantScopeSkipped_AccountsForEveryRegisteredService(t *testing.T) {
	if len(registeredTenantServices) == 0 {
		t.Skip("no tenant services registered")
	}
	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	reportTenantScopeSkipped(recordingStore(&svcs, &notices, &warnings), []subscription{{ID: "sub"}}, nil, federatedNoGraph)

	if len(notices) != len(registeredTenantServices) {
		t.Fatalf("emitted %d notices; want one per registered tenant service (%d)",
			len(notices), len(registeredTenantServices))
	}
	for _, n := range notices {
		if n.Message == "" {
			t.Errorf("service %q got an empty notice", n.Service)
		}
	}

	// A suppressed DIRECTORY read is a coverage change, which store.ScanNotice's
	// own contract reserves for a warning: the customer's directory objects and
	// management-group tree are absent and nothing replaces them. Exactly ONE
	// warning, though — the same doc says a warning firing on every healthy scan
	// trains people to ignore the block, and under federation this fires on every
	// scan forever. Per-service fan-out would also grow the count as tenant
	// services are added, which is what the phase-wide gate exists to absorb.
	want := directoryReadTenantServices()
	if len(warnings) != 1 {
		t.Fatalf("emitted %d warnings (%+v); want exactly one for the phase", len(warnings), warnings)
	}
	for _, name := range want {
		if !strings.Contains(warnings[0].Message, name) {
			t.Errorf("the phase warning does not name %q, so an operator cannot tell what is missing: %q", name, warnings[0].Message)
		}
	}
	if len(svcs) != len(want) {
		t.Fatalf("reported %d service lines (%+v); want one per directory-reading service %v", len(svcs), svcs, want)
	}
	for _, r := range svcs {
		if r.status != store.ServiceOK {
			t.Errorf("service %q reported status %v; want ServiceOK", r.name, r.status)
		}
	}
}

// TestSkipNotices_OnlyDirectoryLossCarriesThePrefix pins the invariant every
// prefix-based assertion in this package rests on. Without it, dropping the
// prefix from a loss notice makes those checks vacuous rather than red — the
// same failure this file already shipped once with a quoted phrase.
//
// Three of the four arms are DEFINITIONAL as written: the loss constants are
// composed as directoryLossPrefix + "…", so they cannot fail without editing
// those definitions. That is the mutant they exist for — a later author
// spelling one out as a bare literal, which is how the convention erodes —
// and it is measured, not assumed. The dedup arm is the one with content
// independent of how the constants are spelled. What NONE of them sees is a
// FOURTH loss notice added without the prefix — three exist, and the fourth
// arm here is the dedup one, which is not a loss notice. The list is
// hand-enumerated because nothing in the type system carries the set.
func TestSkipNotices_OnlyDirectoryLossCarriesThePrefix(t *testing.T) {
	for _, n := range []struct{ label, msg string }{
		{"armSkipNotice", armSkipNotice},
		{"graphSkipNoticeUnnamed", graphSkipNoticeUnnamed},
		{"graphSkipNoticeMalformed", graphSkipNoticeMalformed},
	} {
		if !strings.HasPrefix(n.msg, directoryLossPrefix) {
			t.Errorf("%s does not carry directoryLossPrefix, so every check that tests for it silently stops seeing this notice: %q", n.label, n.msg)
		}
	}
	if strings.HasPrefix(dedupSkipNotice, directoryLossPrefix) {
		t.Errorf("dedupSkipNotice carries directoryLossPrefix, which marks a lost directory read; this phase loses nothing: %q", dedupSkipNotice)
	}
}

// TestReportTenantScopeSkipped_SeparatesDedupFromDirectoryLoss pins the
// distinction the notice text has to carry. A dedupOnly phase reads no
// directory: its data is Microsoft-shipped and every subscription stores its
// own copy when the tenant phase does not run, so handing it either
// directory-loss notice claims a loss that did not happen — for a service that
// then reports real counts under the same name.
func TestReportTenantScopeSkipped_SeparatesDedupFromDirectoryLoss(t *testing.T) {
	var dedup []string
	for _, svc := range registeredTenantServices {
		if svc.dedupOnly {
			dedup = append(dedup, svc.name)
		}
	}
	if len(dedup) == 0 {
		t.Skip("no dedup-only tenant services registered")
	}

	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	reportTenantScopeSkipped(recordingStore(&svcs, &notices, &warnings), []subscription{{ID: "sub"}}, nil, federatedNoGraph)

	byService := map[string]string{}
	for _, n := range notices {
		byService[n.Service] = n.Message
	}
	for _, name := range dedup {
		got := byService[name]
		// STRUCTURAL, and derived from production rather than quoted: every
		// directory-loss notice carries directoryLossPrefix and the dedup one
		// must not, so this catches a third loss kind added later as well as
		// the two that exist.
		if strings.HasPrefix(got, directoryLossPrefix) {
			t.Errorf("dedup-only service %q was handed a directory-loss notice: %q", name, got)
		}
		// CONTENT, and positive. The two checks above pin which BRANCH ran and
		// say nothing about what the message SAYS: comparing against
		// dedupSkipNotice reads the same constant the production code emits,
		// so rewriting that constant into a directory-loss sentence satisfies
		// both and satisfies the prefix test too, which is the mutant that
		// survived the previous form of this test. A positive assertion on the
		// wording is the only kind that goes red on a reword — and going red
		// on a reword is the point, since the reword is when the claim needs
		// re-reading.
		if !strings.Contains(got, "each subscription stores its own copy") {
			t.Errorf("dedup-only service %q notice does not say the rows are stored elsewhere, which is the whole reason this phase loses nothing: %q", name, got)
		}
		if got != dedupSkipNotice {
			t.Errorf("dedup-only service %q notice = %q; want dedupSkipNotice", name, got)
		}
		for _, w := range warnings {
			if w.Service == name {
				t.Errorf("dedup-only service %q was warned about; it reached the right answer by another route, so it inflates the warning count for nothing", name)
			}
		}
		for _, r := range svcs {
			if r.name == name {
				t.Errorf("dedup-only service %q got a zero-count service line beside the per-sub phase's real counts", name)
			}
		}
	}
}

// TestReportTenantScopeSkipped_HonoursFilter proves the --services filter is
// respected, so a run that asked for one service is not told about the others.
func TestReportTenantScopeSkipped_HonoursFilter(t *testing.T) {
	var svcs []serviceReport
	var notices []store.ScanNotice
	var warnings []store.ScanWarning
	reportTenantScopeSkipped(recordingStore(&svcs, &notices, &warnings), []subscription{{ID: "sub"}}, []string{"azure:microsoft.entra"}, federatedNoGraph)

	if len(notices) != 1 || notices[0].Service != "azure:microsoft.entra" {
		t.Fatalf("emitted %+v; want exactly one notice, for azure:microsoft.entra", notices)
	}
	// The phase warning must name only the filtered service — a run that asked
	// for one service is not told the others are missing.
	if len(warnings) != 1 {
		t.Fatalf("emitted %d warnings; want exactly one for the phase", len(warnings))
	}
	for _, svc := range registeredTenantServices {
		if svc.name != "azure:microsoft.entra" && strings.Contains(warnings[0].Message, svc.name) {
			t.Errorf("the phase warning names %q, which --services excluded: %q", svc.name, warnings[0].Message)
		}
	}
}

// TestPartiallyConfigured pins the fail-closed reading of a half-declared
// federation.
//
// The consequence is bigger than which credential gets built: tenantScopeEnabled
// reads configured() too, so treating a half-set contract as "not federated"
// silently re-enables the whole tenant phase AND unpins subscription
// enumeration — in the one deployment where both guards are load-bearing, off
// a single typo'd variable name.
func TestPartiallyConfigured(t *testing.T) {
	tests := []struct {
		name string
		cfg  wifConfig
		want bool
	}{
		{"nothing set: an operator's own machine", wifConfig{}, false},
		{"both halves set", federatedCfg(), false},
		{"client id alone", wifConfig{clientID: "client-guid"}, true},
		{"tenant id alone", wifConfig{tenantID: "our-tenant-guid"}, true},
		{"audience alone", wifConfig{audience: tokenExchangeAudience}, true},
		{"role arn alone", wifConfig{roleARN: "arn:aws:iam::1:role/r"}, true},
		{"session name alone", wifConfig{sessionName: "s"}, true},
		{"complete session, no identity", wifConfig{roleARN: "arn:aws:iam::1:role/r", sessionName: "s"}, true},
		// The graph tenant alone is the worst member to let through, and the
		// only one whose omission is a DISCLOSURE rather than a wrong
		// credential: configured() is false, so tenantScopeEnabled() is true,
		// every tenant service runs, and scanEntra reads whatever directory an
		// ambient credential authenticated in -- with no pin, because
		// graphTenantEnabled() is false too. Setting the variable that exists
		// to prevent that is what turns it on.
		{"graph tenant alone", wifConfig{graphTenantID: customerDirectory}, true},
		{"graph tenant beside a complete pair is not partial", wifConfig{clientID: "c", tenantID: "t", graphTenantID: customerDirectory}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.partiallyConfigured(); got != tt.want {
				t.Errorf("partiallyConfigured() = %v; want %v", got, tt.want)
			}
		})
	}
}

// TestNewAzureCredential_RefusesHalfSet asserts the refusal reaches the one
// call site that matters. Without it the scan proceeds on an ambient
// credential with every cross-tenant guard switched off.
func TestNewAzureCredential_RefusesHalfSet(t *testing.T) {
	_, err := newAzureCredential(t.Context(), wifConfig{clientID: "client-guid"})
	if !errors.Is(err, ErrIncompleteWIFConfig) {
		t.Fatalf("newAzureCredential() error = %v; want ErrIncompleteWIFConfig", err)
	}
}

// TestRetryCredential_BackoffUnitDefaultsToTheRealDelay covers the branch every
// other retry test bypasses: all of them set delay explicitly, so a mutant that
// returns r.delay unconditionally leaves production retrying four times with no
// pause at all — inside the replication window the retry exists to ride out —
// and the suite stays green.
func TestRetryCredential_BackoffUnitDefaultsToTheRealDelay(t *testing.T) {
	if got := (&retryCredential{}).backoffUnit(); got != federationPropagationDelay {
		t.Errorf("backoffUnit() = %v; want %v", got, federationPropagationDelay)
	}
}

// TestRetryCredential_KeepsRetryingInsideTheBudget is the half a
// one-exhaustion latch got wrong. One acquisition sleeps 1s+2s+3s while
// Microsoft describes replication in MINUTES, so giving up after a single
// exhausted acquisition abandons exactly the window the retry exists for: a
// credential that becomes valid at t+30s would fail the whole scan.
func TestRetryCredential_KeepsRetryingInsideTheBudget(t *testing.T) {
	prop := errors.New("AADSTS70021: No matching federated identity record found for presented assertion")
	errs := make([]error, federationPropagationAttempts*2)
	for i := range errs {
		errs[i] = prop
	}
	inner := &countingCredential{errs: errs}
	r := &retryCredential{inner: inner, delay: time.Millisecond, budget: time.Hour}

	for acquisition := 1; acquisition <= 2; acquisition++ {
		if _, err := r.GetToken(t.Context(), policy.TokenRequestOptions{}); err == nil {
			t.Fatalf("acquisition %d: GetToken() = nil error; want the propagation error", acquisition)
		}
		if want := federationPropagationAttempts * acquisition; inner.calls != want {
			t.Fatalf("after acquisition %d: %d inner calls; want %d — a window longer than one acquisition must still be retried",
				acquisition, inner.calls, want)
		}
	}
}

// TestRetryCredential_GivesUpPastTheBudget is the other half. cachingCredential
// caches successes and never failures, so a permanent misconfiguration — the
// same code with a wrong subject, which never stops — would otherwise pay the
// whole backoff on every scope and every acquisition for the length of a scan
// that is going to fail anyway.
func TestRetryCredential_GivesUpPastTheBudget(t *testing.T) {
	prop := errors.New("AADSTS70021: No matching federated identity record found for presented assertion")
	errs := make([]error, federationPropagationAttempts*3)
	for i := range errs {
		errs[i] = prop
	}
	inner := &countingCredential{errs: errs}
	r := &retryCredential{inner: inner, delay: time.Millisecond, budget: time.Nanosecond}

	if _, err := r.GetToken(t.Context(), policy.TokenRequestOptions{}); err == nil {
		t.Fatal("GetToken() = nil error; want the propagation error")
	}
	first := inner.calls
	if first >= federationPropagationAttempts {
		t.Errorf("first acquisition made %d calls; want fewer than the cap %d once the budget is spent", first, federationPropagationAttempts)
	}

	if _, err := r.GetToken(t.Context(), policy.TokenRequestOptions{}); err == nil {
		t.Fatal("GetToken() = nil error on the second acquisition")
	}
	if got := inner.calls - first; got != 1 {
		t.Errorf("second acquisition made %d calls; want 1 — past the budget the failure is reported immediately", got)
	}
}

// stubWebIdentityTokenAPI records the request the assertion callback builds.
type stubWebIdentityTokenAPI struct {
	got   *sts.GetWebIdentityTokenInput
	token *string
	err   error
}

func (s *stubWebIdentityTokenAPI) GetWebIdentityToken(_ context.Context, in *sts.GetWebIdentityTokenInput, _ ...func(*sts.Options)) (*sts.GetWebIdentityTokenOutput, error) {
	s.got = in
	if s.err != nil {
		return nil, s.err
	}
	return &sts.GetWebIdentityTokenOutput{WebIdentityToken: s.token}, nil
}

// TestAssertionCallback_BuildsTheRequestEntraAccepts pins the three parameters
// that decide whether the exchange works at all. Each fails only at exchange
// time, with an Entra error naming none of them.
func TestAssertionCallback_BuildsTheRequestEntraAccepts(t *testing.T) {
	stub := &stubWebIdentityTokenAPI{token: aws.String("jwt")}
	got, err := assertionCallback(stub, tokenExchangeAudience)(t.Context())
	if err != nil {
		t.Fatalf("assertion callback error = %v", err)
	}
	if got != "jwt" {
		t.Errorf("assertion = %q; want the token STS returned", got)
	}
	// RS256 is not a preference: Entra supports it alone for workload identity
	// federation, while AWS's own examples use ES384 and the parameter is
	// required with no default.
	if alg := aws.ToString(stub.got.SigningAlgorithm); alg != "RS256" {
		t.Errorf("SigningAlgorithm = %q; want RS256 — Entra rejects anything else", alg)
	}
	if ttl := aws.ToInt32(stub.got.DurationSeconds); ttl < 300 {
		t.Errorf("DurationSeconds = %d; want at least 300 — Entra validates exp against ITS clock, so a tight TTL expires on skew", ttl)
	}
	if len(stub.got.Audience) != 1 || stub.got.Audience[0] != tokenExchangeAudience {
		t.Errorf("Audience = %v; want exactly [%s]", stub.got.Audience, tokenExchangeAudience)
	}
}

// TestAssertionCallback_RefusesAnEmptyToken guards the shape azidentity would
// otherwise present to Entra as a client assertion.
func TestAssertionCallback_RefusesAnEmptyToken(t *testing.T) {
	for name, stub := range map[string]*stubWebIdentityTokenAPI{
		"nil token":   {token: nil},
		"empty token": {token: aws.String("")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := assertionCallback(stub, tokenExchangeAudience)(t.Context()); err == nil {
				t.Fatal("assertion callback = nil error; want a refusal")
			}
		})
	}
}

// TestAssertionCallback_UsesTheConfiguredAudience proves the audience is
// threaded rather than hardcoded at the call.
func TestAssertionCallback_UsesTheConfiguredAudience(t *testing.T) {
	stub := &stubWebIdentityTokenAPI{token: aws.String("jwt")}
	if _, err := assertionCallback(stub, "api://custom")(t.Context()); err != nil {
		t.Fatalf("assertion callback error = %v", err)
	}
	if len(stub.got.Audience) != 1 || stub.got.Audience[0] != "api://custom" {
		t.Errorf("Audience = %v; want [api://custom]", stub.got.Audience)
	}
}

// TestRetryCredential_BudgetMeasuresTheCurrentStreak pins the difference
// between a streak window and an absolute deadline.
//
// Microsoft's guidance is to keep retrying even after a request has already
// succeeded, because a later one can still land on a node with stale data. An
// unreset stamp turns the budget into a per-credential deadline: one transient
// failure that resolved on its own retry arms it, and every propagation error
// more than a budget later gets a single attempt — abandoning exactly that
// case.
func TestRetryCredential_BudgetMeasuresTheCurrentStreak(t *testing.T) {
	prop := errors.New("AADSTS70021: No matching federated identity record found for presented assertion")
	// One transient that resolves, then — after the budget would have expired —
	// a fresh streak.
	inner := &countingCredential{errs: []error{prop, nil, prop, prop}}
	r := &retryCredential{inner: inner, delay: time.Millisecond, budget: time.Nanosecond}

	if _, err := r.GetToken(t.Context(), policy.TokenRequestOptions{}); err != nil {
		t.Fatalf("first acquisition: GetToken() error = %v; want success on the retry", err)
	}
	if r.firstFailure.Load() != 0 {
		t.Fatal("a resolved transient left the budget armed; later propagation errors would get no retry at all")
	}

	before := inner.calls
	if _, err := r.GetToken(t.Context(), policy.TokenRequestOptions{}); err == nil {
		t.Fatal("second acquisition: GetToken() = nil error; want the propagation error")
	}
	if got := inner.calls - before; got < 2 {
		t.Errorf("second acquisition made %d calls; want it retried — a new streak gets its own budget", got)
	}
}

// TestIncompleteWIFConfig_NamesEveryCountedVariable pins the correspondence
// between what partiallyConfigured COUNTS and what ErrIncompleteWIFConfig
// NAMES.
//
// The message carries a hand-written list over a live set. It used to say "Any
// DISCO_AZURE_ variable in this contract", which was a rule rather than a list
// and stopped being true when DISCO_AZURE_SUBSCRIPTION_TENANT_ID arrived as
// the one exception.
//
// Three assertions, and the third is the staleness guard. Each listed variable
// must make partiallyConfigured true ALONE and must be named in the message —
// that catches a removal. The exception must be named too. And every field of
// wifConfig must be accounted for by name in this test, which is what catches
// an ADDITION: a new member of the struct that nobody classified turns this
// red, whereas a table checked only against itself stays green forever. It
// cannot see a new field added to partiallyConfigured's disjunction without a
// new struct field, which is not a reachable shape — the predicate reads
// fields.
func TestIncompleteWIFConfig_NamesEveryCountedVariable(t *testing.T) {
	counted := []struct {
		env   string
		field string
		cfg   wifConfig
	}{
		{envWIFAudience, "audience", wifConfig{audience: "api://x"}},
		{envWIFRoleARN, "roleARN", wifConfig{roleARN: "arn:aws:iam::111111111111:role/r"}},
		{envWIFSessionName, "sessionName", wifConfig{sessionName: "s"}},
		{envGraphTenantID, "graphTenantID", wifConfig{graphTenantID: customerDirectory}},
	}
	for _, c := range counted {
		t.Run(c.env, func(t *testing.T) {
			if !c.cfg.partiallyConfigured() {
				t.Fatalf("partiallyConfigured() = false for %s alone", c.env)
			}
			if !strings.Contains(ErrIncompleteWIFConfig.Error(), c.env) {
				t.Errorf("ErrIncompleteWIFConfig does not name %s; the operator is told to fix a variable the message never mentions", c.env)
			}
		})
	}

	// The exception, stated in the message as well as in the predicate: an
	// operator who set only this one must not read the refusal as being about
	// it. Its own behaviour is pinned in subscription_binding_test.go.
	if !strings.Contains(ErrIncompleteWIFConfig.Error(), envSubscriptionTenantID) {
		t.Errorf("ErrIncompleteWIFConfig does not mention %s; the exception is invisible", envSubscriptionTenantID)
	}

	// Every field classified. clientID and tenantID are the pair the whole
	// contract is about, subscriptionTenantID is the exception, and the rest
	// are the counted table above.
	classified := map[string]bool{"clientID": true, "tenantID": true, "subscriptionTenantID": true}
	for _, c := range counted {
		classified[c.field] = true
	}
	cfgType := reflect.TypeOf(wifConfig{})
	for i := range cfgType.NumField() {
		name := cfgType.Field(i).Name
		if !classified[name] {
			t.Errorf("wifConfig.%s is not classified by this test: decide whether partiallyConfigured counts it, then add it to `counted` or to `classified`", name)
		}
	}
}
