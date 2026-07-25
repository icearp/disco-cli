package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

func TestResolvePoolSize(t *testing.T) {
	cases := []struct {
		name string
		opt  int
		env  string
		want int
	}{
		// The bug this replaced: an unconfigured pool inherited database/sql's
		// unlimited default, so a full scan could demand one physical
		// connection per in-flight service goroutine.
		{"nothing set falls back to the bounded default", 0, "", pgDefaultMaxConns},
		{"env var honoured", 0, "25", 25},
		{"option honoured", 25, "", 25},
		// A module consumer sizing from its own topology must win over whatever
		// happens to be in the task's environment.
		{"option beats env", 5, "25", 5},
		// A typo in deployment config must not stop the scan, and must not
		// leave the pool unbounded either.
		{"malformed env falls through", 0, "lots", pgDefaultMaxConns},
		{"zero env falls through", 0, "0", pgDefaultMaxConns},
		{"negative env falls through", 0, "-4", pgDefaultMaxConns},
		{"non-positive option falls through to env", -1, "7", 7},
		{"non-positive option and no env falls through to default", -1, "", pgDefaultMaxConns},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DISCO_PG_MAX_CONNS", c.env)
			if got := resolvePoolSize(c.opt); got != c.want {
				t.Errorf("resolvePoolSize(%d) with env %q = %d, want %d", c.opt, c.env, got, c.want)
			}
		})
	}
}

// resolvePoolSize must never return the unlimited sentinel, whatever it is fed.
func TestResolvePoolSize_NeverUnbounded(t *testing.T) {
	for _, env := range []string{"", "0", "-1", "not-a-number", "  ", "9999999999999999999999"} {
		t.Setenv("DISCO_PG_MAX_CONNS", env)
		for _, opt := range []int{-1, 0} {
			if got := resolvePoolSize(opt); got <= 0 {
				t.Errorf("resolvePoolSize(%d) with env %q = %d; 0 or less means unlimited in database/sql", opt, env, got)
			}
		}
	}
}

// boundPool must apply every bound, not just the open-conn cap: an
// unconfigured pool previously got no lifetime or idle-time limits either, so
// connections accumulated for the life of the process.
func TestBoundPool_AppliesAllBounds(t *testing.T) {
	db := newPoolTestDB(t)
	boundPool(db, 7)

	s := db.Stats()
	if s.MaxOpenConnections != 7 {
		t.Errorf("MaxOpenConnections = %d, want 7", s.MaxOpenConnections)
	}
	// database/sql exposes no getter for the idle/lifetime settings, so assert
	// the observable consequence: with idle == open, returning a connection to
	// the pool leaves it idle rather than closing it.
	if err := db.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if idle := db.Stats().Idle; idle != 1 {
		t.Errorf("Idle = %d after one returned conn, want 1 (idle cap must track open, not sit below it)", idle)
	}
}

// The reuse property is the point of MaxIdleConns tracking MaxOpenConns:
// re-dialing costs a TLS handshake and, under DISCO_PG_IAM_AUTH, a token mint.
func TestBoundPool_ReusesConnectionsRatherThanRedialing(t *testing.T) {
	db := newPoolTestDB(t)
	boundPool(db, 4)

	for range 5 {
		if err := db.Ping(); err != nil {
			t.Fatalf("ping: %v", err)
		}
	}
	if opened := db.Stats().MaxIdleClosed; opened != 0 {
		t.Errorf("MaxIdleClosed = %d, want 0: sequential use must reuse one conn, not churn it", opened)
	}
}

// newPoolTestDB returns a real *sqlx.DB whose pool settings can be observed.
// It uses SQLite (the CGO-free driver already linked) because the assertions
// are about database/sql's pool, which is driver-independent — no Postgres
// server required.
func newPoolTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", t.TempDir()+"/pool.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db := sqlx.NewDb(raw, "sqlite")
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Guard the constant itself: it exists to bound a fan-out that is far wider
// than it, and a "fix" that raised it to match the goroutine count would
// reintroduce the dial burst it was added to prevent.
func TestPGDefaultMaxConnsIsAFloorNotTheFanoutWidth(t *testing.T) {
	if pgDefaultMaxConns <= 0 {
		t.Fatalf("pgDefaultMaxConns = %d; must be positive or the pool is unlimited", pgDefaultMaxConns)
	}
	// AWS fans out maxConcurrentServices (10) × maxConcurrentRegions (5) = 50.
	// Hardcoded, not imported: store must not depend on internal/providers.
	const awsScanFanout = 50
	if pgDefaultMaxConns >= awsScanFanout {
		t.Errorf("pgDefaultMaxConns = %d >= scan fan-out %d; the cap no longer bounds the dial burst",
			pgDefaultMaxConns, awsScanFanout)
	}
}

// database/sql exposes no getter for the lifetime bounds, so assert on the
// production constants themselves rather than local copies — a local copy would
// pass no matter what boundPool actually applied.
func TestPGLifetimeBoundsAreNeverUnbounded(t *testing.T) {
	// 0 means "no bound" in database/sql: connections are reused forever and
	// idle ones are never returned. That is the state this change fixed, so a
	// future edit must not reintroduce it by zeroing either constant.
	if pgConnMaxLifetime <= 0 {
		t.Errorf("pgConnMaxLifetime = %v; 0 or less disables recycling entirely", pgConnMaxLifetime)
	}
	if pgConnMaxIdleTime <= 0 {
		t.Errorf("pgConnMaxIdleTime = %v; 0 or less means idle conns are never released", pgConnMaxIdleTime)
	}
	// Idle-time above lifetime could never fire — the connection would always
	// be recycled first, leaving nothing to bound a finished task's footprint.
	if pgConnMaxIdleTime > pgConnMaxLifetime {
		t.Errorf("pgConnMaxIdleTime (%v) > pgConnMaxLifetime (%v): the idle bound can never fire",
			pgConnMaxIdleTime, pgConnMaxLifetime)
	}
	// idle==open is only defensible because the idle bound releases connections
	// promptly once a scan goes quiet. If it drifts long, that trade-off is
	// silently gone — see boundPool's comment.
	if pgConnMaxIdleTime > 2*time.Minute {
		t.Errorf("pgConnMaxIdleTime = %v; idle conns equal max conns, so a long idle bound "+
			"leaves every finished task holding a full pool against shared RDS", pgConnMaxIdleTime)
	}
}
