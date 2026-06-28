package gcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"codeberg.org/icearp/disco/internal/coverage"
)

// discoveryFake serves a Discovery API list whose per-API doc URLs point back
// at itself, so a test can control whether an individual doc fetch succeeds or
// fails. docStatus/docBody govern the "/compute" doc response.
func discoveryFake(t *testing.T, docStatus int, docBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/apis", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// "compute" is in relevantAPISet()'s always-on list, so it survives the
		// allow filter and gets a doc fetch.
		_, _ = w.Write([]byte(`{"items":[{"name":"compute","discoveryRestUrl":"` + srv.URL + `/compute","preferred":true}]}`))
	})
	mux.HandleFunc("/compute", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(docStatus)
		_, _ = w.Write([]byte(docBody))
	})
	t.Cleanup(srv.Close)
	return srv
}

// TestCoverageFetch_PerAPIDocFailurePropagates pins that a per-API Discovery doc
// fetch failure surfaces as a Fetch error rather than a silently-partial
// upstream (which the cmd layer would otherwise render as false upstream-missing
// rows). The cmd layer maps this to a fatal "registry unreachable" (exit 2).
func TestCoverageFetch_PerAPIDocFailurePropagates(t *testing.T) {
	srv := discoveryFake(t, http.StatusInternalServerError, `{"error":"boom"}`)
	orig := discoveryListURL
	discoveryListURL = srv.URL + "/apis"
	t.Cleanup(func() { discoveryListURL = orig })

	_, err := coverageProvider{}.Fetch(context.Background(), coverage.FetchOptions{})
	if err == nil {
		t.Fatal("per-API doc fetch failure must propagate as a Fetch error, got nil")
	}
}

// TestCoverageFetch_AllDocsOKNoError is the negative-space counterpart: when
// every doc fetch succeeds, Fetch returns the walked types and no error.
func TestCoverageFetch_AllDocsOKNoError(t *testing.T) {
	doc := `{"resources":{"instances":{"methods":{"list":{}}}}}`
	srv := discoveryFake(t, http.StatusOK, doc)
	orig := discoveryListURL
	discoveryListURL = srv.URL + "/apis"
	t.Cleanup(func() { discoveryListURL = orig })

	out, err := coverageProvider{}.Fetch(context.Background(), coverage.FetchOptions{})
	if err != nil {
		t.Fatalf("all docs OK should not error: %v", err)
	}
	if len(out) == 0 {
		t.Error("want at least one upstream type from the fetchable collection, got 0")
	}
}
