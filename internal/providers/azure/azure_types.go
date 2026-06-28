package azure

// Resource type constants for all Azure resource types discovered by this provider.
// Using constants prevents typos in scanner and resolver files — a mismatched
// string would create orphan resources with an undeclared type.
const (
	// Resource Manager
	TypeResourcesResourceGroup = "azure:microsoft.resources:resource-groups"
	TypeSubscription           = "azure:microsoft.resources:subscriptions"
	// Management Groups (tenant scope)
	TypeManagementGroup = "azure:microsoft.management:management-groups"
	// Service quota limits via the unified Microsoft.Quota proxy. One row per
	// (provider-namespace, location, resource) quota; limit-only (no usage) so
	// the resource version chain bumps only on an actual limit change.
	TypeQuotaLimit = "azure:microsoft.quota:quotas"
	// Entra ID (Microsoft Graph — tenant scope)
	TypeEntraUser             = "azure:microsoft.entra:user"
	TypeEntraGroup            = "azure:microsoft.entra:group"
	TypeEntraServicePrincipal = "azure:microsoft.entra:service-principal"
	TypeEntraApplication      = "azure:microsoft.entra:application"
	// Microsoft Defender for Cloud
	TypeSecurityPricing = "azure:microsoft.security:pricings"
	// Authorization (RBAC)
	TypeAuthorizationRoleAssignment = "azure:microsoft.authorization:role-assignments"
	TypeAuthorizationRoleDefinition = "azure:microsoft.authorization:role-definitions"
	// Managed Identity (user-assigned)
	TypeManagedIdentityUserAssigned = "azure:microsoft.managedidentity:user-assigned-identities"
	// Log Analytics
	TypeOpInsightsWorkspace = "azure:microsoft.operationalinsights:workspaces"
	// Container Registry
	TypeContainerRegistryRegistry = "azure:microsoft.containerregistry:registries"
	// Cosmos DB
	TypeCosmosDatabaseAccount = "azure:microsoft.documentdb:database-accounts"
	// Redis Cache
	TypeRedisCache = "azure:microsoft.cache:redis"
	// Container Apps + Container Instances
	TypeAppContainersManagedEnvironment = "azure:microsoft.app:managed-environments"
	TypeAppContainersContainerApp       = "azure:microsoft.app:container-apps"
	TypeContainerInstanceContainerGroup = "azure:microsoft.containerinstance:container-groups"
	// Database flexible servers
	TypePostgreSQLFlexibleServer = "azure:microsoft.dbforpostgresql:flexible-servers"
	TypeMySQLFlexibleServer      = "azure:microsoft.dbformysql:flexible-servers"
	// Databases & analytics (Wave 2)
	TypeAnalysisServicesServer   = "azure:microsoft.analysisservices:servers"
	TypeDatabaseWatcher          = "azure:microsoft.databasewatcher:watchers"
	TypeDataMigrationService     = "azure:microsoft.datamigration:services"
	TypeHorizonDBCluster         = "azure:microsoft.horizondb:clusters"
	TypeMongoCluster             = "azure:microsoft.documentdb:mongo-clusters"
	TypePostgreSQLServerGroupV2  = "azure:microsoft.dbforpostgresql:server-groups-v2"
	TypePowerBIDedicatedCapacity = "azure:microsoft.powerbidedicated:capacities"
	// Hybrid / Arc (Wave 3)
	TypeAzureArcDataController = "azure:microsoft.azurearcdata:data-controllers"
	TypeAzureStackHCICluster   = "azure:microsoft.azurestackhci:clusters"
	// Azure Local (HCI) VM/network/storage family — armazurestackhcivm module.
	TypeAzureStackHCIGalleryImage            = "azure:microsoft.azurestackhci:gallery-images"
	TypeAzureStackHCILogicalNetwork          = "azure:microsoft.azurestackhci:logical-networks"
	TypeAzureStackHCIMarketplaceGalleryImage = "azure:microsoft.azurestackhci:marketplace-gallery-images"
	TypeAzureStackHCINetworkInterface        = "azure:microsoft.azurestackhci:network-interfaces"
	TypeAzureStackHCINetworkSecurityGroup    = "azure:microsoft.azurestackhci:network-security-groups"
	TypeAzureStackHCIStorageContainer        = "azure:microsoft.azurestackhci:storage-containers"
	TypeAzureStackHCIVirtualHardDisk         = "azure:microsoft.azurestackhci:virtual-hard-disks"
	TypeConnectedVMwareVCenter               = "azure:microsoft.connectedvmwarevsphere:vcenters"
	TypeConnectedVMwareCluster               = "azure:microsoft.connectedvmwarevsphere:clusters"
	TypeConnectedVMwareDatastore             = "azure:microsoft.connectedvmwarevsphere:datastores"
	TypeConnectedVMwareHost                  = "azure:microsoft.connectedvmwarevsphere:hosts"
	TypeConnectedVMwareResourcePool          = "azure:microsoft.connectedvmwarevsphere:resource-pools"
	TypeConnectedVMwareVMTemplate            = "azure:microsoft.connectedvmwarevsphere:virtual-machine-templates"
	TypeConnectedVMwareVirtualNetwork        = "azure:microsoft.connectedvmwarevsphere:virtual-networks"
	TypeCustomLocation                       = "azure:microsoft.extendedlocation:custom-locations"
	TypeHybridComputeMachine                 = "azure:microsoft.hybridcompute:machines"
	TypeHybridComputePrivateLinkScope        = "azure:microsoft.hybridcompute:private-link-scopes"
	TypeHybridConnectivityPublicCloud        = "azure:microsoft.hybridconnectivity:public-cloud-connectors"
	TypeHybridContainerVirtualNetwork        = "azure:microsoft.hybridcontainerservice:virtual-networks"
	TypeHybridNetworkDevice                  = "azure:microsoft.hybridnetwork:devices"
	TypeKubernetesConnectedCluster           = "azure:microsoft.kubernetes:connected-clusters"
	TypeResourceConnectorAppliance           = "azure:microsoft.resourceconnector:appliances"
	TypeScVmmServer                          = "azure:microsoft.scvmm:vmm-servers"
	TypeScVmmCloud                           = "azure:microsoft.scvmm:clouds"
	TypeScVmmAvailabilitySet                 = "azure:microsoft.scvmm:availability-sets"
	TypeScVmmVMTemplate                      = "azure:microsoft.scvmm:virtual-machine-templates"
	TypeScVmmVirtualNetwork                  = "azure:microsoft.scvmm:virtual-networks"
	TypeAzureArcDataPostgres                 = "azure:microsoft.azurearcdata:postgres-instances"
	TypeAzureArcDataSQLManagedInstance       = "azure:microsoft.azurearcdata:sql-managed-instances"
	TypeAzureArcDataSQLServerInstance        = "azure:microsoft.azurearcdata:sql-server-instances"
	TypeConnectedCacheEnterpriseCustomer     = "azure:microsoft.connectedcache:enterprise-mcc-customers"
	TypeConnectedCacheIspCustomer            = "azure:microsoft.connectedcache:isp-customers"
	// Networking & telco/edge (Wave 4)
	TypeEdgeZonesExtendedZone              = "azure:microsoft.edgezones:extended-zones"
	TypeHybridNetworkFunction              = "azure:microsoft.hybridnetwork:network-functions"
	TypeManagedNetworkFabric               = "azure:microsoft.managednetworkfabric:network-fabrics"
	TypeNetworkCloudCluster                = "azure:microsoft.networkcloud:clusters"
	TypeNetworkFunctionTrafficCollector    = "azure:microsoft.networkfunction:azure-traffic-collectors"
	TypePeeringPeering                     = "azure:microsoft.peering:peerings"
	TypeServiceNetworkingTrafficController = "azure:microsoft.servicenetworking:traffic-controllers"
	// Compute & scale (Wave 5)
	TypeComputeFleet                = "azure:microsoft.azurefleet:fleets"
	TypeLargeInstance               = "azure:microsoft.azurelargeinstance:azure-large-instances"
	TypeBareMetalInstance           = "azure:microsoft.baremetalinfrastructure:bare-metal-instances"
	TypeDevOpsInfrastructurePool    = "azure:microsoft.devopsinfrastructure:pools"
	TypeSQLVirtualMachine           = "azure:microsoft.sqlvirtualmachine:sql-virtual-machines"
	TypeStandbyVMPool               = "azure:microsoft.standbypool:standby-virtual-machine-pools"
	TypeStandbyContainerGroupPool   = "azure:microsoft.standbypool:standby-container-group-pools"
	TypeWorkloadsSAPVirtualInstance = "azure:microsoft.workloads:sap-virtual-instances"
	// IoT / device / edge-order (Wave 6)
	TypeDeviceRegistryAsset         = "azure:microsoft.deviceregistry:assets"
	TypeDeviceUpdateAccount         = "azure:microsoft.deviceupdate:accounts"
	TypeEdgeOrderItem               = "azure:microsoft.edgeorder:order-items"
	TypeIoTCentralApp               = "azure:microsoft.iotcentral:iot-apps"
	TypeIoTFirmwareDefenseWorkspace = "azure:microsoft.iotfirmwaredefense:workspaces"
	TypeIoTOperationsInstance       = "azure:microsoft.iotoperations:instances"
	// AI / ML / media / test (Wave 7)
	TypeHealthBot                      = "azure:microsoft.healthbot:health-bots"
	TypeHealthDataAIDeidService        = "azure:microsoft.healthdataaiservices:deid-services"
	TypeLoadTest                       = "azure:microsoft.loadtestservice:load-tests"
	TypeOnlineExperimentationWorkspace = "azure:microsoft.onlineexperimentation:workspaces"
	TypePlaywrightAccount              = "azure:microsoft.azureplaywrightservice:accounts"
	TypeQuantumWorkspace               = "azure:microsoft.quantum:workspaces"
	// Dev/test, integration, mgmt, security, misc (Wave 8)
	TypeAPICenterService              = "azure:microsoft.apicenter:services"
	TypeAttestationProvider           = "azure:microsoft.attestation:attestation-providers"
	TypeAutomanageConfigProfile       = "azure:microsoft.automanage:configuration-profiles"
	TypeAzureSphereCatalog            = "azure:microsoft.azuresphere:catalogs"
	TypeCertificateOrder              = "azure:microsoft.certificateregistration:certificate-orders"
	TypeConfidentialLedger            = "azure:microsoft.confidentialledger:ledgers"
	TypeDevCenter                     = "azure:microsoft.devcenter:dev-centers"
	TypeDevHubWorkflow                = "azure:microsoft.devhub:workflows"
	TypeDevTestLab                    = "azure:microsoft.devtestlab:labs"
	TypeDomain                        = "azure:microsoft.domainregistration:domains"
	TypeDurableTaskScheduler          = "azure:microsoft.durabletask:schedulers"
	TypeFabricCapacity                = "azure:microsoft.fabric:capacities"
	TypeFluidRelayServer              = "azure:microsoft.fluidrelay:fluid-relay-servers"
	TypeGraphServicesAccount          = "azure:microsoft.graphservices:accounts"
	TypeDedicatedHsm                  = "azure:microsoft.hardwaresecuritymodules:dedicated-hsms"
	TypeIntegrationSpace              = "azure:microsoft.integrationspaces:spaces"
	TypeLabServicesLab                = "azure:microsoft.labservices:labs"
	TypeMaintenanceConfiguration      = "azure:microsoft.maintenance:maintenance-configurations"
	TypeMigrateAssessmentProject      = "azure:microsoft.migrate:assessment-projects"
	TypeNotificationHubNamespace      = "azure:microsoft.notificationhubs:namespaces"
	TypePowerPlatformEnterprisePolicy = "azure:microsoft.powerplatform:enterprise-policies"
	TypeOpenShiftCluster              = "azure:microsoft.redhatopenshift:openshift-clusters"
	TypeSpringbootSite                = "azure:microsoft.offazurespringboot:springboot-sites"
	// Verify-or-drop keepers (genuine user resources from the bucket)
	TypeManagedServicesRegistrationDefinition = "azure:microsoft.managedservices:registration-definitions"
	TypePolicyInsightsRemediation             = "azure:microsoft.policyinsights:remediations"
	TypePolicyInsightsAttestation             = "azure:microsoft.policyinsights:attestations"
	// Private Endpoints
	TypeNetworkPrivateEndpoint = "azure:microsoft.network:private-endpoints"
	// Enterprise networking — ExpressRoute + Virtual WAN + VPN
	TypeNetworkExpressRouteCircuit = "azure:microsoft.network:express-route-circuits"
	TypeNetworkVirtualWAN          = "azure:microsoft.network:virtual-wans"
	TypeNetworkVirtualHub          = "azure:microsoft.network:virtual-hubs"
	TypeNetworkVPNSite             = "azure:microsoft.network:vpn-sites"
	TypeNetworkVPNGateway          = "azure:microsoft.network:vpn-gateways"
	TypeNetworkVirtualNetworkGW    = "azure:microsoft.network:virtual-network-gateways"
	TypeNetworkExpressRouteGateway = "azure:microsoft.network:express-route-gateways"
	// Messaging — Event Hubs + Service Bus
	TypeEventHubNamespace   = "azure:microsoft.eventhub:namespaces"
	TypeServiceBusNamespace = "azure:microsoft.servicebus:namespaces"
	// Eventing — Event Grid
	TypeEventGridTopic             = "azure:microsoft.eventgrid:topics"
	TypeEventGridSystemTopic       = "azure:microsoft.eventgrid:system-topics"
	TypeEventGridDomain            = "azure:microsoft.eventgrid:domains"
	TypeEventGridEventSubscription = "azure:microsoft.eventgrid:event-subscriptions"
	// L7 ingress
	TypeNetworkApplicationGateway    = "azure:microsoft.network:application-gateways"
	TypeNetworkTrafficManagerProfile = "azure:microsoft.network:traffic-manager-profiles"
	TypeCDNProfile                   = "azure:microsoft.cdn:profiles"
	TypeAPIManagementService         = "azure:microsoft.apimanagement:service"
	// Analytics — Databricks + Synapse
	TypeDatabricksWorkspace = "azure:microsoft.databricks:workspaces"
	TypeSynapseWorkspace    = "azure:microsoft.synapse:workspaces"
	TypeDataFactoryFactory  = "azure:microsoft.datafactory:factories"
	// Integration — Logic Apps
	TypeLogicWorkflow = "azure:microsoft.logic:workflows"
	// Azure Policy
	TypePolicyDefinition    = "azure:microsoft.authorization:policy-definitions"
	TypePolicySetDefinition = "azure:microsoft.authorization:policy-set-definitions"
	TypePolicyAssignment    = "azure:microsoft.authorization:policy-assignments"
	// Public IP (already covered by network scanner — no const needed beyond the existing one above)
	// DNS — public + private zones + vnet links
	TypeDNSZone                = "azure:microsoft.network:dns-zones"
	TypeDNSPrivateZone         = "azure:microsoft.network:private-dns-zones"
	TypeDNSPrivateZoneVNetLink = "azure:microsoft.network:private-dns-zones:virtual-network-links"
	TypeDNSRecordSet           = "azure:microsoft.network:dns-zones:record-set"
	TypeDNSPrivateRecordSet    = "azure:microsoft.network:private-dns-zones:record-set"
	// Compute — vms
	TypeComputeVirtualMachine          = "azure:microsoft.compute:virtual-machines"
	TypeComputeVMExtension             = "azure:microsoft.compute:virtual-machines:extensions"
	TypeComputeAvailabilitySet         = "azure:microsoft.compute:availability-sets"
	TypeComputeProximityPlacementGroup = "azure:microsoft.compute:proximity-placement-groups"
	TypeComputeSSHPublicKey            = "azure:microsoft.compute:ssh-public-keys"
	TypeComputeRestorePointCollection  = "azure:microsoft.compute:restore-point-collections"
	// Compute — disks
	TypeComputeManagedDisk       = "azure:microsoft.compute:disks"
	TypeComputeSnapshot          = "azure:microsoft.compute:snapshots"
	TypeComputeDiskEncryptionSet = "azure:microsoft.compute:disk-encryption-sets"
	TypeComputeDiskAccess        = "azure:microsoft.compute:disk-accesses"
	// Compute — images
	TypeComputeImage = "azure:microsoft.compute:images"
	// Compute — vmss
	TypeComputeVMSS            = "azure:microsoft.compute:virtual-machine-scale-sets"
	TypeComputeVMSSExtension   = "azure:microsoft.compute:virtual-machine-scale-sets:extensions"
	TypeComputeVMSSVM          = "azure:microsoft.compute:virtual-machine-scale-sets:virtual-machines"
	TypeComputeVMSSVMExtension = "azure:microsoft.compute:virtual-machine-scale-sets:virtual-machines:extensions"
	// Compute — galleries
	TypeComputeGallery                   = "azure:microsoft.compute:galleries"
	TypeComputeGalleryImage              = "azure:microsoft.compute:galleries:images"
	TypeComputeGalleryImageVersion       = "azure:microsoft.compute:galleries:images:versions"
	TypeComputeGalleryApplication        = "azure:microsoft.compute:galleries:applications"
	TypeComputeGalleryApplicationVersion = "azure:microsoft.compute:galleries:applications:versions"
	TypeComputeGalleryInVMACP            = "azure:microsoft.compute:galleries:in-vm-access-control-profiles"
	TypeComputeGalleryInVMACPVersion     = "azure:microsoft.compute:galleries:in-vm-access-control-profiles:versions"
	// Compute — dedicated
	TypeComputeHostGroup                = "azure:microsoft.compute:host-groups"
	TypeComputeDedicatedHost            = "azure:microsoft.compute:host-groups:hosts"
	TypeComputeCapacityReservationGroup = "azure:microsoft.compute:capacity-reservation-groups"
	TypeComputeCapacityReservation      = "azure:microsoft.compute:capacity-reservation-groups:capacity-reservations"
	// Compute — cloud-services
	TypeComputeCloudService             = "azure:microsoft.compute:cloud-services"
	TypeComputeCloudServiceRole         = "azure:microsoft.compute:cloud-services:roles"
	TypeComputeCloudServiceRoleInstance = "azure:microsoft.compute:cloud-services:role-instances"
	// Network
	TypeNetworkVirtualNetwork  = "azure:microsoft.network:virtual-networks"
	TypeNetworkSubnet          = "azure:microsoft.network:virtual-networks:subnets"
	TypeNetworkSecurityGroup   = "azure:microsoft.network:network-security-groups"
	TypeNetworkPublicIPAddress = "azure:microsoft.network:public-ip-addresses"
	// Container Service (AKS)
	TypeContainerServiceManagedCluster = "azure:microsoft.containerservice:managed-clusters"
	// Key Vault
	TypeKeyVaultVault = "azure:microsoft.keyvault:vaults"
	// Storage
	TypeStorageStorageAccount = "azure:microsoft.storage:storage-accounts"
	// Storage & data-plane adjacencies (Wave 1)
	TypeDataBoxJob                = "azure:microsoft.databox:jobs"
	TypeDataBoxEdgeDevice         = "azure:microsoft.databoxedge:data-box-edge-devices"
	TypeDataShareAccount          = "azure:microsoft.datashare:accounts"
	TypeElasticSan                = "azure:microsoft.elasticsan:elastic-sans"
	TypeFileSharesFileShare       = "azure:microsoft.fileshares:file-shares"
	TypeStorageActionsTask        = "azure:microsoft.storageactions:storage-tasks"
	TypeStorageCacheCache         = "azure:microsoft.storagecache:caches"
	TypeStorageDiscoveryWorkspace = "azure:microsoft.storagediscovery:storage-discovery-workspaces"
	TypeStorageMover              = "azure:microsoft.storagemover:storage-movers"
	// SQL — servers and databases
	TypeSQLServer   = "azure:microsoft.sql:servers"
	TypeSQLDatabase = "azure:microsoft.sql:servers:databases"
	// SQL — subscription-wide resources
	TypeSQLInstancePool   = "azure:microsoft.sql:instance-pools"
	TypeSQLVirtualCluster = "azure:microsoft.sql:virtual-clusters"
	// SQL — managed instances and sub-resources
	TypeSQLManagedInstance            = "azure:microsoft.sql:managed-instances"
	TypeSQLManagedDatabase            = "azure:microsoft.sql:managed-instances:databases"
	TypeSQLManagedInstanceAdmin       = "azure:microsoft.sql:managed-instances:administrators"
	TypeSQLManagedInstanceVA          = "azure:microsoft.sql:managed-instances:vulnerability-assessments"
	TypeSQLManagedDatabaseVA          = "azure:microsoft.sql:managed-instances:databases:vulnerability-assessments"
	TypeSQLManagedInstanceKey         = "azure:microsoft.sql:managed-instances:keys"
	TypeSQLManagedInstanceEP          = "azure:microsoft.sql:managed-instances:encryption-protector"
	TypeSQLManagedInstancePEC         = "azure:microsoft.sql:managed-instances:private-endpoint-connections"
	TypeSQLManagedServerSecurityAlert = "azure:microsoft.sql:managed-instances:security-alert-policies"
	TypeSQLManagedDatabaseTDE         = "azure:microsoft.sql:managed-instances:databases:transparent-data-encryption"
	TypeSQLManagedDatabaseSecAlert    = "azure:microsoft.sql:managed-instances:databases:security-alert-policies"
	// SQL — server sub-resources
	TypeSQLServerKey                 = "azure:microsoft.sql:servers:keys"
	TypeSQLEncryptionProtector       = "azure:microsoft.sql:servers:encryption-protector"
	TypeSQLServerAdministrator       = "azure:microsoft.sql:servers:administrators"
	TypeSQLServerAuditingSettings    = "azure:microsoft.sql:servers:auditing-settings"
	TypeSQLServerExtAuditingSettings = "azure:microsoft.sql:servers:extended-auditing-settings"
	TypeSQLServerDevOpsAuditSettings = "azure:microsoft.sql:servers:dev-ops-audit-settings"
	TypeSQLServerSecurityAlert       = "azure:microsoft.sql:servers:security-alert-policies"
	TypeSQLServerAdvancedThreatProt  = "azure:microsoft.sql:servers:advanced-threat-protection-settings"
	TypeSQLServerVulnAssessment      = "azure:microsoft.sql:servers:vulnerability-assessments"
	TypeSQLElasticPool               = "azure:microsoft.sql:servers:elasticpools"
	TypeSQLFailoverGroup             = "azure:microsoft.sql:servers:failover-groups"
	TypeSQLServerDNSAlias            = "azure:microsoft.sql:servers:dns-aliases"
	TypeSQLVirtualNetworkRule        = "azure:microsoft.sql:servers:virtual-network-rules"
	TypeSQLJobAgent                  = "azure:microsoft.sql:servers:job-agents"
	TypeSQLSyncAgent                 = "azure:microsoft.sql:servers:sync-agents"
	TypeSQLRestorableDroppedDB       = "azure:microsoft.sql:servers:restorable-dropped-databases"
	// SQL — database sub-resources
	TypeSQLDBTransparentDataEnc = "azure:microsoft.sql:servers:databases:transparent-data-encryption"
	TypeSQLDBSecurityAlert      = "azure:microsoft.sql:servers:databases:security-alert-policies"
	TypeSQLDBAdvancedThreatProt = "azure:microsoft.sql:servers:databases:advanced-threat-protection-settings"
	TypeSQLDBAuditingSettings   = "azure:microsoft.sql:servers:databases:auditing-settings"
	TypeSQLDBVulnAssessment     = "azure:microsoft.sql:servers:databases:vulnerability-assessments"
	TypeSQLSyncGroup            = "azure:microsoft.sql:servers:databases:sync-groups"
	TypeSQLReplicationLink      = "azure:microsoft.sql:servers:databases:replication-links"
	TypeSQLWorkloadGroup        = "azure:microsoft.sql:servers:databases:workload-groups"
	TypeSQLGeoBackupPolicy      = "azure:microsoft.sql:servers:databases:geo-backup-policies"
	TypeSQLLedgerDigestUpload   = "azure:microsoft.sql:servers:databases:ledger-digest-uploads"
	// App Service / Microsoft.Web
	TypeAppServiceServerFarm               = "azure:microsoft.web:server-farms"
	TypeAppServiceSite                     = "azure:microsoft.web:sites"
	TypeAppServiceSiteSlot                 = "azure:microsoft.web:sites:slots"
	TypeAppServiceEnvironment              = "azure:microsoft.web:hosting-environments"
	TypeAppServiceEnvironmentWorkerPool    = "azure:microsoft.web:hosting-environments:worker-pools"
	TypeAppServiceEnvironmentMultiRolePool = "azure:microsoft.web:hosting-environments:multi-role-pools"
	TypeAppServiceKubeEnvironment          = "azure:microsoft.web:kube-environments"
	TypeAppServiceStaticSite               = "azure:microsoft.web:static-sites"
	TypeAppServiceStaticSiteBuild          = "azure:microsoft.web:static-sites:builds"
	TypeAppServiceCertificate              = "azure:microsoft.web:certificates"
	// Cognitive Services (Azure AI / OpenAI)
	TypeCognitiveServicesAccount = "azure:microsoft.cognitiveservices:accounts"
	// App Configuration
	TypeAppConfigurationStore = "azure:microsoft.appconfiguration:configuration-stores"
	// Azure AI Search
	TypeSearchService = "azure:microsoft.search:search-services"
	// Recovery Services (Backup / Site Recovery vaults)
	TypeRecoveryServicesVault = "azure:microsoft.recoveryservices:vaults"
	// Data Protection (Backup vaults)
	TypeDataProtectionBackupVault = "azure:microsoft.dataprotection:backup-vaults"
	// Batch
	TypeBatchAccount = "azure:microsoft.batch:batch-accounts"
	// Azure Data Explorer (Kusto)
	TypeKustoCluster = "azure:microsoft.kusto:clusters"
	// Azure NetApp Files
	TypeNetAppAccount = "azure:microsoft.netapp:net-app-accounts"
	// Azure Spring Apps
	TypeAppPlatformService = "azure:microsoft.appplatform:spring"
	// Azure Machine Learning
	TypeMachineLearningWorkspace = "azure:microsoft.machinelearningservices:workspaces"
	// Automation
	TypeAutomationAccount = "azure:microsoft.automation:automation-accounts"
	// SignalR / Web PubSub
	TypeSignalR   = "azure:microsoft.signalrservice:signalr"
	TypeWebPubSub = "azure:microsoft.signalrservice:web-pub-sub"
	// Stream Analytics
	TypeStreamAnalyticsJob = "azure:microsoft.streamanalytics:streaming-jobs"
	// HDInsight
	TypeHDInsightCluster = "azure:microsoft.hdinsight:clusters"
	// IoT Hub
	TypeIoTHub = "azure:microsoft.devices:iot-hubs"
	// Azure Virtual Desktop
	TypeDVCHostPool         = "azure:microsoft.desktopvirtualization:host-pools"
	TypeDVCApplicationGroup = "azure:microsoft.desktopvirtualization:application-groups"
	TypeDVCWorkspace        = "azure:microsoft.desktopvirtualization:workspaces"
	TypeDVCScalingPlan      = "azure:microsoft.desktopvirtualization:scaling-plans"
	// Service Fabric
	TypeServiceFabricCluster = "azure:microsoft.servicefabric:clusters"
	// Healthcare APIs
	TypeHealthcareAPIsService   = "azure:microsoft.healthcareapis:services"
	TypeHealthcareAPIsWorkspace = "azure:microsoft.healthcareapis:workspaces"
	// Azure VMware Solution
	TypeAVSPrivateCloud = "azure:microsoft.avs:private-clouds"
	// Azure Digital Twins
	TypeDigitalTwinsInstance = "azure:microsoft.digitaltwins:digital-twins-instances"
	// Relay
	TypeRelayNamespace = "azure:microsoft.relay:namespaces"
	// Azure Maps
	TypeMapsAccount = "azure:microsoft.maps:accounts"
	// Communication Services
	TypeCommunicationService      = "azure:microsoft.communication:communication-services"
	TypeCommunicationEmailService = "azure:microsoft.communication:email-services"
	// Storage Sync (Azure File Sync)
	TypeStorageSyncService = "azure:microsoft.storagesync:storage-sync-services"
	// Bot Service
	TypeBotServiceBot = "azure:microsoft.botservice:bot-services"
	// Microsoft Purview
	TypePurviewAccount = "azure:microsoft.purview:accounts"
	// Azure Managed Grafana
	TypeDashboardGrafana = "azure:microsoft.dashboard:grafana"
	// Azure Chaos Studio
	TypeChaosExperiment = "azure:microsoft.chaos:experiments"
	// Coverage sweep: data-services
	TypeRedisEnterpriseCluster          = "azure:microsoft.cache:redis-enterprise"
	TypeCognitiveCommitmentPlan         = "azure:microsoft.cognitiveservices:commitment-plans"
	TypeDatabricksAccessConnector       = "azure:microsoft.databricks:access-connectors"
	TypeDataProtectionResourceGuard     = "azure:microsoft.dataprotection:resource-guards"
	TypeDataReplicationFabric           = "azure:microsoft.datareplication:replication-fabrics"
	TypeDataReplicationVault            = "azure:microsoft.datareplication:replication-vaults"
	TypeCosmosCassandraCluster          = "azure:microsoft.documentdb:cassandra-clusters"
	TypeCosmosRestorableDatabaseAccount = "azure:microsoft.documentdb:restorable-database-accounts"
	TypeEventGridNamespace              = "azure:microsoft.eventgrid:namespaces"
	TypeEventGridPartnerConfiguration   = "azure:microsoft.eventgrid:partner-configurations"
	TypeEventGridPartnerNamespace       = "azure:microsoft.eventgrid:partner-namespaces"
	TypeEventGridPartnerRegistration    = "azure:microsoft.eventgrid:partner-registrations"
	TypeEventGridPartnerTopic           = "azure:microsoft.eventgrid:partner-topics"
	TypeEventHubCluster                 = "azure:microsoft.eventhub:clusters"
	TypeHorizonDBParameterGroup         = "azure:microsoft.horizondb:parameter-groups"
	TypeKeyVaultManagedHSM              = "azure:microsoft.keyvault:managed-hsms"
	TypeMachineLearningRegistry         = "azure:microsoft.machinelearningservices:registries"
	TypeOpInsightsCluster               = "azure:microsoft.operationalinsights:clusters"
	TypeOpsManagementSolution           = "azure:microsoft.operationsmanagement:solutions"
	TypeStorageStorageTask              = "azure:microsoft.storage:storage-tasks"
	TypeStreamAnalyticsCluster          = "azure:microsoft.streamanalytics:clusters"
	TypeSynapsePrivateLinkHub           = "azure:microsoft.synapse:private-link-hubs"

	// Coverage sweep: network (microsoft.network/* via armnetwork/v6)
	TypeNetworkWAFPolicy                 = "azure:microsoft.network:application-gateway-web-application-firewall-policies"
	TypeNetworkApplicationSecurityGroup  = "azure:microsoft.network:application-security-groups"
	TypeNetworkAzureFirewallFqdnTag      = "azure:microsoft.network:azure-firewall-fqdn-tags"
	TypeNetworkAzureFirewall             = "azure:microsoft.network:azure-firewalls"
	TypeNetworkAzureWebCategory          = "azure:microsoft.network:azure-web-categories"
	TypeNetworkBastionHost               = "azure:microsoft.network:bastion-hosts"
	TypeNetworkBgpServiceCommunity       = "azure:microsoft.network:bgp-service-communities"
	TypeNetworkConnection                = "azure:microsoft.network:connections"
	TypeNetworkCustomIPPrefix            = "azure:microsoft.network:custom-ip-prefixes"
	TypeNetworkDdosProtectionPlan        = "azure:microsoft.network:ddos-protection-plans"
	TypeNetworkDscpConfiguration         = "azure:microsoft.network:dscp-configurations"
	TypeNetworkExpressRoutePort          = "azure:microsoft.network:express-route-ports"
	TypeNetworkExpressRoutePortsLocation = "azure:microsoft.network:express-route-ports-locations"
	TypeNetworkExpressRouteServiceProv   = "azure:microsoft.network:express-route-service-providers"
	TypeNetworkFirewallPolicy            = "azure:microsoft.network:firewall-policies"
	TypeNetworkIPAllocation              = "azure:microsoft.network:ip-allocations"
	TypeNetworkIPGroup                   = "azure:microsoft.network:ip-groups"
	TypeNetworkLoadBalancer              = "azure:microsoft.network:load-balancers"
	TypeNetworkLocalNetworkGateway       = "azure:microsoft.network:local-network-gateways"
	TypeNetworkNatGateway                = "azure:microsoft.network:nat-gateways"
	TypeNetworkInterface                 = "azure:microsoft.network:network-interfaces"
	TypeNetworkManagerConnection         = "azure:microsoft.network:network-manager-connections"
	TypeNetworkManager                   = "azure:microsoft.network:network-managers"
	TypeNetworkProfile                   = "azure:microsoft.network:network-profiles"
	TypeNetworkVirtualAppliance          = "azure:microsoft.network:network-virtual-appliances"
	TypeNetworkWatcher                   = "azure:microsoft.network:network-watchers"
	TypeNetworkP2SVPNGateway             = "azure:microsoft.network:p2s-vpn-gateways"
	TypeNetworkPrivateLinkService        = "azure:microsoft.network:private-link-services"
	TypeNetworkPublicIPPrefix            = "azure:microsoft.network:public-ip-prefixes"
	TypeNetworkRouteFilter               = "azure:microsoft.network:route-filters"
	TypeNetworkRouteTable                = "azure:microsoft.network:route-tables"
	TypeNetworkSecurityPartnerProvider   = "azure:microsoft.network:security-partner-providers"
	TypeNetworkServiceEndpointPolicy     = "azure:microsoft.network:service-endpoint-policies"
	TypeNetworkVirtualNetworkTap         = "azure:microsoft.network:virtual-network-taps"
	TypeNetworkVirtualRouter             = "azure:microsoft.network:virtual-routers"
	TypeNetworkVPNServerConfiguration    = "azure:microsoft.network:vpn-server-configurations"
	// Coverage sweep: network — DNS resolver family (microsoft.network/* via armdnsresolver)
	TypeNetworkDNSForwardingRuleset  = "azure:microsoft.network:dns-forwarding-rulesets"
	TypeNetworkDNSResolverDomainList = "azure:microsoft.network:dns-resolver-domain-lists"
	TypeNetworkDNSResolverPolicy     = "azure:microsoft.network:dns-resolver-policies"
	TypeNetworkDNSResolver           = "azure:microsoft.network:dns-resolvers"
	// Coverage sweep: network — Front Door / WAF / network-experiment (microsoft.network/* via armfrontdoor)
	TypeNetworkFrontDoor                  = "azure:microsoft.network:front-doors"
	TypeNetworkFrontDoorWAFManagedRuleset = "azure:microsoft.network:front-door-web-application-firewall-managed-rulesets"
	TypeNetworkFrontDoorWAFPolicy         = "azure:microsoft.network:front-door-web-application-firewall-policies"
	TypeNetworkExperimentProfile          = "azure:microsoft.network:network-experiment-profiles"

	// Coverage sweep: telco/edge
	TypeManagedNetworkFabricACL             = "azure:microsoft.managednetworkfabric:access-control-lists"
	TypeManagedNetworkFabricInternetGwRule  = "azure:microsoft.managednetworkfabric:internet-gateway-rules"
	TypeManagedNetworkFabricInternetGateway = "azure:microsoft.managednetworkfabric:internet-gateways"
	TypeManagedNetworkFabricIPCommunity     = "azure:microsoft.managednetworkfabric:ip-communities"
	TypeManagedNetworkFabricIPExtCommunity  = "azure:microsoft.managednetworkfabric:ip-extended-communities"
	TypeManagedNetworkFabricIPPrefix        = "azure:microsoft.managednetworkfabric:ip-prefixes"
	TypeManagedNetworkFabricL2IsolationDom  = "azure:microsoft.managednetworkfabric:l2-isolation-domains"
	TypeManagedNetworkFabricL3IsolationDom  = "azure:microsoft.managednetworkfabric:l3-isolation-domains"
	TypeManagedNetworkFabricNeighborGroup   = "azure:microsoft.managednetworkfabric:neighbor-groups"
	TypeManagedNetworkFabricNetworkDevice   = "azure:microsoft.managednetworkfabric:network-devices"
	TypeManagedNetworkFabricController      = "azure:microsoft.managednetworkfabric:network-fabric-controllers"
	TypeManagedNetworkFabricPacketBroker    = "azure:microsoft.managednetworkfabric:network-packet-brokers"
	TypeManagedNetworkFabricRack            = "azure:microsoft.managednetworkfabric:network-racks"
	TypeManagedNetworkFabricNetworkTapRule  = "azure:microsoft.managednetworkfabric:network-tap-rules"
	TypeManagedNetworkFabricNetworkTap      = "azure:microsoft.managednetworkfabric:network-taps"
	TypeManagedNetworkFabricRoutePolicy     = "azure:microsoft.managednetworkfabric:route-policies"

	TypeNetworkCloudBareMetalMachine  = "azure:microsoft.networkcloud:bare-metal-machines"
	TypeNetworkCloudServicesNetwork   = "azure:microsoft.networkcloud:cloud-services-networks"
	TypeNetworkCloudClusterManager    = "azure:microsoft.networkcloud:cluster-managers"
	TypeNetworkCloudKubernetesCluster = "azure:microsoft.networkcloud:kubernetes-clusters"
	TypeNetworkCloudL2Network         = "azure:microsoft.networkcloud:l2-networks"
	TypeNetworkCloudL3Network         = "azure:microsoft.networkcloud:l3-networks"
	TypeNetworkCloudRack              = "azure:microsoft.networkcloud:racks"
	TypeNetworkCloudRackSKU           = "azure:microsoft.networkcloud:rack-skus"
	TypeNetworkCloudStorageAppliance  = "azure:microsoft.networkcloud:storage-appliances"
	TypeNetworkCloudTrunkedNetwork    = "azure:microsoft.networkcloud:trunked-networks"
	TypeNetworkCloudVirtualMachine    = "azure:microsoft.networkcloud:virtual-machines"
	TypeNetworkCloudVolume            = "azure:microsoft.networkcloud:volumes"

	TypeEdgeOrderAddress = "azure:microsoft.edgeorder:addresses"
	TypeEdgeOrderOrder   = "azure:microsoft.edgeorder:orders"

	TypePeeringPeerAsn        = "azure:microsoft.peering:peer-asns"
	TypePeeringPeeringService = "azure:microsoft.peering:peering-services"

	TypeEdgeMarketplaceOffer     = "azure:microsoft.edgemarketplace:offers"
	TypeEdgeMarketplacePublisher = "azure:microsoft.edgemarketplace:publishers"
	// Coverage sweep: app/compute/misc
	TypeAppContainersConnectedEnvironment  = "azure:microsoft.app:connected-environments"
	TypeAppContainersJob                   = "azure:microsoft.app:jobs"
	TypeAppContainersSessionPool           = "azure:microsoft.app:session-pools"
	TypeAutomanageBestPractice             = "azure:microsoft.automanage:best-practices"
	TypeAutomanageConfigProfileAssignment  = "azure:microsoft.automanage:configuration-profile-assignments"
	TypeAutomanageServicePrincipal         = "azure:microsoft.automanage:service-principals"
	TypeLargeInstanceStorage               = "azure:microsoft.azurelargeinstance:azure-large-storage-instances"
	TypeResilienceUsagePlan                = "azure:microsoft.azureresiliencemanagement:usage-plans"
	TypeBlueprintBlueprint                 = "azure:microsoft.blueprint:blueprints"
	TypeBlueprintAssignment                = "azure:microsoft.blueprint:blueprint-assignments"
	TypeCDNWAFPolicy                       = "azure:microsoft.cdn:cdn-web-application-firewall-policies"
	TypeCloudHealthHealthModel             = "azure:microsoft.cloudhealth:health-models"
	TypeCodeSigningAccount                 = "azure:microsoft.codesigning:codesigning-accounts"
	TypeContainerServiceFleet              = "azure:microsoft.containerservice:fleets"
	TypeContainerServiceSnapshot           = "azure:microsoft.containerservice:snapshots"
	TypeCustomProvidersResourceProvider    = "azure:microsoft.customproviders:resource-providers"
	TypeDependencyMapMap                   = "azure:microsoft.dependencymap:maps"
	TypeDesktopVirtAppAttachPackage        = "azure:microsoft.desktopvirtualization:app-attach-packages"
	TypeDevCenterNetworkConnection         = "azure:microsoft.devcenter:network-connections"
	TypeDevCenterProject                   = "azure:microsoft.devcenter:projects"
	TypeDeviceRegistryAssetEndpointProfile = "azure:microsoft.deviceregistry:asset-endpoint-profiles"
	TypeDeviceRegistryBillingContainer     = "azure:microsoft.deviceregistry:billing-containers"
	TypeDevicesProvisioningService         = "azure:microsoft.devices:provisioning-services"
	TypeDevTestLabSchedule                 = "azure:microsoft.devtestlab:schedules"
	TypeElasticMonitor                     = "azure:microsoft.elastic:monitors"
	TypeLabServicesLabPlan                 = "azure:microsoft.labservices:lab-plans"
	TypeLogicIntegrationAccount            = "azure:microsoft.logic:integration-accounts"
	TypeLogicIntegrationServiceEnv         = "azure:microsoft.logic:integration-service-environments"
	TypeMaintenanceConfigAssignment        = "azure:microsoft.maintenance:configuration-assignments"
	TypeMaintenancePublicConfiguration     = "azure:microsoft.maintenance:public-maintenance-configurations"
	TypeManagedServicesMarketplaceRegDef   = "azure:microsoft.managedservices:marketplace-registration-definitions"
	TypeManagedServicesRegistrationAssign  = "azure:microsoft.managedservices:registration-assignments"
	TypeOrbitalGeoCatalog                  = "azure:microsoft.orbital:geo-catalogs"
	TypePowerBIDedicatedAutoScaleVCore     = "azure:microsoft.powerbidedicated:auto-scale-vcores"
	TypePowerPlatformAccount               = "azure:microsoft.powerplatform:accounts"
	TypeSaaSApplication                    = "azure:microsoft.saas:applications"
	TypeSaaSResource                       = "azure:microsoft.saas:resources"
	TypeServiceFabricManagedCluster        = "azure:microsoft.servicefabric:managed-clusters"
	TypeSolutionsApplicationDefinition     = "azure:microsoft.solutions:application-definitions"
	TypeSolutionsApplication               = "azure:microsoft.solutions:applications"
	TypeSolutionsJitRequest                = "azure:microsoft.solutions:jit-requests"
	TypeSQLVirtualMachineGroup             = "azure:microsoft.sqlvirtualmachine:sql-virtual-machine-groups"
	TypeVMImageBuilderImageTemplate        = "azure:microsoft.virtualmachineimages:image-templates"
	TypeWorkloadsMonitor                   = "azure:microsoft.workloads:monitors"
)

