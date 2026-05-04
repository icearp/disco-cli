# CLAUDE.md — `internal/providers/aws/`

AWS scanner + resolver conventions. Cross-provider rules: see `../CLAUDE.md`.

## Resolver conventions

- **Scanner attribute JSON uses PascalCase keys.** `mustJSON` calls `json.Marshal` on AWS SDK v2 response structs, no json tags — `ClusterArn` stays `ClusterArn`, not `clusterArn`. Resolver structs need PascalCase tags (`json:"ClusterArn"`) or silent match nothing on real scan data while tests pass on hand-rolled JSON.
- **ARN helpers** (`arn.go` + service files): `ec2ARN(region, acct, kind, id)` → `arn:aws:ec2:{r}:{a}:{kind}/{id}` (slash sep). `rdsARN(region, acct, kind, id)` → `arn:aws:rds:{r}:{a}:{kind}:{id}` (colon sep). `apigatewayARN(region, path...)` → `arn:aws:apigateway:{r}::/p1/p2/...` (empty account, variadic path joined `/`). Resolvers rebuild target ARN from native ID, pass to `store.ResourceID(...)`. Wrong shape = phantom target, buried FK error.
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

`isAPIErrorCode(err, codes ...string) bool` in `aws.go` = single choke point (wraps `errors.As` + `smithy.APIError.ErrorCode()` + `slices.Contains`). Use inline for one-off checks. Wrap in named helper only when reused 3+ times (precedent: `isAccessDenied` wraps 6 codes, 146 callers). Predicates needing code + message-substring match (e.g. `isCacheSecurityGroupsNotPermitted`) stay one-off — outside helper's shape.

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

## IAM scan uses GetAccountAuthorizationDetails (single paginated call)

