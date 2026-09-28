# CLAUDE.md — `internal/sdkinv/`

Provider-neutral core of the coverage denominator: the SDK source cache, the `Universe` model,
the extractor and resolver registries, path grammar, the conformance contract and the pairing
walk. Imports nothing from `internal/providers` or `internal/coverage`.

**Provider knowledge lives in `internal/providers/<p>/<p>inventory`** (extractor, refs, pairing
resolver, pins, scope vocabulary, fixtures, its own `CLAUDE.md`). Those SDK-free leaves register
from `init()` and are blank-imported by the slim-gated `internal/providers/all/<p>.go`, so a slim
build knows only its compiled providers. `TestCoreIsProviderNeutral` fails when an identifier or
string literal in a non-test core file names a cloud or SDK format (comments are exempt); adding a
provider never edits the core. Put a provider need behind an Extractor/Resolver hook in
`<p>inventory`, never a core special case.

## Cache layout (`$XDG_CACHE_HOME/disco/sdk/<provider>@<ref>/`, `disco coverage sdk fetch|status`, `make sdk-fetch`)

- `manifest.json` written last; a dir without it is "absent". Fetch lands in `.tmp-*` then renames.
- A snapshot's identity is provider + ref + `SpecFingerprint(FetchSpec)` (manifest `spec`).
  `Cache.Status(e Extractor)` returns `ErrNotFetched` on any mismatch, so a wrong-identity or
  wrong-spec directory refetches instead of being read. **Changing a source's `Keep` or `Expand`
  MUST bump its `KeepID`/`ExpandID`** (funcs cannot be hashed; `TestExtractorsRegistered` in
  `internal/providers/all` only checks the id is non-empty). Widening `keepARMFile` at an unchanged Azure `SDKRef` once left every
  cache stale and silently served the old, narrower Azure file set.
- Full fetch is hundreds of MB. Rerun is a no-op; `--force` refetches.

## Pins

- Each provider pins its sources in `internal/providers/<p>/<p>inventory/pins.go`. A pin bump
  changes the denominator; reports print the pins.

## Archive handling (`fetch.go`)

