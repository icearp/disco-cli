package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/option"
	"google.golang.org/api/sqladmin/v1"
)

// fakeSQLService builds a *sqladmin.Service pointed at the fake server.
// sqladmin's BasePath carries no version segment, so every route template
// embeds "v1/" itself — route keys below are "/v1/...".
func fakeSQLService(t *testing.T, srv *httptest.Server) *sqladmin.Service {
	t.Helper()
	svc, err := sqladmin.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("sqladmin.NewService: %v", err)
	}
	return svc
}

func TestScanCloudSQLInstanceChildren_FullFanout(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	instName := "inst1"
	instID := store.ResourceID("gcp", p.ID, "projects/my-project/instances/inst1")
	upsertTestResource(t, st, "gcp", p.ID, TypeSQLInstance, "projects/my-project/instances/inst1", "", "{}")

	base := "/v1/projects/my-project/instances/inst1"
	routes := map[string]string{
		base + "/backupRuns": marshalAttrs(t, sqladmin.BackupRunsListResponse{
			Items: []*sqladmin.BackupRun{{Id: 42, Status: "SUCCESSFUL", EnqueuedTime: "2026-01-01T00:00:00Z"}},
		}),
		base + "/databases": marshalAttrs(t, sqladmin.DatabasesListResponse{
			Items: []*sqladmin.Database{{Name: "appdb", Instance: instName}},
		}),
		base + "/sslCerts": marshalAttrs(t, sqladmin.SslCertsListResponse{
			Items: []*sqladmin.SslCert{{CommonName: "client1", Sha1Fingerprint: "abc123"}},
		}),
		base + "/users": marshalAttrs(t, sqladmin.UsersListResponse{
			Items: []*sqladmin.User{{Name: "appuser", Host: "%"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeSQLService(t, srv)

	total, inserted, err := scanCloudSQLInstanceChildren(t.Context(), svc, p, instName, instID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudSQLInstanceChildren: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Fatalf("counts: got total=%d inserted=%d, want 4/4 (backuprun+database+sslcert+user)", total, inserted)
	}

	wantChildren := []string{
		store.ResourceID("gcp", p.ID, "projects/my-project/instances/inst1/backupRuns/42"),
		store.ResourceID("gcp", p.ID, "projects/my-project/instances/inst1/databases/appdb"),
		store.ResourceID("gcp", p.ID, "projects/my-project/instances/inst1/sslCerts/abc123"),
		store.ResourceID("gcp", p.ID, "projects/my-project/instances/inst1/users/appuser@%"),
	}
	rels, err := st.RelationshipsFrom(instID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(instance): %v", err)
	}
	for _, want := range wantChildren {
		found := false
		for _, r := range rels {
			if r.ToID == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s not found as child of instance; got %+v", want, rels)
		}
	}
}

func TestScanCloudSQLInstanceChildren_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	instID := store.ResourceID("gcp", p.ID, "projects/my-project/instances/inst1")

	body := `{"error":{"code":403,"message":"caller is missing permission","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeSQLService(t, srv)

	total, inserted, err := scanCloudSQLInstanceChildren(t.Context(), svc, p, "inst1", instID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudSQLInstanceChildren (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

// TestScanCloudSQLInstanceChildren_PartialDenyContinues denies exactly one
// sibling call (BackupRuns.List) while every other route succeeds, guarding
// against a regression where one 403'd child type aborts the whole
// instance's remaining child scans instead of just skipping that one type.
func TestScanCloudSQLInstanceChildren_PartialDenyContinues(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	instName := "inst1"
	instID := store.ResourceID("gcp", p.ID, "projects/my-project/instances/inst1")

	base := "/v1/projects/my-project/instances/inst1"
	deniedBody := `{"error":{"code":403,"message":"caller is missing cloudsql.backupRuns.list","errors":[{"reason":"forbidden"}]}}`
	routes := map[string]string{
		base + "/databases": marshalAttrs(t, sqladmin.DatabasesListResponse{
			Items: []*sqladmin.Database{{Name: "appdb", Instance: instName}},
		}),
		base + "/sslCerts": marshalAttrs(t, sqladmin.SslCertsListResponse{}),
		base + "/users":    marshalAttrs(t, sqladmin.UsersListResponse{}),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == base+"/backupRuns" {
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
	svc := fakeSQLService(t, srv)

	total, inserted, err := scanCloudSQLInstanceChildren(t.Context(), svc, p, instName, instID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudSQLInstanceChildren: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (database only — backupRuns denied, sslcerts/users empty)", total, inserted)
	}
	dbID := store.ResourceID("gcp", p.ID, "projects/my-project/instances/inst1/databases/appdb")
	if _, err := st.GetResource(dbID); err != nil {
		t.Errorf("GetResource(database): %v — databases.list should still run after backupRuns.list is denied", err)
	}
}
