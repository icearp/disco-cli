package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/certificatemanager/v1"
	"google.golang.org/api/option"
)

func fakeCertificateManagerService(t *testing.T, srv *httptest.Server) *certificatemanager.Service {
	t.Helper()
	svc, err := certificatemanager.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("certificatemanager.NewService: %v", err)
	}
	return svc
}

func TestScanCertificateManager_FullChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	certName := "projects/proj1/locations/global/certificates/cert1"
	mapName := "projects/proj1/locations/global/certificateMaps/map1"
	entryName := mapName + "/certificateMapEntries/entry1"
	dnsAuthName := "projects/proj1/locations/global/dnsAuthorizations/dnsauth1"
	issuanceName := "projects/proj1/locations/global/certificateIssuanceConfigs/ic1"
	trustName := "projects/proj1/locations/global/trustConfigs/tc1"

	routes := map[string]string{
		"/v1/projects/proj1/locations/global/certificates": marshalAttrs(t, certificatemanager.ListCertificatesResponse{
			Certificates: []*certificatemanager.Certificate{{Name: certName}},
		}),
		"/v1/projects/proj1/locations/global/certificateMaps": marshalAttrs(t, certificatemanager.ListCertificateMapsResponse{
			CertificateMaps: []*certificatemanager.CertificateMap{{Name: mapName}},
		}),
		"/v1/" + mapName + "/certificateMapEntries": marshalAttrs(t, certificatemanager.ListCertificateMapEntriesResponse{
			CertificateMapEntries: []*certificatemanager.CertificateMapEntry{{Name: entryName}},
		}),
		"/v1/projects/proj1/locations/global/dnsAuthorizations": marshalAttrs(t, certificatemanager.ListDnsAuthorizationsResponse{
			DnsAuthorizations: []*certificatemanager.DnsAuthorization{{Name: dnsAuthName}},
		}),
		"/v1/projects/proj1/locations/global/certificateIssuanceConfigs": marshalAttrs(t, certificatemanager.ListCertificateIssuanceConfigsResponse{
			CertificateIssuanceConfigs: []*certificatemanager.CertificateIssuanceConfig{{Name: issuanceName}},
		}),
		"/v1/projects/proj1/locations/global/trustConfigs": marshalAttrs(t, certificatemanager.ListTrustConfigsResponse{
			TrustConfigs: []*certificatemanager.TrustConfig{{Name: trustName}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeCertificateManagerService(t, srv)

	total, inserted, err := scanCertificateManagerWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCertificateManagerWithClient: %v", err)
	}
	// cert + map + entry + dnsAuth + issuanceConfig + trustConfig
	if total != 6 || inserted != 6 {
		t.Fatalf("counts: got total=%d inserted=%d, want 6/6", total, inserted)
	}

	for _, tc := range []struct {
		typ      string
		nativeID string
	}{
		{TypeCertManagerIssuanceConfig, issuanceName},
		{TypeCertManagerTrustConfig, trustName},
	} {
		id := store.ResourceID("gcp", p.ID, tc.typ, tc.nativeID)
		res, err := st.GetResource(id)
		if err != nil {
			t.Fatalf("GetResource(%s): %v", tc.typ, err)
		}
		if res == nil {
			t.Fatalf("%s %s not stored", tc.typ, tc.nativeID)
		}
	}
}

func TestScanCertificateManager_TrustConfigsAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	issuanceName := "projects/proj1/locations/global/certificateIssuanceConfigs/ic1"
	notEnabledBody := `{"error":{"code":403,"message":"Certificate Manager API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/proj1/locations/global/certificates":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/projects/proj1/locations/global/certificateMaps":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/projects/proj1/locations/global/dnsAuthorizations":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/projects/proj1/locations/global/certificateIssuanceConfigs":
			_, _ = w.Write([]byte(marshalAttrs(t, certificatemanager.ListCertificateIssuanceConfigsResponse{
				CertificateIssuanceConfigs: []*certificatemanager.CertificateIssuanceConfig{{Name: issuanceName}},
			})))
		case "/v1/projects/proj1/locations/global/trustConfigs":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeCertificateManagerService(t, srv)

	total, inserted, err := scanCertificateManagerWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCertificateManagerWithClient: %v (TrustConfigs' isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel)", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanCertificateManager_EmptyProject(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1/projects/proj1/locations/global/certificates":               marshalAttrs(t, certificatemanager.ListCertificatesResponse{}),
		"/v1/projects/proj1/locations/global/certificateMaps":            marshalAttrs(t, certificatemanager.ListCertificateMapsResponse{}),
		"/v1/projects/proj1/locations/global/dnsAuthorizations":          marshalAttrs(t, certificatemanager.ListDnsAuthorizationsResponse{}),
		"/v1/projects/proj1/locations/global/certificateIssuanceConfigs": marshalAttrs(t, certificatemanager.ListCertificateIssuanceConfigsResponse{}),
		"/v1/projects/proj1/locations/global/trustConfigs":               marshalAttrs(t, certificatemanager.ListTrustConfigsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeCertificateManagerService(t, srv)

	total, inserted, err := scanCertificateManagerWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCertificateManagerWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
