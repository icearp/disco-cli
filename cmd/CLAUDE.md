# CLAUDE.md — `cmd/`

Cobra command layer.

## CLI structure

- `disco scan` — runs all registered providers in parallel
- `disco scan <provider>` — single provider (e.g. `disco scan aws`)
- `disco scan --providers aws,gcp` — only named providers (comma-separated `StringSlice`)
- `disco list` — query local DB with filters (`--provider`, `--type`, `--region`, `--status`, `--tag-key`/`--tag-value`, `--output table|json|csv|jsonl`)
- `disco diff <scanA> <scanB>` — drift detect; emits added/removed/changed rows between two scan IDs
- `disco graph <resource-id> --depth N --kinds contains,attached-to --direction both --output table|json|dot|mermaid --dot-theme light|dark|mono` — walks `relationships` + `hierarchy_closure`. DOT styling lives in `cmd/graph_theme.go` — single `dotTheme` struct holds graph/node/edge attribute blocks + `nodePreset` map (primary/secondary/storage/identity/muted/error) + cluster palette. `presetForResource` picks a preset by `Type` second segment (`s3|rds|...`→storage, `iam|sso|...`→identity, `ec2|lambda|...`→primary). `mono` reproduces pre-theme output byte-for-byte for diff-stable piping.
- `disco graph complete` — dumps every customer resource + every provider-managed resource that shares an edge with one. No seed, no BFS — backed by `store.GraphAll(GraphAllOpts)` which reads `ListResources({IncludeManaged: true})` paginated + `ListRelationships()` and applies the customer-edge inclusion rule in-memory. `--include-managed` keeps orphan managed nodes too. Traversal flags (`--depth`/`--kinds`/`--direction`) ignored.
- `disco check --rules ./policies --severity high --output sarif --exit-nonzero` — Runs OPA Rego policies against store. `--rules` takes `.rego` files or directories (recursive). `--output` ∈ `table|json|jsonl|sarif` (sarif = v2.1.0 for GitHub/GitLab code-scanning, marshalled inline in `cmd/check_sarif.go` — no external SARIF lib). Engine in `internal/policy/` ships in OSS; no first-party policies bundled — bring your own (Conftest AWS, regula, in-house CIS pack). Curated compliance packs (NIST, CIS, PCI-DSS, Well-Architected) are paid add-ons. Each policy module must populate `data.disco.deny` (set) with finding objects shaped `{id, severity, message, resource_id?, tags?, category?, remediation?, ref_url?}`. Input shape: `{id, provider, account_id, type, native_id, name, region, status, attributes}` — `attributes` is the decoded `AttributesJSON` (object), not the raw string.

## `disco coverage`

Source-of-truth coverage cmd (ROADMAP G5). Wired for AWS, Azure, and GCP. Reads scanner-declared `emits []coverage.TypeDecl` (NOT `KnownTypes()`, which has been deleted) and matches against live upstream registries (CFN ListTypes / ARM Providers/List / GCP Discovery API). Add new coverage-related flags in `cmd/coverage.go`. Provider-side glue lives at `internal/providers/<p>/coverage.go`.

## Resume

`disco scan --resume <scan-id|latest>` reuses a previous scan_id instead of generating a fresh one. `latest` picks the most-recent scan whose status is `running` or `partial`. The OSS path persists per-(scan, service, scope) checkpoints (`store.SaveCheckpoint`); the paid incremental scanner consumes them on the next `--resume` to skip already-listed pages. `startOrResumeScan` in `scan.go` owns the dispatch. Without `--resume`, behaviour matches pre-Phase-3 — fresh scan_id, no checkpoint reuse.

## Parallel scanning

`cmd/scan.go` runs selected scanners concurrent via plain `sync.WaitGroup` — no sibling cancellation. Per-service / per-region failures collected via `store.OnError` and rendered as one grouped block at end. Scan record always finalised via `db.CompleteScan` (failed or not). Lifecycle + errgroup-error-tolerance details: `internal/providers/CLAUDE.md` "Errors never abort scan".

`runScan(cmd, scanners)` (`scan.go`) holds the shared open-db / `CreateScan` / WaitGroup / `CompleteScan` lifecycle. `scanCmd.RunE` calls it with `providers.All()`; per-provider subcommands call it with a single-element slice.

## Provider blank imports

`cmd/providers.go` holds all blank imports (`_ "codeberg.org/icearp/disco/internal/providers/<name>"`). `cmd/scan.go`'s `init()` iterates `providers.All()` to build `disco scan <name>` subcommands — no `scan.go` change when adding provider. See `internal/providers/CLAUDE.md` for add-new-provider steps.

