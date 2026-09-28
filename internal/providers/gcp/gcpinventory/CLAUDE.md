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

- A list response's element is the array property named after the collection, else the richest
  item schema, else alphabetical; an aggregated list's element is the array inside its map value
  (`viaMap`). Taking the first array outright gave `dataflow/jobs` `FailedLocation`'s zero refs
  over `Job`'s many. Refs are string properties named `*Link/*Url/*Id/*Ref/*Account/*Network` or
  described as a URL / resource name / service account / KMS, walked through inline schemas,
  array items and map values (`labels.{}.x`) at any depth. `$`-prefixed names are JSON-schema
  keys; `selfLink/id/name/kind/displayName/…` are the element's own. Schemas decode lazily, and a
  `seen` set guards `$ref` cycles: the cache holds self-referential schemas (`JsonSchema`,
  `BackendRule`, `BoundedTrieNode`) and an unguarded walk aborts the command.

## Extractor (`extract.go`)

Model-first: every rule reads HTTP methods, path templates and schemas. There are **no method-name
or noun lists** (the old `list`/`search`/`fetch` names, `scopesFor`, `dropKnativeRoot`,
`versionRe` are gone); do not add one. The one declared vocabulary is the tenancy roots
(`cloudRoots`: projects, organizations, folders, billingAccounts, customers) and placements
(`placements`: locations, zones, regions) — Discovery has no structured tenancy marker, so this is
the provider's `Scopes()` declaration. Each rule below has a fixture case in
`testdata/cache/api/widgets` (comments there name the rule); break one and a test goes red —
verified by mutating each of 22 rules (2026-09-28). A rule without a red mutation is untested.

- **Admission.** An API is in the universe when any of its documents accepts the
  `cloud-platform` OAuth scope. Others drop as `non-cloud-api` (youtube, adsense,
  chromepolicy, …). Measured alternatives, both rejected: "non-creatable node = scope" and
  "CRM collections are the roots" (CRM lists operations/locations/liens, lacks billingAccounts).
- **Alias documents.** Documents sharing rootUrl + servicePath + canonicalName are one service;
  the name matching the rootUrl's host label wins, the rest drop as `alias-document` (`sql` vs
  `sqladmin`). rootUrl alone is shared by 31 documents — never collapse on it.
- **Template.** servicePath + flatPath (else path with `{+x}` expanded from the parameter
  pattern), the custom verb cut by core `sdkinv.SplitVerb`. Leading statics strip while another
  static follows, or while the segment is the document's own version (`isVersion`: Deployment
  Manager's `alpha`, `v2_beta`'s `beta`) — so `compute/v1/`, `admin/directory/v1/` and run v1's
  `apis/serving.knative.dev/v1/` go without a list.
- **Scope pairs.** `<name>/{id}` is a scope when `name` is declared **and** this document cannot
  create it (no POST/PUT on that collection path): apigee creates organizations and Dataplex
  creates lake zones, so there they are parents. A declared name followed by a static
  (`locations/global`) is a pinned scope; both strip. Other mid-path literals stay in the key.
- **Lister.** A GET on a collection path (last segment static) whose response carries an
  element, confirmed by a sibling (`confirm`, signal `list:<how>`): the item GET must answer the
  element (either may wrap the other one level) — this is what keeps filtered views such as
  `searchFeatures` out; without an item GET, a write on the item carrying it (`item-write`), a
  DELETE on the item (`item-delete`), the create body (`create-body`), or for a map element any
  GET answering it (`aggregated`). An untyped `any` array takes the create body as its element
  (admin Aliases). An unconfirmed paged GET stays in Other with an info diagnostic.
