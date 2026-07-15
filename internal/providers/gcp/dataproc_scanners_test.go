package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/dataproc/v1"
)

// fakeDataprocService (defined in gcp_scan_helpers_test.go) points a
// *dataproc.Service at a fake server. Route templates embed the full "v1/"
// prefix.

func TestScanDataprocIn_AllSixSecondaryTypesBasic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	apNative := "projects/proj1/regions/us-central1/autoscalingPolicies/ap1"
	batchNative := "projects/proj1/locations/us-central1/batches/b1"
	sessionNative := "projects/proj1/locations/us-central1/sessions/s1"
	sessionTmplNative := "projects/proj1/locations/us-central1/sessionTemplates/st1"
	workflowTmplNative := "projects/proj1/regions/us-central1/workflowTemplates/wt1"

	routes := map[string]string{
		"/v1/projects/proj1/regions/us-central1/clusters": marshalAttrs(t, dataproc.ListClustersResponse{
			Clusters: []*dataproc.Cluster{{ClusterName: "c1"}},
		}),
		"/v1/projects/proj1/regions/us-central1/autoscalingPolicies": marshalAttrs(t, dataproc.ListAutoscalingPoliciesResponse{
			Policies: []*dataproc.AutoscalingPolicy{{Name: apNative}},
		}),
		"/v1/projects/proj1/locations/us-central1/batches": marshalAttrs(t, dataproc.ListBatchesResponse{
			Batches: []*dataproc.Batch{{Name: batchNative, State: "SUCCEEDED"}},
		}),
		"/v1/projects/proj1/locations/us-central1/sessions": marshalAttrs(t, dataproc.ListSessionsResponse{
			Sessions: []*dataproc.Session{{Name: sessionNative, State: "ACTIVE"}},
		}),
		"/v1/projects/proj1/locations/us-central1/sessionTemplates": marshalAttrs(t, dataproc.ListSessionTemplatesResponse{
			SessionTemplates: []*dataproc.SessionTemplate{{Name: sessionTmplNative}},
		}),
		"/v1/projects/proj1/regions/us-central1/workflowTemplates": marshalAttrs(t, dataproc.ListWorkflowTemplatesResponse{
			Templates: []*dataproc.WorkflowTemplate{{Name: workflowTmplNative}},
		}),
		"/v1/projects/proj1/regions/us-central1/jobs": marshalAttrs(t, dataproc.ListJobsResponse{
			Jobs: []*dataproc.Job{{
				Reference: &dataproc.JobReference{ProjectId: "proj1", JobId: "j1"},
				Status:    &dataproc.JobStatus{State: "DONE"},
			}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeDataprocService(t, srv.URL, srv.Client())

	total, inserted, err := scanDataprocIn(t.Context(), svc, p, st, testScanID, []string{"us-central1"})
	if err != nil {
		t.Fatalf("scanDataprocIn: %v", err)
	}
	// 1 cluster + 1 autoscaling policy + 1 batch + 1 session + 1 session template + 1 workflow template + 1 job.
	if total != 7 || inserted != 7 {
		t.Fatalf("counts: got total=%d inserted=%d, want 7/7", total, inserted)
	}

	apID := store.ResourceID("gcp", p.ID, apNative)
	res, err := st.GetResource(apID)
	if err != nil {
		t.Fatalf("GetResource(autoscaling policy): %v", err)
	}
	if res == nil || res.Region == nil || *res.Region != "us-central1" {
		t.Errorf("autoscaling policy region: got %+v, want Region=us-central1", res)
	}

	jobID := store.ResourceID("gcp", p.ID, "projects/proj1/regions/us-central1/jobs/j1")
	jobRes, err := st.GetResource(jobID)
	if err != nil {
		t.Fatalf("GetResource(job): %v", err)
	}
	if jobRes == nil {
		t.Fatalf("job not stored")
	}
	if jobRes.Status == nil || *jobRes.Status != "DONE" {
		t.Errorf("job status: got %+v, want DONE", jobRes.Status)
	}

	batchID := store.ResourceID("gcp", p.ID, batchNative)
	batchRes, err := st.GetResource(batchID)
	if err != nil {
		t.Fatalf("GetResource(batch): %v", err)
	}
	if batchRes == nil || batchRes.Status == nil || *batchRes.Status != "SUCCEEDED" {
		t.Errorf("batch status: got %+v, want SUCCEEDED", batchRes)
	}
}

func TestScanDataprocIn_JobWithoutReferenceSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1/projects/proj1/regions/us-central1/clusters":            marshalAttrs(t, dataproc.ListClustersResponse{}),
		"/v1/projects/proj1/regions/us-central1/autoscalingPolicies": marshalAttrs(t, dataproc.ListAutoscalingPoliciesResponse{}),
		"/v1/projects/proj1/locations/us-central1/batches":           marshalAttrs(t, dataproc.ListBatchesResponse{}),
		"/v1/projects/proj1/locations/us-central1/sessions":          marshalAttrs(t, dataproc.ListSessionsResponse{}),
		"/v1/projects/proj1/locations/us-central1/sessionTemplates":  marshalAttrs(t, dataproc.ListSessionTemplatesResponse{}),
		"/v1/projects/proj1/regions/us-central1/workflowTemplates":   marshalAttrs(t, dataproc.ListWorkflowTemplatesResponse{}),
		"/v1/projects/proj1/regions/us-central1/jobs": marshalAttrs(t, dataproc.ListJobsResponse{
			Jobs: []*dataproc.Job{{Reference: nil}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeDataprocService(t, srv.URL, srv.Client())

	total, inserted, err := scanDataprocIn(t.Context(), svc, p, st, testScanID, []string{"us-central1"})
	if err != nil {
		t.Fatalf("scanDataprocIn: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0 (job with nil Reference must be skipped, not panic)", total, inserted)
	}
}

func TestScanDataprocIn_PartialRegionDenyContinues(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	deniedBody := `{"error":{"code":403,"message":"caller is missing dataproc.clusters.list","errors":[{"reason":"forbidden"}]}}`
	c2Native := "c2"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/proj1/regions/us-central1/clusters":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v1/projects/proj1/regions/us-central1/autoscalingPolicies",
			"/v1/projects/proj1/locations/us-central1/batches",
			"/v1/projects/proj1/locations/us-central1/sessions",
			"/v1/projects/proj1/locations/us-central1/sessionTemplates",
			"/v1/projects/proj1/regions/us-central1/workflowTemplates",
			"/v1/projects/proj1/regions/us-central1/jobs":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/projects/proj1/regions/us-east1/clusters":
			_, _ = w.Write([]byte(marshalAttrs(t, dataproc.ListClustersResponse{
				Clusters: []*dataproc.Cluster{{ClusterName: c2Native}},
			})))
		case "/v1/projects/proj1/regions/us-east1/autoscalingPolicies",
			"/v1/projects/proj1/locations/us-east1/batches",
			"/v1/projects/proj1/locations/us-east1/sessions",
			"/v1/projects/proj1/locations/us-east1/sessionTemplates",
			"/v1/projects/proj1/regions/us-east1/workflowTemplates",
			"/v1/projects/proj1/regions/us-east1/jobs":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeDataprocService(t, srv.URL, srv.Client())

	total, inserted, err := scanDataprocIn(t.Context(), svc, p, st, testScanID, []string{"us-central1", "us-east1"})
	if err != nil {
		t.Fatalf("scanDataprocIn: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (us-central1 clusters denied, us-east1 succeeds)", total, inserted)
	}

	c2ID := store.ResourceID("gcp", p.ID, "projects/proj1/regions/us-east1/clusters/"+c2Native)
	res, err := st.GetResource(c2ID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res == nil {
		t.Fatalf("cluster in us-east1 not stored after us-central1 denied")
	}
}
