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
	TypeComputeInstance         = "gcp:compute:instance"
	TypeComputeNetwork          = "gcp:compute:network"
	TypeComputeSubnet           = "gcp:compute:subnetwork"
	TypeComputeFirewall         = "gcp:compute:firewall"
	TypeComputeForwardingRule   = "gcp:compute:forwarding-rule"
	TypeComputeTargetHTTPProxy  = "gcp:compute:target-http-proxy"
	TypeComputeTargetHTTPSProxy = "gcp:compute:target-https-proxy"
	TypeComputeURLMap           = "gcp:compute:url-map"
	TypeComputeBackendService   = "gcp:compute:backend-service"
	TypeComputeBackendBucket    = "gcp:compute:backend-bucket"
	TypeComputeSecurityPolicy   = "gcp:compute:security-policy"
	// Certificate Manager
	TypeCertManagerCertificate = "gcp:certificatemanager:certificate"
	TypeCertManagerMap         = "gcp:certificatemanager:certificate-map"
	TypeCertManagerMapEntry    = "gcp:certificatemanager:certificate-map-entry"
	TypeCertManagerDNSAuth     = "gcp:certificatemanager:dns-authorization"
	// Cloud DNS
	TypeDNSManagedZone = "gcp:dns:managed-zone"
	TypeDNSRecordSet   = "gcp:dns:record-set"
	// Cloud Functions / Cloud Run
	TypeCloudFunction = "gcp:cloudfunctions:function"
	TypeCloudRunSvc   = "gcp:run:service"
	// Pub/Sub
	TypePubSubTopic        = "gcp:pubsub:topic"
	TypePubSubSubscription = "gcp:pubsub:subscription"
	TypePubSubSchema       = "gcp:pubsub:schema"
	// BigQuery
	TypeBQDataset = "gcp:bigquery:dataset"
	TypeBQTable   = "gcp:bigquery:table"
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
