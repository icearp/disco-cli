package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/composer/v1"
)

func init() { registerService(serviceEntry{name: "gcp:composer", fn: scanComposer}) }

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
	err = svc.Projects.Locations.Environments.List(parent).Pages(ctx, func(page *composer.ListEnvironmentsResponse) error {
		var batch []*store.Resource
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
		t, n, ce := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return ce
	})
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "composer:environments.list", p.ID, err)
		}
		return 0, 0, err
	}
	return total, inserted, nil
}
