package store

import (
	"errors"
	"strings"
	"testing"
)

// seedDiamond inserts A→B, A→C, B→D, C→D `uses` edges into st and returns IDs.
//
//	  A
//	 / \
//	B   C
//	 \ /
//	  D
func seedDiamond(t *testing.T, st *Store) (a, b, c, d string) {
	t.Helper()
	mk := func(native string) string {
		r := &Resource{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:instance",
			NativeID: native, AttributesJSON: "{}", DiscoveredBy: testScanID,
		}
		if _, err := st.UpsertResource(r); err != nil {
			t.Fatalf("upsert %s: %v", native, err)
		}
		return r.ID
	}
	a, b, c, d = mk("i-A"), mk("i-B"), mk("i-C"), mk("i-D")
	for _, e := range [][2]string{{a, b}, {a, c}, {b, d}, {c, d}} {
		if err := st.UpsertRelationship(e[0], e[1], RelUses, "directed", nil); err != nil {
			t.Fatalf("rel %s->%s: %v", e[0], e[1], err)
		}
	}
	return
}

// TestGraphWalk_Diamond covers the core BFS: reaches all 4 nodes at depth 2,
// records 4 edges without duplicates, and respects the node ordering contract.
func TestGraphWalk_Diamond(t *testing.T) {
	st := openTestStore(t)
	a, _, _, d := seedDiamond(t, st)

	g, err := st.GraphWalk(a, GraphWalkOpts{MaxDepth: 2, Direction: DirBoth})
	if err != nil {
		t.Fatalf("GraphWalk: %v", err)
	}
	if len(g.Nodes) != 4 {
		t.Errorf("Nodes: got %d, want 4", len(g.Nodes))
	}
	if len(g.Edges) != 4 {
		t.Errorf("Edges: got %d, want 4", len(g.Edges))
	}
	if g.Nodes[0].Resource.ID != a || g.Nodes[0].Depth != 0 {
		t.Errorf("Nodes[0]: want seed %s at depth 0, got %+v", a, g.Nodes[0])
	}
	// D reached at depth 2.
	for _, n := range g.Nodes {
		if n.Resource.ID == d && n.Depth != 2 {
			t.Errorf("D depth: got %d, want 2", n.Depth)
		}
	}
}

// TestGraphWalk_MaxNodesNoDanglingEdges asserts that when MaxNodes truncates a
// node, no surviving edge references the dropped node — edges are collected
// before node admission, so the post-walk filter must drop the orphans.
func TestGraphWalk_MaxNodesNoDanglingEdges(t *testing.T) {
	st := openTestStore(t)
	a, _, _, _ := seedDiamond(t, st)

	g, err := st.GraphWalk(a, GraphWalkOpts{MaxDepth: 5, Direction: DirOut, MaxNodes: 2})
	if err != nil {
		t.Fatalf("GraphWalk: %v", err)
	}
	if g.TruncatedNodes == 0 {
		t.Fatalf("expected node truncation with MaxNodes=2, got TruncatedNodes=0")
	}
	nodeIDs := make(map[string]struct{}, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeIDs[n.Resource.ID] = struct{}{}
	}
	for _, e := range g.Edges {
		if _, ok := nodeIDs[e.FromID]; !ok {
			t.Errorf("edge %s->%s: FromID not in node set", e.FromID, e.ToID)
		}
		if _, ok := nodeIDs[e.ToID]; !ok {
			t.Errorf("edge %s->%s: ToID not in node set", e.FromID, e.ToID)
		}
	}
}

// TestGraphWalk_DepthZero asserts that depth=0 returns only the seed.
func TestGraphWalk_DepthZero(t *testing.T) {
	st := openTestStore(t)
	a, _, _, _ := seedDiamond(t, st)

	g, err := st.GraphWalk(a, GraphWalkOpts{MaxDepth: 0, Direction: DirBoth})
	if err != nil {
		t.Fatalf("GraphWalk: %v", err)
	}
	if len(g.Nodes) != 1 || g.Nodes[0].Resource.ID != a {
		t.Errorf("want just seed, got %d nodes", len(g.Nodes))
	}
	if len(g.Edges) != 0 {
		t.Errorf("Edges: got %d, want 0", len(g.Edges))
	}
}

// TestGraphWalk_KindsFilter asserts that Kinds restricts edge traversal.
func TestGraphWalk_KindsFilter(t *testing.T) {
	st := openTestStore(t)
	a, _, _, _ := seedDiamond(t, st)

	g, err := st.GraphWalk(a, GraphWalkOpts{MaxDepth: 5, Direction: DirBoth, Kinds: []string{RelContains}})
	if err != nil {
		t.Fatalf("GraphWalk: %v", err)
	}
	if len(g.Edges) != 0 {
		t.Errorf("Edges for unused kind: got %d, want 0", len(g.Edges))
	}
	if len(g.Nodes) != 1 {
		t.Errorf("Nodes for unused kind: got %d, want 1 (seed only)", len(g.Nodes))
	}
}

