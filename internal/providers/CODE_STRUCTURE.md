# Provider code structure

## Context

Providers make per-service API calls using each cloud's native Go SDK. A provider package must implement the `Scanner` interface (`internal/providers/registry.go`), write resources via `store.UpsertResources`, write relationships via `store.UpsertRelationship`, and update the closure table via `store.AddToHierarchyClosure`. This plan defines the internal structure every provider should follow so they are consistent and easy to add.

## Package layout

```
internal/providers/<name>/
├── <name>.go     # Scanner struct, Name(), Scan() — top-level orchestrator
├── config.go     # reads viper config for accounts/subscriptions/projects, credentials
└── <service>.go  # one file per cloud service (ec2.go, s3.go, iam.go, …)
```

Service files are package-level functions, not methods — the Scanner struct holds config/credential state passed as arguments. This keeps individual service files small, focused, and independently testable.

## Scanner struct and registration

```go
// internal/providers/aws/aws.go
package aws

import "codeburg.org/icearp/disco/internal/providers"

func init() { providers.Register(&Scanner{}) }

type Scanner struct{}

func (s *Scanner) Name() string { return "aws" }

func (s *Scanner) Scan(ctx context.Context, st *store.Store, scanID string) error {
    cfg, err := loadConfig()   // reads viper + resolves credentials
    if err != nil { return err }
    return scanAccounts(ctx, cfg, st, scanID)
}
```

## Two-phase scan (resources then relationships)

Foreign keys are ON. A relationship INSERT referencing a resource that does not yet exist in the DB will fail. Providers must scan in two phases:

1. **Phase 1 — resources**: run all service scanners in parallel via `errgroup`. Each upserts resources (and calls `AddToHierarchyClosure` for resources with a parent).
2. **Phase 2 — relationships**: after all resources are written, run relationship resolution. Each service's resolver reads resource IDs from the DB or derives them via `store.ResourceID(...)` and calls `store.UpsertRelationship`.

```go
func scanAccounts(ctx context.Context, cfg *awsConfig, st *store.Store, scanID string) error {
    for _, acct := range cfg.Accounts {
        if err := scanAccount(ctx, acct, st, scanID); err != nil {
            return err
        }
    }
    return nil
}

func scanAccount(ctx context.Context, acct accountConfig, st *store.Store, scanID string) error {
    // Phase 1: resources (parallel across services)
    g, ctx := errgroup.WithContext(ctx)
    g.Go(func() error { return scanEC2(ctx, acct, st, scanID) })
    g.Go(func() error { return scanS3(ctx, acct, st, scanID) })
    g.Go(func() error { return scanIAM(ctx, acct, st, scanID) })
    // … one g.Go per service …
    if err := g.Wait(); err != nil { return err }

    // Phase 2: relationships (can also be parallel, resources already exist)
    g2, ctx := errgroup.WithContext(ctx)
    g2.Go(func() error { return resolveEC2Relationships(ctx, acct, st, scanID) })
    // …
    return g2.Wait()
}
```

## Per-service scanner pattern

```go
// internal/providers/aws/ec2.go
func scanEC2(ctx context.Context, acct accountConfig, st *store.Store, scanID string) error {
    client := ec2.NewFromConfig(acct.awsCfg)
    paginator := ec2.NewDescribeInstancesPaginator(client, &ec2.DescribeInstancesInput{})
    for paginator.HasMorePages() {
        page, err := paginator.NextPage(ctx)
        if err != nil {
            // permission errors: log and continue (best-effort)
            if isAccessDenied(err) {
                log.Printf("warn: ec2:DescribeInstances %s: %v", acct.ID, err)
                return nil
            }
            return err
        }
        var batch []*store.Resource
        for _, r := range page.Reservations {
            for _, inst := range r.Instances {
                batch = append(batch, instanceToResource(inst, acct.ID, scanID))
            }
        }
        if err := st.UpsertResources(batch); err != nil { return err }
    }
    return nil
}
```

