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

### Addressing (5) — Wave 3
| Type | Method | Scope |
|---|---|---|
| Address | `AddressesService.AggregatedList` | regional |
| GlobalAddress | `GlobalAddressesService.List(project)` | global |
| PublicAdvertisedPrefix | `PublicAdvertisedPrefixesService.List(project)` | global |
| PublicDelegatedPrefix | `PublicDelegatedPrefixesService.AggregatedList` | regional |
| GlobalPublicDelegatedPrefix | `GlobalPublicDelegatedPrefixesService.List(project)` | global |

### Networking core (20) — Wave 4
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

### Interconnect (4) — Wave 5
| Type | Method | Scope |
|---|---|---|
| Interconnect | `InterconnectsService.List(project)` | global |
| InterconnectAttachment | `InterconnectAttachmentsService.AggregatedList` | regional |
| InterconnectGroup | `InterconnectGroupsService.List(project)` | global |
| InterconnectAttachmentGroup | `InterconnectAttachmentGroupsService.List(project)` | global |

### Load balancing / health checks / SSL-TLS (24) — Wave 6
| Type | Method | Scope |
|---|---|---|
| GlobalForwardingRule | `GlobalForwardingRulesService.List(project)` | global |
| HealthCheck | `HealthChecksService.AggregatedList` | global |
| RegionHealthCheck | `RegionHealthChecksService.List(project, region)` | regional |
| RegionCompositeHealthCheck | `RegionCompositeHealthChecksService.AggregatedList` | regional |
| RegionHealthAggregationPolicy | `RegionHealthAggregationPoliciesService.AggregatedList` | regional |
| RegionHealthCheckService | `RegionHealthCheckServicesService.AggregatedList` | regional |
| RegionHealthSource | `RegionHealthSourcesService.AggregatedList` | regional |
| RegionNotificationEndpoint | `RegionNotificationEndpointsService.AggregatedList` | regional |
| HttpHealthCheck | `HttpHealthChecksService.List(project)` | global (legacy, distinct resource) |
| HttpsHealthCheck | `HttpsHealthChecksService.List(project)` | global |
| SslCertificate | `SslCertificatesService.AggregatedList` | global |
| RegionSslCertificate | `RegionSslCertificatesService.List(project, region)` | regional |
| SslPolicy | `SslPoliciesService.AggregatedList` | global |
| RegionSslPolicy | `RegionSslPoliciesService.List(project, region)` | regional |
| TargetSslProxy | `TargetSslProxiesService.List(project)` | global |
| TargetTcpProxy | `TargetTcpProxiesService.AggregatedList` | global |
| RegionTargetTcpProxy | `RegionTargetTcpProxiesService.List(project, region)` | regional |
| TargetGrpcProxy | `TargetGrpcProxiesService.List(project)` | global |
| RegionTargetHttpProxy | `RegionTargetHttpProxiesService.List(project, region)` | regional |
| RegionTargetHttpsProxy | `RegionTargetHttpsProxiesService.List(project, region)` | regional |
| RegionUrlMap | `RegionUrlMapsService.List(project, region)` | regional |
| RegionBackendBucket | `RegionBackendBucketsService.List(project, region)` | regional |
| RegionBackendService | `RegionBackendServicesService.List(project, region)` | regional |
| TargetInstance | `TargetInstancesService.AggregatedList` | zonal |
| TargetPool | `TargetPoolsService.AggregatedList` | regional |

