package aws

import (
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/drs"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// expectedAWSServices is the authoritative list of service names that must be
// registered. Update this list when adding a new service scanner.
var expectedAWSServices = []string{
	"aws:storagegateway",
	"aws:savingsplans",
	"aws:swf",
	"aws:serverlessrepo",
	"aws:scn",
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
	"aws:servicequotas",
	"aws:servicequotas-template",
	"aws:sms-voice",
	"aws:sns",
	"aws:snowball",
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
	"aws:waf",
	"aws:wafregional",
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
	"aws:clouddirectory",
	"aws:cloudhsm",
	"aws:cloudsearch",
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
	"aws:devicefarm",
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
	"aws:license-manager-linux-subscriptions",
	"aws:license-manager-user-subscriptions",
	"aws:kinesis-video",
	"aws:ivs-chat",
	"aws:greengrass-v2",
	"aws:forecast",
	"aws:fis",
	"aws:directory-service",
	"aws:directory-service-data",
	"aws:comprehend",
	"aws:compute-optimizer",
	"aws:budgets",
	"aws:workspaces-thin-client",
	"aws:voice-id",
	"aws:uxc",
	"aws:systems-manager-sap",
	"aws:ssm-gui-connect",
	"aws:rum",
	"aws:rbin",
	"aws:pipes",
	"aws:osis",
	"aws:nova-act",
	"aws:notifications-contacts",
	"aws:mwaa-serverless",
	"aws:mwaa",
	"aws:lookout-equipment",
	"aws:launch-wizard",
	"aws:kendra-ranking",
	"aws:iot-core-device-advisor",
	"aws:invoicing",
	"aws:internet-monitor",
	"aws:interconnect",
	"aws:health-lake",
	"aws:health-agent",
	"aws:health-imaging",
	"aws:grafana",
	"aws:fin-space",
	"aws:finspace",
	"aws:evs",
	"aws:emr-serverless",
	"aws:elemental-inference",
	"aws:dsql",
	"aws:doc-db-elastic",
	"aws:dlm",
	"aws:cur",
	"aws:connect-campaigns-v2",
	"aws:connect-campaigns",
	"aws:codestar-notifications",
	"aws:code-guru-reviewer",
	"aws:code-guru-profiler",
	"aws:codecommit",
	"aws:cloud9",
	"aws:chime",
	"aws:accessanalyzer",
	"aws:agent-registry",
	"aws:aidevops",
	"aws:aiops",
	"aws:mq",
	"aws:amplify",
	"aws:appconfig",
	"aws:appfabric",
	"aws:appflow",
	"aws:application-autoscaling",
	"aws:appintegrations",
	"aws:applicationinsights",
	"aws:applicationsignals",
	"aws:aps",
	"aws:arc-region-switch",
	"aws:arc-zonal-shift",
	"aws:backupgateway",
	"aws:bcmdashboards",
	"aws:bcmdataexports",
	"aws:bcmpricingcalculator",
	"aws:billing",
	"aws:appstream",
	"aws:appsync",
	"aws:artifact",
	"aws:bedrock",
	"aws:bedrockagentcore",
	"aws:datasync",
	"aws:dataexchange",
	"aws:datapipeline",
	"aws:datazone",
	"aws:deadline",
	"aws:dms",
	"aws:drs",
	"aws:greengrass",
	"aws:mediaconnect",
	"aws:medialive",
	"aws:networkflowmonitor",
	"aws:networkmanager",
	"aws:networkmonitor",
	"aws:pinpoint",
	"aws:qapps",
	"aws:qbusiness",
	"aws:quicksight",
	"aws:route53globalresolver",
	"aws:route53resolver",
	"aws:billingconductor",
	"aws:braket",
	"aws:connect",
	"aws:gamelift",
	"aws:gameliftstreams",
	"aws:glacier",
	"aws:iot",
	"aws:iotfleetwise",
	"aws:iotmanagedintegrations",
	"aws:iotsitewise",
	"aws:iotwireless",
	"aws:ivs",
	"aws:sagemaker",
	"aws:sagemaker-geospatial",
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
	"aws:outposts",
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
	"aws:security-ir",
	"aws:snow-device-management",
	"aws:social-messaging",
	"aws:s3outposts",
	"aws:s3files",
	"aws:route53-recovery-readiness",
	"aws:route53-recovery-control",
	"aws:refactor-spaces",
	"aws:personalize",
	"aws:lex",
	"aws:kinesis-analytics-v2",
	"aws:ground-station",
	"aws:ground-station-satellites",
	"aws:global-accelerator",
	"aws:event-schemas",
	"aws:dev-ops-agent",
	"aws:code-build",
	"aws:b2bi",
	"aws:workspaces-instances",
	"aws:work-spaces",
	"aws:wellarchitected",
	"aws:workmail",
	"aws:wickr",
	"aws:aco-automation",
	"aws:supportauthz",
	"aws:account-access",
	"aws:support-app",
	"aws:s3vectors",
	"aws:s3express",
	"aws:route53-profiles",
	"aws:roles-anywhere",
	"aws:resource-explorer-2",
	"aws:rekognition",
	"aws:repostspace",
	"aws:redshift-serverless",
	"aws:proton",
	"aws:pcs",
	"aws:polly",
	"aws:pricingplanmanager",
	"aws:neptune-graph",
	"aws:media-convert",
	"aws:managed-blockchain",
	"aws:m2",
	"aws:kendra",
	"aws:kafka-connect",
	"aws:fms",
	"aws:mgn",
	"aws:migrationhub",
	"aws:migrationhub-orchestrator",
	"aws:textract",
	"aws:thinclient",
	"aws:tnb",
	"aws:transcribe",
	"aws:translate",
	"aws:trustedadvisor",
	"aws:mturk",
}

// TestRegisteredServices_NoDuplicates verifies no two services share a name.
// registerService panics on duplicates at runtime; this test surfaces the bug
// in CI rather than at startup.
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
	// Also fail on extra registered services not in the expected list, so new
	// services are consciously acknowledged here.
	for _, svc := range registeredServices {
		if !slices.Contains(expectedAWSServices, svc.name) {
			t.Errorf("unrecognised service %q — add it to expectedAWSServices in this test", svc.name)
		}
	}
}

