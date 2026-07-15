# disco code structure

Architectural map. Build commands + CGO rule live in `CLAUDE.md`. Behavioural conventions live in path-scoped `CLAUDE.md` files (cmd/, store/, internal/providers/, internal/providers/aws/) — this doc routes you to them.

## Top-level flow

```
cmd/           →  internal/providers/<name>/  →  store/  →  sqlite ($XDG_DATA_HOME/disco/disco.db)
 (cobra CLI)      (scanners + resolvers)         (sqlx + squirrel)
                            ↓                            ↑
                   cmd/graph, cmd/list, cmd/diff  ───────┘
```

- **`cmd/`** — cobra subcommands. `cmd/providers.go` blank-imports provider packages so `init()` registration runs. CLI-flag + lifecycle detail: `cmd/CLAUDE.md`.
- **`internal/providers/<name>/`** — AWS / Azure / GCP scanners. Two-phase: resources, then relationships. Conventions: `internal/providers/CLAUDE.md`. AWS specifics: `internal/providers/aws/CLAUDE.md`.
- **`store/`** — sqlite layer (`modernc.org/sqlite` CGO-free, `sqlx`, `squirrel`). Tables (`resources`, `relationships`, `hierarchy_closure`, `scans`), edge kinds, scrubbing, IDs, migrations: `store/CLAUDE.md`.
- **`internal/util/`** — `MustJSON`, `Sv`, `TimeRFC3339`, `AllResources`.

Edge kinds + relationship semantics: `store/CLAUDE.md` "Edge kinds".

CLI subcommand surface (`scan`, `resources`, `diff`, `graph`, `check`, `coverage`) + flags: `cmd/CLAUDE.md`. `check` ships the engine + BYO `--rules`; curated compliance packs (CIS / NIST 800-53 / PCI-DSS / Well-Architected) are not yet bundled.

---

# Provider code structure

Per-service API calls via each cloud's native Go SDK (no unified discovery APIs). Provider package implements the `Scanner` interface (`internal/providers/registry.go`) and persists via `store.UpsertResources` / `store.UpsertRelationship` / `store.RecordHierarchyBatch`. Registry, file naming, "add new provider" / "add new service" steps, sidecar pattern, embed-child-data, registration tests, resolver test pattern: `internal/providers/CLAUDE.md`.

## Per-provider scanner / resolver function signatures

Function-pointer shapes differ because each provider's scope object differs. Reference when reading any `<service>_scanners.go` / `<service>_resolvers.go`:

| Provider | Scanner fn signature | Resolver fn signature |
|----------|----------------------|-----------------------|
| aws      | `func(ctx, *account, region string, *store.Store, scanID) (total, inserted int, err error)` | `func(*account, *store.Store) error` |
| azure    | `func(ctx, *subscription, *azidentity.DefaultAzureCredential, *store.Store, scanID) (total, inserted int, err error)` | `func(*subscription, *store.Store) error` |
| gcp      | `func(ctx, *project, *store.Store, scanID) (total, inserted int, err error)` | `func(*project, *store.Store) error` |

AWS service entries carry `global bool` — global services (IAM, Organizations, S3, CloudFront, Route 53, Shield) run once per account with `region=""`; regional services run per region. `registerService` panics on duplicate name.

```go
// internal/providers/aws/kms_scanners.go
func init() { registerService(serviceEntry{name: "aws:kms", fn: scanKMS}) }
func scanKMS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) { ... }

// internal/providers/aws/kms_resolvers.go
func init() { registerResolver(resolveKMSAliases) }
func resolveKMSAliases(acct *account, st *store.Store) error { ... }
```

## Two-phase scan + concurrency

Foreign keys ON. Relationship rows reference resource IDs — resources must exist first.

1. **Phase 1a (aws only)**: global services, bounded by `semaphore.Weighted(maxConcurrentServices=10)`.
2. **Phase 1b**: regional services (aws), per-subscription services (azure), per-project services (gcp). Same semaphore-bounded fan-out. Each service gets `context.WithTimeout(ctx, serviceTimeout)` — aws/gcp `5 * time.Minute`, azure `30 * time.Minute`.
3. **Phase 2**: iterate `registeredResolvers` in order, call each with account/subscription/project + store handle. `st.WithRelCounter(&atomic.Int64)` wraps the store so resolver edges are counted for progress.

