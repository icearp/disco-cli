# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

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

`disco` is a cloud resource discovery CLI (cobra + viper) that scans AWS accounts, Azure subscriptions/resource groups, and GCP organizations/folders, then resolves and stores relationships between all discovered resources in a local SQLite database.

### Key constraint: CGO_ENABLED=0 always

The storage engine is `modernc.org/sqlite` — a pure-Go transpilation of SQLite. This is a deliberate choice that enables cross-platform single-binary distribution without C toolchains. **Never replace it with `mattn/go-sqlite3` or any CGO dependency.**

### Data flow

```
cmd/scan.go  →  internal/providers/<provider>/  →  internal/store/
```

Provider scanners call `store.UpsertResources()`, `store.UpsertRelationship()`, and `store.AddToHierarchyClosure()` to persist discovered resources.

### Per-service API mandate

Providers make **individual per-service API calls** using each cloud's native Go SDK. Do not use unified discovery APIs (AWS Resource Explorer, Azure Resource Graph, GCP Cloud Asset Inventory). Every AWS service, Azure `arm*` package, and GCP service client is called directly. This is required for complete resource coverage.

### CLI structure

- `disco scan` — runs all registered providers in parallel
- `disco scan <provider>` — runs a single provider (e.g. `disco scan aws`)
- `disco list` — queries the local database with optional filters (`--provider`, `--type`, `--region`, `--status`, `--tag-key`/`--tag-value`, `--output table|json`)

### Provider registry (`internal/providers/registry.go`)

Each provider self-registers via `init()` by calling `providers.Register(s Scanner)`. The `Scanner` interface requires two methods:

```go
Name() string
Scan(ctx context.Context, st *store.Store, scanID string) error
```

`providers.All()` returns all registered scanners sorted by name. `providers.Get(name)` is used for validation. `providers.Names()` is used in error messages.

**Adding a new provider** (three steps):
1. Create `internal/providers/<name>/` implementing `Scanner`
2. Call `providers.Register(&MyScanner{})` in the package's `init()`
3. Add `_ "codeburg.org/icearp/disco/internal/providers/<name>"` to `cmd/providers.go`

`cmd/providers.go` holds all blank imports. `cmd/scan.go`'s `init()` iterates `providers.All()` to build `disco scan <name>` subcommands — no changes to `scan.go` are needed when adding a provider.

### Parallel scanning

`cmd/scan.go` runs all selected scanners concurrently via `errgroup.WithContext`. The first scanner error cancels all siblings. On any error the scan record is marked failed via `db.FailScan`; on success it is marked complete via `db.CompleteScan`.

### Storage layer (`internal/store/`)

Four tables: `resources`, `relationships`, `hierarchy_closure`, `scans`.

- **`resources`**: One row per discovered cloud entity. `attributes` (JSON blob) holds the full provider-specific API response. `tags` (JSON) is denormalized for efficient `json_extract()` queries. `parent_id` is the immediate parent in the provider hierarchy (e.g. GCP folder → project).
- **`relationships`**: Directed edges between resources. `kind` values: `contains`, `attached-to`, `uses`, `routes-to`, `peer`, `assumes`.
- **`hierarchy_closure`**: Closure table enabling O(1) "all descendants of node X" queries without recursive CTEs. Must be populated via `AddToHierarchyClosure(childID, parentID)` whenever a resource with a `parent_id` is upserted.
- **`scans`**: Lifecycle record per scan run (created at start, updated on complete/fail).

Queries are built with `squirrel` (`sq.Select(...).Where(...)`) to avoid string interpolation. `sqlx` handles struct scanning. Raw SQL is used for CTEs and anything squirrel doesn't express cleanly.

### Resource IDs

`ResourceID(provider, accountID, type, nativeID)` — `internal/store/resources.go` — produces a 32-hex-char SHA-256 prefix. Stable across rescans; this is the primary key.

Scan IDs are generated with `crypto/rand` + `encoding/hex` (same 32-char hex format). No `uuid` dependency.

### Resource type naming

Namespaced lowercase strings: `aws:ec2:instance`, `azure:compute:virtual-machine`, `gcp:compute:instance`.

### Migrations

SQL files in `internal/store/migrations/` are embedded at compile time via `//go:embed`. File names must be `NNN_description.sql` (e.g. `002_add_foo.sql`). The runner splits on semicolons and executes each statement individually — SQLite's `database/sql` driver silently ignores everything after the first statement in a multi-statement `Exec` call.

### Config and DB path

Viper reads `~/.disco/config.yaml` with env prefix `DISCO_`. The `--db` flag (or `$DISCO_DB`) overrides the database path; default is `~/.disco/disco.db`. `defaultDBPath()` is a pure getter — directory creation is `store.Open()`'s responsibility.

## Final Notes

1. **KEEP THINGS SIMPLE**
2. Do not "reinvent the wheel."
3. Comment everything.
4. Write code that is easy for humans to read.
5. Do not write redundant code.
6. Optimize first for speed, then for minimum memory and CPU consumption.
7. Keep dependencies minimal.
