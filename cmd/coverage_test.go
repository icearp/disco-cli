package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/coverage"
)

// fakeCoverageProvider scopes coverage tests to a synthetic provider so the
// real AWS/Azure/GCP providers (which Fetch live registries over the
// network) stay untouched. Name carries the test name to avoid duplicate-
// registration panics across t.Run / parallel tests.
type fakeCoverageProvider struct {
	name     string
	emits    []coverage.TypeDecl
	upstream []coverage.UpstreamType
	fetchErr error
}

func (f *fakeCoverageProvider) Name() string { return f.name }
func (f *fakeCoverageProvider) Fetch(_ context.Context, _ coverage.FetchOptions) ([]coverage.UpstreamType, error) {
	return f.upstream, f.fetchErr
}
func (f *fakeCoverageProvider) Emits() []coverage.TypeDecl     { return f.emits }
func (f *fakeCoverageProvider) Aliases() map[string]string     { return nil }
func (f *fakeCoverageProvider) AlgorithmicKey(_ string) string { return "" }

// TestCoverage_StrictCannotAssessOnFetchFailure: registry-unreachable under
// --check-strict surfaces a distinct "cannot assess" error rather than the
// fleet-wide false-drift report (F9).
func TestCoverage_StrictCannotAssessOnFetchFailure(t *testing.T) {
	name := "f9-cannot-assess"
	coverage.Register(&fakeCoverageProvider{
		name:     name,
		emits:    []coverage.TypeDecl{{Service: "ec2", DiscoType: name + ":ec2:instance"}},
		fetchErr: errors.New("throttled"),
	})

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "--provider", name, "--check-strict", "--timeout", "1s"})
		return cmd.Execute()
	})
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !strings.Contains(err.Error(), "cannot assess --check-strict") {
		t.Errorf("want cannot-assess message, got: %v", err)
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

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "--provider", name, "--check-strict", "--timeout", "1s"})
		return cmd.Execute()
	})
	if err == nil {
		t.Fatalf("want strict-drift error, got nil")
	}
	if !strings.Contains(err.Error(), "upstream-missing rows present") {
		t.Errorf("want existing drift message, got: %v", err)
	}
}

// TestCoverage_NonStrictTolerantOnFetchFailure: without --check-strict, fetch
// failures warn-and-continue without returning an error.
func TestCoverage_NonStrictTolerantOnFetchFailure(t *testing.T) {
	name := "f9-tolerant"
	coverage.Register(&fakeCoverageProvider{
		name:     name,
		emits:    []coverage.TypeDecl{{Service: "ec2", DiscoType: name + ":ec2:instance"}},
		fetchErr: errors.New("throttled"),
	})

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "--provider", name, "--timeout", "1s", "--check-strict=false"})
		return cmd.Execute()
	})
	if err != nil {
		t.Errorf("non-strict should tolerate fetch failure, got: %v", err)
	}
}
