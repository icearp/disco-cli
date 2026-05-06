package cmd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
	header := rows[0]
	if header[0] != "provider" {
		t.Errorf("header row[0]: got %q, want provider", header[0])
	}
	if len(header) != 18 {
		t.Errorf("header width: got %d, want 18", len(header))
	}
	headerIdx := map[string]int{}
	for i, h := range header {
		headerIdx[h] = i
	}
	for _, must := range []string{"id", "account_name", "zone", "managed_by_provider", "tags", "attributes", "discovered_at", "discovered_by", "verified_at", "verified_by"} {
		if _, ok := headerIdx[must]; !ok {
			t.Errorf("custody column missing: %s", must)
		}
	}
	dataRow := rows[1]
	idCell := dataRow[headerIdx["id"]]
	if matched, _ := regexp.MatchString(`^[0-9a-f]{32}$`, idCell); !matched {
		t.Errorf("id cell not 32-hex: %q", idCell)
	}
	if dataRow[headerIdx["discovered_by"]] == "" {
		t.Errorf("discovered_by empty")
	}
	if dataRow[headerIdx["verified_at"]] == "" {
		t.Errorf("verified_at empty")
	}
	mbp := dataRow[headerIdx["managed_by_provider"]]
	if mbp != "true" && mbp != "false" {
		t.Errorf("managed_by_provider: got %q, want true|false", mbp)
	}
}

// TestListCmd_CSV_TagsAttrsRoundTrip seeds a row with tags + attrs and asserts
// the CSV blob cells parse back via json.Unmarshal — F7 fidelity guard.
func TestListCmd_CSV_TagsAttrsRoundTrip(t *testing.T) {
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	tags := `{"env":"prod","team":"core"}`
	attrs := `{"Encrypted":false,"Size":8}`
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:volume",
		NativeID: "vol-rt", AttributesJSON: attrs, TagsJSON: &tags,
		DiscoveredBy: scanID,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	resetListFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--output", "csv", "--type", "aws:ec2:volume"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("list csv: %v", err)
	}
	r := csv.NewReader(bytes.NewReader([]byte(out)))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("want header + 1 row, got %d", len(rows))
	}
	header := rows[0]
	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}
	var gotTags map[string]string
	if err := json.Unmarshal([]byte(rows[1][idx["tags"]]), &gotTags); err != nil {
		t.Fatalf("tags cell not JSON: %v\n%s", err, rows[1][idx["tags"]])
	}
	if gotTags["env"] != "prod" || gotTags["team"] != "core" {
		t.Errorf("tags drift: %v", gotTags)
	}
	var gotAttrs map[string]any
	if err := json.Unmarshal([]byte(rows[1][idx["attributes"]]), &gotAttrs); err != nil {
		t.Fatalf("attributes cell not JSON: %v", err)
	}
	if gotAttrs["Encrypted"] != false {
		t.Errorf("attrs.Encrypted drift: %v", gotAttrs)
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
	listExcludeTypes = nil
	listRegion = ""
	listStatus = ""
	listTagKey = ""
	listTagValue = ""
	listScanID = ""
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

// TestScan_RejectedUnderReadOnly guards F20: scan refuses to run when the
// global --db-readonly flag is set, before opening anything.
func TestScan_RejectedUnderReadOnly(t *testing.T) {
	dbReadOnly = true
	t.Cleanup(func() { dbReadOnly = false })

	err := runScan(scanCmd, nil)
	if err == nil {
		t.Fatalf("want error from runScan under --db-readonly")
	}
	if !strings.Contains(err.Error(), "--db-readonly") {
		t.Errorf("want db-readonly message, got: %v", err)
	}
}

// TestListCmd_DBReadOnly guards F20: --db-readonly opens RO and reads OK.
func TestListCmd_DBReadOnly(t *testing.T) {
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:volume",
		NativeID: "vol-ro", AttributesJSON: "{}", DiscoveredBy: scanID,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	_ = st.Close()

	resetListFlags()
	dbReadOnly = true
	t.Cleanup(func() { dbReadOnly = false })

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--output", "json", "--type", "aws:ec2:volume"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("list under --db-readonly: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Errorf("want 1 row, got %d", len(rows))
	}
}

