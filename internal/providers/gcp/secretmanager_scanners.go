package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/secretmanager/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:secretmanager",
		fn:   scanSecrets,
		emits: []coverage.TypeDecl{
			{Service: "secretmanager", DiscoType: TypeSecret},
		},
	})
}

// scanSecrets discovers Secret Manager secrets for a project. SecretVersions
// are intentionally not scanned: per-secret version pagination explodes
// cardinality on long-lived secrets and the version payload is the actual
// secret material — disco's denylist already redacts payloads if accidentally
// captured, but the cleanest path is to skip them entirely. Rotation /
// last-rotation-time queries can pivot off the secret's `topics` and
// `rotation` attributes.
func scanSecrets(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := secretmanager.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("secretmanager client: %w", err)
	}

	parent := fmt.Sprintf("projects/%s", p.ID)
	return runPaginated(ctx, st, p, "secretmanager:secrets.list",
		svc.Projects.Secrets.List(parent),
		func(page *secretmanager.ListSecretsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Secrets))
			for _, s := range page.Secrets {
				name := lastSegment(s.Name)
				r := &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeSecret,
					NativeID:       s.Name,
					Name:           &name,
					CreatedAt:      strp(s.CreateTime),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				}
				if len(s.Labels) > 0 {
					lj := mustJSON(s.Labels)
					r.TagsJSON = &lj
				}
				batch = append(batch, r)
			}
			return upsertWithProjClosure(p, st, batch)
		})
}
