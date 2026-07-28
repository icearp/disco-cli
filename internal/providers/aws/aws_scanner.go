// Package aws implements cloud resource discovery for Amazon Web Services.
// It makes per-service API calls using the AWS SDK v2 and follows the
// two-phase scan pattern: resources are written first, relationships second.
package aws

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/icearp/disco-cli/internal/providers"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	// maxConcurrentServices caps service scanners running in parallel per
	// region, to avoid hitting AWS API rate limits.
	maxConcurrentServices = 10
	// maxConcurrentRegions caps how many regions are scanned in parallel for a
	// single account. Each region multiplies the in-flight service goroutines
	// (= maxConcurrentServices × maxConcurrentRegions) and the SQLite write
	// queue depth, so keep this low. The region axis is throttle-independent
	// (AWS buckets regional services per-region-per-service, and the per-region
	// maxConcurrentServices cap already protects each bucket), so the binding
	// constraints here are peak memory (each scanner buffers its service's rows
	// before a batch upsert) and the single SQLite writer (SetMaxOpenConns(1) —
	// more regions deepen the write queue without adding throughput). 5 keeps the
	// adaptive retryer's per-account client-side token bucket comfortable in
	// high-latency regions (at 6, a burst of dial failures in distant regions —
	// Seoul/Osaka — drained the bucket into "retry quota exceeded"); going higher
	// wants a global scanner semaphore + streaming upserts to decouple memory from
	// region count first.
	maxConcurrentRegions = 5
	// serviceTimeout is the per-service hard deadline. A misbehaving API endpoint
	// won't stall the entire scan beyond this duration.
	serviceTimeout = 5 * time.Minute
)

func init() { providers.Register(&Scanner{}) }

// Scanner implements providers.Scanner for AWS.
type Scanner struct {
	serviceFilter       []string // nil = scan all registered services
	regionOverride      []string // non-nil overrides all per-account and default regions
	profile             string   // "" = default AWS credential chain
	skipGlobals         bool     // when true, services registered as global are not invoked
	roleARN             string   // "" = use config-file accounts; non-empty pins single-account scan via assume-role
	externalID          string   // included in AssumeRole only when roleARN is also set
	sourceIdentity      string   // "" = off; "auto" = scan ID; else literal STS SourceIdentity stamped on assumed sessions
	regionScopeDisabled bool     // when true, skip SSM global-infra region pre-scoping (--scope-regions=false)

	includeServiceQuotas bool // when true, the opt-in aws:servicequotas scanner is added to the default scan (--include-service-quotas)
}

// Name implements providers.Scanner.
func (s *Scanner) Name() string { return "aws" }

// LongDescription is the help text for `disco scan aws --help`.
func (s *Scanner) LongDescription() string {
	return `Scan AWS resources across one or more regions.

Account scope comes from the ambient AWS identity (env vars, instance
profile, ~/.aws/config) or, if config.yaml lists explicit accounts, the
declared role-chain per entry. Use --profile to pick a named profile and
--regions to override the configured region list; --regions all scans every
region (trimmed to those your account has opted into). --skip-globals omits
account-wide services (IAM, Route53, CloudFront, etc.) when running a
per-region audit.

aws:servicequotas (account quota limits) is opt-in — a default scan skips it.
Add it with --include-service-quotas, or run it on its own with
--services aws:servicequotas.

Examples:
  disco scan aws
  disco scan aws --regions all
  disco scan aws --regions us-west-2,eu-west-1
  disco scan aws --services aws:ec2,aws:s3 --profile prod
  disco scan aws --skip-globals --regions us-east-1
  disco scan aws --include-service-quotas`
}

// ServiceFilterExample is the --services example shown in aws scan help.
func (s *Scanner) ServiceFilterExample() string { return "aws:ec2,aws:s3" }

// SetServiceFilter restricts the scan to the named services (e.g. "aws:ec2", "aws:iam").
// An empty or nil slice scans all registered services.
func (s *Scanner) SetServiceFilter(services []string) { s.serviceFilter = services }

// SetRegionOverride forces all accounts to scan only the given regions,
// ignoring both per-account and default_regions config values.
func (s *Scanner) SetRegionOverride(regions []string) { s.regionOverride = regions }

