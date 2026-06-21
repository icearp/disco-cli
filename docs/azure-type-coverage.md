# Azure resource-type coverage ledger

Adjudication of **every uncovered Azure resource type** reported by
`disco coverage services --providers azure --filter uncovered | grep microsoft.`
against the Azure SDK for Go. Verdicts: **INCLUDE** (a scanner is/has been added —
the SDK lists it subscription- or RG-wide), **DEFER** (only lists under a parent
instance — no sub/RG-wide pager; revisit via parent fan-out), **DROP** (not a
listable user resource). Provider-managed resources (auto-materialised + not
user-deletable) are scanned with `managed_by_provider=true` and hidden from
default `list`/`graph`/`check` unless `--include-managed`.

Generated 2026-06-17. Regenerate the candidate set with the coverage command above.

## Totals

| Bucket | Count |
|---|---|
| Top-level types adjudicated | 1900 |
| — INCLUDE (scannable) | 182 |
| — DEFER (parent-scoped) | 32 |
| — DROP (probed: no list / no module / query / third-party) | 304 |
| — DROP (pattern: operations/metadata/action-verb) | 778 |
| — DROP (namespace: third-party / billing / query / metadata RP) | 604 |
| Nested child types (bulk DEFER parent-scoped) | 2319 |

## INCLUDE — scanners added (work-list)

| Type | Module | List op | Item | Managed | Secrets |
|---|---|---|---|---|---|
| microsoft.app/builders | appcontainers/armappcontainers/v3 | BuildersClient.NewListBySubscriptionPager | BuilderResource | false | - |
| microsoft.app/connectedenvironments | appcontainers/armappcontainers/v3 | ConnectedEnvironmentsClient.NewListBySubscriptionPager | ConnectedEnvironment | false | - |
| microsoft.app/jobs | appcontainers/armappcontainers/v3 | JobsClient.NewListBySubscriptionPager | Job | false | properties.configuration.secrets.value |
| microsoft.app/sessionpools | appcontainers/armappcontainers/v3 | ContainerAppsSessionPoolsClient.NewListBySubscriptionPager | SessionPool | false | - |
| microsoft.automanage/bestpractices | automanage/armautomanage | BestPracticesClient.NewListByTenantPager | BestPractice | true | - |
| microsoft.automanage/configurationprofileassignments | automanage/armautomanage | ConfigurationProfileAssignmentsClient.NewListBySubscriptionPager | ConfigurationProfileAssignment | false | - |
| microsoft.automanage/serviceprincipals | automanage/armautomanage | ServicePrincipalsClient.NewListBySubscriptionPager | ServicePrincipal | false | - |
| microsoft.azurearcdata/postgresinstances | azurearcdata/armazurearcdata | PostgresInstancesClient.NewListPager | PostgresInstance | false | properties.basicLoginInformation.password |
| microsoft.azurearcdata/sqlmanagedinstances | azurearcdata/armazurearcdata | SQLManagedInstancesClient.NewListPager | SQLManagedInstance | false | properties.basicLoginInformation.password |
| microsoft.azurearcdata/sqlserverinstances | azurearcdata/armazurearcdata | SQLServerInstancesClient.NewListPager | SQLServerInstance | false | - |
| microsoft.azurelargeinstance/azurelargestorageinstances | largeinstance/armlargeinstance | AzureLargeStorageInstanceClient.NewListBySubscriptionPager | AzureLargeStorageInstance | false | - |
| microsoft.azureresiliencemanagement/usageplans | resiliencemanagement/armresiliencemanagement | UsagePlansClient.NewListBySubscriptionPager | UsagePlan | false | - |
| microsoft.azurestackhci/galleryimages | azurestackhci/armazurestackhcivm | GalleryImagesClient.NewListAllPager | GalleryImage | false | - |
| microsoft.azurestackhci/logicalnetworks | azurestackhci/armazurestackhcivm | LogicalNetworksClient.NewListAllPager | LogicalNetwork | false | - |
| microsoft.azurestackhci/marketplacegalleryimages | azurestackhci/armazurestackhcivm | MarketplaceGalleryImagesClient.NewListAllPager | MarketplaceGalleryImage | false | - |
| microsoft.azurestackhci/networkinterfaces | azurestackhci/armazurestackhcivm | NetworkInterfacesClient.NewListAllPager | NetworkInterface | false | - |
| microsoft.azurestackhci/networksecuritygroups | azurestackhci/armazurestackhcivm | NetworkSecurityGroupsClient.NewListAllPager | NetworkSecurityGroup | false | - |
| microsoft.azurestackhci/storagecontainers | azurestackhci/armazurestackhcivm | StorageContainersClient.NewListAllPager | StorageContainer | false | - |
| microsoft.azurestackhci/virtualharddisks | azurestackhci/armazurestackhcivm | VirtualHardDisksClient.NewListAllPager | VirtualHardDisk | false | - |
| microsoft.blueprint/blueprintassignments | blueprint/armblueprint | AssignmentsClient.NewListPager | Assignment | false | - |
| microsoft.blueprint/blueprints | blueprint/armblueprint | BlueprintsClient.NewListPager | Blueprint | false | - |
| microsoft.cache/redisenterprise | redisenterprise/armredisenterprise | Client.NewListPager | Cluster | false | - |
| microsoft.cdn/cdnwebapplicationfirewallpolicies | cdn/armcdn | PoliciesClient.NewListPager | WebApplicationFirewallPolicy | false | - |
| microsoft.cloudhealth/healthmodels | cloudhealth/armcloudhealth | HealthModelsClient.NewListBySubscriptionPager | HealthModel | false | - |
| microsoft.codesigning/codesigningaccounts | trustedsigning/armtrustedsigning | CodeSigningAccountsClient.NewListBySubscriptionPager | CodeSigningAccount | false | - |
| microsoft.cognitiveservices/commitmentplans | cognitiveservices/armcognitiveservices | CommitmentPlansClient.NewListPlansBySubscriptionPager | CommitmentPlan | false | - |
| microsoft.connectedcache/enterprisecustomers | connectedcache/armconnectedcache | EnterpriseMccCustomersClient.NewListBySubscriptionPager | EnterpriseMccCustomerResource | false | - |
| microsoft.connectedcache/enterprisemcccustomers | connectedcache/armconnectedcache | EnterpriseMccCustomersClient.NewListBySubscriptionPager | EnterpriseMccCustomerResource | false | - |
| microsoft.connectedcache/ispcustomers | connectedcache/armconnectedcache | IspCustomersClient.NewListBySubscriptionPager | IspCustomerResource | false | - |
| microsoft.connectedvmwarevsphere/clusters | connectedvmware/armconnectedvmware | ClustersClient.NewListPager | Cluster | false | - |
| microsoft.connectedvmwarevsphere/datastores | connectedvmware/armconnectedvmware | DatastoresClient.NewListPager | Datastore | false | - |
| microsoft.connectedvmwarevsphere/hosts | connectedvmware/armconnectedvmware | HostsClient.NewListPager | Host | false | - |
| microsoft.connectedvmwarevsphere/resourcepools | connectedvmware/armconnectedvmware | ResourcePoolsClient.NewListPager | ResourcePool | false | - |
| microsoft.connectedvmwarevsphere/virtualmachinetemplates | connectedvmware/armconnectedvmware | VirtualMachineTemplatesClient.NewListPager | VirtualMachineTemplate | false | - |
| microsoft.connectedvmwarevsphere/virtualnetworks | connectedvmware/armconnectedvmware | VirtualNetworksClient.NewListPager | VirtualNetwork | false | - |
| microsoft.containerservice/fleets | containerservicefleet/armcontainerservicefleet/v3 | FleetsClient.NewListBySubscriptionPager | Fleet | false | - |
| microsoft.containerservice/snapshots | containerservice/armcontainerservice/v6 | SnapshotsClient.NewListPager | Snapshot | false | - |
| microsoft.customproviders/resourceproviders | customproviders/armcustomproviders | CustomResourceProviderClient.NewListBySubscriptionPager | CustomRPManifest | false | - |
| microsoft.dashboard/dashboards | dashboard/armdashboard | GrafanaClient.NewListPager | ManagedGrafana | false | - |
| microsoft.databricks/accessconnectors | databricks/armdatabricks | AccessConnectorsClient.NewListBySubscriptionPager | AccessConnector | false | - |
| microsoft.datalakeanalytics/accounts | datalake-analytics/armdatalakeanalytics | AccountsClient.NewListPager | AccountBasic | false | - |
| microsoft.dataprotection/resourceguards | dataprotection/armdataprotection | ResourceGuardsClient.NewGetResourcesInSubscriptionPager | ResourceGuardResource | false | - |
| microsoft.datareplication/replicationfabrics | recoveryservicesdatareplication/armrecoveryservicesdatareplication | FabricClient.NewListBySubscriptionPager | FabricModel | false | - |
| microsoft.datareplication/replicationvaults | recoveryservicesdatareplication/armrecoveryservicesdatareplication | VaultClient.NewListBySubscriptionPager | VaultModel | false | - |
| microsoft.dbformariadb/servers | mariadb/armmariadb | ServersClient.NewListPager | Server | false | - |
| microsoft.dbformysql/servers | mysql/armmysql | ServersClient.NewListPager | Server | false | - |
| microsoft.dbforpostgresql/servers | postgresql/armpostgresql | ServersClient.NewListPager | Server | false | - |
| microsoft.dependencymap/maps | dependencymap/armdependencymap | MapsClient.NewListBySubscriptionPager | MapsResource | false | - |
| microsoft.desktopvirtualization/appattachpackages | desktopvirtualization/armdesktopvirtualization/v2 | AppAttachPackageClient.NewListBySubscriptionPager | AppAttachPackage | false | - |
| microsoft.devcenter/networkconnections | devcenter/armdevcenter | NetworkConnectionsClient.NewListBySubscriptionPager | NetworkConnection | false | - |
| microsoft.devcenter/projects | devcenter/armdevcenter | ProjectsClient.NewListBySubscriptionPager | Project | false | - |
| microsoft.deviceregistry/assetendpointprofiles | deviceregistry/armdeviceregistry | AssetEndpointProfilesClient.NewListBySubscriptionPager | AssetEndpointProfile | false | - |
| microsoft.deviceregistry/billingcontainers | deviceregistry/armdeviceregistry | BillingContainersClient.NewListBySubscriptionPager | BillingContainer | true | - |
| microsoft.devices/provisioningservices | deviceprovisioningservices/armdeviceprovisioningservices | IotDpsResourceClient.NewListBySubscriptionPager | ProvisioningServiceDescription | false | - |
| microsoft.devtestlab/schedules | devtestlabs/armdevtestlabs | GlobalSchedulesClient.NewListBySubscriptionPager | Schedule | false | - |
| microsoft.documentdb/cassandraclusters | cosmos/armcosmos | CassandraClustersClient.NewListBySubscriptionPager | ClusterResource | false | - |
| microsoft.documentdb/restorabledatabaseaccounts | cosmos/armcosmos | RestorableDatabaseAccountsClient.NewListPager | RestorableDatabaseAccountGetResult | true | - |
| microsoft.edgemarketplace/offers | edgemarketplace/armedgemarketplace | OffersClient.NewListBySubscriptionPager | Offer | false | - |
| microsoft.edgemarketplace/publishers | edgemarketplace/armedgemarketplace | PublishersClient.NewListBySubscriptionPager | Publisher | false | - |
| microsoft.edgeorder/addresses | edgeorder/armedgeorder | ManagementClient.NewListAddressesAtSubscriptionLevelPager | AddressResource | false | - |
| microsoft.edgeorder/orders | edgeorder/armedgeorder | ManagementClient.NewListOrderAtSubscriptionLevelPager | Order | false | - |
| microsoft.elastic/monitors | elastic/armelastic | MonitorsClient.NewListPager | MonitorResource | false | - |
| microsoft.eventgrid/namespaces | eventgrid/armeventgrid/v2 | NamespacesClient.NewListBySubscriptionPager | Namespace | false | - |
| microsoft.eventgrid/partnerconfigurations | eventgrid/armeventgrid/v2 | PartnerConfigurationsClient.NewListBySubscriptionPager | PartnerConfiguration | false | - |
| microsoft.eventgrid/partnernamespaces | eventgrid/armeventgrid/v2 | PartnerNamespacesClient.NewListBySubscriptionPager | PartnerNamespace | false | - |
| microsoft.eventgrid/partnerregistrations | eventgrid/armeventgrid/v2 | PartnerRegistrationsClient.NewListBySubscriptionPager | PartnerRegistration | false | - |
| microsoft.eventgrid/partnertopics | eventgrid/armeventgrid/v2 | PartnerTopicsClient.NewListBySubscriptionPager | PartnerTopic | false | - |
| microsoft.eventhub/clusters | eventhub/armeventhub | ClustersClient.NewListBySubscriptionPager | Cluster | false | - |
| microsoft.horizondb/parametergroups | horizondb/armhorizondb | ParameterGroupsClient.NewListBySubscriptionPager | ParameterGroup | false | - |
| microsoft.hybridcompute/privatelinkscopes | hybridcompute/armhybridcompute | PrivateLinkScopesClient.NewListPager | HybridComputePrivateLinkScope | false | - |
| microsoft.hybridconnectivity/publiccloudconnectors | hybridconnectivity/armhybridconnectivity | PublicCloudConnectorsClient.NewListBySubscriptionPager | PublicCloudConnector | false | - |
| microsoft.hybridnetwork/devices | hybridnetwork/armhybridnetwork | DevicesClient.NewListBySubscriptionPager | Device | false | - |
| microsoft.keyvault/managedhsms | keyvault/armkeyvault | ManagedHsmsClient.NewListBySubscriptionPager | ManagedHsm | false | - |
| microsoft.labservices/labplans | labservices/armlabservices | LabPlansClient.NewListBySubscriptionPager | LabPlan | false | - |
| microsoft.logic/integrationaccounts | logic/armlogic | IntegrationAccountsClient.NewListBySubscriptionPager | IntegrationAccount | false | - |
| microsoft.logic/integrationserviceenvironments | logic/armlogic | IntegrationServiceEnvironmentsClient.NewListBySubscriptionPager | IntegrationServiceEnvironment | false | - |
| microsoft.machinelearningservices/registries | machinelearning/armmachinelearning/v4 | RegistriesClient.NewListBySubscriptionPager | Registry | false | - |
| microsoft.maintenance/configurationassignments | maintenance/armmaintenance | ConfigurationAssignmentsWithinSubscriptionClient.NewListPager | ConfigurationAssignment | false | - |
| microsoft.maintenance/publicmaintenanceconfigurations | maintenance/armmaintenance | PublicMaintenanceConfigurationsClient.NewListPager | MaintenanceConfiguration | true | - |
| microsoft.managednetworkfabric/accesscontrollists | managednetworkfabric/armmanagednetworkfabric | AccessControlListsClient.NewListBySubscriptionPager | AccessControlList | false | - |
| microsoft.managednetworkfabric/fabrics | managednetworkfabric/armmanagednetworkfabric | NetworkFabricsClient.NewListBySubscriptionPager | NetworkFabric | false | - |
| microsoft.managednetworkfabric/internetgatewayrules | managednetworkfabric/armmanagednetworkfabric | InternetGatewayRulesClient.NewListBySubscriptionPager | InternetGatewayRule | false | - |
| microsoft.managednetworkfabric/internetgateways | managednetworkfabric/armmanagednetworkfabric | InternetGatewaysClient.NewListBySubscriptionPager | InternetGateway | false | - |
| microsoft.managednetworkfabric/ipcommunities | managednetworkfabric/armmanagednetworkfabric | IPCommunitiesClient.NewListBySubscriptionPager | IPCommunity | false | - |
| microsoft.managednetworkfabric/ipextendedcommunities | managednetworkfabric/armmanagednetworkfabric | IPExtendedCommunitiesClient.NewListBySubscriptionPager | IPExtendedCommunity | false | - |
| microsoft.managednetworkfabric/ipprefixes | managednetworkfabric/armmanagednetworkfabric | IPPrefixesClient.NewListBySubscriptionPager | IPPrefix | false | - |
| microsoft.managednetworkfabric/l2isolationdomains | managednetworkfabric/armmanagednetworkfabric | L2IsolationDomainsClient.NewListBySubscriptionPager | L2IsolationDomain | false | - |
| microsoft.managednetworkfabric/l3isolationdomains | managednetworkfabric/armmanagednetworkfabric | L3IsolationDomainsClient.NewListBySubscriptionPager | L3IsolationDomain | false | - |
| microsoft.managednetworkfabric/neighborgroups | managednetworkfabric/armmanagednetworkfabric | NeighborGroupsClient.NewListBySubscriptionPager | NeighborGroup | false | - |
| microsoft.managednetworkfabric/networkdevices | managednetworkfabric/armmanagednetworkfabric | NetworkDevicesClient.NewListBySubscriptionPager | NetworkDevice | false | - |
| microsoft.managednetworkfabric/networkfabriccontrollers | managednetworkfabric/armmanagednetworkfabric | NetworkFabricControllersClient.NewListBySubscriptionPager | NetworkFabricController | false | - |
| microsoft.managednetworkfabric/networkpacketbrokers | managednetworkfabric/armmanagednetworkfabric | NetworkPacketBrokersClient.NewListBySubscriptionPager | NetworkPacketBroker | false | - |
| microsoft.managednetworkfabric/networkracks | managednetworkfabric/armmanagednetworkfabric | NetworkRacksClient.NewListBySubscriptionPager | NetworkRack | false | - |
| microsoft.managednetworkfabric/networktaprules | managednetworkfabric/armmanagednetworkfabric | NetworkTapRulesClient.NewListBySubscriptionPager | NetworkTapRule | false | - |
| microsoft.managednetworkfabric/networktaps | managednetworkfabric/armmanagednetworkfabric | NetworkTapsClient.NewListBySubscriptionPager | NetworkTap | false | - |
| microsoft.managednetworkfabric/routepolicies | managednetworkfabric/armmanagednetworkfabric | RoutePoliciesClient.NewListBySubscriptionPager | RoutePolicy | false | - |
| microsoft.managedservices/marketplaceregistrationdefinitions | managedservices/armmanagedservices | MarketplaceRegistrationDefinitionsClient.NewListPager | MarketplaceRegistrationDefinition | false | - |
| microsoft.managedservices/registrationassignments | managedservices/armmanagedservices | RegistrationAssignmentsClient.NewListPager | RegistrationAssignment | false | - |
| microsoft.network/applicationgatewaywebapplicationfirewallpolicies | network/armnetwork/v6 | WebApplicationFirewallPoliciesClient.NewListAllPager | WebApplicationFirewallPolicy | false | - |
| microsoft.network/applicationsecuritygroups | network/armnetwork/v6 | ApplicationSecurityGroupsClient.NewListAllPager | ApplicationSecurityGroup | false | - |
| microsoft.network/azurefirewallfqdntags | network/armnetwork/v6 | AzureFirewallFqdnTagsClient.NewListAllPager | AzureFirewallFqdnTag | true | - |
| microsoft.network/azurefirewalls | network/armnetwork/v6 | AzureFirewallsClient.NewListAllPager | AzureFirewall | false | - |
| microsoft.network/azurewebcategories | network/armnetwork/v6 | WebCategoriesClient.NewListBySubscriptionPager | AzureWebCategory | true | - |
| microsoft.network/bastionhosts | network/armnetwork/v6 | BastionHostsClient.NewListPager | BastionHost | false | - |
| microsoft.network/bgpservicecommunities | network/armnetwork/v6 | BgpServiceCommunitiesClient.NewListPager | BgpServiceCommunity | true | - |
| microsoft.networkcloud/accessbridges | networkcloud/armnetworkcloud | AccessBridgesClient.NewListBySubscriptionPager | AccessBridge | false | - |
| microsoft.networkcloud/baremetalmachines | networkcloud/armnetworkcloud | BareMetalMachinesClient.NewListBySubscriptionPager | BareMetalMachine | false | - |
| microsoft.networkcloud/cloudservicesnetworks | networkcloud/armnetworkcloud | CloudServicesNetworksClient.NewListBySubscriptionPager | CloudServicesNetwork | false | - |
| microsoft.networkcloud/clustermanagers | networkcloud/armnetworkcloud | ClusterManagersClient.NewListBySubscriptionPager | ClusterManager | false | - |
| microsoft.networkcloud/kubernetesclusters | networkcloud/armnetworkcloud | KubernetesClustersClient.NewListBySubscriptionPager | KubernetesCluster | false | - |
| microsoft.networkcloud/kubernetesversions | networkcloud/armnetworkcloud | KubernetesVersionsClient.NewListBySubscriptionPager | KubernetesVersion | true | - |
| microsoft.networkcloud/l2networks | networkcloud/armnetworkcloud | L2NetworksClient.NewListBySubscriptionPager | L2Network | false | - |
| microsoft.networkcloud/l3networks | networkcloud/armnetworkcloud | L3NetworksClient.NewListBySubscriptionPager | L3Network | false | - |
| microsoft.networkcloud/rackskus | networkcloud/armnetworkcloud | RackSKUsClient.NewListBySubscriptionPager | RackSKU | true | - |
| microsoft.networkcloud/racks | networkcloud/armnetworkcloud | RacksClient.NewListBySubscriptionPager | Rack | false | - |
| microsoft.networkcloud/storageappliances | networkcloud/armnetworkcloud | StorageAppliancesClient.NewListBySubscriptionPager | StorageAppliance | false | - |
| microsoft.networkcloud/trunkednetworks | networkcloud/armnetworkcloud | TrunkedNetworksClient.NewListBySubscriptionPager | TrunkedNetwork | false | - |
| microsoft.networkcloud/virtualmachines | networkcloud/armnetworkcloud | VirtualMachinesClient.NewListBySubscriptionPager | VirtualMachine | false | - |
| microsoft.networkcloud/volumes | networkcloud/armnetworkcloud | VolumesClient.NewListBySubscriptionPager | Volume | false | - |
| microsoft.network/connections | network/armnetwork/v6 | VirtualNetworkGatewayConnectionsClient.NewListPager | VirtualNetworkGatewayConnection | false | properties.sharedKey |
| microsoft.network/customipprefixes | network/armnetwork/v6 | CustomIPPrefixesClient.NewListAllPager | CustomIPPrefix | false | - |
| microsoft.network/ddosprotectionplans | network/armnetwork/v6 | DdosProtectionPlansClient.NewListPager | DdosProtectionPlan | false | - |
| microsoft.network/dnsforwardingrulesets | dnsresolver/armdnsresolver | DNSForwardingRulesetsClient.NewListPager | DNSForwardingRuleset | false | - |
| microsoft.network/dnsresolverdomainlists | dnsresolver/armdnsresolver | DomainListsClient.NewListPager | DNSResolverDomainList | false | - |
| microsoft.network/dnsresolverpolicies | dnsresolver/armdnsresolver | PoliciesClient.NewListPager | DNSResolverPolicy | false | - |
| microsoft.network/dnsresolvers | dnsresolver/armdnsresolver | DNSResolversClient.NewListPager | DNSResolver | false | - |
| microsoft.network/dscpconfigurations | network/armnetwork/v6 | DscpConfigurationClient.NewListAllPager | DscpConfiguration | false | - |
| microsoft.network/expressrouteportslocations | network/armnetwork/v6 | ExpressRoutePortsLocationsClient.NewListPager | ExpressRoutePortsLocation | true | - |
| microsoft.network/expressrouteports | network/armnetwork/v6 | ExpressRoutePortsClient.NewListPager | ExpressRoutePort | false | - |
| microsoft.network/expressrouteserviceproviders | network/armnetwork/v6 | ExpressRouteServiceProvidersClient.NewListPager | ExpressRouteServiceProvider | true | - |
| microsoft.network/firewallpolicies | network/armnetwork/v6 | FirewallPoliciesClient.NewListAllPager | FirewallPolicy | false | - |
| microsoft.network/frontdoors | frontdoor/armfrontdoor | FrontDoorsClient.NewListPager | FrontDoor | false | - |
| microsoft.network/frontdoorwebapplicationfirewallmanagedrulesets | frontdoor/armfrontdoor | ManagedRuleSetsClient.NewListPager | ManagedRuleSetDefinition | true | - |
| microsoft.network/frontdoorwebapplicationfirewallpolicies | frontdoor/armfrontdoor | PoliciesClient.NewListBySubscriptionPager | WebApplicationFirewallPolicy | false | - |
| microsoft.network/ipallocations | network/armnetwork/v6 | IPAllocationsClient.NewListPager | IPAllocation | false | - |
| microsoft.network/ipgroups | network/armnetwork/v6 | IPGroupsClient.NewListPager | IPGroup | false | - |
| microsoft.network/loadbalancers | network/armnetwork/v6 | LoadBalancersClient.NewListAllPager | LoadBalancer | false | - |
| microsoft.network/localnetworkgateways | network/armnetwork/v6 | LocalNetworkGatewaysClient.NewListPager | LocalNetworkGateway | false | - |
| microsoft.network/natgateways | network/armnetwork/v6 | NatGatewaysClient.NewListAllPager | NatGateway | false | - |
| microsoft.network/networkexperimentprofiles | frontdoor/armfrontdoor | NetworkExperimentProfilesClient.NewListPager | Profile | false | - |
| microsoft.network/networkinterfaces | network/armnetwork/v6 | InterfacesClient.NewListAllPager | Interface | false | - |
| microsoft.network/networkmanagerconnections | network/armnetwork/v6 | SubscriptionNetworkManagerConnectionsClient.NewListPager | ManagerConnection | false | - |
| microsoft.network/networkmanagers | network/armnetwork/v6 | ManagersClient.NewListBySubscriptionPager | Manager | false | - |
| microsoft.network/networkprofiles | network/armnetwork/v6 | ProfilesClient.NewListAllPager | Profile | false | - |
| microsoft.network/networkvirtualappliances | network/armnetwork/v6 | VirtualAppliancesClient.NewListPager | VirtualAppliance | false | - |
| microsoft.network/networkwatchers | network/armnetwork/v6 | WatchersClient.NewListAllPager | Watcher | false | - |
| microsoft.network/p2svpngateways | network/armnetwork/v6 | P2SVPNGatewaysClient.NewListPager | P2SVPNGateway | false | - |
| microsoft.network/privatelinkservices | network/armnetwork/v6 | PrivateLinkServicesClient.NewListBySubscriptionPager | PrivateLinkService | false | - |
| microsoft.network/publicipprefixes | network/armnetwork/v6 | PublicIPPrefixesClient.NewListAllPager | PublicIPPrefix | false | - |
| microsoft.network/routefilters | network/armnetwork/v6 | RouteFiltersClient.NewListPager | RouteFilter | false | - |
| microsoft.network/routetables | network/armnetwork/v6 | RouteTablesClient.NewListAllPager | RouteTable | false | - |
| microsoft.network/securitypartnerproviders | network/armnetwork/v6 | SecurityPartnerProvidersClient.NewListPager | SecurityPartnerProvider | false | - |
| microsoft.network/serviceendpointpolicies | network/armnetwork/v6 | ServiceEndpointPoliciesClient.NewListPager | ServiceEndpointPolicy | false | - |
| microsoft.network/virtualnetworktaps | network/armnetwork/v6 | VirtualNetworkTapsClient.NewListAllPager | VirtualNetworkTap | false | - |
| microsoft.network/virtualrouters | network/armnetwork/v6 | VirtualRoutersClient.NewListPager | VirtualRouter | false | - |
| microsoft.network/vpnserverconfigurations | network/armnetwork/v6 | VPNServerConfigurationsClient.NewListPager | VPNServerConfiguration | false | properties.radiusServerSecret |
| microsoft.operationalinsights/clusters | operationalinsights/armoperationalinsights | ClustersClient.NewListPager | Cluster | false | - |
| microsoft.operationsmanagement/solutions | operationsmanagement/armoperationsmanagement | SolutionsClient.ListBySubscription | Solution | false | - |
| microsoft.orbital/geocatalogs | planetarycomputer/armplanetarycomputer | GeoCatalogsClient.NewListBySubscriptionPager | GeoCatalog | false | - |
| microsoft.peering/peerasns | peering/armpeering | PeerAsnsClient.NewListBySubscriptionPager | PeerAsn | false | - |
| microsoft.peering/peeringservices | peering/armpeering | ServicesClient.NewListBySubscriptionPager | Service | false | - |
| microsoft.powerbidedicated/autoscalevcores | powerbidedicated/armpowerbidedicated | AutoScaleVCoresClient.NewListBySubscriptionPager | AutoScaleVCore | false | - |
| microsoft.powerplatform/accounts | powerplatform/armpowerplatform | AccountsClient.NewListBySubscriptionPager | Account | false | - |
| microsoft.saas/applications | saas/armsaas | ApplicationsClient.NewListPager | App | false | - |
| microsoft.saas/resources | saas/armsaas | SubscriptionLevelClient.NewListByAzureSubscriptionPager | Resource | false | - |
| microsoft.scvmm/availabilitysets | scvmm/armscvmm | AvailabilitySetsClient.NewListBySubscriptionPager | AvailabilitySet | false | - |
| microsoft.scvmm/clouds | scvmm/armscvmm | CloudsClient.NewListBySubscriptionPager | Cloud | false | - |
| microsoft.scvmm/virtualmachinetemplates | scvmm/armscvmm | VirtualMachineTemplatesClient.NewListBySubscriptionPager | VirtualMachineTemplate | false | - |
| microsoft.scvmm/virtualnetworks | scvmm/armscvmm | VirtualNetworksClient.NewListBySubscriptionPager | VirtualNetwork | false | - |
| microsoft.servicefabric/managedclusters | servicefabricmanagedclusters/armservicefabricmanagedclusters | ManagedClustersClient.NewListBySubscriptionPager | ManagedCluster | false | - |
| microsoft.solutions/applicationdefinitions | solutions/armmanagedapplications | ApplicationDefinitionsClient.NewListBySubscriptionPager | ApplicationDefinition | false | - |
| microsoft.solutions/applications | solutions/armmanagedapplications | ApplicationsClient.NewListBySubscriptionPager | Application | false | - |
| microsoft.solutions/jitrequests | solutions/armmanagedapplications | JitRequestsClient.ListBySubscription | JitRequestDefinition | false | - |
| microsoft.sqlvirtualmachine/sqlvirtualmachinegroups | sqlvirtualmachine/armsqlvirtualmachine | GroupsClient.NewListPager | SQLVirtualMachineGroup | false | - |
| microsoft.storage/storagetasks | storageactions/armstorageactions | StorageTasksClient.NewListBySubscriptionPager | StorageTask | false | - |
| microsoft.streamanalytics/clusters | streamanalytics/armstreamanalytics | ClustersClient.NewListBySubscriptionPager | Cluster | false | - |
| microsoft.synapse/privatelinkhubs | synapse/armsynapse | PrivateLinkHubsClient.NewListPager | PrivateLinkHub | false | - |
| microsoft.virtualmachineimages/imagetemplates | virtualmachineimagebuilder/armvirtualmachineimagebuilder | VirtualMachineImageTemplatesClient.NewListPager | ImageTemplate | false | - |
| microsoft.web/containerapps | appservice/armappservice | ContainerAppsClient.NewListBySubscriptionPager | ContainerApp | false | - |
| microsoft.workloads/monitors | workloads/armworkloads | MonitorsClient.NewListPager | Monitor | false | - |

## DEFER — parent-scoped (no subscription/RG-wide list)

| Type | Module | List op | Secrets |
|---|---|---|---|
| microsoft.apimanagement/gateways | apimanagement/armapimanagement | GatewayClient.NewListByServicePager | - |
| microsoft.app/logicapps | appcontainers/armappcontainers/v3 | LogicAppsClient.NewListWorkflowsPager | - |
| microsoft.azureresiliencemanagement/drills | resiliencemanagement/armresiliencemanagement | DrillsClient.NewListPager | - |
| microsoft.azureresiliencemanagement/goalassignments | resiliencemanagement/armresiliencemanagement | GoalAssignmentsClient.NewListPager | - |
| microsoft.azureresiliencemanagement/goaltemplates | resiliencemanagement/armresiliencemanagement | GoalTemplatesClient.NewListPager | - |
| microsoft.azureresiliencemanagement/recoveryplans | resiliencemanagement/armresiliencemanagement | RecoveryPlansClient.NewListPager | - |
| microsoft.azureresiliencemanagement/unifiedresilienceitems | resiliencemanagement/armresiliencemanagement | UnifiedResilienceItemsClient.NewListPager | - |
| microsoft.azurestackhci/virtualmachineinstances | azurestackhci/armazurestackhcivm | VirtualMachineInstancesClient.NewListPager | - |
| microsoft.chaos/targets | chaos/armchaos | TargetsClient.NewListPager | - |
| microsoft.cognitiveservices/raipolicy | cognitiveservices/armcognitiveservices | RaiPoliciesClient.NewListPager | - |
| microsoft.connectedcache/cachenodes | connectedcache/armconnectedcache | IspCacheNodesOperationsClient.NewListByIspCustomerResourcePager | - |
| microsoft.connectedvmwarevsphere/virtualmachineinstances | connectedvmware/armconnectedvmware | VirtualMachineInstancesClient.NewListPager | - |
| microsoft.containerservice/fleetmemberships | containerservicefleet/armcontainerservicefleet/v3 | FleetMembersClient.NewListByFleetPager | - |
| microsoft.customproviders/associations | customproviders/armcustomproviders | AssociationsClient.NewListAllPager | - |
| microsoft.dataprotection/backupinstances | dataprotection/armdataprotection | BackupInstancesClient.NewListPager | - |
| microsoft.devopsinfrastructure/images | devopsinfrastructure/armdevopsinfrastructure | ImageVersionsClient.NewListByImagePager | - |
| microsoft.devopsinfrastructure/resources | devopsinfrastructure/armdevopsinfrastructure | ResourceDetailsClient.NewListByPoolPager | - |
| microsoft.guestconfiguration/guestconfigurationassignments | guestconfiguration/armguestconfiguration | AssignmentsClient.NewListPager | - |
| microsoft.hybridconnectivity/endpoints | hybridconnectivity/armhybridconnectivity | EndpointsClient.NewListPager | - |
| microsoft.hybridconnectivity/solutionconfigurations | hybridconnectivity/armhybridconnectivity | SolutionConfigurationsClient.NewListPager | - |
| microsoft.hybridcontainerservice/provisionedclusterinstances | hybridcontainerservice/armhybridcontainerservice | ProvisionedClusterInstancesClient.NewListPager | - |
| microsoft.kubernetesconfiguration/extensions | kubernetesconfiguration/armkubernetesconfiguration | ExtensionsClient.NewListPager | - |
| microsoft.kubernetesconfiguration/fluxconfigurations | kubernetesconfiguration/armkubernetesconfiguration | FluxConfigurationsClient.NewListPager | - |
| microsoft.kubernetesconfiguration/sourcecontrolconfigurations | kubernetesconfiguration/armkubernetesconfiguration | SourceControlConfigurationsClient.NewListPager | - |
| microsoft.labservices/users | labservices/armlabservices | UsersClient.NewListByLabPager | - |
| microsoft.maintenance/updates | maintenance/armmaintenance | UpdatesClient.NewListPager | - |
| microsoft.operationalinsights/storageinsightconfigs | operationalinsights/armoperationalinsights | StorageInsightConfigsClient.NewListByWorkspacePager | properties.storageAccount.key |
| microsoft.recoveryservices/backupprotecteditems | recoveryservices/armrecoveryservicesbackup | BackupProtectedItemsClient.NewListPager | - |
| microsoft.scvmm/virtualmachineinstances | scvmm/armscvmm | VirtualMachineInstancesClient.NewListPager | - |
| microsoft.serialconsole/serialports | serialconsole/armserialconsole | SerialPortsClient.List | - |
| microsoft.servicelinker/dryruns | servicelinker/armservicelinker | - | - |
| microsoft.servicelinker/linkers | servicelinker/armservicelinker | LinkerClient.NewListPager | properties.authInfo.secret |

