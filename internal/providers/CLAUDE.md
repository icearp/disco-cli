# CLAUDE.md — `internal/providers/`

Cross-provider conventions. AWS-specific guidance: see `aws/CLAUDE.md`.

## Splitting high-complexity resolvers/scanners

When a resolver/scanner trips gocognit (>80) and the shape is "for each resource → emit edges per N target families", extract one helper per family and bundle the per-type id sets in a single `*TargetSets` struct rather than passing many maps. Main loop only dispatches; mutation lives in each helper. Precedents: `openSearchTargetSets` (aws/opensearch_resolvers.go), `diagTargetIndexes` (azure/monitor_resolvers.go).

## Resolver-side resource upsert (synthetic stubs)

Resolvers needing to insert resources (e.g. cross-tenant stubs for R5) get scanID via `<row>.DiscoveredBy` from any resource they list — resolver signature carries no scanID. Two-phase pattern: collect pending edges + distinct stub keys in pass 1; `UpsertResources(stubs)` in pass 2; emit `UpsertRelationship` in pass 3. Pre-upsert before edge emit or FK on `relationships.to_id` blows up. Precedent: `resolveIAMRoleCrossAccountTrust` (aws), `resolveAuthorizationRelationships` (azure), `resolveIAMPolicyRelationships` (gcp).

## Persist API contract

Provider scanners call `store.UpsertResources()`, `store.UpsertRelationship()`, `store.RecordHierarchyBatch()` to persist. Errors from all three must propagate — never silence with `_ =`.

## Provider-managed resources

**Definition.** A resource is provider-managed when both hold: (1) it materialises automatically — created by the cloud at account / tenant / project creation, on service enablement, or as a default-region rollout, with no explicit user API call; AND (2) the user cannot delete it directly (Delete API rejects, or AWS/Azure/GCP recreates on next reconcile). One condition without the other is not enough — user-created defaults (e.g. a default VPC the user kept) fail (1); deletable system rows (rare) fail (2). When in doubt, attempt delete in a scratch account; if the API rejects with a "managed by AWS / built-in / system" error, flag it.

Resources owned by the cloud (Azure built-in policy/role definitions, AWS-managed IAM policies, AWS-owned prefix lists, IAM service-linked roles, AuditMgr Standard frameworks/controls) set `store.Resource.ManagedByProvider=true`. Hidden from `disco list` / `disco graph` by default; `--include-managed` opts in. In `graph`, managed nodes are terminal — appear when reached via direct edge but BFS does not expand through them. Detection lives at scan time, reads typed SDK field (e.g. `OwnerId == "AWS"`, `PolicyType == BuiltIn`, `RoleType == "BuiltInRole"`, role path `/aws-service-role/`). Where the SDK exposes a scope/type filter (IAM `PolicyScope`, AuditMgr `FrameworkType`/`ControlType`), loop both values in a single scanner and flag the managed pass — precedent: `scanIAMPolicies`, `scanAuditManagerFrameworks`, `scanAuditManagerControls`.

## Don't mix `g.Wait()` with same-statement counter reads

Go evaluates return-list expressions left-to-right. `return total, inserted, g.Wait()` reads `total`/`inserted` *before* `Wait()` blocks, so any counters mutated inside the goroutines come back as their pre-fan-out value. Same hazard for `return int(t.Load()), int(n.Load()), g.Wait()` with `atomic.Int64`. Always bind `err := g.Wait()` first, then return the counters. Precedents fixed: `aws/ec2_scanners.go::runScanners`, `azure/sql_scanners.go::scanSQL` (phase-3 return), `azure/sql_managed_scanners.go::scanSQLManaged` (phase-3 return).

## Per-(acct, region) singleton config rows are provider-managed

Account-level / region-level singleton config types (e.g. data-lake-settings, account-audit-configuration, encryption-configuration, replication-configuration, resolver-config) represent cloud state, not user-created resources. Set `ManagedByProvider: true` at upsert time so they hide from default `disco list` / `disco graph`. Precedents under `internal/providers/aws/`: ECR replication-configuration, ECR registry-scanning-configuration, IoT encryption-configuration, IoT account-audit-configuration, LakeFormation data-lake-settings, Route53Resolver resolver-config.

## Errors never abort scan

Provider scanners must NOT propagate per-service / per-region / per-resolver errors. Instead:

- On failure: `st.ReportError(store.ScanError{Provider, Service, Scope, Message})` then continue.
- `ReportService(name, total, inserted, errCount)` — `errCount>0` surfaces a `(with errors)` suffix on the per-service progress line.
- `Scan()` returns `nil` even when individual services failed; load-credentials / load-accounts failures also report-and-return-nil.
- `cmd/scan.go` collects errors via `OnError` and renders one grouped block at end. Inline `FAILED:` lines no longer printed.

