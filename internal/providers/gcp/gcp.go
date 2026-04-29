// Package gcp implements cloud resource discovery for Google Cloud Platform.
// It makes per-service REST API calls using google.golang.org/api and follows
// the two-phase scan pattern: resources (and the org→folder→project hierarchy)
// are written first, relationships second.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
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
	serviceFilter []string // nil = scan all registered services
}

func (s *Scanner) Name() string { return "gcp" }

// SetServiceFilter restricts the scan to the named services (e.g. "gcp:compute", "gcp:gke").
// An empty or nil slice scans all registered services.
func (s *Scanner) SetServiceFilter(services []string) { s.serviceFilter = services }

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
	projects, err := loadProjects(ctx)
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
		if err := resolveRelationships(ctx, &projects[i], st2); err != nil {
			return fmt.Errorf("gcp relationships %s: %w", projects[i].ID, err)
		}
	}
	st.ReportResolveComplete("gcp", int(counter.Load()))
	return nil
}

// resolveRelationships is phase 2 for GCP: derive edges between resources that
// have already been written to the DB.
func resolveRelationships(_ context.Context, p *project, st *store.Store) error {
	for _, r := range registeredResolvers {
		if err := r.fn(p, st); err != nil {
			return err
		}
	}
	return nil
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
			total, inserted, err := svc.fn(svcCtx, p, st, scanID)
			if err != nil {
				if errors.Is(err, errServiceDisabled) {
					// GCP API not enabled in this project — surface as
					// "(service disabled)" suffix instead of a warning.
					st.ReportService(svc.name, 0, 0, 0, true)
					return nil
				}
				return err
			}
			st.ReportService(svc.name, total, inserted, 0, false)
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

// — shared helpers —

// pager is the structural interface satisfied by every *.List() call result in
// google.golang.org/api/*. Every Discovery-generated client exposes
// Pages(ctx, fn) on its List request structs — generics let one helper drive
// every paginated walk in this provider.
type pager[P any] interface {
	Pages(context.Context, func(*P) error) error
}

// runPaginated drives a paginated List call with the boilerplate every GCP
// scanner phase repeats: invoke req.Pages with a page handler that returns
// (pageTotal, pageInserted, err), accumulate the totals, then classify the
// final error via isPermissionDenied / skipIfDenied. Behavior:
//
//   - API-not-enabled (`isAPINotEnabled`) → returns the wrapped errServiceDisabled
//     sentinel so the dispatch loop renders "(service disabled)".
//   - Real permission denial → records a ScanWarning, returns nil.
//   - Other errors → propagated unwrapped.
//
// `action` is the per-API label used in any ScanWarning
// (e.g. "pubsub:topics.list"). `pageHandler` builds and persists the batch
// from each page and reports its own counts; counts are summed across pages.
func runPaginated[P any](ctx context.Context, st *store.Store, p *project, action string,
	req pager[P], pageHandler func(*P) (int, int, error)) (total, inserted int, err error) {
	err = req.Pages(ctx, func(page *P) error {
		t, n, e := pageHandler(page)
		total += t
		inserted += n
		return e
	})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, action, p.ID, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

// forEachItem runs fn over each item with at most concurrency goroutines in
// flight. First non-nil error aborts siblings via the errgroup-derived
// context. Used by per-location / per-zone / per-SA fan-out scanners (KMS
// locations, DNS zones, BigQuery datasets, IAM-key SAs) so the
// errgroup+semaphore boilerplate lives in one place.
func forEachItem[T any](ctx context.Context, concurrency int, items []T, fn func(ctx context.Context, item T) error) error {
	sem := semaphore.NewWeighted(int64(concurrency))
	g, gctx := errgroup.WithContext(ctx)
	for _, it := range items {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			return fn(gctx, it)
		})
	}
	return g.Wait()
}