## DROP — probed candidates (full list)

> Note: two types were INCLUDE'd then dropped after the first live scan exposed
> unrecoverable SDK/API failures (see below).

```
microsoft.maintenance/applyupdates	404 operation-not-supported — not sub-listable
microsoft.network/networkvirtualapplianceskus	SDK deserialization bug (instanceCount string vs int32)
microsoft.aadcustomsecurityattributesdiagnosticsettings/diagnosticsettingscategories	metadata/ops
microsoft.aadcustomsecurityattributesdiagnosticsettings/diagnosticsettings	no-sdk-module
microsoft.app/agents	no-sdk-module
microsoft.app/agentspaces	no-sdk-module
microsoft.appcomplianceautomation/onboard	action-verb
microsoft.appcomplianceautomation/triggerevaluation	action-verb
microsoft.app/functions	action-verb
microsoft.appplatform/runtimeversions	metadata/ops
microsoft.app/sandboxgroups	no-sdk-module
microsoft.azurearcdata/sqlserveresulicenses	no-sdk-module
microsoft.azurearcdata/sqlserverlicenses	no-sdk-module
microsoft.azurebusinesscontinuity/unifiedprotecteditems	no-sdk-module
microsoft.azurecontextcache/accounts	no-sdk-module
microsoft.azuredatatransfer/connections	no-sdk-module
microsoft.azuredatatransfer/pipelines	no-sdk-module
microsoft.azurescan/scanningaccounts	no-sdk-module
microsoft.azurestack/cloudmanifestfiles	no-sdk-module
microsoft.azurestackhci/devicepools	no-sdk-module
microsoft.azurestackhci/edgedevices	no-sdk-module
microsoft.azurestackhci/edgemachines	no-sdk-module
microsoft.azurestackhci/loadbalancers	no-sdk-module
microsoft.azurestackhci/natgateways	no-sdk-module
microsoft.azurestackhci/publicipaddresses	no-sdk-module
microsoft.azurestackhci/snapshots	no-sdk-module
microsoft.azurestackhci/virtualmachines	no-sdk-module
microsoft.azurestackhci/virtualnetworks	no-sdk-module
microsoft.azurestack/linkedsubscriptions	no-sdk-module
microsoft.azurestack/registrations	no-sdk-module
microsoft.backupsolutions/vmwareapplications	no-sdk-module
microsoft.baremetal/baremetalconnections	no-sdk-module
microsoft.baremetal/baremetalinventorybase	no-sdk-module
microsoft.baremetalinfrastructure/baremetalstorageinstances	no-sdk-module
microsoft.baremetal/peeringsettings	no-sdk-module
microsoft.baremetal/sdnapplianceinventory	no-sdk-module
microsoft.baremetal/utilization	no-sdk-module
microsoft.bing/accounts	deprecated
microsoft.botservice/hostsettings	metadata/ops
microsoft.cdn/canmigrate	action-verb
microsoft.cdn/cdnwebapplicationfirewallmanagedrulesets	metadata/ops
microsoft.cdn/edgeactionoperations	no-sdk-module
microsoft.cdn/edgeactions	no-sdk-module
microsoft.cdn/edgenodes	metadata/ops
microsoft.cdn/webagents	no-sdk-module
microsoft.chaos/privateaccesses	no-sdk-module
microsoft.chaos/workspaces	no-sdk-module
microsoft.cleanroom/collaborations	no-sdk-module
microsoft.cleanroom/consortiums	no-sdk-module
microsoft.cleanroom/consortiumviews	no-sdk-module
microsoft.cloudtest/accounts	no-sdk-module
microsoft.cloudtest/buildcaches	no-sdk-module
microsoft.cloudtest/hostedpools	no-sdk-module
microsoft.cloudtest/images	no-sdk-module
microsoft.cloudtest/pools	no-sdk-module
microsoft.cognitiveservices/attestationdefinitions	no-sdk-module
microsoft.cognitiveservices/attestations	no-sdk-module
microsoft.cognitiveservices/managedcomputecapacities	no-sdk-module
microsoft.cognitiveservices/modelcapacities	query-only
microsoft.cognitiveservices/quotatiers	no-sdk-module
microsoft.cognitiveservices/raiexternalsafetyproviders	no-sdk-module
microsoft.compute/payloadgroups	no-sdk-module
microsoft.computeschedule/location	action-verb
microsoft.compute/sharedvmimages	query-only
microsoft.confluent/agreements	third-party
microsoft.confluent/organizations	third-party
microsoft.confluent/validations	action-verb
microsoft.connectedcredentials/credentials	no-sdk-module
microsoft.connectedvmwarevsphere/virtualmachines	deprecated
microsoft.containerinstance/containergroupprofiles	no-sdk-module
microsoft.containerinstance/ngroups	no-sdk-module
microsoft.containerinstance/serviceassociationlinks	no-sdk-module
microsoft.containerservice/aimanagers	no-sdk-module
microsoft.containerservice/deploymentsafeguards	no-sdk-module
microsoft.containerservice/managedclustersnapshots	no-sdk-module
microsoft.databoxedge/availableskus	metadata/ops
microsoft.datadog/activatesaas	third-party
microsoft.datadog/agreements	third-party
microsoft.datadog/monitors	third-party
microsoft.datadog/subscriptionstatuses	third-party
microsoft.datamigration/databasemigrations	no-sdk-module
microsoft.datamigration/migrationservices	no-sdk-module
microsoft.datamigration/sqlmigrationservices	no-sdk-module
microsoft.dbformysql/assessformigration	action-verb
microsoft.dbforpostgresql/availableengineversions	query-only
microsoft.desktopvirtualization/connectionpolicies	no-sdk-module
microsoft.desktopvirtualization/repositoryfolders	no-sdk-module
microsoft.devhub/templates	query-only
microsoft.deviceregistry/discoveredassetendpointprofiles	no-sdk-module
microsoft.deviceregistry/discoveredassets	no-sdk-module
microsoft.deviceregistry/namespaces	no-sdk-module
microsoft.deviceregistry/schemaregistries	no-sdk-module
microsoft.devices/provisioningserviceoperationresults	metadata/ops
microsoft.directorystore/directorystoreinstances	no-sdk-module
microsoft.documentdb/databaseaccountnames	query-only
microsoft.documentdb/fleets	no-sdk-module
microsoft.documentdb/managedresources	metadata/ops
microsoft.documentdb/operationsstatus	metadata/ops
microsoft.documentdb/throughputpools	no-sdk-module
microsoft.domainregistration/topleveldomains	Global catalog of purchasable top-level domain names (.com/.net), not user resource instances; query-only metadata
microsoft.easm/workspaces	No arm SDK module (easm/armeasm not published)
microsoft.edgeorder/productfamiliesmetadata	Catalog metadata of product families, not tracked subscription resources
microsoft.elastic/elasticversions	Available Elastic version strings (metadata catalog), keyed by region not resource instances
microsoft.enterprisesupport/enterprisesupports	No arm SDK module published
microsoft.enterprisesupport/supportplans	No arm SDK module published
microsoft.entraidgovernance/guestgovernanceusage	No arm SDK module published
microsoft.entraidgovernance/scimapiconsumptions	No arm SDK module published
microsoft.eventgrid/extensiontopics	Only single Get by resourceURI, no List op; extension resource metadata
microsoft.eventgrid/operationsstatus	Async operation status endpoint, not a tracked resource
microsoft.eventgrid/partnerdestinations	No PartnerDestinationsClient in SDK module (type not generated)
microsoft.eventgrid/topictypes	Global catalog of available topic types (metadata), not resource instances
microsoft.eventgrid/verifiedpartners	Global catalog of verified partners (metadata), not subscription resources
microsoft.eventhub/availableclusterregions	Metadata: regions where clusters can be created, not resource instances
microsoft.eventhub/sku	SKU metadata, not a listable tracked resource
microsoft.fabric/privatelinkservicesforfabric	No client for this type in SDK (only CapacitiesClient/OperationsClient)
microsoft.genome/accounts	No arm SDK module published
microsoft.hanaonazure/hanainstances	Deprecated/retired service; no HanaInstances client in SDK module
microsoft.hanaonazure/sapmonitors	Retired: HANA Large Instances decommissioned (Dec 2025); SAP monitor superseded by microsoft.workloads/monitors (scanned)
microsoft.hardware/orderpreview	No arm SDK module published
microsoft.hardware/orders	No arm SDK module published
microsoft.hardware/returnpreview	No arm SDK module published
microsoft.hardware/returns	No arm SDK module published
microsoft.hardwaresecuritymodules/cloudhsmclusters	No CloudHsmClusters client in GA SDK module (only DedicatedHsm)
microsoft.healthplatform/accounts	No arm SDK module published
microsoft.hybridcloud/cloudconnections	No arm SDK module published
microsoft.hybridcloud/cloudconnectors	No arm SDK module published
microsoft.hybridcompute/gateways	No Gateways client in GA SDK module
microsoft.hybridcompute/licenses	No Licenses client in GA SDK module
microsoft.hybridcompute/networkconfigurations	No NetworkConfigurations client in GA SDK module
microsoft.hybridcompute/ostype	OS type metadata, no client/list, not a resource
microsoft.hybridcompute/settings	No Settings list client; per-scope config, not listable resource
microsoft.hybridconnectivity/solutiontypes	Catalog of available solution types (capability metadata), not user resource instances
microsoft.hybridcontainerservice/kubernetesversions	Available Kubernetes versions catalog keyed by custom-location URI, metadata not resources
microsoft.hybridcontainerservice/provisionedclusters	No dedicated client; superseded by provisionedClusterInstances extension model in SDK
microsoft.hybridnetwork/configurationgroupvalues	AOSM type not in GA SDK module (v1.1.1 exposes older AOSM client set)
microsoft.hybridnetwork/networkfunctionvendors	Catalog of NF vendors (metadata), not subscription resource instances
microsoft.hybridnetwork/proxypublishers	AOSM type not in GA SDK module
microsoft.hybridnetwork/publishers	AOSM publishers type not in GA SDK module (v1.1.1 exposes older AOSM client set)
microsoft.hybridnetwork/sitenetworkservices	AOSM type not in GA SDK module
microsoft.hybridnetwork/sites	AOSM sites type not in GA SDK module
microsoft.impact/impactcategories	No arm SDK module published (impact/armimpact)
microsoft.impact/workloadimpacts	No arm SDK module published (impact/armimpact)
microsoft.iotcentral/apptemplates	Catalog of IoT Central app templates (metadata, no ARM ID), not resource instances
microsoft.iotoperationsdataprocessor/instances	Retired Data Processor preview; no arm SDK module (distinct from iotoperations)
microsoft.kubernetesconfiguration/extensiontypes	Catalog of available extension types (metadata); no client/list of instances
microsoft.kubernetesconfiguration/namespaces	No Namespaces client in SDK module
microsoft.kubernetesconfiguration/privatelinkscopes	No PrivateLinkScopes client in SDK module
microsoft.kubernetesruntime/bfdprofiles	No arm SDK module published (kubernetesruntime/armkubernetesruntime)
microsoft.kubernetesruntime/bgppeers	No arm SDK module published
microsoft.kubernetesruntime/loadbalancers	No arm SDK module published
microsoft.kubernetesruntime/services	No arm SDK module published
microsoft.kubernetesruntime/storageclasses	No arm SDK module published
microsoft.labservices/labaccounts	Deprecated v1 lab accounts; no LabAccounts client in current SDK module
microsoft.loadtestservice/loadtestmappings	No LoadTestMappings client in SDK module
microsoft.loadtestservice/loadtestprofilemappings	No LoadTestProfileMappings client in SDK module
microsoft.loadtestservice/playwrightworkspaces	No PlaywrightWorkspaces client under loadtesting RP module (Playwright lives under separate azureplaywrightservice RP)
microsoft.logic/automationprojects	no client in logic/armlogic SDK
microsoft.logic/businessprocesses	no client in logic/armlogic SDK
microsoft.logic/templates	no client in logic/armlogic SDK
microsoft.machinelearningservices/capacityreservationgroups	no SDK client (absent in armmachinelearning GA/beta)
microsoft.machinelearningservices/inferencemodels	no SDK client (absent in armmachinelearning GA/beta)
microsoft.machinelearningservices/virtualclusters	no SDK client (absent in armmachinelearning GA/beta)
microsoft.managedidentity/identities	no IdentitiesClient; msi/armmsi exposes UserAssignedIdentities/FederatedIdentityCredentials only
microsoft.managednetworkfabric/edgeconnectors	no SDK client in armmanagednetworkfabric
microsoft.managednetworkfabric/networkbootstrapdevices	no SDK client in armmanagednetworkfabric
microsoft.managednetworkfabric/networkbootstrapinterfaces	no SDK client in armmanagednetworkfabric
microsoft.managednetworkfabric/networkmonitors	no SDK client in armmanagednetworkfabric
microsoft.migrate/castscanreports	no SDK client in migrate/armmigrate
microsoft.migrate/onpremtcodetails	no SDK client in migrate/armmigrate
microsoft.monitor/accounts	Azure Monitor Workspace: no resolvable Go SDK module
microsoft.monitor/investigations	no SDK client / module
microsoft.monitor/pipelinegroups	no resolvable Go SDK module
microsoft.monitor/settings	no SDK client / module
microsoft.monitor/slisignalpreview	preview, no SDK client / module
microsoft.monitor/slis	no SDK client / module
microsoft.mysqldiscovery/mysqlsites	no SDK module (mysqldiscovery)
microsoft.network/applicationgatewayavailablerequestheaders	metadata/ops (static lookup, not a resource)
microsoft.network/applicationgatewayavailableresponseheaders	metadata/ops (static lookup)
microsoft.network/applicationgatewayavailableservervariables	metadata/ops (static lookup)
microsoft.network/applicationgatewayavailablessloptions	metadata/ops (static lookup)
microsoft.network/applicationgatewayavailablewafrulesets	metadata/ops (static lookup)
microsoft.network/assist	action-verb / feature, no listable resource
microsoft.network/authenticationpolicies	no SDK client in armnetwork
microsoft.network/authorizationpolicies	no SDK client in armnetwork
microsoft.network/checktrafficmanagernameavailabilityv2	action-verb (name availability check)
microsoft.network/cloudserviceslots	no SDK client (internal cloud-service slot)
microsoft.network/copilot	action-verb / feature, no listable resource
microsoft.network/ddoscustompolicies	no List op (only Get/CreateOrUpdate/Delete/UpdateTags)
microsoft.network/dnsoperationresults	metadata/ops (async operation result)
microsoft.network/dnsoperationstatuses	metadata/ops (async operation status)
microsoft.network/expressroutelags	no ExpressRouteLags client in armnetwork (LAGs surfaced under ExpressRoutePort)
microsoft.network/expressrouteproviderports	metadata/ops (provider port lookup, not a tracked resource)
microsoft.network/frontdooroperationresults	metadata/ops (async operation result)
microsoft.networkfunction/copilot	action/feature verb, no listable resource client
microsoft.networkfunction/meshvpns	no SDK client (armnetworkfunction has only AzureTrafficCollectors/CollectorPolicies)
microsoft.orbital/spacecrafts	Retired: Azure Orbital ground-station service ended Dec 2024; microsoft.orbital/geocatalogs (Planetary Computer) still scanned
microsoft.network/internalnotify	internal action-verb, not a resource
microsoft.network/internalpublicipaddresses	internal type, no SDK client
microsoft.network/networkgroupmemberships	parent-scoped membership under network group; no top-level listable resource
microsoft.network/networkintentpolicies	no SDK client in armnetwork (internal intent policy)
microsoft.network/networksecurityperimeters	no resolvable Go SDK module (armnetworksecurityperimeter unpublished)
microsoft.network/privatednsoperationresults	metadata/ops (async operation result)
microsoft.network/privatednsoperationstatuses	metadata/ops (async operation status)
microsoft.network/privatednszonesinternal	internal type, no SDK client
microsoft.network/privateendpointredirectmaps	internal type, no SDK client
microsoft.network/servicegateways	no SDK client in armnetwork
microsoft.network/trafficmanagergeographichierarchies	metadata/ops (static geographic hierarchy lookup)
microsoft.network/trafficmanagerusermetricskeys	metadata/ops (user metrics key, not a tracked resource)
microsoft.network/virtualnetworkappliances	no SDK client (legacy alias of networkvirtualappliances)
microsoft.nexusidentity/identitycontrollers	no SDK module: resourcemanager/nexusidentity absent from azure-sdk-for-go (confirmed via go get + GitHub contents API)
microsoft.nexusidentity/identitysets	no SDK module: resourcemanager/nexusidentity absent
microsoft.nutanix/interfaces	no SDK module: resourcemanager/nutanix absent
microsoft.nutanix/nodes	no SDK module: resourcemanager/nutanix absent
microsoft.objectstore/osnamespaces	no SDK module: resourcemanager/objectstore absent
microsoft.offazure/hypervsites	no SDK module: no offazure/migrationdiscoverysite arm module; armmigrate exposes collectors only, no Sites client
microsoft.offazure/mastersites	no SDK module: no offazure site arm module
microsoft.offazure/serversites	no SDK module: no offazure site arm module
microsoft.offazure/vmwaresites	no SDK module: no offazure site arm module
microsoft.openenergyplatform/energyservices	no SDK module: resourcemanager/openenergyplatform absent
microsoft.operationalinsights/linktargets	metadata/ops: no LinkTargets client; available-to-link account enumeration, not a tracked resource
microsoft.operationsmanagement/views	no SDK support: armoperationsmanagement has no Views client
microsoft.peering/cdnpeeringprefixes	query-only/catalog: NewListPager requires peeringLocation; read-only CDN prefix reference data, not a subscription-owned resource
microsoft.peering/legacypeerings	query-only: NewListPager(peeringLocation,kind) enumerates legacy peerings for migration; discovery helper, not a tracked resource
microsoft.peering/lookingglass	action-verb: LookingGlassClient exposes Invoke only, no List
microsoft.peering/peeringlocations	metadata/catalog: LocationsClient.NewListPager(kind) returns reference location catalog, not subscription resources
microsoft.peering/peeringservicecountries	metadata/catalog: ServiceCountriesClient.NewListPager returns reference country list
microsoft.peering/peeringservicelocations	metadata/catalog: ServiceLocationsClient.NewListPager returns reference location list
microsoft.peering/peeringserviceproviders	metadata/catalog: ServiceProvidersClient.NewListPager returns reference provider list
microsoft.portalservices/assistant	no SDK module: resourcemanager/portalservices absent (distinct from armportal RP)
microsoft.portalservices/compilefile	no SDK module: resourcemanager/portalservices absent
microsoft.portalservices/copilotsettings	no SDK module: resourcemanager/portalservices absent
microsoft.portalservices/dashboards	no SDK module: microsoft.portalservices RP has no arm module (dashboards SDK is under microsoft.portal/armportal, a different namespace)
microsoft.portalservices/settings	no SDK module: resourcemanager/portalservices absent
microsoft.powerbi/privatelinkservicesforpowerbi	no SDK module: resourcemanager/powerbi absent
microsoft.powerbi/workspacecollections	no SDK module: resourcemanager/powerbi absent (legacy/deprecated PowerBI Embedded workspace collections)
microsoft.powerplatformmonitoringhub/copilotstudio	no SDK module: resourcemanager/powerplatformmonitoringhub absent
microsoft.powerplatformmonitoringhub/microsoftapp	no SDK module: resourcemanager/powerplatformmonitoringhub absent
microsoft.powerplatformmonitoringhub/powerapps	no SDK module: resourcemanager/powerplatformmonitoringhub absent
microsoft.powerplatformmonitoringhub/powerautomate	no SDK module: resourcemanager/powerplatformmonitoringhub absent
microsoft.professionalservice/eligibilitycheck	no SDK module: resourcemanager/professionalservice absent; eligibilitycheck is an action-verb regardless
microsoft.professionalservice/resources	no SDK module: resourcemanager/professionalservice absent
microsoft.programenrollment/eduenrollments	no-sdk-module: no arm module for programenrollment
microsoft.purview/policies	metadata/ops: no policies list client in armpurview (data-plane sub-resource)
microsoft.purview/removedefaultaccount	action-verb: DefaultAccountsClient.Remove
microsoft.purview/setdefaultaccount	action-verb: DefaultAccountsClient.Set
microsoft.quantum/suiteoffers	query-only: OfferingsClient lists provider catalog, not instances
microsoft.recoveryservices/replicationeligibilityresults	query-only: List requires resourceName/vmName, returns eligibility check results not tracked resources
microsoft.resourceconnector/telemetryconfig	metadata/ops: only AppliancesClient exists; telemetryconfig has no list client
microsoft.resourceintelligence/agents	no-sdk-module: no arm module for resourceintelligence
microsoft.resourcenotifications/eventgridfilters	no-sdk-module: no arm module for resourcenotifications
microsoft.saas/saasresources	query-only: tenant-level legacy alias, no subscription scoping; superseded by SubscriptionLevel resources
microsoft.scom/managedinstances	no-sdk-module: no arm module for scom
microsoft.scvmm/virtualmachines	deprecated: legacy type superseded by virtualmachineinstances; no VirtualMachinesClient
microsoft.search/offerings	metadata: no offerings list client
microsoft.search/resourcehealthmetadata	metadata: resource-health metadata, not a tracked resource
microsoft.secretsynccontroller/azurekeyvaultsecretproviderclasses	no-sdk-module: no arm module for secretsynccontroller
microsoft.secretsynccontroller/secretsyncs	no-sdk-module: no arm module for secretsynccontroller
microsoft.securitycopilot/capacities	no-sdk-module: no arm module for securitycopilot
microsoft.securitydetonation/chambers	no-sdk-module: no arm module for securitydetonation
microsoft.sentinelplatformservices/sentinelplatformservices	no-sdk-module: no arm module for sentinelplatformservices
microsoft.serialconsole/consoleservices	metadata/ops: MicrosoftSerialConsoleClient is enable/disable/status verbs, no listable resource
microsoft.servicebus/premiummessagingregions	metadata: region capability list
microsoft.servicebus/sku	metadata: SKU catalog
microsoft.servicefabricmesh/applications	deprecated: Service Fabric Mesh preview was retired by Microsoft (2021); no live instances
microsoft.servicefabricmesh/gateways	deprecated: Service Fabric Mesh preview was retired by Microsoft (2021); no live instances
microsoft.servicefabricmesh/networks	deprecated: Service Fabric Mesh preview was retired by Microsoft (2021); no live instances
microsoft.servicefabricmesh/secrets	deprecated: Service Fabric Mesh preview was retired by Microsoft (2021); no live instances
microsoft.servicefabricmesh/volumes	deprecated: Service Fabric Mesh preview was retired by Microsoft (2021); no live instances
microsoft.servicelinker/configurationnames	metadata: configuration-names catalog, not a tracked resource
microsoft.servicelinker/daprconfigurations	metadata: dapr-config catalog/extension, no sub/RG list
microsoft.serviceshub/connectors	no-sdk-module: no arm module for serviceshub
microsoft.serviceshub/supportofferingentitlement	no-sdk-module: no arm module for serviceshub
microsoft.serviceshub/workspaces	no-sdk-module: no arm module for serviceshub
microsoft.singularity/accounts	no-sdk-module: no arm module for singularity
microsoft.singularity/images	no-sdk-module: no arm module for singularity
microsoft.storagecache/amlfilesystems	no-sdk-module: AmlFilesystemsClient absent from published GA module (v1.0.0)
microsoft.storagecache/usagemodels	metadata: usage-model catalog
microsoft.synapse/kustooperations	metadata/ops: KustoOperationsClient lists available operations
microsoft.syntex/accounts	no-sdk-module: no arm module for syntex
microsoft.syntex/documentprocessors	no-sdk-module: no arm module for syntex
microsoft.verifiedid/authorities	no-sdk-module: no arm module for verifiedid
microsoft.videoindexer/accounts	no-sdk-module: no arm module for videoindexer (armvideoindexer not published)
microsoft.visualstudio/account	deprecated: legacy Azure DevOps account RP; only single-call ListByResourceGroup, retired service
microsoft.web/aseregions	metadata: ASE region catalog
microsoft.web/availablestacks	metadata: runtime-stack catalog
microsoft.web/billingmeters	metadata: billing-meter catalog
microsoft.web/connectiongateways	no-sdk-module: API-connection gateway has no modern Go arm module
microsoft.web/connections	no-sdk-module: Logic Apps API connections have no modern Go arm module
microsoft.web/connectorgateways	no-sdk-module: connector gateway has no modern Go arm module
microsoft.web/customapis	no-sdk-module: Logic Apps custom APIs have no modern Go arm module
microsoft.web/customhostnamesites	metadata: hostname-to-site lookup, not a tracked resource
microsoft.web/deploymentlocations	metadata: deployment-location catalog
microsoft.web/functionappstacks	metadata: function-app stack catalog
microsoft.web/georegions	metadata: geo-region catalog
microsoft.web/ishostingenvironmentnameavailable	action-verb: name-availability check
microsoft.web/ishostnameavailable	action-verb: name-availability check
microsoft.web/isusernameavailable	action-verb: name-availability check
microsoft.web/publishingusers	metadata/ops: single publishing-user account, not listable resource set
microsoft.web/recommendations	metadata/ops: advisor recommendations, not tracked resources
microsoft.web/resourcehealthmetadata	metadata: resource-health metadata
microsoft.web/runtimes	metadata: runtime catalog
microsoft.web/sourcecontrols	metadata: source-control provider tokens (GitHub/etc), tenant-level config not tracked resource
microsoft.web/staticsiteregions	metadata: static-site region catalog
microsoft.web/verifyhostingenvironmentvnet	action-verb: vnet verification
microsoft.web/webappstacks	metadata: web-app stack catalog
microsoft.weightsandbiases/instances	third-party: Weights & Biases marketplace SaaS
microsoft.workloads/connectors	no-sdk-module: connectors type not in armworkloads (no ConnectorsClient)
microsoft.workloads/sapdiscoverysites	no-sdk-module: sapdiscoverysites in armworkloadssapvirtualinstance/migrate module, not armworkloads
```

## DROP — mechanical (pattern: operations / metadata / action-verb)

