package coverage

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuild_BucketAssignment(t *testing.T) {
	emits := []TypeDecl{
		{Service: "ec2", DiscoType: "aws:ec2:instance"},
		{Service: "elasticloadbalancing", DiscoType: "aws:elasticloadbalancing:load-balancer"},
		{Service: "phantom", DiscoType: "aws:phantom:thing"},
		// Uncatalogued + no upstream key -> uncatalogued bucket, NOT upstream-missing.
		{Service: "kms", DiscoType: "aws:kms:grant", Uncatalogued: true},
		// Uncatalogued but its key IS in upstream -> auto-upgrades to covered.
		{Service: "s3", DiscoType: "aws:s3:bucket", Uncatalogued: true},
	}
	aliases := map[string]string{
		"aws:elasticloadbalancing:load-balancer": "AWS::ElasticLoadBalancingV2::LoadBalancer",
		"aws:s3:bucket":                          "AWS::S3::Bucket",
	}
	upstream := []UpstreamType{
		{Key: "AWS::EC2::Instance", Service: "EC2"},
		{Key: "AWS::ElasticLoadBalancingV2::LoadBalancer", Service: "ElasticLoadBalancingV2"},
		{Key: "AWS::S3::Bucket", Service: "S3"},
	}
	// Test algorithmic fallback simulating CFN PascalCase: aws:ec2:instance -> AWS::EC2::Instance.
	algo := func(s string) string {
		// Simple test stub: convert "aws:ec2:instance" -> "AWS::EC2::Instance".
		parts := strings.Split(s, ":")
		if len(parts) != 3 {
			return s
		}
		k := parts[2]
		if k != "" {
			k = strings.ToUpper(k[:1]) + k[1:]
		}
		return strings.ToUpper(parts[0]) + "::" + strings.ToUpper(parts[1]) + "::" + k
	}

	got := Build("aws", emits, aliases, algo, upstream, nil, nil)

	buckets := map[string]Bucket{}
	for _, r := range got.Rows {
		key := r.DiscoType
		if key == "" {
			key = r.UpstreamKey
		}
		buckets[key] = r.Bucket
	}

	want := map[string]Bucket{
		"aws:ec2:instance":                       BucketCovered,
		"aws:elasticloadbalancing:load-balancer": BucketCovered,
		"aws:phantom:thing":                      BucketUpstreamMissing,
		"aws:kms:grant":                          BucketUncatalogued, // uncatalogued, no upstream key
		"aws:s3:bucket":                          BucketCovered,      // uncatalogued but registry lists it -> covered
	}
	if len(buckets) != len(want) {
		t.Fatalf("row count mismatch: got %d (%v), want %d", len(buckets), buckets, len(want))
	}
	for k, b := range want {
		if buckets[k] != b {
			t.Errorf("%s: got bucket %q, want %q", k, buckets[k], b)
		}
	}
}

func TestBuild_DedupesEmits(t *testing.T) {
	emits := []TypeDecl{
		{Service: "iam", DiscoType: "aws:iam:managed-policy"},
		{Service: "iam", DiscoType: "aws:iam:managed-policy"}, // duplicate (e.g. catalogue stub + GAAD scanner)
	}
	upstream := []UpstreamType{{Key: "AWS::IAM::ManagedPolicy", Service: "IAM"}}
	aliases := map[string]string{"aws:iam:managed-policy": "AWS::IAM::ManagedPolicy"}

	got := Build("aws", emits, aliases, nil, upstream, nil, nil)
	covered := 0
	for _, r := range got.Rows {
		if r.Bucket == BucketCovered {
			covered++
		}
	}
	if covered != 1 {
		t.Fatalf("expected 1 covered row after dedup, got %d (%+v)", covered, got.Rows)
	}
}

func TestBuild_NotScannableReclassifiesUncovered(t *testing.T) {
	// No emits: every upstream entry is leftover. The skip map should pull the
	// declared key into not-scannable (with its reason); the rest stay uncovered.
	upstream := []UpstreamType{
		{Key: "AWS::EC2::Route", Service: "EC2"},
		{Key: "AWS::EC2::Instance", Service: "EC2"},
	}
	skips := map[string]string{
		// Case-insensitive match: declare in CFN casing, upstream uses same.
		"AWS::EC2::Route": "sub-resource: route lives inside a route table, no List API",
	}

	got := Build("aws", nil, nil, nil, upstream, skips, nil)

	byKey := map[string]Row{}
	for _, r := range got.Rows {
		byKey[r.UpstreamKey] = r
	}
	route, ok := byKey["AWS::EC2::Route"]
	if !ok {
		t.Fatalf("missing AWS::EC2::Route row: %+v", got.Rows)
	}
	if route.Bucket != BucketNotScannable {
		t.Errorf("AWS::EC2::Route: got bucket %q, want %q", route.Bucket, BucketNotScannable)
	}
	if route.Reason != skips["AWS::EC2::Route"] {
		t.Errorf("AWS::EC2::Route: got reason %q, want %q", route.Reason, skips["AWS::EC2::Route"])
	}
	// The undeclared key must remain a genuine uncovered gap, not be swallowed.
	if inst := byKey["AWS::EC2::Instance"]; inst.Bucket != BucketUncovered {
		t.Errorf("AWS::EC2::Instance: got bucket %q, want %q", inst.Bucket, BucketUncovered)
	}
}

