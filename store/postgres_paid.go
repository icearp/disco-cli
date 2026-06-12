//go:build paid

// Postgres backend for *Store. Single struct, driver-branched dialect bits —
// the SQLite path is untouched. Pinned to one tenant at open time; every
// physical conn runs `set_config('app.tenant_id', $1, false)` via the pgx
// AfterConnect hook so RLS policies see the GUC on every query.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// schemaNameRE constrains per-tenant schemas to the SaaS provisioner's naming
// scheme: `tenant_` plus 32 lowercase hex chars. Identifiers cannot be bound
// as parameters, so this regex is the only injection guard before quoting.
var schemaNameRE = regexp.MustCompile(`^tenant_[0-9a-f]{32}$`)

// validateSchemaName rejects any schema name outside the tenant_<32hex>
// pattern. Defence-in-depth alongside caller validation in the SaaS repo.
func validateSchemaName(name string) error {
	if !schemaNameRE.MatchString(name) {
		return fmt.Errorf("invalid schema name %q: must match ^tenant_[0-9a-f]{32}$", name)
	}
	return nil
}

// pgQuoteIdent quotes a Postgres identifier by doubling embedded quotes and
// wrapping in `"..."`. Used after validateSchemaName for DDL that cannot bind
// the schema name as a parameter (CREATE SCHEMA, SET search_path).
func pgQuoteIdent(name string) string {
	out := make([]byte, 0, len(name)+2)
	out = append(out, '"')
	for i := 0; i < len(name); i++ {
		if name[i] == '"' {
			out = append(out, '"', '"')
			continue
		}
		out = append(out, name[i])
	}
	out = append(out, '"')
	return string(out)
}

// OpenPostgres opens a Postgres-backed Store at dsn, pinned to tenantID.
// tenantID must parse as a UUID; the value is set as `app.tenant_id` GUC
// on every new physical connection so per-table RLS policies filter rows
// without per-query plumbing.
//
// The same *Store value satisfies every read and write method the SQLite
// path supports; dialect differences are handled inside dialect.go,
// rebind helpers, and the migration runner.
func OpenPostgres(ctx context.Context, dsn, tenantID string) (*Store, error) {
	return openPG(ctx, dsn, tenantID, "", "")
}

// OpenPostgresWithWorkspace opens a Postgres-backed Store like OpenPostgres
// but also pins the `app.workspace_id` GUC, so writes satisfy the per-workspace
// RLS predicate and the NOT-NULL workspace_id column default, and reads see the
// per-workspace USING clause. workspaceID, when non-empty, must parse as a
// UUID; an empty value leaves the GUC unset (legacy single-tenant callers).
func OpenPostgresWithWorkspace(ctx context.Context, dsn, tenantID, workspaceID string) (*Store, error) {
	return openPG(ctx, dsn, tenantID, workspaceID, "")
}

// OpenPostgresInSchema opens a Postgres-backed Store whose connections have
// search_path pinned to schemaName, then "public". The schema is created if
// it does not exist. Migrations run within that schema, including a
// schema-local `schema_migrations` bookkeeping table — each per-tenant schema
// tracks its own migration version, so a rolling DDL fan-out is possible
// when upstream ships a new column.
//
// schemaName must match ^tenant_[0-9a-f]{32}$. tenantID is set as the
// `app.tenant_id` GUC on every connection (same as OpenPostgres).
//
// Use case: SaaS provisioner creating a per-tenant schema. The pool only
// needs to live long enough to migrate; close it after.
func OpenPostgresInSchema(ctx context.Context, dsn, schemaName, tenantID string) (*Store, error) {
	if err := validateSchemaName(schemaName); err != nil {
		return nil, err
	}
	return openPG(ctx, dsn, tenantID, "", schemaName)
}

// OpenPostgresInSchemaWithWorkspace opens a Postgres-backed Store like
// OpenPostgresInSchema but also pins the `app.workspace_id` GUC so writes
// satisfy the per-workspace RLS predicate and NOT-NULL workspace_id column
// default inside a shared per-tenant schema.
//
// workspaceID, when non-empty, must parse as a UUID; an empty value leaves
// the GUC unset (the single-tenant-per-schema legacy callers that don't yet
// carry a workspace stay working, relying on the schema + app.tenant_id
// boundary alone). schemaName must match ^tenant_[0-9a-f]{32}$.
func OpenPostgresInSchemaWithWorkspace(ctx context.Context, dsn, schemaName, tenantID, workspaceID string) (*Store, error) {
	if err := validateSchemaName(schemaName); err != nil {
		return nil, err
	}
	return openPG(ctx, dsn, tenantID, workspaceID, schemaName)
}