func filteredServiceNames(filter []string, includeServiceQuotas bool) []string {
	svcs := filteredServices(filter, includeServiceQuotas)
	names := make([]string, len(svcs))
	for i, s := range svcs {
		names[i] = s.name
	}
	return names
}

// optInCount returns how many registered services are opt-in (excluded from the
// default scan). The opt-in assertions below derive expectations from it so a future
// second opt-in service doesn't silently break them.
func optInCount() int {
	n := 0
	for _, s := range registeredServices {
		if s.optIn {
			n++
		}
	}
	return n
}

// TestFilteredServices_DefaultExcludesOptIn verifies a nil filter returns every
// non-opt-in service and omits aws:servicequotas (which is opt-in) unless explicitly
// included.
func TestFilteredServices_DefaultExcludesOptIn(t *testing.T) {
	got := filteredServiceNames(nil, false)
	if want := len(registeredServices) - optInCount(); len(got) != want {
		t.Errorf("filteredServices(nil, false): got %d, want %d (registered minus opt-in)", len(got), want)
	}
	if slices.Contains(got, "aws:servicequotas") {
		t.Error("default scan must exclude opt-in aws:servicequotas")
	}
}

// TestFilteredServices_IncludeOptIn verifies the includeServiceQuotas flag adds
// only the quotas-tagged opt-in services to the default set, and that explicit
// selection by name works for any opt-in service.
func TestFilteredServices_IncludeOptIn(t *testing.T) {
	got := filteredServiceNames(nil, true)
	for _, name := range []string{"aws:servicequotas", "aws:servicequotas-template"} {
		if !slices.Contains(got, name) {
			t.Errorf("filteredServices(nil, true) must include %s", name)
		}
	}
	if slices.Contains(got, "aws:mturk") {
		t.Error("filteredServices(nil, true) must exclude aws:mturk: --include-service-quotas adds only quota services")
	}
	if want := len(registeredServices) - optInCount() + 2; len(got) != want {
		t.Errorf("filteredServices(nil, true): got %d, want %d (non-opt-in plus the two quota services)", len(got), want)
	}
	// Explicit selection wins regardless of includeServiceQuotas.
	got = filteredServiceNames([]string{"aws:servicequotas"}, false)
	if len(got) != 1 || got[0] != "aws:servicequotas" {
		t.Errorf("filteredServices([aws:servicequotas], false): got %v, want [aws:servicequotas]", got)
	}
}