Key points:
- Paginate in a loop; flush each page to the DB immediately (no buffering all results in memory).
- Permission errors (`AccessDenied`, `UnauthorizedOperation`) are logged and skipped — not every account has every service enabled.
- `instanceToResource` converts the SDK type to `*store.Resource`: set `Provider`, `AccountID`, `Type` (e.g. `"aws:ec2:instance"`), `NativeID` (ARN), `Name`, `Region`, `Status`, `TagsJSON` (marshal tags map), `AttributesJSON` (marshal full SDK struct), `ScanID`. Leave `ID` empty — `UpsertResources` calls `ResourceID` to fill it.

## Resource construction helper pattern

```go
func instanceToResource(inst types.Instance, accountID, scanID string) *store.Resource {
    attrsJSON, _ := json.Marshal(inst)   // full SDK struct as JSON blob
    tagsJSON, _  := json.Marshal(tagsToMap(inst.Tags))
    name := tagValue(inst.Tags, "Name")
    return &store.Resource{
        Provider:       "aws",
        AccountID:      accountID,
        Type:           "aws:ec2:instance",
        NativeID:       aws.ToString(inst.InstanceId),  // ARN preferred if available
        Name:           name,
        Region:         regionPtr(inst),
        Status:         (*string)(inst.State.Name),
        TagsJSON:       &tagsJSON,
        AttributesJSON: string(attrsJSON),
        ScanID:         scanID,
    }
}
```

## Closure table

Call `st.AddToHierarchyClosure(childID, parentID)` immediately after `UpsertResources` for any resource with a parent (e.g. GCP project under a folder). Use `store.ResourceID(...)` to compute the parent ID deterministically without a DB lookup.

## Error handling strategy

| Error type | Behaviour |
|------------|-----------|
| `AccessDenied` / `403` | Log warning, skip service, continue |
| Network / throttle | Return error (errgroup cancels siblings) |
| Unmarshal / logic error | Return error |

Partial scan failures bubble up through the errgroup in `scanAccount`, which marks the scan as `failed`. Future: consider `partial` status when some services succeed.

## Credential configuration (viper)

Read from `~/.disco/config.yaml` under the provider key. Each provider's `config.go` calls `viper.GetStringSlice` / `viper.UnmarshalKey`. Credentials fall through to the SDK's default chain (env vars, credential files, IAM/managed identity/ADC) — disco does not manage secrets.

```yaml
# ~/.disco/config.yaml
aws:
  regions: [us-east-1, us-west-2]    # default regions for all accounts
  accounts:
    - id: "123456789012"
      name: production
      role_arn: arn:aws:iam::123456789012:role/disco-scanner   # optional
      regions: [us-east-1]           # overrides default for this account
```

## Files to create (for each new provider)

| File | Contents |
|------|----------|
| `internal/providers/<name>/<name>.go` | Scanner struct, `init()`, `Name()`, `Scan()`, account/project enumeration |
| `internal/providers/<name>/config.go` | viper config struct + loader |
| `internal/providers/<name>/<service>.go` | one per service: scan + relationship resolver functions |
| `cmd/providers.go` | add `_ "codeburg.org/icearp/disco/internal/providers/<name>"` |

---

# Resource ID hash function: keep SHA-256 truncated to 128 bits

## Decision: no change

`ResourceID` in `internal/store/resources.go:33` currently uses `sha256.Sum256`, takes the first 16 bytes, and encodes as 32 hex chars. The question is whether a simpler/smaller hash would improve collision odds, table size, or SQLite read performance.

### Collision odds at 128 bits

Birthday bound at 128 bits: 50% collision probability requires ~2^64 items. A large multi-account cloud scan might produce 1 million resources. At that scale:

```
P(collision) ≈ n² / 2·2^128  =  (10^6)² / (2 × 3.4×10^38)  ≈  1.5×10^-27
```

Effectively impossible. Dropping to 64 bits (FNV-1a 64): birthday bound drops to ~4 billion, giving P(collision) ≈ 2.7×10^-8 (~1 in 37 million) at 1M resources — non-zero, and one collision means silently corrupted data. Not worth it.

