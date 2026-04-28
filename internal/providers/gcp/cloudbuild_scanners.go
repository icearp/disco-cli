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
	err = svc.Projects.Triggers.List(p.ID).Pages(ctx, func(page *cloudbuild.ListBuildTriggersResponse) error {
		var batch []*store.Resource
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
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	})
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "cloudbuild:triggers.list", p.ID, err)
		}
		return 0, 0, err
	}
	return total, inserted, nil
}
