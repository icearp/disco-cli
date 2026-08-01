package store

import (
	"bufio"
	"embed"
	"io/fs"
	"strings"
	"testing"
)

// dmlAllowedMigrations names every migration permitted to carry DML, with the
// reason it is safe. A file may be added here only when its version is already
// recorded in every environment: an already-recorded version is skipped, so the
// statement can never run against a schema that has since been hardened.
var dmlAllowedMigrations = map[string]string{
	// Shipped long before any consumer put row-level security on resources,
	// and its version is burnt everywhere, so it only ever runs inside a fresh
	// provision -- where the table has no policy yet.
	"006_resource_versioning.sql": "predates RLS on resources; version burnt in every environment",
}

// TestMigrationsCarryNoDML is a static guard on a failure that only appears in
// a consumer, months later, on an environment no disco test can construct.
//
// disco-saas puts FORCE ROW LEVEL SECURITY on scans/resources/relationships
// with a policy reading current_setting('app.workspace_id'), and runs disco's
// migrations through a hook that sets the tenant GUC and not the workspace one.
// FORCE subjects the table owner too, so any DML there raises 42704 and takes
// the whole rolling migrate-tenants pass down. DDL is unaffected.
//
// Nothing in disco's own suite can see it: a fresh schema has no policy when
// the migration runs, which is every test schema and every new tenant. Only
// re-migrating an already-hardened schema fails, so the check has to be on the
// TEXT of the migration rather than on its behaviour. Version 016 shipped
// exactly this rewrite in v0.31.0 and was gutted in v0.31.1.
func TestMigrationsCarryNoDML(t *testing.T) {
	for _, set := range []struct {
		name string
		fsys embed.FS
		dir  string
	}{
		{"sqlite", migrationFS, "migrations"},
		{"postgres", migrationPGFS, "migrations/pg"},
	} {
		t.Run(set.name, func(t *testing.T) {
			entries, err := fs.ReadDir(set.fsys, set.dir)
			if err != nil {
				t.Fatalf("read %s: %v", set.dir, err)
			}
			var checked int
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
					continue
				}
				checked++
				if _, ok := dmlAllowedMigrations[e.Name()]; ok {
					continue
				}
				body, err := fs.ReadFile(set.fsys, set.dir+"/"+e.Name())
				if err != nil {
					t.Fatalf("read %s: %v", e.Name(), err)
				}
				for lineNo, stmt := range dmlStatements(string(body)) {
					t.Errorf("%s/%s:%d starts a %s. A disco migration may add, alter or drop "+
						"schema objects freely, but DML on a tenant table cannot run under a "+
						"consumer's FORCE row-level security -- see TestMigrationsCarryNoDML. "+
						"If the version is already burnt everywhere, add it to "+
						"dmlAllowedMigrations with the reason.",
						set.dir, e.Name(), lineNo, stmt)
				}
			}
			// A path typo or a renamed embed would make the loop above pass by
			// examining nothing at all.
			if checked == 0 {
				t.Fatalf("no .sql files found under %s", set.dir)
			}
		})
	}
}

// dmlStatements returns the 1-based line numbers of every line that begins a
// DML statement, keyed to the verb. Whole-line and trailing "--" comments are
// stripped first so prose describing an UPDATE is not mistaken for one.
func dmlStatements(body string) map[int]string {
	found := map[int]string{}
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if i := strings.Index(text, "--"); i >= 0 {
			text = text[:i]
		}
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		switch verb := strings.ToUpper(fields[0]); verb {
		case "UPDATE", "DELETE", "INSERT":
			found[line] = verb
		}
	}
	return found
}
