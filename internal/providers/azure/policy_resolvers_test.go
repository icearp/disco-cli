package azure

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolvePolicyRelationships verifies a policy assignment derives both:
// (a) -[uses]-> policy-definition (FK on policyDefinitionId), and
// (b) -[attached-to]-> scoped-resource (case-insensitive scope lookup).
func TestResolvePolicyRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-pol")

	defNativeID := "/subscriptions/sub-pol/providers/Microsoft.Authorization/policyDefinitions/audit-foo"
	defID := upsertTestResource(t, st, "azure", sub.ID, TypePolicyDefinition, defNativeID, "", "{}")

	scopeNativeID := "/subscriptions/sub-pol/resourceGroups/MyRG"
	scopeResID := upsertTestResource(t, st, "azure", sub.ID, TypeResourcesResourceGroup, scopeNativeID, "", "{}")

	asnNativeID := "/subscriptions/sub-pol/providers/Microsoft.Authorization/policyAssignments/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	asnAttrs := `{"properties":{"policyDefinitionId":"` + defNativeID + `","scope":"/SUBSCRIPTIONS/SUB-POL/RESOURCEGROUPS/MYRG"}}`
	asnID := upsertTestResource(t, st, "azure", sub.ID, TypePolicyAssignment, asnNativeID, "", asnAttrs)

	if err := resolvePolicyRelationships(sub, st); err != nil {
		t.Fatalf("resolvePolicyRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(asnID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d (%+v)", len(rels), rels)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.Kind] = r.ToID
	}
	if got[store.RelUses] != defID {
		t.Errorf("uses edge: got %q, want %q", got[store.RelUses], defID)
	}
	if got[store.RelAttachedTo] != scopeResID {
		t.Errorf("attached-to edge: got %q, want %q", got[store.RelAttachedTo], scopeResID)
	}
}

// TestResolvePolicyRelationships_BuiltInDef verifies an assignment referencing
// a tenant-scoped built-in definition (not in local store) produces no
// definition edge but still emits the scope edge.
func TestResolvePolicyRelationships_BuiltInDef(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-pol")

	asnNativeID := "/subscriptions/sub-pol/providers/Microsoft.Authorization/policyAssignments/x"
	asnAttrs := `{"properties":{"policyDefinitionId":"/providers/Microsoft.Authorization/policyDefinitions/built-in-not-fetched","scope":"/subscriptions/sub-pol"}}`
	asnID := upsertTestResource(t, st, "azure", sub.ID, TypePolicyAssignment, asnNativeID, "", asnAttrs)

	if err := resolvePolicyRelationships(sub, st); err != nil {
		t.Fatalf("resolvePolicyRelationships (builtin): %v", err)
	}
	rels, _ := st.RelationshipsFrom(asnID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships (no def, sub-root scope unresolvable), got %d", len(rels))
	}
}

// TestResolvePolicyRelationships_ManagementGroupScope covers an assignment
// inherited from an ancestor management group: its scope is the MG ID, stored
// under the tenant account (outside the per-sub index), so the resolver must
// merge tenant-account MGs in to emit the attached-to edge.
func TestResolvePolicyRelationships_ManagementGroupScope(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-pol")
	sub.tenantID = "tenant-pol"

	mgID := "/providers/Microsoft.Management/managementGroups/mg-root"
	mgRID := upsertTestResource(t, st, "azure", sub.tenantID, TypeManagementGroup, mgID, "global", "{}")

	asnNativeID := "/subscriptions/sub-pol/providers/Microsoft.Authorization/policyAssignments/inherited"
	asnAttrs := `{"properties":{"scope":` + jsonStr(mgID) + `}}`
	asnID := upsertTestResource(t, st, "azure", sub.ID, TypePolicyAssignment, asnNativeID, "", asnAttrs)

	if err := resolvePolicyRelationships(sub, st); err != nil {
		t.Fatalf("resolvePolicyRelationships (mg scope): %v", err)
	}

	rels, _ := st.RelationshipsFrom(asnID, store.RelAttachedTo)
	var found bool
	for _, r := range rels {
		if r.ToID == mgRID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected attached-to edge %s -> management group %s, got %v", asnID, mgRID, rels)
	}
}

// jsonStr quotes s as a JSON string literal for inline attribute fixtures.
func jsonStr(s string) string { return `"` + s + `"` }
