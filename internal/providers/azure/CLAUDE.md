# CLAUDE.md — `internal/providers/azure/`

Azure scanner conventions. Cross-provider rules: see `../CLAUDE.md`.

## Discover what's not yet covered

`disco coverage services --providers azure --filter uncovered` — diff ARM Providers/List vs scanner `emits` decls (deduped via the alias map in `azure_coverage.go`, which inverts `azureAPITypeMap`). Other filters: `covered`, `uncatalogued`, `upstream-missing`. `--check-strict` exits non-zero on any `upstream-missing` (alias-map drift). Subscription auto-detected; pass `--subscriptions` to override.

**ARM `Providers/List` never enumerates proxy child types.** Deep child/proxy resourceTypes (e.g. `microsoft.sql/managedinstances/keys`, `…/managedinstances/databases/transparentdataencryption`, `microsoft.network/virtualnetworks/subnets`, `microsoft.sql/servers/devopsauditsettings`) are real, scannable ARM resources but absent from the registry view, so they'd false-flag as `upstream-missing`. Their emit decls carry `Uncatalogued: true` to bucket them as `uncatalogued` instead (they are all SDK-scanned). (ARM is inconsistent — it *does* list some children like `managedinstances/vulnerabilityassessments`, which stay non-uncatalogued/covered; only flag the ones a live run shows missing.) Entra identities (Graph, not an ARM RP) are `Uncatalogued` for the same reason.

**A clean `--check-strict` needs the preview RPs registered.** Three top-level RPs disco scans are absent until registered in the coverage subscription: `Microsoft.AzureLargeInstance`, `Microsoft.HardwareSecurityModules`, `Microsoft.OnlineExperimentation`. These are listable once registered; run `az provider register --namespace <RP>` for each so they bucket as `covered`.

## Adding a new type — 3 spots

1. `azure_types.go`: `Type*` const **and** entry in `azureAPITypeMap` (lowercase ARM key like `microsoft.foo/bars`).
2. `azure_scanner_test.go`: append service name to `expectedAzureServices`.
3. New `<svc>_scanners.go` self-registers the service via `init() { registerService(serviceEntry{name, fn}) }` and declares each type it upserts via `registerType(restype.Descriptor{...})` in the same `init()`. Resolvers via `registerResolver(fn)` from `<svc>_resolvers.go`.

### Unified per-type declaration via `registerType`

`registerType(restype.Descriptor{...})` in `azure_registry.go` is the single-site
declaration for coverage emit (`Service` + `Leaf`/`Uncatalogued`), redaction
rules (`Redact`), and the unconditional `Managed` flag (the store stamps
`ManagedByProvider` by type — used only by `TypeNetworkCloudRackSKU`; the
built-in role/policy/set-definition types stay scanner-set because they are
managed only for tenant built-ins, not custom defs). It forwards field rules
into the shared redact/volatile/managed engines and routes the coverage decl
through `descriptorEmits`.

**Aliases are NOT on the descriptor.** Azure keeps them in `azureAPITypeMap`
(`azure_types.go`) because that map is both the alias source AND the
`azure_type_mirror_test.go` truth, and it carries *multiple* upstream keys per
disco type (which a single `Descriptor.Upstream` can't represent). `Aliases()`
inverts it as before — leave it alone.

**Azure is fully migrated** — every type is declared via `registerType`;
`azure_redact.go` and the legacy `serviceEntry.emits` / `registerExtraEmits`
paths are gone. `TestNoDoubleDeclaredTypes` guards against a type declared via
both paths; the mirror/leaf/redact tests guard naming and redaction.

## Service names align to the ARM namespace: `azure:microsoft.<namespace>`

`serviceEntry.name` (the `--services` selector + scan-progress label) is always
`azure:microsoft.<arm-namespace>` — the `azure:` provider prefix (room for future
`azure:<vendor>.*` 3rd-party RPs) plus the ARM namespace the service emits (the `Service` value in
its `emits`, e.g. `microsoft.documentdb` — NOT the friendly `cosmos`). One registration **per
namespace**: the registry panics on duplicate names (`azure_registry.go`).
- If several scanners share a namespace (e.g. dns/frontdoor/private-endpoints are all
  `microsoft.network`), **merge** them under one `serviceEntry` whose `fn` runs each via
  `azRunPhases(...)` and whose `emits` is the union — secondary files keep their `scanX` fn but
  declare emits via `registerExtraEmits(...)` instead of a second `registerService`. Precedent:
  `network_scanners.go::scanNetworkNamespace`, `cosmos_scanners.go::scanDocumentDBNamespace`.
- If one scanner spans two namespaces, **split** it into one registration each. Precedent:
  `containerapps_scanners.go` (`microsoft.app` + `microsoft.containerinstance`).
Tenant-scope `microsoft.entra` (Graph) is the lone non-ARM-RP exception, registered via
`registerTenantService` and excluded from `expectedAzureServices`.

## Resolver-edge metadata: `EdgeDecl`

`registerResolver(fn, emits ...EdgeDecl)` is variadic — every resolver lists each
`(Source, Target, Kind)` triple it upserts (`EdgeDecl{Source: TypeX, Target: TypeY, Kind: store.RelUses}`).
Source = the disco type whose `.ID` is the edge's from_id (the resolver's iteration type);
Target = the type the edge points at; Kind = a `store.Rel*` constant. Audit + coverage tooling
(`disco coverage resolvers [--missing] --providers azure`) reads this metadata, so an unannotated
resolver is invisible to gap analysis. Cross-cutting central resolvers whose source is *every*
resource type (`resolveManagedIdentityConsumers`, `resolveExtendedLocationConsumers`) stay
unannotated **on purpose** — per-type Source enumeration is meaningless and would pollute the
`--missing` per-service inventory; they carry a comment saying so. `TestLeafTypesNotResolverSources`
(`azure_coverage_test.go`) fails if a type flagged `Leaf: true` on its emits decl appears as an
`EdgeDecl.Source` — drop the Leaf flag in the same commit that ships the resolver.

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
- `skipIfAccessDenied(st, svc, sub.ID, err)` — log scan warning, continue (returns nil).
- `azClientOptions` — shared `*arm.ClientOptions` with retry tuned for ARM throttling. Pass to every arm* `NewXClient(...)`.

