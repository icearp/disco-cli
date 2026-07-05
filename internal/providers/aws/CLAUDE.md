# CLAUDE.md — `internal/providers/aws/`

AWS scanner + resolver conventions. Cross-provider rules: see `../CLAUDE.md`.

## Resolver conventions

- **Scanner attribute JSON uses PascalCase keys.** `mustJSON` calls `json.Marshal` on AWS SDK v2 response structs, no json tags — `ClusterArn` stays `ClusterArn`, not `clusterArn`. Resolver structs need PascalCase tags (`json:"ClusterArn"`) or silent match nothing on real scan data while tests pass on hand-rolled JSON.
- **ARN helpers** (`aws_arn.go`): `ec2ARN(region, acct, kind, id)` → `arn:aws:ec2:{r}:{a}:{kind}/{id}` (slash sep). `rdsARN(region, acct, kind, id)` → `arn:aws:rds:{r}:{a}:{kind}:{id}` (colon sep). `apigatewayARN(region, path...)` → `arn:aws:apigateway:{r}::/p1/p2/...` (empty account, variadic path joined `/`). `logGroupNativeIDFromName(acct, region, name)` → `arn:aws:logs:{r}:{a}:log-group:{name}` (SDK ARN ":\*" suffix stripped). Synthetic NativeIDs for APIs that issue no ARN: `macieSessionNativeID`, `ssoAssignmentNativeID`, `identityStoreUserNativeID`, `identityStoreGroupNativeID`. Resolvers rebuild target ARN from native ID, pass to `store.ResourceID(...)`. Wrong shape = phantom target, buried FK error.
- **KMS edges**: skip empty `KmsKeyId` / `KMSKeyArn`. AWS-managed default keys unscanned, edge = dangling target. `if sv(attrs.KmsKeyId) == "" { continue }`.
- **`logGroupNativeIDFromName(accountID, region, name)`** — in `logs_scanners.go`, callable from any file in `package aws`. Rebuilds `arn:aws:logs:{r}:{a}:log-group:{name}`. Use instead of `fmt.Sprintf` for NativeID shape consistency.
- **CloudWatch Logs ARN `:*` suffix**: SDK returns `CloudWatchLogsLogGroupArn` with trailing `:*`. Strip via `strings.TrimSuffix(arn, ":*")` before NativeID lookup or edge points to phantom resource.
- **EFS mount target NativeID**: no native ARN. Synthesize: `arn:aws:elasticfilesystem:{region}:{acct}:file-system/{fsid}/mount-target/{mtid}` using `FileSystemId` + `MountTargetId` from `DescribeMountTargets`.
- **KMS grant NativeID**: `ListGrants` returns no `GrantArn` — `GrantListEntry` only has `GrantId`. Synthesize `{keyARN}/grant/{grantId}`. No real `arn:aws:kms:...:grant/...` ARN exists; pattern-matchers keyed on AWS-issued ARNs skip.
- **SSO account-assignment NativeID**: assignments have no AWS-issued ARN. Synthesize `{permissionSetArn}/account/{accountId}/{principalType}/{principalId}` (`ssoAssignmentNativeID` in `sso_scanners.go`). Permission-set ARN already encodes instance id, synthetic carries enough context to dedupe across re-scans.
- **Identity Store user/group NativeID**: Identity Store APIs return no ARN. Synthesize `arn:aws:identitystore::{ownerAccountId}:user/{IdentityStoreId}/{UserId}` and `…:group/…/{GroupId}` (`identityStoreUserNativeID` / `identityStoreGroupNativeID`). `ownerAccountId` from parent SSO instance's `OwnerAccountId`.
- **SSO permission-set ARN → instance ARN**: rebuild via `instanceArnFromPermissionSetArn` in `sso_resolvers.go` — permission-set ARN's `:permissionSet/{ssoins-id}/…` path embeds instance id; canonical instance ARN is `arn:aws:sso:::instance/{ssoins-id}`. `strings.Cut` twice, no Index.
- **AWS Backup plan ARN** uses `backup-plan:`, not `plan:`. Real: `arn:aws:backup:{r}:{a}:backup-plan:{planId}`. Synthetic selection NativeID `{planARN}/selection/{selId}` — trim `/selection/...` in resolver to recover parent plan ARN. Wrong prefix → FK error on closure insert.
- **Org test fixtures**: `loadOrgTargetIndex` keys on attrs JSON `{"Id":...}`, not NativeID. Test rows for `TypeOrganizationsAccount` / `TypeOrganizationsOU` need `{"Id":"<raw-id>","Arn":"<arn>"}` in attrs — bare `{}` leaves index empty, every chained resolver silently emits zero edges with no FK error.
- **Macie session NativeID**: `GetMacieSession` returns no ARN — session is account/region config. Synthesize `arn:aws:macie2:{region}:{acct}:session` (`macieSessionNativeID` in `macie_scanners.go`). Singleton per (account, region); jobs / CDIs / allow-lists hang off it via contains closure.
- **Organizations NativeID = full ARN, not raw ID**. Accounts + OUs keyed by `sv(a.Arn)`, not `o-xxx` / 12-digit account ID. APIs like `ListDelegatedAdministrators` return raw IDs — translate via `loadOrgTargetIndex` (`organizations_resolvers.go`) before building `ResourceID`.

## ELBv2 LB attrs wrapped

Scanner stores LoadBalancer as `{"lb": <LB>, "type": "<kind>"}` (see `elb_scanners.go:109`), not top-level. Resolvers reading `DNSName`, `Scheme`, `VpcId` etc. must unmarshal under `"lb"` key or silent zero values.

## Route53 alias DNS normalization

`AliasTarget.DNSName` carries trailing `.` + (on ELB targets) leading `dualstack.` prefix backend attrs lack. Normalize both before lookup: `strings.TrimSuffix(strings.ToLower(s), ".")` then `strings.TrimPrefix(s, "dualstack.")`. See `normalizeAliasDNS` in `route53_resolvers.go`.

## CloudWatch alarm dimensions — two shapes

Simple alarms: top-level `Namespace` + `Dimensions[]`. Metric-math alarms: nested under `Metrics[].MetricStat.Metric.{Namespace,Dimensions}`. Resolvers must read both or skip half real alarms. See `resolveAlarmDimensions` in `cloudwatch_resolvers.go`.

## Cognito JWT issuer URL

APIGW v2 JWT authorizer `JwtConfiguration.Issuer` shape: `https://cognito-idp.{region}.amazonaws.com/{poolId}`. `strings.Cut` on host/path after prefix strip; rebuild `arn:aws:cognito-idp:{region}:{acct}:userpool/{poolId}` for `store.ResourceID` lookup. Non-Cognito issuers (Auth0, Okta) skip — no phantom edges.

## IAM policy-document parsing

- **`AssumeRolePolicyDocument` + all IAM policy docs URL-encoded JSON** (AWS SDK v2). `url.QueryUnescape` before `json.Unmarshal` or parse silent fail.
- **`Principal.Federated` / `AWS` / `Service` may be string OR `[]string`.** Use custom `UnmarshalJSON` wrapper type (see `principalList` in `iam_resolvers.go`) — bare `[]string` tag only matches array form.
- **`Statement` may be single object OR array.** Same trick as `principalList` — see `statementList` in `iam_resolvers.go`. **`Statement[].Resource`** likewise string-or-array (`resourceList`). `Effect != "Allow"` (Deny / conditional) emits no positive edge.
- **Managed policy doc requires `GetPolicyVersion` fan-out.** `ListPolicies` returns no Document body. `scanIAMPolicies` enriches each row as `{"Policy": ..., "PolicyVersion": ...}`; walker reads `PolicyVersion.Document` for managed, `PolicyDocument` for inline (`GetRolePolicy` etc. already include it).
- **Federated-provider ARN dispatch**: `:saml-provider/` → `TypeIAMSAMLProvider`; `:oidc-provider/` → `TypeIAMOIDCProvider`. Other Federated shapes emit no edges (skip, no dangle).
- **Bare resource names in `Resource[]` skip.** Policy docs carry no region context, synthesizing ARN risks wrong region. Contrast `ecsSecretTarget` (`ecs_resolvers.go`), which DOES synthesize from bare names — task-defs supply region. Same input shape, different rule, different carrier.

## WAFv2 scope pattern

WAFv2 two scopes: `REGIONAL` (per-region) + `CLOUDFRONT` (global). CLOUDFRONT scope reachable only from `us-east-1` — other regions error. Guard with `if region == "us-east-1"` before CLOUDFRONT-scope calls to dodge duplicates.

## FK-safe edge emit when target partially scanned

Resolver targets type with unscanned members (public/Marketplace AMIs, cross-account ARNs, shared snapshots) — build target id set once via `ListResources(Types: []string{TargetType})`, emit edge only if computed target id present. Prevents FK blowup on `UpsertRelationship` + phantom edges. Precedent: `keyPairByNameRegion`, `imageByID` in `ec2_compute_mgmt_resolvers.go`.

## Ownership-filtered AWS scanners

AWS Describe* with ownership filter (`Owners=["self"]` for AMIs/snapshots/FPGA images) — scan self-owned only. Public/Marketplace/shared sets unbounded + not ours to audit (third-party, not AWS-managed — distinct from the `ManagedByProvider` flag in `internal/providers/CLAUDE.md`, which covers AWS-owned catalogue resources like managed prefix lists, IAM AWS-managed policies, IAM service-linked roles, AuditMgr Standard frameworks/controls). Cross-account refs from scanned resources (instance → public AMI) handled via FK-safe lookup above.

## AWS service-integration ARNs use `:::`

Step Functions Definitions + similar carry built-in integration ARNs like `arn:aws:states:::sns:publish` where region+account segments empty. Substring-based ARN dispatchers (`sfnTargetType`, `eventBridgeTargetType`) must filter `strings.Contains(arn, ":::")` before classifying, or emit edges to non-existent resources + blow FK constraints.

