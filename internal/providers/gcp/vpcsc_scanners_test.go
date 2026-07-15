package gcp

import (
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/accesscontextmanager/v1"
	"google.golang.org/api/option"
)

// fakeACMService builds a *accesscontextmanager.Service pointed at the fake
// server. accesscontextmanager's BasePath carries no version segment, so
// every route template embeds "v1/" itself — route keys below are "/v1/...".
func fakeACMService(t *testing.T, srv *httptest.Server) *accesscontextmanager.Service {
	t.Helper()
	svc, err := accesscontextmanager.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("accesscontextmanager.NewService: %v", err)
	}
	return svc
}

func TestScanVPCSCForOrg_NestedFanout(t *testing.T) {
	st := newTestStore(t)
	sc := orgScope{Kind: "organization", Name: "organizations/123", Resource: store.ResourceID("gcp", "organizations/123", "organizations/123")}
	upsertTestResource(t, st, "gcp", sc.Name, TypeOrganization, sc.Name, "", "{}")

	policyName := "accessPolicies/1"
	routes := map[string]string{
		"/v1/accessPolicies": marshalAttrs(t, accesscontextmanager.ListAccessPoliciesResponse{
			AccessPolicies: []*accesscontextmanager.AccessPolicy{{Name: policyName, Title: "corp"}},
		}),
		"/v1/" + policyName + "/servicePerimeters": marshalAttrs(t, accesscontextmanager.ListServicePerimetersResponse{
			ServicePerimeters: []*accesscontextmanager.ServicePerimeter{{Name: policyName + "/servicePerimeters/sp1", Title: "perimeter"}},
		}),
		"/v1/" + policyName + "/accessLevels": marshalAttrs(t, accesscontextmanager.ListAccessLevelsResponse{
			AccessLevels: []*accesscontextmanager.AccessLevel{{Name: policyName + "/accessLevels/al1", Title: "trusted"}},
		}),
		"/v1/" + policyName + "/authorizedOrgsDescs": marshalAttrs(t, accesscontextmanager.ListAuthorizedOrgsDescsResponse{
			AuthorizedOrgsDescs: []*accesscontextmanager.AuthorizedOrgsDesc{{Name: policyName + "/authorizedOrgsDescs/aod1"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeACMService(t, srv)

	total, inserted, err := scanVPCSCForOrg(t.Context(), svc, sc, st, testScanID)
	if err != nil {
		t.Fatalf("scanVPCSCForOrg: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Fatalf("counts: got total=%d inserted=%d, want 4/4 (policy+perimeter+accesslevel+authorizedorgsdesc)", total, inserted)
	}

	policyID := store.ResourceID("gcp", sc.Name, policyName)
	assertParent := func(childType, childNative string) {
		t.Helper()
		childID := store.ResourceID("gcp", sc.Name, childNative)
		rels, err := st.RelationshipsFrom(policyID, store.RelContains)
		if err != nil {
			t.Fatalf("RelationshipsFrom(policy): %v", err)
		}
		for _, r := range rels {
			if r.ToID == childID {
				return
			}
		}
		t.Errorf("%s (%s) not found as child of policy; got %+v", childType, childNative, rels)
	}
	assertParent(TypeServicePerimeter, policyName+"/servicePerimeters/sp1")
	assertParent(TypeAccessLevel, policyName+"/accessLevels/al1")
	assertParent(TypeAuthorizedOrgsDesc, policyName+"/authorizedOrgsDescs/aod1")

	orgRels, err := st.RelationshipsFrom(sc.Resource, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(org): %v", err)
	}
	found := false
	for _, r := range orgRels {
		if r.ToID == policyID {
			found = true
		}
	}
	if !found {
		t.Errorf("policy not found as child of org; got %+v", orgRels)
	}
}

func TestScanGcpUserAccessBindingsForOrg(t *testing.T) {
	st := newTestStore(t)
	sc := orgScope{Kind: "organization", Name: "organizations/456", Resource: store.ResourceID("gcp", "organizations/456", "organizations/456")}
	upsertTestResource(t, st, "gcp", sc.Name, TypeOrganization, sc.Name, "", "{}")

	bindingName := "organizations/456/gcpUserAccessBindings/b1"
	routes := map[string]string{
		"/v1/" + sc.Name + "/gcpUserAccessBindings": marshalAttrs(t, accesscontextmanager.ListGcpUserAccessBindingsResponse{
			GcpUserAccessBindings: []*accesscontextmanager.GcpUserAccessBinding{{Name: bindingName, GroupKey: "01d520gv"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeACMService(t, srv)

	total, inserted, err := scanGcpUserAccessBindingsForOrg(t.Context(), svc, sc, st, testScanID)
	if err != nil {
		t.Fatalf("scanGcpUserAccessBindingsForOrg: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	bindingID := store.ResourceID("gcp", sc.Name, bindingName)
	rels, err := st.RelationshipsFrom(sc.Resource, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(org): %v", err)
	}
	for _, r := range rels {
		if r.ToID == bindingID {
			return
		}
	}
	t.Errorf("binding not found as child of org; got %+v", rels)
}

func TestScanVPCSCForOrg_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	sc := orgScope{Kind: "organization", Name: "organizations/789", Resource: store.ResourceID("gcp", "organizations/789", "organizations/789")}

	body := `{"error":{"code":403,"message":"caller is missing accesscontextmanager permission","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, 403, body)
	svc := fakeACMService(t, srv)

	total, inserted, err := scanVPCSCForOrg(t.Context(), svc, sc, st, testScanID)
	if err != nil {
		t.Fatalf("scanVPCSCForOrg (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
