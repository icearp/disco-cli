package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
)

func init() { coverage.Register(&coverageProvider{}) }

// coverageProvider implements coverage.Provider for AWS. Upstream truth
// source = CloudFormation ListTypes (Visibility=Public, Type=Resource).
// Coverage truth source = CollectEmits() in aws_services.go, which unions
// every registerService emits decl plus extraEmits from resolver-side
// synthetic stubs.
type coverageProvider struct{}

func (coverageProvider) Name() string { return "aws" }

func (coverageProvider) Emits() []coverage.TypeDecl { return CollectEmits() }

// Aliases overrides algorithmic disco-type → CFN-key mapping for cases
// where the disco service segment doesn't equal the CFN service segment.
//
// CFN keys: "AWS::Service::Resource" — case-sensitive.
//
// Cases where disco uses a different segment than CFN:
//   - aws:elasticloadbalancing:* → AWS::ElasticLoadBalancingV2::* (disco
//     emits ELBv2 only; classic ELB type left uncovered).
//   - aws:logs:* → AWS::Logs::*
//   - aws:events:* → AWS::Events::*
//   - aws:sso-admin:* → AWS::SSO::*
//   - aws:network-firewall:* → AWS::NetworkFirewall::*
//   - aws:secretsmanager:* → AWS::SecretsManager::*
//
// Hand-curated for the disco types that exist today; new types either fit
// the algorithmic form (`AWS::<Service>::<Pascal>`) or need an entry here.
func (coverageProvider) Aliases() map[string]string {
	return map[string]string{
		// IAM.
		TypeIAMUser:              "AWS::IAM::User",
		TypeIAMGroup:             "AWS::IAM::Group",
		TypeIAMRole:              "AWS::IAM::Role",
		TypeIAMServiceLinkedRole: "AWS::IAM::ServiceLinkedRole",
		TypeIAMPolicy:            "AWS::IAM::ManagedPolicy",
		TypeIAMRolePolicy:        "AWS::IAM::RolePolicy",
		TypeIAMUserPolicy:        "AWS::IAM::UserPolicy",
		TypeIAMGroupPolicy:       "AWS::IAM::GroupPolicy",
		TypeIAMAccessKey:         "AWS::IAM::AccessKey",
		TypeIAMInstanceProfile:   "AWS::IAM::InstanceProfile",
		TypeIAMOIDCProvider:      "AWS::IAM::OIDCProvider",
		TypeIAMSAMLProvider:      "AWS::IAM::SAMLProvider",
		TypeIAMServerCertificate: "AWS::IAM::ServerCertificate",
		TypeIAMVirtualMFADevice:  "AWS::IAM::VirtualMFADevice",
		// EC2.
		TypeEC2Instance:                                 "AWS::EC2::Instance",
		TypeEC2SecurityGroup:                            "AWS::EC2::SecurityGroup",
		TypeEC2SecurityGroupVPCAssociation:              "AWS::EC2::SecurityGroupVpcAssociation",
		TypeEC2Volume:                                   "AWS::EC2::Volume",
		TypeEC2KeyPair:                                  "AWS::EC2::KeyPair",
		TypeEC2LaunchTemplate:                           "AWS::EC2::LaunchTemplate",
		TypeEC2PlacementGroup:                           "AWS::EC2::PlacementGroup",
		TypeEC2Image:                                    "AWS::ImageBuilder::Image",
		TypeEC2Host:                                     "AWS::EC2::Host",
		TypeEC2SpotFleet:                                "AWS::EC2::SpotFleet",
		TypeEC2Fleet:                                    "AWS::EC2::EC2Fleet",
		TypeEC2CapacityReservation:                      "AWS::EC2::CapacityReservation",
		TypeEC2CapacityReservationFleet:                 "AWS::EC2::CapacityReservationFleet",
		TypeEC2InstanceConnectEndpoint:                  "AWS::EC2::InstanceConnectEndpoint",
		TypeEC2SnapshotBlockPublicAccess:                "AWS::EC2::SnapshotBlockPublicAccess",
		TypeEC2VPC:                                      "AWS::EC2::VPC",
		TypeEC2Subnet:                                   "AWS::EC2::Subnet",
		TypeEC2InternetGateway:                          "AWS::EC2::InternetGateway",
		TypeEC2EgressOnlyIGW:                            "AWS::EC2::EgressOnlyInternetGateway",
		TypeEC2NatGateway:                               "AWS::EC2::NatGateway",
		TypeEC2RouteTable:                               "AWS::EC2::RouteTable",
		TypeEC2NetworkInterface:                         "AWS::EC2::NetworkInterface",
		TypeEC2NetworkInterfacePermission:               "AWS::EC2::NetworkInterfacePermission",
		TypeEC2NetworkACL:                               "AWS::EC2::NetworkAcl",
		TypeEC2EIP:                                      "AWS::EC2::EIP",
		TypeEC2DHCPOptions:                              "AWS::EC2::DHCPOptions",
		TypeEC2CarrierGateway:                           "AWS::EC2::CarrierGateway",
		TypeEC2VPCEndpoint:                              "AWS::EC2::VPCEndpoint",
		TypeEC2VPCEndpointService:                       "AWS::EC2::VPCEndpointService",
		TypeEC2VPCEndpointServicePermissions:            "AWS::EC2::VPCEndpointServicePermissions",
		TypeEC2VPCEndpointConnectionNotification:        "AWS::EC2::VPCEndpointConnectionNotification",
		TypeEC2VPCPeeringConnection:                     "AWS::EC2::VPCPeeringConnection",
		TypeEC2VPCBlockPublicAccessOptions:              "AWS::EC2::VPCBlockPublicAccessOptions",
		TypeEC2VPCBlockPublicAccessExclusion:            "AWS::EC2::VPCBlockPublicAccessExclusion",
		TypeEC2FlowLog:                                  "AWS::EC2::FlowLog",
		TypeEC2PrefixList:                               "AWS::EC2::PrefixList",
		TypeEC2NetworkInsightsPath:                      "AWS::EC2::NetworkInsightsPath",
		TypeEC2NetworkInsightsAnalysis:                  "AWS::EC2::NetworkInsightsAnalysis",
		TypeEC2NetworkInsightsAccessScope:               "AWS::EC2::NetworkInsightsAccessScope",
		TypeEC2NetworkInsightsAccessScopeAnalysis:       "AWS::EC2::NetworkInsightsAccessScopeAnalysis",
		TypeEC2IPAM:                                     "AWS::EC2::IPAM",
		TypeEC2IPAMScope:                                "AWS::EC2::IPAMScope",
		TypeEC2IPAMPool:                                 "AWS::EC2::IPAMPool",
		TypeEC2IPAMPoolCIDR:                             "AWS::EC2::IPAMPoolCidr",
		TypeEC2IPAMAllocation:                           "AWS::EC2::IPAMAllocation",
		TypeEC2IPAMResourceDiscovery:                    "AWS::EC2::IPAMResourceDiscovery",
		TypeEC2IPAMResourceDiscoveryAssociation:         "AWS::EC2::IPAMResourceDiscoveryAssociation",
		TypeEC2TransitGateway:                           "AWS::EC2::TransitGateway",
		TypeEC2TransitGatewayAttachment:                 "AWS::EC2::TransitGatewayAttachment",
		TypeEC2TransitGatewayConnect:                    "AWS::EC2::TransitGatewayConnect",
		TypeEC2TransitGatewayConnectPeer:                "AWS::EC2::TransitGatewayConnectPeer",
		TypeEC2TransitGatewayMulticastDomain:            "AWS::EC2::TransitGatewayMulticastDomain",
		TypeEC2TransitGatewayMulticastDomainAssociation: "AWS::EC2::TransitGatewayMulticastDomainAssociation",
		TypeEC2TransitGatewayMulticastGroupMember:       "AWS::EC2::TransitGatewayMulticastGroupMember",
		TypeEC2TransitGatewayMulticastGroupSource:       "AWS::EC2::TransitGatewayMulticastGroupSource",
		TypeEC2TransitGatewayPeeringAttachment:          "AWS::EC2::TransitGatewayPeeringAttachment",
		TypeEC2TransitGatewayRoute:                      "AWS::EC2::TransitGatewayRoute",
		TypeEC2TransitGatewayRouteTable:                 "AWS::EC2::TransitGatewayRouteTable",
		TypeEC2TransitGatewayRouteTableAssociation:      "AWS::EC2::TransitGatewayRouteTableAssociation",
		TypeEC2TransitGatewayRouteTablePropagation:      "AWS::EC2::TransitGatewayRouteTablePropagation",
		TypeEC2TransitGatewayVPCAttachment:              "AWS::EC2::TransitGatewayVpcAttachment",
		TypeEC2TrafficMirrorTarget:                      "AWS::EC2::TrafficMirrorTarget",
		TypeEC2TrafficMirrorFilter:                      "AWS::EC2::TrafficMirrorFilter",
		TypeEC2TrafficMirrorFilterRule:                  "AWS::EC2::TrafficMirrorFilterRule",
		TypeEC2TrafficMirrorSession:                     "AWS::EC2::TrafficMirrorSession",
		TypeEC2VerifiedAccessTrustProvider:              "AWS::EC2::VerifiedAccessTrustProvider",
		TypeEC2VerifiedAccessInstance:                   "AWS::EC2::VerifiedAccessInstance",
		TypeEC2VerifiedAccessGroup:                      "AWS::EC2::VerifiedAccessGroup",
		TypeEC2VerifiedAccessEndpoint:                   "AWS::EC2::VerifiedAccessEndpoint",
		TypeEC2CustomerGateway:                          "AWS::EC2::CustomerGateway",
		TypeEC2VPNGateway:                               "AWS::EC2::VPNGateway",
		TypeEC2VPNConnection:                            "AWS::EC2::VPNConnection",
		TypeEC2ClientVPNEndpoint:                        "AWS::EC2::ClientVpnEndpoint",
		TypeEC2ClientVPNAuthorizationRule:               "AWS::EC2::ClientVpnAuthorizationRule",
		TypeEC2ClientVPNRoute:                           "AWS::EC2::ClientVpnRoute",
		TypeEC2ClientVPNTargetNetworkAssociation:        "AWS::EC2::ClientVpnTargetNetworkAssociation",
		TypeEC2LocalGatewayRouteTable:                   "AWS::EC2::LocalGatewayRouteTable",
		TypeEC2LocalGatewayRoute:                        "AWS::EC2::LocalGatewayRoute",
		TypeEC2LocalGatewayVirtualInterface:             "AWS::EC2::LocalGatewayVirtualInterface",
		TypeEC2LocalGatewayVirtualInterfaceGroup:        "AWS::EC2::LocalGatewayVirtualInterfaceGroup",
		TypeEC2LocalGatewayRouteTableVPCAssociation:     "AWS::EC2::LocalGatewayRouteTableVPCAssociation",
		TypeEC2LocalGatewayRouteTableVIGAssociation:     "AWS::EC2::LocalGatewayRouteTableVirtualInterfaceGroupAssociation",
		// ELBv2 (disco service segment is "elasticloadbalancing", CFN uses
		// "ElasticLoadBalancingV2").
		TypeELBClassicLoadBalancer:    "AWS::ElasticLoadBalancing::LoadBalancer",
		TypeELBv2LoadBalancer:         "AWS::ElasticLoadBalancingV2::LoadBalancer",
		TypeELBv2Listener:             "AWS::ElasticLoadBalancingV2::Listener",
		TypeELBv2ListenerCertificate:  "AWS::ElasticLoadBalancingV2::ListenerCertificate",
		TypeELBv2ListenerRule:         "AWS::ElasticLoadBalancingV2::ListenerRule",
		TypeELBv2TargetGroup:          "AWS::ElasticLoadBalancingV2::TargetGroup",
		TypeELBv2TrustStore:           "AWS::ElasticLoadBalancingV2::TrustStore",
		TypeELBv2TrustStoreRevocation: "AWS::ElasticLoadBalancingV2::TrustStoreRevocation",
		// API Gateway.
		TypeAPIGatewayRestAPI:               "AWS::ApiGateway::RestApi",
		TypeAPIGatewayResource:              "AWS::ApiGateway::Resource",
		TypeAPIGatewayMethod:                "AWS::ApiGateway::Method",
		TypeAPIGatewayStage:                 "AWS::ApiGateway::Stage",
		TypeAPIGatewayDeployment:            "AWS::ApiGateway::Deployment",
		TypeAPIGatewayAccount:               "AWS::ApiGateway::Account",
		TypeAPIGatewayAuthorizer:            "AWS::ApiGateway::Authorizer",
		TypeAPIGatewayDomainName:            "AWS::ApiGateway::DomainName",
		TypeAPIGatewayDomainNameAccessAssoc: "AWS::ApiGateway::DomainNameAccessAssociation",
		TypeAPIGatewayBasePathMapping:       "AWS::ApiGateway::BasePathMapping",
		// APIGW V1 SDK private-domain flavor — separate CFN resource keyed by V2 suffix.
		TypeAPIGatewayPrivateDomainName:      "AWS::ApiGateway::DomainNameV2",
		TypeAPIGatewayPrivateBasePathMapping: "AWS::ApiGateway::BasePathMappingV2",
		TypeAPIGatewayAPIKey:                 "AWS::ApiGateway::ApiKey",
		TypeAPIGatewayUsagePlan:              "AWS::ApiGateway::UsagePlan",
		TypeAPIGatewayUsagePlanKey:           "AWS::ApiGateway::UsagePlanKey",
		TypeAPIGatewayClientCertificate:      "AWS::ApiGateway::ClientCertificate",
		TypeAPIGatewayDocumentationPart:      "AWS::ApiGateway::DocumentationPart",
		TypeAPIGatewayDocumentationVersion:   "AWS::ApiGateway::DocumentationVersion",
		TypeAPIGatewayGatewayResponse:        "AWS::ApiGateway::GatewayResponse",
		TypeAPIGatewayModel:                  "AWS::ApiGateway::Model",
		TypeAPIGatewayRequestValidator:       "AWS::ApiGateway::RequestValidator",
		TypeAPIGatewayVpcLink:                "AWS::ApiGateway::VpcLink",
		// APIGW v2 — uses the AWS::ApiGatewayV2 prefix.
		TypeAPIGatewayV2API:                 "AWS::ApiGatewayV2::Api",
		TypeAPIGatewayV2Authorizer:          "AWS::ApiGatewayV2::Authorizer",
		TypeAPIGatewayDomainNameV2:          "AWS::ApiGatewayV2::DomainName",
		TypeAPIGatewayBasePathMappingV2:     "AWS::ApiGatewayV2::ApiMapping",
		TypeAPIGatewayV2Deployment:          "AWS::ApiGatewayV2::Deployment",
		TypeAPIGatewayV2Integration:         "AWS::ApiGatewayV2::Integration",
		TypeAPIGatewayV2IntegrationResponse: "AWS::ApiGatewayV2::IntegrationResponse",
		TypeAPIGatewayV2Model:               "AWS::ApiGatewayV2::Model",
		TypeAPIGatewayV2Route:               "AWS::ApiGatewayV2::Route",
		TypeAPIGatewayV2RouteResponse:       "AWS::ApiGatewayV2::RouteResponse",
		TypeAPIGatewayV2Stage:               "AWS::ApiGatewayV2::Stage",
		TypeAPIGatewayV2RoutingRule:         "AWS::ApiGatewayV2::RoutingRule",
		// Lambda.
		TypeLambdaFunction:          "AWS::Lambda::Function",
		TypeLambdaAlias:             "AWS::Lambda::Alias",
		TypeLambdaVersion:           "AWS::Lambda::Version",
		TypeLambdaURL:               "AWS::Lambda::Url",
		TypeLambdaESM:               "AWS::Lambda::EventSourceMapping",
		TypeLambdaLayerVersion:      "AWS::Lambda::LayerVersion",
		TypeLambdaCodeSigningConfig: "AWS::Lambda::CodeSigningConfig",
		TypeLambdaEventInvokeConfig: "AWS::Lambda::EventInvokeConfig",
		// CloudWatch / Logs / Events.
		TypeCloudWatchAlarm:           "AWS::CloudWatch::Alarm",
		TypeCloudWatchCompositeAlarm:  "AWS::CloudWatch::CompositeAlarm",
		TypeCloudWatchAlarmMuteRule:   "AWS::CloudWatch::AlarmMuteRule",
		TypeCloudWatchAnomalyDetector: "AWS::CloudWatch::AnomalyDetector",
		TypeCloudWatchDashboard:       "AWS::CloudWatch::Dashboard",
		TypeCloudWatchInsightRule:     "AWS::CloudWatch::InsightRule",
		TypeCloudWatchMetricStream:    "AWS::CloudWatch::MetricStream",
		TypeLogsLogGroup:              "AWS::Logs::LogGroup",
		TypeLogsLogStream:             "AWS::Logs::LogStream",
		TypeLogsMetricFilter:          "AWS::Logs::MetricFilter",
		TypeLogsSubscriptionFilter:    "AWS::Logs::SubscriptionFilter",
		TypeLogsQueryDefinition:       "AWS::Logs::QueryDefinition",
		TypeLogsResourcePolicy:        "AWS::Logs::ResourcePolicy",
		TypeLogsAccountPolicy:         "AWS::Logs::AccountPolicy",
		TypeLogsDestination:           "AWS::Logs::Destination",
		TypeLogsDelivery:              "AWS::Logs::Delivery",
		TypeLogsDeliverySource:        "AWS::Logs::DeliverySource",
		TypeLogsDeliveryDest:          "AWS::Logs::DeliveryDestination",
		TypeLogsLogAnomalyDetector:    "AWS::Logs::LogAnomalyDetector",
		TypeLogsTransformer:           "AWS::Logs::Transformer",
		TypeLogsIntegration:           "AWS::Logs::Integration",
		TypeEventsEventBus:            "AWS::Events::EventBus",
		TypeEventsRule:                "AWS::Events::Rule",
		TypeEventsConnection:          "AWS::Events::Connection",
		TypeEventsAPIDestination:      "AWS::Events::ApiDestination",
		// Step Functions.
		TypeSFNStateMachine: "AWS::StepFunctions::StateMachine",
		TypeSFNActivity:     "AWS::StepFunctions::Activity",
		// SecretsManager.
		TypeSecretsManagerSecret: "AWS::SecretsManager::Secret",
		// SSM.
		TypeSSMDocument:      "AWS::SSM::Document",
		TypeSSMParameter:     "AWS::SSM::Parameter",
		TypeSSMPatchBaseline: "AWS::SSM::PatchBaseline",
		// SSO / IdentityStore.
		TypeSSOInstance:          "AWS::SSO::Instance",
		TypeSSOPermissionSet:     "AWS::SSO::PermissionSet",
		TypeSSOAccountAssignment: "AWS::SSO::Assignment",
		TypeIdentityStoreUser:    "AWS::IdentityStore::User",
		TypeIdentityStoreGroup:   "AWS::IdentityStore::Group",
		// MSK.
		TypeMSKCluster:          "AWS::MSK::Cluster",
		TypeMSKBatchScramSecret: "AWS::MSK::BatchScramSecret",
		TypeMSKClusterPolicy:    "AWS::MSK::ClusterPolicy",
		TypeMSKConfiguration:    "AWS::MSK::Configuration",
		TypeMSKReplicator:       "AWS::MSK::Replicator",
		TypeMSKVpcConnection:    "AWS::MSK::VpcConnection",
		// CloudFront.
		TypeCloudFrontDistribution:               "AWS::CloudFront::Distribution",
		TypeCloudFrontStreamingDistribution:      "AWS::CloudFront::StreamingDistribution",
		TypeCloudFrontDistributionTenant:         "AWS::CloudFront::DistributionTenant",
		TypeCloudFrontOAI:                        "AWS::CloudFront::CloudFrontOriginAccessIdentity",
		TypeCloudFrontOriginAccessControl:        "AWS::CloudFront::OriginAccessControl",
		TypeCloudFrontConnectionFunction:         "AWS::CloudFront::ConnectionFunction",
		TypeCloudFrontConnectionGroup:            "AWS::CloudFront::ConnectionGroup",
		TypeCloudFrontKeyValueStore:              "AWS::CloudFront::KeyValueStore",
		TypeCloudFrontPublicKey:                  "AWS::CloudFront::PublicKey",
		TypeCloudFrontTrustStore:                 "AWS::CloudFront::TrustStore",
		TypeCloudFrontAnycastIPList:              "AWS::CloudFront::AnycastIpList",
		TypeCloudFrontCachePolicy:                "AWS::CloudFront::CachePolicy",
		TypeCloudFrontContinuousDeploymentPolicy: "AWS::CloudFront::ContinuousDeploymentPolicy",
		TypeCloudFrontFunction:                   "AWS::CloudFront::Function",
		TypeCloudFrontKeyGroup:                   "AWS::CloudFront::KeyGroup",
		TypeCloudFrontOriginRequestPolicy:        "AWS::CloudFront::OriginRequestPolicy",
		TypeCloudFrontRealtimeLogConfig:          "AWS::CloudFront::RealtimeLogConfig",
		TypeCloudFrontResponseHeadersPolicy:      "AWS::CloudFront::ResponseHeadersPolicy",
		TypeCloudFrontVpcOrigin:                  "AWS::CloudFront::VpcOrigin",
		TypeCloudFrontMonitoringSubscription:     "AWS::CloudFront::MonitoringSubscription",
		// Route 53.
		TypeRoute53HostedZone:     "AWS::Route53::HostedZone",
		TypeRoute53RecordSet:      "AWS::Route53::RecordSet",
		TypeRoute53HealthCheck:    "AWS::Route53::HealthCheck",
		TypeRoute53DNSSEC:         "AWS::Route53::DNSSEC",
		TypeRoute53KeySigningKey:  "AWS::Route53::KeySigningKey",
		TypeRoute53CIDRCollection: "AWS::Route53::CidrCollection",
		// S3 / S3 Control.
		TypeS3Bucket:                       "AWS::S3::Bucket",
		TypeS3BucketPolicy:                 "AWS::S3::BucketPolicy",
		TypeS3AccessGrantsInstance:         "AWS::S3::AccessGrantsInstance",
		TypeS3AccessGrantsLocation:         "AWS::S3::AccessGrantsLocation",
		TypeS3AccessGrant:                  "AWS::S3::AccessGrant",
		TypeS3AccessPoint:                  "AWS::S3::AccessPoint",
		TypeS3MultiRegionAccessPoint:       "AWS::S3::MultiRegionAccessPoint",
		TypeS3MultiRegionAccessPointPolicy: "AWS::S3::MultiRegionAccessPointPolicy",
		TypeS3StorageLens:                  "AWS::S3::StorageLens",
		TypeS3StorageLensGroup:             "AWS::S3::StorageLensGroup",
		// CloudFormation.
		TypeCloudFormationStack:    "AWS::CloudFormation::Stack",
		TypeCloudFormationStackSet: "AWS::CloudFormation::StackSet",
		// CloudTrail.
		TypeCloudTrailTrail:          "AWS::CloudTrail::Trail",
		TypeCloudTrailEventDataStore: "AWS::CloudTrail::EventDataStore",
		// Cognito.
		TypeCognitoUserPool:     "AWS::Cognito::UserPool",
		TypeCognitoAppClient:    "AWS::Cognito::UserPoolClient",
		TypeCognitoIdentityPool: "AWS::Cognito::IdentityPool",
		// Config.
		TypeConfigRule:            "AWS::Config::ConfigRule",
		TypeConfigRecorder:        "AWS::Config::ConfigurationRecorder",
		TypeConfigDeliveryChannel: "AWS::Config::DeliveryChannel",
		// ControlTower.
		TypeControlTowerLandingZone:     "AWS::ControlTower::LandingZone",
		TypeControlTowerEnabledBaseline: "AWS::ControlTower::EnabledBaseline",
		// Detective.
		TypeDetectiveGraph: "AWS::Detective::Graph",
		// AuditManager.
		TypeAuditManagerAssessment: "AWS::AuditManager::Assessment",
		TypeAuditManagerControl:    "AWS::AuditManager::Control",
		TypeAuditManagerFramework:  "AWS::AuditManager::Framework",
		// Backup.
		TypeBackupVault:                      "AWS::Backup::BackupVault",
		TypeBackupLogicallyAirGappedVault:    "AWS::Backup::LogicallyAirGappedBackupVault",
		TypeBackupPlan:                       "AWS::Backup::BackupPlan",
		TypeBackupSelection:                  "AWS::Backup::BackupSelection",
		TypeBackupGatewayHypervisor:          "AWS::BackupGateway::Hypervisor",
		TypeBCMDataExportsExport:             "AWS::BCMDataExports::Export",
		TypeBcmPricingCalculatorBillScenario: "AWS::BcmPricingCalculator::BillScenario",
		// Batch.
		TypeBatchComputeEnvironment: "AWS::Batch::ComputeEnvironment",
		TypeBatchJobQueue:           "AWS::Batch::JobQueue",
		TypeBatchJobDefinition:      "AWS::Batch::JobDefinition",
		TypeBatchSchedulingPolicy:   "AWS::Batch::SchedulingPolicy",
		TypeBatchConsumableResource: "AWS::Batch::ConsumableResource",
		TypeBatchServiceEnvironment: "AWS::Batch::ServiceEnvironment",
		TypeBatchQuotaShare:         "AWS::Batch::QuotaShare",
		// DocDB / Neptune / RDS / Redshift / DynamoDB.
		TypeDocDBCluster:                  "AWS::DocDB::DBCluster",
		TypeDocDBInstance:                 "AWS::DocDB::DBInstance",
		TypeNeptuneCluster:                "AWS::Neptune::DBCluster",
		TypeNeptuneInstance:               "AWS::Neptune::DBInstance",
		TypeDynamoDBTable:                 "AWS::DynamoDB::Table",
		TypeDynamoDBGlobalTable:           "AWS::DynamoDB::GlobalTable",
		TypeRedshiftCluster:               "AWS::Redshift::Cluster",
		TypeRedshiftSubnetGroup:           "AWS::Redshift::ClusterSubnetGroup",
		TypeRedshiftClusterParameterGroup: "AWS::Redshift::ClusterParameterGroup",
		TypeRedshiftClusterSecurityGroup:  "AWS::Redshift::ClusterSecurityGroup",
		TypeRedshiftEndpointAccess:        "AWS::Redshift::EndpointAccess",
		TypeRedshiftEndpointAuthorization: "AWS::Redshift::EndpointAuthorization",
		TypeRedshiftEventSubscription:     "AWS::Redshift::EventSubscription",
		TypeRedshiftIntegration:           "AWS::Redshift::Integration",
		TypeRedshiftScheduledAction:       "AWS::Redshift::ScheduledAction",
		TypeRDSDBCluster:                  "AWS::RDS::DBCluster",
		TypeRDSDBInstance:                 "AWS::RDS::DBInstance",
		TypeRDSGlobalCluster:              "AWS::RDS::GlobalCluster",
		TypeRDSDBClusterParameterGroup:    "AWS::RDS::DBClusterParameterGroup",
		TypeRDSDBParameterGroup:           "AWS::RDS::DBParameterGroup",
		TypeRDSDBSubnetGroup:              "AWS::RDS::DBSubnetGroup",
		TypeRDSDBSecurityGroup:            "AWS::RDS::DBSecurityGroup",
		TypeRDSOptionGroup:                "AWS::RDS::OptionGroup",
		TypeRDSEventSubscription:          "AWS::RDS::EventSubscription",
		TypeRDSIntegration:                "AWS::RDS::Integration",
		TypeRDSDBProxy:                    "AWS::RDS::DBProxy",
		TypeRDSDBProxyEndpoint:            "AWS::RDS::DBProxyEndpoint",
		TypeRDSDBProxyTargetGroup:         "AWS::RDS::DBProxyTargetGroup",
		TypeRDSDBShardGroup:               "AWS::RDS::DBShardGroup",
		TypeRDSCustomDBEngineVersion:      "AWS::RDS::CustomDBEngineVersion",
		// ECR / ECS / EKS.
		TypeECRRepository:     "AWS::ECR::Repository",
		TypeECSCluster:        "AWS::ECS::Cluster",
		TypeECSService:        "AWS::ECS::Service",
		TypeECSTaskDefinition: "AWS::ECS::TaskDefinition",
		TypeEKSCluster:        "AWS::EKS::Cluster",
		// EFS.
		TypeEFSFileSystem:  "AWS::EFS::FileSystem",
		TypeEFSAccessPoint: "AWS::EFS::AccessPoint",
		TypeEFSMountTarget: "AWS::EFS::MountTarget",
		// ElastiCache.
		TypeElastiCacheCacheCluster:           "AWS::ElastiCache::CacheCluster",
		TypeElastiCacheReplicationGroup:       "AWS::ElastiCache::ReplicationGroup",
		TypeElastiCacheGlobalReplicationGroup: "AWS::ElastiCache::GlobalReplicationGroup",
		TypeElastiCacheServerlessCache:        "AWS::ElastiCache::ServerlessCache",
		TypeElastiCacheParameterGroup:         "AWS::ElastiCache::ParameterGroup",
		TypeElastiCacheSecurityGroup:          "AWS::ElastiCache::SecurityGroup",
		TypeElastiCacheSubnetGroup:            "AWS::ElastiCache::SubnetGroup",
		TypeElastiCacheUser:                   "AWS::ElastiCache::User",
		TypeElastiCacheUserGroup:              "AWS::ElastiCache::UserGroup",
		// Beanstalk.
		TypeBeanstalkApplication: "AWS::ElasticBeanstalk::Application",
		TypeBeanstalkEnvironment: "AWS::ElasticBeanstalk::Environment",
		// Glue / Athena / GuardDuty / Inspector.
		TypeGlueDatabase:                            "AWS::Glue::Database",
		TypeGlueTable:                               "AWS::Glue::Table",
		TypeAthenaWorkgroup:                         "AWS::Athena::WorkGroup",
		TypeAthenaDataCatalog:                       "AWS::Athena::DataCatalog",
		TypeGuardDutyDetector:                       "AWS::GuardDuty::Detector",
		TypeGuardDutyFilter:                         "AWS::GuardDuty::Filter",
		TypeGuardDutyIPSet:                          "AWS::GuardDuty::IPSet",
		TypeInspector2Filter:                        "AWS::InspectorV2::Filter",
		TypeInspector2CisScanConfiguration:          "AWS::InspectorV2::CisScanConfiguration",
		TypeInspector2CodeSecurityIntegration:       "AWS::InspectorV2::CodeSecurityIntegration",
		TypeInspector2CodeSecurityScanConfiguration: "AWS::InspectorV2::CodeSecurityScanConfiguration",
		// Macie.
		TypeMacieClassificationJob:    "AWS::Macie::ClassificationJob",
		TypeMacieAllowList:            "AWS::Macie::AllowList",
		TypeMacieCustomDataIdentifier: "AWS::Macie::CustomDataIdentifier",
		// LakeFormation / NetworkFirewall / OpenSearch / Organizations.
		TypeLakeFormationResource:         "AWS::LakeFormation::Resource",
		TypeNetworkFirewallFirewall:       "AWS::NetworkFirewall::Firewall",
		TypeNetworkFirewallFirewallPolicy: "AWS::NetworkFirewall::FirewallPolicy",
		TypeNetworkFirewallRuleGroup:      "AWS::NetworkFirewall::RuleGroup",
		TypeOpenSearchDomain:              "AWS::OpenSearchService::Domain",
		TypeOrganization:                  "AWS::Organizations::Organization",
		TypeOrganizationsAccount:          "AWS::Organizations::Account",
		TypeOrganizationsOU:               "AWS::Organizations::OrganizationalUnit",
		TypeOrganizationsSCP:              "AWS::Organizations::Policy",
		// AppRunner / SES / Service Catalog / SecurityHub / Shield / SNS / SQS.
		TypeAppRunnerService:                 "AWS::AppRunner::Service",
		TypeAppRunnerVPCConnector:            "AWS::AppRunner::VpcConnector",
		TypeSESEmailIdentity:                 "AWS::SES::EmailIdentity",
		TypeSESConfigurationSet:              "AWS::SES::ConfigurationSet",
		TypeServiceCatalogPortfolio:          "AWS::ServiceCatalog::Portfolio",
		TypeServiceCatalogProduct:            "AWS::ServiceCatalog::CloudFormationProduct",
		TypeSecurityHubHub:                   "AWS::SecurityHub::Hub",
		TypeSecurityHubInsight:               "AWS::SecurityHub::Insight",
		TypeSecurityHubProductSubscription:   "AWS::SecurityHub::ProductSubscription",
		TypeSecurityHubStandardsSubscription: "AWS::SecurityHub::StandardsSubscription",
		TypeShieldProtection:                 "AWS::Shield::Protection",
		TypeShieldProtectionGroup:            "AWS::Shield::ProtectionGroup",
		TypeSNSTopic:                         "AWS::SNS::Topic",
		TypeSQSQueue:                         "AWS::SQS::Queue",
		// Kinesis / Firehose / KMS.
		TypeKinesisStream:          "AWS::Kinesis::Stream",
		TypeFirehoseDeliveryStream: "AWS::KinesisFirehose::DeliveryStream",
		TypeKMSKey:                 "AWS::KMS::Key",
		TypeKMSAlias:               "AWS::KMS::Alias",
		// Lightsail / WAFv2 / ACM.
		TypeLightsailInstance:         "AWS::Lightsail::Instance",
		TypeLightsailDatabase:         "AWS::Lightsail::Database",
		TypeLightsailContainerService: "AWS::Lightsail::Container",
		TypeWAFv2WebACL:               "AWS::WAFv2::WebACL",
		TypeWAFv2RuleGroup:            "AWS::WAFv2::RuleGroup",
		TypeWAFv2IPSet:                "AWS::WAFv2::IPSet",
		TypeACMCertificate:            "AWS::CertificateManager::Certificate",
		TypeACMPrivateCA:              "AWS::ACMPCA::CertificateAuthority",
		// IAM Access Analyzer (CFN service segment "AccessAnalyzer" mixed-case).
		TypeAccessAnalyzerAnalyzer: "AWS::AccessAnalyzer::Analyzer",
		// ACM Private CA — disco service segment "acm-pca" (kebab-case, hyphen
		// would not survive the algorithmic Pascal-case conversion).
		TypeACMPCAPermission: "AWS::ACMPCA::Permission",
		// CloudWatch AIOps (CFN service segment "AIOps" mixed-case).
		TypeAIOpsInvestigationGroup: "AWS::AIOps::InvestigationGroup",
		// Amazon MQ — disco service segment "mq", CFN segment "AmazonMQ".
		TypeMQBroker:                   "AWS::AmazonMQ::Broker",
		TypeMQConfiguration:            "AWS::AmazonMQ::Configuration",
		TypeMQConfigurationAssociation: "AWS::AmazonMQ::ConfigurationAssociation",
		// Amazon Managed Prometheus / APS — CFN segment "APS" mixed-case.
		TypeAPSWorkspace: "AWS::APS::Workspace",
		TypeAPSScraper:   "AWS::APS::Scraper",
		// ARC Region Switch — disco service segment "arc-region-switch", CFN "ARCRegionSwitch".
		TypeARCRegionSwitchPlan: "AWS::ARCRegionSwitch::Plan",
		// AWS Auto Scaling (Plans) — disco "autoscaling-plans", CFN "AutoScalingPlans".
		TypeAutoScalingPlansScalingPlan: "AWS::AutoScalingPlans::ScalingPlan",
		// EC2 Auto Scaling — disco service segment "autoscaling", CFN "AutoScaling".
		TypeAutoScalingGroup:               "AWS::AutoScaling::AutoScalingGroup",
		TypeAutoScalingLaunchConfiguration: "AWS::AutoScaling::LaunchConfiguration",
		TypeAutoScalingLifecycleHook:       "AWS::AutoScaling::LifecycleHook",
		TypeAutoScalingScalingPolicy:       "AWS::AutoScaling::ScalingPolicy",
		TypeAutoScalingScheduledAction:     "AWS::AutoScaling::ScheduledAction",
		TypeAutoScalingWarmPool:            "AWS::AutoScaling::WarmPool",
		// ARC Zonal Shift — disco service segment "arc-zonal-shift", CFN "ARCZonalShift".
		TypeARCZonalShiftObserverStatus: "AWS::ARCZonalShift::AutoshiftObserverNotificationStatus",
		TypeARCZonalShiftConfiguration:  "AWS::ARCZonalShift::ZonalAutoshiftConfiguration",
		// API Gateway v2 — VPC Link.
		TypeAPIGatewayV2VpcLink: "AWS::ApiGatewayV2::VpcLink",
		// EMRContainers — disco "emr-containers" segment vs CFN "EMRContainers".
		TypeEMRContainersVirtualCluster: "AWS::EMRContainers::VirtualCluster",
		TypeEMRContainersEndpoint:       "AWS::EMRContainers::Endpoint",
		TypeEMRContainersSecurityConfig: "AWS::EMRContainers::SecurityConfiguration",
		// DevOpsGuru — disco "devops-guru" segment vs CFN "DevOpsGuru".
		TypeDevOpsGuruNotificationChannel:            "AWS::DevOpsGuru::NotificationChannel",
		TypeDevOpsGuruResourceCollection:             "AWS::DevOpsGuru::ResourceCollection",
		TypeDevOpsGuruLogAnomalyDetectionIntegration: "AWS::DevOpsGuru::LogAnomalyDetectionIntegration",
		// DAX — disco "dax" segment vs CFN "DAX".
		TypeDAXCluster:        "AWS::DAX::Cluster",
		TypeDAXParameterGroup: "AWS::DAX::ParameterGroup",
		TypeDAXSubnetGroup:    "AWS::DAX::SubnetGroup",
		// CodeStarConnections — disco "codestar-connections" segment vs CFN "CodeStarConnections".
		TypeCodeStarConnectionsConnection:        "AWS::CodeStarConnections::Connection",
		TypeCodeStarConnectionsRepositoryLink:    "AWS::CodeStarConnections::RepositoryLink",
		TypeCodeStarConnectionsSyncConfiguration: "AWS::CodeStarConnections::SyncConfiguration",
		// CodePipeline — disco "codepipeline" segment vs CFN "CodePipeline".
		TypeCodePipelinePipeline:         "AWS::CodePipeline::Pipeline",
		TypeCodePipelineCustomActionType: "AWS::CodePipeline::CustomActionType",
		TypeCodePipelineWebhook:          "AWS::CodePipeline::Webhook",
		// CodeDeploy — disco "codedeploy" segment vs CFN "CodeDeploy".
		TypeCodeDeployApplication:      "AWS::CodeDeploy::Application",
		TypeCodeDeployDeploymentGroup:  "AWS::CodeDeploy::DeploymentGroup",
		TypeCodeDeployDeploymentConfig: "AWS::CodeDeploy::DeploymentConfig",
		// CodeArtifact — disco "codeartifact" segment vs CFN "CodeArtifact".
		TypeCodeArtifactDomain:       "AWS::CodeArtifact::Domain",
		TypeCodeArtifactRepository:   "AWS::CodeArtifact::Repository",
		TypeCodeArtifactPackageGroup: "AWS::CodeArtifact::PackageGroup",
		// CleanRoomsML — disco "cleanrooms-ml" segment vs CFN "CleanRoomsML".
		TypeCleanRoomsMLConfiguredModelAlgorithm:            "AWS::CleanRoomsML::ConfiguredModelAlgorithm",
		TypeCleanRoomsMLConfiguredModelAlgorithmAssociation: "AWS::CleanRoomsML::ConfiguredModelAlgorithmAssociation",
		TypeCleanRoomsMLTrainingDataset:                     "AWS::CleanRoomsML::TrainingDataset",
		// CE — disco "ce" segment vs CFN "CE".
		TypeCEAnomalyMonitor:      "AWS::CE::AnomalyMonitor",
		TypeCEAnomalySubscription: "AWS::CE::AnomalySubscription",
		TypeCECostCategory:        "AWS::CE::CostCategory",
		// APS — disco "aps" segment vs CFN "APS" (Workspace/Scraper aliased earlier).
		TypeAPSAnomalyDetector:     "AWS::APS::AnomalyDetector",
		TypeAPSRuleGroupsNamespace: "AWS::APS::RuleGroupsNamespace",
		TypeAPSResourcePolicy:      "AWS::APS::ResourcePolicy",
		// AmplifyUIBuilder — disco "amplify-ui-builder" segment vs CFN "AmplifyUIBuilder".
		TypeAmplifyUIBuilderComponent: "AWS::AmplifyUIBuilder::Component",
		TypeAmplifyUIBuilderForm:      "AWS::AmplifyUIBuilder::Form",
		TypeAmplifyUIBuilderTheme:     "AWS::AmplifyUIBuilder::Theme",
		// SSMQuickSetup — disco "ssm-quick-setup" segment vs CFN "SSMQuickSetup".
		TypeSSMQuickSetupConfigurationManager: "AWS::SSMQuickSetup::ConfigurationManager",
		// SSMIncidents — disco "ssm-incidents" segment vs CFN "SSMIncidents".
		TypeSSMIncidentsReplicationSet: "AWS::SSMIncidents::ReplicationSet",
		TypeSSMIncidentsResponsePlan:   "AWS::SSMIncidents::ResponsePlan",
		// S3ObjectLambda — disco "s3-object-lambda" segment vs CFN "S3ObjectLambda".
		TypeS3ObjectLambdaAccessPoint:       "AWS::S3ObjectLambda::AccessPoint",
		TypeS3ObjectLambdaAccessPointPolicy: "AWS::S3ObjectLambda::AccessPointPolicy",
		// ResilienceHub — disco "resilience-hub" segment vs CFN "ResilienceHub".
		TypeResilienceHubApp:              "AWS::ResilienceHub::App",
		TypeResilienceHubResiliencyPolicy: "AWS::ResilienceHub::ResiliencyPolicy",
		// RAM — disco "ram" segment vs CFN "RAM".
		TypeRAMResourceShare: "AWS::RAM::ResourceShare",
		TypeRAMPermission:    "AWS::RAM::Permission",
		// PCAConnectorSCEP — disco "pca-connector-scep" segment vs CFN "PCAConnectorSCEP".
		TypePCAConnectorSCEPConnector: "AWS::PCAConnectorSCEP::Connector",
		TypePCAConnectorSCEPChallenge: "AWS::PCAConnectorSCEP::Challenge",
		// PaymentCryptography — disco "payment-cryptography" segment vs CFN "PaymentCryptography".
		TypePaymentCryptographyKey:   "AWS::PaymentCryptography::Key",
		TypePaymentCryptographyAlias: "AWS::PaymentCryptography::Alias",
		// MPA — disco "mpa" segment vs CFN "MPA".
		TypeMPAApprovalTeam:   "AWS::MPA::ApprovalTeam",
		TypeMPAIdentitySource: "AWS::MPA::IdentitySource",
		// LicenseManager — disco "license-manager" segment vs CFN "LicenseManager".
		TypeLicenseManagerLicense: "AWS::LicenseManager::License",
		TypeLicenseManagerGrant:   "AWS::LicenseManager::Grant",
		// KinesisVideo — disco "kinesis-video" segment vs CFN "KinesisVideo".
		TypeKinesisVideoStream:           "AWS::KinesisVideo::Stream",
		TypeKinesisVideoSignalingChannel: "AWS::KinesisVideo::SignalingChannel",
		// IVSChat — disco "ivs-chat" segment vs CFN "IVSChat".
		TypeIVSChatRoom:                 "AWS::IVSChat::Room",
		TypeIVSChatLoggingConfiguration: "AWS::IVSChat::LoggingConfiguration",
		// GreengrassV2 — disco "greengrass-v2" segment vs CFN "GreengrassV2".
		TypeGreengrassV2ComponentVersion: "AWS::GreengrassV2::ComponentVersion",
		TypeGreengrassV2Deployment:       "AWS::GreengrassV2::Deployment",
		// EMR — disco "emr:instance-fleet" vs CFN "InstanceFleetConfig"; "instance-group" vs "InstanceGroupConfig".
		TypeEMRInstanceFleet: "AWS::EMR::InstanceFleetConfig",
		TypeEMRInstanceGroup: "AWS::EMR::InstanceGroupConfig",
		// DirectoryService — disco "directory-service" segment vs CFN "DirectoryService".
		TypeDSMicrosoftAD: "AWS::DirectoryService::MicrosoftAD",
		TypeDSSimpleAD:    "AWS::DirectoryService::SimpleAD",
		// WorkSpacesThinClient — disco "workspaces-thin-client" segment vs CFN "WorkSpacesThinClient".
		TypeWorkSpacesThinClientEnvironment: "AWS::WorkSpacesThinClient::Environment",
		// VoiceID — disco "voice-id" segment vs CFN "VoiceID".
		TypeVoiceIDDomain: "AWS::VoiceID::Domain",
		// UXC — disco "uxc" segment vs CFN "UXC".
		TypeUXCAccountCustomization: "AWS::UXC::AccountCustomization",
		// SystemsManagerSAP — disco "systems-manager-sap" segment vs CFN "SystemsManagerSAP".
		TypeSSMSAPApplication: "AWS::SystemsManagerSAP::Application",
		// SSMGuiConnect — disco "ssm-gui-connect" segment vs CFN "SSMGuiConnect".
		TypeSSMGuiConnectPreferences: "AWS::SSMGuiConnect::Preferences",
		// SimSpaceWeaver — disco "sim-space-weaver" segment vs CFN "SimSpaceWeaver".
		TypeSimSpaceWeaverSimulation: "AWS::SimSpaceWeaver::Simulation",
		// RUM — disco "rum" segment vs CFN "RUM".
		TypeRUMAppMonitor: "AWS::RUM::AppMonitor",
		// OSIS — disco "osis" segment vs CFN "OSIS".
		TypeOSISPipeline: "AWS::OSIS::Pipeline",
		// NovaAct — disco "nova-act" segment vs CFN "NovaAct".
		TypeNovaActWorkflowDefinition: "AWS::NovaAct::WorkflowDefinition",
		// NotificationsContacts — disco "notifications-contacts" segment vs CFN "NotificationsContacts".
		TypeNotificationsContactsEmailContact: "AWS::NotificationsContacts::EmailContact",
		// MWAAServerless — disco "mwaa-serverless" segment vs CFN "MWAAServerless".
		TypeMWAAServerlessWorkflow: "AWS::MWAAServerless::Workflow",
		// MediaStore — disco "media-store" segment vs CFN "MediaStore".
		TypeMediaStoreContainer: "AWS::MediaStore::Container",
		// LookoutEquipment — disco "lookout-equipment" segment vs CFN "LookoutEquipment".
		TypeLookoutEquipmentInferenceScheduler: "AWS::LookoutEquipment::InferenceScheduler",
		// LaunchWizard — disco "launch-wizard" segment vs CFN "LaunchWizard".
		TypeLaunchWizardDeployment: "AWS::LaunchWizard::Deployment",
		// KendraRanking — disco "kendra-ranking" segment vs CFN "KendraRanking".
		TypeKendraRankingExecutionPlan: "AWS::KendraRanking::ExecutionPlan",
		// IoTCoreDeviceAdvisor — disco "iot-core-device-advisor" segment vs CFN "IoTCoreDeviceAdvisor".
		TypeIoTDeviceAdvisorSuiteDefinition: "AWS::IoTCoreDeviceAdvisor::SuiteDefinition",
		// InternetMonitor — disco "internet-monitor" segment vs CFN "InternetMonitor".
		TypeInternetMonitorMonitor: "AWS::InternetMonitor::Monitor",
		// HealthLake — disco "health-lake" segment vs CFN "HealthLake".
		TypeHealthLakeFHIRDatastore: "AWS::HealthLake::FHIRDatastore",
		// HealthImaging — disco "health-imaging" segment vs CFN "HealthImaging".
		TypeHealthImagingDatastore: "AWS::HealthImaging::Datastore",
		// FinSpace — disco "fin-space" segment vs CFN "FinSpace".
		TypeFinSpaceEnvironment: "AWS::FinSpace::Environment",
		// EVS — disco "evs" segment vs CFN "EVS".
		TypeEVSEnvironment: "AWS::EVS::Environment",
		// EMRServerless — disco "emr-serverless" segment vs CFN "EMRServerless".
		TypeEMRServerlessApplication: "AWS::EMRServerless::Application",
		// ElementalInference — disco "elemental-inference" segment vs CFN "ElementalInference".
		TypeElementalInferenceFeed: "AWS::ElementalInference::Feed",
		// DSQL — disco "dsql" segment vs CFN "DSQL".
		TypeDSQLCluster: "AWS::DSQL::Cluster",
	}
}

