package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

func init() { coverage.Register(&coverageProvider{}) }

// coverageProvider implements coverage.Provider for AWS. Upstream truth
// source = CloudFormation ListTypes (Visibility=Public, Type=Resource) unioned
// with the credential-free AWS Service Reference catalog (see Fetch).
// Coverage truth source = CollectEmits() in aws_services.go, which unions
// every registerService emits decl plus extraEmits from resolver-side
// synthetic stubs.
type coverageProvider struct{}

func (coverageProvider) Name() string { return "aws" }

// Emits returns CollectEmits() verbatim — the Leaf flag on each TypeDecl
// is set at registration time alongside the scanner's emits decl. Locality
// keeps the decision next to the SDK-shape author who knows whether the
// type carries outbound refs.
func (coverageProvider) Emits() []coverage.TypeDecl { return CollectEmits() }

// ListResolvers implements coverage.ResolverAuditor by adapting the package's
// ListResolvers() registry view into the neutral coverage shape, so cmd can
// render `disco coverage resolvers` without importing this package directly.
func (coverageProvider) ListResolvers() []coverage.ResolverInfo {
	src := ListResolvers()
	out := make([]coverage.ResolverInfo, len(src))
	for i, r := range src {
		out[i] = coverage.ResolverInfo{Name: r.Name, EdgeCount: r.EdgeCount, Services: r.Services}
	}
	return out
}

