# CLAUDE.md — `internal/coverage/`

Coverage matrix engine for `disco coverage services`. The denominator is the SDK-derived
universe (`internal/sdkinv`); the numerator is the static pairing of scanner SDK calls with
the types they store (`internal/sdkinv/pairing`). Per-provider glue in
`internal/providers/<p>/<p>_coverage.go` registers via `coverage.Register` from init; each
provider's extractor and resolver live in `internal/providers/<p>/<p>inventory`.

## The admitting rule travels with the row

Every candidate carries `Rule`: the extractor rule that decided its class (each provider's own
vocabulary; see `internal/providers/<p>/<p>inventory/CLAUDE.md`). It reaches
`Row.Rule`, `Summary.ByRule` and the markdown report.

`Summary.ByRule` is computed in `BuildInventory`, **before** any `Filter`, because
`make gen-coverage` renders with `--filter gaps` and a table built from the filtered rows
reported every rule at 0% covered. `docs/coverage.md` states the denominator's definition in
its header and prints the per-rule table under it, so a weak rule (AWS `child-uncatalogued`
sits far below `sr-resource`) is visible as a low percentage instead of silently inflating one
headline number. Never recompute percentages from filtered rows.

## Provider contract

- `Provider` = `Name()` + `Emits()` only. Optional: `CrossChecker` (`CrossCheck`, `RegistryKey`,
  `CanonicalKey` — drives `--cross-check`), `RegionLister`, `ResolverAuditor`, `ServiceMapper`
  (`TypeServices()`: disco type → scanner service names registered from the same file, via
  `restype.Origin`; `coverage verify` joins `scans.errors` through it), `ScaffoldStubber`
  (`ScannerStub()`: the stub scanner's imports, signature and body for `cmd/disco-scaffold`;
  `TestGenScaffold_EveryProviderStubsAScanner` fails if a registered provider loses it).
- `Inputs.Paired` is set explicitly by `InputsFromCache`; a walk that parsed but anchored nothing
  is a paired run with no pairings, not "no source".
- `InputsFromCache(ctx, cache, provider, emits, scannerDir)` is the one derivation path shared by
  `cmd/coverage.go`, `cmd/disco-scaffold` and the reconcile tests; an empty `scannerDir` means
  "name matching only" (`Matrix.Pairing=false`, disco-only rows carry `pairing-unavailable`).

## Bucket semantics (`inventory.go`)

- `covered` — resource candidate with an `emits`/`sidecar`/`derived` pairing to one of its ops,
  or (reason `matched-by-name`) an emitted type whose `sdkinv.Ident` equals the candidate's.
  A name match yields when the pairing ran and reported that type `unexplained`: it set
  `accounted[t]`, suppressing the disco-only row and with it `Summary.Unexplained` — the only
  number `--check-strict` exits on. `other-op` and `sdk-skew` explanations still name-match.
  A `sidecar` pairing with no types is still covered (reason `sidecar`): the scanner lists it.
  `label` pairings prove nothing (no SDK call) and never cover.
- `uncovered` — resource candidate no scanner lists. The only actionable gap. There is no skip list:
  ephemeral, retired or catalog rows the SDK still lists stay honest `uncovered` rows.
- `attribute` — `ClassAttribute` (Get + id, no collection). Not in `%`.
- `excluded` — catalog / non-resource / `preview-only`; reason carries the rule. Not in `%`.
  An excluded row a scanner provably lists keeps its `discoType` and gains the `scanner-lists`
  signal; `--filter scanner-lists` is that worklist, and the markdown Excluded section prints the
  type. The class rule still wins the bucket — re-bucketing these would inflate the percent
  before the rules are fixed; discarding the evidence hid real classifier bugs.
- `disco-only` — emitted type no candidate accounts for. Reason `explained: <unpaired reason>`
  (`non-sdk`, `other-op:<label>`, `sdk-skew:<op>`), `pairing-unavailable`, or `unexplained`
  (the only one `--check-strict` fails on).
