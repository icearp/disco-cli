# CLAUDE.md — `internal/providers/azure/`

Azure scanner conventions. Cross-provider rules: see `../CLAUDE.md`.

## Discover what's not yet covered

`disco coverage --provider azure --filter uncovered` — diff ARM Providers/List vs scanner `emits` decls (deduped via the alias map in `azure_coverage.go`, which inverts `azureAPITypeMap`). Other filters: `covered`, `synthetic`, `upstream-missing`. `--check-strict` exits non-zero on any `upstream-missing` (alias-map drift). Subscription auto-detected; pass `--subscription` to override.

## Adding a new type — 3 spots

1. `azure_types.go`: `Type*` const **and** entry in `azureAPITypeMap` (lowercase ARM key like `microsoft.foo/bars`).
2. `azure_scanner_test.go`: append service name to `expectedAzureServices`.
3. New `<svc>_scanners.go` self-registers via `init() { registerService(serviceEntry{name, fn}) }`. Resolvers via `registerResolver(fn)` from `<svc>_resolvers.go`.

## Helpers (reuse before reinventing)

- `azPageScan(ctx, action, sub, st, pager, toResources)` — paginate + upsert + hierarchy + AccessDenied skip in one call. Returns `(total, inserted, err)`. **Non-paginator single-call APIs** (e.g. `armsecurity.PricingsClient.List`): unwrap `azcore.ResponseError` manually for 401/403 → `skipIfAccessDenied`; precedent: `security_scanners.go`.
- `azSimpleScan[T,P](ctx, action, rtype, sub, st, scanID, pager, pageItems, extract)` — wraps `azPageScan` for dominant pattern: sub-scoped list → tracked Resource + RG hierarchy pair. Extractor returns `azTrackedBase{id, name, location, tags, full}`. Each scanner ~12 LOC: client construct + one call + extractor. Always emits RG pair when ID has `/resourceGroups/` segment — prefer over hand-rolled `azPageScan` callbacks. Precedent: `keyvault_scanners.go`, `network_scanners.go` (multi-phase).
- `sqlChildScan[C,T](ctx, label, rtype, sub, st, scanID, srv, pager, pageItems, extract)` — body for SQL-server child scanners. Tolerates AccessDenied + FeatureNotAvailable (break loop, no error). Extractor returns `sqlChildExtract` via `sqlProxyExtract(id, name)` for proxy resources or `sqlTrackedExtract(id, name, location, tags)` for tracked types (ElasticPool, FailoverGroup, JobAgent, RestorableDroppedDB). Precedent: `sql_server_child_scanners.go`.
- **Multi-type single service** pattern (precedent: `network_scanners.go`): one `serviceEntry` runs N concurrent phases via `sync.WaitGroup` + mutex-aggregated counts. Per-type `*ToBase` extractors feed `azSimpleScan` / `azRGFanoutScan` / `azTrackedRows` with the shared `(ID, Name, Location, Tags)` shape. Use when several SDK types in the same ARM namespace logical area (e.g. core networking + WAN) belong together — keeps the registry small and `--services <name>` selects the whole area.
- `azRGFanoutScan[T,P](ctx, action, rtype, sub, cred, st, scanID, pagerFn, pageItems, extract)` — for ARM resource types with NO subscription-wide list API (only per-RG endpoints). Enumerates RGs via `listSubscriptionRGNames` (ARM `armresources.ResourceGroupsClient`), fans out per-RG list calls bounded by `maxConcurrentFanout`, batches all results, single upsert + closure. Per-RG AccessDenied + 404 (RG vanished mid-scan) tolerated. Same extract shape as `azSimpleScan`. Precedent: classic `VirtualNetworkGateways` in `network_scanners.go`. Use for Front Door endpoints, ADF linked services, Logic Apps API connections, etc.
- `rgHierarchyPair(sub, type, nativeID)` — RG closure pair (resource → RG).
- `vnetIDFromSubnetID(s)` — strip `/subnets/X` suffix to recover parent VNet ARM ID.
- `nameFromID(id)` — last `/`-segment of ARM ID. Builds name-keyed indexes (vault-name, registry-name). NOTE: preserves case — lowercase the result when building a lookup key.
- `vaultNameIndex(sub, st)` — lowercased `vault-name → resource-ID` index of the sub's Key Vaults. Shared by every CMK resolver mapping a key/vault URI back to a vault (cognitiveservices / appconfiguration / recoveryservices / ACR / Cosmos / network). Reuse — do not re-inline the list+loop.
- `nativeIDIndex(sub, st, rtype)` — lowercased `NativeID → resource-ID` index for one type. Use when a reference field carries a full ARM resource ID (case-insensitive), not a name or URI. Precedent: `batch_resolvers.go` (auto-storage + key-vault refs).
- `vaultNameFromKeyURI(s)` — parse full Key Vault key URI (`https://v.vault.azure.net/keys/k/v`). Used by ACR / Cosmos / MySQL CMEK.
- `vaultNameFromVaultURI(s)` — parse vault DNS root (`https://v.vault.azure.net/`). Used by Event Hubs / Service Bus CMEK. **Pick right one per service** — wrong choice silently produces zero edges.
- `skipIfAccessDenied(st, svc, sub.ID, err)` — log scan warning, continue (returns nil).
- `azClientOptions` — shared `*arm.ClientOptions` with retry tuned for ARM throttling. Pass to every arm* `NewXClient(...)`.

