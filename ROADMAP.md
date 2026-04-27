# Disco Feature Roadmap

## Context

`disco` = CGO-free Go CLI scan AWS/Azure/GCP into local SQLite with resource graph (relationships + closure table). Foundation solid: parallel scan, stable resource IDs, secret scrub at store boundary, rule engine, graph query, diff.

**Primary audience:** security/compliance teams — posture review, drift detect, relationship audit.

**Strategic focus (this cut):**
1. **Coverage** — top services across all three clouds
2. **Resolvers** — same-service completeness + cross-service edges (graph useless without edges)
3. **Graph command** — power-user query surface; "blast radius", "path", "why"

Tiers: **Now (1–2 sprints)** → **Next (quarter)** → **Later (6–12mo / v1.0)**.

---

## COMPLETED

### Foundations
- **N1** Partial scan status (`PartialScan` at `internal/store/scans.go:75`, wired in `cmd/scan.go:183`)
- **N2** Progress reporting (`OnServiceComplete`/`OnResolve*` → stderr, `--quiet` flag)
- **N3** `disco diff <scanA> <scanB>` (`cmd/diff.go`)
- **N4** CSV + JSONL export (`cmd/list.go`)
- **N5** `cmd/` test coverage (list/diff/graph/check tests)
- **X1** Graph query (`cmd/graph.go` — `--depth`, `--kinds`, `--direction`, `--output table|json|dot`)
- **X2** Rule engine (`internal/rules/*` + `cmd/check.go`)
- **Secret scrub** at store boundary (`internal/store/sanitize.go`)

### AWS resolver expansion (tiers 1–3)
- **Tier 1** — edges from existing scanned attributes:
  - RDS instance/cluster → KMS; instance → OptionGroup
  - DynamoDB table → KMS (customer-managed SSE)
  - Lambda function → KMS / Subnet / SG
  - Lambda ESM → SQS source (via `EventSourceArn` parse)
  - EC2 instance → IAM instance profile, Network Interface
  - ECR repository → KMS (new resolver)
  - Secret → rotation Lambda
  - ECS service → Subnet / SG; task-def → task role + execution role
  - Fix: ECS service JSON tags lowercase, scanner emit PascalCase — silent edge loss
- **Tier 2** — scanner enrichment:
  - SNS: `GetTopicAttributes`, Topic → KMS + SQS DLQ
  - SQS: `GetQueueAttributes`, NativeID switched URL → ARN, Queue → KMS + DLQ
  - APIGW: Method → Lambda integration, Method → VpcLink
- **Tier 3** — new service scanners:
  - ACM: cert scanner + resolver (cert → PrivateCA); from-side edges from CloudFront, ELBv2 listener, APIGW DomainName (v1 + v2)
  - Kinesis Data Streams: scanner + KMS resolver; Lambda ESM → Kinesis stream now resolves
  - Firehose: delivery stream scanner; → Kinesis source, → S3 destinations, → KMS

### R1 resolver gaps (partial, this session)
- **DynamoDB** table → stream (`LatestStreamArn`, new type `aws:dynamodb:stream`)
- **ECS** task-def → ECR repo (container image URI parse); task-def → log-group (awslogs driver)
- **Lambda** function → EFS access point (`FileSystemConfigs[].Arn`)
- **CloudFront** distribution → S3 origin bucket (`Origins.Items[].DomainName` parse); → Lambda@Edge (`LambdaFunctionAssociations` in behaviors)

### R2 new AWS scanners (this session)
- **EFS** — `aws:efs:file-system`, `aws:efs:mount-target`, `aws:efs:access-point`. Resolvers: FS→KMS, mount-target→FS/subnet, access-point→FS.
- **WAFv2** — `aws:wafv2:web-acl`, `aws:wafv2:rule-group`, `aws:wafv2:ip-set`. Resolvers: ACL→RuleGroup/IPSet. REGIONAL+CLOUDFRONT scopes.
- **EventBridge** — `aws:events:event-bus`, `aws:events:rule`. Resolvers: rule→bus, rule→Lambda/SNS/SQS/Kinesis targets.
- **CloudTrail** — `aws:cloudtrail:trail`. Resolvers: trail→S3/KMS/log-group.

### R1 resolver gaps + R2 scanners (prior session)
- **EventBridge** extended: rule → StepFunctions state-machine, rule → Firehose delivery stream.
- **APIGW** stage → WAFv2 web-ACL (via `WebAclArn`).
- **RDS** instance → parameter-group; cluster → cluster-parameter-group.
- **ELBv2** TargetGroup → Lambda (lambda targets); TargetGroup → EC2 instance (instance targets). Scanner now fetch `DescribeTargetHealth` + embed `Targets` in TG attrs.
- **SecretsManager** replica → primary secret (via `PrimaryRegion`).
- **KMS** key → alias inverse (`contains` edge; alias → key already exist as `attached-to`).
- **StepFunctions** — `aws:sfn:state-machine`, `aws:sfn:activity`. Resolvers: SM→IAM role, SM→log-group, SM→downstream targets (Lambda/SNS/SQS/DynamoDB/Kinesis/Firehose/SFN) by walking Definition + Parameters for ARNs.
- **Cognito** — `aws:cognito:user-pool`, `aws:cognito:app-client`, `aws:cognito:identity-pool`. Resolvers: app-client→user-pool, identity-pool→IAM roles, identity-pool→user-pool/app-client.

