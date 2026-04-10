package gcp

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/storage/v1"
)

func init() { registerService(serviceEntry{name: "gcp:storage", fn: scanStorage}) }

// scanStorage discovers Cloud Storage buckets for a project.
func scanStorage(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := storage.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("storage client: %w", err)
	}

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
				Type:           TypeStorageBucket,
				NativeID:       b.SelfLink,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(b),
				DiscoveredBy:   scanID,
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
		n, e := st.UpsertResources(batch)
		if e != nil {
			return fmt.Errorf("upsert GCS buckets: %w", e)
		}
		total += len(batch)
		inserted += n
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied("storage:buckets.list", p.ID, err)
		}
		return 0, 0, err
	}
	return total, inserted, nil
}
