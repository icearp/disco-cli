package aws

// Resource type constants for all AWS resource types discovered by this provider.
// Using constants prevents typos in scanner and resolver files — a mismatched
// string would create orphan resources with an undeclared type.
const (
	// EC2 — originally covered
	TypeEC2Instance        = "aws:ec2:instance"
	TypeEC2VPC             = "aws:ec2:vpc"
	TypeEC2Subnet          = "aws:ec2:subnet"
	TypeEC2SecurityGroup   = "aws:ec2:security-group"
	TypeEC2Volume          = "aws:ec2:volume"
	TypeEC2InternetGateway = "aws:ec2:internet-gateway"
	// EC2 — networking extensions
	TypeEC2NatGateway               = "aws:ec2:nat-gateway"
	TypeEC2RouteTable               = "aws:ec2:route-table"
	TypeEC2EIP                      = "aws:ec2:eip"
	TypeEC2NetworkInterface         = "aws:ec2:network-interface"
	TypeEC2NetworkACL               = "aws:ec2:network-acl"
	TypeEC2VPCEndpoint              = "aws:ec2:vpc-endpoint"
	TypeEC2VPCPeeringConnection     = "aws:ec2:vpc-peering-connection"
	TypeEC2DHCPOptions              = "aws:ec2:dhcp-options"
	TypeEC2EgressOnlyIGW            = "aws:ec2:egress-only-internet-gateway"
	// EC2 — VPN / transit
	TypeEC2CustomerGateway          = "aws:ec2:customer-gateway"
	TypeEC2VPNGateway               = "aws:ec2:vpn-gateway"
	TypeEC2VPNConnection            = "aws:ec2:vpn-connection"
	TypeEC2TransitGateway           = "aws:ec2:transit-gateway"
	TypeEC2TransitGatewayAttachment = "aws:ec2:transit-gateway-attachment"
	// EC2 — compute management
	TypeEC2LaunchTemplate         = "aws:ec2:launch-template"
	TypeEC2KeyPair                = "aws:ec2:key-pair"
	TypeEC2PlacementGroup         = "aws:ec2:placement-group"
	TypeEC2SpotFleet              = "aws:ec2:spot-fleet"
	TypeEC2Host                   = "aws:ec2:host"
	TypeEC2CapacityReservation    = "aws:ec2:capacity-reservation"
	TypeEC2InstanceConnectEndpoint = "aws:ec2:instance-connect-endpoint"
	// EC2 — observability / policy
	TypeEC2FlowLog    = "aws:ec2:flow-log"
	TypeEC2PrefixList = "aws:ec2:prefix-list"
	// EC2 — IPAM
	TypeEC2IPAM                             = "aws:ec2:ipam"
	TypeEC2IPAMScope                        = "aws:ec2:ipam-scope"
	TypeEC2IPAMPool                         = "aws:ec2:ipam-pool"
	TypeEC2IPAMPoolCIDR                     = "aws:ec2:ipam-pool-cidr"
	TypeEC2IPAMAllocation                   = "aws:ec2:ipam-allocation"
	TypeEC2IPAMResourceDiscovery            = "aws:ec2:ipam-resource-discovery"
	TypeEC2IPAMResourceDiscoveryAssociation = "aws:ec2:ipam-resource-discovery-association"
	// EC2 — Transit Gateway extended
	TypeEC2TransitGatewayConnect                           = "aws:ec2:transit-gateway-connect"
	TypeEC2TransitGatewayConnectPeer                       = "aws:ec2:transit-gateway-connect-peer"
	TypeEC2TransitGatewayMulticastDomain                   = "aws:ec2:transit-gateway-multicast-domain"
	TypeEC2TransitGatewayMulticastDomainAssociation        = "aws:ec2:transit-gateway-multicast-domain-association"
	TypeEC2TransitGatewayMulticastGroupMember              = "aws:ec2:transit-gateway-multicast-group-member"
	TypeEC2TransitGatewayMulticastGroupSource              = "aws:ec2:transit-gateway-multicast-group-source"
	TypeEC2TransitGatewayPeeringAttachment                 = "aws:ec2:transit-gateway-peering-attachment"
	TypeEC2TransitGatewayRouteTable                        = "aws:ec2:transit-gateway-route-table"
	TypeEC2TransitGatewayRouteTableAssociation             = "aws:ec2:transit-gateway-route-table-association"
	TypeEC2TransitGatewayRouteTablePropagation             = "aws:ec2:transit-gateway-route-table-propagation"
	TypeEC2TransitGatewayVPCAttachment                     = "aws:ec2:transit-gateway-vpc-attachment"
	TypeEC2TransitGatewayRoute                             = "aws:ec2:transit-gateway-route"
	// EC2 — Traffic Mirroring
	TypeEC2TrafficMirrorFilter     = "aws:ec2:traffic-mirror-filter"
	TypeEC2TrafficMirrorFilterRule = "aws:ec2:traffic-mirror-filter-rule"
	TypeEC2TrafficMirrorSession    = "aws:ec2:traffic-mirror-session"
	TypeEC2TrafficMirrorTarget     = "aws:ec2:traffic-mirror-target"
	// EC2 — Verified Access
	TypeEC2VerifiedAccessInstance      = "aws:ec2:verified-access-instance"
	TypeEC2VerifiedAccessTrustProvider = "aws:ec2:verified-access-trust-provider"
	TypeEC2VerifiedAccessGroup         = "aws:ec2:verified-access-group"
	TypeEC2VerifiedAccessEndpoint      = "aws:ec2:verified-access-endpoint"
	// EC2 — Network Insights
	TypeEC2NetworkInsightsPath                = "aws:ec2:network-insights-path"
	TypeEC2NetworkInsightsAnalysis            = "aws:ec2:network-insights-analysis"
	TypeEC2NetworkInsightsAccessScope         = "aws:ec2:network-insights-access-scope"
	TypeEC2NetworkInsightsAccessScopeAnalysis = "aws:ec2:network-insights-access-scope-analysis"
	// EC2 — Local Gateway
	TypeEC2LocalGatewayRouteTable                                 = "aws:ec2:local-gateway-route-table"
	TypeEC2LocalGatewayRoute                                      = "aws:ec2:local-gateway-route"
	TypeEC2LocalGatewayVirtualInterface                           = "aws:ec2:local-gateway-virtual-interface"
	TypeEC2LocalGatewayVirtualInterfaceGroup                      = "aws:ec2:local-gateway-virtual-interface-group"
	TypeEC2LocalGatewayRouteTableVPCAssociation                   = "aws:ec2:local-gateway-route-table-vpc-association"
	TypeEC2LocalGatewayRouteTableVIGAssociation                   = "aws:ec2:local-gateway-route-table-virtual-interface-group-association"
	// EC2 — Client VPN
	TypeEC2ClientVPNEndpoint                 = "aws:ec2:client-vpn-endpoint"
	TypeEC2ClientVPNAuthorizationRule        = "aws:ec2:client-vpn-authorization-rule"
	TypeEC2ClientVPNRoute                    = "aws:ec2:client-vpn-route"
	TypeEC2ClientVPNTargetNetworkAssociation = "aws:ec2:client-vpn-target-network-association"
	// EC2 — capacity / fleet
	TypeEC2CapacityReservationFleet = "aws:ec2:capacity-reservation-fleet"
	TypeEC2Fleet                    = "aws:ec2:ec2-fleet"
	// EC2 — carrier gateway
	TypeEC2CarrierGateway = "aws:ec2:carrier-gateway"
	// EC2 — VPC features
	TypeEC2VPCBlockPublicAccessOptions       = "aws:ec2:vpc-block-public-access-options"
	TypeEC2VPCBlockPublicAccessExclusion     = "aws:ec2:vpc-block-public-access-exclusion"
	TypeEC2VPCEndpointConnectionNotification = "aws:ec2:vpc-endpoint-connection-notification"
	TypeEC2VPCEndpointService                = "aws:ec2:vpc-endpoint-service"
	TypeEC2VPCEndpointServicePermissions     = "aws:ec2:vpc-endpoint-service-permissions"
	// EC2 — security group extensions
	TypeEC2SecurityGroupIngress        = "aws:ec2:security-group-ingress"
	TypeEC2SecurityGroupEgress         = "aws:ec2:security-group-egress"
	TypeEC2SecurityGroupVPCAssociation = "aws:ec2:security-group-vpc-association"
	// EC2 — network interface / misc extensions
	TypeEC2NetworkInterfacePermission          = "aws:ec2:network-interface-permission"
	TypeEC2NetworkPerformanceMetricSubscription = "aws:ec2:network-performance-metric-subscription"
	TypeEC2SnapshotBlockPublicAccess           = "aws:ec2:snapshot-block-public-access"
	// EC2 — sub-resources (expanded from parent API responses)
	TypeEC2Route                       = "aws:ec2:route"
	TypeEC2GatewayRouteTableAssociation = "aws:ec2:gateway-route-table-association"
	TypeEC2SubnetRouteTableAssociation  = "aws:ec2:subnet-route-table-association"
	TypeEC2NetworkACLEntry             = "aws:ec2:network-acl-entry"
	TypeEC2SubnetNetworkACLAssociation  = "aws:ec2:subnet-network-acl-association"
	TypeEC2VPCCIDRBlock                = "aws:ec2:vpc-cidr-block"
	TypeEC2VPCDHCPOptionsAssociation   = "aws:ec2:vpcdhcp-options-association"
	TypeEC2VPCGatewayAttachment        = "aws:ec2:vpc-gateway-attachment"
	TypeEC2SubnetCIDRBlock             = "aws:ec2:subnet-cidr-block"
	TypeEC2EIPAssociation              = "aws:ec2:eip-association"
	TypeEC2NetworkInterfaceAttachment  = "aws:ec2:network-interface-attachment"
	TypeEC2VolumeAttachment            = "aws:ec2:volume-attachment"
	TypeEC2VPNConnectionRoute          = "aws:ec2:vpn-connection-route"
	// IAM
	TypeIAMRole  = "aws:iam:role"
	TypeIAMUser  = "aws:iam:user"
	TypeIAMGroup = "aws:iam:group"
	// Lambda
	TypeLambdaFunction = "aws:lambda:function"
	// RDS
	TypeRDSDBInstance = "aws:rds:db-instance"
	// DynamoDB
	TypeDynamoDBTable = "aws:dynamodb:table"
	// EKS
	TypeEKSCluster = "aws:eks:cluster"
	// Elastic Load Balancing
	TypeELBLoadBalancer = "aws:elasticloadbalancing:load-balancer"
	// S3
	TypeS3Bucket = "aws:s3:bucket"
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
	TypeElastiCacheReplicationGroup = "aws:elasticache:replication-group"
	TypeElastiCacheCluster          = "aws:elasticache:cluster"
	// CloudFront
	TypeCloudFrontDistribution = "aws:cloudfront:distribution"
	// Route53
	TypeRoute53HostedZone  = "aws:route53:hosted-zone"
	TypeRoute53RecordSet   = "aws:route53:record-set"
	// API Gateway
	TypeAPIGatewayRestAPI = "aws:apigateway:rest-api"
	TypeAPIGatewayV2API   = "aws:apigatewayv2:api"
)

