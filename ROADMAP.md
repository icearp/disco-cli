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

### R3.9 Azure Cosmos DB (this session)
- **Azure Cosmos DB** new type `azure:microsoft.documentdb:database-account`. Subscription-scoped service `azure:cosmos` runs one phase: `DatabaseAccountsClient.NewListPager` from `armcosmos`. NativeIDs are full Azure resource IDs verbatim. Hierarchy pair to RG. New SDK dep: `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos`. Per-API child resources (SQL/Mongo/Cassandra/Gremlin/Table databases + containers/graphs) deferred — they explode in volume on multi-tenant accounts and the account row alone carries the security-relevant edges.
- **Resolver** `resolveCosmosRelationships` derives account -[uses]-> Key Vault via the `properties.keyVaultKeyUri` CMEK reference. Reuses `vaultNameFromKeyURI` from the ACR resolver. Cosmos's user-assigned identity → MSI edges already covered by the generic `resolveManagedIdentityConsumers` resolver. Private endpoint connection edges deferred (requires Microsoft.Network/privateEndpoints scanner).
- Out of scope: SQL/Mongo/Cassandra/Gremlin/Table API databases + containers, throughput settings, restorable accounts, cluster-scoped IP rules detail, private endpoint connections.
- Live-scan validation: 1 sub, 0 accounts, scanner ran clean.

### R3.7 Azure Container Registry (this session)
- **Azure Container Registry** new type `azure:microsoft.containerregistry:registry`. Subscription-scoped service `azure:containerregistry` runs one phase: `RegistriesClient.NewListPager` from `armcontainerregistry`. NativeIDs are full Azure resource IDs verbatim. Hierarchy pair to RG via `rgHierarchyPair`. New SDK dep: `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry`.
- **Resolver** `resolveContainerRegistryRelationships` derives ACR -[uses]-> Key Vault when CMEK is enabled. The CMEK reference on a registry is a Key Vault key URI (`https://{vault}.vault.azure.net/keys/{name}/{version}`); resolver parses the host via new helper `vaultNameFromKeyURI` (handles public, US-government, China, and Germany Key Vault DNS suffixes), then matches the leading subdomain against a per-sub vault-name index built from existing keyvault-scanned vaults. AKS-pull edges (registry ← AKS) deferred — AKS configures pull via role assignments + identity rather than direct ARM reference. ACR's user-assigned identity → MSI edges already covered by the generic `resolveManagedIdentityConsumers` resolver from R3.3.
- Out of scope: replications, webhooks, tasks, scope-maps, tokens, cache rules, agent pools, private link resources — sub-resources with narrow cross-service edge value.
- **CLAUDE.md** addition: Key Vault key/secret URI → vault parsing pattern documented inline in `containerregistry_resolvers.go::vaultNameFromKeyURI` for reuse by future resolvers (Storage account CMEK, Cosmos DB CMEK, Disk Encryption Set, etc.).
- Live-scan validation: 1 sub, 0 registries / 0 vaults, scanner ran clean.

### R3.4 Azure Log Analytics workspaces (this session)
- **Azure Log Analytics** new type `azure:microsoft.operationalinsights:workspace`. Subscription-scoped service `azure:operationalinsights` runs one phase: `WorkspacesClient.NewListPager` from `armoperationalinsights`. NativeIDs are full Azure resource IDs verbatim. Hierarchy pair to RG via `rgHierarchyPair`. New SDK dep: `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights`.
- **No resolver this iteration**. Workspaces are predominantly an edge *target*: most edges land via diagnostic-settings (resource → workspace), which lives on every diagnosable resource and is its own per-resource API call (`armmonitor.DiagnosticSettingsClient.List`). Implementing diagnostic-settings cleanly requires a cross-service resolver pass (walk all azure resources, batch GET diagnostic settings, FK to workspace/storage/eventhub destinations); deferred to a follow-up so this iteration stays tight. Workspace rows alone provide inventory + tagging value for compliance/audit queries today.
- Out of scope: solutions (`armoperationsmanagement`), data collection rules / endpoints (`armmonitor`), saved searches, linked services, query packs, dedicated clusters, Application Insights components — separate sub-resources with narrow cross-service edge value.
- Live-scan validation: 1 sub, 0 workspaces, scanner ran clean.

### R3.3 Azure user-assigned managed identities (this session)
- **Azure Managed Identity** new type `azure:microsoft.managedidentity:user-assigned-identity`. Subscription-scoped service `azure:managedidentity` runs one phase: `UserAssignedIdentitiesClient.NewListBySubscriptionPager` from `armmsi`. Per-phase `AccessDenied` tolerated. NativeIDs are full Azure resource IDs verbatim. Hierarchy pair to RG via existing `rgHierarchyPair` helper. System-assigned identities are not standalone resources — they live as a `principalId` attribute on their host (VM, AppService, etc.) and surface to graph queries via the host's role assignments without needing their own resource row. Federated identity credentials (`FederatedIdentityCredentialsClient`) deferred — workload-identity sub-resource, narrow customer base.
- **Resolvers**. `resolveManagedIdentityAssignmentPrincipals` builds a per-sub `principalId → MSI-resourceID` index from each user-assigned identity's `properties.principalId` (the Entra service-principal object id Azure issues for the MSI), then walks role assignments and emits `assignment -[uses]-> MSI` whenever the assignment's principalId matches. Closes one branch of the principal-edge gap left by R3.2 without requiring an Entra ID scanner; user/group/non-MSI service-principal principals still wait on R3.1. `resolveManagedIdentityConsumers` walks every Azure resource in the subscription, parses the standard `identity.userAssignedIdentities` map (provider-agnostic — Azure SDK serializes this consistently across VM / VMSS / AppService / AKS / Storage / etc.), and emits `consumer -[uses]-> MSI` for each ARM-ID key matched against the MSI NativeID index (case-insensitive). Both casings (`identity` / `Identity`) tried since some older arm* packages ship capitalized JSON tags.
- Out of scope: federated identity credentials, system-assigned identity as own resource type (no Azure ARM ID exists for it; the host resource carries the principalId directly), cross-subscription identity-consumer edges (a VM in sub A can use a UA-MSI from sub B — would require a tenant-wide MSI index).
- Live-scan validation: 1 sub (no MSIs, no VMs in this sub), scanner ran clean.

