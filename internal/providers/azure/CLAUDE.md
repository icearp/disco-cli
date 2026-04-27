# CLAUDE.md — `internal/providers/azure/`

Azure-specific scanner conventions. Cross-provider rules: see `../CLAUDE.md`.

## Adding a new type — 3 spots

1. `types.go`: `Type*` const **and** entry in `azureAPITypeMap` (lowercase ARM key like `microsoft.foo/bars`).
2. `registration_test.go`: append service name to `expectedAzureServices`.
3. New `<svc>_scanners.go` self-registers via `init() { registerService(serviceEntry{name, fn}) }`. Resolvers via `registerResolver(fn)` from `<svc>_resolvers.go`.

## Helpers (reuse before reinventing)

- `azPageScan(ctx, action, sub, st, pager, toResources)` — paginate + upsert + hierarchy + AccessDenied skip in one call. Returns `(total, inserted, err)`.
- `rgHierarchyPair(sub, type, nativeID)` — RG closure pair (resource → RG).
- `vnetIDFromSubnetID(s)` — strip `/subnets/X` suffix to recover parent VNet ARM ID.
- `nameFromID(id)` — last `/`-segment of an ARM ID. Used to build name-keyed indexes (vault-name, registry-name).
- `vaultNameFromKeyURI(s)` — parse full Key Vault key URI (`https://v.vault.azure.net/keys/k/v`). Used by ACR / Cosmos / MySQL CMEK.
- `vaultNameFromVaultURI(s)` — parse vault DNS root (`https://v.vault.azure.net/`). Used by Event Hubs / Service Bus CMEK. **Pick the right one per service** — wrong choice silently produces zero edges.
- `skipIfAccessDenied(st, svc, sub.ID, err)` — log scan warning, continue (returns nil).
- `azClientOptions` — shared `*arm.ClientOptions` with retry tuned for ARM throttling. Pass to every arm* `NewXClient(...)`.

## ARM IDs are case-insensitive

Azure stores IDs as-returned (whatever case the user typed at create time). Resolvers matching scope/principal/target IDs against local resources MUST build a `strings.ToLower`-keyed index and lowercase the input before lookup. Precedent: `authorization_resolvers.go`, `privateendpoints_resolvers.go`, `containerapps_resolvers.go`.

## PE target IDs carry sub-resource suffixes

`privateLinkServiceConnections[].privateLinkServiceId` often points at a sub-path (e.g. `/storageAccounts/foo/blobServices/default`) not a stored resource. Resolver pattern: progressively trim `/`-segments from the right until one matches the index. See `privateendpoints_resolvers.go::resolvePrivateEndpointRelationships`.

## Built-in role definitions duplicate per subscription

`armauthorization.RoleDefinitionsClient.List(scope=sub)` returns built-ins with the sub prefix rewritten in. Each sub gets its own copy — accepted because `ResourceID` hash includes account_id, so per-sub resolvers FK-match locally. Same logic applies to anything tenant-scoped that the per-sub API surfaces.

## Identity → MSI edges centralized

`managedidentity_resolvers.go::resolveManagedIdentityConsumers` walks every Azure resource's `identity.userAssignedIdentities` map. New scanners that store native SDK responses verbatim get MSI-consumer edges automatically — do NOT add per-service identity-map resolvers.

## Subscription-scoped vs tenant-scoped

Every current scanner runs per-subscription via `scanSubscription`. Tenant-scoped services (Entra ID via Microsoft Graph SDK, Management Groups via `armmanagementgroups`) need a separate top-level scanner — don't shoehorn into `scanSubscription`.

## Lint gotchas

- `forvar` (Go 1.22+): drop `i, x := i, x` shadows in goroutines — per-iteration scope is built-in.
- Project gofmt config rewrites `init()` one-liners to multiline; run `gofmt -w .` before each commit to avoid post-commit linter drift.
