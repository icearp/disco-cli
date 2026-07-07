package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	dataflow "google.golang.org/api/dataflow/v1b3"
	"google.golang.org/api/option"
)

func fakeDataflowService(t *testing.T, srv *httptest.Server) *dataflow.Service {
	t.Helper()
	svc, err := dataflow.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("dataflow.NewService: %v", err)
	}
	return svc
}

func TestScanDataflow_JobsAndSnapshotsChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1b3/projects/proj1/jobs:aggregated": marshalAttrs(t, dataflow.ListJobsResponse{
			Jobs: []*dataflow.Job{{Id: "job1", Name: "job-one", Location: "us-central1", CurrentState: "JOB_STATE_RUNNING"}},
		}),
		"/v1b3/projects/proj1/locations/us-central1/snapshots": marshalAttrs(t, dataflow.ListSnapshotsResponse{
			Snapshots: []*dataflow.Snapshot{{Id: "snap1", State: "SNAPSHOT_STATE_READY"}},
		}),
		"/v1b3/projects/proj1/locations/us-east1/snapshots": marshalAttrs(t, dataflow.ListSnapshotsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeDataflowService(t, srv)

	total, inserted, err := scanDataflowWithClient(t.Context(), svc, []string{"us-central1", "us-east1"}, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanDataflowWithClient: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	id := store.ResourceID("gcp", p.ID, TypeDataflowSnapshot, "projects/proj1/locations/us-central1/snapshots/snap1")
	res, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource(snapshot): %v", err)
	}
	if res == nil {
		t.Fatalf("snapshot not stored")
	}
}

func TestScanDataflow_SnapshotsAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	notEnabledBody := `{"error":{"code":403,"message":"Dataflow API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1b3/projects/proj1/jobs:aggregated":
			_, _ = w.Write([]byte(marshalAttrs(t, dataflow.ListJobsResponse{
				Jobs: []*dataflow.Job{{Id: "job1", Name: "job-one", Location: "us-central1", CurrentState: "JOB_STATE_RUNNING"}},
			})))
		case "/v1b3/projects/proj1/locations/us-central1/snapshots":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeDataflowService(t, srv)

	total, inserted, err := scanDataflowWithClient(t.Context(), svc, []string{"us-central1"}, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanDataflowWithClient: %v (Snapshots' isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel)", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanDataflow_EmptyProjectNoJobsNoSnapshots(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1b3/projects/proj1/jobs:aggregated":                 marshalAttrs(t, dataflow.ListJobsResponse{}),
		"/v1b3/projects/proj1/locations/us-central1/snapshots": marshalAttrs(t, dataflow.ListSnapshotsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeDataflowService(t, srv)

	total, inserted, err := scanDataflowWithClient(t.Context(), svc, []string{"us-central1"}, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanDataflowWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
