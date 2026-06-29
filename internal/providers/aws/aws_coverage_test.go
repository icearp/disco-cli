package aws

import (
	"strings"
	"testing"
)

// TestLeafTypesNotResolverSources guards against marking a type as leaf
// (Leaf: true on the scanner's emits decl) when a resolver actually emits
// edges from it. Such a misclassification silently hides the type from
// `disco coverage resolvers --missing` without removing the resolver —
// bug-attractant.
func TestLeafTypesNotResolverSources(t *testing.T) {
	sources := map[string]bool{}
	for _, e := range CollectResolverEdges() {
		sources[e.Source] = true
	}
	for _, decl := range CollectEmits() {
		if !decl.Leaf {
			continue
		}
		if sources[decl.DiscoType] {
			t.Errorf("emits[%q] flagged Leaf: true but type appears as resolver source — drop the Leaf flag or remove the resolver", decl.DiscoType)
		}
	}
}

// TestCanonicalKey pins the SR↔CFN duplicate-collapse normalization: plurals,
// hyphens, the SR "Resource" suffix, and the service-rename bridge all reduce
// the two catalog spellings of one resource to the same identity.
func TestCanonicalKey(t *testing.T) {
	p := coverageProvider{}
	same := []struct{ a, b string }{
		{"AWS::Amplify::App", "AWS::amplify::apps"},                                      // plural
		{"AWS::Amplify::Branch", "AWS::amplify::branches"},                               // -es plural
		{"AWS::AmplifyUIBuilder::Component", "AWS::amplifyuibuilder::ComponentResource"}, // SR Resource suffix
		{"AWS::ACMPCA::CertificateAuthority", "AWS::acm-pca::certificate-authority"},     // hyphen + case
		{"AWS::CertificateManager::Certificate", "AWS::acm::certificate"},                // service rename acm
		{"AWS::MWAA::Environment", "AWS::airflow::environment"},                          // service rename airflow
		{"AWS::MWAAServerless::Workflow", "AWS::airflow-serverless::Workflow"},           // rename + hyphen
		{"AWS::aidevops::private-connection", "AWS::DevOpsAgent::PrivateConnection"},     // rename devopsagent
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