// TestGraphWalk_DirectionOutVsIn asserts out-only from A reaches B,C,D but
// in-only from A reaches nobody (A has no inbound edges).
func TestGraphWalk_DirectionOutVsIn(t *testing.T) {
	st := openTestStore(t)
	a, _, _, _ := seedDiamond(t, st)

	out, err := st.GraphWalk(a, GraphWalkOpts{MaxDepth: 5, Direction: DirOut})
	if err != nil {
		t.Fatalf("out: %v", err)
	}
	if len(out.Nodes) != 4 {
		t.Errorf("out nodes: got %d, want 4", len(out.Nodes))
	}

	in, err := st.GraphWalk(a, GraphWalkOpts{MaxDepth: 5, Direction: DirIn})
	if err != nil {
		t.Fatalf("in: %v", err)
	}
	if len(in.Nodes) != 1 {
		t.Errorf("in nodes: got %d, want 1 (seed only)", len(in.Nodes))
	}
}

// TestGraphWalk_UnknownSeed verifies a missing seed ID surfaces an error.
func TestGraphWalk_UnknownSeed(t *testing.T) {
	st := openTestStore(t)
	_, err := st.GraphWalk("deadbeef00000000000000000000ffff", GraphWalkOpts{MaxDepth: 1})
	if err == nil {
		t.Error("expected error for unknown seed")
	}
}

// TestGraphWalk_ManagedTerminal asserts provider-managed nodes appear as
// edge endpoints when reached, but BFS does NOT expand through them. With
// IncludeManaged=true the walk traverses normally.
//
//	A --uses--> B(managed) --uses--> C
func TestGraphWalk_ManagedTerminal(t *testing.T) {
	st := openTestStore(t)
	mk := func(native string, managed bool) string {
		r := &Resource{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:instance",
			NativeID: native, AttributesJSON: "{}", DiscoveredBy: testScanID,
			ManagedByProvider: managed,
		}
		if _, err := st.UpsertResource(r); err != nil {
			t.Fatalf("upsert %s: %v", native, err)
		}
		return r.ID
	}
	a, b, c := mk("i-A", false), mk("i-B", true), mk("i-C", false)
	for _, e := range [][2]string{{a, b}, {b, c}} {
		if err := st.UpsertRelationship(e[0], e[1], RelUses, "directed", nil); err != nil {
			t.Fatalf("rel: %v", err)
		}
	}

	// Default: B is terminal — C must NOT appear, but B must.
	g, err := st.GraphWalk(a, GraphWalkOpts{MaxDepth: 5, Direction: DirOut})
	if err != nil {
		t.Fatalf("GraphWalk: %v", err)
	}
	ids := map[string]bool{}
	for _, n := range g.Nodes {
		ids[n.Resource.ID] = true
	}
	if !ids[a] || !ids[b] {
		t.Errorf("seed/managed-terminal missing: nodes=%v", ids)
	}
	if ids[c] {
		t.Errorf("C reached past managed B without IncludeManaged: nodes=%v", ids)
	}
	if len(g.Edges) != 1 || g.Edges[0].FromID != a || g.Edges[0].ToID != b {
		t.Errorf("expected single A->B edge, got %+v", g.Edges)
	}

	// IncludeManaged: full walk reaches C.
	g, err = st.GraphWalk(a, GraphWalkOpts{MaxDepth: 5, Direction: DirOut, IncludeManaged: true})
	if err != nil {
		t.Fatalf("GraphWalk include: %v", err)
	}
	ids = map[string]bool{}
	for _, n := range g.Nodes {
		ids[n.Resource.ID] = true
	}
	if !ids[a] || !ids[b] || !ids[c] {
		t.Errorf("IncludeManaged missing nodes: %v", ids)
	}
	if len(g.Edges) != 2 {
		t.Errorf("IncludeManaged edges: got %d, want 2", len(g.Edges))
	}
}

// TestGraphPath_Reachable: shortest path A→D in the diamond is A→B→D or
// A→C→D (BFS picks one deterministically based on iteration order); both
// have length 2.
func TestGraphPath_Reachable(t *testing.T) {
	st := openTestStore(t)
	a, _, _, d := seedDiamond(t, st)

	g, err := st.GraphPath(a, d, GraphPathOpts{Direction: DirOut})
	if err != nil {
		t.Fatalf("GraphPath: %v", err)
	}
	if len(g.Nodes) != 3 || len(g.Edges) != 2 {
		t.Errorf("path len: got %d nodes %d edges, want 3/2", len(g.Nodes), len(g.Edges))
	}
	if g.Nodes[0].Resource.ID != a || g.Nodes[len(g.Nodes)-1].Resource.ID != d {
		t.Errorf("path endpoints: got %s..%s want %s..%s",
			g.Nodes[0].Resource.ID, g.Nodes[len(g.Nodes)-1].Resource.ID, a, d)
	}
}

// TestGraphPath_Unreachable: B and C are not directly connected; an
// out-only walk from B cannot reach C without going back through A.
func TestGraphPath_Unreachable(t *testing.T) {
	st := openTestStore(t)
	_, b, c, _ := seedDiamond(t, st)

	_, err := st.GraphPath(b, c, GraphPathOpts{Direction: DirOut})
	if !errors.Is(err, ErrNoPath) {
		t.Errorf("want ErrNoPath, got %v", err)
	}
}

