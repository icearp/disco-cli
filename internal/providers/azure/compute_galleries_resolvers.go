package azure

import (
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveGalleryImageRelationships)
	registerResolver(resolveGalleryImageVersionRelationships)
	registerResolver(resolveGalleryApplicationRelationships)
	registerResolver(resolveGalleryApplicationVersionRelationships)
	registerResolver(resolveGalleryInVMACPRelationships)
	registerResolver(resolveGalleryInVMACPVersionRelationships)
}

// resolveGalleryImageRelationships derives the parent gallery for each gallery image
// by truncating the image's NativeID at "/images/".
// NativeID form: .../galleries/{gallery}/images/{image}
func resolveGalleryImageRelationships(sub *subscription, st *store.Store) error {
	images, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeGalleryImage},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range images {
		galleryNativeID := truncateAtSegment(r.NativeID, "/images/")
		if galleryNativeID == "" {
			continue
		}
		galleryID := store.ResourceID("azure", sub.ID, TypeComputeGallery, galleryNativeID)
		if err := st.UpsertRelationship(r.ID, galleryID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryImage→gallery relationship: %w", err)
		}
	}
	return nil
}

// resolveGalleryImageVersionRelationships derives the parent gallery image for each
// image version by truncating the version's NativeID at "/versions/".
// NativeID form: .../images/{image}/versions/{version}
func resolveGalleryImageVersionRelationships(sub *subscription, st *store.Store) error {
	versions, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeGalleryImageVersion},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range versions {
		imageNativeID := truncateAtSegment(r.NativeID, "/versions/")
		if imageNativeID == "" {
			continue
		}
		imageID := store.ResourceID("azure", sub.ID, TypeComputeGalleryImage, imageNativeID)
		if err := st.UpsertRelationship(r.ID, imageID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryImageVersion→galleryImage relationship: %w", err)
		}
	}
	return nil
}

// resolveGalleryApplicationRelationships derives the parent gallery for each gallery
// application by truncating the application's NativeID at "/applications/".
// NativeID form: .../galleries/{gallery}/applications/{app}
func resolveGalleryApplicationRelationships(sub *subscription, st *store.Store) error {
	apps, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeGalleryApplication},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range apps {
		galleryNativeID := truncateAtSegment(r.NativeID, "/applications/")
		if galleryNativeID == "" {
			continue
		}
		galleryID := store.ResourceID("azure", sub.ID, TypeComputeGallery, galleryNativeID)
		if err := st.UpsertRelationship(r.ID, galleryID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryApplication→gallery relationship: %w", err)
		}
	}
	return nil
}

// resolveGalleryApplicationVersionRelationships derives the parent gallery application
// for each application version by truncating the version's NativeID at "/versions/".
// NativeID form: .../applications/{app}/versions/{version}
func resolveGalleryApplicationVersionRelationships(sub *subscription, st *store.Store) error {
	versions, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeGalleryApplicationVersion},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range versions {
		appNativeID := truncateAtSegment(r.NativeID, "/versions/")
		if appNativeID == "" {
			continue
		}
		appID := store.ResourceID("azure", sub.ID, TypeComputeGalleryApplication, appNativeID)
		if err := st.UpsertRelationship(r.ID, appID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryApplicationVersion→galleryApplication relationship: %w", err)
		}
	}
	return nil
}

// resolveGalleryInVMACPRelationships derives the parent gallery for each
// inVMAccessControlProfile by truncating the profile's NativeID at
// "/inVMAccessControlProfiles/".
// NativeID form: .../galleries/{gallery}/inVMAccessControlProfiles/{profile}
func resolveGalleryInVMACPRelationships(sub *subscription, st *store.Store) error {
	profiles, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeGalleryInVMACP},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range profiles {
		galleryNativeID := truncateAtSegment(r.NativeID, "/inVMAccessControlProfiles/")
		if galleryNativeID == "" {
			continue
		}
		galleryID := store.ResourceID("azure", sub.ID, TypeComputeGallery, galleryNativeID)
		if err := st.UpsertRelationship(r.ID, galleryID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryInVMACP→gallery relationship: %w", err)
		}
	}
	return nil
}

// resolveGalleryInVMACPVersionRelationships derives the parent inVMAccessControlProfile
// for each profile version by truncating the version's NativeID at "/versions/".
// NativeID form: .../inVMAccessControlProfiles/{profile}/versions/{version}
func resolveGalleryInVMACPVersionRelationships(sub *subscription, st *store.Store) error {
	versions, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeGalleryInVMACPVersion},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range versions {
		profileNativeID := truncateAtSegment(r.NativeID, "/versions/")
		if profileNativeID == "" {
			continue
		}
		profileID := store.ResourceID("azure", sub.ID, TypeComputeGalleryInVMACP, profileNativeID)
		if err := st.UpsertRelationship(r.ID, profileID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryInVMACPVersion→galleryInVMACP relationship: %w", err)
		}
	}
	return nil
}
