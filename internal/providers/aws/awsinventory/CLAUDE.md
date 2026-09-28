# CLAUDE.md — `internal/providers/aws/awsinventory/`

AWS SDK inventory: the extractor that derives the AWS coverage denominator from the pinned SDK source, its refs, its pairing resolver and its pins. Core contract and pairing walk: `internal/sdkinv/CLAUDE.md`.

## Cache layout

- `aws@<tag>/repo/codegen/sdk-codegen/aws-models/*.json` (Smithy models) +
  `service-reference/index.json` and `<service>.json`.

## Pins (`pins.go`)

- `SDKRef` = aws-sdk-go-v2 `release-YYYY-MM-DD` tag.
- The Service Reference catalog is unversioned and served live, so its pin is content:
  `ServiceReferenceDigest` = first 12 hex of `sha256(index.json)`, printed as
  `service-reference@<digest>`. A fetch that disagrees is **reported, never enforced**
  (extractor diagnostic + `coverage sdk status` stderr note) — the served catalog is whatever it
  is. The catalog moves most days: bump the digest in the same commit as the regenerated
  baseline. `make check-coverage` treats a moved pin as growth to regenerate, not a regression.

## Catalog facts

- Smithy service shape: `aws.auth#sigv4.name` equals the Service Reference service name (join key);
  `aws.api#service` carries `sdkId`/`arnNamespace`/`cloudFormationName`. Ops carry
  `smithy.api#required` on input members and `smithy.api#paginated`. Resource shapes are rare
  (ec2/s3/rds have none) — never rely on them.
- Service Reference doc: `Actions[].Annotations.Properties.{IsList,IsWrite}`, `Actions[].Resources[]`
  (targets), `Operations[].AuthorizedActions` (SDK op → IAM action, e.g. `ListObjectsV2`→`ListBucket`),
  `Resources[].ARNFormats`.

## Refs (`refs.go`)

