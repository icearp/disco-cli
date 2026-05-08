# Plan — SaaS Prereqs (L2 Postgres store + L3 `disco serve`)

Closed-source. `_paid.md` suffix → excluded from OSS mirror via `scripts/oss-sync.sh`.

This plan is the disco-upstream-side work needed before the separate `disco-saas` repo (Go + templ + HTMX, in `~/Projects/codeberg.org/icearp/disco-saas/`) can begin. Two roadmap items become real here:

- **L2 — pluggable store backend**: Postgres driver alongside the existing SQLite store, behind `//go:build paid`.
- **L3 — `disco serve`**: read-only REST + gRPC server over the store, behind `//go:build paid`, license-gated.

Both must satisfy the existing repo invariants in `CLAUDE.md`:
- `CGO_ENABLED=0` always — pick a pure-Go Postgres driver.
- OSS build (default tags) must not link any Postgres or HTTP-server dep added by this work. Verify with `go list -deps . | grep <module>` (empty) and `go list -tags paid -deps . | grep <module>` (non-empty).
- `make oss-sync` must continue to produce a clean OSS mirror.
- `go test ./...` AND `go test -tags paid ./...` both green.

Companion plan for the SaaS app itself: `~/.claude/plans/ask-me-each-question-dazzling-stonebraker.md` (the SaaS repo consumes the artifacts produced here as a typed Go client).

---

## Scope

### In scope
1. Refactor `internal/store` to expose a backend-agnostic interface for the **read path** (resources, relationships, graph traversal, scans, findings) used by `cmd/list`, `cmd/graph`, `cmd/scans`, `cmd/check`, `cmd/findings_paid.go`.
2. Add Postgres backend implementation in `internal/store/postgres_paid.go` plus paid-only migrations (`migrations/*_paid.sql` Postgres-flavored variants).
3. Add `disco serve` subcommand (`cmd/serve_paid.go`) — read-only HTTP + gRPC.
4. Add a typed Go client for the HTTP API (`internal/serve/client_paid.go`) that the SaaS imports.
5. License gate every paid command entrypoint via existing `license.Require()`.
6. Tests for both backends; CI runs both `paid` and OSS test suites.

### Out of scope (explicitly)
- Any **write** path on Postgres for non-scan callers. Scans still run only on SQLite locally; the SaaS scan worker does its own write-path migration in a follow-up. `disco serve` is read-only.
- The SaaS web UI itself — separate repo.
- Changes to scanner / resolver code in `internal/providers/...`.
- Authentication on `disco serve` beyond a static bearer-token gate (sufficient for v1; SaaS sits behind it on a private network).
- Stripe billing, magic-link auth, multi-tenant orchestration — all live in the SaaS repo.

---

## Why an interface, not just a second `Open(driver=...)`

Today `internal/store/store.go` is a concrete struct holding `*sqlx.DB`. Most call sites either invoke methods directly on `*store.Store` or pass it around. Pure-driver-swap (using `sqlx` for both backends) is tempting but breaks on:

- modernc/sqlite-specific quirks already baked in: `INSERT OR IGNORE` semantics, `json_extract()`, `splitStatements` migration runner. Postgres needs `ON CONFLICT DO NOTHING`, `jsonb`, native `pgx` migrations.
- File-perm chmod and `:memory:` handling are SQLite-only; must no-op on PG.
- The closure table + relationship-FK pattern documented in `internal/store/CLAUDE.md` ("modernc/sqlite FK + INSERT OR IGNORE asymmetry") differs on PG — PG enforces FKs identically for both `INSERT ... ON CONFLICT DO NOTHING` and a plain insert, so the explicit `EXISTS` pre-check stops being necessary on PG (but stays harmless).

Cleanest split: introduce a **read-path** interface used by `disco serve` and OSS read-only commands, and let scans keep calling the concrete SQLite struct. Avoids touching every write site.

---

## File layout (disco-upstream)

