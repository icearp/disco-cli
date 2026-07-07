package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/option"
	runv1 "google.golang.org/api/run/v1"
	"google.golang.org/api/run/v2"
)

// fakeCloudRunServices builds v2 and v1 clients both pointed at the same
// fake server. Both APIs share bare-hostname BasePaths, so route templates
// below carry "v1/"/"v2/" prefixes.
func fakeCloudRunServices(t *testing.T, srv *httptest.Server) (*run.Service, *runv1.APIService) {
	t.Helper()
	svc, err := run.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("run.NewService: %v", err)
	}
	svc1, err := runv1.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("runv1.NewService: %v", err)
	}
	return svc, svc1
}

func TestScanCloudRun_FullChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	svcName := "projects/proj1/locations/us-central1/services/svc1"
	revName := svcName + "/revisions/svc1-00001"
	wpName := "projects/proj1/locations/us-central1/workerPools/wp1"
	instName := "projects/proj1/locations/us-central1/instances/inst1"
	dmName := "mydomain.example.com"

	routes := map[string]string{
		"/v2/projects/proj1/locations/-/services": marshalAttrs(t, run.GoogleCloudRunV2ListServicesResponse{
			Services: []*run.GoogleCloudRunV2Service{{Name: svcName}},
		}),
		"/v2/" + svcName + "/revisions": marshalAttrs(t, run.GoogleCloudRunV2ListRevisionsResponse{
			Revisions: []*run.GoogleCloudRunV2Revision{{Name: revName}},
		}),
		"/v2/projects/proj1/locations/us-central1/workerPools": marshalAttrs(t, run.GoogleCloudRunV2ListWorkerPoolsResponse{
			WorkerPools: []*run.GoogleCloudRunV2WorkerPool{{Name: wpName}},
		}),
		"/v2/projects/proj1/locations/us-central1/instances": marshalAttrs(t, run.GoogleCloudRunV2ListInstancesResponse{
			Instances: []*run.GoogleCloudRunV2Instance{{Name: instName}},
		}),
		"/v1/namespaces/proj1/domainmappings": marshalAttrs(t, runv1.ListDomainMappingsResponse{
			Items: []*runv1.DomainMapping{{Metadata: &runv1.ObjectMeta{Name: dmName}}},
		}),
		"/v1/projects/proj1/authorizeddomains": marshalAttrs(t, runv1.ListAuthorizedDomainsResponse{
			Domains: []*runv1.AuthorizedDomain{{Id: "example.com", Name: "projects/proj1/authorizedDomains/example.com"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc, svc1 := fakeCloudRunServices(t, srv)

	total, inserted, err := scanCloudRunWithClient(t.Context(), svc, svc1, []string{"us-central1"}, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudRunWithClient: %v", err)
	}
	// 1 service + 1 revision + 1 worker pool + 1 instance + 1 domain mapping + 1 authorized domain
	if total != 6 || inserted != 6 {
		t.Fatalf("counts: got total=%d inserted=%d, want 6/6", total, inserted)
	}

	for _, tc := range []struct {
		typ      string
		nativeID string
	}{
		{TypeCloudRunSvc, svcName},
		{TypeCloudRunRevision, revName},
		{TypeCloudRunWorkerPool, wpName},
		{TypeCloudRunInstance, instName},
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

	svcID := store.ResourceID("gcp", p.ID, TypeCloudRunSvc, svcName)
	rels, err := st.RelationshipsFrom(svcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(service): %v", err)
	}
	if len(rels) == 0 {
		t.Errorf("expected service to contain the revision row via hierarchy closure, got none")
	}
}

func TestScanCloudRun_RevisionsPermissionDeniedContinuesToWorkerPools(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	svcName := "projects/proj1/locations/us-central1/services/svc1"
	wpName := "projects/proj1/locations/us-central1/workerPools/wp1"
	deniedBody := `{"error":{"code":403,"message":"caller does not have run.revisions.list access"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/projects/proj1/locations/-/services":
			_, _ = w.Write([]byte(marshalAttrs(t, run.GoogleCloudRunV2ListServicesResponse{
				Services: []*run.GoogleCloudRunV2Service{{Name: svcName}},
			})))
		case "/v2/" + svcName + "/revisions":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v2/projects/proj1/locations/us-central1/workerPools":
			_, _ = w.Write([]byte(marshalAttrs(t, run.GoogleCloudRunV2ListWorkerPoolsResponse{
				WorkerPools: []*run.GoogleCloudRunV2WorkerPool{{Name: wpName}},
			})))
		case "/v2/projects/proj1/locations/us-central1/instances":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/namespaces/proj1/domainmappings", "/v1/projects/proj1/authorizeddomains":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc, svc1 := fakeCloudRunServices(t, srv)

	total, inserted, err := scanCloudRunWithClient(t.Context(), svc, svc1, []string{"us-central1"}, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudRunWithClient: %v", err)
	}
	// Service + worker pool land; Revisions' 403 must warn, not abort the
	// remaining phases.
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2 (revisions 403 must not block worker pools)", total, inserted)
	}
}

func TestScanCloudRun_WorkerPoolsAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	svcName := "projects/proj1/locations/us-central1/services/svc1"
	notEnabledBody := `{"error":{"code":403,"message":"Cloud Run Admin API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/projects/proj1/locations/-/services":
			_, _ = w.Write([]byte(marshalAttrs(t, run.GoogleCloudRunV2ListServicesResponse{
				Services: []*run.GoogleCloudRunV2Service{{Name: svcName}},
			})))
		case "/v2/" + svcName + "/revisions":
			_, _ = w.Write([]byte(`{}`))
		case "/v2/projects/proj1/locations/us-central1/workerPools":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		case "/v2/projects/proj1/locations/us-central1/instances":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/namespaces/proj1/domainmappings", "/v1/projects/proj1/authorizeddomains":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc, svc1 := fakeCloudRunServices(t, srv)

	total, inserted, err := scanCloudRunWithClient(t.Context(), svc, svc1, []string{"us-central1"}, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudRunWithClient: %v (WorkerPools' isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel)", err)
	}
	// Only the service lands; WorkerPools' isAPINotEnabled-shaped 403 must
	// warn-and-continue, not abort the scan.
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanCloudRun_AuthorizedDomainsAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	svcName := "projects/proj1/locations/us-central1/services/svc1"
	notEnabledBody := `{"error":{"code":403,"message":"Cloud Run Admin API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/projects/proj1/locations/-/services":
			_, _ = w.Write([]byte(marshalAttrs(t, run.GoogleCloudRunV2ListServicesResponse{
				Services: []*run.GoogleCloudRunV2Service{{Name: svcName}},
			})))
		case "/v2/" + svcName + "/revisions":
			_, _ = w.Write([]byte(`{}`))
		case "/v2/projects/proj1/locations/us-central1/workerPools":
			_, _ = w.Write([]byte(`{}`))
		case "/v2/projects/proj1/locations/us-central1/instances":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/namespaces/proj1/domainmappings":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/projects/proj1/authorizeddomains":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc, svc1 := fakeCloudRunServices(t, srv)

	total, inserted, err := scanCloudRunWithClient(t.Context(), svc, svc1, []string{"us-central1"}, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudRunWithClient: %v (AuthorizedDomains' isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel)", err)
	}
	// Only the service lands; AuthorizedDomains' isAPINotEnabled-shaped 403
	// must warn-and-continue, not abort the scan (this phase must classify
	// exactly once via a manual Pages() call — routing it through
	// runPaginated double-classifies and lets the sentinel escape undetected
	// as a plain error, escalating the whole call).
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanCloudRun_EmptyProjectNoServices(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v2/projects/proj1/locations/-/services": marshalAttrs(t, run.GoogleCloudRunV2ListServicesResponse{}),
		"/v1/namespaces/proj1/domainmappings":     marshalAttrs(t, runv1.ListDomainMappingsResponse{}),
		"/v1/projects/proj1/authorizeddomains":    marshalAttrs(t, runv1.ListAuthorizedDomainsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc, svc1 := fakeCloudRunServices(t, srv)

	// No regions injected — no fake project has enabled compute regions, and
	// this also proves the WorkerPool/Instance fan-out phase is a no-op on an
	// empty region list rather than panicking.
	total, inserted, err := scanCloudRunWithClient(t.Context(), svc, svc1, nil, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudRunWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
