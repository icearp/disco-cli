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
CGO_ENABLED=0 go test ./internal/store/... -run TestFoo -v

# Vet and lint
go vet ./...

# Format before commit (project gofmt config rewrites init() one-liners; run before each commit to avoid linter drift)
gofmt -w .
```

## Architecture

`disco` = cloud resource discovery CLI (cobra + viper). Scan AWS accounts, Azure subs/resource groups, GCP orgs/folders. Resolve + store resource relationships in local SQLite.

### Key constraint: CGO_ENABLED=0 always

Storage: `modernc.org/sqlite` — pure-Go SQLite transpile. Cross-platform single-binary, no C toolchain. **Never swap for `mattn/go-sqlite3` or any CGO dep.**

### Data flow

```
cmd/scan.go  →  internal/providers/<provider>/  →  internal/store/
```

### Per-service API mandate

Providers make **per-service API calls** via each cloud's native Go SDK. No unified discovery APIs (AWS Resource Explorer, Azure Resource Graph, GCP Cloud Asset Inventory). Every AWS service, Azure `arm*` package, GCP service client called direct. Needed for full coverage.

### CLI subcommands (summary)

`disco scan|list|diff|graph|check`. Details: `cmd/CLAUDE.md`.

### Resource type naming

Namespaced lowercase: `aws:ec2:instance`, `azure:compute:virtual-machine`, `gcp:compute:instance`.

### Config and DB path

Viper reads `xdg.ConfigHome/disco/config.yaml`, env prefix `DISCO_`. `--db` flag (or `$DISCO_DB`) overrides DB path; default `xdg.DataHome/disco/disco.db`. Linux: `~/.config/disco/` + `~/.local/share/disco/`. macOS/Windows: both collapse to platform app-data dir. Paths resolved via `github.com/adrg/xdg` in `cmd/paths.go` (`configDir()`, `dataDir()`). `defaultDBPath()` = pure getter — directory creation is `store.Open()` job.

## Nested guidance

Path-scoped `CLAUDE.md` files auto-load when working in subtrees:

- `cmd/CLAUDE.md` — CLI subcommand details, parallel scan orchestration, blank imports
- `internal/store/CLAUDE.md` — schema, edge kinds, scrubbing/redaction, ResourceID, migrations, UpsertResources scope, DB perms
- `internal/providers/CLAUDE.md` — registry, Scanner iface, add-provider steps, file naming, sidecar pattern, embed-child-data, registration tests, resolver test pattern
- `internal/providers/aws/CLAUDE.md` — AWS-specific resolver/scanner conventions (ARN helpers, KMS, IAM, ELBv2, Route53, paginators, Smithy, transient errors, etc.)
- `internal/providers/azure/CLAUDE.md` — Azure-specific helpers (azPageScan, rgHierarchyPair, vault-URI parsers), case-insensitive ARM-ID rule, MSI consumer resolver, sub-scoped vs tenant-scoped pattern
- `internal/providers/gcp/CLAUDE.md` — GCP-specific (per-project fan-out, scopes-above-project gap, IAM policy synth-resource shape, permission-denied handling, NativeID conventions)

## OSS / paid split

Two build modes: default (OSS) and `-tags paid` (closed-source upstream). `make build` / `make build-paid` / `make test-paid` cover both. CI runs both `go test ./...` and `go test -tags paid ./...` — don't break either.

- Paid-only files: name `*_paid.go` (or `*_paid_test.go`) **and** first line `//go:build paid`. Both required — `scripts/oss-sync.sh` excludes by name pattern + content scan.
- OSS stub for paid pkgs: `<pkg>.go` with `//go:build !paid` (e.g. `internal/license/license.go`). Stubs ship to OSS; `_paid.go` siblings do not.
- Paid commands: first line of `RunE` must be `if err := license.Require(); err != nil { return err }`. Canonical shape: `cmd/diff_paid.go`.
- Bug fixes / new free features: edit untagged files normally — flow downstream to OSS via next `make oss-sync`.
- Paid-only docs: name `*_paid.md` — excluded by `scripts/oss-sync.sh` name pattern. Canonical: `ROADMAP_paid.md`. Do not cross-reference from OSS-tracked files.

### Verifying paid-only deps don't leak

After adding a heavy dep behind `//go:build paid`, confirm OSS build doesn't pull it: `go list -deps . | grep <module>` should be empty; `go list -tags paid -deps . | grep <module>` should be non-empty. Every importer of the dep must carry the `paid` build tag, otherwise the OSS binary still links it.

### Demoting a paid feature to OSS

Mirror of promotion. Four touches: (1) rename `*_paid.go` **and any `*_paid_test.go` sibling** → drop `_paid` suffix; (2) strip `//go:build paid` line from each; (3) delete the `license.Require()` block at top of `RunE`; (4) `go mod tidy` (flips formerly-paid deps from `// indirect` to direct OSS). Also un-tag any `internal/<pkg>/*.go` + `*_test.go` the command imports. Easy miss: leaving the test file tagged silently drops OSS coverage — `go test ./...` still passes because the tests just don't compile in. Verify both `go test ./...` and `go test -tags paid ./...` green.

After build-tag edits, gopls may report stale `BrokenImport` diagnostics — trust `go build` / `go test` output, not the LSP.

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

### Don't enable `tagliatelle`

Conflicts with two project conventions: AWS resolver structs deliberately use PascalCase JSON tags to match SDK marshal output (see `internal/providers/aws/CLAUDE.md`), and the `disco check` Rego input contract uses snake_case (`cmd/CLAUDE.md`). Enabling produces hundreds of false-positives that, if "fixed", silently break unmarshalling.

### Bulk `revive` var-naming sweeps

Resolver-local struct fields rename safely with per-file `re.sub(r'\b' + old + r'\b', new, text)` because explicit `json:"OldName"` tags preserve wire format. Watch one collateral case: bare-suffix renames like `Id→ID` also rewrite SDK call-site accesses (e.g. `a.Id` on an `organizationstypes.DelegatedAdministrator`). Build immediately after a bulk apply — compile error names the offending file/line, revert that site only.

## Solution Rules

1. **KEEP THINGS SIMPLE**
2. No reinvent wheel.
3. Comment everything.
4. Human-readable code.
5. No redundant code.
6. First optimize scan speed, then min memory + CPU.
7. Keep deps minimal.
8. Minimize token use. No re-read source already in context. Use sed, grep, head, tail cut lines during discovery + implementation.
