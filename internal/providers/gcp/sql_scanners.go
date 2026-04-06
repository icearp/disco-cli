package gcp

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/sqladmin/v1"
)

func init() { registerService(serviceEntry{name: "gcp:sql", fn: scanCloudSQL}) }

// scanCloudSQL discovers Cloud SQL instances for a project. Uses Pages() so
// that projects with many instances are not silently truncated at the default
// page size.
func scanCloudSQL(ctx context.Context, p *project, st *store.Store, scanID string) error {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := sqladmin.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("sqladmin client: %w", err)
	}

	err = svc.Instances.List(p.ID).Pages(ctx, func(page *sqladmin.InstancesListResponse) error {
		var batch []*store.Resource
		for _, inst := range page.Items {
			name := inst.Name
			region := inst.Region
			status := inst.State
			r := &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeSQLInstance,
				NativeID:       fmt.Sprintf("projects/%s/instances/%s", p.ID, name),
				Name:           &name,
				Region:         &region,
				CreatedAt:      strp(inst.CreateTime),
				Status:         &status,
				AttributesJSON: mustJSON(inst),
				DiscoveredBy:   scanID,
			}
			if len(inst.Settings.UserLabels) > 0 {
				s := mustJSON(inst.Settings.UserLabels)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) == 0 {
			return nil
		}
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert Cloud SQL instances: %w", err)
		}
		return nil
	})
	if err != nil {
		if isPermissionDenied(err) {
			return skipIfDenied("sqladmin:instances.list", p.ID, err)
		}
		return fmt.Errorf("sqladmin:instances.list %s: %w", p.ID, err)
	}
	return nil
}
