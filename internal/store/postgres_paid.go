//go:build paid

// Postgres backend for *Store. Single struct, driver-branched dialect bits —
// the SQLite path is untouched. Pinned to one tenant at open time; every
// physical conn runs `set_config('app.tenant_id', $1, false)` via the pgx
// AfterConnect hook so RLS policies see the GUC on every query.
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// OpenPostgres opens a Postgres-backed Store at dsn, pinned to tenantID.
// tenantID must parse as a UUID; the value is set as `app.tenant_id` GUC
// on every new physical connection so per-table RLS policies filter rows
// without per-query plumbing.
//
// The same *Store value satisfies every read and write method the SQLite
// path supports; dialect differences are handled inside dialect.go,
// rebind helpers, and the migration runner.
func OpenPostgres(ctx context.Context, dsn, tenantID string) (*Store, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return nil, fmt.Errorf("tenant_id must be a UUID: %w", err)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg dsn: %w", err)
	}
	// Custom GUCs (`app.tenant_id`) cannot be set via RuntimeParams — PG
	// rejects unknown startup params. AfterConnect runs once per physical
	// connection, before any handle is returned to database/sql, so the
	// session-scoped GUC is sticky for every subsequent query through that
	// conn. tenantID is UUID-validated above, so inlining is safe — PgConn
	// has no parameterised Exec on the simple-query path.
	cfg.AfterConnect = func(ctx context.Context, c *pgconn.PgConn) error {
		mrr := c.Exec(ctx, "SELECT set_config('app.tenant_id', '"+tenantID+"', false)")
		if _, err := mrr.ReadAll(); err != nil {
			return fmt.Errorf("set tenant_id GUC: %w", err)
		}
		return nil
	}
	std := stdlib.OpenDB(*cfg)
	db := sqlx.NewDb(std, "pgx")
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