// AlgorithmicKey is the fallback when no alias entry exists. Disco type
// "aws:foo:bar-baz" → "AWS::Foo::BarBaz". The alias map handles the cases
// where this fails (any disco service segment that doesn't map cleanly to
// CFN's segment, e.g. logs vs Logs, ses vs SES, plus all the "aws prefix
// missing" or different-case oddities).
func (coverageProvider) AlgorithmicKey(discoType string) string {
	parts := strings.SplitN(discoType, ":", 3)
	if len(parts) != 3 {
		return discoType
	}
	svc, kind := parts[1], parts[2]
	pascal := func(s string) string {
		segs := strings.Split(s, "-")
		for i, p := range segs {
			if p == "" {
				continue
			}
			segs[i] = strings.ToUpper(p[:1]) + p[1:]
		}
		return strings.Join(segs, "")
	}
	return "AWS::" + pascal(svc) + "::" + pascal(kind)
}

// Fetch pages CloudFormation ListTypes (Public, Resource) and returns every
// AWS-prefixed type. Third-party (community / Hooks / Modules) types are
// filtered out — they're not relevant to disco's coverage matrix.
func (coverageProvider) Fetch(ctx context.Context, opts coverage.FetchOptions) ([]coverage.UpstreamType, error) {
	region := opts.Region
	if region == "" {
		region = "us-east-1"
	}
	cfgOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if opts.Profile != "" {
		cfgOpts = append(cfgOpts, awsconfig.WithSharedConfigProfile(opts.Profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := cloudformation.NewFromConfig(cfg)

	var out []coverage.UpstreamType
	input := &cloudformation.ListTypesInput{
		Visibility: cftypes.VisibilityPublic,
		Type:       cftypes.RegistryTypeResource,
	}
	paginator := cloudformation.NewListTypesPaginator(client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range page.TypeSummaries {
			if s.TypeName == nil {
				continue
			}
			name := *s.TypeName
			// Filter to AWS-vendor types only; third-party + Hooks /
			// Modules carry different prefixes.
			if !strings.HasPrefix(name, "AWS::") {
				continue
			}
			parts := strings.SplitN(name, "::", 3)
			svc := ""
			if len(parts) == 3 {
				svc = parts[1]
			}
			out = append(out, coverage.UpstreamType{Key: name, Service: svc})
		}
	}
	return out, nil
}
