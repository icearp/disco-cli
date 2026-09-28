# CLAUDE.md — `internal/providers/azure/`

Azure scanner conventions. Cross-provider rules: see `../CLAUDE.md`.

## Discover what's not yet covered

Coverage commands and buckets: `internal/coverage/CLAUDE.md`. Azure specifics: ARM `Providers/List` never enumerates proxy child types (`microsoft.sql/managedinstances/keys`, `…/virtualnetworks/subnets`), so they read `candidate-only` under `--cross-check` — the SDK, not the registry, is the truth. Entra identities (Graph, not an ARM RP) are `disco-only: explained: non-sdk`.

## Adding a new type — 3 spots

1. `azure_types.go`: `Type*` const (`azure:<namespace>:<kebab collection>[:<child>]`; the pairing test only needs the scanner to call the `arm*` list op).
2. `azure_scanner_test.go`: append service name to `expectedAzureServices`.
3. New `<svc>_scanners.go` self-registers the service via `init() { registerService(serviceEntry{name, fn}) }` and declares each type it upserts via `registerType(restype.Descriptor{...})` in the same `init()`. Resolvers via `registerResolver(fn)` from `<svc>_resolvers.go`.

Types are declared via `registerType` (see `../CLAUDE.md`). Azure delta: `Managed` is used only by `TypeNetworkCloudRackSKU`; built-in role/policy/set definitions stay scanner-set (managed only for tenant built-ins). Guards: `TestNoDoubleDeclaredTypes`, `azure_pairing_test.go`.

## Service names align to the ARM namespace: `azure:microsoft.<namespace>`

`serviceEntry.name` (the `--services` selector + scan-progress label) is always
`azure:microsoft.<arm-namespace>` — the `azure:` provider prefix (room for future
`azure:<vendor>.*` 3rd-party RPs) plus the ARM namespace the service emits (the `Service` value in
its `emits`, e.g. `microsoft.documentdb` — NOT the friendly `cosmos`). One registration **per
namespace**: the registry panics on duplicate names (`azure_registry.go`).
- If several scanners share a namespace (e.g. dns/frontdoor/private-endpoints are all
  `microsoft.network`), **merge** them under one `serviceEntry` whose `fn` runs each via
  `azRunPhases(...)` and whose `emits` is the union — secondary files keep their `scanX` fn and
  declare their types with `registerType` instead of a second `registerService`. Precedent:
  `network_scanners.go::scanNetworkNamespace`, `cosmos_scanners.go::scanDocumentDBNamespace`.
- If one scanner spans two namespaces, **split** it into one registration each. Precedent:
  `containerapps_scanners.go` (`microsoft.app` + `microsoft.containerinstance`).
Tenant-scope `microsoft.entra` (Graph) is the lone non-ARM-RP exception, registered via
`registerTenantService` and excluded from `expectedAzureServices`.

Dispatch-level reporting must use those registered names too — `entraServiceName` and
`resourcesServiceName` in `azure_scanner.go`, never the ad-hoc `"entra"` / `"resourcegroups"`
they replaced. A label no service is registered under joins to no declared type, so the failure
it records explains nothing. A failure of one subscription's whole scan is
`subscriptionScanService` (`scan:subscription`), deliberately not the runner's provider-wide
`scan`: with the bare label every declared type, tenant-scoped ones included, was blamed on one
unreachable subscription.

Warnings carry their service without knowing it: the dispatch loop passes
`st.WithWarningService(svc.name)`, so a scanner keeps reporting op labels
(`armnetwork:VirtualWans.List`) and the registered name rides along. Nothing turns an `arm<module>`
prefix into `microsoft.<ns>` — armappservice, armcosmos and armresources each disagree — so that
field is the only join a consumer has.

`enumerateSubscriptions` fails with `ErrNoSubscriptions` when ARM lists none, like the pin and
config paths. Returning an empty slice produced a completed scan with no rows, no error and no
warning, so a credential pointed at the wrong tenant was indistinguishable from an empty one.

## Resolver-edge metadata: `EdgeDecl`

