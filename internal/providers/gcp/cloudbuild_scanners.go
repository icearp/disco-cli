package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/cloudbuild/v1"
)

func init() { registerService(serviceEntry{name: "gcp:cloudbuild", fn: scanCloudBuildTriggers}) }

// scanCloudBuildTriggers discovers Cloud Build triggers. Worker pools +
// connection (Cloud Build → GitHub/GitLab) deferred — separate sibling APIs
// (cloudbuild/v2 for repositories, v1 ProjectsLocationsWorkerPools for
// pools); each merits its own scanner iteration.
func scanCloudBuildTriggers(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := cloudbuild.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudbuild client: %w", err)
	}
	return runPaginated(ctx, st, p, "cloudbuild:triggers.list",
		svc.Projects.Triggers.List(p.ID),
		func(page *cloudbuild.ListBuildTriggersResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Triggers))
			for _, tr := range page.Triggers {
				name := tr.Name
				// Synthesize NativeID — `ResourceName` is canonical format
				// `projects/{p}/locations/global/triggers/{id}` but is not
				// always populated; fall back to a synthesized form.
				nativeID := tr.ResourceName
				if nativeID == "" {
					nativeID = fmt.Sprintf("projects/%s/triggers/%s", p.ID, tr.Id)
				}
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCloudBuildTrigger,
					NativeID:       nativeID,
					Name:           &name,
					CreatedAt:      strp(tr.CreateTime),
					AttributesJSON: mustJSON(tr),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}
