package store

import (
	"embed"
	"strings"
	"testing"
)

// maxMigrationVersion returns the highest NNN_ version among the .sql files in
// dir of fsys. Fails the test on a malformed name.
func maxMigrationVersion(t *testing.T, fsys embed.FS, dir string) int {
	t.Helper()
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	max := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		v, err := parseMigrationVersion(e.Name())
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		if v > max {
			max = v
		}
	}
	return max
}

// TestMigrationVersionCeilingsMatch guards a coincidence the staleness check
// (cmd/helpers.go) relies on: TargetSchemaVersion reads ONLY the SQLite embed,
// yet it is compared against PG-backed CurrentSchemaVersion. The two dialects
// already number the same logical change differently (SQLite 003_scan_errors vs
// PG 005_scan_errors_jsonb), so their max versions could silently diverge — at
// which point the PG staleness check would compare against the wrong ceiling.
// They are equal today (both 14); a future migration that lands under a different
// number per dialect trips this test, prompting the author to keep the ceilings
// aligned (or make TargetSchemaVersion driver-aware).
func TestMigrationVersionCeilingsMatch(t *testing.T) {
	sqlite := maxMigrationVersion(t, migrationFS, "migrations")
	pg := maxMigrationVersion(t, migrationPGFS, "migrations/pg")
	if sqlite != pg {
		t.Fatalf("migration version ceilings diverged: SQLite max=%d, PG max=%d — "+
			"align the highest version across both dialects, or make TargetSchemaVersion driver-aware", sqlite, pg)
	}
}
