package store

import (
	"strings"
	"testing"
)

// TestCreateScanWithID_HonoursExplicitID ensures the SaaS-supplied
// DISCO_SCAN_ID (forwarded as the idHint argument) lands on disk as the
// scan's primary key. Critical: chain-of-custody from audit_log → scans →
// resources depends on this single identifier.
func TestCreateScanWithID_HonoursExplicitID(t *testing.T) {
	st := openTestStore(t)
	want := "abcdef0123456789-saas-uuid"
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
