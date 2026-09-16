package azure

import (
	"context"
	"errors"
	"testing"

	"github.com/icearp/disco-cli/internal/sdkinv"
	// Registered for side effect: the SDK inventory extractors.
	_ "github.com/icearp/disco-cli/internal/sdkinv/all"
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
	fatal := map[string]bool{"label-no-op": true, "label-no-anchor": true, "unresolved-receiver": true}
	skew := 0
	for _, d := range res.Diagnostics {
		if fatal[d.Kind] {
			t.Errorf("%s:%d %s: %s", d.File, d.Line, d.Kind, d.Message)
			continue
		}
		skew++
	}
	t.Logf("%d pairings, %d skew diagnostics", len(res.Pairings), skew)
}

// TestEveryEmittedTypePaired: every Type* constant is stored by a function
// with an SDK call in reach, or is explained — no SDK module in its file,
// rows built from a non-listing op, or SDK skew.
func TestEveryEmittedTypePaired(t *testing.T) {
	_, unpaired := pairScanners(t)
	for typ, reason := range unpaired {
		if reason == "unexplained" {
			t.Errorf("%s: no SDK call pairs with it", typ)
		}
	}
	t.Logf("%d types explained without a candidate op", len(unpaired))
}
