# CLAUDE.md

Guide Claude Code (claude.ai/code) in repo.

## Commands

```bash
# Build (CGO_ENABLED=0 is required — all builds must be CGO-free)
CGO_ENABLED=0 go build -o disco .

# Cross-compile for all targets from Linux
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64  go build -o dist/disco-linux-amd64 .
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64  go build -o dist/disco-darwin-arm64 .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64  go build -o dist/disco-windows-amd64.exe .

# Run tests
CGO_ENABLED=0 go test ./...

# Run a single test
CGO_ENABLED=0 go test ./internal/store/... -run TestFoo -v

# Vet and lint
go vet ./...
```

## Architecture

`disco` = cloud resource discovery CLI (cobra + viper). Scan AWS accounts, Azure subs/resource groups, GCP orgs/folders. Resolve + store resource relationships in local SQLite.

### Key constraint: CGO_ENABLED=0 always

Storage: `modernc.org/sqlite` — pure-Go SQLite transpile. Cross-platform single-binary, no C toolchain. **Never swap for `mattn/go-sqlite3` or any CGO dep.**

### Data flow

```
cmd/scan.go  →  internal/providers/<provider>/  →  internal/store/
```

Provider scanners call `store.UpsertResources()`, `store.UpsertRelationship()`, `store.BatchAddToHierarchyClosure()` to persist. Errors from all three must propagate — never silence with `_ =`.

### Resolver conventions

- **Scanner attribute JSON uses PascalCase keys.** `mustJSON` calls `json.Marshal` on AWS SDK v2 response structs, no json tags — `ClusterArn` stays `ClusterArn`, not `clusterArn`. Resolver structs need PascalCase tags (`json:"ClusterArn"`) or silently match nothing on real scan data while tests pass on hand-rolled JSON.
- **ARN helpers**: `ec2ARN(region, acct, kind, id)` → `arn:aws:ec2:{r}:{a}:{kind}/{id}` (slash sep). `rdsARN(region, acct, kind, id)` → `arn:aws:rds:{r}:{a}:{kind}:{id}` (colon sep, RDS-specific). Resolvers rebuild target ARN from native ID, pass to `store.ResourceID(...)`. Wrong shape = phantom target, buried FK error.
- **Edge kinds** (`internal/store`):
  - `contains` — hierarchy edge. Intended parent→child (VPC→subnet, KMS key→alias), but some resolvers emit child→parent (EFS mt→fs, GuardDuty filter→detector, Backup selection→plan). Match existing direction for service you touch; no "fix" without sweeping all tests.
  - `attached-to` — structural membership (instance → VPC/subnet, ESM → function)
  - `uses` — runtime dep, no lifecycle coupling (instance → security-group, function → KMS key, service → subnet in awsvpc mode)
  - `assumes` — IAM trust (function → execution role, task-def → task/exec role)
  - `routes-to` — routing edges (route table → target)
  - `peer` — bidirectional peering (VPC peering)
- **KMS edges**: skip empty `KmsKeyId` / `KMSKeyArn`. AWS-managed default keys unscanned, edge = dangling target. `if sv(attrs.KmsKeyId) == "" { continue }`.
- **`logGroupNativeIDFromName(accountID, region, name)`** — in `logs_scanners.go`, callable from any file in `package aws`. Rebuilds `arn:aws:logs:{r}:{a}:log-group:{name}`. Use instead of `fmt.Sprintf` to keep NativeID shape consistent with scanner.
- **CloudWatch Logs ARN `:*` suffix**: SDK returns `CloudWatchLogsLogGroupArn` with trailing `:*`. Strip via `strings.TrimSuffix(arn, ":*")` before NativeID lookup or edge points to phantom resource.
- **EFS mount target NativeID**: no native ARN. Synthesize: `arn:aws:elasticfilesystem:{region}:{acct}:file-system/{fsid}/mount-target/{mtid}` using `FileSystemId` + `MountTargetId` from `DescribeMountTargets` response.
- **AWS Backup plan ARN** uses `backup-plan:`, not `plan:`. Real: `arn:aws:backup:{r}:{a}:backup-plan:{planId}`. Synthetic selection NativeID `{planARN}/selection/{selId}` — trim `/selection/...` in resolver to recover parent plan ARN. Wrong prefix → FK error on closure insert.
- **Organizations NativeID = full ARN, not raw ID**. Accounts + OUs keyed by `sv(a.Arn)`, not `o-xxx` / 12-digit account ID. APIs like `ListDelegatedAdministrators` return raw IDs — translate via `loadOrgTargetIndex` (`organizations_resolvers.go`) before building `ResourceID`.

