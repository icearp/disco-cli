// Package azure implements cloud resource discovery for Microsoft Azure via
// per-service API calls using the Azure SDK for Go (arm* packages), following
// the two-phase scan pattern.
package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/icearp/disco-cli/internal/providers"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	// maxConcurrentSubscriptions caps parallel subscription scans.
	maxConcurrentSubscriptions = 10
	// maxConcurrentServices caps parallel service scanners per subscription.
	maxConcurrentServices = 10
	// serviceTimeout is the per-service hard deadline. azure:microsoft.compute now covers VMSS,
	// galleries, and hosting fan-outs in addition to core compute types, so this must
	// be generous enough for large subscriptions.
	serviceTimeout = 30 * time.Minute
)

// azHTTPClient pools connections to the ARM control plane. Every arm* client
// targets the single host management.azure.com, but Go's default transport
// keeps only MaxIdleConnsPerHost=2 — under the scan's service+fanout
// concurrency this forces most requests to pay a fresh TCP+TLS handshake (or
// block for a free connection), dominating wall-clock even for empty
// services. Raise the per-host idle pool; the scan's own semaphores already
// bound in-flight concurrency, so MaxConnsPerHost stays unbounded.
var azHTTPClient = newAzHTTPClient()

func newAzHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 200
	tr.MaxIdleConnsPerHost = 100
	tr.MaxConnsPerHost = 0
	tr.IdleConnTimeout = 90 * time.Second
	return &http.Client{Transport: tr}
}

// azClientOptions is shared by all arm* SDK client constructors. The retry
// policy reduces the base delay from the SDK default (800ms) to 500ms and
// allows up to 4 attempts — enough headroom for transient ARM errors without
// stalling the fanout critical path. Transport is the pooled azHTTPClient.
var azClientOptions = &arm.ClientOptions{
	ClientOptions: azcore.ClientOptions{
		Transport: azHTTPClient,
		Retry: policy.RetryOptions{
			MaxRetries:    4,
			RetryDelay:    500 * time.Millisecond,
			MaxRetryDelay: 30 * time.Second,
		},
	},
}

func init() { providers.Register(&Scanner{}) }

// Scanner implements providers.Scanner for Azure.
type Scanner struct {
	serviceFilter        []string // nil = scan all registered services
	subscriptionOverride []string // nil = config-then-enumerate; non-nil = pinned, no auto-enumeration
}

// Name implements providers.Scanner.
func (s *Scanner) Name() string { return "azure" }

// LongDescription is the help text for `disco scan azure --help`.
func (s *Scanner) LongDescription() string {
	return `Scan Azure resources across reachable subscriptions.

Subscription scope comes from DefaultAzureCredential (az login, managed
identity, env vars) or the explicit 'subscriptions:' list in config.yaml.
--subscriptions pins the scan to exactly the listed subscription IDs and
disables auto-enumeration (fail-closed) — use it to constrain a shared
credential delegated across multiple tenants to one tenant's subscriptions.
There is no --regions / --profile flag — Azure scopes per
subscription/resource group, configured statically. --services narrows
the scanner set when iterating on one provider.

Keyless auth (Entra workload identity federation): on AWS, set
DISCO_AZURE_WIF_CLIENT_ID + DISCO_AZURE_WIF_TENANT_ID to exchange an AWS
STS web identity token for an Entra token via a federated identity
credential — no client secret is stored. To present a named session, so
the Entra trust names one identity rather than whatever the platform
chose, also set DISCO_AZURE_WIF_ROLE_ARN + DISCO_AZURE_WIF_SESSION_NAME.
DISCO_AZURE_WIF_AUDIENCE overrides the token audience and rarely needs to
be set. Any DISCO_AZURE_ variable in this contract set WITHOUT both required
ids is refused rather than ignored — including DISCO_AZURE_GRAPH_TENANT_ID,
described below — because a half-declared federation would otherwise fall
back to a credential the tenant-scope guards do not apply to.

Under this mode the tenant-scope services (Entra ID directory objects,
management groups, and the tenant-wide fetch of Microsoft's built-in role,
policy and policy-set definitions) are SKIPPED by default. The reason is
what this build can CHECK, not what your setup is: nothing here confirms
the federated identity belongs to the tenant being scanned, so tenant-scope
results could describe a different directory. The case that motivates it is
Azure Lighthouse, where the token authenticates in the MANAGING tenant and
only subscription scope is delegated. It applies just the same when you
federate into your OWN tenant, where the skip is unnecessary.

Set DISCO_AZURE_GRAPH_TENANT_ID to the GUID of the directory you want Entra
ID objects read from, and the Entra ID services run again. That is not a
trust setting: every Microsoft Graph token is then issued FOR that directory
by name, and the scan refuses to store anything if a token comes back issued
for a different one. The Entra application must be permitted to issue tokens
there — in the Lighthouse case, an administrator of that directory grants it
admin consent.

What stays skipped whatever that variable says does so for reasons that are
not the same one. Management groups read through a tenant-root Azure Resource
Manager call, which names no directory, so it answers about whichever one the
credential authenticated in and there is nothing to point at the customer and
nothing to check. The tenant-wide fetch of Microsoft's built-in role, policy
and policy-set definitions loses nothing at all: it is a deduplication pass
rather than a directory read, and each subscription stores its own copy
instead. Role ASSIGNMENTS are unaffected either way: they are read per
subscription, inside the delegation.

Subscriptions must also be named explicitly, by --subscriptions or by the
config list: one delegated credential can see MANY customers' subscriptions,
so auto-discovery is refused rather than left to guess which this scan meant.

Examples:
  disco scan azure
  disco scan azure --subscriptions 00000000-0000-0000-0000-000000000000
  disco scan azure --services azure:microsoft.compute,azure:microsoft.network`
}

