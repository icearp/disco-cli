package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/snapshot"
)

func resetSnapshotFlags() {
	snapshotForce = false
	snapshotSigningPayload = ""
	verifySigPath = ""
	verifyPubKeyPath = ""
}

func runSnapshot(t *testing.T, ext string) string {
	t.Helper()
	seedTestDB(t)
	resetSnapshotFlags()
	out := filepath.Join(t.TempDir(), "snap."+ext)
	_, err := captureStderr(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"snapshot", out})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", ext, err)
	}
	return out
}

func TestSnapshotCmd_Zip(t *testing.T)      { archiveRoundTrip(t, runSnapshot(t, "zip")) }
func TestSnapshotCmd_TarGz(t *testing.T)    { archiveRoundTrip(t, runSnapshot(t, "tar.gz")) }
func TestSnapshotCmd_TarXz(t *testing.T)    { archiveRoundTrip(t, runSnapshot(t, "tar.xz")) }
func TestSnapshotCmd_TgzAlias(t *testing.T) { archiveRoundTrip(t, runSnapshot(t, "tgz")) }
func TestSnapshotCmd_TxzAlias(t *testing.T) { archiveRoundTrip(t, runSnapshot(t, "txz")) }

func archiveRoundTrip(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perm: %o", fi.Mode().Perm())
	}
	m, computed, err := snapshot.ArchiveContents(path)
	if err != nil {
		t.Fatalf("ArchiveContents: %v", err)
	}
	if m.Format != snapshot.FormatV1 {
		t.Errorf("format: %q", m.Format)
	}
	if m.ToolVersion != Version {
		t.Errorf("tool_version: %q want %q", m.ToolVersion, Version)
	}
	if computed != m.DBSHA256 {
		t.Errorf("manifest hash drift: %s vs %s", m.DBSHA256, computed)
	}
}

func TestSnapshotCmd_UnknownExt(t *testing.T) {
	seedTestDB(t)
	resetSnapshotFlags()
	out := filepath.Join(t.TempDir(), "snap.tar.foo")
	_, err := captureStderr(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"snapshot", out})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported snapshot format") {
		t.Errorf("want format error, got %v", err)
	}
}

func TestSnapshotCmd_RefusesExisting(t *testing.T) {
	seedTestDB(t)
	resetSnapshotFlags()
	out := filepath.Join(t.TempDir(), "snap.zip")
	if err := os.WriteFile(out, []byte("hello"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := captureStderr(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"snapshot", out})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("want --force suggestion, got %v", err)
	}
}

func TestSnapshotCmd_ForceOverwrite(t *testing.T) {
	seedTestDB(t)
	resetSnapshotFlags()
	out := filepath.Join(t.TempDir(), "snap.tar.gz")
	if err := os.WriteFile(out, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	snapshotForce = true
	t.Cleanup(func() { snapshotForce = false })
	_, err := captureStderr(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"snapshot", out, "--force"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("snapshot --force: %v", err)
	}
	archiveRoundTrip(t, out)
}

func TestSnapshotCmd_DBReadOnlyAllowed(t *testing.T) {
	seedTestDB(t)
	resetSnapshotFlags()
	out := filepath.Join(t.TempDir(), "snap.tar.xz")
	dbReadOnly = true
	t.Cleanup(func() { dbReadOnly = false })
	_, err := captureStderr(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"snapshot", out})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("snapshot under --db-readonly: %v", err)
	}
	archiveRoundTrip(t, out)
}
