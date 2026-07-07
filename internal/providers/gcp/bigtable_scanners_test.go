package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/bigtableadmin/v2"
	"google.golang.org/api/option"
)

// fakeBigtableService builds a *bigtableadmin.Service pointed at the fake
// server. Route templates embed the full "v2/" prefix.
func fakeBigtableService(t *testing.T, srv *httptest.Server) *bigtableadmin.Service {
	t.Helper()
	svc, err := bigtableadmin.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("bigtableadmin.NewService: %v", err)
	}
	return svc
}

func TestScanBigtableAppProfiles_MultiParentDerivedFromName(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	inst1Native := "projects/proj1/instances/i1"
	inst2Native := "projects/proj1/instances/i2"
	// upsertWithParent/RecordHierarchyBatch's closure write silently no-ops
	// when the parent row doesn't already exist — seed both instances since
	// this test exercises the AppProfiles phase standalone.
	upsertTestResource(t, st, "gcp", p.ID, TypeBigtableInstance, inst1Native, "", "{}")
	upsertTestResource(t, st, "gcp", p.ID, TypeBigtableInstance, inst2Native, "", "{}")

	ap1Native := inst1Native + "/appProfiles/ap1"
	ap2Native := inst2Native + "/appProfiles/ap2"
	routes := map[string]string{
		"/v2/projects/proj1/instances/-/appProfiles": marshalAttrs(t, bigtableadmin.ListAppProfilesResponse{
			AppProfiles: []*bigtableadmin.AppProfile{
				{Name: ap1Native},
				{Name: ap2Native},
			},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeBigtableService(t, srv)

	total, inserted, err := scanBigtableAppProfiles(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigtableAppProfiles: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	ap1ID := store.ResourceID("gcp", p.ID, TypeBigtableAppProfile, ap1Native)
	ap2ID := store.ResourceID("gcp", p.ID, TypeBigtableAppProfile, ap2Native)
	inst1ID := store.ResourceID("gcp", p.ID, TypeBigtableInstance, inst1Native)
	inst2ID := store.ResourceID("gcp", p.ID, TypeBigtableInstance, inst2Native)

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
	assertChild(ap1ID, inst1ID)
	assertChild(ap2ID, inst2ID)

	// ap1 must NOT show up under inst2 — guards the string-split parent
	// derivation from cross-wiring app profiles to the wrong instance.
	rels, err := st.RelationshipsFrom(inst2ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(inst2): %v", err)
	}
	for _, r := range rels {
		if r.ToID == ap1ID {
			t.Errorf("ap1 wrongly parented under inst2: %+v", rels)
		}
	}
}

func TestScanBigtableBackups_PartialDenyContinues(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	inst1Native := "projects/proj1/instances/i1"
	inst2Native := "projects/proj1/instances/i2"
	cluster2Native := inst2Native + "/clusters/c2"
	deniedBody := `{"error":{"code":403,"message":"caller is missing bigtable.backups.list","errors":[{"reason":"forbidden"}]}}`

	inst2BackupsBody := marshalAttrs(t, bigtableadmin.ListBackupsResponse{
		Backups: []*bigtableadmin.Backup{{Name: cluster2Native + "/backups/b1", State: "READY"}},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/" + inst1Native + "/clusters/-/backups":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v2/" + inst2Native + "/clusters/-/backups":
			_, _ = w.Write([]byte(inst2BackupsBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeBigtableService(t, srv)

	// Seed the owning cluster so the hierarchy closure write isn't a no-op.
	upsertTestResource(t, st, "gcp", p.ID, TypeBigtableCluster, cluster2Native, "", "{}")

	instances := []*bigtableadmin.Instance{{Name: inst1Native}, {Name: inst2Native}}
	total, inserted, err := scanBigtableBackups(t.Context(), svc, p, instances, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigtableBackups: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (inst1 denied, inst2 succeeds)", total, inserted)
	}

	backupNative := cluster2Native + "/backups/b1"
	backupID := store.ResourceID("gcp", p.ID, TypeBigtableBackup, backupNative)
	res, err := st.GetResource(backupID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res == nil {
		t.Fatalf("backup not stored after inst1 denied")
	}

	cluster2ID := store.ResourceID("gcp", p.ID, TypeBigtableCluster, cluster2Native)
	rels, err := st.RelationshipsFrom(cluster2ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(cluster2): %v", err)
	}
	var found bool
	for _, r := range rels {
		if r.ToID == backupID {
			found = true
		}
	}
	if !found {
		t.Errorf("backup not found as child of owning cluster; got %+v", rels)
	}
}

func TestScanBigtableMemoryLayers_MultiParentDerivedFromName(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	instNative := "projects/proj1/instances/i1"
	cluster1Native := instNative + "/clusters/c1"
	cluster2Native := instNative + "/clusters/c2"
	upsertTestResource(t, st, "gcp", p.ID, TypeBigtableCluster, cluster1Native, "", "{}")
	upsertTestResource(t, st, "gcp", p.ID, TypeBigtableCluster, cluster2Native, "", "{}")

	ml1Native := cluster1Native + "/memoryLayer"
	ml2Native := cluster2Native + "/memoryLayer"
	routes := map[string]string{
		"/v2/" + instNative + "/clusters/-/memoryLayers": marshalAttrs(t, bigtableadmin.ListMemoryLayersResponse{
			MemoryLayers: []*bigtableadmin.MemoryLayer{
				{Name: ml1Native, State: "READY"},
				{Name: ml2Native, State: "ENABLING"},
			},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeBigtableService(t, srv)

	instances := []*bigtableadmin.Instance{{Name: instNative}}
	total, inserted, err := scanBigtableMemoryLayers(t.Context(), svc, p, instances, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigtableMemoryLayers: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	ml1ID := store.ResourceID("gcp", p.ID, TypeBigtableMemoryLayer, ml1Native)
	cluster1ID := store.ResourceID("gcp", p.ID, TypeBigtableCluster, cluster1Native)
	cluster2ID := store.ResourceID("gcp", p.ID, TypeBigtableCluster, cluster2Native)

	rels, err := st.RelationshipsFrom(cluster1ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(cluster1): %v", err)
	}
	var found bool
	for _, r := range rels {
		if r.ToID == ml1ID {
			found = true
		}
	}
	if !found {
		t.Errorf("memory layer 1 not found as child of cluster1; got %+v", rels)
	}

	// ml1 must NOT show up under cluster2 — guards the suffix-trim parent
	// derivation from cross-wiring memory layers to the wrong cluster.
	rels2, err := st.RelationshipsFrom(cluster2ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(cluster2): %v", err)
	}
	for _, r := range rels2 {
		if r.ToID == ml1ID {
			t.Errorf("memory layer 1 wrongly parented under cluster2: %+v", rels2)
		}
	}
}

func TestScanBigtableTables_FanoutAndAuthorizedViewSchemaBundleChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	instNative := "projects/proj1/instances/i1"
	upsertTestResource(t, st, "gcp", p.ID, TypeBigtableInstance, instNative, "", "{}")

	tableNative := instNative + "/tables/t1"
	avNative := tableNative + "/authorizedViews/av1"
	sbNative := tableNative + "/schemaBundles/sb1"
	routes := map[string]string{
		"/v2/" + instNative + "/tables": marshalAttrs(t, bigtableadmin.ListTablesResponse{
			Tables: []*bigtableadmin.Table{{Name: tableNative}},
		}),
		"/v2/" + tableNative + "/authorizedViews": marshalAttrs(t, bigtableadmin.ListAuthorizedViewsResponse{
			AuthorizedViews: []*bigtableadmin.AuthorizedView{{Name: avNative}},
		}),
		"/v2/" + tableNative + "/schemaBundles": marshalAttrs(t, bigtableadmin.ListSchemaBundlesResponse{
			SchemaBundles: []*bigtableadmin.SchemaBundle{{Name: sbNative}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeBigtableService(t, srv)

	instances := []*bigtableadmin.Instance{{Name: instNative}}
	tableNames, total, inserted, err := scanBigtableTables(t.Context(), svc, p, instances, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigtableTables: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("tables counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	if len(tableNames) != 1 || tableNames[0] != tableNative {
		t.Fatalf("tableNames: got %v, want [%s]", tableNames, tableNative)
	}

	total, inserted, err = scanBigtableAuthorizedViews(t.Context(), svc, p, tableNames, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigtableAuthorizedViews: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("authorizedViews counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	total, inserted, err = scanBigtableSchemaBundles(t.Context(), svc, p, tableNames, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigtableSchemaBundles: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("schemaBundles counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	tableID := store.ResourceID("gcp", p.ID, TypeBigtableTable, tableNative)
	avID := store.ResourceID("gcp", p.ID, TypeBigtableAuthorizedView, avNative)
	sbID := store.ResourceID("gcp", p.ID, TypeBigtableSchemaBundle, sbNative)
	rels, err := st.RelationshipsFrom(tableID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(table): %v", err)
	}
	var gotAV, gotSB bool
	for _, r := range rels {
		if r.ToID == avID {
			gotAV = true
		}
		if r.ToID == sbID {
			gotSB = true
		}
	}
	if !gotAV {
		t.Errorf("authorized view not found as child of table; got %+v", rels)
	}
	if !gotSB {
		t.Errorf("schema bundle not found as child of table; got %+v", rels)
	}
}

func TestScanBigtableHotTablets_FanoutPerCluster(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	cluster1Native := "projects/proj1/instances/i1/clusters/c1"
	upsertTestResource(t, st, "gcp", p.ID, TypeBigtableCluster, cluster1Native, "", "{}")

	htNative := cluster1Native + "/hotTablets/ht1"
	routes := map[string]string{
		"/v2/" + cluster1Native + "/hotTablets": marshalAttrs(t, bigtableadmin.ListHotTabletsResponse{
			HotTablets: []*bigtableadmin.HotTablet{{Name: htNative}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeBigtableService(t, srv)

	clusters := []*bigtableadmin.Cluster{{Name: cluster1Native}}
	total, inserted, err := scanBigtableHotTablets(t.Context(), svc, p, clusters, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigtableHotTablets: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	htID := store.ResourceID("gcp", p.ID, TypeBigtableHotTablet, htNative)
	res, err := st.GetResource(htID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res == nil {
		t.Fatalf("hot tablet not stored")
	}
}

func TestScanBigtableLogicalAndMaterializedViews_Basic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	instNative := "projects/proj1/instances/i1"
	upsertTestResource(t, st, "gcp", p.ID, TypeBigtableInstance, instNative, "", "{}")

	lvNative := instNative + "/logicalViews/lv1"
	mvNative := instNative + "/materializedViews/mv1"
	routes := map[string]string{
		"/v2/" + instNative + "/logicalViews": marshalAttrs(t, bigtableadmin.ListLogicalViewsResponse{
			LogicalViews: []*bigtableadmin.LogicalView{{Name: lvNative}},
		}),
		"/v2/" + instNative + "/materializedViews": marshalAttrs(t, bigtableadmin.ListMaterializedViewsResponse{
			MaterializedViews: []*bigtableadmin.MaterializedView{{Name: mvNative}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeBigtableService(t, srv)

	instances := []*bigtableadmin.Instance{{Name: instNative}}
	total, inserted, err := scanBigtableLogicalViews(t.Context(), svc, p, instances, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigtableLogicalViews: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("logicalViews counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	total, inserted, err = scanBigtableMaterializedViews(t.Context(), svc, p, instances, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigtableMaterializedViews: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("materializedViews counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	instID := store.ResourceID("gcp", p.ID, TypeBigtableInstance, instNative)
	lvID := store.ResourceID("gcp", p.ID, TypeBigtableLogicalView, lvNative)
	mvID := store.ResourceID("gcp", p.ID, TypeBigtableMaterializedView, mvNative)
	rels, err := st.RelationshipsFrom(instID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(instance): %v", err)
	}
	var gotLV, gotMV bool
	for _, r := range rels {
		if r.ToID == lvID {
			gotLV = true
		}
		if r.ToID == mvID {
			gotMV = true
		}
	}
	if !gotLV {
		t.Errorf("logical view not found as child of instance; got %+v", rels)
	}
	if !gotMV {
		t.Errorf("materialized view not found as child of instance; got %+v", rels)
	}
}

func TestScanBigtableAppProfiles_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	body := `{"error":{"code":403,"message":"caller is missing bigtable permission","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeBigtableService(t, srv)

	total, inserted, err := scanBigtableAppProfiles(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigtableAppProfiles (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
