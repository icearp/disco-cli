package gcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/icearp/disco-cli/internal/coverage"
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

func TestSingularize(t *testing.T) {
	cases := map[string]string{
		"addresses": "address",
		"aliases":   "alias",
		"boxes":     "box",
		"branches":  "branch",
		"indexes":   "index",
		"instances": "instance",
		"policies":  "policy",
		"keys":      "key",
		"services":  "service",
		"disks":     "disk",
		// Exception: "databases" ends in the identical "-ases" suffix as
		// "aliases" but its true singular ends in a silent "e", not a
		// sibilant — no suffix-only rule distinguishes them.
		"databases": "database",
		// Exception: "snoozes" ends in "-zes"; the sibilant-stem rule reads
		// "snooz" (ends in "z") as a genuine sibilant stem and strips "-es",
		// but the true singular is "snooze" (silent-e word, "+s" plural).
		"snoozes": "snooze",
	}
	for in, want := range cases {
		if got := singularize(in); got != want {
			t.Errorf("singularize(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLeafTypesNotResolverSources guards against marking a type as leaf
// (Leaf: true on the scanner's emits decl) when a resolver actually emits
// edges from it. Such a misclassification silently hides the type from
// `disco coverage resolvers --missing` without a resolver existing —
// bug-attractant. Mirrors aws.TestLeafTypesNotResolverSources.
func TestLeafTypesNotResolverSources(t *testing.T) {
	sources := make(map[string]bool)
	for _, s := range ResolverEdgeSources() {
		sources[s] = true
	}
	for _, decl := range CollectEmits() {
		if !decl.Leaf {
			continue
		}
		if sources[decl.DiscoType] {
			t.Errorf("emits[%q] flagged Leaf: true but type appears as resolver source — drop the Leaf flag or remove the resolver", decl.DiscoType)
		}
	}
}
