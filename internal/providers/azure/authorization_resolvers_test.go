package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
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

// TestResolveAuthorizationRelationships_CrossSubScope verifies that an
// assignment whose Scope subscription differs from the assignment's owner sub
// produces a cross-sub-rbac edge to a foreign-subscription stub. R5.
func TestResolveAuthorizationRelationships_CrossSubScope(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("home-sub")

	asnNativeID := "/subscriptions/home-sub/providers/Microsoft.Authorization/roleAssignments/dddddddd-dddd-dddd-dddd-dddddddddddd"
	asnAttrs := `{
		"properties": {
			"roleDefinitionId": "/subscriptions/home-sub/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000003",
			"scope": "/subscriptions/other-sub/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/foo",
			"principalId": "33333333-3333-3333-3333-333333333333"
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
	var cross *store.Relationship
	for i := range rels {
		if rels[i].Kind == store.RelCrossSubRBAC {
			cross = &rels[i]
			break
		}
	}
	if cross == nil {
		t.Fatalf("missing cross-sub-rbac edge, got: %+v", rels)
	}
	wantStub := store.ResourceID("azure", "other-sub", TypeForeignSubscription, "/subscriptions/other-sub")
	if cross.ToID != wantStub {
		t.Errorf("cross-sub-rbac target: got %q want %q", cross.ToID, wantStub)
	}
	if cross.Attributes == nil {
		t.Errorf("expected non-nil attrs on cross-sub-rbac edge")
	}
}

// TestResolveAuthorizationRelationships_EntraPrincipal verifies that a role
// assignment whose principalId matches an in-store Entra row emits a
// `uses` edge to that principal. Tenant-wide GUID match (case-insensitive).
func TestResolveAuthorizationRelationships_EntraPrincipal(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-123")
	const tenantID = "tenant-abc"
	const userGUID = "55555555-5555-5555-5555-555555555555"

	// Entra user lives under tenant AccountID, NativeID = object GUID.
	userResID := upsertTestResource(t, st, "azure", tenantID, TypeEntraUser, userGUID, "", `{"id":"`+userGUID+`","displayName":"Alice"}`)

	asnNativeID := "/subscriptions/sub-123/providers/Microsoft.Authorization/roleAssignments/eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
	// principalId mixed-case — verify lowercased lookup still matches.
	asnAttrs := `{
		"properties": {
			"roleDefinitionId": "/subscriptions/sub-123/providers/Microsoft.Authorization/roleDefinitions/00000000-0000-0000-0000-000000000004",
			"scope": "/subscriptions/sub-123",
			"principalId": "55555555-5555-5555-5555-555555555555",
			"principalType": "User"
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
	var hit *store.Relationship
	for i := range rels {
		if rels[i].Kind == store.RelUses && rels[i].ToID == userResID {
			hit = &rels[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("missing assignment→entra-user uses edge, got: %+v", rels)
	}
	if hit.Attributes == nil {
		t.Errorf("expected non-nil attrs on principal edge")
	}
}
