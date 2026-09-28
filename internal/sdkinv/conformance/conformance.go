// Package conformance is the contract every extractor's fixture must satisfy.
// A new provider passes by registering: TestAllExtractorsConform in
// internal/sdkinv/all runs Check against internal/sdkinv/<name>/testdata/cache.
package conformance

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// Check extracts fixtureDir twice and asserts the universe is deterministic,
// complete (every class, depth 0 and depth 1, a parent for every child, every
// source operation accounted for) and well-formed (keys unique and
// service-prefixed, labels namespaced, every candidate backed by an
// operation).
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
	refs := false
	for _, c := range u.Candidates {
		if keys[c.Key] {
			t.Errorf("%s: duplicate key %s", e.Name(), c.Key)
		}
		keys[c.Key] = true
		classes[c.Class] = true
		depths[c.Depth] = true
		refs = refs || len(c.Refs) > 0
		checkCandidate(t, e.Name(), c)
	}
	for _, c := range u.Candidates {
		if c.Parent != "" && !keys[c.Parent] {
			t.Errorf("%s: %s parent %s not in fixture universe", e.Name(), c.Key, c.Parent)
		}
	}
	if !refs {
		t.Errorf("%s: fixture has no candidate with refs", e.Name())
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
	CheckUniverse(t, e.Name(), u)
}

// CheckUniverse holds for fixtures and live caches alike: every operation the
// sources declare is a candidate op, an Other op or a Drop with a reason,
// nothing else appears, and every op's scope is in the declared vocabulary.
func CheckUniverse(t *testing.T, name string, u *sdkinv.Universe) {
	t.Helper()
	for _, c := range u.Candidates {
		for _, o := range c.Ops {
			if o.Scope != "" && !slices.Contains(u.Scopes, o.Scope) {
				t.Errorf("%s: %s op %s scope %q is not in the universe's scope vocabulary", name, c.Key, o.Label, o.Scope)
			}
		}
	}
	if len(u.SourceOps) == 0 {
		t.Errorf("%s: no source operations enumerated", name)
	}
	missing, extra, conflicts := sdkinv.Unaccounted(u)
	for _, f := range []struct {
		what string
		refs []sdkinv.OpRef
	}{{"silently dropped", missing}, {"not declared by the sources", extra}, {"in more than one of candidates, Other and Dropped", conflicts}} {
		if len(f.refs) > 0 {
			t.Errorf("%s: %d operations %s, e.g. %v", name, len(f.refs), f.what, f.refs[:min(len(f.refs), 5)])
		}
	}
	for _, d := range u.Dropped {
		if d.Reason == "" {
			t.Errorf("%s: %v dropped without a reason", name, d.Op.Ref())
		}
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

// checkCandidate asserts one candidate's own shape.
func checkCandidate(t *testing.T, name string, c sdkinv.Candidate) {
	t.Helper()
	if c.Provider != name {
		t.Errorf("%s: candidate %s carries provider %q", name, c.Key, c.Provider)
	}
	if why := sdkinv.ValidateKey(c.Key, c.Service); why != "" {
		t.Errorf("%s: malformed key %q (service %q): %s", name, c.Key, c.Service, why)
	}
	if c.Class == "" || len(c.Ops) == 0 {
		t.Errorf("%s: %s has class %q and %d ops", name, c.Key, c.Class, len(c.Ops))
	}
	// A depth>0 candidate may name no parent: the template counts parent ids
	// the document tree has no listable node for. Depth 0 with a parent is
	// always wrong.
	if c.Depth == 0 && c.Parent != "" {
		t.Errorf("%s: %s depth %d parent %q", name, c.Key, c.Depth, c.Parent)
	}
	for _, o := range c.Ops {
		if o.Service != c.Service || o.Name == "" || o.Module == "" || !strings.Contains(o.Label, ":") {
			t.Errorf("%s: %s malformed op %+v", name, c.Key, o)
		}
	}
	if !slices.IsSorted(c.Refs) || len(slices.Compact(slices.Clone(c.Refs))) != len(c.Refs) {
		t.Errorf("%s: %s refs not sorted and unique: %v", name, c.Key, c.Refs)
	}
}
