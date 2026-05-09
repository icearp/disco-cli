//go:build paid

package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/pg/*.sql
var migrationPGFS embed.FS

// migratePG applies any pending Postgres migrations. Mirrors the SQLite
// runner (migrate.go) one-for-one: same `schema_migrations` bookkeeping
// table, same NNN_name.sql filename → version parsing, same per-statement
// splitStatements semicolon split. Each migration is applied in its own
// transaction so partial failure doesn't leave a half-migrated DB.
func (s *Store) migratePG(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := s.db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("query migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan migrations: %w", err)
	}

	entries, err := migrationPGFS.ReadDir("migrations/pg")
	if err != nil {
		return fmt.Errorf("read pg migrations dir: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, err := parseMigrationVersion(e.Name())
		if err != nil {
			return err
		}
		if applied[version] {
			continue
		}

		data, err := migrationPGFS.ReadFile("migrations/pg/" + e.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", e.Name(), err)
		}

		tx, err := s.db.BeginTxx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin tx for migration %d: %w", version, err)
		}
		for _, stmt := range splitStatements(string(data)) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("apply migration %d (%q): %w", version, stmt[:min(len(stmt), 60)], err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, applied_at) VALUES ($1, now())",
			version,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return nil
}