- **Class** (`classify`), by HTTP method × path, in order: POST/PUT on the collection, or a
  custom-verb POST on it answering the element (`patchJobs:execute`, `evaluations:import`;
  signal `create-verb` — `objects:lookup` lands here too, and stream/migration objects are the
  caller's, not a provider catalog) → resource `create`; LRO element shape (`done` + `response|error`), or the element answered by a
  non-POST write elsewhere (-1: dns `managedZones.patch` → `changes`) or by POSTs on ≥2 other
  collections (compute `Operation`) → non-resource `operation-node`; one other POST → resource
  `created-elsewhere` (`widgets:snapshot`); DELETE on the item → `delete-only`; PATCH/PUT/POST
  on the item, or a compute-style action sub-path (`edits`) → `mutable`; item GET → catalog
  `get-only`; else non-resource `list-only`. An action sub-path (`regionInstanceGroups/{g}/setNamedPorts`)
  counts only when it answers the document's change handle (a schema some DELETE answers), has
  no item path below it (sub-collection) and no GET (singleton `regions/{r}/snapshotSettings`);
  `listInstances`/`testIamPermissions` POSTs and `setIamPolicy` answer something else. Custom
  verbs on the item stay a `custom-verb` signal: `locations/{l}:batchTranslateText` answers an
  Operation too, and counting it made 12 `*/locations` resources. In `writers`, "other
  collection" means another last static, so compute's `instances/{i}/start` counts as one.
  An aggregated list is classified by its **home** collection, the one whose item GET answers
  its element.
- **Key.** API + Discovery resource path, minus leading prefix nodes and scope nodes (never the
  last), lower-cased. A method listing below its own node's item
  (`instances.listVmExtensionStates`) appends its last static. Root methods (no node) key by the
  last static.
- **Folds.** `foldSameCollection` (per document): a nested route whose element and leaf match a
  depth-0 route, and whose parents the document lists nowhere, takes the top key (run v1
  `namespaces.services` → `run/services`; discoveryengine collections). `foldAliasRoutes` (per
  API, across documents): a route through a container the API lists nowhere merges into the
  API's **one** other collection with that leaf, when the elements match up to a version prefix
  (`sameElement`: `GoogleCloudRunV2Job` ≈ `Job`, but `PublisherModel` ≠ `Model`). It folds 10
  live routes (run v1 `namespaces/*`, appengine `apps/*`, …), named in an info diagnostic.
- **Aggregated twins.** An `aggregatedList` is also registered on every other collection of the
  document with the same element **and scope** (`aggregated-by:<leaf>`): the regional and global
  twins are listed by that one call, and pairing anchors every candidate an op lists. Without the
  scope gate `networkFirewallPolicies.aggregatedList` covered the org/folder `firewallPolicies`
  it never returns.
- **Parents.** The innermost non-scope parent item path matched against the API's listed item
  paths, walking outward. A miss leaves Parent empty with Depth kept (info diagnostic) —
  depth>0 with no parent is legal; `TestExtract_Live` asserts every parent resolves.
- **Scope** (`scopeOf`): a param-first path → `unscoped` (warn; cloudasset and serviceusage take
  any container), a template rooted at a cloud root → that scope, a required query parameter
  naming one (`?project=`, storage buckets) → that scope, else `global`. Alpha/beta-only
  collections carry `preview-only`. `Required` = `parameterOrder`; `Paged` = a `pageToken` param.

### Accepted residuals (2026-09-28, v0.292.0)

1660 candidates (1186 resource, 260 catalog, 214 non-resource) across 193 APIs, from 1882. Gone:
the 7 non-cloud APIs, filtered views (`:search`), listers returning strings (managedkafka,
logging), `monitoring/timeseries` (#48), apigee `organizations.list` (its element is
`OrganizationProjectMapping`), and duplicate keys the old name rules minted. Gained:
`sqladmin/backups`, regional compute parents (`regioninstancegroupmanagerresizerequests`).
apigee children key as `apigee/organizations/*` at depth 1 with no parent (organizations are
creatable, not listable). run v1's namespace routes report scope `global`: the path names no
container. `foldAliasRoutes` merges collections of one element reached through different unlisted
containers (dialogflow draft vs environment session contexts, aiplatform memory-bank vs
reasoning-engine memories): the element is the resource type. `foldSameCollection` decides per
document, so a later version that lists the container keeps its own key (discoveryengine v1alpha
`collections/datastores`, policysimulator `replays/operations`); today those stragglers are
`preview-only` and outside the denominator — re-check after a pin bump.

## Pairing resolver (`resolver.go`)

- Every call down a bound `*Service` chain (root a bound local or struct field) is a Discovery
  method and anchors, so a method the pin lacks raises `sdk-skew`: the generator names every
  resource-chain type `*Service`. Any other bound SDK type (a response page, `googleapi.Error`)
  anchors only on an op the universe knows — else `page.Header.Get` read as skew and hid a
  disco-only type as "explained". An unbound chain anchors only when exactly one imported API has
  that candidate op. No method-name list.
- `OpKey` and anchors compare canonically per dotted segment (`sdkinv.Canon`): the generator
  turns Discovery's `iap_tunnel` into the Go field `IapTunnel`. `ImportKey` keys every package
  under `google.golang.org/api/`; a helper package (option, googleapi) has no ops and no
`*Service`, so nothing in it anchors. `LabelOp` is canonical too: the core label fallback looks
up `byKey` by it.
- Aliases: full path, scope-stripped path in the document's spelling (for an Other op, the
  declared scope nodes drop), and `svc:<leaf>.<method>`.
