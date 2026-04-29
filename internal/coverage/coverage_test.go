package coverage

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

type fakeProvider struct {
	name     string
	upstream []UpstreamType
	emits    []TypeDecl
	aliases  map[string]string
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Fetch(_ context.Context, _ FetchOptions) ([]UpstreamType, error) {
	return f.upstream, nil
}
func (f *fakeProvider) Emits() []TypeDecl          { return f.emits }
func (f *fakeProvider) Aliases() map[string]string { return f.aliases }

func TestBuild_BucketAssignment(t *testing.T) {
	emits := []TypeDecl{
		{Service: "ec2", DiscoType: "aws:ec2:instance"},
		{Service: "kms", DiscoType: "aws:kms:grant", Synthetic: true},
		{Service: "elasticloadbalancing", DiscoType: "aws:elasticloadbalancing:load-balancer"},
		{Service: "phantom", DiscoType: "aws:phantom:thing"},
	}
	aliases := map[string]string{
		"aws:elasticloadbalancing:load-balancer": "AWS::ElasticLoadBalancingV2::LoadBalancer",
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

	got := Build("aws", emits, aliases, algo, upstream)

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
		"aws:kms:grant":                          BucketSynthetic,
		"aws:phantom:thing":                      BucketUpstreamMissing,
		"AWS::S3::Bucket":                        BucketUncovered,
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
		{Service: "iam", DiscoType: "aws:iam:policy"},
		{Service: "iam", DiscoType: "aws:iam:policy"}, // duplicate (e.g. catalogue stub + GAAD scanner)
	}
	upstream := []UpstreamType{{Key: "AWS::IAM::ManagedPolicy", Service: "IAM"}}
	aliases := map[string]string{"aws:iam:policy": "AWS::IAM::ManagedPolicy"}

	got := Build("aws", emits, aliases, nil, upstream)
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

func TestRenderMarkdown_HasSections(t *testing.T) {
	m := Matrix{
		Provider: "aws",
		Rows: []Row{
			{Provider: "aws", Service: "ec2", DiscoType: "aws:ec2:instance", UpstreamKey: "AWS::EC2::Instance", Bucket: BucketCovered},
			{Provider: "aws", Service: "kms", DiscoType: "aws:kms:grant", Bucket: BucketSynthetic},
			{Provider: "aws", Service: "s3", UpstreamKey: "AWS::S3::Bucket", Bucket: BucketUncovered},
		},
	}
	var buf bytes.Buffer
	if err := RenderMarkdown(&buf, []Matrix{m}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"## AWS", "### Covered", "### Uncovered", "### Synthetic", "aws:ec2:instance", "AWS::S3::Bucket", "aws:kms:grant"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q\n%s", want, out)
		}
	}
}

func TestPascalToKebab(t *testing.T) {
	cases := map[string]string{
		"Instance":     "instance",
		"LoadBalancer": "load-balancer",
		"DBInstance":   "db-instance",
		"":             "",
	}
	for in, want := range cases {
		if got := PascalToKebab(in); got != want {
			t.Errorf("PascalToKebab(%q) = %q, want %q", in, got, want)
		}
	}
}
