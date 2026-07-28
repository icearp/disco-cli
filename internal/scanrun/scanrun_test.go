package scanrun

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/providers"
	"github.com/icearp/disco-cli/store"
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

	_, _, errs1, _, _ := RunScanners(context.Background(), st, "scan-1", scanners)
	if len(errs1) != 1 {
		t.Fatalf("run 1: want 1 error, got %d", len(errs1))
	}
	if st.OnWarn != nil || st.OnError != nil || st.OnNotice != nil {
		t.Fatalf("callbacks not restored after run 1: OnNotice=%v OnWarn=%v OnError=%v",
			st.OnNotice != nil, st.OnWarn != nil, st.OnError != nil)
	}

	// Second run on the same store must capture exactly its own error, proving
	// it did not chain onto run 1's leaked closure.
	_, _, errs2, _, _ := RunScanners(context.Background(), st, "scan-2", scanners)
	if len(errs2) != 1 {
		t.Fatalf("run 2: want 1 error (no chaining), got %d", len(errs2))
	}
	if st.OnWarn != nil || st.OnError != nil || st.OnServiceComplete != nil || st.OnNotice != nil {
		t.Fatalf("callbacks not restored after run 2")
	}
}

// noticingScanner emits one of each severity so a test can prove they land in
// separate buckets.
type noticingScanner struct{}

func (noticingScanner) Name() string { return "aws" }

func (noticingScanner) Scan(_ context.Context, st *store.Store, _ string) error {
	st.ReportNotice(store.ScanNotice{Provider: "aws", Service: "preflight:regions", Message: "skipping region(s) not enabled for this account: af-south-1"})
	st.ReportWarning(store.ScanWarning{Provider: "aws", Service: "kms:ListKeys", Message: "denied"})
	return nil
}

// TestRunScanners_NoticesAreNotWarnings pins the separation that motivates the
// notice channel. Pruning regions an account never opted into happens on every
// healthy scan, so counting it as a warning meant no scan could ever report
// zero warnings — and a block that is never empty is a block people stop
// reading. The assertion that matters is the warning count, not the notice
// count: a mutant routing ReportNotice to OnWarn still delivers the message,
// just under the heading that makes it noise.
func TestRunScanners_NoticesAreNotWarnings(t *testing.T) {
	st := &store.Store{}

	notices, warnings, _, _, _ := RunScanners(context.Background(), st, "scan-1", []providers.Scanner{noticingScanner{}})

	if len(warnings) != 1 {
		t.Errorf("warnings = %d, want exactly 1 (the access denial); a by-design region skip must not land here", len(warnings))
	}
	if len(notices) != 1 {
		t.Errorf("notices = %d, want 1", len(notices))
	}
	if len(warnings) == 1 && !strings.Contains(warnings[0].Message, "denied") {
		t.Errorf("warnings[0] = %q; want the access denial, not the region skip", warnings[0].Message)
	}
	if len(notices) == 1 && !strings.Contains(notices[0].Message, "not enabled for this account") {
		t.Errorf("notices[0] = %q; want the region-skip notice", notices[0].Message)
	}
}

