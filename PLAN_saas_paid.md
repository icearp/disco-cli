# Plan — SaaS upstream prereqs (consolidated)

Closed-source. `_paid.md` suffix → excluded from OSS mirror via `scripts/oss-sync.sh`.

Supersedes `PLAN_saas_prereqs_paid.md` and `PLAN_saas_schema_per_tenant_paid.md`. Reconciled where they disagree:

- **`disco serve` is out.** Original prereqs plan introduced an HTTP/gRPC read server (L3) plus a typed Go client. The SaaS rev-3 design (schema-per-tenant) instead imports `*store.Store` directly from this repo as a Go module. No HTTP boundary. Drop all serve/client work.
- **No `ReadBackend` interface.** Same reason — without `disco serve`, there is no second consumer of an interface; the concrete `*Store` is the API. Skip the interface refactor.
- **Tenancy model is schema-per-tenant + RLS belt-and-suspenders.** Each workspace lives in its own PG schema (`tenant_<32-hex>`). Upstream's full migration set, including `005_tenant_id_rls.sql`, runs unchanged inside every per-tenant schema. RLS stays on as the second layer.

Companion (SaaS side): `~/.claude/plans/ask-me-each-question-dazzling-stonebraker.md`.

---

## Repo invariants (must hold throughout)

From `CLAUDE.md`:

- `CGO_ENABLED=0` always → pure-Go Postgres driver only (`github.com/jackc/pgx/v5` + `pgx/v5/stdlib`).
- OSS build (default tags) must not link any Postgres dep. Verify:
  ```
  go list -deps .            | grep -Ei 'pgx|jackc'   # empty
  go list -tags paid -deps . | grep -Ei 'pgx|jackc'   # non-empty
  strings dist/disco-linux-amd64 | grep jackc/pgx     # empty (OSS binary)
  ```
- `make oss-sync --dry-run` lists no `_paid.*` artifact and nothing under `migrations/pg/` (extend `scripts/oss-sync.sh` exclusion globs if not already covered).
- `go test ./...` AND `go test -tags paid ./...` both green.
- `gofmt -w .` + `golangci-lint run` clean for both tag sets.

---

## Scope

### In scope
1. **L2 — Postgres read+write store backend** under `//go:build paid`, alongside the existing SQLite store. Pure-Go via `pgx`. Same `*Store` type, dialect-aware.
2. **Postgres migration set** mirroring the SQLite migrations, plus tenant-scoping (`tenant_id` columns) and RLS policies.
3. **Schema-per-tenant constructor** (`OpenPostgresInSchema`) + migration-runner change to `CREATE SCHEMA IF NOT EXISTS` and keep `schema_migrations` bookkeeping inside each per-tenant schema.
4. **Tx-bound store** (`store.WrapTx(*sqlx.Tx) *Store`) so the SaaS web app can run upstream read methods inside a transaction that has already issued `SET LOCAL search_path` + `SET LOCAL app.tenant_id` + `SET LOCAL app.workspace_id`.
5. License-gate any new paid CLI entrypoints via `license.Require()` (none currently planned beyond what already exists).
6. Tests for both backends; CI runs both `paid` and OSS test suites.

### Out of scope (explicitly)
- `disco serve` (HTTP/gRPC), typed Go client, REST routes, bearer-token middleware. Dead.
- A `ReadBackend` interface refactor.
- The SaaS web UI itself — separate repo (`disco-saas/`).
- Provider/scanner/resolver changes (`internal/providers/...`).
- Stripe billing, magic-link auth, multi-tenant orchestration — SaaS repo.
- Multi-schema migration runner. The SaaS provisioner iterates schemas itself when fanning out a new upstream migration.
- Tenant-aware logging in upstream. SaaS owns request-scoped logging.

---

## File layout (disco-upstream)

```
store/
  postgres_paid.go              (NEW) pgx-backed *Store impl; OpenPostgres + OpenPostgresInSchema
  postgres_paid_test.go         (NEW) integration tests, gated on DISCO_PG_TEST_DSN
  migrate_pg_paid.go            (NEW) hand-rolled migration runner over pgx
  dialect.go                    (MODIFIED) dialect-aware querier; sqlx.ExtContext seam for WrapTx
  store.go                      (MODIFIED) WrapTx constructor; *Store holds an ExtContext
  migrations/pg/                (NEW) Postgres-targeted migrations, embedded
    001_init_paid.sql
    002_scan_checkpoints_paid.sql
    003_findings_paid.sql
    004_check_runs_paid.sql
    005_tenant_id_rls_paid.sql  tenant_id columns + RLS policies; SaaS sets app.tenant_id per-tx
PLAN_saas_paid.md               this file
```

