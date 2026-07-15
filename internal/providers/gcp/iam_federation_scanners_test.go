package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

// fakeIAMService builds a *iam.Service pointed at the fake server. iam/v1's
// BasePath carries no version segment; route templates embed a bare "v1/"
// prefix — route keys below are "/v1/...".
func fakeIAMService(t *testing.T, srv *httptest.Server) *iam.Service {
	t.Helper()
	svc, err := iam.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("iam.NewService: %v", err)
	}
	return svc
}

func TestScanIAMWorkforceTree_FullFanout(t *testing.T) {
	st := newTestStore(t)
	sc := orgScope{Kind: "organization", Name: "organizations/123", Resource: store.ResourceID("gcp", "organizations/123", "organizations/123")}
	upsertTestResource(t, st, "gcp", sc.Name, TypeOrganization, sc.Name, "", "{}")

	poolName := "locations/global/workforcePools/pool1"
	providerName := poolName + "/providers/prov1"
	tenantName := providerName + "/scimTenants/tenant1"

	routes := map[string]string{
		"/v1/locations/global/workforcePools": marshalAttrs(t, iam.ListWorkforcePoolsResponse{
			WorkforcePools: []*iam.WorkforcePool{{Name: poolName, DisplayName: "Pool 1"}},
		}),
		"/v1/" + poolName + "/providers": marshalAttrs(t, iam.ListWorkforcePoolProvidersResponse{
			WorkforcePoolProviders: []*iam.WorkforcePoolProvider{{Name: providerName, DisplayName: "Okta"}},
		}),
		"/v1/" + providerName + "/scimTenants": marshalAttrs(t, iam.ListWorkforcePoolProviderScimTenantsResponse{
			WorkforcePoolProviderScimTenants: []*iam.WorkforcePoolProviderScimTenant{{Name: tenantName, DisplayName: "Tenant 1"}},
		}),
		"/v1/organizations/123/roles": marshalAttrs(t, iam.ListRolesResponse{
			Roles: []*iam.Role{{Name: "organizations/123/roles/custom1", Title: "Custom Org Role"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeIAMService(t, srv)

	total, inserted, poolIDs, err := scanIAMWorkforcePools(t.Context(), svc, sc, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMWorkforcePools: %v", err)
	}
	if total != 1 || inserted != 1 || len(poolIDs) != 1 {
		t.Fatalf("pools: got total=%d inserted=%d poolIDs=%v, want 1/1/[%s]", total, inserted, poolIDs, poolName)
	}

	total, inserted, providerIDs, err := scanIAMWorkforceProviders(t.Context(), svc, sc, poolIDs, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMWorkforceProviders: %v", err)
	}
	if total != 1 || inserted != 1 || len(providerIDs) != 1 {
		t.Fatalf("providers: got total=%d inserted=%d providerIDs=%v, want 1/1/[%s]", total, inserted, providerIDs, providerName)
	}

	total, inserted, err = scanIAMWorkforceScimTenants(t.Context(), svc, sc, providerIDs, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMWorkforceScimTenants: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("scim tenants: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	total, inserted, err = scanIAMOrgRoles(t.Context(), svc, sc, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMOrgRoles: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("org roles: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	orgID := store.ResourceID("gcp", sc.Name, sc.Name)
	poolID := store.ResourceID("gcp", sc.Name, poolName)
	providerID := store.ResourceID("gcp", sc.Name, providerName)
	tenantID := store.ResourceID("gcp", sc.Name, tenantName)
	roleID := store.ResourceID("gcp", sc.Name, "organizations/123/roles/custom1")

	assertChildOf(t, st, poolID, orgID)
	assertChildOf(t, st, providerID, poolID)
	assertChildOf(t, st, tenantID, providerID)
	assertChildOf(t, st, roleID, orgID)
}

func TestScanIAMWorkloadTree_FullFanout(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	poolName := "projects/my-project/locations/global/workloadIdentityPools/pool1"
	providerName := poolName + "/providers/prov1"
	nsName := poolName + "/namespaces/ns1"
	miName := nsName + "/managedIdentities/mi1"
	oauthClientName := "projects/my-project/locations/global/oauthClients/client1"
	credName := oauthClientName + "/credentials/cred1"

	routes := map[string]string{
		"/v1/projects/my-project/locations/global/workloadIdentityPools": marshalAttrs(t, iam.ListWorkloadIdentityPoolsResponse{
			WorkloadIdentityPools: []*iam.WorkloadIdentityPool{{Name: poolName, DisplayName: "Pool 1"}},
		}),
		"/v1/" + poolName + "/providers": marshalAttrs(t, iam.ListWorkloadIdentityPoolProvidersResponse{
			WorkloadIdentityPoolProviders: []*iam.WorkloadIdentityPoolProvider{{Name: providerName, DisplayName: "GitHub"}},
		}),
		"/v1/" + poolName + "/namespaces": marshalAttrs(t, iam.ListWorkloadIdentityPoolNamespacesResponse{
			WorkloadIdentityPoolNamespaces: []*iam.WorkloadIdentityPoolNamespace{{Name: nsName}},
		}),
		"/v1/" + nsName + "/managedIdentities": marshalAttrs(t, iam.ListWorkloadIdentityPoolManagedIdentitiesResponse{
			WorkloadIdentityPoolManagedIdentities: []*iam.WorkloadIdentityPoolManagedIdentity{{Name: miName}},
		}),
		"/v1/projects/my-project/locations/global/oauthClients": marshalAttrs(t, iam.ListOauthClientsResponse{
			OauthClients: []*iam.OauthClient{{Name: oauthClientName, DisplayName: "Client 1"}},
		}),
		"/v1/" + oauthClientName + "/credentials": marshalAttrs(t, iam.ListOauthClientCredentialsResponse{
			OauthClientCredentials: []*iam.OauthClientCredential{{Name: credName, DisplayName: "Cred 1", ClientSecret: "s3cr3t"}},
		}),
		"/v1/projects/my-project/roles": marshalAttrs(t, iam.ListRolesResponse{
			Roles: []*iam.Role{{Name: "projects/my-project/roles/custom1", Title: "Custom Project Role"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeIAMService(t, srv)

	total, inserted, poolIDs, err := scanIAMWorkloadIdentityPools(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMWorkloadIdentityPools: %v", err)
	}
	if total != 1 || inserted != 1 || len(poolIDs) != 1 {
		t.Fatalf("pools: got total=%d inserted=%d poolIDs=%v, want 1/1/[%s]", total, inserted, poolIDs, poolName)
	}

	total, inserted, err = scanIAMWorkloadIdentityProviders(t.Context(), svc, p, poolIDs, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMWorkloadIdentityProviders: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("providers: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	total, inserted, nsIDs, err := scanIAMWorkloadIdentityNamespaces(t.Context(), svc, p, poolIDs, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMWorkloadIdentityNamespaces: %v", err)
	}
	if total != 1 || inserted != 1 || len(nsIDs) != 1 {
		t.Fatalf("namespaces: got total=%d inserted=%d nsIDs=%v, want 1/1/[%s]", total, inserted, nsIDs, nsName)
	}

	total, inserted, err = scanIAMWorkloadIdentityManagedIdentities(t.Context(), svc, p, nsIDs, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMWorkloadIdentityManagedIdentities: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("managed identities: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	total, inserted, clientIDs, err := scanIAMOauthClients(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMOauthClients: %v", err)
	}
	if total != 1 || inserted != 1 || len(clientIDs) != 1 {
		t.Fatalf("oauth clients: got total=%d inserted=%d clientIDs=%v, want 1/1/[%s]", total, inserted, clientIDs, oauthClientName)
	}

	total, inserted, err = scanIAMOauthClientCredentials(t.Context(), svc, p, clientIDs, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMOauthClientCredentials: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("oauth client credentials: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	total, inserted, err = scanIAMProjectRoles(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMProjectRoles: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("project roles: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	// Project-parented assertions (pool/client/role → project) are skipped:
	// newTestProject only returns a struct, it doesn't insert a Project
	// resource row, and RecordHierarchyBatch silently skips the depth-1
	// `contains` row when the parent endpoint doesn't exist in `resources`
	// (see store/CLAUDE.md — same behavior observed in the DNS Wave 8e and
	// SQL Wave 8d tests, which don't assert project-level parentage either).
	poolID := store.ResourceID("gcp", p.ID, poolName)
	providerID := store.ResourceID("gcp", p.ID, providerName)
	nsID := store.ResourceID("gcp", p.ID, nsName)
	miID := store.ResourceID("gcp", p.ID, miName)
	clientID := store.ResourceID("gcp", p.ID, oauthClientName)
	credID := store.ResourceID("gcp", p.ID, credName)

	assertChildOf(t, st, providerID, poolID)
	assertChildOf(t, st, nsID, poolID)
	assertChildOf(t, st, miID, nsID)
	assertChildOf(t, st, credID, clientID)
}

// TestScanIAMWorkforceProviders_PartialDenyContinues denies exactly one
// pool's Providers.List call while a sibling pool's succeeds, guarding
// against a regression where one 403'd pool aborts the whole fan-out
// instead of just skipping that pool.
func TestScanIAMWorkforceProviders_PartialDenyContinues(t *testing.T) {
	st := newTestStore(t)
	sc := orgScope{Kind: "organization", Name: "organizations/123", Resource: store.ResourceID("gcp", "organizations/123", "organizations/123")}
	upsertTestResource(t, st, "gcp", sc.Name, TypeOrganization, sc.Name, "", "{}")

	pool1 := "locations/global/workforcePools/pool1"
	pool2 := "locations/global/workforcePools/pool2"
	upsertTestResource(t, st, "gcp", sc.Name, TypeIAMWorkforcePool, pool1, "", "{}")
	upsertTestResource(t, st, "gcp", sc.Name, TypeIAMWorkforcePool, pool2, "", "{}")
	provider2 := pool2 + "/providers/prov2"

	deniedBody := `{"error":{"code":403,"message":"caller is missing iam.workforcePoolProviders.list","errors":[{"reason":"forbidden"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/" + pool1 + "/providers":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v1/" + pool2 + "/providers":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(marshalAttrs(t, iam.ListWorkforcePoolProvidersResponse{
				WorkforcePoolProviders: []*iam.WorkforcePoolProvider{{Name: provider2, DisplayName: "Azure AD"}},
			})))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"error":{"code":404,"message":"no fake route"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeIAMService(t, srv)

	total, inserted, providerIDs, err := scanIAMWorkforceProviders(t.Context(), svc, sc, []string{pool1, pool2}, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMWorkforceProviders: %v", err)
	}
	if total != 1 || inserted != 1 || len(providerIDs) != 1 {
		t.Fatalf("counts: got total=%d inserted=%d providerIDs=%v, want 1/1/[%s] (pool1 denied, pool2 has one provider)", total, inserted, providerIDs, provider2)
	}

	pool2ID := store.ResourceID("gcp", sc.Name, pool2)
	provider2ID := store.ResourceID("gcp", sc.Name, provider2)
	assertChildOf(t, st, provider2ID, pool2ID)
}

func TestScanIAMWorkforcePools_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	sc := orgScope{Kind: "organization", Name: "organizations/123", Resource: store.ResourceID("gcp", "organizations/123", "organizations/123")}

	body := `{"error":{"code":403,"message":"caller is missing iam.workforcePools.list","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeIAMService(t, srv)

	total, inserted, poolIDs, err := scanIAMWorkforcePools(t.Context(), svc, sc, st, testScanID)
	if err != nil {
		t.Fatalf("scanIAMWorkforcePools (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 || len(poolIDs) != 0 {
		t.Fatalf("counts: got total=%d inserted=%d poolIDs=%v, want 0/0/[]", total, inserted, poolIDs)
	}
}
