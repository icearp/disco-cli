# CLAUDE.md

Guide Claude Code (claude.ai/code) in repo.

## ROADMAP.md is historical, not authoritative

Open-section bullets and per-session COMPLETED notes describe state at time of writing. Before treating an item as "deferred", grep for the named files / type constants — items often ship without the open-section bullet getting flipped.

## Commands

Primary branch: `dev` (origin no `main`). Feature branches fork from `dev`, merge back to `dev`.

```bash
# Build (CGO_ENABLED=0 is required — all builds must be CGO-free)
CGO_ENABLED=0 go build -o disco .

# Cross-compile for all targets from Linux
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64  go build -o dist/disco-linux-amd64 .
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64  go build -o dist/disco-darwin-arm64 .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64  go build -o dist/disco-windows-amd64.exe .

# Live scan flags differ by provider:
#   AWS: --regions us-east-1,us-west-2
#   Azure / GCP: no --regions flag (Azure scopes per subscription/RG; GCP per project)

# Run tests
CGO_ENABLED=0 go test ./...

# Run a single test
CGO_ENABLED=0 go test ./store/... -run TestFoo -v

# Vet and lint
go vet ./...
golangci-lint run --max-issues-per-linter 0 --max-same-issues 0

# Guard SQLite ↔ Postgres migration parity (PG-only RLS `tenant_id` + the
# external-control-plane scan-attribution columns are allowlisted)
make check-migrations

# Format before commit (project gofmt config rewrites init() one-liners; run before each commit to avoid linter drift)
gofmt -w .
```

Version stamp: `make build` injects `git describe --tags --always --dirty=+dirty` via `-X cmd.Version` ldflag (canonical release path). Plain `go build .` from a git checkout now falls back to `runtime/debug.ReadBuildInfo()` — uses `vcs.revision[:12]` plus `+dirty` when the worktree has uncommitted changes. Falls through to the literal `dev` only when neither ldflag nor build-info is available (e.g. `go test`, `go install` from a tarball without VCS info). SARIF `tool.driver.version`, snapshot `manifest.tool_version`, and `disco --version` all read `cmd.Version` — single source of truth.

## Architecture

`disco` = cloud resource discovery CLI (cobra + viper). Scan AWS accounts, Azure subs/resource groups, GCP orgs/folders. Resolve + store resource relationships in local SQLite.

### Key constraint: CGO_ENABLED=0 always

Storage: `modernc.org/sqlite` — pure-Go SQLite transpile. Cross-platform single-binary, no C toolchain. **Never swap for `mattn/go-sqlite3` or any CGO dep.**

### Data flow

```
cmd/scan.go  →  internal/providers/<provider>/  →  store/
```

### Per-service API mandate

Providers make **per-service API calls** via each cloud's native Go SDK. No unified discovery APIs (AWS Resource Explorer, Azure Resource Graph, GCP Cloud Asset Inventory). Every AWS service, Azure `arm*` package, GCP service client called direct. Needed for full coverage.

### CLI subcommands (summary)

`disco scan|list|diff|graph|check`. Details: `cmd/CLAUDE.md`.

### Resource type naming

Namespaced lowercase: `aws:ec2:instance`, `azure:compute:virtual-machine`, `gcp:compute:instance`.

### Config and DB path

Viper reads `xdg.ConfigHome/disco/config.yaml`, env prefix `DISCO_`. `--db` flag (or `$DISCO_DB`) overrides DB path; default `xdg.DataHome/disco/disco.db`. Linux: `~/.config/disco/` + `~/.local/share/disco/`. macOS/Windows: both collapse to platform app-data dir. Paths resolved via `github.com/adrg/xdg` in `cmd/paths.go` (`configDir()`, `dataDir()`). `defaultDBPath()` = pure getter — directory creation is `store.Open()` job.

## Coverage gap docs (`docs/`)

- `docs/aws-missing-services.md` — scanner-layer skip list (CFN types not scanned). Gitignored, per-dev. Read by `scripts/aws-next-service.sh`.
- `docs/aws-missing-resolvers.md` — resolver-layer gaps: audit workflow + orphan-types TSV (fenced block). Tracked. Refresh via `disco coverage resolvers --missing --providers aws`.

## Nested guidance

Path-scoped `CLAUDE.md` files auto-load when working in subtrees:

- `cmd/CLAUDE.md` — CLI subcommand details, parallel scan orchestration, blank imports
- `store/CLAUDE.md` — schema, edge kinds, scrubbing/redaction, ResourceID, migrations, UpsertResources scope, DB perms
- `internal/providers/CLAUDE.md` — registry, Scanner iface, add-provider steps, file naming, sidecar pattern, embed-child-data, registration tests, resolver test pattern
- `internal/providers/aws/CLAUDE.md` — AWS-specific resolver/scanner conventions (ARN helpers, KMS, IAM, ELBv2, Route53, paginators, Smithy, transient errors, etc.)
- `internal/providers/azure/CLAUDE.md` — Azure-specific helpers (azPageScan, rgHierarchyPair, vault-URI parsers), case-insensitive ARM-ID rule, MSI consumer resolver, sub-scoped vs tenant-scoped pattern
- `internal/providers/gcp/CLAUDE.md` — GCP-specific (per-project fan-out, scopes-above-project gap, IAM policy synth-resource shape, permission-denied handling, NativeID conventions)

