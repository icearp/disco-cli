# CLAUDE.md — `cmd/`

Cobra command layer.

## CLI structure

- `disco scan` — runs all registered providers in parallel
- `disco scan <provider>` — single provider (e.g. `disco scan aws`)
- `disco scan --providers aws,gcp` — only named providers (comma-separated `StringSlice`)
- `disco list` — query local DB with filters (`--provider`, `--type`, `--region`, `--status`, `--tag-key`/`--tag-value`, `--output table|json|csv|jsonl`)
- `disco diff <scanA> <scanB>` — drift detect; emits added/removed/changed rows between two scan IDs
- `disco graph <resource-id> --depth N --kinds contains,attached-to --direction both --output table|json|dot` — walks `relationships` + `hierarchy_closure`
- `disco check --rules rules.yaml --builtins --severity high --exit-nonzero` — runs security rules against store

## Parallel scanning

`cmd/scan.go` runs selected scanners concurrent via `errgroup.WithContext`. First error cancels siblings. On error: scan record marked failed via `db.FailScan`. On success: `db.CompleteScan`.

## Provider blank imports

`cmd/providers.go` holds all blank imports (`_ "codeberg.org/icearp/disco/internal/providers/<name>"`). `cmd/scan.go`'s `init()` iterates `providers.All()` to build `disco scan <name>` subcommands — no `scan.go` change when adding provider. See `internal/providers/CLAUDE.md` for add-new-provider steps.

## Paid commands

Paid subcommands live in `cmd/<name>_paid.go` with `//go:build paid`. `init()` still does `rootCmd.AddCommand(...)` — OSS build simply omits the file so the subcommand is absent. First line of `RunE` must be `if err := license.Require(); err != nil { return err }`. Canonical shape: `cmd/diff_paid.go`.

## Shared render helpers (`helpers.go`)

`ptrOrDash(*string) string`, `short(id string) string` (8-char ID prefix), `renderMessages(w, label, []messageRow, quiet)` (column-aligned grouped block used by `renderErrors`/`renderWarnings` in `scan.go`). New commands rendering tabular output should reuse these instead of redefining.