- GitHub tarballs prefix `<repo>-<ref>/` (Strip 1); module zips `<module>@<ver>/` (Strip 2).
- Entry paths are backslash-normalised, `..`/absolute rejected, 64 MB per-entry cap; JSON-index
  entry names must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`, must be unique, and may not be
  `index` (it would overwrite the index document). The whole index is validated **before** the
  errgroup fan-out: a mid-loop reject left goroutines writing into the discarded snapshot.
- `fetchClient()` sets per-hop timeouts (`ResponseHeaderTimeout`, `TLSHandshakeTimeout`) and
  **never an overall `Client.Timeout`** — the AWS tarball is hundreds of MB and a whole-request
  deadline aborts a healthy slow download. Bodies are wrapped in `idleReader`, which cancels the
  request after 60 s with no byte and reports `transfer stalled: …` rather than `context canceled`.

## Accounting invariant (`Universe.SourceOps` / `Dropped`, `conformance.CheckUniverse`)

- Every source op lands in exactly one of candidate ops (any number of candidates), `Other`, or
  `Dropped` (with a `Reason`); `sdkinv.Unaccounted` reports missing / extra / conflicting.
  `CheckUniverse` runs on fixtures **and** live caches (`TestLiveUniverseWellFormed` in `internal/providers/all`).
- Enumerate `SourceOps` by a walk **apart from classification** — derived from the classifier's own
  parse it can never fail. Azure first reused `builderRe` (vacuous); it now walks exported client
  methods (`publicRe`), mapping `Begin<Op>`/`New<Op>Pager` onto builders.
- Never filter `Other` or delete entries without a `Drop`; the live test names the lost ops.
- Live drops at current pins: aws 0, azure 0, gcp 6,591 (non-cloud-api 6125,
  version-without-cloud-rooted-lister 414, alias-document 47, document-root-method 3,
  lister-on-item-path 2).
- `Universe.Scopes` = provider scope vocabulary, narrowest first; coverage ranks `Row.Scope` by it;
  every op scope must be declared.

## Extractors

- Contract: `conformance.Check` runs against `internal/providers/<p>/<p>inventory/testdata/cache`
  (`TestInventoriesConform` in `internal/providers/all`, over every registered extractor). A fixture must hold
  resource, catalog and non-resource candidates at depth 0 and 1, sorted keys, every parent
  present, deterministic output (`reflect.DeepEqual` across two runs — sort every slice you
  build from a map, including `Signals`).
- `StrongerClass` ranks resource > catalog > non-resource > attribute: a detail read (Get)
  merged onto a lister's key never outranks the lister.
- `irregular` is a closed, purely linguistic suffix table (`indices`→`index`, `thesauri`→`thesaurus`,
  `series`/`species`/`ephemeris` invariant, `lenses`→`lens`) consulted first by **both** `Singular`
  and `Ident`. It is matched as a suffix, so compounds work (`attachedIndices`,
  `revenueStatisticsTimeSeries`). Nothing in it may name a cloud resource — that would be the
  hand-maintained list this subsystem exists to avoid — and every entry carries a `norm_test` pair.
- `Ident` is the only identity (irregulars, `-ies`→`y`, `-yses`→`-ysis`, then every trailing `e`/`s`
  dropped): `caches`/`cache`, `aliases`/`alias`, `statuses`/`status`, `analyses`/`analysis`,
  `indices`/`index` meet there and nowhere else. Never show an Ident (`alias`→`alia`). Without the
  irregulars `qbusiness/index` and `qbusiness/indice` were two covered rows for one collection.
- `Singular` is display only: irregulars, then keeps `-ss`/`-us`/`-is`/`-ias`, `-ies`→`y`,
  `-yses`→`-ysis`, `-sses/-xes/-shes`→`-es`, `-ches` after a single non-`e` vowel→`-e` (cache,
  niche) else `-es` (batch, beach, approach), `-ses` after `u`/`ia`→`-s` (status, alias) else
  `-se` (database, case, license), else `-s`. No inflector dep. Any new rule needs a `norm_test`
  pair; the keys shipped before this was audited include `bedrock/flowalia`, `wellarchitected/len`,
  `config/…statuse`, `iotsitewise/timesery`, `kendra/thesauri` and
  `gameliftstreams/applicationshadercach`, and `RestApi`/`RestApis` were two candidates.
- Live counts: see each `TestExtract_Live` log and the `docs/coverage.md` headline (resource +
  attribute + excluded); a large swing after a pin bump means re-check the anchors.
- `Candidate.Refs` (`<p>/refs.go`): dotted paths on the listed element that name other
  resources, sorted and unique, own id excluded, depth-bounded. Refs are a **hint**: they rank
  `coverage resolvers --missing`, never bucket a row, and their absence is not proof of a derived
  leaf (`internal/providers/CLAUDE.md`). Recall matters more than precision.
  - Every fixture carries one candidate with refs (conformance).
- `Universe.Other` carries every SDK op that is not a candidate op (writes, item reads,
  actions). Every extractor must end with `sdkinv.SortOps(u.Other)`: it is filled from map
  walks, and the conformance DeepEqual only catches the omission on some runs (`-count=5`).
  Pairing needs it to explain types built from a Get/Describe (`other-op:` reason).

## Pairing (`pairing/`)

- `Resolver.LabelOp` maps a label to its anchor-form op name for the skew check; provider naming
  tokens (Azure's `Client` suffix) live there, never in core. `pairing.Register` panics on
  duplicates; resolvers use exported `(*Func).Line`.
- `pairing.Scan(ctx, cache, provider, dir)` = extract from the cache + `Walk` + `Unpaired`;
  wraps `sdkinv.ErrNotFetched` so `internal/providers/<p>/<p>_pairing_test.go` skips without
  the cache. Those two tests (`TestScannerOpLabelsResolve`, `TestEveryEmittedTypePaired`) are the
  gate: label-no-op / label-no-anchor / label-malformed / unresolved-receiver fail; sdk-skew is
  logged. `TestEveryEmittedTypePaired` prints `Result.StoredBy[type]` with the failure, which is
  the only thing that names which scanner the walker could not reach.
- go/parser with `SkipObjectResolution`, non-test files only, stdlib only. Anchors (SDK call
  shapes, per `Resolver.Anchors`) are authoritative; op labels in string literals are a
  cross-check. `Type*` constants with a `<provider>:` value are the types (other string consts
  such as `quotaServiceName = "gcp:cloudquotas"` are ignored by name).
- **Naming a type is not storing it.** A function's `Type*` identifiers count only when the
  function, or something it reaches, builds a `store.Resource` or calls `UpsertResource(s)` /
  `InsertResourcesIfAbsent`. The signal travels down through callees and then **up** the caller
  chain to a fixpoint (`markStoring`): the store is usually one hop down (a batch handed to
  `upsertWithProjClosure`), and a phase table such as `wafPhases` names the type and the op while
  its storing driver sits one hop up and never sees the constant. Relationship and hierarchy
  writes are deliberately not the signal — a resolver names its source types in a
  `store.ResourceFilter` and writes edges alone, and counting those made
  `microsoft.insights/diagnosticsettings` claim foreign types. Without a store in reach the
  anchor still stands, so the candidate stays **covered as `sidecar`**; only the credited types go.
- **Fed callees only.** `reachableTypes` walks `walkFedCallees`: a callee counts when the call
  hands it something the caller produced (a local, a field of one, a composite, a call result),
  or when the caller was itself fed and forwards its parameters on (`linkFeeds`, a fixpoint — a
  store four plain pass-through hops down is still this listing's). A helper called with nothing
  but the caller's own `(ctx, st, scanID)` cannot be storing rows this listing returned, so its
  types are not credited to this anchor; it keeps its own pairing. `orphanTypes`/`derived` read
  the **unpruned** set instead, because "rows built from a sibling's listing" is exactly what
  derived means.
- Reach: a function's types are its own plus its fed callees' to depth 3, and past the cap only
  through callees that anchor nothing themselves — the cap holds over-attribution down, but a
  correct scanner storing four hops down was reported `unexplained` and failed both gates. A
  method call on a local
  (`s.scanTables`) and a method value passed as an argument (`forEachItem(…, s.scanDataset)`)
  both resolve to the one method of that name in the package (ambiguous names resolve to
  nothing). A `Type*` constant passed as an argument flows to the callee per caller
  (`fn.outflow`/`inflow`): the helper's own pairing carries every caller's type, a caller's
  pairing only what it and its walked callees pass. The union rule it replaced let
  `storeIDs(st, TypeA, …)` from one scanner explain TypeB from a scanner with no SDK call
  (fixture `scanMasked`) and hid two Azure sdk-skew types behind sibling anchors.
  A package-level `var` table naming `Type*` constants flows into every function that
  references the var (`collectVarTypes`), and `SDKFiles` counts those references, so a
  table-only type is `unexplained`, never `non-sdk`. Labels resolve against the function, its callees and its direct callers — the
  label sits at the error site, the pager is often built one frame up.
- `Walk` is deterministic within a process: callee sets are walked in sorted order and two
  callees anchoring one candidate tie-break by `betterAnchor` (a listing beats a detail read,
  then the smaller label). Map-range order otherwise picked the reported op per run.
- Kinds: `emits` (anchor + types), `sidecar` (anchor, no types — a listing helper; its direct
  caller is then paired with what it stores), `derived` (a dispatcher with no anchor of its
  own: types no anchored callee stores, paired with the listings whose service or leaf relates
  to the type, minus types some `emits` pairing already carries), `label` (label with no call
  anywhere), `other` (non-candidate op), `skew` (call the pinned SDK lacks).
- A **promoted** anchor — one a typeless listing helper contributed, not one this function calls
  — is credited only with what this function's own flow stores, never with what a *caller* passed
  in. The `cur == f` inflow rule is right for a helper that genuinely lists and stores and wrong
  here: it gave `microsoft.resources/resourcegroups` types from unrelated callers of the
  same fan-out helper.
- Unpaired reasons: `non-sdk` (every file referencing the const imports no SDK module —
  Entra over Graph), `other-op:<label>`, `sdk-skew:<op>`, else `unexplained` (fatal). A `label`
  pairing does **not** count as paired (it proves no SDK call, which is why `inventory.go`'s
  `pairingKinds` excludes it), and `derived` yields to `other-op`/`sdk-skew`: evidence by
  proximity must not displace a named op.
- Fixtures: each resolver's synthetic scanner package is
  `internal/providers/<p>/<p>inventory/testdata/scannerpkg`, parsed, never compiled, against that
  package's `testdata/cache`; its `resolver_test.go` asserts one case per rule and the exact line of
  each diagnostic — renumber when editing those files. Shared assertions live in
  `pairing/pairingtest`. The core's own `pairing_test.go` pairs a fake provider
  (`fakeResolver`, `testdata/scannerpkg`) end to end, proving the walk carries no provider
  knowledge.
- sdk-skew is expected: an op newer than a pinned snapshot, or deleted upstream while the linked
  SDK still has it. Provider specifics in `<p>inventory/CLAUDE.md`.
