package store

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"
)

// withDialects runs fn against SQLite and against a real Postgres. Ordering is
// the property these tests exist to pin, and it is exactly the property that
// differs between the two: the singular and batched readers hit different
// plans on Postgres (index scan vs sequential scan + sort), so a SQLite-only
// run certifies an equivalence that does not hold where the SaaS consumer
// actually runs. The Postgres subtest skips when Docker is unreachable; the
// SQLite one always runs.
func withDialects(t *testing.T, fn func(t *testing.T, st *Store)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { fn(t, openTestStore(t)) })
	t.Run("postgres", func(t *testing.T) {
		dsn, purge := pgTestEnv(t)
		t.Cleanup(purge)
		st, err := OpenPostgres(context.Background(), dsn)
		if err != nil {
			t.Fatalf("open pg: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })
		if _, err := st.exec(`
			INSERT INTO scans (id, started_at, status, providers, scope)
			VALUES (?, ?, 'running', '["test"]', '{}')
			ON CONFLICT (id) DO NOTHING`,
			testScanID, time.Now().UTC().Format(time.RFC3339)); err != nil {
			t.Fatalf("seed scan: %v", err)
		}
		fn(t, st)
	})
}

// seedTies wires a hub to n leaves in BOTH directions across two kinds, so
// every (endpoint, kind) group holds several rows that differ only in the
// opposite endpoint. Those groups are the ties the ORDER BY tiebreaker exists
// to resolve; without them a fixture cannot tell a total ordering from a plan
// artifact that happens to agree.
//
// Edges are inserted in reverse id order so insertion order never coincides
// with sorted order — an implementation that returns rows in physical order
// diverges rather than passing by luck.
func seedTies(t *testing.T, st *Store, n int) (hub string, leaves []string) {
	t.Helper()
	hub = mkNode(t, st, "i-hub")
	for i := range n {
		leaves = append(leaves, mkNode(t, st, fmt.Sprintf("i-leaf-%03d", i)))
	}
	sorted := append([]string{}, leaves...)
	for i := len(sorted) - 1; i >= 0; i-- {
		leaf := sorted[i]
		kind := RelUses
		if i%2 == 1 {
			kind = RelAttachedTo
		}
		if err := st.UpsertRelationship(hub, leaf, kind, "directed", nil); err != nil {
			t.Fatalf("rel hub->%s: %v", leaf, err)
		}
		if err := st.UpsertRelationship(leaf, hub, kind, "directed", nil); err != nil {
			t.Fatalf("rel %s->hub: %v", leaf, err)
		}
	}
	return hub, leaves
}

// TestRelationshipsMany_MatchPerIDSingular is the contract the batched readers
// exist to preserve: bucketing a batched read by its endpoint column must
// reproduce, row for row and in order, what the singular reader returns for
// each id. GraphWalk's MaxEdges cap discards candidates by position, so a
// batched walk returning the same SET in a different ORDER would silently
// change which edges a truncated walk keeps.
func TestRelationshipsMany_MatchPerIDSingular(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		hub, leaves := seedTies(t, st, 12)
		ids := append([]string{hub}, leaves...)

		t.Run("from", func(t *testing.T) {
			batched, err := st.RelationshipsFromMany(ids)
			if err != nil {
				t.Fatalf("RelationshipsFromMany: %v", err)
			}
			byID := groupRelationships(batched, func(r Relationship) string { return r.FromID })
			for _, id := range ids {
				want, err := st.RelationshipsFrom(id)
				if err != nil {
					t.Fatalf("RelationshipsFrom(%s): %v", id, err)
				}
				if got := byID[id]; !reflect.DeepEqual(got, want) {
					t.Errorf("RelationshipsFromMany bucket for %s = %v; want %v", id, got, want)
				}
			}
		})

		t.Run("to", func(t *testing.T) {
			batched, err := st.RelationshipsToMany(ids)
			if err != nil {
				t.Fatalf("RelationshipsToMany: %v", err)
			}
			byID := groupRelationships(batched, func(r Relationship) string { return r.ToID })
			for _, id := range ids {
				want, err := st.RelationshipsTo(id)
				if err != nil {
					t.Fatalf("RelationshipsTo(%s): %v", id, err)
				}
				if got := byID[id]; !reflect.DeepEqual(got, want) {
					t.Errorf("RelationshipsToMany bucket for %s = %v; want %v", id, got, want)
				}
			}
		})
	})
}