## ARM IDs are case-insensitive

Azure stores IDs as-returned (whatever case user typed at create time). Resolvers matching scope/principal/target IDs against local resources MUST build `strings.ToLower`-keyed index and lowercase input before lookup. Precedent: `authorization_resolvers.go`, `privateendpoints_resolvers.go`, `containerapps_resolvers.go`.

Helpers extracting segments from ARM IDs (subscription guid, RG name, resource name) must return the *lowercased* segment, not the original mixed-case slice — caller-side `strings.EqualFold` works but each call site is easy to miss. Precedent: `subscriptionFromScope` in `authorization_resolvers.go`.

## PE target IDs carry sub-resource suffixes

`privateLinkServiceConnections[].privateLinkServiceId` often points at sub-path (e.g. `/storageAccounts/foo/blobServices/default`) not stored resource. Resolver pattern: progressively trim `/`-segments from right until one matches index. See `privateendpoints_resolvers.go::resolvePrivateEndpointRelationships`.

## Built-in role definitions duplicate per subscription

`armauthorization.RoleDefinitionsClient.List(scope=sub)` returns built-ins with sub prefix rewritten in. Each sub gets own copy — accepted because `ResourceID` hash includes account_id, so per-sub resolvers FK-match locally. Same logic applies to anything tenant-scoped that per-sub API surfaces.

## Microsoft Graph (Entra ID) via raw REST + azcore token

Tenant-scope identity scanners hit Graph v1.0 (`https://graph.microsoft.com/v1.0/{users,groups,servicePrincipals,applications}`) directly through the in-package `graphClient` — a thin `*http.Client` + token-issuer pair that issues bearer tokens via `cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{graphScope}})`. The official `msgraph-sdk-go` (kiota-generated) was dropped — its 88-subpkg discriminator-driven model graph cost ~9 MB symbols + matching rodata to call four list endpoints whose JSON shape `userAttrs`/`groupAttrs`/`spAttrs`/`appAttrs` already model 1:1. Pagination is the OData `@odata.nextLink` chain via the generic `iterateGraph[T]` helper. Tenant ID still resolved by issuing a Graph token and parsing the `tid` claim from the JWT (`tenantIDFromCred`) — `azidentity` exposes no tenant getter. Permission failures surface as `ScanWarning` (Authorization_RequestDenied / Insufficient privileges / 401 / 403); other errors as `ScanError`. The `*Attrs` JSON-tag set must keep matching Graph's response keys — same struct doubles as the unmarshal target, so a tag drift silently zeros the field. Tests inject an httptest server URL via the `graphClient.baseURL` seam plus a `tokenIssuer`-implementing stub. Precedent: `entra_scanners.go`.

## API-driven cross-cutting resolvers

Resolvers needing API access (not just DB reads) register via `registerAPIResolver(apiResolverEntry{name, fn})` in `azure_registry.go` — fn signature is `func(ctx, sub, cred, st) (edges int, err error)`. Runs after phase-1 services complete and BEFORE the local-only `registeredResolvers`, so `st.ListResources` returns the full resource set. Errors degrade to `ReportError` + `ReportService(errCount=1)` — never propagate. Per-resource fan-out should bound concurrency via `semaphore.NewWeighted(maxConcurrentFanout)`. Precedent: `monitor_resolvers.go` (diagnostic-settings).

Cross-cutting resolvers iterate diagnosable resources via an explicit type allowlist (`diagnosableTypes` in `monitor_resolvers.go`) — calling Microsoft.Insights APIs on non-diagnosable types returns 404/400 per call. Extend the allowlist when new scanners land for diagnosable types; consult learn.microsoft.com/azure/azure-monitor/essentials/resource-logs-categories for the master list.

## Identity → MSI edges centralized

`managedidentity_resolvers.go::resolveManagedIdentityConsumers` walks every Azure resource's `identity.userAssignedIdentities` map. New scanners storing native SDK responses verbatim get MSI-consumer edges automatically — do NOT add per-service identity-map resolvers.

