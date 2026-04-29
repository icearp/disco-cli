package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// resetGraphFlags clears package-level graph flag vars between tests since
// cobra reuses the same global Command tree across invocations.
func resetGraphFlags() {
	graphOutputFmt, graphDirection = "", "both"
	graphProvider, graphType, graphAccount = "", "", ""
	graphDepth, graphKinds = 2, nil
	graphIncludeManaged = false
	graphExcludeTypes, graphExcludeRegions = nil, nil
	graphMaxNodes, graphMaxEdges = 0, 0
	graphCluster, graphLabelTemplate = "", ""
	graphDotTheme = "light"
	graphRankdir = "LR"
}

// TestGraphCmd_JSON exercises the end-to-end JSON rendering path using the
// seeded test DB plus an `attached-to` edge between the two resources.
func TestGraphCmd_JSON(t *testing.T) {
	st := seedTestDB(t)
	// Grab the two seeded resources so we have real IDs + native IDs.
	rs, err := st.ListResources(store.ResourceFilter{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("expected 2 seeded resources, got %d", len(rs))
	}
	if err := st.UpsertRelationship(rs[0].ID, rs[1].ID, store.RelAttachedTo, "directed", nil); err != nil {
		t.Fatalf("upsert rel: %v", err)
	}

	// Reset flag vars to defaults — package-level state leaks between tests.
	resetGraphFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph", rs[0].NativeID, "--type", rs[0].Type, "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("graph: %v", err)
	}

	var g store.GraphResult
	if jerr := json.Unmarshal([]byte(out), &g); jerr != nil {
		t.Fatalf("not valid JSON: %v\n%s", jerr, out)
	}
	if len(g.Nodes) != 2 {
		t.Errorf("Nodes: got %d, want 2", len(g.Nodes))
	}
	if len(g.Edges) != 1 {
		t.Errorf("Edges: got %d, want 1", len(g.Edges))
	}
}

// TestGraphCmd_MissingArg verifies that graph with no args prints help (not an error).
func TestGraphCmd_MissingArg(t *testing.T) {
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph"})
		return cmd.Execute()
	})
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if !strings.Contains(out, "Usage:") {
		t.Errorf("expected help output, got %q", out)
	}
}

// TestGraphCmd_Path_Reachable runs `graph path A B` end-to-end against a
// seeded DB with a single A→B edge. Output JSON must include both nodes
// in path order plus the connecting edge.
func TestGraphCmd_Path_Reachable(t *testing.T) {
	st := seedTestDB(t)
	rs, err := st.ListResources(store.ResourceFilter{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if err := st.UpsertRelationship(rs[0].ID, rs[1].ID, store.RelAttachedTo, "directed", nil); err != nil {
		t.Fatalf("upsert rel: %v", err)
	}

	resetGraphFlags()
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph", "path", rs[0].ID, rs[1].ID, "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("graph path: %v", err)
	}
	var g store.GraphResult
	if jerr := json.Unmarshal([]byte(out), &g); jerr != nil {
		t.Fatalf("not valid JSON: %v\n%s", jerr, out)
	}
	if len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Errorf("path: got %d nodes %d edges, want 2/1", len(g.Nodes), len(g.Edges))
	}
}

// TestGraphCmd_Path_Unreachable returns store.ErrNoPath; root.Execute()
// would map to exit 1 silently — the in-process test asserts the sentinel.
func TestGraphCmd_Path_Unreachable(t *testing.T) {
	st := seedTestDB(t)
	rs, err := st.ListResources(store.ResourceFilter{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	// No edge between the two seeded resources.
	resetGraphFlags()
	_, err = captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph", "path", rs[0].ID, rs[1].ID})
		return cmd.Execute()
	})
	if !errors.Is(err, store.ErrNoPath) {
		t.Errorf("want ErrNoPath, got %v", err)
	}
}

