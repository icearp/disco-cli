package gcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/cloudkms/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:cloudkms",
		fn:   scanCloudKMS,
		emits: []coverage.TypeDecl{
			{Service: "cloudkms", DiscoType: TypeKMSKeyRing},
			{Service: "cloudkms", DiscoType: TypeKMSCryptoKey},
		},
	})
}

// maxConcurrentKMSLocations caps per-project location fan-out for KMS list calls.
const maxConcurrentKMSLocations = 10

// scanCloudKMS discovers KMS keyrings and crypto keys across every location
// in a project. Three phases:
//  1. Projects.Locations.List(projects/{p}) — paginated, returns all locations
//     KMS is enabled in for this project.
//  2. Per-location KeyRings.List, fan-out bounded by maxConcurrentKMSLocations.
//  3. Per-keyring CryptoKeys.List (sequential within each location goroutine —
//     keyring counts per location are typically small).
//
// CryptoKey versions and ImportJobs deferred — versions in particular have
// pagination concerns at scale.
func scanCloudKMS(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := cloudkms.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudkms client: %w", err)
	}

	// Phase 1: locations.
	parent := fmt.Sprintf("projects/%s", p.ID)
	var locations []string
	if _, _, err := runPaginated(ctx, st, p, "cloudkms:projects.locations.list",
		svc.Projects.Locations.List(parent),
		func(page *cloudkms.ListLocationsResponse) (int, int, error) {
			for _, loc := range page.Locations {
				locations = append(locations, loc.Name)
			}
			return 0, 0, nil
		}); err != nil {
		return 0, 0, err
	}

	// Phase 2 + 3: keyrings + crypto keys per location, bounded fan-out.
	// Locations.List returns the global location catalog even when the KMS
	// API has not been enabled in the project — the per-location keyRings.List
	// is what gates on enablement and surfaces a 403. To avoid logging the
	// same "API not enabled" warning once per location (~30+ noisy lines),
	// trip a shared flag on the first such 403 and skip the remaining
	// locations for this scan.
	var apiDisabled atomic.Bool
	var mu sync.Mutex
	var batch []*store.Resource
	if err := forEachItem(ctx, maxConcurrentKMSLocations, locations, func(gctx context.Context, locName string) error {
		if apiDisabled.Load() {
			return nil
		}
		region := lastSegment(locName) // "projects/{p}/locations/us-central1" → "us-central1"
		if err := svc.Projects.Locations.KeyRings.List(locName).Pages(gctx, func(krPage *cloudkms.ListKeyRingsResponse) error {
			for _, kr := range krPage.KeyRings {
				mu.Lock()
				name := lastSegment(kr.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeKMSKeyRing,
					NativeID:       kr.Name,
					Name:           &name,
					Region:         &region,
					CreatedAt:      strp(kr.CreateTime),
					AttributesJSON: mustJSON(kr),
					DiscoveredBy:   scanID,
				})
				mu.Unlock()

				if err := svc.Projects.Locations.KeyRings.CryptoKeys.List(kr.Name).Pages(gctx, func(ckPage *cloudkms.ListCryptoKeysResponse) error {
					for _, ck := range ckPage.CryptoKeys {
						mu.Lock()
						ckName := lastSegment(ck.Name)
						batch = append(batch, &store.Resource{
							Provider:       "gcp",
							AccountID:      p.ID,
							AccountName:    &p.Name,
							Type:           TypeKMSCryptoKey,
							NativeID:       ck.Name,
							Name:           &ckName,
							Region:         &region,
							CreatedAt:      strp(ck.CreateTime),
							AttributesJSON: mustJSON(ck),
							DiscoveredBy:   scanID,
						})
						mu.Unlock()
					}
					return nil
				}); err != nil {
					if isPermissionDenied(err) {
						return skipIfDenied(st, "cloudkms:cryptoKeys.list", p.ID, err)
					}
					return err
				}
			}
			return nil
		}); err != nil {
			if isPermissionDenied(err) {
				if !apiDisabled.Swap(true) {
					return skipIfDenied(st, "cloudkms:keyRings.list", p.ID, err)
				}
				return nil
			}
			return err
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
		return 0, 0, fmt.Errorf("upsert KMS resources: %w", e)
	}

	// Closure: keyring → project; crypto-key → keyring.
	projParentID := store.ResourceID("gcp", p.ID, TypeProject, p.ID)
	var pairs [][2]string
	for _, r := range batch {
		switch r.Type {
		case TypeKMSKeyRing:
			id := store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID)
			pairs = append(pairs, [2]string{id, projParentID})
		case TypeKMSCryptoKey:
			// Parent keyring NativeID = strip "/cryptoKeys/{name}" suffix.
			parentName := r.NativeID
			if i := strings.Index(parentName, "/cryptoKeys/"); i >= 0 {
				parentName = parentName[:i]
			}
			id := store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID)
			parentID := store.ResourceID(r.Provider, r.AccountID, TypeKMSKeyRing, parentName)
			pairs = append(pairs, [2]string{id, parentID})
		}
	}
	if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
		return len(batch), n, fmt.Errorf("closure KMS: %w", err)
	}
	return len(batch), n, nil
}
