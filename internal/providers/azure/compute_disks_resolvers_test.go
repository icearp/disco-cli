package azure

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveSnapshotSourceRelationships verifies that a snapshot's source disk
// relationship is correctly derived from creationData.sourceResourceId.
func TestResolveSnapshotSourceRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	diskNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/disks/my-disk"
	snapNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/snapshots/my-snap"

	attrsJSON := `{"properties":{"creationData":{"sourceResourceId":"` + diskNativeID + `"}}}`

	snapID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeSnapshot, snapNativeID, "eastus", attrsJSON)
	diskID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeManagedDisk, diskNativeID, "eastus", "{}")

	if err := resolveSnapshotSourceRelationships(sub, st); err != nil {
		t.Fatalf("resolveSnapshotSourceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(snapID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != diskID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected snapshot -[attached-to]-> disk, got %+v", rels[0])
	}
}

// TestResolveSnapshotSourceRelationships_NoSnapshots verifies no error when no snapshots exist.
func TestResolveSnapshotSourceRelationships_NoSnapshots(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	if err := resolveSnapshotSourceRelationships(sub, st); err != nil {
		t.Fatalf("resolveSnapshotSourceRelationships (empty): %v", err)
	}
}

// TestResolveDiskEncryptionSetRelationships verifies that a managed disk is linked
// to its disk encryption set via properties.encryption.diskEncryptionSetId.
func TestResolveDiskEncryptionSetRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	desNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/diskEncryptionSets/my-des"
	diskNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/disks/my-disk"

	attrsJSON := `{"properties":{"encryption":{"diskEncryptionSetId":"` + desNativeID + `"}}}`

	diskID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeManagedDisk, diskNativeID, "eastus", attrsJSON)
	desID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeDiskEncryptionSet, desNativeID, "eastus", "{}")

	if err := resolveDiskEncryptionSetRelationships(sub, st); err != nil {
		t.Fatalf("resolveDiskEncryptionSetRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(diskID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != desID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected disk -[attached-to]-> diskEncryptionSet, got %+v", rels[0])
	}
}

// TestResolveDiskEncryptionSetRelationships_NoAttrs verifies no error when disk
// has no encryption field.
func TestResolveDiskEncryptionSetRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	diskNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/disks/bare-disk"
	upsertTestResource(t, st, "azure", sub.ID, TypeComputeManagedDisk, diskNativeID, "eastus", "{}")

	if err := resolveDiskEncryptionSetRelationships(sub, st); err != nil {
		t.Fatalf("resolveDiskEncryptionSetRelationships (empty): %v", err)
	}
}

// TestResolveDiskSourceRelationships_FromSnapshot verifies that a managed disk
// created from a snapshot is linked via properties.creationData.sourceResourceId.
func TestResolveDiskSourceRelationships_FromSnapshot(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	snapNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/snapshots/my-snap"
	diskNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/disks/my-disk"

	attrsJSON := `{"properties":{"creationData":{"sourceResourceId":"` + snapNativeID + `"}}}`

	diskID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeManagedDisk, diskNativeID, "eastus", attrsJSON)
	snapID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeSnapshot, snapNativeID, "eastus", "{}")

	if err := resolveDiskSourceRelationships(sub, st); err != nil {
		t.Fatalf("resolveDiskSourceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(diskID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != snapID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected disk -[attached-to]-> snapshot, got %+v", rels[0])
	}
}

// TestResolveDiskSourceRelationships_NoSource verifies no error when disk has
// no creationData.sourceResourceId.
func TestResolveDiskSourceRelationships_NoSource(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	diskNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/disks/bare-disk"
	upsertTestResource(t, st, "azure", sub.ID, TypeComputeManagedDisk, diskNativeID, "eastus", "{}")

	if err := resolveDiskSourceRelationships(sub, st); err != nil {
		t.Fatalf("resolveDiskSourceRelationships (empty): %v", err)
	}
}
