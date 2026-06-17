package scanrun

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/store"
)

// fakeScanner reports one service (5 seen, 3 new) and one error per Scan via
// the store callbacks, exercising the OnServiceComplete and OnError chains
// RunScanners installs.
type fakeScanner struct{ name string }

func (f fakeScanner) Name() string { return f.name }

func (f fakeScanner) Scan(_ context.Context, st *store.Store, _ string) error {
	st.ReportService("svc", "global", 5, 3, 0, false)
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

	_, errs1, _, _ := RunScanners(context.Background(), st, "scan-1", scanners)
	if len(errs1) != 1 {
		t.Fatalf("run 1: want 1 error, got %d", len(errs1))
	}
	if st.OnWarn != nil || st.OnError != nil {
		t.Fatalf("callbacks not restored after run 1: OnWarn=%v OnError=%v", st.OnWarn != nil, st.OnError != nil)
	}

	// Second run on the same store must capture exactly its own error, proving
	// it did not chain onto run 1's leaked closure.
	_, errs2, _, _ := RunScanners(context.Background(), st, "scan-2", scanners)
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

	_, _, totalSeen, totalNew := RunScanners(context.Background(), st, "scan-1", scanners)
	if totalSeen != 10 { // 2 scanners × 5 seen
		t.Errorf("totalSeen = %d, want 10", totalSeen)
	}
	if totalNew != 6 { // 2 scanners × 3 new
		t.Errorf("totalNew = %d, want 6", totalNew)
	}
}
