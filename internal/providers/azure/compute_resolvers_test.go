package azure

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

const testSubID = "sub-abc-123"

// TestResolveVMAvailabilitySetRelationships verifies that a VM's availability set
// relationship is correctly derived from the availabilitySet.id field in stored JSON.
func TestResolveVMAvailabilitySetRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	availNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/availabilitySets/my-avail"
	vmNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/my-vm"

	attrsJSON := `{"properties":{"availabilitySet":{"id":"` + availNativeID + `"}}}`

	vmID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVirtualMachine, vmNativeID, "eastus", attrsJSON)
	availID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeAvailabilitySet, availNativeID, "eastus", "{}")

	if err := resolveVMAvailabilitySetRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMAvailabilitySetRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(vmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != availID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected vm -[attached-to]-> availSet, got %+v", rels[0])
	}
}

// TestResolveVMAvailabilitySetRelationships_NoAttrs verifies no error when VM has
// no availability set in its attributes (empty JSON case).
func TestResolveVMAvailabilitySetRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	vmNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/bare-vm"
	upsertTestResource(t, st, "azure", sub.ID, TypeComputeVirtualMachine, vmNativeID, "eastus", "{}")

	if err := resolveVMAvailabilitySetRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMAvailabilitySetRelationships (empty): %v", err)
	}
}

// TestResolveVMProximityGroupRelationships verifies that a VM's proximity placement
// group relationship is correctly derived from the stored attributes JSON.
func TestResolveVMProximityGroupRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	ppgNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/proximityPlacementGroups/my-ppg"
	vmNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/my-vm"

	attrsJSON := `{"properties":{"proximityPlacementGroup":{"id":"` + ppgNativeID + `"}}}`

	vmID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVirtualMachine, vmNativeID, "eastus", attrsJSON)
	ppgID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeProximityPlacementGroup, ppgNativeID, "eastus", "{}")

	if err := resolveVMProximityGroupRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMProximityGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(vmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != ppgID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected vm -[attached-to]-> ppg, got %+v", rels[0])
	}
}

// TestResolveVMProximityGroupRelationships_NoAttrs verifies no error when no VMs exist.
func TestResolveVMProximityGroupRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	if err := resolveVMProximityGroupRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMProximityGroupRelationships (empty): %v", err)
	}
}

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