```
microsoft.aadcustomsecurityattributesdiagnosticsettings/operations	metadata/ops
microsoft.aadiam/operations	metadata/ops
microsoft.aad/locations	metadata/ops
microsoft.aad/operations	metadata/ops
microsoft.addons/operationresults	metadata/ops
microsoft.addons/operations	metadata/ops
microsoft.adhybridhealthservice/operations	metadata/ops
microsoft.adhybridhealthservice/reports	action-verb
microsoft.advisor/generaterecommendations	action-verb
microsoft.advisor/metadata	metadata/ops
microsoft.advisor/operations	metadata/ops
microsoft.advisor/predict	action-verb
microsoft.alertsmanagement/migratefromsmartdetection	action-verb
microsoft.alertsmanagement/operations	metadata/ops
microsoft.alertsmanagement/previewalertrule	action-verb
microsoft.analysisservices/locations	metadata/ops
microsoft.analysisservices/operations	metadata/ops
microsoft.apicenter/deletedservices	metadata/ops
microsoft.apicenter/operations	metadata/ops
microsoft.apimanagement/checkfeedbackrequired	action-verb
microsoft.apimanagement/checknameavailability	action-verb
microsoft.apimanagement/checkservicenameavailability	action-verb
microsoft.apimanagement/deletedservices	metadata/ops
microsoft.apimanagement/getdomainownershipidentifier	action-verb
microsoft.apimanagement/locations	metadata/ops
microsoft.apimanagement/operations	metadata/ops
microsoft.apimanagement/reportfeedback	action-verb
microsoft.apimanagement/validateservicename	action-verb
microsoft.appassessment/locations	metadata/ops
microsoft.appassessment/operations	metadata/ops
microsoft.appcomplianceautomation/checknameavailability	action-verb
microsoft.appcomplianceautomation/getcollectioncount	action-verb
microsoft.appcomplianceautomation/getoverviewstatus	action-verb
microsoft.appcomplianceautomation/listinusestorageaccounts	action-verb
microsoft.appcomplianceautomation/locations	metadata/ops
microsoft.appcomplianceautomation/operations	metadata/ops
microsoft.appcomplianceautomation/reports	action-verb
microsoft.appconfiguration/checknameavailability	action-verb
microsoft.appconfiguration/deletedconfigurationstores	metadata/ops
microsoft.appconfiguration/locations	metadata/ops
microsoft.appconfiguration/operations	metadata/ops
microsoft.app/getcustomdomainverificationid	action-verb
microsoft.applicationmigration/locations	metadata/ops
microsoft.applicationmigration/operations	metadata/ops
microsoft.applink/locations	metadata/ops
microsoft.applink/operations	metadata/ops
microsoft.app/locations	metadata/ops
microsoft.app/operations	metadata/ops
microsoft.appplatform/locations	metadata/ops
microsoft.appplatform/operations	metadata/ops
microsoft.approvals/locations	metadata/ops
microsoft.approvals/operations	metadata/ops
microsoft.arccontainerstorage/locations	metadata/ops
microsoft.arccontainerstorage/operations	metadata/ops
microsoft.attestation/defaultproviders	metadata/ops
microsoft.attestation/locations	metadata/ops
microsoft.attestation/operations	metadata/ops
microsoft.authorization/acquirepolicytoken	action-verb
microsoft.authorization/batchresourcecheckaccess	action-verb
microsoft.authorization/checkaccess	action-verb
microsoft.authorization/createattributenamespace	action-verb
microsoft.authorization/dataaliases	metadata/ops
microsoft.authorization/datapolicymanifests	metadata/ops
microsoft.authorization/elevateaccess	action-verb
microsoft.authorization/enableprivatelinknetworkaccess	action-verb
microsoft.authorization/findorphanroleassignments	action-verb
microsoft.authorization/listpolicydefinitionversions	action-verb
microsoft.authorization/listpolicysetdefinitionversions	action-verb
microsoft.authorization/migraterbac	action-verb
microsoft.authorization/operations	metadata/ops
microsoft.authorization/operationstatus	metadata/ops
microsoft.authorization/provideroperations	metadata/ops
microsoft.automanage/operations	metadata/ops
microsoft.automation/deletedautomationaccounts	metadata/ops
microsoft.automation/locations	metadata/ops
microsoft.automation/operations	metadata/ops
microsoft.avs/locations	metadata/ops
microsoft.avs/operations	metadata/ops
microsoft.awsconnector/locations	metadata/ops
microsoft.awsconnector/operations	metadata/ops
microsoft.azureactivedirectory/checknameavailability	action-verb
microsoft.azureactivedirectory/operations	metadata/ops
microsoft.azureactivedirectory/operationstatuses	metadata/ops
microsoft.azurearcdata/locations	metadata/ops
microsoft.azurearcdata/operations	metadata/ops
microsoft.azurebusinesscontinuity/deletedunifiedprotecteditems	metadata/ops
microsoft.azurebusinesscontinuity/operations	metadata/ops
microsoft.azurecontextcache/locations	metadata/ops
microsoft.azurecontextcache/operations	metadata/ops
microsoft.azuredatatransfer/listapprovedschemas	action-verb
microsoft.azuredatatransfer/listflowprofiles	action-verb
microsoft.azuredatatransfer/locations	metadata/ops
microsoft.azuredatatransfer/operations	metadata/ops
microsoft.azuredatatransfer/validateschema	action-verb
microsoft.azurefleet/locations	metadata/ops
microsoft.azurefleet/operations	metadata/ops
microsoft.azureimagetestingforlinux/operations	metadata/ops
microsoft.azurelargeinstance/locations	metadata/ops
microsoft.azurelargeinstance/operations	metadata/ops
microsoft.azureplaywrightservice/checknameavailability	action-verb
microsoft.azureplaywrightservice/locations	metadata/ops
microsoft.azureplaywrightservice/operations	metadata/ops
microsoft.azureplaywrightservice/registeredsubscriptions	action-verb
microsoft.azureresiliencemanagement/locations	metadata/ops
microsoft.azureresiliencemanagement/operations	metadata/ops
microsoft.azurescan/checknameavailability	action-verb
microsoft.azurescan/locations	metadata/ops
microsoft.azurescan/operations	metadata/ops
microsoft.azuresphere/locations	metadata/ops
microsoft.azuresphere/operations	metadata/ops
microsoft.azurestack/generatedeploymentlicense	action-verb
microsoft.azurestackhci/locations	metadata/ops
microsoft.azurestackhci/operations	metadata/ops
microsoft.azurestackhci/registeredsubscriptions	action-verb
microsoft.azurestack/operations	metadata/ops
microsoft.azureterraform/exportterraform	action-verb
microsoft.azureterraform/operations	metadata/ops
microsoft.azureterraform/operationstatuses	metadata/ops
microsoft.backupsolutions/locations	metadata/ops
microsoft.backupsolutions/operations	metadata/ops
microsoft.baremetalinfrastructure/locations	metadata/ops
microsoft.baremetalinfrastructure/operations	metadata/ops
microsoft.baremetal/locations	metadata/ops
microsoft.baremetal/operations	metadata/ops
microsoft.batch/locations	metadata/ops
microsoft.batch/operations	metadata/ops
microsoft.billingbenefits/calculatemigrationcost	action-verb
microsoft.billingbenefits/listsellerresources	action-verb
microsoft.billingbenefits/locations	metadata/ops
microsoft.billingbenefits/operationresults	metadata/ops
microsoft.billingbenefits/operations	metadata/ops
microsoft.billingbenefits/validate	action-verb
microsoft.billing/createbillingroleassignment	action-verb
microsoft.billing/operationresults	metadata/ops
microsoft.billing/operations	metadata/ops
microsoft.billing/operationstatus	metadata/ops
microsoft.billingtrust/locations	metadata/ops
microsoft.billingtrust/operations	metadata/ops
microsoft.billing/validateaddress	action-verb
microsoft.bing/locations	metadata/ops
microsoft.bing/operations	metadata/ops
microsoft.bing/registeredsubscriptions	action-verb
microsoft.blockchaintokens/operations	metadata/ops
microsoft.blueprint/operations	metadata/ops
microsoft.botservice/checknameavailability	action-verb
microsoft.botservice/listauthserviceproviders	action-verb
microsoft.botservice/listqnamakerendpointkeys	action-verb
microsoft.botservice/locations	metadata/ops
microsoft.botservice/operationresults	metadata/ops
microsoft.botservice/operations	metadata/ops
microsoft.cache/checknameavailability	action-verb
microsoft.cache/locations	metadata/ops
microsoft.cache/operations	metadata/ops
microsoft.capacity/calculateexchange	action-verb
microsoft.capacity/calculateprice	action-verb
microsoft.capacity/calculatepurchaseprice	action-verb
microsoft.capacity/checkbenefitscopes	action-verb
microsoft.capacity/checkoffers	action-verb
microsoft.capacity/checkpurchasestatus	action-verb
microsoft.capacity/checkscopes	action-verb
microsoft.capacity/listbenefits	action-verb
microsoft.capacity/listskus	action-verb
microsoft.capacity/operationresults	metadata/ops
microsoft.capacity/operations	metadata/ops
microsoft.capacity/validatereservationorder	action-verb
microsoft.carbon/operations	metadata/ops
microsoft.carbon/querycarbonemissiondataavailabledaterange	action-verb
microsoft.cdn/checkendpointnameavailability	action-verb
microsoft.cdn/checknameavailability	action-verb
microsoft.cdn/checkresourceusage	action-verb
microsoft.cdn/migrate	action-verb
microsoft.cdn/operationresults	metadata/ops
microsoft.cdn/operations	metadata/ops
microsoft.cdn/validateprobe	action-verb
microsoft.cdn/validatesecret	action-verb
microsoft.certificateregistration/operations	metadata/ops
microsoft.certificateregistration/validatecertificateregistrationinformation	action-verb
microsoft.changesafety/locations	metadata/ops
microsoft.changesafety/operations	metadata/ops
microsoft.chaos/locations	metadata/ops
microsoft.chaos/operations	metadata/ops
microsoft.cleanroom/locations	metadata/ops
microsoft.cleanroom/operations	metadata/ops
microsoft.clouddeviceplatform/operations	metadata/ops
microsoft.cloudhealth/locations	metadata/ops
microsoft.cloudhealth/operations	metadata/ops
microsoft.cloudshell/operations	metadata/ops
microsoft.cloudtest/locations	metadata/ops
microsoft.cloudtest/operations	metadata/ops
microsoft.codesigning/checknameavailability	action-verb
microsoft.codesigning/locations	metadata/ops
microsoft.codesigning/operations	metadata/ops
microsoft.codesigning/registeredsubscriptions	action-verb
microsoft.cognitiveservices/calculatemodelcapacity	action-verb
microsoft.cognitiveservices/checkdomainavailability	action-verb
microsoft.cognitiveservices/deletedaccounts	metadata/ops
microsoft.cognitiveservices/locations	metadata/ops
microsoft.cognitiveservices/operations	metadata/ops
microsoft.commerce/operations	metadata/ops
microsoft.communication/checknameavailability	action-verb
microsoft.communication/locations	metadata/ops
microsoft.communication/operations	metadata/ops
microsoft.communication/registeredsubscriptions	action-verb
microsoft.computebulkactions/locations	metadata/ops
microsoft.computebulkactions/operations	metadata/ops
microsoft.computelimit/locations	metadata/ops
microsoft.computelimit/operations	metadata/ops
microsoft.compute/locations	metadata/ops
microsoft.compute/operations	metadata/ops
microsoft.computeschedule/locations	metadata/ops
microsoft.computeschedule/operations	metadata/ops
microsoft.confidentialledger/checknameavailability	action-verb
microsoft.confidentialledger/locations	metadata/ops
microsoft.confidentialledger/operations	metadata/ops
microsoft.confluent/checknameavailability	action-verb
microsoft.confluent/locations	metadata/ops
microsoft.confluent/operations	metadata/ops
microsoft.connectedcache/locations	metadata/ops
microsoft.connectedcache/operations	metadata/ops
microsoft.connectedcache/registeredsubscriptions	action-verb
microsoft.connectedcredentials/locations	metadata/ops
microsoft.connectedcredentials/operations	metadata/ops
microsoft.connectedopenstack/locations	metadata/ops
microsoft.connectedopenstack/operations	metadata/ops
microsoft.connectedvehicle/checknameavailability	action-verb
microsoft.connectedvehicle/locations	metadata/ops
microsoft.connectedvehicle/operations	metadata/ops
microsoft.connectedvehicle/registeredsubscriptions	action-verb
microsoft.connectedvmwarevsphere/locations	metadata/ops
microsoft.connectedvmwarevsphere/operations	metadata/ops
microsoft.consumption/operationresults	metadata/ops
microsoft.consumption/operations	metadata/ops
microsoft.consumption/operationstatus	metadata/ops
microsoft.containerinstance/locations	metadata/ops
microsoft.containerinstance/operations	metadata/ops
microsoft.containerregistry/checknameavailability	action-verb
microsoft.containerregistry/locations	metadata/ops
microsoft.containerregistry/operations	metadata/ops
microsoft.containerservice/locations	metadata/ops
microsoft.containerservice/operations	metadata/ops
microsoft.costmanagement/calculatecost	action-verb
microsoft.costmanagement/calculateprice	action-verb
microsoft.costmanagement/checknameavailability	action-verb
microsoft.costmanagement/exports	action-verb
microsoft.costmanagementexports/operations	metadata/ops
microsoft.costmanagement/generatebenefitutilizationsummariesreport	action-verb
microsoft.costmanagement/generatecostdetailsreport	action-verb
microsoft.costmanagement/generatedetailedcostreport	action-verb
microsoft.costmanagement/generatereservationdetailsreport	action-verb
microsoft.costmanagement/operationresults	metadata/ops
microsoft.costmanagement/operations	metadata/ops
microsoft.costmanagement/operationstatus	metadata/ops
microsoft.costmanagement/query	action-verb
microsoft.costmanagement/reportconfigs	action-verb
microsoft.costmanagement/reports	action-verb
microsoft.customerlockbox/enablelockbox	action-verb
microsoft.customerlockbox/operations	metadata/ops
microsoft.customproviders/locations	metadata/ops
microsoft.customproviders/operations	metadata/ops
microsoft.d365customerinsights/operations	metadata/ops
microsoft.dashboard/checknameavailability	action-verb
microsoft.dashboard/locations	metadata/ops
microsoft.dashboard/operations	metadata/ops
microsoft.databasefleetmanager/operations	metadata/ops
microsoft.databasewatcher/locations	metadata/ops
microsoft.databasewatcher/operations	metadata/ops
microsoft.databoxedge/operations	metadata/ops
microsoft.databox/locations	metadata/ops
microsoft.databox/operations	metadata/ops
microsoft.databricks/locations	metadata/ops
microsoft.databricks/operations	metadata/ops
microsoft.datadog/locations	metadata/ops
microsoft.datadog/operations	metadata/ops
microsoft.datadog/registeredsubscriptions	action-verb
microsoft.datafactory/checknameavailability	action-verb
microsoft.datafactory/locations	metadata/ops
microsoft.datafactory/operations	metadata/ops
microsoft.datalakeanalytics/locations	metadata/ops
microsoft.datalakeanalytics/operations	metadata/ops
microsoft.datalakestore/locations	metadata/ops
microsoft.datalakestore/operations	metadata/ops
microsoft.datamigration/locations	metadata/ops
microsoft.datamigration/operations	metadata/ops
microsoft.dataprotection/locations	metadata/ops
microsoft.dataprotection/operations	metadata/ops
microsoft.datareplication/locations	metadata/ops
microsoft.datareplication/operationresults	metadata/ops
microsoft.datareplication/operations	metadata/ops
microsoft.datashare/listinvitations	action-verb
microsoft.datashare/locations	metadata/ops
microsoft.datashare/operations	metadata/ops
microsoft.dbformariadb/checknameavailability	action-verb
microsoft.dbformariadb/locations	metadata/ops
microsoft.dbformariadb/operations	metadata/ops
microsoft.dbformysql/checknameavailability	action-verb
microsoft.dbformysql/getprivatednszonesuffix	action-verb
microsoft.dbformysql/locations	metadata/ops
microsoft.dbformysql/operations	metadata/ops
microsoft.dbforpostgresql/checknameavailability	action-verb
microsoft.dbforpostgresql/getprivatednszonesuffix	action-verb
microsoft.dbforpostgresql/locations	metadata/ops
microsoft.dbforpostgresql/operations	metadata/ops
microsoft.dependencymap/locations	metadata/ops
microsoft.dependencymap/operations	metadata/ops
microsoft.desktopvirtualization/operations	metadata/ops
microsoft.devcenter/checknameavailability	action-verb
microsoft.devcenter/checkscopednameavailability	action-verb
microsoft.devcenter/locations	metadata/ops
microsoft.devcenter/operations	metadata/ops
microsoft.developmentwindows365/operations	metadata/ops
microsoft.devhub/locations	metadata/ops
microsoft.devhub/operations	metadata/ops
microsoft.deviceonboarding/operations	metadata/ops
microsoft.deviceregistry/locations	metadata/ops
microsoft.deviceregistry/operations	metadata/ops
microsoft.deviceregistry/operationstatuses	metadata/ops
microsoft.devices/checknameavailability	action-verb
microsoft.devices/checkprovisioningservicenameavailability	action-verb
microsoft.devices/locations	metadata/ops
microsoft.devices/operations	metadata/ops
microsoft.devices/usages	metadata/ops
microsoft.deviceupdate/checknameavailability	action-verb
microsoft.deviceupdate/locations	metadata/ops
microsoft.deviceupdate/operations	metadata/ops
microsoft.deviceupdate/registeredsubscriptions	action-verb
microsoft.devopsinfrastructure/checknameavailability	action-verb
microsoft.devopsinfrastructure/locations	metadata/ops
microsoft.devopsinfrastructure/operations	metadata/ops
microsoft.devopsinfrastructure/skus	metadata/ops
microsoft.devopsinfrastructure/usages	metadata/ops
microsoft.devtestlab/locations	metadata/ops
microsoft.devtestlab/operations	metadata/ops
microsoft.digitaltwins/locations	metadata/ops
microsoft.digitaltwins/operations	metadata/ops
microsoft.directorystore/locations	metadata/ops
microsoft.directorystore/operationstatuses	metadata/ops
microsoft.discovery/checknameavailability	action-verb
microsoft.discovery/locations	metadata/ops
microsoft.discovery/operations	metadata/ops
microsoft.documentdb/locations	metadata/ops
microsoft.documentdb/operationresults	metadata/ops
microsoft.documentdb/operations	metadata/ops
microsoft.domainregistration/checkdomainavailability	action-verb
microsoft.domainregistration/generatessorequest	action-verb
microsoft.domainregistration/listdomainrecommendations	action-verb
microsoft.domainregistration/operations	metadata/ops
microsoft.domainregistration/validatedomainregistrationinformation	action-verb
microsoft.durabletask/locations	metadata/ops
microsoft.durabletask/operations	metadata/ops
microsoft.easm/operations	metadata/ops
microsoft.edge/locations	metadata/ops
microsoft.edgemarketplace/locations	metadata/ops
microsoft.edgemarketplace/operations	metadata/ops
microsoft.edge/operations	metadata/ops
microsoft.edgeorder/listconfigurations	action-verb
microsoft.edgeorder/listproductfamilies	action-verb
microsoft.edgeorder/locations	metadata/ops
microsoft.edgeorder/operations	metadata/ops
microsoft.edgeorderpartner/operations	metadata/ops
microsoft.edge/registeredsubscriptions	action-verb
microsoft.edgezones/operations	metadata/ops
microsoft.elastic/checknameavailability	action-verb
microsoft.elastic/getelasticorganizationtoazuresubscriptionmapping	action-verb
microsoft.elastic/getorganizationapikey	action-verb
microsoft.elastic/locations	metadata/ops
microsoft.elastic/operations	metadata/ops
microsoft.elasticsan/locations	metadata/ops
microsoft.elasticsan/operations	metadata/ops
microsoft.enterprisesupport/operations	metadata/ops
microsoft.enterprisesupport/operationstatuses	metadata/ops
microsoft.enterprisesupport/validate	action-verb
microsoft.entitlementmanagement/operations	metadata/ops
microsoft.entraidgovernanceaccelerator/operations	metadata/ops
microsoft.entraidgovernance/operations	metadata/ops
microsoft.erroratlas/operations	metadata/ops
microsoft.eventgrid/locations	metadata/ops
microsoft.eventgrid/operationresults	metadata/ops
microsoft.eventgrid/operations	metadata/ops
microsoft.eventhub/checknameavailability	action-verb
microsoft.eventhub/checknamespaceavailability	action-verb
microsoft.eventhub/locations	metadata/ops
microsoft.eventhub/operations	metadata/ops
microsoft.experimentation/operations	metadata/ops
microsoft.extendedlocation/locations	metadata/ops
microsoft.extendedlocation/operations	metadata/ops
microsoft.fabric/locations	metadata/ops
microsoft.fabric/operations	metadata/ops
microsoft.features/operations	metadata/ops
microsoft.fileshares/locations	metadata/ops
microsoft.fileshares/operations	metadata/ops
microsoft.fluidrelay/locations	metadata/ops
microsoft.fluidrelay/operations	metadata/ops
microsoft.gcpconnector/locations	metadata/ops
microsoft.gcpconnector/operations	metadata/ops
microsoft.genome/locations	metadata/ops
microsoft.genome/operations	metadata/ops
microsoft.graphservices/locations	metadata/ops
microsoft.graphservices/operations	metadata/ops
microsoft.graphservices/registeredsubscriptions	action-verb
microsoft.guestconfiguration/operations	metadata/ops
microsoft.hanaonazure/locations	metadata/ops
microsoft.hanaonazure/operations	metadata/ops
microsoft.hardware/operations	metadata/ops
microsoft.hardware/registeredsubscriptions	action-verb
microsoft.hardwaresecuritymodules/locations	metadata/ops
microsoft.hardwaresecuritymodules/operations	metadata/ops
microsoft.hdinsight/locations	metadata/ops
microsoft.hdinsight/operations	metadata/ops
microsoft.healthbot/locations	metadata/ops
microsoft.healthbot/operations	metadata/ops
microsoft.healthcareapis/checknameavailability	action-verb
microsoft.healthcareapis/locations	metadata/ops
microsoft.healthcareapis/operations	metadata/ops
microsoft.healthcareapis/validatemedtechmappings	action-verb
microsoft.healthcareinterop/operations	metadata/ops
microsoft.healthdataaiservices/locations	metadata/ops
microsoft.healthdataaiservices/operations	metadata/ops
microsoft.healthmodel/operations	metadata/ops
microsoft.help/checknameavailability	action-verb
microsoft.help/operationresults	metadata/ops
microsoft.help/operations	metadata/ops
microsoft.horizondb/locations	metadata/ops
microsoft.horizondb/operations	metadata/ops
microsoft.hybridcompute/locations	metadata/ops
microsoft.hybridcompute/operations	metadata/ops
microsoft.hybridcompute/validatelicense	action-verb
microsoft.hybridconnectivity/generateawstemplate	action-verb
microsoft.hybridconnectivity/generategcptemplate	action-verb
microsoft.hybridconnectivity/locations	metadata/ops
microsoft.hybridconnectivity/operations	metadata/ops
microsoft.hybridcontainerservice/locations	metadata/ops
microsoft.hybridcontainerservice/operations	metadata/ops
microsoft.hybridcontainerservice/skus	metadata/ops
microsoft.hybridnetwork/locations	metadata/ops
microsoft.hybridnetwork/operations	metadata/ops
microsoft.impact/getuploadtoken	action-verb
microsoft.impact/operations	metadata/ops
microsoft.inferenceservice/operations	metadata/ops
microsoft.insights/createnotifications	action-verb
microsoft.insights/deletedworkbooks	metadata/ops
microsoft.insights/generatelivetoken	action-verb
microsoft.insights/listmigrationdate	action-verb
microsoft.insights/locations	metadata/ops
microsoft.insights/migratealertrules	action-verb
microsoft.insights/migratetonewpricingmodel	action-verb
microsoft.insights/operations	metadata/ops
microsoft.integrationspaces/locations	metadata/ops
microsoft.integrationspaces/operations	metadata/ops
microsoft.iotcentral/checknameavailability	action-verb
microsoft.iotcentral/checksubdomainavailability	action-verb
microsoft.iotcentral/locations	metadata/ops
microsoft.iotcentral/operations	metadata/ops
microsoft.iotfirmwaredefense/locations	metadata/ops
microsoft.iotfirmwaredefense/operations	metadata/ops
microsoft.iotoperationsdataprocessor/locations	metadata/ops
microsoft.iotoperationsdataprocessor/operations	metadata/ops
microsoft.iotoperations/locations	metadata/ops
microsoft.iotoperations/operations	metadata/ops
microsoft.iotsecurity/locations	metadata/ops
microsoft.iotsecurity/operations	metadata/ops
microsoft.keyvault/checkmhsmnameavailability	action-verb
microsoft.keyvault/checknameavailability	action-verb
microsoft.keyvault/deletedmanagedhsms	metadata/ops
microsoft.keyvault/deletedvaults	metadata/ops
microsoft.keyvault/locations	metadata/ops
microsoft.keyvault/operations	metadata/ops
microsoft.kubernetesconfiguration/operations	metadata/ops
microsoft.kubernetes/locations	metadata/ops
microsoft.kubernetes/operations	metadata/ops
microsoft.kubernetes/registeredsubscriptions	action-verb
microsoft.kubernetesruntime/locations	metadata/ops
microsoft.kubernetesruntime/operations	metadata/ops
microsoft.kusto/locations	metadata/ops
microsoft.kusto/operations	metadata/ops
microsoft.labservices/locations	metadata/ops
microsoft.labservices/operations	metadata/ops
microsoft.loadtestservice/checknameavailability	action-verb
microsoft.loadtestservice/locations	metadata/ops
microsoft.loadtestservice/operations	metadata/ops
microsoft.loadtestservice/registeredsubscriptions	action-verb
microsoft.logic/locations	metadata/ops
microsoft.logic/operations	metadata/ops
microsoft.machinelearningservices/locations	metadata/ops
microsoft.machinelearningservices/operations	metadata/ops
microsoft.maintenance/operations	metadata/ops
microsoft.managedidentity/operations	metadata/ops
microsoft.managednetworkfabric/locations	metadata/ops
microsoft.managednetworkfabric/operations	metadata/ops
microsoft.managedops/locations	metadata/ops
microsoft.managedops/operations	metadata/ops
microsoft.managedservices/operations	metadata/ops
microsoft.managedservices/operationstatuses	metadata/ops
microsoft.management/checknameavailability	action-verb
microsoft.management/getentities	action-verb
microsoft.management/operationresults	metadata/ops
microsoft.management/operations	metadata/ops
microsoft.maps/locations	metadata/ops
microsoft.maps/operationresults	metadata/ops
microsoft.maps/operations	metadata/ops
microsoft.maps/operationstatuses	metadata/ops
microsoft.marketplace/locations	metadata/ops
microsoft.marketplace/operations	metadata/ops
microsoft.marketplaceordering/operations	metadata/ops
microsoft.marketplace/register	action-verb
microsoft.marketplace/skus	metadata/ops
microsoft.messagingcatalog/operations	metadata/ops
microsoft.messagingconnectors/locations	metadata/ops
microsoft.messagingconnectors/operations	metadata/ops
microsoft.migrate/locations	metadata/ops
microsoft.migrate/migrateprojects	action-verb
microsoft.migrate/movecollections	action-verb
microsoft.migrate/operations	metadata/ops
microsoft.mission/checknameavailability	action-verb
microsoft.mission/locations	metadata/ops
microsoft.mission/operations	metadata/ops
microsoft.monitor/locations	metadata/ops
microsoft.monitor/operations	metadata/ops
microsoft.mysqldiscovery/locations	metadata/ops
microsoft.mysqldiscovery/operations	metadata/ops
microsoft.netapp/locations	metadata/ops
microsoft.netapp/operations	metadata/ops
microsoft.network/checkfrontdoornameavailability	action-verb
microsoft.network/checktrafficmanagernameavailability	action-verb
microsoft.networkcloud/locations	metadata/ops
microsoft.networkcloud/operations	metadata/ops
microsoft.networkcloud/registeredsubscriptions	action-verb
microsoft.networkfunction/locations	metadata/ops
microsoft.networkfunction/operations	metadata/ops
microsoft.network/getdnsresourcereference	action-verb
microsoft.network/locations	metadata/ops
microsoft.network/operations	metadata/ops
microsoft.network/queryexpressrouteportsbandwidth	action-verb
microsoft.nexusidentity/locations	metadata/ops
microsoft.nexusidentity/operations	metadata/ops
microsoft.notificationhubs/checknameavailability	action-verb
microsoft.notificationhubs/checknamespaceavailability	action-verb
microsoft.notificationhubs/operations	metadata/ops
microsoft.nutanix/locations	metadata/ops
microsoft.nutanix/operations	metadata/ops
microsoft.offazure/importsites	action-verb
microsoft.offazure/locations	metadata/ops
microsoft.offazure/operations	metadata/ops
microsoft.offazurespringboot/locations	metadata/ops
microsoft.offazurespringboot/operations	metadata/ops
microsoft.onlineexperimentation/locations	metadata/ops
microsoft.onlineexperimentation/operations	metadata/ops
microsoft.openenergyplatform/checknameavailability	action-verb
microsoft.openenergyplatform/locations	metadata/ops
microsoft.openenergyplatform/operations	metadata/ops
microsoft.operationalinsights/deletedworkspaces	metadata/ops
microsoft.operationalinsights/locations	metadata/ops
microsoft.operationalinsights/operations	metadata/ops
microsoft.operationalinsights/querypacks	action-verb
microsoft.operationsmanagement/operations	metadata/ops
microsoft.operatorvoicemail/locations	metadata/ops
microsoft.operatorvoicemail/operations	metadata/ops
microsoft.orbital/locations	metadata/ops
microsoft.orbital/operations	metadata/ops
microsoft.partnermanagedconsumerrecurrence/checkeligibility	action-verb
microsoft.partnermanagedconsumerrecurrence/operations	metadata/ops
microsoft.partnermanagedconsumerrecurrence/operationstatuses	metadata/ops
microsoft.peering/checkserviceprovideravailability	action-verb
microsoft.peering/operations	metadata/ops
microsoft.pki/operations	metadata/ops
microsoft.policyinsights/checkpolicyrestrictions	action-verb
microsoft.policyinsights/generatepolicyauthoringguide	action-verb
microsoft.policyinsights/generatepolicyeffect	action-verb
microsoft.policyinsights/generatepolicyparameters	action-verb
microsoft.policyinsights/generatepolicyrule	action-verb
microsoft.policyinsights/generatepolicyruleif	action-verb
microsoft.policyinsights/generatepolicyskeleton	action-verb
microsoft.policyinsights/operations	metadata/ops
microsoft.policyinsights/policymetadata	metadata/ops
microsoft.portal/listtenantconfigurationviolations	action-verb
microsoft.portal/locations	metadata/ops
microsoft.portal/operations	metadata/ops
microsoft.portalservices/locations	metadata/ops
microsoft.portalservices/operations	metadata/ops
microsoft.powerbidedicated/locations	metadata/ops
microsoft.powerbidedicated/operations	metadata/ops
microsoft.powerbi/locations	metadata/ops
microsoft.powerbi/operations	metadata/ops
microsoft.powerplatform/operations	metadata/ops
microsoft.premonition/locations	metadata/ops
microsoft.professionalservice/checknameavailability	action-verb
microsoft.professionalservice/operationresults	metadata/ops
microsoft.professionalservice/operations	metadata/ops
microsoft.programenrollment/locations	metadata/ops
microsoft.programenrollment/operations	metadata/ops
microsoft.providerhub/operations	metadata/ops
microsoft.providerhub/operationstatuses	metadata/ops
microsoft.purview/checknameavailability	action-verb
microsoft.purview/getdefaultaccount	action-verb
microsoft.purview/locations	metadata/ops
microsoft.purview/operations	metadata/ops
microsoft.quantum/locations	metadata/ops
microsoft.quantum/operations	metadata/ops
microsoft.quota/operations	metadata/ops
microsoft.quota/quotas	metadata/ops
microsoft.quota/usages	metadata/ops
microsoft.recommendationsservice/checknameavailability	action-verb
microsoft.recommendationsservice/locations	metadata/ops
microsoft.recommendationsservice/operations	metadata/ops
microsoft.recoveryservices/locations	metadata/ops
microsoft.recoveryservices/operations	metadata/ops
microsoft.redhatopenshift/locations	metadata/ops
microsoft.redhatopenshift/operations	metadata/ops
microsoft.relationships/operationresults	metadata/ops
microsoft.relationships/operations	metadata/ops
microsoft.relay/checknameavailability	action-verb
microsoft.relay/locations	metadata/ops
microsoft.relay/operations	metadata/ops
microsoft.resourcebuilder/locations	metadata/ops
microsoft.resourcebuilder/operations	metadata/ops
microsoft.resourcebuilder/registeredsubscriptions	action-verb
microsoft.resourceconnector/locations	metadata/ops
microsoft.resourceconnector/operations	metadata/ops
microsoft.resourcegraph/generatequery	action-verb
microsoft.resourcegraph/operations	metadata/ops
microsoft.resourcehealth/metadata	metadata/ops
microsoft.resourcehealth/operations	metadata/ops
microsoft.resourceintelligence/generatequery	action-verb
microsoft.resourceintelligence/operations	metadata/ops
microsoft.resourcenotifications/locations	metadata/ops
microsoft.resourcenotifications/operations	metadata/ops
microsoft.resources/calculatetemplatehash	action-verb
microsoft.resources/checkpolicycompliance	action-verb
microsoft.resources/checkresourcename	action-verb
microsoft.resources/locations	metadata/ops
microsoft.resources/operationresults	metadata/ops
microsoft.resources/operations	metadata/ops
microsoft.resources/validateresources	action-verb
microsoft.saas/checknameavailability	action-verb
microsoft.saashub/checknameavailability	action-verb
microsoft.saashub/locations	metadata/ops
microsoft.saashub/operations	metadata/ops
microsoft.saashub/operationstatuses	metadata/ops
microsoft.saashub/registeredsubscriptions	action-verb
microsoft.saashub/validatefuturepriceeligibility	action-verb
microsoft.saashub/validatepromotions	action-verb
microsoft.saas/operationresults	metadata/ops
microsoft.saas/operations	metadata/ops
microsoft.scom/locations	metadata/ops
microsoft.scom/operations	metadata/ops
microsoft.scvmm/locations	metadata/ops
microsoft.scvmm/operations	metadata/ops
microsoft.search/checknameavailability	action-verb
microsoft.search/checkservicenameavailability	action-verb
microsoft.search/locations	metadata/ops
microsoft.search/operations	metadata/ops
microsoft.secretsynccontroller/locations	metadata/ops
microsoft.secretsynccontroller/operations	metadata/ops
microsoft.securitycopilot/locations	metadata/ops
microsoft.securitycopilot/operations	metadata/ops
microsoft.securitydetonation/checknameavailability	action-verb
microsoft.securitydetonation/operationresults	metadata/ops
microsoft.securitydetonation/operations	metadata/ops
microsoft.securityinsights/exportconnections	action-verb
microsoft.securityinsights/listrepositories	action-verb
microsoft.securityinsights/metadata	metadata/ops
microsoft.securityinsights/operations	metadata/ops
microsoft.security/locations	metadata/ops
microsoft.security/operations	metadata/ops
microsoft.securityplatform/locations	metadata/ops
microsoft.securityplatform/operations	metadata/ops
microsoft.security/query	action-verb
microsoft.sentinelplatformservices/locations	metadata/ops
microsoft.sentinelplatformservices/operations	metadata/ops
microsoft.serialconsole/locations	metadata/ops
microsoft.serialconsole/operations	metadata/ops
microsoft.servicebus/checknameavailability	action-verb
microsoft.servicebus/checknamespaceavailability	action-verb
microsoft.servicebus/locations	metadata/ops
microsoft.servicebus/operations	metadata/ops
microsoft.servicefabric/locations	metadata/ops
microsoft.servicefabricmesh/locations	metadata/ops
microsoft.servicefabricmesh/operations	metadata/ops
microsoft.servicefabric/operations	metadata/ops
microsoft.servicelinker/locations	metadata/ops
microsoft.servicelinker/operations	metadata/ops
microsoft.servicenetworking/locations	metadata/ops
microsoft.servicenetworking/operations	metadata/ops
microsoft.serviceshub/getrecommendationscontent	action-verb
microsoft.serviceshub/operations	metadata/ops
microsoft.signalrservice/locations	metadata/ops
microsoft.signalrservice/operations	metadata/ops
microsoft.singularity/locations	metadata/ops
microsoft.singularity/operations	metadata/ops
microsoft.singularity/quotas	metadata/ops
microsoft.softwareplan/operationresults	metadata/ops
microsoft.softwareplan/operations	metadata/ops
microsoft.softwareplan/refreshlicensesummary	action-verb
microsoft.softwareplan/validatesoftwarebenefitcreate	action-verb
microsoft.softwareplan/validatesoftwarelicensecreate	action-verb
microsoft.softwareplan/validatesoftwareperpetualproductcreate	action-verb
microsoft.softwareplan/validatesoftwaresubscriptioncreate	action-verb
microsoft.solutions/locations	metadata/ops
microsoft.solutions/operations	metadata/ops
microsoft.sovereign/checknameavailability	action-verb
microsoft.sovereign/locations	metadata/ops
microsoft.sovereign/operations	metadata/ops
microsoft.sql/checknameavailability	action-verb
microsoft.sql/locations	metadata/ops
microsoft.sql/operations	metadata/ops
microsoft.sqlvirtualmachine/locations	metadata/ops
microsoft.sqlvirtualmachine/operations	metadata/ops
microsoft.standbypool/locations	metadata/ops
microsoft.standbypool/operations	metadata/ops
microsoft.storageactions/locations	metadata/ops
microsoft.storageactions/operations	metadata/ops
microsoft.storagecache/checkamlfssubnets	action-verb
microsoft.storagecache/getrequiredamlfssubnetssize	action-verb
microsoft.storagecache/locations	metadata/ops
microsoft.storagecache/operations	metadata/ops
microsoft.storage/checknameavailability	action-verb
microsoft.storage/deletedaccounts	metadata/ops
microsoft.storagediscovery/locations	metadata/ops
microsoft.storagediscovery/operations	metadata/ops
microsoft.storage/locations	metadata/ops
microsoft.storagemover/locations	metadata/ops
microsoft.storagemover/operations	metadata/ops
microsoft.storage/operations	metadata/ops
microsoft.storagesync/locations	metadata/ops
microsoft.storagesync/operations	metadata/ops
microsoft.storagetasks/locations	metadata/ops
microsoft.storage/usages	metadata/ops
microsoft.streamanalytics/locations	metadata/ops
microsoft.streamanalytics/operations	metadata/ops
microsoft.subscription/createsubscription	action-verb
microsoft.subscription/operationresults	metadata/ops
microsoft.subscription/operations	metadata/ops
microsoft.subscription/validatecancel	action-verb
microsoft.supercomputerinfrastructure/locations	metadata/ops
microsoft.supercomputerinfrastructure/operations	metadata/ops
microsoft.support/checknameavailability	action-verb
microsoft.support/operationresults	metadata/ops
microsoft.support/operations	metadata/ops
microsoft.sustainabilityservices/locations	metadata/ops
microsoft.sustainabilityservices/operations	metadata/ops
microsoft.synapse/checknameavailability	action-verb
microsoft.synapse/locations	metadata/ops
microsoft.synapse/operations	metadata/ops
microsoft.syntex/locations	metadata/ops
microsoft.syntex/operations	metadata/ops
microsoft.toolchainorchestrator/operations	metadata/ops
microsoft.updatemanager/locations	metadata/ops
microsoft.updatemanager/operations	metadata/ops
microsoft.usagebilling/operations	metadata/ops
microsoft.validate/locations	metadata/ops
microsoft.validate/operations	metadata/ops
microsoft.videoindexer/checknameavailability	action-verb
microsoft.videoindexer/locations	metadata/ops
microsoft.videoindexer/operations	metadata/ops
microsoft.virtualmachineimages/locations	metadata/ops
microsoft.virtualmachineimages/operations	metadata/ops
microsoft.visualstudio/checknameavailability	action-verb
microsoft.visualstudio/operations	metadata/ops
microsoft.vmware/locations	metadata/ops
microsoft.vmware/operations	metadata/ops
microsoft.web/checknameavailability	action-verb
microsoft.web/deletedsites	metadata/ops
microsoft.web/generategithubaccesstokenforappservicecli	action-verb
microsoft.web/listsitesassignedtohostname	action-verb
microsoft.web/locations	metadata/ops
microsoft.web/operations	metadata/ops
microsoft.web/validate	action-verb
microsoft.weightsandbiases/checknameavailability	action-verb
microsoft.weightsandbiases/locations	metadata/ops
microsoft.weightsandbiases/operations	metadata/ops
microsoft.weightsandbiases/registeredsubscriptions	action-verb
microsoft.windows365/operations	metadata/ops
microsoft.windowspushnotificationservices/checknameavailability	action-verb
microsoft.workloadbuilder/locations	metadata/ops
microsoft.workloadbuilder/operations	metadata/ops
microsoft.workloads/locations	metadata/ops
microsoft.workloads/operations	metadata/ops
microsoft.zerotrustsegmentation/locations	metadata/ops
microsoft.zerotrustsegmentation/operations	metadata/ops
```