### R3.2 Azure RBAC role assignments + role definitions (this session)
- **Azure Authorization** new types `azure:microsoft.authorization:role-assignment`, `azure:microsoft.authorization:role-definition`. Subscription-scoped service `azure:authorization` runs two phases sequentially. Phase 1: `RoleDefinitionsClient.NewListPager(scope=/subscriptions/{sub})` returns built-in + custom defs visible at the subscription scope (Azure rewrites built-in IDs with the sub prefix in this call). Phase 2: `RoleAssignmentsClient.NewListForSubscriptionPager` returns every assignment whose scope ⊇ subscription (including assignments at the sub itself, RGs, and individual resources). Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`. NativeIDs are full Azure resource IDs verbatim (`/subscriptions/.../providers/Microsoft.Authorization/role(Assignments|Definitions)/{guid}`). New SDK dep: `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2`. Built-in role definitions are tenant-scoped but get duplicated per subscription (acceptable — `ResourceID` hash differs per `account_id` and resolvers operate per-sub, so FK lookups stay local).
- **Resolver** `resolveAuthorizationRelationships` derives two edges per assignment: → role-definition (`uses`) via FK on `properties.roleDefinitionId` matched against the same-sub role-definition NativeID, and → scoped-resource (`attached-to`) via case-insensitive lookup of `properties.scope` in a per-subscription lowercased NativeID index. Azure returns scope strings in arbitrary case (matches whatever the user typed at assignment time), so the index lowercases every Azure resource NativeID once and the resolver lowercases scope before lookup. Self-edges (assignment scope = its own ARN, theoretically possible) skipped via `toID != r.ID` guard. Principal edges (assignment → user/group/service-principal/managed-identity) intentionally deferred until R3.1 Entra ID scanner lands — `principalId` + `principalType` preserved in `attributes` for backfill.
- Out of scope: deny assignments (`DenyAssignmentsClient`, read-only Microsoft-managed at landing-zone level — narrow customer use), classic admins (deprecated), eligible/PIM role schedules (`RoleEligibilitySchedules` — Microsoft Entra Privileged Identity Management; separate licensing tier), management-group / tenant-root scope assignments not visible from the subscription (would need explicit MG enumeration via `armmanagementgroups`), provider-operations metadata (`ProviderOperationsMetadataClient` — surface-area catalog rather than deployed resource).
- Live-scan validation: 1 sub (`b61c54cf-...`), 1 role assignment (Owner — me), 873 role definitions, 1 edge (asn → role-def). Scope edge zero — assignment scope is the sub root which has no resource representation. No errors, no warnings.

### R2.9 IAM Identity Center (SSO) + Identity Store (this session)
- **IAM Identity Center** new types `aws:sso:instance`, `aws:sso:permission-set`, `aws:sso:account-assignment`. **Identity Store** new types `aws:identitystore:user`, `aws:identitystore:group`. Single regional service `aws:sso-admin` runs four phases: (1) `ListInstances` (paginator), (2) per-instance `ListPermissionSets` + concurrent `DescribePermissionSet` fan-out (errgroup + `semaphore.NewWeighted(fanoutMed)`), (3) per (instance, permission-set, account) triple — built sequentially via `ListAccountsForProvisionedPermissionSet` per perm-set then concurrent `ListAccountAssignments` fan-out (`fanoutHigh`), (4) per `IdentityStoreId` `ListUsers` + `ListGroups` against the identity-store SDK client. Phase-level `AccessDenied` tolerated via `skipIfAccessDenied` — non-management accounts cannot read SSO admin APIs and bail at phase 1. ListInstances is region-scoped (returns instances active only in the calling region), so non-home regions short-circuit immediately. Two new SDK deps added: `github.com/aws/aws-sdk-go-v2/service/ssoadmin`, `github.com/aws/aws-sdk-go-v2/service/identitystore`.
- **Synthetic NativeIDs**. Account-assignments lack any AWS-issued ARN; synthesized as `{permissionSetArn}/account/{accountId}/{principalType}/{principalId}` so re-scans dedupe and the permission-set ARN (which already encodes the instance id) carries forward. Identity Store users/groups likewise lack ARNs; synthesized as `arn:aws:identitystore::{ownerAcct}:user/{IdentityStoreId}/{UserId}` and `…:group/…/{GroupId}`. Documented in aws/CLAUDE.md "Synthetic NativeIDs" section (precedent: KMS grant, EFS mount-target, Backup selection).
- **Resolvers**. `resolveSSOPermissionSetInstance` rebuilds the canonical instance ARN from the permission-set ARN's `:permissionSet/{ssoins-id}/{ps-id}` shape via `strings.Cut`, emits `contains` edge from instance to each permission-set. `resolveSSOAccountAssignments` walks each scanned assignment and emits up to three edges: assignment → permission-set (`uses`), assignment → identity-store user/group (`uses`, branched on `PrincipalType`), assignment → Organizations account (`attached-to`, via existing `loadOrgTargetIndex` mapping 12-digit account-id to canonical Organizations account ARN per aws/CLAUDE.md "Organizations NativeID = full ARN" rule). All four target lookups FK-safe via per-type id sets + the org-index map; partial-coverage scans (no Identity Store creds, no Org tree scanned) silently skip those branches without erroring. Instance metadata (`IdentityStoreId`, `OwnerAccountId`) preloaded into a single `ssoInstanceIndex` so the resolver does not re-decode attrs per assignment row.
- Out of scope: permission-set → managed-policy / customer-managed-policy / inline-policy edges (separate `ListManagedPoliciesInPermissionSet` + `ListCustomerManagedPolicyReferencesInPermissionSet` + `GetInlinePolicyForPermissionSet` fan-outs); SSO applications (`ListApplications`, `ListApplicationAssignments`); group memberships (`ListGroupMemberships` per group adds another fan-out tier); permission boundary on permission-set; assignment → AWS account fallback when Org tree NOT scanned (would require an `aws:account` synthesized type, deferred).

### R2.8 CloudFormation stacks + stack-sets (this session)
- **CloudFormation** new types `aws:cloudformation:stack`, `aws:cloudformation:stack-set`. Regional scanner `scanCloudFormation` runs two phases. Phase 1: `ListStacks` (paginator, filtered to active statuses — DELETE_COMPLETE excluded since deleted stacks return empty resource lists for 90 days), then per-stack `ListStackResources` paginator fan-out (errgroup + `semaphore.NewWeighted(fanoutMed)`). `AttributesJSON` wrapped as `{"Stack": <StackSummary>, "Resources": <[]StackResourceSummary>}` (embedding child data). Stack NativeID = full `StackId` ARN. Per-stack `ValidationError` (stack vanished between list+describe) and `AccessDenied` tolerated; persists stack with empty Resources rather than dropping. Phase 2: `ListStackSets` + per-set `DescribeStackSet` + `ListStackInstances` fan-out. Stack-set NativeID = `StackSetARN`. Non-admin accounts return either `AccessDenied` or `ValidationError` ("StackSets is not active in this account") — both caught via `isStackValidationError` helper alongside `isAccessDenied`, skipped without barring phase 1 totals. Resolvers: `resolveCloudFormationStackResources` walks `Resources[]` per stack and emits `contains` edges to managed AWS resources via a table-driven `cfnTypeMap` (CFN ResourceType → disco type + NativeID synthesis func). 27 entries cover S3, IAM (role/user/managed-policy), Lambda function + layer, EC2 (instance/SG/VPC/subnet), DynamoDB, SNS, SQS (queue URL → ARN by trailing path segment), Logs log-group, KMS, Secrets Manager, RDS instance + cluster, SFN, EventBridge rule (default-bus only — pipe-form `BUS|NAME` rejected since CFN strips bus context) + event-bus, EFS file-system, ECR, Kinesis, SSM (leading slash trimmed), ELBv2 LB + target-group, APIGW v1 REST API + v2 API, and `AWS::CloudFormation::Stack` (nested-stack pass-through for stack→stack edges). Skip-list: empty `PhysicalResourceId`, `ResourceStatus` ∈ {CREATE_FAILED, DELETE_*}, unmapped types (e.g. RDS::DBSubnetGroup, custom resources). FK-safe via single combined id-set query (one `ListResources` over `cfnTypeMap`'s union of types). `resolveCloudFormationStackSetInstances` emits stack-set → deployed stack `contains` edges via `Instances[].StackId`, FK-safe across accounts (stack lookup unfiltered by acct.ID since instances commonly live in member accounts). Out of scope this pass: drift detection, template-level (`!Ref` / `!GetAtt`) edges, output-export resolution, `AWS::CloudFormation::CustomResource` (arbitrary user-string physIDs), stack-instance as own type.

### R2.15 Elastic Beanstalk (this session)
- **Elastic Beanstalk** new types `aws:elasticbeanstalk:application`, `aws:elasticbeanstalk:environment`. Regional scanner `scanElasticBeanstalk` runs two phases. Phase 1: `DescribeApplications` (single call, no pagination — small per-account quota). Phase 2: `DescribeEnvironments` with manual `NextToken` loop (no SDK paginator per `aws/CLAUDE.md` "SDK v2 paginator availability per-op"). Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`. NativeIDs are AWS-issued ARNs verbatim (`ApplicationArn`, `EnvironmentArn`). Application versions, configuration templates, configuration option settings, and platform versions deferred — versions explode in volume on long-lived apps; the underlying CFN stack created per environment is already covered by the existing `aws:cloudformation:stack` scanner if Org has CFN visibility.
- **Resolver** `resolveBeanstalkEnvironmentTargets` emits application → environment `contains` edge keyed on `EnvironmentDescription.ApplicationName` matched against scanned applications by `Name`. Beanstalk environments reference their app by name (not ARN), so the resolver builds a name→app-id index. FK-safe via app id-set; cross-account / unscanned apps skip silently.
- Out of scope: environment → CloudFormation stack edge (would require Beanstalk → CFN stack-id lookup; CFN stack name is `awseb-<env-id>-stack` but `EnvironmentDescription` doesn't expose the stack id directly), environment → ELB load balancer (`Resources.LoadBalancer.LoadBalancerName` is shape `<bytes>` not ARN), environment → S3 bucket for app source bundle.

### R2.15 Lightsail (this session)
- **Lightsail** new types `aws:lightsail:instance`, `aws:lightsail:database`, `aws:lightsail:container-service`. Regional scanner `scanLightsail` runs three phases sequentially. Lightsail's SDK doesn't ship paginators (per `aws/CLAUDE.md` "SDK v2 paginator availability per-op") — manual `PageToken`/`NextPageToken` loop used for `GetInstances` and `GetRelationalDatabases`. `GetContainerServices` returns the full list in a single call (no pagination). Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`. NativeIDs are AWS-issued ARNs verbatim. Snapshots, disks, key pairs, static IPs, distributions, domains, buckets, load balancers deferred — Lightsail's resource graph is largely self-contained per service, adding little cross-service edge value.
- **No resolvers** this iteration. Lightsail resources don't reference EC2/IAM/KMS targets in ways that map cleanly to disco's existing types — the service is intentionally a self-contained PaaS abstraction. The resource rows themselves provide inventory + tagging value for compliance/audit queries; cross-service edges would add minimal graph value relative to scanner volume.

### R2.15 Batch (this session)
- **AWS Batch** new types `aws:batch:compute-environment`, `aws:batch:job-queue`, `aws:batch:job-definition`. Regional scanner `scanBatch` runs three phases sequentially, each `Describe*` paginator-native with full body on List. Job-definitions filtered to `Status=ACTIVE` to drop historical revisions (unbounded volume, graph-irrelevant). Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`. Job runs (`ListJobs`) deferred — event data per the Macie/Detective/SecurityHub event-data precedent. NativeIDs are AWS-issued ARNs verbatim (`ComputeEnvironmentArn`, `JobQueueArn`, `JobDefinitionArn`).
- **Resolvers**. `resolveBatchComputeEnvironmentTargets` emits compute-env outbound edges: → IAM service role + IAM instance role (`assumes`), → subnet (`uses`) per `ComputeResources.Subnets[]`, → security group (`uses`) per `ComputeResources.SecurityGroupIds[]`. Fargate compute-envs (no `ComputeResources.InstanceRole`/`SecurityGroups`) skip those branches via the nil-check on `ComputeResources`. `resolveBatchJobQueueComputeEnvs` emits job-queue → compute-env (`uses`) per `ComputeEnvironmentOrder[]`, with the dispatch priority preserved as edge attrs JSON `{"order":N}` so graph consumers can reconstruct the queue's CE preference order. `resolveBatchJobDefinitionTargets` emits job-def → IAM job role + IAM execution role (`assumes`), → ECR repository (`uses`) via `ContainerProperties.Image` reusing the `apprunnerImageToRepoARN` helper documented in `aws/CLAUDE.md`. All FK-safe via per-type id sets.
- Out of scope: scheduling policies (`SchedulingPolicy` ARN ref under JobQueue), multi-node parallel jobs (`NodeProperties` variant), ECS task-properties variant (`EcsProperties`), EKS pod-properties variant (`EksProperties`), compute-env → EKS cluster edge (`EksConfiguration.EksClusterArn`), compute-env → ECS cluster (`EcsClusterArn` reverse-derived).

### R2.15 App Runner (this session)
- **App Runner** new types `aws:apprunner:service`, `aws:apprunner:vpc-connector`. Regional scanner `scanAppRunner` runs two phases. Phase 1: `ListServices` (paginator, skeleton) → fan-out `DescribeService` (errgroup + `semaphore.NewWeighted(fanoutMed)`); per-item `AccessDenied` tolerated. Full `Service` body (NetworkConfiguration, SourceConfiguration, EncryptionConfiguration, InstanceConfiguration, AutoScalingConfigurationSummary, ObservabilityConfiguration, etc.) stored as `AttributesJSON`. Phase 2: `ListVpcConnectors` (paginator, full body — Subnets, SecurityGroups, Status). Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`. NativeIDs are AWS-issued ARNs verbatim (`Service.ServiceArn`, `VpcConnector.VpcConnectorArn`). Auto-scaling configurations, observability configurations, custom domains, and outbound-traffic connections deferred — separate sub-resources, narrow value beyond the core service rows.
- **Resolvers**. `resolveAppRunnerServiceTargets` emits five edge kinds per service: → VPC connector (`uses`) via `NetworkConfiguration.EgressConfiguration.VpcConnectorArn`, → IAM instance role (`assumes`) via `InstanceConfiguration.InstanceRoleArn`, → IAM access role (`assumes`) via `SourceConfiguration.AuthenticationConfiguration.AccessRoleArn`, → ECR repository (`uses`) via `SourceConfiguration.ImageRepository.ImageIdentifier` (URL-form parsed to canonical repo ARN by new `apprunnerImageToRepoARN` helper — handles `{acct}.dkr.ecr.{region}.amazonaws.com/{repo}[:tag]` shape, multi-segment repo names preserved, public-ECR / non-ECR registries skip), → KMS key (`uses`) via `EncryptionConfiguration.KmsKey` (resolved through `loadKMSResolveIndex`). `resolveAppRunnerVPCConnectorTargets` emits vpc-connector → subnet (`uses`) + → security group (`uses`) per `Subnets[]` / `SecurityGroups[]`. All FK-safe via per-type id sets + KMS resolve index.
- **CLAUDE.md** addition: ECR image-identifier → repository-ARN parsing convention documented under "ECR image identifier → repository ARN" — reusable for ECS task-def `Image` field and other services that reference ECR by URL form.

### R2.14 Control Tower (this session)
- **Control Tower** new types `aws:controltower:landing-zone`, `aws:controltower:enabled-baseline`. Regional scanner `scanControlTower` runs two phases. Phase 1: `ListLandingZones` (paginator, ARN-only) → fan-out `GetLandingZone` (errgroup + `semaphore.NewWeighted(fanoutMed)`); per-item `AccessDenied` tolerated. Full `LandingZone` body (Manifest, Status, Version, DriftStatus, etc.) stored as `AttributesJSON`. Phase 2: `ListEnabledBaselines(IncludeChildren=true)` (paginator, full body — Arn, BaselineIdentifier, BaselineVersion, TargetIdentifier, StatusSummary, DriftStatusSummary). Enabled controls (`ListEnabledControls`) deferred — that API requires a per-OU `TargetIdentifier` parameter, so it would need fan-out keyed off baseline targets or a separate Org-OU enumeration; defensible to land in a follow-up iteration.
- **Multi-code not-enabled detection** via new `isControlTowerNotEnabled` helper: Control Tower surfaces the not-deployed state under TWO codes — `AccessDeniedException` (calling account is not the org management account) AND `ValidationException` (`AWSControlTowerAdmin` role missing / setup incomplete). Helper matches message hint (`AWSControlTowerAdmin` / `landing zone` / `management account`) then accepts either code, returning `markServiceDisabled` to surface `(service disabled)` on the progress line. New convention documented in `aws/CLAUDE.md` "Control Tower variant — multi-code disambiguation".
- **Resolver** `resolveControlTowerBaselineTarget` emits `attached-to` edge from each enabled baseline to its target Organizations OU or account. Target ARN shape discriminated via substring (`:account/` → `aws:organizations:account`; `:ou/` → `aws:organizations:ou`). FK-safe via per-type id sets; targets outside the scanned org tree skip silently. Out of scope: enabled-control → target edges (defer with the controls scanner), drift-status decomposition, control operations history, baseline override parameters.

### R2.14 Audit Manager (this session)
- **Audit Manager** new types `aws:auditmanager:assessment`, `aws:auditmanager:framework`, `aws:auditmanager:control`. Regional scanner `scanAuditManager` runs three phases sequentially. Phase 1: `ListAssessments` (paginator, skeleton) → fan-out `GetAssessment` (errgroup + `semaphore.NewWeighted(fanoutMed)`); per-item `AccessDenied` tolerated. Full `Assessment` body (Framework, Metadata.Roles, Metadata.AssessmentReportsDestination, Tags) stored as `AttributesJSON`. Phase 2: `ListAssessmentFrameworks(FrameworkType=Custom)` — standard frameworks (PCI-DSS, HIPAA, SOC2, etc.) deliberately skipped, AWS-managed catalogue is huge and graph-irrelevant. Phase 3: `ListControls(ControlType=Custom)` — standard controls likewise skipped. NativeIDs are AWS-issued ARNs verbatim (`Assessment.Arn`, `AssessmentFrameworkMetadata.Arn`, `ControlMetadata.Arn`). Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`.
- **Not-enabled disambiguation** via new `isAuditManagerNotEnabled` helper (sibling to `isMacieNotEnabled`): Audit Manager raises `AccessDeniedException` with message `"Please complete AWS Audit Manager setup..."` when not enabled in the calling region. The helper matches code + message and the scanner returns `markServiceDisabled(err)` to suppress the warning and surface `(service disabled)` on the per-service progress line. Real IAM denial still falls through to `skipIfAccessDenied` (warning preserved). Per `aws/CLAUDE.md` "Macie variant — code+message disambiguation".
- **Resolver** `resolveAuditManagerAssessmentTargets` emits three edge kinds per assessment: → framework (`uses`) via `Framework.Arn`, → IAM role (`assumes`) per `Metadata.Roles[].RoleArn`, → S3 bucket (`uses`) via `Metadata.AssessmentReportsDestination.Destination` (parsed as `s3://bucket/...` via existing `s3BucketARNFromS3URL`, only when `DestinationType == "S3"`). FK-safe via per-type id sets; cross-account refs skip. Framework → control containment edges, evidence-collection links, share-request graph deferred.

### R2.14 Service Catalog (this session)
- **Service Catalog** new types `aws:servicecatalog:portfolio`, `aws:servicecatalog:product`. Regional scanner `scanServiceCatalog` runs two phases. Phase 1: `ListPortfolios` (paginator) → per-portfolio `ListConstraintsForPortfolio` + `SearchProductsAsAdmin(PortfolioId=...)` enrichment fan-out (errgroup + `semaphore.NewWeighted(fanoutMed)`); per-portfolio sub-call `AccessDenied` tolerated via `break`-to-skip without barring sibling portfolios. Portfolio `AttributesJSON` wrapped as `{"Portfolio": <PortfolioDetail>, "Constraints": [<flattened map>], "ProductARNs": [<ARN>...]}` so the resolver can emit portfolio→product edges without re-calling the API. Phase 2: `SearchProductsAsAdmin` (paginator, no `PortfolioId` filter) — fetches the full product catalog admin view; `AttributesJSON` = `ProductViewDetail` verbatim (carries `ProductARN`, `ProductViewSummary` with name/distributor/owner, `Status`, `SourceConnection` for git-backed products). NativeIDs are AWS-issued ARNs verbatim (`PortfolioDetail.ARN`, `ProductViewDetail.ProductARN`). Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`. App Registry (separate `servicecatalogappregistry` SDK) deferred — distinct service.
- **Resolver** `resolveServiceCatalogPortfolioProducts` emits portfolio → product `contains` edges from the embedded `ProductARNs` list. Service Catalog products are many-to-many with portfolios (a product can be in multiple portfolios), so this is a regular relationship edge (not a hierarchy-closure entry — closure represents 1-to-N parent-child). FK-safe via scanned-product id-set; cross-account / shared-portfolio products skip silently.
- Out of scope: provisioned products (instances of products launched into accounts), launch paths, share invitations, TagOption associations, principals (`ListPrincipalsForPortfolio` — IAM role/user grantees), constraint → CloudFormation template links, App Registry applications + attribute groups.

### R2.18 DocumentDB (this session)
- **DocumentDB** new types `aws:docdb:cluster`, `aws:docdb:instance`. DocumentDB has its own dedicated control-plane API (`aws-sdk-go-v2/service/docdb`), distinct from RDS — confirmed via SDK source: `docdb:CreateDBCluster.Engine` valid values = `["docdb"]` only, and the API endpoint is namespaced separately from RDS. Regional scanner `scanDocDB` runs two phases sequentially, both `Describe*` paginator-native with full body on List. NativeID = `DBClusterArn` / `DBInstanceArn` verbatim (DocumentDB ARNs reuse the `arn:aws:rds:` prefix despite the dedicated API — historical artefact). Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`. Cluster snapshots, parameter groups, subnet groups, global clusters deferred — same scope rationale as Redshift.
- **Resolvers**. `resolveDocDBClusterTargets` emits cluster → KMS key (`uses`) via `KmsKeyId` (resolved via `loadKMSResolveIndex`) + cluster → security group (`uses`) per `VpcSecurityGroups[]`. `resolveDocDBInstanceCluster` wires instance → cluster containment via `BatchAddToHierarchyClosure` keyed on `DBInstanceAttrs.DBClusterIdentifier` matched against scanned clusters' `Name` (= cluster identifier). FK-safe via per-type id-sets + KMS resolve index. Subnet-group + IAM role + VPC edges deferred (no `aws:docdb:subnet-group` type yet; mirrors RDS scanner's choice not to model `aws:rds:subnet-group`).

