# CLAUDE.md — `internal/providers/aws/`

AWS scanner + resolver conventions. Cross-provider rules: see `../CLAUDE.md`.

## Declaring types

Types are declared via `registerType` (see `../CLAUDE.md`); new redact/volatile/managed rules go on the descriptor. `TestNoDoubleDeclaredTypes` (`descriptor_guard_test.go`) rejects a type declared twice; `aws_pairing_test.go` guards that every type's scanner calls an SDK list op.

**`Managed: true` is only for UNCONDITIONALLY-managed types.** A type whose
`ManagedByProvider` is per-row conditional (`OwnerId == "AWS"`,
`PolicyType == Managed`, name prefixes) stays scanner-set — do NOT set `Managed`
on its descriptor (dual-natured: `TypeIAMPolicy`, `TypeLambdaLayerVersion`,
`TypeOrganizationsOU` keep their scanner literal). A managed type that is ALSO a
resolver source needs `IncludeManaged: true` on that resolver's `ListResources`
or the store stamp hides it from its own resolver (precedent:
`ecr`/`uxc`/`route53resolver`/`iot` config resolvers).

## Resolver conventions

- **Scanner attribute JSON uses PascalCase keys.** `mustJSON` calls `json.Marshal` on AWS SDK v2 response structs, no json tags — `ClusterArn` stays `ClusterArn`, not `clusterArn`. Resolver structs need PascalCase tags (`json:"ClusterArn"`) or silent match nothing on real scan data while tests pass on hand-rolled JSON.
- **ARN helpers** (`aws_arn.go`): `ec2ARN(region, acct, kind, id)` → `arn:aws:ec2:{r}:{a}:{kind}/{id}` (slash sep). `rdsARN(region, acct, kind, id)` → `arn:aws:rds:{r}:{a}:{kind}:{id}` (colon sep). `apigatewayARN(region, path...)` → `arn:aws:apigateway:{r}::/p1/p2/...` (empty account, variadic path joined `/`). `logGroupNativeIDFromName(acct, region, name)` → `arn:aws:logs:{r}:{a}:log-group:{name}` — use it instead of `fmt.Sprintf`. The SDK returns `CloudWatchLogsLogGroupArn` with a trailing `:*`; strip it (`strings.TrimSuffix(arn, ":*")`) before NativeID lookup or the edge points at a phantom resource. Synthetic NativeIDs for APIs that issue no ARN: `macieSessionNativeID`, `ssoAssignmentNativeID`, `identityStoreUserNativeID`, `identityStoreGroupNativeID`. Resolvers rebuild target ARN from native ID, pass to `store.ResourceID(...)`. Wrong shape = phantom edge.
- **KMS edges**: skip empty `KmsKeyId` / `KMSKeyArn`. AWS-managed default keys unscanned, edge = dangling target. `if sv(attrs.KmsKeyId) == "" { continue }`.
- **EFS mount target NativeID**: no native ARN. Synthesize: `arn:aws:elasticfilesystem:{region}:{acct}:file-system/{fsid}/mount-target/{mtid}` using `FileSystemId` + `MountTargetId` from `DescribeMountTargets`.
- **KMS grant NativeID**: `ListGrants` returns no `GrantArn` — `GrantListEntry` only has `GrantId`. Synthesize `{keyARN}/grant/{grantId}`. No real `arn:aws:kms:...:grant/...` ARN exists; pattern-matchers keyed on AWS-issued ARNs skip.
- **SSO account-assignment NativeID**: assignments have no AWS-issued ARN. Synthesize `{permissionSetArn}/account/{accountId}/{principalType}/{principalId}` (`ssoAssignmentNativeID` in `sso_scanners.go`). Permission-set ARN already encodes instance id, synthetic carries enough context to dedupe across re-scans.
- **Identity Store user/group NativeID**: Identity Store APIs return no ARN. Synthesize `arn:aws:identitystore::{ownerAccountId}:user/{IdentityStoreId}/{UserId}` and `…:group/…/{GroupId}` (`identityStoreUserNativeID` / `identityStoreGroupNativeID`). `ownerAccountId` from parent SSO instance's `OwnerAccountId`.
- **SSO permission-set ARN → instance ARN**: rebuild via `instanceArnFromPermissionSetArn` in `sso_resolvers.go` — permission-set ARN's `:permissionSet/{ssoins-id}/…` path embeds instance id; canonical instance ARN is `arn:aws:sso:::instance/{ssoins-id}`. `strings.Cut` twice, no Index.
- **AWS Backup plan ARN** uses `backup-plan:`, not `plan:`. Real: `arn:aws:backup:{r}:{a}:backup-plan:{planId}`. Synthetic selection NativeID `{planARN}/selection/{selId}` — trim `/selection/...` in resolver to recover parent plan ARN. Wrong prefix → the closure `contains` row is gated out with a ScanWarning.
- **Org test fixtures**: `loadOrgTargetIndex` keys on attrs JSON `{"Id":...}`, not NativeID. Test rows for `TypeOrganizationsAccount` / `TypeOrganizationsOU` need `{"Id":"<raw-id>","Arn":"<arn>"}` in attrs — bare `{}` leaves index empty, every chained resolver silently emits zero edges with no error.
- **Macie session NativeID**: `GetMacieSession` returns no ARN — session is account/region config. Synthesize `arn:aws:macie2:{region}:{acct}:session` (`macieSessionNativeID` in `macie_scanners.go`). Singleton per (account, region); jobs / CDIs / allow-lists hang off it via contains closure.
- **Organizations NativeID = full ARN, not raw ID**. Accounts + OUs keyed by `sv(a.Arn)`, not `o-xxx` / 12-digit account ID. APIs like `ListDelegatedAdministrators` return raw IDs — translate via `loadOrgTargetIndex` (`organizations_resolvers.go`) before building `ResourceID`.

## ELBv2 LB attrs wrapped

Scanner stores LoadBalancer as `{"lb": <LB>, "type": "<kind>"}` (see `elb_scanners.go`), not top-level. Resolvers reading `DNSName`, `Scheme`, `VpcId` etc. must unmarshal under `"lb"` key or silent zero values.

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
- **Managed policy rows are wrapped `{"Policy": ..., "PolicyVersion": ...}`** — `scanIAMAuthDetails` fills `PolicyVersion` from GAAD's `PolicyVersionList` (`defaultPolicyVersion`); `extractPolicyDoc` reads `PolicyVersion.Document` for managed, `PolicyDocument` for inline. Catalogue stub rows carry no `PolicyVersion` and are skipped.
- **Federated-provider ARN dispatch**: `:saml-provider/` → `TypeIAMSAMLProvider`; `:oidc-provider/` → `TypeIAMOIDCProvider`. Other Federated shapes emit no edges (skip, no dangle).
- **Bare resource names in `Resource[]` skip.** Policy docs carry no region context, synthesizing ARN risks wrong region. Contrast `ecsSecretTarget` (`ecs_resolvers.go`), which DOES synthesize from bare names — task-defs supply region. Same input shape, different rule, different carrier.

## WAFv2 scope pattern

WAFv2 two scopes: `REGIONAL` (per-region) + `CLOUDFRONT` (global). CLOUDFRONT scope reachable only from `us-east-1` — other regions error. Guard with `if region == "us-east-1"` before CLOUDFRONT-scope calls to dodge duplicates.

## FK-safe edge emit when target partially scanned

Resolver targets type with unscanned members (public/Marketplace AMIs, cross-account ARNs, shared snapshots) — build target id set once via `ListResources(Types: []string{TargetType})`, emit edge only if computed target id present. Prevents phantom edges (edges have no DB FK since migration 006). Precedent: `keyPairByNameRegion`, `imageByID` in `ec2_compute_mgmt_resolvers.go`.

## Ownership-filtered AWS scanners

