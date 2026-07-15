package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func fakePubSubService(t *testing.T, srv *httptest.Server) *pubsub.Service {
	t.Helper()
	svc, err := pubsub.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("pubsub.NewService: %v", err)
	}
	return svc
}

func TestScanPubSub_FullChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	topicName := "projects/proj1/topics/topic1"
	subName := "projects/proj1/subscriptions/sub1"
	schemaName := "projects/proj1/schemas/schema1"
	snapName := "projects/proj1/snapshots/snap1"

	routes := map[string]string{
		"/v1/projects/proj1/topics": marshalAttrs(t, pubsub.ListTopicsResponse{
			Topics: []*pubsub.Topic{{Name: topicName}},
		}),
		"/v1/projects/proj1/subscriptions": marshalAttrs(t, pubsub.ListSubscriptionsResponse{
			Subscriptions: []*pubsub.Subscription{{Name: subName}},
		}),
		"/v1/projects/proj1/schemas": marshalAttrs(t, pubsub.ListSchemasResponse{
			Schemas: []*pubsub.Schema{{Name: schemaName}},
		}),
		"/v1/projects/proj1/snapshots": marshalAttrs(t, pubsub.ListSnapshotsResponse{
			Snapshots: []*pubsub.Snapshot{{Name: snapName}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakePubSubService(t, srv)

	total, inserted, err := scanPubSubWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanPubSubWithClient: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Fatalf("counts: got total=%d inserted=%d, want 4/4", total, inserted)
	}

	id := store.ResourceID("gcp", p.ID, snapName)
	res, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("GetResource(snapshot): %v", err)
	}
	if res == nil {
		t.Fatalf("snapshot %s not stored", snapName)
	}
}

func TestScanPubSub_SnapshotsAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	topicName := "projects/proj1/topics/topic1"
	notEnabledBody := `{"error":{"code":403,"message":"Cloud Pub/Sub API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/proj1/topics":
			_, _ = w.Write([]byte(marshalAttrs(t, pubsub.ListTopicsResponse{
				Topics: []*pubsub.Topic{{Name: topicName}},
			})))
		case "/v1/projects/proj1/subscriptions":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/projects/proj1/schemas":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/projects/proj1/snapshots":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakePubSubService(t, srv)

	total, inserted, err := scanPubSubWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanPubSubWithClient: %v (Snapshots' isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel)", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
}

func TestScanPubSub_EmptyProject(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1/projects/proj1/topics":        marshalAttrs(t, pubsub.ListTopicsResponse{}),
		"/v1/projects/proj1/subscriptions": marshalAttrs(t, pubsub.ListSubscriptionsResponse{}),
		"/v1/projects/proj1/schemas":       marshalAttrs(t, pubsub.ListSchemasResponse{}),
		"/v1/projects/proj1/snapshots":     marshalAttrs(t, pubsub.ListSnapshotsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakePubSubService(t, srv)

	total, inserted, err := scanPubSubWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanPubSubWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