### R2.18 Neptune (this session)
- **Neptune** new types `aws:neptune:cluster`, `aws:neptune:instance`. Although Neptune rides on the shared RDS control-plane API (`rds:DescribeDBClusters` returns `Engine=neptune` rows alongside Aurora/MySQL/Postgres — verified via `rds:CreateDBCluster.Engine` SDK doc valid values list), giving it dedicated `aws:neptune:*` types provides proper semantic typing for auditors and isolates Neptune from RDS-engine resolvers. Regional scanner `scanNeptune` (`aws-sdk-go-v2/service/neptune`) runs two phases sequentially, both `Describe*` paginator-native with full body on List. Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`. NativeID = `DBClusterArn` / `DBInstanceArn` verbatim (Neptune ARNs reuse `arn:aws:rds:` prefix despite the dedicated API — historical artefact, same as DocumentDB). Cluster snapshots, parameter groups, subnet groups, global clusters, Neptune-specific resources (`DescribeDBClusterEndpoints`) deferred — same scope rationale as Redshift / DocumentDB.
- **RDS scanner filter** (`rds_scanners.go`): new `nonRDSEngines` map filters `Engine ∈ {neptune, docdb}` from `scanDBInstances` / `scanDBClusters` to avoid duplicate rows under both `aws:rds:*` and `aws:neptune:*` / `aws:docdb:*` types. The docdb entry is precautionary (in practice `rds:DescribeDBClusters` does not return docdb rows, but the filter is cheap and guards against AWS later choosing to surface docdb via the shared API).
- **Resolvers**. `resolveNeptuneClusterTargets` emits cluster → KMS key (`uses`) via `KmsKeyId` (resolved via `loadKMSResolveIndex`) + cluster → security group (`uses`) per `VpcSecurityGroups[]`. `resolveNeptuneInstanceCluster` wires instance → cluster containment via `BatchAddToHierarchyClosure` keyed on `DBInstanceAttrs.DBClusterIdentifier` matched against scanned clusters' `Name` (= cluster identifier). Mirrors DocumentDB resolver structure exactly (parallel implementations rather than shared helper — Neptune-specific edges may diverge in future iterations).

### R2.18 OpenSearch domains (this session)
- **OpenSearch** (covers legacy Elasticsearch domains via the same SDK package; `aws-sdk-go-v2/service/opensearch`) new type `aws:opensearch:domain`. Regional scanner `scanOpenSearch` runs single-phase: `ListDomainNames` returns name-only entries → fan-out `DescribeDomain` (errgroup + `semaphore.NewWeighted(fanoutMed)`); per-item `AccessDenied` tolerated. ListDomainNames is not paginator-native (single-call) so `_ = skipIfAccessDenied(...); return` on phase-level deny. NativeID = `DomainStatus.ARN` verbatim (`arn:aws:es:{r}:{a}:domain/{name}` — note `es:` legacy prefix retained for OpenSearch). Outbound connections (cross-cluster), package associations, reserved instances deferred — narrow value relative to core domain edges.
- **Resolver** `resolveOpenSearchDomainTargets` emits eight edge kinds per domain: → VPC (`attached-to`) via `VPCOptions.VPCId`, → subnet (`uses`) per `VPCOptions.SubnetIds[]`, → security group (`uses`) per `VPCOptions.SecurityGroupIds[]`, → KMS key (`uses`) via `EncryptionAtRestOptions.KmsKeyId` (resolved via `loadKMSResolveIndex`), → Cognito user-pool (`uses`) via `CognitoOptions.UserPoolId` (ARN reconstructed `arn:aws:cognito-idp:{r}:{a}:userpool/{id}`), → Cognito identity-pool (`uses`) via `CognitoOptions.IdentityPoolId` (ARN reconstructed `arn:aws:cognito-identity:{r}:{a}:identitypool/{id}`), → IAM role (`assumes`) via `CognitoOptions.RoleArn`, → CloudWatch log-group (`uses`) per `LogPublishingOptions[*].CloudWatchLogsLogGroupArn` (per `aws/CLAUDE.md` rule, trailing `:*` SDK suffix stripped before NativeID lookup). FK-safe via per-type id sets + KMS resolve index; cross-account refs and AWS-managed default keys skip silently.

### R2.18 Redshift clusters + subnet groups (this session)
- **Redshift** new types `aws:redshift:cluster`, `aws:redshift:subnet-group`. Regional scanner `scanRedshift` runs two phases sequentially: (1) `DescribeClusters` (paginator-native; full `Cluster` body — VpcId, KmsKeyId, ClusterSubnetGroupName, VpcSecurityGroups[], IamRoles[], ClusterStatus, etc.), (2) `DescribeClusterSubnetGroups` (paginator; full body — VpcId, Subnets[].SubnetIdentifier). Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`. NativeIDs use canonical Redshift ARN shapes (colon-separated like RDS): cluster `arn:aws:redshift:{r}:{a}:cluster:{name}`, subnet-group `arn:aws:redshift:{r}:{a}:subnetgroup:{name}`. Cluster parameter groups, cluster security groups (legacy EC2-Classic, deprecated), HSM client certificates, snapshot copy grants, scheduled actions, and Redshift Serverless workgroups+namespaces (different SDK package) deferred — parameter groups have no graph edges (config artefacts), legacy classic-mode resources rare in modern accounts.
- **Resolvers**. `resolveRedshiftClusterTargets` emits five edge kinds per cluster: → subnet-group (`uses`) via `ClusterSubnetGroupName`, → VPC (`attached-to`) via `VpcId` (with `ec2ARN` shape `arn:aws:ec2:{r}:{a}:vpc/{id}`), → security group (`uses`) per `VpcSecurityGroups[].VpcSecurityGroupId`, → IAM role (`assumes`) per `IamRoles[].IamRoleArn`, → KMS key (`uses`) via `KmsKeyId` resolved through `loadKMSResolveIndex`. `resolveRedshiftSubnetGroupTargets` emits subnet-group → VPC (`attached-to`) + subnet-group → subnet (`contains`) per `Subnets[].SubnetIdentifier`. All FK-safe via per-type id sets + KMS resolve index; cross-account refs and AWS-managed default keys skip silently.

