package store

import (
	"context"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	sqlite "modernc.org/sqlite"
)

// ErrStoreWrite marks a failure to persist scan data. Every scan-path write
// wraps its cause in this sentinel so the provider dispatchers can tell "the
// database is unreachable" apart from "this cloud API misbehaved".
//
// The distinction matters because the two are otherwise indistinguishable: a
// pgconn dial timeout is a net.Error, and a dropped Postgres connection surfaces
// as io.ErrUnexpectedEOF — both of which the AWS dispatcher's
// isTransientNetworkError classifies as a momentary cloud-side glitch worth a
// warn-and-continue. Left unwrapped, a dead database therefore produced a
// benign per-service warning while every row it should have stored vanished,
// and the scan still reported success.
var ErrStoreWrite = errors.New("store write failed")

const (
	// writeMaxAttempts includes the first try, so this is one initial attempt
	// plus two retries.
	writeMaxAttempts = 3
	// writeRetryBackoff is the first inter-attempt pause; it doubles each retry
	// (100ms, 200ms), so a doomed write costs ~300ms rather than stalling the
	// 5-minute per-service deadline.
	writeRetryBackoff = 100 * time.Millisecond
	// writeCircuitTrip is how many consecutive connection-level write failures
	// open the circuit. Past it, writes stop retrying and fail immediately:
	// against a genuinely dead database every one of a scan's thousands of
	// writes would otherwise pay the full backoff, turning a fast, loud failure
	// into a very slow one. Any successful write closes the circuit again.
	writeCircuitTrip = 5
)

// SQLite primary result codes for writer contention. Extended codes carry
// detail in the high bits, so compare against the low byte.
const (
	sqliteBusy   = 5
	sqliteLocked = 6
)

// isRetryableDBError reports whether err is a transient *connection* failure
// worth retrying, as opposed to a defect in the statement itself. Constraint
// violations, type errors and syntax errors are bugs: retrying them wastes the
// budget and delays the report, so they fail fast.
func isRetryableDBError(err error) bool {
	if err == nil {
		return false
	}
	// A cancelled scan (Ctrl-C) must not be kept alive by retries.
	if errors.Is(err, context.Canceled) {
		return false
	}
	// Server-reported Postgres errors carry a SQLSTATE that says precisely
	// whether the connection or the statement was at fault, so classify on it
	// and never fall through to the transport checks below.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.HasPrefix(pgErr.Code, "08"): // connection exception
			return true
		case pgErr.Code == "57P01", // admin shutdown
			pgErr.Code == "57P02", // crash shutdown
			pgErr.Code == "57P03", // cannot connect now
			pgErr.Code == "53300": // too many connections
			return true
		}
		return false
	}
	// SQLite serialises writers; contention is transient by definition.
	var sqErr *sqlite.Error
	if errors.As(err, &sqErr) {
		switch sqErr.Code() & 0xff {
		case sqliteBusy, sqliteLocked:
			return true
		}
		return false
	}
	// Transport level: the pool handed back a dead conn, the dial timed out, or
	// the peer hung up mid-statement.
	if errors.Is(err, sqldriver.ErrBadConn) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// storeWriteError wraps cause in the ErrStoreWrite sentinel.
//
// The cause is rendered with %s, deliberately NOT %w. Keeping it in the
// errors.Is chain would let the provider dispatchers match the underlying
// net.Error / io.EOF and reclassify a database outage as a transient cloud
// glitch — exactly the masking this sentinel exists to prevent. The message
// text is preserved, so nothing is lost for diagnosis; only the type identity
// is severed. Do not "tidy" this to %w.
func storeWriteError(op string, cause error) error {
	return fmt.Errorf("%w: %s: %s", ErrStoreWrite, op, cause)
}

// withWriteRetry runs fn, retrying transient connection failures with
// exponential backoff.
//
// Outcomes, per the contract the scan dispatchers rely on:
//   - succeeded first try      → nil, silent.
//   - succeeded after a retry  → nil, plus a ScanWarning naming the recovery.
//     The data IS persisted, so this is not an error, but the operator should
//     see that the database wobbled.
//   - never succeeded          → ErrStoreWrite, which every dispatcher reports
//     as a hard scan error rather than a skippable warning.
func (s *Store) withWriteRetry(op string, fn func() error) error {
	attempts := writeMaxAttempts
	switch {
	case s.tx != nil:
		// Caller-owned transaction (WrapTx). A failed statement aborts the
		// transaction on Postgres, so every later command in it returns 25P02
		// (in_failed_sql_transaction) — a retry could only bury the real cause
		// behind that. Recovery here means restarting the caller's transaction,
		// which is theirs to decide at their own boundary.
		attempts = 1
	case s.writeFailStreak != nil && s.writeFailStreak.Load() >= writeCircuitTrip:
		attempts = 1 // circuit open: fail fast, no backoff
	}

	var err error
	for i := 1; i <= attempts; i++ {
		if err = fn(); err == nil {
			if i > 1 {
				s.ReportWarning(ScanWarning{
					Provider: "store",
					Service:  "write",
					Scope:    op,
					Message:  fmt.Sprintf("recovered after %d attempts", i),
				})
			}
			if s.writeFailStreak != nil {
				s.writeFailStreak.Store(0)
			}
			return nil
		}
		if !isRetryableDBError(err) {
			// A statement-level defect: report it now rather than after backoff.
			return storeWriteError(op, err)
		}
		if i < attempts {
			time.Sleep(writeRetryBackoff << (i - 1))
		}
	}
	// Only connection-level exhaustion counts toward the circuit; a stream of
	// constraint violations means the schema is wrong, not the server missing.
	if s.writeFailStreak != nil {
		s.writeFailStreak.Add(1)
	}
	return storeWriteError(op, err)
}
