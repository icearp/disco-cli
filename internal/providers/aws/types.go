package aws

// Resource type constants for all AWS resource types discovered by this provider.
// Using constants prevents typos in scanner and resolver files — a mismatched
// string would create orphan resources with an undeclared type.
const (
	// EC2
	TypeEC2Instance        = "aws:ec2:instance"
	TypeEC2VPC             = "aws:ec2:vpc"
	TypeEC2Subnet          = "aws:ec2:subnet"
	TypeEC2SecurityGroup   = "aws:ec2:security-group"
	TypeEC2Volume          = "aws:ec2:volume"
	TypeEC2InternetGateway = "aws:ec2:internet-gateway"
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
)
