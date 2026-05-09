# disco-saas Handoff

Handoff document for the disco-saas team. Summarises what disco-upstream delivered, the architectural decisions behind it, and how to consume the work when building the SaaS web app.

This file is paid-only (`_paid.md` suffix) — excluded from the OSS mirror by `scripts/oss-sync.sh`. Do not reference its contents from any OSS-tracked file.

---

## Executive summary

The disco-upstream side now supports running disco against a shared Postgres backend, multi-tenant via schema-per-tenant + PostgreSQL Row-Level Security (RLS) belt-and-suspenders. The SaaS app consumes this in three ways:

1. **Provisioning** — On workspace creation, SaaS calls `store.OpenPostgresInSchema(ctx, dsn, "tenant_<32-hex>", tenantUUID)` to CREATE the per-tenant schema and run the full upstream migration set inside it (incl. tenant_id columns + RLS policies). One PG schema per workspace; `schema_migrations` bookkeeping lives inside each schema.
2. **Reads** — SaaS opens one shared `*sqlx.DB` against RDS Proxy. Per request: `BEGIN; SET LOCAL search_path = tenant_<hex>, public; SET LOCAL app.tenant_id = '<uuid>'; SET LOCAL app.workspace_id = '<uuid>';` then `store.WrapTx(tx, store.DriverPostgres)` to get a tx-bound `*Store`. Run upstream read methods. `COMMIT`. RDS Proxy still multiplexes (everything is `SET LOCAL`).
3. **Writes (scans)** — SaaS triggers a scan via Lambda → ECS RunTask. Lambda passes the scan scope as a command override; scope-pinned tenant + DSN ride along as task env. Container `ENTRYPOINT = /disco`. The CLI runs the scan, persists to PG, exits when finished. SaaS polls the `scans` table for progress.

There is **no `disco serve` HTTP API**. It shipped briefly and was removed the same day after architecture review concluded the HTTP layer didn't carry weight in this deploy shape.

---

## What disco-upstream delivered

### L2 — Pluggable Postgres backend (paid)

`store/*Store` now opens either SQLite or Postgres depending on which constructor the caller uses. The same struct, the same method surface, the same wire shapes. Driver branching is internal:

- `store.Open(path)` → SQLite (CLI default; local dev).
- `store.OpenPostgres(ctx, dsn, tenantID)` → Postgres, default search_path (Fargate scan workers).
- `store.OpenPostgresInSchema(ctx, dsn, schemaName, tenantID)` → Postgres, search_path pinned to a per-tenant schema (SaaS provisioner).
- `store.WrapTx(tx, store.DriverPostgres)` → tx-bound `*Store`, no pool (SaaS request handlers).

Files of interest:
- `store/postgres_paid.go` — PG constructors, pgxpool config, AfterConnect GUC + search_path hook, schema-name validation.
- `store/migrate_pg_paid.go` — hand-rolled PG migration runner mirroring the SQLite path.
- `store/migrations/pg/*.sql` — PG-flavoured migrations. tenant_id columns + RLS policies live in `001_initial.sql`.
- `store/store.go` — `WrapTx` constructor, `Driver` exported type.
- `store/dialect.go` — `sqlxExt` interface (DB or Tx); driver-branched helpers: placeholder format, `tags ->>` vs `json_extract`, query wrappers (`s.exec` / `s.get` / `s.query` / `s.queryRow` / `s.selectAll`) that auto-Rebind `?` to `$N`.

