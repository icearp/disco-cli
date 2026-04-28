package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/secretmanager/v1"
)

func init() { registerService(serviceEntry{name: "gcp:secretmanager", fn: scanSecrets}) }

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
	if err := svc.Projects.Secrets.List(parent).Pages(ctx, func(page *secretmanager.ListSecretsResponse) error {
		var batch []*store.Resource
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
		if len(batch) == 0 {
			return nil
		}
		n, e := st.UpsertResources(batch)
		if e != nil {
			return fmt.Errorf("upsert secrets: %w", e)
		}
		total += len(batch)
		inserted += n

		projParentID := store.ResourceID("gcp", p.ID, TypeProject, p.ID)
		var pairs [][2]string
		for _, r := range batch {
			pairs = append(pairs, [2]string{
				store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID),
				projParentID,
			})
		}
		return st.BatchAddToHierarchyClosure(pairs)
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "secretmanager:secrets.list", p.ID, err)
		}
		return 0, 0, err
	}
	return total, inserted, nil
}
