package aws

// Resource type constants for all AWS resource types discovered by this provider.
// Using constants prevents typos in scanner and resolver files — a mismatched
// string would create orphan resources with an undeclared type.
const (
	// EC2 — compute management (ec2_compute_mgmt_scanners.go)
	TypeEC2Instance                = "aws:ec2:instance"
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
	// EventBridge (eventbridge_scanners.go, eventbridge_resolvers.go)
	TypeEventsEventBus = "aws:events:event-bus"
	TypeEventsRule     = "aws:events:rule"
	// CloudTrail (cloudtrail_scanners.go, cloudtrail_resolvers.go)
	TypeCloudTrailTrail = "aws:cloudtrail:trail"
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
	TypeCloudFrontAnycastIpList              = "aws:cloudfront:anycast-ip-list"
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
	TypeAPIGatewayV2API             = "aws:apigatewayv2:api"
	TypeAPIGatewayBasePathMappingV2 = "aws:apigatewayv2:base-path-mapping"
	TypeAPIGatewayDomainNameV2      = "aws:apigatewayv2:domain-name"
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
	// Config (config_scanners.go)
	TypeConfigRecorder        = "aws:config:recorder"
	TypeConfigDeliveryChannel = "aws:config:delivery-channel"
	TypeConfigRule            = "aws:config:rule"
	// Backup (backup_scanners.go)
	TypeBackupVault     = "aws:backup:vault"
	TypeBackupPlan      = "aws:backup:plan"
	TypeBackupSelection = "aws:backup:selection"
)

