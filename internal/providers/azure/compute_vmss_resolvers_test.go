package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

const vmssSubID = "sub-vmss-test"

// TestResolveVMSSExtensionRelationships verifies that a VMSS extension's parent VMSS
// is derived by truncating the NativeID at "/extensions/".
func TestResolveVMSSExtensionRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(vmssSubID)

	vmssNativeID := "/subscriptions/sub-vmss-test/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachineScaleSets/my-vmss"
	extNativeID := vmssNativeID + "/extensions/my-ext"

	extID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVMSSExtension, extNativeID, "", "{}")
	vmssID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVMSS, vmssNativeID, "eastus", "{}")

	if err := resolveVMSSExtensionRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMSSExtensionRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(extID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vmssID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected vmssExtension -[attached-to]-> vmss, got %+v", rels[0])
	}
}

// TestResolveVMSSExtensionRelationships_Empty verifies no error when no VMSS extensions exist.
func TestResolveVMSSExtensionRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(vmssSubID)

	if err := resolveVMSSExtensionRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMSSExtensionRelationships (empty): %v", err)
	}
}

// TestResolveVMSSVMRelationships verifies that a VMSS VM's parent VMSS is derived
// by truncating the NativeID at "/virtualMachines/".
func TestResolveVMSSVMRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(vmssSubID)

	vmssNativeID := "/subscriptions/sub-vmss-test/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachineScaleSets/my-vmss"
	vmNativeID := vmssNativeID + "/virtualMachines/0"

	vmID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVMSSVM, vmNativeID, "eastus", "{}")
	vmssID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVMSS, vmssNativeID, "eastus", "{}")

	if err := resolveVMSSVMRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMSSVMRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(vmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vmssID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected vmssVM -[attached-to]-> vmss, got %+v", rels[0])
	}
}

// TestResolveVMSSVMRelationships_Empty verifies no error when no VMSS VMs exist.
func TestResolveVMSSVMRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(vmssSubID)

	if err := resolveVMSSVMRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMSSVMRelationships (empty): %v", err)
	}
}

// TestResolveVMSSVMExtensionRelationships verifies that a VMSS VM extension's parent
// VMSS VM is derived by truncating the NativeID at "/extensions/".
func TestResolveVMSSVMExtensionRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(vmssSubID)

	vmssNativeID := "/subscriptions/sub-vmss-test/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachineScaleSets/my-vmss"
	vmNativeID := vmssNativeID + "/virtualMachines/0"
	extNativeID := vmNativeID + "/extensions/my-ext"

	extID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVMSSVMExtension, extNativeID, "", "{}")
	vmID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVMSSVM, vmNativeID, "eastus", "{}")

	if err := resolveVMSSVMExtensionRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMSSVMExtensionRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(extID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vmID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected vmssVMExtension -[attached-to]-> vmssVM, got %+v", rels[0])
	}
}

// TestResolveVMSSVMExtensionRelationships_Empty verifies no error when no VMSS VM extensions exist.
func TestResolveVMSSVMExtensionRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(vmssSubID)

	if err := resolveVMSSVMExtensionRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMSSVMExtensionRelationships (empty): %v", err)
	}
}

// TestResolveVMSSProximityGroupRelationships verifies that a VMSS's proximity
// placement group relationship is derived from stored attributes JSON.
func TestResolveVMSSProximityGroupRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(vmssSubID)

	ppgNativeID := "/subscriptions/sub-vmss-test/resourceGroups/rg1/providers/Microsoft.Compute/proximityPlacementGroups/my-ppg"
	vmssNativeID := "/subscriptions/sub-vmss-test/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachineScaleSets/my-vmss"

	attrsJSON := `{"properties":{"proximityPlacementGroup":{"id":"` + ppgNativeID + `"}}}`

	vmssID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVMSS, vmssNativeID, "eastus", attrsJSON)
	ppgID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeProximityPlacementGroup, ppgNativeID, "eastus", "{}")

	if err := resolveVMSSProximityGroupRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMSSProximityGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(vmssID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != ppgID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected vmss -[attached-to]-> ppg, got %+v", rels[0])
	}
}

// TestResolveVMSSProximityGroupRelationships_Empty verifies no error when no VMSS exist.
func TestResolveVMSSProximityGroupRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(vmssSubID)

	if err := resolveVMSSProximityGroupRelationships(sub, st); err != nil {
		t.Fatalf("resolveVMSSProximityGroupRelationships (empty): %v", err)
	}
}
