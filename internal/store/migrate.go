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
	defer rows.Close()
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
				tx.Rollback()
				return fmt.Errorf("apply migration %d (%q): %w", version, stmt[:min(len(stmt), 60)], err)
			}
		}
		if _, err := tx.Exec(
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))",
			version,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return nil
}

// splitStatements splits a SQL script into individual statements on semicolons.
// This is necessary because Go's database/sql Exec executes only the first
// statement in a multi-statement string; the rest are silently ignored.
// Blank and comment-only chunks are skipped.
func splitStatements(script string) []string {
	var stmts []string
	for _, raw := range strings.Split(script, ";") {
		stmt := strings.TrimSpace(raw)
		if stmt == "" {
			continue
		}
		// Skip pure-comment chunks (lines all starting with --)
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
	return stmts
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
