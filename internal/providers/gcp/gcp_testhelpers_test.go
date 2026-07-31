package gcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/icearp/disco-cli/store"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

// testScanID is the fixed scan ID inserted into every test database.
const testScanID = "00000000000000000000000000000000"

// newTestStore opens a temp SQLite DB for provider tests and inserts a scan
// record so resources satisfy the discovered_by FK.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("newTestStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.DB().Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("newTestStore: insert test scan: %v", err)
	}
	// Direction invariant — see aws_testhelper_test.go for rationale.
	t.Cleanup(func() {
		rows, err := st.ReversedContainsEdges()
		if err != nil {
			t.Errorf("ReversedContainsEdges: %v", err)
			return
		}
		if len(rows) > 0 {
			t.Errorf("reversed contains edges leaked: %d rows; first %+v", len(rows), rows[0])
		}
	})
	return st
}

// upsertTestResource inserts a minimal resource and returns its computed stable ID.
// Pass an empty region to leave Region unset.
func upsertTestResource(t *testing.T, st *store.Store, provider, accountID, rtype, nativeID, region, attrsJSON string) string {
	t.Helper()
	r := &store.Resource{
		Provider:       provider,
		AccountID:      accountID,
		Type:           rtype,
		NativeID:       nativeID,
		AttributesJSON: attrsJSON,
		DiscoveredBy:   testScanID,
	}
	if region != "" {
		r.Region = &region
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsertTestResource %s/%s: %v", rtype, nativeID, err)
	}
	return store.ResourceID(provider, accountID, nativeID)
}

// newTestProject returns a minimal project struct for resolver tests.
func newTestProject(id string) *project {
	return &project{ID: id, Name: "Test Project", Number: "123456789"}
}

// marshalAttrs returns the JSON encoding of v as the attrsJSON value scanners
// persist. Tests use real SDK structs (e.g. compute.Instance,
// compute.Firewall) so JSON-tag drift across google.golang.org/api/* SDK
// upgrades surfaces as a compile error, not a silent resolver edge-loss from
// hand-rolled JSON drifting out of sync with the discovery document.
func marshalAttrs(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalAttrs: %v", err)
	}
	return string(b)
}

// fakeGCPServer returns an httptest.Server whose handler dispatches by URL
// path to canned JSON responses supplied by the test. Unrouted paths fail
// the test loudly so silent zero-result regressions surface immediately
// rather than masquerading as legitimate empty pages.
//
// Each route value is the full HTTP body the discovery client unmarshals
// into the response page type. Use marshalAttrs to build it from the SDK
// struct (e.g. compute.ForwardingRuleAggregatedList).
func fakeGCPServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"error":{"code":404,"message":"no fake route"}}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeGCPServerStatus is the error-injection sibling of fakeGCPServer:
// returns the given HTTP status + JSON body for every request, so tests
// covering runPaginated's permission-denied / API-not-enabled branches can
// pin the exact googleapi.Error shape isPermissionDenied + isAPINotEnabled
// inspect.
func fakeGCPServerStatus(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeComputeService builds a *compute.Service pointed at the fake server.
// Mirrors the production clientOptions chain (option.WithEndpoint /
// WithHTTPClient) minus authentication. Returned client is concrete — same
// type production code uses, so no interface extraction needed (mirrors
// googleapis/google-cloud-go/testing.md fakes-over-mocks guidance).
func fakeComputeService(t *testing.T, srv *httptest.Server) *compute.Service {
	t.Helper()
	svc, err := compute.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("compute.NewService: %v", err)
	}
	return svc
}
