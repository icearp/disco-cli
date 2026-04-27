package azure

// Resource type constants for all Azure resource types discovered by this provider.
// Using constants prevents typos in scanner and resolver files — a mismatched
// string would create orphan resources with an undeclared type.
const (
	// Resource Manager
	TypeResourcesResourceGroup = "azure:microsoft.resources:resource-group"
	// Authorization (RBAC)
	TypeAuthorizationRoleAssignment = "azure:microsoft.authorization:role-assignment"
	TypeAuthorizationRoleDefinition = "azure:microsoft.authorization:role-definition"
	// Managed Identity (user-assigned)
	TypeManagedIdentityUserAssigned = "azure:microsoft.managedidentity:user-assigned-identity"
	// Log Analytics
	TypeOpInsightsWorkspace = "azure:microsoft.operationalinsights:workspace"
	// Container Registry
	TypeContainerRegistryRegistry = "azure:microsoft.containerregistry:registry"
	// Cosmos DB
	TypeCosmosDatabaseAccount = "azure:microsoft.documentdb:database-account"
	// Redis Cache
	TypeRedisCache = "azure:microsoft.cache:redis"
	// Container Apps + Container Instances
	TypeAppContainersManagedEnvironment = "azure:microsoft.app:managed-environment"
	TypeAppContainersContainerApp       = "azure:microsoft.app:container-app"
	TypeContainerInstanceContainerGroup = "azure:microsoft.containerinstance:container-group"
	// Database flexible servers
	TypePostgreSQLFlexibleServer = "azure:microsoft.dbforpostgresql:flexible-server"
	TypeMySQLFlexibleServer      = "azure:microsoft.dbformysql:flexible-server"
	// Private Endpoints
	TypeNetworkPrivateEndpoint = "azure:microsoft.network:private-endpoint"
	// Compute — vms
	TypeComputeVirtualMachine          = "azure:microsoft.compute:virtual-machine"
	TypeComputeVMExtension             = "azure:microsoft.compute:virtual-machines/extensions"
	TypeComputeAvailabilitySet         = "azure:microsoft.compute:availability-sets"
	TypeComputeProximityPlacementGroup = "azure:microsoft.compute:proximity-placement-groups"
	TypeComputeSSHPublicKey            = "azure:microsoft.compute:ssh-public-keys"
	TypeComputeRestorePointCollection  = "azure:microsoft.compute:restore-point-collections"
	// Compute — disks
	TypeComputeManagedDisk       = "azure:microsoft.compute:disk"
	TypeComputeSnapshot          = "azure:microsoft.compute:snapshots"
	TypeComputeDiskEncryptionSet = "azure:microsoft.compute:disk-encryption-sets"
	TypeComputeDiskAccess        = "azure:microsoft.compute:disk-accesses"
	// Compute — images
	TypeComputeImage = "azure:microsoft.compute:images"
	// Compute — vmss
	TypeComputeVMSS            = "azure:microsoft.compute:virtual-machine-scale-sets"
	TypeComputeVMSSExtension   = "azure:microsoft.compute:virtual-machine-scale-sets/extensions"
	TypeComputeVMSSVM          = "azure:microsoft.compute:virtual-machine-scale-sets/virtual-machines"
	TypeComputeVMSSVMExtension = "azure:microsoft.compute:virtual-machine-scale-sets/virtual-machines/extensions"
	// Compute — galleries
	TypeComputeGallery                   = "azure:microsoft.compute:galleries"
	TypeComputeGalleryImage              = "azure:microsoft.compute:galleries/images"
	TypeComputeGalleryImageVersion       = "azure:microsoft.compute:galleries/images/versions"
	TypeComputeGalleryApplication        = "azure:microsoft.compute:galleries/applications"
	TypeComputeGalleryApplicationVersion = "azure:microsoft.compute:galleries/applications/versions"
	TypeComputeGalleryInVMACP            = "azure:microsoft.compute:galleries/in-vm-access-control-profiles"
	TypeComputeGalleryInVMACPVersion     = "azure:microsoft.compute:galleries/in-vm-access-control-profiles/versions"
	// Compute — dedicated
	TypeComputeHostGroup                = "azure:microsoft.compute:host-groups"
	TypeComputeDedicatedHost            = "azure:microsoft.compute:host-groups/hosts"
	TypeComputeCapacityReservationGroup = "azure:microsoft.compute:capacity-reservation-groups"
	TypeComputeCapacityReservation      = "azure:microsoft.compute:capacity-reservation-groups/capacity-reservations"
	// Compute — cloud-services
	TypeComputeCloudService             = "azure:microsoft.compute:cloud-services"
	TypeComputeCloudServiceRole         = "azure:microsoft.compute:cloud-services/roles"
	TypeComputeCloudServiceRoleInstance = "azure:microsoft.compute:cloud-services/role-instances"
	// Network
	TypeNetworkVirtualNetwork  = "azure:microsoft.network:virtual-network"
	TypeNetworkSubnet          = "azure:microsoft.network:subnet"
	TypeNetworkSecurityGroup   = "azure:microsoft.network:network-security-group"
	TypeNetworkPublicIPAddress = "azure:microsoft.network:public-ip-address"
	// Container Service (AKS)
	TypeContainerServiceManagedCluster = "azure:microsoft.containerservice:managed-cluster"
	// Key Vault
	TypeKeyVaultVault = "azure:microsoft.keyvault:vault"
	// Storage
	TypeStorageStorageAccount = "azure:microsoft.storage:storage-account"
	// SQL — servers and databases
	TypeSQLServer   = "azure:microsoft.sql:server"
	TypeSQLDatabase = "azure:microsoft.sql:database"
	// SQL — subscription-wide resources
	TypeSQLInstancePool   = "azure:microsoft.sql:instance-pools"
	TypeSQLVirtualCluster = "azure:microsoft.sql:virtual-clusters"
	// SQL — managed instances and sub-resources
	TypeSQLManagedInstance            = "azure:microsoft.sql:managed-instances"
	TypeSQLManagedDatabase            = "azure:microsoft.sql:managed-instances/databases"
	TypeSQLManagedInstanceAdmin       = "azure:microsoft.sql:managed-instances/administrators"
	TypeSQLManagedInstanceVA          = "azure:microsoft.sql:managed-instances/vulnerability-assessments"
	TypeSQLManagedDatabaseVA          = "azure:microsoft.sql:managed-instances/databases/vulnerability-assessments"
	TypeSQLManagedInstanceKey         = "azure:microsoft.sql:managed-instances/keys"
	TypeSQLManagedInstanceEP          = "azure:microsoft.sql:managed-instances/encryption-protector"
	TypeSQLManagedInstancePEC         = "azure:microsoft.sql:managed-instances/private-endpoint-connections"
	TypeSQLManagedServerSecurityAlert = "azure:microsoft.sql:managed-instances/security-alert-policies"
	TypeSQLManagedDatabaseTDE         = "azure:microsoft.sql:managed-instances/databases/transparent-data-encryption"
	TypeSQLManagedDatabaseSecAlert    = "azure:microsoft.sql:managed-instances/databases/security-alert-policies"
	// SQL — server sub-resources
	TypeSQLServerKey                 = "azure:microsoft.sql:servers/keys"
	TypeSQLEncryptionProtector       = "azure:microsoft.sql:servers/encryption-protector"
	TypeSQLServerAdministrator       = "azure:microsoft.sql:servers/administrators"
	TypeSQLServerAuditingSettings    = "azure:microsoft.sql:servers/auditing-settings"
	TypeSQLServerExtAuditingSettings = "azure:microsoft.sql:servers/extended-auditing-settings"
	TypeSQLServerDevOpsAuditSettings = "azure:microsoft.sql:servers/dev-ops-auditing-settings"
	TypeSQLServerSecurityAlert       = "azure:microsoft.sql:servers/security-alert-policies"
	TypeSQLServerAdvancedThreatProt  = "azure:microsoft.sql:servers/advanced-threat-protection-settings"
	TypeSQLServerVulnAssessment      = "azure:microsoft.sql:servers/vulnerability-assessments"
	TypeSQLElasticPool               = "azure:microsoft.sql:servers/elasticpools"
	TypeSQLFailoverGroup             = "azure:microsoft.sql:servers/failover-groups"
	TypeSQLServerDNSAlias            = "azure:microsoft.sql:servers/dns-aliases"
	TypeSQLVirtualNetworkRule        = "azure:microsoft.sql:servers/virtual-network-rules"
	TypeSQLJobAgent                  = "azure:microsoft.sql:servers/job-agents"
	TypeSQLSyncAgent                 = "azure:microsoft.sql:servers/sync-agents"
	TypeSQLRestorableDroppedDB       = "azure:microsoft.sql:servers/restorable-dropped-databases"
	// SQL — database sub-resources
	TypeSQLDBTransparentDataEnc = "azure:microsoft.sql:servers/databases/transparent-data-encryption"
	TypeSQLDBSecurityAlert      = "azure:microsoft.sql:servers/databases/security-alert-policies"
	TypeSQLDBAdvancedThreatProt = "azure:microsoft.sql:servers/databases/advanced-threat-protection-settings"
	TypeSQLDBAuditingSettings   = "azure:microsoft.sql:servers/databases/auditing-settings"
	TypeSQLDBVulnAssessment     = "azure:microsoft.sql:servers/databases/vulnerability-assessments"
	TypeSQLSyncGroup            = "azure:microsoft.sql:servers/databases/sync-groups"
	TypeSQLReplicationLink      = "azure:microsoft.sql:servers/databases/replication-links"
	TypeSQLWorkloadGroup        = "azure:microsoft.sql:servers/databases/workload-groups"
	TypeSQLGeoBackupPolicy      = "azure:microsoft.sql:servers/databases/geo-backup-policies"
	TypeSQLLedgerDigestUpload   = "azure:microsoft.sql:servers/databases/ledger-digest-uploads"
	// App Service / Microsoft.Web
	TypeAppServiceServerFarm               = "azure:microsoft.web:server-farms"
	TypeAppServiceSite                     = "azure:microsoft.web:sites"
	TypeAppServiceSiteSlot                 = "azure:microsoft.web:sites/slots"
	TypeAppServiceEnvironment              = "azure:microsoft.web:hosting-environments"
	TypeAppServiceEnvironmentWorkerPool    = "azure:microsoft.web:hosting-environments/worker-pools"
	TypeAppServiceEnvironmentMultiRolePool = "azure:microsoft.web:hosting-environments/multi-role-pools"
	TypeAppServiceKubeEnvironment          = "azure:microsoft.web:kube-environments"
	TypeAppServiceStaticSite               = "azure:microsoft.web:static-sites"
	TypeAppServiceStaticSiteBuild          = "azure:microsoft.web:static-sites/builds"
	TypeAppServiceCertificate              = "azure:microsoft.web:certificates"
)

// azureAPITypeMap maps the lowercase Azure provider resource-type string
// (e.g. "microsoft.compute/virtualmachines") to the disco type constant.
// Update this map whenever a new service scanner is added.
var azureAPITypeMap = map[string]string{
	"microsoft.resources/resourcegroups":                                   TypeResourcesResourceGroup,
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
}

// KnownTypes returns all disco type strings currently covered by this provider.
// Used by the types command for gap-analysis against the Azure provider registry.
func KnownTypes() []string {
	types := make([]string, 0, len(azureAPITypeMap))
	for _, v := range azureAPITypeMap {
		types = append(types, v)
	}
	return types
}

// LookupAzureType returns the disco type string for a given lowercase Azure
// provider resource-type key (e.g. "microsoft.compute/virtualmachines") and
// whether it is covered by disco.
func LookupAzureType(key string) (string, bool) {
	v, ok := azureAPITypeMap[key]
	return v, ok
}
