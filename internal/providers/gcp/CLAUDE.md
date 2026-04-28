# CLAUDE.md — `internal/providers/gcp/`

GCP scanner/resolver conventions. Cross-provider rules: `internal/providers/CLAUDE.md`.

## Scoping cheat-sheet

Pick scanner shape on first read:
- **Per-project, no location** (Pub/Sub, BigQuery, Cloud DNS, Cloud Build): `parent = projects/{p}` — one List call, paginated.
- **Wildcard `locations/-`** (Cloud Functions v2, Cloud Run, Cloud Run Jobs, Batch, Composer, Artifact Registry, Cert Manager): `parent = projects/{p}/locations/-` returns every location in one paginated walk. Prefer when API supports.
- **Per-location fan-out** (Cloud KMS): `Locations.List` → bounded fan-out via `semaphore.NewWeighted`. Pair with `apiDisabled atomic.Bool` to dedup repeat 403s when API off.
- **Org-scoped** (VPC-SC, folder/org IAM policies, folder/org Logging sinks): parent above project. **No clean lane today** — needs once-per-scan registration shared by all three. Defer until lane lands.
- **Per-region (no wildcard)** (Dataproc clusters, Dataflow jobs, Spanner): each region listed individually. Spanner enumerates instance regions from `Config`. Dataproc + Dataflow need shared region-list helper not yet built — defer.

## Singleton resources via Get

Some GCP services have singleton "policy" per project, no list surface — fetch via `Get`, upsert one row. Precedent: BinAuth `Projects.GetPolicy(projects/{p}/policy)`. Don't list these.

## Service-account email index

Many resolvers FK-check SA email ref (Cloud Functions, Cloud Run, Composer, Cloud Build trigger, BinAuth attestor, Cloud Run Jobs, Batch, IAM policy bindings). Build index once per resolver: list `gcp:iam:service-account` resources, key by trailing path segment of `NativeID` (email). For fields that may store full resource name `projects/{p}/serviceAccounts/{email}` or just email, build second index keyed on full NativeID, check both. Cross-project SA refs implicitly skip — won't match in-project index.

## Service registration

Each `<svc>_scanners.go` calls `registerService` from `init()`. Service `fn` runs once per project — fan-out across projects + concurrency cap (`maxConcurrentServices = 10`) handled by `scanProject`. Resolvers register via `registerResolver(fn)`; resolver fn called once per project after all phase-1 scans land.

## Scopes above project (org / folder)

Per-project service entries can't reach folder/org scopes — need either (a) single-pass scanner running once per scan (sibling to `scanHierarchy`), or (b) synthetic per-project entry filtering to `p.ParentID`. No fan-out helper yet — first follow-up needing it (folder/org IAM policies, org policies, asset inventory at folder scope) should add one to `gcp.go`.

## IAM policy resource shape

`gcp:iam:policy` synthesized — IAM policies not first-class GCP resources, JSON blobs returned by `GetIamPolicy` on protected scope. NativeID `projects/{id}/policy` (and `folders/{id}/policy` / `organizations/{id}/policy` when land). Bindings stored verbatim under resource's `attributes` JSON; resolvers parse `bindings[].members[]`, emit edges typed `RelUses` with `{role: roles/...}` in edge attrs.

Member matching: only `serviceAccount:{email}` members FK-safe today. Email parsed from existing `gcp:iam:service-account` NativeIDs (`projects/{id}/serviceAccounts/{email}`); cross-project SA emails won't match in-store index, edge skipped. Non-SA member kinds (user, group, domain, allUsers, allAuthenticatedUsers) need Entra-equivalent identity scanner — defer until lands rather than synthesizing principal resources.

## API-not-enabled → service-disabled sentinel

When API not enabled, scanners propagate sentinel error; dispatch loop renders `(service disabled)` on per-service progress line — no warning. Mechanism:

- `isAPINotEnabled(err)` (in `gcp.go`) matches three known shapes: 403 message `"has not been used in project"`, 400 message `"has not enabled"` (BigQuery), `googleapi.Error.Errors[].Reason == "accessNotConfigured"`. Extend this predicate (not `isPermissionDenied`) when adding scanners surfacing API-not-enabled differently.
- `skipIfDenied` returns `markServiceDisabled(err)` when `isAPINotEnabled` matches, else records `ScanWarning`, returns nil. Existing call sites (`if isPermissionDenied(err) { return ..., skipIfDenied(...) }`) bubble sentinel up — no per-scanner-file edits needed.
- `scanProject` detects sentinel via `errors.Is(err, errServiceDisabled)`, calls `st.ReportService(name, 0, 0, 0, true)` so `cmd/scan.go` renders `(service disabled)` suffix. Mirrors AWS pattern (`aws/aws.go` `errServiceDisabled` + `markServiceDisabled`).

Real IAM 403 (rare; caller lacks permission but API enabled) still goes to warnings via `skipIfDenied`'s second branch. Spanner billing-disabled (`"has billing disabled"`) intentionally out of scope — billing precondition, not API enablement; surfaces as warning.

## Permission-denied is non-fatal

