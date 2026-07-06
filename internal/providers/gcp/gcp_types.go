package gcp

// Resource type constants for all GCP resource types this provider discovers.
// Constants prevent typos in scanner/resolver files — a mismatched string
// creates orphan resources with an undeclared type.
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
	// Compute Engine — storage (Wave 1 of the type-coverage buildout, docs/gcp-type-coverage.md)
	TypeComputeDisk                       = "gcp:compute:disk"
	TypeComputeRegionDisk                 = "gcp:compute:region-disk"
	TypeComputeImage                      = "gcp:compute:image"
	TypeComputeMachineImage               = "gcp:compute:machine-image"
	TypeComputeSnapshot                   = "gcp:compute:snapshot"
	TypeComputeRegionSnapshot             = "gcp:compute:region-snapshot"
	TypeComputeInstantSnapshot            = "gcp:compute:instant-snapshot"
	TypeComputeRegionInstantSnapshot      = "gcp:compute:region-instant-snapshot"
	TypeComputeInstantSnapshotGroup       = "gcp:compute:instant-snapshot-group"
	TypeComputeRegionInstantSnapshotGroup = "gcp:compute:region-instant-snapshot-group"
	TypeComputeStoragePool                = "gcp:compute:storage-pool"
	// Compute Engine — instance groups & templates (Wave 2 of the type-coverage buildout)
	TypeComputeInstanceGroup                           = "gcp:compute:instance-group"
	TypeComputeRegionInstanceGroup                     = "gcp:compute:region-instance-group"
	TypeComputeInstanceGroupManager                    = "gcp:compute:instance-group-manager"
	TypeComputeRegionInstanceGroupManager              = "gcp:compute:region-instance-group-manager"
	TypeComputeInstanceGroupManagerResizeRequest       = "gcp:compute:instance-group-manager-resize-request"
	TypeComputeRegionInstanceGroupManagerResizeRequest = "gcp:compute:region-instance-group-manager-resize-request"
	TypeComputeInstanceTemplate                        = "gcp:compute:instance-template"
	TypeComputeRegionInstanceTemplate                  = "gcp:compute:region-instance-template"
	// Compute Engine — addressing (Wave 3 of the type-coverage buildout)
	TypeComputeAddress                     = "gcp:compute:address"
	TypeComputeGlobalAddress               = "gcp:compute:global-address"
	TypeComputePublicAdvertisedPrefix      = "gcp:compute:public-advertised-prefix"
	TypeComputePublicDelegatedPrefix       = "gcp:compute:public-delegated-prefix"
	TypeComputeGlobalPublicDelegatedPrefix = "gcp:compute:global-public-delegated-prefix"
	// Certificate Manager
	TypeCertManagerCertificate = "gcp:certificatemanager:certificate"
	TypeCertManagerMap         = "gcp:certificatemanager:certificate-map"
	TypeCertManagerMapEntry    = "gcp:certificatemanager:certificate-map-entry"
	TypeCertManagerDNSAuth     = "gcp:certificatemanager:dns-authorization"
	// Cloud DNS
	TypeDNSManagedZone = "gcp:dns:managed-zone"
	TypeDNSRecordSet   = "gcp:dns:resource-record-set"
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
	TypeIAMSAKey          = "gcp:iam:key"
	// Cloud KMS
	TypeKMSKeyRing   = "gcp:cloudkms:key-ring"
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
	// Workspace Directory + Cloud Identity (tenant-scope identity)
	TypeWorkspaceUser      = "gcp:admin:user"
	TypeCloudIdentityGroup = "gcp:cloudidentity:group"
)
