package aws

import (
	"testing"
)

// TestPartitionARNBuilders_CommercialUnchanged pins that every partition-taking
// core, given "aws", produces exactly the string the builder produced before
// partitions existed — those strings are NativeIDs, and a changed byte gives
// every existing commercial row a new ResourceID. The aws-cn column proves the
// partition argument reaches the output instead of being ignored.
func TestPartitionARNBuilders_CommercialUnchanged(t *testing.T) {
	const acct, region = "123456789012", "us-east-1"
	cases := []struct {
		name       string
		build      func(partition string) string
		legacy     string // the wrapper still used by every caller
		commercial string // the pre-partition literal form
		china      string
	}{
		{
			"ec2", func(p string) string { return ec2ARNIn(p, region, acct, "instance", "i-1") },
			ec2ARN(region, acct, "instance", "i-1"),
			"arn:aws:ec2:us-east-1:123456789012:instance/i-1",
			"arn:aws-cn:ec2:us-east-1:123456789012:instance/i-1",
		},
		{
			"rds", func(p string) string { return rdsARNIn(p, region, acct, "cluster", "c1") },
			rdsARN(region, acct, "cluster", "c1"),
			"arn:aws:rds:us-east-1:123456789012:cluster:c1",
			"arn:aws-cn:rds:us-east-1:123456789012:cluster:c1",
		},
		{
			"apigateway", func(p string) string { return apigatewayARNIn(p, region, "restapis", "a1") },
			apigatewayARN(region, "restapis", "a1"),
			"arn:aws:apigateway:us-east-1::/restapis/a1",
			"arn:aws-cn:apigateway:us-east-1::/restapis/a1",
		},
		{
			"apigateway empty path", func(p string) string { return apigatewayARNIn(p, region) },
			apigatewayARN(region),
			"arn:aws:apigateway:us-east-1::/",
			"arn:aws-cn:apigateway:us-east-1::/",
		},
		{
			"cloudhsm", func(p string) string { return cloudhsmClusterARNIn(p, region, acct, "cluster-1") },
			cloudhsmClusterARN(region, acct, "cluster-1"),
			"arn:aws:cloudhsm:us-east-1:123456789012:cluster/cluster-1",
			"arn:aws-cn:cloudhsm:us-east-1:123456789012:cluster/cluster-1",
		},
		{
			"log group", func(p string) string { return logGroupARNIn(p, acct, region, "/aws/lambda/f") },
			logGroupNativeIDFromName(acct, region, "/aws/lambda/f"),
			"arn:aws:logs:us-east-1:123456789012:log-group:/aws/lambda/f",
			"arn:aws-cn:logs:us-east-1:123456789012:log-group:/aws/lambda/f",
		},
		{
			"macie session", func(p string) string { return macieSessionNativeIDIn(p, acct, region) },
			macieSessionNativeID(acct, region),
			"arn:aws:macie2:us-east-1:123456789012:session",
			"arn:aws-cn:macie2:us-east-1:123456789012:session",
		},
		{
			"identity store user", func(p string) string { return identityStoreUserNativeIDIn(p, acct, "d-1", "u-1") },
			identityStoreUserNativeID(acct, "d-1", "u-1"),
			"arn:aws:identitystore::123456789012:user/d-1/u-1",
			"arn:aws-cn:identitystore::123456789012:user/d-1/u-1",
		},
		{
			"identity store group", func(p string) string { return identityStoreGroupNativeIDIn(p, acct, "d-1", "g-1") },
			identityStoreGroupNativeID(acct, "d-1", "g-1"),
			"arn:aws:identitystore::123456789012:group/d-1/g-1",
			"arn:aws-cn:identitystore::123456789012:group/d-1/g-1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.build("aws"); got != c.commercial {
				t.Errorf("core(aws) = %q; want %q", got, c.commercial)
			}
			if c.legacy != c.commercial {
				t.Errorf("wrapper = %q; want %q", c.legacy, c.commercial)
			}
			if got := c.build("aws-cn"); got != c.china {
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

// TestEC2ARN verifies EC2's NativeID ARN format. NativeID feeds
// store.ResourceID, so a format change silently breaks all relationships
// referencing EC2 resources.
func TestEC2ARN(t *testing.T) {
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
		got := ec2ARN(tc.region, tc.account, tc.rtype, tc.id)
		if got != tc.want {
			t.Errorf("ec2ARN(%q,%q,%q,%q) = %q, want %q",
				tc.region, tc.account, tc.rtype, tc.id, got, tc.want)
		}
	}
}

func TestRDSARN(t *testing.T) {
	got := rdsARN("us-east-1", "123456789012", "db", "mydb")
	want := "arn:aws:rds:us-east-1:123456789012:db:mydb"
	if got != want {
		t.Errorf("rdsARN = %q, want %q", got, want)
	}
}

func TestApigatewayARN(t *testing.T) {
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
			if got := apigatewayARN("us-east-1", tt.in...); got != tt.want {
				t.Errorf("apigatewayARN = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLogGroupNativeIDFromName(t *testing.T) {
	got := logGroupNativeIDFromName("123456789012", "us-east-1", "/aws/lambda/foo")
	want := "arn:aws:logs:us-east-1:123456789012:log-group:/aws/lambda/foo"
	if got != want {
		t.Errorf("logGroupNativeIDFromName = %q, want %q", got, want)
	}
}

func TestMacieSessionNativeID(t *testing.T) {
	got := macieSessionNativeID("123456789012", "us-east-1")
	want := "arn:aws:macie2:us-east-1:123456789012:session"
	if got != want {
		t.Errorf("macieSessionNativeID = %q, want %q", got, want)
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

func TestIdentityStoreNativeIDs(t *testing.T) {
	if got, want := identityStoreUserNativeID("123456789012", "d-9067", "u-1"),
		"arn:aws:identitystore::123456789012:user/d-9067/u-1"; got != want {
		t.Errorf("identityStoreUserNativeID = %q, want %q", got, want)
	}
	if got, want := identityStoreGroupNativeID("123456789012", "d-9067", "g-1"),
		"arn:aws:identitystore::123456789012:group/d-9067/g-1"; got != want {
		t.Errorf("identityStoreGroupNativeID = %q, want %q", got, want)
	}
}
