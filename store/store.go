// Package store is the SQLite persistence layer (modernc.org/sqlite,
// CGO-free) for resources, relationships, hierarchy closure, and scan
// lifecycle. See store/CLAUDE.md for table shape and edge
// kinds.
package store

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jmoiron/sqlx"
	// modernc.org/sqlite is the CGO-free SQLite driver; blank import
	// registers it with database/sql so sqlx can open `sqlite` URIs.
	_ "modernc.org/sqlite"
)

// ScanWarning is a non-fatal skip captured during a scan — typically an
// access-denied error on a single service or region. The scan orchestrator
// collects warnings in memory and renders them as a grouped block after the
// scan, instead of interleaving with progress output.
type ScanWarning struct {
	Provider string // "aws", "azure", "gcp"
	Service  string // e.g. "kms:ListKeys", "compute"
	Scope    string // accountID[/region] or subscriptionID or projectID
	Message  string // err.Error()
}

// ScanNotice is a by-design decision a scan made that the operator should see
// but that indicates nothing wrong — the scanner working as intended, not
// degrading. Pruning regions an account has not enabled is the canonical case.
//
// It exists to keep [ScanWarning] meaningful. A warning that fires on every
// healthy scan trains people to ignore the block it appears in, and a
// by-design skip reported as a warning also inflates the "N warnings" summary
// count, so a genuinely clean run never reads as clean. Notices are collected
// and rendered exactly like warnings, under their own heading and outside that
// count.
//
// A skip is only a notice when the scan reached the right answer. If the
// scanner could not determine something and fell back — a preflight probe that
// failed, an optimisation switched off because a lookup was denied — that is a
// warning, because coverage or cost silently changed.
type ScanNotice struct {
	Provider string // "aws", "azure", "gcp"
	Service  string // e.g. "preflight:regions"
	Scope    string // accountID[/region] or subscriptionID or projectID
	Message  string // what was decided, and why
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
// silent default; cmd/scan.go wires the ones it needs before kicking off a
// scan. See internal/providers/CLAUDE.md ("Errors never abort scan") for the
// wider contract.
// driver names the underlying database backend. Branched in dialect-specific
// query construction (json_extract vs ->>, placeholder format, ON CONFLICT
// semantics). Set by Open* constructors; never mutated.
type driver string

const (
	driverSQLite   driver = "sqlite"
	driverPostgres driver = "postgres"
)

// ServiceStatus is the terminal state of a per-service scan, surfaced as a
// uniform "(<scope>: <state>)" suffix on the progress line (see
// cmd/scan.go::serviceStatusSuffix). ServiceDisabled: account/subscription/
// project hasn't enabled the service but could → "(<tenant>: disabled)".
// ServiceUnavailable: service not deployed in this AWS region, nothing the
// user can do → "(region: unavailable)". ServiceNotEntitled: service exists
// but the tenant can't self-enable it — a support-tier gate (Trusted
// Advisor API needs Business/Enterprise AWS Support), a service closed to
// new customers (Migration Hub), or an
// account AWS hasn't made eligible (CloudSearch) → "(<tenant>: not
// entitled)". Distinct from ServiceDisabled because there's no toggle the
// user controls. ServiceBillingDisabled: the project/account has billing
// disabled (self-enableable — associate a billing account) → "(<tenant>:
// billing disabled)". Sits beside ServiceDisabled (both self-enableable
// preconditions) rather than the warnings block. ServiceBlocked: the provider
// refuses every operation on the service for every caller, whatever their
// permissions — a withdrawn or closed service answering 403 "all API operations
// are blocked" → "(service: blocked)". Distinct from ServiceNotEntitled, which
// is about THIS tenant's eligibility; blocked is about the service itself.
type ServiceStatus uint8

const (
	ServiceOK ServiceStatus = iota
	ServiceDisabled
	ServiceUnavailable
	ServiceNotEntitled
	ServiceBillingDisabled
	ServiceBlocked
)

type Store struct {
	db                *sqlx.DB // pool. nil iff this Store was produced by WrapTx.
	tx                *sqlx.Tx // non-nil iff produced by WrapTx; caller owns lifecycle.
	driver            driver
	readOnly          bool                                                                                      // true iff opened via OpenReadOnly; skips the Close-time WAL checkpoint+cleanup on a RO DB.
	path              string                                                                                    // SQLite file path; set by Open. Names the DB in the WAL-cleanup-deferred diagnostic.
	OnServiceComplete func(service, scope string, total, newCount, changed, errCount int, status ServiceStatus) // after each service scan; scope = AWS region (or "global"), Azure subscription ID, GCP project ID; errCount>0 surfaces "(with errors)", status surfaces "(<tenant>: disabled)" / "(region: unavailable)" / "(<tenant>: not entitled)" / "(<tenant>: billing disabled)"
	OnResolveStart    func(provider string)                                                                     // just before phase-2 resolvers run
	OnResolveComplete func(provider string, edges int)                                                          // after all resolvers finish
	OnWarn            func(ScanWarning)                                                                         // skip-worthy error handled (transient, access-denied)
	OnNotice          func(ScanNotice)                                                                          // by-design decision worth showing; nothing is wrong
	OnError           func(ScanError)                                                                           // service or resolver failure; never aborts the scan
	activeCounter     *atomic.Int64                                                                             // non-nil only in scoped copies returned by WithRelCounter
	upsertNew         *atomic.Int64                                                                             // non-nil only in scoped copies returned by WithUpsertCounters; bumped on first-discovery
	upsertChanged     *atomic.Int64                                                                             // non-nil only in scoped copies returned by WithUpsertCounters; bumped on version split
	relBuf            *relBuffer                                                                                // non-nil only in scoped copies returned by BeginRelBuffer
	nativeIDSeen      *sync.Map                                                                                 // key r.ID → nativeIDSighting; per-writable-pool collision detector (see noteNativeIDType). Shared across scoped copies.
	writeFailStreak   *atomic.Int64                                                                             // consecutive connection-level write failures; opens the withWriteRetry circuit at writeCircuitTrip. Pointer so scoped copies share one breaker.
}

// ReportService invokes OnServiceComplete if set, called after each service
// scan function returns. scope is the per-call dimension that would otherwise
// duplicate the line in multi-region / multi-account scans (AWS region or
// "global", Azure subscription ID, GCP project ID). total = resources seen
// this scan; newCount = resources discovered for the first time (never
// previously in the DB); changed = existing resources whose attributes or
// tags changed this scan (a version split); errCount = errors encountered
// scanning this service (>0 surfaces as a "(with errors)" suffix on the
// progress line). status = ServiceDisabled (not enabled in this
// account/project/subscription), ServiceNotEntitled (exists but the tenant
// can't self-enable it), or ServiceUnavailable (not deployed in this AWS
// region); each surfaces as a "(<scope>: <state>)" suffix, mutually exclusive
// with errCount>0 since a skipped service emits no errors.
func (s *Store) ReportService(service, scope string, total, newCount, changed, errCount int, status ServiceStatus) {
	if s.OnServiceComplete != nil {
		s.OnServiceComplete(service, scope, total, newCount, changed, errCount, status)
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

// WithUpsertCounters returns a shallow copy of the Store with the new/changed
// upsert counters bound. UpsertResources bumps newC on each first-discovery and
// changedC on each version split. Providers create two local atomic.Int64
// around a per-service scan dispatch, pass them here, then read them for
// ReportService — attributing the split per (service, scope) without threading
// a second count through every scanner signature. Mirrors WithRelCounter.
func (s *Store) WithUpsertCounters(newC, changedC *atomic.Int64) *Store {
	s2 := *s
	s2.upsertNew = newC
	s2.upsertChanged = changedC
	return &s2
}

// BeginRelBuffer returns a shallow copy of the Store whose UpsertRelationship
// calls accumulate into an in-memory buffer instead of each running its own
// autocommit transaction. Call FlushRelBuffer on the returned store to write
// every buffered edge in one transaction via UpsertRelationships.
//
// Phase-2 resolvers emit ~1k+ edges per scan; on SQLite (MaxOpenConns=1) every
// autocommit INSERT serialises on the single writer, each paying prepare +
// WAL-frame + lock overhead. Buffering per resolver collapses that to one tx
// per resolver. Each call returns an independent buffer, so concurrent
// resolvers (Azure runs them in an errgroup) each get their own — safe to use
// one buffered store per resolver goroutine. activeCounter is preserved, so the
// ReportResolveComplete edge tally is unaffected.
func (s *Store) BeginRelBuffer() *Store {
	s2 := *s
	s2.relBuf = &relBuffer{}
	return &s2
}

// FlushRelBuffer writes all edges buffered since BeginRelBuffer in a single
// transaction and clears the buffer. No-op (nil) on a store without a buffer.
func (s *Store) FlushRelBuffer() error {
	if s.relBuf == nil {
		return nil
	}
	s.relBuf.mu.Lock()
	edges := s.relBuf.edges
	s.relBuf.edges = nil
	s.relBuf.mu.Unlock()
	return s.UpsertRelationships(edges)
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

// ReportNotice fires OnNotice if set. Providers call this instead of
// ReportWarning when a skip or narrowing is the intended behaviour rather than
// a problem — see [ScanNotice] for where the line falls.
func (s *Store) ReportNotice(n ScanNotice) {
	if s.OnNotice != nil {
		s.OnNotice(n)
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
	// concurrently during a scan. WAL mode still allows concurrent readers
	// alongside the single writer.
	db.SetMaxOpenConns(1)

	if err := applyPragmas(db.DB, false); err != nil {
		_ = db.Close()
		return nil, err
	}

	// Fold any WAL left behind by a prior unclean exit (SIGKILL / crash) into
	// the main file and reset it. applyPragmas just re-set WAL mode, so this
	// keeps WAL active for the scan while self-healing leftover sidecars — the
	// clean-exit Close path (checkpoint + journal_mode=DELETE) removes them.
	_, _ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

	s := &Store{db: db, driver: driverSQLite, path: path, nativeIDSeen: &sync.Map{}, writeFailStreak: &atomic.Int64{}}
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
	return &Store{db: db, driver: driverSQLite, readOnly: true}, nil
}

// Close closes the underlying database connection. No-op for tx-bound Stores
// produced by WrapTx — the caller owns the transaction lifecycle.
//
// For a writable SQLite store it first ends the WAL session cleanly: checkpoint
// the WAL tail into the main file, then switch journal_mode to DELETE so SQLite
// deterministically removes the -wal/-shm sidecars (the next Open re-enables
// WAL). Without this, SQLite's best-effort last-connection auto-delete — which
// needs an exclusive lock and is silently skipped if a reader lingers — is the
// source of orphaned sidecars. Errors are ignored: a held lock leaves the WAL
// in place (safely replayed on next open) rather than failing the command.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	if s.driver == driverSQLite && !s.readOnly {
		s.checkpointAndCleanup()
	}
	return s.db.Close()
}

// walCleanupWarnW is where the WAL-cleanup-deferred diagnostic is written.
// Package-level + injectable so a test can capture the line; os.Stderr in prod.
var walCleanupWarnW io.Writer = os.Stderr

// checkpointAndCleanup ends the WAL session before the final db.Close():
// checkpoint the WAL tail into the main file, then flip journal_mode to DELETE
// so SQLite removes the -wal/-shm sidecars (the next Open re-enables WAL). Both
// steps need the locks; another process (or a lingering reader) holding the DB
// blocks them — wal_checkpoint(TRUNCATE) reports busy and the DELETE switch is
// refused (SQLite forbids leaving WAL mode while other connections are open).
// That is safe (the WAL stays, replayed on next open) but is the likely source
// of "sidecars left behind", so surface one diagnostic line instead of failing
// silently. Never returns an error or blocks.
func (s *Store) checkpointAndCleanup() {
	// wal_checkpoint(TRUNCATE) returns one row: (busy, log, checkpointed).
	// busy != 0 (or a scan error) means locks blocked a full checkpoint.
	var busy, logPages, ckpt int
	ckptErr := s.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logPages, &ckpt)

	// journal_mode = DELETE returns the resulting mode; anything other than
	// "delete" means the WAL→rollback switch was refused (DB open elsewhere).
	var mode string
	_ = s.db.QueryRow("PRAGMA journal_mode = DELETE").Scan(&mode)

	if ckptErr != nil || busy != 0 || (mode != "" && !strings.EqualFold(mode, "delete")) {
		_, _ = fmt.Fprintf(walCleanupWarnW,
			"disco: SQLite WAL cleanup deferred (%s in use); -wal/-shm remain, cleaned on next open\n", s.path)
	}
}

// DB returns the underlying sqlx.DB for use in packages that need direct access.
// Returns nil for tx-bound Stores produced by WrapTx.
func (s *Store) DB() *sqlx.DB {
	return s.db
}

// WrapTx returns a *Store that runs queries against tx instead of a connection
// pool. The returned store does NOT own the transaction — caller must Commit
// or Rollback. Close() is a no-op.
//
// Intended for read-only use from a multi-tenant request path, where the caller
// has already issued `SET LOCAL search_path = tenant_<hex>, public` and
// `SET LOCAL app.tenant_id = '<uuid>'` on the tx. Write methods that call
// s.db.Begin* directly (UpsertResources, UpsertRelationships, RecordHierarchyBatch,
// etc.) will panic on a nil pool — intentional; do not invoke them on this code
// path.
//
// drv must match the dialect of the tx's driver: store.DriverPostgres for a
// pgx-backed tx, store.DriverSQLite for SQLite.
func WrapTx(tx *sqlx.Tx, drv Driver) *Store {
	return &Store{tx: tx, driver: driver(drv)}
}

// Driver names a supported backend for WrapTx.
type Driver string

const (
	DriverSQLite   Driver = Driver(driverSQLite)
	DriverPostgres Driver = Driver(driverPostgres)
)

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