### R2.18 Athena workgroups + data catalogs (this session)
- **Athena** new types `aws:athena:workgroup`, `aws:athena:datacatalog`. Regional scanner `scanAthena` runs two phases sequentially. Phase 1: `ListWorkGroups` (paginator, name-only) → fan-out `GetWorkGroup` (errgroup + `semaphore.NewWeighted(fanoutMed)`); per-item `AccessDenied` tolerated; `out.WorkGroup` (full body — Configuration, ResultConfiguration, EngineVersion, State, etc.) stored as `AttributesJSON`. Phase 2: `ListDataCatalogs` (paginator) → fan-out `GetDataCatalog` (`fanoutMed`); per-item `AccessDenied` AND `InvalidRequestException` tolerated — `ListDataCatalogs` returns the implicit `AwsDataCatalog` (default Glue catalog) but `GetDataCatalog` rejects it as not-found, so the InvalidRequestException tolerance preserves sibling-catalog totals (documented in `aws/CLAUDE.md` "List returns entry, Get rejects it"). NativeIDs reconstructed from bare names: workgroup `arn:aws:athena:{r}:{a}:workgroup/{n}`, data-catalog `arn:aws:athena:{r}:{a}:datacatalog/{n}` (precedent: Cognito user-pool, SES name→ARN reconstruction). Named queries + prepared statements deferred — saved-SQL artefacts, not graph nodes.
- **Resolvers**. `resolveAthenaWorkgroupTargets` emits two edges per workgroup: → S3 bucket (`uses`) via `Configuration.ResultConfiguration.OutputLocation` (`s3://bucket/prefix/` parsed by existing `s3BucketARNFromS3URL`), → KMS key (`uses`) via `ResultConfiguration.EncryptionConfiguration.KmsKey` (resolved through `loadKMSResolveIndex` → `resolveKMSKeyID` per `aws/CLAUDE.md` KMS edge convention; `alias/aws/*` AWS-managed keys auto-skip via the helper). FK-safe via scanned-bucket id-set + KMS resolve index. `resolveAthenaDataCatalogLambda` walks each LAMBDA / HIVE / FEDERATED data-catalog and emits `uses` edges to Lambda function(s) named in `Parameters` map keys `function`, `metadata-function`, `record-function` (HIVE catalogs use `metadata-function`; LAMBDA uses `function` or both metadata + record functions). GLUE-type catalogs skip — they reference the implicit Glue catalog already covered by `aws:glue:database`/`aws:glue:table` containment closure. FK-safe via scanned Lambda id-set; cross-account function refs skip.
- Out of scope this iteration: workgroup → IAM role (`Configuration.ExecutionRole` only set when WorkGroup uses Spark; resolver readily extensible when relevant), workgroup → CloudWatch log group, `CustomerContentEncryptionConfiguration` (separate KMS field for Spark notebooks), `ManagedQueryResultsConfiguration` (newer encryption variant), per-engine-version edge.

