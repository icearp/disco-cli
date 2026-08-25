package azure

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/store"
)

// tenantServiceEntry describes a tenant-scope Azure service (API surface
// above the subscription boundary). fn runs ONCE per scan, after subscription
// discovery and concurrently with the per-subscription fan-out; each
// subscription's phase-2 resolvers block on its completion (see Scan /
// waitForTenant) so its written principals are present before any resolver
// consumes them.
//
// Targets: Entra ID (Microsoft Graph users / groups / service principals /
// app registrations / directory roles) and other Graph or tenant-scope ARM
// APIs. Distinct from registeredServices, which fans out per-subscription.
//
// The signature receives the discovered subscription set so a tenant scanner
// can correlate tenant principals with already-collected per-sub RBAC scopes
// (e.g. Graph object IDs against subscription role assignments).
// Subscriptions are read-only here — never mutate.
type tenantServiceEntry struct {
	name string
	fn   func(ctx context.Context, subs []subscription, cred azcore.TokenCredential, wif wifConfig, st *store.Store, scanID string) (total, inserted int, err error)
	// dedupOnly marks a tenant phase that READS NO DIRECTORY: it fetches data
	// identical in every directory (Microsoft-shipped built-ins) once instead
	// of once per subscription, and the per-sub scanners store their own copy
	// whenever it does not run. Such a service loses nothing when the
	// federation gate suppresses it, which is the difference
	// reportTenantScopeSkipped has to tell an operator.
	dedupOnly bool
	// graphScoped marks a tenant phase that reads its directory over Microsoft
	// Graph rather than tenant-root ARM. That is the whole difference between a
	// phase a consented customer directory can be redirected to and one it
	// cannot: a Graph token can NAME a directory
	// (policy.TokenRequestOptions.TenantID), while a tenant-root ARM call
	// answers about whichever directory the credential authenticated in and has
	// no such knob. So [wifConfig.graphTenantEnabled] ungates these and only
	// these — see tenantServiceRunnable, which is where the decision is made
	// and which both the running loop and the accounting loop ask.
	graphScoped bool
	emits       []coverage.TypeDecl
}

// registeredTenantServices is populated by each tenant-scope *_scanners.go
// file's init(). Phase 1 (Entra ID) wires the first consumers; the registry
// is intentionally empty in the foundation drop.
var registeredTenantServices []tenantServiceEntry

// registerTenantService adds a tenant-scope service to the registry.
// Panics on duplicate name to catch copy-paste errors at init time.
func registerTenantService(e tenantServiceEntry) {
	for _, s := range registeredTenantServices {
		if s.name == e.name {
			panic("disco: duplicate Azure tenant service registration: " + e.name)
		}
	}
	registeredTenantServices = append(registeredTenantServices, e)
}

// tenantServiceRunnable reports whether one tenant-scope service may run under
// this credential contract. The SINGLE source of truth for that decision:
// runTenantServices runs what it admits and reportTenantScopeSkipped accounts
// for what it refuses, so the two can never disagree about a service and none
// can fall through both.
//
// Two independent admissions, and the second is narrower on purpose. An
// unfederated credential runs everything ([wifConfig.tenantScopeEnabled]). A
// federated one runs a service only if that service reads its directory over
// Graph AND a consented customer directory was named
// ([wifConfig.graphTenantEnabled]) — because only a Graph token can be pointed
// at a directory, while a tenant-root ARM call answers about the credential's
// own directory whatever it is told.
func tenantServiceRunnable(svc tenantServiceEntry, wif wifConfig) bool {
	if wif.tenantScopeEnabled() {
		return true
	}
	return svc.graphScoped && wif.graphTenantEnabled()
}

