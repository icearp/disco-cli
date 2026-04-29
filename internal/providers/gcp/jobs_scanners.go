package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/batch/v1"
	"google.golang.org/api/run/v2"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:cloudrunjobs",
		fn:   scanCloudRunJobs,
		emits: []coverage.TypeDecl{
			{Service: "run", DiscoType: TypeCloudRunJob},
		},
	})
	registerService(serviceEntry{
		name: "gcp:batch",
		fn:   scanBatchJobs,
		emits: []coverage.TypeDecl{
			{Service: "batch", DiscoType: TypeBatchJob},
		},
	})
}

// scanCloudRunJobs discovers Cloud Run v2 Jobs (sibling surface to Cloud Run
// Services from R4.10). Uses the locations/- wildcard parent.
func scanCloudRunJobs(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := run.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("run client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	return runPaginated(ctx, st, p, "run:jobs.list",
		svc.Projects.Locations.Jobs.List(parent),
		func(page *run.GoogleCloudRunV2ListJobsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Jobs))
			for _, j := range page.Jobs {
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
}

// scanBatchJobs discovers Cloud Batch jobs. Like Cloud Run Jobs, uses the
// locations/- wildcard. Job task groups + per-task data deferred — task
// objects are runtime artifacts, not graph-meaningful.
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