### Autoscaling & reservations (10) — Wave 7
| Type | Method | Scope |
|---|---|---|
| Autoscaler | `AutoscalersService.AggregatedList` | zonal |
| RegionAutoscaler | `RegionAutoscalersService.List(project, region)` | regional |
| Reservation | `ReservationsService.AggregatedList` | zonal |
| ReservationBlock | `ReservationBlocksService.List(project, zone, reservation)` | nested under Reservation |
| ReservationSlot | `ReservationSlotsService.List(project, zone, parentName)` | nested under ReservationBlock |
| ReservationSubBlock | `ReservationSubBlocksService.List(project, zone, parentName)` | nested under Reservation |
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
| Type | Package.Client | List method | Scope |
|---|---|---|---|
| cloudkms CryptoKeyVersion | cloudkms/v1 CryptoKeysCryptoKeyVersionsService | List(parent) | fan-out per CryptoKey |
| cloudkms EkmConnection | cloudkms/v1 EkmConnectionsService | List(parent) | project |
| cloudkms ImportJob | cloudkms/v1 ImportJobsService | List(parent) | fan-out per KeyRing |
| cloudkms KeyHandle | cloudkms/v1 KeyHandlesService | List(parent) | project |
| cloudkms SingleTenantHsmInstance | cloudkms/v1 SingleTenantHsmInstancesService | List(parent) | project |
| cloudresourcemanager TagKey | cloudresourcemanager/v3 TagKeysService | List() | org |
| cloudresourcemanager TagValue | cloudresourcemanager/v3 TagValuesService | List(parent) | fan-out per TagKey |
| cloudresourcemanager TagBinding | cloudresourcemanager/v3 TagBindingsService | List(parent) | fan-out per resource |
| cloudresourcemanager EffectiveTag | cloudresourcemanager/v3 EffectiveTagsService | List(parent) | fan-out per resource |
| cloudresourcemanager TagHold | cloudresourcemanager/v3 TagValuesTagHoldsService | List(parent) | fan-out per TagValue |
| cloudresourcemanager Lien | cloudresourcemanager/v3 LiensService | List(parent) | project |
| accesscontextmanager AccessLevel | accesscontextmanager/v1 AccessPoliciesAccessLevelsService | List(parent) | fan-out per AccessPolicy (already scanned) |
| accesscontextmanager AuthorizedOrgsDesc | accesscontextmanager/v1 AccessPoliciesAuthorizedOrgsDescsService | List(parent) | fan-out per AccessPolicy |
| accesscontextmanager GcpUserAccessBinding | accesscontextmanager/v1 OrganizationsGcpUserAccessBindingsService | List(parent) | org |
| sqladmin BackupRun | sqladmin/v1 BackupRunsService | List(project, instance) | fan-out per Instance |
| sqladmin Database | sqladmin/v1 DatabasesService | List(project, instance) | fan-out per Instance |
| sqladmin SslCert | sqladmin/v1 SslCertsService | List(project, instance) | fan-out per Instance |
| sqladmin User | sqladmin/v1 UsersService | List(project, instance) | fan-out per Instance |
| dns DnsKey | dns/v1 DnsKeysService | List(project, managedZone) | fan-out per zone (already scanned) |
| dns Policy | dns/v1 PoliciesService | List(project) | project |
| dns ResponsePolicy | dns/v1 ResponsePoliciesService | List(project) | project |
| dns ResponsePolicyRule | dns/v1 ResponsePolicyRulesService | List(project, responsePolicy) | fan-out per ResponsePolicy |
| cloudidentity Device | cloudidentity/v1 DevicesService | List() | tenant |
| cloudidentity DeviceUser | cloudidentity/v1 DevicesDeviceUsersService | List(parent) | fan-out per Device |
| cloudidentity ClientState | cloudidentity/v1 DevicesDeviceUsersClientStatesService | List(parent) | fan-out per DeviceUser |
| cloudidentity Membership | cloudidentity/v1 GroupsMembershipsService | List(parent) | fan-out per Group (already scanned) |
| cloudidentity InboundOidcSsoProfile | cloudidentity/v1 InboundOidcSsoProfilesService | List() | tenant |
| cloudidentity InboundSamlSsoProfile | cloudidentity/v1 InboundSamlSsoProfilesService | List() | tenant |
| cloudidentity IdpCredential | cloudidentity/v1 InboundSamlSsoProfilesIdpCredentialsService | List(parent) | fan-out per SSO profile |
| cloudidentity InboundSsoAssignment | cloudidentity/v1 InboundSsoAssignmentsService | List() | tenant |
| cloudidentity Policy | cloudidentity/v1 PoliciesService | List() | tenant |
| cloudidentity Userinvitation | cloudidentity/v1 CustomersUserinvitationsService | List(parent) | tenant |
| iam WorkforcePool | iam/v1 LocationsWorkforcePoolsService | List(location="locations/global") | org — closes ROADMAP R4.23 |
| iam WorkloadIdentityPool | iam/v1 ProjectsLocationsWorkloadIdentityPoolsService | List(parent) | project — closes R4.23 |
| iam Provider (workforce+workload) | iam/v1 (respective ProvidersService) | List(parent) | fan-out per pool — closes R4.23 |
| iam Namespace, ManagedIdentity | iam/v1 (respective Service) | List(parent) | fan-out per pool |
| iam OauthClient, Credential | iam/v1 (respective Service) | List(parent) | project / fan-out |
| iam ScimTenant | iam/v1 LocationsWorkforcePoolsProvidersScimTenantsService | List(parent) | fan-out per Provider |
| iam Role (custom only) | iam/v1 ProjectsRolesService / OrganizationsRolesService | List(parent) | project/org (exclude predefined-role catalog) |