// TestRegisteredServices_QuotasTags pins which services --include-service-quotas
// adds: exactly the two quota scanners carry quotas, and every other opt-in
// service is listed here, so a new opt-in service forces a decision.
func TestRegisteredServices_QuotasTags(t *testing.T) {
	quotaServices := []string{"aws:servicequotas", "aws:servicequotas-template"}
	optInOnly := []string{"aws:mturk"}
	for _, s := range registeredServices {
		wantQuotas := slices.Contains(quotaServices, s.name)
		if s.quotas != wantQuotas {
			t.Errorf("%s quotas = %v, want %v", s.name, s.quotas, wantQuotas)
		}
		if s.quotas && !s.optIn {
			t.Errorf("%s is quotas-tagged but not opt-in", s.name)
		}
		if s.optIn && !s.quotas && !slices.Contains(optInOnly, s.name) {
			t.Errorf("%s is opt-in without quotas; add it to optInOnly if --include-service-quotas must not add it", s.name)
		}
	}
}

// TestFilteredServices_Subset verifies that a named filter returns only the matching service.
func TestFilteredServices_Subset(t *testing.T) {
	got := filteredServices([]string{"aws:ec2"}, false)
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
	got := filteredServices([]string{"aws:nonexistent"}, false)
	if len(got) != 0 {
		t.Errorf("filteredServices([aws:nonexistent]): got %d results, want 0", len(got))
	}
}

// TestFilteredServices_Multiple verifies that multiple services can be selected at once.
func TestFilteredServices_Multiple(t *testing.T) {
	got := filteredServices([]string{"aws:ec2", "aws:s3"}, false)
	if len(got) != 2 {
		t.Errorf("filteredServices([aws:ec2,aws:s3]): got %d results, want 2", len(got))
	}
}

func TestMustJSON_ResultMetadata(t *testing.T) {
	// The SDK fills ResultMetadata on every call; set a value so the case
	// matches a real output rather than a zero one.
	identity := &sts.GetCallerIdentityOutput{Account: ptrStr("123456789012"), Arn: ptrStr("arn:aws:iam::123456789012:root"), UserId: ptrStr("AIDA1")}
	identity.ResultMetadata.Set("requestID", "req-1")
	var nilIdentity *sts.GetCallerIdentityOutput

	cases := []struct {
		name string
		v    any
		want string
	}{
		{
			"output pointer drops the key and keeps every other field", identity,
			`{"Account":"123456789012","Arn":"arn:aws:iam::123456789012:root","UserId":"AIDA1"}`,
		},
		{
			"output value drops the key", *identity,
			`{"Account":"123456789012","Arn":"arn:aws:iam::123456789012:root","UserId":"AIDA1"}`,
		},
		{"output with no members becomes an empty object", &drs.DeleteJobOutput{}, `{}`},
		{"nil output pointer stays null", nilIdentity, `null`},
		{"element without the field is unchanged", ec2types.Tag{Key: ptrStr("team"), Value: ptrStr("dr")}, `{"Key":"team","Value":"dr"}`},
		{
			"own field named ResultMetadata of another type is kept",
			struct{ ResultMetadata map[string]string }{map[string]string{}},
			`{"ResultMetadata":{}}`,
		},
		{"map key named ResultMetadata is kept", map[string]any{"ResultMetadata": map[string]any{}}, `{"ResultMetadata":{}}`},
		{
			// Only the wrapper path re-marshals through a map, so its keys come back sorted.
			"wrapper with fields after the embedded output drops the mid-object key",
			struct {
				*sts.GetCallerIdentityOutput
				Extra string
			}{identity, "x"},
			`{"Account":"123456789012","Arn":"arn:aws:iam::123456789012:root","Extra":"x","UserId":"AIDA1"}`,
		},
		{
			"wrapper keeps a nested member of the same name",
			struct {
				Pre map[string]any
				*drs.DeleteJobOutput
				Extra string
			}{map[string]any{"ResultMetadata": map[string]any{}}, &drs.DeleteJobOutput{}, "x"},
			`{"Extra":"x","Pre":{"ResultMetadata":{}}}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The second call answers from hasResultMetadata's type cache.
			for call := range 2 {
				if got := mustJSON(c.v); got != c.want {
					t.Errorf("call %d: mustJSON = %s, want %s", call+1, got, c.want)
				}
			}
		})
	}
}