// ResolverEdgeSources implements coverage.ResolverAuditor: the distinct
// EdgeDecl.Source disco-types declared across every registered resolver.
func (coverageProvider) ResolverEdgeSources() []string {
	edges := CollectResolverEdges()
	seen := make(map[string]struct{}, len(edges))
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		if _, dup := seen[e.Source]; dup {
			continue
		}
		seen[e.Source] = struct{}{}
		out = append(out, e.Source)
	}
	return out
}

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
		TypeEC2ReservedInstances:                        "AWS::ec2::reserved-instances",
		TypeEC2HostReservation:                          "AWS::ec2::host-reservation",
		TypeEC2CapacityBlock:                            "AWS::ec2::capacity-block",
		TypeEC2FpgaImage:                                "AWS::ec2::fpga-image",
		TypeEC2PublicIpv4Pool:                           "AWS::ec2::ipv4pool-ec2",
		TypeEC2Ipv6Pool:                                 "AWS::ec2::ipv6pool-ec2",
		TypeEC2TransitGatewayPolicyTable:                "AWS::ec2::transit-gateway-policy-table",
		TypeEC2TransitGatewayRouteTableAnnouncement:     "AWS::ec2::transit-gateway-route-table-announcement",
		TypeEC2IpamPolicy:                               "AWS::ec2::ipam-policy",
		TypeEC2IpamExternalResourceVerificationToken:    "AWS::ec2::ipam-external-resource-verification-token",
		TypeEC2LocalGateway:                             "AWS::ec2::local-gateway",
		TypeEC2CoipPool:                                 "AWS::ec2::coip-pool",
		TypeEC2OutpostLag:                               "AWS::ec2::outpost-lag",
		TypeEC2SpotInstanceRequest:                      "AWS::ec2::spot-instances-request",
		TypeEC2InstanceEventWindow:                      "AWS::ec2::instance-event-window",
		TypeEC2SecondaryInterface:                       "AWS::ec2::secondary-interface",
		TypeEC2SecondaryNetwork:                         "AWS::ec2::secondary-network",
		TypeEC2SecondarySubnet:                          "AWS::ec2::secondary-subnet",
		TypeECSContainerInstance:                        "AWS::ecs::container-instance",
		TypeEKSAnywhereSubscription:                     "AWS::eks::eks-anywhere-subscription",
		TypeElastiCacheReservedInstance:                 "AWS::elasticache::reserved-instance",
		TypeEMRContainersJobTemplate:                    "AWS::emr-containers::jobTemplate",
		TypeOpenSearchDataSource:                        "AWS::es::datasource",
		TypeEventsEventSource:                           "AWS::events::event-source",
		TypeFSxFileCache:                                "AWS::fsx::file-cache",
		TypeFMSAppsList:                                 "AWS::fms::applications-list",
		TypeFMSProtocolsList:                            "AWS::fms::protocols-list",
		TypeFraudDetectorExternalModel:                  "AWS::frauddetector::external-model",
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
		// lookup-table is Service-Reference-only (no CFN twin); SR spells the
		// service lower-cased and the resource hyphenated, which the PascalCase
		// AlgorithmicKey can't match — bridge to the exact SR key.
		TypeLogsLookupTable:      "AWS::logs::lookup-table",
		TypeEventsEventBus:       "AWS::Events::EventBus",
		TypeEventsRule:           "AWS::Events::Rule",
		TypeEventsConnection:     "AWS::Events::Connection",
		TypeEventsAPIDestination: "AWS::Events::ApiDestination",
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
		// field-level-encryption config/profile are Service-Reference-only (CFN does
		// not model FLE) — alias to the exact SR key.
		TypeCloudFrontFieldLevelEncryptionConfig:  "AWS::cloudfront::field-level-encryption-config",
		TypeCloudFrontFieldLevelEncryptionProfile: "AWS::cloudfront::field-level-encryption-profile",
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
		// CloudFormation. generated-template/resource-scan/type/type-hook are
		// Service-Reference-only (the CFN registry leg has no matching twin) — alias
		// to the exact SR keys.
		TypeCloudFormationStack:             "AWS::CloudFormation::Stack",
		TypeCloudFormationStackSet:          "AWS::CloudFormation::StackSet",
		TypeCloudFormationGeneratedTemplate: "AWS::cloudformation::generatedtemplate",
		TypeCloudFormationResourceScan:      "AWS::cloudformation::resourcescan",
		TypeCloudFormationType:              "AWS::cloudformation::type",
		TypeCloudFormationTypeHook:          "AWS::cloudformation::typeHook",
		// Chime — these Chime-SDK-v2 resources are Service-Reference-only (no CFN
		// twin), and their hyphenated SR resource names don't match the algorithmic
		// PascalCase key, so alias each to its exact SR key. (app-instance/-bot/-user
		// match via their CFN twins and need no alias.)
		TypeChimeChannelFlow:                         "AWS::chime::channel-flow",
		TypeChimeMediaPipeline:                       "AWS::chime::media-pipeline",
		TypeChimeMediaInsightsPipelineConfiguration:  "AWS::chime::media-insights-pipeline-configuration",
		TypeChimeMediaPipelineKinesisVideoStreamPool: "AWS::chime::media-pipeline-kinesis-video-stream-pool",
		TypeChimeSipMediaApplication:                 "AWS::chime::sip-media-application",
		TypeChimeVoiceConnector:                      "AWS::chime::voice-connector",
		TypeChimeVoiceProfileDomain:                  "AWS::chime::voice-profile-domain",
		TypeChimeVoiceProfile:                        "AWS::chime::voice-profile",
		// CodeStar Connections — host has no CFN twin; its only upstream spelling
		// is the hyphenated SR key, which the algorithmic key would strip.
		TypeCodeStarConnectionsHost: "AWS::codestar-connections::Host",
		// Comprehend — entity-recognizer and the inference endpoints have no CFN
		// twin; their only upstream spelling is the hyphenated Service Reference key.
		TypeComprehendEntityRecognizer:           "AWS::comprehend::entity-recognizer",
		TypeComprehendDocumentClassifierEndpoint: "AWS::comprehend::document-classifier-endpoint",
		TypeComprehendEntityRecognizerEndpoint:   "AWS::comprehend::entity-recognizer-endpoint",
		// Connect — authentication-profile has no CFN twin; its only upstream
		// spelling is the hyphenated Service Reference key.
		TypeConnectAuthenticationProfile: "AWS::connect::authentication-profile",
		// DataExchange — the disco types mirror the plural Service Reference keys.
		// CFN's singular twins (AWS::DataExchange::DataSet, ::Revision) collapse
		// into these via canonical-identity dedup, so coverage stays clean.
		TypeDataExchangeDataSets:     "AWS::dataexchange::data-sets",
		TypeDataExchangeDataGrants:   "AWS::dataexchange::data-grants",
		TypeDataExchangeEventActions: "AWS::dataexchange::event-actions",
		// DeviceFarm — the internal "TestGrid" capital is invisible to the
		// algorithmic PascalCase key (it yields "TestgridProject"); alias to the
		// hyphenated Service Reference spelling so both upstream twins resolve.
		TypeDeviceFarmTestGridProject: "AWS::devicefarm::testgrid-project",
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
		// AuditManager (Control/Framework are synthetic — no CFN type).
		TypeAuditManagerAssessment: "AWS::AuditManager::Assessment",
		// Backup.
		TypeBackupVault:                   "AWS::Backup::BackupVault",
		TypeBackupLogicallyAirGappedVault: "AWS::Backup::LogicallyAirGappedBackupVault",
		TypeBackupPlan:                    "AWS::Backup::BackupPlan",
		TypeBackupSelection:               "AWS::Backup::BackupSelection",
		TypeBackupGatewayHypervisor:       "AWS::BackupGateway::Hypervisor",
		// gateway + virtual-machine are Service-Reference-only (CFN models just the
		// hypervisor); SR uses the hyphenated "backup-gateway" service segment, which
		// the algorithmic key (no hyphen) can't reproduce, so alias both explicitly.
		TypeBackupGatewayGateway:        "AWS::backup-gateway::gateway",
		TypeBackupGatewayVirtualMachine: "AWS::backup-gateway::virtualmachine",
		TypeBCMDataExportsExport:        "AWS::BCMDataExports::Export",
		// bcm-dashboards exists only in the hyphenated Service Reference catalog.
		TypeBCMDashboardsDashboard:           "AWS::bcm-dashboards::dashboard",
		TypeBCMDashboardsScheduledReport:     "AWS::bcm-dashboards::scheduled-report",
		TypeBcmPricingCalculatorBillScenario: "AWS::BcmPricingCalculator::BillScenario",
		// bill-estimate + workload-estimate exist only in the hyphenated Service
		// Reference catalog (no CloudFormation type), so alias to the exact SR key.
		TypeBcmPricingCalculatorBillEstimate:     "AWS::bcm-pricing-calculator::bill-estimate",
		TypeBcmPricingCalculatorWorkloadEstimate: "AWS::bcm-pricing-calculator::workload-estimate",
		// Bedrock models exist only in the Service Reference catalog (no CFN type),
		// so alias to the exact SR keys.
		TypeBedrockCustomModel:              "AWS::bedrock::custom-model",
		TypeBedrockImportedModel:            "AWS::bedrock::imported-model",
		TypeBedrockProvisionedModel:         "AWS::bedrock::provisioned-model",
		TypeBedrockCustomModelDeployment:    "AWS::bedrock::custom-model-deployment",
		TypeBedrockMarketplaceModelEndpoint: "AWS::bedrock::bedrock-marketplace-model-endpoint",
		// foundation-model + system-defined inference-profile catalogs are SR-only.
		TypeBedrockFoundationModel:  "AWS::bedrock::foundation-model",
		TypeBedrockInferenceProfile: "AWS::bedrock::inference-profile",
		// Bedrock Data Automation (bedrockdataautomation SDK). blueprint is SR-only;
		// project + library also exist as CFN twins that collapse canonically.
		TypeBedrockBlueprint:             "AWS::bedrock::blueprint",
		TypeBedrockDataAutomationProject: "AWS::bedrock::data-automation-project",
		TypeBedrockDataAutomationLibrary: "AWS::bedrock::data-automation-library",
		// AgentCore system code-interpreter exists only in the Service Reference
		// (CFN models CodeInterpreterCustom but no plain CodeInterpreter); the
		// system browser does have a CFN twin so it needs no alias.
		TypeBedrockAgentCoreCodeInterpreter: "AWS::bedrock-agentcore::code-interpreter",
		// registry, registry-record, harness-endpoint and policy-generation exist
		// only in the Service Reference (no CFN type); alias to the exact SR keys.
		TypeBedrockAgentCoreRegistry:         "AWS::bedrock-agentcore::registry",
		TypeBedrockAgentCoreRegistryRecord:   "AWS::bedrock-agentcore::registry-record",
		TypeBedrockAgentCoreHarnessEndpoint:  "AWS::bedrock-agentcore::harness-endpoint",
		TypeBedrockAgentCorePolicyGeneration: "AWS::bedrock-agentcore::policy-generation",
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
		// Macie. ClassificationJob has no CFN type but the Service Reference
		// catalog lists it under service "macie2" (disco's segment is "macie"),
		// so it needs an explicit alias — the others match algorithmically.
		TypeMacieAllowList:            "AWS::Macie::AllowList",
		TypeMacieCustomDataIdentifier: "AWS::Macie::CustomDataIdentifier",
		TypeMacieClassificationJob:    "AWS::macie2::ClassificationJob",
		TypeMacieMember:               "AWS::macie2::Member",
		// MediaLive input-device: the Service Reference catalog spells it
		// hyphenated (input-device); the algorithmic Pascal form folds the
		// hyphen away (InputDevice), so it needs an explicit alias.
		TypeMediaLiveInputDevice: "AWS::medialive::input-device",
		// LakeFormation / NetworkFirewall / OpenSearch / Organizations.
		TypeLakeFormationResource:         "AWS::LakeFormation::Resource",
		TypeNetworkFirewallFirewall:       "AWS::NetworkFirewall::Firewall",
		TypeNetworkFirewallFirewallPolicy: "AWS::NetworkFirewall::FirewallPolicy",
		TypeNetworkFirewallRuleGroup:      "AWS::NetworkFirewall::RuleGroup",
		// Proxy types exist only in the Service Reference catalog (no CFN
		// type yet), which spells the service hyphenated/lower-cased — the
		// algorithmic key folds to "NetworkFirewall", so pin the SR spelling.
		TypeNetworkFirewallProxyConfiguration: "AWS::network-firewall::ProxyConfiguration",
		TypeNetworkFirewallProxyRuleGroup:     "AWS::network-firewall::ProxyRuleGroup",
		TypeOpenSearchDomain:                  "AWS::OpenSearchService::Domain",
		TypeOrganization:                      "AWS::Organizations::Organization",
		TypeOrganizationsAccount:              "AWS::Organizations::Account",
		TypeOrganizationsOU:                   "AWS::Organizations::OrganizationalUnit",
		TypeOrganizationsSCP:                  "AWS::Organizations::Policy",
		// AppRunner / SES / Service Catalog / SecurityHub / Shield / SNS / SQS.
		TypeAppRunnerService:               "AWS::AppRunner::Service",
		TypeAppRunnerVPCConnector:          "AWS::AppRunner::VpcConnector",
		TypeSESEmailIdentity:               "AWS::SES::EmailIdentity",
		TypeSESConfigurationSet:            "AWS::SES::ConfigurationSet",
		TypeServiceCatalogPortfolio:        "AWS::ServiceCatalog::Portfolio",
		TypeServiceCatalogProduct:          "AWS::ServiceCatalog::CloudFormationProduct",
		TypeSecurityHubHub:                 "AWS::SecurityHub::Hub",
		TypeSecurityHubInsight:             "AWS::SecurityHub::Insight",
		TypeSecurityHubProductSubscription: "AWS::SecurityHub::ProductSubscription",
		TypeShieldProtection:               "AWS::Shield::Protection",
		TypeShieldProtectionGroup:          "AWS::Shield::ProtectionGroup",
		TypeSNSTopic:                       "AWS::SNS::Topic",
		TypeSQSQueue:                       "AWS::SQS::Queue",
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
		TypeACMAccount:                "AWS::CertificateManager::Account",
		TypeACMPrivateCA:              "AWS::ACMPCA::CertificateAuthority",
		// IAM Access Analyzer (CFN service segment "AccessAnalyzer" mixed-case).
		TypeAccessAnalyzerAnalyzer: "AWS::AccessAnalyzer::Analyzer",
		// Archive rules exist only in the Service Reference catalog, under the
		// hyphenated "access-analyzer" service segment (CFN models them as a
		// property of the Analyzer, not a standalone type).
		TypeAccessAnalyzerArchiveRule: "AWS::access-analyzer::ArchiveRule",
		// AI DevOps agent — agentspace/service/associations match algorithmically;
		// only private-connection needs an alias (Service Reference spells the
		// resource hyphenated; CFN spells it PascalCase as a separate dup key).
		TypeAidevopsPrivateConnection: "AWS::aidevops::private-connection",
		// Amplify webhooks — Service Reference only (CFN models no standalone
		// webhook type); resource segment is plural to mirror the catalog.
		TypeAmplifyWebhooks: "AWS::amplify::webhooks",
		// AWS Artifact — Service Reference only (not in the CloudFormation
		// registry); the algorithmic key strips the hyphen (CustomerAgreement),
		// so the hyphenated SR key needs an explicit alias. (report matches
		// algorithmically; agreement is skipped.)
		TypeArtifactCustomerAgreement: "AWS::artifact::customer-agreement",
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
		// audience-model/configured-audience-model/ml-input-channel/trained-model
		// are Service-Reference-only (no CFN twin) — alias to the exact SR key.
		TypeCleanRoomsMLAudienceModel:           "AWS::cleanrooms-ml::audiencemodel",
		TypeCleanRoomsMLConfiguredAudienceModel: "AWS::cleanrooms-ml::configuredaudiencemodel",
		TypeCleanRoomsMLMLInputChannel:          "AWS::cleanrooms-ml::MLInputChannel",
		TypeCleanRoomsMLTrainedModel:            "AWS::cleanrooms-ml::TrainedModel",
		// configured-audience-model-association is Service-Reference-only (no CFN
		// twin) — alias to the exact SR key.
		TypeCleanRoomsConfiguredAudienceModelAssociation: "AWS::cleanrooms::configuredaudiencemodelassociation",
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
		// license-configuration / report-generator / license-asset-group /
		// license-asset-ruleset are Service-Reference-only (no CFN twin); SR
		// spells the service hyphenated, which the PascalCase AlgorithmicKey
		// (LicenseManager) can't match — bridge to the exact SR keys.
		TypeLicenseManagerLicenseConfiguration: "AWS::license-manager::license-configuration",
		TypeLicenseManagerReportGenerator:      "AWS::license-manager::report-generator",
		TypeLicenseManagerLicenseAssetGroup:    "AWS::license-manager::license-asset-group",
		TypeLicenseManagerLicenseAssetRuleset:  "AWS::license-manager::license-asset-ruleset",
		// LicenseManagerLinuxSubscriptions / LicenseManagerUserSubscriptions are
		// Service-Reference-only; SR spells both the service and resource segments
		// lowercase-hyphenated, which the PascalCase AlgorithmicKey can't match —
		// bridge to the exact SR keys.
		TypeLicenseManagerLinuxSubscriptionsSubscriptionProvider: "AWS::license-manager-linux-subscriptions::subscription-provider",
		TypeLicenseManagerUserSubscriptionsIdentityProvider:      "AWS::license-manager-user-subscriptions::identity-provider",
		TypeLicenseManagerUserSubscriptionsLicenseServerEndpoint: "AWS::license-manager-user-subscriptions::license-server-endpoint",
		TypeLicenseManagerUserSubscriptionsProductSubscription:   "AWS::license-manager-user-subscriptions::product-subscription",
		TypeLicenseManagerUserSubscriptionsInstanceUser:          "AWS::license-manager-user-subscriptions::instance-user",
		// KinesisVideo — disco "kinesis-video" segment vs CFN "KinesisVideo".
		TypeKinesisVideoStream:           "AWS::KinesisVideo::Stream",
		TypeKinesisVideoSignalingChannel: "AWS::KinesisVideo::SignalingChannel",
		// IVS — the Service Reference spells this resource with an internal hyphen
		// (Ad-Configuration), which the algorithmic key collapses to AdConfiguration;
		// bridge the exact catalog spelling so the direct match holds.
		TypeIVSAdConfiguration: "AWS::ivs::Ad-Configuration",
		// IVSChat — disco "ivs-chat" segment vs CFN "IVSChat".
		TypeIVSChatRoom:                 "AWS::IVSChat::Room",
		TypeIVSChatLoggingConfiguration: "AWS::IVSChat::LoggingConfiguration",
		// GreengrassV2 — disco "greengrass-v2" segment vs CFN "GreengrassV2".
		TypeGreengrassV2ComponentVersion: "AWS::GreengrassV2::ComponentVersion",
		TypeGreengrassV2Deployment:       "AWS::GreengrassV2::Deployment",
		// component + core-device are scanned via GreengrassV2 but the Service
		// Reference catalogs them under the legacy "greengrass" service (no CFN
		// twin), so the algorithmic AWS::GreengrassV2::* key won't match — bridge
		// explicitly to the SR keys.
		TypeGreengrassV2Component:  "AWS::greengrass::component",
		TypeGreengrassV2CoreDevice: "AWS::greengrass::coreDevice",
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
		// RUM — disco "rum" segment vs CFN "RUM".
		TypeRUMAppMonitor: "AWS::RUM::AppMonitor",
		// OSIS — disco "osis" segment vs CFN "OSIS". The Service Reference lists
		// the blueprint/endpoint with hyphenated lowercase keys the algorithmic
		// PascalCase fold can't reach.
		TypeOSISPipeline:          "AWS::OSIS::Pipeline",
		TypeOSISPipelineBlueprint: "AWS::osis::pipeline-blueprint",
		TypeOSISPipelineEndpoint:  "AWS::osis::pipeline-endpoint",
		// ODB — Service Reference uses hyphenated lowercase resource keys.
		TypeODBAutonomousDatabase:       "AWS::odb::autonomous-database",
		TypeODBAutonomousDatabaseBackup: "AWS::odb::autonomous-database-backup",
		TypeODBDbNode:                   "AWS::odb::db-node",
		// NovaAct — disco "nova-act" segment vs CFN "NovaAct".
		TypeNovaActWorkflowDefinition: "AWS::NovaAct::WorkflowDefinition",
		// NotificationsContacts — disco "notifications-contacts" segment vs CFN "NotificationsContacts".
		TypeNotificationsContactsEmailContact: "AWS::NotificationsContacts::EmailContact",
		// MWAAServerless — disco "mwaa-serverless" segment vs CFN "MWAAServerless".
		TypeMWAAServerlessWorkflow: "AWS::MWAAServerless::Workflow",
		// LookoutEquipment — disco "lookout-equipment" segment vs CFN "LookoutEquipment".
		TypeLookoutEquipmentInferenceScheduler: "AWS::LookoutEquipment::InferenceScheduler",
		// dataset / model match the SR keys algorithmically (single-word); the
		// hyphenated label-group / model-version are Service-Reference-only and
		// need the exact SR key (lower-cased service, hyphenated resource).
		TypeLookoutEquipmentLabelGroup:   "AWS::lookoutequipment::label-group",
		TypeLookoutEquipmentModelVersion: "AWS::lookoutequipment::model-version",
		// Lex test-set is Service-Reference-only (no CFN twin) and SR spells it
		// lower-cased + space-separated ("test set"), which the PascalCase
		// AlgorithmicKey can't match — bridge to the exact SR key.
		TypeLexTestSet: "AWS::lex::test set",
		// LaunchWizard — disco "launch-wizard" segment vs CFN "LaunchWizard".
		TypeLaunchWizardDeployment: "AWS::LaunchWizard::Deployment",
		// KendraRanking — disco "kendra-ranking" segment vs CFN "KendraRanking".
		TypeKendraRankingExecutionPlan: "AWS::KendraRanking::ExecutionPlan",
		// Kendra — these per-index children are Service-Reference-only (no CFN
		// twin) and SR spells them hyphenated, which the PascalCase
		// AlgorithmicKey (AccessControlConfiguration) can't match lowercase-exact.
		// (experience/thesaurus are single-word and match algorithmically.)
		TypeKendraAccessControlConfiguration: "AWS::kendra::access-control-configuration",
		TypeKendraFeaturedResultsSet:         "AWS::kendra::featured-results-set",
		TypeKendraQuerySuggestionsBlockList:  "AWS::kendra::query-suggestions-block-list",
		// IoTCoreDeviceAdvisor — disco "iot-core-device-advisor" segment vs CFN "IoTCoreDeviceAdvisor".
		TypeIoTDeviceAdvisorSuiteDefinition: "AWS::IoTCoreDeviceAdvisor::SuiteDefinition",
		// IoTManagedIntegrations — the Service Reference catalog spells these
		// resources hyphenated (account-association, …), which the PascalCase
		// AlgorithmicKey (AccountAssociation) cannot match lowercase-exact.
		TypeIoTManagedIntegrationsAccountAssociation:  "AWS::iotmanagedintegrations::account-association",
		TypeIoTManagedIntegrationsCredentialLocker:    "AWS::iotmanagedintegrations::credential-locker",
		TypeIoTManagedIntegrationsManagedThing:        "AWS::iotmanagedintegrations::managed-thing",
		TypeIoTManagedIntegrationsOtaTask:             "AWS::iotmanagedintegrations::ota-task",
		TypeIoTManagedIntegrationsProvisioningProfile: "AWS::iotmanagedintegrations::provisioning-profile",
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
		// DocDBElastic — disco "doc-db-elastic" segment vs CFN "DocDBElastic".
		TypeDocDBElasticCluster: "AWS::DocDBElastic::Cluster",
		// cluster-snapshot is Service-Reference-only (no CFN twin); its hyphenated
		// SR key won't match the algorithmic PascalCase key, so alias it directly.
		TypeDocDBElasticClusterSnapshot: "AWS::docdb-elastic::cluster-snapshot",
		// DLM — disco "dlm" segment vs CFN "DLM".
		TypeDLMLifecyclePolicy: "AWS::DLM::LifecyclePolicy",
		// CUR — disco "cur" segment vs CFN "CUR".
		TypeCURReportDefinition: "AWS::CUR::ReportDefinition",
		// ConnectCampaignsV2 — disco "connect-campaigns-v2" segment vs CFN "ConnectCampaignsV2".
		TypeConnectCampaignsV2Campaign: "AWS::ConnectCampaignsV2::Campaign",
		// ConnectCampaigns — disco "connect-campaigns" segment vs CFN "ConnectCampaigns".
		TypeConnectCampaignsCampaign: "AWS::ConnectCampaigns::Campaign",
		// CodeStarNotifications — disco "codestar-notifications" segment vs CFN "CodeStarNotifications".
		TypeCodeStarNotificationsNotificationRule: "AWS::CodeStarNotifications::NotificationRule",
		// CodeGuruReviewer — disco "code-guru-reviewer" segment vs CFN "CodeGuruReviewer".
		TypeCodeGuruReviewerRepositoryAssociation: "AWS::CodeGuruReviewer::RepositoryAssociation",
		// CodeGuruProfiler — disco "code-guru-profiler" segment vs CFN "CodeGuruProfiler".
		TypeCodeGuruProfilerProfilingGroup: "AWS::CodeGuruProfiler::ProfilingGroup",
		// GameLift Streams stream-group — the Service Reference key spells the
		// resource "stream group" (with a space), which no algorithmic key can
		// produce; alias it directly.
		TypeGameLiftStreamsStreamGroup: "AWS::gameliftstreams::stream group",
		// MGN (Application Migration Service) — Service-Reference-only (no CFN
		// twin). The SR keys carry a "Resource" suffix (SourceServerResource) and
		// a lowercase service segment that no algorithmic key can produce, so alias
		// each scanned type to the exact SR key. The mirror test strips a trailing
		// "Resource" (mirroring canonResource) so disco's suffix-less type matches.
		TypeMGNSourceServer:                     "AWS::mgn::SourceServerResource",
		TypeMGNApplication:                      "AWS::mgn::ApplicationResource",
		TypeMGNWave:                             "AWS::mgn::WaveResource",
		TypeMGNConnector:                        "AWS::mgn::ConnectorResource",
		TypeMGNLaunchConfigurationTemplate:      "AWS::mgn::LaunchConfigurationTemplateResource",
		TypeMGNReplicationConfigurationTemplate: "AWS::mgn::ReplicationConfigurationTemplateResource",
		TypeMGNVcenterClient:                    "AWS::mgn::VcenterClientResource",
		TypeMGNNetworkMigrationDefinition:       "AWS::mgn::NetworkMigrationDefinitionResource",
		// Migration Hub — disco "migrationhub" segment vs SR "mgh"; the SR key is
		// lowercase-exact, which no algorithmic key can produce.
		TypeMigrationHubProgressUpdateStream: "AWS::mgh::progressUpdateStream",
		// Migration Hub Orchestrator — SR spells the service hyphenated and the
		// resources lowercase, which the PascalCase algorithmic key can't match.
		TypeMigrationHubOrchestratorWorkflow: "AWS::migrationhub-orchestrator::workflow",
		TypeMigrationHubOrchestratorTemplate: "AWS::migrationhub-orchestrator::template",
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

