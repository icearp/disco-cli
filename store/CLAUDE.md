# CLAUDE.md — `store/`

SQLite persistence layer (`modernc.org/sqlite`, CGO-free). Tables, edges, scrubbing, IDs, migrations.

## Tables

Seven: `resources`, `quotas`, `relationships`, `hierarchy_closure`, `scans`, `scan_checkpoints`, plus `schema_migrations` (migration runner bookkeeping; not user-visible).

- **`quotas`** (migration 017): one row per version of one service quota limit. Separate from `resources` because a quota is a limit *value*, not a provisioned thing: nothing creates it, it has no graph edges, and it is queried by service and by proximity to the limit rather than by name-ordered page slice. On a real account they were ~90% of every row in `resources`, so they dominated every index whether or not anyone read them. Identity is `(provider, account_id, region, service_code, quota_code, dimension_key)` — `region` is part of it, so unlike `resources.region` it is NOT NULL, and a partition-wide limit uses the `'global'` sentinel; `dimension_key` is empty on an undimensioned limit and names the dimension set otherwise, because one quota code can carry a different value per dimension set (every GCP `DimensionsInfo`, an AWS quota context). Same version-chain shape as `resources` (per-row UUIDv7 `id`, deterministic `QuotaID` hash in `root_id`, current row has `superseded_by IS NULL`), and `value` is a real NUMERIC so "which limits am I near" and "which have I raised" are expressible in SQL. Three indexes, each with a named reader in the migration header — do not seed it with resource-shaped ones. `description` (migration 018) is the provider's own prose for what a limit governs — AWS populates it on every row, Azure and GCP report none — and is display-only, so like `name` and `service_name` it updates in place rather than splitting a chain. API: `UpsertQuotas`, `ListQuotas`, `GetQuota`, `ResolveQuota`, `GetQuotaVersions`. Unlike `UpsertResources` this runs on `s.ext()`, so it works inside a caller-owned `WrapTx` transaction where `s.db` is nil.

- **`scan_checkpoints`** (migration 002): per-(scan, provider, service, scope) opaque continuation tokens. Schema is generic — `last_token` is whatever cursor shape the upstream SDK exposes (AWS NextToken, Azure pager continuation, GCP pageToken). API: `SaveCheckpoint`, `GetCheckpoint`, `ListCheckpoints`, `DeleteScanCheckpoints`. disco persists checkpoints; a future incremental scanner can consume them on `disco scan --resume`. `splitStatements` (the SQLite runner only — see § Migrations) is dollar-quote (`$$`, `$tag$`) and `--`-comment aware, so plpgsql function bodies and inline `;` in `--` comments are safe inside migrations.


