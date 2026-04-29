# CLAUDE.md — `cmd/`

Cobra command layer.

## CLI structure

- `disco scan` — runs all registered providers in parallel
- `disco scan <provider>` — single provider (e.g. `disco scan aws`)
- `disco scan --providers aws,gcp` — only named providers (comma-separated `StringSlice`)
- `disco list` — query local DB with filters (`--provider`, `--type`, `--region`, `--status`, `--tag-key`/`--tag-value`, `--output table|json|csv|jsonl`)
- `disco diff <scanA> <scanB>` — drift detect; emits added/removed/changed rows between two scan IDs
- `disco graph <resource-id> --depth N --kinds contains,attached-to --direction both --output table|json|dot` — walks `relationships` + `hierarchy_closure`
- `disco check --rules ./policies --severity high --exit-nonzero` — **paid only.** Runs OPA Rego policies against store. `--rules` takes `.rego` files or directories (recursive). Engine in `internal/policy/` (paid-tagged); ships no first-party policies — bring your own bundle (Conftest AWS, regula, in-house CIS pack). Each policy module must populate `data.disco.deny` (set) with finding objects shaped `{id, severity, message, resource_id?, tags?, category?, remediation?, ref_url?}`. Input shape: `{id, provider, account_id, type, native_id, name, region, status, attributes}` — `attributes` is the decoded `AttributesJSON` (object), not the raw string.

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

## Shared test helpers (`list_test.go`)

Reused by `graph_test.go`, `check_test.go`, `diff_paid_test.go`:
- `seedTestDB(t)` — temp SQLite + scan record + 2 resources; sets `viper.Set("db", path)` so cobra cmds pick it up via `defaultDBPath()`.
- `captureStdout(t, fn)` — pipes `os.Stdout` for cmds that write directly to it (not via `cmd.OutOrStdout`).

Cobra package-level flag vars (`graph*`, `list*`, …) persist across tests because `rootCmd` is shared. Each subcommand test must reset its flags before `cmd.SetArgs(...)` — see `resetGraphFlags()` in `graph_test.go`.

## Silent exit codes for query-absence

When "no result" is a valid query outcome (e.g. `graph path` between unreachable resources), return a sentinel error from the store layer (`store.ErrNoPath`) and let `cmd/root.go` `Execute()` map it to `os.Exit(1)` without printing. Keeps `RunE` testable — `os.Exit` inside `RunE` bypasses in-process test assertions.
