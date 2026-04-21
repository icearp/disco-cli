package cmd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/spf13/viper"
)

// seedTestDB opens a temp SQLite DB, inserts a scan + two resources, and
// points viper at it so cobra commands pick it up via defaultDBPath().
// Returns the store so the caller can seed more rows if needed.
func seedTestDB(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "disco.db")
	viper.Set("db", dbPath)
	t.Cleanup(func() { viper.Set("db", "") })

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}

	name1, region1 := "web", "us-east-1"
	name2, region2 := "db", "us-west-2"
	resources := []*store.Resource{
		{Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "i-1",
			Name: &name1, Region: &region1, AttributesJSON: "{}", DiscoveredBy: scanID},
		{Provider: "aws", AccountID: "111", Type: "aws:s3:bucket", NativeID: "b-1",
			Name: &name2, Region: &region2, AttributesJSON: "{}", DiscoveredBy: scanID},
	}
	if _, err := st.UpsertResources(resources); err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}
	return st
}

// captureStdout replaces os.Stdout for the duration of fn and returns what
// was written. Needed because the list/diff commands write directly to
// os.Stdout rather than cmd.OutOrStdout — a limitation the tests expose.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = orig
	b, _ := io.ReadAll(r)
	return string(b), runErr
}

// TestListCmd_JSON verifies that --output json emits a valid JSON array of
// all seeded resources.
func TestListCmd_JSON(t *testing.T) {
	seedTestDB(t)

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--output", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("list --output json: %v", err)
	}

	var decoded []store.Resource
	if jerr := json.Unmarshal([]byte(out), &decoded); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jerr, out)
	}
	if len(decoded) != 2 {
		t.Errorf("expected 2 resources, got %d", len(decoded))
	}
}

// TestListCmd_CSV verifies that --output csv emits the header row plus one
// row per resource with the canonical column order.
func TestListCmd_CSV(t *testing.T) {
	seedTestDB(t)
	// Reset the filter vars that earlier tests may have mutated.
	listOutputFmt, listProvider = "", ""

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--output", "csv"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("list --output csv: %v", err)
	}

	r := csv.NewReader(bytes.NewReader([]byte(out)))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v\n%s", err, out)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (header + 2 resources), got %d", len(rows))
	}
	if rows[0][0] != "provider" {
		t.Errorf("header row[0]: got %q, want provider", rows[0][0])
	}
}

// TestListCmd_JSONL verifies that --output jsonl emits one JSON object per
// newline-terminated line.
func TestListCmd_JSONL(t *testing.T) {
	seedTestDB(t)
	listOutputFmt = ""

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--output", "jsonl"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("list --output jsonl: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d:\n%s", len(lines), out)
	}
	for i, line := range lines {
		var r store.Resource
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Errorf("line %d not valid JSON: %v", i, err)
		}
	}
}

// TestListCmd_UnknownFormat verifies the unknown --output format error path.
func TestListCmd_UnknownFormat(t *testing.T) {
	seedTestDB(t)
	listOutputFmt = ""

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--output", "xml"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "unknown --output format") {
		t.Errorf("expected unknown format error, got %v", err)
	}
}
