package all_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/icearp/disco-cli/internal/providers"
	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/icearp/disco-cli/internal/sdkinv/conformance"
	"github.com/icearp/disco-cli/internal/sdkinv/pairing"

	_ "github.com/icearp/disco-cli/internal/providers/all"
)

// TestExtractorsRegistered guards the blank-import wiring and the fetch specs
// every provider must ship: a pinned ref, at least one source, safe kinds. The
// want list is the scanner registry, so a slim build checks only what it links.
func TestExtractorsRegistered(t *testing.T) {
	want := providers.Names()
	got := sdkinv.Names()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("registered extractors = %v, want the scanner providers %v", got, want)
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

// TestInventoriesConform runs the extractor contract over every registered
// provider's fixture and requires its pairing resolver, so a new provider cannot
// register an extractor that skips either. Fixtures live at the conventional
// <p>/<p>inventory/testdata/cache.
func TestInventoriesConform(t *testing.T) {
	for _, n := range sdkinv.Names() {
		t.Run(n, func(t *testing.T) {
			e, _ := sdkinv.Get(n)
			conformance.Check(t, e, filepath.Join("..", n, n+"inventory", "testdata", "cache"))
			if _, ok := pairing.Get(n); !ok {
				t.Errorf("%s: extractor registered without a pairing resolver", n)
			}
		})
	}
}

// TestLiveUniverseWellFormed runs the key contract and the determinism check
// against the real caches, not the fixtures: "resource-explorer-2/",
// "sagemaker/", "*/features" and "datazone/domain/" all shipped in
// docs/coverage.md while every fixture passed, and two extractions of the AWS
// cache in one process used to disagree on healthlake's Required.
func TestLiveUniverseWellFormed(t *testing.T) {
	cache := sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}
	classOf := map[string]sdkinv.Class{}
	services := map[string]bool{}
	for _, n := range sdkinv.Names() {
		e, _ := sdkinv.Get(n)
		dir := cache.Dir(n, e.Ref())
		if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
			t.Skipf("%s SDK cache not fetched", n)
		}
		t.Run(n, func(t *testing.T) {
			u, err := e.Extract(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			conformance.CheckUniverse(t, n, u)
			bad := 0
			for _, c := range u.Candidates {
				classOf[c.Key] = c.Class
				services[c.Service] = true
				if why := sdkinv.ValidateKey(c.Key, c.Service); why != "" && bad < 10 {
					bad++
					t.Errorf("malformed key %q (service %q): %s", c.Key, c.Service, why)
				}
			}
			again, err := e.Extract(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(u, again) {
				t.Error("two extractions of the live cache disagree")
			}
		})
	}
	checkReadmeClassExamples(t, classOf, services)
}

// classRowRe matches a row of the class table in internal/sdkinv/README.md: the class
// name, then its worked examples in the last cell.
var classRowRe = regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\|.*\\| ([^|]*`[^|]*) \\|$")

// checkReadmeClassExamples pins the README's worked class examples to the
// extractor's output. microsoft.compute/resourceskus and s3/bucket/encryption
// were never keys, and ec2/accountattribute had moved class, all while the
// README said a surprising row could be explained from it. An example whose
// service no extracted universe holds belongs to a provider this build did not
// link (slim tags), so it is skipped rather than failed.
func checkReadmeClassExamples(t *testing.T, classOf map[string]sdkinv.Class, services map[string]bool) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "sdkinv", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows := classRowRe.FindAllStringSubmatch(string(raw), -1)
	if len(rows) < 4 {
		t.Fatalf("found %d class rows in the README table", len(rows))
	}
	for _, row := range rows {
		for _, ex := range strings.Split(row[2], ",") {
			key := strings.Trim(strings.TrimSpace(ex), "`")
			if strings.Contains(key, "*") || !services[strings.SplitN(key, "/", 2)[0]] {
				continue
			}
			if got, ok := classOf[key]; !ok || string(got) != row[1] {
				t.Errorf("README example %s: class %q (present %v), want %s", key, got, ok, row[1])
			}
		}
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
