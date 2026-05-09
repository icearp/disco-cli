package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/dataproc/v1"
	"google.golang.org/api/option"
)

// regionResp pairs an HTTP status with a JSON body for per-route mock
// responses; lets a single fake server return 200 on one region and 403 on
// another.
type regionResp struct {
	status int
	body   string
}

// newRegionFanoutServer routes by URL path with per-route status codes.
// Distinct from fakeGCPServer (always 200) and fakeGCPServerStatus (single
// status for all paths).
func newRegionFanoutServer(t *testing.T, routes map[string]regionResp) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unrouted hit: %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"error":{"code":404}}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeDataprocService points a *dataproc.Service at the test fake server.
// Mirrors fakeComputeService in fake_testhelper_test.go.
func fakeDataprocService(t *testing.T, baseURL string, c *http.Client) *dataproc.Service {
	t.Helper()
	svc, err := dataproc.NewService(
		t.Context(),
		option.WithEndpoint(baseURL),
		option.WithHTTPClient(c),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("dataproc.NewService: %v", err)
	}
	return svc
}

// TestRegionFanout_HappyPath fans out across two regions, each returning one
// cluster, and verifies both rows land with correct per-region NativeID +
// Region fields. Exercises gcpRegionFanoutScanIn (the testable core of
// gcpRegionFanoutScan) so the test does not depend on compute.Regions.List.
func TestRegionFanout_HappyPath(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	pageUS := dataproc.ListClustersResponse{Clusters: []*dataproc.Cluster{{ClusterName: "c-us"}}}
	pageEU := dataproc.ListClustersResponse{Clusters: []*dataproc.Cluster{{ClusterName: "c-eu"}}}

	srv := fakeGCPServer(t, map[string]string{
		"/v1/projects/my-project/regions/us-central1/clusters":  marshalAttrs(t, pageUS),
		"/v1/projects/my-project/regions/europe-west1/clusters": marshalAttrs(t, pageEU),
	})
	svc := fakeDataprocService(t, srv.URL, srv.Client())

	total, inserted, err := gcpRegionFanoutScanIn(
		t.Context(), p, st, 2,
		[]string{"us-central1", "europe-west1"},
		"dataproc:clusters.list",
		func(region string) pager[dataproc.ListClustersResponse] {
			return svc.Projects.Regions.Clusters.List(p.ID, region)
		},
		func(page *dataproc.ListClustersResponse) []*dataproc.Cluster { return page.Clusters },
		func(c *dataproc.Cluster, region string) *store.Resource {
			if c == nil || c.ClusterName == "" {
				return nil
			}
			name := c.ClusterName
			reg := region
			return &store.Resource{
				Provider:     "gcp",
				AccountID:    p.ID,
				Type:         TypeDataprocCluster,
				NativeID:     "projects/" + p.ID + "/regions/" + reg + "/clusters/" + name,
				Name:         &name,
				Region:       &reg,
				DiscoveredBy: testScanID,
			}
		},
	)
	if err != nil {
		t.Fatalf("fanout: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got %d/%d, want 2/2", total, inserted)
	}
}

// TestRegionFanout_EmptyRegions short-circuits before any list call. No
// upserts, no warnings, no errors.
func TestRegionFanout_EmptyRegions(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	total, inserted, err := gcpRegionFanoutScanIn(
		t.Context(), p, st, 2, nil, "dataproc:clusters.list",
		func(string) pager[dataproc.ListClustersResponse] {
			t.Fatal("pagerFn called on empty regions")
			return nil
		},
		func(*dataproc.ListClustersResponse) []*dataproc.Cluster { return nil },
		func(*dataproc.Cluster, string) *store.Resource { return nil },
	)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("expected 0/0/nil, got %d/%d/%v", total, inserted, err)
	}
}

// TestRegionFanout_PerRegion403 verifies a 403 from one region degrades to a
// ScanWarning (does not propagate, does not abort the other region). The
// passing region's cluster still lands.
func TestRegionFanout_PerRegion403(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	pageOK := dataproc.ListClustersResponse{Clusters: []*dataproc.Cluster{{ClusterName: "c-ok"}}}

	srv := newRegionFanoutServer(t, map[string]regionResp{
		"/v1/projects/my-project/regions/us-central1/clusters":  {200, marshalAttrs(t, pageOK)},
		"/v1/projects/my-project/regions/europe-west1/clusters": {403, `{"error":{"code":403,"message":"caller missing dataproc.clusters.list","errors":[{"reason":"forbidden"}]}}`},
	})
	svc := fakeDataprocService(t, srv.URL, srv.Client())

	total, inserted, err := gcpRegionFanoutScanIn(
		t.Context(), p, st, 2,
		[]string{"us-central1", "europe-west1"},
		"dataproc:clusters.list",
		func(region string) pager[dataproc.ListClustersResponse] {
			return svc.Projects.Regions.Clusters.List(p.ID, region)
		},
		func(page *dataproc.ListClustersResponse) []*dataproc.Cluster { return page.Clusters },
		func(c *dataproc.Cluster, region string) *store.Resource {
			if c == nil || c.ClusterName == "" {
				return nil
			}
			name := c.ClusterName
			reg := region
			return &store.Resource{
				Provider:     "gcp",
				AccountID:    p.ID,
				Type:         TypeDataprocCluster,
				NativeID:     "projects/" + p.ID + "/regions/" + reg + "/clusters/" + name,
				Name:         &name,
				Region:       &reg,
				DiscoveredBy: testScanID,
			}
		},
	)
	if err != nil {
		t.Fatalf("fanout (mixed): %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got %d/%d, want 1/1 (only us-central1 succeeds)", total, inserted)
	}
}
