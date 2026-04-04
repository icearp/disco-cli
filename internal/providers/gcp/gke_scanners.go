package gcp

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/container/v1"
)

// scanGKE discovers GKE clusters for a project across all locations.
func scanGKE(ctx context.Context, p *project, st *store.Store, scanID string) error {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := container.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("container client: %w", err)
	}

	projParentID := store.ResourceID("gcp", p.ID, "gcp:cloudresourcemanager:project", p.ID)

	// "-" as location returns clusters across all locations.
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	out, err := svc.Projects.Locations.Clusters.List(parent).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return skipIfDenied("container:clusters.list", p.ID, err)
		}
		return fmt.Errorf("container:clusters.list %s: %w", p.ID, err)
	}
	var batch []*store.Resource
	for _, c := range out.Clusters {
		name := c.Name
		region := c.Location
		status := c.Status
		r := &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           "gcp:container:cluster",
			NativeID:       c.SelfLink,
			Name:           &name,
			Region:         &region,
			Status:         &status,
			AttributesJSON: mustJSON(c),
			ScanID:         scanID,
			ParentID:       &projParentID,
		}
		if len(c.ResourceLabels) > 0 {
			s := mustJSON(c.ResourceLabels)
			r.TagsJSON = &s
		}
		batch = append(batch, r)
	}
	if len(batch) > 0 {
		if err := st.UpsertResources(batch); err != nil {
			return fmt.Errorf("upsert GKE clusters: %w", err)
		}
	}
	return nil
}
