# disco code structure

Architectural overview of `disco` codebase. Build commands + CGO rule + store internals in `CLAUDE.md` — this doc = map.

## Top-level flow

```
cmd/           →  internal/providers/<name>/  →  internal/store/  →  sqlite (~/.disco/disco.db)
 (cobra CLI)      (scanners + resolvers)         (sqlx + squirrel)
                            ↓                            ↑
                   internal/rules/ (check, eval)  ───────┘
                   cmd/graph, cmd/list, cmd/diff  ───────┘
```

- **`cmd/`** — cobra subcommands. `scan` (parallel provider run), `list` (filter DB), `check` (run rules), `graph` (closure-table traversal), `diff` (compare scans). `cmd/providers.go` holds blank imports registering scanners.
- **`internal/providers/<name>/`** — AWS/Azure/GCP scanners. Two-phase: resources then relationships. Detail below.
- **`internal/store/`** — sqlite layer. Tables: `resources`, `relationships`, `hierarchy_closure`, `scans`. Built on `modernc.org/sqlite` (CGO-free), `sqlx`, `squirrel`. Embedded migrations in `migrations/`.
- **`internal/rules/`** — rule DSL + evaluator. `builtin/` holds packaged rules. `eval.go` runs rules against store, `finding.go` captures matches. `cmd/check` consumes.
- **`internal/util/`** — `MustJSON`, `Sv`, `TimeRFC3339`, `AllResources` constant.

## Relationship kinds (`internal/store/relationships.go`)

| Const | Value | Meaning |
|-------|-------|---------|
| `RelContains`   | `contains`    | parent owns child (org → account, VPC → subnet) |
| `RelAttachedTo` | `attached-to` | resource attached to another (alias → key, SCP → OU) |
| `RelUses`       | `uses`        | runtime dep (secret → KMS key, Lambda → role) |
| `RelRoutesTo`   | `routes-to`   | network routing edge (route table → NAT GW) |
| `RelPeer`       | `peer`        | symmetric relation (VPC peering) |
| `RelAssumes`    | `assumes`     | IAM trust (role → principal) |

`UpsertRelationship(fromID, toID, kind, direction, attrs)` — `direction` = `"directed"` or `"symmetric"`.

## Query / analysis subsystems

- **`cmd/list.go`** — filter via `store.ListResources(ResourceFilter{...})`. Flags: `--provider`, `--type`, `--region`, `--status`, `--tag-key`/`--tag-value`, `--output table|json`.
- **`cmd/graph.go`** — closure-table descendant/ancestor traversal for any resource ID. `--depth N` limits.
- **`cmd/check.go`** — loads builtin rules + any `--rule-file` glob, evaluates via `internal/rules/eval.go`, emits `Finding`s.
- **`cmd/diff.go`** — compare two scan IDs: added/removed/changed resources.

---

# Provider code structure

## Context

Providers make per-service API calls using each cloud's native Go SDK. Provider package implement `Scanner` interface (`internal/providers/registry.go`), write resources via `store.UpsertResources`, write relationships via `store.UpsertRelationship`, populate closure table via `store.BatchAddToHierarchyClosure` (preferred) or `store.AddToHierarchyClosure`. Doc capture actual structure in `aws/`, `azure/`, `gcp/`.

## Package layout

```
internal/providers/<name>/
├── <name>.go                 # Scanner struct, Name(), Scan() — top-level orchestrator
├── <name>_config.go / config.go   # loads accounts/subscriptions/projects, credentials
├── services.go / <name>_services.go  # serviceEntry + registerService + resolverEntry + registerResolver
├── types.go / <name>_types.go       # resource type string constants + KnownTypes()
├── <service>_scanners.go     # one per service: init() registers scanner fn (phase 1)
├── <service>_resolvers.go    # one per service: init() registers resolver fn (phase 2)
├── <service>_resolvers_test.go  # resolver unit tests
├── registration_test.go / aws_registration_test.go  # expected service name list
└── testhelper_test.go / <name>_testhelper_test.go   # newTestStore, upsertTestResource, etc.
```

