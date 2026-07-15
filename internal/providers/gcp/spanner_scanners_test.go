package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/option"
	"google.golang.org/api/spanner/v1"
)

// fakeSpannerService builds a *spanner.Service pointed at the fake server.
// Route templates embed the full "v1/" prefix.
func fakeSpannerService(t *testing.T, srv *httptest.Server) *spanner.Service {
	t.Helper()
	svc, err := spanner.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("spanner.NewService: %v", err)
	}
	return svc
}

func TestScanSpannerInstancePartitions_MultiParentDerivedFromName(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	inst1Native := "projects/proj1/instances/i1"
	inst2Native := "projects/proj1/instances/i2"
	// upsertWithParent/RecordHierarchyBatch's closure write silently no-ops
	// when the parent row doesn't already exist — seed both instances since
	// this test exercises the InstancePartitions phase standalone.
	upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstance, inst1Native, "", "{}")
	upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstance, inst2Native, "", "{}")

	part1Native := inst1Native + "/instancePartitions/part1"
	part2Native := inst2Native + "/instancePartitions/part2"
	routes := map[string]string{
		"/v1/projects/proj1/instances/-/instancePartitions": marshalAttrs(t, spanner.ListInstancePartitionsResponse{
			InstancePartitions: []*spanner.InstancePartition{
				{Name: part1Native, Config: "projects/proj1/instanceConfigs/regional-us-east1"},
				{Name: part2Native, Config: "projects/proj1/instanceConfigs/regional-us-west1"},
			},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeSpannerService(t, srv)

	total, inserted, err := scanSpannerInstancePartitions(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanSpannerInstancePartitions: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	part1ID := store.ResourceID("gcp", p.ID, part1Native)
	part2ID := store.ResourceID("gcp", p.ID, part2Native)
	inst1ID := store.ResourceID("gcp", p.ID, inst1Native)
	inst2ID := store.ResourceID("gcp", p.ID, inst2Native)

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
	assertChild(part1ID, inst1ID)
	assertChild(part2ID, inst2ID)

	// part1 must NOT show up under inst2 — guards the string-split parent
	// derivation from cross-wiring partitions to the wrong instance.
	rels, err := st.RelationshipsFrom(inst2ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(inst2): %v", err)
	}
	for _, r := range rels {
		if r.ToID == part1ID {
			t.Errorf("part1 wrongly parented under inst2: %+v", rels)
		}
	}
}

func TestScanSpannerBackups_PartialDenyContinues(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	inst1Native := "projects/proj1/instances/i1"
	inst2Native := "projects/proj1/instances/i2"
	deniedBody := `{"error":{"code":403,"message":"caller is missing spanner.backups.list","errors":[{"reason":"forbidden"}]}}`

	inst2BackupsBody := marshalAttrs(t, spanner.ListBackupsResponse{
		Backups: []*spanner.Backup{{Name: inst2Native + "/backups/b1", State: "READY"}},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/" + inst1Native + "/backups":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v1/" + inst2Native + "/backups":
			_, _ = w.Write([]byte(inst2BackupsBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeSpannerService(t, srv)

	instances := []*spanner.Instance{{Name: inst1Native}, {Name: inst2Native}}
	total, inserted, err := scanSpannerBackups(t.Context(), svc, p, instances, st, testScanID)
	if err != nil {
		t.Fatalf("scanSpannerBackups: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (inst1 denied, inst2 succeeds)", total, inserted)
	}

	backupID := store.ResourceID("gcp", p.ID, inst2Native+"/backups/b1")
	res, err := st.GetResource(backupID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res == nil {
		t.Fatalf("backup not stored after inst1 denied")
	}
}

func TestScanSpannerFlatAndFanoutPhases_Basic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	dbNative := "projects/proj1/instances/i1/databases/d1"
	routes := map[string]string{
		"/v1/projects/proj1/instanceConfigs": marshalAttrs(t, spanner.ListInstanceConfigsResponse{
			InstanceConfigs: []*spanner.InstanceConfig{{Name: "projects/proj1/instanceConfigs/regional-us-east1", DisplayName: "us-east1"}},
		}),
		"/v1/" + dbNative + "/backupSchedules": marshalAttrs(t, spanner.ListBackupSchedulesResponse{
			BackupSchedules: []*spanner.BackupSchedule{{Name: dbNative + "/backupSchedules/s1"}},
		}),
		"/v1/" + dbNative + "/databaseRoles": marshalAttrs(t, spanner.ListDatabaseRolesResponse{
			DatabaseRoles: []*spanner.DatabaseRole{{Name: dbNative + "/databaseRoles/r1"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeSpannerService(t, srv)

	// upsertWithParent's closure write silently no-ops when the parent row
	// doesn't already exist — seed the database since these phases run
	// standalone here, without the preceding Databases phase.
	upsertTestResource(t, st, "gcp", p.ID, TypeSpannerDatabase, dbNative, "", "{}")

	total, inserted, err := scanSpannerInstanceConfigs(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanSpannerInstanceConfigs: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("instanceConfigs counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	total, inserted, err = scanSpannerBackupSchedules(t.Context(), svc, p, []string{dbNative}, st, testScanID)
	if err != nil {
		t.Fatalf("scanSpannerBackupSchedules: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("backupSchedules counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	total, inserted, err = scanSpannerDatabaseRoles(t.Context(), svc, p, []string{dbNative}, st, testScanID)
	if err != nil {
		t.Fatalf("scanSpannerDatabaseRoles: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("databaseRoles counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	dbID := store.ResourceID("gcp", p.ID, dbNative)
	scheduleID := store.ResourceID("gcp", p.ID, dbNative+"/backupSchedules/s1")
	roleID := store.ResourceID("gcp", p.ID, dbNative+"/databaseRoles/r1")
	rels, err := st.RelationshipsFrom(dbID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var gotSchedule, gotRole bool
	for _, r := range rels {
		if r.ToID == scheduleID {
			gotSchedule = true
		}
		if r.ToID == roleID {
			gotRole = true
		}
	}
	if !gotSchedule {
		t.Errorf("backup schedule not found as child of database; got %+v", rels)
	}
	if !gotRole {
		t.Errorf("database role not found as child of database; got %+v", rels)
	}
}

func TestScanSpannerInstances_ManagedCatalogFlagged(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1/projects/proj1/instances": marshalAttrs(t, spanner.ListInstancesResponse{
			Instances: []*spanner.Instance{{Name: "projects/proj1/instances/i1", Config: "projects/proj1/instanceConfigs/regional-us-east1"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeSpannerService(t, srv)

	instances, total, inserted, err := scanSpannerInstances(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanSpannerInstances: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	if len(instances) != 1 || instances[0].Name != "projects/proj1/instances/i1" {
		t.Fatalf("instances: got %v, want [projects/proj1/instances/i1]", instances)
	}
}

func TestScanSpannerInstanceConfigs_GoogleManagedFlagged(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1/projects/proj1/instanceConfigs": marshalAttrs(t, spanner.ListInstanceConfigsResponse{
			InstanceConfigs: []*spanner.InstanceConfig{
				{Name: "projects/proj1/instanceConfigs/regional-us-east1", DisplayName: "us-east1", ConfigType: "GOOGLE_MANAGED"},
				{Name: "projects/proj1/instanceConfigs/custom-config", DisplayName: "Custom", ConfigType: "USER_MANAGED"},
			},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeSpannerService(t, srv)

	total, inserted, err := scanSpannerInstanceConfigs(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanSpannerInstanceConfigs: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	googleID := store.ResourceID("gcp", p.ID, "projects/proj1/instanceConfigs/regional-us-east1")
	googleRes, err := st.GetResource(googleID)
	if err != nil {
		t.Fatalf("GetResource(google): %v", err)
	}
	if googleRes == nil || !googleRes.ManagedByProvider {
		t.Errorf("Google-managed instance config not flagged ManagedByProvider: %+v", googleRes)
	}

	customID := store.ResourceID("gcp", p.ID, "projects/proj1/instanceConfigs/custom-config")
	customRes, err := st.GetResource(customID)
	if err != nil {
		t.Fatalf("GetResource(custom): %v", err)
	}
	if customRes == nil || customRes.ManagedByProvider {
		t.Errorf("user-managed instance config wrongly flagged ManagedByProvider: %+v", customRes)
	}
}

// TestScanSpannerDatabases_AccumulatesAcrossInstancesAndContinuesPastDenial
// guards two things at once: (1) databaseNames must accumulate across every
// instance, not just the last one iterated; (2) a permission-denied on one
// instance's Databases.List must not stop the loop from processing the
// remaining instances.
func TestScanSpannerDatabases_AccumulatesAcrossInstancesAndContinuesPastDenial(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	inst1Native := "projects/proj1/instances/i1"
	inst2Native := "projects/proj1/instances/i2"
	upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstance, inst1Native, "", "{}")
	upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstance, inst2Native, "", "{}")

	db2Native := inst2Native + "/databases/d2"
	deniedBody := `{"error":{"code":403,"message":"caller is missing spanner.databases.list","errors":[{"reason":"forbidden"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/" + inst1Native + "/databases":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v1/" + inst2Native + "/databases":
			_, _ = w.Write([]byte(marshalAttrs(t, spanner.ListDatabasesResponse{
				Databases: []*spanner.Database{{Name: db2Native, State: "READY"}},
			})))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeSpannerService(t, srv)

	instances := []*spanner.Instance{{Name: inst1Native}, {Name: inst2Native}}
	databaseNames, total, inserted, err := scanSpannerDatabases(t.Context(), svc, p, instances, st, testScanID)
	if err != nil {
		t.Fatalf("scanSpannerDatabases: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (inst1 denied, inst2 succeeds)", total, inserted)
	}
	if len(databaseNames) != 1 || databaseNames[0] != db2Native {
		t.Fatalf("databaseNames: got %v, want [%s]", databaseNames, db2Native)
	}

	db2ID := store.ResourceID("gcp", p.ID, db2Native)
	inst2ID := store.ResourceID("gcp", p.ID, inst2Native)
	rels, err := st.RelationshipsFrom(inst2ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var found bool
	for _, r := range rels {
		if r.ToID == db2ID {
			found = true
		}
	}
	if !found {
		t.Errorf("database %s not found as child of instance %s; got %+v", db2ID, inst2ID, rels)
	}
}

func TestScanSpannerInstancePartitions_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	body := `{"error":{"code":403,"message":"caller is missing spanner permission","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeSpannerService(t, srv)

	total, inserted, err := scanSpannerInstancePartitions(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanSpannerInstancePartitions (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
