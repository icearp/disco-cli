package scanrun

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/internal/providers"
	"codeberg.org/icearp/disco/store"
)

// fakeScanner reports one error per Scan via the store callback, exercising the
// OnError chain RunScanners installs.
type fakeScanner struct{ name string }

func (f fakeScanner) Name() string { return f.name }

func (f fakeScanner) Scan(_ context.Context, st *store.Store, _ string) error {
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

	_, errs1 := RunScanners(context.Background(), st, "scan-1", scanners)
	if len(errs1) != 1 {
		t.Fatalf("run 1: want 1 error, got %d", len(errs1))
	}
	if st.OnWarn != nil || st.OnError != nil {
		t.Fatalf("callbacks not restored after run 1: OnWarn=%v OnError=%v", st.OnWarn != nil, st.OnError != nil)
	}

	// Second run on the same store must capture exactly its own error, proving
	// it did not chain onto run 1's leaked closure.
	_, errs2 := RunScanners(context.Background(), st, "scan-2", scanners)
	if len(errs2) != 1 {
		t.Fatalf("run 2: want 1 error (no chaining), got %d", len(errs2))
	}
	if st.OnWarn != nil || st.OnError != nil {
		t.Fatalf("callbacks not restored after run 2")
	}
}