`isPermissionDenied(err)` covers 401/403/BigQuery-400. Always pair with `skipIfDenied(st, "<api>:<method>", scope, err)` — never propagate from per-service scanner. Function dispatches internally between sentinel (API not enabled — see above) and warning (real permission denial).

## `forEachItem[T]` helper — bounded-concurrency fan-out

`forEachItem[T any](ctx, concurrency, items, fn)` in `gcp.go` runs `fn(gctx, item)` over each item with at most `concurrency` goroutines in flight. First non-nil err aborts siblings. Used by per-location / per-zone / per-SA / per-dataset fan-out (KMS, DNS record-sets, IAM-key, BigQuery). Replaces the `sem := semaphore.NewWeighted(...); g, gctx := errgroup.WithContext(...); for ... { g.Go(... sem.Acquire ... defer sem.Release ...) }; g.Wait()` setup repeated across four scanners.

Inner pagination + mutex still belong to the caller — `forEachItem` only owns the fan-out skeleton. Caller-owned: `apiDisabled atomic.Bool` short-circuit (KMS), `var mu sync.Mutex` over shared batch slice, per-call err classification.

## `runPaginated[P]` helper — preferred List driver

`runPaginated[P any](ctx, st, p, action, req, pageHandler)` in `gcp.go` wraps `req.Pages(ctx, fn)` with the boilerplate every scanner phase repeats: invoke `Pages`, accumulate `(total, inserted)` per page, classify final err via `isPermissionDenied` → `skipIfDenied` (which dispatches sentinel vs warning). Generic over the page type — works on every `google.golang.org/api/*` `*.List()` request struct (all expose `Pages(ctx, func(*P) error) error`).

Use it for every paginated phase. Page handler returns `(int, int, error)` — usually after building a `[]*store.Resource` and calling `upsertWithProjClosure`. Replaces ~150 LOC of repeated `err = req.Pages(...) {...}; if err != nil { if isPermissionDenied(err) { return ..., skipIfDenied(...) } return ..., err }` boilerplate.

Single-page `.Do()` calls (BigTable, Firestore, Spanner per-instance Databases.List) and inner `Pages` calls running inside `errgroup.WithContext` (BigQuery phase 2, DNS record-sets, KMS keyrings, IAM-key fan-out) keep manual classification — `runPaginated` doesn't fit those because the gctx/mu interactions are tied to the surrounding errgroup.

## Wildcard locations parent

For per-location APIs supporting it (`cloudfunctions/v2`, `run/v2`, future Pub/Sub regional, AI Platform), `parent = "projects/{p}/locations/-"` returns resources across every location in one paginated call. Prefer over per-location fan-out — API does per-location query in parallel server-side. Helper `locationFromResourceName` (in `serverless_scanners.go`) extracts location segment from returned resource names for per-resource `Region` field. Some legacy APIs (Cloud KMS, Certificate Manager) don't support `-`, require locations-list-then-fan-out pattern.

## Synthetic NativeIDs

Some GCP resources have no API-issued canonical name. Synthesize from parent resource path + natural key:
- `gcp:dns:record-set` → `{zoneNativeID}/rrsets/{type}/{name}` — `(name, type)` is natural key (one zone can have A + AAAA for same hostname).
- `gcp:iam:policy` → `{scope}/policy` — IAM policy is JSON returned by `GetIamPolicy`, not real resource.
Stable across rescans; matches synthetic-NativeID precedent in `internal/store/CLAUDE.md`.

## Shared LB upsert helper

`upsertWithProjClosure(p, st, batch)` in `loadbalancing_scanners.go` factors out upsert + `BatchAddToHierarchyClosure` pair-fanout to `projParentID`. Reuse from any scanner whose resources hang directly off project (no intermediate parent). When parent is else (e.g. record-set → managed-zone), build closure pairs inline.

## AggregatedList scope-key parsing

GCP Compute `*.AggregatedList` returns `map[string]ScopedList` where keys are either `"global"` or `"regions/{region}"`. Helper `scopedListRegion(scope)` in `loadbalancing_scanners.go` extracts region segment (returns "" for global). Reuse for any new AggregatedList consumer.

## API-not-enabled fan-out short-circuit

Scanners fanning out over global locations/regions catalog before hitting API-gated endpoint (KMS keyrings, future Pub/Sub regional, Cloud Run regional) still benefit from per-project `atomic.Bool` short-circuit even though sentinel mechanism above already suppresses warning storm. Reason: each goroutine still issues API call before returning sentinel. Flipping bool on first 403 lets remaining goroutines exit without network round-trip. Precedent: `kms_scanners.go` `apiDisabled`.

## Resource ID conventions

NativeID = full resource name where GCP returns one (`sa.Name`, `inst.SelfLink`, `projects/{id}` for projects, `organizations/{id}` for orgs). Compute uses self-link URLs verbatim — include project/region/zone, so same instance scanned in two projects produces two distinct rows. For hierarchy parent of any project-scoped resource use `store.ResourceID("gcp", p.ID, TypeProject, p.ID)` — project's NativeID is bare ID, not `projects/{id}` form.