package aws

import (
	"context"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	apigatewaytypes "github.com/aws/aws-sdk-go-v2/service/apigateway/types"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudhsmv2"
	cloudhsmv2types "github.com/aws/aws-sdk-go-v2/service/cloudhsmv2/types"
	dstypes "github.com/aws/aws-sdk-go-v2/service/directoryservice/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing/types"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	istypes "github.com/aws/aws-sdk-go-v2/service/identitystore/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/macie2"
	macie2types "github.com/aws/aws-sdk-go-v2/service/macie2/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"

	"github.com/icearp/disco-cli/store"
)

// TestPartitionARNBuilders_CommercialUnchanged pins that every partition-taking
// core, given "aws", produces exactly the string the builder produced before
// partitions existed — those strings are NativeIDs, and a changed byte gives
// every existing commercial row a new ResourceID. The aws-cn column, in cn-north-1,
// proves the partition argument reaches the output instead of being ignored.
func TestPartitionARNBuilders_CommercialUnchanged(t *testing.T) {
	const acct = "123456789012"
	cases := []struct {
		name       string
		build      func(partition, region string) string
		commercial string // the pre-partition literal form
		china      string
	}{
		{
			"ec2", func(p, region string) string { return ec2ARNIn(p, region, acct, "instance", "i-1") },
			"arn:aws:ec2:us-east-1:123456789012:instance/i-1",
			"arn:aws-cn:ec2:cn-north-1:123456789012:instance/i-1",
		},
		{
			"rds", func(p, region string) string { return rdsARNIn(p, region, acct, "cluster", "c1") },
			"arn:aws:rds:us-east-1:123456789012:cluster:c1",
			"arn:aws-cn:rds:cn-north-1:123456789012:cluster:c1",
		},
		{
			"apigateway", func(p, region string) string { return apigatewayARNIn(p, region, "restapis", "a1") },
			"arn:aws:apigateway:us-east-1::/restapis/a1",
			"arn:aws-cn:apigateway:cn-north-1::/restapis/a1",
		},
		{
			"apigateway empty path", func(p, region string) string { return apigatewayARNIn(p, region) },
			"arn:aws:apigateway:us-east-1::/",
			"arn:aws-cn:apigateway:cn-north-1::/",
		},
		{
			"cloudhsm", func(p, region string) string { return cloudhsmClusterARNIn(p, region, acct, "cluster-1") },
			"arn:aws:cloudhsm:us-east-1:123456789012:cluster/cluster-1",
			"arn:aws-cn:cloudhsm:cn-north-1:123456789012:cluster/cluster-1",
		},
		{
			"log group", func(p, region string) string { return logGroupARNIn(p, acct, region, "/aws/lambda/f") },
			"arn:aws:logs:us-east-1:123456789012:log-group:/aws/lambda/f",
			"arn:aws-cn:logs:cn-north-1:123456789012:log-group:/aws/lambda/f",
		},
		{
			"macie session", func(p, region string) string { return macieSessionNativeIDIn(p, acct, region) },
			"arn:aws:macie2:us-east-1:123456789012:session",
			"arn:aws-cn:macie2:cn-north-1:123456789012:session",
		},
		{
			"identity store user", func(p, region string) string { return identityStoreUserNativeIDIn(p, acct, "d-1", "u-1") },
			"arn:aws:identitystore::123456789012:user/d-1/u-1",
			"arn:aws-cn:identitystore::123456789012:user/d-1/u-1",
		},
		{
			"identity store group", func(p, region string) string { return identityStoreGroupNativeIDIn(p, acct, "d-1", "g-1") },
			"arn:aws:identitystore::123456789012:group/d-1/g-1",
			"arn:aws-cn:identitystore::123456789012:group/d-1/g-1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.build("aws", "us-east-1"); got != c.commercial {
				t.Errorf("core(aws) = %q; want %q", got, c.commercial)
			}
			if got := c.build("aws-cn", "cn-north-1"); got != c.china {
				t.Errorf("core(aws-cn) = %q; want %q", got, c.china)
			}
		})
	}
}

func TestArnFor(t *testing.T) {
	if got, want := arnFor("aws-us-gov", "s3", "", "", "bucket"), "arn:aws-us-gov:s3:::bucket"; got != want {
		t.Errorf("arnFor(aws-us-gov, s3, \"\", \"\", bucket) = %q; want %q", got, want)
	}
}

