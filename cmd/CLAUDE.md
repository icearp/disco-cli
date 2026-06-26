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
- `disco check --rules ./policies --severity high --output sarif` — Runs OPA Rego policies against store. Findings reported → exit 1 by default; `--exit-zero` overrides for inventory-only runs. `--rules` takes `.rego` files or directories (recursive). `--output` ∈ `table|json|jsonl|sarif` (sarif = v2.1.0 for GitHub/GitLab code-scanning, marshalled inline in `cmd/check_sarif.go` — no external SARIF lib). Engine in `internal/policy/` ships with no first-party policies bundled — bring your own (Conftest AWS, regula, in-house CIS pack). Curated compliance packs (NIST, CIS, PCI-DSS, Well-Architected) are future work, not yet bundled. Each policy module must populate `data.disco.deny` (set) with finding objects shaped `{id, severity, message, resource_id?, tags?, category?, remediation?, ref_url?}`. Input shape: `{id, provider, account_id, type, native_id, name, region, status, attributes}` — `attributes` is the decoded `AttributesJSON` (object), not the raw string.

## `disco coverage`

Drift-detection cmd, split into three subcommands. Bare `disco coverage` prints help. Add new coverage-related flags on the matching subcommand in `cmd/coverage.go`. Provider-side glue lives at `internal/providers/<p>/coverage.go`. Parent owns `--output` (PersistentFlags), default `table`, inherited by every subcommand.

- `disco coverage services` — per-service type matrix. Reads scanner-declared `emits []coverage.TypeDecl` and matches against live upstream registries (CFN ListTypes / ARM Providers/List / GCP Discovery). Buckets: covered / uncovered / synthetic / upstream-missing. `--check-strict` exits 1 on any upstream-missing row, 2 on transient registry-fetch failure (the distinct exit code lets CI branch). `--regions us-east-1,us-west-2` triggers per-region CFN calls + unions the upstream slice (catches per-region launch drift).
- `disco coverage regions` — diff each provider's static `RegionNames` slice against the cloud's live SDK region list. `--regions <r1,r2>` post-filters the diff to those regions; full live list still fetched. `--check-strict` exits 1 on any non-covered row.
- `disco coverage resolvers` — implemented by AWS and Azure (any provider whose coverage.Provider also satisfies `coverage.ResolverAuditor`). `--providers` selects which (unset = every auditing provider; naming one without support errors). Default mode lists every registered resolver with its EdgeDecl count + service segments touched; `--only-unannotated` omits annotated resolvers. `--missing` flips to the orphan-type inventory (emitted disco types never appearing as `EdgeDecl.Source`). `--services ec2,s3` filters to resolvers (or orphan types) touching named services.

Plural flags throughout: `--providers` (StringSlice; empty = all), `--regions` (StringSlice; semantics differ per subcommand — see above), `--services` (StringSlice; cross-cutting filter on services + resolvers subcommands). Tests must call `resetCoverageFlags(t)` before each `cmd.Execute()` because pflag StringSlice values accumulate across consecutive runs.

## Resume

`disco scan --resume <scan-id|latest>` reuses a previous scan_id instead of generating a fresh one. `latest` picks the most-recent scan whose status is `running` or `partial`. disco persists per-(scan, service, scope) checkpoints (`store.SaveCheckpoint`); a future incremental scanner can consume them on the next `--resume` to skip already-listed pages. `startOrResumeScan` in `scan.go` owns the dispatch. Without `--resume`, behaviour matches pre-Phase-3 — fresh scan_id, no checkpoint reuse.

## Parallel scanning

`cmd/scan.go` runs selected scanners concurrent via plain `sync.WaitGroup` — no sibling cancellation. Per-service / per-region failures collected via `store.OnError` and rendered as one grouped block at end. Scan record always finalised via `db.CompleteScan` (failed or not). Lifecycle + errgroup-error-tolerance details: `internal/providers/CLAUDE.md` "Errors never abort scan".