## ARM IDs are case-insensitive

Azure stores IDs as-returned (whatever case user typed at create time). Resolvers matching scope/principal/target IDs against local resources MUST build `strings.ToLower`-keyed index and lowercase input before lookup. Precedent: `authorization_resolvers.go`, `privateendpoints_resolvers.go`, `containerapps_resolvers.go`.

Helpers extracting segments from ARM IDs (subscription guid, RG name, resource name) must return the *lowercased* segment, not the original mixed-case slice — caller-side `strings.EqualFold` works but each call site is easy to miss. Precedent: `subscriptionFromScope` in `authorization_resolvers.go`.

## PE target IDs carry sub-resource suffixes

`privateLinkServiceConnections[].privateLinkServiceId` often points at sub-path (e.g. `/storageAccounts/foo/blobServices/default`) not stored resource. Resolver pattern: progressively trim `/`-segments from right until one matches index. See `privateendpoints_resolvers.go::resolvePrivateEndpointRelationships`.

## Built-in role/policy definitions are deduplicated under the tenant account

Built-in role definitions, built-in policy definitions, and built-in policy set definitions are tenant-identical Microsoft-shipped resources. The tenant service `scanAuthorizationBuiltins` fetches them once per scan (`RoleDefinitions.List` with `$filter=type eq 'BuiltInRole'`, iterating subscriptions until one is authorized; `armpolicy` `NewListBuiltInPager` for policy/set defs) and stores them under the tenant GUID. The per-sub scanners (`scanRoleDefinitionsInto`, `scanPolicy`) skip built-ins when `sub.tenantID != ""` — role: `RoleType=="BuiltInRole"`; policy: **only** `PolicyTypeBuiltIn` (Static/NotSpecified are NOT returned by `ListBuiltIn`, so they stay per-sub). Custom definitions always stay per-sub.

Role-definition ARM IDs are returned scope-prefixed (`/subscriptions/{sub}/...`); the tenant copy is stored with a scope-stripped NativeID (`roleDefSuffix`) and resolvers FK via the scope-independent `normalizeRoleDefKey` (matches custom + built-in uniformly). Built-in policy-definition IDs are already scope-free, stored verbatim. `resolveAuthorizationRelationships` builds its role-def index over sub.ID + tenantID accounts (`buildRoleDefIndex`); `resolvePolicyRelationships` merges tenant-account built-in policy/set defs into its per-sub index. When `tenantID` is empty (resolution failed), all of this degrades to per-sub storage with no data loss. Custom role/policy definitions and role/policy **assignments** are genuinely per-sub and never deduplicated.

## Microsoft Graph (Entra ID) via raw REST + azcore token

Tenant-scope identity scanners hit Graph v1.0 (`https://graph.microsoft.com/v1.0/{users,groups,servicePrincipals,applications}`) directly through the in-package `graphClient` — a thin `*http.Client` + token-issuer pair that issues bearer tokens via `cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{graphScope}, TenantID: g.tenantID})`, where an empty `tenantID` means the credential's own directory. Its `*http.Client` is `graphHTTPClient`, which refuses redirects — NOT the shared `azHTTPClient`. The official `msgraph-sdk-go` (kiota-generated) was dropped — its 88-subpkg discriminator-driven model graph cost ~9 MB symbols + matching rodata to call four list endpoints whose JSON shape `userAttrs`/`groupAttrs`/`spAttrs`/`appAttrs` already model 1:1. Pagination is the OData `@odata.nextLink` chain via the generic `iterateGraph[T]` helper. Tenant ID still resolved by issuing a token and parsing the `tid` claim from the JWT (`tenantIDFromCredScopeTenant`; there is no `tenantIDFromCred` any more) — `azidentity` exposes no tenant getter, and reading the tid back is also what proves a federated Graph token came from the directory that was asked for. Permission failures surface as `ScanWarning` (Authorization_RequestDenied / Insufficient privileges / 401 / 403); other errors as `ScanError` — except that the error types this package MINTS against a Graph response never reach that substring test at all, see `neverAConsentFailure`. The `*Attrs` JSON-tag set must keep matching Graph's response keys — same struct doubles as the unmarshal target, so a tag drift silently zeros the field. Tests inject an httptest server URL via the `graphClient.baseURL` seam plus a `tokenIssuer`-implementing stub. Precedent: `entra_scanners.go`.

## API-driven cross-cutting resolvers

Resolvers needing API access (not just DB reads) register via `registerAPIResolver(apiResolverEntry{name, fn})` in `azure_registry.go` — fn signature is `func(ctx, sub, cred, st) (edges int, err error)`. Runs after phase-1 services complete and BEFORE the local-only `registeredResolvers`, so `st.ListResources` returns the full resource set. Errors degrade to `ReportError` + `ReportService(errCount=1)` — never propagate. Per-resource fan-out should bound concurrency via `semaphore.NewWeighted(maxConcurrentFanout)`. Precedent: `monitor_resolvers.go` (diagnostic-settings).