// canonService normalizes a service segment: lowercase, hyphens/underscores
// stripped, then a serviceRenames bridge for the few services CFN and the
// Service Reference name differently beyond case/hyphen. Services are NOT
// de-pluralized — many legitimately end in "s" (aidevops, logs, ecs, sns),
// and CFN/SR agree on the plural ("Logs"↔"logs"), so stripping it would
// desync the two spellings.
func canonService(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	if r, ok := serviceRenames[s]; ok {
		s = r
	}
	return s
}

// canonResource normalizes a resource segment: lowercase, hyphens/underscores
// stripped, de-pluralized, and the Service-Reference "Resource" suffix removed
// when a non-empty stem remains (so "Resources" stays "resource", never
// collapses to ""). Singularization is intentionally crude — it only has to be
// *consistent* across the two spellings of one resource, not linguistically
// correct, since the result is an internal matching identity, never displayed.
func canonResource(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	switch {
	case strings.HasSuffix(s, "ies"): // policies → policy
		s = s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "sses"), // addresses → address
		strings.HasSuffix(s, "ches"), // branches → branch
		strings.HasSuffix(s, "shes"), // meshes → mesh
		strings.HasSuffix(s, "xes"),  // boxes → box
		strings.HasSuffix(s, "zes"):  // quizzes → quiz
		s = s[:len(s)-2]
	case strings.HasSuffix(s, "ss"):
		// keep — "access", "address" are not plurals.
	case strings.HasSuffix(s, "s"):
		s = s[:len(s)-1]
	}
	if stem := strings.TrimSuffix(s, "resource"); stem != "" && stem != s {
		s = stem
	}
	return s
}

