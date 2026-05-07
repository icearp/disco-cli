package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RulesSHA256 returns a deterministic hex-encoded sha256 over the union of
// every .rego file under paths (files or directories, recursive) plus every
// in-memory module passed via modules. The hash is over a NUL-delimited
// concatenation of `name\x00body\x00...` with entries sorted by name, so
// adding/removing/editing any rule perturbs it. Used as SARIF rule-pack
// provenance so attestations can prove which ruleset produced findings.
//
// Module names are normalized to a stable shape: filesystem paths are
// rendered absolute then made repo-relative via filepath.Clean; embedded
// pack entries (already keyed `<pack>/<file>`) round-trip unchanged. Empty
// inputs yield the sha256 of the empty string ("e3b0c4...").
func RulesSHA256(paths []string, modules map[string]string) (string, error) {
	collected := map[string]string{}
	maps.Copy(collected, modules)
	for _, p := range paths {
		if err := walkRegoFiles(p, collected); err != nil {
			return "", err
		}
	}

	names := make([]string, 0, len(collected))
	for n := range collected {
		names = append(names, n)
	}
	sort.Strings(names)

	h := sha256.New()
	for _, n := range names {
		_, _ = h.Write([]byte(n))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(collected[n]))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func walkRegoFiles(p string, out map[string]string) error {
	info, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("stat %s: %w", p, err)
	}
	if !info.IsDir() {
		if !strings.HasSuffix(p, ".rego") {
			return nil
		}
		return readInto(p, out)
	}
	return filepath.WalkDir(p, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || !strings.HasSuffix(path, ".rego") {
			return werr
		}
		return readInto(path, out)
	})
}

func readInto(path string, out map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	out[filepath.Clean(path)] = string(b)
	return nil
}
