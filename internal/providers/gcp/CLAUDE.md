# CLAUDE.md — `internal/providers/gcp/`

GCP scanner/resolver conventions. Cross-provider rules: `internal/providers/CLAUDE.md`.

## GCP-specific registration quirks

- New `Type*` const lives in `gcp_types.go` const block. `KnownTypes()` is gone — coverage truth is `emits []coverage.TypeDecl` on the scanner's `registerService` / `registerOrgService`. Hierarchy scanners use `registerExtraEmits` in `gcp_hierarchy.go`. `CollectEmits()` in `gcp_registry.go` unions all sources for the `coverage.Provider` impl in `gcp_coverage.go`. Add the disco-type → Discovery-key entry in `gcp_coverage.go` `Aliases()` too.

### Unified per-type declaration via `registerType`

`registerType(restype.Descriptor{...})` in `gcp_registry.go` is the single-site
declaration for everything disco knows about a resource type: coverage emit
(`Service` + `Leaf`/`Uncatalogued`), upstream alias (`Upstream`, empty falls
through to `AlgorithmicKey`), redaction rules (`Redact`), volatile fields
(`Volatile`), and the unconditional `Managed` flag (the store stamps
`ManagedByProvider` by type). It forwards field rules into the shared
redact/volatile/managed engines and routes the coverage decl through
`descriptorEmits` so `CollectEmits` still surfaces it; `Aliases()` returns
`descriptorAliases()` directly.

**GCP is fully migrated** — every type is declared via `registerType` from the
`init()` of the file owning its upsert. The legacy `staticAliases` map and
`gcp_redact.go` are gone; there is no `serviceEntry.emits` /
`registerExtraEmits` for new work. New services declare their types with
`registerType`. `TestNoDoubleDeclaredTypes` rejects any type declared via both
the descriptor path and a legacy emit site; the mirror/orphan/leaf coverage
tests guard naming and resolver-source correctness.
- `gcp_scanner_test.go` carries TWO expectation lists: `expectedGCPServices` (project-scope) and `expectedGCPOrgServices` (org-scope, via `registerOrgService`). New scanner updates whichever list matches its registration call — getting it wrong only fails at test time, not build time.

## Discover what's not yet covered

`disco coverage services --providers gcp --filter uncovered` — diff GCP Discovery API vs scanner emits. Works credless (Discovery is public). Other filters: `covered`, `uncatalogued` (SDK-scanned but absent from Discovery — none on GCP today: `gcp:iam:policy` lands `covered` because Discovery's `iam.googleapis.com/Policy` matches it), `upstream-missing`. `--check-strict` exits 1 on `upstream-missing` rows (alias-map drift signal) — wire into CI to catch alias/scanner drift. A Discovery fetch failure — the top-level list **or any per-API doc** — is always fatal (exit 2 via the cmd layer's `errCoverageRegistryUnreachable`), so a dropped doc never silently undercounts the upstream and falsely flags that API's types as drift.

## Resolver-edge metadata: `EdgeDecl`

