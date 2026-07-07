package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/logging/v2"
	"google.golang.org/api/option"
)

// fakeLoggingService builds a *logging.Service pointed at the fake server.
// Like DNS, logging's route templates embed the full "v2/" prefix (not just
// bare paths) — route keys below need that exact prefix.
func fakeLoggingService(t *testing.T, srv *httptest.Server) *logging.Service {
	t.Helper()
	svc, err := logging.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("logging.NewService: %v", err)
	}
	return svc
}

func TestScanLoggingBuckets_BucketLinkViewFanout(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	b1Native := "projects/proj1/locations/global/buckets/b1"
	b2Native := "projects/proj1/locations/us-central1/buckets/b2"
	b1ID := store.ResourceID("gcp", p.ID, TypeLoggingBucket, b1Native)

	routes := map[string]string{
		"/v2/projects/proj1/locations/-/buckets": marshalAttrs(t, logging.ListBucketsResponse{
			Buckets: []*logging.LogBucket{{Name: b1Native}, {Name: b2Native}},
		}),
		"/v2/" + b1Native + "/links": marshalAttrs(t, logging.ListLinksResponse{
			Links: []*logging.Link{{Name: b1Native + "/links/l1"}},
		}),
		"/v2/" + b1Native + "/views": marshalAttrs(t, logging.ListViewsResponse{
			Views: []*logging.LogView{{Name: b1Native + "/views/v1"}},
		}),
		"/v2/" + b2Native + "/links": marshalAttrs(t, logging.ListLinksResponse{}),
		"/v2/" + b2Native + "/views": marshalAttrs(t, logging.ListViewsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeLoggingService(t, srv)

	bucketIDs, total, inserted, err := scanLoggingBuckets(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanLoggingBuckets: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("bucket counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}
	if len(bucketIDs) != 2 {
		t.Fatalf("bucketIDs: got %v, want 2 entries", bucketIDs)
	}

	total, inserted, err = scanLoggingBucketLinks(t.Context(), svc, p, bucketIDs, st, testScanID)
	if err != nil {
		t.Fatalf("scanLoggingBucketLinks: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("link counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	total, inserted, err = scanLoggingBucketViews(t.Context(), svc, p, bucketIDs, st, testScanID)
	if err != nil {
		t.Fatalf("scanLoggingBucketViews: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("view counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	linkID := store.ResourceID("gcp", p.ID, TypeLoggingLink, b1Native+"/links/l1")
	viewID := store.ResourceID("gcp", p.ID, TypeLoggingView, b1Native+"/views/v1")
	rels, err := st.RelationshipsFrom(b1ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var gotLink, gotView bool
	for _, r := range rels {
		if r.ToID == linkID {
			gotLink = true
		}
		if r.ToID == viewID {
			gotView = true
		}
	}
	if !gotLink {
		t.Errorf("link %s not found as child of bucket %s; got %+v", linkID, b1ID, rels)
	}
	if !gotView {
		t.Errorf("view %s not found as child of bucket %s; got %+v", viewID, b1ID, rels)
	}

	// b2 has no children — must not appear as a parent of anything.
	b2ID := store.ResourceID("gcp", p.ID, TypeLoggingBucket, b2Native)
	rels2, err := st.RelationshipsFrom(b2ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(b2): %v", err)
	}
	if len(rels2) != 0 {
		t.Errorf("bucket b2 should have no children, got %+v", rels2)
	}
}

// TestScanLoggingBucketLinks_PartialDenyContinues denies Links.List for one
// bucket while the same call succeeds for a sibling bucket, guarding
// against a regression where forEachItem's first-error-aborts-siblings
// behavior swallows the second bucket's results.
func TestScanLoggingBucketLinks_PartialDenyContinues(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	b1Native := "projects/proj1/locations/global/buckets/b1"
	b2Native := "projects/proj1/locations/global/buckets/b2"
	deniedBody := `{"error":{"code":403,"message":"caller is missing logging.links.list","errors":[{"reason":"forbidden"}]}}`

	// upsertWithParent's closure write silently no-ops when the parent row
	// doesn't already exist — seed both buckets directly since this test
	// exercises the Links fan-out phase standalone, without the preceding
	// Buckets phase that would normally insert them.
	upsertTestResource(t, st, "gcp", p.ID, TypeLoggingBucket, b1Native, "global", "{}")
	upsertTestResource(t, st, "gcp", p.ID, TypeLoggingBucket, b2Native, "global", "{}")

	b2LinksBody := marshalAttrs(t, logging.ListLinksResponse{
		Links: []*logging.Link{{Name: b2Native + "/links/l1"}},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/" + b1Native + "/links":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v2/" + b2Native + "/links":
			_, _ = w.Write([]byte(b2LinksBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeLoggingService(t, srv)

	total, inserted, err := scanLoggingBucketLinks(t.Context(), svc, p, []string{b1Native, b2Native}, st, testScanID)
	if err != nil {
		t.Fatalf("scanLoggingBucketLinks: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (b1 denied, b2 succeeds)", total, inserted)
	}
	linkID := store.ResourceID("gcp", p.ID, TypeLoggingLink, b2Native+"/links/l1")
	b2ID := store.ResourceID("gcp", p.ID, TypeLoggingBucket, b2Native)
	rels, err := st.RelationshipsFrom(b2ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var found bool
	for _, r := range rels {
		if r.ToID == linkID {
			found = true
		}
	}
	if !found {
		t.Errorf("link %s not found as child of bucket %s after b1 denied; got %+v", linkID, b2ID, rels)
	}
}

func TestScanLoggingFlatPhases_Basic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v2/projects/proj1/sinks": marshalAttrs(t, logging.ListSinksResponse{
			Sinks: []*logging.LogSink{{Name: "sink1"}},
		}),
		"/v2/projects/proj1/exclusions": marshalAttrs(t, logging.ListExclusionsResponse{
			Exclusions: []*logging.LogExclusion{{Name: "excl1"}},
		}),
		"/v2/projects/proj1/metrics": marshalAttrs(t, logging.ListLogMetricsResponse{
			Metrics: []*logging.LogMetric{{
				Name:         "nginx/requests",
				ResourceName: "projects/proj1/metrics/nginx%2Frequests",
			}},
		}),
		"/v2/projects/proj1/locations/global/logScopes": marshalAttrs(t, logging.ListLogScopesResponse{
			LogScopes: []*logging.LogScope{{Name: "projects/proj1/locations/global/logScopes/ls1"}},
		}),
		"/v2/projects/proj1/locations/-/savedQueries": marshalAttrs(t, logging.ListSavedQueriesResponse{
			SavedQueries: []*logging.SavedQuery{{Name: "projects/proj1/locations/global/savedQueries/sq1", DisplayName: "My Query"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeLoggingService(t, srv)

	check := func(label string, fn func() (int, int, error)) {
		t.Helper()
		total, inserted, err := fn()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if total != 1 || inserted != 1 {
			t.Errorf("%s counts: got total=%d inserted=%d, want 1/1", label, total, inserted)
		}
	}
	check("sinks", func() (int, int, error) { return scanLoggingSinks(t.Context(), svc, p, st, testScanID) })
	check("exclusions", func() (int, int, error) { return scanLoggingExclusions(t.Context(), svc, p, st, testScanID) })
	check("metrics", func() (int, int, error) { return scanLoggingMetrics(t.Context(), svc, p, st, testScanID) })
	check("logScopes", func() (int, int, error) { return scanLoggingLogScopes(t.Context(), svc, p, st, testScanID) })
	check("savedQueries", func() (int, int, error) { return scanLoggingSavedQueries(t.Context(), svc, p, st, testScanID) })

	// Metric.Name may contain "/" (SDK example: "nginx/requests") — NativeID
	// must come from the SDK-populated, already-URL-encoded ResourceName,
	// not a naive fmt.Sprintf join that would mis-nest the extra segment.
	metricID := store.ResourceID("gcp", p.ID, TypeLoggingMetric, "projects/proj1/metrics/nginx%2Frequests")
	res, err := st.GetResource(metricID)
	if err != nil {
		t.Fatalf("GetResource(metric): %v", err)
	}
	if res == nil {
		t.Fatalf("metric not stored under expected ResourceName-derived NativeID; got nothing at %s", metricID)
	}
}

func TestScanLoggingBuckets_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	body := `{"error":{"code":403,"message":"caller is missing logging permission","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeLoggingService(t, srv)

	bucketIDs, total, inserted, err := scanLoggingBuckets(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanLoggingBuckets (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 || len(bucketIDs) != 0 {
		t.Fatalf("got total=%d inserted=%d bucketIDs=%v, want 0/0/empty", total, inserted, bucketIDs)
	}
}
