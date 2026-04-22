package azure

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveCloudServiceRoleRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(hostSubID)

	csNativeID := "/subscriptions/sub-host-test/resourceGroups/rg1/providers/Microsoft.Compute/cloudServices/my-cs"
	roleNativeID := csNativeID + "/roles/my-role"

	roleID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeCloudServiceRole, roleNativeID, "", "{}")
	csID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeCloudService, csNativeID, "eastus", "{}")

	if err := resolveCloudServiceRoleRelationships(sub, st); err != nil {
		t.Fatalf("resolveCloudServiceRoleRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(roleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != csID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected cloudServiceRole -[attached-to]-> cloudService, got %+v", rels[0])
	}
}

func TestResolveCloudServiceRoleRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(hostSubID)
	if err := resolveCloudServiceRoleRelationships(sub, st); err != nil {
		t.Fatalf("resolveCloudServiceRoleRelationships (empty): %v", err)
	}
}

func TestResolveCloudServiceRoleInstanceRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(hostSubID)

	csNativeID := "/subscriptions/sub-host-test/resourceGroups/rg1/providers/Microsoft.Compute/cloudServices/my-cs"
	riNativeID := csNativeID + "/roleInstances/my-instance"

	riID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeCloudServiceRoleInstance, riNativeID, "", "{}")
	csID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeCloudService, csNativeID, "eastus", "{}")

	if err := resolveCloudServiceRoleInstanceRelationships(sub, st); err != nil {
		t.Fatalf("resolveCloudServiceRoleInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(riID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != csID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected cloudServiceRoleInstance -[attached-to]-> cloudService, got %+v", rels[0])
	}
}

func TestResolveCloudServiceRoleInstanceRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(hostSubID)
	if err := resolveCloudServiceRoleInstanceRelationships(sub, st); err != nil {
		t.Fatalf("resolveCloudServiceRoleInstanceRelationships (empty): %v", err)
	}
}
