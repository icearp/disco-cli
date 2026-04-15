# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repo.

## Commands

```bash
# Build (CGO_ENABLED=0 is required — all builds must be CGO-free)
CGO_ENABLED=0 go build -o disco .

# Cross-compile for all targets from Linux
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64  go build -o dist/disco-linux-amd64 .
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64  go build -o dist/disco-darwin-arm64 .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64  go build -o dist/disco-windows-amd64.exe .

# Run tests
CGO_ENABLED=0 go test ./...

# Run a single test
CGO_ENABLED=0 go test ./internal/store/... -run TestFoo -v

# Vet and lint
go vet ./...
```

## Architecture

`disco` = cloud resource discovery CLI (cobra + viper). Scans AWS accounts, Azure subscriptions/resource groups, GCP orgs/folders. Resolves + stores resource relationships in local SQLite.

### Key constraint: CGO_ENABLED=0 always

Storage engine: `modernc.org/sqlite` — pure-Go SQLite transpilation. Enables cross-platform single-binary without C toolchains. **Never replace with `mattn/go-sqlite3` or any CGO dep.**

### Data flow

```
cmd/scan.go  →  internal/providers/<provider>/  →  internal/store/
```

Provider scanners call `store.UpsertResources()`, `store.UpsertRelationship()`, `store.BatchAddToHierarchyClosure()` to persist resources. Errors from all three must propagate — never silence with `_ =`.

### Per-service API mandate

Providers make **individual per-service API calls** via each cloud's native Go SDK. No unified discovery APIs (AWS Resource Explorer, Azure Resource Graph, GCP Cloud Asset Inventory). Every AWS service, Azure `arm*` package, GCP service client called directly. Required for complete resource coverage.

### CLI structure

- `disco scan` — runs all registered providers in parallel
- `disco scan <provider>` — runs single provider (e.g. `disco scan aws`)
- `disco scan --providers aws,gcp` — scans only named providers (comma-separated `StringSlice`)
- `disco list` — queries local DB with optional filters (`--provider`, `--type`, `--region`, `--status`, `--tag-key`/`--tag-value`, `--output table|json`)

### Provider registry (`internal/providers/registry.go`)

Each provider self-registers via `init()` calling `providers.Register(s Scanner)`. `Scanner` interface requires two methods:

```go
Name() string
Scan(ctx context.Context, st *store.Store, scanID string) error
```

`providers.All()` returns all registered scanners sorted by name. `providers.Get(name)` for validation. `providers.Names()` for error messages.

**Adding a new provider** (three steps):
1. Create `internal/providers/<name>/` implementing `Scanner`
2. Call `providers.Register(&MyScanner{})` in package `init()`
3. Add `_ "codeburg.org/icearp/disco/internal/providers/<name>"` to `cmd/providers.go`

`cmd/providers.go` holds all blank imports. `cmd/scan.go`'s `init()` iterates `providers.All()` to build `disco scan <name>` subcommands — no changes to `scan.go` needed when adding provider.

### Parallel scanning

`cmd/scan.go` runs all selected scanners concurrently via `errgroup.WithContext`. First error cancels all siblings. On error: scan record marked failed via `db.FailScan`. On success: marked complete via `db.CompleteScan`.

### Storage layer (`internal/store/`)

Four tables: `resources`, `relationships`, `hierarchy_closure`, `scans`.

- **`resources`**: One row per discovered cloud entity. `attributes` (JSON blob) holds full provider-specific API response. `tags` (JSON) denormalized for efficient `json_extract()` queries. `parent_id` = immediate parent in provider hierarchy (e.g. GCP folder → project). `verified_at` (RFC3339) and `verified_by` (scan ID FK) set automatically by `UpsertResources` — callers must not set them.
- **`relationships`**: Directed edges between resources. `kind` values: `contains`, `attached-to`, `uses`, `routes-to`, `peer`, `assumes`.
- **`hierarchy_closure`**: Closure table enabling O(1) "all descendants of node X" without recursive CTEs. Always populate via `BatchAddToHierarchyClosure(pairs)` (single transaction) after upserting resources with `parent_id`.
- **`scans`**: Lifecycle record per scan run (created at start, updated on complete/fail).