// SetSkipGlobals suppresses every service registered with global=true.
// Use case: data-residency / per-region audits where global-scope reads
// (IAM, Route53, CloudFront, etc.) are explicitly out of scope.
func (s *Scanner) SetSkipGlobals(skip bool) { s.skipGlobals = skip }

// SetProfile selects a named credential profile from ~/.aws/config.
// An empty string uses the default credential chain.
func (s *Scanner) SetProfile(profile string) { s.profile = profile }

// SetRoleOverride pins the scan to a single account reached by AssumeRole
// against roleARN with an optional STS external_id. Bypasses the config
// file's accounts: section — an external orchestrator (e.g. a scan-trigger
// Lambda) uses this to drive a per-tenant scan without writing config to
// disk in the worker container.
//
// Empty roleARN clears the override (restores config-driven account list).
// externalID is honoured only when roleARN is also set.
func (s *Scanner) SetRoleOverride(roleARN, externalID string) {
	s.roleARN = roleARN
	s.externalID = externalID
}

// SetSourceIdentity opts the scan into stamping an STS SourceIdentity on every
// assumed-role session (CloudTrail attribution). "auto" resolves to the scan ID;
// any other non-empty value is used verbatim. Requires the target role's trust
// policy to grant sts:SetSourceIdentity, so it is off unless explicitly set.
func (s *Scanner) SetSourceIdentity(sourceIdentity string) { s.sourceIdentity = sourceIdentity }

// SetRegionScope toggles SSM global-infrastructure region pre-scoping (default on
// via --scope-regions). When disabled, every regional service is dispatched into
// every enabled region as before.
func (s *Scanner) SetRegionScope(enabled bool) { s.regionScopeDisabled = !enabled }

// SetIncludeServiceQuotas adds the opt-in aws:servicequotas scanner to the default
// scan (--include-service-quotas). Off by default: servicequotas reads account quota
// limits (metadata, not resources) and is markedly slower than any resource scan, so
// it runs only when explicitly requested — here, or via --services aws:servicequotas.
func (s *Scanner) SetIncludeServiceQuotas(include bool) { s.includeServiceQuotas = include }

// ServiceNames returns the names of all services this scanner will report.
func (s *Scanner) ServiceNames() []string {
	svcs := filteredServices(s.serviceFilter, s.includeServiceQuotas)
	names := make([]string, len(svcs))
	for i, svc := range svcs {
		names[i] = svc.name
	}
	return names
}

// Scan discovers all AWS resources across all configured accounts and regions.
// Errors are reported via st.ReportError and never abort the scan: a failure
// in one service / resolver / account does not stop the others.
func (s *Scanner) Scan(ctx context.Context, st *store.Store, scanID string) error {
	srcID := resolveSourceIdentity(s.sourceIdentity, scanID)
	if srcID != "" {
		if err := validateSourceIdentity(srcID); err != nil {
			st.ReportError(store.ScanError{
				Provider: "aws", Service: "source-identity", Scope: "", Message: err.Error(),
			})
			return nil
		}
	}
	accounts, err := loadAccounts(ctx, s.profile, s.regionOverride, s.roleARN, s.externalID, srcID)
	if err != nil {
		st.ReportError(store.ScanError{
			Provider: "aws", Service: "load-accounts", Scope: "", Message: err.Error(),
		})
		return nil
	}
	for i := range accounts {
		accounts[i].regionScopeDisabled = s.regionScopeDisabled
		accounts[i].includeServiceQuotas = s.includeServiceQuotas
		scanAccount(ctx, &accounts[i], s.serviceFilter, s.skipGlobals, st, scanID)
	}
	return nil
}

