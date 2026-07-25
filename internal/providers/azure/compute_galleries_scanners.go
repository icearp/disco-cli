package azure

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeComputeGallery, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeGalleryImage, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeGalleryImageVersion, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeGalleryApplication, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeGalleryApplicationVersion, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeGalleryInVMACP, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeGalleryInVMACPVersion, Service: "microsoft.compute"})
}

// galleryEntry holds the identifying fields of a gallery for child scans.
type galleryEntry struct {
	rg, name, nativeID, discoID string
}

// galleryChildEntry holds the identifying fields of a gallery child (image or application)
// for version scans.
type galleryChildEntry struct {
	rg, galleryName, childName, nativeID, discoID string
}

// galleryProfileEntry holds the identifying fields of an inVMAccessControlProfile for version scans.
type galleryProfileEntry struct {
	rg, galleryName, profileName, nativeID, discoID string
}

// scanGalleries discovers Azure Compute Gallery resources across five phases:
//  1. Galleries (subscription-wide list)
//  2. Gallery images, applications, and inVMAccessControlProfiles (per gallery, fanned out)
//  3. Gallery image versions, application versions, and inVMACP versions (per child, fanned out)
func scanGalleries(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase A: list all galleries in the subscription.
	galleriesClient, err := armcompute.NewGalleriesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewGalleriesClient: %w", err)
	}

	var (
		galBatch  []*store.Resource
		galPairs  [][2]string
		galleries []galleryEntry
	)
	pager := galleriesClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:Galleries.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:Galleries.List: %w", err)
		}
		for _, g := range page.Value {
			if g.ID == nil {
				continue
			}
			name := sv(g.Name)
			location := sv(g.Location)
			nativeID := sv(g.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeGallery,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(g),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(g.Tags)
			discoID := store.ResourceID("azure", sub.ID, nativeID)
			galBatch = append(galBatch, r)
			galPairs = append(galPairs, rgHierarchyPair(sub, TypeComputeGallery, nativeID))
			galleries = append(galleries, galleryEntry{
				rg:       rgNameFromID(nativeID),
				name:     name,
				nativeID: nativeID,
				discoID:  discoID,
			})
		}
	}
	if len(galBatch) > 0 {
		n, err := st.UpsertResources(galBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure galleries: %w", err)
		}
		total += len(galBatch)
		inserted += n
		if err := st.RecordHierarchyBatch(galPairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure galleries: %w", err)
		}
	}
	if len(galleries) == 0 {
		return total, inserted, nil
	}

	// Phase B: for each gallery, scan images, applications, and inVMACPs.
	var (
		bMu               sync.Mutex
		bTotal, bInserted int
		imgEntries        []galleryChildEntry
		appEntries        []galleryChildEntry
		profileEntries    []galleryProfileEntry
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	gB, gBCtx := errgroup.WithContext(ctx)
	for _, gal := range galleries {
		g := gal
		gB.Go(func() error {
			if err := sem.Acquire(gBCtx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			imgT, imgN, imgs, imgErr := scanGalleryImages(gBCtx, sub, cred, st, scanID, g)
			if imgErr != nil {
				return imgErr
			}
			appT, appN, apps, appErr := scanGalleryApplications(gBCtx, sub, cred, st, scanID, g)
			if appErr != nil {
				return appErr
			}
			profT, profN, profs, profErr := scanGalleryInVMACPs(gBCtx, sub, cred, st, scanID, g)
			if profErr != nil {
				return profErr
			}

			bMu.Lock()
			bTotal += imgT + appT + profT
			bInserted += imgN + appN + profN
			imgEntries = append(imgEntries, imgs...)
			appEntries = append(appEntries, apps...)
			profileEntries = append(profileEntries, profs...)
			bMu.Unlock()
			return nil
		})
	}
	if err := gB.Wait(); err != nil {
		return 0, 0, err
	}
	total += bTotal
	inserted += bInserted

	// Phase C: fan out version scans for images, applications, and inVMACPs in parallel.
	var (
		cMu               sync.Mutex
		cTotal, cInserted int
	)
	sem2 := semaphore.NewWeighted(maxConcurrentFanout)
	gC, gCCtx := errgroup.WithContext(ctx)

	for _, img := range imgEntries {
		e := img
		gC.Go(func() error {
			if err := sem2.Acquire(gCCtx, 1); err != nil {
				return err
			}
			defer sem2.Release(1)
			t, n, err := scanGalleryImageVersions(gCCtx, sub, cred, st, scanID, e)
			if err != nil {
				return err
			}
			cMu.Lock()
			cTotal += t
			cInserted += n
			cMu.Unlock()
			return nil
		})
	}
	for _, app := range appEntries {
		e := app
		gC.Go(func() error {
			if err := sem2.Acquire(gCCtx, 1); err != nil {
				return err
			}
			defer sem2.Release(1)
			t, n, err := scanGalleryApplicationVersions(gCCtx, sub, cred, st, scanID, e)
			if err != nil {
				return err
			}
			cMu.Lock()
			cTotal += t
			cInserted += n
			cMu.Unlock()
			return nil
		})
	}
	for _, prof := range profileEntries {
		e := prof
		gC.Go(func() error {
			if err := sem2.Acquire(gCCtx, 1); err != nil {
				return err
			}
			defer sem2.Release(1)
			t, n, err := scanGalleryInVMACPVersions(gCCtx, sub, cred, st, scanID, e)
			if err != nil {
				return err
			}
			cMu.Lock()
			cTotal += t
			cInserted += n
			cMu.Unlock()
			return nil
		})
	}
	if err := gC.Wait(); err != nil {
		return 0, 0, err
	}
	total += cTotal
	inserted += cInserted

	return total, inserted, nil
}

func scanGalleryImages(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, gal galleryEntry) (total, inserted int, entries []galleryChildEntry, err error) {
	client, err := armcompute.NewGalleryImagesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("armcompute:NewGalleryImagesClient: %w", err)
	}

	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListByGalleryPager(gal.rg, gal.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "armcompute:GalleryImages.ListByGallery", sub.ID, err)
			}
			return 0, 0, nil, fmt.Errorf("armcompute:GalleryImages.ListByGallery %s/%s: %w", gal.rg, gal.name, err)
		}
		for _, img := range page.Value {
			if img.ID == nil {
				continue
			}
			name := sv(img.Name)
			location := sv(img.Location)
			nativeID := sv(img.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeGalleryImage,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(img),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(img.Tags)
			imgDiscoID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{imgDiscoID, gal.discoID})
			entries = append(entries, galleryChildEntry{
				rg:          gal.rg,
				galleryName: gal.name,
				childName:   name,
				nativeID:    nativeID,
				discoID:     imgDiscoID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("upsert Azure gallery images %s: %w", gal.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, nil, fmt.Errorf("closure Azure gallery images %s: %w", gal.name, err)
		}
	}
	return total, inserted, entries, nil
}

func scanGalleryApplications(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, gal galleryEntry) (total, inserted int, entries []galleryChildEntry, err error) {
	client, err := armcompute.NewGalleryApplicationsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("armcompute:NewGalleryApplicationsClient: %w", err)
	}

	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListByGalleryPager(gal.rg, gal.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "armcompute:GalleryApplications.ListByGallery", sub.ID, err)
			}
			return 0, 0, nil, fmt.Errorf("armcompute:GalleryApplications.ListByGallery %s/%s: %w", gal.rg, gal.name, err)
		}
		for _, app := range page.Value {
			if app.ID == nil {
				continue
			}
			name := sv(app.Name)
			location := sv(app.Location)
			nativeID := sv(app.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeGalleryApplication,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(app),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(app.Tags)
			appDiscoID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{appDiscoID, gal.discoID})
			entries = append(entries, galleryChildEntry{
				rg:          gal.rg,
				galleryName: gal.name,
				childName:   name,
				nativeID:    nativeID,
				discoID:     appDiscoID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("upsert Azure gallery applications %s: %w", gal.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, nil, fmt.Errorf("closure Azure gallery applications %s: %w", gal.name, err)
		}
	}
	return total, inserted, entries, nil
}

func scanGalleryInVMACPs(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, gal galleryEntry) (total, inserted int, entries []galleryProfileEntry, err error) {
	client, err := armcompute.NewGalleryInVMAccessControlProfilesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("armcompute:NewGalleryInVMAccessControlProfilesClient: %w", err)
	}

	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListByGalleryPager(gal.rg, gal.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "armcompute:GalleryInVMACPs.ListByGallery", sub.ID, err)
			}
			return 0, 0, nil, fmt.Errorf("armcompute:GalleryInVMACPs.ListByGallery %s/%s: %w", gal.rg, gal.name, err)
		}
		for _, prof := range page.Value {
			if prof.ID == nil {
				continue
			}
			name := sv(prof.Name)
			location := sv(prof.Location)
			nativeID := sv(prof.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeGalleryInVMACP,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(prof),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(prof.Tags)
			profDiscoID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{profDiscoID, gal.discoID})
			entries = append(entries, galleryProfileEntry{
				rg:          gal.rg,
				galleryName: gal.name,
				profileName: name,
				nativeID:    nativeID,
				discoID:     profDiscoID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("upsert Azure gallery inVMACPs %s: %w", gal.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, nil, fmt.Errorf("closure Azure gallery inVMACPs %s: %w", gal.name, err)
		}
	}
	return total, inserted, entries, nil
}

func scanGalleryImageVersions(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, img galleryChildEntry) (total, inserted int, err error) {
	client, err := armcompute.NewGalleryImageVersionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewGalleryImageVersionsClient: %w", err)
	}

	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListByGalleryImagePager(img.rg, img.galleryName, img.childName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:GalleryImageVersions.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:GalleryImageVersions.List %s/%s/%s: %w", img.rg, img.galleryName, img.childName, err)
		}
		for _, v := range page.Value {
			if v.ID == nil {
				continue
			}
			name := sv(v.Name)
			location := sv(v.Location)
			nativeID := sv(v.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeGalleryImageVersion,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(v),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(v.Tags)
			vID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{vID, img.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure gallery image versions %s: %w", img.childName, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure gallery image versions %s: %w", img.childName, err)
		}
	}
	return total, inserted, nil
}

