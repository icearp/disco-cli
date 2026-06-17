package store

import (
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
// buffered store are not written until FlushRelBuffer, and the activeCounter
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