// openPG is the shared implementation behind OpenPostgres,
// OpenPostgresInSchema, and OpenPostgresInSchemaWithWorkspace. schemaName == ""
// means "use the connection default search_path"; non-empty pins search_path =
// <schema>, public after creating the schema if missing. workspaceID == ""
// leaves the app.workspace_id GUC unset.
func openPG(ctx context.Context, dsn, tenantID, workspaceID, schemaName string) (*Store, error) {
	if _, err := uuid.Parse(tenantID); err != nil {
		return nil, fmt.Errorf("tenant_id must be a UUID: %w", err)
	}
	if workspaceID != "" {
		if _, err := uuid.Parse(workspaceID); err != nil {
			return nil, fmt.Errorf("workspace_id must be a UUID: %w", err)
		}
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg dsn: %w", err)
	}

	// RDS IAM auth (DISCO_PG_IAM_AUTH): when enabled, beforeConnect mints a
	// fresh IAM token as the password before every physical dial; nil when
	// off (DSN used verbatim — password or local trust).
	beforeConnect, err := iamBeforeConnect(ctx)
	if err != nil {
		return nil, err
	}

	// The schema must exist before any AfterConnect-bearing conn runs (AfterConnect
	// sets search_path to it). Use a one-shot pgx conn (no AfterConnect) to create
	// it only when absent, then close it and open the real pool. Creating only when
	// absent keeps the common path privilege-free: a least-privilege role opening a
	// schema that has already been provisioned needs no CREATE-on-database right.
	if schemaName != "" {
		if err := bootstrapSchema(ctx, cfg, schemaName, beforeConnect); err != nil {
			return nil, err
		}
	}

	// Custom GUCs (`app.tenant_id`) cannot be set via RuntimeParams — PG
	// rejects unknown startup params. AfterConnect runs once per physical
	// connection, before any handle is returned to database/sql, so the
	// session-scoped GUC and search_path are sticky for every subsequent
	// query through that conn. tenantID and workspaceID are UUID-validated
	// above and schemaName is regex-validated, so inlining is safe — PgConn
	// has no parameterised Exec on the simple-query path.
	cfg.AfterConnect = afterConnect(tenantID, workspaceID, schemaName)

	// With IAM auth the pool opens through a connector whose BeforeConnect
	// mints a fresh token per physical dial; otherwise the plain stdlib pool.
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
// positive integer; otherwise the pool keeps the database/sql defaults
// (unbounded open connections, no lifetime), so a standalone CLI is unaffected.
// A SaaS deployment that runs many scanner tasks against a shared RDS sets it to
// keep each task's footprint small. MaxIdleConns is held at min(n, 2) and the
// lifetime/idle-time bounds let idle connections drain back to the server.
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

// bootstrapSchema opens a one-shot pgx conn and creates schemaName only when it
// does not already exist, then closes. Must run BEFORE cfg.AfterConnect is
// assigned — otherwise the conn would itself try to SET search_path against the
// missing schema. The existence probe (to_regnamespace) is a privilege-free
// catalog lookup, so a least-privilege role connecting to an already-provisioned
// schema issues no DDL and needs no CREATE-on-database privilege; only first-time
// provisioning (by a privileged role) actually runs CREATE SCHEMA.
func bootstrapSchema(ctx context.Context, cfg *pgx.ConnConfig, schemaName string, beforeConnect func(context.Context, *pgx.ConnConfig) error) error {
	// Mint the IAM token on a copy so the shared cfg (reused by the pool,
	// which re-mints per dial) is left untouched. No-op when IAM is off.
	cc := cfg.Copy()
	if beforeConnect != nil {
		if err := beforeConnect(ctx, cc); err != nil {
			return fmt.Errorf("bootstrap iam before connect: %w", err)
		}
	}
	conn, err := pgx.ConnectConfig(ctx, cc)
	if err != nil {
		return fmt.Errorf("bootstrap conn: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regnamespace($1) IS NOT NULL", schemaName).Scan(&exists); err != nil {
		return fmt.Errorf("probe schema %q: %w", schemaName, err)
	}
	if exists {
		return nil
	}
	if _, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgQuoteIdent(schemaName)); err != nil {
		return fmt.Errorf("create schema %q: %w", schemaName, err)
	}
	return nil
}

// afterConnect builds the per-physical-conn setup hook. Pins search_path
// (when schemaName is non-empty) and sets the app.tenant_id GUC, plus the
// app.workspace_id GUC when workspaceID is non-empty. Order matters:
// search_path first so the GUC SELECTs run in the intended schema scope
// (set_config is schema-agnostic but consistency aids debugging).
//
// app.workspace_id is left unset when workspaceID == "" so single-tenant
// callers that predate the per-workspace discriminator keep working — the
// workspace_id column default (current_setting('app.workspace_id')) then
// raises only on writes, which those callers don't perform against the
// workspace-isolated tables.
func afterConnect(tenantID, workspaceID, schemaName string) func(ctx context.Context, c *pgconn.PgConn) error {
	return func(ctx context.Context, c *pgconn.PgConn) error {
		if schemaName != "" {
			sql := "SET search_path = " + pgQuoteIdent(schemaName) + ", public"
			mrr := c.Exec(ctx, sql)
			if _, err := mrr.ReadAll(); err != nil {
				return fmt.Errorf("set search_path: %w", err)
			}
		}
		mrr := c.Exec(ctx, "SELECT set_config('app.tenant_id', '"+tenantID+"', false)")
		if _, err := mrr.ReadAll(); err != nil {
			return fmt.Errorf("set tenant_id GUC: %w", err)
		}
		if workspaceID != "" {
			mrr := c.Exec(ctx, "SELECT set_config('app.workspace_id', '"+workspaceID+"', false)")
			if _, err := mrr.ReadAll(); err != nil {
				return fmt.Errorf("set workspace_id GUC: %w", err)
			}
		}
		return nil
	}
}