func scanGalleryApplicationVersions(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, app galleryChildEntry) (total, inserted int, err error) {
	client, err := armcompute.NewGalleryApplicationVersionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewGalleryApplicationVersionsClient: %w", err)
	}

	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListByGalleryApplicationPager(app.rg, app.galleryName, app.childName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:GalleryApplicationVersions.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:GalleryApplicationVersions.List %s/%s/%s: %w", app.rg, app.galleryName, app.childName, err)
		}
		for _, v := range page.Value {
			if v.ID == nil {
				continue
			}
			name := sv(v.Name)
			location := sv(v.Location)
			nativeID := sv(v.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeGalleryApplicationVersion,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(v),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(v.Tags)
			vID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{vID, app.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure gallery application versions %s: %w", app.childName, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure gallery application versions %s: %w", app.childName, err)
		}
	}
	return total, inserted, nil
}

func scanGalleryInVMACPVersions(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, prof galleryProfileEntry) (total, inserted int, err error) {
	client, err := armcompute.NewGalleryInVMAccessControlProfileVersionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewGalleryInVMAccessControlProfileVersionsClient: %w", err)
	}

	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListByGalleryInVMAccessControlProfilePager(prof.rg, prof.galleryName, prof.profileName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:GalleryInVMACPVersions.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:GalleryInVMACPVersions.List %s/%s/%s: %w", prof.rg, prof.galleryName, prof.profileName, err)
		}
		for _, v := range page.Value {
			if v.ID == nil {
				continue
			}
			name := sv(v.Name)
			location := sv(v.Location)
			nativeID := sv(v.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeGalleryInVMACPVersion,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(v),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(v.Tags)
			vID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{vID, prof.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure gallery inVMACP versions %s: %w", prof.profileName, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure gallery inVMACP versions %s: %w", prof.profileName, err)
		}
	}
	return total, inserted, nil
}
