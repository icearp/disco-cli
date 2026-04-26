# CLAUDE.md — `internal/providers/aws/`

AWS-specific scanner + resolver conventions. Cross-provider rules: see `../CLAUDE.md`.

## Resolver conventions

- **Scanner attribute JSON uses PascalCase keys.** `mustJSON` calls `json.Marshal` on AWS SDK v2 response structs, no json tags — `ClusterArn` stays `ClusterArn`, not `clusterArn`. Resolver structs need PascalCase tags (`json:"ClusterArn"`) or silent match nothing on real scan data while tests pass on hand-rolled JSON.
- **ARN helpers**: `ec2ARN(region, acct, kind, id)` → `arn:aws:ec2:{r}:{a}:{kind}/{id}` (slash sep). `rdsARN(region, acct, kind, id)` → `arn:aws:rds:{r}:{a}:{kind}:{id}` (colon sep, RDS-specific). Resolvers rebuild target ARN from native ID, pass to `store.ResourceID(...)`. Wrong shape = phantom target, buried FK error.
- **KMS edges**: skip empty `KmsKeyId` / `KMSKeyArn`. AWS-managed default keys unscanned, edge = dangling target. `if sv(attrs.KmsKeyId) == "" { continue }`.
- **`logGroupNativeIDFromName(accountID, region, name)`** — in `logs_scanners.go`, callable from any file in `package aws`. Rebuilds `arn:aws:logs:{r}:{a}:log-group:{name}`. Use instead of `fmt.Sprintf` for NativeID shape consistency with scanner.
- **CloudWatch Logs ARN `:*` suffix**: SDK returns `CloudWatchLogsLogGroupArn` with trailing `:*`. Strip via `strings.TrimSuffix(arn, ":*")` before NativeID lookup or edge points to phantom resource.
- **EFS mount target NativeID**: no native ARN. Synthesize: `arn:aws:elasticfilesystem:{region}:{acct}:file-system/{fsid}/mount-target/{mtid}` using `FileSystemId` + `MountTargetId` from `DescribeMountTargets` response.
- **KMS grant NativeID**: `ListGrants` returns no `GrantArn` — `GrantListEntry` only has `GrantId`. Synthesize `{keyARN}/grant/{grantId}`. No real `arn:aws:kms:...:grant/...` ARN exists; pattern-matchers keyed on AWS-issued ARNs skip.
- **AWS Backup plan ARN** uses `backup-plan:`, not `plan:`. Real: `arn:aws:backup:{r}:{a}:backup-plan:{planId}`. Synthetic selection NativeID `{planARN}/selection/{selId}` — trim `/selection/...` in resolver to recover parent plan ARN. Wrong prefix → FK error on closure insert.
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
- **Federated-provider ARN dispatch**: `:saml-provider/` → `TypeIAMSAMLProvider`; `:oidc-provider/` → `TypeIAMOIDCProvider`. Other Federated shapes emit no edges (skip, no dangle).

## WAFv2 scope pattern

WAFv2 two scopes: `REGIONAL` (per-region) + `CLOUDFRONT` (global). CLOUDFRONT scope reachable only from `us-east-1` — other regions error. Guard with `if region == "us-east-1"` before CLOUDFRONT-scope calls to dodge duplicates.

## FK-safe edge emit when target partially scanned

Resolver targets type with unscanned members (public/Marketplace AMIs, cross-account ARNs, shared snapshots) — build target id set once via `ListResources(Types: []string{TargetType})`, emit edge only if computed target id present. Prevents FK blowup on `UpsertRelationship` + phantom edges. Precedent: `keyPairByNameRegion`, `imageByID` in `ec2_compute_mgmt_resolvers.go`.

## Ownership-filtered AWS scanners

AWS Describe* with ownership filter (`Owners=["self"]` for AMIs/snapshots/FPGA images) — scan self-owned only. Public/Marketplace/shared sets unbounded + not ours to audit. Cross-account refs from scanned resources (instance → public AMI) handled via FK-safe lookup above.

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

`isAPIErrorCode(err, codes ...string) bool` in `aws.go` = single choke point (wraps `errors.As` + `smithy.APIError.ErrorCode()` + `slices.Contains`). Use inline for one-off checks. Wrap in named helper only when reused 3+ times (precedent: `isAccessDenied` wraps 6 codes, 146 callers). Predicates needing code + message-substring match (e.g. `isCacheSecurityGroupsNotPermitted`) stay one-off — outside this helper's shape.

## `skipIfAccessDenied` always returns nil

Single-phase scanners `return 0, 0, skipIfAccessDenied(...)`. Multi-phase scanners (e.g. `scanEventBridge` running buses + rules + connections + api-destinations in one call) cannot early-return — use `_ = skipIfAccessDenied(...); break` to skip denied phase while preserving totals from prior phases. Precedent: `scanEventBridge` phases 3/4.

## Transient errors already wrapped at dispatch

`aws.go` `scanRegion` + phase-1a wrap each `svc.fn` return with `isTransientNetworkError` → `skipIfTransient` (warn + nil). Scanners do NOT need inline handling for DNS (`net.DNSError`), dial/read/write (`net.OpError`), `net.Error` timeouts, or Smithy `RequestTimeout`/`ServiceUnavailable`/`InternalFailure` variants — those warn-skip automatically. Only `AccessDenied` still needs per-scanner `return 0, 0, skipIfAccessDenied(...)` (not wrapped at dispatch because SDK surfaces it mid-paginator, not as top-level svc.fn error).

## ROADMAP vs source drift

Before implementing `ROADMAP.md` NOW item, grep for target `Type*` constants + resolver fn names — items get implemented without roadmap sweep (R1.3 Lambda layers + R1.4 SQS KMS both listed TODO while already shipped). Audit first; move DONE items into COMPLETED section same pass.

## KMS key edge — use `loadKMSResolveIndex` + `resolveKMSKeyID`

Resolver sees KMS ref in four shapes: full key ARN, alias ARN, `alias/foo`, bare key UUID. Build index once per resolver via `loadKMSResolveIndex(acct, st)` (`kms_helpers.go`), then call `idx.resolveKMSKeyID(ref, region, acctID)` per edge — returns `(keyID, ok)` where `ok=false` means target wasn't scanned (skip emit, no FK error). Index also resolves alias name → underlying key ARN, so `alias/aws/foo` references now link to the AWS-managed key (which IS scanned — see kms scanner). Don't manually call `kmsKeyTargetARN` + build a key ID set + check `alias/aws/` — the helper does all three. Precedent: backup, rds, sns, sqs, kinesis, firehose, ssm, config, s3-encryption, kafka, cloudtrail-eds resolvers.
