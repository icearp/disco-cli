// Package gcp implements cloud resource discovery for Google Cloud Platform.
// Makes per-service REST calls via google.golang.org/api and follows a
// two-phase scan: resources (and the org→folder→project hierarchy) written
// first, relationships second.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/icearp/disco-cli/internal/providers"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	// maxConcurrentServices caps the number of service scanners running in parallel
	// per project to avoid hitting GCP API rate limits.
	maxConcurrentServices = 10
	// serviceTimeout is the per-service hard deadline. A misbehaving API endpoint
	// won't stall the entire scan beyond this duration.
	serviceTimeout = 5 * time.Minute
)

func init() { providers.Register(&Scanner{}) }

// Scanner implements providers.Scanner for GCP.
type Scanner struct {
	serviceFilter        []string // nil = scan all registered services
	credentialConfigFile string   // "" = use config file / ambient credentials
}

// Name implements providers.Scanner.
func (s *Scanner) Name() string { return "gcp" }

// LongDescription is the help text for `disco scan gcp --help`.
func (s *Scanner) LongDescription() string {
	return `Scan GCP resources across reachable projects.

Project scope comes from Application Default Credentials (gcloud auth
application-default login) or the explicit 'projects:' list in
config.yaml. There is no --regions / --profile flag — GCP fans out per
project against each service's default scope. --services narrows the
scanner set when iterating on one provider.

Keyless auth (Workload Identity Federation): pass --credential-config
(or set gcp.credential_config_file) to a cred-config file from
'gcloud iam workload-identity-pools create-cred-config' to authenticate
without downloading a service-account key. On AWS ECS/Fargate, where the
task-role identity is reachable only via the container-credentials
endpoint, set DISCO_GCP_WIF_AUDIENCE + DISCO_GCP_WIF_SERVICE_ACCOUNT
instead.

Examples:
  disco scan gcp
  disco scan gcp --services gcp:compute,gcp:storage
  disco scan gcp --credential-config ./wif-cred-config.json`
}

// ServiceFilterExample is the --services example shown in gcp scan help.
func (s *Scanner) ServiceFilterExample() string { return "gcp:compute,gcp:storage" }

// ScopeColumnWidth pins the scan progress scope column to fit a project ID.
func (s *Scanner) ScopeColumnWidth() int { return 30 }

// SetServiceFilter restricts the scan to the named services (e.g. "gcp:compute", "gcp:gke").
// An empty or nil slice scans all registered services.
func (s *Scanner) SetServiceFilter(services []string) { s.serviceFilter = services }

// SetCredentialConfigOverride pins this scan's GCP authentication to the
// credential-configuration file at path — a Workload Identity Federation
// cred-config (keyless) or a service-account key. Overrides the config file's
// gcp.credential_config_file. Implements providers.CredentialConfigOverrider.
func (s *Scanner) SetCredentialConfigOverride(path string) { s.credentialConfigFile = path }

// ServiceNames returns the names of all services this scanner will report.
func (s *Scanner) ServiceNames() []string {
	svcs := filteredServices(s.serviceFilter)
	names := make([]string, len(svcs))
	for i, svc := range svcs {
		names[i] = svc.name
	}
	return names
}

// Scan discovers all GCP resources across all accessible projects.
func (s *Scanner) Scan(ctx context.Context, st *store.Store, scanID string) error {
	projects, err := loadProjects(ctx, s.credentialConfigFile)
	if err != nil {
		return fmt.Errorf("gcp: load projects: %w", err)
	}

	// Phase 1a: build the org → folder → project hierarchy first so that
	// project resources can reference their parent IDs in the closure table.
	scopes, err := scanHierarchy(ctx, projects, st, scanID)
	if err != nil {
		return fmt.Errorf("gcp: hierarchy: %w", err)
	}

	// Phase 1a': run org/folder-scope services once per scan, before per-project
	// fan-out. Targets sit above the project boundary (VPC-SC, folder/org IAM
	// policies, org-scope Logging sinks). Skipped when no scopes were resolved.
	runOrgServices(ctx, scopes, s.serviceFilter, st, scanID)

	// Phase 1b: scan all project-scoped resources in parallel across projects.
	filter := s.serviceFilter
	g, ctx := errgroup.WithContext(ctx)
	for i := range projects {
		p := &projects[i]
		g.Go(func() error { return scanProject(ctx, p, filter, st, scanID) })
	}
	if err := g.Wait(); err != nil {
		return err
	}

	// Phase 2: resolve relationships now that all resources are in the DB.
	st.ReportResolveStart("gcp")
	var counter atomic.Int64
	st2 := st.WithRelCounter(&counter)
	for i := range projects {
		resolveRelationships(ctx, &projects[i], st2)
	}

	// Phase 2b: org/customer-scoped resolvers (accesscontextmanager,
	// cloudidentity) run once per scan, after every per-project resolver —
	// their source types' AccountID is an org/folder/customer identifier, not
	// a project ID, so they query across every scope in one pass rather than
	// once per project.
	resolveOrgRelationships(ctx, st2)

	st.ReportResolveComplete("gcp", int(counter.Load()))
	return nil
}

