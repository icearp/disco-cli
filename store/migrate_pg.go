package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
)

//go:embed migrations/pg/*.sql
var migrationPGFS embed.FS

// migratePG applies any pending Postgres migrations. It shares the SQLite
// runner's (migrate.go) `schema_migrations` bookkeeping table and its
// NNN_name.sql filename → version parsing, but NOT its per-statement
// [splitStatements] split: each file is executed whole, in one round trip
// (see [applyOnePG]). Each migration is applied in its own transaction so
// partial failure doesn't leave a half-migrated DB.
func (s *Store) migratePG(ctx context.Context) error {
	entries, err := migrationPGFS.ReadDir("migrations/pg")
	if err != nil {
		return fmt.Errorf("read pg migrations dir: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	// Read the already-applied versions WITHOUT first creating the bookkeeping
	// table. The probe and reads below are privilege-free, so a least-privilege
	// role (USAGE+CRUD, no CREATE on the schema) opening a fully-provisioned and
	// up-to-date store issues no DDL — pending migrations are applied by a
	// privileged role at provisioning/upgrade time. The probe is scoped to
	// current_schema() so a public.schema_migrations reachable via search_path
	// can't be mistaken for this schema's bookkeeping table.
	applied := map[int]bool{}
	var tracked bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_tables
		    WHERE schemaname = current_schema() AND tablename = 'schema_migrations')`,
	).Scan(&tracked); err != nil {
		return fmt.Errorf("probe schema_migrations: %w", err)
	}
	if tracked {
		rows, err := s.db.QueryContext(ctx, "SELECT version FROM schema_migrations")
		if err != nil {
			return fmt.Errorf("query migrations: %w", err)
		}
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				_ = rows.Close()
				return err
			}
			applied[v] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan migrations: %w", err)
		}
		_ = rows.Close()
	}

	type pendingMigration struct {
		version int
		name    string
	}
	var pending []pendingMigration
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, err := parseMigrationVersion(e.Name())
		if err != nil {
			return err
		}
		if !applied[version] {
			pending = append(pending, pendingMigration{version: version, name: e.Name()})
		}
	}
	if len(pending) == 0 {
		// Up-to-date: no DDL, no privilege required.
		return nil
	}

	// Pending work exists — from here on DDL is issued, which requires a
	// privileged (owner) role. A least-privilege role that is behind fails here
	// with a clear permission error rather than silently skipping migrations.
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, m := range pending {
		data, err := migrationPGFS.ReadFile("migrations/pg/" + m.name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", m.name, err)
		}

		tx, err := s.db.BeginTxx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin tx for migration %d: %w", m.version, err)
		}
		if err := applyOnePG(ctx, tx, m.version, m.name, data); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, applied_at) VALUES ($1, now())",
			m.version,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}
	return nil
}

// applyOnePG executes one migration file's whole body in a single ExecContext,
// rather than splitting it into statements the way the SQLite runner must.
// pgx sends a parameterless query over the simple protocol, which accepts a
// multi-statement body, so the round trips per migration drop from one per
// statement to one per file.
//
// It also retires a trap. [splitStatements] tracks `--` line comments and `$$`
// dollar-quoting but NOT single-quoted string literals, so a `;` inside a
// literal split the statement mid-string and failed with 42601 (measured; it
// shipped once in real COMMENT ON text). A `--` inside a literal is the milder
// half: the splitter keeps the bytes and merely fails to split there, so the
// two statements merge into one chunk that Postgres executes correctly anyway.
// Executing the body whole removes the first outright and makes the second
// moot. The SQLite runner still splits, because its driver takes one statement
// per Exec.
//
// A body that is empty or contains only comments is a no-op, not an error.
func applyOnePG(ctx context.Context, tx *sqlx.Tx, version int, name string, body []byte) error {
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("apply migration %d (%s): %w", version, name, err)
	}
	return nil
}
