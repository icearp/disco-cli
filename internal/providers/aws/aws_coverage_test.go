package aws

import (
	"slices"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/coverage"
)

// canonicalKey feeds CanonicalKey a registry key with its own service
// segment, as the fetch produces it.
func canonicalKey(k string) string {
	svc := ""
	if parts := strings.SplitN(k, "::", 3); len(parts) == 3 {
		svc = parts[1]
	}
	return coverageProvider{}.CanonicalKey(coverage.UpstreamType{Key: k, Service: svc})
}

// TestCanonicalKey pins the SR↔CFN duplicate-collapse normalization: plurals,
// hyphens and the SR "Resource" suffix reduce the two catalog spellings of one
// resource to the same identity. Genuine service renames (CFN MWAA vs SR
// airflow) are not bridged: they surface as registry-only drift.
func TestCanonicalKey(t *testing.T) {
	same := []struct{ a, b string }{
		{"AWS::Amplify::App", "AWS::amplify::apps"},                                      // plural
		{"AWS::Amplify::Branch", "AWS::amplify::branches"},                               // -es plural
		{"AWS::AmplifyUIBuilder::Component", "AWS::amplifyuibuilder::ComponentResource"}, // SR Resource suffix
		// A stem ending in "e" only meets its plain spelling when the suffix is
		// trimmed before Ident's trailing e/s strip. "Component" ends in "t",
		// which is why the case above passed while seven real rows drifted.
		{"AWS::AmplifyUIBuilder::Theme", "AWS::amplifyuibuilder::ThemeResource"},
		{"AWS::mgn::SourceServer", "AWS::mgn::SourceServerResource"},
		{"AWS::ACMPCA::CertificateAuthority", "AWS::acm-pca::certificate-authority"}, // hyphen + case
		{"AWS::ElastiCache::Cache", "AWS::elasticache::caches"},                      // -ches plural vs -che singular
	}
	for _, c := range same {
		if ka, kb := canonicalKey(c.a), canonicalKey(c.b); ka != kb {
			t.Errorf("CanonicalKey(%q)=%q != CanonicalKey(%q)=%q", c.a, ka, c.b, kb)
		}
	}
	// Distinct resources must NOT collapse.
	diff := []struct{ a, b string }{
		{"AWS::amplify::app", "AWS::amplify::branch"},
		{"AWS::s3::bucket", "AWS::sns::topic"},
	}
	for _, c := range diff {
		if ka, kb := canonicalKey(c.a), canonicalKey(c.b); ka == kb {
			t.Errorf("CanonicalKey collapsed distinct resources: %q and %q both → %q", c.a, c.b, ka)
		}
	}
}

// TestCanonicalKey_NeverEmptySegment guards the over-strip hazard that turned
// "AWS::apigateway::Resources" into ("apigateway",""). A service ending in "s"
// (aidevops) must also survive — services are not de-pluralized.
func TestCanonicalKey_NeverEmptySegment(t *testing.T) {
	for _, k := range []string{
		"AWS::apigateway::Resources", "AWS::apigateway::Resource",
		"AWS::aidevops::service", "AWS::Logs::LogGroup", "AWS::ECS::Cluster",
	} {
		got := canonicalKey(k)
		svc, res, ok := strings.Cut(got, "::")
		if !ok || svc == "" || res == "" {
			t.Errorf("CanonicalKey(%q)=%q has an empty segment", k, got)
		}
	}
	// aidevops keeps its trailing "s" (not singularized as a service).
	if got := canonicalKey("AWS::aidevops::service"); !strings.HasPrefix(got, "aidevops::") {
		t.Errorf("aidevops service segment got de-pluralized: %q", got)
	}
	if canonicalKey("AWS::apigateway::Resources") != canonicalKey("AWS::apigateway::Resource") {
		t.Errorf("Resources/Resource did not collapse")
	}
}

// TestMarkCFNOnly pins #121: a CloudFormation type with no Service Reference
// twin is cfn-only, a twinned one keeps no reason, and the CFN service is
// spelled the way the Service Reference spells it.
func TestMarkCFNOnly(t *testing.T) {
	cfn := []coverage.UpstreamType{
		{Key: "AWS::EC2::Instance", Service: "EC2"},
		{Key: "AWS::EC2::SecurityGroupIngress", Service: "EC2"},
		{Key: "AWS::MWAA::Environment", Service: "MWAA"},
	}
	sr := []coverage.UpstreamType{{Key: "AWS::ec2::instance", Service: "ec2"}}
	got := markCFNOnly(cfn, sr)
	want := []coverage.UpstreamType{
		{Key: "AWS::EC2::Instance", Service: "ec2"},
		{Key: "AWS::EC2::SecurityGroupIngress", Service: "ec2", Reason: coverage.ReasonCFNOnly},
		{Key: "AWS::MWAA::Environment", Service: "mwaa", Reason: coverage.ReasonCFNOnly},
	}
	if !slices.Equal(got, want) {
		t.Errorf("markCFNOnly = %+v\nwant %+v", got, want)
	}
}