AWS Describe* with ownership filter (`Owners=["self"]` for AMIs/snapshots/FPGA images) — scan self-owned only. Public/Marketplace/shared sets unbounded + not ours to audit (third-party, not AWS-managed — distinct from the `ManagedByProvider` flag in `internal/providers/CLAUDE.md`, which covers AWS-owned catalogue resources like managed prefix lists, IAM AWS-managed policies, IAM service-linked roles, AuditMgr Standard frameworks/controls). Cross-account refs from scanned resources (instance → public AMI) handled via FK-safe lookup above.

## AWS service-integration ARNs use `:::`

Step Functions Definitions + similar carry built-in integration ARNs like `arn:aws:states:::sns:publish` where region+account segments empty. Substring-based ARN dispatchers (`sfnTargetType`, `eventBridgeTargetType`) must filter `strings.Contains(arn, ":::")` before classifying, or emit phantom edges to non-existent resources.

## List-then-describe pattern (N+1 avoidance)

AWS service returns only names from List API (EKS, DynamoDB) — describe each resource concurrent via `errgroup` + `sync.Mutex` to collect, then batch upsert. No sequential Describe in loop.

## Provisioned + Serverless flavors → single type

One resource type, not two. Flavor lives as sibling sub-structs (`Provisioned *...`, `Serverless *...`) in native attrs; resolver branches on whichever non-nil. Precedent: `aws:kafka:cluster` MSK, `kafka_resolvers.go` reads subnets/SGs from `BrokerNodeGroupInfo` vs `VpcConfigs[]`. Applies to services modeling variants as parallel fields on same List/Describe response (Redshift Serverless, Aurora Serverless v2, EMR Serverless likely).

## SDK v2 paginator availability per-op

`New<Op>Paginator` exists only for ops AWS models as paginated. Many List ops no paginator — eventbridge, cloudfront Marker ops, wafv2, apigatewayv2 (`GetApis`/`GetAuthorizers`/`GetDomainNames`/`GetApiMappings`), logs (`DescribeAccountPolicies`/`DescribeQueryDefinitions`/`DescribeResourcePolicies`), ec2 (`DescribeVpcEndpointServices`/`DescribeVpcBlockPublicAccessExclusions`), rds `DescribeDBShardGroups`. Before converting manual `NextToken`/`Marker` loop, grep `~/go/pkg/mod/github.com/aws/aws-sdk-go-v2/service/<svc>@v*/api_op_<Op>.go` for `Paginator struct`. Author comments like `// ... uses manual NextToken pagination` flag intentional choice — do not "fix". EC2 has shared helper `ec2PageScan` for paginator-enabled ops; reuse it.

## Smithy API-error-code predicates

`isAPIErrorCode(err, codes ...string) bool` in `aws_errors.go` = single choke point (wraps `errors.As` + `smithy.APIError.ErrorCode()` + `slices.Contains`). Use inline for one-off checks. Wrap in named helper only when reused 3+ times (precedent: `isAccessDenied` over `accessDeniedCodes`).

Predicates needing **code + message-substring** match use `isAPIErrorWithMessage(err, code, needle)` (single code) or `isAccessDeniedWithMessage(err, needle)` (any `accessDeniedCodes` entry). Both read `ae.ErrorMessage()` directly via `errors.As(&smithy.APIError)`, never `err.Error()` — the match is decoupled from the Smithy `"api error CODE: MSG"` wrapper format and the outer SDK `"operation error <Op>: ..."` wrapping. Use these for AWS exception codes reused across semantically-distinct cases (`AccessDeniedException` for closed-to-customers vs real IAM deny; `ValidationException` for per-region feature gap vs malformed input). Do NOT add new sites that match against `err.Error()` substrings — every site in this package routes through one of these two helpers. Only `ScanWarning.Message` still shows the wrapped `api error CODE: MSG` form — tests asserting on it include that prefix (`TestSkipIfAccessDenied_RecordsWarningReturnsNil`).

## Not-enabled predicates sharing an access-denied code must be checked FIRST

When a service signals "not enabled / not subscribed" with a code that is in `accessDeniedCodes` (Comprehend's `NotAuthorizedException`, Macie's `AccessDeniedException`), its message-disambiguated predicate MUST run **before** the `isAccessDenied` branch. `isAccessDenied` matches on code alone, so it swallows the error first and records an IAM-style warning that no policy change could ever fix. Precedent: `isComprehendNotEnabled` sits above `isAccessDenied` in all four comprehend phases.

## Region-entitlement denials silent-skip globally

`skipIfAccessDenied` silent-skips `isNotAuthorizedForRegion` — AWS's `Account: <id> is not authorized for region: <region>` (CloudDirectory in every region the account was never enabled for). The account cannot self-enable it and no IAM change helps, so warning on every scan is noise. This sits inside `skipIfAccessDenied`, so it applies to **all ~1430 call sites**, not one service — deliberate, since any service using that phrasing states the same fact. Real per-action denials say "is not authorized **to perform**: `<action>`" and still warn; `TestSkipIfAccessDenied_RealDenialStillWarns` pins the distinction.

## `skipIfAccessDenied` always returns nil

Single-phase scanners `return 0, 0, skipIfAccessDenied(...)`. Multi-phase scanners (e.g. `scanEventBridge` running buses + rules + connections + api-destinations in one call) cannot early-return — use `_ = skipIfAccessDenied(...); break` to skip denied phase while preserving totals from prior phases. Precedent: `scanEventBridge` phases 3/4.

## Transient errors already wrapped at dispatch

`scanRegion` / `scanAccount` (`aws_scanner.go`) route each `svc.fn` error through `classifyServiceError` (`aws_errors.go`); its transient rung (`isTransientNetworkError`) → `skipIfTransient` (warn + nil). Scanners do NOT need inline handling for dial/read/write (`net.OpError`), `net.Error` timeouts, throttling codes, or Smithy `RequestTimeout`/`ServiceUnavailable`/`InternalFailure` variants — those warn-skip automatically. NXDOMAIN is a silent `(region: unavailable)`; the per-service `context.DeadlineExceeded` is a hard error (`outcomeDeadline`). Only `AccessDenied` still needs per-scanner `return 0, 0, skipIfAccessDenied(...)` (not wrapped at dispatch because SDK surfaces it mid-paginator, not as top-level svc.fn error).

`isTransientNetworkError` also matches transport send failures — `*smithyhttp.RequestSendError` and bare `io.EOF` / `io.ErrUnexpectedEOF` (observed post-retry on `transcribe:ListCallAnalyticsCategories`: "request send failed, Post ...: EOF"). **Consequence for store-write errors:** pgconn reports a dropped Postgres connection as `unexpected EOF`, so any wrapper around a store-write failure MUST break the `errors.Is` chain (format the cause with `%s`, not `%w`). Otherwise a dead DB is reclassified as a transient AWS warning and the scan reports success while silently dropping rows.

## KMS key edge — use `loadKMSResolveIndex` + `resolveKMSKeyID`

Resolver sees KMS ref in four shapes: full key ARN, alias ARN, `alias/foo`, bare key UUID. Build index once per resolver via `loadKMSResolveIndex(acct, st)` (`kms_helpers.go`), then call `idx.resolveKMSKeyID(ref, region, acctID)` per edge — returns `(keyID, ok)` where `ok=false` means target unscanned (skip emit). Index also resolves alias name → underlying key ARN, so `alias/aws/foo` refs link to AWS-managed key (which IS scanned — see kms scanner). Don't manually call `kmsKeyTargetARN` + build key ID set + check `alias/aws/` — helper does all three. Precedent: backup, rds, sns, sqs, kinesis, firehose, ssm, config, s3-encryption, kafka, cloudtrail-eds resolvers.

## Wildcard guard runs on canonical resource, not raw ref

Policy `Resource` walkers (e.g. `classifyPolicyResource` in `iam_resolvers.go`) trim object/version/index suffixes before checking for `*?`. `arn:aws:s3:::bucket/*` is real bucket-level grant; only wildcards inside canonical segment (`prod-*` in bucket name, `*` as whole ref) skip. Same for `:function:NAME:*`, `:secret:NAME:*`, `:table/NAME/*`, `:log-group:NAME:*`.