Per-service split: scanner logic in `<service>_scanners.go`, relationship logic in `<service>_resolvers.go`. Both register into package-level registries via `init()` — no edits to `<name>.go` when adding service.

## Scanner struct and registration

Each provider top-level file registers `Scanner` with shared `providers` registry:

```go
// internal/providers/aws/aws.go
func init() { providers.Register(&Scanner{}) }

type Scanner struct {
    serviceFilter  []string // optional; matches providers.ServiceFilterer
    regionOverride []string // aws only; matches providers.RegionOverrider
    profile        string   // aws only; matches providers.ProfileOverrider
}

func (s *Scanner) Name() string { return "aws" }
func (s *Scanner) Scan(ctx context.Context, st *store.Store, scanID string) error { ... }
```

Optional interfaces in `registry.go` (provider implement what apply):
- `ServiceFilterer` — `SetServiceFilter([]string)` for `--services`
- `ServiceNamer` — `ServiceNames() []string` so `cmd/scan.go` size progress columns
- `RegionOverrider` — `SetRegionOverride([]string)` for `--region` (aws)
- `ProfileOverrider` — `SetProfile(string)` for `--profile` (aws)

## Per-service registry pattern

Each provider package own `services.go` define `serviceEntry`, `registerService`, `resolverEntry`, `registerResolver`. Signatures differ per provider (scope differ):

| Provider | Scanner fn signature | Resolver fn signature |
|----------|----------------------|-----------------------|
| aws      | `func(ctx, *account, region string, *store.Store, scanID) (total, inserted int, err error)` | `func(*account, *store.Store) error` |
| azure    | `func(ctx, *subscription, *azidentity.DefaultAzureCredential, *store.Store, scanID) (total, inserted int, err error)` | `func(*subscription, *store.Store) error` |
| gcp      | `func(ctx, *project, *store.Store, scanID) (total, inserted int, err error)` | `func(*project, *store.Store) error` |

AWS service entries carry `global bool` — global services (IAM, Organizations, S3, CloudFront, Route53) run once per account with `region=""`; regional services run per region. `registerService` panic on duplicate name — catch copy-paste mistake at startup.

```go
// internal/providers/aws/kms_scanners.go
func init() { registerService(serviceEntry{name: "aws:kms", fn: scanKMS}) }
func scanKMS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) { ... }

// internal/providers/aws/kms_resolvers.go
func init() { registerResolver(resolveKMSAliases) }
func resolveKMSAliases(acct *account, st *store.Store) error { ... }
```

## Two-phase scan

Foreign keys ON. Relationship rows reference resource IDs — resources must exist first.

1. **Phase 1a (aws only)**: global services, bounded by `semaphore.Weighted(maxConcurrentServices=10)` inside `errgroup`.
2. **Phase 1b**: regional services (aws), per-subscription services (azure), per-project services (gcp). Same semaphore-bounded errgroup pattern. Each service get `context.WithTimeout(ctx, serviceTimeout)` — aws/gcp use 5 min, azure use 30 min.
3. **Phase 2**: iterate `registeredResolvers` in order, call each with account/subscription/project + store handle. `st.WithRelCounter(&atomic.Int64)` wrap store so resolver-inserted edges counted for progress.

GCP add **phase 1a hierarchy pass** (`hierarchy_scanners.go`): org → folder → project closure rows must write before project-scoped resources so they can reference ancestors.

## Progress reporting

Store expose hook methods called by orchestrator:
- `st.ReportService(name, total, inserted)` — after each service complete phase 1.
- `st.ReportResolveStart(provider)` / `st.ReportResolveComplete(provider, count)` — bracket phase 2.
- `st.WithRelCounter(*atomic.Int64)` — derive child store counting relationship upserts.

`cmd/scan.go` use `Scanner.ServiceNames()` to pre-compute column width for aligned live output.

