package gcp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/bigqueryconnection/v1"
	"google.golang.org/api/option"
)

// fakeBQConnectionService builds a *bigqueryconnection.Service pointed at
// the fake server. Unlike bigquery/v2 (whose BasePath embeds the version),
// bigqueryconnection's default BasePath is just the bare host — the "v1/"
// segment lives in the per-method REST path template instead, so route keys
// below DO need the "/v1" prefix (opposite of the bigquery/compute gotcha).
func fakeBQConnectionService(t *testing.T, srv *httptest.Server) *bigqueryconnection.Service {
	t.Helper()
	svc, err := bigqueryconnection.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("bigqueryconnection.NewService: %v", err)
	}
	return svc
}

func TestScanBQConnections_HappyPath(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	connName := "projects/proj1/locations/us-central1/connections/conn1"
	srv := fakeGCPServer(t, map[string]string{
		"/v1/projects/proj1/locations/us-central1/connections": marshalAttrs(t, bigqueryconnection.ListConnectionsResponse{
			Connections: []*bigqueryconnection.Connection{
				{Name: connName, FriendlyName: "conn1", CloudSql: &bigqueryconnection.CloudSqlProperties{
					InstanceId: "proj1:us-central1:inst1",
				}},
			},
		}),
		"/v1/projects/proj1/locations/us-east1/connections": marshalAttrs(t, bigqueryconnection.ListConnectionsResponse{}),
	})
	svc := fakeBQConnectionService(t, srv)

	total, inserted, err := scanBQConnectionsIn(t.Context(), svc, p, st, testScanID, []string{"us-central1", "us-east1"})
	if err != nil {
		t.Fatalf("scanBQConnectionsIn: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}

	resID := store.ResourceID("gcp", p.ID, connName)
	res, err := st.GetResource(resID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if res == nil {
		t.Fatalf("connection not stored")
	}
	if res.Region == nil || *res.Region != "us-central1" {
		t.Errorf("Region = %v, want %q", res.Region, "us-central1")
	}
	if res.Name == nil || *res.Name != "conn1" {
		t.Errorf("Name = %v, want %q", res.Name, "conn1")
	}
}

func TestScanBQConnections_PermissionDeniedWarnsAndContinues(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	deniedBody := `{"error":{"code":403,"message":"caller does not have permission","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, deniedBody)
	svc := fakeBQConnectionService(t, srv)

	total, inserted, err := scanBQConnectionsIn(t.Context(), svc, p, st, testScanID, []string{"us-central1"})
	if err != nil {
		t.Fatalf("scanBQConnectionsIn: expected nil error on permission-denied warn-and-skip, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

func TestScanBQConnections_APINotEnabledSentinel(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	notEnabledBody := `{"error":{"code":403,"message":"BigQuery Connection API has not been used in project proj1 before or it is disabled"}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, notEnabledBody)
	svc := fakeBQConnectionService(t, srv)

	_, _, err := scanBQConnectionsIn(t.Context(), svc, p, st, testScanID, []string{"us-central1"})
	if err == nil {
		t.Fatalf("scanBQConnectionsIn: expected errServiceDisabled sentinel, got nil")
	}
	if !errors.Is(err, errServiceDisabled) {
		t.Fatalf("scanBQConnectionsIn: expected errServiceDisabled sentinel, got %v", err)
	}
}

func TestScanBQConnections_MultiRegionAccumulates(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	conn1 := "projects/proj1/locations/us-central1/connections/conn1"
	conn2 := "projects/proj1/locations/us-east1/connections/conn2"
	srv := fakeGCPServer(t, map[string]string{
		"/v1/projects/proj1/locations/us-central1/connections": marshalAttrs(t, bigqueryconnection.ListConnectionsResponse{
			Connections: []*bigqueryconnection.Connection{{Name: conn1}},
		}),
		"/v1/projects/proj1/locations/us-east1/connections": marshalAttrs(t, bigqueryconnection.ListConnectionsResponse{
			Connections: []*bigqueryconnection.Connection{{Name: conn2}},
		}),
	})
	svc := fakeBQConnectionService(t, srv)

	total, inserted, err := scanBQConnectionsIn(t.Context(), svc, p, st, testScanID, []string{"us-central1", "us-east1"})
	if err != nil {
		t.Fatalf("scanBQConnectionsIn: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}
}