## Scan subcommand flag registration

`scan.go` `init()` builds per-provider subcommands. Register `--services` / `--regions` / `--profile` **only when the scanner implements the matching capability interface** (`providers.ServiceFilterer`, `RegionOverrider`, `ProfileOverrider`). Listing a flag a provider silently ignores misleads users — Cobra has no per-subcommand "hide if unsupported" toggle. New optional flags follow same gate. Real service-prefix examples come from `serviceFilterExample(provider)` — keep entries truthful (e.g. `aws:ec2,aws:s3`, not `aws:compute`).

## Paid commands

Paid subcommands live in `cmd/<name>_paid.go` with `//go:build paid`. `init()` still does `rootCmd.AddCommand(...)` — OSS build simply omits the file so the subcommand is absent. First line of `RunE` must be `if err := license.Require(); err != nil { return err }`. Canonical shape: `cmd/diff_paid.go`.

## Shared render helpers (`helpers.go`)

`ptrOrDash(*string) string`, `short(id string) string` (8-char ID prefix), `renderMessages(w, label, []messageRow, quiet)` (column-aligned grouped block used by `renderErrors`/`renderWarnings` in `scan.go`). New commands rendering tabular output should reuse these instead of redefining.

Output styling: per-format theme modules (`cmd/graph_theme.go` for DOT) own all attribute blocks + a preset map keyed by an enum. Renderers look up presets, never inline color/shape literals. New themes = one entry in the `themes` map; new resource→preset rules = one switch case in `presetForResource`. Always include a `mono` theme that reproduces pre-theme output byte-for-byte for diff-stable piping.

## Shared test helpers (`list_test.go`)

Reused by `graph_test.go`, `check_test.go`, `diff_paid_test.go`:
- `seedTestDB(t)` — temp SQLite + scan record + 2 resources; sets `viper.Set("db", path)` so cobra cmds pick it up via `defaultDBPath()`.
- `captureStdout(t, fn)` — pipes `os.Stdout` for cmds that write directly to it (not via `cmd.OutOrStdout`).
- `captureStdout` does NOT redirect `os.Stderr`. Stderr writes (population stamps, truncation warnings, banner-under-`--verbose`) bypass test assertions — safe place for telemetry that must not contaminate `-o json|jsonl|sarif` pipelines. If you need to assert on stderr, use `captureStderr` (drained via goroutine to avoid >64KB pipe deadlock).
- Default invocations are stderr-clean. The "Using config file:" banner is gated behind the global `--verbose` flag (`cmd/root.go::initConfig`); banners added later should reuse the same `verbose` boolean rather than introducing per-cmd `--quiet` flags.
- Cobra's `InitDefaultVersionFlag` (lazy, called at execute) only claims `-v` when no other flag holds the shorthand. Pre-register a global flag with `-v` in `init()` to repurpose it (precedent: `--verbose` in `cmd/root.go`); `--version` long-form keeps working with no shorthand.

Cobra package-level flag vars (`graph*`, `list*`, …) persist across tests because `rootCmd` is shared. Each subcommand test must reset its flags before `cmd.SetArgs(...)` — see `resetGraphFlags()` in `graph_test.go`. Flag pollution is transitive: a NEW test setting `--type`/`--limit`/`--direction` via `SetArgs` can break older sibling tests that only did partial resets (e.g. `listOutputFmt = ""`). When adding such a test, upgrade siblings to the full `resetXFlags()` helper.

## Silent exit codes for query-absence

When "no result" is a valid query outcome (e.g. `graph path` between unreachable resources), return a sentinel error from the store layer (`store.ErrNoPath`) and let `cmd/root.go` `Execute()` map it to `os.Exit(1)` without printing. Keeps `RunE` testable — `os.Exit` inside `RunE` bypasses in-process test assertions.

## JSON dialect: snake_case + nested attrs/tags

`store.Resource.MarshalJSON` is the single source of truth — emits snake_case keys with nested `attributes` / `tags` objects, not stringified `AttributesJSON` / `TagsJSON`. Matches `policy.Finding` and `coverage.Row` shape. New JSON output paths must encode `[]store.Resource` (or struct embedding it) directly; do not reach for raw field access.

## DOT `dir=back` requires endpoint swap, not just attribute

Graphviz `dir=back` only re-renders the arrowhead — rank still flows tail→head. To flip layout direction (e.g. force `attached-to` parent left of source under `rankdir=LR`), `renderGraphDot` swaps `FromID`/`ToID` for any edge whose preset carries `dir=back`. Adding `dir=back` to a theme preset alone is a no-op for rank; both pieces are needed.