// runTenantPhase runs the whole tenant-scope phase: it accounts for every
// service tenantServiceRunnable refused, then runs the ones it admitted.
//
// Both halves unconditionally, and each consults tenantServiceRunnable. An
// if/else was right while the decision was per-PHASE; it is wrong now that a
// federated scan can run the Graph services and skip the ARM ones in the same
// pass, where an if/else silently drops the notices for the half it did not
// take.
//
// ACCOUNTING FIRST, and that order is the reason this is a function rather
// than two statements in Scan's goroutine. runTenantServices does not recover
// — the recovery is Scan's deferred reportPanic — so a panicking service
// unwinds past whatever follows it. Reported second, the ordinary consented
// shape (Entra runs, ARM is skipped) loses every ARM notice, its zero-count
// service rows and the phase warning to any panic, leaving an inventory with
// no account of the suppressed half. The two are independent:
// reportTenantScopeSkipped reads the registry, the filter and wif, none of
// which runTenantServices writes. Two are shared rather than read-only and
// both are named rather than left to the word "independent": subs, held by
// slice and which no tenant service may mutate (see the read-only note on the
// registry above), and st, which BOTH halves write to — so the order decides
// how zero-count service rows and skip notices interleave with real service
// rows in the record and on the progress line. That is a presentation
// difference, not a correctness one (no reader of the store depends on the
// order, and each service reports exactly once whichever loop admits it).
// What it fixes is the order of the tenant phase's OWN two halves and nothing
// wider: this whole phase runs as one goroutine beside the per-subscription
// fan-out, both writing the same store, so where tenant rows fall among real
// per-sub rows is nondeterministic under either order.
//
// A TRADE, not a free win, and stated because the argument above only makes
// one side. The accounting now runs on EVERY scan, unfederated included
// (where it is a no-op loop: tenantScopeEnabled admits every service, so
// nothing is reported), and it invokes three caller-supplied store callbacks.
// A panic in one of those now takes the tenant SERVICES with it — the mirror
// of the failure this order fixes, for a population that has nothing to do
// with federation. Taken deliberately: this half is string work over a fixed
// registry, the other half is network I/O against Graph and ARM. Nothing
// tests the inverse direction.
func runTenantPhase(ctx context.Context, subs []subscription, cred azcore.TokenCredential, wif wifConfig, filter []string, st *store.Store, scanID string) {
	reportTenantScopeSkipped(st, subs, filter, wif)
	runTenantServices(ctx, subs, cred, wif, filter, st, scanID)
}

// runTenantServices fires every registered tenant-scope service exactly once
// per scan, concurrently with the per-subscription fan-out, and gates each
// subscription's phase-2 resolvers on its completion. Per-service errors go
// through st.ReportError + st.ReportService (errCount=1) — never propagated.
// Skipped when no tenant services are registered, and each service is admitted
// by tenantServiceRunnable.
func runTenantServices(ctx context.Context, subs []subscription, cred azcore.TokenCredential, wif wifConfig, filter []string, st *store.Store, scanID string) {
	if len(registeredTenantServices) == 0 {
		return
	}
	allowed := tenantServiceFilterSet(filter)
	scope := tenantScopeLabel(subs)
	for _, svc := range registeredTenantServices {
		if allowed != nil && !allowed[svc.name] {
			continue
		}
		if !tenantServiceRunnable(svc, wif) {
			continue
		}
		// The same hard deadline every PER-SUBSCRIPTION service gets. The tenant
		// phase had none, on the raw scan context, and Scan blocks on wg.Wait()
		// — so one service that never returns hung the whole scan, not just its
		// own results. A Graph server naming a fresh @odata.nextLink forever is
		// enough, and iterateGraph's repeated-link refusal does NOT cover it: a
		// varying $skiptoken is a new URL every time. That guard names the exact
		// repeat precisely; this is what bounds the general case.
		// The call is wrapped so cancel() is DEFERRED: svc.fn is not
		// panic-guarded here (the only recovery is on the goroutine in Scan), and
		// azure_scanner_test.go registers a tenant service that panics on
		// purpose, so a bare cancel() after the call is skipped exactly when the
		// timer most needs releasing.
		var newC, changedC atomic.Int64
		var total int
		var err error
		func() {
			svcCtx, cancel := context.WithTimeout(ctx, serviceTimeout)
			defer cancel()
			total, _, err = svc.fn(svcCtx, subs, cred, wif, st.WithUpsertCounters(&newC, &changedC), scanID)
		}()
		if err != nil {
			st.ReportError(store.ScanError{
				Provider: "azure", Service: svc.name, Scope: scope,
				Message: formatAzureError(err),
			})
			st.ReportService(svc.name, scope, total, int(newC.Load()), int(changedC.Load()), 1, store.ServiceOK)
			continue
		}
		st.ReportService(svc.name, scope, total, int(newC.Load()), int(changedC.Load()), 0, store.ServiceOK)
	}
}

