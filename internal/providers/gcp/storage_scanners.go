package gcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/storage/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeStorageBucket, Service: "storage", Upstream: "storage.googleapis.com/Bucket"})
	registerType(restype.Descriptor{Type: TypeStorageHmacKey, Service: "storage", Upstream: "storage.googleapis.com/HmacKey"})
	registerType(restype.Descriptor{Type: TypeStorageNotification, Service: "storage", Upstream: "storage.googleapis.com/Notification"})
	registerType(restype.Descriptor{Type: TypeStorageManagedFolder, Service: "storage", Upstream: "storage.googleapis.com/ManagedFolder", Leaf: true})
	registerType(restype.Descriptor{Type: TypeStorageAnywhereCache, Service: "storage", Upstream: "storage.googleapis.com/AnywhereCache", Leaf: true})
	registerType(restype.Descriptor{Type: TypeStorageFolder, Service: "storage", Upstream: "storage.googleapis.com/Folder", Leaf: true})
	registerType(restype.Descriptor{Type: TypeStorageBucketAccessControl, Service: "storage", Upstream: "storage.googleapis.com/BucketAccessControl"})
	registerType(restype.Descriptor{Type: TypeStorageDefaultObjectAccessControl, Service: "storage", Upstream: "storage.googleapis.com/DefaultObjectAccessControl"})
	registerService(serviceEntry{
		name: "gcp:storage",
		fn:   scanStorage,
	})
}

// maxConcurrentStorageBuckets caps per-project bucket fan-out for the
// per-bucket Notifications/ManagedFolders/AnywhereCaches/Folders/
// BucketAccessControls/DefaultObjectAccessControls enrichment phase.
const maxConcurrentStorageBuckets = 10

// scanStorage discovers Cloud Storage buckets plus per-project HMAC keys and
// per-bucket secondary resources.
func scanStorage(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := storage.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("storage client: %w", err)
	}
	return scanStorageWithClient(ctx, svc, p, st, scanID)
}

// scanStorageWithClient is the test seam for scanStorage — takes the
// pre-built client directly so tests can point it at a fake server.
func scanStorageWithClient(ctx context.Context, svc *storage.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase 1: buckets — capture (name, resourceID) pairs for the per-bucket
	// fan-out below; resourceID uses the same NativeID (SelfLink) as the
	// upserted row so upsertWithParent's closure lookup finds it.
	type bucketRef struct {
		name string
		id   string
	}
	var bucketRefs []bucketRef
	t, n, err := runPaginated(ctx, st, p, "storage:buckets.list",
		svc.Buckets.List(p.ID),
		func(page *storage.Buckets) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, b := range page.Items {
				if b == nil || b.Name == "" || b.SelfLink == "" {
					continue
				}
				bucketRefs = append(bucketRefs, bucketRef{
					name: b.Name,
					id:   store.ResourceID("gcp", p.ID, b.SelfLink),
				})
				name := b.Name
				region := b.Location
				r := &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeStorageBucket,
					NativeID:       b.SelfLink,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(b),
					DiscoveredBy:   scanID,
				}
				if len(b.Labels) > 0 {
					s := mustJSON(b.Labels)
					r.TagsJSON = &s
				}
				batch = append(batch, r)
			}
			if len(batch) == 0 {
				return 0, 0, nil
			}
			nn, e := st.UpsertResources(batch)
			if e != nil {
				return 0, 0, fmt.Errorf("upsert GCS buckets: %w", e)
			}
			return len(batch), nn, nil
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 2: HMAC keys — per-project, independent of any bucket.
	t, n, err = runPaginated(ctx, st, p, "storage:hmacKeys.list",
		svc.Projects.HmacKeys.List(p.ID),
		func(page *storage.HmacKeysMetadata) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, k := range page.Items {
				if k == nil || k.Id == "" {
					continue
				}
				name := k.AccessId
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeStorageHmacKey,
					NativeID:       k.Id,
					Name:           &name,
					Status:         strp(k.State),
					CreatedAt:      strp(k.TimeCreated),
					AttributesJSON: mustJSON(k),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 3: per-bucket secondary resources.
	var mu sync.Mutex
	err = forEachItem(ctx, maxConcurrentStorageBuckets, bucketRefs, func(gctx context.Context, ref bucketRef) error {
		bucket := ref.name
		bucketResID := ref.id

		if t, n, e := scanBucketNotifications(gctx, svc, st, p, scanID, bucket, bucketResID); e != nil {
			return e
		} else {
			mu.Lock()
			total += t
			inserted += n
			mu.Unlock()
		}

		if t, n, e := scanBucketManagedFolders(gctx, svc, st, p, scanID, bucket, bucketResID); e != nil {
			return e
		} else {
			mu.Lock()
			total += t
			inserted += n
			mu.Unlock()
		}

		if t, n, e := scanBucketAnywhereCaches(gctx, svc, st, p, scanID, bucket, bucketResID); e != nil {
			return e
		} else {
			mu.Lock()
			total += t
			inserted += n
			mu.Unlock()
		}

		if t, n, e := scanBucketFolders(gctx, svc, st, p, scanID, bucket, bucketResID); e != nil {
			return e
		} else {
			mu.Lock()
			total += t
			inserted += n
			mu.Unlock()
		}

		if t, n, e := scanBucketAccessControls(gctx, svc, st, p, scanID, bucket, bucketResID); e != nil {
			return e
		} else {
			mu.Lock()
			total += t
			inserted += n
			mu.Unlock()
		}

		if t, n, e := scanDefaultObjectAccessControls(gctx, svc, st, p, scanID, bucket, bucketResID); e != nil {
			return e
		} else {
			mu.Lock()
			total += t
			inserted += n
			mu.Unlock()
		}

		return nil
	})
	if err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanBucketNotifications lists Pub/Sub notification configs on bucket. The
// SDK exposes only a single Do() call (Notifications response carries no
// NextPageToken — bucket notification counts are small, capped well below
// one page by GCS itself).
func scanBucketNotifications(ctx context.Context, svc *storage.Service, st *store.Store, p *project, scanID, bucket, bucketResID string) (int, int, error) {
	resp, err := svc.Notifications.List(bucket).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "storage:notifications.list", p.ID, err)
		}
		return 0, 0, err
	}
	batch := make([]*store.Resource, 0, len(resp.Items))
	for _, notif := range resp.Items {
		if notif == nil || notif.Id == "" {
			continue
		}
		nativeID := fmt.Sprintf("%s/notificationConfigs/%s", bucket, notif.Id)
		batch = append(batch, &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           TypeStorageNotification,
			NativeID:       nativeID,
			Name:           &notif.Id,
			AttributesJSON: mustJSON(notif),
			DiscoveredBy:   scanID,
		})
	}
	return upsertWithParent(st, batch, bucketResID)
}

