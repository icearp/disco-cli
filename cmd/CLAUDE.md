# CLAUDE.md — `cmd/`

Cobra command layer.

## CLI structure

- `disco scan` — runs all registered providers in parallel
- `disco scan <provider>` — single provider (e.g. `disco scan aws`)
- `disco scan --providers aws,gcp` — only named providers (comma-separated `StringSlice`)
- `disco resources` — query local DB with filters (`--providers`, `--type`, `--regions`, `--status`, `--tag-key`/`--tag-value`, `--output`); `--providers`/`--regions` are comma-separated multi-value. `disco resources show <id|native-id|name>` resolves and prints one resource via `store.ResolveResource`.
- `disco quotas` — query recorded service quota limits (`--providers`, `--accounts`, `--regions`, `--service`, `--adjustable=true|false`, `--raised`, `--changed`, `--limit`, `--output`). Quotas live in their own `quotas` table, not in `resources`. AWS quotas need a scan with `--include-service-quotas`; Azure and GCP quotas arrive on every scan.
- `disco diff <scanA> <scanB>` — emits `added` (first seen in B) and `stale` (verified in A, not refreshed by B) rows (`store.ScanDiff`); no attribute-change rows
- `disco graph <resource-id> --depth N --kinds contains,attached-to --direction both --output dot --dot-theme light|dark` — walks `relationships` + `hierarchy_closure`.
- `disco graph complete` — dumps every customer resource + every provider-managed resource that shares an edge with one. No seed, no BFS — backed by `store.GraphAll(GraphAllOpts)` which reads `ListResources({IncludeManaged: true})` paginated + `ListRelationships()` and applies the customer-edge inclusion rule in-memory. `--include-managed` keeps orphan managed nodes too. Traversal flags (`--depth`/`--kinds`/`--direction`) ignored.
- `disco check --rules ./policies --severity high --output sarif` — Runs OPA Rego policies against store. Findings reported → exit 1 by default; `--exit-zero` overrides for inventory-only runs. `--rules` takes `.rego` files or directories (recursive). `--output` ∈ `table|markdown|csv|json|jsonl|sarif` (sarif = v2.1.0 for GitHub/GitLab code-scanning, marshalled inline in `cmd/check_sarif.go` — no external SARIF lib). Bundled packs: `--packs` (below). Each policy module must populate `data.disco.deny` (set) with finding objects shaped `{id, severity, message, resourceId?, tags?, category?, remediation?, refUrl?}` (decoded into `policy.Finding`). Input is camelCase, built by `resourceToInput` (`internal/policy/policy.go`); `attributes`/`tags` are decoded objects, not raw strings.

## `disco coverage`

Drift-detection cmd, split into five subcommands. Bare `disco coverage` prints help. Add new coverage-related flags on the matching subcommand in `cmd/coverage.go`. Provider-side glue lives at `internal/providers/<p>/<p>_coverage.go`. Parent owns `--output` (PersistentFlags), default `table`, inherited by every subcommand.

