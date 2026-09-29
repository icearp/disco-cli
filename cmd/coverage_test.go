package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/providers/aws/awsinventory"
	"github.com/icearp/disco-cli/internal/sdkinv"
)

// resetCoverageFlags clears StringSlice flags on every coverage subcommand
// before each test: pflag's StringSlice values accumulate across consecutive
// Execute() calls in the same process, so providers/regions/services would
// otherwise carry stale entries from a prior run; bools (--missing,
// --with-refs) would stay set for the next test.
func resetCoverageFlags(t *testing.T) {
	t.Helper()
	subs := coverageCmd.Commands()
	for _, sub := range coverageCmd.Commands() {
		subs = append(subs, sub.Commands()...) // sdk fetch|status carry their own --providers
	}
	for _, sub := range subs {
		sub.Flags().VisitAll(func(fl *pflag.Flag) {
			if sv, ok := fl.Value.(interface{ Replace([]string) error }); ok {
				_ = sv.Replace(nil) // a slice's DefValue is "[]", which Set would append
			} else {
				_ = fl.Value.Set(fl.DefValue)
			}
			fl.Changed = false
		})
	}
}

// fixtureCache stages the AWS extractor's synthetic SDK fixture as a populated
// cache snapshot, so `coverage services` runs offline against a two-service
// universe instead of the 400-service live one.
func fixtureCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := sdkinv.Cache{Root: root}.Dir("aws", awsinventory.SDKRef)
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "internal", "providers", "aws", "awsinventory", "testdata", "cache"))); err != nil {
		t.Fatal(err)
	}
	e, ok := sdkinv.Get("aws")
	if !ok {
		t.Fatal("aws extractor not registered")
	}
	manifest, _ := json.Marshal(sdkinv.Manifest{Provider: "aws", Ref: awsinventory.SDKRef, Spec: sdkinv.SpecFingerprint(e.FetchSpec())})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// fixtureSourceRoot stages awsinventory's synthetic AWS scanner
// package as <root>/internal/providers/aws, so the gates can run against the
// fixture universe without parsing the 400-service real scanner tree.
func fixtureSourceRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dst := filepath.Join(root, "internal", "providers", "aws")
	src := filepath.Join("..", "internal", "providers", "aws", "awsinventory", "testdata", "scannerpkg")
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestCoverageServices_CacheAbsentExits2: without a populated SDK cache the
// inventory cannot be derived; the distinct sentinel maps to exit 2 and the
// message tells the operator how to populate it.
func TestCoverageServices_CacheAbsentExits2(t *testing.T) {
	resetCoverageFlags(t)
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--providers", "aws", "--sdk-cache", t.TempDir(), "--source-root="})
		return cmd.Execute()
	})
	if !errors.Is(err, errCoverageInventoryUnavailable) {
		t.Fatalf("want errCoverageInventoryUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "disco coverage sdk fetch") {
		t.Errorf("error must name the fetch command: %v", err)
	}
}

// TestCoverageServices_Offline runs the whole services path against the
// fixture cache with pairing unavailable: every fixture candidate is either
// name-matched or uncovered and every emitted type is disco-only with the
// pairing-unavailable reason. The gates are not exercised here — without
// pairing they are refused (TestCoverageServices_GatesNeedPairing).
func TestCoverageServices_Offline(t *testing.T) {
	root := fixtureCache(t)
	resetCoverageFlags(t)
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--providers", "aws", "--sdk-cache", root, "--source-root=", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("services: %v", err)
	}
	var matrices []coverage.Matrix
	if err := json.Unmarshal([]byte(out), &matrices); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(matrices) != 1 || matrices[0].Provider != "aws" || matrices[0].Pairing {
		t.Fatalf("matrices = %+v", matrices)
	}
	m := matrices[0]
	if m.Pins["aws-sdk-go-v2"] != awsinventory.SDKRef {
		t.Errorf("pins = %v", m.Pins)
	}
	if m.Summary.Uncovered == 0 || m.Summary.Unexplained != 0 {
		t.Errorf("summary = %+v", m.Summary)
	}
	var sawUncovered, sawDiscoOnly bool
	for _, r := range m.Rows {
		switch r.Bucket {
		case coverage.BucketUncovered:
			sawUncovered = r.Key == "widgets/widget" && len(r.Ops) > 0 || sawUncovered
		case coverage.BucketDiscoOnly:
			sawDiscoOnly = true
			if r.Reason != coverage.ReasonPairingUnavailable {
				t.Errorf("disco-only %s reason = %q", r.DiscoType, r.Reason)
			}
		}
	}
	if !sawUncovered || !sawDiscoOnly {
		t.Errorf("rows lack widgets/widget uncovered (%v) or a disco-only row (%v)", sawUncovered, sawDiscoOnly)
	}
}

