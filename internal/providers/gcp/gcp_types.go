package gcp

// Resource type constants for all GCP resource types this provider discovers.
// Constants prevent typos in scanner/resolver files — a mismatched string
// creates orphan resources with an undeclared type.
const (
	// Cloud Resource Manager
	TypeOrganization = "gcp:cloudresourcemanager:organization"
	TypeFolder       = "gcp:cloudresourcemanager:folder"
	TypeProject      = "gcp:cloudresourcemanager:project"
	// Cloud Resource Manager — Tags + Liens (Wave 8b of the type-coverage buildout)
	TypeTagKey       = "gcp:cloudresourcemanager:tag-key"
	TypeTagValue     = "gcp:cloudresourcemanager:tag-value"
	TypeTagBinding   = "gcp:cloudresourcemanager:tag-binding"
	TypeEffectiveTag = "gcp:cloudresourcemanager:effective-tag"
	TypeTagHold      = "gcp:cloudresourcemanager:tag-hold"
	TypeLien         = "gcp:cloudresourcemanager:lien"
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
	// Compute Engine — networking core (Wave 4 of the type-coverage buildout)
	TypeComputeRoute                       = "gcp:compute:route"
	TypeComputeRouter                      = "gcp:compute:router"
	TypeComputeVpnGateway                  = "gcp:compute:vpn-gateway"
	TypeComputeExternalVpnGateway          = "gcp:compute:external-vpn-gateway"
	TypeComputeTargetVpnGateway            = "gcp:compute:target-vpn-gateway"
	TypeComputeVpnTunnel                   = "gcp:compute:vpn-tunnel"
	TypeComputeNetworkAttachment           = "gcp:compute:network-attachment"
	TypeComputeNetworkEndpointGroup        = "gcp:compute:network-endpoint-group"
	TypeComputeRegionNetworkEndpointGroup  = "gcp:compute:region-network-endpoint-group"
	TypeComputeGlobalNetworkEndpointGroup  = "gcp:compute:global-network-endpoint-group"
	TypeComputeNetworkFirewallPolicy       = "gcp:compute:network-firewall-policy"
	TypeComputeRegionNetworkFirewallPolicy = "gcp:compute:region-network-firewall-policy"
	TypeComputeNetworkProfile              = "gcp:compute:network-profile"
	TypeComputeNodeGroup                   = "gcp:compute:node-group"
	TypeComputeNodeTemplate                = "gcp:compute:node-template"
	TypeComputePacketMirroring             = "gcp:compute:packet-mirroring"
	TypeComputeServiceAttachment           = "gcp:compute:service-attachment"
	TypeComputeNetworkEdgeSecurityService  = "gcp:compute:network-edge-security-service"
	TypeComputeCrossSiteNetwork            = "gcp:compute:cross-site-network"
	TypeComputeWireGroup                   = "gcp:compute:wire-group"
	// Compute Engine — interconnect (Wave 5 of the type-coverage buildout)
	TypeComputeInterconnect                = "gcp:compute:interconnect"
	TypeComputeInterconnectAttachment      = "gcp:compute:interconnect-attachment"
	TypeComputeInterconnectGroup           = "gcp:compute:interconnect-group"
	TypeComputeInterconnectAttachmentGroup = "gcp:compute:interconnect-attachment-group"
	// Compute Engine — load balancing / health checks / SSL-TLS (Wave 6 of the type-coverage buildout)
	TypeComputeGlobalForwardingRule          = "gcp:compute:global-forwarding-rule"
	TypeComputeHealthCheck                   = "gcp:compute:health-check"
	TypeComputeRegionHealthCheck             = "gcp:compute:region-health-check"
	TypeComputeRegionCompositeHealthCheck    = "gcp:compute:region-composite-health-check"
	TypeComputeRegionHealthAggregationPolicy = "gcp:compute:region-health-aggregation-policy"
	TypeComputeRegionHealthCheckService      = "gcp:compute:region-health-check-service"
	TypeComputeRegionHealthSource            = "gcp:compute:region-health-source"
	TypeComputeRegionNotificationEndpoint    = "gcp:compute:region-notification-endpoint"
	TypeComputeHttpHealthCheck               = "gcp:compute:http-health-check"
	TypeComputeHttpsHealthCheck              = "gcp:compute:https-health-check"
	TypeComputeSslCertificate                = "gcp:compute:ssl-certificate"
	TypeComputeRegionSslCertificate          = "gcp:compute:region-ssl-certificate"
	TypeComputeSslPolicy                     = "gcp:compute:ssl-policy"
	TypeComputeRegionSslPolicy               = "gcp:compute:region-ssl-policy"
	TypeComputeTargetSslProxy                = "gcp:compute:target-ssl-proxy"
	TypeComputeTargetTcpProxy                = "gcp:compute:target-tcp-proxy"
	TypeComputeRegionTargetTcpProxy          = "gcp:compute:region-target-tcp-proxy"
	TypeComputeTargetGrpcProxy               = "gcp:compute:target-grpc-proxy"
	TypeComputeRegionTargetHTTPProxy         = "gcp:compute:region-target-http-proxy"
	TypeComputeRegionTargetHTTPSProxy        = "gcp:compute:region-target-https-proxy"
	TypeComputeRegionURLMap                  = "gcp:compute:region-url-map"
	TypeComputeRegionBackendBucket           = "gcp:compute:region-backend-bucket"
	TypeComputeRegionBackendService          = "gcp:compute:region-backend-service"
	TypeComputeTargetInstance                = "gcp:compute:target-instance"
	TypeComputeTargetPool                    = "gcp:compute:target-pool"
	// Compute Engine — autoscaling / reservations (Wave 7 of the type-coverage buildout)
	TypeComputeAutoscaler           = "gcp:compute:autoscaler"
	TypeComputeRegionAutoscaler     = "gcp:compute:region-autoscaler"
	TypeComputeReservation          = "gcp:compute:reservation"
	TypeComputeReservationBlock     = "gcp:compute:reservation-block"
	TypeComputeReservationSubBlock  = "gcp:compute:reservation-sub-block"
	TypeComputeFutureReservation    = "gcp:compute:future-reservation"
	TypeComputeRegionCommitment     = "gcp:compute:region-commitment"
	TypeComputeResourcePolicy       = "gcp:compute:resource-policy"
	TypeComputeRegionSecurityPolicy = "gcp:compute:region-security-policy"
	// Certificate Manager
	TypeCertManagerCertificate = "gcp:certificatemanager:certificate"
	TypeCertManagerMap         = "gcp:certificatemanager:certificate-map"
	TypeCertManagerMapEntry    = "gcp:certificatemanager:certificate-map-entry"
	TypeCertManagerDNSAuth     = "gcp:certificatemanager:dns-authorization"
	// Cloud DNS
	TypeDNSManagedZone = "gcp:dns:managed-zone"
	TypeDNSRecordSet   = "gcp:dns:resource-record-set"
	// Cloud DNS — DNSSEC keys, network policies, response policies (Wave 8e of
	// the type-coverage buildout)
	TypeDNSKey                = "gcp:dns:dns-key"
	TypeDNSPolicy             = "gcp:dns:policy"
	TypeDNSResponsePolicy     = "gcp:dns:response-policy"
	TypeDNSResponsePolicyRule = "gcp:dns:response-policy-rule"
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
	// BigQuery secondary resources (Wave 10d of the type-coverage buildout)
	TypeBQModel           = "gcp:bigquery:model"
	TypeBQRoutine         = "gcp:bigquery:routine"
	TypeBQRowAccessPolicy = "gcp:bigquery:row-access-policy"
	// Bigtable / Firestore / Spanner
	TypeBigtableInstance = "gcp:bigtableadmin:instance"
	TypeBigtableCluster  = "gcp:bigtableadmin:cluster"
	TypeFirestoreDB      = "gcp:firestore:database"
	// Firestore secondary resources (Wave 10c of the type-coverage buildout)
	TypeFirestoreBackup         = "gcp:firestore:backup"
	TypeFirestoreBackupSchedule = "gcp:firestore:backup-schedule"
	TypeFirestoreUserCred       = "gcp:firestore:user-cred"
	TypeSpannerInstance         = "gcp:spanner:instance"
	TypeSpannerDatabase         = "gcp:spanner:database"
	// Spanner secondary resources (Wave 10a of the type-coverage buildout)
	TypeSpannerInstanceConfig    = "gcp:spanner:instance-config"
	TypeSpannerInstancePartition = "gcp:spanner:instance-partition"
	TypeSpannerBackup            = "gcp:spanner:backup"
	TypeSpannerBackupSchedule    = "gcp:spanner:backup-schedule"
	TypeSpannerDatabaseRole      = "gcp:spanner:database-role"
	// Bigtable secondary resources (Wave 10b of the type-coverage buildout)
	TypeBigtableBackup           = "gcp:bigtableadmin:backup"
	TypeBigtableAppProfile       = "gcp:bigtableadmin:app-profile"
	TypeBigtableTable            = "gcp:bigtableadmin:table"
	TypeBigtableAuthorizedView   = "gcp:bigtableadmin:authorized-view"
	TypeBigtableLogicalView      = "gcp:bigtableadmin:logical-view"
	TypeBigtableMaterializedView = "gcp:bigtableadmin:materialized-view"
	TypeBigtableSchemaBundle     = "gcp:bigtableadmin:schema-bundle"
	TypeBigtableHotTablet        = "gcp:bigtableadmin:hot-tablet"
	TypeBigtableMemoryLayer      = "gcp:bigtableadmin:memory-layer"
	// Composer
	TypeComposerEnv = "gcp:composer:environment"
	// Artifact Registry
	TypeArtifactRepository = "gcp:artifactregistry:repository"
	// Artifact Registry secondary resources (Wave 11b of the type-coverage buildout)
	TypeArtifactPackage    = "gcp:artifactregistry:package"
	TypeArtifactTag        = "gcp:artifactregistry:tag"
	TypeArtifactRule       = "gcp:artifactregistry:rule"
	TypeArtifactAttachment = "gcp:artifactregistry:attachment"
	// Logging / Monitoring
	TypeLoggingSink                   = "gcp:logging:sink"
	TypeLoggingBucket                 = "gcp:logging:bucket"
	TypeLoggingExclusion              = "gcp:logging:exclusion"
	TypeLoggingMetric                 = "gcp:logging:metric"
	TypeLoggingLink                   = "gcp:logging:link"
	TypeLoggingView                   = "gcp:logging:view"
	TypeLoggingLogScope               = "gcp:logging:log-scope"
	TypeLoggingSavedQuery             = "gcp:logging:saved-query"
	TypeMonitoringAlertPol            = "gcp:monitoring:alert-policy"
	TypeMonitoringDashboard           = "gcp:monitoring:dashboard"
	TypeMonitoringGroup               = "gcp:monitoring:group"
	TypeMonitoringNotificationChannel = "gcp:monitoring:notification-channel"
	TypeMonitoringService             = "gcp:monitoring:service"
	TypeMonitoringSLO                 = "gcp:monitoring:service-level-objective"
	TypeMonitoringSnooze              = "gcp:monitoring:snooze"
	TypeMonitoringUptimeCheckConfig   = "gcp:monitoring:uptime-check-config"
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
	// IAM — workforce/workload federation, OAuth clients, custom roles
	// (Wave 8g of the type-coverage buildout, closes ROADMAP R4.23)
	TypeIAMWorkforcePool        = "gcp:iam:workforce-pool"
	TypeIAMWorkloadIdentityPool = "gcp:iam:workload-identity-pool"
	// TypeIAMProvider covers BOTH WorkforcePoolProvider and
	// WorkloadIdentityPoolProvider — the Discovery API's collection name
	// ("providers") is identical at both nesting paths, so they singularize to
	// the same upstream key; splitting into two disco types would leave one
	// permanently unmatched against that shared key.
	TypeIAMProvider        = "gcp:iam:provider"
	TypeIAMScimTenant      = "gcp:iam:scim-tenant"
	TypeIAMNamespace       = "gcp:iam:namespace"
	TypeIAMManagedIdentity = "gcp:iam:managed-identity"
	TypeIAMOauthClient     = "gcp:iam:oauth-client"
	TypeIAMCredential      = "gcp:iam:credential"
	// TypeIAMRole is custom roles only (org- and project-scoped) — the
	// predefined-role catalog is Google-managed and out of scope.
	TypeIAMRole = "gcp:iam:role"
	// Cloud KMS
	TypeKMSKeyRing                 = "gcp:cloudkms:key-ring"
	TypeKMSCryptoKey               = "gcp:cloudkms:crypto-key"
	TypeKMSCryptoKeyVersion        = "gcp:cloudkms:crypto-key-version"
	TypeKMSEkmConnection           = "gcp:cloudkms:ekm-connection"
	TypeKMSImportJob               = "gcp:cloudkms:import-job"
	TypeKMSKeyHandle               = "gcp:cloudkms:key-handle"
	TypeKMSSingleTenantHsmInstance = "gcp:cloudkms:single-tenant-hsm-instance"
	// Secret Manager
	TypeSecret = "gcp:secretmanager:secret"
	// Cloud Storage
	TypeStorageBucket = "gcp:storage:bucket"
	// Cloud Storage secondary resources (Wave 11a of the type-coverage buildout)
	TypeStorageHmacKey                    = "gcp:storage:hmac-key"
	TypeStorageNotification               = "gcp:storage:notification"
	TypeStorageManagedFolder              = "gcp:storage:managed-folder"
	TypeStorageAnywhereCache              = "gcp:storage:anywhere-cache"
	TypeStorageFolder                     = "gcp:storage:folder"
	TypeStorageBucketAccessControl        = "gcp:storage:bucket-access-control"
	TypeStorageDefaultObjectAccessControl = "gcp:storage:default-object-access-control"
	// Cloud SQL
	TypeSQLInstance = "gcp:sqladmin:instance"
	// Cloud SQL — per-instance children (Wave 8d of the type-coverage buildout)
	TypeSQLBackupRun = "gcp:sqladmin:backup-run"
	TypeSQLDatabase  = "gcp:sqladmin:database"
	TypeSQLSslCert   = "gcp:sqladmin:ssl-cert"
	TypeSQLUser      = "gcp:sqladmin:user"
	// Access Context Manager (VPC Service Controls — org-scoped)
	TypeAccessPolicy     = "gcp:accesscontextmanager:access-policy"
	TypeServicePerimeter = "gcp:accesscontextmanager:service-perimeter"
	// Access Context Manager — Access Levels + Authorized Orgs Descs (fan-out
	// per AccessPolicy) and GcpUserAccessBinding (org-scoped, context-aware
	// access for Workspace users) — Wave 8c of the type-coverage buildout.
	TypeAccessLevel          = "gcp:accesscontextmanager:access-level"
	TypeAuthorizedOrgsDesc   = "gcp:accesscontextmanager:authorized-orgs-desc"
	TypeGcpUserAccessBinding = "gcp:accesscontextmanager:gcp-user-access-binding"
	// Dataproc + Dataflow
	TypeDataprocCluster = "gcp:dataproc:cluster"
	TypeDataflowJob     = "gcp:dataflow:job"
	// Dataproc secondary resources (Wave 10e of the type-coverage buildout)
	TypeDataprocAutoscalingPolicy = "gcp:dataproc:autoscaling-policy"
	TypeDataprocBatch             = "gcp:dataproc:batch"
	TypeDataprocSession           = "gcp:dataproc:session"
	TypeDataprocSessionTemplate   = "gcp:dataproc:session-template"
	TypeDataprocWorkflowTemplate  = "gcp:dataproc:workflow-template"
	TypeDataprocJob               = "gcp:dataproc:job"
	// Workspace Directory + Cloud Identity (tenant-scope identity)
	TypeWorkspaceUser      = "gcp:admin:user"
	TypeCloudIdentityGroup = "gcp:cloudidentity:group"
	// Cloud Identity — devices, memberships, SSO, policy (Wave 8f of the
	// type-coverage buildout)
	TypeCloudIdentityDevice                = "gcp:cloudidentity:device"
	TypeCloudIdentityDeviceUser            = "gcp:cloudidentity:device-user"
	TypeCloudIdentityClientState           = "gcp:cloudidentity:client-state"
	TypeCloudIdentityMembership            = "gcp:cloudidentity:membership"
	TypeCloudIdentityInboundOidcSsoProfile = "gcp:cloudidentity:inbound-oidc-sso-profile"
	TypeCloudIdentityInboundSamlSsoProfile = "gcp:cloudidentity:inbound-saml-sso-profile"
	TypeCloudIdentityIdpCredential         = "gcp:cloudidentity:idp-credential"
	TypeCloudIdentityInboundSsoAssignment  = "gcp:cloudidentity:inbound-sso-assignment"
	TypeCloudIdentityPolicy                = "gcp:cloudidentity:policy"
	TypeCloudIdentityUserinvitation        = "gcp:cloudidentity:userinvitation"
)