func TestPartitionOf(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"arn:aws:iam::123456789012:role/R", "aws", true},
		{"arn:aws-cn:iam::123456789012:role/R", "aws-cn", true},
		{"arn:aws-us-gov:sts::123456789012:assumed-role/R/s", "aws-us-gov", true},
		{"arn:aws-eusc:iam::123456789012:role/R", "aws-eusc", true},
		{"arn:aws-iso-b:iam::123456789012:role/R", "aws-iso-b", true},
		// Parses as an ARN, but no partition the SDK knows: must not read as commercial.
		{"arn:aws-mars:iam::123456789012:role/R", "", false},
		{"not-an-arn", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := partitionOf(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("partitionOf(%q) = (%q, %v); want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestDNSSuffix(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"aws", "amazonaws.com", true},
		{"aws-cn", "amazonaws.com.cn", true},
		{"aws-us-gov", "amazonaws.com", true},
		{"aws-eusc", "amazonaws.eu", true},
		{"aws-iso", "c2s.ic.gov", true},
		{"aws-mars", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := dnsSuffix(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("dnsSuffix(%q) = (%q, %v); want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestIsARNOf(t *testing.T) {
	cases := []struct {
		in, service string
		want        bool
	}{
		{"arn:aws:sns:us-east-1:123456789012:t", "sns", true},
		{"arn:aws-cn:sns:cn-north-1:123456789012:t", "sns", true},
		{"arn:aws-us-gov:sns:us-gov-west-1:123456789012:t", "sns", true},
		{"arn:aws-eusc:sns:eusc-de-east-1:123456789012:t", "sns", true},
		{"arn:aws-iso-b:sns:us-isob-east-1:123456789012:t", "sns", true},
		{"arn:aws-cn:sqs:cn-north-1:123456789012:t", "sns", false},
		// Parses, but names a partition the SDK does not know.
		{"arn:aws-mars:sns:mars-1:123456789012:t", "sns", false},
		{"arn:aws:sns", "sns", false},
		{"sns", "sns", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := isARNOf(c.in, c.service); got != c.want {
			t.Errorf("isARNOf(%q, %q) = %v; want %v", c.in, c.service, got, c.want)
		}
	}
}

func TestParseARN(t *testing.T) {
	cases := []struct {
		in     string
		want   arn.ARN
		wantOK bool
	}{
		{"arn:aws-cn:iam::aws:policy/ReadOnlyAccess", arn.ARN{Partition: "aws-cn", Service: "iam", AccountID: "aws", Resource: "policy/ReadOnlyAccess"}, true},
		{"arn:aws-us-gov:sqs:us-gov-west-1:123456789012:q", arn.ARN{Partition: "aws-us-gov", Service: "sqs", Region: "us-gov-west-1", AccountID: "123456789012", Resource: "q"}, true},
		{"arn:aws-mars:iam::aws:policy/P", arn.ARN{}, false},
		{"arn:aws:iam", arn.ARN{}, false},
		{"", arn.ARN{}, false},
	}
	for _, c := range cases {
		got, ok := parseARN(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("parseARN(%q) = (%+v, %v); want (%+v, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestS3BucketARNFromLocation(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:s3:::bucket", "arn:aws:s3:::bucket"},
		{"arn:aws:s3:::bucket/prefix/", "arn:aws:s3:::bucket"},
		{"arn:aws:s3:::bucket/a/b/c", "arn:aws:s3:::bucket"},
		{"arn:aws:lambda:::function/foo", ""},
		{"", ""},
		{"arn:aws:s3:::", ""},
		{"arn:aws-cn:s3:::bucket/prefix/", "arn:aws-cn:s3:::bucket"},
		{"arn:aws-us-gov:s3:::bucket", "arn:aws-us-gov:s3:::bucket"},
		// An access point carries a region and an account: not a bucket.
		{"arn:aws-cn:s3:cn-north-1:123456789012:accesspoint/ap", ""},
		{"arn:aws-mars:s3:::bucket", ""},
		{"s3://bucket/key", ""},
	}
	for _, c := range cases {
		if got := s3BucketARNFromLocation(c.in); got != c.want {
			t.Errorf("s3BucketARNFromLocation(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestHostRegion(t *testing.T) {
	cases := []struct {
		partition, host, prefix string
		want                    string
		wantOK                  bool
	}{
		{"aws", "sqs.us-east-1.amazonaws.com", "sqs.", "us-east-1", true},
		{"aws-cn", "sqs.cn-north-1.amazonaws.com.cn", "sqs.", "cn-north-1", true},
		{"aws-us-gov", "sqs.us-gov-west-1.amazonaws.com", "sqs.", "us-gov-west-1", true},
		// Suffix of another partition.
		{"aws-cn", "sqs.us-east-1.amazonaws.com", "sqs.", "", false},
		{"aws", "sqs.cn-north-1.amazonaws.com.cn", "sqs.", "", false},
		// Same suffix, region of another partition.
		{"aws-us-gov", "sqs.us-east-1.amazonaws.com", "sqs.", "", false},
		{"aws", "sqs.us-gov-west-1.amazonaws.com", "sqs.", "", false},
		{"aws", "sns.us-east-1.amazonaws.com", "sqs.", "", false},
		{"aws", "sqs.amazonaws.com", "sqs.", "", false},
		{"aws", "sqs.fips.us-east-1.amazonaws.com", "sqs.", "", false},
		// FIPS form of the prefix.
		{"aws-us-gov", "sqs-fips.us-gov-west-1.amazonaws.com", "sqs.", "us-gov-west-1", true},
		{"aws-us-gov", "dkr.ecr-fips.us-gov-east-1.amazonaws.com", "dkr.ecr.", "us-gov-east-1", true},
		{"aws-us-gov", "sqs-fips.us-east-1.amazonaws.com", "sqs.", "", false},
		{"aws", "sns-fips.us-east-1.amazonaws.com", "sqs.", "", false},
		{"aws-mars", "sqs.us-east-1.amazonaws.com", "sqs.", "", false},
	}
	for _, c := range cases {
		got, ok := hostRegion(c.partition, c.host, c.prefix)
		if got != c.want || ok != c.wantOK {
			t.Errorf("hostRegion(%q, %q, %q) = (%q, %v); want (%q, %v)", c.partition, c.host, c.prefix, got, ok, c.want, c.wantOK)
		}
	}
}

// TestS3HostBucket walks each rule of the S3 host grammar: normalisation, the
// endpoint label, the per-label region check, the SDK's region-less hosts and
// the aws-only accelerate fact.
func TestS3HostBucket(t *testing.T) {
	cases := []struct {
		name, partition, host, bucket string
		ok                            bool
	}{
		{"regional", "aws", "b.s3.us-east-1.amazonaws.com", "b", true},
		{"path-style", "aws", "s3.us-east-1.amazonaws.com", "", true},
		{"legacy dash region", "aws", "b.s3-us-west-2.amazonaws.com", "b", true},
		{"website dash", "aws", "b.s3-website-us-east-1.amazonaws.com", "b", true},
		{"website dot", "aws", "b.s3-website.us-east-1.amazonaws.com", "b", true},
		{"dualstack", "aws", "b.s3.dualstack.us-east-1.amazonaws.com", "b", true},
		{"fips dualstack", "aws-us-gov", "b.s3-fips.dualstack.us-gov-west-1.amazonaws.com", "b", true},
		{"vpce", "aws", "b.vpce-1a2b.s3.us-east-1.vpce.amazonaws.com", "b", true},
		{"vpce dotted bucket", "aws", "x.y.vpce-1a2b.s3.us-east-1.vpce.amazonaws.com", "x.y", true},
		{"vpce path-style", "aws", "vpce-1a2b.s3.us-east-1.vpce.amazonaws.com", "", true},
		{"vpce access point", "aws", "accesspoint.vpce-1a2b.s3.us-east-1.vpce.amazonaws.com", "", false},
		{"vpce control", "aws", "control.vpce-1a2b.s3.us-east-1.vpce.amazonaws.com", "", false},
		{"vpce without endpoint id", "aws", "b.s3.us-east-1.vpce.amazonaws.com", "", false},
		{"vpce without region", "aws", "b.vpce-1a2b.s3.vpce.amazonaws.com", "", false},
		{"vpce dualstack", "aws", "b.vpce-1a2b.s3.dualstack.us-east-1.vpce.amazonaws.com", "", false},
		{"cn regional", "aws-cn", "b.s3.cn-north-1.amazonaws.com.cn", "b", true},
		{"gov regional", "aws-us-gov", "b.s3.us-gov-west-1.amazonaws.com", "b", true},
		{"dotted bucket with s3 label", "aws", "x.s3data.s3.amazonaws.com", "x.s3data", true},
		{"dotted bucket with s3- label", "aws", "a.s3-logs.s3.us-east-1.amazonaws.com", "a.s3-logs", true},

		{"uppercase", "aws", "B.S3.AMAZONAWS.COM", "b", true},
		{"trailing dot", "aws", "b.s3.amazonaws.com.", "b", true},
		{"port", "aws", "b.s3.us-east-1.amazonaws.com:443", "b", true},
		{"trailing dot and port", "aws", "s3.amazonaws.com.:443", "", true},

		{"sdk region-less global", "aws", "b.s3.amazonaws.com", "b", true},
		{"sdk region-less external-1", "aws", "b.s3-external-1.amazonaws.com", "b", true},
		{"gov has no region-less global", "aws-us-gov", "b.s3.amazonaws.com", "", false},
		{"gov has no external-1", "aws-us-gov", "b.s3-external-1.amazonaws.com", "", false},
		{"cn has no region-less global", "aws-cn", "b.s3.amazonaws.com.cn", "", false},
		{"website without region", "aws", "b.s3-website.amazonaws.com", "", false},

		{"accelerate aws", "aws", "b.s3-accelerate.amazonaws.com", "b", true},
		{"accelerate dualstack aws", "aws", "b.s3-accelerate.dualstack.amazonaws.com", "b", true},
		{"accelerate gov", "aws-us-gov", "b.s3-accelerate.amazonaws.com", "", false},
		{"accelerate cn", "aws-cn", "b.s3-accelerate.amazonaws.com.cn", "", false},
		{"accelerate dualstack region aws", "aws", "b.s3-accelerate.dualstack.us-east-1.amazonaws.com", "b", true},
		{"accelerate region without dualstack", "aws", "b.s3-accelerate.us-east-1.amazonaws.com", "", false},
		{"accelerate dualstack region gov", "aws-us-gov", "b.s3-accelerate.dualstack.us-gov-west-1.amazonaws.com", "", false},
		{"accelerate foreign region aws", "aws", "b.s3-accelerate.dualstack.us-gov-west-1.amazonaws.com", "", false},
		{"accelerate vpce", "aws", "b.vpce-1a2b.s3-accelerate.us-east-1.vpce.amazonaws.com", "", false},

		{"region of another partition", "aws", "s3.us-gov-west-1.amazonaws.com", "", false},
		{"commercial region under gov", "aws-us-gov", "b.s3.us-east-1.amazonaws.com", "", false},
		{"commercial dash region under gov", "aws-us-gov", "b.s3-website-us-east-1.amazonaws.com", "", false},
		{"commercial vpce under gov", "aws-us-gov", "b.vpce-1a2b.s3.us-east-1.vpce.amazonaws.com", "", false},
		{"region before vpce under aws", "aws", "b.vpce-1a2b.s3.us-gov-west-1.vpce.amazonaws.com", "", false},
		{"cn suffix, commercial region", "aws-cn", "b.s3.us-east-1.amazonaws.com.cn", "", false},

		{"two regions", "aws", "b.s3.us-east-1.us-west-2.amazonaws.com", "", false},
		{"repeated vpce", "aws", "b.vpce-1a2b.s3.us-east-1.vpce.vpce.amazonaws.com", "", false},
		{"repeated dualstack", "aws", "b.s3.dualstack.dualstack.us-east-1.amazonaws.com", "", false},
		{"dualstack after region", "aws", "b.s3.us-east-1.dualstack.amazonaws.com", "", false},
		{"dash region then dualstack", "aws", "b.s3-us-east-1.dualstack.amazonaws.com", "", false},
		{"dash region then region", "aws", "b.s3-us-east-1.us-east-1.amazonaws.com", "", false},
		{"website dualstack", "aws", "b.s3-website.dualstack.us-east-1.amazonaws.com", "", false},
		{"fips vpce", "aws", "b.vpce-1a2b.s3-fips.us-east-1.vpce.amazonaws.com", "b", true},
		{"gov fips vpce", "aws-us-gov", "b.vpce-1a2b.s3-fips.us-gov-west-1.vpce.amazonaws.com", "b", true},
		{"fips vpce dualstack", "aws", "b.vpce-1a2b.s3-fips.dualstack.us-east-1.vpce.amazonaws.com", "", false},
		{"website vpce", "aws", "b.vpce-1a2b.s3-website.us-east-1.vpce.amazonaws.com", "", false},
		{"dash region vpce", "aws", "b.vpce-1a2b.s3-us-east-1.vpce.amazonaws.com", "", false},
		{"empty leading label", "aws", ".s3.amazonaws.com", "", false},
		{"empty inner label", "aws", "a..s3.amazonaws.com", "", false},
		{"empty endpoint label", "aws", "b.s3..us-east-1.amazonaws.com", "", false},
		{"fips-region label", "aws", "b.s3.fips-us-gov-west-1.amazonaws.com", "", false},
		{"fips-region label beside a region", "aws", "b.s3-us-east-1.fips-us-gov-west-1.amazonaws.com", "", false},
		{"unknown label beside a region", "aws", "b.s3.foo.us-east-1.amazonaws.com", "", false},
		{"suffix of another partition", "aws-cn", "b.s3.us-east-1.amazonaws.com", "", false},
		{"access point", "aws", "ap-123.s3-accesspoint.us-east-1.amazonaws.com", "", false},
		{"not s3", "aws", "example.com", "", false},
		{"no endpoint label", "aws", "b.amazonaws.com", "", false},
		{"unknown partition", "aws-mars", "s3.amazonaws.com", "", false},
	}
	for _, c := range cases {
		bucket, ok := s3HostBucket(c.partition, c.host)
		if bucket != c.bucket || ok != c.ok {
			t.Errorf("%s: s3HostBucket(%q, %q) = %q, %v; want %q, %v", c.name, c.partition, c.host, bucket, ok, c.bucket, c.ok)
		}
	}
}

func TestECRImageRepoARN(t *testing.T) {
	cases := []struct{ partition, in, want string }{
		{"aws", "123456789012.dkr.ecr.us-east-1.amazonaws.com/myapp:latest", "arn:aws:ecr:us-east-1:123456789012:repository/myapp"},
		{"aws", "123456789012.dkr.ecr.us-east-1.amazonaws.com/team/myapp:v1", "arn:aws:ecr:us-east-1:123456789012:repository/team/myapp"},
		{"aws", "123456789012.dkr.ecr.us-east-1.amazonaws.com/team/myapp@sha256:abc", "arn:aws:ecr:us-east-1:123456789012:repository/team/myapp"},
		{"aws", "123456789012.dkr.ecr.us-east-1.amazonaws.com/myapp", "arn:aws:ecr:us-east-1:123456789012:repository/myapp"},
		{"aws-cn", "123456789012.dkr.ecr.cn-north-1.amazonaws.com.cn/myapp:latest", "arn:aws-cn:ecr:cn-north-1:123456789012:repository/myapp"},
		{"aws-us-gov", "123456789012.dkr.ecr.us-gov-west-1.amazonaws.com/myapp:latest", "arn:aws-us-gov:ecr:us-gov-west-1:123456789012:repository/myapp"},
		{"aws-us-gov", "123456789012.dkr.ecr-fips.us-gov-west-1.amazonaws.com/myapp:latest", "arn:aws-us-gov:ecr:us-gov-west-1:123456789012:repository/myapp"},
		{"aws-cn", "123456789012.dkr.ecr.us-east-1.amazonaws.com/myapp:latest", ""},
		{"aws-us-gov", "123456789012.dkr.ecr.us-east-1.amazonaws.com/myapp:latest", ""},
		{"aws", "public.ecr.aws/lambda/python:latest", ""},
		{"aws", "docker.io/library/nginx:latest", ""},
		{"aws", "123456789012.dkr.ecr.us-east-1.amazonaws.com/", ""},
		{"aws", "", ""},
	}
	for _, c := range cases {
		if got := ecrImageRepoARN(c.partition, c.in); got != c.want {
			t.Errorf("ecrImageRepoARN(%q, %q) = %q; want %q", c.partition, c.in, got, c.want)
		}
	}
}

// TestEC2ARN verifies EC2's NativeID ARN format. NativeID feeds
// store.ResourceID, so a format change silently breaks all relationships
// referencing EC2 resources.
func TestEC2ARNIn_Commercial(t *testing.T) {
	cases := []struct {
		region, account, rtype, id, want string
	}{
		{
			"us-east-1", "123456789012", "instance", "i-abc123",
			"arn:aws:ec2:us-east-1:123456789012:instance/i-abc123",
		},
		{
			"eu-west-1", "999999999999", "vpc", "vpc-xyz",
			"arn:aws:ec2:eu-west-1:999999999999:vpc/vpc-xyz",
		},
		{
			"us-west-2", "111111111111", "security-group", "sg-001",
			"arn:aws:ec2:us-west-2:111111111111:security-group/sg-001",
		},
	}
	for _, tc := range cases {
		got := ec2ARNIn("aws", tc.region, tc.account, tc.rtype, tc.id)
		if got != tc.want {
			t.Errorf("ec2ARNIn(aws,%q,%q,%q,%q) = %q, want %q",
				tc.region, tc.account, tc.rtype, tc.id, got, tc.want)
		}
	}
}

func TestRDSARNIn_Commercial(t *testing.T) {
	got := rdsARNIn("aws", "us-east-1", "123456789012", "db", "mydb")
	want := "arn:aws:rds:us-east-1:123456789012:db:mydb"
	if got != want {
		t.Errorf("rdsARNIn(aws, …) = %q, want %q", got, want)
	}
}

func TestAPIGatewayARNIn_Commercial(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"single segment", []string{"restapis"}, "arn:aws:apigateway:us-east-1::/restapis"},
		{"multi segment", []string{"restapis", "abc", "stages", "prod"}, "arn:aws:apigateway:us-east-1::/restapis/abc/stages/prod"},
		{"empty path", nil, "arn:aws:apigateway:us-east-1::/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := apigatewayARNIn("aws", "us-east-1", tt.in...); got != tt.want {
				t.Errorf("apigatewayARNIn(aws, us-east-1, %v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLogGroupARNIn_Commercial(t *testing.T) {
	got := logGroupARNIn("aws", "123456789012", "us-east-1", "/aws/lambda/foo")
	want := "arn:aws:logs:us-east-1:123456789012:log-group:/aws/lambda/foo"
	if got != want {
		t.Errorf("logGroupARNIn(aws, …) = %q, want %q", got, want)
	}
}

func TestMacieSessionNativeIDIn_Commercial(t *testing.T) {
	got := macieSessionNativeIDIn("aws", "123456789012", "us-east-1")
	want := "arn:aws:macie2:us-east-1:123456789012:session"
	if got != want {
		t.Errorf("macieSessionNativeIDIn(aws, …) = %q, want %q", got, want)
	}
}

func TestSSOAssignmentNativeID(t *testing.T) {
	psArn := "arn:aws:sso:::permissionSet/ssoins-1/ps-2"
	got := ssoAssignmentNativeID(psArn, "111122223333", "USER", "u-1")
	want := psArn + "/account/111122223333/USER/u-1"
	if got != want {
		t.Errorf("ssoAssignmentNativeID = %q, want %q", got, want)
	}
}

func TestIdentityStoreNativeIDIn_Commercial(t *testing.T) {
	if got, want := identityStoreUserNativeIDIn("aws", "123456789012", "d-9067", "u-1"),
		"arn:aws:identitystore::123456789012:user/d-9067/u-1"; got != want {
		t.Errorf("identityStoreUserNativeIDIn(aws, …) = %q, want %q", got, want)
	}
	if got, want := identityStoreGroupNativeIDIn("aws", "123456789012", "d-9067", "g-1"),
		"arn:aws:identitystore::123456789012:group/d-9067/g-1"; got != want {
		t.Errorf("identityStoreGroupNativeIDIn(aws, …) = %q, want %q", got, want)
	}
}

// partitionFixtures are the non-commercial partitions each builder family is
// proven against through a real scanner or resolver, each with its own region.
var partitionFixtures = []struct{ partition, region string }{
	{"aws-cn", "cn-north-1"},
	{"aws-us-gov", "us-gov-west-1"},
}

// partitionARN builds arn:{partition}:{service}:{region}:{testAccountID}:{resource}
// for test expectations, independently of arnFor.
func partitionARN(partition, service, region, resource string) string {
	return "arn:" + partition + ":" + service + ":" + region + ":" + testAccountID + ":" + resource
}

// assertEdgeTargets fails unless the edges out of from go to exactly want, so
// an edge to the commercial form of a target fails as loudly as a missing one.
func assertEdgeTargets(t *testing.T, st *store.Store, from string, want ...string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(from)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", from, err)
	}
	got := map[string]bool{}
	for _, r := range rels {
		got[r.ToID] = true
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
		if !got[w] {
			t.Errorf("no edge %s → %s", from, w)
		}
	}
	for to := range got {
		if !wantSet[to] {
			t.Errorf("unexpected edge %s → %s", from, to)
		}
	}
}

// errUnstubbedOpDenied answers the ops a partition test does not stub. It is an
// access denial so the scanner skips that phase with a warning, as it would
// for a role lacking the action, instead of failing the scan.
var errUnstubbedOpDenied = apiErr("AccessDeniedException", "operation not stubbed by this test")

// partitionAccount is an account as loadAccounts leaves it for partition p.
// Its regions are the partition's fixture region, so a test cannot pair a
// non-aws partition with a commercial region.
func partitionAccount(partition string) *account {
	acct := newTestAccount(testAccountID)
	acct.Partition = partition
	for _, pf := range partitionFixtures {
		if pf.partition == partition {
			acct.Regions = []string{pf.region}
			acct.scanRegions = []string{pf.region}
		}
	}
	return acct
}

// onlyRow returns the single row of typ, failing unless there is exactly one.
func onlyRow(t *testing.T, st *store.Store, typ string) store.Resource {
	t.Helper()
	rows := rowsByNativeID(t, st, typ)
	if len(rows) != 1 {
		t.Fatalf("%s rows = %v; want exactly one", typ, rows)
	}
	for _, r := range rows {
		return r
	}
	return store.Resource{}
}

func TestEC2ARNIn_ScannerUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			client := ec2.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"DescribeVpcs": {{Output: &ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{{VpcId: ptrStr("vpc-1")}}}}},
			}), pf.region))
			if _, _, err := scanVPCs(context.Background(), client, partitionAccount(pf.partition), pf.region, st, testScanID); err != nil {
				t.Fatalf("scanVPCs: %v", err)
			}
			if got, want := onlyRow(t, st, TypeEC2VPC).NativeID, partitionARN(pf.partition, "ec2", pf.region, "vpc/vpc-1"); got != want {
				t.Errorf("vpc NativeID = %s; want %s", got, want)
			}
		})
	}
}

func TestEC2ARNIn_ResolverUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			ngw := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NatGateway,
				ec2ARNIn(acct.partition(), pf.region, acct.ID, "natgateway", "nat-1"), pf.region,
				mustJSON(ec2types.NatGateway{SubnetId: ptrStr("subnet-1"), VpcId: ptrStr("vpc-1")}))
			if err := resolveNatGatewayRelationships(acct, st); err != nil {
				t.Fatalf("resolveNatGatewayRelationships: %v", err)
			}
			assertEdgeTargets(t, st, ngw,
				store.ResourceID("aws", acct.ID, partitionARN(pf.partition, "ec2", pf.region, "subnet/subnet-1")),
				store.ResourceID("aws", acct.ID, partitionARN(pf.partition, "ec2", pf.region, "vpc/vpc-1")))
		})
	}
}