OSS-sync exclusion: every new file matches `*_paid.go` / `*_paid.sql` / `*_paid.md` or sits under `migrations/pg/`. Verify after first paid commit:

```
make oss-sync --dry-run | grep -Ei 'postgres|paid|pgx|tenant'    # empty
```

---

## Phase 1 — Postgres backend (Week 1)

### Step 1.1 — Driver + DSN

`github.com/jackc/pgx/v5` + `pgx/v5/stdlib` for `database/sql` compat with sqlx. CGO-free. DSN from `DISCO_PG_DSN` env or `--pg-dsn` flag where relevant. Pool via `pgxpool.New`.

### Step 1.2 — Migrations

Translate the existing SQLite migrations file-for-file into `store/migrations/pg/`:

| SQLite | Postgres |
|---|---|
| `INTEGER PRIMARY KEY` | `BIGINT GENERATED ALWAYS AS IDENTITY` |
| `TEXT` 32-char IDs | `TEXT` (PG indexes both efficiently) |
| `json_extract(col,'$.path')` | `col->'path'` / `col->>'path'` |
| `INSERT OR IGNORE` | `INSERT ... ON CONFLICT DO NOTHING` |
| `PRAGMA foreign_keys = ON` | no-op (PG enforces by default) |
| FK clauses | identical syntax |

Add to every user-data row (resources, relationships, hierarchy_closure, scans, scan_checkpoints, findings, check_runs):

```sql
tenant_id UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid
```

Plus per-table RLS policy `USING (tenant_id = current_setting('app.tenant_id')::uuid)` in `005_tenant_id_rls_paid.sql`.

### Step 1.3 — Migration runner (`migrate_pg_paid.go`)

Hand-rolled to keep deps minimal (CLAUDE.md rule 7). ~200 LOC over `pgx`. Embeds `migrations/pg/*.sql` via `//go:embed`.

Behaviour:

1. Acquire conn from pool.
2. `CREATE SCHEMA IF NOT EXISTS <quoted_schema>` (only when called with a non-default schema; idempotent).
3. `SET search_path = <schema>, public` for the conn.
4. Create `schema_migrations` bookkeeping table **inside the target schema**, not `public`. Each tenant tracks its own migration version → enables rolling DDL fan-out when upstream ships a new column.
5. Apply each unapplied migration in lexicographic order, in its own tx.

Use `pgQuoteIdent` on the schema name. Defence-in-depth alongside caller validation.

### Step 1.4 — Constructors

```go
//go:build paid

// OpenPostgres opens a Postgres-backed Store on the connection's default
// search_path. Migrations run there. tenantID pins the RLS GUC for the pool.
func OpenPostgres(ctx context.Context, dsn, tenantID string) (*Store, error)

// OpenPostgresInSchema opens a Postgres-backed Store whose connections have
// search_path pinned to schemaName, then "public". The schema is created if
// it does not exist. Migrations run within that schema.
//
// Use case: SaaS provisioner creating a per-tenant schema. tenantID still
// pins the RLS GUC.
func OpenPostgresInSchema(ctx context.Context, dsn, schemaName, tenantID string) (*Store, error)
```

`pgxpool.Config.AfterConnect` hook on the in-schema variant runs:

1. `SET search_path = <schema>, public`
2. `SELECT set_config('app.tenant_id', $1, false)`

(in that order; both per-conn so they survive across pool checkouts).

Validate `schemaName` against `^tenant_[0-9a-f]{32}$` at the boundary via a small `validateSchemaName(name string) error` in `postgres_paid.go`. Reject anything else. Document the contract in godoc — identifiers can't be parameter-bound, so this is the only injection guard before `pgQuoteIdent`.

### Step 1.5 — Dialect seam for WrapTx

Today `*Store` wraps `*sqlx.DB` directly. The dialect helpers in `store/dialect.go` (`s.exec` / `s.get` / `s.query` / `s.queryRow` / `s.selectAll`) are the natural seam.

Refactor: `*Store` holds an `sqlx.ExtContext` instead of `*sqlx.DB`. Both `*sqlx.DB` and `*sqlx.Tx` satisfy the interface. Existing constructors (`Open`, `OpenPostgres`, `OpenPostgresInSchema`) populate it with the pool. New constructor:

```go
// WrapTx returns a *Store backed by tx. The returned store does NOT own the
// transaction — caller is responsible for commit/rollback. Close() is a no-op.
//
// Intended for read-only use; write methods may behave unexpectedly inside a
// caller-owned tx and are not exercised on this code path.
func WrapTx(tx *sqlx.Tx) *Store
```

Grep before refactor:

```
rg -n 'st\.db|s\.db' store/
```

Each direct `s.db.` access becomes `s.q.` (or `s.ext.`) where the field is `sqlx.ExtContext`. Most read code already routes through dialect helpers — confirm and extend.

`squirrel` placeholder format split: keep per-backend statement builders. SQLite path uses `?`, PG path uses `$N`. Verify a single `*Store` instance handles only one dialect at a time (it does — dialect is a field on `*Store`, set at construction).

The `On*` callback fields (OnServiceComplete, OnWarn, OnError) are unused on the SaaS read path. They stay nil; no behavioural change.

### Step 1.6 — `json_extract` portability

Grep:

```
rg -n 'json_extract' internal/ cmd/
```

Each call site becomes either:
- a per-backend helper (e.g. `s.dialect.JSONExtract(col, path)` returning the right SQL fragment), or
- moved into a query that already branches on dialect.

Document the mapping in `store/CLAUDE.md` once stable.

### Step 1.7 — Tests (`postgres_paid_test.go`)

- Gate on `DISCO_PG_TEST_DSN`; skip otherwise. CI runs a `services: postgres` block. Avoids dockertest dep in module graph.
- Round-trip data identical to SQLite for a curated dataset.
- RLS test: connect, set `app.tenant_id` to A, INSERT; SET to B, SELECT returns zero rows.
- Schema-per-tenant test: with a brand-new DB, call `OpenPostgresInSchema` against three different schema names. Verify three independent `tenant_<hex>.schema_migrations` tables, each at the latest version, plus expected tables and RLS policies in each.
- Tx-bound test: `BEGIN; SET LOCAL search_path = tenant_xxx, public; SET LOCAL app.tenant_id = '<uuid>'; <run ListResources, GraphWalk, GraphPath via WrapTx>; COMMIT` returns expected rows. Same flow without `SET LOCAL search_path` returns zero rows from per-tenant tables (proves the GUC is the actual filter).

---

## Phase 2 — Wiring + verification (Week 2)

### Step 2.1 — Build / lint
- `make build` (OSS): green.
- `make build-paid`: green.
- `golangci-lint run --max-issues-per-linter 0 --max-same-issues 0`: green for both tag sets.
- `gofmt -w .` before commit.

### Step 2.2 — Dep leak verification

```
go list -deps .            | grep -Ei 'pgx|jackc'   # empty
go list -tags paid -deps . | grep -Ei 'pgx|jackc'   # non-empty
strings dist/disco-linux-amd64 | grep jackc/pgx     # empty
```

### Step 2.3 — OSS sync rehearsal

```
./scripts/oss-sync.sh --dry-run | grep -Ei 'postgres|paid|tenant'   # empty
```

If `migrations/pg/` not stripped, extend `scripts/oss-sync.sh` exclusion globs.

### Step 2.4 — Smoke test against Postgres locally

```sh
docker run --rm -d -e POSTGRES_PASSWORD=test -p 5432:5432 postgres:16
psql -h localhost -U postgres -d postgres -c "CREATE DATABASE disco_saas_dev"

# Tiny Go program (or paid test):
#   OpenPostgresInSchema(ctx, dsn, "tenant_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "<uuid>")
#   OpenPostgresInSchema(ctx, dsn, "tenant_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "<uuid>")

psql -h localhost -U postgres -d disco_saas_dev -c "\dn"
# Expect: tenant_aaa..., tenant_bbb..., public

psql -h localhost -U postgres -d disco_saas_dev -c "\dt tenant_aaa....*"
# Expect: resources, relationships, hierarchy_closure, scans, scan_checkpoints,
#         schema_migrations, findings, check_runs

psql -h localhost -U postgres -d disco_saas_dev \
     -c "SELECT polname FROM pg_policies WHERE schemaname = 'tenant_aaa...'"
# Expect: RLS policies from 005_tenant_id_rls_paid.sql, one per per-tenant table
```

### Step 2.5 — Update roadmap/features
Move L2 from "LATER" to shipped in `ROADMAP_paid.md`. Strike L3 (`disco serve`) entirely or mark "deferred indefinitely — superseded by SaaS direct-import design". Add `FEATURES_paid.md` entry for the PG backend + schema-per-tenant constructor.