## List-then-describe pattern (N+1 avoidance)

AWS service returns only names from List API (EKS, DynamoDB) — describe each resource concurrent via `errgroup` + `sync.Mutex` to collect, then batch upsert. No sequential Describe in loop.

## Provisioned + Serverless flavors → single type

One resource type, not two. Flavor lives as sibling sub-structs (`Provisioned *...`, `Serverless *...`) in native attrs; resolver branches on whichever non-nil. Precedent: `aws:kafka:cluster` MSK, `kafka_resolvers.go` reads subnets/SGs from `BrokerNodeGroupInfo` vs `VpcConfigs[]`. Applies to services modeling variants as parallel fields on same List/Describe response (Redshift Serverless, Aurora Serverless v2, EMR Serverless likely).

## Sparse list entry → store Get body

`List*` returns skeleton (`{Id, Arn, HomeRegion, IsEnabled}`) while edge-bearing fields live on `Get*`/`Describe*` body (e.g. S3 StorageLens `Include.Buckets[]`, `DataExport.S3BucketDestination`). Enrich scanner: fan-out Get per entry, store Get response as `AttributesJSON`. Still "native SDK response verbatim" — Get body IS native. Don't merge List+Get into ad-hoc struct; pick one SDK response + store whole. Precedent: `scanStorageLens` in `s3control_scanners.go`.

## SDK v2 paginator availability per-op

`New<Op>Paginator` exists only for ops AWS models as paginated. Many List ops no paginator — eventbridge, cloudfront Marker ops, wafv2, apigatewayv2 (`GetApis`/`GetAuthorizers`/`GetDomainNames`/`GetApiMappings`), logs (`DescribeAccountPolicies`/`DescribeQueryDefinitions`/`DescribeResourcePolicies`), ec2 (`DescribeVpcEndpointServices`/`DescribeVpcBlockPublicAccessExclusions`), rds `DescribeDBShardGroups`. Before converting manual `NextToken`/`Marker` loop, grep `~/go/pkg/mod/github.com/aws/aws-sdk-go-v2/service/<svc>@v*/api_op_<Op>.go` for `Paginator struct`. Author comments like `// ... uses manual NextToken pagination` flag intentional choice — do not "fix". EC2 has shared helper `ec2PageScan` for paginator-enabled ops; reuse it.

## Smithy API-error-code predicates

`isAPIErrorCode(err, codes ...string) bool` in `aws.go` = single choke point (wraps `errors.As` + `smithy.APIError.ErrorCode()` + `slices.Contains`). Use inline for one-off checks. Wrap in named helper only when reused 3+ times (precedent: `isAccessDenied` wraps 6 codes via `accessDeniedCodes`, 146 callers).

Predicates needing **code + message-substring** match use `isAPIErrorWithMessage(err, code, needle)` (single code) or `isAccessDeniedWithMessage(err, needle)` (any of the six access-denied codes). Both read `ae.ErrorMessage()` directly via `errors.As(&smithy.APIError)`, never `err.Error()` — the match is decoupled from the Smithy `"api error CODE: MSG"` wrapper format and the outer SDK `"operation error <Op>: ..."` wrapping. Use these for AWS exception codes reused across semantically-distinct cases (`AccessDeniedException` for closed-to-customers vs real IAM deny; `ValidationException` for per-region feature gap vs malformed input). Do NOT add new sites that match against `err.Error()` substrings — every site in this package routes through one of these two helpers.

## `skipIfAccessDenied` always returns nil

Single-phase scanners `return 0, 0, skipIfAccessDenied(...)`. Multi-phase scanners (e.g. `scanEventBridge` running buses + rules + connections + api-destinations in one call) cannot early-return — use `_ = skipIfAccessDenied(...); break` to skip denied phase while preserving totals from prior phases. Precedent: `scanEventBridge` phases 3/4.

## Transient errors already wrapped at dispatch

`aws.go` `scanRegion` + phase-1a wrap each `svc.fn` return with `isTransientNetworkError` → `skipIfTransient` (warn + nil). Scanners do NOT need inline handling for DNS (`net.DNSError`), dial/read/write (`net.OpError`), `net.Error` timeouts, or Smithy `RequestTimeout`/`ServiceUnavailable`/`InternalFailure` variants — those warn-skip automatically. Only `AccessDenied` still needs per-scanner `return 0, 0, skipIfAccessDenied(...)` (not wrapped at dispatch because SDK surfaces it mid-paginator, not as top-level svc.fn error).

## ROADMAP vs source drift

Before implementing `ROADMAP.md` NOW item, grep for target `Type*` constants + resolver fn names — items get implemented without roadmap sweep (R1.3 Lambda layers + R1.4 SQS KMS both listed TODO while already shipped). Audit first; move DONE items into COMPLETED section same pass.

## KMS key edge — use `loadKMSResolveIndex` + `resolveKMSKeyID`

Resolver sees KMS ref in four shapes: full key ARN, alias ARN, `alias/foo`, bare key UUID. Build index once per resolver via `loadKMSResolveIndex(acct, st)` (`kms_helpers.go`), then call `idx.resolveKMSKeyID(ref, region, acctID)` per edge — returns `(keyID, ok)` where `ok=false` means target unscanned (skip emit, no FK error). Index also resolves alias name → underlying key ARN, so `alias/aws/foo` refs link to AWS-managed key (which IS scanned — see kms scanner). Don't manually call `kmsKeyTargetARN` + build key ID set + check `alias/aws/` — helper does all three. Precedent: backup, rds, sns, sqs, kinesis, firehose, ssm, config, s3-encryption, kafka, cloudtrail-eds resolvers.

## Wildcard guard runs on canonical resource, not raw ref

Policy `Resource` walkers (e.g. `classifyPolicyResource` in `iam_resolvers.go`) trim object/version/index suffixes before checking for `*?`. `arn:aws:s3:::bucket/*` is real bucket-level grant; only wildcards inside canonical segment (`prod-*` in bucket name, `*` as whole ref) skip. Same for `:function:NAME:*`, `:secret:NAME:*`, `:table/NAME/*`, `:log-group:NAME:*`.

## IAM API has a sustained TPS ceiling near `fanoutMed`

AWS IAM throttles around 10–20 sustained TPS per account. `fanoutMed` (10) is the safe ceiling for any per-resource fan-out (`GetPolicyVersion`, `Get*Policy`, etc.). Bumping to `fanoutHigh` (20) trips `ThrottlingException`, SDK retries with exp backoff, multi-minute hangs across the ~1500-policy AWS-managed catalogue. The proper speedup is `GetAccountAuthorizationDetails` consolidation (single paginated call), not concurrency tuning.

## Service Quotas is opt-in; rate-limiter holds 10 req/s, `sqWorkers`=30, `MaxResults=100`

**Opt-in.** `aws:servicequotas` registers with `serviceEntry.optIn=true`, so a default `disco scan aws` skips it — it reads account *quota limits* (metadata, not resources) and is ~4× the slowest resource scan. `filteredServices(filter, includeOptIn)` excludes opt-in services from the default set; they run only when (a) selected by name (`--services aws:servicequotas`) or (b) `--include-service-quotas` is passed (config `aws.include_service_quotas`, plumbed via the `ServiceQuotasIncluder` capability → `Scanner.includeServiceQuotas` → stamped onto `account.includeServiceQuotas`, mirroring `--scope-regions`). `optIn` is the generic registry knob for "default-off service"; reuse it for any future slow/peripheral scanner.

