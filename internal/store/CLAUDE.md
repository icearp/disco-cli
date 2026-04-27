# CLAUDE.md — `internal/store/`

SQLite persistence layer (`modernc.org/sqlite`, CGO-free). Tables, edges, scrubbing, IDs, migrations.

## Tables

Four: `resources`, `relationships`, `hierarchy_closure`, `scans`.

- **`resources`**: one row per cloud entity. `attributes` (JSON) = full provider API response. `tags` (JSON) denormalized for `json_extract()` queries. `verified_at` (RFC3339) + `verified_by` (scan ID FK) auto-set by `UpsertResources` — callers must not set. No `parent_id` column — hierarchy via `BatchAddToHierarchyClosure(pairs)` only.
- **`relationships`**: directed edges. `kind`: `contains`, `attached-to`, `uses`, `routes-to`, `peer`, `assumes`. UNIQUE on `(from_id, to_id, kind)` — multiple kinds may coexist between same pair. Hierarchy `contains` lives in `hierarchy_closure` only (not here), so second edge (e.g. `attached-to`) between already-hierarchical resources conflict-free. `UpsertRelationship(..., attrs *string)` accepts JSON blob for per-edge metadata (e.g. Orgs delegated-services list).
- **`hierarchy_closure`**: closure table for O(1) "all descendants of node X", no recursive CTEs. Always populate via `BatchAddToHierarchyClosure(pairs)` (single tx) after upserting resources with `parent_id`.
- **`scans`**: lifecycle record per scan run (created at start, updated on complete/fail).

Queries built with `squirrel` (`sq.Select(...).Where(...)`) — no string interpolation. `sqlx` handles struct scanning. Raw SQL for CTEs + anything squirrel can't express cleanly.

## Edge kinds

- `contains` — hierarchy edge. Intended parent→child (VPC→subnet, KMS key→alias), but some resolvers emit child→parent (EFS mt→fs, GuardDuty filter→detector, Backup selection→plan). Match existing direction for service touched; no "fix" without sweeping all tests.
- `attached-to` — structural membership (instance → VPC/subnet, ESM → function)
- `uses` — runtime dep, no lifecycle coupling (instance → security-group, function → KMS key, service → subnet in awsvpc mode)
- `assumes` — IAM trust (function → execution role, task-def → task/exec role)
- `routes-to` — routing edges (route table → target)
- `peer` — bidirectional peering (VPC peering)

## Secret scrubbing

`UpsertResources` calls `scrubAttributes` (`sanitize.go`) on every `attributes` JSON blob before insert. Denylist of key substrings (`password`, `passphrase`, `secret`, `token`, `signature`, `presignedurl`, `credential`, `privatekey`, `apikey`, `bearer`, `authorization`) → `"[REDACTED]"`. Malformed JSON passes through untouched. Providers must NOT pre-sanitize — store boundary owns this.

**Three redaction modes** (`sanitize.go`):
1. `sensitiveKeySubstrings` (scalar-only, substring match): key matches denylist → scalar value redacted; object/array values recurse so structural containers whose name matches denylist (e.g. ECS `ContainerDefinitions[].Secrets[]`) stay intact. Leaf leaks (`SecretString`, `Password`, ...) caught. If resolver unmarshal silent yields zero edges under key whose name matches denylist, check here first.
2. `containerRedactKeys` (wholesale, exact lower-case match): key matches → every scalar descendant redacted regardless of leaf name. Use for user-key/value maps where leaf names unpredictable (Lambda `Environment.Variables`, CodeBuild env). Add exact lower-case name only; short strings over-match.
3. `isReferenceURI` allowlist (shape-bounded escape hatch from mode 1): a scalar under a denylist key is preserved verbatim if its value matches a known pointer-only URI shape (currently Azure Key Vault: `https://{vault}.vault.{azure.net|usgovcloudapi.net|azure.cn|microsoftazure.de}/{secrets|keys|certificates}/{name}[/{ver}]`). The URI is addressing data (vault host + object name + version), not material. Resolvers downstream (AGW → KV via `sslCertificates[].keyVaultSecretId`, App Service config refs, AKS secret-store CSI, Logic Apps named values) consume the unredacted URI. **Extend the allowlist** when a new pointer shape is needed — add to `keyVaultDNSSuffixes` / `keyVaultObjectPaths` for KV variants, or grow `isReferenceURI` for non-KV pointer shapes. Do NOT touch the denylist; do NOT sidecar during scan unless the value cannot be expressed as a stable shape.

## DB file perms (0600)

`Open` chmods SQLite file to `0600` *after* `migrate()` runs. `sqlx.Open` lazy — file no exist until first query, chmod before migrate silent no-ops. Non-regular paths (e.g. `:memory:`) skipped.

## Resource IDs

`ResourceID(provider, accountID, type, nativeID)` — `resources.go` — produces 32-hex-char SHA-256 prefix. Stable across rescans; primary key.

Scan IDs: `crypto/rand` + `encoding/hex` (same 32-char hex). No `uuid` dep.

## Migrations

SQL files in `migrations/` embedded at compile time via `//go:embed`. Names must be `NNN_description.sql` (e.g. `002_add_foo.sql`). Runner splits on semicolons, executes each statement individually — SQLite `database/sql` driver silent ignores everything after first statement in multi-statement `Exec`.

## UpsertResources ON CONFLICT scope

ON CONFLICT only updates: `name`, `status`, `tags`, `attributes`, `verified_at`, `verified_by`. Does **not** update `region`, `zone`, `account_name`, `discovered_at`. Set all fields on initial insert — second upsert can't patch.

## FK constraint: resources require scan record

`resources.discovered_by` + `resources.verified_by` = FKs to `scans(id)`. Any test inserting resources needs scan record in DB first. `newTestStore` (provider tests) handles — inserts scan with fixed ID `"00000000000000000000000000000000"`.
