package regionsgen

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot walks up to the directory holding go.mod, mirroring what the
// generator does when go generate runs it from a package directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

// TestGeneratedFileIsCurrent is the staleness guard, and it is a test rather
// than a make target on purpose: disco's CI runs `go test ./...` and no make
// targets, so a Makefile check would gate nothing. A drifted table is silent by
// nature — the scanner keeps working, just against a stale view of which
// (service × region) pairs exist — so nothing else would surface it.
func TestGeneratedFileIsCurrent(t *testing.T) {
	root := repoRoot(t)

	table, err := Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want, err := Render(table)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	path := filepath.Join(root, "internal", "providers", "aws", "awsregions", GeneratedFile)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is stale (%d services regenerate to %d bytes, file has %d); run `make gen-regions` and commit the result",
			GeneratedFile, len(table), len(want), len(got))
	}
}

// TestBuildCoversTheScannerSurface guards the generator's own failure mode,
// which is quiet: a parse that matches nothing yields an empty table, region
// scoping silently switches off everywhere, and every scan still succeeds — just
// slower. Build refuses to return an empty table for exactly this reason, so the
// assertions here are about the shape it does return.
func TestBuildCoversTheScannerSurface(t *testing.T) {
	table, err := Build(repoRoot(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// The AWS surface is ~300 services and roughly three quarters resolve to an
	// SDK endpoint table. A floor well under that catches a parser that half
	// works — the case a non-empty check sails past.
	const floor = 150
	if len(table) < floor {
		t.Errorf("Build resolved %d services, want at least %d; the scanner-file or endpoints-file shape likely changed", len(table), floor)
	}

	regions, ok := table["aws:cassandra"]
	if !ok {
		t.Fatal("aws:cassandra missing; its scanner imports the keyspaces SDK package, so the import-to-service join broke")
	}
	// The name-derived catalog code for this service is "cassandra", which AWS
	// files as "mcs" — the mismatch that made name-based mapping unworkable.
	// Resolving it anyway is the whole point of joining on the SDK package.
	if len(regions) == 0 {
		t.Error("aws:cassandra resolved to no regions")
	}
}
