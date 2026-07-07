package gcp

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	dataflow "google.golang.org/api/dataflow/v1b3"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:dataflow",
		fn:   scanDataflow,
		emits: []coverage.TypeDecl{
			{Service: "dataflow", DiscoType: TypeDataflowJob},
			{Service: "dataflow", DiscoType: TypeDataflowSnapshot},
		},
	})
}

// maxConcurrentDataflowSnapshotFanout caps the per-region Snapshot fan-out.
const maxConcurrentDataflowSnapshotFanout = 10

// scanDataflow discovers Dataflow jobs across all locations via the
// aggregated `Projects.Jobs.Aggregated` endpoint — no per-region fan-out
// needed; the API exposes a project-wide variant. Snapshots have no such
// aggregated endpoint, so they fan out per region (Compute regions catalog,
// same gcpRegions helper used elsewhere).
func scanDataflow(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := dataflow.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("dataflow client: %w", err)
	}
	regions, err := gcpRegions(ctx, p)
	if err != nil {
		return 0, 0, err
	}
	return scanDataflowWithClient(ctx, svc, regions, p, st, scanID)
}

// scanDataflowWithClient is the test seam for scanDataflow — takes the
// pre-built client plus a pre-resolved region list directly, so tests can
// point the client at a fake server and inject regions without a real
// compute.Regions.List dependency.
func scanDataflowWithClient(ctx context.Context, svc *dataflow.Service, regions []string, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	err = svc.Projects.Jobs.Aggregated(p.ID).Context(ctx).Pages(ctx, func(page *dataflow.ListJobsResponse) error {
		for _, j := range page.Jobs {
			if j == nil || j.Id == "" {
				continue
			}
			name := j.Name
			region := j.Location
			nativeID := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", p.ID, j.Location, j.Id)
			r := &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeDataflowJob,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &region,
				CreatedAt:      strp(j.CreateTime),
				Status:         &j.CurrentState,
				AttributesJSON: mustJSON(j),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		return nil
	})
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "dataflow:jobs.aggregated", p.ID, err)
		}
		return 0, 0, err
	}
	if len(batch) > 0 {
		total, inserted, err = upsertWithProjClosure(p, st, batch)
		if err != nil {
			return total, inserted, err
		}
	}

	// Per-region fan-out — Snapshots. Nested under a fan-out that only runs
	// after Jobs.Aggregated (above) already proved the dataflow API enabled
	// for this project — never let a nested isAPINotEnabled-shaped error
	// escalate to the whole-service disabled sentinel.
	var mu sync.Mutex
	ferr := forEachItem(ctx, maxConcurrentDataflowSnapshotFanout, regions, func(gctx context.Context, region string) error {
		out, serr := svc.Projects.Locations.Snapshots.List(p.ID, region).Context(gctx).Do()
		if serr != nil {
			if isPermissionDenied(serr) {
				_ = skipIfDenied(st, "dataflow:snapshots.list", p.ID, serr)
				return nil
			}
			return serr
		}
		sbatch := make([]*store.Resource, 0, len(out.Snapshots))
		for _, s := range out.Snapshots {
			if s == nil || s.Id == "" {
				continue
			}
			name := s.Id
			nativeID := fmt.Sprintf("projects/%s/locations/%s/snapshots/%s", p.ID, region, s.Id)
			sbatch = append(sbatch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeDataflowSnapshot,
				NativeID:       nativeID,
				Name:           &name,
				Region:         strp(region),
				CreatedAt:      strp(s.CreationTime),
				Status:         strp(s.State),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		mu.Lock()
		defer mu.Unlock()
		st2, sn2, uerr := upsertWithProjClosure(p, st, sbatch)
		total += st2
		inserted += sn2
		return uerr
	})
	if ferr != nil {
		return total, inserted, ferr
	}
	return total, inserted, nil
}
