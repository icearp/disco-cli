package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// Store is the primary access point for the disco database.
type Store struct {
	db                *sqlx.DB
	OnServiceComplete func(service string, total, inserted int) // optional; called by providers after each service scan
	OnResolveStart    func(provider string)                     // optional; called just before phase-2 resolvers run
	OnResolveComplete func(provider string, edges int)          // optional; called after all resolvers finish
	activeCounter     *atomic.Int64                             // non-nil only in scoped copies returned by WithRelCounter
}

// ReportService invokes OnServiceComplete if set. Providers call this after each
// service scan function returns successfully. total = resources seen this scan,
// inserted = resources newly added (not previously in the DB).
func (s *Store) ReportService(service string, total, inserted int) {
	if s.OnServiceComplete != nil {
		s.OnServiceComplete(service, total, inserted)
	}
}

// WithRelCounter returns a shallow copy of the Store with activeCounter set.
// UpsertRelationship increments the counter on each call. Providers create a
// local atomic.Int64, pass it here, then read it for ReportResolveComplete.
// Safe to shallow-copy because the struct contains no embedded sync/atomic values.
func (s *Store) WithRelCounter(c *atomic.Int64) *Store {
	s2 := *s
	s2.activeCounter = c
	return &s2
}

// ReportResolveStart fires OnResolveStart (if set).
// Call this immediately before the phase-2 resolver loop begins.
func (s *Store) ReportResolveStart(provider string) {
	if s.OnResolveStart != nil {
		s.OnResolveStart(provider)
	}
}

// ReportResolveComplete fires OnResolveComplete with the supplied edge count.
// Call this immediately after all resolvers finish.
func (s *Store) ReportResolveComplete(provider string, edges int) {
	if s.OnResolveComplete != nil {
		s.OnResolveComplete(provider, edges)
	}
}

// Open opens (or creates) the SQLite database at path and applies any pending migrations.
// It creates the parent directory if it does not exist.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}

	db, err := sqlx.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// SQLite allows only one writer at a time. Setting MaxOpenConns=1 prevents
	// "database is locked" (SQLITE_BUSY) errors when multiple goroutines write
	// concurrently during a scan. Reads are not affected because WAL mode allows
	// concurrent readers alongside the single writer.
	db.SetMaxOpenConns(1)

	if err := applyPragmas(db.DB); err != nil {
		db.Close()
		return nil, err
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying sqlx.DB for use in packages that need direct access.
func (s *Store) DB() *sqlx.DB {
	return s.db
}

func applyPragmas(db *sql.DB) error {
	pragmas := []string{
		// WAL allows one writer and many concurrent readers without blocking.
		"PRAGMA journal_mode = WAL",
		// NORMAL is safe with WAL (no data loss on crash) and much faster than FULL,
		// which would fsync on every transaction.
		"PRAGMA synchronous = NORMAL",
		// Enforce foreign key constraints (disabled by default in SQLite).
		"PRAGMA foreign_keys = ON",
		// Negative value = kibibytes. -64000 ≈ 64 MB in-process page cache.
		"PRAGMA cache_size = -64000",
		// Keep temp tables (used for sorting/grouping) in memory instead of disk.
		"PRAGMA temp_store = MEMORY",
		// Map up to 256 MB of the database file into virtual memory for faster reads.
		"PRAGMA mmap_size = 268435456",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	return nil
}
