package gcp

import (
	"context"
	"errors"

	"codeberg.org/icearp/disco/internal/store"
)

// serviceEntry describes a scannable GCP service (scoped to one project).
type serviceEntry struct {
	name string
	fn   func(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error)
}

// registeredServices is populated by each *_scanners.go file's init().
// Adding a new service only requires creating a new file and calling registerService
// from its init() — no changes to gcp.go are needed.
var registeredServices []serviceEntry

// registerService adds a service to the package-level registry.
// Panics if a service with the same name has already been registered, which
// catches copy-paste errors that would otherwise silently scan a service twice.
func registerService(e serviceEntry) {
	for _, s := range registeredServices {
		if s.name == e.name {
			panic("disco: duplicate GCP service registration: " + e.name)
		}
	}
	registeredServices = append(registeredServices, e)
}

// orgScope identifies an organization- or folder-scope target for org-services.
// Populated by scanHierarchy from the project parents discovered during the scan.
type orgScope struct {
	Kind     string // "organization" or "folder"
	Name     string // canonical GCP name, e.g. "organizations/123" / "folders/456"
	Resource string // disco resource ID (store.ResourceID(...) result)
}

// orgServiceEntry describes a scannable org/folder-scope GCP service. The fn
// runs ONCE per scan (not once per project) and receives every org+folder
// scope discovered by scanHierarchy. Targets: VPC Service Controls, folder/org
// IAM policies, org-scope Logging sinks. Empty scopes => fn is skipped.
type orgServiceEntry struct {
	name string
	fn   func(ctx context.Context, scopes []orgScope, st *store.Store, scanID string) (total, inserted int, err error)
}

// registeredOrgServices is populated by org-scope *_scanners.go files' init().
var registeredOrgServices []orgServiceEntry

// registerOrgService adds an org/folder-scope service to the registry.
// Panics on duplicate name to catch copy-paste errors at init time.
func registerOrgService(e orgServiceEntry) {
	for _, s := range registeredOrgServices {
		if s.name == e.name {
			panic("disco: duplicate GCP org service registration: " + e.name)
		}
	}
	registeredOrgServices = append(registeredOrgServices, e)
}

// runOrgServices fires every registered org/folder-scope service exactly once
// per scan, after scanHierarchy and before per-project fan-out. Errors per
// service are reported via st.ReportError + st.ReportService — never propagated
// (matches the per-service convention in scanProject). Skipped when no
// org/folder scopes were resolved (e.g. user only has project-level creds).
func runOrgServices(ctx context.Context, scopes []orgScope, filter []string, st *store.Store, scanID string) {
	if len(scopes) == 0 || len(registeredOrgServices) == 0 {
		return
	}
	allowed := serviceFilterSet(filter)
	for _, svc := range registeredOrgServices {
		if allowed != nil && !allowed[svc.name] {
			continue
		}
		total, inserted, err := svc.fn(ctx, scopes, st, scanID)
		if err != nil {
			// API-not-enabled at the org scope (accesscontextmanager,
			// org-policy, etc.) returns the errServiceDisabled sentinel —
			// mirror scanProject and surface "(service disabled)" instead
			// of an error.
			if errors.Is(err, errServiceDisabled) {
				st.ReportService(svc.name, 0, 0, 0, true)
				continue
			}
			st.ReportError(store.ScanError{
				Provider: "gcp", Service: svc.name, Scope: "org",
				Message: err.Error(),
			})
			st.ReportService(svc.name, total, inserted, 1, false)
			continue
		}
		st.ReportService(svc.name, total, inserted, 0, false)
	}
}

// serviceFilterSet returns nil for an empty filter (i.e. allow all), or a set
// of allowed names. Shared by the project-scope and org-scope dispatch loops.
func serviceFilterSet(filter []string) map[string]bool {
	if len(filter) == 0 {
		return nil
	}
	out := make(map[string]bool, len(filter))
	for _, name := range filter {
		out[name] = true
	}
	return out
}

// resolverEntry describes a phase-2 relationship resolver.
type resolverEntry struct {
	fn func(p *project, st *store.Store) error
}

// registeredResolvers is populated by each *_resolvers.go file's init().
var registeredResolvers []resolverEntry

// registerResolver adds a resolver to the package-level registry.
func registerResolver(fn func(p *project, st *store.Store) error) {
	registeredResolvers = append(registeredResolvers, resolverEntry{fn: fn})
}
