package gcp

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/storage/v1"
)

// scanStorage discovers Cloud Storage buckets for a project.
func scanStorage(ctx context.Context, p *project, st *store.Store, scanID string) error {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := storage.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("storage client: %w", err)
	}

	projParentID := store.ResourceID("gcp", p.ID, "gcp:cloudresourcemanager:project", p.ID)

	req := svc.Buckets.List(p.ID)
	if err := req.Pages(ctx, func(page *storage.Buckets) error {
		var batch []*store.Resource
		for _, b := range page.Items {
			name := b.Name
			region := b.Location
			r := &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           "gcp:storage:bucket",
				NativeID:       b.SelfLink,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(b),
				ScanID:         scanID,
				ParentID:       &projParentID,
			}
			if len(b.Labels) > 0 {
				s := mustJSON(b.Labels)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) == 0 {
			return nil
		}
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert GCS buckets: %w", err)
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return skipIfDenied("storage:buckets.list", p.ID, err)
		}
		return err
	}
	return nil
}
