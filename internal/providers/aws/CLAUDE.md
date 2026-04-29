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

## Per-call concurrency constants

`concurrency.go` exports `fanoutHigh` (20), `fanoutMed` (10), `fanoutLow` (2) for `semaphore.NewWeighted(...)` inside scanner/resolver fan-out loops. Distinct from `maxConcurrentServices` (`aws.go`) which caps top-level service scanners. Do not redeclare `const maxConcurrent` inside individual scanners — pick fanout tier.

## Service-not-enabled → markServiceDisabled sentinel

Some AWS APIs return distinct exception code when account has not subscribed to / activated feature — Shield Advanced (`ResourceNotFoundException` via `isShieldNotSubscribed`), Security Hub (`InvalidAccessException` / `ResourceNotFoundException` via `isSecurityHubNotEnabled`). Phase-1 detection step returns `markServiceDisabled(err)` (`aws.go`) instead of nil; dispatch loop in `scanRegion` / `scanAccount` detects sentinel via `errors.Is(err, errServiceDisabled)`, suppresses warning + error reporting, surfaces `(service disabled)` on per-service progress line. New service-disabled helpers follow this shape.

**Macie variant — code+message disambiguation.** Macie collides on `AccessDeniedException` for both real IAM denial AND not-enabled. `isMacieNotEnabled` in `macie_scanners.go` adds `strings.Contains(err.Error(), "Macie is not enabled")` check on top of `isAccessDenied`. Real IAM deny on `GetMacieSession` falls through to `skipIfAccessDenied`, preserving warning signal. Precedent: `isCacheSecurityGroupsNotPermitted` (code+message matcher per "Smithy API-error-code predicates" above). Audit Manager follows same shape via `isAuditManagerNotEnabled` (matches "complete AWS Audit Manager setup").

**Control Tower variant — multi-code disambiguation.** Some services surface not-enabled state under MULTIPLE error codes depending on missing prerequisite. Control Tower returns `AccessDeniedException` when calling account is not org management account AND `ValidationException` when `AWSControlTowerAdmin` role missing — both mean "not deployed here". `isControlTowerNotEnabled` first matches message hint (`AWSControlTowerAdmin` / `landing zone` / `management account`), then accepts EITHER `isAccessDenied(err)` OR `isAPIErrorCode(err, "ValidationException")`. Message check first short-circuits real validation errors (malformed input, etc.) sharing code but not not-enabled semantics. Same `markServiceDisabled` sentinel + `(service disabled)` progress line as single-code variants.

**Multi-phase orchestration.** Only phase-1 detection step (`DescribeSubscription` / `DescribeHub` / `GetMacieSession`) returns sentinel. Phases 2+ keep per-phase tolerant `isAccessDenied` skip — handles rare partial-IAM-grant case where subscription detectable but list APIs fail. Top-level scanner (`scanShield` / `scanSecurityHub` / `scanMacie`) propagates sentinel via existing `if ferr != nil { return 0, 0, ferr }` chain, halting downstream phases naturally.

**Gate-only phase-0 (no upsert).** Account/region config that gates a service but isn't an ARN'd resource uses `gateXxx(ctx, client, acct, st) error` — calls Describe purely for disabled-sentinel side-effect (`markServiceDisabled` on not-subscribed, nil otherwise). No `store.Resource` built. Distinct from "Multi-phase orchestration" above, which DOES upsert the phase-1 detection row. Precedent: `gateShieldSubscription` in `shield_scanners.go`.

## Multi-phase parent + children closure-wiring helper

Scanners modeling per-(acct,region) singleton parent with N child phases (Macie session + jobs/CDIs/allow-lists, Security Hub hub + insights/standards/product-subs) factor closure-wiring into one `upsertXChildren(st, parentARN, acct, batch, kind)` helper. Helper does `UpsertResources(batch)` + `BatchAddToHierarchyClosure([(child.ID, parentID)])` together. Don't inline per phase — three+ duplicated copies of same closure-pair build = sign to extract. Precedent: `upsertMacieChildren` (`macie_scanners.go:343`), `upsertSecurityHubChildren` (`securityhub_scanners.go`).

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