func TestEC2ARNIn_RouteTargetUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			igw := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InternetGateway,
				ec2ARNIn(acct.partition(), pf.region, acct.ID, "internet-gateway", "igw-1"), pf.region, "{}")
			rt := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteTable,
				ec2ARNIn(acct.partition(), pf.region, acct.ID, "route-table", "rtb-1"), pf.region,
				mustJSON(ec2types.RouteTable{Routes: []ec2types.Route{{GatewayId: ptrStr("igw-1")}}}))
			if err := resolveRouteTableRoutes(acct, st); err != nil {
				t.Fatalf("resolveRouteTableRoutes: %v", err)
			}
			assertEdgeTargets(t, st, rt, igw)
		})
	}
}

// Every cfnTypeMap binding that takes a partition must reach the row the
// target's own scanner stores. The stack's NativeID is the StackId
// CloudFormation returns; each target NativeID is built the way its scanner
// builds it (the SDK ARN for RDS and Logs).
func TestCloudFormationStackResources_PartitionBindings(t *testing.T) {
	type target struct{ typ, nativeID string }
	cases := []struct {
		cfnType, physID string
		target          func(p, region string) target
	}{
		{"AWS::EC2::Instance", "i-1", func(p, r string) target {
			return target{TypeEC2Instance, ec2ARNIn(p, r, testAccountID, "instance", "i-1")}
		}},
		{"AWS::EC2::SecurityGroup", "sg-1", func(p, r string) target {
			return target{TypeEC2SecurityGroup, ec2ARNIn(p, r, testAccountID, "security-group", "sg-1")}
		}},
		{"AWS::EC2::VPC", "vpc-1", func(p, r string) target {
			return target{TypeEC2VPC, ec2ARNIn(p, r, testAccountID, "vpc", "vpc-1")}
		}},
		{"AWS::EC2::Subnet", "subnet-1", func(p, r string) target {
			return target{TypeEC2Subnet, ec2ARNIn(p, r, testAccountID, "subnet", "subnet-1")}
		}},
		{"AWS::Logs::LogGroup", "/app/lg", func(p, r string) target {
			return target{TypeLogsLogGroup, logGroupARN(sdkaws.String(partitionARN(p, "logs", r, "log-group:/app/lg:*")))}
		}},
		{"AWS::RDS::DBInstance", "d1", func(p, r string) target {
			return target{TypeRDSDBInstance, partitionARN(p, "rds", r, "db:d1")}
		}},
		{"AWS::RDS::DBCluster", "c1", func(p, r string) target {
			return target{TypeRDSDBCluster, partitionARN(p, "rds", r, "cluster:c1")}
		}},
		{"AWS::ApiGateway::RestApi", "a1", func(p, r string) target {
			return target{TypeAPIGatewayRestAPI, apigatewayARNIn(p, r, "restapis", "a1")}
		}},
		{"AWS::ApiGatewayV2::Api", "h1", func(p, r string) target {
			return target{TypeAPIGatewayV2API, apigatewayARNIn(p, r, "apis", "h1")}
		}},
	}
	for _, c := range cases {
		for _, pf := range partitionFixtures {
			t.Run(c.cfnType+"/"+pf.partition, func(t *testing.T) {
				st := newTestStore(t)
				acct := partitionAccount(pf.partition)
				tgt := c.target(acct.partition(), pf.region)
				if !strings.HasPrefix(tgt.nativeID, "arn:"+pf.partition+":") {
					t.Fatalf("target NativeID %s is not in %s", tgt.nativeID, pf.partition)
				}
				tgtID := upsertTestResource(t, st, "aws", acct.ID, tgt.typ, tgt.nativeID, pf.region, "{}")
				stack := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack,
					partitionARN(pf.partition, "cloudformation", pf.region, "stack/s1/uuid-1"), pf.region,
					mustJSON(map[string]any{"Resources": []cfntypes.StackResourceSummary{{
						LogicalResourceId: ptrStr("R"), PhysicalResourceId: ptrStr(c.physID),
						ResourceType: ptrStr(c.cfnType), ResourceStatus: cfntypes.ResourceStatusCreateComplete,
					}}}))
				if err := resolveCloudFormationStackResources(acct, st); err != nil {
					t.Fatalf("resolveCloudFormationStackResources: %v", err)
				}
				assertEdgeTargets(t, st, stack, tgtID)
			})
		}
	}
}

