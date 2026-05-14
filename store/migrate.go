package store

import (
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := s.db.Query("SELECT version FROM schema_migrations")
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

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if isPaidMigration(e.Name()) && !paidBuild {
			continue
		}
		version, err := parseMigrationVersion(e.Name())
		if err != nil {
			return err
		}
		if applied[version] {
			continue
		}

		data, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", e.Name(), err)
		}

		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for migration %d: %w", version, err)
		}
		for _, stmt := range splitStatements(string(data)) {
			if _, err := tx.Exec(stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("apply migration %d (%q): %w", version, stmt[:min(len(stmt), 60)], err)
			}
		}
		if _, err := tx.Exec(
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))",
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

// splitStatements splits a SQL script into individual statements on
// semicolons. Necessary because Go's database/sql Exec executes only the
// first statement in a multi-statement string; the rest are silently
// ignored. Blank and comment-only chunks are skipped.
//
// Dollar-quoted strings ($$ ... $$ or $tag$ ... $tag$) are treated as
// opaque, so semicolons inside plpgsql function bodies don't split the
// statement. Line comments (-- ...) are also tracked so a `;` inside a
// comment doesn't fragment the surrounding DDL.
func splitStatements(script string) []string {
	var (
		stmts   []string
		buf     strings.Builder
		inDQ    bool   // inside a $tag$ ... $tag$ block
		dqTag   string // the active tag (including the wrapping $$s), e.g. "$fn$"
		inLnCmt bool   // inside a -- ... \n comment
	)
	flush := func() {
		stmt := strings.TrimSpace(buf.String())
		buf.Reset()
		if stmt == "" {
			return
		}
		allComment := true
		for _, line := range strings.Split(stmt, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
				allComment = false
				break
			}
		}
		if !allComment {
			stmts = append(stmts, stmt)
		}
	}
	for i := 0; i < len(script); i++ {
		c := script[i]
		if inLnCmt {
			buf.WriteByte(c)
			if c == '\n' {
				inLnCmt = false
			}
			continue
		}
		if inDQ {
			buf.WriteByte(c)
			if c == '$' && i+len(dqTag)-1 < len(script) && script[i:i+len(dqTag)] == dqTag {
				// Closing tag — emit the rest of it and exit the block.
				buf.WriteString(dqTag[1:])
				i += len(dqTag) - 1
				inDQ = false
				dqTag = ""
			}
			continue
		}
		// Top-level scanning.
		if c == '-' && i+1 < len(script) && script[i+1] == '-' {
			inLnCmt = true
			buf.WriteByte(c)
			continue
		}
		if c == '$' {
			// Look for an opening $tag$ where tag is empty or [A-Za-z0-9_]+.
			j := i + 1
			for j < len(script) && (script[j] == '_' ||
				(script[j] >= 'A' && script[j] <= 'Z') ||
				(script[j] >= 'a' && script[j] <= 'z') ||
				(script[j] >= '0' && script[j] <= '9')) {
				j++
			}
			if j < len(script) && script[j] == '$' {
				dqTag = script[i : j+1]
				inDQ = true
				buf.WriteString(dqTag)
				i = j
				continue
			}
		}
		if c == ';' {
			flush()
			continue
		}
		buf.WriteByte(c)
	}
	flush()
	return stmts
}

// isPaidMigration reports whether a migration file is paid-only by
// suffix convention: any `<n>_*_paid.sql` filename. Paid-only files
// are skipped in OSS builds (paidBuild = false) so the OSS schema
// stays free of paid-specific tables/columns.
func isPaidMigration(name string) bool {
	return strings.HasSuffix(name, "_paid.sql")
}

// parseMigrationVersion extracts the leading integer from a filename like "001_initial.sql".
func parseMigrationVersion(name string) (int, error) {
	parts := strings.SplitN(name, "_", 2)
	if len(parts) == 0 {
		return 0, fmt.Errorf("invalid migration filename: %s", name)
	}
	v, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("invalid migration version in %s: %w", name, err)
	}
	return v, nil
}

// TargetSchemaVersion returns the highest migration version embedded in the
// binary — the schema state a fully-migrated DB will land at after Open()
// applies any pending migrations. Used by read-only callers (cmd/helpers.go's
// openDB) to detect a stale on-disk schema and reject with a clear hint.
func TargetSchemaVersion() (int, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return 0, fmt.Errorf("read migrations dir: %w", err)
	}
	max := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if isPaidMigration(e.Name()) && !paidBuild {
			continue
		}
		v, err := parseMigrationVersion(e.Name())
		if err != nil {
			return 0, err
		}
		if v > max {
			max = v
		}
	}
	return max, nil
}

// CurrentSchemaVersion reads the highest applied migration from the on-disk
// schema_migrations table. Returns 0 when the table is missing or empty (a
// fresh-but-empty DB or a pre-migration-tracking checkout). Read-only callers
// pair this with TargetSchemaVersion to detect stale schemas and reject
// before issuing queries that would surface as cryptic SQLite errors.
func (s *Store) CurrentSchemaVersion() (int, error) {
	// Existence probe: schema_migrations is created by migrate() so an
	// uninitialised DB lacks the table. Treat that as version 0 rather than
	// erroring — caller decides whether 0 vs target counts as stale.
	var name string
	err := s.db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'",
	).Scan(&name)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return 0, nil
		}
		return 0, fmt.Errorf("probe schema_migrations: %w", err)
	}
	var max int
	if err := s.db.QueryRow(
		"SELECT COALESCE(MAX(version), 0) FROM schema_migrations",
	).Scan(&max); err != nil {
		return 0, fmt.Errorf("query current schema version: %w", err)
	}
	return max, nil
}