// ServiceFilterExample is the --services example shown in azure scan help.
func (s *Scanner) ServiceFilterExample() string {
	return "azure:microsoft.compute,azure:microsoft.network"
}

// ScopeColumnWidth pins the scan progress scope column to fit a subscription UUID.
func (s *Scanner) ScopeColumnWidth() int { return 36 }

// SetServiceFilter restricts the scan to the named services (e.g. "azure:microsoft.compute", "azure:microsoft.network").
// An empty or nil slice scans all registered services.
func (s *Scanner) SetServiceFilter(services []string) { s.serviceFilter = services }

// SetSubscriptionOverride pins the scan to exactly the given subscription IDs,
// disabling auto-enumeration (fail-closed). Implements providers.SubscriptionOverrider.
// A nil slice leaves the default config-then-enumerate behavior intact.
func (s *Scanner) SetSubscriptionOverride(subscriptionIDs []string) {
	s.subscriptionOverride = subscriptionIDs
}

// ServiceNames returns the names of all services this scanner will report.
func (s *Scanner) ServiceNames() []string {
	svcs := filteredServices(s.serviceFilter)
	names := make([]string, len(svcs))
	for i, svc := range svcs {
		names[i] = svc.name
	}
	return names
}

// Scan discovers all Azure resources across all configured subscriptions.
// Subscriptions are scanned in parallel, bounded by maxConcurrentServices.
func (s *Scanner) Scan(ctx context.Context, st *store.Store, scanID string) error {
	// Read the federation contract once and thread it down, so every gate in
	// this scan agrees about which tenant the credential speaks for.
	wif := wifEnv()
	subs, cred, err := loadSubscriptions(ctx, s.subscriptionOverride, wif)
	if err != nil {
		// Redacted like every other credential failure. This one runs before
		// any scanner, so it reaches the scan record through scanrun rather
		// than through formatAzureError, and it is the likeliest of all of
		// them to carry disco's own identifiers. A configuration refusal is
		// NOT redacted — it names the variable to fix — so it keeps its wrap
		// and stays matchable with errors.Is. See redactCredentialError.
		if red := redactCredentialError(err); red != "" {
			return errors.New("azure: load subscriptions: " + red)
		}
		return fmt.Errorf("azure: load subscriptions: %w", err)
	}
	s.scanWithCredential(ctx, st, scanID, subs, cred, wif)
	return nil
}