// TestCoverageServices_FiltersAndFormats: --filter narrows rows without
// touching the headline, and every documented format renders.
func TestCoverageServices_FiltersAndFormats(t *testing.T) {
	root := fixtureCache(t)
	for _, tc := range []struct {
		format, filter, want string
	}{
		{"table", "uncovered", "aws: **Coverage:** 0.0%"},
		{"markdown", "gaps", "### Uncovered"},
		{"csv", "all", "provider,service,key,disco_type,bucket,depth,parent,scope,reason,ops,pairing"},
		{"jsonl", "uncovered", `"bucket":"uncovered"`},
		// --source-root= turns pairing off; a row stream must say so on
		// every row (#85).
		{"csv", "uncovered", ",false\n"},
		{"jsonl", "uncovered", `"pairing":false`},
	} {
		resetCoverageFlags(t)
		out, err := captureStdout(t, func() error {
			cmd := rootCmd
			cmd.SetArgs([]string{"coverage", "services", "--providers", "aws", "--sdk-cache", root, "--source-root=", "--filter", tc.filter, "-o", tc.format})
			return cmd.Execute()
		})
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.format, tc.filter, err)
		}
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s/%s lacks %q:\n%s", tc.format, tc.filter, tc.want, out)
		}
		if tc.filter == "uncovered" && strings.Contains(out, "disco-only") && tc.format != "table" {
			t.Errorf("%s/%s leaked disco-only rows", tc.format, tc.filter)
		}
	}
}

// TestCoverage_RejectsUnknownFilter: an unrecognised --filter value errors
// rather than silently returning every row.
func TestCoverage_RejectsUnknownFilter(t *testing.T) {
	resetCoverageFlags(t)
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--filter", "bogus"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "--filter must be one of") {
		t.Errorf("want --filter validation error, got %v", err)
	}
}

// TestCoverage_RegistryDriftNeedsCrossCheck: the drift bucket only exists
// when the live registry was fetched, so asking for it offline is an error,
// not an empty report.
func TestCoverage_RegistryDriftNeedsCrossCheck(t *testing.T) {
	resetCoverageFlags(t)
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--filter", "registry-drift", "--cross-check=false"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "needs --cross-check") {
		t.Errorf("want cross-check validation error, got %v", err)
	}
}

// TestCoverageServices_GatesNeedPairing: --check-strict, --baseline and
// --write-baseline all measure the pairing, so each is refused when the
// scanner source is unavailable. --check-strict used to exit 0 in that mode
// whatever was wrong (Summary.Unexplained is 0 by construction), and a
// baseline written in it recorded a name-matching-only covered set that a
// later pairing-on run accepted as clean.
func TestCoverageServices_GatesNeedPairing(t *testing.T) {
	root := fixtureCache(t)
	for _, gate := range [][]string{
		{"--check-strict"},
		{"--baseline", filepath.Join(t.TempDir(), "b.json")},
		{"--write-baseline", filepath.Join(t.TempDir(), "b.json")},
	} {
		resetCoverageFlags(t)
		_, err := captureStdout(t, func() error {
			cmd := rootCmd
			cmd.SetArgs(append([]string{"coverage", "services", "--providers", "aws", "--sdk-cache", root, "--source-root=", "-o", "json"}, gate...))
			return cmd.Execute()
		})
		if !errors.Is(err, errCoverageInventoryUnavailable) {
			t.Errorf("%v: want errCoverageInventoryUnavailable, got %v", gate, err)
		}
	}
}

