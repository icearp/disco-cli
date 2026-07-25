package store

import (
	"context"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// timeoutErr is a net.Error reporting Timeout()==true, the shape a pgconn dial
// timeout presents.
type timeoutErr struct{}

func (timeoutErr) Error() string { return "i/o timeout" }
func (timeoutErr) Timeout() bool { return true }
func (timeoutErr) Temporary() bool {
	return true
}

func TestIsRetryableDBError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		// Connection-level: worth retrying.
		{"bad conn", sqldriver.ErrBadConn, true},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"io.EOF", io.EOF, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"net timeout", timeoutErr{}, true},
		{"dial error", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, true},
		{"wrapped dial error", fmt.Errorf("upsert resources: %w", &net.OpError{Op: "dial", Err: errors.New("refused")}), true},
		{"pg connection exception", &pgconn.PgError{Code: "08006"}, true},
		{"pg admin shutdown", &pgconn.PgError{Code: "57P01"}, true},
		{"pg too many connections", &pgconn.PgError{Code: "53300"}, true},
		// Statement-level defects: a bug, so fail fast rather than burn the budget.
		{"pg unique violation", &pgconn.PgError{Code: "23505"}, false},
		{"pg foreign key violation", &pgconn.PgError{Code: "23503"}, false},
		{"pg syntax error", &pgconn.PgError{Code: "42601"}, false},
		// A cancelled scan must not be kept alive by retries.
		{"context canceled", context.Canceled, false},
		{"plain error", errors.New("something else"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRetryableDBError(c.err); got != c.want {
				t.Errorf("isRetryableDBError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// The whole point of the sentinel: an exhausted store write must NOT still look
// like the network error underneath it. The AWS dispatcher classifies
// net.Error / io.EOF as a transient cloud glitch worth a warn-and-continue, and
// pgconn reports a dropped Postgres connection exactly that way — so if the
// cause stayed in the errors.Is chain, a dead database would be reported as a
// benign per-service warning while every row it should have stored vanished.
func TestStoreWriteError_BreaksTheCauseChain(t *testing.T) {
	cause := fmt.Errorf("failed to receive message: %w", io.ErrUnexpectedEOF)
	err := storeWriteError("upsert resources", cause)

	if !errors.Is(err, ErrStoreWrite) {
		t.Error("must satisfy errors.Is(err, ErrStoreWrite) so dispatchers can identify it")
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		t.Error("cause must NOT remain in the errors.Is chain: wrap with a formatted string, not a wrapping verb")
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		t.Error("cause must NOT remain reachable via errors.As, or it reads as a transient network error")
	}
	// The text is still preserved for diagnosis; only type identity is severed.
	if want := "unexpected EOF"; !strings.Contains(err.Error(), want) {
		t.Errorf("message must retain the cause text %q, got %q", want, err.Error())
	}
}

func TestWithWriteRetry_SucceedsFirstTryIsSilent(t *testing.T) {
	s := newRetryTestStore(t)
	var warned int
	s.OnWarn = func(ScanWarning) { warned++ }

	calls := 0
	if err := s.withWriteRetry("op", func() error { calls++; return nil }); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if calls != 1 {
		t.Errorf("want 1 call, got %d", calls)
	}
	if warned != 0 {
		t.Errorf("a clean write must not warn; got %d warnings", warned)
	}
}

// Recovered-after-retry is not an error — the data IS persisted — but the
// operator should still see that the database wobbled.
func TestWithWriteRetry_RecoveredWarnsButSucceeds(t *testing.T) {
	s := newRetryTestStore(t)
	var got ScanWarning
	var warned int
	s.OnWarn = func(w ScanWarning) { got = w; warned++ }

	calls := 0
	err := s.withWriteRetry("upsert resources", func() error {
		calls++
		if calls < 2 {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
	if err != nil {
		t.Fatalf("recovered write must return nil, got %v", err)
	}
	if calls != 2 {
		t.Errorf("want 2 calls, got %d", calls)
	}
	if warned != 1 {
		t.Fatalf("want exactly 1 warning, got %d", warned)
	}
	if got.Provider != "store" || got.Service != "write" || got.Scope != "upsert resources" {
		t.Errorf("warning fields: %+v", got)
	}
}

func TestWithWriteRetry_ExhaustedReturnsSentinel(t *testing.T) {
	s := newRetryTestStore(t)

	calls := 0
	err := s.withWriteRetry("upsert resources", func() error {
		calls++
		return io.ErrUnexpectedEOF
	})
	if !errors.Is(err, ErrStoreWrite) {
		t.Fatalf("want ErrStoreWrite, got %v", err)
	}
	if calls != writeMaxAttempts {
		t.Errorf("want %d attempts, got %d", writeMaxAttempts, calls)
	}
}

// A statement-level defect must not consume the retry budget: it can never
// succeed, and delaying it delays the report.
func TestWithWriteRetry_NonRetryableFailsFast(t *testing.T) {
	s := newRetryTestStore(t)

	calls := 0
	err := s.withWriteRetry("upsert resources", func() error {
		calls++
		return &pgconn.PgError{Code: "23505", Message: "duplicate key"}
	})
	if !errors.Is(err, ErrStoreWrite) {
		t.Fatalf("want ErrStoreWrite, got %v", err)
	}
	if calls != 1 {
		t.Errorf("non-retryable error must be attempted exactly once, got %d", calls)
	}
}

// Against a genuinely dead database every write would otherwise pay the full
// backoff, turning a fast loud failure into a very slow one. After
// writeCircuitTrip consecutive connection failures, writes stop retrying.
func TestWithWriteRetry_CircuitOpensThenCloses(t *testing.T) {
	s := newRetryTestStore(t)

	for range writeCircuitTrip {
		_ = s.withWriteRetry("op", func() error { return io.ErrUnexpectedEOF })
	}

	calls := 0
	err := s.withWriteRetry("op", func() error { calls++; return io.ErrUnexpectedEOF })
	if !errors.Is(err, ErrStoreWrite) {
		t.Fatalf("want ErrStoreWrite, got %v", err)
	}
	if calls != 1 {
		t.Errorf("circuit open must attempt exactly once, got %d", calls)
	}

	// Any success closes the circuit again, so a recovered DB resumes retrying.
	if err := s.withWriteRetry("op", func() error { return nil }); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	calls = 0
	_ = s.withWriteRetry("op", func() error { calls++; return io.ErrUnexpectedEOF })
	if calls != writeMaxAttempts {
		t.Errorf("closed circuit must retry again: want %d attempts, got %d", writeMaxAttempts, calls)
	}
}

// A statement-level defect is not evidence the server is missing, so it must
// not push the breaker toward opening.
func TestWithWriteRetry_NonRetryableDoesNotTripCircuit(t *testing.T) {
	s := newRetryTestStore(t)

	for range writeCircuitTrip + 2 {
		_ = s.withWriteRetry("op", func() error {
			return &pgconn.PgError{Code: "23505"}
		})
	}
	calls := 0
	_ = s.withWriteRetry("op", func() error { calls++; return io.ErrUnexpectedEOF })
	if calls != writeMaxAttempts {
		t.Errorf("circuit must still be closed: want %d attempts, got %d", writeMaxAttempts, calls)
	}
}

// A WrapTx store runs inside a transaction the caller owns. Re-running a
// statement there cannot recover — on Postgres the first failure aborts the
// transaction and every later command returns 25P02, which would replace the
// real cause in the reported error.
func TestWithWriteRetry_CallerOwnedTxIsAttemptedOnce(t *testing.T) {
	base := openTestStore(t)
	tx, err := base.db.Beginx()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	s := WrapTx(tx, DriverSQLite)
	calls := 0
	err = s.withWriteRetry("upsert relationship", func() error {
		calls++
		return io.ErrUnexpectedEOF // retryable on a pool-backed store
	})
	if calls != 1 {
		t.Errorf("caller-owned tx must be attempted once, got %d attempts", calls)
	}
	if !errors.Is(err, ErrStoreWrite) {
		t.Errorf("want ErrStoreWrite, got %v", err)
	}
	// The single attempt's cause must survive into the message, not be replaced
	// by whatever a doomed second attempt would have reported.
	if want := "unexpected EOF"; !strings.Contains(err.Error(), want) {
		t.Errorf("want original cause %q in %q", want, err.Error())
	}
}

// newRetryTestStore returns a Store with only the fields withWriteRetry needs.
// It touches no database — these tests exercise the retry policy itself.
func newRetryTestStore(t *testing.T) *Store {
	t.Helper()
	return &Store{writeFailStreak: &atomic.Int64{}}
}

// keep the retry tests fast: backoff is real time, so assert it stays bounded.
func TestWriteRetryBackoffIsBounded(t *testing.T) {
	total := time.Duration(0)
	for i := 1; i < writeMaxAttempts; i++ {
		total += writeRetryBackoff << (i - 1)
	}
	if total > time.Second {
		t.Errorf("worst-case backoff %v exceeds 1s; a doomed write would stall the scan", total)
	}
}
