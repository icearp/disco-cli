// Package store is the SQLite persistence layer (modernc.org/sqlite,
// CGO-free) for resources, relationships, hierarchy closure, and scan
// lifecycle. See internal/store/CLAUDE.md for table shape and edge
// kinds.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/jmoiron/sqlx"
	// modernc.org/sqlite is the CGO-free SQLite driver; blank import
	// registers it with database/sql so sqlx can open `sqlite` URIs.
	_ "modernc.org/sqlite"
)

// ScanWarning is a non-fatal skip captured during a scan — typically an
// access-denied error on a single service or region. Warnings are collected
// in memory by the scan orchestrator and rendered as a grouped block after
// the scan completes, instead of interleaving with progress output.
type ScanWarning struct {
	Provider string // "aws", "azure", "gcp"
	Service  string // e.g. "kms:ListKeys", "compute"
	Scope    string // accountID[/region] or subscriptionID or projectID
	Message  string // err.Error()
}

// ScanError is a per-service / per-resolver failure captured during a scan.
// Errors never abort the scan; they are accumulated and rendered as a grouped
// block at the end so the user sees each failure exactly once.
type ScanError struct {
	Provider string // "aws", "azure", "gcp"
	Service  string // e.g. "ec2", "iam", "resolve:resolveBackupVaults"
	Scope    string // accountID[/region] or subscriptionID or projectID
	Message  string // err.Error()
}

// Store is the primary access point for the disco database.
//
// The On* callback fields implement the report-and-continue scan contract:
// providers never propagate per-service or per-resolver errors out of Scan().
// Instead they invoke OnError / OnWarn so cmd/scan.go can accumulate the
// failures and render one grouped block at the end. A nil callback is the
// silent default; callers (cmd/scan.go) wire the ones they need before
// kicking off a scan. See internal/providers/CLAUDE.md ("Errors never abort
// scan") for the wider contract.
type Store struct {
	db                *sqlx.DB
	OnServiceComplete func(service string, total, inserted, errCount int, disabled bool) // after each service scan; errCount>0 surfaces "(with errors)", disabled surfaces "(service disabled)"
	OnResolveStart    func(provider string)                                              // just before phase-2 resolvers run
	OnResolveComplete func(provider string, edges int)                                   // after all resolvers finish
	OnWarn            func(ScanWarning)                                                  // skip-worthy error handled (transient, access-denied)
	OnError           func(ScanError)                                                    // service or resolver failure; never aborts the scan
	activeCounter     *atomic.Int64                                                      // non-nil only in scoped copies returned by WithRelCounter
}

// ReportService invokes OnServiceComplete if set. Providers call this after each
// service scan function returns. total = resources seen this scan, inserted =
// resources newly added (not previously in the DB), errCount = number of errors
// encountered while scanning this service (>0 surfaces as a "(with errors)"
// suffix on the progress line). disabled = service is not enabled in this
// account/region (surfaces as a "(service disabled)" suffix; mutually
// exclusive with errCount>0 since a disabled service emits no errors).
func (s *Store) ReportService(service string, total, inserted, errCount int, disabled bool) {
	if s.OnServiceComplete != nil {
		s.OnServiceComplete(service, total, inserted, errCount, disabled)
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

// ReportWarning fires OnWarn if set. Providers call this from skip-handling
// helpers (skipIfAccessDenied, skipIfDenied) in place of log.Printf so that
// warnings can be collected and rendered as a single grouped block rather
// than interleaving with aligned progress output.
func (s *Store) ReportWarning(w ScanWarning) {
	if s.OnWarn != nil {
		s.OnWarn(w)
	}
}

// ReportError fires OnError if set. Providers call this when a service or
// resolver fails — the error is accumulated and rendered as a grouped block
// at the end of the scan, never aborting other in-flight work.
func (s *Store) ReportError(e ScanError) {
	if s.OnError != nil {
		s.OnError(e)
	}
}

// Open opens (or creates) the SQLite database at path and applies any pending migrations.
// It creates the parent directory if it does not exist.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
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

	if err := applyPragmas(db.DB, false); err != nil {
		_ = db.Close()
		return nil, err
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// Restrict DB file to owner-only. Stored attributes may embed sensitive
	// cloud metadata; default umask (0644) leaves them group/world-readable.
	// Chmod runs after migrate so the file is guaranteed to exist; skipped
	// for non-regular paths like :memory: URIs.
	if fi, statErr := os.Stat(path); statErr == nil && fi.Mode().IsRegular() {
		if err := os.Chmod(path, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("chmod db: %w", err)
		}
	}
	return s, nil
}

// OpenReadOnly opens the SQLite database at path with SQLITE_OPEN_READONLY.
// Skips migrate (schema is whatever's on disk) and skips chmod (caller owns
// the file). Any write path will fail at SQLite layer with "attempt to
// write a readonly database" — structural enforcement of the auditor /
// pipeline-handoff contract.
func OpenReadOnly(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	uri := "file:" + path + "?mode=ro"
	db, err := sqlx.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("open db (readonly): %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := applyPragmas(db.DB, true); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying sqlx.DB for use in packages that need direct access.
func (s *Store) DB() *sqlx.DB {
	return s.db
}

// applyPragmas sets per-connection SQLite tuning. readOnly skips writer-only
// pragmas (journal_mode=WAL, synchronous) — opening a RO DB and trying to
// switch journal modes fails with "attempt to write a readonly database",
// which would brick `disco --db-readonly check` against an evidence snapshot.
func applyPragmas(db *sql.DB, readOnly bool) error {
	pragmas := []string{
		// Enforce foreign key constraints (disabled by default in SQLite).
		"PRAGMA foreign_keys = ON",
		// Negative value = kibibytes. -64000 ≈ 64 MB in-process page cache.
		"PRAGMA cache_size = -64000",
		// Keep temp tables (used for sorting/grouping) in memory instead of disk.
		"PRAGMA temp_store = MEMORY",
		// Map up to 256 MB of the database file into virtual memory for faster reads.
		"PRAGMA mmap_size = 268435456",
	}
	if !readOnly {
		// WAL allows one writer and many concurrent readers without blocking.
		// NORMAL is safe with WAL (no data loss on crash) and much faster than FULL.
		// Both pragmas write to the DB header, so they're skipped on read-only opens.
		pragmas = append([]string{
			"PRAGMA journal_mode = WAL",
			"PRAGMA synchronous = NORMAL",
		}, pragmas...)
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	return nil
}
