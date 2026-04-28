# CLAUDE.md — `internal/providers/gcp/`

GCP-specific scanner / resolver conventions. Cross-provider rules: `internal/providers/CLAUDE.md`.

## Service registration

Each `<svc>_scanners.go` file calls `registerService` from `init()`. Service `fn` runs once per project — fan-out across projects + concurrency cap (`maxConcurrentServices = 10`) handled by `scanProject`. Resolvers register via `registerResolver(fn)`; resolver fn is called once per project after all phase-1 scans land.

## Scopes above project (org / folder)

Per-project service entries can't reach folder / organization scopes — those need either (a) a single-pass scanner that runs once per scan (sibling to `scanHierarchy`), or (b) a synthetic per-project entry that filters to `p.ParentID`. No fan-out helper exists yet — first follow-up to need it (folder/org IAM policies, org policies, asset inventory at folder scope) should add one to `gcp.go`.

## IAM policy resource shape

`gcp:iam:policy` is a synthesized resource — IAM policies are not first-class GCP resources but JSON blobs returned by `GetIamPolicy` on the scope they protect. NativeID `projects/{id}/policy` (and `folders/{id}/policy` / `organizations/{id}/policy` when those land). Bindings are stored verbatim under the resource's `attributes` JSON; resolvers parse `bindings[].members[]` and emit edges typed `RelUses` with `{role: roles/...}` in edge attrs.

Member matching: only `serviceAccount:{email}` members are FK-safe today. Email parsed from existing `gcp:iam:service-account` resource NativeIDs (`projects/{id}/serviceAccounts/{email}`); cross-project SA emails won't match the in-store index and the edge is skipped. Non-SA member kinds (user, group, domain, allUsers, allAuthenticatedUsers) require an Entra-equivalent identity scanner — defer until that lands rather than synthesizing principal resources.

## Permission-denied is non-fatal

`isPermissionDenied(err)` covers 401/403. Always pair with `skipIfDenied(st, "<api>:<method>", scope, err)` which calls `ReportWarning` + returns nil — never propagate 403 from a per-service scanner. Compute / GKE / IAM all enforce per-API enablement; users with disabled APIs see warnings, not failures.

## Wildcard locations parent

For per-location APIs that support it (`cloudfunctions/v2`, `run/v2`, future Pub/Sub regional, AI Platform), `parent = "projects/{p}/locations/-"` returns resources across every location in one paginated call. Prefer this over per-location fan-out — the API does the per-location query in parallel server-side. Helper `locationFromResourceName` (in `serverless_scanners.go`) extracts the location segment from the returned resource names for the per-resource `Region` field. Some legacy APIs (Cloud KMS, Certificate Manager) don't support `-` and require the locations-list-then-fan-out pattern instead.

## Synthetic NativeIDs

Some GCP resources have no API-issued canonical name. Synthesize from the parent resource path + a natural key:
- `gcp:dns:record-set` → `{zoneNativeID}/rrsets/{type}/{name}` — `(name, type)` is the natural key (one zone can have A + AAAA for the same hostname).
- `gcp:iam:policy` → `{scope}/policy` — IAM policy is JSON returned by `GetIamPolicy`, not a real resource.
Stable across rescans; matches the synthetic-NativeID precedent in `internal/store/CLAUDE.md`.

## Shared LB upsert helper

`upsertWithProjClosure(p, st, batch)` in `loadbalancing_scanners.go` factors out the upsert + `BatchAddToHierarchyClosure` pair-fanout to `projParentID`. Reuse it from any scanner whose resources hang directly off the project (no intermediate parent). When the parent is something else (e.g. record-set → managed-zone), build the closure pairs inline instead.

## AggregatedList scope-key parsing

GCP Compute `*.AggregatedList` returns `map[string]ScopedList` where keys are either `"global"` or `"regions/{region}"`. Helper `scopedListRegion(scope)` in `loadbalancing_scanners.go` extracts the region segment (returns "" for global). Reuse for any new AggregatedList consumer.

## API-not-enabled noise dedup

Some GCP scanners fan out over the global locations / regions catalog before hitting an API-gated endpoint (KMS keyrings, future Pub/Sub regional, Cloud Run regional). When the API is disabled in the project, every fan-out unit returns 403, producing ~30 identical "API has not been used" warnings per project. Pattern: per-project `atomic.Bool`, flip on first 403 via `Swap(true)`, skip remaining units. Precedent: `kms_scanners.go` `apiDisabled`. Reuse for any future scanner with this fan-out shape.

## Resource ID conventions

NativeID = full resource name where GCP returns one (`sa.Name`, `inst.SelfLink`, `projects/{id}` for projects, `organizations/{id}` for orgs). Compute uses self-link URLs verbatim — they include project / region / zone, so the same instance scanned in two projects produces two distinct rows. For the hierarchy parent of any project-scoped resource use `store.ResourceID("gcp", p.ID, TypeProject, p.ID)` — the project's NativeID is the bare ID, not the `projects/{id}` form.
