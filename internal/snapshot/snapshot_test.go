package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifest_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	want := Manifest{
		Format:      FormatV1,
		ToolVersion: "v1.2.3",
		GeneratedAt: "2026-05-06T12:00:00Z",
		DBSHA256:    "deadbeef",
		Scans: []ScanRef{
			{ID: "a"}, {ID: "b"}, {ID: "c"},
		},
	}
	if err := WriteManifest(path, want); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	got, err := ReadManifest(path)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if got.Format != want.Format || got.ToolVersion != want.ToolVersion ||
		got.GeneratedAt != want.GeneratedAt || got.DBSHA256 != want.DBSHA256 ||
		len(got.Scans) != 3 {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, want)
	}
}

func TestHashFile_KnownVector(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("disco"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	exp := sha256.Sum256([]byte("disco"))
	want := hex.EncodeToString(exp[:])
	if got != want {
		t.Errorf("hash: got %s want %s", got, want)
	}
}

func TestReadManifest_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := ReadManifest(path)
	if err == nil || !strings.Contains(err.Error(), "decode manifest") {
		t.Errorf("want decode error, got %v", err)
	}
}

func TestWriteManifest_Deterministic(t *testing.T) {
	dir := t.TempDir()
	m := Manifest{
		Format:      FormatV1,
		ToolVersion: "v1",
		GeneratedAt: "t",
		DBSHA256:    "h",
		Scans:       []ScanRef{{ID: "a"}, {ID: "b"}},
	}
	p1 := filepath.Join(dir, "m1.json")
	p2 := filepath.Join(dir, "m2.json")
	if err := WriteManifest(p1, m); err != nil {
		t.Fatalf("write m1: %v", err)
	}
	if err := WriteManifest(p2, m); err != nil {
		t.Fatalf("write m2: %v", err)
	}
	b1, _ := os.ReadFile(p1)
	b2, _ := os.ReadFile(p2)
	if string(b1) != string(b2) {
		t.Errorf("non-deterministic write:\n%s\nvs\n%s", b1, b2)
	}
}