- `disco coverage services` — per-service coverage matrix derived offline from the SDK source cache (`--sdk-cache`, default `$XDG_CACHE_HOME/disco/sdk`; absent → `errCoverageInventoryUnavailable`, exit 2, hint `disco coverage sdk fetch`) and the scanner source (`--source-root`, default cwd when its `go.mod` is disco-cli; empty → name matching only, warning on stderr). Buckets and reasons: `internal/coverage/CLAUDE.md`. `--filter all|covered|uncovered|attribute|excluded|disco-only|gaps|registry-drift` (`gaps` = uncovered ∪ unexplained disco-only) narrows rows after the headline is computed. `--check-strict` exits 1 only on an *unexplained* disco-only row (an emitted type paired with no SDK call). `--cross-check` additionally fetches the live registry (CFN ∪ Service Reference / ARM Providers / Discovery) and adds `registry-drift` rows; `--regions/--profile/--subscriptions/--timeout` apply only then, and a failed fetch is always fatal (`errCoverageRegistryUnreachable`, exit 2) so an empty registry never masquerades as drift. `--filter registry-drift` without `--cross-check` is rejected. Formats: table (headline + rows), markdown (headline, pins, per-service table, bucket sections), csv, json (`[]coverage.Matrix`), jsonl (rows). `--check-strict`, `--baseline` and `--write-baseline` all measure the pairing, so `requirePairing` refuses them with `errCoverageInventoryUnavailable` (exit 2) when any matrix has `!Pairing` — a name-matching-only baseline would ratchet against a different metric. `--write-baseline FILE` / `--baseline FILE` (`internal/coverage/baseline.go`; the same path for both is rejected, the fresh matrix would compare with itself; a write merges into the existing file rather than truncating it) see the **unfiltered** matrices — `buildServiceMatrices` returns every row and `runCoverageServices` filters after the baseline step, so `--filter gaps` in `make gen-coverage` cannot empty the baseline. Fatal baseline drift returns `errCoverageBaseline` (exit 1) after the matrix renders, and `--check-strict` returns `errCoverageStrict` the same way; neither adds the JSON error envelope, the matrix is the payload. Baseline drift kinds and which are fatal: `internal/coverage/CLAUDE.md`.
- `disco coverage regions` — diff each provider's static `RegionNames` slice against the cloud's live SDK region list. `--regions <r1,r2>` post-filters the diff to those regions; full live list still fetched. A failed region-list fetch is always fatal (exit 2, like `services`); `--check-strict` exits 1 on any non-covered row.
- `disco coverage resolvers` — implemented by AWS, Azure, and GCP (any provider whose coverage.Provider also satisfies `coverage.ResolverAuditor`). `--providers` selects which (unset = every auditing provider; naming one without support errors). Default mode lists every registered resolver with its EdgeDecl count + service segments touched; `--only-unannotated` omits annotated resolvers. `--missing` flips to the orphan-type inventory (emitted disco types never appearing as `EdgeDecl.Source`). `--services ec2,s3` filters to resolvers (or orphan types) touching named services.
- `disco coverage verify` (`cmd/coverage_verify.go`) — unrelated to `disco verify` (snapshot archives); keep both help texts saying so. `--scan-id`: `latest` (default; `LatestCompleteScan`, completed/partial, never running), a full id, or the 8-char prefix `disco scans` prints (`isScanIDPrefix`/`resolveScanIDPrefix`). Compares the types one scan stored (`store.TypesForScan`: rows it discovered **or** re-verified) with `Emits()`. `--providers` defaults to the scan's scope, so a single-provider scan is not buried under 2,000 out-of-scope rows.
  - `emitted-undeclared` (a stored type no scanner declares) returns `errCoverageUndeclared` **after** rendering: exit 1, no JSON error envelope (rows are the payload, like `check`).
  - `declared-not-emitted` carries one reason; first match wins, in this order:
    1. `out-of-scope` (provider not in `scans.scope.providers`), then service outside `scope.<provider>.services` (a `--services`-filtered scan is not an empty account).
    2. `scan-error: <code> (<region>)` from `scans.errors`, matched in two passes: exact service or whole-scan first; then, only when the provider stored nothing, any error of that provider (`aws:load-accounts` on an expired login — the label is no scanner service). So one provider-prefixed entry cannot answer for every type. Whole-scan = `scan` and `scan:interrupted` (no provider, so it explains every provider of the scan); `scan:subscription` deliberately is not, so one unreachable Azure subscription does not answer for the tenant-scoped types. A type's service names come from `typeServiceNames`: the scanner services registered from the type's own file (`coverage.ServiceMapper`), its declared `Service`, its type segment. Scanner names (`aws:sso-admin`) and declared services (`sso`) differ for dozens of services; `TestEveryEmittedTypeHasScannerService` guards the join.
    3. `warning: <label> (<region>): <msg>` from `scans.warnings`, whose op label pairs to the type via the SDK cache + scanner source. With the pairing present only a label paired to the type matches — a label it does not know is a store-level warning such as a native-id collision, not an op. Without the pairing a label joins by service prefix.
    4. Fallbacks: `nothing stored: <provider> recorded no rows and no failure`; `no rows (scan limited to regions: …)` when `scope.<provider>.regions` narrowed the run; else `no rows`.
  - A reason renders code, service, region-or-scope and the truncated message; the code is the literal `Error` whenever the runner could not read one. Persisted entries are `<provider>:<service|label>`; `stripProvider` removes the prefix, twice when a scanner already prefixed it.
