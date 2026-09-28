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
  `smithy.api#required` on input members and `smithy.api#paginated` (a service-level default
  merges under each op's own). Resource shapes cover 123 of 431 models (735 shapes; ec2/s3/rds
  have none): authoritative where present, never assumed.
- Service Reference doc: `Actions[].Annotations.Properties.{IsList,IsWrite,IsTaggingOnly}`, `Actions[].Resources[]`
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

## Extractor (`extract.go`, `model.go`, `catalog.go`)

Model-first. Each op's facts come from the model (resource bindings, `paginated`, `readonly`,
`http`, `iamAction`) and the Service Reference; where neither states the fact, a **structural**
rule decides and tags the candidate (`Rule`/`Signals`). There are **no verb, preposition or noun
word lists** — do not add one. Surviving name rules are listed under "Structural fallbacks".

- **Closure.** `newServiceModel` walks `service.operations`/`resources` recursively; an op shape
  the closure never reaches is `Dropped` as `unreachable-from-service` (healthlake ships a second
  namespace the Go client lacks); a model with no service name drops as `no-service-name`.
  `SourceOps` = every op shape, so the accounting invariant holds. Iterate shapes and lifecycle
  roles in a fixed order: a map-order pick changes the output between runs.
- **Resource tree.** `nestByIdentifiers` places a top-level resource under the one whose
  identifiers are the largest strict subset of its own (lambda declares FunctionAlias beside
  Function). The parent's ids must all be named (a bare `id` says nothing); a tie or an id-less
  parent nests nothing; the model's own nesting always stands. `keepResourceSuffix` keeps
  "Resource" when an op spells it (`ListManagedResources`). A lineage from a resource's own lifecycle is `declared` and
  outranks every op-inferred placement (`entry.place`): deadline's SearchWorkers hangs off Farm, but
  Worker sits under Fleet. `rebind` fixes loose bindings: an instance read named after its resource
  is its `read`; a collection op requiring all its own ids is an instance op.
- **SR join** (`joinSR`): signing name, then SDK client name (lowercased sdkId), then op-name
  containment (`sr:document=<name>`, e.g. cloudwatch→monitoring). Diagnostics name the fallback.
- **Action binding** (`bindAction`): `aws.iam#iamAction` first (`sr:iam-action`), else the union of
  own-service authorising actions — a listing if **any** lists, a write if **any** writes,
  tagging-only only if all are. Else an SR action named as the op (`sr:action-name`). Nothing
  bound → `sr:unbound`.