### R2.18 Glue Data Catalog — database + table (this session)
- **Glue** new types `aws:glue:database`, `aws:glue:table`. Regional scanner `scanGlue` runs two phases: (1) `GetDatabases` (paginator-native; full `Database` body per entry — Name, CatalogId, LocationUri, FederatedDatabase, TargetDatabase, Parameters), (2) per-database `GetTables` paginator fan-out (errgroup + `semaphore.NewWeighted(fanoutMed)`); per-database `AccessDenied` tolerated. Catalog itself is implicit (one per account+region) and not modeled — `aws:glue:catalog` would be a singleton with no graph value distinct from its containing database/table closure. NativeIDs reconstructed from bare names (List APIs return name-only): database `arn:aws:glue:{region}:{acct}:database/{name}`, table `arn:aws:glue:{region}:{acct}:table/{database}/{name}` (precedent: Cognito user-pool, SES identity/cfg-set name→ARN reconstruction). Tables hierarchy-closure-wired to parent database via `BatchAddToHierarchyClosure` at scan time. Crawlers, jobs, triggers, classifiers, connections, partitions, and column statistics deferred — each adds its own sub-tree of edges (role refs, network refs, classifier chains) that warrant separate iterations.
- **Resolver** `resolveGlueTableS3Location` walks each scanned table and emits `uses` edge to the S3 bucket backing `StorageDescriptor.Location`. Format `s3://bucket[/prefix...]` parsed via new `s3BucketARNFromS3URL` helper (sibling of existing `s3BucketARNFromLocation` which handles `arn:aws:s3:::` form used by Lake Formation). Non-s3:// schemes (HDFS, JDBC, federated catalogs, empty) skip silently. FK-safe via scanned-bucket id set; cross-account bucket refs skip.
- Out of scope this iteration: `AdditionalLocations[]` (Delta-table secondary paths), `FederatedTable` cross-catalog refs, `IsRegisteredWithLakeFormation` flag → Lake Formation resource cross-link, partition-level S3 location refs, view definitions (`ViewOriginalText` SQL parsing). Lake Formation permissions edges (`R2.16` deferred) now unblocked but separately tracked.

