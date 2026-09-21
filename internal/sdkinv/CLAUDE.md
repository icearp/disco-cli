# CLAUDE.md — `internal/sdkinv/`

SDK source cache + extractors that derive the coverage denominator. Imports nothing from
`internal/providers` or `internal/coverage`. Extractors register from `init()`; blank imports
live in `internal/sdkinv/all` (no slim build tags — extractors link no cloud SDK).

## Cache layout (`$XDG_CACHE_HOME/disco/sdk/<provider>@<ref>/`, `disco coverage sdk fetch|status`, `make sdk-fetch`)

- `manifest.json` written last; a dir without it is "absent". Fetch lands in `.tmp-*` then renames.
- A snapshot's identity is provider + ref + `SpecFingerprint(FetchSpec)` (manifest `spec`).
  `Cache.Status(e Extractor)` returns `ErrNotFetched` on any mismatch, so a wrong-identity or
  wrong-spec directory refetches instead of being read. **Changing a source's `Keep` or `Expand`
  MUST bump its `KeepID`/`ExpandID`** (funcs cannot be hashed; the `all` guard test only checks
  the id is non-empty). Widening `keepARMFile` at an unchanged `AzureSDKRef` once left every
  cache holding the old narrower file set, silently reporting Azure 19.52% instead of 19.70%.
- `aws@<tag>/repo/codegen/sdk-codegen/aws-models/*.json` (431 Smithy models) +
  `service-reference/index.json` and `<service>.json` (456 docs).
- `azure@<sha>/repo/sdk/resourcemanager/<rp>/arm<rp>/{*_client.go,models.go,response_types.go,responses.go}`.
  Filter anchors on `sdk/resourcemanager/` — the monorepo also ships `profile/*/resourcemanager` copies.
  Monorepo HEAD holds one major per module dir (majors are tags), so no version picking.
- `gcp@<ver>/api/<api>/<ver>/<api>-api.json` (654 Discovery docs), copied from GOMODCACHE when
  present, else the module zip from proxy.golang.org.
- Full fetch ≈ 26 s / 435 MB. Rerun is a no-op; `--force` refetches.

## Pins (`pins.go`)

- `AWSSDKRef` = aws-sdk-go-v2 `release-YYYY-MM-DD` tag; `AzureSDKRef` = commit SHA (no monorepo tag).
- `GCPAPIRef` is a **fallback** for binaries that do not link `google.golang.org/api`; the disco
  binary reads the version from `debug.ReadBuildInfo()`. `TestPinMatchesGoMod` fails when go.mod
  moves — bump the pin in the same commit as the dependency.
- A pin bump changes the denominator; reports print the pins.
- The Service Reference catalog is unversioned and served live, so its pin is content:
  `AWSServiceReferenceDigest` = first 12 hex of `sha256(index.json)`, printed as
  `service-reference@<digest>`. A fetch that disagrees is **reported, never enforced**
  (extractor diagnostic + `coverage sdk status` stderr note) — the served catalog is whatever it
  is. The catalog moves most days: bump the digest in the same commit as the regenerated
  baseline. `make check-coverage` treats a moved pin as growth to regenerate, not a regression.

## Archive handling (`fetch.go`)

- GitHub tarballs prefix `<repo>-<ref>/` (Strip 1); module zips `<module>@<ver>/` (Strip 2).
- Entry paths are backslash-normalised, `..`/absolute rejected, 64 MB per-entry cap; JSON-index
  entry names must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`, must be unique, and may not be
  `index` (it would overwrite the index document). The whole index is validated **before** the
  errgroup fan-out: a mid-loop reject left goroutines writing into the discarded snapshot.
- `fetchClient()` sets per-hop timeouts (`ResponseHeaderTimeout`, `TLSHandshakeTimeout`) and
  **never an overall `Client.Timeout`** — the AWS tarball is hundreds of MB and a whole-request
  deadline aborts a healthy slow download. Bodies are wrapped in `idleReader`, which cancels the
  request after 60 s with no byte and reports `transfer stalled: …` rather than `context canceled`.

## Catalog facts (verified 2026-09-16)

- Smithy service shape: `aws.auth#sigv4.name` equals the Service Reference service name (join key);
  `aws.api#service` carries `sdkId`/`arnNamespace`/`cloudFormationName`. Ops carry
  `smithy.api#required` on input members and `smithy.api#paginated`. Resource shapes are rare
  (lambda 11, ec2/s3/rds 0) — never rely on them.