- **Lister ladder** (`lister`, signal `list:<rung>`): resource `list` binding; SR `IsList` without
  `IsWrite` (workspaces' `CreateStandbyWorkspaces` is both); paginated `items` on a read; paginated
  read with any collection (servicediscovery `ListInstances` names no items); read over a written
  collection; read over an id-named primitive list (`idList`); read over a collection taking no
  input (ses `ListReceiptFilters`); no catalog action and a list-shaped output that is
  read-traited, paged, or untraited.
- **Key**: bound resource name, else the op noun (after the first camel word, `V\d+` dropped),
  retried as `parent+noun` against the catalog (`ListVersionsByFunction`). A unique uncatalogued
  noun drops leading model op first-words until it names another op's noun or a catalog resource
  (`key:verb-prefix`: `BatchGetFarms` → `farm`); ~13 BatchGet keys with no match stay (uncovered).
  `keyByShape` folds ops over one element shape, never a tag shape and never an op whose own ident
  is catalogued (that merged distinct resources); election prefers the bound resource, then a
  catalogued ident, then the shorter. The SR action's resource is **not** a key: `DescribeLogStreams` is authorised against
  the log group.
- **Lineage** (`lineage`): resource node → URI labels (`/fleets/{fleetId}`; static segment names
  the parent) → SR ARN levels (`arnLevels`, positional `SplitN(":", 6)`) → required id-like stems
  (weak; a weak parent naming neither an entry nor a catalog resource is a handle, dropped). A
  required id whose stem ends the noun is the subject's own, not a parent (`FarmId` on a farm).
- **Class** (`classify`), in order: not a lister → attribute `detail-read`; bound → `smithy-resource`;
  SR resource in an owned ARN namespace → `sr-resource`; ≥3 foreign targets → `cross-cutting`;
  unpaged structural listing of its own subject → `single-subject-read`; tag element →
  `tagging`; depth > 0 → `child-uncatalogued` if the element carries an id, else
  `id-less-collection`; no collection → non-resource; element written → `element-written`;
  element ARN → `element-arn`; creation timestamp → `element-created`; else catalog `read-only`.
- **Written** (`writtenBy`/`owns`), from ops known to mutate (`writeish`: SR `IsWrite`, else
  traited and not read — an untraited op with no catalog action is unknown, never evidence). An
  element is written when a write takes/answers its shape, answers an id named after the write's
  own noun (`AllocateHosts`→`HostIds`; a stem from any other id is a reference), or shares **three**
  fields with it (one level deep; the lister's own inputs, generic ids and other resources' ids
  excluded). Two fields admitted `ec2/availabilityzone` via `ModifyAvailabilityZoneGroup`. Only
  structure lists count (a primitive list is ids, not the element), and a tagging-only write
  contributes tag shapes, never written ones.
- **Tagging** (`isTag`/`tagged`): an element carrying every field of a structure (≥2 fields) a
  tagging-only write takes (`Tag{Key,Value}`) reads tags. From the catalog's `IsTaggingOnly`, not
  the word "tag". Tag shapes are excluded everywhere an element decides identity (`owns`,
  `keyByShape`, the index element pick) or `iam/role` absorbs `ListRoleTags`.
- **Admission** (`entry.admit`): a later op replaces an entry's class only with a stronger rule
  (`ruleRank`: smithy > sr > element-written > element-arn > element-created > child).
- **Detail fold** (`mergeDetailReads`): attribute `svc/P/N` folds into non-attribute `svc/N`.
- An attribute whose parent `resolveTree` dropped renders as `svc/N`, never `svc//N`.

### Structural fallbacks (the only name rules; each justified)

- `cutQualifier` (`noun:qualifier-cut`): `TagsForResource` given input `ResourceArn` → `Tags`. The
  joiner is found by **shape** — a capitalised word of ≤3 letters with a lowercase tail, leftmost,
  whose remainder an input member's stem ends with (or ends). Never cut when that stem starts with
  the subject: `ClientVpnEndpoints` given `ClientVpnEndpointId` is a name, not a qualifier.
  `camelWords` keeps a plural acronym whole (`HITsForQualificationType` → `HITs`).
- `idLikeRe`/`idWordRe`: SDK member-naming grammar for identifiers (`Id`, `Arn`, `Name`,
  `Identifier`); lowercase whole words for AppSync/DataZone/Bedrock-style models.
- `arnMemberRe`: an element member ending `Arn` — ARN is AWS's resource-name grammar.
- `createdRe`: an element member recording creation — catalog rows are published, not created.
- `tokenNameRe` (refs): idempotency/concurrency tokens are never refs.
- `scopeParams`: paging and account/region members never identify.
- Primitive lists count as collections when named as ids or after the noun (sqs `QueueUrls`);
  maps of primitives are key/value pairs, never elements.

### Measured cost of dropping word lists (2026-09-28, release-2026-09-15)

49.76% vs 48.74% covered (5399 candidates, 3336 resources). 20 scanned types fell to
catalog/attribute (e.g. `cloudfront/cloudfrontoriginaccessidentity`, `ses/emailidentity`,
`workspaces/ipgroup`, `lightsail/bucket`, `securityhub/securitycontrol`) and 10 were gained. The
old verb lists admitted them; no structural fact does.

- Identity vs display, `resolveTree`, `assemble` and determinism behave as before: entries merge on
  `Ident`; verify determinism with four hashed `disco coverage services -o json` runs.
- `Operation.Scope` stays **empty** for AWS: nothing in a model separates regional from
  account-wide listings.
- SR structs use camelCase tags: `encoding/json` matches keys case-insensitively.

## Pairing resolver (`resolver.go`)

- AWS: anchors are `pkg.New<Op>Paginator(`, `pkg.<Op>Input{` and `recv.<Op>(` for any op an
  imported service package ships; receivers need no binding. `LabelAliases` accepts the
  separator-stripped service (`accessanalyzer:` for `access-analyzer`) and the model's own name
  (`docdb:` for an op signing as `rds`). `OpKey` = the model file name minus hyphens, which the
  generator derives from sdkId exactly as it names the Go package — `TestModelFileIsSDKPackage`
  holds the two together.
