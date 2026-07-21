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
#   AWS: --regions us-east-1,us-west-2  (or --regions all for every opted-in region)
#   Azure / GCP: no --regions flag (Azure scopes per subscription/RG; GCP per project)

# Run tests
CGO_ENABLED=0 go test ./...

# Run a single test
CGO_ENABLED=0 go test ./store/... -run TestFoo -v

# Vet and lint
go vet ./...
golangci-lint run --max-issues-per-linter 0 --max-same-issues 0

# Guard SQLite ↔ Postgres migration parity (single-tenant schema: the two
# dialects' column sets must match exactly — no PG-only allowlist; the SaaS
# multi-tenant columns live in disco-saas's own migration set)
make check-migrations

# Format before commit (project gofmt config rewrites init() one-liners; run before each commit to avoid linter drift)
gofmt -w .
```

Version stamp: `make build` injects `git describe --tags --always --dirty=+dirty` via `-X cmd.Version` ldflag (canonical release path). Plain `go build .` from a git checkout now falls back to `runtime/debug.ReadBuildInfo()` — uses `vcs.revision[:12]` plus `+dirty` when the worktree has uncommitted changes. Falls through to the literal `dev` only when neither ldflag nor build-info is available (e.g. `go test`, `go install` from a tarball without VCS info). SARIF `tool.driver.version`, snapshot `manifest.tool_version`, and `disco --version` all read `cmd.Version` — single source of truth.

## Architecture

`disco` = cloud resource discovery CLI (cobra + viper). Scan AWS accounts, Azure subs/resource groups, GCP orgs/folders. Resolve + store resource relationships in local SQLite.

### Key constraint: CGO_ENABLED=0 always

Storage: `modernc.org/sqlite` — pure-Go SQLite transpile. Cross-platform single-binary, no C toolchain. **Never swap for `mattn/go-sqlite3` or any CGO dep.**

### text/template defeats linker DCE (binary-size landmine)

`text/template` (and `html/template`) execution calls `reflect.Value.MethodByName` with a
non-constant name inside `text/template.(*state).evalField`. The Go linker treats any
**reachable** non-constant `MethodByName` as a signal to disable per-method dead-code
elimination for the **entire binary** — one whole-link-unit `reflectSeen` flag in
`cmd/link/internal/ld/deadcode.go`. Once tripped, the full AWS SDK v2 method surface is
retained: the `-tags "slim aws"` build balloons from ~199MB to ~780MB (default build worse).
This is per-link-invocation, so it is a property of *this* binary's reachable graph.

Rule: **never make `text/template`/`html/template` reachable from disco's own code.** For any
label/format templating over a fixed field set, use `strings.NewReplacer` / `fmt.Sprintf`
instead (precedent: `cmd/graph.go:nodeLabel`, `--label-template`).

Three known reachable call sites — **all now closed**:
1. `cmd/graph.go:nodeLabel` (`--label-template`) — **fixed** (plain substitution).
2. OPA's vendored `internal/gojsonschema.formatErrorDescription`, reachable via
   `policy.NewEngine → ast.Compiler.Compile → Compiler.init → loadSchema` — **fixed upstream**.
   OPA merged a self-vendored methodless copy of `text/template` (`internal/methodlesstemplate`)
   for the schema-error formatter. `go.mod` currently pins OPA to that unreleased main-branch
   commit (see the pin comment on the `open-policy-agent/opa` require line) until OPA cuts a
   tagged release containing it — drop the pin then.
3. `google.golang.org/grpc` (pulled in transitively by the GCP SDK) imports
   `golang.org/x/net/trace`, whose `init()` unconditionally calls
   `http.HandleFunc("/debug/requests", Traces)` / `http.HandleFunc("/debug/events", Events)` —
   reachable regardless of the runtime `grpc.EnableTracing` bool, and `trace.Events` →
   `RenderEvents` → `html/template.Execute` → `text/template.execute`. This is **independent**
   of #2 and was masked by it: fixing #2 alone only dropped the default build from ~942MB to
   ~899MB, because #3 alone is enough to keep DCE dead. grpc ships exactly the build tag needed:
   `-tags grpcnotrace` (`trace_notrace.go`, `//go:build grpcnotrace`) strips the `x/net/trace`
   wiring entirely. Baked into `Makefile`'s `TAGFLAG` (always on, on top of any `TAGS=`) and into
   every `dist` target. Fixing #2 and #3 together took the default build from ~942MB to ~294MB,
   and `slim aws` from ~780MB (broken) to ~232MB.