- Service Reference doc: `Actions[].Annotations.Properties.{IsList,IsWrite}`, `Actions[].Resources[]`
  (targets), `Operations[].AuthorizedActions` (SDK op → IAM action, e.g. `ListObjectsV2`→`ListBucket`),
  `Resources[].ARNFormats`.

## Extractors (Phase 2, verified against live caches 2026-09-16)

- Contract: `internal/sdkinv/conformance.Check` runs against `internal/sdkinv/<name>/testdata/cache`
  for every registered extractor (`TestAllExtractorsConform` in `all`). A fixture must hold
  resource, catalog and non-resource candidates at depth 0 and 1, sorted keys, every parent
  present, deterministic output (`reflect.DeepEqual` across two runs — sort every slice you
  build from a map, including `Signals`).
- `StrongerClass` ranks resource > catalog > non-resource > attribute: a detail read (Get)
  merged onto a lister's key never outranks the lister.
- `irregular` is a closed, purely linguistic suffix table (`indices`→`index`, `thesauri`→`thesaurus`,
  `series`/`species`/`ephemeris` invariant, `lenses`→`lens`) consulted first by **both** `Singular`
  and `Ident`. It is matched as a suffix, so compounds work (`attachedIndices`,
  `revenueStatisticsTimeSeries`). Nothing in it may name a cloud resource — that would be the
  hand-maintained list this subsystem exists to avoid — and every entry carries a `norm_test` pair.
- `Ident` is the only identity (irregulars, `-ies`→`y`, `-yses`→`-ysis`, then every trailing `e`/`s`
  dropped): `caches`/`cache`, `aliases`/`alias`, `statuses`/`status`, `analyses`/`analysis`,
  `indices`/`index` meet there and nowhere else. Never show an Ident (`alias`→`alia`). Without the
  irregulars `qbusiness/index` and `qbusiness/indice` were two covered rows for one collection.
- `Singular` is display only: irregulars, then keeps `-ss`/`-us`/`-is`/`-ias`, `-ies`→`y`,
  `-yses`→`-ysis`, `-sses/-xes/-shes`→`-es`, `-ches` after a single non-`e` vowel→`-e` (cache,
  niche) else `-es` (batch, beach, approach), `-ses` after `u`/`ia`→`-s` (status, alias) else
  `-se` (database, case, license), else `-s`. No inflector dep. Any new rule needs a `norm_test`
  pair; the keys shipped before this was audited include `bedrock/flowalia`, `wellarchitected/len`,
  `config/…statuse`, `iotsitewise/timesery`, `kendra/thesauri` and
  `gameliftstreams/applicationshadercach`, and `RestApi`/`RestApis` were two candidates.
- Live counts (pins in `pins.go`): AWS 5551 candidates / 354 services (3246 resource);
  Azure 3650 (1959 resource); GCP 1843 / 191 APIs (1140 resource). Each live test logs these;
  a large swing after a pin bump is the signal to re-check anchors.