## Bundled features of note

Single build, no feature gating — everything ships in this one binary.

- Bundled OPA Rego packs follow `<provider>-<framework>` naming under `internal/policy/<name>/`, surfaced via `disco check --packs <name>`. Ships `aws-waf` (5-rule AWS Well-Architected sample pack, one or two rules per pillar). Curated full packs — Well-Architected (complete), CIS-AWS-Foundations, NIST 800-53, PCI-DSS, ISO 27001 — and future `azure-waf` / `gcp-waf` are not yet bundled.
- Findings persistence: `disco check --persist` writes a check run + findings to the DB; `disco findings list/runs` query them (migration `002_findings.sql`). The tables stay empty until `--persist` is used. Drift analytics (`findings diff`, heatmaps, retention, ticket sync) can build atop the same schema.
- Evidence snapshots: `disco snapshot <output-file>` + `disco verify <archive>` produce/verify single-file archives (`.zip`, `.tar.gz`/`.tgz`, `.tar.xz`/`.txz` — format from extension; xz via pure-Go `github.com/ulikunitz/xz`). Manifest at `disco-snapshot/v1` shape (tool_version, db_sha256-of-inner-DB, generated_at, scans[] — each entry carries id/started_at/finished_at/scope). The `disco snapshot --signing-payload` / `disco verify --signature` ed25519 flow closes the unsigned-manifest gap; cosign/Sigstore-witnessed signing is a future follow-up.

## Go lint conventions

### gocognit threshold = 80

`.golangci.yaml` enables `gocognit` at `min-complexity: 80`. Most resolver/scanner funcs are linear "for each resource → check field A, field B, ..." walks — complexity scales with edge-kind count, not nesting. Splitting them into per-branch helpers hides the walk. Refactor only outliers above the bar. Precedents: `store.GraphWalk`, `aws.classifyPolicyResource`, `aws.resolveOpenSearchDomainTargets`, `gcp.resolveIAMPolicyRelationships`, `azure.resolveDiagnosticSettings`.

### golangci-lint flags

- v2 config (`version: "2"` at top of `.golangci.yaml`) — schema renamed in v2; missing version yields `unsupported version of the configuration`.
- Default caps output at 50 issues per linter. For a full survey: `golangci-lint run --max-issues-per-linter 0 --max-same-issues 0`.

### Go 1.25 modernizer lint

Repo surfaces `slicescontains`, `stringscut`, `rangeint` diagnostics. Prefer `slices.Contains(xs, v)`, `before, after, ok := strings.Cut(s, sep)`, `for i := range N` over manual equivalents.

### Loop-var copy unneeded (Go 1.22+)

`for _, x := range xs { g.Go(func() { ... x ... }) }` — no `x := x` shadow needed. Linter flags `forvar: copying variable is unneeded`. Per-iteration scope built in.

### `sync.WaitGroup.Go` (Go 1.25+)

Linter `waitgroup` flags `wg.Add(1); go func() { defer wg.Done(); ... }`. Use `wg.Go(func() { ... })` instead.

### `tagliatelle` is path-scoped

Enabled globally with the camelCase rule, but excluded under `linters.exclusions.rules` for convention zones where camelCase JSON would silently break unmarshalling. Current zones:
- PascalCase to match SDK marshal output: `internal/providers/.*\.go`.
- snake_case for disco wire contracts: `cmd/(summary|coverage|diff|findings)\.go`, `(internal/(coverage|policy|snapshot|serve)|store)/.*\.go`.

New packages emitting snake_case JSON extend the snake_case path pattern; provider scanners extend the PascalCase one. Otherwise the linter demands camelCase that breaks the wire format.

### Bulk `revive` var-naming sweeps

Resolver-local struct fields rename safely with per-file `re.sub(r'\b' + old + r'\b', new, text)` because explicit `json:"OldName"` tags preserve wire format. Watch one collateral case: bare-suffix renames like `Id→ID` also rewrite SDK call-site accesses (e.g. `a.Id` on an `organizationstypes.DelegatedAdministrator`). Build immediately after a bulk apply — compile error names the offending file/line, revert that site only.

## Solution Rules

1. **KEEP THINGS SIMPLE**
2. No reinvent wheel.
3. Comment non-obvious WHY only — invariants, hidden constraints, surprising behavior. Skip WHAT-comments; well-named identifiers explain that.
4. Human-readable code.
5. No redundant code.
6. First optimize scan speed, then min memory + CPU.
7. Keep deps minimal.
8. Minimize token use. No re-read source already in context. Use sed, grep, head, tail cut lines during discovery + implementation.
