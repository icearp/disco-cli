package store

import (
	"sync"
	"sync/atomic"
	"testing"
)

// mkNode inserts a bare resource and returns its ID.
func mkNode(t *testing.T, st *Store, native string) string {
	t.Helper()
	r := &Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:instance",
		NativeID: native, AttributesJSON: "{}", DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsert %s: %v", native, err)
	}
	return r.ID
}

// TestUpsertRelationships_Batch pins that the batch primitive writes every edge
// and is idempotent on re-run (same ON CONFLICT semantics as the singular form).
func TestUpsertRelationships_Batch(t *testing.T) {
	st := openTestStore(t)
	a, b, c := mkNode(t, st, "i-A"), mkNode(t, st, "i-B"), mkNode(t, st, "i-C")

	edges := []RelEdge{
		{FromID: a, ToID: b, Kind: RelUses},
		{FromID: a, ToID: c, Kind: RelUses},
		{FromID: b, ToID: c, Kind: RelAttachedTo}, // empty Direction → "directed"
	}
	if err := st.UpsertRelationships(edges); err != nil {
		t.Fatalf("UpsertRelationships: %v", err)
	}
	got, err := st.ListRelationships()
	if err != nil {
		t.Fatalf("ListRelationships: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("after batch: got %d edges, want 3", len(got))
	}
	for _, r := range got {
		if r.Direction != "directed" {
			t.Errorf("edge %s->%s: direction %q, want directed", r.FromID, r.ToID, r.Direction)
		}
	}

	// Idempotent: re-running the same batch must not duplicate rows.
	if err := st.UpsertRelationships(edges); err != nil {
		t.Fatalf("UpsertRelationships rerun: %v", err)
	}
	got, _ = st.ListRelationships()
	if len(got) != 3 {
		t.Errorf("after rerun: got %d edges, want 3 (idempotent)", len(got))
	}
}

// TestUpsertRelationships_Empty pins the no-op path.
func TestUpsertRelationships_Empty(t *testing.T) {
	st := openTestStore(t)
	if err := st.UpsertRelationships(nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
}

// TestRelBuffer_DefersThenFlushes pins the buffer seam: edges emitted on a
// buffered store aren't written until FlushRelBuffer, and activeCounter
// still advances so ReportResolveComplete's tally is unaffected.
func TestRelBuffer_DefersThenFlushes(t *testing.T) {
	st := openTestStore(t)
	a, b, c := mkNode(t, st, "i-A"), mkNode(t, st, "i-B"), mkNode(t, st, "i-C")

	var counter atomic.Int64
	bs := st.WithRelCounter(&counter).BeginRelBuffer()
	if err := bs.UpsertRelationship(a, b, RelUses, "", nil); err != nil {
		t.Fatalf("buffered upsert: %v", err)
	}
	if err := bs.UpsertRelationship(a, c, RelUses, "", nil); err != nil {
		t.Fatalf("buffered upsert: %v", err)
	}

	// Counter advances on buffer append (edge tally), but nothing is in the DB.
	if counter.Load() != 2 {
		t.Errorf("counter = %d, want 2", counter.Load())
	}
	if got, _ := st.ListRelationships(); len(got) != 0 {
		t.Fatalf("before flush: %d edges in DB, want 0 (buffered)", len(got))
	}

	if err := bs.FlushRelBuffer(); err != nil {
		t.Fatalf("FlushRelBuffer: %v", err)
	}
	if got, _ := st.ListRelationships(); len(got) != 2 {
		t.Errorf("after flush: %d edges, want 2", len(got))
	}

	// Buffer cleared: a second flush is a no-op and writes nothing new.
	if err := bs.FlushRelBuffer(); err != nil {
		t.Fatalf("second FlushRelBuffer: %v", err)
	}
	if got, _ := st.ListRelationships(); len(got) != 2 {
		t.Errorf("after second flush: %d edges, want 2", len(got))
	}
}

// TestRelBuffer_ConcurrentResolversPattern reproduces the provider phase-2
// dispatch under the race detector: N "resolvers" run in parallel, each
// taking its own buffered store off the shared counter-bound store, emitting
// edges, reporting an error via OnError, and flushing. Pins that the buffer
// seam + activeCounter + ReportError path is data-race-free under
// concurrency (run with `go test -race`). Mirrors aws/gcp/azure's
// resolveRelationships.
func TestRelBuffer_ConcurrentResolversPattern(t *testing.T) {
	st := openTestStore(t)
	const n = 16
	ids := make([]string, n)
	for i := range ids {
		ids[i] = mkNode(t, st, "i-"+string(rune('A'+i)))
	}

	var counter atomic.Int64
	var errMu sync.Mutex
	var errCount int
	base := st.WithRelCounter(&counter)
	// Emulate the scanrun-installed, mutex-guarded OnError closure.
	base.OnError = func(ScanError) {
		errMu.Lock()
		errCount++
		errMu.Unlock()
	}

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bs := base.BeginRelBuffer()
			// Every resolver links node i -> node (i+1)%n.
			if err := bs.UpsertRelationship(ids[i], ids[(i+1)%n], RelUses, "", nil); err != nil {
				t.Errorf("buffered upsert: %v", err)
			}
			base.ReportError(ScanError{Provider: "test", Service: "resolve"})
			if err := bs.FlushRelBuffer(); err != nil {
				t.Errorf("flush: %v", err)
			}
		}()
	}
	wg.Wait()

	if counter.Load() != n {
		t.Errorf("counter = %d, want %d", counter.Load(), n)
	}
	if errCount != n {
		t.Errorf("errCount = %d, want %d", errCount, n)
	}
	got, _ := st.ListRelationships()
	if len(got) != n {
		t.Errorf("edges = %d, want %d", len(got), n)
	}
}