// TestCoverageServices_Baseline: --write-baseline records the unfiltered
// matrix (a --filter that hides every covered row must not empty it),
// comparing against that file is clean, and a baseline claiming a key the
// fixture never covers fails with the baseline sentinel after rendering.
func TestCoverageServices_Baseline(t *testing.T) {
	root := fixtureCache(t)
	srcRoot := fixtureSourceRoot(t)
	path := filepath.Join(t.TempDir(), "baseline.json")
	run := func(args ...string) (string, error) {
		resetCoverageFlags(t)
		return captureStdout(t, func() error {
			cmd := rootCmd
			cmd.SetArgs(append([]string{"coverage", "services", "--providers", "aws", "--sdk-cache", root, "--source-root", srcRoot, "-o", "json"}, args...))
			return cmd.Execute()
		})
	}
	if _, err := run("--filter", "uncovered", "--write-baseline", path); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := coverage.ReadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	aws := b["aws"]
	if len(aws.CoveredKeys) == 0 || !aws.Pairing || aws.Pins["aws-sdk-go-v2"] != awsinventory.SDKRef {
		t.Fatalf("baseline = %+v", aws)
	}
	if aws.Covered != len(aws.CoveredKeys) || aws.Uncovered != len(aws.UncoveredKeys) {
		t.Errorf("baseline counts %d/%d disagree with keys %d/%d", aws.Covered, aws.Uncovered, len(aws.CoveredKeys), len(aws.UncoveredKeys))
	}
	if _, err := run("--baseline", path); err != nil {
		t.Fatalf("clean compare: %v", err)
	}
	// A covered key the fresh run no longer has at all: under identical pins
	// the universe cannot shrink, so this is a scanner or extractor losing it.
	aws.CoveredKeys = append(aws.CoveredKeys, "widgets/phantom")
	aws.Covered++
	b["aws"] = aws
	if err := coverage.WriteBaseline(path, b); err != nil {
		t.Fatal(err)
	}
	out, err := run("--baseline", path)
	if !errors.Is(err, errCoverageBaseline) {
		t.Fatalf("want errCoverageBaseline, got %v", err)
	}
	var matrices []coverage.Matrix
	if jerr := json.Unmarshal([]byte(out), &matrices); jerr != nil || len(matrices) != 1 {
		t.Errorf("matrix must still render before the failure: %v\n%s", jerr, out)
	}
	if _, err := run("--baseline", filepath.Join(t.TempDir(), "none.json")); err == nil {
		t.Error("missing baseline file must error")
	}
}

// TestCoverageServices_PartialWriteBaselineMerges: writing the baseline for
// one provider must leave the others in the file. A --providers-narrowed
// regen used to truncate it, after which CompareBaseline reported only
// no-baseline for the missing providers and nothing guarded them.
func TestCoverageServices_PartialWriteBaselineMerges(t *testing.T) {
	root := fixtureCache(t)
	srcRoot := fixtureSourceRoot(t)
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := coverage.WriteBaseline(path, coverage.Baseline{
		"azure": {Pins: map[string]string{"azure-sdk-for-go": "sha"}, Pairing: true, Percent: 19.7, Covered: 386},
	}); err != nil {
		t.Fatal(err)
	}
	resetCoverageFlags(t)
	if _, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--providers", "aws", "--sdk-cache", root, "--source-root", srcRoot, "-o", "json", "--write-baseline", path})
		return cmd.Execute()
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := coverage.ReadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 2 || b["azure"].Covered != 386 || len(b["aws"].CoveredKeys) == 0 {
		t.Errorf("merged baseline = %+v", b)
	}
}

// TestCoverageResolvers_UnknownFormat verifies `coverage resolvers` rejects an
// invalid -o instead of silently falling through to the table (parity with
// the services/regions siblings). Registry-only, no network.
func TestCoverageResolvers_UnknownFormat(t *testing.T) {
	resetCoverageFlags(t)
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "resolvers", "-o", "xml"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "unknown --output format") {
		t.Errorf("want unknown-format error, got %v", err)
	}
}

