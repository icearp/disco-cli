package gcp

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"codeberg.org/icearp/disco/internal/coverage"
)

var updateSnapshot = flag.Bool("update-snapshot", false, "rewrite testdata/coverage_snapshot.json")

// TestCoverageSnapshotStable is the behavior-preserving guard for the
// registerType migration: moving a type's emit/alias declaration from the
// legacy sites into a restype.Descriptor must leave the provider's Emits() and
// Aliases() output byte-identical. Regenerate intentionally with
// `go test ./internal/providers/gcp/ -run CoverageSnapshot -update-snapshot`.
func TestCoverageSnapshotStable(t *testing.T) {
	snap := coverageSnapshot()
	path := filepath.Join("testdata", "coverage_snapshot.json")

	if *updateSnapshot {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, snap, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot (regenerate with -update-snapshot): %v", err)
	}
	if string(want) != string(snap) {
		t.Errorf("coverage snapshot drift: Emits()/Aliases() changed.\n"+
			"If intentional, regenerate with -update-snapshot.\ngot:\n%s", snap)
	}
}

// coverageSnapshot serializes the provider's aggregated Emits + Aliases in a
// stable, sorted form.
func coverageSnapshot() []byte {
	var p coverageProvider

	emits := p.Emits()
	sort.Slice(emits, func(i, j int) bool { return emits[i].DiscoType < emits[j].DiscoType })

	type snapshot struct {
		Emits   []coverage.TypeDecl `json:"emits"`
		Aliases map[string]string   `json:"aliases"`
	}
	// json marshals maps with sorted keys, so Aliases is deterministic.
	out, _ := json.MarshalIndent(snapshot{Emits: emits, Aliases: p.Aliases()}, "", "  ")
	return append(out, '\n')
}
