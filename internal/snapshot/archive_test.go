package snapshot

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectFormat_Extensions(t *testing.T) {
	cases := []struct {
		path string
		want Format
		err  bool
	}{
		{"snap.zip", FormatZip, false},
		{"snap.ZIP", FormatZip, false},
		{"snap.tar.gz", FormatTarGz, false},
		{"snap.TGZ", FormatTarGz, false},
		{"snap.tar.xz", FormatTarXz, false},
		{"snap.txz", FormatTarXz, false},
		{"snap.tar.foo", FormatUnknown, true},
		{"snap", FormatUnknown, true},
	}
	for _, c := range cases {
		got, err := DetectFormat(c.path)
		if c.err {
			if err == nil {
				t.Errorf("%s: want error, got %v", c.path, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: want %v, got error %v", c.path, c.want, err)
		}
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.path, got, c.want)
		}
	}
}

func TestArchive_RoundTrip(t *testing.T) {
	for _, ext := range []string{"zip", "tar.gz", "tar.xz"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "src.db")
			payload := make([]byte, 4096)
			if _, err := rand.Read(payload); err != nil {
				t.Fatalf("rand: %v", err)
			}
			if err := os.WriteFile(dbPath, payload, 0o600); err != nil {
				t.Fatalf("seed db: %v", err)
			}

			srcHash, err := HashFile(dbPath)
			if err != nil {
				t.Fatalf("hash src: %v", err)
			}

			m := Manifest{
				Format:      FormatV1,
				ToolVersion: "vTest",
				GeneratedAt: "2026-05-06T00:00:00Z",
				DBSHA256:    srcHash,
				Scans:       []ScanRef{{ID: "s1"}},
			}

			out := filepath.Join(dir, "snap."+ext)
			format, err := DetectFormat(out)
			if err != nil {
				t.Fatalf("DetectFormat: %v", err)
			}
			if err := WriteArchive(out, format, dbPath, m); err != nil {
				t.Fatalf("WriteArchive: %v", err)
			}

			got, computed, err := ArchiveContents(out)
			if err != nil {
				t.Fatalf("ArchiveContents: %v", err)
			}
			if computed != srcHash {
				t.Errorf("inner hash drift: got %s want %s", computed, srcHash)
			}
			if got.DBSHA256 != srcHash || got.ToolVersion != "vTest" || got.Format != FormatV1 {
				t.Errorf("manifest mismatch: %+v", got)
			}

			fi, err := os.Stat(out)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if fi.Mode().Perm() != 0o600 {
				t.Errorf("output perm: %o", fi.Mode().Perm())
			}
			if _, err := os.Stat(out + ".tmp"); !os.IsNotExist(err) {
				t.Errorf("tmp file lingered: %v", err)
			}
		})
	}
}

func TestArchive_MissingEntries(t *testing.T) {
	// Truncated archive should surface an error from the decoder, not
	// silently report missing entries.
	dir := t.TempDir()
	out := filepath.Join(dir, "broken.zip")
	if err := os.WriteFile(out, []byte("not a zip"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, err := ArchiveContents(out)
	if err == nil || !strings.Contains(err.Error(), "open zip") {
		t.Errorf("want zip error, got %v", err)
	}
}
