package azure

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveAuthorizationRelationships verifies that a role assignment derives
// (a) a -[uses]-> edge to its role definition (FK on roleDefinitionId), and
// (b) an -[attached-to]-> edge to its scope when the scope matches a known
// resource (case-insensitive match on Azure resource IDs).
func TestResolveAuthorizationRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")

	roleDefNativeID := "/subscriptions/sub-123/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000001"
	scopeNativeID := "/subscriptions/sub-123/resourceGroups/MyRG/providers/Microsoft.Storage/storageAccounts/myacct"
	asnNativeID := "/subscriptions/sub-123/providers/Microsoft.Authorization/roleAssignments/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	// Role assignment scope is intentionally upper-cased to verify case-insensitive match.
	asnAttrs := `{
		"properties": {
			"roleDefinitionId": "` + roleDefNativeID + `",
			"scope": "/SUBSCRIPTIONS/SUB-123/RESOURCEGROUPS/MYRG/PROVIDERS/MICROSOFT.STORAGE/STORAGEACCOUNTS/MYACCT",
			"principalId": "11111111-1111-1111-1111-111111111111",
			"principalType": "ServicePrincipal"
		}
	}`

	roleDefID := upsertTestResource(t, st, "azure", sub.ID, TypeAuthorizationRoleDefinition, roleDefNativeID, "", `{"properties":{"roleName":"Storage Blob Data Reader"}}`)
	scopeResID := upsertTestResource(t, st, "azure", sub.ID, TypeStorageStorageAccount, scopeNativeID, "eastus", "{}")
	asnID := upsertTestResource(t, st, "azure", sub.ID, TypeAuthorizationRoleAssignment, asnNativeID, "", asnAttrs)

	if err := resolveAuthorizationRelationships(sub, st); err != nil {
		t.Fatalf("resolveAuthorizationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(asnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d (%+v)", len(rels), rels)
	}

	gotKinds := map[string]string{}
	for _, r := range rels {
		gotKinds[r.Kind] = r.ToID
	}
	if gotKinds[store.RelUses] != roleDefID {
		t.Errorf("uses edge: got %q, want %q", gotKinds[store.RelUses], roleDefID)
	}
	if gotKinds[store.RelAttachedTo] != scopeResID {
		t.Errorf("attached-to edge: got %q, want %q", gotKinds[store.RelAttachedTo], scopeResID)
	}
}

// TestResolveAuthorizationRelationships_UnknownScope verifies that an
// assignment whose scope does not match any local resource still produces
// the role-definition edge but no scope edge — and does not error.
func TestResolveAuthorizationRelationships_UnknownScope(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-456")

	roleDefNativeID := "/subscriptions/sub-456/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000002"
	asnNativeID := "/subscriptions/sub-456/providers/Microsoft.Authorization/roleAssignments/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	asnAttrs := `{
		"properties": {
			"roleDefinitionId": "` + roleDefNativeID + `",
			"scope": "/subscriptions/sub-456/resourceGroups/UnknownRG/providers/Microsoft.Compute/virtualMachines/missing",
			"principalId": "22222222-2222-2222-2222-222222222222"
		}
	}`

	roleDefID := upsertTestResource(t, st, "azure", sub.ID, TypeAuthorizationRoleDefinition, roleDefNativeID, "", "{}")
	asnID := upsertTestResource(t, st, "azure", sub.ID, TypeAuthorizationRoleAssignment, asnNativeID, "", asnAttrs)

	if err := resolveAuthorizationRelationships(sub, st); err != nil {
		t.Fatalf("resolveAuthorizationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(asnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship (role-def only), got %d", len(rels))
	}
	if rels[0].ToID != roleDefID || rels[0].Kind != store.RelUses {
		t.Errorf("expected uses→role-def, got %+v", rels[0])
	}
}

// TestResolveAuthorizationRelationships_MissingRoleDef verifies that an
// assignment referencing a role definition not present locally (e.g. a
// tenant-scope built-in not yet listed) produces no edge and no error.
func TestResolveAuthorizationRelationships_MissingRoleDef(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-789")

	asnNativeID := "/subscriptions/sub-789/providers/Microsoft.Authorization/roleAssignments/cccccccc-cccc-cccc-cccc-cccccccccccc"
	asnAttrs := `{
		"properties": {
			"roleDefinitionId": "/providers/Microsoft.Authorization/roleDefinitions/ffffffff-ffff-ffff-ffff-ffffffffffff",
			"scope": "/subscriptions/sub-789"
		}
	}`
	asnID := upsertTestResource(t, st, "azure", sub.ID, TypeAuthorizationRoleAssignment, asnNativeID, "", asnAttrs)

	if err := resolveAuthorizationRelationships(sub, st); err != nil {
		t.Fatalf("resolveAuthorizationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(asnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