Cross-cutting resolvers iterate diagnosable resources via an explicit type allowlist (`diagnosableTypes` in `monitor_resolvers.go`) — calling Microsoft.Insights APIs on non-diagnosable types returns 404/400 per call. Extend the allowlist when new scanners land for diagnosable types; consult learn.microsoft.com/azure/azure-monitor/essentials/resource-logs-categories for the master list.

### `armmonitor` must stay >= v0.13.0 (v0.12.0 is the broken one)

`monitor_resolvers.go` uses `armmonitor.NewDiagnosticSettingsClient` / `DiagnosticSettingsResource` — the modern resource-level Diagnostic Settings API (`Microsoft.Insights/diagnosticSettings`, api-version `2021-05-01-preview`): multiple named settings per resource, `List`, and destinations (storage / Event Hub / Log Analytics) the resolver walks to emit edges.

**v0.12.0 removed it.** Between v0.11.0 and v0.12.0 the package switched code generators from AutoRest (Swagger) to TypeSpec (`tsp-location.yaml` → `Microsoft.Insights/Insights`), and the TypeSpec port dropped the `2021-05-01-preview` api-version entirely. v0.12.0's `ServiceDiagnosticSettings` is NOT a successor — it's the *legacy* `2016-09-01` `/diagnosticSettings/service` singleton (no `List`, no `Delete`, one unnamed setting), a functional downgrade. The modern API is still live in Azure (documented on learn.microsoft.com under `rest-monitor-2021-05-01-preview`); there is no `armmonitor/v2` and no dedicated `armdiagnosticsettings`/`arminsights` module that exposes it.

**v0.13.0 restored it** (verified 2026-08-05 in the module cache: `diagnosticsettings_client.go` says `Generated from API version 2021-05-01-preview` and carries `NewListPager` + `Delete`), so the repo now runs v0.13.0 and the old v0.11.0 pin is gone. v0.12.0 remains the one bad release. Keep the floor at v0.13.0.

The trap this leaves behind is a reasoning one: a green build is what tells you a bump is safe here, because v0.12.0's replacement had *different type names* and would not compile. That will not hold for every future Azure bump — a regen that keeps the names and changes the semantics compiles fine. Check the generated api-version comment, not just the build.

## Identity → MSI edges centralized

`managedidentity_resolvers.go::resolveManagedIdentityConsumers` walks every Azure resource's `identity.userAssignedIdentities` map. New scanners storing native SDK responses verbatim get MSI-consumer edges automatically — do NOT add per-service identity-map resolvers.

## Federated credential, and why tenant scope switches off with it

`wif.go` swaps `DefaultAzureCredential` for an AWS-to-Entra exchange when
`DISCO_AZURE_WIF_CLIENT_ID` + `DISCO_AZURE_WIF_TENANT_ID` are set: `sts:GetWebIdentityToken`
signs a JWT asserting the caller's AWS identity, `azidentity.NewClientAssertionCredential`
presents it against a federated identity credential. Every link of the default chain fails in a
distroless Fargate task, which is what this exists for. **`SigningAlgorithm` is required and must
be `RS256`** — Entra supports nothing else for token exchange, AWS's own examples use ES384, and
the mismatch surfaces only at exchange time. `retryCredential` retries `AADSTS70021:` (matched
*with* the colon, or it would also match — and so keep retrying — the permanent `AADSTS700213`), which Microsoft documents as
an expected transient while a federated credential replicates.

**Under this credential the tenant phase is suppressed by default, and that is a correctness
requirement, not a permissions accommodation.** (Suppressed, not skipped wholesale — the Entra
services can be redirected to a named directory; see the `DISCO_AZURE_GRAPH_TENANT_ID` paragraphs
below, and read the rest of this paragraph as the default it changes.) Azure Lighthouse delegates SUBSCRIPTION scope: the
token's authority is *disco's* tenant and ARM resolves the delegation, so every tenant-scope API
answers about disco's own directory. Note the gate keys on `configured()` — "the WIF contract is
set" — not on "the identity is foreign to the scanned tenant", which nothing here can check. So it
also fires for a standalone operator federating into their OWN tenant, where the skip is a real
capability loss and is unnecessary. Deliberate: prose says so (`azure_scanner.go`'s LongDescription,
`cmd/config.go`) instead of the customer-visible message claiming a topology. Closing it wanted a
positive check rather than a knob, and the Graph half now has one — `scanEntra` refuses to store
anything when the token's `tid` is not the directory the scan was configured for. Every registered tenant service would have
(`grep -n 'registerTenantService' *.go`), plus `tenantDisplayName`, `tenantIDFromCredScope` and the
management-group Entities list in `stitchTopHierarchy` — a DIFFERENT call from
`scanManagementTenant`'s flat list, and it keys on `tenantScopeEnabled` DIRECTLY rather
than on the per-service gate below — correct, because it is an ARM call that no token can
redirect, but it does mean two functions now answer "is tenant scope open" for different
callers. All of them **succeed**
rather than 403, so the customer's inventory silently gains disco's users, groups, service
principals, applications and management groups, with no warning anywhere. The gate is `tenantServiceRunnable`
(`tenant_scanners.go`), asked per service by BOTH `runTenantServices` and
`reportTenantScopeSkipped` so the two can never disagree and no service falls through both. A tenant
service added later is refused under federation without being named — `graphScoped` is opt-in, and
the default is the suppression.

**That grep is not the whole set.** A call is tenant-wide because of its URL, not because of the
phase that registered it, so `GET /subscriptions` (covered by `enumerateScope` +
`subscriptionResourceBatch`) and armautomanage's `BestPractices.ListByTenant` (a tenant-ROOT path,
deliberately left ungated — it returns Microsoft's published catalog and discloses nothing) both sit
inside the per-SUBSCRIPTION fan-out. Re-derive the set from ARM URL templates carrying no
`{subscriptionId}`/`{scope}` segment, not from the registry.