// scanWithCredential runs the scan against an already-resolved credential.
//
// Split from [Scanner.Scan] so the federation gates can be exercised with a
// stub credential: each of them decides whether a token is requested at all,
// which is not observable from outside the network calls it prevents.
func (s *Scanner) scanWithCredential(ctx context.Context, st *store.Store, scanID string, subs []subscription, cred azcore.TokenCredential, wif wifConfig) {
	// Resolve the tenant GUID once and stamp it onto every subscription so
	// tenant-scope scanners/resolvers can store tenant-identical resources
	// (management groups, built-in role/policy definitions) under one account
	// instead of duplicating per subscription. Best-effort: on failure the
	// empty tenantID disables dedup and each subscription falls back to
	// storing its own copy (current behavior).
	//
	// Skipped entirely under a federated credential (wif.tenantScopeEnabled,
	// which is what the code below actually tests): the `tid` claim names the
	// credential's OWN tenant, which under Lighthouse is not the customer's,
	// and which nothing here can confirm either way. Stamping it
	// would both mislabel their rows and make the per-subscription scanners
	// skip built-in role/policy definitions on the assumption that a tenant
	// service is storing them — which is precisely what is disabled. Leaving
	// it empty selects the documented per-subscription fallback, so no data is
	// lost. See wifConfig.tenantScopeEnabled.
	if wif.tenantScopeEnabled() {
		if tenantID, terr := tenantIDFromCredScope(ctx, cred, armScope); terr != nil {
			st.ReportWarning(store.ScanWarning{
				Provider: "azure", Service: "scan", Scope: "tenant",
				Message: "resolve tenant id: " + formatAzureError(terr),
			})
		} else {
			// Friendly tenant name is a separate best-effort Graph call; degrade
			// to the GUID (left as empty tenantName) when directory read is
			// unavailable.
			tenantName, _ := tenantDisplayName(ctx, cred)
			for i := range subs {
				subs[i].tenantID = tenantID
				subs[i].tenantName = tenantName
			}
		}
	}

	// Plain WaitGroup (not errgroup): a per-subscription failure is reported
	// and skipped rather than cancelling the other subscriptions' scans. Fatal
	// conditions (load-subscriptions) already returned early above.
	sem := semaphore.NewWeighted(maxConcurrentSubscriptions)
	var wg sync.WaitGroup

	// Tenant-scope services (e.g. Entra ID via Microsoft Graph) populate the
	// principal resources that per-sub phase-2 resolvers (RBAC role assignments)
	// FK-match against. The dependency is narrow — only the phase-2 authorization
	// resolver consumes Entra rows — so instead of running the tenant phase
	// serially before the fan-out, we run it concurrently and gate only each
	// subscription's resolver phase on entraDone (see scanSubscription). Entra's
	// latency is thus hidden behind phase-1 scanning instead of added to total
	// scan time.
	//
	// close(entraDone) is deferred FIRST so it runs LAST — after reportPanic
	// recovers — guaranteeing waiters are released even if the tenant phase panics
	// (no deadlock). The channel close happens-after every principal upsert, so
	// phase-2 readers always observe a fully-written principal set.
	entraDone := make(chan struct{})
	wg.Go(func() {
		defer close(entraDone)
		defer reportPanic(st, "entra", tenantScopeLabel(subs))
		runTenantPhase(ctx, subs, cred, wif, s.serviceFilter, st, scanID)
	})

	for i := range subs {
		sub := &subs[i]
		wg.Go(func() {
			defer reportPanic(st, "scan", sub.scopeLabel())
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			if err := scanSubscription(ctx, sub, cred, s.serviceFilter, st, scanID, entraDone); err != nil {
				st.ReportError(store.ScanError{
					Provider: "azure", Service: "scan", Scope: sub.scopeLabel(),
					Message: formatAzureError(err),
				})
			}
		})
	}
	wg.Wait()

	// Stitch the top three hierarchy tiers (management-group → subscription →
	// resource-group) once every subscription's phase-1 and the tenant phase have
	// written their rows — the only point at which both endpoints of each
	// cross-phase closure pair exist in the store.
	stitchTopHierarchy(ctx, subs, cred, st, wif)
}