Package was renamed from `internal/store` to `store` so the disco-saas module can import it across module boundaries (Go's internal-package rule blocked the prior layout).

### L3 — `disco serve` (REMOVED 2026-05-08)

Briefly shipped a 2-route HTTP API (`POST /v1/scans`, `GET /v1/healthz`) inside the Fargate container. Removed after architecture review:

- Single caller (Lambda) made a typed API redundant.
- Scope known at task-create time eliminated the misroute attack vector that JWT defended against — there is no separate "send" step.
- Container is single-use; multi-request listener pulled no weight.
- Async semantics already provided by Fargate task lifecycle + `scans` table polling.

Replaced by an ECS RunTask command-override: Lambda invokes the existing CLI directly. ~700 LOC removed; pattern is simpler, smaller attack surface, and reuses well-tested code.

### Scanrun extraction

`internal/scanrun` (untagged) holds the orchestration core that both the CLI and any future programmatic caller use:

- `Allocate(*store.Store, Request) (*Allocation, error)` — synchronously creates a `pending` scan row.
- `Execute(ctx, *store.Store, *Allocation) error` — runs scanners + finalises.
- `Run(ctx, *store.Store, Request) (scanID, error)` — convenience: Allocate + Execute.
- `RunScanners(ctx, *store.Store, scanID, scanners) (warnings, errors)` — the parallel fan-out core. CLI uses this directly so progress callbacks still fire.

If the SaaS app ever needs to invoke a scan in-process (it shouldn't, but in case), this is the entry point. Today only `cmd/scan.go` (CLI) calls it.

---

## Architecture

### Deploy topology

```
┌────────────┐    HTTP    ┌────────────┐
│ SaaS UI    │──────────► │ API Gateway│
└────────────┘            └─────┬──────┘
                                │
                                ▼
                        ┌───────────────┐
                        │   Lambda      │
                        │ (scan trigger)│
                        └───────┬───────┘
                                │ ecs:RunTask
                                ▼
              ┌───────────────────────────────────┐
              │ ECS Fargate task                  │
              │  ENTRYPOINT /disco                │
              │  command: ["scan","aws", ...]     │
              │  env: DISCO_PG_DSN, DISCO_TENANT_ │
              │       ID                          │
              └────────────────┬──────────────────┘
                               │ pgx (writes)
                               ▼
                       ┌───────────────┐
                       │ RDS Proxy     │◄──── pool (reads) ─── SaaS web app
                       └───────┬───────┘
                               │
                               ▼
                       ┌───────────────┐
                       │ Aurora PG     │
                       │ RLS by tenant │
                       └───────────────┘
```

### Tenant isolation

Every user-data table carries a `tenant_id UUID NOT NULL`. Per-table RLS policies match on `current_setting('app.tenant_id')::uuid`. The GUC is set on each physical connection at conn-establish time:

```go
// store/postgres_paid.go
cfg.AfterConnect = func(ctx context.Context, c *pgconn.PgConn) error {
    mrr := c.Exec(ctx, "SELECT set_config('app.tenant_id', '"+tenantID+"', false)")
    _, err := mrr.ReadAll()
    return err
}
```

Result: every query through that conn sees only rows for the pinned tenant. No application-level filter, no opportunity to forget. Default values on `tenant_id` columns also come from `current_setting('app.tenant_id')` so `INSERT` paths never need to spell tenant_id explicitly.

**SaaS team must replicate this pattern in its own connection pool.** When SaaS opens a pgxpool against RDS Proxy, set `AfterConnect` to call `set_config('app.tenant_id', $1, false)` with the tenant the request is for.

### RDS Proxy session pinning trade-off

`SET app.tenant_id` at conn open (Fargate scan workers, via `OpenPostgres` `AfterConnect`) is session-scoped, which causes RDS Proxy to pin the conn to a single backend session — Proxy stops multiplexing it. For a single-tenant Fargate container, this is fine (every conn serves the same tenant; pinning is the same as not pinning).

The SaaS web app uses `SET LOCAL` inside a per-request transaction instead (see `WrapTx` example above). `SET LOCAL` is reset at COMMIT; the conn returns to the pool with no sticky session state, so RDS Proxy keeps multiplexing.

Don't mix: never call `OpenPostgres` from the SaaS web path (would pin), never call `WrapTx` from a scan-worker container (write methods panic on a tx-bound store).

### Build-tag discipline

Postgres backend and SaaS-specific helpers all carry `//go:build paid`. The OSS mirror published by `scripts/oss-sync.sh` never sees them. Verify with:

```sh
go list -deps . | grep -Ei 'pgx|jackc'                     # empty (OSS clean)
go list -tags paid -deps . | grep -Ei 'pgx|jackc'          # populated
strings <oss-binary> | grep jackc/pgx                      # empty (authoritative)
```

`go list -deps` is advisory because the module graph reflects all build tags. The authoritative check is `strings` against the actual built binary.

---

## How the SaaS team should consume this

### Provisioning a workspace

```go
import "codeberg.org/icearp/disco/store"

// Schema name = "tenant_" + 32-hex (UUID without dashes). Validated upstream.
schema := "tenant_" + strings.ReplaceAll(workspaceUUID, "-", "")
st, err := store.OpenPostgresInSchema(ctx, dsn, schema, workspaceUUID)
if err != nil { return err }
_ = st.Close() // pool only needs to live long enough to migrate
```

Then record `(workspace_id, schema_name)` in `public.tenant_schemas` so the request-path resolver can find it.

### Reading from Postgres (request path)

Open one shared `*sqlx.DB` at startup (no `AfterConnect` GUC — that's set per-tx). Per request:

```go
import "codeberg.org/icearp/disco/store"

tx, err := db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
if err != nil { return err }
defer tx.Rollback()

if _, err := tx.ExecContext(ctx, "SET TRANSACTION READ ONLY"); err != nil { return err }
if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", schema)); err != nil { return err }
if _, err := tx.ExecContext(ctx, "SET LOCAL app.tenant_id = $1", tenantID); err != nil { return err }

st := store.WrapTx(tx, store.DriverPostgres)
resources, err := st.ListResources(store.ResourceFilter{IncludeManaged: true, Limit: 100})
if err != nil { return err }

return tx.Commit()
```

`SET LOCAL` is reset at COMMIT, so RDS Proxy keeps multiplexing. Schema-name interpolation in `SET LOCAL search_path` is unavoidable (identifiers can't be parameter-bound) — validate against `^tenant_[0-9a-f]{32}$` at the boundary.

`WrapTx` is read-only by contract; write methods (`UpsertResources`, `RecordHierarchyBatch`, etc.) panic on a tx-bound store. Writes happen on the scan-worker path via `OpenPostgres`.

The exported `Resource`, `Relationship`, `Scan`, `ScanCheckpoint`, `CheckRun`, `Finding` types serialize as the on-wire JSON shape (snake_case, nested `attributes`/`tags`, RFC3339 timestamps) — same shape the CLI outputs. `MarshalJSON`/`UnmarshalJSON` round-trips byte-stably.

Use the documented method surface — `ListResources`, `GetResource`, `ResolveResource`, `GraphWalk`, `GraphPath`, `GraphAll`, `ListRelationships`, `RelationshipsFrom/To`, `NeighboursOf`, `DescendantsOf`, `DiffScans`, `ListScans`, `GetScan`, paid `ListCheckRuns`/`GetCheckRun`/`ListFindings`. See `store/CLAUDE.md` for the canonical "read every resource" idiom (`GraphAll` page-loops `ListResources`).

### Triggering a scan from Lambda

Lambda assembles an ECS RunTask call:

```python
ecs.run_task(
    cluster="disco-scan",
    taskDefinition="disco-scan-worker",
    launchType="FARGATE",
    networkConfiguration={
        "awsvpcConfiguration": {
            "subnets": [PRIVATE_SUBNETS],
            "securityGroups": [SG_ID],
            "assignPublicIp": "DISABLED",
        }
    },
    overrides={
        "containerOverrides": [{
            "name": "disco",
            "command": ["scan", scan_request["provider"]] + flags_from(scan_request),
            "environment": [
                {"name": "DISCO_PG_DSN",    "value": pg_dsn_from_secrets_manager()},
                {"name": "DISCO_TENANT_ID", "value": tenant_id_for_this_user()},
            ],
        }],
    },
)
```

Container `ENTRYPOINT = /disco`. The `command` overrides the default: `disco scan aws --regions us-east-1,us-west-2`. CLI surface is the contract — every flag `disco scan <provider>` accepts on a developer's laptop is available here (e.g. `--regions`, `--services`, `--profile`, `--skip-globals`).

`disco scan` opens PG via `openWriteDB()` which checks `openWriteDBHook` (assigned in `cmd/helpers_paid.go::init()`); the hook reads `DISCO_PG_DSN` + `DISCO_TENANT_ID` and dials `store.OpenPostgres`.

### Reading scan status

SaaS polls the `scans` table directly. There is no GET status endpoint — the table is the canonical state.

```sql
SELECT id, status, finished_at, error, resource_count
FROM scans
WHERE tenant_id = current_setting('app.tenant_id')::uuid
  AND started_at > $1   -- after Lambda's RunTask call
ORDER BY started_at DESC
LIMIT 1;
```

Status transitions: `running` → `completed` | `failed` | `partial`. RLS guarantees only the requesting tenant's scans are visible.

For a one-container-per-scan deploy, a single LIMIT 1 is fine. If multiple containers per tenant become possible, key on the task ARN: pass it through to the container via env (`DISCO_SCAN_TAG`?) and have the scan command persist it on the row. v1 doesn't need this.

### Tenant ID provenance

The tenant_id UUID must be authoritative — Lambda must validate the SaaS user's session, look up their tenant, and pass that exact UUID. Never accept tenant_id from a request body. Once Lambda decides, `DISCO_TENANT_ID` env on the task is the trust boundary; everything below (RLS, GUC, default values) flows from it.

### Cred handling

Cloud-provider credentials for the scan are NEVER passed via env vars or task overrides:

- AWS: use the Fargate task's IAM role.
- Azure: workload identity / DefaultAzureCredential.
- GCP: ADC via Workload Identity Federation.

The scanner picks them up automatically. Same as the CLI on a laptop with the same ambient creds.

---

## Design decisions and rationale

Decisions made during this work, captured here so the SaaS team can apply the same reasoning to its own build.

### Why no `disco serve`

The original plan included a 2-route HTTP API. We removed it because, in the locked-in deploy shape:

| Problem the HTTP layer solved | Disappears in env-driven model? |
|---|---|
| Multiple consumers need a typed API | Yes — Lambda is sole caller |
| Container needs to be reusable across requests | Yes — one-shot per scan |
| Scope arrives after container starts | Yes — scope known at RunTask time |
| Tenant misroute defense | Yes — no send step to misroute |
| Async submission with poll | Yes — Fargate is already async; SaaS already polls scans |
| Liveness during long-running listener | Yes — process exit is the lifecycle |

Cost paid for nothing: ~700 LOC, JWT secret to manage, readiness race between Lambda and the HTTP listener, body-scrub layer for forbidden cred keys, an OpenAPI spec to keep in sync.

If the SaaS team encounters a similar "wrap a worker in HTTP" temptation, ask: does any of the above table apply? If not, command-override is simpler.

### Why a single `*Store`, not an interface

The original plan considered `ReadBackend` + `WriteBackend` interfaces with SQLite + Postgres impls. Rejected because:

- Single struct + driver flag is fewer files.
- The SaaS app imports `*Store` directly; abstraction would buy nothing for the only consumer.
- Existing test suite doesn't need mocks; `:memory:` SQLite is fast enough.

CLAUDE.md rule 1 (KEEP THINGS SIMPLE) won over architectural symmetry.

### Why hand-rolled migration runner

The original plan called for `golang-migrate/migrate/v4`. Replaced with ~80 LOC mirroring the existing SQLite runner because:

- Pure-Go migration runner already exists in `store/migrate.go`.
- Adding a dep for a tiny well-defined task contradicts CLAUDE.md rule 7 (minimize deps).
- Same `splitStatements` semicolon split + `schema_migrations` bookkeeping table works for both backends.

If migrations grow in complexity (rollbacks, multi-statement DDL with embedded semicolons, parallel migration workers), revisit.

### Why no oapi-codegen

The original plan included oapi-codegen for the HTTP API. Replaced with stdlib `net/http` + Go 1.22 ServeMux pattern matching because the surface was 2 routes. Codegen pays for itself ~10 routes; below that it's a tax.

The HTTP API was then removed entirely, so this decision is moot now — but the heuristic stands for future API work.

### Why JWT defense was unnecessary

The original plan had JWT validation with a `tenant` claim hard-matched against the pinned tenant. Defense against Lambda misroute (token issued for tenant A delivered to a container started for tenant B). With the env-driven model, tenant_id is set at task-create time as part of the task itself. There is no separate "send" step. The misroute attack vector vanishes; JWT defended against nothing.

### Why scope is in the command, not the env

ECS RunTask accepts both env-var and command overrides. Scope (`provider`, `regions`, `services`, etc.) goes in `command` because:

- Reuses the existing CLI flag surface verbatim. No custom env-var parsing logic.
- Keeps env vars limited to per-tenant config (DSN, tenant_id) — a clean separation of "who/where" vs "what".
- Failure modes are familiar: if the command is bad, the container exits with cobra's flag-parse error, same as on a laptop.

---

## Operations

### Logs and observability

- Per-scan stdout/stderr → CloudWatch Logs via the `awslogs` log driver on the task definition. Already structured (cobra prints scan progress to stderr; final summary to stdout).
- ECS task lifecycle events → EventBridge → SaaS audit trail (optional).
- The `scans` table is the canonical state. CloudWatch is for human debugging.

### Failure modes and recovery

- **Container fails to start (image pull, IAM, ENI):** ECS marks task STOPPED with a reason. SaaS sees the `scans` row stay in `pending` forever (because the container never opened the DB). Mitigation: SaaS polls a timeout window; if the row is `pending` after N minutes, mark it `failed` from the SaaS side. Future improvement: have the Lambda watch ECS task lifecycle events and reconcile.
- **Scan fails mid-flight:** scanrun captures errors and finalises the row as `partial` (some resources succeeded) or `failed` (everything errored). The `error` column carries the message.
- **PG unavailable:** `OpenPostgres` returns an error, container exits non-zero, ECS marks STOPPED with non-zero exit code, scan row never created. SaaS sees nothing in the table. Same as case 1; same mitigation.
- **Tenant_id mismatch:** SaaS would have to mint a wrong env to a task. Defense at the IAM layer: scope Lambda's `ecs:RunTask` permission to a task definition family that has the tenant_id derived server-side, not user-supplied.

### IAM scoping

- **Lambda role:** `ecs:RunTask` on the scan-worker task definition family + `iam:PassRole` on the task execution role + the task role + read on the Secrets Manager entry holding the PG DSN.
- **Task role:** `secretsmanager:GetSecretValue` for the DSN secret + `rds-db:connect` on the RDS Proxy + cloud-provider read perms (varies per provider).
- **Task execution role:** standard ECS — `ecr:GetAuthorizationToken`, ECR pull, CloudWatch Logs write. No PG access.

No JWT secret to provision anywhere.

---

## Open follow-ups for the SaaS team

These were deferred from the original plan; the SaaS team should pick them up if/when the use case demands.

1. **Cancel an in-flight scan.** v1 has no API; use `aws ecs stop-task` directly. If a "Cancel scan" button in the UI is needed, wrap the AWS call in a Lambda.
2. **Multi-tenant per server.** Today every container is single-tenant. If usage shifts to long-lived workers serving multiple tenants, switch from `AfterConnect` GUC to `SET LOCAL` per request (transaction-scoped). Don't introduce a new HTTP layer to do this — work within the existing `*Store` API.
3. **Scan cost telemetry.** `scans` rows could carry start/finish + resource counts; SaaS can aggregate for billing. Schema's already there.
4. **Scan progress streaming.** Today it's poll-based on `scans.status`. If real-time progress is wanted, add a `scan_progress` table with per-service rows the scanner writes during the run; SaaS polls or subscribes (LISTEN/NOTIFY).
5. **Audit log of who triggered which scan.** Lambda already knows the SaaS user. Persist on the `scans` row (new `triggered_by` column) — would need an OSS-side migration since `scans` is shared.

---

## Pointers

| Topic | File |
|---|---|
| Store struct, dialect helpers | `store/store.go`, `store/dialect.go` |
| PG constructor + AfterConnect | `store/postgres_paid.go` |
| PG migrations | `store/migrations/pg/*.sql` |
| Migration parity check | `scripts/check-migrations.sh`, `make check-migrations` |
| Scan orchestration | `internal/scanrun/scanrun.go` |
| Hook indirection pattern | `cmd/CLAUDE.md` § "Hook-var indirection" |
| Wire shape contract | `store/CLAUDE.md` § "Wire shape ≠ storage shape" |
| OSS / paid build split | `CLAUDE.md` § "OSS / paid split" |
| `disco scan` flag surface | `cmd/scan.go` + `disco scan --help`, `disco scan aws --help` |

For deeper context: each `CLAUDE.md` in this repo is path-scoped. Read the one in the directory you're working in. Top-level `CLAUDE.md` covers cross-cutting rules (CGO_ENABLED=0, build tag discipline, lint conventions).

Plan that drove this work: `~/.claude/plans/load-the-plan-in-expressive-bumblebee.md`. Auto-memory: `~/.claude/projects/-home-dickc-Projects-codeberg-org-icearp-disco-upstream/memory/project_saas_arch.md`.
