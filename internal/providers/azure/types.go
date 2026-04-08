package azure

// Resource type constants for all Azure resource types discovered by this provider.
// Using constants prevents typos in scanner and resolver files — a mismatched
// string would create orphan resources with an undeclared type.
const (
	// Resource Manager
	TypeResourceGroup = "azure:resources:resource-group"
	// Compute
	TypeVirtualMachine = "azure:microsoft.compute:virtual-machine"
	TypeManagedDisk    = "azure:microsoft.compute:disk"
	// Network
	TypeVirtualNetwork       = "azure:network:virtual-network"
	TypeSubnet               = "azure:network:subnet"
	TypeNetworkSecurityGroup = "azure:network:network-security-group"
	TypePublicIPAddress      = "azure:network:public-ip-address"
	// Container Service (AKS)
	TypeAKSManagedCluster = "azure:containerservice:managed-cluster"
	// Key Vault
	TypeKeyVaultVault = "azure:keyvault:vault"
	// Storage
	TypeStorageAccount = "azure:storage:storage-account"
	// SQL
	TypeSQLServer   = "azure:sql:server"
	TypeSQLDatabase = "azure:sql:database"
)

// azureAPITypeMap maps the lowercase Azure provider resource-type string
// (e.g. "microsoft.compute/virtualmachines") to the disco type constant.
// Update this map whenever a new service scanner is added.
var azureAPITypeMap = map[string]string{
	"microsoft.resources/resourcegroups":         TypeResourceGroup,
	"microsoft.compute/virtualmachines":          TypeVirtualMachine,
	"microsoft.compute/disks":                    TypeManagedDisk,
	"microsoft.network/virtualnetworks":          TypeVirtualNetwork,
	"microsoft.network/virtualnetworks/subnets":  TypeSubnet,
	"microsoft.network/networksecuritygroups":    TypeNetworkSecurityGroup,
	"microsoft.network/publicipaddresses":        TypePublicIPAddress,
	"microsoft.containerservice/managedclusters": TypeAKSManagedCluster,
	"microsoft.keyvault/vaults":                  TypeKeyVaultVault,
	"microsoft.storage/storageaccounts":          TypeStorageAccount,
	"microsoft.sql/servers":                      TypeSQLServer,
	"microsoft.sql/servers/databases":            TypeSQLDatabase,
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
