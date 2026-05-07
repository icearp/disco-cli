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

`UpsertResources` calls `scrubAttributes` (`sanitize.go`) on every `attributes` JSON blob before insert. Denylist of key substrings (`password`, `passphrase`, `secret`, `token`, `signature`, `presignedurl`, `credential`, `privatekey`, `apikey`, `bearer`, `authorization`) → `"[REDACTED]"`. Malformed JSON passes through untouched. Providers must NOT pre-sanitize — store boundary owns this.

AWS access key IDs (AKIA…) are public-ish identifiers, not credentials — IAM console, CloudTrail, `ListAccessKeys` all surface them unredacted. The denylist intentionally **omits** `accesskey` so attrs.AccessKeyId matches Name/NativeID. The credential is `SecretAccessKey`, returned only on `CreateAccessKey` (write op disco never calls); the `secret` substring catches it if it ever appears.

Scrub state is pinned at scan time. Editing `sensitiveKeySubstrings` / `containerRedactKeys` only affects rows upserted after the change — pre-existing rows keep their old `[REDACTED]` (or unredacted) values until re-scanned.

**Three redaction modes** (`sanitize.go`):
1. `sensitiveKeySubstrings` (scalar-only, substring match): key matches denylist → scalar value redacted; object/array values recurse so structural containers whose name matches denylist (e.g. ECS `ContainerDefinitions[].Secrets[]`) stay intact. Leaf leaks (`SecretString`, `Password`, ...) caught. If resolver unmarshal silent yields zero edges under key whose name matches denylist, check here first.
2. `containerRedactKeys` (wholesale, exact lower-case match): key matches → every scalar descendant redacted regardless of leaf name. Use for user-key/value maps where leaf names unpredictable (Lambda `Environment.Variables`, CodeBuild env). Add exact lower-case name only; short strings over-match.
3. Pointer-shape allowlist (shape-bounded escape hatch from mode 1): a scalar under a denylist key is preserved verbatim if its value matches a known pointer-only shape. Two recognisers today: `isReferenceURI` (Azure Key Vault: `https://{vault}.vault.{azure.net|usgovcloudapi.net|azure.cn|microsoftazure.de}/{secrets|keys|certificates}/{name}[/{ver}]`) and `isAWSARN` (any `arn:aws[-cn|-us-gov|-iso[-b]]:<service>:...` with ≥5 colons). Both express addressing data (vault host + object name + version, or service + region + account + resource path), never material. Resolvers downstream (AGW → KV via `sslCertificates[].keyVaultSecretId`, App Service config refs, AKS secret-store CSI, Logic Apps named values, AWS resolvers reading `CredentialsArn` / `SecretArn` / `TokenSourceArn` / `AuthorizationHeaderArn`) consume the unredacted pointer. **Extend the allowlist** when a new pointer shape is needed — add to `keyVaultDNSSuffixes` / `keyVaultObjectPaths` for KV variants, widen `isAWSARN` partition list, or add a sibling recogniser for non-ARN/non-KV shapes. Do NOT touch the denylist; do NOT sidecar during scan unless the value cannot be expressed as a stable shape.

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

## UpsertResources ON CONFLICT scope

ON CONFLICT only updates: `name`, `status`, `tags`, `attributes`, `verified_at`, `verified_by`, `managed_by_provider`. Does **not** update `region`, `zone`, `account_name`, `discovered_at`. Set all fields on initial insert — second upsert can't patch. Adding a new mutable column = three edits: INSERT col list, VALUES placeholder, ON CONFLICT SET.

## FK constraint: resources require scan record

`resources.discovered_by` + `resources.verified_by` = FKs to `scans(id)`. Any test inserting resources needs scan record in DB first. `newTestStore` (provider tests) handles — inserts scan with fixed ID `"00000000000000000000000000000000"`.

## ListResources filter shape

`store.ListResources(store.ResourceFilter{...})` — filter struct is `ResourceFilter`, not `ListFilter`. Multi-type filter is `Types []string`, not `Type string`. Two zero-value defaults bite: `IncludeManaged=false` silently filters provider-managed rows, and `Limit=0` falls back to 500. Passing `ResourceFilter{}` is NOT "give me everything" — set `IncludeManaged: true` and either a large `Limit` or paginate via `Offset` for whole-table reads.

Canonical "read every resource" idiom: `store.GraphAll` (`graph.go:451`) page-loops `ListResources` with `IncludeManaged: true` + `Limit: 5000` until an empty page returns. Reuse that shape from CLI commands that must evaluate the full population (e.g. `cmd/check.loadAllResources`).

## Wire shape ≠ storage shape

`Resource` stores `AttributesJSON` / `TagsJSON` as JSON strings (raw SDK marshal output) but `MarshalJSON` / `UnmarshalJSON` (`resources.go`) surface them on the wire as nested `attributes` / `tags` objects under snake_case keys (`native_id`, `account_id`, ...). Round-trips byte-stable via the matching UnmarshalJSON. Tests asserting JSON output must compare against the parsed shape, not Go field names. New JSON encoders should emit `[]Resource` directly — no per-call shape massaging.

Adding a field to `Resource` has three downstream touch-points: (1) `MarshalJSON`/`UnmarshalJSON` if it carries on the JSON wire; (2) `resourceToInput` in `internal/policy/policy.go` so Rego policies can see it; (3) `listColumns`/`resourceRow` in `cmd/list.go` for CSV. Skipping (2) silently hides the field from every Rego rule.

modernc/sqlite accepts SQLite URI parameters via `file:<path>?<params>` form. `OpenReadOnly` uses `mode=ro`; same shape extends to `cache=shared`, `_pragma=...`, etc. when needed.

## `applyPragmas(db, readOnly bool)` skips writer-only pragmas on RO opens

`journal_mode=WAL` and `synchronous=NORMAL` write the SQLite DB header. A read-only open (`OpenReadOnly`, `mode=ro`) errors with `attempt to write a readonly database (8)` when those pragmas fire — bricks `disco --db-readonly check` against a customer-supplied snapshot. RO callers pass `readOnly=true`; writer-only pragmas are skipped. FK + cache + mmap pragmas are safe on RO and stay applied.

## No `internal/policy` import in store package

`internal/store` must not import `internal/policy` (or other downstream packages). Doing so creates `cmd → policy → store → policy` cycle. Keep store types bare (string/pointer fields, no `policy.Finding`); conversion between store rows and wire types lives in cmd-side helpers (`storedFindingToFinding`, `findingToStored` in `cmd/findings_paid.go`).

## `Scan.StartedAt` format = SQLite datetime, not RFC3339

`CreateScan` uses `datetime('now')` which returns `YYYY-MM-DD HH:MM:SS` (space-separated, UTC, no `T`/`Z`). Don't assume RFC3339-parseable; consumers needing `time.Time` must `time.Parse("2006-01-02 15:04:05", s)`.

## `scans.resource_count` = totalSeen, not totalNew

`CompleteScan` / `PartialScan` persist the count of rows the scan upserted (every row visited, including pre-existing). The insert-only `totalNew` value (return of `UpsertResources`) is printed at scan-end stdout but not persisted. Drift between scans is `disco diff`'s job, not a column on `scans`. Don't re-derive "what changed" from `resource_count` deltas.

## Denylist filters via `sq.NotEq`

`squirrel.NotEq{"col": []string{...}}` emits `col NOT IN (?, ?, ...)`. Mirror of `sq.Eq` allowlist; use for any new exclude-X filter on `ResourceFilter` rather than hand-rolled OR-NOT chains. Precedent: `ExcludeTypes` (resources.go).
