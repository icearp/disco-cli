package gcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/firestore/v1"
	"google.golang.org/api/option"
)

// fakeFirestoreService builds a *firestore.Service pointed at the fake
// server. Route templates embed the full "v1/" prefix.
func fakeFirestoreService(t *testing.T, srv *httptest.Server) *firestore.Service {
	t.Helper()
	svc, err := firestore.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("firestore.NewService: %v", err)
	}
	return svc
}

func TestScanFirestoreBackups_ParentedByOwnDatabaseField(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	db1Native := "projects/proj1/databases/d1"
	db2Native := "projects/proj1/databases/d2"
	// upsertWithParent/RecordHierarchyBatch's closure write silently no-ops
	// when the parent row doesn't already exist — seed both databases since
	// this test exercises the Backups phase standalone.
	upsertTestResource(t, st, "gcp", p.ID, TypeFirestoreDB, db1Native, "", "{}")
	upsertTestResource(t, st, "gcp", p.ID, TypeFirestoreDB, db2Native, "", "{}")

	backup1Native := "projects/proj1/locations/us/backups/b1"
	backup2Native := "projects/proj1/locations/us/backups/b2"
	routes := map[string]string{
		"/v1/projects/proj1/locations/-/backups": marshalAttrs(t, firestore.GoogleFirestoreAdminV1ListBackupsResponse{
			Backups: []*firestore.GoogleFirestoreAdminV1Backup{
				{Name: backup1Native, Database: db1Native, State: "READY"},
				{Name: backup2Native, Database: db2Native, State: "READY"},
			},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeFirestoreService(t, srv)

	total, inserted, err := scanFirestoreBackups(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanFirestoreBackups: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	backup1ID := store.ResourceID("gcp", p.ID, backup1Native)
	backup2ID := store.ResourceID("gcp", p.ID, backup2Native)
	db1ID := store.ResourceID("gcp", p.ID, db1Native)
	db2ID := store.ResourceID("gcp", p.ID, db2Native)

	assertChild := func(childID, parentID string) {
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
	assertChild(backup1ID, db1ID)
	assertChild(backup2ID, db2ID)

	// backup1 must NOT show up under db2 — guards the Database-field-based
	// parent derivation from cross-wiring backups to the wrong database.
	rels, err := st.RelationshipsFrom(db2ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(db2): %v", err)
	}
	for _, r := range rels {
		if r.ToID == backup1ID {
			t.Errorf("backup1 wrongly parented under db2: %+v", rels)
		}
	}
}

func TestScanFirestoreBackupSchedules_PartialDenyContinues(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	db1Native := "projects/proj1/databases/d1"
	db2Native := "projects/proj1/databases/d2"
	upsertTestResource(t, st, "gcp", p.ID, TypeFirestoreDB, db1Native, "", "{}")
	upsertTestResource(t, st, "gcp", p.ID, TypeFirestoreDB, db2Native, "", "{}")

	deniedBody := `{"error":{"code":403,"message":"caller is missing firestore.backupSchedules.list","errors":[{"reason":"forbidden"}]}}`
	db2Body := marshalAttrs(t, firestore.GoogleFirestoreAdminV1ListBackupSchedulesResponse{
		BackupSchedules: []*firestore.GoogleFirestoreAdminV1BackupSchedule{
			{Name: db2Native + "/backupSchedules/s1"},
		},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/" + db1Native + "/backupSchedules":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v1/" + db2Native + "/backupSchedules":
			_, _ = w.Write([]byte(db2Body))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeFirestoreService(t, srv)

	total, inserted, err := scanFirestoreBackupSchedules(t.Context(), svc, p, []string{db1Native, db2Native}, st, testScanID)
	if err != nil {
		t.Fatalf("scanFirestoreBackupSchedules: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (db1 denied, db2 succeeds)", total, inserted)
	}

	scheduleID := store.ResourceID("gcp", p.ID, db2Native+"/backupSchedules/s1")
	res, err := st.GetResource(scheduleID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res == nil {
		t.Fatalf("backup schedule not stored after db1 denied")
	}
}

func TestScanFirestoreUserCreds_SecurePasswordRedacted(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	dbNative := "projects/proj1/databases/d1"
	upsertTestResource(t, st, "gcp", p.ID, TypeFirestoreDB, dbNative, "", "{}")

	ucNative := dbNative + "/userCreds/uc1"
	routes := map[string]string{
		"/v1/" + dbNative + "/userCreds": marshalAttrs(t, firestore.GoogleFirestoreAdminV1ListUserCredsResponse{
			UserCreds: []*firestore.GoogleFirestoreAdminV1UserCreds{
				{Name: ucNative, State: "ENABLED", SecurePassword: "plaintext-should-never-land"},
			},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeFirestoreService(t, srv)

	total, inserted, err := scanFirestoreUserCreds(t.Context(), svc, p, []string{dbNative}, st, testScanID)
	if err != nil {
		t.Fatalf("scanFirestoreUserCreds: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	ucID := store.ResourceID("gcp", p.ID, ucNative)
	res, err := st.GetResource(ucID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res == nil {
		t.Fatalf("user cred not stored")
	}
	if res.AttributesJSON == "" {
		t.Fatalf("empty attributes")
	}
	if strings.Contains(res.AttributesJSON, "plaintext-should-never-land") {
		t.Errorf("securePassword not redacted: %s", res.AttributesJSON)
	}

	dbID := store.ResourceID("gcp", p.ID, dbNative)
	rels, err := st.RelationshipsFrom(dbID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var found bool
	for _, r := range rels {
		if r.ToID == ucID {
			found = true
		}
	}
	if !found {
		t.Errorf("user cred not found as child of database; got %+v", rels)
	}
}

func TestScanFirestoreDatabases_ReturnsNamesForFanout(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	dbNative := "projects/proj1/databases/d1"
	routes := map[string]string{
		"/v1/projects/proj1/databases": marshalAttrs(t, firestore.GoogleFirestoreAdminV1ListDatabasesResponse{
			Databases: []*firestore.GoogleFirestoreAdminV1Database{{Name: dbNative, LocationId: "us-east1"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeFirestoreService(t, srv)

	databaseNames, total, inserted, err := scanFirestoreDatabases(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanFirestoreDatabases: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	if len(databaseNames) != 1 || databaseNames[0] != dbNative {
		t.Fatalf("databaseNames: got %v, want [%s]", databaseNames, dbNative)
	}
}

func TestScanFirestoreBackups_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	body := `{"error":{"code":403,"message":"caller is missing firestore permission","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeFirestoreService(t, srv)

	total, inserted, err := scanFirestoreBackups(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanFirestoreBackups (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