// scanBucketManagedFolders lists managed folders on bucket. ManagedFolders is
// an opt-in bucket feature (hierarchical namespace) — a bucket that hasn't
// enabled it returns a 400 rather than an empty list, per the SDK's own doc
// on the sibling Folders endpoint ("only applicable to buckets with
// hierarchical namespace enabled"). Treat any 4xx here as non-fatal: most
// buckets in a real project won't have the feature on, and this must not
// abort the whole storage scan over a single bucket's shape.
func scanBucketManagedFolders(ctx context.Context, svc *storage.Service, st *store.Store, p *project, scanID, bucket, bucketResID string) (total, inserted int, err error) {
	err = svc.ManagedFolders.List(bucket).Pages(ctx, func(page *storage.ManagedFolders) error {
		batch := make([]*store.Resource, 0, len(page.Items))
		for _, mf := range page.Items {
			if mf == nil || mf.Id == "" {
				continue
			}
			name := mf.Name
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeStorageManagedFolder,
				NativeID:       mf.Id,
				Name:           &name,
				CreatedAt:      strp(mf.CreateTime),
				AttributesJSON: mustJSON(mf),
				DiscoveredBy:   scanID,
			})
		}
		t, n, e := upsertWithParent(st, batch, bucketResID)
		total += t
		inserted += n
		return e
	})
	if err != nil {
		if isPermissionDenied(err) || isBucketFeatureNotApplicable(err) {
			return total, inserted, skipIfDenied(st, "storage:managedFolders.list", p.ID, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanBucketAnywhereCaches lists Anywhere Cache instances on bucket — same
// opt-in-feature tolerance as scanBucketManagedFolders.
func scanBucketAnywhereCaches(ctx context.Context, svc *storage.Service, st *store.Store, p *project, scanID, bucket, bucketResID string) (total, inserted int, err error) {
	err = svc.AnywhereCaches.List(bucket).Pages(ctx, func(page *storage.AnywhereCaches) error {
		batch := make([]*store.Resource, 0, len(page.Items))
		for _, ac := range page.Items {
			if ac == nil || ac.Id == "" {
				continue
			}
			name := ac.AnywhereCacheId
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeStorageAnywhereCache,
				NativeID:       ac.Id,
				Name:           &name,
				Status:         strp(ac.State),
				CreatedAt:      strp(ac.CreateTime),
				AttributesJSON: mustJSON(ac),
				DiscoveredBy:   scanID,
			})
		}
		t, n, e := upsertWithParent(st, batch, bucketResID)
		total += t
		inserted += n
		return e
	})
	if err != nil {
		if isPermissionDenied(err) || isBucketFeatureNotApplicable(err) {
			return total, inserted, skipIfDenied(st, "storage:anywhereCaches.list", p.ID, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanBucketFolders lists hierarchical-namespace folders on bucket — same
// opt-in-feature tolerance as scanBucketManagedFolders.
func scanBucketFolders(ctx context.Context, svc *storage.Service, st *store.Store, p *project, scanID, bucket, bucketResID string) (total, inserted int, err error) {
	err = svc.Folders.List(bucket).Pages(ctx, func(page *storage.Folders) error {
		batch := make([]*store.Resource, 0, len(page.Items))
		for _, f := range page.Items {
			if f == nil || f.Id == "" {
				continue
			}
			name := f.Name
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeStorageFolder,
				NativeID:       f.Id,
				Name:           &name,
				CreatedAt:      strp(f.CreateTime),
				AttributesJSON: mustJSON(f),
				DiscoveredBy:   scanID,
			})
		}
		t, n, e := upsertWithParent(st, batch, bucketResID)
		total += t
		inserted += n
		return e
	})
	if err != nil {
		if isPermissionDenied(err) || isBucketFeatureNotApplicable(err) {
			return total, inserted, skipIfDenied(st, "storage:folders.list", p.ID, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanBucketAccessControls lists bucket-level ACL entries. Single Do() call —
// BucketAccessControls carries no NextPageToken (ACL lists are small,
// bounded by the small set of legacy ACL grantees GCS allows per bucket).
func scanBucketAccessControls(ctx context.Context, svc *storage.Service, st *store.Store, p *project, scanID, bucket, bucketResID string) (int, int, error) {
	resp, err := svc.BucketAccessControls.List(bucket).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "storage:bucketAccessControls.list", p.ID, err)
		}
		return 0, 0, err
	}
	batch := make([]*store.Resource, 0, len(resp.Items))
	for _, ac := range resp.Items {
		if ac == nil || ac.SelfLink == "" {
			continue
		}
		batch = append(batch, &store.Resource{
			Provider:    "gcp",
			AccountID:   p.ID,
			AccountName: &p.Name,
			Type:        TypeStorageBucketAccessControl,
			// SelfLink (not Id): GCS returns the same Id ({bucket}/{entity}) for both
			// bucket and default-object ACLs; only SelfLink differs by collection.
			NativeID:       ac.SelfLink,
			Name:           &ac.Entity,
			AttributesJSON: mustJSON(ac),
			DiscoveredBy:   scanID,
		})
	}
	return upsertWithParent(st, batch, bucketResID)
}

// scanDefaultObjectAccessControls lists the bucket's default object-ACL
// template applied to newly written objects. Single Do() call — same
// small-and-bounded rationale as scanBucketAccessControls.
func scanDefaultObjectAccessControls(ctx context.Context, svc *storage.Service, st *store.Store, p *project, scanID, bucket, bucketResID string) (int, int, error) {
	resp, err := svc.DefaultObjectAccessControls.List(bucket).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "storage:defaultObjectAccessControls.list", p.ID, err)
		}
		return 0, 0, err
	}
	batch := make([]*store.Resource, 0, len(resp.Items))
	for _, ac := range resp.Items {
		if ac == nil || ac.SelfLink == "" {
			continue
		}
		batch = append(batch, &store.Resource{
			Provider:    "gcp",
			AccountID:   p.ID,
			AccountName: &p.Name,
			Type:        TypeStorageDefaultObjectAccessControl,
			// SelfLink (not Id): shares Id namespace with bucket ACLs — see
			// scanBucketAccessControls.
			NativeID:       ac.SelfLink,
			Name:           &ac.Entity,
			AttributesJSON: mustJSON(ac),
			DiscoveredBy:   scanID,
		})
	}
	return upsertWithParent(st, batch, bucketResID)
}

// isBucketFeatureNotApplicable reports whether err is a plain 400 Bad Request
// unrelated to BigQuery's "has not enabled" shape (already covered by
// isPermissionDenied) — the shape GCS returns when a bucket lacks an opt-in
// feature (hierarchical namespace, Anywhere Cache) the called endpoint
// requires. Narrow to storage's ManagedFolders/AnywhereCaches/Folders call
// sites; not a general-purpose predicate.
func isBucketFeatureNotApplicable(err error) bool {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	return gerr.Code == http.StatusBadRequest
}
