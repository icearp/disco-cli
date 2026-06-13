package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/spf13/viper"
)

// TestDiffCmd_JSON seeds two scans with one overlapping resource and one
// unique resource per scan, then verifies the diff command's JSON output
// reports the expected added/stale counts.
func TestDiffCmd_JSON(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "disco.db")
	viper.Set("db", dbPath)
	t.Cleanup(func() { viper.Set("db", "") })

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	scanA, _ := st.CreateScan([]string{"aws"}, map[string]any{})
	scanB, _ := st.CreateScan([]string{"aws"}, map[string]any{})

	// Resource unique to scan A → stale after B.
	_, _ = st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "i-gone",
		AttributesJSON: "{}", DiscoveredBy: scanA,
	})
	// Resource unique to scan B → added.
	_, _ = st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "i-new",
		AttributesJSON: "{}", DiscoveredBy: scanB,
	})

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"diff", scanA, scanB, "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("diff: %v", err)
	}

	var d store.ScanDiff
	if jerr := json.Unmarshal([]byte(out), &d); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jerr, out)
	}
	if len(d.Added) != 1 || d.Added[0].NativeID != "i-new" {
		t.Errorf("Added: want [i-new], got %+v", d.Added)
	}
	if len(d.Stale) != 1 || d.Stale[0].NativeID != "i-gone" {
		t.Errorf("Stale: want [i-gone], got %+v", d.Stale)
	}
}

// TestDiffCmd_MissingArg verifies cobra surfaces an arg-count error.
func TestDiffCmd_MissingArg(t *testing.T) {
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"diff", "only-one-id"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "accepts 2 arg") {
		t.Errorf("expected arg-count error, got %v", err)
	}
}
