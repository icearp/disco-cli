// Postgres backend for *Store: single struct, driver-branched dialect bits;
// the SQLite path is untouched. The backend is single-tenant — a plain
// pool against one schema. Module consumers needing multi-tenancy
// (disco-saas) inject a per-connection hook via WithAfterConnect (below) to
// pin search_path and set the app.* GUCs their row-level-security layer
// needs.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// PGOption configures an OpenPostgres call.
type PGOption func(*pgConfig)

// pgConfig accumulates optional OpenPostgres settings.
type pgConfig struct {
	afterConnect func(context.Context, *pgconn.PgConn) error
	maxConns     int // 0 = not set by the caller; fall through to env, then default
}

// pgDefaultMaxConns bounds the pool when neither WithMaxConns nor
// DISCO_PG_MAX_CONNS is set.
//
// This is a safety floor, not a tuned throughput figure. database/sql's own
// default is *unlimited*, and a provider scan fans out far wider than a
// database wants to be dialed: the AWS scanner alone runs up to
// maxConcurrentServices × maxConcurrentRegions (10 × 5 = 50) service
// goroutines, each batch-upserting. Unbounded, that is up to 50 simultaneous
// cold connections — TCP + TLS handshake apiece, plus an IAM token mint per
// dial when DISCO_PG_IAM_AUTH is on — which is enough to exhaust a modest RDS
// max_connections or simply queue past the write deadline.
//
// 10 keeps write throughput ample (batch upserts are fast, and a scan is
// dominated by cloud API latency, not by the store) while capping the dial
// burst well below the goroutine count. A deployment that knows its own RDS
// budget and task count should set an explicit value rather than rely on this.
//
// The value is not derived from the provider constants programmatically: store
// must not import internal/providers (see store/CLAUDE.md — it would create a
// cmd → policy → store → providers cycle). If those constants move materially,
// revisit this comment.
const pgDefaultMaxConns = 10

// WithMaxConns caps the connection pool at n. It is the programmatic form of
// DISCO_PG_MAX_CONNS and takes precedence over it, so a module consumer
// (disco-saas) can size the pool from its own deployment topology — RDS
// max_connections divided across concurrent scanner tasks — without reaching
// through the process environment.
//
// n <= 0 is ignored, leaving the env var (then pgDefaultMaxConns) to decide.
//
// Sizing note for RDS Proxy deployments: a WithAfterConnect hook that sets
// session-scoped GUCs pins each connection, so the Proxy stops multiplexing it
// and pool size maps 1:1 onto real backend connections. Size as if there were
// no multiplexing benefit. See store/CLAUDE.md "RDS Proxy session-pinning
// trade-off".
func WithMaxConns(n int) PGOption {
	return func(c *pgConfig) { c.maxConns = n }
}

// WithAfterConnect registers a hook that runs once per physical connection,
// before any handle reaches database/sql. It is the multi-tenant extension
// point: disco-saas passes a hook that SETs search_path to a per-tenant
// schema and set_config's app.tenant_id / app.workspace_id for its RLS
// policies. The CLI passes no options and gets a plain single-tenant
// pool.
//
// Composes with RDS IAM auth (pgx's separate BeforeConnect phase) — both can
// be active at once.
func WithAfterConnect(h func(context.Context, *pgconn.PgConn) error) PGOption {
	return func(c *pgConfig) { c.afterConnect = h }
}