### Observability — Wave 9
| Type | Package.Client | List method | Scope |
|---|---|---|---|
| logging Bucket | logging/v2 ProjectsLocationsBucketsService | List(parent) | fan-out per location |
| logging Exclusion | logging/v2 ProjectsExclusionsService | List(parent) | project |
| logging Metric | logging/v2 ProjectsMetricsService | List(parent) | project |
| logging Link | logging/v2 ProjectsLocationsBucketsLinksService | List(parent) | fan-out per Bucket |
| logging View | logging/v2 ProjectsLocationsBucketsViewsService | List(parent) | fan-out per Bucket (IAM-boundary relevant) |
| logging LogScope | logging/v2 ProjectsLocationsLogScopesService | List(parent) | fan-out per location |
| logging SavedQuery | logging/v2 ProjectsLocationsSavedQueriesService | List(parent) | fan-out per location |
| monitoring Dashboard | monitoring/v1 ProjectsDashboardsService | List(parent) | project |
| monitoring Group | monitoring/v3 ProjectsGroupsService | List(name) | project |
| monitoring Group Member | monitoring/v3 ProjectsGroupsMembersService | List(name) | fan-out per Group |
| monitoring NotificationChannel | monitoring/v3 ProjectsNotificationChannelsService | List(name) | project — some `labels` may carry secrets (Slack/PagerDuty keys), flag for redaction |
| monitoring Service | monitoring/v3 ServicesService | List(parent) | project |
| monitoring ServiceLevelObjective | monitoring/v3 ServicesServiceLevelObjectivesService | List(parent) | fan-out per Service |
| monitoring Snooze | monitoring/v3 ProjectsSnoozesService | List(parent) | project |
| monitoring UptimeCheckConfig | monitoring/v3 ProjectsUptimeCheckConfigsService | List(parent) | project |

### Data services secondary resources — Wave 10
| Type | Package.Client | List method | Scope |
|---|---|---|---|
| spanner Backup | spanner/v1 ProjectsInstancesBackupsService | List(parent) | fan-out per Instance |
| spanner InstancePartition | spanner/v1 ProjectsInstancesInstancePartitionsService | List(parent) | fan-out per Instance |
| spanner BackupSchedule | spanner/v1 ProjectsInstancesDatabasesBackupSchedulesService | List(parent) | fan-out per Database |
| spanner DatabaseRole | spanner/v1 ProjectsInstancesDatabasesDatabaseRolesService | List(parent) | fan-out per Database |
| spanner InstanceConfig | spanner/v1 ProjectsInstanceConfigsService | List(parent) | project (mix of Google catalog + custom configs) |
| bigtableadmin Backup | bigtableadmin/v2 ProjectsInstancesClustersBackupsService | List(parent) | fan-out per Cluster |
| bigtableadmin AppProfile | bigtableadmin/v2 ProjectsInstancesAppProfilesService | List(parent) | fan-out per Instance |
| bigtableadmin Table | bigtableadmin/v2 ProjectsInstancesTablesService | List(parent) | fan-out per Instance |
| bigtableadmin AuthorizedView | bigtableadmin/v2 ProjectsInstancesTablesAuthorizedViewsService | List(parent) | fan-out per Table |
| bigtableadmin LogicalView | bigtableadmin/v2 ProjectsInstancesLogicalViewsService | List(parent) | fan-out per Instance |
| bigtableadmin MaterializedView | bigtableadmin/v2 ProjectsInstancesMaterializedViewsService | List(parent) | fan-out per Instance |
| bigtableadmin SchemaBundle | bigtableadmin/v2 ProjectsInstancesTablesSchemaBundlesService | List(parent) | fan-out per Table |
| bigtableadmin HotTablet | bigtableadmin/v2 ProjectsInstancesClustersHotTabletsService | List(parent) | fan-out per Cluster |
| bigtableadmin MemoryLayer | bigtableadmin/v2 (instance-scoped Service) | List(parent) | fan-out per Instance |
| firestore Backup | firestore/v1 ProjectsLocationsBackupsService | List(parent) | fan-out per location |
| firestore BackupSchedule | firestore/v1 ProjectsDatabasesBackupSchedulesService | List(parent) | fan-out per Database |
| firestore UserCred | firestore/v1 ProjectsDatabasesUserCredsService | List(parent) | fan-out per Database |
| bigquery Model | bigquery/v2 ModelsService | List(projectId, datasetId) | fan-out per Dataset |
| bigquery Routine | bigquery/v2 RoutinesService | List(projectId, datasetId) | fan-out per Dataset |
| bigquery RowAccessPolicy | bigquery/v2 RowAccessPoliciesService | List(projectId, datasetId, tableId) | fan-out per Table |
| dataproc AutoscalingPolicy | dataproc/v1 ProjectsRegionsAutoscalingPoliciesService | List(parent) | per-region fan-out (`gcpRegionFanoutScan`) |
| dataproc Batches | dataproc/v1 ProjectsLocationsBatchesService | List(parent) | per-region fan-out |
| dataproc Session | dataproc/v1 ProjectsLocationsSessionsService | List(parent) | per-region fan-out |
| dataproc SessionTemplate | dataproc/v1 ProjectsLocationsSessionTemplatesService | List(parent) | per-region fan-out |
| dataproc WorkflowTemplate | dataproc/v1 ProjectsRegionsWorkflowTemplatesService | List(parent) | per-region fan-out |
| dataproc Job | dataproc/v1 ProjectsRegionsJobsService | List(projectId, region) | per-region fan-out |

