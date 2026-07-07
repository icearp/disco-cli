package gcp

import (
	"context"
	"fmt"
	"sync"

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
			{Service: "secretmanager", DiscoType: TypeSecretVersion},
		},
	})
}

// maxConcurrentSecretVersionFanout caps the per-Secret Version fan-out.
const maxConcurrentSecretVersionFanout = 10

// scanSecrets discovers Secret Manager secrets for a project, then fans out
// per secret for Versions. SecretVersions.List's response carries no payload
// — the SDK docs are explicit ("This call does not return secret data"), the
// payload requires a separate AccessSecretVersion call disco never invokes —
// so only version metadata (state, timestamps, replication) is stored.
// Rotation queries can still pivot off the parent secret's `topics` and
// `rotation` attributes.
func scanSecrets(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := secretmanager.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("secretmanager client: %w", err)
	}
	return scanSecretsWithClient(ctx, svc, p, st, scanID)
}

// scanSecretsWithClient is the test seam for scanSecrets — takes the
// pre-built client directly so tests can point it at a fake server.
func scanSecretsWithClient(ctx context.Context, svc *secretmanager.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	type secretRef struct {
		name string
		id   string
	}
	var secretRefs []secretRef
	parent := fmt.Sprintf("projects/%s", p.ID)
	t, n, err := runPaginated(ctx, st, p, "secretmanager:secrets.list",
		svc.Projects.Secrets.List(parent),
		func(page *secretmanager.ListSecretsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Secrets))
			for _, s := range page.Secrets {
				if s == nil || s.Name == "" {
					continue
				}
				secretRefs = append(secretRefs, secretRef{
					name: s.Name,
					id:   store.ResourceID("gcp", p.ID, TypeSecret, s.Name),
				})
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
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Per-Secret fan-out — Versions. Nested under a fan-out that only runs
	// after Secrets.List (above) already proved the secretmanager API
	// enabled for this project — never let a nested isAPINotEnabled-shaped
	// error escalate to the whole-service disabled sentinel.
	var mu sync.Mutex
	err = forEachItem(ctx, maxConcurrentSecretVersionFanout, secretRefs, func(gctx context.Context, s secretRef) error {
		verr := svc.Projects.Secrets.Versions.List(s.name).Pages(gctx, func(page *secretmanager.ListSecretVersionsResponse) error {
			batch := make([]*store.Resource, 0, len(page.Versions))
			for _, v := range page.Versions {
				if v == nil || v.Name == "" {
					continue
				}
				name := lastSegment(v.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeSecretVersion,
					NativeID:       v.Name,
					Name:           &name,
					CreatedAt:      strp(v.CreateTime),
					Status:         strp(v.State),
					AttributesJSON: mustJSON(v),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			vt, vn, verr := upsertWithParent(st, batch, s.id)
			total += vt
			inserted += vn
			return verr
		})
		if verr != nil {
			if isPermissionDenied(verr) {
				_ = skipIfDenied(st, "secretmanager:versions.list", p.ID, verr)
			} else {
				return verr
			}
		}
		return nil
	})
	if err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}