`registerResolver(fn, emits ...EdgeDecl)` is variadic — every resolver lists each
`(Source, Target, Kind)` triple it upserts (`EdgeDecl{Source: TypeX, Target: TypeY, Kind: store.RelUses}`).
Source = the disco type whose `.ID` is the edge's from_id (the resolver's iteration type);
Target = the type the edge points at; Kind = a `store.Rel*` constant. Audit + coverage tooling
(`disco coverage resolvers [--missing] --providers azure`) reads this metadata, so an unannotated
resolver is invisible to gap analysis. Cross-cutting central resolvers whose source is *every*
resource type (`resolveManagedIdentityConsumers`, `resolveExtendedLocationConsumers`) stay
unannotated **on purpose** — per-type Source enumeration is meaningless and would pollute the
`--missing` per-service inventory; they carry a comment saying so. There is no leaf flag: a type
leaves `--missing` when a resolver's `EdgeDecl` names it.

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
- `nativeIDIndex(sub, st, rtype)` — lowercased `NativeID → resource-ID` index for one type. Use when a reference field carries a full ARM resource ID (case-insensitive), not a name or URI. Precedent: `batch_resolvers.go` (auto-storage + key-vault refs), `machinelearning_resolvers.go` (storage/keyvault/acr).
- `upsertVNetAttachment(st, fromID, subnetID, vnetByID)` — resolve a subnet ARM ID to its parent VNet (via `vnetIDFromSubnetID`) and emit `from -[attached-to]-> VNet` when in scope. `vnetByID` is a lowercased VNet `nativeIDIndex`. Shared by resolvers carrying VNet-injection subnet refs (kusto, appplatform, hdinsight). Lives in `kusto_resolvers.go`.
- `vaultNameFromKeyURI(s)` — parse full Key Vault key URI (`https://v.vault.azure.net/keys/k/v`). Used by ACR / Cosmos / MySQL CMEK.
- `vaultNameFromVaultURI(s)` — parse vault DNS root (`https://v.vault.azure.net/`). Used by Event Hubs / Service Bus CMEK. **Pick right one per service** — wrong choice silently produces zero edges.
- `skipIfAccessDenied(st, svc, sub.ID, err)` — records a warning, returns nil; special-cases only `isProviderUnavailable`. The 401≡403 skip lives in `isSkippableScanError`, which callers check first (exceptions: `grep -n isAccessDenied internal/providers/azure/*.go`); an unguarded error hits the dispatcher's `default:` → `ScanError`.
- `subscriptionUnreachable(probeErr, rgListed)` — refuses a whole subscription only when the providers probe is 401 **and** the RG list failed (a listed RG proves the token works). One `ScanError` via `unreachableSubscriptionError`, carrying the ARM code, never the body (it names disco's issuer and lands on the customer's record); disco-saas reads that partial-with-no-rows scan as unhealthy (rows stamped with the Entra directory's guid don't count, so a Graph-consented workspace can't read healthy for a refused subscription). This is the only place a 401 changes what is scanned rather than how it is reported. In-scan retry is futile: `newCachingCredential` has no invalidation path (azcore `Expire()` clears only the policy copy). `scanrun.maxPersistedWarnings` (200) is a cap, not a call count.
- `azClientOptions` — shared `*arm.ClientOptions` with retry tuned for ARM throttling. Pass to every arm* `NewXClient(...)`.

## ARM IDs are case-insensitive

Azure stores IDs as-returned (whatever case user typed at create time). Resolvers matching scope/principal/target IDs against local resources MUST build `strings.ToLower`-keyed index and lowercase input before lookup. Precedent: `authorization_resolvers.go`, `privateendpoints_resolvers.go`, `containerapps_resolvers.go`.

Helpers extracting segments from ARM IDs (subscription guid, RG name, resource name) must return the *lowercased* segment, not the original mixed-case slice — caller-side `strings.EqualFold` works but each call site is easy to miss. Precedent: `subscriptionFromScope` in `authorization_resolvers.go`.

## Azure can return malformed IDs

Some list results carry an `id` with empty segments, e.g. `azureFirewallFqdnTags` and `expressRouteServiceProviders` return `/subscriptions//resourceGroups//providers/Microsoft.Network/<type>/` for every item. Stored raw, the list collapses onto one NativeID and each scan writes the items as successive versions of one resource. `azTrackedRows` runs every ID through `repairARMID`, which rebuilds `/subscriptions/<sub>/providers/<ns>/<type>/<name>`. A scanner building rows by hand must call it too. Symptom: a fresh `--db` scan that reports `changed` > 0.

## PE target IDs carry sub-resource suffixes

`privateLinkServiceConnections[].privateLinkServiceId` often points at sub-path (e.g. `/storageAccounts/foo/blobServices/default`) not stored resource. Resolver pattern: progressively trim `/`-segments from right until one matches index. See `privateendpoints_resolvers.go::resolvePrivateEndpointRelationships`.

## Built-in role/policy definitions are deduplicated under the tenant account

Built-in role definitions, built-in policy definitions, and built-in policy set definitions are tenant-identical Microsoft-shipped resources. The tenant service `scanAuthorizationBuiltins` fetches them once per scan (`RoleDefinitions.List` with `$filter=type eq 'BuiltInRole'`, iterating subscriptions until one is authorized; `armpolicy` `NewListBuiltInPager` for policy/set defs) and stores them under the tenant GUID. The per-sub scanners (`scanRoleDefinitionsInto`, `scanPolicy`) skip built-ins when `sub.tenantID != ""` — role: `RoleType=="BuiltInRole"`; policy: **only** `PolicyTypeBuiltIn` (Static/NotSpecified are NOT returned by `ListBuiltIn`, so they stay per-sub). Custom definitions always stay per-sub.

Role-definition ARM IDs are returned scope-prefixed (`/subscriptions/{sub}/...`); the tenant copy is stored with a scope-stripped NativeID (`roleDefSuffix`) and resolvers join via the scope-independent `normalizeRoleDefKey` (matches custom + built-in uniformly). Built-in policy-definition IDs are already scope-free, stored verbatim. `resolveAuthorizationRelationships` builds its role-def index over sub.ID + tenantID accounts (`buildRoleDefIndex`); `resolvePolicyRelationships` merges tenant-account built-in policy/set defs into its per-sub index. When `tenantID` is empty (resolution failed), all of this degrades to per-sub storage with no data loss. Custom role/policy definitions and role/policy **assignments** are genuinely per-sub and never deduplicated.

## Microsoft Graph (Entra ID) via raw REST + azcore token

Tenant-scope identity scanners hit Graph v1.0 (`https://graph.microsoft.com/v1.0/{users,groups,servicePrincipals,applications}`) directly through the in-package `graphClient` — a thin `*http.Client` + token-issuer pair that issues bearer tokens via `cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{graphScope}, TenantID: g.tenantID})`, where an empty `tenantID` means the credential's own directory. Its `*http.Client` is `graphHTTPClient`, which refuses redirects — NOT the shared `azHTTPClient`. The official `msgraph-sdk-go` (kiota-generated) was dropped — its 88-subpkg discriminator-driven model graph cost ~9 MB symbols + matching rodata to call four list endpoints whose JSON shape `userAttrs`/`groupAttrs`/`spAttrs`/`appAttrs` already model 1:1. Pagination is the OData `@odata.nextLink` chain via the generic `iterateGraph[T]` helper. Tenant ID still resolved by issuing a token and parsing the `tid` claim from the JWT (`tenantIDFromCredScopeTenant`; there is no `tenantIDFromCred` any more) — `azidentity` exposes no tenant getter, and reading the tid back is also what proves a federated Graph token came from the directory that was asked for. Permission failures surface as `ScanWarning` (Authorization_RequestDenied / Insufficient privileges / 401 / 403); other errors as `ScanError` — except that the error types this package MINTS against a Graph response never reach that substring test at all, see `neverAConsentFailure`. The `*Attrs` JSON-tag set must keep matching Graph's response keys — same struct doubles as the unmarshal target, so a tag drift silently zeros the field. Tests inject an httptest server URL via the `graphClient.baseURL` seam plus a `tokenIssuer`-implementing stub. Precedent: `entra_scanners.go`.

## API-driven cross-cutting resolvers

Resolvers needing API access (not just DB reads) register via `registerAPIResolver(apiResolverEntry{name, fn})` in `azure_registry.go` — fn signature is `func(ctx, sub, cred, st) (edges int, err error)`. Runs after phase-1 services complete and BEFORE the local-only `registeredResolvers`, so `st.ListResources` returns the full resource set. Errors degrade to `ReportError` + `ReportService(errCount=1)` — never propagate. Per-resource fan-out should bound concurrency via `semaphore.NewWeighted(maxConcurrentFanout)`. Precedent: `monitor_resolvers.go` (diagnostic-settings).

Cross-cutting resolvers iterate diagnosable resources via an explicit type allowlist (`diagnosableTypes` in `monitor_resolvers.go`) — calling Microsoft.Insights APIs on non-diagnosable types returns 404/400 per call. Extend the allowlist when new scanners land for diagnosable types; consult learn.microsoft.com/azure/azure-monitor/essentials/resource-logs-categories for the master list.

### `armmonitor` must stay >= v0.13.0

Keep `armmonitor` ≥ v0.13.0. v0.12.0's TypeSpec regen dropped the `2021-05-01-preview` Diagnostic Settings API (`NewListPager`/`Delete`) that `monitor_resolvers.go` needs. On a bump, check the generated api-version comment, not just the build.

## Identity → MSI edges centralized

`managedidentity_resolvers.go::resolveManagedIdentityConsumers` walks every Azure resource's `identity.userAssignedIdentities` map. New scanners storing native SDK responses verbatim get MSI-consumer edges automatically — do NOT add per-service identity-map resolvers.

## Federated credential (WIF) and tenant scope

`wif.go`: `DISCO_AZURE_WIF_CLIENT_ID` + `_TENANT_ID` → `sts:GetWebIdentityToken` → `azidentity.NewClientAssertionCredential` (distroless Fargate; the default chain fails there). `SigningAlgorithm` must be `RS256` (Entra accepts nothing else; fails only at exchange). `retryCredential` retries `AADSTS70021:` — keep the colon, or it also retries the permanent `AADSTS700213`.

Invariants (each was a shipped defect):
- **Tenant phase is suppressed under federation by default — correctness, not permissions.** Under Lighthouse the token authority is disco's tenant, so tenant-scope APIs *succeed* and answer about disco's directory. Gate = `tenantServiceRunnable` (`tenant_scanners.go`), asked by both `runTenantServices` and `reportTenantScopeSkipped`; a new tenant service is refused unless `graphScoped`. It keys on `wifConfig.configured()`, so it also fires for own-tenant federation (documented capability loss — say so in help text; never claim a topology in a scan message, Solution Rule 9). Besides the registered tenant services, `tenantDisplayName` and `tenantIDFromCredScope` are tenant-scope too. `stitchTopHierarchy` takes the whole `wifConfig`, skips only the Entities call, and keys on `tenantScopeEnabled` directly.
- **Tenant-wide is decided by URL, not phase.** Re-derive the set from ARM URL templates with no `{subscriptionId}`/`{scope}`. `GET /subscriptions`: `enumerateScope` refuses under federation (subscriptions must be named; an invisible pin warns) and `scanSubscriptionResource` filters the page to the scanned subscription — unfiltered it wrote other customers' subscriptions under this customer's account. `azure_coverage.go` still uses `DefaultAzureCredential`; moving it to `newAzureCredential` needs the same filter. `managed:true` is no containment for a misattributed tenant-wide row.
- Under federation `Scan` leaves `subscription.tenantID` empty (the `tid` names disco's tenant; a value makes per-sub scanners skip built-in role/policy defs).
- Positive binding: `bindSubscriptions` refuses unless ARM reports every scanned subscription reachable and owned by `DISCO_AZURE_SUBSCRIPTION_TENANT_ID`; `scanEntra` stores nothing when the Graph token's `tid` ≠ the configured directory. The proof is the token's `tid`, never the variable.
- `partiallyConfigured` refuses a half-set contract — the WIF pair (a silent fallback would re-enable the tenant phase via `tenantScopeEnabled`), and `DISCO_AZURE_GRAPH_TENANT_ID` alone (which would otherwise *enable* every tenant service against an ambient credential). `DISCO_AZURE_SUBSCRIPTION_TENANT_ID` is the one uncounted variable (stated in `ErrIncompleteWIFConfig`). `TestIncompleteWIFConfig_NamesEveryCountedVariable` reflects over `wifConfig` fields.
- `DISCO_AZURE_GRAPH_TENANT_ID` (GUID only; `graphTenantGUID` rejects `common`/`organizations`) reopens **graphScoped services only** and needs both halves: `credentialOptions` allow-lists exactly that tenant (never `"*"`), and `graphClient` threads it as `TokenRequestOptions.TenantID`. Never admit ARM tenant phases (`TestTenantServiceRunnable_GraphConsentUngatesGraphAlone`).

### Credential-error redaction
`redactCredentialError` (first branch of `formatAzureError`) keys on error TYPE and HTTP **401**, never a code list: every `*azcore.ResponseError` 401 renders `azure token rejected for this scope ({code}); see scanner logs`, body dropped (it names disco's tenant/role); code via `armAuthCode` (AADSTS from message, else ARM code). A 403 (`AuthorizationFailed`) is never redacted (`TestRedactCredentialError_DoesNotRedactAnOrdinaryARMError`). Graph arm keys on `graphErr.status == 401` (`TestReportEntraErr_KeepsAConsentDenialReadable`); it is live because `graphErr` has no `Unwrap`. `scanBodyForAADSTS` is the fallback for untyped MSAL/STS errors — a false-positive gate, not a cost gate; its `*azcore.ResponseError` 401 disjunct is dead (the first net returns earlier) but kept as the predicate's contract. Never trigger on the `azure wif:` prefix (config refusals must stay readable).

### Graph transport
- `graphHTTPClient` refuses every redirect (it shares the ARM pool's transport, not its `http.Client`) and `get` refuses a 3xx status explicitly (`ErrUseLastResponse` returns a decodable body). net/http would forward `Authorization` to subdomains and across https→http. `azHTTPClient` (ARM) still follows redirects — known, out-of-scope gap.
- `@odata.nextLink` must pass `sameGraphHost`: scheme + parsed host + port vs `baseURL` (scheme compared to base, so httptest needs no escape hatch), via `asciiHostEqual` (not `EqualFold`: U+017F folds to `s`).
- **Never classify text the remote side can write** (URLs, hosts, `Location` headers, IPv6 zones). Refusals are typed (`foreignLinkError`, `oversizePageError`, `tooManyPagesError`, …; messages via `hostOnly`) and listed in `neverAConsentFailure`; `reportEntraErr` resolves type *before* `formatAzureError`/substring matching (else an `AADSTS…` host is rewritten as a credential failure and lands in `loggedCredentialErrors`); status comes from `graphErr.status`, not `" 403"` in the body. A new error type minted against a Graph response must join `neverAConsentFailure` — nothing enforces it.
- Bounds: `maxGraphErrorBody` (at read); `sanitizeForScanRecord` chokepoint (`reportEntra`, `reportPanic`); `maxGraphPageBody` 64 MiB via `*io.LimitedReader` seeded at cap+1 (`N==0` ⇒ over cap); `maxGraphPages` (a `var` only for tests; `iterateGraph`'s cycle-key size is an open gap). The repeated-link guard catches exact repeats only; the general bound is `serviceTimeout` in `runTenantServices` (cancel deferred in a wrapper — a test service panics on purpose).

### Skip reporting
Suppressed tenant phase = one notice per service + ONE phase warning. Notices are constants chosen by kind (ARM `armSkipNotice` / `dedupOnly` `dedupSkipNotice` / Graph) and, for Graph, by state via `graphSkipNotice(wif)` (`graphSkipNoticeUnnamed`, `graphSkipNoticeMalformed`); loss notices carry `directoryLossPrefix`, the dedup one must not. The warning's advice comes from `graphTenantAdvice` (consented → none; malformed → name the shape; unset → name the variable; shared text `graphWhichDirectory`), each clause gated on `skippedARM`/`skippedGraph`. Re-derive the set from the `const` block, not a count here.

### Testing customer-visible security text
- Assert on the rendered surface (`warnings[0].Message`), not a helper's return — deleting the call site is otherwise green.
- Comparing to the emitting constant pins the branch, not the content; add a positive wording assertion.
- For an instruction with an argument (which audience), assert the label↔verb *pairing*, in either order.
- Absences: ban verbs (`confirm`/`verif`/`prove`) and bound the lead's bytes (≤140); word bans lose to paraphrase.
- Shared strings must be self-contained; re-read every caller's assembled output.
- A guard whose removal is silent gets a source-level test (`TestLoadSubscriptions_CallsTheBinding`, `go/ast`); a fail-loud joining (`newFederatedCredential` options) may stay untested with a site comment.

## Subscription-scoped vs tenant-scoped

Per-sub scanners run via `scanSubscription`; tenant services via `registerTenantService` (`tenant_scanners.go`), once per scan through `runTenantServices`, concurrently with the per-sub fan-out. Only phase-2 resolvers wait (`waitForTenant`); a tenant service a phase-1 scanner needs would require widening that gate. Tenant rows are stored under `subscription.tenantID` (the ARM token `tid`; empty under federation, and `DISCO_AZURE_GRAPH_TENANT_ID` does not fill it). Precedents: `scanManagementTenant` (management groups), `scanAuthorizationBuiltins` (built-in role/policy/set definitions). An empty `tenantID` falls back to per-sub storage stamped with the first subscription's id (mislabel, icearp/disco-cli#12). Hybrid: a tenant-wide ARM call may run inside `scanSubscription` only if its response is filtered to the scanned subscription (precedent `scanSubscriptionResource`); `ResourceID` includes the account id, so per-sub duplicates resolve within their own account. AccessDenied is tolerated via `skipIfAccessDenied` for callers without tenant-level RBAC.

## Generic helpers split by concern

Cross-service helpers live one-per-file under the `azure_` prefix: `azure_scan_helpers.go` (`azPageScan`, `azSimpleScan`, `azTrackedRows`, `azPager`, `azRGFanoutScan`, `listSubscriptionRGNames`, `isResourceGroupNotFound`), `azure_armid.go` (`rgFromID`, `rgNameFromID`, `nameFromID`, `truncateAtSegment`, `vnetIDFromSubnetID`), `azure_tags.go` (`azTagsJSON`), `azure_errors.go` (`isAccessDenied`, `isAuthenticationFailure`, `isFeatureNotAvailable`, `skipIfAccessDenied`, `formatAzureError`, `subscriptionUnreachable`, `unreachableSubscriptionError`), `azure_concurrency.go` (`maxConcurrentFanout`), `azure_scanner.go` (`Scanner`, `Scan`, `subscription`, `azClientOptions`, `mustJSON`/`sv`/`tp`/`regionGlobal`, function-app sidecar). Per-service code stays in `<svc>_scanners.go` / `<svc>_resolvers.go`. Mirror the AWS / GCP layout when adding a new generic concern.

## SDK pointer-element types

`pageItems` returns `[]*T` where T is SDK's per-resource struct, often *not* obvious singular of client name. Common patterns: `armredis.ResourceInfo` (not `Cache`), `armservicebus.SBNamespace`, `armeventhub.EHNamespace`, `armapimanagement.ServiceResource`, `armmsi.Identity`, `armcosmos.DatabaseAccountGetResults`, `armcompute.SSHPublicKeyResource`. Grep SDK's `models.go` or build-and-fix when uncertain.

## Service quotas: scope-addressed fan-out + limit-only versioning

`quota_scanners.go` is the lone scanner that talks to a *unified proxy RP*
(`Microsoft.Quota` via `armquota`) instead of a per-service list. The proxy is
scope-addressed — `NewListPager(scope)` where
`scope = /subscriptions/{sub}/providers/{RP}/locations/{loc}` — so it fans out the
cartesian product of `quotaProviderNamespaces × azureregions.Regions`, bounded by
`maxConcurrentFanout` (same errgroup+semaphore shape as `azRGFanoutScan`). Any
(namespace, region) the proxy doesn't serve returns an `isSkippableScanError` and
is dropped; only a genuine error aborts. Stored **limit-only** (the Quota API
returns no usage and the serialized `CurrentQuotaLimitBase` omits
`ProxyResource`/`SystemData`, so no etag/timestamp; `armquota.Properties` holds
only Limit, Name, ResourceType, Unit, QuotaPeriod and IsQuotaApplicable), which
makes each quota churn-free — the version chain bumps only on a real limit
change. `disco history <id>` reads that chain (see `cmd/CLAUDE.md`). When adding
another quota-bearing namespace, extend `quotaProviderNamespaces` — nothing else.

**Quotas are NOT resources and register no type.** `scanQuotaLimits` writes
`store.Quota` rows into the `quotas` table (disco migration 017) via
`UpsertQuotas`; `TestQuotaLimitsDeclareNoResourceType` fails if a `registerType`
comes back. The service registration must survive alongside the
absent type — dropping that stops quotas being scanned at all. Identity is
`(provider, subscription, region, namespace, quota name)`, where the quota name
is the resource provider's own `Properties.Name.Value` (e.g.
`standardDDv4Family`), **not** the ARM wrapper name and **not** the ARM ID —
which is preserved in the attributes remainder. `IsQuotaApplicable` maps to the
`adjustable` column. This scanner is not opt-in, unlike AWS's, so every Azure
scan records quotas — and Azure *resource* counts dropped when they moved out.

## Top three hierarchy tiers are stitched post-scan, not per-scanner

`management-group → subscription → resource-group` can't be wired by any single
per-subscription scanner: the three tiers are stored by different phases under
different accounts (MGs under the tenant account in the tenant phase; the
subscription-as-resource and RGs per-sub). `stitchTopHierarchy` (`management_scanners.go`)
runs ONCE from `Scan` after `wg.Wait()` — the only point where all endpoints are
committed, so `RecordHierarchyBatch` emits the graph-visible `contains` row instead of
gating it out. It looks targets up in store-built lowercased `NativeID → ResourceID`
indexes (`storeNativeIDIndex`), never recomputing `store.ResourceID`, so a casing diff
between APIs can't desync the hash. The RG→subscription tier is pure store data (links
even without tenant Management read); the MG→MG and subscription→MG tiers need the
tenant-wide `armmanagementgroups.EntitiesClient.NewListPager` (the flat
`Client.NewListPager` carries no parent), whose AccessDenied is tolerated. New top-level
container types that nest above the RG join here, not in a per-sub scanner.

## RG hierarchy pairs are mandatory

Every RG-scoped resource must emit `rgHierarchyPair` (resource → RG closure). `azSimpleScan` does this automatically. When hand-rolling callback, do not omit pairs — peers without pairs were oversights, not design.

## Scanner-level tests via SDK fake transport

Each `arm*` module ships generated `fake.<Type>Server` (e.g. `armcomputefake.DisksServer`) plus `NewXServerTransport`. Pattern: split scanner into `scanX(ctx, sub, cred, st, scanID)` (production wrapper) + `scanXWithClient(ctx, sub, st, scanID, client)` (testable body). Test constructs client via `armcompute.NewDisksClient(subID, fakeCred(), fakeClientOptions(t, transport))` and calls the body directly. Helpers in `azure_testhelpers_test.go`: `fakeCred()` returns `*azfake.TokenCredential{}`, `fakeClientOptions` collapses retries (MaxRetries=0). Precedent: `compute_disks_scanners_test.go` covers happy path, multi-page pagination, 403 AccessDenied.

For error injection use `azfake.PagerResponder.AddResponseError(http.StatusForbidden, "AuthorizationFailed")` — produces an `azcore.ResponseError` the `isAccessDenied` check recognises.

## Error formatting — always `formatAzureError`

`azcore.ResponseError.Error()` dumps the request line (method, scheme, host, escaped path — no headers, no query) plus the response status and the full ARM error body — multi-KB per warning. It renders no part of the REQUEST beyond that line — no headers, so no `Authorization: Bearer`; no request body, so no client-assertion JWT — which is why neither can reach the store or stderr through this path. **Never** pass `err.Error()` directly into `store.ScanWarning.Message` / `store.ScanError.Message`. Use `formatAzureError(err)` (in `azure_errors.go`) — narrows to `"{statusCode} {errorCode}: {message}"` matching AWS/GCP brevity. Falls back to `err.Error()` for non-`*azcore.ResponseError` (store / JSON / I/O errors), so it's safe at every site — **except** that it collapses a CREDENTIAL failure to its diagnostic code first, ahead of every other branch, because that text names disco's own tenant and AWS role rather than anything the customer scanned (`redactCredentialError`). **A 401 does NOT render in that shape** and has not since the ARM-token-rejection redaction: it becomes `azure token rejected for this scope ({code}); see scanner logs`, with the body dropped. Call sites: `grep -n 'formatAzureError(' internal/providers/azure/*.go | grep -v -e '_test\.go' -e 'func formatAzureError'`.

## Cross-check keys are not candidate keys (#123)

`RegistryKey` rebuilds the ARM type name from a candidate key: it drops a singleton instance id
(`blobservices/default/containers` → `blobServices/containers`) and restores the `locations`
segment the extractor strips as a scope pair, which `azureinventory` flags with
`scope-pair:locations` (set only when *every* lister reaches the collection through a location —
a sibling `ListBySubscription` proves ARM keeps no `locations` in the type). Without both, 34
keys could never match `Providers/List` and each one produced a false drift row on both sides.
`--cross-check` needs a live subscription, so this is verified against the key shapes, not against
ARM.