### Storage/artifact/build/misc secondary — Wave 11
| Type | Package.Client | List method | Scope |
|---|---|---|---|
| storage HmacKey | storage/v1 ProjectsHmacKeysService | List(projectId) | project — credential-shaped metadata, flag for redaction rule |
| storage Notification | storage/v1 NotificationsService | List(bucket) | fan-out per Bucket |
| storage ManagedFolder | storage/v1 ManagedFoldersService | List(bucket) | fan-out per Bucket |
| storage AnywhereCache | storage/v1 AnywhereCachesService | List(bucket) | fan-out per Bucket |
| storage Folder | storage/v1 FoldersService | List(bucket) | fan-out per Bucket |
| storage BucketAccessControl | storage/v1 BucketAccessControlsService | List(bucket) | fan-out per Bucket |
| storage DefaultObjectAccessControl | storage/v1 DefaultObjectAccessControlsService | List(bucket) | fan-out per Bucket |
| artifactregistry Package | artifactregistry/v1 ProjectsLocationsRepositoriesPackagesService | List(parent) | fan-out per Repository |
| artifactregistry Version | artifactregistry/v1 ...PackagesVersionsService | List(parent) | fan-out per Package |
| artifactregistry Tag | artifactregistry/v1 ...PackagesTagsService | List(parent) | fan-out per Package |
| artifactregistry Rule | artifactregistry/v1 ProjectsLocationsRepositoriesRulesService | List(parent) | fan-out per Repository |
| artifactregistry Attachment | artifactregistry/v1 ...RepositoriesAttachmentsService | List(parent) | fan-out per Repository |
| artifactregistry DockerImage | artifactregistry/v1 ...RepositoriesDockerImagesService | List(parent) | fan-out per Repository, format-specific view |
| artifactregistry MavenArtifact | artifactregistry/v1 ...RepositoriesMavenArtifactsService | List(parent) | fan-out per Repository |
| artifactregistry NpmPackage | artifactregistry/v1 ...RepositoriesNpmPackagesService | List(parent) | fan-out per Repository |
| artifactregistry PythonPackage | artifactregistry/v1 ...RepositoriesPythonPackagesService | List(parent) | fan-out per Repository |
| artifactregistry PrewarmedArtifact | artifactregistry/v1 ...RepositoriesPrewarmedArtifactsService | List(parent) | fan-out per Repository |
| cloudbuild Connection | **cloudbuild/v2** (new import; existing scanner uses v1) ProjectsLocationsConnectionsService | List(parent) | project |
| cloudbuild Repository (2nd-gen) | **cloudbuild/v2** ...ConnectionsRepositoriesService | List(parent) | fan-out per Connection |
| cloudbuild WorkerPool | cloudbuild/v1 ProjectsLocationsWorkerPoolsService | List(parent) | project |
| cloudbuild GitLabConfig | cloudbuild/v1 ProjectsLocationsGitLabConfigsService | List(parent) | project |
| cloudbuild GithubEnterpriseConfig | cloudbuild/v1 ProjectsGithubEnterpriseConfigsService | List(projectId) | project |
| cloudbuild BitbucketServerConfig | cloudbuild/v1 ProjectsLocationsBitbucketServerConfigsService | List(parent) | project |
| cloudbuild Repo | cloudbuild/v1 (BitbucketServerConfigs/GitLabConfigs ReposService) | List(parent) | fan-out per VCS config |
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
