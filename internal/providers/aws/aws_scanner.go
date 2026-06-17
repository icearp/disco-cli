// Package aws implements cloud resource discovery for Amazon Web Services.
// It makes per-service API calls using the AWS SDK v2 and follows the
// two-phase scan pattern: resources are written first, relationships second.
package aws

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	// maxConcurrentServices caps the number of service scanners running in parallel
	// per region to avoid hitting AWS API rate limits.
	maxConcurrentServices = 10
	// maxConcurrentRegions caps how many regions are scanned in parallel for a
	// single account. Each region multiplies the in-flight service goroutines
	// (= maxConcurrentServices × maxConcurrentRegions) and the SQLite write
	// queue depth, so keep this low.
	maxConcurrentRegions = 4
	// serviceTimeout is the per-service hard deadline. A misbehaving API endpoint
	// won't stall the entire scan beyond this duration.
	serviceTimeout = 5 * time.Minute
)

func init() { providers.Register(&Scanner{}) }

// Scanner implements providers.Scanner for AWS.
type Scanner struct {
	serviceFilter  []string // nil = scan all registered services
	regionOverride []string // non-nil overrides all per-account and default regions
	profile        string   // "" = default AWS credential chain
	skipGlobals    bool     // when true, services registered as global are not invoked
	roleARN        string   // "" = use config-file accounts; non-empty pins single-account scan via assume-role
	externalID     string   // included in AssumeRole only when roleARN is also set
}

// Name implements providers.Scanner.
func (s *Scanner) Name() string { return "aws" }

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

// ServiceNames returns the names of all services this scanner will report.
func (s *Scanner) ServiceNames() []string {
	svcs := filteredServices(s.serviceFilter)
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
	accounts, err := loadAccounts(ctx, s.profile, s.regionOverride, s.roleARN, s.externalID)
	if err != nil {
		st.ReportError(store.ScanError{
			Provider: "aws", Service: "load-accounts", Scope: "", Message: err.Error(),
		})
		return nil
	}
	for i := range accounts {
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
	// Phase 1: global + regional services run CONCURRENTLY. Globals had
	// historically gated regionals via a wg.Wait() barrier, but phase-1
	// scanners only upsert (no DB reads); resolvers in phase 2 are the only
	// readers and they're already gated by the combined wait below. Letting
	// regionals start immediately means slow globals (IAM with its
	// ~1100-policy enrichment) no longer stall the rest of the scan.
	//
	// Plain WaitGroups + semaphores rather than errgroup — sibling
	// cancellation on first error is explicitly unwanted.
	globalSem := semaphore.NewWeighted(maxConcurrentServices)
	regionSem := semaphore.NewWeighted(maxConcurrentRegions)
	var wg sync.WaitGroup

	for _, svc := range filteredServices(services) {
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
			total, inserted, err := svc.fn(svcCtx, acct, "", st, scanID)
			if err != nil {
				if errors.Is(err, errServiceDisabled) {
					st.ReportService(svc.name, "global", 0, 0, 0, true)
					return
				}
				// NXDOMAIN = service not deployed in this scope. Silent-skip
				// (no warning) — distinct from a transient DNS outage.
				if isDNSNotFound(err) {
					st.ReportService(svc.name, "global", 0, 0, 0, true)
					return
				}
				if isTransientNetworkError(err) {
					_ = skipIfTransient(st, svc.name, acct.ID, "", err)
					st.ReportService(svc.name, "global", 0, 0, 0, false)
					return
				}
				st.ReportError(store.ScanError{
					Provider: "aws", Service: svc.name, Scope: acct.ID, Message: err.Error(),
				})
				st.ReportService(svc.name, "global", total, inserted, 1, false)
				return
			}
			st.ReportService(svc.name, "global", total, inserted, 0, false)
		})
	}

	for _, region := range acct.Regions {
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
			// Each resolver gets its own buffered store (independent buffer) so
			// concurrent resolvers stay isolated; flush collapses the per-edge
			// autocommit serialisation into one tx per resolver.
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

// scanRegion runs all regional service scanners in parallel, bounded by
// maxConcurrentServices to avoid API rate limits. Service failures are
// reported and never abort sibling services.
func scanRegion(ctx context.Context, acct *account, region string, services []string, st *store.Store, scanID string) {
	sem := semaphore.NewWeighted(maxConcurrentServices)
	var wg sync.WaitGroup
	for _, svc := range filteredServices(services) {
		if svc.global {
			continue
		}
		wg.Go(func() {
			if err := sem.Acquire(ctx, 1); err != nil {
				return
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(ctx, serviceTimeout)
			defer cancel()
			total, inserted, err := svc.fn(svcCtx, acct, region, st, scanID)
			if err != nil {
				if errors.Is(err, errServiceDisabled) {
					st.ReportService(svc.name, region, 0, 0, 0, true)
					return
				}
				// NXDOMAIN = service not deployed in this region. Silent-skip
				// (no warning) — distinct from a transient DNS outage.
				if isDNSNotFound(err) {
					st.ReportService(svc.name, region, 0, 0, 0, true)
					return
				}
				if isTransientNetworkError(err) {
					_ = skipIfTransient(st, svc.name, acct.ID, region, err)
					st.ReportService(svc.name, region, 0, 0, 0, false)
					return
				}
				st.ReportError(store.ScanError{
					Provider: "aws", Service: svc.name, Scope: acct.ID + "/" + region, Message: err.Error(),
				})
				st.ReportService(svc.name, region, total, inserted, 1, false)
				return
			}
			st.ReportService(svc.name, region, total, inserted, 0, false)
		})
	}
	wg.Wait()
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