// The per-service skip notices, as constants so a caller and a test name one
// message rather than re-typing it. Note a constant is not a CONTENT oracle:
// a test comparing against these pins which branch was taken, never what the
// message SAYS, so a claim about the wording still needs a positive assertion
// on the wording.
const (
	// directoryLossPrefix opens every notice in this block that reports a LOST
	// directory read, and no other, so "is this a directory-loss notice?" is a
	// property a test can ask without re-typing any message. Two limits worth
	// knowing before leaning on it. It is a CONVENTION over the constants
	// below, not over the type: nothing stops a fourth loss notice being added
	// without it, nothing stops a non-loss notice opening with the same
	// ordinary English word, and nothing stops a ScanWarning or ScanError
	// carrying it. And it is structural only — it says a directory read was
	// lost, never what the message CLAIMS, so a test asserting the claim still
	// needs a positive assertion on the wording.
	directoryLossPrefix = "skipped: "

	// armWarningReason is the ARM clause of the PHASE warning. Deliberately a
	// second spelling of armSkipNotice's reason and not a shared fragment: the
	// warning needs a relative clause ("reads through a call, which names…")
	// where the notice needs a main clause, and forcing one string to serve
	// both costs the grammar of whichever it was not written for. Exported to
	// the package so a test names it rather than re-typing a piece of it.
	armWarningReason = "reads through a tenant-root Azure Resource Manager call, which names no directory, so nothing could confirm which one answered"

	// dedupSkipNotice reports a suppressed dedupOnly phase, which loses no
	// rows: the per-subscription scanners store the same definitions. No
	// directoryLossPrefix, deliberately.
	dedupSkipNotice = "tenant-wide deduplication skipped under a federated credential: each subscription stores its own copy of the Microsoft-shipped definitions instead"

	// armSkipNotice reports a suppressed tenant-root ARM phase. It offers no
	// remedy because none exists WITHIN this credential: an ARM call names no
	// directory. Running unfederated does lift it — which is the right answer
	// for an operator federating into their own tenant, and which a scan
	// record is the wrong place to advise, since it names a deployment change
	// rather than a setting.
	armSkipNotice = directoryLossPrefix + "this scan uses a federated credential, and a tenant-root Azure Resource Manager call names no directory, so nothing could confirm which one answered"

	// graphSkipNoticeUnnamed reports a suppressed Graph phase with no
	// directory named at all.
	graphSkipNoticeUnnamed = directoryLossPrefix + "this scan uses a federated credential and no directory was named for Microsoft Graph, so there is nothing to confirm these objects would have come from — see the scan warning for how to name one"

	// graphSkipNoticeMalformed reports a suppressed Graph phase where a
	// directory WAS named and was rejected. Split from the line above because
	// the two states fail closed identically — the same reason
	// graphTenantAdvice splits the phase warning — and the unnamed wording
	// asserts something false here: the operator set the variable, and being
	// told nothing was named sends them to set it again.
	graphSkipNoticeMalformed = directoryLossPrefix + "the directory named for Microsoft Graph is not a directory GUID (8-4-4-4-12), so it was refused and these objects were not read — see the scan warning"
)

// graphSkipNotice picks the notice for a SUPPRESSED Graph phase, and is
// correct only for one. Same discriminator as graphTenantAdvice, and it works
// for the same reason: with the phase already known suppressed, a non-empty
// graphTenantID can only be a value this package refused. Called anywhere the
// phase is running it answers graphSkipNoticeMalformed for a perfectly valid
// GUID, which nothing in the name or signature would warn you about.
func graphSkipNotice(wif wifConfig) string {
	if wif.graphTenantID != "" {
		return graphSkipNoticeMalformed
	}
	return graphSkipNoticeUnnamed
}

