# CLAUDE.md — `internal/providers/azure/`

Azure scanner conventions. Cross-provider rules: see `../CLAUDE.md`.

## Discover what's not yet covered

`go run . types azure --filter uncovered` — diff Azure provider registry vs `azureAPITypeMap`. Pick next scanner. Other filters: `--filter covered`, `--services microsoft.compute,microsoft.network`, `--output json`.

## Adding a new type — 3 spots

1. `types.go`: `Type*` const **and** entry in `azureAPITypeMap` (lowercase ARM key like `microsoft.foo/bars`).
2. `registration_test.go`: append service name to `expectedAzureServices`.
3. New `<svc>_scanners.go` self-registers via `init() { registerService(serviceEntry{name, fn}) }`. Resolvers via `registerResolver(fn)` from `<svc>_resolvers.go`.

## Helpers (reuse before reinventing)

- `azPageScan(ctx, action, sub, st, pager, toResources)` — paginate + upsert + hierarchy + AccessDenied skip in one call. Returns `(total, inserted, err)`. **Non-paginator single-call APIs** (e.g. `armsecurity.PricingsClient.List`): unwrap `azcore.ResponseError` manually for 401/403 → `skipIfAccessDenied`; precedent: `security_scanners.go`.
- `azSimpleScan[T,P](ctx, action, rtype, sub, st, scanID, pager, pageItems, extract)` — wraps `azPageScan` for dominant pattern: sub-scoped list → tracked Resource + RG hierarchy pair. Extractor returns `azTrackedBase{id, name, location, tags, full}`. Each scanner ~12 LOC: client construct + one call + extractor. Always emits RG pair when ID has `/resourceGroups/` segment — prefer over hand-rolled `azPageScan` callbacks. Precedent: `keyvault_scanners.go`, `wan_scanners.go` (multi-phase).
- `sqlChildScan[C,T](ctx, label, rtype, sub, st, scanID, srv, pager, pageItems, extract)` — body for SQL-server child scanners. Tolerates AccessDenied + FeatureNotAvailable (break loop, no error). Extractor returns `sqlChildExtract` via `sqlProxyExtract(id, name)` for proxy resources or `sqlTrackedExtract(id, name, location, tags)` for tracked types (ElasticPool, FailoverGroup, JobAgent, RestorableDroppedDB). Precedent: `sql_server_child_scanners.go`.
- **Multi-type single service** pattern (precedent: `wan_scanners.go`): one `serviceEntry` runs N sequential phases. Generic `wanRows[T]` + per-type `*ToBase` extractors share single batch-build + hierarchy-pair path, each scanner ~100 LOC. Use when several SDK types share `(ID, Name, Location, Tags)` shape in same logical area (e.g. enterprise networking).
- `azRGFanoutScan[T,P](ctx, action, rtype, sub, cred, st, scanID, pagerFn, pageItems, extract)` — for ARM resource types with NO subscription-wide list API (only per-RG endpoints). Enumerates RGs via `listSubscriptionRGNames` (ARM `armresources.ResourceGroupsClient`), fans out per-RG list calls bounded by `maxConcurrentFanout`, batches all results, single upsert + closure. Per-RG AccessDenied + 404 (RG vanished mid-scan) tolerated. Same extract shape as `azSimpleScan`. Precedent: classic `VirtualNetworkGateways` in `wan_scanners.go`. Use for Front Door endpoints, ADF linked services, Logic Apps API connections, etc.
- `rgHierarchyPair(sub, type, nativeID)` — RG closure pair (resource → RG).
- `vnetIDFromSubnetID(s)` — strip `/subnets/X` suffix to recover parent VNet ARM ID.
- `nameFromID(id)` — last `/`-segment of ARM ID. Builds name-keyed indexes (vault-name, registry-name).
- `vaultNameFromKeyURI(s)` — parse full Key Vault key URI (`https://v.vault.azure.net/keys/k/v`). Used by ACR / Cosmos / MySQL CMEK.
- `vaultNameFromVaultURI(s)` — parse vault DNS root (`https://v.vault.azure.net/`). Used by Event Hubs / Service Bus CMEK. **Pick right one per service** — wrong choice silently produces zero edges.
- `skipIfAccessDenied(st, svc, sub.ID, err)` — log scan warning, continue (returns nil).
- `azClientOptions` — shared `*arm.ClientOptions` with retry tuned for ARM throttling. Pass to every arm* `NewXClient(...)`.