### ELBv2 LB attrs wrapped

Scanner stores LoadBalancer as `{"lb": <LB>, "type": "<kind>"}` (see `elb_scanners.go:109`), not top-level. Resolvers reading `DNSName`, `Scheme`, `VpcId`, etc. must unmarshal under `"lb"` key or silently get zero values.

### Route53 alias DNS normalization

`AliasTarget.DNSName` carries trailing `.` + (on ELB targets) leading `dualstack.` prefix backend attrs lack. Normalize both before lookup: `strings.TrimSuffix(strings.ToLower(s), ".")` then `strings.TrimPrefix(s, "dualstack.")`. See `normalizeAliasDNS` in `route53_resolvers.go`.

### CloudWatch alarm dimensions — two shapes

Simple alarms: top-level `Namespace` + `Dimensions[]`. Metric-math alarms: nested under `Metrics[].MetricStat.Metric.{Namespace,Dimensions}`. Resolvers must read both or skip half of real alarms. See `resolveAlarmDimensions` in `cloudwatch_resolvers.go`.

### Cognito JWT issuer URL

APIGW v2 JWT authorizer `JwtConfiguration.Issuer` shape: `https://cognito-idp.{region}.amazonaws.com/{poolId}`. `strings.Cut` on host/path after prefix strip; rebuild `arn:aws:cognito-idp:{region}:{acct}:userpool/{poolId}` for `store.ResourceID` lookup. Non-Cognito issuers (Auth0, Okta) skip — no phantom edges.

### IAM policy-document parsing

- **`AssumeRolePolicyDocument` + all IAM policy docs URL-encoded JSON** (AWS SDK v2). `url.QueryUnescape` before `json.Unmarshal` or parse silently fails.
- **`Principal.Federated` / `AWS` / `Service` may be string OR `[]string`.** Use custom `UnmarshalJSON` wrapper type (see `principalList` in `iam_resolvers.go`) — bare `[]string` tag only matches array form.
- **Federated-provider ARN dispatch**: `:saml-provider/` → `TypeIAMSAMLProvider`; `:oidc-provider/` → `TypeIAMOIDCProvider`. No other Federated shapes emit edges (skip rather than dangle).

### WAFv2 scope pattern

WAFv2 two scopes: `REGIONAL` (per-region) + `CLOUDFRONT` (global). CLOUDFRONT scope reachable only from `us-east-1` — other regions error. Guard with `if region == "us-east-1"` before CLOUDFRONT-scope calls to dodge duplicates.

### Per-service API mandate

Providers make **per-service API calls** via each cloud's native Go SDK. No unified discovery APIs (AWS Resource Explorer, Azure Resource Graph, GCP Cloud Asset Inventory). Every AWS service, Azure `arm*` package, GCP service client called direct. Needed for full coverage.

### CLI structure

- `disco scan` — runs all registered providers in parallel
- `disco scan <provider>` — single provider (e.g. `disco scan aws`)
- `disco scan --providers aws,gcp` — only named providers (comma-separated `StringSlice`)
- `disco list` — query local DB with filters (`--provider`, `--type`, `--region`, `--status`, `--tag-key`/`--tag-value`, `--output table|json|csv|jsonl`)
- `disco diff <scanA> <scanB>` — drift detection; emits added/removed/changed rows between two scan IDs
- `disco graph <resource-id> --depth N --kinds contains,attached-to --direction both --output table|json|dot` — walks `relationships` + `hierarchy_closure`
- `disco check --rules rules.yaml --builtins --severity high --exit-nonzero` — runs security rules against store

### Provider registry (`internal/providers/registry.go`)

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

