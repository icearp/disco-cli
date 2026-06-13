// Package scanrun wraps the orchestration core of `disco scan` so the engine
// is reusable independent of the CLI front end (cmd/scan.go). It owns:
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
	"codeberg.org/icearp/disco/store"
)

// Request describes a scan to launch. The CLI builds one from cobra flags;
// the shape is deliberately serialisable so other drivers can construct it
// too. Empty slices mean "use the provider's default scope" — e.g. all
// regions for AWS.
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

// Allocate creates a "pending" scan row and returns its ID without running
// any scanners. Used by the API handler to surface a scan_id at 202 time
// before the fan-out begins. The handler then calls Execute(scanID, req)
// in a background goroutine.
//
// The returned scanners + scope are cached in the *Allocation so Execute
// does not re-resolve providers (which would double-apply overrides).
func Allocate(st *store.Store, req Request) (*Allocation, error) {
	scanners, err := resolveScanners(req)
	if err != nil {
		return nil, err
	}
	if len(scanners) == 0 {
		return nil, fmt.Errorf("no providers selected")
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
		return nil, fmt.Errorf("create scan: %w", err)
	}
	return &Allocation{ScanID: scanID, scanners: scanners}, nil
}

// Allocation is the handle returned by Allocate; pass it to Execute to
// actually run the scanners + finalise the scan row.
type Allocation struct {
	ScanID   string
	scanners []providers.Scanner
}

// Execute runs the scanners attached to a, finalises the scan row, and
// returns a non-nil error only for store-level failures. Per-scanner
// errors are persisted as PartialScan; the call returns nil so the
// caller can exit cleanly.
func Execute(ctx context.Context, st *store.Store, a *Allocation) error {
	_, scanErrors := RunScanners(ctx, st, a.ScanID, a.scanners)

	count, _ := st.CountResourcesByScan(a.ScanID)
	if len(scanErrors) > 0 {
		msgs := make([]string, len(scanErrors))
		for i, e := range scanErrors {
			msgs[i] = fmt.Sprintf("%s/%s: %s", e.Provider, e.Service, e.Message)
		}
		if perr := st.PartialScan(a.ScanID, count, strings.Join(msgs, "; ")); perr != nil {
			return fmt.Errorf("mark partial: %w", perr)
		}
		return nil
	}
	if cerr := st.CompleteScan(a.ScanID, count); cerr != nil {
		return fmt.Errorf("complete scan: %w", cerr)
	}
	return nil
}

// Run is the synchronous shorthand for Allocate + Execute. CLI uses this;
// API handler splits to surface scanID at 202 time.
func Run(ctx context.Context, st *store.Store, req Request) (string, error) {
	a, err := Allocate(st, req)
	if err != nil {
		return "", err
	}
	if err := Execute(ctx, st, a); err != nil {
		return a.ScanID, err
	}
	return a.ScanID, nil
}

// RunScanners is the parallel fan-out core: invokes Scan() on each scanner
// concurrently, capturing warnings + errors via the Store's existing
// callbacks. The caller owns the scan row lifecycle (CreateScan +
// Complete/PartialScan) — RunScanners only writes resources via the
// scanners themselves.
//
// Captured warnings/errors are returned for the caller to render
// (cmd/scan.go renders to stderr; the scan row's `error` column also carries
// the summary for later inspection). Existing OnWarn / OnError
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
