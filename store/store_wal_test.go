package store

import (
	"bytes"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// walSidecarsGone asserts the -wal and -shm sidecars for path do not exist.
func walSidecarsGone(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Errorf("sidecar %s%s still present (stat err=%v); want removed", path, suffix, err)
		}
	}
}

// TestClose_RemovesWALSidecars is the clean-exit contract: after a writable
// store closes, the -wal/-shm sidecars are gone and the WAL tail has been
// folded into the main file (a fresh read-only open sees the row).
//
// Note: in this clean in-process path SQLite's own last-connection auto-delete
// also removes the sidecars, so this guards the end-to-end lifecycle contract
// rather than isolating Close()'s explicit checkpoint+DELETE. The explicit
// cleanup is the backstop for the intermittent case where auto-delete's
// exclusive-lock acquisition is skipped (mmap / lock timing) — that condition
// is platform-dependent and not deterministically reproducible in-process; it
// is covered by the manual Ctrl-C / sidecar verification in the plan.
func TestClose_RemovesWALSidecars(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("insert scan: %v", err)
	}
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "111", Type: "aws:s3:bucket",
		NativeID: "b-1", AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	walSidecarsGone(t, path)

	// Data must be self-contained in the main file, not stranded in a discarded
	// WAL — a read-only reopen (no WAL replay on a RO header) still sees it.
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() { _ = ro.Close() }()
	rows, err := ro.ListResources(ResourceFilter{IncludeManaged: true, Limit: 10})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("row not flushed to main file: got %d rows, want 1", len(rows))
	}
}

// TestClose_ReadOnlyDoesNotWrite proves the Close-time checkpoint+DELETE is
// gated off for a read-only store: closing it returns no error and leaves the
// DB openable again (the cleanup must never fire a write against a RO handle,
// which would error "attempt to write a readonly database").
func TestClose_ReadOnlyDoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("insert scan: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close writable: %v", err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	if err := ro.Close(); err != nil {
		t.Fatalf("Close read-only must not error, got: %v", err)
	}
	// Reopen to prove the RO close left the file intact, not corrupted.
	if reopened, err := OpenReadOnly(path); err != nil {
		t.Fatalf("reopen after RO close: %v", err)
	} else {
		_ = reopened.Close()
	}
}

// TestOpenSelfHealsLeftoverWAL is the crash-recovery contract: a prior process
// that exited without closing leaves -wal/-shm behind; the next Open folds them
// in (wal_checkpoint(TRUNCATE)) and the next clean Close removes the sidecars,
// with the leaked-in-WAL row preserved.
//
// The leftover is produced by re-exec'ing this test binary into
// TestHelperLeakWAL, which writes a resource through a real store and
// os.Exit(0)s WITHOUT Close — faithfully reproducing a SIGKILL'd scan (an
// in-process last-connection close always auto-deletes the sidecars, so it
// cannot reproduce the leak).
//
// Note: SQLite recovers a leftover WAL on the next open regardless, so this
// guards the crash-recovery lifecycle contract (no orphaned sidecars, data
// intact) end-to-end rather than isolating Open()'s self-heal checkpoint.
func TestOpenSelfHealsLeftoverWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leak.db")

	cmd := exec.Command(os.Args[0], "-test.run", "^TestHelperLeakWAL$") //nolint:gosec // os.Args[0] is this test binary
	cmd.Env = append(os.Environ(), "DISCO_WAL_LEAK_DB="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("leak helper failed: %v\n%s", err, out)
	}

	// Precondition: the helper really left orphaned sidecars with no open conn.
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("expected leftover -wal after uncleanly-exited helper, stat err=%v", err)
	}

	st, err := Open(path) // self-heal: checkpoint(TRUNCATE) folds the leaked WAL in
	if err != nil {
		t.Fatalf("Open (self-heal): %v", err)
	}
	if err := st.Close(); err != nil { // clean close removes the sidecars
		t.Fatalf("Close: %v", err)
	}

	walSidecarsGone(t, path)

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() { _ = ro.Close() }()
	rows, err := ro.ListResources(ResourceFilter{IncludeManaged: true, Limit: 10})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("leaked-in-WAL row lost after self-heal: got %d rows, want 1", len(rows))
	}
}

// TestHelperLeakWAL is not a real test: it is the subprocess body for
// TestOpenSelfHealsLeftoverWAL. When DISCO_WAL_LEAK_DB is set it writes one
// resource through a real store and exits WITHOUT closing, leaving the -wal/-shm
// sidecars on disk. It is a no-op (skips) in a normal test run.
func TestHelperLeakWAL(t *testing.T) {
	path := os.Getenv("DISCO_WAL_LEAK_DB")
	if path == "" {
		t.Skip("subprocess helper for TestOpenSelfHealsLeftoverWAL; set DISCO_WAL_LEAK_DB to run")
	}
	st, err := Open(path)
	if err != nil {
		t.Fatalf("helper Open: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("helper insert scan: %v", err)
	}
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "111", Type: "aws:s3:bucket",
		NativeID: "b-1", AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("helper upsert: %v", err)
	}
	os.Exit(0) // intentionally no st.Close() → orphan the WAL sidecars
}

// TestClose_LogsWhenSidecarCleanupBlocked is the multi-process visibility
// contract: when another connection holds the DB open, Close()'s WAL cleanup is
// blocked (checkpoint busy + DELETE switch refused) and must surface one
// diagnostic line instead of failing silently — the likely explanation for
// "sidecars left behind even though no process has the db open" (the blocker
// outlived disco's close). A lingering second connection deterministically
// reproduces the block, so this guards the new code fail-first (without it the
// captured buffer is empty).
func TestClose_LogsWhenSidecarCleanupBlocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("insert scan: %v", err)
	}
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "111", Type: "aws:s3:bucket",
		NativeID: "b-1", AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// A second connection holds an OPEN read transaction over the DB: in WAL
	// mode that keeps a read lock, which blocks both the checkpoint truncate and
	// the WAL→DELETE switch when the store closes. (An autocommit read would
	// release the lock immediately and let cleanup succeed — the lock must be
	// held across st.Close().)
	blocker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	blocker.SetMaxOpenConns(1)
	defer func() { _ = blocker.Close() }()
	tx, err := blocker.Begin()
	if err != nil {
		t.Fatalf("blocker begin: %v", err)
	}
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM resources").Scan(&n); err != nil {
		t.Fatalf("blocker read: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	var buf bytes.Buffer
	orig := walCleanupWarnW
	walCleanupWarnW = &buf
	t.Cleanup(func() { walCleanupWarnW = orig })

	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := buf.String(); !strings.Contains(got, "WAL cleanup deferred") || !strings.Contains(got, path) {
		t.Errorf("expected a cleanup-deferred diagnostic naming %q, got: %q", path, got)
	}
	// The blocked path must leave the sidecars in place (cleaned on next open),
	// not pretend success — proving we logged the skip, not the happy path.
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Errorf("expected -wal to persist while DB still open elsewhere, stat err=%v", err)
	}
}
