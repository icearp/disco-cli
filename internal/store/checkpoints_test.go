package store

import (
	"path/filepath"
	"testing"
)

func TestSaveAndGetCheckpoint(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ck.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close() }()

	const sid = "scan-1"
	if err := st.SaveCheckpoint(sid, "aws", "aws:ec2", "111111111111/us-east-1", "tok-A"); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := st.GetCheckpoint(sid, "aws", "aws:ec2", "111111111111/us-east-1")
	if err != nil || !ok || got != "tok-A" {
		t.Fatalf("get: got=%q ok=%v err=%v", got, ok, err)
	}

	// Update on conflict — last_token should advance.
	if err := st.SaveCheckpoint(sid, "aws", "aws:ec2", "111111111111/us-east-1", "tok-B"); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	got, _, _ = st.GetCheckpoint(sid, "aws", "aws:ec2", "111111111111/us-east-1")
	if got != "tok-B" {
		t.Errorf("expected tok-B after upsert, got %q", got)
	}

	// Empty token (page returned no continuation) persists.
	if err := st.SaveCheckpoint(sid, "aws", "aws:ec2", "222222222222/us-east-1", ""); err != nil {
		t.Fatalf("save empty: %v", err)
	}
	got, ok, err = st.GetCheckpoint(sid, "aws", "aws:ec2", "222222222222/us-east-1")
	if err != nil || !ok {
		t.Fatalf("get empty: ok=%v err=%v", ok, err)
	}
	if got != "" {
		t.Errorf("expected empty string for null token, got %q", got)
	}

	// Missing row returns ok=false.
	_, ok, _ = st.GetCheckpoint(sid, "aws", "aws:ec2", "nonexistent")
	if ok {
		t.Error("expected ok=false for missing checkpoint")
	}
}

func TestListCheckpointsAndDelete(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ck.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close() }()

	const sid = "scan-X"
	for _, tup := range []struct{ svc, scope, tok string }{
		{"aws:ec2", "111/us-east-1", "t1"},
		{"aws:ec2", "111/us-west-2", "t2"},
		{"aws:s3", "111/global", "t3"},
	} {
		if err := st.SaveCheckpoint(sid, "aws", tup.svc, tup.scope, tup.tok); err != nil {
			t.Fatalf("save %s/%s: %v", tup.svc, tup.scope, err)
		}
	}
	cps, err := st.ListCheckpoints(sid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(cps) != 3 {
		t.Fatalf("expected 3 checkpoints, got %d", len(cps))
	}
	// Ordered by service then scope.
	if cps[0].Service != "aws:ec2" || cps[0].Scope != "111/us-east-1" {
		t.Errorf("ordering broken: first = %+v", cps[0])
	}
	if cps[2].Service != "aws:s3" {
		t.Errorf("ordering broken: last = %+v", cps[2])
	}

	if err := st.DeleteScanCheckpoints(sid); err != nil {
		t.Fatalf("delete: %v", err)
	}
	cps, _ = st.ListCheckpoints(sid)
	if len(cps) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(cps))
	}
}
