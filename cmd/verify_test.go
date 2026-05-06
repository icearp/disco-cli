package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func snapshotForVerifyTest(t *testing.T, ext string) string {
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
		t.Fatalf("snapshot: %v", err)
	}
	return out
}

func TestVerifyCmd_OK_Zip(t *testing.T)   { verifyOK(t, snapshotForVerifyTest(t, "zip")) }
func TestVerifyCmd_OK_TarGz(t *testing.T) { verifyOK(t, snapshotForVerifyTest(t, "tar.gz")) }
func TestVerifyCmd_OK_TarXz(t *testing.T) { verifyOK(t, snapshotForVerifyTest(t, "tar.xz")) }

func verifyOK(t *testing.T, path string) {
	t.Helper()
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"verify", path})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.HasPrefix(out, "OK:") {
		t.Errorf("want OK prefix, got %q", out)
	}
}

func TestVerifyCmd_HashMismatch(t *testing.T) {
	path := snapshotForVerifyTest(t, "zip")
	// Truncate / overwrite to corrupt the inner DB.
	if err := os.WriteFile(path, []byte("PK\x03\x04tampered"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"verify", path})
		return cmd.Execute()
	})
	if err == nil {
		t.Errorf("want error on tampered archive, got nil")
	}
}

func TestVerifyCmd_MissingArchive(t *testing.T) {
	dir := t.TempDir()
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"verify", filepath.Join(dir, "nope.zip")})
		return cmd.Execute()
	})
	if err == nil {
		t.Errorf("want error on missing archive, got nil")
	}
}

func TestVerifyCmd_UnknownExt(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "snap.tar.foo")
	if err := os.WriteFile(bad, []byte("anything"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"verify", bad})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported snapshot format") {
		t.Errorf("want format error, got %v", err)
	}
}