```
internal/store/
  backend_paid.go               (NEW) ReadBackend interface — methods needed by L3 + read CLI
  postgres_paid.go              (NEW) ReadBackend impl over pgx; opens via DISCO_PG_DSN
  postgres_paid_test.go         (NEW) integration tests, gated on env var
  migrations/
    pg/                         (NEW) Postgres-targeted migration files
      001_init_paid.sql
      002_scan_checkpoints_paid.sql
      003_findings_paid.sql
      004_rls_paid.sql          tenant_id columns + RLS policies; SaaS sets app.tenant_id per request
  sqlite_backend_paid.go        (NEW) thin adapter making *Store satisfy ReadBackend (no logic dup)

internal/serve/                 (NEW package, all files //go:build paid)
  server_paid.go                HTTP handlers (chi or net/http) + gRPC server
  routes_paid.go                Route table; wires handlers to ReadBackend
  proto/                        gRPC + protobuf defs (only checked in if a buf workflow lands; otherwise skip gRPC for v1, ship REST only)
  client_paid.go                Typed Go client over REST; consumed by disco-saas
  client_paid_test.go           Round-trip tests against an in-process server
  server_paid_test.go           Handler tests with both backends (matrix)

cmd/
  serve_paid.go                 (NEW) cobra subcommand `disco serve`; license.Require()
  serve_paid_test.go            (NEW) flag parsing + smoke

internal/license/
  (no changes — existing license.Require() pattern)

PLAN_saas_prereqs_paid.md       this file
```

OSS-sync exclusion: every new file matches `*_paid.go` / `*_paid.sql` / `*_paid.md` / lives under `migrations/pg/` (parent dir contains paid SQL only — add an exclusion rule if `oss-sync.sh` does not already strip it; check `scripts/oss-sync.sh` first and extend if needed). Must verify after first paid commit:

```
make oss-sync && grep -r "postgres\|pgx\|disco serve" oss-mirror-tree/ # expect nothing
```

---

## Phase 1 — `ReadBackend` interface (Week 1)

### Step 1.1 — Inventory read call sites
Grep what touches `*store.Store` in read-only contexts:

```
rg -n 'st\.(Resources|Relationships|GraphWalk|Scans|Diff|FindingRuns|FindingsList|HierarchyDescendants|HierarchyAncestors)' cmd/ internal/
```

Catalogue the method set. Expected core verbs (cross-check against actual code):

- `Resources(ctx, ResourceFilter) ([]*Resource, error)`
- `ResourceByID(ctx, id) (*Resource, error)`
- `Relationships(ctx, RelFilter) ([]*Relationship, error)`
- `GraphWalk(ctx, seed, kinds, depth, dir) (*Graph, error)`  ← used by `cmd/graph`
- `HierarchyDescendants(ctx, id) ([]string, error)`
- `Scans(ctx) ([]*ScanRun, error)`
- `ScanByID(ctx, id) (*ScanRun, error)`
- `Diff(ctx, scanA, scanB string, filter DiffFilter) (*DiffResult, error)`
- `FindingRuns(ctx) ([]*FindingRun, error)` (paid)
- `FindingsList(ctx, FindingsFilter) ([]*Finding, error)` (paid)

### Step 1.2 — Define `internal/store/backend_paid.go`

```go
//go:build paid

package store

import "context"

// ReadBackend is the read-only surface consumed by paid services
// (disco serve, SaaS web app) and by OSS read commands when they are
// pointed at a non-SQLite store. Concrete *Store satisfies it via
// sqlite_backend_paid.go.
type ReadBackend interface {
    Resources(ctx context.Context, filter ResourceFilter) ([]*Resource, error)
    ResourceByID(ctx context.Context, id string) (*Resource, error)
    Relationships(ctx context.Context, filter RelFilter) ([]*Relationship, error)
    GraphWalk(ctx context.Context, seed string, kinds []string, depth int, dir Direction) (*Graph, error)
    HierarchyDescendants(ctx context.Context, id string) ([]string, error)
    Scans(ctx context.Context) ([]*ScanRun, error)
    ScanByID(ctx context.Context, id string) (*ScanRun, error)
    Diff(ctx context.Context, a, b string, filter DiffFilter) (*DiffResult, error)
    FindingRuns(ctx context.Context) ([]*FindingRun, error)
    FindingsList(ctx context.Context, filter FindingsFilter) ([]*Finding, error)
    Close() error
}
```

Define the filter / result structs in this same file (or re-export the existing ones from store.go) so both impls reference the same shapes.

### Step 1.3 — `sqlite_backend_paid.go`
Adapter so `*Store` (the existing SQLite struct) satisfies `ReadBackend`. If method signatures already match, this is a single `var _ ReadBackend = (*Store)(nil)` assertion. Otherwise thin shims that translate.

### Step 1.4 — Tests
`backend_test.go` (NEW, paid build) that runs the same suite against both backends via a table-driven harness:

```go
backends := map[string]func(t *testing.T) ReadBackend{
    "sqlite": newSQLiteTestBackend,
    "pg":     newPgTestBackend,  // skip if DISCO_PG_TEST_DSN unset
}
```

---

## Phase 2 — Postgres backend (Weeks 1–2)

### Step 2.1 — Pick driver
`github.com/jackc/pgx/v5` + `pgx/v5/stdlib` for `database/sql` compat with sqlx. Pure-Go, CGO-free. Verify post-add:

```
go list -deps . | grep pgx                # OSS build: must be empty
go list -tags paid -deps . | grep pgx     # paid build: present
```

If anything in the OSS path imports pgx, it's a bug — every importer must carry `//go:build paid`.

### Step 2.2 — Migrations
Translate the existing SQLite migrations to Postgres equivalents:

- `INTEGER PRIMARY KEY` → `BIGSERIAL` or `BIGINT GENERATED ALWAYS AS IDENTITY`.
- `TEXT` 32-char IDs → `CHAR(32)` or `TEXT` (TEXT is fine; PG indexes both efficiently).
- `json_extract(col, '$.path')` → `col->'path'` / `col->>'path'`. Document the mapping in `internal/store/CLAUDE.md` once impl is stable.
- `INSERT OR IGNORE` → `INSERT ... ON CONFLICT DO NOTHING`.
- Foreign keys identical syntax.
- `PRAGMA foreign_keys = ON` → no-op (PG enforces by default).
- Add `tenant_id UUID NOT NULL DEFAULT current_setting('app.tenant_id')::uuid` to every user-data row (resources, relationships, hierarchy_closure, scans, scan_checkpoints, findings, check_runs). Also add per-table RLS policy `USING (tenant_id = current_setting('app.tenant_id')::uuid)`.

Migration runner: existing SQLite runner (`migrate.go`, semicolon-split) can be reused if migration SQL is single-statement-per-line clean. Safer to use `golang-migrate/migrate` v4 with the file source over pgx — but adds a dep. Recommended: hand-roll a parallel runner in `postgres_paid.go` using `pgx`'s built-in batch — keeps deps minimal (CLAUDE.md rule 7). 200 LOC.

Migrations live in `internal/store/migrations/pg/*.sql`, embedded via `//go:embed migrations/pg/*.sql`.

### Step 2.3 — Implement `ReadBackend` over pgx
- Use `squirrel` with `sq.StatementBuilder.PlaceholderFormat(sq.Dollar)` for PG-style `$1` params. The existing SQLite code uses `?` — keep the same query construction with a per-backend placeholder builder.
- Walk-style queries (`GraphWalk` BFS) — current impl in `internal/store/graph.go` is in-memory after a single edge fetch; reuse the algorithm verbatim once the edge fetch is portable.
- Closure-table descendant query — port directly; SQL is standard.

### Step 2.4 — Open / DSN
```go
//go:build paid

func OpenPostgres(ctx context.Context, dsn string) (*PGBackend, error) {
    pool, err := pgxpool.New(ctx, dsn)
    ...
    if err := migratePG(ctx, pool); err != nil { ... }
    return &PGBackend{pool: pool}, nil
}
```

DSN from `DISCO_PG_DSN` env or `--pg-dsn` flag on `disco serve`.

### Step 2.5 — Tests
- Local Postgres via `dockertest` (preferred, ephemeral) **OR** require `DISCO_PG_TEST_DSN` env in CI; skip otherwise. Pick the lower-dep option; dockertest pulls a lot.
- Recommendation: gate on `DISCO_PG_TEST_DSN` env, run a `services: postgres` block in CI. Avoids dockertest dep in module graph. `go test -tags paid -run TestPG ./internal/store/...`.
- Tests must verify RLS: connect as the `app_user` role, set `app.tenant_id`, INSERT for tenant A, SET to tenant B, SELECT returns zero rows.

---

## Phase 3 — `disco serve` (Week 2)

### Step 3.1 — Subcommand skeleton
`cmd/serve_paid.go`, modeled on `cmd/diff_paid.go`:

```go
//go:build paid

func init() { rootCmd.AddCommand(serveCmd) }

var serveCmd = &cobra.Command{
    Use:   "serve",
    Short: "Read-only HTTP/gRPC server over the disco store (paid).",
    RunE: func(cmd *cobra.Command, args []string) error {
        if err := license.Require(); err != nil { return err }
        ...
    },
}
```