// scanSubscription runs phase 1 (resources + hierarchy) then phase 2
// (relationships) for one subscription.
func scanSubscription(ctx context.Context, sub *subscription, cred azcore.TokenCredential, services []string, st *store.Store, scanID string, entraDone <-chan struct{}) error {
	// scanResourceGroups (RG parents of all resources) and the RP-registration
	// probe are independent ARM list calls, both on the critical path before any
	// service scanner can start. Run them concurrently so the service loop is
	// gated on the slower of the two, not their sum.
	//
	// scanResourceGroups failure is reported and the scan continues — one
	// service's (even the RG list's) error must never abort the subscription's
	// other scanners. The RP probe is the ONE exception, and only for a 401:
	// see the isAuthenticationFailure arm below.
	//
	// The probe reads RP registration state once per subscription. ARM allows
	// LIST on unregistered providers (empty 200, no error), so the per-call error
	// path can't see a NotRegistered RP — this proactive gate is the only signal,
	// the Azure analog of AWS's phase-0 "service enabled?" gate. A probe failure
	// is non-fatal: regProviders stays nil and every service is scanned with the
	// reactive error classifier as fallback. Non-fatal EXCEPT on a 401, which is
	// not a gap in the probe's own permissions but a statement that this
	// credential has no standing for the subscription at all.
	var (
		regProviders map[string]bool
		rgListed     bool
		rgErr, perr  error
		preWG        sync.WaitGroup
	)
	preWG.Go(func() {
		defer reportPanic(st, "resourcegroups", sub.scopeLabel())
		rgListed, rgErr = scanResourceGroups(ctx, sub, cred, st, scanID)
	})
	preWG.Go(func() {
		defer reportPanic(st, "providers", sub.scopeLabel())
		regProviders, perr = loadRegisteredProviders(ctx, sub.ID, cred)
	})
	preWG.Wait()
	if rgErr != nil {
		st.ReportError(store.ScanError{
			Provider: "azure", Service: "resourcegroups", Scope: sub.scopeLabel(),
			Message: formatAzureError(rgErr),
		})
	}
	if perr != nil {
		// A 401 on the RP probe means the presented token has no standing for
		// this subscription -- under Lighthouse, that it was never delegated to
		// this principal, or that the delegation is gone. Every service scanner
		// would then issue a list call that cannot succeed, one near-identical
		// warning each, which on the real never-delegated subscription
		// OVERFLOWED the persisted warnings array and evicted everything worth
		// reading. Do not read a call count off that run: the recorded 200 is
		// scanrun.maxPersistedWarnings, a cap with the truncation marker set.
		// The population is the per-subscription registry -- regenerate it with
		// `grep -h 'registerService(serviceEntry{' internal/providers/azure/*.go |
		// wc -l` and NOT with `grep -c`, which prints one count per file over a
		// glob and no total -- and it bounds the count only loosely in both
		// directions -- a scanner
		// can issue several list calls, and an RG-fanout scanner issues none
		// once the RG list itself refused. Returning here costs those calls
		// nothing and yields exactly ONE scan error for the subscription, which
		// is also the signal a consumer needs to tell "unreachable" apart from
		// "empty" -- the two are otherwise identical, both being a scan that
		// finishes clean with no rows.
		//
		// Only a 401. A 403 stays the existing warning: the principal IS
		// recognised and some other service may well be readable, so gating the
		// whole subscription on one narrow role would lose real inventory. Note
		// this is the ONLY place a 401 is treated as anything but a per-call
		// skip -- isSkippableScanError still absorbs one everywhere else.
		//
		// rgListed is the corroboration, and it is why the gate is a
		// conjunction. A successful resource-group list proves the token WAS
		// accepted for this subscription, so a 401 from the providers endpoint
		// alone is about that endpoint and must not cost the customer every row
		// they can actually see.
		//
		// What corroboration cannot rule out is a 401 both calls share that is
		// not a delegation problem -- an in-flight tenant transfer, whose ARM
		// body says propagation takes up to an hour, refuses both. That scan
		// reports the account unreachable for one cycle and heals on the next
		// clean one, so the corroboration buys the asymmetric case only and the
		// symmetric one is accepted.
		//
		// These calls DO share one frozen token, and a retry inside the scan is
		// futile -- but the reason is ours, not azcore's. azcore's bearer-token
		// policy calls Expire() on any 401 (runtime/policy_bearer_token.go,
		// handleChallenge), and refreshes early only on its own schedule --
		// shouldRefresh takes the five-minute window only when RefreshOn is
		// zero, which azidentity's confidential client leaves it only when the
		// token response carried no refresh_in, which is the only thing MSAL
		// fills it from, so neither window is a fixed fact;
		// Expire() clears only the policy's own copy, and the next GetToken
		// reads newCachingCredential (azure_credential.go), which returns the
		// memoised token for the scope while it is more than five minutes from
		// expiry and has no invalidation path. Read the credential this scan was
		// handed before reasoning about token lifetime -- a correction written
		// off the module cache alone got this backwards once.
		//
		// rgErr is already on the record above, and deliberately so even when the
		// refusal follows: `rgListed` tracks the CALL, not the error, so a page
		// that came back and then failed to STORE leaves rgListed true and
		// SUPPRESSES this refusal -- which is the point, a database fault of ours
		// must not be reported as the customer's principal losing access. The
		// scan record then carries both the store failure and, if it fired, this
		// refusal, and says which is which by service name.
		//
		// One fault of ours escapes that: a PANIC in the resource-group goroutine
		// never returns, so rgListed stays false however many pages came back,
		// and a concurrent probe 401 then refuses the subscription. It stays
		// distinguishable on the record -- reportPanic files its own
		// `resourcegroups` ScanError beside this one -- so the guarantee above is
		// about the ERROR path, not about every way our side can fail.
		if subscriptionUnreachable(perr, rgListed) {
			return unreachableSubscriptionError(perr)
		}
		st.ReportWarning(store.ScanWarning{
			Provider: "azure", Service: "providers", Scope: sub.scopeLabel(),
			Message: formatAzureError(perr),
		})
	}

	// Scan all registered service types in parallel, bounded by
	// maxConcurrentServices. A plain WaitGroup (not errgroup) so one service's
	// failure is reported and skipped, never cancelling its siblings — see
	// providers/CLAUDE.md "Errors never abort scan".
	sem := semaphore.NewWeighted(maxConcurrentServices)
	var wg sync.WaitGroup
	for _, svc := range filteredServices(services) {
		// RP not registered in this subscription → mark disabled and skip the
		// scanner entirely (no goroutine, no list call), mirroring AWS.
		if providerDisabled(regProviders, svc.name) {
			st.ReportService(svc.name, sub.scopeLabel(), 0, 0, 0, 0, store.ServiceDisabled)
			continue
		}
		wg.Go(func() {
			defer reportPanic(st, svc.name, sub.scopeLabel())
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(ctx, serviceTimeout)
			defer cancel()
			var newC, changedC atomic.Int64
			total, _, err := svc.fn(svcCtx, sub, cred, st.WithUpsertCounters(&newC, &changedC), scanID)
			switch {
			case err == nil:
				st.ReportService(svc.name, sub.scopeLabel(), total, int(newC.Load()), int(changedC.Load()), 0, store.ServiceOK)
			case errors.Is(err, store.ErrStoreWrite):
				// Ahead of every skip arm: a failed store write is not an
				// Azure-side condition. Without this, a database outage would
				// match a skip arm below and the scan would report success
				// having persisted nothing. Mirrors the AWS dispatcher.
				st.ReportError(store.ScanError{
					Provider: "azure", Service: svc.name, Scope: sub.scopeLabel(),
					Message: formatAzureError(err),
				})
				st.ReportService(svc.name, sub.scopeLabel(), total, int(newC.Load()), int(changedC.Load()), 1, store.ServiceOK)
			case errors.Is(err, errServiceNotRegistered) && total == 0:
				// Service not available in this subscription (RP unregistered or
				// resource type absent) and nothing was scanned — mark the
				// service disabled (no error, no warning), the Azure analog of
				// AWS's errServiceDisabled. The total==0 guard ensures a merged
				// service that already scanned data in another phase is never
				// blanked.
				st.ReportService(svc.name, sub.scopeLabel(), 0, 0, 0, 0, store.ServiceDisabled)
			default:
				st.ReportError(store.ScanError{
					Provider: "azure", Service: svc.name, Scope: sub.scopeLabel(),
					Message: formatAzureError(err),
				})
				st.ReportService(svc.name, sub.scopeLabel(), total, int(newC.Load()), int(changedC.Load()), 1, store.ServiceOK)
			}
		})
	}
	wg.Wait()

	// Phase 1c: API-driven cross-cutting resolvers (e.g. diagnostic-settings)
	// run AFTER phase-1 services so st.ListResources returns the full set.
	// Errors degrade to ReportError; the resolve phase still proceeds.
	for _, ar := range registeredAPIResolvers {
		edges, aerr := ar.fn(ctx, sub, cred, st)
		if aerr != nil {
			st.ReportError(store.ScanError{
				Provider: "azure", Service: ar.name, Scope: sub.scopeLabel(),
				Message: formatAzureError(aerr),
			})
			st.ReportService(ar.name, sub.scopeLabel(), 0, edges, 0, 1, store.ServiceOK)
			continue
		}
		st.ReportService(ar.name, sub.scopeLabel(), 0, edges, 0, 0, store.ServiceOK)
	}

	// Phase 2 resolvers consume Entra principals written by the tenant phase, which
	// runs concurrently with phase-1 scanning. Block until it completes so
	// authorization edges (assignment -uses-> principal) are complete. ctx
	// cancellation releases the wait so a cancelled scan never hangs here.
	waitForTenant(ctx, entraDone)

	st.ReportResolveStart("azure")
	var counter atomic.Int64
	resolveRelationships(ctx, sub, st.WithRelCounter(&counter))
	st.ReportResolveComplete("azure", int(counter.Load()))
	return nil
}