// The instance's NativeID is the DBInstanceArn RDS returns.
func TestRDSARNIn_ResolverUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			db := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBInstance,
				partitionARN(pf.partition, "rds", pf.region, "db:d1"), pf.region,
				mustJSON(rdstypes.DBInstance{DBClusterIdentifier: ptrStr("c1")}))
			if err := resolveRDSInstanceRelationships(acct, st); err != nil {
				t.Fatalf("resolveRDSInstanceRelationships: %v", err)
			}
			assertEdgeTargets(t, st, db, store.ResourceID("aws", acct.ID, partitionARN(pf.partition, "rds", pf.region, "cluster:c1")))
		})
	}
}

func TestAPIGatewayARNIn_ResolverUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayRestAPI,
				apigatewayARNIn(acct.partition(), pf.region, "restapis", "a1"), pf.region, "{}")
			stage := upsertTestResource(t, st, "aws", acct.ID, TypeAPIGatewayStage,
				apigatewayARNIn(acct.partition(), pf.region, "restapis", "a1", "stages", "prod"), pf.region,
				mustJSON(apigatewaytypes.Stage{ClientCertificateId: ptrStr("cc1")}))
			if err := resolveAPIGatewayStageRelationships(acct, st); err != nil {
				t.Fatalf("resolveAPIGatewayStageRelationships: %v", err)
			}
			want := "arn:" + pf.partition + ":apigateway:" + pf.region + "::/clientcertificates/cc1"
			assertEdgeTargets(t, st, stage, store.ResourceID("aws", acct.ID, want))
		})
	}
}