// scanAccount runs phase 1 (resources) then phase 2 (relationships) for one
// account. Errors are reported via st.ReportError and never propagate — a
// service failure does not abort sibling services or the relationship phase.
// When skipGlobals is true, services registered as global=true are not
// invoked; per-region services run normally.
func scanAccount(ctx context.Context, acct *account, services []string, skipGlobals bool, st *store.Store, scanID string) {
	// Phase 1: global + regional services run CONCURRENTLY. Globals used to
	// gate regionals via a wg.Wait() barrier, but phase-1 scanners only upsert
	// (no DB reads) — phase-2 resolvers are the only readers, already gated by
	// the combined wait below. Letting regionals start immediately means slow
	// globals (IAM's ~1100-policy enrichment) no longer stall the rest of the
	// scan.
	//
	// Plain WaitGroups + semaphores, not errgroup — sibling cancellation on
	// first error is explicitly unwanted.
	globalSem := semaphore.NewWeighted(maxConcurrentServices)
	regionSem := semaphore.NewWeighted(maxConcurrentRegions)
	var wg sync.WaitGroup

	for _, svc := range filteredServices(services, acct.includeServiceQuotas) {
		if !svc.global {
			continue
		}
		if skipGlobals {
			continue
		}
		wg.Go(func() {
			if err := globalSem.Acquire(ctx, 1); err != nil {
				return
			}
			defer globalSem.Release(1)
			svcCtx, cancel := context.WithTimeout(ctx, serviceTimeout)
			defer cancel()
			var newC, changedC atomic.Int64
			total, _, err := svc.fn(svcCtx, acct, "", st.WithUpsertCounters(&newC, &changedC), scanID)
			if err != nil {
				switch classifyServiceError(err) {
				case outcomeDisabled:
					st.ReportService(svc.name, "global", 0, 0, 0, 0, store.ServiceDisabled)
				case outcomeNotEntitled:
					st.ReportService(svc.name, "global", 0, 0, 0, 0, store.ServiceNotEntitled)
				case outcomeUnavailable:
					st.ReportService(svc.name, "global", 0, 0, 0, 0, store.ServiceUnavailable)
				case outcomeTransient:
					_ = skipIfTransient(st, svc.name, acct.ID, "", err)
					st.ReportService(svc.name, "global", 0, 0, 0, 0, store.ServiceOK)
				case outcomeDeadline:
					st.ReportError(store.ScanError{
						Provider: "aws", Service: svc.name, Scope: acct.ID, Message: serviceDeadlineMessage(err),
					})
					st.ReportService(svc.name, "global", total, int(newC.Load()), int(changedC.Load()), 1, store.ServiceOK)
				case outcomeStoreWrite, outcomeError:
					st.ReportError(store.ScanError{
						Provider: "aws", Service: svc.name, Scope: acct.ID, Message: err.Error(),
					})
					st.ReportService(svc.name, "global", total, int(newC.Load()), int(changedC.Load()), 1, store.ServiceOK)
				}
				return
			}
			st.ReportService(svc.name, "global", total, int(newC.Load()), int(changedC.Load()), 0, store.ServiceOK)
		})
	}

	kept := enabledScanRegions(ctx, acct, st)
	// Loads the live SSM global-infrastructure catalog, the second-opinion half
	// of region scoping, so services AWS doesn't offer in a region are never
	// dispatched there. Fail-open, and skipped for single-region scans — but
	// note that skips only the CATALOG: the shipped SDK table still scopes those
	// scans, which is what keeps a one-region run from stalling on a service
	// that region doesn't host.
	//
	// Runs on this goroutine while the global scanners launched above are still
	// in flight (on wg, no wait between) — the preflight overlaps them for free.
	// The regional loop below reads acct.availByCode via
	// serviceAvailableInRegion, a genuine data dependency, so it must join here
	// before dispatching; that join is the only necessary serial point.
	buildRegionAvailability(ctx, acct, services, st, kept)
	for _, region := range kept {
		wg.Go(func() {
			if err := regionSem.Acquire(ctx, 1); err != nil {
				return
			}
			defer regionSem.Release(1)
			scanRegion(ctx, acct, region, services, st, scanID)
		})
	}
	wg.Wait()

	// Phase 2: derive relationships now that all resources exist in the DB.
	st.ReportResolveStart("aws")
	var counter atomic.Int64
	resolveRelationships(ctx, acct, st.WithRelCounter(&counter))
	st.ReportResolveComplete("aws", int(counter.Load()))
}