GCP adds **phase 1a hierarchy pass** (`hierarchy_scanners.go`): org → folder → project closure rows must write before project-scoped resources so they can reference ancestors.

Per-service / per-region errors do **not** abort the scan or cancel siblings — they are reported via `store.ReportError` and rendered as one grouped block at end. See `internal/providers/CLAUDE.md` "Errors never abort scan".

## Progress reporting

Store hook methods called by orchestrator:

- `st.ReportService(service, total, inserted, errCount int)` — after each service completes phase 1; `errCount>0` surfaces a `(with errors)` suffix on the per-service progress line.
- `st.ReportResolveStart(provider)` / `st.ReportResolveComplete(provider, count)` — bracket phase 2.
- `st.WithRelCounter(*atomic.Int64)` — derives a child store that counts relationship upserts.

`cmd/scan.go` uses `Scanner.ServiceNames()` (optional interface) to pre-compute column width for aligned live output.

## Resource construction

Every `*store.Resource`: `Provider`, `AccountID`, `Type` (e.g. `"aws:ec2:instance"`), `NativeID` (ARN preferred), `Name`, `Region`, `Status`, `TagsJSON` (`*string`, nilable), `AttributesJSON` (full SDK struct marshalled), `DiscoveredBy = scanID`. Leave `ID` empty — `UpsertResources` fills it via `ResourceID(provider, accountID, nativeID)`.

ON CONFLICT update scope and FK constraints: `store/CLAUDE.md` "UpsertResources ON CONFLICT scope".

### Paginated batching

Paginate via SDK paginators. Flush each page with `UpsertResources(batch)` — never accumulate all pages in memory.

### List-then-describe with concurrent enrichment

When List APIs return names/IDs only (EKS, KMS, DynamoDB), describe each item concurrently inside a page:

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

Templates: `eks_scanners.go`, `kms_scanners.go`. Concurrency tiers (`fanoutHigh`, `fanoutMed`, `fanoutLow`) live in `aws/concurrency.go` — see `internal/providers/aws/CLAUDE.md`.

Closure-table population pattern (`RecordHierarchyBatch`): `store/CLAUDE.md`.

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
| `skipIfAccessDenied(st, service, accountID, region, err) error` | report soft-skip + return nil |

Azure and GCP have equivalent helpers in their own `azure.go` / `gcp.go` (adapted to SDK error types: `azcore.ResponseError` 401/403 for azure, `googleapi.Error` 403 for gcp).

## Credential configuration

Read from `$XDG_CONFIG_HOME/disco/config.yaml` (viper; `~/.config/disco/config.yaml` on Linux, platform app-data dir on macOS/Windows), env prefix `DISCO_`. Credentials resolve through each SDK's default chain (env, files, IAM role / managed identity / ADC). Disco does not manage secrets.

```yaml
aws:
  default_regions: [us-east-1, us-west-2]
  accounts:
    - id: "123456789012"
      name: production
      regions: [us-east-1]            # overrides default_regions
      role_arn: arn:aws:iam::...      # optional assume-role
```

---

# Resource ID hash function: keep SHA-256 truncated to 128 bits

## Decision: no change

`ResourceID` in `store/resources.go` uses `sha256.Sum256`, takes first 16 bytes, encodes as 32 hex chars.

### Collision odds at 128 bits

Birthday bound at 128 bits: 50% collision at ~2^64 items. At 1M resources:

```
P(collision) ≈ n² / 2·2^128  =  (10^6)² / (2 × 3.4×10^38)  ≈  1.5×10^-27
```

Effectively impossible. Drop to 64 bits (FNV-1a 64): P(collision) ≈ 2.7×10^-8 at 1M — non-zero, one collision silently corrupts data. Not worth.

### Table size

32-char TEXT PK ≈ 37 bytes/row (32 data + ~5 SQLite overhead). At 1M resources ≈ 37 MB ID storage. BLOB(16) saves ~21 MB — negligible, and forces `hex(id)` / `unhex(?)` in every query.

### SQLite read performance

`memcmp` on 32-char ASCII is single-digit ns. INTEGER rowid is faster but not stable across rescans — auto-increment IDs won't do.

### FNV-1a 128-bit

Same output size, ~10× faster compute. But hashing ~100 ns vs 50–500 ms per API call — unmeasurable end-to-end.

### Verdict

Keep SHA-256 truncated to 128 bits. Switching requires data migration, no user-visible benefit, introduces risk.
