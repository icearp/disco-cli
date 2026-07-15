package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/cloudkms/v1"
	"google.golang.org/api/option"
)

// fakeKMSService builds a *cloudkms.Service pointed at the fake server.
// cloudkms's BasePath carries no version segment (unlike compute), so every
// route template embeds "v1/" itself — route keys below are "/v1/...".
func fakeKMSService(t *testing.T, srv *httptest.Server) *cloudkms.Service {
	t.Helper()
	svc, err := cloudkms.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("cloudkms.NewService: %v", err)
	}
	return svc
}

// TestScanCloudKMS_FullNestedFanout exercises the whole KMS pipeline added in
// Wave 8: per-location EkmConnection/KeyHandle/SingleTenantHsmInstance
// siblings alongside KeyRings, per-keyring ImportJob alongside CryptoKeys,
// and per-crypto-key CryptoKeyVersion — plus the closure parent derivation
// for each new type.
func TestScanCloudKMS_FullNestedFanout(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	// RecordHierarchyBatch only writes a `contains` edge when both endpoints
	// already exist as resource rows — seed the project so the
	// keyring/ekm/keyHandle/hsm → project edges aren't silently dropped.
	upsertTestResource(t, st, "gcp", p.ID, TypeProject, p.ID, "", "{}")

	locName := "projects/my-project/locations/us-central1"
	krName := locName + "/keyRings/kr1"
	ckName := krName + "/cryptoKeys/ck1"
	ekmName := locName + "/ekmConnections/ekm1"
	khName := locName + "/keyHandles/kh1"
	hsmName := locName + "/singleTenantHsmInstances/hsm1"
	ijName := krName + "/importJobs/ij1"
	cvName := ckName + "/cryptoKeyVersions/1"

	routes := map[string]string{
		"/v1/projects/my-project/locations": marshalAttrs(t, cloudkms.ListLocationsResponse{
			Locations: []*cloudkms.Location{{Name: locName}},
		}),
		"/v1/" + locName + "/ekmConnections": marshalAttrs(t, cloudkms.ListEkmConnectionsResponse{
			EkmConnections: []*cloudkms.EkmConnection{{Name: ekmName}},
		}),
		"/v1/" + locName + "/keyHandles": marshalAttrs(t, cloudkms.ListKeyHandlesResponse{
			KeyHandles: []*cloudkms.KeyHandle{{Name: khName}},
		}),
		"/v1/" + locName + "/singleTenantHsmInstances": marshalAttrs(t, cloudkms.ListSingleTenantHsmInstancesResponse{
			SingleTenantHsmInstances: []*cloudkms.SingleTenantHsmInstance{{Name: hsmName}},
		}),
		"/v1/" + locName + "/keyRings": marshalAttrs(t, cloudkms.ListKeyRingsResponse{
			KeyRings: []*cloudkms.KeyRing{{Name: krName}},
		}),
		"/v1/" + krName + "/importJobs": marshalAttrs(t, cloudkms.ListImportJobsResponse{
			ImportJobs: []*cloudkms.ImportJob{{Name: ijName}},
		}),
		"/v1/" + krName + "/cryptoKeys": marshalAttrs(t, cloudkms.ListCryptoKeysResponse{
			CryptoKeys: []*cloudkms.CryptoKey{{Name: ckName}},
		}),
		"/v1/" + ckName + "/cryptoKeyVersions": marshalAttrs(t, cloudkms.ListCryptoKeyVersionsResponse{
			CryptoKeyVersions: []*cloudkms.CryptoKeyVersion{{Name: cvName}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeKMSService(t, srv)

	total, inserted, err := scanCloudKMSWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudKMSWithClient: %v", err)
	}
	if total != 7 || inserted != 7 {
		t.Fatalf("counts: got total=%d inserted=%d, want 7/7 (keyring+ekm+keyhandle+hsm+importjob+cryptokey+cryptokeyversion)", total, inserted)
	}

	for _, tc := range []struct {
		typ, native string
	}{
		{TypeKMSKeyRing, krName},
		{TypeKMSCryptoKey, ckName},
		{TypeKMSCryptoKeyVersion, cvName},
		{TypeKMSEkmConnection, ekmName},
		{TypeKMSImportJob, ijName},
		{TypeKMSKeyHandle, khName},
		{TypeKMSSingleTenantHsmInstance, hsmName},
	} {
		if _, err := st.GetResource(store.ResourceID("gcp", p.ID, tc.native)); err != nil {
			t.Errorf("GetResource(%s): %v", tc.typ, err)
		}
	}

	projID := store.ResourceID("gcp", p.ID, p.ID)
	krID := store.ResourceID("gcp", p.ID, krName)
	ckID := store.ResourceID("gcp", p.ID, ckName)

	assertParent := func(childType, childNative, wantParentID string) {
		t.Helper()
		childID := store.ResourceID("gcp", p.ID, childNative)
		rels, err := st.RelationshipsFrom(wantParentID, store.RelContains)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", wantParentID, err)
		}
		for _, r := range rels {
			if r.ToID == childID {
				return
			}
		}
		t.Errorf("%s (%s) not found as a child of %s; got %+v", childType, childNative, wantParentID, rels)
	}

	assertParent(TypeKMSKeyRing, krName, projID)
	assertParent(TypeKMSEkmConnection, ekmName, projID)
	assertParent(TypeKMSKeyHandle, khName, projID)
	assertParent(TypeKMSSingleTenantHsmInstance, hsmName, projID)
	assertParent(TypeKMSImportJob, ijName, krID)
	assertParent(TypeKMSCryptoKey, ckName, krID)
	assertParent(TypeKMSCryptoKeyVersion, cvName, ckID)
}

func TestScanCloudKMS_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	body := `{"error":{"code":403,"message":"caller is missing cloudkms.keyRings.list","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeKMSService(t, srv)

	total, inserted, err := scanCloudKMSWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudKMSWithClient (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

// TestScanCloudKMS_PartialDenyContinues denies exactly one sibling call
// (EkmConnections.List) while every other route succeeds, guarding against a
// regression where one 403'd sibling call aborts the whole location's
// processing instead of just skipping that one resource type. A uniform
// fakeGCPServerStatus 403 (as in TestScanCloudKMS_PermissionDenied) can't
// distinguish "deny-then-continue" from "deny-then-abort" since every route
// fails identically either way.
func TestScanCloudKMS_PartialDenyContinues(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	locName := "projects/my-project/locations/us-central1"
	krName := locName + "/keyRings/kr1"

	deniedBody := `{"error":{"code":403,"message":"caller is missing cloudkms.ekmConnections.list","errors":[{"reason":"forbidden"}]}}`
	routes := map[string]string{
		"/v1/projects/my-project/locations": marshalAttrs(t, cloudkms.ListLocationsResponse{
			Locations: []*cloudkms.Location{{Name: locName}},
		}),
		"/v1/" + locName + "/keyHandles":               marshalAttrs(t, cloudkms.ListKeyHandlesResponse{}),
		"/v1/" + locName + "/singleTenantHsmInstances": marshalAttrs(t, cloudkms.ListSingleTenantHsmInstancesResponse{}),
		"/v1/" + locName + "/keyRings": marshalAttrs(t, cloudkms.ListKeyRingsResponse{
			KeyRings: []*cloudkms.KeyRing{{Name: krName}},
		}),
		"/v1/" + krName + "/importJobs": marshalAttrs(t, cloudkms.ListImportJobsResponse{}),
		"/v1/" + krName + "/cryptoKeys": marshalAttrs(t, cloudkms.ListCryptoKeysResponse{}),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/"+locName+"/ekmConnections" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"error":{"code":404,"message":"no fake route"}}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	svc := fakeKMSService(t, srv)

	total, inserted, err := scanCloudKMSWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudKMSWithClient: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (keyring only — ekm denied, everything else empty)", total, inserted)
	}
	if _, err := st.GetResource(store.ResourceID("gcp", p.ID, krName)); err != nil {
		t.Errorf("GetResource(keyring): %v — keyRings.List should still run after ekmConnections.list is denied", err)
	}
}
