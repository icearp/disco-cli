# CLAUDE.md — `internal/store/`

SQLite persistence layer (`modernc.org/sqlite`, CGO-free). Tables, edges, scrubbing, IDs, migrations.

## Tables

Six: `resources`, `relationships`, `hierarchy_closure`, `scans`, `scan_checkpoints`, plus `schema_migrations` (migration runner bookkeeping; not user-visible).

- **`scan_checkpoints`** (migration 002): per-(scan, provider, service, scope) opaque continuation tokens. Schema is generic — `last_token` is whatever cursor shape the upstream SDK exposes (AWS NextToken, Azure pager continuation, GCP pageToken). API: `SaveCheckpoint`, `GetCheckpoint`, `ListCheckpoints`, `DeleteScanCheckpoints`. The OSS path persists checkpoints; the paid incremental scanner (Phase 6) consumes them on `disco scan --resume`. Avoid `;` in migration SQL comments — `splitStatements` splits on raw semicolons and treats the post-`;` chunk as a fresh statement, producing a confusing "near per: syntax error" if a comment carried one.


- **`resources`**: one row per cloud entity. `attributes` (JSON) = full provider API response. `tags` (JSON) denormalized for `json_extract()` queries. `verified_at` (RFC3339) + `verified_by` (scan ID FK) auto-set by `UpsertResources` — callers must not set. No `parent_id` column — hierarchy via `RecordHierarchyBatch(pairs)` only.
- **`relationships`**: directed edges. `kind`: `contains`, `attached-to`, `uses`, `routes-to`, `peer`, `assumes`, `bounded-by`, `cross-account-trust`, `cross-sub-rbac`, `cross-project-iam`. UNIQUE on `(from_id, to_id, kind)` — multiple kinds may coexist between same pair. Hierarchy `contains` lives in `hierarchy_closure` only (not here), so second edge (e.g. `attached-to`) between already-hierarchical resources conflict-free. `UpsertRelationship(..., attrs *string)` accepts JSON blob for per-edge metadata (e.g. Orgs delegated-services list). **UNIQUE collapses many-to-one refs**: when N distinct source refs (e.g. two trust-policy principals from the same foreign account) all map to the same target, only one row survives. Edge count = distinct (from, to) pairs, not distinct refs — tests asserting counts must account for this.
- **`hierarchy_closure`**: closure table for O(1) "all descendants of node X", no recursive CTEs. Always populate via `RecordHierarchyBatch(pairs)` (single tx) after upserting resources. The same call ALSO writes a `parent → child contains` row to `relationships` so `GraphWalk` (relationships-only) sees the edge — single source of truth across providers. Closure rows always go down; the relationship row is gated on both endpoints existing in `resources`, with a missing endpoint surfacing as a `ScanWarning` (operators see drift, callers stay simple). Don't add a separate `UpsertRelationship(parent, child, RelContains)` call beside the closure write — duplicate but idempotent under `INSERT OR IGNORE`.
- **`scans`**: lifecycle record per scan run (created at start, updated on complete/fail).

Queries built with `squirrel` (`sq.Select(...).Where(...)`) — no string interpolation. `sqlx` handles struct scanning. Raw SQL for CTEs + anything squirrel can't express cleanly.

## Edge kinds

- `contains` — hierarchy edge. Intended parent→child (VPC→subnet, KMS key→alias), but some resolvers emit child→parent (EFS mt→fs, GuardDuty filter→detector, Backup selection→plan). Match existing direction for service touched; no "fix" without sweeping all tests.
- `attached-to` — structural membership (instance → VPC/subnet, ESM → function)
- `uses` — runtime dep, no lifecycle coupling (instance → security-group, function → KMS key, service → subnet in awsvpc mode)
- `assumes` — IAM trust (function → execution role, task-def → task/exec role)
- `bounded-by` — IAM principal scoping by permission boundary (role/user → boundary policy). Distinct from `attached-to` (which would conflate boundary with normal policy attachment) and `uses` (which is runtime). Lets queries filter "all roles bounded by policy X".
- `routes-to` — routing edges (route table → target)
- `peer` — bidirectional peering (VPC peering)
- `cross-account-trust` / `cross-sub-rbac` / `cross-project-iam` — R5 cross-tenant edges. Targets are synthetic stub resources (`aws:iam:foreign-account`, `azure:microsoft.resources:foreign-subscription`, `gcp:iam:foreign-project`) when foreign tenant out of scan scope.

