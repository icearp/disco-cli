package aws

import "testing"

// TestEC2ARN verifies the ARN format used as NativeID for EC2 resources.
// The NativeID feeds into store.ResourceID, so any format change silently
// breaks all relationships that reference EC2 resources.
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