// KnownTypes returns all disco type strings currently covered by this provider.
// Used by the types command for gap-analysis against the CloudFormation registry.
func KnownTypes() []string {
	return []string{
		// EC2 — compute management
		TypeEC2Instance, TypeEC2SecurityGroup, TypeEC2Volume,
		TypeEC2LaunchTemplate, TypeEC2KeyPair, TypeEC2PlacementGroup,
		TypeEC2SpotFleet, TypeEC2Host, TypeEC2CapacityReservation,
		TypeEC2InstanceConnectEndpoint,
		// EC2 — networking
		TypeEC2VPC, TypeEC2Subnet, TypeEC2InternetGateway,
		TypeEC2NatGateway, TypeEC2RouteTable, TypeEC2EIP,
		TypeEC2NetworkInterface, TypeEC2NetworkACL,
		TypeEC2VPCEndpoint, TypeEC2VPCPeeringConnection,
		TypeEC2DHCPOptions, TypeEC2EgressOnlyIGW,
		// EC2 — VPN
		TypeEC2CustomerGateway, TypeEC2VPNGateway, TypeEC2VPNConnection,
		TypeEC2TransitGateway, TypeEC2TransitGatewayAttachment,
		// EC2 — observability
		TypeEC2FlowLog, TypeEC2PrefixList,
		TypeEC2NetworkInsightsPath, TypeEC2NetworkInsightsAnalysis,
		TypeEC2NetworkInsightsAccessScope, TypeEC2NetworkInsightsAccessScopeAnalysis,
		// EC2 — IPAM
		TypeEC2IPAM, TypeEC2IPAMScope, TypeEC2IPAMPool,
		TypeEC2IPAMPoolCIDR, TypeEC2IPAMAllocation,
		TypeEC2IPAMResourceDiscovery, TypeEC2IPAMResourceDiscoveryAssociation,
		// EC2 — Transit Gateway extended
		TypeEC2TransitGatewayConnect, TypeEC2TransitGatewayConnectPeer,
		TypeEC2TransitGatewayMulticastDomain, TypeEC2TransitGatewayMulticastDomainAssociation,
		TypeEC2TransitGatewayMulticastGroupMember, TypeEC2TransitGatewayMulticastGroupSource,
		TypeEC2TransitGatewayPeeringAttachment, TypeEC2TransitGatewayRouteTable,
		TypeEC2TransitGatewayRouteTableAssociation, TypeEC2TransitGatewayRouteTablePropagation,
		TypeEC2TransitGatewayVPCAttachment, TypeEC2TransitGatewayRoute,
		// EC2 — Traffic Mirroring
		TypeEC2TrafficMirrorFilter, TypeEC2TrafficMirrorFilterRule,
		TypeEC2TrafficMirrorSession, TypeEC2TrafficMirrorTarget,
		// EC2 — Verified Access
		TypeEC2VerifiedAccessInstance, TypeEC2VerifiedAccessTrustProvider,
		TypeEC2VerifiedAccessGroup, TypeEC2VerifiedAccessEndpoint,
		// EC2 — Local Gateway
		TypeEC2LocalGatewayRouteTable, TypeEC2LocalGatewayRoute,
		TypeEC2LocalGatewayVirtualInterface, TypeEC2LocalGatewayVirtualInterfaceGroup,
		TypeEC2LocalGatewayRouteTableVPCAssociation, TypeEC2LocalGatewayRouteTableVIGAssociation,
		// EC2 — Client VPN
		TypeEC2ClientVPNEndpoint, TypeEC2ClientVPNAuthorizationRule,
		TypeEC2ClientVPNRoute, TypeEC2ClientVPNTargetNetworkAssociation,
		// EC2 — extended resources
		TypeEC2CapacityReservationFleet, TypeEC2Fleet, TypeEC2CarrierGateway,
		TypeEC2VPCBlockPublicAccessOptions, TypeEC2VPCBlockPublicAccessExclusion,
		TypeEC2VPCEndpointConnectionNotification,
		TypeEC2VPCEndpointService, TypeEC2VPCEndpointServicePermissions,
		TypeEC2SecurityGroupVPCAssociation,
		TypeEC2NetworkInterfacePermission,
		TypeEC2SnapshotBlockPublicAccess,
		// IAM
		TypeIAMRole, TypeIAMUser, TypeIAMGroup, TypeIAMServiceLinkedRole,
		TypeIAMPolicy, TypeIAMRolePolicy, TypeIAMUserPolicy, TypeIAMGroupPolicy, TypeIAMAccessKey,
		TypeIAMInstanceProfile, TypeIAMOIDCProvider, TypeIAMSAMLProvider, TypeIAMServerCertificate, TypeIAMVirtualMFADevice,
		TypeLambdaFunction,
		TypeLambdaAlias, TypeLambdaCapacityProvider, TypeLambdaCodeSigningConfig,
		TypeLambdaEventInvokeConfig, TypeLambdaESM, TypeLambdaLayerVersion,
		TypeLambdaURL, TypeLambdaVersion,
		TypeRDSCustomDBEngineVersion,
		TypeRDSDBCluster, TypeRDSDBClusterParameterGroup,
		TypeRDSDBInstance, TypeRDSDBParameterGroup,
		TypeRDSDBProxy, TypeRDSDBProxyEndpoint, TypeRDSDBProxyTargetGroup,
		TypeRDSDBSecurityGroup, TypeRDSDBShardGroup, TypeRDSDBSubnetGroup,
		TypeRDSEventSubscription, TypeRDSGlobalCluster,
		TypeRDSIntegration, TypeRDSOptionGroup,
		TypeDynamoDBTable, TypeDynamoDBGlobalTable, TypeDynamoDBStream,
		TypeEKSCluster,
		TypeELBClassicLoadBalancer,
		TypeELBv2LoadBalancer, TypeELBv2Listener, TypeELBv2ListenerCertificate,
		TypeELBv2ListenerRule, TypeELBv2TargetGroup,
		TypeELBv2TrustStore, TypeELBv2TrustStoreRevocation,
		TypeS3Bucket, TypeS3BucketPolicy,
		TypeS3AccessGrantsInstance, TypeS3AccessGrantsLocation, TypeS3AccessGrant,
		TypeS3AccessPoint,
		TypeS3MultiRegionAccessPoint, TypeS3MultiRegionAccessPointPolicy,
		TypeS3StorageLens, TypeS3StorageLensGroup,
		TypeSNSTopic,
		TypeSQSQueue,
		TypeECSCluster, TypeECSService, TypeECSTaskDefinition,
		TypeECRRepository,
		TypeElastiCacheReplicationGroup, TypeElastiCacheCacheCluster,
		TypeElastiCacheGlobalReplicationGroup, TypeElastiCacheParameterGroup,
		TypeElastiCacheSecurityGroup, TypeElastiCacheServerlessCache,
		TypeElastiCacheSubnetGroup, TypeElastiCacheUser, TypeElastiCacheUserGroup,
		TypeCloudFrontDistribution,
		TypeCloudFrontAnycastIpList, TypeCloudFrontCachePolicy, TypeCloudFrontOAI,
		TypeCloudFrontConnectionFunction, TypeCloudFrontConnectionGroup,
		TypeCloudFrontContinuousDeploymentPolicy, TypeCloudFrontDistributionTenant,
		TypeCloudFrontFunction, TypeCloudFrontKeyGroup, TypeCloudFrontKeyValueStore,
		TypeCloudFrontMonitoringSubscription, TypeCloudFrontOriginAccessControl,
		TypeCloudFrontOriginRequestPolicy, TypeCloudFrontPublicKey,
		TypeCloudFrontRealtimeLogConfig, TypeCloudFrontResponseHeadersPolicy,
		TypeCloudFrontStreamingDistribution, TypeCloudFrontTrustStore, TypeCloudFrontVpcOrigin,
		TypeRoute53HostedZone, TypeRoute53RecordSet,
		TypeRoute53CIDRCollection, TypeRoute53DNSSEC,
		TypeRoute53HealthCheck, TypeRoute53KeySigningKey,
		TypeAPIGatewayRestAPI,
		TypeAPIGatewayAccount, TypeAPIGatewayAPIKey, TypeAPIGatewayAuthorizer,
		TypeAPIGatewayBasePathMapping, TypeAPIGatewayClientCertificate,
		TypeAPIGatewayDeployment, TypeAPIGatewayDocumentationPart, TypeAPIGatewayDocumentationVersion,
		TypeAPIGatewayDomainName, TypeAPIGatewayDomainNameAccessAssoc,
		TypeAPIGatewayGatewayResponse, TypeAPIGatewayMethod, TypeAPIGatewayModel,
		TypeAPIGatewayRequestValidator, TypeAPIGatewayResource, TypeAPIGatewayStage,
		TypeAPIGatewayUsagePlan, TypeAPIGatewayUsagePlanKey, TypeAPIGatewayVpcLink,
		TypeAPIGatewayV2API, TypeAPIGatewayBasePathMappingV2, TypeAPIGatewayDomainNameV2,
		// CloudWatch
		TypeCloudWatchAlarm, TypeCloudWatchAlarmMuteRule, TypeCloudWatchAnomalyDetector,
		TypeCloudWatchCompositeAlarm, TypeCloudWatchDashboard,
		TypeCloudWatchInsightRule, TypeCloudWatchMetricStream,
		// CloudWatch Logs
		TypeLogsAccountPolicy, TypeLogsDelivery, TypeLogsDeliveryDest,
		TypeLogsDeliverySource, TypeLogsDestination, TypeLogsIntegration,
		TypeLogsLogAnomalyDetector, TypeLogsLogGroup, TypeLogsLogStream,
		TypeLogsMetricFilter, TypeLogsQueryDefinition, TypeLogsResourcePolicy,
		TypeLogsScheduledQuery, TypeLogsSubscriptionFilter, TypeLogsTransformer,
		// KMS
		TypeKMSKey, TypeKMSAlias,
		// Secrets Manager
		TypeSecretsManagerSecret,
		// Organizations
		TypeOrganization, TypeOrganizationsAccount,
		TypeOrganizationsOU, TypeOrganizationsSCP,
		// ACM
		TypeACMCertificate, TypeACMPrivateCA,
		// Kinesis
		TypeKinesisStream,
		// Firehose
		TypeFirehoseDeliveryStream,
		// EFS
		TypeEFSFileSystem, TypeEFSMountTarget, TypeEFSAccessPoint,
		// WAFv2
		TypeWAFv2WebACL, TypeWAFv2RuleGroup, TypeWAFv2IPSet,
		// EventBridge
		TypeEventsEventBus, TypeEventsRule,
		// CloudTrail
		TypeCloudTrailTrail,
		// StepFunctions
		TypeSFNStateMachine, TypeSFNActivity,
		// Cognito
		TypeCognitoUserPool, TypeCognitoIdentityPool, TypeCognitoAppClient,
		// SSM
		TypeSSMParameter, TypeSSMDocument, TypeSSMPatchBaseline,
		// GuardDuty
		TypeGuardDutyDetector, TypeGuardDutyFilter, TypeGuardDutyIPSet,
		// Config
		TypeConfigRecorder, TypeConfigDeliveryChannel, TypeConfigRule,
		// Backup
		TypeBackupVault, TypeBackupPlan, TypeBackupSelection,
	}
}