// resolveRelationships is phase 2: after all resources are written to the DB,
// derive relationships from the JSON attributes stored on each resource.
// Using ResourceID to compute stable IDs means we never need to read back
// resources just to get their primary keys. Resolvers run concurrently
// (bounded by fanoutMed) since they operate on disjoint resource types; a
// failure in one resolver is reported and does not stop the others — partial
// graph beats no graph.
func resolveRelationships(ctx context.Context, acct *account, st *store.Store) {
	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, r := range registeredResolvers {
		g.Go(func() error {
			// Each resolver gets its own buffered store so concurrent resolvers
			// stay isolated; flush collapses per-edge autocommit serialisation
			// into one tx per resolver.
			bs := st.BeginRelBuffer()
			if err := r.fn(acct, bs); err != nil {
				st.ReportError(store.ScanError{
					Provider: "aws",
					Service:  "resolve:" + r.name,
					Scope:    acct.ID,
					Message:  err.Error(),
				})
			}
			// Flush whatever the resolver emitted before any error — partial
			// graph beats no graph, matching the pre-buffer autocommit behaviour.
			if ferr := bs.FlushRelBuffer(); ferr != nil {
				st.ReportError(store.ScanError{
					Provider: "aws",
					Service:  "resolve:" + r.name,
					Scope:    acct.ID,
					Message:  ferr.Error(),
				})
			}
			return nil // resolver errors are reported, never abort siblings
		})
	}
	_ = g.Wait()
}

// enabledScanRegions returns acct.Regions minus any region not enabled for the
// account. Scanning a not-opted-in opt-in region (e.g. af-south-1, ap-east-1)
// returns AuthFailure / UnrecognizedClientException on every service call —
// which read like token-expiry errors and, under the 10-attempt adaptive
// retryer, cost minutes of doomed retries. One ec2:DescribeRegions probe (from
// always-enabled us-east-1, under the account's own credentials) replaces that
// storm. If the probe fails (e.g. ec2:DescribeRegions denied), fall back to the
// full list so restricted roles still scan — they just forgo the speedup.
func enabledScanRegions(ctx context.Context, acct *account, st *store.Store) []string {
	client := ec2.NewFromConfig(acct.cfg, func(o *ec2.Options) { o.Region = "us-east-1" })
	enabled, err := enabledRegionSet(ctx, client)
	if err != nil {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: "preflight:regions", Scope: acct.ID,
			Message: "could not list enabled regions (ec2:DescribeRegions): " + err.Error() + "; scanning all configured regions",
		})
		return acct.Regions
	}
	kept, skipped := filterToEnabled(acct.Regions, enabled)
	if len(skipped) > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: "preflight:regions", Scope: acct.ID,
			Message: "skipping region(s) not enabled for this account: " + strings.Join(skipped, ", "),
		})
	}
	return kept
}

// scanRegion runs all regional service scanners in parallel, bounded by
// maxConcurrentServices to avoid API rate limits. Service failures are
// reported and never abort sibling services.
func scanRegion(ctx context.Context, acct *account, region string, services []string, st *store.Store, scanID string) {
	sem := semaphore.NewWeighted(maxConcurrentServices)
	var wg sync.WaitGroup
	for _, svc := range filteredServices(services, acct.includeServiceQuotas) {
		if svc.global {
			continue
		}
		// AWS doesn't offer this service in this region (per the shipped SDK
		// table or the SSM global-infra catalog) — don't dispatch. Fail-open:
		// serviceAvailableInRegion returns true whenever neither source has an
		// opinion. Note the SDK half applies even for a single-region scan,
		// where buildRegionAvailability skips the catalog lookup entirely;
		// --no-scope-regions is what turns both off.
		if !acct.regionScopeDisabled &&
			!serviceAvailableInRegion(acct.availByCode, svc.name, regionAvailabilityCode(svc.name), region) {
			st.ReportService(svc.name, region, 0, 0, 0, 0, store.ServiceUnavailable)
			continue
		}
		wg.Go(func() {
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(ctx, serviceTimeout)
			defer cancel()
			var newC, changedC atomic.Int64
			total, _, err := svc.fn(svcCtx, acct, region, st.WithUpsertCounters(&newC, &changedC), scanID)
			if err != nil {
				switch classifyServiceError(err) {
				case outcomeDisabled:
					st.ReportService(svc.name, region, 0, 0, 0, 0, store.ServiceDisabled)
				case outcomeNotEntitled:
					st.ReportService(svc.name, region, 0, 0, 0, 0, store.ServiceNotEntitled)
				case outcomeUnavailable:
					st.ReportService(svc.name, region, 0, 0, 0, 0, store.ServiceUnavailable)
				case outcomeTransient:
					_ = skipIfTransient(st, svc.name, acct.ID, region, err)
					st.ReportService(svc.name, region, 0, 0, 0, 0, store.ServiceOK)
				case outcomeDeadline:
					st.ReportError(store.ScanError{
						Provider: "aws", Service: svc.name, Scope: acct.ID + "/" + region, Message: serviceDeadlineMessage(err),
					})
					st.ReportService(svc.name, region, total, int(newC.Load()), int(changedC.Load()), 1, store.ServiceOK)
				case outcomeStoreWrite, outcomeError:
					st.ReportError(store.ScanError{
						Provider: "aws", Service: svc.name, Scope: acct.ID + "/" + region, Message: err.Error(),
					})
					st.ReportService(svc.name, region, total, int(newC.Load()), int(changedC.Load()), 1, store.ServiceOK)
				}
				return
			}
			st.ReportService(svc.name, region, total, int(newC.Load()), int(changedC.Load()), 0, store.ServiceOK)
		})
	}
	wg.Wait()
}