Consequences to keep in step. `Scan` must also skip stamping `subscription.tenantID` — the
`tid` claim names disco's tenant, and a non-empty value makes the per-sub scanners skip built-in
role/policy definitions on the assumption a tenant service is storing them, which would silently
drop them from the scan. Leaving it empty selects the documented per-sub fallback: no data lost.
`stitchTopHierarchy` takes the whole `wifConfig` rather than a boolean — so the caller cannot hand
it a decision that disagrees with the rest of the scan — and skips only the Entities call while the
store-derived tiers still record.

And **the `GET /subscriptions` calls are tenant-wide while the gate covers only the tenant PHASE**
(`grep -n 'NewSubscriptionsClient' *.go` for the live set). `enumerateScope` refuses under
federation, so naming subscriptions — by `--subscriptions` or the config list — becomes mandatory
(and a pin the credential cannot see is warned about, not reported as an empty success);
`scanSubscriptionResource` filters the page to the subscription being scanned, and unfiltered it
wrote every delegated subscription, i.e. OTHER CUSTOMERS' ids and display names, under the current
customer's `AccountID`. `azure_coverage.go` keeps its own copy of this shape on
`DefaultAzureCredential`; it never touches the federated credential today, and pointing it at
`newAzureCredential` means giving it the same filter.

A **credential** failure is redacted before it reaches the scan record (`redactCredentialError`,
with the raw cause on stderr): the text carries disco's tenant GUID in the authority URL, and — when
the assertion callback fails, where there is no HTTP body at all — disco's AWS role ARN. It triggers
on the error TYPE, because the cases that leak most carry no `AADSTS` code. `scanBodyForAADSTS` is
the second, narrower net, for a credential failure arriving as some other type. It is NOT a cost
gate, though it reads like one: `runtime.NewResponseError` passes `respErr.Error()` to `log.Write`
as an argument, so an `*azcore.ResponseError` has already rendered itself and memoized the result
before disco sees it — there is no dump left to avoid. It is a FALSE-POSITIVE gate. Redaction throws
the message away, so an ARM failure that merely MENTIONS `AADSTS` would cost the customer the action
and scope they lack permission on. So an `*azcore.ResponseError` is answered from what ARM
classified it as, and only three shapes reach `err.Error()`: an `AADSTS` code in `ErrorCode`; a
**401**, which is ARM rejecting OUR token and carries the `AADSTS` text in the MESSAGE; or no parsed
code at all. The test is the STATUS, not a list of codes — a list is a hand-maintained allow-list on
a disclosure boundary and Microsoft adds codes, while `AuthorizationFailed`, the one error that must
never be redacted, is a 403.
`TestRedactCredentialError_DoesNotRedactAnOrdinaryARMError` pins that direction, since nothing else
can see it. Graph has its own arm on the same shape, keyed on `*graphErr.status == 401`, because
`reportEntraErr` routes through `formatAzureError`: without it a Graph **403** — whose body says
`Authorization_RequestDenied`, the customer's missing consent — fell to the bare substring fallback
and was collapsed if it mentioned `AADSTS` anywhere
(`TestReportEntraErr_KeepsAConsentDenialReadable`). It deliberately does NOT
trigger on the `azure wif:` prefix: every pre-exchange CONFIG refusal carries that too, and
collapsing those to "authentication failed" is exactly what the eager `AWS_REGION` check exists to
prevent — two guards in one change cancelling.

**The phase warning's trailing ADVICE clause varies by state (`graphTenantAdvice`), because the two
SUPPRESSED states fail closed identically and only the message can tell them apart** (the third is
not a fail-closed state at all — the services run, and the advice must be silent there): a consented
directory gets no advice at all (those services RAN and are not in the skipped list, so repeating
their remedy tells an operator to do what they have done), a value set but not a GUID names the
shape (otherwise it reads exactly like unset, and that is the one case where naming the shape is
the whole diagnosis), and unset names the variable. Each clause of that warning is gated on the KIND it explains
(`skippedARM` / `skippedGraph`), not on the wifConfig: `--services` can exclude either kind, so an
unconditional ARM justification explains a Graph-only list with a fact that the very next clause
contradicts, and unconditional advice tells an operator how to re-enable a service the run never
asked for.

The suppressed tenant phase reports **a notice per service plus ONE warning for the phase**. Both
halves of `store.ScanNotice`'s contract bind at once: coverage genuinely changed (so a notice alone
would sit outside the warning count, and `scanrun` persists warnings while discarding notices), but
this fires on every federated scan forever, and per-service fan-out would grow the count as tenant
services are added.

**The skip notices are constants and a selector, and they split on two axes.** By KIND: an ARM
phase offers no remedy through this credential because an ARM call names no directory, a `dedupOnly`
phase lost nothing at all (the per-subscription scanners store the same rows), and the Graph phase's
cause is actionable. Then the Graph one splits again by STATE, because suppression has two —
`graphSkipNoticeUnnamed` and `graphSkipNoticeMalformed`, picked by `graphSkipNotice(wif)` on the same
discriminator `graphTenantAdvice` uses for the phase warning. Both states fail closed identically, so
only the message distinguishes them, and the unnamed wording told an operator who HAD set the
variable that nothing was named — pointing them at a warning that said the opposite. Re-derive that
set from the `const` block; it has been "three notices" in this file while the code had four.

