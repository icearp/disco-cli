package azure

import (
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveGalleryImageRelationships,
		EdgeDecl{Source: TypeComputeGalleryImage, Target: TypeComputeGallery, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveGalleryImageVersionRelationships,
		EdgeDecl{Source: TypeComputeGalleryImageVersion, Target: TypeComputeGalleryImage, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveGalleryApplicationRelationships,
		EdgeDecl{Source: TypeComputeGalleryApplication, Target: TypeComputeGallery, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveGalleryApplicationVersionRelationships,
		EdgeDecl{Source: TypeComputeGalleryApplicationVersion, Target: TypeComputeGalleryApplication, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveGalleryInVMACPRelationships,
		EdgeDecl{Source: TypeComputeGalleryInVMACP, Target: TypeComputeGallery, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveGalleryInVMACPVersionRelationships,
		EdgeDecl{Source: TypeComputeGalleryInVMACPVersion, Target: TypeComputeGalleryInVMACP, Kind: store.RelAttachedTo},
	)
}

// resolveGalleryImageRelationships derives each image's parent gallery by truncating
// NativeID at "/images/". NativeID form: .../galleries/{gallery}/images/{image}
func resolveGalleryImageRelationships(sub *subscription, st *store.Store) error {
	images, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
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
		galleryID := store.ResourceID("azure", sub.ID, galleryNativeID)
		if err := st.UpsertRelationship(r.ID, galleryID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryImage→gallery relationship: %w", err)
		}
	}
	return nil
}

// resolveGalleryImageVersionRelationships derives each image version's parent image
// by truncating NativeID at "/versions/". NativeID form: .../images/{image}/versions/{version}
func resolveGalleryImageVersionRelationships(sub *subscription, st *store.Store) error {
	versions, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
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
		imageID := store.ResourceID("azure", sub.ID, imageNativeID)
		if err := st.UpsertRelationship(r.ID, imageID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryImageVersion→galleryImage relationship: %w", err)
		}
	}
	return nil
}

// resolveGalleryApplicationRelationships derives each application's parent gallery by
// truncating NativeID at "/applications/". NativeID form: .../galleries/{gallery}/applications/{app}
func resolveGalleryApplicationRelationships(sub *subscription, st *store.Store) error {
	apps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
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
		galleryID := store.ResourceID("azure", sub.ID, galleryNativeID)
		if err := st.UpsertRelationship(r.ID, galleryID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryApplication→gallery relationship: %w", err)
		}
	}
	return nil
}

// resolveGalleryApplicationVersionRelationships derives each application version's parent
// application by truncating NativeID at "/versions/". NativeID form: .../applications/{app}/versions/{version}
func resolveGalleryApplicationVersionRelationships(sub *subscription, st *store.Store) error {
	versions, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
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
		appID := store.ResourceID("azure", sub.ID, appNativeID)
		if err := st.UpsertRelationship(r.ID, appID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryApplicationVersion→galleryApplication relationship: %w", err)
		}
	}
	return nil
}

// resolveGalleryInVMACPRelationships derives each inVMAccessControlProfile's parent gallery
// by truncating NativeID at "/inVMAccessControlProfiles/".
// NativeID form: .../galleries/{gallery}/inVMAccessControlProfiles/{profile}
func resolveGalleryInVMACPRelationships(sub *subscription, st *store.Store) error {
	profiles, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
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
		galleryID := store.ResourceID("azure", sub.ID, galleryNativeID)
		if err := st.UpsertRelationship(r.ID, galleryID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryInVMACP→gallery relationship: %w", err)
		}
	}
	return nil
}

// resolveGalleryInVMACPVersionRelationships derives each profile version's parent
// inVMAccessControlProfile by truncating NativeID at "/versions/".
// NativeID form: .../inVMAccessControlProfiles/{profile}/versions/{version}
func resolveGalleryInVMACPVersionRelationships(sub *subscription, st *store.Store) error {
	versions, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
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
		profileID := store.ResourceID("azure", sub.ID, profileNativeID)
		if err := st.UpsertRelationship(r.ID, profileID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert galleryInVMACPVersion→galleryInVMACP relationship: %w", err)
		}
	}
	return nil
}
