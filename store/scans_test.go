package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// TestCreateScanWithID_HonoursExplicitID ensures an externally-supplied
// DISCO_SCAN_ID (forwarded as the idHint argument) lands on disk as the
// scan's primary key. Critical: chain-of-custody from an audit trail → scans →
// resources depends on this single identifier.
func TestCreateScanWithID_HonoursExplicitID(t *testing.T) {
	st := openTestStore(t)
	want := "abcdef0123456789-explicit-id"
	got, err := st.CreateScanWithID(want, []string{"aws"}, map[string]any{"providers": []string{"aws"}})
	if err != nil {
		t.Fatalf("CreateScanWithID: %v", err)
	}
	if got != want {
		t.Fatalf("returned id = %q, want %q", got, want)
	}
	sc, err := st.GetScan(want)
	if err != nil {
		t.Fatalf("GetScan(%q): %v", want, err)
	}
	if sc.ID != want {
		t.Fatalf("persisted id = %q, want %q", sc.ID, want)
	}
}

// TestAppendScanError_RoundTrip writes two structured entries then
// asserts both land in scans.errors as a JSON array. SQLite path.
func TestAppendScanError_RoundTrip(t *testing.T) {
	st := openTestStore(t)
	id, err := st.CreateScanWithID("err-scan-1", []string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScanWithID: %v", err)
	}
	if err := st.AppendScanError(id, ScanErrorEntry{
		Service: "aws:ec2", Region: "us-east-1", Code: "AccessDenied",
		Message: "User: arn:… is not authorized",
	}); err != nil {
		t.Fatalf("AppendScanError 1: %v", err)
	}
	if err := st.AppendScanError(id, ScanErrorEntry{
		Service: "aws:s3", Region: "us-east-2", Code: "Throttling",
		Message: "Rate exceeded",
	}); err != nil {
		t.Fatalf("AppendScanError 2: %v", err)
	}
	var raw string
	if err := st.get(&raw, `SELECT COALESCE(errors, '[]') FROM scans WHERE id = ?`, id); err != nil {
		t.Fatalf("read errors: %v", err)
	}
	if !strings.Contains(raw, "AccessDenied") || !strings.Contains(raw, "Throttling") {
		t.Errorf("errors payload missing entries: %s", raw)
	}
}

// TestAppendScanWarning_RoundTrip mirrors the error round-trip on the warnings
// column, and additionally reads through GetScan: the read projection must
// carry `warnings`, or every SELECT in the package fails at scan time with
// "missing destination name warnings" rather than at compile time.
func TestAppendScanWarning_RoundTrip(t *testing.T) {
	st := openTestStore(t)
	id, err := st.CreateScanWithID("warn-scan-1", []string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScanWithID: %v", err)
	}

	// A fresh row must read as an empty array, never NULL — readers branch on
	// neither.
	sc, err := st.GetScan(id)
	if err != nil {
		t.Fatalf("GetScan before append: %v", err)
	}
	if sc.WarningsJSON == nil || *sc.WarningsJSON != "[]" {
		t.Fatalf("fresh warnings = %v, want the '[]' column default", sc.WarningsJSON)
	}

	if err := st.AppendScanWarning(id, ScanWarningEntry{
		Service: "aws:bedrockagentcore", Region: "us-west-1",
		Scope: "228886154857/us-west-1", Message: "AccessDeniedException",
	}); err != nil {
		t.Fatalf("AppendScanWarning 1: %v", err)
	}
	if err := st.AppendScanWarning(id, ScanWarningEntry{
		Service: "aws:s3control", Region: "ap-northeast-3",
		Scope: "228886154857/ap-northeast-3", Message: "Region is not supported as home region",
	}); err != nil {
		t.Fatalf("AppendScanWarning 2: %v", err)
	}

	sc, err = st.GetScan(id)
	if err != nil {
		t.Fatalf("GetScan after append: %v", err)
	}
	if sc.WarningsJSON == nil {
		t.Fatal("warnings read back NULL after two appends")
	}
	if !strings.Contains(*sc.WarningsJSON, "bedrockagentcore") ||
		!strings.Contains(*sc.WarningsJSON, "s3control") {
		t.Errorf("warnings payload missing entries: %s", *sc.WarningsJSON)
	}
	// Appending a warning must not disturb the errors column: they are separate
	// severities, not one list with a flag.
	if sc.ErrorsJSON == nil || *sc.ErrorsJSON != "[]" {
		t.Errorf("errors = %v, want it untouched at '[]'", sc.ErrorsJSON)
	}
}

// TestCreateScanWithID_EmptyMints exercises the legacy fallback path.
func TestCreateScanWithID_EmptyMints(t *testing.T) {
	st := openTestStore(t)
	got, err := st.CreateScanWithID("", []string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScanWithID: %v", err)
	}
	if len(got) != 32 {
		t.Fatalf("minted id length = %d, want 32-hex", len(got))
	}
}

func TestTypesForScan(t *testing.T) {
	st := openTestStore(t)
	testTypesForScan(t, st)
}

// TestTypesForScan_PG guards dialect parity: the query is plain SQL under
// Rebind, so Postgres must return the same pairs in the same order.
func TestTypesForScan_PG(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	t.Cleanup(purge)
	st, err := OpenPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	testTypesForScan(t, st)
}

func testTypesForScan(t *testing.T, st *Store) {
	t.Helper()
	first, err := st.CreateScanWithID("types-scan-1", []string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScanWithID: %v", err)
	}
	rows := []*Resource{
		{Provider: "aws", AccountID: "1", Type: "aws:ec2:instance", NativeID: "i-1", AttributesJSON: "{}", DiscoveredBy: first},
		{Provider: "aws", AccountID: "1", Type: "aws:ec2:volume", NativeID: "vol-1", AttributesJSON: "{}", DiscoveredBy: first},
		{Provider: "aws", AccountID: "1", Type: "aws:ec2:volume", NativeID: "vol-2", AttributesJSON: "{}", DiscoveredBy: first},
	}
	if _, err := st.UpsertResources(rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.CompleteScan(first); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// A second scan re-verifies the unchanged instance (verified_by moves,
	// discovered_by stays) and adds a bucket.
	second, err := st.CreateScanWithID("types-scan-2", []string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScanWithID: %v", err)
	}
	if _, err := st.UpsertResources([]*Resource{
		{Provider: "aws", AccountID: "1", Type: "aws:ec2:instance", NativeID: "i-1", AttributesJSON: "{}", DiscoveredBy: second},
		{Provider: "aws", AccountID: "1", Type: "aws:s3:bucket", NativeID: "b", AttributesJSON: "{}", DiscoveredBy: second},
	}); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	got, err := st.TypesForScan(second)
	if err != nil {
		t.Fatalf("TypesForScan: %v", err)
	}
	want := []ProviderType{{"aws", "aws:ec2:instance"}, {"aws", "aws:s3:bucket"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("second scan types = %v, want %v", got, want)
	}
	got, err = st.TypesForScan(first)
	if err != nil {
		t.Fatalf("TypesForScan: %v", err)
	}
	if len(got) != 2 || got[0].Type != "aws:ec2:instance" || got[1].Type != "aws:ec2:volume" {
		t.Errorf("first scan types = %v", got)
	}
	if got, _ := st.TypesForScan("nope"); len(got) != 0 {
		t.Errorf("unknown scan types = %v", got)
	}
}