**A CONSTANT IS NOT A CONTENT ORACLE — this is the correction to what that paragraph used to say.**
A test comparing a message against the constant the production code emits pins which BRANCH ran and
says nothing about what the message SAYS, so rewriting that constant's VALUE into the forbidden
claim satisfies it. Measured: the assertion "a dedupOnly service is not told its results may describe
a different directory" was first written as a grep for a phrase, went vacuous when the phrase was
reworded, was "fixed" by comparing against `dedupSkipNotice` — and the mutant that gives
`dedupSkipNotice` the old directory-loss sentence PASSED. Three things settle it and the third is the
only one with content: a STRUCTURAL check derived from production (`directoryLossPrefix`, which every
loss notice carries and the dedup one must not), an identity check against the constant, and a
POSITIVE assertion on the wording itself. Positive is the only kind that goes red on a reword, and
going red on a reword is the point — the reword is when the claim needs re-reading.

**And a POSITIVE assertion is not automatically a content check — a list of tokens the message must
mention is a VOCABULARY check, and the two read identically.** The advice test asserted that the
operator guidance names `DISCO_AZURE_WIF_TENANT_ID`, "OWN tenant", "Lighthouse", "CUSTOMER" and
"inventory", which is every noun in the sentence and none of its polarity: replacing "and never
`DISCO_AZURE_WIF_TENANT_ID`" with "and usually" — inverting the one security instruction the message
carries, for the one value that passes every check in this package and discloses disco's own
directory — left all five present and the package green.

**Asserting the PREDICATE is not the fix either, and that correction was itself falsified in one
round.** `"never "+env` was added, and the next mutant SWAPPED the two branch labels — Lighthouse
gets "the same value as", the operator's own tenant gets "and never" — which keeps every token
including the predicate, still tells a Lighthouse MSP to point Graph at disco's own directory, and
was green package-wide. A security instruction has an ARGUMENT (here: which federation mode), and a
mutant moves the argument. **Assert the PAIRING**: split the string at the two audience labels and
check each clause carries its own verb and not the other's, in EITHER order — requiring one label to
come first reports a correct Lighthouse-first rewrite as a swap, and a guard that fails with the
wrong diagnosis gets suppressed rather than read.

**Then stop refining WHAT is asserted and ask WHICH VALUE — that escalation ran three rounds up a
dead axis.** grep, predicate, pairing are three sharpenings of one assertion against
`graphTenantAdvice`'s RETURN VALUE, and all three were made in tests that call the helper directly.
Nothing asserted the guidance against `warnings[0].Message`. So replacing the one call site,
`msg += graphTenantAdvice(wif)`, with a one-line "set the variable" sentence deleted the whole
which-directory guidance — prohibition included — from the only surface a customer sees, and was
green package-wide: **a test on a helper's return value cannot see the helper's call being deleted.**
Assert on the rendering the audience receives; a fragment-level pin is a supplement to that, never a
substitute. The tell that this gap exists is a doc comment saying "read the assembled string, do not
trust the suite" — that sentence names an assertion somebody chose not to write.

**An ABSENCE has no predicate, so a negative guard cannot be made structural the way a pairing can —
bound its SIZE instead.** Forbidding the words a retired claim used is a vocabulary check however it
is framed: "cannot be confirmed to be" was reworded to "may not belong to" and carried none of
`confirm`/`verif`/`prove`. What survives paraphrase is that a second causal clause has to be spelled
somehow and every spelling is long, so the warning lead's prose carries a measured byte bound (110
today, 140 allowed) alongside the verb list. Say in the comment which half is which; the framing is
what a later round trusts.

The same shape hides in a negative: a guard grepping the literal a previous round RETIRED is
satisfied by re-adding the same claim reworded, so pin the structural property instead. Pick that
property from the CLAIM, not from a word the claim happens to use — forbidding "directory" in the
warning lead failed twice over, since this codebase says "tenant" and "directory" interchangeably
(so the reworded re-add walked through) and the docs call the service "Entra ID directory objects"
(so the obvious next edit would have gone red naming something that had not happened). The verb is
what survives paraphrase: the lead is asserted to carry no "confirm"/"verif"/"prove".

**Sharing a string between two callers re-creates, on the caller that did not have it, whatever
defect the other caller's context was hiding.** `graphWhichDirectory` was extracted from the unset
arm and appended to the malformed one; it opened "WHICH directory that is", whose "that" resolved
against a preceding clause the unset arm supplied and the malformed arm did not — reintroducing on
one side the dangling reference an earlier round had fixed on the other. The inverse shipped in the
same edit: the anti-sufficiency clause stayed arm-local while the shared text's doc argued the
property for both, so the malformed state — whose operator is about to retype a value — was told
which directory to name and never what naming it buys. When text is shared, move the clauses that
make it self-contained INTO it, and re-read every caller's assembled output, not the arms.

A **half-set** `DISCO_AZURE_WIF_CLIENT_ID`/`_TENANT_ID` pair is refused (`partiallyConfigured`)
rather than falling back. `tenantScopeEnabled` reads `configured()` too, so a silent fall back would
re-enable the whole tenant phase and unpin enumeration off one typo'd variable name.

**`DISCO_AZURE_GRAPH_TENANT_ID` reopens the GRAPH half only, and it is two changes rather than
one — either alone is broken.** `azidentity`'s `resolveTenant` returns the credential's DEFAULT
tenant whenever a request specifies none, and ERRORS when a request names a tenant that is neither
the credential's own nor listed in `AdditionallyAllowedTenants`. So the variable sets both:
`credentialOptions` puts the named directory in the allow list (exactly one entry, never `"*"`) and
`graphClient` threads it as `policy.TokenRequestOptions.TenantID` on every Graph token. Threading
without the allow list fails every acquisition; allow-listing without the threading is the version
that was cut from the previous phase, which would ungate the scanners while every token still
targeted disco's tenant — the disclosure above, switched on by a variable promising the opposite.

