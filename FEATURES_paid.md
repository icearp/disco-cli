# Disco Paid Features

Shipped, in-tree paid surface. Closed-source — excluded from OSS sync via `scripts/oss-sync.sh` (`*_paid.md` name pattern). Do not reference these items from any OSS-tracked file (commits, CLAUDE.md, README, OSS `FEATURES.md` / `ROADMAP.md`).

Companion to `ROADMAP_paid.md` (forward-looking paid plans).

---

## License gate

`internal/license/license_paid.go` + OSS stub at `internal/license/license.go`. `license.Require()` is the canonical entry-call every paid command runs as the first line of `RunE`. OSS build gets a no-meaning stub that never authorizes; paid build validates the license envelope. Pattern documented in `CLAUDE.md` (OSS / paid split).

## `disco diff <scanA> <scanB>` — drift detection

Paid command in `cmd/diff_paid.go`. Walks two scan timestamps and emits added / removed / changed resources.

Filters:
- `--type` — disco type (`aws:ec2:instance`, etc.)
- `--provider` — `aws|azure|gcp`
- `--kind added|removed|changed` — restrict result class
- `--region`
- `--account`

Pairs with G10 (`disco scan --resume`, partial — checkpoint persistence shipped OSS-side; resumable consumer remains in `ROADMAP_paid.md`) and L6 (continuous mode, planned).

## `disco check --persist` — findings persistence

Paid override of OSS `disco check` via the var-function reassignment pattern (`cmd/check_paid.go` + `cmd/helpers_paid.go` reassign in `init()`; OSS file ships the no-op default). When `--persist` is set, every finding produced by Rego eval lands in the persistent tables defined by migration `004_findings_paid.sql`:

- `check_runs` — one row per `disco check --persist` invocation (timestamp, packs, scope filters).
- `findings` — per-finding rows with severity, rule ID, message, resource ID, run-id FK (CASCADE on run delete).

The schema migration ships paid-only — name pattern `*_paid.sql` excludes from OSS sync. Upstream OSS dev-builds embed and apply it; published OSS mirror never sees it.

## `disco findings list` / `disco findings runs` — read commands

`cmd/findings_paid.go` exposes two read verbs over the persisted store:

- `disco findings runs` — list `check_runs` rows.
- `disco findings list` — list findings, optionally scoped to a run-id. Carries `--since` filter (matches the OSS `--since` shape on `list`).

Both gated behind `license.Require()`.

## Findings persistence schema

`internal/store/findings_paid.go` + `internal/store/migrations/004_findings_paid.sql` define the storage layer:

- Tables: `check_runs`, `findings` (FK CASCADE).
- Indices on `(run_id, severity)` and `(resource_id)` for the common query shapes.
- Schema additive — empty in OSS builds since no `--persist` flag is registered.

Future paid follow-ups (`disco findings diff`, retention pruning, drift heatmaps, ticket sync) build atop this same schema. See `ROADMAP_paid.md` focus-group follow-ups for the planned surface.

## Postgres backend (L2)

`internal/store/postgres_paid.go` + `internal/store/migrate_pg_paid.go` + `internal/store/migrations/pg/*.sql`. Single `*Store` type covers both SQLite and Postgres — driver-branched dialect bits in `dialect.go` (json_extract vs `->>`, `?` vs `$N` placeholders, etc). `OpenPostgres(ctx, dsn, tenantID)` opens a tenant-pinned pgx-backed `*sqlx.DB`; pgconn `AfterConnect` runs `set_config('app.tenant_id', $1, false)` on every new connection so RLS sees it without per-query plumbing.

PG migrations 001–004 mirror the SQLite set; `005_tenant_id_rls.sql` layers `tenant_id` columns + per-table RLS policies. Hand-rolled migration runner mirrors `migrate.go:14–111` shape — no golang-migrate dep. `make check-migrations` (`scripts/check-migrations.sh`) guards SQLite ↔ PG column-set parity in CI; PG-only `tenant_id` is allowlisted.

OSS dep graph remains pgx-free: every importer of pgx carries `//go:build paid`. Verified via `go list -deps . | grep -Ei 'pgx|jackc'` (empty) and `go list -tags paid -deps .` (non-empty).

Consumed by:
- `disco serve` (L3) for scan persistence on Fargate workers.
- `disco-saas` Go app for direct read access against the same RDS Proxy + tenant pinning contract.

## `disco serve` — scan-trigger HTTP API (L3)

`cmd/serve_paid.go` + `internal/serve/*` implement the one-shot Fargate-per-scan API. Two routes:

- `GET /v1/healthz` — liveness, no auth.
- `POST /v1/scans` — submit a scan; returns 202 + `scan_id`. Runs the scan in a background goroutine, persists to PG, then closes a `Done` channel that the cmd-level main loop reads to trigger graceful shutdown + process exit.

Lifecycle: container is one-shot. A second POST while the first scan is in-flight returns 409 `scan_in_progress`. After the scan completes the process exits 0 — ECS RunTask launches a fresh task per scan.

Auth: HS256 JWT. Token MUST carry a `tenant` claim equal to the server's pinned `DISCO_TENANT_ID` env (mismatched tenant → 403 `tenant_mismatch`). Defence in depth against Lambda misroute (token issued for tenant A delivered to a container started for tenant B).

Body scrubbing: scan request bodies that carry credential or server-pinned config keys (`credentials`, `access_key`, `secret_key`, `service_account_json`, `client_secret`, `password`, `api_key`, `bearer_token`, `dsn`, `pg_dsn`, `database_url`, `tenant_id`) are rejected 400 `credentials_in_body_forbidden`. Recursive walker matches case-insensitive at any depth.

Stdlib `net/http` with Go 1.22+ pattern-matched ServeMux — no chi, no router lib, no oapi-codegen. Tiny route count (2) made hand-rolling cheaper than codegen tooling.

Scan orchestration shared with `disco scan` CLI via `internal/scanrun.Allocate` + `Execute` (untagged package). Single source of truth for provider resolution + parallel fan-out + scan row lifecycle.

Spec at `internal/serve/openapi.yaml` is documentation-only — not driven through codegen.

OSS dep graph: pgx, golang-jwt, dockertest are paid-only — verified by build-tag scoping. Every file in `internal/serve/*` carries `//go:build paid`; the entire dir is excluded from `oss-sync.sh` by name pattern.
