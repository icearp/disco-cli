# CLAUDE.md

Guide Claude Code (claude.ai/code) in repo.

## Plans ship in phases; every phase ends reviewed, documented, committed

**Plan structure.** Every plan groups work into logical, distinct phases that build on each
other. A phase is a coherent unit that leaves the tree buildable and green — not an
arbitrary slice of the file list. Order phases so later ones depend on earlier ones, and
state that dependency in the plan.

**Per-phase exit sequence.** On completing a phase, run these in order — do not skip ahead
and do not batch phases together:

1. **Adversarial review** the phase's changes *and any code they impact* — not just the
   diff. Hunt for the failure the change enables, not confirmation it looks right.
2. **Fix every finding** from that review. A finding is closed by a fix or by an explicit,
   recorded reason it is not a defect — never by silence.
3. **Update `CLAUDE.md`** via the `claude-md-management:revise-claude-md` skill, recording
   what the phase taught (invariants discovered, footguns hit, conventions established).
4. **Commit** the reviewed, finalized work.
5. **Pause.** Surface the phase result and wait — the next phase is the user's call, not an
   automatic continuation.

**Rewriting an extractor:** build the new one beside the old, diff both universes per candidate
(key, class, rule, parent, depth) with a throwaway live-cache test, classify every changed row,
mutation-check every rule against the fixture (see `internal/sdkinv/CLAUDE.md`), then swap and
delete the old code and the diff test in the same commit. Measure every review fix against the
live cache (dump candidates before and after, diff the key/class/rule columns): a one-line gate
in Azure moved 56 rows.

**After the final phase.** Run the `claude-md-management:claude-md-improver` skill across
the repo's `CLAUDE.md` files to optimize them, then commit that pass separately.

## ROADMAP.md is historical, not authoritative

Open-section bullets and per-session COMPLETED notes describe state at time of writing. Before treating an item as "deferred", grep for the named files / type constants — items often ship without the open-section bullet getting flipped.

## Commands

Primary branch: `main`. Feature branches fork from `main`, merge back to `main`.

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

# SQLite ↔ Postgres column parity — manual, not run in CI (store/CLAUDE.md "Migration parity")
make check-migrations

# Populate the SDK source cache the coverage denominator derives from (no-op when present)
make sdk-fetch            # = disco coverage sdk fetch; see internal/sdkinv/CLAUDE.md

# Before/after equivalence check: capture `disco coverage services -o json` from the repo root
# (--source-root defaults to cwd; elsewhere pairing is off and output differs spuriously)

# Cold `go build ./...` exceeds 2 min (three cloud SDKs). Build/test/lint scoped packages first
# (`./internal/sdkinv/... ./cmd/...`), run the full build in the background.

