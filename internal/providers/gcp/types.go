package gcp

// Resource type constants for all GCP resource types discovered by this provider.
// Using constants prevents typos in scanner and resolver files — a mismatched
// string would create orphan resources with an undeclared type.
const (
	// Cloud Resource Manager
	TypeOrganization = "gcp:cloudresourcemanager:organization"
	TypeFolder       = "gcp:cloudresourcemanager:folder"
	TypeProject      = "gcp:cloudresourcemanager:project"
	// Compute Engine
	TypeComputeInstance = "gcp:compute:instance"
	TypeComputeNetwork  = "gcp:compute:network"
	TypeComputeSubnet   = "gcp:compute:subnetwork"
	TypeComputeFirewall = "gcp:compute:firewall"
	// GKE
	TypeGKECluster = "gcp:container:cluster"
	// IAM
	TypeIAMServiceAccount = "gcp:iam:service-account"
	TypeIAMPolicy         = "gcp:iam:policy"
	TypeIAMSAKey          = "gcp:iam:service-account-key"
	// Cloud KMS
	TypeKMSKeyRing   = "gcp:cloudkms:keyring"
	TypeKMSCryptoKey = "gcp:cloudkms:crypto-key"
	// Secret Manager
	TypeSecret = "gcp:secretmanager:secret"
	// Cloud Storage
	TypeStorageBucket = "gcp:storage:bucket"
	// Cloud SQL
	TypeSQLInstance = "gcp:sqladmin:instance"
)