- `registry-drift` — only with `--cross-check`. Drift proper: `registry-only` (live registry key,
  within a service the universe knows, matching no candidate of any class) / `candidate-only`
  (covered/uncovered candidate the registry lacks). Explained by construction, kept visible:
  `unlistable` (GCP get-only node), `cfn-only` (CFN type with no SR twin), `arm-operation` /
  `location-scoped` (Azure ARM RPC endpoints: `operations`, `checknameavailability`, async-operation tracking,
  anything under `locations/`; the registry marks them no other way — capabilities `None` covers ~670 real proxy
  types too, so do not key on it), `near-name` (unique
  same-service prefix/suffix stem pair; twin in a `near-name:<key>` signal),
  `child-of-registered` (parent's registry id seen), `service-unregistered` (registry has nothing
  for the service). Identities compare via `RegistryKey(candidate)` vs
  `CanonicalKey(UpstreamType)`, after `r.Service` is mapped through `Universe.ServiceAliases`.
  Registry services with no counterpart are returned by `CrossCheck` and printed on stderr.
- `Row.Refs` copies `Candidate.Refs`; `TypeRefs(matrix)` unions them per paired disco type for
  `resolvers --missing`. Refs are hints (id/ARN/URL-shaped element fields, own id excluded), never
  a bucket input. `TypeRefs` gives a row's refs to `Row.DiscoType`, and to a `Row.DiscoTypes` entry
  **only** when it matches the candidate's ident or shares its leaf: the five `aws:docdb:*` orphans
  do share `rds/dbinstance`'s leaf and were starved of its 44 refs, while a dispatcher's derived
  pairing spans a whole service and must not hand every network type the app gateway's 280 fields.
- `Row.Ops` folds repeated labels (sibling AWS models, per-version GCP documents). `Ops` is never read back, so the fold is presentation only.
- `Row.Scope` is the **narrowest** scope among the candidate's ops, ranked by the provider's own
  `Universe.Scopes` (narrowest first; an undeclared scope ranks widest). coverage holds no
  cross-provider scope table. Never `Ops[0]`: ops sort by label, so it printed `billingAccounts.`
  on GCP rows a project-scoped lister also serves. AWS ops carry no scope at
  all, by rule (`internal/providers/aws/awsinventory/CLAUDE.md`), and the renderers dash an empty value.
- Unit of coverage is the candidate: one op → N types counts once; N ops → one type marks every
  candidate covered. `Row.DiscoType` is the type to display — identity match, else shared leaf —
  and `Row.DiscoTypes` the whole set when more than one is paired. With several paired and neither
  tier matching there is no answer, so `bestType` returns `""` and the row carries reason
  `multi-type` (the alphabetically first was a coin toss). **The covered bucket therefore
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
`BuildInventory`, so `docs/coverage.md` is byte-stable.
`ProviderBaseline.Pairing` records whether the scanner source was paired; a mode-mixed compare
is fatal (`pairing-mode`) because name matching alone reports ~7 points less on AWS. A provider
absent from the file is fatal (`no-baseline`) — nothing would guard it. An uncovered key leaving
the universe is reported (`denominator-shrunk`): it raises the percent, so no other check fires.
`--write-baseline` **merges** into the existing file (`coverage.Merge`), so a `--providers`-narrowed
run cannot silently drop the ratchet for the providers it did not compute.
Workflow (`gen-coverage` / `check-coverage`): root `CLAUDE.md` "Coverage report". Keep `.gitignore` narrow (`*.out`, `coverage.html`)
— a bare `coverage.*` once ignored it and made CI's diff a no-op.

## Identity

`sdkinv.Ident` is the only cross-source equality; its rules are in `internal/sdkinv/CLAUDE.md`.
GCP's `RegistryKey`/`CanonicalKey` also compare through `Ident`.

## Live numbers: `docs/coverage.md` is authoritative

Do not copy percentages here; CI regenerates the report. Invariants: zero unexplained disco-only
rows; Azure/GCP explained disco-only rows are Entra `non-sdk`, `sdk-skew` and GCP `other-op`
listers no sibling confirms; the Azure/GCP extractors emit no `attribute` class (their detail
reads are item paths, not ops). Pairing off (an installed binary) reads ~7 points lower on AWS.

## Cross-check drift reads buckets, not classes

`CrossCheck` builds the candidate-only set from rows bucketed `covered`/`uncovered`. Reading
`Class == ClassResource` instead made the same report exclude a `preview-only` candidate as out
of scope and then re-report it as drift. `preview-only` is a signal, not
a class, so nothing else removes them.

Candidates are `map[string][]Candidate`: siblings sharing a leaf identity (GCP leaf, AWS folded
`…Resource`) each get a row. Measure drift changes with a scratch `_test.go` in the provider
package calling `InputsFromCache` → `BuildInventory` → `CrossCheck` (AWS SR half is credential-free;
the CFN half needs live creds); delete it before commit.