// reportTenantScopeSkipped records a notice per tenant-scope service that
// tenantServiceRunnable refused, plus one warning for the phase when any of
// them read a directory, for a scan whose credential's directory cannot be
// confirmed to be the scanned tenant's (see wifConfig.tenantScopeEnabled —
// the gate keys on the WIF contract being set, not on the directory actually
// differing, so this fires for self-federation too).
//
// It runs on EVERY scan, beside runTenantServices rather than instead of it,
// because the two halves are now per-SERVICE: a federated scan naming a
// consented directory runs the Graph services and still suppresses the
// tenant-root ARM ones, and those still owe an operator an account of
// themselves. Both loops ask tenantServiceRunnable, so a service reaches
// exactly one of them.
//
// Reported rather than silently skipped: a tenant service that writes nothing
// and says nothing is indistinguishable from a tenant that genuinely has no
// management groups or no directory objects, and the difference decides
// whether an empty result is a finding.
//
// Reach differs by kind, which is why the directory case does not rely on the
// notice: scanrun persists warnings and only RETURNS notices, so a notice
// reaches the CLI renderer and leaves no trace on a SaaS scan record. A
// dedupOnly-only suppression therefore reports to the CLI alone — acceptable
// because it loses no rows, unlike the directory case.
//
// Never ServiceDisabled, which means the customer has not enabled something
// they could enable and renders through tenantNoun as "(subscription:
// disabled)" — two claims that are both false here: nothing on their side is
// off, and the scope is the tenant.
//
// The two kinds of suppression differ in SEVERITY as well as wording. A
// suppressed dedupOnly phase reached the right answer by another route — the
// per-subscription scanners each keep their own copy of the same
// Microsoft-shipped rows, and that service reports real counts under this same
// name later in the run — so it is a notice and nothing more. A suppressed
// DIRECTORY read changed coverage, which store.ScanNotice's contract reserves
// for a warning; the phase raises exactly one, for the reason at the emission
// site.
//
// Honours the --services filter so a run that never asked for these services
// does not report them.
func reportTenantScopeSkipped(st *store.Store, subs []subscription, filter []string, wif wifConfig) {
	allowed := tenantServiceFilterSet(filter)
	scope := tenantScopeLabel(subs)

	var skipped []string
	// Which KINDS are in that list decides which explanations may be attached
	// to it. Neither is a property of the wifConfig alone: --services can
	// exclude either kind, so a run that never asked for the Graph service
	// must not be told how to re-enable it, and a list holding only the Graph
	// service must not be EXPLAINED by a fact about ARM.
	//
	// Explained, precisely. The Graph advice closes by bounding its own remedy
	// ("the tenant-root services stay suppressed whatever it names"), which
	// names ARM and so reaches a Graph-only list under --services. Deliberate:
	// that is a bound on what the setting buys, never a reason the Graph
	// service was skipped, and withholding it is how an operator concludes the
	// variable re-enables the whole phase. Threading skippedARM into
	// graphTenantAdvice to suppress it for one filter shape would buy a
	// shorter message and a branch no test can reach.
	var skippedGraph bool
	var skippedARM []string
	for _, svc := range registeredTenantServices {
		if allowed != nil && !allowed[svc.name] {
			continue
		}
		if tenantServiceRunnable(svc, wif) {
			continue
		}
		if svc.dedupOnly {
			st.ReportNotice(store.ScanNotice{
				Provider: "azure", Service: svc.name, Scope: scope,
				Message: dedupSkipNotice,
			})
			continue
		}
		skipped = append(skipped, svc.name)
		if svc.graphScoped {
			skippedGraph = true
		} else {
			skippedARM = append(skippedARM, svc.name)
		}
		// Split by KIND for the same reason the phase warning is, and it was one
		// identical sentence for both until this was noticed fifty lines from
		// that argument. "Cannot be confirmed" is true of an ARM call, which
		// names no directory and offers the operator nothing to do. For the Graph
		// service the reason is narrower and ACTIONABLE — no directory was named
		// — and saying "cannot be confirmed" there withholds the remedy that the
		// phase warning goes on to give.
		msg := armSkipNotice
		if svc.graphScoped {
			msg = graphSkipNotice(wif)
		}
		st.ReportNotice(store.ScanNotice{
			Provider: "azure", Service: svc.name, Scope: scope,
			Message: msg,
		})
		// Zero counts and zero errors: the progress line accounts for the
		// service, and the warning below carries the severity. An errCount
		// here would render "(with errors)" and claim a failure that did not
		// happen. Not emitted for a dedupOnly phase: the per-sub service
		// reports real counts under the same name, and a 0-count row beside
		// it reads as a contradiction.
		st.ReportService(svc.name, scope, 0, 0, 0, 0, store.ServiceOK)
	}

	// ONE warning for the phase, not one per service. Both halves of
	// store.ScanNotice's contract have to hold at once: coverage genuinely
	// changed, so a notice alone would leave it outside the "N warnings" count
	// and outside scanrun's persistence — which drops notices entirely, so on
	// the SaaS the notice half is write-only. But this fires on EVERY federated
	// scan, permanently, and the same doc warns that a warning firing on every
	// healthy scan trains people to ignore the block. Per-service fan-out would
	// also grow the count as tenant services are added, which is precisely what
	// the phase-wide gate is designed to absorb.
	if len(skipped) > 0 {
		// A WEAKER reason in the lead, not none — do not read this as licence
		// to add one back, which would leave two competing causal clauses. It
		// carried "a federated credential whose directory cannot be confirmed
		// to be the scanned tenant's", false in the two states that matter
		// most: with a consented directory the Entra phase confirms its own by
		// tid and stores rows keyed to it, and with a malformed one that is
		// not why Entra is off. It was the only state-independent sentence in
		// the block and the first one read, so what survives is the one fact
		// true in every state — this scan is federated. The KIND-specific
		// reasons follow, from clauses that know their state.
		msg := "tenant-scope services skipped (" + strings.Join(skipped, ", ") +
			"): this scan uses a federated credential (" + envWIFClientID + "), so these services are absent from this scan"
		if len(skippedARM) > 0 {
			// Names the members it explains. Unqualified, this reason follows a
			// list whose first member may be the GRAPH service — which can be
			// pointed at a directory, as the very next clause then says.
			// "each reads" so the sentence is grammatical for one member and for
			// several — the list length is the registry's to decide. And "Of
			// those" names a SUBSET, so it is dropped when the ARM members are the
			// whole list, which is the ORDINARY rendering of a consented scan (the
			// Entra service runs, so only ARM is skipped) as well as of
			// --services azure:microsoft.management.
			//
			// The test is the LENGTH, which states that condition directly, and
			// NOT the equivalent-today !skippedGraph. They ARE equivalent today —
			// every member of skipped sets exactly one of skippedGraph and
			// skippedARM — so the choice only matters for what happens next.
			// Adding a third KIND means splitting the binary if/else above into
			// three arms; do THAT and skipped=[X, M] leaves skippedARM=[M], where
			// the length correctly names M alone while !skippedGraph would apply
			// the ARM reason to X. Until that split, X would land in skippedARM
			// and both forms would be equally wrong: the length is the form that
			// becomes correct on the same edit, not one that is correct now.
			if len(skippedARM) == len(skipped) {
				msg += ". Each " + armWarningReason
			} else {
				msg += ". Of those, " + strings.Join(skippedARM, ", ") + " — each " + armWarningReason
			}
		}
		if skippedGraph {
			msg += graphTenantAdvice(wif)
		}
		st.ReportWarning(store.ScanWarning{
			Provider: "azure", Service: "azure:tenant-scope", Scope: scope,
			Message: msg,
		})
	}
}