- `Candidate.Refs` (Phase 6, `<p>/refs.go`): dotted paths on the listed element that name other
  resources, sorted and unique, own id excluded, depth-bounded. Refs are a **hint**: they rank
  `coverage resolvers --missing`, never bucket a row, and their absence is not proof of a derived
  leaf (`internal/providers/CLAUDE.md`). Recall matters more than precision.
  - **AWS**: Smithy output → collection element; a detail read has no collection, so its elements
    are **every** structure member of the output, and `flatRefs` additionally takes the output's
    own id-like primitives — `GetEnvironment` answers with `vpcId`/`subnetIds`/`loadBalancerArn`
    beside a `storageConfigurations` list, and walking only the structures loses all three
    (recognising only the single-structure shape left 1,275 detail reads refless). A ref must
    target a `string`: enum, integer and long targets are never refs (`State.Name`), and
    `tokenNameRe` drops `*Token`/`*ETag`/`*RequestId`/`*RevisionId` names. Own id = bare
    `Arn/Id/Name` or a suffix-of-noun stem at depth 0, **plus an exact-noun stem at every depth**
    (`DescribeInstances`' element is `Reservation`, so `Instances.InstanceId` arrives at depth 1).
    Never extend the loose suffix rule below depth 0: it matches 682 genuine cross-resource refs.
  - **Azure**: each `<op>HandleResponse` body is bounded at the **next top-level `func`** before
    the `&result.X` search — unbounded, a HEAD op's tag-only decoder swallowed the following
    function and stole its result type, and non-overlapping matches then left the real lister with
    none (106 listers, 66 in armapimanagement). The models table is `models.go` **or** the older
    `zz_generated_models.go` (whose fields carry a struct tag, hence the cut at the first backtick),
    plus `response_types.go`: a bare `<X>Array []*X` response field is registered as a synthetic
    list result so `refsOf` resolves it like a real `*ListResult`. String refs = `*ID`/`*IDs`, the
    exact name `ManagedBy`, and `*URI`/`*URL` **only** under a `keyvault`/`encryptionkey` path — a
    bare URI/URL suffix pulls in a hundred data-plane endpoints and sign-on URLs. Envelope
    `ID/Name/Type/Location/Tags` skipped.
  - **GCP**: `response.$ref` → the array-of-`$ref` property whose `Ident` matches the collection
    noun, else the richest item schema, else alphabetical (aggregated lists: the map value's
    array). Taking the first outright gave `dataflow/jobs` `FailedLocation`'s zero refs over
    `Job`'s 17. Then string properties named `*Link/*Url/*Id/*Ref/*Account/*Network` or described
    as a URL / resource name / service account / KMS; `$`-prefixed names are JSON-schema keys, not
    refs, and `selfLink/id/name/kind/displayName/generateName/clientOperationId/revisionId` are the
    element's own. Schemas decode lazily from `json.RawMessage`, and `element` shares `walk`'s
    `seen` set: self-referential schemas are in the cache (`discovery JsonSchema`, `BackendRule`,
    dataflow `BoundedTrieNode`) and an unguarded recursion aborts the whole command.
  - Every fixture carries one candidate with refs (conformance).
- `Universe.Other` carries every SDK op that is not a candidate op (writes, item reads,
  actions). Every extractor must end with `sdkinv.SortOps(u.Other)`: it is filled from map
  walks, and the conformance DeepEqual only catches the omission on some runs (`-count=5`).
  Pairing needs it to explain types built from a Get/Describe (`other-op:` reason).

### AWS (`aws/extract.go`)

- Join key = Smithy `aws.auth#sigv4.name`; shared signing names (rds/neptune/docdb, s3/s3control,
  apigateway/apigatewayv2) land ops from several models on one key — `Operation.Module` tells them
  apart; pairing (Phase 3) disambiguates by import.
- Service Reference `Operations[].AuthorizedActions` binds an op to an action only by same name
  or same verb (`ListObjectsV2`→`ListBucket`). A generic action (apigateway's `GET`) binds nothing,
  so the Smithy shape decides `IsList` (signal `fallback`). 14 services are absent from the
  catalog entirely (cloudwatch=`monitoring`, tagging, sso portal, partner central…).
- SR `IsList` is incomplete (backup-gateway `ListGateways`, batch `DescribeComputeEnvironments`
  are false): a List/Describe op with a collection output is a lister unless the bound action
  `IsWrite` (signal `shape-list`). Trusting SR alone left 269 scanner types unpaired.
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
    lower-case for the same reason: three spellings of the account member put 15 QuickSight rows
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
    251 candidates whose depth was not their parent's plus one. Do not deepen an entry from a
    proposal of a deeper lineage: `route53/hostedzone` and `lambda/function` are listed both
    ways, and the shallow listing is the truth.
- Admission evidence (Phase 8 step 4): a member is id-like by the PascalCase suffix **or** by the
  whole word in any case (`id`, `arn`, `name` — AppSync, DataZone, Bedrock, EKS, Grafana, Lex and
  Cognito model them lower-case, and 65 child collections were demoted as id-less). A list of
  primitives counts as a collection when its name is id-like or stems to the op's noun
  (`sqs:ListQueues` answers `QueueUrls []string`, and `aws:sqs:queue` was in neither the
  numerator nor the denominator). A payload wrapping its collection one level down
  (`GetApps` → `ApplicationsResponse.Item[]`) counts only when the op noun is plural or the inner
  collection is that noun — a blind descent turns all 399 single-structure read outputs into
  listings (`wrapped-list` signal).
- A service the catalog files under another name (`cloudwatch`→`monitoring`, `cloudcontrol`→
  `cloudformation`, the IoT data planes) joins by **operation-name containment**: the smallest
  document that authorises every operation of the model, recorded as `sr:document=<name>`.
  Without it CloudWatch's dashboards, insight rules, mute rules and anomaly detectors were
  `catalog` and excluded although disco stores all of them.
- Ballast rules (Phase 8 step 5). `writeNoun` fills from **lifecycle** verbs only
  (Create/Delete/Put/Add/Import/Provision/Allocate/Register/Associate/Attach/Copy/Restore/
  Launch/Run/Publish); a setting verb (Update, Modify, Enable, Set, Export, Start) records a
  `mutable` signal instead, because `ModifyIdFormat` does not make `ec2/idformat` a resource a
  scanner could ever close (99 rows left the denominator). The cost is measured and accepted:
  seven rows disco does store (`ssm/instanceinformation`, `iot/v2loggingoption`,
  `lakeformation/permission`, `shield/emergencycontactsetting`,
  `securityhub/configurationpolicyassociation` and two ec2 options) are now `excluded`; they keep
  their `discoType` and their `scanner-lists` signal, so the evidence is visible, not lost.
- `tag` is a **non-subject** like `resource`/`target` in `selfStems`: an op whose noun is `tag`
  keys no candidate and ships as an `Other` op (`route53/tag` was a covered row attributed to
  `aws:route53:cidr-collection`). `ec2/tag` is therefore no longer a candidate — the old
  catalog example.
- Class: cross-cutting counts only the targets that are neither the subject nor an ancestor its
  lineage names (55 rows → 9; `identitystore:ListGroupMemberships` is authorised against the
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
- Folding (Phase 8 step 6): a noun that is the service's own name plus an existing candidate's
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
  the parent entry's display when one exists, else the lineage's spelling; ~250 live parents
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

### Azure (`azure/extract.go`)

- Walk every non-test `.go` under `sdk/resourcemanager` (builders live in `client.go` /
  `api_client.go` too); receiver may be bare `Client` (label `armX:Client.Op`).
- Key = namespace + statics after the last `providers/` segment **whose successor is static**;
  the generic `…/providers/{resourceProviderNamespace}/features` form otherwise discarded the real
  namespace for `*`. `providers` is in `scopeNames`, so the trailing `providers/{param}` pair of
  that form strips instead of keying `microsoft.features/providers/features`.
  `armresources`/`armsubscriptions` own paths without `providers/` (`microsoft.resources/resourcegroups`).
- A trailing static singleton (`default`, `current`) after a collection name is the **item id**,
  not another collection (`sdkinv.singletonIDs`, applied in `StripScopes`): `.../blobServices/default`
  is the one blob service. Reading it as a collection discarded the PUT on that path into
  `Universe.Other`, so 114 rows were `no-item-path` non-resources while the cache plainly showed
  GET+PUT, and 171 keys carried a `default` segment no scanner could ever match. It is a grammar
  rule about ARM ids; do not extend it into a list of resource names.
- Scope pairs (`subscriptions/{}`, `resourceGroups/{}`, `locations/{}`, `managementGroups/{}`)
  strip only when more path follows, so a trailing container (`resourceGroups`) stays a
  candidate; `{scope}` as first param → `extension` scope (role assignments).
- Class from item-path methods: PUT/PATCH/DELETE → resource (`item-write`); GET/HEAD only →
  catalog (`item-read-only`); none → non-resource (`no-item-path`). **Exception** (`arm-envelope`):
  a GET-only collection whose element carries the ARM proxy-resource envelope (`SystemData`, or
  `ID`+`Type`) **and** is a child or lives at subscription/resource-group scope is a resource.
  517 of 572 catalog rows carried that envelope and 434 were children — the very shape the AWS
  extractor calls a resource — so the two providers' denominators rested on different rules and
  504 real gaps never reached the report. Both halves are load-bearing: the envelope alone
  admits `microsoft.authorization/provideroperations` and `microsoft.advisor/metadata`, and
  dropping the GET/HEAD requirement admits the generic `/subscriptions/{id}/resources` lister
  (a live test guards that one).
- `scopeOf` reads scope pairs from the **whole** template (a `managementGroups/{}` pair sits after
  the namespace in microsoft.management's own paths) but decides `extension` on the prefix before
  the namespace only — a `providers/{param}` pair there is the parent the caller names.
- A collection every one of whose listers is reached through a `locations/{location}` pair
  carries `scope-pair:locations`: the key strips the pair and the ARM type name keeps it, so
  `internal/providers/azure`'s `RegistryKey` puts it back for `--cross-check`. A sibling lister
  on a path without the pair clears it — then ARM has no `locations` in the type either.
- A lister with no item path whose result element matches a same-module entry that does write is
  folded into that entry as an extra op under the `alternate-lister` signal (87 rows, e.g.
  `microsoft.sql/servers/replicationlinks` into `…/servers/databases/replicationlinks`). Judged on
  its own path an alternate has no write verb and read as a non-resource.
- `index()` admits any collection GET, paged or not: ~307 singleton/action GETs come in this way
  and all land in `excluded`. The README says so; do not read `Value []*T` as an enforced rule.

### GCP (`gcp/extract.go`)

- API included iff some lister is cloud-rooted (`cloudRooted`): the template's first segment is a
  cloud root (`projects`, `organizations`, `folders`, `billingAccounts`, `customers`), **or** the
  template is param-first and a path parameter's *description* names one of those formats (Cloud
  Asset and Service Usage expand `{+parent}` to a generic `{id}/{id}` because the parameter takes
  any container), **or** a leading static sits directly in front of a cloud root (Pub/Sub Lite's
  `admin/projects/{p}/…`, stripped from the template and the doc path by `dropGroupingRoot`).
  A cloud root **deeper** in the path is deliberately not enough: it admits DFA reporting, Tag
  Manager and the Cloud Channel reseller API (110 rows). Service Networking stays out for that
  reason — its project appears only below `services/{service}`. Roots that are the
  listed collection (`cloudresourcemanager/projects`) keep their own scope.
- Keys are lower-cased so one collection reached through several versions is one candidate
  (run v1 `namespaces.workerpools` + v2 `projects.locations.workerPools` → `run/workerpools`).
  Op labels keep the document's spelling. A leading `namespaces/{}` pair is run v1's project
  alias and is dropped; `namespaces` below a real root (iam workload identity pools) is a
  resource collection — never add it to `scopeNames`.
- Template = `flatPath` else `path` with `{+x}` expanded from the parameter `pattern`; the leading
  version prefix strips on `^v\d` **or** the document's own `Version` (Deployment Manager's alpha
  document is spelled `alpha` and the whole API dropped out of the universe). Scope nodes strip but
  the last node is kept.
- `zones`/`regions` strip only in the compute shape `projects/{p}/(zones|regions)/{x}`
  (`scopesFor`): Dataplex nests assets under `lakes/{lake}/zones/{zone}` where the zone is itself a
  create-capable collection. The same map drives `stripScopeNodes`, or the key and the doc path
  disagree.
- `Scope` is the template root's cloud root, else `project` when `dropKnativeRoot` stripped run
  v1's alias or the lister declares a required `project`/`projectId`/`parent` **query** parameter
  (storage buckets), else `global`.
- Two Discovery documents can be one service (`sql` and `sqladmin` both answer at
  `sqladmin.googleapis.com`): documents are grouped by rootUrl + servicePath + canonicalName and
  the name matching the rootUrl's host label wins. rootUrl alone is shared by 31 documents — never
  collapse on it.
- `Parent` is resolved to the nearest ancestor prefix that is itself a candidate and cleared when
  none is; the document tree nests nodes that list nothing (`appengine/apps`). `Depth` stays the
  template's parent-id count, so **depth>0 with no parent is legal** — `conformance.Check` only
  rejects depth 0 *with* a parent, and `TestExtract_Live` asserts every parent resolves.
- Operation nodes detected by response `$ref` suffix (`Operation`, `ListOperationsResponse`),
  never by method-name rules (`run` executions have `cancel`).
- Class: `insert|create` → resource; `delete` only → resource; `patch|update|destroy|undelete` →
  resource (`mutable`: README defines catalog as provider-published and read-only, and a node the
  caller can patch or destroy is neither — `setIamPolicy` alone is too weak and would pull in four
  genuine catalogs); `get` only → catalog; else non-resource. Alpha/beta-only collections carry
  `preview-only`.
- Listers are `list` and `aggregatedList`, plus `search`/`fetch`/`listPolicies` **only on a node
  with no `list`** (`altListers`): `cloudresourcemanager/organizations` is reachable by `search`
  in v1 and v3 and `list` only in v1beta1. A shape-based lister rule mints 313 bogus rows from
  filtered sub-views (`listUsable`, `listManagedInstances`).
- An `aggregatedList` is registered on every sibling collection of the same document whose
  element schema matches, under `aggregated-by:<node>` (39 rows, the Compute regional and global
  twins): the response is a map of scoped lists, and disco lists the regional types through that
  one call. Method names are walked **sorted** — map order otherwise decided which lister
  recorded a node's element, and two extractions disagreed.
- A node with `create` and no `get`/`delete`/`patch` anywhere stays a resource. Only three
  candidates are in that state and two (`cloudbilling/subaccounts`,
  `androiddeviceprovisioning/partners/customers`) are genuine resources, so the rule that would
  drop `monitoring/timeseries` costs more than it saves (#48, recorded not shipped).

## Pairing (Phase 3, `pairing/`)

- `pairing.Scan(ctx, cache, provider, dir)` = extract from the cache + `Walk` + `Unpaired`;
  wraps `sdkinv.ErrNotFetched` so `internal/providers/<p>/<p>_pairing_test.go` skips without
  the cache. Those two tests (`TestScannerOpLabelsResolve`, `TestEveryEmittedTypePaired`) are the
  gate: label-no-op / label-no-anchor / label-malformed / unresolved-receiver fail; sdk-skew is
  logged. `TestEveryEmittedTypePaired` prints `Result.StoredBy[type]` with the failure, which is
  the only thing that names which scanner the walker could not reach.
- go/parser with `SkipObjectResolution`, non-test files only, stdlib only. Anchors (SDK call
  shapes, per `Resolver.Anchors`) are authoritative; op labels in string literals are a
  cross-check. `Type*` constants with a `<provider>:` value are the types (other string consts
  such as `quotaServiceName = "gcp:cloudquotas"` are ignored by name).
- **Naming a type is not storing it.** A function's `Type*` identifiers count only when the
  function, or something it reaches, builds a `store.Resource` or calls `UpsertResource(s)` /
  `InsertResourcesIfAbsent`. The signal travels down through callees and then **up** the caller
  chain to a fixpoint (`markStoring`): the store is usually one hop down (a batch handed to
  `upsertWithProjClosure`), and a phase table such as `wafPhases` names the type and the op while
  its storing driver sits one hop up and never sees the constant. Relationship and hierarchy
  writes are deliberately not the signal — a resolver names its source types in a
  `store.ResourceFilter` and writes edges alone, and counting those made
  `microsoft.insights/diagnosticsettings` claim 30 foreign types. Without a store in reach the
  anchor still stands, so the candidate stays **covered as `sidecar`**; only the credited types go.
- **Fed callees only.** `reachableTypes` walks `walkFedCallees`: a callee counts when the call
  hands it something the caller produced (a local, a field of one, a composite, a call result),
  or when the caller was itself fed and forwards its parameters on (`linkFeeds`, a fixpoint — a
  store four plain pass-through hops down is still this listing's). A helper called with nothing
  but the caller's own `(ctx, st, scanID)` cannot be storing rows this listing returned, so its
  types are not credited to this anchor; it keeps its own pairing. `orphanTypes`/`derived` read
  the **unpruned** set instead, because "rows built from a sibling's listing" is exactly what
  derived means.
- Reach: a function's types are its own plus its fed callees' to depth 3, and past the cap only
  through callees that anchor nothing themselves — the cap holds over-attribution down, but a
  correct scanner storing four hops down was reported `unexplained` and failed both gates. A
  method call on a local
  (`s.scanTables`) and a method value passed as an argument (`forEachItem(…, s.scanDataset)`)
  both resolve to the one method of that name in the package (ambiguous names resolve to
  nothing). A `Type*` constant passed as an argument flows to the callee per caller
  (`fn.outflow`/`inflow`): the helper's own pairing carries every caller's type, a caller's
  pairing only what it and its walked callees pass. The union rule it replaced let
  `storeIDs(st, TypeA, …)` from one scanner explain TypeB from a scanner with no SDK call
  (fixture `scanMasked`) and hid two Azure sdk-skew types behind sibling anchors.
  A package-level `var` table naming `Type*` constants flows into every function that
  references the var (`collectVarTypes`), and `SDKFiles` counts those references, so a
  table-only type is `unexplained`, never `non-sdk`. Labels resolve against the function, its callees and its direct callers — the
  label sits at the error site, the pager is often built one frame up.
- `Walk` is deterministic within a process: callee sets are walked in sorted order and two
  callees anchoring one candidate tie-break by `betterAnchor` (a listing beats a detail read,
  then the smaller label). Map-range order otherwise picked the reported op per run.
- Kinds: `emits` (anchor + types), `sidecar` (anchor, no types — a listing helper; its direct
  caller is then paired with what it stores), `derived` (a dispatcher with no anchor of its
  own: types no anchored callee stores, paired with the listings whose service or leaf relates
  to the type, minus types some `emits` pairing already carries), `label` (label with no call
  anywhere), `other` (non-candidate op), `skew` (call the pinned SDK lacks).
- A **promoted** anchor — one a typeless listing helper contributed, not one this function calls
  — is credited only with what this function's own flow stores, never with what a *caller* passed
  in. The `cur == f` inflow rule is right for a helper that genuinely lists and stores and wrong
  here: it gave `microsoft.resources/resourcegroups` five types from unrelated callers of the
  same fan-out helper.
- Unpaired reasons: `non-sdk` (every file referencing the const imports no SDK module —
  Entra over Graph), `other-op:<label>`, `sdk-skew:<op>`, else `unexplained` (fatal). A `label`
  pairing does **not** count as paired (it proves no SDK call, which is why `inventory.go`'s
  `pairingKinds` excludes it), and `derived` yields to `other-op`/`sdk-skew`: evidence by
  proximity must not displace a named op.
- sdk-skew runs both ways. `AzureSDKRef` is monorepo HEAD: an op newer than the snapshot is fixed
  by bumping the pin, an op **deleted upstream** (armcompute CloudServices, the whole
  armappplatform module) is not — bumping moves further away, and the fix is a `go.mod` major bump
  plus retiring the scanner. `TestScannerOpLabelsResolve` logs the imported `arm` modules the
  snapshot no longer holds. Never pin Azure per `go.mod`: that deletes ~769 rows and inflates the
  percentage.
- sdk-skew is real and expected: the Azure monorepo HEAD differs from the go.mod majors
  (armcompute `CloudServices*`, armsubscription `Subscriptions.List`, armappplatform absent,
  postgresql flexible servers, edgeorder); GCP `serviceusage.services.list`. 24 Azure + 1 GCP
  at the 2026-09-16 pins. Which direction the fix runs is the bullet above.
- Azure: a receiver binds from `armX.New<Y>Client(`, a client-factory `cf.New<Y>Client()`, a
  `*armX.<Y>Client` parameter or struct field, or a package-local interface seam whose method
  signatures mention `armX.<Y>Client…Response/Options` (`TypeOwner`). An unbound pager resolves
  only when exactly one imported client has that op. Any `List*` on a bound client anchors
  even when the pin lacks it (generated clients have no other methods; skew surfaces in Walk).
  Label form is `armX:<Y>.<Op>` with the
  `Client` suffix dropped and bare `Client` kept (`armredis:Client.ListBySubscription`) — 22
  scanner labels were typos against this form and were corrected in Phase 3. `LooseLabelGrammar`
  (the optional `LooseLabeller` half of the `Resolver` interface, Azure only) catches the near
  miss `arm<module>:<Op>` with the `Client.` dropped, which the strict grammar made invisible —
  an off-grammar literal is simply not a label, so a typo read as "this function names no op".
  AWS's decorated labels (`amp:ListWorkspaces(extended)`) are deliberate operator-facing text
  and must stay undiagnosed: do not give AWS a loose grammar.
- AWS: anchors are `pkg.New<Op>Paginator(`, `pkg.<Op>Input{` and `recv.<Op>(` for any op an
  imported service package ships; receivers need no binding. `LabelAliases` accepts the
  separator-stripped service (`accessanalyzer:` for `access-analyzer`).
- GCP: anchors are `svc.A.B.List(`/`.AggregatedList(` with the root a bound local or struct
  field, and any other Discovery method on a bound service (`Projects.GetIamPolicy`). Aliases:
  full path, scope-stripped path in the document's spelling, and `svc:<leaf>.<method>`.
- Fixture: `pairing/testdata/scannerpkg/<p>` is a synthetic scanner package parsed, never
  compiled, against `../<p>/testdata/cache`; `pairing_test.go` asserts one case per rule and the
  exact line of each diagnostic — renumber when editing those files.