// TestRunScanners_AccumulatesTotals pins that RunScanners sums the per-service
// write outcomes.
//
// It deliberately does NOT sum each service's self-reported `total`. Scanners
// run concurrently over independent scopes and nothing dedupes across them, so
// that sum counts an identity once per emitting scope; scans.resource_count is
// derived from the rows instead (store.CompleteScan). Returning the sum anyway
// would leave a wrong number in reach of the next caller who needs one.
func TestRunScanners_AccumulatesTotals(t *testing.T) {
	st := &store.Store{}
	scanners := []providers.Scanner{fakeScanner{name: "aws"}, fakeScanner{name: "gcp"}}

	_, _, _, totalNew, totalChanged := RunScanners(context.Background(), st, "scan-1", scanners)
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

	res, err := Finalize(st, scanID, nil, nil, true)
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

	res, err := Finalize(st, scanID, nil, nil, false)
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
	res, err := Finalize(st, scanID, errs, nil, false)
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

// parsedWarnings reads scans.warnings back off the row. Reading the persisted
// column rather than trusting the FinalizeResult is the point: the whole feature
// is that a remote consumer can see these after the process has exited.
func parsedWarnings(t *testing.T, st *store.Store, id string) []store.ScanWarningEntry {
	t.Helper()
	sc := scanStatus(t, st, id)
	if sc.WarningsJSON == nil {
		t.Fatalf("scan %s has NULL warnings; the column defaults to '[]'", id)
	}
	var out []store.ScanWarningEntry
	if err := json.Unmarshal([]byte(*sc.WarningsJSON), &out); err != nil {
		t.Fatalf("unmarshal warnings %q: %v", *sc.WarningsJSON, err)
	}
	return out
}

// TestFinalize_WarningsPersistOnCleanPath is the assertion the whole change
// exists for. A scan with warnings and zero errors is the NORMAL case — a
// per-region availability gap on an otherwise healthy account — so persisting
// warnings only on the partial branch would drop them exactly when they are the
// only signal there is. The status assertion is equally load-bearing in the
// other direction: a warning must never degrade a scan to partial.
func TestFinalize_WarningsPersistOnCleanPath(t *testing.T) {
	st, scanID := newFinalizeStore(t)

	warns := []store.ScanWarning{{
		Provider: "aws", Service: "bedrockagentcore",
		Scope: "228886154857/us-west-1", Message: "AccessDeniedException",
	}}
	res, err := Finalize(st, scanID, nil, warns, false)
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if res.Partial || res.Interrupted {
		t.Errorf("result = %+v, want clean; a warning is not a failure", res)
	}
	if sc := scanStatus(t, st, scanID); sc.Status != "completed" {
		t.Errorf("status = %q, want completed; warnings must not influence status", sc.Status)
	}

	got := parsedWarnings(t, st, scanID)
	if len(got) != 1 {
		t.Fatalf("persisted %d warnings, want 1", len(got))
	}
	if got[0].Service != "aws:bedrockagentcore" {
		t.Errorf("service = %q, want the provider-qualified name", got[0].Service)
	}
	if got[0].Region != "us-west-1" {
		t.Errorf("region = %q, want it parsed off the scope", got[0].Region)
	}
	// The errors path throws the original scope away; warnings keep it, because
	// "<account>/<region>" is what ties an entry back to a scanner log line.
	if got[0].Scope != "228886154857/us-west-1" {
		t.Errorf("scope = %q, want it kept verbatim", got[0].Scope)
	}
}

// TestFinalize_WarningsPersistOnPartialPath: the partial branch returns before
// the clean branch's persist call, so it needs its own write. Errors and
// warnings must both land, in their own columns.
func TestFinalize_WarningsPersistOnPartialPath(t *testing.T) {
	st, scanID := newFinalizeStore(t)

	errs := []store.ScanError{{Provider: "aws", Service: "ec2", Message: "boom"}}
	warns := []store.ScanWarning{{Provider: "aws", Service: "kms", Scope: "acct/eu-west-1", Message: "denied"}}
	if _, err := Finalize(st, scanID, errs, warns, false); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	if got := parsedWarnings(t, st, scanID); len(got) != 1 || got[0].Message != "denied" {
		t.Errorf("warnings = %+v, want the one reported warning", got)
	}
	sc := scanStatus(t, st, scanID)
	if sc.ErrorsJSON == nil || !strings.Contains(*sc.ErrorsJSON, "boom") {
		t.Errorf("errors = %v, want the error still in its own column", sc.ErrorsJSON)
	}
}

// TestFinalize_WarningsAreCapped guards the overflow marker. Warnings are
// numerous in healthy operation (one per region per service for an availability
// gap), so an uncapped array is a when, not an if — and a cap that dropped
// silently would make a rendered count a quiet lie.
func TestFinalize_WarningsAreCapped(t *testing.T) {
	st, scanID := newFinalizeStore(t)

	warns := make([]store.ScanWarning, maxPersistedWarnings+50)
	for i := range warns {
		warns[i] = store.ScanWarning{Provider: "aws", Service: "svc", Message: fmt.Sprintf("w%d", i)}
	}
	if _, err := Finalize(st, scanID, nil, warns, false); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	got := parsedWarnings(t, st, scanID)
	if len(got) != maxPersistedWarnings+1 {
		t.Fatalf("persisted %d entries, want %d kept + 1 truncation marker",
			len(got), maxPersistedWarnings)
	}
	last := got[len(got)-1]
	if last.Service != warningsTruncatedService {
		t.Errorf("last entry service = %q, want %q", last.Service, warningsTruncatedService)
	}
	if !strings.Contains(last.Message, "50") {
		t.Errorf("truncation marker = %q, want it to name the 50 dropped warnings", last.Message)
	}
}
