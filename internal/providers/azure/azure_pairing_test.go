package azure

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/sdkinv"
	// Registered for side effect: the SDK inventory extractors.
	_ "github.com/icearp/disco-cli/internal/providers/azure/azureinventory"
	"github.com/icearp/disco-cli/internal/sdkinv/pairing"
)

// pairScanners pairs this package's scanner source against the pinned Azure
// SDK inventory; skipped until `disco coverage sdk fetch` has populated the cache.
func pairScanners(t *testing.T) (*pairing.Result, map[string]string) {
	t.Helper()
	res, unpaired, err := pairing.Scan(context.Background(), sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}, "azure", ".")
	if errors.Is(err, sdkinv.ErrNotFetched) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return res, unpaired
}

// TestScannerOpLabelsResolve: every op label in a scanner names an operation
// the file's SDK imports ship, and the matching SDK call is in reach (the
// function, its helpers or its callers). SDK skew — a call the pinned
// snapshot no longer has — is logged, not fatal: it moves with the pin.
func TestScannerOpLabelsResolve(t *testing.T) {
	res, _ := pairScanners(t)
	fatal := map[string]bool{"label-no-op": true, "label-no-anchor": true, "label-malformed": true, "unresolved-receiver": true}
	skew := 0
	for _, d := range res.Diagnostics {
		if fatal[d.Kind] {
			t.Errorf("%s:%d %s: %s", d.File, d.Line, d.Kind, d.Message)
			continue
		}
		skew++
	}
	t.Logf("%d pairings, %d skew diagnostics", len(res.Pairings), skew)
	if absent := armModulesAbsentFromCache(t); len(absent) > 0 {
		// Not a failure: the pin is monorepo HEAD, so a module disco imports
		// can be one upstream deleted. Those ops can only ever read as skew.
		t.Logf("arm modules imported here but absent from the pinned snapshot: %v", absent)
	}
}

// armModulesAbsentFromCache lists the sdk/resourcemanager module directories
// this package imports that the pinned cache snapshot does not hold.
func armModulesAbsentFromCache(t *testing.T) []string {
	t.Helper()
	e, ok := sdkinv.Get("azure")
	if !ok {
		t.Fatal("azure extractor not registered")
	}
	root := filepath.Join(sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}.Dir("azure", e.Ref()), "repo", "sdk", "resourcemanager")
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read scanner package: %v", err)
	}
	fset := token.NewFileSet()
	seen, absent := map[string]bool{}, []string{}
	for _, fi := range files {
		name := fi.Name()
		if fi.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			_, rel, found := strings.Cut(path, "/sdk/resourcemanager/")
			if !found || seen[rel] {
				continue
			}
			seen[rel] = true
			dir := rel
			if base := filepath.Base(dir); strings.HasPrefix(base, "v") && base != "v" {
				dir = filepath.Dir(dir) // major-version suffix is not a directory upstream
			}
			if _, serr := os.Stat(filepath.Join(root, dir)); serr != nil {
				absent = append(absent, rel)
			}
		}
	}
	sort.Strings(absent)
	return absent
}

// TestEveryEmittedTypePaired: every Type* constant is stored by a function
// with an SDK call in reach, or is explained — no SDK module in its file,
// rows built from a non-listing op, or SDK skew.
func TestEveryEmittedTypePaired(t *testing.T) {
	res, unpaired := pairScanners(t)
	for typ, reason := range unpaired {
		if reason == "unexplained" {
			t.Errorf("%s: no SDK call pairs with it (stored by %v)", typ, res.StoredBy[typ])
		}
	}
	t.Logf("%d types explained without a candidate op", len(unpaired))
}
