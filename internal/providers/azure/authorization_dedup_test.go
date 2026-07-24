package azure

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"
)

// TestIsTenantDedupedPolicyType pins the per-sub skip boundary: BuiltIn and
// Static dedup to the tenant (both returned by ListBuiltIn) so are skipped
// per-sub; NotSpecified and Custom stay per-sub.
func TestIsTenantDedupedPolicyType(t *testing.T) {
	cases := []struct {
		name string
		in   *armpolicy.PolicyType
		want bool
	}{
		{"BuiltIn", to.Ptr(armpolicy.PolicyTypeBuiltIn), true},
		{"Static", to.Ptr(armpolicy.PolicyTypeStatic), true},
		{"NotSpecified", to.Ptr(armpolicy.PolicyTypeNotSpecified), false},
		{"Custom", to.Ptr(armpolicy.PolicyTypeCustom), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTenantDedupedPolicyType(tc.in); got != tc.want {
				t.Errorf("isTenantDedupedPolicyType(%s) = %v; want %v", tc.name, got, tc.want)
			}
		})
	}
}

// upsertManagedTestResource inserts a ManagedByProvider resource (built-in role /
// policy definition) and returns its stable ID. Built-ins are managed in
// production and ListResources hides managed rows by default, so resolver tests
// MUST seed them managed to exercise the IncludeManaged FK path.
func upsertManagedTestResource(t *testing.T, st *store.Store, accountID, rtype, nativeID, attrsJSON string) string {
	t.Helper()
	region := "global"
	r := &store.Resource{
		Provider: "azure", AccountID: accountID, Type: rtype, NativeID: nativeID,
		Region: &region, AttributesJSON: attrsJSON, DiscoveredBy: testScanID,
		ManagedByProvider: true,
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsertManagedTestResource %s/%s: %v", rtype, nativeID, err)
	}
	return store.ResourceID("azure", accountID, nativeID)
}

// TestNormalizeRoleDefKey pins the scope-independent role-definition identity:
// the GUID-bearing suffix, lowercased, regardless of the (subscription / MG /
// already-stripped) scope prefix the ARM ID carried.
func TestNormalizeRoleDefKey(t *testing.T) {
	const guid = "/providers/microsoft.authorization/roledefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c"
	cases := []struct {
		name, in, want string
	}{
		{"subscription-scoped builtin", "/subscriptions/sub-1/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c", guid},
		{"management-group scoped", "/providers/Microsoft.Management/managementGroups/mg1/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c", guid},
		{"already stripped", "/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c", guid},
		{"mixed case", "/SUBSCRIPTIONS/SUB-1/PROVIDERS/MICROSOFT.AUTHORIZATION/ROLEDEFINITIONS/B24988AC-6180-42A0-AB88-20F7382DD24C", guid},
		{"no segment → lowercased input", "/subscriptions/sub-1/something/else", "/subscriptions/sub-1/something/else"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeRoleDefKey(tc.in); got != tc.want {
				t.Errorf("normalizeRoleDefKey(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestResolveAuthorization_CrossAccountBuiltinFK is the FK-break guard for the
// dedup: a role assignment in a subscription must resolve its -[uses]-> edge to
// a BUILT-IN role definition under the TENANT account (a different account_id),
// matched via the scope-independent role key. Reverting the resolver to a
// same-account GetResource turns this red.
func TestResolveAuthorization_CrossAccountBuiltinFK(t *testing.T) {
	st := newTestStore(t)
	sub := &subscription{ID: "sub-1", tenantID: "tenant-1"}

	// Built-in role def stored once under the tenant account, scope-free
	// NativeID (as scanAuthorizationBuiltins writes it).
	builtinNativeID := "/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c"
	builtinID := upsertManagedTestResource(t, st, sub.tenantID, TypeAuthorizationRoleDefinition, builtinNativeID, `{"properties":{"roleName":"Contributor"}}`)

	// Assignment under the subscription references the built-in with the
	// subscription-scoped form Azure returns.
	asnNativeID := "/subscriptions/sub-1/providers/Microsoft.Authorization/roleAssignments/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	asnAttrs := `{"properties":{"roleDefinitionId":"/subscriptions/sub-1/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c"}}`
	asnID := upsertTestResource(t, st, "azure", sub.ID, TypeAuthorizationRoleAssignment, asnNativeID, "", asnAttrs)

	if err := resolveAuthorizationRelationships(sub, st); err != nil {
		t.Fatalf("resolveAuthorizationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(asnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var got string
	for _, r := range rels {
		if r.Kind == store.RelUses {
			got = r.ToID
		}
	}
	if got != builtinID {
		t.Errorf("uses edge: got %q, want cross-account builtin %q", got, builtinID)
	}
}

// TestResolveAuthorization_DegradedNoTenantID confirms graceful degradation:
// when tenantID is empty (resolution failed → built-ins stored per-sub), the
// assignment still resolves to the role def under its own subscription.
func TestResolveAuthorization_DegradedNoTenantID(t *testing.T) {
	st := newTestStore(t)
	sub := &subscription{ID: "sub-1"} // tenantID empty

	roleNativeID := "/subscriptions/sub-1/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c"
	roleID := upsertManagedTestResource(t, st, sub.ID, TypeAuthorizationRoleDefinition, roleNativeID, `{"properties":{"roleName":"Contributor"}}`)
	asnNativeID := "/subscriptions/sub-1/providers/Microsoft.Authorization/roleAssignments/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	asnID := upsertTestResource(t, st, "azure", sub.ID, TypeAuthorizationRoleAssignment, asnNativeID, "",
		`{"properties":{"roleDefinitionId":"`+roleNativeID+`"}}`)

	if err := resolveAuthorizationRelationships(sub, st); err != nil {
		t.Fatalf("resolveAuthorizationRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(asnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var got string
	for _, r := range rels {
		if r.Kind == store.RelUses {
			got = r.ToID
		}
	}
	if got != roleID {
		t.Errorf("uses edge: got %q, want per-sub role %q", got, roleID)
	}
}

// TestResolvePolicy_CrossAccountBuiltinFK is the policy-side FK-break guard: a
// policy assignment in a subscription resolves its -[uses]-> edge to a built-in
// policy definition stored under the tenant account (scope-free NativeID,
// matched directly).
func TestResolvePolicy_CrossAccountBuiltinFK(t *testing.T) {
	st := newTestStore(t)
	sub := &subscription{ID: "sub-1", tenantID: "tenant-1"}

	defNativeID := "/providers/Microsoft.Authorization/policyDefinitions/0a914e76-4921-4c19-b460-a2d36003525a"
	defID := upsertManagedTestResource(t, st, sub.tenantID, TypePolicyDefinition, defNativeID, `{"properties":{"policyType":"BuiltIn"}}`)

	asnNativeID := "/subscriptions/sub-1/providers/Microsoft.Authorization/policyAssignments/myassignment"
	asnID := upsertTestResource(t, st, "azure", sub.ID, TypePolicyAssignment, asnNativeID, "",
		`{"properties":{"policyDefinitionId":"`+defNativeID+`"}}`)

	if err := resolvePolicyRelationships(sub, st); err != nil {
		t.Fatalf("resolvePolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(asnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var got string
	for _, r := range rels {
		if r.Kind == store.RelUses {
			got = r.ToID
		}
	}
	if got != defID {
		t.Errorf("uses edge: got %q, want cross-account builtin policy def %q", got, defID)
	}
}
