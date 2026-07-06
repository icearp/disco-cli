// Package scanrun wraps the orchestration core of `disco scan` so the engine
// is reusable outside the CLI front end (cmd/scan.go). It owns:
//
//   - resolving Scanner instances from the provider registry by name;
//   - applying per-request scope (regions, services, profile, skip-globals);
//   - creating the scan row and fanning out the Scan() calls in parallel;
//   - finalising the scan row (Complete or Partial) based on captured errors.
//
// CLI-only concerns (--if-older-than, --resume, --dry-run, progress
// rendering, --quiet) stay in cmd/scan.go. The Store's OnError /
// OnServiceComplete / OnResolveStart / OnResolveComplete callbacks still
// fire if the caller wires them — RunScanners's own OnError is only a
// last-resort fallback so an error returned from Scan() is captured, not
// dropped.
package scanrun

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

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

// Allocation is the handle returned by Allocate; pass it to Execute to run
// the scanners and finalise the scan row.
type Allocation struct {
	ScanID   string
	scanners []providers.Scanner
}

// Execute runs the scanners attached to a, finalises the scan row, and
// returns a non-nil error only for store-level failures. Per-scanner
// errors are persisted as PartialScan; the call returns nil so the
// caller can exit cleanly.
func Execute(ctx context.Context, st *store.Store, a *Allocation) error {
	_, scanErrors, totalSeen, _, _ := RunScanners(ctx, st, a.ScanID, a.scanners)
	// totalSeen (rows visited this scan, the canonical scans.resource_count) is
	// accumulated by RunScanners so this path and cmd/scan.go record the same
	// count. Finalize owns the Complete/Partial dispatch and structured-error
	// persistence shared with the CLI. ctx.Err() != nil means the scan was
	// interrupted (signal/deadline) before finishing — finalize it partial even
	// if no per-service error was reported.
	if _, err := Finalize(st, a.ScanID, totalSeen, scanErrors, ctx.Err() != nil); err != nil {
		return err
	}
	return nil
}

// FinalizeResult reports the outcome of finalising a scan row. AppendErrors
// holds non-fatal failures persisting structured per-failure entries — the CLI
// renders them to stderr; library callers may ignore them. Interrupted is set
// when the scan was finalised partial because it was signal/deadline-cancelled
// rather than because a service failed — the CLI uses it to print a distinct
// "interrupted" summary.
type FinalizeResult struct {
	Partial      bool
	Interrupted  bool
	AppendErrors []error
}

// interruptedReason is the synthetic failure recorded when a scan is finalised
// partial solely because it was cancelled (SIGINT/SIGTERM/deadline) before
// finishing. Surfaced in the partial `error` blob and as a structured entry so
// the truncation is visible even when no per-service error was reported (e.g.
// cancellation landed silently at a concurrency-semaphore gate).
const interruptedReason = "scan interrupted before completion (signal)"

// Finalize marks the scan row complete (no errors and not interrupted) or
// partial (one or more scan errors, or the scan was interrupted), and on the
// partial path persists one structured ScanErrorEntry per failure alongside the
// concatenated legacy `error` blob. Single source of truth shared by Execute and
// cmd/scan.go so a scan is finalised identically regardless of entry point.
// count is the canonical rows-visited total. interrupted reflects ctx.Err() at
// the call site — a cancelled scan is always partial, regardless of whether any
// provider reported a context-canceled error (the silent semaphore-gate path
// reports none), so the status can't depend on timing.
func Finalize(st *store.Store, scanID string, count int, scanErrors []store.ScanError, interrupted bool) (FinalizeResult, error) {
	if len(scanErrors) == 0 && !interrupted {
		if err := st.CompleteScan(scanID, count); err != nil {
			return FinalizeResult{}, fmt.Errorf("complete scan: %w", err)
		}
		return FinalizeResult{}, nil
	}

	// Any errors or an interruption → partial scan. We no longer distinguish
	// "all failed" from "some failed" because nothing aborts: even with errors,
	// resources from the surviving services are persisted and worth keeping.
	msgs := make([]string, 0, len(scanErrors)+1)
	if interrupted {
		msgs = append(msgs, interruptedReason)
	}
	for _, e := range scanErrors {
		msgs = append(msgs, fmt.Sprintf("%s/%s: %s", e.Provider, e.Service, e.Message))
	}
	if err := st.PartialScan(scanID, count, strings.Join(msgs, "; ")); err != nil {
		return FinalizeResult{Partial: true, Interrupted: interrupted}, fmt.Errorf("mark partial scan: %w", err)
	}

	res := FinalizeResult{Partial: true, Interrupted: interrupted}
	// Record the interruption as a structured entry too, so scans.errors carries
	// it queryably even when scanErrors is empty.
	if interrupted {
		if aerr := st.AppendScanError(scanID, store.ScanErrorEntry{
			Service: "scan:interrupted",
			Code:    "Canceled",
			Message: interruptedReason,
		}); aerr != nil {
			res.AppendErrors = append(res.AppendErrors, aerr)
		}
	}
	// Structured per-failure entries land in scans.errors (jsonb) so downstream
	// consumers can group + filter without parsing prose. region is parsed
	// best-effort from Scope (shaped "<account>/<region>" for AWS; bare for
	// Azure/GCP).
	for _, e := range scanErrors {
		region := ""
		if i := strings.LastIndex(e.Scope, "/"); i >= 0 {
			region = e.Scope[i+1:]
		}
		if aerr := st.AppendScanError(scanID, store.ScanErrorEntry{
			Service: e.Provider + ":" + e.Service,
			Region:  region,
			Code:    scanErrorCode(e.Message),
			Message: e.Message,
		}); aerr != nil {
			res.AppendErrors = append(res.AppendErrors, aerr)
		}
	}
	return res, nil
}