### Table size

The 32-char TEXT primary key costs ~37 bytes per row (32 data + ~5 SQLite overhead). At 1M resources that's 37 MB just for IDs. A BLOB(16) alternative would save ~21 MB — negligible. More importantly, switching to BLOB makes every SQL query use `hex(id)` or `unhex(?)` — worse readability for no meaningful gain.

### SQLite read performance

SQLite B-tree key comparisons on 32-char ASCII are fast (`memcmp`). The difference between a 16-char and 32-char comparison is single-digit nanoseconds. An INTEGER rowid would be faster, but the ID must be stable across rescans — auto-increment integers are not stable.

### FNV-1a 128-bit (same output size, faster compute)

`hash/fnv` stdlib provides `fnv.New128a()` — 128-bit output, ~10× faster than SHA-256 to compute. Same collision odds as current. But the hash computation is never the bottleneck: each cloud API call takes 50–500 ms; hashing takes ~100 ns. The speedup is unmeasurable end-to-end.

### Verdict

Keep SHA-256 truncated to 128 bits. No code change. Any switch requires a data migration, provides no user-visible benefit, and introduces risk.

---

# Scan subcommands: `disco scan <provider>`

## Context

Each registered provider should be reachable as a subcommand of `disco scan` (e.g. `disco scan aws`). Scanning a single provider is the common case; scanning all providers at once is supported but rare. The `--provider` flag on `disco scan` is replaced by this subcommand structure — it's redundant now that providers are addressable directly. The provider registry (`internal/providers/registry.go`) and parallel errgroup execution in `cmd/scan.go` already exist; this change is purely in how cobra routes commands.

## Approach

### Routing

| Command | Behaviour |
|---------|-----------|
| `disco scan` | Runs all registered providers in parallel (existing logic) |
| `disco scan aws` | Runs only the AWS scanner |
| `disco scan azure` | Runs only the Azure scanner |
| `disco scan gcp` | Runs only the GCP scanner |

### Init ordering guarantee

Go guarantees that all `import` statements across a package are initialized before any `init()` function in that package runs. Blank imports in `cmd/providers.go` therefore have their `init()`s (which call `providers.Register(...)`) complete before `cmd/scan.go`'s `init()` iterates `providers.All()` to build subcommands. No fragility.

### Shared helper

Extract a `runScan(cmd *cobra.Command, scanners []providers.Scanner) error` function used by both `scanCmd.RunE` (all providers) and each subcommand's `RunE` (single provider). Eliminates duplication of the open-db / CreateScan / errgroup / Complete/FailScan lifecycle.

## Files

### `cmd/scan.go` — modify

- Remove `scanProviders []string` var and `--provider` flag.
- Add `runScan(cmd, scanners)` helper containing the current RunE body.
- `scanCmd.RunE` → calls `runScan(cmd, providers.All())`.
- In `init()`, after `rootCmd.AddCommand(scanCmd)`, iterate `providers.All()` and add one subcommand per scanner:
  ```go
  for _, s := range providers.All() {
      s := s
      scanCmd.AddCommand(&cobra.Command{
          Use:   s.Name(),
          Short: fmt.Sprintf("Scan %s resources", s.Name()),
          RunE:  func(cmd *cobra.Command, _ []string) error {
              return runScan(cmd, []providers.Scanner{s})
          },
      })
  }
  ```

### `cmd/providers.go` — create (new)

Blank imports only. Empty for now; grows as providers are added.
```go
package cmd

// Provider packages register themselves via init(). Add a blank import here
// for each new provider so its Scanner is available to disco scan subcommands.
```

## Verification

```bash
CGO_ENABLED=0 go build ./...   # must compile clean
./disco scan --help             # should list registered provider subcommands (none yet)
./disco scan                    # "No providers registered — nothing to scan."
```

Once a provider is implemented and imported in `cmd/providers.go`:
```bash
./disco scan aws --help         # subcommand appears
./disco scan aws                # runs only AWS scanner
./disco scan                    # runs all scanners in parallel
```
