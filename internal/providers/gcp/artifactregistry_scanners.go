package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/artifactregistry/v1"
)

func init() { registerService(serviceEntry{name: "gcp:artifactregistry", fn: scanArtifactRegistry}) }

// scanArtifactRegistry discovers Artifact Registry repositories across every
// location via the `locations/-` wildcard. Repositories carry the package
// format (DOCKER / NPM / MAVEN / PYTHON / APT / YUM / GO / KFP) inline; no
// per-format scanner is needed. Per-package + per-version fan-out deferred —
// version cardinality is unbounded on busy registries and the package list
// rarely contributes graph-meaningful edges.
func scanArtifactRegistry(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := artifactregistry.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("artifactregistry client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	err = svc.Projects.Locations.Repositories.List(parent).Pages(ctx, func(page *artifactregistry.ListRepositoriesResponse) error {
		var batch []*store.Resource
		for _, r := range page.Repositories {
			name := lastSegment(r.Name)
			region := locationFromResourceName(r.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeArtifactRepository,
				NativeID:       r.Name,
				Name:           &name,
				Region:         strp(region),
				CreatedAt:      strp(r.CreateTime),
				AttributesJSON: mustJSON(r),
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
			return 0, 0, skipIfDenied(st, "artifactregistry:repositories.list", p.ID, err)
		}
		return 0, 0, err
	}
	return total, inserted, nil
}