**Only `graphScoped` services are reopened, and the asymmetry is not a conservatism.** A Graph token
can name the directory it is for; a tenant-root ARM call answers about whichever directory the
credential authenticated in and has no such parameter, so there is nothing to point and nothing to
check. Widening `tenantServiceRunnable` to admit the ARM phases is the way this becomes a
cross-customer disclosure again, which is what
`TestTenantServiceRunnable_GraphConsentUngatesGraphAlone` exists to catch.

**A `@odata.nextLink` is now checked against the configured Graph ORIGIN before it is followed.**
A nextLink is a fresh REQUEST, not a redirect, so net/http's rule about dropping `Authorization`
across hosts never applied to it — `graphClient.get` would present the bearer to whatever host the
response body named. Do not read that as "the redirect case was covered by net/http": it was not,
and it was worse — see the redirect paragraph below. That was true before this change; what changed is the token's value, now a
CUSTOMER's directory under `Directory.Read.All` rather than disco's own. `sameGraphHost` compares
scheme and parsed HOST against `baseURL`, never a string prefix
(`https://graph.microsoft.com.evil.example` carries the right prefix). Scheme is compared to base
rather than hardcoded to https so the httptest servers need no test-only escape hatch, and
production — where `baseURL` is a `const` https literal — still refuses a downgrade. The host
comparison is ASCII-only (`asciiHostEqual`), NOT `strings.EqualFold`: that applies Unicode simple
folding, where U+017F folds to `s`, and `url.Parse` passes any byte >= 0x80 into `Host`
unvalidated — so `EqualFold` answers true for `graph.microſoft.com`. The origin includes the PORT,
so a `:443`-qualified link against a bare-host base is refused; Graph emits none, and refusing is
the safe direction.

**The refusal is its own ERROR TYPE (`foreignLinkError`), and `reportEntraErr` tests that type
BEFORE its substring block.** The first version echoed the URL verbatim, so a nextLink of
`https://evil.example/Authorization_RequestDenied` demoted the refusal to the routine
missing-consent WARNING — the strongest evidence of a tampered Graph response, filed as a
permission the customer had simply not granted. **Restricting the message to the parsed HOST was
NOT sufficient, which is the part worth remembering**: `url.Parse` admits
`Authorization_RequestDenied.evil.example` as a host outright (`shouldEscape` permits
alphanumerics and `-`, `_`, `.`, `~`), and an IPv6 ZONE admits SPACES (`encodeZone` exempts `' '`),
so `https://[fe80::1%25%20401]/x` yields the host `[fe80::1% 401]`, matching `" 401"`. Both
measured. Sanitising the text of an error is not a control when something else classifies it;
moving classification off the text is. `graphErr` keeps splicing the response BODY into the same
substring channel, and that is correct — the body is the classifier's intended input and the reason
it exists. The rule is not "nothing remote reaches the classifier", it is **"an error the
classifier was not written to read must not be classified by reading it"**.

**Two more text classifiers see the same errors, and both were live defects rather than
hypotheticals.** Ordering the type test AFTER `msg := formatAzureError(err)` closed nothing:
`scanBodyForAADSTS`'s last arm matches `"AADSTS"` in ANY error type — deliberately, because MSAL
and STS return untyped credential errors and that arm is what catches them — so a host of
`AADSTS700016.evil.example` rewrote the refusal as `azure authentication failed (AADSTS700016)`,
dropped the host an operator would act on, wrote the attacker's host to stderr and left an
attacker-chosen key in the never-cleared `loggedCredentialErrors` map. `reportEntraErr` therefore
resolves the type BEFORE it formats. And `sameGraphHost` constrains a nextLink's scheme and host
and nothing else, so `http.Client.Do`'s `*url.Error` carried the attacker's PATH and QUERY into the
substring block — a tampered link the attacker then refuses to answer demoted a transport failure
to the same warning.

**Stripping the URL from that cause was NOT the fix, and believing it was is the mistake worth
recording.** The unwrapped cause carries text the SERVER chooses: Go parses the `Location` header
BEFORE it consults `CheckRedirect`, so an unparseable one renders as `failed to parse Location
header "/v1.0/Authorization_RequestDenied%zz"` — measured, and classified as the missing-consent
warning. A malformed response header line does the same with no redirect at all. There is no
sanitising a channel the remote side writes into; the only fix is that `reportEntraErr` never reads
it. `neverAConsentFailure` holds every type this package mints against a Graph response and is
where the next one belongs — the rule is one line of code, not a habit. Re-derive the set from the
function rather than from a count here: it read "all four" while the function held six, because the
change that added `oversizePageError` and `tooManyPagesError` wired them in and left the sentence
alone. Nothing enforces membership, so a seventh escapes silently.

**Bounding that text is a CHOKEPOINT, not a property of each type, and getting that wrong shipped
once.** `sanitizeForScanRecord` is called from `reportEntra` and nowhere else. The first attempt
bounded one type's `Error()`, which left `graphErr` — the response BODY, the largest remote-chosen
string of the lot — going through `formatAzureError`'s pass-through arm untouched, and `hostOnly`'s
doc claiming a bound it did not have (a 20 kB host parses fine, measured). The body is capped a
second time at the READ (`maxGraphErrorBody`), because the record's bound cannot stop an
`io.ReadAll` holding it in memory first — two guards, two separate tests, and the read cap's mutant
survived the record test.

