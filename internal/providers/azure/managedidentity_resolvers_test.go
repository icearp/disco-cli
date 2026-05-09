package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveManagedIdentityAssignmentPrincipals verifies that a role assignment
// whose principalId matches a user-assigned-identity's properties.principalId
// gets a -[uses]-> edge to that identity. Lookup is case-insensitive on the
// principal GUID.
func TestResolveManagedIdentityAssignmentPrincipals(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-msi")

	msiNativeID := "/subscriptions/sub-msi/resourceGroups/RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/my-msi"
	msiAttrs := `{"properties":{"principalId":"AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA","clientId":"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}}`
	msiID := upsertTestResource(t, st, "azure", sub.ID, TypeManagedIdentityUserAssigned, msiNativeID, "eastus", msiAttrs)

	asnNativeID := "/subscriptions/sub-msi/providers/Microsoft.Authorization/roleAssignments/cccccccc-cccc-cccc-cccc-cccccccccccc"
	asnAttrs := `{"properties":{"principalId":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","principalType":"ServicePrincipal"}}`
	asnID := upsertTestResource(t, st, "azure", sub.ID, TypeAuthorizationRoleAssignment, asnNativeID, "", asnAttrs)

	if err := resolveManagedIdentityAssignmentPrincipals(sub, st); err != nil {
		t.Fatalf("resolveManagedIdentityAssignmentPrincipals: %v", err)
	}
	rels, err := st.RelationshipsFrom(asnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != msiID || rels[0].Kind != store.RelUses {
		t.Errorf("expected asn -[uses]-> msi, got %+v", rels)
	}
}

// TestResolveManagedIdentityConsumers verifies that a host resource (e.g. a
// VM) whose attributes carry an identity.userAssignedIdentities map gets a
// -[uses]-> edge to each referenced user-assigned identity. Map keys are
// matched case-insensitively against MSI NativeIDs.
func TestResolveManagedIdentityConsumers(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-msi")

	msiNativeID := "/subscriptions/sub-msi/resourceGroups/RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/my-msi"
	msiID := upsertTestResource(t, st, "azure", sub.ID, TypeManagedIdentityUserAssigned, msiNativeID, "eastus", `{"properties":{"principalId":"x"}}`)

	vmNativeID := "/subscriptions/sub-msi/resourceGroups/RG/providers/Microsoft.Compute/virtualMachines/host"
	// Mixed casing on the map key — matches what Azure returns when the user
	// supplied an upper-cased ARM ID at assignment time.
	vmAttrs := `{"identity":{"type":"UserAssigned","userAssignedIdentities":{"/SUBSCRIPTIONS/SUB-MSI/RESOURCEGROUPS/RG/PROVIDERS/MICROSOFT.MANAGEDIDENTITY/USERASSIGNEDIDENTITIES/MY-MSI":{"clientId":"x","principalId":"y"}}}}`
	vmID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVirtualMachine, vmNativeID, "eastus", vmAttrs)

	if err := resolveManagedIdentityConsumers(sub, st); err != nil {
		t.Fatalf("resolveManagedIdentityConsumers: %v", err)
	}
	rels, err := st.RelationshipsFrom(vmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != msiID || rels[0].Kind != store.RelUses {
		t.Errorf("expected vm -[uses]-> msi, got %+v", rels)
	}
}

// TestResolveManagedIdentityConsumers_NoIdentity verifies that a host with
// no `identity` block produces no edges and no error.
func TestResolveManagedIdentityConsumers_NoIdentity(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-msi")

	msiNativeID := "/subscriptions/sub-msi/resourceGroups/RG/providers/Microsoft.ManagedIdentity/userAssignedIdentities/orphan"
	upsertTestResource(t, st, "azure", sub.ID, TypeManagedIdentityUserAssigned, msiNativeID, "eastus", `{"properties":{"principalId":"x"}}`)
	vmID := upsertTestResource(t, st, "azure", sub.ID, TypeComputeVirtualMachine,
		"/subscriptions/sub-msi/resourceGroups/RG/providers/Microsoft.Compute/virtualMachines/bare", "eastus", "{}")

	if err := resolveManagedIdentityConsumers(sub, st); err != nil {
		t.Fatalf("resolveManagedIdentityConsumers: %v", err)
	}
	rels, err := st.RelationshipsFrom(vmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
