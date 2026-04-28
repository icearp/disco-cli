package gcp

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"google.golang.org/api/iam/v1"
)

func init() { registerService(serviceEntry{name: "gcp:iam-key", fn: scanIAMServiceAccountKeys}) }

// maxConcurrentSAKeyFetches caps the per-SA Keys.List fan-out within a project.
// Tightly bounded — Keys.List is unauthenticated for some access patterns and
// the iam.googleapis.com quota is shared across the project.
const maxConcurrentSAKeyFetches = 10

// scanIAMServiceAccountKeys discovers IAM service account keys (user-managed
// + system-managed) for every service account in a project. Each key becomes
// a gcp:iam:service-account-key resource whose NativeID is the full key
// resource name returned by the API. The phase-2 resolver derives a
// key -[attached-to]-> service-account edge by stripping the "/keys/{id}"
// suffix.
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
	if err := svc.Projects.ServiceAccounts.List(parent).Pages(ctx, func(page *iam.ListServiceAccountsResponse) error {
		for _, sa := range page.Accounts {
			saNames = append(saNames, sa.Name)
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "iam:serviceAccounts.list", p.ID, err)
		}
		return 0, 0, err
	}

	// Phase B: fan-out Keys.List per SA, bounded by maxConcurrentSAKeyFetches.
	sem := semaphore.NewWeighted(maxConcurrentSAKeyFetches)
	g, gctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	var batch []*store.Resource
	for _, name := range saNames {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
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
				r := &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeIAMSAKey,
					NativeID:       keyName,
					Name:           &keyName,
					CreatedAt:      strp(k.ValidAfterTime),
					AttributesJSON: mustJSON(k),
					DiscoveredBy:   scanID,
				}
				batch = append(batch, r)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
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
