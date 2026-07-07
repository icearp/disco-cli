package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/bigquery/v2"
	"google.golang.org/api/option"
)

// fakeBigQueryService builds a *bigquery.Service pointed at the fake server.
// bigquery's BasePath already embeds "bigquery/v2/" — option.WithEndpoint
// replaces the entire BasePath (same gotcha as compute's "/compute/v1"), so
// route templates below have NO version prefix.
func fakeBigQueryService(t *testing.T, srv *httptest.Server) *bigquery.Service {
	t.Helper()
	svc, err := bigquery.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("bigquery.NewService: %v", err)
	}
	return svc
}

func TestScanBigQuery_ModelsRoutinesRowAccessPoliciesChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	tableNative := "proj1:ds1.t1"
	dsNative := "proj1:ds1"
	routes := map[string]string{
		"/projects/proj1/datasets": marshalAttrs(t, bigquery.DatasetList{
			Datasets: []*bigquery.DatasetListDatasets{
				{DatasetReference: &bigquery.DatasetReference{ProjectId: "proj1", DatasetId: "ds1"}},
			},
		}),
		"/projects/proj1/datasets/ds1": marshalAttrs(t, bigquery.Dataset{
			Id:       dsNative,
			Location: "US",
			DatasetReference: &bigquery.DatasetReference{
				ProjectId: "proj1", DatasetId: "ds1",
			},
		}),
		"/projects/proj1/datasets/ds1/tables": marshalAttrs(t, bigquery.TableList{
			Tables: []*bigquery.TableListTables{
				{Id: tableNative, TableReference: &bigquery.TableReference{ProjectId: "proj1", DatasetId: "ds1", TableId: "t1"}},
			},
		}),
		"/projects/proj1/datasets/ds1/tables/t1/rowAccessPolicies": marshalAttrs(t, bigquery.ListRowAccessPoliciesResponse{
			RowAccessPolicies: []*bigquery.RowAccessPolicy{
				{RowAccessPolicyReference: &bigquery.RowAccessPolicyReference{ProjectId: "proj1", DatasetId: "ds1", TableId: "t1", PolicyId: "rap1"}},
			},
		}),
		"/projects/proj1/datasets/ds1/models": marshalAttrs(t, bigquery.ListModelsResponse{
			Models: []*bigquery.Model{
				{ModelReference: &bigquery.ModelReference{ProjectId: "proj1", DatasetId: "ds1", ModelId: "m1"}, Location: "US"},
			},
		}),
		"/projects/proj1/datasets/ds1/routines": marshalAttrs(t, bigquery.ListRoutinesResponse{
			Routines: []*bigquery.Routine{
				{RoutineReference: &bigquery.RoutineReference{ProjectId: "proj1", DatasetId: "ds1", RoutineId: "r1"}},
			},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeBigQueryService(t, srv)

	total, inserted, err := scanBigQueryWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigQueryWithClient: %v", err)
	}
	// 1 dataset + 1 table + 1 row access policy + 1 model + 1 routine.
	if total != 5 || inserted != 5 {
		t.Fatalf("counts: got total=%d inserted=%d, want 5/5", total, inserted)
	}

	dsResID := store.ResourceID("gcp", p.ID, TypeBQDataset, dsNative)
	tableResID := store.ResourceID("gcp", p.ID, TypeBQTable, tableNative)
	rapResID := store.ResourceID("gcp", p.ID, TypeBQRowAccessPolicy, "projects/proj1/datasets/ds1/tables/t1/rowAccessPolicies/rap1")
	modelResID := store.ResourceID("gcp", p.ID, TypeBQModel, "projects/proj1/datasets/ds1/models/m1")
	routineResID := store.ResourceID("gcp", p.ID, TypeBQRoutine, "projects/proj1/datasets/ds1/routines/r1")

	assertChild := func(parentID, childID, label string) {
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
		t.Errorf("%s not found as child of %s; got %+v", label, parentID, rels)
	}
	assertChild(dsResID, tableResID, "table")
	assertChild(dsResID, modelResID, "model")
	assertChild(dsResID, routineResID, "routine")
	assertChild(tableResID, rapResID, "row access policy")
}

func TestScanBigQuery_RowAccessPoliciesPermissionDeniedContinuesToModels(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	dsNative := "proj1:ds1"
	tableNative := "proj1:ds1.t1"
	deniedBody := `{"error":{"code":403,"message":"caller is missing bigquery.rowAccessPolicies.list","errors":[{"reason":"forbidden"}]}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/projects/proj1/datasets":
			_, _ = w.Write([]byte(marshalAttrs(t, bigquery.DatasetList{
				Datasets: []*bigquery.DatasetListDatasets{
					{DatasetReference: &bigquery.DatasetReference{ProjectId: "proj1", DatasetId: "ds1"}},
				},
			})))
		case "/projects/proj1/datasets/ds1":
			_, _ = w.Write([]byte(marshalAttrs(t, bigquery.Dataset{
				Id: dsNative, Location: "US",
				DatasetReference: &bigquery.DatasetReference{ProjectId: "proj1", DatasetId: "ds1"},
			})))
		case "/projects/proj1/datasets/ds1/tables":
			_, _ = w.Write([]byte(marshalAttrs(t, bigquery.TableList{
				Tables: []*bigquery.TableListTables{
					{Id: tableNative, TableReference: &bigquery.TableReference{ProjectId: "proj1", DatasetId: "ds1", TableId: "t1"}},
				},
			})))
		case "/projects/proj1/datasets/ds1/tables/t1/rowAccessPolicies":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/projects/proj1/datasets/ds1/models":
			_, _ = w.Write([]byte(marshalAttrs(t, bigquery.ListModelsResponse{
				Models: []*bigquery.Model{
					{ModelReference: &bigquery.ModelReference{ProjectId: "proj1", DatasetId: "ds1", ModelId: "m1"}},
				},
			})))
		case "/projects/proj1/datasets/ds1/routines":
			_, _ = w.Write([]byte(marshalAttrs(t, bigquery.ListRoutinesResponse{})))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeBigQueryService(t, srv)

	total, inserted, err := scanBigQueryWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigQueryWithClient: %v", err)
	}
	// 1 dataset + 1 table + 0 row access policies (denied) + 1 model + 0 routines.
	if total != 3 || inserted != 3 {
		t.Fatalf("counts: got total=%d inserted=%d, want 3/3 (row access policies denied, models/routines still scanned)", total, inserted)
	}

	modelResID := store.ResourceID("gcp", p.ID, TypeBQModel, "projects/proj1/datasets/ds1/models/m1")
	res, err := st.GetResource(modelResID)
	if err != nil {
		t.Fatalf("GetResource(model): %v", err)
	}
	if res == nil {
		t.Fatalf("model not stored after row access policies denied")
	}
}

// TestScanBigQuery_RowAccessPoliciesAPINotEnabledShapeDoesNotDisableWholeService
// guards a real bug an adversarial review caught: an isAPINotEnabled-shaped
// error (e.g. "has not enabled" — a documented BigQuery error shape) on a
// single table's RowAccessPolicies.List must NOT escalate to the
// whole-service disabled sentinel, since Datasets.List (phase 1) already
// proved the API is enabled by the time this nested call runs. Before the
// fix, this returned a wrapped errServiceDisabled that discarded the
// already-successful dataset/table/model work from the caller's perspective.
func TestScanBigQuery_RowAccessPoliciesAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	dsNative := "proj1:ds1"
	tableNative := "proj1:ds1.t1"
	// isAPINotEnabled matches a 400 containing "has not enabled" — the
	// BigQuery-specific shape documented in gcp_errors.go.
	apiNotEnabledBody := `{"error":{"code":400,"message":"BigQuery Row Level Security has not enabled for this project"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/projects/proj1/datasets":
			_, _ = w.Write([]byte(marshalAttrs(t, bigquery.DatasetList{
				Datasets: []*bigquery.DatasetListDatasets{
					{DatasetReference: &bigquery.DatasetReference{ProjectId: "proj1", DatasetId: "ds1"}},
				},
			})))
		case "/projects/proj1/datasets/ds1":
			_, _ = w.Write([]byte(marshalAttrs(t, bigquery.Dataset{
				Id: dsNative, Location: "US",
				DatasetReference: &bigquery.DatasetReference{ProjectId: "proj1", DatasetId: "ds1"},
			})))
		case "/projects/proj1/datasets/ds1/tables":
			_, _ = w.Write([]byte(marshalAttrs(t, bigquery.TableList{
				Tables: []*bigquery.TableListTables{
					{Id: tableNative, TableReference: &bigquery.TableReference{ProjectId: "proj1", DatasetId: "ds1", TableId: "t1"}},
				},
			})))
		case "/projects/proj1/datasets/ds1/tables/t1/rowAccessPolicies":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(apiNotEnabledBody))
		case "/projects/proj1/datasets/ds1/models":
			_, _ = w.Write([]byte(marshalAttrs(t, bigquery.ListModelsResponse{})))
		case "/projects/proj1/datasets/ds1/routines":
			_, _ = w.Write([]byte(marshalAttrs(t, bigquery.ListRoutinesResponse{})))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeBigQueryService(t, srv)

	total, inserted, err := scanBigQueryWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigQueryWithClient: expected nil error (must not escalate to disabled sentinel), got %v", err)
	}
	// 1 dataset + 1 table + 0 row access policies (API-not-enabled shape).
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2 (dataset+table survive despite row access policy API-not-enabled shape)", total, inserted)
	}

	tableResID := store.ResourceID("gcp", p.ID, TypeBQTable, tableNative)
	res, err := st.GetResource(tableResID)
	if err != nil {
		t.Fatalf("GetResource(table): %v", err)
	}
	if res == nil {
		t.Fatalf("table not stored despite row access policy API-not-enabled shape")
	}
}

func TestScanBigQuery_DatasetsListPermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	body := `{"error":{"code":403,"message":"caller is missing bigquery permission","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeBigQueryService(t, srv)

	total, inserted, err := scanBigQueryWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanBigQueryWithClient (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