Flags:
- `--listen` (default `127.0.0.1:7777`) — bind addr.
- `--auth-token` — required static bearer token; refuse to start if empty.
- `--pg-dsn` — Postgres DSN; if empty, fall back to local SQLite (`--db` flag inherited from root). The dual-backend path is what makes the SaaS-vs-local-eval split work.
- `--read-timeout`, `--write-timeout`, `--shutdown-grace`.

### Step 3.2 — Routes (REST v1)
Path prefix: `/v1`. JSON only. Bearer-token middleware on every route.

```
GET  /v1/healthz                                     liveness, no auth
GET  /v1/resources?provider=&type=&region=&account=&tag.k=v&since=&limit=&cursor=
GET  /v1/resources/{id}
GET  /v1/relationships?from=&to=&kind=
GET  /v1/graph/blast/{id}?depth=&kinds=&dir=
GET  /v1/graph/path?from=&to=&depth=
GET  /v1/hierarchy/{id}/descendants
GET  /v1/scans
GET  /v1/scans/{id}
GET  /v1/diff?a=&b=&type=&kind=
GET  /v1/findings/runs
GET  /v1/findings?run=&severity=&rule=&resource=
```

Pagination: opaque cursor (base64-encoded `(last_id, last_sort_key)` tuple). Limit defaults 100, cap 1000. List responses:

```json
{ "items": [...], "next_cursor": "..." }
```

Error envelope:

```json
{ "error": { "code": "not_found", "message": "...", "details": {} } }
```

### Step 3.3 — Handler implementation
`internal/serve/server_paid.go` constructs an `http.Handler` from a `ReadBackend`. Use stdlib `net/http` + `http.ServeMux` (Go 1.22+ pattern matching) — avoid pulling chi/gorilla unless routes get complex.

```go
//go:build paid

func NewServer(b store.ReadBackend, token string) http.Handler {
    mux := http.NewServeMux()
    mux.HandleFunc("GET /v1/resources", listResources(b))
    mux.HandleFunc("GET /v1/resources/{id}", getResource(b))
    ...
    return authMiddleware(token, mux)
}
```

Per-request RLS context (PG only): set `app.tenant_id` per request *only if* the token carries a `tenant` claim (deferred — v1 server is single-tenant; the SaaS sets the tenant context in its own DB connection pool, not via the `disco serve` HTTP layer).

### Step 3.4 — Typed Go client (`internal/serve/client_paid.go`)
Used by the SaaS app. Generates / hand-codes typed methods that mirror the routes. Hand-code for v1 — ~300 LOC:

```go
//go:build paid

type Client struct {
    base   string
    token  string
    client *http.Client
}

func New(base, token string) *Client { ... }

func (c *Client) Resources(ctx context.Context, f Filter) ([]*store.Resource, string, error) // items, next_cursor
func (c *Client) ResourceByID(ctx context.Context, id string) (*store.Resource, error)
func (c *Client) GraphBlast(ctx context.Context, id string, opts BlastOpts) (*store.Graph, error)
// ...one per route
```

Returns shapes from `internal/store` directly so SaaS deserializes into the same Go types disco-upstream defines. Do not re-define `Resource` / `Relationship` shapes in the SaaS repo.

### Step 3.5 — gRPC (defer, optional)
Skip gRPC for v1 unless trivial. The SaaS doesn't need it; REST is sufficient. Note in `ROADMAP_paid.md` as a follow-up.

### Step 3.6 — Tests
- `server_paid_test.go`: spin up `httptest.Server` over an in-memory SQLite backend, hit each route, verify response shapes and error envelopes.
- `client_paid_test.go`: round-trip the client against the same `httptest.Server`.
- Auth tests: missing / wrong token → 401.
- Pagination test: 250 resources, limit 100, walk cursor twice, expect 250 distinct rows.

---

## Phase 4 — Wiring + verification (Week 2)

### Step 4.1 — Build / lint
- `make build` (OSS): green.
- `make build-paid`: green.
- `golangci-lint run --max-issues-per-linter 0 --max-same-issues 0`: green for both tag sets.
- `gofmt -w .` before commit.

### Step 4.2 — Dep leak verification

```
go list -deps .            | grep -Ei 'pgx|pgxpool'  # expect empty
go list -tags paid -deps . | grep -Ei 'pgx|pgxpool'  # expect non-empty
```

### Step 4.3 — OSS sync rehearsal

```
./scripts/oss-sync.sh --dry-run
```

Confirm none of the new `_paid.*` files appear in the would-sync list. If `migrations/pg/` is not stripped, extend `oss-sync.sh` exclusion globs.