// TestBeginRelBuffer_IndependentBuffers pins that two buffered stores derived
// from the same parent don't share a buffer — the property Azure's parallel
// resolver errgroup relies on.
func TestBeginRelBuffer_IndependentBuffers(t *testing.T) {
	st := openTestStore(t)
	a, b, c := mkNode(t, st, "i-A"), mkNode(t, st, "i-B"), mkNode(t, st, "i-C")

	bs1 := st.BeginRelBuffer()
	bs2 := st.BeginRelBuffer()
	if err := bs1.UpsertRelationship(a, b, RelUses, "", nil); err != nil {
		t.Fatalf("bs1 upsert: %v", err)
	}
	if err := bs2.UpsertRelationship(a, c, RelUses, "", nil); err != nil {
		t.Fatalf("bs2 upsert: %v", err)
	}

	// Flushing bs1 writes only its own edge.
	if err := bs1.FlushRelBuffer(); err != nil {
		t.Fatalf("bs1 flush: %v", err)
	}
	if got, _ := st.ListRelationships(); len(got) != 1 {
		t.Fatalf("after bs1 flush: %d edges, want 1", len(got))
	}
	if err := bs2.FlushRelBuffer(); err != nil {
		t.Fatalf("bs2 flush: %v", err)
	}
	if got, _ := st.ListRelationships(); len(got) != 2 {
		t.Errorf("after bs2 flush: %d edges, want 2", len(got))
	}
}

// TestUpsertRelationships_RefreshesAttributes covers the UPDATE half of the
// UPDATE-then-INSERT upsert on the SQLite backend, which is the one the CLI
// ships. Every other edge test passes nil attrs, so without this the refresh is
// exercised only by the docker-gated Postgres tests.
//
// Three claims: a re-upsert of an existing edge overwrites attributes in place
// rather than duplicating the row, a repeat WITHIN one batch lets the later
// element win (its UPDATE must see the INSERT its predecessor made in the same
// uncommitted transaction), and direction is not refreshed.
func TestUpsertRelationships_RefreshesAttributes(t *testing.T) {
	st := openTestStore(t)
	a, b := mkNode(t, st, "i-refresh-A"), mkNode(t, st, "i-refresh-B")

	first, second, third := `{"seen":"first"}`, `{"seen":"second"}`, `{"seen":"third"}`

	if err := st.UpsertRelationship(a, b, RelUses, "undirected", &first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if err := st.UpsertRelationship(a, b, RelUses, "directed", &second); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	got, err := st.ListRelationships()
	if err != nil {
		t.Fatalf("ListRelationships: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("after re-upsert: %d edges, want 1", len(got))
	}
	if derefStr(got[0].Attributes) != second {
		t.Errorf("attributes = %q, want %q (the re-upsert must refresh in place)",
			derefStr(got[0].Attributes), second)
	}
	if got[0].Direction != "undirected" {
		t.Errorf("direction = %q, want %q — direction is written on insert only",
			got[0].Direction, "undirected")
	}

	// A batch carrying the same edge twice: the second element's UPDATE has to
	// see the first element's row inside the still-open transaction.
	if err := st.UpsertRelationships([]RelEdge{
		{FromID: a, ToID: b, Kind: RelAttachedTo, Attrs: &first},
		{FromID: a, ToID: b, Kind: RelAttachedTo, Attrs: &third},
	}); err != nil {
		t.Fatalf("batch with a repeated edge: %v", err)
	}
	got, err = st.ListRelationships()
	if err != nil {
		t.Fatalf("ListRelationships after batch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("after batch: %d edges, want 2 (the repeat must not duplicate)", len(got))
	}
	for _, r := range got {
		if r.Kind == RelAttachedTo && derefStr(r.Attributes) != third {
			t.Errorf("batched edge attributes = %q, want %q (later element wins)",
				derefStr(r.Attributes), third)
		}
	}
}
