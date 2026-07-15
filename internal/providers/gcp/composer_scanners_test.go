package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/composer/v1"
	"google.golang.org/api/option"
)

func fakeComposerService(t *testing.T, srv *httptest.Server) *composer.Service {
	t.Helper()
	svc, err := composer.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("composer.NewService: %v", err)
	}
	return svc
}

func TestScanComposer_EnvironmentConfigMapChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	envName := "projects/proj1/locations/us-central1/environments/env1"
	cmName := envName + "/userWorkloadsConfigMaps/cm1"

	routes := map[string]string{
		"/v1/projects/proj1/locations/-/environments": marshalAttrs(t, composer.ListEnvironmentsResponse{
			Environments: []*composer.Environment{{Name: envName}},
		}),
		"/v1/" + envName + "/userWorkloadsConfigMaps": marshalAttrs(t, composer.ListUserWorkloadsConfigMapsResponse{
			UserWorkloadsConfigMaps: []*composer.UserWorkloadsConfigMap{{Name: cmName}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeComposerService(t, srv)

	total, inserted, err := scanComposerWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComposerWithClient: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	envID := store.ResourceID("gcp", p.ID, envName)
	rels, err := st.RelationshipsFrom(envID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(env): %v", err)
	}
	if len(rels) == 0 {
		t.Errorf("expected environment to contain the config map row via hierarchy closure, got none")
	}
}

func TestScanComposer_ConfigMapsAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	envName := "projects/proj1/locations/us-central1/environments/env1"
	notEnabledBody := `{"error":{"code":403,"message":"Cloud Composer API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/proj1/locations/-/environments":
			_, _ = w.Write([]byte(marshalAttrs(t, composer.ListEnvironmentsResponse{
				Environments: []*composer.Environment{{Name: envName}},
			})))
		case "/v1/" + envName + "/userWorkloadsConfigMaps":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeComposerService(t, srv)

	total, inserted, err := scanComposerWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComposerWithClient: %v (ConfigMaps' isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel)", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanComposer_EmptyProjectNoEnvironments(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1/projects/proj1/locations/-/environments": marshalAttrs(t, composer.ListEnvironmentsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeComposerService(t, srv)

	total, inserted, err := scanComposerWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanComposerWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