`cmd/providers.go` holds all blank imports. `cmd/scan.go`'s `init()` iterates `providers.All()` to build `disco scan <name>` subcommands — no `scan.go` change when adding provider.

### Parallel scanning

`cmd/scan.go` runs selected scanners concurrent via `errgroup.WithContext`. First error cancels siblings. On error: scan record marked failed via `db.FailScan`. On success: `db.CompleteScan`.

### Storage layer (`internal/store/`)

Four tables: `resources`, `relationships`, `hierarchy_closure`, `scans`.

- **`resources`**: one row per cloud entity. `attributes` (JSON) = full provider API response. `tags` (JSON) denormalized for `json_extract()` queries. `verified_at` (RFC3339) + `verified_by` (scan ID FK) auto-set by `UpsertResources` — callers must not set. No `parent_id` column — hierarchy via `BatchAddToHierarchyClosure(pairs)` only.
- **`relationships`**: directed edges. `kind`: `contains`, `attached-to`, `uses`, `routes-to`, `peer`, `assumes`. UNIQUE on `(from_id, to_id, kind)` — multiple kinds may coexist between same pair. Hierarchy `contains` lives in `hierarchy_closure` only (not here), so second edge (e.g. `attached-to`) between already-hierarchical resources conflict-free. `UpsertRelationship(..., attrs *string)` accepts JSON blob for per-edge metadata (e.g. Orgs delegated-services list).
- **`hierarchy_closure`**: closure table for O(1) "all descendants of node X", no recursive CTEs. Always populate via `BatchAddToHierarchyClosure(pairs)` (single tx) after upserting resources with `parent_id`.
- **`scans`**: lifecycle record per scan run (created at start, updated on complete/fail).

Queries built with `squirrel` (`sq.Select(...).Where(...)`) — no string interpolation. `sqlx` handles struct scanning. Raw SQL for CTEs + anything squirrel can't express cleanly.

**Secret scrubbing**: `UpsertResources` calls `scrubAttributes` (`internal/store/sanitize.go`) on every `attributes` JSON blob before insert. Denylist of key substrings (`password`, `passphrase`, `secret`, `token`, `signature`, `presignedurl`, `credential`, `privatekey`, `apikey`, `bearer`, `authorization`) → `"[REDACTED]"`. Malformed JSON passes through untouched. Providers must NOT pre-sanitize — store boundary owns this.

**Scalar-only redaction**: sensitive key redacts only when value scalar. Object/array values recurse, so structural containers whose name matches denylist (e.g. ECS `ContainerDefinitions[].Secrets[]`, array of `{ValueFrom}` refs) pass through intact; leaf leaks (`SecretString`, `Password`, ...) still caught. If resolver unmarshal silently yields zero edges under key whose name matches denylist, check here first.

### Resource IDs

`ResourceID(provider, accountID, type, nativeID)` — `internal/store/resources.go` — produces 32-hex-char SHA-256 prefix. Stable across rescans; primary key.

Scan IDs: `crypto/rand` + `encoding/hex` (same 32-char hex). No `uuid` dep.

### Resource type naming

Namespaced lowercase: `aws:ec2:instance`, `azure:compute:virtual-machine`, `gcp:compute:instance`.

### New `Type*` constant → append to `KnownTypes()`

New `Type*` const in `aws_types.go` must append to `KnownTypes()` slice same file. Coverage command + types gap-analysis use it. No test catches omission.

### FK-safe edge emit when target partially scanned

Resolver targets type with unscanned members (public/Marketplace AMIs, cross-account ARNs, shared snapshots) — build target id set once via `ListResources(Types: []string{TargetType})`, emit edge only if computed target id present. Prevents FK blowup on `UpsertRelationship` + phantom edges. Precedent: `keyPairByNameRegion`, `imageByID` in `ec2_compute_mgmt_resolvers.go`.

### Ownership-filtered AWS scanners

AWS Describe* with ownership filter (`Owners=["self"]` for AMIs/snapshots/FPGA images) — scan self-owned only. Public/Marketplace/shared sets unbounded + not ours to audit. Cross-account refs from scanned resources (instance → public AMI) handled via FK-safe lookup above.

