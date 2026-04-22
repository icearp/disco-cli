package azure

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const galSubID = "sub-gal-test"

func TestResolveGalleryImageRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)

	galNativeID := "/subscriptions/sub-gal-test/resourceGroups/rg1/providers/Microsoft.Compute/galleries/my-gal"
	imgNativeID := galNativeID + "/images/my-img"

	imgID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGalleryImage, imgNativeID, "eastus", "{}")
	galID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGallery, galNativeID, "eastus", "{}")

	if err := resolveGalleryImageRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryImageRelationships: %v", err)
	}
	assertRelationship(t, st, imgID, galID)
}

func TestResolveGalleryImageRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)
	if err := resolveGalleryImageRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryImageRelationships (empty): %v", err)
	}
}

func TestResolveGalleryImageVersionRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)

	imgNativeID := "/subscriptions/sub-gal-test/resourceGroups/rg1/providers/Microsoft.Compute/galleries/my-gal/images/my-img"
	verNativeID := imgNativeID + "/versions/1.0.0"

	verID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGalleryImageVersion, verNativeID, "eastus", "{}")
	imgID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGalleryImage, imgNativeID, "eastus", "{}")

	if err := resolveGalleryImageVersionRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryImageVersionRelationships: %v", err)
	}
	assertRelationship(t, st, verID, imgID)
}

func TestResolveGalleryImageVersionRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)
	if err := resolveGalleryImageVersionRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryImageVersionRelationships (empty): %v", err)
	}
}

func TestResolveGalleryApplicationRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)

	galNativeID := "/subscriptions/sub-gal-test/resourceGroups/rg1/providers/Microsoft.Compute/galleries/my-gal"
	appNativeID := galNativeID + "/applications/my-app"

	appID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGalleryApplication, appNativeID, "eastus", "{}")
	galID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGallery, galNativeID, "eastus", "{}")

	if err := resolveGalleryApplicationRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryApplicationRelationships: %v", err)
	}
	assertRelationship(t, st, appID, galID)
}

func TestResolveGalleryApplicationRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)
	if err := resolveGalleryApplicationRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryApplicationRelationships (empty): %v", err)
	}
}

func TestResolveGalleryApplicationVersionRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)

	appNativeID := "/subscriptions/sub-gal-test/resourceGroups/rg1/providers/Microsoft.Compute/galleries/my-gal/applications/my-app"
	verNativeID := appNativeID + "/versions/1.0.0"

	verID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGalleryApplicationVersion, verNativeID, "eastus", "{}")
	appID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGalleryApplication, appNativeID, "eastus", "{}")

	if err := resolveGalleryApplicationVersionRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryApplicationVersionRelationships: %v", err)
	}
	assertRelationship(t, st, verID, appID)
}

func TestResolveGalleryApplicationVersionRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)
	if err := resolveGalleryApplicationVersionRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryApplicationVersionRelationships (empty): %v", err)
	}
}

func TestResolveGalleryInVMACPRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)

	galNativeID := "/subscriptions/sub-gal-test/resourceGroups/rg1/providers/Microsoft.Compute/galleries/my-gal"
	profNativeID := galNativeID + "/inVMAccessControlProfiles/my-profile"

	profID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGalleryInVMACP, profNativeID, "eastus", "{}")
	galID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGallery, galNativeID, "eastus", "{}")

	if err := resolveGalleryInVMACPRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryInVMACPRelationships: %v", err)
	}
	assertRelationship(t, st, profID, galID)
}

func TestResolveGalleryInVMACPRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)
	if err := resolveGalleryInVMACPRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryInVMACPRelationships (empty): %v", err)
	}
}

func TestResolveGalleryInVMACPVersionRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)

	profNativeID := "/subscriptions/sub-gal-test/resourceGroups/rg1/providers/Microsoft.Compute/galleries/my-gal/inVMAccessControlProfiles/my-profile"
	verNativeID := profNativeID + "/versions/1.0.0"

	verID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGalleryInVMACPVersion, verNativeID, "eastus", "{}")
	profID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeGalleryInVMACP, profNativeID, "eastus", "{}")

	if err := resolveGalleryInVMACPVersionRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryInVMACPVersionRelationships: %v", err)
	}
	assertRelationship(t, st, verID, profID)
}

func TestResolveGalleryInVMACPVersionRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(galSubID)
	if err := resolveGalleryInVMACPVersionRelationships(sub, st); err != nil {
		t.Fatalf("resolveGalleryInVMACPVersionRelationships (empty): %v", err)
	}
}

// assertRelationship is a helper that verifies a single attached-to relationship exists from→to.
func assertRelationship(t *testing.T, st *store.Store, fromID, toID string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(fromID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != toID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected -[attached-to]-> %s, got %+v", toID, rels[0])
	}
}
