# GCP resource-type coverage ledger

Adjudication of every uncovered GCP Discovery-API resource type reported by
`disco coverage services --providers gcp --filter uncovered`, against the real Go client
libraries (`google.golang.org/api/...`, all already vendored in `go.mod` at `v0.286.0` unless
noted). Verdicts: **INCLUDE** (real, listable, currently-unscanned customer resource — a
scanner should be added), **DEFER** (real resource, but no bulk-list path disco can reach
yet — revisit when a prerequisite lands), **DROP** (not a listable user resource, or a
duplicate of something already scanned). Mirrors the shape of `docs/azure-type-coverage.md`.

Generated 2026-07-06. Wave sequencing for implementing the INCLUDE list lives in
`ROADMAP.md` §R4, not here — this doc is the verdict record only. Regenerate the candidate
set with the coverage command above.

## Totals

| Bucket | Count |
|---|---|
| Compute Engine types adjudicated | 139 |
| — INCLUDE | 88 |
| — DROP | 51 |
| Non-compute types adjudicated (26 services) | 235 |
| — INCLUDE | ~140 |
| — DEFER | 2 |
| — DROP | ~93 |

## Naming corrections (coverage-tool pluralization)

The coverage tool's `singularize()` (`internal/providers/gcp/gcp_coverage.go`) mishandled
words that pluralize with `-es` before this ledger's audit — fixed alongside this ledger.
Real Discovery/SDK names for types that previously rendered corrupted:

| Rendered (before fix) | Real name | Struct |
|---|---|---|
| `Addresse` | `Address` | `compute.Address` |
| `GlobalAddresse` | `GlobalAddress` | `compute.Address` |
| `PublicAdvertisedPrefixe` | `PublicAdvertisedPrefix` | `compute.PublicAdvertisedPrefix` |
| `PublicDelegatedPrefixe` | `PublicDelegatedPrefix` | `compute.PublicDelegatedPrefix` |
| `GlobalPublicDelegatedPrefixe` | `GlobalPublicDelegatedPrefix` | `compute.PublicDelegatedPrefix` |
| `Aliase` (admin) | `Alias` | two distinct Discovery collections (`users.aliases`, `groups.aliases`) collide on this key — see DROP below |

## INCLUDE — Compute Engine (88)

