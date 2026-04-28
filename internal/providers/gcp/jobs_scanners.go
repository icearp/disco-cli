package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/batch/v1"
	"google.golang.org/api/run/v2"
)

func init() {
	registerService(serviceEntry{name: "gcp:cloudrunjobs", fn: scanCloudRunJobs})
	registerService(serviceEntry{name: "gcp:batch", fn: scanBatchJobs})
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
	err = svc.Projects.Locations.Jobs.List(parent).Pages(ctx, func(page *run.GoogleCloudRunV2ListJobsResponse) error {
		var batch []*store.Resource
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
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	})
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "run:jobs.list", p.ID, err)
		}
		return 0, 0, err
	}
	return total, inserted, nil
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
	err = svc.Projects.Locations.Jobs.List(parent).Pages(ctx, func(page *batch.ListJobsResponse) error {
		var bbatch []*store.Resource
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
		t, n, e := upsertWithProjClosure(p, st, bbatch)
		total += t
		inserted += n
		return e
	})
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "batch:jobs.list", p.ID, err)
		}
		return 0, 0, err
	}
	return total, inserted, nil
}
