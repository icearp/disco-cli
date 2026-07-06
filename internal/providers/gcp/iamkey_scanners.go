package gcp

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/iam/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:iam-key",
		fn:   scanIAMServiceAccountKeys,
		emits: []coverage.TypeDecl{
			{Service: "iam", DiscoType: TypeIAMSAKey},
		},
	})
}

// maxConcurrentSAKeyFetches caps the per-SA Keys.List fan-out within a project.
// Tightly bounded: Keys.List is unauthenticated for some access patterns and
// the iam.googleapis.com quota is shared across the project.
const maxConcurrentSAKeyFetches = 10

// scanIAMServiceAccountKeys discovers IAM service account keys (user- and
// system-managed) for every service account in a project. Each key becomes
// a gcp:iam:key resource whose NativeID is the API's full key resource name.
// The phase-2 resolver derives a key -[attached-to]-> service-account edge by
// stripping the "/keys/{id}" suffix.
func scanIAMServiceAccountKeys(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := iam.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("iam client: %w", err)
	}

	// Phase A: list every SA name in the project. Reuse the same paginated
	// API as the SA scanner so this scanner doesn't depend on scan order.
	parent := fmt.Sprintf("projects/%s", p.ID)
	var saNames []string
	if _, _, err := runPaginated(ctx, st, p, "iam:serviceAccounts.list",
		svc.Projects.ServiceAccounts.List(parent),
		func(page *iam.ListServiceAccountsResponse) (int, int, error) {
			for _, sa := range page.Accounts {
				saNames = append(saNames, sa.Name)
			}
			return 0, 0, nil
		}); err != nil {
		return 0, 0, err
	}

	// Phase B: fan-out Keys.List per SA, bounded by maxConcurrentSAKeyFetches.
	var mu sync.Mutex
	var batch []*store.Resource
	if err := forEachItem(ctx, maxConcurrentSAKeyFetches, saNames, func(gctx context.Context, name string) error {
		resp, err := svc.Projects.ServiceAccounts.Keys.List(name).Context(gctx).Do()
		if err != nil {
			if isPermissionDenied(err) {
				return skipIfDenied(st, "iam:serviceAccounts.keys.list", p.ID, err)
			}
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, k := range resp.Keys {
			keyName := k.Name
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeIAMSAKey,
				NativeID:       keyName,
				Name:           &keyName,
				CreatedAt:      strp(k.ValidAfterTime),
				AttributesJSON: mustJSON(k),
				DiscoveredBy:   scanID,
			})
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, e := st.UpsertResources(batch)
	if e != nil {
		return 0, 0, fmt.Errorf("upsert IAM SA keys: %w", e)
	}
	return len(batch), n, nil
}
