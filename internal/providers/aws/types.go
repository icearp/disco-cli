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
