package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/option"
	"google.golang.org/api/run/v2"
)

func fakeCloudRunJobsService(t *testing.T, srv *httptest.Server) *run.Service {
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
	return svc
}

func TestScanCloudRunJobs_JobExecutionChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	jobName := "projects/proj1/locations/us-central1/jobs/job1"
	execName := jobName + "/executions/job1-abcde"

	routes := map[string]string{
		"/v2/projects/proj1/locations/-/jobs": marshalAttrs(t, run.GoogleCloudRunV2ListJobsResponse{
			Jobs: []*run.GoogleCloudRunV2Job{{Name: jobName}},
		}),
		"/v2/" + jobName + "/executions": marshalAttrs(t, run.GoogleCloudRunV2ListExecutionsResponse{
			Executions: []*run.GoogleCloudRunV2Execution{{Name: execName}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeCloudRunJobsService(t, srv)

	total, inserted, err := scanCloudRunJobsWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudRunJobsWithClient: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	jobID := store.ResourceID("gcp", p.ID, jobName)
	execID := store.ResourceID("gcp", p.ID, execName)
	if res, err := st.GetResource(execID); err != nil || res == nil {
		t.Fatalf("GetResource(execution): res=%v err=%v", res, err)
	}

	rels, err := st.RelationshipsFrom(jobID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(job): %v", err)
	}
	if len(rels) == 0 {
		t.Errorf("expected job to contain the execution row via hierarchy closure, got none")
	}
}

func TestScanCloudRunJobs_ExecutionsAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	jobName := "projects/proj1/locations/us-central1/jobs/job1"
	notEnabledBody := `{"error":{"code":403,"message":"Cloud Run Admin API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/projects/proj1/locations/-/jobs":
			_, _ = w.Write([]byte(marshalAttrs(t, run.GoogleCloudRunV2ListJobsResponse{
				Jobs: []*run.GoogleCloudRunV2Job{{Name: jobName}},
			})))
		case "/v2/" + jobName + "/executions":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeCloudRunJobsService(t, srv)

	total, inserted, err := scanCloudRunJobsWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudRunJobsWithClient: %v (Executions' isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel)", err)
	}
	// Only the job lands; Executions' isAPINotEnabled-shaped 403 must warn
	// and continue, not abort the scan.
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanCloudRunJobs_EmptyProjectNoJobs(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v2/projects/proj1/locations/-/jobs": marshalAttrs(t, run.GoogleCloudRunV2ListJobsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeCloudRunJobsService(t, srv)

	total, inserted, err := scanCloudRunJobsWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudRunJobsWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