## IAM API has a sustained TPS ceiling near `fanoutMed`

AWS IAM throttles around 10–20 sustained TPS per account. `fanoutMed` (10) is the safe ceiling for any per-resource fan-out (`GetPolicyVersion`, `Get*Policy`, etc.). Bumping to `fanoutHigh` (20) trips `ThrottlingException`, SDK retries with exp backoff, multi-minute hangs across the ~1500-policy AWS-managed catalogue. The speedup is GAAD consolidation (below), not concurrency tuning.

## Service Quotas is opt-in; rate-limiter holds 10 req/s, `sqWorkers`=30, `MaxResults=100`

**Quotas are NOT resources and register no type.** `scanServiceQuotas` writes `store.Quota` rows into the `quotas` table (disco migration 017) via `UpsertQuotas`, and there is no `registerType` for them — `TestServiceQuotasDeclaresNoResourceType` (`descriptor_guard_test.go`) fails if one comes back. Why quotas live in their own table: see `store/CLAUDE.md` (`quotas`). The service registration must survive alongside the absent type — dropping that stops quotas being scanned at all, which is the larger regression. Same shape on the Azure side (`azure:microsoft.quota`), which is **not** opt-in.

**Both adjustable and non-adjustable limits are recorded**, and non-adjustable is the more interesting class: a hard ceiling moves only when AWS moves it, with no customer request and no notification, so its version chain is the only record that it happened. `quotaRow` used to open with `if !q.Adjustable { return nil, false }`, silently discarding all of them. Identity is `(provider, account, region, service code, quota code)` — **not** the ARN, which now lives in the attributes remainder alongside everything else AWS reported. `listQuotaDefaultsForCode` pairs each limit with `ListAWSDefaultServiceQuotas`, which is what makes applied-versus-default answerable; it **doubles the API calls** and degrades to a NULL `default_value` on failure rather than costing us the applied limit.

**Opt-in.** `aws:servicequotas` registers with `serviceEntry.optIn=true`, so a default `disco scan aws` skips it — it reads account *quota limits* (metadata, not resources) and is ~4× the slowest resource scan. `filteredServices(filter, includeOptIn)` excludes opt-in services from the default set; they run only when (a) selected by name (`--services aws:servicequotas`) or (b) `--include-service-quotas` is passed (config `aws.include_service_quotas`, plumbed via the `ServiceQuotasIncluder` capability → `Scanner.includeServiceQuotas` → stamped onto `account.includeServiceQuotas`, mirroring `--scope-regions`). `optIn` is the generic registry knob for "default-off service"; reuse it for any future slow/peripheral scanner.