// serviceRenames bridges the few services CloudFormation and the Service
// Reference name differently beyond mere case/hyphen (CFN "MWAA" vs SR
// "airflow"). Keyed and valued in canonService form (lowercase, no hyphens).
// Extend as the A→Z buildout surfaces more genuine renames — the canonicalizer
// handles every other case.
var serviceRenames = map[string]string{
	"airflow":           "mwaa",                 // SR airflow ↔ CFN MWAA
	"airflowserverless": "mwaaserverless",       // SR airflow-serverless ↔ CFN MWAAServerless
	"acm":               "certificatemanager",   // SR acm ↔ CFN CertificateManager
	"devopsagent":       "aidevops",             // CFN DevOpsAgent ↔ SR aidevops
	"aoss":              "opensearchserverless", // SR aoss ↔ CFN OpenSearchServerless
	"codeconnections":   "codestarconnections",  // SR codeconnections ↔ scanned aws:codestar-connections (AWS renamed the service)
	"cognitoidentity":   "cognito",              // SR cognito-identity (identity pools) ↔ unified CFN/scanned Cognito
	"cognitoidp":        "cognito",              // SR cognito-idp (user pools) ↔ unified CFN/scanned Cognito
	"elasticfilesystem": "efs",                  // SR elasticfilesystem ↔ CFN EFS / scanned aws:efs
	"elasticmapreduce":  "emr",                  // SR elasticmapreduce ↔ CFN EMR / scanned aws:emr
	"firehose":          "kinesisfirehose",      // SR firehose ↔ CFN KinesisFirehose / scanned aws:firehose
	"geo":               "location",             // SR geo ↔ CFN Location / scanned aws:location
	"kafka":             "msk",                  // SR kafka ↔ CFN MSK / scanned aws:kafka
	"medicalimaging":    "healthimaging",        // SR medical-imaging ↔ CFN HealthImaging / scanned aws:health-imaging
	"mgh":               "migrationhub",         // SR mgh ↔ scanned aws:migrationhub (SDK service migrationhub)
	"opensearch":        "opensearchservice",    // SR opensearch ↔ CFN OpenSearchService / scanned aws:opensearchservice
	"es":                "opensearchservice",    // legacy Elasticsearch IAM prefix ↔ CFN OpenSearchService
	"profile":           "customerprofiles",     // SR profile ↔ CFN CustomerProfiles / scanned aws:customer-profiles
}

