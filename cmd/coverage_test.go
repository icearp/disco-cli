package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/coverage"
)

// fakeCoverageProvider scopes coverage tests to a synthetic provider so real
// AWS/Azure/GCP providers (which Fetch live registries over the network)
// stay untouched. Name carries the test name to avoid duplicate-registration
// panics across t.Run / parallel tests.
type fakeCoverageProvider struct {
	name     string
	emits    []coverage.TypeDecl
	upstream []coverage.UpstreamType
	fetchErr error
}

// resetCoverageFlags clears StringSlice flags on every coverage subcommand
// before each test: pflag's StringSlice values accumulate across consecutive
// Execute() calls in the same process, so providers/regions/services would
// otherwise carry stale entries from a prior run.
func resetCoverageFlags(t *testing.T) {
	t.Helper()
	for _, sub := range coverageCmd.Commands() {
		for _, f := range []string{"providers", "regions", "services"} {
			if fl := sub.Flags().Lookup(f); fl != nil {
				_ = fl.Value.Set("")
				fl.Changed = false
				if sv, ok := fl.Value.(interface{ Replace([]string) error }); ok {
					_ = sv.Replace(nil)
				}
			}
		}
	}
}

func (f *fakeCoverageProvider) Name() string { return f.name }
func (f *fakeCoverageProvider) Fetch(_ context.Context, _ coverage.FetchOptions) ([]coverage.UpstreamType, error) {
	return f.upstream, f.fetchErr
}
func (f *fakeCoverageProvider) Emits() []coverage.TypeDecl     { return f.emits }
func (f *fakeCoverageProvider) Aliases() map[string]string     { return nil }
func (f *fakeCoverageProvider) AlgorithmicKey(_ string) string { return "" }

// TestCoverage_StrictCannotAssessOnFetchFailure: a registry-unreachable fetch
// under --check-strict surfaces the distinct "registry unreachable" error
// rather than the fleet-wide false-drift report (F9).
func TestCoverage_StrictCannotAssessOnFetchFailure(t *testing.T) {
	name := "f9-cannot-assess"
	coverage.Register(&fakeCoverageProvider{
		name:     name,
		emits:    []coverage.TypeDecl{{Service: "ec2", DiscoType: name + ":ec2:instance"}},
		fetchErr: errors.New("throttled"),
	})

	resetCoverageFlags(t)
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--providers", name, "--check-strict", "--timeout", "1s"})
		return cmd.Execute()
	})
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !errors.Is(err, errCoverageRegistryUnreachable) {
		t.Errorf("want errCoverageRegistryUnreachable, got: %v", err)
	}
	if strings.Contains(err.Error(), "upstream-missing rows present") {
		t.Errorf("strict-gate fell through to drift message: %v", err)
	}
}

// TestCoverage_StrictDriftStillFires: clean fetch + drift row still hits the
// existing strict gate.
func TestCoverage_StrictDriftStillFires(t *testing.T) {
	name := "f9-drift"
	coverage.Register(&fakeCoverageProvider{
		name:     name,
		emits:    []coverage.TypeDecl{{Service: "ec2", DiscoType: name + ":ec2:instance"}},
		upstream: nil, // empty but no fetchErr → real "drift": disco emits without upstream
	})

	resetCoverageFlags(t)
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--providers", name, "--check-strict", "--timeout", "1s"})
		return cmd.Execute()
	})
	if err == nil {
		t.Fatalf("want strict-drift error, got nil")
	}
	if !strings.Contains(err.Error(), "upstream-missing rows present") {
		t.Errorf("want existing drift message, got: %v", err)
	}
}

// TestCoverage_NonStrictErrorsOnFetchFailure: even without --check-strict, a
// fetch failure (e.g. expired/missing credentials) is fatal — an empty
// upstream would falsely bucket every emitted type as upstream-missing.
func TestCoverage_NonStrictErrorsOnFetchFailure(t *testing.T) {
	name := "f9-fatal"
	coverage.Register(&fakeCoverageProvider{
		name:     name,
		emits:    []coverage.TypeDecl{{Service: "ec2", DiscoType: name + ":ec2:instance"}},
		fetchErr: errors.New("ExpiredToken"),
	})

	resetCoverageFlags(t)
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--providers", name, "--timeout", "1s", "--check-strict=false"})
		return cmd.Execute()
	})
	if err == nil {
		t.Fatalf("non-strict fetch failure must error, got nil")
	}
	if !errors.Is(err, errCoverageRegistryUnreachable) {
		t.Errorf("want errCoverageRegistryUnreachable, got: %v", err)
	}
	// No matrix should have been rendered to stdout before the error.
	if strings.Contains(out, "upstream-missing") || strings.Contains(out, name+":ec2:instance") {
		t.Errorf("a misleading matrix was rendered before the fatal error:\n%s", out)
	}
}

