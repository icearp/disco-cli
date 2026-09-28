# CLAUDE.md — `internal/providers/azure/azureinventory/`

Azure SDK inventory: the extractor that derives the Azure coverage denominator from the pinned SDK source, its refs, its pairing resolver and its pins. Core contract and pairing walk: `internal/sdkinv/CLAUDE.md`.

## Cache layout

- `azure@<sha>/repo/sdk/resourcemanager/<rp>/arm<rp>/{*_client.go,models.go,response_types.go,responses.go}`.
  Filter anchors on `sdk/resourcemanager/` — the monorepo also ships `profile/*/resourcemanager` copies.
  Monorepo HEAD holds one major per module dir (majors are tags), so no version picking.

## Pins (`pins.go`)

- `SDKRef` = azure-sdk-for-go commit SHA (no monorepo tag).

## Refs (`refs.go`)

- each `<op>HandleResponse` body is bounded at the **next top-level `func`** before
    the `&result.X` search — unbounded, a HEAD op's tag-only decoder swallowed the following
    function and stole its result type, and non-overlapping matches then left the real lister with
    none (mostly in armapimanagement). The models table is `models.go` **or** the older
    `zz_generated_models.go` (whose fields carry a struct tag, hence the cut at the first backtick),
    plus `response_types.go`: a bare `<X>Array []*X` response field is registered as a synthetic
    list result so `refsOf` resolves it like a real `*ListResult`. String refs = `*ID`/`*IDs`, the
    exact name `ManagedBy`, and `*URI`/`*URL` **only** under a `keyvault`/`encryptionkey` path — a
    bare URI/URL suffix pulls in data-plane endpoints and sign-on URLs. Envelope
    `ID/Name/Type/Location/Tags` skipped.

## Extractor (`extract.go`)

- Walk every non-test `.go` under `sdk/resourcemanager` (builders live in `client.go` /
  `api_client.go` too); receiver may be bare `Client` (label `armX:Client.Op`).
- Request builders are **lowercase-first** (`[a-z]\w*CreateRequest(`): an exported method may end in
  `CreateRequest` (hdinsight `ValidateClusterCreateRequest`); matching it minted a phantom op.
- Key = namespace + statics after the last `providers/` segment **whose successor is static**;
  the generic `…/providers/{resourceProviderNamespace}/features` form otherwise discarded the real
  namespace for `*`. `providers` is in `scopeNames`, so the trailing `providers/{param}` pair of
  that form strips instead of keying `microsoft.features/providers/features`.
  `armresources`/`armsubscriptions` own paths without `providers/` (`microsoft.resources/resourcegroups`).
- A trailing static singleton (`default`, `current`) after a collection name is the **item id**,
  not another collection (`singletonIDs`, exposed as `IsSingletonID` and applied through `sdkinv.MarkIDs` before
  `StripScopes`; the core holds no spellings, and GCP deliberately applies none — the pinned
  Discovery docs carry no static `default`/`current` id segment): `.../blobServices/default`
  is the one blob service. Reading it as a collection discarded the PUT on that path into
  `Universe.Other`, so those rows were `no-item-path` non-resources while the cache plainly showed
  GET+PUT, and keys carried a `default` segment no scanner could ever match. It is a grammar
  rule about ARM ids; do not extend it into a list of resource names.
- Scope pairs (`subscriptions/{}`, `resourceGroups/{}`, `locations/{}`, `managementGroups/{}`)
  strip only when more path follows, so a trailing container (`resourceGroups`) stays a
  candidate; `{scope}` as first param → `extension` scope (role assignments).
- Class from item-path methods: PUT/PATCH/DELETE → resource (`item-write`); GET/HEAD only →
  catalog (`item-read-only`); none → non-resource (`no-item-path`). **Exception** (`arm-envelope`):
  a GET-only collection whose element carries the ARM proxy-resource envelope (`SystemData`, or
  `ID`+`Type`) **and** is a child or lives at subscription/resource-group scope is a resource.
  Most catalog rows carried that envelope and were children — the very shape the AWS extractor
  calls a resource — so without it the two providers' denominators rested on different rules and
  real gaps never reached the report. Both halves are load-bearing: the envelope alone
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
  folded into that entry as an extra op under the `alternate-lister` signal (e.g.
  `microsoft.sql/servers/replicationlinks` into `…/servers/databases/replicationlinks`). Judged on
  its own path an alternate has no write verb and read as a non-resource.
- `index()` admits any collection GET, paged or not: singleton/action GETs come in this way
  and all land in `excluded`. The README says so; do not read `Value []*T` as an enforced rule.

## Pairing resolver (`resolver.go`)

- sdk-skew is expected and runs both ways, because `SDKRef` is monorepo HEAD, not the
  go.mod majors. An op newer than the snapshot → bump the pin. An op **deleted upstream**
  (armcompute `CloudServices*`, the whole armappplatform module) → bumping moves further away;
  the fix is a `go.mod` major bump plus retiring the scanner. `TestScannerOpLabelsResolve` logs
  the imported `arm` modules the snapshot no longer holds. Never pin Azure per `go.mod`: that
  deletes rows and inflates the percentage.

- Azure: a receiver binds from `armX.New<Y>Client(`, a client-factory `cf.New<Y>Client()`, a
  `*armX.<Y>Client` parameter or struct field, or a package-local interface seam whose method
  signatures mention `armX.<Y>Client…Response/Options` (`TypeOwner`). An unbound pager resolves
  only when exactly one imported client has that op. Any `List*` on a bound client anchors
  even when the pin lacks it (generated clients have no other methods; skew surfaces in Walk).
  Label form is `armX:<Y>.<Op>` with the
  `Client` suffix dropped and bare `Client` kept (`armredis:Client.ListBySubscription`). `LooseLabelGrammar`
  (the optional `LooseLabeller` half of the `Resolver` interface, Azure only) catches the near
  miss `arm<module>:<Op>` with the `Client.` dropped, which the strict grammar made invisible —
  an off-grammar literal is simply not a label, so a typo read as "this function names no op".
  AWS's decorated labels (`amp:ListWorkspaces(extended)`) are deliberate operator-facing text
  and must stay undiagnosed: do not give AWS a loose grammar.