Replace `errgroup.WithContext` with plain `sync.WaitGroup` for per-service fan-out — errgroup cancels siblings on first error, which we explicitly do not want. Precedent: `aws/aws.go` `scanRegion` + `scanAccount` phase 1a.

## Provider registry (`registry.go`)

Each provider self-registers via `init()` calling `providers.Register(s Scanner)`. `Scanner` interface needs two methods:

```go
Name() string
Scan(ctx context.Context, st *store.Store, scanID string) error
```

`providers.All()` returns registered scanners sorted by name. `providers.Get(name)` for validation. `providers.Names()` for error messages.

Optional capability interfaces a Scanner may implement: `ServiceFilterer` (`--services`), `RegionOverrider` (`--regions`), `ProfileOverrider` (`--profile`), `GlobalsSkipper` (`--skip-globals`), `RoleOverrider` (`SetRoleOverride(roleARN, externalID)` → `--role-arn`/`--external-id`; pins the scan to one AssumeRole target, ignoring config-file accounts; external-id never lands in `scans.scope` JSON).

**Add new provider** (three steps):
1. Create `internal/providers/<name>/` implementing `Scanner`
2. Call `providers.Register(&MyScanner{})` in package `init()`
3. Add `internal/providers/all/<name>.go` — `//go:build !slim || <name>`, `package all`, blank-importing the provider package. `cmd` imports only `internal/providers/all`, so this tagged file is the sole wiring point and `cmd` never names a provider.

## Build-tag opt-in (`slim`)

Default `go build` compiles every provider. `go build -tags 'slim aws'` compiles only the named provider(s) — excluded providers' SDKs are never linked (smaller binary, for provider-specific containers). The opt-in lives in `internal/providers/all/`: one `<name>.go` per provider, gated `//go:build !slim || <name>` (references only `slim` plus its own tag, never siblings, so providers stay decoupled). `all/all.go` is an untagged, import-less package stub that keeps `all` importable when every provider is tagged out (`-tags slim` alone → no providers). `-tags 'slim aws gcp'` selects a subset. cmd must never import a provider package directly — route provider-specific cmd needs through a registry interface (precedent: `coverage.ResolverAuditor` for `disco coverage resolvers`) so slim builds degrade gracefully.

## Declaring redaction rules

Per-provider `redact.go` (e.g. `internal/providers/aws/redact.go`) declares per-type rules in an `init()` block: `redact.Register(redact.TypeRules{Type: TypeFoo, Attributes: []redact.Rule{{Path: "Bar.Baz", Mode: redact.RedactScalar}}})`. Path syntax: dotted literals; `*` map-key wildcard; `[*]` array wildcard. Modes: `RedactScalar` (leaf only) or `RedactSubtree` (every scalar descendant).

Adding a new resource type whose SDK response carries credentials, tokens, init-script payload, plaintext env vars, or connection strings: register a rule alongside `registerService`. Rules are NOT inferred — without one, the field ships unredacted. Pointer-shape fields (ARNs, KeyVault reference URIs) are preserved by **omission** — don't add a rule then add a shape-allowlist escape; just don't add the rule.

Per-provider `redact_test.go` constructs sample SDK responses via `json.Marshal` of real SDK types and asserts the sensitive field comes back `[REDACTED]`. SDK field renames break the test on `go mod tidy` — cheap drift catch.

## Provider file naming

Scanners in `<service>_scanners.go`, resolvers in `<service>_resolvers.go`. AWS scanners + resolvers self-register via `registerService` / `registerResolver` (see `aws_services.go`) called from each file's `init()` — no manual wire-up in `aws.go`.

## Shared utilities (`internal/util`)

`util.MustJSON(v any) string`, `util.Sv(p *string) string`, `util.AllResources` (= `math.MaxUint32`, used as `Limit` in `ListResources` to fetch all rows). Each provider keeps unexported one-liner wrappers (`mustJSON`, `sv`) delegating to `util` — call sites clean, logic centralized.

## Scanner `emits []coverage.TypeDecl` is coverage truth source

Every `registerService` / `registerOrgService` / `registerTenantService` call must declare the disco types it upserts via `emits []coverage.TypeDecl{{Service, DiscoType, Synthetic, Leaf}}`. Coverage matrix (`disco coverage`) reads this. `KnownTypes()` no longer exists — emits + alias map are the source of truth. Disco-only types (no upstream registry entry — IAM policy synth, foreign-project / foreign-account stubs, KMS grants, Macie session, GuardDuty / Detective / Inspector members, Entra identities) get `Synthetic: true`. Terminal types whose scanner upserts rows but for which no resolver will ever wire outbound edges (account/region singletons, third-party catalogue mirrors, config-only policy bodies) get `Leaf: true` so `disco coverage resolvers --missing` filters them out. Drop the flag in the same commit that ships an outbound resolver — `TestLeafTypesNotResolverSources` (in `internal/providers/aws/coverage_leaves_test.go`) catches the contradiction. Non-serviceEntry upsert sites (hierarchy scanners, ec2_*/compute_*/sql_* child files, resolver-side stubs) declare via `registerExtraEmits(coverage.TypeDecl{...})` from the same file's init(). Per-provider `CollectEmits()` aggregates and dedupes for the `coverage.Provider` impl.