# Format before commit. The linter's formatters are gofumpt + goimports (.golangci.yaml), a superset
# of gofmt; `golangci-lint fmt` applies exactly those (slow repo-wide — pass the changed packages)
golangci-lint fmt ./cmd/... ./store/...
```

Version stamp: `make build` injects `git describe --tags --always --dirty=+dirty` via `-X cmd.Version` ldflag (canonical release path). Without the ldflag, `cmd/root.go` falls back to build-info `vcs.revision[:12]` (+`+dirty`), then the literal `dev` (e.g. `go test`). SARIF `tool.driver.version`, snapshot `manifest.toolVersion`, and `disco --version` all read `cmd.Version` — single source of truth.

## Architecture

`disco` = cloud resource discovery CLI (cobra + viper). Scan AWS accounts, Azure subs/resource groups, GCP orgs/folders. Resolve + store resource relationships in local SQLite.

### Key constraint: CGO_ENABLED=0 always

Storage: `modernc.org/sqlite` — pure-Go SQLite transpile. Cross-platform single-binary, no C toolchain. **Never swap for `mattn/go-sqlite3` or any CGO dep.**

### text/template defeats linker DCE (binary-size landmine)

Any reachable `text/template`/`html/template` execution (non-constant `reflect.Value.MethodByName`)
disables per-method DCE for the whole binary; the full AWS SDK surface is retained (`slim aws`
219MB → ~780MB). **Never make `text/template`/`html/template` reachable from disco**; use
`strings.NewReplacer`/`fmt.Sprintf` (precedent `cmd/graph.go:nodeLabel`). Known sources, all
closed — keep them closed; they are independent, and any one reopened alone kills DCE:

1. `--label-template` — plain substitution.
2. OPA schema-error formatter — fixed in OPA **v1.19.0** (`internal/methodlesstemplate`). Never go
   below; verify by file (`internal/gojsonschema/errors_dce_test.go` exists, `errors.go` imports
   `internal/methodlesstemplate`), not version string — the real `v1.18.2` tag lacks the fix.
3. grpc → `golang.org/x/net/trace` `init()` → `html/template`, reachable whatever the runtime
   `grpc.EnableTracing` says. Fixed by `-tags grpcnotrace`, always on in `Makefile` `TAGFLAG` and
   every `dist` target.

Baseline (2026-08-05): 279MB default, 219MB `slim aws`; a jump toward 780MB is the regression signal.
Guard: `go build -tags grpcnotrace -ldflags=-dumpdep 2>deps.txt; grep -c ' -> text/template.(\*Template).execute$' deps.txt`
must be `0`. `go tool nm | grep evalField` is not a health check (OPA's methodless copy reuses the name).

### Data flow

```
cmd/scan.go  →  internal/providers/<provider>/  →  store/
```

### Per-service API mandate

Providers make **per-service API calls** via each cloud's native Go SDK. No unified discovery APIs (AWS Resource Explorer, Azure Resource Graph, GCP Cloud Asset Inventory). Every AWS service, Azure `arm*` package, GCP service client called direct. Needed for full coverage.

### CLI subcommands (summary)

`disco scan|resources|scans|diff|graph|check|findings|history|summary|tag-coverage|quotas|coverage|snapshot|verify|config`. Details: `cmd/CLAUDE.md`.

### Resource type naming

Namespaced lowercase: `aws:ec2:instance`, `azure:microsoft.compute:virtual-machines`, `gcp:compute:instance`.

### Config and DB path

Viper reads `xdg.ConfigHome/disco/config.yaml`, env prefix `DISCO_`. `--db` flag (or `$DISCO_DB`) overrides DB path; default `xdg.DataHome/disco/disco.db`. Linux: `~/.config/disco/` + `~/.local/share/disco/`. macOS/Windows: both collapse to platform app-data dir. Paths resolved via `github.com/adrg/xdg` in `cmd/paths.go` (`configDir()`, `dataDir()`). `defaultDBPath()` = pure getter — directory creation is `store.Open()` job.

## Coverage report (`docs/coverage.md`, `docs/coverage-baseline.json`)

Both are generated, never edited: `make gen-coverage` writes them from the SDK cache (`-o markdown
--filter gaps` + `--write-baseline`); `make check-coverage` is the CI ratchet (`--baseline
--check-strict`, then a diff against the committed report). A pin bump or a new scanner changes
both — regenerate and commit the diff as the review. Resolver gaps: `disco coverage resolvers
--missing --with-refs`. The hand ledgers (`docs/{azure,gcp}-type-coverage.md`,
`docs/aws-missing-*.md`, `scripts/aws-next-service.sh`) are gone; do not recreate them.

## Nested guidance

Path-scoped `CLAUDE.md` files auto-load when working in subtrees:

- `cmd/CLAUDE.md` — CLI subcommand details, parallel scan orchestration, blank imports
- `store/CLAUDE.md` — schema, edge kinds, scrubbing/redaction, ResourceID, migrations, UpsertResources scope, DB perms
- `internal/providers/CLAUDE.md` — registry, Scanner iface, add-provider steps, file naming, sidecar pattern, embed-child-data, registration tests, resolver test pattern
- `internal/providers/aws/CLAUDE.md` — AWS-specific resolver/scanner conventions (ARN helpers, KMS, IAM, ELBv2, Route53, paginators, Smithy, transient errors, etc.)
- `internal/providers/azure/CLAUDE.md` — Azure-specific helpers (azPageScan, rgHierarchyPair, vault-URI parsers), case-insensitive ARM-ID rule, MSI consumer resolver, sub-scoped vs tenant-scoped pattern
- `internal/providers/gcp/CLAUDE.md` — GCP-specific (per-project fan-out, scopes-above-project gap, IAM policy synth-resource shape, permission-denied handling, NativeID conventions)
- `internal/sdkinv/CLAUDE.md` — provider-neutral core: SDK source cache, accounting invariant, extractor contract, AST pairing walk
- `internal/providers/<p>/<p>inventory/CLAUDE.md` — per-provider SDK facts: cache layout, pins, extractor rules, refs, pairing resolver
- `internal/coverage/CLAUDE.md` — coverage buckets and reasons, identity rule, baseline ratchet, live numbers

## Bundled features of note

Single build, no feature gating — everything ships in this one binary.

- Bundled OPA Rego packs follow `<provider>-<framework>` naming under `internal/policy/<name>/`, surfaced via `disco check --packs <name>`. Ships `aws-waf` (5-rule AWS Well-Architected sample pack, one or two rules per pillar). Curated full packs — Well-Architected (complete), CIS-AWS-Foundations, NIST 800-53, PCI-DSS, ISO 27001 — and future `azure-waf` / `gcp-waf` are not yet bundled.
- Findings persistence: `disco check --persist` writes a check run + findings to the DB; `disco findings list/runs` query them (migration `002_findings.sql`). The tables stay empty until `--persist` is used.
- Evidence snapshots: `disco snapshot` / `disco verify` produce and verify single-file archives (`disco-snapshot/v1` manifest, optional ed25519 signature); details in `cmd/CLAUDE.md`.
- Release SBOMs: each tagged release attaches a per-binary CycloneDX (`.cdx.json`) + SPDX (`.spdx.json`) SBOM beside the `.sha256` sidecar, generated by `syft` from the binary's Go buildinfo. `make sbom` reproduces them locally into `dist/`. Generated from the **raw binary before upx/xz** (`go version -m` can't read a upx-packed binary); both the Makefile target and the CI step (`.github/workflows/release.yaml`) run before the compression steps. `SYFT_VERSION` is pinned in both places (keep in sync), and the emitted spec versions are pinned in the `-o` selectors (`cyclonedx-json@1.7`, `spdx-json@2.3`) so a syft bump can't silently reshape the output. syft is invoked via `go run …@version`, never added to disco's go.mod. Not embedded in the `disco snapshot` evidence archive (yet) — a signed-SBOM follow-up.
- Release vuln gate: the CI `test` job runs `govulncheck` against the shipped build config (`-tags grpcnotrace`, `CGO_ENABLED=0`); a **reachable** known vuln exits non-zero, failing `test` so `build`/`release` never run (release-blocking via the existing `build: needs: test`). Runs once per tag — the vuln DB (vuln.go.dev) is queried live, so pinning `GOVULNCHECK_VERSION` (kept in sync between `Makefile` and `.github/workflows/release.yaml`) fixes the tool, not the data. `make vulncheck` mirrors it locally. Tool invoked via `go run …@version`, never in go.mod. Release-gate only for now — a continuous push/PR/cron workflow (catching vulns disclosed between tags) is a deliberate follow-up.
  - **Binary mode, not source mode** (`-mode binary` over a freshly built binary). Source mode builds whole-program SSA over three cloud SDKs and needs **>23GB**; binary mode reads the symbol table (still symbol-level reachability) and fits in <8GB.
  - Never scan a **`-w -s` stripped** binary. govulncheck does not error on a missing symbol table; it silently falls back to module granularity and reports whole modules reachable — a **spurious red** (e.g. `x/crypto/openpgp`) someone would paper over with `continue-on-error`. Both `make vulncheck` (depends on `build`, not `dist`) and the CI step (throwaway `vulnscan-target`) assert `go tool nm <binary>` succeeds first. Stripping removes debug data, not code, so the unstripped result holds for the shipped binary.
  - The `go` directive in `go.mod` is a **security floor**: most reachable findings are stdlib vulns fixed by a toolchain patch release, so bumping `go.mod` is the fix when the gate goes red on `Standard library` entries. **Bump the Dockerfile's `GO_VERSION` with it** — the golang images set `GOTOOLCHAIN=local`, so an older builder refuses `go mod download`, and a floating tag on a discontinued alpine variant stays frozen.

## Go lint conventions

### gocyclo threshold = 30 (gocognit is NOT enabled)

`.golangci.yaml` enables `gocyclo` at `min-complexity: 30`; `_test.go` files are excluded (fixture branches are test shape, not production logic). Most resolver/scanner funcs are linear "for each resource → check field A, field B, ..." walks — complexity scales with edge-kind count, not nesting. Splitting them into per-branch helpers hides the walk. Refactor only outliers above the bar. Precedents: `store.GraphWalk`, `aws.classifyPolicyResource`, `aws.resolveOpenSearchDomainTargets`, `gcp.resolveIAMPolicyRelationships`, `azure.resolveDiagnosticSettings`.

### golangci-lint flags

- v2 config (`version: "2"` at top of `.golangci.yaml`) — schema renamed in v2; missing version yields `unsupported version of the configuration`.
- Default caps output at 50 issues per linter; the full-survey command is under Commands.

### Go 1.25 modernizer lint

Repo surfaces `slicescontains`, `stringscut`, `rangeint` diagnostics. Prefer `slices.Contains(xs, v)`, `before, after, ok := strings.Cut(s, sep)`, `for i := range N` over manual equivalents.

### Loop-var copy unneeded (Go 1.22+)

`for _, x := range xs { g.Go(func() { ... x ... }) }` — no `x := x` shadow needed. Linter flags `forvar: copying variable is unneeded`. Per-iteration scope built in.

### `sync.WaitGroup.Go` (Go 1.25+)

Linter `waitgroup` flags `wg.Add(1); go func() { defer wg.Done(); ... }`. Use `wg.Go(func() { ... })` instead.

### `tagliatelle` is path-scoped

Enabled globally with the camelCase rule. There is exactly **one** exclusion under
`linters.exclusions.rules`: `internal/providers/.*\.go`, where scanner/resolver structs carry
PascalCase tags to match SDK marshal output (see `internal/providers/aws/CLAUDE.md`).

Every other JSON surface — store rows, CLI `-o json` envelopes, snapshot manifest, Rego input,
policy findings — is **camelCase** as of the v0.18.0 wire migration (`json:"nativeId"`,
`json:"accountId"`, `json:"startedAt"`, `json:"fromScanId"`). A new package emitting JSON needs
no config change; just use camelCase tags. Only provider scanners extend the PascalCase pattern.

Help text, README `jq` examples and message literals that name JSON keys are not checked by any test;
the v0.18.0 migration left six stale (summary/scans-show/snapshot/verify help, a README graph `jq`, a
diff comment). When renaming a tag, grep `Long`/`Example` strings, README and `fmt.*f` literals for the old key.

(CSV column names are a separate surface and remain snake_case — e.g. `resourcesColumns` in
`cmd/resources.go`. tagliatelle doesn't see those.)

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
9. **This is a STANDALONE PUBLIC CLI as well as the SaaS scanner, so scope every claim about the
   operator's deployment.** A doc comment, `Long`/`LongDescription` help text or
   `ReportNotice`/`ReportWarning` message written for the SaaS deployment ships as a FALSE statement
   to everyone else, and the message literals reach a customer-visible scan record. Say "Under Azure
   Lighthouse …", or use a modal ("one delegated credential CAN see many customers' subscriptions") —
   the bare categorical present tense is the tell. Fix all surfaces in ONE pass (doc comments, help
   text, message literals, the package CLAUDE.md); the Azure federation gate keys on
   `wifConfig.configured()` and correcting it one file per round took three rounds.
