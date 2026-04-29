# CLAUDE.md — `internal/coverage/`

Coverage matrix engine for `disco coverage`. Per-provider impl in `internal/providers/<p>/coverage.go` registers via `coverage.Register(...)` from init.

## Adding a provider

1. Implement `coverage.Provider` (Name, Fetch, Emits, Aliases, AlgorithmicKey).
2. Sweep every scanner's `registerService` to add `emits []coverage.TypeDecl{{Service, DiscoType, Synthetic}}`.
3. Build alias map for known disco↔upstream mismatches; algorithmic fallback covers the rest.
4. Mark Synthetic disco-only types so they don't trigger `upstream-missing`.

## Bucket semantics

- `covered` — disco emits + upstream registry has it.
- `uncovered` — upstream has it, no disco scanner.
- `synthetic` — disco-only (e.g. `gcp:iam:policy`, `aws:kms:grant`).
- `upstream-missing` — disco emits but upstream registry doesn't list. Drift signal: alias-map typo, retired API, or scanner targeting obsolete type. `--check-strict` exits non-zero on any.

## GCP Discovery quirks

- Fetch all versions of each relevant API (v1+v2 expose different collections, e.g. cloudbuild Trigger in v1, Connection in v2). Dedupe by upstream key.
- `singularize` strips trailing `s`/`ies` only — irregular plurals (Indexes→Index) need alias-map entry, not heuristic patches.
- Discovery resource collection name → singular → PascalCase. Walk recurses through nested `resources` tree.
