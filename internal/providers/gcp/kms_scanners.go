package gcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
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

// kmsScan holds the shared state threaded through a single Cloud KMS scan:
// the SDK client, store, project, scan ID, the mutex-guarded resource batch,
// and the per-list-call "disabled" latches. The latches are atomic.Bool so
// the bounded per-location fan-out (forEachItem) can trip them once and skip
// siblings without a data race. Methods take a pointer receiver so the
// atomics are never copied. Not safe for concurrent use across scans — one
// kmsScan value is scoped to one scanCloudKMSWithClient call.
type kmsScan struct {
	svc    *cloudkms.Service
	st     *store.Store
	p      *project
	scanID string

	mu    sync.Mutex
	batch []*store.Resource

	// Each sibling List call gets its own latch since a caller can lack one
	// permission (e.g. cloudkms.ekmConnections.list) while still holding the
	// others — one shared flag would either mute a real warning or spam the
	// same warning per location/keyring/crypto-key.
	apiDisabled       atomic.Bool
	ekmDisabled       atomic.Bool
	keyHandleDisabled atomic.Bool
	hsmDisabled       atomic.Bool
	importJobDisabled atomic.Bool
	cryptoVerDisabled atomic.Bool
}

// guarded runs a leaf list call under its disabled latch: it skips entirely
// when the latch is already set, trips the latch once on the first
// permission-denied (recording the skip warning via skipIfDenied) so siblings
// continue, and propagates any other error. It returns nil on success or on a
// skipped/latched permission denial. Only safe for LEAF list calls whose
// error return does not need to abort an enclosing loop early — callers that
// must stop sibling processing on denial keep their List call inline.
func (s *kmsScan) guarded(flag *atomic.Bool, op string, list func() error) error {
	if flag.Load() {
		return nil
	}
	if err := list(); err != nil {
		if isPermissionDenied(err) {
			if !flag.Swap(true) {
				return skipIfDenied(s.st, op, s.p.ID, err)
			}
			return nil
		}
		return err
	}
	return nil
}

func (s *kmsScan) ekmConnections(ctx context.Context, locName, region string) error {
	return s.guarded(&s.ekmDisabled, "cloudkms:ekmConnections.list", func() error {
		return s.svc.Projects.Locations.EkmConnections.List(locName).Pages(ctx, func(ekmPage *cloudkms.ListEkmConnectionsResponse) error {
			for _, ekm := range ekmPage.EkmConnections {
				s.mu.Lock()
				name := lastSegment(ekm.Name)
				s.batch = append(s.batch, &store.Resource{
					Provider: "gcp", AccountID: s.p.ID, AccountName: &s.p.Name,
					Type: TypeKMSEkmConnection, NativeID: ekm.Name, Name: &name,
					Region:         &region,
					CreatedAt:      strp(ekm.CreateTime),
					AttributesJSON: mustJSON(ekm),
					DiscoveredBy:   s.scanID,
				})
				s.mu.Unlock()
			}
			return nil
		})
	})
}

func (s *kmsScan) keyHandles(ctx context.Context, locName, region string) error {
	return s.guarded(&s.keyHandleDisabled, "cloudkms:keyHandles.list", func() error {
		return s.svc.Projects.Locations.KeyHandles.List(locName).Pages(ctx, func(khPage *cloudkms.ListKeyHandlesResponse) error {
			for _, kh := range khPage.KeyHandles {
				s.mu.Lock()
				name := lastSegment(kh.Name)
				s.batch = append(s.batch, &store.Resource{
					Provider: "gcp", AccountID: s.p.ID, AccountName: &s.p.Name,
					Type: TypeKMSKeyHandle, NativeID: kh.Name, Name: &name,
					Region:         &region,
					AttributesJSON: mustJSON(kh),
					DiscoveredBy:   s.scanID,
				})
				s.mu.Unlock()
			}
			return nil
		})
	})
}