// resolveRelationships is phase 2 for Azure: derive edges between resources
// that have already been written to the DB. Resolvers run concurrently
// (bounded by maxConcurrentResolvers) since they operate on disjoint resource
// types; a failure in one is reported and does not stop the others — partial
// graph beats no graph.
func resolveRelationships(ctx context.Context, sub *subscription, st *store.Store) {
	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentResolvers)
	for _, r := range registeredResolvers {
		resolverLabel := r.name
		if resolverLabel == "" {
			resolverLabel = "resolve"
		}
		g.Go(func() error {
			defer reportPanic(st, resolverLabel, sub.scopeLabel())
			// Each resolver gets its own buffered store (independent buffer) so
			// concurrent resolvers stay isolated; flush collapses the per-edge
			// autocommit serialisation into one tx per resolver.
			bs := st.BeginRelBuffer()
			if err := r.fn(sub, bs); err != nil {
				st.ReportError(store.ScanError{
					Provider: "azure", Service: resolverLabel, Scope: sub.scopeLabel(), Message: formatAzureError(err),
				})
			}
			if ferr := bs.FlushRelBuffer(); ferr != nil {
				st.ReportError(store.ScanError{
					Provider: "azure", Service: resolverLabel, Scope: sub.scopeLabel(), Message: formatAzureError(ferr),
				})
			}
			return nil // resolver errors are reported, never abort siblings
		})
	}
	_ = g.Wait()
}