// OpenPostgres opens a Postgres-backed Store at dsn. With no options it's the
// single-tenant PG backend the CLI uses; pass WithAfterConnect to layer
// multi-tenancy onto every connection.
//
// The same *Store satisfies every read/write method the SQLite path
// supports; dialect differences live in dialect.go, rebind helpers, and the
// migration runner.
func OpenPostgres(ctx context.Context, dsn string, opts ...PGOption) (*Store, error) {
	var pc pgConfig
	for _, o := range opts {
		o(&pc)
	}

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg dsn: %w", err)
	}

	// RDS IAM auth (DISCO_PG_IAM_AUTH): when enabled, beforeConnect mints a
	// fresh IAM token as password before every physical dial; nil when off
	// (DSN used verbatim — password or local trust).
	beforeConnect, err := iamBeforeConnect(ctx)
	if err != nil {
		return nil, err
	}

	// AfterConnect runs once per physical connection, before any handle
	// reaches database/sql, so anything it sets (session GUCs, search_path)
	// stays sticky for every query through that conn.
	cfg.AfterConnect = pc.afterConnect

	// With IAM auth, the pool opens via a connector whose BeforeConnect mints
	// a fresh token per physical dial; otherwise the plain stdlib pool.
	var std *sql.DB
	if beforeConnect != nil {
		std = sql.OpenDB(stdlib.GetConnector(*cfg, stdlib.OptionBeforeConnect(beforeConnect)))
	} else {
		std = stdlib.OpenDB(*cfg)
	}
	db := sqlx.NewDb(std, "pgx")
	boundPool(db, resolvePoolSize(pc.maxConns))
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping pg: %w", err)
	}

	s := &Store{db: db, driver: driverPostgres, nativeIDSeen: &sync.Map{}, writeFailStreak: &atomic.Int64{}}
	if err := s.migratePG(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pg migrate: %w", err)
	}
	return s, nil
}

// resolvePoolSize picks the pool cap: an explicit WithMaxConns wins, then
// DISCO_PG_MAX_CONNS, then pgDefaultMaxConns. Unparseable or non-positive
// values at either layer fall through to the next rather than erroring — a
// malformed env var must not stop a scan from running, and the fallback is
// still bounded.
//
// The pool is never left unbounded. database/sql's zero value means unlimited,
// which is the wrong default for a writer this concurrent (see
// pgDefaultMaxConns).
func resolvePoolSize(opt int) int {
	if opt > 0 {
		return opt
	}
	if n, err := strconv.Atoi(os.Getenv("DISCO_PG_MAX_CONNS")); err == nil && n > 0 {
		return n
	}
	return pgDefaultMaxConns
}

const (
	// pgConnMaxLifetime recycles a connection regardless of use, so a long-lived
	// task doesn't pin one backend process forever.
	pgConnMaxLifetime = 30 * time.Minute
	// pgConnMaxIdleTime is how long an unused connection lingers before being
	// returned to the server. It is the release valve that makes idle==open
	// safe (see boundPool): deliberately short, because it is the only thing
	// bounding a finished task's footprint.
	//
	// Neither may be zero — database/sql reads 0 as "no bound", which is what
	// left connections accumulating for the life of a process.
	pgConnMaxIdleTime = 90 * time.Second
)

// boundPool applies the resolved cap plus lifetime bounds. Unlike the previous
// env-gated version this always runs, so an unconfigured pool gets bounds too.
//
// MaxIdleConns tracks MaxOpenConns deliberately, and it is a change from the
// old min(n, 2). Holding idle far below open makes the pool re-dial constantly
// under bursty write load — return 10 connections, keep 2, re-handshake 8 on
// the next batch — and re-dialing is the expensive part here: TLS plus, under
// DISCO_PG_IAM_AUTH, a fresh token mint every time. That churn is the suspected
// source of the RDS write timeouts this bounding was added for.
//
// The trade-off that buys: a task now holds up to n idle connections instead of
// 2 after its writes finish, which matters for the case DISCO_PG_MAX_CONNS was
// created for — many scanner tasks against one RDS — and matters more behind
// RDS Proxy, where a session-pinned connection maps 1:1 onto a backend one
// (see store/CLAUDE.md "RDS Proxy session-pinning trade-off"). pgConnMaxIdleTime
// is therefore cut to 90s from the old 5m: during a scan the connections are
// hot and never idle that long, so reuse is unaffected, but a finished task
// drops back to zero in well under two minutes rather than five. Deployments
// running many concurrent tasks should still set an explicit WithMaxConns.
func boundPool(db *sqlx.DB, n int) {
	db.SetMaxOpenConns(n)
	db.SetMaxIdleConns(n)
	db.SetConnMaxLifetime(pgConnMaxLifetime)
	db.SetConnMaxIdleTime(pgConnMaxIdleTime)
}
