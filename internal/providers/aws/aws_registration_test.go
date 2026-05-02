package aws

import (
	"slices"
	"testing"
)

// expectedAWSServices is the authoritative list of service names that must be
// registered. Update this list when adding a new service scanner.
var expectedAWSServices = []string{
	"aws:apigateway",
	"aws:apigatewayv2",
	"aws:cloudfront",
	"aws:cloudwatch",
	"aws:logs",
	"aws:dynamodb",
	"aws:ec2",
	"aws:ecr",
	"aws:ecs",
	"aws:eks",
	"aws:elasticache",
	"aws:elasticloadbalancing",
	"aws:elasticloadbalancingv2",
	"aws:route53",
	"aws:iam",
	"aws:imagebuilder",
	"aws:lambda",
	"aws:rds",
	"aws:s3",
	"aws:s3control",
	"aws:sms-voice",
	"aws:sns",
	"aws:sqs",
	"aws:kms",
	"aws:secretsmanager",
	"aws:organizations",
	"aws:acm",
	"aws:kinesis",
	"aws:firehose",
	"aws:frauddetector",
	"aws:efs",
	"aws:wafv2",
	"aws:events",
	"aws:cloudtrail",
	"aws:cognito",
	"aws:sfn",
	"aws:ssm",
	"aws:guardduty",
	"aws:config",
	"aws:backup",
	"aws:acm-pca",
	"aws:kafka",
	"aws:network-firewall",
	"aws:notifications",
	"aws:cleanrooms",
	"aws:cloudformation",
	"aws:sso-admin",
	"aws:shield",
	"aws:macie",
	"aws:securityhub",
	"aws:detective",
	"aws:lakeformation",
	"aws:location",
	"aws:ses",
	"aws:inspector2",
	"aws:glue",
	"aws:athena",
	"aws:autoscaling",
	"aws:autoscaling-plans",
	"aws:redshift",
	"aws:opensearch",
	"aws:omics",
	"aws:opensearchserverless",
	"aws:docdb",
	"aws:neptune",
	"aws:servicecatalog",
	"aws:auditmanager",
	"aws:controltower",
	"aws:customer-profiles",
	"aws:apprunner",
	"aws:batch",
	"aws:lightsail",
	"aws:elasticbeanstalk",
	"aws:emr",
	"aws:emr-containers",
	"aws:devops-guru",
	"aws:dax",
	"aws:codestar-connections",
	"aws:codepipeline",
	"aws:codedeploy",
	"aws:codeartifact",
	"aws:cleanrooms-ml",
	"aws:chatbot",
	"aws:ce",
	"aws:cassandra",
	"aws:amplify-ui-builder",
	"aws:synthetics",
	"aws:ssm-quick-setup",
	"aws:ssm-incidents",
	"aws:signer",
	"aws:scheduler",
	"aws:resource-groups",
	"aws:resilience-hub",
	"aws:ram",
	"aws:pca-connector-scep",
	"aws:payment-cryptography",
	"aws:oam",
	"aws:mpa",
	"aws:license-manager",
	"aws:kinesis-video",
	"aws:ivs-chat",
	"aws:greengrass-v2",
	"aws:forecast",
	"aws:fis",
	"aws:directory-service",
	"aws:comprehend",
	"aws:budgets",
	"aws:workspaces-thin-client",
	"aws:voice-id",
	"aws:uxc",
	"aws:systems-manager-sap",
	"aws:ssm-gui-connect",
	"aws:sim-space-weaver",
	"aws:rum",
	"aws:rbin",
	"aws:pipes",
	"aws:osis",
	"aws:nova-act",
	"aws:notifications-contacts",
	"aws:mwaa-serverless",
	"aws:mwaa",
	"aws:media-store",
	"aws:lookout-equipment",
	"aws:launch-wizard",
	"aws:kendra-ranking",
	"aws:iot-core-device-advisor",
	"aws:invoicing",
	"aws:internet-monitor",
	"aws:interconnect",
	"aws:health-lake",
	"aws:health-imaging",
	"aws:accessanalyzer",
	"aws:aiops",
	"aws:mq",
	"aws:amplify",
	"aws:appconfig",
	"aws:appflow",
	"aws:application-autoscaling",
	"aws:appintegrations",
	"aws:applicationinsights",
	"aws:applicationsignals",
	"aws:aps",
	"aws:arc-region-switch",
	"aws:arc-zonal-shift",
	"aws:backupgateway",
	"aws:bcmdataexports",
	"aws:bcmpricingcalculator",
	"aws:billing",
	"aws:appstream",
	"aws:appsync",
	"aws:bedrock",
	"aws:bedrockagentcore",
	"aws:datasync",
	"aws:datazone",
	"aws:deadline",
	"aws:dms",
	"aws:greengrass",
	"aws:mediaconnect",
	"aws:medialive",
	"aws:networkmanager",
	"aws:pinpoint",
	"aws:qbusiness",
	"aws:quicksight",
	"aws:route53globalresolver",
	"aws:route53resolver",
	"aws:billingconductor",
	"aws:braket",
	"aws:connect",
	"aws:gamelift",
	"aws:iot",
	"aws:iotfleetwise",
	"aws:iotsitewise",
	"aws:iotwireless",
	"aws:ivs",
	"aws:sagemaker",
	"aws:vpclattice",
	"aws:transfer",
	"aws:wisdom",
	"aws:workspaces-web",
	"aws:directconnect",
	"aws:appmesh",
	"aws:observabilityadmin",
	"aws:memorydb",
	"aws:mediatailor",
	"aws:fsx",
	"aws:databrew",
	"aws:timestream",
	"aws:servicediscovery",
	"aws:s3tables",
	"aws:rtbfabric",
	"aws:pca-connector-ad",
	"aws:odb",
	"aws:mediapackagev2",
	"aws:mediapackage",
	"aws:iottwinmaker",
	"aws:entityresolution",
	"aws:cases",
	"aws:xray",
	"aws:verifiedpermissions",
	"aws:ssm-contacts",
	"aws:service-catalog-app-registry",
	"aws:security-lake",
	"aws:security-agent",
	"aws:s3outposts",
	"aws:s3files",
	"aws:route53-recovery-readiness",
	"aws:route53-recovery-control",
	"aws:refactor-spaces",
	"aws:pinpoint-email",
	"aws:personalize",
	"aws:lex",
	"aws:kinesis-analytics-v2",
	"aws:ground-station",
	"aws:global-accelerator",
	"aws:event-schemas",
	"aws:dev-ops-agent",
	"aws:code-build",
	"aws:b2bi",
	"aws:workspaces-instances",
	"aws:work-spaces",
	"aws:support-app",
	"aws:s3vectors",
	"aws:s3express",
	"aws:route53-profiles",
	"aws:roles-anywhere",
	"aws:resource-explorer-2",
	"aws:rekognition",
	"aws:redshift-serverless",
	"aws:proton",
	"aws:pcs",
	"aws:panorama",
	"aws:neptune-graph",
	"aws:media-convert",
	"aws:managed-blockchain",
	"aws:m2",
	"aws:kinesis-analytics",
	"aws:kendra",
	"aws:kafka-connect",
	"aws:iot-events",
	"aws:fms",
}

