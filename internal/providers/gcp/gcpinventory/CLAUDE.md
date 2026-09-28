# CLAUDE.md — `internal/providers/gcp/gcpinventory/`

GCP SDK inventory: the extractor that derives the GCP coverage denominator from the pinned SDK source, its refs, its pairing resolver and its pins. Core contract and pairing walk: `internal/sdkinv/CLAUDE.md`.

## Cache layout

- `gcp@<ver>/api/<api>/<ver>/<api>-api.json` (Discovery docs), copied from GOMODCACHE when
  present, else the module zip from proxy.golang.org.

## Pins (`pins.go`)

- `APIRef` is a **fallback** for binaries that do not link `google.golang.org/api`; the disco
  binary reads the version from `debug.ReadBuildInfo()`. `TestPinMatchesGoMod` fails when go.mod
  moves — bump the pin in the same commit as the dependency.

## Refs (`refs.go`)

- `response.$ref` → the array-of-`$ref` property whose `Ident` matches the collection
    noun, else the richest item schema, else alphabetical (aggregated lists: the map value's
    array). Taking the first outright gave `dataflow/jobs` `FailedLocation`'s zero refs over
    `Job`'s many. Then string properties named `*Link/*Url/*Id/*Ref/*Account/*Network` or described
    as a URL / resource name / service account / KMS; `$`-prefixed names are JSON-schema keys, not
    refs, and `selfLink/id/name/kind/displayName/generateName/clientOperationId/revisionId` are the
    element's own. Schemas decode lazily from `json.RawMessage`, and `element` shares `walk`'s
    `seen` set: self-referential schemas are in the cache (`discovery JsonSchema`, `BackendRule`,
    dataflow `BoundedTrieNode`) and an unguarded recursion aborts the whole command.

## Extractor (`extract.go`)

- API included iff some lister is cloud-rooted (`cloudRooted`): the template's first segment is a
  cloud root (`projects`, `organizations`, `folders`, `billingAccounts`, `customers`), **or** the
  template is param-first and a path parameter's *description* names one of those formats (Cloud
  Asset and Service Usage expand `{+parent}` to a generic `{id}/{id}` because the parameter takes
  any container), **or** a leading static sits directly in front of a cloud root (Pub/Sub Lite's
  `admin/projects/{p}/…`, stripped from the template and the doc path by `dropGroupingRoot`).
  A cloud root **deeper** in the path is deliberately not enough: it admits DFA reporting, Tag
  Manager and the Cloud Channel reseller API. Service Networking stays out for that
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
  the name matching the rootUrl's host label wins. rootUrl alone is shared by many documents — never
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
  in v1 and v3 and `list` only in v1beta1. A shape-based lister rule mints bogus rows from
  filtered sub-views (`listUsable`, `listManagedInstances`).
- An `aggregatedList` is registered on every sibling collection of the same document whose
  element schema matches, under `aggregated-by:<node>` (the Compute regional and global
  twins): the response is a map of scoped lists, and disco lists the regional types through that
  one call. Method names are walked **sorted** — map order otherwise decided which lister
  recorded a node's element, and two extractions disagreed.
- A node with `create` and no `get`/`delete`/`patch` anywhere stays a resource. Few
  candidates are in that state and most (`cloudbilling/subaccounts`,
  `androiddeviceprovisioning/partners/customers`) are genuine resources, so the rule that would
  drop `monitoring/timeseries` costs more than it saves (#48, recorded not shipped).

## Pairing resolver (`resolver.go`)

- GCP: anchors are `svc.A.B.List(`/`.AggregatedList(` with the root a bound local or struct
  field, and any other Discovery method on a bound service (`Projects.GetIamPolicy`). Aliases:
  full path, scope-stripped path in the document's spelling, and `svc:<leaf>.<method>`.
