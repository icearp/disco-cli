# CLAUDE.md — `internal/coverage/`

Coverage matrix engine for `disco coverage services`. The denominator is the SDK-derived
universe (`internal/sdkinv`); the numerator is the static pairing of scanner SDK calls with
the types they store (`internal/sdkinv/pairing`). Per-provider glue in
`internal/providers/<p>/<p>_coverage.go` registers via `coverage.Register` from init.

## Provider contract

- `Provider` = `Name()` + `Emits()` only. Optional: `CrossChecker` (`CrossCheck`, `RegistryKey`,
  `CanonicalKey` — drives `--cross-check`), `RegionLister`, `ResolverAuditor`.
- `InputsFromCache(ctx, cache, provider, emits, scannerDir)` is the one derivation path shared by
  `cmd/coverage.go`, `cmd/disco-scaffold` and the reconcile tests; an empty `scannerDir` means
  "name matching only" (`Matrix.Pairing=false`, disco-only rows carry `pairing-unavailable`).
- `BuildInventory(Inputs) Matrix` computes the summary **before** any filter; `Filter` only
  narrows `Rows`. Never recompute percentages from filtered rows.

## Bucket semantics (`inventory.go`)

- `covered` — resource candidate with an `emits`/`sidecar`/`derived` pairing to one of its ops,
  or (reason `matched-by-name`) an emitted type whose `sdkinv.Ident` equals the candidate's.
  A `sidecar` pairing with no types is still covered (reason `sidecar`): the scanner lists it.
  `label` pairings prove nothing (no SDK call) and never cover.
- `uncovered` — resource candidate no scanner lists. The only actionable gap.
- `attribute` — `ClassAttribute` (Get + id, no collection). Not in `%`.
- `excluded` — catalog / non-resource / `preview-only`; reason carries the rule. Not in `%`.
- `disco-only` — emitted type no candidate accounts for. Reason `explained: <unpaired reason>`
  (`non-sdk`, `other-op:<label>`, `sdk-skew:<op>`), `pairing-unavailable`, or `unexplained`
  (the only one `--check-strict` fails on).
- `registry-drift` — only with `--cross-check`: `registry-only` (live registry key, within a
  service the universe knows, matching no candidate of any class) / `candidate-only` (resource
  candidate the registry lacks). Identities compare via `RegistryKey(candidate)` vs
  `CanonicalKey(registryKey)`. Registry entries for services outside the universe (non-cloud
  GCP APIs, CFN service renames) are excluded by rule, not drift.
- Unit of coverage is the candidate: one op → N types counts once (`Row.DiscoType` is the
  name-matching type, `Row.DiscoTypes` the rest); N ops → one type marks every candidate covered.

## Identity

`sdkinv.Ident` (Canon, `-ies`→`y`, trailing `e`/`s` run stripped) is the only cross-source equality used
here and by GCP's `RegistryKey`/`CanonicalKey`: `Singular` alone splits "caches"/"cache" and
"aliases"/"alias". Never display an Ident; keys come from the extractor.

## Live numbers (2026-09-16 pins, pairing on)

AWS 50.2% (1624/3236), Azure 19.7% (386/1959), GCP 23.3% (235/1009); zero unexplained. The
Azure/GCP extractors emit no `attribute` class (their detail reads are item paths, not ops).

## Retired hand lists (Phase 5)

`aws_skips.go`, `Descriptor.Upstream`/`Uncatalogued`, `azureAPITypeMap`, `serviceRenames`,
GCP `singularizeExceptions` and the Discovery allowlist are gone; the pairing tests replace
them. The one-time reconcile report (Phase 4) found: AWS `skip-contradicted` 198 — ephemeral,
retired or catalog rows the SDK still lists, now honest `uncovered` rows for the Phase 8
baseline; `skip-unmatched` 194 — CFN-only or retired services with no Smithy model; Azure
`alias-orphan` 2 — the `CloudServices*` sdk-skew; ledger-absent rows were ledger errors
(`microsoft.storage/storagetasks` lives under `microsoft.storageactions`, `hybridnetwork/devices`
left the SDK, `admin Transfer` was never scanned).