func (s *kmsScan) singleTenantHsmInstances(ctx context.Context, locName, region string) error {
	return s.guarded(&s.hsmDisabled, "cloudkms:singleTenantHsmInstances.list", func() error {
		return s.svc.Projects.Locations.SingleTenantHsmInstances.List(locName).Pages(ctx, func(hsmPage *cloudkms.ListSingleTenantHsmInstancesResponse) error {
			for _, hsm := range hsmPage.SingleTenantHsmInstances {
				s.mu.Lock()
				name := lastSegment(hsm.Name)
				s.batch = append(s.batch, &store.Resource{
					Provider: "gcp", AccountID: s.p.ID, AccountName: &s.p.Name,
					Type: TypeKMSSingleTenantHsmInstance, NativeID: hsm.Name, Name: &name,
					Region:         &region,
					CreatedAt:      strp(hsm.CreateTime),
					AttributesJSON: mustJSON(hsm),
					DiscoveredBy:   s.scanID,
				})
				s.mu.Unlock()
			}
			return nil
		})
	})
}

func (s *kmsScan) importJobs(ctx context.Context, keyRingName, region string) error {
	return s.guarded(&s.importJobDisabled, "cloudkms:importJobs.list", func() error {
		return s.svc.Projects.Locations.KeyRings.ImportJobs.List(keyRingName).Pages(ctx, func(ijPage *cloudkms.ListImportJobsResponse) error {
			for _, ij := range ijPage.ImportJobs {
				s.mu.Lock()
				ijName := lastSegment(ij.Name)
				s.batch = append(s.batch, &store.Resource{
					Provider: "gcp", AccountID: s.p.ID, AccountName: &s.p.Name,
					Type: TypeKMSImportJob, NativeID: ij.Name, Name: &ijName,
					Region:         &region,
					CreatedAt:      strp(ij.CreateTime),
					AttributesJSON: mustJSON(ij),
					DiscoveredBy:   s.scanID,
				})
				s.mu.Unlock()
			}
			return nil
		})
	})
}

func (s *kmsScan) cryptoKeyVersions(ctx context.Context, cryptoKeyName, region string) error {
	return s.guarded(&s.cryptoVerDisabled, "cloudkms:cryptoKeyVersions.list", func() error {
		return s.svc.Projects.Locations.KeyRings.CryptoKeys.CryptoKeyVersions.List(cryptoKeyName).Pages(ctx, func(cvPage *cloudkms.ListCryptoKeyVersionsResponse) error {
			for _, cv := range cvPage.CryptoKeyVersions {
				s.mu.Lock()
				cvName := lastSegment(cv.Name)
				s.batch = append(s.batch, &store.Resource{
					Provider: "gcp", AccountID: s.p.ID, AccountName: &s.p.Name,
					Type: TypeKMSCryptoKeyVersion, NativeID: cv.Name, Name: &cvName,
					Region:         &region,
					CreatedAt:      strp(cv.CreateTime),
					AttributesJSON: mustJSON(cv),
					DiscoveredBy:   s.scanID,
				})
				s.mu.Unlock()
			}
			return nil
		})
	})
}

