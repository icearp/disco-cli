// Package conformance is the contract every extractor's fixture must satisfy.
// A new provider passes by registering: TestAllExtractorsConform in
// internal/sdkinv/all runs Check against internal/sdkinv/<name>/testdata/cache.
package conformance

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// Check extracts fixtureDir twice and asserts the universe is deterministic,
// complete (every class, depth 0 and depth 1, a parent for every child) and
// well-formed (keys unique and service-prefixed, labels namespaced, every
// candidate backed by an operation).
func Check(t *testing.T, e sdkinv.Extractor, fixtureDir string) {
	t.Helper()
	start := time.Now()
	u, err := e.Extract(context.Background(), fixtureDir)
	if err != nil {
		t.Fatalf("%s: extract: %v", e.Name(), err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("%s: fixture extract took %s", e.Name(), d)
	}
	again, err := e.Extract(context.Background(), fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(u, again) {
		t.Errorf("%s: extract is not deterministic", e.Name())
	}
	if u.Provider != e.Name() {
		t.Errorf("%s: universe provider = %q", e.Name(), u.Provider)
	}
	if len(u.Pins) == 0 {
		t.Errorf("%s: no pins recorded", e.Name())
	}
	if e.Ref() == "" {
		t.Errorf("%s: empty ref", e.Name())
	}
	if len(e.FetchSpec()) == 0 {
		t.Errorf("%s: empty fetch spec", e.Name())
	}
	keys := map[string]bool{}
	classes := map[sdkinv.Class]bool{}
	depths := map[int]bool{}
	for _, c := range u.Candidates {
		if keys[c.Key] {
			t.Errorf("%s: duplicate key %s", e.Name(), c.Key)
		}
		keys[c.Key] = true
		classes[c.Class] = true
		depths[c.Depth] = true
		if c.Provider != e.Name() || c.Service == "" || !strings.HasPrefix(c.Key, c.Service+"/") {
			t.Errorf("%s: malformed candidate %+v", e.Name(), c)
		}
		if c.Class == "" || len(c.Ops) == 0 {
			t.Errorf("%s: %s has class %q and %d ops", e.Name(), c.Key, c.Class, len(c.Ops))
		}
		if (c.Depth > 0) != (c.Parent != "") {
			t.Errorf("%s: %s depth %d parent %q", e.Name(), c.Key, c.Depth, c.Parent)
		}
		for _, o := range c.Ops {
			if o.Service != c.Service || o.Name == "" || o.Module == "" || !strings.Contains(o.Label, ":") {
				t.Errorf("%s: %s malformed op %+v", e.Name(), c.Key, o)
			}
		}
	}
	for _, c := range u.Candidates {
		if c.Parent != "" && !keys[c.Parent] {
			t.Errorf("%s: %s parent %s not in fixture universe", e.Name(), c.Key, c.Parent)
		}
	}
	for _, cl := range []sdkinv.Class{sdkinv.ClassResource, sdkinv.ClassCatalog, sdkinv.ClassNonResource} {
		if !classes[cl] {
			t.Errorf("%s: fixture has no %s candidate", e.Name(), cl)
		}
	}
	if !depths[0] || !depths[1] {
		t.Errorf("%s: fixture lacks depth 0 and depth 1 candidates: %v", e.Name(), depths)
	}
	if !sortedKeys(u.Candidates) {
		t.Errorf("%s: candidates not sorted", e.Name())
	}
}

func sortedKeys(cs []sdkinv.Candidate) bool {
	for i := 1; i < len(cs); i++ {
		if cs[i-1].Key > cs[i].Key {
			return false
		}
	}
	return true
}
