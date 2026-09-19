package aws

import (
	"strings"
	"testing"
)

// TestCanonicalKey pins the SR↔CFN duplicate-collapse normalization: plurals,
// hyphens and the SR "Resource" suffix reduce the two catalog spellings of one
// resource to the same identity. Genuine service renames (CFN MWAA vs SR
// airflow) are not bridged: they surface as registry-only drift.
func TestCanonicalKey(t *testing.T) {
	p := coverageProvider{}
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
		if ka, kb := p.CanonicalKey(c.a), p.CanonicalKey(c.b); ka != kb {
			t.Errorf("CanonicalKey(%q)=%q != CanonicalKey(%q)=%q", c.a, ka, c.b, kb)
		}
	}
	// Distinct resources must NOT collapse.
	diff := []struct{ a, b string }{
		{"AWS::amplify::app", "AWS::amplify::branch"},
		{"AWS::s3::bucket", "AWS::sns::topic"},
	}
	for _, c := range diff {
		if ka, kb := p.CanonicalKey(c.a), p.CanonicalKey(c.b); ka == kb {
			t.Errorf("CanonicalKey collapsed distinct resources: %q and %q both → %q", c.a, c.b, ka)
		}
	}
}

// TestCanonicalKey_NeverEmptySegment guards the over-strip hazard that turned
// "AWS::apigateway::Resources" into ("apigateway",""). A service ending in "s"
// (aidevops) must also survive — services are not de-pluralized.
func TestCanonicalKey_NeverEmptySegment(t *testing.T) {
	p := coverageProvider{}
	for _, k := range []string{
		"AWS::apigateway::Resources", "AWS::apigateway::Resource",
		"AWS::aidevops::service", "AWS::Logs::LogGroup", "AWS::ECS::Cluster",
	} {
		got := p.CanonicalKey(k)
		svc, res, ok := strings.Cut(got, "::")
		if !ok || svc == "" || res == "" {
			t.Errorf("CanonicalKey(%q)=%q has an empty segment", k, got)
		}
	}
	// aidevops keeps its trailing "s" (not singularized as a service).
	if got := p.CanonicalKey("AWS::aidevops::service"); !strings.HasPrefix(got, "aidevops::") {
		t.Errorf("aidevops service segment got de-pluralized: %q", got)
	}
	if p.CanonicalKey("AWS::apigateway::Resources") != p.CanonicalKey("AWS::apigateway::Resource") {
		t.Errorf("Resources/Resource did not collapse")
	}
}
