# CLAUDE.md — `internal/sdkinv/`

SDK source cache + extractors that derive the coverage denominator. Imports nothing from
`internal/providers` or `internal/coverage`. Extractors register from `init()`; blank imports
live in `internal/sdkinv/all` (no slim build tags — extractors link no cloud SDK).

## Cache layout (`$XDG_CACHE_HOME/disco/sdk/<provider>@<ref>/`, `disco coverage sdk fetch|status`, `make sdk-fetch`)

- `manifest.json` written last; a dir without it is "absent". Fetch lands in `.tmp-*` then renames.
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
- The Service Reference catalog is unversioned. Its pin (`service-reference@YYYY-MM-DD`) is the
  newest per-service `modified` stamp in `index.json`, never the file mtime: two fetches of the
  same catalog agree, a catalog update is a pin bump, and `make check-coverage` treats a moved
  pin as growth to regenerate, not a regression. A cache rebuild on CI can therefore move it.

## Archive handling (`fetch.go`)

- GitHub tarballs prefix `<repo>-<ref>/` (Strip 1); module zips `<module>@<ver>/` (Strip 2).
- Entry paths are backslash-normalised, `..`/absolute rejected, 64 MB per-entry cap; JSON-index
  entry names must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`.

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
- `Ident` is the only identity (`-ies`→`y`, `-yses`→`-ysis`, then every trailing `e`/`s`
  dropped): `caches`/`cache`, `aliases`/`alias`, `statuses`/`status`, `analyses`/`analysis` meet
  there and nowhere else. Never show an Ident (`alias`→`alia`).
- `Singular` is display only: keeps `-sis`/`-ss`/`-us`/`-ias`, `-ies`→`y`, `-yses`→`-ysis`,
  `-sses/-xes/-ches/-shes`→`-es`, `-ses` after `u`/`ia`/`n`→`-s` (status, alias, lens) else `-se`
  (database, case), else `-s`. No inflector dep. Any new rule needs a `norm_test` pair; the old
  `CanonSingular` keys shipped `bedrock/flowalia`, `wellarchitected/len`, `config/…statuse` and
  split `RestApi`/`RestApis` into two candidates before the live keys were audited.
- Live counts (pins in `pins.go`): AWS 5561 candidates / 354 services (3250 resource);
  Azure 3744 (1959 resource); GCP 1859 / 192 APIs (1151 resource). Each live test logs these;
  a large swing after a pin bump is the signal to re-check anchors.
- `Candidate.Refs` (Phase 6, `<p>/refs.go`): dotted paths on the listed element that name other
  resources, sorted and unique, own id excluded, depth-bounded. AWS: Smithy output → collection
  element (or a detail read's single structure) → `idLikeRe` members, prelude `smithy.api#String`
  targets are absent from the model file and count as primitives; own = bare `Arn/Id/Name` or a
  stem equal to the noun at depth 0. Azure: `<op>HandleResponse` names the `*ListResult`, its
  `Value []*T` element walked through `models.go` (regex over generated structs, parsed once per
  module): sub-resource structs (`*SubResource`, `*…Reference`, or only an `ID`) and `*ID`
  strings; envelope `ID/Name/Type/Location/Tags` skipped. GCP: `response.$ref` → array-of-`$ref`
  property (aggregated lists: the map value's array) → string properties named `*Link/*Url/*Id/
  *Ref/*Account/*Network` or described as a URL / resource name / service account / KMS;
  `selfLink/id/name/kind` skipped. Schemas decode lazily from `json.RawMessage`. Every fixture
  carries one candidate with refs (conformance).
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
- Class: cross-cutting (≥3 targets, `ListTagsForResource`) → attribute; SR resource with an
  ARN matching the noun → resource; child with id-bearing collection → resource
  (`child-uncatalogued`, e.g. `kms/grant`); child without ids → attribute; noun with an
  `IsWrite` non-tagging action → resource (`writable-noun`); no collection → non-resource;
  else catalog (`ec2/instancetype`, `ec2/accountattribute`, `ec2/tag`).
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
  resource (GetBasePathMapping's `BasePath` is not id-like).
- Scope params (`AccountId`, `Region`, paging members) never denote a parent.
- SR structs use camelCase tags: `encoding/json` matches keys case-insensitively, so the
  PascalCase catalog decodes without a tagliatelle exclusion.

### Azure (`azure/extract.go`)

- Walk every non-test `.go` under `sdk/resourcemanager` (builders live in `client.go` /
  `api_client.go` too); receiver may be bare `Client` (label `armX:Client.Op`).
- Key = namespace + statics after the last `providers/` segment; `armresources`/`armsubscriptions`
  own paths without `providers/` (`microsoft.resources/resourcegroups`).
- Scope pairs (`subscriptions/{}`, `resourceGroups/{}`, `locations/{}`, `managementGroups/{}`)
  strip only when more path follows, so a trailing container (`resourceGroups`) stays a
  candidate; `{scope}` as first param → `extension` scope (role assignments).
- Class from item-path methods: PUT/PATCH/DELETE → resource; GET/HEAD only → catalog; none →
  non-resource.

### GCP (`gcp/extract.go`)

- API included iff some lister's template root is a cloud root (`projects`, `organizations`,
  `folders`, `billingAccounts`, `customers`); 192 of 654 docs qualify. Roots that are the
  listed collection (`cloudresourcemanager/projects`) keep their own scope.
- Keys are lower-cased so one collection reached through several versions is one candidate
  (run v1 `namespaces.workerpools` + v2 `projects.locations.workerPools` → `run/workerpools`).
  Op labels keep the document's spelling. A leading `namespaces/{}` pair is run v1's project
  alias and is dropped; `namespaces` below a real root (iam workload identity pools) is a
  resource collection — never add it to `scopeNames`.
- Template = `flatPath` else `path` with `{+x}` expanded from the parameter `pattern`; scope
  nodes strip but the last node is kept.
- Operation nodes detected by response `$ref` suffix (`Operation`, `ListOperationsResponse`),
  never by method-name rules (`run` executions have `cancel`).
- Class: `insert|create` → resource; `delete` only → resource; `get` only → catalog; else
  non-resource. Alpha/beta-only collections carry `preview-only`.

## Pairing (Phase 3, `pairing/`)

- `pairing.Scan(ctx, cache, provider, dir)` = extract from the cache + `Walk` + `Unpaired`;
  wraps `sdkinv.ErrNotFetched` so `internal/providers/<p>/<p>_pairing_test.go` skips without
  the cache. Those two tests (`TestScannerOpLabelsResolve`, `TestEveryEmittedTypePaired`) are the
  gate: label-no-op / label-no-anchor / unresolved-receiver fail; sdk-skew is logged.
- go/parser with `SkipObjectResolution`, non-test files only, stdlib only. Anchors (SDK call
  shapes, per `Resolver.Anchors`) are authoritative; op labels in string literals are a
  cross-check. `Type*` constants with a `<provider>:` value are the types (other string consts
  such as `quotaServiceName = "gcp:cloudquotas"` are ignored by name).
- Reach: a function's types are its own plus its callees' to depth 3; a method call on a local
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
- Kinds: `emits` (anchor + types), `sidecar` (anchor, no types — a listing helper; its direct
  caller is then paired with what it stores), `derived` (a dispatcher with no anchor of its
  own: types no anchored callee stores, paired with everything the callees list, minus types
  some `emits` pairing already carries), `label` (label with no call anywhere), `other`
  (non-candidate op), `skew` (call the pinned SDK lacks).
- Unpaired reasons: `non-sdk` (every file referencing the const imports no SDK module —
  Entra over Graph), `other-op:<label>`, `sdk-skew:<op>`, else `unexplained` (fatal).
- sdk-skew is real and expected: the Azure monorepo HEAD differs from the go.mod majors
  (armcompute `CloudServices*`, armsubscription `Subscriptions.List`, armappplatform absent,
  postgresql flexible servers, edgeorder); GCP `serviceusage.services.list`. 24 Azure + 1 GCP
  at the 2026-09-16 pins. Bumping the pins is the fix, not the scanner.
- Azure: a receiver binds from `armX.New<Y>Client(`, a client-factory `cf.New<Y>Client()`, a
  `*armX.<Y>Client` parameter or struct field, or a package-local interface seam whose method
  signatures mention `armX.<Y>Client…Response/Options` (`TypeOwner`). An unbound pager resolves
  only when exactly one imported client has that op. Any `List*` on a bound client anchors
  even when the pin lacks it (generated clients have no other methods; skew surfaces in Walk).
  Label form is `armX:<Y>.<Op>` with the
  `Client` suffix dropped and bare `Client` kept (`armredis:Client.ListBySubscription`) — 22
  scanner labels were typos against this form and were corrected in Phase 3.
- AWS: anchors are `pkg.New<Op>Paginator(`, `pkg.<Op>Input{` and `recv.<Op>(` for any op an
  imported service package ships; receivers need no binding. `LabelAliases` accepts the
  separator-stripped service (`accessanalyzer:` for `access-analyzer`).
- GCP: anchors are `svc.A.B.List(`/`.AggregatedList(` with the root a bound local or struct
  field, and any other Discovery method on a bound service (`Projects.GetIamPolicy`). Aliases:
  full path, scope-stripped path in the document's spelling, and `svc:<leaf>.<method>`.
- Fixture: `pairing/testdata/scannerpkg/<p>` is a synthetic scanner package parsed, never
  compiled, against `../<p>/testdata/cache`; `pairing_test.go` asserts one case per rule and the
  exact line of each diagnostic — renumber when editing those files.
