package aws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/providers/aws/awsregions"
	"github.com/icearp/disco-cli/store"
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

// A source declared only through contains edges has no outbound resolver, so
// it must stay on `coverage resolvers --missing`; one with any other edge
// kind must leave it.
func TestResolverEdgeSources_SkipsContainsOnly(t *testing.T) {
	kinds := map[string][]string{}
	for _, e := range CollectResolverEdges() {
		kinds[e.Source] = append(kinds[e.Source], e.Kind)
	}
	got := coverageProvider{}.ResolverEdgeSources()
	var containsOnly, outbound int
	for src, ks := range kinds {
		onlyContains := !slices.ContainsFunc(ks, func(k string) bool { return k != store.RelContains })
		listed := slices.Contains(got, src)
		if onlyContains {
			containsOnly++
		} else {
			outbound++
		}
		if listed == onlyContains {
			t.Errorf("%s (kinds %v): in ResolverEdgeSources = %v, want %v", src, ks, listed, !onlyContains)
		}
	}
	if containsOnly == 0 || outbound == 0 {
		t.Fatalf("registry has %d contains-only and %d outbound sources; need both for this test to prove anything", containsOnly, outbound)
	}
}

// isolateCoverageProfiles points the SDK at a config file holding profiles
// "gov" (region us-gov-west-1) and "noregion", with static keys for both, so
// the coverage fetches see neither the developer's AWS setup nor a region from
// the environment.
func isolateCoverageProfiles(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("config", "[profile gov]\nregion = us-gov-west-1\n[profile noregion]\n")
	write("credentials", "[gov]\naws_access_key_id = AKIDTEST\naws_secret_access_key = secret\n"+
		"[noregion]\naws_access_key_id = AKIDTEST\naws_secret_access_key = secret\n")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	// Environment settings outrank the files above.
	for _, env := range []string{
		"AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_EC2", "AWS_ENDPOINT_URL_CLOUDFORMATION", "AWS_IGNORE_CONFIGURED_ENDPOINT_URLS",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
	} {
		t.Setenv(env, "")
	}
}

// A China or GovCloud profile's credentials fail in us-east-1, so the coverage
// fetches call in the profile's own region and fall back to the commercial
// home only when nothing names one.
func TestLoadCoverageConfig_Region(t *testing.T) {
	isolateCoverageProfiles(t)
	tests := []struct {
		name, profile, region, want string
	}{
		{"profile region", "gov", "", "us-gov-west-1"},
		{"explicit region wins", "gov", "us-gov-east-1", "us-gov-east-1"},
		{"no region anywhere", "noregion", "", "us-east-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := loadCoverageConfig(context.Background(), tt.profile, tt.region)
			if err != nil {
				t.Fatalf("loadCoverageConfig: %v", err)
			}
			if cfg.Region != tt.want {
				t.Errorf("Region = %q, want %q", cfg.Region, tt.want)
			}
		})
	}
}

const govDescribeRegions = `<DescribeRegionsResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
<requestId>r</requestId><regionInfo>
<item><regionName>us-gov-west-1</regionName></item>
<item><regionName>us-gov-east-1</regionName></item>
</regionInfo></DescribeRegionsResponse>`

// FetchRegions signs for the profile's region and diffs the live list against
// that region's partition, not the commercial RegionNames.
func TestFetchRegions_ProfilePartition(t *testing.T) {
	isolateCoverageProfiles(t)
	var scope atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(govDescribeRegions))
	}))
	defer srv.Close()
	t.Setenv("AWS_ENDPOINT_URL", srv.URL)

	static, live, err := coverageProvider{}.FetchRegions(context.Background(), coverage.FetchOptions{Profile: "gov"})
	if err != nil {
		t.Fatalf("FetchRegions: %v", err)
	}
	if auth, _ := scope.Load().(string); !strings.Contains(auth, "/us-gov-west-1/ec2/aws4_request") {
		t.Errorf("Authorization = %q, want a signature scoped to us-gov-west-1", auth)
	}
	if want := awsregions.RegionsByPartition["aws-us-gov"]; !slices.Equal(static, want) {
		t.Errorf("static = %v, want the aws-us-gov regions %v", static, want)
	}
	if want := []string{"us-gov-west-1", "us-gov-east-1"}; !slices.Equal(live, want) {
		t.Errorf("live = %v, want %v", live, want)
	}
}

// A region the pinned SDK places in no partition has no static list to diff
// against, so FetchRegions fails before calling AWS.
func TestFetchRegions_UnknownPartition(t *testing.T) {
	isolateCoverageProfiles(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer srv.Close()
	t.Setenv("AWS_ENDPOINT_URL", srv.URL)
	t.Setenv("AWS_REGION", "zz-nowhere-1")

	_, _, err := coverageProvider{}.FetchRegions(context.Background(), coverage.FetchOptions{Profile: "noregion"})
	var unknown *unknownRegionError
	if !errors.As(err, &unknown) || unknown.Region != "zz-nowhere-1" {
		t.Fatalf("FetchRegions err = %v, want *unknownRegionError for zz-nowhere-1", err)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("made %d calls, want 0", n)
	}
}

// CrossCheck calls CloudFormation in the profile's region when --regions is
// empty. The stub refuses the call, so CrossCheck stops before the
// credential-free Service Reference fetch, which would leave the test.
func TestCrossCheck_ProfileRegion(t *testing.T) {
	isolateCoverageProfiles(t)
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	t.Setenv("AWS_ENDPOINT_URL", srv.URL)

	if _, err := (coverageProvider{}).CrossCheck(context.Background(), coverage.FetchOptions{Profile: "gov"}); err == nil {
		t.Fatal("CrossCheck: nil error from a refused ListTypes")
	}
	if got, _ := auth.Load().(string); !strings.Contains(got, "/us-gov-west-1/cloudformation/aws4_request") {
		t.Errorf("Authorization = %q, want a signature scoped to us-gov-west-1", got)
	}
}

// One profile's credentials reach one partition, so --regions spanning two is
// refused before any call.
func TestCrossCheck_RegionsSpanPartitions(t *testing.T) {
	isolateCoverageProfiles(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer srv.Close()
	t.Setenv("AWS_ENDPOINT_URL", srv.URL)

	_, err := coverageProvider{}.CrossCheck(context.Background(),
		coverage.FetchOptions{Profile: "gov", Regions: []string{"us-gov-west-1", "us-east-1"}})
	var conflict *partitionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("CrossCheck err = %v, want *partitionConflictError", err)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("made %d calls, want 0", n)
	}
}