### Step 2.6 — Update `store/CLAUDE.md`
Append:
- The PG backend exists and is dialect-aware via `*Store`'s ExtContext.
- Where SQLite-isms (`json_extract`, `INSERT OR IGNORE`) are translated.
- The `app.tenant_id` RLS contract (paid only) and the schema-per-tenant pattern.
- `WrapTx` semantics — read-only, caller owns tx.
- Migration-parity rule: a new column in `001_init.sql` (SQLite) requires the matching change in `001_init_paid.sql` (PG).

---

## Phase 3 — Hand-off to SaaS repo

The SaaS repo (`disco-saas/`) consumes:

1. `store` directly as a Go module dep. Local-dev `replace` directive in SaaS `go.mod`:
   ```
   replace disco/upstream => ../disco-upstream
   ```
2. Per-request flow:
   ```
   BEGIN
   SET LOCAL search_path = tenant_<hex>, public
   SET LOCAL app.tenant_id   = '<uuid>'
   SET LOCAL app.workspace_id = '<uuid>'
   st := store.WrapTx(tx)
   <upstream read methods>
   COMMIT
   ```
3. Provisioner flow: `OpenPostgresInSchema(ctx, dsn, "tenant_<hex>", tenantUUID)` for fresh-tenant migration, then close. The pool only needs to live long enough to migrate.
4. Scan-worker container path: `OpenPostgres` / `OpenPostgresInSchema`. Never `WrapTx`.
5. SaaS owns:
   - Setting GUCs per request.
   - Multi-schema fan-out when upstream ships a new migration.
   - Tenant validation regex (`^tenant_[0-9a-f]{32}$`) before calling upstream — upstream re-validates at the boundary.
6. Document the `app.tenant_id` + `search_path` contract in `docs/saas-handoff_paid.md`, replacing any single-shared-schema guidance.

---

## Risks / open items

1. **`squirrel` placeholder format split** — verify dialect is per-`*Store` and never crosses streams. The dialect field on `*Store` set at construction is the single source of truth.
2. **`json_extract` call sites** — every one must be portable (see Step 1.6). No automated check; reviewer discipline.
3. **`store.UpsertResources` redaction** — runs only on writes, untouched here. Read methods return whatever was scrubbed at write time. Document scrubbing as a write-time invariant in SaaS docs.
4. **Migration drift between SQLite and PG** — keep in lockstep. Consider a `make check-migrations` target that diffs column lists across the two migration sets.
5. **License footprint** — any future paid CLI entrypoint must call `license.Require()` in `RunE`. Confirm `license_paid.go` does real validation, not the OSS stub.
6. **Backwards compat** — none of the OSS commands change behavior. Run existing `cmd/list`, `cmd/graph`, `cmd/diff`, `cmd/check` integration tests against the SQLite path; expect no diffs.
7. **RDS Proxy / search_path normalisation** — explicit `SET search_path` in `AfterConnect` (not DSN-borne `?search_path=`) is the more robust path. Avoid relying on the DSN runtime parameter.
8. **Schema-name injection** — identifiers can't be parameter-bound. `validateSchemaName` regex + `pgQuoteIdent` are both required, not either-or.

---

## Done criteria

- [ ] `OpenPostgres` and `OpenPostgresInSchema` ship in `store/postgres_paid.go`, with godoc + boundary validation.
- [ ] Migration runner does `CREATE SCHEMA IF NOT EXISTS`; per-schema `schema_migrations` bookkeeping verified by integration test.
- [ ] `store/migrations/pg/*.sql` mirrors the SQLite set, plus tenant-scoping + RLS policies in `005_tenant_id_rls_paid.sql`.
- [ ] `store.WrapTx(*sqlx.Tx) *Store` exists; all read methods work tx-bound. `*Store` holds an `sqlx.ExtContext`.
- [ ] Paid integration test exercises full per-tenant provision + tx-bound read flow.
- [ ] `go test ./...` and `go test -tags paid ./...` both green.
- [ ] OSS dep graph free of `pgx`/`jackc`; `strings` on the OSS binary clean.
- [ ] `make oss-sync --dry-run` lists no `_paid` artifact.
- [ ] `FEATURES_paid.md` updated; `ROADMAP_paid.md` L2 advanced, L3 retired.
- [ ] `docs/saas-handoff_paid.md` documents `OpenPostgresInSchema` + `WrapTx` and the schema-per-tenant pattern.
- [ ] Manual smoke against local Postgres confirms migrations + RLS + a non-empty resource list query through `WrapTx`.

Estimated total: ~1.5 weeks for a focused single contributor familiar with the codebase. Add a buffer week if migration drift surfaces during integration tests.
