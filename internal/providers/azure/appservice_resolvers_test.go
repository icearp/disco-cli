package azure

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// ----- resolveSiteToServerFarm -----

func TestResolveSiteToServerFarm(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	planNativeID := "/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/serverFarms/my-plan"
	siteNativeID := "/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/sites/my-app"

	planID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceServerFarm, planNativeID, "eastus", "{}")
	siteID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceSite, siteNativeID, "eastus",
		`{"properties":{"serverFarmId":"`+planNativeID+`"}}`)

	if err := resolveSiteToServerFarm(sub, st); err != nil {
		t.Fatalf("resolveSiteToServerFarm: %v", err)
	}

	rels, err := st.RelationshipsFrom(siteID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != planID || rels[0].Kind != store.RelUses {
		t.Errorf("expected site -[uses]-> plan, got %+v", rels[0])
	}
}

func TestResolveSiteToServerFarm_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceSite,
		"/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/sites/my-app",
		"eastus", "{}")

	if err := resolveSiteToServerFarm(sub, st); err != nil {
		t.Fatalf("resolveSiteToServerFarm (empty): %v", err)
	}
}

// ----- resolveSiteToHostingEnv -----

func TestResolveSiteToHostingEnv(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	aseNativeID := "/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/hostingEnvironments/my-ase"
	siteNativeID := "/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/sites/my-app"

	aseID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceEnvironment, aseNativeID, "eastus", "{}")
	siteID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceSite, siteNativeID, "eastus",
		`{"properties":{"hostingEnvironmentProfile":{"id":"`+aseNativeID+`"}}}`)

	if err := resolveSiteToHostingEnv(sub, st); err != nil {
		t.Fatalf("resolveSiteToHostingEnv: %v", err)
	}

	rels, err := st.RelationshipsFrom(siteID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != aseID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected site -[attached-to]-> ase, got %+v", rels[0])
	}
}

func TestResolveSiteToHostingEnv_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceSite,
		"/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/sites/my-app",
		"eastus", "{}")

	if err := resolveSiteToHostingEnv(sub, st); err != nil {
		t.Fatalf("resolveSiteToHostingEnv (empty): %v", err)
	}
}

// ----- resolveSlotToSite -----

func TestResolveSlotToSite(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	siteNativeID := "/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/sites/my-app"
	slotNativeID := siteNativeID + "/slots/staging"

	siteID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceSite, siteNativeID, "eastus", "{}")
	slotID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceSiteSlot, slotNativeID, "eastus", "{}")

	if err := resolveSlotToSite(sub, st); err != nil {
		t.Fatalf("resolveSlotToSite: %v", err)
	}

	rels, err := st.RelationshipsFrom(slotID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != siteID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected slot -[attached-to]-> site, got %+v", rels[0])
	}
}

func TestResolveSlotToSite_NoSlots(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	if err := resolveSlotToSite(sub, st); err != nil {
		t.Fatalf("resolveSlotToSite (empty): %v", err)
	}
}

// ----- resolveSlotToServerFarm -----

func TestResolveSlotToServerFarm(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	planNativeID := "/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/serverFarms/my-plan"
	slotNativeID := "/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/sites/my-app/slots/staging"

	planID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceServerFarm, planNativeID, "eastus", "{}")
	slotID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceSiteSlot, slotNativeID, "eastus",
		`{"properties":{"serverFarmId":"`+planNativeID+`"}}`)

	if err := resolveSlotToServerFarm(sub, st); err != nil {
		t.Fatalf("resolveSlotToServerFarm: %v", err)
	}

	rels, err := st.RelationshipsFrom(slotID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != planID || rels[0].Kind != store.RelUses {
		t.Errorf("expected slot -[uses]-> plan, got %+v", rels[0])
	}
}

func TestResolveSlotToServerFarm_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceSiteSlot,
		"/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/sites/my-app/slots/staging",
		"eastus", "{}")

	if err := resolveSlotToServerFarm(sub, st); err != nil {
		t.Fatalf("resolveSlotToServerFarm (empty): %v", err)
	}
}

// ----- resolveServerFarmToHostingEnv -----

func TestResolveServerFarmToHostingEnv(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	aseNativeID := "/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/hostingEnvironments/my-ase"
	planNativeID := "/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/serverFarms/my-plan"

	aseID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceEnvironment, aseNativeID, "eastus", "{}")
	planID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceServerFarm, planNativeID, "eastus",
		`{"properties":{"hostingEnvironmentProfile":{"id":"`+aseNativeID+`"}}}`)

	if err := resolveServerFarmToHostingEnv(sub, st); err != nil {
		t.Fatalf("resolveServerFarmToHostingEnv: %v", err)
	}

	rels, err := st.RelationshipsFrom(planID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != aseID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected plan -[attached-to]-> ase, got %+v", rels[0])
	}
}

func TestResolveServerFarmToHostingEnv_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceServerFarm,
		"/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/serverFarms/my-plan",
		"eastus", "{}")

	if err := resolveServerFarmToHostingEnv(sub, st); err != nil {
		t.Fatalf("resolveServerFarmToHostingEnv (empty): %v", err)
	}
}

// ----- siteIDFromSlotID -----

func TestSiteIDFromSlotID(t *testing.T) {
	slotID := "/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/sites/my-app/slots/staging"
	want := "/subscriptions/sub-123/resourceGroups/WebRG/providers/Microsoft.Web/sites/my-app"

	got := siteIDFromSlotID(slotID)
	if got != want {
		t.Errorf("siteIDFromSlotID:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestSiteIDFromSlotID_NotSlot(t *testing.T) {
	cases := []string{
		"",
		"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Web/sites/my-app",
	}
	for _, id := range cases {
		if got := siteIDFromSlotID(id); got != "" {
			t.Errorf("siteIDFromSlotID(%q) = %q, want empty", id, got)
		}
	}
}
