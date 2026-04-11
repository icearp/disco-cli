// Package gcp implements cloud resource discovery for Google Cloud Platform.
// It makes per-service REST API calls using google.golang.org/api and follows
// the two-phase scan pattern: resources (and the org→folder→project hierarchy)
// are written first, relationships second.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"codeburg.org/icearp/disco/internal/providers"
	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
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
	if err := scanHierarchy(ctx, projects, st, scanID); err != nil {
		return fmt.Errorf("gcp: hierarchy: %w", err)
	}

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
	for i := range projects {
		if err := resolveRelationships(ctx, &projects[i], st); err != nil {
			return fmt.Errorf("gcp relationships %s: %w", projects[i].ID, err)
		}
	}
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
		svc := svc
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			svcCtx, cancel := context.WithTimeout(gctx, serviceTimeout)
			defer cancel()
			total, inserted, err := svc.fn(svcCtx, p, st, scanID)
			if err != nil {
				return err
			}
			st.ReportService(svc.name, total, inserted)
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

// isPermissionDenied reports whether err is a GCP 403 / permission denied error.
func isPermissionDenied(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusForbidden || gerr.Code == http.StatusUnauthorized
	}
	return false
}

// skipIfDenied logs a concise error message (no Details JSON) and returns nil.
func skipIfDenied(service, projectID string, err error) error {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		reason := ""
		if len(gerr.Errors) > 0 {
			reason = gerr.Errors[0].Reason
		}
		if reason != "" {
			log.Printf("warn: gcp %s %s: %d %s (%s) (skipping)", service, projectID, gerr.Code, gerr.Message, reason)
		} else {
			log.Printf("warn: gcp %s %s: %d %s (skipping)", service, projectID, gerr.Code, gerr.Message)
		}
		return nil
	}
	log.Printf("warn: gcp %s %s: %v (skipping)", service, projectID, err)
	return nil
}
