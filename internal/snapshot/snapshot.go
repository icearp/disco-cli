// Package snapshot defines the disco-snapshot envelope: a manifest carrying
// tool version, scan IDs, generated-at timestamp, and a SHA-256 of the
// frozen DB. Used by `disco snapshot` to write the envelope and by
// `disco verify` to recompute the hash and compare. Pure stdlib so the
// receiver-side `disco verify` works regardless of the producer's build.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// FormatV1 is the only manifest format this package writes or accepts.
// Future revisions (e.g. signed manifests) bump this and add backward-
// compatible reader paths.
const FormatV1 = "disco-snapshot/v1"

// Manifest is the disco-snapshot envelope. JSON keys are stable;
// receiver-side checksums of manifest.json are reproducible across
// machines as long as the producer used WriteManifest (sorted-key indent).
type Manifest struct {
	Format      string   `json:"format"`
	ToolVersion string   `json:"tool_version"`
	GeneratedAt string   `json:"generated_at"`
	DBSHA256    string   `json:"db_sha256"`
	ScanIDs     []string `json:"scan_ids"`
}

// WriteManifest writes m to path with two-space indent and a trailing
// newline. encoding/json marshals struct fields in declaration order;
// the Manifest struct is laid out so the on-disk shape is deterministic.
// File mode 0o600 to mirror the store's DB permissions.
func WriteManifest(path string, m Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

// ReadManifest reads and decodes a manifest.json file written by
// WriteManifest. Malformed JSON or absent file returns a wrapped error.
func ReadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	return m, nil
}

// HashFile returns the lowercase-hex SHA-256 of the file at path. Streams
// via io.Copy so large DBs don't load entirely into memory.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}
	defer func() { _ = f.Close() }()
	return hashReader(f)
}

func hashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
