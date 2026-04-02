// Package gcp implements cloud resource discovery for Google Cloud Platform.
// It makes per-service REST API calls using google.golang.org/api and follows
// the two-phase scan pattern: resources (and the org→folder→project hierarchy)
// are written first, relationships second.
package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"codeburg.org/icearp/disco/internal/providers"
	"codeburg.org/icearp/disco/internal/store"
	"golang.org/x/sync/errgroup"
	"google.golang.org/api/googleapi"
)

func init() { providers.Register(&Scanner{}) }

// Scanner implements providers.Scanner for GCP.
type Scanner struct{}

func (s *Scanner) Name() string { return "gcp" }

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
	g, ctx := errgroup.WithContext(ctx)
	for i := range projects {
		p := &projects[i]
		g.Go(func() error { return scanProject(ctx, p, st, scanID) })
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

// scanProject runs all per-project service scanners in parallel.
func scanProject(ctx context.Context, p *project, st *store.Store, scanID string) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return scanCompute(ctx, p, st, scanID) })
	g.Go(func() error { return scanStorage(ctx, p, st, scanID) })
	g.Go(func() error { return scanCloudSQL(ctx, p, st, scanID) })
	g.Go(func() error { return scanGKE(ctx, p, st, scanID) })
	g.Go(func() error { return scanIAMServiceAccounts(ctx, p, st, scanID) })
	return g.Wait()
}

// — shared helpers —

// project holds a resolved GCP project with its parent hierarchy IDs.
type project struct {
	ID       string // GCP project ID (e.g. "my-project-123")
	Name     string // display name
	Number   string // numeric project number
	ParentID string // disco resource ID of the immediate parent (folder or org)
}

// mustJSON marshals v to JSON, returning "{}" on error.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// isPermissionDenied reports whether err is a GCP 403 / permission denied error.
func isPermissionDenied(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusForbidden || gerr.Code == http.StatusUnauthorized
	}
	return false
}

// skipIfDenied logs the error and returns nil, allowing the caller to continue.
func skipIfDenied(service, projectID string, err error) error {
	log.Printf("warn: gcp %s %s: %v (skipping)", service, projectID, err)
	return nil
}
