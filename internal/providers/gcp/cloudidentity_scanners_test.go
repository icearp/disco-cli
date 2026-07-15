package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/cloudidentity/v1"
	"google.golang.org/api/option"
)

const testCustomerID = "C0123abc"

// fakeCloudIdentityService builds a *cloudidentity.Service pointed at the
// fake server. Route templates use the bare "v1/..." prefix (BasePath has no
// version segment) — same shape as cloudkms/cloudresourcemanager/
// accesscontextmanager/sqladmin, unlike dns's "dns/v1/..." full prefix.
func fakeCloudIdentityService(t *testing.T, srv *httptest.Server) *cloudidentity.Service {
	t.Helper()
	svc, err := cloudidentity.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("cloudidentity.NewService: %v", err)
	}
	return svc
}

func TestScanCloudIdentityGroups_ReturnsGroupNames(t *testing.T) {
	st := newTestStore(t)

	routes := map[string]string{
		"/v1/groups": marshalAttrs(t, cloudidentity.ListGroupsResponse{
			Groups: []*cloudidentity.Group{
				{Name: "groups/g1", DisplayName: "Engineering", GroupKey: &cloudidentity.EntityKey{Id: "eng@example.com"}},
			},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeCloudIdentityService(t, srv)

	groupNames, total, inserted, err := scanCloudIdentityGroups(t.Context(), svc, testCustomerID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityGroups: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	if len(groupNames) != 1 || groupNames[0] != "groups/g1" {
		t.Fatalf("groupNames = %v, want [groups/g1]", groupNames)
	}
}

func TestScanCloudIdentityDeviceTree_FullFanout(t *testing.T) {
	st := newTestStore(t)

	deviceID := store.ResourceID("gcp", testCustomerID, "devices/d1")
	deviceUserID := store.ResourceID("gcp", testCustomerID, "devices/d1/deviceUsers/u1")

	routes := map[string]string{
		"/v1/devices": marshalAttrs(t, cloudidentity.GoogleAppsCloudidentityDevicesV1ListDevicesResponse{
			Devices: []*cloudidentity.GoogleAppsCloudidentityDevicesV1Device{
				{Name: "devices/d1", Model: "Pixel 9"},
			},
		}),
		"/v1/devices/-/deviceUsers": marshalAttrs(t, cloudidentity.GoogleAppsCloudidentityDevicesV1ListDeviceUsersResponse{
			DeviceUsers: []*cloudidentity.GoogleAppsCloudidentityDevicesV1DeviceUser{
				{Name: "devices/d1/deviceUsers/u1", UserEmail: "alice@example.com"},
			},
		}),
		"/v1/devices/-/deviceUsers/-/clientStates": marshalAttrs(t, cloudidentity.GoogleAppsCloudidentityDevicesV1ListClientStatesResponse{
			ClientStates: []*cloudidentity.GoogleAppsCloudidentityDevicesV1ClientState{
				{Name: "devices/d1/deviceUsers/u1/clientStates/c1", OwnerType: "OWNER_TYPE_CUSTOMER"},
			},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeCloudIdentityService(t, srv)

	total, inserted, err := scanCloudIdentityDevices(t.Context(), svc, testCustomerID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityDevices: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("devices counts: got %d/%d, want 1/1", total, inserted)
	}

	total, inserted, err = scanCloudIdentityDeviceUsers(t.Context(), svc, testCustomerID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityDeviceUsers: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("device users counts: got %d/%d, want 1/1", total, inserted)
	}

	total, inserted, err = scanCloudIdentityClientStates(t.Context(), svc, testCustomerID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityClientStates: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("client states counts: got %d/%d, want 1/1", total, inserted)
	}

	clientStateID := store.ResourceID("gcp", testCustomerID, "devices/d1/deviceUsers/u1/clientStates/c1")

	assertChildOf(t, st, deviceUserID, deviceID)
	assertChildOf(t, st, clientStateID, deviceUserID)
}

// assertChildOf asserts childID appears as a `contains` child of parentID.
func assertChildOf(t *testing.T, st *store.Store, childID, parentID string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(parentID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", parentID, err)
	}
	for _, r := range rels {
		if r.ToID == childID {
			return
		}
	}
	t.Errorf("%s not found as child of %s; got %+v", childID, parentID, rels)
}

func TestScanCloudIdentityMemberships_PartialDenyContinues(t *testing.T) {
	st := newTestStore(t)

	// RecordHierarchyBatch only writes the depth-1 `contains` row when both
	// endpoints already exist in `resources` — seed the two groups the
	// production scanCloudIdentityGroups phase would have inserted first.
	group1ID := upsertTestResource(t, st, "gcp", testCustomerID, TypeCloudIdentityGroup, "groups/g1", "", "{}")
	group2ID := upsertTestResource(t, st, "gcp", testCustomerID, TypeCloudIdentityGroup, "groups/g2", "", "{}")
	membershipID := store.ResourceID("gcp", testCustomerID, "groups/g2/memberships/m1")

	deniedBody := `{"error":{"code":403,"message":"caller is missing cloudidentity.groups.memberships.list","errors":[{"reason":"forbidden"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/groups/g1/memberships":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v1/groups/g2/memberships":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(marshalAttrs(t, cloudidentity.ListMembershipsResponse{
				Memberships: []*cloudidentity.Membership{
					{Name: "groups/g2/memberships/m1", PreferredMemberKey: &cloudidentity.EntityKey{Id: "bob@example.com"}},
				},
			})))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"error":{"code":404,"message":"no fake route"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeCloudIdentityService(t, srv)

	total, inserted, err := scanCloudIdentityMemberships(t.Context(), svc, testCustomerID, []string{"groups/g1", "groups/g2"}, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityMemberships: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (g1 denied, g2 has one membership)", total, inserted)
	}
	assertChildOf(t, st, membershipID, group2ID)

	// g1 denied — no membership rows exist under it.
	rels, err := st.RelationshipsFrom(group1ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(group1): %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("group1 (denied) has children: %+v, want none", rels)
	}
}

func TestScanCloudIdentitySSO_FullFanout(t *testing.T) {
	st := newTestStore(t)

	profileID := store.ResourceID("gcp", testCustomerID, "inboundSamlSsoProfiles/p1")
	credID := store.ResourceID("gcp", testCustomerID, "inboundSamlSsoProfiles/p1/idpCredentials/cred1")

	routes := map[string]string{
		"/v1/inboundOidcSsoProfiles": marshalAttrs(t, cloudidentity.ListInboundOidcSsoProfilesResponse{
			InboundOidcSsoProfiles: []*cloudidentity.InboundOidcSsoProfile{
				{Name: "inboundOidcSsoProfiles/o1", DisplayName: "Okta OIDC"},
			},
		}),
		"/v1/inboundSamlSsoProfiles": marshalAttrs(t, cloudidentity.ListInboundSamlSsoProfilesResponse{
			InboundSamlSsoProfiles: []*cloudidentity.InboundSamlSsoProfile{
				{Name: "inboundSamlSsoProfiles/p1", DisplayName: "Okta SAML"},
			},
		}),
		"/v1/inboundSamlSsoProfiles/p1/idpCredentials": marshalAttrs(t, cloudidentity.ListIdpCredentialsResponse{
			IdpCredentials: []*cloudidentity.IdpCredential{
				{Name: "inboundSamlSsoProfiles/p1/idpCredentials/cred1"},
			},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeCloudIdentityService(t, srv)

	total, inserted, err := scanCloudIdentityInboundOidcSsoProfiles(t.Context(), svc, testCustomerID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityInboundOidcSsoProfiles: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("oidc counts: got %d/%d, want 1/1", total, inserted)
	}

	profileNames, total, inserted, err := scanCloudIdentityInboundSamlSsoProfiles(t.Context(), svc, testCustomerID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityInboundSamlSsoProfiles: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("saml counts: got %d/%d, want 1/1", total, inserted)
	}
	if len(profileNames) != 1 || profileNames[0] != "inboundSamlSsoProfiles/p1" {
		t.Fatalf("profileNames = %v, want [inboundSamlSsoProfiles/p1]", profileNames)
	}

	total, inserted, err = scanCloudIdentityIdpCredentials(t.Context(), svc, testCustomerID, profileNames, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityIdpCredentials: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("idp credential counts: got %d/%d, want 1/1", total, inserted)
	}
	assertChildOf(t, st, credID, profileID)
}

func TestScanCloudIdentityFlatPhases_Basic(t *testing.T) {
	st := newTestStore(t)

	routes := map[string]string{
		"/v1/inboundSsoAssignments": marshalAttrs(t, cloudidentity.ListInboundSsoAssignmentsResponse{
			InboundSsoAssignments: []*cloudidentity.InboundSsoAssignment{
				{Name: "inboundSsoAssignments/a1", SsoMode: "SAML_SSO"},
			},
		}),
		"/v1/policies": marshalAttrs(t, cloudidentity.ListPoliciesResponse{
			Policies: []*cloudidentity.Policy{
				{Name: "policies/pol1", Type: "ADMIN"},
			},
		}),
		"/v1/customers/" + testCustomerID + "/userinvitations": marshalAttrs(t, cloudidentity.ListUserInvitationsResponse{
			UserInvitations: []*cloudidentity.UserInvitation{
				{Name: "customers/" + testCustomerID + "/userinvitations/carol@example.com", State: "INVITED"},
			},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeCloudIdentityService(t, srv)

	total, inserted, err := scanCloudIdentityInboundSsoAssignments(t.Context(), svc, testCustomerID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityInboundSsoAssignments: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("assignments counts: got %d/%d, want 1/1", total, inserted)
	}

	total, inserted, err = scanCloudIdentityPolicies(t.Context(), svc, testCustomerID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityPolicies: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("policies counts: got %d/%d, want 1/1", total, inserted)
	}

	total, inserted, err = scanCloudIdentityUserinvitations(t.Context(), svc, testCustomerID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityUserinvitations: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("userinvitations counts: got %d/%d, want 1/1", total, inserted)
	}
}

func TestScanCloudIdentityDevices_PermissionDenied(t *testing.T) {
	st := newTestStore(t)

	body := `{"error":{"code":403,"message":"caller is missing cloudidentity.devices.list","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeCloudIdentityService(t, srv)

	total, inserted, err := scanCloudIdentityDevices(t.Context(), svc, testCustomerID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudIdentityDevices (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