Guard when investigating: `go build -tags grpcnotrace -ldflags=-dumpdep 2>deps.txt` then
`grep -c ' -> text/template.(\*Template).execute$' deps.txt` — must be `0`. `go tool nm <binary> |
grep -c evalField` is **not** a reliable health check on its own anymore: OPA's
`internal/methodlesstemplate` package reuses the same method names (`evalField`,
`evalFieldChain`) in its non-reflect copy, so a nonzero count there is expected and harmless —
only a nonzero `dumpdep` hit on `text/template.(*Template).execute` means DCE is actually dead.

### Data flow

```
cmd/scan.go  →  internal/providers/<provider>/  →  store/
```

### Per-service API mandate

Providers make **per-service API calls** via each cloud's native Go SDK. No unified discovery APIs (AWS Resource Explorer, Azure Resource Graph, GCP Cloud Asset Inventory). Every AWS service, Azure `arm*` package, GCP service client called direct. Needed for full coverage.

### CLI subcommands (summary)

`disco scan|list|diff|graph|check`. Details: `cmd/CLAUDE.md`.

### Resource type naming

Namespaced lowercase: `aws:ec2:instance`, `azure:microsoft.compute:virtual-machines`, `gcp:compute:instance`.

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
- Release SBOMs: each tagged release attaches a per-binary CycloneDX (`.cdx.json`) + SPDX (`.spdx.json`) SBOM beside the `.sha256` sidecar, generated by `syft` from the binary's Go buildinfo. `make sbom` reproduces them locally into `dist/`. Generated from the **raw binary before upx/xz** so the SBOM derives straight from the Go build (stdlib `go version -m` can't read a upx-packed binary — syft happens to see through it, but we don't rely on that); both the Makefile target and the CI step (`.forgejo/workflows/release.yaml`) run before the compression steps. `SYFT_VERSION` is pinned in both places (keep in sync), and the emitted spec versions are pinned in the `-o` selectors (`cyclonedx-json@1.7`, `spdx-json@2.3`) so a syft bump can't silently reshape the output. syft is invoked via `go run …@version`, never added to disco's go.mod. Not embedded in the `disco snapshot` evidence archive (yet) — a signed-SBOM follow-up.
- Release vuln gate: the CI `test` job runs `govulncheck` against the shipped build config (`-tags grpcnotrace`, `CGO_ENABLED=0`); a **reachable** known vuln exits non-zero, failing `test` so `build`/`release` never run (release-blocking via the existing `build: needs: test`). Runs once per tag — the vuln DB (vuln.go.dev) is queried live, so pinning `GOVULNCHECK_VERSION` (kept in sync between `Makefile` and `.forgejo/workflows/release.yaml`) fixes the tool, not the data. `make vulncheck` mirrors it locally. Tool invoked via `go run …@version`, never in go.mod. Release-gate only for now — a continuous push/PR/cron workflow (catching vulns disclosed between tags) is a deliberate follow-up.
  - **Binary mode, not source mode** (`-mode binary` over a freshly built binary). Source mode (`govulncheck ./...`) builds whole-program SSA over disco's ~496-module graph — three full cloud SDKs — and needs **>23GB**, OOMing a 31GB workstation. Binary mode reads the symbol table instead: still symbol-level reachability, fits in <8GB. Measured, not assumed.
  - The scanned binary must be built **without `-w -s`**. `-s` strips the symbol table entirely (measured: 218530 symbols → `no symbol section`), and govulncheck does **not** error on that — it silently falls back to module granularity and reports whole modules as reachable, still under a `=== Symbol Results ===` header. The failure mode is therefore a **spurious red**, not a silent miss: the stripped build flags `golang.org/x/crypto/openpgp` as reachable (wildcard `openpgp/*` "symbols") where the unstripped build correctly reports it as required-but-not-called. Both `make vulncheck` and the CI step assert `go tool nm <binary>` succeeds first so this fails legibly instead of looking like a real finding someone would paper over with `continue-on-error`. `make vulncheck` depends on `build` (whose `$(LDFLAGS)` omits `-w -s`), not `dist`; CI builds a throwaway `vulnscan-target`. Stripping removes debug data, not code, so reachability computed on the unstripped binary holds for the shipped stripped one.
  - The `go` directive in `go.mod` is a **security floor**, not just a language-version pin — most reachable findings here are stdlib vulns fixed by a toolchain patch release. Bumping `go.mod` is the fix when the gate goes red on `Standard library` entries (precedent: 1.25.8 → 1.25.12 cleared 12 reachable stdlib vulns, worst GO-2026-5856 ECH privacy leak in `crypto/tls`).

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