All via `google.golang.org/api/compute/v1` (module confirmed at `v0.286.0`, "maintenance
mode" per package doc but the SDK disco already depends on). Prefer `AggregatedList` over
per-zone/per-region `List` wherever the SDK exposes it — single paginated call vs manual
zone/region enumeration.

### Storage (11) — Wave 1, implemented this session
| Type | Method | Scope |
|---|---|---|
| Disk | `DisksService.AggregatedList` | zonal |
| RegionDisk | `RegionDisksService.List(project, region)` | regional (no AggregatedList) |
| Image | `ImagesService.List(project)` | global |
| MachineImage | `MachineImagesService.List(project)` | global |
| Snapshot | `SnapshotsService.List(project)` | global |
| RegionSnapshot | `RegionSnapshotsService.List(project, region)` | regional |
| InstantSnapshot | `InstantSnapshotsService.AggregatedList` | zonal |
| RegionInstantSnapshot | `RegionInstantSnapshotsService.List(project, region)` | regional |
| InstantSnapshotGroup | `InstantSnapshotGroupsService.List(project, zone)` | zonal (no AggregatedList) |
| RegionInstantSnapshotGroup | `RegionInstantSnapshotGroupsService.List(project, region)` | regional |
| StoragePool | `StoragePoolsService.AggregatedList` | zonal |

### Instance groups & templates (8) — Wave 2, implemented
| Type | Method | Scope |
|---|---|---|
| InstanceGroup | `InstanceGroupsService.AggregatedList` | zonal |
| RegionInstanceGroup | `RegionInstanceGroupsService.List(project, region)` | regional |
| InstanceGroupManager | `InstanceGroupManagersService.AggregatedList` | zonal |
| RegionInstanceGroupManager | `RegionInstanceGroupManagersService.List(project, region)` | regional |
| InstanceGroupManagerResizeRequest | `InstanceGroupManagerResizeRequestsService.List(project, zone, igm)` | nested under IGM |
| RegionInstanceGroupManagerResizeRequest | `RegionInstanceGroupManagerResizeRequestsService.List(project, region, igm)` | nested |
| InstanceTemplate | `InstanceTemplatesService.AggregatedList` | global |
| RegionInstanceTemplate | `RegionInstanceTemplatesService.List(project, region)` | regional |

### Addressing (5) — Wave 3, implemented
| Type | Method | Scope |
|---|---|---|
| Address | `AddressesService.AggregatedList` | regional |
| GlobalAddress | `GlobalAddressesService.List(project)` | global |
| PublicAdvertisedPrefix | `PublicAdvertisedPrefixesService.List(project)` | global |
| PublicDelegatedPrefix | `PublicDelegatedPrefixesService.AggregatedList` | regional |
| GlobalPublicDelegatedPrefix | `GlobalPublicDelegatedPrefixesService.List(project)` | global |

### Networking core (20) — Wave 4, implemented
| Type | Method | Scope |
|---|---|---|
| Route | `RoutesService.List(project)` | global |
| Router | `RoutersService.AggregatedList` | regional |
| VpnGateway | `VpnGatewaysService.AggregatedList` | regional |
| ExternalVpnGateway | `ExternalVpnGatewaysService.List(project)` | global |
| TargetVpnGateway | `TargetVpnGatewaysService.AggregatedList` | regional |
| VpnTunnel | `VpnTunnelsService.AggregatedList` | regional |
| NetworkAttachment | `NetworkAttachmentsService.AggregatedList` | regional |
| NetworkEndpointGroup | `NetworkEndpointGroupsService.AggregatedList` | zonal |
| RegionNetworkEndpointGroup | `RegionNetworkEndpointGroupsService.List(project, region)` | regional |
| GlobalNetworkEndpointGroup | `GlobalNetworkEndpointGroupsService.List(project)` | global |
| NetworkFirewallPolicy | `NetworkFirewallPoliciesService.AggregatedList` | global |
| RegionNetworkFirewallPolicy | `RegionNetworkFirewallPoliciesService.List(project, region)` | regional |
| NetworkProfile | `NetworkProfilesService.List(project)` | global |
| NodeGroup | `NodeGroupsService.AggregatedList` | zonal |
| NodeTemplate | `NodeTemplatesService.AggregatedList` | regional |
| PacketMirroring | `PacketMirroringsService.AggregatedList` | regional |
| ServiceAttachment | `ServiceAttachmentsService.AggregatedList` | regional |
| NetworkEdgeSecurityService | `NetworkEdgeSecurityServicesService.AggregatedList` (only — no plain regional List) | regional |
| CrossSiteNetwork | `CrossSiteNetworksService.List(project)` | global |
| WireGroup | `WireGroupsService.List(project, crossSiteNetwork)` | nested under CrossSiteNetwork |

### Interconnect (4) — Wave 5, implemented
| Type | Method | Scope |
|---|---|---|
| Interconnect | `InterconnectsService.List(project)` | global |
| InterconnectAttachment | `InterconnectAttachmentsService.AggregatedList` | regional |
| InterconnectGroup | `InterconnectGroupsService.List(project)` | global |
| InterconnectAttachmentGroup | `InterconnectAttachmentGroupsService.List(project)` | global |

### Load balancing / health checks / SSL-TLS (24) — Wave 6, implemented
| Type | Method | Scope |
|---|---|---|
| GlobalForwardingRule | `GlobalForwardingRulesService.List(project)` | global |
| HealthCheck | `HealthChecksService.AggregatedList` | global |
| RegionHealthCheck | `HealthChecksService.AggregatedList` (dual-type split w/ HealthCheck) | regional |
| RegionCompositeHealthCheck | `RegionCompositeHealthChecksService.AggregatedList` | regional |
| RegionHealthAggregationPolicy | `RegionHealthAggregationPoliciesService.AggregatedList` | regional |
| RegionHealthCheckService | `RegionHealthCheckServicesService.AggregatedList` | regional |
| RegionHealthSource | `RegionHealthSourcesService.AggregatedList` | regional |
| RegionNotificationEndpoint | `RegionNotificationEndpointsService.AggregatedList` | regional |
| HttpHealthCheck | `HttpHealthChecksService.List(project)` | global (legacy, distinct resource) |
| HttpsHealthCheck | `HttpsHealthChecksService.List(project)` | global |
| SslCertificate | `SslCertificatesService.AggregatedList` | global |
| RegionSslCertificate | `SslCertificatesService.AggregatedList` (dual-type split w/ SslCertificate) | regional |
| SslPolicy | `SslPoliciesService.AggregatedList` | global |
| RegionSslPolicy | `SslPoliciesService.AggregatedList` (dual-type split w/ SslPolicy) | regional |
| TargetSslProxy | `TargetSslProxiesService.List(project)` | global |
| TargetTcpProxy | `TargetTcpProxiesService.AggregatedList` | global |
| RegionTargetTcpProxy | `TargetTcpProxiesService.AggregatedList` (dual-type split w/ TargetTcpProxy) | regional |
| TargetGrpcProxy | `TargetGrpcProxiesService.List(project)` | global |
| RegionTargetHttpProxy | `RegionTargetHttpProxiesService.List(project, region)` | regional |
| RegionTargetHttpsProxy | `RegionTargetHttpsProxiesService.List(project, region)` | regional |
| RegionUrlMap | `RegionUrlMapsService.List(project, region)` | regional |
| RegionBackendBucket | `RegionBackendBucketsService.List(project, region)` | regional |
| RegionBackendService | `BackendServicesService.AggregatedList` (dual-type split w/ BackendService) | regional |
| TargetInstance | `TargetInstancesService.AggregatedList` | zonal |
| TargetPool | `TargetPoolsService.AggregatedList` | regional |

### Autoscaling & reservations (10) — Wave 7, implemented (9/10; ReservationSlot deferred)
| Type | Method | Scope |
|---|---|---|
| Autoscaler | `AutoscalersService.AggregatedList` (dual-type split w/ RegionAutoscaler, on Zone-field presence) | zonal |
| RegionAutoscaler | `AutoscalersService.AggregatedList` (dual-type split w/ Autoscaler) | regional |
| Reservation | `ReservationsService.AggregatedList` | zonal |
| ReservationBlock | `ReservationBlocksService.List(project, zone, reservation)` | nested under Reservation |
| ReservationSubBlock | `ReservationSubBlocksService.List(project, zone, parentName)` | nested under ReservationBlock |
| ReservationSlot | `ReservationSlotsService.List(project, zone, parentName)` | **DEFER** — 4th nesting level under ReservationSubBlock; no edges of its own and unbounded per-subblock cardinality (large ML/TPU reservations can run into the thousands of slots) |
| FutureReservation | `FutureReservationsService.AggregatedList` | zonal |
| RegionCommitment | `RegionCommitmentsService.AggregatedList` | regional |
| ResourcePolicy | `ResourcePoliciesService.AggregatedList` | regional |
| RegionSecurityPolicy | `RegionSecurityPoliciesService.List(project, region)` | regional |

### Newer project-level policy features (7) — lower priority, fold into Wave 7 or defer further
| Type | Method | Scope | Note |
|---|---|---|---|
| GlobalVmExtensionPolicy | `GlobalVmExtensionPoliciesService.AggregatedList` | global | fleet-wide guest-extension policy |
| ZoneVmExtensionPolicy | `ZoneVmExtensionPoliciesService.List(project, zone)` | zonal | |
| PreviewFeature | `PreviewFeaturesService.List(project)` | project | feature-flag objects |
| Rollout | `RolloutsService.List(project)` | project | maintenance/rollout schedule |
| RolloutPlan | `RolloutPlansService.List(project)` | project | same family as Rollout |

(2 more of the 88 — `GlobalForwardingRule` counted above once; totals reconcile against the
research-agent audit's per-domain table, not re-tabulated separately here.)

## DROP — Compute Engine (51)

```
# Operations / action-verbs (9) — LRO polling objects, no durable identity
GlobalOperation
RegionOperation
ZoneOperation
GlobalOrganizationOperation
GlobalFolderOperation	no SDK service exists
ZoneFolderOperation	no SDK service exists
ZoneOrganizationOperation	no SDK service exists

# Metadata/catalog reference data (11) — Google-defined catalogs, not customer resources
AcceleratorType
DiskType
RegionDiskType
MachineType
NodeType
StoragePoolType
Zone
Region
RegionZone
InterconnectLocation
InterconnectRemoteLocation

# License/singleton config objects (7)
License	catalog of OS/publisher licenses
LicenseCode	Get only, no List
InstanceSetting	per-project singleton config, Get/Patch only
SnapshotSetting	per-project singleton config, Get/Patch only
RegionSnapshotSetting	per-region singleton config, Get/Patch only
DiskSetting	no SDK type/service exists in this SDK version
RegionDiskSetting	no SDK type/service exists in this SDK version

# Organization/folder-scoped policy singletons (9) — hierarchical, not project-listable
FirewallPolicy	List() is ParentId-scoped (org/folder), not project
OrganizationSecurityPolicy	List() is ParentId-scoped, not project
OrganizationRollout	no SDK service
OrganizationRolloutPlan	no SDK service
OrganizationSnapshotRecycleBinPolicy	no SDK service
FolderGlobalVmExtensionPolicy	no SDK service (only project-scoped Global/Zone variants exist)
FolderZoneVmExtensionPolicy	no SDK service
OrganizationGlobalVmExtensionPolicy	no SDK service
OrganizationZoneVmExtensionPolicy	no SDK service

# Deprecated/legacy, no real resource (1)
Host	sole-tenant concept superseded by NodeGroup/NodeTemplate; no HostsService

# View/lookup-only, not a collection (3)
ImageFamilyView	Get only, no List
ImageView	no SDK service exists
ProjectView	no SDK service exists

# The scan target itself (1)
Project	ProjectsService.Get is the project being scanned, not a sub-resource to enumerate

# No SDK List method / type does not exist in compute/v1@v0.286.0 (11)
AcceleratorPodController
HaController
ReliabilityRisk
DhcpOptionsConfig
RecoverableSnapshot
RegionRecoverableSnapshot
SnapshotGroup
SnapshotRecycleBinPolicy
RegionMultiMig
RegionMultiMigMember
RegionNetworkPolicy
ZoneQueuedResource
```

## INCLUDE — non-compute services (~140 across 26 services)

Grouped by service; every client package is already vendored in the same
`google.golang.org/api` module except two noted new sub-package imports.

### Security-critical secondary services — Wave 8 priority
Wave 8 is split into API-scoped sub-waves (8a-8g) rather than one commit,
matching the size of prior waves. Sub-wave status noted inline below.

**8a — cloudkms, implemented.** Inlined into the existing `scanCloudKMS`
nested loops (per-location siblings of KeyRings.List; per-keyring sibling of
CryptoKeys.List; per-crypto-key nested under CryptoKeys.List) rather than
separate scanners, since the parent location/keyring/crypto-key context was
already being walked. `scanCloudKMS` split into a thin outer wrapper + a
testable `scanCloudKMSWithClient` core (test-seam pattern, `internal/providers/CLAUDE.md`).

| Type | Package.Client | List method | Scope |
|---|---|---|---|
| cloudkms CryptoKeyVersion | cloudkms/v1 CryptoKeysCryptoKeyVersionsService | List(parent) | fan-out per CryptoKey |
| cloudkms EkmConnection | cloudkms/v1 EkmConnectionsService | List(parent) | project |
| cloudkms ImportJob | cloudkms/v1 ImportJobsService | List(parent) | fan-out per KeyRing |
| cloudkms KeyHandle | cloudkms/v1 KeyHandlesService | List(parent) | project |
| cloudkms SingleTenantHsmInstance | cloudkms/v1 SingleTenantHsmInstancesService | List(parent) | project |
| cloudresourcemanager TagKey | cloudresourcemanager/v3 TagKeysService | List(parent) | org **and** project — a TagKey can be parented directly by either (`TagKey.Parent` doc), so both `scanCRMTags` (org-wide) and `scanCRMLiensAndBindings` (per-project) call `TagKeys.List` |
| cloudresourcemanager TagValue | cloudresourcemanager/v3 TagValuesService | List(parent) | fan-out per TagKey |
| cloudresourcemanager TagBinding | cloudresourcemanager/v3 TagBindingsService | List(parent) | **scoped down to project resource only**, not the API's full per-resource fan-out — see below |
| cloudresourcemanager EffectiveTag | cloudresourcemanager/v3 EffectiveTagsService | List(parent) | **scoped down to project resource only** — see below |
| cloudresourcemanager TagHold | cloudresourcemanager/v3 TagValuesTagHoldsService | List(parent) | fan-out per TagValue |
| cloudresourcemanager Lien | cloudresourcemanager/v3 LiensService | List(parent) | project |

**8b — cloudresourcemanager, implemented.** TagBinding/EffectiveTag formally
accept any Google Cloud resource as `parent`, but enumerating every scanned
resource of every type in a project to check for tag bindings would multiply
the API-call count across the entire scan for a feature most commonly used at
the project/folder/org level. Scoped down to the project resource itself only
(same judgment call as Wave 7's ReservationSlot deferral) — per-resource
TagBinding/EffectiveTag rows are a known, accepted gap, not yet implemented.
TagBindings/EffectiveTags require the full-resource-name form of `parent`
(`//cloudresourcemanager.googleapis.com/projects/{number}`), built from the
project NUMBER; skipped (not an error) for any project whose number wasn't
resolved during hierarchy discovery (permission-denied `Projects.Get`).

**8c — accesscontextmanager, implemented.** AccessLevel and AuthorizedOrgsDesc
inlined as new siblings in the existing `scanVPCSCForOrg` per-policy loop
(alongside the pre-existing ServicePerimeter fan-out) rather than a parallel
scanner re-listing AccessPolicies — same "extend in place" precedent as 8a.
GcpUserAccessBinding is parented directly by the org (not by any
AccessPolicy), so it's a sibling call in the outer `scanVPCSC` org loop
instead. `vpcsc_scanners.go` had zero test coverage before this sub-wave;
`vpcsc_scanners_test.go` is new.

| accesscontextmanager AccessLevel | accesscontextmanager/v1 AccessPoliciesAccessLevelsService | List(parent) | fan-out per AccessPolicy (already scanned) |
| accesscontextmanager AuthorizedOrgsDesc | accesscontextmanager/v1 AccessPoliciesAuthorizedOrgsDescsService | List(parent) | fan-out per AccessPolicy |
| accesscontextmanager GcpUserAccessBinding | accesscontextmanager/v1 OrganizationsGcpUserAccessBindingsService | List(parent) | org |

**8d — sqladmin, implemented.** Fans out per already-scanned Instance
(bounded concurrency via `forEachItem`, same shape as KMS's per-location
fan-out). Only BackupRuns paginates (`Pages()`); Databases/SslCerts/Users are
single-page `.Do()` calls per the SDK (Users' `NextPageToken` is explicitly
documented "Unused"). Fixed a pre-existing `singularize()` bug found while
verifying `Database` lands `covered`: the Discovery collection name
`"databases"` and `"aliases"` share an identical `-ases` suffix, but one's
true singular ends in a sibilant "s" and the other in a silent "e" — no
suffix-only rule can tell them apart. Added a small exception map rather than
reworking the general heuristic; this also fixed two other previously
`upstream-missing` rows (`firestore Database`, `spanner Database`) as a
side effect.

| sqladmin BackupRun | sqladmin/v1 BackupRunsService | List(project, instance) | fan-out per Instance |
| sqladmin Database | sqladmin/v1 DatabasesService | List(project, instance) | fan-out per Instance |
| sqladmin SslCert | sqladmin/v1 SslCertsService | List(project, instance) | fan-out per Instance |
| sqladmin User | sqladmin/v1 UsersService | List(project, instance) | fan-out per Instance |

**8e — dns, implemented.** DnsKey/Policy/ResponsePolicy/ResponsePolicyRule added
to the existing `scanCloudDNS` (previously ManagedZone + ResourceRecordSet
only) rather than a new scanner — reverses this file's own Wave-1-era
deferral note ("DNSSEC keys + policies + response policy rules deferred —
narrow graph value vs. cardinality risk"); the ledger's Wave 8 priority list
re-scoped these as in-scope. DnsKey fans out per already-scanned zone
alongside the pre-existing record-set listing (same zone closure's per-zone
loop gained a second sequential list call); Policy is project-scoped, no zone
parent; ResponsePolicy → ResponsePolicyRule is a new two-level fan-out
(response policy list, then per-response-policy rules list). DnsKey has no
API-issued name — NativeID synthesized from its per-zone-unique numeric `Id`,
with a synthesized display `Name` of `"{Type} ({Algorithm})"`. Split
`scanCloudDNS` into a thin outer wrapper + `scanCloudDNSWithClient` test-seam
core (this scanner had zero test coverage before this sub-wave). Route-prefix
gotcha: unlike cloudkms/cloudresourcemanager/accesscontextmanager/sqladmin
(bare `v1/` route templates), the `dns/v1` SDK package's route templates
embed the full `dns/v1/` prefix — test fake-server routes use `/dns/v1/...`.

| dns DnsKey | dns/v1 DnsKeysService | List(project, managedZone) | fan-out per zone (already scanned) |
| dns Policy | dns/v1 PoliciesService | List(project) | project |
| dns ResponsePolicy | dns/v1 ResponsePoliciesService | List(project) | project |
| dns ResponsePolicyRule | dns/v1 ResponsePolicyRulesService | List(project, responsePolicy) | fan-out per ResponsePolicy |

**8f — cloudidentity, implemented.** Ten new types added to the existing
`scanCloudIdentity` (previously Workspace Directory Users + Cloud Identity
Groups only, both flat with no hierarchy closure). Device is flat
(customer-scoped, no parent); DeviceUser/ClientState use the API's wildcard
parent (`devices/-`, `devices/-/deviceUsers/-`) rather than fanning out per
already-scanned Device/DeviceUser — one paginated call returns every device's
children customer-wide, KMS-style multi-parent batch derives each row's
owning parent by string-splitting its own resource name (`/deviceUsers/`,
`/clientStates/`) since a single page can mix children of many different
parents. Membership has no wildcard support (GCP requires an explicit
`groups/{group}`), so it fans out per already-scanned Group via
`forEachItem`; IdpCredential likewise fans out per already-listed SAML SSO
profile. InboundOidcSsoProfile/InboundSamlSsoProfile/InboundSsoAssignment/
Policy all omit their optional `filter` param entirely rather than
hand-building a CEL expression — the SDK documents omission as defaulting to
the caller's own customer, which is this scanner's only supported scope
anyway. `scanCloudIdentityGroups`'s signature grew a `groupNames []string`
return so the Membership fan-out has a list to iterate. Added a
`redact.Register` rule for `InboundOidcSsoProfile.rpConfig.clientSecret`
(SDK-documented input-only field, defensive against a future echo — same
rationale as the pre-existing SQL user password rule). First test file this
scanner ever had.

| cloudidentity Device | cloudidentity/v1 DevicesService | List() | tenant |
| cloudidentity DeviceUser | cloudidentity/v1 DevicesDeviceUsersService | List(parent=`devices/-`) | wildcard, customer-wide |
| cloudidentity ClientState | cloudidentity/v1 DevicesDeviceUsersClientStatesService | List(parent=`devices/-/deviceUsers/-`) | wildcard, customer-wide |
| cloudidentity Membership | cloudidentity/v1 GroupsMembershipsService | List(parent) | fan-out per Group (already scanned) |
| cloudidentity InboundOidcSsoProfile | cloudidentity/v1 InboundOidcSsoProfilesService | List() | tenant |
| cloudidentity InboundSamlSsoProfile | cloudidentity/v1 InboundSamlSsoProfilesService | List() | tenant |
| cloudidentity IdpCredential | cloudidentity/v1 InboundSamlSsoProfilesIdpCredentialsService | List(parent) | fan-out per SSO profile |
| cloudidentity InboundSsoAssignment | cloudidentity/v1 InboundSsoAssignmentsService | List() | tenant |
| cloudidentity Policy | cloudidentity/v1 PoliciesService | List() | tenant |
| cloudidentity Userinvitation | cloudidentity/v1 CustomersUserinvitationsService | List(parent) | tenant |

**8g — iam, implemented, closes ROADMAP R4.23 and Wave 8.** New file
`iam_federation_scanners.go` (existing `iam_scanners.go` only had
ServiceAccounts). Two new registrations: `gcp:iam-org` (WorkforcePool →
Provider → ScimTenant, plus org-scoped custom Roles) and `gcp:iam-project`
(WorkloadIdentityPool → Provider, WorkloadIdentityPool → Namespace →
ManagedIdentity, OauthClient → Credential, plus project-scoped custom
Roles). `TypeIAMProvider` is ONE disco type shared by two distinct SDK
structs (`WorkforcePoolProvider`, `WorkloadIdentityPoolProvider`) — the
Discovery API's collection name (`providers`) is identical at both nesting
paths, so they singularize to the same upstream key; splitting into two
disco types would leave one permanently unmatched against that shared key.
Custom Role has separate org/project SDK service types (unlike CRM TagKeys,
which shares one List call across both scopes) but both emit the same
`TypeIAMRole`. Redact rules added for `TypeIAMCredential.clientSecret`
(genuinely returned by List, SDK-documented "Output only") and
`TypeIAMProvider`'s three OIDC/OAuth2 client-secret paths (input-only,
defensive against a future echo — adversarial review caught the latter
after the former was already in place). First test file this scanner ever
had.

| iam WorkforcePool | iam/v1 LocationsWorkforcePoolsService | List(location="locations/global") | org — closes ROADMAP R4.23 |
| iam WorkloadIdentityPool | iam/v1 ProjectsLocationsWorkloadIdentityPoolsService | List(parent) | project — closes R4.23 |
| iam Provider (workforce+workload) | iam/v1 (respective ProvidersService) | List(parent) | fan-out per pool — closes R4.23 |
| iam Namespace, ManagedIdentity | iam/v1 (respective Service) | List(parent) | fan-out per pool |
| iam OauthClient, Credential | iam/v1 (respective Service) | List(parent) | project / fan-out |
| iam ScimTenant | iam/v1 LocationsWorkforcePoolsProvidersScimTenantsService | List(parent) | fan-out per Provider |
| iam Role (custom only) | iam/v1 ProjectsRolesService / OrganizationsRolesService | List(parent) | project/org (exclude predefined-role catalog) |

### Observability — Wave 9

**9a — logging, implemented.** `internal/providers/gcp/observability_scanners.go`'s
`scanLogging` extends the existing `gcp:logging` service (was sinks-only) into a 7-phase
orchestrator: Sinks → Buckets (wildcard `locations/-`, SDK-doc-confirmed) → Links/Views
(fan-out per already-scanned Bucket, no wildcard support for either) → Exclusions → Metrics
→ LogScopes → SavedQueries (wildcard `locations/-`, SDK-doc-confirmed). LogScopes uses the
literal `locations/global` (not a wildcard) — LogScope.Name's SDK doc states log scopes are
only available in the global location, so there's nothing else to fan out across; an
adversarial review caught an earlier draft's incorrect "every sibling confirms the wildcard"
claim (Links/Views don't support it either) before this shipped. Metric NativeID uses the
SDK-populated `ResourceName` field rather than hand-building `projects/{p}/metrics/{name}` —
review also caught that `LogMetric.Name` may itself contain `/` (SDK example: `nginx/requests`),
which would mis-nest a hand-built path; `ResourceName` is already correctly URL-encoded.
Sinks/Exclusions/Metrics still synthesize NativeIDs where the SDK provides only a bare name
(Sinks/Exclusions have a restricted charset with no `/`, so their synthesis is safe).
Resolver work deferred, per every prior wave this session.

| Type | Package.Client | List method | Scope |
|---|---|---|---|
| logging Bucket | logging/v2 ProjectsLocationsBucketsService | List(parent) | wildcard `locations/-` |
| logging Exclusion | logging/v2 ProjectsExclusionsService | List(parent) | project |
| logging Metric | logging/v2 ProjectsMetricsService | List(parent) | project |
| logging Link | logging/v2 ProjectsLocationsBucketsLinksService | List(parent) | fan-out per Bucket |
| logging View | logging/v2 ProjectsLocationsBucketsViewsService | List(parent) | fan-out per Bucket (IAM-boundary relevant) |
| logging LogScope | logging/v2 ProjectsLocationsLogScopesService | List(parent) | literal `locations/global` (global-only, not a wildcard) |
| logging SavedQuery | logging/v2 ProjectsLocationsSavedQueriesService | List(parent) | wildcard `locations/-` |

**9b — monitoring, implemented.** `scanMonitoring` extends the existing `gcp:monitoring`
service (was AlertPolicies-only) into a 7-phase orchestrator: AlertPolicies → Dashboards
(separate `monitoring/v1` client — dashboards live on a different API version from everything
else here) → Groups (with Members embedded, see below) → NotificationChannels → Services →
SLOs (fan-out per already-scanned Service) → Snoozes → UptimeCheckConfigs. Group Members
(`MonitoredResource`) have no SDK-issued name or ID of their own — just a `Type` string and a
`Labels` map describing whichever resource they refer to — so there's no natural key for an
independent row; they're fetched per Group and embedded under a `members` key in the owning
Group's own attributes via `embedMembersJSON`, per the embed-child-data convention. That helper
exists because the obvious approach (anonymously struct-embedding `*monitoring.Group` and
marshaling the wrapper directly) silently drops the `Members` field: the SDK generates a
value-receiver `MarshalJSON` on `Group` (for `ForceSendFields` handling) that gets promoted to
satisfy `json.Marshaler` on the wrapper, so `encoding/json` calls only that promoted method and
ignores every sibling field. `embedMembersJSON` round-trips through a `map[string]json.RawMessage`
instead. An adversarial review also caught that the first cut of `scanMonitoringGroups` batched
every group behind one final upsert after the whole per-group Members fan-out completed — so a
single real (non-permission-denied) error fetching one group's members discarded every other
group's row too, including ones already fetched successfully. Fixed to commit each group's row
as soon as its own Members fetch completes, matching the per-item-commit shape already used by
`scanLoggingBucketLinks`/`Views` and `scanMonitoringSLOs`. Also fixed a pre-existing
`singularize()` coverage-key bug this wave's own `TypeMonitoringSnooze` addition surfaced:
"snoozes" was mis-derived to "Snooz" (the sibilant-stem rule reads "snooz" as a genuine `+es`
stem), added to `singularizeExceptions` alongside the existing "databases" entry. Resolver work
deferred, per every prior wave this session.

| Type | Package.Client | List method | Scope |
|---|---|---|---|
| monitoring Dashboard | monitoring/v1 ProjectsDashboardsService | List(parent) | project |
| monitoring Group | monitoring/v3 ProjectsGroupsService | List(name) | project, Members embedded (no independent type) |
| monitoring NotificationChannel | monitoring/v3 ProjectsNotificationChannelsService | List(name) | project — `labels.*` redacted (Slack/PagerDuty webhook URLs/keys) |
| monitoring Service | monitoring/v3 ServicesService | List(parent) | project |
| monitoring ServiceLevelObjective | monitoring/v3 ServicesServiceLevelObjectivesService | List(parent) | fan-out per Service |
| monitoring Snooze | monitoring/v3 ProjectsSnoozesService | List(parent) | project |
| monitoring UptimeCheckConfig | monitoring/v3 ProjectsUptimeCheckConfigsService | List(parent) | project |

### Data services secondary resources — Wave 10

**10a — spanner, implemented.** `internal/providers/gcp/databases_scanners.go`'s spanner
section extends the existing `gcp:spanner` service (was Instances/Databases-only) into a
7-phase orchestrator: Instances → InstanceConfigs (project-scoped, mix of Google-catalog and
customer-defined) → InstancePartitions (wildcard parent `projects/{p}/instances/-` — one page
can mix partitions from multiple instances, so each row's owning Instance is derived via
`strings.Cut(ip.Name, "/instancePartitions/")`, safe since Spanner instance IDs cannot contain
`/`) → Databases (now paginated via `.Pages()`, see below) → Backups (fan-out per Instance) →
BackupSchedules (fan-out per Database) → DatabaseRoles (fan-out per Database). An adversarial
review caught three issues: (1) a real bug — Google-managed catalog InstanceConfigs
(`ConfigType == "GOOGLE_MANAGED"`) weren't flagged `ManagedByProvider`, so they'd show up
alongside customer-defined configs in `disco resources`/`disco graph` by default; fixed to set
the flag per-row from the SDK's own `ConfigType` field. (2) a test-coverage gap for
`scanSpannerInstances`/`scanSpannerDatabases` — fixed with 3 added tests. (3) a pagination gap:
`scanSpannerDatabases` used a single `.Do()` call per instance instead of `.Pages()` — harmless
while Databases was a leaf, but the new BackupSchedule/DatabaseRole fan-out phases consume its
`databaseNames` output, so a truncated first page would have silently starved both downstream
phases; fixed to paginate fully. All three fan-out phases (Backups/BackupSchedules/DatabaseRoles)
commit each item's row as soon as it's fetched, inside the `forEachItem` closure — not batched
behind one final upsert — per the per-item-commit convention established in Wave 9b. Resolver
work deferred, per every prior wave this session.

**10b — bigtableadmin, implemented.** `internal/providers/gcp/databases_scanners.go`'s
`scanBigtable` extends the existing `gcp:bigtable` service (was Instances/Clusters-only) into a
10-phase orchestrator: Instances → Clusters (both unchanged, single `.Do()` calls — their
`NextPageToken` is SDK-doc-marked "DEPRECATED: unused and ignored", so no pagination gap exists
here unlike Spanner's Databases) → AppProfiles (project-wide wildcard `instances/-`, per-row
Instance derived via `strings.Cut(ap.Name, "/appProfiles/")`) → Tables/LogicalViews/
MaterializedViews (fan-out per Instance, no wildcard support) → AuthorizedViews/SchemaBundles
(fan-out per Table) → Backups (fan-out per Instance using the cluster wildcard `clusters/-`,
per-row Cluster derived via `strings.Cut(b.Name, "/backups/")`) → HotTablets (fan-out per
Cluster — the one endpoint in this wave confirmed to have no wildcard support, verified against
its own doc comment rather than assumed from sibling endpoints) → MemoryLayers (fan-out per
Instance using the cluster wildcard, per-row Cluster derived via
`strings.TrimSuffix(ml.Name, "/memoryLayer")`, since a memory layer's name always ends in that
literal fixed suffix — one per cluster, unlike Backup's variable-cardinality child). Every
wildcard/no-wildcard claim was checked against its own SDK doc comment individually rather than
inferred from a sibling endpoint's shape — this wave's own precedent (Wave 9a's LogScope
wildcard mistake) was the reason to re-verify rather than pattern-match. An adversarial review
found zero real issues: all wildcard claims, per-row parent derivations, pagination choices, and
fan-out commit patterns checked out against the vendored SDK source. Resolver work deferred,
per every prior wave this session.

**10c — firestore, implemented.** `internal/providers/gcp/databases_scanners.go`'s
`scanFirestore` extends the existing `gcp:firestore` service (was Databases-only) into a
4-phase orchestrator: Databases -> Backups (project-wide wildcard `locations/-`; unlike
Spanner/Bigtable's name-splitting, each `Backup` carries its owning Database's full resource
name directly in its own `Database` field, byte-identical in format to the already-stored
Database `Name`, so no string-splitting is needed) -> BackupSchedules (fan-out per Database) ->
UserCreds (fan-out per Database). None of the three new List endpoints paginates at all — their
response types carry no `NextPageToken` field and their List calls expose no `Pages()` method —
so all three use single `.Do()` calls, verified individually rather than assumed. UserCreds gets
a defensive `securePassword` redact rule even though the SDK doc states List responses never
populate it (only Create/ResetPassword echo it) — same defense-in-depth rationale as the
existing SQL user password rule. An adversarial review found zero real issues. Resolver work
deferred, per every prior wave this session.

**10d — bigquery, implemented.** `internal/providers/gcp/bigquery_scanners.go`'s `scanBigQuery`
extends the existing `gcp:bigquery` service (was Datasets/Tables-only) with Models/Routines
(fan-out per Dataset) and RowAccessPolicies (fan-out per Table, nested inside the per-dataset
Tables.List page loop — row access policies must be listed after their owning Table row is
already committed, since `upsertWithParent`'s closure write silently no-ops otherwise). Unlike
Dataset/Table (which carry an SDK-issued opaque `.Id` string), Model/Routine/RowAccessPolicy have
no such field — NativeIDs are synthesized from each type's own `*Reference` struct
(`ModelReference`/`RoutineReference`/`RowAccessPolicyReference`), all fields SDK-doc-marked
Required so no empty-segment risk. `scanBigQuery` was also split into a thin outer wrapper plus
`scanBigQueryWithClient` — the existing test-seam convention — since this service previously had
no test file at all. An adversarial review found one real bug: the RowAccessPolicies per-table
branch escalated an `isAPINotEnabled`-shaped error (a documented BigQuery error class) to the
whole-service disabled sentinel, discarding the already-successful dataset/table work from the
caller's perspective — even though Datasets.List (phase 1) already proves the API is enabled by
the time this nested call runs. Fixed to always warn-and-continue for this specific nested call
regardless of error shape, fail-first verified. The per-table RowAccessPolicies fan-out cost
(one extra call per table, same cardinality concern already documented for the pre-existing
Tables.Get skip) is an accepted tradeoff — it's the only enumeration path for a security-relevant
type with no independent listing surface. Resolver work deferred, per every prior wave this
session.

**10e — dataproc, implemented, closing Wave 10.** `internal/providers/gcp/dataproc_scanners.go`'s
`scanDataproc` extends the existing `gcp:dataproc` service (was Clusters-only) into a 7-phase
per-region orchestrator: Clusters → AutoscalingPolicy/WorkflowTemplate/Job (parent
`projects/{p}/regions/{region}`) → Batch/Session/SessionTemplate (parent
`projects/{p}/locations/{region}`, per each endpoint's own SDK URL template — not assumed by
analogy to the Regions-scoped trio). Job has no SDK-issued `Name` field (unlike the other 6
types); its NativeID is synthesized from `JobReference.JobId` (confirmed the correct field over
the separate `JobUuid`, by cross-checking the Get/Cancel/Delete URL templates which key on
`jobId`), and its `List` call takes two positional args (`projectId`, `region`) rather than a
single parent string — matches the vendored SDK signature exactly, verified during review rather
than assumed from the other 6 endpoints' single-parent-string shape. The rewrite also introduced
a 3-layer test seam (`scanDataproc` → `scanDataprocWithClient` → `scanDataprocIn`, the latter
taking a pre-resolved `regions []string`) so tests never call the live Compute Regions API; as a
side effect of solving that, `gcpRegions` is now resolved once per project scan and threaded via
`gcpRegionFanoutScanIn` into all 7 phases, instead of each phase's own `gcpRegionFanoutScan`
independently re-resolving the region list (6 redundant `Regions.List` calls removed per scan). An
adversarial review of the parent-string choices, the Job NativeID/positional-List claims, and all
7 response types' pagination support against the vendored SDK source found zero real issues.
Resolver work deferred, per every prior wave this session — this closes out Wave 10 in full.

| Type | Package.Client | List method | Scope |
|---|---|---|---|
| spanner Backup | spanner/v1 ProjectsInstancesBackupsService | List(parent) | fan-out per Instance |
| spanner InstancePartition | spanner/v1 ProjectsInstancesInstancePartitionsService | List(parent) | fan-out per Instance |
| spanner BackupSchedule | spanner/v1 ProjectsInstancesDatabasesBackupSchedulesService | List(parent) | fan-out per Database |
| spanner DatabaseRole | spanner/v1 ProjectsInstancesDatabasesDatabaseRolesService | List(parent) | fan-out per Database |
| spanner InstanceConfig | spanner/v1 ProjectsInstanceConfigsService | List(parent) | project (mix of Google catalog + custom configs) |
| bigtableadmin Backup | bigtableadmin/v2 ProjectsInstancesClustersBackupsService | List(parent) | fan-out per Instance, cluster wildcard `clusters/-` |
| bigtableadmin AppProfile | bigtableadmin/v2 ProjectsInstancesAppProfilesService | List(parent) | project-wide wildcard `instances/-` |
| bigtableadmin Table | bigtableadmin/v2 ProjectsInstancesTablesService | List(parent) | fan-out per Instance |
| bigtableadmin AuthorizedView | bigtableadmin/v2 ProjectsInstancesTablesAuthorizedViewsService | List(parent) | fan-out per Table |
| bigtableadmin LogicalView | bigtableadmin/v2 ProjectsInstancesLogicalViewsService | List(parent) | fan-out per Instance |
| bigtableadmin MaterializedView | bigtableadmin/v2 ProjectsInstancesMaterializedViewsService | List(parent) | fan-out per Instance |
| bigtableadmin SchemaBundle | bigtableadmin/v2 ProjectsInstancesTablesSchemaBundlesService | List(parent) | fan-out per Table |
| bigtableadmin HotTablet | bigtableadmin/v2 ProjectsInstancesClustersHotTabletsService | List(parent) | fan-out per Cluster (no wildcard support) |
| bigtableadmin MemoryLayer | bigtableadmin/v2 ProjectsInstancesClustersMemoryLayersService | List(parent) | fan-out per Instance, cluster wildcard `clusters/-` |
| firestore Backup | firestore/v1 ProjectsLocationsBackupsService | List(parent) | project-wide wildcard `locations/-` |
| firestore BackupSchedule | firestore/v1 ProjectsDatabasesBackupSchedulesService | List(parent) | fan-out per Database |
| firestore UserCred | firestore/v1 ProjectsDatabasesUserCredsService | List(parent) | fan-out per Database |
| bigquery Model | bigquery/v2 ModelsService | List(projectId, datasetId) | fan-out per Dataset |
| bigquery Routine | bigquery/v2 RoutinesService | List(projectId, datasetId) | fan-out per Dataset |
| bigquery RowAccessPolicy | bigquery/v2 RowAccessPoliciesService | List(projectId, datasetId, tableId) | fan-out per Table |
| dataproc AutoscalingPolicy | dataproc/v1 ProjectsRegionsAutoscalingPoliciesService | List(parent) | per-region fan-out (`gcpRegionFanoutScanIn`) |
| dataproc Batches | dataproc/v1 ProjectsLocationsBatchesService | List(parent) | per-region fan-out |
| dataproc Session | dataproc/v1 ProjectsLocationsSessionsService | List(parent) | per-region fan-out |
| dataproc SessionTemplate | dataproc/v1 ProjectsLocationsSessionTemplatesService | List(parent) | per-region fan-out |
| dataproc WorkflowTemplate | dataproc/v1 ProjectsRegionsWorkflowTemplatesService | List(parent) | per-region fan-out |
| dataproc Job | dataproc/v1 ProjectsRegionsJobsService | List(projectId, region) | per-region fan-out, NativeID synthesized from JobReference.JobId |

### Storage/artifact/build/misc secondary — Wave 11

**11a — storage, implemented.** `internal/providers/gcp/storage_scanners.go`'s `scanStorage`
extends the existing `gcp:storage` service (was Buckets-only) into a 3-phase orchestrator:
Buckets (unchanged, now also captures each bucket's `(name, ResourceID)` pair for the fan-out
below) → HmacKeys (per-project, independent of any bucket) → per-bucket fan-out (Notifications,
ManagedFolders, AnywhereCaches, Folders, BucketAccessControls, DefaultObjectAccessControls).
Notifications/BucketAccessControls/DefaultObjectAccessControls use a single `.Do()` call each
(none of the three paginates — verified individually, no `Pages()` method on any of their List
calls); ManagedFolders/AnywhereCaches/Folders/HmacKeys paginate via `.Pages()`. ManagedFolders,
AnywhereCaches, and Folders are opt-in bucket features (hierarchical namespace / cache) that most
buckets in a real project won't have enabled — their List calls 400 rather than returning an empty
page on a bucket lacking the feature, so a narrow `isBucketFeatureNotApplicable` predicate (bare
400, scoped to just these 3 call sites) treats that shape as non-fatal alongside the usual
`isPermissionDenied` path, so one bucket's missing feature can't abort the whole storage scan.
`HmacKeyMetadata` (the List/Get response shape) carries no `Secret` field — only the separate
`HmacKey` struct returned by `Create` does — so no redaction rule is needed, contrary to the
audit's original "flag for redaction rule" note (struck below). Fixed a real `singularize()`
coverage-key bug along the way: `"anywhereCaches"` hit the sibilant-stem `-es` rule (stem
`"anywhereCach"` ends in `"ch"`) and wrongly reduced to `"anywhereCach"` instead of
`"anywhereCache"` — added to `singularizeExceptions`, same ambiguity class as the existing
`databases`/`snoozes` entries. An adversarial review found zero real issues; the bucket
ResourceID/NativeID linkage (`b.SelfLink` used identically for both the upserted row and the
per-bucket-fan-out parent lookup) was independently traced end-to-end through
`RecordHierarchyBatch`/`recordHierarchyTx` and confirmed correct. Resolver work deferred, per every
prior wave this session.

| Type | Package.Client | List method | Scope |
|---|---|---|---|
| storage HmacKey | storage/v1 ProjectsHmacKeysService | List(projectId) | project — `HmacKeyMetadata` list shape carries no secret, no redaction rule needed |
| storage Notification | storage/v1 NotificationsService | List(bucket) | fan-out per Bucket |
| storage ManagedFolder | storage/v1 ManagedFoldersService | List(bucket) | fan-out per Bucket, opt-in feature (400 tolerated) |
| storage AnywhereCache | storage/v1 AnywhereCachesService | List(bucket) | fan-out per Bucket, opt-in feature (400 tolerated) |
| storage Folder | storage/v1 FoldersService | List(bucket) | fan-out per Bucket, opt-in feature (400 tolerated) |
| storage BucketAccessControl | storage/v1 BucketAccessControlsService | List(bucket) | fan-out per Bucket |
| storage DefaultObjectAccessControl | storage/v1 DefaultObjectAccessControlsService | List(bucket) | fan-out per Bucket |

**11b — artifactregistry, implemented.** `internal/providers/gcp/artifactregistry_scanners.go`'s
`scanArtifactRegistry` extends the existing `gcp:artifactregistry` service (was Repositories-only)
into a 3-phase orchestrator: Repositories (unchanged, now also captures each repo's
`(name, ResourceID)` pair) → per-Repository fan-out (Packages, Rules, Attachments) → per-Package
fan-out (Tags, nested two levels deep: Repository → Package → Tag). Re-scoped the original
audit's type list after reading the vendored SDK directly: Version, DockerImage, MavenArtifact,
NpmPackage, and PythonPackage are all NOT scanned — they share Version's cardinality profile (one
row per pushed image/artifact, not per logical package, unbounded on busy/CI-fed registries), and
Tag already captures the graph/security-relevant named subset. PrewarmedArtifact has no resource
`Name` field and — confirmed during review — no List RPC at all (only reachable via
Check/Report/Remove request bodies), so it isn't a listable resource to begin with. An adversarial
review caught one real issue: the code's original comment claimed DockerImage/MavenArtifact/
NpmPackage/PythonPackage's fields are "literally mirrored" into the generic Version resource's
Metadata — checked against the vendored source and only a handful of DockerImage's fields actually
carry that doc annotation; MavenArtifact/NpmPackage/PythonPackage are independently-addressable
resources with their own `Name`/List RPC, not duplicates of anything else scanned. The skip
decision itself still holds (same cardinality argument as Version), but the comment was rewritten
to state that rationale accurately instead of the inaccurate "already captured elsewhere" claim.
The nested per-repository/per-package fan-out phases (Packages/Rules/Attachments/Tags) all run
only after Repositories.List already proves the artifactregistry API enabled, so each applies the
Wave 10d fix pattern: `isAPINotEnabled`-shaped errors warn-and-continue rather than escalating to
the whole-service disabled sentinel — verified consistently applied across all 4 nested call
sites. Resolver work deferred, per every prior wave this session.

| Type | Package.Client | List method | Scope |
|---|---|---|---|
| artifactregistry Package | artifactregistry/v1 ProjectsLocationsRepositoriesPackagesService | List(parent) | fan-out per Repository |
| artifactregistry Tag | artifactregistry/v1 ...PackagesTagsService | List(parent) | fan-out per Package |
| artifactregistry Rule | artifactregistry/v1 ProjectsLocationsRepositoriesRulesService | List(parent) | fan-out per Repository |
| artifactregistry Attachment | artifactregistry/v1 ...RepositoriesAttachmentsService | List(parent) | fan-out per Repository |
| artifactregistry Version | artifactregistry/v1 ...PackagesVersionsService | DROP — same cardinality class as the 4 format-specific views below; Tag captures the named/referenced subset |
| artifactregistry DockerImage | artifactregistry/v1 ...RepositoriesDockerImagesService | DROP — one row per pushed image, unbounded on busy registries |
| artifactregistry MavenArtifact | artifactregistry/v1 ...RepositoriesMavenArtifactsService | DROP — same cardinality class as DockerImage |
| artifactregistry NpmPackage | artifactregistry/v1 ...RepositoriesNpmPackagesService | DROP — same cardinality class as DockerImage |
| artifactregistry PythonPackage | artifactregistry/v1 ...RepositoriesPythonPackagesService | DROP — same cardinality class as DockerImage |
| artifactregistry PrewarmedArtifact | artifactregistry/v1 ...RepositoriesPrewarmedArtifactsService | DROP — no `Name` field, no List RPC at all; not a listable resource |

**11c — cloudbuild, implemented.** `internal/providers/gcp/cloudbuild_scanners.go`'s
`scanCloudBuildTriggers` extends the existing `gcp:cloudbuild` service (was Triggers-only) into a
5-phase orchestrator — the first scanner in the repo to import two API-version packages for the
same service (`cloudbuild/v1` and `cloudbuild/v2`, since 2nd-gen repository Connections only
exist in v2). Triggers (unchanged) → Locations.List (v2, discovers Cloud Build's regional
footprint — v1 has no Locations.List of its own, so this catalog is reused for the v1 WorkerPools
fan-out too, on the disclosed assumption that v1/v2 share one regional deployment rather than two
independently-scoped location catalogs; residual risk noted in code: WorkerPools is the older
product and could in principle exist in a region 2nd-gen Connections hasn't reached) →
WorkerPools (v1) + Connections (v2, capturing refs for the next phase), both fan-out per location
→ Repositories 2nd-gen (v2, fan-out per Connection) → GithubEnterpriseConfig (v1, queried at both
the legacy global parent AND every discovered location's parent, since `GitHubEnterpriseConfig.Name`
is a location-partitioned resource like `BuildTrigger`; both parents hit the identical flexible
`{+parent}/githubEnterpriseConfigs` URL template so an overlap upserts as a no-op). Deliberately
NOT scanned: GitLabConfig, BitbucketServerConfig, and their nested Repo child — both APIs are
self-marked "experimental," superseded by 2nd-gen Connections/Repositories; this is an honest
usage/risk tradeoff, not a duplicate-data claim (BitbucketServerConfig's `PeeredNetwork`/`SslCa`
fields carry real data 2nd-gen Connection doesn't expose, acknowledged in the code comment rather
than glossed over). An adversarial review caught two real bugs: (1) the original code only queried
the legacy global GithubEnterpriseConfigs parent, silently missing any config created at a
location scope — fixed by also fanning out per discovered location; (2) the Locations.List call
(phase 2) and the GithubEnterpriseConfigs calls (phase 5) propagated an `isAPINotEnabled`-shaped
error as the whole-service disabled sentinel even though phase 1's Triggers.List already proved
the service enabled (API enablement is per-service, not per-API-version) — fixed to discard the
sentinel and warn-and-continue, matching the pattern already applied correctly at the other three
nested call sites. Both fixes fail-first verified. Resolver work deferred, per every prior wave
this session.

| Type | Package.Client | List method | Scope |
|---|---|---|---|
| cloudbuild WorkerPool | cloudbuild/v1 ProjectsLocationsWorkerPoolsService | List(parent) | fan-out per location (v2 Locations.List catalog) |
| cloudbuild Connection | **cloudbuild/v2** (new import; existing scanner uses v1) ProjectsLocationsConnectionsService | List(parent) | fan-out per location |
| cloudbuild Repository (2nd-gen) | **cloudbuild/v2** ...ConnectionsRepositoriesService | List(parent) | fan-out per Connection |
| cloudbuild GithubEnterpriseConfig | cloudbuild/v1 ProjectsGithubEnterpriseConfigsService | List(parent) | global parent + fan-out per location (location-partitioned resource) |
| cloudbuild GitLabConfig | cloudbuild/v1 ProjectsLocationsGitLabConfigsService | DROP — experimental API, superseded by 2nd-gen Connections |
| cloudbuild BitbucketServerConfig | cloudbuild/v1 ProjectsLocationsBitbucketServerConfigsService | DROP — experimental API, superseded by 2nd-gen Connections (real but low-usage data loss: PeeredNetwork/SslCa) |
| cloudbuild Repo | cloudbuild/v1 (BitbucketServerConfigs/GitLabConfigs ReposService) | DROP — child of the two dropped VCS configs above |
| run WorkerPool | run/v2 ProjectsLocationsWorkerPoolsService | List(parent) | project |
| run Revision | run/v2 ProjectsLocationsServicesRevisionsService | List(parent) | fan-out per Service (already scanned) |
| run Execution | run/v2 ProjectsLocationsJobsExecutionsService | List(parent) | fan-out per Job (already scanned) |
| run Domainmapping | **run/v1** (new import; existing scanner uses v2) ProjectsLocationsDomainmappingsService | List(parent) | project — legacy Knative API |
| run Authorizeddomain | **run/v1** ProjectsLocationsAuthorizeddomainsService | List(parent) | project |
| container NodePool | container/v1 ProjectsLocationsClustersNodePoolsService | List(parent) | fan-out per Cluster (already scanned) |
| certificatemanager CertificateIssuanceConfig | certificatemanager/v1 ProjectsLocationsCertificateIssuanceConfigsService | List(parent) | project |
| certificatemanager TrustConfig | certificatemanager/v1 ProjectsLocationsTrustConfigsService | List(parent) | project |
| composer UserWorkloadsConfigMap | composer/v1 ProjectsLocationsEnvironmentsUserWorkloadsConfigMapsService | List(parent) | fan-out per Environment |
| dataflow Snapshot | dataflow/v1b3 ProjectsLocationsSnapshotsService | List(projectId, location) | per-region |
| secretmanager Version | secretmanager/v1 ProjectsSecretsVersionsService | List(parent) | fan-out per Secret (already scanned); metadata only, no payload |
| pubsub Snapshot | pubsub/v1 ProjectsSnapshotsService | List(project) | project |

### Admin Directory (partial — most of the 29 uncovered `admin` types)
| Type | Package.Client | List method | Scope |
|---|---|---|---|
| admin Building, Calendar, Feature | admin/directory/v1 ResourcesBuildingsService / CalendarsService / FeaturesService | List(customer) | tenant |
| admin Chromeosdevice, Mobiledevice | admin/directory/v1 ChromeosdevicesService / MobiledevicesService | List(customerId) | tenant |
| admin Domain, DomainAliase (→DomainAlias) | admin/directory/v1 DomainsService / DomainAliasesService | List(customer) | tenant |
| admin Orgunit | admin/directory/v1 OrgunitsService | List(customerId) | tenant |
| admin PrintServer, Printer | admin/directory/v1 CustomersChromePrintServersService / PrintersService | List(parent) | tenant |
| admin Role, RoleAssignment | admin/directory/v1 RolesService / RoleAssignmentsService | List(customer) | tenant (custom admin roles) |
| admin Schema | admin/directory/v1 SchemasService | List(customerId) | tenant |
| admin Asp, Token | admin/directory/v1 AspsService / TokensService | List(userKey) | fan-out per user — **Token is credential-shaped, DROP instead (see below); Asp (app-specific passwords) is borderline, treat as DROP too pending redaction-rule review** |
| admin Transfer | admin/datatransfer/v1 TransfersService | List() | tenant, niche |

## DEFER (2)

```
firestore.../Field	parent is a collectionGroup ID; no bulk "list collection groups" endpoint exists — disco has no enumeration path to reach it yet
firestore.../Indexes	same — revisit if/when Firestore document/collection-group scanning lands
```

## DROP — non-compute (reasoning buckets)

```
# Location catalog (static "where this API is deployed", not a resource)
bigtableadmin.../Location
cloudkms.../Location
certificatemanager.../Location
cloudbuild.../Location (v1 and v2)
batch.../Location
run.../Location
secretmanager.../Location
cloudfunctions.../Location
dataproc.../Location (region-list itself; per-region fan-out target, not a scanned type)

# Operation (LRO polling objects, no durable identity) — one per service, omitted per-line for brevity

# Get-only singletons, no List
admin.../Customer
admin.../Command
admin.../Photo
storage.../ServiceAccount
sqladmin.../Connect
dataproc.../NodeGroup
cloudresourcemanager.../Capability
cloudresourcemanager.../EffectiveTagBindingCollection
cloudresourcemanager.../TagBindingCollection	aggregate view of already-INCLUDEd TagBinding
dns.../Project

# Google-managed catalog/reference data, not user resources
accesscontextmanager.../Permission
accesscontextmanager.../Service
monitoring.../MetricDescriptor
monitoring.../MonitoredResourceDescriptor
monitoring.../NotificationChannelDescriptor
monitoring.../UptimeCheckIp
monitoring.../Metadata	Prometheus metadata catalog
logging.../MonitoredResourceDescriptor
logging.../Log
sqladmin.../Flag
sqladmin.../Tier
cloudfunctions.../Runtime
composer.../ImageVersion
iam.../Role	predefined-role catalog only; custom roles are INCLUDE above
admin.../Privilege
container.../UsableSubnetwork	derived view of already-scanned Compute Subnetwork
bigquery.../Project	duplicate of cloudresourcemanager Project

# Telemetry / execution-log / data-plane content, not inventory
logging.../Entry
logging.../RecentQuery
monitoring.../TimeSery
admin.../Activity
admin.../CustomerUsageReport
admin.../EntityUsageReport
admin.../UserUsageReport
bigquery.../Job
bigquery.../Tabledata
dataflow.../Message
dataflow.../Template	GCS files, not an API collection — no list op
spanner.../Scan
spanner.../Session
storage.../Object
firestore.../Document
dns.../Change
cloudbuild.../Build
batch.../Task
run.../Task
composer.../Workload	live health/status, not a configured resource

# Security-sensitive credential/token material — do not persist without a redaction-rule design first
admin.../VerificationCode	backup codes
admin.../Token	SCIM/OAuth bearer tokens
admin.../Asp	app-specific passwords
iam.../Token
composer.../UserWorkloadsSecret	Kubernetes Secret objects, payload-redaction unconfirmed

# Duplicate — already covered under a different key
admin.../Group	same underlying object as already-scanned cloudidentity.../Group (Cloud Identity is the modern API; Admin SDK Directory Groups is the legacy alias)
admin.../Member	superseded by cloudidentity's modern Membership, which is INCLUDEd instead
run.../Configuration	1:1 Knative shadow of already-scanned Service, no independent config
run.../Route	same — Knative shadow of Service

# Ambiguous / lossy singularization collision (documented, not fixable by a naming-rule tweak)
admin.../Aliase	two distinct Discovery collections (users.aliases, groups.aliases) collide on the same singularized key; whichever is chosen would be a small per-user/per-group fan-out add, not yet prioritized
```
