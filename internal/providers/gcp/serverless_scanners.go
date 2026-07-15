package gcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/cloudfunctions/v2"
	runv1 "google.golang.org/api/run/v1"
	"google.golang.org/api/run/v2"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCloudFunction, Service: "cloudfunctions", Upstream: "cloudfunctions.googleapis.com/Function", Redact: []redact.Rule{{Path: "serviceConfig.environmentVariables.*", Mode: redact.RedactScalar}, {Path: "environmentVariables.*", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeCloudRunSvc, Service: "run", Upstream: "run.googleapis.com/Service", Redact: []redact.Rule{{Path: "template.containers[*].env[*].value", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeCloudRunRevision, Service: "run", Upstream: "run.googleapis.com/Revision", Redact: []redact.Rule{{Path: "containers[*].env[*].value", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeCloudRunWorkerPool, Service: "run", Upstream: "run.googleapis.com/WorkerPool", Redact: []redact.Rule{{Path: "template.containers[*].env[*].value", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeCloudRunInstance, Service: "run", Upstream: "run.googleapis.com/Instance", Redact: []redact.Rule{{Path: "containers[*].env[*].value", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeCloudRunDomainMapping, Service: "run", Upstream: "run.googleapis.com/Domainmapping"})
	registerType(restype.Descriptor{Type: TypeCloudRunAuthorizedDomain, Service: "run", Upstream: "run.googleapis.com/Authorizeddomain", Leaf: true})
	registerService(serviceEntry{
		name: "gcp:cloudfunctions",
		fn:   scanCloudFunctions,
	})
	registerService(serviceEntry{
		name: "gcp:cloudrun",
		fn:   scanCloudRun,
	})
}

// maxConcurrentCloudRunFanout caps the per-Service Revisions fan-out and the
// per-region WorkerPool/Instance fan-out.
const maxConcurrentCloudRunFanout = 10

// scanCloudFunctions discovers Cloud Functions Gen1 + Gen2 (v2 API returns
// both; `environment` distinguishes them). Wildcard location parent
// `projects/{p}/locations/-` returns functions across every location in one
// paginated call — no per-location fan-out needed.
func scanCloudFunctions(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := cloudfunctions.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudfunctions client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	return runPaginated(ctx, st, p, "cloudfunctions:functions.list",
		svc.Projects.Locations.Functions.List(parent),
		func(page *cloudfunctions.ListFunctionsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Functions))
			for _, f := range page.Functions {
				name := lastSegment(f.Name)
				region := locationFromResourceName(f.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudFunction,
					NativeID:       f.Name,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(f.CreateTime),
					Status:         strp(f.State),
					AttributesJSON: mustJSON(f),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanCloudRun discovers Cloud Run v2 Services via the wildcard-location
// pattern (same as Functions), then fans out per Service for Revisions, per
// region for WorkerPool/Instance, and issues two project-scoped run/v1 legacy
// calls for DomainMapping/AuthorizedDomain. Cloud Run Jobs
// (`Projects.Locations.Jobs`) are a separate sibling API surface, scanned by
// `scanCloudRunJobs` in jobs_scanners.go (which also fans out Executions).
//
// Deliberately NOT scanned (see docs/gcp-type-coverage.md Wave 11d): the
// Knative-legacy `Configuration`/`Route` types are shadow representations of
// the same Service already scanned here (one Configuration + one Route per
// Service, no independent data). `Task` (per-Execution runtime attempt, same
// unbounded-cardinality/runtime-artifact profile as Batch's Task objects —
// see jobs_scanners.go). `Location`/`Operation` are catalog/action-verb types.
func scanCloudRun(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := run.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("run client: %w", err)
	}
	svc1, err := runv1.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("run v1 client: %w", err)
	}
	// run/v2 exposes no Locations catalog of its own (unlike cloudbuild/v2);
	// reuse the Compute regions list for the WorkerPool/Instance per-region
	// fan-out below.
	regions, err := gcpRegions(ctx, p)
	if err != nil {
		return 0, 0, err
	}
	return scanCloudRunWithClient(ctx, svc, svc1, regions, p, st, scanID)
}

// scanCloudRunWithClient is the test seam for scanCloudRun — takes the
// pre-built v2 and v1 clients plus a pre-resolved region list directly, so
// tests can point the clients at a fake server and inject regions without a
// real compute.Regions.List dependency.
func scanCloudRunWithClient(ctx context.Context, svc *run.Service, svc1 *runv1.APIService, regions []string, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase 1: Services — capture (name, resourceID) pairs for the
	// per-service Revisions fan-out below.
	type serviceRef struct {
		name string
		id   string
	}
	var svcRefs []serviceRef
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	t, n, err := runPaginated(ctx, st, p, "run:services.list",
		svc.Projects.Locations.Services.List(parent),
		func(page *run.GoogleCloudRunV2ListServicesResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Services))
			for _, s := range page.Services {
				if s == nil || s.Name == "" {
					continue
				}
				svcRefs = append(svcRefs, serviceRef{
					name: s.Name,
					id:   store.ResourceID("gcp", p.ID, s.Name),
				})
				name := lastSegment(s.Name)
				region := locationFromResourceName(s.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudRunSvc,
					NativeID:       s.Name,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(s.CreateTime),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 2: per-Service fan-out — Revisions. Nested under a fan-out that
	// only runs after Services.List (phase 1) already proved the run API
	// enabled for this project — never let a nested isAPINotEnabled-shaped
	// error escalate to the whole-service disabled sentinel.
	var mu sync.Mutex
	err = forEachItem(ctx, maxConcurrentCloudRunFanout, svcRefs, func(gctx context.Context, s serviceRef) error {
		rerr := svc.Projects.Locations.Services.Revisions.List(s.name).Pages(gctx, func(page *run.GoogleCloudRunV2ListRevisionsResponse) error {
			batch := make([]*store.Resource, 0, len(page.Revisions))
			for _, rev := range page.Revisions {
				if rev == nil || rev.Name == "" {
					continue
				}
				name := lastSegment(rev.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudRunRevision,
					NativeID:       rev.Name,
					Name:           &name,
					Region:         strp(locationFromResourceName(rev.Name)),
					CreatedAt:      strp(rev.CreateTime),
					AttributesJSON: mustJSON(rev),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			rt, rn, rerr := upsertWithParent(st, batch, s.id)
			total += rt
			inserted += rn
			return rerr
		})
		if rerr != nil {
			if isPermissionDenied(rerr) {
				_ = skipIfDenied(st, "run:revisions.list", p.ID, rerr)
			} else {
				return rerr
			}
		}
		return nil
	})
	if err != nil {
		return total, inserted, err
	}

	// Phase 3: WorkerPool + Instance — per-region fan-out over the
	// caller-resolved region list (run/v2 exposes no Locations catalog of its
	// own). Nested after phase 1 — discard rather than escalate.
	err = forEachItem(ctx, maxConcurrentCloudRunFanout, regions, func(gctx context.Context, region string) error {
		locParent := fmt.Sprintf("projects/%s/locations/%s", p.ID, region)

		werr := svc.Projects.Locations.WorkerPools.List(locParent).Pages(gctx, func(page *run.GoogleCloudRunV2ListWorkerPoolsResponse) error {
			batch := make([]*store.Resource, 0, len(page.WorkerPools))
			for _, wp := range page.WorkerPools {
				if wp == nil || wp.Name == "" {
					continue
				}
				name := lastSegment(wp.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudRunWorkerPool,
					NativeID:       wp.Name,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(wp.CreateTime),
					AttributesJSON: mustJSON(wp),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			wt, wn, werr := upsertWithProjClosure(p, st, batch)
			total += wt
			inserted += wn
			return werr
		})
		if werr != nil {
			if isPermissionDenied(werr) {
				_ = skipIfDenied(st, "run:workerpools.list", p.ID, werr)
			} else {
				return werr
			}
		}

		ierr := svc.Projects.Locations.Instances.List(locParent).Pages(gctx, func(page *run.GoogleCloudRunV2ListInstancesResponse) error {
			batch := make([]*store.Resource, 0, len(page.Instances))
			for _, inst := range page.Instances {
				if inst == nil || inst.Name == "" {
					continue
				}
				name := lastSegment(inst.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudRunInstance,
					NativeID:       inst.Name,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(inst.CreateTime),
					AttributesJSON: mustJSON(inst),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			it, in, ierr := upsertWithProjClosure(p, st, batch)
			total += it
			inserted += in
			return ierr
		})
		if ierr != nil {
			if isPermissionDenied(ierr) {
				_ = skipIfDenied(st, "run:instances.list", p.ID, ierr)
			} else {
				return ierr
			}
		}
		return nil
	})
	if err != nil {
		return total, inserted, err
	}

	// Phase 4: DomainMapping — project-scoped legacy Knative call (run/v1),
	// no per-location fan-out despite the "Locations" service name (parent is
	// a bare namespace = project ID). No Pages() helper on this call (the
	// Knative-legacy List uses a Kubernetes-style watch/continue token, not
	// the standard googleapi pager) — single Do() call; domain mappings are a
	// low-cardinality legacy feature, unlikely to paginate in practice.
	dmParent := fmt.Sprintf("namespaces/%s", p.ID)
	dmResp, derr := svc1.Projects.Locations.Domainmappings.List(dmParent).Context(ctx).Do()
	if derr != nil {
		if isPermissionDenied(derr) {
			_ = skipIfDenied(st, "run:domainmappings.list", p.ID, derr)
		} else {
			return total, inserted, derr
		}
	} else {
		batch := make([]*store.Resource, 0, len(dmResp.Items))
		for _, dm := range dmResp.Items {
			if dm == nil || dm.Metadata == nil || dm.Metadata.Name == "" {
				continue
			}
			name := dm.Metadata.Name
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeCloudRunDomainMapping,
				NativeID:       fmt.Sprintf("projects/%s/domainMappings/%s", p.ID, name),
				Name:           &name,
				AttributesJSON: mustJSON(dm),
				DiscoveredBy:   scanID,
			})
		}
		dt, dn, uerr := upsertWithProjClosure(p, st, batch)
		if uerr != nil {
			return total, inserted, uerr
		}
		total += dt
		inserted += dn
	}

	// Phase 5: AuthorizedDomain — project-scoped legacy call (run/v1), no
	// location component at all. Nested after phase 1 — classify once via a
	// manual Pages() call (same shape as phases 2-4) and discard rather than
	// escalate; routing this through runPaginated would double-classify (it
	// already resolves isAPINotEnabled to the errServiceDisabled sentinel
	// internally, which isPermissionDenied can't unwrap a second time),
	// letting a nested not-enabled 403 escalate the whole scanCloudRun call.
	adParent := fmt.Sprintf("projects/%s", p.ID)
	aerr := svc1.Projects.Locations.Authorizeddomains.List(adParent).Pages(ctx, func(page *runv1.ListAuthorizedDomainsResponse) error {
		batch := make([]*store.Resource, 0, len(page.Domains))
		for _, ad := range page.Domains {
			if ad == nil || ad.Id == "" {
				continue
			}
			name := ad.Id
			nativeID := ad.Name
			if nativeID == "" {
				nativeID = fmt.Sprintf("projects/%s/authorizedDomains/%s", p.ID, ad.Id)
			}
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeCloudRunAuthorizedDomain,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(ad),
				DiscoveredBy:   scanID,
			})
		}
		at, an, uerr := upsertWithProjClosure(p, st, batch)
		total += at
		inserted += an
		return uerr
	})
	if aerr != nil {
		if isPermissionDenied(aerr) {
			_ = skipIfDenied(st, "run:authorizeddomains.list", p.ID, aerr)
		} else {
			return total, inserted, aerr
		}
	}

	return total, inserted, nil
}

// locationFromResourceName extracts the location segment from a
// `projects/{p}/locations/{loc}/...` resource name, or "" if no match.
func locationFromResourceName(name string) string {
	_, rest, ok := strings.Cut(name, "/locations/")
	if !ok {
		return ""
	}
	loc, _, _ := strings.Cut(rest, "/")
	return loc
}