// TestCoverage_FetchFailureJSONEnvelope: with -o json, a fetch failure emits the
// structured {"error":...} envelope on stdout (via maybeStructuredError) so
// machine consumers see the failure, not a false zero-coverage document.
func TestCoverage_FetchFailureJSONEnvelope(t *testing.T) {
	name := "f9-json"
	coverage.Register(&fakeCoverageProvider{
		name:     name,
		emits:    []coverage.TypeDecl{{Service: "ec2", DiscoType: name + ":ec2:instance"}},
		fetchErr: errors.New("ExpiredToken"),
	})

	resetCoverageFlags(t)
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--providers", name, "--timeout", "1s", "-o", "json"})
		return cmd.Execute()
	})
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var env struct {
		Error string `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
		t.Fatalf("stdout is not a JSON error envelope: %v\n%s", jerr, out)
	}
	if !strings.Contains(env.Error, "upstream registry unreachable") {
		t.Errorf("envelope error = %q; want it to mention the unreachable registry", env.Error)
	}
}

// TestCoverage_UncataloguedFilterPassesStrict: an Uncatalogued emit lands in the
// uncatalogued bucket (not upstream-missing), so --check-strict stays clean and
// --filter uncatalogued surfaces the row. Guards the WS2 bucket end to end.
func TestCoverage_UncataloguedFilterPassesStrict(t *testing.T) {
	name := "ws2-uncat"
	coverage.Register(&fakeCoverageProvider{
		name:  name,
		emits: []coverage.TypeDecl{{Service: "kms", DiscoType: name + ":kms:grant", Uncatalogued: true}},
		// No upstream entry — a synthetic-era flag would false-flag this as
		// upstream-missing and trip --check-strict.
	})

	resetCoverageFlags(t)
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--providers", name, "--filter", "uncatalogued", "--check-strict", "--timeout", "1s"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("uncatalogued row must not trip --check-strict: %v", err)
	}
	if !strings.Contains(out, name+":kms:grant") {
		t.Errorf("--filter uncatalogued did not surface the uncatalogued row:\n%s", out)
	}
}

// TestCoverage_RejectsUnknownFilter: an unrecognised --filter value errors
// rather than silently returning every row.
func TestCoverage_RejectsUnknownFilter(t *testing.T) {
	resetCoverageFlags(t)
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "services", "--filter", "bogus", "--timeout", "1s"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "--filter must be one of") {
		t.Errorf("want --filter validation error, got %v", err)
	}
}

// TestSelectedAuditors pins which providers expose resolver auditing to
// `disco coverage resolvers`. AWS and Azure implement coverage.ResolverAuditor;
// GCP does not. The registry is populated by the internal/providers/all blank
// import; the call does no network I/O (registry lookup + interface assertion).
// TestCoverageResolvers_UnknownFormat verifies `coverage resolvers` now
// rejects an invalid -o instead of silently falling through to the table
// (parity with the services/regions siblings). Registry-only, no network.
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
	if !got["aws"] || !got["azure"] {
		t.Errorf("expected aws and azure auditors, got %v", got)
	}

	// Explicit azure selects exactly one.
	az, err := selectedAuditors([]string{"azure"})
	if err != nil {
		t.Fatalf("selectedAuditors([azure]): %v", err)
	}
	if len(az) != 1 || az[0].prov.Name() != "azure" {
		t.Fatalf("expected [azure], got %d providers", len(az))
	}

	// GCP is registered for coverage but has no resolver auditing → clear error.
	if _, err := selectedAuditors([]string{"gcp"}); err == nil ||
		!strings.Contains(err.Error(), "does not support resolver coverage") {
		t.Errorf("expected gcp 'does not support resolver coverage', got %v", err)
	}

	// Unknown provider → registry error.
	if _, err := selectedAuditors([]string{"nope"}); err == nil ||
		!strings.Contains(err.Error(), "no coverage support") {
		t.Errorf("expected unknown-provider error, got %v", err)
	}
}
