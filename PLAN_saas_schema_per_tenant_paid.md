# Plan — SaaS schema-per-tenant follow-ups

Closed-source. `_paid.md` suffix → excluded from OSS sync.

The SaaS web app (`disco-saas/`) chose **schema-per-tenant + RLS belt-and-suspenders** as its multi-tenant data plane (rev 3 of the SaaS plan; see `~/.claude/plans/ask-me-each-question-dazzling-stonebraker.md`). Each workspace gets its own PG schema named `tenant_<32-hex>`; upstream's full PG migration set runs unchanged in every per-tenant schema, including `005_tenant_id_rls.sql` (RLS stays on as a second layer).

Three small additions to the upstream paid surface unblock this. All three are localised, low-risk, and consistent with the existing patterns in `store/postgres_paid.go`. Estimated total: ~1 day for someone familiar with the package.

If any item turns out to be more invasive than expected, the SaaS has a fallback (per-tenant pgxpool with `AfterConnect` setting `search_path` + `app.tenant_id`) — but that path multiplies open connections by tenant count and we'd rather avoid it. Aim to land all three.

---

## Item 1 — `store.OpenPostgresInSchema` (or honour `search_path` in DSN)

### What

The provisioner in the SaaS app needs to migrate a freshly-created PG schema to upstream's full schema definition. Today `store.OpenPostgres(ctx, dsn, tenantID)` opens a pool, runs migrations, returns `*Store`. We need the same flow but targeted at a named schema instead of the search-path default.

### Two acceptable shapes

**Option A — new constructor (preferred, explicit):**

```go
// store/postgres_paid.go

// OpenPostgresInSchema opens a Postgres-backed Store whose connections have
// search_path pinned to schemaName, then "public". The schema is created if
// it does not exist. Migrations run within that schema.
//
// Use case: SaaS provisioner creating a per-tenant schema. Single-tenant
// container or process; tenantID still pins the RLS GUC.
func OpenPostgresInSchema(ctx context.Context, dsn, schemaName, tenantID string) (*Store, error)
```

Implementation: identical to `OpenPostgres` but the `pgxpool.Config.AfterConnect` hook also runs `CREATE SCHEMA IF NOT EXISTS <name>` (once, idempotent) and `SET search_path = <name>, public` before the existing `set_config('app.tenant_id', ...)` call.

Validate `schemaName` against `^tenant_[0-9a-f]{32}$` (or accept the SaaS-side validation as canonical and panic on regex miss). Identifiers can't be parameter-bound, so guard against injection at the boundary.

**Option B — `OpenPostgres` honours `?search_path=` in the DSN.**

If `pgx` already passes `search_path` from the DSN to the connection (it does, via `RuntimeParams`), then the only addition needed is `CREATE SCHEMA IF NOT EXISTS` before the migration step in `OpenPostgres`. Caller controls schema by including `search_path` in the DSN.

Caveat: DSN-borne `search_path` only sets the runtime parameter at connect time, which RDS Proxy may strip or normalise. Less robust than option A's explicit `SET search_path` in `AfterConnect`. Prefer A.

### Acceptance

- New constructor (or augmented existing one) exposed from `store`.
- Calling it with a fresh schema name creates the schema and runs every migration in `store/migrations/pg/*.sql` against it.
- Calling it with an existing schema is idempotent (migrations skip already-applied versions; `schema_migrations` bookkeeping table lives in the per-tenant schema).
- Test: spin a temp PG, call `OpenPostgresInSchema(ctx, dsn, "tenant_aaaa...", uuid)`, then independently in a second connection `\dn` shows the schema, `\dt tenant_aaaa....*` shows all upstream tables, `005_tenant_id_rls.sql` policies enumerated via `pg_policies`.

---

## Item 2 — Migration runner: `CREATE SCHEMA IF NOT EXISTS` before apply

### What

Today `store/migrate_pg_paid.go` assumes the target schema exists. Provisioner needs the runner to create the schema first when invoked through `OpenPostgresInSchema`.

### Implementation

Single line, run before the migration loop:

