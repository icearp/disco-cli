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
	// Bigtable / Firestore / Spanner
	TypeBigtableInstance = "gcp:bigtableadmin:instance"
	TypeBigtableCluster  = "gcp:bigtableadmin:cluster"
	TypeFirestoreDB      = "gcp:firestore:database"
	TypeSpannerInstance  = "gcp:spanner:instance"
	TypeSpannerDatabase  = "gcp:spanner:database"
	// Composer
	TypeComposerEnv = "gcp:composer:environment"
	// Artifact Registry
	TypeArtifactRepository = "gcp:artifactregistry:repository"
	// Logging / Monitoring
	TypeLoggingSink        = "gcp:logging:sink"
	TypeMonitoringAlertPol = "gcp:monitoring:alert-policy"
	// Cloud Build
	TypeCloudBuildTrigger = "gcp:cloudbuild:trigger"
	// Binary Authorization
	TypeBinAuthPolicy   = "gcp:binaryauthorization:policy"
	TypeBinAuthAttestor = "gcp:binaryauthorization:attestor"
	// Cloud Run Jobs / Batch
	TypeCloudRunJob = "gcp:run:job"
	TypeBatchJob    = "gcp:batch:job"
	// GKE
	TypeGKECluster = "gcp:container:cluster"
	// IAM
	TypeIAMServiceAccount = "gcp:iam:service-account"
	TypeIAMPolicy         = "gcp:iam:policy"
	TypeIAMSAKey          = "gcp:iam:service-account-key"
	// Synthetic stub for cross-project IAM bindings whose member SA lives in a
	// project not in scan scope (R5). NativeID = projects/<other>.
	TypeIAMForeignProject = "gcp:iam:foreign-project"
	// Cloud KMS
	TypeKMSKeyRing   = "gcp:cloudkms:keyring"
	TypeKMSCryptoKey = "gcp:cloudkms:crypto-key"
	// Secret Manager
	TypeSecret = "gcp:secretmanager:secret"
	// Cloud Storage
	TypeStorageBucket = "gcp:storage:bucket"
	// Cloud SQL
	TypeSQLInstance = "gcp:sqladmin:instance"
	// Access Context Manager (VPC Service Controls — org-scoped)
	TypeAccessPolicy     = "gcp:accesscontextmanager:access-policy"
	TypeServicePerimeter = "gcp:accesscontextmanager:service-perimeter"
	// Dataproc + Dataflow
	TypeDataprocCluster = "gcp:dataproc:cluster"
	TypeDataflowJob     = "gcp:dataflow:job"
)