## Embedding child data in parent attributes

Child resource (e.g. EventBridge rule targets) no independent lifecycle, meaningful only via parent — fetch child at scan time, embed under key in parent's `AttributesJSON` (e.g. `{"Rule": ..., "Targets": [...]}`). Resolvers read embedded data, no extra API calls.

**Warning — wrapping breaks existing resolvers.** Switch scanner from raw SDK struct to wrapped (e.g. add `Targets` alongside `TargetGroup`) silent drops every edge from resolvers still reading old top-level shape — JSON unmarshal into old struct succeeds with zero values, no error. Grep resolvers for type before wrapping, update attribute structs to nest under new key.

## Embedded child → row: when to promote

Embedded child data gets promoted to its own resource row only when ALL hold: (1) the child is an **edge endpoint** (resolver targets the child as `to_id`, not just walks it to emit edges from parent); (2) per-child state matters operationally (diff/check value — blackhole flips, propagation toggles); (3) cardinality bounded (≲ 100 / parent typical). Otherwise keep embedded — adding rows for CIDR-keyed entries (route-table routes, NACL entries, VPN static routes) trades scan-time + DB size for nothing the resolver couldn't already extract from the parent walk. Promotion uses **composite NativeID** `{parentARN}/<kind>/{childId}` and a new child resource type `aws:<svc>:<parent>-<child>` — never invent a 4-part disco-id format. ResourceID stays 3-part (provider/account/type/native); hierarchy lives in NativeID. Precedent: `aws:ec2:transit-gateway-route` (`{rtbARN}/{cidr}`), `aws:ec2:tgw-rtb-prop` (`{rtbARN}/{attId}`) in `aws/ec2_tgw_scanners.go`.

## Non-resource config fetches → sidecar on `account`

Do NOT invent resource types for cloud concepts that aren't real resources (`aws:s3:bucket-encryption` rejected — `GetBucketEncryption` returns config *of* existing bucket, not own ARN'd resource). Do NOT wrap primary resource's raw SDK attrs JSON to co-locate config — `attributes` must equal native SDK response verbatim.

Pattern for edges needing non-resource config: stash on `account` struct during scan (mutex-protected if concurrent), consume in resolver. Edge persists; raw config ephemeral per scan. Precedent: `account.s3BucketEncryption` populated by `scanS3BucketEncryptions`, consumed by `resolveS3BucketEncryptionRelationships`. If config must later be queryable, add generic `resource_configs(resource_id, config_type, payload_json)` sidecar table — do NOT retrofit via synthesized resource type or wrapped attrs.

## Synthetic-resource removal audit

Before deleting a `Type*` constant, grep for downstream consumers: (1) resolvers reading the type (`grep -rn TypeX`), (2) hierarchy closure parents (`RecordHierarchyBatch` callers), (3) rules / sidecar reads, (4) `emits` decls + alias maps in `coverage.go`. Zero hits beyond the scanner's own upsert = safe to remove. Edit sites: type constant in `<provider>_types.go`, scanner upsert call, scanner's `emits` decl, alias-map entry in `<provider>/coverage.go`, test fixtures, `ROADMAP.md` historical entry. Existing DB rows left as cruft — no migration unless edge-bearing. Precedent: `aws:shield:subscription` removed (zero consumers); contrast `aws:macie:session` kept (Security Hub product-sub resolver + Macie children hierarchy parent).

## Testing

Test files exist for: `store/`, `internal/util/`, all three provider packages.

### Writing tests for new services

Every new `<service>_resolvers.go` must have matching `<service>_resolvers_test.go`. Pattern:

1. Call `newTestStore(t)` — opens temp-file SQLite DB, inserts required test scan record.
2. Call `upsertTestResource(t, st, provider, accountID, rtype, nativeID, region, attrsJSON)` to insert resources. **Pass region** if resolver uses `sv(r.Region)` to build ARNs — omit makes computed relationship IDs point to phantom resources, FK error no obvious diagnosis. Helper does **not** set `Name`; for resolvers building name-keyed index (e.g. KeyPair by `(region, Name)`), bypass + call `st.UpsertResource(&Resource{..., Name: &name})` direct.
3. Call resolver function direct (tests same package, e.g. `package aws`).
4. Assert via `st.RelationshipsFrom(id)`.

Always add "no attrs / empty case" test alongside happy-path — guards nil-pointer panics on missing JSON fields.