// TestGraphPath_SameNode: A→A returns just the seed with no edges.
func TestGraphPath_SameNode(t *testing.T) {
	st := openTestStore(t)
	a, _, _, _ := seedDiamond(t, st)

	g, err := st.GraphPath(a, a, GraphPathOpts{Direction: DirOut})
	if err != nil {
		t.Fatalf("GraphPath: %v", err)
	}
	if len(g.Nodes) != 1 || len(g.Edges) != 0 {
		t.Errorf("self-path: got %d/%d, want 1/0", len(g.Nodes), len(g.Edges))
	}
}

// TestGraphWalk_ExcludeTypes drops nodes whose Type matches the suffix-glob
// pattern, transitively pruning their downstream too.
func TestGraphWalk_ExcludeTypes(t *testing.T) {
	st := openTestStore(t)
	mk := func(native, typ string) string {
		r := &Resource{
			Provider: "aws", AccountID: "111", Type: typ,
			NativeID: native, AttributesJSON: "{}", DiscoveredBy: testScanID,
		}
		if _, err := st.UpsertResource(r); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		return r.ID
	}
	a := mk("i-A", "aws:ec2:instance")
	b := mk("k-B", "aws:iam:role") // excluded
	c := mk("i-C", "aws:ec2:instance")
	for _, e := range [][2]string{{a, b}, {b, c}} {
		if err := st.UpsertRelationship(e[0], e[1], RelUses, "directed", nil); err != nil {
			t.Fatalf("rel: %v", err)
		}
	}

	g, err := st.GraphWalk(a, GraphWalkOpts{
		MaxDepth: 5, Direction: DirOut,
		ExcludeTypes: []string{"aws:iam:*"},
	})
	if err != nil {
		t.Fatalf("GraphWalk: %v", err)
	}
	for _, n := range g.Nodes {
		if n.Resource.ID == b {
			t.Errorf("excluded role still present in nodes")
		}
	}
	if len(g.Edges) != 0 {
		t.Errorf("edges should be dropped when endpoint excluded; got %d", len(g.Edges))
	}
	if g.ExcludedTypes != 1 {
		t.Errorf("ExcludedTypes counter: got %d want 1", g.ExcludedTypes)
	}
}

// TestGraphWalk_MaxNodes caps additions and reports drops in TruncatedNodes.
func TestGraphWalk_MaxNodes(t *testing.T) {
	st := openTestStore(t)
	a, _, _, _ := seedDiamond(t, st)

	g, err := st.GraphWalk(a, GraphWalkOpts{MaxDepth: 5, Direction: DirOut, MaxNodes: 2})
	if err != nil {
		t.Fatalf("GraphWalk: %v", err)
	}
	if len(g.Nodes) != 2 {
		t.Errorf("Nodes: got %d, want 2 (capped)", len(g.Nodes))
	}
	if g.TruncatedNodes == 0 {
		t.Errorf("TruncatedNodes: got 0, want >0")
	}
}

// TestResolveResource covers the native-id/hex-id resolution helper.
func TestResolveResource(t *testing.T) {
	st := openTestStore(t)

	// Two resources share native_id "shared" across different types.
	r1 := &Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:instance",
		NativeID: "shared", AttributesJSON: "{}", DiscoveredBy: testScanID,
	}
	r2 := &Resource{
		Provider: "aws", AccountID: "111", Type: "aws:s3:bucket",
		NativeID: "shared", AttributesJSON: "{}", DiscoveredBy: testScanID,
	}
	for _, r := range []*Resource{r1, r2} {
		if _, err := st.UpsertResource(r); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	// 1. Hex ID passthrough.
	got, err := st.ResolveResource(r1.ID, "", "", "")
	if err != nil || got.ID != r1.ID {
		t.Errorf("hex passthrough: got %+v err=%v", got, err)
	}

	// 2. Ambiguous native ID → error lists candidates.
	_, err = st.ResolveResource("shared", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("ambiguous: want error, got %v", err)
	}

	// 3. Disambiguated by --type.
	got, err = st.ResolveResource("shared", "", "aws:s3:bucket", "")
	if err != nil || got.ID != r2.ID {
		t.Errorf("disambiguate: got %+v err=%v", got, err)
	}

	// 4. Unknown native ID → not-found error.
	_, err = st.ResolveResource("does-not-exist", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "no resource") {
		t.Errorf("not-found: want error, got %v", err)
	}

	// 5. Lookup by name (Name field, not native_id).
	name := "prod-db"
	r3 := &Resource{
		Provider: "aws", AccountID: "111", Type: "aws:rds:db-instance",
		NativeID: "db-xyz", Name: &name, AttributesJSON: "{}", DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResource(r3); err != nil {
		t.Fatalf("upsert r3: %v", err)
	}
	got, err = st.ResolveResource("prod-db", "", "", "")
	if err != nil || got.ID != r3.ID {
		t.Errorf("name lookup: got %+v err=%v", got, err)
	}
}