`runScan(cmd, scanners)` (`scan.go`) holds the shared open-db / `CreateScan` / WaitGroup / `CompleteScan` lifecycle. `scanCmd.RunE` calls it with `providers.All()`; per-provider subcommands call it with a single-element slice.

## Provider wiring (no provider names in cmd)

`cmd/providers.go` blank-imports a single generic aggregator (`internal/providers/all`) — cmd names no provider directly. Inside `all/`, one build-tagged file per provider (`aws.go` etc., gated `//go:build !slim || <name>`) blank-imports that provider's package, registering it via init(); `all/all.go` is an import-less stub keeping the package importable when all providers are tagged out. `cmd/scan.go`'s `init()` iterates `providers.All()` to build `disco scan <name>` subcommands — a slim build naturally exposes only the compiled providers. No `cmd` change when adding a provider. Build-tag opt-in (`go build -tags 'slim aws'`) and add-new-provider steps: `internal/providers/CLAUDE.md`.

Keep cmd provider-agnostic: never `import` a provider package here. When a subcommand needs provider-specific data (e.g. `coverage resolvers` reads AWS's resolver registry), reach it through a registry interface (`coverage.ResolverAuditor`) so a slim build that excludes the provider returns a clean "not in this build" error instead of failing to compile.

## Scan subcommand flag registration

`scan.go` `init()` builds per-provider subcommands. Register `--services` / `--regions` / `--profile` **only when the scanner implements the matching capability interface** (`providers.ServiceFilterer`, `RegionOverrider`, `ProfileOverrider`; also `--scope-regions` via `RegionScopeToggler`, `--include-service-quotas` via `ServiceQuotasIncluder`). Listing a flag a provider silently ignores misleads users — Cobra has no per-subcommand "hide if unsupported" toggle. New optional flags follow same gate. Real service-prefix examples come from `serviceFilterExample(provider)` — keep entries truthful (e.g. `aws:ec2,aws:s3`, not `aws:compute`). Note `aws:servicequotas` is opt-in (`serviceEntry.optIn`) — excluded from a default scan; `--include-service-quotas` adds it, or select it by name with `--services aws:servicequotas`.

## Shared render helpers (`helpers.go`)

`ptrOrDash(*string) string`, `short(id string) string` (8-char ID prefix), `renderMessages(w, label, []messageRow, quiet)` (column-aligned grouped block used by `renderErrors`/`renderWarnings` in `scan.go`). New commands rendering tabular output should reuse these instead of redefining.

Output styling: per-format theme modules (`cmd/graph_theme.go` for DOT) own all attribute blocks + a preset map keyed by an enum. Renderers look up presets, never inline color/shape literals. New themes = one entry in the `themes` map; new resource→preset rules = one switch case in `presetForResource`. Always include a `mono` theme that reproduces pre-theme output byte-for-byte for diff-stable piping.

## Shared test helpers (`list_test.go`)

Reused by `graph_test.go`, `check_test.go`, `diff_test.go`:
- `seedTestDB(t)` — temp SQLite + scan record + 2 resources; sets `viper.Set("db", path)` so cobra cmds pick it up via `defaultDBPath()`.
- `captureStdout(t, fn)` — pipes `os.Stdout` for cmds that write directly to it (not via `cmd.OutOrStdout`).
- `captureStdout` does NOT redirect `os.Stderr`. Stderr writes (population stamps, truncation warnings, banner-under-`--verbose`) bypass test assertions — safe place for telemetry that must not contaminate `-o json|jsonl|sarif` pipelines. If you need to assert on stderr, use `captureStderr` (drained via goroutine to avoid >64KB pipe deadlock).
- Default invocations are stderr-clean. The "Using config file:" banner is gated behind the global `--verbose` flag (`cmd/root.go::initConfig`); banners added later should reuse the same `verbose` boolean rather than introducing per-cmd `--quiet` flags.
- Cobra's `InitDefaultVersionFlag` (lazy, called at execute) only claims `-v` when no other flag holds the shorthand. Pre-register a global flag with `-v` in `init()` to repurpose it (precedent: `--verbose` in `cmd/root.go`); `--version` long-form keeps working with no shorthand.
- JSON/JSONL output paths: wrap RunE as `func(...) (rerr error) { defer func() { maybeStructuredError(<formatVar>, rerr) }(); ... }` so failures emit a `{"error": "msg"}` envelope on stdout (helper in `cmd/helpers.go`). Skip the envelope for sentinel "absence" errors like `ErrNoPath` where empty stdout + exit 1 is documented contract — `graph path` does this with an `errors.Is` guard.
- `list -o csv` columns are positional-stable: append-only when adding fields to `listColumns` / `resourceRow` in `cmd/list.go`. Pre-existing positions back spreadsheet imports keyed on index; reordering breaks downstream silently.
- Read commands open the DB via `openDB()` (`cmd/helpers.go`) which always opens read-only — defense-in-depth so a future read-side bug can't silently mutate evidence. Write commands (`scan`, `config init`, `check --persist`) call `openWriteDB()` and refuse `dbReadOnly` up-front. The global `--db-readonly` flag is preserved as the writer-refuse override; on read commands it's a no-op (already RO). First-run UX: when the DB file doesn't exist, `openDB()` errors with a "run a scan first" hint inline. Stale-schema gate: `openDB()` probes `schema_migrations` and rejects with a "run `disco scan` to upgrade" hint when the on-disk schema lags the binary's `TargetSchemaVersion()` — RO opens skip migrate by design, so reads must reject pre-flight rather than surface cryptic SQLite errors.

Cobra package-level flag vars (`graph*`, `list*`, …) persist across tests because `rootCmd` is shared. Each subcommand test must reset its flags before `cmd.SetArgs(...)` — see `resetGraphFlags()` in `graph_test.go`. Flag pollution is transitive: a NEW test setting `--type`/`--limit`/`--direction` via `SetArgs` can break older sibling tests that only did partial resets (e.g. `listOutputFmt = ""`). When adding such a test, upgrade siblings to the full `resetXFlags()` helper.

Cobra also persists flag-attached values across tests when commands read via `cmd.Flags().GetX("name")` instead of package vars (e.g. `coverage.go::runCoverage`). `resetXFlags()` won't clear those — pass an explicit `--flag=false` in negative-case tests, or call `cmd.Flags().Set("flag", "false")` before `Execute()`.

## `disco history <id>` surfaces the resource version chain

`history` (alias `versions`) renders every version of a resource oldest→newest —
the OSS read surface for change-over-time (e.g. Azure quota-limit grants). The arg
resolves through `store.ResolveResource` (same exact-id / name / native-id / short-id
lookup as `graph`); the resolved current row's `.ID` (= `root_id`) keys
`store.GetResourceVersions`. Output uses a purpose-built `historyEntry` struct, NOT
`store.ResourceVersion` directly: `store.Resource` has a value-receiver `MarshalJSON`
that would be promoted onto the embedding struct and silently drop the version-only
fields (`verified_at`, `superseded_by`, …) in JSON. New version-chain output paths
must follow the same explicit-struct pattern, not encode `ResourceVersion`. The
`historyEntry` snake_case JSON tags require `cmd/history.go` in the tagliatelle
snake_case exclusion zone (`.golangci.yaml`).

## Silent exit codes for query-absence

When "no result" is a valid query outcome (e.g. `graph path` between unreachable resources), return a sentinel error from the store layer (`store.ErrNoPath`) and let `cmd/root.go` `Execute()` map it to `os.Exit(1)` without printing. Keeps `RunE` testable — `os.Exit` inside `RunE` bypasses in-process test assertions.

## JSON dialect: snake_case + nested attrs/tags

`store.Resource.MarshalJSON` is the single source of truth — emits snake_case keys with nested `attributes` / `tags` objects, not stringified `AttributesJSON` / `TagsJSON`. Matches `policy.Finding` and `coverage.Row` shape. New JSON output paths must encode `[]store.Resource` (or struct embedding it) directly; do not reach for raw field access. Empty / missing / malformed `attributes`/`tags` always render as `{}` (never absent); optional fields render as `null` (never omitted).

`disco list -o json` initialises the result slice as `[]store.Resource{}` not `nil` so a zero-row query emits `[]` instead of `null` — fix for F6. Mirror the pattern in any new top-level array command.

`disco scans -o json` / `disco scans show -o json` use `store.Scan.MarshalJSON` (F5 fix): snake_case keys, RFC3339 timestamps, parsed `providers` / `scope` / `meta`, no PascalCase or `*JSON` SQLite-column leak. `disco summary.as_of` is normalised at population time via `store.ToRFC3339`.

`coverage --resolvers -o json` / `coverage --missing-resolvers -o json` honour the `-o json` flag (F8 fix); previously they always emitted TSV.

## One error message, not two: `structuredErrorEmitted`

`maybeStructuredError` (`cmd/helpers.go`) writes a JSON `{"error":"..."}` envelope to stdout when the caller's `-o` is `json`/`jsonl`, AND sets the package-level `structuredErrorEmitted` flag. `cmd/root.go::Execute` reads the flag and skips the duplicate plaintext stderr print so a `disco ... -o json` failure produces ONE message, not two — fix for F25 / F30.

## `PersistentFlags` on parent cobra cmd inherits to subcommands

`scansCmd.PersistentFlags().StringVarP(&scansOutputFmt, "output", "o", ...)` makes `-o` available on both `disco scans` and `disco scans show` with one declaration. Use for shared flags on multi-subcommand verbs (`scans`, future `snapshot`, etc.); plain `Flags()` would require duplicate plumbing.

## `--scan-id` + `latest` shorthand via `resolveScanID`

`list`, `summary`, `tag-coverage`, and `scans show` all accept `--scan-id <id|latest>`. `latest` resolves via `resolveScanID(db, raw)` (`cmd/helpers.go`) to the most-recent scan whose `resource_count > 0` — a re-verify run that touched no new rows otherwise silently zero-rows the documented drift workflow (F3 fix). Falls back to the most-recent scan when none qualify with a one-line stderr note. Literal IDs round-trip after a `GetScan` presence check; unknown IDs return `scan %q not found`. Plumbed onto `ResourceFilter.DiscoveredBy`; `scan --resume <id|latest>` uses the same shorthand convention.

`ListScans` ORDER BY tie-breaks `started_at DESC` with `rowid DESC` because `datetime('now')` has 1s resolution — two scans created within the same second otherwise ordered by SQLite implementation default and `latest` could resolve to the older one.

## `--scan-as discovered|verified|any` reconciles `scans.ResourceCount` ↔ `list --scan-id`

`scans.resource_count` counts every row a scan touched (insert OR re-verify). `list --scan-id <id>` previously filtered on `discovered_by` only, so a scan that re-verified pre-existing rows reported `RESOURCES: 2045` in `scans` but yielded 0 from `list --scan-id`. F3 fix:

- `ResourceFilter.ScanAs` selects which scan-FK column matches: `discovered` → `discovered_by = ?`, `verified` → `verified_by = ?`, `any` (default, empty) → either column matches.
- `list --scan-as <value>` exposes the choice. Default `any` matches the persona expectation that the named scan returns rows it touched. Flag name was originally `--scan-role`; renamed to `--scan-as` to avoid IAM-role cognitive collision and read naturally inline (`--scan-id latest --scan-as discovered`).
- `list --id <resource-id>` is a primary-key short-circuit on `ResourceFilter.ID` (`WHERE id = ?`); pairs with the F12 partial-ID lookup planned for WS7.

## seedTestDB ships with 2 baseline rows

`seedTestDB` (`list_test.go`) seeds one `aws:ec2:instance` + one `aws:s3:bucket` plus the scan record. Tests adding more rows must factor those two into expected totals (e.g. `summary.total`, `tag-coverage.total`). Don't try to delete them — every other cmd test already depends on them.

## `disco graph complete --orphans-only` filters to disconnected nodes

`graph complete` honours a new `--orphans-only` flag (F17): post-pass keeps only nodes whose ID appears in neither `from_id` nor `to_id` of any returned edge. Surfaces dangling EBS volumes, key-pairs no instance uses, IAM principals with no group/policy, etc. — the forensic / hygiene targets the IR persona was after. Implementation lives in `cmd/graph.go::filterOrphans`; renderers stay unchanged because the filter only drops nodes, never reshapes `GraphResult`.

## Atomic file writes: temp + rename

When a producer writes a single output file consumed downstream by a verifier (e.g. `disco snapshot` → `disco verify`), write to `<path>.tmp` first and `os.Rename` to the final name only on full success. A producer crash mid-write leaves no file at `<path>` — receivers never see partial output. Cleanup the tmp on failure via `defer os.Remove`. Precedent: `internal/snapshot.WriteArchive`.

## `disco verify` says `OK (unsigned …)` by default

`verify`'s success line is `OK (unsigned — manifest not authenticated): ...` for archives without a detached signature, `OK (signed — manifest authenticated via ed25519): ...` when both `--signature` and `--pubkey` are supplied and validate. The wording change deliberately rules out the bare-`OK` interpretation that would mislead a CI step into treating internal-consistency as provenance. `verify` also emits `WARN: tool_version=dev — ...` on stderr when the manifest's `tool_version=="dev"`.

Friendly-error wrapping lives in `friendlyArchiveErr(err)` (cmd/verify.go): collapses raw xz/gzip decoder messages into `verify failed: archive corrupt or truncated`. The original is preserved when `--verbose`. Format-detection errors are intentionally returned BEFORE the friendly wrap so unsupported extensions surface clearly.

## `disco snapshot --signing-payload <file>` is the OSS signing primitive

`internal/snapshot.CanonicalManifestBytes(m)` returns deterministic JCS-style bytes (`json.Marshal(m)` — struct field declaration order, no whitespace). Sign externally (`openssl pkeyutl -sign -inkey priv.pem -rawin -in payload -out sig`, `minisign`, `ssh-keygen -Y sign`, cosign blob-attest) and ship the detached signature alongside the archive. `disco verify --signature <sig> --pubkey <key>` re-derives the canonical bytes from the embedded `manifest.json` and validates with `crypto/ed25519` — stdlib only, no x/crypto dep.

`LoadEd25519PublicKey` accepts PEM-wrapped PKIX SubjectPublicKeyInfo (the format `openssl pkey -pubout` produces) or a raw 32-byte binary key. OpenSSH `ssh-ed25519 AAAAC3...` text is intentionally out of scope — convert with `ssh-keygen -e -m PKCS8` first.

Cosign/Sigstore-witnessed signing (Rekor inclusion proofs) is a future follow-up; the existing plumbing is enough to close the unsigned-manifest forgery gap reported in focus-group/SUMMARY.md F1.

## `disco snapshot <output-file>` writes a single archive

Output is one file — `.zip`, `.tar.gz` (`.tgz`), or `.tar.xz` (`.txz`) — extension drives format. `internal/snapshot.DetectFormat` rejects unknown extensions with a clear error listing supported shapes. `cmd/snapshot.go` opens the source DB via `store.OpenReadOnly`, issues `VACUUM INTO '<out>.db.tmp'` to a sibling temp file, hashes it, packages disco.db + manifest.json into the archive via `snapshot.WriteArchive`, then `os.Rename` for atomicity. `--db-readonly` is allowed (the global flag scopes the source, not the output). `manifest.db_sha256` hashes the inner DB (not the archive) so receivers spot-check the same value across formats. `internal/snapshot` package houses the manifest format (`disco-snapshot/v1`) and the per-format archive readers; `disco verify` decodes via the same package without extracting to a temp dir. Signed-manifest layer (cosign/Sigstore) is a deferred future follow-up.

## `disco check` opens DB read-only by default

`check` is logically a read; opening writable flips the SQLite WAL header and silently mutates `disco.db`, breaking any subsequent `disco verify` against a snapshot of the same DB. So `check` uses `openDB()` (always RO) unless `--persist` is set: `needsWrite := checkPersist` (cmd/check.go) flips the open to `openWriteDB()` after refusing `--db-readonly` up-front, and the persist body writes the run + findings inline.

## AWS account_id resolution + emulator override

`internal/providers/aws/aws_config.go::loadAccounts` resolves the `account.ID` recorded on `resources.account_id` via three precedences:
1. Config-file `aws.accounts[].id` (explicit).
2. `--role-arn` override → `sts:GetCallerIdentity` on the assumed creds.
3. Auto-detect → `sts:GetCallerIdentity` on the default chain.

`DISCO_CLOUD_ACCOUNT_ID` is an **emulator-only** override that short-circuits the STS lookup. **Honored only when `AWS_ENDPOINT_URL` is also set** — the AWS SDK's canonical "talking to a non-AWS endpoint" signal. Prod scanners never set `AWS_ENDPOINT_URL`, so the env is inert outside emulator mode. Emulators (e.g. LocalStack) return a sentinel `"000000000000"` from `sts:GetCallerIdentity` that would otherwise overwrite the configured account id on every emulator-backed scan.

The gate function `emulatorAccountIDOverride()` is the single read site; both the `--role-arn` branch and the auto-detect branch call it before STS. `TestEmulatorAccountIDOverride` in `aws_config_test.go` pins the prod-safety assertion (env value ignored when `AWS_ENDPOINT_URL` is unset).

## `disco check` defaults to customer-managed (F24); --include-managed opts in

`check` previously evaluated every row in the DB (including AWS-managed IAM policies, Azure built-in role definitions, GCP foreign-project stubs) — every BYO Rego author had to defensively `not input.managed_by_provider` or get noisy findings against resources they cannot remediate. Now `check` mirrors `list` / `summary`: customer-only by default, opt in via `--include-managed`. The flag is wired straight onto `ResourceFilter.IncludeManaged`.

## Findings gate the exit code by default; `--exit-zero` overrides

When findings are reported and `--exit-zero` is not set, `RunE` returns the package-level `errFindingsReported` sentinel; the deferred `maybeStructuredError` wrapper checks `errors.Is` and skips emitting `{"error":"N finding(s)"}` to stdout. The findings array IS the payload, the exit code IS the gate — strict consumers (`json.load`, `jq -e`, `go json.Decoder`) parse the stdout in one pass. Stderr keeps the human-readable `N finding(s)` count line.

The default flipped from "opt-in via `--exit-nonzero`" to "opt-out via `--exit-zero`" so CI pipelines that wire `disco check` directly into a job step gate on findings without an extra flag — mirrors `tag-coverage --min-coverage` / `--exit-zero` (`cmd/tag_coverage.go`). Sentinel name kept (`errFindingsReported`) for diff continuity; only the trigger condition flipped (`!checkExitZero` vs the old `checkExitNonZero`). Tests that exercise rendering paths must pass `--exit-zero` to keep `cmd.Execute() == nil` when fixtures produce findings.

## SARIF rule polish: descriptions, defaultConfiguration, partialFingerprints, taxonomies

`cmd/check_sarif.go` (F11) now populates:
- `rules[].shortDescription` / `fullDescription` (mirror the message; richer text can land if the engine ever surfaces a separate longer-form field)
- `rules[].defaultConfiguration.level` mapped from `severity` via `severityToLevel`
- `rules[].properties.tags` flattened as `["waf_pillar:security", "soc2:CC6.1", ...]`
- `results[].partialFingerprints["disco/v1"] = sha256(rule_id+":"+resource_id)[:16]` so GitHub code-scanning de-dupes across runs
- `runs[0].taxonomies[]` — one taxonomy per non-empty `tags.<key>` listed in `taxonomyKeys` (`waf_pillar`, `soc2`, `iso27001`, `pci_dss`, `nist_800_53`, `waf_qid`); each taxon's ID is the unique tag value, sorted for byte-stable output. Empty keys are skipped, so a rule that emits only `waf_pillar` + `waf_qid` produces a SARIF doc with two taxonomy entries; a BYO rule emitting `soc2` adds a third without code change.

Bundled `aws-waf` rules (F10) ship a deliberately minimal `tags: { waf_pillar, waf_qid }` — the pack is the wiring sample, not a curated framework-mapped pack. The `soc2` / `iso27001` / `pci_dss` / `nist_800_53` keys are kept reserved in `taxonomyKeys` so future framework packs (CIS-AWS-Foundations, NIST 800-53, PCI-DSS, ISO 27001) and BYO Rego authors can populate them and get SARIF taxonomies + `--tag soc2=CC6.1` filtering for free. Don't fold control-catalogue mappings into the sample rules — that's a curated pack's job.

The unprefixed `pillar` key is intentionally reserved for a future cross-framework grouping (e.g. NIST CSF Identify/Protect/Detect/Respond/Recover, CIS controls categories) — using it for AWS WAF pillars only would collide. Frame-specific keys (`waf_pillar`, future `csf_function`, `cis_category`) are the convention.

## Rego authors must check scanner wrapping for attrs path

Some scanners wrap the SDK response under a key (CloudTrail: `{"Trail": ..., "Status": ...}`; ELBv2 LB: `{"lb": ..., "type": ...}`; EventBridge rule: `{"rule": ..., "Targets": [...]}`; Lambda function: SDK type embedded with `Code` sibling). Rego rules reading these resources must match the wrapped path: `input.attributes.Trail.IsMultiRegionTrail`, not `input.attributes.IsMultiRegionTrail`. The wrapping is documented in `internal/providers/aws/CLAUDE.md` — grep for the resource type before authoring a rule. Wrong path silently matches nothing.

## `--packs <name,...>` loads bundled OSS Rego packs

`disco check --packs aws-waf` loads `internal/policy/aws-waf/*.rego` via `//go:embed`. Pack names follow `<provider>-<framework>` convention. `policy.LoadPacks([]string)` walks the embed.FS, returns `map[name]source`; `policy.NewEngine(ctx, paths, modules)` accepts both `--rules <dir>` paths AND module map in one call so `--rules ./mine --packs aws-waf` composes. Adding a new pack = one `//go:embed` line + one entry to `AvailablePacks()`. Bare `disco check` errors with "--rules or --packs is required (e.g. --packs aws-waf)" — never default to one or the other silently.

## Set `Args: cobra.NoArgs` on flag-only subcommands

Cobra's default Args validator silently accepts arbitrary positional tokens. `disco list --discovered-since 2025-05-01 12:01:01` parses `--discovered-since=2025-05-01` and treats `12:01:01` as a positional, ignored without error. Read commands with no positional arity (`list`, `summary`, `scans`) MUST set `Args: cobra.NoArgs`. Use `cobra.ExactArgs(N)` / `MaximumNArgs(N)` / `MinimumNArgs(N)` per shape — never leave Args unset on a flag-only verb.

## Time filters: `{discovered, created} × {since, before}` — half-open `[since, before)`

`list`, `summary`, `tag-coverage` accept the column-anchored time-filter pair `--<col>-since` / `--<col>-before`:

- `--discovered-since <ts>` → `ResourceFilter.DiscoveredSince` → SQL `discovered_at >= ?`. Inclusive lower bound on first-seen-by-disco.
- `--discovered-before <ts>` → `ResourceFilter.DiscoveredBefore` → SQL `discovered_at < ?`. Strict upper bound; pairs with `--discovered-since` for half-open `[since, before)` intervals; also serves as the standalone "stale" hygiene query.
- `--created-since` / `--created-before` mirror the pair on the resource's intrinsic `created_at` column (lifted from the SDK at scan time). Rows with NULL `created_at` are excluded from both filters because `NULL < X` is unknown in SQL — not every scanner lifts the SDK timestamp yet (see EBS volume precedent in commit 8e61c52).

`disco findings list` carries an analogous `--run-since` flag that filters check-run `started_at` (different table, different anchor — not a `ResourceFilter` field).

All take `<RFC3339|YYYY-MM-DD>` via `parseTimeFlag` (`cmd/helpers.go`). Bare date auto-extends to `T00:00:00Z`; non-UTC zones normalise to UTC. Discovered-axis filters are pinned to `discovered_at` (immutable first-seen), NOT `verified_at` — re-scans don't re-stamp it. Means `--scan-id latest --discovered-since X` legitimately returns 0 when the latest scan only re-verified pre-existing rows.

Backed by `singleSetString` (`cmd/helpers.go`) — pflag.Value that errors on second `Set()` so repeated `--discovered-since A --discovered-since B` rejects rather than last-wins-silently. Test reset helpers must call `<flag>.reset()` on the value, not `<flag> = ""` (compile error: untyped string into struct).

New column-anchored time filters follow the same `{*-since, *-before}` shape — `{since, until}` is no longer used (the inclusive `<= X` upper bound collapses to half-open `[since, before)` at any practical granularity).

## `tag-coverage --case-insensitive` folds case before tallying

`tag-coverage` exposes `--case-insensitive` to fold tag keys to lower-case during aggregation (so `environment` and `Environment` collapse into one row instead of producing two separate scorecards — F13 fix). Implementation tracks the first observed casing in an `origKey` map so table output preserves operator-friendly capitalisation. A future "secrets-in-tags" pass will own the suspicious-shape detection — out of scope for this command, which is a coverage-rate report, not a security scanner.

## `summary --by-account` rollup

`summary` adds a `BY ACCOUNT` section (table) and a `by_account: [{account_id, account_name, count}]` JSON field — F21 fix for the CIO portfolio rollup. The CSV `dimension` column carries `account` rows alongside `provider` / `region` / `type`. Account name renders parenthetically when set (`123456789012 (prod)`); empty otherwise. Counts roll up via `acctCounts` in `buildSummary`.

## `--exclude-types` plumbs through `ResourceFilter.ExcludeTypes`

`list`, `summary`, and `tag-coverage` all expose `--exclude-types` (StringSlice → comma-separated). All three forward to `store.ResourceFilter.ExcludeTypes`, which emits a SQL `type NOT IN (...)` clause via `squirrel.NotEq`. Filter is applied at the SQL layer, so denominators (tag-coverage rate, summary `total`) drop along with the displayed rows — not just display masking. Compatible with `--type` (include); both clauses AND together. Default-hide of noisy types (e.g. `aws:logs:log-stream`) deliberately rejected — security work cares about log-stream coverage; the flag is the user-driven escape hatch.

## DOT `dir=back` requires endpoint swap, not just attribute

Graphviz `dir=back` only re-renders the arrowhead — rank still flows tail→head. To flip layout direction (e.g. force `attached-to` parent left of source under `rankdir=LR`), `renderGraphDot` swaps `FromID`/`ToID` for any edge whose preset carries `dir=back`. Adding `dir=back` to a theme preset alone is a no-op for rank; both pieces are needed.

## Output-format parity: `table | markdown | csv | json` floor

Every reportable subcommand (`list`, `summary`, `scans` + `scans show`, `tag-coverage`, `diff`, `graph` + `path`/`blast`/`complete`, `check`, `coverage` + `services`/`regions`/`resolvers`, `findings list`/`runs`) accepts the four canonical output formats as a floor. Per-command extras (`jsonl`, `sarif`, `dot`, `mermaid`) layer on top. Markdown rendering goes through the shared `renderMarkdownTable(w, headers, rows)` helper in `cmd/helpers.go` for byte-stable output. Markdown case label is `markdown`; `md` is accepted as a short alias. Operational commands (`scan`, `snapshot`, `verify`, `config init`) have no `-o` flag — they perform actions, not reports. Help text lists every supported format the command accepts in canonical order then extras.
