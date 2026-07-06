// Postgres backend for *Store: single struct, driver-branched dialect bits;
// the SQLite path is untouched. The OSS backend is single-tenant — a plain
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
}

// WithAfterConnect registers a hook that runs once per physical connection,
// before any handle reaches database/sql. It is the multi-tenant extension
// point: disco-saas passes a hook that SETs search_path to a per-tenant
// schema and set_config's app.tenant_id / app.workspace_id for its RLS
// policies. The OSS CLI passes no options and gets a plain single-tenant
// pool.
//
// Composes with RDS IAM auth (pgx's separate BeforeConnect phase) — both can
// be active at once.
func WithAfterConnect(h func(context.Context, *pgconn.PgConn) error) PGOption {
	return func(c *pgConfig) { c.afterConnect = h }
}

// OpenPostgres opens a Postgres-backed Store at dsn. With no options it's the
// OSS CLI's single-tenant PG backend; pass WithAfterConnect to layer
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
	boundPoolFromEnv(db)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping pg: %w", err)
	}

	s := &Store{db: db, driver: driverPostgres}
	if err := s.migratePG(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pg migrate: %w", err)
	}
	return s, nil
}

// boundPoolFromEnv caps the connection pool when DISCO_PG_MAX_CONNS holds a
// positive integer; otherwise it keeps database/sql defaults (unbounded open
// conns, no lifetime), so a standalone CLI is unaffected. A deployment
// running many scanner tasks against a shared RDS sets it to keep each
// task's footprint small. MaxIdleConns caps at min(n, 2); lifetime/idle-time
// bounds let idle conns drain back to the server.
func boundPoolFromEnv(db *sqlx.DB) {
	v := os.Getenv("DISCO_PG_MAX_CONNS")
	if v == "" {
		return
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return
	}
	db.SetMaxOpenConns(n)
	idle := n
	if idle > 2 {
		idle = 2
	}
	db.SetMaxIdleConns(idle)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
}