- `disco coverage sdk fetch|status` (`cmd/coverage_sdk.go`) — both validate `--output` up front
  (`table` or `json` only) and emit the `maybeStructuredError` envelope, so `-o json | jq`
  sees a parseable failure. `status` prints the table on stdout and, on stderr, any content-pin disagreement plus the reason a present directory reads as `absent` (wrong provider/ref, or a different source spec). Populates/inspects the SDK source cache the coverage denominator derives from (`internal/sdkinv`; each provider's extractor registers from `internal/providers/<p>/<p>inventory`, blank-imported via the slim-gated `internal/providers/all/<p>.go`). Persistent `--sdk-cache` (default `$XDG_CACHE_HOME/disco/sdk`); `fetch --providers/--force`. `resetCoverageFlags` recurses one level so these subcommands' `--providers` reset too.

`--providers` values are lower-cased and trimmed inside `coverage.Get` / `sdkinv.Get`, not at the
call sites.
`--services` on `coverage services` matches the row's SDK service **or** the disco type's service
segment (`--services cloudwatch` finds `monitoring/alarm`), and a value matching no row is named on
stderr — the headline above the table is unfiltered, so a zero-row table otherwise reads as a
coverage claim.

`--source-root` defaults to the cwd when `go.mod`'s first line (whitespace-trimmed) names this module; `.gitattributes` pins `*.go`/`go.mod`/`go.sum` to LF because a CRLF checkout silently turns pairing off.

Plural flags throughout: `--providers` (StringSlice; empty = all), `--regions` (StringSlice; semantics differ per subcommand — see above), `--services` (StringSlice; cross-cutting filter on services + resolvers subcommands). Tests must call `resetCoverageFlags(t)` before each `cmd.Execute()` because pflag StringSlice values accumulate across consecutive runs.

## `cmd/disco-scaffold` emits only what the package does not already have

The generator reads the provider package before writing: a `registerService`
already claiming `<prov>:<svc>` (a duplicate panics every provider at init) or an existing
`func scan<Svc>` (gcp:spanner's scanner lives in `databases_scanners.go`) suppresses **both** the
registration and the stub, and the header says why. Type strings and const names carry every key segment below
the service (`azure:microsoft.compute:virtualmachinescalesets:virtualmachines:runcommands`), because
the leaf alone declared `virtualmachines/runcommands` and
`virtualmachinescalesets/virtualmachines/runcommands` identically — and `format.Source` parses a
duplicate const happily. A type string the provider already declares, or two rows declaring one
string, refuses the whole scaffold with the conflicting keys named. `--write` joins `--source-root`
(both the path and the existing-file guard) and is refused when it is empty.

The stub scanner's imports, signature and TODO body come from the provider's
`coverage.ScaffoldStubber` (`ScannerStub()` on its `coverageProvider`), not from cmd. A provider
without it gets descriptors only; `TestGenScaffold_EveryProviderStubsAScanner` fails if a
registered provider loses it.

Segment spelling = `sdkinv.Singular`; fix wrong spellings in its `irregular` table (with a
`norm_test` pair), never a local shim.

## Resume

`disco scan --resume <scan-id|latest>` reuses a previous scan_id instead of generating a fresh one. `latest` picks the most-recent scan whose status is `running` or `partial`. Resume only reuses the id: every service is re-listed and `Finalize` stamps the old scan record (the checkpoint table that promised mid-service resume never had a writer and was dropped in `020`). `startOrResumeScan` in `scan.go` owns the dispatch. Without `--resume`: fresh scan_id.

## Parallel scanning

Fan-out and finalisation live in `internal/scanrun` (shared with the API driver): `RunScanners` runs scanners concurrently via `sync.WaitGroup` (no sibling cancellation); `Finalize` records Complete/Partial. `runScan(cmd, scanners)` (`scan.go`) owns the CLI side (open-db, `CreateScan`/`--resume`, progress, grouped error block); `scanCmd.RunE` passes `providers.All()`, per-provider subcommands a one-element slice. Error tolerance: `internal/providers/CLAUDE.md` "Errors never abort scan".

## Provider wiring (no provider names in cmd)

`cmd/providers.go` blank-imports only `internal/providers/all`; `cmd/scan.go`'s `init()` builds `disco scan <name>` from `providers.All()`, so a slim build exposes only its compiled providers and adding a provider needs no `cmd` change. Never `import` a provider package in cmd: reach provider-specific data through a registry interface (`coverage.ResolverAuditor` for `coverage resolvers`), so a slim build returns a clean "not in this build" error instead of failing to compile. `all/` wiring, slim tags, add-provider steps: `internal/providers/CLAUDE.md`.

## Scan subcommand flag registration

`scan.go` `init()` builds per-provider subcommands. Register `--services` / `--regions` / `--profile` **only when the scanner implements the matching capability interface** (`providers.ServiceFilterer`, `RegionOverrider`, `ProfileOverrider`; also `--scope-regions` via `RegionScopeToggler`, `--include-service-quotas` via `ServiceQuotasIncluder`). Listing a flag a provider silently ignores misleads users — Cobra has no per-subcommand "hide if unsupported" toggle. New optional flags follow same gate. Service-prefix examples come from each provider's `providers.ServiceFilterExemplar` (`ServiceFilterExample()`; fallback `<name>:<service>`) via `serviceFilterExample` — keep each provider's string truthful (e.g. `aws:ec2,aws:s3`, not `aws:compute`). Note `aws:servicequotas` is opt-in (`serviceEntry.optIn`) — excluded from a default scan; `--include-service-quotas` adds it, or select it by name with `--services aws:servicequotas`.

## Shared render helpers (`helpers.go`)

`ptrOrDash(*string) string`, `short(id string) string` (8-char ID prefix), `renderMessages(w, label, []messageRow, quiet)` (column-aligned grouped block used by `renderErrors`/`renderWarnings` in `scan.go`). New commands rendering tabular output should reuse these instead of redefining.

Output styling: per-format theme modules (`cmd/graph_theme.go` for DOT) own all attribute blocks + a preset map keyed by an enum. Renderers look up presets, never inline color/shape literals. New themes = one entry in the `themes` map; new resource→preset rules = one switch case in `presetForResource`.

## Global flags and CSV columns

- Default invocations are stderr-clean. The "Using config file:" banner is gated behind the global `--verbose` flag (`cmd/root.go::initConfig`); banners added later should reuse the same `verbose` boolean rather than introducing per-cmd `--quiet` flags.
- Cobra's `InitDefaultVersionFlag` (lazy, called at execute) only claims `-v` when no other flag holds the shorthand. Pre-register a global flag with `-v` in `init()` to repurpose it (precedent: `--verbose` in `cmd/root.go`); `--version` long-form keeps working with no shorthand.
- Shared flags on a multi-subcommand verb go on the parent's `PersistentFlags()` (`scansCmd` `-o` reaches `scans show`), not duplicated `Flags()`.
- `resources -o csv` columns come from `resourcesColumns` / `resourceRow` in `cmd/resources.go`. Keep `resourceRow` in the same order as `resourcesColumns`, and keep `resourcesColumns` and `resourcesMarkdownHeaders` the same length (`TestResourcesMarkdownHeadersParity`).

## Read vs write DB opens

Read commands open the DB via `openDB()` (`cmd/helpers.go`) which always opens read-only — defense-in-depth so a future read-side bug can't silently mutate evidence. Write commands (`scan`, `config init`, `check --persist`) call `openWriteDB()` and refuse `dbReadOnly` up-front. The global `--db-readonly` flag is preserved as the writer-refuse override; on read commands it's a no-op (already RO). First-run UX: when the DB file doesn't exist, `openDB()` errors with a "run a scan first" hint inline. Stale-schema gate: `openDB()` probes `schema_migrations` and rejects with a "run `disco scan` to upgrade" hint when the on-disk schema lags the binary's `TargetSchemaVersion()` — RO opens skip migrate by design, so reads must reject pre-flight rather than surface cryptic SQLite errors.

## Shared test helpers (`resources_test.go`)

- `seedTestDB(t)` — temp SQLite + scan record + 2 resources (`aws:ec2:instance`, `aws:s3:bucket`: count them in expected totals, never delete them); sets `viper.Set("db", path)` so cobra cmds pick it up via `defaultDBPath()`.
- `captureStdout(t, fn)` — pipes `os.Stdout` for cmds that write directly to it (not via `cmd.OutOrStdout`).
- `captureStdout` does NOT redirect `os.Stderr`. Stderr writes (population stamps, truncation warnings, banner-under-`--verbose`) bypass test assertions — safe place for telemetry that must not contaminate `-o json|jsonl|sarif` pipelines. If you need to assert on stderr, use `captureStderr` (drained via goroutine to avoid >64KB pipe deadlock).

Cobra package-level flag vars (`graph*`, `resources*`, …) persist across tests because `rootCmd` is shared. Each subcommand test must reset its flags before `cmd.SetArgs(...)` — see `resetGraphFlags()` in `graph_test.go`. Flag pollution is transitive: a NEW test setting `--type`/`--limit`/`--direction` via `SetArgs` can break older sibling tests that only did partial resets (e.g. `resourcesOutputFmt = ""`). When adding such a test, upgrade siblings to the full `resetXFlags()` helper.

Cobra also persists flag-attached values across tests when commands read via `cmd.Flags().GetX("name")` instead of package vars (e.g. `scan.go`'s `fail-on-error`; `resetCoverageFlags` already resets every coverage flag to `DefValue`). A package-var `resetXFlags()` won't clear those — pass an explicit `--flag=false` in negative-case tests, or call `cmd.Flags().Set("flag", "false")` before `Execute()`.

Resetting a flag's variable does not reset pflag's `Changed`, so code reading `Flags().Changed(...)` (graph `blast`'s `--direction` fallback) sees the previous test's flag. A reset helper must also do `VisitAll(func(f){ f.Changed = false })` (`resetGraphFlags`, `resetCoverageFlags`); without it `TestGraphBlast_PrincipalAutoFallback` failed on every `-count>1` iteration.

## `disco history <id>` surfaces the resource version chain

`history` renders every version of a resource oldest→newest —
the read surface for change-over-time. The arg
resolves through `store.ResolveResource` (same exact-id / name / native-id / short-id
lookup as `graph`); the resolved current row's `.ID` (= `root_id`) keys
`store.GetResourceVersions`.

**It also accepts a quota id.** When `ResolveResource` finds nothing, `history`
retries through `store.ResolveQuota` and renders `renderQuotaHistory`
(`cmd/quotas.go`) — a different column set, because the interesting change in a
quota chain is the VALUE, which a resource-shaped view has nowhere to put. Users
paste ids without knowing which table they came from, so the fallback is the
point; when neither matches, the *resource* error surfaces, since that is what
the command is mostly for. `disco quotas --adjustable` is a **string** flag parsed by `parseTristateFlag`, not a bool: a
bool cannot distinguish "not filtering" from "filtering on false", so a bool
would silently turn a bare `disco quotas` into `--adjustable=false`.

Output uses a purpose-built `historyEntry` struct, NOT
`store.ResourceVersion` directly: `store.Resource` has a value-receiver `MarshalJSON`
that would be promoted onto the embedding struct and silently drop the version-only
fields (`verified_at`, `superseded_by`, …) in JSON. New version-chain output paths
must follow the same explicit-struct pattern, not encode `ResourceVersion`. `historyEntry` uses camelCase JSON tags like every other CLI envelope, so it
needs no tagliatelle exclusion.

## Silent exit codes for query-absence

When "no result" is a valid query outcome (e.g. `graph path` between unreachable resources), return a sentinel error from the store layer (`store.ErrNoPath`) and let `cmd/root.go` `Execute()` map it to `os.Exit(1)` without printing. Keeps `RunE` testable — `os.Exit` inside `RunE` bypasses in-process test assertions.

## JSON dialect: camelCase + nested attrs/tags

`store.Resource.MarshalJSON` and `store.Scan.MarshalJSON` are the single source of truth (contract: `store/CLAUDE.md` "Wire shape ≠ storage shape" and "`Scan.StartedAt`"). New JSON output paths encode `[]store.Resource` directly, never a struct embedding it (see `historyEntry`). `disco summary.asOf` is normalised at population time via `store.ToRFC3339`.

`coverage resolvers -o json` / `coverage resolvers --missing -o json` honour `-o`. `--missing` rows carry `refs` (from the SDK cache + pairing, richest first). `--with-refs` needs the cache (exit 2 without it); its semantics: `internal/providers/aws/CLAUDE.md` (resolver audit).

## One error message, not two: `structuredErrorEmitted`

JSON/JSONL output paths: wrap RunE as `func(...) (rerr error) { defer func() { maybeStructuredError(<formatVar>, rerr) }(); ... }` so failures emit a `{"error": "msg"}` envelope on stdout (helper in `cmd/helpers.go`). Skip the envelope for sentinel "absence" errors like `ErrNoPath` where empty stdout + exit 1 is documented contract — `graph path` does this with an `errors.Is` guard.

`maybeStructuredError` (`cmd/helpers.go`) writes a JSON `{"error":"..."}` envelope to stdout when the caller's `-o` is `json`/`jsonl`, AND sets the package-level `structuredErrorEmitted` flag. `cmd/root.go::Execute` reads the flag and skips the duplicate plaintext stderr print so a `disco ... -o json` failure produces ONE message, not two.

## `--scan-id` + `latest` shorthand via `resolveScanID`

`resources`, `summary`, `tag-coverage` accept `--scan-id <id|latest>`; `scans show` and `diff <from> <to>` take it positionally. Every form accepts a full 32-hex id, an 8–31-char prefix (as `disco scans` prints) or `latest`, all through `resolveScanID` — any new scan-id-taking command must do the same. `latest` resolves via `resolveScanID(db, raw)` (`cmd/helpers.go`) to the most-recent scan whose `resource_count > 0` — a scan that recorded no rows (failed early, empty scope, still running) otherwise silently zero-rows the documented drift workflow. (`resource_count` counts `verified_by`, so a re-verify-only run qualifies.) Falls back to the most-recent scan when none qualify with a one-line stderr note. Literal IDs round-trip after a `GetScan` presence check; unknown IDs return `scan %q not found`. Plumbed onto `ResourceFilter.SeenBy`; `scan --resume <id|latest>` uses the same shorthand convention.

Scan and check-run "latest" ordering is `store.newestFirst()` (rationale, and why ids stay random: `store/CLAUDE.md` "Resource IDs").

## `resources --scan-id` matches `discovered_by` OR `verified_by`

`--scan-id` on `resources`, `summary`, `tag-coverage` sets `ResourceFilter.SeenBy` (semantics and cost: `store/CLAUDE.md` "ListResources filter shape"). For the latest scan it lists exactly the current rows `scans.resource_count` counts, so a re-verify-only scan lists its rows. For an older scan, a row a later scan re-verified matches only if that scan discovered it — `verified_by` moves forward. "New this run" is `disco diff`'s `added` (`discovered_by = B`); there is no `--scan-as`. `resources --id` short-circuits on `root_id` via `ResourceFilter.ID`.

## `disco graph complete --orphans-only` filters to disconnected nodes

Post-pass keeps only nodes whose ID appears in neither `from_id` nor `to_id` of any returned edge. Surfaces dangling EBS volumes, key-pairs no instance uses, IAM principals with no group/policy, etc. — the forensic / hygiene targets the IR persona was after. Implementation lives in `cmd/graph.go::filterOrphans`; renderers stay unchanged because the filter only drops nodes, never reshapes `GraphResult`.

## `disco verify` says `OK (unsigned …)` by default

`verify`'s success line is `OK (unsigned — manifest not authenticated): ...` for archives without a detached signature, `OK (signed — manifest authenticated via ed25519): ...` when both `--signature` and `--pubkey` are supplied and validate. The wording change deliberately rules out the bare-`OK` interpretation that would mislead a CI step into treating internal-consistency as provenance. `verify` also warns on stderr for a `dev` or `+dirty` `toolVersion`; `--require-clean` / `--require-signed` turn those into failures.

Friendly-error wrapping lives in `friendlyArchiveErr(err)` (cmd/verify.go): collapses raw xz/gzip decoder messages into `verify failed: archive corrupt or truncated`. The original is preserved when `--verbose`. Format-detection errors are intentionally returned BEFORE the friendly wrap so unsupported extensions surface clearly.

## `disco snapshot --signing-payload <file>` is the signing primitive

`internal/snapshot.CanonicalManifestBytes(m)` returns deterministic JCS-style bytes (`json.Marshal(m)` — struct field declaration order, no whitespace). Sign externally (`openssl pkeyutl -sign -inkey priv.pem -rawin -in payload -out sig`, `minisign`, `ssh-keygen -Y sign`, cosign blob-attest) and ship the detached signature alongside the archive. `disco verify --signature <sig> --pubkey <key>` re-derives the canonical bytes from the embedded `manifest.json` and validates with `crypto/ed25519` — stdlib only, no x/crypto dep.

`LoadEd25519PublicKey` accepts PEM-wrapped PKIX SubjectPublicKeyInfo (the format `openssl pkey -pubout` produces) or a raw 32-byte binary key. OpenSSH `ssh-ed25519 AAAAC3...` text is intentionally out of scope — convert with `ssh-keygen -e -m PKCS8` first.

## `disco snapshot <output-file>` writes a single archive

Output is one file — `.zip`, `.tar.gz` (`.tgz`), or `.tar.xz` (`.txz`) — extension drives format. `internal/snapshot.DetectFormat` rejects unknown extensions with a clear error listing supported shapes. `cmd/snapshot.go` opens the source DB via `store.OpenReadOnly`, issues `VACUUM INTO '<out>.db.tmp'` to a sibling temp file, hashes it, packages disco.db + manifest.json into the archive via `snapshot.WriteArchive`, then `os.Rename` for atomicity (temp + rename, `defer os.Remove` on failure, so `disco verify` never sees a partial archive; precedent `internal/snapshot.WriteArchive`). `--db-readonly` is allowed (the global flag scopes the source, not the output). `manifest.dbSha256` hashes the inner DB (not the archive) so receivers spot-check the same value across formats. `internal/snapshot` package houses the manifest format (`disco-snapshot/v1`) and the per-format archive readers; `disco verify` decodes via the same package without extracting to a temp dir.

## `disco check` opens DB read-only by default

`check` is logically a read; opening writable flips the SQLite WAL header and silently mutates `disco.db`, breaking any subsequent `disco verify` against a snapshot of the same DB. So `check` uses `openDB()` (always RO) unless `--persist` is set: `needsWrite := checkPersist` (cmd/check.go) flips the open to `openWriteDB()` after refusing `--db-readonly` up-front, and the persist body writes the run + findings inline.

## `disco check` defaults to customer-managed; `--include-managed` opts in

`check` mirrors `resources` / `summary`: customer-only by default, so BYO Rego authors need no `not input.managedByProvider` guard against provider-managed rows (AWS-managed IAM policies, Azure built-in role definitions) they cannot remediate. `--include-managed` is wired straight onto `ResourceFilter.IncludeManaged`.

## Findings gate the exit code by default; `--exit-zero` overrides

When findings are reported and `--exit-zero` is not set, `RunE` returns the package-level `errFindingsReported` sentinel; the deferred `maybeStructuredError` wrapper checks `errors.Is` and skips emitting `{"error":"N finding(s)"}` to stdout. The findings array IS the payload, the exit code IS the gate — strict consumers (`json.load`, `jq -e`, `go json.Decoder`) parse the stdout in one pass. Stderr keeps the human-readable `N finding(s)` count line.

CI steps gate on findings without an extra flag, mirroring `tag-coverage --min-coverage` / `--exit-zero` (`cmd/tag_coverage.go`). Tests that exercise rendering paths must pass `--exit-zero` to keep `cmd.Execute() == nil` when fixtures produce findings.

## SARIF rule polish: descriptions, defaultConfiguration, partialFingerprints, taxonomies

`cmd/check_sarif.go` populates:
- `rules[].shortDescription` / `fullDescription` (mirror the message); `rules[].defaultConfiguration.level` from `severity` via `severityToLevel`; `rules[].properties.tags` flattened as `["waf_pillar:security", "soc2:CC6.1", ...]`
- `results[].partialFingerprints["disco/v1"] = sha256(rule_id+":"+resource_id)[:16]` so GitHub code-scanning de-dupes across runs
- `runs[0].taxonomies[]` — one taxonomy per non-empty `tags.<key>` in `taxonomyKeys` (`waf_pillar`, `soc2`, `iso27001`, `pci_dss`, `nist_800_53`, `waf_qid`); taxon IDs are the unique tag values, sorted for byte-stable output. Empty keys are skipped, so a BYO rule emitting `soc2` adds a taxonomy without code change.

Bundled `aws-waf` rules ship a deliberately minimal `tags: { waf_pillar, waf_qid }` — the pack is the wiring sample, not a curated framework-mapped pack. The `soc2` / `iso27001` / `pci_dss` / `nist_800_53` keys are kept reserved in `taxonomyKeys` so future framework packs and BYO Rego authors can populate them and get SARIF taxonomies + `--tag soc2=CC6.1` filtering for free. Don't fold control-catalogue mappings into the sample rules — that's a curated pack's job.

The unprefixed `pillar` key is intentionally reserved for a future cross-framework grouping (e.g. NIST CSF Identify/Protect/Detect/Respond/Recover, CIS controls categories) — using it for AWS WAF pillars only would collide. Frame-specific keys (`waf_pillar`, future `csf_function`, `cis_category`) are the convention.

## Rego authors must check scanner wrapping for attrs path

Some scanners wrap the SDK response under a key (CloudTrail: `{"Trail": ..., "Status": ...}`; ELBv2 LB: `{"lb": ..., "type": ...}`; EventBridge rule: `{"Rule": ..., "Targets": [...]}`; Lambda function: SDK type embedded with `Code` sibling). Rego rules reading these resources must match the wrapped path: `input.attributes.Trail.IsMultiRegionTrail`, not `input.attributes.IsMultiRegionTrail`. Known wrappers: `internal/providers/aws/CLAUDE.md` ("Wrapper-key json tags…" and the resolver-test attrs helpers); read the type's scanner before authoring a rule. Wrong path silently matches nothing.

## `--packs <name,...>` loads bundled Rego packs

`disco check --packs aws-waf` loads `internal/policy/aws-waf/*.rego` via `//go:embed`. Pack names follow `<provider>-<framework>` convention. `policy.LoadPacks([]string)` walks the embed.FS, returns `map[name]source`; `policy.NewEngine(ctx, paths, modules)` accepts both `--rules <dir>` paths AND module map in one call so `--rules ./mine --packs aws-waf` composes. Adding a new pack = one `//go:embed` line + one entry to `AvailablePacks()`. Bare `disco check` errors with "--rules or --packs is required (e.g. --packs aws-waf)" — never default to one or the other silently.

## Set `Args: cobra.NoArgs` on flag-only subcommands

Cobra's default Args validator silently accepts arbitrary positional tokens. `disco resources --discovered-since 2025-05-01 12:01:01` parses `--discovered-since=2025-05-01` and treats `12:01:01` as a positional, ignored without error. Read commands with no positional arity (`resources`, `summary`, `scans`) MUST set `Args: cobra.NoArgs`. Use `cobra.ExactArgs(N)` / `MaximumNArgs(N)` / `MinimumNArgs(N)` per shape — never leave Args unset on a flag-only verb.

## Time filters: `{discovered, created} × {since, before}` — half-open `[since, before)`

`resources`, `summary`, `tag-coverage` accept the column-anchored time-filter pair `--<col>-since` / `--<col>-before`:

- `--discovered-since <ts>` → `ResourceFilter.DiscoveredSince` → SQL `discovered_at >= ?`. Inclusive lower bound on first-seen-by-disco.
- `--discovered-before <ts>` → `ResourceFilter.DiscoveredBefore` → SQL `discovered_at < ?`. Strict upper bound; pairs with `--discovered-since` for half-open `[since, before)` intervals; also serves as the standalone "stale" hygiene query.
- `--created-since` / `--created-before` mirror the pair on the resource's intrinsic `created_at` column (lifted from the SDK at scan time). Rows with NULL `created_at` are excluded from both filters because `NULL < X` is unknown in SQL — not every scanner lifts the SDK timestamp yet (see EBS volume precedent in commit 8e61c52).

`disco findings list` carries an analogous `--run-since` flag that filters check-run `started_at` (different table, different anchor — not a `ResourceFilter` field).

All take `<RFC3339|YYYY-MM-DD>` via `parseTimeFlag` (`cmd/helpers.go`). Bare date auto-extends to `T00:00:00Z`; non-UTC zones normalise to UTC. Discovered-axis filters are pinned to `discovered_at` (immutable first-seen), NOT `verified_at` — re-scans don't re-stamp it. Means `--scan-id latest --discovered-since X` legitimately returns 0 when the latest scan only re-verified pre-existing rows.

Backed by `singleSetString` (`cmd/helpers.go`) — pflag.Value that errors on second `Set()` so repeated `--discovered-since A --discovered-since B` rejects rather than last-wins-silently. Test reset helpers must call `<flag>.reset()` on the value, not `<flag> = ""` (compile error: untyped string into struct).

New column-anchored time filters follow the same half-open `{*-since, *-before}` shape, never `{since, until}`.

## `tag-coverage --case-insensitive` folds case before tallying

`tag-coverage` exposes `--case-insensitive` to fold tag keys to lower-case during aggregation (`environment`/`Environment` collapse into one row). Implementation tracks the first observed casing in an `origKey` map so table output preserves operator-friendly capitalisation.

## `summary` BY ACCOUNT rollup

`summary` always renders a `BY ACCOUNT` section (no flag) and a `byAccount: [{accountId, accountName, count}]` JSON field. The CSV `dimension` column carries `account` rows alongside `provider` / `region` / `type`. Account name renders parenthetically when set (`123456789012 (prod)`); empty otherwise. Counts roll up via `acctCounts` in `buildSummary`.

## `--exclude-types` plumbs through `ResourceFilter.ExcludeTypes`

`resources`, `summary`, `tag-coverage`, and `check` all expose `--exclude-types` (StringSlice → comma-separated). All forward to `store.ResourceFilter.ExcludeTypes` (SQL `NOT IN`), so the filter is applied at the SQL layer: so denominators (tag-coverage rate, summary `total`) drop along with the displayed rows — not just display masking. Compatible with `--type` (include); both clauses AND together. Default-hide of noisy types (e.g. `aws:logs:log-stream`) deliberately rejected — security work cares about log-stream coverage; the flag is the user-driven escape hatch.

## DOT `dir=back` requires endpoint swap, not just attribute

Graphviz `dir=back` only re-renders the arrowhead — rank still flows tail→head. To flip layout direction (e.g. force `attached-to` parent left of source under `rankdir=LR`), `renderGraphDot` swaps `FromID`/`ToID` for any edge whose preset carries `dir=back`. Adding `dir=back` to a theme preset alone is a no-op for rank; both pieces are needed.

## Output-format parity: `table | markdown | csv | json` floor

Every reportable subcommand (`resources`, `summary`, `scans` + `scans show`, `tag-coverage`, `diff`, `graph` + `path`/`blast`/`complete`, `check`, `coverage` + `services`/`regions`/`resolvers`, `findings list`/`runs`, `quotas`, `history`) accepts the four canonical output formats as a floor. Per-command extras (`jsonl`, `sarif`, `dot`, `mermaid`) layer on top. Markdown rendering goes through the shared `renderMarkdownTable(w, headers, rows)` helper in `cmd/helpers.go` for byte-stable output. Markdown case label is `markdown`; `md` is accepted as a short alias. Operational commands (`snapshot`, `verify`, `config init`) have no `-o` flag; `scan -o table|json` applies only to `--dry-run`. Help text lists every supported format the command accepts in canonical order then extras.

## UX consistency conventions (scan exit codes, collections, completion)

Preserve these shapes:

- **Scan exit codes.** `runScan` (`scan.go`) returns sentinel `errScanInterrupted` on SIGINT/SIGTERM (ctx cancelled) → `cmd/root.go::Execute` maps it to exit **130**; with `--fail-on-error` a partial run (one or more services errored) returns `errScanPartial` → exit **1**. Default partial stays exit 0. The summary line is already on stdout; the sentinels only carry the exit-code gate (no duplicate stderr print, mirrors `errFindingsReported`). `--quiet` suppresses the `Scan … started` / `Resuming scan …` banners too (each guarded by its own `if !quiet`, the same boolean the per-service line uses).

- **Scan progress (`cmd/progress.go`).** Per-service / resolve lines go through `progress.line()` to stderr. A braille spinner (`⠋ scanning… <elapsed> · <n> done`) animates only when `!quiet && !no-progress && isTerminal(stderr)` (`isTerminal`: stdlib `*os.File` + `os.ModeCharDevice`; no `x/term`/`go-isatty` dep); off-TTY (CI, tests) it never starts and `line()` is a plain write. No `done/total`: the denominator (Azure subscriptions, GCP projects) is discovered mid-scan. Clears use `\r` + spaces, no ANSI (non-VT Windows). All stderr writes share one mutex; `p.stop()` (`sync.Once`) runs after `RunScanners` and before warnings/errors render. `--quiet` = final summary only; `--no-progress` = lines without the spinner.

- **Empty-result JSON is `[]`, never `null`.** `renderScans` / `renderCheckRuns` force a non-nil slice before encoding; `diff` forces non-nil `Added`/`Stale`; `store.ScanDiff` carries camelCase json tags. Mirror the non-nil-before-encode pattern in any new array command (precedent: `resources`).

- **Collection grammar.** The resource collection is the canonical noun `resources` (consistent with the other collections `scans` / `findings`). Scan runs are top-level (`disco scans`); check runs stay nested (`disco findings runs`) — they are subordinate to the findings they produce, and a top-level `disco checks` would overload the `disco check` verb. This asymmetry is deliberate.

- **Shell completion.** `--output` flags register `staticCompletion(<formats>)` and `scan --providers` registers `completeProviderNames` (`cmd/completion.go`). A new reportable command should register `--output` completion with exactly the formats its switch accepts. Resource/scan-ID argument completion is intentionally not wired (it needs DB I/O on the completion path) — a deliberate follow-up, not an oversight.

- **Markdown cells are sanitized centrally.** `renderMarkdownTable` runs every header/cell through `sanitizeMarkdownCell` (escape `|`, fold newlines), so callers may pass raw JSON blobs (scope/tags) without pre-escaping.