// The subscription's NativeID comes from dsDirectoryARN, the builder the
// Directory Service scanner uses.
func TestLogGroupARNIn_DSLogSubscriptionUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			group := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup,
				partitionARN(pf.partition, "logs", pf.region, "log-group:/aws/ds/d-1"), pf.region, "{}")
			sub := upsertTestResource(t, st, "aws", acct.ID, TypeDSLogSubscription,
				dsDirectoryARN(pf.partition, pf.region, acct.ID, "d-1")+"/log-subscription//aws/ds/d-1", pf.region,
				mustJSON(dstypes.LogSubscription{DirectoryId: ptrStr("d-1"), LogGroupName: ptrStr("/aws/ds/d-1")}))
			if err := resolveDSLogSubscriptions(acct, st); err != nil {
				t.Fatalf("resolveDSLogSubscriptions: %v", err)
			}
			assertEdgeTargets(t, st, sub, group)
		})
	}
}

func TestMacieSessionNativeIDIn_ScannerUsesAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			client := macie2.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"GetMacieSession": {{Output: &macie2.GetMacieSessionOutput{Status: macie2types.MacieStatusEnabled}}},
			}), pf.region))
			if _, _, _, err := scanMacieSession(context.Background(), client, partitionAccount(pf.partition), pf.region, st, testScanID); err != nil {
				t.Fatalf("scanMacieSession: %v", err)
			}
			if got, want := onlyRow(t, st, TypeMacieSession).NativeID, partitionARN(pf.partition, "macie2", pf.region, "session"); got != want {
				t.Errorf("session NativeID = %s; want %s", got, want)
			}
		})
	}
}