IAM principals (users/roles/groups, service accounts) are edge **destinations**: group→user is `contains`, user→access-key is `contains`, policy→user is `attached-to`, role→trust-policy is `assumes`. Outbound-only BFS from a principal returns seed-only — use DirBoth (or `cmd/graph` blast's auto-fallback) and include `contains` in `Kinds`.

## Secret scrubbing

`UpsertResources` calls `redact.Apply(r.Type, r.AttributesJSON)` (in `internal/redact`) on every row before insert. Provider packages register per-type rules in their `init()` blocks (see `internal/providers/<p>/redact.go`). Each rule names a JSON path inside `AttributesJSON` and a mode — `RedactScalar` (leaf only) or `RedactSubtree` (every scalar descendant). Path syntax: dotted literals, `*` for map-key wildcard, `[*]` for array wildcard. Malformed JSON passes through untouched. Providers must NOT pre-sanitize — store boundary owns this.

Pointer-style fields (ARNs, KeyVault reference URIs, CredentialsArn / SecretArn / TokenSourceArn) are preserved by **omission** — no rule targets them. Adding edge support for a previously-redacted pointer field means dropping or narrowing the rule, not adding a shape allowlist.

Redact state is pinned at scan time. Editing rules only affects rows upserted after the change — pre-existing rows keep their old `[REDACTED]` (or unredacted) values until re-scanned.

AWS access key IDs (AKIA…) are public-ish identifiers, not credentials — IAM console, CloudTrail, `ListAccessKeys` all surface them unredacted. The `aws:iam:access-key` rule targets only `SecretAccessKey`, leaving `AccessKeyId` clear so it matches Name/NativeID.

**No CI gate** on rule coverage — adding a new resource type whose SDK response carries credentials/tokens leaks unless the author registers a rule alongside `registerService`. Reviewers must catch this. Per-provider `redact_test.go` files use real SDK types, so SDK field renames break the test on `go mod tidy` (cheap drift catch without a global harness).

## modernc/sqlite FK + INSERT OR IGNORE asymmetry

`INSERT OR IGNORE` silently absorbs FK violations on `hierarchy_closure` (caller intent — pairs for unscanned parents are normal) but surfaces them as errors on `relationships`. `recordHierarchyTx` (in `relationships.go`) handles this with an explicit `EXISTS` pre-check and a `ScanWarning` on miss — bugs become visible without spamming `errors.Is` checks across every scanner call site. Use the same pattern when writing into `relationships` from a context where endpoints may not all be scanned.

Rule of thumb for "advisory" failures (skip happens, but operator should know): prefer `ReportWarning` over a sentinel error. Sentinel forces every caller to add `errors.Is` boilerplate; warning surfaces in scan output AND tests can attach `OnWarn` to detect drift, with zero caller-side churn. Reserve sentinel errors for cases callers must branch on (e.g. `ErrNoPath` driving exit-code routing in `cmd/root.go`).

## DB file perms (0600)

`Open` chmods SQLite file to `0600` *after* `migrate()` runs. `sqlx.Open` lazy — file no exist until first query, chmod before migrate silent no-ops. Non-regular paths (e.g. `:memory:`) skipped.

## Resource IDs

`ResourceID(provider, accountID, type, nativeID)` — `resources.go` — produces 32-hex-char SHA-256 prefix. Stable across rescans; primary key.

Scan IDs: `crypto/rand` + `encoding/hex` (same 32-char hex). No `uuid` dep.

## Migrations

SQL files in `migrations/` embedded at compile time via `//go:embed`. Names must be `NNN_description.sql` (e.g. `002_add_foo.sql`). Runner splits on semicolons, executes each statement individually — SQLite `database/sql` driver silent ignores everything after first statement in multi-statement `Exec`.

Paid-only migrations use the `_paid.sql` suffix (e.g. `004_findings_paid.sql`). `scripts/oss-sync.sh` and `scripts/oss-cherry-pick.sh` strip them by name pattern — the OSS-mirror repo never sees the file, so `//go:embed migrations/*.sql` matches only OSS-resident migrations there. Upstream OSS dev-builds (no `-tags paid`) DO embed and apply paid SQL files because the files are physically present in the dev tree; that's intentional dev-only behaviour. Production OSS guarantee is the published mirror, not the upstream tree.

## `region = "global"` is the canonical non-regional sentinel

Resources scoped above any single region — AWS IAM/Route53/CloudFront/S3/Organizations/etc., Azure tenant-scope (Entra ID), GCP org/folder-scope, plus resolver-side cross-tenant synthetic stubs (`aws:iam:foreign-account`, foreign-subscription, foreign-project) — carry `region = "global"`, not NULL. Each provider package exposes a package-level `regionGlobal *string` pointer (`internal/providers/<p>/<p>.go`); global scanners and stub-emitting resolvers set `Resource.Region = regionGlobal` directly on the literal. Single sentinel pointer per package keeps the call sites trivial.

`ResourceFilter.Regions` exact-match filter folds "global" rows in by default — `--regions us-east-1` matches both us-east-1 AND global rows because users intuit a regional filter as "what's scoped to here", and globals sit logically in every region. `ResourceFilter.SkipGlobals=true` opts out (wired as `--skip-globals` on `disco list` / `summary` / `tag-coverage`). The empty-Regions + SkipGlobals path emits `region != "global"` so callers can blanket-exclude globals without naming a region.

`disco list --regions global` is the canonical "show me every global resource" query.

## UpsertResources ON CONFLICT scope

ON CONFLICT only updates: `name`, `status`, `tags`, `attributes`, `verified_at`, `verified_by`, `managed_by_provider`. Does **not** update `region`, `zone`, `account_name`, `discovered_at`. Set all fields on initial insert — second upsert can't patch. Adding a new mutable column = three edits: INSERT col list, VALUES placeholder, ON CONFLICT SET.

## FK constraint: resources require scan record

`resources.discovered_by` + `resources.verified_by` = FKs to `scans(id)`. Any test inserting resources needs scan record in DB first. `newTestStore` (provider tests) handles — inserts scan with fixed ID `"00000000000000000000000000000000"`.

## ListResources filter shape

`store.ListResources(store.ResourceFilter{...})` — filter struct is `ResourceFilter`, not `ListFilter`. Multi-type filter is `Types []string`, not `Type string`. Two zero-value defaults bite: `IncludeManaged=false` silently filters provider-managed rows, and `Limit=0` falls back to 500. Passing `ResourceFilter{}` is NOT "give me everything" — set `IncludeManaged: true` and either a large `Limit` or paginate via `Offset` for whole-table reads.

Canonical "read every resource" idiom: `store.GraphAll` (`graph.go:451`) page-loops `ListResources` with `IncludeManaged: true` + `Limit: 5000` until an empty page returns. Reuse that shape from CLI commands that must evaluate the full population (e.g. `cmd/check.loadAllResources`).

## Wire shape ≠ storage shape

`Resource` stores `AttributesJSON` / `TagsJSON` as JSON strings (raw SDK marshal output) but `MarshalJSON` / `UnmarshalJSON` (`resources.go`) surface them on the wire as nested `attributes` / `tags` objects under snake_case keys (`native_id`, `account_id`, ...). Round-trips byte-stable via the matching UnmarshalJSON. Tests asserting JSON output must compare against the parsed shape, not Go field names. New JSON encoders should emit `[]Resource` directly — no per-call shape massaging.

**Schema contract — every documented key always present.** `Resource.MarshalJSON` (and the matching `resources_json_test.go::TestResource_MarshalJSON_AlwaysPresent`) emits every key listed under `disco check --help`: optional pointer fields render as `null` (not omitted), `tags` and `attributes` always render as objects (`{}` for empty / missing / malformed legacy blobs). Stripping `,omitempty` was the F6 fix from focus-group/SUMMARY.md — Rego authors and downstream consumers can traverse `input.attributes.X` / `input.tags.Y` without per-row presence guards. Don't reintroduce `,omitempty` on the contract fields.

Adding a field to `Resource` has three downstream touch-points: (1) `MarshalJSON`/`UnmarshalJSON` if it carries on the JSON wire; (2) `resourceToInput` in `internal/policy/policy.go` so Rego policies can see it; (3) `listColumns`/`resourceRow` in `cmd/list.go` for CSV. Skipping (2) silently hides the field from every Rego rule.

modernc/sqlite accepts SQLite URI parameters via `file:<path>?<params>` form. `OpenReadOnly` uses `mode=ro`; same shape extends to `cache=shared`, `_pragma=...`, etc. when needed.

## `applyPragmas(db, readOnly bool)` skips writer-only pragmas on RO opens

`journal_mode=WAL` and `synchronous=NORMAL` write the SQLite DB header. A read-only open (`OpenReadOnly`, `mode=ro`) errors with `attempt to write a readonly database (8)` when those pragmas fire — bricks `disco --db-readonly check` against a customer-supplied snapshot. RO callers pass `readOnly=true`; writer-only pragmas are skipped. FK + cache + mmap pragmas are safe on RO and stay applied.

## No `internal/policy` import in store package

`internal/store` must not import `internal/policy` (or other downstream packages). Doing so creates `cmd → policy → store → policy` cycle. Keep store types bare (string/pointer fields, no `policy.Finding`); conversion between store rows and wire types lives in cmd-side helpers (`storedFindingToFinding`, `findingToStored` in `cmd/findings_paid.go`).

## `Scan.StartedAt` format = SQLite datetime, not RFC3339 (in storage)

`CreateScan` uses `datetime('now')` which returns `YYYY-MM-DD HH:MM:SS` (space-separated, UTC, no `T`/`Z`). The DB column carries that shape verbatim. Consumers that read `Scan.StartedAt` directly (e.g. for `time.Time` math) must `time.Parse("2006-01-02 15:04:05", s)` — or use the exported helper `store.ToRFC3339(s)`.

**Wire shape is RFC3339.** `Scan.MarshalJSON` (added F5 fix) projects `started_at` / `finished_at` to RFC3339 before emitting, so `disco scans -o json` and `disco summary -o json | jq '.as_of'` carry parseable timestamps that match resource-row `discovered_at` / `verified_at`. The wire envelope also uses snake_case keys and drops the SQLite `*JSON` columns (`ProvidersJSON`, `ScopeJSON`, `MetaJSON`) in favour of parsed `providers` / `scope` / `meta` objects. Don't reach into `scans -o json` consumers expecting the legacy PascalCase shape.

## `scans.resource_count` = totalSeen, not totalNew

`CompleteScan` / `PartialScan` persist the count of rows the scan upserted (every row visited, including pre-existing). The insert-only `totalNew` value (return of `UpsertResources`) is printed at scan-end stdout but not persisted. Drift between scans is `disco diff`'s job, not a column on `scans`. Don't re-derive "what changed" from `resource_count` deltas.

## `ResolveResource` two-pass: exact → id-prefix → substring

Seed lookup (`graph blast`, `graph path`, `list --id`) tries exact `native_id`/`name` first, then ID-prefix on the 32-hex resource ID (when arg is 4–31 lowercase hex), then `LIKE %arg%` on `native_id`/`name`. F12 fix for "the CLI's own short-ID prints don't round-trip as input." Disambiguators (`--provider`, `--type`, `--account`) narrow each pass; multi-row results surface as the existing ambiguity error. Each pass capped at 50 rows so substring-on-large-DB doesn't OOM. New callers should route through `ResolveResource` rather than rolling their own lookups — single source of truth.

## Cross-backend SQL: `s.exec`/`s.get`/`s.query`/`s.queryRow`/`s.selectAll`

Wrappers in `dialect.go` proxy `s.db.Exec/Get/Query/QueryRow/Select` with auto-`Rebind`. Always use them for raw `?`-placeholder SQL — `s.db.Exec(...)` directly works on SQLite but breaks on Postgres (sqlx Rebind isn't auto-applied). Squirrel queries pass `s.placeholder()` to `PlaceholderFormat(...)`. New code adding SQL to the store package follows both patterns.

## PG session GUCs accept placeholders only via `set_config(...)`

`SET app.tenant_id = $1` errors at parse time. The parameterised form is `SELECT set_config('<key>', $1, false)` (third arg = is_local; false = session-scoped). Used in `postgres_paid.go::OpenPostgres` `AfterConnect`. Any future per-conn GUC writes follow the same shape.

## Postgres backend (paid only)

Single `*Store` covers both SQLite and Postgres; `OpenPostgres(ctx, dsn, tenantID)` (`postgres_paid.go`, `//go:build paid`) opens a pgx-backed `*sqlx.DB`. The `driver` field selects per-call dialect via three helpers in `dialect.go`:

- `s.placeholder()` — `sq.Question` for SQLite, `sq.Dollar` for Postgres. Squirrel queries use this; raw `?` SQL goes through `s.exec` / `s.get` / `s.selectAll` / `s.queryRow` / `s.query` wrappers that auto-rebind via `db.Rebind`.
- `s.tagJSONFilter(key)` — emits `json_extract(tags, '$.k')` (SQLite) or `tags ->> 'k'` (Postgres).
- `s.tagJSONValueExists()` — `json_each(tags)` vs `jsonb_each_text(tags)`.

Other portability rules baked in:
- `INSERT OR IGNORE` was replaced with `INSERT ... ON CONFLICT (cols) DO NOTHING`. SQLite supports this since 3.24; Postgres requires the explicit conflict target. New writes follow the same shape.
- `recordHierarchyTx` and friends accept `*sql.Tx` but pass through `s.rebind(...)` first because tx itself is unaware of the driver.

### Tenant isolation (Postgres only)

PG migrations 005 add `tenant_id UUID` to every user-data table plus a per-table RLS policy `USING (tenant_id = current_setting('app.tenant_id')::uuid)`. `OpenPostgres` runs `SELECT set_config('app.tenant_id', '<uuid>', false)` in pgconn `AfterConnect` so the GUC is sticky for every conn the pool returns. Inserts pick up `tenant_id` automatically via column DEFAULT — no explicit value in app code.

Tenant ID is **pinned at process start**: `OpenPostgres` bakes it into the pool's `AfterConnect`. Switch tenants by re-opening the store, never per-query. The disco-saas Fargate-per-scan model is built on this — single tenant per container.

### RDS Proxy session-pinning trade-off

`SET app.tenant_id` at conn open is session-scoped, which RDS Proxy treats as session pinning — that conn stops participating in multiplexing for its lifetime. For one-shot single-tenant containers this is fine (every conn serves the same tenant; pinning is the same as not pinning). If a future deploy shares a Proxy across tenants, swap to `SET LOCAL app.tenant_id` inside a transaction per query.

### Migration parity

`internal/store/migrations/*.sql` (SQLite) and `internal/store/migrations/pg/*.sql` (Postgres) must converge on identical `(table, column)` sets — the **only** allowed PG-only columns are RLS plumbing (`tenant_id`). `make check-migrations` (script: `scripts/check-migrations.sh`) extracts column lists from each set and diffs them with that allowlist applied. Add a column on one side, the script fails. CI gates this; reviewers also.

PG migration runner is hand-rolled in `migrate_pg_paid.go`, mirroring `migrate.go:14–111` shape: same `schema_migrations` bookkeeping, same `splitStatements` semicolon split, same NNN_name.sql convention. Per-migration BEGIN+exec+INSERT+COMMIT means partial failure leaves a clean state.

## Denylist filters via `sq.NotEq`

`squirrel.NotEq{"col": []string{...}}` emits `col NOT IN (?, ?, ...)`. Mirror of `sq.Eq` allowlist; use for any new exclude-X filter on `ResourceFilter` rather than hand-rolled OR-NOT chains. Precedent: `ExcludeTypes` (resources.go).
