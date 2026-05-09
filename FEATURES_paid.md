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

`store/findings_paid.go` + `store/migrations/004_findings_paid.sql` define the storage layer:

- Tables: `check_runs`, `findings` (FK CASCADE).
- Indices on `(run_id, severity)` and `(resource_id)` for the common query shapes.
- Schema additive — empty in OSS builds since no `--persist` flag is registered.

Future paid follow-ups (`disco findings diff`, retention pruning, drift heatmaps, ticket sync) build atop this same schema. See `ROADMAP_paid.md` focus-group follow-ups for the planned surface.

## Postgres backend (L2)

`store/postgres_paid.go` + `store/migrate_pg_paid.go` + `store/migrations/pg/*.sql`. Single `*Store` type covers both SQLite and Postgres — driver-branched dialect bits in `dialect.go` (json_extract vs `->>`, `?` vs `$N` placeholders, etc). `OpenPostgres(ctx, dsn, tenantID)` opens a tenant-pinned pgx-backed `*sqlx.DB`; pgconn `AfterConnect` runs `set_config('app.tenant_id', $1, false)` on every new connection so RLS sees it without per-query plumbing.

PG migrations 001–004 mirror the SQLite set; `005_tenant_id_rls.sql` layers `tenant_id` columns + per-table RLS policies. Hand-rolled migration runner mirrors `migrate.go:14–111` shape — no golang-migrate dep. `make check-migrations` (`scripts/check-migrations.sh`) guards SQLite ↔ PG column-set parity in CI; PG-only `tenant_id` is allowlisted.

OSS dep graph remains pgx-free: every importer of pgx carries `//go:build paid`. Verified via `go list -deps . | grep -Ei 'pgx|jackc'` (empty) and `go list -tags paid -deps .` (non-empty).

Consumed by:
- `disco serve` (L3) for scan persistence on Fargate workers.
- `disco-saas` Go app for direct read access against the same RDS Proxy + tenant pinning contract.

## Scan worker deploy — ECS RunTask command override (supersedes L3 `disco serve`)

`disco serve` (HTTP API for triggering scans) shipped briefly and was removed 2026-05-08. In the locked-in deploy shape — Lambda → ECS RunTask → fresh Fargate container per scan, env-injected `DISCO_PG_DSN` + `DISCO_TENANT_ID`, container exits when scan completes, SaaS reads `scans` table for status — the HTTP layer was solving problems that the architecture doesn't have:

- Single caller (Lambda) makes a typed API redundant.
- Scope known at task-create time eliminates the "send scope after container starts" step where a misroute could happen, removing the attack vector that JWT defended.
- Container is single-use; no multi-request reuse means no need for an HTTP listener.
- Async semantics already provided by Fargate task lifecycle + scans-table polling.

Replaced with: Lambda invokes `ecs:RunTask` with `containerOverrides.command = ["scan", "<provider>", "--regions", ...]`. Container `ENTRYPOINT` is `/disco`. Scan runs via the existing CLI (`cmd/scan.go` + `internal/scanrun`) and writes to PG via the same `*store.Store` the SaaS reads from.

PG dialing on the scan path is gated by env: when `DISCO_PG_DSN` + `DISCO_TENANT_ID` are set, paid `cmd/helpers_paid.go` reassigns the `openWriteDBHook` so write commands (`disco scan`, `disco check --persist`) land in PG with the tenant-pinned RLS contract. Empty env falls back to local SQLite for dev.

Net result: ~700 LOC removed (server, JWT middleware, runner, cred-scrub, JSON envelope, all serve tests). pgx + dockertest stay paid-only via the existing `store/postgres_paid.go` path. SaaS is unaffected — it always read from PG directly, never via `disco serve`.

### Lambda → ECS RunTask invocation contract

Container task definition pins `ENTRYPOINT = ["/disco"]`. Lambda assembles a RunTask call shaped like:

```
ecs:RunTask {
  cluster:        "disco-scan",
  taskDefinition: "disco-scan-worker",
  launchType:     "FARGATE",
  networkConfiguration: { /* private subnets, no public IP */ },
  overrides: {
    containerOverrides: [{
      name:    "disco",
      command: ["scan", "aws", "--regions", "us-east-1,us-west-2"],
      environment: [
        { name: "DISCO_PG_DSN",    value: "<RDS-Proxy-DSN-from-Secrets-Manager>" },
        { name: "DISCO_TENANT_ID", value: "<UUID for the requesting SaaS tenant>" },
      ],
    }],
  },
}
```

Required env on the task:
- `DISCO_PG_DSN` — RDS Proxy DSN; tenant pool lives behind it.
- `DISCO_TENANT_ID` — UUID. Pinned for this container's lifetime; baked into `app.tenant_id` GUC by `OpenPostgres` AfterConnect.

Optional env:
- AWS creds via task IAM role (preferred over static keys).
- Azure: workload identity / DefaultAzureCredential.
- GCP: ADC via Workload Identity Federation.

Command shape mirrors the CLI exactly — the same flags `disco scan aws --regions ...` accepts on a developer's laptop. No special-case `--from-env` flag, no JSON shim.

Lifecycle:
1. RunTask returns immediately with `taskArn`.
2. Container starts, dials PG, runs the scan, persists to `scans` + `resources` + `relationships` rows under the pinned tenant, exits 0 on success or non-zero on failure.
3. SaaS polls the `scans` row keyed by the new task's `scan_id`. The scan_id can either be (a) generated server-side by the scan command and surfaced via container stdout / a sidecar (deferred), or (b) discovered by SaaS via `SELECT id FROM scans WHERE tenant_id = ? AND started_at > <RunTask-time> ORDER BY started_at DESC LIMIT 1`. Path (b) suffices for v1 since one container = one scan = one new row.
4. ECS task transitions STOPPED. SaaS sees the `scans.status` change to `completed` / `failed` / `partial`.

Observability:
- Per-scan stdout/stderr → CloudWatch Logs via the `awslogs` driver on the task definition.
- ECS task lifecycle events → EventBridge → SaaS audit trail (optional).
- The `scans` table is the canonical state. CloudWatch is for human debugging.

IAM scoping:
- Lambda role needs `ecs:RunTask` on the task definition + `iam:PassRole` on the task execution role + the task role.
- Task role needs read on Secrets Manager for the PG DSN secret + cloud-provider read perms for the scan target.
- No JWT secret to provision anywhere.
