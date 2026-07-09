package gcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/cloudkms/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeKMSKeyRing, Service: "cloudkms", Upstream: "cloudkms.googleapis.com/KeyRing", Leaf: true})
	registerType(restype.Descriptor{Type: TypeKMSCryptoKey, Service: "cloudkms", Upstream: "cloudkms.googleapis.com/CryptoKey"})
	registerType(restype.Descriptor{Type: TypeKMSCryptoKeyVersion, Service: "cloudkms"})
	registerType(restype.Descriptor{Type: TypeKMSEkmConnection, Service: "cloudkms", Leaf: true})
	registerType(restype.Descriptor{Type: TypeKMSImportJob, Service: "cloudkms", Leaf: true})
	registerType(restype.Descriptor{Type: TypeKMSKeyHandle, Service: "cloudkms"})
	registerType(restype.Descriptor{Type: TypeKMSSingleTenantHsmInstance, Service: "cloudkms", Leaf: true})
	registerService(serviceEntry{
		name: "gcp:cloudkms",
		fn:   scanCloudKMS,
	})
}

// maxConcurrentKMSLocations caps per-project location fan-out for KMS list calls.
const maxConcurrentKMSLocations = 10

// scanCloudKMS discovers KMS keyrings, crypto keys, and their secondary
// resources across every location in a project, in four phases:
//  1. Projects.Locations.List(projects/{p}) — paginated, all locations KMS
//     is enabled in for this project.
//  2. Per-location KeyRings.List + EkmConnections.List + KeyHandles.List +
//     SingleTenantHsmInstances.List, fan-out bounded by
//     maxConcurrentKMSLocations.
//  3. Per-keyring CryptoKeys.List + ImportJobs.List, sequential within each
//     location goroutine — keyring counts per location are typically small.
//  4. Per-crypto-key CryptoKeyVersions.List, sequential within each keyring
//     loop — version counts per key are bounded by rotation history.
func scanCloudKMS(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := cloudkms.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudkms client: %w", err)
	}
	return scanCloudKMSWithClient(ctx, svc, p, st, scanID)
}