### Step 4.4 — Smoke test against Postgres locally

```
docker run --rm -d -e POSTGRES_PASSWORD=test -p 5432:5432 postgres:16
DISCO_PG_DSN=postgres://postgres:test@localhost:5432/postgres ./disco serve \
    --auth-token devtoken --listen 127.0.0.1:7777 &
curl -H 'Authorization: Bearer devtoken' http://127.0.0.1:7777/v1/healthz
curl -H 'Authorization: Bearer devtoken' http://127.0.0.1:7777/v1/scans
```

(empty results expected on a fresh DB — proves migrations ran + auth works).

### Step 4.5 — Update `ROADMAP_paid.md`
Move L2 + L3 entries from "LATER" to "NEXT" or strike out the "planned" framing and replace with shipped status, mirroring how other shipped paid features are documented in `FEATURES_paid.md`. Add a `FEATURES_paid.md` entry for each.

---

## Phase 5 — Hand-off to SaaS repo

The SaaS repo (`disco-saas/`) consumes:

1. `internal/serve.Client` as a Go module dep — add `replace` directive in SaaS `go.mod` for local dev:
   ```
   replace disco/upstream => ../disco-upstream
   ```
2. `disco serve` deployment unit — built with `make build-paid` and packaged into the SaaS web container image.
3. The `app.tenant_id` RLS contract — SaaS owns setting it per-request on its DB pool. Document the contract in this file once finalized.

---

## Risks / open items for the disco-upstream Claude session to resolve

1. **`squirrel` placeholder format split** — verify a single `Store` instance can support both `?` and `$N` placeholder formats without breaking existing tests. Likely needs per-backend statement builders. Check whether the existing read code uses `sq.StatementBuilder` directly or constructs queries inline.

2. **`json_extract` call sites** — grep for current uses:
   ```
   rg -n 'json_extract' internal/ cmd/
   ```
   Each must be portable to PG `->` / `->>` or moved into a per-backend helper.

3. **`store.UpsertResources` redaction step** — runs only on writes, untouched here. But `ReadBackend` returns `Resource` structs whose `AttributesJSON` was redacted at write time on whichever backend wrote them. Confirm SaaS docs flag that scrubbing is a write-time invariant, not a read-time one.

4. **Migration drift between SQLite and PG** — keep both in lockstep. A new column in `001_init.sql` (SQLite) requires the matching change in `001_init_paid.sql` (PG). No automated check; reviewers must catch. Consider a `make check-migrations` target that diffs column lists.

5. **License footprint** — `disco serve` running with no license (OSS user accidentally building with `-tags paid`) must refuse to start. `license.Require()` in `RunE` covers this; double-check `license_paid.go` does real validation (not the OSS stub).

6. **CLAUDE.md updates** — once L2 lands, append a section to `internal/store/CLAUDE.md` documenting:
   - The `ReadBackend` interface.
   - Where SQLite-isms (`json_extract`, `INSERT OR IGNORE`) are translated.
   - The `app.tenant_id` RLS contract (paid only).
   - Migration parity rule.

7. **Backwards compat** — none of the OSS commands change behavior. Verify by running the existing `cmd/list`, `cmd/graph`, `cmd/diff`, `cmd/check` integration tests against the SQLite path with no changes expected.

---

## Done criteria

- [ ] `ReadBackend` interface defined; `*Store` satisfies it.
- [ ] `pgx`-backed `PGBackend` implements `ReadBackend`; round-trips data identical to SQLite for a curated dataset.
- [ ] `internal/store/migrations/pg/*.sql` mirrors the SQLite migration set, plus tenant-scoping + RLS policies.
- [ ] `disco serve` (`cmd/serve_paid.go`) starts, auth-gates, serves all listed routes against either backend.
- [ ] `internal/serve.Client` typed Go client passes round-trip tests.
- [ ] `go test ./...` and `go test -tags paid ./...` both green.
- [ ] OSS dep graph free of `pgx`, `serve` packages, server-side deps.
- [ ] `make oss-sync --dry-run` does not list any `_paid` artifact.
- [ ] `FEATURES_paid.md` updated; `ROADMAP_paid.md` L2/L3 entries advanced.
- [ ] Manual smoke against a local Postgres confirms migrations + auth + a non-empty resource list query.

Estimated total: ~2 weeks for a focused single contributor familiar with the codebase. Add a buffer week if integration tests surface migration drift.