// TestCollectFrontierEdges_MatchesPerNodeWalk pins the batching at the call
// site rather than in the readers: collectFrontierEdges must emit candidates in
// the same order the per-node walk it replaced did, since GraphWalk truncates
// that slice positionally.
func TestCollectFrontierEdges_MatchesPerNodeWalk(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		hub, leaves := seedTies(t, st, 12)
		frontier := append([]string{hub}, leaves...)

		for _, dir := range []string{DirOut, DirIn, DirBoth} {
			t.Run(dir, func(t *testing.T) {
				got, _, err := st.collectFrontierEdges(frontier, dir, nil, map[string]int{})
				if err != nil {
					t.Fatalf("collectFrontierEdges: %v", err)
				}

				var want []GraphEdge
				for _, id := range frontier {
					if dir == DirOut || dir == DirBoth {
						rels, err := st.RelationshipsFrom(id)
						if err != nil {
							t.Fatalf("RelationshipsFrom(%s): %v", id, err)
						}
						for _, r := range rels {
							want = append(want, GraphEdge{FromID: r.FromID, ToID: r.ToID, Kind: r.Kind})
						}
					}
					if dir == DirIn || dir == DirBoth {
						rels, err := st.RelationshipsTo(id)
						if err != nil {
							t.Fatalf("RelationshipsTo(%s): %v", id, err)
						}
						for _, r := range rels {
							want = append(want, GraphEdge{FromID: r.FromID, ToID: r.ToID, Kind: r.Kind})
						}
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("collectFrontierEdges(%s) emitted %d candidates in a different order than the per-node walk's %d", dir, len(got), len(want))
				}
			})
		}
	})
}

// TestGraphWalk_MaxEdgesTruncationIsStable closes the loop at the layer that
// actually pays for the ordering: a capped walk must keep exactly the edges
// the per-node reader order would have kept. The two tests above prove
// candidate order; this proves the cap consumes it as expected, which is the
// customer-visible behavior — the SaaS blast-radius view caps every request.
//
// Compared as a SET, not a sequence: GraphWalk sorts result.Edges by
// (from_id, kind, to_id) before returning, so its output order says nothing
// about the candidate order the cap actually ate. Which edges survive is the
// property at stake; the order they are reported in is already normalized.
func TestGraphWalk_MaxEdgesTruncationIsStable(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		hub, _ := seedTies(t, st, 12)

		// The oracle comes from the SINGULAR readers, never from
		// collectFrontierEdges — deriving it from the code under test would
		// make this pass against any self-consistent ordering, including the
		// broken one.
		var candidates []GraphEdge
		for _, load := range []func(string, ...string) ([]Relationship, error){
			st.RelationshipsFrom, st.RelationshipsTo,
		} {
			rels, err := load(hub)
			if err != nil {
				t.Fatalf("seed edges: %v", err)
			}
			for _, r := range rels {
				candidates = append(candidates, GraphEdge{FromID: r.FromID, ToID: r.ToID, Kind: r.Kind})
			}
		}
		if len(candidates) < 24 {
			t.Fatalf("fixture yielded %d seed edges; too few to exercise a cap", len(candidates))
		}

		for _, maxEdges := range []int{1, 5, 13, len(candidates)} {
			t.Run(fmt.Sprintf("cap%d", maxEdges), func(t *testing.T) {
				res, err := st.GraphWalk(hub, GraphWalkOpts{
					MaxDepth: 2, Direction: DirBoth, MaxEdges: maxEdges, IncludeManaged: true,
				})
				if err != nil {
					t.Fatalf("GraphWalk: %v", err)
				}
				if len(res.Edges) != maxEdges {
					t.Fatalf("MaxEdges=%d kept %d edges; want the cap to fill", maxEdges, len(res.Edges))
				}
				// The cap fills from the depth-0 frontier first, in the order
				// collectFrontierEdges emits it, then GraphWalk sorts.
				want := append([]GraphEdge{}, candidates[:maxEdges]...)
				sortGraphEdges(want)
				got := append([]GraphEdge{}, res.Edges...)
				sortGraphEdges(got)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("MaxEdges=%d kept %v; want the first %d candidates %v", maxEdges, got, maxEdges, want)
				}
			})
		}
	})
}

// sortGraphEdges applies GraphWalk's own result ordering so two edge sets can
// be compared independently of the order they were collected in.
func sortGraphEdges(es []GraphEdge) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].FromID != es[j].FromID {
			return es[i].FromID < es[j].FromID
		}
		if es[i].Kind != es[j].Kind {
			return es[i].Kind < es[j].Kind
		}
		return es[i].ToID < es[j].ToID
	})
}

// The remaining tests cover batching mechanics — chunking, dedup, empty input,
// predicate assembly — which are dialect-independent Go logic, so they run on
// SQLite alone rather than paying for a container each.