// tenantServiceFilterSet returns nil when filter is empty (allow all), else
// a set of allowed service names. Shared with the per-sub dispatch shape so
// a single --services flag can target both tenant and subscription services.
func tenantServiceFilterSet(filter []string) map[string]bool {
	if len(filter) == 0 {
		return nil
	}
	out := make(map[string]bool, len(filter))
	for _, name := range filter {
		out[name] = true
	}
	return out
}

// graphWhichDirectory names the directory to use and the one that must not be
// used, for BOTH audiences. Shared by the two suppressed states rather than
// written into one, because the operator who most needs it — the one who has
// already set a value and got it wrong — is in the other.
//
// It opens with its own subject rather than a pronoun. As " WHICH directory
// that is" the "that" resolved against the preceding clause, which named a
// directory in the unset arm and named none in the malformed one — so the
// extraction that shared this text re-created, on the malformed side, the
// dangling reference an earlier round had fixed on the unset side.
//
// MEASURED, and accepted rather than trimmed: the assembled phase warning is
// 934 BYTES in the unset state and 919 in the malformed one, against 303 once
// a directory is consented. Bytes, not runes — each long rendering carries one
// em dash, so the rune counts are 932 and 917, and 919 is therefore both the
// malformed byte count and an earlier unset figure. Re-derive with len(); a
// figure here has already been invalidated twice by a later edit in its own
// round, and one number that matches reads as confirmation for both.
//
// Accepted because the alternatives are worse, not because the text is
// minimal. It is not: sharing this const makes the unset assembly state the
// capability twice ("can be pointed at a named directory", then "Naming one
// restores the Entra ID services alone"), which the malformed assembly does
// not — the shared-text failure mode below, in the edit that shared the text.
// The two claims differ (capability, then sufficiency bound) and the second is
// what the malformed state was missing, so the redundancy is the price of one
// text serving both. Cutting security content is not on the table. Moving any
// of it to the per-service notice would strand it: scanrun persists warnings
// and only RETURNS notices, so a notice reaches the CLI and no scan record.
//
// NOT covered by any test: that the opening names its own subject. Grammar has
// no assertion here, and the only defect this text has had twice is a pronoun
// without an antecedent — read the assembled string, do not trust the suite.
//
// It closes by stating the SUFFICIENCY BOUND, and that clause lives here
// rather than in a caller. It reached the unset arm alone while this text
// reached both, which left the malformed state — the one whose operator is
// about to retype a value — told which directory to name and never told what
// naming it buys. An earlier version closed instead with "setting it is the
// correct answer", which read as done.
const graphWhichDirectory = " The directory to name depends on how you federated: into your OWN tenant it is the same value as " + envWIFTenantID +
	"; under Azure Lighthouse it is the CUSTOMER's directory and never " + envWIFTenantID +
	", which is accepted and writes YOUR directory's users, groups and service principals into the customer's inventory." +
	" Naming one restores the Entra ID services alone; the tenant-root services stay suppressed whatever it names"

