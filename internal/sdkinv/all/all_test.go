package all

import (
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/sdkinv"
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
			if s.Kind != sdkinv.KindJSONIndex && s.Keep == nil {
				t.Errorf("%s/%s: archive source without Keep filter", n, s.Name)
			}
		}
	}
}