// gcpRegions enumerates the compute regions enabled for a project. Used by
// per-region fan-out scanners (Dataproc, Dataflow when not using its
// aggregated API, Spanner instance regions). Returns empty + nil on
// permission denial (treats lack of compute.regions.list as "no per-region
// scope possible" rather than aborting). Cache per project via the caller —
// list calls are cheap but each scanner shouldn't burn one.
func gcpRegions(ctx context.Context, p *project) ([]string, error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := svc.Regions.List(p.ID).Pages(ctx, func(page *compute.RegionList) error {
		for _, r := range page.Items {
			if r != nil && r.Name != "" {
				out = append(out, r.Name)
			}
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) || isAPINotEnabled(err) {
			return nil, nil
		}
		return nil, err
	}
	return out, nil
}

// gcpRegionFanoutScan drives the per-region fan-out pattern shared by
// GCP services that have no aggregated/wildcard list endpoint (Dataproc,
// future per-region Spanner, AI Platform regional, etc.). It enumerates
// enabled regions via gcpRegions, fans out one paginated list call per
// region (concurrency-bounded by `concurrency`), accumulates resources
// across all regions under a mutex, and finally upserts + closure-pairs
// the batch with the project as parent.
//
// Per-region permission-denied + API-not-enabled are tolerated silently
// per region (caller's region scope might be restricted) — other errors
// propagate. action is the label fed to skipIfDenied for the warning
// path. pagerFn returns a fresh pager per region. pageItems projects a
// page into items. itemToResource shapes each item; returning nil skips.
//
// Generic over Page (P) and Item (T) — works against any
// google.golang.org/api List request that exposes Pages(ctx, fn).
func gcpRegionFanoutScan[P any, T any](
	ctx context.Context,
	p *project,
	st *store.Store,
	concurrency int,
	action string,
	pagerFn func(region string) pager[P],
	pageItems func(*P) []T,
	itemToResource func(item T, region string) *store.Resource,
) (total, inserted int, err error) {
	regions, err := gcpRegions(ctx, p)
	if err != nil {
		return 0, 0, err
	}
	return gcpRegionFanoutScanIn(ctx, p, st, concurrency, regions, action, pagerFn, pageItems, itemToResource)
}

// gcpRegionFanoutScanIn is the testable core of gcpRegionFanoutScan: same
// fan-out + accumulate + upsert pipeline, but takes a pre-resolved region
// slice instead of calling gcpRegions. Lets unit tests inject an arbitrary
// region list (and skip the compute.Regions.List dependency).
func gcpRegionFanoutScanIn[P any, T any](
	ctx context.Context,
	p *project,
	st *store.Store,
	concurrency int,
	regions []string,
	action string,
	pagerFn func(region string) pager[P],
	pageItems func(*P) []T,
	itemToResource func(item T, region string) *store.Resource,
) (total, inserted int, err error) {
	if len(regions) == 0 {
		return 0, 0, nil
	}
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	if err := forEachItem(ctx, concurrency, regions, func(gctx context.Context, region string) error {
		err := pagerFn(region).Pages(gctx, func(page *P) error {
			items := pageItems(page)
			local := make([]*store.Resource, 0, len(items))
			for _, it := range items {
				if r := itemToResource(it, region); r != nil {
					local = append(local, r)
				}
			}
			if len(local) > 0 {
				mu.Lock()
				batch = append(batch, local...)
				mu.Unlock()
			}
			return nil
		})
		if err != nil {
			if isPermissionDenied(err) {
				return skipIfDenied(st, action, p.ID+"/"+region, err)
			}
			return err
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	return upsertWithProjClosure(p, st, batch)
}

// buildSAEmailIndex returns email → resource ID for every
// gcp:iam:service-account in the project. Used by every resolver that
// FK-checks a runtime SA email against the project's discovered SAs (Cloud
// Functions, Cloud Run, Composer, Cloud Build, BinAuth attestor, Cloud Run
// Jobs, Batch, IAM-policy bindings). Cross-project SA refs implicitly skip
// — won't match in-project index.
func buildSAEmailIndex(p *project, st *store.Store) (map[string]string, error) {
	sas, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeIAMServiceAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(sas))
	for _, sa := range sas {
		if i := strings.LastIndex(sa.NativeID, "/"); i >= 0 {
			out[sa.NativeID[i+1:]] = sa.ID
		}
	}
	return out, nil
}

// project holds a resolved GCP project with its parent hierarchy IDs.
type project struct {
	ID       string // GCP project ID (e.g. "my-project-123")
	Name     string // display name
	Number   string // numeric project number
	ParentID string // disco resource ID of the immediate parent (folder or org)
}

func mustJSON(v any) string { return util.MustJSON(v) }

// strp returns a pointer to s, or nil if s is empty.
func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// errServiceDisabled is a sentinel returned by per-service scanners when
// the GCP API itself is not enabled in the calling project. The scanProject
// dispatch loop detects it via errors.Is and surfaces "(service disabled)"
// on the per-service progress line — no warning, no error report. Wrap
// upstream errors via markServiceDisabled so the original message is
// preserved for debugging if anyone unwraps. Mirrors the AWS pattern in
// internal/providers/aws/aws.go.
var errServiceDisabled = errors.New("gcp service not enabled")

// markServiceDisabled wraps the upstream "API not enabled" error so the
// dispatch loop can identify it via errors.Is(err, errServiceDisabled).
// skipIfDenied returns this when isAPINotEnabled matches.
func markServiceDisabled(err error) error {
	return fmt.Errorf("%w: %s", errServiceDisabled, err.Error())
}

// isAPINotEnabled is a narrow predicate that matches the three known shapes
// GCP uses to signal "this API is not enabled in the project":
//   - 403 with message "...has not been used in project..." (most APIs)
//   - 400 with message "has not enabled..." (BigQuery, Spanner billing-flavour)
//   - error reason "accessNotConfigured" inside googleapi.Error.Errors[]
//
// Distinct from isPermissionDenied (a wider check that also fires on real
// IAM 403s); the two don't agree on every input. isAPINotEnabled is what
// gates the sentinel path; isPermissionDenied gates the warning path.
func isAPINotEnabled(err error) bool {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	if strings.Contains(gerr.Message, "has not been used in project") {
		return true
	}
	if strings.Contains(gerr.Message, "has not enabled") {
		return true
	}
	for _, e := range gerr.Errors {
		if e.Reason == "accessNotConfigured" {
			return true
		}
	}
	return false
}

// isPermissionDenied reports whether err is a GCP 403 / permission denied error.
//
// Also covers the BigQuery quirk where API-not-enabled surfaces as HTTP 400
// with message "has not enabled BigQuery" instead of the usual 403
// `accessNotConfigured`. Treating both as non-fatal lets downstream code use
// a single `skipIfDenied` path for the "service unreachable in this project"
// failure mode regardless of which HTTP code the API picks.
func isPermissionDenied(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		if gerr.Code == http.StatusForbidden || gerr.Code == http.StatusUnauthorized {
			return true
		}
		if gerr.Code == http.StatusBadRequest && strings.Contains(gerr.Message, "has not enabled") {
			return true
		}
	}
	return false
}

// skipIfDenied either escalates the error to the service-disabled sentinel
// (when isAPINotEnabled matches — see errServiceDisabled) or records it as
// a ScanWarning and returns nil. The sentinel path keeps the disabled-API
// case off the warnings block; only real permission denials warn.
func skipIfDenied(st *store.Store, service, projectID string, err error) error {
	if isAPINotEnabled(err) {
		return markServiceDisabled(err)
	}
	msg := err.Error()
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		reason := ""
		if len(gerr.Errors) > 0 {
			reason = gerr.Errors[0].Reason
		}
		if reason != "" {
			msg = fmt.Sprintf("%d %s (%s)", gerr.Code, gerr.Message, reason)
		} else {
			msg = fmt.Sprintf("%d %s", gerr.Code, gerr.Message)
		}
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "gcp",
		Service:  service,
		Scope:    projectID,
		Message:  msg,
	})
	return nil
}