## Resource construction

Set on every `*store.Resource`: `Provider`, `AccountID`, `Type` (e.g. `"aws:ec2:instance"`), `NativeID` (ARN preferred), `Name`, `Region`, `Status`, `TagsJSON` (nilable `*string`), `AttributesJSON` (full SDK struct marshalled), `DiscoveredBy = scanID`. Leave `ID` empty — `UpsertResources` fill via `ResourceID(provider, accountID, type, nativeID)`.

`UpsertResources` ON CONFLICT only update: `name`, `status`, `tags`, `attributes`, `verified_at`, `verified_by`. **Not** update `region`, `zone`, `account_name`, `discovered_at` — set those on first insert.

### Paginated batching

Paginate with SDK paginators. Flush each page via `UpsertResources(batch)` — no accumulate all pages in memory.

### List-then-describe with concurrent enrichment

When List API return names/IDs only (EKS, KMS, DynamoDB), describe each item concurrently inside page:

```go
var mu sync.Mutex
var batch []*store.Resource
g, gctx := errgroup.WithContext(ctx)
for _, item := range page.Items {
    g.Go(func() error {
        desc, err := client.Describe(gctx, item.ID)
        if err != nil { return err }
        mu.Lock(); batch = append(batch, toResource(desc)); mu.Unlock()
        return nil
    })
}
if err := g.Wait(); err != nil { return 0, 0, err }
st.UpsertResources(batch)
```

Template: `eks_scanners.go`, `kms_scanners.go`.

## Closure table

Use `BatchAddToHierarchyClosure([][2]string{{child, parent}, ...})` after batch upsert — single transaction. Only use `AddToHierarchyClosure(child, parent)` for one-offs. Root nodes self-reference: `{rootID, rootID}`. Resource IDs deterministic — derive parent IDs via `store.ResourceID(...)`, no DB lookup needed.

`store.Resource` has **no `ParentID` field**. Hierarchy purely in closure table.

## Shared helpers (aws package — `aws.go`)

| Helper | Purpose |
|--------|---------|
| `mustJSON(v) string` | `util.MustJSON` wrapper |
| `sv(*string) string` | safe string deref |
| `tp(*time.Time) *string` | RFC3339 pointer |
| `sp(string) *string` | pointer-to-string literal |
| `ec2ARN(region, accountID, type, id) string` | build EC2 ARN |
| `awsTagsJSON[T awsTag]([]T) *string` | generic over all `types.Tag` variants |
| `mapTagsJSON(map[string]string) *string` | for SDKs returning tag maps |
| `isAccessDenied(err) bool` | matches `AccessDenied*`, `UnauthorizedOperation`, etc. |
| `skipIfAccessDenied(service, accountID, region, err) error` | log + return nil |

Azure and GCP have equivalent helpers in own `azure.go` / `gcp.go` (adapted to SDK error types: `azcore.ResponseError` 401/403 for azure, `googleapi.Error` 403 for gcp).

## Error handling

| Error type | Behaviour |
|------------|-----------|
| AccessDenied / 401 / 403 | `skipIfAccessDenied` log warning, return nil — scan continue |
| UnsupportedOperationException (kms asymmetric rotation) | caller ignore, store nil |
| FK violation from phantom target | resolver pre-load known target ID set, skip edges to missing rows (kms, secretsmanager) |
| Network / throttle / unmarshal | return error — errgroup cancel siblings, scan marked failed |

## Credential configuration

Read from `~/.disco/config.yaml` (viper), env prefix `DISCO_`. Credentials resolve through each SDK's default chain (env, files, IAM role / managed identity / ADC). Disco not manage secrets.

```yaml
aws:
  default_regions: [us-east-1, us-west-2]
  accounts:
    - id: "123456789012"
      name: production
      regions: [us-east-1]            # overrides default_regions
      role_arn: arn:aws:iam::...      # optional assume-role
```

