package gcp

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"google.golang.org/api/container/v1"
)

func init() { registerService(serviceEntry{name: "gcp:gke", fn: scanGKE}) }

// scanGKE discovers GKE clusters for a project across all locations.
func scanGKE(ctx context.Context, p *project, st *store.Store, scanID string) error {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := container.NewService(ctx, opts...)
	if err != nil {
		return fmt.Errorf("container client: %w", err)
	}

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
			Type:           TypeGKECluster,
			NativeID:       c.SelfLink,
			Name:           &name,
			Region:         &region,
			CreatedAt:      strp(c.CreateTime),
			Status:         &status,
			AttributesJSON: mustJSON(c),
			DiscoveredBy:         scanID,
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
