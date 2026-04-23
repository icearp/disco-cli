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
	"aws:lambda",
	"aws:rds",
	"aws:s3",
	"aws:s3control",
	"aws:sns",
	"aws:sqs",
	"aws:kms",
	"aws:secretsmanager",
	"aws:organizations",
	"aws:acm",
	"aws:kinesis",
	"aws:firehose",
	"aws:efs",
	"aws:wafv2",
	"aws:events",
	"aws:cloudtrail",
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
