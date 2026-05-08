// Package scanrun wraps the orchestration core of `disco scan` so it can be
// driven by both the CLI (cmd/scan.go) and the paid HTTP server
// (cmd/serve_paid.go via internal/serve). It owns:
//
//   - resolving Scanner instances from the provider registry by name;
//   - applying per-request scope (regions, services, profile, skip-globals);
//   - creating the scan row and fanning out the Scan() calls in parallel;
//   - finalising the scan row (Complete or Partial) based on captured errors.
//
// CLI-only concerns (--if-older-than, --resume, --dry-run, progress
// rendering, --quiet) stay in cmd/scan.go. The Store's OnError /
// OnServiceComplete / OnResolveStart / OnResolveComplete callbacks are still
// honoured if the caller wires them — RunScanners attaches its own OnError
// only as a last-resort fallback so a Scanner that returns an error from
// Scan() is captured rather than dropped.
package scanrun

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/internal/store"
)

// Request describes a scan to launch. The shape mirrors the JSON body the
// `disco serve` POST /v1/scans handler accepts; the CLI builds an equivalent
// from cobra flags. Empty slices mean "use the provider's default scope" —
// e.g. all regions for AWS.
type Request struct {
	// Provider names ("aws", "azure", "gcp"). Empty = all registered.
	Providers []string
	// AWS-specific override.
	Regions []string
	// AWS-specific override.
	Accounts []string
	// Azure-specific override.
	Subscriptions []string
	// GCP-specific override.
	Projects []string
	// Limit scanners to this set of services (e.g. "aws:ec2", "aws:s3").
	// Applied per provider via providers.ServiceFilterer.
	ResourceTypes []string
	// AWS named credentials profile.
	Profile string
	// Skip globally-scoped scanners (IAM, Route53, etc.) where supported.
	SkipGlobals bool
}

// Run resolves scanners, applies per-request scope, opens a scan row, fans
// out, and finalises. Returns (scanID, err). A nil error means at least one
// scanner ran cleanly; per-scanner failures are aggregated on the scan row
// as a partial completion. Returns an error only for hard failures
// (registry mismatch, store unable to record the scan).
func Run(ctx context.Context, st *store.Store, req Request) (string, error) {
	scanners, err := resolveScanners(req)
	if err != nil {
		return "", err
	}
	if len(scanners) == 0 {
		return "", fmt.Errorf("no providers selected")
	}
	applyOverrides(scanners, req)

	names := make([]string, len(scanners))
	for i, s := range scanners {
		names[i] = s.Name()
	}
	scope := map[string]any{"providers": names}
	if len(req.Regions) > 0 {
		scope["regions"] = req.Regions
	}
	if len(req.ResourceTypes) > 0 {
		scope["resource_types"] = req.ResourceTypes
	}
	scanID, err := st.CreateScan(names, scope)
	if err != nil {
		return "", fmt.Errorf("create scan: %w", err)
	}

	warnings, scanErrors := RunScanners(ctx, st, scanID, scanners)

	count, _ := st.CountResourcesByScan(scanID)
	if len(scanErrors) > 0 {
		msgs := make([]string, len(scanErrors))
		for i, e := range scanErrors {
			msgs[i] = fmt.Sprintf("%s/%s: %s", e.Provider, e.Service, e.Message)
		}
		_ = warnings // future: persist warning count once schema lands
		if perr := st.PartialScan(scanID, count, strings.Join(msgs, "; ")); perr != nil {
			return scanID, fmt.Errorf("mark partial: %w", perr)
		}
		return scanID, nil
	}
	if cerr := st.CompleteScan(scanID, count); cerr != nil {
		return scanID, fmt.Errorf("complete scan: %w", cerr)
	}
	return scanID, nil
}

// RunScanners is the parallel fan-out core: invokes Scan() on each scanner
// concurrently, capturing warnings + errors via the Store's existing
// callbacks. The caller owns the scan row lifecycle (CreateScan +
// Complete/PartialScan) — RunScanners only writes resources via the
// scanners themselves.
//
// Captured warnings/errors are returned for the caller to render
// (cmd/scan.go renders to stderr; the serve handler logs them and lets the
// scan row's `error` column carry the summary). Existing OnWarn / OnError
// callbacks set by the caller still fire — RunScanners installs its own
// only when none is registered, so wiring stays additive for the CLI.
func RunScanners(
	ctx context.Context,
	st *store.Store,
	scanID string,
	scanners []providers.Scanner,
) (warnings []store.ScanWarning, scanErrors []store.ScanError) {
	var (
		warnMu sync.Mutex
		errMu  sync.Mutex
	)
	prevWarn := st.OnWarn
	st.OnWarn = func(w store.ScanWarning) {
		warnMu.Lock()
		warnings = append(warnings, w)
		warnMu.Unlock()
		if prevWarn != nil {
			prevWarn(w)
		}
	}
	prevErr := st.OnError
	st.OnError = func(e store.ScanError) {
		errMu.Lock()
		scanErrors = append(scanErrors, e)
		errMu.Unlock()
		if prevErr != nil {
			prevErr(e)
		}
	}

	var wg sync.WaitGroup
	for _, s := range scanners {
		wg.Go(func() {
			if err := s.Scan(ctx, st, scanID); err != nil {
				errMu.Lock()
				scanErrors = append(scanErrors, store.ScanError{
					Provider: s.Name(), Service: "scan", Scope: "", Message: err.Error(),
				})
				errMu.Unlock()
			}
		})
	}
	wg.Wait()
	return warnings, scanErrors
}

func resolveScanners(req Request) ([]providers.Scanner, error) {
	if len(req.Providers) == 0 {
		return providers.All(), nil
	}
	out := make([]providers.Scanner, 0, len(req.Providers))
	for _, name := range req.Providers {
		s, ok := providers.Get(name)
		if !ok {
			return nil, fmt.Errorf("unknown provider %q (available: %s)",
				name, strings.Join(providers.Names(), ", "))
		}
		out = append(out, s)
	}
	return out, nil
}

// applyOverrides applies the per-request scope to scanners that implement
// the matching capability interfaces. Provider-agnostic — fields not
// applicable to a given scanner (e.g. Subscriptions on AWS) are silently
// ignored. Mutates the registered Scanner instances; safe for one-shot
// containers but callers running multiple scans in-process must serialise.
func applyOverrides(scanners []providers.Scanner, req Request) {
	for _, s := range scanners {
		if ro, ok := s.(providers.RegionOverrider); ok && len(req.Regions) > 0 {
			ro.SetRegionOverride(req.Regions)
		}
		if po, ok := s.(providers.ProfileOverrider); ok && req.Profile != "" {
			po.SetProfile(req.Profile)
		}
		if sf, ok := s.(providers.ServiceFilterer); ok && len(req.ResourceTypes) > 0 {
			sf.SetServiceFilter(req.ResourceTypes)
		}
		if gs, ok := s.(providers.GlobalsSkipper); ok && req.SkipGlobals {
			gs.SetSkipGlobals(true)
		}
	}
}