### Shared utilities (`internal/util`)

`util.MustJSON(v any) string`, `util.Sv(p *string) string`, `util.AllResources` (= `math.MaxUint32`, used as `Limit` in `ListResources` to fetch all rows). Each provider keeps unexported one-liner wrappers (`mustJSON`, `sv`) delegating to `util` — call sites clean, logic centralized.

### Provider file naming

Scanners in `<service>_scanners.go`, resolvers in `<service>_resolvers.go`. `resolveRelationships` orchestrator in provider top-level file (`aws.go`, `azure.go`, `gcp.go`).

### Embedding child data in parent attributes

Child resource (e.g. EventBridge rule targets) no independent lifecycle, meaningful only via parent — fetch child at scan time, embed under key in parent's `AttributesJSON` (e.g. `{"Rule": ..., "Targets": [...]}`). Resolvers read embedded data, no extra API calls.

**Warning — wrapping breaks existing resolvers.** Switching scanner from raw SDK struct to wrapped (e.g. adding `Targets` alongside `TargetGroup`) silently drops every edge from resolvers still reading old top-level shape — JSON unmarshal into old struct succeeds with zero values, no error. Grep resolvers for type before wrapping, update attribute structs to nest under new key.

### Non-resource config fetches → sidecar on `account`

Do NOT invent resource types for AWS concepts that aren't real resources (`aws:s3:bucket-encryption` rejected — `GetBucketEncryption` returns config *of* existing bucket, not own ARN'd resource). Do NOT wrap primary resource's raw SDK attrs JSON to co-locate config — `attributes` must equal native SDK response verbatim.

Pattern for edges needing non-resource config: stash on `account` struct during scan (mutex-protected if concurrent), consume in resolver. Edge persists; raw config ephemeral per scan. Precedent: `account.s3BucketEncryption` populated by `scanS3BucketEncryptions`, consumed by `resolveS3BucketEncryptionRelationships`. If config must later be queryable, add generic `resource_configs(resource_id, config_type, payload_json)` sidecar table — do NOT retrofit by synthesizing resource type or wrapping attrs.

### AWS service-integration ARNs use `:::`

Step Functions Definitions + similar carry built-in integration ARNs like `arn:aws:states:::sns:publish` where region+account segments empty. Substring-based ARN dispatchers (`sfnTargetType`, `eventBridgeTargetType`) must filter `strings.Contains(arn, ":::")` before classifying, or emit edges to non-existent resources + blow FK constraints.

### List-then-describe pattern (N+1 avoidance)

AWS service returns only names from List API (EKS, DynamoDB) — describe each resource concurrent via `errgroup` + `sync.Mutex` to collect, then batch upsert. No sequential Describe in loop.

### Sparse list entry → store Get body

`List*` returns skeleton (`{Id, Arn, HomeRegion, IsEnabled}`) while edge-bearing fields live on `Get*`/`Describe*` body (e.g. S3 StorageLens `Include.Buckets[]`, `DataExport.S3BucketDestination`). Enrich scanner: fan-out Get per entry, store Get response as `AttributesJSON`. Still "native SDK response verbatim" — Get body IS native. Don't merge List+Get into ad-hoc struct; pick one SDK response + store whole. Precedent: `scanStorageLens` in `s3control_scanners.go`.

### Loop-var copy unneeded (Go 1.22+)

`for _, x := range xs { g.Go(func() { ... x ... }) }` — no `x := x` shadow needed. Linter flags `forvar: copying variable is unneeded`. Per-iteration scope built in.

### SDK v2 paginator availability per-op

`New<Op>Paginator` exists only for ops AWS models as paginated. Many List ops no paginator — eventbridge, cloudfront Marker ops, wafv2, apigatewayv2 (`GetApis`/`GetAuthorizers`/`GetDomainNames`/`GetApiMappings`), logs (`DescribeAccountPolicies`/`DescribeQueryDefinitions`/`DescribeResourcePolicies`), ec2 (`DescribeVpcEndpointServices`/`DescribeVpcBlockPublicAccessExclusions`), rds `DescribeDBShardGroups`. Before converting manual `NextToken`/`Marker` loop, grep `~/go/pkg/mod/github.com/aws/aws-sdk-go-v2/service/<svc>@v*/api_op_<Op>.go` for `Paginator struct`. Author comments like `// ... uses manual NextToken pagination` flag intentional choice — do not "fix". EC2 has shared helper `ec2PageScan` for paginator-enabled ops; reuse it.