```go
// migrate_pg_paid.go (inside the runner, after pool acquired, before SELECT FROM schema_migrations)
if _, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgQuoteIdent(schemaName)); err != nil {
    return fmt.Errorf("create schema: %w", err)
}
```

Use `pgQuoteIdent` (or `pgx`'s identifier-quoting helper) on the schema name. Defence-in-depth even though caller validates format.

The `schema_migrations` bookkeeping table lives **inside** each per-tenant schema, not in `public`. That way each tenant tracks its own migration version independently — needed for the rolling DDL fan-out pattern the SaaS will use when upstream ships a new column.

### Acceptance

- Migration runner creates the schema if missing.
- Bookkeeping table `schema_migrations` is created inside the target schema, not `public`.
- Re-running the runner against the same schema is a no-op.
- Test: with a brand-new DB, `OpenPostgresInSchema` against three different schema names yields three independent `tenant_<hex>.schema_migrations` tables, each at the latest version.

---

## Item 3 — `store.WrapTx(*sqlx.Tx) *store.Store`

### What

The SaaS web app per-request flow wants to:

1. `BEGIN`.
2. `SET LOCAL search_path = tenant_<hex>, public`.
3. `SET LOCAL app.tenant_id = '<uuid>'`.
4. `SET LOCAL app.workspace_id = '<uuid>'`.
5. Run handler queries via `*store.Store` read methods.
6. `COMMIT`.

For step 5 to use upstream's read methods (`ListResources`, `GraphWalk`, `GraphPath`, `GetResource`, `ListScans`, etc.) directly, those methods must run on the same transaction that set the GUCs — otherwise the GUCs don't apply.

Today `*Store` wraps `*sqlx.DB`. Need a tx-bound variant.

### Acceptable shapes

**Option A — direct constructor:**

```go
// store/store.go (untagged file is fine; tx wrapping isn't paid-only)

// WrapTx returns a *Store backed by tx. The returned store does NOT own the
// transaction — caller is responsible for commit/rollback. Close() is a no-op.
func WrapTx(tx *sqlx.Tx) *Store
```

Inside `*Store`, replace direct `s.db.Query(...)` calls with a small `s.querier()` returning either `*sqlx.DB` or `*sqlx.Tx` via the `sqlx.QueryerContext` / `sqlx.ExtContext` interface. Most read methods already use such an abstraction; verify by grepping for `s\.db\.` in `store/*.go` (excluding `_test.go`):

```
rg -n 'st\.db|s\.db' store/
```

Each such site becomes `s.q.` where `q` is the runtime queryer — `*sqlx.DB` for the normal `Open` / `OpenPostgres` path, `*sqlx.Tx` for `WrapTx`.

**Option B — store accepts an `sqlx.ExtContext` directly.**

Refactor `*Store` to hold an `sqlx.ExtContext` instead of `*sqlx.DB`. Both `*sqlx.DB` and `*sqlx.Tx` satisfy that interface. Constructors stay the same; an extra `WrapTx` constructor wraps a tx into a new `*Store`.

Either shape works; pick whichever fits the existing dialect-helper layout (`s.exec` / `s.get` / `s.query` / `s.queryRow` / `s.selectAll` per `store/dialect.go`). The dialect helpers are the natural seam — change them to take an `sqlx.ExtContext` and the `WrapTx` constructor becomes a one-liner.

### Constraints

- **No write methods on a tx-bound store from the SaaS.** Keep this discipline at the documentation layer (godoc on `WrapTx` says "intended for read-only use; write methods may behave unexpectedly inside a caller-owned tx"). The scan-worker container path stays on `OpenPostgres` / `OpenPostgresInSchema`, never touches `WrapTx`.
- `Close()` on a tx-bound store is a no-op (the caller owns the tx lifecycle).
- The `On*` callback fields on `*Store` (OnServiceComplete, OnWarn, OnError, etc.) are unused on the SaaS read path. They stay nil; no behavioural change.

### Acceptance

- `store.WrapTx(*sqlx.Tx) *store.Store` exists and compiles.
- All read methods (the surface listed in `docs/saas-handoff_paid.md` § "How the SaaS team should consume this") work against a tx-bound store identically to a DB-bound store.
- Test: `BEGIN; SET LOCAL search_path = tenant_xxx, public; SET LOCAL app.tenant_id = 'xxx-uuid'; <run ListResources, GraphWalk, GraphPath via WrapTx>; COMMIT` returns the expected rows.
- Test: same flow without `SET LOCAL search_path` returns zero rows from per-tenant tables (proves the GUC is the actual filter).
- `go test ./...` and `go test -tags paid ./...` both green.

---

## Cross-cutting: schema-name validation

The SaaS validates `^tenant_[0-9a-f]{32}$` before passing schema names down. Upstream functions that accept a schema name (`OpenPostgresInSchema`) should re-validate at the boundary — defence-in-depth against caller bugs.

Pattern: a small `validateSchemaName(name string) error` in `store/postgres_paid.go`. Reject anything outside the regex. Document the contract in godoc.

---

## Non-goals

- **No `disco serve`.** Removed and stays removed. SaaS imports `*Store` directly.
- **No write API on `WrapTx`** beyond what already works through the existing `*Store` methods. No new write helpers, no upsert-via-tx convenience methods.
- **No multi-schema migration runner.** The SaaS provisioner iterates schemas itself when fanning out a new upstream migration.
- **No tenant-aware logging.** The SaaS owns request-scoped logging; upstream stays oblivious.

---

## Verification

After all three items land:

```sh
# Build both tag sets
make build && make build-paid

# Tests
go test ./...
go test -tags paid ./...

# Manual smoke: per-tenant migrate
docker run -d --rm -e POSTGRES_PASSWORD=test -p 5432:5432 postgres:16
psql -h localhost -U postgres -d postgres -c "CREATE DATABASE disco_saas_dev"

# In a tiny Go program (or paid test):
#   OpenPostgresInSchema(ctx, dsn, "tenant_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "aaaa...uuid")
#   OpenPostgresInSchema(ctx, dsn, "tenant_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bbbb...uuid")

psql -h localhost -U postgres -d disco_saas_dev -c "\dn"
# Expect: tenant_aaa..., tenant_bbb..., public

psql -h localhost -U postgres -d disco_saas_dev -c "\dt tenant_aaa....*"
# Expect: resources, relationships, hierarchy_closure, scans, scan_checkpoints, schema_migrations, findings, check_runs

psql -h localhost -U postgres -d disco_saas_dev -c "SELECT polname FROM pg_policies WHERE schemaname = 'tenant_aaa...'"
# Expect: RLS policies from 005_tenant_id_rls.sql, one per per-tenant table
```

```sh
# Dep-leak + OSS-clean checks
go list -deps . | grep -Ei 'pgx|jackc'                   # empty
go list -tags paid -deps . | grep -Ei 'pgx|jackc'        # populated
make oss-sync --dry-run | grep -E 'postgres|paid|tenant' # nothing in OSS-bound list
strings dist/disco-linux-amd64 | grep jackc/pgx          # empty (OSS binary)
```

---

## Done criteria

- [ ] Item 1: `OpenPostgresInSchema` (or DSN-borne `search_path`) ships, with godoc + boundary validation.
- [ ] Item 2: Migration runner does `CREATE SCHEMA IF NOT EXISTS`; per-schema `schema_migrations` bookkeeping verified.
- [ ] Item 3: `store.WrapTx(*sqlx.Tx) *store.Store` exists; all read methods work tx-bound.
- [ ] Tests: a paid integration test exercises the full per-tenant provision + tx-bound read flow.
- [ ] Doc: `docs/saas-handoff_paid.md` updated with the new `OpenPostgresInSchema` + `WrapTx` symbols and the schema-per-tenant pattern, replacing the single-shared-schema guidance currently there.
- [ ] OSS dep graph + binary `strings` checks remain clean.

After this lands, the SaaS-side week 1 work is unblocked and `disco-saas/internal/provisioner` + `disco-saas/internal/tenancy` can be implemented against the documented surface.
