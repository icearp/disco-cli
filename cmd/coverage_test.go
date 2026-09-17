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
	dir := sdkinv.Cache{Root: root}.Dir("aws", sdkinv.AWSSDKRef)
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "internal", "sdkinv", "aws", "testdata", "cache"))); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(sdkinv.Manifest{Provider: "aws", Ref: sdkinv.AWSSDKRef})
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifest, 0o644); err != nil {
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
// name-matched or uncovered, every emitted type is disco-only with the
// pairing-unavailable reason, and --check-strict passes because nothing can
// be called unexplained without pairing.
func TestCoverageServices_Offline(t *testing.T) {
	root := fixtureCache(t)
	resetCoverageFlags(t)
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--providers", "aws", "--sdk-cache", root, "--source-root=", "--check-strict", "-o", "json"})
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
	if m.Pins["aws-sdk-go-v2"] != sdkinv.AWSSDKRef {
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
		{"csv", "all", "provider,service,key,disco_type,bucket,depth,parent,scope,reason,ops"},
		{"jsonl", "uncovered", `"bucket":"uncovered"`},
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

// TestCoverageResolversMissing_WithRefsNeedsCache: --with-refs hides derived
// leaves, which needs the SDK inventory; without it the run exits 2 like
// `services` does instead of silently printing an empty list.
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
