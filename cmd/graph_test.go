package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

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
	graphOutputFmt, graphDirection, graphProvider, graphType, graphAccount = "", "both", "", "", ""
	graphDepth, graphKinds = 2, nil

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
