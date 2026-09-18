package all

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/icearp/disco-cli/internal/sdkinv/conformance"
)

// TestExtractorsRegistered guards the blank-import wiring and the fetch specs
// every provider must ship: a pinned ref, at least one source, safe kinds.
func TestExtractorsRegistered(t *testing.T) {
	want := []string{"aws", "azure", "gcp"}
	got := sdkinv.Names()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("registered = %v, want %v", got, want)
	}
	for _, n := range got {
		e, _ := sdkinv.Get(n)
		if e.Ref() == "" || e.Ref() == "unknown" {
			t.Errorf("%s: empty or unknown ref %q", n, e.Ref())
		}
		spec := e.FetchSpec()
		if len(spec) == 0 {
			t.Errorf("%s: no fetch sources", n)
		}
		for _, s := range spec {
			if s.Name == "" || s.URL == "" || s.Dest == "" {
				t.Errorf("%s: incomplete source %+v", n, s)
			}
			if s.Kind == sdkinv.KindJSONIndex && s.Expand == nil {
				t.Errorf("%s/%s: json-index without Expand", n, s.Name)
			}
			if s.Expand != nil && s.ExpandID == "" {
				t.Errorf("%s/%s: Expand without an ExpandID; a cached snapshot cannot tell the index parse changed", n, s.Name)
			}
			if s.Keep != nil && s.KeepID == "" {
				t.Errorf("%s/%s: Keep filter without a KeepID; a cached snapshot cannot tell the filter changed", n, s.Name)
			}
			if s.Kind != sdkinv.KindJSONIndex && s.Keep == nil {
				t.Errorf("%s/%s: archive source without Keep filter", n, s.Name)
			}
		}
	}
}

// TestAllExtractorsConform runs the shared contract against every registered
// extractor's fixture, so a new provider is checked the moment it registers.
func TestAllExtractorsConform(t *testing.T) {
	for _, n := range sdkinv.Names() {
		e, _ := sdkinv.Get(n)
		t.Run(n, func(t *testing.T) {
			conformance.Check(t, e, filepath.Join("..", n, "testdata", "cache"))
		})
	}
}

// TestInventoryWalkTime bounds a full live extraction; skipped without the cache.
func TestInventoryWalkTime(t *testing.T) {
	cache := sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}
	for _, n := range sdkinv.Names() {
		e, _ := sdkinv.Get(n)
		dir := cache.Dir(n, e.Ref())
		if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
			t.Skipf("%s SDK cache not fetched", n)
		}
		start := time.Now()
		u, err := e.Extract(context.Background(), dir)
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		// A generous ceiling on purpose: the point is catching an accidental
		// quadratic walk, not policing seconds on a cold page cache or a
		// loaded CI runner. Azure parses ~30k generated Go files.
		if d := time.Since(start); d > 60*time.Second {
			t.Errorf("%s: extract took %s", n, d)
		}
		if len(u.Candidates) < 500 {
			t.Errorf("%s: only %d candidates", n, len(u.Candidates))
		}
	}
}
