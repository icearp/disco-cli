// Package pairingtest holds the assertions every provider's resolver tests
// share: pair a synthetic scanner package against a fixture universe, then
// check pairings and diagnostics.
package pairingtest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/icearp/disco-cli/internal/sdkinv/pairing"
)

// WalkFixture extracts fixtureDir with e and pairs the scanner package in
// scannerDir against it, returning the result and its unpaired types.
func WalkFixture(t *testing.T, e sdkinv.Extractor, fixtureDir, scannerDir string) (*pairing.Result, map[string]string) {
	t.Helper()
	u, err := e.Extract(context.Background(), fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	return Walk(t, u, scannerDir)
}

// Walk pairs the scanner package in dir against u.
func Walk(t *testing.T, u *sdkinv.Universe, dir string) (*pairing.Result, map[string]string) {
	t.Helper()
	res, err := pairing.Walk(dir, u)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := pairing.Get(u.Provider)
	if !ok {
		t.Fatalf("no resolver registered for %s", u.Provider)
	}
	sdk, err := pairing.SDKFiles(dir, r)
	if err != nil {
		t.Fatal(err)
	}
	return res, res.Unpaired(res.Consts, sdk)
}

// Pairing is the part of a pairing the tests compare.
type Pairing struct {
	Func  string
	Kind  string
	Types []string
}

// PairingsFor returns the pairings for a candidate key or "other" label, by
// function.
func PairingsFor(res *pairing.Result, key string) map[string]Pairing {
	out := map[string]Pairing{}
	for _, p := range res.Pairings {
		if p.Key == key || (p.Key == "" && p.Label == key) {
			out[p.Func] = Pairing{p.Func, p.Kind, p.Types}
		}
	}
	return out
}

// DiagKinds counts the diagnostics by kind.
func DiagKinds(res *pairing.Result) map[string]int {
	m := map[string]int{}
	for _, d := range res.Diagnostics {
		m[d.Kind]++
	}
	return m
}

// ExpectPairing asserts fn pairs with key as kind, storing exactly types.
func ExpectPairing(t *testing.T, res *pairing.Result, key, fn, kind string, types ...string) {
	t.Helper()
	got, ok := PairingsFor(res, key)[fn]
	if !ok {
		t.Errorf("%s: no pairing from %s; have %v", key, fn, PairingsFor(res, key))
		return
	}
	sort.Strings(types)
	if got.Kind != kind || strings.Join(got.Types, ",") != strings.Join(types, ",") {
		t.Errorf("%s from %s = %s %v, want %s %v", key, fn, got.Kind, got.Types, kind, types)
	}
}

// ExpectDiag asserts a diagnostic of kind at file:line.
func ExpectDiag(t *testing.T, res *pairing.Result, kind, file string, line int) {
	t.Helper()
	for _, d := range res.Diagnostics {
		if d.Kind == kind && d.File == file && d.Line == line {
			return
		}
	}
	t.Errorf("no %s diagnostic at %s:%d; have %v", kind, file, line, res.Diagnostics)
}

// Dump renders the pairings one per line, for determinism checks.
func Dump(res *pairing.Result) string {
	var b strings.Builder
	for _, p := range res.Pairings {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%v\n", p.Func, p.Key, p.Op, p.Kind, p.Types)
	}
	return b.String()
}

// DiffLines reports the first differing line of two newline-separated dumps.
func DiffLines(a, b string) string {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range max(len(as), len(bs)) {
		x, y := "", ""
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			return fmt.Sprintf("line %d:\n-%s\n+%s", i+1, x, y)
		}
	}
	return "(no line differs)"
}