// TestRelationshipsMany_KindsFilter checks the optional kind narrowing survives
// batching. The batched form assembles its two IN clauses in one statement, so
// a dropped predicate would silently widen every graph walk that passes Kinds.
func TestRelationshipsMany_KindsFilter(t *testing.T) {
	st := openTestStore(t)
	hub, _ := seedTies(t, st, 6)

	got, err := st.RelationshipsFromMany([]string{hub}, RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFromMany: %v", err)
	}
	want, err := st.RelationshipsFrom(hub, RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RelationshipsFromMany(hub, %q) = %v; want %v", RelUses, got, want)
	}
	if len(got) == 0 {
		t.Fatal("kind-filtered read returned nothing; fixture cannot detect a dropped predicate")
	}
	for _, r := range got {
		if r.Kind != RelUses {
			t.Errorf("RelationshipsFromMany returned kind %q; want only %q", r.Kind, RelUses)
		}
	}
}

// TestRelationshipsMany_KindsFilterAcrossBatches combines the two features that
// share one statement. The kinds argument is rebuilt per chunk, so a hoisted
// args slice would append kinds again on every iteration and bind a growing
// parameter list against a fixed placeholder count.
func TestRelationshipsMany_KindsFilterAcrossBatches(t *testing.T) {
	st := openTestStore(t)
	_, leaves := seedTies(t, st, relIDBatchSize+10)

	got, err := st.RelationshipsFromMany(leaves, RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFromMany: %v", err)
	}
	unfiltered, err := st.RelationshipsFromMany(leaves)
	if err != nil {
		t.Fatalf("RelationshipsFromMany unfiltered: %v", err)
	}
	if len(got) == 0 || len(got) >= len(unfiltered) {
		t.Fatalf("kind-filtered multi-batch read returned %d of %d rows; want a nonempty proper subset", len(got), len(unfiltered))
	}
	for _, r := range got {
		if r.Kind != RelUses {
			t.Errorf("multi-batch kind filter returned kind %q; want only %q", r.Kind, RelUses)
		}
	}
}

// TestRelationshipsMany_Empty pins that an empty frontier short-circuits. The
// naive form would build `IN ()`, which is a syntax error rather than an empty
// result, so this is a real deny case and not a triviality.
func TestRelationshipsMany_Empty(t *testing.T) {
	st := openTestStore(t)
	seedTies(t, st, 2)

	for _, tc := range []struct {
		name string
		fn   func([]string, ...string) ([]Relationship, error)
	}{
		{"from", st.RelationshipsFromMany},
		{"to", st.RelationshipsToMany},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn(nil)
			if err != nil {
				t.Fatalf("nil ids: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("nil ids returned %d rows; want 0", len(got))
			}
		})
	}
}

// TestRelationshipsMany_DuplicateIDsAcrossBatches guards the dedup: a repeated
// id must not duplicate its rows, or a frontier containing one twice would
// double-count every edge it owns against GraphWalk's MaxEdges cap.
//
// Only the across-batches case can detect a missing dedup. Within one batch
// `IN (a, a)` already returns each row once, so a small fixture stays green
// against the exact bug — the repeat has to straddle a chunk boundary to be
// read by two separate queries.
func TestRelationshipsMany_DuplicateIDsAcrossBatches(t *testing.T) {
	st := openTestStore(t)
	_, leaves := seedTies(t, st, relIDBatchSize+10)

	// leaves[0] lands in the first chunk; repeating it at the tail puts a
	// second copy in the last chunk, so an undeduped read queries it twice.
	ids := append(append([]string{}, leaves...), leaves[0])
	if len(ids) <= relIDBatchSize {
		t.Fatalf("fixture of %d ids does not span a %d-id batch", len(ids), relIDBatchSize)
	}
	got, err := st.RelationshipsFromMany(ids)
	if err != nil {
		t.Fatalf("RelationshipsFromMany: %v", err)
	}
	want, err := st.RelationshipsFromMany(leaves)
	if err != nil {
		t.Fatalf("RelationshipsFromMany without the repeat: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a repeat straddling a batch boundary returned %d rows; want the %d of the distinct set", len(got), len(want))
	}
}

// TestRelationshipsMany_SpansBatches drives more ids than relIDBatchSize so the
// chunking loop runs more than once. A single-chunk implementation passes every
// other test here and then silently drops rows on a wide frontier.
func TestRelationshipsMany_SpansBatches(t *testing.T) {
	st := openTestStore(t)
	n := relIDBatchSize + 10
	_, leaves := seedTies(t, st, n)

	got, err := st.RelationshipsFromMany(leaves)
	if err != nil {
		t.Fatalf("RelationshipsFromMany: %v", err)
	}
	// seedTies gives every leaf exactly one out-edge, back to the hub.
	if len(got) != n {
		t.Errorf("RelationshipsFromMany over %d ids returned %d rows; want %d", len(leaves), len(got), n)
	}
	seen := map[string]bool{}
	for _, r := range got {
		seen[r.FromID] = true
	}
	if len(seen) != n {
		t.Errorf("rows covered %d distinct from_ids; want %d", len(seen), n)
	}
}
