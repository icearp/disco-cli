package azure

// Resource type constants for all Azure resource types discovered by this provider.
// Using constants prevents typos in scanner and resolver files — a mismatched
// string would create orphan resources with an undeclared type.
const (
	// Resource Manager
	TypeResourcesResourceGroup = "azure:microsoft.resources:resource-group"
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
	// SQL
	TypeSQLServer   = "azure:microsoft.sql:server"
	TypeSQLDatabase = "azure:microsoft.sql:database"
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
