package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/container/v1"
	"google.golang.org/api/option"
)

func fakeGKEService(t *testing.T, srv *httptest.Server) *container.Service {
	t.Helper()
	svc, err := container.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("container.NewService: %v", err)
	}
	return svc
}

func TestScanGKE_ClusterNodePoolChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	clusterSelfLink := "https://container.googleapis.com/v1/projects/proj1/locations/us-central1/clusters/cluster1"
	npSelfLink := clusterSelfLink + "/nodePools/np1"

	routes := map[string]string{
		"/v1/projects/proj1/locations/-/clusters": marshalAttrs(t, container.ListClustersResponse{
			Clusters: []*container.Cluster{{Name: "cluster1", Location: "us-central1", SelfLink: clusterSelfLink}},
		}),
		"/v1/projects/proj1/locations/us-central1/clusters/cluster1/nodePools": marshalAttrs(t, container.ListNodePoolsResponse{
			NodePools: []*container.NodePool{{Name: "np1", SelfLink: npSelfLink}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeGKEService(t, srv)

	total, inserted, err := scanGKEWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanGKEWithClient: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	clusterID := store.ResourceID("gcp", p.ID, clusterSelfLink)
	npID := store.ResourceID("gcp", p.ID, npSelfLink)
	if res, err := st.GetResource(npID); err != nil || res == nil {
		t.Fatalf("GetResource(nodepool): res=%v err=%v", res, err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(cluster): %v", err)
	}
	if len(rels) == 0 {
		t.Errorf("expected cluster to contain the node pool row via hierarchy closure, got none")
	}
}

func TestScanGKE_NodePoolsAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	clusterSelfLink := "https://container.googleapis.com/v1/projects/proj1/locations/us-central1/clusters/cluster1"
	notEnabledBody := `{"error":{"code":403,"message":"Kubernetes Engine API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/proj1/locations/-/clusters":
			_, _ = w.Write([]byte(marshalAttrs(t, container.ListClustersResponse{
				Clusters: []*container.Cluster{{Name: "cluster1", Location: "us-central1", SelfLink: clusterSelfLink}},
			})))
		case "/v1/projects/proj1/locations/us-central1/clusters/cluster1/nodePools":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeGKEService(t, srv)

	total, inserted, err := scanGKEWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanGKEWithClient: %v (NodePools' isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel)", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanGKE_EmptyProjectNoClusters(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1/projects/proj1/locations/-/clusters": marshalAttrs(t, container.ListClustersResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeGKEService(t, srv)

	total, inserted, err := scanGKEWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanGKEWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