// keyRings lists keyrings for a location and, per keyring, its import jobs and
// crypto keys (and per crypto key its versions). The keyRings-list +
// cryptoKeys-list nesting stays inline here — NOT extracted into a guarded
// leaf — because a cryptoKeys permission denial does `return skipIfDenied(...)`
// from the keyrings page callback, which exits the keyrings loop and skips the
// remaining keyrings on that page. Routing that through guarded (which returns
// nil to continue) would silently process the remaining keyrings.
func (s *kmsScan) keyRings(ctx context.Context, locName, region string) error {
	return s.guarded(&s.apiDisabled, "cloudkms:keyRings.list", func() error {
		return s.svc.Projects.Locations.KeyRings.List(locName).Pages(ctx, func(krPage *cloudkms.ListKeyRingsResponse) error {
			for _, kr := range krPage.KeyRings {
				s.mu.Lock()
				name := lastSegment(kr.Name)
				s.batch = append(s.batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      s.p.ID,
					AccountName:    &s.p.Name,
					Type:           TypeKMSKeyRing,
					NativeID:       kr.Name,
					Name:           &name,
					Region:         &region,
					CreatedAt:      strp(kr.CreateTime),
					AttributesJSON: mustJSON(kr),
					DiscoveredBy:   s.scanID,
				})
				s.mu.Unlock()

				if err := s.importJobs(ctx, kr.Name, region); err != nil {
					return err
				}

				if err := s.svc.Projects.Locations.KeyRings.CryptoKeys.List(kr.Name).Pages(ctx, func(ckPage *cloudkms.ListCryptoKeysResponse) error {
					for _, ck := range ckPage.CryptoKeys {
						s.mu.Lock()
						ckName := lastSegment(ck.Name)
						s.batch = append(s.batch, &store.Resource{
							Provider:       "gcp",
							AccountID:      s.p.ID,
							AccountName:    &s.p.Name,
							Type:           TypeKMSCryptoKey,
							NativeID:       ck.Name,
							Name:           &ckName,
							Region:         &region,
							CreatedAt:      strp(ck.CreateTime),
							AttributesJSON: mustJSON(ck),
							DiscoveredBy:   s.scanID,
						})
						s.mu.Unlock()

						if err := s.cryptoKeyVersions(ctx, ck.Name, region); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					if isPermissionDenied(err) {
						return skipIfDenied(s.st, "cloudkms:cryptoKeys.list", s.p.ID, err)
					}
					return err
				}
			}
			return nil
		})
	})
}

// scanLocation runs the per-location sub-scans. apiDisabled (tripped by a
// keyRings.list denial) gates the entire location — including the ekm/
// keyHandle/hsm sibling lists — so once one location's keyRings is denied the
// remaining locations skip entirely, matching the original single top-of-loop
// check.
func (s *kmsScan) scanLocation(ctx context.Context, locName string) error {
	if s.apiDisabled.Load() {
		return nil
	}
	region := lastSegment(locName) // "projects/{p}/locations/us-central1" → "us-central1"
	if err := s.ekmConnections(ctx, locName, region); err != nil {
		return err
	}
	if err := s.keyHandles(ctx, locName, region); err != nil {
		return err
	}
	if err := s.singleTenantHsmInstances(ctx, locName, region); err != nil {
		return err
	}
	return s.keyRings(ctx, locName, region)
}

// recordKMSHierarchy builds and records the parent/child closure for the
// scanned batch: keyring/ekm-connection/key-handle/hsm-instance → project;
// crypto-key/import-job → keyring; crypto-key-version → crypto-key. n is the
// UpsertResources inserted count, threaded through to the (total, inserted)
// return.
func (s *kmsScan) recordKMSHierarchy(n int) (total, inserted int, err error) {
	projParentID := store.ResourceID("gcp", s.p.ID, s.p.ID)
	var pairs [][2]string
	for _, r := range s.batch {
		id := store.ResourceID(r.Provider, r.AccountID, r.NativeID)
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
			parentID := store.ResourceID(r.Provider, r.AccountID, parentName)
			pairs = append(pairs, [2]string{id, parentID})
		case TypeKMSCryptoKeyVersion:
			// Parent crypto-key NativeID = strip "/cryptoKeyVersions/{name}" suffix.
			parentName := r.NativeID
			if i := strings.Index(parentName, "/cryptoKeyVersions/"); i >= 0 {
				parentName = parentName[:i]
			}
			parentID := store.ResourceID(r.Provider, r.AccountID, parentName)
			pairs = append(pairs, [2]string{id, parentID})
		}
	}
	if err := s.st.RecordHierarchyBatch(pairs); err != nil {
		return len(s.batch), n, fmt.Errorf("closure KMS: %w", err)
	}
	return len(s.batch), n, nil
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
	s := &kmsScan{svc: svc, st: st, p: p, scanID: scanID}
	if err := forEachItem(ctx, maxConcurrentKMSLocations, locations, s.scanLocation); err != nil {
		return 0, 0, err
	}
	if len(s.batch) == 0 {
		return 0, 0, nil
	}
	n, e := st.UpsertResources(s.batch)
	if e != nil {
		return 0, 0, fmt.Errorf("upsert KMS resources: %w", e)
	}
	return s.recordKMSHierarchy(n)
}