**Rate.** `ListServices`, `ListServiceQuotas` and `ListAWSDefaultServiceQuotas` are each **10 req/s steady + 10 burst, per region per account**, under a 50 req/s account-wide cap (https://docs.aws.amazon.com/servicequotas/latest/userguide/reference_limits.html). Each is paced by its own per-region `pacer` (`sqReqPerSec`=10, `sqBurst`=10; see "Rate-paced fan-out" below). **The limiter, not the worker count, holds the ceiling:** `sqWorkers`=30 sits comfortably above `rate × worst-case-latency` (~1.5–2.5s control-plane latency), so concurrency keeps the limiter fed; don't lower it toward 10, which re-couples throughput to latency. Always pass `MaxResults: sdkaws.Int32(100)` (the API max). `maxConcurrentRegions`=5 × 10 req/s = exactly the 50 req/s account cap, with no headroom; a sustained `--regions all --include-service-quotas` run leans on adaptive retry. `DISCO_SCAN_RATE_DEBUG=1` prints an `N calls in Ts = R req/s` saturation line per region.

## CloudWatch Logs phase-2 fan-out

`scanLogs` in `logs_scanners.go` runs in two phases. Phase 1 is the independent surface (log groups, account policies, deliveries, metric filters, etc.) executed sequentially. Phase 2 is per-log-group enrichment — `DescribeLogStreams`, `DescribeSubscriptionFilters`, `GetTransformer`. The three phase-2 sub-scanners are launched **concurrently** via `sync.WaitGroup`; they hit independent CloudWatch Logs APIs whose 5 TPS quotas are documented as **per log group** (https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/cloudwatch_limits_cwl.html), so concurrent calls to N distinct groups consume N independent buckets. Within one group the SDK paginator is sequential, so per-group TPS stays ≤ 1.

Per-group fan-out inside each sub-scanner uses `fanoutMed` (10), not `fanoutLow`. Account-wide pressure is absorbed by adaptive retry (`aws_config.go` `RetryModeAdaptive` + `RetryMaxAttempts(10)`); `ThrottlingException` is dispatch-level transient (see "Dispatch ladder" below). The phase-2 dispatcher loads the region's log-group set ONCE (`loadLogGroupsForRegion`) and passes the slice into each sub-scanner — three duplicate `ListResources` queries removed.

Errors from phase-2 sub-scanners are gathered and `errors.Join`-ed before propagating; one failed sibling does not cancel the others (matches `aws_scanner.go::scanRegion` "Errors never abort scan"). For users who don't need log-stream inventory, two existing escape hatches stand: `disco scan aws --services aws:ec2,aws:s3,...` (omit `aws:logs`) skips the service entirely; `disco resources --exclude-types aws:logs:log-stream` mutes streams from queries while keeping them in the DB (`cmd/CLAUDE.md`).

**`UploadSequenceToken` is dropped as volatile.** `DescribeLogStreams` returns a fresh, deprecated `UploadSequenceToken` on every call regardless of log activity — left in `AttributesJSON` it version-splits every log stream on every scan (a 907-stream account reports `907 changed` each scan). `TypeLogsLogStream`'s descriptor carries `Volatile: []string{"UploadSequenceToken"}` (registered via `registerType` in `logs_scanners.go`) so the store drops the key before the version comparison; see `internal/providers/CLAUDE.md` "Declaring redaction and volatile-field rules". `LastEventTimestamp` / `LastIngestionTime` (real ingestion) and the log-group `StoredBytes` are kept — they reflect genuine change.

## IAM scan uses GetAccountAuthorizationDetails (single paginated call)

`scanIAMAuthDetails` (`iam_scanners.go`) consolidates users + roles + groups + managed policies (Local + AWS scope, including each policy's default version `Document`) + every principal's inline policies into one paginated `iam:GetAccountAuthorizationDetails` (GAAD) call with `MaxItems=1000` + a `Filter` listing all five entity types.

**GAAD `AWSManagedPolicy` filter only returns *attached* AWS-managed policies, NOT the full catalogue.** `scanIAMAuthDetails` runs the GAAD pass first, then `scanIAMAWSManagedCatalogue` — a stub `ListPolicies(Scope=AWS)` pass that upserts a metadata-only row per AWS-managed policy GAAD did not already store (no `GetPolicyVersion` enrichment, avoids the throttling fan-out; see "Two-pass scanner" below). Unattached catalogue policies keep their stub row as edge targets for `resolveManagedPolicyAttachments`. Walker silently skips no-document rows. Don't drop the catalogue stub pass — without it, unattached AWS-managed policies disappear from the store. Don't reintroduce per-principal `List*`/`Get*Policy` or per-policy `GetPolicyVersion` fan-outs — they hit IAM TPS throttling and dominate wall-time. AWS-managed policies detected via the `arn:aws:iam::aws:` ARN prefix (canonical scope marker; GAAD doesn't expose the per-policy scope flag). Inline policies still upsert as `aws:iam:{role,user,group}-policy` rows with NativeID `{parentARN}/policy/{name}` so existing inline-policy resolvers stay unchanged. Independent IAM APIs not covered by GAAD (`ListInstanceProfiles`, `ListOpenIDConnectProviders`, `ListSAMLProviders`, `ListServerCertificates`, `ListVirtualMFADevices`, `ListAccessKeys`) keep their own scanners.

## Phase 1 globals + regionals run concurrently

`scanAccount` (`aws_scanner.go`) launches global services and per-region fan-out into a single `WaitGroup` — no barrier between them. Don't reintroduce the `wg0.Wait()` between phases: scanners only upsert (no reads); resolvers in phase 2 are the readers and gate behind the combined wait. Slow globals (IAM with its managed-policy catalogue enrichment) blocked the entire regional fleet under the old gated shape.

## Per-call concurrency constants

`aws_concurrency.go` exports `fanoutHigh` (20), `fanoutMed` (10), `fanoutLow` (2) for `semaphore.NewWeighted(...)` inside scanner/resolver fan-out loops. Distinct from `maxConcurrentServices` (`aws_scanner.go`) which caps top-level service scanners. Do not redeclare `const maxConcurrent` inside individual scanners — pick fanout tier.

## Rate-paced fan-out (`aws_ratepace.go`)

The `fanout*` tiers cap **concurrency**, not **rate** — fine for latency-bound scanners (a handful of calls each). When a fan-out makes a **high call count against a low *documented* per-second API limit** with non-trivial control-plane latency, a fixed semaphore of N under-fills the budget (`N ÷ latency` req/s < the API ceiling). For that one profile, use the shared `pacer` (`newPacer(rps, burst)` + `pacer.wait(ctx)`; optional `reportRateDebug` under `DISCO_SCAN_RATE_DEBUG`): size the worker semaphore **above** `rate × worst-case-latency` so the limiter — not the workers — holds the ceiling. **Do NOT reach for a pacer on ordinary scanners** — it only ever slows them. Sole user: `scanServiceQuotas`.

**One pacer per OPERATION, not per scanner.** AWS meters throttling per API operation, so a scanner making several different calls needs one limiter each — `scanServiceQuotas` uses `sqPacers{services, quotas, defaults}`. It originally shared one limiter across all three and thereby spent a single 10 req/s budget against three separate 10 req/s meters, running the two per-service-code calls at roughly half their allowance. The evidence is in the scanner's own output: Service Quotas publishes `Throttle rate for ListServiceQuotas`, `Throttle rate for ListAWSDefaultServiceQuotas` and `Throttle rate for ListServices` as three distinct quotas of 10. Before pacing a multi-call fan-out, check whether the operations share a meter — assuming they do is the conservative-looking choice that silently halves throughput. `reportRateDebug` takes one pacer, so emit a line per operation or the report cannot say which meter is saturated.

Require a live `DISCO_SCAN_RATE_DEBUG=1` A/B before adding a pacer.

## Dispatch states: `(account: disabled)` / `(account: not entitled)` / `(region: unavailable)` / `(service: blocked)`

The per-service progress suffix comes from `store.ServiceStatus` (`ServiceOK` / `ServiceDisabled` / `ServiceNotEntitled` / `ServiceUnavailable` / `ServiceBlocked`; `ServiceBillingDisabled` is GCP-only), rendered by `cmd/scan.go::serviceStatusSuffix`). A scanner selects one by returning a sentinel from `aws_errors.go`; `classifyServiceError` maps it at dispatch.

- **Decision axis: can the account self-enable the service?** Yes (enable / init / subscribe / onboard / register an SLR) → `markServiceDisabled` (`errServiceDisabled`, `(account: disabled)`). No (closed to new customers, support tier, payer-only, not eligible) → `markServiceNotEntitled` (`errServiceNotEntitled`, `(account: not entitled)`). Azure (`errServiceNotRegistered`) and GCP (`errServiceDisabled`) map their not-enabled states to disabled.
- **Whole service absent from the region** (every op fails) → `markServiceUnavailable` (`errServiceUnavailable`, `(region: unavailable)`), alongside the dispatcher's NXDOMAIN and SSM-catalog skips. Precedent: omics' `isServiceNotAvailableInRegion`. A per-op or sub-feature gap inside a present service keeps a per-phase silent `return 0, 0, nil` — the sentinel would blank a working service.
- **Use a sentinel only when zero rows were produced.** Dispatch calls `ReportService(..., 0, 0, 0, 0, status)` and discards the scanner's total and the bound new/changed counters. After partial work, return `(total, inserted, nil)` instead. Precedent: the member-account path in `scanOrganizations`, which upserts the organization row, then returns nil when `MasterAccountId != acct.ID` instead of hitting the management-only List ops.

Detection shapes:
- **Single code:** Shield `isShieldNotSubscribed`, Security Hub `isSecurityHubNotEnabled`, `AWSOrganizationsNotInUseException` at the top of `scanOrganizations`.
- **Code + message** when the code collides with a real IAM denial: `isMacieNotEnabled` = `isAccessDeniedWithMessage(err, "Macie is not enabled")`; `isAuditManagerNotEnabled`. A real deny still falls through to `skipIfAccessDenied` and warns.
- **Multi-code:** `isControlTowerNotEnabled` matches the message hint first (`AWSControlTowerAdmin` / `landing zone` / `management account`), then accepts `isAccessDenied` OR `ValidationException`; the message check keeps real validation errors surfacing.
- **Only the phase-1 detection step returns the sentinel** (`DescribeSubscription` / `DescribeHub` / `GetMacieSession`); later phases keep a tolerant per-phase `isAccessDenied` skip for partial IAM grants. The top-level scanner propagates it via `if ferr != nil { return 0, 0, ferr }`.
- **Gate-only phase-0:** `gateXxx(ctx, client, acct, st) error` calls Describe only for the sentinel side-effect and upserts nothing. Precedent: `gateShieldSubscription`.
- **Shared predicate** when two services gate on one enablement: `isCostExplorerNotEnabled` (`bcmpricingcalculator_scanners.go`) serves both `aws:ce` and `aws:bcmpricingcalculator`.

## Single-region globals → `global: true`, hardcode home in client

AWS exposes some services with account-wide scope but a single regional endpoint (IAM, Route53, CloudFront, Globalaccelerator us-west-2, Budgets us-east-1, Route53 Recovery us-west-2, Route53 Global Resolver us-east-2, etc.). Register these with `global: true` on the `serviceEntry`; the dispatcher (`aws_scanner.go::scanAccount`) calls them once per account with `region=""`, regardless of `--regions`. Inside the scanner, hardcode the home in the SDK client option-fn:

```go
region := "us-west-2"
client := globalaccelerator.NewFromConfig(acct.cfg, func(o *globalaccelerator.Options) { o.Region = region })
```

Use **short var decl `region := "X"`**, not `const region = "X"`. Untyped string constants are not addressable, so `&region` for `Region: *string` in resource batch fields fails to compile (`cannot take address of region (untyped string constant ...)`). The short-decl form is gofmt-stable and lint-clean.

Substituting the home in a local variable (rather than relying on the dispatcher arg) keeps `Resource.Region`, error scopes, and `skipIfAccessDenied` reports accurate. Precedents: `route53_scanners.go`, `globalaccelerator_scanners.go`.

**Anti-pattern (deprecated):** registering a single-region global as **regional** with an inline `if region != "<home>" { return 0, 0, nil }` early-return. The pattern was historically used to dodge per-region NXDOMAIN warnings, but `isDNSNotFound` at the dispatcher (`aws_errors.go`) already silent-skips those. The inline-gate pattern silently produces zero rows when `--regions` excludes the home (`disco scan aws --regions us-east-1` would skip globalaccelerator entirely). Convert to `global: true` instead.

Inline `if region != "X"` gates remain correct only for **sub-op** cases — a regional service with one specific op pinned to a single region (Lightsail Distributions/Domains in us-east-1, ECR registry-scanning in us-east-1, Direct Connect Gateway in us-east-1). See "Per-op region gates" below.

CLI: `--skip-globals` (defined in `cmd/scan.go`, plumbed via `providers.GlobalsSkipper`) suppresses every global service regardless of registration shape — for data-residency / per-region audits.

## `--services` flag values come from `name:` field, not file stub

The string passed to `disco scan aws --services X` must match the `name:` field of the corresponding `registerService(serviceEntry{...})` call, not the file basename. Drift examples: `costexplorer_scanners.go` → `aws:ce`; `supportapp_scanners.go` → `aws:support-app`; `licensemanager_scanners.go` → `aws:license-manager`; `networkmanager_scanners.go` → `aws:networkmanager` (no dash); `notificationscontacts_scanners.go` → `aws:notifications-contacts`. Authoritative listing: `grep -hE 'name: *"aws:' internal/providers/aws/*_scanners.go | sort -u`.

## Multi-phase parent + children closure-wiring helper

Scanners modeling per-(acct,region) singleton parent with N child phases (Macie session + jobs/CDIs/allow-lists, Security Hub hub + insights/standards/product-subs) factor closure-wiring into one `upsertXChildren(st, parentARN, acct, batch, kind)` helper. Helper does `UpsertResources(batch)` + `RecordHierarchyBatch([][2]string{{child.ID, parentID}})` together. Don't inline per phase — three+ duplicated copies of same closure-pair build = sign to extract. Precedent: `upsertMacieChildren` (`macie_scanners.go`), `upsertSecurityHubChildren` (`securityhub_scanners.go`).

## Region-scoped FK-safe id sets

When target NativeID not deterministic per (acct, region) (e.g. multiple `aws:guardduty:detector` rows per region; `aws:config:configuration-recorder` arbitrary name), use `scannedIDsByRegion(acct, st, type) → map[region][]resourceID` instead of flat id set. Singleton-per-region services (`aws:macie:session` via `macieSessionNativeID`) keep flat `scannedIDSet`. Both helpers in `securityhub_resolvers.go`. Same FK-safety guarantees as flat-set pattern; emits one edge per scanned target in region rather than guessing NativeID.

## Multi-phase scanner totals

Each phase returns `(total, inserted, err)`. `total = len(batch)` (rows scanned), `inserted = n` from `UpsertResources` (rows newly inserted, excludes upserts of existing). Never return `len(batch)` for both — the scan-progress line's `total` column reports nonsense on rescans otherwise.

Scanners still return `(total, inserted, err)`; `inserted` is vestigial for reporting (new/changed come from `WithUpsertCounters`, see `../CLAUDE.md`).

## Cross-service ResourceArn ≠ scanner NativeID shape

Security-overlay services (Shield, GuardDuty findings, Inspector findings) emit `ResourceArn` refs in canonical AWS shape that may differ from per-service scanner's NativeID shape. Example: Shield emits EIP as `arn:aws:ec2:{r}:{a}:eip-allocation/eipalloc-xxx`, but `ec2_networking_scanners.go` stores `arn:aws:ec2:{r}:{a}:elastic-ip/eipalloc-xxx`. Normalise at classify time (`strings.Replace(arn, ":eip-allocation/", ":elastic-ip/", 1)`) before `store.ResourceID` lookup, or every edge is silently dropped by the scanned-set check. See `classifyShieldProtectedResource` in `shield_resolvers.go`.

## CFN `PhysicalResourceId` shape varies per ResourceType

Adding entries to `cfnTypeMap` (`cloudformation_resolvers.go`): full ARN for some (Lambda, SNS, ELBv2, SFN, SecretsManager, Lambda layer), bare name/ID for others (S3, IAM, EC2 *, KMS key, DynamoDB, Logs, ECR, Kinesis, RDS, EFS, EventBridge), queue URL for SQS, ID-only for APIGW. Verify shape against CFN resource-ref docs per type — wrong synth = phantom NativeID, FK-safe lookup silently drops edge with no error. Custom-bus EventBridge rules cannot resolve from physID alone (no bus context); reject pipe-form `BUS|NAME` rather than synth wrong ARN.

## Adding new AWS SDK service module

`go get github.com/aws/aws-sdk-go-v2/service/<svc>@latest` then `go mod tidy`. Service modules version-independent of base SDK; no pin needed.

## Coverage: SDK universe first, registries only under `--cross-check`

`disco coverage services --providers aws` derives every listable resource from the pinned aws-sdk-go-v2 Smithy models + Service Reference in the SDK cache and pairs them with the scanner source (`internal/sdkinv/CLAUDE.md`, `internal/coverage/CLAUDE.md`). Nothing here is hand-listed (retired list: `../CLAUDE.md`). A skipped-for-a-reason resource (ephemeral job records, retired services the SDK still ships) is an honest `uncovered` row the committed baseline accepts.

`coverageProvider.CrossCheck` (`aws_coverage.go`, `--cross-check` only) returns the **union** of two live catalogs, because neither is complete:

- **CloudFormation ListTypes** (`Visibility=Public, Type=Resource`) — needs AWS creds; lists only resources with a CFN provider. Misses SDK-real resources like DynamoDB streams, AuditManager controls, IdentityStore users, Macie classification jobs, service quotas.
- **AWS Service Reference** (`aws_servicereference.go`) — the credential-free public JSON form of the IAM Service Authorization Reference (`https://servicereference.us-east-1.amazonaws.com/`, ~451 services, ~2250 resource types). Supplies the CFN-absent reals above, but itself omits real CFN-modeled resources (e.g. SecurityHub `insight`, `delegated-admin`).

Service Reference entries are synthesized into the same `AWS::<service>::<resource>` shape as CFN, and `CanonicalKey` (`sdkinv.Canon` service + `sdkinv.Ident` resource, "Resource" suffix dropped) collapses the twins onto the candidate's `RegistryKey`. Both fetches are **fatal on failure** (the `errCoverageRegistryUnreachable` → exit-2 contract) — the union requires both, so a partial fetch can't silently report drift. No hand rename map: the extractor derives `Universe.ServiceAliases` from each model's `sdkId`/`arnNamespace`/`endpointPrefix` and the SR document joined by operation names (`MWAA`→`airflow`, `ApiGatewayV2`→`apigateway`, `CloudWatch`→`monitoring`); an alias that is itself a candidate service or claimed by two services is dropped. Services still unmatched are printed on stderr, not reported. `markCFNOnly` tags CFN types with no SR twin `cfn-only` (mostly template constructs like `SecurityGroupIngress`) and respells the CFN service the SR way; keep CFN in the union — its nouns match keys the SR renames (`elasticache/cachecluster`).

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

**Paginator iface trick**: `New<Op>Paginator(client, ...)` constructors only require the underlying `<Op>` method on `client`, not a per-paginator interface. Listing the underlying `DescribeXxx` / `ListXxx` / `GetXxx` / `SearchXxx` op on `<svc>API` satisfies every paginator constructor that wraps it. Local dispatcher func-type aliases take the iface, not `*svc.Client` (precedent: `perFnScanner` in `lambda_scanners.go`).

**Footgun — duplicate client line**: when introducing a `scanXBody(ctx, client xAPI, ...)` wrapper, move the `client := <svc>.NewFromConfig(...)` construction up into `scanX` and DELETE it from the body (and from every sub-fn that built its own client, cloudwatch-style). Leaving it causes "no new variables on left side of :=".

**Shared package-level iface (ec2 pattern)**: services with 10+ scanner files and 50+ ops (only EC2 today) get ONE shared `<svc>API` in `<svc>_iface.go` rather than per-file ifaces. Add a new ec2 op to `ec2_iface.go` after confirming the SDK has it (`go doc github.com/aws/aws-sdk-go-v2/service/ec2.Client.<Op>`).

**Multi-SDK service (cognito + sso pattern)**: a scanner spanning two SDK packages (Cognito = `cognitoidentityprovider` + `cognitoidentity`; SSO = `ssoadmin` + `identitystore`) declares two narrow ifaces; the wrapper constructs both clients and forwards to a `scanXAll(ctx, client1, client2, ...)` body.

## Cross-account member-row → org account edges

Services that model multi-account membership (Inspector v2, Detective, GuardDuty, future SecurityHub member, Macie member) get a per-member resource type (`aws:<svc>:member`) and a resolver that emits `attached-to` → `aws:organizations:account` via `loadOrgTargetIndex`. Members are scanned even when the org tree is not — the resolver short-circuits when the index is empty (no edges, no error). Precedent: `inspector_resolvers.go::resolveInspector2MemberOrgAccount`, `guardduty_resolvers.go::resolveGuardDutyMemberOrgAccount`. Member NativeID shape: `arn:aws:<svc>:{r}:{a}:detector/{id}/member/{memberAcctId}` (or analogous synthetic when no AWS-issued ARN exists).

## Per-target embedded fan-out

When a parent resource references N children that have no independent lifecycle (Control Tower baseline → enabled-controls per OU, Backup plan → selections, EventBridge rule → targets), fetch children at scan time and embed under a key in the parent's `AttributesJSON` (`{"Baseline": ..., "EnabledControls": [...]}`). Per-target AccessDenied / ValidationException tolerated via `skipIfAccessDenied` — never propagate per-target errors during fan-out, or one missing OU breaks the whole baseline upsert. Precedent: `controltower_scanners.go::listEnabledControlsForTarget`.

## Multi-hop role chaining

`accountCfg.RoleChain []string` (preferred) walks N assume-role hops in order — each step's STS client is built from the prior step's `CredentialsCache`. `RoleARN` (single string) remains the single-hop path; `role_chain` takes precedence when both are set. Helper: `chainAssumeRoles(baseCfg, roleARNs, sourceIdentity)` in `aws_config.go`. Use for hub-and-spoke org topologies where the runner must hop through an Audit role before reaching the target account.

## Tag JSON helpers

`awsTagsJSON[T awsTag]` (`aws_tags.go`) is generic-union restricted. New SDK service tag types (`sesv2types.Tag`, `lakeformationtypes.Tag`, etc.) must be added to `awsTag` union AND new `case` in `switch tt := any(t).(type)` block — both edits or helper drops tags silently. For map-typed tags (Macie `map[string]string`, ECR repo tags map) use `mapTagsJSON` instead. Defer tag plumbing if scope tight; tags rarely block graph analysis and adding union touches global type list.

## Helper-test colocation

Cross-cutting pure-helper tests (ARN builders, error predicates, tag helpers, transient classifier) live in `aws_arn_test.go`, `aws_errors_test.go`, `aws_tags_test.go`. Per-service helper tests (e.g. `apprunnerImageToRepoARN`, `instanceArnFromPermissionSetArn`) live in the matching `<svc>_resolvers_test.go`. Before adding a new helper test, grep `^func Test<Helper>` across `aws/*_test.go` — duplicate `TestX` in same package fails to compile.

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

Embedding (not nesting) flattens the SDK fields back to top level so existing resolvers reading `Role`/`KMSKeyArn`/`VpcConfig` keep working. Sibling key (`Code` here) carries the enrichment. Cheaper than a per-row Describe when only one or two fields are needed and the Describe call is conditional. Precedent: `lambdaFunctionAttrs` (lambda_scanners.go).

## ARN slot indexing after `strings.Split(arn, ":")`

For colon-separated ARNs `arn:aws:<svc>:<region>:<acct>:<rtype>:<id>` Split returns 7 parts: `[0]=arn [1]=aws [2]=svc [3]=region [4]=acct [5]=rtype [6]=id`. Slash-separated ARNs (`...:rtype/id`) keep `[5]="rtype/id"`. When dispatching by resource-type segment, compare `parts[5] == "cluster"` (exact) — `strings.HasPrefix(parts[5], "cluster:")` always fails because Split already consumed the colon. Precedent: `lambdaESMSourceType` DocDB branch (lambda_resolvers.go).

## Wrapper-key json tags are lowercase by design

The "Scanner attribute JSON uses PascalCase keys" rule applies to fields produced by `json.Marshal` on raw SDK structs. Hand-built wrapper containers that namespace the SDK payload (`{"lb": <LB>, "type": ...}` in `elb_scanners.go`, `{"rule": ..., "listenerArn": ...}` and `{"listenerArn": ..., "cert": ...}` in `elb_scanners.go`; EventBridge's `ruleWithTargets` is PascalCase `Rule`/`Targets`) deliberately use lowercase / camelCase outer keys to distinguish them from SDK fields. Resolver struct tags like `json:"lb"` / `json:"listenerArn"` / `json:"deadLetterTargetArn"` are correct — do not "fix" to PascalCase, and any tag-shape lint must allowlist these.

## EventBridge resolver — `EventBusArn` is dead path

`eventbridge_resolvers.go` reads `attrs.Rule.EventBusArn` first then falls back to `EventBusName`. SDK `eventstypes.Rule` has no `EventBusArn` field and `eventbridge_scanners.go` never synthesizes one — real scans always take the EventBusName fallback. Tests using hand-rolled JSON with `EventBusArn` work in isolation but exercise no production code path. Use `EventBusName` in fixtures and mirror the synthesis the resolver does (`arn:aws:events:{r}:{a}:event-bus/{name}`).

## Wrapper-shape test fixtures — `aws_testhelpers_test.go`

Scanner-side wrapper containers (`{"lb": <LB>, "type": ...}` in `elb_scanners.go`, `tgWithTargets`, `ruleWithTargets`, etc.) are declared as function-local types so tests cannot reuse them directly. Build resolver-test `AttributesJSON` via the helpers in `aws_testhelpers_test.go` (`elbv2LBAttrs`, `elbv2TargetGroupAttrs`, `eventBridgeRuleAttrs`) — they take real SDK types so wrapping-shape drift surfaces in tests rather than as silent zero-value resolutions in production. New wrappers go here, named `<svc><Resource>Attrs`.

## VPC subtree wired via `attached-to`, not `contains`

`ec2_networking_resolvers.go` emits `child → vpc` `RelAttachedTo` for subnet, IGW, route-table, NAT gateway, VPC endpoint, network ACL, peering. No `RecordHierarchyBatch` call wires VPC as a closure parent — VPC has zero `contains` rows. Graphs and `disco resources --hierarchy` see VPC only via the reverse `attached-to` edges. Add hierarchy wiring deliberately if a feature needs VPC→child closure traversal.

## Probe-first for low-TPS multi-phase services

Services with ≥20 sub-phase List ops AND a low per-account TPS quota (SageMaker is the canonical case) burn minutes when adaptive retry's token bucket throttles — every phase pays the penalty even on dormant accounts. Add a phase-0 probe of 2-3 cheap `MaxResults=1` List ops covering the highest-signal surfaces; short-circuit the full fan-out on empty. Precedent: `sagemakerInUseProbe` (sagemaker_scanners.go) probes ListDomains + ListNotebookInstances + ListEndpoints. Accept false negatives (pipelines-only / training-jobs-only accounts) for the wall-time win.

## Expected-state singletons → silent no-op, not warn

Singleton-config Get/List ops return distinct error codes when the config has not been opted into (`SigningConfigurationNotFoundException`, `NotConfiguredException`, `RegistryPolicyNotFoundException`, `ResourceNotFoundException`, `TagOptionNotMigratedException`, `UnauthorizedException` from org-only APIs called by non-mgmt accounts). These are the **default state**, not warnings. Return `(0, 0, nil)` directly — do NOT route through `skipIfAccessDenied`, which records a `ScanWarning` and clutters the per-region warnings block. Real IAM denies still warn via `isAccessDenied`.

## AppStream DescribeUsers: USERPOOL is the only accepted auth type

`DescribeUsers` takes exactly one `AuthenticationType`, and it is `USERPOOL` — the SDK's own field doc says *"You must specify USERPOOL"*. Do NOT iterate the enum. `appstreamtypes.AuthenticationType` is the shape shared with `CreateUser`/`EnableUser`/`BatchAssociateUserStack` and now carries four values (USERPOOL, API, SAML, AWS_AD); the other three name nothing a user pool holds — an API-auth session has no user record (`CreateStreamingURL` mints the URL) and SAML/AWS_AD users are federated — so each is a guaranteed `InvalidParameterValueException`.

## `isAPIErrorCode` compares SHAPE NAMES, so never pass a namespaced code

`isAPIErrorCode` (`aws_errors.go`) routes `ae.ErrorCode()` through `errorShapeName`, which strips anything up to the last `#`. Pass the bare name (`"AccessDenied"`), never `"com.amazonaws.x#AccessDenied"` — the latter would never match after stripping.

It exists because rpc-v2-cbor deserializers do not sanitize. Their switch matches modeled errors by shape name, but the `default:` fallthrough for an error the operation does NOT model returns `smithy.GenericAPIError{Code: typ}` carrying the raw `__type` — so an unmodeled error arrives as `com.amazon.coral.service#InvalidParameterValueException` while every awsjson/restjson service arrives bare. Six services disco scans are on that protocol today: `appstream`, `applicationinsights`, `backupgateway`, `kendraranking`, `mailmanager`, `workspacesinstances`. More will follow, and the failure is silent — `isAccessDenied`, `isServiceBlocked` and the `withNonRetryableCodes` retry guard all read codes through this path, so a missed match turns a warn-skip into a hard error or burns a retry budget.

## Resolver-edge metadata: `EdgeDecl`

The generic `EdgeDecl` contract lives in `../CLAUDE.md` ("Resolver EdgeDecl contract"). AWS tooling:

- `disco coverage resolvers --providers aws [--only-unannotated]` — per-resolver edge counts.
- `disco coverage resolvers --missing --providers aws [--with-refs]` — emitted disco types with no `EdgeDecl.Source` mention, each with the id/ARN fields its SDK element carries (`VpcConfig.SubnetIds`, `KmsKeyId`), richest first. The candidate gap inventory: pick from the top; a type leaves the list when a resolver's `EdgeDecl` names it as Source. `--with-refs` requires the SDK cache and reports the ref-less count; it hides nothing.
- `go run ./cmd/aws-resolver-audit/ --list-edges` — every declared (src, tgt, kind) triple.
- `go run ./cmd/aws-resolver-audit/ --db <path>` — diffs declared metadata + DB edges against ARN/ID refs walked from `AttributesJSON`.

Audit workflow (no checked-in snapshot; the SDK-derived list is reproducible): scan a representative account (`disco scan aws --regions us-east-1,us-west-2,eu-west-1`), run `go run ./cmd/aws-resolver-audit --db <path> --top 100` for `(source → target)` pairs whose `AttributesJSON` carries a ref but no edge exists, and `disco coverage resolvers --missing --with-refs --providers aws` for the orphan types worth a resolver.

## NativeID parent-extraction = dominant child→parent shape

Most service hierarchies encode the parent in the child's NativeID via `{parentARN}/<kind>/<id>` (Cognito user-pool children, Logs streams / metric-filters / subscription-filters / transformers, Deadline farm children, AppSync per-API children, MediaConnect VPC interfaces, Connect instance children, Glue partitions). Resolver pattern: `strings.Index(arn, "<segment>/")` + slice up to that point; wire one resolver per child cluster with `EdgeDecl`, FK-safe via `scannedIDSet(parentType)`. No need to walk parent attrs — the child already carries the link in its NativeID.

## Don't parallelize agents over shared resolver registries

`<service>_resolvers.go` `init()` blocks are append targets for every resolver added. Dispatching parallel agents that each write to the same `init()` produces registration calls without function bodies when one agent runs out of usage budget mid-task — the package fails to compile with N undefined symbols. Either dispatch one agent per service file, or have agents create new `<service>_extended_resolvers.go` files (separate `init()` blocks merge cleanly).

## Per-op region gates (sub-API only available in one region)

Some scanners are regional, but a subset of their ops only work in a specific region — Lightsail's `GetDistributions` and `GetDomains` are us-east-1-only while the rest of the Lightsail surface is regional. AWS rejects from other regions with `InvalidInputException: ${kind}-related APIs are only available in the ${region} Region`. Gate per-phase (`if region != "us-east-1" { return 0, 0, nil }` at the top of `scanLSDistributions`), not via `global: true` on registerService — that would skip the regional ops too. Precedent: `lightsail_extended_scanners.go` `scanLSDistributions` / `scanLSDomains`.

## DNS probe to confirm global-service region

Before relying on AWS docs (or existing scanner code) for which region a global service lives in, probe with `getent hosts <svc>.<region>.amazonaws.com` across candidate regions — only the correct endpoint resolves; others return NXDOMAIN. Session live-scan revealed three scanner errors this way: `route53-recovery-readiness` + `route53-recovery-control` are us-west-2 only (not us-east-1), and `route53globalresolver` is us-east-2 only on the `.api.aws` TLD.

## One upsert per resource per scan

`resources` has no ON CONFLICT DO UPDATE: a second upsert of the same resource in one scan with different attributes is a version split (see `store/CLAUDE.md`). Build the row once from the detail (`Describe*`) body.

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

## Re-verify "blocked" comments before trusting them

Scanner comments claiming a reference is unusable ("refs blocked by sanitize", "refs need Describe enrichment", "no SDK list op") rot: redaction is now per-type and per-path (each type's `registerType` descriptor `Redact` field); ARN-bearing fields like `CredentialsArn` / `SecretArn` / `TokenSourceArn` / `AuthorizationHeaderArn` are preserved by *omission* (no rule targets them). Before adding a sidecar workaround for what a comment says is "blocked", read the type's descriptor `Redact` and confirm the field actually has a rule on it; if not, the resolver can read it directly. Same applies to "no Describe op" claims — SDK additions land after the comment was written.

## Parent-row "leaf" ≠ no edges

Many parent types (mediaconnect:flow, kinesis:stream, eventbridge:rule) stay on `coverage resolvers --missing` because their existing resolvers emit *child→parent* `attached-to`, not parent→outbound. To demote, identify a NEW outbound edge from the parent's own SDK body to a *non-child* type — RelContains/closure to children doesn't count. Precedents: `mediaconnect:bridge → mediaconnect:gateway` via `PlacementArn` demoted the parent; `mediaconnect:flow` stayed listed because every Flow body field maps to an existing child type. Confirm via SDK doc grep on the body struct *before* scanner enrichment work — if every ref-bearing field is already spawned as a child row, the parent is genuinely leaf.

## Empty-message AccessDeniedException = closed-to-new-customers signal

When AWS retires a service to new customers (existing customers keep access), list ops on unregistered accounts return `AccessDeniedException` with an *empty message body* — distinct from real per-op IAM denials which always carry an action-identifying message. Detect via `isClosedToNewCustomers(err)` (`aws_errors.go`). Closure state is **non-uniform across ops on the same account** (`ListCampaigns` may succeed while `ListDecoderManifests` fails). Wire two layers: phase-0 gate for the common case (one cheap probe → `markServiceNotEntitled` if empty-msg — the account can't self-enable a closed service, so this is not-entitled, not disabled) AND per-phase silent return `(0,0,nil)` for the partial-uniformity case. Real IAM denials with messages still warn via `skipIfAccessDenied`. Precedent: `gateIoTFleetWise` + `gateFraudDetector` (per-service gate fns) calling shared `isClosedToNewCustomers`. Whole-service closed detections (kendra `isKendraClosedToAccount`, interconnect `isInterconnectClosedToAccount`, timestream `isTimestreamLiveAnalyticsClosed`, voiceid `isVoiceIDNotEnabled`, cloud9, datapipeline) likewise return `markServiceNotEntitled`. See "Dispatch states" above.

## Two-pass scanner: keep total == inserted via skip-set dedup

Multi-pass scanners that pre-stub catalogue rows then re-upsert with rich detail (e.g. IAM AWS-managed policy catalogue + GAAD pass) inflate the per-service progress line on a fresh DB: `total = len(batch)` counts both upserts, but `inserted` only counts the first because the second upsert is a verify or split, not an insert. Surfaces as `(1520 total, 1508 new)` → confuses users into thinking the scan was partial. Fix: reverse pass order so the *rich* pass runs first and captures the dedup ARN set, then the *stub* pass filters its batch via `if skipARNs[arn] { continue }`. Each row upserted exactly once; total == inserted on fresh DB. Precedent: `scanIAMAuthDetails` + `scanIAMAWSManagedCatalogue`.

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
anyway). `serviceAvailableInRegion` unions the catalog with the shipped SDK endpoint table
(`regions.ServiceAvailable`, which also scopes single-region scans): scan if either lists
the region, skip only if some source has an opinion and none lists it, scan when neither
knows (nil map, divergent code, empty set). The service code derives from the registerService name minus `aws:`;
divergent names (e.g. `aws:code-build` vs catalog `codebuild`) simply aren't found
→ scanned everywhere. `regionAvailabilityCodeOverrides` (near-empty — only services whose SDK package ships
no endpoint table, e.g. `aws:bedrockagentcore`) is the unlock for divergent services — only add a mapping VERIFIED against the live
catalog, since a wrong override is the one way to skip a region the service serves.
This complements, never replaces, the per-region silent-skip predicates (NXDOMAIN,
feature-gap codes) — those catch sub-feature gaps the service-level catalog can't.
Toggle: `--scope-regions=false` (or `aws.scope_to_available_regions: false`).
Capability: `providers.RegionScopeToggler`.

## Dispatch ladder lives in `classifyServiceError`, and its order is load-bearing

`scanAccount`'s global lane and `scanRegion`'s per-region lane classify a failed `svc.fn`
identically and differ only in the scope label they report, so the decision is one function in
`aws_errors.go` returning a `serviceOutcome` (ladder order: `outcomeStoreWrite` / `Disabled` / `NotEntitled` /
`Unavailable` / `Blocked` / `Deadline` / `Transient` / `Error`); both dispatchers just `switch` on it.
`Unavailable` covers NXDOMAIN (`isDNSNotFound`) — real DNS server problems surface as timeouts /
SERVFAIL, not NXDOMAIN, and still warn. `Blocked` (`isServiceBlocked`: a 403 whose message matches
`blockedServiceNeedles`) silences a service AWS refuses to everyone. `Deadline` must precede
`Transient` — a deadline satisfies `net.Error.Timeout()`. Add a new rung
there, not inline in a dispatcher — and add a case to `TestClassifyServiceError_Rungs`.

`outcomeStoreWrite` is first for the reason in `../CLAUDE.md` ("`store.ErrStoreWrite` is the first rung"); `TestClassifyServiceError_StoreWriteBeatsEveryOtherRung` pins it.

## Per-region feature-gap error codes are service-specific

AWS surfaces "this sub-feature is not deployed in this region" under different codes per service. Build a code+message silent-skip predicate against the exact code observed and return `(0, 0, nil)` — not `markServiceDisabled`, since other phases still work in the region. Known shapes:

- `UnsupportedRegionException` (gamelift Containers + FlexMatch)
- `InvalidRequestException` + "Feature not supported yet" (iotsitewise)
- `InvalidAction` "Operation not supported" (cloudwatch GetOTelEnrichment)
- `ValidationException` + "Member must satisfy enum value set" (App Auto Scaling per-region namespace enum)
- `AccessDeniedException` + canned message linking docs URL (workspaces:DescribeWorkspacesPools)
- `InternalFailure` 500 post-retry (quicksight ListActionConnectors)
- `InvalidParameterValueException` + "Access Denied to API Version" (DAX V3 control plane)

Guard **every phase** of a multi-phase scanner, not just the first. The DAX V3 gap rejects `DescribeClusters` / `DescribeParameterGroups` / `DescribeSubnetGroups` alike, but only the clusters phase carried the guard — the other two surfaced as hard scan errors in every non-V3 region.

Empty-message `AccessDeniedException` is a separate signal (closed-to-new-customers — see iotfleetwise / interconnect precedents).

## SDK paginator `Limit=0` nils MaxResults → 400 ValidationException

`New<Op>Paginator` constructors that expose a `Limit` paginator-option overwrite `params.MaxResults` to `nil` when `Limit==0` (default). Some AWS APIs reject the resulting empty MaxResults with `Value '0' at 'maxResults' failed to satisfy constraint`. Always pass an option-fn setting `o.Limit` to a valid page size: `NewListXxxPaginator(client, in, func(o *svc.ListXxxPaginatorOptions) { o.Limit = 100 })`. Precedent: `scanQSActionConnectors` (quicksight_scanners.go).

## Clamp retries per-op for ops that return persistent 5xx

Global config sets `WithRetryMaxAttempts(10)` + adaptive backoff for low-TPS services like IAM. Newer/region-gated ops sometimes return `InternalFailure` 500 (instead of clean 4xx) when their feature is not deployed; the global budget then burns ~2m before the call returns. Clamp on the offending op via paginator `NextPage` optFn or direct call optFn: `func(o *svc.Options) { o.RetryMaxAttempts = 2 }`. Pair with adding the post-retry code to the soft-skip predicate. Precedent: `scanQSActionConnectors` clamps + soft-skips `InternalFailure`.

## AccessDenied disambiguation — canned doc URL = per-region feature gap

Real per-action IAM denials carry the SDK-formatted body `User: arn:... is not authorized to perform: <action> on <resource>`. AWS's "this feature is not in this region" canned response is a different shape: generic "You do not have the permissions required to perform this action" + a docs URL marker. Detect via `isAccessDeniedWithMessage(err, "<doc-url-fragment>")` (`aws_errors.go`) and silent-skip. Real denials still warn via `skipIfAccessDenied`. Precedent: `isWorkSpacesPoolsGap` (`aws_errors.go`, matching `workspaces-access-control.html` and `wsp-pools-end-of-support.html`), called from `scanWSWorkspacesPools`.

## Terminated EC2 instances drop `IamInstanceProfile` (and other volatile attrs)

`DescribeInstances` returns terminated instances for ~1 hour after termination, but AWS clears volatile attributes — `IamInstanceProfile`, post-cleanup network-interface specifics, attached-volume mounts — on the way out. The EC2 instance row in disco's DB faithfully reflects the post-termination state, so resolvers that read those fields (instance → instance-profile, instance → ENI, instance → EBS) correctly emit no edge for terminated instances. If `disco graph blast <iam-role>` returns fewer hops than expected for a role attached to a recently-terminated instance, check `attributes.State.Name == "terminated"` before chasing a resolver bug. Not a scanner bug — the scanner stores what AWS returns.

Filtering terminated instances out of inventory entirely is a separate product question (TTL? user opt-in?); today disco keeps them so the user can see "this terminated yesterday" in `disco resources`. Use a Rego rule (`input.attributes.State.Name == "terminated"`) to surface them as findings if your policy demands cleanup.

## AWS account_id resolution + emulator override

`internal/providers/aws/aws_config.go::loadAccounts` resolves the `account.ID` recorded on `resources.account_id` via three precedences:
1. Config-file `aws.accounts[].id` (explicit).
2. `--role-arn` override → `sts:GetCallerIdentity` on the assumed creds.
3. Auto-detect → `sts:GetCallerIdentity` on the default chain.

`DISCO_CLOUD_ACCOUNT_ID` is an **emulator-only** override that short-circuits the STS lookup. **Honored only when `AWS_ENDPOINT_URL` is also set** — the AWS SDK's canonical "talking to a non-AWS endpoint" signal. Prod scanners never set `AWS_ENDPOINT_URL`, so the env is inert outside emulator mode. Emulators (e.g. LocalStack) return a sentinel `"000000000000"` from `sts:GetCallerIdentity` that would otherwise overwrite the configured account id on every emulator-backed scan.

The gate function `emulatorAccountIDOverride()` is the single read site; both the `--role-arn` branch and the auto-detect branch call it before STS. `TestEmulatorAccountIDOverride` in `aws_config_test.go` pins the prod-safety assertion (env value ignored when `AWS_ENDPOINT_URL` is unset).