// graphTenantAdvice returns the trailing clause of the phase warning: what the
// operator can still do about a suppressed GRAPH service.
//
// Called only when the skipped list actually holds one, so it never offers a
// remedy for the ARM services (no setting reaches them, and offering one would
// be worse than silence) and never speaks for a run whose --services excluded
// the Graph service.
//
// The two remaining states fail closed IDENTICALLY, so the outcome cannot tell
// them apart and only the message can. A value set that is not a GUID reads
// exactly like unset, which is the one case where naming the shape IS the
// whole diagnosis — nothing else in the scan record says why. The
// consented-and-running case is unreachable from here by construction (that
// service would not be in the list) and returns nothing rather than repeating
// a remedy the operator has already applied.
func graphTenantAdvice(wif wifConfig) string {
	switch {
	case wif.graphTenantEnabled():
		return ""
	case wif.graphTenantID != "":
		// Carries the which-directory guidance too. It used to reach the unset
		// state ONLY — and this is the state whose operator is about to retype
		// a value, having by construction already left the unset state for
		// good. A Lighthouse environment already holds a GUID that passes
		// every check in this package and discloses disco's own directory, so
		// "must be 8-4-4-4-12" alone is an invitation to reach for it.
		return ". " + envGraphTenantID + " is set but is not a directory GUID (8-4-4-4-12), so the Entra ID services stayed off." + graphWhichDirectory
	default:
		// Names WHICH directory, and names the wrong answer explicitly. "That
		// directory's GUID" had no antecedent, and the GUID already in a
		// Lighthouse operator's environment is DISCO_AZURE_WIF_TENANT_ID —
		// which passes every check in this package (azidentity short-circuits
		// when the requested tenant IS the credential's default, so the tid
		// comes back equal) and writes the managing directory's users, groups
		// and service principals into the customer's inventory. The same
		// sentence is read by a standalone operator federating into their own
		// tenant, for whom that value is the CORRECT answer, so it has to
		// distinguish the two rather than just give one.
		return ". Only the Entra ID services can be pointed at a named directory, by setting " + envGraphTenantID +
			" to that directory's GUID." + graphWhichDirectory
	}
}