- **`resources`**: one row per attribute-snapshot of a cloud entity. `attributes` (JSON) = full provider API response. `tags` (JSON) denormalized for `json_extract()` queries. PK `id` is a per-row UUIDv7, the deterministic `ResourceID` hash lives in `root_id`, and the current row in a chain has `superseded_by IS NULL`. `UpsertResources` auto-handles version splits (see resource-versioning rule below). No `parent_id` column — hierarchy via `RecordHierarchyBatch(pairs)` only.
- **`relationships`**: directed edges. `kind`: `contains`, `attached-to`, `uses`, `routes-to`, `peer`, `assumes`, `bounded-by`, `cross-account-trust`, `cross-sub-rbac`, `cross-project-iam`, `org-iam`. UNIQUE on `(from_id, to_id, kind)` — multiple kinds may coexist between same pair. The upsert is UPDATE-then-INSERT rather than `ON CONFLICT … DO UPDATE`, because that form cannot omit its conflict target and an embedder may widen this key. An embedder that widens it MUST scope it with row-level security, and all four conditions matter, because the UPDATE half carries no scope predicate of its own: the table `ENABLE`d **and** `FORCE`d (without FORCE the owner — the ordinary writer — is exempt), the writing role NOSUPERUSER and NOBYPASSRLS, and a permissive policy that covers UPDATE. A policy with `USING` and no `WITH CHECK` is fine, not a hole: Postgres reuses `USING` as the check, so an UPDATE cannot move a row out of the caller's scope. Get it wrong in the first two ways and one scope's re-scan rewrites every other scope's row while `RowsAffected` hides the count; get it wrong in the third and the UPDATE matches nothing, the INSERT is absorbed, and the edge is dropped in silence. Hierarchy `contains` lives in `hierarchy_closure` only (not here), so second edge (e.g. `attached-to`) between already-hierarchical resources conflict-free. `UpsertRelationship(..., attrs *string)` accepts JSON blob for per-edge metadata (e.g. Orgs delegated-services list). **UNIQUE collapses many-to-one refs**: when N distinct source refs (e.g. two trust-policy principals from the same foreign account) all map to the same target, only one row survives. Edge count = distinct (from, to) pairs, not distinct refs — tests asserting counts must account for this.
- **`hierarchy_closure`**: closure table for O(1) "all descendants of node X", no recursive CTEs. Always populate via `RecordHierarchyBatch(pairs)` (single tx) after upserting resources. The same call ALSO writes a `parent → child contains` row to `relationships` so `GraphWalk` (relationships-only) sees the edge — single source of truth across providers. Closure rows always go down; the relationship row is gated on both endpoints existing in `resources`, with a missing endpoint surfacing as a `ScanWarning` (operators see drift, callers stay simple). Don't add a separate `UpsertRelationship(parent, child, RelContains)` call beside the closure write — duplicate but idempotent under `ON CONFLICT DO NOTHING`.
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
- `cross-account-trust` / `cross-sub-rbac` / `cross-project-iam` — R5 cross-tenant edges. Targets are the referenced **account / subscription / project self-node** (`aws:iam:account`, `azure:microsoft.resources:subscriptions`, `gcp:cloudresourcemanager:project`) — a real resource type, not a synthetic stub. When the foreign tenant is out of scan scope the resolver `InsertResourcesIfAbsent`s an empty-attribute placeholder at that self-node's natural key; if the account/sub/project is later scanned, its own scanner version-populates the row (the edge's `to_id` is the deterministic `root_id`, stable across the split). See "Reference-discovered placeholders" below.
- `org-iam` — GCP org/folder-scoped IAM policy binding → service account (or, if unscanned, that SA's owning project self-node placeholder, same mechanism as `cross-project-iam`). Kept distinct from `cross-project-iam` even though the target resolution mechanism is identical: an org/folder-level grant has org-wide blast radius, not a two-project relationship, so a rule/query filtering one kind shouldn't silently also match the other.

IAM principals (users/roles/groups, service accounts) are edge **destinations**: group→user is `contains`, user→access-key is `contains`, policy→user is `attached-to`, role→trust-policy is `assumes`. Outbound-only BFS from a principal returns seed-only — use DirBoth (or `cmd/graph` blast's auto-fallback) and include `contains` in `Kinds`.

## Secret scrubbing

`UpsertResources` calls `redact.Apply(r.Type, r.AttributesJSON)` (in `internal/redact`) on every row before insert. Provider packages register per-type rules in their `init()` blocks (see `internal/providers/<p>/redact.go`). Each rule names a JSON path inside `AttributesJSON` and a mode — `RedactScalar` (leaf only) or `RedactSubtree` (every scalar descendant). Path syntax: dotted literals, `*` for map-key wildcard, `[*]` for array wildcard. Malformed JSON passes through untouched. Providers must NOT pre-sanitize — store boundary owns this.

Immediately after redaction, `UpsertResources` calls `volatile.Apply(r.Type, r.AttributesJSON)` (in `internal/volatile`), which **removes** provider-declared volatile keys (e.g. CloudWatch Logs `UploadSequenceToken`, which AWS rotates every read) so they don't version-split an otherwise-unchanged resource. Both passes run before the `jsonEqual` version comparison. Volatile removes the key (vs redact's `[REDACTED]` placeholder); see `internal/providers/CLAUDE.md` "Declaring volatile-field rules".

Pointer-style fields (ARNs, KeyVault reference URIs, CredentialsArn / SecretArn / TokenSourceArn) are preserved by **omission** — no rule targets them. Adding edge support for a previously-redacted pointer field means dropping or narrowing the rule, not adding a shape allowlist.

Redact state is pinned at scan time. Editing rules only affects rows upserted after the change — pre-existing rows keep their old `[REDACTED]` (or unredacted) values until re-scanned.

AWS access key IDs (AKIA…) are public-ish identifiers, not credentials — IAM console, CloudTrail, `ListAccessKeys` all surface them unredacted. The `aws:iam:access-key` rule targets only `SecretAccessKey`, leaving `AccessKeyId` clear so it matches Name/NativeID.

**No CI gate** on rule coverage — adding a new resource type whose SDK response carries credentials/tokens leaks unless the author registers a rule alongside `registerService`. Reviewers must catch this. Per-provider `redact_test.go` files use real SDK types, so SDK field renames break the test on `go mod tidy` (cheap drift catch without a global harness).

## Edge endpoints are checked in Go, not by the database

`recordHierarchyTx` (in `relationships.go`) pre-checks both endpoints with an
`EXISTS` and reports a `ScanWarning` on a miss, rather than letting the write
fail. The hierarchy `contains` row is the only edge write that checks its endpoints at all — `UpsertRelationship`/`UpsertRelationships` deliberately do not, since cross-tenant edges point at resources outside scan scope, which is what the `InsertResourcesIfAbsent` placeholders exist for. Nothing in the database enforces it either: `006`
dropped the FKs from `relationships` and `hierarchy_closure` to `resources` on
both backends, because `id` became the per-version row id and `root_id` is
non-unique by design. `ON CONFLICT DO NOTHING` would not have covered it either
— it absorbs uniqueness conflicts only, never FK, NOT NULL or CHECK. Use the
same pre-check pattern when writing into `relationships` from a context where
endpoints may not all be scanned.

Rule of thumb for "advisory" failures (skip happens, but operator should know): prefer `ReportWarning` over a sentinel error. Sentinel forces every caller to add `errors.Is` boilerplate; warning surfaces in scan output AND tests can attach `OnWarn` to detect drift, with zero caller-side churn. Reserve sentinel errors for cases callers must branch on (e.g. `ErrNoPath` driving exit-code routing in `cmd/root.go`).

## DB file perms (0600)

`Open` chmods SQLite file to `0600` *after* `migrate()` runs. `sqlx.Open` lazy — file no exist until first query, chmod before migrate silent no-ops. Non-regular paths (e.g. `:memory:`) skipped.

## Resource IDs

`ResourceID(provider, accountID, nativeID)` — `resources.go` — produces 32-hex-char SHA-256 prefix. Stable across rescans; primary key. `type` is deliberately **excluded** — identity is `(provider, account_id, native_id)`; `type` is a versioned attribute (a type change supersedes the row, it does not fork a new chain). native_id already encodes the type in every provider (ARNs / resource paths), so folding type into the hash was redundant.

Scan IDs: `crypto/rand` + `encoding/hex` (same 32-char hex). No `uuid` dep.

## Migrations

SQL files in `migrations/` (SQLite) and `migrations/pg/` (Postgres) embedded at compile time via `//go:embed`. Names must be `NNN_description.sql` (e.g. `002_add_foo.sql`). **The two runners differ here: SQLite splits on semicolons and executes each statement individually** (its `database/sql` driver silently ignores everything after the first statement in a multi-statement `Exec`), **while Postgres executes each file WHOLE in one `ExecContext`** — pgx sends a parameterless query over the simple protocol, which takes a multi-statement body, so a fresh provision costs one round trip per file instead of one per statement. All migrations apply unconditionally; keep the two backend sets in column-parity (see `make check-migrations`).

## `region = "global"` is the canonical non-regional sentinel

Resources scoped above any single region — AWS IAM/Route53/CloudFront/S3/Organizations/etc., Azure tenant-scope (Entra ID), GCP org/folder-scope — carry `region = "global"`, not NULL. Each provider package exposes a package-level `regionGlobal *string` pointer (`internal/providers/<p>/<p>.go`); global scanners set `Resource.Region = regionGlobal` directly on the literal. Single sentinel pointer per package keeps the call sites trivial. (Cross-tenant placeholders mirror whatever region the real self-node scanner uses — AWS account → `global`, Azure subscription / GCP project → unset, matching their scanners.)

`ResourceFilter.Regions` exact-match filter folds "global" rows in by default — `--regions us-east-1` matches both us-east-1 AND global rows because users intuit a regional filter as "what's scoped to here", and globals sit logically in every region. `ResourceFilter.SkipGlobals=true` opts out (wired as `--exclude-global-region` on `disco resources` / `summary` / `tag-coverage`; the `--skip-globals` name is reserved for scan's service-discovery skip). The empty-Regions + SkipGlobals path emits `region != "global"` so callers can blanket-exclude globals without naming a region.

`disco resources --regions global` is the canonical "show me every global resource" query.

## Resource versioning

Resources are immutable per attribute-snapshot. A scan that finds
**unchanged** attributes + tags advances `verified_at` / `verified_by`
on the current row. A scan that finds **changed** attributes or tags
inserts a NEW row with a fresh UUIDv7 id, links it back via
`previous_version_id`, and marks the old row `superseded_by = new.id`.
Top-level columns (name / region / zone / status / managed) still
update in place — they don't trigger a split. Caller-facing identity
is the deterministic `ResourceID` hash, stored in `root_id` and
exposed as `Resource.ID` via the `root_id AS id` projection hook
(`resources_hooks.go`). Per-version row PKs are internal, surfaced
only on `ResourceVersion.VersionRowID`.

Reads filter to the current row of each chain via
`applyCurrentVersionPredicate()` (`WHERE superseded_by IS NULL`).
Writes order matters: a version split UPDATEs the old row's
`superseded_by` BEFORE inserting the new row, so the partial unique
index `idx_resources_current_by_natural_key` (over
`(provider, account_id, native_id) WHERE superseded_by IS
NULL`) never sees two current rows simultaneously.

**Changed-detection canonicalizes embedded JSON.** `jsonEqual`
(`resources_versioning.go`) decides unchanged-vs-split by parsing both
`AttributesJSON`/`TagsJSON` blobs and re-marshalling through
`canonicalizeJSONValue`, which recursively normalizes key order **including
inside string leaves that are themselves JSON** (an IAM/KMS policy document
embedded as an opaque string). AWS returns those policy strings with
`Condition`-map keys in non-deterministic order; without this an unchanged
KMS key (and S3/SNS/SQS resource policies, IAM assume-role docs) would
version-split on every scan. A genuinely different policy still produces
different canonical bytes, so real changes are still detected. When adding a
field whose value is an opaque embedded-JSON string, no special handling is
needed — canonicalization already covers it.

**`new` vs `changed` reporting.** `UpsertResources` returns `inserted` (=
first-discoveries + version-splits) for backward compatibility, but the scan
progress line attributes the two separately via scoped atomic counters:
`Store.WithUpsertCounters(newC, changedC)` returns a shallow-copy `*Store`
(mirrors `WithRelCounter`) whose `UpsertResources` bumps `newC` on a
first-discovery and `changedC` on a version split. Provider dispatchers bind
a pair per per-service scan and pass them to `ReportService(service, scope,
total, new, changed, errCount, disabled)` — so the count splits per (service,
scope) without threading a second value through every scanner's
`(total, inserted, err)` signature. The scanner-returned `inserted` is no
longer what drives the progress line's "new" column.

Relationships reference `root_id` (the deterministic hash), not
per-version row ids. FK to `resources(id)` is dropped (SQLite
recreates `relationships` + `hierarchy_closure` without the FK
clause; PG's ALTER TABLE DROP CONSTRAINT in the 006 migration).
`resourceExistsTx` uses `resourceIDColumn()` + `currentVersionWhereSQL()`
so the hierarchy gating fires correctly.

**Type-separation rule:** `Resource` in `resources.go` is the base row
type. Every versioning-only field lives on `ResourceVersion` in
`resources_versioning.go` via embedding —
`type ResourceVersion struct { Resource; VerifiedAt *string; ... }`.
Adding a field to `Resource` cascades to `ResourceVersion` for free.
The invariant test (`resources_merge_invariant_test.go`) fails the
build if a PR accidentally puts `db:"verified_at"` etc. on `Resource`.

UUIDv7 is generated app-side via `uuid.NewV7()` (`github.com/google/uuid`
v1.6+). PG 17.7 lacks native `uuidv7()` (PG 18 only); the future
migration path to `uuid` column type when PG 18 ships is a single
`ALTER TABLE ... TYPE uuid USING ...::uuid` per column.

## UpsertResources verify-path scope

There is no `ON CONFLICT … DO UPDATE` on `resources`: the verify path is a plain
`UPDATE` and a changed resource is a version SPLIT (an `UPDATE` of the
predecessor's `superseded_by` plus a full `INSERT`), both in `upsertResourcesTx`
(`resources_upsert.go`). Read off the statement at `resources_upsert.go:210`, the verify `UPDATE` writes
`verified_at`, `verified_by`, `name`, `region`, `zone`, `status`,
`account_name`, `managed_by_provider`, and clears `deleted_at`/`deleted_by` (a
re-seen resource lifts its archival tombstone). It does **not** write `tags` or
`attributes` — it cannot, since that branch is only reached when both already
compare equal — nor `discovered_at`/`discovered_by`, which belong to the chain
root. Adding a new mutable column means editing the verify UPDATE, the split
INSERT's column list, and its VALUES.

## Reference-discovered placeholders (`InsertResourcesIfAbsent`)

`InsertResourcesIfAbsent(resources)` runs **only** the first-discovery
`INSERT … ON CONFLICT DO NOTHING` path — never the verify or version-split
paths. That clause names **no conflict target**, so it is correct whatever
columns the current-by-natural-key index carries: an embedder may widen the key
(a multi-tenant host scoping it per workspace), and an inferred target must
match the live index exactly or the insert fails `42P10`. It is the
reference-discovery primitive: when a resolver sees a cross-tenant edge into an
account/subscription/project outside scan scope, it inserts an empty-attribute
(`{}`) row at that resource's **real self-node natural key** (so the edge's
`to_id` — the deterministic `root_id` — names a row that exists, though nothing
in the database enforces that since `006` dropped the edge FKs), then emits the
edge. If that target is later scanned (this run or a future one), its own
scanner calls `UpsertResources`, finds the placeholder as the current version,
and version-splits it `{}`→populated; the placeholder is preserved in history
and the edge keeps resolving across the split. The ON CONFLICT DO NOTHING makes
it non-destructive — a populated row is never reduced back to `{}`,
regardless of resolver-vs-scanner ordering. Do **not** use `UpsertResources`
for placeholders: it would version-split a populated row down to `{}`. Sole
callers today are the three cross-tenant resolvers (`resolveIAMRoleCrossAccountTrust`
+ `resolveOrganizationsManagementAccount` in aws, `resolveAuthorizationRelationships`
in azure, `resolveIAMPolicyRelationships` in gcp). There is no synthetic stub
type and no marker column — the version chain is the whole mechanism.

## FK constraint: resources require scan record

`resources.discovered_by` = FK to `scans(id)`; `verified_by` holds a scan id too but carries no constraint (`006` added it as a plain column). Any test inserting resources needs scan record in DB first. `newTestStore` (provider tests) handles — inserts scan with fixed ID `"00000000000000000000000000000000"`.

## ListResources filter shape

`store.ListResources(store.ResourceFilter{...})` — filter struct is `ResourceFilter`, not `ListFilter`. Multi-type filter is `Types []string`, not `Type string`. Two zero-value defaults bite: `IncludeManaged=false` silently filters provider-managed rows, and `Limit=0` falls back to 500. Passing `ResourceFilter{}` is NOT "give me everything" — set `IncludeManaged: true` and either a large `Limit` or paginate via `Offset` for whole-table reads.

Canonical "read every resource" idiom: `store.GraphAll` (`graph.go:451`) page-loops `ListResources` with `IncludeManaged: true` + `Limit: 5000` until an empty page returns. Reuse that shape from CLI commands that must evaluate the full population (e.g. `cmd/check.loadAllResources`).

## Wire shape ≠ storage shape

`Resource` stores `AttributesJSON` / `TagsJSON` as JSON strings (raw SDK marshal output) but `MarshalJSON` / `UnmarshalJSON` (`resources.go`) surface them on the wire as nested `attributes` / `tags` objects under camelCase keys (`nativeId`, `accountId`, ...) — camelCase since the v0.18.0 wire migration. Round-trips byte-stable via the matching UnmarshalJSON. Tests asserting JSON output must compare against the parsed shape, not Go field names. New JSON encoders should emit `[]Resource` directly — no per-call shape massaging.

**Schema contract — every documented key always present.** `Resource.MarshalJSON` (and the matching `resources_json_test.go::TestResource_MarshalJSON_AlwaysPresent`) emits every key listed under `disco check --help`: optional pointer fields render as `null` (not omitted), `tags` and `attributes` always render as objects (`{}` for empty / missing / malformed legacy blobs). Stripping `,omitempty` was the F6 fix from focus-group/SUMMARY.md — Rego authors and downstream consumers can traverse `input.attributes.X` / `input.tags.Y` without per-row presence guards. Don't reintroduce `,omitempty` on the contract fields.

Adding a field to `Resource` has three downstream touch-points: (1) `MarshalJSON`/`UnmarshalJSON` if it carries on the JSON wire; (2) `resourceToInput` in `internal/policy/policy.go` so Rego policies can see it; (3) `resourcesColumns`/`resourceRow` in `cmd/resources.go` for CSV. Skipping (2) silently hides the field from every Rego rule.

modernc/sqlite accepts SQLite URI parameters via `file:<path>?<params>` form. `OpenReadOnly` uses `mode=ro`; same shape extends to `cache=shared`, `_pragma=...`, etc. when needed.

## `applyPragmas(db, readOnly bool)` skips writer-only pragmas on RO opens

`journal_mode=WAL` and `synchronous=NORMAL` write the SQLite DB header. A read-only open (`OpenReadOnly`, `mode=ro`) errors with `attempt to write a readonly database (8)` when those pragmas fire — bricks `disco --db-readonly check` against a customer-supplied snapshot. RO callers pass `readOnly=true`; writer-only pragmas are skipped. FK + cache + mmap pragmas are safe on RO and stay applied.

## No `internal/policy` import in store package

`store` must not import `internal/policy` (or other downstream packages). Doing so creates `cmd → policy → store → policy` cycle. Keep store types bare (string/pointer fields, no `policy.Finding`); conversion between store rows and wire types lives in cmd-side helpers (`storedFindingToFinding`, `findingToStored` in `cmd/findings.go`).

## `Scan.StartedAt` in storage = RFC3339 since v0.31.0, and older rows keep the zoneless shape

`CreateScan` stamps `started_at` via `nowExpr` (`dialect.go`), which since v0.31.0 returns **RFC3339** (`2026-07-28T20:47:08Z`) on both dialects; the DB column carries that shape verbatim. It emitted a zoneless `YYYY-MM-DD HH:MM:SS` before, which is why readers stay tolerant: consumers doing `time.Time` math on `Scan.StartedAt` (or `Checkpoint.UpdatedAt`) use `store.ParseTimestamp(s) (time.Time, bool)`, which accepts both shapes, or `store.ToRFC3339(s)` for the string form. Do not hardcode either layout at a call site — **nothing rewrites the old rows** (`migrations/pg/016` is deliberately empty; it shipped a rewrite in v0.31.0 and gave it up in v0.31.1, because disco-saas FORCEs RLS on these tables and its migration connection sets no `app.workspace_id`, so the DML `42704`'d on every already-provisioned tenant schema), so a store written across the v0.31.0 boundary holds BOTH shapes at once. Every caller treats a parse failure as "no timestamp" rather than an error, so a too-strict parse fails silently.

The trailing `Z` is load-bearing, not cosmetic: disco-saas casts these TEXT columns with `::timestamptz`, and a zoneless string resolves against the session `TimeZone` instead of UTC. These columns are also compared and ordered **as TEXT** (here, and by a keyset cursor and an evidence-range filter in the SaaS), so both dialects must render identical bytes — pinned by `TestNowExpr_WritesRFC3339OnBothDialects` under `withDialects`.

**Wire shape is RFC3339.** `Scan.MarshalJSON` (added F5 fix) projects `startedAt` / `finishedAt` to RFC3339 before emitting, so `disco scans -o json` and `disco summary -o json | jq '.asOf'` carry parseable timestamps that match resource-row `discoveredAt` / `verifiedAt`. The wire envelope uses camelCase keys and drops the SQLite `*JSON` columns (`ProvidersJSON`, `ScopeJSON`, `MetaJSON`) in favour of parsed `providers` / `scope` / `meta` objects. Don't reach into `scans -o json` consumers expecting the legacy PascalCase shape.

## `scans.resource_count` = totalSeen, not totalNew

`CompleteScan` / `PartialScan` persist the count of rows the scan upserted (every row visited, including pre-existing). The insert-only `totalNew` value (return of `UpsertResources`) is printed at scan-end stdout but not persisted. Drift between scans is `disco diff`'s job, not a column on `scans`. Don't re-derive "what changed" from `resource_count` deltas.

## `ResolveResource` two-pass: exact → id-prefix → substring

Seed lookup (`graph blast`, `graph path`, `resources --id`) tries exact `native_id`/`name` first, then ID-prefix on the 32-hex resource ID (when arg is 4–31 lowercase hex), then `LIKE %arg%` on `native_id`/`name`. F12 fix for "the CLI's own short-ID prints don't round-trip as input." Disambiguators (`--provider`, `--type`, `--account`) narrow each pass; multi-row results surface as the existing ambiguity error. Each pass capped at 50 rows so substring-on-large-DB doesn't OOM. New callers should route through `ResolveResource` rather than rolling their own lookups — single source of truth.

## Cross-backend SQL: `s.exec`/`s.get`/`s.query`/`s.queryRow`/`s.selectAll`

Wrappers in `dialect.go` proxy `Exec/Get/Query/QueryRow/Select` on `s.ext()` (which returns `*sqlx.DB` or `*sqlx.Tx` — see `WrapTx` below) with auto-`Rebind`. Always use them for raw `?`-placeholder SQL — `s.db.Exec(...)` directly works on SQLite but breaks on Postgres (sqlx Rebind isn't auto-applied), and skips the tx-bound path. Squirrel queries pass `s.placeholder()` to `PlaceholderFormat(...)`. New code adding SQL to the store package follows both patterns.

**A store method carrying hand-written SQL needs `withDialects`, not `openTestStore`** (`relationships_many_test.go`) — the latter is SQLite-only, and SQLite is the PERMISSIVE dialect, so a SQLite-only test can certify a query that cannot execute on Postgres at all. `CountManaged` shipped as `WHERE managed_by_provider = 1`: SQLite has no boolean type and stores 0/1, so it matched, while Postgres raised `operator does not exist: boolean = integer` (42883) and failed the entire call. It survived because `disco check` is the only caller and the hosted product never runs that command. Same class: `?`-vs-`$n` placeholders, `json_extract` vs `->>`, string/number coercion. `withDialects` skips its Postgres subtest when Docker is unreachable — check the run actually reported `--- PASS: …/postgres` and did not skip, or the extra coverage is imaginary.

## PG session GUCs accept placeholders only via `set_config(...)`

`SET app.tenant_id = $1` errors at parse time. The parameterised form is `SELECT set_config('<key>', $1, false)` (third arg = is_local; false = session-scoped). This is the shape a `WithAfterConnect` hook (disco-saas, see below) uses for per-conn GUC writes. The request-path form `SET LOCAL app.tenant_id = $1` works only inside a transaction — different surface, same constraint.

## Postgres backend

Single `*Store` covers both SQLite and Postgres; `OpenPostgres(ctx, dsn, ...PGOption)` (`postgres.go`) opens a pgx-backed `*sqlx.DB`. The `driver` field selects per-call dialect via three helpers in `dialect.go`:

- `s.placeholder()` — `sq.Question` for SQLite, `sq.Dollar` for Postgres. Squirrel queries use this; raw `?` SQL goes through `s.exec` / `s.get` / `s.selectAll` / `s.queryRow` / `s.query` wrappers that auto-rebind via `db.Rebind`.
- `s.tagJSONFilter(key)` — emits `json_extract(tags, '$.k')` (SQLite) or `tags ->> 'k'` (Postgres).
- `s.tagJSONValueExists()` — `json_each(tags)` vs `jsonb_each_text(tags)`.

Other portability rules baked in:
- `INSERT OR IGNORE` was replaced with `INSERT ... ON CONFLICT DO NOTHING`, which both backends support. Writes on `resources`, `quotas`, `relationships` and `hierarchy_closure` name **no conflict target**: an inferred target must match a live unique index exactly, and an embedder may widen those keys (a multi-tenant host scoping them per workspace), which would then fail `42P10`. `scans` and `scan_checkpoints` keep their targets — `scans (id)` and the four-column `scan_checkpoints (scan_id, provider, service, scope)`, both led by a globally unique scan id, so widening their KEYS would buy an embedder nothing. They are still RLS-scoped like every other scan-data table; this is about the key, not about isolation. New writes follow the same rule.
- `recordHierarchyTx` and friends accept `*sql.Tx` but pass through `s.rebind(...)` first because tx itself is unaware of the driver.

### Single-tenant backend + `WithAfterConnect` extension point

The PG backend is **single-tenant**: a plain pool against one schema, migrations carry no `tenant_id` column and no RLS. `OpenPostgres(ctx, dsn)` with no options is what the CLI uses (`cmd/helpers.go`, gated on `DISCO_PG_DSN`).

Multi-tenancy is the disco-saas control plane's job — it consumes disco as a module and layers `tenant_id`, per-table RLS policies, `FORCE ROW LEVEL SECURITY`, the per-tenant scan-notify trigger, and schema-per-tenant (`CREATE SCHEMA` + `search_path` pinning) via its **own** migration set and connection plumbing. The single seam disco exposes for that is:

```go
store.OpenPostgres(ctx, dsn, store.WithAfterConnect(func(ctx, c *pgconn.PgConn) error {
    // SET search_path = tenant_<hex>, public
    // SELECT set_config('app.tenant_id',    '<uuid>', false)
    // SELECT set_config('app.workspace_id', '<uuid>', false)
}))
```

`WithAfterConnect` registers a hook run once per physical connection, before any handle is returned to database/sql, so anything it sets (search_path, session GUCs) is sticky for every query through that conn. It composes with RDS IAM auth, which lives on pgx's separate `BeforeConnect` phase. disco keeps no tenant/schema/quoting logic — schema-name validation, identifier quoting, and `CREATE SCHEMA` bootstrap all live in disco-saas's hook. `TestPG_WithAfterConnect` (`postgres_test.go`) pins that the hook fires.

**Reads project explicit columns, never `SELECT *`.** disco-saas overlays its RLS columns (`tenant_id`, `workspace_id`) onto the shared `resources` / `scans` / `relationships` / `check_runs` / `findings` tables, so a `SELECT *` from the single-tenant store would scan a column the store struct has no field for (`missing destination name`). Every shared-table read therefore selects an explicit list that **omits** the RLS columns — `resourceSelectColumns()` (`resources_hooks.go`), `scanColumns` (`scans.go`), `relationshipColumns` (`relationships.go`), `checkRunColumns` / `findingColumns` (`findings.go`). The `WorkspaceID` struct fields stay for documentation but are unprojected (always nil); there is no `TenantID` field. New shared-table reads must follow the same pattern — add a column to the projection const when you add one to the struct, and never reintroduce `SELECT *`.

### `WrapTx` — tx-bound `*Store`

`WrapTx(tx *sqlx.Tx, drv Driver) *Store` returns a `*Store` whose dialect helpers run against the caller-owned tx instead of a pool. `Close()` is a no-op; the caller owns commit/rollback. Generic primitive — disco-saas's request path uses it after pinning `search_path` + RLS GUCs on the tx:

```
BEGIN
SET LOCAL search_path  = tenant_<hex>, public
SET LOCAL app.tenant_id = '<uuid>'
SET LOCAL app.workspace_id = '<uuid>'
st := store.WrapTx(tx, store.DriverPostgres)
<upstream read methods>
COMMIT
```

`SET LOCAL` is reset at COMMIT, so RDS Proxy still multiplexes the underlying conn. Intended primarily for reads — write methods that call `s.db.Begin*` directly (`UpsertResources`, `UpsertRelationships`, `RecordHierarchyBatch`, finalise calls) panic on a nil pool. That panic is intentional; scan workers run a real `OpenPostgres` pool. The one sanctioned write on the wrapped-tx path is archival: `ArchiveResource` / `RestoreResource` branch on `s.tx` (`resources_archive.go`) and run their tombstone / restore write on the caller-owned transaction when wrapped, so a request path may archive/restore under its RLS tx even though the bulk writers stay pool-only.

`*Store` holds both `db *sqlx.DB` and `tx *sqlx.Tx`. `s.ext()` returns whichever is non-nil; dialect helpers route through it. Driver tag is set by the constructor (`store.DriverPostgres` / `store.DriverSQLite`) — without it, placeholder format and JSON-extract dialect can't be selected.

### Connection pool is always bounded

`OpenPostgres` always calls `boundPool` — there is no unbounded path. Size resolves by
precedence: `WithMaxConns(n)` → `DISCO_PG_MAX_CONNS` → `pgDefaultMaxConns` (10). Malformed or
non-positive values at either layer fall through to the next rather than erroring; a typo in
deployment config must not stop a scan, and the fallback is still bounded.

**Why a default exists at all.** `database/sql`'s zero value is *unlimited*, and disco is a very
concurrent writer: the AWS scanner alone fans out `maxConcurrentServices × maxConcurrentRegions`
(10 × 5 = 50) service goroutines, each batch-upserting. Unbounded, one scan can demand ~50
simultaneous cold connections — TLS handshake each, plus an IAM token mint per dial under
`DISCO_PG_IAM_AUTH` — enough to exhaust a modest RDS `max_connections` or queue past the write
deadline. That was a real production failure, and it surfaced as store-write timeouts.

**`MaxIdleConns` tracks `MaxOpenConns`** (changed from the old `min(n, 2)`). Holding idle far
below open makes a bursty writer re-dial constantly — return 10, keep 2, re-handshake 8 on the
next batch — and re-dialing is the expensive operation. The cost is that a task holds up to `n`
idle conns after finishing instead of 2, which matters behind RDS Proxy where a pinned connection
maps 1:1 onto a backend one. `pgConnMaxIdleTime` is the release valve and is deliberately short
(90s, down from 5m): mid-scan the conns are hot and never idle that long, so reuse is unaffected,
but a finished task drains fast. Neither lifetime constant may be zero — 0 means "no bound".
`TestPGLifetimeBoundsAreNeverUnbounded` pins all of that.

**disco owns the floor; the deployment owns the number.** disco can't know RDS `max_connections`
divided across concurrent scanner tasks, so a multi-task deployment (disco-saas) should pass an
explicit `WithMaxConns` rather than inherit the default.

### RDS Proxy session-pinning trade-off

A `WithAfterConnect` hook that issues session-scoped `SET`/`set_config` (`is_local=false`) at conn open is treated by RDS Proxy as session pinning — that conn stops participating in multiplexing for its lifetime. For one-shot single-tenant containers this is fine (every conn serves the same tenant; pinning is the same as not pinning). A deploy sharing a Proxy across tenants should instead set GUCs as `SET LOCAL` inside a per-query transaction (the `WrapTx` request-path shape above) and skip the AfterConnect hook.

### Migration parity

`store/migrations/*.sql` (SQLite) and `store/migrations/pg/*.sql` (Postgres) must converge on **identical** `(table, column)` sets — the schema is single-tenant, so there are no allowed PG-only columns. `make check-migrations` (script: `scripts/check-migrations.sh`) extracts column lists from each set and diffs them. Add a column on one side, the script fails. CI gates this; reviewers also. (The SaaS multi-tenant columns — `tenant_id` + RLS plumbing — live in disco-saas's own migration set, not here.) Column **types** may diverge by design where PG has a richer native type: `tags`/`resources.attributes`/`relationships.attributes` are JSONB on PG but TEXT on SQLite, and `scans.errors` likewise — the parity check is column-presence, not type, and the Go fields are `string`/`*string` either way (pgx round-trips JSONB ↔ string).

PG migration runner is hand-rolled in `migrate_pg.go`: same `schema_migrations` bookkeeping and NNN_name.sql convention as `migrate.go`, but **NOT its `splitStatements` semicolon split** — `applyOnePG` executes each file whole (see § Migrations). Per-migration BEGIN+exec+INSERT+COMMIT means partial failure leaves a clean state. **Consequence for authors: a `;` or `--` inside a string literal in a PG migration is now harmless.** It was not before — `splitStatements` tracks `--` comments and `$$` quoting but not single-quoted literals, so a `;` in `COMMENT ON … IS '…'` split mid-string and raised 42601 (shipped once, v0.20.2). The constraint still binds `migrations/` (SQLite), which still splits.

## Scan-path writes: `withWriteRetry` + the `ErrStoreWrite` sentinel

Every scan-path writer runs its transaction inside `s.withWriteRetry(op, fn)` (`retry.go`):
`UpsertResources`, `InsertResourcesIfAbsent`, `UpsertRelationship`, `UpsertRelationships`,
`RecordHierarchy`, `RecordHierarchyBatch`. Outcomes — first try OK: silent. Recovered after a
retry: nil error plus a `ScanWarning{Provider:"store", Service:"write", Scope:op}` (the data
IS persisted). Exhausted or non-retryable: an error satisfying `errors.Is(err, ErrStoreWrite)`,
which provider dispatchers report as a hard scan error. A new scan-path writer must be wrapped
too — an unwrapped one leaks a bare DB error and gets masked (see next paragraph).

**`storeWriteError` formats the cause with `%s`, never `%w` — this is load-bearing.** The AWS
dispatcher's `isTransientNetworkError` matches `net.Error` timeouts, `io.EOF` and
`io.ErrUnexpectedEOF`, and pgconn reports a dead Postgres connection as exactly those. If the
cause stayed in the `errors.Is`/`errors.As` chain, a DB outage would be reclassified as a benign
transient cloud warning while every row silently vanished. Don't "tidy" it to `%w`.
(Mirrored in `internal/providers/aws/CLAUDE.md`.)

**The retried unit must publish no shared state.** An atomic bumped inside a transaction cannot
be un-bumped by the rollback, so a retry double-counts. `upsertResourcesTx` /
`insertResourcesIfAbsentTx` therefore *return* their new/changed counts and the exported wrapper
publishes to `s.upsertNew` / `s.upsertChanged` only after the commit; `activeCounter` is likewise
bumped outside the retry. Same rule for warnings: `UpsertResources`' preprocessing loop
(`redact.Apply` / `volatile.Apply` / `managed.Is` / `noteNativeIDType`) stays *outside*
`withWriteRetry` because `noteNativeIDType` emits the duplicate-native_id `ScanWarning`.
Guarded by `TestResourceWriteTxHelpersPublishNoSharedCounters`.

**`WrapTx` stores get exactly one attempt.** The transaction belongs to the caller; on Postgres
the first failed statement aborts it and every later command returns 25P02, so a retry could only
bury the real cause. Retry is a pool-backed-store behavior.

**Reproducing a write failure locally.** `applyPragmas` sets no `busy_timeout`, so SQLite returns
`SQLITE_BUSY` immediately rather than waiting — hold an exclusive lock from another process
(`sqlite3` connection, `BEGIN EXCLUSIVE`) against the scan's DB and every concurrent write fails
at once. A lock held past the retry budget (~300ms) produces the hard-error path; a ~200ms lock
produces `store write … recovered after 2 attempts` with the data still persisted. Start the lock
*after* the scan begins: `CreateScan` and `PartialScan` are startup/finalize writes outside
`withWriteRetry`, so locking first just fails the run at `create scan record`.

`isRetryableDBError` classifies **connection** failures only — PG SQLSTATE `08*`/`57P01-03`/`53300`,
SQLite `SQLITE_BUSY`/`SQLITE_LOCKED` (compare `Code() & 0xff`; extended codes carry detail in the
high bits), `driver.ErrBadConn`, EOFs, `net.Error` timeouts. Constraint/syntax errors and
`context.Canceled` are **not** retryable: they can never succeed, and burning the budget just
delays the report. A circuit breaker (`writeFailStreak`, 5 consecutive connection failures) drops
to single-attempt so a genuinely dead DB fails fast instead of paying backoff on every one of a
scan's thousands of writes; any success closes it. The field is a `*atomic.Int64` so
`WithUpsertCounters`-style shallow copies share one breaker — and it is nil-guarded, because
`OpenReadOnly` and `WrapTx` don't set it.

## Denylist filters via `sq.NotEq`

`squirrel.NotEq{"col": []string{...}}` emits `col NOT IN (?, ?, ...)`. Mirror of `sq.Eq` allowlist; use for any new exclude-X filter on `ResourceFilter` rather than hand-rolled OR-NOT chains. Precedent: `ExcludeTypes` (resources.go).