## DROP — wholesale namespace (third-party connectors / billing / query / metadata RPs)

```
microsoft.aad/domainservices	metadata/ops
microsoft.aadiam/diagnosticsettingscategories	metadata/ops
microsoft.aadiam/diagnosticsettings	metadata/ops
microsoft.aadiam/tenants	metadata/ops
microsoft.addons/supportproviders	metadata/ops
microsoft.adhybridhealthservice/aadsupportcases	query-only
microsoft.adhybridhealthservice/addsservices	query-only
microsoft.adhybridhealthservice/agents	query-only
microsoft.adhybridhealthservice/anonymousapiusers	query-only
microsoft.adhybridhealthservice/configuration	query-only
microsoft.adhybridhealthservice/logs	query-only
microsoft.adhybridhealthservice/servicehealthmetrics	query-only
microsoft.adhybridhealthservice/services	query-only
microsoft.advisor/advisorscore	query-only
microsoft.advisor/assessments	query-only
microsoft.advisor/assessmenttypes	query-only
microsoft.advisor/configurations	query-only
microsoft.advisor/criticalresources	query-only
microsoft.advisor/recommendations	query-only
microsoft.advisor/resiliencyreviews	query-only
microsoft.advisor/risks	query-only
microsoft.advisor/risktypes	query-only
microsoft.advisor/suppressions	query-only
microsoft.advisor/triagerecommendations	query-only
microsoft.advisor/triageresources	query-only
microsoft.advisor/workloads	query-only
microsoft.alertsmanagement/actionrules	query-only
microsoft.alertsmanagement/alertrulerecommendations	query-only
microsoft.alertsmanagement/alertsmetadata	query-only
microsoft.alertsmanagement/alerts	query-only
microsoft.alertsmanagement/alertssummary	query-only
microsoft.alertsmanagement/issues	query-only
microsoft.alertsmanagement/prometheusrulegroups	query-only
microsoft.alertsmanagement/smartdetectoralertrules	query-only
microsoft.alertsmanagement/smartgroups	query-only
microsoft.alertsmanagement/tenantactivitylogalerts	query-only
microsoft.applicationmigration/codescanmappings	metadata/ops
microsoft.applicationmigration/codescansites	metadata/ops
microsoft.applicationmigration/connectionhubs	metadata/ops
microsoft.applicationmigration/discoveryhubs	metadata/ops
microsoft.applicationmigration/mongosites	metadata/ops
microsoft.applicationmigration/networksites	metadata/ops
microsoft.applicationmigration/oraclesites	metadata/ops
microsoft.applicationmigration/pgsqlsites	metadata/ops
microsoft.applicationmigration/storagesites	metadata/ops
microsoft.authorization/accessreviewhistorydefinitions	metadata/ops
microsoft.authorization/accessreviewscheduledefinitions	metadata/ops
microsoft.authorization/accessreviewschedulesettings	metadata/ops
microsoft.authorization/attributenamespaces	metadata/ops
microsoft.authorization/denyassignments	metadata/ops
microsoft.authorization/diagnosticsettingscategories	metadata/ops
microsoft.authorization/diagnosticsettings	metadata/ops
microsoft.authorization/eligiblechildresources	metadata/ops
microsoft.authorization/locks	metadata/ops
microsoft.authorization/permissions	metadata/ops
microsoft.authorization/policyenrollments	metadata/ops
microsoft.authorization/policyexemptions	metadata/ops
microsoft.authorization/privatelinkassociations	metadata/ops
microsoft.authorization/resourcemanagementprivatelinks	metadata/ops
microsoft.authorization/roleassignmentapprovals	metadata/ops
microsoft.authorization/roleassignmentscheduleinstances	metadata/ops
microsoft.authorization/roleassignmentschedulerequests	metadata/ops
microsoft.authorization/roleassignmentschedules	metadata/ops
microsoft.authorization/roleassignmentsusagemetrics	metadata/ops
microsoft.authorization/roleeligibilityscheduleinstances	metadata/ops
microsoft.authorization/roleeligibilityschedulerequests	metadata/ops
microsoft.authorization/roleeligibilityschedules	metadata/ops
microsoft.authorization/rolemanagementalertconfigurations	metadata/ops
microsoft.authorization/rolemanagementalertdefinitions	metadata/ops
microsoft.authorization/rolemanagementalertoperations	metadata/ops
microsoft.authorization/rolemanagementalerts	metadata/ops
microsoft.authorization/rolemanagementpolicies	metadata/ops
microsoft.authorization/rolemanagementpolicyassignments	metadata/ops
microsoft.awsconnector/accessanalyzeranalyzers	third-party
microsoft.awsconnector/acmcertificatesummaries	third-party
microsoft.awsconnector/apigatewayrestapis	third-party
microsoft.awsconnector/apigatewaystages	third-party
microsoft.awsconnector/applicationautoscalingscalabletargets	third-party
microsoft.awsconnector/appsyncgraphqlapis	third-party
microsoft.awsconnector/autoscalingautoscalinggroups	third-party
microsoft.awsconnector/bedrockagentaliases	third-party
microsoft.awsconnector/bedrockagents	third-party
microsoft.awsconnector/bedrockapplicationinferenceprofiles	third-party
microsoft.awsconnector/bedrockblueprints	third-party
microsoft.awsconnector/bedrockdataautomationprojects	third-party
microsoft.awsconnector/bedrockdatasources	third-party
microsoft.awsconnector/bedrockflowaliases	third-party
microsoft.awsconnector/bedrockguardrails	third-party
microsoft.awsconnector/bedrockknowledgebases	third-party
microsoft.awsconnector/bedrockprompts	third-party
microsoft.awsconnector/cloudformationstacksets	third-party
microsoft.awsconnector/cloudformationstacks	third-party
microsoft.awsconnector/cloudfrontdistributions	third-party
microsoft.awsconnector/cloudtrailtrails	third-party
microsoft.awsconnector/cloudwatchalarms	third-party
microsoft.awsconnector/codebuildprojects	third-party
microsoft.awsconnector/codebuildsourcecredentialsinfos	third-party
microsoft.awsconnector/configserviceconfigurationrecorderstatuses	third-party
microsoft.awsconnector/configserviceconfigurationrecorders	third-party
microsoft.awsconnector/configservicedeliverychannels	third-party
microsoft.awsconnector/databasemigrationservicereplicationinstances	third-party
microsoft.awsconnector/daxclusters	third-party
microsoft.awsconnector/dynamodbcontinuousbackupsdescriptions	third-party
microsoft.awsconnector/dynamodbtables	third-party
microsoft.awsconnector/ec2accountattributes	third-party
microsoft.awsconnector/ec2addresses	third-party
microsoft.awsconnector/ec2flowlogs	third-party
microsoft.awsconnector/ec2images	third-party
microsoft.awsconnector/ec2instancestatuses	third-party
microsoft.awsconnector/ec2instances	third-party
microsoft.awsconnector/ec2ipams	third-party
microsoft.awsconnector/ec2keypairs	third-party
microsoft.awsconnector/ec2networkacls	third-party
microsoft.awsconnector/ec2networkinterfaces	third-party
microsoft.awsconnector/ec2routetables	third-party
microsoft.awsconnector/ec2securitygroups	third-party
microsoft.awsconnector/ec2snapshots	third-party
microsoft.awsconnector/ec2subnets	third-party
microsoft.awsconnector/ec2volumes	third-party
microsoft.awsconnector/ec2vpcendpoints	third-party
microsoft.awsconnector/ec2vpcpeeringconnections	third-party
microsoft.awsconnector/ec2vpcs	third-party
microsoft.awsconnector/ecrimagedetails	third-party
microsoft.awsconnector/ecrrepositories	third-party
microsoft.awsconnector/ecsclusters	third-party
microsoft.awsconnector/ecsservices	third-party
microsoft.awsconnector/ecstaskdefinitions	third-party
microsoft.awsconnector/efsfilesystems	third-party
microsoft.awsconnector/efsmounttargets	third-party
microsoft.awsconnector/eksclusters	third-party
microsoft.awsconnector/eksnodegroups	third-party
microsoft.awsconnector/elasticbeanstalkapplications	third-party
microsoft.awsconnector/elasticbeanstalkconfigurationtemplates	third-party
microsoft.awsconnector/elasticbeanstalkenvironments	third-party
microsoft.awsconnector/elasticloadbalancingv2listeners	third-party
microsoft.awsconnector/elasticloadbalancingv2loadbalancers	third-party
microsoft.awsconnector/elasticloadbalancingv2targetgroups	third-party
microsoft.awsconnector/elasticloadbalancingv2targethealthdescriptions	third-party
microsoft.awsconnector/elasticsearchdomains	third-party
microsoft.awsconnector/emrclusters	third-party
microsoft.awsconnector/guarddutydetectors	third-party
microsoft.awsconnector/iamaccesskeylastuseds	third-party
microsoft.awsconnector/iamaccesskeymetadata	third-party
microsoft.awsconnector/iamgroups	third-party
microsoft.awsconnector/iaminstanceprofiles	third-party
microsoft.awsconnector/iammanagedpolicies	third-party
microsoft.awsconnector/iammfadevices	third-party
microsoft.awsconnector/iampasswordpolicies	third-party
microsoft.awsconnector/iampolicyversions	third-party
microsoft.awsconnector/iamroles	third-party
microsoft.awsconnector/iamservercertificates	third-party
microsoft.awsconnector/iamuserpolicies	third-party
microsoft.awsconnector/iamvirtualmfadevices	third-party
microsoft.awsconnector/kmsaliases	third-party
microsoft.awsconnector/kmskeys	third-party
microsoft.awsconnector/lambdaeventinvokeconfigs	third-party
microsoft.awsconnector/lambdafunctioncodelocations	third-party
microsoft.awsconnector/lambdafunctionconfigurations	third-party
microsoft.awsconnector/lambdafunctions	third-party
microsoft.awsconnector/lambdapermissions	third-party
microsoft.awsconnector/licensemanagerlicenses	third-party
microsoft.awsconnector/lightsailbuckets	third-party
microsoft.awsconnector/lightsailinstances	third-party
microsoft.awsconnector/logsloggroups	third-party
microsoft.awsconnector/logslogstreams	third-party
microsoft.awsconnector/logsmetricfilters	third-party
microsoft.awsconnector/logssubscriptionfilters	third-party
microsoft.awsconnector/macie2jobsummaries	third-party
microsoft.awsconnector/macieallowlists	third-party
microsoft.awsconnector/networkfirewallfirewallpolicies	third-party
microsoft.awsconnector/networkfirewallfirewalls	third-party
microsoft.awsconnector/networkfirewallrulegroups	third-party
microsoft.awsconnector/opensearchdomainstatuses	third-party
microsoft.awsconnector/opensearchservicedomains	third-party
microsoft.awsconnector/organizationsaccounts	third-party
microsoft.awsconnector/organizationsorganizations	third-party
microsoft.awsconnector/rdsdbclusters	third-party
microsoft.awsconnector/rdsdbinstances	third-party
microsoft.awsconnector/rdsdbsnapshotattributesresults	third-party
microsoft.awsconnector/rdsdbsnapshots	third-party
microsoft.awsconnector/rdseventsubscriptions	third-party
microsoft.awsconnector/rdsexporttasks	third-party
microsoft.awsconnector/redshiftclusterparametergroups	third-party
microsoft.awsconnector/redshiftclusters	third-party
microsoft.awsconnector/route53domainsdomainsummaries	third-party
microsoft.awsconnector/route53hostedzones	third-party
microsoft.awsconnector/route53resourcerecordsets	third-party
microsoft.awsconnector/s3accesscontrolpolicies	third-party
microsoft.awsconnector/s3accesspoints	third-party
microsoft.awsconnector/s3bucketpolicies	third-party
microsoft.awsconnector/s3buckets	third-party
microsoft.awsconnector/s3controlmultiregionaccesspointpolicydocuments	third-party
microsoft.awsconnector/sagemakerapps	third-party
microsoft.awsconnector/sagemakerdevices	third-party
microsoft.awsconnector/sagemakerimages	third-party
microsoft.awsconnector/sagemakernotebookinstancesummaries	third-party
microsoft.awsconnector/secretsmanagerresourcepolicies	third-party
microsoft.awsconnector/secretsmanagersecrets	third-party
microsoft.awsconnector/snssubscriptions	third-party
microsoft.awsconnector/snstopics	third-party
microsoft.awsconnector/sqsqueues	third-party
microsoft.awsconnector/ssminstanceinformations	third-party
microsoft.awsconnector/ssmparameters	third-party
microsoft.awsconnector/ssmresourcecompliancesummaryitems	third-party
microsoft.awsconnector/wafv2ipsets	third-party
microsoft.awsconnector/wafv2loggingconfigurations	third-party
microsoft.awsconnector/wafv2webaclassociations	third-party
microsoft.awsconnector/wafwebaclsummaries	third-party
microsoft.azureactivedirectory/associatedbillingaccounts	metadata/ops
microsoft.azureactivedirectory/b2cdirectories	metadata/ops
microsoft.azureactivedirectory/b2ctenants	metadata/ops
microsoft.azureactivedirectory/ciamdirectories	metadata/ops
microsoft.azureactivedirectory/directories	metadata/ops
microsoft.azureactivedirectory/entratenants	metadata/ops
microsoft.azureactivedirectory/guestusages	metadata/ops
microsoft.billingbenefits/applicableconditionalcredits	billing-only
microsoft.billingbenefits/applicablecontributors	billing-only
microsoft.billingbenefits/applicablecredits	billing-only
microsoft.billingbenefits/applicablediscounts	billing-only
microsoft.billingbenefits/applicablemaccs	billing-only
microsoft.billingbenefits/conditionalcredits	billing-only
microsoft.billingbenefits/credits	billing-only
microsoft.billingbenefits/discounts	billing-only
microsoft.billingbenefits/freeservices	billing-only
microsoft.billingbenefits/incentiveschedules	billing-only
microsoft.billingbenefits/maccs	billing-only
microsoft.billingbenefits/reservationorderaliases	billing-only
microsoft.billingbenefits/savingsplanorderaliases	billing-only
microsoft.billingbenefits/savingsplanorders	billing-only
microsoft.billingbenefits/savingsplans	billing-only
microsoft.billing/billingaccounts	billing-only
microsoft.billing/billingperiods	billing-only
microsoft.billing/billingpermissions	billing-only
microsoft.billing/billingproperty	billing-only
microsoft.billing/billingrequests	billing-only
microsoft.billing/billingroleassignments	billing-only
microsoft.billing/billingroledefinitions	billing-only
microsoft.billing/departments	billing-only
microsoft.billing/enrollmentaccounts	billing-only
microsoft.billing/invoices	billing-only
microsoft.billing/paymentmethods	billing-only
microsoft.billing/permissionrequests	billing-only
microsoft.billing/policies	billing-only
microsoft.billing/promotionalcredits	billing-only
microsoft.billing/promotions	billing-only
microsoft.billing/transfers	billing-only
microsoft.capacity/appliedreservations	metadata/ops
microsoft.capacity/catalogs	metadata/ops
microsoft.capacity/commercialreservationorders	metadata/ops
microsoft.capacity/exchange	metadata/ops
microsoft.capacity/ownreservations	metadata/ops
microsoft.capacity/placepurchaseorder	metadata/ops
microsoft.capacity/reservationorders	metadata/ops
microsoft.capacity/reservations	metadata/ops
microsoft.capacity/resourceproviders	metadata/ops
microsoft.capacity/resources	metadata/ops
microsoft.carbon/carbonemissionreports	metadata/ops
microsoft.changesafety/changeactivityevents	metadata/ops
microsoft.changesafety/changerecords	metadata/ops
microsoft.changesafety/changestates	metadata/ops
microsoft.changesafety/saferollouts	metadata/ops
microsoft.changesafety/stagemaps	metadata/ops
microsoft.changesafety/validations	metadata/ops
microsoft.changesafety/validators	metadata/ops
microsoft.commerce/ratecard	billing-only
microsoft.commerce/usageaggregates	billing-only
microsoft.consumption/aggregatedcost	billing-only
microsoft.consumption/balances	billing-only
microsoft.consumption/budgets	billing-only
microsoft.consumption/charges	billing-only
microsoft.consumption/costtags	billing-only
microsoft.consumption/credits	billing-only
microsoft.consumption/events	billing-only
microsoft.consumption/forecasts	billing-only
microsoft.consumption/lots	billing-only
microsoft.consumption/marketplaces	billing-only
microsoft.consumption/pricesheets	billing-only
microsoft.consumption/products	billing-only
microsoft.consumption/reservationdetails	billing-only
microsoft.consumption/reservationrecommendationdetails	billing-only
microsoft.consumption/reservationrecommendations	billing-only
microsoft.consumption/reservationsummaries	billing-only
microsoft.consumption/reservationtransactions	billing-only
microsoft.consumption/tags	billing-only
microsoft.consumption/tenants	billing-only
microsoft.consumption/terms	billing-only
microsoft.consumption/usagedetails	billing-only
microsoft.costmanagement/alerts	billing-only
microsoft.costmanagement/benefitrecommendations	billing-only
microsoft.costmanagement/benefitutilizationsummaries	billing-only
microsoft.costmanagement/benefitutilizationsummariesoperationresults	billing-only
microsoft.costmanagement/billingaccounts	billing-only
microsoft.costmanagement/budgets	billing-only
microsoft.costmanagement/costallocationrules	billing-only
microsoft.costmanagement/costdetailsoperationresults	billing-only
microsoft.costmanagement/departments	billing-only
microsoft.costmanagement/dimensions	billing-only
microsoft.costmanagement/enrollmentaccounts	billing-only
microsoft.costmanagement/fetchmarketplaceprices	billing-only
microsoft.costmanagement/fetchmicrosoftprices	billing-only
microsoft.costmanagement/fetchprices	billing-only
microsoft.costmanagement/forecast	billing-only
microsoft.costmanagement/insights	billing-only
microsoft.costmanagement/markuprules	billing-only
microsoft.costmanagement/pricesheets	billing-only
microsoft.costmanagement/pricesheetsoperationresults	billing-only
microsoft.costmanagement/pricesheetsoperationstatus	billing-only
microsoft.costmanagement/publish	billing-only
microsoft.costmanagement/reservationdetailsoperationresults	billing-only
microsoft.costmanagement/scheduledactions	billing-only
microsoft.costmanagement/sendmessage	billing-only
microsoft.costmanagement/settings	billing-only
microsoft.costmanagement/startconversation	billing-only
microsoft.costmanagement/views	billing-only
microsoft.customerlockbox/disablelockbox	metadata/ops
microsoft.customerlockbox/requests	metadata/ops
microsoft.customerlockbox/tenantoptedin	metadata/ops
microsoft.edge/configtemplates	metadata/ops
microsoft.edge/configurationreferences	metadata/ops
microsoft.edge/configurations	metadata/ops
microsoft.edge/connectivitystatuses	metadata/ops
microsoft.edge/contexts	metadata/ops
microsoft.edge/diagnostics	metadata/ops
microsoft.edge/jobs	metadata/ops
microsoft.edge/resourceinsights	metadata/ops
microsoft.edge/schemareferences	metadata/ops
microsoft.edge/schemas	metadata/ops
microsoft.edge/siteawareresourcetypes	metadata/ops
microsoft.edge/sitekeys	metadata/ops
microsoft.edge/sites	metadata/ops
microsoft.edge/solutiondeployments	metadata/ops
microsoft.edge/solutiontemplates	metadata/ops
microsoft.edge/targets	metadata/ops
microsoft.edge/updates	metadata/ops
microsoft.edge/workflows	metadata/ops
microsoft.features/featureconfigurations	metadata/ops
microsoft.features/featureprovidernamespaces	metadata/ops
microsoft.features/featureproviders	metadata/ops
microsoft.features/features	metadata/ops
microsoft.features/providers	metadata/ops
microsoft.features/subscriptionfeatureregistrations	metadata/ops
microsoft.gcpconnector/bigquerydatasets	third-party
microsoft.gcpconnector/cloudfunctions	third-party
microsoft.gcpconnector/computeinstances	third-party
microsoft.gcpconnector/containerclusters	third-party
microsoft.gcpconnector/locationss	third-party
microsoft.gcpconnector/sqladmininstances	third-party
microsoft.gcpconnector/storagebuckets	third-party
microsoft.help/diagnostics	metadata/ops
microsoft.help/discoversolutions	metadata/ops
microsoft.help/discoverysolutions	metadata/ops
microsoft.help/monitorinsights	metadata/ops
microsoft.help/plugins	metadata/ops
microsoft.help/selfhelp	metadata/ops
microsoft.help/simplifiedsolutions	metadata/ops
microsoft.help/smartdiagnostics	metadata/ops
microsoft.help/solutions	metadata/ops
microsoft.help/troubleshooters	metadata/ops
microsoft.insights/actiongroups	query-only
microsoft.insights/activitylogalerts	query-only
microsoft.insights/autoscalesettings	query-only
microsoft.insights/components	query-only
microsoft.insights/datacollectionendpoints	query-only
microsoft.insights/datacollectionruleassociations	query-only
microsoft.insights/datacollectionrules	query-only
microsoft.insights/diagnosticsettingscategories	query-only
microsoft.insights/diagnosticsettings	query-only
microsoft.insights/eventcategories	query-only
microsoft.insights/eventtypes	query-only
microsoft.insights/extendeddiagnosticsettings	query-only
microsoft.insights/logdefinitions	query-only
microsoft.insights/logprofiles	query-only
microsoft.insights/logs	query-only
microsoft.insights/metricalerts	query-only
microsoft.insights/metricbaselines	query-only
microsoft.insights/metricbatch	query-only
microsoft.insights/metricdefinitions	query-only
microsoft.insights/metricnamespaces	query-only
microsoft.insights/metrics	query-only
microsoft.insights/monitoredobjects	query-only
microsoft.insights/notificationstatus	query-only
microsoft.insights/platformlogsconfigurations	query-only
microsoft.insights/privatelinkscopeoperationstatuses	query-only
microsoft.insights/privatelinkscopes	query-only
microsoft.insights/rollbacktolegacypricingmodel	query-only
microsoft.insights/scheduledqueryrules	query-only
microsoft.insights/tenantactiongroups	query-only
microsoft.insights/topology	query-only
microsoft.insights/transactions	query-only
microsoft.insights/vminsightsonboardingstatuses	query-only
microsoft.insights/webtests	query-only
microsoft.insights/workbooks	query-only
microsoft.insights/workbooktemplates	query-only
microsoft.iotsecurity/alerttypes	query-only
microsoft.iotsecurity/defendersettings	query-only
microsoft.iotsecurity/licenseskus	query-only
microsoft.iotsecurity/onpremisesensors	query-only
microsoft.iotsecurity/recommendationtypes	query-only
microsoft.iotsecurity/sensors	query-only
microsoft.iotsecurity/sites	query-only
microsoft.managedops/managedops	metadata/ops
microsoft.management/resources	metadata/ops
microsoft.management/servicegroups	metadata/ops
microsoft.management/starttenantbackfill	metadata/ops
microsoft.management/tenantbackfillstatus	metadata/ops
microsoft.marketplace/mysolutions	metadata/ops
microsoft.marketplace/offers	metadata/ops
microsoft.marketplaceordering/agreements	metadata/ops
microsoft.marketplaceordering/offertypes	metadata/ops
microsoft.marketplace/privatestores	metadata/ops
microsoft.marketplace/products	metadata/ops
microsoft.marketplace/publishers	metadata/ops
microsoft.marketplace/search	metadata/ops
microsoft.partnermanagedconsumerrecurrence/recurrences	metadata/ops
microsoft.policyinsights/asyncoperationresults	metadata/ops
microsoft.policyinsights/componentpolicystates	metadata/ops
microsoft.policyinsights/derivepolicyproperties	metadata/ops
microsoft.policyinsights/eventgridfilters	metadata/ops
microsoft.policyinsights/handlepolicycopilotrequest	metadata/ops
microsoft.policyinsights/policyevents	metadata/ops
microsoft.policyinsights/policystates	metadata/ops
microsoft.policyinsights/policytrackedresources	metadata/ops
microsoft.policyinsights/verifypolicy	metadata/ops
microsoft.portal/consoles	metadata/ops
microsoft.portal/dashboards	metadata/ops
microsoft.portal/flags	metadata/ops
microsoft.portal/tenantconfigurations	metadata/ops
microsoft.portal/usersettings	metadata/ops
microsoft.providerhub/availableaccounts	metadata/ops
microsoft.providerhub/providerregistrations	metadata/ops
microsoft.quota/groupquotas	metadata/ops
microsoft.quota/operationsstatus	metadata/ops
microsoft.quota/quotarequests	metadata/ops
microsoft.recommendationsservice/accounts	query-only
microsoft.relationships/dependencyof	query-only
microsoft.relationships/operationsstatuses	query-only
microsoft.relationships/servicegroupmember	query-only
microsoft.resourcegraph/queries	metadata/ops
microsoft.resourcegraph/resourcechangedetails	metadata/ops
microsoft.resourcegraph/resourcechanges	metadata/ops
microsoft.resourcegraph/resourceshistory	metadata/ops
microsoft.resourcegraph/resources	metadata/ops
microsoft.resourcegraph/subscriptionsstatus	metadata/ops
microsoft.resourcehealth/availabilitystatuses	query-only
microsoft.resourcehealth/childavailabilitystatuses	query-only
microsoft.resourcehealth/childresources	query-only
microsoft.resourcehealth/emergingissues	query-only
microsoft.resourcehealth/events	query-only
microsoft.resources/builtintemplatespecs	metadata/ops
microsoft.resources/bulkdelete	metadata/ops
microsoft.resources/changes	metadata/ops
microsoft.resources/databoundaries	metadata/ops
microsoft.resources/decompilebicep	metadata/ops
microsoft.resources/deploymentscripts	metadata/ops
microsoft.resources/deployments	metadata/ops
microsoft.resources/deploymentstacks	metadata/ops
microsoft.resources/deploymentstackswhatifresults	metadata/ops
microsoft.resources/links	metadata/ops
microsoft.resources/mobobrokers	metadata/ops
microsoft.resources/notifyresourcejobs	metadata/ops
microsoft.resources/populateregionalmovetargetresource	metadata/ops
microsoft.resources/providers	metadata/ops
microsoft.resources/regionalmovecontainers	metadata/ops
microsoft.resources/relayregionalmoverequest	metadata/ops
microsoft.resources/resources	metadata/ops
microsoft.resources/snapshots	metadata/ops
microsoft.resources/tags	metadata/ops
microsoft.resources/tagsoperationresults	metadata/ops
microsoft.resources/templatespecs	metadata/ops
microsoft.resources/tenants	metadata/ops
microsoft.saashub/cancreate	third-party
microsoft.saashub/cantransact	third-party
microsoft.saashub/cloudservices	third-party
microsoft.saashub/coterminousdates	third-party
microsoft.saashub/saasresources	third-party
microsoft.saashub/tenantlevelcancreate	third-party
microsoft.saashub/tenantlevelcantransact	third-party
microsoft.saashub/tenantlevelcoterminousdates	third-party
microsoft.security/advancedthreatprotectionsettings	query-only
microsoft.security/aggregations	query-only
microsoft.security/alerts	query-only
microsoft.security/alertssuppressionrules	query-only
microsoft.security/allowedconnections	query-only
microsoft.security/apicollections	query-only
microsoft.security/applications	query-only
microsoft.security/assessmentmetadata	query-only
microsoft.security/assessments	query-only
microsoft.security/assignments	query-only
microsoft.security/autodismissalertsrules	query-only
microsoft.security/automations	query-only
microsoft.security/autoprovisioningsettings	query-only
microsoft.security/complianceresults	query-only
microsoft.security/compliances	query-only
microsoft.security/customassessmentautomations	query-only
microsoft.security/customrecommendations	query-only
microsoft.security/datacollectionagents	query-only
microsoft.security/datascanners	query-only
microsoft.security/defenderforstoragesettings	query-only
microsoft.security/devicesecuritygroups	query-only
microsoft.security/discoveredsecuritysolutions	query-only
microsoft.security/externalsecuritysolutions	query-only
microsoft.security/governancerules	query-only
microsoft.security/healthreports	query-only
microsoft.security/informationprotectionpolicies	query-only
microsoft.securityinsights/aggregations	query-only
microsoft.securityinsights/alertrules	query-only
microsoft.securityinsights/alertruletemplates	query-only
microsoft.securityinsights/automationrules	query-only
microsoft.securityinsights/billingstatistics	query-only
microsoft.securityinsights/bookmarks	query-only
microsoft.securityinsights/cases	query-only
microsoft.securityinsights/confidentialwatchlists	query-only
microsoft.securityinsights/contentpackages	query-only
microsoft.securityinsights/contentproductpackages	query-only
microsoft.securityinsights/contentproducttemplates	query-only
microsoft.securityinsights/contenttemplates	query-only
microsoft.securityinsights/dataconnectordefinitions	query-only
microsoft.securityinsights/dataconnectorscheckrequirements	query-only
microsoft.securityinsights/dataconnectors	query-only
microsoft.securityinsights/enrichment	query-only
microsoft.securityinsights/enrichmentwidgets	query-only
microsoft.securityinsights/entities	query-only
microsoft.securityinsights/entityqueries	query-only
microsoft.securityinsights/entityquerytemplates	query-only
microsoft.securityinsights/fileimports	query-only
microsoft.securityinsights/huntsessions	query-only
microsoft.securityinsights/hunts	query-only
microsoft.securityinsights/incidents	query-only
microsoft.securityinsights/mitrecoveragerecords	query-only
microsoft.securityinsights/officeconsents	query-only
microsoft.securityinsights/onboardingstates	query-only
microsoft.securityinsights/overview	query-only
microsoft.securityinsights/recommendations	query-only
microsoft.securityinsights/securitymlanalyticssettings	query-only
microsoft.securityinsights/settings	query-only
microsoft.securityinsights/sourcecontrols	query-only
microsoft.securityinsights/threatintelligence	query-only
microsoft.securityinsights/triggeredanalyticsruleruns	query-only
microsoft.securityinsights/watchlists	query-only
microsoft.securityinsights/workspacemanagerassignments	query-only
microsoft.securityinsights/workspacemanagerconfigurations	query-only
microsoft.securityinsights/workspacemanagergroups	query-only
microsoft.securityinsights/workspacemanagermembers	query-only
microsoft.security/integrations	query-only
microsoft.security/iotsecuritysolutions	query-only
microsoft.security/jitnetworkaccesspolicies	query-only
microsoft.security/jitpolicies	query-only
microsoft.security/mdeonboardings	query-only
microsoft.security/policies	query-only
microsoft.security/privatelinks	query-only
microsoft.security/regulatorycompliancestandards	query-only
microsoft.security/securescorecontroldefinitions	query-only
microsoft.security/securescorecontrols	query-only
microsoft.security/securescores	query-only
microsoft.security/securityconnectors	query-only
microsoft.security/securitycontacts	query-only
microsoft.security/securityoperators	query-only
microsoft.security/securitysolutions	query-only
microsoft.security/securitysolutionsreferencedata	query-only
microsoft.security/securitystandards	query-only
microsoft.security/securitystatuses	query-only
microsoft.security/securitystatusessummaries	query-only
microsoft.security/sensitivitysettings	query-only
microsoft.security/servervulnerabilityassessments	query-only
microsoft.security/servervulnerabilityassessmentssettings	query-only
microsoft.security/settings	query-only
microsoft.security/sqlvulnerabilityassessments	query-only
microsoft.security/standardassignments	query-only
microsoft.security/standards	query-only
microsoft.security/subassessments	query-only
microsoft.security/tasks	query-only
microsoft.security/topologies	query-only
microsoft.security/trustedips	query-only
microsoft.security/vmscanners	query-only
microsoft.security/workspacesettings	query-only
microsoft.softwareplan/hybridusebenefits	metadata/ops
microsoft.softwareplan/licensesummary	metadata/ops
microsoft.softwareplan/licensesummaryoperationresults	metadata/ops
microsoft.softwareplan/softwarebenefits	metadata/ops
microsoft.softwareplan/softwarelicenses	metadata/ops
microsoft.softwareplan/softwareperpetualproducts	metadata/ops
microsoft.softwareplan/softwaresubscriptions	metadata/ops
microsoft.subscription/acceptchangetenant	metadata/ops
microsoft.subscription/acceptownership	metadata/ops
microsoft.subscription/acceptownershipstatus	metadata/ops
microsoft.subscription/aliases	metadata/ops
microsoft.subscription/cancel	metadata/ops
microsoft.subscription/changetenantrequest	metadata/ops
microsoft.subscription/changetenantstatus	metadata/ops
microsoft.subscription/directories	metadata/ops
microsoft.subscription/enable	metadata/ops
microsoft.subscription/policies	metadata/ops
microsoft.subscription/rename	metadata/ops
microsoft.subscription/subscriptiondefinitions	metadata/ops
microsoft.subscription/subscriptionoperations	metadata/ops
microsoft.subscription/subscriptions	metadata/ops
microsoft.subscription/supportplans	metadata/ops
microsoft.subscription/transfers	metadata/ops
microsoft.support/classifyservices	metadata/ops
microsoft.support/fileworkspaces	metadata/ops
microsoft.support/lookupresourceid	metadata/ops
microsoft.support/operationsstatus	metadata/ops
microsoft.support/services	metadata/ops
microsoft.support/supporttickets	metadata/ops
```