// waitForTenant blocks until the tenant phase (Entra) completes (done closed)
// or ctx is cancelled. It's the entire synchronization surface between the
// concurrent tenant goroutine and each subscription's phase-2 resolver —
// split out so the ordering invariant is unit-testable without a live scan.
// Returns on ctx cancellation so a cancelled scan never hangs; downstream
// resolvers honour the cancelled ctx themselves.
func waitForTenant(ctx context.Context, done <-chan struct{}) {
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// reportPanic recovers a panicking scan goroutine and reports it as a scan
// error instead of crashing the process. Scanner/resolver extract closures
// dereference deeply-nested SDK pointer fields; a nil deref must degrade to a
// reported error for that service/scope, never abort the scan — the
// panic-case extension of the "errors never abort scan" contract
// (providers/CLAUDE.md). Call deferred.
func reportPanic(st *store.Store, service, scope string) {
	if r := recover(); r != nil {
		// A recovered value is often an error, and a panicked *azcore.ResponseError
		// renders its whole ARM body -- which for a 401 names disco's own directory
		// as the presented issuer, on the CUSTOMER's scan record. This was the last
		// path in the package reaching store.ScanError without passing either
		// chokepoint, and both preWG goroutines defer it.
		// fmt.Sprintf("%v", r) is panic-SAFE by construction: fmt recovers a
		// panicking Error() and prints <nil> for a nil pointer receiver. It is
		// NOT hang-safe, and it sits outside every recover -- an Error() that
		// blocks stalls this handler, then preWG.Wait, then the whole scan, with
		// no record written. No recover can catch that; it is named so this
		// comment does not read as a complete safety argument.
		// formatAzureError gives that up, and this runs inside a deferred
		// recover that sync.WaitGroup.Go does not guard -- so a panic HERE is
		// unrecovered and takes the whole scan down, turning one subscription's
		// failure into total loss for every other. Measured: panicking a typed
		// nil (*azcore.ResponseError)(nil) satisfies r.(error), clears the
		// err == nil guard, and errors.As MATCHES it and sets respErr nil, so
		// the first field read dereferences nil.
		text := fmt.Sprintf("%v", r)
		if err, ok := r.(error); ok {
			func() {
				// LOAD-BEARING, not belt-and-braces: a BARE typed-nil error
				// reaches formatAzureError from the r.(error) above, and the
				// respErr != nil guards in azure_errors.go do not save it --
				// every predicate there ends in a bare err.Error(), which
				// dereferences the receiver. Deleting this recover turns one
				// subscription's panic into an unrecovered second panic, and
				// sync.WaitGroup.Go installs no recover of its own. Pinned by
				// TestReportPanic_SurvivesAPanickedTypedNil.
				//
				// Attributed rather than discarded: a nil deref in OUR formatter
				// would otherwise record a bare "panic: <nil>", indistinguishable
				// from a scanner panicking a typed nil.
				defer func() {
					if p := recover(); p != nil {
						text = fmt.Sprintf("%v [formatting the panic value failed: %v]", r, p)
					}
				}()
				text = formatAzureError(err)
			}()
		}
		st.ReportError(store.ScanError{
			Provider: "azure", Service: service, Scope: scope,
			Message: "panic: " + sanitizeForScanRecord(text),
		})
	}
}

// filteredServices returns the services to run. When filter is non-empty, only
// services whose name appears in filter are returned.
func filteredServices(filter []string) []serviceEntry {
	if len(filter) == 0 {
		return registeredServices
	}
	allowed := make(map[string]bool, len(filter))
	for _, name := range filter {
		allowed[name] = true
	}
	var out []serviceEntry
	for _, svc := range registeredServices {
		if allowed[svc.name] {
			out = append(out, svc)
		}
	}
	return out
}

// loadRegisteredProviders returns a lowercased ARM-namespace → isRegistered map
// for the subscription. ARM allows LIST on unregistered RPs (they return an
// empty 200, not an error), so this is the only signal that a service is
// genuinely not available in the sub — the Azure analog of AWS's phase-0
// "service enabled?" gate. A nil map (probe failed) means "don't gate": fall
// back to scanning every service and classifying per-call errors reactively.
func loadRegisteredProviders(ctx context.Context, subID string, cred azcore.TokenCredential) (map[string]bool, error) {
	client, err := armresources.NewProvidersClient(subID, cred, azClientOptions)
	if err != nil {
		return nil, err
	}
	return registeredProvidersFromPager(ctx, client.NewListPager(nil))
}

// registeredProvidersFromPager drains a Providers/List pager into the
// namespace→registered map. Split from loadRegisteredProviders so tests can
// drive it with an armresources fake transport.
func registeredProvidersFromPager(ctx context.Context, pager *runtime.Pager[armresources.ProvidersClientListResponse]) (map[string]bool, error) {
	out := map[string]bool{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range page.Value {
			if p.Namespace == nil || p.RegistrationState == nil {
				continue
			}
			// "Registered" / "Registering" → scan it; "NotRegistered" /
			// "Unregistering" → nothing to enumerate, mark disabled.
			state := *p.RegistrationState
			out[strings.ToLower(*p.Namespace)] = strings.EqualFold(state, "Registered") ||
				strings.EqualFold(state, "Registering")
		}
	}
	return out, nil
}

// providerDisabled reports whether svc's ARM namespace is known-not-registered
// in this subscription. Unknown namespace or nil map ⇒ false (scan it).
func providerDisabled(reg map[string]bool, svcName string) bool {
	if reg == nil {
		return false
	}
	ns := strings.ToLower(strings.TrimPrefix(svcName, "azure:"))
	registered, known := reg[ns]
	return known && !registered
}

// — shared helpers —

// subscription holds a resolved Azure subscription.
type subscription struct {
	ID   string
	Name string
	// tenantID is the AAD tenant GUID, resolved once in Scan and stamped onto
	// every subscription. Tenant-scope scanners/resolvers use it as AccountID
	// for tenant-identical resources (management groups, built-in role/policy
	// definitions) so they're stored once per tenant, not once per
	// subscription. Empty when tenant resolution failed — callers fall back
	// to per-subscription behavior.
	tenantID string
	// tenantName is the tenant's friendly display name, resolved once in Scan
	// via Microsoft Graph's /organization endpoint and stamped onto every
	// subscription alongside tenantID. Best-effort: empty when Graph is
	// unreachable / unauthorized, in which case tenant-scope rows label with
	// the GUID instead. Display-only — never used as an account key.
	tenantName string
}

// scopeLabel is the human-readable per-subscription scope shown in scan
// progress + error output. Azure lists are subscription-wide (one call spans
// all regions) so the scope is the subscription, not a region — prefer the
// DisplayName, falling back to the GUID when the config path supplied only an
// ID.
func (s *subscription) scopeLabel() string {
	if s.Name != "" {
		return s.Name
	}
	return s.ID
}

// tenantScopeLabel renders the scope column for tenant-scope service rows
// (Entra ID, management groups, built-in role/policy definitions). Mirrors
// scopeLabel's "friendly name, else GUID" shape: prefer the tenant display
// name, fall back to the tenant GUID, and only as a last resort the literal
// "tenant" placeholder when neither resolved (degraded mode). Tenant identity
// is uniform across subs, so the first one is representative.
func tenantScopeLabel(subs []subscription) string {
	if len(subs) > 0 {
		if subs[0].tenantName != "" {
			return subs[0].tenantName
		}
		if subs[0].tenantID != "" {
			return subs[0].tenantID
		}
	}
	return "tenant"
}

// functionAppSettingsByDiscoID is a per-subscription sidecar populated by the
// AppService scanner during scan and consumed by the Functions resolver.
// Outer key = subscription ID, inner key = function-app disco ID, innermost
// = setting name → value. App settings are NOT a first-class ARM resource
// (they live as sub-resource config of `Microsoft.Web/sites`), so per
// providers/CLAUDE.md's "non-resource config fetches" rule they sidecar
// rather than wrap into the parent site's AttributesJSON. Package-level
// rather than a `subscription` field because subscription is passed by value
// in some call paths, and adding a mutex would break those copy semantics.
var (
	functionAppSettingsMu sync.Mutex
	functionAppSettings   = map[string]map[string]map[string]string{}
)

// recordFunctionAppSettings stores app settings for one function-app site
// under a given subscription, concurrent-safe across per-site fan-out.
func recordFunctionAppSettings(subID, siteDiscoID string, settings map[string]string) {
	if len(settings) == 0 {
		return
	}
	functionAppSettingsMu.Lock()
	defer functionAppSettingsMu.Unlock()
	subMap, ok := functionAppSettings[subID]
	if !ok {
		subMap = map[string]map[string]string{}
		functionAppSettings[subID] = subMap
	}
	subMap[siteDiscoID] = settings
}

// loadFunctionAppSettings returns the app settings recorded for the given
// subscription. Concurrent-safe; returns nil if scanner did not run.
func loadFunctionAppSettings(subID string) map[string]map[string]string {
	functionAppSettingsMu.Lock()
	defer functionAppSettingsMu.Unlock()
	return functionAppSettings[subID]
}

func mustJSON(v any) string { return util.MustJSON(v) }

// regionGlobal is the canonical Region pointer for non-regional Azure
// resources (tenant-scope Entra ID users / groups / SPs / app regs / roles)
// and any other rows whose location is "everywhere". Mirrors AWS
// regionGlobal; see store/CLAUDE.md "region = \"global\" sentinel".
var regionGlobal = func() *string { s := "global"; return &s }()

func sv(p *string) string     { return util.Sv(p) }
func tp(t *time.Time) *string { return util.TimeRFC3339(t) }
