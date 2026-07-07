package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/option"
	"google.golang.org/api/secretmanager/v1"
)

func fakeSecretManagerService(t *testing.T, srv *httptest.Server) *secretmanager.Service {
	t.Helper()
	svc, err := secretmanager.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("secretmanager.NewService: %v", err)
	}
	return svc
}

func TestScanSecrets_SecretVersionChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	secretName := "projects/proj1/secrets/secret1"
	versionName := secretName + "/versions/1"

	routes := map[string]string{
		"/v1/projects/proj1/secrets": marshalAttrs(t, secretmanager.ListSecretsResponse{
			Secrets: []*secretmanager.Secret{{Name: secretName}},
		}),
		"/v1/" + secretName + "/versions": marshalAttrs(t, secretmanager.ListSecretVersionsResponse{
			Versions: []*secretmanager.SecretVersion{{Name: versionName, State: "ENABLED"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeSecretManagerService(t, srv)

	total, inserted, err := scanSecretsWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanSecretsWithClient: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	secretID := store.ResourceID("gcp", p.ID, TypeSecret, secretName)
	rels, err := st.RelationshipsFrom(secretID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(secret): %v", err)
	}
	if len(rels) == 0 {
		t.Errorf("expected secret to contain the version row via hierarchy closure, got none")
	}
}

func TestScanSecrets_VersionsAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	secretName := "projects/proj1/secrets/secret1"
	notEnabledBody := `{"error":{"code":403,"message":"Secret Manager API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/proj1/secrets":
			_, _ = w.Write([]byte(marshalAttrs(t, secretmanager.ListSecretsResponse{
				Secrets: []*secretmanager.Secret{{Name: secretName}},
			})))
		case "/v1/" + secretName + "/versions":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeSecretManagerService(t, srv)

	total, inserted, err := scanSecretsWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanSecretsWithClient: %v (Versions' isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel)", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanSecrets_EmptyProjectNoSecrets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1/projects/proj1/secrets": marshalAttrs(t, secretmanager.ListSecretsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeSecretManagerService(t, srv)

	total, inserted, err := scanSecretsWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanSecretsWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