func TestRenderMarkdown_HasSections(t *testing.T) {
	m := Matrix{
		Provider: "aws",
		Rows: []Row{
			{Provider: "aws", Service: "ec2", DiscoType: "aws:ec2:instance", UpstreamKey: "AWS::EC2::Instance", Bucket: BucketCovered},
			{Provider: "aws", Service: "kms", DiscoType: "aws:kms:grant", Bucket: BucketUncatalogued},
			{Provider: "aws", Service: "s3", UpstreamKey: "AWS::S3::Bucket", Bucket: BucketUncovered},
		},
	}
	var buf bytes.Buffer
	if err := RenderMarkdown(&buf, []Matrix{m}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"## AWS", "### Covered", "### Uncovered", "### Uncatalogued", "aws:ec2:instance", "AWS::S3::Bucket", "aws:kms:grant"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q\n%s", want, out)
		}
	}
}

// TestCanonicalDuplicateMatching: when a provider supplies a CanonicalKey, an
// unmatched upstream key whose canonical identity equals an already-covered
// key's identity is reclassified as a covered cross-catalog duplicate (carrying
// a "duplicate of …" Reason) rather than left as an actionable uncovered gap.
func TestCanonicalDuplicateMatching(t *testing.T) {
	emits := []TypeDecl{{Service: "amplify", DiscoType: "aws:amplify:app"}}
	aliases := map[string]string{"aws:amplify:app": "AWS::Amplify::App"}
	upstream := []UpstreamType{
		{Key: "AWS::Amplify::App", Service: "Amplify"},  // CFN spelling — disco covers it
		{Key: "AWS::amplify::apps", Service: "amplify"}, // SR twin — must collapse, not be a gap
		{Key: "AWS::amplify::jobs", Service: "amplify"}, // genuinely uncovered, no covered twin
	}
	// Minimal canonical: lowercase, strip "::"-segment hyphens, singularize res.
	canon := func(k string) string {
		p := strings.SplitN(k, "::", 3)
		if len(p) != 3 {
			return strings.ToLower(k)
		}
		res := strings.ToLower(p[2])
		res = strings.TrimSuffix(res, "s")
		return strings.ToLower(p[1]) + "::" + res
	}

	got := Build("aws", emits, aliases, nil, upstream, nil, canon)

	var dupRow *Row
	for i := range got.Rows {
		r := &got.Rows[i]
		if r.UpstreamKey == "AWS::amplify::apps" {
			dupRow = r
		}
		if r.Bucket == BucketUncovered && r.UpstreamKey == "AWS::amplify::apps" {
			t.Errorf("SR twin AWS::amplify::apps left uncovered; want covered duplicate")
		}
	}
	if dupRow == nil {
		t.Fatal("no row for AWS::amplify::apps")
	}
	if dupRow.Bucket != BucketCovered {
		t.Errorf("AWS::amplify::apps bucket = %q; want covered", dupRow.Bucket)
	}
	if !strings.HasPrefix(dupRow.Reason, "duplicate of ") {
		t.Errorf("AWS::amplify::apps Reason = %q; want \"duplicate of …\"", dupRow.Reason)
	}
	// The genuine gap must remain uncovered (no false collapse).
	var jobsBucket Bucket
	for _, r := range got.Rows {
		if r.UpstreamKey == "AWS::amplify::jobs" {
			jobsBucket = r.Bucket
		}
	}
	if jobsBucket != BucketUncovered {
		t.Errorf("AWS::amplify::jobs bucket = %q; want uncovered", jobsBucket)
	}
}

// A skip declared under one catalog's spelling (SR hyphenated) must also mark
// the cross-catalog twin (CFN PascalCase) not-scannable via canonical matching,
// so each divergent service needs only one skip entry.
func TestSkipMatchesCanonicalTwin(t *testing.T) {
	skips := map[string]string{
		"AWS::aws-external-anthropic::workspace": "no SDK: external-partner prefix",
	}
	upstream := []UpstreamType{
		{Key: "AWS::AWSExternalAnthropic::Workspace", Service: "AWSExternalAnthropic"}, // CFN twin
	}
	canon := func(k string) string {
		p := strings.SplitN(k, "::", 3)
		if len(p) != 3 {
			return strings.ToLower(k)
		}
		strip := func(s string) string { return strings.ReplaceAll(strings.ToLower(s), "-", "") }
		return strip(p[1]) + "::" + strip(p[2])
	}

	got := Build("aws", nil, nil, nil, upstream, skips, canon)

	var row *Row
	for i := range got.Rows {
		if got.Rows[i].UpstreamKey == "AWS::AWSExternalAnthropic::Workspace" {
			row = &got.Rows[i]
		}
	}
	if row == nil {
		t.Fatal("no row for the CFN twin")
	}
	if row.Bucket != BucketNotScannable {
		t.Errorf("CFN twin bucket = %q; want not-scannable", row.Bucket)
	}
}