## DEFER — nested child types (parent-scoped; revisit via parent fan-out)

These 2319 types have `>=2` path segments and list only under a parent resource.
Scanned only if/when a parent fan-out is added. Full list:

```
microsoft.aad/domainservices/oucontainer
microsoft.aad/domainservices/unsuspend
microsoft.aad/locations/operationresults
microsoft.analysisservices/locations/checknameavailability
microsoft.analysisservices/locations/operationresults
microsoft.analysisservices/locations/operationstatuses
microsoft.apicenter/services/eventgridfilters
microsoft.apimanagement/locations/deletedgateways
microsoft.apimanagement/locations/deletedservices
microsoft.apimanagement/locations/operationresults
microsoft.apimanagement/locations/operationsstatuses
microsoft.apimanagement/service/eventgridfilters
microsoft.appassessment/locations/operationstatuses
microsoft.appassessment/locations/osversions
microsoft.app/builders/builds
microsoft.app/builders/patches
microsoft.appcomplianceautomation/locations/operationstatuses
microsoft.appcomplianceautomation/reports/evidences
microsoft.appcomplianceautomation/reports/scopingconfigurations
microsoft.appcomplianceautomation/reports/snapshots
microsoft.appcomplianceautomation/reports/snapshots/controls
microsoft.appcomplianceautomation/reports/webhooks
microsoft.appconfiguration/configurationstores/eventgridfilters
microsoft.appconfiguration/configurationstores/experimentation
microsoft.appconfiguration/configurationstores/keyvalues
microsoft.appconfiguration/configurationstores/networksecurityperimeterassociationproxies
microsoft.appconfiguration/configurationstores/networksecurityperimeterconfigurations
microsoft.appconfiguration/configurationstores/replicas
microsoft.appconfiguration/configurationstores/snapshots
microsoft.appconfiguration/locations/checknameavailability
microsoft.appconfiguration/locations/deletedconfigurationstores
microsoft.appconfiguration/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.appconfiguration/locations/operationresults
microsoft.appconfiguration/locations/operationsstatus
microsoft.app/connectedenvironments/certificates
microsoft.app/containerapps/privateendpointconnectionproxies
microsoft.app/containerapps/resiliencypolicies
microsoft.applicationmigration/codescansites/codescanreports
microsoft.applicationmigration/codescansites/codescanreports/codescanissues
microsoft.applicationmigration/connectionhubs/githubconnections
microsoft.applicationmigration/connectionhubs/githubconnections/githubissues
microsoft.applicationmigration/discoveryhubs/applications
microsoft.applicationmigration/discoveryhubs/applications/members
microsoft.applicationmigration/locations/operationstatuses
microsoft.applicationmigration/mongosites/agents
microsoft.applicationmigration/mongosites/mongocollections
microsoft.applicationmigration/mongosites/mongodatabases
microsoft.applicationmigration/mongosites/mongoinstances
microsoft.applicationmigration/networksites/agents
microsoft.applicationmigration/networksites/nsxdistributedfirewallpolicies
microsoft.applicationmigration/networksites/nsxdistributedfirewallrules
microsoft.applicationmigration/networksites/nsxgatewayfirewallpolicies
microsoft.applicationmigration/networksites/nsxgatewayfirewallrules
microsoft.applicationmigration/networksites/nsxloadbalancers
microsoft.applicationmigration/networksites/nsxmanagers
microsoft.applicationmigration/networksites/nsxnatrules
microsoft.applicationmigration/networksites/nsxnsgroups
microsoft.applicationmigration/networksites/nsxsegments
microsoft.applicationmigration/networksites/nsxtier0gateways
microsoft.applicationmigration/networksites/nsxtier1gateways
microsoft.applicationmigration/oraclesites/agents
microsoft.applicationmigration/oraclesites/oracledatabases
microsoft.applicationmigration/oraclesites/oracleinstances
microsoft.applicationmigration/pgsqlsites/agents
microsoft.applicationmigration/pgsqlsites/pgsqldatabases
microsoft.applicationmigration/pgsqlsites/pgsqlinstances
microsoft.applicationmigration/storagesites/agents
microsoft.applicationmigration/storagesites/fileshares
microsoft.applicationmigration/storagesites/nas
microsoft.applink/locations/operationstatuses
microsoft.app/locations/availablemanagedenvironmentsworkloadprofiletypes
microsoft.app/locations/billingmeters
microsoft.app/locations/connectedenvironmentoperationresults
microsoft.app/locations/connectedenvironmentoperationstatuses
microsoft.app/locations/connectedoperationresults
microsoft.app/locations/connectedoperationstatuses
microsoft.app/locations/containerappoperationresults
microsoft.app/locations/containerappoperationstatuses
microsoft.app/locations/containerappsjoboperationresults
microsoft.app/locations/containerappsjoboperationstatuses
microsoft.app/locations/managedcertificateoperationstatuses
microsoft.app/locations/managedenvironmentoperationresults
microsoft.app/locations/managedenvironmentoperationstatuses
microsoft.app/locations/operationresults
microsoft.app/locations/operationstatuses
microsoft.app/locations/sourcecontroloperationresults
microsoft.app/locations/sourcecontroloperationstatuses
microsoft.app/locations/sreagentoperationresults
microsoft.app/locations/sreagentoperationstatuses
microsoft.app/locations/supportedagentmodels
microsoft.app/locations/usages
microsoft.app/managedenvironments/certificates
microsoft.app/managedenvironments/daprcomponents
microsoft.app/managedenvironments/daprcomponents/resiliencypolicies
microsoft.app/managedenvironments/dotnetcomponents
microsoft.app/managedenvironments/javacomponents
microsoft.app/managedenvironments/managedcertificates
microsoft.app/managedenvironments/privateendpointconnectionproxies
microsoft.appplatform/locations/checknameavailability
microsoft.appplatform/locations/operationresults
microsoft.appplatform/locations/operationstatus
microsoft.appplatform/spring/apps
microsoft.appplatform/spring/apps/deployments
microsoft.appplatform/spring/apps/deployments/operationresults
microsoft.appplatform/spring/apps/deployments/operationstatuses
microsoft.appplatform/spring/apps/domains
microsoft.appplatform/spring/apps/domains/operationresults
microsoft.appplatform/spring/apps/domains/operationstatuses
microsoft.appplatform/spring/apps/operationresults
microsoft.appplatform/spring/apps/operationstatuses
microsoft.appplatform/spring/configservers
microsoft.appplatform/spring/configservers/operationresults
microsoft.appplatform/spring/configservers/operationstatuses
microsoft.appplatform/spring/eurekaservers
microsoft.appplatform/spring/eurekaservers/operationresults
microsoft.appplatform/spring/eurekaservers/operationstatuses
microsoft.appplatform/spring/operationresults
microsoft.appplatform/spring/operationstatuses
microsoft.app/sandboxgroups/vnetconnections
microsoft.attestation/locations/defaultprovider
microsoft.authorization/migraterbac/operationstatuses
microsoft.authorization/policydefinitions/versions
microsoft.authorization/policysetdefinitions/versions
microsoft.automanage/bestpractices/versions
microsoft.automanage/configurationprofiles/versions
microsoft.automation/automationaccounts/agentregistrationinformation
microsoft.automation/automationaccounts/configurations
microsoft.automation/automationaccounts/hybridrunbookworkergroups
microsoft.automation/automationaccounts/hybridrunbookworkergroups/hybridrunbookworkers
microsoft.automation/automationaccounts/jobs
microsoft.automation/automationaccounts/privateendpointconnectionproxies
microsoft.automation/automationaccounts/privateendpointconnections
microsoft.automation/automationaccounts/privatelinkresources
microsoft.automation/automationaccounts/runbooks
microsoft.automation/automationaccounts/softwareupdateconfigurationmachineruns
microsoft.automation/automationaccounts/softwareupdateconfigurationruns
microsoft.automation/automationaccounts/softwareupdateconfigurations
microsoft.automation/automationaccounts/webhooks
microsoft.automation/locations/usages
microsoft.avs/locations/checkquotaavailability
microsoft.avs/locations/checktrialavailability
microsoft.avs/locations/usages
microsoft.avs/privateclouds/addons
microsoft.avs/privateclouds/authorizations
microsoft.avs/privateclouds/cloudlinks
microsoft.avs/privateclouds/clusters
microsoft.avs/privateclouds/clusters/datastores
microsoft.avs/privateclouds/clusters/placementpolicies
microsoft.avs/privateclouds/clusters/virtualmachines
microsoft.avs/privateclouds/eventgridfilters
microsoft.avs/privateclouds/globalreachconnections
microsoft.avs/privateclouds/hcxenterprisesites
microsoft.avs/privateclouds/iscsipaths
microsoft.avs/privateclouds/licenses
microsoft.avs/privateclouds/maintenances
microsoft.avs/privateclouds/scriptexecutions
microsoft.avs/privateclouds/scriptpackages
microsoft.avs/privateclouds/scriptpackages/scriptcmdlets
microsoft.avs/privateclouds/workloadnetworks
microsoft.avs/privateclouds/workloadnetworks/dhcpconfigurations
microsoft.avs/privateclouds/workloadnetworks/dnsservices
microsoft.avs/privateclouds/workloadnetworks/dnszones
microsoft.avs/privateclouds/workloadnetworks/gateways
microsoft.avs/privateclouds/workloadnetworks/portmirroringprofiles
microsoft.avs/privateclouds/workloadnetworks/publicips
microsoft.avs/privateclouds/workloadnetworks/segments
microsoft.avs/privateclouds/workloadnetworks/virtualmachines
microsoft.avs/privateclouds/workloadnetworks/vmgroups
microsoft.awsconnector/locations/operationstatuses
microsoft.azurearcdata/datacontrollers/activedirectoryconnectors
microsoft.azurearcdata/locations/operationstatuses
microsoft.azurearcdata/sqlmanagedinstances/failovergroups
microsoft.azurearcdata/sqlserverinstances/availabilitygroups
microsoft.azurearcdata/sqlserverinstances/databases
microsoft.azurecontextcache/accounts/containers
microsoft.azurecontextcache/locations/operationstatuses
microsoft.azuredatatransfer/connections/flows
microsoft.azuredatatransfer/locations/operationstatuses
microsoft.azuredatatransfer/pipelines/flowprofiles
microsoft.azurefleet/locations/operations
microsoft.azurelargeinstance/locations/operationsstatus
microsoft.azureplaywrightservice/accounts/quotas
microsoft.azureplaywrightservice/locations/operationstatuses
microsoft.azureplaywrightservice/locations/quotas
microsoft.azureresiliencemanagement/drills/drillresources
microsoft.azureresiliencemanagement/drills/drillruns
microsoft.azureresiliencemanagement/drills/drillruns/drillrunchildjobs
microsoft.azureresiliencemanagement/drills/drillruns/drillrunresources
microsoft.azureresiliencemanagement/goalassignments/goalresources
microsoft.azureresiliencemanagement/locations/operationstatuses
microsoft.azureresiliencemanagement/recoveryplans/recoveryjobs
microsoft.azureresiliencemanagement/recoveryplans/recoveryjobs/recoverychildjobs
microsoft.azureresiliencemanagement/recoveryplans/recoveryjobs/recoveryjobresources
microsoft.azureresiliencemanagement/recoveryplans/recoveryresources
microsoft.azureresiliencemanagement/usageplans/enrollments
microsoft.azurescan/locations/operationstatuses
microsoft.azuresphere/catalogs/certificates
microsoft.azuresphere/catalogs/images
microsoft.azuresphere/catalogs/products
microsoft.azuresphere/catalogs/products/devicegroups
microsoft.azuresphere/catalogs/products/devicegroups/deployments
microsoft.azuresphere/catalogs/products/devicegroups/devices
microsoft.azuresphere/locations/operationstatuses
microsoft.azurestackhci/clusters/arcsettings
microsoft.azurestackhci/clusters/arcsettings/extensions
microsoft.azurestackhci/clusters/deploymentsettings
microsoft.azurestackhci/clusters/jobs
microsoft.azurestackhci/clusters/offers
microsoft.azurestackhci/clusters/publishers
microsoft.azurestackhci/clusters/publishers/offers
microsoft.azurestackhci/clusters/publishers/offers/skus
microsoft.azurestackhci/clusters/securitysettings
microsoft.azurestackhci/clusters/updates
microsoft.azurestackhci/clusters/updatesummaries
microsoft.azurestackhci/clusters/updates/updateruns
microsoft.azurestackhci/edgemachines/disks
microsoft.azurestackhci/edgemachines/disks/jobs
microsoft.azurestackhci/edgemachines/disks/privilegedjobs
microsoft.azurestackhci/edgemachines/gpus
microsoft.azurestackhci/edgemachines/gpus/jobs
microsoft.azurestackhci/edgemachines/jobs
microsoft.azurestackhci/edgemachines/networkadapters
microsoft.azurestackhci/edgemachines/networkadapters/jobs
microsoft.azurestackhci/edgemachines/updates
microsoft.azurestackhci/edgemachines/volumes
microsoft.azurestackhci/locations/operationstatuses
microsoft.azurestackhci/locations/osimages
microsoft.azurestackhci/locations/platformupdates
microsoft.azurestackhci/locations/validatedsolutionrecipes
microsoft.azurestackhci/locations/validateownershipvouchers
microsoft.azurestackhci/natgateways/inboundrules
microsoft.azurestackhci/networksecuritygroups/securityrules
microsoft.azurestackhci/virtualmachines/extensions
microsoft.azurestackhci/virtualmachines/hybrididentitymetadata
microsoft.azurestackhci/virtualnetworks/subnets
microsoft.azurestack/registrations/customersubscriptions
microsoft.azurestack/registrations/products
microsoft.backupsolutions/locations/operationstatuses
microsoft.baremetalinfrastructure/locations/operationsstatus
microsoft.baremetal/locations/operationresults
microsoft.baremetal/locations/updateroutesonvirtualnetwork
microsoft.batch/batchaccounts/certificateoperationresults
microsoft.batch/batchaccounts/certificates
microsoft.batch/batchaccounts/detectors
microsoft.batch/batchaccounts/networksecurityperimeterconfigurationoperationresults
microsoft.batch/batchaccounts/operationresults
microsoft.batch/batchaccounts/pooloperationresults
microsoft.batch/batchaccounts/pools
microsoft.batch/batchaccounts/privateendpointconnectionproxyresults
microsoft.batch/batchaccounts/privateendpointconnectionresults
microsoft.batch/locations/accountoperationresults
microsoft.batch/locations/checknameavailability
microsoft.batch/locations/cloudserviceskus
microsoft.batch/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.batch/locations/quotas
microsoft.batch/locations/virtualmachineskus
microsoft.billingbenefits/credits/sources
microsoft.billingbenefits/incentiveschedules/milestones
microsoft.billingbenefits/locations/operationstatuses
microsoft.billingbenefits/maccs/contributors
microsoft.billingbenefits/savingsplanorders/return
microsoft.billingbenefits/savingsplanorders/savingsplans
microsoft.billing/billingaccounts/addresses
microsoft.billing/billingaccounts/addresses/versions
microsoft.billing/billingaccounts/agreements
microsoft.billing/billingaccounts/alertpreferences
microsoft.billing/billingaccounts/alerts
microsoft.billing/billingaccounts/appliedreservationorders
microsoft.billing/billingaccounts/associatedbillingaccounts
microsoft.billing/billingaccounts/associatedtenants
microsoft.billing/billingaccounts/availablebalance
microsoft.billing/billingaccounts/availableoffers
microsoft.billing/billingaccounts/billingperiods
microsoft.billing/billingaccounts/billingpermissions
microsoft.billing/billingaccounts/billingprofiles
microsoft.billing/billingaccounts/billingprofiles/alertpreferences
microsoft.billing/billingaccounts/billingprofiles/alerts
microsoft.billing/billingaccounts/billingprofiles/availablebalance
microsoft.billing/billingaccounts/billingprofiles/billingperiods
microsoft.billing/billingaccounts/billingprofiles/billingpermissions
microsoft.billing/billingaccounts/billingprofiles/billingrequests
microsoft.billing/billingaccounts/billingprofiles/billingroleassignments
microsoft.billing/billingaccounts/billingprofiles/billingroledefinitions
microsoft.billing/billingaccounts/billingprofiles/billingsubscriptions
microsoft.billing/billingaccounts/billingprofiles/chargerevisions
microsoft.billing/billingaccounts/billingprofiles/createbillingroleassignment
microsoft.billing/billingaccounts/billingprofiles/customers
microsoft.billing/billingaccounts/billingprofiles/customers/billingpermissions
microsoft.billing/billingaccounts/billingprofiles/customers/billingrequests
microsoft.billing/billingaccounts/billingprofiles/customers/billingroleassignments
microsoft.billing/billingaccounts/billingprofiles/customers/billingroledefinitions
microsoft.billing/billingaccounts/billingprofiles/customers/billingsubscriptions
microsoft.billing/billingaccounts/billingprofiles/customers/policies
microsoft.billing/billingaccounts/billingprofiles/customers/transactions
microsoft.billing/billingaccounts/billingprofiles/departments
microsoft.billing/billingaccounts/billingprofiles/departments/billingperiods
microsoft.billing/billingaccounts/billingprofiles/departments/billingpermissions
microsoft.billing/billingaccounts/billingprofiles/departments/billingroleassignments
microsoft.billing/billingaccounts/billingprofiles/departments/billingroledefinitions
microsoft.billing/billingaccounts/billingprofiles/departments/billingsubscriptions
microsoft.billing/billingaccounts/billingprofiles/departments/enrollmentaccounts
microsoft.billing/billingaccounts/billingprofiles/departments/enrollmentaccounts/billingperiods
microsoft.billing/billingaccounts/billingprofiles/editpurchaseordernumbers
microsoft.billing/billingaccounts/billingprofiles/enrollmentaccounts
microsoft.billing/billingaccounts/billingprofiles/enrollmentaccounts/billingperiods
microsoft.billing/billingaccounts/billingprofiles/enrollmentaccounts/billingpermissions
microsoft.billing/billingaccounts/billingprofiles/enrollmentaccounts/billingsubscriptions
microsoft.billing/billingaccounts/billingprofiles/fetchtransactionproducts
microsoft.billing/billingaccounts/billingprofiles/fetchtransactionpurchaseordernumbers
microsoft.billing/billingaccounts/billingprofiles/fetchtransactiontypes
microsoft.billing/billingaccounts/billingprofiles/instructions
microsoft.billing/billingaccounts/billingprofiles/invoices
microsoft.billing/billingaccounts/billingprofiles/invoicesections
microsoft.billing/billingaccounts/billingprofiles/invoicesections/billingpermissions
microsoft.billing/billingaccounts/billingprofiles/invoicesections/billingrequests
microsoft.billing/billingaccounts/billingprofiles/invoicesections/billingroleassignments
microsoft.billing/billingaccounts/billingprofiles/invoicesections/billingroledefinitions
microsoft.billing/billingaccounts/billingprofiles/invoicesections/billingsubscriptions
microsoft.billing/billingaccounts/billingprofiles/invoicesections/createbillingroleassignment
microsoft.billing/billingaccounts/billingprofiles/invoicesections/initiatetransfer
microsoft.billing/billingaccounts/billingprofiles/invoicesections/policies
microsoft.billing/billingaccounts/billingprofiles/invoicesections/products
microsoft.billing/billingaccounts/billingprofiles/invoicesections/products/transfer
microsoft.billing/billingaccounts/billingprofiles/invoicesections/products/updateautorenew
microsoft.billing/billingaccounts/billingprofiles/invoicesections/transactions
microsoft.billing/billingaccounts/billingprofiles/invoicesections/transfers
microsoft.billing/billingaccounts/billingprofiles/invoicesections/validatedeleteinvoicesectioneligibility
microsoft.billing/billingaccounts/billingprofiles/invoices/operationresults
microsoft.billing/billingaccounts/billingprofiles/invoices/pricesheet
microsoft.billing/billingaccounts/billingprofiles/invoices/transactions
microsoft.billing/billingaccounts/billingprofiles/invoicingpreferences
microsoft.billing/billingaccounts/billingprofiles/notificationcontacts
microsoft.billing/billingaccounts/billingprofiles/patchoperations
microsoft.billing/billingaccounts/billingprofiles/paymentmethodlinks
microsoft.billing/billingaccounts/billingprofiles/paymentmethods
microsoft.billing/billingaccounts/billingprofiles/policies
microsoft.billing/billingaccounts/billingprofiles/pricesheet
microsoft.billing/billingaccounts/billingprofiles/pricesheetdownloadoperations
microsoft.billing/billingaccounts/billingprofiles/products
microsoft.billing/billingaccounts/billingprofiles/purchaseorderinvoices
microsoft.billing/billingaccounts/billingprofiles/purchaseordermappingrules
microsoft.billing/billingaccounts/billingprofiles/purchaseordermappings
microsoft.billing/billingaccounts/billingprofiles/purchaseorders
microsoft.billing/billingaccounts/billingprofiles/purchaseordertransactionexports
microsoft.billing/billingaccounts/billingprofiles/purchaseordertransactions
microsoft.billing/billingaccounts/billingprofiles/reevaluatepurchaseordermappings
microsoft.billing/billingaccounts/billingprofiles/reservations
microsoft.billing/billingaccounts/billingprofilessummaries
microsoft.billing/billingaccounts/billingprofiles/transactions
microsoft.billing/billingaccounts/billingprofiles/validatedeletebillingprofileeligibility
microsoft.billing/billingaccounts/billingprofiles/validatedetachpaymentmethodeligibility
microsoft.billing/billingaccounts/billingprofiles/validatepurchaseordermapping
microsoft.billing/billingaccounts/billingrequests
microsoft.billing/billingaccounts/billingroleassignments
microsoft.billing/billingaccounts/billingroledefinitions
microsoft.billing/billingaccounts/billingsubscriptionaliases
microsoft.billing/billingaccounts/billingsubscriptions
microsoft.billing/billingaccounts/billingsubscriptions/elevaterole
microsoft.billing/billingaccounts/billingsubscriptions/invoices
microsoft.billing/billingaccounts/billingsubscriptions/invoices/operationresults
microsoft.billing/billingaccounts/billingsubscriptions/operationresults
microsoft.billing/billingaccounts/billingsubscriptions/policies
microsoft.billing/billingaccounts/createbillingroleassignment
microsoft.billing/billingaccounts/createinvoicesectionoperations
microsoft.billing/billingaccounts/customeraliases
microsoft.billing/billingaccounts/customers
microsoft.billing/billingaccounts/customers/billingpermissions
microsoft.billing/billingaccounts/customers/billingroleassignments
microsoft.billing/billingaccounts/customers/billingroledefinitions
microsoft.billing/billingaccounts/customers/billingsubscriptions
microsoft.billing/billingaccounts/customers/createbillingroleassignment
microsoft.billing/billingaccounts/customers/initiatetransfer
microsoft.billing/billingaccounts/customers/policies
microsoft.billing/billingaccounts/customers/products
microsoft.billing/billingaccounts/customers/transactions
microsoft.billing/billingaccounts/customers/transfers
microsoft.billing/billingaccounts/customers/transfersupportedaccounts
microsoft.billing/billingaccounts/departments
microsoft.billing/billingaccounts/departments/billingperiods
microsoft.billing/billingaccounts/departments/billingpermissions
microsoft.billing/billingaccounts/departments/billingroleassignments
microsoft.billing/billingaccounts/departments/billingroledefinitions
microsoft.billing/billingaccounts/departments/billingsubscriptions
microsoft.billing/billingaccounts/departments/enrollmentaccounts
microsoft.billing/billingaccounts/departments/enrollmentaccounts/billingperiods
microsoft.billing/billingaccounts/editpurchaseordernumbers
microsoft.billing/billingaccounts/enrollmentaccounts
microsoft.billing/billingaccounts/enrollmentaccounts/activationstatus
microsoft.billing/billingaccounts/enrollmentaccounts/billingperiods
microsoft.billing/billingaccounts/enrollmentaccounts/billingpermissions
microsoft.billing/billingaccounts/enrollmentaccounts/billingroleassignments
microsoft.billing/billingaccounts/enrollmentaccounts/billingroledefinitions
microsoft.billing/billingaccounts/enrollmentaccounts/billingsubscriptions
microsoft.billing/billingaccounts/fetchtransactionproducts
microsoft.billing/billingaccounts/fetchtransactionpurchaseordernumbers
microsoft.billing/billingaccounts/fetchtransactiontypes
microsoft.billing/billingaccounts/incentiveschedules
microsoft.billing/billingaccounts/incentiveschedules/milestones
microsoft.billing/billingaccounts/invoices
microsoft.billing/billingaccounts/invoicesections
microsoft.billing/billingaccounts/invoicesections/billingsubscriptionmoveoperations
microsoft.billing/billingaccounts/invoicesections/billingsubscriptions
microsoft.billing/billingaccounts/invoicesections/billingsubscriptions/transfer
microsoft.billing/billingaccounts/invoicesections/elevate
microsoft.billing/billingaccounts/invoicesections/initiatetransfer
microsoft.billing/billingaccounts/invoicesections/patchoperations
microsoft.billing/billingaccounts/invoicesections/productmoveoperations
microsoft.billing/billingaccounts/invoicesections/products
microsoft.billing/billingaccounts/invoicesections/products/transfer
microsoft.billing/billingaccounts/invoicesections/products/updateautorenew
microsoft.billing/billingaccounts/invoicesections/producttransfersresults
microsoft.billing/billingaccounts/invoicesections/transactions
microsoft.billing/billingaccounts/invoicesections/transfers
microsoft.billing/billingaccounts/invoices/operationresults
microsoft.billing/billingaccounts/invoices/summary
microsoft.billing/billingaccounts/invoices/transactions
microsoft.billing/billingaccounts/invoices/transactionsummary
microsoft.billing/billingaccounts/invoicingpreferences
microsoft.billing/billingaccounts/licensepurchases
microsoft.billing/billingaccounts/licensereservations
microsoft.billing/billingaccounts/lineofcredit
microsoft.billing/billingaccounts/listinvoicesectionswithcreatesubscriptionpermission
microsoft.billing/billingaccounts/listproductrecommendations
microsoft.billing/billingaccounts/migrations
microsoft.billing/billingaccounts/notificationcontacts
microsoft.billing/billingaccounts/operationresults
microsoft.billing/billingaccounts/partnerchangerequests
microsoft.billing/billingaccounts/partnerorganizations
microsoft.billing/billingaccounts/patchoperations
microsoft.billing/billingaccounts/payableoverage
microsoft.billing/billingaccounts/paymentmethodlinks
microsoft.billing/billingaccounts/paymentmethods
microsoft.billing/billingaccounts/paynow
microsoft.billing/billingaccounts/permissionrequests
microsoft.billing/billingaccounts/policies
microsoft.billing/billingaccounts/previewagreements
microsoft.billing/billingaccounts/products
microsoft.billing/billingaccounts/promotionalcredits
microsoft.billing/billingaccounts/purchaseorderinvoices
microsoft.billing/billingaccounts/purchaseordermappingrules
microsoft.billing/billingaccounts/purchaseordermappings
microsoft.billing/billingaccounts/purchaseorderoperationresults
microsoft.billing/billingaccounts/purchaseorders
microsoft.billing/billingaccounts/purchaseordertransactionexports
microsoft.billing/billingaccounts/purchaseordertransactions
microsoft.billing/billingaccounts/reevaluatepurchaseordermappings
microsoft.billing/billingaccounts/reservationorders
microsoft.billing/billingaccounts/reservationorders/reservations
microsoft.billing/billingaccounts/reservations
microsoft.billing/billingaccounts/savingsplanorders
microsoft.billing/billingaccounts/savingsplanorders/savingsplans
microsoft.billing/billingaccounts/savingsplans
microsoft.billing/billingaccounts/signagreement
microsoft.billing/billingaccounts/transactions
microsoft.billing/billingaccounts/validatepurchaseordermapping
microsoft.billing/promotionalcredits/operationresults
microsoft.billing/promotions/checkeligibility
microsoft.billing/transfers/accepttransfer
microsoft.billing/transfers/declinetransfer
microsoft.billing/transfers/operationstatus
microsoft.billing/transfers/validatetransfer
microsoft.billingtrust/locations/operationstatuses
microsoft.bing/accounts/customsearchconfigurations
microsoft.bing/accounts/skus
microsoft.bing/accounts/usages
microsoft.bing/locations/operationstatuses
microsoft.blueprint/blueprintassignments/assignmentoperations
microsoft.blueprint/blueprintassignments/operations
microsoft.blueprint/blueprints/artifacts
microsoft.blueprint/blueprints/versions
microsoft.blueprint/blueprints/versions/artifacts
microsoft.botservice/botservices/channels
microsoft.botservice/botservices/connections
microsoft.botservice/botservices/privateendpointconnectionproxies
microsoft.botservice/botservices/privateendpointconnections
microsoft.botservice/botservices/privatelinkresources
microsoft.botservice/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.cache/locations/asyncoperations
microsoft.cache/locations/checknameavailability
microsoft.cache/locations/migratedacrdnsrecords
microsoft.cache/locations/operationresults
microsoft.cache/locations/operationsstatus
microsoft.cache/redisenterprise/databases
microsoft.cache/redisenterprise/migrations
microsoft.cache/redisenterprise/privateendpointconnectionproxies
microsoft.cache/redisenterprise/privateendpointconnectionproxies/operationresults
microsoft.cache/redisenterprise/privateendpointconnectionproxies/validate
microsoft.cache/redisenterprise/privateendpointconnections
microsoft.cache/redisenterprise/privateendpointconnections/operationresults
microsoft.cache/redisenterprise/privatelinkresources
microsoft.cache/redis/eventgridfilters
microsoft.cache/redis/privateendpointconnectionproxies
microsoft.cache/redis/privateendpointconnectionproxies/validate
microsoft.cache/redis/privateendpointconnections
microsoft.cache/redis/privatelinkresources
microsoft.capacity/reservationorders/availablescopes
microsoft.capacity/reservationorders/calculaterefund
microsoft.capacity/reservationorders/changedirectory
microsoft.capacity/reservationorders/merge
microsoft.capacity/reservationorders/reservations
microsoft.capacity/reservationorders/reservations/availablescopes
microsoft.capacity/reservationorders/reservations/revisions
microsoft.capacity/reservationorders/return
microsoft.capacity/reservationorders/split
microsoft.capacity/reservationorders/swap
microsoft.capacity/resourceproviders/locations
microsoft.capacity/resourceproviders/locations/servicelimits
microsoft.capacity/resourceproviders/locations/servicelimitsrequests
microsoft.cdn/edgeactions/attachments
microsoft.cdn/edgeactions/executionfilters
microsoft.cdn/edgeactions/versioncodepackage
microsoft.cdn/edgeactions/versions
microsoft.cdn/operationresults/cdnwebapplicationfirewallpolicyresults
microsoft.cdn/operationresults/profileresults
microsoft.cdn/operationresults/profileresults/afdendpointresults
microsoft.cdn/operationresults/profileresults/afdendpointresults/routeresults
microsoft.cdn/operationresults/profileresults/agentlinkresults
microsoft.cdn/operationresults/profileresults/authpolicyresults
microsoft.cdn/operationresults/profileresults/customdomainresults
microsoft.cdn/operationresults/profileresults/endpointresults
microsoft.cdn/operationresults/profileresults/endpointresults/customdomainresults
microsoft.cdn/operationresults/profileresults/endpointresults/origingroupresults
microsoft.cdn/operationresults/profileresults/endpointresults/originresults
microsoft.cdn/operationresults/profileresults/origingroupresults
microsoft.cdn/operationresults/profileresults/origingroupresults/originresults
microsoft.cdn/operationresults/profileresults/policyresults
microsoft.cdn/operationresults/profileresults/rulesetresults
microsoft.cdn/operationresults/profileresults/rulesetresults/ruleresults
microsoft.cdn/operationresults/profileresults/secretresults
microsoft.cdn/operationresults/profileresults/securitypolicyresults
microsoft.cdn/operationresults/profileresults/targetgroupresults
microsoft.cdn/operationresults/profileresults/tunnelpolicyresults
microsoft.cdn/operationresults/webagentresults
microsoft.cdn/operationresults/webagentresults/knowledgesourceresults
microsoft.cdn/profiles/afdendpoints
microsoft.cdn/profiles/afdendpoints/routes
microsoft.cdn/profiles/agents
microsoft.cdn/profiles/authpolicies
microsoft.cdn/profiles/customdomains
microsoft.cdn/profiles/deploymentversions
microsoft.cdn/profiles/edgeextensiongroups
microsoft.cdn/profiles/endpoints
microsoft.cdn/profiles/endpoints/customdomains
microsoft.cdn/profiles/endpoints/origingroups
microsoft.cdn/profiles/endpoints/origins
microsoft.cdn/profiles/keygroups
microsoft.cdn/profiles/networkpolicies
microsoft.cdn/profiles/origingroups
microsoft.cdn/profiles/origingroups/origins
microsoft.cdn/profiles/policies
microsoft.cdn/profiles/rulesets
microsoft.cdn/profiles/rulesets/rules
microsoft.cdn/profiles/secrets
microsoft.cdn/profiles/securitypolicies
microsoft.cdn/profiles/targetgroups
microsoft.cdn/profiles/tunnelpolicies
microsoft.cdn/webagents/knowledgesources
microsoft.certificateregistration/certificateorders/certificates
microsoft.changesafety/changerecords/stageprogressions
microsoft.changesafety/changestates/stageprogressions
microsoft.changesafety/locations/operationstatuses
microsoft.changesafety/operations/versions
microsoft.changesafety/saferollouts/stages
microsoft.changesafety/saferollouts/steps
microsoft.changesafety/validators/versions
microsoft.chaos/locations/operationresults
microsoft.chaos/locations/operationstatuses
microsoft.chaos/locations/targettypes
microsoft.chaos/locations/workspaceoperationresults
microsoft.cleanroom/consortiumviews/contracts
microsoft.cleanroom/locations/operationstatuses
microsoft.cloudhealth/healthmodels/authenticationsettings
microsoft.cloudhealth/healthmodels/discoveryrules
microsoft.cloudhealth/healthmodels/entities
microsoft.cloudhealth/healthmodels/relationships
microsoft.cloudhealth/healthmodels/signaldefinitions
microsoft.cloudhealth/locations/operationstatuses
microsoft.cloudtest/locations/operations
microsoft.codesigning/codesigningaccounts/certificateprofiles
microsoft.codesigning/locations/operationstatuses
microsoft.cognitiveservices/accounts/capabilityhosts
microsoft.cognitiveservices/accounts/connections
microsoft.cognitiveservices/accounts/encryptionscopes
microsoft.cognitiveservices/accounts/managednetworks
microsoft.cognitiveservices/accounts/networksecurityperimeterassociationproxies
microsoft.cognitiveservices/accounts/privateendpointconnectionproxies
microsoft.cognitiveservices/accounts/privateendpointconnections
microsoft.cognitiveservices/accounts/privatelinkresources
microsoft.cognitiveservices/accounts/projects
microsoft.cognitiveservices/accounts/projects/applications
microsoft.cognitiveservices/accounts/projects/capabilityhosts
microsoft.cognitiveservices/accounts/projects/connections
microsoft.cognitiveservices/locations/checkskuavailability
microsoft.cognitiveservices/locations/commitmenttiers
microsoft.cognitiveservices/locations/deletevirtualnetworkorsubnets
microsoft.cognitiveservices/locations/managedcomputeusages
microsoft.cognitiveservices/locations/modelcapacities
microsoft.cognitiveservices/locations/models
microsoft.cognitiveservices/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.cognitiveservices/locations/operationresults
microsoft.cognitiveservices/locations/raicontentfilters
microsoft.cognitiveservices/locations/resourcegroups
microsoft.cognitiveservices/locations/resourcegroups/deletedaccounts
microsoft.cognitiveservices/locations/usages
microsoft.communication/communicationservices/eventgridfilters
microsoft.communication/communicationservices/smtpusernames
microsoft.communication/emailservices/domains
microsoft.communication/emailservices/domains/senderusernames
microsoft.communication/emailservices/domains/suppressionlists
microsoft.communication/emailservices/domains/suppressionlists/suppressionlistaddresses
microsoft.communication/locations/operationstatuses
microsoft.communication/locations/usages
microsoft.computebulkactions/locations/operations
microsoft.computebulkactions/locations/virtualmachinescanceloperations
microsoft.computebulkactions/locations/virtualmachinesexecutecreate
microsoft.computebulkactions/locations/virtualmachinesexecutedeallocate
microsoft.computebulkactions/locations/virtualmachinesexecutedelete
microsoft.computebulkactions/locations/virtualmachinesexecutehibernate
microsoft.computebulkactions/locations/virtualmachinesexecutestart
microsoft.computebulkactions/locations/virtualmachinesgetoperationerrors
microsoft.computebulkactions/locations/virtualmachinesgetoperationstatus
microsoft.computebulkactions/locations/virtualmachinessubmitdeallocate
microsoft.computebulkactions/locations/virtualmachinessubmithibernate
microsoft.computebulkactions/locations/virtualmachinessubmitstart
microsoft.compute/cloudservices/networkinterfaces
microsoft.compute/cloudservices/publicipaddresses
microsoft.compute/cloudservices/roleinstances/networkinterfaces
microsoft.compute/galleries/scripts
microsoft.compute/galleries/scripts/versions
microsoft.computelimit/locations/features
microsoft.computelimit/locations/guestsubscriptions
microsoft.computelimit/locations/operationresults
microsoft.computelimit/locations/sharedlimits
microsoft.computelimit/locations/vmfamilies
microsoft.compute/locations/artifactpublishers
microsoft.compute/locations/autoupgradableextensions
microsoft.compute/locations/capsoperations
microsoft.compute/locations/cloudserviceosfamilies
microsoft.compute/locations/cloudserviceosversions
microsoft.compute/locations/communitygalleries
microsoft.compute/locations/csoperations
microsoft.compute/locations/diagnosticoperations
microsoft.compute/locations/diagnosticruncommands
microsoft.compute/locations/diagnostics
microsoft.compute/locations/diskoperations
microsoft.compute/locations/edgezones
microsoft.compute/locations/edgezones/publishers
microsoft.compute/locations/edgezones/vmimages
microsoft.compute/locations/galleries
microsoft.compute/locations/loganalytics
microsoft.compute/locations/operations
microsoft.compute/locations/placementrecommendations
microsoft.compute/locations/placementscores
microsoft.compute/locations/publishers
microsoft.compute/locations/recommendations
microsoft.compute/locations/runcommands
microsoft.compute/locations/sharedgalleries
microsoft.compute/locations/sharedgallerysubscriptions
microsoft.compute/locations/spotevictionrates
microsoft.compute/locations/spotpricehistory
microsoft.compute/locations/tenantlevelsharedgallerysubscriptions
microsoft.compute/locations/usages
microsoft.compute/locations/virtualmachines
microsoft.compute/locations/virtualmachinesbulkcancel
microsoft.compute/locations/virtualmachinesbulkdeallocate
microsoft.compute/locations/virtualmachinesbulkdelete
microsoft.compute/locations/virtualmachinesbulkgetoperationstatus
microsoft.compute/locations/virtualmachinesbulkhibernate
microsoft.compute/locations/virtualmachinesbulkstart
microsoft.compute/locations/virtualmachinescalesets
microsoft.compute/locations/vmfamilyrecommendations
microsoft.compute/locations/vmsizerecommendations
microsoft.compute/locations/vmsizes
microsoft.compute/restorepointcollections/restorepoints
microsoft.compute/restorepointcollections/restorepoints/diskrestorepoints
microsoft.computeschedule/locations/notifications
microsoft.computeschedule/locations/operationstatuses
microsoft.computeschedule/locations/regionalnotifications
microsoft.computeschedule/locations/virtualmachinescanceloperations
microsoft.computeschedule/locations/virtualmachinesexecutecreate
microsoft.computeschedule/locations/virtualmachinesexecutedeallocate
microsoft.computeschedule/locations/virtualmachinesexecutedelete
microsoft.computeschedule/locations/virtualmachinesexecutehibernate
microsoft.computeschedule/locations/virtualmachinesexecutestart
microsoft.computeschedule/locations/virtualmachinesgetoperationerrors
microsoft.computeschedule/locations/virtualmachinesgetoperationstatus
microsoft.computeschedule/locations/virtualmachinessubmitdeallocate
microsoft.computeschedule/locations/virtualmachinessubmithibernate
microsoft.computeschedule/locations/virtualmachinessubmitstart
microsoft.compute/sharedvmimages/versions
microsoft.compute/virtualmachinescalesets/applications
microsoft.compute/virtualmachinescalesets/disks
microsoft.compute/virtualmachinescalesets/eventgridfilters
microsoft.compute/virtualmachinescalesets/networkinterfaces
microsoft.compute/virtualmachinescalesets/publicipaddresses
microsoft.compute/virtualmachinescalesets/virtualmachines/networkinterfaces
microsoft.compute/virtualmachines/diagnosticruncommands
microsoft.compute/virtualmachines/metricdefinitions
microsoft.compute/virtualmachines/runcommands
microsoft.compute/virtualmachines/vmapplications
microsoft.confidentialledger/locations/operations
microsoft.confidentialledger/locations/operationstatuses
microsoft.confluent/locations/operationstatuses
microsoft.confluent/organizations/access
microsoft.confluent/organizations/access/deleterolebinding
microsoft.confluent/organizations/apikeys
microsoft.confluent/organizations/environments
microsoft.confluent/organizations/environments/clusters
microsoft.confluent/organizations/environments/clusters/connectors
microsoft.confluent/organizations/environments/clusters/createapikey
microsoft.confluent/organizations/environments/clusters/topics
microsoft.confluent/organizations/environments/schemaregistryclusters
microsoft.confluent/organizations/listregions
microsoft.connectedcache/enterprisemcccustomers/enterprisemcccachenodes
microsoft.connectedcache/ispcustomers/ispcachenodes
microsoft.connectedcache/locations/operationstatuses
microsoft.connectedcredentials/locations/operationstatuses
microsoft.connectedopenstack/locations/operationstatuses
microsoft.connectedvehicle/locations/operationstatuses
microsoft.connectedvmwarevsphere/locations/operationstatuses
microsoft.connectedvmwarevsphere/vcenters/inventoryitems
microsoft.connectedvmwarevsphere/virtualmachines/extensions
microsoft.connectedvmwarevsphere/virtualmachines/guestagents
microsoft.connectedvmwarevsphere/virtualmachines/hybrididentitymetadata
microsoft.consumption/lots/contingencies
microsoft.containerinstance/locations/cachedimages
microsoft.containerinstance/locations/capabilities
microsoft.containerinstance/locations/createreservationset
microsoft.containerinstance/locations/deletevirtualnetworkorsubnets
microsoft.containerinstance/locations/ngroupsoperations
microsoft.containerinstance/locations/operationresults
microsoft.containerinstance/locations/operations
microsoft.containerinstance/locations/reservationsets
microsoft.containerinstance/locations/usages
microsoft.containerinstance/locations/validatedeletevirtualnetworkorsubnets
microsoft.containerregistry/locations/authorize
microsoft.containerregistry/locations/deletevirtualnetworkorsubnets
microsoft.containerregistry/locations/operationresults
microsoft.containerregistry/registries/agentpools
microsoft.containerregistry/registries/agentpools/listqueuestatus
microsoft.containerregistry/registries/agentpoolsoperationresults
microsoft.containerregistry/registries/cacherules
microsoft.containerregistry/registries/connectedregistries
microsoft.containerregistry/registries/connectedregistries/deactivate
microsoft.containerregistry/registries/connectedregistries/resync
microsoft.containerregistry/registries/credentialsets
microsoft.containerregistry/registries/eventgridfilters
microsoft.containerregistry/registries/exportpipelines
microsoft.containerregistry/registries/generatecredentials
microsoft.containerregistry/registries/importimage
microsoft.containerregistry/registries/importpipelines
microsoft.containerregistry/registries/listbuildsourceuploadurl
microsoft.containerregistry/registries/listcredentials
microsoft.containerregistry/registries/listpolicies
microsoft.containerregistry/registries/listusages
microsoft.containerregistry/registries/pipelineruns
microsoft.containerregistry/registries/privateendpointconnectionproxies
microsoft.containerregistry/registries/privateendpointconnectionproxies/validate
microsoft.containerregistry/registries/privateendpointconnections
microsoft.containerregistry/registries/privatelinkresources
microsoft.containerregistry/registries/regeneratecredential
microsoft.containerregistry/registries/replications
microsoft.containerregistry/registries/runs
microsoft.containerregistry/registries/runs/cancel
microsoft.containerregistry/registries/runs/listlogsasurl
microsoft.containerregistry/registries/schedulerun
microsoft.containerregistry/registries/scopemaps
microsoft.containerregistry/registries/taskruns
microsoft.containerregistry/registries/taskruns/listdetails
microsoft.containerregistry/registries/tasks
microsoft.containerregistry/registries/tasks/listdetails
microsoft.containerregistry/registries/tokens
microsoft.containerregistry/registries/updatepolicies
microsoft.containerregistry/registries/webhooks
microsoft.containerregistry/registries/webhooks/getcallbackconfig
microsoft.containerregistry/registries/webhooks/listevents
microsoft.containerregistry/registries/webhooks/ping
microsoft.containerservice/fleets/autoupgradeprofiles
microsoft.containerservice/fleets/clustermeshprofiles
microsoft.containerservice/fleets/gates
microsoft.containerservice/fleets/managednamespaces
microsoft.containerservice/fleets/members
microsoft.containerservice/fleets/updateruns
microsoft.containerservice/fleets/updatestrategies
microsoft.containerservice/locations/guardrailsversions
microsoft.containerservice/locations/kubernetesversions
microsoft.containerservice/locations/meshrevisionprofiles
microsoft.containerservice/locations/nodeimageversions
microsoft.containerservice/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.containerservice/locations/operationresults
microsoft.containerservice/locations/operations
microsoft.containerservice/locations/orchestrators
microsoft.containerservice/locations/osoptions
microsoft.containerservice/locations/safeguardsversions
microsoft.containerservice/locations/trustedaccessroles
microsoft.containerservice/locations/usages
microsoft.containerservice/locations/vmskus
microsoft.containerservice/managedclusters/eventgridfilters
microsoft.containerservice/managedclusters/managednamespaces
microsoft.containerservice/managedclusters/meshmemberships
microsoft.customproviders/locations/operationresults
microsoft.customproviders/locations/operationstatuses
microsoft.customproviders/resourceproviders/operationresults
microsoft.customproviders/resourceproviders/operationstatuses
microsoft.dashboard/dashboards/dashboarddefinitions
microsoft.dashboard/grafana/grafanadefinitions
microsoft.dashboard/grafana/integrationfabrics
microsoft.dashboard/grafana/managedprivateendpoints
microsoft.dashboard/grafana/privateendpointconnections
microsoft.dashboard/grafana/privatelinkresources
microsoft.dashboard/locations/checknameavailability
microsoft.dashboard/locations/operationstatuses
microsoft.databasewatcher/locations/operationstatuses
microsoft.databasewatcher/watchers/alertruleresources
microsoft.databasewatcher/watchers/healthvalidations
microsoft.databasewatcher/watchers/sharedprivatelinkresources
microsoft.databasewatcher/watchers/targets
microsoft.databoxedge/databoxedgedevices/checknameavailability
microsoft.databox/jobs/eventgridfilters
microsoft.databox/locations/availableskus
microsoft.databox/locations/checknameavailability
microsoft.databox/locations/operationresults
microsoft.databox/locations/regionconfiguration
microsoft.databox/locations/validateaddress
microsoft.databox/locations/validateinputs
microsoft.databricks/locations/getnetworkpolicies
microsoft.databricks/locations/operationstatuses
microsoft.databricks/workspaces/dbworkspaces
microsoft.databricks/workspaces/virtualnetworkpeerings
microsoft.datadog/locations/operationstatuses
microsoft.datadog/monitors/getdefaultkey
microsoft.datadog/monitors/listapikeys
microsoft.datadog/monitors/listhosts
microsoft.datadog/monitors/listlinkedresources
microsoft.datadog/monitors/listmonitoredresources
microsoft.datadog/monitors/managesreagentconnectors
microsoft.datadog/monitors/monitoredsubscriptions
microsoft.datadog/monitors/refreshsetpasswordlink
microsoft.datadog/monitors/setdefaultkey
microsoft.datadog/monitors/singlesignonconfigurations
microsoft.datadog/monitors/tagrules
microsoft.datafactory/factories/integrationruntimes
microsoft.datafactory/factories/privateendpointconnectionproxies
microsoft.datafactory/locations/configurefactoryrepo
microsoft.datafactory/locations/getfeaturevalue
microsoft.datalakeanalytics/accounts/datalakestoreaccounts
microsoft.datalakeanalytics/accounts/storageaccounts
microsoft.datalakeanalytics/accounts/storageaccounts/containers
microsoft.datalakeanalytics/accounts/storageaccounts/containers/listsastokens
microsoft.datalakeanalytics/accounts/transferanalyticsunits
microsoft.datalakeanalytics/accounts/transferecoanalyticsunits
microsoft.datalakeanalytics/locations/capability
microsoft.datalakeanalytics/locations/checknameavailability
microsoft.datalakeanalytics/locations/operationresults
microsoft.datalakeanalytics/locations/usages
microsoft.datalakestore/locations/capability
microsoft.datalakestore/locations/checknameavailability
microsoft.datalakestore/locations/deletevirtualnetworkorsubnets
microsoft.datalakestore/locations/operationresults
microsoft.datalakestore/locations/usages
microsoft.datamigration/locations/checknameavailability
microsoft.datamigration/locations/migrationserviceoperationresults
microsoft.datamigration/locations/operationresults
microsoft.datamigration/locations/operationstatuses
microsoft.datamigration/locations/operationtypes
microsoft.datamigration/locations/sqlmigrationserviceoperationresults
microsoft.datamigration/services/projects
microsoft.dataprotection/locations/checkfeaturesupport
microsoft.dataprotection/locations/checknameavailability
microsoft.dataprotection/locations/crossregionrestore
microsoft.dataprotection/locations/deletedvaults
microsoft.dataprotection/locations/fetchcrossregionrestorejob
microsoft.dataprotection/locations/fetchcrossregionrestorejobs
microsoft.dataprotection/locations/fetchsecondaryrecoverypoints
microsoft.dataprotection/locations/operationresults
microsoft.dataprotection/locations/operationstatus
microsoft.dataprotection/locations/validatecrossregionrestore
microsoft.datareplication/locations/operationresults
microsoft.datashare/accounts/shares
microsoft.datashare/accounts/shares/datasets
microsoft.datashare/accounts/shares/invitations
microsoft.datashare/accounts/shares/providersharesubscriptions
microsoft.datashare/accounts/shares/synchronizationsettings
microsoft.datashare/accounts/sharesubscriptions
microsoft.datashare/accounts/sharesubscriptions/consumersourcedatasets
microsoft.datashare/accounts/sharesubscriptions/datasetmappings
microsoft.datashare/accounts/sharesubscriptions/triggers
microsoft.datashare/locations/activateemail
microsoft.datashare/locations/consumerinvitations
microsoft.datashare/locations/operationresults
microsoft.datashare/locations/registeremail
microsoft.datashare/locations/rejectinvitation
microsoft.dbformariadb/locations/azureasyncoperation
microsoft.dbformariadb/locations/operationresults
microsoft.dbformariadb/locations/performancetiers
microsoft.dbformariadb/locations/privateendpointconnectionazureasyncoperation
microsoft.dbformariadb/locations/privateendpointconnectionoperationresults
microsoft.dbformariadb/locations/privateendpointconnectionproxyazureasyncoperation
microsoft.dbformariadb/locations/privateendpointconnectionproxyoperationresults
microsoft.dbformariadb/locations/recommendedactionsessionsazureasyncoperation
microsoft.dbformariadb/locations/recommendedactionsessionsoperationresults
microsoft.dbformariadb/locations/securityalertpoliciesazureasyncoperation
microsoft.dbformariadb/locations/securityalertpoliciesoperationresults
microsoft.dbformariadb/locations/serverkeyazureasyncoperation
microsoft.dbformariadb/locations/serverkeyoperationresults
microsoft.dbformariadb/servers/advisors
microsoft.dbformariadb/servers/keys
microsoft.dbformariadb/servers/privateendpointconnectionproxies
microsoft.dbformariadb/servers/privateendpointconnections
microsoft.dbformariadb/servers/privatelinkresources
microsoft.dbformariadb/servers/querytexts
microsoft.dbformariadb/servers/recoverableservers
microsoft.dbformariadb/servers/resetqueryperformanceinsightdata
microsoft.dbformariadb/servers/start
microsoft.dbformariadb/servers/stop
microsoft.dbformariadb/servers/topquerystatistics
microsoft.dbformariadb/servers/virtualnetworkrules
microsoft.dbformariadb/servers/waitstatistics
microsoft.dbformysql/locations/administratorazureasyncoperation
microsoft.dbformysql/locations/administratoroperationresults
microsoft.dbformysql/locations/azureasyncoperation
microsoft.dbformysql/locations/capabilities
microsoft.dbformysql/locations/capabilitysets
microsoft.dbformysql/locations/checknameavailability
microsoft.dbformysql/locations/checkvirtualnetworksubnetusage
microsoft.dbformysql/locations/listmigrations
microsoft.dbformysql/locations/operationprogress
microsoft.dbformysql/locations/operationresults
microsoft.dbformysql/locations/performancetiers
microsoft.dbformysql/locations/privateendpointconnectionazureasyncoperation
microsoft.dbformysql/locations/privateendpointconnectionoperationresults
microsoft.dbformysql/locations/privateendpointconnectionproxyazureasyncoperation
microsoft.dbformysql/locations/privateendpointconnectionproxyoperationresults
microsoft.dbformysql/locations/recommendedactionsessionsazureasyncoperation
microsoft.dbformysql/locations/recommendedactionsessionsoperationresults
microsoft.dbformysql/locations/securityalertpoliciesazureasyncoperation
microsoft.dbformysql/locations/securityalertpoliciesoperationresults
microsoft.dbformysql/locations/serverkeyazureasyncoperation
microsoft.dbformysql/locations/serverkeyoperationresults
microsoft.dbformysql/locations/updatemigration
microsoft.dbformysql/locations/usages
microsoft.dbformysql/servers/advisors
microsoft.dbformysql/servers/keys
microsoft.dbformysql/servers/privateendpointconnectionproxies
microsoft.dbformysql/servers/privateendpointconnections
microsoft.dbformysql/servers/privatelinkresources
microsoft.dbformysql/servers/querytexts
microsoft.dbformysql/servers/recoverableservers
microsoft.dbformysql/servers/resetqueryperformanceinsightdata
microsoft.dbformysql/servers/topquerystatistics
microsoft.dbformysql/servers/upgrade
microsoft.dbformysql/servers/virtualnetworkrules
microsoft.dbformysql/servers/waitstatistics
microsoft.dbforpostgresql/flexibleservers/migrations
microsoft.dbforpostgresql/locations/administratorazureasyncoperation
microsoft.dbforpostgresql/locations/administratoroperationresults
microsoft.dbforpostgresql/locations/azureasyncoperation
microsoft.dbforpostgresql/locations/capabilities
microsoft.dbforpostgresql/locations/checknameavailability
microsoft.dbforpostgresql/locations/checkvirtualnetworksubnetusage
microsoft.dbforpostgresql/locations/getautomigrationfreeslots
microsoft.dbforpostgresql/locations/getcachedservername
microsoft.dbforpostgresql/locations/getlatestautomigrationschedule
microsoft.dbforpostgresql/locations/operationresults
microsoft.dbforpostgresql/locations/performancetiers
microsoft.dbforpostgresql/locations/privateendpointconnectionazureasyncoperation
microsoft.dbforpostgresql/locations/privateendpointconnectionoperationresults
microsoft.dbforpostgresql/locations/privateendpointconnectionproxyazureasyncoperation
microsoft.dbforpostgresql/locations/privateendpointconnectionproxyoperationresults
microsoft.dbforpostgresql/locations/recommendedactionsessionsazureasyncoperation
microsoft.dbforpostgresql/locations/recommendedactionsessionsoperationresults
microsoft.dbforpostgresql/locations/resourcetype
microsoft.dbforpostgresql/locations/securityalertpoliciesazureasyncoperation
microsoft.dbforpostgresql/locations/securityalertpoliciesoperationresults
microsoft.dbforpostgresql/locations/serverkeyazureasyncoperation
microsoft.dbforpostgresql/locations/serverkeyoperationresults
microsoft.dbforpostgresql/locations/updateautomigrationschedule
microsoft.dbforpostgresql/servers/advisors
microsoft.dbforpostgresql/servers/keys
microsoft.dbforpostgresql/servers/privateendpointconnectionproxies
microsoft.dbforpostgresql/servers/privateendpointconnections
microsoft.dbforpostgresql/servers/privatelinkresources
microsoft.dbforpostgresql/servers/querytexts
microsoft.dbforpostgresql/servers/recoverableservers
microsoft.dbforpostgresql/servers/resetqueryperformanceinsightdata
microsoft.dbforpostgresql/servers/topquerystatistics
microsoft.dbforpostgresql/servers/virtualnetworkrules
microsoft.dbforpostgresql/servers/waitstatistics
microsoft.dependencymap/locations/operationstatuses
microsoft.dependencymap/maps/discoverysources
microsoft.desktopvirtualization/applicationgroups/applications
microsoft.desktopvirtualization/applicationgroups/desktops
microsoft.desktopvirtualization/applicationgroups/startmenuitems
microsoft.desktopvirtualization/hostpools/msixpackages
microsoft.desktopvirtualization/hostpools/sessionhosts
microsoft.desktopvirtualization/hostpools/sessionhosts/usersessions
microsoft.desktopvirtualization/hostpools/usersessions
microsoft.desktopvirtualization/repositoryfolders/repositoryintegrations
microsoft.devcenter/devcenters/attachednetworks
microsoft.devcenter/devcenters/catalogs
microsoft.devcenter/devcenters/catalogs/devboxdefinitions
microsoft.devcenter/devcenters/catalogs/environmentdefinitions
microsoft.devcenter/devcenters/catalogs/imagedefinitions
microsoft.devcenter/devcenters/catalogs/imagedefinitions/builds
microsoft.devcenter/devcenters/catalogs/tasks
microsoft.devcenter/devcenters/devboxdefinitions
microsoft.devcenter/devcenters/environmenttypes
microsoft.devcenter/devcenters/galleries
microsoft.devcenter/devcenters/galleries/images
microsoft.devcenter/devcenters/galleries/images/versions
microsoft.devcenter/devcenters/images
microsoft.devcenter/devcenters/projectpolicies
microsoft.devcenter/locations/operationstatuses
microsoft.devcenter/locations/usages
microsoft.devcenter/networkconnections/healthchecks
microsoft.devcenter/networkconnections/outboundnetworkdependenciesendpoints
microsoft.devcenter/projects/allowedenvironmenttypes
microsoft.devcenter/projects/attachednetworks
microsoft.devcenter/projects/catalogs
microsoft.devcenter/projects/catalogs/environmentdefinitions
microsoft.devcenter/projects/catalogs/imagedefinitions
microsoft.devcenter/projects/catalogs/imagedefinitions/builds
microsoft.devcenter/projects/devboxdefinitions
microsoft.devcenter/projects/environmenttypes
microsoft.devcenter/projects/images
microsoft.devcenter/projects/images/versions
microsoft.devcenter/projects/listskus
microsoft.devcenter/projects/pools
microsoft.devcenter/projects/pools/schedules
microsoft.devhub/locations/adooauth
microsoft.devhub/locations/generatepreviewartifacts
microsoft.devhub/locations/githuboauth
microsoft.devhub/templates/versions
microsoft.devhub/templates/versions/generate
microsoft.deviceregistry/locations/operationstatuses
microsoft.deviceregistry/namespaces/assets
microsoft.deviceregistry/namespaces/credentials
microsoft.deviceregistry/namespaces/credentials/policies
microsoft.deviceregistry/namespaces/devices
microsoft.deviceregistry/namespaces/discoveredassets
microsoft.deviceregistry/namespaces/discovereddevices
microsoft.deviceregistry/schemaregistries/schemas
microsoft.deviceregistry/schemaregistries/schemas/schemaversions
microsoft.devices/iothubs/eventgridfilters
microsoft.devices/iothubs/failover
microsoft.devices/iothubs/securitysettings
microsoft.devices/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.devices/locations/operationresults
microsoft.devices/locations/provisioningserviceoperationresults
microsoft.deviceupdate/accounts/instances
microsoft.deviceupdate/accounts/privateendpointconnectionproxies
microsoft.deviceupdate/accounts/privateendpointconnections
microsoft.deviceupdate/accounts/privatelinkresources
microsoft.deviceupdate/locations/operationstatuses
microsoft.devopsinfrastructure/images/versions
microsoft.devopsinfrastructure/locations/operationstatuses
microsoft.devopsinfrastructure/locations/skus
microsoft.devopsinfrastructure/locations/usages
microsoft.devopsinfrastructure/pools/resources
microsoft.devtestlab/labs/environments
microsoft.devtestlab/labs/servicerunners
microsoft.devtestlab/labs/virtualmachines
microsoft.devtestlab/locations/operations
microsoft.digitaltwins/digitaltwinsinstances/endpoints
microsoft.digitaltwins/digitaltwinsinstances/operationresults
microsoft.digitaltwins/digitaltwinsinstances/timeseriesdatabaseconnections
microsoft.digitaltwins/locations/checknameavailability
microsoft.digitaltwins/locations/operationresults
microsoft.digitaltwins/locations/operationsstatuses
microsoft.discovery/locations/operationstatuses
microsoft.documentdb/databaseaccounts/encryptionscopes
microsoft.documentdb/locations/checkmongoclusternameavailability
microsoft.documentdb/locations/deletevirtualnetworkorsubnets
microsoft.documentdb/locations/mongoclusterazureasyncoperation
microsoft.documentdb/locations/mongoclusteroperationresults
microsoft.documentdb/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.documentdb/locations/operationresults
microsoft.documentdb/locations/operationsstatus
microsoft.documentdb/locations/restorabledatabaseaccounts
microsoft.documentdb/locations/softdeleteddatabaseaccounts
microsoft.documentdb/throughputpools/throughputpoolaccounts
microsoft.domainregistration/domains/domainownershipidentifiers
microsoft.durabletask/locations/operationstatuses
microsoft.durabletask/schedulers/privateendpointconnections
microsoft.durabletask/schedulers/privatelinkresources
microsoft.durabletask/schedulers/retentionpolicies
microsoft.durabletask/schedulers/taskhubs
microsoft.durabletask/schedulers/transparentdataencryptions
microsoft.easm/workspaces/labels
microsoft.easm/workspaces/tasks
microsoft.edge/configtemplates/configtemplatemetadatas
microsoft.edge/configtemplates/versions
microsoft.edge/configtemplates/versions/configtemplateschemas
microsoft.edge/configurations/arcgatewayconfigurations
microsoft.edge/configurations/connectivityconfigurations
microsoft.edge/configurations/dynamicconfigurations
microsoft.edge/configurations/dynamicconfigurations/versions
microsoft.edge/configurations/networkconfigurations
microsoft.edge/configurations/provisioningconfigurations
microsoft.edge/configurations/securityconfigurations
microsoft.edge/configurations/timeserverconfigurations
microsoft.edge/contexts/eventgridfilters
microsoft.edge/contexts/sitereferences
microsoft.edge/contexts/workflows
microsoft.edge/contexts/workflows/versions
microsoft.edge/contexts/workflows/versions/executions
microsoft.edge/locations/operationstatuses
microsoft.edgemarketplace/locations/operationstatuses
microsoft.edgeorder/locations/hcicatalog
microsoft.edgeorder/locations/hcicatalog/platforms
microsoft.edgeorder/locations/hcicatalog/projects
microsoft.edgeorder/locations/hcicatalog/vendors
microsoft.edgeorder/locations/hciflightcatalog
microsoft.edgeorder/locations/hciflightcatalog/platforms
microsoft.edgeorder/locations/hciflightcatalog/projects
microsoft.edgeorder/locations/hciflightcatalog/vendors
microsoft.edgeorder/locations/operationresults
microsoft.edgeorder/locations/orders
microsoft.edgeorder/locations/validateinputs
microsoft.edge/schemas/dynamicschemas
microsoft.edge/schemas/dynamicschemas/versions
microsoft.edge/schemas/versions
microsoft.edge/solutiontemplates/versions
microsoft.edge/solutiontemplates/versions/solutionschemas
microsoft.edge/targets/solutions
microsoft.edge/targets/solutions/instances
microsoft.edge/targets/solutions/instances/histories
microsoft.edge/targets/solutions/versions
microsoft.edge/workflows/versions
microsoft.edge/workflows/versions/executions
microsoft.elastic/locations/operationstatuses
microsoft.elastic/monitors/monitoredsubscriptions
microsoft.elastic/monitors/openaiintegrations
microsoft.elastic/monitors/tagrules
microsoft.elasticsan/elasticsans/volumegroups
microsoft.elasticsan/locations/asyncoperations
microsoft.eventgrid/domains/topics
microsoft.eventgrid/locations/eventsubscriptions
microsoft.eventgrid/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.eventgrid/locations/operationresults
microsoft.eventgrid/locations/operationsstatus
microsoft.eventgrid/locations/topictypes
microsoft.eventgrid/partnernamespaces/channels
microsoft.eventgrid/partnernamespaces/eventchannels
microsoft.eventgrid/partnertopics/eventsubscriptions
microsoft.eventgrid/systemtopics/eventsubscriptions
microsoft.eventhub/locations/clusteroperationresults
microsoft.eventhub/locations/deletevirtualnetworkorsubnets
microsoft.eventhub/locations/namespaceoperationresults
microsoft.eventhub/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.eventhub/locations/operationstatus
microsoft.eventhub/namespaces/applicationgroups
microsoft.eventhub/namespaces/authorizationrules
microsoft.eventhub/namespaces/disasterrecoveryconfigs
microsoft.eventhub/namespaces/disasterrecoveryconfigs/checknameavailability
microsoft.eventhub/namespaces/eventhubs
microsoft.eventhub/namespaces/eventhubs/authorizationrules
microsoft.eventhub/namespaces/eventhubs/consumergroups
microsoft.eventhub/namespaces/hoboconfigurations
microsoft.eventhub/namespaces/networkrulesets
microsoft.eventhub/namespaces/networksecurityperimeterassociationproxies
microsoft.eventhub/namespaces/networksecurityperimeterconfigurations
microsoft.eventhub/namespaces/privateendpointconnectionproxies
microsoft.eventhub/namespaces/privateendpointconnections
microsoft.extendedlocation/customlocations/enabledresourcetypes
microsoft.extendedlocation/customlocations/internalmetadata
microsoft.extendedlocation/customlocations/resourcesyncrules
microsoft.extendedlocation/locations/operationresults
microsoft.extendedlocation/locations/operationsstatus
microsoft.fabric/locations/checknameavailability
microsoft.fabric/locations/operationresults
microsoft.fabric/locations/operationstatuses
microsoft.fabric/locations/usages
microsoft.fabric/privatelinkservicesforfabric/operationresults
microsoft.fabric/privatelinkservicesforfabric/operationstatuses
microsoft.fileshares/fileshares/filesharesnapshots
microsoft.fileshares/locations/checknameavailability
microsoft.fileshares/locations/getlimits
microsoft.fileshares/locations/getprovisioningrecommendation
microsoft.fileshares/locations/getusagedata
microsoft.fileshares/locations/partitions
microsoft.fileshares/locations/partitions/operationresults
microsoft.fileshares/locations/partitions/operations
microsoft.fluidrelay/fluidrelayservers/fluidrelaycontainers
microsoft.fluidrelay/fluidrelayservers/privateendpointconnections
microsoft.fluidrelay/fluidrelayservers/privatelinkresources
microsoft.fluidrelay/locations/operationstatuses
microsoft.gcpconnector/locations/operationstatuses
microsoft.genome/locations/operationstatuses
microsoft.graphservices/locations/operationstatuses
microsoft.hanaonazure/locations/operations
microsoft.hanaonazure/locations/operationsstatus
microsoft.hardwaresecuritymodules/locations/cloudhsmoperationresults
microsoft.hdinsight/clusters/applications
microsoft.hdinsight/clusters/operationresults
microsoft.hdinsight/locations/azureasyncoperations
microsoft.hdinsight/locations/billingspecs
microsoft.hdinsight/locations/capabilities
microsoft.hdinsight/locations/checknameavailability
microsoft.hdinsight/locations/operationresults
microsoft.hdinsight/locations/operationstatuses
microsoft.hdinsight/locations/usages
microsoft.hdinsight/locations/validatecreaterequest
microsoft.healthbot/locations/operationstatuses
microsoft.healthcareapis/locations/operationresults
microsoft.healthcareapis/services/privateendpointconnectionproxies
microsoft.healthcareapis/services/privateendpointconnections
microsoft.healthcareapis/services/privatelinkresources
microsoft.healthcareapis/workspaces/dicomservices
microsoft.healthcareapis/workspaces/eventgridfilters
microsoft.healthcareapis/workspaces/fhirservices
microsoft.healthcareapis/workspaces/iotconnectors
microsoft.healthcareapis/workspaces/iotconnectors/fhirdestinations
microsoft.healthcareapis/workspaces/privateendpointconnectionproxies
microsoft.healthcareapis/workspaces/privateendpointconnections
microsoft.healthcareapis/workspaces/privatelinkresources
microsoft.healthdataaiservices/deidservices/privateendpointconnections
microsoft.healthdataaiservices/deidservices/privatelinkresources
microsoft.healthdataaiservices/locations/operationstatuses
microsoft.horizondb/locations/azureasyncoperation
microsoft.horizondb/locations/operationresults
microsoft.hybridcompute/locations/notifyextension
microsoft.hybridcompute/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.hybridcompute/locations/notifyruncommand
microsoft.hybridcompute/locations/operationresults
microsoft.hybridcompute/locations/operationstatus
microsoft.hybridcompute/locations/privatelinkscopes
microsoft.hybridcompute/locations/publishers
microsoft.hybridcompute/locations/publishers/extensiontypes
microsoft.hybridcompute/locations/publishers/extensiontypes/versions
microsoft.hybridcompute/locations/updatecenteroperationresults
microsoft.hybridcompute/machines/applications
microsoft.hybridcompute/machines/assesspatches
microsoft.hybridcompute/machines/extensions
microsoft.hybridcompute/machines/hybrididentitymetadata
microsoft.hybridcompute/machines/installpatches
microsoft.hybridcompute/machines/licenseprofiles
microsoft.hybridcompute/machines/privatelinkscopes
microsoft.hybridcompute/machines/runcommands
microsoft.hybridcompute/ostype/agentversions
microsoft.hybridcompute/ostype/agentversions/latest
microsoft.hybridcompute/privatelinkscopes/networksecurityperimeterassociationproxies
microsoft.hybridcompute/privatelinkscopes/networksecurityperimeterconfigurations
microsoft.hybridcompute/privatelinkscopes/privateendpointconnectionproxies
microsoft.hybridcompute/privatelinkscopes/privateendpointconnections
microsoft.hybridconnectivity/locations/operationstatuses
microsoft.hybridcontainerservice/locations/operationstatuses
microsoft.hybridcontainerservice/provisionedclusters/agentpools
microsoft.hybridcontainerservice/provisionedclusters/hybrididentitymetadata
microsoft.hybridcontainerservice/provisionedclusters/upgradeprofiles
microsoft.hybridnetwork/locations/operationstatuses
microsoft.hybridnetwork/networkfunctions/components
microsoft.hybridnetwork/proxypublishers/configurationgroupschemas
microsoft.hybridnetwork/proxypublishers/networkfunctiondefinitiongroups
microsoft.hybridnetwork/proxypublishers/networkfunctiondefinitiongroups/networkfunctiondefinitionversions
microsoft.hybridnetwork/proxypublishers/networkservicedesigngroups
microsoft.hybridnetwork/proxypublishers/networkservicedesigngroups/networkservicedesignversions
microsoft.hybridnetwork/publishers/artifactstores
microsoft.hybridnetwork/publishers/artifactstores/artifactmanifests
microsoft.hybridnetwork/publishers/artifactstores/artifacts
microsoft.hybridnetwork/publishers/artifactstores/artifactversions
microsoft.hybridnetwork/publishers/configurationgroupschemas
microsoft.hybridnetwork/publishers/networkfunctiondefinitiongroups
microsoft.hybridnetwork/publishers/networkfunctiondefinitiongroups/networkfunctiondefinitionversions
microsoft.hybridnetwork/publishers/networkservicedesigngroups
microsoft.hybridnetwork/publishers/networkservicedesigngroups/networkservicedesignversions
microsoft.impact/workloadimpacts/insights
microsoft.insights/actiongroups/networksecurityperimeterassociationproxies
microsoft.insights/actiongroups/networksecurityperimeterconfigurations
microsoft.insights/components/aggregate
microsoft.insights/components/analyticsitems
microsoft.insights/components/annotations
microsoft.insights/components/api
microsoft.insights/components/apikeys
microsoft.insights/components/currentbillingfeatures
microsoft.insights/components/defaultworkitemconfig
microsoft.insights/components/events
microsoft.insights/components/exportconfiguration
microsoft.insights/components/extendqueries
microsoft.insights/components/favorites
microsoft.insights/components/featurecapabilities
microsoft.insights/components/getavailablebillingfeatures
microsoft.insights/components/linkedstorageaccounts
microsoft.insights/components/metadata
microsoft.insights/components/metricdefinitions
microsoft.insights/components/metrics
microsoft.insights/components/move
microsoft.insights/components/myanalyticsitems
microsoft.insights/components/myfavorites
microsoft.insights/components/operations
microsoft.insights/components/pricingplans
microsoft.insights/components/proactivedetectionconfigs
microsoft.insights/components/purge
microsoft.insights/components/query
microsoft.insights/components/quotastatus
microsoft.insights/components/syntheticmonitorlocations
microsoft.insights/components/webtests
microsoft.insights/components/workitemconfigs
microsoft.insights/datacollectionendpoints/networksecurityperimeterassociationproxies
microsoft.insights/datacollectionendpoints/networksecurityperimeterconfigurations
microsoft.insights/datacollectionendpoints/scopedprivatelinkproxies
microsoft.insights/locations/fetchdatacollectionruleconfigurationmetadata
microsoft.insights/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.insights/locations/operationresults
microsoft.insights/privatelinkscopes/privateendpointconnectionproxies
microsoft.insights/privatelinkscopes/privateendpointconnections
microsoft.insights/privatelinkscopes/scopedresources
microsoft.insights/scheduledqueryrules/networksecurityperimeterassociationproxies
microsoft.insights/scheduledqueryrules/networksecurityperimeterconfigurations
microsoft.insights/webtests/gettestresultfile
microsoft.integrationspaces/locations/operationstatuses
microsoft.integrationspaces/spaces/applications
microsoft.integrationspaces/spaces/applications/businessprocesses
microsoft.integrationspaces/spaces/applications/businessprocesses/versions
microsoft.integrationspaces/spaces/applications/resources
microsoft.integrationspaces/spaces/infrastructureresources
microsoft.iotcentral/locations/operationresults
microsoft.iotfirmwaredefense/locations/operationstatuses
microsoft.iotfirmwaredefense/workspaces/firmwares
microsoft.iotfirmwaredefense/workspaces/firmwares/binaryhardeningresults
microsoft.iotfirmwaredefense/workspaces/firmwares/commonvulnerabilitiesandexposures
microsoft.iotfirmwaredefense/workspaces/firmwares/cryptocertificates
microsoft.iotfirmwaredefense/workspaces/firmwares/cryptokeys
microsoft.iotfirmwaredefense/workspaces/firmwares/cves
microsoft.iotfirmwaredefense/workspaces/firmwares/passwordhashes
microsoft.iotfirmwaredefense/workspaces/firmwares/sbomcomponents
microsoft.iotfirmwaredefense/workspaces/firmwares/summaries
microsoft.iotfirmwaredefense/workspaces/usagemetrics
microsoft.iotoperationsdataprocessor/instances/datasets
microsoft.iotoperationsdataprocessor/instances/pipelines
microsoft.iotoperationsdataprocessor/locations/operationstatuses
microsoft.iotoperations/instances/akriconnectortemplates
microsoft.iotoperations/instances/akriconnectortemplates/connectors
microsoft.iotoperations/instances/akriservices
microsoft.iotoperations/instances/brokers
microsoft.iotoperations/instances/brokers/authentications
microsoft.iotoperations/instances/brokers/authorizations
microsoft.iotoperations/instances/brokers/listeners
microsoft.iotoperations/instances/dataflowendpoints
microsoft.iotoperations/instances/dataflowprofiles
microsoft.iotoperations/instances/dataflowprofiles/dataflowgraphs
microsoft.iotoperations/instances/dataflowprofiles/dataflows
microsoft.iotoperations/instances/registryendpoints
microsoft.iotoperations/locations/operationstatuses
microsoft.iotsecurity/locations/devicegroups
microsoft.iotsecurity/locations/devicegroups/alerts
microsoft.iotsecurity/locations/devicegroups/alerts/learn
microsoft.iotsecurity/locations/devicegroups/alerts/pcaps
microsoft.iotsecurity/locations/devicegroups/alerts/violations
microsoft.iotsecurity/locations/devicegroups/devices
microsoft.iotsecurity/locations/devicegroups/recommendations
microsoft.iotsecurity/locations/devicegroups/vulnerabilities
microsoft.iotsecurity/locations/endpoints
microsoft.iotsecurity/locations/sites
microsoft.iotsecurity/locations/sites/inventorynetworks
microsoft.iotsecurity/locations/sites/sensors
microsoft.keyvault/locations/deletedmanagedhsms
microsoft.keyvault/locations/deletedvaults
microsoft.keyvault/locations/deletevirtualnetworkorsubnets
microsoft.keyvault/locations/managedhsmoperationresults
microsoft.keyvault/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.keyvault/locations/operationresults
microsoft.keyvault/managedhsms/keys
microsoft.keyvault/managedhsms/keys/versions
microsoft.keyvault/vaults/accesspolicies
microsoft.keyvault/vaults/eventgridfilters
microsoft.keyvault/vaults/keys
microsoft.keyvault/vaults/keys/versions
microsoft.keyvault/vaults/secrets
microsoft.kubernetesconfiguration/locations/extensiontypes
microsoft.kubernetesconfiguration/locations/extensiontypes/versions
microsoft.kubernetesconfiguration/privatelinkscopes/privateendpointconnectionproxies
microsoft.kubernetesconfiguration/privatelinkscopes/privateendpointconnections
microsoft.kubernetes/locations/operationstatuses
microsoft.kubernetesruntime/locations/operationstatuses
microsoft.kusto/clusters/attacheddatabaseconfigurations
microsoft.kusto/clusters/databases
microsoft.kusto/clusters/databases/dataconnections
microsoft.kusto/clusters/databases/eventhubconnections
microsoft.kusto/clusters/databases/principalassignments
microsoft.kusto/clusters/databases/scripts
microsoft.kusto/clusters/managedprivateendpoints
microsoft.kusto/clusters/principalassignments
microsoft.kusto/clusters/sandboxcustomimages
microsoft.kusto/locations/checknameavailability
microsoft.kusto/locations/operationresults
microsoft.kusto/locations/skus
microsoft.labservices/locations/operationresults
microsoft.labservices/locations/operations
microsoft.labservices/locations/usages
microsoft.loadtestservice/loadtests/limits
microsoft.loadtestservice/loadtests/outboundnetworkdependenciesendpoints
microsoft.loadtestservice/locations/operationstatuses
microsoft.loadtestservice/locations/playwrightquotas
microsoft.loadtestservice/locations/quotas
microsoft.loadtestservice/playwrightworkspaces/quotas
microsoft.logic/automationprojects/applications
microsoft.logic/integrationserviceenvironments/managedapis
microsoft.logic/locations/generatecopilotresponse
microsoft.logic/locations/validateworkflowexport
microsoft.logic/locations/workflowexport
microsoft.logic/locations/workflows
microsoft.machinelearningservices/locations/availablequota
microsoft.machinelearningservices/locations/availablequota/default
microsoft.machinelearningservices/locations/computeoperationsstatus
microsoft.machinelearningservices/locations/instancetypeseries
microsoft.machinelearningservices/locations/mfeoperationresults
microsoft.machinelearningservices/locations/mfeoperationsstatus
microsoft.machinelearningservices/locations/quotaandusage
microsoft.machinelearningservices/locations/quotas
microsoft.machinelearningservices/locations/registryoperationsstatus
microsoft.machinelearningservices/locations/updatequotas
microsoft.machinelearningservices/locations/usages
microsoft.machinelearningservices/locations/virtualclusteroperations
microsoft.machinelearningservices/locations/vmsizes
microsoft.machinelearningservices/locations/workspaceoperationsstatus
microsoft.machinelearningservices/registries/codes
microsoft.machinelearningservices/registries/codes/versions
microsoft.machinelearningservices/registries/components
microsoft.machinelearningservices/registries/components/versions
microsoft.machinelearningservices/registries/data
microsoft.machinelearningservices/registries/datareferences
microsoft.machinelearningservices/registries/datareferences/versions
microsoft.machinelearningservices/registries/data/versions
microsoft.machinelearningservices/registries/environments
microsoft.machinelearningservices/registries/environments/versions
microsoft.machinelearningservices/registries/models
microsoft.machinelearningservices/registries/models/versions
microsoft.machinelearningservices/workspaces/batchendpoints
microsoft.machinelearningservices/workspaces/batchendpoints/deployments
microsoft.machinelearningservices/workspaces/capabilityhosts
microsoft.machinelearningservices/workspaces/codes
microsoft.machinelearningservices/workspaces/codes/versions
microsoft.machinelearningservices/workspaces/components
microsoft.machinelearningservices/workspaces/components/versions
microsoft.machinelearningservices/workspaces/computes
microsoft.machinelearningservices/workspaces/data
microsoft.machinelearningservices/workspaces/datasets
microsoft.machinelearningservices/workspaces/datastores
microsoft.machinelearningservices/workspaces/data/versions
microsoft.machinelearningservices/workspaces/endpoints
microsoft.machinelearningservices/workspaces/environments
microsoft.machinelearningservices/workspaces/environments/versions
microsoft.machinelearningservices/workspaces/eventgridfilters
microsoft.machinelearningservices/workspaces/featuresets
microsoft.machinelearningservices/workspaces/featuresets/versions
microsoft.machinelearningservices/workspaces/featurestoreentities
microsoft.machinelearningservices/workspaces/featurestoreentities/versions
microsoft.machinelearningservices/workspaces/inferencepools
microsoft.machinelearningservices/workspaces/inferencepools/endpoints
microsoft.machinelearningservices/workspaces/inferencepools/groups
microsoft.machinelearningservices/workspaces/jobs
microsoft.machinelearningservices/workspaces/labelingjobs
microsoft.machinelearningservices/workspaces/linkedservices
microsoft.machinelearningservices/workspaces/managednetworks
microsoft.machinelearningservices/workspaces/marketplacesubscriptions
microsoft.machinelearningservices/workspaces/models
microsoft.machinelearningservices/workspaces/models/versions
microsoft.machinelearningservices/workspaces/onlineendpoints
microsoft.machinelearningservices/workspaces/onlineendpoints/deployments
microsoft.machinelearningservices/workspaces/onlineendpoints/deployments/skus
microsoft.machinelearningservices/workspaces/schedules
microsoft.machinelearningservices/workspaces/serverlessendpoints
microsoft.machinelearningservices/workspaces/services
microsoft.maintenance/maintenanceconfigurations/eventgridfilters
microsoft.managedidentity/userassignedidentities/federatedidentitycredentials
microsoft.managednetworkfabric/l3isolationdomains/externalnetworks
microsoft.managednetworkfabric/l3isolationdomains/internalnetworks
microsoft.managednetworkfabric/locations/operationstatuses
microsoft.managednetworkfabric/networkbootstrapdevices/networkbootstrapinterfaces
microsoft.managednetworkfabric/networkdevices/networkinterfaces
microsoft.managednetworkfabric/networkfabrics/networktonetworkinterconnects
microsoft.managedops/locations/operationstatuses
microsoft.management/managementgroups/settings
microsoft.management/operationresults/asyncoperation
microsoft.maps/accounts/eventgridfilters
microsoft.maps/accounts/privateendpointconnectionproxies
microsoft.maps/accounts/privateendpointconnections
microsoft.maps/locations/operationresults
microsoft.maps/locations/operationstatuses
microsoft.marketplace/locations/edgezones
microsoft.marketplace/locations/edgezones/products
microsoft.marketplace/privatestores/adminrequestapprovals
microsoft.marketplace/privatestores/anyexistingoffersinthecollections
microsoft.marketplace/privatestores/billingaccounts
microsoft.marketplace/privatestores/bulkcollectionsaction
microsoft.marketplace/privatestores/collections
microsoft.marketplace/privatestores/collections/approveallitems
microsoft.marketplace/privatestores/collections/disableapproveallitems
microsoft.marketplace/privatestores/collections/mapofferstocontexts
microsoft.marketplace/privatestores/collections/offers
microsoft.marketplace/privatestores/collections/offers/contextsview
microsoft.marketplace/privatestores/collections/offers/upsertofferwithmulticontext
microsoft.marketplace/privatestores/collections/queryrules
microsoft.marketplace/privatestores/collections/setrules
microsoft.marketplace/privatestores/collectionstosubscriptionsmapping
microsoft.marketplace/privatestores/collections/transferoffers
microsoft.marketplace/privatestores/fetchallsubscriptionsintenant
microsoft.marketplace/privatestores/listnewplansnotifications
microsoft.marketplace/privatestores/liststopselloffersplansnotifications
microsoft.marketplace/privatestores/listsubscriptionscontext
microsoft.marketplace/privatestores/offers
microsoft.marketplace/privatestores/offers/acknowledgenotification
microsoft.marketplace/privatestores/queryapprovedplans
microsoft.marketplace/privatestores/querynotificationsstate
microsoft.marketplace/privatestores/queryoffers
microsoft.marketplace/privatestores/queryuseroffers
microsoft.marketplace/privatestores/queryuserrules
microsoft.marketplace/privatestores/requestapprovals
microsoft.marketplace/privatestores/requestapprovals/query
microsoft.marketplace/privatestores/requestapprovals/withdrawplan
microsoft.marketplace/products/checkuserhasreview
microsoft.marketplace/products/reviews
microsoft.marketplace/products/reviews/comments
microsoft.marketplace/products/reviews/helpful
microsoft.marketplace/products/usermetadata
microsoft.marketplace/publishers/offers
microsoft.marketplace/publishers/offers/amendments
microsoft.migrate/assessmentprojects/assessments
microsoft.migrate/locations/operationstatuses
microsoft.migrate/locations/rmsoperationresults
microsoft.migrate/migrateprojects/generatewaveplan
microsoft.migrate/migrateprojects/migrationentities
microsoft.migrate/migrateprojects/migrationentitygroups
microsoft.migrate/migrateprojects/refreshentities
microsoft.migrate/migrateprojects/reports
microsoft.migrate/migrateprojects/tasks
microsoft.migrate/migrateprojects/tasksummary
microsoft.migrate/migrateprojects/waveoperations
microsoft.migrate/migrateprojects/waves
microsoft.migrate/migrateprojects/workloadoperations
microsoft.mission/locations/operationstatuses
microsoft.monitor/accounts/issues
microsoft.monitor/locations/locationoperationstatuses
microsoft.monitor/locations/operationresults
microsoft.monitor/locations/operationstatuses
microsoft.mysqldiscovery/locations/operationstatuses
microsoft.mysqldiscovery/mysqlsites/agents
microsoft.mysqldiscovery/mysqlsites/errorsummaries
microsoft.mysqldiscovery/mysqlsites/mysqlservers
microsoft.mysqldiscovery/mysqlsites/refresh
microsoft.mysqldiscovery/mysqlsites/summaries
microsoft.netapp/locations/checkfilepathavailability
microsoft.netapp/locations/checkinventory
microsoft.netapp/locations/checknameavailability
microsoft.netapp/locations/checkquotaavailability
microsoft.netapp/locations/elasticregioninfos
microsoft.netapp/locations/operationresults
microsoft.netapp/locations/querynetworksiblingset
microsoft.netapp/locations/quotalimits
microsoft.netapp/locations/regioninfo
microsoft.netapp/locations/regioninfos
microsoft.netapp/locations/updatenetworksiblingset
microsoft.netapp/locations/usages
microsoft.netapp/netappaccounts/accountbackups
microsoft.netapp/netappaccounts/backuppolicies
microsoft.netapp/netappaccounts/backupvaults
microsoft.netapp/netappaccounts/backupvaults/backups
microsoft.netapp/netappaccounts/capacitypools
microsoft.netapp/netappaccounts/capacitypools/volumes
microsoft.netapp/netappaccounts/capacitypools/volumes/backups
microsoft.netapp/netappaccounts/capacitypools/volumes/mounttargets
microsoft.netapp/netappaccounts/capacitypools/volumes/ransomwarereports
microsoft.netapp/netappaccounts/capacitypools/volumes/snapshots
microsoft.netapp/netappaccounts/capacitypools/volumes/volumequotarules
microsoft.netapp/netappaccounts/quotalimits
microsoft.netapp/netappaccounts/snapshotpolicies
microsoft.netapp/netappaccounts/vaults
microsoft.netapp/netappaccounts/volumegroups
microsoft.networkcloud/clusters/baremetalmachinekeysets
microsoft.networkcloud/clusters/bmckeysets
microsoft.networkcloud/clusters/metricsconfigurations
microsoft.networkcloud/kubernetesclusters/agentpools
microsoft.networkcloud/kubernetesclusters/features
microsoft.networkcloud/locations/operationstatuses
microsoft.networkcloud/virtualmachines/consoles
microsoft.network/dnsforwardingrulesets/forwardingrules
microsoft.network/dnsforwardingrulesets/virtualnetworklinks
microsoft.network/dnsresolverdomainlists/bulk
microsoft.network/dnsresolverpolicies/dnssecurityrules
microsoft.network/dnsresolverpolicies/virtualnetworklinks
microsoft.network/dnsresolvers/inboundendpoints
microsoft.network/dnsresolvers/outboundendpoints
microsoft.network/dnszones/a
microsoft.network/dnszones/aaaa
microsoft.network/dnszones/all
microsoft.network/dnszones/caa
microsoft.network/dnszones/cname
microsoft.network/dnszones/dnssecconfigs
microsoft.network/dnszones/ds
microsoft.network/dnszones/mx
microsoft.network/dnszones/naptr
microsoft.network/dnszones/ptr
microsoft.network/dnszones/recordsets
microsoft.network/dnszones/soa
microsoft.network/dnszones/srv
microsoft.network/dnszones/tlsa
microsoft.network/dnszones/txt
microsoft.network/frontdoors/frontendendpoints
microsoft.network/frontdoors/frontendendpoints/customhttpsconfiguration
microsoft.networkfunction/azuretrafficcollectors/collectorpolicies
microsoft.networkfunction/locations/nfvoperationresults
microsoft.networkfunction/locations/nfvoperations
microsoft.networkfunction/meshvpns/connectionpolicies
microsoft.networkfunction/meshvpns/privateendpointconnectionproxies
microsoft.networkfunction/meshvpns/privateendpointconnections
microsoft.network/locations/applicationgatewaywafdynamicmanifests
microsoft.network/locations/autoapprovedprivatelinkservices
microsoft.network/locations/availabledelegations
microsoft.network/locations/availableprivateendpointtypes
microsoft.network/locations/availableservicealiases
microsoft.network/locations/baremetaltenants
microsoft.network/locations/batchnotifyprivateendpointsforresourcemove
microsoft.network/locations/batchvalidateprivateendpointsforresourcemove
microsoft.network/locations/checkacceleratednetworkingsupport
microsoft.network/locations/checkdnsnameavailability
microsoft.network/locations/checkprivatelinkservicevisibility
microsoft.network/locations/commitinternalazurenetworkmanagerconfiguration
microsoft.network/locations/datatasks
microsoft.network/locations/deletepackettagging
microsoft.network/locations/dnsresolveroperationresults
microsoft.network/locations/dnsresolveroperationstatuses
microsoft.network/locations/dnsresolverpolicyoperationresults
microsoft.network/locations/dnsresolverpolicyoperationstatuses
microsoft.network/locations/effectiveresourceownership
microsoft.network/locations/getazurenetworkmanagerconfiguration
microsoft.network/locations/getpackettagging
microsoft.network/locations/internalazurevirtualnetworkmanageroperation
microsoft.network/locations/ipampooloperationresults
microsoft.network/locations/networksecurityperimeteroperationstatuses
microsoft.network/locations/nfvoperationresults
microsoft.network/locations/nfvoperations
microsoft.network/locations/nspservicetags
microsoft.network/locations/operationresults
microsoft.network/locations/operations
microsoft.network/locations/perimeterassociableresourcetypes
microsoft.network/locations/privatelinkservices
microsoft.network/locations/publicipaddresses
microsoft.network/locations/publishresources
microsoft.network/locations/querynetworksecurityperimeter
microsoft.network/locations/rnmeffectivenetworksecuritygroups
microsoft.network/locations/rnmeffectiveroutetable
microsoft.network/locations/servicetagdetails
microsoft.network/locations/servicetags
microsoft.network/locations/setazurenetworkmanagerconfiguration
microsoft.network/locations/setloadbalancerfrontendpublicipaddresses
microsoft.network/locations/setresourceownership
microsoft.network/locations/startpackettagging
microsoft.network/locations/supportedvirtualmachinesizes
microsoft.network/locations/usages
microsoft.network/locations/validateresourceownership
microsoft.network/locations/verifierworkspaceoperationresults
microsoft.network/locations/virtualnetworkavailableendpointservices
microsoft.network/locations/virtualnetworks
microsoft.network/networkmanagers/ipampools
microsoft.network/networkmanagers/verifierworkspaces
microsoft.network/networkwatchers/agents
microsoft.network/networkwatchers/connectionanalyzers
microsoft.network/networkwatchers/connectionmonitors
microsoft.network/networkwatchers/flowlogs
microsoft.network/networkwatchers/pingmeshes
microsoft.network/privatednszones/a
microsoft.network/privatednszones/aaaa
microsoft.network/privatednszones/all
microsoft.network/privatednszones/cname
microsoft.network/privatednszones/ptr
microsoft.network/privatednszones/soa
microsoft.network/privatednszones/srv
microsoft.network/privatednszones/txt
microsoft.network/privateendpoints/privatelinkserviceproxies
microsoft.network/trafficmanagerprofiles/azureendpoints
microsoft.network/trafficmanagerprofiles/externalendpoints
microsoft.network/trafficmanagerprofiles/heatmaps
microsoft.network/trafficmanagerprofiles/nestedendpoints
microsoft.network/trafficmanagerprofiles/validatelink
microsoft.network/virtualnetworks/listdnsforwardingrulesets
microsoft.network/virtualnetworks/listdnsresolverpolicies
microsoft.network/virtualnetworks/listdnsresolvers
microsoft.network/virtualnetworks/listnetworkmanagereffectiveconnectivityconfigurations
microsoft.network/virtualnetworks/listnetworkmanagereffectivesecurityadminrules
microsoft.network/virtualnetworks/moveipconfigurations
microsoft.network/virtualnetworks/privatednszonelinks
microsoft.network/virtualnetworks/taggedtrafficconsumers
microsoft.nexusidentity/locations/operationstatuses
microsoft.notificationhubs/namespaces/notificationhubs
microsoft.nutanix/locations/operationresults
microsoft.offazure/locations/operationresults
microsoft.offazure/mastersites/sqlsites
microsoft.offazurespringboot/locations/operationstatuses
microsoft.offazurespringboot/springbootsites/errorsummaries
microsoft.offazurespringboot/springbootsites/springbootapps
microsoft.offazurespringboot/springbootsites/springbootservers
microsoft.offazurespringboot/springbootsites/summaries
microsoft.openenergyplatform/energyservices/privateendpointconnectionproxies
microsoft.openenergyplatform/energyservices/privateendpointconnections
microsoft.openenergyplatform/energyservices/privatelinkresources
microsoft.openenergyplatform/locations/operationstatuses
microsoft.operationalinsights/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.operationalinsights/locations/operationstatuses
microsoft.operationalinsights/locations/workspaces
microsoft.operationalinsights/workspaces/api
microsoft.operationalinsights/workspaces/dataexports
microsoft.operationalinsights/workspaces/datasources
microsoft.operationalinsights/workspaces/linkedservices
microsoft.operationalinsights/workspaces/linkedstorageaccounts
microsoft.operationalinsights/workspaces/metadata
microsoft.operationalinsights/workspaces/networksecurityperimeterassociationproxies
microsoft.operationalinsights/workspaces/networksecurityperimeterconfigurations
microsoft.operationalinsights/workspaces/operations
microsoft.operationalinsights/workspaces/purge
microsoft.operationalinsights/workspaces/query
microsoft.operationalinsights/workspaces/scopedprivatelinkproxies
microsoft.operationalinsights/workspaces/storageinsightconfigs
microsoft.operationalinsights/workspaces/summarylogs
microsoft.operationalinsights/workspaces/tables
microsoft.operatorvoicemail/locations/checknameavailability
microsoft.operatorvoicemail/locations/operationstatuses
microsoft.orbital/locations/operationstatuses
microsoft.portal/locations/consoles
microsoft.portal/locations/flags
microsoft.portal/locations/usersettings
microsoft.portalservices/locations/operationstatuses
microsoft.powerbidedicated/locations/checknameavailability
microsoft.powerbidedicated/locations/operationresults
microsoft.powerbidedicated/locations/operationstatuses
microsoft.powerbi/locations/checknameavailability
microsoft.powerbi/privatelinkservicesforpowerbi/operationresults
microsoft.premonition/locations/operationstatuses
microsoft.programenrollment/locations/operationstatuses
microsoft.providerhub/providerregistrations/authorizedapplications
microsoft.providerhub/providerregistrations/checkinmanifest
microsoft.providerhub/providerregistrations/customrollouts
microsoft.providerhub/providerregistrations/defaultrollouts
microsoft.providerhub/providerregistrations/manifests
microsoft.providerhub/providerregistrations/newregionfrontloadrelease
microsoft.providerhub/providerregistrations/resourceactions
microsoft.providerhub/providerregistrations/resourcetyperegistrations
microsoft.purview/accounts/kafkaconfigurations
microsoft.purview/locations/listfeatures
microsoft.purview/locations/operationresults
microsoft.purview/locations/usages
microsoft.quantum/locations/checknameavailability
microsoft.quantum/locations/offerings
microsoft.quantum/locations/operationstatuses
microsoft.quota/groupquotas/groupoperationsstatus
microsoft.quota/groupquotas/groupquotalimits
microsoft.quota/groupquotas/groupquotaoperationsstatus
microsoft.quota/groupquotas/groupquotarequests
microsoft.quota/groupquotas/locationsettings
microsoft.quota/groupquotas/locationsettingsoperationsstatus
microsoft.quota/groupquotas/locationusages
microsoft.quota/groupquotas/quotaallocationoperationsstatus
microsoft.quota/groupquotas/quotaallocationrequests
microsoft.quota/groupquotas/quotaallocations
microsoft.quota/groupquotas/subscriptionrequests
microsoft.quota/groupquotas/subscriptionrequestsoperationsstatus
microsoft.quota/groupquotas/subscriptions
microsoft.recommendationsservice/accounts/modeling
microsoft.recommendationsservice/accounts/serviceendpoints
microsoft.recommendationsservice/locations/operationstatuses
microsoft.recoveryservices/locations/allocatedstamp
microsoft.recoveryservices/locations/allocatestamp
microsoft.recoveryservices/locations/backupaadproperties
microsoft.recoveryservices/locations/backupcrossregionrestore
microsoft.recoveryservices/locations/backupcrrjob
microsoft.recoveryservices/locations/backupcrrjobs
microsoft.recoveryservices/locations/backupcrroperationresults
microsoft.recoveryservices/locations/backupcrroperationsstatus
microsoft.recoveryservices/locations/backupprevalidateprotection
microsoft.recoveryservices/locations/backupstatus
microsoft.recoveryservices/locations/backupvalidatefeatures
microsoft.recoveryservices/locations/capabilities
microsoft.recoveryservices/locations/checknameavailability
microsoft.recoveryservices/locations/deletedvaults
microsoft.recoveryservices/vaults/backupcrosstenantvaultmappings
microsoft.redhatopenshift/locations/openshiftversions
microsoft.redhatopenshift/locations/operationresults
microsoft.redhatopenshift/locations/operationsstatus
microsoft.redhatopenshift/locations/platformworkloadidentityrolesets
microsoft.relay/locations/namespaceoperationresults
microsoft.relay/namespaces/authorizationrules
microsoft.relay/namespaces/hybridconnections
microsoft.relay/namespaces/hybridconnections/authorizationrules
microsoft.relay/namespaces/privateendpointconnectionproxies
microsoft.relay/namespaces/privateendpointconnections
microsoft.relay/namespaces/wcfrelays
microsoft.relay/namespaces/wcfrelays/authorizationrules
microsoft.resourcebuilder/locations/operationstatuses
microsoft.resourceconnector/locations/operationresults
microsoft.resourceconnector/locations/operationsstatus
microsoft.resourcenotifications/locations/notificationsessions
microsoft.resources/builtintemplatespecs/versions
microsoft.resources/deploymentscripts/logs
microsoft.resources/deployments/operations
microsoft.resources/locations/deploymentoperationresults
microsoft.resources/locations/deploymentscriptoperationresults
microsoft.resources/locations/deploymentstackoperationresults
microsoft.resources/locations/deploymentstackoperationstatus
microsoft.resources/locations/deploymentstatuses
microsoft.resources/locations/exportresourcetemplate
microsoft.resources/locations/mobooperationstatuses
microsoft.resources/locations/notifydeploymentjobs
microsoft.resources/subscriptions/locations
microsoft.resources/subscriptions/operationresults
microsoft.resources/subscriptions/providers
microsoft.resources/subscriptions/resourcegroups
microsoft.resources/subscriptions/resourcegroups/resources
microsoft.resources/subscriptions/resources
microsoft.resources/subscriptions/tagnames
microsoft.resources/subscriptions/tagnames/tagvalues
microsoft.resources/subscriptions/tagsoperationresults
microsoft.resources/templatespecs/versions
microsoft.saashub/locations/operationstatuses
microsoft.saashub/locations/tenantoperationstatuses
microsoft.scom/locations/operationstatuses
microsoft.scom/managedinstances/managedgateways
microsoft.scom/managedinstances/monitoredresources
microsoft.scvmm/locations/operationstatuses
microsoft.scvmm/virtualmachines/extensions
microsoft.scvmm/virtualmachines/guestagents
microsoft.scvmm/virtualmachines/hybrididentitymetadata
microsoft.scvmm/vmmservers/inventoryitems
microsoft.search/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.search/locations/operationresults
microsoft.search/locations/usages
microsoft.secretsynccontroller/locations/operationstatuses
microsoft.security/assessments/governanceassignments
microsoft.securitycopilot/locations/operationstatuses
microsoft.security/iotsecuritysolutions/analyticsmodels
microsoft.security/iotsecuritysolutions/analyticsmodels/aggregatedalerts
microsoft.security/iotsecuritysolutions/analyticsmodels/aggregatedrecommendations
microsoft.security/iotsecuritysolutions/iotalerts
microsoft.security/iotsecuritysolutions/iotalerttypes
microsoft.security/iotsecuritysolutions/iotrecommendations
microsoft.security/iotsecuritysolutions/iotrecommendationtypes
microsoft.security/locations/alerts
microsoft.security/locations/allowedconnections
microsoft.security/locations/discoveredsecuritysolutions
microsoft.security/locations/externalsecuritysolutions
microsoft.security/locations/jitnetworkaccesspolicies
microsoft.security/locations/operationresults
microsoft.security/locations/operationstatuses
microsoft.security/locations/securitysolutions
microsoft.security/locations/securitysolutionsreferencedata
microsoft.security/locations/tasks
microsoft.security/locations/topologies
microsoft.securityplatform/locations/operationstatuses
microsoft.security/pricings/securityoperators
microsoft.security/privatelinks/privateendpointconnectionproxies
microsoft.security/privatelinks/privateendpointconnectionproxies/validate
microsoft.security/privatelinks/privateendpointconnections
microsoft.security/privatelinks/privatelinkresources
microsoft.security/regulatorycompliancestandards/regulatorycompliancecontrols
microsoft.security/regulatorycompliancestandards/regulatorycompliancecontrols/regulatorycomplianceassessments
microsoft.security/securescores/securescorecontrols
microsoft.security/securityconnectors/devops
microsoft.serialconsole/locations/consoleservices
microsoft.servicebus/locations/deletevirtualnetworkorsubnets
microsoft.servicebus/locations/namespaceoperationresults
microsoft.servicebus/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.servicebus/locations/operationstatus
microsoft.servicebus/namespaces/authorizationrules
microsoft.servicebus/namespaces/disasterrecoveryconfigs
microsoft.servicebus/namespaces/disasterrecoveryconfigs/checknameavailability
microsoft.servicebus/namespaces/eventgridfilters
microsoft.servicebus/namespaces/migrationconfigurations
microsoft.servicebus/namespaces/networkrulesets
microsoft.servicebus/namespaces/networksecurityperimeterassociationproxies
microsoft.servicebus/namespaces/networksecurityperimeterconfigurations
microsoft.servicebus/namespaces/privateendpointconnectionproxies
microsoft.servicebus/namespaces/privateendpointconnections
microsoft.servicebus/namespaces/queues
microsoft.servicebus/namespaces/queues/authorizationrules
microsoft.servicebus/namespaces/topics
microsoft.servicebus/namespaces/topics/authorizationrules
microsoft.servicebus/namespaces/topics/subscriptions
microsoft.servicebus/namespaces/topics/subscriptions/rules
microsoft.servicefabric/clusters/applications
microsoft.servicefabric/clusters/applications/services
microsoft.servicefabric/clusters/applicationtypes
microsoft.servicefabric/clusters/applicationtypes/versions
microsoft.servicefabric/locations/clusterversions
microsoft.servicefabric/locations/environments
microsoft.servicefabric/locations/environments/managedclusterversions
microsoft.servicefabric/locations/managedclusteroperationresults
microsoft.servicefabric/locations/managedclusteroperations
microsoft.servicefabric/locations/managedclusterversions
microsoft.servicefabric/locations/managedunsupportedvmsizes
microsoft.servicefabric/locations/operationresults
microsoft.servicefabric/locations/operations
microsoft.servicefabric/locations/unsupportedvmsizes
microsoft.servicefabric/managedclusters/applications
microsoft.servicefabric/managedclusters/applications/services
microsoft.servicefabric/managedclusters/applicationtypes
microsoft.servicefabric/managedclusters/applicationtypes/versions
microsoft.servicefabric/managedclusters/nodetypes
microsoft.servicefabricmesh/locations/applicationoperations
microsoft.servicefabricmesh/locations/gatewayoperations
microsoft.servicefabricmesh/locations/networkoperations
microsoft.servicefabricmesh/locations/secretoperations
microsoft.servicefabricmesh/locations/volumeoperations
microsoft.servicelinker/locations/connectors
microsoft.servicelinker/locations/dryruns
microsoft.servicelinker/locations/operationstatuses
microsoft.servicenetworking/locations/operationresults
microsoft.servicenetworking/locations/operations
microsoft.servicenetworking/locations/privateendpointconnectionproxyazureasyncoperation
microsoft.servicenetworking/locations/privateendpointconnectionproxyoperationresults
microsoft.servicenetworking/trafficcontrollers/associations
microsoft.servicenetworking/trafficcontrollers/frontends
microsoft.servicenetworking/trafficcontrollers/privateendpointconnectionproxies
microsoft.servicenetworking/trafficcontrollers/privateendpointconnectionproxies/updateprivateendpointproperties
microsoft.servicenetworking/trafficcontrollers/privateendpointconnectionproxies/validate
microsoft.servicenetworking/trafficcontrollers/privateendpointconnections
microsoft.servicenetworking/trafficcontrollers/privatelinkresources
microsoft.servicenetworking/trafficcontrollers/securitypolicies
microsoft.serviceshub/connectors/connectorspaces
microsoft.signalrservice/locations/checknameavailability
microsoft.signalrservice/locations/operationresults
microsoft.signalrservice/locations/operationstatuses
microsoft.signalrservice/locations/usages
microsoft.signalrservice/signalr/customdomains
microsoft.signalrservice/signalr/eventgridfilters
microsoft.signalrservice/signalr/replicas
microsoft.signalrservice/signalr/sharedprivatelinkresources
microsoft.signalrservice/webpubsub/customdomains
microsoft.signalrservice/webpubsub/replicas
microsoft.signalrservice/webpubsub/sharedprivatelinkresources
microsoft.singularity/accounts/accountquotapolicies
microsoft.singularity/accounts/grouppolicies
microsoft.singularity/accounts/jobs
microsoft.singularity/accounts/models
microsoft.singularity/accounts/networks
microsoft.singularity/accounts/secrets
microsoft.singularity/accounts/storagecontainers
microsoft.singularity/accounts/templatedmodels
microsoft.singularity/locations/instancetypeseries
microsoft.singularity/locations/instancetypeseries/instancetypes
microsoft.singularity/locations/operationnewresults
microsoft.singularity/locations/operationnewstatus
microsoft.singularity/locations/operationresults
microsoft.singularity/locations/operationstatus
microsoft.softwareplan/softwarelicenses/productdownloadoptions
microsoft.softwareplan/softwarelicenses/productkeys
microsoft.softwareplan/softwareperpetualproducts/productdownloadoptions
microsoft.softwareplan/softwareperpetualproducts/productkeys
microsoft.softwareplan/softwaresubscriptions/productdownloadoptions
microsoft.softwareplan/softwaresubscriptions/productkeys
microsoft.solutions/locations/operationstatuses
microsoft.sovereign/locations/checknameavailability
microsoft.sovereign/locations/operationstatuses
microsoft.sql/locations/administratorazureasyncoperation
microsoft.sql/locations/administratoroperationresults
microsoft.sql/locations/advancedthreatprotectionazureasyncoperation
microsoft.sql/locations/advancedthreatprotectionoperationresults
microsoft.sql/locations/auditingsettingsazureasyncoperation
microsoft.sql/locations/auditingsettingsoperationresults
microsoft.sql/locations/capabilities
microsoft.sql/locations/changelongtermretentionbackupaccesstierazureasyncoperation
microsoft.sql/locations/changelongtermretentionbackupaccesstieroperationresults
microsoft.sql/locations/connectionpoliciesazureasyncoperation
microsoft.sql/locations/connectionpoliciesoperationresults
microsoft.sql/locations/databaseazureasyncoperation
microsoft.sql/locations/databaseencryptionprotectorrevalidateazureasyncoperation
microsoft.sql/locations/databaseencryptionprotectorrevalidateoperationresults
microsoft.sql/locations/databaseencryptionprotectorrevertazureasyncoperation
microsoft.sql/locations/databaseencryptionprotectorrevertoperationresults
microsoft.sql/locations/databaseoperationresults
microsoft.sql/locations/databaserestoreazureasyncoperation
microsoft.sql/locations/deletedserverasyncoperation
microsoft.sql/locations/deletedserveroperationresults
microsoft.sql/locations/deletedservers
microsoft.sql/locations/deletevirtualnetworkorsubnets
microsoft.sql/locations/deletevirtualnetworkorsubnetsazureasyncoperation
microsoft.sql/locations/deletevirtualnetworkorsubnetsoperationresults
microsoft.sql/locations/devopsauditingsettingsazureasyncoperation
microsoft.sql/locations/devopsauditingsettingsoperationresults
microsoft.sql/locations/distributedavailabilitygroupsazureasyncoperation
microsoft.sql/locations/distributedavailabilitygroupsoperationresults
microsoft.sql/locations/dnsaliasasyncoperation
microsoft.sql/locations/dnsaliasoperationresults
microsoft.sql/locations/elasticpoolazureasyncoperation
microsoft.sql/locations/elasticpooloperationresults
microsoft.sql/locations/encryptionprotectorazureasyncoperation
microsoft.sql/locations/encryptionprotectoroperationresults
microsoft.sql/locations/extendedauditingsettingsazureasyncoperation
microsoft.sql/locations/extendedauditingsettingsoperationresults
microsoft.sql/locations/externalpolicybasedauthorizationsazureasycoperation
microsoft.sql/locations/externalpolicybasedauthorizationsoperationresults
microsoft.sql/locations/failovergroupazureasyncoperation
microsoft.sql/locations/failovergroupoperationresults
microsoft.sql/locations/firewallrulesazureasyncoperation
microsoft.sql/locations/firewallrulesoperationresults
microsoft.sql/locations/importexportazureasyncoperation
microsoft.sql/locations/importexportoperationresults
microsoft.sql/locations/instancefailovergroupazureasyncoperation
microsoft.sql/locations/instancefailovergroupoperationresults
microsoft.sql/locations/instancefailovergroups
microsoft.sql/locations/instancepoolazureasyncoperation
microsoft.sql/locations/instancepooloperationresults
microsoft.sql/locations/ipv6firewallrulesazureasyncoperation
microsoft.sql/locations/ipv6firewallrulesoperationresults
microsoft.sql/locations/jobagentazureasyncoperation
microsoft.sql/locations/jobagentoperationresults
microsoft.sql/locations/jobagentprivateendpointazureasyncoperation
microsoft.sql/locations/jobagentprivateendpointoperationresults
microsoft.sql/locations/ledgerdigestuploadsazureasyncoperation
microsoft.sql/locations/ledgerdigestuploadsoperationresults
microsoft.sql/locations/longtermretentionbackupazureasyncoperation
microsoft.sql/locations/longtermretentionbackupoperationresults
microsoft.sql/locations/longtermretentionbackups
microsoft.sql/locations/longtermretentionmanagedinstancebackupazureasyncoperation
microsoft.sql/locations/longtermretentionmanagedinstancebackupoperationresults
microsoft.sql/locations/longtermretentionmanagedinstancebackups
microsoft.sql/locations/longtermretentionmanagedinstances
microsoft.sql/locations/longtermretentionpolicyazureasyncoperation
microsoft.sql/locations/longtermretentionpolicyoperationresults
microsoft.sql/locations/longtermretentionservers
microsoft.sql/locations/manageddatabaseazureasyncoperation
microsoft.sql/locations/manageddatabasecompleterestoreazureasyncoperation
microsoft.sql/locations/manageddatabasecompleterestoreoperationresults
microsoft.sql/locations/manageddatabasemoveoperationresults
microsoft.sql/locations/manageddatabaseoperationresults
microsoft.sql/locations/manageddatabaserestoreazureasyncoperation
microsoft.sql/locations/manageddatabaserestoreoperationresults
microsoft.sql/locations/manageddnsaliasasyncoperation
microsoft.sql/locations/manageddnsaliasoperationresults
microsoft.sql/locations/managedinstanceadvancedthreatprotectionazureasyncoperation
microsoft.sql/locations/managedinstanceadvancedthreatprotectionoperationresults
microsoft.sql/locations/managedinstanceazureasyncoperation
microsoft.sql/locations/managedinstancedtcazureasyncoperation
microsoft.sql/locations/managedinstanceencryptionprotectorazureasyncoperation
microsoft.sql/locations/managedinstanceencryptionprotectoroperationresults
microsoft.sql/locations/managedinstancekeyazureasyncoperation
microsoft.sql/locations/managedinstancekeyoperationresults
microsoft.sql/locations/managedinstancelongtermretentionpolicyazureasyncoperation
microsoft.sql/locations/managedinstancelongtermretentionpolicyoperationresults
microsoft.sql/locations/managedinstanceoperationresults
microsoft.sql/locations/managedinstanceprivateendpointconnectionazureasyncoperation
microsoft.sql/locations/managedinstanceprivateendpointconnectionoperationresults
microsoft.sql/locations/managedinstanceprivateendpointconnectionproxyazureasyncoperation
microsoft.sql/locations/managedinstanceprivateendpointconnectionproxyoperationresults
microsoft.sql/locations/managedinstancetdecertazureasyncoperation
microsoft.sql/locations/managedinstancetdecertoperationresults
microsoft.sql/locations/managedledgerdigestuploadsazureasyncoperation
microsoft.sql/locations/managedledgerdigestuploadsoperationresults
microsoft.sql/locations/managedserversecurityalertpoliciesazureasyncoperation
microsoft.sql/locations/managedserversecurityalertpoliciesoperationresults
microsoft.sql/locations/managedshorttermretentionpolicyazureasyncoperation
microsoft.sql/locations/managedshorttermretentionpolicyoperationresults
microsoft.sql/locations/managedtransparentdataencryptionazureasyncoperation
microsoft.sql/locations/managedtransparentdataencryptionoperationresults
microsoft.sql/locations/notifyazureasyncoperation
microsoft.sql/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.sql/locations/outboundfirewallrulesazureasyncoperation
microsoft.sql/locations/outboundfirewallrulesoperationresults
microsoft.sql/locations/privateendpointconnectionazureasyncoperation
microsoft.sql/locations/privateendpointconnectionoperationresults
microsoft.sql/locations/privateendpointconnectionproxyazureasyncoperation
microsoft.sql/locations/privateendpointconnectionproxyoperationresults
microsoft.sql/locations/refreshexternalgovernancestatusazureasyncoperation
microsoft.sql/locations/refreshexternalgovernancestatusmiazureasyncoperation
microsoft.sql/locations/refreshexternalgovernancestatusmioperationresults
microsoft.sql/locations/refreshexternalgovernancestatusoperationresults
microsoft.sql/locations/replicationlinksazureasyncoperation
microsoft.sql/locations/replicationlinksoperationresults
microsoft.sql/locations/securityalertpoliciesazureasyncoperation
microsoft.sql/locations/securityalertpoliciesoperationresults
microsoft.sql/locations/serveradministratorazureasyncoperation
microsoft.sql/locations/serveradministratoroperationresults
microsoft.sql/locations/serverazureasyncoperation
microsoft.sql/locations/serverconfigurationoptionazureasyncoperation
microsoft.sql/locations/serverkeyazureasyncoperation
microsoft.sql/locations/serverkeyoperationresults
microsoft.sql/locations/serveroperationresults
microsoft.sql/locations/servertrustcertificatesazureasyncoperation
microsoft.sql/locations/servertrustcertificatesoperationresults
microsoft.sql/locations/servertrustgroupazureasyncoperation
microsoft.sql/locations/servertrustgroupoperationresults
microsoft.sql/locations/servertrustgroups
microsoft.sql/locations/shorttermretentionpolicyazureasyncoperation
microsoft.sql/locations/shorttermretentionpolicyoperationresults
microsoft.sql/locations/sqlvulnerabilityassessmentazureasyncoperation
microsoft.sql/locations/sqlvulnerabilityassessmentoperationresults
microsoft.sql/locations/startmanagedinstanceazureasyncoperation
microsoft.sql/locations/startmanagedinstanceoperationresults
microsoft.sql/locations/stopmanagedinstanceazureasyncoperation
microsoft.sql/locations/stopmanagedinstanceoperationresults
microsoft.sql/locations/syncagentoperationresults
microsoft.sql/locations/syncdatabaseids
microsoft.sql/locations/syncgroupazureasyncoperation
microsoft.sql/locations/syncgroupoperationresults
microsoft.sql/locations/syncmemberoperationresults
microsoft.sql/locations/tdecertazureasyncoperation
microsoft.sql/locations/tdecertoperationresults
microsoft.sql/locations/transparentdataencryptionazureasyncoperation
microsoft.sql/locations/transparentdataencryptionoperationresults
microsoft.sql/locations/updatemanagedinstancednsserversazureasyncoperation
microsoft.sql/locations/updatemanagedinstancednsserversoperationresults
microsoft.sql/locations/usages
microsoft.sql/locations/virtualclusterazureasyncoperation
microsoft.sql/locations/virtualclusteroperationresults
microsoft.sql/locations/virtualnetworkrulesazureasyncoperation
microsoft.sql/locations/virtualnetworkrulesoperationresults
microsoft.sql/locations/vulnerabilityassessmentscanazureasyncoperation
microsoft.sql/locations/vulnerabilityassessmentscanoperationresults
microsoft.sql/managedinstances/advancedthreatprotectionsettings
microsoft.sql/managedinstances/databases/advancedthreatprotectionsettings
microsoft.sql/managedinstances/databases/backuplongtermretentionpolicies
microsoft.sql/managedinstances/databases/ledgerdigestuploads
microsoft.sql/managedinstances/dnsaliases
microsoft.sql/managedinstances/metricdefinitions
microsoft.sql/managedinstances/metrics
microsoft.sql/managedinstances/recoverabledatabases
microsoft.sql/managedinstances/sqlagent
microsoft.sql/managedinstances/startstopschedules
microsoft.sql/managedinstances/tdecertificates
microsoft.sql/servers/administratoroperationresults
microsoft.sql/servers/advisors
microsoft.sql/servers/aggregateddatabasemetrics
microsoft.sql/servers/automatictuning
microsoft.sql/servers/communicationlinks
microsoft.sql/servers/connectionpolicies
microsoft.sql/servers/databases/advisors
microsoft.sql/servers/databases/auditrecords
microsoft.sql/servers/databases/automatictuning
microsoft.sql/servers/databases/backuplongtermretentionpolicies
microsoft.sql/servers/databases/backupshorttermretentionpolicies
microsoft.sql/servers/databases/datamaskingpolicies
microsoft.sql/servers/databases/datamaskingpolicies/rules
microsoft.sql/servers/databasesecuritypolicies
microsoft.sql/servers/databases/extensions
microsoft.sql/servers/databases/metricdefinitions
microsoft.sql/servers/databases/metrics
microsoft.sql/servers/databases/recommendedsensitivitylabels
microsoft.sql/servers/databases/sqlvulnerabilityassessments
microsoft.sql/servers/databases/syncgroups/syncmembers
microsoft.sql/servers/databases/topqueries
microsoft.sql/servers/databases/topqueries/querytext
microsoft.sql/servers/databases/vulnerabilityassessment
microsoft.sql/servers/databases/vulnerabilityassessmentscans
microsoft.sql/servers/databases/vulnerabilityassessmentsettings
microsoft.sql/servers/devopsauditingsettings
microsoft.sql/servers/disasterrecoveryconfiguration
microsoft.sql/servers/elasticpoolestimates
microsoft.sql/servers/elasticpools/advisors
microsoft.sql/servers/elasticpools/metricdefinitions
microsoft.sql/servers/elasticpools/metrics
microsoft.sql/servers/failovergroups/tryplannedbeforeforcedfailover
microsoft.sql/servers/import
microsoft.sql/servers/importexportoperationresults
microsoft.sql/servers/jobaccounts
microsoft.sql/servers/jobagents/jobs
microsoft.sql/servers/jobagents/jobs/executions
microsoft.sql/servers/jobagents/jobs/steps
microsoft.sql/servers/jobagents/privateendpoints
microsoft.sql/servers/operationresults
microsoft.sql/servers/recommendedelasticpools
microsoft.sql/servers/recoverabledatabases
microsoft.sql/servers/serviceobjectives
microsoft.sql/servers/sqlvulnerabilityassessments
microsoft.sql/servers/tdecertificates
microsoft.sql/servers/usages
microsoft.sqlvirtualmachine/locations/availabilitygrouplisteneroperationresults
microsoft.sqlvirtualmachine/locations/operationtypes
microsoft.sqlvirtualmachine/locations/registersqlvmcandidate
microsoft.sqlvirtualmachine/locations/sqlvirtualmachinegroupoperationresults
microsoft.sqlvirtualmachine/locations/sqlvirtualmachineoperationresults
microsoft.sqlvirtualmachine/sqlvirtualmachinegroups/availabilitygrouplisteners
microsoft.standbypool/locations/operationstatuses
microsoft.standbypool/standbycontainergrouppools/runtimeviews
microsoft.standbypool/standbyvirtualmachinepools/runtimeviews
microsoft.standbypool/standbyvirtualmachinepools/standbyvirtualmachines
microsoft.storageactions/locations/asyncoperations
microsoft.storageactions/locations/operationstatuses
microsoft.storageactions/storagetasks/reports
microsoft.storageactions/storagetasks/storagetaskassignments
microsoft.storagecache/amlfilesystems/autoexportjobs
microsoft.storagecache/amlfilesystems/autoimportjobs
microsoft.storagecache/amlfilesystems/expansionjobs
microsoft.storagecache/amlfilesystems/importjobs
microsoft.storagecache/caches/storagetargets
microsoft.storagecache/locations/ascoperations
microsoft.storagecache/locations/usages
microsoft.storagediscovery/locations/operationstatuses
microsoft.storagediscovery/storagediscoveryworkspaces/reports
microsoft.storage/locations/actionsrpoperationstatuses
microsoft.storage/locations/asyncoperations
microsoft.storage/locations/checknameavailability
microsoft.storage/locations/deletedaccounts
microsoft.storage/locations/deletevirtualnetworkorsubnets
microsoft.storage/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.storage/locations/usages
microsoft.storagemover/locations/operationstatuses
microsoft.storagemover/storagemovers/agents
microsoft.storagemover/storagemovers/connections
microsoft.storagemover/storagemovers/endpoints
microsoft.storagemover/storagemovers/projects
microsoft.storagemover/storagemovers/projects/jobdefinitions
microsoft.storagemover/storagemovers/projects/jobdefinitions/jobruns
microsoft.storage/storageaccounts/advancedplatformmetrics
microsoft.storage/storageaccounts/blobservices
microsoft.storage/storageaccounts/encryptionscopes
microsoft.storage/storageaccounts/fileservices
microsoft.storage/storageaccounts/listaccountsas
microsoft.storage/storageaccounts/listservicesas
microsoft.storage/storageaccounts/queueservices
microsoft.storage/storageaccounts/reports
microsoft.storage/storageaccounts/services
microsoft.storage/storageaccounts/services/metricdefinitions
microsoft.storage/storageaccounts/storagetaskassignments
microsoft.storage/storageaccounts/storagetaskassignments/reports
microsoft.storage/storageaccounts/tableservices
microsoft.storagesync/locations/checknameavailability
microsoft.storagesync/locations/operationresults
microsoft.storagesync/locations/operations
microsoft.storagesync/locations/workflows
microsoft.storagesync/storagesyncservices/registeredservers
microsoft.storagesync/storagesyncservices/syncgroups
microsoft.storagesync/storagesyncservices/syncgroups/cloudendpoints
microsoft.storagesync/storagesyncservices/syncgroups/serverendpoints
microsoft.storagesync/storagesyncservices/workflows
microsoft.streamanalytics/clusters/privateendpoints
microsoft.streamanalytics/locations/compilequery
microsoft.streamanalytics/locations/operationresults
microsoft.streamanalytics/locations/quotas
microsoft.streamanalytics/locations/sampleinput
microsoft.streamanalytics/locations/testinput
microsoft.streamanalytics/locations/testoutput
microsoft.streamanalytics/locations/testquery
microsoft.supercomputerinfrastructure/locations/operationstatuses
microsoft.support/fileworkspaces/files
microsoft.support/services/problemclassifications
microsoft.support/supporttickets/communications
microsoft.synapse/locations/kustopoolchecknameavailability
microsoft.synapse/locations/kustopooloperationresults
microsoft.synapse/locations/operationresults
microsoft.synapse/locations/operationstatuses
microsoft.synapse/locations/sqldatabaseazureasyncoperation
microsoft.synapse/locations/sqldatabaseoperationresults
microsoft.synapse/locations/sqlpoolazureasyncoperation
microsoft.synapse/locations/sqlpooloperationresults
microsoft.synapse/locations/usages
microsoft.synapse/workspaces/bigdatapools
microsoft.synapse/workspaces/kustopools
microsoft.synapse/workspaces/kustopools/attacheddatabaseconfigurations
microsoft.synapse/workspaces/kustopools/databases
microsoft.synapse/workspaces/kustopools/databases/dataconnections
microsoft.synapse/workspaces/operationresults
microsoft.synapse/workspaces/operationstatuses
microsoft.synapse/workspaces/sqldatabases
microsoft.synapse/workspaces/sqlpools
microsoft.synapse/workspaces/usages
microsoft.syntex/locations/operationstatuses
microsoft.videoindexer/accounts/privateendpointconnections
microsoft.videoindexer/accounts/privatelinkresources
microsoft.videoindexer/locations/classicaccounts
microsoft.videoindexer/locations/operationstatuses
microsoft.videoindexer/locations/userclassicaccounts
microsoft.virtualmachineimages/imagetemplates/runoutputs
microsoft.virtualmachineimages/imagetemplates/triggers
microsoft.virtualmachineimages/locations/operations
microsoft.visualstudio/account/extension
microsoft.visualstudio/account/project
microsoft.vmware/locations/operationstatuses
microsoft.vmware/vcenters/inventoryitems
microsoft.web/hostingenvironments/eventgridfilters
microsoft.web/locations/apioperations
microsoft.web/locations/checknameavailability
microsoft.web/locations/connectiongatewayinstallations
microsoft.web/locations/deletedsites
microsoft.web/locations/deletevirtualnetworkorsubnets
microsoft.web/locations/extractapidefinitionfromwsdl
microsoft.web/locations/functionappstacks
microsoft.web/locations/getnetworkpolicies
microsoft.web/locations/listvirtualnetworkintegrations
microsoft.web/locations/listwsdlinterfaces
microsoft.web/locations/managedapis
microsoft.web/locations/notifynetworksecurityperimeterupdatesavailable
microsoft.web/locations/operationresults
microsoft.web/locations/operations
microsoft.web/locations/previewstaticsiteworkflowfile
microsoft.web/locations/purgeunusedvirtualnetworkintegration
microsoft.web/locations/runtimes
microsoft.web/locations/staticsitesoperationresults
microsoft.web/locations/staticsitesoperationstatuses
microsoft.web/locations/usages
microsoft.web/locations/validatedeletevirtualnetworkorsubnets
microsoft.web/locations/webappstacks
microsoft.web/serverfarms/eventgridfilters
microsoft.web/serverfarms/firstpartyapps
microsoft.web/serverfarms/firstpartyapps/keyvaultsettings
microsoft.web/sites/certificates
microsoft.web/sites/eventgridfilters
microsoft.web/sites/hostnamebindings
microsoft.web/sites/networkconfig
microsoft.web/sites/premieraddons
microsoft.web/sites/slots/certificates
microsoft.web/sites/slots/eventgridfilters
microsoft.web/sites/slots/hostnamebindings
microsoft.web/sites/slots/networkconfig
microsoft.web/staticsites/builds/databaseconnections
microsoft.web/staticsites/builds/linkedbackends
microsoft.web/staticsites/builds/userprovidedfunctionapps
microsoft.web/staticsites/databaseconnections
microsoft.web/staticsites/linkedbackends
microsoft.web/staticsites/userprovidedfunctionapps
microsoft.weightsandbiases/locations/operationstatuses
microsoft.workloadbuilder/locations/operationstatuses
microsoft.workloads/connectors/acssbackups
microsoft.workloads/locations/operationstatuses
microsoft.workloads/locations/sapvirtualinstancemetadata
microsoft.workloads/monitors/providerinstances
microsoft.workloads/monitors/saplandscapemonitor
microsoft.workloads/sapdiscoverysites/sapinstances
microsoft.workloads/sapdiscoverysites/sapinstances/serverinstances
microsoft.workloads/sapvirtualinstances/applicationinstances
microsoft.workloads/sapvirtualinstances/centralinstances
microsoft.workloads/sapvirtualinstances/databaseinstances
```