### R1.5 IAM policy doc → KMS / S3 / Secrets / DynamoDB (this session)
- **Managed policy enrichment** (`scanIAMPolicies`): default `PolicyVersion` now fetched per policy (errgroup + `semaphore.NewWeighted(fanoutMed)`, per-policy AccessDenied tolerated). `AttributesJSON` wrapped as `{"Policy": <ListPolicies entry>, "PolicyVersion": <PolicyVersion>}`. URL-encoded `Document` field preserved verbatim. Existing reader (`resolveManagedPolicyAttachments`) consumes only NativeID, so wrap is silent-drop-free.
- **Walker resolver** `resolveIAMPolicyResources` (`iam_resolvers.go`): single resolver covers all four policy types (`TypeIAMPolicy`, `TypeIAMRolePolicy`, `TypeIAMUserPolicy`, `TypeIAMGroupPolicy`). Decode `Document` via `url.QueryUnescape`; parse `Statement` (object-or-array via new `statementList` unmarshal type, modelled on `principalList`); per Allow statement walk `Resource` (string-or-array via `resourceList`); classify each ARN by service segment and emit `RelUses` to scanned target. Classification rules: `:kms:` via existing `loadKMSResolveIndex` + `resolveKMSKeyID` (handles ARN/alias-ARN/`alias/foo`/bare-UUID, skips `alias/aws/*`); `arn:aws:s3:::bucket[/...]` strip object suffix to bucket ARN; `arn:aws:secretsmanager:...:secret:NAME[:VERSIONSTAGE:VERSIONID]` keep first 7 colon segments (precedent: ECS `Secrets[].ValueFrom` parser); `arn:aws:dynamodb:...:table/NAME[/index|stream|...]` strip child suffix. Per-target wildcard (`*?` in canonical resource segment) and `Resource: "*"` skipped. FK-safe via per-type id sets (`TypeS3Bucket`, `TypeSecretsManagerSecret`, `TypeDynamoDBTable`) + KMS index built once per resolver run (`resourceIDSet` helper added). Cross-account / unscanned targets skip silently.
- Out of scope for v1: permission boundaries (different attrs path), AWS-managed policies (still excluded by `PolicyScopeTypeLocal` perf decision), `Action`-aware filtering (rule-engine concern), wildcard glob expansion against scanned ids.
- **Tier 1 expansion (this session)**: `classifyPolicyResource` now also handles Lambda function (`:function:NAME` + `:VERSION/:ALIAS` trim, 7-segment), CloudWatch Logs log group (strip `:*` and `:log-stream:` tail past name), SNS topic (5-colon ARN, subscription ARNs rejected), SQS queue (5-colon ARN). FK-safe via four added id sets (`TypeLambdaFunction`, `TypeLogsLogGroup`, `TypeSNSTopic`, `TypeSQSQueue`).
- **Tier 2 expansion (this session)**: SSM parameter (full ARN only — bare names skipped, no region context in policy doc), Kinesis stream (`:stream/NAME`, consumer ARNs rejected via `/consumer/...` tail), ECR repository (`:repository/NAME`), IAM role for PassRole/AssumeRole references (regular `:role/NAME` and service-linked `/aws-service-role/...` discriminated by path).
- **Tier 3 expansion (this session)**: RDS instance (`:db:NAME`) and cluster (`:cluster:NAME`) — colon-separated, snapshot/parameter-group/subnet-group share prefix and reject; SFN state-machine (`:stateMachine:NAME`, `:::` integration ARNs rejected per aws/CLAUDE.md); EventBridge event-bus (`:event-bus/NAME`) and rule (`:rule/[BUS/]NAME`); EFS file-system (`:file-system/fs-xxx`, mount-target/access-point intentionally skipped). `classifyPolicyResource` signature refactored: 14 separate map args replaced by one `*policyResourceSets` struct built once via `loadPolicyResourceSets`.

### R2.9 IAM Identity Center (SSO) + Identity Store (this session)
- **IAM Identity Center** new types `aws:sso:instance`, `aws:sso:permission-set`, `aws:sso:account-assignment`. **Identity Store** new types `aws:identitystore:user`, `aws:identitystore:group`. Single regional service `aws:sso-admin` runs four phases: (1) `ListInstances` (paginator), (2) per-instance `ListPermissionSets` + concurrent `DescribePermissionSet` fan-out (errgroup + `semaphore.NewWeighted(fanoutMed)`), (3) per (instance, permission-set, account) triple — built sequentially via `ListAccountsForProvisionedPermissionSet` per perm-set then concurrent `ListAccountAssignments` fan-out (`fanoutHigh`), (4) per `IdentityStoreId` `ListUsers` + `ListGroups` against the identity-store SDK client. Phase-level `AccessDenied` tolerated via `skipIfAccessDenied` — non-management accounts cannot read SSO admin APIs and bail at phase 1. ListInstances is region-scoped (returns instances active only in the calling region), so non-home regions short-circuit immediately. Two new SDK deps added: `github.com/aws/aws-sdk-go-v2/service/ssoadmin`, `github.com/aws/aws-sdk-go-v2/service/identitystore`.
- **Synthetic NativeIDs**. Account-assignments lack any AWS-issued ARN; synthesized as `{permissionSetArn}/account/{accountId}/{principalType}/{principalId}` so re-scans dedupe and the permission-set ARN (which already encodes the instance id) carries forward. Identity Store users/groups likewise lack ARNs; synthesized as `arn:aws:identitystore::{ownerAcct}:user/{IdentityStoreId}/{UserId}` and `…:group/…/{GroupId}`. Documented in aws/CLAUDE.md "Synthetic NativeIDs" section (precedent: KMS grant, EFS mount-target, Backup selection).
- **Resolvers**. `resolveSSOPermissionSetInstance` rebuilds the canonical instance ARN from the permission-set ARN's `:permissionSet/{ssoins-id}/{ps-id}` shape via `strings.Cut`, emits `contains` edge from instance to each permission-set. `resolveSSOAccountAssignments` walks each scanned assignment and emits up to three edges: assignment → permission-set (`uses`), assignment → identity-store user/group (`uses`, branched on `PrincipalType`), assignment → Organizations account (`attached-to`, via existing `loadOrgTargetIndex` mapping 12-digit account-id to canonical Organizations account ARN per aws/CLAUDE.md "Organizations NativeID = full ARN" rule). All four target lookups FK-safe via per-type id sets + the org-index map; partial-coverage scans (no Identity Store creds, no Org tree scanned) silently skip those branches without erroring. Instance metadata (`IdentityStoreId`, `OwnerAccountId`) preloaded into a single `ssoInstanceIndex` so the resolver does not re-decode attrs per assignment row.
- Out of scope: permission-set → managed-policy / customer-managed-policy / inline-policy edges (separate `ListManagedPoliciesInPermissionSet` + `ListCustomerManagedPolicyReferencesInPermissionSet` + `GetInlinePolicyForPermissionSet` fan-outs); SSO applications (`ListApplications`, `ListApplicationAssignments`); group memberships (`ListGroupMemberships` per group adds another fan-out tier); permission boundary on permission-set; assignment → AWS account fallback when Org tree NOT scanned (would require an `aws:account` synthesized type, deferred).

