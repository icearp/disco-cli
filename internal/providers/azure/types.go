package azure

// Resource type constants for all Azure resource types discovered by this provider.
// Using constants prevents typos in scanner and resolver files — a mismatched
// string would create orphan resources with an undeclared type.
const (
	// Resource Manager
	TypeResourceGroup = "azure:resources:resource-group"
	// Compute
	TypeVirtualMachine = "azure:compute:virtual-machine"
	TypeManagedDisk    = "azure:compute:disk"
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
