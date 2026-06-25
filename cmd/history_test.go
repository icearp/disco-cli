package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/store"
)

// seedVersionChain adds a resource with two versions (a limit change) to the
// seeded DB and returns its caller-facing id (root_id). Models the quota
// change-over-time case: same natural key, changed attributes → version split.
func seedVersionChain(t *testing.T, st *store.Store) string {
	t.Helper()
	scanA, err := st.CreateScan([]string{"azure"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan A: %v", err)
	}
	base := &store.Resource{
		Provider: "azure", AccountID: "sub-1", Type: "azure:microsoft.quota:quota",
		NativeID:       "/subscriptions/sub-1/providers/Microsoft.Compute/locations/eastus/providers/Microsoft.Quota/quotas/cores",
		AttributesJSON: `{"limit":100}`, DiscoveredBy: scanA,
	}
	if _, err := st.UpsertResource(base); err != nil {
		t.Fatalf("UpsertResource v1: %v", err)
	}
	scanB, err := st.CreateScan([]string{"azure"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan B: %v", err)
	}
	changed := *base
	changed.AttributesJSON = `{"limit":200}`
	changed.DiscoveredBy = scanB
	if _, err := st.UpsertResource(&changed); err != nil {
		t.Fatalf("UpsertResource v2: %v", err)
	}
	return store.ResourceID(base.Provider, base.AccountID, base.Type, base.NativeID)
}

// attrLimit decodes the "limit" field from a version's attributes (the encoder
// re-indents the embedded raw JSON, so assert the value, not the bytes).
func attrLimit(t *testing.T, raw json.RawMessage) int {
	t.Helper()
	var a struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("decode attributes %s: %v", raw, err)
	}
	return a.Limit
}

// TestHistoryCmd_JSON is the change-over-time contract: history returns every
// version oldest→newest, flags only the newest as current, and carries the
// per-version attributes (the limit at each point).
func TestHistoryCmd_JSON(t *testing.T) {
	st := seedTestDB(t)
	rootID := seedVersionChain(t, st)

	historyOutputFmt = "table"
	out, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"history", rootID, "-o", "json"})
		return rootCmd.Execute()
	})
	if err != nil {
		t.Fatalf("history: %v", err)
	}

	var entries []historyEntry
	if jerr := json.Unmarshal([]byte(out), &entries); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jerr, out)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 versions, got %d: %s", len(entries), out)
	}

	v1, v2 := entries[0], entries[1]
	if v1.Version != 1 || v2.Version != 2 {
		t.Errorf("version order: got %d,%d want 1,2", v1.Version, v2.Version)
	}
	if v1.Current {
		t.Error("oldest version must not be current")
	}
	if !v2.Current {
		t.Error("newest version must be current")
	}
	if got := attrLimit(t, v1.Attributes); got != 100 {
		t.Errorf("v1 limit: got %d, want 100", got)
	}
	if got := attrLimit(t, v2.Attributes); got != 200 {
		t.Errorf("v2 limit: got %d, want 200", got)
	}
	if v2.ID != rootID {
		t.Errorf("id: got %s, want root %s", v2.ID, rootID)
	}
}

// TestHistoryCmd_SingleVersion covers the common case: a resource scanned once
// has exactly one version, flagged current.
func TestHistoryCmd_SingleVersion(t *testing.T) {
	_ = seedTestDB(t)
	// seedTestDB's aws:ec2:instance i-1, never re-scanned with changed attrs.
	rootID := store.ResourceID("aws", "111", "aws:ec2:instance", "i-1")

	historyOutputFmt = "table"
	out, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"history", rootID, "-o", "json"})
		return rootCmd.Execute()
	})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	var entries []historyEntry
	if jerr := json.Unmarshal([]byte(out), &entries); jerr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jerr, out)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 version, got %d", len(entries))
	}
	if entries[0].Version != 1 || !entries[0].Current {
		t.Errorf("single version: got version=%d current=%t, want 1/true", entries[0].Version, entries[0].Current)
	}
}

// TestHistoryCmd_ResolvesByNativeID proves the arg routes through ResolveResource:
// a native_id (not the hashed id) resolves to the same chain.
func TestHistoryCmd_ResolvesByNativeID(t *testing.T) {
	_ = seedTestDB(t)
	historyOutputFmt = "table"
	out, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"history", "i-1", "-o", "json"})
		return rootCmd.Execute()
	})
	if err != nil {
		t.Fatalf("history by native_id: %v", err)
	}
	var entries []historyEntry
	if jerr := json.Unmarshal([]byte(out), &entries); jerr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jerr, out)
	}
	if len(entries) != 1 || entries[0].Type != "aws:ec2:instance" {
		t.Errorf("native_id resolve: got %+v", entries)
	}
}

// TestHistoryCmd_UnknownID returns a clear error for an arg matching no resource.
func TestHistoryCmd_UnknownID(t *testing.T) {
	_ = seedTestDB(t)
	historyOutputFmt = "table"
	_, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"history", "no-such-resource"})
		return rootCmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "no resource matching") {
		t.Errorf("expected no-resource error, got %v", err)
	}
}

// TestHistoryCmd_MissingArg verifies cobra enforces the single positional arg.
func TestHistoryCmd_MissingArg(t *testing.T) {
	historyOutputFmt = "table"
	_, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"history"})
		return rootCmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "accepts 1 arg") {
		t.Errorf("expected arg-count error, got %v", err)
	}
}
