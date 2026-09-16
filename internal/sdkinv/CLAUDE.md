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
- `Singular` keeps `-sis`/`-ss`/`-us`, strips `-ies`→`y`, `-sses/-xes/-ches/-shes`→`-es`, else `-s`.
  `apis`→`api`; `addresses`→`address`. No inflector dep — matching is separator-stripped anyway.
- Live counts (pins in `pins.go`): AWS 4490 candidates / 348 services (2460 resource);
  Azure 3744 (1959 resource); GCP 1859 / 192 APIs (1151 resource). Each live test logs these;
  a large swing after a pin bump is the signal to re-check anchors.

### AWS (`aws/extract.go`)

- Join key = Smithy `aws.auth#sigv4.name`; shared signing names (rds/neptune/docdb, s3/s3control,
  apigateway/apigatewayv2) land ops from several models on one key — `Operation.Module` tells them
  apart; pairing (Phase 3) disambiguates by import.
- Service Reference `Operations[].AuthorizedActions` binds an op to an action only by same name
  or same verb (`ListObjectsV2`→`ListBucket`). A generic action (apigateway's `GET`) binds nothing,
  so the Smithy shape decides `IsList` (signal `fallback`). 14 services are absent from the
  catalog entirely (cloudwatch=`monitoring`, tagging, sso portal, partner central…).
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
- Template = `flatPath` else `path` with `{+x}` expanded from the parameter `pattern`; scope
  nodes strip but the last node is kept.
- Operation nodes detected by response `$ref` suffix (`Operation`, `ListOperationsResponse`),
  never by method-name rules (`run` executions have `cancel`).
- Class: `insert|create` → resource; `delete` only → resource; `get` only → catalog; else
  non-resource. Alpha/beta-only collections carry `preview-only`.