Queries built with `squirrel` (`sq.Select(...).Where(...)`) — no string interpolation. `sqlx` handles struct scanning. Raw SQL for CTEs and anything squirrel can't express cleanly.

### Resource IDs

`ResourceID(provider, accountID, type, nativeID)` — `internal/store/resources.go` — produces 32-hex-char SHA-256 prefix. Stable across rescans; primary key.

Scan IDs: `crypto/rand` + `encoding/hex` (same 32-char hex format). No `uuid` dep.

### Resource type naming

Namespaced lowercase strings: `aws:ec2:instance`, `azure:compute:virtual-machine`, `gcp:compute:instance`.

### Shared utilities (`internal/util`)

`util.MustJSON(v any) string`, `util.Sv(p *string) string`, `util.AllResources` (= `math.MaxUint32`, used as `Limit` in `ListResources` to fetch all rows). Each provider keeps unexported one-liner wrappers (`mustJSON`, `sv`) delegating to `util` — call sites clean, logic centralized.

### Provider file naming

Scanners in `<service>_scanners.go`, relationship resolvers in `<service>_resolvers.go`. `resolveRelationships` orchestrator in provider's top-level file (`aws.go`, `azure.go`, `gcp.go`).

### List-then-describe pattern (N+1 avoidance)

When AWS service returns only names from List API (EKS, DynamoDB), describe each resource concurrently via `errgroup` + `sync.Mutex` to collect results, then upsert batch. Don't call Describe sequentially in loop.

### Migrations

SQL files in `internal/store/migrations/` embedded at compile time via `//go:embed`. Names must be `NNN_description.sql` (e.g. `002_add_foo.sql`). Runner splits on semicolons, executes each statement individually — SQLite's `database/sql` driver silently ignores everything after first statement in multi-statement `Exec` call.

### Config and DB path

Viper reads `~/.disco/config.yaml` with env prefix `DISCO_`. `--db` flag (or `$DISCO_DB`) overrides DB path; default `~/.disco/disco.db`. `defaultDBPath()` = pure getter — directory creation is `store.Open()`'s job.

### Testing

**Test files exist** for: `internal/store/`, `internal/util/`, all three provider packages.

#### Writing tests for new services

Every new `<service>_resolvers.go` must have matching `<service>_resolvers_test.go`. Pattern:

1. Call `newTestStore(t)` — opens temp-file SQLite DB, inserts required test scan record.
2. Call `upsertTestResource(t, st, provider, accountID, rtype, nativeID, region, attrsJSON)` to insert resources. **Pass region** if resolver uses `sv(r.Region)` to build ARNs — omitting causes computed relationship IDs to point to phantom resources, FK error with no obvious diagnosis.
3. Call resolver function directly (tests in same package, e.g. `package aws`).
4. Assert via `st.RelationshipsFrom(id)`.

Always add "no attrs / empty case" test alongside happy-path — guards against nil-pointer panics on missing JSON fields.

#### FK constraint: resources require a scan record

`resources.discovered_by` and `resources.verified_by` are FKs to `scans(id)`. Any test inserting resources needs scan record in DB first. `newTestStore` handles this — inserts scan with fixed ID `"00000000000000000000000000000000"`.

#### UpsertResources ON CONFLICT scope

`UpsertResources` ON CONFLICT only updates: `name`, `status`, `tags`, `attributes`, `verified_at`, `verified_by`. Does **not** update `region`, `zone`, `account_name`, `discovered_at`. Set all fields on initial insert — second upsert can't patch them.

#### Registration tests

`internal/providers/<provider>/registration_test.go` holds `expectedAWSServices` / `expectedAzureServices` / `expectedGCPServices` — authoritative list of registered service names. **Update when adding new service scanner.** Test fails if service registered but not listed, or listed but not registered.

## Solution Rules

1. **KEEP THINGS SIMPLE**
2. No reinventing wheel.
3. Comment everything.
4. Write human-readable code.
5. No redundant code.
6. Optimize first for scan speed, then min memory + CPU.
7. Keep dependencies minimal.
8. Minimize token use. Don't re-read source already in context. Use sed, grep, head, tail to reduce lines during discovery and implementation.