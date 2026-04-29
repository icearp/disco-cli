package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/container/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:gke",
		fn:   scanGKE,
		emits: []coverage.TypeDecl{
			{Service: "container", DiscoType: TypeGKECluster},
		},
	})
}

// scanGKE discovers GKE clusters for a project across all locations.
func scanGKE(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := container.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("container client: %w", err)
	}

	// "-" as location returns clusters across all locations.
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	out, err := svc.Projects.Locations.Clusters.List(parent).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "container:clusters.list", p.ID, err)
		}
		return 0, 0, fmt.Errorf("container:clusters.list %s: %w", p.ID, err)
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
			DiscoveredBy:   scanID,
		}
		if len(c.ResourceLabels) > 0 {
			s := mustJSON(c.ResourceLabels)
			r.TagsJSON = &s
		}
		batch = append(batch, r)
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert GKE clusters: %w", err)
		}
		total += len(batch)
		inserted += n
	}
	return total, inserted, nil
}
