package azure

import (
	"net/http"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	armauthzfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"
	armpolicyfake "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy/fake"
)

const (
	builtinRoleGUID    = "/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c"
	customRoleNativeID = "/subscriptions/sub-1/providers/Microsoft.Authorization/roleDefinitions/cccccccc-cccc-cccc-cccc-cccccccccccc"
)

func roleDefsServer(t *testing.T) *armauthorization.RoleDefinitionsClient {
	t.Helper()
	server := armauthzfake.RoleDefinitionsServer{
		NewListPager: func(_ string, _ *armauthorization.RoleDefinitionsClientListOptions) fake.PagerResponder[armauthorization.RoleDefinitionsClientListResponse] {
			r := fake.PagerResponder[armauthorization.RoleDefinitionsClientListResponse]{}
			r.AddPage(http.StatusOK, armauthorization.RoleDefinitionsClientListResponse{
				RoleDefinitionListResult: armauthorization.RoleDefinitionListResult{
					Value: []*armauthorization.RoleDefinition{
						{ // built-in, scope-prefixed ID as Azure returns at sub scope
							ID:   to.Ptr("/subscriptions/sub-1/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c"),
							Name: to.Ptr("b24988ac-6180-42a0-ab88-20f7382dd24c"),
							Properties: &armauthorization.RoleDefinitionProperties{
								RoleName: to.Ptr("Contributor"), RoleType: to.Ptr("BuiltInRole"),
							},
						},
						{ // custom role
							ID:   to.Ptr(customRoleNativeID),
							Name: to.Ptr("cccccccc-cccc-cccc-cccc-cccccccccccc"),
							Properties: &armauthorization.RoleDefinitionProperties{
								RoleName: to.Ptr("My Custom Role"), RoleType: to.Ptr("CustomRole"),
							},
						},
					},
				},
			}, nil)
			return r
		},
	}
	client, err := armauthorization.NewRoleDefinitionsClient(fakeCred(), fakeClientOptions(t, armauthzfake.NewRoleDefinitionsServerTransport(&server)))
	if err != nil {
		t.Fatalf("NewRoleDefinitionsClient: %v", err)
	}
	return client
}

// TestScanBuiltinRoleDefsInto_NormalizesUnderTenant verifies the tenant built-in
// fetch stores built-in role defs under the tenant account with a scope-free
// NativeID, never leaking a custom role into the tenant store.
func TestScanBuiltinRoleDefsInto_NormalizesUnderTenant(t *testing.T) {
	st := newTestStore(t)
	const tenantID = "tenant-1"

	total, inserted, err := scanBuiltinRoleDefsInto(t.Context(), "/subscriptions/sub-1", tenantID, st, testScanID, roleDefsServer(t))
	if err != nil {
		t.Fatalf("scanBuiltinRoleDefsInto: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (built-in only, custom filtered)", total, inserted)
	}
	// Built-in stored under tenant account with the normalized, scope-free ID.
	if _, err := st.GetResource(store.ResourceID("azure", tenantID, TypeAuthorizationRoleDefinition, builtinRoleGUID)); err != nil {
		t.Errorf("built-in role not stored under tenant with normalized NativeID: %v", err)
	}
}

// TestScanRoleDefinitionsInto_SkipsBuiltinsWhenTenantSet verifies the per-sub
// scanner stores ONLY custom roles when a tenant GUID is set (built-ins dedup
// to the tenant), but stores both when it is empty (degraded mode).
func TestScanRoleDefinitionsInto_SkipsBuiltinsWhenTenantSet(t *testing.T) {
	t.Run("tenant set → custom only", func(t *testing.T) {
		st := newTestStore(t)
		sub := &subscription{ID: "sub-1", tenantID: "tenant-1"}
		total, _, err := scanRoleDefinitionsInto(t.Context(), sub, st, testScanID, roleDefsServer(t))
		if err != nil {
			t.Fatalf("scanRoleDefinitionsInto: %v", err)
		}
		if total != 1 {
			t.Errorf("got %d stored, want 1 (custom only; built-in skipped)", total)
		}
		if _, err := st.GetResource(store.ResourceID("azure", sub.ID, TypeAuthorizationRoleDefinition, customRoleNativeID)); err != nil {
			t.Errorf("custom role should persist per-sub: %v", err)
		}
	})
	t.Run("no tenant → built-in + custom", func(t *testing.T) {
		st := newTestStore(t)
		sub := &subscription{ID: "sub-1"} // tenantID empty
		total, _, err := scanRoleDefinitionsInto(t.Context(), sub, st, testScanID, roleDefsServer(t))
		if err != nil {
			t.Fatalf("scanRoleDefinitionsInto: %v", err)
		}
		if total != 2 {
			t.Errorf("got %d stored, want 2 (built-in + custom in degraded mode)", total)
		}
	})
}

// TestScanBuiltinPolicyDefsInto verifies built-in policy defs are fetched from
// the tenant-level ListBuiltIn endpoint and stored under the tenant account with
// scope-free NativeID verbatim.
func TestScanBuiltinPolicyDefsInto(t *testing.T) {
	st := newTestStore(t)
	const tenantID = "tenant-1"
	policyID := "/providers/Microsoft.Authorization/policyDefinitions/0a914e76-4921-4c19-b460-a2d36003525a"

	server := armpolicyfake.DefinitionsServer{
		NewListBuiltInPager: func(_ *armpolicy.DefinitionsClientListBuiltInOptions) fake.PagerResponder[armpolicy.DefinitionsClientListBuiltInResponse] {
			r := fake.PagerResponder[armpolicy.DefinitionsClientListBuiltInResponse]{}
			r.AddPage(http.StatusOK, armpolicy.DefinitionsClientListBuiltInResponse{
				DefinitionListResult: armpolicy.DefinitionListResult{
					Value: []*armpolicy.Definition{{
						ID:         to.Ptr(policyID),
						Name:       to.Ptr("0a914e76-4921-4c19-b460-a2d36003525a"),
						Properties: &armpolicy.DefinitionProperties{PolicyType: to.Ptr(armpolicy.PolicyTypeBuiltIn)},
					}},
				},
			}, nil)
			return r
		},
	}
	client, err := armpolicy.NewDefinitionsClient("sub-1", fakeCred(), fakeClientOptions(t, armpolicyfake.NewDefinitionsServerTransport(&server)))
	if err != nil {
		t.Fatalf("NewDefinitionsClient: %v", err)
	}

	total, inserted, err := scanBuiltinPolicyDefsInto(t.Context(), tenantID, st, testScanID, client)
	if err != nil {
		t.Fatalf("scanBuiltinPolicyDefsInto: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	got, err := st.GetResource(store.ResourceID("azure", tenantID, TypePolicyDefinition, policyID))
	if err != nil {
		t.Fatalf("built-in policy def not stored under tenant: %v", err)
	}
	if !got.ManagedByProvider {
		t.Error("built-in policy def should be ManagedByProvider")
	}
}