// scanCloudKMSWithClient is the testable core of scanCloudKMS — same body,
// but takes a pre-built client so tests can point it at a fake server
// (mirrors gcpRegionFanoutScan / gcpRegionFanoutScanIn's split).
func scanCloudKMSWithClient(ctx context.Context, svc *cloudkms.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
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
	// API isn't enabled in the project — per-location keyRings.List is what
	// gates on enablement and surfaces the 403. To avoid logging the same
	// "API not enabled" warning per location (~30+ noisy lines), trip a
	// shared flag on the first 403 and skip remaining locations for this scan.
	// Each sibling List call added alongside KeyRings gets its own flag since
	// a caller can lack one permission (e.g. cloudkms.ekmConnections.list)
	// while still holding the others — one shared flag would either mute a
	// real warning or spam the same warning per location/keyring/crypto-key.
	var (
		apiDisabled       atomic.Bool
		ekmDisabled       atomic.Bool
		keyHandleDisabled atomic.Bool
		hsmDisabled       atomic.Bool
		importJobDisabled atomic.Bool
		cryptoVerDisabled atomic.Bool
	)
	var mu sync.Mutex
	var batch []*store.Resource
	if err := forEachItem(ctx, maxConcurrentKMSLocations, locations, func(gctx context.Context, locName string) error {
		if apiDisabled.Load() {
			return nil
		}
		region := lastSegment(locName) // "projects/{p}/locations/us-central1" → "us-central1"

		if !ekmDisabled.Load() {
			if err := svc.Projects.Locations.EkmConnections.List(locName).Pages(gctx, func(ekmPage *cloudkms.ListEkmConnectionsResponse) error {
				for _, ekm := range ekmPage.EkmConnections {
					mu.Lock()
					name := lastSegment(ekm.Name)
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeKMSEkmConnection, NativeID: ekm.Name, Name: &name,
						Region:         &region,
						CreatedAt:      strp(ekm.CreateTime),
						AttributesJSON: mustJSON(ekm),
						DiscoveredBy:   scanID,
					})
					mu.Unlock()
				}
				return nil
			}); err != nil {
				if isPermissionDenied(err) {
					if !ekmDisabled.Swap(true) {
						if err := skipIfDenied(st, "cloudkms:ekmConnections.list", p.ID, err); err != nil {
							return err
						}
					}
				} else {
					return err
				}
			}
		}

		if !keyHandleDisabled.Load() {
			if err := svc.Projects.Locations.KeyHandles.List(locName).Pages(gctx, func(khPage *cloudkms.ListKeyHandlesResponse) error {
				for _, kh := range khPage.KeyHandles {
					mu.Lock()
					name := lastSegment(kh.Name)
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeKMSKeyHandle, NativeID: kh.Name, Name: &name,
						Region:         &region,
						AttributesJSON: mustJSON(kh),
						DiscoveredBy:   scanID,
					})
					mu.Unlock()
				}
				return nil
			}); err != nil {
				if isPermissionDenied(err) {
					if !keyHandleDisabled.Swap(true) {
						if err := skipIfDenied(st, "cloudkms:keyHandles.list", p.ID, err); err != nil {
							return err
						}
					}
				} else {
					return err
				}
			}
		}

		if !hsmDisabled.Load() {
			if err := svc.Projects.Locations.SingleTenantHsmInstances.List(locName).Pages(gctx, func(hsmPage *cloudkms.ListSingleTenantHsmInstancesResponse) error {
				for _, hsm := range hsmPage.SingleTenantHsmInstances {
					mu.Lock()
					name := lastSegment(hsm.Name)
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeKMSSingleTenantHsmInstance, NativeID: hsm.Name, Name: &name,
						Region:         &region,
						CreatedAt:      strp(hsm.CreateTime),
						AttributesJSON: mustJSON(hsm),
						DiscoveredBy:   scanID,
					})
					mu.Unlock()
				}
				return nil
			}); err != nil {
				if isPermissionDenied(err) {
					if !hsmDisabled.Swap(true) {
						if err := skipIfDenied(st, "cloudkms:singleTenantHsmInstances.list", p.ID, err); err != nil {
							return err
						}
					}
				} else {
					return err
				}
			}
		}

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

				if !importJobDisabled.Load() {
					if err := svc.Projects.Locations.KeyRings.ImportJobs.List(kr.Name).Pages(gctx, func(ijPage *cloudkms.ListImportJobsResponse) error {
						for _, ij := range ijPage.ImportJobs {
							mu.Lock()
							ijName := lastSegment(ij.Name)
							batch = append(batch, &store.Resource{
								Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
								Type: TypeKMSImportJob, NativeID: ij.Name, Name: &ijName,
								Region:         &region,
								CreatedAt:      strp(ij.CreateTime),
								AttributesJSON: mustJSON(ij),
								DiscoveredBy:   scanID,
							})
							mu.Unlock()
						}
						return nil
					}); err != nil {
						if isPermissionDenied(err) {
							if !importJobDisabled.Swap(true) {
								if err := skipIfDenied(st, "cloudkms:importJobs.list", p.ID, err); err != nil {
									return err
								}
							}
						} else {
							return err
						}
					}
				}

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

						if !cryptoVerDisabled.Load() {
							if err := svc.Projects.Locations.KeyRings.CryptoKeys.CryptoKeyVersions.List(ck.Name).Pages(gctx, func(cvPage *cloudkms.ListCryptoKeyVersionsResponse) error {
								for _, cv := range cvPage.CryptoKeyVersions {
									mu.Lock()
									cvName := lastSegment(cv.Name)
									batch = append(batch, &store.Resource{
										Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
										Type: TypeKMSCryptoKeyVersion, NativeID: cv.Name, Name: &cvName,
										Region:         &region,
										CreatedAt:      strp(cv.CreateTime),
										AttributesJSON: mustJSON(cv),
										DiscoveredBy:   scanID,
									})
									mu.Unlock()
								}
								return nil
							}); err != nil {
								if isPermissionDenied(err) {
									if !cryptoVerDisabled.Swap(true) {
										if err := skipIfDenied(st, "cloudkms:cryptoKeyVersions.list", p.ID, err); err != nil {
											return err
										}
									}
								} else {
									return err
								}
							}
						}
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

	// Closure: keyring/ekm-connection/key-handle/hsm-instance → project;
	// crypto-key/import-job → keyring; crypto-key-version → crypto-key.
	projParentID := store.ResourceID("gcp", p.ID, TypeProject, p.ID)
	var pairs [][2]string
	for _, r := range batch {
		id := store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID)
		switch r.Type {
		case TypeKMSKeyRing, TypeKMSEkmConnection, TypeKMSKeyHandle, TypeKMSSingleTenantHsmInstance:
			pairs = append(pairs, [2]string{id, projParentID})
		case TypeKMSCryptoKey, TypeKMSImportJob:
			// Parent keyring NativeID = strip "/cryptoKeys/{name}" or
			// "/importJobs/{name}" suffix — both live directly under a keyring.
			parentName := r.NativeID
			if i := strings.Index(parentName, "/cryptoKeys/"); i >= 0 {
				parentName = parentName[:i]
			} else if i := strings.Index(parentName, "/importJobs/"); i >= 0 {
				parentName = parentName[:i]
			}
			parentID := store.ResourceID(r.Provider, r.AccountID, TypeKMSKeyRing, parentName)
			pairs = append(pairs, [2]string{id, parentID})
		case TypeKMSCryptoKeyVersion:
			// Parent crypto-key NativeID = strip "/cryptoKeyVersions/{name}" suffix.
			parentName := r.NativeID
			if i := strings.Index(parentName, "/cryptoKeyVersions/"); i >= 0 {
				parentName = parentName[:i]
			}
			parentID := store.ResourceID(r.Provider, r.AccountID, TypeKMSCryptoKey, parentName)
			pairs = append(pairs, [2]string{id, parentID})
		}
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return len(batch), n, fmt.Errorf("closure KMS: %w", err)
	}
	return len(batch), n, nil
}