- Smithy output → collection element; a detail read has no collection, so its elements
    are **every** structure member of the output, and `flatRefs` additionally takes the output's
    own id-like primitives — `GetEnvironment` answers with `vpcId`/`subnetIds`/`loadBalancerArn`
    beside a `storageConfigurations` list, and walking only the structures loses all three. A ref must
    target a `string`: enum, integer and long targets are never refs (`State.Name`), and
    `tokenNameRe` drops `*Token`/`*ETag`/`*RequestId`/`*RevisionId` names. Own id = bare
    `Arn/Id/Name` or a suffix-of-noun stem at depth 0, **plus an exact-noun stem at every depth**
    (`DescribeInstances`' element is `Reservation`, so `Instances.InstanceId` arrives at depth 1).
    Never extend the loose suffix rule below depth 0: it matches genuine cross-resource refs.

## Extractor (`extract.go`)

- Join key = Smithy `aws.auth#sigv4.name`; shared signing names (rds/neptune/docdb, s3/s3control,
  apigateway/apigatewayv2) land ops from several models on one key — `Operation.Module` tells them
  apart; pairing disambiguates by import.
- Service Reference `Operations[].AuthorizedActions` binds an op to an action only by same name
  or same verb (`ListObjectsV2`→`ListBucket`). A generic action (apigateway's `GET`) binds nothing,
  so the Smithy shape decides `IsList` (signal `fallback`). Some services are absent from the
  catalog entirely (cloudwatch=`monitoring`, tagging, sso portal, partner central…).
- SR `IsList` is incomplete (backup-gateway `ListGateways`, batch `DescribeComputeEnvironments`
  are false): a List/Describe op with a collection output is a lister unless the bound action
  `IsWrite` (signal `shape-list`). Trusting SR alone left scanner types unpaired.
- Candidates: IsList ops, plus non-list Get/Describe/Head with no collection output (attribute).
  Writes, actions and batch reads are never candidates.
- Lineage: catalog targets count only when the op has a required input (a lister with none is
  top-level — `DescribeDBInstances` targets `db` but is depth 0). The subject's own ARN is
  authoritative: depth = its id variables − 1, parent = the variable before the last
  (`object` → `${BucketName}/${ObjectName}` → depth 1, parent bucket). Otherwise the deepest
  target is the parent. Without targets, required members whose stem matches a catalogued
  resource's own id (`Bucket`, `VolumeId`) or look id-like name the ancestors (`required-id`).
  - An ARN variable is scope, not a level, when its name is or ends in partition/region/account
    (`${AwsAccountId}`) — **except the last**, which is the subject's own id whatever it is
    called (organizations' account resource ends `${AccountId}`). `scopeParams` is keyed
    lower-case for the same reason: three spellings of the account member put QuickSight rows
    under a phantom `quicksight/awsaccount`.
  - A bare `job` stem with no catalogued `job` resource is an asynchronous handle, not a parent
    (`job-handle` signal): Rekognition's `JobId` names a Start… call. `JobRun`/`JobQueue` are
    real resources and keep their parentage.
  - One id stem can name several resources (glue `Job`/`JobRun`); `stemResource` keeps them all
    and picks by the operation's own noun, then the shortest name — the last read used to win.
  - `resolveTree` runs after every entry exists, because indexing sees one operation at a time.
    An entry any operation lists top-level stays depth 0; otherwise, among the proposals of its
    **shallowest** lineage, one that is itself an entry beats one that is not, deepest first, and
    an entry is never its own parent. Depth is then the resolved parent's depth + 1, memoised
    and cycle-guarded — ARN variable counting made `stack/${StackName}/${Id}` two levels and left
    candidates whose depth was not their parent's plus one. Do not deepen an entry from a
    proposal of a deeper lineage: `route53/hostedzone` and `lambda/function` are listed both
    ways, and the shallow listing is the truth.
- Admission evidence: a member is id-like by the PascalCase suffix **or** by the
  whole word in any case (`id`, `arn`, `name` — AppSync, DataZone, Bedrock, EKS, Grafana, Lex and
  Cognito model them lower-case, and child collections were demoted as id-less). A list of
  primitives counts as a collection when its name is id-like or stems to the op's noun
  (`sqs:ListQueues` answers `QueueUrls []string`, and `aws:sqs:queue` was in neither the
  numerator nor the denominator). A payload wrapping its collection one level down
  (`GetApps` → `ApplicationsResponse.Item[]`) counts only when the op noun is plural or the inner
  collection is that noun — a blind descent turns every single-structure read output into a
  listing (`wrapped-list` signal).
- A service the catalog files under another name (`cloudwatch`→`monitoring`, `cloudcontrol`→
  `cloudformation`, the IoT data planes) joins by **operation-name containment**: the smallest
  document that authorises every operation of the model, recorded as `sr:document=<name>`.
  Without it CloudWatch's dashboards, insight rules, mute rules and anomaly detectors were
  `catalog` and excluded although disco stores all of them.
- Ballast rules. `writeNoun` fills from **lifecycle** verbs only
  (Create/Delete/Put/Add/Import/Provision/Allocate/Register/Associate/Attach/Copy/Restore/
  Launch/Run/Publish); a setting verb (Update, Modify, Enable, Set, Export, Start) records a
  `mutable` signal instead, because `ModifyIdFormat` does not make `ec2/idformat` a resource a
  scanner could ever close. The cost is measured and accepted: some rows disco does store (`ssm/instanceinformation`, `iot/v2loggingoption`,
  `lakeformation/permission`, `shield/emergencycontactsetting`,
  `securityhub/configurationpolicyassociation` and two ec2 options) are now `excluded`; they keep
  their `discoType` and their `scanner-lists` signal, so the evidence is visible, not lost.
- `tag` is a **non-subject** like `resource`/`target` in `selfStems`: an op whose noun is `tag`
  keys no candidate and ships as an `Other` op (`route53/tag` was a covered row attributed to
  `aws:route53:cidr-collection`). `ec2/tag` is therefore no longer a candidate — the old
  catalog example.
- Class: cross-cutting counts only the targets that are neither the subject nor an ancestor its
  lineage names (`identitystore:ListGroupMemberships` is authorised against the
  membership, its group and the store); the SR-resource test runs **before** it. A catalog
  resource whose ARN namespace is unrelated to the model's own is not this service's
  (`ec2/group` carries a `resource-groups` ARN); a namespace that is a prefix of the model's, or
  vice versa, is the same family under a longer name (route53-recovery-control-config's safety
  rules). Order after that: SR resource with an owned ARN → resource; cross-cutting → attribute;
  a single-subject read the catalog does not call a listing, over an already singular noun →
  attribute (`single-subject-read`, sub-state such as `lambda/functionconfiguration`); child with
  id-bearing collection → resource (`child-uncatalogued`, e.g. `kms/grant`); child without ids →
  attribute; noun with a lifecycle `IsWrite` action → resource (`writable-noun`); no collection →
  non-resource; an element carrying its own ARN or a creation timestamp → resource
  (`element-arn` / `element-created`, the evidence that beats the catalog fallback for a service
  the catalog does not carry); else catalog (`ec2/instancetype`, `ec2/accountattribute`).
  The `resCanon` lookup retries with the descriptor suffix stripped
  (`ListClusterSummaries` lists clusters), and an `Associate`/`Attach`/`Register` write stamps
  `<noun>association` and `<noun>attachment` as write nouns, because that is the noun the lister
  spells (`AssociateResolverRule` → `resolverruleassociation`).
- Folding: a noun that is the service's own name plus an existing candidate's
  noun folds into that candidate under a `legacy-noun` signal — `elasticsearch-service.json` and
  `opensearch.json` both sign as `es`, so `es/elasticsearchdomain` was a permanently uncovered
  duplicate of the covered `es/domain`. Only a **service word** strips (the join key, the ARN
  namespace, the endpoint prefix, the `sdkId`'s words), and never one that names a resource of
  the service itself (`connect/contact`, `bedrock/agent`) — stripping any shared prefix would
  collide `lambda/functionurlconfig` with unrelated candidates. A candidate whose ops come from
  several model files carries `multi-module:<files>`: sibling models share a signing name
  (docdb, neptune and rds all sign as `rds`), and `lex/bot` is covered partly by Lex Classic ops
  the v2 scanner never calls. Splitting on `endpointPrefix` only separates some of them, so the
  row says so instead.
- Identity vs display: entries merge on `Ident` (`svc/<ident>`, attributes `svc/<parent
  ident>/<ident>`); `assemble` renders keys last. `entry.display()` = the one spelling
  singularised, else the spelling another spelling singularises to (`analysis` over
  `analyses`), else the shortest; the catalog's own name joins the spellings (`resCanon` keeps
  the shortest catalog name per identity — SR lists `RestApi` and `RestApis`). `Parent` is
  the parent entry's display when one exists, else the lineage's spelling; a parent may
  name no candidate (an id member with no lister) and that is accepted.
- Determinism: `entry.place` folds lineages by shallowest depth, then smallest parent ident,
  then shortest parent display (`GetLink` says `gateway`, `ListLinks` says
  `respondergateway`, both ident `gateway`); `assemble` folds in sorted id order. Verify with
  four `disco coverage services -o json` runs hashed — the conformance `DeepEqual` only sees
  the fixture.
- `mergeDetailReads` folds `<svc>/<parent>/<noun>` attributes into an existing `<svc>/<noun>`
  resource (GetBasePathMapping's `BasePath` is not id-like); it carries the detail read's **noun**
  too, because that spelling is usually the singular the key should show (`GetAlias` beside
  `ListAliases`).
- A noun missing from `resCanon` is retried as `Ident(parent+noun)` **before** the key and the
  class are fixed: `ListVersionsByFunction` says `versions` where the catalog says "function
  version", and a sibling op spelling `FunctionVersions` keyed the same object a second time.
- An op whose noun is empty (`sagemaker:Search`) goes to `Universe.Other`: its key would end in
  `/` and match nothing. `conformance.Check` rejects any empty key segment.
- `Operation.Scope` is left **empty** for AWS. Nothing in a Smithy model separates a regional
  listing from an account-wide one (iam and ec2 both declare a `Region` endpoint parameter), and
  stamping every op `account` made the column say nothing.
- Scope params (`AccountId`, `Region`, paging members) never denote a parent.
- SR structs use camelCase tags: `encoding/json` matches keys case-insensitively, so the
  PascalCase catalog decodes without a tagliatelle exclusion.

## Pairing resolver (`resolver.go`)

- AWS: anchors are `pkg.New<Op>Paginator(`, `pkg.<Op>Input{` and `recv.<Op>(` for any op an
  imported service package ships; receivers need no binding. `LabelAliases` accepts the
  separator-stripped service (`accessanalyzer:` for `access-analyzer`).