**A field the response carries as a TYPE must not be re-derived from its text.** `reportEntraErr`
read the status with `strings.Contains(raw, " 403")`, which matches anywhere in the 8 KiB body the
remote side wrote — so a 500 whose body said `Error 403` was filed as the routine missing-consent
WARNING and the real fault never surfaced as one. `graphErr` carries `status` as an int; the code
STRINGS (`Authorization_RequestDenied`, `Insufficient privileges`) still come from the body and
should, because those are Graph's own diagnosis. Same claim as the paragraph above, one field
further in: sanitising the channel is not the fix, not reading the channel for something it does
not own is.

**Two byte caps and a page cap, and the repeated-link refusal is NOT the loop's bound.** The ERROR
body was capped while the SUCCESS body — the path that decodes into memory and then goes back for
another page — was not; `maxGraphPageBody` (64 MiB) closes that, constructed as a `*io.LimitedReader` for the
HANDLE — `io.LimitReader` RETURNS one, so the two truncate identically — because reading `N` back is
what distinguishes "the page hit the cap" from a TRUNCATION-shaped malformed body — both reach
`json.Decoder` as `io.ErrUnexpectedEOF`, while a syntactically invalid body arrives as
`*json.SyntaxError` and was never confusable. `N` is seeded at the cap PLUS ONE, so `N == 0` means
strictly more than the cap was delivered and the refusal's stated limit is always true — a body of
exactly the cap leaves `N == 1` and cannot reach the branch. It does not establish that size is WHY
the decode failed, so an over-cap body that was also malformed is reported as too large. `maxGraphPages` (200k) bounds the page
count — the COUNT of `iterateGraph`'s cycle-detection keys and NOT their size, since a key is a
whole nextLink and nothing caps a nextLink's length; the map's worst case is that product, which is
an open gap stated at the constant rather than closed by it. It is a `var` only so a test can lower it — 200k round trips is not an observable, and a
test re-deriving the bound would agree with itself whether or not `iterateGraph` still consulted it.
**The repeated-link guard names an exact repeat and nothing else**: a server incrementing
`$skiptoken` produces a fresh URL every time and walks straight past it. What bounds the general
case is the `serviceTimeout` `runTenantServices` now applies — every (subscription, service) pair
already had one and the tenant phase had none, so a service that never returned hung the whole scan
on `wg.Wait()`. That deadline's `cancel()` is DEFERRED inside a wrapper func: `runTenantServices`
does not recover, `azure_scanner_test.go` registers a tenant service that panics on purpose, and
`go vet`'s lostcancel cannot see a call the panic jumps over.

**`sameGraphHost` guards the response BODY; the redirect was the unguarded half, and the worse
one.** It reads `@odata.nextLink`, so a 3xx went unexamined — and `azHTTPClient` had no
`CheckRedirect`, so it was followed. Measured on the unfixed client: a 302 from the Graph host to
another origin had the foreign response DECODED and stored as the customer's directory objects,
**with the bearer forwarded to that origin**. net/http's rule is LOOSER than "same host":
`shouldCopyHeaderOnRedirect` calls `isDomainOrSubdomain`, so `Authorization` survives a hop to a
SUBDOMAIN (`evil.graph.microsoft.com`), and it reads `url.Hostname()`, so scheme and port are
ignored and an https→http downgrade on the same host puts a `Directory.Read.All` token on the wire
in clear — the exact shape `sameGraphHost` refuses by name for a nextLink. `graphHTTPClient` refuses
every redirect; it shares the ARM pool's TRANSPORT and not its `http.Client`. **`azHTTPClient`
itself is unchanged and ARM still follows redirects** — azcore ships no redirect policy, so the
foreign-body half of this hazard is open there and is deliberately out of scope for a Graph change;
do not read the Graph fix as covering it. Note
`ErrUseLastResponse` hands the 3xx BODY back, and a 3xx body is decodable if the server says so — so
`get` must refuse the status explicitly, or a redirect whose body is valid JSON is parsed as the
page. Both halves are mutation-proved; the second survived a test whose redirect body was
`http.Redirect`'s HTML.

`DISCO_AZURE_GRAPH_TENANT_ID`'s value is required to be a GUID (`graphTenantGUID`). The
multi-tenant aliases `common` and
`organizations` are exactly what must not be accepted — they resolve to whatever directory the
token happens to come back for, which is the property being pinned.

**`DISCO_AZURE_GRAPH_TENANT_ID` is REFUSED when half-set, like every other variable in the
contract, and it is the member whose omission from that check would be worst.** Alone it is not
inert: `configured()` is false, so `tenantScopeEnabled()` is TRUE, every tenant service runs, and
`scanEntra` reads whatever directory an ambient credential authenticated in with no pin at all —
the disclosure the variable is advertised to prevent, switched on by setting it. The realistic
arrival is a deployment still holding `AZURE_CLIENT_ID`/`AZURE_CLIENT_SECRET` for a Lighthouse
managing principal while a rename drops the WIF pair.

**One JOINING is deliberately untested and says so at the site.** `credentialOptions` is asserted
in isolation and `graphClient`'s threading is asserted at the token, but nothing reaches
`newFederatedCredential` without AWS STS — so reverting its options argument to `nil` is green
across the suite. Recorded in a comment there rather than propped up with a seam built only for a
test, because the failure is fail-closed and loud: every Graph acquisition would error.

**The proof is the `tid`, never the variable.** `scanEntra` reads the tenant id from the token that
came back rather than echoing what it asked for, and refuses to store anything if the two disagree:
every row it writes is keyed by that id, so a mismatch that proceeded would file one directory's
identities under another's account id, and an append-only inventory cannot be corrected afterwards.
The disagreement can only mean something upstream is wrong, so a warning and zero rows is the
answer, not a best effort.

## Subscription-scoped vs tenant-scoped