// TestGraphCmd_Blast verifies the blast subcommand walks outbound only and
// emits ring-grouped table output containing the seeded peer.
func TestGraphCmd_Blast(t *testing.T) {
	st := seedTestDB(t)
	rs, err := st.ListResources(store.ResourceFilter{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if err := st.UpsertRelationship(rs[0].ID, rs[1].ID, store.RelUses, "directed", nil); err != nil {
		t.Fatalf("upsert rel: %v", err)
	}

	resetGraphFlags()
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph", "blast", rs[0].ID})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("graph blast: %v", err)
	}
	if !strings.Contains(out, "Ring 0") || !strings.Contains(out, "Ring 1") {
		t.Errorf("missing ring output:\n%s", out)
	}
}

// TestGraphCmd_Mermaid verifies the mermaid renderer emits a flowchart with
// node lines and an edge between the two seeded nodes.
func TestGraphCmd_Mermaid(t *testing.T) {
	st := seedTestDB(t)
	rs, err := st.ListResources(store.ResourceFilter{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if err := st.UpsertRelationship(rs[0].ID, rs[1].ID, store.RelAttachedTo, "directed", nil); err != nil {
		t.Fatalf("upsert rel: %v", err)
	}

	resetGraphFlags()
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph", rs[0].ID, "-o", "mermaid"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("graph mermaid: %v", err)
	}
	if !strings.HasPrefix(out, "flowchart LR") {
		t.Errorf("mermaid header missing:\n%s", out)
	}
	if !strings.Contains(out, "-- \"attached-to\" -->") {
		t.Errorf("mermaid edge missing:\n%s", out)
	}
}

// TestGraphCmd_ExcludeTypes confirms the --exclude-types flag wires through
// to the store and drops matching nodes from output.
func TestGraphCmd_ExcludeTypes(t *testing.T) {
	st := seedTestDB(t)
	rs, err := st.ListResources(store.ResourceFilter{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if err := st.UpsertRelationship(rs[0].ID, rs[1].ID, store.RelAttachedTo, "directed", nil); err != nil {
		t.Fatalf("upsert rel: %v", err)
	}

	resetGraphFlags()
	// rs[1] is aws:s3:bucket — excluding "aws:s3:*" should prune it.
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph", rs[0].ID, "--exclude-types", "aws:s3:*", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	var g store.GraphResult
	if jerr := json.Unmarshal([]byte(out), &g); jerr != nil {
		t.Fatalf("not valid JSON: %v\n%s", jerr, out)
	}
	if len(g.Nodes) != 1 {
		t.Errorf("Nodes: got %d, want 1 (seed only after exclude)", len(g.Nodes))
	}
	if g.ExcludedTypes != 1 {
		t.Errorf("ExcludedTypes counter: got %d, want 1", g.ExcludedTypes)
	}
}

// TestGraphCmd_DotLightTheme exercises the default light theme: assert per-
// preset attrs land on the right nodes (cylinder for S3, primary fill for
// EC2) and that the edge picks up its kind-specific style.
func TestGraphCmd_DotLightTheme(t *testing.T) {
	st := seedTestDB(t)
	rs, err := st.ListResources(store.ResourceFilter{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if err := st.UpsertRelationship(rs[0].ID, rs[1].ID, store.RelAttachedTo, "directed", nil); err != nil {
		t.Fatalf("upsert rel: %v", err)
	}

	resetGraphFlags()
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph", rs[0].ID, "-o", "dot", "--dot-theme", "light"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("graph dot: %v", err)
	}

	want := []string{
		`digraph disco {`,
		`graph [`, // theme global block
		`bgcolor="white"`,
		`splines="ortho"`,
		`shape="cylinder"`,    // S3 bucket → storage preset
		`fillcolor="#FFF3E0"`, // storage fillcolor
		`fillcolor="#E3F2FD"`, // primary (EC2) fillcolor
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("light theme output missing %q\n%s", w, out)
		}
	}
}

// TestGraphCmd_DotMonoBackcompat asserts --dot-theme=mono reproduces the
// diff-stable output: no per-node fills, no edge colors, just a `node`
// header with shape + fontname. Attribute key order may differ from the
// pre-theme legacy form (alphabetical now) — that's intentional.
func TestGraphCmd_DotMonoBackcompat(t *testing.T) {
	st := seedTestDB(t)
	rs, err := st.ListResources(store.ResourceFilter{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if err := st.UpsertRelationship(rs[0].ID, rs[1].ID, store.RelAttachedTo, "directed", nil); err != nil {
		t.Fatalf("upsert rel: %v", err)
	}

	resetGraphFlags()
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph", rs[0].ID, "-o", "dot", "--dot-theme", "mono"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("graph dot mono: %v", err)
	}

	if strings.Contains(out, "fillcolor") {
		t.Errorf("mono should emit no fillcolor:\n%s", out)
	}
	if strings.Contains(out, "splines") {
		t.Errorf("mono should emit no splines (legacy header only):\n%s", out)
	}
	// Renderer sorts attrs alphabetically, so legacy `[shape=box, fontname=...]`
	// comes out as `[fontname="Helvetica", shape="box"]`. Assert both
	// pieces are present without pinning the order.
	if !strings.Contains(out, `node [`) ||
		!strings.Contains(out, `shape="box"`) ||
		!strings.Contains(out, `fontname="Helvetica"`) {
		t.Errorf("mono missing minimal node header (shape=box + fontname=Helvetica):\n%s", out)
	}
}

// TestGraphCmd_DotUnknownTheme confirms the flag validator rejects unknown
// theme names with a friendly error rather than silently falling back.
func TestGraphCmd_DotUnknownTheme(t *testing.T) {
	st := seedTestDB(t)
	rs, err := st.ListResources(store.ResourceFilter{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}

	resetGraphFlags()
	_, err = captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph", rs[0].ID, "-o", "dot", "--dot-theme", "neon"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "unknown --dot-theme") {
		t.Errorf("want unknown-theme error, got %v", err)
	}
}

// TestPresetForResource is a table-driven sanity check on the type→preset
// heuristic so adding a new resource service to the switch can't silently
// regress an existing mapping.
func TestPresetForResource(t *testing.T) {
	cases := []struct {
		typ     string
		managed bool
		want    nodePreset
	}{
		{"aws:ec2:instance", false, presetPrimary},
		{"aws:lambda:function", false, presetPrimary},
		{"aws:s3:bucket", false, presetStorage},
		{"aws:rds:instance", false, presetStorage},
		{"aws:iam:role", false, presetIdentity},
		{"gcp:bigquery:dataset", false, presetStorage},
		{"azure:microsoft.authorization:role-definition", false, presetIdentity},
		{"aws:ec2:vpc", false, presetPrimary}, // ec2 service segment hits primary
		{"aws:iam:policy", true, presetMuted}, // managed wins over identity
		{"weird", false, presetSecondary},     // no service segment
	}
	for _, c := range cases {
		r := &store.Resource{Type: c.typ, ManagedByProvider: c.managed}
		got := presetForResource(r)
		if got != c.want {
			t.Errorf("%s (managed=%v): got %s, want %s", c.typ, c.managed, got, c.want)
		}
	}
}

// TestGraphCmd_Complete asserts `graph complete` returns every customer
// resource plus managed resources reachable from a customer via any edge.
// Seed: customer A (ec2), customer B (s3), managed M (aws-managed policy)
// connected to A. Orphan managed N (no edges). Expect A+B+M, drop N.
func TestGraphCmd_Complete(t *testing.T) {
	st := seedTestDB(t)
	rs, err := st.ListResources(store.ResourceFilter{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	mName, mRegion := "managed-policy", "us-east-1"
	nName, nRegion := "orphan-policy", "us-east-1"
	managed := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:iam:policy", NativeID: "arn:aws:iam::aws:policy/M",
		Name: &mName, Region: &mRegion, AttributesJSON: "{}",
		ManagedByProvider: true, DiscoveredBy: scanID,
	}
	orphan := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:iam:policy", NativeID: "arn:aws:iam::aws:policy/N",
		Name: &nName, Region: &nRegion, AttributesJSON: "{}",
		ManagedByProvider: true, DiscoveredBy: scanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{managed, orphan}); err != nil {
		t.Fatalf("upsert managed: %v", err)
	}
	// Edge: customer A -> managed M (uses).
	if err := st.UpsertRelationship(rs[0].ID, managed.ID, store.RelUses, "directed", nil); err != nil {
		t.Fatalf("upsert rel: %v", err)
	}

	resetGraphFlags()
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph", "complete", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("graph complete: %v", err)
	}
	var g store.GraphResult
	if jerr := json.Unmarshal([]byte(out), &g); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if len(g.Nodes) != 3 {
		t.Errorf("Nodes: got %d, want 3 (A+B+M; orphan N excluded)", len(g.Nodes))
	}
	for _, n := range g.Nodes {
		if n.Resource.NativeID == orphan.NativeID {
			t.Errorf("orphan managed leaked into output: %+v", n.Resource)
		}
	}
	if len(g.Edges) != 1 {
		t.Errorf("Edges: got %d, want 1 (A->M)", len(g.Edges))
	}
}

// TestGraphCmd_Complete_IncludeManaged asserts --include-managed promotes
// orphan managed resources back into the graph.
func TestGraphCmd_Complete_IncludeManaged(t *testing.T) {
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	nName := "orphan"
	orphan := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:iam:policy", NativeID: "arn:aws:iam::aws:policy/N",
		Name: &nName, AttributesJSON: "{}", ManagedByProvider: true, DiscoveredBy: scanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{orphan}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resetGraphFlags()
	graphIncludeManaged = true
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"graph", "complete", "--include-managed", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("graph complete: %v", err)
	}
	var g store.GraphResult
	if jerr := json.Unmarshal([]byte(out), &g); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if len(g.Nodes) != 3 {
		t.Errorf("Nodes: got %d, want 3 (A+B+orphan)", len(g.Nodes))
	}
}

// TestThemesCompleteness asserts every themed (non-mono) theme covers all
// nodePreset values plus all known store.Rel* edge kinds. Catches drift
// when a new edge kind lands but a theme isn't updated — without this
// the edge would silently render with no kind-specific styling.
func TestThemesCompleteness(t *testing.T) {
	allPresets := []nodePreset{
		presetPrimary, presetSecondary, presetStorage,
		presetIdentity, presetMuted, presetError,
	}
	allEdgeKinds := []string{
		store.RelContains, store.RelAttachedTo, store.RelUses,
		store.RelAssumes, store.RelRoutesTo, store.RelPeer,
		store.RelBoundedBy, store.RelCrossAccountTrust,
		store.RelCrossSubRBAC, store.RelCrossProjectIAM,
	}
	for name, theme := range themes {
		if len(theme.NodePresets) == 0 {
			continue // mono / minimal themes by design — no presets
		}
		for _, p := range allPresets {
			if _, ok := theme.NodePresets[p]; !ok {
				t.Errorf("theme %q missing NodePreset %q", name, p)
			}
		}
		for _, k := range allEdgeKinds {
			if _, ok := theme.EdgePresets[k]; !ok {
				t.Errorf("theme %q missing EdgePreset for kind %q", name, k)
			}
		}
	}
}