// Users, groups and memberships come from the real scanner; the membership
// resolver must reach the user and group rows it stored.
func TestIdentityStoreNativeIDIn_MembershipEdgesUseAccountPartition(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			client := identitystore.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
				"ListUsers": func(any) (any, error) {
					return &identitystore.ListUsersOutput{Users: []istypes.User{{UserId: ptrStr("u-1"), IdentityStoreId: ptrStr("d-1")}}}, nil
				},
				"ListGroups": func(any) (any, error) {
					return &identitystore.ListGroupsOutput{Groups: []istypes.Group{{GroupId: ptrStr("g-1"), IdentityStoreId: ptrStr("d-1")}}}, nil
				},
				"ListGroupMemberships": func(any) (any, error) {
					return &identitystore.ListGroupMembershipsOutput{GroupMemberships: []istypes.GroupMembership{{
						MembershipId: ptrStr("m-1"), GroupId: ptrStr("g-1"), IdentityStoreId: ptrStr("d-1"),
						MemberId: &istypes.MemberIdMemberUserId{Value: "u-1"},
					}}}, nil
				},
			}, errUnstubbedOpDenied))
			instances := []ssotypes.InstanceMetadata{{
				InstanceArn:     sdkaws.String("arn:" + pf.partition + ":sso:::instance/ssoins-1"),
				IdentityStoreId: ptrStr("d-1"), OwnerAccountId: ptrStr(testAccountID),
			}}
			if _, _, err := scanIdentityStoreUsersGroups(context.Background(), client, acct, pf.region, instances, st, testScanID); err != nil {
				t.Fatalf("scanIdentityStoreUsersGroups: %v", err)
			}
			user := onlyRow(t, st, TypeIdentityStoreUser)
			group := onlyRow(t, st, TypeIdentityStoreGroup)
			for got, want := range map[string]string{
				user.NativeID:  "arn:" + pf.partition + ":identitystore::" + testAccountID + ":user/d-1/u-1",
				group.NativeID: "arn:" + pf.partition + ":identitystore::" + testAccountID + ":group/d-1/g-1",
			} {
				if got != want {
					t.Errorf("NativeID = %s; want %s", got, want)
				}
			}
			if err := resolveIdentityStoreGroupMembershipRefs(acct, st); err != nil {
				t.Fatalf("resolveIdentityStoreGroupMembershipRefs: %v", err)
			}
			assertEdgeTargets(t, st, onlyRow(t, st, TypeIdentityStoreGroupMembership).ID, user.ID, group.ID)
		})
	}
}