// TestRoot_BannerSuppressedByDefault guards F18: the "Using config file:"
// banner must not contaminate stderr on default invocations.
func TestRoot_BannerSuppressedByDefault(t *testing.T) {
	seedTestDB(t)
	resetListFlags()
	verbose = false

	stderrCap, err := captureStderr(t, func() error {
		_, inner := captureStdout(t, func() error {
			cmd := rootCmd
			cmd.SetArgs([]string{"list", "-o", "json"})
			return cmd.Execute()
		})
		return inner
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(stderrCap, "Using config file") {
		t.Errorf("banner leaked on default invocation: %q", stderrCap)
	}
}

// TestListCmd_LimitNoWarnAtExactBoundary guards against the F2 boundary
// false positive: when population fits exactly within --limit, no warning.
func TestListCmd_LimitNoWarnAtExactBoundary(t *testing.T) {
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	rows := make([]*store.Resource, 0, 50)
	for i := 0; i < 50; i++ {
		nid := fmt.Sprintf("vol-bnd-%04d", i)
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
			cmd.SetArgs([]string{"list", "--output", "json", "--type", "aws:ec2:volume", "--limit", "50"})
			return cmd.Execute()
		})
		return inner
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(stderrCap, "warning: --limit") {
		t.Errorf("spurious truncation warning at exact boundary: %q", stderrCap)
	}
	var decoded []store.Resource
	if jerr := json.Unmarshal([]byte(stdoutCap), &decoded); jerr != nil {
		t.Fatalf("not JSON: %v", jerr)
	}
	if len(decoded) != 50 {
		t.Errorf("want 50 rows, got %d", len(decoded))
	}
}

// TestListCmd_ExcludeTypes round-trips --exclude-types through the SQL
// filter; rows of named types must be absent from JSON output.
func TestListCmd_ExcludeTypes(t *testing.T) {
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	noisy := []*store.Resource{
		{Provider: "aws", AccountID: "111", Type: "aws:logs:log-stream", NativeID: "ls-1",
			AttributesJSON: "{}", DiscoveredBy: scanID},
		{Provider: "aws", AccountID: "111", Type: "aws:logs:log-stream", NativeID: "ls-2",
			AttributesJSON: "{}", DiscoveredBy: scanID},
	}
	if _, err := st.UpsertResources(noisy); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	resetListFlags()
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--exclude-types", "aws:logs:log-stream", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var decoded []store.Resource
	if jerr := json.Unmarshal([]byte(out), &decoded); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	for _, r := range decoded {
		if r.Type == "aws:logs:log-stream" {
			t.Errorf("excluded type leaked: %+v", r)
		}
	}
	// seedTestDB plants 2 customer rows (instance + bucket); both should remain.
	if len(decoded) != 2 {
		t.Errorf("want 2 rows after exclude, got %d", len(decoded))
	}
}

// TestListCmd_ScanID seeds two distinct scan runs with rows under each, then
// asserts --scan-id returns only that run's rows. Also exercises the
// 'latest' shorthand and the unknown-id error path.
func TestListCmd_ScanID(t *testing.T) {
	st := seedTestDB(t) // baseline scan + 2 rows
	scanB, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	if _, err := st.UpsertResources([]*store.Resource{
		{Provider: "aws", AccountID: "111", Type: "aws:ec2:vpc", NativeID: "vpc-B",
			AttributesJSON: "{}", DiscoveredBy: scanB},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// scanB is the most-recent scan; --scan-id latest should resolve to it
	// and return only the one row inserted under it.
	resetListFlags()
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--scan-id", "latest", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("list latest: %v", err)
	}
	var rows []store.Resource
	if jerr := json.Unmarshal([]byte(out), &rows); jerr != nil {
		t.Fatalf("not JSON: %v\n%s", jerr, out)
	}
	if len(rows) != 1 || rows[0].NativeID != "vpc-B" {
		t.Errorf("--scan-id latest: got %d rows %+v, want 1 vpc-B", len(rows), rows)
	}

	// Explicit literal scan ID round-trip.
	resetListFlags()
	out, err = captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--scan-id", scanB, "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("list literal: %v", err)
	}
	rows = nil
	if jerr := json.Unmarshal([]byte(out), &rows); jerr != nil {
		t.Fatalf("not JSON: %v", jerr)
	}
	if len(rows) != 1 {
		t.Errorf("literal id: got %d rows, want 1", len(rows))
	}

	// Unknown id rejects.
	resetListFlags()
	_, err = captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--scan-id", "deadbeef"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown id: want 'not found', got %v", err)
	}
}

// TestListCmd_UnknownFormat verifies the unknown --output format error path.
func TestListCmd_UnknownFormat(t *testing.T) {
	seedTestDB(t)
	resetListFlags()

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"list", "--output", "xml"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "unknown --output format") {
		t.Errorf("expected unknown format error, got %v", err)
	}
}