// resolveRelationships is phase 2 for GCP: derive edges between resources
// already written to the DB. Resolvers run concurrently (bounded by
// maxConcurrentServices) since they operate on disjoint resource types; a
// failure in one is reported and does not stop the others — partial graph
// beats no graph.
func resolveRelationships(ctx context.Context, p *project, st *store.Store) {
	_ = forEachItem(ctx, maxConcurrentServices, registeredResolvers,
		func(_ context.Context, r resolverEntry) error {
			// Each resolver gets its own buffered store so concurrent resolvers
			// stay isolated; flush collapses the per-edge autocommit
			// serialisation into one tx per resolver.
			bs := st.BeginRelBuffer()
			if err := r.fn(p, bs); err != nil {
				st.ReportError(store.ScanError{
					Provider: "gcp", Service: "resolve", Scope: p.ID, Message: err.Error(),
				})
			}
			if ferr := bs.FlushRelBuffer(); ferr != nil {
				st.ReportError(store.ScanError{
					Provider: "gcp", Service: "resolve", Scope: p.ID, Message: ferr.Error(),
				})
			}
			return nil // resolver errors are reported, never abort siblings
		})
}

// resolveOrgRelationships runs every registered org-scoped resolver exactly
// once per scan (mirrors runOrgServices' once-per-scan scanner lane). Errors
// are reported and never abort siblings, matching resolveRelationships'
// per-project convention.
func resolveOrgRelationships(ctx context.Context, st *store.Store) {
	_ = forEachItem(ctx, maxConcurrentServices, registeredOrgResolvers,
		func(_ context.Context, r orgResolverEntry) error {
			bs := st.BeginRelBuffer()
			if err := r.fn(bs); err != nil {
				st.ReportError(store.ScanError{
					Provider: "gcp", Service: "resolve", Scope: "org", Message: err.Error(),
				})
			}
			if ferr := bs.FlushRelBuffer(); ferr != nil {
				st.ReportError(store.ScanError{
					Provider: "gcp", Service: "resolve", Scope: "org", Message: ferr.Error(),
				})
			}
			return nil // resolver errors are reported, never abort siblings
		})
}

// scanProject runs all per-project service scanners in parallel, bounded by
// maxConcurrentServices to avoid API rate limits.
func scanProject(ctx context.Context, p *project, services []string, st *store.Store, scanID string) error {
	sem := semaphore.NewWeighted(maxConcurrentServices)
	g, gctx := errgroup.WithContext(ctx)
	for _, svc := range filteredServices(services) {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(gctx, serviceTimeout)
			defer cancel()
			var newC, changedC atomic.Int64
			total, _, err := svc.fn(svcCtx, p, st.WithUpsertCounters(&newC, &changedC), scanID)
			if err != nil {
				// First rung, ahead of every skip: a failed store write is not a
				// GCP-side condition and must never be reported as one. Mirrors
				// the AWS and Azure dispatchers.
				if errors.Is(err, store.ErrStoreWrite) {
					st.ReportError(store.ScanError{
						Provider: "gcp", Service: svc.name, Scope: p.ID, Message: err.Error(),
					})
					st.ReportService(svc.name, p.ID, total, int(newC.Load()), int(changedC.Load()), 1, store.ServiceOK)
					return nil
				}
				if errors.Is(err, errServiceDisabled) {
					// GCP API not enabled in this project — surface as
					// "(project: disabled)" suffix instead of a warning.
					st.ReportService(svc.name, p.ID, 0, 0, 0, 0, store.ServiceDisabled)
					return nil
				}
				if errors.Is(err, errBillingDisabled) {
					// Project billing off (self-enableable) — surface as
					// "(project: billing disabled)" instead of a warning/error.
					st.ReportService(svc.name, p.ID, 0, 0, 0, 0, store.ServiceBillingDisabled)
					return nil
				}
				return err
			}
			st.ReportService(svc.name, p.ID, total, int(newC.Load()), int(changedC.Load()), 0, store.ServiceOK)
			return nil
		})
	}
	return g.Wait()
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

// project holds a resolved GCP project with its parent hierarchy IDs.
type project struct {
	ID       string // GCP project ID (e.g. "my-project-123")
	Name     string // display name
	Number   string // numeric project number
	ParentID string // disco resource ID of the immediate parent (folder or org)
}

func mustJSON(v any) string { return util.MustJSON(v) }

// regionGlobal is the canonical Region pointer for non-regional GCP
// resources (org/folder-scope services, IAM policy synth resources).
// Mirrors AWS / Azure regionGlobal; see
// store/CLAUDE.md "region = \"global\" sentinel".
var regionGlobal = func() *string { s := "global"; return &s }()

// strp returns a pointer to s, or nil if s is empty.
func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