### R2.8 CloudFormation stacks + stack-sets (this session)
- **CloudFormation** new types `aws:cloudformation:stack`, `aws:cloudformation:stack-set`. Regional scanner `scanCloudFormation` runs two phases. Phase 1: `ListStacks` (paginator, filtered to active statuses — DELETE_COMPLETE excluded since deleted stacks return empty resource lists for 90 days), then per-stack `ListStackResources` paginator fan-out (errgroup + `semaphore.NewWeighted(fanoutMed)`). `AttributesJSON` wrapped as `{"Stack": <StackSummary>, "Resources": <[]StackResourceSummary>}` (embedding child data). Stack NativeID = full `StackId` ARN. Per-stack `ValidationError` (stack vanished between list+describe) and `AccessDenied` tolerated; persists stack with empty Resources rather than dropping. Phase 2: `ListStackSets` + per-set `DescribeStackSet` + `ListStackInstances` fan-out. Stack-set NativeID = `StackSetARN`. Non-admin accounts return either `AccessDenied` or `ValidationError` ("StackSets is not active in this account") — both caught via `isStackValidationError` helper alongside `isAccessDenied`, skipped without barring phase 1 totals. Resolvers: `resolveCloudFormationStackResources` walks `Resources[]` per stack and emits `contains` edges to managed AWS resources via a table-driven `cfnTypeMap` (CFN ResourceType → disco type + NativeID synthesis func). 27 entries cover S3, IAM (role/user/managed-policy), Lambda function + layer, EC2 (instance/SG/VPC/subnet), DynamoDB, SNS, SQS (queue URL → ARN by trailing path segment), Logs log-group, KMS, Secrets Manager, RDS instance + cluster, SFN, EventBridge rule (default-bus only — pipe-form `BUS|NAME` rejected since CFN strips bus context) + event-bus, EFS file-system, ECR, Kinesis, SSM (leading slash trimmed), ELBv2 LB + target-group, APIGW v1 REST API + v2 API, and `AWS::CloudFormation::Stack` (nested-stack pass-through for stack→stack edges). Skip-list: empty `PhysicalResourceId`, `ResourceStatus` ∈ {CREATE_FAILED, DELETE_*}, unmapped types (e.g. RDS::DBSubnetGroup, custom resources). FK-safe via single combined id-set query (one `ListResources` over `cfnTypeMap`'s union of types). `resolveCloudFormationStackSetInstances` emits stack-set → deployed stack `contains` edges via `Instances[].StackId`, FK-safe across accounts (stack lookup unfiltered by acct.ID since instances commonly live in member accounts). Out of scope this pass: drift detection, template-level (`!Ref` / `!GetAtt`) edges, output-export resolution, `AWS::CloudFormation::CustomResource` (arbitrary user-string physIDs), stack-instance as own type.

### R2.3 Security Hub (this session)
- **Security Hub** new types `aws:securityhub:hub`, `aws:securityhub:insight`, `aws:securityhub:standards-subscription`, `aws:securityhub:product-subscription`. Regional scanner `scanSecurityHub` runs four phases sequentially. Phase 1: `DescribeHub` (singleton; NativeID = `out.HubArn`, falls back to synthesized `arn:aws:securityhub:{region}:{acct}:hub/default` only when SDK omits it). Returns `present bool`; remaining phases short-circuit when not enabled — Security Hub returns `InvalidAccessException` from every API uniformly when the hub is disabled in a region, mirroring `scanMacie`'s phase-1-flag pattern. New helper `isSecurityHubNotEnabled` (sibling to `isShieldNotSubscribed`) treats `InvalidAccessException` + `ResourceNotFoundException` as soft skips alongside the standard `isAccessDenied` check. Phases 2–4: `NewGetInsightsPaginator`, `NewGetEnabledStandardsPaginator`, `NewListEnabledProductsForImportPaginator` — all paginator-native, no fan-out needed. NativeIDs are AWS-issued ARNs verbatim (`InsightArn`, `StandardsSubscriptionArn`, raw ProductSubscriptionArn); no synthetic identifiers in this scanner. Each child upserts via `upsertSecurityHubChildren` (modeled on `upsertMacieChildren`) which wires `contains` closure to the per-region hub. `AttributesJSON` stores SDK output verbatim; product-subscription rows wrap the bare ARN as `{"ProductSubscriptionArn": ...}` since `ListEnabledProductsForImport` returns only `[]string`.
- **Resolver** `resolveSecurityHubProductSubscriptions` parses each subscription's trailing `:product-subscription/{vendor}/{product}` segment via `parseSecurityHubProductSubscriptionARN` (`strings.Cut`-based, no `strings.Index`) and emits `uses` edges to scanned upstream finding sources in the same `(account, region)`. AWS-vendor products mapped: `aws/guardduty` → every `aws:guardduty:detector` in region (rare multi-detector case handled), `aws/config` → every `aws:config:recorder` in region, `aws/macie` → singleton `aws:macie:session` (deterministic NativeID via `macieSessionNativeID`). `aws/securityhub` (Foundational Security Best Practices self-feed) skipped explicitly — no edge. Third-party vendors (CrowdStrike, Tenable, etc.) and AWS products without scanners (Inspector v2, IAM Access Analyzer, Firewall Manager, Detective) skip silently. FK-safe via per-type id-sets (`scannedIDsByRegion` for variable-NativeID services, `scannedIDSet` for singleton-NativeID services) built once per resolver run. Out of scope: insight resource-ref edges (insights are saved finding-filter expressions, not resource refs); standards-subscription → standard catalog (standards are AWS-managed identifiers, not scanned resources); finding-aggregator (cross-region aggregation config); action-targets (EventBridge custom-action wiring); members (cross-account beyond current provider scope); findings themselves (event data, mirrors GuardDuty findings deferral).

### R2.4 Macie (this session)
- **Macie** new types `aws:macie:session`, `aws:macie:classification-job`, `aws:macie:custom-data-identifier`, `aws:macie:allow-list`. Regional scanner `scanMacie` runs four phases sequentially. Phase 1: `GetMacieSession` (singleton). NativeID synthesized as `arn:aws:macie2:{region}:{acct}:session` since the API exposes no session ARN; documented in `aws/CLAUDE.md` "Synthetic NativeIDs". `AccessDeniedException` (Macie not enabled in this region) tolerated via `skipIfAccessDenied` and short-circuits remaining phases — every downstream phase would fail identically. Phase 2: `ListClassificationJobsPaginator` → concurrent `DescribeClassificationJob` fan-out (errgroup + `semaphore.NewWeighted(fanoutMed)`); per-item `AccessDenied` tolerated. NativeID = `JobArn`. Phase 3: `ListCustomDataIdentifiersPaginator` → concurrent `GetCustomDataIdentifier` fan-out. NativeID = `Arn`. Phase 4: `ListAllowListsPaginator` → concurrent `GetAllowList` fan-out. NativeID = `Arn`. `AttributesJSON` stores SDK Get/Describe output verbatim. Each child upsert links to parent session via `BatchAddToHierarchyClosure` (`upsertMacieChildren` helper).
- **Resolvers**. `resolveMacieClassificationJobBuckets` walks each job's `S3JobDefinition.BucketDefinitions[].Buckets[]` and emits `uses` edge to scanned S3 bucket. FK-safe via `scannedBucketIDSet` helper; cross-account bucket refs skip silently. `BucketCriteria` (tag-condition expressions) deferred — same precedent as Backup selection tag-expansion. `resolveMacieAllowListBucket` reads `Criteria.S3WordsList.BucketName` and emits `uses` edge to the bucket hosting the words file. Regex-only allow lists (no `S3WordsList`) skip without error.
- Out of scope: findings (`ListFindings`/`GetFindings`) — event data, not resource (mirrors GuardDuty findings deferral); `BucketCriteria` tag-condition expansion against scanned-bucket tag set; member accounts (`ListMembers`/`GetMember`) — cross-account beyond current provider scope; custom data identifier → KMS — no KMS field on CDI (regex+keywords only); managed data identifiers (`ListManagedDataIdentifiers`) — AWS-managed catalogue, not a customer resource.

### R2.12 Shield Advanced (this session)
- **Shield Advanced** new types `aws:shield:protection`, `aws:shield:protection-group`, `aws:shield:subscription`. Global service registered with `global: true` (CloudFront precedent) — single client pinned to `us-east-1`. Scanner `scanShield` runs three phases sequentially: (1) `DescribeSubscription` (singleton; `Subscription.SubscriptionArn` used as NativeID when populated, else synthesized as `arn:aws:shield::{accountID}:subscription`), (2) `ListProtections` (paginator; full `Protection` body returned per entry — no separate Describe needed), (3) `ListProtectionGroups` (paginator; full `ProtectionGroup` body per entry). Accounts without Shield Advanced surface `ResourceNotFoundException` from every API; tolerated via new `isShieldNotSubscribed` helper alongside the standard `isAccessDenied` skip — both phases bail without barring sibling totals (multi-phase scanner pattern). `AttributesJSON` stores native SDK output verbatim (DescribeSubscriptionOutput / Protection / ProtectionGroup).
- **Resolvers**. `resolveShieldProtectionTargets` classifies each protection's `ResourceArn` by ARN segment via `classifyShieldProtectedResource` and emits an `attached-to` edge to one of: ELBv2 load balancer (`:elasticloadbalancingv2:.*:loadbalancer/`), Classic ELB (`:elasticloadbalancing:.*:loadbalancer/`), CloudFront distribution (`:cloudfront::.*:distribution/`), Route 53 hosted zone (`:route53:::hostedzone/`), or EC2 EIP. EIP shape normalised: Shield emits `:eip-allocation/eipalloc-...` while disco's EC2 scanner stores NativeID as `:elastic-ip/eipalloc-...` (per `ec2_networking_scanners.go:218`) — resolver rewrites `:eip-allocation/` → `:elastic-ip/` before lookup so the FK-safe id-set hit succeeds. Other shapes (Global Accelerator, AppSync) skip silently — no phantom edges. FK-safe via single combined id-set query over all five target types. `resolveShieldProtectionGroupMembers` walks each protection-group with `Pattern == "ARBITRARY"` and emits `contains` edges to every `Members[]` entry that resolves to a scanned protection; `Pattern=ALL` and `Pattern=BY_RESOURCE_TYPE` (implicit memberships) skipped — would require expanding against the scanned-protection set.
- Out of scope: protection → Route 53 health-check (`HealthCheckIds[]`) — no health-check type yet (`TypeRoute53HealthCheck` exists but not currently scanned for Shield linkage); Global Accelerator target — protections on accelerators dangle until `aws:globalaccelerator:accelerator` lands; DDoS attack history (`ListAttacks` / `DescribeAttack`) — event data, not a resource; application-layer automatic mitigation rules — already covered indirectly by WAFv2 web-ACL → CloudFront/ELBv2 edges; Pattern=ALL / BY_RESOURCE_TYPE membership expansion; SRT (Shield Response Team) DRT IAM role + log-bucket associations.

### R2.11 Network Firewall (this session)
- **Network Firewall** new types `aws:network-firewall:firewall`, `aws:network-firewall:firewall-policy`, `aws:network-firewall:rule-group`. Regional scanner `scanNetworkFirewall` runs three phases, each List (paginator) → concurrent Describe (errgroup + `semaphore.NewWeighted(20)`); phase-level `AccessDenied` tolerated via `skipIfAccessDenied` without barring later phases. List APIs: `ListFirewalls`, `ListFirewallPolicies`, `ListRuleGroups` — all have generated paginators. Describe output (`*Output` verbatim) stored as `AttributesJSON` so both `Firewall`/`FirewallStatus` + `FirewallPolicyResponse`/`FirewallPolicy` + `RuleGroupResponse`/`RuleGroup` nesting survives. `ListRuleGroups` called with default scope (customer-owned; `MANAGED` deliberately excluded). Two resolvers: `resolveNetworkFirewallFirewallRelationships` emits firewall → policy (`uses`), → VPC (`attached-to`), → each subnet in `SubnetMappings[]` (`attached-to`); `resolveNetworkFirewallPolicyRelationships` emits policy → rule-group (`uses`) across both `StatelessRuleGroupReferences[]` and `StatefulRuleGroupReferences[]`. All edges FK-safe via pre-built id sets over `{TypeNetworkFirewallFirewallPolicy, TypeEC2VPC, TypeEC2Subnet}` and `{TypeNetworkFirewallRuleGroup}` respectively. Out of scope: TLS inspection configuration, logging destinations, customer-managed KMS encryption, tags.

### R2.10 IAM SAML + OIDC providers (audit-moved — landed in prior session)
- `TypeIAMSAMLProvider` + `TypeIAMOIDCProvider` scanners live in `iam_scanners.go` phase 1, using `ListSAMLProviders` / `ListOpenIDConnectProviders` + concurrent Describe (semaphore 20). Federated-trust resolver `resolveIAMRoleFederatedTrust` already emits `assumes` edges from roles to these provider ARNs. (This item had been re-listed under NOW in ROADMAP drift; moved to COMPLETED this pass per CLAUDE.md audit rule.)

### R2.7 CloudTrail event-data-store (prior session)
- **CloudTrail** new type `aws:cloudtrail:event-data-store` (CloudTrail Lake). `scanCloudTrail` converted to multi-phase: trail phase tolerates per-region AccessDenied without barring Lake phase; Lake phase uses `NewListEventDataStoresPaginator` + errgroup fan-out over `GetEventDataStore` (per-item AccessDenied tolerated), Get body stored verbatim as `AttributesJSON`. New resolver `resolveCloudTrailEventDataStoreRelationships` emits EDS → KMS (`uses`, normalized via `kmsKeyTargetARN`, `alias/aws/*` skipped, FK-safe via scanned KMS id set) and EDS → IAM role (`assumes`, via `FederationRoleArn`, FK-safe via scanned role id set). AdvancedEventSelector resource-ARN parsing deferred.

### R2.6 EventBridge api-destination + connection (this session)
- **EventBridge** new types `aws:events:api-destination`, `aws:events:connection`. Scanner `scanEventBridge` gains two phases (manual `NextToken` — no paginator for either `ListConnections` or `ListApiDestinations`). Per-region `AccessDenied` tolerated via `skipIfAccessDenied`. Connections carry auth metadata only (secrets never returned from List). Resolver additions: `eventBridgeTargetType` now classifies `:events:...:api-destination/` ARNs, so existing rule→target loop emits `routes-to` edges. New resolver `resolveEventBridgeAPIDestinationConnection` emits `uses` edge from each api-destination to its `ConnectionArn`, FK-safe via scanned-connection id set (cross-account conn refs skip).

### R1 resolver gaps (Lambda layers + SQS KMS — prior sessions)
- **Lambda** function → layer-version via `Layers[].Arn` on function attrs. New type `aws:lambda:layer-version`; scanner `scanLambdaLayerVersions` uses `ListLayers` + per-layer `ListLayerVersions` paginator (per-layer `AccessDenied` tolerated). Resolver `resolveLambdaLayerRelationships` emits `uses` edge.
- **SQS** queue → KMS via `KmsMasterKeyId` on queue attrs. `alias/aws/*` skipped (AWS-managed default key unscanned). `kmsKeyTargetARN` normalizes alias/key-id/ARN input shapes.

### R2 MSK scanner + Lambda ESM→MSK (this session)
- **MSK** new type `aws:kafka:cluster` covering both Provisioned and Serverless flavors in one row (variant distinguished by which sub-struct is populated in `AttributesJSON`). Scanner `scanKafka` uses `ListClustersV2` (paginator) which returns the full `types.Cluster` per entry — no separate Describe. Per-region `AccessDenied` tolerated via `skipIfAccessDenied`. Resolver `resolveKafkaRelationships` emits: cluster → subnet (`attached-to`), cluster → security-group (`uses`), cluster → KMS key (`uses`). Provisioned edges read from `Provisioned.BrokerNodeGroupInfo.{ClientSubnets,SecurityGroups}` + `Provisioned.EncryptionInfo.EncryptionAtRest.DataVolumeKMSKeyId`; Serverless reads `Serverless.VpcConfigs[].{SubnetIds,SecurityGroupIds}` (Serverless has no CMEK). All edges FK-safe via a single merged id set across `TypeEC2Subnet`, `TypeEC2SecurityGroup`, `TypeKMSKey`. `alias/aws/` KMS references skipped (AWS-managed default key unscanned). IAM auth, ClientAuthentication details, and `LoggingInfo.BrokerLogs.*` destinations deliberately not modeled this pass.
- **Lambda ESM → MSK cluster** — `lambdaESMSourceType` now maps `kafka:` EventSourceArns to `TypeMSKCluster`. Self-managed Kafka sources (bootstrap-server lists, not ARNs) still fall through to the skip path.

### R1 resolver gaps (KMS grants → principals — prior session)
- **KMS grants** new type `aws:kms:grant`. `scanKMS` extended with per-key `ListGrants` fan-out under the existing errgroup; grants stored verbatim (`GrantListEntry` as `AttributesJSON`). Synthetic NativeID `{keyARN}/grant/{grantId}` since AWS does not return a grant ARN from ListGrants. `BatchAddToHierarchyClosure` wires grant→key under the `contains` closure. Per-key `AccessDenied` tolerated. Resolver `resolveKMSGrants` emits `uses` edges from each grant to its `GranteePrincipal` and `RetiringPrincipal` — filter to `arn:aws:iam::` ARNs only, classify by `:role/` / `:user/` substring, FK-safe via scanned IAM role+user id set. Service principals (`ec2.amazonaws.com`) and ephemeral assumed-role/federated session ARNs skipped — no target resource exists.

### R1 resolver gaps (S3 Storage Lens → buckets — prior session)
- **S3 Storage Lens** configuration → bucket edges. Scanner `scanStorageLens` enriched: after `ListStorageLensConfigurations`, fans out `GetStorageLensConfiguration` per entry (errgroup + weighted semaphore, per-item AccessDenied tolerated) so `AttributesJSON` now carries the full `StorageLensConfiguration` (Include/Exclude/DataExport). Resolver `resolveStorageLensRelationships` emits `uses` edges from Storage Lens → each `Include.Buckets[]` bucket and → `DataExport.S3BucketDestination.Arn` export bucket. FK-safe via scanned-bucket id set lookup (cross-account export targets skip silently). `Exclude.Buckets[]` deliberately not emitted (represents absence of coverage, not a relationship).

### R1 + R2 (EC2 AMI scanner + instance→AMI — prior session)
- **EC2** new type `aws:ec2:image`. Scanner `scanImages` in `ec2_compute_mgmt_scanners.go` uses `DescribeImages` with `Owners=["self"]` via `NewDescribeImagesPaginator` — self-owned AMIs only (public/Marketplace/shared AMIs are unbounded and not "ours" to audit). Resolver adds `ImageId` to `instanceAttrs`, builds AMI id set from scanned images, emits `uses` edge from instance to AMI. Dangling references (instances using public AMIs) silently skip — FK-safe. AMI→EBS snapshot edges deferred (no `aws:ec2:snapshot` type).

### R1 resolver gaps (S3 bucket → KMS — prior session)
- **S3** bucket → KMS via `GetBucketEncryption`. New scanner pass `scanS3BucketEncryptions` fans out per bucket (errgroup + weighted semaphore), resolves home region via `s3BucketRegion`, tolerates `ServerSideEncryptionConfigurationNotFoundError` + `AccessDenied`. Collected config stashed on `account.s3BucketEncryption` (ephemeral per scan; no synthetic resource type, no SDK attrs modification). Resolver `resolveS3BucketEncryptionRelationships` walks `ServerSideEncryptionConfiguration.Rules[]`, filters `SSEAlgorithm ∈ {aws:kms, aws:kms:dsse}`, normalizes `KMSMasterKeyID` via `kmsKeyTargetARN`, emits bucket→KMS `uses` edge. Skips `alias/aws/s3` (AWS-managed, unscanned).

### R1 resolver gaps (APIGW v2 JWT authorizer → Cognito — prior session)
- **APIGW v2** JWT authorizer → Cognito user-pool. New scanner pass `scanAPIGatewayV2Authorizers` fans out per HTTP/WebSocket API via `errgroup` (added to `scanAPIGatewayHTTPAPIs`). New type `aws:apigatewayv2:authorizer`. Resolver `resolveAPIGatewayV2AuthorizerCognito` filters `AuthorizerType == "JWT"`, parses `JwtConfiguration.Issuer` URL `https://cognito-idp.{region}.amazonaws.com/{poolId}` via `strings.Cut`, rebuilds user-pool ARN, emits `uses` edge. Non-Cognito JWT issuers (Auth0/Okta) and malformed URLs skipped — no phantom edges.

### R1 resolver gaps (CloudWatch alarm dimensions — prior session)
- **CloudWatch** metric alarm → monitored resource via `Namespace` + `Dimensions[]`. Static namespace→(type,dim) map covers AWS/EC2 (InstanceId), AWS/RDS (DBInstanceIdentifier/DBClusterIdentifier), AWS/Lambda, AWS/SQS, AWS/SNS, AWS/DynamoDB, AWS/ApplicationELB + AWS/NetworkELB (NativeID suffix match on `loadbalancer/...`), AWS/EKS. Pre-builds per-type indexes: `(type,region,name)`, `(region,instance-id)` for EC2, and `loadbalancer/...` suffix for ELBv2. Supports metric-math alarms (`Metrics[].MetricStat.Metric`). Unmatched (namespace, dim, or value) skipped — no phantom edges. Edge: `uses`.

### R1 resolver gaps (Route53 alias reverse-lookup — prior session)
- **Route53** RecordSet `AliasTarget.DNSName` → backend (ELBv2 LB, CloudFront distribution, APIGW v1 + v2 custom domain). Pre-builds DNS → backend index over scanned resources; normalizes trailing dot + leading `dualstack.` prefix before lookup. Unmatched DNS skipped (no phantom edges). S3-website aliases deferred (require `RecordSet.Name` → bucket mapping).

### R1 resolver gaps (IAM federated trust — prior session)
- **IAM** role → SAML/OIDC provider via `AssumeRolePolicyDocument` trust policy. SDK URL-encodes the doc; resolver `url.QueryUnescape` + parse, extract `Principal.Federated` (string or array), classify ARN by `:saml-provider/` / `:oidc-provider/` substring, emit `assumes` edge. Covers both `TypeIAMRole` and `TypeIAMServiceLinkedRole`.

### R1 resolver gaps (APIGW usage-plan stages — prior session)
- **APIGW** UsagePlan → REST API Stage via `ApiStages[]` on usage-plan attrs (scanner already stores full `GetUsagePlans` item). Rebuild stage NativeID using scanner `arn:aws:apigateway:{region}::/restapis/{apiId}/stages/{stage}` shape; emit `attached-to`.

### R1 resolver gaps (ECS secrets + EC2 keypair + Orgs delegation — prior session)
- **ECS** task-def → Secrets Manager secret / SSM Parameter via `ContainerDefinitions[].Secrets[].ValueFrom`. Handles full Secrets Manager ARNs (trims `:key::ver-stage:ver-id` suffix), full SSM ARNs, bare parameter names.
- **EC2** instance → KeyPair via region+name index over scanned key pairs (instance carries `KeyName` only; KeyPair NativeID by KeyPairId).
- **Organizations** → delegated-admin accounts via `ListDelegatedAdministrators` + `ListDelegatedServicesForAccount`. Emit `attached-to` edge from organization to each delegated admin with service principals in edge attrs; distinct from hierarchy `contains` path.
- **Store scrubber** refined: sensitive JSON keys now redact only scalar values, not entire subtrees. Lets structural containers like ECS `Secrets` (array of `{ValueFrom}` refs) survive scrub while `SecretString`, `Password`, etc. still caught at leaf.

### Security/ops R2 scanners + R1 cleanup (prior session)
- **SSM** — `aws:ssm:parameter`, `aws:ssm:document`, `aws:ssm:patch-baseline`. Resolver: SecureString parameter → KMS key (skips AWS-managed `alias/aws/*`). Parameter *values* deliberately never fetched.
- **GuardDuty** — `aws:guardduty:detector`, `aws:guardduty:filter`, `aws:guardduty:ipset`. Resolvers: filter→detector, ipset→detector (both `contains`); ipset `Location` URL parsed (`s3://`, virtual-hosted, path-style) → S3 bucket `uses`. Hierarchy closure populated.
- **AWS Config** — `aws:config:recorder`, `aws:config:delivery-channel`, `aws:config:rule`. Resolvers: recorder→IAM role (assumes); delivery-channel→S3 + KMS + SNS; custom-Lambda rules → Lambda function.
- **Backup** — `aws:backup:vault`, `aws:backup:plan`, `aws:backup:selection`. Resolvers: vault→KMS (skips managed), selection→plan (contains), selection→IAM role (assumes). Hierarchy closure populated.
- **ACM Private CA** — `aws:acm-pca:*`. Closes pre-existing ACM cert→CA dangle. Resolver: CA→S3 (CRL bucket).
- **APIGW** authorizer → Cognito user-pool (REST v1 `COGNITO_USER_POOLS` + `ProviderARNs[]`).

---

## NOW — 1–2 sprints

Theme: finish AWS resolver debts before sprawl to more providers.

### R1. Same-service AWS resolver gaps
Edges whose attrs already in store but no resolver emits them.
- **Route53** RecordSet → S3-website via `RecordSet.Name` bucket-name parse (deferred — `AliasTarget.DNSName` alone insufficient).
- *(removed — IAM policy doc → KMS/S3/Secrets/DynamoDB landed; see COMPLETED R1.5)*

### R2. New AWS scanners — highest graph/security value
Order by edges-opened vs scanner effort.
1. *(removed — delegated-admin edges landed; no outstanding org-scanner work)*
2. **Inspector v2** — `aws:inspector2:finding` (stretch — maybe separate table not resources row).
3. *(removed — Security Hub scanner + product-subscription resolver landed; see COMPLETED R2.3)*
4. *(removed — Macie scanner + classification-job/allow-list → S3 resolvers landed; see COMPLETED R2.4)*
5. *(removed — MSK scanner + Lambda ESM→MSK landed; no outstanding cluster-level work)*
6. *(removed — EventBridge api-destination + connection landed; see COMPLETED R2.6)*
7. *(removed — CloudTrail event-data-store landed; see COMPLETED R2.7)*
8. *(removed — CloudFormation stack + stack-set scanners landed; see COMPLETED R2.8)*
9. *(removed — IAM Identity Center + Identity Store scanners landed; see COMPLETED R2.9)*
10. *(removed — IAM SAML + OIDC provider scanners landed; federated-trust resolver already emits role → provider edges)*
11. *(removed — Network Firewall scanner + resolvers landed)*
12. *(removed — Shield Advanced scanner + resolvers landed; see COMPLETED R2.12)*
13. **Detective** — `aws:detective:graph`, `:member`. Edges: graph → member accounts.
14. **Governance surface** — Audit Manager, Artifact, Service Catalog, Control Tower. Group; prioritise by user demand.
15. **Compute surfaces** — Elastic Beanstalk, App Runner, Lightsail, Batch. Group; each adds workload types + VPC/SG/IAM edges.
16. **Lake Formation** — `aws:lakeformation:resource`, `:permissions`. Edges over Glue catalog + S3 data lakes.
17. **Comms** — SES identities/configuration-sets, Pinpoint apps. Lower priority.
18. **Data services:** Glue, Athena, Redshift, OpenSearch/ES, Neptune, DocumentDB — each separate scanner; prioritize by user demand.

Deferred within services already landed:
- **SSM document** content → referenced IAM role (needs YAML/JSON doc-content parser).
- **Backup selection** → tagged resources (needs tag-condition-expression expansion against resources table).
- **GuardDuty detector** → member accounts (cross-account edges beyond current provider scope).

### R3. Azure scanner expansion
Current Azure: AKS, AppService, Compute (VMs/VMSS/Disks/Galleries/Dedicated/CloudServices/Infra), KeyVault, Network, ResourceGroups, SQL (Database/Server + children + Managed), Storage.

**Add, priority order:**
1. **Entra ID** (formerly Azure AD) — users, groups, app registrations, service principals. Edges to RBAC assignments = graph payoff.
2. **RBAC role assignments** — `Microsoft.Authorization/roleAssignments`. Edges: principal → scope (management-group / subscription / RG / resource). Highest-value Azure edge type.
3. **Managed Identities** — system-assigned (on VM/AppService/etc.) + user-assigned. Edges: resource → identity → RBAC assignments.
4. **Azure Monitor / Log Analytics workspaces** — workspace + solutions + data collection rules. Edges: resource diagnostic settings → workspace.
5. **Azure Policy** — assignments, definitions, exemptions. Edges: assignment → scope, → role (for DINE/deployIfNotExists).
6. **Defender for Cloud** — pricing tiers, recommendations, assessments per subscription.
7. **Container Registry (ACR)** — registry, replication, webhook. Edges: registry → KeyVault (customer-managed key), AKS → registry (pull auth).
8. **Container Apps / Container Instances** — edges: → VNet, → ACR, → Log Analytics.
9. **Cosmos DB** — account, database, container. Edges: account → KeyVault (customer-managed), → VNet private endpoints.
10. **PostgreSQL / MySQL flexible servers** — edges to VNet, KeyVault, backup vaults.
11. **Redis Cache** — edges to VNet, KeyVault, private endpoint.
12. **Event Grid + Event Hubs + Service Bus** — edges: topic/namespace → KeyVault, → private endpoint; subscription → destination (function/queue/webhook).
13. **Functions** (if not under AppService) — edges to storage account, KeyVault, Insights.
14. **Logic Apps** — workflow → API connection → downstream resources.
15. **Application Gateway / Front Door / Traffic Manager / API Management** — networking L7 ingress; edges: → Key Vault certs, → WAF policy, → backend pools (AppService / VM / etc.).
16. **Private Endpoints / Private DNS Zones** — edges: PE → VNet subnet → target resource.
17. **DNS Zones** (public + private) — records → target resource.
18. **ExpressRoute / Virtual WAN / VPN Gateway** — enterprise networking.
19. **Databricks + Synapse + Data Factory** — workspace + linked services.
20. **Management groups + Subscriptions** (currently scoped input, not scanned as resources).

### R4. GCP scanner expansion
Current GCP: Compute (incl. some networking), GKE, Hierarchy, IAM (SA-level), SQL, Storage.

**Add, priority order:**
1. **IAM policy bindings** at project/folder/org level (flagged in old roadmap). Bindings → member principals (user/group/SA/domain) + role → scope. Foundation for every cross-resource access query in GCP.
2. **Service account keys** — edges: key → SA.
3. **Cloud KMS** — keyring, cryptoKey. Edges from Storage bucket / BigQuery dataset / Compute disk CMEK references.
4. **Secret Manager** — secret, version. Edges: secret → KMS, → IAM policy.
5. **VPC firewall rules** — edges: rule → VPC, rule target tags → instances.
6. **Load Balancers** (global + regional HTTP(S), TCP/SSL, internal) — forwarding rule → target proxy → URL map → backend service → backend (instance group / NEG / bucket).
7. **Cloud Armor** — security policy → backend service attachment.
8. **Certificate Manager** — cert, map, map entry, dns authorization.
9. **Cloud DNS** — managed zones + record sets. Edges: record → target IP / LB.
10. **Cloud Functions (Gen1 + Gen2) + Cloud Run** — edges: function → SA, → trigger (Pub/Sub / HTTP / storage), → VPC connector.
11. **Pub/Sub** — topic, subscription, schema. Edges: subscription → push endpoint, → BigQuery dataset (BigQuery subscriptions), → dead-letter topic.
12. **BigQuery** — dataset, table, routine, model. Edges: dataset → CMEK key, → authorized views, table → external source (Storage / Drive).
13. **Bigtable / Firestore / Spanner** — instance + database; edges to CMEK, backups.
14. **Dataproc / Dataflow / Composer** — cluster / job / environment; edges to network, SA.
15. **Artifact Registry** — repo (docker/npm/maven/...). Edges: repo → CMEK, GKE/Cloud Run → repo pull.
16. **Cloud Logging sinks** + monitoring alert policies — sink → destination (GCS/BQ/PubSub).
17. **Cloud Build triggers** — trigger → repo + worker pool.
18. **VPC Service Controls** — perimeter, bridge. Edges: perimeter → projects + services.
19. **Binary Authorization** — policy, attestor. Edges: policy → KMS attestor keys.
20. **Cloud Run Jobs, Batch** — job → SA + network.

### R5. Cross-service resolvers (multi-provider aware)
Most resolvers same-provider same-service today. Add:
- **AWS** cross-account references in `relationships` already work via account ID in ResourceID. Add explicit `aws:cross-account-trust` edges: IAM role trust policy principals → `arn:aws:iam::<other-acct>:...`.
- **Azure** cross-subscription RBAC assignments (cross by construction).
- **GCP** cross-project IAM (member in another project, role at higher scope).
- **Cloud-to-cloud** (opt-in, expensive): dangling DNS records → rechecked against other clouds. Out of scope unless user demand.

---

## NEXT — this quarter

### G1. Graph command enhancements
Current: `disco graph <id> --depth N --kinds X --direction both --output table|json|dot`. Extend:

- **`disco graph path <A> <B>`** — shortest-path BFS between two resource IDs. Output edge list or dot subgraph. Answers "can this IAM role reach this bucket?"
- **`disco graph blast <id>`** — blast radius. All reachable nodes by distance; default kinds = all. Emit rings (distance 1, 2, 3). Ties to rule engine: flag blast-radius outliers.
- **`disco graph reverse <id>`** — inbound-only traversal. Answers "what references this KMS key?"
- **`--format mermaid`** — Mermaid output for docs + Slack embeds.
- **`--prune-types aws:iam:*`** — skip noisy types from traversal.
- **`--prune-regions us-east-1`** — constrain traversal by region.
- **`--cluster by=provider|region|account`** — dot output layout hints (`subgraph cluster_X`) so big graphs readable.
- **`--label-template '{{.Name}}\n{{.Type}}'`** — custom node labels.
- **`--max-nodes 500`** — soft cap with truncation summary.
- **Path provenance** — store `source_resolver` on each relationship row (migration) so graph output can annotate "emitted by: resolveLambdaRelationships". Debuggability win.
- **Incremental traversal** — cache closure walk for hot IDs (likely premature; revisit after benchmarks).

### G2. Relationships table evolution
- Add `source_resolver TEXT` column (migration) populated from resolver name via context.
- Add `confidence REAL` column for heuristic edges (e.g. Route53 alias reverse-lookup).
- Add index on `(to_id, kind)` — current indexes likely cover `from_id` only; reverse-graph queries pay for it.

### G4. Rule engine expansion
- **Graph-aware rules** — rules that traverse relationships, not just filter resources. E.g. "Lambda function with env-var secret that does NOT have a KMS edge". Needs small graph DSL in YAML.
- **Severity profiles** — CIS / NIST / PCI bundles. Each a rule-pack YAML.
- **Suppression** — `disco check --suppress suppressions.yaml` (by resource ID or rule ID + scope).
- **Baseline diff** — `disco check --baseline findings.json` reports only new findings since baseline.

### G5. `disco coverage`
New command: prints coverage matrix vs CloudFormation / ARM / GCP Asset Inventory registries. Uses `KnownTypes()` per provider. Markdown output for README inclusion.

### G7. `disco check --format sarif`
SARIF v2.1.0 output for GitHub / GitLab code-scanning integration. Rule ID → SARIF ruleId, severity → level, resource ID → result.locations. Enables PR-gate workflows without custom glue.

### G8. `disco export` / `disco import`
Portable DB snapshots: export resources + relationships + scans to single JSONL bundle, re-importable into fresh DB. Distinct from L5 SIEM sinks — this for offline analysis, airgapped reviews, support dumps.

### G9. Redaction verification + deprecation registry
- `disco audit --redaction` — re-runs `scrubAttributes` over stored rows, flags entries written before denylist update.
- Type-deprecation registry — `KnownTypes()` entries marked deprecated; list / graph annotate affected rows.

---

## LATER — 6–12mo / v1.0

### L1. New providers
- Kubernetes (kubeconfig-driven; feed pods/services/secrets/ingresses into graph; bridges EKS/AKS/GKE context).
- Oracle Cloud, DigitalOcean, Cloudflare (DNS/WAF adjacent).

### L4. Web UI (separate repo)
- Graph visualizer + rule results. Consumes L3 API.

### L8. Query language
- `disco query 'provider=aws type=kms:key upstream=lambda:function'` — unified over list + graph. Stretch; revisit after G1 stabilizes.

---

## Cross-cutting

- **Scan retention / compaction** — policy for pruning old `scans` + orphaned `resources` rows on long-lived DBs. Distinct from G3 (incremental); retention is time-axis concern.
- **Multi-account AWS role chaining** — codify cross-account `AssumeRole` workflow for scanning N accounts from one runner (config shape, credential cache, per-account scan record). Companion to R5 cross-account-trust edges.
- **Performance** — benchmark harness (`go test -bench`) with fake provider emitting N resources. Target: 10k resources/sec UpsertResources, 100k edges/sec for closure inserts.
- **Observability** — slog with `scan_id` correlation throughout. Redirect provider SDK logs behind flag.
- **Docs** — `go generate`-produced `docs/coverage.md` (supersedes `disco coverage` command or complements it).
- **CI** — coverage budget enforcement per-package; test gates per-provider.
- **Release** — goreleaser pipeline (linux/darwin/windows amd64+arm64), signed binaries.

---

## Deferred / tracked gaps

### Lint / cleanup
- `internal/providers/aws/apigateway_resolvers.go` — three remaining `strings.Index` sites (grep `strings.Index`) → `strings.Cut` simplifications. Pre-existing.

### Synthetic NativeIDs — KMS grant
- `aws:kms:grant` NativeID synthesized as `{keyARN}/grant/{grantId}` because `ListGrants` returns no canonical ARN (only `GrantId`). Shape is stable across rescans but not an AWS-issued identifier — anything that pattern-matches "`arn:aws:kms:...`" ARNs elsewhere (e.g. future cross-service resolvers, external tools consuming `disco export`, SIEM enrichment) could collide or misclassify. If AWS ever adds a `GrantArn` field to the ListGrants response (or `DescribeGrant` surfaces one), switch to it and migrate; the synthetic form only needs to hold until then. Precedent for synthetic shape: EFS mount-target + Backup selection already use similar `{parent}/...` patterns (documented in CLAUDE.md).

### Persisting non-resource scan state
- S3 bucket encryption config (`GetBucketEncryption`) currently lives only on `account.s3BucketEncryption` during one scan+resolve run. KMS edge is persisted; underlying `ServerSideEncryptionConfiguration` is not. Not visible in `disco list` / `disco graph <bucket> --output json` attrs. If we later want the raw config queryable (rule-engine checks like "bucket encrypted with CMK of rotation age < N days", compliance exports), add a generic sidecar — options from the design discussion: (a) new `resource_configs(resource_id, config_type, payload_json)` table via migration, or (b) per-service sidecar column. Applies to any future `Get*Config` fetch whose result is not its own AWS resource (candidates: bucket versioning, bucket logging, bucket notification, bucket public-access-block, bucket replication, queue attributes not already flattened). Do not wrap the primary resource's SDK attrs — that's been ruled out.

### Not worth building (for now)
- Real-time streaming scan (SQS/EventBridge-driven). Batch fine.
- Fancy CLI TUI (`disco ui`). Web UI (L4) preferred.
- Built-in secret scanning of resource attrs beyond key-name denylist. Out-of-scope; use dedicated tools (trufflehog etc.) upstream.

---

## Verification

Roadmap doc. Validate by:
1. User review for priority/scope fit.
2. Each numbered item (R1 / R2.N / R3.N / R4.N / G1 / ...) → tracked issue with acceptance criteria before implementation.
3. Resolver additions: existing test pattern per `CLAUDE.md` — `newTestStore` + `upsertTestResource` + `assertRelationship`.
4. New scanner additions: update `expectedAWSServices` / equivalent Azure+GCP, add `<svc>_resolvers_test.go`, verify in `types` command output.