## Adding a new provider

1. Create `internal/providers/<name>/<name>.go` — Scanner + `init()` + `Scan()`.
2. Create `internal/providers/<name>/services.go` — `serviceEntry` / `resolverEntry` registries.
3. Create `internal/providers/<name>/config.go` — viper loader for accounts/subs/projects.
4. Add blank import to `cmd/providers.go`: `_ "codeberg.org/icearp/disco/internal/providers/<name>"`.
5. Service files follow `<service>_scanners.go` + `<service>_resolvers.go` split.

## Adding a new service to an existing provider

1. `<service>_scanners.go` with `func init() { registerService(serviceEntry{...}) }`.
2. `<service>_resolvers.go` (optional) with `func init() { registerResolver(resolveFoo) }`.
3. Add type constants to `<name>_types.go` / `types.go`, append to `KnownTypes()`.
4. Append service name to `expectedAWSServices` / `expectedAzureServices` / `expectedGCPServices` in `registration_test.go` — test fail if service registered without being listed.
5. Add `<service>_resolvers_test.go` follow `newTestStore` / `upsertTestResource` / assert via `st.RelationshipsFrom(id)`.

---

# Resource ID hash function: keep SHA-256 truncated to 128 bits

## Decision: no change

`ResourceID` in `internal/store/resources.go` use `sha256.Sum256`, take first 16 bytes, encode as 32 hex chars.

### Collision odds at 128 bits

Birthday bound at 128 bits: 50% collision at ~2^64 items. At 1M resources:

```
P(collision) ≈ n² / 2·2^128  =  (10^6)² / (2 × 3.4×10^38)  ≈  1.5×10^-27
```

Effectively impossible. Drop to 64 bits (FNV-1a 64): P(collision) ≈ 2.7×10^-8 at 1M — non-zero, one collision silently corrupt data. Not worth.

### Table size

32-char TEXT PK ≈ 37 bytes/row (32 data + ~5 SQLite overhead). At 1M resources ≈ 37 MB ID storage. BLOB(16) save ~21 MB — negligible, force `hex(id)` / `unhex(?)` in every query.

### SQLite read performance

`memcmp` on 32-char ASCII single-digit ns. INTEGER rowid faster but not stable across rescans — auto-increment IDs won't do.

### FNV-1a 128-bit

Same output size, ~10× faster compute. But hashing ~100 ns vs 50–500 ms per API call — unmeasurable end-to-end.

### Verdict

Keep SHA-256 truncated to 128 bits. Switch require data migration, no user-visible benefit, introduce risk.

---

# Scan subcommands: `disco scan <provider>`

## Routing (as implemented)

| Command | Behaviour |
|---------|-----------|
| `disco scan` | Runs all registered providers |
| `disco scan aws` | Runs only AWS scanner |
| `disco scan azure` | Runs only Azure scanner |
| `disco scan gcp` | Runs only GCP scanner |

## Flags

- `--services s1,s2` — restrict to named services (use `ServiceFilterer`). Subcommand-only.
- `--region r1,r2` — aws only (use `RegionOverrider`). Override config and per-account regions.
- `--profile name` — aws only (use `ProfileOverrider`). Select credential profile.

Flags present only on subcommands whose Scanner implement corresponding optional interface.

## Init ordering

Go guarantee all `import` init functions complete before importing package's `init()` run. Blank imports in `cmd/providers.go` register scanners before `cmd/scan.go`'s `init()` iterate `providers.All()` to build subcommands — no fragility.

## Shared helper

`runScan(cmd, scanners)` in `cmd/scan.go` hold common open-db / CreateScan / errgroup / Complete/FailScan lifecycle. `scanCmd.RunE` call it with `providers.All()`; each subcommand call it with single-element slice.

## Adding a provider subcommand

Nothing to do — blank import in `cmd/providers.go` enough. `cmd/scan.go`'s `init()` loop `providers.All()` and build one subcommand per scanner.