When the production scanner emits a `RecordHierarchyBatch` pair that the resolver under test depends on (or relies on for `contains` row coverage post `recordHierarchyTx` unification), seed the same pair in the test before invoking the resolver: `st.RecordHierarchyBatch([][2]string{{childID, parentID}})`. The unified closure writer emits the matching `parent → child contains` row so existing `RelationshipsFrom(parentID)` assertions still pass. Precedent: `backup_resolvers_test.go::TestResolveBackupRelationships`, `guardduty_resolvers_test.go::TestResolveGuardDutyRelationships`.

`newTestStore` (each provider) registers a `t.Cleanup` that fails the test if any reversed `contains` edge leaks (`store.ReversedContainsEdges`). Ensures a future scanner emitting child→parent direction breaks tests immediately rather than silently producing flipped graphs. Pattern generalises: when you ship a store-level invariant query (returns rows that should never exist), wire it into `newTestStore`'s cleanup so every provider test guards it for free.

### SDK-typed attrs builders over hand-rolled JSON

Resolver tests should construct attrs via `json.Marshal` of the real SDK struct, not hand-rolled JSON literals. Azure `arm*` types use custom `MarshalJSON` with `populate("camelCaseKey", ...)` — JSON shape is invisible on struct tags, so a string literal that drifts from the SDK silently passes. Helper: `marshalAttrs(t, v)` in `internal/providers/azure/attrs_testhelper_test.go`. AWS equivalent: `wrapped_attrs_testhelper_test.go`.

### Registration tests

`<provider>/registration_test.go` holds `expectedAWSServices` / `expectedAzureServices` / `expectedGCPServices` — authoritative list of registered service names. **Update when adding new service scanner.** Test fails if service registered but not listed, or listed but not registered.

### Test-seam pattern for helpers that build their own clients

Production helpers that internally construct API clients (via ADC / `clientOptions` / `azidentity.DefaultAzureCredential`) can't be aimed at httptest fakes or SDK fake transports. Split into thin outer wrapper + inner `*In` / `*WithClient` core that takes the pre-resolved values (region list, client, customer ID). Tests call the inner core directly; production code uses the outer wrapper. Precedent: `gcpRegionFanoutScan` / `gcpRegionFanoutScanIn` in `gcp/gcp.go`; Azure `scanX` / `scanXWithClient` in `compute_disks_scanners.go`.

## List ops with required input filters

Many AWS/Azure/GCP List ops reject blanket calls — they require a parent identifier (DescribeUserStackAssociations needs StackName, ListPolicies needs PolicyEngineId, ListIndexes needs VectorBucketName, ListServiceNetworkServiceAssociations needs ServiceNetworkIdentifier). The compile-time signature exposes the field as `*string` so empty input compiles but fails at runtime with `InvalidParameterCombinationException` / `validation error` / `Must provide at least one of: …`. Before writing a List paginator, grep the SDK package for `validators.go` — `ErrParamRequired` entries reveal required inputs. Fan-out per parent enumerated by an upstream sibling scanner phase; pass the parent IDs/ARNs as a slice argument.

## List-only summary scanners block resolver work

Some `List*` ops return only `{Arn, Name, Status}` summaries — DataSync `ListLocations` is canonical: full IAM/S3/EFS/FSx refs live on `DescribeLocation*` per subtype. Resolvers can't synthesize edges from data the scanner never fetched. Either skip the resolver with a note in the scanner header, or enrich the scanner via per-row Describe fan-out before adding the resolver. Precedent for the enrichment pattern: `scanStorageLens` in `aws/s3control_scanners.go`.

## Generic-file layout per provider

All three provider packages share the layout `<provider>_<concern>.go` for generic glue: `scanner` (orchestration, dispatcher caps, util wrappers), `registry` (`registerService` + emits aggregator), `config`, `types`, `errors`, `regions`, `redact`, `coverage`, plus per-provider extras `arn` (AWS) / `armid` (Azure), `tags` (AWS, Azure), `concurrency` (AWS, Azure), `scan_helpers` (Azure, GCP), `hierarchy` (GCP). Test files mirror the production file: `<provider>_<concern>_test.go`. Shared test infrastructure collapses into one `<provider>_testhelpers_test.go`. When adding a new generic concern, follow this layout — don't reintroduce the kitchen-sink `<provider>.go` shape that all three packages just got out of.

## Orphan-Type-constant guard

Each `<provider>_types_test.go` runs `TestEveryTypeConstantIsUsed`: AST-walks `<provider>_types.go` for `Type*` const declarations, walks the rest of the package for `ast.Ident` references, errors on any constant declared but referenced nowhere else. Catches retired-service cruft. Adding a `Type*` const must come paired with at least one consumer (scanner emits decl, resolver edge target, sidecar lookup) or the test fails. The test is the only signal — no build error.
