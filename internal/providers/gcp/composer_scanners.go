package gcp

import (
	"context"
	"fmt"
	"sync"

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
			{Service: "composer", DiscoType: TypeComposerUserWorkloadsConfigMap, Leaf: true},
		},
	})
}

// maxConcurrentComposerConfigMapFanout caps the per-Environment
// UserWorkloadsConfigMap fan-out.
const maxConcurrentComposerConfigMapFanout = 10

// scanComposer discovers Cloud Composer environments. Uses the
// `locations/-` wildcard parent (supported by composer/v1) so all
// per-location environments come back in one paginated call, then fans out
// per environment for UserWorkloadsConfigMaps.
//
// Composer SDK doesn't expose a Locations.List, so per-location fan-out
// isn't cheaply implementable anyway — the wildcard is the only reasonable
// shape.
func scanComposer(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := composer.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("composer client: %w", err)
	}
	return scanComposerWithClient(ctx, svc, p, st, scanID)
}

// scanComposerWithClient is the test seam for scanComposer — takes the
// pre-built client directly so tests can point it at a fake server.
func scanComposerWithClient(ctx context.Context, svc *composer.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	type envRef struct {
		name string
		id   string
	}
	var envRefs []envRef
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	t, n, err := runPaginated(ctx, st, p, "composer:environments.list",
		svc.Projects.Locations.Environments.List(parent),
		func(page *composer.ListEnvironmentsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Environments))
			for _, e := range page.Environments {
				if e == nil || e.Name == "" {
					continue
				}
				envRefs = append(envRefs, envRef{
					name: e.Name,
					id:   store.ResourceID("gcp", p.ID, TypeComposerEnv, e.Name),
				})
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
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Per-Environment fan-out — UserWorkloadsConfigMaps. Nested under a
	// fan-out that only runs after Environments.List (above) already proved
	// the composer API enabled for this project — never let a nested
	// isAPINotEnabled-shaped error escalate to the whole-service disabled
	// sentinel.
	var mu sync.Mutex
	err = forEachItem(ctx, maxConcurrentComposerConfigMapFanout, envRefs, func(gctx context.Context, e envRef) error {
		cerr := svc.Projects.Locations.Environments.UserWorkloadsConfigMaps.List(e.name).Pages(gctx, func(page *composer.ListUserWorkloadsConfigMapsResponse) error {
			batch := make([]*store.Resource, 0, len(page.UserWorkloadsConfigMaps))
			for _, cm := range page.UserWorkloadsConfigMaps {
				if cm == nil || cm.Name == "" {
					continue
				}
				name := lastSegment(cm.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeComposerUserWorkloadsConfigMap,
					NativeID:       cm.Name,
					Name:           &name,
					AttributesJSON: mustJSON(cm),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			ct, cn, cerr := upsertWithParent(st, batch, e.id)
			total += ct
			inserted += cn
			return cerr
		})
		if cerr != nil {
			if isPermissionDenied(cerr) {
				_ = skipIfDenied(st, "composer:userWorkloadsConfigMaps.list", p.ID, cerr)
			} else {
				return cerr
			}
		}
		return nil
	})
	if err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}