Every per-sub scanner runs via `scanSubscription`. Tenant-scope services (Entra ID via Microsoft Graph SDK, etc.) register via `registerTenantService(tenantServiceEntry{...})` in `tenant_scanners.go` — fn fires ONCE per scan, receives `[]subscription` + cred. Dispatch via `runTenantServices` in `tenant_scanners.go`. `Scan` runs the tenant phase **concurrently** with the per-sub fan-out (in the same `WaitGroup`) and gates only each subscription's phase-2 resolvers on it via `waitForTenant(ctx, entraDone)` — the tenant phase's only consumer is the phase-2 authorization resolver, so its latency hides behind phase-1 scanning instead of preceding it. New tenant services that other resolvers depend on inherit this join for free; a tenant service whose data a *phase-1* scanner needs would require widening the gate. Tenant-scope resources are stored under the tenant GUID (`subscription.tenantID`, resolved once in `Scan` from the ARM token's `tid` claim and stamped onto every subscription — **but not under a federated credential**, where the stamp is deliberately left empty; see the federation section above. That stays true even when `DISCO_AZURE_GRAPH_TENANT_ID` lets the Entra phase run — consenting to a directory does not fill the stamp, and could not: the stamp's source is an ARM token, which names disco's directory, while `scanEntra` resolves its own id from the GRAPH token it pinned, and under Lighthouse those are different directories) so a multi-subscription scan keeps a single copy — precedent: `scanManagementTenant` (management groups), `scanAuthorizationBuiltins` (built-in role/policy/set definitions). When `tenantID` is empty (resolution failed) these fall back to per-subscription storage. **Hybrid pattern** (precedent: `scanSubscriptionResource` for `microsoft.resources`/`TypeSubscription`): a tenant-wide ARM API can still run *inside* `scanSubscription` if you accept per-sub duplication — `ResourceID` hash includes account_id so per-sub resolvers FK locally — **and you filter the response to the scanned scope**, which is the half that was missing. Its `GET /subscriptions` answers for the whole tenant, so under a delegated credential the unfiltered loop wrote other customers' subscription ids and display names under this customer's account. Unfiltered, per-sub duplication and cross-customer disclosure are the same bug. AccessDenied tolerated via `skipIfAccessDenied` for callers without tenant-level RBAC.

## Generic helpers split by concern

Cross-service helpers live one-per-file under the `azure_` prefix: `azure_scan_helpers.go` (`azPageScan`, `azSimpleScan`, `azTrackedRows`, `azPager`, `azRGFanoutScan`, `listSubscriptionRGNames`, `isResourceGroupNotFound`), `azure_armid.go` (`rgFromID`, `rgNameFromID`, `nameFromID`, `truncateAtSegment`, `vnetIDFromSubnetID`), `azure_tags.go` (`azTagsJSON`), `azure_errors.go` (`isAccessDenied`, `isFeatureNotAvailable`, `skipIfAccessDenied`, `formatAzureError`), `azure_concurrency.go` (`maxConcurrentFanout`), `azure_scanner.go` (`Scanner`, `Scan`, `subscription`, `azClientOptions`, `mustJSON`/`sv`/`tp`/`regionGlobal`, function-app sidecar). Per-service code stays in `<svc>_scanners.go` / `<svc>_resolvers.go`. Mirror the AWS / GCP layout when adding a new generic concern.

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
comes back, and it also asserts `azureAPITypeMap` no longer maps
`microsoft.quota/quotas`. The service registration must survive alongside the
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

Each `arm*` module ships generated `fake.<Type>Server` (e.g. `armcomputefake.DisksServer`) plus `NewXServerTransport`. Pattern: split scanner into `scanX(ctx, sub, cred, st, scanID)` (production wrapper) + `scanXWithClient(ctx, sub, st, scanID, client)` (testable body). Test constructs client via `armcompute.NewDisksClient(subID, fakeCred(), fakeClientOptions(t, transport))` and calls the body directly. Helpers in `fake_testhelper_test.go`: `fakeCred()` returns `*azfake.TokenCredential{}`, `fakeClientOptions` collapses retries (MaxRetries=0). Precedent: `compute_disks_scanners_test.go` covers happy path, multi-page pagination, 403 AccessDenied.

For error injection use `azfake.PagerResponder.AddResponseError(http.StatusForbidden, "AuthorizationFailed")` — produces an `azcore.ResponseError` the `isAccessDenied` check recognises.

## Error formatting — always `formatAzureError`

`azcore.ResponseError.Error()` dumps the request line (method, scheme, host, escaped path — no headers, no query) plus the response status and the full ARM error body — multi-KB per warning. It renders no part of the REQUEST beyond that line — no headers, so no `Authorization: Bearer`; no request body, so no client-assertion JWT — which is why neither can reach the store or stderr through this path. **Never** pass `err.Error()` directly into `store.ScanWarning.Message` / `store.ScanError.Message`. Use `formatAzureError(err)` (in `azure_errors.go`) — narrows to `"{statusCode} {errorCode}: {message}"` matching AWS/GCP brevity. Falls back to `err.Error()` for non-`*azcore.ResponseError` (store / JSON / I/O errors), so it's safe at every site — **except** that it collapses a CREDENTIAL failure to its diagnostic code first, ahead of every other branch, because that text names disco's own tenant and AWS role rather than anything the customer scanned (`redactCredentialError`). Existing call sites: `skipIfAccessDenied`, `runTenantServices`, `runAPIResolvers` dispatch, `reportEntraErr` + Entra storage-error sites.

## Lint gotchas

- `forvar` (Go 1.22+): drop `i, x := i, x` shadows in goroutines — per-iteration scope built-in.
- Project gofmt config rewrites `init()` one-liners to multiline; run `gofmt -w .` before each commit to avoid post-commit linter drift.