// azureAPITypeMap maps the lowercase Azure provider resource-type string
// (e.g. "microsoft.compute/virtualmachines") to the disco type constant.
// Update this map whenever a new service scanner is added.
var azureAPITypeMap = map[string]string{
	"microsoft.resources/resourcegroups":                                   TypeResourcesResourceGroup,
	"microsoft.resources/subscriptions":                                    TypeSubscription,
	"microsoft.management/managementgroups":                                TypeManagementGroup,
	"microsoft.quota/quotas":                                               TypeQuotaLimit,
	"microsoft.security/pricings":                                          TypeSecurityPricing,
	"microsoft.authorization/roleassignments":                              TypeAuthorizationRoleAssignment,
	"microsoft.authorization/roledefinitions":                              TypeAuthorizationRoleDefinition,
	"microsoft.managedidentity/userassignedidentities":                     TypeManagedIdentityUserAssigned,
	"microsoft.operationalinsights/workspaces":                             TypeOpInsightsWorkspace,
	"microsoft.containerregistry/registries":                               TypeContainerRegistryRegistry,
	"microsoft.documentdb/databaseaccounts":                                TypeCosmosDatabaseAccount,
	"microsoft.cache/redis":                                                TypeRedisCache,
	"microsoft.app/managedenvironments":                                    TypeAppContainersManagedEnvironment,
	"microsoft.app/containerapps":                                          TypeAppContainersContainerApp,
	"microsoft.containerinstance/containergroups":                          TypeContainerInstanceContainerGroup,
	"microsoft.dbforpostgresql/flexibleservers":                            TypePostgreSQLFlexibleServer,
	"microsoft.dbformysql/flexibleservers":                                 TypeMySQLFlexibleServer,
	"microsoft.network/privateendpoints":                                   TypeNetworkPrivateEndpoint,
	"microsoft.network/expressroutecircuits":                               TypeNetworkExpressRouteCircuit,
	"microsoft.network/virtualwans":                                        TypeNetworkVirtualWAN,
	"microsoft.network/virtualhubs":                                        TypeNetworkVirtualHub,
	"microsoft.network/vpnsites":                                           TypeNetworkVPNSite,
	"microsoft.network/vpngateways":                                        TypeNetworkVPNGateway,
	"microsoft.network/virtualnetworkgateways":                             TypeNetworkVirtualNetworkGW,
	"microsoft.network/expressroutegateways":                               TypeNetworkExpressRouteGateway,
	"microsoft.eventhub/namespaces":                                        TypeEventHubNamespace,
	"microsoft.servicebus/namespaces":                                      TypeServiceBusNamespace,
	"microsoft.eventgrid/topics":                                           TypeEventGridTopic,
	"microsoft.eventgrid/systemtopics":                                     TypeEventGridSystemTopic,
	"microsoft.eventgrid/domains":                                          TypeEventGridDomain,
	"microsoft.eventgrid/eventsubscriptions":                               TypeEventGridEventSubscription,
	"microsoft.network/dnszones":                                           TypeDNSZone,
	"microsoft.network/privatednszones":                                    TypeDNSPrivateZone,
	"microsoft.network/privatednszones/virtualnetworklinks":                TypeDNSPrivateZoneVNetLink,
	"microsoft.network/dnszones/a":                                         TypeDNSRecordSet,
	"microsoft.network/dnszones/aaaa":                                      TypeDNSRecordSet,
	"microsoft.network/dnszones/cname":                                     TypeDNSRecordSet,
	"microsoft.network/dnszones/mx":                                        TypeDNSRecordSet,
	"microsoft.network/dnszones/ns":                                        TypeDNSRecordSet,
	"microsoft.network/dnszones/ptr":                                       TypeDNSRecordSet,
	"microsoft.network/dnszones/srv":                                       TypeDNSRecordSet,
	"microsoft.network/dnszones/txt":                                       TypeDNSRecordSet,
	"microsoft.network/dnszones/caa":                                       TypeDNSRecordSet,
	"microsoft.network/privatednszones/a":                                  TypeDNSPrivateRecordSet,
	"microsoft.network/privatednszones/aaaa":                               TypeDNSPrivateRecordSet,
	"microsoft.network/privatednszones/cname":                              TypeDNSPrivateRecordSet,
	"microsoft.network/privatednszones/mx":                                 TypeDNSPrivateRecordSet,
	"microsoft.network/privatednszones/ptr":                                TypeDNSPrivateRecordSet,
	"microsoft.network/privatednszones/srv":                                TypeDNSPrivateRecordSet,
	"microsoft.network/privatednszones/txt":                                TypeDNSPrivateRecordSet,
	"microsoft.network/applicationgateways":                                TypeNetworkApplicationGateway,
	"microsoft.network/trafficmanagerprofiles":                             TypeNetworkTrafficManagerProfile,
	"microsoft.cdn/profiles":                                               TypeCDNProfile,
	"microsoft.apimanagement/service":                                      TypeAPIManagementService,
	"microsoft.databricks/workspaces":                                      TypeDatabricksWorkspace,
	"microsoft.synapse/workspaces":                                         TypeSynapseWorkspace,
	"microsoft.datafactory/factories":                                      TypeDataFactoryFactory,
	"microsoft.logic/workflows":                                            TypeLogicWorkflow,
	"microsoft.authorization/policydefinitions":                            TypePolicyDefinition,
	"microsoft.authorization/policysetdefinitions":                         TypePolicySetDefinition,
	"microsoft.authorization/policyassignments":                            TypePolicyAssignment,
	"microsoft.compute/virtualmachines":                                    TypeComputeVirtualMachine,
	"microsoft.compute/disks":                                              TypeComputeManagedDisk,
	"microsoft.compute/availabilitysets":                                   TypeComputeAvailabilitySet,
	"microsoft.compute/virtualmachines/extensions":                         TypeComputeVMExtension,
	"microsoft.compute/sshpublickeys":                                      TypeComputeSSHPublicKey,
	"microsoft.compute/proximityplacementgroups":                           TypeComputeProximityPlacementGroup,
	"microsoft.compute/snapshots":                                          TypeComputeSnapshot,
	"microsoft.compute/diskencryptionsets":                                 TypeComputeDiskEncryptionSet,
	"microsoft.compute/diskaccesses":                                       TypeComputeDiskAccess,
	"microsoft.compute/restorepointcollections":                            TypeComputeRestorePointCollection,
	"microsoft.compute/images":                                             TypeComputeImage,
	"microsoft.compute/virtualmachinescalesets":                            TypeComputeVMSS,
	"microsoft.compute/virtualmachinescalesets/extensions":                 TypeComputeVMSSExtension,
	"microsoft.compute/virtualmachinescalesets/virtualmachines":            TypeComputeVMSSVM,
	"microsoft.compute/virtualmachinescalesets/virtualmachines/extensions": TypeComputeVMSSVMExtension,
	"microsoft.compute/galleries":                                          TypeComputeGallery,
	"microsoft.compute/galleries/images":                                   TypeComputeGalleryImage,
	"microsoft.compute/galleries/images/versions":                          TypeComputeGalleryImageVersion,
	"microsoft.compute/galleries/applications":                             TypeComputeGalleryApplication,
	"microsoft.compute/galleries/applications/versions":                    TypeComputeGalleryApplicationVersion,
	"microsoft.compute/galleries/invmaccesscontrolprofiles":                TypeComputeGalleryInVMACP,
	"microsoft.compute/galleries/invmaccesscontrolprofiles/versions":       TypeComputeGalleryInVMACPVersion,
	"microsoft.compute/hostgroups":                                         TypeComputeHostGroup,
	"microsoft.compute/hostgroups/hosts":                                   TypeComputeDedicatedHost,
	"microsoft.compute/capacityreservationgroups":                          TypeComputeCapacityReservationGroup,
	"microsoft.compute/capacityreservationgroups/capacityreservations":     TypeComputeCapacityReservation,
	"microsoft.compute/cloudservices":                                      TypeComputeCloudService,
	"microsoft.compute/cloudservices/roles":                                TypeComputeCloudServiceRole,
	"microsoft.compute/cloudservices/roleinstances":                        TypeComputeCloudServiceRoleInstance,
	"microsoft.network/virtualnetworks":                                    TypeNetworkVirtualNetwork,
	"microsoft.network/virtualnetworks/subnets":                            TypeNetworkSubnet,
	"microsoft.network/networksecuritygroups":                              TypeNetworkSecurityGroup,
	"microsoft.network/publicipaddresses":                                  TypeNetworkPublicIPAddress,
	"microsoft.containerservice/managedclusters":                           TypeContainerServiceManagedCluster,
	"microsoft.keyvault/vaults":                                            TypeKeyVaultVault,
	"microsoft.storage/storageaccounts":                                    TypeStorageStorageAccount,
	"microsoft.databox/jobs":                                               TypeDataBoxJob,
	"microsoft.databoxedge/databoxedgedevices":                             TypeDataBoxEdgeDevice,
	"microsoft.datashare/accounts":                                         TypeDataShareAccount,
	"microsoft.elasticsan/elasticsans":                                     TypeElasticSan,
	"microsoft.fileshares/fileshares":                                      TypeFileSharesFileShare,
	"microsoft.storageactions/storagetasks":                                TypeStorageActionsTask,
	"microsoft.storagecache/caches":                                        TypeStorageCacheCache,
	"microsoft.storagediscovery/storagediscoveryworkspaces":                TypeStorageDiscoveryWorkspace,
	"microsoft.storagemover/storagemovers":                                 TypeStorageMover,
	"microsoft.analysisservices/servers":                                   TypeAnalysisServicesServer,
	"microsoft.databasewatcher/watchers":                                   TypeDatabaseWatcher,
	"microsoft.datamigration/services":                                     TypeDataMigrationService,
	"microsoft.horizondb/clusters":                                         TypeHorizonDBCluster,
	"microsoft.documentdb/mongoclusters":                                   TypeMongoCluster,
	"microsoft.dbforpostgresql/servergroupsv2":                             TypePostgreSQLServerGroupV2,
	"microsoft.powerbidedicated/capacities":                                TypePowerBIDedicatedCapacity,
	"microsoft.azurearcdata/datacontrollers":                               TypeAzureArcDataController,
	"microsoft.azurestackhci/clusters":                                     TypeAzureStackHCICluster,
	"microsoft.azurestackhci/galleryimages":                                TypeAzureStackHCIGalleryImage,
	"microsoft.azurestackhci/logicalnetworks":                              TypeAzureStackHCILogicalNetwork,
	"microsoft.azurestackhci/marketplacegalleryimages":                     TypeAzureStackHCIMarketplaceGalleryImage,
	"microsoft.azurestackhci/networkinterfaces":                            TypeAzureStackHCINetworkInterface,
	"microsoft.azurestackhci/networksecuritygroups":                        TypeAzureStackHCINetworkSecurityGroup,
	"microsoft.azurestackhci/storagecontainers":                            TypeAzureStackHCIStorageContainer,
	"microsoft.azurestackhci/virtualharddisks":                             TypeAzureStackHCIVirtualHardDisk,
	"microsoft.connectedvmwarevsphere/clusters":                            TypeConnectedVMwareCluster,
	"microsoft.connectedvmwarevsphere/datastores":                          TypeConnectedVMwareDatastore,
	"microsoft.connectedvmwarevsphere/hosts":                               TypeConnectedVMwareHost,
	"microsoft.connectedvmwarevsphere/resourcepools":                       TypeConnectedVMwareResourcePool,
	"microsoft.connectedvmwarevsphere/virtualmachinetemplates":             TypeConnectedVMwareVMTemplate,
	"microsoft.connectedvmwarevsphere/virtualnetworks":                     TypeConnectedVMwareVirtualNetwork,
	"microsoft.scvmm/clouds":                                               TypeScVmmCloud,
	"microsoft.scvmm/availabilitysets":                                     TypeScVmmAvailabilitySet,
	"microsoft.scvmm/virtualmachinetemplates":                              TypeScVmmVMTemplate,
	"microsoft.scvmm/virtualnetworks":                                      TypeScVmmVirtualNetwork,
	"microsoft.azurearcdata/postgresinstances":                             TypeAzureArcDataPostgres,
	"microsoft.azurearcdata/sqlmanagedinstances":                           TypeAzureArcDataSQLManagedInstance,
	"microsoft.azurearcdata/sqlserverinstances":                            TypeAzureArcDataSQLServerInstance,
	"microsoft.hybridcompute/privatelinkscopes":                            TypeHybridComputePrivateLinkScope,
	"microsoft.hybridconnectivity/publiccloudconnectors":                   TypeHybridConnectivityPublicCloud,
	"microsoft.hybridnetwork/devices":                                      TypeHybridNetworkDevice,
	"microsoft.connectedcache/enterprisemcccustomers":                      TypeConnectedCacheEnterpriseCustomer,
	"microsoft.connectedcache/enterprisecustomers":                         TypeConnectedCacheEnterpriseCustomer,
	"microsoft.connectedcache/ispcustomers":                                TypeConnectedCacheIspCustomer,
	"microsoft.connectedvmwarevsphere/vcenters":                            TypeConnectedVMwareVCenter,
	"microsoft.extendedlocation/customlocations":                           TypeCustomLocation,
	"microsoft.hybridcompute/machines":                                     TypeHybridComputeMachine,
	"microsoft.hybridcontainerservice/virtualnetworks":                     TypeHybridContainerVirtualNetwork,
	"microsoft.kubernetes/connectedclusters":                               TypeKubernetesConnectedCluster,
	"microsoft.resourceconnector/appliances":                               TypeResourceConnectorAppliance,
	"microsoft.scvmm/vmmservers":                                           TypeScVmmServer,
	"microsoft.edgezones/extendedzones":                                    TypeEdgeZonesExtendedZone,
	"microsoft.hybridnetwork/networkfunctions":                             TypeHybridNetworkFunction,
	"microsoft.managednetworkfabric/networkfabrics":                        TypeManagedNetworkFabric,
	"microsoft.networkcloud/clusters":                                      TypeNetworkCloudCluster,
	"microsoft.networkfunction/azuretrafficcollectors":                     TypeNetworkFunctionTrafficCollector,
	"microsoft.peering/peerings":                                           TypePeeringPeering,
	"microsoft.servicenetworking/trafficcontrollers":                       TypeServiceNetworkingTrafficController,
	"microsoft.azurefleet/fleets":                                          TypeComputeFleet,
	"microsoft.azurelargeinstance/azurelargeinstances":                     TypeLargeInstance,
	"microsoft.baremetalinfrastructure/baremetalinstances":                 TypeBareMetalInstance,
	"microsoft.devopsinfrastructure/pools":                                 TypeDevOpsInfrastructurePool,
	"microsoft.sqlvirtualmachine/sqlvirtualmachines":                       TypeSQLVirtualMachine,
	"microsoft.standbypool/standbyvirtualmachinepools":                     TypeStandbyVMPool,
	"microsoft.standbypool/standbycontainergrouppools":                     TypeStandbyContainerGroupPool,
	"microsoft.workloads/sapvirtualinstances":                              TypeWorkloadsSAPVirtualInstance,
	"microsoft.deviceregistry/assets":                                      TypeDeviceRegistryAsset,
	"microsoft.deviceupdate/accounts":                                      TypeDeviceUpdateAccount,
	"microsoft.edgeorder/orderitems":                                       TypeEdgeOrderItem,
	"microsoft.iotcentral/iotapps":                                         TypeIoTCentralApp,
	"microsoft.iotfirmwaredefense/workspaces":                              TypeIoTFirmwareDefenseWorkspace,
	"microsoft.iotoperations/instances":                                    TypeIoTOperationsInstance,
	"microsoft.healthbot/healthbots":                                       TypeHealthBot,
	"microsoft.healthdataaiservices/deidservices":                          TypeHealthDataAIDeidService,
	"microsoft.loadtestservice/loadtests":                                  TypeLoadTest,
	"microsoft.onlineexperimentation/workspaces":                           TypeOnlineExperimentationWorkspace,
	"microsoft.azureplaywrightservice/accounts":                            TypePlaywrightAccount,
	"microsoft.quantum/workspaces":                                         TypeQuantumWorkspace,
	"microsoft.apicenter/services":                                         TypeAPICenterService,
	"microsoft.attestation/attestationproviders":                           TypeAttestationProvider,
	"microsoft.automanage/configurationprofiles":                           TypeAutomanageConfigProfile,
	"microsoft.azuresphere/catalogs":                                       TypeAzureSphereCatalog,
	"microsoft.certificateregistration/certificateorders":                  TypeCertificateOrder,
	"microsoft.confidentialledger/ledgers":                                 TypeConfidentialLedger,
	"microsoft.devcenter/devcenters":                                       TypeDevCenter,
	"microsoft.devhub/workflows":                                           TypeDevHubWorkflow,
	"microsoft.devtestlab/labs":                                            TypeDevTestLab,
	"microsoft.domainregistration/domains":                                 TypeDomain,
	"microsoft.durabletask/schedulers":                                     TypeDurableTaskScheduler,
	"microsoft.fabric/capacities":                                          TypeFabricCapacity,
	"microsoft.fluidrelay/fluidrelayservers":                               TypeFluidRelayServer,
	"microsoft.graphservices/accounts":                                     TypeGraphServicesAccount,
	"microsoft.hardwaresecuritymodules/dedicatedhsms":                      TypeDedicatedHsm,
	"microsoft.integrationspaces/spaces":                                   TypeIntegrationSpace,
	"microsoft.labservices/labs":                                           TypeLabServicesLab,
	"microsoft.maintenance/maintenanceconfigurations":                      TypeMaintenanceConfiguration,
	"microsoft.migrate/assessmentprojects":                                 TypeMigrateAssessmentProject,
	"microsoft.notificationhubs/namespaces":                                TypeNotificationHubNamespace,
	"microsoft.powerplatform/enterprisepolicies":                           TypePowerPlatformEnterprisePolicy,
	"microsoft.redhatopenshift/openshiftclusters":                          TypeOpenShiftCluster,
	"microsoft.offazurespringboot/springbootsites":                         TypeSpringbootSite,
	"microsoft.managedservices/registrationdefinitions":                    TypeManagedServicesRegistrationDefinition,
	"microsoft.policyinsights/remediations":                                TypePolicyInsightsRemediation,
	"microsoft.policyinsights/attestations":                                TypePolicyInsightsAttestation,
	"microsoft.sql/servers":                                                TypeSQLServer,
	"microsoft.sql/servers/databases":                                      TypeSQLDatabase,
	"microsoft.sql/instancepools":                                          TypeSQLInstancePool,
	"microsoft.sql/virtualclusters":                                        TypeSQLVirtualCluster,
	"microsoft.sql/managedinstances":                                       TypeSQLManagedInstance,
	"microsoft.sql/managedinstances/databases":                             TypeSQLManagedDatabase,
	"microsoft.sql/managedinstances/administrators":                        TypeSQLManagedInstanceAdmin,
	"microsoft.sql/managedinstances/vulnerabilityassessments":              TypeSQLManagedInstanceVA,
	"microsoft.sql/managedinstances/databases/vulnerabilityassessments":    TypeSQLManagedDatabaseVA,
	"microsoft.sql/managedinstances/keys":                                  TypeSQLManagedInstanceKey,
	"microsoft.sql/managedinstances/encryptionprotector":                   TypeSQLManagedInstanceEP,
	"microsoft.sql/managedinstances/privateendpointconnections":            TypeSQLManagedInstancePEC,
	"microsoft.sql/managedinstances/securityalertpolicies":                 TypeSQLManagedServerSecurityAlert,
	"microsoft.sql/managedinstances/databases/transparentdataencryption":   TypeSQLManagedDatabaseTDE,
	"microsoft.sql/managedinstances/databases/securityalertpolicies":       TypeSQLManagedDatabaseSecAlert,
	"microsoft.sql/servers/keys":                                           TypeSQLServerKey,
	"microsoft.sql/servers/encryptionprotector":                            TypeSQLEncryptionProtector,
	"microsoft.sql/servers/administrators":                                 TypeSQLServerAdministrator,
	"microsoft.sql/servers/auditingsettings":                               TypeSQLServerAuditingSettings,
	"microsoft.sql/servers/extendedauditingsettings":                       TypeSQLServerExtAuditingSettings,
	"microsoft.sql/servers/devopsauditsettings":                            TypeSQLServerDevOpsAuditSettings,
	"microsoft.sql/servers/securityalertpolicies":                          TypeSQLServerSecurityAlert,
	"microsoft.sql/servers/advancedthreatprotectionsettings":               TypeSQLServerAdvancedThreatProt,
	"microsoft.sql/servers/vulnerabilityassessments":                       TypeSQLServerVulnAssessment,
	"microsoft.sql/servers/elasticpools":                                   TypeSQLElasticPool,
	"microsoft.sql/servers/failovergroups":                                 TypeSQLFailoverGroup,
	"microsoft.sql/servers/dnsaliases":                                     TypeSQLServerDNSAlias,
	"microsoft.sql/servers/virtualnetworkrules":                            TypeSQLVirtualNetworkRule,
	"microsoft.sql/servers/jobagents":                                      TypeSQLJobAgent,
	"microsoft.sql/servers/syncagents":                                     TypeSQLSyncAgent,
	"microsoft.sql/servers/restorabledroppeddatabases":                     TypeSQLRestorableDroppedDB,
	"microsoft.sql/servers/databases/transparentdataencryption":            TypeSQLDBTransparentDataEnc,
	"microsoft.sql/servers/databases/securityalertpolicies":                TypeSQLDBSecurityAlert,
	"microsoft.sql/servers/databases/advancedthreatprotectionsettings":     TypeSQLDBAdvancedThreatProt,
	"microsoft.sql/servers/databases/auditingsettings":                     TypeSQLDBAuditingSettings,
	"microsoft.sql/servers/databases/vulnerabilityassessments":             TypeSQLDBVulnAssessment,
	"microsoft.sql/servers/databases/syncgroups":                           TypeSQLSyncGroup,
	"microsoft.sql/servers/databases/replicationlinks":                     TypeSQLReplicationLink,
	"microsoft.sql/servers/databases/workloadgroups":                       TypeSQLWorkloadGroup,
	"microsoft.sql/servers/databases/geobackuppolicies":                    TypeSQLGeoBackupPolicy,
	"microsoft.sql/servers/databases/ledgerdigestuploads":                  TypeSQLLedgerDigestUpload,
	// App Service / Microsoft.Web
	"microsoft.web/serverfarms":                        TypeAppServiceServerFarm,
	"microsoft.web/sites":                              TypeAppServiceSite,
	"microsoft.web/sites/slots":                        TypeAppServiceSiteSlot,
	"microsoft.web/hostingenvironments":                TypeAppServiceEnvironment,
	"microsoft.web/hostingenvironments/workerpools":    TypeAppServiceEnvironmentWorkerPool,
	"microsoft.web/hostingenvironments/multirolepools": TypeAppServiceEnvironmentMultiRolePool,
	"microsoft.web/kubeenvironments":                   TypeAppServiceKubeEnvironment,
	"microsoft.web/staticsites":                        TypeAppServiceStaticSite,
	"microsoft.web/staticsites/builds":                 TypeAppServiceStaticSiteBuild,
	"microsoft.web/certificates":                       TypeAppServiceCertificate,
	// Cognitive Services / App Configuration / Search / Recovery Services / Data Protection / Batch
	"microsoft.cognitiveservices/accounts":           TypeCognitiveServicesAccount,
	"microsoft.appconfiguration/configurationstores": TypeAppConfigurationStore,
	"microsoft.search/searchservices":                TypeSearchService,
	"microsoft.recoveryservices/vaults":              TypeRecoveryServicesVault,
	"microsoft.dataprotection/backupvaults":          TypeDataProtectionBackupVault,
	"microsoft.batch/batchaccounts":                  TypeBatchAccount,
	// Kusto / NetApp / Spring Apps / ML / Automation / SignalR / Web PubSub / Stream Analytics / HDInsight
	"microsoft.kusto/clusters":                     TypeKustoCluster,
	"microsoft.netapp/netappaccounts":              TypeNetAppAccount,
	"microsoft.appplatform/spring":                 TypeAppPlatformService,
	"microsoft.machinelearningservices/workspaces": TypeMachineLearningWorkspace,
	"microsoft.automation/automationaccounts":      TypeAutomationAccount,
	"microsoft.signalrservice/signalr":             TypeSignalR,
	"microsoft.signalrservice/webpubsub":           TypeWebPubSub,
	"microsoft.streamanalytics/streamingjobs":      TypeStreamAnalyticsJob,
	"microsoft.hdinsight/clusters":                 TypeHDInsightCluster,
	// IoT Hub / AVD / Service Fabric / Healthcare APIs / AVS / Digital Twins /
	// Relay / Maps / Communication / Storage Sync / Bot Service / Purview /
	// Managed Grafana / Chaos Studio
	"microsoft.devices/iothubs":                         TypeIoTHub,
	"microsoft.desktopvirtualization/hostpools":         TypeDVCHostPool,
	"microsoft.desktopvirtualization/applicationgroups": TypeDVCApplicationGroup,
	"microsoft.desktopvirtualization/workspaces":        TypeDVCWorkspace,
	"microsoft.desktopvirtualization/scalingplans":      TypeDVCScalingPlan,
	"microsoft.servicefabric/clusters":                  TypeServiceFabricCluster,
	"microsoft.healthcareapis/services":                 TypeHealthcareAPIsService,
	"microsoft.healthcareapis/workspaces":               TypeHealthcareAPIsWorkspace,
	"microsoft.avs/privateclouds":                       TypeAVSPrivateCloud,
	"microsoft.digitaltwins/digitaltwinsinstances":      TypeDigitalTwinsInstance,
	"microsoft.relay/namespaces":                        TypeRelayNamespace,
	"microsoft.maps/accounts":                           TypeMapsAccount,
	"microsoft.communication/communicationservices":     TypeCommunicationService,
	"microsoft.communication/emailservices":             TypeCommunicationEmailService,
	"microsoft.storagesync/storagesyncservices":         TypeStorageSyncService,
	"microsoft.botservice/botservices":                  TypeBotServiceBot,
	"microsoft.purview/accounts":                        TypePurviewAccount,
	"microsoft.dashboard/grafana":                       TypeDashboardGrafana,
	"microsoft.chaos/experiments":                       TypeChaosExperiment,
	// Coverage sweep: data-services
	"microsoft.cache/redisenterprise":                 TypeRedisEnterpriseCluster,
	"microsoft.cognitiveservices/commitmentplans":     TypeCognitiveCommitmentPlan,
	"microsoft.databricks/accessconnectors":           TypeDatabricksAccessConnector,
	"microsoft.dataprotection/resourceguards":         TypeDataProtectionResourceGuard,
	"microsoft.datareplication/replicationfabrics":    TypeDataReplicationFabric,
	"microsoft.datareplication/replicationvaults":     TypeDataReplicationVault,
	"microsoft.documentdb/cassandraclusters":          TypeCosmosCassandraCluster,
	"microsoft.documentdb/restorabledatabaseaccounts": TypeCosmosRestorableDatabaseAccount,
	"microsoft.eventgrid/namespaces":                  TypeEventGridNamespace,
	"microsoft.eventgrid/partnerconfigurations":       TypeEventGridPartnerConfiguration,
	"microsoft.eventgrid/partnernamespaces":           TypeEventGridPartnerNamespace,
	"microsoft.eventgrid/partnerregistrations":        TypeEventGridPartnerRegistration,
	"microsoft.eventgrid/partnertopics":               TypeEventGridPartnerTopic,
	"microsoft.eventhub/clusters":                     TypeEventHubCluster,
	"microsoft.horizondb/parametergroups":             TypeHorizonDBParameterGroup,
	"microsoft.keyvault/managedhsms":                  TypeKeyVaultManagedHSM,
	"microsoft.machinelearningservices/registries":    TypeMachineLearningRegistry,
	"microsoft.operationalinsights/clusters":          TypeOpInsightsCluster,
	"microsoft.operationsmanagement/solutions":        TypeOpsManagementSolution,
	"microsoft.storage/storagetasks":                  TypeStorageStorageTask,
	"microsoft.streamanalytics/clusters":              TypeStreamAnalyticsCluster,
	"microsoft.synapse/privatelinkhubs":               TypeSynapsePrivateLinkHub,

	// Coverage sweep: network (microsoft.network/*)
	"microsoft.network/applicationgatewaywebapplicationfirewallpolicies": TypeNetworkWAFPolicy,
	"microsoft.network/applicationsecuritygroups":                        TypeNetworkApplicationSecurityGroup,
	"microsoft.network/azurefirewallfqdntags":                            TypeNetworkAzureFirewallFqdnTag,
	"microsoft.network/azurefirewalls":                                   TypeNetworkAzureFirewall,
	"microsoft.network/azurewebcategories":                               TypeNetworkAzureWebCategory,
	"microsoft.network/bastionhosts":                                     TypeNetworkBastionHost,
	"microsoft.network/bgpservicecommunities":                            TypeNetworkBgpServiceCommunity,
	"microsoft.network/connections":                                      TypeNetworkConnection,
	"microsoft.network/customipprefixes":                                 TypeNetworkCustomIPPrefix,
	"microsoft.network/ddosprotectionplans":                              TypeNetworkDdosProtectionPlan,
	"microsoft.network/dscpconfigurations":                               TypeNetworkDscpConfiguration,
	"microsoft.network/expressrouteports":                                TypeNetworkExpressRoutePort,
	"microsoft.network/expressrouteportslocations":                       TypeNetworkExpressRoutePortsLocation,
	"microsoft.network/expressrouteserviceproviders":                     TypeNetworkExpressRouteServiceProv,
	"microsoft.network/firewallpolicies":                                 TypeNetworkFirewallPolicy,
	"microsoft.network/ipallocations":                                    TypeNetworkIPAllocation,
	"microsoft.network/ipgroups":                                         TypeNetworkIPGroup,
	"microsoft.network/loadbalancers":                                    TypeNetworkLoadBalancer,
	"microsoft.network/localnetworkgateways":                             TypeNetworkLocalNetworkGateway,
	"microsoft.network/natgateways":                                      TypeNetworkNatGateway,
	"microsoft.network/networkinterfaces":                                TypeNetworkInterface,
	"microsoft.network/networkmanagerconnections":                        TypeNetworkManagerConnection,
	"microsoft.network/networkmanagers":                                  TypeNetworkManager,
	"microsoft.network/networkprofiles":                                  TypeNetworkProfile,
	"microsoft.network/networkvirtualappliances":                         TypeNetworkVirtualAppliance,
	"microsoft.network/networkwatchers":                                  TypeNetworkWatcher,
	"microsoft.network/p2svpngateways":                                   TypeNetworkP2SVPNGateway,
	"microsoft.network/privatelinkservices":                              TypeNetworkPrivateLinkService,
	"microsoft.network/publicipprefixes":                                 TypeNetworkPublicIPPrefix,
	"microsoft.network/routefilters":                                     TypeNetworkRouteFilter,
	"microsoft.network/routetables":                                      TypeNetworkRouteTable,
	"microsoft.network/securitypartnerproviders":                         TypeNetworkSecurityPartnerProvider,
	"microsoft.network/serviceendpointpolicies":                          TypeNetworkServiceEndpointPolicy,
	"microsoft.network/virtualnetworktaps":                               TypeNetworkVirtualNetworkTap,
	"microsoft.network/virtualrouters":                                   TypeNetworkVirtualRouter,
	"microsoft.network/vpnserverconfigurations":                          TypeNetworkVPNServerConfiguration,
	"microsoft.network/dnsforwardingrulesets":                            TypeNetworkDNSForwardingRuleset,
	"microsoft.network/dnsresolverdomainlists":                           TypeNetworkDNSResolverDomainList,
	"microsoft.network/dnsresolverpolicies":                              TypeNetworkDNSResolverPolicy,
	"microsoft.network/dnsresolvers":                                     TypeNetworkDNSResolver,
	"microsoft.network/frontdoors":                                       TypeNetworkFrontDoor,
	"microsoft.network/frontdoorwebapplicationfirewallmanagedrulesets":   TypeNetworkFrontDoorWAFManagedRuleset,
	"microsoft.network/frontdoorwebapplicationfirewallpolicies":          TypeNetworkFrontDoorWAFPolicy,
	"microsoft.network/networkexperimentprofiles":                        TypeNetworkExperimentProfile,
	// Coverage sweep: telco/edge
	"microsoft.managednetworkfabric/accesscontrollists":       TypeManagedNetworkFabricACL,
	"microsoft.managednetworkfabric/internetgatewayrules":     TypeManagedNetworkFabricInternetGwRule,
	"microsoft.managednetworkfabric/internetgateways":         TypeManagedNetworkFabricInternetGateway,
	"microsoft.managednetworkfabric/ipcommunities":            TypeManagedNetworkFabricIPCommunity,
	"microsoft.managednetworkfabric/ipextendedcommunities":    TypeManagedNetworkFabricIPExtCommunity,
	"microsoft.managednetworkfabric/ipprefixes":               TypeManagedNetworkFabricIPPrefix,
	"microsoft.managednetworkfabric/l2isolationdomains":       TypeManagedNetworkFabricL2IsolationDom,
	"microsoft.managednetworkfabric/l3isolationdomains":       TypeManagedNetworkFabricL3IsolationDom,
	"microsoft.managednetworkfabric/neighborgroups":           TypeManagedNetworkFabricNeighborGroup,
	"microsoft.managednetworkfabric/networkdevices":           TypeManagedNetworkFabricNetworkDevice,
	"microsoft.managednetworkfabric/networkfabriccontrollers": TypeManagedNetworkFabricController,
	"microsoft.managednetworkfabric/networkpacketbrokers":     TypeManagedNetworkFabricPacketBroker,
	"microsoft.managednetworkfabric/networkracks":             TypeManagedNetworkFabricRack,
	"microsoft.managednetworkfabric/networktaprules":          TypeManagedNetworkFabricNetworkTapRule,
	"microsoft.managednetworkfabric/networktaps":              TypeManagedNetworkFabricNetworkTap,
	"microsoft.managednetworkfabric/routepolicies":            TypeManagedNetworkFabricRoutePolicy,
	"microsoft.networkcloud/baremetalmachines":                TypeNetworkCloudBareMetalMachine,
	"microsoft.networkcloud/cloudservicesnetworks":            TypeNetworkCloudServicesNetwork,
	"microsoft.networkcloud/clustermanagers":                  TypeNetworkCloudClusterManager,
	"microsoft.networkcloud/kubernetesclusters":               TypeNetworkCloudKubernetesCluster,
	"microsoft.networkcloud/l2networks":                       TypeNetworkCloudL2Network,
	"microsoft.networkcloud/l3networks":                       TypeNetworkCloudL3Network,
	"microsoft.networkcloud/racks":                            TypeNetworkCloudRack,
	"microsoft.networkcloud/rackskus":                         TypeNetworkCloudRackSKU,
	"microsoft.networkcloud/storageappliances":                TypeNetworkCloudStorageAppliance,
	"microsoft.networkcloud/trunkednetworks":                  TypeNetworkCloudTrunkedNetwork,
	"microsoft.networkcloud/virtualmachines":                  TypeNetworkCloudVirtualMachine,
	"microsoft.networkcloud/volumes":                          TypeNetworkCloudVolume,
	"microsoft.edgeorder/addresses":                           TypeEdgeOrderAddress,
	"microsoft.edgeorder/orders":                              TypeEdgeOrderOrder,
	"microsoft.peering/peerasns":                              TypePeeringPeerAsn,
	"microsoft.peering/peeringservices":                       TypePeeringPeeringService,
	"microsoft.edgemarketplace/offers":                        TypeEdgeMarketplaceOffer,
	"microsoft.edgemarketplace/publishers":                    TypeEdgeMarketplacePublisher,
	// Coverage sweep: app/compute/misc
	"microsoft.app/connectedenvironments":                          TypeAppContainersConnectedEnvironment,
	"microsoft.app/jobs":                                           TypeAppContainersJob,
	"microsoft.app/sessionpools":                                   TypeAppContainersSessionPool,
	"microsoft.automanage/bestpractices":                           TypeAutomanageBestPractice,
	"microsoft.automanage/configurationprofileassignments":         TypeAutomanageConfigProfileAssignment,
	"microsoft.automanage/serviceprincipals":                       TypeAutomanageServicePrincipal,
	"microsoft.azurelargeinstance/azurelargestorageinstances":      TypeLargeInstanceStorage,
	"microsoft.azureresiliencemanagement/usageplans":               TypeResilienceUsagePlan,
	"microsoft.blueprint/blueprints":                               TypeBlueprintBlueprint,
	"microsoft.blueprint/blueprintassignments":                     TypeBlueprintAssignment,
	"microsoft.cdn/cdnwebapplicationfirewallpolicies":              TypeCDNWAFPolicy,
	"microsoft.cloudhealth/healthmodels":                           TypeCloudHealthHealthModel,
	"microsoft.codesigning/codesigningaccounts":                    TypeCodeSigningAccount,
	"microsoft.containerservice/fleets":                            TypeContainerServiceFleet,
	"microsoft.containerservice/snapshots":                         TypeContainerServiceSnapshot,
	"microsoft.customproviders/resourceproviders":                  TypeCustomProvidersResourceProvider,
	"microsoft.dependencymap/maps":                                 TypeDependencyMapMap,
	"microsoft.desktopvirtualization/appattachpackages":            TypeDesktopVirtAppAttachPackage,
	"microsoft.devcenter/networkconnections":                       TypeDevCenterNetworkConnection,
	"microsoft.devcenter/projects":                                 TypeDevCenterProject,
	"microsoft.deviceregistry/assetendpointprofiles":               TypeDeviceRegistryAssetEndpointProfile,
	"microsoft.deviceregistry/billingcontainers":                   TypeDeviceRegistryBillingContainer,
	"microsoft.devices/provisioningservices":                       TypeDevicesProvisioningService,
	"microsoft.devtestlab/schedules":                               TypeDevTestLabSchedule,
	"microsoft.elastic/monitors":                                   TypeElasticMonitor,
	"microsoft.labservices/labplans":                               TypeLabServicesLabPlan,
	"microsoft.logic/integrationaccounts":                          TypeLogicIntegrationAccount,
	"microsoft.logic/integrationserviceenvironments":               TypeLogicIntegrationServiceEnv,
	"microsoft.maintenance/configurationassignments":               TypeMaintenanceConfigAssignment,
	"microsoft.maintenance/publicmaintenanceconfigurations":        TypeMaintenancePublicConfiguration,
	"microsoft.managedservices/marketplaceregistrationdefinitions": TypeManagedServicesMarketplaceRegDef,
	"microsoft.managedservices/registrationassignments":            TypeManagedServicesRegistrationAssign,
	"microsoft.orbital/geocatalogs":                                TypeOrbitalGeoCatalog,
	"microsoft.powerbidedicated/autoscalevcores":                   TypePowerBIDedicatedAutoScaleVCore,
	"microsoft.powerplatform/accounts":                             TypePowerPlatformAccount,
	"microsoft.saas/applications":                                  TypeSaaSApplication,
	"microsoft.saas/resources":                                     TypeSaaSResource,
	"microsoft.servicefabric/managedclusters":                      TypeServiceFabricManagedCluster,
	"microsoft.solutions/applicationdefinitions":                   TypeSolutionsApplicationDefinition,
	"microsoft.solutions/applications":                             TypeSolutionsApplication,
	"microsoft.solutions/jitrequests":                              TypeSolutionsJitRequest,
	"microsoft.sqlvirtualmachine/sqlvirtualmachinegroups":          TypeSQLVirtualMachineGroup,
	"microsoft.virtualmachineimages/imagetemplates":                TypeVMImageBuilderImageTemplate,
	"microsoft.workloads/monitors":                                 TypeWorkloadsMonitor,
}