`registerResolver(fn, emits ...EdgeDecl)` is variadic — every resolver should list each `(source, target, kind)` triple it upserts (mirrors AWS's `aws.EdgeDecl`/`aws_registry.go`). Backs `disco coverage resolvers --providers gcp` (per-resolver edge counts + service segments) and `--missing` (emitted disco types never appearing as an `EdgeDecl.Source` — the resolver-gap inventory). `fn` must be a named function, not an anonymous closure — `registerResolver` panics at init time on an anonymous fn (reflects as `pkg.init.funcN`, useless in audit output). As of the Wave 11 scanner buildout closing, this gap list is large (~216 of 247 emitted types) because Waves 1-11 shipped scanners only, resolver work deferred every wave — most orphans are genuinely terminal (KeyRing, Organization, Project self-node, config singletons) rather than real gaps; triage via a `Leaf: true` flag on the scanner's `coverage.TypeDecl` (AWS precedent: `internal/providers/CLAUDE.md` "Leaf") before treating the raw `--missing` count as an actionable backlog.

## Scoping cheat-sheet

Pick scanner shape on first read:
- **Per-project, no location** (Pub/Sub, BigQuery, Cloud DNS, Cloud Build): `parent = projects/{p}` — one List call, paginated.
- **Wildcard `locations/-`** (Cloud Functions v2, Cloud Run, Cloud Run Jobs, Batch, Composer, Artifact Registry, Cert Manager): `parent = projects/{p}/locations/-` returns every location in one paginated walk. Prefer when API supports.
- **Per-location fan-out** (Cloud KMS): `Locations.List` → bounded fan-out via `semaphore.NewWeighted`. Pair with `apiDisabled atomic.Bool` to dedup repeat 403s when API off.
- **Org-scoped** (VPC-SC, folder/org IAM policies, folder/org Logging sinks): use `registerOrgService(orgServiceEntry{...})` in `gcp_registry.go`. fn fires ONCE per scan with `[]orgScope` from `scanHierarchy`. Dispatch via `runOrgServices` in `gcp_scanner.go`.
- **Per-region (no wildcard)** (Dataproc clusters, future Spanner regional, AI Platform regional): use `gcpRegionFanoutScan[P,T]` in `gcp_scan_helpers.go` — generic helper that enumerates regions via `gcpRegions`, fans out per-region paginated lists bounded by `concurrency`, accumulates a mutex-protected batch, and finally calls `upsertWithProjClosure`. Per-region 403 / API-not-enabled tolerated silently. Caller supplies pagerFn (region → pager), pageItems (page → items), itemToResource (item, region → *store.Resource or nil to skip). Test seam: `gcpRegionFanoutScanIn` takes a pre-resolved region slice (skips `gcpRegions`) so unit tests inject regions directly. Dataflow uses its `Projects.Jobs.Aggregated` endpoint instead — no fan-out needed when an aggregated SDK call exists.

## Org-service scope-kind dispatch

Org-services receiving `[]orgScope` typically dispatch on `sc.Kind` ("organization" / "folder") to pick the matching SDK sub-service (`svc.Organizations.X` vs `svc.Folders.X`) — bodies are otherwise identical. Keep the dispatch in a tiny helper (e.g. `getOrgScopePolicy`, `pages := func(handler) { switch sc.Kind { ... } }`) so the per-scope batch+upsert+closure code stays single-pathed. VPC-SC is org-only — skip folder scopes silently. Precedent: `iampolicy_org_scanners.go` (CRM), `observability_org_scanners.go` (Logging), `vpcsc_scanners.go` (org-only).

## Firewall edge direction (R4.5)

`firewall_resolvers.go::resolveFirewallRelationships` emits `firewall -[uses]-> instance` (firewall is the FROM side). Rule authors writing exposure rules anchored on instances must walk inbound (`direction: in` in `Related`) to reach the firewall — see `builtin/gcp-instance-internet-exposed.yaml`. Mirrors AWS where SG→ENI also flows from policy to consumer; differs from intuition that "instance has firewall".

## Singleton resources via Get

Some GCP services have singleton "policy" per project, no list surface — fetch via `Get`, upsert one row. Precedent: BinAuth `Projects.GetPolicy(projects/{p}/policy)`. Don't list these.

## Service-account email index

Many resolvers FK-check SA email ref (Cloud Functions, Cloud Run, Composer, BinAuth attestor, Cloud Run Jobs, Batch, IAM policy bindings). Use `buildSAEmailIndex(p, st)` in `gcp_scan_helpers.go` — returns `map[email]ResourceID` over all in-project `gcp:iam:service-account` resources. Cross-project SA refs implicitly skip (won't match in-project index).

For fields that may store either full resource name `projects/{p}/serviceAccounts/{email}` or bare email (Cloud Build trigger), keep the inline two-map pattern: `saIDByNative` + `saIDByEmail`, check native first, fall back to email. `cloudbuild_resolvers.go` is the canonical site.

## Service registration

Each `<svc>_scanners.go` calls `registerService` from `init()`. Service `fn` runs once per project — fan-out across projects + concurrency cap (`maxConcurrentServices = 10`) handled by `scanProject`. Resolvers register via `registerResolver(fn)`; resolver fn called once per project after all phase-1 scans land.

## Scopes above project (org / folder)

Per-project service entries can't reach folder/org scopes — need either (a) single-pass scanner running once per scan (sibling to `scanHierarchy`), or (b) synthetic per-project entry filtering to `p.ParentID`. No fan-out helper yet — first follow-up needing it (folder/org IAM policies, org policies, asset inventory at folder scope) should add one to `gcp_scan_helpers.go`.

## IAM policy resource shape

`gcp:iam:policy` synthesized — IAM policies not first-class GCP resources, JSON blobs returned by `GetIamPolicy` on protected scope. NativeID `projects/{id}/iamPolicy` (and `folders/{id}/iamPolicy` / `organizations/{id}/iamPolicy`) — the `/iamPolicy` suffix disambiguates from BinAuth's real `{scope}/policy` (see Synthetic NativeIDs). Bindings stored verbatim under resource's `attributes` JSON; resolvers parse `bindings[].members[]`, emit edges typed `RelUses` with `{role: roles/...}` in edge attrs.

Member matching: only `serviceAccount:{email}` members FK-safe today. Email parsed from existing `gcp:iam:service-account` NativeIDs (`projects/{id}/serviceAccounts/{email}`); cross-project SA emails won't match in-store index, edge skipped. Non-SA member kinds (user, group, domain, allUsers, allAuthenticatedUsers) need Entra-equivalent identity scanner — defer until lands rather than synthesizing principal resources.

## API-not-enabled → service-disabled sentinel

When API not enabled, scanners propagate sentinel error; dispatch loop renders `(project: disabled)` on per-service progress line — no warning. Mechanism:

- `isAPINotEnabled(err)` (in `gcp_errors.go`) matches three known shapes: 403 message `"has not been used in project"`, 400 message `"has not enabled"` (BigQuery), `googleapi.Error.Errors[].Reason == "accessNotConfigured"`. Extend this predicate (not `isPermissionDenied`) when adding scanners surfacing API-not-enabled differently.
- `skipIfDenied` returns `markServiceDisabled(err)` when `isAPINotEnabled` matches, else records `ScanWarning`, returns nil. Existing call sites (`if isPermissionDenied(err) { return ..., skipIfDenied(...) }`) bubble sentinel up — no per-scanner-file edits needed.
- `scanProject` detects sentinel via `errors.Is(err, errServiceDisabled)`, calls `st.ReportService(name, scope, 0, 0, 0, 0, store.ServiceDisabled)` so `cmd/scan.go` renders `(project: disabled)` suffix. Mirrors AWS pattern (`aws/aws.go` `errServiceDisabled` + `markServiceDisabled`). The non-disabled paths bind `st.WithUpsertCounters(&newC, &changedC)` around `svc.fn` and report `(total, new, changed)` — same as AWS.

Real IAM 403 (rare; caller lacks permission but API enabled) still goes to warnings via `skipIfDenied`'s final branch. Billing-disabled (project billing off — free trial ended / no billing account) routes to its own `errBillingDisabled` sentinel → `(project: billing disabled)` annotation (a self-enableable precondition, sibling to service-disabled — not a warning, not fatal). `isBillingDisabled` matches on message, so it catches both flavours GCP emits: 403 `"...has billing disabled..."` (Spanner) and 400 failedPrecondition `"Billing is disabled for project ..."`. It's folded into `isPermissionDenied` (so the ~130 `skipIfDenied` gate sites route it non-fatally) and re-classified first in `skipIfDenied` before the API-not-enabled check.

## Permission-denied is non-fatal

`isPermissionDenied(err)` covers 401/403/BigQuery-400. Always pair with `skipIfDenied(st, "<api>:<method>", scope, err)` — never propagate from per-service scanner. Function dispatches internally between sentinel (API not enabled — see above) and warning (real permission denial).

## `forEachItem[T]` helper — bounded-concurrency fan-out

`forEachItem[T any](ctx, concurrency, items, fn)` in `gcp_scan_helpers.go` runs `fn(gctx, item)` over each item with at most `concurrency` goroutines in flight. First non-nil err aborts siblings. Used by per-location / per-zone / per-SA / per-dataset fan-out (KMS, DNS record-sets, IAM-key, BigQuery). Replaces the `sem := semaphore.NewWeighted(...); g, gctx := errgroup.WithContext(...); for ... { g.Go(... sem.Acquire ... defer sem.Release ...) }; g.Wait()` setup repeated across four scanners.

Inner pagination + mutex still belong to the caller — `forEachItem` only owns the fan-out skeleton. Caller-owned: `apiDisabled atomic.Bool` short-circuit (KMS), `var mu sync.Mutex` over shared batch slice, per-call err classification.

## `runPaginated[P]` helper — preferred List driver

`runPaginated[P any](ctx, st, p, action, req, pageHandler)` in `gcp_scan_helpers.go` wraps `req.Pages(ctx, fn)` with the boilerplate every scanner phase repeats: invoke `Pages`, accumulate `(total, inserted)` per page, classify final err via `isPermissionDenied` → `skipIfDenied` (which dispatches sentinel vs warning). Generic over the page type — works on every `google.golang.org/api/*` `*.List()` request struct (all expose `Pages(ctx, func(*P) error) error`).

Use it for every paginated phase. Page handler returns `(int, int, error)` — usually after building a `[]*store.Resource` and calling `upsertWithProjClosure`. Replaces ~150 LOC of repeated `err = req.Pages(...) {...}; if err != nil { if isPermissionDenied(err) { return ..., skipIfDenied(...) } return ..., err }` boilerplate.

Single-page `.Do()` calls (BigTable, Firestore, Spanner per-instance Databases.List) and inner `Pages` calls running inside `errgroup.WithContext` (BigQuery phase 2, DNS record-sets, KMS keyrings, IAM-key fan-out) keep manual classification — `runPaginated` doesn't fit those because the gctx/mu interactions are tied to the surrounding errgroup.

## Wildcard locations parent

For per-location APIs supporting it (`cloudfunctions/v2`, `run/v2`, future Pub/Sub regional, AI Platform), `parent = "projects/{p}/locations/-"` returns resources across every location in one paginated call. Prefer over per-location fan-out — API does per-location query in parallel server-side. Helper `locationFromResourceName` (in `serverless_scanners.go`) extracts location segment from returned resource names for per-resource `Region` field. Some legacy APIs (Cloud KMS, Certificate Manager) don't support `-`, require locations-list-then-fan-out pattern.

## Synthetic NativeIDs

Some GCP resources have no API-issued canonical name. Synthesize from parent resource path + natural key:
- `gcp:dns:resource-record-set` → `{zoneNativeID}/rrsets/{type}/{name}` — `(name, type)` is natural key (one zone can have A + AAAA for same hostname).
- `gcp:iam:policy` → `{scope}/iamPolicy` — IAM policy is JSON returned by `GetIamPolicy`, not real resource. The `/iamPolicy` suffix (not `/policy`) keeps it distinct from `gcp:binaryauthorization:policy`, whose **real** API name is `{scope}/policy`; both live in account `{scope}` so a shared suffix would collide identity now that `type` is out of the hash. IAM's is the synthesized side, so it's the one that moves.
- **GCS bucket children** → `{bucket}/{collection}/{name}`: `storage:notification` = `{bucket}/notificationConfigs/{id}`, `storage:bucket-access-control` = `{bucket}/acl/{entity}`, `storage:default-object-access-control` = `{bucket}/defaultObjectAcl/{entity}`, `storage:managed-folder` = `{bucket}/managedFolders/{name}`, `storage:folder` = `{bucket}/folders/{name}`. The GCS API returns the **same** `Id` (`{bucket}/{entity}` / `{bucket}/{name}`) for bucket-ACL vs default-object-ACL and for managed-folder vs HNS-folder; the collection segment is the disambiguator the raw `Id` lacks. Built from the in-scope `bucket` param + a fixed collection literal + the real natural-key field (`bucket` var, not a mutation of the returned `Id`). Chosen over `SelfLink` so native_id stays a short path (consistent with siblings) instead of a full `https://…` URL. The parent bucket itself keys on `SelfLink` — bucket-children do NOT.
Stable across rescans; matches synthetic-NativeID precedent in `store/CLAUDE.md`.

## Shared upsert+closure helpers

`upsertWithProjClosure(p, st, batch)` in `loadbalancing_scanners.go` upserts + closure-pairs to project parent. Resources hanging directly off project use this.

`upsertWithParent(st, batch, parentID)` (same file) takes any parent resource id — use for child→parent closure where parent isn't the project: BigQuery table → dataset, DNS rrset → managed-zone, cert-map entry → map, Spanner database → instance, Bigtable cluster → instance. KMS-style multi-parent batches (keyring → project + cryptokey → keyring in one slice) still build pairs inline because the parent depends on the resource type.

## AggregatedList scope-key parsing

GCP Compute `*.AggregatedList` returns `map[string]ScopedList` where keys are either `"global"` or `"regions/{region}"`. Helper `scopedListRegion(scope)` in `loadbalancing_scanners.go` extracts region segment (returns "" for global). Reuse for any new AggregatedList consumer.

## API-not-enabled fan-out short-circuit

Scanners fanning out over global locations/regions catalog before hitting API-gated endpoint (KMS keyrings, future Pub/Sub regional, Cloud Run regional) still benefit from per-project `atomic.Bool` short-circuit even though sentinel mechanism above already suppresses warning storm. Reason: each goroutine still issues API call before returning sentinel. Flipping bool on first 403 lets remaining goroutines exit without network round-trip. Precedent: `kms_scanners.go` `apiDisabled`.

## Scanner-level tests via httptest fake server

Per-phase scanners (`scanForwardingRules`, `scanInstances`, etc.) already accept `*compute.Service` — directly testable, no body extraction needed. Pattern: `httptest.NewServer` + `option.WithEndpoint(srv.URL)` + `option.WithHTTPClient(srv.Client())` + `option.WithoutAuthentication()` builds a concrete client pointed at the fake. Helpers in `fake_testhelper_test.go`: `fakeGCPServer(t, routes)`, `fakeGCPServerStatus(t, status, body)`, `fakeComputeService(t, srv)`. Precedent: `loadbalancing_scanners_test.go` covers happy path, real-403 ScanWarning, API-not-enabled sentinel.

**Endpoint path gotcha:** `option.WithEndpoint(srv.URL)` replaces the *full* base URL including `/compute/v1`. Route keys are `/projects/{p}/aggregated/forwardingRules` — **not** `/compute/v1/projects/...`. First-time 404? Strip the API-version prefix.

For permission-denied test bodies, mirror the exact `googleapi.Error` JSON shape `isPermissionDenied` / `isAPINotEnabled` inspect — `accessNotConfigured` reason or `"has not been used in project"` message triggers the sentinel path; anything else triggers the warning path.

## Resource ID conventions

NativeID = full resource name where GCP returns one (`sa.Name`, `inst.SelfLink`, `projects/{id}` for projects, `organizations/{id}` for orgs). Compute uses self-link URLs verbatim — include project/region/zone, so same instance scanned in two projects produces two distinct rows. For hierarchy parent of any project-scoped resource use `store.ResourceID("gcp", p.ID, p.ID)` — project's NativeID is bare ID, not `projects/{id}` form.