### R2.2 Inspector v2 (this session)
- **Inspector v2** new types `aws:inspector2:filter`, `aws:inspector2:member`. Regional scanner `scanInspector2` runs two phases sequentially: (1) `ListFilters` (paginator-native, returns full `Filter` body — Action, Criteria, Arn, Name, OwnerId, timestamps), (2) `ListMembers` (paginator-native, returns full `Member` body — AccountId, DelegatedAdminAccountId, RelationshipStatus). No Describe fan-out needed; List bodies carry edge-bearing fields. Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`. Filter NativeID = `Filter.Arn` verbatim (`arn:aws:inspector2:{region}:{owner-acct}:owner/{owner}/filter/{filter-id}`). Member NativeID synthesized as `arn:aws:inspector2:{region}:{adminAcctID}:member/{memberAcctID}` since List/Get APIs return no per-tuple ARN (precedent: Detective member, KMS grant, EFS mount-target, SSO assignment per `aws/CLAUDE.md` "Synthetic NativeIDs"). Findings (`ListFindings`/`GetFindings`), coverage rows (`ListCoverage` — one row per scanned resource × scan-type, explodes row count), and the singleton org-level configuration (`GetConfiguration`) deliberately out of scope: findings are event data (matches Macie / Detective / Security Hub precedent), coverage is volume-noisy, configuration is a singleton config rather than a graph-edge target.
- **Resolver** `resolveInspector2MemberOrgAccount` walks each scanned member, parses `AccountId` from attrs, and emits `attached-to` edge to the matching `aws:organizations:account` row via `loadOrgTargetIndex`. FK-safe; partial-coverage scans (no Org tree scanned) skip silently. Mirrors Detective + SSO assignment → org-account precedent. Filter `Criteria` resource-ARN refs (EC2 instance ID lists, ECR repo ARN lists, etc. nested under `FilterCriteria` substructs) deferred — narrow value (filters are saved finding queries, not infrastructure relationships).

### R1 Route53 RecordSet → S3 website bucket (this session)
- **Route53** alias resolver extended: when `AliasTarget.DNSName` is recognized as an S3 static-website regional endpoint (legacy `s3-website-{region}` or modern `s3-website.{region}` shapes), the resolver pivots from DNS-keyed lookup to record-name-keyed lookup against scanned S3 buckets. S3 website hosting requires the bucket name to exactly match the record FQDN, so `RecordSet.Name` (lowercased + trailing-dot-stripped via existing `normalizeAliasDNS`) keys directly into a new `buildS3BucketNameIndex` over scanned `aws:s3:bucket` rows. Edge: `uses` (matches the rest of the alias resolver). Bucket NativeID prefix `arn:aws:s3:::` stripped to recover the bare name. FK-safe via the bucket id-set; records pointing at unscanned/cross-account buckets skip silently. New helper `isS3WebsiteEndpoint` recognises both endpoint shapes after normalisation.
- Out of scope: CNAME records to S3 website (no `AliasTarget`, would need a separate non-alias-resolver pass); buckets without website-hosting enabled (would need a `GetBucketWebsite` sidecar fetch — false-positive surface acceptable since AWS only routes the alias when website hosting is enabled, and bucket-name match is highly specific).

### R2.17 SES email identities + configuration sets (this session)
- **SES v2** new types `aws:ses:email-identity`, `aws:ses:configuration-set`. Regional scanner `scanSES` runs two phases sequentially. Phase 1: `ListEmailIdentities` (paginator) → fan-out `GetEmailIdentity` (errgroup + `semaphore.NewWeighted(fanoutMed)`); per-item `AccessDenied` tolerated, `AttributesJSON` stores `GetEmailIdentityOutput` verbatim (DkimAttributes, MailFromAttributes, Policies, VerificationInfo, etc.). Phase 2: `ListConfigurationSets` (paginator, returns bare names) → fan-out `GetConfigurationSet` (`fanoutMed`). Both phases tolerate phase-level `AccessDenied` via `skipIfAccessDenied` without barring sibling. NativeID built via new `sesEmailIdentityARN` (`arn:aws:ses:{region}:{acct}:identity/{name}`) + `sesConfigurationSetARN` (`arn:aws:ses:{region}:{acct}:configuration-set/{name}`) — SES v2 List/Get APIs return bare names; ARN reconstruction needed for cross-resource edges (matches Cognito user-pool precedent). Tags on both types deferred — `sesv2types.Tag` not in the existing `awsTagsJSON` generic union and adding it touches the global type list; defer to a tags-cleanup pass.
- **Resolver** `resolveSESEmailIdentityConfigSet` emits `uses` edge from each scanned email identity to its default configuration set (`ConfigurationSetName` field on `GetEmailIdentityOutput`), FK-safe via `resourceIDSet` over `TypeSESConfigurationSet`. Config sets are region-scoped and SES enforces same-region for default-cfg refs, so resolver pins the cfg lookup to the identity's own region. Out of scope: configuration-set event destinations (sub-resource — would either need a third type `aws:ses:configuration-set-event-destination` or per-cfg `GetConfigurationSetEventDestinations` fan-out + embedded array, plus per-destination ARN walker for SNS/Firehose/Kinesis Data Streams targets); identity → SNS topic (v1 `GetIdentityNotificationAttributes` only; v2 routes notifications via cfg-set event destinations); Pinpoint apps (v2 `pinpoint` SDK service is being deprecated, separately tracked).

### R2.16 Lake Formation (this session)
- **Lake Formation** new type `aws:lakeformation:resource`. Regional scanner `scanLakeFormation` runs a single phase: `ListResources` (paginator-native) returns the full `ResourceInfo` body per registered data location (RoleArn, ResourceArn, federation/hybrid-access flags, verification status) — no Describe fan-out needed. NativeID = `ResourceInfo.ResourceArn` verbatim (`arn:aws:s3:::bucket[/prefix]`). Per-region `AccessDenied` tolerated via `skipIfAccessDenied`. Permissions (`ListPermissions`), LF-tags (`ListLFTags`), data-cells filters, and LF-tag expressions deferred until the Glue catalog (R2.18) lands — without scanned `aws:glue:database`/`aws:glue:table` rows, the bulk of permissions edges would dangle.
- **Resolver** `resolveLakeFormationResourceTargets` emits two edges per registered location: → S3 bucket (`uses`) via new `s3BucketARNFromLocation` helper that strips the `/prefix...` suffix from the location ARN to recover the canonical bucket NativeID `arn:aws:s3:::bucket`; → IAM role (`assumes`) via `RoleArn` (the service role registered to access the location). FK-safe via `resourceIDSet` over `TypeS3Bucket` + `TypeIAMRole`; cross-account refs (foreign bucket / foreign role) skip silently. Out of scope: `WithFederation` / `HybridAccessEnabled` semantics in edge attrs (flag-only, no extra target), `ExpectedResourceOwnerAccount` cross-account hint.

### R2.13 Detective (this session)
- **Detective** new types `aws:detective:graph`, `aws:detective:member`. Regional scanner `scanDetective` runs two phases: (1) `ListGraphs` (paginator) — graph NativeID = `Graph.Arn` verbatim; (2) per-graph `ListMembers` paginator fan-out (errgroup + `semaphore.NewWeighted(fanoutMed)`). `MemberDetails[]` ships full body on List — no Describe fan-out. Member NativeID synthesized as `{graphArn}/member/{accountId}` (no AWS-issued ARN; documented in aws/CLAUDE.md "Synthetic NativeIDs"). Per-phase `AccessDenied` tolerated via `skipIfAccessDenied`; non-administrator accounts get an empty graph list and short-circuit at phase 1 (no service-disabled sentinel needed — Detective does not surface a distinct not-enabled exception). `BatchAddToHierarchyClosure` wires graph→member containment at scan time.
- **Resolver** `resolveDetectiveMemberOrgAccount` walks each scanned member, parses `AccountId` from attrs, and emits `attached-to` edge to the matching `aws:organizations:account` row via `loadOrgTargetIndex` (same precedent as SSO `resolveSSOAccountAssignments`). FK-safe via the org index map; partial-coverage scans (no Org tree) skip silently.
- Out of scope: graph → GuardDuty detector (Detective backs onto GuardDuty findings but exposes no per-graph detector ARN); admin/master-account dedicated edge (derivable from `MasterId`/`AdministratorId` on members); cross-region member-status reconciliation; investigations + behavior-graph queries (event data, not resources).

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
- *(removed — Route53 → S3-website resolver landed via record-name pivot when alias DNS matches `s3-website-{r}` / `s3-website.{r}` endpoints; see COMPLETED R1 Route53 RecordSet → S3 website bucket)*
- *(removed — IAM policy doc → KMS/S3/Secrets/DynamoDB landed; see COMPLETED R1.5)*

### R2. New AWS scanners — highest graph/security value
Order by edges-opened vs scanner effort.
1. *(removed — delegated-admin edges landed; no outstanding org-scanner work)*
2. *(removed — Inspector v2 filter + member scanners landed with member→org-account resolver; findings/coverage/configuration deferred per event-data precedent; see COMPLETED R2.2)*
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
13. *(removed — Detective scanner + member→org-account resolver landed; see COMPLETED R2.13)*
14. **Governance surface** — Artifact (deferred — S3-backed compliance reports, narrow graph value). *(Service Catalog + Audit Manager + Control Tower landed; enabled-controls within Control Tower + sub-resources within each deferred — see COMPLETED R2.14 entries.)*
15. *(removed — entire R2.15 compute group closed: App Runner, Batch, Lightsail, Elastic Beanstalk all landed. Sub-resources deferred per service; see COMPLETED R2.15 entries.)*
16. *(removed — Lake Formation resource scanner + S3/IAM resolvers landed; permissions/LF-tags deferred until Glue catalog scanner lands; see COMPLETED R2.16)*
17. *(removed — SES v2 email-identity + configuration-set scanners landed with default-cfg resolver; event-destination sub-resources + Pinpoint deferred; see COMPLETED R2.17)*
18. *(removed — entire R2.18 data-services group closed: Glue (db+table), Athena (workgroups+data-catalogs), Redshift (cluster+subnet-group), OpenSearch (domain) landed this session; Neptune + DocumentDB audit-moved (already covered by RDS scanner via shared control-plane API). Sub-resources deferred: Glue crawlers/jobs/triggers/classifiers/connections, Athena named-queries/prepared-statements, Redshift parameter-groups/snapshots/Serverless, OpenSearch outbound-connections/packages. See COMPLETED R2.18 entries.)*

Deferred within services already landed:
- **SSM document** content → referenced IAM role (needs YAML/JSON doc-content parser).
- **Backup selection** → tagged resources (needs tag-condition-expression expansion against resources table).
- **GuardDuty detector** → member accounts (cross-account edges beyond current provider scope).

### R3. Azure scanner expansion
Current Azure: AKS, AppService, Compute (VMs/VMSS/Disks/Galleries/Dedicated/CloudServices/Infra), KeyVault, Network, ResourceGroups, SQL (Database/Server + children + Managed), Storage.

**Add, priority order:**
1. **Entra ID** (formerly Azure AD) — users, groups, app registrations, service principals. Edges to RBAC assignments = graph payoff.
2. *(removed — RBAC role assignments + role definitions scanner + assignment→def / assignment→scope resolvers landed; principal edges deferred to R3.1 Entra ID; see COMPLETED R3.2)*
3. *(removed — user-assigned MSI scanner + assignment→MSI (via principalId match) + consumer→MSI (via identity.userAssignedIdentities map) resolvers landed; system-assigned MSIs intentionally left as host attributes; see COMPLETED R3.3)*
4. *(removed — Log Analytics workspace scanner landed; solutions/DCRs/diagnostic-settings resolver deferred — see COMPLETED R3.4)*
5. **Azure Policy** — assignments, definitions, exemptions. Edges: assignment → scope, → role (for DINE/deployIfNotExists).
6. **Defender for Cloud** — pricing tiers, recommendations, assessments per subscription.
7. *(removed — ACR registry scanner + registry→KeyVault CMEK resolver landed; replications/webhooks/AKS-pull deferred — see COMPLETED R3.7)*
8. **Container Apps / Container Instances** — edges: → VNet, → ACR, → Log Analytics.
9. *(removed — Cosmos DB account scanner + account→KeyVault CMEK resolver landed; databases/containers + private-endpoint edges deferred — see COMPLETED R3.9)*
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