// Both rows come from their real scanners, and the key store's synthetic
// NativeID need not share the cluster's partition for the edge to form.
func TestCloudHSMClusterARNIn_KMSKeyStoreReachesScannedCluster(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			hsm := cloudhsmv2.NewFromConfig(opStubConfig(map[string]func(any) (any, error){
				"DescribeClusters": func(any) (any, error) {
					return &cloudhsmv2.DescribeClustersOutput{Clusters: []cloudhsmv2types.Cluster{{ClusterId: ptrStr("cluster-1")}}}, nil
				},
			}, errUnstubbedOpDenied))
			if _, _, err := scanCloudHSMClusters(context.Background(), hsm, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanCloudHSMClusters: %v", err)
			}
			cluster := onlyRow(t, st, TypeCloudHSMCluster)
			if want := partitionARN(pf.partition, "cloudhsm", pf.region, "cluster/cluster-1"); cluster.NativeID != want {
				t.Errorf("cluster NativeID = %s; want %s", cluster.NativeID, want)
			}
			kmsClient := newKMSTestClient(map[string]func(any) (any, error){
				"DescribeCustomKeyStores": func(any) (any, error) {
					return &kms.DescribeCustomKeyStoresOutput{CustomKeyStores: []kmstypes.CustomKeyStoresListEntry{{
						CustomKeyStoreId: ptrStr("cks-1"), CloudHsmClusterId: ptrStr("cluster-1"),
					}}}, nil
				},
			})
			if _, _, err := scanKMSCustomKeyStores(context.Background(), kmsClient, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanKMSCustomKeyStores: %v", err)
			}
			if err := resolveKMSCustomKeyStoreBackends(acct, st); err != nil {
				t.Fatalf("resolveKMSCustomKeyStoreBackends: %v", err)
			}
			assertEdgeTargets(t, st, onlyRow(t, st, TypeKMSCustomKeyStore).ID, cluster.ID)
		})
	}
}

