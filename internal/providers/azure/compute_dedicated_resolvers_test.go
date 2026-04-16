package azure

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

const hostSubID = "sub-host-test"

func TestResolveDedicatedHostRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(hostSubID)

	hgNativeID := "/subscriptions/sub-host-test/resourceGroups/rg1/providers/Microsoft.Compute/hostGroups/my-hg"
	hostNativeID := hgNativeID + "/hosts/my-host"

	hostID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeDedicatedHost, hostNativeID, "eastus", "{}")
	hgID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeHostGroup, hgNativeID, "eastus", "{}")

	if err := resolveDedicatedHostRelationships(sub, st); err != nil {
		t.Fatalf("resolveDedicatedHostRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(hostID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != hgID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected host -[attached-to]-> hostGroup, got %+v", rels[0])
	}
}

func TestResolveDedicatedHostRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(hostSubID)
	if err := resolveDedicatedHostRelationships(sub, st); err != nil {
		t.Fatalf("resolveDedicatedHostRelationships (empty): %v", err)
	}
}

func TestResolveCapacityReservationRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(hostSubID)

	crgNativeID := "/subscriptions/sub-host-test/resourceGroups/rg1/providers/Microsoft.Compute/capacityReservationGroups/my-crg"
	crNativeID := crgNativeID + "/capacityReservations/my-cr"

	crID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeCapacityReservation, crNativeID, "eastus", "{}")
	crgID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeCapacityReservationGroup, crgNativeID, "eastus", "{}")

	if err := resolveCapacityReservationRelationships(sub, st); err != nil {
		t.Fatalf("resolveCapacityReservationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(crID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != crgID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected capacityReservation -[attached-to]-> CRG, got %+v", rels[0])
	}
}

func TestResolveCapacityReservationRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(hostSubID)
	if err := resolveCapacityReservationRelationships(sub, st); err != nil {
		t.Fatalf("resolveCapacityReservationRelationships (empty): %v", err)
	}
}
