# CLAUDE.md — `internal/store/`

SQLite persistence layer (`modernc.org/sqlite`, CGO-free). Tables, edges, scrubbing, IDs, migrations.

## Tables

Five: `resources`, `relationships`, `hierarchy_closure`, `scans`, `scan_checkpoints`.

- **`scan_checkpoints`** (migration 002): per-(scan, provider, service, scope) opaque continuation tokens. Schema is generic — `last_token` is whatever cursor shape the upstream SDK exposes (AWS NextToken, Azure pager continuation, GCP pageToken). API: `SaveCheckpoint`, `GetCheckpoint`, `ListCheckpoints`, `DeleteScanCheckpoints`. The OSS path persists checkpoints; the paid incremental scanner (Phase 6) consumes them on `disco scan --resume`. Avoid `;` in migration SQL comments — `splitStatements` splits on raw semicolons and treats the post-`;` chunk as a fresh statement, producing a confusing "near per: syntax error" if a comment carried one.


- **`resources`**: one row per cloud entity. `attributes` (JSON) = full provider API response. `tags` (JSON) denormalized for `json_extract()` queries. `verified_at` (RFC3339) + `verified_by` (scan ID FK) auto-set by `UpsertResources` — callers must not set. No `parent_id` column — hierarchy via `BatchAddToHierarchyClosure(pairs)` only.
- **`relationships`**: directed edges. `kind`: `contains`, `attached-to`, `uses`, `routes-to`, `peer`, `assumes`, `bounded-by`, `cross-account-trust`, `cross-sub-rbac`, `cross-project-iam`. UNIQUE on `(from_id, to_id, kind)` — multiple kinds may coexist between same pair. Hierarchy `contains` lives in `hierarchy_closure` only (not here), so second edge (e.g. `attached-to`) between already-hierarchical resources conflict-free. `UpsertRelationship(..., attrs *string)` accepts JSON blob for per-edge metadata (e.g. Orgs delegated-services list). **UNIQUE collapses many-to-one refs**: when N distinct source refs (e.g. two trust-policy principals from the same foreign account) all map to the same target, only one row survives. Edge count = distinct (from, to) pairs, not distinct refs — tests asserting counts must account for this.
- **`hierarchy_closure`**: closure table for O(1) "all descendants of node X", no recursive CTEs. Always populate via `BatchAddToHierarchyClosure(pairs)` (single tx) after upserting resources. The same call ALSO writes a `parent → child contains` row to `relationships` so `GraphWalk` (relationships-only) sees the edge — single source of truth across providers. Relationship row is FK-guarded via `WHERE EXISTS`; closure row is not (unscanned parent IDs survive). Don't add a separate `UpsertRelationship(parent, child, RelContains)` call beside the closure write — duplicate but idempotent under `INSERT OR IGNORE`.
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

## Secret scrubbing

`UpsertResources` calls `scrubAttributes` (`sanitize.go`) on every `attributes` JSON blob before insert. Denylist of key substrings (`password`, `passphrase`, `secret`, `token`, `signature`, `presignedurl`, `credential`, `privatekey`, `apikey`, `bearer`, `authorization`) → `"[REDACTED]"`. Malformed JSON passes through untouched. Providers must NOT pre-sanitize — store boundary owns this.

**Three redaction modes** (`sanitize.go`):
1. `sensitiveKeySubstrings` (scalar-only, substring match): key matches denylist → scalar value redacted; object/array values recurse so structural containers whose name matches denylist (e.g. ECS `ContainerDefinitions[].Secrets[]`) stay intact. Leaf leaks (`SecretString`, `Password`, ...) caught. If resolver unmarshal silent yields zero edges under key whose name matches denylist, check here first.
2. `containerRedactKeys` (wholesale, exact lower-case match): key matches → every scalar descendant redacted regardless of leaf name. Use for user-key/value maps where leaf names unpredictable (Lambda `Environment.Variables`, CodeBuild env). Add exact lower-case name only; short strings over-match.
3. `isReferenceURI` allowlist (shape-bounded escape hatch from mode 1): a scalar under a denylist key is preserved verbatim if its value matches a known pointer-only URI shape (currently Azure Key Vault: `https://{vault}.vault.{azure.net|usgovcloudapi.net|azure.cn|microsoftazure.de}/{secrets|keys|certificates}/{name}[/{ver}]`). The URI is addressing data (vault host + object name + version), not material. Resolvers downstream (AGW → KV via `sslCertificates[].keyVaultSecretId`, App Service config refs, AKS secret-store CSI, Logic Apps named values) consume the unredacted URI. **Extend the allowlist** when a new pointer shape is needed — add to `keyVaultDNSSuffixes` / `keyVaultObjectPaths` for KV variants, or grow `isReferenceURI` for non-KV pointer shapes. Do NOT touch the denylist; do NOT sidecar during scan unless the value cannot be expressed as a stable shape.

## modernc/sqlite FK + INSERT OR IGNORE asymmetry

`INSERT OR IGNORE` silently absorbs FK violations on `hierarchy_closure` (caller intent — pairs for unscanned parents are normal) but surfaces them as errors on `relationships`. Guard relationship inserts with `WHERE EXISTS (SELECT 1 FROM resources WHERE id = ?)` when the endpoint may not be scanned (e.g. closure-driven contains rows). Precedent: `addClosureTx` in `relationships.go`.

## DB file perms (0600)

`Open` chmods SQLite file to `0600` *after* `migrate()` runs. `sqlx.Open` lazy — file no exist until first query, chmod before migrate silent no-ops. Non-regular paths (e.g. `:memory:`) skipped.

## Resource IDs

`ResourceID(provider, accountID, type, nativeID)` — `resources.go` — produces 32-hex-char SHA-256 prefix. Stable across rescans; primary key.

Scan IDs: `crypto/rand` + `encoding/hex` (same 32-char hex). No `uuid` dep.

## Migrations

SQL files in `migrations/` embedded at compile time via `//go:embed`. Names must be `NNN_description.sql` (e.g. `002_add_foo.sql`). Runner splits on semicolons, executes each statement individually — SQLite `database/sql` driver silent ignores everything after first statement in multi-statement `Exec`.

## UpsertResources ON CONFLICT scope

ON CONFLICT only updates: `name`, `status`, `tags`, `attributes`, `verified_at`, `verified_by`, `managed_by_provider`. Does **not** update `region`, `zone`, `account_name`, `discovered_at`. Set all fields on initial insert — second upsert can't patch. Adding a new mutable column = three edits: INSERT col list, VALUES placeholder, ON CONFLICT SET.

## FK constraint: resources require scan record

`resources.discovered_by` + `resources.verified_by` = FKs to `scans(id)`. Any test inserting resources needs scan record in DB first. `newTestStore` (provider tests) handles — inserts scan with fixed ID `"00000000000000000000000000000000"`.

## ListResources filter shape

`store.ListResources(store.ResourceFilter{...})` — filter struct is `ResourceFilter`, not `ListFilter`. Multi-type filter is `Types []string`, not `Type string`. Two zero-value defaults bite: `IncludeManaged=false` silently filters provider-managed rows, and `Limit=0` falls back to 500. Passing `ResourceFilter{}` is NOT "give me everything" — set `IncludeManaged: true` and either a large `Limit` or paginate via `Offset` for whole-table reads.
