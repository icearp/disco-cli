package gcp

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/batch/v1"
	"google.golang.org/api/run/v2"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCloudRunJob, Service: "run", Upstream: "run.googleapis.com/Job", Redact: []redact.Rule{{Path: "template.template.containers[*].env[*].value", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeCloudRunExecution, Service: "run", Upstream: "run.googleapis.com/Execution", Redact: []redact.Rule{{Path: "template.containers[*].env[*].value", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeBatchJob, Service: "batch", Upstream: "batch.googleapis.com/Job"})
	registerService(serviceEntry{
		name: "gcp:cloudrunjobs",
		fn:   scanCloudRunJobs,
	})
	registerService(serviceEntry{
		name: "gcp:batch",
		fn:   scanBatchJobs,
	})
}

// maxConcurrentCloudRunJobFanout caps the per-Job Executions fan-out.
const maxConcurrentCloudRunJobFanout = 10

// scanCloudRunJobs discovers Cloud Run v2 Jobs (sibling to Cloud Run Services
// from R4.10) via the locations/- wildcard parent, then fans out per Job for
// Executions.
func scanCloudRunJobs(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := run.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("run client: %w", err)
	}
	return scanCloudRunJobsWithClient(ctx, svc, p, st, scanID)
}

// scanCloudRunJobsWithClient is the test seam for scanCloudRunJobs — takes
// the pre-built client directly so tests can point it at a fake server.
func scanCloudRunJobsWithClient(ctx context.Context, svc *run.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase 1: Jobs — capture (name, resourceID) pairs for the per-Job
	// Executions fan-out below.
	type jobRef struct {
		name string
		id   string
	}
	var jobRefs []jobRef
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	t, n, err := runPaginated(ctx, st, p, "run:jobs.list",
		svc.Projects.Locations.Jobs.List(parent),
		func(page *run.GoogleCloudRunV2ListJobsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Jobs))
			for _, j := range page.Jobs {
				if j == nil || j.Name == "" {
					continue
				}
				jobRefs = append(jobRefs, jobRef{
					name: j.Name,
					id:   store.ResourceID("gcp", p.ID, TypeCloudRunJob, j.Name),
				})
				name := lastSegment(j.Name)
				region := locationFromResourceName(j.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudRunJob,
					NativeID:       j.Name,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(j.CreateTime),
					AttributesJSON: mustJSON(j),
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

	// Phase 2: per-Job fan-out — Executions. Nested under a fan-out that only
	// runs after Jobs.List (phase 1) already proved the run API enabled for
	// this project — never let a nested isAPINotEnabled-shaped error escalate
	// to the whole-service disabled sentinel.
	var mu sync.Mutex
	err = forEachItem(ctx, maxConcurrentCloudRunJobFanout, jobRefs, func(gctx context.Context, j jobRef) error {
		eerr := svc.Projects.Locations.Jobs.Executions.List(j.name).Pages(gctx, func(page *run.GoogleCloudRunV2ListExecutionsResponse) error {
			batch := make([]*store.Resource, 0, len(page.Executions))
			for _, exec := range page.Executions {
				if exec == nil || exec.Name == "" {
					continue
				}
				name := lastSegment(exec.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudRunExecution,
					NativeID:       exec.Name,
					Name:           &name,
					Region:         strp(locationFromResourceName(exec.Name)),
					CreatedAt:      strp(exec.CreateTime),
					AttributesJSON: mustJSON(exec),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			et, en, eerr := upsertWithParent(st, batch, j.id)
			total += et
			inserted += en
			return eerr
		})
		if eerr != nil {
			if isPermissionDenied(eerr) {
				_ = skipIfDenied(st, "run:executions.list", p.ID, eerr)
			} else {
				return eerr
			}
		}
		return nil
	})
	if err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanBatchJobs discovers Cloud Batch jobs via the locations/- wildcard
// (like Cloud Run Jobs). Task groups + per-task data deferred — task objects
// are runtime artifacts, not graph-meaningful.
func scanBatchJobs(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := batch.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("batch client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	return runPaginated(ctx, st, p, "batch:jobs.list",
		svc.Projects.Locations.Jobs.List(parent),
		func(page *batch.ListJobsResponse) (int, int, error) {
			bbatch := make([]*store.Resource, 0, len(page.Jobs))
			for _, j := range page.Jobs {
				name := lastSegment(j.Name)
				region := locationFromResourceName(j.Name)
				bbatch = append(bbatch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBatchJob,
					NativeID:       j.Name,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(j.CreateTime),
					AttributesJSON: mustJSON(j),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, bbatch)
		})
}
