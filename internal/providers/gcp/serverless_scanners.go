package gcp

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/cloudfunctions/v2"
	"google.golang.org/api/run/v2"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:cloudfunctions",
		fn:   scanCloudFunctions,
		emits: []coverage.TypeDecl{
			{Service: "cloudfunctions", DiscoType: TypeCloudFunction},
		},
	})
	registerService(serviceEntry{
		name: "gcp:cloudrun",
		fn:   scanCloudRun,
		emits: []coverage.TypeDecl{
			{Service: "run", DiscoType: TypeCloudRunSvc},
		},
	})
}

// scanCloudFunctions discovers Cloud Functions Gen1 + Gen2 (the v2 API
// surface returns both, with `environment` distinguishing them). The wildcard
// location parent `projects/{p}/locations/-` returns functions across every
// location in one paginated call — no per-location fan-out needed.
func scanCloudFunctions(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := cloudfunctions.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudfunctions client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	return runPaginated(ctx, st, p, "cloudfunctions:functions.list",
		svc.Projects.Locations.Functions.List(parent),
		func(page *cloudfunctions.ListFunctionsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Functions))
			for _, f := range page.Functions {
				name := lastSegment(f.Name)
				region := locationFromResourceName(f.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudFunction,
					NativeID:       f.Name,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(f.CreateTime),
					Status:         strp(f.State),
					AttributesJSON: mustJSON(f),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanCloudRun discovers Cloud Run v2 services. Same wildcard-location
// pattern as Functions. Cloud Run Jobs (separate sibling API surface
// `Projects.Locations.Jobs`) deferred — Jobs are R4.20.
func scanCloudRun(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := run.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("run client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	return runPaginated(ctx, st, p, "run:services.list",
		svc.Projects.Locations.Services.List(parent),
		func(page *run.GoogleCloudRunV2ListServicesResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Services))
			for _, s := range page.Services {
				name := lastSegment(s.Name)
				region := locationFromResourceName(s.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudRunSvc,
					NativeID:       s.Name,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(s.CreateTime),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// locationFromResourceName extracts the location segment from any
// `projects/{p}/locations/{loc}/...` resource name. Returns "" if the
// pattern doesn't match.
func locationFromResourceName(name string) string {
	_, rest, ok := strings.Cut(name, "/locations/")
	if !ok {
		return ""
	}
	loc, _, _ := strings.Cut(rest, "/")
	return loc
}
