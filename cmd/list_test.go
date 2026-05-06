package cmd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
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
// was written. Drains the pipe concurrently — without that, output larger
// than the kernel pipe buffer (~64KB) deadlocks the writer.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	return capturePipe(t, &os.Stdout, fn)
}

// captureStderr mirrors captureStdout for os.Stderr — used to assert on
// telemetry/warning lines (e.g. list's truncation warning) that must not
// pollute -o json|jsonl|sarif stdout pipelines.
func captureStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	return capturePipe(t, &os.Stderr, fn)
}

// capturePipe swaps *target with a pipe, drains it in a goroutine for the
// duration of fn, then restores. The concurrent drain avoids a deadlock
// when fn writes more than the pipe buffer's worth of data.
func capturePipe(t *testing.T, target **os.File, fn func() error) (string, error) {
	t.Helper()
	orig := *target
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	*target = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	runErr := fn()
	_ = w.Close()
	<-done
	*target = orig
	_ = r.Close()
	return buf.String(), runErr
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

// TestListCmd_JSON_SnakeCase guards the F3 unification: keys must be
// snake_case and attributes/tags must be nested objects on the wire, not
// stringified JSON blobs as in the legacy PascalCase shape.
func TestListCmd_JSON_SnakeCase(t *testing.T) {
	st := seedTestDB(t)
	resetListFlags()
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	tags := `{"env":"prod"}`
	name := "vol"
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:volume",
		NativeID: "vol-x", Name: &name,
		AttributesJSON: `{"Encrypted":false}`, TagsJSON: &tags,
		DiscoveredBy: scanID,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--output", "json", "--type", "aws:ec2:volume"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("list json: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if _, ok := r["NativeID"]; ok {
		t.Errorf("PascalCase NativeID leaked: %v", r)
	}
	if r["native_id"] != "vol-x" {
		t.Errorf("native_id: got %v", r["native_id"])
	}
	attrs, ok := r["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("attributes not nested object: %T %v", r["attributes"], r["attributes"])
	}
	if attrs["Encrypted"] != false {
		t.Errorf("attrs.Encrypted: got %v", attrs["Encrypted"])
	}
	tagsOut, ok := r["tags"].(map[string]any)
	if !ok || tagsOut["env"] != "prod" {
		t.Errorf("tags not nested: %v", r["tags"])
	}
}

// TestListCmd_CSV verifies that --output csv emits the header row plus one
// row per resource with the canonical column order.
func TestListCmd_CSV(t *testing.T) {
	seedTestDB(t)
	resetListFlags()

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
	resetListFlags()

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

// resetListFlags returns package-level list flag vars to defaults — cobra
// leaks them across tests because rootCmd is shared.
func resetListFlags() {
	listProvider = ""
	listType = ""
	listRegion = ""
	listStatus = ""
	listTagKey = ""
	listTagValue = ""
	listOutputFmt = ""
	listLimit = 0
	listIncludeManaged = false
}

// TestListCmd_DefaultReturnsAll guards against silent 500-row truncation.
// Seeds 600+ rows (well above the historical default cap) and asserts the
// no-flag invocation returns every row.
func TestListCmd_DefaultReturnsAll(t *testing.T) {
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	rows := make([]*store.Resource, 0, 600)
	for i := 0; i < 600; i++ {
		nid := fmt.Sprintf("vol-%04d", i)
		rows = append(rows, &store.Resource{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:volume",
			NativeID: nid, AttributesJSON: "{}", DiscoveredBy: scanID,
		})
	}
	if _, err := st.UpsertResources(rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resetListFlags()
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--output", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var decoded []store.Resource
	if jerr := json.Unmarshal([]byte(out), &decoded); jerr != nil {
		t.Fatalf("not JSON: %v", jerr)
	}
	if len(decoded) != 602 { // 600 volumes + 2 from seedTestDB
		t.Errorf("want 602 rows (full population), got %d", len(decoded))
	}
}

// TestListCmd_LimitWarnsOnTruncation asserts a positive --limit that hits
// the cap emits a stderr warning and clean stdout.
func TestListCmd_LimitWarnsOnTruncation(t *testing.T) {
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	rows := make([]*store.Resource, 0, 600)
	for i := 0; i < 600; i++ {
		nid := fmt.Sprintf("vol-%04d", i)
		rows = append(rows, &store.Resource{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:volume",
			NativeID: nid, AttributesJSON: "{}", DiscoveredBy: scanID,
		})
	}
	if _, err := st.UpsertResources(rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resetListFlags()
	var stdoutCap string
	stderrCap, err := captureStderr(t, func() error {
		var inner error
		stdoutCap, inner = captureStdout(t, func() error {
			cmd := rootCmd
			cmd.SetArgs([]string{"list", "--output", "json", "--limit", "100"})
			return cmd.Execute()
		})
		return inner
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(stderrCap, "warning: --limit 100") {
		t.Errorf("want truncation warning on stderr, got %q", stderrCap)
	}
	var decoded []store.Resource
	if jerr := json.Unmarshal([]byte(stdoutCap), &decoded); jerr != nil {
		t.Fatalf("stdout not JSON (warning leaked?): %v\n%s", jerr, stdoutCap)
	}
	if len(decoded) != 100 {
		t.Errorf("want 100 rows, got %d", len(decoded))
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