## ARM IDs are case-insensitive

Azure stores IDs as-returned (whatever case user typed at create time). Resolvers matching scope/principal/target IDs against local resources MUST build `strings.ToLower`-keyed index and lowercase input before lookup. Precedent: `authorization_resolvers.go`, `privateendpoints_resolvers.go`, `containerapps_resolvers.go`.

## PE target IDs carry sub-resource suffixes

`privateLinkServiceConnections[].privateLinkServiceId` often points at sub-path (e.g. `/storageAccounts/foo/blobServices/default`) not stored resource. Resolver pattern: progressively trim `/`-segments from right until one matches index. See `privateendpoints_resolvers.go::resolvePrivateEndpointRelationships`.

## Built-in role definitions duplicate per subscription

`armauthorization.RoleDefinitionsClient.List(scope=sub)` returns built-ins with sub prefix rewritten in. Each sub gets own copy — accepted because `ResourceID` hash includes account_id, so per-sub resolvers FK-match locally. Same logic applies to anything tenant-scoped that per-sub API surfaces.

## Identity → MSI edges centralized

`managedidentity_resolvers.go::resolveManagedIdentityConsumers` walks every Azure resource's `identity.userAssignedIdentities` map. New scanners storing native SDK responses verbatim get MSI-consumer edges automatically — do NOT add per-service identity-map resolvers.

## Subscription-scoped vs tenant-scoped

Every current scanner runs per-subscription via `scanSubscription`. Truly tenant-scoped services (Entra ID via Microsoft Graph SDK) need separate top-level scanner. **Hybrid pattern** (precedent: `management_scanners.go`): tenant-scoped ARM API (e.g. `armmanagementgroups`, `armsubscription`) can run *inside* `scanSubscription` if you accept per-sub duplication — `ResourceID` hash includes account_id so dedup works locally. Same trick as RBAC built-in role-definitions. AccessDenied tolerated via `skipIfAccessDenied` for callers without tenant-level RBAC.

## Shared helpers live in `azure.go`

Cross-service helpers (`azPageScan`, `azSimpleScan`, `azTrackedRows`, `azTagsJSON`, `rgFromID`, `nameFromID`, `azPager`, `azClientOptions`) live in `azure.go`. Do not create new top-level files like `helpers.go` or `<concept>_scanner.go` — append to `azure.go`. Per-service code stays in `<svc>_scanners.go` / `<svc>_resolvers.go`.

## SDK pointer-element types

`pageItems` returns `[]*T` where T is SDK's per-resource struct, often *not* obvious singular of client name. Common patterns: `armredis.ResourceInfo` (not `Cache`), `armservicebus.SBNamespace`, `armeventhub.EHNamespace`, `armapimanagement.ServiceResource`, `armmsi.Identity`, `armcosmos.DatabaseAccountGetResults`, `armcompute.SSHPublicKeyResource`. Grep SDK's `models.go` or build-and-fix when uncertain.

## RG hierarchy pairs are mandatory

Every RG-scoped resource must emit `rgHierarchyPair` (resource → RG closure). `azSimpleScan` does this automatically. When hand-rolling callback, do not omit pairs — peers without pairs were oversights, not design.

## Lint gotchas

- `forvar` (Go 1.22+): drop `i, x := i, x` shadows in goroutines — per-iteration scope built-in.
- Project gofmt config rewrites `init()` one-liners to multiline; run `gofmt -w .` before each commit to avoid post-commit linter drift.