`scanIAMAuthDetails` (`iam_scanners.go`) consolidates users + roles + groups + managed policies (Local + AWS scope, including each policy's default version `Document`) + every principal's inline policies into one paginated `iam:GetAccountAuthorizationDetails` (GAAD) call with `MaxItems=1000` + a `Filter` listing all five entity types.

**GAAD `AWSManagedPolicy` filter only returns *attached* AWS-managed policies, NOT the full catalogue.** `scanIAMAuthDetails` runs `scanIAMAWSManagedCatalogue` first — a stub `ListPolicies(Scope=AWS)` pass that upserts a metadata-only row per AWS-managed policy (no `GetPolicyVersion` enrichment, avoids the throttling fan-out). The subsequent GAAD pass overwrites attached managed-policy rows with the version-enriched form; unattached catalogue policies keep their stub row as FK targets for `resolveManagedPolicyAttachments`. Walker silently skips no-document rows. Don't drop the catalogue stub pass — without it, unattached AWS-managed policies disappear from the store. Replaces the pre-GAAD shape (separate `ListRoles` / `ListUsers` / `ListGroups` / `ListPolicies` + per-policy `GetPolicyVersion` fan-out + per-principal `ListRolePolicies` / `ListUserPolicies` / `ListGroupPolicies` + per-name `Get*Policy`). Don't reintroduce those — the old fan-outs hit IAM TPS throttling and dominate scan wall-time. AWS-managed policies detected via the `arn:aws:iam::aws:` ARN prefix (canonical scope marker; GAAD doesn't expose the per-policy scope flag). Inline policies still upsert as `aws:iam:{role,user,group}-policy` rows with NativeID `{parentARN}/policy/{name}` so existing inline-policy resolvers stay unchanged. Independent IAM APIs not covered by GAAD (`ListInstanceProfiles`, `ListOpenIDConnectProviders`, `ListSAMLProviders`, `ListServerCertificates`, `ListVirtualMFADevices`, `ListAccessKeys`) keep their own scanners.

## Phase 1 globals + regionals run concurrently

`scanAccount` (`aws.go`) launches global services and per-region fan-out into a single `WaitGroup` — no barrier between them. Don't reintroduce the `wg0.Wait()` between phases: scanners only upsert (no reads); resolvers in phase 2 are the readers and gate behind the combined wait. Slow globals (IAM with its managed-policy catalogue enrichment) blocked the entire regional fleet under the old gated shape.

## Per-call concurrency constants

`concurrency.go` exports `fanoutHigh` (20), `fanoutMed` (10), `fanoutLow` (2) for `semaphore.NewWeighted(...)` inside scanner/resolver fan-out loops. Distinct from `maxConcurrentServices` (`aws.go`) which caps top-level service scanners. Do not redeclare `const maxConcurrent` inside individual scanners — pick fanout tier.

## Service-not-enabled → markServiceDisabled sentinel

Some AWS APIs return distinct exception code when account has not subscribed to / activated feature — Shield Advanced (`ResourceNotFoundException` via `isShieldNotSubscribed`), Security Hub (`InvalidAccessException` / `ResourceNotFoundException` via `isSecurityHubNotEnabled`). Phase-1 detection step returns `markServiceDisabled(err)` (`aws.go`) instead of nil; dispatch loop in `scanRegion` / `scanAccount` detects sentinel via `errors.Is(err, errServiceDisabled)`, suppresses warning + error reporting, surfaces `(service disabled)` on per-service progress line. New service-disabled helpers follow this shape.

**Macie variant — code+message disambiguation.** Macie collides on `AccessDeniedException` for both real IAM denial AND not-enabled. `isMacieNotEnabled` in `macie_scanners.go` adds `strings.Contains(err.Error(), "Macie is not enabled")` check on top of `isAccessDenied`. Real IAM deny on `GetMacieSession` falls through to `skipIfAccessDenied`, preserving warning signal. Precedent: `isCacheSecurityGroupsNotPermitted` (code+message matcher per "Smithy API-error-code predicates" above). Audit Manager follows same shape via `isAuditManagerNotEnabled` (matches "complete AWS Audit Manager setup").

**Control Tower variant — multi-code disambiguation.** Some services surface not-enabled state under MULTIPLE error codes depending on missing prerequisite. Control Tower returns `AccessDeniedException` when calling account is not org management account AND `ValidationException` when `AWSControlTowerAdmin` role missing — both mean "not deployed here". `isControlTowerNotEnabled` first matches message hint (`AWSControlTowerAdmin` / `landing zone` / `management account`), then accepts EITHER `isAccessDenied(err)` OR `isAPIErrorCode(err, "ValidationException")`. Message check first short-circuits real validation errors (malformed input, etc.) sharing code but not not-enabled semantics. Same `markServiceDisabled` sentinel + `(service disabled)` progress line as single-code variants.

**Multi-phase orchestration.** Only phase-1 detection step (`DescribeSubscription` / `DescribeHub` / `GetMacieSession`) returns sentinel. Phases 2+ keep per-phase tolerant `isAccessDenied` skip — handles rare partial-IAM-grant case where subscription detectable but list APIs fail. Top-level scanner (`scanShield` / `scanSecurityHub` / `scanMacie`) propagates sentinel via existing `if ferr != nil { return 0, 0, ferr }` chain, halting downstream phases naturally.

**Gate-only phase-0 (no upsert).** Account/region config that gates a service but isn't an ARN'd resource uses `gateXxx(ctx, client, acct, st) error` — calls Describe purely for disabled-sentinel side-effect (`markServiceDisabled` on not-subscribed, nil otherwise). No `store.Resource` built. Distinct from "Multi-phase orchestration" above, which DOES upsert the phase-1 detection row. Precedent: `gateShieldSubscription` in `shield_scanners.go`.

**Member-account variant — DescribeOrganization probe.** Organizations List* ops (`ListRoots` / `ListAccounts` / `ListPolicies` / `ListOrganizationalUnitsForParent`) reject member-account calls with opaque `AccessDeniedException`. `DescribeOrganization` succeeds from any member and exposes `MasterAccountId`. Probe at top of `scanOrganizations`: if `sv(org.MasterAccountId) != acct.ID`, return `markServiceDisabled(errors.New("not the management account"))`. Member-account scans surface `(service disabled)` instead of N AccessDenied warnings. Precedent: `organizations_scanners.go`.

**Shared not-enabled predicate across services.** When two services gate on the same account-level enablement (e.g. Cost Explorer access blocks both `aws:ce` and `aws:bcmpricingcalculator`), declare one predicate (`isCostExplorerNotEnabled` in `bcmpricingcalculator_scanners.go`) and have both scanners' dispatcher call it before falling through to `skipIfAccessDenied`. Avoids drift between two near-identical message matchers.

## Multi-region DNS NXDOMAIN = global service

A transient warning of shape `dial tcp: lookup <svc>.<region>.amazonaws.com on …: no such host` firing from every non-`us-east-1` region (and only those) is the symptom of a global service registered as per-region. The endpoint does not exist outside `us-east-1`; `isTransientNetworkError` warn-skips it N-1 times per multi-region scan. Fix: early-return at the top of `scanX` — `if region != "us-east-1" { return 0, 0, nil }` — same shape as Route53 / Budgets / CloudFront. Precedent: `route53recoveryreadiness_scanners.go`.

## Multi-phase parent + children closure-wiring helper

Scanners modeling per-(acct,region) singleton parent with N child phases (Macie session + jobs/CDIs/allow-lists, Security Hub hub + insights/standards/product-subs) factor closure-wiring into one `upsertXChildren(st, parentARN, acct, batch, kind)` helper. Helper does `UpsertResources(batch)` + `RecordHierarchyBatch([][2]string{{child.ID, parentID}})` together. Don't inline per phase — three+ duplicated copies of same closure-pair build = sign to extract. Precedent: `upsertMacieChildren` (`macie_scanners.go:343`), `upsertSecurityHubChildren` (`securityhub_scanners.go`).

## Region-scoped FK-safe id sets

When target NativeID not deterministic per (acct, region) (e.g. multiple `aws:guardduty:detector` rows per region; `aws:config:recorder` arbitrary name), use `scannedIDsByRegion(acct, st, type) → map[region][]resourceID` instead of flat id set. Singleton-per-region services (`aws:macie:session` via `macieSessionNativeID`) keep flat `scannedIDSet`. Both helpers in `securityhub_resolvers.go`. Same FK-safety guarantees as flat-set pattern; emits one edge per scanned target in region rather than guessing NativeID.

## Multi-phase scanner totals

Each phase returns `(total, inserted, err)`. `total = len(batch)` (rows scanned), `inserted = n` from `UpsertResources` (rows newly inserted, excludes upserts of existing). Never return `len(batch)` for both — scan-progress line reports nonsense on rescans where every row updates.

## Cross-service ResourceArn ≠ scanner NativeID shape

Security-overlay services (Shield, GuardDuty findings, Inspector findings) emit `ResourceArn` refs in canonical AWS shape that may differ from per-service scanner's NativeID shape. Example: Shield emits EIP as `arn:aws:ec2:{r}:{a}:eip-allocation/eipalloc-xxx`, but `ec2_networking_scanners.go` stores `arn:aws:ec2:{r}:{a}:elastic-ip/eipalloc-xxx`. Normalise at classify time (`strings.Replace(arn, ":eip-allocation/", ":elastic-ip/", 1)`) before `store.ResourceID` lookup, or every edge silently FK-drops. See `classifyShieldProtectedResource` in `shield_resolvers.go`.

## CFN `PhysicalResourceId` shape varies per ResourceType

Adding entries to `cfnTypeMap` (`cloudformation_resolvers.go`): full ARN for some (Lambda, SNS, ELBv2, SFN, SecretsManager, Lambda layer), bare name/ID for others (S3, IAM, EC2 *, KMS key, DynamoDB, Logs, ECR, Kinesis, RDS, EFS, EventBridge), queue URL for SQS, ID-only for APIGW. Verify shape against CFN resource-ref docs per type — wrong synth = phantom NativeID, FK-safe lookup silently drops edge with no error. Custom-bus EventBridge rules cannot resolve from physID alone (no bus context); reject pipe-form `BUS|NAME` rather than synth wrong ARN.

## Adding new AWS SDK service module

`go get github.com/aws/aws-sdk-go-v2/service/<svc>@latest` then `go mod tidy`. Service modules version-independent of base SDK; no pin needed.

## ECR image identifier → repository ARN

Services referencing container images by image-URL (App Runner `ImageRepository.ImageIdentifier`, ECS task-def `ContainerDefinitions[].Image`, etc.) carry URL form `{acct}.dkr.ecr.{region}.amazonaws.com/{repo}[:tag]`. Strip tag suffix (`strings.LastIndexByte ':'`), parse host into `{acct}` + `{region}` via `.dkr.ecr.` and `.amazonaws.com` cuts, then reconstruct `arn:aws:ecr:{region}:{acct}:repository/{repo}` for canonical NativeID lookup. `public.ecr.aws/...` and other registries (`docker.io/...`, `quay.io/...`) skip — no edge. Helper precedent: `apprunnerImageToRepoARN` in `apprunner_resolvers.go`. Multi-segment repo names (`team/myapp`) preserved.

## RDS-shaped engines: shared API vs dedicated API

Neptune AND DocumentDB each have dedicated SDK service (`aws-sdk-go-v2/service/neptune` / `.../docdb`) with own scanners (`aws:neptune:*` / `aws:docdb:*` types). Neptune *also* surfaces via `rds:DescribeDBClusters` (`Engine=neptune`); DocumentDB does NOT (separate API endpoint). To prevent duplicate rows when scanning same physical Neptune cluster under both `aws:rds:cluster` AND `aws:neptune:cluster`, RDS scanner filters `Engine ∈ {neptune, docdb}` via `nonRDSEngines` in `rds_scanners.go`. Add engine to `nonRDSEngines` whenever adding dedicated scanner conflicting with shared RDS API. Verify by checking dedicated SDK's `api_op_CreateDBCluster.go` `Engine` valid-values list AND probing `rds:DescribeDBClusters` behaviour in test account. (Both Neptune and DocDB *ARN prefixes* use `arn:aws:rds:` — historical artefact predating API split.)

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

Cross-cutting pure-helper tests (ARN builders, error predicates, tag helpers, transient classifier) live in `aws_test.go`, `arn_test.go`, `errors_test.go`, `tags_test.go`. Per-service helper tests (e.g. `apprunnerImageToRepoARN`, `instanceArnFromPermissionSetArn`) live in the matching `<svc>_resolvers_test.go`. Before adding a new helper test, grep `^func Test<Helper>` across `aws/*_test.go` — duplicate `TestX` in same package fails to compile.

## Smithy GenericAPIError string shape

`(&smithy.GenericAPIError{Code:"AccessDenied",Message:"denied"}).Error()` = `"api error AccessDenied: denied"`. Tests asserting on `err.Error()` or `ScanWarning.Message` must include the `api error ` prefix.

## SDK middleware test stubs — placement

When stubbing SDK responses via `Stack.Initialize.Add(...)`, place with `smithymw.After` not `Before`. `RegisterServiceMetadata` is itself an Initialize middleware that populates op-name + service-id in ctx; a `Before` stub short-circuits ahead of it and `awsmw.GetOperationName(ctx)` returns `""`. Precedent: `middleware_testhelper_test.go` `stubResponses`.

## Wrapper-key json tags are lowercase by design

The "Scanner attribute JSON uses PascalCase keys" rule applies to fields produced by `json.Marshal` on raw SDK structs. Hand-built wrapper containers that namespace the SDK payload (`{"lb": <LB>, "type": ...}` in `elb_scanners.go`, `{"rule": ..., "Targets": [...]}` via `ruleWithTargets` in `eventbridge_scanners.go`, `{"listenerArn": ..., "cert": ...}` in `elb_scanners.go`) deliberately use lowercase / camelCase outer keys to distinguish them from SDK fields. Resolver struct tags like `json:"lb"` / `json:"listenerArn"` / `json:"deadLetterTargetArn"` are correct — do not "fix" to PascalCase, and any tag-shape lint must allowlist these.

## EventBridge resolver — `EventBusArn` is dead path

`eventbridge_resolvers.go` reads `attrs.Rule.EventBusArn` first then falls back to `EventBusName`. SDK `eventstypes.Rule` has no `EventBusArn` field and `eventbridge_scanners.go` never synthesizes one — real scans always take the EventBusName fallback. Tests using hand-rolled JSON with `EventBusArn` work in isolation but exercise no production code path. Use `EventBusName` in fixtures and mirror the synthesis the resolver does (`arn:aws:events:{r}:{a}:event-bus/{name}`).

## Wrapper-shape test fixtures — `wrapped_attrs_testhelper_test.go`

Scanner-side wrapper containers (`{"lb": <LB>, "type": ...}` in `elb_scanners.go`, `tgWithTargets`, `ruleWithTargets`, etc.) are declared as function-local types so tests cannot reuse them directly. Build resolver-test `AttributesJSON` via the helpers in `wrapped_attrs_testhelper_test.go` (`elbv2LBAttrs`, `elbv2TargetGroupAttrs`, `eventBridgeRuleAttrs`) — they take real SDK types so wrapping-shape drift surfaces in tests rather than as silent zero-value resolutions in production. New wrappers go here, named `<svc><Resource>Attrs`.

## VPC subtree wired via `attached-to`, not `contains`

`ec2_networking_resolvers.go` emits `child → vpc` `RelAttachedTo` for subnet, IGW, route-table, NAT gateway, VPC endpoint, network ACL, peering. No `RecordHierarchyBatch` call wires VPC as a closure parent — VPC has zero `contains` rows. Graphs and `disco list --hierarchy` see VPC only via the reverse `attached-to` edges. Add hierarchy wiring deliberately if a feature needs VPC→child closure traversal.

## Probe-first for low-TPS multi-phase services

Services with ≥20 sub-phase List ops AND a low per-account TPS quota (SageMaker is the canonical case) burn minutes when adaptive retry's token bucket throttles — every phase pays the penalty even on dormant accounts. Add a phase-0 probe of 2-3 cheap `MaxResults=1` List ops covering the highest-signal surfaces; short-circuit the full fan-out on empty. Precedent: `sagemakerInUseProbe` (sagemaker_scanners.go) probes ListDomains + ListNotebookInstances + ListEndpoints. Accept false negatives (pipelines-only / training-jobs-only accounts) for the wall-time win.

## Expected-state singletons → silent no-op, not warn

Singleton-config Get/List ops return distinct error codes when the config has not been opted into (`SigningConfigurationNotFoundException`, `NotConfiguredException`, `RegistryPolicyNotFoundException`, `ResourceNotFoundException`, `TagOptionNotMigratedException`, `UnauthorizedException` from org-only APIs called by non-mgmt accounts). These are the **default state**, not warnings. Return `(0, 0, nil)` directly — do NOT route through `skipIfAccessDenied`, which records a `ScanWarning` and clutters the per-region warnings block. Real IAM denies still warn via `isAccessDenied`.

## ThrottlingException is dispatch-level transient

`isTransientNetworkError` (aws.go) matches `ThrottlingException` / `Throttling` / `ThrottledException` / `RateExceededException`. Post-retry throttle exhaust (SDK retryer burned its 10-attempt adaptive budget) warn-skips at `scanRegion` dispatch automatically. Scanners do NOT need inline `isAPIErrorCode(err, "ThrottlingException")` handling — same shape as RequestTimeout/ServiceUnavailable.

## AppStream DescribeUsers: SAML auth-type rejected

The SDK enum `appstreamtypes.AuthenticationType` exposes USERPOOL, SAML, API. AWS rejects SAML on `DescribeUsers` (`'SAML' is not a supported authentication type for describing users`); SAML federation users are not first-class user-pool entries. Iterate USERPOOL + API only.

## Resolver-edge metadata: `EdgeDecl`

`registerResolver(fn, emits ...EdgeDecl)` is variadic — every new resolver MUST list each `(source, target, kind)` triple it upserts. Audit + coverage tooling reads the metadata; resolvers without `emits` are invisible to gap analysis. EdgeDecl shape: `{Source: TypeXxx, Target: TypeYyy, Kind: store.RelXxx}`. Annotate dynamic-dispatch resolvers (e.g. EventBridge target classifier) with one EdgeDecl per yielded type — enumerate the dispatch table. Cross-account stub targets (e.g. `aws:iam:foreign-account`) are valid Target values; the synthetic resource gets upserted before the edge so FK holds. `RecordHierarchyBatch` calls produce `parent → child contains` rows — declare as `EdgeDecl{Parent, Child, store.RelContains}`. Read-only / sidecar-populator resolvers stay `registerResolver(fn)` with no edges and surface in `disco coverage --resolvers --only-unannotated` as intentional no-ops.

Tooling:
- `disco coverage --resolvers --provider aws [--only-unannotated]` — per-resolver edge counts.
- `disco coverage --missing-resolvers --provider aws` — emitted disco types with no `EdgeDecl.Source` mention. The candidate gap inventory.
- `go run ./cmd/aws-resolver-audit/ --list-edges` — every declared (src, tgt, kind) triple.
- `go run ./cmd/aws-resolver-audit/ --db <path>` — diffs declared metadata + DB edges against ARN/ID refs walked from `AttributesJSON`.

Snapshot lives in the orphan-types fenced block of `docs/aws-missing-resolvers.md` — refresh with `disco coverage --missing-resolvers --provider aws` after each resolver-shipping commit and replace the block contents so future PRs diff against it.

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

## Re-verify leaf-flag comments before trusting them

`coverage_leaves.go` entries often carry an inline reason ("refs blocked by sanitize", "refs need Describe enrichment", "no SDK list op"). These rot: sanitize.go's shape-bounded ARN allowlist (`isAWSARN` in `internal/store/sanitize.go`) was added after `appflow:connector-profile` was flagged blocked, leaving the entry's claim stale until commit `abd36e2`. Before adding a sidecar workaround for what a comment says is "blocked", grep `internal/store/sanitize.go` for recent `isAWSARN` / `keyVaultDNSSuffixes` extensions and try the direct path first. Same applies to "no Describe op" claims — SDK additions land between scanner-write and leaf-comment time.

## Parent-row "leaf" ≠ no edges

Many parent types (mediaconnect:flow, kinesis:stream, eventbridge:rule) appear leaf-flagged because their existing resolvers emit *child→parent* `attached-to`, not parent→outbound. To demote, identify a NEW outbound edge from the parent's own SDK body to a *non-child* type — RelContains/closure to children doesn't count. Precedents: `mediaconnect:bridge → mediaconnect:gateway` via `PlacementArn` (commit 5ccaf80) demoted the parent; `mediaconnect:flow` stayed flagged because every Flow body field maps to an existing child type. Confirm via SDK doc grep on the body struct *before* scanner enrichment work — if every ref-bearing field is already spawned as a child row, the parent is genuinely leaf.

## Empty-message AccessDeniedException = closed-to-new-customers signal

When AWS retires a service to new customers (existing customers keep access), list ops on unregistered accounts return `AccessDeniedException` with an *empty message body* — distinct from real per-op IAM denials which always carry an action-identifying message. Detect via `errors.As(err, &smithy.APIError); strings.TrimSpace(ae.ErrorMessage()) == ""` on top of `isAccessDenied`. Closure state is **non-uniform across ops on the same account** (`ListCampaigns` may succeed while `ListDecoderManifests` fails). Wire two layers: phase-0 gate for the common case (one cheap probe → `markServiceDisabled` if empty-msg) AND per-phase silent return `(0,0,nil)` for the partial-uniformity case. Real IAM denials with messages still warn via `skipIfAccessDenied`. Precedent: `gateIoTFleetWise` + `isIoTFleetWiseClosedToAccount` (iotfleetwise_scanners.go, commits 40460b0 + 278b1ea).

## Two-pass scanner: keep total == inserted via skip-set dedup

Multi-pass scanners that pre-stub catalogue rows then re-upsert with rich detail (e.g. IAM AWS-managed policy catalogue + GAAD pass) inflate the per-service progress line on a fresh DB: `total = len(batch)` counts both upserts, but `inserted` only counts the first because the second is an ON CONFLICT update. Surfaces as `(1520 total, 1508 new)` → confuses users into thinking the scan was partial. Fix: reverse pass order so the *rich* pass runs first and captures the dedup ARN set, then the *stub* pass filters its batch via `if skipARNs[arn] { continue }`. Each row upserted exactly once; total == inserted on fresh DB. Precedent: `scanIAMAuthDetails` + `scanIAMAWSManagedCatalogue` (commit 14cbee2).