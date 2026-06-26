package scanrun

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/store"
)

// newFinalizeStore opens a temp SQLite store and creates a scan row, returning
// the store and the new scan id. Finalize writes through real store methods
// (CompleteScan/PartialScan/AppendScanError), so it needs a backing DB.
func newFinalizeStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "finalize.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	return st, scanID
}

func scanStatus(t *testing.T, st *store.Store, id string) *store.Scan {
	t.Helper()
	sc, err := st.GetScan(id)
	if err != nil {
		t.Fatalf("GetScan: %v", err)
	}
	return sc
}

// fakeScanner reports one service (5 seen, 3 new, 2 changed) and one error per
// Scan via the store callbacks, exercising the OnServiceComplete and OnError
// chains RunScanners installs.
type fakeScanner struct{ name string }

func (f fakeScanner) Name() string { return f.name }

func (f fakeScanner) Scan(_ context.Context, st *store.Store, _ string) error {
	st.ReportService("svc", "global", 5, 3, 2, 0, store.ServiceOK)
	st.ReportError(store.ScanError{Provider: f.name, Service: "svc", Message: "boom"})
	return nil
}

// TestRunScanners_RestoresCallbacks pins the leak fix: the shared *Store
// outlives a single scan (the Allocate/Execute multi-scan driver reuses one
// store), so RunScanners must restore OnWarn/OnError before returning. Without
// the restore, a second run chains atop the first's dangling closure and counts
// grow unbounded.
func TestRunScanners_RestoresCallbacks(t *testing.T) {
	st := &store.Store{}
	scanners := []providers.Scanner{fakeScanner{name: "aws"}}

	_, errs1, _, _, _ := RunScanners(context.Background(), st, "scan-1", scanners)
	if len(errs1) != 1 {
		t.Fatalf("run 1: want 1 error, got %d", len(errs1))
	}
	if st.OnWarn != nil || st.OnError != nil {
		t.Fatalf("callbacks not restored after run 1: OnWarn=%v OnError=%v", st.OnWarn != nil, st.OnError != nil)
	}

	// Second run on the same store must capture exactly its own error, proving
	// it did not chain onto run 1's leaked closure.
	_, errs2, _, _, _ := RunScanners(context.Background(), st, "scan-2", scanners)
	if len(errs2) != 1 {
		t.Fatalf("run 2: want 1 error (no chaining), got %d", len(errs2))
	}
	if st.OnWarn != nil || st.OnError != nil || st.OnServiceComplete != nil {
		t.Fatalf("callbacks not restored after run 2")
	}
}

// TestRunScanners_AccumulatesTotals pins that RunScanners sums the per-service
// totals so both entry points derive the same scans.resource_count.
func TestRunScanners_AccumulatesTotals(t *testing.T) {
	st := &store.Store{}
	scanners := []providers.Scanner{fakeScanner{name: "aws"}, fakeScanner{name: "gcp"}}

	_, _, totalSeen, totalNew, totalChanged := RunScanners(context.Background(), st, "scan-1", scanners)
	if totalSeen != 10 { // 2 scanners × 5 seen
		t.Errorf("totalSeen = %d, want 10", totalSeen)
	}
	if totalNew != 6 { // 2 scanners × 3 new
		t.Errorf("totalNew = %d, want 6", totalNew)
	}
	if totalChanged != 4 { // 2 scanners × 2 changed
		t.Errorf("totalChanged = %d, want 4", totalChanged)
	}
}

// quietScanner reports nothing — used to prove an interrupted scan is marked
// partial solely via the ctx.Err() wiring, with zero scan errors in play.
type quietScanner struct{}

func (quietScanner) Name() string                                           { return "quiet" }
func (quietScanner) Scan(_ context.Context, _ *store.Store, _ string) error { return nil }

// TestExecute_CancelledCtxMarksPartial guards the Execute call-site wiring: a
// pre-cancelled ctx must finalize the scan partial even though no scanner
// reported an error. Without `ctx.Err() != nil` threaded into Finalize, the
// empty-error path would mark it completed.
func TestExecute_CancelledCtxMarksPartial(t *testing.T) {
	st, scanID := newFinalizeStore(t)
	a := &Allocation{ScanID: scanID, scanners: []providers.Scanner{quietScanner{}}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before Execute runs

	if err := Execute(ctx, st, a); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if sc := scanStatus(t, st, scanID); sc.Status != "partial" {
		t.Errorf("status = %q, want partial (cancelled ctx)", sc.Status)
	}
}

// TestFinalize_InterruptedMarksPartial is the regression guard: a scan cancelled
// before completion must be recorded partial even when NO per-service error was
// reported (cancellation lands silently at the concurrency-semaphore gate). Pre
// fix, empty scanErrors → CompleteScan, so this asserted 'completed' wrongly.
func TestFinalize_InterruptedMarksPartial(t *testing.T) {
	st, scanID := newFinalizeStore(t)

	res, err := Finalize(st, scanID, 42, nil, true)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !res.Partial || !res.Interrupted {
		t.Errorf("result = %+v, want Partial && Interrupted", res)
	}
	sc := scanStatus(t, st, scanID)
	if sc.Status != "partial" {
		t.Errorf("status = %q, want partial", sc.Status)
	}
	if sc.Error == nil || !strings.Contains(*sc.Error, "interrupted") {
		t.Errorf("error blob = %v, want it to mention the interruption", sc.Error)
	}
}

// TestFinalize_CleanMarksComplete: no errors and not interrupted → completed.
func TestFinalize_CleanMarksComplete(t *testing.T) {
	st, scanID := newFinalizeStore(t)

	res, err := Finalize(st, scanID, 100, nil, false)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if res.Partial || res.Interrupted {
		t.Errorf("result = %+v, want clean (not partial/interrupted)", res)
	}
	if sc := scanStatus(t, st, scanID); sc.Status != "completed" {
		t.Errorf("status = %q, want completed", sc.Status)
	}
}

// TestFinalize_ErrorsMarkPartial pins existing behavior: a reported scan error
// (not an interruption) → partial, not flagged Interrupted.
func TestFinalize_ErrorsMarkPartial(t *testing.T) {
	st, scanID := newFinalizeStore(t)

	errs := []store.ScanError{{Provider: "aws", Service: "ec2", Message: "boom"}}
	res, err := Finalize(st, scanID, 7, errs, false)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !res.Partial || res.Interrupted {
		t.Errorf("result = %+v, want Partial && !Interrupted", res)
	}
	if sc := scanStatus(t, st, scanID); sc.Status != "partial" {
		t.Errorf("status = %q, want partial", sc.Status)
	}
}