// CanonicalKey normalizes an "AWS::svc::res" upstream key to a catalog-agnostic
// identity so a CloudFormation spelling and its Service-Reference twin collapse
// to one resource (e.g. AWS::Amplify::App and AWS::amplify::apps both →
// "amplify::app"). coverage.Build uses it to treat an uncovered upstream key as
// covered when its identity matches an already-covered key — the cross-catalog
// duplicate case. The covered-vs-leftover asymmetry (one side is a disco-emitted
// alias target, the other an unmatched catalog entry) is what scopes the merge;
// `coverage services --filter duplicate` surfaces every collapse for audit.
func (coverageProvider) CanonicalKey(upstreamKey string) string {
	parts := strings.SplitN(upstreamKey, "::", 3)
	if len(parts) != 3 {
		return strings.ToLower(upstreamKey)
	}
	return canonService(parts[1]) + "::" + canonResource(parts[2])
}

// Fetch returns the union of CloudFormation ListTypes (Public, Resource) and
// the AWS Service Reference catalog. CFN supplies registry-modeled resources;
// Service Reference supplies the SDK-real resources CFN omits. Third-party CFN
// types (community / Hooks / Modules) are filtered out — not relevant to
// disco's coverage matrix.
func (coverageProvider) Fetch(ctx context.Context, opts coverage.FetchOptions) ([]coverage.UpstreamType, error) {
	regions := opts.Regions
	if len(regions) == 0 {
		regions = []string{"us-east-1"}
	}
	seen := map[string]struct{}{}
	var out []coverage.UpstreamType
	for _, region := range regions {
		cfgOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
		if opts.Profile != "" {
			cfgOpts = append(cfgOpts, awsconfig.WithSharedConfigProfile(opts.Profile))
		}
		cfg, err := awsconfig.LoadDefaultConfig(ctx, cfgOpts...)
		if err != nil {
			return nil, fmt.Errorf("load aws config (%s): %w", region, err)
		}
		client := cloudformation.NewFromConfig(cfg)

		input := &cloudformation.ListTypesInput{
			Visibility: cftypes.VisibilityPublic,
			Type:       cftypes.RegistryTypeResource,
		}
		paginator := cloudformation.NewListTypesPaginator(client, input)
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("cfn ListTypes (%s): %w", region, err)
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
				if _, dup := seen[name]; dup {
					continue
				}
				seen[name] = struct{}{}
				parts := strings.SplitN(name, "::", 3)
				svc := ""
				if len(parts) == 3 {
					svc = parts[1]
				}
				out = append(out, coverage.UpstreamType{Key: name, Service: svc})
			}
		}
	}

	// Union the credential-free AWS Service Reference catalog. CFN's registry
	// only lists resources with a CloudFormation provider; Service Reference
	// supplies the SDK-real resources CFN omits (DynamoDB streams, AuditManager
	// controls, IdentityStore users, Macie classification jobs). Both fetches
	// are fatal on failure — the union requires both, so a partial fetch can't
	// silently re-introduce false upstream-missing rows. coverage.Build dedupes
	// the overlap case-insensitively.
	srTypes, err := fetchServiceReference(ctx)
	if err != nil {
		return nil, fmt.Errorf("service reference fetch: %w", err)
	}
	out = append(out, srTypes...)
	return out, nil
}

// FetchRegions calls ec2:DescribeRegions(AllRegions=true) and returns the
// authoritative AWS region-name list, filtered to commercial-partition
// regions the caller can opt into. Excludes regions the account hasn't
// opted into (Status != "opt-in-not-required" && != "opted-in") so they
// don't masquerade as missing in `disco coverage --regions`.
func (coverageProvider) FetchRegions(ctx context.Context, opts coverage.FetchOptions) ([]string, error) {
	region := "us-east-1"
	if len(opts.Regions) > 0 {
		region = opts.Regions[0]
	}
	cfgOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	if opts.Profile != "" {
		cfgOpts = append(cfgOpts, awsconfig.WithSharedConfigProfile(opts.Profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	client := ec2.NewFromConfig(cfg)
	allRegions := true
	out, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: &allRegions,
		Filters: []ec2types.Filter{{
			Name:   sp("opt-in-status"),
			Values: []string{"opt-in-not-required", "opted-in"},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("ec2:DescribeRegions: %w", err)
	}
	regions := make([]string, 0, len(out.Regions))
	for _, r := range out.Regions {
		if r.RegionName != nil {
			regions = append(regions, *r.RegionName)
		}
	}
	return regions, nil
}