// TestSelectedAuditors pins which providers expose resolver auditing to
// `disco coverage resolvers`. AWS, Azure, and GCP all implement
// coverage.ResolverAuditor. The registry is populated by the
// internal/providers/all blank import; the call does no network I/O (registry
// lookup + interface assertion).
func TestSelectedAuditors(t *testing.T) {
	// Empty selection = every auditing provider; must include aws + azure.
	all, err := selectedAuditors(nil)
	if err != nil {
		t.Fatalf("selectedAuditors(nil): %v", err)
	}
	got := map[string]bool{}
	for _, a := range all {
		got[a.prov.Name()] = true
	}
	if !got["aws"] || !got["azure"] || !got["gcp"] {
		t.Errorf("expected aws, azure, and gcp auditors, got %v", got)
	}

	// Explicit azure selects exactly one.
	az, err := selectedAuditors([]string{"azure"})
	if err != nil {
		t.Fatalf("selectedAuditors([azure]): %v", err)
	}
	if len(az) != 1 || az[0].prov.Name() != "azure" {
		t.Fatalf("expected [azure], got %d providers", len(az))
	}

	// Explicit gcp selects exactly one.
	gp, err := selectedAuditors([]string{"gcp"})
	if err != nil {
		t.Fatalf("selectedAuditors([gcp]): %v", err)
	}
	if len(gp) != 1 || gp[0].prov.Name() != "gcp" {
		t.Fatalf("expected [gcp], got %d providers", len(gp))
	}

	// Unknown provider → registry error.
	if _, err := selectedAuditors([]string{"nope"}); err == nil ||
		!strings.Contains(err.Error(), "no coverage support") {
		t.Errorf("expected unknown-provider error, got %v", err)
	}
}

// TestCoverageResolversMissing_WithRefsNeedsCache: --with-refs asks for the
// refs column, which needs the SDK inventory; without it the run exits 2 like
// `services` does instead of silently printing a refless list.
func TestCoverageResolversMissing_WithRefsNeedsCache(t *testing.T) {
	resetCoverageFlags(t)
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "resolvers", "--missing", "--with-refs", "--providers", "aws", "--sdk-cache", t.TempDir(), "--source-root="})
		return cmd.Execute()
	})
	if !errors.Is(err, errCoverageInventoryUnavailable) {
		t.Fatalf("want errCoverageInventoryUnavailable, got %v", err)
	}
}

// TestCoverageResolversMissing_Offline: the orphan list itself never needs
// the cache; refs are a hint, omitted (with a warning) when the inventory is
// unavailable, and the fixture universe pairs with no real AWS type.
func TestCoverageResolversMissing_Offline(t *testing.T) {
	for _, cache := range []string{t.TempDir(), fixtureCache(t)} {
		resetCoverageFlags(t)
		out, err := captureStdout(t, func() error {
			cmd := rootCmd
			cmd.SetArgs([]string{"coverage", "resolvers", "--missing", "--providers", "aws", "--services", "ec2", "--sdk-cache", cache, "--source-root=", "-o", "json"})
			return cmd.Execute()
		})
		if err != nil {
			t.Fatalf("resolvers --missing: %v", err)
		}
		var rows []orphanRow
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("json: %v\n%s", err, out)
		}
		if len(rows) == 0 {
			t.Fatal("no orphan rows for ec2")
		}
		for _, r := range rows {
			if r.Provider != "aws" || r.Service != "ec2" || len(r.Refs) != 0 {
				t.Errorf("row = %+v", r)
			}
		}
	}
}

// TestCoverageResolversMissing_WithRefsKeepsReflessTypes: refs recall is
// lossy, so their absence is a hint, not the definition of a derived leaf.
// Using it as a hide gate deleted 477 real gaps from the worklist; the fixture
// universe pairs with no real AWS type, so every row here is refless.
func TestCoverageResolversMissing_WithRefsKeepsReflessTypes(t *testing.T) {
	cache := fixtureCache(t)
	rows := func(args ...string) []orphanRow {
		t.Helper()
		resetCoverageFlags(t)
		out, err := captureStdout(t, func() error {
			cmd := rootCmd
			cmd.SetArgs(append([]string{"coverage", "resolvers", "--missing", "--providers", "aws", "--services", "ec2", "--sdk-cache", cache, "--source-root=", "-o", "json"}, args...))
			return cmd.Execute()
		})
		if err != nil {
			t.Fatalf("resolvers --missing %v: %v", args, err)
		}
		var got []orphanRow
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("json: %v\n%s", err, out)
		}
		return got
	}
	plain, withRefs := rows(), rows("--with-refs")
	if len(plain) == 0 || len(withRefs) != len(plain) {
		t.Fatalf("--with-refs returned %d rows, plain returned %d", len(withRefs), len(plain))
	}
}
