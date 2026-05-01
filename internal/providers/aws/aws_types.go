package aws

// Resource type constants for all AWS resource types discovered by this provider.
// Using constants prevents typos in scanner and resolver files — a mismatched
// string would create orphan resources with an undeclared type.
const (
	// EC2 — compute management (ec2_compute_mgmt_scanners.go)
	TypeEC2Instance                = "aws:ec2:instance"
	TypeEC2Image                   = "aws:ec2:image"
	TypeEC2SecurityGroup           = "aws:ec2:security-group"
	TypeEC2Volume                  = "aws:ec2:volume"
	TypeEC2LaunchTemplate          = "aws:ec2:launch-template"
	TypeEC2KeyPair                 = "aws:ec2:key-pair"
	TypeEC2PlacementGroup          = "aws:ec2:placement-group"
	TypeEC2SpotFleet               = "aws:ec2:spot-fleet"
	TypeEC2Host                    = "aws:ec2:host"
	TypeEC2CapacityReservation     = "aws:ec2:capacity-reservation"
	TypeEC2InstanceConnectEndpoint = "aws:ec2:instance-connect-endpoint"
	// EC2 — networking (ec2_networking_scanners.go)
	TypeEC2VPC                  = "aws:ec2:vpc"
	TypeEC2Subnet               = "aws:ec2:subnet"
	TypeEC2InternetGateway      = "aws:ec2:internet-gateway"
	TypeEC2NatGateway           = "aws:ec2:nat-gateway"
	TypeEC2RouteTable           = "aws:ec2:route-table"
	TypeEC2EIP                  = "aws:ec2:eip"
	TypeEC2NetworkInterface     = "aws:ec2:network-interface"
	TypeEC2NetworkACL           = "aws:ec2:network-acl"
	TypeEC2VPCEndpoint          = "aws:ec2:vpc-endpoint"
	TypeEC2VPCPeeringConnection = "aws:ec2:vpc-peering-connection"
	TypeEC2DHCPOptions          = "aws:ec2:dhcp-options"
	TypeEC2EgressOnlyIGW        = "aws:ec2:egress-only-internet-gateway"
	// EC2 — VPN (ec2_vpn_scanners.go)
	TypeEC2CustomerGateway          = "aws:ec2:customer-gateway"
	TypeEC2VPNGateway               = "aws:ec2:vpn-gateway"
	TypeEC2VPNConnection            = "aws:ec2:vpn-connection"
	TypeEC2TransitGateway           = "aws:ec2:transit-gateway"
	TypeEC2TransitGatewayAttachment = "aws:ec2:transit-gateway-attachment"
	// EC2 — observability (ec2_observability_scanners.go)
	TypeEC2FlowLog                            = "aws:ec2:flow-log"
	TypeEC2PrefixList                         = "aws:ec2:prefix-list"
	TypeEC2NetworkInsightsPath                = "aws:ec2:network-insights-path"
	TypeEC2NetworkInsightsAnalysis            = "aws:ec2:network-insights-analysis"
	TypeEC2NetworkInsightsAccessScope         = "aws:ec2:network-insights-access-scope"
	TypeEC2NetworkInsightsAccessScopeAnalysis = "aws:ec2:network-insights-access-scope-analysis"
	// EC2 — IPAM (ec2_ipam_scanners.go)
	TypeEC2IPAM                             = "aws:ec2:ipam"
	TypeEC2IPAMScope                        = "aws:ec2:ipam-scope"
	TypeEC2IPAMPool                         = "aws:ec2:ipam-pool"
	TypeEC2IPAMPoolCIDR                     = "aws:ec2:ipam-pool-cidr"
	TypeEC2IPAMAllocation                   = "aws:ec2:ipam-allocation"
	TypeEC2IPAMResourceDiscovery            = "aws:ec2:ipam-resource-discovery"
	TypeEC2IPAMResourceDiscoveryAssociation = "aws:ec2:ipam-resource-discovery-association"
	// EC2 — Transit Gateway extended (ec2_tgw_ext_scanners.go)
	TypeEC2TransitGatewayConnect                    = "aws:ec2:transit-gateway-connect"
	TypeEC2TransitGatewayConnectPeer                = "aws:ec2:transit-gateway-connect-peer"
	TypeEC2TransitGatewayMulticastDomain            = "aws:ec2:transit-gateway-multicast-domain"
	TypeEC2TransitGatewayMulticastDomainAssociation = "aws:ec2:transit-gateway-multicast-domain-association"
	TypeEC2TransitGatewayMulticastGroupMember       = "aws:ec2:transit-gateway-multicast-group-member"
	TypeEC2TransitGatewayMulticastGroupSource       = "aws:ec2:transit-gateway-multicast-group-source"
	TypeEC2TransitGatewayPeeringAttachment          = "aws:ec2:transit-gateway-peering-attachment"
	TypeEC2TransitGatewayRouteTable                 = "aws:ec2:transit-gateway-route-table"
	TypeEC2TransitGatewayRouteTableAssociation      = "aws:ec2:transit-gateway-route-table-association"
	TypeEC2TransitGatewayRouteTablePropagation      = "aws:ec2:transit-gateway-route-table-propagation"
	TypeEC2TransitGatewayVPCAttachment              = "aws:ec2:transit-gateway-vpc-attachment"
	TypeEC2TransitGatewayRoute                      = "aws:ec2:transit-gateway-route"
	// EC2 — Traffic Mirroring (ec2_traffic_mirror_scanners.go)
	TypeEC2TrafficMirrorFilter     = "aws:ec2:traffic-mirror-filter"
	TypeEC2TrafficMirrorFilterRule = "aws:ec2:traffic-mirror-filter-rule"
	TypeEC2TrafficMirrorSession    = "aws:ec2:traffic-mirror-session"
	TypeEC2TrafficMirrorTarget     = "aws:ec2:traffic-mirror-target"
	// EC2 — Verified Access (ec2_verified_access_scanners.go)
	TypeEC2VerifiedAccessInstance      = "aws:ec2:verified-access-instance"
	TypeEC2VerifiedAccessTrustProvider = "aws:ec2:verified-access-trust-provider"
	TypeEC2VerifiedAccessGroup         = "aws:ec2:verified-access-group"
	TypeEC2VerifiedAccessEndpoint      = "aws:ec2:verified-access-endpoint"
	// EC2 — Local Gateway (ec2_local_gateway_scanners.go)
	TypeEC2LocalGatewayRouteTable               = "aws:ec2:local-gateway-route-table"
	TypeEC2LocalGatewayRoute                    = "aws:ec2:local-gateway-route"
	TypeEC2LocalGatewayVirtualInterface         = "aws:ec2:local-gateway-virtual-interface"
	TypeEC2LocalGatewayVirtualInterfaceGroup    = "aws:ec2:local-gateway-virtual-interface-group"
	TypeEC2LocalGatewayRouteTableVPCAssociation = "aws:ec2:local-gateway-route-table-vpc-association"
	TypeEC2LocalGatewayRouteTableVIGAssociation = "aws:ec2:local-gateway-route-table-virtual-interface-group-association"
	// EC2 — Client VPN (ec2_client_vpn_scanners.go)
	TypeEC2ClientVPNEndpoint                 = "aws:ec2:client-vpn-endpoint"
	TypeEC2ClientVPNAuthorizationRule        = "aws:ec2:client-vpn-authorization-rule"
	TypeEC2ClientVPNRoute                    = "aws:ec2:client-vpn-route"
	TypeEC2ClientVPNTargetNetworkAssociation = "aws:ec2:client-vpn-target-network-association"
	// EC2 — extended resources (ec2_compute_mgmt_scanners.go, ec2_networking_scanners.go)
	TypeEC2CapacityReservationFleet          = "aws:ec2:capacity-reservation-fleet"
	TypeEC2Fleet                             = "aws:ec2:ec2-fleet"
	TypeEC2CarrierGateway                    = "aws:ec2:carrier-gateway"
	TypeEC2VPCBlockPublicAccessOptions       = "aws:ec2:vpc-block-public-access-options"
	TypeEC2VPCBlockPublicAccessExclusion     = "aws:ec2:vpc-block-public-access-exclusion"
	TypeEC2VPCEndpointConnectionNotification = "aws:ec2:vpc-endpoint-connection-notification"
	TypeEC2VPCEndpointService                = "aws:ec2:vpc-endpoint-service"
	TypeEC2VPCEndpointServicePermissions     = "aws:ec2:vpc-endpoint-service-permissions"
	TypeEC2SecurityGroupVPCAssociation       = "aws:ec2:security-group-vpc-association"
	TypeEC2NetworkInterfacePermission        = "aws:ec2:network-interface-permission"
	TypeEC2SnapshotBlockPublicAccess         = "aws:ec2:snapshot-block-public-access"
	// EC2 — Route Server (ec2_route_server_scanners.go)
	TypeEC2RouteServer         = "aws:ec2:route-server"
	TypeEC2RouteServerEndpoint = "aws:ec2:route-server-endpoint"
	TypeEC2RouteServerPeer     = "aws:ec2:route-server-peer"
	// EC2 — IPAM Prefix List Resolver (ec2_ipam_resolver_scanners.go)
	TypeEC2IPAMPrefixListResolver       = "aws:ec2:ipam-prefix-list-resolver"
	TypeEC2IPAMPrefixListResolverTarget = "aws:ec2:ipam-prefix-list-resolver-target"
	// EC2 — Misc (ec2_misc_extra_scanners.go)
	TypeEC2CapacityManagerDataExport            = "aws:ec2:capacity-manager-data-export"
	TypeEC2NetworkPerformanceMetricSubscription = "aws:ec2:network-performance-metric-subscription"
	TypeEC2TransitGatewayMeteringPolicy         = "aws:ec2:transit-gateway-metering-policy"
	TypeEC2VPCEncryptionControl                 = "aws:ec2:vpc-encryption-control"
	TypeEC2VPNConcentrator                      = "aws:ec2:vpn-concentrator"
	// IoT — Things (iot_things_scanners.go)
	TypeIoTThing                    = "aws:iot:thing"
	TypeIoTThingGroup               = "aws:iot:thing-group"
	TypeIoTThingType                = "aws:iot:thing-type"
	TypeIoTBillingGroup             = "aws:iot:billing-group"
	TypeIoTThingPrincipalAttachment = "aws:iot:thing-principal-attachment"
	// IoT — Certs/Auth (iot_certs_scanners.go)
	TypeIoTCertificate               = "aws:iot:certificate"
	TypeIoTCACertificate             = "aws:iot:ca-certificate"
	TypeIoTCertificateProvider       = "aws:iot:certificate-provider"
	TypeIoTPolicy                    = "aws:iot:policy"
	TypeIoTPolicyPrincipalAttachment = "aws:iot:policy-principal-attachment"
	TypeIoTRoleAlias                 = "aws:iot:role-alias"
	TypeIoTAuthorizer                = "aws:iot:authorizer"
	TypeIoTDomainConfiguration       = "aws:iot:domain-configuration"
	// IoT — Defender (iot_defender_scanners.go)
	TypeIoTAccountAuditConfiguration = "aws:iot:account-audit-configuration"
	TypeIoTScheduledAudit            = "aws:iot:scheduled-audit"
	TypeIoTMitigationAction          = "aws:iot:mitigation-action"
	TypeIoTSecurityProfile           = "aws:iot:security-profile"
	TypeIoTCustomMetric              = "aws:iot:custom-metric"
	TypeIoTDimension                 = "aws:iot:dimension"
	// IoT — Jobs (iot_jobs_scanners.go)
	TypeIoTCommand              = "aws:iot:command"
	TypeIoTJobTemplate          = "aws:iot:job-template"
	TypeIoTFleetMetric          = "aws:iot:fleet-metric"
	TypeIoTProvisioningTemplate = "aws:iot:provisioning-template"
	// IoT — Software (iot_software_scanners.go)
	TypeIoTSoftwarePackage        = "aws:iot:software-package"
	TypeIoTSoftwarePackageVersion = "aws:iot:software-package-version"
	// IoT — Topic Rules (iot_topic_scanners.go)
	TypeIoTTopicRule            = "aws:iot:topic-rule"
	TypeIoTTopicRuleDestination = "aws:iot:topic-rule-destination"
	// IoT — Logging/Encryption (iot_logging_scanners.go)
	TypeIoTLogging                 = "aws:iot:logging"
	TypeIoTResourceSpecificLogging = "aws:iot:resource-specific-logging"
	TypeIoTEncryptionConfiguration = "aws:iot:encryption-configuration"
	// IAM — principals
	TypeIAMRole              = "aws:iam:role"
	TypeIAMUser              = "aws:iam:user"
	TypeIAMGroup             = "aws:iam:group"
	TypeIAMServiceLinkedRole = "aws:iam:service-linked-role"
	// IAM — policies and credentials
	TypeIAMPolicy      = "aws:iam:policy"
	TypeIAMRolePolicy  = "aws:iam:role-policy"
	TypeIAMUserPolicy  = "aws:iam:user-policy"
	TypeIAMGroupPolicy = "aws:iam:group-policy"
	TypeIAMAccessKey   = "aws:iam:access-key"
	// IAM — identity provider and certificate resources
	TypeIAMInstanceProfile   = "aws:iam:instance-profile"
	TypeIAMOIDCProvider      = "aws:iam:oidc-provider"
	TypeIAMSAMLProvider      = "aws:iam:saml-provider"
	TypeIAMServerCertificate = "aws:iam:server-certificate"
	TypeIAMVirtualMFADevice  = "aws:iam:virtual-mfa-device"
	// IAM — synthetic stub for cross-account trust principals (R5).
	// One row per foreign account ID referenced by an IAM role trust policy. NativeID = arn:aws:iam::<acct>:root.
	TypeIAMForeignAccount = "aws:iam:foreign-account"
	// Lambda
	TypeLambdaFunction          = "aws:lambda:function"
	TypeLambdaAlias             = "aws:lambda:alias"
	TypeLambdaCapacityProvider  = "aws:lambda:capacity-provider"
	TypeLambdaCodeSigningConfig = "aws:lambda:code-signing-config"
	TypeLambdaEventInvokeConfig = "aws:lambda:event-invoke-config"
	TypeLambdaESM               = "aws:lambda:event-source-mapping"
	TypeLambdaLayerVersion      = "aws:lambda:layer-version"
	TypeLambdaURL               = "aws:lambda:url"
	TypeLambdaVersion           = "aws:lambda:version"
	// RDS
	TypeRDSCustomDBEngineVersion   = "aws:rds:custom-db-engine-version"
	TypeRDSDBCluster               = "aws:rds:db-cluster"
	TypeRDSDBClusterParameterGroup = "aws:rds:db-cluster-parameter-group"
	TypeRDSDBInstance              = "aws:rds:db-instance"
	TypeRDSDBParameterGroup        = "aws:rds:db-parameter-group"
	TypeRDSDBProxy                 = "aws:rds:db-proxy"
	TypeRDSDBProxyEndpoint         = "aws:rds:db-proxy-endpoint"
	TypeRDSDBProxyTargetGroup      = "aws:rds:db-proxy-target-group"
	TypeRDSDBSecurityGroup         = "aws:rds:db-security-group"
	TypeRDSDBShardGroup            = "aws:rds:db-shard-group"
	TypeRDSDBSubnetGroup           = "aws:rds:db-subnet-group"
	TypeRDSEventSubscription       = "aws:rds:event-subscription"
	TypeRDSGlobalCluster           = "aws:rds:global-cluster"
	TypeRDSIntegration             = "aws:rds:integration"
	TypeRDSOptionGroup             = "aws:rds:option-group"
	// DynamoDB
	TypeDynamoDBTable       = "aws:dynamodb:table"
	TypeDynamoDBGlobalTable = "aws:dynamodb:global-table"
	TypeDynamoDBStream      = "aws:dynamodb:stream"
	// EFS (efs_scanners.go, efs_resolvers.go)
	TypeEFSFileSystem  = "aws:efs:file-system"
	TypeEFSMountTarget = "aws:efs:mount-target"
	TypeEFSAccessPoint = "aws:efs:access-point"
	// WAFv2 (wafv2_scanners.go, wafv2_resolvers.go)
	TypeWAFv2WebACL    = "aws:wafv2:web-acl"
	TypeWAFv2RuleGroup = "aws:wafv2:rule-group"
	TypeWAFv2IPSet     = "aws:wafv2:ip-set"
	// Network Firewall (networkfirewall_scanners.go, networkfirewall_resolvers.go)
	TypeNetworkFirewallFirewall       = "aws:network-firewall:firewall"
	TypeNetworkFirewallFirewallPolicy = "aws:network-firewall:firewall-policy"
	TypeNetworkFirewallRuleGroup      = "aws:network-firewall:rule-group"
	// EventBridge (eventbridge_scanners.go, eventbridge_resolvers.go)
	TypeEventsEventBus       = "aws:events:event-bus"
	TypeEventsRule           = "aws:events:rule"
	TypeEventsAPIDestination = "aws:events:api-destination"
	TypeEventsConnection     = "aws:events:connection"
	// CloudTrail (cloudtrail_scanners.go, cloudtrail_resolvers.go)
	TypeCloudTrailTrail          = "aws:cloudtrail:trail"
	TypeCloudTrailEventDataStore = "aws:cloudtrail:event-data-store"
	// StepFunctions (sfn_scanners.go, sfn_resolvers.go)
	TypeSFNStateMachine = "aws:sfn:state-machine"
	TypeSFNActivity     = "aws:sfn:activity"
	// Cognito (cognito_scanners.go, cognito_resolvers.go)
	TypeCognitoUserPool     = "aws:cognito:user-pool"
	TypeCognitoIdentityPool = "aws:cognito:identity-pool"
	TypeCognitoAppClient    = "aws:cognito:app-client"
	// EKS
	TypeEKSCluster = "aws:eks:cluster"
	// Classic ELB (elb_classic_scanners.go, elb_classic_resolvers.go)
	TypeELBClassicLoadBalancer = "aws:elasticloadbalancing:load-balancer"
	// ELBv2 (elb_scanners.go, elb_resolvers.go)
	TypeELBv2LoadBalancer         = "aws:elasticloadbalancingv2:load-balancer"
	TypeELBv2Listener             = "aws:elasticloadbalancingv2:listener"
	TypeELBv2ListenerCertificate  = "aws:elasticloadbalancingv2:listener-certificate"
	TypeELBv2ListenerRule         = "aws:elasticloadbalancingv2:listener-rule"
	TypeELBv2TargetGroup          = "aws:elasticloadbalancingv2:target-group"
	TypeELBv2TrustStore           = "aws:elasticloadbalancingv2:trust-store"
	TypeELBv2TrustStoreRevocation = "aws:elasticloadbalancingv2:trust-store-revocation"
	// S3 — buckets (s3_scanners.go)
	TypeS3Bucket = "aws:s3:bucket"
	// S3 — extended (s3_scanners.go, s3control_scanners.go)
	TypeS3BucketPolicy                 = "aws:s3:bucket-policy"
	TypeS3AccessGrantsInstance         = "aws:s3:access-grants-instance"
	TypeS3AccessGrantsLocation         = "aws:s3:access-grants-location"
	TypeS3AccessGrant                  = "aws:s3:access-grant"
	TypeS3AccessPoint                  = "aws:s3:access-point"
	TypeS3MultiRegionAccessPoint       = "aws:s3:multi-region-access-point"
	TypeS3MultiRegionAccessPointPolicy = "aws:s3:multi-region-access-point-policy"
	TypeS3StorageLens                  = "aws:s3:storage-lens"
	TypeS3StorageLensGroup             = "aws:s3:storage-lens-group"
	// SNS
	TypeSNSTopic = "aws:sns:topic"
	// SQS
	TypeSQSQueue = "aws:sqs:queue"
	// ECS
	TypeECSCluster        = "aws:ecs:cluster"
	TypeECSService        = "aws:ecs:service"
	TypeECSTaskDefinition = "aws:ecs:task-definition"
	// ECR
	TypeECRRepository = "aws:ecr:repository"
	// ElastiCache
	TypeElastiCacheReplicationGroup       = "aws:elasticache:replication-group"
	TypeElastiCacheCacheCluster           = "aws:elasticache:cache-cluster"
	TypeElastiCacheGlobalReplicationGroup = "aws:elasticache:global-replication-group"
	TypeElastiCacheParameterGroup         = "aws:elasticache:parameter-group"
	TypeElastiCacheSecurityGroup          = "aws:elasticache:security-group"
	TypeElastiCacheServerlessCache        = "aws:elasticache:serverless-cache"
	TypeElastiCacheSubnetGroup            = "aws:elasticache:subnet-group"
	TypeElastiCacheUser                   = "aws:elasticache:user"
	TypeElastiCacheUserGroup              = "aws:elasticache:user-group"
	// CloudFront
	TypeCloudFrontDistribution               = "aws:cloudfront:distribution"
	TypeCloudFrontAnycastIPList              = "aws:cloudfront:anycast-ip-list"
	TypeCloudFrontCachePolicy                = "aws:cloudfront:cache-policy"
	TypeCloudFrontOAI                        = "aws:cloudfront:cloud-front-origin-access-identity"
	TypeCloudFrontConnectionFunction         = "aws:cloudfront:connection-function"
	TypeCloudFrontConnectionGroup            = "aws:cloudfront:connection-group"
	TypeCloudFrontContinuousDeploymentPolicy = "aws:cloudfront:continuous-deployment-policy"
	TypeCloudFrontDistributionTenant         = "aws:cloudfront:distribution-tenant"
	TypeCloudFrontFunction                   = "aws:cloudfront:function"
	TypeCloudFrontKeyGroup                   = "aws:cloudfront:key-group"
	TypeCloudFrontKeyValueStore              = "aws:cloudfront:key-value-store"
	TypeCloudFrontMonitoringSubscription     = "aws:cloudfront:monitoring-subscription"
	TypeCloudFrontOriginAccessControl        = "aws:cloudfront:origin-access-control"
	TypeCloudFrontOriginRequestPolicy        = "aws:cloudfront:origin-request-policy"
	TypeCloudFrontPublicKey                  = "aws:cloudfront:public-key"
	TypeCloudFrontRealtimeLogConfig          = "aws:cloudfront:realtime-log-config"
	TypeCloudFrontResponseHeadersPolicy      = "aws:cloudfront:response-headers-policy"
	TypeCloudFrontStreamingDistribution      = "aws:cloudfront:streaming-distribution"
	TypeCloudFrontTrustStore                 = "aws:cloudfront:trust-store"
	TypeCloudFrontVpcOrigin                  = "aws:cloudfront:vpc-origin"
	// Route53
	TypeRoute53HostedZone     = "aws:route53:hosted-zone"
	TypeRoute53RecordSet      = "aws:route53:record-set"
	TypeRoute53CIDRCollection = "aws:route53:cidr-collection"
	TypeRoute53DNSSEC         = "aws:route53:dnssec"
	TypeRoute53HealthCheck    = "aws:route53:health-check"
	TypeRoute53KeySigningKey  = "aws:route53:key-signing-key"
	// API Gateway v1 (apigateway_scanners.go, apigateway_resolvers.go)
	TypeAPIGatewayRestAPI               = "aws:apigateway:rest-api"
	TypeAPIGatewayAccount               = "aws:apigateway:account"
	TypeAPIGatewayAPIKey                = "aws:apigateway:api-key"
	TypeAPIGatewayAuthorizer            = "aws:apigateway:authorizer"
	TypeAPIGatewayBasePathMapping       = "aws:apigateway:base-path-mapping"
	TypeAPIGatewayClientCertificate     = "aws:apigateway:client-certificate"
	TypeAPIGatewayDeployment            = "aws:apigateway:deployment"
	TypeAPIGatewayDocumentationPart     = "aws:apigateway:documentation-part"
	TypeAPIGatewayDocumentationVersion  = "aws:apigateway:documentation-version"
	TypeAPIGatewayDomainName            = "aws:apigateway:domain-name"
	TypeAPIGatewayDomainNameAccessAssoc = "aws:apigateway:domain-name-access-association"
	TypeAPIGatewayGatewayResponse       = "aws:apigateway:gateway-response"
	TypeAPIGatewayMethod                = "aws:apigateway:method"
	TypeAPIGatewayModel                 = "aws:apigateway:model"
	TypeAPIGatewayRequestValidator      = "aws:apigateway:request-validator"
	TypeAPIGatewayResource              = "aws:apigateway:resource"
	TypeAPIGatewayStage                 = "aws:apigateway:stage"
	TypeAPIGatewayUsagePlan             = "aws:apigateway:usage-plan"
	TypeAPIGatewayUsagePlanKey          = "aws:apigateway:usage-plan-key"
	TypeAPIGatewayVpcLink               = "aws:apigateway:vpc-link"
	// API Gateway v2 (apigateway_scanners.go)
	TypeAPIGatewayV2API                  = "aws:apigatewayv2:api"
	TypeAPIGatewayV2Authorizer           = "aws:apigatewayv2:authorizer"
	TypeAPIGatewayBasePathMappingV2      = "aws:apigatewayv2:base-path-mapping"
	TypeAPIGatewayDomainNameV2           = "aws:apigatewayv2:domain-name"
	TypeAPIGatewayPrivateDomainName      = "aws:apigateway:domain-name-v2"
	TypeAPIGatewayPrivateBasePathMapping = "aws:apigateway:base-path-mapping-v2"
	// CloudWatch (cloudwatch_scanners.go)
	TypeCloudWatchAlarm           = "aws:cloudwatch:alarm"
	TypeCloudWatchAlarmMuteRule   = "aws:cloudwatch:alarm-mute-rule"
	TypeCloudWatchAnomalyDetector = "aws:cloudwatch:anomaly-detector"
	TypeCloudWatchCompositeAlarm  = "aws:cloudwatch:composite-alarm"
	TypeCloudWatchDashboard       = "aws:cloudwatch:dashboard"
	TypeCloudWatchInsightRule     = "aws:cloudwatch:insight-rule"
	TypeCloudWatchMetricStream    = "aws:cloudwatch:metric-stream"
	// CloudWatch Logs (logs_scanners.go)
	TypeLogsAccountPolicy      = "aws:logs:account-policy"
	TypeLogsDelivery           = "aws:logs:delivery"
	TypeLogsDeliveryDest       = "aws:logs:delivery-destination"
	TypeLogsDeliverySource     = "aws:logs:delivery-source"
	TypeLogsDestination        = "aws:logs:destination"
	TypeLogsIntegration        = "aws:logs:integration"
	TypeLogsLogAnomalyDetector = "aws:logs:log-anomaly-detector"
	TypeLogsLogGroup           = "aws:logs:log-group"
	TypeLogsLogStream          = "aws:logs:log-stream"
	TypeLogsMetricFilter       = "aws:logs:metric-filter"
	TypeLogsQueryDefinition    = "aws:logs:query-definition"
	TypeLogsResourcePolicy     = "aws:logs:resource-policy"
	TypeLogsScheduledQuery     = "aws:logs:scheduled-query"
	TypeLogsSubscriptionFilter = "aws:logs:subscription-filter"
	TypeLogsTransformer        = "aws:logs:transformer"

	// KMS (kms_scanners.go)
	TypeKMSKey   = "aws:kms:key"
	TypeKMSAlias = "aws:kms:alias"
	TypeKMSGrant = "aws:kms:grant"
	// MSK / Kafka (kafka_scanners.go)
	TypeMSKCluster = "aws:kafka:cluster"
	// Secrets Manager (secretsmanager_scanners.go)
	TypeSecretsManagerSecret = "aws:secretsmanager:secret"
	// Organizations (organizations_scanners.go)
	TypeOrganization         = "aws:organizations:organization"
	TypeOrganizationsAccount = "aws:organizations:account"
	TypeOrganizationsOU      = "aws:organizations:ou"
	TypeOrganizationsSCP     = "aws:organizations:scp"
	// ACM (acm_scanners.go)
	TypeACMCertificate = "aws:acm:certificate"
	TypeACMPrivateCA   = "aws:acm:private-ca"
	// Kinesis Data Streams (kinesis_scanners.go)
	TypeKinesisStream = "aws:kinesis:stream"
	// Firehose (firehose_scanners.go)
	TypeFirehoseDeliveryStream = "aws:firehose:delivery-stream"
	// SSM (ssm_scanners.go)
	TypeSSMParameter     = "aws:ssm:parameter"
	TypeSSMDocument      = "aws:ssm:document"
	TypeSSMPatchBaseline = "aws:ssm:patch-baseline"
	// GuardDuty (guardduty_scanners.go)
	TypeGuardDutyDetector = "aws:guardduty:detector"
	TypeGuardDutyFilter   = "aws:guardduty:filter"
	TypeGuardDutyIPSet    = "aws:guardduty:ipset"
	TypeGuardDutyMember   = "aws:guardduty:member"
	// Config (config_scanners.go)
	TypeConfigRecorder        = "aws:config:recorder"
	TypeConfigDeliveryChannel = "aws:config:delivery-channel"
	TypeConfigRule            = "aws:config:rule"
	// Backup (backup_scanners.go)
	TypeBackupVault                   = "aws:backup:vault"
	TypeBackupLogicallyAirGappedVault = "aws:backup:logically-air-gapped-vault"
	TypeBackupPlan                    = "aws:backup:plan"
	TypeBackupSelection               = "aws:backup:selection"
	// Backup Gateway (backupgateway_scanners.go)
	TypeBackupGatewayHypervisor = "aws:backupgateway:hypervisor"
	// BCM Data Exports (bcmdataexports_scanners.go)
	TypeBCMDataExportsExport = "aws:bcmdataexports:export"
	// BCM Pricing Calculator (bcmpricingcalculator_scanners.go)
	TypeBcmPricingCalculatorBillScenario = "aws:bcmpricingcalculator:bill-scenario"
	// Billing (billing_scanners.go, billing_resolvers.go)
	TypeBillingView = "aws:billing:billing-view"
	// Billing Conductor (billingconductor_scanners.go, billingconductor_resolvers.go)
	TypeBillingConductorBillingGroup   = "aws:billingconductor:billing-group"
	TypeBillingConductorCustomLineItem = "aws:billingconductor:custom-line-item"
	TypeBillingConductorPricingPlan    = "aws:billingconductor:pricing-plan"
	TypeBillingConductorPricingRule    = "aws:billingconductor:pricing-rule"
	// Braket (braket_scanners.go, braket_resolvers.go)
	TypeBraketSpendingLimit = "aws:braket:spending-limit"
	// CloudFormation (cloudformation_scanners.go, cloudformation_resolvers.go)
	TypeCloudFormationStack    = "aws:cloudformation:stack"
	TypeCloudFormationStackSet = "aws:cloudformation:stack-set"
	// IAM Identity Center / Identity Store (sso_scanners.go, sso_resolvers.go)
	TypeSSOInstance          = "aws:sso:instance"
	TypeSSOPermissionSet     = "aws:sso:permission-set"
	TypeSSOAccountAssignment = "aws:sso:account-assignment"
	TypeIdentityStoreUser    = "aws:identitystore:user"
	TypeIdentityStoreGroup   = "aws:identitystore:group"
	// Macie (macie_scanners.go, macie_resolvers.go)
	TypeMacieSession              = "aws:macie:session"
	TypeMacieClassificationJob    = "aws:macie:classification-job"
	TypeMacieCustomDataIdentifier = "aws:macie:custom-data-identifier"
	TypeMacieAllowList            = "aws:macie:allow-list"
	// SageMaker — Studio (sagemaker_scanners.go)
	TypeSageMakerDomain                = "aws:sagemaker:domain"
	TypeSageMakerUserProfile           = "aws:sagemaker:user-profile"
	TypeSageMakerSpace                 = "aws:sagemaker:space"
	TypeSageMakerApp                   = "aws:sagemaker:app"
	TypeSageMakerAppImageConfig        = "aws:sagemaker:app-image-config"
	TypeSageMakerStudioLifecycleConfig = "aws:sagemaker:studio-lifecycle-config"
	// SageMaker — Training/Notebook (sagemaker_training_scanners.go)
	TypeSageMakerNotebookInstance                = "aws:sagemaker:notebook-instance"
	TypeSageMakerNotebookInstanceLifecycleConfig = "aws:sagemaker:notebook-instance-lifecycle-config"
	TypeSageMakerCodeRepository                  = "aws:sagemaker:code-repository"
	TypeSageMakerProcessingJob                   = "aws:sagemaker:processing-job"
	// SageMaker — Inference (sagemaker_inference_scanners.go)
	TypeSageMakerEndpoint            = "aws:sagemaker:endpoint"
	TypeSageMakerEndpointConfig      = "aws:sagemaker:endpoint-config"
	TypeSageMakerModel               = "aws:sagemaker:model"
	TypeSageMakerInferenceComponent  = "aws:sagemaker:inference-component"
	TypeSageMakerInferenceExperiment = "aws:sagemaker:inference-experiment"
	// SageMaker — Model registry (sagemaker_registry_scanners.go)
	TypeSageMakerModelPackage         = "aws:sagemaker:model-package"
	TypeSageMakerModelPackageGroup    = "aws:sagemaker:model-package-group"
	TypeSageMakerModelCard            = "aws:sagemaker:model-card"
	TypeSageMakerFeatureGroup         = "aws:sagemaker:feature-group"
	TypeSageMakerMlflowTrackingServer = "aws:sagemaker:mlflow-tracking-server"
	// SageMaker — Monitoring (sagemaker_monitoring_scanners.go)
	TypeSageMakerMonitoringSchedule               = "aws:sagemaker:monitoring-schedule"
	TypeSageMakerDataQualityJobDefinition         = "aws:sagemaker:data-quality-job-definition"
	TypeSageMakerModelBiasJobDefinition           = "aws:sagemaker:model-bias-job-definition"
	TypeSageMakerModelExplainabilityJobDefinition = "aws:sagemaker:model-explainability-job-definition"
	TypeSageMakerModelQualityJobDefinition        = "aws:sagemaker:model-quality-job-definition"
	// SageMaker — Pipelines (sagemaker_pipelines_scanners.go)
	TypeSageMakerPipeline   = "aws:sagemaker:pipeline"
	TypeSageMakerProject    = "aws:sagemaker:project"
	TypeSageMakerPartnerApp = "aws:sagemaker:partner-app"
	// SageMaker — Edge / images (sagemaker_edge_scanners.go)
	TypeSageMakerDeviceFleet  = "aws:sagemaker:device-fleet"
	TypeSageMakerDevice       = "aws:sagemaker:device"
	TypeSageMakerImage        = "aws:sagemaker:image"
	TypeSageMakerImageVersion = "aws:sagemaker:image-version"
	// SageMaker — Misc (sagemaker_misc_scanners.go)
	TypeSageMakerCluster  = "aws:sagemaker:cluster"
	TypeSageMakerWorkteam = "aws:sagemaker:workteam"
	// Connect — Core (connect_core_scanners.go)
	TypeConnectInstance                 = "aws:connect:instance"
	TypeConnectTrafficDistributionGroup = "aws:connect:traffic-distribution-group"
	TypeConnectPhoneNumber              = "aws:connect:phone-number"
	TypeConnectEmailAddress             = "aws:connect:email-address"
	// Connect — Routing (connect_routing_scanners.go)
	TypeConnectQueue            = "aws:connect:queue"
	TypeConnectRoutingProfile   = "aws:connect:routing-profile"
	TypeConnectHoursOfOperation = "aws:connect:hours-of-operation"
	TypeConnectAgentStatus      = "aws:connect:agent-status"
	TypeConnectQuickConnect     = "aws:connect:quick-connect"
	// Connect — Users (connect_users_scanners.go)
	TypeConnectUser                   = "aws:connect:user"
	TypeConnectUserHierarchyGroup     = "aws:connect:user-hierarchy-group"
	TypeConnectUserHierarchyStructure = "aws:connect:user-hierarchy-structure"
	TypeConnectSecurityProfile        = "aws:connect:security-profile"
	TypeConnectPredefinedAttribute    = "aws:connect:predefined-attribute"
	// Connect — Flows (connect_flows_scanners.go)
	TypeConnectContactFlow              = "aws:connect:contact-flow"
	TypeConnectContactFlowVersion       = "aws:connect:contact-flow-version"
	TypeConnectContactFlowModule        = "aws:connect:contact-flow-module"
	TypeConnectContactFlowModuleVersion = "aws:connect:contact-flow-module-version"
	TypeConnectContactFlowModuleAlias   = "aws:connect:contact-flow-module-alias"
	// Connect — Integration (connect_integration_scanners.go)
	TypeConnectApprovedOrigin         = "aws:connect:approved-origin"
	TypeConnectSecurityKey            = "aws:connect:security-key"
	TypeConnectInstanceStorageConfig  = "aws:connect:instance-storage-config"
	TypeConnectIntegrationAssociation = "aws:connect:integration-association"
	TypeConnectNotification           = "aws:connect:notification"
	TypeConnectRule                   = "aws:connect:rule"
	// Connect — Workspace (connect_workspace_scanners.go)
	TypeConnectTaskTemplate   = "aws:connect:task-template"
	TypeConnectEvaluationForm = "aws:connect:evaluation-form"
	TypeConnectView           = "aws:connect:view"
	TypeConnectViewVersion    = "aws:connect:view-version"
	TypeConnectWorkspace      = "aws:connect:workspace"
	TypeConnectPrompt         = "aws:connect:prompt"
	// Connect — DataTable (connect_datatable_scanners.go)
	TypeConnectDataTable          = "aws:connect:data-table"
	TypeConnectDataTableAttribute = "aws:connect:data-table-attribute"
	TypeConnectDataTableRecord    = "aws:connect:data-table-record"
	// Shield Advanced (shield_scanners.go, shield_resolvers.go)
	TypeShieldProtection      = "aws:shield:protection"
	TypeShieldProtectionGroup = "aws:shield:protection-group"
	// Security Hub (securityhub_scanners.go, securityhub_resolvers.go)
	TypeSecurityHubHub                   = "aws:securityhub:hub"
	TypeSecurityHubInsight               = "aws:securityhub:insight"
	TypeSecurityHubStandardsSubscription = "aws:securityhub:standards-subscription"
	TypeSecurityHubProductSubscription   = "aws:securityhub:product-subscription"
	// Detective (detective_scanners.go, detective_resolvers.go)
	TypeDetectiveGraph  = "aws:detective:graph"
	TypeDetectiveMember = "aws:detective:member"
	// Lake Formation (lakeformation_scanners.go, lakeformation_resolvers.go)
	TypeLakeFormationResource = "aws:lakeformation:resource"
	// SES v2 (ses_scanners.go, ses_resolvers.go)
	TypeSESEmailIdentity    = "aws:ses:email-identity"
	TypeSESConfigurationSet = "aws:ses:configuration-set"
	// SES v2 — extended (ses_v2_extended_scanners.go)
	TypeSESConfigurationSetEventDestination = "aws:ses:configuration-set-event-destination"
	TypeSESContactList                      = "aws:ses:contact-list"
	TypeSESCustomVerificationEmailTemplate  = "aws:ses:custom-verification-email-template"
	TypeSESDedicatedIpPool                  = "aws:ses:dedicated-ip-pool"
	TypeSESMultiRegionEndpoint              = "aws:ses:multi-region-endpoint"
	TypeSESTemplate                         = "aws:ses:template"
	TypeSESTenant                           = "aws:ses:tenant"
	TypeSESVdmAttributes                    = "aws:ses:vdm-attributes"
	// SES v1 — Receipt family (ses_receipt_scanners.go)
	TypeSESReceiptFilter  = "aws:ses:receipt-filter"
	TypeSESReceiptRule    = "aws:ses:receipt-rule"
	TypeSESReceiptRuleSet = "aws:ses:receipt-rule-set"
	// SES — MailManager family (ses_mailmanager_scanners.go)
	TypeSESMailManagerAddonInstance     = "aws:ses:mailmanager-addon-instance"
	TypeSESMailManagerAddonSubscription = "aws:ses:mailmanager-addon-subscription"
	TypeSESMailManagerAddressList       = "aws:ses:mailmanager-address-list"
	TypeSESMailManagerArchive           = "aws:ses:mailmanager-archive"
	TypeSESMailManagerIngressPoint      = "aws:ses:mailmanager-ingress-point"
	TypeSESMailManagerRelay             = "aws:ses:mailmanager-relay"
	TypeSESMailManagerRuleSet           = "aws:ses:mailmanager-rule-set"
	TypeSESMailManagerTrafficPolicy     = "aws:ses:mailmanager-traffic-policy"
	// Inspector v2 (inspector_scanners.go, inspector_resolvers.go)
	TypeInspector2Filter = "aws:inspector2:filter"
	TypeInspector2Member = "aws:inspector2:member"
	// Glue Data Catalog (glue_scanners.go, glue_resolvers.go)
	TypeGlueDatabase = "aws:glue:database"
	TypeGlueTable    = "aws:glue:table"
	// Bedrock — Agents family (bedrock_agents_scanners.go)
	TypeBedrockAgent         = "aws:bedrock:agent"
	TypeBedrockAgentAlias    = "aws:bedrock:agent-alias"
	TypeBedrockKnowledgeBase = "aws:bedrock:knowledge-base"
	TypeBedrockDataSource    = "aws:bedrock:data-source"
	TypeBedrockFlow          = "aws:bedrock:flow"
	TypeBedrockFlowAlias     = "aws:bedrock:flow-alias"
	TypeBedrockFlowVersion   = "aws:bedrock:flow-version"
	TypeBedrockPrompt        = "aws:bedrock:prompt"
	TypeBedrockPromptVersion = "aws:bedrock:prompt-version"
	// Bedrock — Foundation family (bedrock_foundation_scanners.go)
	TypeBedrockGuardrail                       = "aws:bedrock:guardrail"
	TypeBedrockGuardrailVersion                = "aws:bedrock:guardrail-version"
	TypeBedrockAutomatedReasoningPolicy        = "aws:bedrock:automated-reasoning-policy"
	TypeBedrockAutomatedReasoningPolicyVersion = "aws:bedrock:automated-reasoning-policy-version"
	TypeBedrockIntelligentPromptRouter         = "aws:bedrock:intelligent-prompt-router"
	TypeBedrockApplicationInferenceProfile     = "aws:bedrock:application-inference-profile"
	TypeBedrockEnforcedGuardrailConfiguration  = "aws:bedrock:enforced-guardrail-configuration"
	// Glue — Schema (glue_schema_scanners.go)
	TypeGlueRegistry              = "aws:glue:registry"
	TypeGlueSchema                = "aws:glue:schema"
	TypeGlueSchemaVersion         = "aws:glue:schema-version"
	TypeGlueSchemaVersionMetadata = "aws:glue:schema-version-metadata"
	// Glue — Catalog family (glue_catalog_scanners.go)
	TypeGlueCrawler    = "aws:glue:crawler"
	TypeGlueConnection = "aws:glue:connection"
	TypeGlueClassifier = "aws:glue:classifier"
	// Glue — Misc family (glue_misc_scanners.go)
	TypeGlueCatalog                       = "aws:glue:catalog"
	TypeGlueCustomEntityType              = "aws:glue:custom-entity-type"
	TypeGlueDataCatalogEncryptionSettings = "aws:glue:data-catalog-encryption-settings"
	TypeGlueDataQualityRuleset            = "aws:glue:data-quality-ruleset"
	TypeGlueIdentityCenterConfiguration   = "aws:glue:identity-center-configuration"
	TypeGlueIntegration                   = "aws:glue:integration"
	TypeGlueIntegrationResourceProperty   = "aws:glue:integration-resource-property"
	TypeGlueSecurityConfiguration         = "aws:glue:security-configuration"
	TypeGlueUsageProfile                  = "aws:glue:usage-profile"
	// Glue — Jobs family (glue_jobs_scanners.go)
	TypeGlueJob         = "aws:glue:job"
	TypeGlueTrigger     = "aws:glue:trigger"
	TypeGlueWorkflow    = "aws:glue:workflow"
	TypeGlueMLTransform = "aws:glue:ml-transform"
	TypeGlueDevEndpoint = "aws:glue:dev-endpoint"
	// Athena (athena_scanners.go, athena_resolvers.go)
	TypeAthenaWorkgroup   = "aws:athena:workgroup"
	TypeAthenaDataCatalog = "aws:athena:datacatalog"
	// Redshift (redshift_scanners.go, redshift_resolvers.go)
	TypeRedshiftCluster     = "aws:redshift:cluster"
	TypeRedshiftSubnetGroup = "aws:redshift:subnet-group"
	// OpenSearch (opensearch_scanners.go, opensearch_resolvers.go)
	TypeOpenSearchDomain = "aws:opensearch:domain"
	// DocumentDB (docdb_scanners.go, docdb_resolvers.go)
	TypeDocDBCluster  = "aws:docdb:cluster"
	TypeDocDBInstance = "aws:docdb:instance"
	// Neptune (neptune_scanners.go, neptune_resolvers.go)
	TypeNeptuneCluster  = "aws:neptune:cluster"
	TypeNeptuneInstance = "aws:neptune:instance"
	// Service Catalog (servicecatalog_scanners.go, servicecatalog_resolvers.go)
	TypeServiceCatalogPortfolio = "aws:servicecatalog:portfolio"
	TypeServiceCatalogProduct   = "aws:servicecatalog:product"
	// Audit Manager (auditmanager_scanners.go, auditmanager_resolvers.go)
	TypeAuditManagerAssessment = "aws:auditmanager:assessment"
	TypeAuditManagerFramework  = "aws:auditmanager:framework"
	TypeAuditManagerControl    = "aws:auditmanager:control"
	// Control Tower (controltower_scanners.go, controltower_resolvers.go)
	TypeControlTowerLandingZone     = "aws:controltower:landing-zone"
	TypeControlTowerEnabledBaseline = "aws:controltower:enabled-baseline"
	// App Runner (apprunner_scanners.go, apprunner_resolvers.go)
	TypeAppRunnerService                    = "aws:apprunner:service"
	TypeAppRunnerVPCConnector               = "aws:apprunner:vpc-connector"
	TypeAppRunnerAutoScalingConfiguration   = "aws:apprunner:auto-scaling-configuration"
	TypeAppRunnerObservabilityConfiguration = "aws:apprunner:observability-configuration"
	TypeAppRunnerVpcIngressConnection       = "aws:apprunner:vpc-ingress-connection"
	// Batch (batch_scanners.go, batch_resolvers.go)
	TypeBatchComputeEnvironment = "aws:batch:compute-environment"
	TypeBatchJobQueue           = "aws:batch:job-queue"
	TypeBatchJobDefinition      = "aws:batch:job-definition"
	TypeBatchSchedulingPolicy   = "aws:batch:scheduling-policy"
	TypeBatchConsumableResource = "aws:batch:consumable-resource"
	TypeBatchServiceEnvironment = "aws:batch:service-environment"
	TypeBatchQuotaShare         = "aws:batch:quota-share"
	// Lightsail (lightsail_scanners.go)
	TypeLightsailInstance         = "aws:lightsail:instance"
	TypeLightsailDatabase         = "aws:lightsail:database"
	TypeLightsailContainerService = "aws:lightsail:container-service"
	// Elastic Beanstalk (elasticbeanstalk_scanners.go, elasticbeanstalk_resolvers.go)
	TypeBeanstalkApplication = "aws:elasticbeanstalk:application"
	TypeBeanstalkEnvironment = "aws:elasticbeanstalk:environment"
	// IAM Access Analyzer (accessanalyzer_scanners.go, accessanalyzer_resolvers.go)
	TypeAccessAnalyzerAnalyzer = "aws:accessanalyzer:analyzer"
	// ACM Private CA permissions (acmpca_scanners.go, acmpca_resolvers.go)
	TypeACMPCAPermission = "aws:acm-pca:permission"
	// CloudWatch AIOps investigation groups (aiops_scanners.go, aiops_resolvers.go)
	TypeAIOpsInvestigationGroup = "aws:aiops:investigation-group"
	// Amazon MQ (mq_scanners.go, mq_resolvers.go)
	TypeMQBroker                   = "aws:mq:broker"
	TypeMQConfiguration            = "aws:mq:configuration"
	TypeMQConfigurationAssociation = "aws:mq:configuration-association"
	// Amplify (amplify_scanners.go, amplify_resolvers.go)
	TypeAmplifyApp    = "aws:amplify:app"
	TypeAmplifyBranch = "aws:amplify:branch"
	TypeAmplifyDomain = "aws:amplify:domain"
	// API Gateway v2 — VPC Link (apigateway_v2_scanners.go); separate from
	// the v1 TypeAPIGatewayVpcLink which targets a different SDK module
	// (apigateway, not apigatewayv2).
	TypeAPIGatewayV2VpcLink = "aws:apigatewayv2:vpc-link"
	// AppFlow (appflow_scanners.go, appflow_resolvers.go)
	// Application Auto Scaling (applicationautoscaling_scanners.go)
	TypeApplicationAutoScalingScalableTarget = "aws:application-autoscaling:scalable-target"
	TypeApplicationAutoScalingScalingPolicy  = "aws:application-autoscaling:scaling-policy"
	TypeAppFlowFlow                          = "aws:appflow:flow"
	TypeAppFlowConnector                     = "aws:appflow:connector"
	TypeAppFlowConnectorProfile              = "aws:appflow:connector-profile"
	// AppIntegrations (appintegrations_scanners.go, appintegrations_resolvers.go)
	TypeAppIntegrationsApplication      = "aws:appintegrations:application"
	TypeAppIntegrationsDataIntegration  = "aws:appintegrations:data-integration"
	TypeAppIntegrationsEventIntegration = "aws:appintegrations:event-integration"
	// CloudWatch Application Insights (applicationinsights_scanners.go, applicationinsights_resolvers.go)
	TypeApplicationInsightsApplication = "aws:applicationinsights:application"
	// CloudWatch Application Signals (applicationsignals_scanners.go, applicationsignals_resolvers.go)
	TypeApplicationSignalsSLO                   = "aws:applicationsignals:service-level-objective"
	TypeApplicationSignalsGroupingConfiguration = "aws:applicationsignals:grouping-configuration"
	// Amazon Managed Prometheus / APS (aps_scanners.go, aps_resolvers.go); SDK module is `amp`.
	TypeAPSWorkspace = "aws:aps:workspace"
	TypeAPSScraper   = "aws:aps:scraper"
	// ARC Region Switch (arcregionswitch_scanners.go, arcregionswitch_resolvers.go).
	TypeARCRegionSwitchPlan = "aws:arc-region-switch:plan"
	// ARC Zonal Shift (arczonalshift_scanners.go, arczonalshift_resolvers.go).
	TypeARCZonalShiftObserverStatus = "aws:arc-zonal-shift:autoshift-observer-notification-status"
	TypeARCZonalShiftConfiguration  = "aws:arc-zonal-shift:zonal-autoshift-configuration"
	// Athena capacity reservations (athena_scanners.go).
	TypeAthenaCapacityReservation = "aws:athena:capacity-reservation"
	// EC2 Auto Scaling (autoscaling_scanners.go, autoscaling_resolvers.go).
	TypeAutoScalingGroup               = "aws:autoscaling:auto-scaling-group"
	TypeAutoScalingLaunchConfiguration = "aws:autoscaling:launch-configuration"
	TypeAutoScalingLifecycleHook       = "aws:autoscaling:lifecycle-hook"
	TypeAutoScalingScalingPolicy       = "aws:autoscaling:scaling-policy"
	TypeAutoScalingScheduledAction     = "aws:autoscaling:scheduled-action"
	TypeAutoScalingWarmPool            = "aws:autoscaling:warm-pool"
	// AWS Auto Scaling (a.k.a. AutoScalingPlans). Disco service segment
	// "autoscaling-plans"; CFN segment "AutoScalingPlans".
	TypeAutoScalingPlansScalingPlan = "aws:autoscaling-plans:scaling-plan"
)
