package gcp

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/sqladmin/v1"
)

// scanCloudSQL discovers Cloud SQL instances for a project.
func scanCloudSQL(ctx context.Context, p *project, st *store.Store, scanID string) error {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := sqladmin.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("sqladmin client: %w", err)
	}

	projParentID := store.ResourceID("gcp", p.ID, "gcp:cloudresourcemanager:project", p.ID)

	out, err := svc.Instances.List(p.ID).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return skipIfDenied("sqladmin:instances.list", p.ID, err)
		}
		return fmt.Errorf("sqladmin:instances.list %s: %w", p.ID, err)
	}
	var batch []*store.Resource
	for _, inst := range out.Items {
		name := inst.Name
		region := inst.Region
		status := inst.State
		r := &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           "gcp:sqladmin:instance",
			NativeID:       fmt.Sprintf("projects/%s/instances/%s", p.ID, name),
			Name:           &name,
			Region:         &region,
			Status:         &status,
			AttributesJSON: mustJSON(inst),
			ScanID:         scanID,
			ParentID:       &projParentID,
		}
		if len(inst.Settings.UserLabels) > 0 {
			s := mustJSON(inst.Settings.UserLabels)
			r.TagsJSON = &s
		}
		batch = append(batch, r)
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert Cloud SQL instances: %w", err)
		}
	}
	return nil
}
