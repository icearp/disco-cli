# CLAUDE.md — `internal/providers/`

Cross-provider conventions. AWS-specific guidance: see `aws/CLAUDE.md`.

## Resolver-side resource upsert (synthetic stubs)

Resolvers needing to insert resources (e.g. cross-tenant stubs for R5) get scanID via `<row>.DiscoveredBy` from any resource they list — resolver signature carries no scanID. Two-phase pattern: collect pending edges + distinct stub keys in pass 1; `UpsertResources(stubs)` in pass 2; emit `UpsertRelationship` in pass 3. Pre-upsert before edge emit or FK on `relationships.to_id` blows up. Precedent: `resolveIAMRoleCrossAccountTrust` (aws), `resolveAuthorizationRelationships` (azure), `resolveIAMPolicyRelationships` (gcp).

## Persist API contract

Provider scanners call `store.UpsertResources()`, `store.UpsertRelationship()`, `store.BatchAddToHierarchyClosure()` to persist. Errors from all three must propagate — never silence with `_ =`.

## Provider-managed resources

Resources owned by the cloud (Azure built-in policy/role definitions, AWS-managed IAM policies, AWS-owned prefix lists, IAM service-linked roles, AuditMgr Standard frameworks/controls) set `store.Resource.ManagedByProvider=true`. Hidden from `disco list` / `disco graph` by default; `--include-managed` opts in. In `graph`, managed nodes are terminal — appear when reached via direct edge but BFS does not expand through them. Detection lives at scan time, reads typed SDK field (e.g. `OwnerId == "AWS"`, `PolicyType == BuiltIn`, `RoleType == "BuiltInRole"`, role path `/aws-service-role/`). Where the SDK exposes a scope/type filter (IAM `PolicyScope`, AuditMgr `FrameworkType`/`ControlType`), loop both values in a single scanner and flag the managed pass — precedent: `scanIAMPolicies`, `scanAuditManagerFrameworks`, `scanAuditManagerControls`.

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

**Add new provider** (three steps):
1. Create `internal/providers/<name>/` implementing `Scanner`
2. Call `providers.Register(&MyScanner{})` in package `init()`
3. Add `_ "codeberg.org/icearp/disco/internal/providers/<name>"` to `cmd/providers.go`

## Provider file naming

Scanners in `<service>_scanners.go`, resolvers in `<service>_resolvers.go`. AWS scanners + resolvers self-register via `registerService` / `registerResolver` (see `aws_services.go`) called from each file's `init()` — no manual wire-up in `aws.go`.

## Shared utilities (`internal/util`)

`util.MustJSON(v any) string`, `util.Sv(p *string) string`, `util.AllResources` (= `math.MaxUint32`, used as `Limit` in `ListResources` to fetch all rows). Each provider keeps unexported one-liner wrappers (`mustJSON`, `sv`) delegating to `util` — call sites clean, logic centralized.

## New `Type*` constant → append to `KnownTypes()`

New `Type*` const in `<provider>_types.go` must append to `KnownTypes()` slice same file. Coverage command + types gap-analysis use it. AWS + Azure rely on review to catch omissions; GCP has an AST-based test (`types_test.go::TestKnownTypes_NoOmissions`) that parses the const block and fails on drift — port the same test to AWS/Azure if drift bugs surface.

## Embedding child data in parent attributes

Child resource (e.g. EventBridge rule targets) no independent lifecycle, meaningful only via parent — fetch child at scan time, embed under key in parent's `AttributesJSON` (e.g. `{"Rule": ..., "Targets": [...]}`). Resolvers read embedded data, no extra API calls.

**Warning — wrapping breaks existing resolvers.** Switch scanner from raw SDK struct to wrapped (e.g. add `Targets` alongside `TargetGroup`) silent drops every edge from resolvers still reading old top-level shape — JSON unmarshal into old struct succeeds with zero values, no error. Grep resolvers for type before wrapping, update attribute structs to nest under new key.

