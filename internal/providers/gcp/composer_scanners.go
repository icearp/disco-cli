package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/composer/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:composer",
		fn:   scanComposer,
		emits: []coverage.TypeDecl{
			{Service: "composer", DiscoType: TypeComposerEnv},
		},
	})
}

// scanComposer discovers Cloud Composer environments. Uses the
// `locations/-` wildcard parent (supported by composer/v1) so all
// per-location environments come back in one paginated call.
//
// Composer SDK doesn't expose a Locations.List, so an explicit per-location
// fan-out is not implementable cheaply anyway — the wildcard is the only
// reasonable shape.
func scanComposer(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := composer.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("composer client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	return runPaginated(ctx, st, p, "composer:environments.list",
		svc.Projects.Locations.Environments.List(parent),
		func(page *composer.ListEnvironmentsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Environments))
			for _, e := range page.Environments {
				name := lastSegment(e.Name)
				region := locationFromResourceName(e.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeComposerEnv,
					NativeID:       e.Name,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(e.CreateTime),
					Status:         strp(e.State),
					AttributesJSON: mustJSON(e),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}
