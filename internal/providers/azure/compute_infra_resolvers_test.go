package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
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

// TestResolveVMExtensionRelationships verifies that a VM extension is linked to
// its parent VM by truncating the NativeID at "/extensions/".
func TestResolveVMExtensionRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	vmNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/my-vm"
	extNativeID := vmNativeID + "/extensions/my-ext"

	vmID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVirtualMachine, vmNativeID, "eastus", "{}")
	extID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVMExtension, extNativeID, "eastus", "{}")

	if err := resolveVMExtensionRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMExtensionRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(extID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vmID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected vmExtension -[attached-to]-> vm, got %+v", rels[0])
	}
}

// TestResolveVMExtensionRelationships_Empty verifies no error when no extensions exist.
func TestResolveVMExtensionRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	if err := resolveVMExtensionRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMExtensionRelationships (empty): %v", err)
	}
}

// TestResolveImageSourceVMRelationships verifies that a custom image is linked
// to its source VM via properties.sourceVirtualMachine.id.
func TestResolveImageSourceVMRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	vmNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/my-vm"
	imgNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/images/my-image"

	attrsJSON := `{"properties":{"sourceVirtualMachine":{"id":"` + vmNativeID + `"}}}`

	imgID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeImage, imgNativeID, "eastus", attrsJSON)
	vmID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVirtualMachine, vmNativeID, "eastus", "{}")

	if err := resolveImageSourceVMRelationships(sub, st); err != nil {
		t.Fatalf("resolveImageSourceVMRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(imgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vmID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected image -[attached-to]-> vm, got %+v", rels[0])
	}
}

// TestResolveImageSourceVMRelationships_NoAttrs verifies no error when image has
// no sourceVirtualMachine field.
func TestResolveImageSourceVMRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	imgNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/images/bare-image"
	upsertTestResource(t, st, "azure", sub.ID, TypeComputeImage, imgNativeID, "eastus", "{}")

	if err := resolveImageSourceVMRelationships(sub, st); err != nil {
		t.Fatalf("resolveImageSourceVMRelationships (empty): %v", err)
	}
}

// TestResolveRestorePointCollectionSourceRelationships verifies that a restore
// point collection is linked to its source VM via properties.source.id.
func TestResolveRestorePointCollectionSourceRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	vmNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/my-vm"
	rpcNativeID := "/subscriptions/sub-abc-123/resourceGroups/rg1/providers/Microsoft.Compute/restorePointCollections/my-rpc"

	attrsJSON := `{"properties":{"source":{"id":"` + vmNativeID + `"}}}`

	rpcID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeRestorePointCollection, rpcNativeID, "eastus", attrsJSON)
	vmID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVirtualMachine, vmNativeID, "eastus", "{}")

	if err := resolveRestorePointCollectionSourceRelationships(sub, st); err != nil {
		t.Fatalf("resolveRestorePointCollectionSourceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(rpcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vmID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected rpc -[attached-to]-> vm, got %+v", rels[0])
	}
}

// TestResolveRestorePointCollectionSourceRelationships_NoAttrs verifies no error
// when no restore point collections exist.
func TestResolveRestorePointCollectionSourceRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	if err := resolveRestorePointCollectionSourceRelationships(sub, st); err != nil {
		t.Fatalf("resolveRestorePointCollectionSourceRelationships (empty): %v", err)
	}
}