### Smithy API-error-code predicates

`isAPIErrorCode(err, codes ...string) bool` in `aws.go` = single choke point (wraps `errors.As` + `smithy.APIError.ErrorCode()` + `slices.Contains`). Use inline for one-off checks. Wrap in named helper only when reused 3+ times (precedent: `isAccessDenied` wraps 6 codes, 146 callers). Predicates needing code + message-substring match (e.g. `isCacheSecurityGroupsNotPermitted`) stay one-off — outside this helper's shape.

### Migrations

SQL files in `internal/store/migrations/` embedded at compile time via `//go:embed`. Names must be `NNN_description.sql` (e.g. `002_add_foo.sql`). Runner splits on semicolons, executes each statement individually — SQLite's `database/sql` driver silently ignores everything after first statement in multi-statement `Exec`.

### Config and DB path

Viper reads `~/.disco/config.yaml`, env prefix `DISCO_`. `--db` flag (or `$DISCO_DB`) overrides DB path; default `~/.disco/disco.db`. `defaultDBPath()` = pure getter — directory creation is `store.Open()`'s job.

### Rules engine (`internal/rules/`)

YAML or built-in rules evaluated against store by `cmd/check.go`. Rules filter `resources`, emit `Finding`s with severity. Seed rules in `internal/rules/builtin.go`: public S3, unencrypted EBS, SGs open to `0.0.0.0/0:22`, stale IAM keys. Extend by adding to `builtin.go` or authoring YAML + pass `--rules path.yaml`.

### Testing

**Test files exist** for: `internal/store/`, `internal/util/`, all three provider packages.

#### Writing tests for new services

Every new `<service>_resolvers.go` must have matching `<service>_resolvers_test.go`. Pattern:

1. Call `newTestStore(t)` — opens temp-file SQLite DB, inserts required test scan record.
2. Call `upsertTestResource(t, st, provider, accountID, rtype, nativeID, region, attrsJSON)` to insert resources. **Pass region** if resolver uses `sv(r.Region)` to build ARNs — omit makes computed relationship IDs point to phantom resources, FK error no obvious diagnosis. Helper does **not** set `Name`; for resolvers building name-keyed index (e.g. KeyPair by `(region, Name)`), bypass + call `st.UpsertResource(&Resource{..., Name: &name})` direct.
3. Call resolver function direct (tests in same package, e.g. `package aws`).
4. Assert via `st.RelationshipsFrom(id)`.

Always add "no attrs / empty case" test alongside happy-path — guards nil-pointer panics on missing JSON fields.

#### FK constraint: resources require scan record

`resources.discovered_by` + `resources.verified_by` = FKs to `scans(id)`. Any test inserting resources needs scan record in DB first. `newTestStore` handles — inserts scan with fixed ID `"00000000000000000000000000000000"`.

#### UpsertResources ON CONFLICT scope

`UpsertResources` ON CONFLICT only updates: `name`, `status`, `tags`, `attributes`, `verified_at`, `verified_by`. Does **not** update `region`, `zone`, `account_name`, `discovered_at`. Set all fields on initial insert — second upsert can't patch.

#### Registration tests

`internal/providers/<provider>/registration_test.go` holds `expectedAWSServices` / `expectedAzureServices` / `expectedGCPServices` — authoritative list of registered service names. **Update when adding new service scanner.** Test fails if service registered but not listed, or listed but not registered.

## Solution Rules

1. **KEEP THINGS SIMPLE**
2. No reinvent wheel.
3. Comment everything.
4. Human-readable code.
5. No redundant code.
6. Optimize first scan speed, then min memory + CPU.
7. Keep deps minimal.
8. Minimize token use. No re-read source already in context. Use sed, grep, head, tail to cut lines during discovery + implementation.