## Subscription-scoped vs tenant-scoped

Every per-sub scanner runs via `scanSubscription`. Tenant-scope services (Entra ID via Microsoft Graph SDK, etc.) register via `registerTenantService(tenantServiceEntry{...})` in `tenant_scanners.go` — fn fires ONCE per scan, before per-sub fan-out, receives `[]subscription` + cred. Dispatch via `runTenantServices` in `azure.go`. **Hybrid pattern** (precedent: `management_scanners.go`): tenant-scoped ARM API (e.g. `armmanagementgroups`, `armsubscription`) can run *inside* `scanSubscription` if you accept per-sub duplication — `ResourceID` hash includes account_id so dedup works locally. Same trick as RBAC built-in role-definitions. AccessDenied tolerated via `skipIfAccessDenied` for callers without tenant-level RBAC.

## Generic helpers split by concern

Cross-service helpers live one-per-file under the `azure_` prefix: `azure_scan_helpers.go` (`azPageScan`, `azSimpleScan`, `azTrackedRows`, `azPager`, `azRGFanoutScan`, `listSubscriptionRGNames`, `isResourceGroupNotFound`), `azure_armid.go` (`rgFromID`, `rgNameFromID`, `nameFromID`, `truncateAtSegment`, `vnetIDFromSubnetID`), `azure_tags.go` (`azTagsJSON`), `azure_errors.go` (`isAccessDenied`, `isFeatureNotAvailable`, `skipIfAccessDenied`, `formatAzureError`), `azure_concurrency.go` (`maxConcurrentFanout`), `azure_scanner.go` (`Scanner`, `Scan`, `subscription`, `azClientOptions`, `mustJSON`/`sv`/`tp`/`regionGlobal`, function-app sidecar). Per-service code stays in `<svc>_scanners.go` / `<svc>_resolvers.go`. Mirror the AWS / GCP layout when adding a new generic concern.

## SDK pointer-element types

`pageItems` returns `[]*T` where T is SDK's per-resource struct, often *not* obvious singular of client name. Common patterns: `armredis.ResourceInfo` (not `Cache`), `armservicebus.SBNamespace`, `armeventhub.EHNamespace`, `armapimanagement.ServiceResource`, `armmsi.Identity`, `armcosmos.DatabaseAccountGetResults`, `armcompute.SSHPublicKeyResource`. Grep SDK's `models.go` or build-and-fix when uncertain.

## RG hierarchy pairs are mandatory

Every RG-scoped resource must emit `rgHierarchyPair` (resource → RG closure). `azSimpleScan` does this automatically. When hand-rolling callback, do not omit pairs — peers without pairs were oversights, not design.

## Scanner-level tests via SDK fake transport

Each `arm*` module ships generated `fake.<Type>Server` (e.g. `armcomputefake.DisksServer`) plus `NewXServerTransport`. Pattern: split scanner into `scanX(ctx, sub, cred, st, scanID)` (production wrapper) + `scanXWithClient(ctx, sub, st, scanID, client)` (testable body). Test constructs client via `armcompute.NewDisksClient(subID, fakeCred(), fakeClientOptions(t, transport))` and calls the body directly. Helpers in `fake_testhelper_test.go`: `fakeCred()` returns `*azfake.TokenCredential{}`, `fakeClientOptions` collapses retries (MaxRetries=0). Precedent: `compute_disks_scanners_test.go` covers happy path, multi-page pagination, 403 AccessDenied.

For error injection use `azfake.PagerResponder.AddResponseError(http.StatusForbidden, "AuthorizationFailed")` — produces an `azcore.ResponseError` the `isAccessDenied` check recognises.

## Error formatting — always `formatAzureError`

`azcore.ResponseError.Error()` dumps the entire HTTP request+response (preamble, headers, full ARM error body) — multi-KB per warning. **Never** pass `err.Error()` directly into `store.ScanWarning.Message` / `store.ScanError.Message`. Use `formatAzureError(err)` (in `azure.go`) — narrows to `"{statusCode} {errorCode}: {message}"` matching AWS/GCP brevity. Falls back to `err.Error()` for non-`*azcore.ResponseError` (store / JSON / I/O errors), so it's safe at every site. Existing call sites: `skipIfAccessDenied`, `runTenantServices`, `runAPIResolvers` dispatch, `reportEntraErr` + Entra storage-error sites.

## Lint gotchas

- `forvar` (Go 1.22+): drop `i, x := i, x` shadows in goroutines — per-iteration scope built-in.
- Project gofmt config rewrites `init()` one-liners to multiline; run `gofmt -w .` before each commit to avoid post-commit linter drift.