// filteredServices returns the services to run. An explicit filter wins: when
// non-empty, exactly the named services run (opt-in or not). With no filter, the
// default set is every non-opt-in service, plus opt-in services only when
// includeOptIn is set (aws:servicequotas is currently the only opt-in service —
// see --include-service-quotas).
func filteredServices(filter []string, includeOptIn bool) []serviceEntry {
	if len(filter) > 0 {
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
	out := make([]serviceEntry, 0, len(registeredServices))
	for _, svc := range registeredServices {
		if !svc.optIn || includeOptIn {
			out = append(out, svc)
		}
	}
	return out
}

// — shared helpers used across all service files in this package —

// account holds the resolved SDK config and scan scope for one AWS account.
type account struct {
	ID      string
	Name    string
	Regions []string
	cfg     sdkaws.Config // credentials + endpoint config; region is set per client

	// s3BucketEncryption holds per-bucket SSE config fetched during the S3 scan,
	// keyed by bucket name. Populated by scanS3BucketEncryptions, consumed by
	// resolveS3BucketEncryptionRelationships. Ephemeral (per scan run) — the
	// resulting KMS edges persist in relationships; the config itself does not.
	s3BucketEncryption   map[string]s3BucketEncryptionEntry
	s3BucketEncryptionMu sync.Mutex

	// wsiRegions caches the set of regions where the WorkspacesInstances service
	// is enabled (from its ListRegions API), computed once per account. The
	// per-region scanners fan out concurrently, so the lookup is sync.Once-guarded.
	// See workspacesInstancesEnabledRegions.
	wsiRegionsOnce sync.Once
	wsiRegions     map[string]bool
	wsiRegionsErr  error

	// availByCode maps an AWS global-infrastructure service code to the set of
	// regions where AWS offers it, built once per account from the SSM catalog
	// (see aws_region_availability.go). nil means the catalog contributed no
	// opinion — NOT that scoping is off: the shipped SDK table is a second,
	// independent source and still applies. Only regionScopeDisabled
	// (--scope-regions=false) turns scoping off outright.
	availByCode         map[string]map[string]bool
	regionScopeDisabled bool

	// includeServiceQuotas mirrors --include-service-quotas: when true the opt-in
	// aws:servicequotas scanner is added to the default service set for this scan.
	includeServiceQuotas bool
}

// s3BucketEncryptionEntry carries a bucket's SSE configuration alongside the
// bucket's home region. Region is needed by the resolver so bare KMS key IDs
// and aliases in KMSMasterKeyID can be normalized to full cross-region ARNs.
type s3BucketEncryptionEntry struct {
	Region string
	Config *s3types.ServerSideEncryptionConfiguration
}

func mustJSON(v any) string   { return util.MustJSON(v) }
func sv(p *string) string     { return util.Sv(p) }
func tp(t *time.Time) *string { return util.TimeRFC3339(t) }

// sp returns a pointer to s.
func sp(s string) *string { return &s }

// regionGlobal is the canonical Region pointer for non-regional resources
// (IAM, Route53, CloudFront, S3, Organizations, etc., plus resolver-side
// cross-account synthetic stubs). Global scanners and stub-emitting
// resolvers set Resource.Region = regionGlobal so callers can query
// `--regions global` and the default `--regions <r>` filter folds these
// rows in. See store/CLAUDE.md "region = \"global\" sentinel".
var regionGlobal = sp("global")
