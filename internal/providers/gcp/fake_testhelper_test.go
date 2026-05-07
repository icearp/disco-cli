package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

// fakeGCPServer returns an httptest.Server whose handler dispatches by URL
// path to canned JSON responses supplied by the test. Unrouted paths fail
// the test loudly so silent zero-result regressions surface immediately
// rather than masquerading as legitimate empty pages.
//
// Each route value is the full HTTP body the discovery client will receive
// and unmarshal into the response page type. Use the helper marshalAttrs to
// build it from the SDK struct (e.g. compute.ForwardingRuleAggregatedList).
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

// fakeGCPServerStatus is the error-injection sibling of fakeGCPServer. It
// returns the supplied HTTP status + JSON body for every request, so tests
// covering the permission-denied / API-not-enabled branches of runPaginated
// can pin the exact googleapi.Error shape isPermissionDenied + isAPINotEnabled
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
// WithHTTPClient) minus authentication. The returned client is concrete —
// tests retain the same type production code uses, so no interface
// extraction is required (mirrors googleapis/google-cloud-go/testing.md
// fakes-over-mocks guidance).
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
