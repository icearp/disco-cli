# CLAUDE.md — `internal/coverage/`

Coverage matrix engine for `disco coverage services`. The denominator is the SDK-derived
universe (`internal/sdkinv`); the numerator is the static pairing of scanner SDK calls with
the types they store (`internal/sdkinv/pairing`). Per-provider glue in
`internal/providers/<p>/<p>_coverage.go` registers via `coverage.Register` from init.

## Provider contract

- `Provider` = `Name()` + `Emits()` only. Optional: `CrossChecker` (`CrossCheck`, `RegistryKey`,
  `CanonicalKey` — drives `--cross-check`), `RegionLister`, `ResolverAuditor`, `ServiceMapper`
  (`TypeServices()`: disco type → scanner service names registered from the same file, via
  `restype.Origin`; `coverage verify` joins `scans.errors` through it).
- `Inputs.Paired` is set explicitly by `InputsFromCache`; a walk that parsed but anchored nothing
  is a paired run with no pairings, not "no source".
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
- `Row.Refs` copies `Candidate.Refs`; `TypeRefs(matrix)` unions them per paired disco type for
  `resolvers --missing`. Refs are hints (id/ARN/URL-shaped element fields, own id excluded), never
  a bucket input. `TypeRefs` gives a row's refs to `Row.DiscoType`, and to a `Row.DiscoTypes` entry
  **only** when it matches the candidate's ident or shares its leaf: the five `aws:docdb:*` orphans
  do share `rds/dbinstance`'s leaf and were starved of its 44 refs, while a dispatcher's derived
  pairing spans a whole service and must not hand every network type the app gateway's 280 fields.
- Unit of coverage is the candidate: one op → N types counts once; N ops → one type marks every
  candidate covered. `Row.DiscoType` is the type to display — identity match, else shared leaf —
  and `Row.DiscoTypes` the whole set when more than one is paired. With several paired and neither
  tier matching there is no answer, so `bestType` returns `""` and the row carries reason
  `multi-type`; the alphabetically first was a coin toss that showed the diagnostic-settings
  dispatcher as `azure:microsoft.apimanagement:service` (40 rows). **The covered bucket therefore
  keys on `len(paired) > 0`, never on `DiscoType != ""`** — clearing the display type must not cost
  a row its bucket.

## Baseline ratchet (`baseline.go`)

`NewBaseline(unfiltered matrices)` records per provider: pins, percent, covered/uncovered counts
and keys, unexplained disco types (sorted, byte-stable). `CompareBaseline` is fatal on a covered
key now uncovered (under any pins — a scanner lost a listing), a percent drop under identical
pins, or a new unexplained type; a covered key that is no longer a candidate at all is `regressed` under the same pins (the
universe cannot shrink by itself) and `gone-since-baseline` under new ones; a pin bump only
reports `pins-changed` + `new-since-baseline` / `gone-since-baseline` keys, never the percent,
because a larger universe with the same scanners can only lower it. Rows are sorted in
`BuildInventory`, so `docs/coverage.md` is byte-stable (verified: two `make gen-coverage` runs
`cmp` equal).
`ProviderBaseline.Pairing` records whether the scanner source was paired; a mode-mixed compare
is fatal (`pairing-mode`) because name matching alone reports ~7 points less on AWS. A provider
absent from the file is fatal (`no-baseline`) — nothing would guard it. An uncovered key leaving
the universe is reported (`denominator-shrunk`): it raises the percent, so no other check fires.
`--write-baseline` **merges** into the existing file (`coverage.Merge`), so a `--providers`-narrowed
run cannot silently drop the ratchet for the providers it did not compute.
`make gen-coverage` accepts; `make check-coverage` enforces (plus a diff of `docs/coverage.md`).
`docs/coverage.md` is generated but **committed** — `.gitignore`'s `coverage.*` swallowed it and
both the stale-report diff and the CI regen job were no-ops against a file that existed only
locally. Keep ignore patterns narrow (`*.out`, `coverage.html`).

## Identity

`sdkinv.Ident` (Canon, `-ies`→`y`, `-yses`→`-ysis`, trailing `e`/`s` run stripped) is the only cross-source equality used
here and by GCP's `RegistryKey`/`CanonicalKey`: `Singular` alone splits "caches"/"cache" and
"aliases"/"alias". Never display an Ident; keys come from the extractor.

## Live numbers (2026-09-16 pins, pairing on)

AWS 50.3% (1636/3250), Azure 19.7% (386/1959), GCP 23.3% (235/1009); zero unexplained. Azure
carries 8 explained disco-only rows (4 Entra `non-sdk`, 4 `sdk-skew`), GCP 1 (`other-op`). The
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