## Non-resource config fetches → sidecar on `account`

Do NOT invent resource types for cloud concepts that aren't real resources (`aws:s3:bucket-encryption` rejected — `GetBucketEncryption` returns config *of* existing bucket, not own ARN'd resource). Do NOT wrap primary resource's raw SDK attrs JSON to co-locate config — `attributes` must equal native SDK response verbatim.

Pattern for edges needing non-resource config: stash on `account` struct during scan (mutex-protected if concurrent), consume in resolver. Edge persists; raw config ephemeral per scan. Precedent: `account.s3BucketEncryption` populated by `scanS3BucketEncryptions`, consumed by `resolveS3BucketEncryptionRelationships`. If config must later be queryable, add generic `resource_configs(resource_id, config_type, payload_json)` sidecar table — do NOT retrofit via synthesized resource type or wrapped attrs.

## Synthetic-resource removal audit

Before deleting a `Type*` constant, grep for downstream consumers: (1) resolvers reading the type (`grep -rn TypeX`), (2) hierarchy closure parents (`BatchAddToHierarchyClosure` callers), (3) rules / sidecar reads. Zero hits beyond the scanner's own upsert = safe to remove. Edit sites: type constant in `<provider>_types.go`, `KnownTypes()` slice, scanner upsert call, test fixtures, `ROADMAP.md` historical entry. Existing DB rows left as cruft — no migration unless edge-bearing. Precedent: `aws:shield:subscription` removed (zero consumers); contrast `aws:macie:session` kept (Security Hub product-sub resolver + Macie children hierarchy parent).

## Testing

Test files exist for: `internal/store/`, `internal/util/`, all three provider packages.

### Writing tests for new services

Every new `<service>_resolvers.go` must have matching `<service>_resolvers_test.go`. Pattern:

1. Call `newTestStore(t)` — opens temp-file SQLite DB, inserts required test scan record.
2. Call `upsertTestResource(t, st, provider, accountID, rtype, nativeID, region, attrsJSON)` to insert resources. **Pass region** if resolver uses `sv(r.Region)` to build ARNs — omit makes computed relationship IDs point to phantom resources, FK error no obvious diagnosis. Helper does **not** set `Name`; for resolvers building name-keyed index (e.g. KeyPair by `(region, Name)`), bypass + call `st.UpsertResource(&Resource{..., Name: &name})` direct.
3. Call resolver function direct (tests same package, e.g. `package aws`).
4. Assert via `st.RelationshipsFrom(id)`.

Always add "no attrs / empty case" test alongside happy-path — guards nil-pointer panics on missing JSON fields.

### SDK-typed attrs builders over hand-rolled JSON

Resolver tests should construct attrs via `json.Marshal` of the real SDK struct, not hand-rolled JSON literals. Azure `arm*` types use custom `MarshalJSON` with `populate("camelCaseKey", ...)` — JSON shape is invisible on struct tags, so a string literal that drifts from the SDK silently passes. Helper: `marshalAttrs(t, v)` in `internal/providers/azure/attrs_testhelper_test.go`. AWS equivalent: `wrapped_attrs_testhelper_test.go`.

### Registration tests

`<provider>/registration_test.go` holds `expectedAWSServices` / `expectedAzureServices` / `expectedGCPServices` — authoritative list of registered service names. **Update when adding new service scanner.** Test fails if service registered but not listed, or listed but not registered.

### Test-seam pattern for helpers that build their own clients

Production helpers that internally construct API clients (via ADC / `clientOptions` / `azidentity.DefaultAzureCredential`) can't be aimed at httptest fakes or SDK fake transports. Split into thin outer wrapper + inner `*In` / `*WithClient` core that takes the pre-resolved values (region list, client, customer ID). Tests call the inner core directly; production code uses the outer wrapper. Precedent: `gcpRegionFanoutScan` / `gcpRegionFanoutScanIn` in `gcp/gcp.go`; Azure `scanX` / `scanXWithClient` in `compute_disks_scanners.go`.