// scanErrorCode best-effort extracts an AWS-style error code from the failure
// message — e.g. "AccessDenied", "Throttling", "UnknownOperation". Returns
// "Error" when no recognisable token is present so the structured entry's
// `code` field is never empty.
func scanErrorCode(msg string) string {
	for _, candidate := range []string{
		"AccessDenied", "Throttling", "ThrottlingException",
		"UnknownOperationException", "UnsupportedOperation",
		"InvalidParameterValue", "AuthFailure", "RequestTimeout",
		"InternalError", "ServiceUnavailable",
	} {
		if strings.Contains(msg, candidate) {
			return candidate
		}
	}
	return "Error"
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
// concurrently, capturing warnings + errors and accumulating the rows-visited
// (totalSeen) / newly-inserted (totalNew) totals via the Store's callbacks.
// The caller owns the scan row lifecycle (CreateScan + Finalize) — RunScanners
// only writes resources via the scanners themselves.
//
// Captured warnings/errors are returned for the caller to render (cmd/scan.go
// renders to stderr; the scan row's `error` column also carries the summary for
// later inspection); the totals feed Finalize so every entry point records the
// same scans.resource_count. Existing OnWarn / OnError / OnServiceComplete
// callbacks set by the caller still fire — RunScanners chains onto them so
// wiring stays additive for the CLI, and restores them before returning.
func RunScanners(
	ctx context.Context,
	st *store.Store,
	scanID string,
	scanners []providers.Scanner,
) (warnings []store.ScanWarning, scanErrors []store.ScanError, totalSeen, totalNew, totalChanged int) {
	var (
		warnMu  sync.Mutex
		errMu   sync.Mutex
		seen    atomic.Int64
		fresh   atomic.Int64
		changed atomic.Int64
	)
	// Capture and restore the caller's callbacks: this chains onto any existing
	// OnWarn/OnError/OnServiceComplete so wiring (CLI progress lines) stays
	// additive, but the shared *Store outlives a single scan (the
	// Allocate/Execute multi-scan API driver reuses one store), so leaving our
	// closures installed would make scan #2 append to scan #1's dangling slices
	// and grow the chain unbounded.
	prevWarn := st.OnWarn
	prevErr := st.OnError
	prevSvc := st.OnServiceComplete
	defer func() {
		st.OnWarn = prevWarn
		st.OnError = prevErr
		st.OnServiceComplete = prevSvc
	}()
	st.OnWarn = func(w store.ScanWarning) {
		warnMu.Lock()
		warnings = append(warnings, w)
		warnMu.Unlock()
		if prevWarn != nil {
			prevWarn(w)
		}
	}
	st.OnError = func(e store.ScanError) {
		errMu.Lock()
		scanErrors = append(scanErrors, e)
		errMu.Unlock()
		if prevErr != nil {
			prevErr(e)
		}
	}
	// Accumulate the canonical totals here so both entry points (CLI and the
	// Allocate/Execute API driver) derive scans.resource_count identically:
	// totalSeen = rows visited (incl. pre-existing), totalNew = first-discoveries,
	// totalChanged = version splits (existing rows whose attrs/tags changed).
	st.OnServiceComplete = func(service, scope string, total, newCount, changedCount, errCount int, status store.ServiceStatus) {
		seen.Add(int64(total))
		fresh.Add(int64(newCount))
		changed.Add(int64(changedCount))
		if prevSvc != nil {
			prevSvc(service, scope, total, newCount, changedCount, errCount, status)
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
	return warnings, scanErrors, int(seen.Load()), int(fresh.Load()), int(changed.Load())
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
