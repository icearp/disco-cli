package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/dataflow/v1b3"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:dataflow",
		fn:   scanDataflow,
		emits: []coverage.TypeDecl{
			{Service: "dataflow", DiscoType: TypeDataflowJob},
		},
	})
}

// scanDataflow discovers Dataflow jobs across all locations using the
// aggregated `Projects.Jobs.Aggregated` endpoint — no per-region fan-out
// needed, the Dataflow API surface includes a project-wide variant.
func scanDataflow(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := dataflow.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("dataflow client: %w", err)
	}
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
	if len(batch) == 0 {
		return 0, 0, nil
	}
	return upsertWithProjClosure(p, st, batch)
}