// KnownTypes returns all disco type strings currently covered by this provider.
// Used by the types command for gap-analysis against the CloudFormation registry.
func KnownTypes() []string {
	return []string{
		// EC2 — original
		TypeEC2Instance, TypeEC2VPC, TypeEC2Subnet, TypeEC2SecurityGroup,
		TypeEC2Volume, TypeEC2InternetGateway,
		TypeEC2NatGateway, TypeEC2RouteTable, TypeEC2EIP,
		TypeEC2NetworkInterface, TypeEC2NetworkACL,
		TypeEC2VPCEndpoint, TypeEC2VPCPeeringConnection,
		TypeEC2DHCPOptions, TypeEC2EgressOnlyIGW,
		TypeEC2CustomerGateway, TypeEC2VPNGateway, TypeEC2VPNConnection,
		TypeEC2TransitGateway, TypeEC2TransitGatewayAttachment,
		TypeEC2LaunchTemplate, TypeEC2KeyPair, TypeEC2PlacementGroup,
		TypeEC2SpotFleet, TypeEC2Host, TypeEC2CapacityReservation,
		TypeEC2InstanceConnectEndpoint,
		TypeEC2FlowLog, TypeEC2PrefixList,
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
		// EC2 — Network Insights
		TypeEC2NetworkInsightsPath, TypeEC2NetworkInsightsAnalysis,
		TypeEC2NetworkInsightsAccessScope, TypeEC2NetworkInsightsAccessScopeAnalysis,
		// EC2 — Local Gateway
		TypeEC2LocalGatewayRouteTable, TypeEC2LocalGatewayRoute,
		TypeEC2LocalGatewayVirtualInterface, TypeEC2LocalGatewayVirtualInterfaceGroup,
		TypeEC2LocalGatewayRouteTableVPCAssociation, TypeEC2LocalGatewayRouteTableVIGAssociation,
		// EC2 — Client VPN
		TypeEC2ClientVPNEndpoint, TypeEC2ClientVPNAuthorizationRule,
		TypeEC2ClientVPNRoute, TypeEC2ClientVPNTargetNetworkAssociation,
		// EC2 — capacity / fleet / carrier
		TypeEC2CapacityReservationFleet, TypeEC2Fleet, TypeEC2CarrierGateway,
		// EC2 — VPC features
		TypeEC2VPCBlockPublicAccessOptions, TypeEC2VPCBlockPublicAccessExclusion,
		TypeEC2VPCEndpointConnectionNotification,
		TypeEC2VPCEndpointService, TypeEC2VPCEndpointServicePermissions,
		// EC2 — security group extensions
		TypeEC2SecurityGroupIngress, TypeEC2SecurityGroupEgress, TypeEC2SecurityGroupVPCAssociation,
		// EC2 — network interface / misc
		TypeEC2NetworkInterfacePermission, TypeEC2NetworkPerformanceMetricSubscription,
		TypeEC2SnapshotBlockPublicAccess,
		// EC2 — sub-resources
		TypeEC2Route, TypeEC2GatewayRouteTableAssociation, TypeEC2SubnetRouteTableAssociation,
		TypeEC2NetworkACLEntry, TypeEC2SubnetNetworkACLAssociation,
		TypeEC2VPCCIDRBlock, TypeEC2VPCDHCPOptionsAssociation, TypeEC2VPCGatewayAttachment,
		TypeEC2SubnetCIDRBlock, TypeEC2EIPAssociation,
		TypeEC2NetworkInterfaceAttachment, TypeEC2VolumeAttachment, TypeEC2VPNConnectionRoute,
		// IAM
		TypeIAMRole, TypeIAMUser, TypeIAMGroup,
		TypeLambdaFunction,
		TypeRDSDBInstance,
		TypeDynamoDBTable,
		TypeEKSCluster,
		TypeELBLoadBalancer,
		TypeS3Bucket,
		TypeSNSTopic,
		TypeSQSQueue,
		TypeECSCluster, TypeECSService, TypeECSTaskDefinition,
		TypeECRRepository,
		TypeElastiCacheReplicationGroup, TypeElastiCacheCluster,
		TypeCloudFrontDistribution,
		TypeRoute53HostedZone, TypeRoute53RecordSet,
		TypeAPIGatewayRestAPI, TypeAPIGatewayV2API,
	}
}