// Both rows come from their real scanners; the classic load balancer's
// synthetic NativeID need not share the VPC's partition for the edge to form.
func TestEC2ARNIn_ClassicELBReachesScannedVPC(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			ec2Client := ec2.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"DescribeVpcs": {{Output: &ec2.DescribeVpcsOutput{Vpcs: []ec2types.Vpc{{VpcId: ptrStr("vpc-1")}}}}},
			}), pf.region))
			if _, _, err := scanVPCs(context.Background(), ec2Client, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanVPCs: %v", err)
			}
			elbClient := elasticloadbalancing.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
				"DescribeLoadBalancers": {{Output: &elasticloadbalancing.DescribeLoadBalancersOutput{
					LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{{LoadBalancerName: ptrStr("lb-1"), VPCId: ptrStr("vpc-1")}},
				}}},
				"DescribeTags": {{Output: &elasticloadbalancing.DescribeTagsOutput{
					TagDescriptions: []elbtypes.TagDescription{{LoadBalancerName: ptrStr("lb-1")}},
				}}},
			}), pf.region))
			if _, _, err := scanELBClassicLoadBalancers(context.Background(), elbClient, acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanELBClassicLoadBalancers: %v", err)
			}
			if err := resolveELBClassicRelationships(acct, st); err != nil {
				t.Fatalf("resolveELBClassicRelationships: %v", err)
			}
			assertEdgeTargets(t, st, onlyRow(t, st, TypeELBClassicLoadBalancer).ID, onlyRow(t, st, TypeEC2VPC).ID)
		})
	}
}
