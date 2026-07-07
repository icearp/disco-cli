package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	cloudbuild "google.golang.org/api/cloudbuild/v1"
	cloudbuildv2 "google.golang.org/api/cloudbuild/v2"
	"google.golang.org/api/option"
)

// fakeCloudBuildServices builds v1 and v2 *cloudbuild.Service clients both
// pointed at the same fake server. Both APIs share bare-hostname BasePaths
// (no embedded version segment), so route templates below carry "v1/"/"v2/"
// prefixes.
func fakeCloudBuildServices(t *testing.T, srv *httptest.Server) (*cloudbuild.Service, *cloudbuildv2.Service) {
	t.Helper()
	svc, err := cloudbuild.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("cloudbuild.NewService: %v", err)
	}
	svc2, err := cloudbuildv2.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("cloudbuildv2.NewService: %v", err)
	}
	return svc, svc2
}

func TestScanCloudBuild_FullChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	connName := "projects/proj1/locations/us-central1/connections/conn1"
	repoName := connName + "/repositories/repo1"
	wpName := "projects/proj1/locations/us-central1/workerPools/wp1"
	ghecName := "projects/proj1/locations/global/githubEnterpriseConfigs/ghec1"

	routes := map[string]string{
		"/v1/projects/proj1/triggers": marshalAttrs(t, cloudbuild.ListBuildTriggersResponse{
			Triggers: []*cloudbuild.BuildTrigger{{Id: "t1", Name: "trigger1", ResourceName: "projects/proj1/locations/global/triggers/t1"}},
		}),
		"/v2/projects/proj1/locations": marshalAttrs(t, cloudbuildv2.ListLocationsResponse{
			Locations: []*cloudbuildv2.Location{{LocationId: "us-central1", Name: "projects/proj1/locations/us-central1"}},
		}),
		"/v1/projects/proj1/locations/us-central1/workerPools": marshalAttrs(t, cloudbuild.ListWorkerPoolsResponse{
			WorkerPools: []*cloudbuild.WorkerPool{{Name: wpName, State: "RUNNING"}},
		}),
		"/v2/projects/proj1/locations/us-central1/connections": marshalAttrs(t, cloudbuildv2.ListConnectionsResponse{
			Connections: []*cloudbuildv2.Connection{{Name: connName}},
		}),
		"/v2/" + connName + "/repositories": marshalAttrs(t, cloudbuildv2.ListRepositoriesResponse{
			Repositories: []*cloudbuildv2.Repository{{Name: repoName, RemoteUri: "https://github.com/example/repo1.git"}},
		}),
		"/v1/projects/proj1/githubEnterpriseConfigs": marshalAttrs(t, cloudbuild.ListGithubEnterpriseConfigsResponse{
			Configs: []*cloudbuild.GitHubEnterpriseConfig{{Name: ghecName, DisplayName: "ghec1", HostUrl: "https://ghe.example.com"}},
		}),
		"/v1/projects/proj1/locations/us-central1/githubEnterpriseConfigs": marshalAttrs(t, cloudbuild.ListGithubEnterpriseConfigsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc, svc2 := fakeCloudBuildServices(t, srv)

	total, inserted, err := scanCloudBuildWithClient(t.Context(), svc, svc2, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudBuildWithClient: %v", err)
	}
	// 1 trigger + 1 worker pool + 1 connection + 1 repository + 1 GHE config
	// (the per-location GHE call in this test returns empty — same config,
	// no duplicate row).
	if total != 5 || inserted != 5 {
		t.Fatalf("counts: got total=%d inserted=%d, want 5/5", total, inserted)
	}

	for _, tc := range []struct {
		typ      string
		nativeID string
	}{
		{TypeCloudBuildWorkerPool, wpName},
		{TypeCloudBuildConnection, connName},
		{TypeCloudBuildRepository, repoName},
		{TypeCloudBuildGithubEnterpriseConfig, ghecName},
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

	wpID := store.ResourceID("gcp", p.ID, TypeCloudBuildWorkerPool, wpName)
	wpRes, err := st.GetResource(wpID)
	if err != nil {
		t.Fatalf("GetResource(worker pool): %v", err)
	}
	if wpRes.Region == nil || *wpRes.Region != "us-central1" {
		t.Errorf("worker pool region: got %+v, want us-central1", wpRes.Region)
	}
	if wpRes.Status == nil || *wpRes.Status != "RUNNING" {
		t.Errorf("worker pool status: got %+v, want RUNNING", wpRes.Status)
	}

	connID := store.ResourceID("gcp", p.ID, TypeCloudBuildConnection, connName)
	rels, err := st.RelationshipsFrom(connID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(connection): %v", err)
	}
	if len(rels) == 0 {
		t.Errorf("expected connection to contain the repository row via hierarchy closure, got none")
	}
}

func TestScanCloudBuild_WorkerPoolsPermissionDeniedContinuesToConnections(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	connName := "projects/proj1/locations/us-central1/connections/conn1"
	deniedBody := `{"error":{"code":403,"message":"caller does not have cloudbuild.workerpools.list access"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/proj1/triggers":
			_, _ = w.Write([]byte(`{}`))
		case "/v2/projects/proj1/locations":
			_, _ = w.Write([]byte(marshalAttrs(t, cloudbuildv2.ListLocationsResponse{
				Locations: []*cloudbuildv2.Location{{LocationId: "us-central1"}},
			})))
		case "/v1/projects/proj1/locations/us-central1/workerPools":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v2/projects/proj1/locations/us-central1/connections":
			_, _ = w.Write([]byte(marshalAttrs(t, cloudbuildv2.ListConnectionsResponse{
				Connections: []*cloudbuildv2.Connection{{Name: connName}},
			})))
		case "/v2/" + connName + "/repositories":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/projects/proj1/githubEnterpriseConfigs", "/v1/projects/proj1/locations/us-central1/githubEnterpriseConfigs":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc, svc2 := fakeCloudBuildServices(t, srv)

	total, inserted, err := scanCloudBuildWithClient(t.Context(), svc, svc2, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudBuildWithClient: %v", err)
	}
	// Only the connection lands; WorkerPools 403 must warn, not abort the
	// location's remaining Connections sub-phase.
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (worker pools 403 must not block connections)", total, inserted)
	}
}

func TestScanCloudBuild_ConnectionsAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	wpName := "projects/proj1/locations/us-central1/workerPools/wp1"
	notEnabledBody := `{"error":{"code":403,"message":"Cloud Build API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/proj1/triggers":
			_, _ = w.Write([]byte(`{}`))
		case "/v2/projects/proj1/locations":
			_, _ = w.Write([]byte(marshalAttrs(t, cloudbuildv2.ListLocationsResponse{
				Locations: []*cloudbuildv2.Location{{LocationId: "us-central1"}},
			})))
		case "/v1/projects/proj1/locations/us-central1/workerPools":
			_, _ = w.Write([]byte(marshalAttrs(t, cloudbuild.ListWorkerPoolsResponse{
				WorkerPools: []*cloudbuild.WorkerPool{{Name: wpName}},
			})))
		case "/v2/projects/proj1/locations/us-central1/connections":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		case "/v1/projects/proj1/githubEnterpriseConfigs", "/v1/projects/proj1/locations/us-central1/githubEnterpriseConfigs":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc, svc2 := fakeCloudBuildServices(t, srv)

	total, inserted, err := scanCloudBuildWithClient(t.Context(), svc, svc2, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudBuildWithClient: %v (Connections.List's isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel)", err)
	}
	// Only the worker pool lands; Connections' isAPINotEnabled-shaped 403
	// must warn-and-continue, not abort the scan.
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanCloudBuild_GHEConfigFoundOnlyAtLocationScope(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	ghecName := "projects/proj1/locations/us-central1/githubEnterpriseConfigs/ghec1"

	routes := map[string]string{
		"/v1/projects/proj1/triggers": marshalAttrs(t, cloudbuild.ListBuildTriggersResponse{}),
		"/v2/projects/proj1/locations": marshalAttrs(t, cloudbuildv2.ListLocationsResponse{
			Locations: []*cloudbuildv2.Location{{LocationId: "us-central1"}},
		}),
		"/v1/projects/proj1/locations/us-central1/workerPools": marshalAttrs(t, cloudbuild.ListWorkerPoolsResponse{}),
		"/v2/projects/proj1/locations/us-central1/connections": marshalAttrs(t, cloudbuildv2.ListConnectionsResponse{}),
		// Global GHE call returns nothing — the config only shows up via the
		// per-location parent, proving phase 5's location fan-out (not just
		// the legacy global call) is what discovers it.
		"/v1/projects/proj1/githubEnterpriseConfigs": marshalAttrs(t, cloudbuild.ListGithubEnterpriseConfigsResponse{}),
		"/v1/projects/proj1/locations/us-central1/githubEnterpriseConfigs": marshalAttrs(t, cloudbuild.ListGithubEnterpriseConfigsResponse{
			Configs: []*cloudbuild.GitHubEnterpriseConfig{{Name: ghecName, DisplayName: "ghec1"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc, svc2 := fakeCloudBuildServices(t, srv)

	total, inserted, err := scanCloudBuildWithClient(t.Context(), svc, svc2, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudBuildWithClient: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (regional GHE config must be found via the per-location parent, not just the global one)", total, inserted)
	}

	id := store.ResourceID("gcp", p.ID, TypeCloudBuildGithubEnterpriseConfig, ghecName)
	res, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res == nil {
		t.Fatalf("regional GHE config %s not stored", ghecName)
	}
}

func TestScanCloudBuild_GHEConfigsAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	wpName := "projects/proj1/locations/us-central1/workerPools/wp1"
	notEnabledBody := `{"error":{"code":403,"message":"Cloud Build API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/proj1/triggers":
			_, _ = w.Write([]byte(`{}`))
		case "/v2/projects/proj1/locations":
			_, _ = w.Write([]byte(marshalAttrs(t, cloudbuildv2.ListLocationsResponse{
				Locations: []*cloudbuildv2.Location{{LocationId: "us-central1"}},
			})))
		case "/v1/projects/proj1/locations/us-central1/workerPools":
			_, _ = w.Write([]byte(marshalAttrs(t, cloudbuild.ListWorkerPoolsResponse{
				WorkerPools: []*cloudbuild.WorkerPool{{Name: wpName}},
			})))
		case "/v2/projects/proj1/locations/us-central1/connections":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/projects/proj1/githubEnterpriseConfigs", "/v1/projects/proj1/locations/us-central1/githubEnterpriseConfigs":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc, svc2 := fakeCloudBuildServices(t, srv)

	total, inserted, err := scanCloudBuildWithClient(t.Context(), svc, svc2, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudBuildWithClient: %v (GithubEnterpriseConfigs' isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel)", err)
	}
	// Only the worker pool lands; both GHE config calls' isAPINotEnabled-
	// shaped 403 must warn-and-continue, not abort the scan.
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanCloudBuild_EmptyProjectNoLocations(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1/projects/proj1/triggers":                marshalAttrs(t, cloudbuild.ListBuildTriggersResponse{}),
		"/v2/projects/proj1/locations":               marshalAttrs(t, cloudbuildv2.ListLocationsResponse{}),
		"/v1/projects/proj1/githubEnterpriseConfigs": marshalAttrs(t, cloudbuild.ListGithubEnterpriseConfigsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc, svc2 := fakeCloudBuildServices(t, srv)

	total, inserted, err := scanCloudBuildWithClient(t.Context(), svc, svc2, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudBuildWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