// TestRegisteredServices_NoDuplicates verifies that no two services share the
// same name. The registerService function panics on duplicates at runtime, but
// this test surfaces the bug in CI rather than at startup.
func TestRegisteredServices_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool, len(registeredServices))
	for _, svc := range registeredServices {
		if seen[svc.name] {
			t.Errorf("duplicate service registration: %q", svc.name)
		}
		seen[svc.name] = true
	}
}

// TestRegisteredServices_ExpectedNames verifies that every expected service is
// registered. Fails if a service is accidentally removed or renamed.
func TestRegisteredServices_ExpectedNames(t *testing.T) {
	registered := make(map[string]bool, len(registeredServices))
	for _, svc := range registeredServices {
		registered[svc.name] = true
	}
	for _, want := range expectedAWSServices {
		if !registered[want] {
			t.Errorf("service %q is not registered", want)
		}
	}
	// Also fail if extra services appear that aren't in the expected list,
	// so that newly added services are consciously acknowledged here.
	for _, svc := range registeredServices {
		if !slices.Contains(expectedAWSServices, svc.name) {
			t.Errorf("unrecognised service %q — add it to expectedAWSServices in this test", svc.name)
		}
	}
}

// TestFilteredServices_Nil verifies that nil filter returns all registered services.
func TestFilteredServices_Nil(t *testing.T) {
	got := filteredServices(nil)
	if len(got) != len(registeredServices) {
		t.Errorf("filteredServices(nil): got %d, want %d", len(got), len(registeredServices))
	}
}

// TestFilteredServices_Subset verifies that a named filter returns only the matching service.
func TestFilteredServices_Subset(t *testing.T) {
	got := filteredServices([]string{"aws:ec2"})
	if len(got) != 1 {
		t.Fatalf("filteredServices([aws:ec2]): got %d results, want 1", len(got))
	}
	if got[0].name != "aws:ec2" {
		t.Errorf("filteredServices([aws:ec2]): got %q, want %q", got[0].name, "aws:ec2")
	}
}

// TestFilteredServices_Unknown verifies that an unknown service name returns an
// empty slice without panicking.
func TestFilteredServices_Unknown(t *testing.T) {
	got := filteredServices([]string{"aws:nonexistent"})
	if len(got) != 0 {
		t.Errorf("filteredServices([aws:nonexistent]): got %d results, want 0", len(got))
	}
}

// TestFilteredServices_Multiple verifies that multiple services can be selected at once.
func TestFilteredServices_Multiple(t *testing.T) {
	got := filteredServices([]string{"aws:ec2", "aws:s3"})
	if len(got) != 2 {
		t.Errorf("filteredServices([aws:ec2,aws:s3]): got %d results, want 2", len(got))
	}
}