**Rate.** `servicequotas` `ListServiceQuotas` and `ListServices` are each **10 req/s (steady) + 10 burst, per region per account** (overall account cap 50 req/s — https://docs.aws.amazon.com/servicequotas/latest/userguide/reference_limits.html). The scanner enumerates **all** service codes via `ListServices`, then paces every `ListServiceQuotas` request through a **per-region `rate.Limiter(sqReqPerSec=10, sqBurst=10)`** (`golang.org/x/time/rate`) — one `pacer` (the shared helper in `aws_ratepace.go`, built via `newPacer`) per `scanServiceQuotas` call, since the harness dispatches per region. **The limiter, not the worker count, holds the ceiling.** A fixed semaphore of N workers delivers `N / latency` req/s, so a cap of 10 only reached 10 req/s at ~1s latency; at the real ~1.5–2.5s control-plane latency it under-utilized the bucket (~4–6 req/s, half idle — the regression this design fixes). The fan-out is therefore sized at `sqWorkers` (30) — comfortably above `rate × worst-case-latency` — so concurrency always keeps the limiter fed, and the limiter caps throughput at exactly 10 req/s with no overshoot (so no adaptive-retryer thrash). Don't "optimize" `sqWorkers` back down toward 10 — that re-couples throughput to latency. `ListServices` (~4 calls) is left ungated: separate 10 req/s bucket, burst covers it. Always pass `MaxResults: sdkaws.Int32(100)` (the API max) on both list inputs so the page count stays near one-per-service. `maxConcurrentRegions=5` puts account-wide load at 5×10 = 50 req/s, i.e. *at* the 50 req/s overall-account cap (each region's per-region bucket is independent and the pacer holds each at exactly 10, so there is no overshoot — but the headroom the old `=4` (40 req/s) left is now gone; a sustained `--regions all --include-service-quotas` run leans on adaptive retry to absorb any burst). servicequotas is opt-in, so the default scan never hits this. Set `DISCO_SCAN_RATE_DEBUG=1` to emit a one-line `N calls in Ts = R req/s` saturation report per region.

## CloudWatch Logs phase-2 fan-out

`scanLogs` in `logs_scanners.go` runs in two phases. Phase 1 is the independent surface (log groups, account policies, deliveries, metric filters, etc.) executed sequentially. Phase 2 is per-log-group enrichment — `DescribeLogStreams`, `DescribeSubscriptionFilters`, `GetTransformer`. The three phase-2 sub-scanners are launched **concurrently** via `sync.WaitGroup`; they hit independent CloudWatch Logs APIs whose 5 TPS quotas are documented as **per log group** (https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/cloudwatch_limits_cwl.html), so concurrent calls to N distinct groups consume N independent buckets. Within one group the SDK paginator is sequential, so per-group TPS stays ≤ 1.

Per-group fan-out inside each sub-scanner uses `fanoutMed` (10), not `fanoutLow`. Account-wide pressure is absorbed by adaptive retry (`aws_config.go` `RetryModeAdaptive` + `RetryMaxAttempts(10)`); `ThrottlingException` is dispatch-level transient (see "ThrottlingException is dispatch-level transient" above). The phase-2 dispatcher loads the region's log-group set ONCE (`loadLogGroupsForRegion`) and passes the slice into each sub-scanner — three duplicate `ListResources` queries removed.

Errors from phase-2 sub-scanners are gathered and `errors.Join`-ed before propagating; one failed sibling does not cancel the others (matches `aws.go::scanRegion` "Errors never abort scan"). For users who don't need log-stream inventory, two existing escape hatches stand: `disco scan aws --services aws:ec2,aws:s3,...` (omit `aws:logs`) skips the service entirely; `disco resources --exclude-types aws:logs:log-stream` mutes streams from queries while keeping them in the DB (`cmd/CLAUDE.md`).

**`UploadSequenceToken` is dropped as volatile.** `DescribeLogStreams` returns a fresh, deprecated `UploadSequenceToken` on every call regardless of log activity — left in `AttributesJSON` it version-splits every log stream on every scan (a 907-stream account reports `907 changed` each scan). `aws_volatile.go` registers it with `volatile.Register` so the store drops the key before the version comparison; see `internal/providers/CLAUDE.md` "Declaring volatile-field rules". `LastEventTimestamp` / `LastIngestionTime` (real ingestion) and the log-group `StoredBytes` are kept — they reflect genuine change.

## IAM scan uses GetAccountAuthorizationDetails (single paginated call)

`scanIAMAuthDetails` (`iam_scanners.go`) consolidates users + roles + groups + managed policies (Local + AWS scope, including each policy's default version `Document`) + every principal's inline policies into one paginated `iam:GetAccountAuthorizationDetails` (GAAD) call with `MaxItems=1000` + a `Filter` listing all five entity types.

**GAAD `AWSManagedPolicy` filter only returns *attached* AWS-managed policies, NOT the full catalogue.** `scanIAMAuthDetails` runs `scanIAMAWSManagedCatalogue` first — a stub `ListPolicies(Scope=AWS)` pass that upserts a metadata-only row per AWS-managed policy (no `GetPolicyVersion` enrichment, avoids the throttling fan-out). The subsequent GAAD pass overwrites attached managed-policy rows with the version-enriched form; unattached catalogue policies keep their stub row as FK targets for `resolveManagedPolicyAttachments`. Walker silently skips no-document rows. Don't drop the catalogue stub pass — without it, unattached AWS-managed policies disappear from the store. Replaces the pre-GAAD shape (separate `ListRoles` / `ListUsers` / `ListGroups` / `ListPolicies` + per-policy `GetPolicyVersion` fan-out + per-principal `ListRolePolicies` / `ListUserPolicies` / `ListGroupPolicies` + per-name `Get*Policy`). Don't reintroduce those — the old fan-outs hit IAM TPS throttling and dominate scan wall-time. AWS-managed policies detected via the `arn:aws:iam::aws:` ARN prefix (canonical scope marker; GAAD doesn't expose the per-policy scope flag). Inline policies still upsert as `aws:iam:{role,user,group}-policy` rows with NativeID `{parentARN}/policy/{name}` so existing inline-policy resolvers stay unchanged. Independent IAM APIs not covered by GAAD (`ListInstanceProfiles`, `ListOpenIDConnectProviders`, `ListSAMLProviders`, `ListServerCertificates`, `ListVirtualMFADevices`, `ListAccessKeys`) keep their own scanners.

## Phase 1 globals + regionals run concurrently

`scanAccount` (`aws.go`) launches global services and per-region fan-out into a single `WaitGroup` — no barrier between them. Don't reintroduce the `wg0.Wait()` between phases: scanners only upsert (no reads); resolvers in phase 2 are the readers and gate behind the combined wait. Slow globals (IAM with its managed-policy catalogue enrichment) blocked the entire regional fleet under the old gated shape.

## Per-call concurrency constants

`concurrency.go` exports `fanoutHigh` (20), `fanoutMed` (10), `fanoutLow` (2) for `semaphore.NewWeighted(...)` inside scanner/resolver fan-out loops. Distinct from `maxConcurrentServices` (`aws.go`) which caps top-level service scanners. Do not redeclare `const maxConcurrent` inside individual scanners — pick fanout tier.

## Rate-paced fan-out (`aws_ratepace.go`)

The `fanout*` tiers cap **concurrency**, not **rate** — fine for latency-bound scanners (a handful of calls each). When a fan-out makes a **high call count against a low *documented* per-second API limit** with non-trivial control-plane latency, a fixed semaphore of N under-fills the budget (`N ÷ latency` req/s < the API ceiling). For that one profile, use the shared `pacer` (`newPacer(rps, burst)` + `pacer.wait(ctx)`; optional `reportRateDebug` under `DISCO_SCAN_RATE_DEBUG`): size the worker semaphore **above** `rate × worst-case-latency` so the limiter — not the workers — holds the ceiling. **Do NOT reach for a pacer on ordinary scanners** — it only ever slows them. Sole user: `scanServiceQuotas`.

**Audit (2026-06): no other scanner currently fits.** A full sweep of AWS fan-outs found none that clear all four bars (high cardinality + shared low TPS + latency under-fill + currently semaphore-bound):
- **CloudWatch Logs phase-2** (`DescribeLogStreams` / `DescribeSubscriptionFilters` / `GetTransformer`) — **not a fit**: the 5 TPS limit is **per log group**, so concurrent calls across N groups hit N independent buckets; a shared pacer would needlessly serialize them. Concurrency across parents is already the correct lever.
- **S3 per-bucket config** (`GetBucketEncryption` / `GetBucketPolicy`) — **not a fit**: per-bucket throttle, same per-parent reasoning.
- **`resolveManagedPolicyAttachments`** (`ListEntitiesForPolicy`) — **low cardinality**: its `ListResources` call omits `IncludeManaged`, so it fans only over **customer-managed** policies (the ~1500 AWS-managed catalogue rows are `ManagedByProvider:true`, excluded), typically well under 100. Not high-call-count.
- **`resolveUserGroupMemberships`** (`ListGroupsForUser` per user) — **consolidation, not pacing**: GAAD's `UserDetail` already carries `GroupList` (see `scanIAMAuthDetails`), so the per-user fan-out is removable, not something to pace.
- **`scanIAMAccessKeys`** (`ListAccessKeys` per user) and the unbounded `pageScanConcurrent` list-then-describe sites (`scanSNSTopics`, `scanDynamoDBTables`) — **measurement-gated maybes only**: shared TPS unconfirmed and cardinality varies by account; convert only if a live `DISCO_SCAN_RATE_DEBUG=1` run on a large account shows the bucket under-filled.

So before adding a pacer anywhere, prefer call consolidation (per the IAM-TPS section above, GAAD-style folding usually beats concurrency/rate tuning), and require a live A/B that proves a win.

## Service-not-enabled → markServiceDisabled sentinel

Some AWS APIs return distinct exception code when account has not subscribed to / activated feature — Shield Advanced (`ResourceNotFoundException` via `isShieldNotSubscribed`), Security Hub (`InvalidAccessException` / `ResourceNotFoundException` via `isSecurityHubNotEnabled`). Phase-1 detection step returns `markServiceDisabled(err)` (`aws.go`) instead of nil; dispatch loop in `scanRegion` / `scanAccount` detects sentinel via `errors.Is(err, errServiceDisabled)`, suppresses warning + error reporting, surfaces `(account: disabled)` on per-service progress line. New service-disabled helpers follow this shape.

**Macie variant — code+message disambiguation.** Macie collides on `AccessDeniedException` for both real IAM denial AND not-enabled. `isMacieNotEnabled` in `macie_scanners.go` adds `strings.Contains(err.Error(), "Macie is not enabled")` check on top of `isAccessDenied`. Real IAM deny on `GetMacieSession` falls through to `skipIfAccessDenied`, preserving warning signal. Precedent: `isCacheSecurityGroupsNotPermitted` (code+message matcher per "Smithy API-error-code predicates" above). Audit Manager follows same shape via `isAuditManagerNotEnabled` (matches "complete AWS Audit Manager setup").

**Control Tower variant — multi-code disambiguation.** Some services surface not-enabled state under MULTIPLE error codes depending on missing prerequisite. Control Tower returns `AccessDeniedException` when calling account is not org management account AND `ValidationException` when `AWSControlTowerAdmin` role missing — both mean "not deployed here". `isControlTowerNotEnabled` first matches message hint (`AWSControlTowerAdmin` / `landing zone` / `management account`), then accepts EITHER `isAccessDenied(err)` OR `isAPIErrorCode(err, "ValidationException")`. Message check first short-circuits real validation errors (malformed input, etc.) sharing code but not not-enabled semantics. Same `markServiceDisabled` sentinel + `(account: disabled)` progress line as single-code variants.

**Multi-phase orchestration.** Only phase-1 detection step (`DescribeSubscription` / `DescribeHub` / `GetMacieSession`) returns sentinel. Phases 2+ keep per-phase tolerant `isAccessDenied` skip — handles rare partial-IAM-grant case where subscription detectable but list APIs fail. Top-level scanner (`scanShield` / `scanSecurityHub` / `scanMacie`) propagates sentinel via existing `if ferr != nil { return 0, 0, ferr }` chain, halting downstream phases naturally.

**Gate-only phase-0 (no upsert).** Account/region config that gates a service but isn't an ARN'd resource uses `gateXxx(ctx, client, acct, st) error` — calls Describe purely for disabled-sentinel side-effect (`markServiceDisabled` on not-subscribed, nil otherwise). No `store.Resource` built. Distinct from "Multi-phase orchestration" above, which DOES upsert the phase-1 detection row. Precedent: `gateShieldSubscription` in `shield_scanners.go`.

**Standalone-account variant — AWSOrganizationsNotInUseException.** `organizations:DescribeOrganization` returns this code when the calling account has never joined an Organization. Detected at the top of `scanOrganizations` via `isAPIErrorCode(err, "AWSOrganizationsNotInUseException")` and routed through `markServiceDisabled`. Surfaces `(account: disabled)` with no warning, matching the Shield `ResourceNotFoundException` / SecurityHub `InvalidAccessException` shape — not multi-phase, single detection point.

**Member-account variant — DescribeOrganization probe.** Organizations List* ops (`ListRoots` / `ListAccounts` / `ListPolicies` / `ListOrganizationalUnitsForParent`) reject member-account calls with opaque `AccessDeniedException`. `DescribeOrganization` succeeds from any member and exposes `MasterAccountId`. `scanOrganizations` upserts the `aws:organizations:organization` row first (member-account scans still get the org metadata), then probes: if `sv(org.MasterAccountId) != acct.ID`, returns `(total, inserted, nil)` — service ran successfully on the member, just skipping the management-only phases. Returning `nil` (not `markServiceDisabled`) keeps the per-service progress line accurate (`(1 total, 1 new)` instead of a misleading `(account: disabled)` suffix that would also zero the counts via the dispatch-level handler in `aws.go`). Replaces N AccessDenied warnings from the management-only phases. Precedent: `organizations_scanners.go`.

**`errServiceDisabled` zeroes total/new/changed at dispatch.** The dispatch handlers in `aws.go` (`scanRegion`, phase-1a) call `ReportService(svc.name, scope, 0, 0, 0, 0, store.ServiceDisabled)` when `errors.Is(err, errServiceDisabled)` matches — `total` from the scanner and the bound new/changed counters are discarded. If the scanner did partial work (e.g. upserted one row before short-circuiting the rest of the phases), return `(total, inserted, nil)` instead of `markServiceDisabled(...)`. Use the sentinel only when zero rows were produced. Precedent: member-account path in `scanOrganizations`.

**Shared not-enabled predicate across services.** When two services gate on the same account-level enablement (e.g. Cost Explorer access blocks both `aws:ce` and `aws:bcmpricingcalculator`), declare one predicate (`isCostExplorerNotEnabled` in `bcmpricingcalculator_scanners.go`) and have both scanners' dispatcher call it before falling through to `skipIfAccessDenied`. Avoids drift between two near-identical message matchers.

## Single-region globals → `global: true`, hardcode home in client

AWS exposes some services with account-wide scope but a single regional endpoint (IAM, Route53, CloudFront, Globalaccelerator us-west-2, Budgets us-east-1, Route53 Recovery us-west-2, Route53 Global Resolver us-east-2, etc.). Register these with `global: true` on the `serviceEntry`; the dispatcher (`aws.go::scanAccount`) calls them once per account with `region=""`, regardless of `--regions`. Inside the scanner, hardcode the home in the SDK client option-fn:

```go
region := "us-west-2"
client := globalaccelerator.NewFromConfig(acct.cfg, func(o *globalaccelerator.Options) { o.Region = region })
```

Use **short var decl `region := "X"`**, not `const region = "X"`. Untyped string constants are not addressable, so `&region` for `Region: *string` in resource batch fields fails to compile (`cannot take address of region (untyped string constant ...)`). The short-decl form is gofmt-stable and lint-clean.

Substituting the home in a local variable (rather than relying on the dispatcher arg) keeps `Resource.Region`, error scopes, and `skipIfAccessDenied` reports accurate. Precedents: `route53_scanners.go`, `globalaccelerator_scanners.go`, every `*_scanners.go` flipped in the R0 single-region globals sweep.

**Anti-pattern (deprecated):** registering a single-region global as **regional** with an inline `if region != "<home>" { return 0, 0, nil }` early-return. The pattern was historically used to dodge per-region NXDOMAIN warnings, but `isDNSNotFound` at the dispatcher (`aws.go`) already silent-skips those. The inline-gate pattern silently produces zero rows when `--regions` excludes the home (`disco scan aws --regions us-east-1` would skip globalaccelerator entirely). Convert to `global: true` instead.

Inline `if region != "X"` gates remain correct only for **sub-op** cases — a regional service with one specific op pinned to a single region (Lightsail Distributions/Domains in us-east-1, ECR registry-scanning in us-east-1, Direct Connect Gateway in us-east-1). See "Per-op region gates" below.

CLI: `--skip-globals` (defined in `cmd/scan.go`, plumbed via `providers.GlobalsSkipper`) suppresses every global service regardless of registration shape — for data-residency / per-region audits.

## `--services` flag values come from `name:` field, not file stub

The string passed to `disco scan aws --services X` must match the `name:` field of the corresponding `registerService(serviceEntry{...})` call, not the file basename. Drift examples: `costexplorer_scanners.go` → `aws:ce`; `supportapp_scanners.go` → `aws:support-app`; `licensemanager_scanners.go` → `aws:license-manager`; `networkmanager_scanners.go` → `aws:networkmanager` (no dash); `notificationscontacts_scanners.go` → `aws:notifications-contacts`. Authoritative listing: `grep -hE 'name: *"aws:' internal/providers/aws/*_scanners.go | sort -u`.

## Multi-phase parent + children closure-wiring helper

Scanners modeling per-(acct,region) singleton parent with N child phases (Macie session + jobs/CDIs/allow-lists, Security Hub hub + insights/standards/product-subs) factor closure-wiring into one `upsertXChildren(st, parentARN, acct, batch, kind)` helper. Helper does `UpsertResources(batch)` + `RecordHierarchyBatch([][2]string{{child.ID, parentID}})` together. Don't inline per phase — three+ duplicated copies of same closure-pair build = sign to extract. Precedent: `upsertMacieChildren` (`macie_scanners.go:343`), `upsertSecurityHubChildren` (`securityhub_scanners.go`).

## Region-scoped FK-safe id sets

When target NativeID not deterministic per (acct, region) (e.g. multiple `aws:guardduty:detector` rows per region; `aws:config:configuration-recorder` arbitrary name), use `scannedIDsByRegion(acct, st, type) → map[region][]resourceID` instead of flat id set. Singleton-per-region services (`aws:macie:session` via `macieSessionNativeID`) keep flat `scannedIDSet`. Both helpers in `securityhub_resolvers.go`. Same FK-safety guarantees as flat-set pattern; emits one edge per scanned target in region rather than guessing NativeID.

## Multi-phase scanner totals

Each phase returns `(total, inserted, err)`. `total = len(batch)` (rows scanned), `inserted = n` from `UpsertResources` (rows newly inserted, excludes upserts of existing). Never return `len(batch)` for both — the scan-progress line's `total` column reports nonsense on rescans otherwise.

The progress line's **new** / **changed** columns no longer come from the returned `inserted`: the dispatcher (`aws_scanner.go::scanRegion` / `scanAccount`) binds `st.WithUpsertCounters(&newC, &changedC)` around each `svc.fn` call and reads those counters for `ReportService(..., total, new, changed, errCount, disabled)`. `UpsertResources` bumps `newC` on a first-discovery and `changedC` on a version split. So a re-scan of an unchanged account that genuinely re-versions a row (e.g. IAM `RoleLastUsed.LastUsedDate` ticked, Logs `StoredBytes` grew) reports `0 new, N changed` rather than a misleading `N new`. Scanners still return `(total, inserted, err)` — the returned `inserted` is now vestigial for reporting, but `total` and `err` still matter, so keep the signature.

## Cross-service ResourceArn ≠ scanner NativeID shape

Security-overlay services (Shield, GuardDuty findings, Inspector findings) emit `ResourceArn` refs in canonical AWS shape that may differ from per-service scanner's NativeID shape. Example: Shield emits EIP as `arn:aws:ec2:{r}:{a}:eip-allocation/eipalloc-xxx`, but `ec2_networking_scanners.go` stores `arn:aws:ec2:{r}:{a}:elastic-ip/eipalloc-xxx`. Normalise at classify time (`strings.Replace(arn, ":eip-allocation/", ":elastic-ip/", 1)`) before `store.ResourceID` lookup, or every edge silently FK-drops. See `classifyShieldProtectedResource` in `shield_resolvers.go`.

## CFN `PhysicalResourceId` shape varies per ResourceType

Adding entries to `cfnTypeMap` (`cloudformation_resolvers.go`): full ARN for some (Lambda, SNS, ELBv2, SFN, SecretsManager, Lambda layer), bare name/ID for others (S3, IAM, EC2 *, KMS key, DynamoDB, Logs, ECR, Kinesis, RDS, EFS, EventBridge), queue URL for SQS, ID-only for APIGW. Verify shape against CFN resource-ref docs per type — wrong synth = phantom NativeID, FK-safe lookup silently drops edge with no error. Custom-bus EventBridge rules cannot resolve from physID alone (no bus context); reject pipe-form `BUS|NAME` rather than synth wrong ARN.

## Adding new AWS SDK service module

`go get github.com/aws/aws-sdk-go-v2/service/<svc>@latest` then `go mod tidy`. Service modules version-independent of base SDK; no pin needed.

## Coverage upstream = CloudFormation ∪ Service Reference

`coverageProvider.Fetch` (`aws_coverage.go`) returns the **union** of two catalogs, because neither is complete:

- **CloudFormation ListTypes** (`Visibility=Public, Type=Resource`) — needs AWS creds; lists only resources with a CFN provider. Misses SDK-real resources like DynamoDB streams, AuditManager controls, IdentityStore users, Macie classification jobs, service quotas.
- **AWS Service Reference** (`aws_servicereference.go`) — the credential-free public JSON form of the IAM Service Authorization Reference (`https://servicereference.us-east-1.amazonaws.com/`, ~451 services, ~2250 resource types). Supplies the CFN-absent reals above, but itself omits real CFN-modeled resources (e.g. SecurityHub `insight`, `delegated-admin`).

Service Reference entries are synthesized into the same `AWS::<service>::<resource>` shape disco's `Aliases()` / `AlgorithmicKey` already target, so `coverage.Build` unions + dedupes the overlap case-insensitively with no extra machinery. Both fetches are **fatal on failure** (the `errCoverageRegistryUnreachable` → exit-2 contract) — the union requires both, so a partial fetch can't silently re-introduce false `upstream-missing`. SR service segments are lower-cased (`macie2`, `dynamodb`); where disco's segment differs from SR's (disco `macie` vs SR `macie2`) an explicit alias bridges it, otherwise the algorithmic key matches. `TestAWSResourceMirrorsUpstream` ratchets the resource-segment naming against the alias map.

Latency: the SR fan-out is ~451 small credless GETs at concurrency 32, ~1.3s wall. Caching via the index `modified` stamps is a possible follow-up if it regresses.

## CFN type ≠ SDK op

CloudFormation registry exposes resource types ahead of (or independent of) the SDK — e.g. `AWS::EC2::TransitGatewayMeteringPolicy` / `…MeteringPolicyEntry` exist in CFN with no `aws-sdk-go-v2/service/ec2` ops backing them. Per the per-service-API mandate in `internal/providers/CLAUDE.md`, disco scans only via SDK clients — CFN-only types are not scannable. Before scoping a scanner from a roadmap entry or coverage gap, `grep -r <FeatureName> $(go env GOMODCACHE)/github.com/aws/aws-sdk-go-v2/service/<svc>/` to confirm SDK support exists; if empty, defer rather than half-implementing via CFN.

## ECR image identifier → repository ARN

Services referencing container images by image-URL (App Runner `ImageRepository.ImageIdentifier`, ECS task-def `ContainerDefinitions[].Image`, etc.) carry URL form `{acct}.dkr.ecr.{region}.amazonaws.com/{repo}[:tag]`. Strip tag suffix (`strings.LastIndexByte ':'`), parse host into `{acct}` + `{region}` via `.dkr.ecr.` and `.amazonaws.com` cuts, then reconstruct `arn:aws:ecr:{region}:{acct}:repository/{repo}` for canonical NativeID lookup. `public.ecr.aws/...` and other registries (`docker.io/...`, `quay.io/...`) skip — no edge. Helper precedent: `apprunnerImageToRepoARN` in `apprunner_resolvers.go`. Multi-segment repo names (`team/myapp`) preserved.

## RDS-shaped engines: shared API vs dedicated API

Neptune AND DocumentDB each have dedicated SDK service (`aws-sdk-go-v2/service/neptune` / `.../docdb`) with own scanners (`aws:neptune:*` / `aws:docdb:*` types). Neptune *also* surfaces via `rds:DescribeDBClusters` (`Engine=neptune`); DocumentDB does NOT (separate API endpoint). To prevent duplicate rows when scanning same physical Neptune cluster under both `aws:rds:db-cluster` AND `aws:neptune:db-cluster`, RDS scanner filters `Engine ∈ {neptune, docdb}` via `nonRDSEngines` in `rds_scanners.go`. Add engine to `nonRDSEngines` whenever adding dedicated scanner conflicting with shared RDS API. Verify by checking dedicated SDK's `api_op_CreateDBCluster.go` `Engine` valid-values list AND probing `rds:DescribeDBClusters` behaviour in test account. (Both Neptune and DocDB *ARN prefixes* use `arn:aws:rds:` — historical artefact predating API split.)

## List returns entry, Get rejects it

Some AWS services surface implicit/managed entries in `List*` responses but reject matching `Get*`/`Describe*` call (Athena `ListDataCatalogs` returns `AwsDataCatalog` — implicit Glue catalog — but `GetDataCatalog` raises `InvalidRequestException: ... was not found`). Tolerate per-item via same pattern as `AccessDenied`: skip row, preserve sibling totals. `isAPIErrorCode(derr, "InvalidRequestException")` (or service-specific code) alongside `isAccessDenied(derr)` in per-item branch. Don't blanket-tolerate at phase level — real not-found / malformed-input still surface for normal entries.

## Scanner iface lift (testability)

Split each scanner into `scanX(ctx, acct, region, st, scanID)` (concrete client wiring) + `scanXEntities(ctx, client xAPI, ...)` (testable body) + narrow `xAPI` interface listing only the SDK methods called. `*svc.Client` satisfies the interface; tests inject stubs. Method signatures preserve the SDK's variadic `...func(*svc.Options)` so SDK paginators continue to compile against the interface.

**Multi-fn shortcut**: when sub-phases already take `*svc.Client`, lift in one shot — declare iface above `init()` then `sed -i 's|client \*svc\.Client|client svcAPI|g' <file>`. Sub-fn signatures + paginator constructors continue to compile.

**Method enumeration**: `grep -oE "(client|c|gctx)\.[A-Z][a-zA-Z]+|<svc>\.New[A-Z][a-zA-Z]+Paginator" file | grep -v "^c\." | sort -u` produces the exact iface method set.

**Footgun — duplicate client line**: when introducing a `scanXBody(ctx, client xAPI, ...)` wrapper around a single-fn scanner, also DELETE the original `client := <svc>.NewFromConfig(...)` line that lived inside. Forgetting it causes "no new variables on left side of :=" because the wrapper now passes the client in. Move the construction up into scanX, leave only logic in the body.

**Skip criteria**: in practice, sub-phase per-region clients (s3, s3control) and multi-SDK scanners (cognito, sso) are LIFTABLE — see precedents below. The only true defer is when a refactor exceeds a single-PR review budget.

**Dispatcher function-type aliases**: when a scanner uses local `type perFnScanner func(..., *svc.Client, ...) (...)` aliases (lambda, ssm, rds), the sed propagation also needs to update those alias declarations from `*svc.Client` to `svcAPI`. Build error otherwise: "cannot use scanX as perFnScanner value".

**Sub-fn-owned client construction (cloudwatch pattern)**: when each sub-fn builds its own region client (no `client *svc.Client` parameter), the lift requires (1) constructing the client in the dispatcher and (2) deleting the per-sub-fn `client := <svc>.NewFromConfig(...)` line in every sub-fn. `sed -i '/^\tclient := <svc>\.NewFromConfig/d' <file>` handles step 2 across the whole file. Run the sed BEFORE re-adding the dispatcher's `client := ...` — the dispatcher's line matches the same pattern and would also be deleted.

**Shared package-level iface (ec2 pattern)**: services with 10+ scanner files and 50+ ops (only EC2 today) get ONE shared `<svc>API` in a dedicated `<svc>_iface.go` rather than per-file ifaces. New ec2 op needed by a scanner? Add the method to `ec2_iface.go` AND verify the SDK has it (`go doc github.com/aws/aws-sdk-go-v2/service/ec2.Client.<Op>`).

**Paginator iface trick**: `New<Op>Paginator(client, ...)` constructors only require the underlying `<Op>` method on `client`, not a per-paginator interface. Listing the underlying `DescribeXxx` / `ListXxx` / `GetXxx` / `SearchXxx` op on `<svc>API` satisfies every paginator constructor that wraps it.

**Multi-SDK service (cognito + sso pattern)**: when a scanner spans two SDK packages (Cognito = `cognitoidentityprovider` + `cognitoidentity`; SSO = `ssoadmin` + `identitystore`), declare two distinct ifaces. Wrapper constructs both clients then forwards to a `scanXAll(ctx, client1, client2, ...)` body. Don't merge into one mega-iface — each stays narrow.

## Cross-account member-row → org account edges

Services that model multi-account membership (Inspector v2, Detective, GuardDuty, future SecurityHub member, Macie member) get a per-member resource type (`aws:<svc>:member`) and a resolver that emits `attached-to` → `aws:organizations:account` via `loadOrgTargetIndex`. Members are scanned even when the org tree is not — the resolver short-circuits when the index is empty (no edges, no error). Precedent: `inspector_resolvers.go::resolveInspector2MemberOrgAccount`, `guardduty_resolvers.go::resolveGuardDutyMemberOrgAccount`. Member NativeID shape: `arn:aws:<svc>:{r}:{a}:detector/{id}/member/{memberAcctId}` (or analogous synthetic when no AWS-issued ARN exists).

## Per-target embedded fan-out

When a parent resource references N children that have no independent lifecycle (Control Tower baseline → enabled-controls per OU, Backup plan → selections, EventBridge rule → targets), fetch children at scan time and embed under a key in the parent's `AttributesJSON` (`{"Baseline": ..., "EnabledControls": [...]}`). Per-target AccessDenied / ValidationException tolerated via `skipIfAccessDenied` — never propagate per-target errors during fan-out, or one missing OU breaks the whole baseline upsert. Precedent: `controltower_scanners.go::listEnabledControlsForTarget`.

## Multi-hop role chaining

`accountCfg.RoleChain []string` (preferred) walks N assume-role hops in order — each step's STS client is built from the prior step's `CredentialsCache`. `RoleARN` (single string) remains the single-hop path; `role_chain` takes precedence when both are set. Helper: `chainAssumeRoles(baseCfg, []string)` in `aws_config.go`. Use for hub-and-spoke org topologies where the runner must hop through an Audit role before reaching the target account.

## Tag JSON helpers

`awsTagsJSON[T awsTag]` (`aws.go`) is generic-union restricted. New SDK service tag types (`sesv2types.Tag`, `lakeformationtypes.Tag`, etc.) must be added to `awsTag` union AND new `case` in `switch tt := any(t).(type)` block — both edits or helper drops tags silently. For map-typed tags (Macie `map[string]string`, ECR repo tags map) use `mapTagsJSON` instead. Defer tag plumbing if scope tight; tags rarely block graph analysis and adding union touches global type list.

## Helper-test colocation

Cross-cutting pure-helper tests (ARN builders, error predicates, tag helpers, transient classifier) live in `aws_arn_test.go`, `aws_errors_test.go`, `aws_tags_test.go`. Per-service helper tests (e.g. `apprunnerImageToRepoARN`, `instanceArnFromPermissionSetArn`) live in the matching `<svc>_resolvers_test.go`. Before adding a new helper test, grep `^func Test<Helper>` across `aws/*_test.go` — duplicate `TestX` in same package fails to compile.

## Smithy GenericAPIError string shape

`(&smithy.GenericAPIError{Code:"AccessDenied",Message:"denied"}).Error()` = `"api error AccessDenied: denied"`. Production code paths in this package no longer match against `err.Error()` (every code+message predicate routes through `isAPIErrorWithMessage` / `isAccessDeniedWithMessage`, both reading `ae.ErrorMessage()` directly). Only `ScanWarning.Message` still surfaces the wrapped form via `skipIfAccessDenied` — tests asserting on `ScanWarning.Message` must include the `api error ` prefix (precedent: `TestSkipIfAccessDenied_RecordsWarningReturnsNil`).

## SDK middleware test stubs — placement

When stubbing SDK responses via `Stack.Initialize.Add(...)`, place with `smithymw.After` not `Before`. `RegisterServiceMetadata` is itself an Initialize middleware that populates op-name + service-id in ctx; a `Before` stub short-circuits ahead of it and `awsmw.GetOperationName(ctx)` returns `""`. Precedent: `middleware_testhelper_test.go` `stubResponses`.

## Resource-policy SourceArn lives in Condition, not Resource

Lambda function/layer permission policies, S3 bucket policies, SNS topic policies, SQS queue policies and similar resource-based policies put the principal target in `Condition.{ArnLike|ArnEquals|StringLike|StringEquals}["AWS:SourceArn"]`, NOT in `Statement[].Resource`. The IAM policy walker in `iam_resolvers.go` (`policyStmt` struct) only exposes Effect+Resource — don't extend it for resource-policy consumers; declare a focused per-resolver stmt type (`lambdaPermStmt` in lambda_resolvers.go) plus a string-or-array stmt-list wrapper. Condition values are `string` OR `[]string` (use `json.RawMessage` + try array first, fall back to single). Operator + key match is case-insensitive per IAM rules — `strings.EqualFold` on key, `strings.ToLower` on operator. Reuse `classifyPolicyResource` to dispatch the SourceArn to a scanned target.

## Embed SDK type to enrich list-shape attrs

When `List*` attrs need a sibling field from `Describe*`/`Get*` (e.g. Lambda `Code.ImageUri` from `GetFunction`, only on `PackageType=Image`), define a wrapper struct that EMBEDS the SDK list-shape type:

```go
type lambdaFunctionAttrs struct {
    lambdatypes.FunctionConfiguration       // embedded — fields stay top-level on marshal
    Code *lambdaFunctionCodeAttrs `json:"Code,omitempty"`
}
```

Embedding (not nesting) flattens the SDK fields back to top level so existing resolvers reading `Role`/`KMSKeyArn`/`VpcConfig` keep working. Sibling key (`Code` here) carries the enrichment. Cheaper than the "Re-upsert parent with Describe body via ON CONFLICT" pattern when only one or two fields are needed and the Describe call is conditional. Precedent: `lambdaFunctionAttrs` (lambda_scanners.go).

## ARN slot indexing after `strings.Split(arn, ":")`

For colon-separated ARNs `arn:aws:<svc>:<region>:<acct>:<rtype>:<id>` Split returns 7 parts: `[0]=arn [1]=aws [2]=svc [3]=region [4]=acct [5]=rtype [6]=id`. Slash-separated ARNs (`...:rtype/id`) keep `[5]="rtype/id"`. When dispatching by resource-type segment, compare `parts[5] == "cluster"` (exact) — `strings.HasPrefix(parts[5], "cluster:")` always fails because Split already consumed the colon. Precedent: `lambdaESMSourceType` DocDB branch (lambda_resolvers.go).

## Wrapper-key json tags are lowercase by design

The "Scanner attribute JSON uses PascalCase keys" rule applies to fields produced by `json.Marshal` on raw SDK structs. Hand-built wrapper containers that namespace the SDK payload (`{"lb": <LB>, "type": ...}` in `elb_scanners.go`, `{"rule": ..., "Targets": [...]}` via `ruleWithTargets` in `eventbridge_scanners.go`, `{"listenerArn": ..., "cert": ...}` in `elb_scanners.go`) deliberately use lowercase / camelCase outer keys to distinguish them from SDK fields. Resolver struct tags like `json:"lb"` / `json:"listenerArn"` / `json:"deadLetterTargetArn"` are correct — do not "fix" to PascalCase, and any tag-shape lint must allowlist these.

## EventBridge resolver — `EventBusArn` is dead path

`eventbridge_resolvers.go` reads `attrs.Rule.EventBusArn` first then falls back to `EventBusName`. SDK `eventstypes.Rule` has no `EventBusArn` field and `eventbridge_scanners.go` never synthesizes one — real scans always take the EventBusName fallback. Tests using hand-rolled JSON with `EventBusArn` work in isolation but exercise no production code path. Use `EventBusName` in fixtures and mirror the synthesis the resolver does (`arn:aws:events:{r}:{a}:event-bus/{name}`).

## Wrapper-shape test fixtures — `wrapped_attrs_testhelper_test.go`

Scanner-side wrapper containers (`{"lb": <LB>, "type": ...}` in `elb_scanners.go`, `tgWithTargets`, `ruleWithTargets`, etc.) are declared as function-local types so tests cannot reuse them directly. Build resolver-test `AttributesJSON` via the helpers in `wrapped_attrs_testhelper_test.go` (`elbv2LBAttrs`, `elbv2TargetGroupAttrs`, `eventBridgeRuleAttrs`) — they take real SDK types so wrapping-shape drift surfaces in tests rather than as silent zero-value resolutions in production. New wrappers go here, named `<svc><Resource>Attrs`.

## VPC subtree wired via `attached-to`, not `contains`

`ec2_networking_resolvers.go` emits `child → vpc` `RelAttachedTo` for subnet, IGW, route-table, NAT gateway, VPC endpoint, network ACL, peering. No `RecordHierarchyBatch` call wires VPC as a closure parent — VPC has zero `contains` rows. Graphs and `disco resources --hierarchy` see VPC only via the reverse `attached-to` edges. Add hierarchy wiring deliberately if a feature needs VPC→child closure traversal.

## Probe-first for low-TPS multi-phase services

Services with ≥20 sub-phase List ops AND a low per-account TPS quota (SageMaker is the canonical case) burn minutes when adaptive retry's token bucket throttles — every phase pays the penalty even on dormant accounts. Add a phase-0 probe of 2-3 cheap `MaxResults=1` List ops covering the highest-signal surfaces; short-circuit the full fan-out on empty. Precedent: `sagemakerInUseProbe` (sagemaker_scanners.go) probes ListDomains + ListNotebookInstances + ListEndpoints. Accept false negatives (pipelines-only / training-jobs-only accounts) for the wall-time win.

## Expected-state singletons → silent no-op, not warn

Singleton-config Get/List ops return distinct error codes when the config has not been opted into (`SigningConfigurationNotFoundException`, `NotConfiguredException`, `RegistryPolicyNotFoundException`, `ResourceNotFoundException`, `TagOptionNotMigratedException`, `UnauthorizedException` from org-only APIs called by non-mgmt accounts). These are the **default state**, not warnings. Return `(0, 0, nil)` directly — do NOT route through `skipIfAccessDenied`, which records a `ScanWarning` and clutters the per-region warnings block. Real IAM denies still warn via `isAccessDenied`.

## ThrottlingException is dispatch-level transient

`isTransientNetworkError` (aws.go) matches `ThrottlingException` / `Throttling` / `ThrottledException` / `RateExceededException`. Post-retry throttle exhaust (SDK retryer burned its 10-attempt adaptive budget) warn-skips at `scanRegion` dispatch automatically. Scanners do NOT need inline `isAPIErrorCode(err, "ThrottlingException")` handling — same shape as RequestTimeout/ServiceUnavailable.

## AppStream DescribeUsers: SAML auth-type rejected

The SDK enum `appstreamtypes.AuthenticationType` exposes USERPOOL, SAML, API. AWS rejects SAML on `DescribeUsers` (`'SAML' is not a supported authentication type for describing users`); SAML federation users are not first-class user-pool entries. Iterate USERPOOL + API only.

## Resolver-edge metadata: `EdgeDecl`

`registerResolver(fn, emits ...EdgeDecl)` is variadic — every new resolver MUST list each `(source, target, kind)` triple it upserts. Audit + coverage tooling reads the metadata; resolvers without `emits` are invisible to gap analysis. EdgeDecl shape: `{Source: TypeXxx, Target: TypeYyy, Kind: store.RelXxx}`. Annotate dynamic-dispatch resolvers (e.g. EventBridge target classifier) with one EdgeDecl per yielded type — enumerate the dispatch table. Cross-tenant self-node targets (e.g. `aws:iam:account` for an out-of-scope account) are valid Target values; the resolver `InsertResourcesIfAbsent`s an empty-attribute placeholder at that self-node's natural key before the edge so FK holds (see `internal/providers/CLAUDE.md` "reference-discovered placeholders"). `RecordHierarchyBatch` calls produce `parent → child contains` rows — declare as `EdgeDecl{Parent, Child, store.RelContains}`. Read-only / sidecar-populator resolvers stay `registerResolver(fn)` with no edges and surface in `disco coverage resolvers --only-unannotated` as intentional no-ops.

Tooling:
- `disco coverage resolvers --providers aws [--only-unannotated]` — per-resolver edge counts.
- `disco coverage resolvers --missing --providers aws` — emitted disco types with no `EdgeDecl.Source` mention. The candidate gap inventory.
- `go run ./cmd/aws-resolver-audit/ --list-edges` — every declared (src, tgt, kind) triple.
- `go run ./cmd/aws-resolver-audit/ --db <path>` — diffs declared metadata + DB edges against ARN/ID refs walked from `AttributesJSON`.

Snapshot lives in the orphan-types fenced block of `docs/aws-missing-resolvers.md` — refresh with `disco coverage resolvers --missing --providers aws` after each resolver-shipping commit and replace the block contents so future PRs diff against it.

## NativeID parent-extraction = dominant child→parent shape

Most service hierarchies encode the parent in the child's NativeID via `{parentARN}/<kind>/<id>` (Cognito user-pool children, Logs streams / metric-filters / subscription-filters / transformers, Deadline farm children, AppSync per-API children, MediaConnect VPC interfaces, Connect instance children, Glue partitions). Resolver pattern: `strings.Index(arn, "<segment>/")` + slice up to that point; wire one resolver per child cluster with `EdgeDecl`, FK-safe via `scannedIDSet(parentType)`. No need to walk parent attrs — the child already carries the link in its NativeID.

## Don't parallelize agents over shared resolver registries

`<service>_resolvers.go` `init()` blocks are append targets for every resolver added. Dispatching parallel agents that each write to the same `init()` produces registration calls without function bodies when one agent runs out of usage budget mid-task — the package fails to compile with N undefined symbols. Either dispatch one agent per service file, or have agents create new `<service>_extended_resolvers.go` files (separate `init()` blocks merge cleanly).

## Per-op region gates (sub-API only available in one region)

Some scanners are regional, but a subset of their ops only work in a specific region — Lightsail's `GetDistributions` and `GetDomains` are us-east-1-only while the rest of the Lightsail surface is regional. AWS rejects from other regions with `InvalidInputException: ${kind}-related APIs are only available in the ${region} Region`. Gate per-phase (`if region != "us-east-1" { return 0, 0, nil }` at the top of `scanLSDistributions`), not via `global: true` on registerService — that would skip the regional ops too. Precedent: `lightsail_extended_scanners.go` `scanLSDistributions` / `scanLSDomains`.

## Per-region feature gap → InvalidRequestException, silent skip

Some sub-APIs work in subset of regions only. The rejection comes back as `InvalidRequestException: Feature not supported yet` rather than `AccessDeniedException`. This is per-region, not per-account, so `markServiceDisabled` is wrong shape (other phases of the service still work in this region). Detect via code+message predicate and return `(0, 0, nil)` for a silent skip — the warn would fire on every scan in non-supporting regions otherwise. Precedent: `isIoTSiteWiseFeatureUnsupported` in `iotsitewise_scanners.go` (ListComputationModels works us-east-1 / eu-west-1, fails us-west-2).

## DNS probe to confirm global-service region

Before relying on AWS docs (or existing scanner code) for which region a global service lives in, probe with `getent hosts <svc>.<region>.amazonaws.com` across candidate regions — only the correct endpoint resolves; others return NXDOMAIN. Session live-scan revealed three scanner errors this way: `route53-recovery-readiness` + `route53-recovery-control` are us-west-2 only (not us-east-1), and `route53globalresolver` is us-east-2 only on the `.api.aws` TLD.

## Re-upsert parent with Describe body via ON CONFLICT

Scanner pattern when (1) `List*` is upserted first to enumerate children, then (2) `Describe*` per parent fans out to emit child rows. Parent attrs end up as the list-summary shape — strip-of-detail. To wire parent-side resolvers (e.g. `ServiceExecutionRole`, `CloudWatchLoggingOptions[]` on KDA app), append the parent row to the second-pass batch with `mustJSON(detailBody)`. UpsertResources ON CONFLICT updates `attributes`, so the second upsert replaces the summary JSON in place. Precedent: `scanKinesisAnalyticsV1` / `scanKinesisAnalyticsV2` (kinesisanalytics{,v2}_scanners.go). Cheaper than a third API round-trip.

## AWS-default identification heuristics (ManagedByProvider)

Common predicates observed when flagging AWS-default rows at scan time:
- Name / GroupName / RuleName / OptOutListName / DBSecurityGroupName == "default" or "Default"
- Name prefix `default.` (memorydb:parameter-group)
- Alias prefix `alias/aws/` (kms:alias)
- DomainConfigurationName prefix `iot:` (iot:domain-configuration)
- Id prefix `rslvr-autodefined-rr-` / `rslvr-autodefined-assoc-` (route53resolver rules + associations)
- Org root rows with Id `r-xxxx` (organizations:ou)
- SCP `p-FullAWSAccess` (organizations:scp)
- SDK enum field equals service-managed sentinel (kms:key KeyManager=="AWS", mediaconvert:queue Type==SYSTEM, ecs:capacity-provider Cluster==nil for FARGATE)
- Zero-time CreatedAt + canonical name (xray:sampling-rule "Default" RuleName)
- All-empty user customisation (uxc:account-customization with AccountColor "none" and empty visible-* lists)
- StorageLens Id == "default-account-dashboard"

Verify the predicate against a real account if the SDK doc is ambiguous — observed behaviour wins.

## Adding a resolver for a previously-leaf type

`TestLeafTypesNotResolverSources` (`coverage_leaves_test.go`) fails when a `coverage.TypeDecl` flagged `Leaf: true` (set inline on the scanner's `emits` decl) appears as an `EdgeDecl.Source`. Drop the `Leaf: true` flag in the same commit as the new resolver — the test is the only signal, no build error. Leaf flags live next to each `registerService` / `registerExtraEmits` site since the central `coverage_leaves.go` map was retired.

## Re-verify leaf-flag comments before trusting them

Per-emit-decl `Leaf: true` flags often carry an inline reason ("refs blocked by sanitize", "refs need Describe enrichment", "no SDK list op"). These rot: redaction is now per-type and per-path (`internal/providers/aws/redact.go`); ARN-bearing fields like `CredentialsArn` / `SecretArn` / `TokenSourceArn` / `AuthorizationHeaderArn` are preserved by *omission* (no rule targets them). Before adding a sidecar workaround for what a comment says is "blocked", read `internal/providers/aws/redact.go` and confirm the field actually has a rule on it; if not, the resolver can read it directly. Same applies to "no Describe op" claims — SDK additions land between scanner-write and leaf-comment time.

## Parent-row "leaf" ≠ no edges

Many parent types (mediaconnect:flow, kinesis:stream, eventbridge:rule) appear leaf-flagged because their existing resolvers emit *child→parent* `attached-to`, not parent→outbound. To demote, identify a NEW outbound edge from the parent's own SDK body to a *non-child* type — RelContains/closure to children doesn't count. Precedents: `mediaconnect:bridge → mediaconnect:gateway` via `PlacementArn` (commit 5ccaf80) demoted the parent; `mediaconnect:flow` stayed flagged because every Flow body field maps to an existing child type. Confirm via SDK doc grep on the body struct *before* scanner enrichment work — if every ref-bearing field is already spawned as a child row, the parent is genuinely leaf.

## Empty-message AccessDeniedException = closed-to-new-customers signal

When AWS retires a service to new customers (existing customers keep access), list ops on unregistered accounts return `AccessDeniedException` with an *empty message body* — distinct from real per-op IAM denials which always carry an action-identifying message. Detect via `isClosedToNewCustomers(err)` (aws.go). Closure state is **non-uniform across ops on the same account** (`ListCampaigns` may succeed while `ListDecoderManifests` fails). Wire two layers: phase-0 gate for the common case (one cheap probe → `markServiceNotEntitled` if empty-msg — the account can't self-enable a closed service, so this is not-entitled, not disabled) AND per-phase silent return `(0,0,nil)` for the partial-uniformity case. Real IAM denials with messages still warn via `skipIfAccessDenied`. Precedent: `gateIoTFleetWise` + `gateFraudDetector` (per-service gate fns) calling shared `isClosedToNewCustomers` (aws.go). Whole-service closed detections (kendra `isKendraClosedToAccount`, interconnect `isInterconnectClosedToAccount`, timestream `isTimestreamLiveAnalyticsClosed`, voiceid `isVoiceIDNotEnabled`, cloud9, datapipeline) likewise return `markServiceNotEntitled`. See "not-entitled" in the three-state dispatch section below.

## Two-pass scanner: keep total == inserted via skip-set dedup

Multi-pass scanners that pre-stub catalogue rows then re-upsert with rich detail (e.g. IAM AWS-managed policy catalogue + GAAD pass) inflate the per-service progress line on a fresh DB: `total = len(batch)` counts both upserts, but `inserted` only counts the first because the second is an ON CONFLICT update. Surfaces as `(1520 total, 1508 new)` → confuses users into thinking the scan was partial. Fix: reverse pass order so the *rich* pass runs first and captures the dedup ARN set, then the *stub* pass filters its batch via `if skipARNs[arn] { continue }`. Each row upserted exactly once; total == inserted on fresh DB. Precedent: `scanIAMAuthDetails` + `scanIAMAWSManagedCatalogue` (commit 14cbee2).

## `--regions all` sentinel expands to the full region list

`loadAccounts` (`aws_config.go`) wraps every finalized region slice
(`--regions` override, per-account `regions`, or `aws.default_regions`) through
`expandAllRegions` (`aws_regions.go`): the case-insensitive `all` token expands
to a clone of `awsregions.Regions` (the full static list). Expansion happens
*before* `scanAccount` → `enabledScanRegions`, so the opted-in filter below then
trims it to what the account can actually reach. `disco scan aws --regions all`
and `aws.default_regions: [all]` are equivalent. cmd stays provider-agnostic —
it doesn't expand the sentinel, only records it as the compact string `"all"` in
`scans.scope` (matching the no-`--regions` default representation).

## Account-disabled regions filtered before fan-out (not at dispatcher)

`scanAccount` (`aws_scanner.go`) calls `enabledScanRegions` once per account before the
per-region fan-out: a single `ec2:DescribeRegions` probe (from always-enabled `us-east-1`,
under the account's own/assumed credentials — opt-in status is per-account) builds the
enabled-region set via `enabledRegionSet` (`aws_config.go`, opt-in-status filter mirrors
`aws_coverage.go::FetchRegions`), then `filterToEnabled` drops not-opted-in regions. Skipped
regions surface as one `preflight:regions` warning. Probe failure (e.g. `ec2:DescribeRegions`
denied) falls back to the full configured list — restricted roles still scan, just without
the speedup.

Why a pre-filter, not error classification: calling a regional endpoint in a not-opted-in
opt-in region (af-south-1, ap-east-1, …) returns `AuthFailure` / `UnrecognizedClientException`
/ `InvalidClientTokenId` — indistinguishable at the call site from genuinely bad/expired
credentials, and each one burns the 10-attempt adaptive retry budget. Do NOT add those codes
to a dispatcher silent-skip: that would mask real credential failures. Fix at the source.

## Per-service region pre-scoping via SSM global-infrastructure catalog

`buildRegionAvailability` (`aws_region_availability.go`) runs once per account in
`scanAccount`, after `enabledScanRegions`, when `--scope-regions` is on (default)
AND more than one region is scanned. It queries the SSM global-infra catalog
(`/aws/service/global-infrastructure/services/<code>/regions`, `ssm` client pinned
to `us-east-1`) per distinct service code and caches `acct.availByCode`
(code → region set). `scanRegion` then skips dispatching a service into a region
the catalog says AWS doesn't offer it in — surfacing `(region: unavailable)`.

**Fail-open is the whole safety model.** The catalog is AWS's own availability
truth, so a region it omits is one the API genuinely isn't in (we'd NXDOMAIN/error
anyway). `serviceAvailableInRegion` returns "scan" whenever the data is
missing/unknown: nil map, code absent from catalog (divergent name), or empty
region set. The service code derives from the registerService name minus `aws:`;
divergent names (e.g. `aws:code-build` vs catalog `codebuild`) simply aren't found
→ scanned everywhere. `regionAvailabilityCodeOverrides` (intentionally empty) is
the unlock for divergent services — only add a mapping VERIFIED against the live
catalog, since a wrong override is the one way to skip a region the service serves.
This complements, never replaces, the per-region silent-skip predicates (NXDOMAIN,
feature-gap codes) — those catch sub-feature gaps the service-level catalog can't.
Toggle: `--scope-regions=false` (or `aws.scope_to_available_regions: false`).
Capability: `providers.RegionScopeToggler`.

## NXDOMAIN at dispatcher = service not deployed in region

`isDNSNotFound` (`aws.go`) matches `*net.DNSError` with `IsNotFound=true` — AWS endpoint host has no DNS record. Permanent fact about region availability, not transient outage. `scanRegion` / `scanAccount` silent-skip BEFORE `isTransientNetworkError` warn-skip; service progress line shows `(region: unavailable)`. Real DNS server problems surface as timeouts / SERVFAIL, not NXDOMAIN, and still warn. Replaces N per-region "transient: dial tcp: lookup …: no such host" warnings for services not yet deployed in scanned region.

## `(account: disabled)` vs `(account: not entitled)` vs `(region: unavailable)` — three distinct dispatch states

The per-service progress suffix distinguishes account-level self-enableable from account-level not-entitled from region-not-deployed (`store.ServiceStatus`: `ServiceOK` / `ServiceDisabled` / `ServiceNotEntitled` / `ServiceUnavailable`, rendered in `cmd/scan.go::serviceStatusSuffix`):

- **`(account: disabled)`** — `errServiceDisabled` sentinel (`markServiceDisabled`): the **account** hasn't enabled/subscribed the service but **could self-enable** it (Shield, Macie, Security Hub, Organizations-not-in-use, DRS/MGN init, Audit Manager / Cost Explorer / Control Tower setup, license-manager onboard, odb/securityir/shield paid opt-ins, bcmpricingcalculator's Cost-Explorer-not-enabled branch). Azure (`errServiceNotRegistered`) and GCP (`errServiceDisabled`) map their subscription/project not-enabled states here too.
- **`(account: not entitled)`** — `errServiceNotEntitled` sentinel (`markServiceNotEntitled`): the service **exists but the account can't self-enable** it. Two families: (1) closed-to-new-customers — the account never onboarded before AWS retired the service to new customers (timestream LiveAnalytics, voiceid, interconnect, kendra, b2bi, frauddetector, iotfleetwise, cloud9, datapipeline); (2) payer/topology gates — a member account can't enable a payer-only billing API (budgets linked-account, billingconductor, bcmdataexports, invoicing, bcmpricingcalculator payer-only branch). The user cannot flip this from their own account. The decision axis vs disabled: **can the account self-enable?** enable/init/subscribe/onboard/register-SLR → disabled; closed-to-new-customers / support-tier / payer-only / not-eligible → not-entitled.
- **`(region: unavailable)`** — the service is **not deployed in this AWS region**: NXDOMAIN (`isDNSNotFound`), the SSM region-availability catalog miss, OR the `errServiceUnavailable` sentinel (`markServiceUnavailable`) returned by a scanner when the **whole** service is absent (every op fails). Precedent: omics — outside its supported regions the gateway answers `AccessDeniedException: Unable to determine service/operation name`, so each phase's `isServiceNotAvailableInRegion` guard returns `markServiceUnavailable(perr)`. AWS-only — Azure/GCP have no per-region fan-out.

**Use the sentinel only for WHOLE-service-absent.** A per-op / sub-feature region gap where the parent service IS present (lambda capacity-providers, gamelift Containers, iotsitewise ComputationModels, cloudwatch OTel) keeps its existing per-phase silent skip (`return 0, 0, nil`) — marking the whole service unavailable would wrongly blank a working service.

## Per-region feature-gap error codes are service-specific

AWS surfaces "this sub-feature is not deployed in this region" under different codes per service. Build the silent-skip predicate against the exact code observed. Known shapes:

- `UnsupportedRegionException` (gamelift Containers + FlexMatch)
- `InvalidRequestException` + "Feature not supported yet" (iotsitewise)
- `InvalidAction` "Operation not supported" (cloudwatch GetOTelEnrichment)
- `ValidationException` + "Member must satisfy enum value set" (App Auto Scaling per-region namespace enum)
- `AccessDeniedException` + canned message linking docs URL (workspaces:DescribeWorkspacesPools)
- `InternalFailure` 500 post-retry (quicksight ListActionConnectors)

Empty-message `AccessDeniedException` is a separate signal (closed-to-new-customers — see iotfleetwise / interconnect precedents).

## SDK paginator `Limit=0` nils MaxResults → 400 ValidationException

`New<Op>Paginator` constructors that expose a `Limit` paginator-option overwrite `params.MaxResults` to `nil` when `Limit==0` (default). Some AWS APIs reject the resulting empty MaxResults with `Value '0' at 'maxResults' failed to satisfy constraint`. Always pass an option-fn setting `o.Limit` to a valid page size: `NewListXxxPaginator(client, in, func(o *svc.ListXxxPaginatorOptions) { o.Limit = 100 })`. Precedent: `scanQSActionConnectors` (quicksight_scanners.go).

## Clamp retries per-op for ops that return persistent 5xx

Global config sets `WithRetryMaxAttempts(10)` + adaptive backoff for low-TPS services like IAM. Newer/region-gated ops sometimes return `InternalFailure` 500 (instead of clean 4xx) when their feature is not deployed; the global budget then burns ~2m before the call returns. Clamp on the offending op via paginator `NextPage` optFn or direct call optFn: `func(o *svc.Options) { o.RetryMaxAttempts = 2 }`. Pair with adding the post-retry code to the soft-skip predicate. Precedent: `scanQSActionConnectors` clamps + soft-skips `InternalFailure`.

## AccessDenied disambiguation — canned doc URL = per-region feature gap

Real per-action IAM denials carry the SDK-formatted body `User: arn:... is not authorized to perform: <action> on <resource>`. AWS's "this feature is not in this region" canned response is a different shape: generic "You do not have the permissions required to perform this action" + a docs URL marker. Detect via `isAccessDenied(err) && strings.Contains(err.Error(), "<service-doc-url-fragment>")` and silent-skip. Real denials still warn via `skipIfAccessDenied`. Precedent: `scanWSWorkspacesPools` checks `workspaces-access-control.html`.

## Terminated EC2 instances drop `IamInstanceProfile` (and other volatile attrs)

`DescribeInstances` returns terminated instances for ~1 hour after termination, but AWS clears volatile attributes — `IamInstanceProfile`, post-cleanup network-interface specifics, attached-volume mounts — on the way out. The EC2 instance row in disco's DB faithfully reflects the post-termination state, so resolvers that read those fields (instance → instance-profile, instance → ENI, instance → EBS) correctly emit no edge for terminated instances. If `disco graph blast <iam-role>` returns fewer hops than expected for a role attached to a recently-terminated instance, check `attributes.State.Name == "terminated"` before chasing a resolver bug. Not a scanner bug — the scanner stores what AWS returns.

Filtering terminated instances out of inventory entirely is a separate product question (TTL? user opt-in?); today disco keeps them so the user can see "this terminated yesterday" in `disco resources`. Use a Rego rule (`input.attributes.State.Name == "terminated"`) to surface them as findings if your policy demands cleanup — see the infrastructure-engineer persona's `TERM-001` example in `focus-group/reports/infra-engineer.md`.