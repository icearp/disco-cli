package store

import (
	"sync/atomic"
	"testing"
)

func retryRes(nativeID, attrs string) *Resource {
	return &Resource{
		Provider: "aws", AccountID: "111", Type: "aws:s3:bucket",
		NativeID: nativeID, AttributesJSON: attrs, DiscoveredBy: testScanID,
	}
}

// The scan progress line's new/changed columns come from the shared atomics, so
// the retry refactor (which moved counting into a per-attempt local) must still
// report exactly what the pre-refactor code did.
func TestUpsertResources_CountersSurviveRetryRefactor(t *testing.T) {
	st := openTestStore(t)
	var newC, changedC atomic.Int64
	sc := st.WithUpsertCounters(&newC, &changedC)

	if _, err := sc.UpsertResources([]*Resource{
		retryRes("b-1", `{"A":1}`),
		retryRes("b-2", `{"A":1}`),
	}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if newC.Load() != 2 || changedC.Load() != 0 {
		t.Fatalf("first discovery: new=%d changed=%d, want 2/0", newC.Load(), changedC.Load())
	}

	// Re-upsert one unchanged and one with different attributes: the unchanged
	// row is a verify-only update, the other is a version split.
	newC.Store(0)
	changedC.Store(0)
	if _, err := sc.UpsertResources([]*Resource{
		retryRes("b-1", `{"A":1}`),
		retryRes("b-2", `{"A":2}`),
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if newC.Load() != 0 || changedC.Load() != 1 {
		t.Errorf("rescan: new=%d changed=%d, want 0/1", newC.Load(), changedC.Load())
	}
}

// Counter integrity under retry: an attempt that fails partway has already
// counted some rows, so the counts must be per-attempt rather than cumulative.
// Calling the transactional body repeatedly proves it starts from zero each
// time — if it accumulated, the second call would report the first call's rows
// again and inflate the scan's new/changed columns on every retry.
func TestUpsertResourcesTx_CountsArePerAttempt(t *testing.T) {
	st := openTestStore(t)
	batch := []*Resource{retryRes("b-1", `{"A":1}`), retryRes("b-2", `{"A":1}`)}
	for _, r := range batch {
		r.ID = ResourceID(r.Provider, r.AccountID, r.NativeID)
	}

	inserted, newC, changedC, err := st.upsertResourcesTx(batch, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if inserted != 2 || newC != 2 || changedC != 0 {
		t.Fatalf("first attempt: inserted=%d new=%d changed=%d, want 2/2/0", inserted, newC, changedC)
	}

	// Same payload again: every row is now unchanged, so a fresh attempt must
	// report zeros rather than adding to the previous attempt's tally.
	inserted, newC, changedC, err = st.upsertResourcesTx(batch, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if inserted != 0 || newC != 0 || changedC != 0 {
		t.Errorf("second attempt: inserted=%d new=%d changed=%d, want 0/0/0", inserted, newC, changedC)
	}
}

// Shared-counter atomics are process-visible the instant they are bumped, and a
// rolled-back attempt cannot un-bump them. So the retried unit — the *Tx
// function — must publish nothing: it reports its count as a return value and
// the caller publishes only after the commit. Assert both halves.
func TestResourceWriteTxHelpersPublishNoSharedCounters(t *testing.T) {
	st := openTestStore(t)
	var newC, changedC atomic.Int64
	sc := st.WithUpsertCounters(&newC, &changedC)
	const now = "2026-01-01T00:00:00Z"

	absent := []*Resource{retryRes("p-1", `{}`), retryRes("p-2", `{}`)}
	for _, r := range absent {
		r.ID = ResourceID(r.Provider, r.AccountID, r.NativeID)
	}
	inserted, err := sc.insertResourcesIfAbsentTx(absent, now)
	if err != nil {
		t.Fatalf("insertResourcesIfAbsentTx: %v", err)
	}
	if inserted != 2 {
		t.Fatalf("insertResourcesIfAbsentTx inserted=%d, want 2", inserted)
	}
	if newC.Load() != 0 || changedC.Load() != 0 {
		t.Errorf("insertResourcesIfAbsentTx published new=%d changed=%d; the retried unit must publish nothing",
			newC.Load(), changedC.Load())
	}

	upserts := []*Resource{retryRes("u-1", `{"A":1}`)}
	for _, r := range upserts {
		r.ID = ResourceID(r.Provider, r.AccountID, r.NativeID)
	}
	if _, n, c, err := sc.upsertResourcesTx(upserts, now); err != nil {
		t.Fatalf("upsertResourcesTx: %v", err)
	} else if n != 1 || c != 0 {
		t.Fatalf("upsertResourcesTx returned new=%d changed=%d, want 1/0", n, c)
	}
	if newC.Load() != 0 || changedC.Load() != 0 {
		t.Errorf("upsertResourcesTx published new=%d changed=%d; the retried unit must publish nothing",
			newC.Load(), changedC.Load())
	}

	// And the exported wrappers do publish, so the counts are not simply lost.
	if _, err := sc.InsertResourcesIfAbsent([]*Resource{retryRes("p-3", `{}`)}); err != nil {
		t.Fatalf("InsertResourcesIfAbsent: %v", err)
	}
	if newC.Load() != 1 {
		t.Errorf("InsertResourcesIfAbsent published new=%d, want 1", newC.Load())
	}
}

// The duplicate-native_id detector reports a ScanWarning, and it runs in the
// preprocessing loop that must sit OUTSIDE the retry — otherwise a retried
// write would re-report the same collision once per attempt.
func TestUpsertResources_NativeIDWarningNotRepeatedPerAttempt(t *testing.T) {
	st := openTestStore(t)
	var warnings int
	st.OnWarn = func(ScanWarning) { warnings++ }

	// Two types sharing one native_id within a single scan run.
	a := retryRes("dup-1", `{"A":1}`)
	b := retryRes("dup-1", `{"A":1}`)
	b.Type = "aws:ec2:instance"

	if _, err := st.UpsertResources([]*Resource{a, b}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if warnings != 1 {
		t.Errorf("want exactly 1 native_id collision warning, got %d", warnings)
	}
}
