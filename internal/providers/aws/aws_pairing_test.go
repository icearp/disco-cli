package aws

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/icearp/disco-cli/internal/untaggable"

	// Registered for side effect: the SDK inventory extractors.
	_ "github.com/icearp/disco-cli/internal/providers/aws/awsinventory"
	"github.com/icearp/disco-cli/internal/sdkinv/pairing"
)

// pairScanners pairs this package's scanner source against the pinned AWS
// SDK inventory; skipped until `disco coverage sdk fetch` has populated the cache.
func pairScanners(t *testing.T) (*pairing.Result, map[string]string) {
	t.Helper()
	res, unpaired, err := pairing.Scan(context.Background(), sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}, "aws", ".")
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

// TestUntaggableMatchesServiceReference: a type's Untaggable declaration agrees
// with what the extractor derives for it. The type reaches a candidate through
// the coverage matrix (the row whose display type it is). The candidate is
// taggable when its matched Service Reference resource carries tags (a
// tagging-only action names it, or its policy can test its tags) or when one
// of its SDK operations carries a tags member; it is untaggable only when the
// catalog matched it and neither holds. A type whose candidates the extractor
// says nothing about keeps whatever it declares.
func TestUntaggableMatchesServiceReference(t *testing.T) {
	in, err := coverage.InputsFromCache(context.Background(), sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}, "aws", CollectEmits(), ".")
	if errors.Is(err, sdkinv.ErrNotFetched) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	taggable := map[string]*bool{}
	for _, c := range in.Universe.Candidates {
		taggable[c.Key] = c.Taggable
	}
	answersOf := map[string][]bool{} // type -> one answer per row it displays on
	for _, row := range coverage.BuildInventory(in).Rows {
		if tg := taggable[row.Key]; row.DiscoType != "" && tg != nil {
			answersOf[row.DiscoType] = append(answersOf[row.DiscoType], *tg)
		}
	}
	// Anchors, so an extractor that answers nothing cannot pass: the Service
	// Reference lists no way to tag an IAM group or a KMS alias (both marked
	// untaggable), and EC2 instances carry tags.
	for typ, want := range map[string]bool{TypeIAMGroup: false, TypeKMSAlias: false, TypeEC2Instance: true} {
		answers, ok := answersOf[typ]
		if !ok || slices.Contains(answers, true) != want {
			t.Errorf("%s: taggable answers = %v (answered %v); want taggable = %v", typ, answers, ok, want)
		}
	}
	if !untaggable.Is(TypeKMSAlias) {
		t.Errorf("%s: not marked Untaggable; the anchor above needs a marked type", TypeKMSAlias)
	}
	var unmatched []string
	for _, d := range CollectEmits() {
		answers, ok := answersOf[d.DiscoType]
		if !ok {
			unmatched = append(unmatched, d.DiscoType)
			continue
		}
		wantUntaggable := !slices.Contains(answers, true)
		if got := untaggable.Is(d.DiscoType); got != wantUntaggable {
			t.Errorf("%s: Untaggable = %v; the Service Reference and SDK model say untaggable = %v", d.DiscoType, got, wantUntaggable)
		}
	}
	slices.Sort(unmatched)
	t.Logf("%d of %d types have no tagging answer from the extractor and keep their declaration: %v", len(unmatched), len(CollectEmits()), unmatched)
}
